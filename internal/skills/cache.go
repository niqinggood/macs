package skills

import (
	"sync"
	"time"
)

// SkillSearchCache stores short-lived semantic skill lookup results.
type SkillSearchCache struct {
	mu      sync.RWMutex
	entries map[string]*skillCacheEntry
	maxSize int
	ttl     time.Duration
}

type skillCacheEntry struct {
	skills    []string
	timestamp time.Time
}

func NewSkillSearchCache(maxSize int, ttl time.Duration) *SkillSearchCache {
	if ttl > 0 && ttl < time.Millisecond {
		ttl *= time.Millisecond
	}
	return &SkillSearchCache{
		entries: make(map[string]*skillCacheEntry),
		maxSize: maxSize,
		ttl:     ttl,
	}
}

func (c *SkillSearchCache) Get(query string) ([]string, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	entry, ok := c.entries[query]
	if !ok || time.Since(entry.timestamp) > c.ttl {
		return nil, false
	}
	return entry.skills, true
}

func (c *SkillSearchCache) Set(query string, skills []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.entries) >= c.maxSize {
		for key := range c.entries {
			delete(c.entries, key)
			break
		}
	}
	c.entries[query] = &skillCacheEntry{skills: skills, timestamp: time.Now()}
}
