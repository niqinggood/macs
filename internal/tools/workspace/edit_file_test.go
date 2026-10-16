package workspace

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mac/internal/toolcontract"
)

func editTestContext() context.Context {
	return context.WithValue(context.Background(), "agent_name", "workspace-edit-test")
}

func readForEdit(t *testing.T, workspace, relativePath string) {
	t.Helper()
	reader := NewReadFileTool(workspace)
	if _, err := reader.Execute(editTestContext(), map[string]interface{}{"path": relativePath}); err != nil {
		t.Fatalf("read before edit: %v", err)
	}
}

func TestEditFileToolExactReplacement(t *testing.T) {
	workspace := t.TempDir()
	path := filepath.Join(workspace, "sample.txt")
	if err := os.WriteFile(path, []byte("alpha\nbeta\ngamma\n"), 0644); err != nil {
		t.Fatal(err)
	}
	readForEdit(t, workspace, "sample.txt")

	result, err := NewEditFileTool(workspace).Execute(editTestContext(), map[string]interface{}{
		"file_path": "sample.txt", "old_string": "beta", "new_string": "BETA",
	})
	if err != nil || !strings.Contains(result, "Replaced 1 occurrence") {
		t.Fatalf("edit result=%q err=%v", result, err)
	}
	data, readErr := os.ReadFile(path)
	if readErr != nil || string(data) != "alpha\nBETA\ngamma\n" {
		t.Fatalf("edited content=%q err=%v", data, readErr)
	}
}

func TestEditFileToolReportsRecoverableStaleContext(t *testing.T) {
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "sample.txt"), []byte("current value\n"), 0644); err != nil {
		t.Fatal(err)
	}
	_, err := NewEditFileTool(workspace).Execute(editTestContext(), map[string]interface{}{
		"file_path": "sample.txt", "old_string": "old value", "new_string": "new value",
	})
	var recoverable toolcontract.RecoverableError
	if !errors.As(err, &recoverable) || recoverable.ToolRecoveryDirective().SuggestedTool != "read_file" {
		t.Fatalf("stale edit error=%v, want read_file recovery directive", err)
	}
}

func TestEditFileToolUsesInjectedPathPolicy(t *testing.T) {
	tool := NewEditFileTool(t.TempDir(), FileToolDependencies{
		EnforcePath: func(context.Context, string, string) error { return os.ErrPermission },
	})
	if _, err := tool.Execute(editTestContext(), map[string]interface{}{
		"file_path": "blocked.txt", "old_string": "a", "new_string": "b",
	}); !errors.Is(err, os.ErrPermission) {
		t.Fatalf("policy error=%v, want permission denial", err)
	}
}

func TestMultiEditToolAppliesCoordinatedChanges(t *testing.T) {
	workspace := t.TempDir()
	for name, content := range map[string]string{"a.txt": "alpha\n", "b.txt": "beta\n"} {
		if err := os.WriteFile(filepath.Join(workspace, name), []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
		readForEdit(t, workspace, name)
	}

	result, err := NewMultiEditTool(workspace).Execute(editTestContext(), map[string]interface{}{
		"edits": []interface{}{
			map[string]interface{}{"file_path": "a.txt", "old_string": "alpha", "new_string": "ALPHA"},
			map[string]interface{}{"file_path": "b.txt", "old_string": "beta", "new_string": "BETA"},
		},
	})
	if err != nil || !strings.Contains(result, "applied 2 edit(s) atomically") {
		t.Fatalf("multiedit result=%q err=%v", result, err)
	}
	for name, want := range map[string]string{"a.txt": "ALPHA\n", "b.txt": "BETA\n"} {
		data, readErr := os.ReadFile(filepath.Join(workspace, name))
		if readErr != nil || string(data) != want {
			t.Fatalf("%s=%q err=%v, want %q", name, data, readErr, want)
		}
	}
}

func TestNormalizeQuotes(t *testing.T) {
	if got := NormalizeQuotes("‘one’ and “two”"); got != "'one' and \"two\"" {
		t.Fatalf("normalized quotes=%q", got)
	}
}
