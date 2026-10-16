package permissionpolicy

import (
	"fmt"
	"strings"
)

// PermissionMode defines the trust level for tool execution.
// Claude Code borrowed: PermissionMode type in types/permissions.ts
type PermissionMode string

const (
	// PermissionDefault: trusted local automation. Tools run without confirmation.
	PermissionDefault PermissionMode = "default"

	// PermissionAcceptEdits: legacy compatibility mode. Tools run without confirmation.
	PermissionAcceptEdits PermissionMode = "acceptEdits"

	// PermissionBypass: explicit no-confirmation mode. Tools run without confirmation.
	PermissionBypass PermissionMode = "bypassPermissions"

	// PermissionPlan: only read-only tools plus plan/update tools are allowed.
	// Write/execute tools are blocked until plan mode exits.
	PermissionPlan PermissionMode = "plan"
)

var AllPermissionModes = []PermissionMode{
	PermissionDefault,
	PermissionAcceptEdits,
	PermissionBypass,
	PermissionPlan,
}

// IsValidPermissionMode checks if a string is a valid permission mode.
func IsValidPermissionMode(mode string) bool {
	switch PermissionMode(mode) {
	case PermissionDefault, PermissionAcceptEdits, PermissionBypass, PermissionPlan:
		return true
	}
	return false
}

// ResolvePermission determines whether a tool call should be allowed, blocked, or confirmed.
// Returns: (allowed bool, confirmNeeded bool, blockReason string)
type ReadOnlyClassifier func(toolName string) bool

func ResolvePermission(mode PermissionMode, toolName string, args map[string]interface{}, classifyReadOnly ReadOnlyClassifier) (allowed bool, confirmNeeded bool, blockReason string) {
	switch mode {
	case PermissionBypass:
		return true, false, ""

	case PermissionPlan:
		isReadOnly := classifyReadOnly != nil && classifyReadOnly(toolName)
		// Plan mode: only read-only tools + plan_task/update_plan_step
		if isReadOnly || toolName == "plan_task" || toolName == "update_plan_step" ||
			toolName == "enter_plan_mode" || toolName == "exit_plan_mode" {
			return true, false, ""
		}
		return false, false, fmt.Sprintf(
			"tool '%s' is blocked in plan mode — only read-only tools are allowed until plan is approved. Use exit_plan_mode to exit plan mode and execute.",
			toolName,
		)

	case PermissionAcceptEdits:
		return true, false, ""

	case PermissionDefault:
		fallthrough
	default:
		return true, false, ""
	}
}

// extractAction gets the action/subcommand from tool arguments if present.
func ExtractAction(args map[string]interface{}) string {
	if args == nil {
		return ""
	}
	if action, ok := args["action"].(string); ok {
		return action
	}
	// For cli: check if command contains dangerous patterns
	if cmd, ok := args["command"].(string); ok {
		return cmd
	}
	// File/network/data tools usually carry their permission target in a
	// dedicated field rather than "action".
	for _, key := range []string{"path", "file_path", "url", "target", "host", "query", "endpoint"} {
		if value, ok := args[key].(string); ok && strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
