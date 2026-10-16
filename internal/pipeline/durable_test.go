package pipeline

import "testing"

func TestPipelineDurableResumePoint(t *testing.T) {
	pipeline := NewPipelineDurable()
	pipeline.Checkpoint("p1", 0, "init", "completed", "ok", "")
	pipeline.Checkpoint("p1", 1, "build", "completed", "ok", "")
	pipeline.Checkpoint("p1", 2, "deploy", "completed", "ok", "")
	if got := pipeline.GetResumePoint("p1"); got != -1 {
		t.Fatalf("completed pipeline resume point = %d, want -1", got)
	}

	failed := NewPipelineDurable()
	failed.Checkpoint("p2", 0, "a", "completed", "ok", "")
	failed.Checkpoint("p2", 1, "b", "failed", "", "timeout")
	if got := failed.GetResumePoint("p2"); got != 1 {
		t.Fatalf("failed pipeline resume point = %d, want 1", got)
	}

	fresh := NewPipelineDurable()
	if got := fresh.GetResumePoint("p3"); got != 0 {
		t.Fatalf("fresh pipeline resume point = %d, want 0", got)
	}
}

func TestNewDurablePipelineToolProvidesState(t *testing.T) {
	tool := NewDurablePipelineTool(nil)
	if tool.durable == nil {
		t.Fatal("constructor did not initialize durable state")
	}
}
