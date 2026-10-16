package runtimeutil

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
	"time"
)

// ====================== Prompt Cache Break Detection ======================
// 完全按 Claude Code promptCacheBreakDetection.ts 思路构建。
//
// Two-phase detection:
//   Phase 1 (pre-call):  RecordPromptState — 记录 system prompt、tool schemas、
//                         model 等影响 cache 的因子，计算 hash。
//   Phase 2 (post-call): CheckResponseForCacheBreak — 对比当前调用的
//                         cache_hit_tokens 与上次调用，检测断点并诊断原因。
//
// 13 类可检测的变化（Claude Code 同款分类）：
//   1. model_changed
//   2. system_prompt_changed
//   3. tools_added
//   4. tools_removed
//   5. tool_schema_changed
//   6. fast_mode_changed
//   7. cache_scope_changed
//   8. cache_ttl_changed
//   9. global_cache_strategy_changed
//  10. betas_changed
//  11. auto_mode_changed
//  12. effort_changed
//  13. extra_params_changed
//  14. ttl_expiry (server-side)
//  15. server_eviction (server-side)

// CacheBreakCategory describes what caused a cache break.
type CacheBreakCategory string

const (
	BreakNone                 CacheBreakCategory = ""
	BreakModelChanged         CacheBreakCategory = "model_changed"
	BreakSystemPromptChanged  CacheBreakCategory = "system_prompt_changed"
	BreakToolsAdded           CacheBreakCategory = "tools_added"
	BreakToolsRemoved         CacheBreakCategory = "tools_removed"
	BreakToolSchemaChanged    CacheBreakCategory = "tool_schema_changed"
	BreakFastModeChanged      CacheBreakCategory = "fast_mode_changed"
	BreakCacheScopeChanged    CacheBreakCategory = "cache_scope_changed"
	BreakCacheTTLChanged      CacheBreakCategory = "cache_ttl_changed"
	BreakCacheStrategyChanged CacheBreakCategory = "global_cache_strategy_changed"
	BreakBetasChanged         CacheBreakCategory = "betas_changed"
	BreakAutoModeChanged      CacheBreakCategory = "auto_mode_changed"
	BreakEffortChanged        CacheBreakCategory = "effort_changed"
	BreakExtraParamsChanged   CacheBreakCategory = "extra_params_changed"
	BreakTTLExpiry            CacheBreakCategory = "ttl_expiry"
	BreakServerEviction       CacheBreakCategory = "server_eviction"
)

// CacheRecord captures one provider call's cache accounting. It is shared by
// cache metrics and cache-break diagnostics.
type CacheRecord struct {
	ID               int       `json:"id"`
	Time             time.Time `json:"time"`
	Model            string    `json:"model"`
	AgentName        string    `json:"agent_name"`
	TaskID           string    `json:"task_id"`
	PromptTokens     int       `json:"prompt_tokens"`
	CompletionTokens int       `json:"completion_tokens"`
	CacheHitTokens   int       `json:"cache_hit_tokens"`
	CacheMissTokens  int       `json:"cache_miss_tokens"`
	LatencyMs        int64     `json:"latency_ms"`
}

// PromptState captures the cache-relevant state before an API call.
// Claude Code: recordPromptState() hashes system prompt (with and without
// cache_control), tool schemas, model, fast mode, betas, effort, extra body params.
type PromptState struct {
	// Core identity
	Model string `json:"model"`

	// Content hashes (SHA-256)
	SystemPromptHash        string `json:"system_prompt_hash"`         // full system prompt
	SystemPromptNoCacheHash string `json:"system_prompt_nocache_hash"` // system prompt w/o cache_control markers

	// Tool information
	ToolCount      int    `json:"tool_count"`
	ToolNamesHash  string `json:"tool_names_hash"`  // sorted tool names → SHA-256
	ToolSchemaHash string `json:"tool_schema_hash"` // full tool definitions → SHA-256

	// Cache control
	CacheScope string `json:"cache_scope,omitempty"`
	CacheTTL   string `json:"cache_ttl,omitempty"`

	// Runtime params
	FastMode        bool   `json:"fast_mode"`
	Betas           string `json:"betas"`
	AutoModeEnabled bool   `json:"auto_mode_enabled"`
	Effort          string `json:"effort"`
	ExtraParamsHash string `json:"extra_params_hash,omitempty"`

	// Metadata
	Timestamp time.Time `json:"timestamp"`
}

// CacheBreakEvent records a detected cache break.
type CacheBreakEvent struct {
	Time            time.Time          `json:"time"`
	Category        CacheBreakCategory `json:"category"`
	Severity        string             `json:"severity"` // "major", "minor", "info"
	PreviousHitRate float64            `json:"previous_hit_rate"`
	CurrentHitRate  float64            `json:"current_hit_rate"`
	HitDelta        int                `json:"hit_delta"`
	Details         string             `json:"details"`
}

// CacheHealth tracks the overall cache health across multiple calls.
type CacheHealth struct {
	TotalCalls        int                        `json:"total_calls"`
	TotalBreaks       int                        `json:"total_breaks"`
	BreaksByCategory  map[CacheBreakCategory]int `json:"breaks_by_category"`
	LastBreak         *CacheBreakEvent           `json:"last_break,omitempty"`
	RecentEvents      []CacheBreakEvent          `json:"recent_events"`
	CurrentHitRateAvg float64                    `json:"current_hit_rate_avg"` // last 10 calls
}

// CacheBreakDetector is the two-phase cache break detection system.
type CacheBreakDetector struct {
	mu sync.RWMutex

	// State tracking
	prevState *PromptState
	prevCall  *CacheRecord

	// Cache break configuration
	minDropPct      float64 // minimum percentage drop to declare a break (default: 5%)
	minDropAbsolute int     // minimum absolute token drop to declare a break (default: 2000)
	ttlMinutes      int     // cache TTL in minutes (DeepSeek: ~5min, Anthropic: 5min or 60min)
	maxHistory      int     // max break events to keep

	// Event store
	events []CacheBreakEvent

	// Stats
	totalCalls  int
	totalBreaks int
	breaksByCat map[CacheBreakCategory]int
}

// NewCacheBreakDetector creates a new detector.
func NewCacheBreakDetector() *CacheBreakDetector {
	return &CacheBreakDetector{
		minDropPct:      5.0,
		minDropAbsolute: 2000,
		ttlMinutes:      5, // DeepSeek default TTL
		maxHistory:      50,
		events:          make([]CacheBreakEvent, 0, 50),
		breaksByCat:     make(map[CacheBreakCategory]int),
	}
}

// ====================== Phase 1: Pre-call State Recording ======================

// RecordPromptState captures the prompt state before an API call.
// Returns the state so it can be attached to the CacheRecord for Phase 2 comparison.
func (d *CacheBreakDetector) RecordPromptState(
	model string,
	systemPrompt string,
	toolDefs []map[string]interface{},
	fastMode bool,
	autoMode bool,
) *PromptState {
	state := &PromptState{
		Model:           model,
		FastMode:        fastMode,
		AutoModeEnabled: autoMode,
		Timestamp:       time.Now(),
	}

	// System prompt hash
	state.SystemPromptHash = cacheHashString(systemPrompt)

	// System prompt WITHOUT cache_control markers (for detecting cache_control changes)
	noCachePrompt := stripCacheControl(systemPrompt)
	state.SystemPromptNoCacheHash = cacheHashString(noCachePrompt)

	// Tool information — sort deterministically for stable hashing
	state.ToolCount = len(toolDefs)

	// Tool names hash (sorted)
	toolNames := make([]string, 0, len(toolDefs))
	for _, td := range toolDefs {
		if name, ok := td["function"].(map[string]interface{})["name"].(string); ok {
			toolNames = append(toolNames, name)
		}
	}
	sort.Strings(toolNames)
	state.ToolNamesHash = cacheHashString(strings.Join(toolNames, ","))

	// Full tool schema hash
	toolSchemaBytes, _ := json.Marshal(toolDefs)
	state.ToolSchemaHash = cacheHashBytes(toolSchemaBytes)

	return state
}

// ====================== Phase 2: Post-call Break Detection ======================

// CheckForBreak compares the current API response against the previous call
// to detect cache breaks and diagnose their cause.
// Claude Code: checkResponseForCacheBreak() — if cache_read_tokens drops >5%
// AND >2000 tokens absolute, declare a break.
func (d *CacheBreakDetector) CheckForBreak(
	currentRecord CacheRecord,
	currentState *PromptState,
) *CacheBreakEvent {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.totalCalls++

	if d.prevCall == nil || d.prevState == nil {
		// First call — no baseline to compare against
		d.prevCall = &currentRecord
		d.prevState = currentState
		return nil
	}

	prev := d.prevCall
	prevState := d.prevState

	// Compute hit rates
	prevTotal := prev.CacheHitTokens + prev.CacheMissTokens
	currentTotal := currentRecord.CacheHitTokens + currentRecord.CacheMissTokens

	prevHitRate := 0.0
	if prevTotal > 0 {
		prevHitRate = float64(prev.CacheHitTokens) / float64(prevTotal) * 100
	}

	currentHitRate := 0.0
	if currentTotal > 0 {
		currentHitRate = float64(currentRecord.CacheHitTokens) / float64(currentTotal) * 100
	}

	hitDelta := prev.CacheHitTokens - currentRecord.CacheHitTokens

	// Check if this is a significant drop
	isSignificant := hitDelta > d.minDropAbsolute &&
		(prevHitRate-currentHitRate) > d.minDropPct

	if !isSignificant {
		// Cache healthy — update baseline
		d.prevCall = &currentRecord
		d.prevState = currentState
		return nil
	}

	// Cache break detected — diagnose the cause
	event := d.diagnoseCause(currentState, prevState, currentRecord)
	event.PreviousHitRate = prevHitRate
	event.CurrentHitRate = currentHitRate
	event.HitDelta = hitDelta

	// Store event
	if len(d.events) >= d.maxHistory {
		d.events = d.events[1:]
	}
	d.events = append(d.events, *event)
	d.totalBreaks++
	d.breaksByCat[event.Category]++

	// Log the break
	log.Printf("[CACHE-BREAK] %s: %s (hit %.1f%%→%.1f%%, Δ=%d tokens, prev=%.1f%%)",
		event.Severity, event.Category,
		prevHitRate, currentHitRate, hitDelta, prevHitRate)

	if event.Details != "" {
		log.Printf("[CACHE-BREAK] details: %s", event.Details)
	}

	// Update baseline for next comparison
	d.prevCall = &currentRecord
	d.prevState = currentState

	return event
}

// diagnoseCause identifies what changed between two prompt states.
// Claude Code: 13 categories of change detection.
func (d *CacheBreakDetector) diagnoseCause(
	current, previous *PromptState,
	cr CacheRecord,
) *CacheBreakEvent {
	event := &CacheBreakEvent{
		Time:     time.Now(),
		Severity: "minor",
		Category: BreakServerEviction, // default: assume server-side
		Details:  "",
	}

	var diffs []string

	// 1. Model change
	if current.Model != previous.Model {
		diffs = append(diffs, fmt.Sprintf("model: %s → %s", previous.Model, current.Model))
		event.Category = BreakModelChanged
		event.Severity = "major"
	}

	// 2. System prompt change
	if current.SystemPromptHash != previous.SystemPromptHash {
		diffs = append(diffs, "system prompt content changed")
		event.Category = BreakSystemPromptChanged
		event.Severity = "major"
	} else if current.SystemPromptNoCacheHash != previous.SystemPromptNoCacheHash {
		// Cache_control markers changed but content didn't
		diffs = append(diffs, "cache_control markers changed")
		event.Category = BreakCacheScopeChanged
		event.Severity = "minor"
	}

	// 3-5. Tool changes
	if current.ToolCount != previous.ToolCount {
		if current.ToolCount > previous.ToolCount {
			diffs = append(diffs, fmt.Sprintf("tools_added: %d → %d", previous.ToolCount, current.ToolCount))
			event.Category = BreakToolsAdded
		} else {
			diffs = append(diffs, fmt.Sprintf("tools_removed: %d → %d", previous.ToolCount, current.ToolCount))
			event.Category = BreakToolsRemoved
		}
		event.Severity = "major"
	} else if current.ToolNamesHash != previous.ToolNamesHash {
		// Same count, different tools (swapped)
		diffs = append(diffs, "tool set changed (same count, different names)")
		event.Category = BreakToolsAdded // effectively both added and removed
		event.Severity = "major"
	} else if current.ToolSchemaHash != previous.ToolSchemaHash {
		// Same tools, schema changed (parameter updated)
		diffs = append(diffs, "tool schema updated")
		event.Category = BreakToolSchemaChanged
		event.Severity = "minor"
	}

	// 6. Fast mode change
	if current.FastMode != previous.FastMode {
		diffs = append(diffs, fmt.Sprintf("fast_mode: %v → %v", previous.FastMode, current.FastMode))
		event.Category = BreakFastModeChanged
		event.Severity = "minor"
	}

	// 7-8. Cache control changes
	if current.CacheScope != previous.CacheScope {
		diffs = append(diffs, fmt.Sprintf("cache_scope: %s → %s", previous.CacheScope, current.CacheScope))
		event.Category = BreakCacheScopeChanged
		event.Severity = "minor"
	}
	if current.CacheTTL != previous.CacheTTL {
		diffs = append(diffs, fmt.Sprintf("cache_ttl: %s → %s", previous.CacheTTL, current.CacheTTL))
		event.Category = BreakCacheTTLChanged
		event.Severity = "minor"
	}

	// 9. Auto mode change
	if current.AutoModeEnabled != previous.AutoModeEnabled {
		diffs = append(diffs, fmt.Sprintf("auto_mode: %v → %v", previous.AutoModeEnabled, current.AutoModeEnabled))
		event.Category = BreakAutoModeChanged
		event.Severity = "minor"
	}

	// 10. Extra params change
	if current.ExtraParamsHash != previous.ExtraParamsHash {
		diffs = append(diffs, "extra params changed")
		event.Category = BreakExtraParamsChanged
		event.Severity = "minor"
	}

	// 11. Effort change
	if current.Effort != previous.Effort {
		diffs = append(diffs, fmt.Sprintf("effort: %s → %s", previous.Effort, current.Effort))
		event.Category = BreakEffortChanged
		event.Severity = "minor"
	}

	// No client-side change detected → server-side cause
	if len(diffs) == 0 {
		// Check for TTL expiry vs server eviction
		elapsed := current.Timestamp.Sub(previous.Timestamp)
		if elapsed > time.Duration(d.ttlMinutes)*time.Minute {
			event.Category = BreakTTLExpiry
			event.Severity = "info"
			diffs = append(diffs, fmt.Sprintf("TTL expired (elapsed: %v, TTL: %dmin)", elapsed.Round(time.Second), d.ttlMinutes))
		} else {
			event.Category = BreakServerEviction
			event.Severity = "info"
			diffs = append(diffs, "server-side eviction or routing change")
		}
	}

	event.Details = strings.Join(diffs, "; ")
	return event
}

// ====================== Query Interface ======================

// Health returns the current cache health summary.
func (d *CacheBreakDetector) Health() CacheHealth {
	d.mu.RLock()
	defer d.mu.RUnlock()

	h := CacheHealth{
		TotalCalls:       d.totalCalls,
		TotalBreaks:      d.totalBreaks,
		BreaksByCategory: make(map[CacheBreakCategory]int),
		RecentEvents:     make([]CacheBreakEvent, 0),
	}

	for k, v := range d.breaksByCat {
		h.BreaksByCategory[k] = v
	}

	if len(d.events) > 0 {
		last := d.events[len(d.events)-1]
		h.LastBreak = &last
		// Most recent 10 events
		start := 0
		if len(d.events) > 10 {
			start = len(d.events) - 10
		}
		for _, e := range d.events[start:] {
			h.RecentEvents = append(h.RecentEvents, e)
		}
	}

	return h
}

// RecentBreaks returns the most recent N break events.
func (d *CacheBreakDetector) RecentBreaks(n int) []CacheBreakEvent {
	d.mu.RLock()
	defer d.mu.RUnlock()

	start := 0
	if len(d.events) > n {
		start = len(d.events) - n
	}
	out := make([]CacheBreakEvent, len(d.events)-start)
	copy(out, d.events[start:])
	return out
}

// Reset clears all state (on session reset).
func (d *CacheBreakDetector) Reset() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.prevState = nil
	d.prevCall = nil
	d.events = d.events[:0]
	d.totalCalls = 0
	d.totalBreaks = 0
	d.breaksByCat = make(map[CacheBreakCategory]int)
}

// ====================== Helpers ======================

func cacheHashString(s string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(s)))[:16]
}

func cacheHashBytes(b []byte) string {
	return fmt.Sprintf("%x", sha256.Sum256(b))[:16]
}

// stripCacheControl removes cache_control markers from system prompt text.
// Used to detect if only cache_control changed while content stayed the same.
func stripCacheControl(prompt string) string {
	// Remove JSON cache_control patterns: "cache_control":{"type":"ephemeral"}
	result := prompt
	// Simple approach: remove the cache_control JSON object
	for {
		idx := strings.Index(result, `"cache_control"`)
		if idx < 0 {
			break
		}
		// Find the closing brace of the cache_control object
		start := idx
		depth := 0
		end := start
		for i := start; i < len(result); i++ {
			if result[i] == '{' {
				depth++
			} else if result[i] == '}' {
				depth--
				if depth == 0 {
					end = i + 1
					break
				}
			}
		}
		if end > start {
			// Also remove trailing comma if present
			if end < len(result) && result[end] == ',' {
				end++
			}
			result = result[:start] + result[end:]
		} else {
			break
		}
	}
	return result
}

// global detector instance
var globalCacheBreakDetector = NewCacheBreakDetector()

// ====================== Tool Ordering ======================

// GetSortedToolNames returns tool names in deterministic order:
// built-in tools first (stable prefix for cache), then extended tools alphabetically.
// Claude Code: built-ins first as stable prefix, then MCP tools.
func GetSortedToolNames(defs []map[string]interface{}, coreTools map[string]bool) []string {
	var builtins, extended []string
	for _, td := range defs {
		if fn, ok := td["function"].(map[string]interface{}); ok {
			if name, ok := fn["name"].(string); ok {
				if coreTools[name] {
					builtins = append(builtins, name)
				} else {
					extended = append(extended, name)
				}
			}
		}
	}
	sort.Strings(builtins)
	sort.Strings(extended)
	return append(builtins, extended...)
}
