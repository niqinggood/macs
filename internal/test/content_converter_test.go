package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	contenttools "mac/internal/tools/content"
)

func TestTryBasePPTHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	out := filepath.Join(t.TempDir(), "canceled.pptx")
	started := time.Now()

	_, err := NewConvertTool(t.TempDir()).TryBasePPT(ctx, "# Canceled deck", out, out)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context cancellation, got %v", err)
	}
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("canceled PPT generation returned too slowly: %v", elapsed)
	}
}

func TestConvertResolvesRelativeInputAgainstWorkspace(t *testing.T) {
	workspace := t.TempDir()
	source := filepath.Join(workspace, "deck.md")
	if err := os.WriteFile(source, []byte("# Tenant deck\n\n- One"), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := NewConvertTool(workspace).Execute(context.Background(), map[string]interface{}{
		"input":  "deck.md",
		"output": "deck.html",
	})
	if err != nil {
		t.Fatalf("convert workspace-relative input: %v", err)
	}
	if !strings.Contains(result, filepath.Join(workspace, "deck.html")) {
		t.Fatalf("conversion result does not identify workspace output: %s", result)
	}
	data, err := os.ReadFile(filepath.Join(workspace, "deck.html"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "Tenant deck") {
		t.Fatalf("converted HTML lost source content: %s", data)
	}
}

func TestConvertCreatesRealDOCXWithoutExternalDependency(t *testing.T) {
	workspace := t.TempDir()
	source := filepath.Join(workspace, "report.md")
	if err := os.WriteFile(source, []byte("# 季度报告\n\n- 风险已识别"), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := NewConvertTool(workspace).Execute(context.Background(), map[string]interface{}{
		"input":  "report.md",
		"output": "report.docx",
	})
	if err != nil {
		t.Fatalf("convert DOCX: %v", err)
	}
	if !strings.Contains(result, "report.docx") {
		t.Fatalf("conversion receipt does not identify DOCX: %s", result)
	}
	text, err := readDocxText(filepath.Join(workspace, "report.docx"))
	if err != nil {
		t.Fatalf("open generated DOCX: %v", err)
	}
	if !strings.Contains(text, "季度报告") || !strings.Contains(text, "风险已识别") {
		t.Fatalf("generated DOCX lost source content: %q", text)
	}
}

func TestConvertEnforcesTenantWorkspaceForInputAndOutput(t *testing.T) {
	root := t.TempDir()
	aliceWorkspace := filepath.Join(root, "alice")
	bobWorkspace := filepath.Join(root, "bob")
	if err := os.MkdirAll(aliceWorkspace, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(bobWorkspace, 0o755); err != nil {
		t.Fatal(err)
	}
	aliceSource := filepath.Join(aliceWorkspace, "deck.md")
	bobSource := filepath.Join(bobWorkspace, "deck.md")
	if err := os.WriteFile(aliceSource, []byte("# Alice"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bobSource, []byte("# Bob"), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx := personalWorkspaceTestContext(aliceWorkspace, "user:alice")
	tool := NewConvertTool(aliceWorkspace)
	if _, err := tool.Execute(ctx, map[string]interface{}{
		"input":  bobSource,
		"output": "safe.html",
	}); err == nil || !strings.Contains(err.Error(), "personal workspace boundary") {
		t.Fatalf("cross-tenant input was not rejected: %v", err)
	}
	if _, err := tool.Execute(ctx, map[string]interface{}{
		"input":  aliceSource,
		"output": filepath.Join(bobWorkspace, "stolen.html"),
	}); err == nil || !strings.Contains(err.Error(), "personal workspace boundary") {
		t.Fatalf("cross-tenant output was not rejected: %v", err)
	}
}

func TestConvertExplicitPPTMasterWithoutProjectDoesNotSilentlyDowngrade(t *testing.T) {
	dir := t.TempDir()
	shimDir := filepath.Join(dir, "bin")
	if err := os.MkdirAll(shimDir, 0755); err != nil {
		t.Fatal(err)
	}

	if runtime.GOOS == "windows" {
		shim := filepath.Join(shimDir, "python.bat")
		if err := os.WriteFile(shim, []byte("@echo off\r\necho fake pptx> \"%~3\"\r\nexit /b 0\r\n"), 0644); err != nil {
			t.Fatal(err)
		}
	} else {
		shim := filepath.Join(shimDir, "python")
		if err := os.WriteFile(shim, []byte("#!/bin/sh\nprintf 'fake pptx' > \"$3\"\n"), 0755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", shimDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	skillDir := filepath.Join(dir, "skills", "presentation", "better-ppt")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "generate.py"), []byte("# fake better-ppt generator\n"), 0644); err != nil {
		t.Fatal(err)
	}

	out := filepath.Join(dir, "out.pptx")
	_, err := NewConvertTool(dir).ToPPTX(context.Background(), "# Demo\n\n- One\n- Two", out, "pptmaster")
	if err == nil || !strings.Contains(err.Error(), "without silent downgrade") || !strings.Contains(err.Error(), "requires a prepared upstream project") {
		t.Fatalf("missing explicit PPTmaster boundary: %v", err)
	}
	if _, statErr := os.Stat(out); !os.IsNotExist(statErr) {
		t.Fatalf("failed premium route must not leave a fallback PPTX: %v", statErr)
	}
}

func TestNormalizePPTGeneratorTreatsBetterPPTAsBasePPTAlias(t *testing.T) {
	if got := contenttools.NormalizePPTGenerator(""); got != "" {
		t.Fatalf("empty generator = %q, want explicit route requirement", got)
	}
	for _, input := range []string{"base-ppt", "base_ppt", "better-ppt", "better_ppt"} {
		if got := contenttools.NormalizePPTGenerator(input); got != "base-ppt" {
			t.Fatalf("normalizePPTGenerator(%q) = %q, want base-ppt", input, got)
		}
	}
}

func TestPPTXDirectConversionRequiresExplicitTier(t *testing.T) {
	out := filepath.Join(t.TempDir(), "regular.pptx")
	_, err := NewConvertTool(t.TempDir()).ToPPTX(context.Background(), "# Regular deck", out, "")
	if err == nil || !strings.Contains(err.Error(), "task-specific per-deck builder") || !strings.Contains(err.Error(), "generator=base-ppt") {
		t.Fatalf("missing regular/fallback route boundary: %v", err)
	}
	if _, statErr := os.Stat(out); !os.IsNotExist(statErr) {
		t.Fatalf("route error must not materialize a PPTX: %v", statErr)
	}
}

func TestConvertBasePPTScriptPathFindsCanonicalBuiltinDirectory(t *testing.T) {
	path := NewConvertTool(t.TempDir()).BasePPTScriptPath("generate.py")
	if path == "" {
		t.Fatal("expected base-ppt to find builtin/presentation/base-ppt/generate.py")
	}
	normalized := filepath.ToSlash(path)
	if !strings.HasSuffix(normalized, "/builtin/presentation/base-ppt/generate.py") &&
		!strings.HasSuffix(normalized, "/runtime/builtin/presentation/base-ppt/generate.py") {
		t.Fatalf("unexpected base-ppt script path: %s", path)
	}
}
