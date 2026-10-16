package modelruntime

import (
	"context"
	"fmt"
	"log"
	"sync"
)

// ModelTraits lets the generic fallback engine inspect only the two pieces of
// model metadata it needs without depending on the application's config type.
type ModelTraits[T any] struct {
	Configured func(T) bool
	Name       func(T) string
}

// FallbackStats is a point-in-time snapshot of fallback activity.
type FallbackStats struct {
	PrimaryCalls   int64 `json:"primary_calls"`
	FallbackCalls  int64 `json:"fallback_calls"`
	PrimaryErrors  int64 `json:"primary_errors"`
	FallbackErrors int64 `json:"fallback_errors"`
}

// FallbackChain is a concurrency-safe, priority-ordered model chain.
type FallbackChain[T any] struct {
	Name   string
	Models []T

	logger *log.Logger
	traits ModelTraits[T]
	mu     sync.RWMutex
	stats  FallbackStats
}

func NewFallbackChain[T any](name string, models []T, traits ModelTraits[T], logger *log.Logger) *FallbackChain[T] {
	if len(models) == 0 {
		return nil
	}
	return &FallbackChain[T]{Name: name, Models: append([]T(nil), models...), logger: logger, traits: traits}
}

func (chain *FallbackChain[T]) modelName(model T) string {
	if chain.traits.Name == nil {
		return "unknown"
	}
	return chain.traits.Name(model)
}

func (chain *FallbackChain[T]) configured(model T) bool {
	return chain.traits.Configured == nil || chain.traits.Configured(model)
}

func (chain *FallbackChain[T]) logf(format string, args ...interface{}) {
	if chain.logger != nil {
		chain.logger.Printf(format, args...)
	}
}

// SnapshotModels returns a copy suitable for health checks and diagnostics.
func (chain *FallbackChain[T]) SnapshotModels() []T {
	if chain == nil {
		return nil
	}
	chain.mu.RLock()
	defer chain.mu.RUnlock()
	return append([]T(nil), chain.Models...)
}

func (chain *FallbackChain[T]) GetPrimary() T {
	chain.mu.RLock()
	defer chain.mu.RUnlock()
	return chain.Models[0]
}

// Call tries configured models in priority order. Context cancellation is
// observed before starting another provider attempt.
func (chain *FallbackChain[T]) Call(ctx context.Context, execute func(T) (string, error)) (string, error) {
	if chain == nil {
		return "", fmt.Errorf("fallback chain is unavailable")
	}
	if execute == nil {
		return "", fmt.Errorf("fallback executor is unavailable")
	}
	models := chain.SnapshotModels()
	chain.mu.Lock()
	chain.stats.PrimaryCalls++
	chain.mu.Unlock()

	var lastErr error
	attempted := 0
	for i, model := range models {
		if !chain.configured(model) {
			continue
		}
		if ctx != nil {
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			default:
			}
		}

		attempted++
		if i > 0 {
			chain.mu.Lock()
			chain.stats.FallbackCalls++
			chain.mu.Unlock()
			chain.logf("[Fallback] %s: primary failed, trying fallback #%d (%s): %v", chain.Name, i, chain.modelName(model), lastErr)
		}

		result, err := execute(model)
		if err == nil {
			if i > 0 {
				chain.logf("[Fallback] %s: fallback #%d (%s) succeeded", chain.Name, i, chain.modelName(model))
			}
			return result, nil
		}

		lastErr = err
		chain.mu.Lock()
		if i == 0 {
			chain.stats.PrimaryErrors++
		} else {
			chain.stats.FallbackErrors++
		}
		chain.mu.Unlock()
		chain.logf("[Fallback] %s: model %d/%d (%s) failed: %v", chain.Name, i+1, len(models), chain.modelName(model), err)
	}
	if attempted == 0 {
		return "", fmt.Errorf("fallback chain %q has no configured models", chain.Name)
	}
	return "", fmt.Errorf("all %d attempted models failed, last error: %w", attempted, lastErr)
}

func (chain *FallbackChain[T]) Stats() *FallbackStats {
	if chain == nil {
		return &FallbackStats{}
	}
	chain.mu.RLock()
	defer chain.mu.RUnlock()
	snapshot := chain.stats
	return &snapshot
}

// Registry owns named fallback chains and provides default-chain lookup.
type Registry[T any] struct {
	mu     sync.RWMutex
	chains map[string]*FallbackChain[T]
}

func NewRegistry[T any]() *Registry[T] {
	return &Registry[T]{chains: make(map[string]*FallbackChain[T])}
}

func (registry *Registry[T]) Set(name string, chain *FallbackChain[T]) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if chain == nil {
		delete(registry.chains, name)
		return
	}
	registry.chains[name] = chain
}

func (registry *Registry[T]) Get(name string) *FallbackChain[T] {
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	if chain, ok := registry.chains[name]; ok {
		return chain
	}
	return registry.chains["default"]
}

func (registry *Registry[T]) Snapshot() map[string]*FallbackChain[T] {
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	result := make(map[string]*FallbackChain[T], len(registry.chains))
	for name, chain := range registry.chains {
		result[name] = chain
	}
	return result
}
