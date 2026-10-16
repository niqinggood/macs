package workspace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSnapshotToolResolvesRelativePathInsideWorkspace(t *testing.T) {
	workspace := t.TempDir()
	subdirectory := filepath.Join(workspace, "service")
	if err := os.MkdirAll(subdirectory, 0755); err != nil {
		t.Fatalf("create subdirectory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(subdirectory, "main.go"), []byte("package main\n"), 0644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	result, err := NewSnapshotTool(workspace).Execute(context.Background(), map[string]interface{}{
		"path":        "service",
		"include_git": false,
		"max_depth":   float64(1),
	})
	if err != nil {
		t.Fatalf("execute snapshot: %v", err)
	}
	if !strings.Contains(result, filepath.Clean(subdirectory)) || !strings.Contains(result, "main.go") {
		t.Fatalf("relative path was not snapshotted under workspace: %s", result)
	}
}

func TestSnapshotToolInvokesPathGuardBeforeReading(t *testing.T) {
	called := false
	guard := func(_ context.Context, workspace, requested string) error {
		called = true
		if requested != "." {
			t.Fatalf("requested path = %q, want .", requested)
		}
		return nil
	}
	_, err := NewSnapshotTool(t.TempDir(), WithSnapshotPathGuard(guard)).Execute(context.Background(), map[string]interface{}{"include_git": false})
	if err != nil {
		t.Fatalf("execute snapshot: %v", err)
	}
	if !called {
		t.Fatal("path guard was not invoked")
	}
}

func TestFormatSnapshotSize(t *testing.T) {
	if got := FormatSnapshotSize(1536); got != "1.5KB" {
		t.Fatalf("FormatSnapshotSize() = %q", got)
	}
}
