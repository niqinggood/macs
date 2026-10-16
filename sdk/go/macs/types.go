// Package macs provides a client for the MACS AgentOS API.
//
// The package is intentionally transport-only: it does not embed or expose the
// MACS agent runtime. It is currently a repository-local preview package.
package macs

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// Attachment references a file already available inside the MACS workspace.
type Attachment struct {
	Name     string `json:"name,omitempty"`
	Path     string `json:"path"`
	MIMEType string `json:"mime_type,omitempty"`
	Size     int64  `json:"size,omitempty"`
	Kind     string `json:"kind,omitempty"`
}

// SkillRunRequest binds one task to an installed Skill.
type SkillRunRequest struct {
	SkillName       string         `json:"skill_name"`
	Version         string         `json:"version,omitempty"`
	Parameters      map[string]any `json:"parameters,omitempty"`
	ClientReference string         `json:"client_reference,omitempty"`
}

// InitialAssistantMessage is a visible assistant message inserted only into an
// empty session before the first user message.
type InitialAssistantMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// AssistantIdentity gives one task a constrained user-facing assistant name.
type AssistantIdentity struct {
	Name string `json:"name"`
	Role string `json:"role"`
}

// SubmitTaskRequest is the stable AgentOS task submission payload.
type SubmitTaskRequest struct {
	RoleID            string                    `json:"role_id,omitempty"`
	RoleRevision      string                    `json:"role_revision,omitempty"`
	AgentName         string                    `json:"agent_name,omitempty"`
	Message           string                    `json:"message,omitempty"`
	SessionID         string                    `json:"session_id,omitempty"`
	UserID            string                    `json:"user_id,omitempty"`
	Attachments       []Attachment              `json:"attachments,omitempty"`
	Parameters        any                       `json:"parameters,omitempty"`
	SkillRun          *SkillRunRequest          `json:"skill_run,omitempty"`
	InitialMessages   []InitialAssistantMessage `json:"initial_messages,omitempty"`
	AssistantIdentity *AssistantIdentity        `json:"assistant_identity,omitempty"`
}

// AgentRole describes a published AgentOS role.
type AgentRole struct {
	RoleID       string            `json:"role_id"`
	RoleRevision string            `json:"role_revision"`
	AgentName    string            `json:"agent_name"`
	Description  string            `json:"description,omitempty"`
	Enabled      bool              `json:"enabled"`
	InputSchema  map[string]any    `json:"input_schema,omitempty"`
	OutputSchema map[string]any    `json:"output_schema,omitempty"`
	Links        map[string]string `json:"links"`
}

// RoleList is returned by ListRoles.
type RoleList struct {
	Roles []AgentRole `json:"roles"`
	Count int         `json:"count"`
}

// Artifact is a downloadable output produced by a task.
type Artifact struct {
	Name        string `json:"name"`
	Path        string `json:"path"`
	MIMEType    string `json:"mime_type,omitempty"`
	Size        int64  `json:"size,omitempty"`
	Kind        string `json:"kind,omitempty"`
	DownloadURL string `json:"download_url"`
}

// TaskLinks contains relative AgentOS endpoints for a task.
type TaskLinks struct {
	Self      string `json:"self"`
	Events    string `json:"events"`
	Cancel    string `json:"cancel"`
	Followups string `json:"followups"`
	Approvals string `json:"approvals"`
}

// TaskFollowupLinks contains the durable receipt and parent task endpoints.
type TaskFollowupLinks struct {
	Self   string `json:"self"`
	Task   string `json:"task"`
	Events string `json:"events"`
}

// TaskFollowup is one durable instruction added to a pending or running task.
type TaskFollowup struct {
	FollowupID       string            `json:"followup_id"`
	TaskID           string            `json:"task_id"`
	Status           string            `json:"status"`
	Message          string            `json:"message"`
	DeliveryAttempt  int               `json:"delivery_attempt"`
	CreatedAt        time.Time         `json:"created_at"`
	ClaimedAt        *time.Time        `json:"claimed_at,omitempty"`
	AppliedAt        *time.Time        `json:"applied_at,omitempty"`
	CancelledAt      *time.Time        `json:"cancelled_at,omitempty"`
	IdempotentReplay bool              `json:"idempotent_replay"`
	Links            TaskFollowupLinks `json:"links"`
}

// TaskFollowupList is the durable list of instructions accepted for a task.
type TaskFollowupList struct {
	Followups []TaskFollowup `json:"followups"`
	Count     int            `json:"count"`
}

// Task is the durable AgentOS task snapshot. Flexible fields remain raw JSON so
// newer servers can add structured payloads without forcing an SDK update.
type Task struct {
	TaskID           string          `json:"task_id"`
	TenantID         string          `json:"tenant_id"`
	SubjectID        string          `json:"subject_id,omitempty"`
	UserID           string          `json:"user_id,omitempty"`
	RoleID           string          `json:"role_id"`
	RoleRevision     string          `json:"role_revision"`
	AgentName        string          `json:"agent_name,omitempty"`
	SessionID        string          `json:"session_id,omitempty"`
	Status           string          `json:"status"`
	Phase            string          `json:"phase"`
	PhaseCode        string          `json:"phase_code,omitempty"`
	PhaseLabel       string          `json:"phase_label,omitempty"`
	RunAttempt       int             `json:"run_attempt"`
	LastToolCallID   string          `json:"last_tool_call_id,omitempty"`
	HeartbeatAt      *time.Time      `json:"heartbeat_at,omitempty"`
	HeartbeatAgeMS   int64           `json:"heartbeat_age_ms,omitempty"`
	CreatedAt        time.Time       `json:"created_at"`
	CompletedAt      *time.Time      `json:"completed_at,omitempty"`
	Queue            string          `json:"queue,omitempty"`
	WorkerID         string          `json:"worker_id,omitempty"`
	LeaseUntil       *time.Time      `json:"lease_until,omitempty"`
	AvailableAt      *time.Time      `json:"available_at,omitempty"`
	IdempotentReplay bool            `json:"idempotent_replay"`
	SkillRunRequest  json.RawMessage `json:"skill_run_request,omitempty"`
	SkillRun         json.RawMessage `json:"skill_run,omitempty"`
	Result           json.RawMessage `json:"result,omitempty"`
	ErrorMessage     string          `json:"error,omitempty"`
	PendingApproval  json.RawMessage `json:"pending_approval,omitempty"`
	Artifacts        []Artifact      `json:"artifacts,omitempty"`
	Links            TaskLinks       `json:"links"`
}

// Terminal reports whether the server will perform no further task work.
func (task Task) Terminal() bool {
	switch task.Status {
	case "completed", "degraded", "failed", "stopped":
		return true
	default:
		return false
	}
}

// TaskList is one stable cursor page returned by ListTasks.
type TaskList struct {
	Tasks      []Task `json:"tasks"`
	Count      int    `json:"count"`
	NextCursor string `json:"next_cursor,omitempty"`
}

// ListTaskOptions controls server-side task filtering and cursor pagination.
type ListTaskOptions struct {
	CallerID string
	RoleID   string
	Status   string
	Cursor   string
	Limit    int
}

// Event is one Server-Sent Event. Data contains the exact JSON payload emitted
// by the server; callers may decode it into Task or a domain-specific type.
type Event struct {
	ID    string
	Type  string
	Data  json.RawMessage
	Retry time.Duration
}

// Decode unmarshals the event data into target.
func (event Event) Decode(target any) error {
	if len(event.Data) == 0 {
		return fmt.Errorf("MACS SDK: event %q has no data", event.Type)
	}
	if err := json.Unmarshal(event.Data, target); err != nil {
		return fmt.Errorf("MACS SDK: decode event %q: %w", event.Type, err)
	}
	return nil
}

// Progress is the stable, task-agnostic progress payload used by chat, Skill,
// Pipeline, document, coding, research, and multi-Agent tasks.
type Progress struct {
	Type        string `json:"type"`
	AgentName   string `json:"agent,omitempty"`
	TaskID      string `json:"task_id"`
	ExecutionID string `json:"execution_id,omitempty"`
	StepName    string `json:"step_name,omitempty"`
	StepIndex   int    `json:"step_index,omitempty"`
	StepTotal   int    `json:"step_total,omitempty"`
	Attempt     int    `json:"attempt,omitempty"`
	Message     string `json:"message,omitempty"`
	ToolName    string `json:"tool,omitempty"`
	ToolCallID  string `json:"tool_call_id,omitempty"`
	Status      string `json:"status,omitempty"`
	TimestampMS int64  `json:"ts,omitempty"`
}

// Fraction returns completed/total progress when the task reports a bounded
// step position. A task that cannot know its total in advance returns false.
func (progress Progress) Fraction() (float64, bool) {
	if progress.StepTotal <= 0 || progress.StepIndex < 0 || progress.StepIndex > progress.StepTotal {
		return 0, false
	}
	return float64(progress.StepIndex) / float64(progress.StepTotal), true
}

// DecodeProgress decodes a progress event. The boolean is false for other
// event types, allowing one handler to consume a mixed SSE stream.
func (event Event) DecodeProgress() (Progress, bool, error) {
	if event.Type != "progress" {
		return Progress{}, false, nil
	}
	var progress Progress
	if err := event.Decode(&progress); err != nil {
		return Progress{}, true, err
	}
	return progress, true, nil
}

// DecodeTaskSnapshot decodes a durable task_snapshot event.
func (event Event) DecodeTaskSnapshot() (Task, bool, error) {
	if event.Type != "task_snapshot" {
		return Task{}, false, nil
	}
	var task Task
	if err := event.Decode(&task); err != nil {
		return Task{}, true, err
	}
	return task, true, nil
}

// APIError is the structured error returned by AgentOS.
type APIError struct {
	StatusCode int
	Code       string `json:"code"`
	Message    string `json:"message"`
	RequestID  string `json:"request_id"`
	Retryable  bool   `json:"retryable"`
	RetryAfter time.Duration
}

func (err *APIError) Error() string {
	if err == nil {
		return "<nil>"
	}
	message := err.Message
	if message == "" {
		message = http.StatusText(err.StatusCode)
	}
	if err.Code != "" {
		return fmt.Sprintf("MACS AgentOS: %s (%s, HTTP %d)", message, err.Code, err.StatusCode)
	}
	return fmt.Sprintf("MACS AgentOS: %s (HTTP %d)", message, err.StatusCode)
}
