package app

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

type reasoningReplayProbeTool struct{}

func (*reasoningReplayProbeTool) Name() string        { return "list_dir" }
func (*reasoningReplayProbeTool) Description() string { return "reasoning replay protocol probe" }
func (*reasoningReplayProbeTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"path": map[string]interface{}{"type": "string"},
		},
	}
}
func (*reasoningReplayProbeTool) Execute(context.Context, map[string]interface{}) (string, error) {
	return "probe result", nil
}

func reasoningToolCompletionJSON(t *testing.T, reasoning string) string {
	t.Helper()
	message := map[string]interface{}{
		"content": "",
		"tool_calls": []interface{}{map[string]interface{}{
			"id":   "provider_call_1",
			"type": "function",
			"function": map[string]interface{}{
				"name":      "list_dir",
				"arguments": `{"path":"."}`,
			},
		}},
	}
	if reasoning != "" {
		message["reasoning_content"] = reasoning
	}
	payload := map[string]interface{}{
		"choices": []interface{}{map[string]interface{}{
			"finish_reason": "tool_calls",
			"message":       message,
		}},
	}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func reasoningToolCompletionSSE(t *testing.T, reasoning string) string {
	t.Helper()
	delta := map[string]interface{}{
		"tool_calls": []interface{}{map[string]interface{}{
			"index": 0,
			"id":    "provider_call_1",
			"type":  "function",
			"function": map[string]interface{}{
				"name":      "list_dir",
				"arguments": `{"path":"."}`,
			},
		}},
	}
	if reasoning != "" {
		delta["reasoning_content"] = reasoning
	}
	toolChunk, err := json.Marshal(map[string]interface{}{
		"choices": []interface{}{map[string]interface{}{
			"delta":         delta,
			"finish_reason": nil,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	finishChunk, err := json.Marshal(map[string]interface{}{
		"choices": []interface{}{map[string]interface{}{
			"delta":         map[string]interface{}{},
			"finish_reason": "tool_calls",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return "data: " + string(toolChunk) + "\n\n" +
		"data: " + string(finishChunk) + "\n\n" +
		"data: [DONE]\n\n"
}

func capturedLLMRequest(t *testing.T, transport *scriptedLLMTransport, index int) map[string]interface{} {
	t.Helper()
	transport.mu.Lock()
	defer transport.mu.Unlock()
	if index < 0 || index >= len(transport.requests) {
		t.Fatalf("request index %d out of range; request count=%d", index, len(transport.requests))
	}
	var request map[string]interface{}
	if err := json.Unmarshal(transport.requests[index], &request); err != nil {
		t.Fatal(err)
	}
	return request
}

func assistantToolMessageFromRequest(t *testing.T, request map[string]interface{}) map[string]interface{} {
	t.Helper()
	messages, _ := request["messages"].([]interface{})
	for _, raw := range messages {
		message, _ := raw.(map[string]interface{})
		if message["role"] == "assistant" && message["tool_calls"] != nil {
			return message
		}
	}
	t.Fatalf("request has no assistant tool_calls message: %#v", request["messages"])
	return nil
}

func TestGenerateReplaysProviderReasoningAcrossToolTurn(t *testing.T) {
	// The fake key-shaped token proves log redaction does not mutate the wire
	// request. The terminal control sequence proves streaming UI sanitization is
	// kept separate from the exact provider replay value.
	const reasoning = "\x1b]8;;https://example.invalid\x1b\\provider reasoning sk-abcdefghijklmnopqrstuvwxyz1234567890\x1b]8;;\x1b\\"
	tests := []struct {
		name      string
		streaming bool
		thinking  bool
	}{
		{name: "non-streaming thinking", thinking: true},
		{name: "streaming thinking", streaming: true, thinking: true},
		{name: "non-thinking compatibility"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			firstReasoning := ""
			if test.thinking {
				firstReasoning = reasoning
			}
			firstType := "application/json"
			firstBody := reasoningToolCompletionJSON(t, firstReasoning)
			secondBody := completionJSON(t, "stop", "协议回归测试已完成。")
			if test.streaming {
				firstType = "text/event-stream"
				firstBody = reasoningToolCompletionSSE(t, firstReasoning)
				secondBody = completionSSE(t, "stop", "协议回归测试已完成。")
			}
			transport := &scriptedLLMTransport{responses: []scriptedLLMHTTPResponse{
				{contentType: firstType, body: firstBody},
				{contentType: firstType, body: secondBody},
			}}
			agent := newIncompleteResponseTestAgent(t, transport, test.streaming, 4)
			var safeLogs bytes.Buffer
			agent.fileLogger = log.New(&safeLogs, "", 0)
			registry := NewToolRegistry()
			registry.Register(&reasoningReplayProbeTool{})
			agent.toolRegistry = registry

			output, err := agent.Generate(context.Background(), "执行一次协议回归探针并给出结论")
			if err != nil {
				t.Fatalf("Generate error: %v", err)
			}
			if !strings.Contains(output, "回归测试已完成") {
				t.Fatalf("unexpected output: %q", output)
			}
			if got := transport.requestCount(); got != 2 {
				t.Fatalf("request count = %d, want 2", got)
			}

			assistant := assistantToolMessageFromRequest(t, capturedLLMRequest(t, transport, 1))
			got, exists := assistant["reasoning_content"]
			if test.thinking {
				if !exists || got != reasoning {
					t.Fatalf("reasoning replay = %#v (exists=%v), want exact provider value", got, exists)
				}
			} else if exists {
				t.Fatalf("non-thinking request gained reasoning_content: %#v", assistant)
			}
			if strings.Contains(safeLogs.String(), "sk-abcdefghijklmnopqrstuvwxyz1234567890") || strings.Contains(safeLogs.String(), "provider reasoning") {
				t.Fatalf("provider reasoning leaked into logs: %q", safeLogs.String())
			}
		})
	}
}

type reasoningProtocolErrorTransport struct {
	calls atomic.Int32
}

func (t *reasoningProtocolErrorTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	t.calls.Add(1)
	body := `{"error":{"message":"The reasoning_content in the thinking mode must be passed back to the API.","type":"invalid_request_error"}}`
	return &http.Response{
		StatusCode: http.StatusBadRequest,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    req,
	}, nil
}

func TestReasoningProtocolErrorRetriesSafeModeOnceThenStops(t *testing.T) {
	transport := &reasoningProtocolErrorTransport{}
	agent := newIncompleteResponseTestAgent(t, transport, false, 8)
	agent.modelConfig.BaseURL = "http://always-rejects-reasoning.test/v1"
	agent.modelConfig.ModelName = "always-rejects-reasoning-model"
	clearAdaptiveLLMThinkingDisabled(*agent.modelConfig)
	t.Cleanup(func() { clearAdaptiveLLMThinkingDisabled(*agent.modelConfig) })
	_, err := agent.Generate(context.Background(), "触发协议错误")
	if err == nil {
		t.Fatal("expected protocol error")
	}
	if got := transport.calls.Load(); got != 2 {
		t.Fatalf("provider calls = %d, want one original request plus one safe-mode retry", got)
	}
	if strings.Contains(err.Error(), "tool_calls 格式连续错误") {
		t.Fatalf("protocol error was mislabeled as tool-call format failure: %v", err)
	}
	if !strings.Contains(err.Error(), "reasoning_content") || !strings.Contains(err.Error(), "思考上下文协议") {
		t.Fatalf("protocol error lost actionable diagnosis: %v", err)
	}
}
