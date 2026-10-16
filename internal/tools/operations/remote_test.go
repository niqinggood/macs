package operations

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestRemoteToolBlocksCommandThroughInjectedPolicy(t *testing.T) {
	tool := NewRemoteTool(t.TempDir(), func(command string) bool { return strings.Contains(command, "forbidden") })
	_, err := tool.Execute(context.Background(), map[string]interface{}{
		"action": "exec", "host": "example.test", "command": "forbidden command", "dry_run": true,
	})
	if err == nil || !strings.Contains(err.Error(), "BLOCKED") {
		t.Fatalf("dangerous command was not blocked: %v", err)
	}
}

func TestRemoteToolDryRunResolvesRelativeUpload(t *testing.T) {
	workspace := t.TempDir()
	tool := NewRemoteTool(workspace, func(string) bool { return false })
	result, err := tool.Execute(context.Background(), map[string]interface{}{
		"action": "upload", "host": "example.test", "local_path": "dist/app", "remote_path": "/opt/app", "dry_run": true,
	})
	if err != nil {
		t.Fatalf("dry-run upload: %v", err)
	}
	want := filepath.Join(workspace, "dist", "app")
	if !strings.Contains(result, want) || !strings.Contains(result, "example.test:/opt/app") {
		t.Fatalf("unexpected dry-run output: %q", result)
	}
}

func TestRemoteToolFailsClosedWithoutSafetyPolicy(t *testing.T) {
	tool := NewRemoteTool(t.TempDir(), nil)
	_, err := tool.Execute(context.Background(), map[string]interface{}{
		"action": "exec", "host": "example.test", "command": "echo ok", "dry_run": true,
	})
	if err == nil || !strings.Contains(err.Error(), "safety policy is unavailable") {
		t.Fatalf("missing safety policy did not fail closed: %v", err)
	}
}
