package app

import (
	"context"
	"fmt"
	"reflect"
	"sync"
	"testing"
)

type registryArgumentCaptureTool struct {
	got map[string]interface{}
}

func (t *registryArgumentCaptureTool) Name() string { return "capture_args" }
func (t *registryArgumentCaptureTool) Description() string {
	return "captures effective registry arguments"
}
func (t *registryArgumentCaptureTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"value": map[string]interface{}{"type": "string"},
		},
	}
}
func (t *registryArgumentCaptureTool) Execute(_ context.Context, params map[string]interface{}) (string, error) {
	t.got = make(map[string]interface{}, len(params))
	for key, value := range params {
		t.got[key] = value
	}
	return "ok", nil
}

func TestToolRegistryNamesAreSorted(t *testing.T) {
	reg := NewToolRegistry()
	reg.Register(registryNameMockTool{name: "write_file"})
	reg.Register(registryNameMockTool{name: "read_file"})
	reg.Register(registryNameMockTool{name: "cli"})

	want := []string{"cli", "read_file", "write_file"}
	if got := reg.AllNames(); !reflect.DeepEqual(got, want) {
		t.Fatalf("AllNames() = %v, want %v", got, want)
	}
	if got := reg.ListNames(); !reflect.DeepEqual(got, want) {
		t.Fatalf("ListNames() = %v, want %v", got, want)
	}
}

func TestToolRegistryConcurrentRegisterAndRead(t *testing.T) {
	reg := NewToolRegistry()
	reg.Register(registryNameMockTool{name: "seed"})

	var wg sync.WaitGroup
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			reg.Register(registryNameMockTool{name: fmt.Sprintf("tool_%02d", n)})
			reg.Get("seed")
			reg.ListNames()
			reg.Count()
			reg.GetAllToolSummaries()
			reg.GetDefinitions()
			reg.Unregister(fmt.Sprintf("missing_%02d", n))
		}(i)
	}
	wg.Wait()

	if reg.Count() != 41 {
		t.Fatalf("expected 41 tools after concurrent register, got %d", reg.Count())
	}
}

func TestToolRegistryExecutesMiddlewareModifiedArguments(t *testing.T) {
	reg := NewToolRegistry()
	tool := &registryArgumentCaptureTool{}
	reg.Register(tool)

	var seenTaskID, seenSessionID string
	mw := newTestMiddleware("rewrite_args")
	mw.before = func(ctx *AgentContext) error {
		seenTaskID = ctx.TaskID
		seenSessionID = ctx.SessionID
		ctx.ToolCall.Arguments = map[string]interface{}{"value": "modified"}
		return nil
	}
	reg.SetChain(NewMiddlewareChain(mw))

	ctx := context.WithValue(context.Background(), "task_id", "task-123")
	ctx = context.WithValue(ctx, "session_id", "session-456")
	if _, err := reg.Execute(ctx, ToolCall{
		ID:        "call-1",
		Name:      tool.Name(),
		Arguments: map[string]interface{}{"value": "original"},
	}); err != nil {
		t.Fatalf("registry Execute failed: %v", err)
	}
	if got := tool.got["value"]; got != "modified" {
		t.Fatalf("tool received value %#v, want middleware-modified value", got)
	}
	if seenTaskID != "task-123" || seenSessionID != "session-456" {
		t.Fatalf("middleware context IDs = %q/%q, want task-123/session-456", seenTaskID, seenSessionID)
	}
}
