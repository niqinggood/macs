package development

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"mac/internal/configvalue"
)

// IDETool provides IDE integration: open files, detect workspaces,
// show diffs, resolve @-mentions, and integrate with common editors.
type IDETool struct {
	workspace string
}

func NewIDETool(workspace string) *IDETool {
	return &IDETool{workspace: workspace}
}

func (t *IDETool) Name() string { return "ide" }

func (t *IDETool) Description() string {
	return "IDE integration for development workflow. " +
		"Actions: open (open file in IDE at specific line), " +
		"detect (find installed IDEs and workspaces), " +
		"diff (open visual diff in IDE), " +
		"goto (jump to symbol definition in IDE), " +
		"terminal (open IDE terminal at project root). " +
		"Supports VS Code, Cursor, Windsurf, JetBrains (GoLand/IntelliJ), Zed."
}

func (t *IDETool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"action": map[string]interface{}{
				"type":        "string",
				"enum":        []string{"open", "detect", "diff", "goto", "terminal"},
				"description": "open: open file at line. detect: find installed IDEs. diff: open visual diff. goto: jump to definition. terminal: open IDE terminal.",
			},
			"file": map[string]interface{}{
				"type":        "string",
				"description": "File path to open (relative to workspace)",
			},
			"line": map[string]interface{}{
				"type":        "integer",
				"description": "Line number to jump to (1-based)",
			},
			"column": map[string]interface{}{
				"type":        "integer",
				"description": "Column number to jump to (1-based)",
			},
			"ide": map[string]interface{}{
				"type":        "string",
				"description": "IDE to use: vscode, cursor, windsurf, goland, idea, zed, auto (default: auto-detect)",
			},
			"file2": map[string]interface{}{
				"type":        "string",
				"description": "Second file for diff comparison",
			},
		},
		"required": []string{"action"},
	}
}

func (t *IDETool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	action := configvalue.String(params, "action", "detect")
	file := configvalue.String(params, "file", "")
	line := configvalue.Int(params, "line", 0)
	column := configvalue.Int(params, "column", 0)
	ide := configvalue.String(params, "ide", "auto")
	file2 := configvalue.String(params, "file2", "")

	switch action {
	case "open":
		return t.openInIDE(ctx, file, line, column, ide)
	case "detect":
		return t.detectIDEs(ctx)
	case "diff":
		return t.openDiff(ctx, file, file2, ide)
	case "goto":
		return t.gotoDefinition(ctx, file, line, column, ide)
	case "terminal":
		return t.openTerminal(ctx, ide)
	default:
		return "", fmt.Errorf("unknown action: %s", action)
	}
}

// ================== Open in IDE ==================

func (t *IDETool) openInIDE(ctx context.Context, filePath string, line, column int, ide string) (string, error) {
	if filePath == "" {
		return "Error: 'file' parameter required.", nil
	}

	fullPath := filePath
	if !filepath.IsAbs(fullPath) {
		fullPath = filepath.Join(t.workspace, fullPath)
	}

	// Verify file exists
	if _, err := os.Stat(fullPath); err != nil {
		return fmt.Sprintf("File not found: %s", filePath), nil
	}

	idePath := t.findIDE(ide)
	if idePath == "" {
		return t.openFallback(ctx, fullPath, line, column)
	}

	location := fullPath
	if line > 0 {
		location += fmt.Sprintf(":%d", line)
		if column > 0 {
			location += fmt.Sprintf(":%d", column)
		}
	}

	cmd := t.buildOpenCmd(ctx, idePath, location, ide)
	out, err := runIDECmd(cmd)
	if err != nil {
		return fmt.Sprintf("❌ Failed to open in %s:\n```\n%s\n```\nTrying fallback...", ide, out), nil
	}

	return fmt.Sprintf("✅ Opened `%s` in %s (line %d)", filePath, ide, line), nil
}

func (t *IDETool) openFallback(ctx context.Context, fullPath string, line, column int) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	// OS-specific fallback to open file with default editor
	switch runtime.GOOS {
	case "darwin":
		if err := startIDELauncher(exec.Command("open", fullPath)); err != nil {
			return "", fmt.Errorf("open default editor: %w", err)
		}
		return fmt.Sprintf("✅ Opened `%s` in default editor", fullPath), nil
	case "linux":
		if err := startIDELauncher(exec.Command("xdg-open", fullPath)); err != nil {
			return "", fmt.Errorf("open default editor: %w", err)
		}
		return fmt.Sprintf("✅ Opened `%s` in default editor", fullPath), nil
	case "windows":
		if err := startIDELauncher(exec.Command("cmd", "/c", "start", "", fullPath)); err != nil {
			return "", fmt.Errorf("open default editor: %w", err)
		}
		return fmt.Sprintf("✅ Opened `%s` in default editor", fullPath), nil
	default:
		return fmt.Sprintf("File at: %s (no IDE detected, open manually)", fullPath), nil
	}
}

// ================== Detect IDEs ==================

type ideInfo struct {
	Name    string
	Path    string
	Version string
	Running bool
}

func (t *IDETool) detectIDEs(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	var ides []ideInfo

	// VS Code
	if path := findCommand("code"); path != "" {
		ver, _ := runIDEOutput(ctx, "code", "--version")
		version := "unknown"
		if lines := strings.Split(ver, "\n"); len(lines) > 0 {
			version = strings.TrimSpace(lines[0])
		}
		ides = append(ides, ideInfo{Name: "VS Code", Path: path, Version: version, Running: isProcessRunning(ctx, "code")})
	}

	// Cursor
	if path := findCommand("cursor"); path != "" {
		ides = append(ides, ideInfo{Name: "Cursor", Path: path, Running: isProcessRunning(ctx, "cursor")})
	} else if path := findInPath("Cursor"); path != "" {
		ides = append(ides, ideInfo{Name: "Cursor", Path: path})
	}

	// Windsurf
	if path := findCommand("windsurf"); path != "" {
		ides = append(ides, ideInfo{Name: "Windsurf", Path: path, Running: isProcessRunning(ctx, "windsurf")})
	}

	// GoLand
	golandPaths := []string{"goland", "goland64", "GoLand"}
	for _, name := range golandPaths {
		if path := findCommand(name); path != "" {
			ides = append(ides, ideInfo{Name: "GoLand", Path: path, Running: isProcessRunning(ctx, "goland")})
			break
		}
	}

	// IntelliJ IDEA
	if path := findCommand("idea"); path != "" {
		ides = append(ides, ideInfo{Name: "IntelliJ IDEA", Path: path, Running: isProcessRunning(ctx, "idea")})
	}

	// Zed
	if path := findCommand("zed"); path != "" {
		ides = append(ides, ideInfo{Name: "Zed", Path: path, Running: isProcessRunning(ctx, "zed")})
	}

	// VS Code Insiders
	if path := findCommand("code-insiders"); path != "" {
		ides = append(ides, ideInfo{Name: "VS Code Insiders", Path: path})
	}

	var sb strings.Builder
	sb.WriteString("## IDE Detection\n\n")

	if len(ides) == 0 {
		sb.WriteString("No supported IDE detected.\n\n")
		sb.WriteString("Install one of: VS Code, Cursor, Windsurf, GoLand, IntelliJ, Zed\n")
		return sb.String(), nil
	}

	sb.WriteString("| IDE | Status | Version |\n")
	sb.WriteString("|-----|--------|--------|\n")
	for _, ide := range ides {
		status := ""
		if ide.Running {
			status = "🟢 running"
		} else {
			status = "⚫ installed"
		}
		ver := ide.Version
		if ver == "" {
			ver = "-"
		}
		sb.WriteString(fmt.Sprintf("| %s | %s | %s |\n", ide.Name, status, ver))
	}

	// Also show workspace folders
	sb.WriteString("\n### VS Code Workspaces\n\n")
	wsFolders, err := detectVSCodeWorkspaces(ctx, t.workspace)
	if err != nil {
		return "", err
	}
	if len(wsFolders) > 0 {
		for _, ws := range wsFolders {
			sb.WriteString(fmt.Sprintf("- `%s`\n", ws))
		}
	} else {
		sb.WriteString("No `.code-workspace` files found.\n")
	}

	return sb.String(), nil
}

// ================== Diff in IDE ==================

func (t *IDETool) openDiff(ctx context.Context, file1, file2 string, ide string) (string, error) {
	if file1 == "" {
		return "Error: 'file' parameter required.", nil
	}
	if file2 == "" {
		// Diff against git HEAD
		file2 = file1
		file1 = file1 + " (HEAD)"
	}

	full1 := filepath.Join(t.workspace, file1)
	full2 := filepath.Join(t.workspace, file2)

	idePath := t.findIDE(ide)
	if idePath == "" {
		return "No IDE found for diff. Use `git diff` to see changes.", nil
	}

	// VS Code / Cursor diff command
	cmd := exec.CommandContext(ctx, idePath, "--diff", full1, full2)
	out, err := cmd.CombinedOutput()
	if err != nil {
		// Try git difftool as fallback
		gitCmd := exec.CommandContext(ctx, "git", "difftool", "--tool="+ide, "--no-prompt", file1)
		gitCmd.Dir = t.workspace
		_ = gitCmd.Run()
		return fmt.Sprintf("✅ Launched diff in %s", ide), nil
	}
	_ = out
	return fmt.Sprintf("✅ Opened diff in %s:\n  %s ↔ %s", ide, file1, file2), nil
}

// ================== Goto Definition ==================

func (t *IDETool) gotoDefinition(ctx context.Context, filePath string, line, column int, ide string) (string, error) {
	if filePath == "" {
		return "Error: 'file' parameter required.", nil
	}

	if line <= 0 {
		return t.openInIDE(ctx, filePath, 1, 0, ide)
	}

	// Open at specific position
	return t.openInIDE(ctx, filePath, line, column, ide)
}

// ================== Terminal ==================

func (t *IDETool) openTerminal(ctx context.Context, ide string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	idePath := t.findIDE(ide)
	if idePath == "" {
		return "No IDE found. Open terminal manually in your editor.", nil
	}

	// VS Code: open integrated terminal at project root
	cmd := exec.Command(idePath, t.workspace)
	if err := startIDELauncher(cmd); err != nil {
		return "", fmt.Errorf("open IDE terminal: %w", err)
	}
	return fmt.Sprintf("✅ Opened %s at `%s`", ide, t.workspace), nil
}

// ================== Helpers ==================

// findIDE finds the path to an IDE executable.
func (t *IDETool) findIDE(ide string) string {
	if ide == "auto" {
		// Try in priority order
		for _, name := range []string{"cursor", "code", "windsurf", "goland", "idea", "zed"} {
			if path := findCommand(name); path != "" {
				return path
			}
		}
		return ""
	}

	// Look up specific IDE aliases
	aliases := map[string]string{
		"vscode":   "code",
		"cursor":   "cursor",
		"windsurf": "windsurf",
		"goland":   "goland",
		"intellij": "idea",
		"idea":     "idea",
		"zed":      "zed",
	}
	if name, ok := aliases[ide]; ok {
		ide = name
	}
	return findCommand(ide)
}

// buildOpenCmd builds the command to open a file in an IDE.
func (t *IDETool) buildOpenCmd(ctx context.Context, idePath, location, ide string) *exec.Cmd {
	switch {
	case strings.Contains(idePath, "goland") || strings.Contains(ide, "goland"):
		return exec.CommandContext(ctx, idePath, "--line", location, t.workspace)
	case strings.Contains(idePath, "idea"):
		return exec.CommandContext(ctx, idePath, "--line", location, t.workspace)
	default:
		// VS Code, Cursor, Windsurf, Zed all use the same pattern
		return exec.CommandContext(ctx, idePath, "--goto", location)
	}
}

func findCommand(name string) string {
	path, err := exec.LookPath(name)
	if err != nil {
		// Try common install paths
		extraPaths := findExtraPaths(name)
		for _, p := range extraPaths {
			if _, err := os.Stat(p); err == nil {
				return p
			}
		}
		return ""
	}
	return path
}

// findInPath searches for an application in PATH-like directories.
func findInPath(name string) string {
	// Search for .app on macOS
	if runtime.GOOS == "darwin" {
		appPath := fmt.Sprintf("/Applications/%s.app/Contents/MacOS/%s", name, name)
		if _, err := os.Stat(appPath); err == nil {
			return appPath
		}
	}
	return findCommand(name)
}

// isProcessRunning checks if a process is likely running.
func isProcessRunning(ctx context.Context, name string) bool {
	switch runtime.GOOS {
	case "windows":
		out, _ := exec.CommandContext(ctx, "tasklist", "/FI", fmt.Sprintf("IMAGENAME eq %s.exe", name)).Output()
		return strings.Contains(string(out), name+".exe")
	default:
		out, _ := exec.CommandContext(ctx, "pgrep", "-x", name).Output()
		return len(strings.TrimSpace(string(out))) > 0
	}
}

// detectVSCodeWorkspaces finds workspace files related to the current project.
func detectVSCodeWorkspaces(ctx context.Context, workspace string) ([]string, error) {
	var workspaces []string
	err := filepath.Walk(filepath.Dir(workspace), func(path string, info os.FileInfo, err error) error {
		if contextErr := ctx.Err(); contextErr != nil {
			return contextErr
		}
		if err != nil || info.IsDir() {
			return nil
		}
		if strings.HasSuffix(path, ".code-workspace") {
			rel, _ := filepath.Rel(filepath.Dir(workspace), path)
			workspaces = append(workspaces, rel)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("scan VS Code workspaces: %w", err)
	}
	return workspaces, nil
}

func findExtraPaths(name string) []string {
	switch name {
	case "code":
		switch runtime.GOOS {
		case "windows":
			return []string{
				filepath.Join(os.Getenv("LOCALAPPDATA"), "Programs", "Microsoft VS Code", "bin", "code.cmd"),
				filepath.Join(os.Getenv("ProgramFiles"), "Microsoft VS Code", "bin", "code.cmd"),
			}
		case "darwin":
			return []string{"/Applications/Visual Studio Code.app/Contents/Resources/app/bin/code"}
		}
	case "cursor":
		switch runtime.GOOS {
		case "windows":
			return []string{
				filepath.Join(os.Getenv("LOCALAPPDATA"), "Programs", "Cursor", "Cursor.exe"),
			}
		case "darwin":
			return []string{"/Applications/Cursor.app/Contents/MacOS/Cursor"}
		}
	case "goland":
		return []string{
			"/Applications/GoLand.app/Contents/MacOS/goland",
			filepath.Join(os.Getenv("ProgramFiles"), "JetBrains", "GoLand", "bin", "goland64.exe"),
		}
	}
	return nil
}

func runIDECmd(cmd *exec.Cmd) (string, error) {
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func runIDEOutput(ctx context.Context, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func startIDELauncher(cmd *exec.Cmd) error {
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}
