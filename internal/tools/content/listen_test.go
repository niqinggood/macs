package content

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestListenAudioDirectoryCannotEscapeWorkspace(t *testing.T) {
	workspace := t.TempDir()
	tool := NewListenTool(workspace)
	ctx := context.WithValue(context.Background(), "task_id", "../../outside")

	directory, err := tool.audioDirectory(ctx)
	if err != nil {
		t.Fatalf("audioDirectory() error = %v", err)
	}
	storageRoot := filepath.Join(workspace, ".openkitty", "storage")
	relative, err := filepath.Rel(storageRoot, directory)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		t.Fatalf("audio directory escaped workspace storage: %s", directory)
	}
	outside := filepath.Join(filepath.Dir(workspace), "outside", "recordings")
	if _, err := os.Stat(outside); !os.IsNotExist(err) {
		t.Fatalf("unsafe directory was created outside workspace: %s", outside)
	}
}

func TestListenRejectsUnknownActionWithoutCreatingStorage(t *testing.T) {
	workspace := t.TempDir()
	_, err := NewListenTool(workspace).Execute(nil, map[string]interface{}{"action": "unsupported"})
	if err == nil || !strings.Contains(err.Error(), "unknown action") {
		t.Fatalf("Execute() error = %v, want unknown action", err)
	}
	if _, err := os.Stat(filepath.Join(workspace, ".openkitty")); !os.IsNotExist(err) {
		t.Fatalf("invalid action unexpectedly created runtime storage")
	}
}

func TestSafeMediaTaskIDIsStableAndPathSafe(t *testing.T) {
	first := safeMediaTaskID("task/../one")
	second := safeMediaTaskID("task/../one")
	if first != second {
		t.Fatalf("safeMediaTaskID() is not stable: %q != %q", first, second)
	}
	if strings.ContainsAny(first, `/\\`) || strings.Contains(first, "..") {
		t.Fatalf("safeMediaTaskID() returned unsafe value %q", first)
	}
}
