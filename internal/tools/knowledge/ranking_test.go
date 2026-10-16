package knowledge

import "testing"

func TestDeduplicateResultsLimitsPerDocumentAndOverlap(t *testing.T) {
	results := []RAGResult{
		{SourceDoc: "a", Content: "abcdefghij first"},
		{SourceDoc: "a", Content: "abcdefghij first duplicate"},
		{SourceDoc: "a", Content: "unique alpha passage"},
		{SourceDoc: "a", Content: "separate beta material"},
		{SourceDoc: "a", Content: "unrelated gamma excerpt"},
		{SourceDoc: "b", Content: "another document"},
	}
	got := DeduplicateResults(results, 10)
	if len(got) != 4 {
		t.Fatalf("DeduplicateResults returned %d results, want 4", len(got))
	}
	if got[len(got)-1].SourceDoc != "b" {
		t.Fatalf("last result source = %q, want b", got[len(got)-1].SourceDoc)
	}
}

func TestFindMatchedTokensIsCaseInsensitiveAndUnique(t *testing.T) {
	got := FindMatchedTokens("Go makes search useful", []string{"GO", "search", "go", "x"})
	if len(got) != 2 || got[0] != "GO" || got[1] != "search" {
		t.Fatalf("FindMatchedTokens = %#v", got)
	}
}

func TestReciprocalRankFusionHonorsWeights(t *testing.T) {
	keyword := []RAGResult{
		{SourceDoc: "keyword", Content: "keyword top"},
		{SourceDoc: "second", Content: "keyword second"},
	}
	vector := []VectorResult{
		{DocName: "vector", Content: "vector top"},
		{DocName: "third", Content: "vector second"},
	}
	if got := ReciprocalRankFusion(keyword, vector, 4, 0.8); got[0].SourceDoc != "keyword" {
		t.Fatalf("keyword-heavy first source = %q", got[0].SourceDoc)
	}
	if got := ReciprocalRankFusion(keyword, vector, 4, 0.2); got[0].SourceDoc != "vector" {
		t.Fatalf("vector-heavy first source = %q", got[0].SourceDoc)
	}
}

func TestVectorResultsToRAGScalesAndLimits(t *testing.T) {
	got := VectorResultsToRAG([]VectorResult{
		{DocName: "a", Content: "one", Score: 0.9},
		{DocName: "b", Content: "two", Score: 0.8},
	}, 1)
	if len(got) != 1 || got[0].SourceDoc != "a" || got[0].Score != 9 {
		t.Fatalf("VectorResultsToRAG = %#v", got)
	}
}
