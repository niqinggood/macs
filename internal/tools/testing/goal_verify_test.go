package testingtools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGoalVerifyToolChecksFilesInsideWorkspace(t *testing.T) {
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "result.txt"), []byte("ok"), 0o600); err != nil {
		t.Fatal(err)
	}
	tool := NewGoalVerifyTool(workspace)

	result := tool.verifyFiles("result.txt")
	if result.Status != "PASS" || !strings.Contains(result.Evidence, "result.txt") {
		t.Fatalf("existing file result = %#v", result)
	}
	missing := tool.verifyFiles("missing.txt")
	if missing.Status != "FAIL" || !strings.Contains(missing.Error, "missing.txt") {
		t.Fatalf("missing file result = %#v", missing)
	}
}

func TestGoalVerifyDefaultsAndSummary(t *testing.T) {
	if got := defaultTestCmd("Go"); got != "go test ./... -count=1 -short" {
		t.Fatalf("Go test command = %q", got)
	}
	if got := defaultBuildCmd("Go"); got != "go build ./..." {
		t.Fatalf("Go build command = %q", got)
	}
	if got := defaultLintCmd("Go"); got != "go vet ./..." {
		t.Fatalf("Go lint command = %q", got)
	}
	if !hasExecutedTestEvidence("noise\nok\tmac/internal/tools/testing\t0.1s\n") {
		t.Fatal("Go test summary was not recognized")
	}
}
