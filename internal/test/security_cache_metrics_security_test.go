package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestCachePromptIDsRemainUniqueUnderConcurrency(t *testing.T) {
	tracker := NewCacheMetricsTracker(64)
	tracker.promptDir = t.TempDir()
	tracker.promptDumpEnabled = true
	tracker.promptDumpMaxFiles = 64
	const calls = 32
	var wg sync.WaitGroup
	for i := 0; i < calls; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			tracker.RecordWithPrompt(
				CacheRecord{TaskID: fmt.Sprintf("task-%d", i)},
				[]map[string]interface{}{{"role": "user", "content": fmt.Sprintf("request-%d %s", i, syntheticPersistenceSecret)}},
			)
		}(i)
	}
	wg.Wait()

	snapshot := tracker.Snapshot()
	if snapshot.TotalCalls != calls {
		t.Fatalf("recorded calls = %d, want %d", snapshot.TotalCalls, calls)
	}
	seen := make(map[int]bool, calls)
	for _, record := range snapshot.Recent {
		if seen[record.ID] {
			t.Fatalf("duplicate cache/prompt ID: %d", record.ID)
		}
		seen[record.ID] = true
		data, err := os.ReadFile(filepath.Join(tracker.promptDir, fmt.Sprintf("%d.json", record.ID)))
		if err != nil {
			t.Fatalf("read prompt %d: %v", record.ID, err)
		}
		if !strings.Contains(string(data), syntheticPersistenceSecret) {
			t.Fatalf("prompt %d lost workflow account information", record.ID)
		}
		if strings.Contains(string(data), "[REDACTED]") || strings.Contains(string(data), "[SCRUBBED]") {
			t.Fatalf("prompt %d contains a forced-redaction marker", record.ID)
		}
	}
}
