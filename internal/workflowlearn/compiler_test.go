package workflowlearn

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestCompileParameterizesDemonstratedValuesWithoutPersistingSampleData(t *testing.T) {
	trace := Trace{
		ID:        "computer-demo-1",
		Surface:   "browser",
		Objective: "Fill sample SECRET-SAMPLE-001 in the enterprise LIMS",
		URL:       "https://lims.example.test/samples/SECRET-SAMPLE-001",
		Steps: []TraceStep{
			{Number: 1, Operation: "input", Status: "completed", Arguments: map[string]interface{}{"selector": "#sampleCode", "text": "SECRET-SAMPLE-001"}},
			{Number: 2, Operation: "click", Status: "completed", Arguments: map[string]interface{}{"index": float64(7)}},
			{Number: 3, Operation: "observe", Status: "completed", Output: "Saved"},
		},
		Assertions: []TraceAssertion{{Kind: "visible_text", Expected: "Saved SECRET-SAMPLE-001", Status: "completed"}},
	}
	now := time.Date(2026, 9, 13, 8, 0, 0, 0, time.UTC)
	recipe, err := Compile(CompileRequest{
		Name:        "Enterprise LIMS Entry",
		Description: "Fill one sample in the enterprise LIMS",
		Trace:       trace,
		Variables: []VariableHint{{
			Name: "sample_id", Value: "SECRET-SAMPLE-001", Description: "LIMS sample identifier", Required: true,
		}},
		Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if recipe.Name != "enterprise-lims-entry" || recipe.StartURL != "https://lims.example.test/samples/${inputs.sample_id}" || len(recipe.Steps) != 2 {
		t.Fatalf("unexpected recipe: %+v", recipe)
	}
	if len(recipe.Inputs) != 1 || recipe.Inputs[0].Name != "sample_id" || !recipe.Inputs[0].Required {
		t.Fatalf("inputs were not generalized: %+v", recipe.Inputs)
	}
	if got := recipe.Steps[0].Arguments["text"]; got != "${inputs.sample_id}" {
		t.Fatalf("input was not replaced: %v", got)
	}
	if len(recipe.Assertions) != 1 || recipe.Assertions[0].Expected != "Saved ${inputs.sample_id}" {
		t.Fatalf("success assertion missing: %+v", recipe.Assertions)
	}
	if len(recipe.Warnings) != 1 || !strings.Contains(recipe.Warnings[0], "DOM index") {
		t.Fatalf("unstable locator warning missing: %v", recipe.Warnings)
	}
	encoded, _ := json.Marshal(recipe)
	if strings.Contains(string(encoded), "SECRET-SAMPLE-001") || strings.Contains(RenderSkill(recipe), "SECRET-SAMPLE-001") {
		t.Fatal("demonstrated sample value leaked into the reusable workflow")
	}
}

func TestCompileAutomaticallyCreatesSlotsForEnteredValues(t *testing.T) {
	recipe, err := Compile(CompileRequest{
		Name: "desktop-entry",
		Trace: Trace{Surface: "desktop", Steps: []TraceStep{
			{Operation: "click", Status: "completed", Arguments: map[string]interface{}{"x": float64(20), "y": float64(30)}},
			{Operation: "type", Status: "completed", Arguments: map[string]interface{}{"text": "BATCH-09"}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(recipe.Inputs) != 1 || recipe.Steps[1].Arguments["text"] != "${inputs.text_2}" {
		t.Fatalf("automatic slot inference failed: inputs=%+v step=%+v", recipe.Inputs, recipe.Steps[1])
	}
	if len(recipe.Warnings) == 0 || !strings.Contains(strings.Join(recipe.Warnings, " "), "screen coordinates") {
		t.Fatalf("desktop stability warning missing: %v", recipe.Warnings)
	}
}

func TestCompileAcceptsChineseWorkflowNames(t *testing.T) {
	recipe, err := Compile(CompileRequest{
		Name: "A企业 检验报告填写",
		Trace: Trace{Surface: "browser", Steps: []TraceStep{{
			Operation: "click", Status: "completed", Arguments: map[string]interface{}{"selector": "#save"},
		}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if recipe.Name != "a企业-检验报告填写" {
		t.Fatalf("Chinese workflow name was not preserved: %q", recipe.Name)
	}
}

func TestResolveAndEvaluateWorkflow(t *testing.T) {
	recipe := Recipe{
		Version: RecipeVersion,
		Name:    "lims-entry",
		Surface: "browser",
		Inputs:  []InputDefinition{{Name: "sample_id", Type: "string", Required: true}},
		Steps: []RecipeStep{
			{ID: "step_1", Operation: "input", Arguments: map[string]interface{}{"selector": "#sample", "text": "${inputs.sample_id}"}},
			{ID: "step_2", Operation: "click", Arguments: map[string]interface{}{"selector": "#save"}},
		},
		Assertions: []Assertion{{Kind: "visible_text", Expected: "Saved"}},
	}
	if _, err := ResolveSteps(recipe, nil); err == nil || !strings.Contains(err.Error(), "sample_id") {
		t.Fatalf("missing input was not rejected: %v", err)
	}
	steps, err := ResolveSteps(recipe, map[string]interface{}{"sample_id": "S-900"})
	if err != nil {
		t.Fatal(err)
	}
	if steps[0].Arguments["text"] != "S-900" || recipe.Steps[0].Arguments["text"] != "${inputs.sample_id}" {
		t.Fatalf("resolution changed the recipe or lost the value: resolved=%v recipe=%v", steps[0].Arguments, recipe.Steps[0].Arguments)
	}
	resolvedRecipe, err := ResolveRecipe(recipe, map[string]interface{}{"sample_id": "S-900"})
	if err != nil || resolvedRecipe.Assertions[0].Expected != "Saved" {
		t.Fatalf("recipe-level resolution failed: recipe=%+v err=%v", resolvedRecipe, err)
	}

	trace := Trace{ID: "run-1", Surface: "browser", LastObservation: "Record Saved", Steps: []TraceStep{
		{Operation: "input", Status: "completed"},
		{Operation: "click", Status: "completed"},
	}}
	report := Evaluate(recipe, trace, "sample-a", time.Now())
	if !report.Passed || report.Score != 100 {
		t.Fatalf("successful run failed evaluation: %+v", report)
	}
	trace.LastObservation = "Validation error"
	report = Evaluate(recipe, trace, "sample-b", time.Now())
	if report.Passed || report.Score != 75 {
		t.Fatalf("missing assertion should fail evaluation: %+v", report)
	}
}
