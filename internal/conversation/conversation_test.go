package conversation

import (
	"testing"
	"time"
)

func TestCompactBoundaryHelpers(t *testing.T) {
	messages := []map[string]interface{}{
		{"role": "user", "content": "before"},
		CreateCompactBoundary("auto", 1000),
		{"role": "user", "content": "after"},
	}
	if got := CountCompactBoundaries(messages); got != 1 {
		t.Fatalf("CountCompactBoundaries() = %d, want 1", got)
	}
	if got := GetMessagesAfterCompactBoundary(messages); len(got) != 2 {
		t.Fatalf("messages after boundary = %d, want 2", len(got))
	}
	if got := StripCompactBoundaries(messages); len(got) != 2 {
		t.Fatalf("messages after strip = %d, want 2", len(got))
	}
}

func TestMicroCompaction(t *testing.T) {
	messages := []map[string]interface{}{{
		"role": "tool", "name": "read_file", "tool_call_id": "call-1",
		"content": "large result", "_timestamp": time.Now().Add(-time.Hour),
	}}
	compaction := NewMicroCompaction(time.Minute)
	if !compaction.ShouldCompact(messages) || compaction.Compact(messages) != 1 {
		t.Fatal("old tool result was not compacted")
	}
	if compacted, _ := messages[0]["_compacted"].(bool); !compacted {
		t.Fatalf("compacted message = %#v", messages[0])
	}
}

func TestPostCompactCleanupScopes(t *testing.T) {
	mainCalls := 0
	subAgentCalls := 0
	cleanup := NewPostCompactCleanup()
	cleanup.Register("test-main", func() { mainCalls++ }, CleanupMainThread)
	cleanup.Register("test-subagent", func() { subAgentCalls++ }, CleanupSubAgent)
	cleanup.Run(CleanupSubAgent)
	if mainCalls != 0 || subAgentCalls != 1 {
		t.Fatalf("sub-agent cleanup: main=%d subagent=%d", mainCalls, subAgentCalls)
	}
	cleanup.Run(CleanupMainThread)
	if mainCalls != 1 || subAgentCalls != 2 {
		t.Fatalf("main cleanup: main=%d subagent=%d", mainCalls, subAgentCalls)
	}
}
