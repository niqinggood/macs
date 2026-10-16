package runtimeutil

import (
	"fmt"
	"sync"
	"time"
)

// ====================== Token Budget with Diminishing Returns ======================
// Claude Code 借鉴: tokenBudget.ts in query/
//
// Two thresholds control auto-continuation:
//   1. Percentage-based: at 90% budget, inject a "wrap up" nudge
//   2. Diminishing returns: if token delta < threshold for 3+ continuations, STOP
//
// Sub-agents are excluded from continuation logic entirely.

const (
	DefaultBudgetWarningPct     = 0.70
	DefaultBudgetHardPct        = 0.92
	DefaultMaxAutoContinue      = 500000
	DefaultContinuationNudge    = 0.90
	DefaultDiminishingThreshold = 500 // tokens per continuation
	DefaultDiminishingStrikes   = 3   // consecutive low-delta continuations
)

// BudgetTracker tracks continuation and diminishing returns state.
type BudgetTracker struct {
	mu                   sync.Mutex
	continuationCount    int
	lastTurnTokens       int
	lastGlobalTurnTokens int
	totalInputTokens     int
	totalOutputTokens    int
	totalBudget          int // max tokens for this session
	startedAt            time.Time
	diminishingStrikes   int // consecutive continuations with low delta
}

// NewBudgetTracker creates a budget tracker for the given token limit.
func NewBudgetTracker(maxTokens int) *BudgetTracker {
	return &BudgetTracker{
		totalBudget: maxTokens,
		startedAt:   time.Now(),
	}
}

// TrackTurn records a completed turn's token counts.
func (bt *BudgetTracker) TrackTurn(inputTokens, outputTokens int) {
	bt.mu.Lock()
	defer bt.mu.Unlock()
	bt.totalInputTokens += inputTokens
	bt.totalOutputTokens += outputTokens
	bt.lastTurnTokens = inputTokens + outputTokens
}

// ContinuationDecision is the result of a continuation budget check.
type ContinuationDecision struct {
	Action          string  `json:"action"` // "continue", "stop"
	NudgeMessage    string  `json:"nudge_message,omitempty"`
	ContinuationNo  int     `json:"continuation_no"`
	BudgetUsedPct   float64 `json:"budget_used_pct"`
	DiminishingFlag bool    `json:"diminishing_flag"`
}

// ShouldContinue checks if the model should continue after hitting max_output_tokens.
// Returns a decision with optional nudge message.
// isSubAgent should be true for sub-agents (excluded from continuation logic).
func (bt *BudgetTracker) ShouldContinue(isSubAgent bool) ContinuationDecision {
	bt.mu.Lock()
	defer bt.mu.Unlock()

	if isSubAgent {
		return ContinuationDecision{Action: "stop", DiminishingFlag: false}
	}

	totalUsed := bt.totalInputTokens + bt.totalOutputTokens
	pctUsed := float64(totalUsed) / float64(bt.totalBudget)

	if totalUsed >= bt.totalBudget {
		return ContinuationDecision{
			Action:          "stop",
			ContinuationNo:  bt.continuationCount,
			BudgetUsedPct:   pctUsed,
			DiminishingFlag: false,
		}
	}

	// Check diminishing returns
	delta := bt.lastTurnTokens
	if bt.continuationCount > 0 && delta < DefaultDiminishingThreshold {
		bt.diminishingStrikes++
	} else {
		bt.diminishingStrikes = 0
	}

	if bt.diminishingStrikes >= DefaultDiminishingStrikes {
		return ContinuationDecision{
			Action:          "stop",
			ContinuationNo:  bt.continuationCount,
			BudgetUsedPct:   pctUsed,
			DiminishingFlag: true,
		}
	}

	// Percentage threshold: inject nudge at 90%
	if pctUsed >= DefaultContinuationNudge {
		bt.continuationCount++
		nudge := fmt.Sprintf(
			"[Token Budget: %.0f%% used (%d/%d), continuation %d/%d. "+
				"Wrap up the current task and provide final results.]",
			pctUsed*100, totalUsed, bt.totalBudget,
			bt.continuationCount, DefaultDiminishingStrikes,
		)
		return ContinuationDecision{
			Action:          "continue",
			NudgeMessage:    nudge,
			ContinuationNo:  bt.continuationCount,
			BudgetUsedPct:   pctUsed,
			DiminishingFlag: false,
		}
	}

	// Still have budget, continue silently
	bt.continuationCount++
	return ContinuationDecision{
		Action:         "continue",
		ContinuationNo: bt.continuationCount,
		BudgetUsedPct:  pctUsed,
	}
}

// Stats returns budget statistics for monitoring.
func (bt *BudgetTracker) Stats() map[string]interface{} {
	bt.mu.Lock()
	defer bt.mu.Unlock()
	return map[string]interface{}{
		"total_input":         bt.totalInputTokens,
		"total_output":        bt.totalOutputTokens,
		"total":               bt.totalInputTokens + bt.totalOutputTokens,
		"budget":              bt.totalBudget,
		"pct_used":            float64(bt.totalInputTokens+bt.totalOutputTokens) / float64(bt.totalBudget),
		"continuations":       bt.continuationCount,
		"diminishing_strikes": bt.diminishingStrikes,
		"elapsed":             time.Since(bt.startedAt).String(),
	}
}

// Reset clears the tracker for a new session.
func (bt *BudgetTracker) Reset() {
	bt.mu.Lock()
	defer bt.mu.Unlock()
	bt.continuationCount = 0
	bt.diminishingStrikes = 0
	bt.totalInputTokens = 0
	bt.totalOutputTokens = 0
	bt.startedAt = time.Now()
}
