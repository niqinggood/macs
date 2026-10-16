package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"
)

type modelLedObservationTool struct{}

func (modelLedObservationTool) Name() string { return "model_led_test_action" }
func (modelLedObservationTool) Description() string {
	return "returns a deterministic execution error for agent-loop testing"
}
func (modelLedObservationTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"value": map[string]interface{}{"type": "string"},
		},
	}
}
func (modelLedObservationTool) Execute(context.Context, map[string]interface{}) (string, error) {
	return "", errors.New("ValueError: section 7: text block requires heading or body")
}

func modelLedToolCallJSON(t *testing.T) string {
	t.Helper()
	payload := map[string]interface{}{
		"choices": []interface{}{map[string]interface{}{
			"finish_reason": "tool_calls",
			"message": map[string]interface{}{
				"content": "",
				"tool_calls": []interface{}{map[string]interface{}{
					"id":   "model_selected_action",
					"type": "function",
					"function": map[string]interface{}{
						"name":      "model_led_test_action",
						"arguments": `{"value":"build deck"}`,
					},
				}},
			},
		}},
	}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestPresentationTurnStartsWithModelInsteadOfHostSynthesizedAction(t *testing.T) {
	transport := &scriptedLLMTransport{responses: []scriptedLLMHTTPResponse{{
		contentType: "application/json",
		body:        completionJSON(t, "stop", "模型自主判断后给出的回答。"),
	}}}
	agent := newIncompleteResponseTestAgent(t, transport, false, 3)

	_, _ = agent.Generate(context.Background(), "请制作一份未来一周机票方案 PPTX。")
	if got := transport.requestCount(); got != 1 {
		t.Fatalf("model request count = %d, want the presentation turn to begin with exactly one real model call", got)
	}
	request := requestBodyAt(t, transport, 0)
	messages, err := json.Marshal(request["messages"])
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(messages, []byte(`"role":"tool"`)) {
		t.Fatalf("host synthesized a tool action before the first model decision: %s", messages)
	}
}

func TestToolFailureObservationReturnsToModelUnchanged(t *testing.T) {
	transport := &scriptedLLMTransport{responses: []scriptedLLMHTTPResponse{
		{contentType: "application/json", body: modelLedToolCallJSON(t)},
		{contentType: "application/json", body: completionJSON(t, "stop", "我根据工具错误调整了方案。")},
	}}
	agent := newIncompleteResponseTestAgent(t, transport, false, 4)
	agent.toolRegistry = NewToolRegistry()
	agent.toolRegistry.Register(modelLedObservationTool{})

	output, err := agent.Generate(context.Background(), "制作一份文档，并根据每次工具执行结果自主修正。")
	if err != nil {
		t.Fatalf("agent loop did not recover through the model: %v\n%s", err, output)
	}
	if got := transport.requestCount(); got != 2 {
		t.Fatalf("model request count = %d, want action then observation-driven reasoning", got)
	}
	second := requestBodyAt(t, transport, 1)
	messages, err := json.Marshal(second["messages"])
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range [][]byte{[]byte(`"role":"tool"`), []byte("section 7: text block requires heading or body")} {
		if !bytes.Contains(messages, want) {
			t.Fatalf("model did not receive exact tool observation %q: %s", want, messages)
		}
	}
}
