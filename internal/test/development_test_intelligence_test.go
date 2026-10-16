package app

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestAffectedGoPackagesIncludeReverseDependencies(t *testing.T) {
	root := t.TempDir()
	packages := []goListPackage{
		{ImportPath: "example/core", Dir: filepath.Join(root, "core")},
		{ImportPath: "example/service", Dir: filepath.Join(root, "service"), Imports: []string{"example/core"}},
		{ImportPath: "example/app", Dir: filepath.Join(root, "app"), Imports: []string{"example/service"}},
		{ImportPath: "example/unrelated", Dir: filepath.Join(root, "unrelated")},
	}

	got := affectedGoPackageArgs(root, []string{filepath.Join(root, "core", "core.go")}, packages)
	want := []string{"./app", "./core", "./service"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("affected source packages = %v, want %v", got, want)
	}

	got = affectedGoPackageArgs(root, []string{filepath.Join(root, "core", "core_test.go")}, packages)
	want = []string{"./core"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("test-only change should not fan out to importers: got %v, want %v", got, want)
	}
}

func TestAffectedGoSelectionUsesRealPackageGraph(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"go.mod":             "module example.com/affected\n\ngo 1.22\n",
		"core/core.go":       "package core\n\nfunc Value() int { return 1 }\n",
		"service/service.go": "package service\n\nimport \"example.com/affected/core\"\n\nfunc Value() int { return core.Value() }\n",
		"other/other.go":     "package other\n\nfunc Value() int { return 2 }\n",
	}
	for name, content := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	got, note := selectAffectedTestCommand(root, "go test ./... -count=1", []string{"core/core.go"})
	for _, want := range []string{"./core", "./service"} {
		if !strings.Contains(got, want) {
			t.Fatalf("real dependency selection missing %q: command=%q note=%q", want, got, note)
		}
	}
	if strings.Contains(got, "./other") || strings.Contains(got, "./...") {
		t.Fatalf("real dependency selection retained unrelated scope: command=%q note=%q", got, note)
	}
}

func TestAffectedSelectionKeepsBroadGoScopeForModuleMetadata(t *testing.T) {
	root := t.TempDir()
	command := "go test ./... -count=1"
	got, note := selectAffectedTestCommand(root, command, []string{"go.mod"})
	if got != command || !strings.Contains(note, "full original suite") {
		t.Fatalf("module change selection = %q note=%q", got, note)
	}
}

func TestAffectedPytestSelectionUsesChangedTestFile(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "tests", "test_widget.py")
	got, note := selectAffectedTestCommand(root, "pytest -q", []string{file})
	if got != "pytest -q tests/test_widget.py" || !strings.Contains(note, "1 changed") {
		t.Fatalf("pytest affected selection = %q note=%q", got, note)
	}
}

func TestTestBaselineSeparatesExistingAndNewFailures(t *testing.T) {
	tool := NewTestRunnerTool(t.TempDir())
	command := "go test ./..."
	oldFailure := "--- FAIL: TestOld (0.01s)\nold_test.go:10: expected old\nFAIL\n"
	baseline := tool.formatTestRunResult(tool.workspace, command, command, "", oldFailure, errors.New("exit status 1"), time.Second, true)
	receipt, ok := parseTestRunReceipt(baseline)
	if !ok || !receipt.Baseline || receipt.Status != "baseline_failed" {
		t.Fatalf("baseline receipt = %#v ok=%v", receipt, ok)
	}

	unchanged := tool.formatTestRunResult(tool.workspace, command, command, "", oldFailure, errors.New("exit status 1"), time.Second, false)
	receipt, ok = parseTestRunReceipt(unchanged)
	if !ok || !receipt.BaselineMatched || !receipt.BaselineOnly || len(receipt.NewFailureIDs) != 0 {
		t.Fatalf("unchanged baseline comparison = %#v ok=%v", receipt, ok)
	}
	if !strings.Contains(unchanged, "No New Test Failures") || !strings.Contains(unchanged, "TestOld") {
		t.Fatalf("baseline-only output lost status or failure: %s", unchanged)
	}

	withNew := oldFailure + "--- FAIL: TestNew (0.01s)\nnew_test.go:20: expected new\n"
	regressed := tool.formatTestRunResult(tool.workspace, command, command, "", withNew, errors.New("exit status 1"), time.Second, false)
	receipt, ok = parseTestRunReceipt(regressed)
	if !ok || receipt.Status != "failed" || !reflect.DeepEqual(receipt.NewFailureIDs, []string{"TestNew"}) {
		t.Fatalf("new failure comparison = %#v ok=%v", receipt, ok)
	}
}

func TestVerificationGateRequiresPostEditRunAfterBaseline(t *testing.T) {
	tool := NewTestRunnerTool(t.TempDir())
	command := "go test ./..."
	failure := "--- FAIL: TestExisting (0.01s)\nexisting_test.go:9: expected existing\nFAIL\n"
	call := ToolCall{Name: "run_tests", Arguments: map[string]interface{}{"command": command, "dir": tool.workspace}}

	baselineOutput := tool.formatTestRunResult(tool.workspace, command, command, "", failure, errors.New("exit status 1"), time.Second, true)
	baselineRecord := toolResultForFinalization(call, baselineOutput, false)
	if issues := unresolvedVerificationIssues("develop the feature", []string{baselineRecord}); len(issues) != 1 {
		t.Fatalf("baseline alone must not count as post-edit verification: %v", issues)
	}

	verifyOutput := tool.formatTestRunResult(tool.workspace, command, command, "", failure, errors.New("exit status 1"), time.Second, false)
	verifyRecord := toolResultForFinalization(call, verifyOutput, false)
	if issues := unresolvedVerificationIssues("develop the feature", []string{baselineRecord, verifyRecord}); len(issues) != 0 {
		t.Fatalf("unchanged pre-existing failure remained a new-regression blocker: %v", issues)
	}

	newFailure := failure + "--- FAIL: TestIntroduced (0.01s)\nintroduced_test.go:12: expected introduced\n"
	regressedOutput := tool.formatTestRunResult(tool.workspace, command, command, "", newFailure, errors.New("exit status 1"), time.Second, false)
	regressedRecord := toolResultForFinalization(call, regressedOutput, false)
	if issues := unresolvedVerificationIssues("develop the feature", []string{baselineRecord, regressedRecord}); len(issues) != 1 {
		t.Fatalf("new failure was incorrectly excused by baseline: %v", issues)
	}
}
