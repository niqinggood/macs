package skills

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mac/internal/workflowlearn"
)

func TestWorkflowLearnerSavesReplaysAndEvaluates(t *testing.T) {
	skillsDir := t.TempDir()
	demonstration := workflowlearn.Trace{
		ID: "demo-1", Surface: "browser", Objective: "Enter a LIMS sample", URL: "https://lims.test/form",
		Steps: []workflowlearn.TraceStep{
			{Operation: "input", Status: "completed", Arguments: map[string]interface{}{"selector": "#sample", "text": "PRIVATE-SAMPLE-1"}},
			{Operation: "click", Status: "completed", Arguments: map[string]interface{}{"selector": "#save"}},
		},
		Assertions: []workflowlearn.TraceAssertion{{Kind: "visible_text", Expected: "Saved", Status: "completed"}},
	}
	successfulReplay := workflowlearn.Trace{
		ID: "replay-1", Surface: "browser", LastObservation: "Saved",
		Steps: []workflowlearn.TraceStep{
			{Operation: "input", Status: "completed"},
			{Operation: "click", Status: "completed"},
		},
		Assertions: []workflowlearn.TraceAssertion{{Kind: "visible_text", Expected: "Saved", Status: "completed"}},
	}
	encodeTrace := func(trace workflowlearn.Trace) string {
		data, err := json.Marshal(trace)
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	var replayCall map[string]interface{}
	tool := NewWorkflowLearnerTool(skillsDir, WorkflowLearnerRuntime{
		LoadComputerSession: func(sessionID string) (string, error) {
			if sessionID == "demo-1" {
				return encodeTrace(demonstration), nil
			}
			return encodeTrace(successfulReplay), nil
		},
		ExecuteComputer: func(_ context.Context, params map[string]interface{}) (string, error) {
			replayCall = params
			return `{"ok":true,"session":{"id":"replay-1"}}`, nil
		},
	})

	saveOut, err := tool.Execute(context.Background(), map[string]interface{}{
		"action":      "save",
		"name":        "lims-sample-entry",
		"description": "Fill a sample form in LIMS",
		"session_id":  "demo-1",
		"variables": []interface{}{
			map[string]interface{}{"name": "sample_id", "value": "PRIVATE-SAMPLE-1", "description": "Sample identifier"},
		},
	})
	if err != nil {
		t.Fatalf("save failed: %v", err)
	}
	if !strings.Contains(saveOut, `"saved": true`) {
		t.Fatalf("save response=%s", saveOut)
	}
	for _, name := range []string{"recipe.json", "SKILL.md", "_workflow_meta.json"} {
		data, err := os.ReadFile(filepath.Join(skillsDir, "lims-sample-entry", name))
		if err != nil {
			t.Fatalf("%s missing: %v", name, err)
		}
		if strings.Contains(string(data), "PRIVATE-SAMPLE-1") {
			t.Fatalf("private demonstration value persisted in %s", name)
		}
	}

	replayOut, err := tool.Execute(context.Background(), map[string]interface{}{
		"action": "replay", "name": "lims-sample-entry",
		"inputs":      map[string]interface{}{"sample_id": "S-NEW-88"},
		"save_report": false,
	})
	if err != nil {
		t.Fatalf("replay failed: %v", err)
	}
	steps := replayCall["steps"].([]interface{})
	first := steps[0].(map[string]interface{})
	if first["text"] != "S-NEW-88" || replayCall["expect_text"] != "Saved" {
		t.Fatalf("replay did not resolve inputs/assertions: call=%+v", replayCall)
	}
	if !strings.Contains(replayOut, `"passed": true`) || !strings.Contains(replayOut, `"score": 100`) {
		t.Fatalf("replay evaluation failed: %s", replayOut)
	}

	if _, err := tool.Execute(context.Background(), map[string]interface{}{
		"action": "save", "name": "lims-sample-entry", "session_id": "demo-1",
	}); err == nil || !strings.Contains(err.Error(), "overwrite=true") {
		t.Fatalf("existing workflow was overwritten without an explicit revision: %v", err)
	}
	if _, err := tool.Execute(context.Background(), map[string]interface{}{
		"action": "save", "name": "lims-sample-entry", "session_id": "demo-1", "overwrite": true,
	}); err != nil {
		t.Fatalf("revision save failed: %v", err)
	}
	recipe, err := tool.loadRecipe("lims-sample-entry")
	if err != nil || recipe.Revision != 2 {
		t.Fatalf("recipe revision=%d err=%v", recipe.Revision, err)
	}
}

func TestWorkflowLearnerIsPromptHiddenButDiscoverableByMetadata(t *testing.T) {
	tool := NewWorkflowLearnerTool(t.TempDir(), WorkflowLearnerRuntime{})
	metadata := tool.ToolSearchMetadata()
	if tool.Name() != "workflow_learner" || len(metadata.Aliases) == 0 || len(metadata.Tags) == 0 {
		t.Fatalf("workflow learner discovery metadata missing: %+v", metadata)
	}
	if !tool.IsReadOnlyForCall(map[string]interface{}{"action": "preview"}) || tool.IsReadOnlyForCall(map[string]interface{}{"action": "replay"}) {
		t.Fatal("workflow learner call mutability classification is incorrect")
	}
}
