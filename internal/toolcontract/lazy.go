package toolcontract

import (
	"context"
	"sync"
)

// ====================== Lazy Schema Evaluation ======================
// Claude Code 借鉴: lazySchema() in AgentTool.tsx
//
// Tool parameters are computed on first access rather than at registration time.
// Benefits:
//   1. Feature flags can change tool signatures without rebuilding the registry
//   2. Prompt cache stability — schema changes mid-session don't churn the prefix
//   3. Avoids import-time side effects from tool constructors
//
// Usage: wrap any Tool with NewLazyTool(tool) — Parameters() will be
// computed lazily on first call and cached thereafter.

// LazyTool wraps a Tool to defer parameter computation to first access.
// After first computation, the result is cached until invalidated.
type LazyTool struct {
	inner    Tool
	mu       sync.Mutex
	computed bool
	cached   map[string]interface{}
	genFn    func(Tool) map[string]interface{} // optional custom generator
}

// NewLazyTool creates a lazy wrapper around a tool. Parameters() is computed
// on first access and cached. Use Invalidate() to force recomputation.
func NewLazyTool(inner Tool) *LazyTool {
	return &LazyTool{inner: inner}
}

// NewLazyToolWithGen creates a lazy tool with a custom parameter generator.
// The genFn receives the inner tool and returns the parameter schema.
func NewLazyToolWithGen(inner Tool, genFn func(Tool) map[string]interface{}) *LazyTool {
	return &LazyTool{inner: inner, genFn: genFn}
}

// Name delegates to the inner tool.
func (lt *LazyTool) Name() string { return lt.inner.Name() }

// Description delegates to the inner tool.
func (lt *LazyTool) Description() string { return lt.inner.Description() }

// Parameters computes tool parameters on first access, caches, and returns.
func (lt *LazyTool) Parameters() map[string]interface{} {
	lt.mu.Lock()
	defer lt.mu.Unlock()

	if lt.computed {
		return lt.cached
	}

	if lt.genFn != nil {
		lt.cached = lt.genFn(lt.inner)
	} else {
		lt.cached = lt.inner.Parameters()
	}
	lt.computed = true
	return lt.cached
}

// Execute delegates to the inner tool.
func (lt *LazyTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	return lt.inner.Execute(ctx, params)
}

// Invalidate forces recomputation of Parameters on next access.
// Use after feature flag changes.
func (lt *LazyTool) Invalidate() {
	lt.mu.Lock()
	defer lt.mu.Unlock()
	lt.computed = false
	lt.cached = nil
}

// Inner returns the wrapped tool.
func (lt *LazyTool) Inner() Tool { return lt.inner }

// ====================== Lazy Tool Registry ======================

// Registry is the minimal registration surface required by LazyToolRegistry.
type Registry interface {
	Register(Tool)
}

// LazyToolRegistry wraps a Registry with lazy schema support.
type LazyToolRegistry struct {
	Registry
	mu       sync.RWMutex
	lazyReqs map[string]*LazyTool // tool name → lazy wrapper
}

// NewLazyToolRegistry creates a lazy-aware registry wrapping an existing registry.
func NewLazyToolRegistry(reg Registry) *LazyToolRegistry {
	return &LazyToolRegistry{
		Registry: reg,
		lazyReqs: make(map[string]*LazyTool),
	}
}

// RegisterLazy registers a tool through a lazy wrapper, deferring parameter computation.
// The factory function is called on first access to compute Parameters().
func (lr *LazyToolRegistry) RegisterLazy(name string, factory func() Tool) {
	inner := factory()
	lazy := &LazyTool{inner: inner, genFn: func(t Tool) map[string]interface{} {
		return t.Parameters()
	}}
	lr.mu.Lock()
	lr.lazyReqs[name] = lazy
	lr.mu.Unlock()
	lr.Registry.Register(lazy)
}

// InvalidateAll forces recomputation of all lazy tool parameters.
// Use after batch feature flag changes.
func (lr *LazyToolRegistry) InvalidateAll() {
	lr.mu.Lock()
	defer lr.mu.Unlock()
	for _, lt := range lr.lazyReqs {
		lt.Invalidate()
	}
}

// InvalidateTool forces recomputation of a single tool's parameters.
func (lr *LazyToolRegistry) InvalidateTool(name string) {
	lr.mu.Lock()
	defer lr.mu.Unlock()
	if lt, ok := lr.lazyReqs[name]; ok {
		lt.Invalidate()
	}
}

// ====================== Conditional Schema ======================

// ConditionalParamBuilder builds tool parameters with feature-gated sections.
// Example: a tool may expose "isolation" param only when FeatureWorktree is enabled.
type ConditionalParamBuilder struct {
	properties map[string]interface{}
	required   []string
}

// NewConditionalParamBuilder creates a parameter builder.
func NewConditionalParamBuilder() *ConditionalParamBuilder {
	return &ConditionalParamBuilder{
		properties: make(map[string]interface{}),
	}
}

// Add adds a parameter that is always present.
func (cpb *ConditionalParamBuilder) Add(name string, paramType string, description string, required bool, enum []string) *ConditionalParamBuilder {
	prop := map[string]interface{}{
		"type":        paramType,
		"description": description,
	}
	if len(enum) > 0 {
		prop["enum"] = enum
	}
	cpb.properties[name] = prop
	if required {
		cpb.required = append(cpb.required, name)
	}
	return cpb
}

// AddIf adds a parameter only when the given feature flag is enabled.
func (cpb *ConditionalParamBuilder) AddIf(enabled bool, name string, paramType string, description string, required bool, enum []string) *ConditionalParamBuilder {
	if !enabled {
		return cpb
	}
	return cpb.Add(name, paramType, description, required, enum)
}

// Build returns the completed parameter schema.
func (cpb *ConditionalParamBuilder) Build() map[string]interface{} {
	schema := map[string]interface{}{
		"type":       "object",
		"properties": cpb.properties,
	}
	if len(cpb.required) > 0 {
		schema["required"] = cpb.required
	}
	return schema
}

// ====================== Feature-Flag-Aware Tool ======================

// FeatureGatedTool wraps a tool whose Parameters() are conditional on feature flags.
// Parameters are recalculated on every call (not cached), ensuring flag changes
// take effect immediately without restart.
type FeatureGatedTool struct {
	inner  Tool
	params func() map[string]interface{} // called on every Parameters() access
}

// NewFeatureGatedTool creates a feature-gated tool.
func NewFeatureGatedTool(inner Tool, paramsFn func() map[string]interface{}) *FeatureGatedTool {
	return &FeatureGatedTool{inner: inner, params: paramsFn}
}

// Name delegates to inner.
func (fgt *FeatureGatedTool) Name() string { return fgt.inner.Name() }

// Description delegates to inner.
func (fgt *FeatureGatedTool) Description() string { return fgt.inner.Description() }

// Parameters computes parameters fresh each time, reflecting current feature flags.
func (fgt *FeatureGatedTool) Parameters() map[string]interface{} { return fgt.params() }

// Execute delegates to inner.
func (fgt *FeatureGatedTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	return fgt.inner.Execute(ctx, params)
}
