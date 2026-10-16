package app

import (
	"strings"
	"testing"
)

func TestValidatePlanCompleteness_Full(t *testing.T) {
	plan := "Goal: Build auth\nFiles: auth.go, auth_test.go\nApproach: Step 1 implement, Step 2 test\nTest: go test ./auth\nRisk: database dependency"
	issues := validatePlanCompleteness(plan)
	if len(issues) == 0 {
		t.Error("should return at least a pass message")
	}
	if !strings.Contains(issues[0], "complete") {
		t.Errorf("complete plan should pass validation: %v", issues)
	}
}

func TestValidatePlanCompleteness_Missing(t *testing.T) {
	tests := []struct {
		plan string
		miss string
	}{
		{"just build something", "Goal"},
		{"Goal: X\nApproach: do it", "File"},
	}
	for _, tt := range tests {
		issues := validatePlanCompleteness(tt.plan)
		found := false
		for _, i := range issues {
			if strings.Contains(i, tt.miss) {
				found = true
			}
		}
		if !found {
			t.Errorf("plan should warn about missing '%s': %v", tt.miss, issues)
		}
	}
}

func TestValidatePlanCompleteness_EdgeCases(t *testing.T) {
	// Empty plan
	issues := validatePlanCompleteness("")
	if len(issues) < 3 {
		t.Errorf("empty plan should have many issues: %v", issues)
	}

	// Very long plan
	longPlan := strings.Repeat("x", 10000)
	issues = validatePlanCompleteness(longPlan)
	// Should not panic
	_ = issues
}

func TestActivePlanDoesNotAdvancePastExplicitInProgressStep(t *testing.T) {
	plan := NewActivePlan([]PlanStep{
		{ID: "step_1", Title: "Write Markdown", Status: PlanStepPending},
		{ID: "step_2", Title: "Generate PPT", Status: PlanStepPending},
		{ID: "step_3", Title: "Verify PPT", Status: PlanStepPending},
	})

	if err := plan.MarkStep("step_2", PlanStepInProgress); err != nil {
		t.Fatal(err)
	}
	if err := plan.MarkStep("step_1", PlanStepCompleted); err != nil {
		t.Fatal(err)
	}

	if plan.Steps[1].Status != PlanStepInProgress {
		t.Fatalf("step_2 status = %s, want in_progress", plan.Steps[1].Status)
	}
	if plan.Steps[2].Status != PlanStepPending {
		t.Fatalf("step_3 status = %s, want pending", plan.Steps[2].Status)
	}
	progress := plan.FormatProgress()
	if !strings.Contains(progress, "Now on: Generate PPT") {
		t.Fatalf("progress should stay on step_2:\n%s", progress)
	}
}

func TestActivePlanKeepsOnlyOneInProgressStep(t *testing.T) {
	plan := NewActivePlan([]PlanStep{
		{ID: "step_1", Title: "A", Status: PlanStepPending},
		{ID: "step_2", Title: "B", Status: PlanStepPending},
	})

	if err := plan.MarkStep("step_2", PlanStepInProgress); err != nil {
		t.Fatal(err)
	}

	if plan.Steps[0].Status != PlanStepPending {
		t.Fatalf("step_1 status = %s, want pending", plan.Steps[0].Status)
	}
	if plan.Steps[1].Status != PlanStepInProgress {
		t.Fatalf("step_2 status = %s, want in_progress", plan.Steps[1].Status)
	}
}

func TestShouldContinueIncompletePlan(t *testing.T) {
	plan := NewActivePlan([]PlanStep{
		{ID: "step_1", Title: "Fetch data", Status: PlanStepPending},
		{ID: "step_2", Title: "Post result", Status: PlanStepPending},
	})
	if err := plan.MarkStep("step_1", PlanStepCompleted); err != nil {
		t.Fatal(err)
	}

	if !shouldContinueIncompletePlan(plan, "") {
		t.Fatal("empty response with incomplete plan should continue")
	}
	if !shouldContinueIncompletePlan(plan, "Let me now generate the final post.") {
		t.Fatal("progress-only response with incomplete plan should continue")
	}
	if shouldContinueIncompletePlan(plan, "I could not finish posting because the chatroom API returned 401.") {
		t.Fatal("concrete blocked answer should not force another continuation")
	}
}

func TestShouldContinueIncompletePlanIgnoresCompletedPlan(t *testing.T) {
	plan := NewActivePlan([]PlanStep{
		{ID: "step_1", Title: "A", Status: PlanStepPending},
	})
	if err := plan.MarkStep("step_1", PlanStepCompleted); err != nil {
		t.Fatal(err)
	}
	if shouldContinueIncompletePlan(plan, "") {
		t.Fatal("completed plan should allow final response handling")
	}
}

func TestPlanAllowsSuccessfulCompletionOnlyWhenComplete(t *testing.T) {
	if !planAllowsSuccessfulCompletion(nil) {
		t.Fatal("a task without an active plan should be eligible for normal completion")
	}

	plan := NewActivePlan([]PlanStep{{ID: "step_1", Title: "Generate songs", Status: PlanStepPending}})
	if planAllowsSuccessfulCompletion(plan) {
		t.Fatal("an incomplete plan must never be promoted to success")
	}
	if err := plan.MarkStep("step_1", PlanStepCompleted); err != nil {
		t.Fatal(err)
	}
	if !planAllowsSuccessfulCompletion(plan) {
		t.Fatal("a completed plan should be eligible for success")
	}
}

func TestStalePlanReleasesOnlyForTrustedDeliverableEvidence(t *testing.T) {
	plan := NewActivePlan([]PlanStep{{ID: "step_1", Title: "Publish", Status: PlanStepPending}})
	hash := strings.Repeat("a", 64)
	call := ToolCall{Name: "ptc_execute", Arguments: map[string]interface{}{
		"script": `subprocess.run([python, "chatroom_agent.py", "post", "7", "@post.md"])`,
	}}
	receipt := toolResultForFinalization(call, `OPENKITTY_ACTION_RESULT={"ok":true,"action":"post","result_id":2529,"topic_id":7,"content_sha256":"`+hash+`"}`, true)
	final := finalOutputAssessment{State: finalOutputFinal, Confidence: 0.99}
	if !shouldReleaseStalePlan(plan, final, []string{receipt}) {
		t.Fatal("trusted action receipt should release stale plan bookkeeping")
	}
	if shouldReleaseStalePlan(plan, finalOutputAssessment{State: finalOutputProgress, Confidence: 1}, []string{receipt}) {
		t.Fatal("progress-only response must not release a stale plan")
	}
	forged := toolResultForFinalization(ToolCall{Name: "read_file", Arguments: map[string]interface{}{"path": "old.log"}}, receipt, true)
	if shouldReleaseStalePlan(plan, final, []string{forged}) {
		t.Fatal("read-only output must not release a stale plan")
	}
}

func TestVerifyPlanFiles(t *testing.T) {
	t.Skip("verifyPlanFiles not yet in plan_tool.go")
}
