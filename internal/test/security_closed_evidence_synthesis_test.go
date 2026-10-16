package app

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
)

func currentNewsToolCallJSON(t *testing.T, content, callID, query string) string {
	t.Helper()
	payload := map[string]interface{}{
		"choices": []interface{}{map[string]interface{}{
			"finish_reason": "tool_calls",
			"message": map[string]interface{}{
				"content": content,
				"tool_calls": []interface{}{map[string]interface{}{
					"id":   callID,
					"type": "function",
					"function": map[string]interface{}{
						"name":      "web_fetch",
						"arguments": `{"query":"` + query + `"}`,
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

func currentNewsStopJSON(t *testing.T, content string) string {
	t.Helper()
	payload := map[string]interface{}{
		"choices": []interface{}{map[string]interface{}{
			"finish_reason": "stop",
			"message": map[string]interface{}{
				"content": content,
			},
		}},
	}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func requestBodyAt(t *testing.T, transport *scriptedLLMTransport, index int) map[string]interface{} {
	t.Helper()
	transport.mu.Lock()
	defer transport.mu.Unlock()
	if index < 0 || index >= len(transport.requests) {
		t.Fatalf("request index %d out of range (count=%d)", index, len(transport.requests))
	}
	var body map[string]interface{}
	if err := json.Unmarshal(transport.requests[index], &body); err != nil {
		t.Fatalf("decode request %d: %v", index, err)
	}
	return body
}

func completeCurrentNewsFixture() string {
	return "1. Event one: concise update.\n" +
		"2. Event two: concise update.\n" +
		"3. Event three: concise update.\n" +
		"4. Event four: concise update.\n" +
		"5. Event five: concise update.\n" +
		"6. Event six: concise update.\n" +
		"7. Event seven: concise update.\n" +
		"8. Event eight: concise update."
}

func TestCurrentNewsDistinctSearchesRemainAvailableUntilModelStops(t *testing.T) {
	const input = "Give me 8 items of today's news."
	complete := completeCurrentNewsFixture()
	transport := &scriptedLLMTransport{responses: []scriptedLLMHTTPResponse{
		{contentType: "application/json", body: currentNewsToolCallJSON(t, "", "broad", "today technology business news")},
		{contentType: "application/json", body: currentNewsToolCallJSON(t, "The first result was incomplete; I will add a date and source.", "dated", "technology business news July 13 2026 official")},
		{contentType: "application/json", body: currentNewsToolCallJSON(t, "I still need one independent source.", "site", "site:reuters.com technology news July 13 2026")},
		{contentType: "application/json", body: currentNewsStopJSON(t, complete)},
	}}
	agent := newIncompleteResponseTestAgent(t, transport, false, 8)
	agent.toolRegistry = NewToolRegistry()
	agent.toolRegistry.Register(&activationTestTool{name: "web_fetch"})

	output, err := agent.Generate(context.Background(), input)
	if err != nil {
		t.Fatalf("Generate rejected model-led retrieval: %v\n%s", err, output)
	}
	if got := transport.requestCount(); got != 4 {
		t.Fatalf("model request count = %d, want three distinct searches and model-selected stop", got)
	}
	for i := 0; i < 4; i++ {
		if _, ok := requestBodyAt(t, transport, i)["tools"]; !ok {
			t.Fatalf("request %d unexpectedly hid search tools", i)
		}
	}
	messages, err := json.Marshal(requestBodyAt(t, transport, 3)["messages"])
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(messages, []byte(`"role":"tool"`)) || !bytes.Contains(messages, []byte("ok")) {
		t.Fatalf("final model call lost the observation transcript: %s", messages)
	}
}

func TestCurrentNewsCanStopAsSoonAsModelHasEnoughEvidence(t *testing.T) {
	const input = "Give me 8 items of today's news."
	complete := completeCurrentNewsFixture()
	transport := &scriptedLLMTransport{responses: []scriptedLLMHTTPResponse{
		{contentType: "application/json", body: currentNewsToolCallJSON(t, "", "query", "today technology news official sources")},
		{contentType: "application/json", body: currentNewsStopJSON(t, complete)},
	}}
	agent := newIncompleteResponseTestAgent(t, transport, false, 8)
	agent.toolRegistry = NewToolRegistry()
	agent.toolRegistry.Register(&activationTestTool{name: "web_fetch"})

	output, err := agent.Generate(context.Background(), input)
	if err != nil {
		t.Fatalf("Generate rejected the model-selected stop: %v\n%s", err, output)
	}
	if got := transport.requestCount(); got != 2 {
		t.Fatalf("model-selected completion triggered %d requests, want 2", got)
	}
	if !bytes.Contains([]byte(output), []byte("1. Event one")) || !bytes.Contains([]byte(output), []byte("8. Event eight")) {
		t.Fatalf("final output lost the complete response: %q", output)
	}
}
