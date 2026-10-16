package eventbus

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"
)

// ====================== SSE Event Type System ======================
// Claude Code 借鉴: 20+ message subtypes in types/message.ts
//
// Enriches SSE streaming with typed events for frontend consumption.
// Current state: only "task_start" / "task_update". This adds:
//   text_delta, tool_use, tool_result, tool_progress, thinking,
//   agent_task_start, agent_task_complete, compact_boundary,
//   api_error, permission_request, session_stats, debug_trace

// SSEEventType enumerates all server-sent event types.
type SSEEventType string

const (
	// Stream events
	SSETextDelta     SSEEventType = "text_delta"
	SSEThinkingDelta SSEEventType = "thinking_delta"
	SSEToolUse       SSEEventType = "tool_use"
	SSEToolResult    SSEEventType = "tool_result"
	SSEToolProgress  SSEEventType = "tool_progress"
	SSEToolError     SSEEventType = "tool_error"

	// Lifecycle events
	SSEAgentTaskStart    SSEEventType = "agent_task_start"
	SSEAgentTaskComplete SSEEventType = "agent_task_complete"
	SSEAgentTaskError    SSEEventType = "agent_task_error"
	SSEAgentTaskProgress SSEEventType = "agent_task_progress"

	// Session events
	SSESessionStart SSEEventType = "session_start"
	SSESessionEnd   SSEEventType = "session_end"
	SSESessionStats SSEEventType = "session_stats"

	// System events
	SSECompactBoundary   SSEEventType = "compact_boundary"
	SSEAPIError          SSEEventType = "api_error"
	SSEPermissionRequest SSEEventType = "permission_request"
	SSEPermissionDenied  SSEEventType = "permission_denied"
	SSEDebugTrace        SSEEventType = "debug_trace"
	SSEInterruptRequest  SSEEventType = "interrupt_request"

	// Legacy (kept for backward compat)
	SSETaskStart  SSEEventType = "task_start"
	SSETaskUpdate SSEEventType = "task_update"
)

// SSEEvent represents a single typed event in the SSE stream.
type SSEEvent struct {
	Type      SSEEventType           `json:"type"`
	Timestamp time.Time              `json:"timestamp"`
	TaskID    string                 `json:"task_id,omitempty"`
	Content   string                 `json:"content,omitempty"`
	Metadata  map[string]interface{} `json:"metadata,omitempty"`
}

// SSEEventBuilder helps construct typed SSE events.
type SSEEventBuilder struct {
	taskID string
}

// NewSSEEventBuilder creates a builder for the given task.
func NewSSEEventBuilder(taskID string) *SSEEventBuilder {
	return &SSEEventBuilder{taskID: taskID}
}

func (b *SSEEventBuilder) newEvent(typ SSEEventType, content string, meta map[string]interface{}) SSEEvent {
	return SSEEvent{
		Type:      typ,
		Timestamp: time.Now(),
		TaskID:    b.taskID,
		Content:   content,
		Metadata:  meta,
	}
}

// ToJSON serializes an event for SSE transmission: "data: {...}\n\n"
func (e SSEEvent) ToJSON() string {
	data, _ := json.Marshal(e)
	return fmt.Sprintf("data: %s\n\n", string(data))
}

// TextDelta creates a text delta event.
func (b *SSEEventBuilder) TextDelta(content string) SSEEvent {
	return b.newEvent(SSETextDelta, content, nil)
}

// ThinkingDelta creates a reasoning/thinking delta event.
func (b *SSEEventBuilder) ThinkingDelta(content string) SSEEvent {
	return b.newEvent(SSEThinkingDelta, content, nil)
}

// ToolUse creates a tool use event.
func (b *SSEEventBuilder) ToolUse(toolName string, args map[string]interface{}, toolCallID string) SSEEvent {
	return b.newEvent(SSEToolUse, "", map[string]interface{}{
		"tool_name":    toolName,
		"tool_call_id": toolCallID,
		"arguments":    args,
	})
}

// ToolResult creates a tool result event.
func (b *SSEEventBuilder) ToolResult(toolName, callID string, content string, duration time.Duration) SSEEvent {
	preview := content
	if len(preview) > 500 {
		preview = preview[:500] + "..."
	}
	return b.newEvent(SSEToolResult, preview, map[string]interface{}{
		"tool_name":    toolName,
		"tool_call_id": callID,
		"full_length":  len(content),
		"duration_ms":  duration.Milliseconds(),
	})
}

// ToolProgress creates a progress event for long-running tools.
func (b *SSEEventBuilder) ToolProgress(toolName, callID string, progress string) SSEEvent {
	return b.newEvent(SSEToolProgress, progress, map[string]interface{}{
		"tool_name":    toolName,
		"tool_call_id": callID,
	})
}

// ToolError creates a tool error event.
func (b *SSEEventBuilder) ToolError(toolName, callID string, err error) SSEEvent {
	return b.newEvent(SSEToolError, err.Error(), map[string]interface{}{
		"tool_name":    toolName,
		"tool_call_id": callID,
	})
}

// AgentTaskStart creates an agent task start event.
func (b *SSEEventBuilder) AgentTaskStart(agentName, agentType string) SSEEvent {
	return b.newEvent(SSEAgentTaskStart, "", map[string]interface{}{
		"agent_name": agentName,
		"agent_type": agentType,
	})
}

// AgentTaskComplete creates an agent task completion event.
func (b *SSEEventBuilder) AgentTaskComplete(agentName string, result string, duration time.Duration) SSEEvent {
	return b.newEvent(SSEAgentTaskComplete, result, map[string]interface{}{
		"agent_name":  agentName,
		"duration_ms": duration.Milliseconds(),
	})
}

// CompactBoundary creates a compaction boundary event.
func (b *SSEEventBuilder) CompactBoundary(summaryLen int, compactedCount int) SSEEvent {
	return b.newEvent(SSECompactBoundary, "", map[string]interface{}{
		"summary_length":     summaryLen,
		"compacted_messages": compactedCount,
	})
}

// APIError creates an API error event with recovery hints.
func (b *SSEEventBuilder) APIError(statusCode int, message string, retryable bool) SSEEvent {
	meta := map[string]interface{}{
		"status_code": statusCode,
		"retryable":   retryable,
	}
	if statusCode == 429 {
		meta["recovery_hint"] = "Rate limited. Try reducing concurrent requests or upgrading your API plan."
	} else if statusCode >= 500 {
		meta["recovery_hint"] = "Server error. The provider may be experiencing issues. Try again in a few minutes."
	} else if statusCode == 413 {
		meta["recovery_hint"] = "Context too long. The conversation will be compacted and retried."
	}
	return b.newEvent(SSEAPIError, message, meta)
}

// PermissionRequest creates a permission request event for tool approval.
func (b *SSEEventBuilder) PermissionRequest(toolName string, args map[string]interface{}) SSEEvent {
	return b.newEvent(SSEPermissionRequest, fmt.Sprintf("Allow %s?", toolName), map[string]interface{}{
		"tool_name": toolName,
		"arguments": args,
	})
}

// SessionStats creates a session statistics event.
func (b *SSEEventBuilder) SessionStats(stats map[string]interface{}) SSEEvent {
	return b.newEvent(SSESessionStats, "", stats)
}

// ====================== Deprecated Helper (backward compat) ======================

// FormatSSEEvent formats a string message as a legacy SSE task_update event.
// Use typed events above for new code.
func FormatSSEEvent(message string) string {
	return fmt.Sprintf("data: {\"type\":\"task_update\",\"content\":%s}\n\n",
		jsonEscape(message))
}

func jsonEscape(s string) string {
	data, _ := json.Marshal(s)
	// unquote: remove surrounding double quotes
	escaped := string(data)
	if len(escaped) >= 2 && escaped[0] == '"' && escaped[len(escaped)-1] == '"' {
		return escaped
	}
	return escaped
}

// ====================== Event Stream Multiplexer ======================

// EventMultiplexer fans out SSE events to multiple channel subscribers.
type EventMultiplexer struct {
	mu          sync.Mutex
	subscribers map[string]chan<- SSEEvent
}

// NewEventMultiplexer creates a new multiplexer.
func NewEventMultiplexer() *EventMultiplexer {
	return &EventMultiplexer{
		subscribers: make(map[string]chan<- SSEEvent),
	}
}

// Subscribe adds a subscriber channel. Returns an unsubscribe function.
func (em *EventMultiplexer) Subscribe(id string, ch chan<- SSEEvent) func() {
	em.mu.Lock()
	defer em.mu.Unlock()
	em.subscribers[id] = ch
	return func() {
		em.mu.Lock()
		defer em.mu.Unlock()
		delete(em.subscribers, id)
	}
}

// Emit sends an event to all subscribers (non-blocking).
func (em *EventMultiplexer) Emit(event SSEEvent) {
	em.mu.Lock()
	defer em.mu.Unlock()
	for _, ch := range em.subscribers {
		select {
		case ch <- event:
		default:
			// drop event for slow consumer
		}
	}
}

// BroadcastText sends a text delta to all subscribers.
func (em *EventMultiplexer) BroadcastText(taskID, text string) {
	event := NewSSEEventBuilder(taskID).TextDelta(text)
	event.TaskID = taskID
	em.Emit(event)
}

// BroadcastProgress sends tool progress to all subscribers.
func (em *EventMultiplexer) BroadcastProgress(taskID, toolName, callID, progress string) {
	event := NewSSEEventBuilder(taskID).ToolProgress(toolName, callID, progress)
	event.TaskID = taskID
	em.Emit(event)
}

// ====================== Migrate from legacy format ======================

// ConvertLegacySSE converts a legacy SSE string to a typed SSEEvent.
func ConvertLegacySSE(rawSSE string) *SSEEvent {
	rawSSE = strings.TrimSpace(rawSSE)
	if !strings.HasPrefix(rawSSE, "data: ") {
		return nil
	}
	payload := strings.TrimPrefix(rawSSE, "data: ")
	if payload == "" {
		return nil
	}
	// Try to parse existing JSON
	var event SSEEvent
	if json.Unmarshal([]byte(payload), &event) == nil && event.Type != "" {
		return &event
	}
	// Fallback: wrap raw message in task_update
	return &SSEEvent{
		Type:      SSETaskUpdate,
		Timestamp: time.Now(),
		Content:   payload,
	}
}
