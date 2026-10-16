package app

import (
	"os"
	"strings"
	"testing"
)

func TestDocxSkillPreservesSourceAndRecoversFromFileLock(t *testing.T) {
	data, err := os.ReadFile("builtin/documents/docx-tools/SKILL.md")
	if err != nil {
		t.Fatalf("read docx skill: %v", err)
	}
	content := string(data)
	for _, required := range []string{
		"Preserve the source document by default",
		"distinct output",
		"PermissionError",
		"do not request elevation",
		"save the completed document under a new filename",
	} {
		if !strings.Contains(content, required) {
			t.Fatalf("docx edit policy missing %q", required)
		}
	}
}
