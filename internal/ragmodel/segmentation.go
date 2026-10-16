package ragmodel

import (
	"strings"
	"sync"
	"unicode"

	"github.com/go-ego/gse"
)

var (
	seg     gse.Segmenter
	segOnce sync.Once
	segOk   bool
)

func initSeg() {
	var err error
	seg, err = gse.New("zh", "alpha")
	if err != nil {
		seg, err = gse.New()
	}
	if err == nil {
		// Tech terms — prevent over-splitting
		for _, t := range techTerms {
			seg.AddToken(t, 1)
		}
		segOk = true
	}
}

var techTerms = []string{
	"大模型", "机器学习", "深度学习", "人工智能",
	"自然语言处理", "向量检索", "知识图谱", "神经网络",
	"强化学习", "迁移学习", "大语言模型", "检索增强生成",
	"负载均衡", "反向代理", "微服务", "容器化",
	"Transformer", "GPT", "BERT", "LLM", "RAG",
	"Qwen-VL", "AutoGPTQ", "Gradio", "React",
	"SSE", "WebSocket", "Nginx", "Docker", "Kubernetes",
	"BM25", "Bleve", "FTS5", "SQLite",
}

// synonymMap provides query-time synonym expansion.
var synonymMap = map[string][]string{
	"大模型":    {"LLM", "大语言模型", "语言模型"},
	"LLM":    {"大模型", "大语言模型"},
	"RAG":    {"检索增强生成", "知识库检索"},
	"向量":     {"embedding", "特征向量"},
	"Nginx":  {"反向代理", "Web服务器"},
	"Docker": {"容器", "容器化"},
	"GPU":    {"显卡", "CUDA"},
	"API":    {"接口", "REST"},
	"SSE":    {"Server-Sent Events", "服务端推送"},
	"负载均衡":   {"load balancing", "反向代理"},
	"微服务":    {"microservice", "服务拆分"},
	"客服":     {"客户服务", "智能客服", "数字员工"},
	"部署":     {"上线", "发布", "deploy"},
	"Python": {"python", "py"},
	"React":  {"react.js", "前端框架"},
}

// gojiebaCut uses CutAll mode — best for indexing (broad coverage).
func gojiebaCut(text string) []string {
	segOnce.Do(initSeg)
	if !segOk {
		return nil
	}
	words := seg.CutAll(text)
	return trimWords(words)
}

// gojiebaCutSearch uses Cut mode (precise) — best for queries.
func gojiebaCutSearch(text string) []string {
	segOnce.Do(initSeg)
	if !segOk {
		return nil
	}
	words := seg.Cut(text, true)
	return trimWords(words)
}

func trimWords(words []string) []string {
	result := make([]string, 0, len(words))
	for _, w := range words {
		w = strings.TrimSpace(w)
		if w != "" {
			result = append(result, w)
		}
	}
	return result
}

// cjkBigrams generates character-level bigrams for CJK text.
// Used as fallback when gse produces mostly single-char tokens (OOV terms).
func cjkBigrams(text string) []string {
	runes := []rune(text)
	var out []string
	for i := 0; i < len(runes)-1; i++ {
		if isCJK(runes[i]) && isCJK(runes[i+1]) {
			out = append(out, string(runes[i:i+2]))
		}
	}
	// Also keep single chars that are letters/digits (English/numbers in mixed text)
	for _, r := range runes {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			out = append(out, string(r))
		}
	}
	return out
}

// expandSynonyms adds synonym tokens to the keyword list.
func expandSynonyms(tokens []string) []string {
	seen := map[string]bool{}
	var result []string
	for _, t := range tokens {
		if !seen[t] {
			result = append(result, t)
			seen[t] = true
		}
		if syns, ok := synonymMap[t]; ok {
			for _, s := range syns {
				if !seen[s] {
					result = append(result, s)
					seen[s] = true
				}
			}
		}
		// Also check lowercase
		low := strings.ToLower(t)
		if low != t {
			if syns, ok := synonymMap[low]; ok {
				for _, s := range syns {
					if !seen[s] {
						result = append(result, s)
						seen[s] = true
					}
				}
			}
		}
	}
	return result
}

// segTextForIndex tokenizes text for Bleve indexing (broad recall).
func segTextForIndex(text string) string {
	return segTextCore(text, gojiebaCut)
}

// segTextForSearch tokenizes text for search queries (better precision).
// Also applies token merging (adjacent CJK singles → bigram) and term weighting.
func segTextForSearch(text string) string {
	tokens := segTextCore(text, gojiebaCutSearch)
	return mergeAdjacentTokens(tokens)
}

// segText is the default tokenizer used for indexing (backwards compatible).
func segText(text string) string {
	return segTextForIndex(text)
}

// mergeAdjacentTokens combines adjacent single-char CJK tokens into bigrams.
// Inspired by RAGFlow's token_merge: "多" + "工位" → "多工位".
// This recovers phrases that gse over-splits.
func mergeAdjacentTokens(tokens string) string {
	parts := strings.Fields(tokens)
	if len(parts) < 2 {
		return tokens
	}

	var merged []string
	i := 0
	for i < len(parts) {
		// Check if current and next are single CJK chars that could form a word
		if i+1 < len(parts) && isSingleCJK(parts[i]) && !isSingleCJK(parts[i+1]) && len([]rune(parts[i+1])) >= 2 {
			// Merge single char with following multi-char token
			merged = append(merged, parts[i]+parts[i+1])
			i += 2
			continue
		}
		// Two adjacent single CJK chars → merge into bigram
		if i+1 < len(parts) && isSingleCJK(parts[i]) && isSingleCJK(parts[i+1]) {
			merged = append(merged, parts[i]+parts[i+1])
			i += 2
			continue
		}
		merged = append(merged, parts[i])
		i++
	}
	return strings.Join(merged, " ")
}

func isSingleCJK(s string) bool {
	runes := []rune(s)
	return len(runes) == 1 && isCJK(runes[0])
}

// tokenWeights returns per-token importance weights.
// Inspired by RAGFlow's term_weight: longer terms, numbers, mixed-case get higher weight.
// Weights are applied at Bleve query level via DisjunctionQuery.
func tokenWeights(tokens []string) []float64 {
	weights := make([]float64, len(tokens))
	for i, t := range tokens {
		weights[i] = tokenWeight(t)
	}
	return weights
}

// tokenWeight computes importance score for a single token.
// Higher = more important in search matching.
func tokenWeight(tk string) float64 {
	runes := []rune(tk)

	// Numbers → important (dates, versions, ports)
	hasDigit := false
	hasLetter := false
	hasUpper := false
	hasLower := false
	for _, r := range runes {
		if r >= '0' && r <= '9' {
			hasDigit = true
		}
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
			hasLetter = true
			if r >= 'A' && r <= 'Z' {
				hasUpper = true
			} else {
				hasLower = true
			}
		}
	}

	// Base weight by length
	weight := 1.0
	if len(runes) >= 4 {
		weight = 1.5 // longer tokens are more specific
	} else if len(runes) >= 3 {
		weight = 1.2
	}

	// Mixed case (CamelCase, PascalCase) → likely named entity
	if hasUpper && hasLower {
		weight += 0.5
	}

	// Alphanumeric (version numbers, error codes) → important
	if hasDigit && hasLetter {
		weight += 0.3
	}

	// Pure number with digits → moderate
	if hasDigit && !hasLetter && len(runes) >= 2 {
		weight = 1.3
	}

	// Single char → low weight (likely noise)
	if len(runes) == 1 {
		weight = 0.5
	}

	return weight
}

func segTextCore(text string, cutter func(string) []string) string {
	if hasCJK(text) {
		words := cutter(text)
		if len(words) > 0 {
			var keep []string
			shortCount := 0
			for _, w := range words {
				w = strings.TrimSpace(w)
				if w == "" || isPurePunct(w) || stopTokens[w] || stopTokens[strings.ToLower(w)] {
					continue
				}
				keep = append(keep, w)
				if len([]rune(w)) < 2 {
					shortCount++
				}
			}

			// Bigram fallback: if >60% of tokens are single-char (OOV), add bigrams
			if len(keep) > 0 && shortCount*10/len(keep) > 6 {
				bigrams := cjkBigrams(text)
				for _, bg := range bigrams {
					if !stopTokens[bg] && !isPurePunct(bg) {
						keep = append(keep, bg)
					}
				}
			}

			// Synonym expansion for search queries only
			keep = expandSynonyms(keep)

			if len(keep) > 0 {
				return strings.Join(keep, " ")
			}
		}
	}
	// English / fallback
	parts := strings.Fields(strings.ToLower(text))
	var keep []string
	for _, p := range parts {
		if !stopTokens[p] && !isPurePunct(p) {
			keep = append(keep, p)
		}
	}
	if len(keep) > 0 {
		return strings.Join(keep, " ")
	}
	return strings.ToLower(strings.TrimSpace(text))
}

// Exported compatibility seams keep callers independent from the segmenter
// implementation while the legacy root package is being decomposed.
func GoJiebaCut(text string) []string       { return gojiebaCut(text) }
func GoJiebaCutSearch(text string) []string { return gojiebaCutSearch(text) }
func SegTextForIndex(text string) string    { return segTextForIndex(text) }
func SegTextForSearch(text string) string   { return segTextForSearch(text) }
func SegText(text string) string            { return segText(text) }
func FilterTokens(tokens []string) []string { return filterTokens(tokens) }
func TokenWeights(tokens []string) []float64 {
	return tokenWeights(tokens)
}
