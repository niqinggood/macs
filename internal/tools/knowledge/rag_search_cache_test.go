package knowledge

import (
	"testing"
	"time"
)

func TestRAGSearchCacheSeparatesSessionsAndClonesHits(t *testing.T) {
	cache := NewRAGSearchCache(5, time.Minute)
	calls := 0
	search := func(_ []uint, _ string, sessionID, _ string, _ int) ([]RAGResult, error) {
		calls++
		return []RAGResult{{SourceDoc: sessionID, Content: "result"}}, nil
	}

	first, err := cache.Search([]uint{1}, "query", "session-a", "", 5, search)
	if err != nil {
		t.Fatal(err)
	}
	first[0].Content = "mutated"
	hit, err := cache.Search([]uint{1}, "query", "session-a", "", 5, search)
	if err != nil {
		t.Fatal(err)
	}
	secondSession, err := cache.Search([]uint{1}, "query", "session-b", "", 5, search)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("search calls = %d, want 2", calls)
	}
	if hit[0].Content != "result" {
		t.Fatalf("cached result was mutated: %#v", hit)
	}
	if secondSession[0].SourceDoc != "session-b" {
		t.Fatalf("sessions shared a cache entry: %#v", secondSession)
	}
}

func TestRAGSearchCacheExpiresAndDoesNotCacheEmptyResults(t *testing.T) {
	cache := NewRAGSearchCache(2, time.Millisecond)
	calls := 0
	search := func(_ []uint, _ string, _ string, _ string, _ int) ([]RAGResult, error) {
		calls++
		if calls == 1 {
			return nil, nil
		}
		return []RAGResult{{Content: "result"}}, nil
	}
	if _, err := cache.Search(nil, "query", "", "", 5, search); err != nil {
		t.Fatal(err)
	}
	if _, err := cache.Search(nil, "query", "", "", 5, search); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * time.Millisecond)
	if _, err := cache.Search(nil, "query", "", "", 5, search); err != nil {
		t.Fatal(err)
	}
	if calls != 3 {
		t.Fatalf("search calls = %d, want 3", calls)
	}
}
