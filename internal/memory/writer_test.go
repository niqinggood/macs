package memory

import (
	"io"
	"log"
	"sync"
	"testing"
	"time"
)

func TestWriteQueueDebouncesAndFlushesLatestJob(t *testing.T) {
	var mu sync.Mutex
	var writes []string
	queue := NewWriteQueue(WriteDependencies{
		SaveTaskLog: func(_, _, content string) error {
			mu.Lock()
			writes = append(writes, content)
			mu.Unlock()
			return nil
		},
	}, log.New(io.Discard, "", 0))

	queue.EnqueueTaskLog("task", "phase", "first")
	queue.EnqueueTaskLog("task", "phase", "latest")
	queue.Stop()
	if len(writes) != 1 || writes[0] != "latest" {
		t.Fatalf("writes = %#v", writes)
	}
}

func TestWriteQueueStartStopAndRestart(t *testing.T) {
	queue := NewWriteQueue(WriteDependencies{}, log.New(io.Discard, "", 0))
	queue.debounce = time.Millisecond
	queue.Start()
	queue.Stop()
	queue.Start()
	queue.Stop()
	queue.Stop()
}

func TestWriteQueuePassesOpaqueMemoryEvent(t *testing.T) {
	var received interface{}
	queue := NewWriteQueue(WriteDependencies{
		SaveMemoryEvent: func(event interface{}) error {
			received = event
			return nil
		},
	}, log.New(io.Discard, "", 0))
	queue.EnqueueMemoryEvent("session", "agent", "event")
	queue.Stop()
	if received != "event" {
		t.Fatalf("received = %#v", received)
	}
}
