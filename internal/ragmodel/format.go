package ragmodel

import (
	"fmt"
	"regexp"
	"strings"
)

// SearchResult is the provider-neutral view used by RAG presentation helpers.
type SearchResult struct {
	SourceDoc     string
	ChunkIndex    int
	Content       string
	Score         float64
	MatchedTokens []string
}

// FormatRAGResults formats search results as LLM-ready citations.
// The output includes source attribution and is optimized for prompt injection.
func FormatRAGResults(results []SearchResult) string {
	if len(results) == 0 {
		return "知识库中未找到相关内容。"
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("从知识库检索到 %d 条相关内容：\n\n", len(results)))

	for i, r := range results {
		// Trim content to a reasonable size for context window
		content := r.Content
		if len([]rune(content)) > 500 {
			runes := []rune(content)
			// Cut at last sentence boundary within 500 chars
			cut := 500
			for j := 500; j > 350; j-- {
				if runes[j] == '。' || runes[j] == '.' || runes[j] == '！' || runes[j] == '?' || runes[j] == '\n' {
					cut = j + 1
					break
				}
			}
			content = string(runes[:cut]) + "..."
		}

		scorePct := int(r.Score / 10 * 100)
		if scorePct > 100 {
			scorePct = 100
		}
		if scorePct < 0 {
			scorePct = 0
		}

		sb.WriteString(fmt.Sprintf("--- 结果 #%d (相关度: %d%%) ---\n", i+1, scorePct))
		sb.WriteString(fmt.Sprintf("来源文档: %s\n", r.SourceDoc))
		sb.WriteString(content + "\n\n")
	}

	sb.WriteString("请基于以上检索结果回答用户问题，并在回答中引用来源文档名称。")
	return sb.String()
}

// FormatRAGResultsCompact is a denser format for limited context windows.
// FormatSourceFooter returns a citation footer string.
func FormatSourceFooter(results []SearchResult) string {
	seen := map[string]bool{}
	var docs []string
	for _, r := range results {
		if r.SourceDoc != "" && !seen[r.SourceDoc] {
			seen[r.SourceDoc] = true
			docs = append(docs, r.SourceDoc)
		}
	}
	if len(docs) == 0 {
		return ""
	}
	return "\n\n[Sources: " + strings.Join(docs, ", ") + "]"
}

func FormatRAGResultsCompact(results []SearchResult) string {
	if len(results) == 0 {
		return ""
	}

	var sb strings.Builder
	for i, r := range results {
		content := r.Content
		if len([]rune(content)) > 300 {
			content = string([]rune(content)[:300]) + "..."
		}
		sb.WriteString(fmt.Sprintf("[%d] {%s} %s\n", i+1, r.SourceDoc, content))
	}
	return sb.String()
}

// TruncateToSentence cuts text at the last sentence boundary before maxLen.
func TruncateToSentence(text string, maxLen int) string {
	runes := []rune(text)
	if len(runes) <= maxLen {
		return text
	}
	for i := maxLen; i > maxLen/2; i-- {
		if runes[i] == '。' || runes[i] == '.' || runes[i] == '！' || runes[i] == '\n' {
			return string(runes[:i+1]) + "..."
		}
	}
	return string(runes[:maxLen]) + "..."
}

// ====================== Highlighting (inspired by GoFound) ======================

// HighlightMatches wraps matched tokens in content with <mark> tags.
// tokens: space-separated keywords from gse tokenization.
func HighlightMatches(content, tokens string) string {
	if tokens == "" || content == "" {
		return content
	}

	// Build a set of unique tokens to highlight
	kwSet := map[string]bool{}
	for _, t := range strings.Fields(tokens) {
		t = strings.TrimSpace(t)
		if len([]rune(t)) >= 2 {
			kwSet[strings.ToLower(t)] = true
		}
	}
	if len(kwSet) == 0 {
		return content
	}

	// Build regex from keywords, longest first to avoid partial matches
	var kwList []string
	for k := range kwSet {
		kwList = append(kwList, regexp.QuoteMeta(k))
	}
	// Sort by length descending for proper matching
	sortByLen(kwList)

	pattern := "(?i)(" + strings.Join(kwList, "|") + ")"
	re := regexp.MustCompile(pattern)

	// Replace with <mark> wrapping, tracking already-replaced positions
	return re.ReplaceAllStringFunc(content, func(match string) string {
		return "<mark>" + match + "</mark>"
	})
}

func sortByLen(s []string) {
	for i := 0; i < len(s); i++ {
		for j := i + 1; j < len(s); j++ {
			if len(s[j]) > len(s[i]) {
				s[i], s[j] = s[j], s[i]
			}
		}
	}
}

// ExtractMatchSnippet returns a window of text around the first match.
func ExtractMatchSnippet(content, tokens string, window int) string {
	if tokens == "" || content == "" {
		runes := []rune(content)
		if len(runes) > window {
			return string(runes[:window]) + "..."
		}
		return content
	}

	lower := strings.ToLower(content)
	bestPos := -1

	for _, t := range strings.Fields(tokens) {
		t = strings.TrimSpace(t)
		if len([]rune(t)) < 2 {
			continue
		}
		pos := strings.Index(lower, strings.ToLower(t))
		if pos >= 0 && (bestPos < 0 || pos < bestPos) {
			bestPos = pos
		}
	}

	if bestPos < 0 {
		runes := []rune(content)
		if len(runes) > window {
			return string(runes[:window]) + "..."
		}
		return content
	}

	// Convert byte position to rune position for clean slicing
	prefix := []rune(content[:bestPos])
	runes := []rune(content)
	start := len(prefix) - window/3
	if start < 0 {
		start = 0
	}
	end := start + window
	if end > len(runes) {
		end = len(runes)
	}

	snippet := string(runes[start:end])
	if start > 0 {
		snippet = "..." + snippet
	}
	if end < len(runes) {
		snippet = snippet + "..."
	}
	return HighlightMatches(snippet, tokens)
}

// FormatRAGResultsHighlighted formats results with HTML highlighting for web UI.
func FormatRAGResultsHighlighted(results []SearchResult, tokens string) string {
	if len(results) == 0 {
		return "未找到相关内容。"
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("找到 %d 条相关结果：\n\n", len(results)))

	for i, r := range results {
		scorePct := int(r.Score / 10 * 100)
		if scorePct > 100 {
			scorePct = 100
		}
		if scorePct < 0 {
			scorePct = 0
		}

		// Extract a snippet around the first match
		snippet := ExtractMatchSnippet(r.Content, tokens, 300)

		sb.WriteString(fmt.Sprintf("--- 结果 #%d (相关度: %d%%) ---\n", i+1, scorePct))
		sb.WriteString(fmt.Sprintf("来源: %s\n", r.SourceDoc))
		sb.WriteString(snippet + "\n\n")
	}
	return sb.String()
}
