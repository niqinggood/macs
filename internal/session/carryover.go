package session

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	middlewarecore "mac/internal/middleware"
	"mac/internal/textnormalize"
	"mac/internal/textutil"
)

type SessionContextChatMessage struct {
	Role    string `gorm:"size:32"`
	Content string `gorm:"type:text"`
}

func CleanCarryoverContent(role, content string, maxRunes int) string {
	role = strings.ToLower(strings.TrimSpace(role))
	if role == "system" || role == "tool" {
		return ""
	}
	if role == "user" {
		content = stripRuntimeContextPrefix(content)
	}

	// Session carryover is deliberately less destructive than the final-answer
	// cleaner. In particular, users may be discussing tool-call syntax and both
	// roles may contain legitimate examples inside Markdown fences. Stored chat
	// rows remain untouched; this copy is only used as model context.
	cleaned := strings.TrimSpace(textutil.Clean(content))
	if role == "assistant" {
		if hasUnexecutedToolTranscript(content) {
			// A provider/model transcript that merely *claims* a tool call has
			// no execution receipt. Never let it become trusted conversation
			// history on the next turn.
			return ""
		}
		cleaned = strings.TrimSpace(textutil.Clean(stripToolMarkupOutsideMarkdownFences(textnormalize.ModelText(content))))
	}
	if cleaned == "" {
		return ""
	}
	if LooksLikeHardInternalContent(cleaned) {
		return ""
	}
	cleaned = stripSessionInternalLines(cleaned)
	cleaned = strings.TrimSpace(cleaned)
	if cleaned == "" {
		return ""
	}
	if role == "assistant" && LooksLikeInternalContent(cleaned) {
		return ""
	}
	return truncateRunes(cleaned, maxRunes)
}

func LooksLikeInternalContent(content string) bool {
	lower := strings.ToLower(strings.TrimSpace(content))
	if lower == "" {
		return true
	}
	if LooksLikeHardInternalContent(content) {
		return true
	}
	if looksLikeInternalToolReviewOutput(lower) {
		return true
	}
	return (strings.HasPrefix(lower, "\u672a\u80fd\u751f\u6210\u53ef\u7528\u6700\u7ec8\u56de\u7b54") ||
		strings.HasPrefix(lower, "\u4efb\u52a1\u8fbe\u5230\u8fed\u4ee3\u4e0a\u9650")) &&
		strings.Contains(lower, "## \u6267\u884c\u8bca\u65ad")
}

func LooksLikeHardInternalContent(content string) bool {
	lower := strings.ToLower(strings.TrimSpace(content))
	if lower == "" {
		return true
	}
	if looksLikeSubAgentInternalContent(lower) {
		return true
	}
	if strings.HasPrefix(lower, "[internal control \u2014 ") || strings.HasPrefix(lower, "[internal control - ") {
		return true
	}
	if (strings.HasPrefix(lower, "[auto verification required]") ||
		strings.HasPrefix(lower, "auto verification required\n")) &&
		strings.Contains(lower, "original user request:") &&
		strings.Contains(lower, "verify_work") {
		return true
	}
	for _, prefix := range []string{
		"[reflection checkpoint - iteration ",
		"[rethink - iteration ",
		"[task stopped - iteration ",
		"[iteration budget exhausted]",
		"[final iteration]",
	} {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	return false
}

func looksLikeSubAgentInternalContent(lower string) bool {
	lower = strings.TrimSpace(lower)
	if lower == "" {
		return false
	}
	if strings.HasPrefix(lower, "you are a sub-agent named \"") &&
		strings.Contains(lower, "\n## parent task context\n") {
		return true
	}
	if strings.HasPrefix(lower, "## sub-agent '") {
		firstLine := lower
		if idx := strings.IndexByte(firstLine, '\n'); idx >= 0 {
			firstLine = firstLine[:idx]
		}
		return strings.HasSuffix(strings.TrimSpace(firstLine), " result")
	}
	if strings.HasPrefix(lower, "## parallel sub-agent results") {
		return true
	}
	return false
}

func stripRuntimeContextPrefix(content string) string {
	normalized := strings.ReplaceAll(content, "\r\n", "\n")
	marker := "\n## User Request\n"
	idx := strings.LastIndex(normalized, marker)
	markerLen := len(marker)
	if idx < 0 && strings.HasPrefix(normalized, "## User Request\n") {
		idx = 0
		markerLen = len("## User Request\n")
	}
	if idx < 0 {
		return content
	}
	prefix := strings.TrimSpace(normalized[:idx])
	if !hasRuntimeContextProvenance(prefix) {
		return content
	}
	return strings.TrimSpace(normalized[idx+markerLen:])
}

func hasRuntimeContextProvenance(prefix string) bool {
	lower := strings.ToLower(strings.TrimSpace(prefix))
	if strings.HasPrefix(lower, "## recent context (from memory)\n") ||
		strings.HasPrefix(lower, "[session context]\n") ||
		strings.HasPrefix(lower, "## session context (persisted across sessions)\n") {
		return true
	}
	if strings.HasPrefix(lower, "## session\n") && strings.Contains(lower, "\ncold data:") {
		return true
	}
	if strings.HasPrefix(lower, "## memory\n") && strings.Contains(lower, "\ncurrent date:") {
		return true
	}
	if strings.HasPrefix(lower, "## matched skills\n") &&
		strings.Contains(lower, "built-in skill metadata is already loaded") {
		return true
	}
	if strings.HasPrefix(lower, "## project\ncurrent workspace:") {
		return true
	}
	return strings.HasPrefix(lower, "## knowledge graph\nknown facts (entity")
}

func stripSessionInternalLines(content string) string {
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	out := make([]string, 0, len(lines))
	inFence := false
	for _, line := range lines {
		if textutil.IsMarkdownFenceBoundary(line) {
			inFence = !inFence
			out = append(out, line)
			continue
		}
		if !inFence && isSessionInternalLine(line) {
			// Runtime reviews/checkpoints are appended as a suffix. Once their
			// fixed header is reached, the remainder belongs to that block.
			break
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

func isSessionInternalLine(line string) bool {
	lower := strings.ToLower(strings.TrimSpace(line))
	if lower == "" {
		return false
	}
	if lower == "tool/skill review" || lower == "tool audit" {
		return true
	}
	for _, prefix := range []string{
		"[internal control \u2014 ",
		"[internal control - ",
		"[reflection checkpoint - iteration ",
		"[rethink - iteration ",
		"[task stopped - iteration ",
		"[iteration budget exhausted]",
		"[final iteration]",
		"[auto verification required]",
		"## \U0001F50D \u5de5\u5177/\u6280\u80fd\u5ba1\u67e5 (\u5df2\u6267\u884c ",
		"## \u5de5\u5177\u6267\u884c\u5ba1\u67e5",
	} {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	return false
}

func stripToolMarkupOutsideMarkdownFences(content string) string {
	normalized := strings.ReplaceAll(content, "\r\n", "\n")
	lines := strings.SplitAfter(normalized, "\n")
	var out strings.Builder
	var outside strings.Builder
	inFence := false
	flushOutside := func() {
		if outside.Len() == 0 {
			return
		}
		out.WriteString(middlewarecore.StripToolCalls(outside.String()))
		outside.Reset()
	}
	for _, line := range lines {
		if textutil.IsMarkdownFenceBoundary(line) {
			if !inFence {
				flushOutside()
			}
			out.WriteString(line)
			inFence = !inFence
			continue
		}
		if inFence {
			out.WriteString(line)
		} else {
			outside.WriteString(line)
		}
	}
	flushOutside()
	return out.String()
}

func BuildSessionContextPromptFromMessages(messages []SessionContextChatMessage) string {
	if len(messages) == 0 {
		return ""
	}

	var lines []string
	artifactSet := make(map[string]bool)
	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		role := strings.ToLower(strings.TrimSpace(msg.Role))
		if role == "assistant" {
			for _, path := range extractArtifactPaths(middlewarecore.StripToolCalls(msg.Content)) {
				artifactSet[path] = true
			}
		}

		cleaned := CleanCarryoverContent(role, msg.Content, 200)
		if cleaned == "" {
			continue
		}
		label := "User"
		if role == "assistant" {
			label = "AI"
		}
		lines = append(lines, fmt.Sprintf("[%s] %s", label, cleaned))
	}

	artifacts := make([]string, 0, len(artifactSet))
	for path := range artifactSet {
		artifacts = append(artifacts, path)
	}
	sort.Strings(artifacts)

	if len(lines) == 0 && len(artifacts) == 0 {
		return ""
	}
	var sb strings.Builder
	if len(lines) > 0 {
		sb.WriteString("## Recent Conversation\n")
		sb.WriteString(strings.Join(lines, "\n"))
	}
	if len(artifacts) > 0 {
		if sb.Len() > 0 {
			sb.WriteString("\n\n")
		}
		sb.WriteString("## Recent Artifacts\n")
		for _, path := range artifacts {
			sb.WriteString("- ")
			sb.WriteString(path)
			sb.WriteString("\n")
		}
	}
	return strings.TrimSpace(sb.String())
}

func hasUnexecutedToolTranscript(content string) bool {
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	inFence := false
	for _, line := range lines {
		if textutil.IsMarkdownFenceBoundary(line) {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		trimmed := strings.TrimSpace(line)
		for {
			before := trimmed
			trimmed = strings.TrimSpace(strings.TrimPrefix(trimmed, ">"))
			trimmed = strings.TrimSpace(strings.TrimPrefix(trimmed, "-"))
			trimmed = strings.TrimSpace(strings.TrimPrefix(trimmed, "*"))
			if trimmed == before {
				break
			}
		}
		lower := strings.ToLower(trimmed)
		for _, prefix := range []string{
			"[tool use:", "[tool use]", "[tool call:", "[tool call]",
			"[tool result:", "[tool result]", "[工具调用:", "[工具调用]",
			"[工具结果:", "[工具结果]",
		} {
			if strings.HasPrefix(lower, prefix) {
				return true
			}
		}
	}
	return false
}

func looksLikeInternalToolReviewOutput(lower string) bool {
	primaryMarker := ""
	for _, marker := range []string{"工具/技能审查", "工具执行审查"} {
		if strings.Contains(lower, marker) {
			primaryMarker = marker
			break
		}
	}
	if primaryMarker == "" || !hasLeadingInternalControlMarker(lower, primaryMarker) {
		return false
	}
	score := 2
	for _, marker := range []string{
		"失败统计", "失败模式", "接近预算", "停止补充信息收集", "生成最终答复",
		"已执行", "当前无不可用工具", "guardrail", "list_dir",
	} {
		if strings.Contains(lower, marker) {
			score++
		}
	}
	return score >= 3
}

func hasLeadingInternalControlMarker(lower, marker string) bool {
	index := strings.Index(lower, marker)
	if index < 0 {
		return false
	}
	prefix := strings.TrimSpace(lower[:index])
	if len([]rune(prefix)) > 48 {
		return false
	}
	prefix = strings.Trim(prefix, "#>*-_[](){}:;.!?'\" \t\r\n")
	if prefix == "" {
		return true
	}
	for _, completion := range []string{"已完成", "完成如下", "结果如下", "completed", "finished"} {
		if strings.Contains(prefix, completion) {
			return false
		}
	}
	return true
}

func extractArtifactPaths(text string) []string {
	if text == "" {
		return nil
	}
	patterns := []*regexp.Regexp{
		regexp.MustCompile(`(?:^|[\s` + "`" + `"'(:，。])((?:[A-Za-z]:)?(?:[/\\][^\s` + "`" + `"')<>]+)*[/\\]\.openkitty[/\\]storage[/\\][^\s` + "`" + `"')<>]+)`),
		regexp.MustCompile(`(?:^|[\s` + "`" + `"'(:，。])((?:\.[/\\])?(?:\.openkitty[/\\]storage|storage)[/\\][^\s` + "`" + `"')<>]+)`),
	}
	seen := make(map[string]bool)
	paths := make([]string, 0)
	for _, pattern := range patterns {
		for _, match := range pattern.FindAllStringSubmatch(text, -1) {
			if len(match) < 2 {
				continue
			}
			path := strings.ReplaceAll(match[1], "\\", "/")
			path = strings.TrimRight(path, ".,;:!?，。；：！？")
			if index := strings.Index(strings.ToLower(path), ".openkitty/storage/"); index >= 0 {
				path = path[index:]
			}
			if strings.HasPrefix(path, "storage/") {
				path = ".openkitty/" + path
			}
			if path != "" && !seen[path] {
				seen[path] = true
				paths = append(paths, path)
			}
		}
	}
	return paths
}

func truncateRunes(s string, maxRunes int) string {
	if maxRunes <= 0 {
		return s
	}
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}
	return string(runes[:maxRunes]) + "..."
}
