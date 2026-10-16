package knowledge

import (
	"fmt"
	"sync"
	"time"
)

type ragSearchCacheEntry struct {
	results   []RAGResult
	expiresAt time.Time
}

// RAGSearchCache stores successful non-empty hybrid search results. Session ID
// is part of the key because multi-turn query enrichment is session-specific.
type RAGSearchCache struct {
	mu      sync.Mutex
	entries map[string]ragSearchCacheEntry
	ttl     time.Duration
	maxSize int
}

func NewRAGSearchCache(maxSize int, ttl time.Duration) *RAGSearchCache {
	if maxSize <= 0 {
		maxSize = 50
	}
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}
	return &RAGSearchCache{
		entries: make(map[string]ragSearchCacheEntry),
		ttl:     ttl,
		maxSize: maxSize,
	}
}

func (cache *RAGSearchCache) Search(
	kbIDs []uint,
	query, sessionID, sourceDoc string,
	limit int,
	search RAGSearcher,
) ([]RAGResult, error) {
	if search == nil {
		return nil, fmt.Errorf("RAG search callback is unavailable")
	}
	if cache == nil {
		return search(kbIDs, query, sessionID, sourceDoc, limit)
	}
	key := fmt.Sprintf("%v|%s|%s|%s|%d", kbIDs, query, sessionID, sourceDoc, limit)
	now := time.Now()

	cache.mu.Lock()
	if entry, ok := cache.entries[key]; ok {
		if now.Before(entry.expiresAt) {
			results := append([]RAGResult(nil), entry.results...)
			cache.mu.Unlock()
			return results, nil
		}
		delete(cache.entries, key)
	}
	cache.mu.Unlock()

	results, err := search(kbIDs, query, sessionID, sourceDoc, limit)
	if err != nil || len(results) == 0 {
		return results, err
	}

	cache.mu.Lock()
	if len(cache.entries) >= cache.maxSize {
		cache.evictEarliestLocked()
	}
	cache.entries[key] = ragSearchCacheEntry{
		results:   append([]RAGResult(nil), results...),
		expiresAt: now.Add(cache.ttl),
	}
	cache.mu.Unlock()
	return results, nil
}

func (cache *RAGSearchCache) evictEarliestLocked() {
	var oldestKey string
	var oldestExpiry time.Time
	for key, entry := range cache.entries {
		if oldestKey == "" || entry.expiresAt.Before(oldestExpiry) {
			oldestKey = key
			oldestExpiry = entry.expiresAt
		}
	}
	if oldestKey != "" {
		delete(cache.entries, oldestKey)
	}
}

func (cache *RAGSearchCache) Clear() {
	if cache == nil {
		return
	}
	cache.mu.Lock()
	cache.entries = make(map[string]ragSearchCacheEntry)
	cache.mu.Unlock()
}
