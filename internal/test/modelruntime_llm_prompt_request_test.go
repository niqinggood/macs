package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRequestMessagesWithErrorFeedbackDoesNotMutateBaseMessages(t *testing.T) {
	base := []map[string]interface{}{
		{"role": "system", "content": "static"},
		{"role": "user", "content": "hello"},
	}

	first := requestMessagesWithErrorFeedback(base, "bad tool args")
	second := requestMessagesWithErrorFeedback(base, "bad tool args again")

	if len(base) != 2 {
		t.Fatalf("base messages mutated, len = %d", len(base))
	}
	if len(first) != 3 || len(second) != 3 {
		t.Fatalf("patched message lengths = %d/%d, want 3/3", len(first), len(second))
	}
	if first[0]["content"] != "static" || first[2]["content"] != "hello" {
		t.Fatalf("message order not preserved: %#v", first)
	}
	content, _ := first[1]["content"].(string)
	if !strings.Contains(content, "ERROR CONTEXT") || !strings.Contains(content, "bad tool args") {
		t.Fatalf("missing error context message: %#v", first[1])
	}
}

func TestColdMessagesPromptInfoMatchesStoragePath(t *testing.T) {
	filePath, sessionDir, taskFile := coldMessagesFilePath(filepath.Join("root", "logs"), "web_ctx-t2")
	if filePath != filepath.Join("root", "logs", "web_ctx", "t2.json") {
		t.Fatalf("web cold file path = %q", filePath)
	}
	if sessionDir != filepath.Join("root", "logs", "web_ctx") || taskFile != "t2.json" {
		t.Fatalf("web cold parts = %q/%q", sessionDir, taskFile)
	}
	sess, task, rel := coldMessagesPromptInfo("web_ctx-t2", "ignored")
	if sess != "web_ctx" || task != "t2" || rel != filepath.Join(".openkitty", "logs", "web_ctx", "t2.json") {
		t.Fatalf("web prompt info = %q/%q/%q", sess, task, rel)
	}

	filePath, sessionDir, taskFile = coldMessagesFilePath(filepath.Join("root", "logs"), "qq_task_123")
	if filePath != filepath.Join("root", "logs", "qq_task_123.json") {
		t.Fatalf("channel cold file path = %q", filePath)
	}
	if sessionDir != filepath.Join("root", "logs") || taskFile != "qq_task_123.json" {
		t.Fatalf("channel cold parts = %q/%q", sessionDir, taskFile)
	}
	sess, task, rel = coldMessagesPromptInfo("qq_task_123", "qq_group_abc")
	if sess != "qq_group_abc" || task != "qq_task_123" || rel != filepath.Join(".openkitty", "logs", "qq_task_123.json") {
		t.Fatalf("channel prompt info = %q/%q/%q", sess, task, rel)
	}
}

func TestColdMessagesPromptLineDoesNotExposeMissingCurrentLogPath(t *testing.T) {
	dir := t.TempDir()
	_, _, rel := coldMessagesPromptInfo("web_ctx-t2", "")

	missing := coldMessagesPromptLine(dir, "web_ctx-t2", rel)
	if strings.Contains(missing, rel) {
		t.Fatalf("missing current cold log should not be exposed for reading: %q", missing)
	}
	if !strings.Contains(missing, "not saved yet") {
		t.Fatalf("missing current cold log should explain availability: %q", missing)
	}

	filePath, sessionDir, _ := coldMessagesFilePath(dir, "web_ctx-t2")
	if err := os.MkdirAll(sessionDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filePath, []byte("[]"), 0644); err != nil {
		t.Fatal(err)
	}
	existing := coldMessagesPromptLine(dir, "web_ctx-t2", rel)
	if !strings.Contains(existing, rel) {
		t.Fatalf("existing cold log should be exposed: %q", existing)
	}
}
