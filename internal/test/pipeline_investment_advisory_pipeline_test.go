package app

import (
	"context"
	"errors"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type investmentParallelTestAgent struct {
	name   string
	output string
	err    error
}

func (a *investmentParallelTestAgent) Name() string { return a.name }
func (a *investmentParallelTestAgent) Generate(context.Context, string) (string, error) {
	return a.output, a.err
}
func (a *investmentParallelTestAgent) GetType() AgentType { return AgentTypeCustom }

func TestInvestmentAdvisoryPipelineUsesBoundedAutonomousEvidenceWorkflow(t *testing.T) {
	pipeline := (&GatewayService{}).defaultPipelineSet()["investment-advisory"]
	if len(pipeline.Steps) != 4 {
		t.Fatalf("steps = %d, want 4", len(pipeline.Steps))
	}
	collection := pipeline.Steps[0]
	if collection.Name != "自主判断与证据采集" || collection.NoTools || collection.MaxIter > 5 || !collection.ContinueOnError {
		t.Fatalf("collection step = %#v", collection)
	}
	if collection.TimeoutSeconds != 600 {
		t.Fatalf("collection timeout = %ds, want 600s so multi-turn evidence work is not capped by the old two-minute deadline", collection.TimeoutSeconds)
	}
	if collection.AllowMissingInput {
		t.Fatal("market evidence collection must not turn public-data gaps into a user-input stop")
	}
	for _, want := range []string{"stock_facts", "web_search", "web_fetch", "platform_search"} {
		if !containsString(collection.Tools, want) {
			t.Fatalf("collection tools missing %q: %v", want, collection.Tools)
		}
	}
	research := pipeline.Steps[1]
	if research.Name != "分类型研究与研判" || !research.NoTools || !research.ContinueOnError || len(research.Parallel) != 0 || research.MaxIter != 1 {
		t.Fatalf("adaptive research step = %#v", research)
	}
	if research.TimeoutSeconds != 300 {
		t.Fatalf("adaptive research timeout = %ds, want 300s", research.TimeoutSeconds)
	}
	if len(research.Tools) != 0 {
		t.Fatalf("adaptive research must consume frozen upstream evidence without more tools: %v", research.Tools)
	}
	if research.OutputContract == nil || research.OutputContract.OnViolation != "degraded" || research.OutputContract.MinChars != 240 || len(research.OutputContract.RequiredSections) != 0 {
		t.Fatalf("adaptive research output contract = %#v", research.OutputContract)
	}
	decision := pipeline.Steps[2]
	if decision.Name != "投研合成与质量门" || !decision.NoTools || decision.MaxIter != 1 || !decision.ContinueOnError || decision.TimeoutSeconds != 300 || decision.OutputTokenLimit != 4200 {
		t.Fatalf("decision step = %#v", decision)
	}
	if decision.Rework != nil {
		t.Fatalf("restored main flow must not multiply failures through rework: %#v", decision.Rework)
	}
	if decision.OutputContract != nil {
		t.Fatalf("user-facing synthesis must not use a rigid character-count contract: %#v", decision.OutputContract)
	}
	delivery := pipeline.Steps[3]
	if delivery.Name != "结果交付" || delivery.MaxIter != 2 {
		t.Fatalf("delivery step = %#v", delivery)
	}
	if delivery.ArtifactContract != nil {
		t.Fatalf("chat-first delivery must not force an artifact: %#v", delivery.ArtifactContract)
	}
	for _, want := range []string{"write_file"} {
		if !containsString(delivery.Tools, want) {
			t.Fatalf("delivery tools missing %q: %v", want, delivery.Tools)
		}
	}
	allPrompts := collection.Prompt + "\n" + research.Prompt + "\n" + decision.Prompt + "\n" + delivery.Prompt
	for _, want := range []string{
		"INVESTMENT_INTENT", "DECISION_LEVEL", "ANSWER_DEPTH", "PERSONALIZATION_REQUIRED",
		"market_brief", "instrument_analysis", "sector_analysis", "fund_etf_analysis", "event_explanation", "education",
		"最新可获得交易日", "每类缺口最多两个有效来源", "verified_fact", "transparent_inference", "unknown", "source_conflict",
		"上证综指=sh000001", "科创50=sh000688", "lookback_days 省略或使用 80",
		"周六、周日不是 A 股交易日", "这是正常回溯", "不能标成 fetch_failed",
		"不得在行情工具返回前猜一个日期去搜索", "没有涨跌家数时不得升级成“全市场普涨”",
		"单日成交额不能证明放量", "单一行情商不能声称交叉核验",
		"不要求持仓、成本或证券代码", "不得把行业问题缩成一只股票", "不得套用个股财报框架",
		"只分析当前意图真正需要的维度", "不用固定六章节凑完整",
		"不播报 Pipeline", "information/analysis 不询问个人持仓", "personalized_action",
		"默认直接交付", "不强制生成文件", "ANSWER_DEPTH=deep",
	} {
		if !strings.Contains(allPrompts, want) {
			t.Fatalf("investment-advisory prompts missing %q", want)
		}
	}
	for i := range pipeline.Steps {
		if pipeline.Steps[i].ProfessionalAgent != nil {
			t.Fatalf("investment advisor step %q must not invoke a professional-agent facade", pipeline.Steps[i].Name)
		}
		for _, branch := range pipeline.Steps[i].Parallel {
			if branch.ProfessionalAgent != nil {
				t.Fatalf("investment advisor branch %q must not invoke a professional-agent facade", branch.Name)
			}
		}
	}
	for _, forbidden := range []string{"list_skills", "use_skill", "persistent_shell", "24小时、7天、30天和90天", "固定六章节"} {
		if strings.Contains(collection.Prompt, forbidden) {
			t.Fatalf("bounded collection prompt retained expensive rigid route %q", forbidden)
		}
	}
}

func TestInvestmentAdvisoryContinuesAfterPrematureEvidenceStop(t *testing.T) {
	outputs := []string{
		`三轮检索未取得当日成交额和涨跌家数，但已保留来源与未知项。

## 重新发起方式
请重新 @投资顾问 并提供公开行情字段。

INPUT_STATUS: MISSING
<!--PIPELINE_STOP-->`,
		"研究判断：现有公开证据覆盖有限，因此只给可验证事实和观察条件，不推断具体涨跌归因。",
		"当前可确认的是公开数据覆盖不足。建议关注下一交易日指数、量能和板块扩散，未知项不影响给出这一中性观察框架。",
		"当前可确认的是公开数据覆盖不足。建议关注下一交易日指数、量能和板块扩散。",
	}
	created := 0
	factory := &AgentFactory{
		subAgents: make(map[string]Agent),
		testSubAgentCreator: func(config AgentConfig, _ string) (Agent, error) {
			if created >= len(outputs) {
				t.Fatalf("unexpected extra agent creation for %q", config.Name)
			}
			output := outputs[created]
			created++
			return &investmentParallelTestAgent{name: config.Name, output: output}, nil
		},
	}
	gateway := &GatewayService{config: &Config{Workspace: t.TempDir()}}
	executor := NewPipelineExecutor(factory, gateway, log.New(io.Discard, "", 0))
	pipeline := (&GatewayService{}).defaultPipelineSet()["investment-advisory"]
	pipeline.AIEmployeeID = "investment-advisor"
	pipeline.AIEmployeeName = "投资顾问"
	pipeline.AIEmployeeAtName = "投资顾问"
	exec := &PipelineExecution{
		ID: "pipe-investment-premature-stop", PipelineName: pipeline.Name, Status: "running",
		RuntimePipeline: &pipeline,
		Steps: []StepResult{
			{Name: pipeline.Steps[0].Name, Status: "pending", Revision: 1},
			{Name: pipeline.Steps[1].Name, Status: "pending", Revision: 1},
			{Name: pipeline.Steps[2].Name, Status: "pending", Revision: 1},
			{Name: pipeline.Steps[3].Name, Status: "pending", Revision: 1},
		},
	}

	executor.runStepsFrom(context.Background(), exec, pipeline, 0, "分析一下当前 A 股行情和大盘涨跌原因", "session", RunModeInteractive)

	if exec.CompletionReason != nil {
		t.Fatalf("public evidence gap must not set a waiting-input completion: %#v", exec.CompletionReason)
	}
	if exec.Status != "degraded" {
		t.Fatalf("execution status = %q, want degraded", exec.Status)
	}
	if exec.Steps[0].Status != "degraded" || exec.Steps[0].Error != "unexpected_missing_input_stop_suppressed" {
		t.Fatalf("evidence step was not observably degraded: %#v", exec.Steps[0])
	}
	for _, forbidden := range []string{"## 重新发起方式", pipelineWaitingInputMarker, pipelineStopMarker} {
		if strings.Contains(exec.Steps[0].Output, forbidden) {
			t.Fatalf("evidence output retained %q:\n%s", forbidden, exec.Steps[0].Output)
		}
	}
	if (exec.Steps[1].Status != "completed" && exec.Steps[1].Status != "degraded") ||
		exec.Steps[2].Status != "completed" || exec.Steps[3].Status != "completed" {
		t.Fatalf("downstream steps did not complete: %#v", exec.Steps)
	}
	final := exec.FinalOutput()
	if !strings.Contains(final, "建议关注下一交易日") || strings.Contains(final, "重新 @投资顾问") {
		t.Fatalf("unexpected final answer:\n%s", final)
	}
}

func TestParallelStepPersistsFullOutputAndDegradesOptionalBranch(t *testing.T) {
	workspace := t.TempDir()
	gateway := &GatewayService{config: &Config{Workspace: workspace}}
	factory := &AgentFactory{
		subAgents: make(map[string]Agent),
		testSubAgentCreator: func(config AgentConfig, _ string) (Agent, error) {
			if config.Name == "pipe_investment-advisory_optional-source" {
				return &investmentParallelTestAgent{name: config.Name, err: errors.New("source unavailable")}, nil
			}
			return &investmentParallelTestAgent{name: config.Name, output: "verified evidence from " + config.Name}, nil
		},
	}
	executor := NewPipelineExecutor(factory, gateway, log.New(io.Discard, "", 0))
	exec := &PipelineExecution{
		ID:           "pipe_test_parallel",
		PipelineName: "investment-advisory",
		Status:       "running",
		Steps:        []StepResult{{Name: "并行采集", Status: "running"}},
	}
	step := &PipelineStep{
		Name: "并行采集",
		Parallel: []PipelineStep{
			{Name: "required-source", NoTools: true, MaxIter: 1},
			{Name: "optional-source", NoTools: true, MaxIter: 1, ContinueOnError: true},
		},
	}

	executor.executeParallelSteps(context.Background(), exec, step, 0, "analyze target", "session")

	if exec.Status != "running" {
		t.Fatalf("execution status = %q, optional branch should not fail parent", exec.Status)
	}
	if exec.Steps[0].Status != "completed" {
		t.Fatalf("parallel parent status = %q", exec.Steps[0].Status)
	}
	for _, want := range []string{"verified evidence", "DATA_STATUS: fetch_failed", "source unavailable"} {
		if !strings.Contains(exec.Steps[0].Output, want) {
			t.Fatalf("merged output missing %q: %s", want, exec.Steps[0].Output)
		}
	}

	path := filepath.Join(workspace, buildStepOutputPath(exec, 0, "并行采集"))
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read persisted parallel output: %v", err)
	}
	if string(data) != exec.Steps[0].Output {
		t.Fatalf("persisted output differs from complete merged output\nfile=%q\nmemory=%q", string(data), exec.Steps[0].Output)
	}
}
