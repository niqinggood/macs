package middleware

import (
	"encoding/json"
	"fmt"
	"testing"

	"mac/internal/toolcontract"
)

func runLoopDetectionToolResult(
	t *testing.T,
	m *LoopDetectionMiddleware,
	sessionID string,
	call toolcontract.Call,
	readOnly bool,
	succeeded bool,
) error {
	t.Helper()
	ctx := &Context{
		SessionID: sessionID,
		ToolCall:  call,
		Metadata:  map[string]any{"tool_read_only": readOnly},
	}
	if err := m.BeforeTool(ctx); err != nil {
		return err
	}
	ctx.ToolResult = &toolcontract.Result{
		ToolCallID:         call.ID,
		Name:               call.Name,
		Content:            "tool result",
		ExecutionSucceeded: &succeeded,
	}
	if err := m.AfterTool(ctx); err != nil {
		t.Fatalf("AfterTool(%s): %v", call.ID, err)
	}
	return nil
}

func TestLoopDetectionSuccessfulMutationStartsNewSignatureEpoch(t *testing.T) {
	cfg := DefaultLoopDetectionConfig()
	cfg.WarnThreshold = 2
	cfg.HardLimit = 3
	cfg.ToolFreqWarn = 20
	cfg.ToolFreqHard = 30
	m := NewLoopDetectionMiddleware(cfg)

	verify := func(id string) toolcontract.Call {
		return toolcontract.Call{ID: id, Name: "ptc_execute", Arguments: map[string]interface{}{
			"script": "verify runs/A23/workflow.json",
		}}
	}
	for i := 0; i < 2; i++ {
		if err := runLoopDetectionToolResult(t, m, "epoch-session", verify(fmt.Sprintf("verify-before-%d", i)), false, false); err != nil {
			t.Fatalf("verification failure %d blocked before hard limit: %v", i+1, err)
		}
	}

	mutation := toolcontract.Call{ID: "patch", Name: "json_patch", Arguments: map[string]interface{}{
		"path": "runs/A23/workflow.json",
		"operations": []interface{}{
			map[string]interface{}{"op": "replace", "path": "/slides/0/title", "value": "Updated"},
		},
	}}
	if err := runLoopDetectionToolResult(t, m, "epoch-session", mutation, false, true); err != nil {
		t.Fatalf("successful mutation was blocked: %v", err)
	}

	if err := runLoopDetectionToolResult(t, m, "epoch-session", verify("verify-after"), false, false); err != nil {
		t.Fatalf("old-state verification signatures leaked into the new epoch: %v", err)
	}
}

func TestLoopDetectionFailedMutationDoesNotAdvanceSignatureEpoch(t *testing.T) {
	cfg := DefaultLoopDetectionConfig()
	cfg.WarnThreshold = 2
	cfg.HardLimit = 3
	cfg.ToolFreqWarn = 20
	cfg.ToolFreqHard = 30
	m := NewLoopDetectionMiddleware(cfg)

	call := func(id string) toolcontract.Call {
		return toolcontract.Call{ID: id, Name: "write_file", Arguments: map[string]interface{}{
			"path": "runs/report.md", "content": "same payload",
		}}
	}
	for i := 0; i < 2; i++ {
		if err := runLoopDetectionToolResult(t, m, "failed-mutation", call(fmt.Sprintf("write-%d", i)), false, false); err != nil {
			t.Fatalf("failed mutation %d blocked before hard limit: %v", i+1, err)
		}
	}
	if err := runLoopDetectionToolResult(t, m, "failed-mutation", call("write-3"), false, false); err == nil {
		t.Fatal("a failed mutation advanced the epoch and hid the third identical retry")
	}
}

func TestLoopDetectionReadOnlySuccessDoesNotAdvanceSignatureEpoch(t *testing.T) {
	cfg := DefaultLoopDetectionConfig()
	cfg.WarnThreshold = 2
	cfg.HardLimit = 3
	cfg.ToolFreqWarn = 20
	cfg.ToolFreqHard = 30
	m := NewLoopDetectionMiddleware(cfg)

	call := func(id string) toolcontract.Call {
		return toolcontract.Call{ID: id, Name: "read_file", Arguments: map[string]interface{}{
			"path": "runs/report.md", "lines_range": "1-50",
		}}
	}
	for i := 0; i < 2; i++ {
		if err := runLoopDetectionToolResult(t, m, "readonly-session", call(fmt.Sprintf("read-%d", i)), true, true); err != nil {
			t.Fatalf("read %d blocked before hard limit: %v", i+1, err)
		}
	}
	if err := runLoopDetectionToolResult(t, m, "readonly-session", call("read-3"), true, true); err == nil {
		t.Fatal("a successful read-only call advanced the epoch and hid a real read loop")
	}
}

func TestLoopDetectionToolFrequencyLimitSurvivesEpochChanges(t *testing.T) {
	cfg := DefaultLoopDetectionConfig()
	cfg.WarnThreshold = 10
	cfg.HardLimit = 11
	cfg.ToolFreqWarn = 2
	cfg.ToolFreqHard = 3
	m := NewLoopDetectionMiddleware(cfg)

	call := func(id string) toolcontract.Call {
		return toolcontract.Call{ID: id, Name: "write_file", Arguments: map[string]interface{}{
			"path": "runs/report.md", "content": "same payload",
		}}
	}
	for i := 0; i < 2; i++ {
		if err := runLoopDetectionToolResult(t, m, "frequency-session", call(fmt.Sprintf("write-%d", i)), false, true); err != nil {
			t.Fatalf("successful write %d blocked before frequency limit: %v", i+1, err)
		}
	}
	if err := runLoopDetectionToolResult(t, m, "frequency-session", call("write-3"), false, true); err == nil {
		t.Fatal("epoch changes reset the session-wide per-tool frequency limit")
	}
}

func TestLoopDetectionDoesNotChargeHostManagedCallToModelFrequency(t *testing.T) {
	cfg := DefaultLoopDetectionConfig()
	cfg.WarnThreshold = 10
	cfg.HardLimit = 11
	cfg.ToolFreqWarn = 2
	cfg.ToolFreqHard = 3
	m := NewLoopDetectionMiddleware(cfg)

	modelCall := func(id string, sequence int) toolcontract.Call {
		return toolcontract.Call{ID: id, Name: "host_test_action", Arguments: map[string]interface{}{
			"sequence": sequence,
		}}
	}
	for i := 0; i < 2; i++ {
		if err := runLoopDetectionToolResult(t, m, "host-managed-frequency", modelCall(fmt.Sprintf("model-%d", i), i), false, true); err != nil {
			t.Fatalf("model call %d blocked before frequency limit: %v", i+1, err)
		}
	}

	hostCall := modelCall("host-convergence", 99)
	hostCall.HostManaged = true
	if err := runLoopDetectionToolResult(t, m, "host-managed-frequency", hostCall, false, true); err != nil {
		t.Fatalf("bounded host-managed convergence call was charged as a model loop: %v", err)
	}

	if err := runLoopDetectionToolResult(t, m, "host-managed-frequency", modelCall("model-3", 3), false, true); err == nil {
		t.Fatal("host-managed exemption weakened the ordinary model frequency limit")
	}
}

func TestToolCallHostManagedCannotBeSetByProviderJSON(t *testing.T) {
	var call toolcontract.Call
	if err := json.Unmarshal([]byte(`{"id":"provider","name":"cli","arguments":{},"HostManaged":true,"host_managed":true}`), &call); err != nil {
		t.Fatal(err)
	}
	if call.HostManaged {
		t.Fatal("provider JSON forged host-managed execution metadata")
	}
}
