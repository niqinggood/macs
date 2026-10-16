package app

import (
	"context"
	"errors"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLLMAgentAppendToolResultMarksFailedContent(t *testing.T) {
	agent := &LLMAgent{}
	messages := []map[string]interface{}{}
	coldMessages := []map[string]interface{}{}
	summary := []string{}
	callChain := []map[string]interface{}{}
	allResults := []string{}
	execCount := 0
	var totalDuration int64
	failTracker := map[string]int{}
	brokenTools := map[string]bool{}
	failDiagnosticInjected := map[string]bool{}

	agent.appendToolResult(AgentToolRunResult{
		ToolCall: ToolCall{ID: "1", Name: "mock", Arguments: map[string]interface{}{}},
		Result:   ToolResult{Content: "执行失败: boom"},
	},
		&messages, &coldMessages, "task-1", &summary, &callChain,
		&totalDuration, &allResults, &execCount, failTracker, brokenTools, failDiagnosticInjected)

	if execCount != 1 {
		t.Fatalf("expected exec count 1, got %d", execCount)
	}
	if len(messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(messages))
	}
	if status, _ := messages[0]["status"].(bool); status {
		t.Fatalf("failed content should produce status=false, got message %#v", messages[0])
	}
	if len(failTracker) != 1 {
		t.Fatalf("expected failure tracker to record failed content, got %#v", failTracker)
	}
}

func TestLLMAgentAppendToolResultPreservesFailureContentWithError(t *testing.T) {
	agent := &LLMAgent{}
	messages := []map[string]interface{}{}
	coldMessages := []map[string]interface{}{}
	summary := []string{}
	callChain := []map[string]interface{}{}
	allResults := []string{}
	execCount := 0
	var totalDuration int64
	failTracker := map[string]int{}
	brokenTools := map[string]bool{}
	failDiagnosticInjected := map[string]bool{}

	agent.appendToolResult(AgentToolRunResult{
		ToolCall: ToolCall{ID: "1", Name: "cli", Arguments: map[string]interface{}{}},
		Result:   ToolResult{Content: "[CONFIRMATION REQUIRED] Tool 'cli' is HIGH risk."},
		Err:      errors.New("confirmation required for cli"),
	},
		&messages, &coldMessages, "task-1", &summary, &callChain,
		&totalDuration, &allResults, &execCount, failTracker, brokenTools, failDiagnosticInjected)

	content, _ := messages[0]["content"].(string)
	if !strings.Contains(content, "[CONFIRMATION REQUIRED]") {
		t.Fatalf("expected original failure content to be preserved, got %q", content)
	}
	if strings.Contains(content, "tool error:") {
		t.Fatalf("non-empty failure content should not be replaced with generic tool error, got %q", content)
	}
}

func TestLLMAgentSavePreMutationSnapshotUsesPathAliases(t *testing.T) {
	dir := t.TempDir()
	editPath := filepath.Join(dir, "edit.txt")
	writePath := filepath.Join(dir, "write.txt")
	if err := os.WriteFile(editPath, []byte("before edit"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(writePath, []byte("before write"), 0644); err != nil {
		t.Fatal(err)
	}

	agent := &LLMAgent{
		workspace:       dir,
		snapshotManager: NewSnapshotManager(dir, "test-session"),
	}

	agent.savePreMutationSnapshot(ToolCall{
		Name:      "edit_file",
		Arguments: map[string]interface{}{"file_path": "edit.txt"},
	})
	agent.savePreMutationSnapshot(ToolCall{
		Name:      "write_file",
		Arguments: map[string]interface{}{"file": "write.txt"},
	})

	snapshots := agent.snapshotManager.ListSnapshots()
	if len(snapshots) != 2 {
		t.Fatalf("expected snapshots for edit_file file_path and write_file file alias, got %#v", snapshots)
	}
	if snapshots[0].OriginalPath != editPath || snapshots[1].OriginalPath != writePath {
		t.Fatalf("unexpected snapshot paths: %#v", snapshots)
	}
}

func TestLLMAgentExecuteBatchSerialAppliesSafetyGuard(t *testing.T) {
	reg := NewToolRegistry()
	execCount := 0
	reg.Register(&partitionMockTool{name: "write_file", readOnly: false, execCount: &execCount})

	agent := &LLMAgent{
		toolRegistry: reg,
		safetyGuard:  &SafetyGuard{PermissionMode: PermissionPlan},
		fileLogger:   log.New(io.Discard, "", 0),
	}
	messages := []map[string]interface{}{}
	coldMessages := []map[string]interface{}{}
	summary := []string{}
	callChain := []map[string]interface{}{}
	allResults := []string{}
	toolExecCount := 0
	var totalDuration int64
	failTracker := map[string]int{}
	brokenTools := map[string]bool{}
	failDiagnosticInjected := map[string]bool{}

	agent.executeBatchSerial(context.Background(), []ToolCall{
		{ID: "1", Name: "write_file", Arguments: map[string]interface{}{"path": "out.txt"}},
	}, &messages, &coldMessages, "task-1", &summary, &callChain, &totalDuration,
		&allResults, &toolExecCount, failTracker, brokenTools, failDiagnosticInjected)

	if execCount != 0 {
		t.Fatalf("blocked write_file should not execute, got execCount=%d", execCount)
	}
	if len(messages) != 1 {
		t.Fatalf("expected one tool result message, got %#v", messages)
	}
	if status, _ := messages[0]["status"].(bool); status {
		t.Fatalf("blocked tool should be recorded as failed, got %#v", messages[0])
	}
	content, _ := messages[0]["content"].(string)
	if !strings.Contains(content, "plan mode") {
		t.Fatalf("expected plan mode block reason, got %q", content)
	}
}

func TestLLMAgentExecuteBatchSerialSnapshotsBeforeMutation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.txt")
	if err := os.WriteFile(path, []byte("before"), 0644); err != nil {
		t.Fatal(err)
	}

	reg := NewToolRegistry()
	reg.Register(NewWriteFileTool(dir))
	agent := &LLMAgent{
		workspace:       dir,
		toolRegistry:    reg,
		snapshotManager: NewSnapshotManager(dir, "test-session"),
		fileLogger:      log.New(io.Discard, "", 0),
	}
	messages := []map[string]interface{}{}
	coldMessages := []map[string]interface{}{}
	summary := []string{}
	callChain := []map[string]interface{}{}
	allResults := []string{}
	toolExecCount := 0
	var totalDuration int64
	failTracker := map[string]int{}
	brokenTools := map[string]bool{}
	failDiagnosticInjected := map[string]bool{}

	agent.executeBatchSerial(context.Background(), []ToolCall{
		{ID: "1", Name: "write_file", Arguments: map[string]interface{}{"path": "out.txt", "content": "after"}},
	}, &messages, &coldMessages, "task-1", &summary, &callChain, &totalDuration,
		&allResults, &toolExecCount, failTracker, brokenTools, failDiagnosticInjected)

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "after" {
		t.Fatalf("expected write_file to mutate file, got %q", data)
	}

	snapshots := agent.snapshotManager.ListSnapshots()
	if len(snapshots) != 1 {
		t.Fatalf("expected one pre-mutation snapshot, got %#v", snapshots)
	}
	snapshotData, err := os.ReadFile(snapshots[0].SnapshotPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(snapshotData) != "before" {
		t.Fatalf("snapshot should preserve old content, got %q", snapshotData)
	}
}
