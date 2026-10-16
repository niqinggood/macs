package knowledge

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLearnToolAnalyze(t *testing.T) {
	tmpDir := t.TempDir()

	// Create minimal events file with current timestamps (within last hour)
	now := time.Now()
	eventsDir := filepath.Join(tmpDir, ".openkitty")
	os.MkdirAll(eventsDir, 0755)
	eventJSON := `{"time":"` + now.Add(-30*time.Minute).Format(time.RFC3339) + `","type":"error","agent":"test_agent","task_id":"test_task","tool":"cli","summary":"工具执行错误: 工具 cli 不存在","detail":""}
{"time":"` + now.Add(-20*time.Minute).Format(time.RFC3339) + `","type":"error","agent":"test_agent","task_id":"test_task","tool":"read_file","summary":"工具执行错误: 工具 read_file 不存在","detail":""}
{"time":"` + now.Add(-10*time.Minute).Format(time.RFC3339) + `","type":"error","agent":"test_agent","task_id":"test_task","tool":"cli","summary":"工具执行错误: 工具 cli 不存在","detail":""}
`
	os.WriteFile(filepath.Join(eventsDir, "events.jsonl"), []byte(eventJSON), 0644)

	tool := NewLearnTool(tmpDir)
	result, err := tool.Execute(nil, map[string]interface{}{
		"action": "analyze",
		"since":  "24h",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	t.Logf("Analysis:\n%s", result)

	if !strings.Contains(result, "Event Summary") {
		t.Error("should contain event summary")
	}
	if !strings.Contains(result, "3 errors") {
		t.Error("should report 3 errors")
	}
	if !strings.Contains(result, "Tool not found") {
		t.Error("should classify tool not found errors")
	}
}

func TestLearnToolInsights(t *testing.T) {
	tmpDir := t.TempDir()
	memoryDir := filepath.Join(tmpDir, ".claude", "projects", "E--openkitty", "memory")
	os.MkdirAll(memoryDir, 0755)
	os.WriteFile(filepath.Join(memoryDir, "test_insight.md"), []byte(`---
name: test-insight
description: A test insight for testing
metadata:
  type: feedback
---

**Test insight content**
`), 0644)

	tool := NewLearnTool(tmpDir)
	result, err := tool.Execute(nil, map[string]interface{}{
		"action": "insights",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	t.Logf("Insights:\n%s", result)
	if !strings.Contains(result, "test-insight") {
		t.Error("should list test insight")
	}
	if !strings.Contains(result, "insights") {
		t.Error("should show insights count")
	}
}

func TestLearnToolMemory(t *testing.T) {
	tmpDir := t.TempDir()

	memoryDir := filepath.Join(tmpDir, ".claude", "projects", "E--openkitty", "memory")
	os.MkdirAll(memoryDir, 0755)

	tool := NewLearnTool(tmpDir)
	result, err := tool.Execute(nil, map[string]interface{}{
		"action": "memory",
		"title":  "Test Lesson",
		"lesson": "This is a test lesson learned from session analysis.",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(result, "Memory Saved") {
		t.Error("should confirm memory was saved")
	}

	entries, _ := os.ReadDir(memoryDir)
	t.Logf("Memory dir contents: %d entries", len(entries))
}
