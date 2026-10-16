package session

import (
	"fmt"
	"sync"
	"testing"
)

func TestSessionTranscriptSnapshotsToolParameters(t *testing.T) {
	transcript := NewSessionTranscript("params-snapshot", "tester", t.TempDir())
	params := map[string]interface{}{
		"value":  "original",
		"nested": map[string]interface{}{"state": "before"},
	}
	transcript.RecordToolCall("capture", params)
	params["value"] = "mutated"
	params["nested"].(map[string]interface{})["state"] = "after"

	if got := transcript.Entries[0].ToolParams["value"]; got != "original" {
		t.Fatalf("recorded value = %#v, want immutable snapshot", got)
	}
	nested, _ := transcript.Entries[0].ToolParams["nested"].(map[string]interface{})
	if got := nested["state"]; got != "before" {
		t.Fatalf("recorded nested state = %#v, want immutable snapshot", got)
	}
}

func TestSessionTranscriptConcurrentToolRecordsRemainComplete(t *testing.T) {
	transcript := NewSessionTranscript("parallel-tools", "tester", t.TempDir())
	const calls = 100
	var wg sync.WaitGroup
	for i := 0; i < calls; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			name := fmt.Sprintf("tool-%03d", index)
			transcript.RecordToolCall(name, map[string]interface{}{"index": index})
			transcript.RecordToolResult(name, "ok", true, int64(index))
		}(i)
	}
	wg.Wait()

	if got, want := len(transcript.Entries), calls*2; got != want {
		t.Fatalf("concurrent transcript entries = %d, want %d", got, want)
	}
	counts := make(map[string]int, calls)
	for _, entry := range transcript.Entries {
		counts[entry.ToolName]++
	}
	for i := 0; i < calls; i++ {
		name := fmt.Sprintf("tool-%03d", i)
		if counts[name] != 2 {
			t.Fatalf("entries for %s = %d, want call and result", name, counts[name])
		}
	}
}
