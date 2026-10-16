package testingtools

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"mac/internal/codeutil"
	"mac/internal/coding"
	"mac/internal/configvalue"
	"mac/internal/portablepath"
	"mac/internal/projectctx"
)

// GoalVerifyTool runs explicit checks and reports their evidence. Passing a
// command alone cannot establish that an arbitrary natural-language goal is met.
type GoalVerifyTool struct {
	workspace string
}

func NewGoalVerifyTool(workspace string) *GoalVerifyTool {
	return &GoalVerifyTool{workspace: workspace}
}

func (t *GoalVerifyTool) Name() string { return "goal_verify" }

func (t *GoalVerifyTool) Description() string {
	return "Verify selected tests, build, lint or behavior checks with command evidence. " +
		"Returns PASS, FAIL or PARTIAL (missing evidence). Files and git status do not prove behavior. " +
		"Use behavior_cmd for executable acceptance criteria; review the evidence against the user's goal."
}

func (t *GoalVerifyTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"goal":         map[string]interface{}{"type": "string", "description": "The goal these checks are intended to verify"},
			"files":        map[string]interface{}{"type": "string", "description": "Comma-separated workspace files to check for existence (not behavior)"},
			"check":        map[string]interface{}{"type": "string", "description": "Checks to run (default: all)", "enum": []string{"tests", "build", "lint", "behavior", "all"}},
			"test_cmd":     map[string]interface{}{"type": "string", "description": "Test command that preserves failures, e.g. go test ./pkg/..."},
			"behavior_cmd": map[string]interface{}{"type": "string", "description": "Executable acceptance check; nonzero exit means failure. Required to verify behavior."},
		},
		"required": []string{"goal"},
	}
}

type verifyResult struct {
	Check    string
	Status   string // PASS, FAIL, PARTIAL or INFO; INFO never contributes to the verdict.
	Evidence string
	Error    string
	output   string
}

func (t *GoalVerifyTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	goal := strings.TrimSpace(configvalue.String(params, "goal", ""))
	check := configvalue.String(params, "check", "all")
	if goal == "" {
		return "Error: 'goal' parameter is required", nil
	}
	switch check {
	case "tests", "build", "lint", "behavior", "all":
	default:
		return "Error: invalid 'check' parameter", nil
	}
	if ctx == nil {
		return "", fmt.Errorf("verification requires a context")
	}
	// Bound the entire invocation, including git, rather than multiplying the
	// timeout by the number of checks. RunTree also cancels child processes.
	ctx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()
	lang := codeutil.DetectProjectLanguage(t.workspace)
	var results []verifyResult
	if check == "all" || check == "tests" {
		results = append(results, t.verifyTests(ctx, lang, configvalue.String(params, "test_cmd", "")))
	}
	if check == "all" || check == "build" {
		results = append(results, t.verifyCommand(ctx, "Build", defaultBuildCmd(lang)))
	}
	if check == "all" || check == "lint" {
		results = append(results, t.verifyCommand(ctx, "Lint", defaultLintCmd(lang)))
	}
	if files := configvalue.String(params, "files", ""); files != "" {
		results = append(results, t.verifyFiles(files))
	}
	if check == "all" || check == "behavior" {
		results = append(results, t.verifyCommand(ctx, "Behavior", configvalue.String(params, "behavior_cmd", "")))
	}
	results = append(results, t.verifyGitStatus(ctx))
	return formatGoalVerification(goal, results), nil
}

func formatGoalVerification(goal string, results []verifyResult) string {
	passed, failed, partial := 0, 0, 0
	for _, r := range results {
		switch r.Status {
		case "PASS":
			passed++
		case "FAIL":
			failed++
		case "PARTIAL":
			partial++
		}
	}
	verdict := "PASS"
	if partial > 0 || passed == 0 {
		verdict = "PARTIAL"
	}
	if failed > 0 {
		verdict = "FAIL"
	}
	var report strings.Builder
	fmt.Fprintf(&report, "## Goal Verification Report\n\n**Goal:** %s\n\nVERDICT: %s\n\n%d passed, %d failed, %d incomplete checks.\n\nEVIDENCE:\n", goal, verdict, passed, failed, partial)
	for _, r := range results {
		fmt.Fprintf(&report, "\n### %s: %s\n%s\n", r.Check, r.Status, r.Evidence)
		if r.Error != "" {
			fmt.Fprintf(&report, "\n```text\n%s\n```\n", r.Error)
		}
	}
	report.WriteString("\nThis verdict covers only the selected checks. Review their scope against the goal; file existence, a clean git tree, and compilation alone do not establish task completion.\n")
	return report.String()
}

func (t *GoalVerifyTool) verifyCommand(ctx context.Context, check, command string) verifyResult {
	r := verifyResult{Check: check, Status: "PARTIAL"}
	if strings.TrimSpace(command) == "" {
		r.Evidence = "No executable check configured; provide acceptance evidence."
		return r
	}
	// Reuse the completion gate's detection of commands that mask test failure.
	if coding.CommandMasksVerification(command) {
		r.Evidence = "Command masks its failure or truncates its evidence; run the check directly."
		return r
	}
	out, err := RunCommandWithContext(ctx, t.workspace, command, 120*time.Second, nil)
	r.output = out
	r.Evidence = fmt.Sprintf("Command: `%s`\n```text\n%s\n```", command, CompactTestOutput(out, 4000))
	if err != nil {
		r.Status, r.Error = "FAIL", err.Error()
	} else {
		r.Status = "PASS"
	}
	return r
}

func (t *GoalVerifyTool) verifyTests(ctx context.Context, lang, command string) verifyResult {
	if strings.TrimSpace(command) == "" {
		command = defaultTestCmd(lang)
	}
	r := t.verifyCommand(ctx, "Tests", command)
	if r.Status == "PASS" && !hasExecutedTestEvidence(r.output) {
		r.Status = "PARTIAL"
		r.Evidence += "\nThe process exited successfully, but no executed tests were evidenced. Supply a test command with a test count/summary."
	}
	return r
}

var executedTestPattern = regexp.MustCompile(`(?im)(?:\b[1-9][0-9]*\s+(?:passed|passing)\b|\bran\s+[1-9][0-9]*\s+tests?\b|^\s*#\s*pass\s+[1-9][0-9]*\b|^\s*Tests:\s+.*\b[1-9][0-9]*\s+passed\b)`)
var goExecutedTestPattern = regexp.MustCompile(`^ok\s+\S+\s+`)

func hasExecutedTestEvidence(output string) bool {
	if executedTestPattern.MatchString(output) {
		return true
	}
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if goExecutedTestPattern.MatchString(line) && !strings.Contains(line, "[no tests to run]") {
			return true
		}
	}
	return false
}

func (t *GoalVerifyTool) verifyFiles(files string) verifyResult {
	r := verifyResult{Check: "File Check", Status: "PARTIAL"}
	for _, f := range strings.Split(files, ",") {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		path, err := portablepath.SafeJoin(t.workspace, f)
		if err == nil && !projectctx.ContainsPath(t.workspace, path) {
			err = fmt.Errorf("path is outside the workspace")
		}
		if err == nil {
			err = projectctx.EnsureResolvedPathInside(t.workspace, path)
		}
		if err == nil {
			var info os.FileInfo
			info, err = os.Stat(path)
			if err == nil && !info.Mode().IsRegular() {
				err = fmt.Errorf("not a regular file")
			}
			if err == nil {
				r.Evidence += fmt.Sprintf("- `%s`: %d bytes (existence only)\n", f, info.Size())
			}
		}
		if err != nil {
			r.Error += fmt.Sprintf("%s: %v\n", f, err)
		}
	}
	if r.Error != "" {
		r.Status = "FAIL"
	} else if r.Evidence != "" {
		r.Status = "PASS"
	} else {
		r.Evidence = "No files specified."
	}
	return r
}

func (t *GoalVerifyTool) verifyGitStatus(ctx context.Context) verifyResult {
	r := verifyResult{Check: "Git Status", Status: "INFO"}
	out, err := RunCommandWithContext(ctx, t.workspace, "git status --porcelain", 5*time.Second, nil)
	if err != nil {
		r.Evidence = "Git status unavailable: " + err.Error()
	} else if strings.TrimSpace(out) == "" {
		r.Evidence = "Working tree clean."
	} else {
		r.Evidence = "Uncommitted changes (informational):\n" + CompactTestOutput(out, 1500)
	}
	return r
}

func defaultTestCmd(lang string) string {
	switch lang {
	case "Go":
		return "go test ./... -count=1 -short"
	case "Python":
		return "python -m pytest -x --tb=short"
	case "Node":
		return "npm test"
	case "Rust":
		return "cargo test"
	}
	return ""
}

func defaultBuildCmd(lang string) string {
	switch lang {
	case "Go":
		return "go build ./..."
	case "Node":
		return "npm run build"
	case "Rust":
		return "cargo build"
	}
	return ""
}

func defaultLintCmd(lang string) string {
	switch lang {
	case "Go":
		return "go vet ./..."
	case "Python":
		return "ruff check ."
	case "Node":
		return "npx --no-install eslint ."
	case "Rust":
		return "cargo clippy"
	}
	return ""
}
