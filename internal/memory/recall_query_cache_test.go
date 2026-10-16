package memory

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestRecallQueryCacheReuseIsolationAndCancellation(t *testing.T) {
	s := &Service{}
	calls := 0
	backend := RecallEmbedding{ModelID: "model-a", Embed: func(ctx context.Context, text string) ([]float64, error) {
		calls++
		return []float64{3, 4}, nil
	}}
	for _, tc := range []struct {
		namespace, question string
		calls               int
	}{
		{"alice", "question", 1}, {"alice", "question", 1},
		{"bob", "question", 2}, {"alice", "another question", 3},
	} {
		v, err := s.recallQuery(context.Background(), tc.namespace, backend, tc.question)
		if err != nil || calls != tc.calls || len(v) != 2 || v[0] != 0.6 {
			t.Fatalf("query %#v: %v %v calls=%d", tc, v, err, calls)
		}
		v[0] = 99 // Callers must not mutate the cached vector.
	}
	backend.ModelID = "model-b"
	if _, err := s.recallQuery(context.Background(), "alice", backend, "question"); err != nil || calls != 4 {
		t.Fatalf("model change reused a vector: %v calls=%d", err, calls)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.recallQuery(canceled, "alice", backend, "question"); !errors.Is(err, context.Canceled) || calls != 4 {
		t.Fatalf("cache ignored cancellation: %v calls=%d", err, calls)
	}
	backend.Embed = func(context.Context, string) ([]float64, error) { calls++; return nil, errors.New("offline") }
	for i := 0; i < 2; i++ {
		if _, err := s.recallQuery(context.Background(), "alice", backend, "failed query"); err == nil {
			t.Fatal("provider failure lost")
		}
	}
	if calls != 6 {
		t.Fatal("failed result was cached")
	}
}

func TestRecallQueryCacheExpiryAndBound(t *testing.T) {
	var cache recallQueryCache
	now := time.Now()
	for i := 0; i < recallQueryCacheSize; i++ {
		cache.put(fmt.Sprint(i), []float64{1}, now.Add(time.Duration(i)*time.Millisecond))
	}
	cache.get("0", now.Add(time.Second))
	cache.put("new", []float64{1}, now.Add(2*time.Second))
	if len(cache.items) != recallQueryCacheSize || cache.get("1", now.Add(2*time.Second)) != nil || cache.get("0", now.Add(2*time.Second)) == nil {
		t.Fatal("LRU bound not preserved")
	}
	if cache.get("0", now.Add(recallQueryCacheTTL)) != nil {
		t.Fatal("read extended the expiry of a cached query")
	}
	cache.put("fresh", []float64{1}, now.Add(2*recallQueryCacheTTL))
	if len(cache.items) != 1 {
		t.Fatal("expired queries retained during insertion")
	}
}
