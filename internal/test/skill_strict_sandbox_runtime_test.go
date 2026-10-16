package app

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

func TestExecuteBuiltinSkillMaterializesRuntimeInsideStrictSandboxWorkspace(t *testing.T) {
	workspace := t.TempDir()
	loader := NewSkillsLoader(workspace, "strict-agent", "")
	fixture := fstest.MapFS{
		"builtin/OPENKITTY_INDEX.json":    &fstest.MapFile{Data: []byte(`{"docs":{"desc":"docs","skills":["scripted"]}}`)},
		"builtin/scripted/SKILL.md":       &fstest.MapFile{Data: []byte("---\nname: scripted\ndescription: strict sandbox skill\n---\nRun scripts/run.py.\n")},
		"builtin/scripted/scripts/run.py": &fstest.MapFile{Data: []byte(`print("strict")`)},
	}
	if err := loader.LoadBuiltinSkillsFromFS(fixture, "builtin"); err != nil {
		t.Fatal(err)
	}
	sandbox := NewWorkspaceSandbox(workspace, SandboxConfig{
		Mode: SandboxStrict, ShellPolicy: "allow", NetworkPolicy: "deny",
	})
	ctx := withSandboxPolicy(context.Background(), sandbox)
	out, err := loader.ExecuteSkill(ctx, "scripted", nil)
	if err != nil {
		t.Fatalf("ExecuteSkill failed: %v", err)
	}
	runtimeDir := filepath.Join(workspace, ".openkitty", "runtime", "builtin", "scripted")
	if _, err := os.Stat(filepath.Join(runtimeDir, "scripts", "run.py")); err != nil {
		t.Fatalf("strict runtime was not materialized inside workspace: %v", err)
	}
	if !strings.Contains(out, filepath.Join(".openkitty", "runtime", "builtin", "scripted")) {
		t.Fatalf("use_skill did not expose the workspace runtime: %s", out)
	}
	if strings.Contains(out, OpenKittyHome()) && !strings.HasPrefix(OpenKittyHome(), workspace) {
		t.Fatalf("strict use_skill exposed process-user runtime: %s", out)
	}

	// Keep the compiler honest about the embedded fixture's fs.FS contract.
	var _ fs.FS = fixture
}
