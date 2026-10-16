package app

import (
	"context"
	"path/filepath"
	"testing"

	workspacetools "mac/internal/tools/workspace"
)

type instructionProviderStub struct {
	workspace string
	target    string
}

func (provider *instructionProviderStub) LoadInstructions(workspace, target string) (string, string, error) {
	provider.workspace, provider.target = workspace, target
	return "audit", "resolved prompt", nil
}

func TestProjectInstructionsToolIncludesContentOnRequest(t *testing.T) {
	provider := &instructionProviderStub{}
	tool := workspacetools.NewProjectInstructionsTool("/workspace", provider)
	result, err := tool.Execute(context.Background(), map[string]interface{}{
		"target_dir": "subproject", "include_content": true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result != "audit\n\nresolved prompt" {
		t.Fatalf("unexpected result: %q", result)
	}
	if provider.workspace != "/workspace" || provider.target != filepath.Join("/workspace", "subproject") {
		t.Fatalf("provider paths = %q, %q", provider.workspace, provider.target)
	}
}
