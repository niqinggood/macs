package memory

import (
	"strings"
	"testing"
)

func TestSessionMemoryExtractionTextKeepsToolArtifactsOnly(t *testing.T) {
	extractor := NewSessionMemoryExtractor(t.TempDir(), DefaultSessionMemoryConfig(), SessionExtractorDependencies{})
	got := extractor.BuildExtractionText([]map[string]interface{}{
		{"role": "tool", "name": "ptc_execute", "content": "Traceback UnicodeDecodeError\nCreated .openkitty/storage/report/final.png"},
		{"role": "assistant", "content": "Auto verification required\nOriginal user request:\nUse `verify_work` with evidence."},
		{"role": "assistant", "content": "Final answer"},
	})

	if !strings.Contains(got, "[tool-artifacts:ptc_execute] .openkitty/storage/report/final.png") {
		t.Fatalf("tool artifact path missing: %q", got)
	}
	if strings.Contains(got, "Traceback") || strings.Contains(got, "Auto verification") {
		t.Fatalf("internal tool or verification content leaked: %q", got)
	}
	if !strings.Contains(got, "Final answer") {
		t.Fatalf("assistant final answer missing: %q", got)
	}
}
