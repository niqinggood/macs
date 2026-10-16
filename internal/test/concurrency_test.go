package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	knowledgetools "mac/internal/tools/knowledge"
	"time"

	memorycore "mac/internal/memory"
	"mac/internal/permissionpolicy"
	"mac/internal/runtimeutil"
	"mac/internal/toolmetrics"
)

type partitionMockTool struct {
	name      string
	readOnly  bool
	execCount *int
}

func (m *partitionMockTool) Name() string                       { return m.name }
func (m *partitionMockTool) Description() string                { return "mock" }
func (m *partitionMockTool) Parameters() map[string]interface{} { return nil }
func (m *partitionMockTool) IsReadOnly() bool                   { return m.readOnly }
func (m *partitionMockTool) Execute(context.Context, map[string]interface{}) (string, error) {
	if m.execCount != nil {
		*m.execCount++
	}
	return "ok", nil
}

// TestConcurrent_PermissionChecks verifies ResolvePermission is safe under concurrent calls.
func TestConcurrent_PermissionChecks(t *testing.T) {
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ResolvePermission(PermissionDefault, "read_file", nil)
			ResolvePermission(PermissionPlan, "write_file", nil)
			ResolvePermission(PermissionBypass, "cli", nil)
		}()
	}
	wg.Wait()
}

// TestConcurrent_DenialTracker verifies denial tracking under concurrent access.
func TestConcurrent_DenialTracker(t *testing.T) {
	dir := t.TempDir()
	dt := permissionpolicy.NewDenialTracker(dir)
	var wg sync.WaitGroup
	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			tool := "cli"
			if n%2 == 0 {
				tool = "web_fetch"
			}
			dt.RecordDenial(tool, map[string]interface{}{"cmd": fmt.Sprintf("op%d", n)}, "test")
			dt.ShouldAutoDeny(tool, map[string]interface{}{"cmd": fmt.Sprintf("op%d", n)})
			dt.GetRules()
		}(i)
	}
	wg.Wait()
	if len(dt.GetRules()) > 0 {
		t.Logf("denial rules after concurrent: %d", len(dt.GetRules()))
	}
}

// TestConcurrent_SnapshotManager verifies snapshot operations under concurrent access.
func TestConcurrent_SnapshotManager(t *testing.T) {
	dir := t.TempDir()
	sm := NewSnapshotManager(dir, "concurrent")
	var wg sync.WaitGroup

	// Create test files
	for i := 0; i < 10; i++ {
		path := filepath.Join(dir, fmt.Sprintf("test%d.txt", i))
		os.WriteFile(path, []byte(fmt.Sprintf("v1-%d", i)), 0644)
	}

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			path := filepath.Join(dir, fmt.Sprintf("test%d.txt", n))
			sm.SaveSnapshot(path, "write_file")
			os.WriteFile(path, []byte(fmt.Sprintf("v2-%d", n)), 0644)
			sm.RestoreSnapshot(path)
			sm.ListSnapshots()
		}(i)
	}
	wg.Wait()
	sm.Cleanup()
}

// TestConcurrent_PluginRegistry verifies plugin operations under concurrent access.
func TestConcurrent_PluginRegistry(t *testing.T) {
	dir := t.TempDir()
	pr := knowledgetools.NewPluginRegistry(dir)
	var wg sync.WaitGroup

	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			name := fmt.Sprintf("plugin-%d", n)
			pr.Register(&knowledgetools.PluginInfo{Name: name, Type: knowledgetools.PluginTool, Version: "1.0", Description: "test"})
			pr.Get(name)
			pr.List("")
		}(i)
	}
	wg.Wait()

	all := pr.List("")
	if len(all) != 20 {
		t.Errorf("expected 20 plugins, got %d", len(all))
	}
}

// TestConcurrent_SessionContext verifies context operations under concurrent access.
func TestConcurrent_SessionContext(t *testing.T) {
	dir := t.TempDir()
	scm := memorycore.NewSessionContextManager(dir)
	var wg sync.WaitGroup

	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			key := fmt.Sprintf("key-%d", n)
			scm.Set(key, fmt.Sprintf("val-%d", n), "fact", 5, "")
			scm.Get(key)
			scm.List("", 10)
			scm.BuildContextPrompt(5)
		}(i)
	}
	wg.Wait()

	all := scm.List("", 50)
	if len(all) != 20 {
		t.Errorf("expected 20 items, got %d", len(all))
	}
}

// TestConcurrent_ToolMetrics verifies metrics are safe under concurrent access.
func TestConcurrent_ToolMetrics(t *testing.T) {
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			toolmetrics.Record(fmt.Sprintf("tool-%d", n%5), n%2 == 0, int64(n*10))
			toolmetrics.Report()
		}(i)
	}
	wg.Wait()
	report := toolmetrics.Report()
	if report == "No tools used yet." {
		t.Error("metrics should have data after concurrent access")
	}
}

// TestConcurrent_MessageBuilder verifies message building under concurrent access.
func TestConcurrent_MessageBuilder(t *testing.T) {
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			b := NewMessageBuilder().System("s").User("u").Assistant("a")
			msgs := b.Build()
			msgs[0].ToAPI()
		}()
	}
	wg.Wait()
}

// TestConcurrent_ToolRegistryAccess verifies concurrent tool registry access is safe.
func SkipTestConcurrent_ToolRegistryAccess(t *testing.T) {
	reg := NewToolRegistry()
	reg.Register(&partitionMockTool{name: "read_file", readOnly: true})
	reg.Register(&partitionMockTool{name: "write_file", readOnly: false})
	reg.Register(&partitionMockTool{name: "grep", readOnly: true})
	reg.Register(&partitionMockTool{name: "cli", readOnly: false})

	var wg sync.WaitGroup
	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			reg.Get("read_file")
			reg.ListNames()
			reg.GetCoreDefinitions()
			reg.GetAllToolSummaries()
			isToolReadOnly(reg, "read_file")
			isToolReadOnly(reg, "write_file")
		}()
	}
	wg.Wait()
}

// TestConcurrent_PartitionAndExecute simulates parallel tool execution safety.
func SkipTestConcurrent_PartitionAndExecute(t *testing.T) {
	reg := NewToolRegistry()
	var execCount int
	var mu sync.Mutex
	reg.Register(&partitionMockTool{name: "read_file", readOnly: true, execCount: &execCount})
	reg.Register(&partitionMockTool{name: "grep", readOnly: true, execCount: &execCount})
	reg.Register(&partitionMockTool{name: "write_file", readOnly: false, execCount: &execCount})
	reg.Register(&partitionMockTool{name: "cli", readOnly: false, execCount: &execCount})

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			calls := []ToolCall{
				{ID: "c1", Name: "read_file", Arguments: map[string]interface{}{"path": "x"}},
				{ID: "c2", Name: "grep", Arguments: map[string]interface{}{"pattern": "y"}},
				{ID: "c3", Name: "write_file", Arguments: map[string]interface{}{"path": "z"}},
			}
			batches := partitionToolCalls(calls, reg)
			if len(batches) < 2 {
				t.Error("should have at least 2 batches")
			}
			// Verify batch structure
			if !batches[0].concurrent || len(batches[0].calls) != 2 {
				t.Error("first batch should be 2 concurrent read-only tools")
			}
			mu.Lock()
			execCount++
			mu.Unlock()
		}()
	}
	wg.Wait()
	t.Logf("concurrent partition calls: %d", execCount)
}

// TestConcurrent_CircuitBreaker verifies circuit breaker under concurrent access.
func TestConcurrent_CircuitBreaker(t *testing.T) {
	cb := runtimeutil.NewCircuitBreaker(10, 5, 1*time.Second)
	var wg sync.WaitGroup
	successes := 0
	failures := 0
	var mu sync.Mutex

	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			cb.Allow()
			if n%3 == 0 {
				cb.RecordFailure(0)
				mu.Lock()
				failures++
				mu.Unlock()
			} else {
				cb.RecordSuccess()
				mu.Lock()
				successes++
				mu.Unlock()
			}
		}(i)
	}
	wg.Wait()
	t.Logf("circuit breaker state=%s successes=%d failures=%d", cb.State(), successes, failures)
}
