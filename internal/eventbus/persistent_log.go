package eventbus

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// EventType classifies agent operations per ProjectMem's event-sourced log design.
type EventType string

const (
	EventAttempt  EventType = "attempt"  // agent tries a tool or approach
	EventFix      EventType = "fix"      // agent makes a change/fix
	EventDecision EventType = "decision" // strategic choice or direction change
	EventNote     EventType = "note"     // user input or agent observation
	EventError    EventType = "error"    // something fails
	EventSuccess  EventType = "success"  // task/tool completes successfully
)

// AgentEvent is a typed, immutable record of what the agent did.
// Appended to .openkitty/events.jsonl as append-only log.
type AgentEvent struct {
	Time      time.Time `json:"time"`
	Type      EventType `json:"type"`
	AgentName string    `json:"agent"`
	TaskID    string    `json:"task_id,omitempty"`
	ToolName  string    `json:"tool,omitempty"`
	Summary   string    `json:"summary"`
	Detail    string    `json:"detail,omitempty"`
}

// EventLogger writes structured events to an append-only JSONL log.
type EventLogger struct {
	mu       sync.Mutex
	filePath string
	enabled  bool
}

// NewEventLogger creates an append-only event logger below the workspace.
func NewEventLogger(workspace string) *EventLogger {
	dir := filepath.Join(workspace, ".openkitty")
	_ = os.MkdirAll(dir, 0755)
	return &EventLogger{
		filePath: filepath.Join(dir, "events.jsonl"),
		enabled:  true,
	}
}

// Emit writes an event to the append-only log.
func (l *EventLogger) Emit(e AgentEvent) {
	if l == nil || !l.enabled {
		return
	}
	e.Time = time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	f, err := os.OpenFile(l.filePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return // silent fail — event logging is best-effort
	}
	defer f.Close()
	data, _ := json.Marshal(e)
	f.Write(append(data, '\n'))
}

// EmitAttempt records a tool execution attempt.
func (l *EventLogger) EmitAttempt(agent, taskID, tool, summary string) {
	l.Emit(AgentEvent{Type: EventAttempt, AgentName: agent, TaskID: taskID, ToolName: tool, Summary: summary})
}

// EmitFix records a code change or fix.
func (l *EventLogger) EmitFix(agent, taskID, summary, detail string) {
	l.Emit(AgentEvent{Type: EventFix, AgentName: agent, TaskID: taskID, Summary: summary, Detail: detail})
}

// EmitDecision records a strategic choice.
func (l *EventLogger) EmitDecision(agent, taskID, summary string) {
	l.Emit(AgentEvent{Type: EventDecision, AgentName: agent, TaskID: taskID, Summary: summary})
}

// EmitNote records a user input or agent observation.
func (l *EventLogger) EmitNote(agent, taskID, summary string) {
	l.Emit(AgentEvent{Type: EventNote, AgentName: agent, TaskID: taskID, Summary: summary})
}

// EmitError records a failure.
func (l *EventLogger) EmitError(agent, taskID, tool, summary, detail string) {
	l.Emit(AgentEvent{Type: EventError, AgentName: agent, TaskID: taskID, ToolName: tool, Summary: summary, Detail: detail})
}

// EmitSuccess records a completion.
func (l *EventLogger) EmitSuccess(agent, taskID, summary string) {
	l.Emit(AgentEvent{Type: EventSuccess, AgentName: agent, TaskID: taskID, Summary: summary})
}

// QueryLastN returns the last N events from the log for a given agent.
func (l *EventLogger) QueryLastN(agent string, n int) ([]AgentEvent, error) {
	if l == nil {
		return nil, fmt.Errorf("event log not initialized")
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	data, err := os.ReadFile(l.filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var all []AgentEvent
	for _, line := range bytesToLines(data) {
		var e AgentEvent
		if json.Unmarshal(line, &e) == nil {
			if agent == "" || e.AgentName == agent {
				all = append(all, e)
			}
		}
	}
	if len(all) > n {
		all = all[len(all)-n:]
	}
	return all, nil
}

// QueryFailures returns recent failures for pre-action gate checks.
func (l *EventLogger) QueryFailures(agent, tool string, limit int) []AgentEvent {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	data, err := os.ReadFile(l.filePath)
	if err != nil {
		return nil
	}
	var failures []AgentEvent
	for _, line := range bytesToLines(data) {
		var e AgentEvent
		if json.Unmarshal(line, &e) == nil && e.Type == EventError {
			if (agent == "" || e.AgentName == agent) && (tool == "" || e.ToolName == tool) {
				failures = append(failures, e)
			}
		}
	}
	if len(failures) > limit {
		failures = failures[len(failures)-limit:]
	}
	return failures
}

func bytesToLines(data []byte) [][]byte {
	var lines [][]byte
	start := 0
	for i, b := range data {
		if b == '\n' {
			if i > start {
				lines = append(lines, data[start:i])
			}
			start = i + 1
		}
	}
	if start < len(data) {
		lines = append(lines, data[start:])
	}
	return lines
}
