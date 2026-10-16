package coding

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"mac/internal/projectctx"
	"mac/internal/textutil"
	workspacetools "mac/internal/tools/workspace"
)

type CodeChangedFile struct {
	Path   string `json:"path"`
	Status string `json:"status"`
}

type CodeChangeSet struct {
	Turn       int               `json:"turn"`
	Files      []CodeChangedFile `json:"files"`
	CapturedAt time.Time         `json:"captured_at"`
}

type CodeVerificationCheck struct {
	Name     string        `json:"name"`
	Command  string        `json:"command"`
	Status   string        `json:"status"`
	Output   string        `json:"output,omitempty"`
	Duration time.Duration `json:"duration"`
}

type CodeVerificationReceipt struct {
	ID              string                  `json:"id"`
	Turn            int                     `json:"turn"`
	Trigger         string                  `json:"trigger"`
	Status          string                  `json:"status"`
	ChangedFiles    []string                `json:"changed_files,omitempty"`
	Checks          []CodeVerificationCheck `json:"checks,omitempty"`
	StartedAt       time.Time               `json:"started_at"`
	FinishedAt      time.Time               `json:"finished_at"`
	RepairAttempted bool                    `json:"repair_attempted,omitempty"`
	RepairSucceeded bool                    `json:"repair_succeeded,omitempty"`
	RepairError     string                  `json:"repair_error,omitempty"`
}

const (
	CodeVerificationAuto   = "auto"
	CodeVerificationTest   = "test"
	CodeVerificationVerify = "verify"
	CodeVerificationFull   = "full"

	CodeCheckPassed  = "passed"
	CodeCheckFailed  = "failed"
	CodeCheckError   = "error"
	CodeCheckSkipped = "skipped"
)

type codeWorkspaceEntry struct {
	Status      string
	Fingerprint string
}

type codeWorkspaceState map[string]codeWorkspaceEntry

type codeVerificationSpec struct {
	Name         string
	Dir          string
	Executable   string
	Args         []string
	FailOnOutput bool
}

func CaptureCodeWorkspaceState(workspace string) codeWorkspaceState {
	state := make(codeWorkspaceState)
	workspace = projectctx.NormalizeInstructionPath(workspace)
	cmd := exec.Command("git", "status", "--porcelain=v1", "-z", "--untracked-files=all")
	cmd.Dir = workspace
	output, err := cmd.Output()
	if err != nil {
		return state
	}

	parts := strings.Split(string(output), "\x00")
	for index := 0; index < len(parts); index++ {
		entry := parts[index]
		if len(entry) < 4 {
			continue
		}
		status := entry[:2]
		path := entry[3:]
		addCodeWorkspaceEntry(state, workspace, path, status)

		// In porcelain -z format rename/copy records have a second path token.
		if status[0] == 'R' || status[0] == 'C' || status[1] == 'R' || status[1] == 'C' {
			if index+1 < len(parts) && parts[index+1] != "" {
				index++
				addCodeWorkspaceEntry(state, workspace, parts[index], "D ")
			}
		}
	}
	return state
}

func addCodeWorkspaceEntry(state codeWorkspaceState, workspace, path, status string) {
	path = normalizeCodeRelativePath(path)
	if path == "" || isOpenKittyInternalPath(path) {
		return
	}
	state[path] = codeWorkspaceEntry{
		Status:      strings.TrimSpace(status),
		Fingerprint: codeFileFingerprint(filepath.Join(workspace, filepath.FromSlash(path))),
	}
}

func codeFileFingerprint(path string) string {
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "missing"
		}
		return "error:" + err.Error()
	}
	if info.Mode()&os.ModeSymlink != 0 {
		target, _ := os.Readlink(path)
		return fmt.Sprintf("symlink:%s:%s", target, info.ModTime().UTC().Format(time.RFC3339Nano))
	}
	if !info.Mode().IsRegular() {
		return fmt.Sprintf("mode:%s:%d:%s", info.Mode(), info.Size(), info.ModTime().UTC().Format(time.RFC3339Nano))
	}
	// Avoid loading very large generated assets just to detect a coding turn.
	// Size and nanosecond mtime still detect practical changes above this cap.
	if info.Size() > 32*1024*1024 {
		return fmt.Sprintf("large:%d:%s", info.Size(), info.ModTime().UTC().Format(time.RFC3339Nano))
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Sprintf("read-error:%d:%s", info.Size(), info.ModTime().UTC().Format(time.RFC3339Nano))
	}
	sum := sha256.Sum256(data)
	return fmt.Sprintf("%x", sum[:])
}

func DetectCodeChangeSet(
	workspace string,
	humanTurn int,
	snapshotTurn int,
	before codeWorkspaceState,
	after codeWorkspaceState,
	snapshots []*workspacetools.FileSnapshot,
) CodeChangeSet {
	statusByPath := make(map[string]string)
	for path, afterEntry := range after {
		if beforeEntry, ok := before[path]; !ok || beforeEntry != afterEntry {
			status := afterEntry.Status
			if status == "" {
				status = "M"
			}
			statusByPath[path] = status
		}
	}
	for path := range before {
		if _, ok := after[path]; !ok {
			statusByPath[path] = "D"
		}
	}

	workspace = projectctx.NormalizeInstructionPath(workspace)
	for _, snapshot := range snapshots {
		if snapshot == nil || snapshot.Restored || snapshot.Turn != snapshotTurn || !workspacetools.SnapshotChanged(snapshot) {
			continue
		}
		path := snapshot.OriginalPath
		if relative, err := filepath.Rel(workspace, path); err == nil && !strings.HasPrefix(relative, "..") {
			path = relative
		}
		path = normalizeCodeRelativePath(path)
		if path == "" || isOpenKittyInternalPath(path) {
			continue
		}
		if _, exists := statusByPath[path]; !exists {
			if snapshot.Existed {
				statusByPath[path] = "M"
			} else {
				statusByPath[path] = "A"
			}
		}
	}

	paths := make([]string, 0, len(statusByPath))
	for path := range statusByPath {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	files := make([]CodeChangedFile, 0, len(paths))
	for _, path := range paths {
		files = append(files, CodeChangedFile{Path: path, Status: statusByPath[path]})
	}
	return CodeChangeSet{Turn: humanTurn, Files: files, CapturedAt: time.Now()}
}

func CurrentCodeChangeSet(workspace string, turn int, requestedPath string) CodeChangeSet {
	if strings.TrimSpace(requestedPath) != "" {
		path := normalizeCodeRelativePath(requestedPath)
		return CodeChangeSet{
			Turn:       turn,
			Files:      []CodeChangedFile{{Path: path, Status: "M"}},
			CapturedAt: time.Now(),
		}
	}
	state := CaptureCodeWorkspaceState(workspace)
	paths := make([]string, 0, len(state))
	for path := range state {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	files := make([]CodeChangedFile, 0, len(paths))
	for _, path := range paths {
		files = append(files, CodeChangedFile{Path: path, Status: state[path].Status})
	}
	return CodeChangeSet{Turn: turn, Files: files, CapturedAt: time.Now()}
}

func normalizeCodeRelativePath(path string) string {
	path = strings.TrimSpace(path)
	path = strings.Trim(path, `"`)
	path = filepath.ToSlash(filepath.Clean(filepath.FromSlash(path)))
	if path == "." {
		return ""
	}
	return path
}

func isOpenKittyInternalPath(path string) bool {
	path = strings.TrimPrefix(filepath.ToSlash(path), "./")
	return path == ".openkitty" || strings.HasPrefix(path, ".openkitty/") ||
		path == ".git" || strings.HasPrefix(path, ".git/")
}

func CodeChangePaths(changeSet CodeChangeSet) []string {
	paths := make([]string, 0, len(changeSet.Files))
	for _, file := range changeSet.Files {
		if file.Path != "" {
			paths = append(paths, file.Path)
		}
	}
	return paths
}

func planCodeVerification(workspace string, changeSet CodeChangeSet, mode string) []codeVerificationSpec {
	workspace = projectctx.NormalizeInstructionPath(workspace)
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		mode = CodeVerificationVerify
	}
	full := mode == CodeVerificationFull
	includeFormatting := mode != CodeVerificationTest

	var existingGoFiles []string
	var pythonFiles []string
	var javascriptFiles []string
	goGroups := make(map[string]map[string][]string)
	goPackages := make(map[string]map[string]bool)
	goRoots := make(map[string]bool)

	for _, changed := range changeSet.Files {
		path := changed.Path
		if !filepath.IsAbs(path) {
			path = filepath.Join(workspace, filepath.FromSlash(path))
		}
		path = filepath.Clean(path)
		if info, err := os.Stat(path); err != nil || !info.Mode().IsRegular() {
			continue
		}
		switch strings.ToLower(filepath.Ext(path)) {
		case ".go":
			existingGoFiles = append(existingGoFiles, path)
			root, ok := nearestGoModuleRoot(workspace, path)
			if !ok {
				continue
			}
			goRoots[root] = true
			pkg := goPackageArgument(root, filepath.Dir(path))
			if goPackages[root] == nil {
				goPackages[root] = make(map[string]bool)
			}
			goPackages[root][pkg] = true
			names := relatedGoTestNames(path)
			if len(names) > 0 {
				if goGroups[root] == nil {
					goGroups[root] = make(map[string][]string)
				}
				goGroups[root][pkg] = append(goGroups[root][pkg], names...)
			}
		case ".py":
			pythonFiles = append(pythonFiles, path)
		case ".js", ".mjs", ".cjs":
			javascriptFiles = append(javascriptFiles, path)
		}
	}

	if full {
		if info, err := os.Stat(filepath.Join(workspace, "go.mod")); err == nil && info.Mode().IsRegular() {
			goRoots[workspace] = true
		}
	}

	var specs []codeVerificationSpec
	sort.Strings(existingGoFiles)
	if includeFormatting && len(existingGoFiles) > 0 {
		specs = append(specs, codeVerificationSpec{
			Name:         "Go formatting",
			Dir:          workspace,
			Executable:   "gofmt",
			Args:         append([]string{"-d"}, existingGoFiles...),
			FailOnOutput: true,
		})
	}

	roots := sortedStringKeys(goPackages)
	for _, root := range roots {
		packages := sortedBoolKeys(goPackages[root])
		for _, pkg := range packages {
			names := uniqueSortedStrings(goGroups[root][pkg])
			if len(names) > 0 && !full {
				patterns := make([]string, 0, len(names))
				for _, name := range names {
					patterns = append(patterns, regexp.QuoteMeta(name))
				}
				specs = append(specs, codeVerificationSpec{
					Name:       fmt.Sprintf("Related Go tests (%s)", pkg),
					Dir:        root,
					Executable: "go",
					Args:       []string{"test", "-count=1", "-run", "^(" + strings.Join(patterns, "|") + ")$", pkg},
				})
				continue
			}
			if !full {
				specs = append(specs, codeVerificationSpec{
					Name:       fmt.Sprintf("Go compile (%s)", pkg),
					Dir:        root,
					Executable: "go",
					Args:       []string{"test", "-count=1", "-run", "^$", pkg},
				})
			}
		}
	}

	if full {
		for _, root := range sortedBoolKeys(goRoots) {
			specs = append(specs, codeVerificationSpec{
				Name:       "Full Go test suite",
				Dir:        root,
				Executable: "go",
				Args:       []string{"test", "-count=1", "./..."},
			})
		}
	}

	sort.Strings(pythonFiles)
	if len(pythonFiles) > 0 {
		args := append([]string{"-m", "py_compile"}, pythonFiles...)
		specs = append(specs, codeVerificationSpec{
			Name:       "Python syntax",
			Dir:        workspace,
			Executable: "python",
			Args:       args,
		})
		var tests []string
		for _, path := range pythonFiles {
			base := filepath.Base(path)
			if strings.HasPrefix(base, "test_") || strings.HasSuffix(base, "_test.py") {
				tests = append(tests, path)
			}
		}
		if len(tests) > 0 {
			specs = append(specs, codeVerificationSpec{
				Name:       "Related Python tests",
				Dir:        workspace,
				Executable: "python",
				Args:       append([]string{"-m", "pytest", "-q"}, tests...),
			})
		} else if full && pythonTestProject(workspace) {
			specs = append(specs, codeVerificationSpec{
				Name:       "Full Python test suite",
				Dir:        workspace,
				Executable: "python",
				Args:       []string{"-m", "pytest", "-q"},
			})
		}
	}

	sort.Strings(javascriptFiles)
	for _, path := range javascriptFiles {
		specs = append(specs, codeVerificationSpec{
			Name:       "JavaScript syntax: " + filepath.Base(path),
			Dir:        workspace,
			Executable: "node",
			Args:       []string{"--check", path},
		})
	}
	return specs
}

func nearestGoModuleRoot(workspace, filePath string) (string, bool) {
	workspace = filepath.Clean(workspace)
	current := filepath.Dir(filepath.Clean(filePath))
	for {
		if info, err := os.Stat(filepath.Join(current, "go.mod")); err == nil && info.Mode().IsRegular() {
			return current, true
		}
		if current == workspace {
			break
		}
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
		if relative, err := filepath.Rel(workspace, parent); err != nil || strings.HasPrefix(relative, "..") {
			break
		}
		current = parent
	}
	return "", false
}

func goPackageArgument(moduleRoot, packageDir string) string {
	relative, err := filepath.Rel(moduleRoot, packageDir)
	if err != nil || relative == "." {
		return "."
	}
	return "./" + filepath.ToSlash(relative)
}

func relatedGoTestNames(changedFile string) []string {
	testFile := changedFile
	if !strings.HasSuffix(strings.ToLower(changedFile), "_test.go") {
		testFile = strings.TrimSuffix(changedFile, filepath.Ext(changedFile)) + "_test.go"
	}
	if _, err := os.Stat(testFile); err != nil {
		return nil
	}
	parsed, err := parser.ParseFile(token.NewFileSet(), testFile, nil, 0)
	if err != nil {
		return nil
	}
	var names []string
	for _, declaration := range parsed.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Recv != nil || !strings.HasPrefix(function.Name.Name, "Test") {
			continue
		}
		names = append(names, function.Name.Name)
	}
	return uniqueSortedStrings(names)
}

func pythonTestProject(workspace string) bool {
	for _, name := range []string{"pytest.ini", "pyproject.toml", "setup.cfg", "tox.ini"} {
		if info, err := os.Stat(filepath.Join(workspace, name)); err == nil && info.Mode().IsRegular() {
			return true
		}
	}
	return false
}

func RunCodeVerification(
	ctx context.Context,
	workspace string,
	changeSet CodeChangeSet,
	trigger string,
	mode string,
) CodeVerificationReceipt {
	started := time.Now()
	receipt := CodeVerificationReceipt{
		ID:           fmt.Sprintf("verify-%d", started.UnixNano()),
		Turn:         changeSet.Turn,
		Trigger:      trigger,
		Status:       CodeCheckSkipped,
		ChangedFiles: CodeChangePaths(changeSet),
		StartedAt:    started,
	}
	specs := planCodeVerification(workspace, changeSet, mode)
	if len(specs) == 0 {
		receipt.FinishedAt = time.Now()
		return receipt
	}

	hasFailure := false
	hasError := false
	for _, spec := range specs {
		check := executeCodeVerificationSpec(ctx, spec)
		receipt.Checks = append(receipt.Checks, check)
		switch check.Status {
		case CodeCheckFailed:
			hasFailure = true
		case CodeCheckError:
			hasError = true
		}
		if ctx.Err() != nil {
			break
		}
	}
	switch {
	case hasFailure:
		receipt.Status = CodeCheckFailed
	case hasError:
		receipt.Status = CodeCheckError
	default:
		receipt.Status = CodeCheckPassed
	}
	receipt.FinishedAt = time.Now()
	return receipt
}

func executeCodeVerificationSpec(ctx context.Context, spec codeVerificationSpec) CodeVerificationCheck {
	started := time.Now()
	check := CodeVerificationCheck{
		Name:    spec.Name,
		Command: formatCodeVerificationCommand(spec),
		Status:  CodeCheckPassed,
	}
	cmd := exec.CommandContext(ctx, spec.Executable, spec.Args...)
	cmd.Dir = spec.Dir
	output, err := cmd.CombinedOutput()
	check.Duration = time.Since(started).Round(time.Millisecond)
	check.Output = strings.TrimSpace(textutil.Truncate(string(output), 12000))

	if err != nil {
		if ctx.Err() != nil {
			check.Status = CodeCheckError
			check.Output = firstNonEmptyString(check.Output, ctx.Err().Error())
			return check
		}
		var startError *exec.Error
		if errors.As(err, &startError) {
			check.Status = CodeCheckError
		} else {
			check.Status = CodeCheckFailed
		}
		if check.Output == "" {
			check.Output = err.Error()
		}
		return check
	}
	if spec.FailOnOutput && check.Output != "" {
		check.Status = CodeCheckFailed
	}
	return check
}

func formatCodeVerificationCommand(spec codeVerificationSpec) string {
	parts := []string{spec.Executable}
	for _, arg := range spec.Args {
		if strings.ContainsAny(arg, " \t\"'") {
			parts = append(parts, strconvQuote(arg))
		} else {
			parts = append(parts, arg)
		}
	}
	return strings.Join(parts, " ")
}

func strconvQuote(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `\"`) + `"`
}

func (receipt CodeVerificationReceipt) Summary(includePassingOutput bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Verification %s (%s, %d check(s), %s)",
		strings.ToUpper(receipt.Status),
		receipt.Trigger,
		len(receipt.Checks),
		receipt.FinishedAt.Sub(receipt.StartedAt).Round(time.Millisecond),
	)
	for _, check := range receipt.Checks {
		fmt.Fprintf(&b, "\n- [%s] %s (%s)", strings.ToUpper(check.Status), check.Name, check.Duration)
		if check.Command != "" {
			fmt.Fprintf(&b, "\n  `%s`", check.Command)
		}
		if check.Output != "" && (includePassingOutput || check.Status != CodeCheckPassed) {
			fmt.Fprintf(&b, "\n%s", indentCodeVerificationOutput(check.Output))
		}
	}
	if receipt.RepairAttempted {
		if receipt.RepairSucceeded {
			b.WriteString("\n- Repair: succeeded")
		} else if receipt.RepairError != "" {
			fmt.Fprintf(&b, "\n- Repair: failed (%s)", receipt.RepairError)
		} else {
			b.WriteString("\n- Repair: attempted")
		}
	}
	return b.String()
}

func indentCodeVerificationOutput(output string) string {
	lines := strings.Split(strings.TrimSpace(output), "\n")
	for index := range lines {
		lines[index] = "    " + lines[index]
	}
	return strings.Join(lines, "\n")
}

func BuildCodingRepairPrompt(
	originalRequest string,
	previousResponse string,
	changeSet CodeChangeSet,
	receipt CodeVerificationReceipt,
) string {
	var b strings.Builder
	b.WriteString("[CODING VERIFICATION FAILED]\n")
	b.WriteString("The deterministic checks for your previous implementation failed. You have one repair attempt. Inspect the exact evidence below, make the smallest necessary workspace change, and do not broaden scope.\n\n")
	b.WriteString("Original request:\n")
	b.WriteString(textutil.Truncate(strings.TrimSpace(originalRequest), 2000))
	b.WriteString("\n\nPrevious response:\n")
	b.WriteString(textutil.Truncate(strings.TrimSpace(previousResponse), 2000))
	b.WriteString("\n\nChanged files:\n")
	for _, file := range changeSet.Files {
		fmt.Fprintf(&b, "- %s (%s)\n", file.Path, file.Status)
	}
	b.WriteString("\nFailed checks:\n")
	for _, check := range receipt.Checks {
		if check.Status == CodeCheckPassed {
			continue
		}
		fmt.Fprintf(&b, "- %s\n  Command: %s\n", check.Name, check.Command)
		if check.Output != "" {
			b.WriteString(indentCodeVerificationOutput(check.Output))
			b.WriteString("\n")
		}
	}
	b.WriteString("\nFix the failure now. Return a concise final answer describing the repaired result and evidence. Do not merely explain the failure.")
	return b.String()
}

func sortedStringKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func sortedBoolKeys(values map[string]bool) []string {
	return sortedStringKeys(values)
}

func uniqueSortedStrings(values []string) []string {
	seen := make(map[string]bool, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}
