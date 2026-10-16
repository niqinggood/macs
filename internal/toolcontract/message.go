// Package toolcontract defines provider-facing tool and message shapes without
// depending on the Agent runtime or Gateway.
package toolcontract

import (
	"encoding/json"
	"fmt"
	"time"
)

// Message is the interface for all typed messages.
// Claude Code borrowed: message type hierarchy in types/message.ts
type Message interface {
	Type() string
	ToAPI() map[string]interface{}
}

// ToolCallMaps accepts both shapes used inside the process: values decoded
// from JSON ([]interface{}) and values built by Go helpers
// ([]map[string]interface{}). Protocol validation, token accounting and
// compaction must not silently ignore one of them.
func ToolCallMaps(value interface{}) []map[string]interface{} {
	switch calls := value.(type) {
	case []map[string]interface{}:
		return calls
	case []interface{}:
		out := make([]map[string]interface{}, 0, len(calls))
		for _, raw := range calls {
			if call, ok := raw.(map[string]interface{}); ok {
				out = append(out, call)
			}
		}
		return out
	default:
		return nil
	}
}

// ==================== User Message ====================

type UserMessage struct {
	Content   string    `json:"content"`
	Timestamp time.Time `json:"timestamp,omitempty"`
	Meta      bool      `json:"is_meta,omitempty"`
}

func (m *UserMessage) Type() string { return "user" }
func (m *UserMessage) ToAPI() map[string]interface{} {
	result := map[string]interface{}{"role": "user", "content": m.Content}
	if m.Meta {
		result["is_meta"] = true
	}
	return result
}

// ==================== Assistant Message ====================

type AssistantMessage struct {
	Content          string    `json:"content,omitempty"`
	ToolCalls        []Call    `json:"tool_calls,omitempty"`
	ReasoningContent string    `json:"reasoning_content,omitempty"`
	Timestamp        time.Time `json:"timestamp,omitempty"`
}

func (m *AssistantMessage) Type() string { return "assistant" }
func (m *AssistantMessage) ToAPI() map[string]interface{} {
	result := map[string]interface{}{"role": "assistant"}
	if m.Content != "" {
		result["content"] = m.Content
	}
	if len(m.ToolCalls) > 0 {
		toolCallsData := make([]map[string]interface{}, len(m.ToolCalls))
		for i, tc := range m.ToolCalls {
			argsJSON, _ := json.Marshal(tc.Arguments)
			toolCallsData[i] = map[string]interface{}{
				"id":   tc.ID,
				"type": "function",
				"function": map[string]interface{}{
					"name":      tc.Name,
					"arguments": string(argsJSON),
				},
			}
		}
		result["tool_calls"] = toolCallsData
	}
	if m.ReasoningContent != "" {
		result["reasoning_content"] = m.ReasoningContent
	}
	return result
}

// ==================== Tool Result Message ====================

type ToolResultMessage struct {
	ToolCallID string `json:"tool_call_id"`
	Name       string `json:"name"`
	Content    string `json:"content"`
	Success    bool   `json:"success"`
}

func (m *ToolResultMessage) Type() string { return "tool" }
func (m *ToolResultMessage) ToAPI() map[string]interface{} {
	return map[string]interface{}{
		"role":         "tool",
		"tool_call_id": m.ToolCallID,
		"name":         m.Name,
		"content":      m.Content,
		"status":       m.Success,
	}
}

// ==================== System Messages ====================

type TypedSystemMessageLevel string

const (
	SystemInfo    TypedSystemMessageLevel = "info"
	SystemWarning TypedSystemMessageLevel = "warning"
	SystemError   TypedSystemMessageLevel = "error"
)

type TypedSystemMessage struct {
	Content string                  `json:"content"`
	Level   TypedSystemMessageLevel `json:"level,omitempty"`
	Subtype string                  `json:"subtype,omitempty"` // compact_boundary, error, informational, etc.
}

func (m *TypedSystemMessage) Type() string { return "system" }
func (m *TypedSystemMessage) ToAPI() map[string]interface{} {
	result := map[string]interface{}{"role": "system", "content": m.Content}
	if m.Subtype != "" {
		result["subtype"] = m.Subtype
	}
	if m.Level != "" {
		result["level"] = m.Level
	}
	return result
}

// ==================== Compact Boundary (subtype of system) ====================

type CompactBoundaryMessage struct {
	TypedSystemMessage
	Trigger           string `json:"trigger"` // "auto" or "manual"
	PreCompactTokens  int    `json:"pre_compact_tokens"`
	PostCompactTokens int    `json:"post_compact_tokens"`
	PreservedSegment  bool   `json:"preserved_segment,omitempty"`
}

func NewCompactBoundary(trigger string, preTokens int) *CompactBoundaryMessage {
	return &CompactBoundaryMessage{
		TypedSystemMessage: TypedSystemMessage{
			Subtype: "compact_boundary",
			Level:   SystemInfo,
		},
		Trigger:          trigger,
		PreCompactTokens: preTokens,
	}
}

// ==================== Thinking Message ====================

type ThinkingMessage struct {
	Content   string    `json:"content"`
	Duration  int64     `json:"duration_ms,omitempty"`
	Timestamp time.Time `json:"timestamp,omitempty"`
}

func (m *ThinkingMessage) Type() string { return "thinking" }
func (m *ThinkingMessage) ToAPI() map[string]interface{} {
	return map[string]interface{}{"role": "system", "content": m.Content, "subtype": "thinking"}
}

// ==================== Progress Message ====================

type ProgressMessage struct {
	Status    string `json:"status"` // "running", "success", "error"
	ToolName  string `json:"tool,omitempty"`
	AgentName string `json:"agent,omitempty"`
	Message   string `json:"message"`
	TaskID    string `json:"task_id,omitempty"`
}

func (m *ProgressMessage) Type() string { return "progress" }

// ==================== API Error Message ====================

type APIErrorMessage struct {
	TypedSystemMessage
	Error     string `json:"error"`
	Retryable bool   `json:"retryable"`
	Category  string `json:"category"`
}

func NewAPIError(errMsg string, retryable bool, category string) *APIErrorMessage {
	return &APIErrorMessage{
		TypedSystemMessage: TypedSystemMessage{
			Content: fmt.Sprintf("API Error: %s", errMsg),
			Subtype: "api_error",
			Level:   SystemError,
		},
		Error:     errMsg,
		Retryable: retryable,
		Category:  category,
	}
}

// ==================== Hook Result Message ====================

type HookResultMessage struct {
	TypedSystemMessage
	HookType string `json:"hook_type"`
	Allowed  bool   `json:"allowed"`
	Reason   string `json:"reason,omitempty"`
}

// ==================== Conversion Utilities ====================

// ToAPIMessages converts a slice of typed Messages to API format.
func ToAPIMessages(msgs []Message) []map[string]interface{} {
	result := make([]map[string]interface{}, len(msgs))
	for i, m := range msgs {
		result[i] = m.ToAPI()
	}
	return result
}

// FromAPIMessage converts a raw API map to the closest typed message.
func FromAPIMessage(raw map[string]interface{}) Message {
	role, _ := raw["role"].(string)
	switch role {
	case "user":
		content, _ := raw["content"].(string)
		return &UserMessage{Content: content}
	case "assistant":
		msg := &AssistantMessage{}
		if content, ok := raw["content"].(string); ok {
			msg.Content = content
		}
		if reasoning, ok := raw["reasoning_content"].(string); ok {
			msg.ReasoningContent = reasoning
		}
		for _, tcMap := range ToolCallMaps(raw["tool_calls"]) {
			if fn, ok := tcMap["function"].(map[string]interface{}); ok {
				tc := Call{
					ID:   getStringFromMap(tcMap, "id"),
					Name: getStringFromMap(fn, "name"),
				}
				if argsStr, ok := fn["arguments"].(string); ok {
					var args map[string]interface{}
					json.Unmarshal([]byte(argsStr), &args)
					tc.Arguments = args
				}
				msg.ToolCalls = append(msg.ToolCalls, tc)
			}
		}
		return msg
	case "tool":
		return &ToolResultMessage{
			ToolCallID: getStringFromMap(raw, "tool_call_id"),
			Name:       getStringFromMap(raw, "name"),
			Content:    getStringFromMap(raw, "content"),
			Success:    getBoolFromMap(raw, "status"),
		}
	case "system":
		msg := &TypedSystemMessage{
			Content: getStringFromMap(raw, "content"),
			Subtype: getStringFromMap(raw, "subtype"),
		}
		if lvl, ok := raw["level"].(string); ok {
			msg.Level = TypedSystemMessageLevel(lvl)
		}
		return msg
	}
	return nil
}

func getStringFromMap(m map[string]interface{}, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

func getBoolFromMap(m map[string]interface{}, key string) bool {
	if v, ok := m[key].(bool); ok {
		return v
	}
	return false
}

// ==================== Message Builder ====================

// MessageBuilder provides a fluent API for building message sequences.
type MessageBuilder struct {
	messages []Message
}

func NewMessageBuilder() *MessageBuilder {
	return &MessageBuilder{}
}

func (b *MessageBuilder) System(content string) *MessageBuilder {
	b.messages = append(b.messages, &TypedSystemMessage{Content: content})
	return b
}

func (b *MessageBuilder) User(content string) *MessageBuilder {
	b.messages = append(b.messages, &UserMessage{Content: content})
	return b
}

func (b *MessageBuilder) Assistant(content string) *MessageBuilder {
	b.messages = append(b.messages, &AssistantMessage{Content: content})
	return b
}

func (b *MessageBuilder) ToolResult(id, name, content string, success bool) *MessageBuilder {
	b.messages = append(b.messages, &ToolResultMessage{ToolCallID: id, Name: name, Content: content, Success: success})
	return b
}

func (b *MessageBuilder) CompactBoundary(trigger string, preTokens int) *MessageBuilder {
	b.messages = append(b.messages, NewCompactBoundary(trigger, preTokens))
	return b
}

func (b *MessageBuilder) Build() []Message {
	return b.messages
}

func (b *MessageBuilder) BuildAPI() []map[string]interface{} {
	return ToAPIMessages(b.messages)
}
