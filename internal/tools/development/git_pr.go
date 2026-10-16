package development

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"mac/internal/configvalue"
	"mac/internal/gitutil"
	"mac/internal/textutil"
)

// PRTool provides automated PR creation and management.
// Claude Code borrowed: GitHub integration via gh CLI
type PRTool struct {
	workspace string
	timeout   time.Duration
}

func NewPRTool(workspace string) *PRTool {
	return &PRTool{
		workspace: workspace,
		timeout:   60 * time.Second,
	}
}

func (t *PRTool) Name() string { return "git_pr" }

func (t *PRTool) Description() string {
	return "Automate git PR workflow: check status, create PR, check CI. " +
		"Supports: status (show branch/commit state), create (create PR with title+body), ci (check CI status), " +
		"diff (show branch diff against base). Uses 'gh' CLI for GitHub operations."
}

func (t *PRTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"action": map[string]interface{}{
				"type":        "string",
				"description": "Action: 'status' (show branch info), 'diff' (show changes vs base), 'create' (create PR), 'ci' (check CI status)",
				"enum":        []string{"status", "diff", "create", "ci"},
			},
			"title": map[string]interface{}{
				"type":        "string",
				"description": "PR title (required for create action). Keep under 70 characters.",
			},
			"body": map[string]interface{}{
				"type":        "string",
				"description": "PR description/body (for create action). Use markdown.",
			},
			"base": map[string]interface{}{
				"type":        "string",
				"description": "Base branch for PR. Default: main or master.",
			},
			"draft": map[string]interface{}{
				"type":        "boolean",
				"description": "Create as draft PR (for create action).",
			},
		},
		"required": []string{"action"},
	}
}

func (t *PRTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	action := configvalue.String(params, "action", "status")
	title := configvalue.String(params, "title", "")
	body := configvalue.String(params, "body", "")
	base := configvalue.String(params, "base", "")
	draft := false
	if v, ok := params["draft"].(bool); ok {
		draft = v
	}

	switch action {
	case "status":
		return t.handleStatus()
	case "diff":
		return t.handleDiff(base)
	case "create":
		if title == "" {
			return "Error: title is required for create action", nil
		}
		return t.handleCreate(title, body, base, draft)
	case "ci":
		return t.handleCI()
	default:
		return fmt.Sprintf("Unknown action: %s. Use: status, diff, create, ci", action), nil
	}
}

func (t *PRTool) handleStatus() (string, error) {
	var result strings.Builder

	// Git branch
	branch := gitutil.Output(t.workspace, "rev-parse", "--abbrev-ref", "HEAD")
	result.WriteString(fmt.Sprintf("Branch: %s\n", strings.TrimSpace(branch)))

	// Git status
	status := gitutil.Output(t.workspace, "status", "--short")
	if strings.TrimSpace(status) == "" {
		result.WriteString("Status: clean (no uncommitted changes)\n")
	} else {
		result.WriteString(fmt.Sprintf("Status:\n%s\n", status))
	}

	// Recent commits
	log := gitutil.Output(t.workspace, "log", "--oneline", "-5")
	result.WriteString(fmt.Sprintf("Recent commits:\n%s\n", log))

	// Remote info
	remote := gitutil.Output(t.workspace, "remote", "-v")
	result.WriteString(fmt.Sprintf("Remotes:\n%s\n", remote))

	// Check for existing PR
	prInfo := runCmd(t.workspace, "gh", "pr", "view", "--json", "title,state,url")
	if strings.TrimSpace(prInfo) != "" && !strings.Contains(prInfo, "no pull request") {
		result.WriteString(fmt.Sprintf("\nExisting PR:\n%s\n", prInfo))
	}

	return result.String(), nil
}

func (t *PRTool) handleDiff(base string) (string, error) {
	if base == "" {
		base = detectBaseBranch(t.workspace)
	}
	diff := gitutil.Output(t.workspace, "diff", base+"...HEAD", "--stat")
	if strings.TrimSpace(diff) == "" {
		return fmt.Sprintf("No changes vs %s", base), nil
	}
	return fmt.Sprintf("Changes vs %s:\n%s", base, diff), nil
}

func (t *PRTool) preflightCheck() []string {
	var issues []string
	status := strings.TrimSpace(gitutil.Output(t.workspace, "status", "--short"))
	if status != "" {
		issues = append(issues, "Uncommitted changes present, commit or stash first")
	}
	branch := strings.TrimSpace(gitutil.Output(t.workspace, "rev-parse", "--abbrev-ref", "HEAD"))
	if branch == "main" || branch == "master" {
		issues = append(issues, "On base branch, create a feature branch first")
	}
	return issues
}

func (t *PRTool) handleCreate(title, body, base string, draft bool) (string, error) {
	issues := t.preflightCheck()
	if len(issues) > 0 {
		return "Pre-flight checks failed: " + strings.Join(issues, "; "), nil
	}
	if base == "" {
		base = detectBaseBranch(t.workspace)
	}

	// Push current branch first
	branch := strings.TrimSpace(gitutil.Output(t.workspace, "rev-parse", "--abbrev-ref", "HEAD"))
	pushOut := gitutil.Output(t.workspace, "push", "-u", "origin", branch)
	if strings.Contains(pushOut, "error") || strings.Contains(pushOut, "fatal") {
		return fmt.Sprintf("Push failed:\n%s", pushOut), nil
	}

	// Create PR
	args := []string{"pr", "create", "--title", title, "--base", base}
	if body != "" {
		args = append(args, "--body", body)
	}
	if draft {
		args = append(args, "--draft")
	}

	output, err := runCmdWithErr(t.workspace, "gh", args...)
	if err != nil {
		if strings.Contains(output, "already exists") {
			// Get existing PR URL
			prURL := runCmd(t.workspace, "gh", "pr", "view", "--json", "url", "-q", ".url")
			return fmt.Sprintf("PR already exists: %s", strings.TrimSpace(prURL)), nil
		}
		return fmt.Sprintf("PR creation failed: %s\n%s", err.Error(), output), nil
	}

	return fmt.Sprintf("PR created successfully!\n%s", strings.TrimSpace(output)), nil
}

func (t *PRTool) handleCI() (string, error) {
	// Check gh CLI availability
	if _, err := exec.LookPath("gh"); err != nil {
		return "gh CLI not found. Install from https://cli.github.com/", nil
	}

	// Get PR checks
	checks := runCmd(t.workspace, "gh", "pr", "checks")
	if strings.TrimSpace(checks) == "" {
		return "No PR found or no checks available.", nil
	}

	return fmt.Sprintf("CI Checks:\n%s", checks), nil
}

func detectBaseBranch(dir string) string {
	for _, name := range []string{"main", "master"} {
		cmd := exec.Command("git", "rev-parse", "--verify", name)
		cmd.Dir = dir
		if cmd.Run() == nil {
			return name
		}
	}
	return "main"
}

func runCmd(dir, name string, args ...string) string {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, _ := cmd.CombinedOutput()
	return string(out)
}

func runCmdWithErr(dir, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// ==================== git_pr_review tool ====================

type PRReviewTool struct {
	workspace string
}

func NewPRReviewTool(workspace string) *PRReviewTool {
	return &PRReviewTool{workspace: workspace}
}

func (t *PRReviewTool) Name() string { return "git_pr_review" }

func (t *PRReviewTool) Description() string {
	return "Review a GitHub PR: fetch diff, list changed files, show commit history. " +
		"Use this to understand a PR before reviewing or to check your own PR before submitting."
}

func (t *PRReviewTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"pr_number": map[string]interface{}{
				"type":        "string",
				"description": "PR number or URL to review. If empty, reviews the current branch's PR.",
			},
		},
	}
}

func (t *PRReviewTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	prNumber := configvalue.String(params, "pr_number", "")

	args := []string{"pr", "view"}
	if prNumber != "" {
		args = append(args, prNumber)
	}
	args = append(args, "--json", "title,body,state,additions,deletions,files,commits,url")

	output, err := runCmdWithErr(t.workspace, "gh", args...)
	if err != nil {
		return fmt.Sprintf("Failed to fetch PR: %v\n%s\n\nTip: Run 'gh auth login' first.", err, output), nil
	}

	var prData map[string]interface{}
	if err := json.Unmarshal([]byte(output), &prData); err != nil {
		return fmt.Sprintf("Raw PR data:\n%s", output), nil
	}

	var result strings.Builder
	result.WriteString(fmt.Sprintf("## PR: %s\n", getMapString(prData, "title")))
	result.WriteString(fmt.Sprintf("State: %s | +%v/-%v | URL: %s\n\n",
		getMapString(prData, "state"), prData["additions"], prData["deletions"], getMapString(prData, "url")))
	result.WriteString(fmt.Sprintf("### Description\n%s\n\n", getMapString(prData, "body")))

	if files, ok := prData["files"].([]interface{}); ok {
		result.WriteString(fmt.Sprintf("### Changed Files (%d)\n", len(files)))
		for _, f := range files {
			if fm, ok := f.(map[string]interface{}); ok {
				result.WriteString(fmt.Sprintf("- %s (+%v/-%v)\n", getMapString(fm, "path"), fm["additions"], fm["deletions"]))
			}
		}
	}

	if commits, ok := prData["commits"].([]interface{}); ok {
		result.WriteString(fmt.Sprintf("\n### Commits (%d)\n", len(commits)))
		for i, c := range commits {
			if i >= 5 {
				result.WriteString(fmt.Sprintf("  ... (%d more)\n", len(commits)-5))
				break
			}
			if cm, ok := c.(map[string]interface{}); ok {
				result.WriteString(fmt.Sprintf("- %s\n", getMapCommitMsg(cm)))
			}
		}
	}

	return result.String(), nil
}

func getMapString(m map[string]interface{}, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

func getMapCommitMsg(m map[string]interface{}) string {
	if body, ok := m["messageBody"].(string); ok && body != "" {
		subject := getMapString(m, "messageHeadline")
		return subject + "\n  " + strings.ReplaceAll(textutil.Truncate(body, 200), "\n", "\n  ")
	}
	return getMapString(m, "messageHeadline")
}
