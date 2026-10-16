package app

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"mac/internal/conversation"
	"mac/internal/eventbus"
	"mac/internal/runtimeutil"
	"mac/internal/toolcontract"
)

// ====================== Store[T] Tests ======================

func TestStoreGetSet(t *testing.T) {
	s := runtimeutil.NewStore[string]("hello")
	if s.GetState() != "hello" {
		t.Error("initial state mismatch")
	}
	changed := s.SetState(func(prev string) string { return "world" })
	if !changed {
		t.Error("should report changed")
	}
	if s.GetState() != "world" {
		t.Error("updated state mismatch")
	}
}

func TestStoreNoopOnEqual(t *testing.T) {
	type item struct {
		Name  string
		Count int
	}
	s := runtimeutil.NewStore(item{Name: "a", Count: 1})
	changed := s.SetState(func(prev item) item { return item{Name: "a", Count: 1} })
	if changed {
		t.Error("should report no change for equal struct")
	}
}

func TestStoreSubscribe(t *testing.T) {
	s := runtimeutil.NewStore[int](0)
	var mu sync.Mutex
	var got []int
	unsub := s.Subscribe(func() { mu.Lock(); got = append(got, s.GetState()); mu.Unlock() })
	s.SetState(func(prev int) int { return 1 })
	s.SetState(func(prev int) int { return 2 })
	unsub()
	s.SetState(func(prev int) int { return 3 }) // should not fire
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 2 || got[0] != 1 || got[1] != 2 {
		t.Errorf("expected [1,2], got %v", got)
	}
}

func TestStoreOnChange(t *testing.T) {
	var oldVal, newVal int
	s := runtimeutil.NewStore[int](0, runtimeutil.WithOnChange[int](func(nv, ov int) { newVal = nv; oldVal = ov }))
	s.SetState(func(prev int) int { return 42 })
	if oldVal != 0 || newVal != 42 {
		t.Errorf("onChange: old=%d new=%d", oldVal, newVal)
	}
}

func TestKVStore(t *testing.T) {
	kv := runtimeutil.NewKVStore(map[string]interface{}{"a": 1})
	v, ok := kv.Get("a")
	if !ok || v.(int) != 1 {
		t.Error("get existing key failed")
	}
	kv.Set("b", "hello")
	v2, ok2 := kv.Get("b")
	if !ok2 || v2.(string) != "hello" {
		t.Error("set/get new key failed")
	}
	kv.Delete("a")
	if _, ok3 := kv.Get("a"); ok3 {
		t.Error("delete failed")
	}
}

// ====================== LazyTool Tests ======================

type testTool struct {
	callCount int
}

func (t *testTool) Name() string        { return "test_lazy" }
func (t *testTool) Description() string { return "test tool" }
func (t *testTool) Parameters() map[string]interface{} {
	t.callCount++
	return map[string]interface{}{"x": 1}
}
func (t *testTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	return "ok", nil
}

func TestLazyToolCaches(t *testing.T) {
	inner := &testTool{}
	lt := toolcontract.NewLazyTool(inner)
	p1 := lt.Parameters()
	p2 := lt.Parameters()
	if inner.callCount != 1 {
		t.Errorf("expected 1 call, got %d", inner.callCount)
	}
	if p1["x"].(int) != 1 || p2["x"].(int) != 1 {
		t.Error("params mismatch")
	}
}

func TestLazyToolInvalidate(t *testing.T) {
	inner := &testTool{}
	lt := toolcontract.NewLazyTool(inner)
	lt.Parameters()
	lt.Invalidate()
	lt.Parameters()
	if inner.callCount != 2 {
		t.Errorf("expected 2 calls after invalidate, got %d", inner.callCount)
	}
}

func TestLazyToolExecute(t *testing.T) {
	inner := &testTool{}
	lt := toolcontract.NewLazyTool(inner)
	result, err := lt.Execute(context.Background(), nil)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if result != "ok" {
		t.Errorf("expected 'ok', got %q", result)
	}
}

// ====================== ConditionalParamBuilder Tests ======================

func TestConditionalParamBuilder(t *testing.T) {
	cpb := toolcontract.NewConditionalParamBuilder()
	cpb.Add("query", "string", "search query", true, nil)
	cpb.AddIf(false, "hidden", "string", "should not appear", false, nil)
	cpb.AddIf(true, "visible", "string", "should appear", false, nil)
	schema := cpb.Build()
	if schema["type"] != "object" {
		t.Error("type mismatch")
	}
	props := schema["properties"].(map[string]interface{})
	if _, ok := props["hidden"]; ok {
		t.Error("hidden should not be in schema")
	}
	if _, ok := props["visible"]; !ok {
		t.Error("visible should be in schema")
	}
	req := schema["required"].([]string)
	if len(req) != 1 || req[0] != "query" {
		t.Errorf("required mismatch: %v", req)
	}
}

// ====================== FeatureGatedTool Tests ======================

func TestFeatureGatedTool(t *testing.T) {
	inner := &testTool{}
	flag := false
	fgt := toolcontract.NewFeatureGatedTool(inner, func() map[string]interface{} {
		if flag {
			return map[string]interface{}{"extra": true}
		}
		return map[string]interface{}{"basic": true}
	})
	p1 := fgt.Parameters()
	if _, ok := p1["extra"]; ok {
		t.Error("extra should not appear when flag is false")
	}
	flag = true
	p2 := fgt.Parameters()
	if _, ok := p2["extra"]; !ok {
		t.Error("extra should appear when flag is true")
	}
}

// ====================== BudgetTracker Tests ======================

func TestBudgetTrackerBasic(t *testing.T) {
	bt := runtimeutil.NewBudgetTracker(10000)
	bt.TrackTurn(3000, 2000) // 5000 total
	stats := bt.Stats()
	if stats["total_input"].(int) != 3000 {
		t.Error("input mismatch")
	}
	if stats["total"] != 5000 {
		t.Error("total mismatch")
	}
}

func TestBudgetTrackerDiminishingReturns(t *testing.T) {
	bt := runtimeutil.NewBudgetTracker(10000)
	// Simulate 3 tiny continuations (delta < 500)
	for i := 0; i < 4; i++ {
		bt.TrackTurn(100, 50)
		dec := bt.ShouldContinue(false)
		if i >= 3 && dec.Action != "stop" {
			t.Errorf("expected stop at iteration %d, got %s", i, dec.Action)
		}
		if i >= 3 && !dec.DiminishingFlag {
			t.Error("expected diminishing flag")
		}
	}
}

func TestBudgetTrackerSubAgentExcluded(t *testing.T) {
	bt := runtimeutil.NewBudgetTracker(10000)
	bt.TrackTurn(9500, 0)
	dec := bt.ShouldContinue(true) // isSubAgent=true
	if dec.Action != "stop" {
		t.Error("sub-agents should be excluded from continuation")
	}
}

func TestBudgetTrackerReset(t *testing.T) {
	bt := runtimeutil.NewBudgetTracker(10000)
	bt.TrackTurn(9000, 0)
	bt.Reset()
	stats := bt.Stats()
	if stats["total"] != 0 {
		t.Error("reset should clear counts")
	}
}

// ====================== PostCompactCleanup Tests ======================

func TestPostCompactCleanup(t *testing.T) {
	// CleanupSubAgent caches: cleared on BOTH main and sub-agent cleanup (shared state)
	// CleanupMainThread caches: cleared ONLY on main thread (dangerous for sub-agents)
	calls := 0
	cleanup := conversation.NewPostCompactCleanup()
	cleanup.Register("test_main_only", func() { calls++ }, conversation.CleanupMainThread)
	cleanup.Run(conversation.CleanupSubAgent)
	if calls != 0 {
		t.Error("sub-agent cleanup should NOT clear main-only caches")
	}
	cleanup.Run(conversation.CleanupMainThread)
	if calls != 1 {
		t.Errorf("main thread cleanup should clear main-only caches, got %d", calls)
	}
}

// ====================== Tool Orchestrator Tests ======================

func TestPartitionToolCallsReadOnly(t *testing.T) {
	reg := NewToolRegistry()
	reg.Register(newTestReadOnlyTool("read_file"))
	reg.Register(newTestReadOnlyTool("grep"))
	reg.Register(newTestReadOnlyTool("glob"))

	calls := []ToolCall{
		{Name: "read_file", ID: "1"},
		{Name: "grep", ID: "2"},
		{Name: "glob", ID: "3"},
	}
	batches := toolcontract.PartitionToolCalls(reg, calls, nil)
	if len(batches) != 1 {
		t.Fatalf("all read-only should produce 1 batch, got %d", len(batches))
	}
	if !batches[0].IsReadOnly {
		t.Error("batch should be read-only")
	}
	if len(batches[0].Blocks) != 3 {
		t.Errorf("batch should have 3 blocks, got %d", len(batches[0].Blocks))
	}
}

func TestPartitionToolCallsMixed(t *testing.T) {
	reg := NewToolRegistry()
	reg.Register(newTestReadOnlyTool("read_file"))
	reg.Register(newTestWriteTool("write_file"))

	calls := []ToolCall{
		{Name: "read_file", ID: "1"},
		{Name: "read_file", ID: "2"},
		{Name: "write_file", ID: "3"},
		{Name: "read_file", ID: "4"},
	}
	batches := toolcontract.PartitionToolCalls(reg, calls, nil)
	if len(batches) != 3 {
		t.Fatalf("expected 3 batches (read, write, read), got %d", len(batches))
	}
	if !batches[0].IsReadOnly || len(batches[0].Blocks) != 2 {
		t.Error("batch 0 should be 2 read tools")
	}
	if batches[1].IsReadOnly || len(batches[1].Blocks) != 1 {
		t.Error("batch 1 should be 1 write tool")
	}
	if !batches[2].IsReadOnly || len(batches[2].Blocks) != 1 {
		t.Error("batch 2 should be 1 read tool")
	}
}

func TestPartitionToolCallsEmpty(t *testing.T) {
	batches := toolcontract.PartitionToolCalls(nil, nil, nil)
	if len(batches) != 0 {
		t.Error("empty calls should produce empty batches")
	}
}

func TestBatchSummary(t *testing.T) {
	calls := []ToolCall{
		{Name: "read_file", ID: "1"},
		{Name: "write_file", ID: "2"},
	}
	s := toolcontract.BatchSummary(calls, nil)
	if !strings.Contains(s, "2 tools in") {
		t.Errorf("unexpected summary: %s", s)
	}
}

// ====================== Helpers ======================

type testReadTool struct {
	name string
}

func newTestReadTool(name string) *testReadTool            { return &testReadTool{name: name} }
func newTestReadOnlyTool(name string) *testReadTool        { return &testReadTool{name: name} }
func (t *testReadTool) Name() string                       { return t.name }
func (t *testReadTool) Description() string                { return "read tool" }
func (t *testReadTool) Parameters() map[string]interface{} { return nil }
func (t *testReadTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	return "", nil
}
func (t *testReadTool) IsReadOnly() bool { return true }

type testWriteTool struct {
	name string
}

func newTestWriteTool(name string) *testWriteTool           { return &testWriteTool{name: name} }
func (t *testWriteTool) Name() string                       { return t.name }
func (t *testWriteTool) Description() string                { return "write tool" }
func (t *testWriteTool) Parameters() map[string]interface{} { return nil }
func (t *testWriteTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	return "", nil
}
func (t *testWriteTool) IsReadOnly() bool { return false }

// ====================== SSEEventBuilder Tests ======================

func TestSSEEventBuilder(t *testing.T) {
	b := eventbus.NewSSEEventBuilder("task-123")
	evt := b.TextDelta("hello")
	if evt.Type != eventbus.SSETextDelta || evt.TaskID != "task-123" || evt.Content != "hello" {
		t.Error("text delta event mismatch")
	}
	if evt.Timestamp.IsZero() {
		t.Error("timestamp should not be zero")
	}
}

func TestSSEEventToJSON(t *testing.T) {
	b := eventbus.NewSSEEventBuilder("t1")
	evt := b.ToolResult("grep", "call-1", "found 5 matches", 100*time.Millisecond)
	jsonStr := evt.ToJSON()
	if !strings.HasPrefix(jsonStr, "data: ") || !strings.Contains(jsonStr, "tool_result") {
		t.Errorf("invalid SSE JSON: %s", truncate(jsonStr, 80))
	}
}

func TestSSEEventMerge(t *testing.T) {
	b := eventbus.NewSSEEventBuilder("t2")
	evt := b.APIError(429, "rate limited", true)
	if evt.Type != eventbus.SSEAPIError {
		t.Error("type mismatch")
	}
	if evt.Metadata["status_code"].(int) != 429 {
		t.Error("status code mismatch")
	}
}

// ====================== EventMultiplexer Tests ======================

func TestEventMultiplexer(t *testing.T) {
	em := eventbus.NewEventMultiplexer()
	ch := make(chan eventbus.SSEEvent, 10)
	unsub := em.Subscribe("test", ch)
	defer unsub()

	em.Emit(eventbus.NewSSEEventBuilder("t1").TextDelta("hello"))

	select {
	case evt := <-ch:
		if evt.Content != "hello" {
			t.Error("content mismatch")
		}
	case <-time.After(time.Second):
		t.Error("timeout waiting for event")
	}
}

func TestEventMultiplexerDropSlowConsumer(t *testing.T) {
	em := eventbus.NewEventMultiplexer()
	ch := make(chan eventbus.SSEEvent) // unbuffered, will drop
	em.Subscribe("slow", ch)
	// This should not block
	em.Emit(eventbus.NewSSEEventBuilder("t1").TextDelta("dropped"))
	// Clean up
	em.Subscribe("slow", nil)
}

// ====================== PromptCacheBuilder Tests ======================

func TestPromptCacheBuilder(t *testing.T) {
	pcb := runtimeutil.NewPromptCacheBuilder("You are an AI assistant.")
	pcb.SetToolSchemas("- read_file: reads files\n- write_file: writes files")
	checksum := pcb.Checksum()
	if checksum == "" {
		t.Error("checksum should not be empty")
	}
	parts := pcb.Build()
	if parts.StaticPrefix.Text == "" {
		t.Error("static prefix should not be empty")
	}
	if !strings.Contains(parts.StaticPrefix.Text, "AI assistant") {
		t.Error("static prefix missing base prompt")
	}
	if !strings.Contains(parts.StaticPrefix.Text, "read_file") {
		t.Error("static prefix missing tool schemas")
	}
}

func TestToolSchemaCache(t *testing.T) {
	tsc := &runtimeutil.ToolSchemaCache{}
	calls := 0
	gen := func() string { calls++; return "schema-v1" }
	r1 := tsc.Get("v1", gen)
	r2 := tsc.Get("v1", gen)
	if calls != 1 {
		t.Error("should cache and not re-generate")
	}
	if r1 != "schema-v1" || r2 != "schema-v1" {
		t.Error("return value mismatch")
	}
	tsc.Invalidate()
	r3 := tsc.Get("v2", gen)
	if calls != 2 {
		t.Error("should regenerate after invalidate")
	}
	if r3 != "schema-v1" {
		t.Error("post-invalidate mismatch")
	}
}

func TestSplitSystemPrompt(t *testing.T) {
	fullPrompt := "You are AI.\n\n## Git Status\nOn branch main\n\n## Workspace\n/home/user/project"
	parts := runtimeutil.SplitSystemPrompt(fullPrompt)
	if parts.StaticPrefix.Text == "" {
		t.Error("static prefix empty")
	}
	if !strings.Contains(parts.DynamicSuffix.Text, "On branch main") {
		t.Error("dynamic suffix should contain git status")
	}
}

// ====================== Circuit Breaker Tests ======================

func TestCircuitBreakerAuthErrorNotCounted(t *testing.T) {
	cb := runtimeutil.NewCircuitBreaker(3, 2, time.Second)
	for i := 0; i < 10; i++ {
		cb.RecordFailure(401) // auth errors should not count
	}
	if cb.State() != runtimeutil.CircuitClosed {
		t.Errorf("auth errors should not open breaker, got %s", cb.State())
	}
}

func TestCircuitBreakerHalfOpenSingleProbe(t *testing.T) {
	cb := runtimeutil.NewCircuitBreaker(2, 1, 100*time.Millisecond)
	cb.RecordFailure(500)
	cb.RecordFailure(500)
	if cb.State() != runtimeutil.CircuitOpen {
		t.Fatal("should be open after 2 failures")
	}
	time.Sleep(150 * time.Millisecond)
	// Allow() triggers the Open→HalfOpen transition when timeout expires
	if err := cb.Allow(); err != nil {
		t.Fatalf("first probe (transition) should be allowed: %v", err)
	}
	if cb.State() != runtimeutil.CircuitHalfOpen {
		t.Fatalf("should be half-open after first Allow, got %s", cb.State())
	}
	// Second probe blocked (probeInFlight already set)
	if err := cb.Allow(); err == nil {
		t.Error("second probe should be blocked while first in flight")
	}
	cb.RecordSuccess()
	if cb.State() != runtimeutil.CircuitClosed {
		t.Fatalf("should be closed after success, got %s", cb.State())
	}
	// After success, breaker closes, next Allow should succeed
	if err := cb.Allow(); err != nil {
		t.Errorf("after success, Allow should succeed: %v", err)
	}
}

func TestCircuitBreakerHalfOpenAllowsRequiredSuccessProbes(t *testing.T) {
	cb := runtimeutil.NewCircuitBreaker(1, 2, time.Millisecond)
	cb.RecordFailure(500)
	time.Sleep(2 * time.Millisecond)
	if err := cb.Allow(); err != nil {
		t.Fatalf("first half-open probe should be allowed: %v", err)
	}
	cb.RecordSuccess()
	if cb.State() != runtimeutil.CircuitHalfOpen {
		t.Fatalf("one of two successful probes should remain half-open, got %s", cb.State())
	}
	if err := cb.Allow(); err != nil {
		t.Fatalf("second half-open probe should be allowed after first completes: %v", err)
	}
	if err := cb.Allow(); err == nil {
		t.Fatal("concurrent half-open probe should be rejected")
	}
	cb.RecordSuccess()
	if cb.State() != runtimeutil.CircuitClosed {
		t.Fatalf("two successful probes should close breaker, got %s", cb.State())
	}
}

// ====================== Concurrent Safety Tests ======================

func TestStoreConcurrentAccess(t *testing.T) {
	s := runtimeutil.NewStore[int](0)
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.SetState(func(prev int) int { return prev + 1 })
		}()
	}
	wg.Wait()
	if s.GetState() != 100 {
		t.Errorf("concurrent increments: expected 100, got %d", s.GetState())
	}
}

func TestEventMultiplexerConcurrency(t *testing.T) {
	em := eventbus.NewEventMultiplexer()
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		ch := make(chan eventbus.SSEEvent, 5)
		em.Subscribe(string(rune('a'+i%26))+string(rune('0'+i/26)), ch)
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-ch
		}()
	}
	// Emit from multiple goroutines
	for i := 0; i < 10; i++ {
		go func() { em.Emit(eventbus.NewSSEEventBuilder("t").TextDelta("x")) }()
	}
	time.Sleep(100 * time.Millisecond)
	wg.Wait()
}

// ====================== Benchmark Tests ======================

func BenchmarkStoreSet(b *testing.B) {
	s := runtimeutil.NewStore[int](0)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s.SetState(func(prev int) int { return prev + 1 })
	}
}

func BenchmarkLazyToolParams(b *testing.B) {
	lt := toolcontract.NewLazyTool(&testTool{})
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		lt.Parameters()
	}
}

func BenchmarkPartitionToolCalls(b *testing.B) {
	calls := []ToolCall{
		{Name: "read_file", ID: "1"},
		{Name: "write_file", ID: "2"},
		{Name: "grep", ID: "3"},
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		toolcontract.PartitionToolCalls(nil, calls, nil)
	}
}
