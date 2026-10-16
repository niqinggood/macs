package security

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"mac/internal/permissionpolicy"
	"mac/internal/shellpolicy"
	"mac/internal/toolcontract"
)

// RiskLevel classifies how dangerous a tool operation is.
type RiskLevel int

const (
	RiskSafe     RiskLevel = 0 // Read-only, no side effects
	RiskModerate RiskLevel = 1 // Local modifications
	RiskHigh     RiskLevel = 2 // Remote/system operations, needs confirmation
	RiskBlocked  RiskLevel = 3 // Never allowed regardless of config
)

func (r RiskLevel) String() string {
	switch r {
	case RiskSafe:
		return "safe"
	case RiskModerate:
		return "moderate"
	case RiskHigh:
		return "high"
	case RiskBlocked:
		return "blocked"
	default:
		return "unknown"
	}
}

// SafetyGuard enforces risk-based access control.
type SafetyGuard struct {
	DangerouslySkipPermissions bool                            // Legacy: if true, skip confirmation for high-risk ops
	PermissionMode             permissionpolicy.PermissionMode // Current permission mode (default/acceptEdits/bypassPermissions/plan)
}

// SetPermissionMode switches the permission mode at runtime (hot-switch).
func (g *SafetyGuard) SetPermissionMode(mode permissionpolicy.PermissionMode) {
	g.PermissionMode = mode
}

// Check returns nil if the operation is allowed, or an error if blocked/needs confirmation.
// confirmNeeded is true when the caller should ask the user before proceeding.
func (g *SafetyGuard) Check(level RiskLevel, action string) (confirmNeeded bool, err error) {
	if g.PermissionMode != "" {
		allowed, needsConfirm, blockReason := permissionpolicy.ResolvePermission(g.PermissionMode, action, nil, toolcontract.IsReadOnlyToolName)
		if !allowed {
			return false, fmt.Errorf("%s", blockReason)
		}
		return needsConfirm, nil
	}
	return false, nil
}

// CheckTool classifies and checks a concrete tool call. Use this when the
// permission mode matters, because mode rules need the tool name, not only a
// pre-rendered action string.
func (g *SafetyGuard) CheckTool(toolName string, args map[string]interface{}) (confirmNeeded bool, err error) {
	action := permissionpolicy.ExtractAction(args)
	level := ClassifyToolAction(toolName, action)
	if g.PermissionMode != "" {
		allowed, needsConfirm, blockReason := permissionpolicy.ResolvePermission(g.PermissionMode, toolName, args, toolcontract.IsReadOnlyToolName)
		if !allowed {
			return false, fmt.Errorf("%s", blockReason)
		}
		return needsConfirm, nil
	}
	_ = level
	return false, nil
}

// ClassifyToolAction returns the risk level for a given tool+action combination.
func ClassifyToolAction(toolName, action string) RiskLevel {
	toolName = strings.ToLower(toolName)

	switch toolName {
	// ============ SAFE (read-only) ============
	case "read_file", "list_dir":
		return RiskSafe
	case "grep", "glob":
		return RiskSafe
	case "snapshot":
		return RiskSafe
	case "knowledge_query", "knowledge", "search_history", "get_stats":
		return RiskSafe
	case "get_todo", "list_todos":
		return RiskSafe
	case "list_skills":
		return RiskSafe
	case "git":
		switch action {
		case "status", "diff", "log", "branch":
			return RiskSafe
		case "add", "commit", "checkout", "restore":
			return RiskModerate
		case "push":
			return RiskHigh
		case "init":
			return RiskModerate
		}
	case "remote":
		switch action {
		case "check_connection", "status":
			return RiskSafe
		case "backup":
			return RiskModerate
		case "upload", "restart", "exec", "deploy":
			return RiskHigh
		}
	case "code_check":
		switch action {
		case "syntax", "lint", "test":
			return RiskSafe
		case "format", "build":
			return RiskModerate
		case "run":
			return RiskHigh // Running arbitrary code
		}

	// ============ MODERATE (local modifications) ============
	case "write_file", "edit_file", "json_patch":
		return RiskModerate
	case "project_init":
		return RiskModerate
	case "create_todo", "update_todo", "cancel_todo":
		return RiskModerate
	case "convert", "extract", "speak":
		return RiskModerate
	case "screenshot", "image_process":
		return RiskModerate
	case "markdown_to_pdf":
		return RiskModerate
	case "email":
		return RiskModerate
	case "plan_task", "update_plan_step":
		return RiskModerate

	// ============ HIGH (system/remote) ============
	case "cli":
		analysis := shellpolicy.Analyze(action)
		if analysis.Blocked {
			return RiskBlocked
		}
		switch analysis.Class {
		case shellpolicy.ClassReadOnly, shellpolicy.ClassBuildTest:
			return RiskModerate
		case shellpolicy.ClassWrite, shellpolicy.ClassLongRunning, shellpolicy.ClassDestructive, shellpolicy.ClassUnknown:
			return RiskHigh
		}
		return RiskHigh // Shell command execution is high risk
	case "sys":
		switch action {
		case "info", "ps", "disk", "memory", "env", "which":
			return RiskSafe
		case "install":
			return RiskHigh
		}
	case "external_agent":
		return RiskHigh
	case "vision":
		return RiskModerate
	}

	return RiskModerate // Default: unknown tools are moderate risk
}

func allowTrustedExternalAgentDelegate(ctx context.Context, toolCall toolcontract.Call) bool {
	if toolCall.Name != "external_agent" {
		return false
	}
	employeeID, _ := ctx.Value("ae_employee_id").(string)
	employeeID = strings.TrimSpace(employeeID)
	agent, _ := toolCall.Arguments["agent"].(string)
	agent = strings.ToLower(strings.TrimSpace(agent))
	switch employeeID {
	case "codex-specialist":
		return agent == "codex"
	case "cc-specialist":
		return agent == "claude"
	default:
		return false
	}
}

func isReadOnlyCLICommand(command string) bool {
	command = strings.TrimSpace(strings.ToLower(command))
	if command == "" {
		return false
	}
	analysis := shellpolicy.Analyze(command)
	if analysis.Blocked {
		return false
	}
	if strings.ContainsAny(command, ">|;&") {
		return false
	}
	return analysis.Class == shellpolicy.ClassReadOnly
}

// IsProtectedPath checks if a path targets a system-critical directory.
// Operations on protected paths may be blocked or require confirmation.
func IsProtectedPath(path string) bool {
	if path == "" {
		return false
	}

	absPath := path
	if !filepath.IsAbs(path) {
		absPath = filepath.Clean(path)
	}

	absPath = strings.ToLower(filepath.Clean(absPath))
	unixStylePath := strings.ReplaceAll(absPath, "\\", "/")
	winStylePath := strings.ReplaceAll(absPath, "/", "\\")

	// Unix protected paths
	protectedUnix := []string{
		"/", "/root", "/etc", "/boot", "/bin", "/sbin",
		"/usr/bin", "/usr/sbin", "/lib", "/lib64",
		"/sys", "/proc", "/dev",
		"/var/log", "/var/run",
	}

	// Windows protected paths
	protectedWin := []string{
		`c:\windows`, `c:\windows\system32`, `c:\windows\syswow64`,
		`c:\program files`, `c:\program files (x86)`,
		`c:\boot`, `c:\efi`,
	}

	for _, p := range protectedUnix {
		if unixStylePath == p || strings.HasPrefix(unixStylePath, p+"/") {
			return true
		}
	}
	for _, p := range protectedWin {
		if winStylePath == p || strings.HasPrefix(winStylePath, p+"\\") {
			return true
		}
	}

	return false
}

// IsDangerousFileCommand checks if a command string contains system-destructive file operations.
func IsDangerousFileCommand(cmd string) bool {
	cmdLower := strings.ToLower(cmd)

	if shellpolicy.Analyze(cmd).Blocked {
		return true
	}

	systemDestructivePatterns := []string{
		`(?i)(^|[;&|]\s*)format(\.com)?\s+[a-z]:`,
		`(?i)\bmkfs(\.[a-z0-9]+)?\b[^;&|]*/dev/`,
		`(?i)\b(fdisk|parted|gparted)\b[^;&|]*/dev/`,
		`(?i)\bdd\b[^;&|]*\bof=/dev/(sd|hd|nvme|vd|xvd|disk)`,
		`(?i)(^|[;&|]\s*)(shutdown|reboot|halt|poweroff)\b`,
		`(?i)(^|[;&|]\s*)(stop-computer|restart-computer)\b`,
		`(?i):\(\)\s*\{`, // fork bomb
		`(?i)(^|[;&|]\s*)chmod\b[^;&|]*(777|666)[^;&|]*(\s/|\s/etc|\s/bin|\s/usr|\s/boot|\s/sys|\s/proc|\s/dev)`,
		`(?i)(^|[;&|]\s*)chown\b[^;&|]*(\s/|\s/etc|\s/bin|\s/usr|\s/boot|\s/sys|\s/proc|\s/dev)`,
	}
	for _, pattern := range systemDestructivePatterns {
		if regexp.MustCompile(pattern).MatchString(cmdLower) {
			return true
		}
	}

	for _, target := range ShellRedirectionTargets(cmd) {
		if IsAllowedRedirectionTarget(target) {
			continue
		}
		if IsProtectedPath(target) || IsRawBlockDevicePath(target) {
			return true
		}
	}

	return false
}

func ShellRedirectionTargets(cmd string) []string {
	if !strings.Contains(cmd, ">") {
		return nil
	}
	re := regexp.MustCompile(`(?i)(?:\d*)>{1,2}\s*("[^"]+"|'[^']+'|[^"\s'&|;]+)`)
	matches := re.FindAllStringSubmatch(cmd, -1)
	targets := make([]string, 0, len(matches))
	for _, match := range matches {
		if len(match) < 2 {
			continue
		}
		target := strings.Trim(match[1], `"'`)
		if target != "" {
			targets = append(targets, target)
		}
	}
	return targets
}

func IsAllowedRedirectionTarget(target string) bool {
	target = strings.ToLower(strings.TrimSpace(strings.Trim(target, `"'`)))
	switch target {
	case "nul", "$null", "/dev/null", "/dev/zero", "/dev/random", "/dev/urandom":
		return true
	default:
		return false
	}
}

func IsRawBlockDevicePath(target string) bool {
	target = strings.ToLower(strings.TrimSpace(strings.Trim(target, `"'`)))
	return regexp.MustCompile(`^/dev/(sd[a-z]\d*|hd[a-z]\d*|nvme\d+n\d+(p\d+)?|vd[a-z]\d*|xvd[a-z]\d*|disk/)`).MatchString(target)
}

// GetSystemProtectionWarning returns a user-facing warning about protected paths.
func GetSystemProtectionWarning(path string) string {
	return fmt.Sprintf(
		"WARNING: '%s' is a system-protected path. Operations that modify system directories are restricted. "+
			"If you need to modify system files, use a specific subdirectory path.",
		path,
	)
}
