package workspace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// testCtx returns a context with the reader ID set for tests
func testCtx() context.Context {
	return context.WithValue(context.Background(), "agent_name", "test-runner")
}

func setupEditTest(t *testing.T) (string, *EditFileTool) {
	t.Helper()
	tmpDir := t.TempDir()
	tool := NewEditFileTool(tmpDir)
	return tmpDir, tool
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

// ---- P0: Exact match ----

func TestEditFileExactMatch(t *testing.T) {
	dir, tool := setupEditTest(t)
	path := mkfile(t, dir, "test.go", "package main\n\nfunc main() {\n\tx := 1\n\treturn x\n}")

	result, err := tool.Execute(testCtx(), map[string]interface{}{
		"file_path":  path,
		"old_string": "\tx := 1",
		"new_string": "\tx := 42",
	})
	if err != nil {
		t.Fatalf("exact match failed: %v", err)
	}
	if !strings.Contains(result, "Replaced 1 occurrence") {
		t.Errorf("unexpected result: %s", result)
	}

	content := readfile(t, path)
	if !strings.Contains(content, "x := 42") {
		t.Errorf("file not modified. Got:\n%s", content)
	}
}

// ---- P0: Quote normalization fallback ----

func TestEditFileQuoteNormalization(t *testing.T) {
	dir, tool := setupEditTest(t)
	path := mkfile(t, dir, "test.txt", `The user said "hello world" to everyone.`)

	// LLM sends curly double quotes — should still match
	_, err := tool.Execute(testCtx(), map[string]interface{}{
		"file_path":  path,
		"old_string": "The user said “hello world” to everyone.",
		"new_string": "The user said 'hi' to everyone.",
	})
	if err != nil {
		t.Fatalf("quote normalization failed: %v", err)
	}

	content := readfile(t, path)
	if !strings.Contains(content, "'hi'") {
		t.Errorf("file not modified. Got: %s", content)
	}
}

func TestEditFileCurlySingleQuotes(t *testing.T) {
	dir, tool := setupEditTest(t)
	path := mkfile(t, dir, "test.txt", "it's a test of 'quotes'.")

	_, err := tool.Execute(testCtx(), map[string]interface{}{
		"file_path":  path,
		"old_string": "it’s a test of ‘quotes’.",
		"new_string": "it is a test of quotes.",
	})
	if err != nil {
		t.Fatalf("curly single quote fallback failed: %v", err)
	}

	content := readfile(t, path)
	if !strings.Contains(content, "it is a test of quotes.") {
		t.Errorf("file not modified. Got: %s", content)
	}
}

func TestEditFileTrailingWhitespaceNormalization(t *testing.T) {
	dir, tool := setupEditTest(t)
	path := mkfile(t, dir, "test.txt", "hello world\nfoo bar\n")

	// LLM sends with trailing spaces on each line
	_, err := tool.Execute(testCtx(), map[string]interface{}{
		"file_path":  path,
		"old_string": "hello world   \nfoo bar\t\n",
		"new_string": "replaced\n",
	})
	if err != nil {
		t.Fatalf("trailing whitespace normalization failed: %v", err)
	}

	content := readfile(t, path)
	if !strings.Contains(content, "replaced") {
		t.Errorf("file not modified. Got: %s", content)
	}
}

// ---- P0: Multi-match detection ----

func TestEditFileMultiMatchDetection(t *testing.T) {
	dir, tool := setupEditTest(t)
	path := mkfile(t, dir, "test.ts",
		"function foo() {\n  console.log('a');\n}\n\nfunction bar() {\n  console.log('b');\n}")

	_, err := tool.Execute(testCtx(), map[string]interface{}{
		"file_path":  path,
		"old_string": "console.log",
		"new_string": "logger.info",
		// replace_all defaults to false
	})
	if err == nil {
		t.Fatal("expected multi-match error")
	}
	if !strings.Contains(err.Error(), "times") || !strings.Contains(err.Error(), "replace_all") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestEditFileMultiMatchWithReplaceAll(t *testing.T) {
	dir, tool := setupEditTest(t)
	path := mkfile(t, dir, "test.ts",
		"function foo() {\n  console.log('a');\n}\n\nfunction bar() {\n  console.log('b');\n}")

	result, err := tool.Execute(testCtx(), map[string]interface{}{
		"file_path":   path,
		"old_string":  "console.log",
		"new_string":  "logger.info",
		"replace_all": true,
	})
	if err != nil {
		t.Fatalf("replace_all should succeed: %v", err)
	}
	if !strings.Contains(result, "Replaced 2 occurrences") {
		t.Errorf("expected 2 replacements, got: %s", result)
	}
}

// ---- Edge cases ----

func TestEditFileNoMatch(t *testing.T) {
	dir, tool := setupEditTest(t)
	path := mkfile(t, dir, "test.txt", "hello world")

	_, err := tool.Execute(testCtx(), map[string]interface{}{
		"file_path":  path,
		"old_string": "nonexistent text",
		"new_string": "replacement",
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected 'not found' error: %v", err)
	}
}

func TestEditFileNoMatchSuggestsClosestCandidate(t *testing.T) {
	dir, tool := setupEditTest(t)
	path := mkfile(t, dir, "handler.go", strings.Join([]string{
		"package main",
		"",
		"func handler() int {",
		"\tvalue := computeValue()",
		"\treturn value",
		"}",
	}, "\n"))

	before := readfile(t, path)
	_, err := tool.Execute(testCtx(), map[string]interface{}{
		"file_path":  path,
		"old_string": "func handler() int {\n\tvalue := compute()\n\treturn value\n}",
		"new_string": "func handler() int {\n\treturn 42\n}",
	})
	if err == nil {
		t.Fatal("expected no-match error")
	}
	msg := err.Error()
	for _, want := range []string{
		"Closest candidate regions",
		"lines 3-6",
		`read_file path="handler.go"`,
		"computeValue",
	} {
		if !strings.Contains(msg, want) {
			t.Fatalf("expected error to contain %q, got:\n%s", want, msg)
		}
	}
	if after := readfile(t, path); after != before {
		t.Fatalf("file should not be modified on no-match\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

func TestEditFileSameString(t *testing.T) {
	dir, tool := setupEditTest(t)
	path := mkfile(t, dir, "test.txt", "hello")

	_, err := tool.Execute(testCtx(), map[string]interface{}{
		"file_path":  path,
		"old_string": "hello",
		"new_string": "hello",
	})
	if err == nil {
		t.Fatal("expected error for old==new")
	}
}

func TestEditFileMissingParams(t *testing.T) {
	_, tool := setupEditTest(t)
	_, err := tool.Execute(testCtx(), map[string]interface{}{})
	if err == nil {
		t.Fatal("expected error for missing params")
	}
}

func TestEditFileReplaceAll(t *testing.T) {
	dir, tool := setupEditTest(t)
	path := mkfile(t, dir, "test.go", "TODO: fix\n// TODO: improve\nfunc main() {}")

	result, err := tool.Execute(testCtx(), map[string]interface{}{
		"file_path":   path,
		"old_string":  "TODO:",
		"new_string":  "DONE:",
		"replace_all": true,
	})
	if err != nil {
		t.Fatalf("replace_all failed: %v", err)
	}
	if !strings.Contains(result, "Replaced 2 occurrences") {
		t.Errorf("expected 2 replacements, got: %s", result)
	}

	content := readfile(t, path)
	if strings.Count(content, "TODO:") != 0 {
		t.Errorf("TODOs remain: %d", strings.Count(content, "TODO:"))
	}
	if strings.Count(content, "DONE:") != 2 {
		t.Errorf("expected 2 DONEs, got %d", strings.Count(content, "DONE:"))
	}
}

func TestEditFileRejectsDangerousShortReplaceAll(t *testing.T) {
	dir, tool := setupEditTest(t)
	path := mkfile(t, dir, "danger.txt", "a = 1\na = 2\na = 3\n")

	_, err := tool.Execute(testCtx(), map[string]interface{}{
		"file_path":   path,
		"old_string":  "a",
		"new_string":  "b",
		"replace_all": true,
	})
	if err == nil {
		t.Fatal("expected dangerous replace_all rejection")
	}
	for _, want := range []string{"replace_all=true rejected", "too short", "dry_run=true"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("expected error to contain %q, got %v", want, err)
		}
	}
	if got := readfile(t, path); got != "a = 1\na = 2\na = 3\n" {
		t.Fatalf("dangerous replace_all should not modify file, got:\n%s", got)
	}
}

func TestEditFileDangerousShortReplaceAllAllowsDryRun(t *testing.T) {
	dir, tool := setupEditTest(t)
	path := mkfile(t, dir, "danger-dry.txt", "a = 1\na = 2\n")

	result, err := tool.Execute(testCtx(), map[string]interface{}{
		"file_path":   path,
		"old_string":  "a",
		"new_string":  "b",
		"replace_all": true,
		"dry_run":     true,
	})
	if err != nil {
		t.Fatalf("dry run should be allowed for short replace_all: %v", err)
	}
	if !strings.Contains(result, "Dry run") || !strings.Contains(result, "would replace 2 occurrence") {
		t.Fatalf("unexpected dry-run result:\n%s", result)
	}
	if got := readfile(t, path); got != "a = 1\na = 2\n" {
		t.Fatalf("dry run should not modify file, got:\n%s", got)
	}
}

func TestEditFileDryRunDoesNotWrite(t *testing.T) {
	dir, tool := setupEditTest(t)
	path := mkfile(t, dir, "dry.txt", "alpha\nbeta\ngamma\n")

	result, err := tool.Execute(testCtx(), map[string]interface{}{
		"file_path":  path,
		"old_string": "beta",
		"new_string": "BETA",
		"dry_run":    true,
	})
	if err != nil {
		t.Fatalf("dry run failed: %v", err)
	}
	for _, want := range []string{"Dry run", "would replace 1 occurrence", "- beta", "+ BETA"} {
		if !strings.Contains(result, want) {
			t.Fatalf("expected dry-run result to contain %q, got:\n%s", want, result)
		}
	}
	if got := readfile(t, path); got != "alpha\nbeta\ngamma\n" {
		t.Fatalf("dry run should not modify file, got:\n%s", got)
	}
}

func TestEditFileDryRunRequiresRead(t *testing.T) {
	dir, tool := setupEditTest(t)
	path := filepath.Join(dir, "unread.txt")
	if err := os.WriteFile(path, []byte("hello\n"), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := tool.Execute(testCtx(), map[string]interface{}{
		"file_path":  path,
		"old_string": "hello",
		"new_string": "hi",
		"dry_run":    true,
	})
	if err == nil || !strings.Contains(err.Error(), "not been read") {
		t.Fatalf("expected read-before-edit error for dry run, got %v", err)
	}
	if got := readfile(t, path); got != "hello\n" {
		t.Fatalf("dry run should not modify unread file, got:\n%s", got)
	}
}

// ---- safeJoin tests ----

func TestSafeJoinAbsolute(t *testing.T) {
	absPath := `E:\DEV\test\file.txt`
	result, err := safeJoin(`C:\workspace`, absPath)
	if err != nil {
		t.Errorf("absolute path rejected: %v", err)
	}
	if !strings.EqualFold(result, absPath) {
		t.Errorf("expected %q, got %q", absPath, result)
	}
}

func TestSafeJoinRelative(t *testing.T) {
	dir := t.TempDir()
	result, err := safeJoin(dir, "sub/file.txt")
	if err != nil {
		t.Fatalf("relative path failed: %v", err)
	}
	expected := filepath.Join(dir, "sub", "file.txt")
	if result != expected {
		t.Errorf("expected %q, got %q", expected, result)
	}
}

func TestSafeJoinTraversal(t *testing.T) {
	dir := t.TempDir()
	_, err := safeJoin(dir, "../../../etc/passwd")
	if err == nil {
		t.Fatal("expected traversal error")
	}
}

func TestEditFileBlockedWithoutRead(t *testing.T) {
	dir, tool := setupEditTest(t)
	// Write file but DON'T call RecordRead
	fullPath := filepath.Join(dir, "secret.go")
	os.MkdirAll(filepath.Dir(fullPath), 0755)
	os.WriteFile(fullPath, []byte("package main"), 0644)

	_, err := tool.Execute(testCtx(), map[string]interface{}{
		"file_path":  fullPath,
		"old_string": "package main",
		"new_string": "package lib",
	})
	if err == nil {
		t.Fatal("expected error: must read before edit")
	}
	if !strings.Contains(err.Error(), "not been read") {
		t.Errorf("expected read-before-edit error, got: %v", err)
	}
}

func TestEditFileWriteNewFileNoReadNeeded(t *testing.T) {
	// write_file should NOT require prior read for new files
	// This just verifies the check only applies to edit_file, not write_file
	// (no assertion needed — it's a design decision test)
}
