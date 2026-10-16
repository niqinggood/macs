package agentloop

import (
	"context"
	"strings"
	"testing"
)

type planToolRuntime struct {
	plan       *ActivePlan
	inPlanMode bool
}

func (runtime *planToolRuntime) SetActivePlan(plan *ActivePlan) { runtime.plan = plan }
func (runtime *planToolRuntime) GetActivePlan() *ActivePlan     { return runtime.plan }
func (runtime *planToolRuntime) EnterPlanMode()                 { runtime.inPlanMode = true }
func (runtime *planToolRuntime) ExitPlanMode()                  { runtime.inPlanMode = false }
func (runtime *planToolRuntime) IsInPlanMode() bool             { return runtime.inPlanMode }

func TestPlanToolsCreateAndUpdateTrackedPlan(t *testing.T) {
	runtime := &planToolRuntime{}
	create := NewPlanTaskTool(runtime)
	output, err := create.Execute(context.Background(), map[string]interface{}{
		"steps": []interface{}{
			map[string]interface{}{"title": "Collect", "desc": "Collect evidence"},
			map[string]interface{}{"title": "Deliver", "desc": "Write result"},
		},
	})
	if err != nil || runtime.plan == nil || len(runtime.plan.Steps) != 2 || !strings.Contains(output, "Collect") {
		t.Fatalf("plan creation failed: output=%q plan=%#v err=%v", output, runtime.plan, err)
	}

	emitted := ""
	update := NewUpdatePlanStepTool(runtime, func(_ context.Context, stepID, status, title string) {
		emitted = stepID + ":" + status + ":" + title
	})
	output, err = update.Execute(context.Background(), map[string]interface{}{"step_id": "step_1", "status": "completed"})
	if err != nil || runtime.plan.Steps[0].Status != PlanStepCompleted || emitted != "step_1:completed:Collect" || !strings.Contains(output, "Collect") {
		t.Fatalf("plan update failed: output=%q emitted=%q plan=%#v err=%v", output, emitted, runtime.plan, err)
	}
}

func TestPlanModeToolsUseRuntimeBoundary(t *testing.T) {
	runtime := &planToolRuntime{}
	entered, err := NewEnterPlanModeTool(runtime).Execute(context.Background(), map[string]interface{}{"description": "inspect"})
	if err != nil || !runtime.inPlanMode || !strings.Contains(entered, "Plan Mode Activated") {
		t.Fatalf("enter plan mode failed: %q err=%v", entered, err)
	}
	plan := "Goal: refactor\nFiles: a.go\nSteps: migrate\nTest: go test\nRisks: compatibility"
	exited, err := NewExitPlanModeTool(runtime).Execute(context.Background(), map[string]interface{}{"plan": plan})
	if err != nil || runtime.inPlanMode || !strings.Contains(exited, "Plan mode ended") || strings.Contains(exited, "AWAITING USER APPROVAL") {
		t.Fatalf("exit plan mode failed: %q err=%v", exited, err)
	}
}

func TestValidatePlanCompletenessReportsMissingSections(t *testing.T) {
	issues := ValidatePlanCompleteness("only an idea")
	if len(issues) < 4 {
		t.Fatalf("incomplete plan accepted: %#v", issues)
	}
	complete := ValidatePlanCompleteness("Goal Files Steps Test Risks")
	if len(complete) != 1 || !strings.Contains(complete[0], "appears complete") {
		t.Fatalf("complete plan rejected: %#v", complete)
	}
}

func TestPlanModeExitScalesToTaskWithoutInventingApproval(t *testing.T) {
	runtime := &planToolRuntime{inPlanMode: true}
	plan := "修复文件名拼写，并运行相关测试。"
	result, err := NewExitPlanModeTool(runtime).Execute(context.Background(), map[string]interface{}{"plan": plan})
	if err != nil || runtime.inPlanMode || !strings.Contains(result, plan) || strings.Contains(result, "Missing:") || strings.Contains(result, "AWAITING USER APPROVAL") {
		t.Fatalf("unnecessary plan gate: %q %v", result, err)
	}
	if !strings.Contains(result, "If the user requested review") {
		t.Fatal("explicit user review requirement was omitted")
	}
}
