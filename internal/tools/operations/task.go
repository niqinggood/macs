package operations

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"mac/internal/configvalue"
)

type TaskStatus string

const (
	TaskPending    TaskStatus = "pending"
	TaskInProgress TaskStatus = "in_progress"
	TaskCompleted  TaskStatus = "completed"
	TaskDeleted    TaskStatus = "deleted"
)

type Task struct {
	ID          string         `json:"id"`
	Subject     string         `json:"subject"`
	Description string         `json:"description"`
	Owner       string         `json:"owner,omitempty"`
	Status      TaskStatus     `json:"status"`
	Blocks      []string       `json:"blocks,omitempty"`
	BlockedBy   []string       `json:"blocked_by,omitempty"`
	CreatedAt   int64          `json:"created_at"`
	UpdatedAt   int64          `json:"updated_at"`
	Metadata    map[string]any `json:"metadata,omitempty"`
}

type TaskRuntime interface {
	Create(subject, description, owner string, blockedBy []string) (*Task, error)
	List() ([]*Task, error)
	Get(string) (*Task, error)
	Update(string, map[string]any) (*Task, error)
}

type TaskCreateTool struct{ runtime TaskRuntime }

func NewTaskCreateTool(runtime TaskRuntime) *TaskCreateTool { return &TaskCreateTool{runtime: runtime} }
func (t *TaskCreateTool) Name() string                      { return "task_create" }
func (t *TaskCreateTool) Description() string {
	return "Create a structured task for tracking work. Use for multi-step plans where tasks depend on each other."
}
func (t *TaskCreateTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"subject":     map[string]interface{}{"type": "string", "description": "Brief task title"},
			"description": map[string]interface{}{"type": "string", "description": "What needs to be done"},
			"blocked_by":  map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "description": "Task IDs that must complete before this one"},
		},
		"required": []string{"subject", "description"},
	}
}
func (t *TaskCreateTool) Execute(_ context.Context, params map[string]interface{}) (string, error) {
	if t == nil || t.runtime == nil {
		return "", fmt.Errorf("task runtime is not available")
	}
	task, err := t.runtime.Create(
		configvalue.String(params, "subject", ""),
		configvalue.String(params, "description", ""),
		configvalue.String(params, "owner", ""),
		stringValues(params["blocked_by"]),
	)
	if err != nil {
		return "", err
	}
	data, _ := json.MarshalIndent(task, "", "  ")
	return string(data), nil
}

type TaskListTool struct{ runtime TaskRuntime }

func NewTaskListTool(runtime TaskRuntime) *TaskListTool { return &TaskListTool{runtime: runtime} }
func (t *TaskListTool) Name() string                    { return "task_list" }
func (t *TaskListTool) Description() string {
	return "List all active tasks with their status and dependencies."
}
func (t *TaskListTool) Parameters() map[string]interface{} {
	return map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}
}
func (t *TaskListTool) Execute(context.Context, map[string]interface{}) (string, error) {
	if t == nil || t.runtime == nil {
		return "", fmt.Errorf("task runtime is not available")
	}
	tasks, err := t.runtime.List()
	if err != nil {
		return "", err
	}
	if len(tasks) == 0 {
		return "No active tasks.", nil
	}
	var output strings.Builder
	output.WriteString(fmt.Sprintf("## Tasks (%d)\n\n", len(tasks)))
	for _, task := range tasks {
		icon := map[TaskStatus]string{TaskPending: "○", TaskInProgress: "◐", TaskCompleted: "✅"}[task.Status]
		if icon == "" {
			icon = "○"
		}
		output.WriteString(fmt.Sprintf("- %s **#%s** %s [%s]\n", icon, task.ID, task.Subject, task.Status))
		if len(task.BlockedBy) > 0 {
			output.WriteString(fmt.Sprintf("  Blocked by: #%s\n", strings.Join(task.BlockedBy, ", #")))
		}
	}
	return output.String(), nil
}

type TaskGetTool struct{ runtime TaskRuntime }

func NewTaskGetTool(runtime TaskRuntime) *TaskGetTool { return &TaskGetTool{runtime: runtime} }
func (t *TaskGetTool) Name() string                   { return "task_get" }
func (t *TaskGetTool) Description() string            { return "Get full details of a task by ID." }
func (t *TaskGetTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"task_id": map[string]interface{}{"type": "string", "description": "Task ID"},
		},
		"required": []string{"task_id"},
	}
}
func (t *TaskGetTool) Execute(_ context.Context, params map[string]interface{}) (string, error) {
	if t == nil || t.runtime == nil {
		return "", fmt.Errorf("task runtime is not available")
	}
	task, err := t.runtime.Get(configvalue.String(params, "task_id", ""))
	if err != nil {
		return "", err
	}
	data, _ := json.MarshalIndent(task, "", "  ")
	return string(data), nil
}

type TaskUpdateTool struct{ runtime TaskRuntime }

func NewTaskUpdateTool(runtime TaskRuntime) *TaskUpdateTool { return &TaskUpdateTool{runtime: runtime} }
func (t *TaskUpdateTool) Name() string                      { return "task_update" }
func (t *TaskUpdateTool) Description() string {
	return "Update a task — set status, owner, or dependencies. Use 'in_progress' when starting, 'completed' when done."
}
func (t *TaskUpdateTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"task_id":    map[string]interface{}{"type": "string", "description": "Task ID to update"},
			"status":     map[string]interface{}{"type": "string", "enum": []string{"pending", "in_progress", "completed"}, "description": "New status"},
			"owner":      map[string]interface{}{"type": "string", "description": "Assign to this agent/user"},
			"blocked_by": map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "description": "Task IDs that block this one"},
		},
		"required": []string{"task_id"},
	}
}
func (t *TaskUpdateTool) Execute(_ context.Context, params map[string]interface{}) (string, error) {
	if t == nil || t.runtime == nil {
		return "", fmt.Errorf("task runtime is not available")
	}
	updates := make(map[string]any)
	for _, key := range []string{"status", "owner", "subject", "description"} {
		if value, ok := params[key].(string); ok {
			updates[key] = value
		}
	}
	if _, ok := params["blocked_by"].([]interface{}); ok {
		updates["blocked_by"] = stringValues(params["blocked_by"])
	}
	task, err := t.runtime.Update(configvalue.String(params, "task_id", ""), updates)
	if err != nil {
		return "", err
	}
	data, _ := json.MarshalIndent(task, "", "  ")
	return string(data), nil
}

func stringValues(value interface{}) []string {
	raw, ok := value.([]interface{})
	if !ok {
		return nil
	}
	result := make([]string, 0, len(raw))
	for _, value := range raw {
		if text, ok := value.(string); ok {
			result = append(result, text)
		}
	}
	return result
}
