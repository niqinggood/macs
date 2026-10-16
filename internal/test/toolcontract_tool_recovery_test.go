package app

import (
	"strings"
	"testing"
)

func TestToolRecoveryTrackerRejectsPrematureFinishAndClearsOnChangedEdit(t *testing.T) {
	tracker := NewToolRecoveryTracker()
	failed := ToolCall{
		Name: "edit_file",
		Arguments: map[string]interface{}{
			"file_path":  `src\app.go`,
			"old_string": "stale",
			"new_string": "new",
		},
	}
	directive := ToolRecoveryDirective{
		Code:          "edit_stale_context",
		ResourceKey:   `file:C:\work\src\app.go`,
		SuggestedTool: "read_file",
		Arguments:     map[string]interface{}{"path": `src\app.go`, "lines_range": "10-20"},
		Instruction:   "refresh then retry",
		MaxPrompts:    2,
	}
	tracker.Observe(failed, ToolResult{Recovery: &directive, ErrorCode: directive.Code}, false)

	prompt, ok := tracker.NextControlMessage()
	if !ok {
		t.Fatal("recoverable edit miss should block a premature final response")
	}
	for _, want := range []string{"edit_stale_context", "read_file", "Never repeat", "1/2"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("recovery prompt missing %q:\n%s", want, prompt)
		}
	}

	tracker.Observe(ToolCall{Name: "read_file", Arguments: map[string]interface{}{"path": `src\app.go`}}, ToolResult{}, true)
	prompt, ok = tracker.NextControlMessage()
	if !ok || !strings.Contains(prompt, "refresh has completed") {
		t.Fatalf("read refresh should retain and advance the edit obligation: %q", prompt)
	}

	tracker.Observe(ToolCall{Name: "edit_file", Arguments: map[string]interface{}{
		"file_path": `src\app.go`, "old_string": "current", "new_string": "new",
	}}, ToolResult{}, true)
	if tracker.HasPending() {
		t.Fatal("a successful changed edit should resolve the recovery obligation")
	}
	if _, ok := tracker.NextControlMessage(); ok {
		t.Fatal("resolved recovery must not inject another prompt")
	}
}

func TestToolRecoveryTrackerBoundsPrompts(t *testing.T) {
	tracker := NewToolRecoveryTracker()
	directive := ToolRecoveryDirective{Code: "edit_stale_context", SuggestedTool: "read_file", MaxPrompts: 1}
	tracker.Observe(ToolCall{Name: "edit_file"}, ToolResult{Recovery: &directive}, false)
	if _, ok := tracker.NextControlMessage(); !ok {
		t.Fatal("first bounded prompt missing")
	}
	if _, ok := tracker.NextControlMessage(); ok {
		t.Fatal("recovery prompts must be bounded")
	}
}
