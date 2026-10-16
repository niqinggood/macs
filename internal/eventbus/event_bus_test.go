package eventbus

import (
	"testing"
	"time"
)

func TestBusFiltersAndUnsubscribes(t *testing.T) {
	bus := New(nil)
	received := make(chan Event, 1)
	unsubscribe := bus.Subscribe(TaskCompleted, func(event Event) { received <- event })
	bus.Emit(Event{Type: TaskStarted})
	bus.Emit(Event{Type: TaskCompleted, TaskID: "task-1"})
	select {
	case event := <-received:
		if event.TaskID != "task-1" || event.Timestamp.IsZero() {
			t.Fatalf("unexpected event: %+v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("matching event was not delivered")
	}
	unsubscribe()
	if bus.SubscriberCount() != 0 {
		t.Fatalf("subscriber count = %d, want 0", bus.SubscriberCount())
	}
}
