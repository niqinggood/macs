package middleware

import (
	"fmt"
	"strings"

	"mac/internal/runtimeutil"
)

// TokenBudgetConfig controls per-run token budget enforcement.
// Zero values mean the feature is disabled.
type TokenBudgetConfig struct {
	Enabled           bool    `json:"enabled" yaml:"enabled"`
	MaxTokens         int     `json:"max_tokens" yaml:"max_tokens"`                                   // Total token limit per run
	MaxInputTokens    int     `json:"max_input_tokens,omitempty" yaml:"max_input_tokens,omitempty"`   // Optional input-only limit
	MaxOutputTokens   int     `json:"max_output_tokens,omitempty" yaml:"max_output_tokens,omitempty"` // Optional output-only limit
	WarnThreshold     float64 `json:"warn_threshold" yaml:"warn_threshold"`                           // Fraction of limit that triggers warning (0.8 = 80%)
	HardStopThreshold float64 `json:"hard_stop_threshold" yaml:"hard_stop_threshold"`                 // Fraction of limit that triggers force-stop (1.0 = 100%)
}

// DefaultTokenBudgetConfig returns sensible defaults (disabled).
func DefaultTokenBudgetConfig() TokenBudgetConfig {
	return TokenBudgetConfig{
		Enabled:           false,
		MaxTokens:         200000,
		WarnThreshold:     0.8,
		HardStopThreshold: 1.0,
	}
}

// TokenBudgetMiddleware enforces per-run token limits.
// Two levels:
//   - warn_threshold: injects a "wrap up" message for the LLM to see on its next call
//   - hard_stop_threshold: strips tool_calls from the LLM response, forcing a text answer
//
// Usage: call CountBeforeLLM before each model call and AfterLLMResponse after.
// The middleware tracks cumulative usage internally keyed by session/task ID.
type TokenBudgetMiddleware struct {
	Base
	config           TokenBudgetConfig
	estimateMessages func([]map[string]interface{}) int

	// Per-session state
	inputTokens  map[string]int // sessionID → cumulative input tokens
	outputTokens map[string]int // sessionID → cumulative output tokens
	warned       map[string]bool
	hardStopped  map[string]bool
}

func NewTokenBudgetMiddleware(cfg TokenBudgetConfig, estimateMessages func([]map[string]interface{}) int) *TokenBudgetMiddleware {
	if !cfg.Enabled {
		return nil
	}
	if cfg.WarnThreshold <= 0 {
		cfg.WarnThreshold = 0.8
	}
	if cfg.HardStopThreshold <= 0 {
		cfg.HardStopThreshold = 1.0
	}
	if cfg.MaxTokens == 0 {
		cfg.MaxTokens = 200000
	}
	return &TokenBudgetMiddleware{
		Base:             NewBase("token_budget"),
		config:           cfg,
		estimateMessages: estimateMessages,
		inputTokens:      make(map[string]int),
		outputTokens:     make(map[string]int),
		warned:           make(map[string]bool),
		hardStopped:      make(map[string]bool),
	}
}

// CountBeforeLLM estimates tokens for the messages about to be sent.
// Returns a warning message to inject, or empty string.
func (m *TokenBudgetMiddleware) CountBeforeLLM(sessionID string, messages []map[string]interface{}) string {
	if m == nil {
		return ""
	}

	estimate := 0
	if m.estimateMessages != nil {
		estimate = m.estimateMessages(messages)
	} else {
		for _, message := range messages {
			estimate += runtimeutil.EstimateTokens(fmt.Sprint(message["content"])) + 4
		}
	}
	m.inputTokens[sessionID] += estimate

	total := m.inputTokens[sessionID] + m.outputTokens[sessionID]
	maxTokens := m.config.MaxTokens

	// Check hard stop first
	if maxTokens > 0 {
		hardLimit := int(float64(maxTokens) * m.config.HardStopThreshold)
		if total >= hardLimit {
			m.hardStopped[sessionID] = true
			return fmt.Sprintf(
				"[TOKEN BUDGET EXCEEDED] Total token usage (%d) has exceeded the safety limit (%d). "+
					"DO NOT call any tools. Produce a final answer immediately using the information already collected.",
				total, hardLimit,
			)
		}
	}

	// Check warning threshold
	if !m.warned[sessionID] && maxTokens > 0 {
		warnLimit := int(float64(maxTokens) * m.config.WarnThreshold)
		if total >= warnLimit {
			m.warned[sessionID] = true
			pct := float64(total) / float64(maxTokens) * 100
			return fmt.Sprintf(
				"[TOKEN BUDGET WARNING] You have used %d of your %d token budget (%.0f%%). "+
					"Wrap up your current work and produce a final answer soon. "+
					"Avoid starting new tool calls unless necessary.",
				total, maxTokens, pct,
			)
		}
	}

	return ""
}

// AfterLLMResponse records output tokens and returns true if tool_calls should be stripped.
func (m *TokenBudgetMiddleware) AfterLLMResponse(sessionID string, responseText string) (shouldHardStop bool) {
	if m == nil {
		return false
	}

	m.outputTokens[sessionID] += runtimeutil.EstimateTokens(responseText)
	return m.hardStopped[sessionID]
}

// ShouldStripToolCalls returns true if the current response's tool_calls should be
// removed to force a text-only answer.
func (m *TokenBudgetMiddleware) ShouldStripToolCalls(sessionID string) bool {
	if m == nil {
		return false
	}
	return m.hardStopped[sessionID]
}

// Usage returns total tokens used for a session.
func (m *TokenBudgetMiddleware) Usage(sessionID string) (input, output, total int) {
	if m == nil {
		return 0, 0, 0
	}
	input = m.inputTokens[sessionID]
	output = m.outputTokens[sessionID]
	total = input + output
	return
}

// Reset clears tracking for a session.
func (m *TokenBudgetMiddleware) Reset(sessionID string) {
	if m == nil {
		return
	}
	delete(m.inputTokens, sessionID)
	delete(m.outputTokens, sessionID)
	delete(m.warned, sessionID)
	delete(m.hardStopped, sessionID)
}

func (m *TokenBudgetMiddleware) IsWarned(sessionID string) bool {
	return m != nil && m.warned[sessionID]
}

func (m *TokenBudgetMiddleware) IsHardStopped(sessionID string) bool {
	return m != nil && m.hardStopped[sessionID]
}

// BeforeModel injects the token budget warning into the system prompt if needed.
func (m *TokenBudgetMiddleware) BeforeModel(ctx *Context) error {
	if m == nil {
		return nil
	}

	sessionID := ctx.SessionID
	if sessionID == "" {
		sessionID = ctx.TaskID
	}

	// Count input tokens and check thresholds
	warning := m.CountBeforeLLM(sessionID, ctx.Messages)
	if warning != "" {
		if m.hardStopped[sessionID] {
			// Inject as a system message at the end of messages
			ctx.Messages = append(ctx.Messages, map[string]interface{}{
				"role":    "system",
				"content": warning,
			})
		} else {
			// Inject into system prompt as a gentler reminder
			if !strings.Contains(ctx.SystemPrompt, warning) {
				ctx.SystemPrompt += "\n\n" + warning
			}
		}
	}
	return nil
}

// AfterModel strips tool_calls when hard_stop is active.
func (m *TokenBudgetMiddleware) AfterModel(ctx *Context) error {
	if m == nil {
		return nil
	}

	sessionID := ctx.SessionID
	if sessionID == "" {
		sessionID = ctx.TaskID
	}

	// Find last assistant message and record output tokens
	for i := len(ctx.Messages) - 1; i >= 0; i-- {
		msg := ctx.Messages[i]
		role, _ := msg["role"].(string)
		if role != "assistant" {
			continue
		}
		content, _ := msg["content"].(string)
		_ = m.AfterLLMResponse(sessionID, content)

		if m.ShouldStripToolCalls(sessionID) {
			// Strip tool_calls to force text-only answer
			delete(msg, "tool_calls")
			delete(msg, "function_call")
			if content != "" {
				msg["content"] = "[FORCED STOP: Token budget exceeded] " + content
			}
			ctx.Messages[i] = msg
		}
		break
	}
	return nil
}
