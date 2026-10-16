package app

import (
	"context"
	"io"
	"log"
	"strings"
	"testing"
)

type pipelineOutputContractTestAgent struct {
	name   string
	output string
}

func (a *pipelineOutputContractTestAgent) Name() string       { return a.name }
func (a *pipelineOutputContractTestAgent) GetType() AgentType { return AgentTypeCustom }
func (a *pipelineOutputContractTestAgent) Generate(context.Context, string) (string, error) {
	return a.output, nil
}

func TestPipelineOutputContractPassesCompleteOutput(t *testing.T) {
	contract := &PipelineOutputContract{
		MinChars:         20,
		RequiredSections: []string{"摘要", "风险"},
		OnViolation:      "degraded",
	}
	output := "## 摘要\n这是一个具备足够正文长度的完整结论。\n\n## 风险\n这里明确披露主要限制和风险。"
	got, degraded, err := applyPipelineOutputContract(contract, output)
	if err != nil || degraded {
		t.Fatalf("complete output rejected: degraded=%v err=%v", degraded, err)
	}
	if got != output {
		t.Fatalf("passing contract mutated output:\n%s", got)
	}
}

func TestPipelineOutputContractMissingSectionDegradesAndPreservesOutput(t *testing.T) {
	contract := &PipelineOutputContract{
		RequiredSections: []string{"摘要", "风险"},
		OnViolation:      "degraded",
	}
	original := "## 摘要\n已有结论应当被保留。"
	got, degraded, err := applyPipelineOutputContract(contract, original)
	if err != nil || !degraded {
		t.Fatalf("missing section should degrade: degraded=%v err=%v", degraded, err)
	}
	for _, want := range []string{original, "OpenKitty 输出质量门（DEGRADED）", "缺少必需章节 `风险`"} {
		if !strings.Contains(got, want) {
			t.Fatalf("degraded output missing %q:\n%s", want, got)
		}
	}
}

func TestPipelineOutputContractFailPolicyReturnsError(t *testing.T) {
	contract := &PipelineOutputContract{MinChars: 100, OnViolation: "fail"}
	got, degraded, err := applyPipelineOutputContract(contract, "too short")
	if err == nil || degraded {
		t.Fatalf("fail policy should return an error: degraded=%v err=%v", degraded, err)
	}
	if got != "too short" {
		t.Fatalf("fail policy should not rewrite output: %q", got)
	}
	if !strings.Contains(err.Error(), "min_chars not met") {
		t.Fatalf("unexpected contract error: %v", err)
	}
}

func TestPipelineOutputContractReportedStatusControlsSemanticDegradation(t *testing.T) {
	contract := &PipelineOutputContract{
		RequiredSections:    []string{"摘要"},
		ReportedStatusField: "交付状态",
		OnViolation:         "fail",
	}

	completed := "## 摘要\n结构与语义均已完成。\n\n交付状态: completed"
	if got, degraded, err := applyPipelineOutputContract(contract, completed); err != nil || degraded || got != completed {
		t.Fatalf("reported completed should pass unchanged: degraded=%v err=%v output=%q", degraded, err, got)
	}
	markdownStatus := "## 摘要\n结构完整。\n\n交付状态: **degraded**"
	if got, degraded, err := applyPipelineOutputContract(contract, markdownStatus); err != nil || !degraded || got != markdownStatus {
		t.Fatalf("markdown-emphasized degraded should be accepted: degraded=%v err=%v output=%q", degraded, err, got)
	}

	for _, status := range []string{"degraded", "no_decision"} {
		output := "## 摘要\n结构完整，但正文明确披露不能完整交付。\n\n交付状态：" + status
		got, degraded, err := applyPipelineOutputContract(contract, output)
		if err != nil || !degraded || got != output {
			t.Fatalf("reported %s should semantically degrade without rewriting: degraded=%v err=%v output=%q", status, degraded, err, got)
		}
		if reason := pipelineOutputContractDegradedReason(contract, output); reason != "reported_status_"+status {
			t.Fatalf("reported %s reason = %q", status, reason)
		}
	}
}

func TestPipelineOutputContractMissingOrInvalidReportedStatusIsViolation(t *testing.T) {
	contract := &PipelineOutputContract{ReportedStatusField: "交付状态", OnViolation: "degraded"}
	tests := []struct {
		name   string
		output string
		want   string
	}{
		{name: "missing", output: "没有状态行", want: `reported status field "交付状态" is missing`},
		{name: "invalid", output: "交付状态: success", want: `has invalid value "success"`},
		{name: "duplicate", output: "交付状态: completed\n交付状态: degraded", want: "appears more than once"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, degraded, err := applyPipelineOutputContract(contract, test.output)
			if err != nil || !degraded {
				t.Fatalf("ordinary violation should follow degraded policy: degraded=%v err=%v", degraded, err)
			}
			if !strings.Contains(got, test.want) {
				t.Fatalf("quality disclosure missing %q:\n%s", test.want, got)
			}
		})
	}
}

func TestPipelineOutputContractDegradesStepAndExecution(t *testing.T) {
	creator := func(config AgentConfig, _ string) (Agent, error) {
		return &pipelineOutputContractTestAgent{name: config.Name, output: "## 摘要\n只有摘要。"}, nil
	}
	factory := &AgentFactory{subAgents: make(map[string]Agent), testSubAgentCreator: creator}
	gateway := &GatewayService{config: &Config{Workspace: t.TempDir()}}
	executor := NewPipelineExecutor(factory, gateway, log.New(io.Discard, "", 0))
	pipeline := Pipeline{Name: "quality-gated", Steps: []PipelineStep{{
		Name: "report", NoTools: true, MaxIter: 1,
		OutputContract: &PipelineOutputContract{
			RequiredSections: []string{"摘要", "风险"},
			OnViolation:      "degraded",
		},
	}}}
	exec := &PipelineExecution{
		ID: "pipe-quality-gated", PipelineName: pipeline.Name, Status: "running",
		Steps: []StepResult{{Name: "report", Status: "pending", Revision: 1}},
	}

	executor.runStepsFrom(context.Background(), exec, pipeline, 0, "input", "session", RunModeInteractive)

	if exec.Status != "degraded" || exec.Steps[0].Status != "degraded" {
		t.Fatalf("contract gap status = %q/%q, want degraded/degraded", exec.Status, exec.Steps[0].Status)
	}
	if !strings.Contains(exec.Steps[0].Output, "只有摘要") || !strings.Contains(exec.Steps[0].Output, "缺少必需章节 `风险`") {
		t.Fatalf("step did not retain output and append its quality gap:\n%s", exec.Steps[0].Output)
	}
	if !strings.Contains(exec.Steps[0].Error, "missing required_sections: 风险") {
		t.Fatalf("step error does not expose the deterministic gap: %q", exec.Steps[0].Error)
	}
}

func TestPipelineOutputContractFailPolicyFailsStepAndExecution(t *testing.T) {
	creator := func(config AgentConfig, _ string) (Agent, error) {
		return &pipelineOutputContractTestAgent{name: config.Name, output: "short"}, nil
	}
	factory := &AgentFactory{subAgents: make(map[string]Agent), testSubAgentCreator: creator}
	gateway := &GatewayService{config: &Config{Workspace: t.TempDir()}}
	executor := NewPipelineExecutor(factory, gateway, log.New(io.Discard, "", 0))
	pipeline := Pipeline{Name: "strict-quality-gated", Steps: []PipelineStep{{
		Name: "report", NoTools: true, MaxIter: 1,
		OutputContract: &PipelineOutputContract{MinChars: 100, OnViolation: "fail"},
	}}}
	exec := &PipelineExecution{
		ID: "pipe-strict-quality-gated", PipelineName: pipeline.Name, Status: "running",
		Steps: []StepResult{{Name: "report", Status: "pending", Revision: 1}},
	}

	executor.runStepsFrom(context.Background(), exec, pipeline, 0, "input", "session", RunModeInteractive)

	if exec.Status != "failed" || exec.Steps[0].Status != "failed" {
		t.Fatalf("fail policy status = %q/%q, want failed/failed", exec.Status, exec.Steps[0].Status)
	}
	if !strings.Contains(exec.Steps[0].Error, "min_chars not met") {
		t.Fatalf("failed step lacks contract reason: %q", exec.Steps[0].Error)
	}
}

func TestPipelineOutputContractNoDecisionDegradesStepAndExecution(t *testing.T) {
	creator := func(config AgentConfig, _ string) (Agent, error) {
		return &pipelineOutputContractTestAgent{name: config.Name, output: "## 摘要\n全部必需章节已生成。\n\n交付状态: no_decision"}, nil
	}
	factory := &AgentFactory{subAgents: make(map[string]Agent), testSubAgentCreator: creator}
	gateway := &GatewayService{config: &Config{Workspace: t.TempDir()}}
	executor := NewPipelineExecutor(factory, gateway, log.New(io.Discard, "", 0))
	pipeline := Pipeline{Name: "semantic-quality-gated", Steps: []PipelineStep{{
		Name: "report", NoTools: true, MaxIter: 1,
		OutputContract: &PipelineOutputContract{
			RequiredSections:    []string{"摘要"},
			ReportedStatusField: "交付状态",
			OnViolation:         "fail",
		},
	}}}
	exec := &PipelineExecution{
		ID: "pipe-semantic-quality-gated", PipelineName: pipeline.Name, Status: "running",
		Steps: []StepResult{{Name: "report", Status: "pending", Revision: 1}},
	}

	executor.runStepsFrom(context.Background(), exec, pipeline, 0, "input", "session", RunModeInteractive)

	if exec.Status != "degraded" || exec.Steps[0].Status != "degraded" || exec.Steps[0].Error != "reported_status_no_decision" {
		t.Fatalf("reported no_decision status = %q/%q reason=%q", exec.Status, exec.Steps[0].Status, exec.Steps[0].Error)
	}
	if strings.Contains(exec.Steps[0].Output, "OpenKitty 输出质量门") {
		t.Fatalf("valid self-reported no_decision should preserve, not rewrite, output:\n%s", exec.Steps[0].Output)
	}
}

func TestBuildPreviousStepContextIncludesDegradedOutputAndReason(t *testing.T) {
	contextText := buildPreviousStepContext(1, []StepResult{{
		Name:   "research",
		Status: "degraded",
		Output: "partial verified evidence",
		Error:  "output contract violation: missing required_sections: risks",
	}})
	for _, want := range []string{"research [DEGRADED]", "质量状态：DEGRADED", "partial verified evidence", "missing required_sections: risks"} {
		if !strings.Contains(contextText, want) {
			t.Fatalf("degraded upstream context missing %q:\n%s", want, contextText)
		}
	}
}

func TestBuildPreviousStepContextExcludesHostFallbackDecision(t *testing.T) {
	contextText := buildPreviousStepContext(1, []StepResult{{
		Name:   "evidence",
		Status: "completed",
		Output: "verified market facts\n<!--HOST_FALLBACK_DECISION_BEGIN-->\nsynthetic fallback ranking\n<!--HOST_FALLBACK_DECISION_END-->\nverified filing facts",
	}})
	for _, want := range []string{"verified market facts", "verified filing facts"} {
		if !strings.Contains(contextText, want) {
			t.Fatalf("previous-step context lost real evidence %q:\n%s", want, contextText)
		}
	}
	if strings.Contains(contextText, "synthetic fallback ranking") || strings.Contains(contextText, "HOST_FALLBACK_DECISION") {
		t.Fatalf("host fallback decision leaked into downstream evidence context:\n%s", contextText)
	}
}

func TestPipelineOutputContractConfigurationIsRejectedBeforeRegistration(t *testing.T) {
	executor := NewPipelineExecutor(nil, nil, log.New(io.Discard, "", 0))
	invalid := Pipeline{Name: "invalid", Steps: []PipelineStep{{
		Name:           "report",
		OutputContract: &PipelineOutputContract{MinChars: -1, OnViolation: "ignore"},
	}}}
	if err := executor.RegisterDynamic("invalid", invalid); err == nil || !strings.Contains(err.Error(), "min_chars") {
		t.Fatalf("invalid output contract was registered: %v", err)
	}
	if _, exists := executor.pipelines["invalid"]; exists {
		t.Fatal("invalid dynamic pipeline must not enter the registry")
	}
}
