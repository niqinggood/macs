package app

import (
	"context"
	"io"
	"log"
	"strings"
	"testing"
)

type pipelineReworkSequenceAgent struct {
	name       string
	outputs    []string
	callCounts map[string]int
	order      *[]string
	prompts    map[string][]string
}

func (a *pipelineReworkSequenceAgent) Name() string       { return a.name }
func (a *pipelineReworkSequenceAgent) GetType() AgentType { return AgentTypeCustom }
func (a *pipelineReworkSequenceAgent) Generate(_ context.Context, prompt string) (string, error) {
	a.callCounts[a.name]++
	*a.order = append(*a.order, a.name)
	a.prompts[a.name] = append(a.prompts[a.name], prompt)
	index := a.callCounts[a.name] - 1
	if index >= len(a.outputs) {
		index = len(a.outputs) - 1
	}
	return a.outputs[index], nil
}

func TestPipelineReworkRunsUpstreamThenReviewerExactlyOnce(t *testing.T) {
	var order []string
	prompts := map[string][]string{}
	callCounts := map[string]int{}
	creator := func(config AgentConfig, _ string) (Agent, error) {
		name := config.Name
		var outputs []string
		switch {
		case strings.HasSuffix(name, "_collect"):
			outputs = []string{"evidence v1", "evidence v2 with repaired date"}
		case strings.HasSuffix(name, "_review"):
			outputs = []string{
				"cannot use v1\n" + pipelineReworkControlPrefix + `{"status":"retry_required","reason":"add the missing date"}`,
				"accepted decision\n" + pipelineReworkControlPrefix + `{"status":"accepted","reason":""}`,
			}
		default:
			outputs = []string{"final delivery"}
		}
		agent := &pipelineReworkSequenceAgent{name: name, outputs: outputs, callCounts: callCounts, order: &order, prompts: prompts}
		return agent, nil
	}
	factory := &AgentFactory{subAgents: make(map[string]Agent), testSubAgentCreator: creator}
	gateway := &GatewayService{config: &Config{Workspace: t.TempDir()}}
	executor := NewPipelineExecutor(factory, gateway, log.New(io.Discard, "", 0))
	pipeline := Pipeline{Name: "feedback", Steps: []PipelineStep{
		{Name: "collect", NoTools: true, MaxIter: 1},
		{Name: "review", NoTools: true, MaxIter: 1, Rework: &StepRework{Target: "collect", MaxReworks: 1}},
		{Name: "deliver", NoTools: true, MaxIter: 1},
	}}
	exec := &PipelineExecution{ID: "pipe-feedback", PipelineName: pipeline.Name, Status: "running", Steps: []StepResult{
		{Name: "collect", Status: "pending", Revision: 1},
		{Name: "review", Status: "pending", Revision: 1},
		{Name: "deliver", Status: "pending", Revision: 1},
	}}

	executor.runStepsFrom(context.Background(), exec, pipeline, 0, "input", "session", RunModeInteractive)

	wantOrder := []string{"pipe_feedback_collect", "pipe_feedback_review", "pipe_feedback_collect", "pipe_feedback_review", "pipe_feedback_deliver"}
	if strings.Join(order, ",") != strings.Join(wantOrder, ",") {
		t.Fatalf("execution order = %v, want %v; status=%s current=%d steps=%#v pending=%#v", order, wantOrder, exec.Status, exec.CurrentStep, exec.Steps, exec.PendingRework)
	}
	if exec.Status != "completed" || exec.ReworkCounts["1->0"] != 1 || len(exec.ReworkHistory) != 1 || exec.ReworkHistory[0].Outcome != "accepted" {
		t.Fatalf("unexpected final rework state: status=%s counts=%v history=%#v", exec.Status, exec.ReworkCounts, exec.ReworkHistory)
	}
	collectPrompts := prompts["pipe_feedback_collect"]
	if len(collectPrompts) != 2 || !strings.Contains(collectPrompts[1], "add the missing date") || !strings.Contains(collectPrompts[1], "evidence v1") {
		t.Fatalf("second upstream prompt lacks bounded feedback:\n%v", collectPrompts)
	}
	if callCounts["pipe_feedback_collect"] != 2 || callCounts["pipe_feedback_review"] != 2 || callCounts["pipe_feedback_deliver"] != 1 {
		t.Fatalf("unexpected call counts: collect=%d review=%d deliver=%d", callCounts["pipe_feedback_collect"], callCounts["pipe_feedback_review"], callCounts["pipe_feedback_deliver"])
	}
}

func TestPipelineReworkRequestsOneRevisionAndClearsOnAcceptance(t *testing.T) {
	steps := []PipelineStep{
		{Name: "collect", NoTools: true, MaxIter: 1},
		{Name: "review", NoTools: true, MaxIter: 1, Rework: &StepRework{Target: "collect", MaxReworks: 1}},
		{Name: "deliver", NoTools: true, MaxIter: 1},
	}
	exec := &PipelineExecution{
		ID: "pipe-rework", PipelineName: "test", Status: "running",
		Steps: []StepResult{
			{Name: "collect", Status: "completed", Output: "evidence v1", Revision: 1},
			{Name: "review", Status: "running", Revision: 1},
			{Name: "deliver", Status: "pending", Revision: 1},
		},
	}
	executor := &PipelineExecutor{}

	rejected := "insufficient evidence\n" + pipelineReworkControlPrefix + `{"status":"retry_required","reason":"missing the required date"}`
	outcome, err := executor.processPipelineReworkDecision(exec, steps, 1, rejected)
	if err != nil {
		t.Fatal(err)
	}
	if !outcome.Requested || outcome.TargetIndex != 0 || outcome.Attempt != 1 || outcome.Max != 1 {
		t.Fatalf("unexpected rework outcome: %#v", outcome)
	}
	if exec.PendingRework == nil || exec.PendingRework.Reason != "missing the required date" {
		t.Fatalf("pending rework not persisted: %#v", exec.PendingRework)
	}
	if exec.Steps[0].Status != "pending" || exec.Steps[0].Revision != 2 || len(exec.Steps[0].Revisions) != 1 {
		t.Fatalf("target revision was not archived/reset: %#v", exec.Steps[0])
	}
	if context := pendingPipelineReworkContext(exec, 0); !strings.Contains(context, "missing the required date") || !strings.Contains(context, "evidence v1") {
		t.Fatalf("rework context did not carry reviewer feedback and v1 output:\n%s", context)
	}

	exec.Steps[0].Status = "completed"
	exec.Steps[0].Output = "evidence v2 with date"
	exec.Steps[1].Status = "running"
	accepted := "review decision\n" + pipelineReworkControlPrefix + `{"status":"accepted","reason":""}`
	outcome, err = executor.processPipelineReworkDecision(exec, steps, 1, accepted)
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Requested || outcome.Output != "review decision" || exec.PendingRework != nil {
		t.Fatalf("accepted revision did not resume normal flow: outcome=%#v pending=%#v", outcome, exec.PendingRework)
	}
	if len(exec.ReworkHistory) != 1 || exec.ReworkHistory[0].Outcome != "accepted" || exec.ReworkHistory[0].ResolvedAt == nil {
		t.Fatalf("accepted rework audit is incomplete: %#v", exec.ReworkHistory)
	}
}

func TestPipelineReworkNeverRunsUpstreamMoreThanOnce(t *testing.T) {
	steps := []PipelineStep{
		{Name: "collect", NoTools: true, MaxIter: 1},
		{Name: "review", NoTools: true, MaxIter: 1, Rework: &StepRework{Target: "collect", MaxReworks: 1, OnExhausted: "degraded"}},
	}
	exec := &PipelineExecution{ID: "pipe-rework-cap", Status: "running", Steps: []StepResult{
		{Name: "collect", Status: "completed", Output: "v1", Revision: 1},
		{Name: "review", Status: "running", Revision: 1},
	}}
	executor := &PipelineExecutor{}
	control := pipelineReworkControlPrefix + `{"status":"retry_required","reason":"still broken"}`
	first, err := executor.processPipelineReworkDecision(exec, steps, 1, control)
	if err != nil || !first.Requested {
		t.Fatalf("first rework request failed: outcome=%#v err=%v", first, err)
	}
	exec.Steps[0].Status = "completed"
	exec.Steps[0].Output = "v2"
	exec.Steps[1].Status = "running"
	second, err := executor.processPipelineReworkDecision(exec, steps, 1, control)
	if err != nil {
		t.Fatal(err)
	}
	if second.Requested || !second.Degraded || exec.ReworkCounts["1->0"] != 1 {
		t.Fatalf("second rejection triggered another upstream run: %#v counts=%v", second, exec.ReworkCounts)
	}
	if exec.PendingRework != nil || len(exec.ReworkHistory) != 1 || exec.ReworkHistory[0].Outcome != "exhausted" {
		t.Fatalf("exhausted rework state was not finalized: pending=%#v history=%#v", exec.PendingRework, exec.ReworkHistory)
	}
}

func TestPipelineReworkRejectsUnsafeOrInvalidGraphs(t *testing.T) {
	invalid := Pipeline{Name: "invalid", Steps: []PipelineStep{
		{Name: "first", NoTools: true},
		{Name: "review", NoTools: true, Rework: &StepRework{Target: "first", MaxReworks: 3}},
	}}
	if err := validatePipelineReworkConfig(invalid); err == nil || !strings.Contains(err.Error(), "max_reworks") {
		t.Fatalf("invalid rework cap was accepted: %v", err)
	}

	steps := []PipelineStep{
		{Name: "mutate", Tools: []string{"write_file"}},
		{Name: "review", NoTools: true, Rework: &StepRework{Target: "mutate", MaxReworks: 1}},
	}
	exec := &PipelineExecution{ID: "unsafe", Status: "running", Steps: []StepResult{
		{Name: "mutate", Status: "completed", Output: "wrote data", Revision: 1},
		{Name: "review", Status: "running", Revision: 1},
	}}
	control := pipelineReworkControlPrefix + `{"status":"retry_required","reason":"write it again"}`
	if _, err := (&PipelineExecutor{}).processPipelineReworkDecision(exec, steps, 1, control); err == nil || !strings.Contains(err.Error(), "side effects") {
		t.Fatalf("unsafe rework target was accepted: %v", err)
	}
}

func TestPipelineReworkControlMustBeStructuredFinalLine(t *testing.T) {
	prompt := appendPipelineReworkReviewInstruction("review", &PipelineStep{Rework: &StepRework{Target: "collect"}})
	if !strings.Contains(prompt, pipelineReworkControlPrefix) || !strings.Contains(prompt, "at most once") {
		t.Fatalf("review prompt lacks bounded structured contract:\n%s", prompt)
	}
	control, cleaned, found, err := parsePipelineReworkControl("usable result\n" + pipelineReworkControlPrefix + `{"status":"accepted"}`)
	if err != nil || !found || control.Status != "accepted" || cleaned != "usable result" {
		t.Fatalf("structured control parse failed: control=%#v cleaned=%q found=%v err=%v", control, cleaned, found, err)
	}
	_, unchanged, found, err := parsePipelineReworkControl(pipelineReworkControlPrefix + `{"status":"retry_required"}` + "\nordinary tail")
	if err != nil || found || !strings.Contains(unchanged, "ordinary tail") {
		t.Fatalf("non-final control text should not trigger rework: found=%v unchanged=%q err=%v", found, unchanged, err)
	}
}

func TestPipelineReworkRevisionDoesNotInheritExpandedInteractiveTools(t *testing.T) {
	step := &PipelineStep{Tools: []string{"web_fetch", "web_search", "stock_facts"}}
	exec := &PipelineExecution{PendingRework: &PipelineReworkRequest{TargetIndex: 0}}
	tools := pipelineStepExecutionTools(exec, step, 0)
	joined := "," + strings.Join(tools, ",") + ","
	for _, want := range []string{"web_fetch", "web_search", "stock_facts"} {
		if !strings.Contains(joined, ","+want+",") {
			t.Fatalf("rework tools missing %q: %v", want, tools)
		}
	}
	for _, forbidden := range []string{"web_navigate", "web_page", "web_close"} {
		if strings.Contains(joined, ","+forbidden+",") {
			t.Fatalf("read-only rework inherited interactive tool %q: %v", forbidden, tools)
		}
	}
}
