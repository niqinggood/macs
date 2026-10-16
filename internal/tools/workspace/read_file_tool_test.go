package workspace

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/transform"
)

func TestReadFileTextLinesRange(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sample.txt")
	content := strings.Join([]string{"one", "two", "three", "four", "five"}, "\n") + "\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	tool := NewReadFileTool(dir)
	tests := []struct {
		name       string
		linesRange string
		want       string
		notWant    string
	}{
		{name: "head", linesRange: "head:2", want: "[Lines 1-2 of 5]\none\ntwo", notWant: "three"},
		{name: "tail", linesRange: "tail:2", want: "[Lines 4-5 of 5]\nfour\nfive", notWant: "three"},
		{name: "middle", linesRange: "2-4", want: "[Lines 2-4 of 5]\ntwo\nthree\nfour", notWant: "five"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tool.Execute(context.Background(), map[string]interface{}{
				"path":        "sample.txt",
				"lines_range": tt.linesRange,
			})
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(got, tt.want) {
				t.Fatalf("expected output to contain %q, got:\n%s", tt.want, got)
			}
			if strings.Contains(got, tt.notWant) {
				t.Fatalf("expected output not to contain %q, got:\n%s", tt.notWant, got)
			}
		})
	}
}

func TestReadFileTextLinesRangeRejectsInvalidRange(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "sample.txt"), []byte("one\ntwo\n"), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := NewReadFileTool(dir).Execute(context.Background(), map[string]interface{}{
		"path":        "sample.txt",
		"lines_range": "4-8",
	})
	if err == nil {
		t.Fatal("expected out-of-range lines_range to fail")
	}
	if !strings.Contains(err.Error(), "starts after end of file") {
		t.Fatalf("expected helpful range error, got %v", err)
	}
}

func TestReadFileTextEncodingGBK(t *testing.T) {
	dir := t.TempDir()
	var buf bytes.Buffer
	writer := transform.NewWriter(&buf, simplifiedchinese.GBK.NewEncoder())
	if _, err := io.WriteString(writer, "贵州燃气\n第二行\n"); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "gbk.txt"), buf.Bytes(), 0644); err != nil {
		t.Fatal(err)
	}

	got, err := NewReadFileTool(dir).Execute(context.Background(), map[string]interface{}{
		"path":     "gbk.txt",
		"encoding": "gbk",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "贵州燃气") || strings.Contains(got, "????") {
		t.Fatalf("expected decoded GBK content, got %q", got)
	}
}

func TestReadFileMissingFileSuggestsSimilarPath(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "pkg"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "pkg", "handler.go"), []byte("package pkg\n"), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := NewReadFileTool(dir).Execute(context.Background(), map[string]interface{}{
		"path": "pkg/handelr.go",
	})
	if err == nil {
		t.Fatal("expected missing file error")
	}
	msg := err.Error()
	for _, want := range []string{"file does not exist", `Did you mean "pkg/handler.go"`} {
		if !strings.Contains(msg, want) {
			t.Fatalf("expected error to contain %q, got %q", want, msg)
		}
	}
}

func TestReadFileRejectsBinaryPresentationAndPDF(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"slides.pptx", "report.pdf"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("PK\x03\x04\x00\x00binary"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	tool := NewReadFileTool(dir)
	for _, name := range []string{"slides.pptx", "report.pdf"} {
		t.Run(name, func(t *testing.T) {
			_, err := tool.Execute(context.Background(), map[string]interface{}{"path": name})
			if err == nil {
				t.Fatal("expected binary document read to fail")
			}
			msg := err.Error()
			for _, want := range []string{"cannot safely read binary", "Use extract/convert"} {
				if !strings.Contains(msg, want) {
					t.Fatalf("expected error to contain %q, got %q", want, msg)
				}
			}
		})
	}
}

func TestSelectTextLineRangeClipsPastEnd(t *testing.T) {
	got, err := SelectTextLineRange("a\nb\nc\n", "2-9")
	if err != nil {
		t.Fatal(err)
	}
	if got != "[Lines 2-3 of 3]\nb\nc" {
		t.Fatalf("unexpected clipped range output: %q", got)
	}
}
