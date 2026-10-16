package app

import (
	"context"
	"encoding/json"
	"io"
	"io/fs"
	"log"
	"os"
	"strings"
	"testing"

	"mac/internal/aiemployee"
)

func defaultEmployeeByID(id string) *AIEmployeeConfig {
	employees := defaultAIEmployees()
	for i := range employees {
		if employees[i].ID == id {
			return &employees[i]
		}
	}
	return nil
}

func TestDefaultFundraisingAdvisorUsesHuanzhiSkill(t *testing.T) {
	advisor := defaultEmployeeByID("fundraising-advisor")
	if advisor == nil {
		t.Fatal("missing fundraising-advisor")
	}
	if advisor.Name != "融资专家" || advisor.AtName != "融资专家" {
		t.Fatalf("unexpected identity: name=%q at=%q", advisor.Name, advisor.AtName)
	}
	if advisor.Hidden {
		t.Fatal("fundraising advisor should be visible")
	}
	if advisor.Direct == nil {
		t.Fatal("fundraising advisor should use an adaptive direct workflow")
	}
	if advisor.Direct.MaxIter < 20 {
		t.Fatalf("fundraising advisor iteration budget too small: %d", advisor.Direct.MaxIter)
	}
	if len(advisor.Direct.RequiredSkills) != 1 || advisor.Direct.RequiredSkills[0] != "huanzhi-fa-fundraising" {
		t.Fatalf("fundraising advisor required skills = %v", advisor.Direct.RequiredSkills)
	}
	for _, tool := range []string{
		"list_skills", "use_skill", "read_file", "extract", "vision",
		"web_search", "web_fetch", "persistent_shell", "write_file",
	} {
		if !containsString(advisor.Direct.Tools, tool) {
			t.Fatalf("fundraising advisor tools missing %q: %v", tool, advisor.Direct.Tools)
		}
	}
	for _, want := range []string{
		"上游已经读取指定材料",
		"自有项目的资料可以支撑 BP",
		"直接写完整 v0",
		"主动补出有依据的目标客户、商业模式、获客路径",
		"规划估算",
		"不得只写金额未知",
		"项目方后续替换清单",
		"不要在正文反复写未知",
	} {
		if !strings.Contains(advisor.Direct.Prompt, want) {
			t.Fatalf("fundraising advisor prompt missing %q", want)
		}
	}
	for _, want := range []string{
		"不处理股票行情",
		"材料角色由用户语义决定",
		"我的/我们的/本公司/本项目/我产品的资料",
		"本轮直接交付完整 v0 和真实可编辑 PPTX",
		"主动补出有依据的目标客户、商业模式、获客路径",
		"规划估算（非项目事实）",
		"不得只写金额未知",
		"项目方后续替换清单",
		"不要让每章都只剩未知或占位符",
		"专业人士复核",
	} {
		if !strings.Contains(advisor.SystemMessage, want) {
			t.Fatalf("fundraising advisor system message missing %q", want)
		}
	}
}

func TestFundraisingAdvisorResearchesBeforeUsingProfessionalSkill(t *testing.T) {
	advisor := defaultEmployeeByID("fundraising-advisor")
	if advisor == nil || advisor.Direct == nil {
		t.Fatal("missing fundraising advisor direct config")
	}
	tools := aiemployee.DirectToolsForRequest(advisor.Direct, "请基于公司和产品资料制作融资 BP")
	pipeline := buildAIEmployeeDirectPipeline(
		advisor.ID,
		"direct_fundraising-advisor",
		advisor.Direct,
		tools,
		advisor.Direct.MaxIter,
		"请基于公司和产品资料制作融资 BP",
	)
	if len(pipeline.Steps) != 3 {
		t.Fatalf("fundraising pipeline has %d business steps, want research, professional delivery, then PPTX", len(pipeline.Steps))
	}
	research, delivery, presentation := pipeline.Steps[0], pipeline.Steps[1], pipeline.Steps[2]
	if research.Name != "公司、产品与对比研究" || delivery.Name != "融资专业分析与交付" {
		t.Fatalf("unexpected fundraising step order: %q -> %q", research.Name, delivery.Name)
	}
	if presentation.Name != "融资BP演示文稿生成" {
		t.Fatalf("BP creation did not append presentation delivery: %#v", pipeline.Steps)
	}
	for _, want := range []string{
		"材料角色", "用户语义而非 URL", "我的/我们的/本公司/本项目/我产品的资料",
		"竞品、对标、参考产品", "唯一可识别的融资标的", "实际读取用户指定的 URL",
		"直接竞品、替代方案、人工流程或维持现状", "建议性推演", "不要重复输出“未知/来源等级”",
	} {
		if !strings.Contains(research.Prompt, want) {
			t.Fatalf("research step missing general evidence rule %q", want)
		}
	}
	if len(research.RequiredSkills) != 0 ||
		containsString(research.Tools, "use_skill") ||
		containsString(research.Tools, "list_skills") ||
		containsString(research.Tools, "write_file") {
		t.Fatalf("research step activated professional or artifact tools too early: %#v", research)
	}
	for _, want := range []string{"read_file", "extract", "vision", "web_search", "web_fetch"} {
		if !containsString(research.Tools, want) {
			t.Fatalf("research step missing %q: %v", want, research.Tools)
		}
	}
	if !research.ContinueOnError {
		t.Fatal("a partial research outage should not prevent a transparent professional answer")
	}
	if len(delivery.RequiredSkills) != 1 || delivery.RequiredSkills[0] != "huanzhi-fa-fundraising" {
		t.Fatalf("professional skill was not isolated to the delivery step: %v", delivery.RequiredSkills)
	}
	for _, want := range []string{
		"上游已经读取指定材料", "使用融资专业 skill", "自有项目资料",
		"直接完成当前版本", "规划估算（非项目事实）", "项目方后续替换清单",
		"不要在每章反复输出“未知、待补、来源等级”", "不要求确认",
	} {
		if !strings.Contains(delivery.Prompt, want) {
			t.Fatalf("delivery step missing research-to-skill handoff %q", want)
		}
	}
	for _, forbidden := range []string{"list_skills", "persistent_shell"} {
		if containsString(delivery.Tools, forbidden) {
			t.Fatalf("chat-first BP delivery retained unnecessary internal tool %q: %v", forbidden, delivery.Tools)
		}
	}
	if !containsString(delivery.Tools, "use_skill") {
		t.Fatalf("host-required skill activation channel was removed: %v", delivery.Tools)
	}
	for _, forbidden := range []string{"web_search", "web_fetch", "write_file"} {
		if containsString(delivery.Tools, forbidden) {
			t.Fatalf("BP writer retained a tool owned by another stage %q: %v", forbidden, delivery.Tools)
		}
	}
	if len(presentation.RequiredSkills) != 1 || presentation.RequiredSkills[0] != "ppt-art-director" {
		t.Fatalf("presentation skill was not isolated to the PPT step: %v", presentation.RequiredSkills)
	}
	for _, want := range []string{"use_skill", "read_file", "write_file", "edit_file", "persistent_shell", "vision"} {
		if !containsString(presentation.Tools, want) {
			t.Fatalf("presentation step missing %q: %v", want, presentation.Tools)
		}
	}
	if presentation.MaxIter != 80 {
		t.Fatalf("presentation iteration budget = %d, want 80", presentation.MaxIter)
	}
	if presentation.ArtifactContract == nil ||
		len(presentation.ArtifactContract.Paths) != 1 ||
		presentation.ArtifactContract.Paths[0] != ".openkitty/storage/fundraising_bp_${execution_id}.pptx" ||
		presentation.ArtifactContract.MinBytes < 10*1024 {
		t.Fatalf("presentation artifact contract is not run-scoped and validated: %#v", presentation.ArtifactContract)
	}
	for _, want := range []string{
		"真实可编辑 PPTX", "ppt-art-director", "scaffold_task_pptx.py",
		"fundraising_bp_work_${execution_id}/build_deck.py", "规划估算", "项目方后续应替换",
		"逐页 builder", "至少使用 5 种真正不同的构图家族",
	} {
		if !strings.Contains(presentation.Prompt, want) {
			t.Fatalf("presentation step missing %q", want)
		}
	}
	for _, want := range []string{"一次抓取成功或明确失败后不重复", "换一个替代来源"} {
		if !strings.Contains(research.Prompt, want) {
			t.Fatalf("research step missing bounded retrieval rule %q", want)
		}
	}
}

func TestFundraisingAdvisorTreatsOwnedReferenceMaterialAsDraftInput(t *testing.T) {
	advisor := defaultEmployeeByID("fundraising-advisor")
	if advisor == nil || advisor.Direct == nil {
		t.Fatal("missing fundraising advisor")
	}
	task := "@融资专家 我想从零制作一份融资 BP。我产品的参考资料是：https://example.com/product"
	pipeline := buildAIEmployeeDirectPipeline(
		advisor.ID,
		"direct_fundraising-advisor",
		advisor.Direct,
		aiemployee.DirectToolsForRequest(advisor.Direct, task),
		advisor.Direct.MaxIter,
		task,
	)
	if len(pipeline.Steps) != 3 {
		t.Fatalf("owned reference material should produce research, BP, and PPT steps: %#v", pipeline.Steps)
	}
	joined := pipeline.Steps[0].Prompt + "\n" + pipeline.Steps[1].Prompt
	for _, want := range []string{
		"用户语义而非 URL", "我产品的资料", "唯一可识别的融资标的",
		"主动补出有依据的目标客户、商业模式、获客路径", "规划估算（非项目事实）",
		"项目方后续替换清单", "不要在每章反复输出“未知、待补、来源等级”",
		"不得只写金额未知",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("direct BP pipeline missing non-blocking drafting rule %q", want)
		}
	}
	for _, forbidden := range []string{"先确认公司", "填写完整画像", "决定性问题"} {
		if strings.Contains(joined, forbidden) {
			t.Fatalf("direct BP pipeline retained questionnaire rule %q", forbidden)
		}
	}
}

func TestHostActivatedRequiredSkillIsHiddenFromStepModel(t *testing.T) {
	step := &PipelineStep{
		Tools:          []string{"use_skill", "read_file", "vision"},
		RequiredSkills: []string{"huanzhi-fa-fundraising"},
	}
	got := pipelineStepModelExecutionTools(step, stepToolAllowlist(step))
	if containsString(got, "use_skill") {
		t.Fatalf("model could reactivate or switch skills after host activation: %v", got)
	}
	for _, want := range []string{"read_file", "vision"} {
		if !containsString(got, want) {
			t.Fatalf("model tool allowlist lost %q: %v", want, got)
		}
	}
}

func TestFundraisingAdvisorRetainsCalculationToolsForNonBPWork(t *testing.T) {
	advisor := defaultEmployeeByID("fundraising-advisor")
	if advisor == nil || advisor.Direct == nil {
		t.Fatal("missing fundraising advisor direct config")
	}
	task := "请测算本轮融资后的 Cap Table 稀释。"
	tools := aiemployee.DirectToolsForRequest(advisor.Direct, task)
	pipeline := buildAIEmployeeDirectPipeline(
		advisor.ID,
		"direct_fundraising-advisor",
		advisor.Direct,
		tools,
		advisor.Direct.MaxIter,
		task,
	)
	if len(pipeline.Steps) != 2 {
		t.Fatalf("unexpected pipeline shape: %#v", pipeline.Steps)
	}
	delivery := pipeline.Steps[1]
	if !containsString(delivery.Tools, "persistent_shell") {
		t.Fatalf("calculation task lost persistent_shell: %v", delivery.Tools)
	}
	for _, forbidden := range []string{"list_skills"} {
		if containsString(delivery.Tools, forbidden) {
			t.Fatalf("host-activated skill exposed duplicate activation tool %q: %v", forbidden, delivery.Tools)
		}
	}
	if !containsString(delivery.Tools, "use_skill") {
		t.Fatalf("host-required skill activation channel was removed: %v", delivery.Tools)
	}
}

func TestOtherDirectEmployeesRemainSingleStep(t *testing.T) {
	direct := &DirectConfig{Prompt: "回答问题", Tools: []string{"web_search"}, MaxIter: 8}
	pipeline := buildAIEmployeeDirectPipeline(
		"generic-assistant",
		"direct_generic-assistant",
		direct,
		direct.Tools,
		direct.MaxIter,
		"回答这个通用问题",
	)
	if len(pipeline.Steps) != 1 || pipeline.Steps[0].Name != "respond" {
		t.Fatalf("non-fundraising direct employee changed shape: %#v", pipeline.Steps)
	}
}

func TestFundraisingAdvisorPreservesUserChoiceBeforeResearch(t *testing.T) {
	advisor := defaultEmployeeByID("fundraising-advisor")
	if advisor == nil || advisor.Direct == nil {
		t.Fatal("missing fundraising advisor direct config")
	}
	task := "请先让我选择信息起点：①联网检索；②读取材料；③两者结合。选择后再开始制作 BP。"
	tools := aiemployee.DirectToolsForRequest(advisor.Direct, task)
	pipeline := buildAIEmployeeDirectPipeline(
		advisor.ID,
		"direct_fundraising-advisor",
		advisor.Direct,
		tools,
		advisor.Direct.MaxIter,
		task,
	)
	if len(pipeline.Steps) != 1 || pipeline.Steps[0].Name != "融资需求选择与确认" {
		t.Fatalf("explicit user choice was not kept ahead of research: %#v", pipeline.Steps)
	}
	step := pipeline.Steps[0]
	if !step.NoTools || len(step.Tools) != 0 || len(step.RequiredSkills) != 0 {
		t.Fatalf("choice-only turn must not research or activate the professional skill: %#v", step)
	}
}

func TestFundraisingAdvisorNegativeConfirmationRequestStillDeliversBP(t *testing.T) {
	advisor := defaultEmployeeByID("fundraising-advisor")
	if advisor == nil || advisor.Direct == nil {
		t.Fatal("missing fundraising advisor direct config")
	}
	task := "请直接生成完整 BP，不要让我确认，也不要先问问题。"
	tools := aiemployee.DirectToolsForRequest(advisor.Direct, task)
	pipeline := buildAIEmployeeDirectPipeline(
		advisor.ID,
		"direct_fundraising-advisor",
		advisor.Direct,
		tools,
		advisor.Direct.MaxIter,
		task,
	)
	if len(pipeline.Steps) != 3 ||
		pipeline.Steps[0].Name != "公司、产品与对比研究" ||
		pipeline.Steps[1].Name != "融资专业分析与交付" ||
		pipeline.Steps[2].Name != "融资BP演示文稿生成" {
		t.Fatalf("negative confirmation instruction did not route to direct BP delivery: %#v", pipeline.Steps)
	}
}

func TestFundraisingAdvisorChatOnlyBPDoesNotCreatePPT(t *testing.T) {
	advisor := defaultEmployeeByID("fundraising-advisor")
	if advisor == nil || advisor.Direct == nil {
		t.Fatal("missing fundraising advisor direct config")
	}
	task := "请直接在聊天中生成完整 BP，不要让我确认，不要保存文件。"
	tools := aiemployee.DirectToolsForRequest(advisor.Direct, task)
	pipeline := buildAIEmployeeDirectPipeline(
		advisor.ID,
		"direct_fundraising-advisor",
		advisor.Direct,
		tools,
		advisor.Direct.MaxIter,
		task,
	)
	if len(pipeline.Steps) != 2 ||
		pipeline.Steps[0].Name != "公司、产品与对比研究" ||
		pipeline.Steps[1].Name != "融资专业分析与交付" {
		t.Fatalf("chat-only BP unexpectedly created an artifact step: %#v", pipeline.Steps)
	}
	if !containsString(tools, "write_file") {
		t.Fatalf("configured write_file was removed before execution: %v", tools)
	}
	policyCtx := withUserToolPolicy(context.Background(), task)
	if blocked, _ := userToolPolicyBlocksTool(policyCtx, ToolCall{Name: "write_file"}, nil); !blocked {
		t.Fatal("the user's explicit no-file instruction was not enforced at execution")
	}
}

func TestFundraisingAdvisorBPReviewDoesNotCreatePPTByDefault(t *testing.T) {
	task := "请评价我现有 BP 的问题并修改正文。"
	if fundraisingBPDeckRequest(task) {
		t.Fatalf("BP review was mistaken for a net-new presentation request: %q", task)
	}
	if !fundraisingBPDeckRequest("你研究下 OpenKitty，给我做一个 BP 计划书") {
		t.Fatal("net-new BP creation did not request the default presentation")
	}
	if !fundraisingBPDeckRequest("请把这份 BP 制作成投资人路演 PPTX") {
		t.Fatal("explicit BP presentation request was not recognized")
	}
	if fundraisingBPDeckRequest("请做一份 BP，但只在聊天中输出，不要保存文件") {
		t.Fatal("explicit chat-only opt-out was ignored")
	}
}

func TestFundraisingAdvisorMentionDoesNotConflictWithSecuritiesAdvisor(t *testing.T) {
	manager := NewAIEmployeeManager(nil, log.New(io.Discard, "", 0))
	manager.Register(defaultAIEmployees())

	tests := []struct {
		message string
		wantID  string
	}{
		{"@融资专家 请根据公开资料制作融资BP", "fundraising-advisor"},
		{"@投融资顾问 评估我们是否适合启动A轮", "fundraising-advisor"},
		{"@焕智FA 审查这份TS", "fundraising-advisor"},
		{"@融资顾问 帮我准备Data Room", "fundraising-advisor"},
		{"@投融资专家 从VC角度评估这个项目", "fundraising-advisor"},
		{"@融资助手 帮我整理融资材料", "fundraising-advisor"},
		{"@融资规划师 设计融资节奏", "fundraising-advisor"},
		{"@融资专员 说明这些指标能支持什么", "fundraising-advisor"},
		{"@投融资专员 审阅这份BP", "fundraising-advisor"},
		{"@FA顾问 帮我看TS", "fundraising-advisor"},
		{"@BP专家 修改商业计划书", "fundraising-advisor"},
		{"@投资顾问 分析今天A股大盘", "investment-advisor"},
	}
	for _, test := range tests {
		cfg, task := manager.MatchAtName(test.message)
		if cfg == nil || cfg.ID != test.wantID {
			t.Fatalf("MatchAtName(%q) = %#v, want %s", test.message, cfg, test.wantID)
		}
		if strings.TrimSpace(task) == "" || strings.Contains(task, "@") {
			t.Fatalf("MatchAtName(%q) extracted invalid task %q", test.message, task)
		}
	}
}

func TestAgentOSRoutesFundraisingAdvisorAndHidesInternalID(t *testing.T) {
	manager := NewAIEmployeeManager(nil, log.New(io.Discard, "", 0))
	manager.Register(defaultAIEmployees())
	input := `调用方提供的旧角色说明：先询问完整资产、负债和风险偏好。

用户原始任务：
@投融资顾问 我们是企业软件公司，ARR 1200万元，准备启动A轮，请评估准备度`
	cfg, task, current := matchAgentOSExplicitAIEmployee(manager, input)
	if cfg == nil || cfg.ID != "fundraising-advisor" {
		t.Fatalf("explicit fundraising employee was not matched: %#v", cfg)
	}
	if !strings.Contains(task, "ARR 1200万元") || !strings.Contains(current, "@投融资顾问") {
		t.Fatalf("current fundraising request was not preserved: task=%q current=%q", task, current)
	}
	for _, stale := range []string{"完整资产", "负债", "风险偏好"} {
		if strings.Contains(task, stale) {
			t.Fatalf("stale caller wrapper leaked into fundraising task: %q", task)
		}
	}

	gateway := &GatewayService{aeManager: manager}
	response := gateway.agentOSTaskResponse(
		&GatewayTask{
			ID:           "agt-fundraising",
			Status:       "running",
			CurrentPhase: "ai_employee:fundraising-advisor",
		},
		false,
	)
	if response["phase"] != "融资专家" ||
		response["phase_label"] != "融资专家" {
		t.Fatalf("public phase label leaked internal identity: %#v", response)
	}
}

func TestEmbeddedHuanzhiFASkillIsIndexedAndRunnable(t *testing.T) {
	const root = "builtin/business-finance/huanzhi-fa-fundraising"
	for _, relative := range []string{
		"SKILL.md",
		"LICENSE",
		"agents/openai.yaml",
		"references/fundraising-side.md",
		"references/term-sheet-review.md",
		"references/investment-side.md",
		"scripts/funding_diagnosis.py",
		"scripts/test_funding_diagnosis.py",
	} {
		if _, err := fs.ReadFile(builtinSkillsFS, root+"/"+relative); err != nil {
			t.Fatalf("embedded fundraising skill missing %s: %v", relative, err)
		}
	}

	indexData, err := os.ReadFile("builtin/OPENKITTY_INDEX.json")
	if err != nil {
		t.Fatal(err)
	}
	var index map[string]struct {
		Skills []string `json:"skills"`
	}
	if err := json.Unmarshal(indexData, &index); err != nil {
		t.Fatal(err)
	}
	if !containsString(index["business"].Skills, "huanzhi-fa-fundraising") {
		t.Fatalf("business skill index missing huanzhi-fa-fundraising: %v", index["business"].Skills)
	}

	loader := NewSkillsLoader(t.TempDir(), "Assistant", "")
	if err := loader.LoadBuiltinSkillsFromFS(builtinSkillsFS, "builtin"); err != nil {
		t.Fatal(err)
	}
	skill, err := loader.GetSkill("huanzhi-fa-fundraising")
	if err != nil {
		t.Fatal(err)
	}
	if !skill.Embedded || !skill.RequiresRuntime {
		t.Fatalf("fundraising skill should be an embedded runtime skill: %#v", skill)
	}
	for _, want := range []string{
		"Trigger Conditions",
		"startup fundraising",
		"Term Sheet",
		"不要用于二级市场行情",
		"scripts/funding_diagnosis.py",
		"高频概念基准",
		"不能先拿优先金额再参与剩余分配",
		"不等同于借款、保本或到期还本",
		"舒适带",
		"禁止空壳交付",
		"规划估算（非项目事实）",
		"项目方后续替换清单",
		"不得输出由 `[待补]`、`未知`、`来源等级未知`",
		"不得只写“融资金额未知/待补”",
		"OpenKitty 工作模式",
		"承接事实底稿",
		"当前步骤完成业务成稿",
	} {
		if !strings.Contains(skill.Raw, want) {
			t.Fatalf("fundraising skill missing bilingual trigger or runtime contract %q", want)
		}
	}
	resourceChecks := map[string][]string{
		"references/term-sheet-review.md": {
			"不能先取优先金额后再参与剩余分配",
			"不得把“非参与型”解释为“先拿回本金，再和普通股东按比例分剩余”",
		},
		"references/fundraising-side.md": {
			"只有毛利率或 NRR 时标为 `partial`",
			"不得宣称某指标达到 A 轮、B 轮或“行业标准”",
			"严禁为了避免虚构而输出空风险表、空里程碑表或连续占位符",
			"0–3、3–6、6–12 个月",
		},
	}
	for relative, wants := range resourceChecks {
		data, err := fs.ReadFile(builtinSkillsFS, root+"/"+relative)
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range wants {
			if !strings.Contains(string(data), want) {
				t.Fatalf("%s missing professional baseline %q", relative, want)
			}
		}
	}
}

func TestFundraisingAdvisorRoutingExamplesStayInPrivateMarkets(t *testing.T) {
	advisor := defaultEmployeeByID("fundraising-advisor")
	if advisor == nil {
		t.Fatal("missing fundraising-advisor")
	}
	joined := strings.Join(advisor.GetRoutingExamples(), "\n")
	for _, want := range []string{"A轮融资", "TS", "一级市场项目"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("routing examples missing %q: %s", want, joined)
		}
	}
	for _, forbidden := range []string{"股票", "大盘", "K线", "持仓"} {
		if strings.Contains(joined, forbidden) {
			t.Fatalf("routing examples must not attract public-market task %q: %s", forbidden, joined)
		}
	}
}
