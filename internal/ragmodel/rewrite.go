package ragmodel

import (
	"strings"
	"sync"
	"time"
	"unicode"
)

// RAG session context for multi-turn conversation awareness.
var (
	ragSessions   = map[string]*ragSessionCtx{}
	ragSessionsMu sync.RWMutex
)

type ragSessionCtx struct {
	lastKeywords []string
	lastQuery    string
	lastAccess   time.Time
}

// ragSessionTTL is how long a session stays alive without activity.
const ragSessionTTL = 30 * time.Minute

// initSessionCleanup starts a background goroutine to purge expired sessions.
func InitSessionCleanup() {
	go func() {
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			ragSessionsMu.Lock()
			cutoff := time.Now().Add(-ragSessionTTL)
			for id, sess := range ragSessions {
				if sess.lastAccess.Before(cutoff) {
					delete(ragSessions, id)
				}
			}
			ragSessionsMu.Unlock()
		}
	}()
}

// RagEnrichQuery blends the current query with context from previous turns.
// If a session had a recent query, its keywords are merged in for broader recall.
func RagEnrichQuery(sessionID, query string) string {
	if sessionID == "" {
		return query
	}
	ragSessionsMu.RLock()
	sess := ragSessions[sessionID]
	ragSessionsMu.RUnlock()
	if sess != nil {
		sess.lastAccess = time.Now()
	}

	if sess == nil || len(sess.lastKeywords) == 0 {
		return query
	}

	// Merge non-overlapping keywords from previous turn
	currentKW := filterTokens(gojiebaCut(query))
	currentSet := map[string]bool{}
	for _, k := range currentKW {
		currentSet[k] = true
	}

	var extraKW []string
	for _, k := range sess.lastKeywords {
		if !currentSet[k] {
			extraKW = append(extraKW, k)
		}
	}

	if len(extraKW) > 0 {
		// Limit to 3 extra keywords to avoid topic drift
		if len(extraKW) > 3 {
			extraKW = extraKW[:3]
		}
		query = query + " " + strings.Join(extraKW, " ")
	}

	return query
}

// RagRememberQuery stores a query's keywords for the next round.
func RagRememberQuery(sessionID, query string) {
	if sessionID == "" {
		return
	}
	ragSessionsMu.Lock()
	defer ragSessionsMu.Unlock()

	tokens := filterTokens(gojiebaCut(query))
	if len(tokens) > 5 {
		tokens = tokens[:5]
	}

	ragSessions[sessionID] = &ragSessionCtx{
		lastKeywords: tokens,
		lastQuery:    query,
		lastAccess:   time.Now(),
	}
}

// RagClearSession removes a session from context tracking.
func RagClearSession(sessionID string) {
	ragSessionsMu.Lock()
	delete(ragSessions, sessionID)
	ragSessionsMu.Unlock()
}

// QueryType classifies the user's search intent.
type QueryType int

const (
	QueryKeyword  QueryType = iota // short keywords: "大模型 部署"
	QueryQuestion                  // question form: "什么是大模型？"
	QueryLong                      // long descriptive: "我想在K8s上部署..."
	QueryDefault                   // standard
)

// ClassifyQuery determines the query type and returns the best search strategy.
func ClassifyQuery(query string) QueryType {
	query = strings.TrimSpace(query)
	runes := []rune(query)

	// Question patterns
	questionWords := []string{"什么是", "什么叫", "怎么", "如何", "请问", "怎样", "为什么", "告诉我", "解释一下"}
	for _, w := range questionWords {
		if strings.HasPrefix(query, w) {
			return QueryQuestion
		}
	}

	// Short keyword: ≤12 chars, no question marks
	if len(runes) <= 12 {
		hasQMark := strings.ContainsAny(query, "？?吗呢")
		if !hasQMark {
			return QueryKeyword
		}
	}

	// Long form
	if len(runes) > 40 {
		return QueryLong
	}

	return QueryDefault
}

// RewriteQuery preprocesses a user query to improve search recall.
// Strategy varies by query type.
func RewriteQuery(query string) string {
	query = strings.TrimSpace(query)
	if query == "" {
		return query
	}

	// Normalize first
	query = normalizeQuery(query)

	qt := ClassifyQuery(query)

	switch qt {
	case QueryKeyword:
		// Short keywords: tokenize + expand synonyms, keep original
		return rewriteKeyword(query)

	case QueryQuestion:
		// Strip question words, keep core content + tokenize
		core := SimplifyQueryForSearch(query)
		if core != query {
			query = query + " " + core
		}
		return rewriteExpand(query)

	case QueryLong:
		// Long form: extract top keywords only, avoid noise
		return rewriteLong(query)

	default:
		return rewriteExpand(query)
	}
}

// rewriteKeyword handles short keyword queries.
func rewriteKeyword(query string) string {
	if hasCJK(query) {
		tokens := filterTokens(gojiebaCut(query))
		if len(tokens) > 0 {
			// Keyword: keep original + append expanded synonyms + tokens
			return query + " " + strings.Join(expandSynonyms(tokens), " ")
		}
	}
	return query
}

// rewriteExpand adds tokenized form to the query.
func rewriteExpand(query string) string {
	if hasCJK(query) {
		tokens := gojiebaCut(query)
		if len(tokens) > 0 {
			keywords := expandSynonyms(filterTokens(tokens))
			if len(keywords) > 0 {
				query = query + " " + strings.Join(keywords, " ")
			}
		}
	}
	return query
}

// rewriteLong extracts core keywords from long queries to avoid noise.
func rewriteLong(query string) string {
	if hasCJK(query) {
		tokens := gojiebaCut(query)
		if len(tokens) > 0 {
			keywords := filterTokens(tokens)
			if len(keywords) > 0 {
				// Long form: use top 5 keywords as main query, original as supplement
				n := 5
				if len(keywords) < n {
					n = len(keywords)
				}
				short := strings.Join(keywords[:n], " ")
				return short + " " + query
			}
		}
	}
	return query
}

// normalizeQuery cleans query text for better matching.
func normalizeQuery(query string) string {
	var b strings.Builder
	prevSpace := false
	for _, r := range query {
		if r == '\n' || r == '\r' || r == '\t' {
			if !prevSpace {
				b.WriteRune(' ')
				prevSpace = true
			}
			continue
		}
		// Remove repeated punctuation
		if isQueryNoise(r) {
			continue
		}
		if r == ' ' {
			if !prevSpace {
				b.WriteRune(' ')
				prevSpace = true
			}
		} else {
			b.WriteRune(r)
			prevSpace = false
		}
	}
	return strings.TrimSpace(b.String())
}

// isQueryNoise returns true for characters that add no signal to search.
func isQueryNoise(r rune) bool {
	switch r {
	case '？', '?', '！', '!', '，', ',', '：', ':', '；', ';', '。', '.',
		'…', '~', '`', '|', '「', '」', '『', '』', '（', '）', '【', '】':
		return true
	}
	return false
}

// filterTokens keeps the most meaningful tokens from gse output.
func filterTokens(tokens []string) []string {
	seen := map[string]bool{}
	var kw []string
	for _, t := range tokens {
		t = strings.TrimSpace(t)
		if len([]rune(t)) < 2 || stopTokens[t] || stopTokens[strings.ToLower(t)] {
			continue
		}
		if isPurePunct(t) {
			continue
		}
		if seen[t] {
			continue
		}
		seen[t] = true
		kw = append(kw, t)
	}
	// Limit to 10 keywords to avoid query bloat
	if len(kw) > 10 {
		kw = kw[:10]
	}
	return kw
}

// SimplifyQueryForSearch strips question words and keeps core content.
// "什么是大模型？" → "大模型"
func SimplifyQueryForSearch(query string) string {
	// Strip common question prefixes
	prefixes := []string{"什么是", "什么叫", "怎么", "如何", "请问", "我想知道", "告诉我"}
	for _, p := range prefixes {
		if strings.HasPrefix(query, p) {
			query = strings.TrimPrefix(query, p)
			break
		}
	}
	// Strip trailing question marks
	query = strings.TrimRight(query, "？?吗呢啊呀吧")
	query = strings.TrimSpace(query)

	// If all signal words were stripped, return cleaned original
	if len([]rune(query)) < 2 {
		return query
	}

	// For CJK: remove remaining functional words
	if hasCJK(query) {
		runes := []rune(query)
		var keep []rune
		for _, r := range runes {
			if unicode.IsLetter(r) || unicode.IsDigit(r) {
				keep = append(keep, r)
			}
		}
		if len(keep) >= 2 {
			return string(keep)
		}
	}

	return query
}

// deDupStrings removes duplicate strings preserving order.
func deDupStrings(slice []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range slice {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}
