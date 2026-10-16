package content

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWatchListUsesTaskScopedDirectory(t *testing.T) {
	workspace := t.TempDir()
	ctx := context.WithValue(context.Background(), "task_id", "task-one")
	result, err := NewWatchTool(workspace).Execute(ctx, map[string]interface{}{"action": "list"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, ".openkitty") || !strings.Contains(result, "captures") {
		t.Fatalf("unexpected list result: %s", result)
	}
}

func TestWatchTaskIDCannotEscapeWorkspace(t *testing.T) {
	workspace := t.TempDir()
	ctx := context.WithValue(context.Background(), "task_id", "../../outside")
	if _, err := NewWatchTool(workspace).Execute(ctx, map[string]interface{}{"action": "list"}); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(filepath.Dir(workspace), "outside", "captures")
	if _, err := os.Stat(outside); !os.IsNotExist(err) {
		t.Fatalf("unsafe task directory was created outside workspace: %s", outside)
	}
}
