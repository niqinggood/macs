package development

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"mac/internal/configvalue"
	"mac/internal/gitutil"
)

// CodeReviewTool performs automated code review on changes.
// Claude Code borrowed: /review command
type CodeReviewTool struct {
	workspace string
}

func NewCodeReviewTool(workspace string) *CodeReviewTool {
	return &CodeReviewTool{workspace: workspace}
}

func (t *CodeReviewTool) Name() string { return "code_review" }

func (t *CodeReviewTool) Description() string {
	return "Perform automated code review on staged or committed changes. " +
		"Analyzes: diff content, common bug patterns, security issues, performance concerns, style violations, and missing tests. " +
		"Use this before committing or creating a PR to catch issues early."
}

func (t *CodeReviewTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"scope": map[string]interface{}{
				"type":        "string",
				"description": "What to review: 'staged' (git staged changes), 'unstaged' (working tree), 'branch' (vs main), 'file' (specific file)",
				"enum":        []string{"staged", "unstaged", "branch", "file"},
			},
			"file": map[string]interface{}{
				"type":        "string",
				"description": "Specific file to review (only for scope=file)",
			},
			"base_branch": map[string]interface{}{
				"type":        "string",
				"description": "Base branch for comparison (for scope=branch). Default: main",
			},
		},
		"required": []string{"scope"},
	}
}

func (t *CodeReviewTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	scope := configvalue.String(params, "scope", "staged")
	file := configvalue.String(params, "file", "")
	baseBranch := configvalue.String(params, "base_branch", "main")

	// Get the diff
	var diff string
	var changedFiles []string
	var err error

	switch scope {
	case "staged":
		diff, changedFiles, err = t.getStagedDiff()
	case "unstaged":
		diff, changedFiles, err = t.GetUnstagedDiff()
	case "branch":
		diff, changedFiles, err = t.getBranchDiff(baseBranch)
	case "file":
		if file == "" {
			return "Error: file parameter required for scope=file", nil
		}
		diff, changedFiles, err = t.getFileDiff(file)
	default:
		return fmt.Sprintf("Unknown scope: %s", scope), nil
	}

	if err != nil {
		return fmt.Sprintf("Error getting diff: %v", err), nil
	}
	if diff == "" {
		return "No changes to review. Working tree is clean.", nil
	}

	// Run the review analysis
	report := t.analyzeDiff(diff, changedFiles)
	return report, nil
}

func (t *CodeReviewTool) getStagedDiff() (string, []string, error) {
	diff := gitutil.Output(t.workspace, "diff", "--cached")
	if strings.TrimSpace(diff) == "" {
		return "", nil, nil
	}
	files := gitutil.Output(t.workspace, "diff", "--cached", "--name-only")
	return diff, parseFileList(files), nil
}

func (t *CodeReviewTool) GetUnstagedDiff() (string, []string, error) {
	diff := gitutil.Output(t.workspace, "diff")
	files := parseFileList(gitutil.Output(t.workspace, "diff", "--name-only"))
	untracked := parseFileList(gitutil.Output(t.workspace, "ls-files", "--others", "--exclude-standard"))
	for _, path := range untracked {
		synthetic, err := syntheticUntrackedDiff(t.workspace, path)
		if err != nil {
			return "", nil, err
		}
		if strings.TrimSpace(synthetic) != "" {
			if strings.TrimSpace(diff) != "" {
				diff += "\n"
			}
			diff += synthetic
			files = append(files, path)
		}
	}
	return diff, uniqueSortedReviewStrings(files), nil
}

func (t *CodeReviewTool) getBranchDiff(base string) (string, []string, error) {
	diff := gitutil.Output(t.workspace, "diff", base+"...HEAD")
	if strings.TrimSpace(diff) == "" {
		return "", nil, nil
	}
	files := gitutil.Output(t.workspace, "diff", base+"...HEAD", "--name-only")
	return diff, parseFileList(files), nil
}

func (t *CodeReviewTool) getFileDiff(filePath string) (string, []string, error) {
	diff := gitutil.Output(t.workspace, "diff", "HEAD", "--", filePath)
	if strings.TrimSpace(diff) == "" {
		diff = gitutil.Output(t.workspace, "diff", "--", filePath)
	}
	if strings.TrimSpace(diff) == "" &&
		strings.TrimSpace(gitutil.Output(t.workspace, "ls-files", "--", filePath)) == "" {
		var err error
		diff, err = syntheticUntrackedDiff(t.workspace, filePath)
		if err != nil {
			return "", nil, err
		}
	}
	return diff, []string{filePath}, nil
}

func syntheticUntrackedDiff(workspace, relativePath string) (string, error) {
	path := relativePath
	if !filepath.IsAbs(path) {
		path = filepath.Join(workspace, filepath.FromSlash(path))
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	relativePath = filepath.ToSlash(relativePath)
	if strings.ContainsRune(string(data), '\x00') {
		return fmt.Sprintf(
			"diff --git a/%s b/%s\nnew file mode 100644\nBinary files /dev/null and b/%s differ\n",
			relativePath,
			relativePath,
			relativePath,
		), nil
	}
	content := strings.ReplaceAll(string(data), "\r\n", "\n")
	lines := strings.Split(strings.TrimSuffix(content, "\n"), "\n")
	if len(lines) == 1 && lines[0] == "" {
		lines = nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "diff --git a/%s b/%s\n", relativePath, relativePath)
	b.WriteString("new file mode 100644\n")
	b.WriteString("--- /dev/null\n")
	fmt.Fprintf(&b, "+++ b/%s\n", relativePath)
	fmt.Fprintf(&b, "@@ -0,0 +1,%d @@\n", len(lines))
	for _, line := range lines {
		b.WriteString("+")
		b.WriteString(line)
		b.WriteString("\n")
	}
	return b.String(), nil
}

func parseFileList(output string) []string {
	var files []string
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			files = append(files, line)
		}
	}
	return files
}

func (t *CodeReviewTool) analyzeDiff(diff string, files []string) string {
	var report strings.Builder
	report.WriteString("## Code Review Report\n\n")

	// 1. File overview
	report.WriteString(fmt.Sprintf("### Files Changed (%d)\n", len(files)))
	for _, f := range files {
		ext := filepath.Ext(f)
		lang := lspDetectLanguage(f)
		report.WriteString(fmt.Sprintf("- `%s` (%s)\n", f, lang))
		if lang == "" && ext != "" {
			report.WriteString(fmt.Sprintf("  - Unknown language for extension: %s\n", ext))
		}
	}
	report.WriteString("\n")

	// 2. Change statistics
	additions, deletions := CountChanges(diff)
	report.WriteString(fmt.Sprintf("### Change Stats\n- Additions: +%d lines\n- Deletions: -%d lines\n- Net: %+d lines\n\n",
		additions, deletions, additions-deletions))

	// 3. Pattern-based checks
	issues := t.CheckPatterns(diff, files)
	if len(issues) > 0 {
		report.WriteString(fmt.Sprintf("### Issues Found (%d)\n", len(issues)))
		for i, issue := range issues {
			if i >= 15 {
				report.WriteString(fmt.Sprintf("\n... and %d more issues\n", len(issues)-15))
				break
			}
			icon := severityIcon(issue.Severity)
			report.WriteString(fmt.Sprintf("%s **%s** (%s:%d): %s\n", icon, issue.Category, issue.File, issue.Line, issue.Message))
		}
		report.WriteString("\n")
	} else {
		report.WriteString("### Issues\nNo common issues detected.\n\n")
	}

	// 4. Test suggestions
	testSuggestions := t.SuggestTests(files, diff)
	if len(testSuggestions) > 0 {
		report.WriteString("### Test Suggestions\n")
		for _, s := range testSuggestions {
			report.WriteString(fmt.Sprintf("- %s\n", s))
		}
		report.WriteString("\n")
	}

	// 5. Security scan summary
	securityIssues := t.SecurityScan(diff)
	if len(securityIssues) > 0 {
		report.WriteString(fmt.Sprintf("### Security Scan (%d issues)\n", len(securityIssues)))
		for _, s := range securityIssues {
			report.WriteString(fmt.Sprintf("- %s: %s\n", s.File, s.Message))
		}
		report.WriteString("\n")
	}

	// 6. Summary
	totalIssues := len(issues) + len(securityIssues)
	status := "✅ Ready"
	if totalIssues > 5 {
		status = "⚠️ Review recommended"
	}
	if totalIssues > 15 {
		status = "❌ Needs work"
	}
	report.WriteString(fmt.Sprintf("### Summary\n**%s** — %d total issues found.\n", status, totalIssues))

	return report.String()
}

type ReviewIssue struct {
	File     string
	Line     int
	Category string
	Severity string // "error", "warn", "info"
	Message  string
}

func severityIcon(s string) string {
	switch s {
	case "error":
		return "❌"
	case "warn":
		return "⚠️"
	default:
		return "ℹ️"
	}
}

func (t *CodeReviewTool) CheckPatterns(diff string, files []string) []ReviewIssue {
	var issues []ReviewIssue

	lines := strings.Split(diff, "\n")
	currentFile := ""
	lineNum := 0

	for _, line := range lines {
		// Track file and line number from diff
		if strings.HasPrefix(line, "+++ b/") {
			currentFile = strings.TrimPrefix(line, "+++ b/")
			lineNum = 0
			continue
		}
		if strings.HasPrefix(line, "@@") {
			lineNum = ParseDiffLineNumber(line)
			continue
		}
		if strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++") {
			lineNum++
			addedLine := line[1:]

			// Security: hardcoded secrets
			if MatchHardcodedSecret(addedLine) {
				issues = append(issues, ReviewIssue{
					File: currentFile, Line: lineNum,
					Category: "security", Severity: "error",
					Message: "Possible hardcoded secret (password/token/key)",
				})
			}

			// Security: SQL injection risk
			if MatchSQLInjectionRisk(addedLine) {
				issues = append(issues, ReviewIssue{
					File: currentFile, Line: lineNum,
					Category: "security", Severity: "error",
					Message: "Potential SQL injection - use parameterized queries",
				})
			}

			// Error handling: ignored errors
			if MatchIgnoredError(addedLine) {
				issues = append(issues, ReviewIssue{
					File: currentFile, Line: lineNum,
					Category: "error-handling", Severity: "warn",
					Message: "Error is ignored (assigned to _). Consider handling it.",
				})
			}

			// Performance: debug print
			if MatchDebugPrint(addedLine) {
				issues = append(issues, ReviewIssue{
					File: currentFile, Line: lineNum,
					Category: "cleanup", Severity: "warn",
					Message: "Debug print/log statement. Remove before committing.",
				})
			}

			// Go specific: bare panic
			if strings.Contains(addedLine, "panic(") {
				issues = append(issues, ReviewIssue{
					File: currentFile, Line: lineNum,
					Category: "error-handling", Severity: "warn",
					Message: "Use of panic(). Consider returning an error instead.",
				})
			}

			// Large file additions
			if strings.Contains(addedLine, "TODO") || strings.Contains(addedLine, "FIXME") || strings.Contains(addedLine, "HACK") {
				issues = append(issues, ReviewIssue{
					File: currentFile, Line: lineNum,
					Category: "cleanup", Severity: "info",
					Message: fmt.Sprintf("Contains TODO/FIXME/HACK marker: %s", strings.TrimSpace(addedLine)),
				})
			}
		}
	}

	return issues
}

func (t *CodeReviewTool) SecurityScan(diff string) []ReviewIssue {
	var issues []ReviewIssue
	lower := strings.ToLower(diff)

	if strings.Contains(lower, "exec.command") || strings.Contains(lower, "os/exec") {
		if strings.Contains(lower, "userinput") || strings.Contains(lower, "r.form") || strings.Contains(lower, "r.query") {
			issues = append(issues, ReviewIssue{
				Category: "security", Severity: "error",
				Message: "Command execution with user input detected. Ensure proper sanitization.",
			})
		}
	}
	if strings.Contains(lower, "os.chmod") && strings.Contains(lower, "0777") {
		issues = append(issues, ReviewIssue{
			Category: "security", Severity: "error",
			Message: "Setting file permissions to 0777 (world-writable). Use more restrictive permissions.",
		})
	}

	return issues
}

func (t *CodeReviewTool) SuggestTests(files []string, diff string) []string {
	var suggestions []string
	hasSourceChanges := false
	testFilesMissing := true

	for _, f := range files {
		ext := filepath.Ext(f)
		if ext == ".go" || ext == ".py" || ext == ".ts" || ext == ".rs" || ext == ".java" {
			hasSourceChanges = true
		}
		if strings.Contains(f, "_test.go") || strings.Contains(f, "test_") || strings.Contains(f, "_test.py") || strings.Contains(f, ".test.") {
			testFilesMissing = false
		}
	}

	if hasSourceChanges && testFilesMissing {
		suggestions = append(suggestions, "No test files changed. Consider adding tests for the modified code.")
	}

	// Suggest test command based on project type
	for _, f := range files {
		if filepath.Ext(f) == ".go" {
			suggestions = append(suggestions, fmt.Sprintf("Run: `go test ./...` to verify changes don't break existing tests"))
			break
		}
		if filepath.Ext(f) == ".py" {
			suggestions = append(suggestions, fmt.Sprintf("Run: `pytest` to verify changes"))
			break
		}
	}

	return suggestions
}

func CountChanges(diff string) (int, int) {
	additions, deletions := 0, 0
	for _, line := range strings.Split(diff, "\n") {
		if strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++") {
			additions++
		}
		if strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---") {
			deletions++
		}
	}
	return additions, deletions
}

func ParseDiffLineNumber(line string) int {
	// Parse @@ -old,count +new,count @@
	idx := strings.Index(line, "+")
	if idx < 0 {
		return 0
	}
	rest := line[idx+1:]
	commaIdx := strings.Index(rest, ",")
	if commaIdx < 0 {
		spaceIdx := strings.Index(rest, " ")
		if spaceIdx > 0 {
			return atoi(rest[:spaceIdx])
		}
		return atoi(rest)
	}
	return atoi(rest[:commaIdx])
}

func atoi(s string) int {
	n := 0
	for _, c := range s {
		if c >= '0' && c <= '9' {
			n = n*10 + int(c-'0')
		}
	}
	return n
}

// Pattern matchers
func MatchHardcodedSecret(line string) bool {
	lower := strings.ToLower(line)
	patterns := []string{
		"password =", "passwd =", "secret =", "api_key =", "apikey =",
		"token =", "private_key", "access_key",
	}
	for _, p := range patterns {
		if strings.Contains(lower, p) && !strings.Contains(lower, "os.getenv") && !strings.Contains(lower, "env") && !strings.Contains(lower, "config") {
			if strings.Contains(lower, "\"") || strings.Contains(lower, "'") {
				return true
			}
		}
	}
	return false
}

func MatchSQLInjectionRisk(line string) bool {
	lower := strings.ToLower(line)
	return strings.Contains(lower, "\"select") || strings.Contains(lower, "\"insert") || strings.Contains(lower, "\"update") || strings.Contains(lower, "\"delete") ||
		(strings.Contains(lower, "fmt.sprintf") && (strings.Contains(lower, "select") || strings.Contains(lower, "insert")))
}

func MatchIgnoredError(line string) bool {
	return (strings.Contains(line, "_, err") && !strings.Contains(line, "if err")) ||
		strings.Contains(line, "_ = err") ||
		(strings.Contains(line, ", _") && strings.Contains(line, ":="))
}

func MatchDebugPrint(line string) bool {
	lower := strings.ToLower(line)
	return strings.Contains(lower, "fmt.println") || strings.Contains(lower, "console.log") || strings.Contains(lower, "print(")
}

// readFileContent reads a file for review context.
func (t *CodeReviewTool) readChangedFile(filePath string) string {
	fullPath := filepath.Join(t.workspace, filePath)
	data, err := os.ReadFile(fullPath)
	if err != nil {
		return ""
	}
	return string(data)
}

func uniqueSortedReviewStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	unique := make([]string, 0, len(values))
	for _, value := range values {
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		unique = append(unique, value)
	}
	sort.Strings(unique)
	return unique
}
