package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestEnsurePipelineSlotContractsTurnsEveryStepIntoFillableSlot(t *testing.T) {
	pipeline := Pipeline{Name: "wechat-article", Steps: []PipelineStep{
		{Name: aiEmployeeIntakeStepName, NoTools: true},
		{Name: "素材准备", Tools: []string{"web_search"}, SourceContract: &PipelineSourceContract{Required: true}},
		{Name: "公众号交付物", Tools: []string{"wechat_article_bundle"}, ArtifactContract: &PipelineArtifactContract{Paths: []string{"article.md", "article.html"}}},
	}}
	got := ensurePipelineSlotContracts(pipeline)
	if len(got.Steps) != 3 {
		t.Fatalf("steps=%d", len(got.Steps))
	}
	for i, step := range got.Steps {
		if step.Slot == nil || step.Slot.ID == "" || len(step.Slot.RequiredFields) == 0 || len(step.Slot.ValidationRules) == 0 {
			t.Fatalf("step %d has incomplete slot contract: %#v", i, step.Slot)
		}
		if !pipelineStepHasStopRoute(step) {
			t.Fatalf("step %d cannot stop for missing input", i)
		}
	}
	if got.Steps[0].Slot.ID != "task_brief" {
		t.Fatalf("intake slot=%q", got.Steps[0].Slot.ID)
	}
	intakeFields := strings.Join(got.Steps[0].Slot.RequiredFields, "|")
	if !strings.Contains(intakeFields, "任务身份坐标") || !strings.Contains(intakeFields, "硬约束") ||
		!strings.Contains(intakeFields, "优先条件") || !strings.Contains(intakeFields, "自动放宽顺序") {
		t.Fatalf("intake slot omitted universal constraint ledger: %#v", got.Steps[0].Slot.RequiredFields)
	}
	material := got.Steps[1].Slot
	if !strings.Contains(strings.Join(material.RequiredFields, "|"), "事实与观点边界") ||
		!strings.Contains(strings.Join(material.ValidationRules, "|"), "禁止自行随机选题") {
		t.Fatalf("wechat material slot is not domain-specific: %#v", material)
	}
	if len(got.Steps[2].Slot.DependsOn) != 1 || got.Steps[2].Slot.DependsOn[0] != got.Steps[1].Slot.ID {
		t.Fatalf("slot dependency missing: %#v", got.Steps[2].Slot.DependsOn)
	}
}

func TestPPTSlotContractTreatsSmallFontAsQualityWarning(t *testing.T) {
	pipeline := ensurePipelineSlotContracts(Pipeline{Name: "ppt-polish", Steps: []PipelineStep{{Name: "视觉润色"}}})
	rules := strings.Join(pipeline.Steps[0].Slot.ValidationRules, "\n")
	if !strings.Contains(rules, "≤12pt") || !strings.Contains(rules, "不得因此丢弃") {
		t.Fatalf("PPT visual slot omitted severity-aware small-font rule: %s", rules)
	}
}

func TestAppendPipelineSlotInstructionDeclaresFillAndMissingProtocol(t *testing.T) {
	step := PipelineStep{Name: "分析", Slot: &PipelineSlotContract{
		ID: "step_01", TaskDescription: "分析真实材料", RequiredFields: []string{"结论", "证据"}, ValidationRules: []string{"证据可追溯"},
	}}
	got := appendPipelineSlotInstruction("base", nil, &step, 1)
	for _, want := range []string{"slot_id：step_01", "结论；证据", pipelineSlotFilledMarker, pipelineSlotMissingMarker, pipelineWaitingInputMarker, pipelineStopMarker} {
		if !strings.Contains(got, want) {
			t.Fatalf("slot prompt missing %q: %s", want, got)
		}
	}
}

func TestPipelineSlotStepInputExcludesIdentityAndObsoleteClarification(t *testing.T) {
	pipeline := Pipeline{
		AIEmployeeOriginalTask: "## 原始待办\n整理一篇公众号文章\n\n## 用户补充\n主题是AI员工的Pipeline槽位；读者是产品经理。",
		AIEmployeeContextPath:  ".openkitty/storage/.system/ae_context/session.json",
	}
	full := "你是微信公众号编辑。\n\n---\n\n" + pipeline.AIEmployeeOriginalTask + "\n\n## 上轮待补提示\n请提供素材、链接或文件"
	if got := pipelineSlotStepInput(pipeline, full, 0); got != full {
		t.Fatal("intake must retain the full labelled reasoning context")
	}
	got := pipelineSlotStepInput(pipeline, full, 1)
	if strings.Contains(got, "你是微信公众号编辑") || strings.Contains(got, "上轮待补提示") || strings.Contains(got, "请提供素材") {
		t.Fatalf("control/prefix text leaked into business slot input: %s", got)
	}
	if !strings.Contains(got, "主题是AI员工") || extractAIEmployeeContextPath(got) != pipeline.AIEmployeeContextPath {
		t.Fatalf("accepted current task or context path was lost: %s", got)
	}
	if pipelineInputReferencesSource(got) {
		t.Fatalf("original-topic supplement was misclassified as an existing source: %s", got)
	}
}

func TestPipelineResumeReusesOnlyValidatedSlotsBeforeMissingSlot(t *testing.T) {
	pipeline := ensurePipelineSlotContracts(Pipeline{Name: "demo", Steps: []PipelineStep{
		{Name: aiEmployeeIntakeStepName}, {Name: "资料槽"}, {Name: "交付槽"},
	}})
	old := &PipelineExecution{
		ID: "old_exec",
		Steps: []StepResult{
			{Name: aiEmployeeIntakeStepName, SlotID: "task_brief", SlotStatus: pipelineSlotFilled, Status: "completed", Output: "old brief"},
			{Name: "资料槽", SlotID: "step_01", SlotStatus: pipelineSlotFilled, SlotValidation: "passed", Status: "completed", Output: "validated material"},
			{Name: "交付槽", SlotID: "step_02", SlotStatus: pipelineSlotMissing, Status: "completed", Output: "need input"},
		},
		CompletionReason: &PipelineCompletionReason{Code: aeWaitingInputStatus, StepIndex: 2},
	}
	ctx := withPipelineResumeSeed(context.Background(), old)
	current := &PipelineExecution{Steps: make([]StepResult, len(pipeline.Steps))}
	for i, step := range pipeline.Steps {
		current.Steps[i] = StepResult{Name: step.Name, SlotID: step.Slot.ID, SlotStatus: pipelineSlotPending, Status: "pending"}
	}
	seedPipelineExecutionSlots(ctx, current, pipeline)
	if current.ResumeFromExecID != "old_exec" {
		t.Fatalf("resume link=%q", current.ResumeFromExecID)
	}
	if current.Steps[0].Status != "pending" {
		t.Fatal("task brief must be re-evaluated after user supplement")
	}
	if !pipelineStepHasReusableSlot(current, 1) || current.Steps[1].Output != "validated material" {
		t.Fatalf("validated upstream slot was not reused: %#v", current.Steps[1])
	}
	if current.Steps[2].Status != "pending" || current.Steps[2].ReusedFromExec != "" {
		t.Fatalf("missing slot must be filled again: %#v", current.Steps[2])
	}
}

func TestPipelineResumeLinksPriorExecutionWhenTaskBriefWasMissing(t *testing.T) {
	old := &PipelineExecution{
		ID:               "old_intake_exec",
		Steps:            []StepResult{{Name: aiEmployeeIntakeStepName, SlotID: "task_brief", SlotStatus: pipelineSlotMissing, Status: "completed"}},
		CompletionReason: &PipelineCompletionReason{Code: aeWaitingInputStatus, StepIndex: 0},
	}
	ctx := withPipelineResumeSeed(context.Background(), old)
	current := &PipelineExecution{Steps: []StepResult{{Name: aiEmployeeIntakeStepName, SlotID: "task_brief", SlotStatus: pipelineSlotPending, Status: "pending"}}}
	seedPipelineExecutionSlots(ctx, current, Pipeline{})
	if current.ResumeFromExecID != old.ID {
		t.Fatalf("missing task-brief continuation lost audit link: %q", current.ResumeFromExecID)
	}
	if current.Steps[0].Status != "pending" {
		t.Fatal("supplement must re-run task brief rather than reuse its missing result")
	}
}

func TestPipelineSlotStatePersistsInExecutionJSON(t *testing.T) {
	exec := PipelineExecution{ID: "e", ResumeFromExecID: "old", Steps: []StepResult{{Name: "x", SlotID: "step_01", SlotStatus: pipelineSlotFilled, SlotValidation: "passed"}}}
	data, err := json.Marshal(&exec)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{`"resume_from_exec_id":"old"`, `"slot_id":"step_01"`, `"slot_status":"filled"`, `"slot_validation":"passed"`} {
		if !strings.Contains(text, want) {
			t.Fatalf("persisted state missing %s: %s", want, text)
		}
	}
}

func TestBuildPipelineSlotFillRecordCreatesAuditableReceipt(t *testing.T) {
	step := &PipelineStep{Name: "交付", Slot: &PipelineSlotContract{ID: "step_02"}, ArtifactContract: &PipelineArtifactContract{Paths: []string{".openkitty/storage/result.md"}}}
	result := StepResult{
		Name: "交付", Status: "completed", SlotStatus: pipelineSlotFilled,
		Output: "已交付 result.md\n" + pipelineSlotFilledMarker,
		ToolCalls: []ToolCallRecord{{
			ToolName: "bundle", Status: "success",
			Params: `{"output_path":".openkitty/storage/result.md"}`,
			Result: `{"ok":true,"html_path":".openkitty/storage/result.html"}`,
		}},
	}
	receipt := buildPipelineSlotFillRecord(step, result)
	if receipt == nil || receipt.Status != pipelineSlotFilled || receipt.OutputSHA256 == "" || receipt.OutputChars == 0 {
		t.Fatalf("invalid fill receipt: %#v", receipt)
	}
	if len(receipt.ArtifactPaths) != 2 || receipt.ArtifactPaths[0] != ".openkitty/storage/result.md" || receipt.ArtifactPaths[1] != ".openkitty/storage/result.html" {
		t.Fatalf("artifact paths missing from receipt: %#v", receipt.ArtifactPaths)
	}
	if len(receipt.Checks) < 3 || !receipt.Checks[0].Passed || !receipt.Checks[1].Passed || !receipt.Checks[2].Passed {
		t.Fatalf("validation checks missing or failed: %#v", receipt.Checks)
	}
}

func TestPipelineSlotReceiptDoesNotTreatPlannedOutputPathAsArtifact(t *testing.T) {
	step := &PipelineStep{Name: aiEmployeeIntakeStepName, Slot: &PipelineSlotContract{ID: "task_brief"}}
	receipt := buildPipelineSlotFillRecord(step, StepResult{
		Name: aiEmployeeIntakeStepName, Status: "completed", SlotStatus: pipelineSlotFilled,
		Output: "最终将交付到 .openkitty/storage/future_report.md，但本步骤不写文件。",
	})
	if receipt == nil || len(receipt.ArtifactPaths) != 0 {
		t.Fatalf("planned path was incorrectly recorded as a real artifact: %#v", receipt)
	}
}

func TestStripPipelineControlMarkersRemovesSlotProtocol(t *testing.T) {
	got := stripPipelineControlMarkers("结果\n" + pipelineSlotFilledMarker + "\n" + pipelineSlotMissingMarker)
	if got != "结果" {
		t.Fatalf("slot control marker leaked to user: %q", got)
	}
}
