// Package agents contains tool implementations that coordinate specialized
// child agents without depending on the application runtime.
package agents

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
)

const (
	defaultSwarmTimeout   = 300 * time.Second
	defaultMaxSwarmAgents = 8
)

type SwarmTask struct {
	AgentName string   `json:"agent_name"`
	Role      string   `json:"role"`
	Task      string   `json:"task"`
	Tools     []string `json:"tools,omitempty"`
}

type SwarmResult struct {
	AgentName string `json:"agent"`
	Role      string `json:"role"`
	Output    string `json:"output"`
	Error     string `json:"error,omitempty"`
	Duration  string `json:"duration"`
}

// SubAgentExecutor is the narrow application boundary needed by SwarmTool.
type SubAgentExecutor func(context.Context, SwarmTask, time.Duration) (string, error)

// SwarmTool parses, schedules, and summarizes a bounded group of agent tasks.
type SwarmTool struct {
	execute   SubAgentExecutor
	timeout   time.Duration
	maxAgents int
}

func NewSwarmTool(execute SubAgentExecutor) *SwarmTool {
	return &SwarmTool{
		execute:   execute,
		timeout:   defaultSwarmTimeout,
		maxAgents: defaultMaxSwarmAgents,
	}
}

func (t *SwarmTool) Name() string { return "swarm" }

func (t *SwarmTool) Description() string {
	return "Spawn multiple AI agents in parallel to work on different aspects of a task simultaneously. " +
		"Each agent gets a specific role+task. Results are collected when all complete. " +
		"Use this for: parallel code analysis, multi-file refactoring, research gathering, or any task that can be parallelized. " +
		"Max 8 concurrent agents. Each agent has access to the full tool set."
}

func (t *SwarmTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"tasks": map[string]interface{}{
				"type": "array",
				"items": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"agent_name": map[string]interface{}{
							"type":        "string",
							"description": "Name for this sub-agent (e.g. 'analyzer', 'writer', 'reviewer')",
						},
						"role": map[string]interface{}{
							"type":        "string",
							"description": "Role description (e.g. 'code analyzer', 'test writer')",
						},
						"task": map[string]interface{}{
							"type":        "string",
							"description": "The specific task for this agent to accomplish.",
						},
						"tools": map[string]interface{}{
							"type":        "array",
							"items":       map[string]interface{}{"type": "string"},
							"description": "Tools this agent can use. Default: read_file, list_dir, grep, glob, web_fetch, cli",
						},
					},
					"required": []string{"agent_name", "role", "task"},
				},
				"description": "List of sub-agent tasks to execute in parallel (max 8).",
			},
		},
		"required": []string{"tasks"},
	}
}

func (t *SwarmTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	tasksRaw, ok := params["tasks"].([]interface{})
	if !ok || len(tasksRaw) == 0 {
		return "Error: tasks array is required", nil
	}

	maxAgents := defaultMaxSwarmAgents
	if t != nil && t.maxAgents > 0 {
		maxAgents = t.maxAgents
	}
	if len(tasksRaw) > maxAgents {
		return fmt.Sprintf("Error: max %d concurrent agents allowed, got %d", maxAgents, len(tasksRaw)), nil
	}

	tasks, errText := parseSwarmTasks(tasksRaw)
	if errText != "" {
		return errText, nil
	}

	timeout := defaultSwarmTimeout
	if t != nil && t.timeout > 0 {
		timeout = t.timeout
	}
	results := make([]SwarmResult, len(tasks))
	startTime := time.Now()
	var wg sync.WaitGroup
	for i, task := range tasks {
		wg.Add(1)
		go func(index int, item SwarmTask) {
			defer wg.Done()
			started := time.Now()
			var output string
			var err error
			if t == nil || t.execute == nil {
				err = fmt.Errorf("agent factory not available")
			} else {
				output, err = t.execute(ctx, item, timeout)
			}
			result := SwarmResult{
				AgentName: item.AgentName,
				Role:      item.Role,
				Output:    output,
				Duration:  time.Since(started).Truncate(time.Millisecond).String(),
			}
			if err != nil {
				result.Error = err.Error()
			}
			results[index] = result
		}(i, task)
	}
	wg.Wait()

	for i := range results {
		if results[i].Output == "" && results[i].Error == "" {
			results[i].Error = "agent returned empty result (possible timeout or crash)"
		}
		if i > 0 && results[i].Output != "" && results[i].Output == results[i-1].Output {
			results[i].Output = "[Duplicate of agent " + results[i-1].AgentName + " result]"
		}
	}
	return FormatSwarmResults(results, time.Since(startTime).Truncate(time.Millisecond)), nil
}

func parseSwarmTasks(values []interface{}) ([]SwarmTask, string) {
	tasks := make([]SwarmTask, 0, len(values))
	for _, value := range values {
		taskMap, ok := value.(map[string]interface{})
		if !ok {
			return nil, "Error: each task must be an object"
		}
		task := SwarmTask{
			AgentName: stringParam(taskMap, "agent_name", "worker"),
			Role:      stringParam(taskMap, "role", "assistant"),
			Task:      stringParam(taskMap, "task", ""),
		}
		if tools, ok := taskMap["tools"].([]interface{}); ok {
			for _, value := range tools {
				if name, ok := value.(string); ok {
					task.Tools = append(task.Tools, name)
				}
			}
		}
		if task.Tools == nil {
			task.Tools = []string{"read_file", "list_dir", "grep", "glob", "web_fetch", "cli"}
		}
		if task.Task == "" {
			return nil, "Error: each task must have a 'task' field"
		}
		tasks = append(tasks, task)
	}
	return tasks, ""
}

func stringParam(values map[string]interface{}, key, fallback string) string {
	if value, ok := values[key].(string); ok {
		return value
	}
	return fallback
}

func FormatSwarmResults(results []SwarmResult, totalDuration time.Duration) string {
	var output strings.Builder
	output.WriteString(fmt.Sprintf("## Swarm Results (%d agents, %s total)\n\n", len(results), totalDuration))

	successCount := 0
	for index, result := range results {
		icon := "✓"
		if result.Error != "" {
			icon = "✗"
		} else {
			successCount++
		}
		output.WriteString(fmt.Sprintf("### %s %s (%s) [%s]\n", icon, result.AgentName, result.Role, result.Duration))
		if result.Error != "" {
			output.WriteString(fmt.Sprintf("Error: %s\n\n", result.Error))
		} else {
			output.WriteString(fmt.Sprintf("%s\n\n", truncateRunes(result.Output, 2000)))
		}
		if index < len(results)-1 {
			output.WriteString("---\n\n")
		}
	}
	output.WriteString(fmt.Sprintf("**Summary**: %d/%d agents completed successfully.\n", successCount, len(results)))
	return output.String()
}

func truncateRunes(value string, limit int) string {
	runes := []rune(value)
	if limit <= 0 || len(runes) <= limit {
		return value
	}
	return string(runes[:limit]) + "..."
}
