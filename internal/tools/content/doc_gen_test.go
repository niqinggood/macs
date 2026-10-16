package content

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDocGenFindsMissingDocsFromWorkspaceRelativeFile(t *testing.T) {
	workspace := t.TempDir()
	source := "package demo\n\nfunc Exported(value string) string { return value }\n"
	if err := os.WriteFile(filepath.Join(workspace, "demo.go"), []byte(source), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	report, err := NewDocGenTool(workspace).Execute(context.Background(), map[string]interface{}{
		"action": "missing",
		"file":   "demo.go",
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !strings.Contains(report, "Exported") || !strings.Contains(report, "func Exported(value string) string") {
		t.Fatalf("missing-doc report is incomplete:\n%s", report)
	}
}

func TestDocGenGenerateWritesRelativeOutputInsideWorkspace(t *testing.T) {
	workspace := t.TempDir()
	source := "package demo\n\nfunc Exported() {}\n"
	if err := os.WriteFile(filepath.Join(workspace, "demo.go"), []byte(source), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	_, err := NewDocGenTool(workspace).Execute(context.Background(), map[string]interface{}{
		"action": "generate",
		"file":   "demo.go",
		"output": "documented.go",
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	content, err := os.ReadFile(filepath.Join(workspace, "documented.go"))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if !strings.Contains(string(content), "// Exported") {
		t.Fatalf("generated file lacks documentation:\n%s", content)
	}
}

func TestDocGenReferenceHandlesEmbeddedInterfaceMembers(t *testing.T) {
	workspace := t.TempDir()
	source := "package demo\n\ntype Base interface { Close() error }\n\ntype Service interface { Base; Run() error }\n"
	if err := os.WriteFile(filepath.Join(workspace, "service.go"), []byte(source), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	report, err := NewDocGenTool(workspace).Execute(context.Background(), map[string]interface{}{
		"action": "reference",
		"pkg":    ".",
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !strings.Contains(report, "Service") || !strings.Contains(report, "Run") {
		t.Fatalf("reference report is incomplete:\n%s", report)
	}
}

func TestDocGenHonorsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := NewDocGenTool(t.TempDir()).Execute(ctx, map[string]interface{}{"action": "reference"})
	if err == nil || !strings.Contains(err.Error(), "context canceled") {
		t.Fatalf("Execute() error = %v, want context canceled", err)
	}
}
