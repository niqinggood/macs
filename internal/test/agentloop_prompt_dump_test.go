package app

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPromptDumpRetentionKeepsNewestFiles(t *testing.T) {
	tracker := NewCacheMetricsTracker(8)
	tracker.promptDir = t.TempDir()
	tracker.promptDumpEnabled = true
	tracker.promptDumpMaxFiles = 2

	for i := 0; i < 3; i++ {
		tracker.SavePrompt([]map[string]interface{}{{"role": "user", "content": "test"}})
	}

	entries, err := os.ReadDir(tracker.promptDir)
	if err != nil {
		t.Fatalf("read prompt dir: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("prompt dump files = %d, want 2", len(entries))
	}
	if _, err := os.Stat(filepath.Join(tracker.promptDir, "0.json")); !os.IsNotExist(err) {
		t.Fatalf("oldest prompt dump was not pruned")
	}
	for _, name := range []string{"1.json", "2.json"} {
		if _, err := os.Stat(filepath.Join(tracker.promptDir, name)); err != nil {
			t.Fatalf("new prompt dump %s missing: %v", name, err)
		}
	}
}

func TestPromptDumpCanBeDisabledWithoutAffectingIDs(t *testing.T) {
	tracker := NewCacheMetricsTracker(8)
	tracker.promptDir = filepath.Join(t.TempDir(), "disabled")
	tracker.promptDumpEnabled = false

	if id := tracker.SavePrompt([]map[string]interface{}{{"role": "user", "content": "test"}}); id != 0 {
		t.Fatalf("first prompt ID = %d, want 0", id)
	}
	if id := tracker.SavePrompt(nil); id != 1 {
		t.Fatalf("second prompt ID = %d, want 1", id)
	}
	if _, err := os.Stat(tracker.promptDir); !os.IsNotExist(err) {
		t.Fatalf("disabled prompt dump unexpectedly created a directory")
	}
}

func TestPromptDumpSettingsFromEnvironment(t *testing.T) {
	t.Setenv("OPENKITTY_PROMPT_DUMP", "off")
	t.Setenv("OPENKITTY_PROMPT_DUMP_MAX_FILES", "17")

	tracker := NewCacheMetricsTracker(8)
	if tracker.promptDumpEnabled {
		t.Fatal("prompt dumping should be disabled by environment")
	}
	if tracker.promptDumpMaxFiles != 17 {
		t.Fatalf("prompt dump max files = %d, want 17", tracker.promptDumpMaxFiles)
	}
}
