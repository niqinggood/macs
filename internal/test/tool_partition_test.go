package app

import "testing"

func TestPartitionToolCalls_AllReadOnly(t *testing.T) {
	reg := NewToolRegistry()
	reg.Register(&partitionMockTool{name: "read_file", readOnly: true})
	reg.Register(&partitionMockTool{name: "grep", readOnly: true})
	reg.Register(&partitionMockTool{name: "glob", readOnly: true})

	calls := []ToolCall{
		{ID: "1", Name: "read_file", Arguments: map[string]interface{}{"path": "a.txt"}},
		{ID: "2", Name: "grep", Arguments: map[string]interface{}{"pattern": "foo"}},
		{ID: "3", Name: "glob", Arguments: map[string]interface{}{"pattern": "*.go"}},
	}

	batches := partitionToolCalls(calls, reg)
	if len(batches) != 1 {
		t.Fatalf("expected 1 batch, got %d", len(batches))
	}
	if !batches[0].concurrent {
		t.Error("all read-only batch should be concurrent")
	}
	if len(batches[0].calls) != 3 {
		t.Errorf("expected 3 calls in batch, got %d", len(batches[0].calls))
	}
}

func TestPartitionToolCalls_ReadFileSamePathStaysConcurrent(t *testing.T) {
	reg := NewToolRegistry()
	reg.Register(NewReadFileTool(`C:\repo`))

	batches := partitionToolCalls([]ToolCall{
		{ID: "1", Name: "read_file", Arguments: map[string]interface{}{"path": `src\a.txt`}},
		{ID: "2", Name: "read_file", Arguments: map[string]interface{}{"path": `src\a.txt`}},
	}, reg)

	if len(batches) != 1 || !batches[0].concurrent || len(batches[0].calls) != 2 {
		t.Fatalf("same-path read_file calls should stay in one concurrent batch, got %#v", batches)
	}
}

func TestPartitionToolCalls_AllSideEffect(t *testing.T) {
	reg := NewToolRegistry()
	reg.Register(&partitionMockTool{name: "write_file", readOnly: false})
	reg.Register(&partitionMockTool{name: "cli", readOnly: false})

	calls := []ToolCall{
		{ID: "1", Name: "write_file", Arguments: map[string]interface{}{"path": "a.txt"}},
		{ID: "2", Name: "cli", Arguments: map[string]interface{}{"command": "ls"}},
	}

	batches := partitionToolCalls(calls, reg)
	if len(batches) != 2 {
		t.Fatalf("expected 2 batch, got %d", len(batches))
	}
	if !batches[0].concurrent || len(batches[0].calls) != 1 {
		t.Errorf("path-scoped write_file can run as a concurrent-safe singleton, got concurrent=%v len=%d", batches[0].concurrent, len(batches[0].calls))
	}
	if batches[1].concurrent || len(batches[1].calls) != 1 {
		t.Errorf("cli should remain serial, got concurrent=%v len=%d", batches[1].concurrent, len(batches[1].calls))
	}
}

func TestPartitionToolCalls_Mixed(t *testing.T) {
	reg := NewToolRegistry()
	reg.Register(&partitionMockTool{name: "read_file", readOnly: true})
	reg.Register(&partitionMockTool{name: "grep", readOnly: true})
	reg.Register(&partitionMockTool{name: "write_file", readOnly: false})
	reg.Register(&partitionMockTool{name: "read_file", readOnly: true})

	calls := []ToolCall{
		{ID: "1", Name: "read_file", Arguments: map[string]interface{}{"path": "a.txt"}},
		{ID: "2", Name: "grep", Arguments: map[string]interface{}{"pattern": "foo"}},
		{ID: "3", Name: "write_file", Arguments: map[string]interface{}{"path": "b.txt"}},
		{ID: "4", Name: "read_file", Arguments: map[string]interface{}{"path": "c.txt"}},
	}

	batches := partitionToolCalls(calls, reg)
	// Expected: all calls are safe to run together because paths do not overlap.
	if len(batches) != 1 {
		t.Fatalf("expected 1 concurrent-safe batch, got %d", len(batches))
	}

	if !batches[0].concurrent || len(batches[0].calls) != 4 {
		t.Errorf("batch 0: concurrent=%v len=%d, want concurrent=true len=4", batches[0].concurrent, len(batches[0].calls))
	}
}

func TestPartitionToolCalls_PathOverlapSplitsBatch(t *testing.T) {
	reg := NewToolRegistry()
	reg.Register(&partitionMockTool{name: "write_file", readOnly: false})

	calls := []ToolCall{
		{ID: "1", Name: "write_file", Arguments: map[string]interface{}{"path": `src/app`}},
		{ID: "2", Name: "write_file", Arguments: map[string]interface{}{"path": `src/app/main.go`}},
	}

	batches := partitionToolCalls(calls, reg)
	if len(batches) != 2 {
		t.Fatalf("overlapping paths should split into 2 batches, got %d", len(batches))
	}
	for i, b := range batches {
		if !b.concurrent || len(b.calls) != 1 {
			t.Errorf("batch %d should be a concurrent-safe singleton, got concurrent=%v len=%d", i, b.concurrent, len(b.calls))
		}
	}
}

func TestLLMAgentParallelSafeUsesSharedPartitioning(t *testing.T) {
	reg := NewToolRegistry()
	reg.Register(&partitionMockTool{name: "write_file", readOnly: false})
	agent := &LLMAgent{toolRegistry: reg}

	if !agent.areToolsParallelSafe([]ToolCall{
		{ID: "1", Name: "write_file", Arguments: map[string]interface{}{"path": `src/a.go`}},
		{ID: "2", Name: "write_file", Arguments: map[string]interface{}{"path": `src/b.go`}},
	}) {
		t.Fatal("non-overlapping path-scoped writes should be considered parallel-safe")
	}

	if agent.areToolsParallelSafe([]ToolCall{
		{ID: "1", Name: "write_file", Arguments: map[string]interface{}{"path": `src`}},
		{ID: "2", Name: "write_file", Arguments: map[string]interface{}{"path": `src/b.go`}},
	}) {
		t.Fatal("overlapping path-scoped writes should not be considered parallel-safe")
	}
}

func TestIsToolReadOnly(t *testing.T) {
	reg := NewToolRegistry()
	reg.Register(&partitionMockTool{name: "read_file", readOnly: true})
	reg.Register(&partitionMockTool{name: "write_file", readOnly: false})

	if !isToolReadOnly(reg, "read_file") {
		t.Error("read_file should be readonly via interface")
	}
	if isToolReadOnly(reg, "write_file") {
		t.Error("write_file should not be readonly")
	}
	// Fallback to static check
	if !isToolReadOnly(reg, "grep") {
		t.Error("grep should be readonly via isReadOnlyTool fallback")
	}
}

func TestPartitionToolCalls_Empty(t *testing.T) {
	reg := NewToolRegistry()
	batches := partitionToolCalls(nil, reg)
	if len(batches) != 0 {
		t.Errorf("empty calls should produce 0 batches, got %d", len(batches))
	}
}

func TestPartitionToolCalls_SingleReadOnly(t *testing.T) {
	reg := NewToolRegistry()
	reg.Register(&partitionMockTool{name: "list_dir", readOnly: true})

	calls := []ToolCall{
		{ID: "1", Name: "list_dir", Arguments: map[string]interface{}{"path": "."}},
	}
	batches := partitionToolCalls(calls, reg)
	if len(batches) != 1 || !batches[0].concurrent {
		t.Error("single read-only should be one concurrent batch")
	}
}

func TestPartitionToolCalls_SingleSideEffect(t *testing.T) {
	reg := NewToolRegistry()
	reg.Register(&partitionMockTool{name: "cli", readOnly: false})

	calls := []ToolCall{
		{ID: "1", Name: "cli", Arguments: map[string]interface{}{"command": "echo hi"}},
	}
	batches := partitionToolCalls(calls, reg)
	if len(batches) != 1 || batches[0].concurrent {
		t.Error("single side-effect should be one serial batch")
	}
}
