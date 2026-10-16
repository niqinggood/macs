package workspace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGrepContextIncludesMatchAndFollowingLines(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "sample.go"), []byte("line one\nneedle here\nline three\n"), 0644); err != nil {
		t.Fatal(err)
	}

	out, err := NewGrepTool(dir, nil).Execute(context.Background(), map[string]interface{}{
		"pattern":       "needle",
		"file_pattern":  "*.go",
		"context_lines": float64(1),
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"sample.go:2", "  1: line one", "> 2: needle here", "  3: line three"} {
		if !strings.Contains(out, want) {
			t.Fatalf("grep context should contain %q, got:\n%s", want, out)
		}
	}
}

func TestGrepLiteralSearchHandlesRegexMetacharacters(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "sample.js"), []byte("const value = call(foo[1]);\n"), 0644); err != nil {
		t.Fatal(err)
	}

	out, err := NewGrepTool(dir, nil).Execute(context.Background(), map[string]interface{}{
		"pattern":      "call(foo[1])",
		"file_pattern": "*.js",
		"literal":      true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "sample.js:1") {
		t.Fatalf("literal grep should find text with regex metacharacters, got:\n%s", out)
	}
}

func TestGrepInvalidRegexReturnsError(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "sample.txt"), []byte("needle\n"), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := NewGrepTool(dir, nil).Execute(context.Background(), map[string]interface{}{
		"pattern": "[",
	})
	if err == nil {
		t.Fatal("expected invalid regex error")
	}
	if !strings.Contains(err.Error(), "invalid grep regex") || !strings.Contains(err.Error(), "literal=true") {
		t.Fatalf("expected helpful invalid regex error, got %v", err)
	}
}

func TestGlobSupportsRecursiveAndBracePatterns(t *testing.T) {
	dir := t.TempDir()
	for _, rel := range []string{
		"main.go",
		filepath.Join("pkg", "handler_test.go"),
		filepath.Join("web", "app.ts"),
		filepath.Join("web", "style.css"),
	} {
		path := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("x"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	glob := NewGlobTool(dir, nil)
	out, err := glob.Execute(context.Background(), map[string]interface{}{
		"pattern": "**/*_test.go",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(filepath.ToSlash(out), "pkg/handler_test.go") {
		t.Fatalf("recursive glob should find nested test file, got:\n%s", out)
	}

	out, err = glob.Execute(context.Background(), map[string]interface{}{
		"pattern": "**/*.{js,ts}",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(filepath.ToSlash(out), "web/app.ts") || strings.Contains(filepath.ToSlash(out), "web/style.css") {
		t.Fatalf("brace glob should include ts/js and exclude css, got:\n%s", out)
	}
}

func TestGrepSkipsLargeFilesUnlessExplicit(t *testing.T) {
	dir := t.TempDir()
	largePath := filepath.Join(dir, "large.txt")
	large := strings.Repeat("x", MaxDefaultGrepFileBytes+1) + "\nneedle\n"
	if err := os.WriteFile(largePath, []byte(large), 0644); err != nil {
		t.Fatal(err)
	}

	grep := NewGrepTool(dir, nil)
	out, err := grep.Execute(context.Background(), map[string]interface{}{
		"pattern": "needle",
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "large.txt:") {
		t.Fatalf("default grep should skip oversized files, got:\n%s", out)
	}

	out, err = grep.Execute(context.Background(), map[string]interface{}{
		"pattern": "needle",
		"path":    largePath,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "needle") {
		t.Fatalf("explicit file grep should include oversized file, got:\n%s", out)
	}
}
