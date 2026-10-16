package workspace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteAndReadFileToolsRoundTripText(t *testing.T) {
	workspace := t.TempDir()
	writer := NewWriteFileTool(workspace)
	result, err := writer.Execute(context.Background(), map[string]interface{}{
		"path": "notes/result.md", "content": "# Result\n\nVerified content.",
	})
	if err != nil || !strings.Contains(result, "成功写入文件") {
		t.Fatalf("write failed: result=%q err=%v", result, err)
	}
	reader := NewReadFileTool(workspace)
	content, err := reader.Execute(context.Background(), map[string]interface{}{"path": "notes/result.md"})
	if err != nil || !strings.Contains(content, "Verified content") {
		t.Fatalf("read failed: content=%q err=%v", content, err)
	}
}

func TestWriteFileToolUsesInjectedPolicy(t *testing.T) {
	workspace := t.TempDir()
	tool := NewWriteFileTool(workspace, FileToolDependencies{
		EnforcePath: func(context.Context, string, string) error { return os.ErrPermission },
	})
	if _, err := tool.Execute(context.Background(), map[string]interface{}{"path": "blocked.txt", "content": "x"}); err == nil {
		t.Fatal("injected workspace policy was not enforced")
	}
	if _, err := os.Stat(filepath.Join(workspace, "blocked.txt")); !os.IsNotExist(err) {
		t.Fatalf("blocked file was created: %v", err)
	}
}

func TestWriteFileToolCreatesRealXLSX(t *testing.T) {
	workspace := t.TempDir()
	tool := NewWriteFileTool(workspace)
	result, err := tool.Execute(context.Background(), map[string]interface{}{
		"path": "report.xlsx", "content": "name,value\nalpha,42\n",
	})
	if err != nil || !strings.Contains(result, "成功写入文件") {
		t.Fatalf("xlsx write failed: result=%q err=%v", result, err)
	}
	data, err := os.ReadFile(filepath.Join(workspace, "report.xlsx"))
	if err != nil || len(data) < 4 || string(data[:2]) != "PK" {
		t.Fatalf("output is not an OOXML workbook: bytes=%d err=%v", len(data), err)
	}
}

func TestSelectTextLineRange(t *testing.T) {
	got, err := SelectTextLineRange("a\nb\nc\n", "2-9")
	if err != nil || strings.TrimSpace(got) != "[Lines 2-3 of 3]\nb\nc" {
		t.Fatalf("line range=%q err=%v", got, err)
	}
}
