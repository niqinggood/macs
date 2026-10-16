package app

import "testing"

func TestPipelineDiagnosisDetectsMissingTool(t *testing.T) {
	exec := &PipelineExecution{
		ID:           "exec-tool-missing",
		PipelineName: "test",
		Status:       "failed",
		Steps: []StepResult{{
			Name:   "collect",
			Status: "failed",
			Error:  "工具 write_file 不存在",
		}},
	}

	view := NewPipelineExecutionView(exec)
	if view.Delivery == nil || view.Delivery.Diagnosis == nil {
		t.Fatal("expected delivery diagnosis")
	}
	if view.Delivery.Diagnosis.Status != "attention" {
		t.Fatalf("expected attention diagnosis, got %q", view.Delivery.Diagnosis.Status)
	}
	if len(view.Delivery.Diagnosis.Causes) == 0 {
		t.Fatal("expected at least one cause")
	}
	if view.Delivery.Diagnosis.Causes[0].Code != "tool_missing" {
		t.Fatalf("expected tool_missing, got %q", view.Delivery.Diagnosis.Causes[0].Code)
	}
}

func TestPipelineDiagnosisDetectsIterationLimit(t *testing.T) {
	exec := &PipelineExecution{
		ID:           "exec-iteration",
		PipelineName: "test",
		Status:       "completed",
		Steps: []StepResult{{
			Name:   "research",
			Status: "completed",
			Error:  "iteration_budget_degraded",
			Output: "Task reached iteration limit. Unable to generate summary.",
		}},
	}

	view := NewPipelineExecutionView(exec)
	if view.Delivery == nil || view.Delivery.Diagnosis == nil {
		t.Fatal("expected delivery diagnosis")
	}
	if !view.Delivery.Diagnosis.IterationLimited {
		t.Fatal("expected iteration limited flag")
	}
	found := false
	for _, cause := range view.Delivery.Diagnosis.Causes {
		if cause.Code == "iteration_limit" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected iteration_limit cause, got %#v", view.Delivery.Diagnosis.Causes)
	}
}

func TestPipelineDiagnosisDetectsCompletedWithPendingStepsWithoutReason(t *testing.T) {
	exec := &PipelineExecution{
		ID:           "exec-incomplete",
		PipelineName: "investment-advisory",
		Status:       "completed",
		Steps: []StepResult{
			{Name: "judge", Status: "completed", Output: `{"task_type":"single_stock"}`},
			{Name: "collect", Status: "completed", Output: "partial output"},
			{Name: "research", Status: "pending"},
			{Name: "report", Status: "pending"},
		},
	}

	view := NewPipelineExecutionView(exec)
	if view.Delivery == nil || view.Delivery.Diagnosis == nil {
		t.Fatal("expected delivery diagnosis")
	}
	if view.Delivery.Diagnosis.Status != "attention" {
		t.Fatalf("expected attention diagnosis, got %q", view.Delivery.Diagnosis.Status)
	}
	if view.Delivery.RiskLevel != "high" {
		t.Fatalf("expected high risk for incomplete completion, got %q", view.Delivery.RiskLevel)
	}
	found := false
	for _, cause := range view.Delivery.Diagnosis.Causes {
		if cause.Code == "incomplete_completed" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected incomplete_completed cause, got %#v", view.Delivery.Diagnosis.Causes)
	}
}

func TestPipelineDiagnosisAllowsRecordedControlStopWithPendingSteps(t *testing.T) {
	exec := &PipelineExecution{
		ID:           "exec-control-stop",
		PipelineName: "investment-advisory",
		Status:       "completed",
		CompletionReason: &PipelineCompletionReason{
			Code:      "control_stop",
			StepIndex: 1,
			StepName:  "collect",
			Condition: "contains '<!--PIPELINE_STOP-->'",
			Target:    "__stop__",
		},
		Steps: []StepResult{
			{Name: "judge", Status: "completed", Output: `{"task_type":"market"}`},
			{Name: "collect", Status: "completed", Output: "market brief"},
			{Name: "research", Status: "pending"},
		},
	}

	view := NewPipelineExecutionView(exec)
	if view.Delivery == nil || view.Delivery.Diagnosis == nil {
		t.Fatal("expected delivery diagnosis")
	}
	for _, cause := range view.Delivery.Diagnosis.Causes {
		if cause.Code == "incomplete_completed" {
			t.Fatalf("recorded control stop should not be diagnosed as incomplete completion: %#v", view.Delivery.Diagnosis.Causes)
		}
	}
	if view.Delivery.CompletionReason == nil || view.Delivery.CompletionReason.Code != "control_stop" {
		t.Fatalf("expected delivery completion reason, got %#v", view.Delivery.CompletionReason)
	}
}
