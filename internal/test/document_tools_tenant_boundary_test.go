package app

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExtractToolEnforcesTenantWorkspaceForInputAndOutput(t *testing.T) {
	workspace := t.TempDir()
	outside := filepath.Join(filepath.Dir(workspace), "other-user", "secret.pdf")
	tool := NewExtractTool(workspace, false)
	ctx := restrictedTenantTestContext(workspace)

	for _, params := range []map[string]interface{}{
		{"file": outside},
		{
			"file":   filepath.Join(workspace, "input.pdf"),
			"output": filepath.Join(filepath.Dir(outside), "extracted.md"),
		},
	} {
		if _, err := tool.Execute(ctx, params); err == nil ||
			!strings.Contains(err.Error(), "personal workspace boundary") {
			t.Fatalf("extract params=%#v error=%v, want tenant boundary", params, err)
		}
	}
}

func TestExtractToolDeclaresProcessAndConditionalNetworkCapabilities(t *testing.T) {
	local := NewExtractTool(t.TempDir(), false).SandboxCapabilities()
	if !local.Process || local.Network {
		t.Fatalf("local extract capabilities=%+v, want process only", local)
	}
	autoInstall := NewExtractTool(t.TempDir(), true).SandboxCapabilities()
	if !autoInstall.Process || !autoInstall.Network {
		t.Fatalf("auto-install extract capabilities=%+v, want process and network", autoInstall)
	}
}

func TestExtractTemplateRelativeOutputStaysInWorkspace(t *testing.T) {
	workspace := t.TempDir()
	templatePath := filepath.Join(workspace, "template.docx")
	archive, err := os.Create(templatePath)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(archive)
	document, err := zw.Create("word/document.xml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := document.Write([]byte(`<w:document><w:t>{{name}}</w:t></w:document>`)); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}

	outputName := "tenant-filled-" + filepath.Base(workspace) + ".docx"
	cwdOutput := filepath.Join(".", outputName)
	t.Cleanup(func() { _ = os.Remove(cwdOutput) })
	tool := NewExtractTool(workspace, false)
	if _, err := tool.Execute(restrictedTenantTestContext(workspace), map[string]interface{}{
		"file":          templatePath,
		"output":        outputName,
		"template_data": map[string]interface{}{"name": "Alice"},
	}); err != nil {
		t.Fatalf("fill template: %v", err)
	}
	if info, err := os.Stat(filepath.Join(workspace, outputName)); err != nil || info.Size() == 0 {
		t.Fatalf("workspace output missing or empty: info=%v err=%v", info, err)
	}
	if _, err := os.Stat(cwdOutput); !os.IsNotExist(err) {
		t.Fatalf("relative output escaped to process cwd: %v", err)
	}
}
