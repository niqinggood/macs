package memory

import (
	"context"
	"io"
	"log"
	"strings"
	"testing"
)

func TestSessionMemoryExtractorThresholds(t *testing.T) {
	config := DefaultSessionMemoryConfig()
	config.MinimumTokensToInit = 10
	config.MinimumTokensBetweenUpdate = 5
	config.ToolCallsBetweenUpdates = 2
	extractor := NewSessionMemoryExtractor(t.TempDir(), config, SessionExtractorDependencies{
		CallLLM: func(context.Context, []map[string]interface{}, int) (string, error) { return "", nil },
		Logger:  log.New(io.Discard, "", 0),
	})
	if extractor.ShouldExtract(9, 2, false) {
		t.Fatal("extractor ran before initial token threshold")
	}
	if extractor.ShouldExtract(10, 1, false) {
		t.Fatal("extractor ran before tool-call threshold")
	}
	if !extractor.ShouldExtract(10, 1, false) {
		t.Fatal("extractor did not run after both thresholds")
	}
}

func TestSessionMemoryExtractionTextPreservesFailedStatus(t *testing.T) {
	extractor := NewSessionMemoryExtractor(t.TempDir(), DefaultSessionMemoryConfig(), SessionExtractorDependencies{})
	text := extractor.BuildExtractionText([]map[string]interface{}{{
		"role": "tool", "name": "read_file", "status": false, "content": "执行失败: permission denied",
	}})
	if !strings.Contains(text, "[tool-failed:read_file]") || !strings.Contains(text, "执行失败") {
		t.Fatalf("failed tool status missing: %q", text)
	}
}

func TestSessionMemoryPathIsSanitized(t *testing.T) {
	extractor := NewSessionMemoryExtractor(t.TempDir(), DefaultSessionMemoryConfig(), SessionExtractorDependencies{})
	extractor.SetSession("person/one")
	if !strings.HasSuffix(extractor.MemoryPath(), "session-memory-person-one.md") {
		t.Fatalf("MemoryPath() = %q", extractor.MemoryPath())
	}
}
