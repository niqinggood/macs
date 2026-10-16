package app

import (
	"strings"
	"testing"
)

func TestPublicProgressStepNameKeepsBusinessMeaningAndHidesInternals(t *testing.T) {
	for _, name := range []string{"公司与产品研究", "融资 BP 生成", "第 3 轮 · 文件校验"} {
		if got := publicProgressStepName(name); got != name {
			t.Fatalf("business step %q became %q", name, got)
		}
	}
	for _, internal := range []string{
		`.openkitty/runtime/builtin/nova-community/scripts/chatroom_agent.py`,
		`C:\runtime\scripts\build.py`,
		`use_skill huanzhi-fa-fundraising`,
		"code_check action=run",
	} {
		if got := publicProgressStepName(internal); got != "" {
			t.Fatalf("internal step label leaked: input=%q got=%q", internal, got)
		}
	}
}

func TestAgentOSBusinessStepPhaseIsPublicAndBounded(t *testing.T) {
	phase := agentOSBusinessStepPhase("融资 BP 演示文稿生成")
	if !strings.HasPrefix(phase, agentOSBusinessStepPhasePrefix) {
		t.Fatalf("phase=%q", phase)
	}
	if got := agentOSBusinessStepFromPhase(phase); got != "融资 BP 演示文稿生成" {
		t.Fatalf("business step=%q", got)
	}
	gateway := &GatewayService{}
	if got := gateway.agentOSTaskPhaseLabel(phase); got != "融资 BP 演示文稿生成" {
		t.Fatalf("phase label=%q", got)
	}
	if len([]byte(agentOSBusinessStepPhase(strings.Repeat("研究", 80)))) > 64 {
		t.Fatal("business phase exceeds durable field budget")
	}
}

func TestPublicAgentOSPipelineProgressIncludesStepPosition(t *testing.T) {
	exec := &PipelineExecution{
		ID:     "direct_fundraising_1",
		TaskID: "agt-parent",
		Steps:  []StepResult{{Name: "任务受理"}, {Name: "产品研究"}, {Name: "BP 生成"}},
		RuntimePipeline: &Pipeline{
			AIEmployeeName: "融资专家",
		},
	}
	source := ProgressEvent{
		StepName: "产品研究", StepIndex: 2, StepTotal: 3, Status: "running",
	}
	started, ok := publicAgentOSPipelineProgressEvent(exec, "step_start", source)
	if !ok || started.Type != "stage_start" || started.TaskID != exec.TaskID ||
		started.AgentName != "融资专家" || started.StepIndex != 2 || started.StepTotal != 3 ||
		!strings.Contains(started.Message, "第 2/3 步") || !strings.Contains(started.Message, "产品研究") {
		t.Fatalf("public stage start=%+v ok=%v", started, ok)
	}
	done, ok := publicAgentOSPipelineProgressEvent(exec, "step_done", source)
	if !ok || done.Type != "stage_done" || !strings.Contains(done.Message, "已完成") {
		t.Fatalf("public stage done=%+v ok=%v", done, ok)
	}
}

func TestPublicAgentOSPipelineProgressDoesNotMirrorUnrelatedRuns(t *testing.T) {
	for _, exec := range []*PipelineExecution{
		nil,
		{ID: "same", TaskID: "same", Steps: []StepResult{{Name: "step"}}},
		{ID: "missing-parent", Steps: []StepResult{{Name: "step"}}},
	} {
		if event, ok := publicAgentOSPipelineProgressEvent(exec, "step_start", ProgressEvent{StepName: "step"}); ok {
			t.Fatalf("unrelated pipeline event was mirrored: %+v", event)
		}
	}
}

func TestPublicPipelineEmployeeNameFallsBackToRegisteredEmployee(t *testing.T) {
	manager := &AIEmployeeManager{employees: map[string]*AIEmployeeConfig{
		"fundraising-advisor": {ID: "fundraising-advisor", Name: "融资专家", AtName: "融资专家"},
	}}
	executor := &PipelineExecutor{gateway: &GatewayService{aeManager: manager}}
	exec := &PipelineExecution{RuntimePipeline: &Pipeline{AIEmployeeID: "fundraising-advisor"}}
	if got := executor.publicPipelineEmployeeName(exec); got != "融资专家" {
		t.Fatalf("employee name fallback=%q", got)
	}
}
