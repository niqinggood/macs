package app

import (
	"encoding/json"
	"testing"
)

func TestUserMessageToAPI(t *testing.T) {
	msg := &UserMessage{Content: "hello"}
	api := msg.ToAPI()
	if api["role"] != "user" || api["content"] != "hello" {
		t.Error("UserMessage.ToAPI failed")
	}
}

func TestAssistantMessageToAPI(t *testing.T) {
	msg := &AssistantMessage{
		Content:          "answer",
		ReasoningContent: "let me think",
		ToolCalls: []ToolCall{
			{ID: "c1", Name: "read_file", Arguments: map[string]interface{}{"path": "test.txt"}},
		},
	}
	api := msg.ToAPI()
	if api["role"] != "assistant" {
		t.Error("role should be assistant")
	}
	if _, ok := api["reasoning_content"]; !ok {
		t.Error("should have reasoning_content")
	}
	tcs, _ := api["tool_calls"].([]map[string]interface{})
	if len(tcs) != 1 {
		t.Error("should have 1 tool call")
	}
}

func TestToolResultMessageToAPI(t *testing.T) {
	msg := &ToolResultMessage{ToolCallID: "c1", Name: "read_file", Content: "data", Success: true}
	api := msg.ToAPI()
	if api["role"] != "tool" || api["tool_call_id"] != "c1" {
		t.Error("ToolResultMessage.ToAPI failed")
	}
}

func TestSystemMessageToAPI(t *testing.T) {
	msg := &TypedSystemMessage{Content: "sys msg", Subtype: "compact_boundary", Level: SystemInfo}
	api := msg.ToAPI()
	if api["role"] != "system" || api["subtype"] != "compact_boundary" {
		t.Errorf("SystemMessage.ToAPI failed: %v", api)
	}
}

func TestCompactBoundary(t *testing.T) {
	cb := NewCompactBoundary("auto", 50000)
	api := cb.ToAPI()
	if api["subtype"] != "compact_boundary" {
		t.Error("should have compact_boundary subtype")
	}
}

func TestAPIError(t *testing.T) {
	err := NewAPIError("429 Too Many Requests")
	if !err.Retryable || err.Category != "rate_limit" {
		t.Errorf("APIError classification wrong: retryable=%v category=%s", err.Retryable, err.Category)
	}
	api := err.ToAPI()
	if api["level"] != SystemError {
		t.Error("API error should be error level")
	}
}

func TestMessageBuilder(t *testing.T) {
	builder := NewMessageBuilder().
		System("you are a bot").
		User("hello").
		Assistant("hi there").
		ToolResult("c1", "read_file", "data", true).
		CompactBoundary("auto", 10000)

	msgs := builder.Build()
	if len(msgs) != 5 {
		t.Errorf("expected 5 messages, got %d", len(msgs))
	}
	if msgs[0].Type() != "system" || msgs[1].Type() != "user" || msgs[2].Type() != "assistant" {
		t.Error("wrong message order")
	}

	apiMsgs := builder.BuildAPI()
	if len(apiMsgs) != 5 {
		t.Errorf("expected 5 API messages, got %d", len(apiMsgs))
	}
}

func TestFromAPIMessage_RoundTrip(t *testing.T) {
	// User message round trip
	userAPI := map[string]interface{}{"role": "user", "content": "hello"}
	userMsg := FromAPIMessage(userAPI)
	if userMsg.Type() != "user" {
		t.Error("should parse user message")
	}
	backToAPI := userMsg.ToAPI()
	if backToAPI["content"] != "hello" {
		t.Error("round trip failed for user message")
	}

	// Assistant with tool calls round trip
	assistantAPI := map[string]interface{}{
		"role":    "assistant",
		"content": "ok",
		"tool_calls": []interface{}{
			map[string]interface{}{
				"id": "c1",
				"function": map[string]interface{}{
					"name":      "read_file",
					"arguments": `{"path":"test.txt"}`,
				},
			},
		},
	}
	assistantMsg := FromAPIMessage(assistantAPI)
	if assistantMsg.Type() != "assistant" {
		t.Error("should parse assistant message")
	}
	assistantTyped, ok := assistantMsg.(*AssistantMessage)
	if !ok || len(assistantTyped.ToolCalls) != 1 {
		t.Error("should have 1 tool call")
	}

	// Tool result round trip
	toolAPI := map[string]interface{}{"role": "tool", "tool_call_id": "c1", "name": "read_file", "content": "ok", "status": true}
	toolMsg := FromAPIMessage(toolAPI)
	if toolMsg.Type() != "tool" {
		t.Error("should parse tool message")
	}
}

func TestThinkingMessage(t *testing.T) {
	msg := &ThinkingMessage{Content: "let me analyze this...", Duration: 1500}
	if msg.Type() != "thinking" {
		t.Error("thinking message type should be thinking")
	}
	api := msg.ToAPI()
	if api["subtype"] != "thinking" {
		t.Error("thinking message should have thinking subtype")
	}
}

func TestMessageTypes_AllTypes(t *testing.T) {
	allTypes := []Message{
		&UserMessage{Content: "q"},
		&AssistantMessage{Content: "a"},
		&ToolResultMessage{ToolCallID: "1", Name: "t", Content: "r", Success: true},
		&TypedSystemMessage{Content: "s"},
		&CompactBoundaryMessage{},
		&ThinkingMessage{Content: "th"},
	}
	expectedTypes := []string{"user", "assistant", "tool", "system", "system", "thinking"}
	for i, msg := range allTypes {
		if msg.Type() != expectedTypes[i] {
			t.Errorf("msg %d: expected type %s, got %s", i, expectedTypes[i], msg.Type())
		}
		api := msg.ToAPI()
		if api == nil {
			t.Errorf("msg %d: ToAPI returned nil", i)
		}
	}
}

func TestToAPIMessages(t *testing.T) {
	msgs := []Message{
		&UserMessage{Content: "q"},
		&AssistantMessage{Content: "a"},
	}
	apiMsgs := ToAPIMessages(msgs)
	if len(apiMsgs) != 2 {
		t.Error("should convert all messages")
	}
	if apiMsgs[0]["role"] != "user" || apiMsgs[1]["role"] != "assistant" {
		t.Error("wrong conversion")
	}
}

func TestAPIErrorJSONRoundTrip(t *testing.T) {
	err := NewAPIError("401 Unauthorized")
	data, _ := json.Marshal(err)
	var restored APIErrorMessage
	json.Unmarshal(data, &restored)
	if restored.Category != "auth" || restored.Retryable {
		t.Error("JSON round trip failed")
	}
}
