package workspace

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func notepadTestContext(taskID string) context.Context {
	return context.WithValue(context.Background(), "task_id", taskID)
}

func executeNotepadForTest(t *testing.T, tool *NotepadTool, ctx context.Context, action, content string) string {
	t.Helper()
	params := map[string]interface{}{"action": action}
	if content != "" {
		params["content"] = content
	}
	result, err := tool.Execute(ctx, params)
	if err != nil {
		t.Fatalf("notepad %s: %v", action, err)
	}
	return result
}

func TestNotepadIsolatesTasksAndKeepsTaskLifecycle(t *testing.T) {
	workspace := t.TempDir()
	tool := NewNotepadTool(workspace)
	taskA, taskB := notepadTestContext("task-alpha"), notepadTestContext("task-beta")
	executeNotepadForTest(t, tool, taskA, "append", "alpha-one")
	executeNotepadForTest(t, tool, taskA, "append", "alpha-two")
	executeNotepadForTest(t, tool, taskB, "append", "beta-only")
	readA := executeNotepadForTest(t, tool, taskA, "read", "")
	if !strings.Contains(readA, "alpha-one") || !strings.Contains(readA, "alpha-two") || strings.Contains(readA, "beta-only") {
		t.Fatalf("task A notepad leaked or lost content: %s", readA)
	}
	readB := executeNotepadForTest(t, tool, taskB, "read", "")
	if !strings.Contains(readB, "beta-only") || strings.Contains(readB, "alpha-one") {
		t.Fatalf("task B notepad leaked or lost content: %s", readB)
	}
	pathA, _ := tool.pathForContext(taskA)
	pathB, _ := tool.pathForContext(taskB)
	if pathA == pathB {
		t.Fatalf("different tasks resolved to the same notepad: %s", pathA)
	}
	wantParent := filepath.Join(workspace, ".openkitty", "storage", ".system", "notepads")
	if filepath.Dir(pathA) != wantParent || filepath.Dir(pathB) != wantParent {
		t.Fatalf("task notepads are outside the task directory: %q, %q", pathA, pathB)
	}
	executeNotepadForTest(t, tool, taskA, "clear", "")
	if got := executeNotepadForTest(t, tool, taskA, "read", ""); got != "📝 草稿本为空" {
		t.Fatalf("task A clear result = %q", got)
	}
	if got := executeNotepadForTest(t, tool, taskB, "read", ""); !strings.Contains(got, "beta-only") {
		t.Fatalf("clearing task A affected task B: %s", got)
	}
}

func TestNotepadTaskIDCannotTraverseOrCollideAfterSanitizing(t *testing.T) {
	workspace := t.TempDir()
	tool := NewNotepadTool(workspace)
	maliciousIDs := []string{`../../outside/evil`, `..\..\outside\evil`, `C:\Windows\Temp\evil`, `/absolute/outside/evil`}
	seen := make(map[string]bool)
	base := filepath.Join(workspace, ".openkitty", "storage", ".system", "notepads")
	for index, taskID := range maliciousIDs {
		ctx := notepadTestContext(taskID)
		path, err := tool.pathForContext(ctx)
		if err != nil {
			t.Fatalf("resolve %q: %v", taskID, err)
		}
		relative, err := filepath.Rel(base, path)
		if err != nil || filepath.IsAbs(relative) || relative == ".." || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) {
			t.Fatalf("task ID %q escaped notepad directory: %q", taskID, path)
		}
		if filepath.Dir(path) != base || filepath.Ext(path) != ".md" || strings.ContainsAny(filepath.Base(path), `/\:`) {
			t.Fatalf("unsafe task notepad path for %q: %q", taskID, path)
		}
		if seen[path] {
			t.Fatalf("different malicious IDs collided at %q", path)
		}
		seen[path] = true
		executeNotepadForTest(t, tool, ctx, "append", fmt.Sprintf("task-%d", index))
	}
}

func TestNotepadWithoutTaskIDUsesExplicitLegacyPath(t *testing.T) {
	workspace := t.TempDir()
	tool := NewNotepadTool(workspace)
	path, err := tool.pathForContext(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want, _ := filepath.Abs(filepath.Join(workspace, filepath.FromSlash(legacyNotepadRelativePath)))
	if path != want {
		t.Fatalf("legacy path = %q, want %q", path, want)
	}
	executeNotepadForTest(t, tool, context.Background(), "append", "legacy-note")
	if got := executeNotepadForTest(t, tool, context.Background(), "read", ""); !strings.Contains(got, "legacy-note") {
		t.Fatalf("legacy note missing: %s", got)
	}
}

func TestNotepadConcurrentAppendsDoNotLoseLines(t *testing.T) {
	workspace := t.TempDir()
	tools := []*NotepadTool{NewNotepadTool(workspace), NewNotepadTool(workspace)}
	ctx := notepadTestContext("concurrent-task")
	const writes = 100
	start := make(chan struct{})
	errors := make(chan error, writes)
	var waitGroup sync.WaitGroup
	for index := 0; index < writes; index++ {
		waitGroup.Add(1)
		go func(index int) {
			defer waitGroup.Done()
			<-start
			_, err := tools[index%len(tools)].Execute(ctx, map[string]interface{}{"action": "append", "content": fmt.Sprintf("line-%03d", index)})
			if err != nil {
				errors <- err
			}
		}(index)
	}
	close(start)
	waitGroup.Wait()
	close(errors)
	for err := range errors {
		t.Fatalf("concurrent append: %v", err)
	}
	path, _ := tools[0].pathForContext(ctx)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	if len(lines) != writes {
		t.Fatalf("line count = %d, want %d", len(lines), writes)
	}
	seen := make(map[string]bool, writes)
	for _, line := range lines {
		if seen[line] {
			t.Fatalf("duplicate line %q", line)
		}
		seen[line] = true
	}
}
