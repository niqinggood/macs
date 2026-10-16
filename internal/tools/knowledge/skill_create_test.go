package knowledge

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCreateSkillToolWritesSkillAndMetadata(t *testing.T) {
	root := t.TempDir()
	result, err := NewCreateSkillTool(root).Execute(context.Background(), map[string]interface{}{
		"name": "Report Builder", "description": "Build reports",
		"read_when": "报告,report", "content": "# Workflow\n\nBuild it.", "tags": "docs,automation",
	})
	if err != nil {
		t.Fatal(err)
	}
	skillPath := filepath.Join(root, "report-builder", "SKILL.md")
	data, err := os.ReadFile(skillPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"name: report-builder", "  - 报告", "  - report", "# Workflow"} {
		if !strings.Contains(string(data), expected) {
			t.Fatalf("SKILL.md missing %q:\n%s", expected, data)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "report-builder", "_meta.json")); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, skillPath) {
		t.Fatalf("result does not mention skill path: %s", result)
	}
}

func TestCreateSkillToolRejectsTraversalName(t *testing.T) {
	_, err := NewCreateSkillTool(t.TempDir()).Execute(context.Background(), map[string]interface{}{
		"name": "../outside", "description": "x", "read_when": "x", "content": "x",
	})
	if err == nil {
		t.Fatal("expected traversal name to be rejected")
	}
}
