package operations

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"mac/internal/configvalue"
	"mac/internal/textutil"
)

// SysAdminTool provides bounded system inspection and package installation.
type SysAdminTool struct {
	workspace string
}

func NewSysAdminTool(workspace string) *SysAdminTool { return &SysAdminTool{workspace: workspace} }

func (tool *SysAdminTool) Name() string { return "sys" }

func (tool *SysAdminTool) Description() string {
	return "System operations: check OS info, list processes, check disk/memory, install packages. " +
		"Actions: info, ps, disk, memory, install, which. Safe: no sudo, no destructive ops."
}

func (tool *SysAdminTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"action": map[string]interface{}{
				"type":        "string",
				"enum":        []string{"info", "ps", "disk", "memory", "install", "which", "env"},
				"description": "Action: info(system info), ps(process list), disk(disk usage), memory(RAM), install(package), which(find tool), env(environment)",
			},
			"target": map[string]interface{}{
				"type":        "string",
				"description": "For install: package name. For ps: process name filter. For which: tool name.",
			},
			"manager": map[string]interface{}{
				"type":        "string",
				"description": "Package manager: pip, npm, apt, brew, go, cargo (auto-detect if empty)",
			},
		},
		"required": []string{"action"},
	}
}

func (tool *SysAdminTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	action := configvalue.String(params, "action", "")
	target := configvalue.String(params, "target", "")
	manager := configvalue.String(params, "manager", "")
	switch action {
	case "info":
		return tool.systemInfo(), nil
	case "ps":
		return tool.listProcesses(ctx, target), nil
	case "disk":
		return tool.diskUsage(ctx), nil
	case "memory":
		return tool.memoryInfo(ctx), nil
	case "install":
		return tool.installPackage(ctx, target, manager)
	case "which":
		return tool.whichTool(target), nil
	case "env":
		return environmentInfo(os.Environ()), nil
	default:
		return "", fmt.Errorf("unknown action: %s", action)
	}
}

func (tool *SysAdminTool) systemInfo() string {
	var result strings.Builder
	result.WriteString(fmt.Sprintf("OS: %s/%s\n", runtime.GOOS, runtime.GOARCH))
	if host, err := os.Hostname(); err == nil {
		result.WriteString(fmt.Sprintf("Host: %s\n", host))
	}
	result.WriteString(fmt.Sprintf("CPUs: %d\n", runtime.NumCPU()))
	result.WriteString(fmt.Sprintf("Go: %s\n", runtime.Version()))
	result.WriteString(fmt.Sprintf("Workspace: %s\n", tool.workspace))
	result.WriteString("\nAvailable tools:\n")
	for _, name := range []string{"git", "python3", "python", "node", "npm", "go", "docker", "kubectl"} {
		if path, err := exec.LookPath(name); err == nil {
			result.WriteString(fmt.Sprintf("  ✅ %s: %s\n", name, path))
		}
	}
	return result.String()
}

func (tool *SysAdminTool) listProcesses(ctx context.Context, filter string) string {
	var output string
	var err error
	if runtime.GOOS == "windows" {
		output, err = systemCommand(ctx, 15*time.Second, "tasklist", "/NH", "/FO", "CSV")
	} else {
		output, err = systemCommand(ctx, 15*time.Second, "ps", "aux", "--sort=-%mem")
		if err != nil {
			output, err = systemCommand(ctx, 15*time.Second, "ps", "aux")
		}
	}
	if err != nil {
		return fmt.Sprintf("Process list error: %v", err)
	}
	output = filterProcessOutput(output, filter)
	return textutil.TruncateRunes(strings.TrimSpace(output), 3000)
}

func filterProcessOutput(output, filter string) string {
	filter = strings.ToLower(strings.TrimSpace(filter))
	if filter == "" {
		return output
	}
	var matched []string
	for _, line := range strings.Split(output, "\n") {
		if strings.Contains(strings.ToLower(line), filter) {
			matched = append(matched, line)
		}
	}
	return strings.Join(matched, "\n")
}

func (tool *SysAdminTool) diskUsage(ctx context.Context) string {
	var output string
	if runtime.GOOS == "windows" {
		output, _ = systemCommand(ctx, 15*time.Second, "wmic", "logicaldisk", "get", "size,freespace,caption")
	} else {
		output, _ = systemCommand(ctx, 15*time.Second, "df", "-h")
	}
	return "Disk Usage:\n" + output
}

func (tool *SysAdminTool) memoryInfo(ctx context.Context) string {
	var output string
	if runtime.GOOS == "windows" {
		output, _ = systemCommand(ctx, 15*time.Second, "wmic", "OS", "get", "TotalVisibleMemorySize,FreePhysicalMemory", "/Value")
	} else {
		output, _ = systemCommand(ctx, 15*time.Second, "free", "-h")
	}
	return "Go Runtime Memory:\n  Alloc: 0 MB\n  Sys: 0 MB\n  NumGC: check runtime\n\nSystem Memory:\n" + output
}

func (tool *SysAdminTool) installPackage(ctx context.Context, packageName, manager string) (string, error) {
	packageName = strings.TrimSpace(packageName)
	if packageName == "" {
		return "", fmt.Errorf("target package name required for install")
	}
	if manager == "" {
		manager = DetectPackageManager(packageName)
	}
	if manager == "" {
		return "", fmt.Errorf("cannot determine package manager. Specify: pip, npm, apt, brew, go")
	}
	if manager == "apt" || manager == "brew" {
		return fmt.Sprintf("⚠️ System package install (%s %s) requires sudo. Use the cli tool with: sudo %s install %s", manager, packageName, manager, packageName), nil
	}
	var command *exec.Cmd
	switch manager {
	case "pip", "pip3", "npm", "cargo", "gem":
		command = exec.CommandContext(ctx, manager, "install", packageName)
	case "go":
		command = exec.CommandContext(ctx, "go", "install", packageName)
	default:
		return "", fmt.Errorf("unsupported manager: %s", manager)
	}
	command.Env = os.Environ()
	command.Dir = tool.workspace
	output, err := command.CombinedOutput()
	if err != nil {
		return fmt.Sprintf("Install failed: %v\n%s", err, string(output)), nil
	}
	return fmt.Sprintf("✅ Installed %s via %s:\n%s", packageName, manager, textutil.TruncateRunes(string(output), 500)), nil
}

func (tool *SysAdminTool) whichTool(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "Usage: sys action=which target=<tool-name>"
	}
	if path, err := exec.LookPath(name); err == nil {
		return fmt.Sprintf("✅ %s: %s", name, path)
	}
	for _, path := range []string{"/usr/local/bin/" + name, "/usr/bin/" + name, "/opt/homebrew/bin/" + name} {
		if _, err := os.Stat(path); err == nil {
			return fmt.Sprintf("✅ %s found at: %s", name, path)
		}
	}
	return fmt.Sprintf("❌ %s not found in PATH", name)
}

func environmentInfo(environment []string) string {
	var result strings.Builder
	result.WriteString("Environment Variables:\n")
	for _, entry := range environment {
		lower := strings.ToLower(entry)
		if strings.Contains(lower, "key") || strings.Contains(lower, "token") ||
			strings.Contains(lower, "secret") || strings.Contains(lower, "password") ||
			strings.Contains(lower, "passwd") {
			parts := strings.SplitN(entry, "=", 2)
			if len(parts) == 2 {
				result.WriteString(parts[0] + "=***hidden***\n")
			}
			continue
		}
		result.WriteString(entry + "\n")
	}
	return result.String()
}

func DetectPackageManager(packageName string) string {
	if strings.Contains(packageName, ".py") || strings.Contains(packageName, "python") {
		return "pip"
	}
	if strings.Contains(packageName, "@") || strings.Contains(packageName, "/") && !strings.Contains(packageName, ".go") {
		return "npm"
	}
	if strings.HasSuffix(packageName, ".go") || strings.Contains(packageName, "github.com") {
		return "go"
	}
	return "pip"
}

func systemCommand(ctx context.Context, timeout time.Duration, name string, arguments ...string) (string, error) {
	commandContext, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	command := exec.CommandContext(commandContext, name, arguments...)
	command.Env = os.Environ()
	output, err := command.CombinedOutput()
	return string(output), err
}
