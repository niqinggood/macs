package testingtools

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"mac/internal/processutil"
	"mac/internal/textutil"
)

var testLogTimestampRe = regexp.MustCompile(`^\d{4}/\d{2}/\d{2} \d{2}:\d{2}:\d{2} `)

// FormatPlainTestRunResult renders a test process result without project-specific
// baseline or environment metadata.
func FormatPlainTestRunResult(command, fullCmd, output string, err error, duration time.Duration) (string, error) {
	if err != nil {
		// Tests failed — parse output for structured analysis
		analysis := AnalyzeTestOutput(output, command)
		return fmt.Sprintf("## Tests Failed (%.1fs)\n%s\n\n### Command\n```\n%s\n```\n\n### Relevant Output\n```\n%s\n```",
			duration.Seconds(), analysis, fullCmd, CompactTestOutput(output, 2000)), nil
	}

	// Tests passed
	return fmt.Sprintf("## All Tests Passed (%.1fs)\n\n✅ Command: `%s`\n\n```\n%s\n```",
		duration.Seconds(), fullCmd, CompactTestOutput(output, 1000)), nil
}

func runCommand(dir, command string, timeout time.Duration) (string, error) {
	return RunCommandWithEnvironment(dir, command, timeout, nil)
}

// RunCommandWithEnvironment runs a shell command in dir with optional
// per-process environment overrides.
func RunCommandWithEnvironment(dir, command string, timeout time.Duration, environment map[string]string) (string, error) {
	return RunCommandWithContext(context.Background(), dir, command, timeout, environment)
}

// RunCommandWithContext preserves the caller's cancellation and terminates the
// process tree. The legacy wrapper remains available for callers without a context.
func RunCommandWithContext(parent context.Context, dir, command string, timeout time.Duration, environment map[string]string) (string, error) {
	if parent == nil {
		return "", fmt.Errorf("command requires a context")
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	if runtime.GOOS == "windows" {
		// PowerShell must propagate native exit codes as well as cmdlet errors.
		command = "$ErrorActionPreference = 'Stop'\n" + command + "\nif (-not $?) { exit 1 }\nif ($null -ne $LASTEXITCODE) { exit $LASTEXITCODE }\n"
	}
	cmd := processutil.ShellCommand(ctx, command)
	cmd.Dir = dir
	if len(environment) > 0 {
		cmd.Env = mergeCommandEnvironment(os.Environ(), environment)
	}
	output := &commandOutputBuffer{}
	cmd.Stdout, cmd.Stderr = output, output
	err := processutil.RunTree(cmd, true)
	result := output.String()
	if ctx.Err() != nil {
		err = ctx.Err()
	}
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			result = strings.TrimSpace(result)
			if result != "" {
				result += "\n"
			}
			result += fmt.Sprintf("Error: command timed out after %s: context deadline exceeded\n", timeout)
		}
		return result, err
	}
	return result, nil
}

// Keep the tail (where test summaries normally occur) while continuing to drain
// output, so a noisy child cannot exhaust memory or block on a full pipe.
type commandOutputBuffer struct {
	buffer  bytes.Buffer
	omitted bool
}

const commandOutputLimit = 1024 * 1024

func (b *commandOutputBuffer) Write(p []byte) (int, error) {
	n := len(p)
	if len(p) >= commandOutputLimit {
		b.buffer.Reset()
		p = p[len(p)-commandOutputLimit:]
		b.omitted = true
	} else if excess := b.Len() + len(p) - commandOutputLimit; excess > 0 {
		b.buffer.Next(excess)
		b.omitted = true
	}
	_, _ = b.buffer.Write(p)
	return n, nil
}

func (b *commandOutputBuffer) String() string {
	if b.omitted {
		return "[earlier command output omitted]\n" + b.buffer.String()
	}
	return b.buffer.String()
}

func (b *commandOutputBuffer) Len() int { return b.buffer.Len() }

func mergeCommandEnvironment(base []string, overrides map[string]string) []string {
	caseInsensitive := runtime.GOOS == "windows"
	keyFor := func(key string) string {
		if caseInsensitive {
			return strings.ToUpper(key)
		}
		return key
	}
	merged := make(map[string]string, len(base)+len(overrides))
	names := make(map[string]string, len(base)+len(overrides))
	for _, item := range base {
		key, value, ok := strings.Cut(item, "=")
		if !ok || key == "" {
			continue
		}
		canonical := keyFor(key)
		names[canonical] = key
		merged[canonical] = value
	}
	for key, value := range overrides {
		canonical := keyFor(key)
		names[canonical] = key
		merged[canonical] = value
	}
	keys := make([]string, 0, len(merged))
	for canonical := range merged {
		keys = append(keys, canonical)
	}
	sort.Strings(keys)
	result := make([]string, 0, len(keys))
	for _, canonical := range keys {
		result = append(result, names[canonical]+"="+merged[canonical])
	}
	return result
}

// AnalyzeTestOutput parses test output and returns structured failure analysis.
func AnalyzeTestOutput(output, testType string) string {
	var result strings.Builder

	// Extract failed test names
	failedTests := ExtractFailedTests(output, testType)
	if packet := buildFailurePacket(output, testType, failedTests); packet != "" {
		result.WriteString(packet)
		result.WriteString("\n")
	}

	if len(failedTests) > 0 {
		result.WriteString(fmt.Sprintf("### Failed Tests (%d)\n", len(failedTests)))
		for i, name := range failedTests {
			if i >= 10 {
				result.WriteString(fmt.Sprintf("  ... (%d more)\n", len(failedTests)-10))
				break
			}
			result.WriteString(fmt.Sprintf("- %s\n", name))
		}
		if reruns := suggestedRerunCommands(testType, failedTests); len(reruns) > 0 {
			result.WriteString("\n### Suggested Rerun\n")
			for _, rerun := range reruns {
				result.WriteString(fmt.Sprintf("- `%s`\n", rerun))
			}
		}
	}

	if categories := classifyTestFailure(output); len(categories) > 0 {
		result.WriteString("\n### Failure Type\n")
		for _, category := range categories {
			result.WriteString(fmt.Sprintf("- %s\n", category))
		}
	}

	// Extract likely file/line locations before the longer error snippets.
	locations := extractErrorLocations(output)
	if len(locations) > 0 {
		result.WriteString("\n### Likely Locations\n")
		for _, loc := range locations {
			result.WriteString(fmt.Sprintf("- %s\n", loc))
		}
		if reads := suggestedReadRanges(locations); len(reads) > 0 {
			result.WriteString("\n### Suggested Reads\n")
			for _, read := range reads {
				result.WriteString(fmt.Sprintf("- %s\n", read))
			}
		}
	}

	// Extract error messages
	errors := extractErrorMessages(output)
	if len(errors) > 0 {
		result.WriteString("\n### Error Summary\n")
		for i, e := range errors {
			if i >= 5 {
				break
			}
			result.WriteString(fmt.Sprintf("```\n%s\n```\n\n", textutil.Truncate(e, 300)))
		}
	}

	// Provide fix suggestions based on error patterns
	suggestions := suggestFixes(output, testType)
	if len(suggestions) > 0 {
		result.WriteString("\n### Suggested Fixes\n")
		for _, s := range suggestions {
			result.WriteString(fmt.Sprintf("- %s\n", s))
		}
	}

	if recovery := buildRecoveryPlan(output, testType, failedTests); recovery != "" {
		result.WriteString("\n")
		result.WriteString(recovery)
	}

	if result.Len() == 0 {
		result.WriteString("Unable to parse test output structure. Review full output above.\n")
	}

	return result.String()
}

func buildFailurePacket(output, command string, failedTests []string) string {
	categories := classifyTestFailure(output)
	locations := extractErrorLocations(output)
	reruns := suggestedRerunCommands(command, failedTests)
	if len(failedTests) == 0 && len(categories) == 0 && len(locations) == 0 && len(reruns) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("### Failure Packet\n")
	b.WriteString("- status: failed\n")
	b.WriteString(fmt.Sprintf("- command_family: %s\n", testCommandFamily(command)))
	if len(categories) > 0 {
		b.WriteString(fmt.Sprintf("- primary_failure_type: %s\n", categories[0]))
	}
	if len(failedTests) > 0 {
		b.WriteString(fmt.Sprintf("- primary_failed_test: %s\n", failedTests[0]))
	}
	if len(locations) > 0 {
		b.WriteString(fmt.Sprintf("- primary_location: %s\n", locations[0]))
	}
	if len(reruns) > 0 {
		b.WriteString(fmt.Sprintf("- focused_rerun: %s\n", reruns[0]))
	}
	return b.String()
}

func testCommandFamily(command string) string {
	lower := strings.ToLower(command)
	switch {
	case strings.Contains(lower, "go test"):
		return "go"
	case strings.Contains(lower, "pytest"):
		return "pytest"
	case strings.Contains(lower, "npm test"), strings.Contains(lower, "jest"), strings.Contains(lower, "vitest"):
		return "javascript"
	case strings.Contains(lower, "cargo test"):
		return "rust"
	default:
		return "unknown"
	}
}

func buildRecoveryPlan(output, command string, failedTests []string) string {
	locations := extractErrorLocations(output)
	readRanges := suggestedReadRanges(locations)
	reruns := suggestedRerunCommands(command, failedTests)
	suggestions := suggestFixes(output, command)

	var steps []string
	if len(readRanges) > 0 {
		steps = append(steps, "Inspect the smallest relevant code window first:")
		for _, read := range readRanges {
			steps = append(steps, "  "+read)
		}
	} else if len(failedTests) > 0 {
		steps = append(steps, fmt.Sprintf("Search for the failing test or symbol before broad reading: grep pattern=%q", failedTests[0]))
	} else {
		steps = append(steps, "Inspect the compacted output and identify the first actionable error before editing.")
	}

	if len(suggestions) > 0 {
		steps = append(steps, "Apply the smallest fix matching the failure type: "+suggestions[0])
	} else {
		steps = append(steps, "Apply the smallest code change that addresses the primary failure; avoid unrelated refactors.")
	}

	if len(reruns) > 0 {
		steps = append(steps, "Rerun the focused failing case: "+reruns[0])
	}
	if strings.TrimSpace(command) != "" {
		steps = append(steps, "After the focused case passes, rerun the original command: "+command)
	}

	var b strings.Builder
	b.WriteString("### Recovery Plan\n")
	b.WriteString("- loop: inspect -> edit -> focused rerun -> broaden\n")
	for i, step := range steps {
		b.WriteString(fmt.Sprintf("%d. %s\n", i+1, step))
	}
	return b.String()
}

func suggestedRerunCommands(command string, failedTests []string) []string {
	if len(failedTests) == 0 {
		return nil
	}
	lower := strings.ToLower(command)
	var reruns []string
	if strings.Contains(lower, "go test") && !strings.Contains(lower, " -run ") {
		for _, name := range failedTests {
			name = strings.TrimSpace(name)
			if !strings.HasPrefix(name, "Test") && !strings.HasPrefix(name, "Benchmark") && !strings.HasPrefix(name, "Example") {
				continue
			}
			reruns = append(reruns, command+" -run '^"+regexp.QuoteMeta(name)+"$'")
			break
		}
	}
	if strings.Contains(lower, "pytest") {
		for _, name := range failedTests {
			name = strings.TrimSpace(strings.TrimPrefix(name, "FAILED "))
			if idx := strings.Index(name, " "); idx > 0 {
				name = name[:idx]
			}
			if strings.Contains(name, "::") {
				reruns = append(reruns, "pytest "+name+" -q")
				break
			}
		}
	}
	if (strings.Contains(lower, "npm test") || strings.Contains(lower, "jest") || strings.Contains(lower, "vitest")) &&
		!strings.Contains(lower, " -t ") && !strings.Contains(lower, " --testnamepattern") {
		for _, name := range failedTests {
			name = normalizeJSTestName(name)
			if name == "" {
				continue
			}
			if strings.Contains(lower, "npm test") {
				reruns = append(reruns, command+" -- -t "+strconv.Quote(name))
			} else {
				reruns = append(reruns, command+" -t "+strconv.Quote(name))
			}
			break
		}
	}
	return reruns
}

func normalizeJSTestName(name string) string {
	name = strings.TrimSpace(name)
	name = strings.TrimPrefix(name, "●")
	name = strings.TrimSpace(name)
	if name == "" || strings.HasPrefix(name, "FAIL ") {
		return ""
	}
	if idx := strings.Index(name, "\n"); idx > 0 {
		name = name[:idx]
	}
	return name
}

func classifyTestFailure(output string) []string {
	lower := strings.ToLower(output)
	var categories []string
	add := func(category string) {
		for _, existing := range categories {
			if existing == category {
				return
			}
		}
		categories = append(categories, category)
	}
	if strings.Contains(lower, "undefined:") || strings.Contains(lower, "cannot use") ||
		strings.Contains(lower, "syntax error") || strings.Contains(lower, "build failed") {
		add("compile/build")
	}
	if strings.Contains(lower, "assert") || strings.Contains(lower, "expected") {
		add("assertion")
	}
	if strings.Contains(lower, "panic:") || strings.Contains(lower, "nil pointer") ||
		strings.Contains(lower, "index out of range") || strings.Contains(lower, "slice bounds") {
		add("runtime/panic")
	}
	if strings.Contains(lower, "timeout") || strings.Contains(lower, "deadline exceeded") {
		add("timeout")
	}
	if strings.Contains(lower, "permission denied") || strings.Contains(lower, "no such file") ||
		strings.Contains(lower, "no required module provides package") || strings.Contains(lower, "module not found") {
		add("environment/dependency")
	}
	return categories
}

// ExtractFailedTests returns normalized failed-test identifiers found in output.
func ExtractFailedTests(output, testType string) []string {
	var failed []string
	lines := strings.Split(output, "\n")

	for _, line := range lines {
		line = strings.TrimSpace(line)
		// Go test: "--- FAIL: TestName"
		if strings.HasPrefix(line, "--- FAIL:") {
			name := strings.TrimPrefix(line, "--- FAIL:")
			name = strings.TrimSpace(name)
			// Strip duration suffix: "TestFoo (0.01s)" -> "TestFoo"
			if idx := strings.Index(name, " ("); idx > 0 {
				name = name[:idx]
			}
			if name != "" {
				failed = append(failed, name)
			}
		}
		// pytest: "FAILED test_file.py::test_name"
		if strings.Contains(line, "FAILED ") && strings.Contains(line, "::") {
			failed = append(failed, strings.TrimSpace(line))
		}
		// npm/jest: "● TestName"
		if strings.HasPrefix(line, "FAIL ") || strings.HasPrefix(line, "● ") {
			failed = append(failed, strings.TrimSpace(line))
		}
	}
	return failed
}

func extractErrorMessages(output string) []string {
	var errors []string
	lines := strings.Split(output, "\n")
	capturing := false
	var current strings.Builder

	for _, line := range lines {
		if strings.Contains(line, "Error:") || strings.Contains(line, "error:") ||
			strings.Contains(line, "panic:") || strings.Contains(line, "FAIL:") ||
			strings.Contains(line, "AssertionError") || strings.Contains(line, "assert") {
			capturing = true
			current.Reset()
		}
		if capturing {
			current.WriteString(line)
			current.WriteString("\n")
			if strings.TrimSpace(line) == "" {
				if current.Len() > 10 {
					errors = append(errors, current.String())
				}
				capturing = false
				current.Reset()
			}
		}
	}
	if capturing && current.Len() > 10 {
		errors = append(errors, current.String())
	}
	return errors
}

func extractErrorLocations(output string) []string {
	patterns := []*regexp.Regexp{
		// Go compiler/test output: ./foo.go:12:34: undefined: X
		regexp.MustCompile(`(?m)(?:^|\s)((?:\.{1,2}[\\/])?[\w./\\-]+\.go:\d+(?::\d+)?)`),
		// TypeScript/Jest output: src/foo.ts(10,5): error TS...
		regexp.MustCompile(`(?m)((?:[\w.-]+[\\/])*[\w.-]+\.(?:ts|tsx|js|jsx)\(\d+,\d+\))`),
		// JS/TS/Vitest stack traces: src/foo.test.ts:12:7
		regexp.MustCompile(`(?m)(?:^|\s)((?:[\w.-]+[\\/])*[\w.-]+\.(?:ts|tsx|js|jsx):\d+(?::\d+)?)`),
		// Python traceback: File "app.py", line 42, in handler
		regexp.MustCompile(`(?m)File "([^"]+)", line (\d+)`),
		// Pytest and generic locations: tests/test_app.py:42: AssertionError
		regexp.MustCompile(`(?m)(?:^|\s)((?:[\w.-]+[\\/])*[\w.-]+\.(?:py|rs|java|kt|cs|cpp|c|h|hpp):\d+(?::\d+)?)`),
	}

	seen := make(map[string]bool)
	var locations []string
	for _, re := range patterns {
		matches := re.FindAllStringSubmatch(output, -1)
		for _, m := range matches {
			loc := strings.TrimSpace(m[1])
			if len(m) >= 3 && strings.HasPrefix(m[0], "File ") {
				loc = fmt.Sprintf("%s:%s", m[1], m[2])
			}
			loc = strings.Trim(loc, " \t\r\n,;")
			if loc == "" || seen[loc] {
				continue
			}
			seen[loc] = true
			locations = append(locations, loc)
			if len(locations) >= 10 {
				return locations
			}
		}
	}
	return locations
}

func suggestedReadRanges(locations []string) []string {
	seen := make(map[string]bool)
	var reads []string
	for _, loc := range locations {
		file, line, ok := parseLocationFileLine(loc)
		if !ok {
			continue
		}
		start := line - 20
		if start < 1 {
			start = 1
		}
		end := line + 20
		read := fmt.Sprintf("read_file path=%q lines_range=%q", file, fmt.Sprintf("%d-%d", start, end))
		if seen[read] {
			continue
		}
		seen[read] = true
		reads = append(reads, read)
		if len(reads) >= 5 {
			break
		}
	}
	return reads
}

func parseLocationFileLine(loc string) (string, int, bool) {
	loc = strings.TrimSpace(loc)
	patterns := []*regexp.Regexp{
		regexp.MustCompile(`^(.+?)\((\d+),\d+\)$`),
		regexp.MustCompile(`^(.+?):(\d+)(?::\d+)?$`),
	}
	for _, re := range patterns {
		m := re.FindStringSubmatch(loc)
		if len(m) != 3 {
			continue
		}
		line, err := strconv.Atoi(m[2])
		if err != nil || line <= 0 {
			continue
		}
		return m[1], line, true
	}
	return "", 0, false
}

// CompactTestOutput keeps actionable test output while filtering noisy logs.
func CompactTestOutput(output string, maxLen int) string {
	output = strings.TrimSpace(output)
	if output == "" {
		return ""
	}
	lines := strings.Split(output, "\n")
	important := make(map[int]bool)
	for i, line := range lines {
		if IsImportantTestLine(line) {
			for j := i - 2; j <= i+2; j++ {
				if j >= 0 && j < len(lines) {
					important[j] = true
				}
			}
		}
	}

	var kept []string
	omitted := 0
	if len(important) > 0 {
		for i, line := range lines {
			if important[i] && !isLowSignalLogLine(line) {
				kept = append(kept, line)
				continue
			}
			if strings.TrimSpace(line) != "" {
				omitted++
			}
		}
	} else {
		for _, line := range lines {
			if isLowSignalLogLine(line) {
				omitted++
				continue
			}
			kept = append(kept, line)
		}
	}

	if len(kept) == 0 {
		kept = tailLines(lines, 40)
		omitted = 0
	}
	result := strings.TrimSpace(strings.Join(kept, "\n"))
	if omitted > 0 {
		result = fmt.Sprintf("[filtered %d low-signal log lines]\n%s", omitted, result)
	}
	return textutil.Truncate(result, maxLen)
}

// IsImportantTestLine reports whether a line is useful for failure fingerprinting.
func IsImportantTestLine(line string) bool {
	trimmed := strings.TrimSpace(line)
	lower := strings.ToLower(trimmed)
	if strings.Contains(trimmed, "--- FAIL:") || strings.HasPrefix(trimmed, "FAIL ") ||
		strings.HasPrefix(trimmed, "FAIL\t") || strings.Contains(trimmed, "Traceback (most recent call last)") {
		return true
	}
	for _, marker := range []string{
		"error:", "panic:", "assertionerror", "expected", "undefined:", "cannot use",
		"no required module provides package", "permission denied", "deadline exceeded",
		"exit status", "build failed", "failed",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return len(extractErrorLocations(trimmed)) > 0
}

func isLowSignalLogLine(line string) bool {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return false
	}
	if strings.Contains(trimmed, "[DEBUG]") || strings.Contains(trimmed, "[INFO]") ||
		strings.Contains(trimmed, "[PLUGIN]") || strings.Contains(trimmed, "[SNAPSHOT]") ||
		strings.Contains(trimmed, "[NLP]") || strings.Contains(trimmed, "[TRANSCRIPT]") {
		return true
	}
	return testLogTimestampRe.MatchString(trimmed)
}

func tailLines(lines []string, limit int) []string {
	if len(lines) <= limit {
		return lines
	}
	return lines[len(lines)-limit:]
}

func suggestFixes(output, testType string) []string {
	var suggestions []string
	lower := strings.ToLower(output)

	if strings.Contains(lower, "undefined:") || strings.Contains(lower, "undeclared") {
		suggestions = append(suggestions, "Missing import or undefined symbol. Check imports and function/variable names.")
	}
	if strings.Contains(lower, "cannot use") || strings.Contains(lower, "type mismatch") {
		suggestions = append(suggestions, "Type mismatch. Check variable types and function signatures. Use lsp_hover for type info.")
	}
	if strings.Contains(lower, "syntax error") || strings.Contains(lower, "expected ';'") || strings.Contains(lower, "unexpected") {
		suggestions = append(suggestions, "Syntax error. Check for missing brackets, commas, or incorrect syntax.")
	}
	if strings.Contains(lower, "nil pointer") || strings.Contains(lower, "null pointer") || strings.Contains(lower, "nil map") {
		suggestions = append(suggestions, "Nil/null pointer dereference. Add nil check before accessing the value.")
	}
	if strings.Contains(lower, "index out of range") || strings.Contains(lower, "slice bounds") {
		suggestions = append(suggestions, "Array/slice index out of bounds. Check array length before accessing by index.")
	}
	if strings.Contains(lower, "assert") || strings.Contains(lower, "expected") {
		suggestions = append(suggestions, "Assertion/expectation mismatch. The actual value differs from expected. Check the logic.")
	}
	if strings.Contains(lower, "timeout") || strings.Contains(lower, "deadline exceeded") || strings.Contains(lower, "context deadline exceeded") {
		suggestions = append(suggestions, "Timed out or deadline exceeded. Check loops, blocking calls, external dependencies, and test timeouts.")
	}
	if strings.Contains(lower, "cannot find package") || strings.Contains(lower, "no such file") ||
		strings.Contains(lower, "no required module provides package") || strings.Contains(lower, "cannot find module") ||
		strings.Contains(lower, "module not found") {
		suggestions = append(suggestions, "Missing package/module or file. Check import paths, go.mod/package manifests, and file locations.")
	}
	if strings.Contains(lower, "data race") || strings.Contains(lower, "race detected") {
		suggestions = append(suggestions, "Concurrency race detected. Protect shared state with synchronization or remove unsafe parallel access.")
	}
	if strings.Contains(lower, "permission denied") || strings.Contains(lower, "access is denied") {
		suggestions = append(suggestions, "Permission or path access failure. Check file permissions, working directory, and locked files.")
	}

	return suggestions
}
