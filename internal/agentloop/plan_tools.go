package agentloop

import (
	"context"
	"fmt"
	"strings"
)

// PlanRuntime is the narrow host boundary needed by the four planning tools.
// It keeps tool behavior independent from the concrete application agent.
type PlanRuntime interface {
	SetActivePlan(*ActivePlan)
	GetActivePlan() *ActivePlan
	EnterPlanMode()
	ExitPlanMode()
	IsInPlanMode() bool
}

type PlanProgressEmitter func(ctx context.Context, stepID, status, title string)

type PlanTaskTool struct {
	runtime PlanRuntime
}

func NewPlanTaskTool(runtime PlanRuntime) *PlanTaskTool {
	return &PlanTaskTool{runtime: runtime}
}

func (t *PlanTaskTool) Name() string { return "plan_task" }

func (t *PlanTaskTool) Description() string {
	return "Create a structured execution plan for the current task. " +
		"Use it only when several dependent phases genuinely benefit from tracked state; skip it for short tasks and for read-only research that ends in one answer, even when that answer needs several sources or fields. " +
		"Steps may analyze, act, or verify and do not need to create files. " +
		"Include a file path only when that step genuinely creates or modifies the file. " +
		"The engine will track your progress and remind you of the current step after each tool call."
}

func (t *PlanTaskTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"steps": map[string]interface{}{
				"type":        "array",
				"description": "Ordered list of meaningful dependent steps. Do not create files solely to satisfy the plan.",
				"items": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"title": map[string]interface{}{
							"type":        "string",
							"description": "Action title with deliverable, e.g. 'Write approval engine (scripts/approval/engine.ts)'",
						},
						"desc": map[string]interface{}{
							"type":        "string",
							"description": "What this step accomplishes and the observable evidence that will show it is done.",
						},
					},
					"required": []string{"title", "desc"},
				},
			},
		},
		"required": []string{"steps"},
	}
}

func (t *PlanTaskTool) Execute(_ context.Context, params map[string]interface{}) (string, error) {
	stepsRaw, ok := params["steps"].([]interface{})
	if !ok || len(stepsRaw) == 0 {
		return "Error: 'steps' must be a non-empty array of {title, desc} objects.", nil
	}

	steps := make([]PlanStep, 0, len(stepsRaw))
	for index, raw := range stepsRaw {
		values, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		title, _ := values["title"].(string)
		description, _ := values["desc"].(string)
		if title == "" {
			title = fmt.Sprintf("Step %d", index+1)
		}
		steps = append(steps, PlanStep{
			ID:          fmt.Sprintf("step_%d", index+1),
			Title:       title,
			Description: description,
			Status:      PlanStepPending,
		})
	}

	plan := NewActivePlan(steps)
	if t.runtime != nil {
		t.runtime.SetActivePlan(plan)
	}
	var output strings.Builder
	output.WriteString(fmt.Sprintf("Plan '%s' created with %d steps:\n\n", plan.PlanID, len(steps)))
	for index, step := range plan.Steps {
		icon := "⬜"
		if step.Status == PlanStepInProgress {
			icon = "🔄"
		}
		output.WriteString(fmt.Sprintf("%s Step %d: %s — %s\n", icon, index+1, step.Title, step.Description))
	}
	output.WriteString("\nBegin executing Step 1 now. Mark each step complete with update_plan_step(step_id, status=\"completed\").")
	return output.String(), nil
}

type UpdatePlanStepTool struct {
	runtime PlanRuntime
	emit    PlanProgressEmitter
}

func NewUpdatePlanStepTool(runtime PlanRuntime, emitters ...PlanProgressEmitter) *UpdatePlanStepTool {
	tool := &UpdatePlanStepTool{runtime: runtime}
	if len(emitters) > 0 {
		tool.emit = emitters[0]
	}
	return tool
}

func (t *UpdatePlanStepTool) Name() string { return "update_plan_step" }

func (t *UpdatePlanStepTool) Description() string {
	return "Mark a plan step as completed, in_progress, or skipped. " +
		"Use this only when plan_task created an active plan; never create plan bookkeeping for a single read-only research answer. " +
		"Example: update_plan_step(step_id=\"step_1\", status=\"completed\")"
}

func (t *UpdatePlanStepTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"step_id": map[string]interface{}{
				"type":        "string",
				"description": "Step ID to update, e.g. 'step_1', 'step_2'",
			},
			"status": map[string]interface{}{
				"type":        "string",
				"description": "New status",
				"enum":        []string{"in_progress", "completed", "skipped"},
			},
		},
		"required": []string{"step_id", "status"},
	}
}

func (t *UpdatePlanStepTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	stepID, _ := params["step_id"].(string)
	statusText, _ := params["status"].(string)
	if stepID == "" || statusText == "" {
		return "Error: step_id and status are required.", nil
	}

	var status PlanStepStatus
	switch statusText {
	case "in_progress":
		status = PlanStepInProgress
	case "completed":
		status = PlanStepCompleted
	case "skipped":
		status = PlanStepSkipped
	default:
		return fmt.Sprintf("Invalid status '%s'. Use: in_progress, completed, skipped.", statusText), nil
	}
	if t.runtime == nil {
		return "No active plan. Create one with plan_task first.", nil
	}
	plan := t.runtime.GetActivePlan()
	if plan == nil {
		return "No active plan. Create one with plan_task first.", nil
	}
	if err := plan.MarkStep(stepID, status); err != nil {
		return fmt.Sprintf("Error: %v", err), nil
	}

	stepTitle := stepID
	_, steps := plan.Snapshot()
	for _, step := range steps {
		if step.ID == stepID {
			stepTitle = step.Title
			break
		}
	}
	if t.emit != nil {
		t.emit(ctx, stepID, statusText, stepTitle)
	}
	return plan.FormatProgress(), nil
}

type EnterPlanModeTool struct {
	runtime PlanRuntime
}

func NewEnterPlanModeTool(runtime PlanRuntime) *EnterPlanModeTool {
	return &EnterPlanModeTool{runtime: runtime}
}

func (t *EnterPlanModeTool) Name() string { return "enter_plan_mode" }

func (t *EnterPlanModeTool) Description() string {
	return "Enter plan mode. In plan mode, only read-only tools are allowed. " +
		"Use it when a task benefits from a separate investigation and planning phase; routine authorized work does not require it. " +
		"After gathering enough information, call exit_plan_mode with the plan. Wait for review only if the user requested it."
}

func (t *EnterPlanModeTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"description": map[string]interface{}{
				"type":        "string",
				"description": "Brief description of what you plan to investigate or build.",
			},
		},
		"required": []string{"description"},
	}
}

func (t *EnterPlanModeTool) Execute(_ context.Context, params map[string]interface{}) (string, error) {
	if t.runtime == nil {
		return "Error: agent not available", nil
	}
	t.runtime.EnterPlanMode()
	description, _ := params["description"].(string)
	if description == "" {
		description = "investigation and planning"
	}
	return fmt.Sprintf(`## Plan Mode Activated: %s

Use read-only tools and read-only commands to inspect relevant files and constraints.
Record a plan proportional to the task: intended changes and how to verify them.
Call exit_plan_mode when the investigation is complete. Continue work already authorized by the user; wait if the user requested plan review.
Do not modify files or external state while plan mode is active. Configured tool restrictions still apply.`, description), nil
}

type ExitPlanModeTool struct {
	runtime PlanRuntime
}

func NewExitPlanModeTool(runtime PlanRuntime) *ExitPlanModeTool {
	return &ExitPlanModeTool{runtime: runtime}
}

func (t *ExitPlanModeTool) Name() string { return "exit_plan_mode" }

func (t *ExitPlanModeTool) Description() string {
	return "Exit plan mode and record the implementation plan. " +
		"Summarize intended changes and validation, with detail appropriate to the task. " +
		"Continue work already authorized by the user. If the user requested review before implementation, present the plan and wait; configured tool approvals still apply."
}

func (t *ExitPlanModeTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"plan": map[string]interface{}{
				"type":        "string",
				"description": "The implementation plan: intended changes and how to verify them. Scale detail to the task.",
			},
		},
		"required": []string{"plan"},
	}
}

func (t *ExitPlanModeTool) Execute(_ context.Context, params map[string]interface{}) (string, error) {
	if t.runtime == nil {
		return "Error: agent not available", nil
	}
	if !t.runtime.IsInPlanMode() {
		return "Not in plan mode. Call enter_plan_mode first.", nil
	}
	planText, _ := params["plan"].(string)
	if planText == "" {
		return "Error: plan content is required. Provide a detailed implementation plan.", nil
	}
	t.runtime.ExitPlanMode()
	return fmt.Sprintf("## Implementation Plan\n\n%s\n\nPlan mode ended. Continue within the user's existing authorization. If the user requested review before implementation, present this plan and wait. Configured tool approvals still apply.", planText), nil
}

func ValidatePlanCompleteness(plan string) []string {
	var issues []string
	lower := strings.ToLower(plan)
	if !strings.Contains(lower, "goal") && !strings.Contains(lower, "target") && !strings.Contains(lower, "objective") {
		issues = append(issues, "Missing: Goal/Objective section")
	}
	if !strings.Contains(lower, "file") && !strings.Contains(lower, "path") {
		issues = append(issues, "Missing: File paths to modify")
	}
	if !strings.Contains(lower, "step") && !strings.Contains(lower, "approach") {
		issues = append(issues, "Missing: Step-by-step approach")
	}
	if !strings.Contains(lower, "test") && !strings.Contains(lower, "verify") && !strings.Contains(lower, "check") {
		issues = append(issues, "Missing: Testing/Verification strategy")
	}
	if !strings.Contains(lower, "risk") && !strings.Contains(lower, "alternative") {
		issues = append(issues, "Consider adding: Risks and alternatives")
	}
	if len(issues) == 0 {
		issues = append(issues, "Plan appears complete: includes goal, files, approach, verification")
	}
	return issues
}
