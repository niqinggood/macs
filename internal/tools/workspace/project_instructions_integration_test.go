package workspace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mac/internal/projectctx"
)

func TestProjectInstructionsToolReturnsProvenanceAndOptionalContent(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("tool-visible guidance"), 0644); err != nil {
		t.Fatal(err)
	}
	tool := NewProjectInstructionsTool(root, projectctx.NewProjectInstructionLoader(""))

	audit, err := tool.Execute(context.Background(), map[string]interface{}{})
	if err != nil {
		t.Fatalf("audit project instructions: %v", err)
	}
	if !strings.Contains(audit, "AGENTS.md") || strings.Contains(audit, "tool-visible guidance") {
		t.Fatalf("default audit should show provenance without content:\n%s", audit)
	}

	withContent, err := tool.Execute(context.Background(), map[string]interface{}{
		"include_content": true,
	})
	if err != nil {
		t.Fatalf("show project instruction content: %v", err)
	}
	if !strings.Contains(withContent, "tool-visible guidance") {
		t.Fatalf("content view missing resolved instructions:\n%s", withContent)
	}
}
