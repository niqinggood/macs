package runtimeutil

import (
	"context"
	"testing"
	"time"
)

func TestStartProgressHeartbeatEmitsAndStops(t *testing.T) {
	emitted := make(chan time.Duration, 2)
	stop := StartProgressHeartbeat(context.Background(), 10*time.Millisecond, func(elapsed time.Duration) {
		emitted <- elapsed
	})
	select {
	case elapsed := <-emitted:
		if elapsed <= 0 {
			t.Fatalf("elapsed = %v, want positive", elapsed)
		}
	case <-time.After(time.Second):
		t.Fatal("heartbeat did not emit")
	}
	stop()
	stop()
}

func TestStartProgressHeartbeatHonorsContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	stop := StartProgressHeartbeat(ctx, 10*time.Millisecond, func(time.Duration) {
		t.Error("heartbeat emitted after cancellation")
	})
	stop()
}
