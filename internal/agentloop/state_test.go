package agentloop

import "testing"

func TestTurnStateLifecycle(t *testing.T) {
	state := NewTurnState("work", "task-1", 2)
	if !state.CanContinue() || state.NextIteration() != 1 || state.RemainingIterations() != 1 {
		t.Fatalf("unexpected first iteration state: %+v", state)
	}
	if state.NextIteration() != 2 || state.CanContinue() {
		t.Fatalf("unexpected exhausted state: %+v", state)
	}
	state.MarkSuccess("done")
	if state.EventStatus != "success" || state.FinalOutput != "done" {
		t.Fatalf("unexpected success state: %+v", state)
	}
}

func TestTurnStateIterationBudget(t *testing.T) {
	state := NewTurnState("work", "task-1", 1)
	state.NextIteration()
	if notice, force := state.IterationBudgetNotice(true); notice == "" || force {
		t.Fatalf("single tool window = %q, %v", notice, force)
	}
	if notice, force := state.IterationBudgetNotice(false); notice == "" || !force {
		t.Fatalf("final iteration = %q, %v", notice, force)
	}
}
