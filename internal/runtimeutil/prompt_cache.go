package runtimeutil

import (
	"crypto/sha256"
	"fmt"
	"sort"
	"strings"
	"sync"
)

// ====================== Prompt Cache Splitting ======================
// Claude Code 借鉴: splitSysPromptPrefix in utils/api.ts
//
// System prompt split into cache-scoped blocks:
//   Block 1: staticPrefix  — global cache (tools list + core instructions, survives across tenants)
//   Block 2: userContext   — org cache (per-tenant user prefs)
//   Block 3: dynamicSuffix — never cached (per-request: git status, skills, dynamic state)
//
// Cache breakpoints placed between blocks so static content stays cached
// while dynamic content is recomputed each turn.

// SystemPromptBlock represents one cache-scoped segment.
type SystemPromptBlock struct {
	Text       string `json:"text"`
	CacheScope string `json:"cache_scope"` // "global", "org", or ""
}

// SystemPromptParts holds the three segments.
type SystemPromptParts struct {
	StaticPrefix  SystemPromptBlock `json:"static_prefix"`
	UserContext   SystemPromptBlock `json:"user_context"`
	DynamicSuffix SystemPromptBlock `json:"dynamic_suffix"`
}

// SplitSystemPrompt partitions a monolithic prompt into cache-scoped blocks.
func SplitSystemPrompt(fullPrompt string) *SystemPromptParts {
	dynamicMarkers := []string{
		"## Current Context",
		"## Git Status",
		"## Active Skills",
		"## Agent Memory",
		"## Workspace Status",
	}

	parts := &SystemPromptParts{}

	for _, marker := range dynamicMarkers {
		if idx := strings.Index(fullPrompt, marker); idx > 0 {
			parts.StaticPrefix = SystemPromptBlock{Text: fullPrompt[:idx], CacheScope: "global"}
			parts.DynamicSuffix = SystemPromptBlock{Text: fullPrompt[idx:], CacheScope: ""}
			return parts
		}
	}

	// No dynamic marker found — everything is static
	parts.StaticPrefix = SystemPromptBlock{Text: fullPrompt, CacheScope: "global"}
	return parts
}

// Assemble reassembles the prompt with cache breakpoint markers.
func (parts *SystemPromptParts) Assemble() string {
	var sb strings.Builder

	if parts.StaticPrefix.Text != "" {
		sb.WriteString(parts.StaticPrefix.Text)
	}
	if parts.UserContext.Text != "" {
		sb.WriteString("\n\n")
		sb.WriteString(parts.UserContext.Text)
	}
	if parts.DynamicSuffix.Text != "" {
		sb.WriteString("\n\n")
		sb.WriteString(parts.DynamicSuffix.Text)
	}

	return sb.String()
}

// AssembleMessages builds ready-to-send messages.
func (parts *SystemPromptParts) AssembleMessages(userMessage string) []map[string]interface{} {
	return []map[string]interface{}{
		{"role": "system", "content": parts.Assemble()},
		{"role": "user", "content": userMessage},
	}
}

// ====================== Tool Schema Cache ======================

// ToolSchemaCache caches serialized tool schemas to prevent mid-session churn.
type ToolSchemaCache struct {
	mu        sync.Mutex
	cached    string
	cacheKey  string
	generated bool
}

// Get returns cached schemas, regenerating only if inputs changed.
func (tsc *ToolSchemaCache) Get(key string, generate func() string) string {
	tsc.mu.Lock()
	defer tsc.mu.Unlock()

	if tsc.generated && tsc.cacheKey == key {
		return tsc.cached
	}
	tsc.cached = generate()
	tsc.cacheKey = key
	tsc.generated = true
	return tsc.cached
}

// Invalidate forces regeneration on next Get.
func (tsc *ToolSchemaCache) Invalidate() {
	tsc.mu.Lock()
	defer tsc.mu.Unlock()
	tsc.generated = false
}

// ====================== Prompt Builder ======================

// PromptCacheBuilder constructs cache-optimized system prompts.
type PromptCacheBuilder struct {
	mu              sync.Mutex
	basePrompt      string
	toolSchemas     string
	checksum        string
	dynamicSections map[string]func() string
}

// NewPromptCacheBuilder creates a new builder.
func NewPromptCacheBuilder(basePrompt string) *PromptCacheBuilder {
	return &PromptCacheBuilder{
		basePrompt:      basePrompt,
		dynamicSections: make(map[string]func() string),
	}
}

// SetToolSchemas stores pre-computed tool definitions.
func (pcb *PromptCacheBuilder) SetToolSchemas(schemas string) {
	pcb.mu.Lock()
	defer pcb.mu.Unlock()
	pcb.toolSchemas = schemas
	h := sha256.Sum256([]byte(pcb.basePrompt + schemas))
	pcb.checksum = fmt.Sprintf("%x", h[:16])
}

// Checksum returns the current static-block checksum.
func (pcb *PromptCacheBuilder) Checksum() string {
	pcb.mu.Lock()
	defer pcb.mu.Unlock()
	return pcb.checksum
}

// AddDynamicSection registers a per-turn dynamic section.
func (pcb *PromptCacheBuilder) AddDynamicSection(name string, fn func() string) {
	pcb.mu.Lock()
	defer pcb.mu.Unlock()
	pcb.dynamicSections[name] = fn
}

// Build assembles the full split prompt.
func (pcb *PromptCacheBuilder) Build() *SystemPromptParts {
	pcb.mu.Lock()
	defer pcb.mu.Unlock()

	parts := &SystemPromptParts{}

	var static strings.Builder
	static.WriteString(pcb.basePrompt)
	if pcb.toolSchemas != "" {
		static.WriteString("\n\n## Available Tools\n\n")
		static.WriteString(pcb.toolSchemas)
	}
	parts.StaticPrefix = SystemPromptBlock{Text: static.String(), CacheScope: "global"}

	names := make([]string, 0, len(pcb.dynamicSections))
	for name := range pcb.dynamicSections {
		names = append(names, name)
	}
	sort.Strings(names)
	var dynamic strings.Builder
	for _, name := range names {
		if section := pcb.dynamicSections[name](); section != "" {
			dynamic.WriteString(section)
			dynamic.WriteString("\n")
		}
	}
	parts.DynamicSuffix = SystemPromptBlock{Text: dynamic.String(), CacheScope: ""}

	return parts
}
