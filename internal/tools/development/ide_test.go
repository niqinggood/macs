package development

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIDEToolOpenMissingFileDoesNotLaunchEditor(t *testing.T) {
	result, err := NewIDETool(t.TempDir()).Execute(context.Background(), map[string]interface{}{
		"action": "open",
		"file":   "missing.go",
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !strings.Contains(result, "File not found") {
		t.Fatalf("unexpected result: %s", result)
	}
}

func TestIDEBuildOpenCommandPreservesLocationAsSingleArgument(t *testing.T) {
	tool := NewIDETool(t.TempDir())
	command := tool.buildOpenCmd(context.Background(), "code", `C:\work folder\main.go:12:4`, "vscode")
	if len(command.Args) != 3 || command.Args[1] != "--goto" || command.Args[2] != `C:\work folder\main.go:12:4` {
		t.Fatalf("command arguments = %#v", command.Args)
	}
}

func TestDetectVSCodeWorkspacesScansProjectParent(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "project")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "team.code-workspace"), []byte(`{"folders":[]}`), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	workspaces, err := detectVSCodeWorkspaces(context.Background(), workspace)
	if err != nil {
		t.Fatalf("detectVSCodeWorkspaces() error = %v", err)
	}
	if len(workspaces) != 1 || filepath.ToSlash(workspaces[0]) != "team.code-workspace" {
		t.Fatalf("workspaces = %#v", workspaces)
	}
}

func TestDetectVSCodeWorkspacesHonorsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := detectVSCodeWorkspaces(ctx, t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "context canceled") {
		t.Fatalf("detectVSCodeWorkspaces() error = %v, want context canceled", err)
	}
}
