package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"mac/internal/configvalue"
)

type SubAgentStatus struct {
	ID        string    `json:"id"`
	Task      string    `json:"task"`
	AgentType string    `json:"agent_type,omitempty"`
	Status    string    `json:"status"`
	StartedAt time.Time `json:"started_at"`
	EndedAt   time.Time `json:"ended_at,omitempty"`
	Elapsed   string    `json:"elapsed"`
	Progress  []string  `json:"progress,omitempty"`
	Result    string    `json:"result,omitempty"`
	Error     string    `json:"error,omitempty"`
}

type ProgressReporter func(string) error

type ProgressReportTool struct {
	report ProgressReporter
}

func NewProgressReportTool(report ProgressReporter) *ProgressReportTool {
	return &ProgressReportTool{report: report}
}

func (t *ProgressReportTool) Name() string { return "report_progress" }
func (t *ProgressReportTool) Description() string {
	return "Report your progress to the coordinator. Use this during long tasks to keep the coordinator informed about what you're doing and how far along you are."
}
func (t *ProgressReportTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"message": map[string]interface{}{"type": "string", "description": "Progress update, e.g. '40% done, found 3 files, analyzing patterns...'"},
		},
		"required": []string{"message"},
	}
}
func (t *ProgressReportTool) Execute(_ context.Context, params map[string]interface{}) (string, error) {
	message := configvalue.String(params, "message", "")
	if message == "" {
		return "", fmt.Errorf("message required")
	}
	if t == nil || t.report == nil {
		return "", fmt.Errorf("progress reporter is not initialized")
	}
	if err := t.report(message); err != nil {
		return "", err
	}
	return fmt.Sprintf("Progress reported: %s", message), nil
}

type SubAgentController interface {
	KillSubAgent(string) error
	StatusSnapshot(string, bool) []SubAgentStatus
}

type AgentStopTool struct{ controller SubAgentController }

func NewAgentStopTool(controller SubAgentController) *AgentStopTool {
	return &AgentStopTool{controller: controller}
}

func (t *AgentStopTool) Name() string { return "agent_stop" }
func (t *AgentStopTool) Description() string {
	return "Stop a running sub-agent by name. Use when a sub-agent is off-track, timed out, or no longer needed."
}
func (t *AgentStopTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"agent_name": map[string]interface{}{"type": "string", "description": "Name of the sub-agent to stop"},
		},
		"required": []string{"agent_name"},
	}
}
func (t *AgentStopTool) Execute(_ context.Context, params map[string]interface{}) (string, error) {
	if t == nil || t.controller == nil {
		return "", fmt.Errorf("sub-agent manager is not initialized")
	}
	agentName := configvalue.String(params, "agent_name", "")
	if err := t.controller.KillSubAgent(agentName); err != nil {
		return "", err
	}
	return fmt.Sprintf("Sub-agent '%s' has been stopped.", agentName), nil
}

type AgentStatusTool struct{ controller SubAgentController }

func NewAgentStatusTool(controller SubAgentController) *AgentStatusTool {
	return &AgentStatusTool{controller: controller}
}

func (t *AgentStatusTool) Name() string { return "agent_status" }
func (t *AgentStatusTool) Description() string {
	return "List running and recently completed sub-agents, including elapsed time and progress. Set include_result=true to retrieve completed results."
}
func (t *AgentStatusTool) IsReadOnly() bool { return true }
func (t *AgentStatusTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"agent_name": map[string]interface{}{
				"type":        "string",
				"description": "Optional sub-agent name. Empty lists all retained sessions.",
			},
			"include_result": map[string]interface{}{
				"type":        "boolean",
				"description": "Include the full result for completed sessions. Default false.",
			},
		},
	}
}
func (t *AgentStatusTool) Execute(_ context.Context, params map[string]interface{}) (string, error) {
	if t == nil || t.controller == nil {
		return "", fmt.Errorf("sub-agent manager is not initialized")
	}
	agentName := configvalue.String(params, "agent_name", "")
	includeResult, _ := params["include_result"].(bool)
	statuses := t.controller.StatusSnapshot(agentName, includeResult)
	if len(statuses) == 0 {
		if agentName != "" {
			return fmt.Sprintf("No retained sub-agent named %q.", agentName), nil
		}
		return "No running or recently completed sub-agents.", nil
	}
	data, err := json.MarshalIndent(statuses, "", "  ")
	if err != nil {
		return "", err
	}
	return string(data), nil
}
