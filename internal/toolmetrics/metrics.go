// Package toolmetrics records process-local tool usage statistics.
package toolmetrics

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

type metric struct {
	toolName string
	calls    int
	fails    int
	totalMs  int64
	lastUsed time.Time
}

var global = struct {
	mu      sync.Mutex
	metrics map[string]*metric
}{metrics: make(map[string]*metric)}

func Reset() {
	global.mu.Lock()
	defer global.mu.Unlock()
	global.metrics = make(map[string]*metric)
}

func Record(name string, success bool, durationMs int64) {
	global.mu.Lock()
	defer global.mu.Unlock()
	current, ok := global.metrics[name]
	if !ok {
		current = &metric{toolName: name}
		global.metrics[name] = current
	}
	current.calls++
	if !success {
		current.fails++
	}
	current.totalMs += durationMs
	current.lastUsed = time.Now()
}

func Report() string {
	global.mu.Lock()
	defer global.mu.Unlock()
	if len(global.metrics) == 0 {
		return "No tools used yet."
	}
	var result strings.Builder
	result.WriteString("Tool Usage Report:")
	for name, current := range global.metrics {
		failRate := float64(current.fails) / float64(current.calls) * 100
		averageMs := current.totalMs / int64(current.calls)
		result.WriteString(fmt.Sprintf("\n  %s: %d calls, %.0f%% fail, avg %dms", name, current.calls, failRate, averageMs))
	}
	return result.String()
}
