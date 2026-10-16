package app

import "testing"

func TestShouldSkipMemoryEventForSubAgentPrompt(t *testing.T) {
	event := &MemoryEvent{
		UserQuestion: "You are a sub-agent named \"verification_1\" working on a delegated task.\n\n## Parent Task Context\nverify",
		FinalAnswer:  "VERDICT: PASS",
	}
	if !shouldSkipMemoryEvent(event) {
		t.Fatal("internal sub-agent memory event should be skipped")
	}
}
