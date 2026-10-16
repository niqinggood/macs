package testingtools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	osexec "os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

const openKittyTestResultPrefix = "OPENKITTY_TEST_RESULT="

type TestRunReceipt struct {
	Status               string   `json:"status"`
	Baseline             bool     `json:"baseline,omitempty"`
	BaselineMatched      bool     `json:"baseline_matched,omitempty"`
	BaselineOnly         bool     `json:"baseline_only,omitempty"`
	Command              string   `json:"command,omitempty"`
	VerificationFamily   string   `json:"verification_family,omitempty"`
	VerificationRoot     string   `json:"verification_root,omitempty"`
	VerificationTarget   string   `json:"verification_target,omitempty"`
	FailureIDs           []string `json:"failure_ids,omitempty"`
	ExistingFailureIDs   []string `json:"existing_failure_ids,omitempty"`
	NewFailureIDs        []string `json:"new_failure_ids,omitempty"`
	AffectedSelection    bool     `json:"affected_selection,omitempty"`
	RequestedCommandSame bool     `json:"requested_command_same,omitempty"`
}

type testBaselineSnapshot struct {
	FailureIDs []string
	Passed     bool
}

type testBaselineStore struct {
	mu        sync.Mutex
	snapshots map[string]testBaselineSnapshot
}

func newTestBaselineStore() *testBaselineStore {
	return &testBaselineStore{snapshots: make(map[string]testBaselineSnapshot)}
}

func (s *testBaselineStore) set(key string, snapshot testBaselineSnapshot) {
	if s == nil {
		return
	}
	s.mu.Lock()
	if s.snapshots == nil {
		s.snapshots = make(map[string]testBaselineSnapshot)
	}
	snapshot.FailureIDs = append([]string(nil), snapshot.FailureIDs...)
	s.snapshots[key] = snapshot
	s.mu.Unlock()
}

func (s *testBaselineStore) get(key string) (testBaselineSnapshot, bool) {
	if s == nil {
		return testBaselineSnapshot{}, false
	}
	s.mu.Lock()
	snapshot, ok := s.snapshots[key]
	s.mu.Unlock()
	snapshot.FailureIDs = append([]string(nil), snapshot.FailureIDs...)
	return snapshot, ok
}

func (t *TestRunnerTool) formatTestRunResult(dir, requestedCommand, executedCommand, selectionNote, output string, runErr error, duration time.Duration, captureBaseline bool) string {
	return t.formatTestRunResultWithEnvironment(
		dir, requestedCommand, executedCommand, selectionNote, output, runErr, duration, captureBaseline, "")
}

func (t *TestRunnerTool) formatTestRunResultWithEnvironment(dir, requestedCommand, executedCommand, selectionNote, output string, runErr error, duration time.Duration, captureBaseline bool, environmentID string) string {
	if t.baselines == nil {
		t.baselines = newTestBaselineStore()
	}
	failureIDs := testFailureIDs(output, executedCommand, runErr)
	baselineCommand := executedCommand
	if environmentID = strings.TrimSpace(environmentID); environmentID != "" {
		baselineCommand += "\x00native_env=" + environmentID
	}
	key := testBaselineKey(dir, baselineCommand)
	receipt := TestRunReceipt{
		Command:              executedCommand,
		FailureIDs:           failureIDs,
		AffectedSelection:    strings.TrimSpace(selectionNote) != "",
		RequestedCommandSame: normalizeTestCommand(requestedCommand) == normalizeTestCommand(executedCommand),
	}
	descriptor := TestVerificationDescriptor{}
	if t.describe != nil {
		descriptor = t.describe(executedCommand, dir)
	}
	receipt.VerificationFamily = descriptor.Family
	receipt.VerificationRoot = descriptor.Root
	receipt.VerificationTarget = descriptor.Target

	plain, _ := FormatPlainTestRunResult(executedCommand, executedCommand, output, runErr, duration)
	var body string
	if captureBaseline {
		receipt.Baseline = true
		t.baselines.set(key, testBaselineSnapshot{FailureIDs: failureIDs, Passed: runErr == nil})
		if runErr == nil {
			receipt.Status = "baseline_passed"
			body = fmt.Sprintf("## Test Baseline Captured (%.1fs)\n\nThe exact scope currently passes. Rerun the same command without `baseline` after editing.\n\n%s", duration.Seconds(), plain)
		} else {
			receipt.Status = "baseline_failed"
			body = fmt.Sprintf("## Failing Test Baseline Captured (%.1fs)\n\nRecorded %d pre-existing failure fingerprint(s). Rerun the same command without `baseline` after editing; matching failures will remain visible but will not be misclassified as new regressions.\n\n%s", duration.Seconds(), len(failureIDs), plain)
		}
	} else {
		baseline, found := t.baselines.get(key)
		receipt.BaselineMatched = found
		if runErr == nil {
			receipt.Status = "passed"
			body = plain
		} else if found {
			receipt.ExistingFailureIDs = intersectTestFailureIDs(failureIDs, baseline.FailureIDs)
			receipt.NewFailureIDs = subtractTestFailureIDs(failureIDs, baseline.FailureIDs)
			if len(receipt.NewFailureIDs) == 0 {
				receipt.Status = "baseline_only"
				receipt.BaselineOnly = true
				body = fmt.Sprintf("## No New Test Failures (%.1fs)\n\nThe command still exits non-zero, but every current failure matches the captured pre-edit baseline. Existing failures remain visible and should not be called fixed.\n\n### Existing Baseline Failures\n%s\n\n%s", duration.Seconds(), markdownTestFailureIDs(receipt.ExistingFailureIDs), plain)
			} else {
				receipt.Status = "failed"
				body = fmt.Sprintf("## New Test Regressions Detected (%.1fs)\n\n### New Since Baseline\n%s\n\n### Still Present From Baseline\n%s\n\n%s", duration.Seconds(), markdownTestFailureIDs(receipt.NewFailureIDs), markdownTestFailureIDs(receipt.ExistingFailureIDs), plain)
			}
		} else {
			receipt.Status = "failed"
			body = plain
		}
	}

	if selectionNote != "" {
		body = fmt.Sprintf("### Affected-Test Selection\n%s\n\n- requested: `%s`\n- executed: `%s`\n\n%s", selectionNote, requestedCommand, executedCommand, body)
	}
	encoded, _ := json.Marshal(receipt)
	return openKittyTestResultPrefix + string(encoded) + "\n\n" + body
}

func ParseTestRunReceipt(content string) (TestRunReceipt, bool) {
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, openKittyTestResultPrefix) {
			continue
		}
		var receipt TestRunReceipt
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, openKittyTestResultPrefix)), &receipt); err == nil && receipt.Status != "" {
			return receipt, true
		}
	}
	return TestRunReceipt{}, false
}

func testBaselineKey(dir, command string) string {
	absDir, err := filepath.Abs(dir)
	if err != nil {
		absDir = filepath.Clean(dir)
	}
	if runtime.GOOS == "windows" {
		absDir = strings.ToLower(absDir)
	}
	return filepath.ToSlash(absDir) + "|" + normalizeTestCommand(command)
}

func normalizeTestCommand(command string) string {
	return strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(command)), " "))
}

func testFailureIDs(output, command string, runErr error) []string {
	seen := make(map[string]bool)
	var ids []string
	for _, raw := range ExtractFailedTests(output, command) {
		id := normalizeFailedTestID(raw)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		ids = append(ids, id)
	}
	if len(ids) == 0 && runErr != nil {
		for _, line := range strings.Split(output, "\n") {
			line = normalizeFailureDiagnostic(line)
			if line == "" || !IsImportantTestLine(line) {
				continue
			}
			hash := sha256.Sum256([]byte(line))
			id := "diagnostic:" + hex.EncodeToString(hash[:6])
			if !seen[id] {
				seen[id] = true
				ids = append(ids, id)
			}
			if len(ids) >= 12 {
				break
			}
		}
	}
	if len(ids) == 0 && runErr != nil {
		hash := sha256.Sum256([]byte(normalizeFailureDiagnostic(output)))
		ids = append(ids, "exit:"+hex.EncodeToString(hash[:6]))
	}
	sort.Strings(ids)
	return ids
}

func normalizeFailedTestID(value string) string {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, "FAILED ") {
		value = strings.TrimSpace(strings.TrimPrefix(value, "FAILED "))
		if fields := strings.Fields(value); len(fields) > 0 {
			value = fields[0]
		}
	}
	if strings.HasPrefix(value, "FAIL ") {
		value = strings.TrimSpace(strings.TrimPrefix(value, "FAIL "))
	}
	value = strings.TrimSpace(strings.TrimPrefix(value, "●"))
	return value
}

var (
	baselineLogTimestampRe = regexp.MustCompile(`^\d{4}/\d{2}/\d{2} \d{2}:\d{2}:\d{2} `)
	baselineANSISequenceRe = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`)
	baselineDurationRe     = regexp.MustCompile(`\([0-9.]+s\)|\b[0-9.]+s\b`)
)

func normalizeFailureDiagnostic(line string) string {
	line = baselineANSISequenceRe.ReplaceAllString(line, "")
	line = baselineLogTimestampRe.ReplaceAllString(line, "")
	line = baselineDurationRe.ReplaceAllString(line, "<duration>")
	return strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(line)), " "))
}

func intersectTestFailureIDs(current, baseline []string) []string {
	baselineSet := make(map[string]bool, len(baseline))
	for _, id := range baseline {
		baselineSet[id] = true
	}
	var out []string
	for _, id := range current {
		if baselineSet[id] {
			out = append(out, id)
		}
	}
	return out
}

func subtractTestFailureIDs(current, baseline []string) []string {
	baselineSet := make(map[string]bool, len(baseline))
	for _, id := range baseline {
		baselineSet[id] = true
	}
	var out []string
	for _, id := range current {
		if !baselineSet[id] {
			out = append(out, id)
		}
	}
	return out
}

func markdownTestFailureIDs(ids []string) string {
	if len(ids) == 0 {
		return "- none"
	}
	var lines []string
	for _, id := range ids {
		lines = append(lines, "- `"+id+"`")
	}
	return strings.Join(lines, "\n")
}

func selectAffectedTestCommand(workspace, command string, changedFiles []string) (string, string) {
	files := normalizeChangedFiles(workspace, changedFiles)
	if len(files) == 0 {
		return command, "Affected selection was requested, but no changed file resolved inside the test workspace; the original scope was retained."
	}
	lower := strings.ToLower(command)
	switch {
	case strings.Contains(lower, "go test"):
		return selectAffectedGoTestCommand(workspace, command, files)
	case strings.Contains(lower, "pytest"):
		return selectAffectedPytestCommand(workspace, command, files)
	case strings.Contains(lower, "npm test"), strings.Contains(lower, "npx jest"), strings.Contains(lower, "npx vitest"), strings.Contains(lower, "vitest run"), strings.Contains(lower, "jest "):
		return selectAffectedJSTestCommand(workspace, command, files)
	default:
		return command, "This runner has no safe dependency mapping for the command; the original scope was retained."
	}
}

func normalizeChangedFiles(workspace string, files []string) []string {
	root, err := filepath.Abs(workspace)
	if err != nil {
		root = filepath.Clean(workspace)
	}
	seen := make(map[string]bool)
	var out []string
	for _, name := range files {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		full := name
		if !filepath.IsAbs(full) {
			full = filepath.Join(root, full)
		}
		full, err = filepath.Abs(full)
		if err != nil {
			continue
		}
		rel, err := filepath.Rel(root, full)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}
		key := strings.ToLower(filepath.Clean(full))
		if !seen[key] {
			seen[key] = true
			out = append(out, filepath.Clean(full))
		}
	}
	sort.Strings(out)
	return out
}

type goListPackage struct {
	ImportPath   string
	Dir          string
	Imports      []string
	TestImports  []string
	XTestImports []string
}

func selectAffectedGoTestCommand(workspace, command string, files []string) (string, string) {
	if !strings.Contains(command, "./...") || strings.Contains(strings.ToLower(command), " -run ") {
		return command, "The Go command already has an explicit scope/filter, so it was retained."
	}
	var goFiles []string
	for _, file := range files {
		base := strings.ToLower(filepath.Base(file))
		if base == "go.mod" || base == "go.sum" || base == "go.work" || base == "go.work.sum" {
			return command, "Go module/workspace metadata changed, so the full original suite was retained."
		}
		if strings.HasSuffix(base, ".go") {
			goFiles = append(goFiles, file)
		}
	}
	if len(goFiles) == 0 {
		return command, "No changed Go source file was supplied, so the original Go scope was retained."
	}
	packages := loadGoPackageGraph(workspace)
	args := affectedGoPackageArgs(workspace, goFiles, packages)
	if len(args) == 0 {
		return command, "Go package discovery could not map the changed files; the original scope was retained."
	}
	if len(args) > 12 {
		return command, fmt.Sprintf("The reverse dependency closure contains %d packages; the full original suite was retained.", len(args))
	}
	selected := strings.Replace(command, "./...", strings.Join(args, " "), 1)
	return selected, fmt.Sprintf("Selected %d changed or reverse-dependent Go package(s): %s.", len(args), strings.Join(args, ", "))
}

func loadGoPackageGraph(workspace string) []goListPackage {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := osexec.CommandContext(ctx, "go", "list", "-e", "-json", "./...")
	cmd.Dir = workspace
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil
	}
	if err := cmd.Start(); err != nil {
		return nil
	}
	decoder := json.NewDecoder(stdout)
	var packages []goListPackage
	for {
		var pkg goListPackage
		if err := decoder.Decode(&pkg); err != nil {
			if err != io.EOF {
				_ = cmd.Wait()
				return nil
			}
			break
		}
		if pkg.ImportPath != "" && pkg.Dir != "" {
			packages = append(packages, pkg)
		}
	}
	if err := cmd.Wait(); err != nil && len(packages) == 0 {
		return nil
	}
	return packages
}

func affectedGoPackageArgs(workspace string, files []string, packages []goListPackage) []string {
	root, _ := filepath.Abs(workspace)
	byDir := make(map[string]goListPackage)
	byImport := make(map[string]goListPackage)
	for _, pkg := range packages {
		absDir, _ := filepath.Abs(pkg.Dir)
		byDir[strings.ToLower(filepath.Clean(absDir))] = pkg
		byImport[pkg.ImportPath] = pkg
	}
	affected := make(map[string]bool)
	sourceChanged := make(map[string]bool)
	for _, file := range files {
		dir, _ := filepath.Abs(filepath.Dir(file))
		pkg, ok := byDir[strings.ToLower(filepath.Clean(dir))]
		if !ok {
			continue
		}
		affected[pkg.ImportPath] = true
		if !strings.HasSuffix(strings.ToLower(file), "_test.go") {
			sourceChanged[pkg.ImportPath] = true
		}
	}
	if len(affected) == 0 {
		return nil
	}
	frontier := make(map[string]bool)
	for name := range sourceChanged {
		frontier[name] = true
	}
	for len(frontier) > 0 {
		next := make(map[string]bool)
		for _, pkg := range packages {
			if affected[pkg.ImportPath] {
				continue
			}
			imports := append(append(append([]string(nil), pkg.Imports...), pkg.TestImports...), pkg.XTestImports...)
			for _, imported := range imports {
				if frontier[imported] {
					affected[pkg.ImportPath] = true
					next[pkg.ImportPath] = true
					break
				}
			}
		}
		frontier = next
	}
	var args []string
	for importPath := range affected {
		pkg, ok := byImport[importPath]
		if !ok {
			continue
		}
		rel, err := filepath.Rel(root, pkg.Dir)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}
		arg := "."
		if rel != "." {
			arg = "./" + filepath.ToSlash(rel)
		}
		args = append(args, arg)
	}
	sort.Strings(args)
	return uniqueTestStrings(args)
}

func selectAffectedPytestCommand(workspace, command string, files []string) (string, string) {
	if regexp.MustCompile(`(?i)(?:^|\s)[^\s]+\.py(?:::|\s|$)`).MatchString(command) || strings.Contains(command, " -k ") {
		return command, "The pytest command already has an explicit test path/filter, so it was retained."
	}
	candidates := relatedPythonTests(workspace, files)
	if len(candidates) == 0 {
		return command, "No safely related pytest file was found; the original scope was retained."
	}
	args := relativeTestArgs(workspace, candidates)
	return command + " " + strings.Join(args, " "), fmt.Sprintf("Selected %d changed or convention-matched pytest file(s): %s.", len(args), strings.Join(args, ", "))
}

func relatedPythonTests(workspace string, files []string) []string {
	wanted := make(map[string]bool)
	var direct []string
	for _, file := range files {
		lower := strings.ToLower(filepath.Base(file))
		if !strings.HasSuffix(lower, ".py") {
			continue
		}
		if strings.HasPrefix(lower, "test_") || strings.HasSuffix(lower, "_test.py") {
			direct = append(direct, file)
			continue
		}
		base := strings.TrimSuffix(lower, ".py")
		wanted["test_"+base+".py"] = true
		wanted[base+"_test.py"] = true
	}
	if len(wanted) == 0 {
		return uniqueTestStrings(direct)
	}
	_ = filepath.WalkDir(workspace, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if entry.IsDir() && path != workspace {
			switch strings.ToLower(entry.Name()) {
			case ".git", ".venv", "venv", "node_modules", "vendor", "reference":
				return filepath.SkipDir
			}
		}
		if !entry.IsDir() && wanted[strings.ToLower(entry.Name())] {
			direct = append(direct, path)
		}
		return nil
	})
	return uniqueTestStrings(direct)
}

func selectAffectedJSTestCommand(workspace, command string, files []string) (string, string) {
	if strings.Contains(command, " -- ") || regexp.MustCompile(`(?i)\.(?:test|spec)\.(?:js|jsx|ts|tsx)(?:\s|$)`).MatchString(command) {
		return command, "The JavaScript test command already has an explicit file scope, so it was retained."
	}
	var tests []string
	for _, file := range files {
		if regexp.MustCompile(`(?i)\.(?:test|spec)\.(?:js|jsx|ts|tsx)$`).MatchString(file) {
			tests = append(tests, file)
		}
	}
	if len(tests) == 0 {
		return command, "JavaScript source-to-test dependency mapping was not provable, so the original scope was retained."
	}
	args := relativeTestArgs(workspace, tests)
	selected := command + " " + strings.Join(args, " ")
	if strings.Contains(strings.ToLower(command), "npm test") {
		selected = command + " -- " + strings.Join(args, " ")
	}
	return selected, fmt.Sprintf("Selected %d directly changed JavaScript test file(s): %s.", len(args), strings.Join(args, ", "))
}

func relativeTestArgs(workspace string, files []string) []string {
	root, _ := filepath.Abs(workspace)
	var args []string
	for _, file := range uniqueTestStrings(files) {
		rel, err := filepath.Rel(root, file)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}
		args = append(args, quoteTestArg(filepath.ToSlash(rel)))
	}
	sort.Strings(args)
	return args
}

func quoteTestArg(value string) string {
	if regexp.MustCompile(`^[A-Za-z0-9_./:@+-]+$`).MatchString(value) {
		return value
	}
	return `"` + strings.ReplaceAll(value, `"`, `\"`) + `"`
}

func uniqueTestStrings(values []string) []string {
	seen := make(map[string]bool)
	var out []string
	for _, value := range values {
		if value != "" && !seen[value] {
			seen[value] = true
			out = append(out, value)
		}
	}
	return out
}
