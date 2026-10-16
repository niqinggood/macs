package app

import (
	"context"
	"testing"
	"time"
)

type streamReadTool struct {
	name string
}

func (t *streamReadTool) Name() string { return t.name }
func (t *streamReadTool) Description() string {
	return "stream read test tool"
}
func (t *streamReadTool) Parameters() map[string]interface{} { return nil }
func (t *streamReadTool) IsReadOnly() bool                   { return true }
func (t *streamReadTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	return "unused", nil
}

func TestStreamExecutorResultsPreserveCallOrder(t *testing.T) {
	reg := NewToolRegistry()
	reg.Register(&streamReadTool{name: "stream_read"})

	executor := func(ctx context.Context, name string, args map[string]interface{}) (string, error) {
		if delay, ok := args["delay_ms"].(float64); ok {
			time.Sleep(time.Duration(delay) * time.Millisecond)
		}
		if content, ok := args["content"].(string); ok {
			return content, nil
		}
		return "", nil
	}

	se := NewStreamExecutor(reg, executor)
	se.SubmitCall(PendingCall{ID: "first", Name: "stream_read", Arguments: `{"delay_ms":50,"content":"first"}`})
	se.SubmitCall(PendingCall{ID: "second", Name: "stream_read", Arguments: `{"delay_ms":1,"content":"second"}`})

	se.mu.Lock()
	se.finished = true
	se.mu.Unlock()

	se.WaitForAll(time.Second)

	results := se.Results()
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if results[0].CallID != "first" || results[0].Content != "first" {
		t.Fatalf("first result should preserve call order, got %#v", results[0])
	}
	if results[1].CallID != "second" || results[1].Content != "second" {
		t.Fatalf("second result should preserve call order, got %#v", results[1])
	}
}
