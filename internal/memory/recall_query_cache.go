package memory

import (
	"context"
	"sync"
	"time"
)

const (
	recallQueryCacheSize = 32
	recallQueryCacheTTL  = time.Minute
)

// Only query hashes and vectors are retained in this bounded, per-service
// cache. Namespace and model identity prevent cross-owner/vector-space reuse.
type recallQueryCache struct {
	mu    sync.Mutex
	items map[string]recallQueryVector
}

type recallQueryVector struct {
	vector  []float64
	expires time.Time
	used    time.Time
}

func (c *recallQueryCache) get(key string, now time.Time) []float64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	item, ok := c.items[key]
	if !ok || !now.Before(item.expires) {
		delete(c.items, key)
		return nil
	}
	item.used = now
	c.items[key] = item
	return append([]float64(nil), item.vector...)
}

func (c *recallQueryCache) put(key string, vector []float64, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.items == nil {
		c.items = make(map[string]recallQueryVector)
	}
	for id, item := range c.items {
		if !now.Before(item.expires) {
			delete(c.items, id)
		}
	}
	if _, exists := c.items[key]; !exists && len(c.items) >= recallQueryCacheSize {
		var oldest string
		for id, item := range c.items {
			if oldest == "" || item.used.Before(c.items[oldest].used) {
				oldest = id
			}
		}
		delete(c.items, oldest)
	}
	c.items[key] = recallQueryVector{vector: append([]float64(nil), vector...), expires: now.Add(recallQueryCacheTTL), used: now}
}

func (s *Service) recallQuery(ctx context.Context, namespace string, backend RecallEmbedding, question string) ([]float64, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	text := truncateSessionRunes(question, 2000)
	key := memoryHash(namespace, backend.ModelID, text)
	if vector := s.recallQueries.get(key, time.Now()); vector != nil {
		return vector, nil
	}
	vector, err := backend.Embed(ctx, text)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	vector, valid := normalizedRecallVector(vector)
	if valid {
		s.recallQueries.put(key, vector, time.Now())
	}
	return vector, nil
}
