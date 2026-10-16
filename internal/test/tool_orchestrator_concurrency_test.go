package app

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"mac/internal/toolcontract"
)

type countingReadTool struct {
	name  string
	err   error
	count *int32
}

func (m *countingReadTool) Name() string { return m.name }
func (m *countingReadTool) Description() string {
	return "counting read tool"
}
func (m *countingReadTool) Parameters() map[string]interface{} {
	return map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}
}
func (m *countingReadTool) IsReadOnly() bool { return true }
func (m *countingReadTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	if m.count != nil {
		atomic.AddInt32(m.count, 1)
	}
	if m.err != nil {
		return "", m.err
	}
	return m.name + " ok", nil
}

func TestRunToolBatchConcurrentDoesNotSkipAfterFailure(t *testing.T) {
	reg := NewToolRegistry()
	var okCount int32
	reg.Register(&countingReadTool{name: "fail_fast", err: errors.New("boom")})
	reg.Register(&countingReadTool{name: "ok_reader", count: &okCount})

	calls := make([]ToolCall, 0, toolcontract.MaxConcurrentTools+2)
	calls = append(calls, ToolCall{ID: "fail", Name: "fail_fast", Arguments: map[string]interface{}{}})
	for i := 0; i < toolcontract.MaxConcurrentTools+1; i++ {
		calls = append(calls, ToolCall{ID: "ok", Name: "ok_reader", Arguments: map[string]interface{}{}})
	}

	results, err := toolcontract.RunToolBatch(context.Background(), reg, toolcontract.ToolBatch{Concurrent: true, Blocks: calls})
	if err == nil {
		t.Fatal("expected failed concurrent batch to report an error")
	}
	if len(results) != len(calls) {
		t.Fatalf("expected %d results, got %d", len(calls), len(results))
	}
	if got := atomic.LoadInt32(&okCount); got != int32(toolcontract.MaxConcurrentTools+1) {
		t.Fatalf("all non-failing calls should execute, got %d", got)
	}
	for i, result := range results {
		if result.Name == "" {
			t.Fatalf("result %d was skipped or left empty: %#v", i, result)
		}
	}
}
