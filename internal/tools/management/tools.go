package management

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"mac/internal/configvalue"
	"mac/internal/permissionpolicy"
	"mac/internal/shellpolicy"
	"mac/internal/toolmetrics"
)

type ProjectPermissionRule struct {
	Behavior    string
	ToolName    string
	RuleContent string
}

type PermissionState struct {
	Mode      string
	Workspace string
	RuleCount int
}

type HookState struct {
	Names       []string
	MaxTimeout  time.Duration
	MaxRetries  int
	SandboxMode bool
}

type AgentSummary struct {
	Name            string
	Source          string
	Description     string
	PermissionMode  string
	MaxTurns        int
	ToolCount       int
	DeniedToolCount int
	SourceFile      string
}

type SystemState struct {
	PermissionMode  string
	PermissionReady bool
	PlanMode        bool
	ToolCount       int
	ToolsReady      bool
	HookCount       int
	HooksReady      bool
	SnapshotCount   int
	SnapshotsReady  bool
	WorktreeStatus  string
	WorktreesReady  bool
}

// Runtime keeps application-owned Agent state and persistence outside the
// reusable management tool implementations.
type Runtime interface {
	PermissionState() (PermissionState, bool)
	SetPermissionMode(string)
	LoadPermissionRules(string) ([]ProjectPermissionRule, error)
	SavePermissionRules(string, []ProjectPermissionRule) error
	ReloadPermissionRules([]ProjectPermissionRule)
	ClassifyRisk(string, string) string
	HookState() (HookState, bool)
	AgentManagementAvailable() bool
	DiscoverAgents() ([]AgentSummary, error)
	ReloadAgents() ([]AgentSummary, error)
	SystemState() SystemState
}

// ==================== manage_permissions tool ====================

type ManagePermissionsTool struct {
	runtime Runtime
}

func NewManagePermissionsTool(runtime Runtime) *ManagePermissionsTool {
	return &ManagePermissionsTool{runtime: runtime}
}

func (t *ManagePermissionsTool) Name() string { return "manage_permissions" }

func (t *ManagePermissionsTool) Description() string {
	return "Inspect the current permission mode, list project rules, or suggest a rule for a command. " +
		"This agent tool is read-only. Permission changes belong to host configuration; " +
		"ordinary permitted work can proceed without changing permission modes."
}

func (t *ManagePermissionsTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"action": map[string]interface{}{
				"type":        "string",
				"description": "Read-only action: view, list_rules, suggest_rule",
				"enum":        []string{"view", "list_rules", "suggest_rule"},
			},
			"command": map[string]interface{}{
				"type":        "string",
				"description": "Shell command to generate a suggested cli(...) rule from.",
			},
		},
		"required": []string{"action"},
	}
}

func (t *ManagePermissionsTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	action := configvalue.String(params, "action", "view")
	mode := configvalue.String(params, "mode", "")

	if t.runtime == nil {
		return "Permission system not available", nil
	}

	state, available := t.runtime.PermissionState()
	if !available {
		return "Permission system not available", nil
	}
	currentMode := state.Mode
	if currentMode == "" {
		currentMode = string(permissionpolicy.PermissionDefault)
	}

	workspace := state.Workspace
	if workspace == "" {
		workspace = "."
	}

	if action == "view" {
		return fmt.Sprintf("Current permission mode: **%s**\nProject permission rules: **%d**\n\nAvailable modes:\n- **default**: Allow tools without confirmation; tool-level system protection may still block destructive system operations\n- **acceptEdits**: Legacy allow mode; tools run without confirmation\n- **bypassPermissions**: Explicit no-confirmation mode\n- **plan**: Read-only tools only (planning mode)", currentMode, state.RuleCount), nil
	}

	if action == "set" {
		if mode == "" {
			return "Error: mode is required for 'set' action", nil
		}
		if !permissionpolicy.IsValidPermissionMode(mode) {
			return fmt.Sprintf("Invalid mode '%s'. Use: default, acceptEdits, bypassPermissions, plan", mode), nil
		}
		oldMode := currentMode
		t.runtime.SetPermissionMode(mode)
		return fmt.Sprintf("Permission mode changed: **%s** → **%s**", oldMode, mode), nil
	}

	if action == "list_rules" {
		return t.formatProjectPermissionRules(workspace), nil
	}

	if action == "suggest_rule" {
		command := strings.TrimSpace(configvalue.String(params, "command", ""))
		if command == "" {
			return "Error: command is required for suggest_rule", nil
		}
		analysis := shellpolicy.Analyze(command)
		if analysis.SuggestedRule == "" {
			return "No rule suggestion available for this command.", nil
		}
		return fmt.Sprintf("Suggested rule: `%s`\nClass: %s\nBlocked: %v\nRisk: %s", analysis.SuggestedRule, analysis.Class, analysis.Blocked, t.runtime.ClassifyRisk("cli", command)), nil
	}

	if action == "add_rule" {
		rule, behavior := permissionRuleFromParams(params)
		if rule.ToolName == "" {
			return "Error: rule, command, or tool_name is required for add_rule", nil
		}
		rule.Behavior = behavior
		rules, err := t.runtime.LoadPermissionRules(workspace)
		if err != nil {
			return fmt.Sprintf("Error loading project permission rules: %v", err), nil
		}
		for _, existing := range rules {
			if existing.Behavior == rule.Behavior && existing.ToolName == rule.ToolName && existing.RuleContent == rule.RuleContent {
				return fmt.Sprintf("Rule already exists in %s:\n- %s %s", projectPermissionRulesPath(workspace), rule.Behavior, formatRuleString(rule.ToolName, rule.RuleContent)), nil
			}
		}
		rules = append(rules, rule)
		if err := t.runtime.SavePermissionRules(workspace, rules); err != nil {
			return fmt.Sprintf("Error saving project permission rules: %v", err), nil
		}
		t.runtime.ReloadPermissionRules(rules)
		return fmt.Sprintf("Added project permission rule:\n- %s %s\nSaved: %s", rule.Behavior, formatRuleString(rule.ToolName, rule.RuleContent), projectPermissionRulesPath(workspace)), nil
	}

	if action == "remove_rule" {
		rules, err := t.runtime.LoadPermissionRules(workspace)
		if err != nil {
			return fmt.Sprintf("Error loading project permission rules: %v", err), nil
		}
		if len(rules) == 0 {
			return "No project permission rules to remove.", nil
		}
		index, hasIndex := getIntParam(params, "index")
		removeRule, removeBehavior := permissionRuleFromParams(params)
		var kept []ProjectPermissionRule
		removed := ""
		for i, rule := range rules {
			matches := false
			if hasIndex {
				matches = i == index
			} else if removeRule.ToolName != "" {
				matches = rule.ToolName == removeRule.ToolName && rule.RuleContent == removeRule.RuleContent && (removeBehavior == "" || rule.Behavior == removeBehavior)
			}
			if matches && removed == "" {
				removed = fmt.Sprintf("%s %s", rule.Behavior, formatRuleString(rule.ToolName, rule.RuleContent))
				continue
			}
			kept = append(kept, rule)
		}
		if removed == "" {
			return "No matching project permission rule found.", nil
		}
		if err := t.runtime.SavePermissionRules(workspace, kept); err != nil {
			return fmt.Sprintf("Error saving project permission rules: %v", err), nil
		}
		t.runtime.ReloadPermissionRules(kept)
		return fmt.Sprintf("Removed project permission rule:\n- %s\nSaved: %s", removed, projectPermissionRulesPath(workspace)), nil
	}

	return fmt.Sprintf("Unknown action: %s", action), nil
}

func (t *ManagePermissionsTool) formatProjectPermissionRules(workspace string) string {
	rules, err := t.runtime.LoadPermissionRules(workspace)
	if err != nil {
		return fmt.Sprintf("Error loading project permission rules: %v", err)
	}
	if len(rules) == 0 {
		return "No project permission rules.\nPath: " + projectPermissionRulesPath(workspace)
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Project permission rules (%d):\n", len(rules)))
	for i, rule := range rules {
		sb.WriteString(fmt.Sprintf("%d. %s %s\n", i, rule.Behavior, formatRuleString(rule.ToolName, rule.RuleContent)))
	}
	sb.WriteString("Path: ")
	sb.WriteString(projectPermissionRulesPath(workspace))
	return sb.String()
}

func projectPermissionRulesPath(workspace string) string {
	if strings.TrimSpace(workspace) == "" {
		workspace = "."
	}
	return filepath.Join(workspace, ".openkitty", "permissions.json")
}

func formatRuleString(toolName, content string) string {
	if content == "" {
		return toolName
	}
	return toolName + "(" + content + ")"
}

func permissionRuleFromParams(params map[string]interface{}) (ProjectPermissionRule, string) {
	behavior := strings.TrimSpace(configvalue.String(params, "behavior", "allow"))
	if behavior != "allow" && behavior != "deny" && behavior != "ask" {
		behavior = "allow"
	}
	ruleString := strings.TrimSpace(configvalue.String(params, "rule", ""))
	if ruleString == "" {
		if command := strings.TrimSpace(configvalue.String(params, "command", "")); command != "" {
			ruleString = shellpolicy.Analyze(command).SuggestedRule
		}
	}
	toolName, content := parsePermissionRuleString(ruleString)
	if strings.TrimSpace(toolName) == "" {
		toolName = strings.TrimSpace(configvalue.String(params, "tool_name", ""))
		content = strings.TrimSpace(configvalue.String(params, "rule_content", ""))
	}
	return ProjectPermissionRule{ToolName: toolName, RuleContent: content}, behavior
}

func parsePermissionRuleString(value string) (toolName, content string) {
	value = strings.TrimSpace(value)
	parenIndex := strings.Index(value, "(")
	if parenIndex < 0 {
		return value, ""
	}
	toolName = strings.TrimSpace(value[:parenIndex])
	if end := strings.LastIndex(value, ")"); end > parenIndex {
		content = strings.TrimSpace(value[parenIndex+1 : end])
	}
	return toolName, content
}

func getIntParam(params map[string]interface{}, key string) (int, bool) {
	if params == nil {
		return 0, false
	}
	switch v := params[key].(type) {
	case int:
		return v, true
	case float64:
		return int(v), true
	default:
		return 0, false
	}
}

// ==================== manage_hooks tool ====================

type ManageHooksTool struct {
	runtime Runtime
}

func NewManageHooksTool(runtime Runtime) *ManageHooksTool {
	return &ManageHooksTool{runtime: runtime}
}

func (t *ManageHooksTool) Name() string { return "manage_hooks" }

func (t *ManageHooksTool) Description() string {
	return "View registered hooks and hook configuration. " +
		"Hooks are shell scripts in .openkitty/hooks/ that run at lifecycle events: " +
		"pre_tool_use, post_tool_use, pre_compact, post_compact, session_start, stop."
}

func (t *ManageHooksTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"action": map[string]interface{}{
				"type":        "string",
				"description": "Action: 'list' (show registered hooks), 'config' (show hook settings)",
				"enum":        []string{"list", "config"},
			},
		},
		"required": []string{"action"},
	}
}

func (t *ManageHooksTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	action := configvalue.String(params, "action", "list")

	if t.runtime == nil {
		return "Hook system not available", nil
	}

	hooks, available := t.runtime.HookState()
	if !available {
		return "Hook system not available", nil
	}

	if action == "list" {
		registered := hooks.Names
		if len(registered) == 0 {
			return "No hooks registered. Place scripts in .openkitty/hooks/:\n" +
				"- pre_tool_use.sh (called before each tool)\n" +
				"- post_tool_use.sh (called after each tool)\n" +
				"- pre_compact.sh (before compression)\n" +
				"- post_compact.sh (after compression)\n" +
				"- session_start.sh (on session start)\n" +
				"- stop.sh (on session end)", nil
		}
		var sb strings.Builder
		sb.WriteString("Registered hooks:\n")
		for _, ht := range registered {
			sb.WriteString(fmt.Sprintf("- %s\n", ht))
		}
		return sb.String(), nil
	}

	if action == "config" {
		return fmt.Sprintf("Hook configuration:\n- Timeout: %v\n- Retries: %d\n- Sandbox: %v\n- Directory: .openkitty/hooks/",
			hooks.MaxTimeout, hooks.MaxRetries, hooks.SandboxMode), nil
	}

	return fmt.Sprintf("Unknown action: %s", action), nil
}

// ==================== manage_agents tool ====================

type ManageAgentsTool struct {
	runtime Runtime
}

func NewManageAgentsTool(runtime Runtime) *ManageAgentsTool {
	return &ManageAgentsTool{runtime: runtime}
}

func (t *ManageAgentsTool) Name() string { return "manage_agents" }

func (t *ManageAgentsTool) Description() string {
	return "View and manage loaded agents. " +
		"Agents include built-ins plus .openkitty/agents/*.yaml and .openkitty/agents/*.json files."
}

func (t *ManageAgentsTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"action": map[string]interface{}{
				"type":        "string",
				"description": "Action: 'list' (show all agents), 'reload' (re-scan agents directory)",
				"enum":        []string{"list", "reload"},
			},
		},
		"required": []string{"action"},
	}
}

func (t *ManageAgentsTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	action := configvalue.String(params, "action", "list")

	if t.runtime == nil || !t.runtime.AgentManagementAvailable() {
		return "Agent system not available", nil
	}

	if action == "list" {
		agents, err := t.runtime.DiscoverAgents()
		if err != nil {
			return fmt.Sprintf("Error discovering agents: %v", err), nil
		}
		if len(agents) == 0 {
			return "No agents discovered. Place .yaml or .json files in .openkitty/agents/.", nil
		}
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("Agents (%d):\n", len(agents)))
		for _, a := range agents {
			source := a.Source
			if source == "" {
				source = "file"
			}
			mode := a.PermissionMode
			if mode == "" {
				mode = "inherit"
			}
			maxTurns := "inherit"
			if a.MaxTurns > 0 {
				maxTurns = fmt.Sprintf("%d", a.MaxTurns)
			}
			sb.WriteString(fmt.Sprintf("- **%s** [%s]: %s (mode: %s, max_turns: %s, tools: %d, deny: %d)\n",
				a.Name, source, a.Description, mode, maxTurns, a.ToolCount, a.DeniedToolCount))
		}
		return sb.String(), nil
	}

	if action == "reload" {
		changed, err := t.runtime.ReloadAgents()
		if err != nil {
			return fmt.Sprintf("Error reloading agents: %v", err), nil
		}
		if len(changed) == 0 {
			return "No agent changes detected.", nil
		}
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("Reloaded %d agent(s):\n", len(changed)))
		for _, a := range changed {
			sb.WriteString(fmt.Sprintf("- %s (from %s)\n", a.Name, a.SourceFile))
		}
		return sb.String(), nil
	}

	return fmt.Sprintf("Unknown action: %s", action), nil
}

// ==================== system_status tool ====================

type SystemStatusTool struct {
	runtime Runtime
}

func NewSystemStatusTool(runtime Runtime) *SystemStatusTool {
	return &SystemStatusTool{runtime: runtime}
}

func (t *SystemStatusTool) Name() string { return "system_status" }

func (t *SystemStatusTool) Description() string {
	return "Get a quick overview of system state: permission mode, active hooks, tool count, snapshot count, worktree status, and metrics."
}

func (t *SystemStatusTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type":       "object",
		"properties": map[string]interface{}{},
	}
}

func (t *SystemStatusTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	var sb strings.Builder
	sb.WriteString("## System Status\n\n")
	state := SystemState{}
	if t != nil && t.runtime != nil {
		state = t.runtime.SystemState()
	}

	// Permission mode
	if state.PermissionReady {
		mode := state.PermissionMode
		if mode == "" {
			mode = string(permissionpolicy.PermissionDefault)
		}
		sb.WriteString(fmt.Sprintf("**Permission**: %s\n", mode))
	}

	// Plan mode
	if state.PlanMode {
		sb.WriteString("**Plan Mode**: ACTIVE\n")
	}

	// Tools
	if state.ToolsReady {
		sb.WriteString(fmt.Sprintf("**Tools**: %d registered\n", state.ToolCount))
	}

	// Hooks
	if state.HooksReady {
		if state.HookCount > 0 {
			sb.WriteString(fmt.Sprintf("**Hooks**: %d active\n", state.HookCount))
		}
	}

	// Snapshots
	if state.SnapshotsReady {
		if state.SnapshotCount > 0 {
			sb.WriteString(fmt.Sprintf("**Snapshots**: %d active\n", state.SnapshotCount))
		}
	}

	// Worktrees
	if state.WorktreesReady {
		sb.WriteString(fmt.Sprintf("**Worktrees**: %s\n", state.WorktreeStatus))
	}

	// Metrics
	report := toolmetrics.Report()
	if report != "No tools used yet." {
		sb.WriteString("\n**Tool Usage**:\n")
		sb.WriteString(report)
	}

	return sb.String(), nil
}
