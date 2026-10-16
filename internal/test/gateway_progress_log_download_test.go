package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadTaskProgressLogFromDiskDoesNotExposeColdFiles(t *testing.T) {
	tmp := t.TempDir()
	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(tmp); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(oldWd)
	})

	taskID := "web_260709010847_1e-t3"
	sessionDir := filepath.Join(".openkitty", "logs", "web_260709010847_1e")
	if err := os.MkdirAll(sessionDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sessionDir, "t3.json"), []byte(`{"cold":"task-log"}`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(".openkitty", "logs", "to_debug_2026-07-09_"+taskID+".log"), []byte("debug log"), 0644); err != nil {
		t.Fatal(err)
	}

	if content, filename, ok := loadTaskProgressLogFromDisk(taskID); ok {
		t.Fatalf("cold/debug files must not be exposed as progress logs: filename=%s content=%s", filename, content)
	}
}

func TestLoadTaskProgressLogFromDiskUsesDedicatedProgressFile(t *testing.T) {
	tmp := t.TempDir()
	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(tmp); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(oldWd)
	})

	taskID := "web_260709010847_1e-t3"
	progressDir := filepath.Join(".openkitty", "logs", "progress")
	if err := os.MkdirAll(progressDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(progressDir, taskID+".log"), []byte("Task start\nTool complete\n"), 0644); err != nil {
		t.Fatal(err)
	}

	content, filename, ok := loadTaskProgressLogFromDisk(taskID)
	if !ok {
		t.Fatal("expected dedicated progress log")
	}
	if filename != "progress_"+taskID+".log" {
		t.Fatalf("unexpected filename: %s", filename)
	}
	for _, want := range []string{"Task start", "Tool complete"} {
		if !strings.Contains(content, want) {
			t.Fatalf("download content missing %q: %s", want, content)
		}
	}
}

func TestLoadTaskProgressLogFromDiskInDirUsesConfiguredLogDir(t *testing.T) {
	tmp := t.TempDir()
	logDir := filepath.Join(tmp, "runtime", ".openkitty", "logs")
	taskID := "web_260709010847_1e-t3"
	sessionDir := filepath.Join(logDir, "web_260709010847_1e")
	if err := os.MkdirAll(sessionDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sessionDir, "t3.json"), []byte(`{"cold":"runtime-task-log"}`), 0644); err != nil {
		t.Fatal(err)
	}
	progressDir := filepath.Join(logDir, "progress")
	if err := os.MkdirAll(progressDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(progressDir, taskID+".log"), []byte("runtime progress log"), 0644); err != nil {
		t.Fatal(err)
	}

	content, _, ok := loadTaskProgressLogFromDiskInDir(taskID, logDir)
	if !ok {
		t.Fatal("expected dedicated progress log from configured log dir")
	}
	if strings.Contains(content, "runtime-task-log") || !strings.Contains(content, "runtime progress log") {
		t.Fatalf("download content missing runtime log: %s", content)
	}
}

func TestLoadTaskProgressLogFromDiskInDirIgnoresDirectTaskColdFile(t *testing.T) {
	tmp := t.TempDir()
	logDir := filepath.Join(tmp, ".openkitty", "logs")
	if err := os.MkdirAll(logDir, 0755); err != nil {
		t.Fatal(err)
	}
	taskID := "qq_task_123"
	if err := os.WriteFile(filepath.Join(logDir, taskID+".json"), []byte(`{"cold":"channel-task-log"}`), 0644); err != nil {
		t.Fatal(err)
	}

	if content, filename, ok := loadTaskProgressLogFromDiskInDir(taskID, logDir); ok {
		t.Fatalf("direct cold file must not be exposed: filename=%s content=%s", filename, content)
	}
}

func TestLoadTaskProgressLogFromDiskRejectsUnsafeTaskID(t *testing.T) {
	if _, _, ok := loadTaskProgressLogFromDisk("../web_260709010847_1e-t3"); ok {
		t.Fatal("unsafe task id should not be used for disk lookup")
	}
}

func TestGatewayProgressLogDirHonorsRuntimeDir(t *testing.T) {
	tmp := t.TempDir()
	g := &GatewayService{
		config: &Config{
			Workspace: tmp,
			Runtime: RuntimeConfig{
				RuntimeDir: "runtime",
			},
		},
	}

	got := g.progressLogDir()
	want := filepath.Join(tmp, "runtime", ".openkitty", "logs")
	if got != want {
		t.Fatalf("unexpected progress log dir:\n got: %s\nwant: %s", got, want)
	}
}

func TestSanitizeProgressLogMessageDropsInternalReasoning(t *testing.T) {
	got := sanitizeProgressLogMessage(ProgressEvent{
		Type:    "thinking",
		Message: "LLM: <think>secret reasoning</think>",
	})
	if got != "" {
		t.Fatalf("thinking progress should be dropped, got %q", got)
	}
}

func TestSanitizeProgressLogMessageKeepsGenericLLMCompletion(t *testing.T) {
	got := sanitizeProgressLogMessage(ProgressEvent{
		Type:    "llm_call",
		Message: "LLM: <think>secret reasoning</think> visible answer",
	})
	if got != "LLM call complete" {
		t.Fatalf("unexpected sanitized llm progress: %q", got)
	}
}

func TestBroadcastProgressPersistsSanitizedProgressOnly(t *testing.T) {
	tmp := t.TempDir()
	g := &GatewayService{
		config: &Config{Workspace: tmp},
	}
	taskID := "web_260709010847_1e-t3"
	g.BroadcastProgress(ProgressEvent{TaskID: taskID, Type: "thinking", Message: "LLM: <think>secret</think>"})
	g.BroadcastProgress(ProgressEvent{TaskID: taskID, Type: "heartbeat", Message: "模型仍在生成，已等待 10 秒"})
	g.BroadcastProgress(ProgressEvent{TaskID: taskID, Type: "tool_start", Message: "12:00:00 read_file .openkitty/storage/report.md"})

	if len(g.taskProgressLogs[taskID]) != 1 {
		t.Fatalf("expected only one user-facing progress line, got %#v", g.taskProgressLogs[taskID])
	}
	if strings.Contains(strings.Join(g.taskProgressLogs[taskID], "\n"), "secret") {
		t.Fatalf("progress log leaked reasoning: %#v", g.taskProgressLogs[taskID])
	}

	content, _, ok := loadTaskProgressLogFromDiskInDir(taskID, filepath.Join(tmp, ".openkitty", "logs"))
	if !ok {
		t.Fatal("expected persisted sanitized progress log")
	}
	if strings.Contains(content, "secret") || !strings.Contains(content, "read_file") {
		t.Fatalf("unexpected persisted progress log: %s", content)
	}
}
