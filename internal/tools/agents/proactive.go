package agents

import (
	"context"
	"fmt"

	"mac/internal/configvalue"
)

type ProactiveRuntime interface {
	QueueLength() int
	ScheduleTask(string, int)
	SetEnabled(bool)
}

// ProactiveTool exposes the control surface for an application-owned
// background task engine.
type ProactiveTool struct {
	runtime ProactiveRuntime
}

func NewProactiveTool(runtime ProactiveRuntime) *ProactiveTool {
	return &ProactiveTool{runtime: runtime}
}

func (t *ProactiveTool) Name() string { return "proactive" }

func (t *ProactiveTool) Description() string {
	return "Manage autonomous background tasks. Actions: status, schedule, enable, disable"
}

func (t *ProactiveTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"action": map[string]interface{}{
				"type": "string", "enum": []string{"status", "schedule", "enable", "disable"},
			},
			"description": map[string]interface{}{
				"type": "string", "description": "Task description (for schedule)",
			},
			"priority": map[string]interface{}{
				"type": "integer", "description": "Priority 1-5 (for schedule)",
			},
		},
		"required": []string{"action"},
	}
}

func (t *ProactiveTool) Execute(_ context.Context, params map[string]interface{}) (string, error) {
	if t == nil || t.runtime == nil {
		return "Proactive engine not enabled", nil
	}
	action := configvalue.String(params, "action", "status")
	switch action {
	case "status":
		return fmt.Sprintf("Proactive engine running. %d tasks in queue.", t.runtime.QueueLength()), nil
	case "schedule":
		t.runtime.ScheduleTask(
			configvalue.String(params, "description", ""),
			configvalue.Int(params, "priority", 3),
		)
		return "Task scheduled", nil
	case "enable":
		t.runtime.SetEnabled(true)
		return "Proactive mode enabled", nil
	case "disable":
		t.runtime.SetEnabled(false)
		return "Proactive mode disabled", nil
	default:
		return "Unknown action", nil
	}
}
