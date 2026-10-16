package conversation

import (
	"fmt"
	"strings"
	"sync"

	"mac/internal/runtimeutil"
	"mac/internal/toolcontract"
)

// ====================== Context Management — 6-Layer Defense ======================
// 借鉴 Claude Code: multi-layer defense-in-depth for context window management.
//
// Layer 1 — Token Budget: per-turn tracking, warns at 70%, auto-continue at threshold.
// Layer 2 — API Micro-compact: clear old tool results when input tokens exceed threshold.
// Layer 3 — Sliding Window: drop oldest messages when count exceeds limit.
// Layer 4 — Auto-compact: LLM-driven structured summary when close to context limit.
// Layer 5 — Reactive Compact: aggressive recovery on API 413 / context-length errors.
// Layer 6 — Protected Tail: always preserve the N most recent messages intact.
//
// Key design decisions borrowed from Claude Code:
//   - Triggers are TOKEN-COUNT driven, not round-count driven.
//   - Lighter layers fire first to prevent heavier layers.
//   - Circuit breaker prevents infinite compaction loops.
//   - Protected tail ensures the model never loses its immediate context.

// CompressLevel indicates which layer of compression to apply.
type CompressLevel int

const (
	CompressNone     CompressLevel = iota // no action needed
	CompressMicro                         // Layer 2: prune old tool results (token-threshold)
	CompressSlide                         // Layer 3: drop oldest messages (count-threshold)
	CompressFast                          // cheap: per-message truncation, no LLM
	CompressDeep                          // Layer 4: LLM-driven structured summary
	CompressReactive                      // Layer 5: aggressive recovery after 413 error
	CompressCollapse                      // Layer 6: archive large tool results to side channel
)

func (l CompressLevel) String() string {
	switch l {
	case CompressNone:
		return "none"
	case CompressMicro:
		return "micro"
	case CompressSlide:
		return "slide"
	case CompressFast:
		return "fast"
	case CompressDeep:
		return "deep"
	case CompressReactive:
		return "reactive"
	case CompressCollapse:
		return "collapse"
	default:
		return "unknown"
	}
}

// CompressConfig controls all compression thresholds.
// All sizing thresholds are in estimated tokens, not characters.
type CompressConfig struct {
	Enabled bool `json:"enabled" yaml:"enabled"`

	// Layer 2 — Micro: clear tool results when estimated tokens exceed this
	MicroTriggerTokens int `json:"micro_trigger_tokens"` // default: 50% of context window

	// Layer 3 — Slide: drop oldest messages when count exceeds this
	SlideTriggerMsgCount int `json:"slide_trigger_msg_count"` // default: 80

	// Layer 4 — Deep: LLM-driven summary when tokens exceed (context_window - buffer)
	AutoCompactBufferTokens int `json:"auto_compact_buffer_tokens"` // default: 13000

	// Layer 5 — Reactive: retry limit after 413 errors
	MaxReactiveRetries int `json:"max_reactive_retries"` // default: 2

	// Layer 6 — Collapse: commit large tool results to side channel
	CollapseThresholdTokens int `json:"collapse_threshold_tokens"` // default: 20000 per tool result

	// Circuit breaker
	MaxConsecutiveFailures int `json:"max_consecutive_failures"` // default: 3

	// Protected tail: always keep this many messages at the end intact
	TailProtectMessages int `json:"tail_protect_messages"` // default: 8

	// Summary: max tokens for the LLM-generated summary
	SummaryMaxTokens int `json:"summary_max_tokens"` // default: 2000

	// Context window size (model-specific)
	ContextWindowTokens int `json:"context_window_tokens"` // default: 128000 (DeepSeek V3)

	// Token budget: auto-continue when budget used (Claude Code: up to +500K)
	TokenBudgetWarningPct float64 `json:"token_budget_warning_pct"` // default: 0.70 (70%)
	TokenBudgetHardPct    float64 `json:"token_budget_hard_pct"`    // default: 0.92 (92%)
	MaxAutoContinueTokens int     `json:"max_auto_continue_tokens"` // default: 500000
}

// DefaultCompressConfig returns sensible defaults for DeepSeek V3 (128K context).
func DefaultCompressConfig() CompressConfig {
	return CompressConfig{
		Enabled:                 true,
		MicroTriggerTokens:      24000,
		SlideTriggerMsgCount:    48,
		AutoCompactBufferTokens: 13000, // Claude Code uses 13K buffer
		MaxReactiveRetries:      2,
		CollapseThresholdTokens: 8000,
		MaxConsecutiveFailures:  3,
		TailProtectMessages:     8,
		SummaryMaxTokens:        2000,
		ContextWindowTokens:     128000,
		TokenBudgetWarningPct:   0.70,
		TokenBudgetHardPct:      0.92,
		MaxAutoContinueTokens:   500000,
	}
}

// CompressState tracks runtime compression state.
type CompressState struct {
	mu sync.Mutex

	// Token budget tracking
	totalInputTokens  int // cumulative input tokens this session
	totalOutputTokens int // cumulative output tokens this session
	autoContinueCount int // number of auto-continues issued

	// Compression tracking
	totalRounds             int
	lastDeepRound           int
	consecutiveFailures     int
	totalCompressions       int
	totalReactiveRecoveries int

	// Circuit breaker
	circuitOpen bool   // when true, skip LLM compression entirely
	lastError   string // reason the circuit opened
}

// ContextManager orchestrates the multi-layer compression pipeline.
// Layers fire from lightest to heaviest; lighter layers prevent heavier ones.
type ContextManager struct {
	config                  CompressConfig
	state                   CompressState
	smartCompactorAvailable bool
}

// SetSmartCompactorAvailable records whether the host has wired an LLM compactor.
func (cm *ContextManager) SetSmartCompactorAvailable(available bool) {
	cm.smartCompactorAvailable = available
}

// HasSmartCompactor returns true if LLM-driven compaction is available.
func (cm *ContextManager) HasSmartCompactor() bool {
	return cm.smartCompactorAvailable
}

// NewContextManager creates a new context manager.
func NewContextManager(cfg CompressConfig) *ContextManager {
	if cfg.ContextWindowTokens <= 0 {
		cfg.ContextWindowTokens = 128000
	}
	if cfg.MicroTriggerTokens <= 0 {
		cfg.MicroTriggerTokens = cfg.ContextWindowTokens / 2
	}
	if cfg.AutoCompactBufferTokens <= 0 {
		cfg.AutoCompactBufferTokens = 13000
	}
	if cfg.TailProtectMessages <= 0 {
		cfg.TailProtectMessages = 8
	}
	return &ContextManager{config: cfg}
}

// ====================== Layer 1: Token Budget ======================

// TokenBudget tracks per-session token usage and signals when action is needed.
type TokenBudget struct {
	EstimatedInput  int
	EstimatedOutput int
	ContextWindow   int
	WarningPct      float64
	HardPct         float64
}

// Status returns the current budget status.
func (tb *TokenBudget) Status() string {
	pct := float64(tb.EstimatedInput) / float64(tb.ContextWindow)
	switch {
	case pct >= tb.HardPct:
		return "critical"
	case pct >= tb.WarningPct:
		return "warning"
	default:
		return "ok"
	}
}

// ShouldCompress returns true if compression should be attempted.
func (tb *TokenBudget) ShouldCompress(bufferTokens int) bool {
	return tb.EstimatedInput > (tb.ContextWindow - bufferTokens)
}

// ShouldAutoContinue returns true if auto-continue should be issued.
func (tb *TokenBudget) ShouldAutoContinue(maxBudget int) bool {
	return tb.EstimatedInput+tb.EstimatedOutput < maxBudget
}

// ====================== Layer 2-4: Compression Pipeline ======================

// OnTurn evaluates the current state and returns the recommended compression level.
// This replaces the old round-count-based trigger with token-count-based evaluation.
func (cm *ContextManager) OnTurn(msgCount int, estimatedTokens int) CompressLevel {
	cm.state.mu.Lock()
	cm.state.totalRounds++
	round := cm.state.totalRounds
	circuitOpen := cm.state.circuitOpen
	cm.state.mu.Unlock()

	if circuitOpen {
		return CompressFast // circuit breaker: fall back to safe compression
	}

	if !cm.config.Enabled {
		return CompressNone
	}

	// Layer 2: Micro — token threshold based, not round based
	if estimatedTokens > cm.config.MicroTriggerTokens {
		return CompressMicro
	}

	// Layer 3: Slide — message count threshold
	if msgCount > cm.config.SlideTriggerMsgCount {
		return CompressSlide
	}

	// Layer 4: Deep — close to context limit
	if estimatedTokens > (cm.config.ContextWindowTokens - cm.config.AutoCompactBufferTokens) {
		cm.state.mu.Lock()
		if cm.state.consecutiveFailures >= cm.config.MaxConsecutiveFailures {
			cm.state.circuitOpen = true
			cm.state.lastError = "consecutive LLM compaction failures exceeded limit"
			cm.state.mu.Unlock()
			return CompressFast
		}
		cm.state.mu.Unlock()
		return CompressDeep
	}

	_ = round
	return CompressNone
}

// OnReactiveError handles Layer 5: recovery after API 413 / context-length error.
// Returns true if a retry should be attempted with aggressive compression.
func (cm *ContextManager) OnReactiveError() (shouldRetry bool, retryLevel CompressLevel) {
	cm.state.mu.Lock()
	defer cm.state.mu.Unlock()

	cm.state.totalReactiveRecoveries++
	if cm.state.totalReactiveRecoveries > cm.config.MaxReactiveRetries {
		return false, CompressNone
	}

	return true, CompressReactive
}

// OnCompressSuccess records a successful compression.
func (cm *ContextManager) OnCompressSuccess(level CompressLevel) {
	cm.state.mu.Lock()
	defer cm.state.mu.Unlock()

	if level >= CompressDeep {
		cm.state.consecutiveFailures = 0
		cm.state.lastDeepRound = cm.state.totalRounds
		cm.state.totalCompressions++
	}
}

// OnCompressFailure records a failed compression.
func (cm *ContextManager) OnCompressFailure(level CompressLevel, err error) {
	cm.state.mu.Lock()
	defer cm.state.mu.Unlock()

	if level >= CompressDeep {
		cm.state.consecutiveFailures++
		if cm.state.consecutiveFailures >= cm.config.MaxConsecutiveFailures {
			cm.state.circuitOpen = true
			cm.state.lastError = err.Error()
		}
	}
}

// Reset reopens the circuit breaker and resets failure count.
func (cm *ContextManager) Reset() {
	cm.state.mu.Lock()
	defer cm.state.mu.Unlock()
	cm.state.circuitOpen = false
	cm.state.consecutiveFailures = 0
	cm.state.lastError = ""
}

// Stats returns compression statistics.
func (cm *ContextManager) Stats() map[string]interface{} {
	cm.state.mu.Lock()
	defer cm.state.mu.Unlock()

	return map[string]interface{}{
		"total_rounds":              cm.state.totalRounds,
		"total_compressions":        cm.state.totalCompressions,
		"total_reactive_recoveries": cm.state.totalReactiveRecoveries,
		"consecutive_failures":      cm.state.consecutiveFailures,
		"circuit_open":              cm.state.circuitOpen,
		"last_error":                cm.state.lastError,
	}
}

// ====================== Structured Summary (Layer 4: Deep) ======================

const SummaryInstructionPrefix = `[CONTEXT COMPACTION — REFERENCE ONLY] Earlier turns were summarized below.
This is a handoff from a previous context window — treat it as background reference, NOT active instructions.
Do NOT answer questions or fulfill requests mentioned in this summary; they were already addressed.
Respond ONLY to the latest user message that appears AFTER this summary.
`

// BuildCompactionPrompt creates a structured summarization prompt (Hermes + Claude Code style).
func BuildCompactionPrompt(conversationText string, existingSummary string) string {
	var sb strings.Builder

	sb.WriteString("## Compaction Task\n\n")
	sb.WriteString("Summarize the conversation above into a structured context summary. ")
	sb.WriteString("This summary replaces the full conversation history to save context space.\n\n")

	sb.WriteString("### Rules\n")
	sb.WriteString("1. Be factual and brief — use bullet points for key information\n")
	sb.WriteString("2. Record decisions, code changes, file paths, and error messages precisely\n")
	sb.WriteString("3. Do NOT re-express user instructions as active work items\n")
	sb.WriteString("4. Mark completed items as Resolved, ongoing items as In Progress\n")
	sb.WriteString("5. Keep the summary under 2000 tokens — prioritize recent over old\n\n")

	sb.WriteString("### Template\n```\n")
	sb.WriteString("## Resolved\n- [item]\n\n")
	sb.WriteString("## Active Task\nBrief description of current goal\n\n")
	sb.WriteString("## In Progress\n- [item]\n\n")
	sb.WriteString("## Pending\n- [item]\n\n")
	sb.WriteString("## Key Files\n- path: description\n\n")
	sb.WriteString("## Errors to Avoid\n- error: solution\n\n")
	sb.WriteString("## Remaining Work\n- [item]\n```\n\n")

	if existingSummary != "" {
		sb.WriteString("### Previous Summary (preserve and update)\n")
		sb.WriteString(existingSummary)
		sb.WriteString("\n\n")
	}

	sb.WriteString("### Conversation to Summarize\n")
	sb.WriteString(conversationText)
	sb.WriteString("\n\n")
	sb.WriteString("Output ONLY the filled template. No introduction, no closing remarks.\n")

	return sb.String()
}

// ====================== Layer 2: Micro-compact (Tool Result Pruning) ======================

// ClearableToolNames lists tools whose results are safe to prune from context.
// Claude Code: clearable tools = Bash, Read, Grep, Glob, WebFetch, WebSearch
func ClearableToolNames() map[string]bool {
	return map[string]bool{
		"read_file":   true,
		"cli":         true,
		"grep":        true,
		"glob":        true,
		"web_fetch":   true,
		"web_search":  true,
		"list_dir":    true,
		"lsp_hover":   true,
		"lsp_symbols": true,
	}
}

// PruneToolResults replaces old tool results with placeholders.
// keepRecent: number of most recent tool-call pairs to preserve intact.
// clearable: if true, only prunes results from "clearable" tools (safe to clear).
//   - Read, Bash, Grep results are clearable.
//   - Edit, Write results are NOT clearable (the model needs them).
//
// protectTail: number of messages at the very end to never touch.
// Returns the number of results pruned.
func PruneToolResults(messages []map[string]interface{}, keepRecent int, clearableOnly bool, protectTail int) int {
	if len(messages) <= protectTail {
		return 0
	}

	clearable := ClearableToolNames()
	tailStart := len(messages) - protectTail
	pruned := 0
	placeholder := "[Old result cleared to save context]"

	// Count tool calls backwards from tail to find which ones to keep.
	toolCallIDsToKeep := make(map[string]bool)
	toolCallCount := 0
	for i := tailStart - 1; i >= 0; i-- {
		msg := messages[i]
		if _, hasToolCalls := msg["tool_calls"]; hasToolCalls {
			toolCallCount++
			if toolCallCount <= keepRecent {
				// Mark this tool call's results to keep
				for _, call := range toolcontract.ToolCallMaps(msg["tool_calls"]) {
					if callID, ok := call["id"].(string); ok {
						toolCallIDsToKeep[callID] = true
					}
				}
			}
		}
	}

	// Prune tool results that are (a) not in protected tail, (b) not associated with recent tool calls
	for i := 0; i < tailStart; i++ {
		msg := messages[i]
		role, _ := msg["role"].(string)
		if role != "tool" {
			continue
		}

		toolName, _ := msg["name"].(string)
		callID, _ := msg["tool_call_id"].(string)

		// Skip if this tool call's results should be kept
		if toolCallIDsToKeep[callID] {
			continue
		}

		// If clearableOnly, only prune clearable tool results
		if clearableOnly && !clearable[toolName] {
			continue
		}

		// Replace with compact placeholder
		messages[i] = map[string]interface{}{
			"role":         "tool",
			"tool_call_id": callID,
			"name":         toolName,
			"content":      placeholder,
			"_pruned":      true,
		}
		pruned++
	}

	return pruned
}

// PruneToolResultsReactive is an aggressive version for Layer 5: Reactive.
// Clears ALL tool results (not just clearable ones) except the most recent N.
func PruneToolResultsReactive(messages []map[string]interface{}, keepRecent int, protectTail int) int {
	return PruneToolResults(messages, keepRecent, false, protectTail)
}

// ====================== Layer 3: Sliding Window ======================

// SlideWindow drops the oldest messages, preserving the tail.
// Returns the new slice and count of dropped messages.
func SlideWindow(messages []map[string]interface{}, maxCount int, protectTail int) ([]map[string]interface{}, int) {
	if len(messages) <= maxCount {
		return messages, 0
	}

	dropCount := len(messages) - maxCount
	if dropCount <= 0 {
		return messages, 0
	}

	// Ensure we don't drop into the protected tail
	availableToDrop := len(messages) - protectTail
	if dropCount > availableToDrop {
		dropCount = availableToDrop
	}
	if dropCount <= 0 {
		return messages, 0
	}

	return messages[dropCount:], dropCount
}

// ====================== Layer 6: Context Collapse ======================

// CollapseThreshold is the per-tool-result token count above which we archive to side channel.
const CollapseThresholdTokens = 8000

// ShouldCollapseResult checks if a tool result is large enough to warrant collapse.
func ShouldCollapseResult(content string) bool {
	return runtimeutil.EstimateTokens(content) > CollapseThresholdTokens
}

// CollapseResult replaces a huge tool result with a summary + side-channel reference.
func CollapseResult(content string, toolName string, collapseID string) (collapsedMsg string, sideChannelContent string) {
	tokens := runtimeutil.EstimateTokens(content)
	if tokens <= CollapseThresholdTokens {
		return content, ""
	}

	// Store the full content as side-channel data
	sideChannelContent = content

	// Replace with a compact reference
	preview := content
	if len(preview) > 200 {
		preview = preview[:200] + "..."
	}
	collapsedMsg = fmt.Sprintf(
		"[Large %s result: %d tokens. Full content archived to side channel #%s]\nPreview: %s",
		toolName, tokens, collapseID, preview,
	)
	return collapsedMsg, sideChannelContent
}

// ====================== Helpers ======================

// buildCompactSummary generates a preview-like summary of messages.
func buildCompactSummary(messages []map[string]interface{}, maxChars int) string {
	var sb strings.Builder
	charCount := 0

	for _, msg := range messages {
		role, _ := msg["role"].(string)
		content, _ := msg["content"].(string)
		if content == "" {
			continue
		}

		truncated := content
		if len(content) > 300 {
			truncated = content[:300] + "..."
		}

		line := fmt.Sprintf("[%s] %s\n", role, truncated)
		if charCount+len(line) > maxChars {
			sb.WriteString(fmt.Sprintf("... (%d more messages omitted)\n", len(messages)))
			break
		}
		sb.WriteString(line)
		charCount += len(line)
	}

	return sb.String()
}

// contextWindowFromModel returns the context window size for common models.
func contextWindowFromModel(model string) int {
	switch {
	case strings.Contains(model, "deepseek-v3"):
		return 128000
	case strings.Contains(model, "deepseek-r1"):
		return 128000
	case strings.Contains(model, "deepseek-chat"):
		return 128000
	case strings.Contains(model, "gpt-4o"):
		return 128000
	case strings.Contains(model, "gpt-4-turbo"):
		return 128000
	case strings.Contains(model, "claude-3"):
		return 200000
	case strings.Contains(model, "claude-4"):
		return 200000
	case strings.Contains(model, "qwen"):
		return 131072
	default:
		return 128000
	}
}
