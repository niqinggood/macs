package workspace

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"mac/internal/configvalue"
)

// SnapshotTool captures a bounded overview of the current workspace.
type SnapshotTool struct {
	workspace       string
	pathGuard       PathGuard
	gitBashResolver func() string
}

type SnapshotOption func(*SnapshotTool)

func WithSnapshotPathGuard(guard PathGuard) SnapshotOption {
	return func(tool *SnapshotTool) { tool.pathGuard = guard }
}

func WithSnapshotGitBashResolver(resolver func() string) SnapshotOption {
	return func(tool *SnapshotTool) { tool.gitBashResolver = resolver }
}

func NewSnapshotTool(workspace string, options ...SnapshotOption) *SnapshotTool {
	tool := &SnapshotTool{workspace: workspace}
	for _, option := range options {
		if option != nil {
			option(tool)
		}
	}
	return tool
}

func (tool *SnapshotTool) Name() string { return "snapshot" }

func (tool *SnapshotTool) Description() string {
	return "Capture current workspace state: file tree, git status, recent changes, environment info. Use this before planning a task to understand what you're working with."
}

func (tool *SnapshotTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"path": map[string]interface{}{
				"type":        "string",
				"description": "Directory to snapshot (defaults to workspace root)",
			},
			"include_git": map[string]interface{}{
				"type":        "boolean",
				"description": "Include git status and recent commits (default true)",
			},
			"max_depth": map[string]interface{}{
				"type":        "integer",
				"description": "Max directory depth for file listing (default 2, max 4)",
				"minimum":     1,
				"maximum":     4,
			},
		},
	}
}

func (tool *SnapshotTool) IsReadOnly() bool { return true }

func (tool *SnapshotTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	requestedPath := strings.TrimSpace(configvalue.String(params, "path", ""))
	if requestedPath == "" {
		requestedPath = "."
	}
	if tool.pathGuard != nil {
		if err := tool.pathGuard(ctx, tool.workspace, requestedPath); err != nil {
			return "", err
		}
	}
	targetPath := requestedPath
	if !filepath.IsAbs(targetPath) {
		targetPath = filepath.Join(tool.workspace, targetPath)
	}
	targetPath = filepath.Clean(targetPath)
	info, err := os.Stat(targetPath)
	if err != nil {
		return "", fmt.Errorf("snapshot path: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("snapshot path is not a directory: %s", targetPath)
	}

	maximumDepth := configvalue.Int(params, "max_depth", 2)
	if maximumDepth < 1 {
		maximumDepth = 1
	} else if maximumDepth > 4 {
		maximumDepth = 4
	}
	var snapshot strings.Builder
	snapshot.WriteString("# Workspace Snapshot\n\n")
	snapshot.WriteString(fmt.Sprintf("**Time**: %s\n", time.Now().Format("2006-01-02 15:04:05")))
	snapshot.WriteString(fmt.Sprintf("**OS**: %s/%s\n", runtime.GOOS, runtime.GOARCH))
	snapshot.WriteString(fmt.Sprintf("**Workspace**: %s\n\n", targetPath))
	snapshot.WriteString("## File Tree\n\n```\n")
	snapshot.WriteString(tool.buildFileTree(targetPath, maximumDepth))
	snapshot.WriteString("```\n\n")
	if configvalue.Bool(params, "include_git", true) {
		if gitInfo := tool.gitInfo(ctx, targetPath); gitInfo != "" {
			snapshot.WriteString("## Git Status\n\n```\n")
			snapshot.WriteString(gitInfo)
			snapshot.WriteString("\n```\n\n")
		}
	}
	snapshot.WriteString("## Environment\n\n")
	snapshot.WriteString(tool.environmentInfo(ctx))
	return snapshot.String(), nil
}

func (tool *SnapshotTool) buildFileTree(root string, maximumDepth int) string {
	ignored := map[string]bool{
		"node_modules": true, ".git": true, "__pycache__": true,
		"vendor": true, "logs": true, ".idea": true, ".vscode": true,
		"bak": true, "oldversion": true, "memories": true, "storage": true,
		".claude": true,
	}
	var result strings.Builder
	var walk func(string, int, string)
	walk = func(path string, depth int, prefix string) {
		if depth > maximumDepth {
			return
		}
		entries, err := os.ReadDir(path)
		if err != nil {
			return
		}
		filtered := make([]os.DirEntry, 0, len(entries))
		for _, entry := range entries {
			if ignored[entry.Name()] || strings.HasPrefix(entry.Name(), ".") {
				continue
			}
			filtered = append(filtered, entry)
		}
		if len(filtered) > 50 {
			filtered = filtered[:50]
		}
		for index, entry := range filtered {
			connector := "├── "
			childPrefix := prefix + "│   "
			if index == len(filtered)-1 {
				connector = "└── "
				childPrefix = prefix + "    "
			}
			size := ""
			if info, infoErr := entry.Info(); infoErr == nil && !entry.IsDir() {
				size = fmt.Sprintf(" (%s)", FormatSnapshotSize(info.Size()))
			}
			result.WriteString(fmt.Sprintf("%s%s%s%s\n", prefix, connector, entry.Name(), size))
			if entry.IsDir() && depth < maximumDepth {
				walk(filepath.Join(path, entry.Name()), depth+1, childPrefix)
			}
		}
	}
	result.WriteString(fmt.Sprintf("%s/\n", filepath.Base(root)))
	walk(root, 0, "")
	return result.String()
}

func (tool *SnapshotTool) gitInfo(ctx context.Context, path string) string {
	if info, err := os.Stat(filepath.Join(path, ".git")); err != nil || !info.IsDir() {
		return ""
	}
	var result strings.Builder
	if output, err := snapshotCommand(ctx, 5*time.Second, "git", "-C", path, "branch", "--show-current"); err == nil {
		result.WriteString(fmt.Sprintf("Branch: %s", strings.TrimSpace(output)))
	}
	if output, err := snapshotCommand(ctx, 5*time.Second, "git", "-C", path, "status", "--short"); err == nil && strings.TrimSpace(output) != "" {
		lines := strings.Split(strings.TrimSpace(output), "\n")
		if len(lines) > 20 {
			result.WriteString(fmt.Sprintf("Changes (%d files):\n%s\n... and more", len(lines), strings.Join(lines[:20], "\n")))
		} else {
			result.WriteString(fmt.Sprintf("Changes:\n%s", output))
		}
	} else {
		result.WriteString("Working tree clean")
	}
	if output, err := snapshotCommand(ctx, 5*time.Second, "git", "-C", path, "log", "--oneline", "-3"); err == nil && strings.TrimSpace(output) != "" {
		result.WriteString(fmt.Sprintf("\n\nRecent commits:\n%s", strings.TrimSpace(output)))
	}
	return result.String()
}

func (tool *SnapshotTool) environmentInfo(ctx context.Context) string {
	var result strings.Builder
	if executable, err := exec.LookPath("go"); err == nil {
		if output, commandErr := snapshotCommand(ctx, 5*time.Second, executable, "version"); commandErr == nil {
			result.WriteString(fmt.Sprintf("- Go: %s\n", strings.TrimSpace(output)))
		}
	}
	if executable, err := exec.LookPath("git"); err == nil {
		if output, commandErr := snapshotCommand(ctx, 5*time.Second, executable, "--version"); commandErr == nil {
			result.WriteString(fmt.Sprintf("- %s\n", strings.TrimSpace(output)))
		}
	}
	for _, name := range []string{"python3", "python"} {
		if executable, err := exec.LookPath(name); err == nil {
			if output, commandErr := snapshotCommand(ctx, 5*time.Second, executable, "--version"); commandErr == nil && strings.TrimSpace(output) != "" {
				result.WriteString(fmt.Sprintf("- %s: %s\n", name, strings.TrimSpace(output)))
			}
			break
		}
	}
	if runtime.GOOS == "windows" {
		gitBashPath := ""
		if tool.gitBashResolver != nil {
			gitBashPath = strings.TrimSpace(tool.gitBashResolver())
		} else if executable, err := exec.LookPath("bash"); err == nil {
			gitBashPath = executable
		}
		if gitBashPath != "" {
			result.WriteString(fmt.Sprintf("- Git Bash: %s\n", gitBashPath))
		} else {
			result.WriteString("- Shell: PowerShell (Git Bash not found)\n")
		}
	} else {
		result.WriteString("- Shell: POSIX (bash/sh)\n")
	}
	return result.String()
}

func snapshotCommand(ctx context.Context, timeout time.Duration, name string, arguments ...string) (string, error) {
	commandContext, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	command := exec.CommandContext(commandContext, name, arguments...)
	output, err := command.CombinedOutput()
	return string(output), err
}

func FormatSnapshotSize(size int64) string {
	switch {
	case size < 1024:
		return fmt.Sprintf("%dB", size)
	case size < 1024*1024:
		return fmt.Sprintf("%.1fKB", float64(size)/1024)
	default:
		return fmt.Sprintf("%.1fMB", float64(size)/(1024*1024))
	}
}
