package knowledge

import (
	"sort"
	"strings"
)

// RAGResult is a search result shared by keyword and semantic retrieval.
type RAGResult struct {
	SourceDoc     string   `json:"source_doc"`
	ChunkIndex    int      `json:"chunk_index"`
	Content       string   `json:"content"`
	Score         float64  `json:"score"`
	MatchedTokens []string `json:"matched_tokens,omitempty"`
	KBID          string   `json:"-"`
}

// VectorResult is the package-neutral form of a vector search hit.
type VectorResult struct {
	ChunkID uint
	DocName string
	Heading string
	Content string
	Score   float64
}

// DeduplicateResults removes near-duplicate chunks and keeps at most three
// results from the same source document.
func DeduplicateResults(results []RAGResult, limit int) []RAGResult {
	if limit <= 0 || len(results) == 0 {
		return nil
	}
	kept := make([]RAGResult, 0, min(limit, len(results)))
	keptByDoc := make(map[string][]string)
	for _, result := range results {
		if len(kept) >= limit {
			break
		}
		docKept := keptByDoc[result.SourceDoc]
		if len(docKept) >= 3 {
			continue
		}
		duplicate := false
		for _, previous := range docKept {
			if TextOverlap(result.Content, previous) > 0.6 {
				duplicate = true
				break
			}
		}
		if duplicate {
			continue
		}
		kept = append(kept, result)
		keptByDoc[result.SourceDoc] = append(docKept, result.Content)
	}
	return kept
}

// FindMatchedTokens returns the unique query terms present in content.
func FindMatchedTokens(content string, terms []string) []string {
	lowerContent := strings.ToLower(content)
	seen := make(map[string]bool, len(terms))
	matched := make([]string, 0)
	for _, term := range terms {
		key := strings.ToLower(term)
		if len([]rune(term)) < 2 || seen[key] {
			continue
		}
		if strings.Contains(lowerContent, key) {
			seen[key] = true
			matched = append(matched, term)
		}
	}
	return matched
}

// TextOverlap estimates how much of the shorter text is covered by shared
// substrings between four and ten runes long.
func TextOverlap(a, b string) float64 {
	if a == "" || b == "" {
		return 0
	}
	shorterRunes, longerRunes := []rune(a), []rune(b)
	if len(shorterRunes) > len(longerRunes) {
		shorterRunes, longerRunes = longerRunes, shorterRunes
	}
	longer := string(longerRunes)
	matched := 0
	for index := 0; index < len(shorterRunes); index++ {
		for length := 4; length <= 10 && index+length <= len(shorterRunes); length++ {
			if strings.Contains(longer, string(shorterRunes[index:index+length])) {
				matched += length
				index += length - 1
				break
			}
		}
	}
	return float64(matched) / float64(len(shorterRunes))
}

// ReciprocalRankFusion merges keyword and vector rankings using weighted RRF.
func ReciprocalRankFusion(keyword []RAGResult, vector []VectorResult, limit int, keywordWeight float64) []RAGResult {
	const rankOffset = 60
	if limit <= 0 {
		return nil
	}
	if keywordWeight <= 0 || keywordWeight > 1 {
		keywordWeight = 0.5
	}
	vectorWeight := 1 - keywordWeight
	type rankedEntry struct {
		result RAGResult
		score  float64
	}
	fused := make(map[string]*rankedEntry, len(keyword)+len(vector))
	for rank, result := range keyword {
		key := result.SourceDoc + "|" + result.Content
		score := keywordWeight / float64(rankOffset+rank+1)
		if existing, ok := fused[key]; ok {
			existing.score += score
		} else {
			fused[key] = &rankedEntry{result: result, score: score}
		}
	}
	for rank, result := range vector {
		key := result.DocName + "|" + result.Content
		score := vectorWeight / float64(rankOffset+rank+1)
		if existing, ok := fused[key]; ok {
			existing.score += score
		} else {
			fused[key] = &rankedEntry{
				result: RAGResult{SourceDoc: result.DocName, Content: result.Content, Score: result.Score},
				score:  score,
			}
		}
	}
	entries := make([]*rankedEntry, 0, len(fused))
	for _, entry := range fused {
		entries = append(entries, entry)
	}
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].score > entries[j].score })
	if len(entries) > limit {
		entries = entries[:limit]
	}
	results := make([]RAGResult, len(entries))
	for index, entry := range entries {
		results[index] = entry.result
		results[index].Score = entry.score * 10
	}
	return DeduplicateResults(results, limit)
}

// VectorResultsToRAG converts vector hits to the shared result form.
func VectorResultsToRAG(results []VectorResult, limit int) []RAGResult {
	if limit <= 0 {
		return nil
	}
	converted := make([]RAGResult, 0, min(limit, len(results)))
	for _, result := range results {
		converted = append(converted, RAGResult{
			SourceDoc: result.DocName,
			Content:   result.Content,
			Score:     result.Score * 10,
		})
		if len(converted) >= limit {
			break
		}
	}
	return converted
}
