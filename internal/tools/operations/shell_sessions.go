package operations

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"mac/internal/processutil"
	"mac/internal/textutil"
)

type TerminalSessionLister func(context.Context) []processutil.TerminalSessionInfo

type ShellSessionsTool struct {
	workspace    string
	manager      *processutil.ShellJobManager
	listSessions TerminalSessionLister
}

func NewShellSessionsTool(workspace string, manager *processutil.ShellJobManager, listSessions TerminalSessionLister) *ShellSessionsTool {
	return &ShellSessionsTool{workspace: workspace, manager: manager, listSessions: listSessions}
}

func (t *ShellSessionsTool) Name() string { return "shell_sessions" }

func (t *ShellSessionsTool) Description() string {
	return "Manage shell sessions and background shell jobs. Actions: list (sessions/jobs), log (tail a background job log), stop (stop a background job). Use after starting cli background=true."
}

func (t *ShellSessionsTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"action": map[string]interface{}{
				"type":        "string",
				"enum":        []string{"list", "log", "stop"},
				"description": "list sessions/jobs, log tail a background job log, stop terminate a background job",
			},
			"job_id": map[string]interface{}{
				"type":        "string",
				"description": "Background shell job id for action=log or action=stop",
			},
			"max_lines": map[string]interface{}{
				"type":        "integer",
				"description": "Maximum log lines to return for action=log. Default: 200.",
			},
		},
		"required": []string{"action"},
	}
}

func (t *ShellSessionsTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	if t == nil || t.manager == nil {
		return "", fmt.Errorf("shell job manager is unavailable")
	}
	action, _ := params["action"].(string)
	switch action {
	case "list":
		return t.list(ctx), nil
	case "log":
		jobID, _ := params["job_id"].(string)
		if jobID == "" {
			return "", fmt.Errorf("job_id is required for action=log")
		}
		maxLines := 200
		if count, ok := params["max_lines"].(float64); ok && count > 0 {
			maxLines = int(count)
		}
		return t.log(ctx, jobID, maxLines)
	case "stop":
		jobID, _ := params["job_id"].(string)
		if jobID == "" {
			return "", fmt.Errorf("job_id is required for action=stop")
		}
		if err := t.manager.StopForContext(ctx, jobID); err != nil {
			return "", err
		}
		return fmt.Sprintf("Stopped shell job %s", jobID), nil
	default:
		return "", fmt.Errorf("unknown action %q", action)
	}
}

func (t *ShellSessionsTool) list(ctx context.Context) string {
	var output strings.Builder
	var sessions []processutil.TerminalSessionInfo
	if t.listSessions != nil {
		sessions = t.listSessions(ctx)
	}
	jobs := t.manager.ListForContext(ctx)
	output.WriteString("## Shell Sessions\n\n")
	if len(sessions) == 0 {
		output.WriteString("No persistent shell sessions.\n\n")
	} else {
		for _, session := range sessions {
			fmt.Fprintf(&output, "- %s type=%s healthy=%v work_dir=%s last_active=%s\n",
				session.TaskID, session.Type, session.Healthy, session.WorkDir, session.LastActive.Format(time.RFC3339))
		}
		output.WriteString("\n")
	}
	output.WriteString("## Background Jobs\n\n")
	if len(jobs) == 0 {
		output.WriteString("No background shell jobs.\n")
		return output.String()
	}
	for _, job := range jobs {
		fmt.Fprintf(&output, "- %s status=%s pid=%d log=%s cmd=%s\n",
			job.ID, job.Status, job.PID, job.LogPath, textutil.Truncate(job.Command, 100))
	}
	return output.String()
}

func (t *ShellSessionsTool) log(ctx context.Context, jobID string, maxLines int) (string, error) {
	job, ok := t.manager.GetForContext(ctx, jobID)
	if !ok {
		return "", fmt.Errorf("shell job %q not found", jobID)
	}
	logPath := filepath.Clean(job.LogPath)
	if !filepath.IsAbs(logPath) {
		logPath = filepath.Join(t.workspace, logPath)
	}
	content, err := processutil.ReadShellJobLog(logPath, maxLines)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("## Shell Job Log: %s\nstatus=%s pid=%d log=%s\n\n```text\n%s\n```",
		job.ID, job.Status, job.PID, job.LogPath, content), nil
}
