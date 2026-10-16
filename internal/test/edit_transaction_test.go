package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	workspacetools "mac/internal/tools/workspace"
)

var globalFileState = workspacetools.GetFileStateRegistry()
var fingerprintFile = workspacetools.FingerprintFile

func testCtx() context.Context {
	return context.WithValue(context.Background(), "agent_name", "test-runner")
}

func setupEditTest(t *testing.T) (string, *EditFileTool) {
	t.Helper()
	tmpDir := t.TempDir()
	return tmpDir, NewEditFileTool(tmpDir)
}

func mkfile(t *testing.T, dir, name, content string) string {
	t.Helper()
	fullPath := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fullPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	globalFileState.RecordRead(fullPath, "test-runner")
	return fullPath
}

func readfile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestEditFileRejectsExternalContentChangeEvenWhenMtimeAndSizeMatch(t *testing.T) {
	dir, tool := setupEditTest(t)
	path := mkfile(t, dir, "stale.txt", "alpha beta\n")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("alpha zeta\n"), info.Mode().Perm()); err != nil {
		t.Fatal(err)
	}
	// Content hashes, not timestamp resolution, must provide the conflict.
	if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}

	_, err = tool.Execute(testCtx(), map[string]interface{}{
		"file_path":  path,
		"old_string": "alpha",
		"new_string": "ALPHA",
	})
	if err == nil || !strings.Contains(err.Error(), "[STALE FILE]") {
		t.Fatalf("expected stale content error, got %v", err)
	}
	if got := readfile(t, path); got != "alpha zeta\n" {
		t.Fatalf("external content was overwritten: %q", got)
	}
}

func TestEditFileOwnWriteBecomesNextContentBaseline(t *testing.T) {
	dir, tool := setupEditTest(t)
	path := mkfile(t, dir, "sequence.txt", "alpha\n")

	first, err := tool.Execute(testCtx(), map[string]interface{}{
		"file_path":  path,
		"old_string": "alpha",
		"new_string": "beta",
	})
	if err != nil {
		t.Fatalf("first edit failed: %v", err)
	}
	if !strings.Contains(first, "[Edit receipt]") || !strings.Contains(first, "sha256=") {
		t.Fatalf("first edit omitted receipt: %s", first)
	}

	// No extra RecordRead call: the successful write itself is the baseline.
	second, err := tool.Execute(testCtx(), map[string]interface{}{
		"file_path":  path,
		"old_string": "beta",
		"new_string": "gamma",
	})
	if err != nil {
		t.Fatalf("second edit failed: %v", err)
	}
	if !strings.Contains(second, "[Edit receipt]") {
		t.Fatalf("second edit omitted receipt: %s", second)
	}
	if got := readfile(t, path); got != "gamma\n" {
		t.Fatalf("unexpected final content: %q", got)
	}
}

func TestWriteFileRejectsOverwriteWhenPreviouslyReadContentChanged(t *testing.T) {
	dir, _ := setupEditTest(t)
	path := mkfile(t, dir, "artifact.txt", "first version\n")
	if err := os.WriteFile(path, []byte("other version\n"), 0644); err != nil {
		t.Fatal(err)
	}
	tool := NewWriteFileTool(dir)
	_, err := tool.Execute(testCtx(), map[string]interface{}{
		"path":    path,
		"content": "replacement",
	})
	if err == nil || !strings.Contains(err.Error(), "[STALE FILE]") {
		t.Fatalf("expected stale overwrite rejection, got %v", err)
	}
	if got := readfile(t, path); got != "other version\n" {
		t.Fatalf("write_file overwrote external content: %q", got)
	}
}

func TestWriteFileNewFileReturnsContentReceiptWithoutPriorRead(t *testing.T) {
	dir := t.TempDir()
	tool := NewWriteFileTool(dir)
	result, err := tool.Execute(testCtx(), map[string]interface{}{
		"path":    "new.txt",
		"content": "hello",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "[Edit receipt]") || !strings.Contains(result, "sha256=") {
		t.Fatalf("write receipt missing: %s", result)
	}
	if got := readfile(t, filepath.Join(dir, "new.txt")); got != "hello" {
		t.Fatalf("unexpected new file: %q", got)
	}
}

func TestMultiEditStalePreflightLeavesEveryFileUnchanged(t *testing.T) {
	dir, _ := setupEditTest(t)
	first := mkfile(t, dir, "a.txt", "alpha\n")
	second := mkfile(t, dir, "z.txt", "bravo\n")
	secondInfo, err := os.Stat(second)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(second, []byte("BRAVO\n"), secondInfo.Mode().Perm()); err != nil {
		t.Fatal(err)
	}
	tool := NewMultiEditTool(dir)

	_, err = tool.Execute(testCtx(), map[string]interface{}{
		"edits": []interface{}{
			map[string]interface{}{
				"file_path":  first,
				"old_string": "alpha",
				"new_string": "ALPHA",
			},
			map[string]interface{}{
				"file_path":  second,
				"old_string": "BRAVO",
				"new_string": "bravo",
			},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "no changes applied") {
		t.Fatalf("expected atomic stale preflight failure, got %v", err)
	}
	if got := readfile(t, first); got != "alpha\n" {
		t.Fatalf("first file changed before stale preflight completed: %q", got)
	}
	if got := readfile(t, second); got != "BRAVO\n" {
		t.Fatalf("external second-file content changed: %q", got)
	}
}

func TestMultiEditPreservesExecutableMode(t *testing.T) {
	if os.PathSeparator == '\\' {
		t.Skip("Windows does not expose POSIX executable mode semantics")
	}
	dir, _ := setupEditTest(t)
	path := mkfile(t, dir, "script.sh", "echo old\n")
	if err := os.Chmod(path, 0755); err != nil {
		t.Fatal(err)
	}
	globalFileState.RecordRead(path, "test-runner")
	tool := NewMultiEditTool(dir)
	_, err := tool.Execute(testCtx(), map[string]interface{}{
		"edits": []interface{}{map[string]interface{}{
			"file_path": path, "old_string": "old", "new_string": "new",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0755 {
		t.Fatalf("mode changed to %o", info.Mode().Perm())
	}
}

func TestFingerprintFileIsStableForUnchangedContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stable.txt")
	if err := os.WriteFile(path, []byte("stable\n"), 0644); err != nil {
		t.Fatal(err)
	}
	first, err := fingerprintFile(path)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Millisecond)
	second, err := fingerprintFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if first.ContentSHA256 == "" || first.ContentSHA256 != second.ContentSHA256 {
		t.Fatalf("unstable fingerprint: %q vs %q", first.ContentSHA256, second.ContentSHA256)
	}
}

func TestEditFileNoMatchCarriesStructuredRecoveryDirective(t *testing.T) {
	dir, tool := setupEditTest(t)
	path := mkfile(t, dir, "handler.go", "func handler() {\n\tcurrentValue()\n}\n")
	registry := NewToolRegistry()
	registry.Register(tool)
	result, err := registry.ExecuteRaw(testCtx(), ToolCall{ID: "edit-stale", Name: "edit_file", Arguments: map[string]interface{}{
		"file_path": path, "old_string": "func handler() {\n\toldValue()\n}", "new_string": "func handler() {\n\tnewValue()\n}",
	}})
	if err != nil {
		t.Fatalf("registry should return a failed ToolResult: %v", err)
	}
	if result.ExecutionSucceeded == nil || *result.ExecutionSucceeded || result.ErrorCode != "edit_stale_context" || result.Recovery == nil {
		t.Fatalf("missing structured edit recovery: %#v", result)
	}
	if result.Recovery.SuggestedTool != "read_file" || result.Recovery.ResourceKey == "" {
		t.Fatalf("invalid recovery directive: %#v", result.Recovery)
	}
	if got, _ := result.Recovery.Arguments["lines_range"].(string); got == "" {
		t.Fatalf("recovery should include a bounded read range: %#v", result.Recovery.Arguments)
	}
	if after := readfile(t, path); after != "func handler() {\n\tcurrentValue()\n}\n" {
		t.Fatalf("recoverable failure modified the file: %q", after)
	}
}
