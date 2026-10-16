package workspace

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// REPLTool provides quick code snippet evaluation in multiple languages.
type REPLTool struct {
	workspace string
}

func NewREPLTool(workspace string) *REPLTool {
	return &REPLTool{workspace: workspace}
}

func (t *REPLTool) Name() string { return "repl" }

func (t *REPLTool) Description() string {
	return "Evaluate code snippets quickly. " +
		"Actions: eval (evaluate expression and return result). " +
		"Supports: Python, Node, Shell, Go (via go run). " +
		"Auto-detects language from code content."
}

func (t *REPLTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"lang": map[string]interface{}{
				"type":        "string",
				"enum":        []string{"go", "python", "node", "shell", "auto"},
				"description": "Language. Default: auto-detect.",
			},
			"code": map[string]interface{}{
				"type":        "string",
				"description": "Code snippet to evaluate",
			},
			"timeout": map[string]interface{}{
				"type":        "integer",
				"description": "Timeout in seconds. Default: 10.",
			},
		},
		"required": []string{"code"},
	}
}

func (t *REPLTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	lang := stringParam(params, "lang", "auto")
	code := stringParam(params, "code", "")
	timeout := intParam(params, "timeout", 10)

	if code == "" {
		return "Error: 'code' is required", nil
	}
	if lang == "auto" {
		lang = detectCodeLang(code)
	}

	result, err := t.evalCode(lang, code, timeout)
	if err != nil {
		return fmt.Sprintf("## REPL: %s\n\n```%s\n%s\n```\n\n❌ Error:\n```\n%s\n```",
			lang, lang, code, err.Error()), nil
	}

	result = strings.TrimSpace(result)
	if len(result) > 2000 {
		result = result[:2000] + "\n...(truncated)"
	}
	if result == "" {
		result = "(no output)"
	}

	return fmt.Sprintf("## REPL: %s\n\n```%s\n%s\n```\n\n### Output\n```\n%s\n```",
		lang, lang, code, result), nil
}

func stringParam(params map[string]interface{}, key, fallback string) string {
	value, ok := params[key]
	if !ok || value == nil {
		return fallback
	}
	if text, ok := value.(string); ok {
		return text
	}
	return fmt.Sprint(value)
}

func intParam(params map[string]interface{}, key string, fallback int) int {
	value, ok := params[key]
	if !ok || value == nil {
		return fallback
	}
	switch number := value.(type) {
	case int:
		return number
	case float64:
		return int(number)
	default:
		return fallback
	}
}

func (t *REPLTool) evalCode(lang, code string, timeout int) (string, error) {
	switch lang {
	case "python":
		return t.evalPython(code, timeout)
	case "node":
		return t.evalNode(code, timeout)
	case "shell":
		return t.evalShell(code, timeout)
	case "go":
		return t.evalGo(code, timeout)
	default:
		return "", fmt.Errorf("unsupported language: %s", lang)
	}
}

func (t *REPLTool) evalPython(code string, timeout int) (string, error) {
	if !strings.Contains(code, "print(") && !strings.Contains(code, "=") && !strings.Contains(code, "import ") {
		code = fmt.Sprintf("_result = (%s)\nprint(repr(_result))", code)
	}
	return runReplCmd("python", []string{"-c", code}, t.workspace, timeout)
}

func (t *REPLTool) evalNode(code string, timeout int) (string, error) {
	if !strings.Contains(code, "console.log") && !strings.Contains(code, "=") && !strings.Contains(code, "require") {
		code = fmt.Sprintf("console.log(%s)", code)
	}
	return runReplCmd("node", []string{"-e", code}, t.workspace, timeout)
}

func (t *REPLTool) evalShell(code string, timeout int) (string, error) {
	return runReplCmd("sh", []string{"-c", code}, t.workspace, timeout)
}

func (t *REPLTool) evalGo(code string, timeout int) (string, error) {
	script := fmt.Sprintf(`package main

import "fmt"

func main() {
	%s
}`, code)

	tmpDir := t.workspace + "/.openkitty/storage"
	os.MkdirAll(tmpDir, 0755)
	tmpFile := tmpDir + "/repl_temp.go"
	os.WriteFile(tmpFile, []byte(script), 0644)
	defer os.Remove(tmpFile)

	return runReplCmd("go", []string{"run", tmpFile}, t.workspace, timeout)
}

func runReplCmd(name string, args []string, dir string, timeoutSec int) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir

	var out strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &out

	if err := cmd.Start(); err != nil {
		return "", err
	}

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	select {
	case err := <-done:
		result := out.String()
		if err != nil {
			return result, err
		}
		return result, nil
	case <-time.After(time.Duration(timeoutSec) * time.Second):
		cmd.Process.Kill()
		<-done
		return out.String(), fmt.Errorf("timed out after %ds", timeoutSec)
	}
}

func detectCodeLang(code string) string {
	code = strings.TrimSpace(code)
	switch {
	case strings.HasPrefix(code, "def "), strings.HasPrefix(code, "import "),
		strings.HasPrefix(code, "from "), strings.HasPrefix(code, "class "),
		strings.Contains(code, ":\n"):
		return "python"
	case strings.HasPrefix(code, "function "), strings.HasPrefix(code, "const "),
		strings.HasPrefix(code, "let "), strings.HasPrefix(code, "var "),
		strings.HasPrefix(code, "import {"):
		return "node"
	case strings.HasPrefix(code, "package "), strings.HasPrefix(code, "func "),
		strings.Contains(code, "fmt."):
		return "go"
	case strings.HasPrefix(code, "ls "), strings.HasPrefix(code, "cd "),
		strings.HasPrefix(code, "echo "), strings.HasPrefix(code, "cat "),
		strings.HasPrefix(code, "grep "), strings.HasPrefix(code, "find "),
		strings.HasPrefix(code, "curl "), strings.Contains(code, " | "):
		return "shell"
	default:
		return "python"
	}
}
