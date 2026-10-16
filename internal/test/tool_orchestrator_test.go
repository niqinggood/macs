package app

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"mac/internal/toolcontract"
)

type orchestratorMockTool struct {
	name     string
	readOnly bool
	err      error
}

type blockingOrchestratorTool struct {
	name    string
	release <-chan struct{}
}

func (m *blockingOrchestratorTool) Name() string        { return m.name }
func (m *blockingOrchestratorTool) Description() string { return "blocking orchestrator mock tool" }
func (m *blockingOrchestratorTool) Parameters() map[string]interface{} {
	return map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}
}
func (m *blockingOrchestratorTool) IsReadOnly() bool { return true }
func (m *blockingOrchestratorTool) Execute(context.Context, map[string]interface{}) (string, error) {
	<-m.release
	return "released", nil
}

type panicOrchestratorTool struct{ name string }

func (m *panicOrchestratorTool) Name() string        { return m.name }
func (m *panicOrchestratorTool) Description() string { return "panic orchestrator mock tool" }
func (m *panicOrchestratorTool) Parameters() map[string]interface{} {
	return map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}
}
func (m *panicOrchestratorTool) IsReadOnly() bool { return true }
func (m *panicOrchestratorTool) Execute(context.Context, map[string]interface{}) (string, error) {
	panic("boom")
}

func (m *orchestratorMockTool) Name() string { return m.name }
func (m *orchestratorMockTool) Description() string {
	return "orchestrator mock tool"
}
func (m *orchestratorMockTool) Parameters() map[string]interface{} {
	return map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}
}
func (m *orchestratorMockTool) IsReadOnly() bool { return m.readOnly }
func (m *orchestratorMockTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	if m.err != nil {
		return "", m.err
	}
	return "ok", nil
}

func TestPartitionToolCallsUsesRegisteredReadOnlyMetadata(t *testing.T) {
	reg := NewToolRegistry()
	reg.Register(&orchestratorMockTool{name: "custom_reader", readOnly: true})
	reg.Register(&orchestratorMockTool{name: "custom_writer", readOnly: false})

	batches := toolcontract.PartitionToolCalls(reg, []ToolCall{
		{ID: "1", Name: "custom_reader", Arguments: map[string]interface{}{}},
		{ID: "2", Name: "custom_writer", Arguments: map[string]interface{}{}},
	}, nil)

	if len(batches) != 2 {
		t.Fatalf("expected 2 batches, got %d", len(batches))
	}
	if !batches[0].IsReadOnly || len(batches[0].Blocks) != 1 {
		t.Fatalf("registered read-only tool should form read batch, got %#v", batches[0])
	}
	if batches[1].IsReadOnly {
		t.Fatalf("registered writer should form serial batch, got %#v", batches[1])
	}
}

func TestRunToolBatchReportsConcurrentContentFailure(t *testing.T) {
	reg := NewToolRegistry()
	reg.Register(&orchestratorMockTool{name: "custom_reader", readOnly: true, err: errors.New("boom")})

	results, err := toolcontract.RunToolBatch(context.Background(), reg, toolcontract.ToolBatch{
		IsReadOnly: true,
		Blocks: []ToolCall{
			{ID: "1", Name: "custom_reader", Arguments: map[string]interface{}{}},
		},
	})

	if err == nil {
		t.Fatal("expected concurrent batch to report content failure")
	}
	if strings.Contains(err.Error(), "%!w") {
		t.Fatalf("failure error should not wrap nil error: %v", err)
	}
	if len(results) != 1 || !strings.Contains(results[0].Content, "执行失败") {
		t.Fatalf("expected failed tool result to be preserved, got %#v", results)
	}
}

func TestRunToolBatchReportsSerialContentFailure(t *testing.T) {
	reg := NewToolRegistry()
	reg.Register(&orchestratorMockTool{name: "custom_writer", readOnly: false, err: errors.New("boom")})

	results, err := toolcontract.RunToolBatch(context.Background(), reg, toolcontract.ToolBatch{
		IsReadOnly: false,
		Blocks: []ToolCall{
			{ID: "1", Name: "custom_writer", Arguments: map[string]interface{}{}},
		},
	})

	if err == nil {
		t.Fatal("expected serial batch to report content failure")
	}
	if len(results) != 1 || !strings.Contains(results[0].Content, "执行失败") {
		t.Fatalf("expected failed serial result to be preserved, got %#v", results)
	}
}

func TestRunToolBatchUsesMiddlewareChain(t *testing.T) {
	reg := NewToolRegistry()
	reg.Register(&orchestratorMockTool{name: "custom_reader", readOnly: true})

	mw := newTestMiddleware("orchestrator_test")
	mw.after = func(ctx *AgentContext) error {
		ctx.ToolResult.Content = "middleware:" + ctx.ToolResult.Content
		return nil
	}
	reg.SetChain(NewMiddlewareChain(mw))

	results, err := toolcontract.RunToolBatch(context.Background(), reg, toolcontract.ToolBatch{
		Concurrent: true,
		Blocks: []ToolCall{
			{ID: "1", Name: "custom_reader", Arguments: map[string]interface{}{}},
		},
	})

	if err != nil {
		t.Fatalf("RunToolBatch returned error: %v", err)
	}
	if len(results) != 1 || results[0].Content != "middleware:ok" {
		t.Fatalf("expected middleware-mutated result, got %#v", results)
	}
}

func TestRunToolBatchNilRegistryReturnsError(t *testing.T) {
	_, err := toolcontract.RunToolBatch(context.Background(), nil, toolcontract.ToolBatch{
		Blocks: []ToolCall{{ID: "1", Name: "read_file", Arguments: map[string]interface{}{}}},
	})
	if err == nil || !strings.Contains(err.Error(), "tool registry is nil") {
		t.Fatalf("expected nil registry error, got %v", err)
	}
}

func TestRunToolBatchEmptyDoesNotRequireRegistry(t *testing.T) {
	results, err := toolcontract.RunToolBatch(context.Background(), nil, toolcontract.ToolBatch{})
	if err != nil {
		t.Fatalf("empty batch should not require registry: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("expected empty results, got %#v", results)
	}
}

func TestRunToolBatchCancellationDoesNotWaitForStuckConcurrentTool(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	reg := NewToolRegistry()
	reg.Register(&blockingOrchestratorTool{name: "blocking_reader", release: release})
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, err := toolcontract.RunToolBatch(ctx, reg, toolcontract.ToolBatch{Concurrent: true, Blocks: []ToolCall{{ID: "1", Name: "blocking_reader", Arguments: map[string]interface{}{}}}})
	if err == nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected deadline error, got %v", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("cancelled batch remained blocked for %v", elapsed)
	}
}

func TestRunToolBatchConvertsToolPanicToFailure(t *testing.T) {
	reg := NewToolRegistry()
	reg.Register(&panicOrchestratorTool{name: "panic_reader"})
	results, err := toolcontract.RunToolBatch(context.Background(), reg, toolcontract.ToolBatch{Concurrent: true, Blocks: []ToolCall{{ID: "panic-1", Name: "panic_reader", Arguments: map[string]interface{}{}}}})
	if err == nil || !strings.Contains(err.Error(), "panicked") {
		t.Fatalf("expected panic failure, got %v", err)
	}
	if len(results) != 1 || !strings.Contains(results[0].Content, "tool panic") {
		t.Fatalf("panic result was not preserved: %#v", results)
	}
}

func TestExecuteAllToolsSingleContentFailureReturnsResultAndError(t *testing.T) {
	reg := NewToolRegistry()
	reg.Register(&orchestratorMockTool{name: "custom_reader", readOnly: true, err: errors.New("boom")})

	results, err := toolcontract.ExecuteAllTools(context.Background(), reg, []ToolCall{
		{ID: "1", Name: "custom_reader", Arguments: map[string]interface{}{}},
	}, nil)

	if err == nil {
		t.Fatal("expected ExecuteAllTools to return the content failure")
	}
	if len(results) != 1 || !ToolResultLooksFailed(results[0].Content) {
		t.Fatalf("expected failed result to be preserved, got %#v", results)
	}
}

func TestExecuteAllToolsReturnsFirstErrorAndCollectedResults(t *testing.T) {
	reg := NewToolRegistry()
	reg.Register(&orchestratorMockTool{name: "custom_writer", readOnly: false, err: errors.New("boom")})
	reg.Register(&orchestratorMockTool{name: "custom_reader", readOnly: true})

	results, err := toolcontract.ExecuteAllTools(context.Background(), reg, []ToolCall{
		{ID: "1", Name: "custom_writer", Arguments: map[string]interface{}{}},
		{ID: "2", Name: "custom_reader", Arguments: map[string]interface{}{}},
	}, nil)

	if err == nil {
		t.Fatal("expected first batch error")
	}
	if len(results) != 2 {
		t.Fatalf("expected failed and later successful results to be collected, got %#v", results)
	}
	if !ToolResultLooksFailed(results[0].Content) || results[1].Content != "ok" {
		t.Fatalf("unexpected results: %#v", results)
	}
}
