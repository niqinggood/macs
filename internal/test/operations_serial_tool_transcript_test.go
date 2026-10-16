package app

import (
	"context"
	"testing"
)

type serialTranscriptTestTool struct{}

func (serialTranscriptTestTool) Name() string                       { return "serial_transcript_test" }
func (serialTranscriptTestTool) Description() string                { return "records a deterministic result" }
func (serialTranscriptTestTool) Parameters() map[string]interface{} { return nil }
func (serialTranscriptTestTool) Execute(context.Context, map[string]interface{}) (string, error) {
	return "serial result", nil
}

type serialTranscriptCountingTool struct {
	calls int
}

func (t *serialTranscriptCountingTool) Name() string { return "serial_transcript_counting" }
func (t *serialTranscriptCountingTool) Description() string {
	return "counts serial executions"
}
func (t *serialTranscriptCountingTool) Parameters() map[string]interface{} { return nil }
func (t *serialTranscriptCountingTool) Execute(context.Context, map[string]interface{}) (string, error) {
	t.calls++
	return "unexpected execution", nil
}

func TestAgentTurnSerialToolTranscriptRecordsExecutedCallAndResultOnce(t *testing.T) {
	transcript := NewSessionTranscript("serial-executed", "tester", t.TempDir())
	registry := NewToolRegistry()
	registry.Register(serialTranscriptTestTool{})
	agent := &LLMAgent{
		name:         "tester",
		toolRegistry: registry,
		transcript:   transcript,
	}
	toolCall := ToolCall{
		ID:        "call-1",
		Name:      "serial_transcript_test",
		Arguments: map[string]interface{}{"value": "kept"},
	}

	opts := agentTurnSerialToolRunOptions()
	if !opts.RecordTranscript || opts.SkipHooks || opts.SkipSafety {
		t.Fatalf("unexpected serial run options: %#v", opts)
	}
	run := NewAgentToolExecutor(agent).RunSingleTool(context.Background(), toolCall, opts)
	if run.Err != nil {
		t.Fatalf("RunSingleTool() error = %v", run.Err)
	}
	recordAgentTurnSerialToolTranscriptResult(
		transcript,
		toolCall,
		run.TranscriptCallRecorded,
		run.Result.Content,
		true,
		run.DurationMs,
	)

	if len(transcript.Entries) != 2 {
		t.Fatalf("transcript entries = %#v, want exactly one call/result pair", transcript.Entries)
	}
	callEntry, resultEntry := transcript.Entries[0], transcript.Entries[1]
	if callEntry.Type != "tool_call" || callEntry.ToolName != toolCall.Name {
		t.Fatalf("first transcript entry = %#v, want tool_call", callEntry)
	}
	if got := callEntry.ToolParams["value"]; got != "kept" {
		t.Fatalf("recorded params value = %#v, want kept", got)
	}
	if resultEntry.Type != "tool_result" || resultEntry.ToolName != toolCall.Name || resultEntry.ToolResult != "serial result" {
		t.Fatalf("second transcript entry = %#v, want successful tool_result", resultEntry)
	}
	if resultEntry.ToolSuccess == nil || !*resultEntry.ToolSuccess {
		t.Fatalf("tool result success = %#v, want true", resultEntry.ToolSuccess)
	}
}

func TestAgentTurnSerialToolAskRuleRequiresApprovalWithoutExecuting(t *testing.T) {
	transcript := NewSessionTranscript("serial-ask", "tester", t.TempDir())
	registry := NewToolRegistry()
	tool := &serialTranscriptCountingTool{}
	registry.Register(tool)
	permissionEngine := NewPermissionEngine(PermissionDefault)
	permissionEngine.AddRule(PermissionRule{
		Source:   SourceProject,
		Behavior: BehaviorAsk,
		ToolName: tool.Name(),
	})
	agent := &LLMAgent{
		name:             "tester",
		toolRegistry:     registry,
		transcript:       transcript,
		permissionEngine: permissionEngine,
	}
	toolCall := ToolCall{ID: "call-ask", Name: tool.Name(), Arguments: map[string]interface{}{}}

	run := NewAgentToolExecutor(agent).RunSingleTool(context.Background(), toolCall, agentTurnSerialToolRunOptions())
	if run.Err == nil || !run.TranscriptCallRecorded || run.Result.ErrorCode != "approval_required" {
		t.Fatalf("ask rule run = %#v, want approval-required result with recorded call", run)
	}
	if tool.calls != 0 {
		t.Fatalf("ask rule executed tool %d time(s), want 0", tool.calls)
	}
	recordAgentTurnSerialToolTranscriptResult(
		transcript,
		toolCall,
		run.TranscriptCallRecorded,
		run.Result.Content,
		false,
		run.DurationMs,
	)
	if len(transcript.Entries) != 2 || transcript.Entries[0].Type != "tool_call" || transcript.Entries[1].Type != "tool_result" {
		t.Fatalf("blocked serial transcript must be one call/result pair: %#v", transcript.Entries)
	}
	if transcript.Entries[1].ToolSuccess == nil || *transcript.Entries[1].ToolSuccess {
		t.Fatalf("approval-required serial result must be unsuccessful: %#v", transcript.Entries[1])
	}
}

func TestAgentTurnSerialToolTranscriptRecordsPreExecutionRejection(t *testing.T) {
	transcript := NewSessionTranscript("serial-rejected", "tester", t.TempDir())
	toolCall := ToolCall{
		ID:        "call-2",
		Name:      "serial_transcript_test",
		Arguments: map[string]interface{}{"value": "blocked"},
	}

	recordAgentTurnSerialToolTranscriptResult(
		transcript,
		toolCall,
		false,
		"blocked before registry execution",
		false,
		7,
	)

	if len(transcript.Entries) != 2 {
		t.Fatalf("transcript entries = %#v, want rejected call/result pair", transcript.Entries)
	}
	if transcript.Entries[0].Type != "tool_call" {
		t.Fatalf("first transcript entry = %#v, want tool_call", transcript.Entries[0])
	}
	resultEntry := transcript.Entries[1]
	if resultEntry.Type != "tool_result" || resultEntry.ToolSuccess == nil || *resultEntry.ToolSuccess {
		t.Fatalf("rejected result entry = %#v, want success=false", resultEntry)
	}
	if resultEntry.DurationMs != 7 {
		t.Fatalf("rejected result duration = %d, want 7", resultEntry.DurationMs)
	}
}
