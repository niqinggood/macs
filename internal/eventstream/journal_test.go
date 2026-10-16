package eventstream

import (
	"fmt"
	"sync"
	"testing"
)

func TestJournalReplaysAfterCursor(t *testing.T) {
	t.Parallel()
	journal := NewJournal(4, 4)
	first := journal.Append("task-a", "progress", []byte(`{"step":1}`))
	second := journal.Append("task-a", "progress", []byte(`{"step":2}`))
	third := journal.Append("task-a", "completed", []byte(`{"status":"completed"}`))

	replay := journal.After("task-a", first.ID)
	if replay.Gap || replay.Reset || len(replay.Records) != 2 {
		t.Fatalf("replay = %#v", replay)
	}
	if replay.Records[0].ID != second.ID || replay.Records[1].ID != third.ID {
		t.Fatalf("records = %#v", replay.Records)
	}
	replay.Records[0].Data[0] = 'x'
	again := journal.After("task-a", first.ID)
	if string(again.Records[0].Data) != `{"step":2}` {
		t.Fatalf("journal data was mutated: %s", again.Records[0].Data)
	}
}

func TestJournalReportsBoundedGapAndGenerationReset(t *testing.T) {
	t.Parallel()
	journal := NewJournal(2, 4)
	first := journal.Append("task-a", "progress", []byte("1"))
	journal.Append("task-a", "progress", []byte("2"))
	journal.Append("task-a", "progress", []byte("3"))
	journal.Append("task-a", "progress", []byte("4"))

	replay := journal.After("task-a", first.ID)
	if !replay.Gap || replay.Reset || len(replay.Records) != 2 {
		t.Fatalf("gap replay = %#v", replay)
	}
	other := NewJournal(2, 4)
	if reset := other.After("task-a", first.ID); !reset.Reset {
		t.Fatalf("cross-generation cursor should reset: %#v", reset)
	}
	if malformed := journal.After("task-a", "not-an-id"); !malformed.Reset {
		t.Fatalf("malformed cursor should reset: %#v", malformed)
	}
}

func TestJournalBoundsNumberOfStreams(t *testing.T) {
	t.Parallel()
	journal := NewJournal(2, 2)
	first := journal.Append("task-1", "progress", []byte("1"))
	journal.Append("task-2", "progress", []byte("2"))
	journal.Append("task-3", "progress", []byte("3"))

	if replay := journal.After("task-1", first.ID); !replay.Reset {
		t.Fatalf("oldest stream should have been evicted: %#v", replay)
	}
	for index := 0; index < 20; index++ {
		journal.Append("task-3", "progress", []byte(fmt.Sprint(index)))
	}
	current := journal.streams["task-3"]
	if current == nil || len(current.records) != 2 {
		t.Fatalf("records = %#v", current)
	}
}

func TestJournalReplayRefreshesStreamRecency(t *testing.T) {
	t.Parallel()
	journal := NewJournal(2, 2)
	first := journal.Append("task-1", "progress", []byte("1"))
	second := journal.Append("task-2", "progress", []byte("2"))
	if replay := journal.After("task-1", first.ID); replay.Reset {
		t.Fatalf("refresh replay = %#v", replay)
	}
	journal.Append("task-3", "progress", []byte("3"))

	if replay := journal.After("task-2", second.ID); !replay.Reset {
		t.Fatalf("least-recently-used stream should have been evicted: %#v", replay)
	}
	if replay := journal.After("task-1", first.ID); replay.Reset {
		t.Fatalf("recently replayed stream should remain available: %#v", replay)
	}
}

func TestJournalRejectsCursorFromAnotherStream(t *testing.T) {
	t.Parallel()
	journal := NewJournal(4, 4)
	foreign := journal.Append("task-a", "progress", []byte("1"))
	journal.Append("task-b", "progress", []byte("1"))
	if replay := journal.After("task-b", foreign.ID); !replay.Reset {
		t.Fatalf("foreign cursor should reset: %#v", replay)
	}
}

func TestJournalLeavesOversizedEventLiveOnly(t *testing.T) {
	t.Parallel()
	journal := NewJournal(4, 4)
	record := journal.Append("task-a", "progress", make([]byte, maxEventBytes+1))
	if record.ID != "" {
		t.Fatalf("oversized event received replay ID %q", record.ID)
	}
	if len(journal.streams) != 0 {
		t.Fatalf("oversized event was retained: %#v", journal.streams)
	}
}

func TestJournalConcurrentAppendMaintainsSequenceAndBounds(t *testing.T) {
	t.Parallel()
	const (
		workers          = 32
		eventsPerWorker  = 20
		retainedCapacity = 128
	)
	journal := NewJournal(retainedCapacity, 4)
	ids := make(chan string, workers*eventsPerWorker)
	var wait sync.WaitGroup
	for worker := 0; worker < workers; worker++ {
		wait.Add(1)
		go func(worker int) {
			defer wait.Done()
			for event := 0; event < eventsPerWorker; event++ {
				record := journal.Append("shared-task", "progress", []byte(fmt.Sprintf("%d:%d", worker, event)))
				ids <- record.ID
			}
		}(worker)
	}
	wait.Wait()
	close(ids)

	seen := make(map[string]struct{}, workers*eventsPerWorker)
	for id := range ids {
		if id == "" {
			t.Fatal("concurrent append returned an empty event ID")
		}
		if _, duplicate := seen[id]; duplicate {
			t.Fatalf("duplicate event ID %q", id)
		}
		seen[id] = struct{}{}
	}
	current := journal.streams["shared-task"]
	if current == nil {
		t.Fatal("shared stream was not retained")
	}
	if current.next != workers*eventsPerWorker {
		t.Fatalf("next sequence = %d, want %d", current.next, workers*eventsPerWorker)
	}
	if len(current.records) != retainedCapacity {
		t.Fatalf("retained records = %d, want %d", len(current.records), retainedCapacity)
	}
	for index := 1; index < len(current.records); index++ {
		if current.records[index].sequence != current.records[index-1].sequence+1 {
			t.Fatalf("non-contiguous retained sequence at index %d: %d then %d", index, current.records[index-1].sequence, current.records[index].sequence)
		}
	}
}
