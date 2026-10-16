package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	contenttools "mac/internal/tools/content"
	developmenttools "mac/internal/tools/development"
)

func TestCodeCheckSchemaExposesHTMLCheck(t *testing.T) {
	tool := developmenttools.NewCodeCheckTool(".")
	params := tool.Parameters()
	props := params["properties"].(map[string]interface{})
	action := props["action"].(map[string]interface{})
	values := action["enum"].([]string)

	found := false
	for _, value := range values {
		if value == "html_check" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("code_check action enum should include html_check: %v", values)
	}
	if !strings.Contains(tool.Description(), "html_check") {
		t.Fatalf("description should mention html_check, got %q", tool.Description())
	}
}

func TestCodeCheckOutputUsesASCIIStatusTags(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(file, []byte("hello\n"), 0644); err != nil {
		t.Fatal(err)
	}

	out, err := developmenttools.NewCodeCheckTool(dir).Execute(context.Background(), map[string]interface{}{
		"action": "syntax",
		"file":   file,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "[WARN] No syntax checker") {
		t.Fatalf("expected ASCII warning status, got %q", out)
	}
	for _, bad := range []string{"鉁", "鉂", "鈿", "�"} {
		if strings.Contains(out, bad) {
			t.Fatalf("code_check output should not contain mojibake marker %q: %q", bad, out)
		}
	}
}

func TestCodeCheckSyntaxValidatesOfficePackageRelationships(t *testing.T) {
	tests := []struct {
		name        string
		referenceID string
		wantOK      bool
	}{
		{name: "complete relationships", referenceID: "rId1", wantOK: true},
		{name: "undeclared relationship", referenceID: "rId2", wantOK: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			source := writeOPCTestPackage(t, minimalOPCTestParts(tt.referenceID))
			target := strings.TrimSuffix(source, filepath.Ext(source)) + ".xlsx"
			if err := os.Rename(source, target); err != nil {
				t.Fatal(err)
			}
			out, err := developmenttools.NewCodeCheckTool(filepath.Dir(target), contenttools.ValidateOfficeOpenXMLPackage).Execute(context.Background(), map[string]interface{}{
				"action": "syntax",
				"file":   target,
			})
			if err != nil {
				t.Fatal(err)
			}
			if gotOK := ToolExecutionSucceededForTool("code_check", out, nil); gotOK != tt.wantOK {
				t.Fatalf("OOXML check success=%v, want %v; output=%q", gotOK, tt.wantOK, out)
			}
			if !tt.wantOK && !strings.Contains(out, "undeclared relationship") {
				t.Fatalf("broken relationship diagnostic missing: %q", out)
			}
		})
	}
}

func TestCodeCheckRunPreservesQuotedArguments(t *testing.T) {
	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "show args.py")
	if err := os.WriteFile(scriptPath, []byte("import sys\nprint('|'.join(sys.argv[1:]))\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	out, err := developmenttools.NewCodeCheckTool(dir).Execute(context.Background(), map[string]interface{}{
		"action": "run",
		"file":   scriptPath,
		"args":   `"value with spaces" plain`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "value with spaces|plain") {
		t.Fatalf("quoted arguments were split incorrectly: %q", out)
	}
}

func TestCodeCheckRunUsesWorkspaceForRelativeArguments(t *testing.T) {
	dir := t.TempDir()
	scriptDir := filepath.Join(dir, ".openkitty", "runtime", "builtin", "example", "scripts")
	storageDir := filepath.Join(dir, ".openkitty", "storage")
	if err := os.MkdirAll(scriptDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(storageDir, 0o755); err != nil {
		t.Fatal(err)
	}
	scriptPath := filepath.Join(scriptDir, "read_arg.py")
	if err := os.WriteFile(scriptPath, []byte("import sys\nfrom pathlib import Path\np = sys.argv[1].removeprefix('@')\nprint(Path(p).read_text(encoding='utf-8'))\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(storageDir, "post.md"), []byte("workspace payload\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	out, err := developmenttools.NewCodeCheckTool(dir).Execute(context.Background(), map[string]interface{}{
		"action": "run",
		"file":   filepath.Join(".openkitty", "runtime", "builtin", "example", "scripts", "read_arg.py"),
		"args":   "@.openkitty/storage/post.md",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "workspace payload") {
		t.Fatalf("workspace-relative argument was resolved from the wrong directory: %q", out)
	}
}

func TestCodeCheckRunReportsExitErrorWithoutProcessOutput(t *testing.T) {
	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "silent_failure.py")
	if err := os.WriteFile(scriptPath, []byte("raise SystemExit(7)\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	out, err := developmenttools.NewCodeCheckTool(dir).Execute(context.Background(), map[string]interface{}{
		"action": "run",
		"file":   scriptPath,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "[FAIL] Run error:") || !strings.Contains(strings.ToLower(out), "exit status") {
		t.Fatalf("silent process failure did not retain its exit diagnostic: %q", out)
	}
	if ToolExecutionSucceededForTool("code_check", out, nil) {
		t.Fatalf("failed code_check run was classified as successful: %q", out)
	}
}

func TestCodeCheckGoSyntaxUsesPackageContext(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/syntaxcheck\n\ngo 1.22\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "util.go"), []byte("package syntaxcheck\n\nfunc Add(a, b int) int { return a + b }\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "util_test.go"), []byte("package syntaxcheck\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) { if Add(1, 2) != 3 { t.Fatal(\"bad\") } }\n"), 0644); err != nil {
		t.Fatal(err)
	}

	out, err := developmenttools.NewCodeCheckTool(dir).Execute(context.Background(), map[string]interface{}{
		"action": "syntax",
		"file":   filepath.Join(dir, "util.go"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "[OK] Go compile check OK") || !strings.Contains(out, "(.)") {
		t.Fatalf("expected package-level Go compile check, got %q", out)
	}
}

func TestCodeCheckGoSyntaxReportsCompileFailure(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/syntaxfail\n\ngo 1.22\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "broken.go"), []byte("package syntaxfail\n\nfunc Broken() {\n"), 0644); err != nil {
		t.Fatal(err)
	}

	out, err := developmenttools.NewCodeCheckTool(dir).Execute(context.Background(), map[string]interface{}{
		"action": "syntax",
		"file":   filepath.Join(dir, "broken.go"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "[FAIL] Go compile check failed") {
		t.Fatalf("expected Go compile failure output, got %q", out)
	}
}

func TestCodeCheckBuildDoesNotLeaveWorkspaceArtifact(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/buildcheck\n\ngo 1.22\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\nfunc main() {}\n"), 0644); err != nil {
		t.Fatal(err)
	}

	out, err := developmenttools.NewCodeCheckTool(dir).Execute(context.Background(), map[string]interface{}{
		"action": "build",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "[OK] Build OK") {
		t.Fatalf("expected successful build, got %q", out)
	}
	if _, err := os.Stat(filepath.Join(dir, "build_output")); err == nil {
		t.Fatal("code_check build should not leave build_output in workspace root")
	} else if !os.IsNotExist(err) {
		t.Fatal(err)
	}
}
