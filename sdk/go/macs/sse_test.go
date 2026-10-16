package macs

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestWatchTaskParsesSSE(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/agent/tasks/agt_42/events" || r.URL.Query().Get("client_id") != "lims" {
			t.Errorf("URL = %s", r.URL.String())
			return
		}
		if got := r.Header.Get("Last-Event-ID"); got != "prior-event" {
			t.Errorf("Last-Event-ID = %q", got)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, ": heartbeat\n\nevent: progress\nid: e-1\nretry: 250\ndata: {\"step\":\ndata: \"mapping\"}\n\n")
	}))
	defer server.Close()
	client, err := NewClient(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	var events []Event
	err = client.WatchTaskWithOptions(context.Background(), "agt_42", WatchOptions{
		ClientID: "lims", LastEventID: "prior-event",
	}, func(event Event) error {
		events = append(events, event)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Type != "progress" || events[0].ID != "e-1" || events[0].Retry != 250*time.Millisecond {
		t.Fatalf("events = %#v", events)
	}
	var payload map[string]string
	if err := events[0].Decode(&payload); err != nil || payload["step"] != "mapping" {
		t.Fatalf("payload = %#v, err = %v", payload, err)
	}
}

func TestWatchTaskReturnsHandlerError(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {}\n\n")
	}))
	defer server.Close()
	client, _ := NewClient(server.URL)
	want := errors.New("stop")
	err := client.WatchTask(context.Background(), "agt_42", "", func(Event) error { return want })
	if !errors.Is(err, want) {
		t.Fatalf("error = %v", err)
	}
}

func TestWatchTaskRejectsNonEventStreamResponse(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"status":"running"}`)
	}))
	defer server.Close()
	client, _ := NewClient(server.URL)
	err := client.WatchTask(context.Background(), "agt_42", "", func(Event) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "application/json") {
		t.Fatalf("error = %v", err)
	}
}

func TestProgressFractionAndTypedDecode(t *testing.T) {
	t.Parallel()
	event := Event{
		Type: "progress",
		Data: []byte(`{"type":"tool_start","task_id":"agt_42","step_name":"Extract","step_index":2,"step_total":5,"message":"Reading files"}`),
	}
	progress, matched, err := event.DecodeProgress()
	if err != nil || !matched {
		t.Fatalf("progress=%#v matched=%v err=%v", progress, matched, err)
	}
	fraction, known := progress.Fraction()
	if !known || fraction != 0.4 {
		t.Fatalf("fraction=%v known=%v", fraction, known)
	}
	if progress.StepName != "Extract" || progress.Message != "Reading files" {
		t.Fatalf("progress=%#v", progress)
	}
}

func TestWaitTaskFallsBackToDurablePolling(t *testing.T) {
	t.Parallel()
	var gets atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/v1/agent/tasks/agt_42/events" {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = fmt.Fprint(w, "event: progress\ndata: {\"phase\":\"model\"}\n\n")
			return
		}
		if r.URL.Path != "/api/v1/agent/tasks/agt_42" {
			http.NotFound(w, r)
			return
		}
		count := gets.Add(1)
		status := "running"
		if count >= 3 {
			status = "completed"
		}
		_, _ = fmt.Fprintf(w, `{"task_id":"agt_42","status":%q,"created_at":"2026-09-13T00:00:00Z","links":{}}`, status)
	}))
	defer server.Close()
	client, _ := NewClient(server.URL)
	var eventCount atomic.Int32
	var progressCount atomic.Int32
	var snapshotCount atomic.Int32
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	task, err := client.WaitTask(ctx, "agt_42", WaitOptions{
		PollInterval: time.Millisecond,
		OnEvent: func(Event) error {
			eventCount.Add(1)
			return nil
		},
		OnProgress: func(progress Progress) error {
			if progress.Type != "" || progress.TaskID != "" {
				t.Fatalf("unexpected progress payload: %#v", progress)
			}
			progressCount.Add(1)
			return nil
		},
		OnSnapshot: func(Task) error {
			snapshotCount.Add(1)
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if task.Status != "completed" || eventCount.Load() != 1 || progressCount.Load() != 1 || gets.Load() < 3 || snapshotCount.Load() < 3 {
		t.Fatalf("task=%#v events=%d progress=%d snapshots=%d gets=%d", task, eventCount.Load(), progressCount.Load(), snapshotCount.Load(), gets.Load())
	}
}
