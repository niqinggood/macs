package app

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

type countingStreamSideEffectTool struct {
	calls atomic.Int32
}

func (t *countingStreamSideEffectTool) Name() string { return "stream_side_effect" }
func (t *countingStreamSideEffectTool) Description() string {
	return "counting stream side effect tool"
}
func (t *countingStreamSideEffectTool) Parameters() map[string]interface{} {
	return map[string]interface{}{"type": "object"}
}
func (t *countingStreamSideEffectTool) Execute(context.Context, map[string]interface{}) (string, error) {
	t.calls.Add(1)
	return "ok", nil
}

type suppressedStreamTransport struct{}

func (suppressedStreamTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	body := strings.Join([]string{
		`data: {"choices":[{"delta":{"tool_calls":[{"id":"call_forbidden","function":{"name":"stream_side_effect","arguments":"{}"}}]},"finish_reason":null}]}`,
		"",
		`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		"",
		"data: [DONE]",
		"",
	}, "\n")
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    req,
	}, nil
}

func TestStreamingCallWithoutToolSchemasNeverPreExecutesProviderToolCall(t *testing.T) {
	registry := NewToolRegistry()
	registry.Register(&activationTestTool{name: "stream_side_effect"})
	agent := newToolActivationRequestTestAgent(registry, suppressedStreamTransport{})
	response, err := agent.callLLMStream(
		context.Background(),
		[]map[string]interface{}{{"role": "user", "content": "return text only"}},
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("callLLMStream failed: %v", err)
	}
	if len(response.PreExecResults) != 0 {
		t.Fatalf("provider tool call was pre-executed without an authorized schema: %#v", response.PreExecResults)
	}
	if len(response.ToolCalls) != 1 || response.ToolCalls[0].Name != "stream_side_effect" {
		t.Fatalf("non-executed tool call should remain visible to the upper policy layer: %#v", response.ToolCalls)
	}
}

func TestStreamingReadOnlyResearchNeverPreExecutesProviderToolCall(t *testing.T) {
	registry := NewToolRegistry()
	registry.Register(&activationTestTool{name: "stream_side_effect"})
	agent := newToolActivationRequestTestAgent(registry, suppressedStreamTransport{})
	ctx := withReadOnlyResearchTurn(context.Background(), "search today's news and answer in chat")
	tools := registry.GetDefinitions()
	if len(tools) == 0 {
		t.Fatal("test requires a non-empty authorized tool schema list")
	}

	response, err := agent.callLLMStream(
		ctx,
		[]map[string]interface{}{{"role": "user", "content": "search today's news and answer in chat"}},
		tools,
		nil,
	)
	if err != nil {
		t.Fatalf("callLLMStream failed: %v", err)
	}
	if len(response.PreExecResults) != 0 {
		t.Fatalf("read-only research provider tool call was pre-executed: %#v", response.PreExecResults)
	}
	if len(response.ToolCalls) != 1 || response.ToolCalls[0].Name != "stream_side_effect" {
		t.Fatalf("non-executed tool call should remain visible to the upper policy layer: %#v", response.ToolCalls)
	}
}

func TestStreamingAuthorizedToolExecutesExactlyOnceInAgentToolPath(t *testing.T) {
	registry := NewToolRegistry()
	tool := &countingStreamSideEffectTool{}
	registry.Register(tool)
	agent := newToolActivationRequestTestAgent(registry, suppressedStreamTransport{})

	response, err := agent.callLLMStream(
		context.Background(),
		[]map[string]interface{}{{"role": "user", "content": "perform the action"}},
		registry.GetDefinitions(),
		nil,
	)
	if err != nil {
		t.Fatalf("callLLMStream failed: %v", err)
	}
	if got := tool.calls.Load(); got != 0 {
		t.Fatalf("SSE parsing executed a side-effecting tool %d time(s), want 0", got)
	}
	if len(response.PreExecResults) != 0 {
		t.Fatalf("production streaming must not return hidden pre-execution results: %#v", response.PreExecResults)
	}
	if len(response.ToolCalls) != 1 {
		t.Fatalf("expected one parsed tool call, got %#v", response.ToolCalls)
	}

	run := NewAgentToolExecutor(agent).RunSingleTool(context.Background(), response.ToolCalls[0], RunSingleToolOptions{})
	if run.Err != nil {
		t.Fatalf("agent tool execution failed: %v", run.Err)
	}
	if got := tool.calls.Load(); got != 1 {
		t.Fatalf("side-effecting tool executed %d time(s), want exactly 1", got)
	}
}
