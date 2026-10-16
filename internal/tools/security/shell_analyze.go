package security

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"mac/internal/configvalue"
	"mac/internal/permissionpolicy"
	"mac/internal/shellpolicy"
)

// ShellAnalyzeTool exposes shell safety analysis without command execution.
type ShellAnalyzeTool struct{}

func NewShellAnalyzeTool() *ShellAnalyzeTool { return &ShellAnalyzeTool{} }

func (tool *ShellAnalyzeTool) Name() string { return "shell_analyze" }

func (tool *ShellAnalyzeTool) Description() string {
	return "Analyze a shell/CLI command without executing it. This tool never returns command output. Use list_dir/read_file for filesystem inspection; if actual command execution is needed and cli is available, call cli separately after analysis."
}

func (tool *ShellAnalyzeTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"command": map[string]interface{}{
				"type":        "string",
				"description": "Shell command to analyze, without executing it.",
			},
			"permission_mode": map[string]interface{}{
				"type":        "string",
				"description": "Permission mode to evaluate against. Defaults to default.",
				"enum":        []string{"default", "acceptEdits", "bypassPermissions", "plan"},
			},
		},
		"required": []string{"command"},
	}
}

func (tool *ShellAnalyzeTool) IsReadOnly() bool { return true }

func (tool *ShellAnalyzeTool) Execute(_ context.Context, params map[string]interface{}) (string, error) {
	command := strings.TrimSpace(configvalue.String(params, "command", ""))
	if command == "" {
		return "", fmt.Errorf("command is required")
	}
	mode := permissionpolicy.PermissionMode(strings.TrimSpace(configvalue.String(params, "permission_mode", string(permissionpolicy.PermissionDefault))))
	if mode == "" {
		mode = permissionpolicy.PermissionDefault
	}
	if !permissionpolicy.IsValidPermissionMode(string(mode)) {
		return "", fmt.Errorf("invalid permission_mode: %s", mode)
	}

	analysis := shellpolicy.Analyze(command)
	allowed, confirmationNeeded, permissionReason := permissionpolicy.ResolvePermission(mode, "cli", map[string]interface{}{"command": command}, nil)
	payload := map[string]interface{}{
		"command":             analysis.Command,
		"normalized":          analysis.Normalized,
		"executed":            false,
		"next_action":         "This command was not executed. Use list_dir/read_file for filesystem inspection, or call cli separately if actual execution is available and needed.",
		"class":               analysis.Class,
		"blocked":             analysis.Blocked,
		"block_reason":        analysis.BlockReason,
		"suggested_rule":      analysis.SuggestedRule,
		"risk":                shellRisk(analysis),
		"permission_mode":     mode,
		"allowed":             allowed,
		"confirmation_needed": confirmationNeeded,
		"permission_reason":   permissionReason,
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return "", fmt.Errorf("encode shell analysis: %w", err)
	}
	return string(data), nil
}

func shellRisk(analysis shellpolicy.CommandAnalysis) string {
	if analysis.Blocked {
		return "blocked"
	}
	switch analysis.Class {
	case shellpolicy.ClassReadOnly, shellpolicy.ClassBuildTest:
		return "moderate"
	case shellpolicy.ClassWrite, shellpolicy.ClassLongRunning, shellpolicy.ClassDestructive, shellpolicy.ClassUnknown:
		return "high"
	default:
		return "high"
	}
}
