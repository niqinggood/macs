package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mac/internal/projectctx"
)

func TestBuildSystemPromptDynamicIncludesProjectInstructions(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("run targeted tests before handoff"), 0644); err != nil {
		t.Fatal(err)
	}
	agent := &LLMAgent{
		workspace:           root,
		projectInstructions: projectctx.NewProjectInstructionLoader(""),
	}
	prompt := agent.buildSystemPromptDynamic(root)
	if !strings.Contains(prompt, "## Project Instructions") ||
		!strings.Contains(prompt, "run targeted tests before handoff") {
		t.Fatalf("dynamic system prompt does not include project instructions:\n%s", prompt)
	}
}
