package workspace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMultiEditSameFileSequentialEdits(t *testing.T) {
	dir, _ := setupEditTest(t)
	path := mkfile(t, dir, "sample.txt", "alpha\nbeta\ngamma\n")
	tool := NewMultiEditTool(dir)

	done := make(chan struct{})
	var result string
	var err error
	go func() {
		defer close(done)
		result, err = tool.Execute(testCtx(), map[string]interface{}{
			"edits": []interface{}{
				map[string]interface{}{
					"file_path":  path,
					"old_string": "alpha",
					"new_string": "ALPHA",
				},
				map[string]interface{}{
					"file_path":  path,
					"old_string": "gamma",
					"new_string": "GAMMA",
				},
			},
		})
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("multiedit deadlocked on multiple edits to the same file")
	}
	if err != nil {
		t.Fatalf("multiedit failed: %v", err)
	}
	if !strings.Contains(result, "Dry-run validated") {
		t.Fatalf("expected dry-run report, got:\n%s", result)
	}
	content := readfile(t, path)
	for _, want := range []string{"ALPHA", "beta", "GAMMA"} {
		if !strings.Contains(content, want) {
			t.Fatalf("expected %q in content:\n%s", want, content)
		}
	}
}

func TestMultiEditNoMatchIsAtomicAndSuggestsCandidate(t *testing.T) {
	dir, _ := setupEditTest(t)
	first := mkfile(t, dir, "first.txt", "hello\nworld\n")
	second := mkfile(t, dir, "second.txt", "func handler() {\n\tcomputeValue()\n}\n")
	tool := NewMultiEditTool(dir)

	beforeFirst := readfile(t, first)
	beforeSecond := readfile(t, second)
	_, err := tool.Execute(testCtx(), map[string]interface{}{
		"edits": []interface{}{
			map[string]interface{}{
				"file_path":  first,
				"old_string": "hello",
				"new_string": "HELLO",
			},
			map[string]interface{}{
				"file_path":  second,
				"old_string": "compute()",
				"new_string": "computeValue()",
			},
		},
	})
	if err == nil {
		t.Fatal("expected no-match error")
	}
	msg := err.Error()
	for _, want := range []string{"no changes applied", "Closest candidate regions", "computeValue"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("expected error to contain %q, got:\n%s", want, msg)
		}
	}
	if got := readfile(t, first); got != beforeFirst {
		t.Fatalf("first file changed despite failed dry-run:\n%s", got)
	}
	if got := readfile(t, second); got != beforeSecond {
		t.Fatalf("second file changed despite failed dry-run:\n%s", got)
	}
}

func TestMultiEditRequiresReadBeforeExistingEdit(t *testing.T) {
	dir, _ := setupEditTest(t)
	path := mkfile(t, dir, "read.txt", "hello\n")
	tool := NewMultiEditTool(dir)

	_, err := tool.Execute(context.Background(), map[string]interface{}{
		"edits": []interface{}{
			map[string]interface{}{
				"file_path":  path,
				"old_string": "hello",
				"new_string": "hi",
			},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "not been read") {
		t.Fatalf("expected read-before-edit error, got %v", err)
	}
}

func TestMultiEditDirectoryPreflightPreventsPartialWrite(t *testing.T) {
	dir, _ := setupEditTest(t)
	first := mkfile(t, dir, "a.txt", "hello\n")
	blocker := mkfile(t, dir, "blocker", "not a directory\n")
	tool := NewMultiEditTool(dir)

	beforeFirst := readfile(t, first)
	_, err := tool.Execute(testCtx(), map[string]interface{}{
		"edits": []interface{}{
			map[string]interface{}{
				"file_path":  first,
				"old_string": "hello",
				"new_string": "HELLO",
			},
			map[string]interface{}{
				"file_path":  filepath.Join(blocker, "child.txt"),
				"old_string": "",
				"new_string": "new file\n",
			},
		},
	})
	if err == nil {
		t.Fatal("expected preflight error")
	}
	if !strings.Contains(err.Error(), "no changes applied") {
		t.Fatalf("expected no changes applied error, got %v", err)
	}
	if got := readfile(t, first); got != beforeFirst {
		t.Fatalf("first file changed despite preflight failure:\n%s", got)
	}
	// Unix reports ENOTDIR here while Windows commonly reports ENOENT. Either
	// proves that no child was created beneath the blocker file.
	if _, statErr := os.Stat(filepath.Join(blocker, "child.txt")); statErr == nil {
		t.Fatalf("child file should not exist, stat err=%v", statErr)
	}
}

func TestRollbackMultiEditWritesRestoresExistingAndRemovesCreated(t *testing.T) {
	dir := t.TempDir()
	existing := filepath.Join(dir, "existing.txt")
	created := filepath.Join(dir, "created.txt")
	if err := os.WriteFile(existing, []byte("before\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(existing, []byte("after\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(created, []byte("created\n"), 0644); err != nil {
		t.Fatal(err)
	}

	msg := RollbackMultiEditWrites(
		[]string{existing, created},
		map[string][]byte{existing: []byte("before\n")},
		map[string]bool{created: true},
	)
	if !strings.Contains(msg, "Rollback completed") {
		t.Fatalf("unexpected rollback message: %q", msg)
	}
	if got := readfile(t, existing); got != "before\n" {
		t.Fatalf("existing file not restored: %q", got)
	}
	if _, err := os.Stat(created); !os.IsNotExist(err) {
		t.Fatalf("created file should be removed, stat err=%v", err)
	}
}
