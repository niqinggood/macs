package agentloop

import "time"

// TurnState owns mutable state for one user-submitted agent turn. It contains
// no Gateway, persistence, or concrete tool implementation.
type TurnState struct {
	Input         string
	TaskID        string
	StartedAt     time.Time
	Iteration     int
	MaxIterations int

	FinalOutput string
	EventStatus string

	ToolExecCount          int
	TotalToolDurationMs    int64
	ToolCallSummary        []string
	AllToolResults         []string
	CallChain              []map[string]interface{}
	FailTracker            map[string]int
	BrokenTools            map[string]bool
	FailDiagnosticInjected map[string]bool
	RecoveryTracker        *ToolRecoveryTracker
}

func NewTurnState(input, taskID string, maxIterations int) *TurnState {
	return &TurnState{
		Input:                  input,
		TaskID:                 taskID,
		StartedAt:              time.Now(),
		MaxIterations:          maxIterations,
		EventStatus:            "failed",
		FailTracker:            make(map[string]int),
		BrokenTools:            make(map[string]bool),
		FailDiagnosticInjected: make(map[string]bool),
		RecoveryTracker:        NewToolRecoveryTracker(),
	}
}

func (state *TurnState) CanContinue() bool {
	return state != nil && state.Iteration < state.MaxIterations
}

func (state *TurnState) NextIteration() int {
	if state == nil {
		return 0
	}
	state.Iteration++
	return state.Iteration
}

func (state *TurnState) RemainingIterations() int {
	if state == nil {
		return 0
	}
	return state.MaxIterations - state.Iteration
}

func (state *TurnState) IterationBudgetNotice(toolsAvailable bool) (string, bool) {
	if state == nil || state.MaxIterations <= 0 {
		return "", false
	}
	switch state.RemainingIterations() {
	case 3:
		return "[Iteration budget warning] You have 3 model calls left. Stop broad exploration, consolidate useful findings, save important artifacts if needed, and prepare the final answer. Use additional tools only for essential verification.", false
	case 0:
		if state.MaxIterations == 1 && state.Iteration == 1 && toolsAvailable {
			return "[SINGLE ITERATION TOOL WINDOW] This is the only planned task call. If the task requires an available tool, issue the necessary tool call now without narration. Otherwise produce the final answer now. Do not start optional exploration.", false
		}
		return "[FINAL ITERATION] This is the last model call before the task stops. Do NOT call any tools. Produce the best final answer now in the user's language using the information already collected. For research tasks, include findings, evidence, confidence, gaps, and next steps.", true
	default:
		return "", false
	}
}

func (state *TurnState) MarkSuccess(finalOutput string) {
	if state == nil {
		return
	}
	state.FinalOutput = finalOutput
	state.EventStatus = "success"
}

func (state *TurnState) Elapsed() time.Duration {
	if state == nil || state.StartedAt.IsZero() {
		return 0
	}
	return time.Since(state.StartedAt)
}
