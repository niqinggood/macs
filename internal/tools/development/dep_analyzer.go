package development

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"mac/internal/codeutil"
)

// DepAnalyzerTool checks project dependencies for issues.
type DepAnalyzerTool struct {
	workspace string
	timeout   int
}

func NewDepAnalyzerTool(workspace string) *DepAnalyzerTool {
	return &DepAnalyzerTool{workspace: workspace, timeout: 60}
}

func (t *DepAnalyzerTool) Name() string { return "dep_analyze" }

func (t *DepAnalyzerTool) Description() string {
	return "Analyze project dependencies: check for outdated packages, security vulnerabilities, unused imports, and suggest updates. " +
		"Supports: Go (go mod), Python (pip), Node (npm), Rust (cargo)."
}

func (t *DepAnalyzerTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"check": map[string]interface{}{
				"type":        "string",
				"description": "What to check: outdated (outdated deps), security (known vulns), unused (unused deps), all",
				"enum":        []string{"outdated", "security", "unused", "all"},
			},
		},
	}
}

func (t *DepAnalyzerTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	check := "all"
	if value, ok := params["check"].(string); ok && value != "" {
		check = value
	}

	lang := codeutil.DetectProjectLanguage(t.workspace)
	if lang == "" {
		return "No recognized project detected. Supported: Go (go.mod), Python (requirements.txt/pyproject.toml), Node (package.json), Rust (Cargo.toml)", nil
	}

	var report strings.Builder
	report.WriteString(fmt.Sprintf("## Dependency Analysis: %s project\n\n", lang))

	if check == "all" || check == "outdated" {
		report.WriteString(t.checkOutdated(lang))
	}
	if check == "all" || check == "security" {
		report.WriteString(t.checkSecurity(lang))
	}
	if check == "all" || check == "unused" {
		report.WriteString(t.checkUnused(lang))
	}

	if report.Len() == 0 {
		report.WriteString("No issues detected or check not supported for this project type.\n")
	}
	return report.String(), nil
}

func (t *DepAnalyzerTool) checkOutdated(lang string) string {
	switch lang {
	case "Go":
		out := runCmdDep(t.workspace, "go", "list", "-u", "-m", "-json", "all")
		if out == "" {
			return "### Outdated Dependencies\nNo outdated dependencies detected.\n\n"
		}
		// Parse go list output
		result := "### Outdated Dependencies\n```\n"
		lines := strings.Split(out, "\n")
		for i, line := range lines {
			if i > 100 {
				result += "... (truncated)\n"
				break
			}
			result += line + "\n"
		}
		result += "```\n\n"
		result += "Run `go get -u ./...` to update all dependencies.\n\n"
		return result

	case "Node":
		out := runCmdDep(t.workspace, "npm", "outdated", "--json")
		if out == "" || strings.Contains(out, "{}") {
			return "### Outdated Dependencies\nAll dependencies up to date.\n\n"
		}
		return "### Outdated Dependencies\n```json\n" + out + "\n```\nRun `npm update` to update.\n\n"

	case "Python":
		out := runCmdDep(t.workspace, "pip", "list", "--outdated", "--format", "columns")
		return "### Outdated Dependencies\n```\n" + out + "\n```\nRun `pip install --upgrade <package>` to update.\n\n"

	case "Rust":
		out := runCmdDep(t.workspace, "cargo", "update", "--dry-run")
		return "### Outdated Dependencies\n```\n" + out + "\n```\n\n"
	}
	return ""
}

func (t *DepAnalyzerTool) checkSecurity(lang string) string {
	switch lang {
	case "Go":
		out := runCmdDep(t.workspace, "go", "vet", "./...")
		result := "### Security & Static Analysis\n"
		if out == "" {
			result += "`go vet`: No issues found.\n\n"
		} else {
			result += "`go vet` findings:\n```\n" + truncateDependencyOutput(out, 2000) + "\n```\n\n"
		}
		return result

	case "Node":
		out := runCmdDep(t.workspace, "npm", "audit", "--json")
		if out == "" || strings.Contains(out, "\"vulnerabilities\":{}") {
			return "### Security\nNo vulnerabilities found.\n\n"
		}
		return "### Security Vulnerabilities\n```json\n" + truncateDependencyOutput(out, 2000) + "\n```\nRun `npm audit fix` to fix.\n\n"

	case "Python":
		out := runCmdDep(t.workspace, "safety", "check", "--json")
		if out != "" {
			return "### Security Vulnerabilities\n```json\n" + truncateDependencyOutput(out, 1000) + "\n```\n\n"
		}
		return ""
	}
	return ""
}

func (t *DepAnalyzerTool) checkUnused(lang string) string {
	switch lang {
	case "Go":
		out := runCmdDep(t.workspace, "go", "mod", "tidy", "-v")
		if out == "" {
			return "### Unused Dependencies\nNo unused dependencies detected.\n\n"
		}
		return "### Unused Dependencies\n```\n" + truncateDependencyOutput(out, 1000) + "\n```\n\n"
	}
	return ""
}

func runCmdDep(dir, name string, args ...string) string {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, _ := cmd.CombinedOutput()
	return string(out)
}

func truncateDependencyOutput(value string, max int) string {
	if len(value) <= max {
		return value
	}
	return value[:max] + "..."
}
