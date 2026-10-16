package app

import (
	"strings"
	"testing"
)

func TestGuardrailDecisionExecutionSemantics(t *testing.T) {
	if !(GuardrailDecision{Action: ActionAllow}).AllowsExecution() {
		t.Fatal("allow decision should execute")
	}
	if !(GuardrailDecision{Action: ActionWarn}).AllowsExecution() {
		t.Fatal("warn decision should execute")
	}
	if (GuardrailDecision{Action: ActionBlock}).AllowsExecution() {
		t.Fatal("block decision must not execute")
	}
	if !(GuardrailDecision{Action: ActionHalt}).ShouldHaltTurn() {
		t.Fatal("halt decision must halt the turn")
	}
}

func TestToolGuardrailBlocksExactFailedCallAtConfiguredThreshold(t *testing.T) {
	cfg := DefaultGuardrailConfig()
	cfg.ExactFailureBlockAfter = 2
	controller := NewToolGuardrailController(cfg)
	args := map[string]interface{}{"command": "bad"}

	controller.RecordAfter("cli", args, "connection refused", nil, boolPointer(false))
	controller.RecordAfter("cli", args, "connection refused", nil, boolPointer(false))

	decision := controller.CheckBefore("cli", args)
	if decision.Action != ActionBlock || decision.AllowsExecution() {
		t.Fatalf("expected exact failure block, got %#v", decision)
	}
}

func TestDefaultToolGuardrailAllowsCorrectableRetriesBeforeBlockingLoop(t *testing.T) {
	controller := NewToolGuardrailController(DefaultGuardrailConfig())
	args := map[string]interface{}{"command": "local preparation"}

	for i := 0; i < 4; i++ {
		controller.RecordAfter("cli", args, "path or launcher mismatch", nil, boolPointer(false))
	}
	decision := controller.CheckBefore("cli", args)
	if !decision.AllowsExecution() || decision.Action != ActionWarn {
		t.Fatalf("correctable local retry should still be allowed before the default loop boundary: %#v", decision)
	}

	controller.RecordAfter("cli", args, "path or launcher mismatch", nil, boolPointer(false))
	decision = controller.CheckBefore("cli", args)
	if decision.Action != ActionBlock || decision.AllowsExecution() {
		t.Fatalf("unchanged sixth attempt should be blocked after five exact failures: %#v", decision)
	}
}

func TestToolGuardrailMutationInvalidatesStaleExactFailures(t *testing.T) {
	cfg := DefaultGuardrailConfig()
	cfg.ExactFailureBlockAfter = 2
	controller := NewToolGuardrailController(cfg)
	buildArgs := map[string]interface{}{"command": "python3 work/build_deck.py"}

	controller.RecordAfter("persistent_shell", buildArgs, "grounding failed", nil, boolPointer(false))
	controller.RecordAfter("persistent_shell", buildArgs, "grounding failed", nil, boolPointer(false))
	if decision := controller.CheckBefore("persistent_shell", buildArgs); decision.Action != ActionBlock {
		t.Fatalf("expected stale build signature to be blocked before correction: %#v", decision)
	}

	controller.RecordAfter(
		"write_file",
		map[string]interface{}{"path": "work/deck_spec.json", "content": `{"slides":[]}`},
		"wrote deck_spec.json",
		nil,
		boolPointer(true),
	)
	if decision := controller.CheckBefore("persistent_shell", buildArgs); !decision.AllowsExecution() || decision.Action != ActionAllow {
		t.Fatalf("successful state correction did not invalidate the old build failure: %#v", decision)
	}
}

func TestPreActionRecoveryHintStartsAfterThreeMatchingFailures(t *testing.T) {
	controller := NewToolGuardrailController(DefaultGuardrailConfig())
	args := map[string]interface{}{"command": "prepare"}
	call := []ToolCall{{Name: "cli", Arguments: args}}

	for i := 0; i < 2; i++ {
		controller.RecordAfter("cli", args, "launcher mismatch", nil, boolPointer(false))
	}
	if warnings := controller.PreActionGate(call); len(warnings) != 0 {
		t.Fatalf("two correctable failures should not trigger the recovery gate: %#v", warnings)
	}

	controller.RecordAfter("cli", args, "launcher mismatch", nil, boolPointer(false))
	warnings := controller.PreActionGate(call)
	if len(warnings) != 1 || !strings.Contains(warnings[0].Message, "local and read-only setup may continue") {
		t.Fatalf("third failure should produce a scoped recovery hint: %#v", warnings)
	}
}

func TestToolGuardrailNormalizesMissingEnabledThresholds(t *testing.T) {
	controller := NewToolGuardrailController(GuardrailConfig{Enabled: true})
	decision := controller.CheckBefore("cli", map[string]interface{}{"command": "echo ok"})
	if decision.Action != ActionAllow {
		t.Fatalf("zero-value enabled guardrail blocked the first call: %#v", decision)
	}
	args := map[string]interface{}{"command": "echo ok"}
	for range DefaultGuardrailConfig().ExactFailureBlockAfter {
		controller.RecordAfter("cli", args, "failed", nil, boolPointer(false))
	}
	if decision := controller.CheckBefore("cli", args); decision.Action != ActionBlock {
		t.Fatalf("zero-value thresholds were not normalized to the enabled defaults: %#v", decision)
	}
}

func TestBuildToolRetryGuardControlIncludesLegacyFailureAndStaysCompact(t *testing.T) {
	control := buildToolRetryGuardControl(
		[]GuardrailDecision{{
			Action:   ActionWarn,
			Code:     "pre_action_gate",
			Message:  "cli failed twice",
			ToolName: "cli",
		}},
		[]ToolCall{{Name: "ptc_execute"}},
		map[string]int{"ptc_execute:network": 3},
	)
	for _, want := range []string{"cli failed twice", "ptc_execute", "network", "materially different"} {
		if !strings.Contains(control, want) {
			t.Fatalf("retry control missing %q: %s", want, control)
		}
	}
}

func boolPointer(value bool) *bool {
	return &value
}
