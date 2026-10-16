package eventbus

import (
	"encoding/json"
	"fmt"
)

// AG-UI Protocol implementation — standardized agent↔UI communication.
// Compatible with CopilotKit, LangChain, Google ADK, AWS Strands agents.
// Spec: https://github.com/ag-ui-protocol/ag-ui

// AGUIEventType defines standard AG-UI event types.
type AGUIEventType string

const (
	AGUIRunStarted         AGUIEventType = "RUN_STARTED"
	AGUIRunFinished        AGUIEventType = "RUN_FINISHED"
	AGUIRunError           AGUIEventType = "RUN_ERROR"
	AGUITextMessageStart   AGUIEventType = "TEXT_MESSAGE_START"
	AGUITextMessageContent AGUIEventType = "TEXT_MESSAGE_CONTENT"
	AGUITextMessageEnd     AGUIEventType = "TEXT_MESSAGE_END"
	AGUIToolCallStart      AGUIEventType = "TOOL_CALL_START"
	AGUIToolCallArgs       AGUIEventType = "TOOL_CALL_ARGS"
	AGUIToolCallEnd        AGUIEventType = "TOOL_CALL_END"
	AGUIToolCallResult     AGUIEventType = "TOOL_CALL_RESULT"
	AGUIStateSnapshot      AGUIEventType = "STATE_SNAPSHOT"
	AGUIStateDelta         AGUIEventType = "STATE_DELTA"
	AGUIMessagesSnapshot   AGUIEventType = "MESSAGES_SNAPSHOT"
)

// AGUIMessage is a single chat message in AG-UI format.
type AGUIMessage struct {
	ID         string `json:"id"`
	Role       string `json:"role"` // user, assistant, system, tool
	Content    string `json:"content,omitempty"`
	ToolName   string `json:"tool_name,omitempty"`
	ToolCallID string `json:"tool_call_id,omitempty"`
}

// AGUIEvent wraps an AG-UI protocol event.
type AGUIEvent struct {
	Type       AGUIEventType `json:"type"`
	MessageID  string        `json:"messageId,omitempty"`
	Delta      string        `json:"delta,omitempty"`
	ToolName   string        `json:"toolName,omitempty"`
	ToolCallID string        `json:"toolCallId,omitempty"`
	Args       string        `json:"args,omitempty"`
	Result     string        `json:"result,omitempty"`
	Patch      interface{}   `json:"patch,omitempty"` // for STATE_DELTA
}

// AGUIEmitter wraps SSE to emit AG-UI standard events.
type AGUIEmitter struct {
	messageID string
	seq       int
	emitFn    func(eventType string, data interface{})
}

func NewAGUIEmitter(emitFn func(eventType string, data interface{})) *AGUIEmitter {
	return &AGUIEmitter{emitFn: emitFn}
}

// RunStarted emits the start of an agent run.
func (e *AGUIEmitter) RunStarted(runID string) {
	e.emit("agui", AGUIEvent{Type: AGUIRunStarted, MessageID: runID})
}

// RunFinished emits successful completion.
func (e *AGUIEmitter) RunFinished(runID string) {
	e.emit("agui", AGUIEvent{Type: AGUIRunFinished, MessageID: runID})
}

// TextDelta emits incremental text content.
func (e *AGUIEmitter) TextDelta(msgID, delta string) {
	if e.messageID == "" {
		e.messageID = msgID
		e.emit("agui", AGUIEvent{Type: AGUITextMessageStart, MessageID: msgID})
	}
	e.emit("agui", AGUIEvent{Type: AGUITextMessageContent, MessageID: msgID, Delta: delta})
}

// TextEnd closes the current text message.
func (e *AGUIEmitter) TextEnd(msgID string) {
	e.emit("agui", AGUIEvent{Type: AGUITextMessageEnd, MessageID: msgID})
	e.messageID = ""
}

// ToolCallStart emits the start of a tool execution.
func (e *AGUIEmitter) ToolCallStart(toolCallID, toolName, args string) {
	e.emit("agui", AGUIEvent{Type: AGUIToolCallStart, ToolCallID: toolCallID, ToolName: toolName})
	e.emit("agui", AGUIEvent{Type: AGUIToolCallArgs, ToolCallID: toolCallID, Args: args})
}

// ToolCallEnd emits tool completion with result.
func (e *AGUIEmitter) ToolCallEnd(toolCallID, result string) {
	e.emit("agui", AGUIEvent{Type: AGUIToolCallResult, ToolCallID: toolCallID, Result: result})
	e.emit("agui", AGUIEvent{Type: AGUIToolCallEnd, ToolCallID: toolCallID})
}

// StateDelta emits a state change.
func (e *AGUIEmitter) StateDelta(patch map[string]interface{}) {
	e.emit("agui", AGUIEvent{Type: AGUIStateDelta, Patch: patch})
}

// emit sends an AG-UI event through the SSE channel.
func (e *AGUIEmitter) emit(eventType string, evt AGUIEvent) {
	if e.emitFn == nil {
		return
	}
	e.seq++
	data, _ := json.Marshal(evt)
	e.emitFn(eventType, string(data))
}

// SourceEvent is the minimal gateway-event view needed for AG-UI mapping.
type SourceEvent struct {
	Type   string
	Data   interface{}
	TaskID string
}

// MapToAGUI converts an application event to AG-UI format.
func MapToAGUI(evt SourceEvent) *AGUIEvent {
	switch evt.Type {
	case "thinking", "stream":
		return &AGUIEvent{
			Type:  AGUITextMessageContent,
			Delta: fmt.Sprint(evt.Data),
		}
	case "tool_start":
		return &AGUIEvent{
			Type:       AGUIToolCallStart,
			ToolCallID: fmt.Sprint(evt.Data),
		}
	case "tool_result":
		return &AGUIEvent{
			Type:       AGUIToolCallResult,
			ToolCallID: evt.TaskID,
			Result:     fmt.Sprint(evt.Data),
		}
	case "state_change":
		if patch, ok := evt.Data.(map[string]interface{}); ok {
			return &AGUIEvent{Type: AGUIStateDelta, Patch: patch}
		}
	}
	return nil
}
