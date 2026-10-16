package agents

import (
	"context"
	"fmt"
	"strings"

	"mac/internal/aiemployee"
	"mac/internal/configvalue"
)

type CronTaskRuntime interface {
	AddTask(aiemployee.ScheduledTask) error
	RemoveTask(string) error
	ListTasks() []aiemployee.ScheduledTask
}

type CronTaskTool struct {
	runtime CronTaskRuntime
}

func NewCronTaskTool(runtime CronTaskRuntime) *CronTaskTool {
	return &CronTaskTool{runtime: runtime}
}

func (t *CronTaskTool) Name() string { return "cron_task" }

func (t *CronTaskTool) Description() string {
	return "Manage scheduled AI tasks. Use cron expressions (e.g. '0 9 * * *' for daily at 9am, '0 */6 * * *' for every 6 hours). " +
		"Actions: list (show all tasks), add (schedule new), remove (delete task)."
}

func (t *CronTaskTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"action": map[string]interface{}{
				"type":        "string",
				"description": "Action: 'list' (show all), 'add' (schedule new), 'remove' (delete)",
				"enum":        []string{"list", "add", "remove"},
			},
			"name": map[string]interface{}{
				"type":        "string",
				"description": "Task name (for add/remove)",
			},
			"cron_expr": map[string]interface{}{
				"type":        "string",
				"description": "Cron expression (for add). Examples: '0 9 * * 1-5' (weekdays 9am), '0 */2 * * *' (every 2 hours)",
			},
			"prompt": map[string]interface{}{
				"type":        "string",
				"description": "Task prompt/description (for add)",
			},
			"ae_id": map[string]interface{}{
				"type":        "string",
				"description": "Digital employee ID to use (for add)",
			},
		},
		"required": []string{"action"},
	}
}

func (t *CronTaskTool) Execute(_ context.Context, params map[string]interface{}) (string, error) {
	if t == nil || t.runtime == nil {
		return "Cron scheduler not available", nil
	}
	action := configvalue.String(params, "action", "list")
	switch action {
	case "list":
		return t.listTasks(), nil
	case "add":
		name := configvalue.String(params, "name", "")
		cronExpression := configvalue.String(params, "cron_expr", "")
		if name == "" || cronExpression == "" {
			return "Error: name and cron_expr are required for add", nil
		}
		task := aiemployee.ScheduledTask{
			Name:     name,
			CronExpr: cronExpression,
			Prompt:   configvalue.String(params, "prompt", ""),
			DEID:     configvalue.String(params, "ae_id", ""),
			Enabled:  true,
		}
		if err := t.runtime.AddTask(task); err != nil {
			return fmt.Sprintf("Error: %v", err), nil
		}
		return fmt.Sprintf("Task '%s' scheduled: `%s`", name, cronExpression), nil
	case "remove":
		name := configvalue.String(params, "name", "")
		if name == "" {
			return "Error: name is required for remove", nil
		}
		if err := t.runtime.RemoveTask(name); err != nil {
			return fmt.Sprintf("Error: %v", err), nil
		}
		return fmt.Sprintf("Task '%s' removed", name), nil
	default:
		return fmt.Sprintf("Unknown action: %s", action), nil
	}
}

func (t *CronTaskTool) listTasks() string {
	tasks := t.runtime.ListTasks()
	if len(tasks) == 0 {
		return "No scheduled tasks. Use 'add' to schedule one.\n\nCommon cron expressions:\n- `0 9 * * 1-5`: Weekdays at 9am\n- `0 */6 * * *`: Every 6 hours\n- `0 0 * * 0`: Weekly on Sunday midnight"
	}
	var output strings.Builder
	output.WriteString(fmt.Sprintf("Scheduled Tasks (%d):\n", len(tasks)))
	for _, task := range tasks {
		status := "active"
		if !task.Enabled {
			status = "paused"
		}
		output.WriteString(fmt.Sprintf("- **%s**: `%s` [%s]\n", task.Name, task.CronExpr, status))
		if !task.NextRun.IsZero() {
			output.WriteString(fmt.Sprintf("  Next run: %s\n", task.NextRun.Format("2006-01-02 15:04:05")))
		}
	}
	return output.String()
}
