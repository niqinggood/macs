package app

import (
	"strings"
	"testing"
)

func TestPPTToolFailureCircuitAllowsVariedRecoveryWithinEightyCalls(t *testing.T) {
	if toolConsecutiveFailureBlockAfter != 80 {
		t.Fatalf("tool failure budget = %d, want 80", toolConsecutiveFailureBlockAfter)
	}
	if toolConsecutiveFailureBlockAfter != ptcCallLimitPerRun {
		t.Fatalf("tool failure budget = %d, PTC limit = %d", toolConsecutiveFailureBlockAfter, ptcCallLimitPerRun)
	}

	guard := NewToolCallLoopGuard(2)
	call := ToolCall{Name: "cli", Arguments: map[string]interface{}{"command": "python3 corrected_builder.py"}}
	if decision := guard.CheckWithFailureCircuit(
		call,
		map[string]int{},
		map[string]int{"cli": toolConsecutiveFailureBlockAfter - 1},
	); decision.Blocked {
		t.Fatalf("varied recovery call was blocked before the 80-call budget: %#v", decision)
	}

	decision := guard.CheckWithFailureCircuit(
		call,
		map[string]int{},
		map[string]int{"cli": toolConsecutiveFailureBlockAfter},
	)
	if !decision.Blocked || !strings.Contains(decision.Result.ErrorCode, "circuit") {
		t.Fatalf("80-call circuit was not enforced: %#v", decision)
	}
}
