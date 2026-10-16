package app

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestPipelineToolCallAuditNormalizesCorrelationAttemptAndStatus(t *testing.T) {
	exec := &PipelineExecution{
		ID:    "pipe_audit",
		Steps: []StepResult{{Name: "research", Revision: 1}},
	}

	exec.recordToolCall(0, ToolCallRecord{ToolName: "web_fetch", Params: `{"url":"https://example.com"}`, Result: "ok"})
	exec.recordToolCall(0, ToolCallRecord{ToolName: "web_search", Result: "status: failed\nupstream unavailable"})

	if len(exec.Steps[0].ToolCalls) != 2 {
		t.Fatalf("tool calls = %d, want 2", len(exec.Steps[0].ToolCalls))
	}
	first, second := exec.Steps[0].ToolCalls[0], exec.Steps[0].ToolCalls[1]
	if first.ExecutionID != exec.ID || first.StepIndex != 0 || first.Attempt != 1 {
		t.Fatalf("unexpected first correlation fields: %+v", first)
	}
	if first.CallID != "pipe_audit:s0:a1:c1" || second.CallID != "pipe_audit:s0:a1:c2" {
		t.Fatalf("unexpected call IDs: %q, %q", first.CallID, second.CallID)
	}
	if first.Status != "success" || first.Error != "" {
		t.Fatalf("successful call was not normalized: %+v", first)
	}
	if second.Status != "error" || !strings.Contains(second.Error, "status: failed") {
		t.Fatalf("failure result was not normalized: %+v", second)
	}

	exec.Steps[0].Revision = 2
	exec.Steps[0].ToolCalls = nil
	exec.recordToolCall(0, ToolCallRecord{ToolName: "web_fetch", Result: "reworked"})
	if got := exec.Steps[0].ToolCalls[0]; got.Attempt != 2 || got.CallID != "pipe_audit:s0:a2:c1" {
		t.Fatalf("rework correlation = %+v, want attempt 2", got)
	}
}

type pipelineAuditFailureTool struct{}

func (*pipelineAuditFailureTool) Name() string                       { return "failing_tool" }
func (*pipelineAuditFailureTool) Description() string                { return "fails for audit testing" }
func (*pipelineAuditFailureTool) Parameters() map[string]interface{} { return nil }
func (*pipelineAuditFailureTool) Execute(context.Context, map[string]interface{}) (string, error) {
	return "partial output", errors.New("upstream timeout")
}

func TestPipelineAuditToolPersistsExecutionError(t *testing.T) {
	exec := &PipelineExecution{ID: "pipe_error", Steps: []StepResult{{Name: "step", Revision: 1}}}
	wrapped := &auditTool{inner: &pipelineAuditFailureTool{}, exec: exec, stepIdx: 0}

	if _, err := wrapped.Execute(context.Background(), nil); err == nil {
		t.Fatal("expected tool error")
	}
	if len(exec.Steps[0].ToolCalls) != 1 {
		t.Fatalf("tool calls = %d, want 1", len(exec.Steps[0].ToolCalls))
	}
	record := exec.Steps[0].ToolCalls[0]
	if record.Status != "error" || record.Error != "upstream timeout" {
		t.Fatalf("tool error was not persisted: %+v", record)
	}
}

func TestPipelineToolCallProgressOutcomeReflectsRealStatusAndParams(t *testing.T) {
	status, message := pipelineToolCallProgressOutcome(ToolCallRecord{
		ToolName: "web_fetch",
		Params:   `{"url":"https://example.com/report"}`,
		Status:   "success",
	})
	if status != "success" || !strings.Contains(message, "https://example.com/report") {
		t.Fatalf("success progress = (%q, %q)", status, message)
	}

	status, message = pipelineToolCallProgressOutcome(ToolCallRecord{
		ToolName: "web_fetch",
		Status:   "error",
		Error:    "context deadline exceeded",
	})
	if status != "error" || !strings.Contains(message, "context deadline exceeded") || strings.Contains(message, "completed") {
		t.Fatalf("error progress = (%q, %q)", status, message)
	}
}

func TestPipelineProgressStepKeySeparatesReworkAttempts(t *testing.T) {
	first := pipelineProgressStepKey(0, StepResult{Name: "research", Revision: 1})
	second := pipelineProgressStepKey(0, StepResult{Name: "research", Revision: 2})
	if first == second {
		t.Fatalf("rework revisions share progress key %q", first)
	}
	if first != "0:research:r1" || second != "0:research:r2" {
		t.Fatalf("unexpected progress keys: %q, %q", first, second)
	}
}

func TestProgressEventCarriesToolCallCorrelationID(t *testing.T) {
	payload, err := json.Marshal(ProgressEvent{
		Type:        "tool_end",
		ExecutionID: "pipe",
		StepName:    "research",
		Attempt:     1,
		ToolCallID:  "pipe:s0:a1:c1",
		Status:      "error",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"execution_id":"pipe"`, `"step_name":"research"`, `"attempt":1`, `"tool_call_id":"pipe:s0:a1:c1"`} {
		if !strings.Contains(string(payload), want) {
			t.Fatalf("progress event lost %s: %s", want, payload)
		}
	}
}

func TestPipelineProgressSnapshotDeepCopiesToolCalls(t *testing.T) {
	exec := &PipelineExecution{
		ID:     "pipe_snapshot",
		Status: "running",
		Steps: []StepResult{{
			Name:      "step",
			Status:    "running",
			ToolCalls: []ToolCallRecord{{ToolName: "read_file", Result: "before"}},
		}},
	}

	status, steps := exec.progressSnapshot()
	if status != "running" || len(steps) != 1 || len(steps[0].ToolCalls) != 1 {
		t.Fatalf("unexpected progress snapshot: status=%q steps=%+v", status, steps)
	}
	steps[0].ToolCalls[0].Result = "mutated snapshot"
	if exec.Steps[0].ToolCalls[0].Result != "before" {
		t.Fatal("progress snapshot aliases live tool-call storage")
	}
}

func TestPipelineExecutionLogIncludesToolCorrelationAndFailure(t *testing.T) {
	exec := &PipelineExecution{
		ID:           "pipe_log",
		PipelineName: "generic-pipeline",
		SessionID:    "session_42",
		Status:       "degraded",
		Steps:        []StepResult{{Name: "verify", Status: "degraded", Revision: 1}},
	}
	exec.recordToolCall(0, ToolCallRecord{ToolName: "web_fetch", Error: "context deadline exceeded"})
	firstAttempt := exec.Steps[0].ToolCalls[0]
	exec.Steps[0].Revisions = []PipelineStepRevision{{
		Revision: 1,
		Status:   "completed",
		Output:   "rejected evidence",
		ToolCalls: []ToolCallRecord{
			firstAttempt,
		},
	}}
	exec.Steps[0].Revision = 2
	exec.Steps[0].ToolCalls = nil
	exec.recordToolCall(0, ToolCallRecord{ToolName: "web_fetch", Result: "accepted evidence"})

	logText := formatPipelineExecutionLog(exec)
	for _, want := range []string{
		"Execution ID: pipe_log",
		"Session ID: session_42",
		"call_id=pipe_log:s0:a1:c1",
		"call_id=pipe_log:s0:a2:c1",
		"Archived Attempts:",
		"status=error",
		"attempt=1",
		"error: context deadline exceeded",
	} {
		if !strings.Contains(logText, want) {
			t.Fatalf("execution log missing %q:\n%s", want, logText)
		}
	}
}
