package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	contenttools "mac/internal/tools/content"
)

const (
	exportTestAPIKey  = "sk-openkitty-export-1234567890abcdef"
	exportTestEmail   = "owner@example.com"
	exportTestPhone   = "13800138000"
	exportTestAccount = "demo_account"
)

func TestBuildExportHTMLRedactsAccountInfoWithoutMutatingMessages(t *testing.T) {
	rawContent := strings.Join([]string{
		"api_key=" + exportTestAPIKey,
		"email=" + exportTestEmail,
		"phone=" + exportTestPhone,
		"账号=" + exportTestAccount,
	}, "\n")
	messages := []ChatMessage{{
		Role:      "user",
		Content:   rawContent,
		CreatedAt: time.Date(2026, time.August, 5, 12, 0, 0, 0, time.Local),
	}}

	exported := buildExportHTML("session", messages)
	assertExportRedacted(t, exported)
	if messages[0].Content != rawContent {
		t.Fatalf("export mutated the live message\nwant: %q\ngot:  %q", rawContent, messages[0].Content)
	}
}

func TestConvertRedactsOnlyExportedCopy(t *testing.T) {
	workspace := t.TempDir()
	sourcePath := filepath.Join(workspace, "source.md")
	source := strings.Join([]string{
		"# Export",
		"api_key=" + exportTestAPIKey,
		"email=" + exportTestEmail,
		"phone=" + exportTestPhone,
		"账号=" + exportTestAccount,
	}, "\n")
	if err := os.WriteFile(sourcePath, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := NewConvertTool(workspace).Execute(context.Background(), map[string]interface{}{
		"input":  "source.md",
		"output": "export.html",
	}); err != nil {
		t.Fatalf("convert export: %v", err)
	}

	exported, err := os.ReadFile(filepath.Join(workspace, "export.html"))
	if err != nil {
		t.Fatal(err)
	}
	assertExportRedacted(t, string(exported))

	unchanged, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(unchanged) != source {
		t.Fatalf("conversion mutated its source\nwant: %q\ngot:  %q", source, unchanged)
	}
}

func TestMarkdownPDFExportUsesTemporarySanitizedCopy(t *testing.T) {
	dir := t.TempDir()
	sourcePath := filepath.Join(dir, "source.md")
	source := strings.Join([]string{
		"api_key=" + exportTestAPIKey,
		"email=" + exportTestEmail,
		"phone=" + exportTestPhone,
		"账号=" + exportTestAccount,
	}, "\n")
	if err := os.WriteFile(sourcePath, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}

	exportPath, cleanup, err := contenttools.PrepareMarkdownPDFExportInput(sourcePath, redactAccountInfoForExport)
	if err != nil {
		t.Fatal(err)
	}
	if exportPath == sourcePath || filepath.Dir(exportPath) != filepath.Dir(sourcePath) {
		cleanup()
		t.Fatalf("PDF export copy must be a sibling of its source: source=%q export=%q", sourcePath, exportPath)
	}
	exported, err := os.ReadFile(exportPath)
	if err != nil {
		cleanup()
		t.Fatal(err)
	}
	assertExportRedacted(t, string(exported))
	cleanup()
	if _, err := os.Stat(exportPath); !os.IsNotExist(err) {
		t.Fatalf("temporary PDF export copy was not removed: %v", err)
	}
	unchanged, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(unchanged) != source {
		t.Fatalf("PDF export mutated its source\nwant: %q\ngot:  %q", source, unchanged)
	}
}

func assertExportRedacted(t *testing.T, exported string) {
	t.Helper()
	for _, raw := range []string{exportTestAPIKey, exportTestEmail, exportTestPhone, exportTestAccount} {
		if strings.Contains(exported, raw) {
			t.Fatalf("export retained account value %q: %s", raw, exported)
		}
	}
	for _, marker := range []string{"[REDACTED]", "[REDACTED EMAIL]", "[REDACTED PHONE]"} {
		if !strings.Contains(exported, marker) {
			t.Fatalf("export missing marker %q: %s", marker, exported)
		}
	}
}
