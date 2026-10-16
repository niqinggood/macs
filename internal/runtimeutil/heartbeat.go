package runtimeutil

import (
	"context"
	"sync"
	"time"
)

const DefaultProgressHeartbeatInterval = 10 * time.Second

// StartProgressHeartbeat emits low-cost liveness updates while a blocking
// model or tool call is in flight. Callbacks are expected to be non-blocking.
func StartProgressHeartbeat(ctx context.Context, interval time.Duration, emit func(time.Duration)) func() {
	if emit == nil || interval <= 0 {
		return func() {}
	}
	if ctx == nil {
		ctx = context.Background()
	}
	heartbeatCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	started := time.Now()
	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-heartbeatCtx.Done():
				return
			case <-ticker.C:
				emit(time.Since(started))
			}
		}
	}()

	var once sync.Once
	return func() {
		once.Do(func() {
			cancel()
			<-done
		})
	}
}
