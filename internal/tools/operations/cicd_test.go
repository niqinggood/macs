package operations

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCICDToolAnalyzeUsesStableCategoryOrder(t *testing.T) {
	tool := NewCICDTool(t.TempDir())

	report, err := tool.Execute(context.Background(), map[string]interface{}{
		"action":      "analyze",
		"log_content": "permission denied\nmain.go:12: undefined: missingSymbol",
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	buildIndex := strings.Index(report, "| build_error | 1 |")
	permissionIndex := strings.Index(report, "| permission | 1 |")
	if buildIndex < 0 || permissionIndex < 0 {
		t.Fatalf("report does not include expected categories:\n%s", report)
	}
	if buildIndex > permissionIndex {
		t.Fatalf("category table is not sorted:\n%s", report)
	}
	if !strings.Contains(report, "`main.go:12`") {
		t.Fatalf("report does not include source location:\n%s", report)
	}
}

func TestCICDToolAnalyzeResolvesRelativeLogAgainstWorkspace(t *testing.T) {
	workspace := t.TempDir()
	logDirectory := filepath.Join(workspace, "artifacts")
	if err := os.MkdirAll(logDirectory, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(logDirectory, "ci.log"), []byte("##[error]compile failed"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	report, err := NewCICDTool(workspace).Execute(context.Background(), map[string]interface{}{
		"action":   "analyze",
		"log_file": filepath.Join("artifacts", "ci.log"),
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !strings.Contains(report, "compile failed") {
		t.Fatalf("report does not contain relative log contents:\n%s", report)
	}
}

func TestCICDToolWorkflowAudit(t *testing.T) {
	workspace := t.TempDir()
	workflowDirectory := filepath.Join(workspace, ".github", "workflows")
	if err := os.MkdirAll(workflowDirectory, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	workflow := "name: CI\njobs:\n  test:\n    steps:\n      - uses: example/action@main\n"
	if err := os.WriteFile(filepath.Join(workflowDirectory, "ci.yml"), []byte(workflow), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	report, err := NewCICDTool(workspace).Execute(context.Background(), map[string]interface{}{
		"action": "workflow",
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	for _, expected := range []string{"No caching configured", "No timeout set", "Missing `actions/checkout`", "floating action versions"} {
		if !strings.Contains(report, expected) {
			t.Fatalf("report does not contain %q:\n%s", expected, report)
		}
	}
}

func TestParseJSONLogSkipsNonJSONLines(t *testing.T) {
	entries := parseJSONLog("plain text\n{\"timestamp\":\"now\",\"level\":\"error\",\"message\":\"failed\"}\n")
	if len(entries) != 1 || entries[0].Message != "failed" {
		t.Fatalf("parseJSONLog() = %#v", entries)
	}
}
