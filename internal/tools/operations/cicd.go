package operations

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"mac/internal/configvalue"
	"mac/internal/textutil"
)

// CICDTool analyzes CI/CD logs from GitHub Actions, Jenkins, GitLab CI,
// and other platforms. Extracts failures, root causes, and suggests fixes.
type CICDTool struct {
	workspace string
}

func NewCICDTool(workspace string) *CICDTool {
	return &CICDTool{workspace: workspace}
}

func (t *CICDTool) Name() string { return "ci_analyze" }

func (t *CICDTool) Description() string {
	return "Analyze CI/CD logs for failures. " +
		"Actions: analyze (parse log file and extract failures with root causes), " +
		"status (check current CI status from local git), " +
		"workflow (analyze GitHub Actions workflow files for issues). " +
		"Supports GitHub Actions, Jenkins, GitLab CI, and generic CI logs."
}

func (t *CICDTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"action": map[string]interface{}{
				"type":        "string",
				"enum":        []string{"analyze", "status", "workflow"},
				"description": "analyze: parse CI log for failures. status: check current CI status. workflow: analyze workflow files.",
			},
			"log_file": map[string]interface{}{
				"type":        "string",
				"description": "Path to CI log file for analysis",
			},
			"log_content": map[string]interface{}{
				"type":        "string",
				"description": "Raw CI log content (alternative to log_file)",
			},
		},
		"required": []string{"action"},
	}
}

func (t *CICDTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	action := configvalue.String(params, "action", "analyze")
	logFile := configvalue.String(params, "log_file", "")
	logContent := configvalue.String(params, "log_content", "")

	switch action {
	case "analyze":
		return t.analyzeLog(logFile, logContent)
	case "status":
		return t.checkCIStatus(ctx)
	case "workflow":
		return t.analyzeWorkflows()
	default:
		return "", fmt.Errorf("unknown action: %s", action)
	}
}

// ================== Log Analysis ==================

type ciFailure struct {
	Step     string
	Error    string
	Location string // file:line or step name
	Category string
	Fix      string
}

func (t *CICDTool) analyzeLog(logFile, logContent string) (string, error) {
	var content string

	if logContent != "" {
		content = logContent
	} else if logFile != "" {
		if !filepath.IsAbs(logFile) {
			logFile = filepath.Join(t.workspace, logFile)
		}
		data, err := os.ReadFile(logFile)
		if err != nil {
			return "", fmt.Errorf("read log file: %w", err)
		}
		content = string(data)
	} else {
		return "Provide 'log_file' or 'log_content' to analyze.", nil
	}

	failures := t.extractFailures(content)

	var sb strings.Builder
	sb.WriteString("## CI/CD Log Analysis\n\n")

	if len(failures) == 0 {
		sb.WriteString("✅ No failures detected in the log.\n")

		// Check for warnings
		warnings := t.extractWarnings(content)
		if len(warnings) > 0 {
			sb.WriteString(fmt.Sprintf("\n### Warnings (%d)\n\n", len(warnings)))
			for _, w := range warnings {
				sb.WriteString(fmt.Sprintf("- ⚠️ %s\n", w))
			}
		}
		return sb.String(), nil
	}

	// Summary
	categories := make(map[string]int)
	for _, f := range failures {
		categories[f.Category]++
	}

	sb.WriteString(fmt.Sprintf("### %d Failure(s) Found\n\n", len(failures)))
	sb.WriteString("| Category | Count |\n")
	sb.WriteString("|----------|-------|\n")
	for _, cat := range sortedCategoryNames(categories) {
		count := categories[cat]
		sb.WriteString(fmt.Sprintf("| %s | %d |\n", cat, count))
	}
	sb.WriteString("\n")

	// Detailed failures
	for i, f := range failures {
		sb.WriteString(fmt.Sprintf("#### Failure %d: %s\n\n", i+1, f.Step))
		sb.WriteString(fmt.Sprintf("**Category:** %s\n\n", f.Category))
		sb.WriteString(fmt.Sprintf("**Error:**\n```\n%s\n```\n\n", textutil.Truncate(f.Error, 500)))
		if f.Location != "" {
			sb.WriteString(fmt.Sprintf("**Location:** `%s`\n\n", f.Location))
		}
		sb.WriteString(fmt.Sprintf("**Suggested Fix:** %s\n\n", f.Fix))
	}

	// Aggregate root causes
	sb.WriteString("### Root Cause Summary\n\n")
	causeMap := make(map[string]int)
	for _, f := range failures {
		causeMap[f.Category]++
	}
	for _, cat := range sortedCategoryNames(causeMap) {
		count := causeMap[cat]
		switch cat {
		case "test_failure":
			sb.WriteString(fmt.Sprintf("- 🔴 **%d test failures** — review test output above, check for flaky tests or logic errors\n", count))
		case "build_error":
			sb.WriteString(fmt.Sprintf("- 🔴 **%d build errors** — check compiler output for missing imports, syntax errors, or type mismatches\n", count))
		case "lint_error":
			sb.WriteString(fmt.Sprintf("- 🟡 **%d lint issues** — run formatter/linter locally to fix\n", count))
		case "timeout":
			sb.WriteString(fmt.Sprintf("- ⏱️ **%d timeouts** — consider increasing timeout, optimizing slow operations, or splitting workload\n", count))
		case "dependency":
			sb.WriteString(fmt.Sprintf("- 📦 **%d dependency errors** — check version compatibility, network connectivity, or lock file corruption\n", count))
		case "permission":
			sb.WriteString(fmt.Sprintf("- 🔒 **%d permission errors** — check access tokens, secrets, or file permissions\n", count))
		default:
			sb.WriteString(fmt.Sprintf("- **%d %s errors** — investigate the error messages above\n", count, cat))
		}
	}

	return sb.String(), nil
}

func (t *CICDTool) extractFailures(content string) []ciFailure {
	var failures []ciFailure
	lines := strings.Split(content, "\n")

	// Patterns for different CI systems
	patterns := []struct {
		errorPattern *regexp.Regexp
		stepContext  string
		category     string
	}{
		// GitHub Actions
		{regexp.MustCompile(`##\[error\](.*)`), "GitHub Actions", "build_error"},
		{regexp.MustCompile(`Error: Process completed with exit code (\d+)`), "GitHub Actions", "build_error"},
		{regexp.MustCompile(`FAIL\s+.+_test\.go`), "GitHub Actions", "test_failure"},
		{regexp.MustCompile(`^\s*--- FAIL:`), "GitHub Actions", "test_failure"},

		// Generic test failures
		{regexp.MustCompile(`(?i)FAIL\s+(\S+)`), "Tests", "test_failure"},
		{regexp.MustCompile(`(?i)assertion.*failed`), "Tests", "test_failure"},
		{regexp.MustCompile(`(?i)expected.*but got`), "Tests", "test_failure"},
		{regexp.MustCompile(`panic:`), "Runtime", "test_failure"},

		// Build errors
		{regexp.MustCompile(`(?i)cannot find package`), "Build", "dependency"},
		{regexp.MustCompile(`(?i)undefined:`), "Build", "build_error"},
		{regexp.MustCompile(`(?i)syntax error`), "Build", "build_error"},
		{regexp.MustCompile(`(?i)too many arguments`), "Build", "build_error"},
		{regexp.MustCompile(`(?i)cannot use`), "Build", "build_error"},
		{regexp.MustCompile(`(?i)module.*not found`), "Build", "dependency"},

		// Lint errors
		{regexp.MustCompile(`(?i)vet:`), "Lint", "lint_error"},
		{regexp.MustCompile(`(?i)golangci-lint`), "Lint", "lint_error"},
		{regexp.MustCompile(`(?i)eslint.*error`), "Lint", "lint_error"},

		// Timeouts
		{regexp.MustCompile(`(?i)timeout|timed out|deadline exceeded`), "CI Runner", "timeout"},
		{regexp.MustCompile(`(?i)The job was canceled`), "CI Runner", "timeout"},

		// Permission
		{regexp.MustCompile(`(?i)permission denied|access denied|403.*forbidden|401.*unauthorized`), "CI Runner", "permission"},
		{regexp.MustCompile(`(?i)DENIED|credentials`), "CI Runner", "permission"},

		// Dependency errors
		{regexp.MustCompile(`(?i)npm ERR!|pip.*error|go:.*404|failed to download`), "Dependencies", "dependency"},
		{regexp.MustCompile(`(?i)connection refused|no such host|Temporary failure`), "Network", "dependency"},
	}

	currentStep := ""

	for _, line := range lines {
		// Track current step for context
		if strings.Contains(line, "Run ") || strings.Contains(line, "##[group]") {
			currentStep = strings.TrimSpace(line)
			// Clean up step name
			currentStep = strings.TrimPrefix(currentStep, "##[group]")
			currentStep = strings.TrimPrefix(currentStep, "Run ")
			if len(currentStep) > 80 {
				currentStep = currentStep[:80] + "..."
			}
		}

		for _, p := range patterns {
			if matches := p.errorPattern.FindStringSubmatch(line); matches != nil {
				errMsg := line
				if len(matches) > 1 {
					errMsg = matches[1]
				}

				// Extract file:line location
				location := ""
				if locRe := regexp.MustCompile(`(\S+\.(?:go|py|ts|js|rs|java)):(\d+)`); locRe.MatchString(line) {
					location = locRe.FindString(line)
				}

				// Build fix suggestion
				fix := t.suggestFix(p.category, errMsg)

				step := currentStep
				if step == "" {
					step = p.stepContext
				}

				// Avoid duplicates
				isDup := false
				for _, existing := range failures {
					if strings.Contains(existing.Error, textutil.Truncate(errMsg, 50)) {
						isDup = true
						break
					}
				}
				if isDup {
					continue
				}

				failures = append(failures, ciFailure{
					Step:     step,
					Error:    errMsg,
					Location: location,
					Category: p.category,
					Fix:      fix,
				})
				break
			}
		}
	}

	return failures
}

func (t *CICDTool) extractWarnings(content string) []string {
	var warnings []string
	warnPatterns := []string{
		"warning:", "warn:", "deprecated",
		"is deprecated", "will be removed",
	}

	lines := strings.Split(content, "\n")
	for _, line := range lines {
		for _, p := range warnPatterns {
			if strings.Contains(strings.ToLower(line), p) {
				warnings = append(warnings, strings.TrimSpace(line))
				break
			}
		}
	}
	return warnings
}

func (t *CICDTool) suggestFix(category, errMsg string) string {
	lower := strings.ToLower(errMsg)

	switch category {
	case "test_failure":
		if strings.Contains(lower, "assert") {
			return "Check the assertion logic — verify expected vs actual values. Run the test locally with verbose output."
		}
		if strings.Contains(lower, "nil pointer") || strings.Contains(lower, "null") {
			return "Add nil check before accessing the value. See the stack trace for the exact line."
		}
		return "Run the failing test locally with `go test -v -run TestName` to see full output. Check for race conditions with `-race`."

	case "build_error":
		if strings.Contains(lower, "undefined") {
			return "Missing import or undefined symbol. Check package imports and function/variable names. Use `lsp_definition` to find the symbol."
		}
		if strings.Contains(lower, "cannot use") {
			return "Type mismatch. Check the function signature and the types being passed. Use `lsp_hover` for type information."
		}
		if strings.Contains(lower, "syntax") {
			return "Syntax error. Check for missing brackets, commas, or incorrect Go syntax."
		}
		return "Fix the compilation error. Run `go build ./...` locally to see the exact error."

	case "lint_error":
		return "Run `go vet ./...` or the project linter locally. Auto-fix with `gofmt -w .` or `golangci-lint run --fix`."

	case "timeout":
		return "Operation timed out. Consider: 1) increase timeout in CI config, 2) optimize the slow operation, 3) add caching for dependencies, 4) split work across parallel jobs."

	case "dependency":
		if strings.Contains(lower, "not found") || strings.Contains(lower, "404") {
			return "Dependency not found. Check: 1) version exists in registry, 2) network/proxy settings, 3) go.mod or package.json is correct. Try `go mod tidy` or `npm install`."
		}
		return "Dependency error. Check network connectivity and package registry. Try clearing caches and retrying."

	case "permission":
		return "Permission error. Check: 1) CI secrets/tokens are configured, 2) service account has correct permissions, 3) files have correct permissions in the CI environment."

	default:
		return "Investigate the error message and fix the underlying issue. Run the same command locally to reproduce."
	}
}

// ================== CI Status ==================

func (t *CICDTool) checkCIStatus(ctx context.Context) (string, error) {
	var sb strings.Builder
	sb.WriteString("## CI Status Check\n\n")

	// Check for GitHub Actions workflows
	workflowDir := filepath.Join(t.workspace, ".github", "workflows")
	entries, err := os.ReadDir(workflowDir)
	if err != nil {
		sb.WriteString("No `.github/workflows/` directory found.\n\n")
	} else {
		sb.WriteString("### GitHub Actions Workflows\n\n")
		for _, e := range entries {
			if strings.HasSuffix(e.Name(), ".yml") || strings.HasSuffix(e.Name(), ".yaml") {
				data, _ := os.ReadFile(filepath.Join(workflowDir, e.Name()))
				content := string(data)

				// Extract workflow name and triggers
				triggerLine := ""
				for _, line := range strings.Split(content, "\n") {
					if strings.Contains(line, "push:") || strings.Contains(line, "pull_request:") {
						triggerLine = strings.TrimSpace(line)
						break
					}
				}
				sb.WriteString(fmt.Sprintf("- `%s` — %s\n", e.Name(), triggerLine))
			}
		}
		sb.WriteString("\n")
	}

	// Check for Jenkinsfile
	if _, err := os.Stat(filepath.Join(t.workspace, "Jenkinsfile")); err == nil {
		sb.WriteString("✅ Jenkinsfile found.\n\n")
	}

	// Check for GitLab CI
	if _, err := os.Stat(filepath.Join(t.workspace, ".gitlab-ci.yml")); err == nil {
		sb.WriteString("✅ .gitlab-ci.yml found.\n\n")
	}

	// Check git for recent CI-related commits
	sb.WriteString("### Recent CI-Related Changes\n\n")
	gitLog := runCommand(ctx, t.workspace, "git", "log", "--oneline", "--grep=ci", "-5")
	if gitLog != "" {
		sb.WriteString(fmt.Sprintf("```\n%s\n```\n", gitLog))
	} else {
		sb.WriteString("No recent CI-specific commits.\n")
	}

	return sb.String(), nil
}

// ================== Workflow Analysis ==================

func (t *CICDTool) analyzeWorkflows() (string, error) {
	workflowDir := filepath.Join(t.workspace, ".github", "workflows")
	entries, err := os.ReadDir(workflowDir)
	if err != nil {
		return "No `.github/workflows/` directory found.", nil
	}

	var sb strings.Builder
	sb.WriteString("## Workflow Analysis\n\n")

	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".yml") && !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}

		data, _ := os.ReadFile(filepath.Join(workflowDir, e.Name()))
		content := string(data)

		sb.WriteString(fmt.Sprintf("### `%s`\n\n", e.Name()))

		// Check for common issues
		issues := t.auditWorkflow(content)
		if len(issues) > 0 {
			for _, issue := range issues {
				sb.WriteString(fmt.Sprintf("- %s\n", issue))
			}
		} else {
			sb.WriteString("✅ No common issues detected.\n")
		}
		sb.WriteString("\n")
	}

	return sb.String(), nil
}

func (t *CICDTool) auditWorkflow(content string) []string {
	var issues []string

	// Check for common CI anti-patterns
	if !strings.Contains(content, "actions/cache") && !strings.Contains(content, "cache") {
		issues = append(issues, "💡 No caching configured — add `actions/cache` to speed up builds")
	}

	if !strings.Contains(content, "timeout-minutes") {
		issues = append(issues, "💡 No timeout set — add `timeout-minutes` to prevent hung jobs")
	}

	if !strings.Contains(content, "actions/checkout") {
		issues = append(issues, "⚠️ Missing `actions/checkout` — required to access repository code")
	}

	if strings.Contains(content, "secrets.GITHUB_TOKEN") {
		issues = append(issues, "✅ Using GITHUB_TOKEN for authentication")
	}

	// Check for pinned versions vs latest
	if strings.Contains(content, "@master") || strings.Contains(content, "@main") {
		issues = append(issues, "⚠️ Using floating action versions (@master) — pin to specific SHA or tag for reproducibility")
	}

	// Check for job parallelism
	if strings.Contains(content, "matrix:") {
		issues = append(issues, "✅ Matrix strategy configured for parallel testing")
	}

	return issues
}

// ================== Helpers ==================

func runCommand(ctx context.Context, dir, name string, args ...string) string {
	commandContext, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	command := exec.CommandContext(commandContext, name, args...)
	command.Dir = dir
	output, err := command.CombinedOutput()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(output))
}

func sortedCategoryNames(categories map[string]int) []string {
	names := make([]string, 0, len(categories))
	for name := range categories {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Parse JSON CI log format
type ciLogEntry struct {
	Timestamp string `json:"timestamp"`
	Level     string `json:"level"`
	Message   string `json:"message"`
}

func parseJSONLog(content string) []ciLogEntry {
	var entries []ciLogEntry
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || !strings.HasPrefix(line, "{") {
			continue
		}
		var entry ciLogEntry
		if json.Unmarshal([]byte(line), &entry) == nil {
			entries = append(entries, entry)
		}
	}
	return entries
}
