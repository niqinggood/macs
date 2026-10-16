package aiemployee

import (
	"strings"
	"testing"
)

func TestAIEmployeeMinimumProgressRejectsActionableQuestionnaireDeferral(t *testing.T) {
	pipeline := &Pipeline{
		AIEmployeeID: "employee-under-test",
		AIEmployeeOriginalTask: `请根据这两个来源制作一版方案：
https://example.com/product
https://example.com/platform`,
	}
	step := &PipelineStep{Name: "final"}
	output := `## 已知信息
- 已读取两个公开来源。

## 决定性缺口
1. 公司主体是什么？
2. 主产品是什么？
3. 融资阶段是否确定？
4. 目标金额是多少？
5. 是否已有客户？
6. 团队成员有哪些？

你回复后我会立刻开始制作并交付正式方案。`

	issues := aiEmployeeMinimumProgressIssues(pipeline, step, output)
	if len(issues) == 0 {
		t.Fatal("actionable questionnaire deferral was accepted")
	}
}

func TestAIEmployeeMinimumProgressAllowsUsefulV0WithThreeQuestions(t *testing.T) {
	pipeline := &Pipeline{
		AIEmployeeID:           "employee-under-test",
		AIEmployeeOriginalTask: "请分析 https://example.com/product 并形成初稿",
	}
	step := &PipelineStep{Name: "final"}
	output := `## 当前可用 v0
基于现有来源，建议先采用平台产品定位。以下给出当前事实、透明假设、结构初稿和三种收入情景。

## 待确认
1. 公司主体是什么？
2. 当前收入是多少？
3. 本轮目标金额是多少？`

	if issues := aiEmployeeMinimumProgressIssues(pipeline, step, output); len(issues) != 0 {
		t.Fatalf("useful v0 was rejected: %v", issues)
	}
}

func TestAIEmployeeInteractiveHandoffRequiresIndependentOptionBlocks(t *testing.T) {
	task := `请先让我选择信息起点：
①联网检索公开资料；
②读取我接下来上传的材料；
③两者结合。`
	output := `请选择信息起点：

① 联网检索公开资料

③ 两者结合

说明：①、②、③最终都可以形成相同目录。`
	issues := aiEmployeeInteractiveHandoffOutputIssues(task, output)
	if len(issues) != 1 || !strings.Contains(issues[0], "②") {
		t.Fatalf("a marker mentioned only in explanatory prose masked a missing option block: %v", issues)
	}

	covered := ensureAIEmployeeExplicitConfirmationCoverage(task, output)
	if !strings.Contains(covered, "②读取我接下来上传的材料") {
		t.Fatalf("missing option was not restored from the user's own text: %q", covered)
	}
}

func TestAIEmployeeMinimumProgressCountsOnlyClarificationSectionQuestions(t *testing.T) {
	pipeline := &Pipeline{
		AIEmployeeID:           "employee-under-test",
		AIEmployeeOriginalTask: "请分析 https://example.com/product 并形成完整初稿",
	}
	step := &PipelineStep{Name: "final"}
	output := `## 完整 v0
这里已经交付定位、方案、商业模式和风险分析。

## 未知字段
- 当前收入是多少？
- 客户数是多少？
- 团队背景是什么？
- 是否已有合同？

## 真正会改变结论的 3 个问题
1. 主产品优先级是什么？
2. 首批付费客户是谁？
3. 十二个月目标是什么？`

	if got := countAIEmployeeClarificationQuestions(output); got != 3 {
		t.Fatalf("clarification count=%d, want only the three explicit decision-changing questions", got)
	}
	if issues := aiEmployeeMinimumProgressIssues(pipeline, step, output); len(issues) != 0 {
		t.Fatalf("substantive v0 with an unknown-field inventory was rejected: %v", issues)
	}
}

func TestAIEmployeeMinimumProgressRejectsFourExplicitClarificationQuestions(t *testing.T) {
	pipeline := &Pipeline{
		AIEmployeeID:           "employee-under-test",
		AIEmployeeOriginalTask: "请分析 https://example.com/product 并形成完整初稿",
	}
	step := &PipelineStep{Name: "final"}
	output := `## 完整 v0
这里已经交付当前可形成的完整内容。

## 待确认
1. 主产品是什么？
2. 客户是谁？
3. 团队是谁？
4. 目标金额是多少？`
	if issues := aiEmployeeMinimumProgressIssues(pipeline, step, output); len(issues) == 0 {
		t.Fatal("four explicit clarification questions were accepted")
	}
}

func TestAIEmployeeMinimumProgressCountsGroupedQuestionSubfieldsAsThree(t *testing.T) {
	output := `# BP v0

这里已经交付当前证据允许的完整初稿。

## 决定本 BP 内容走向的 3 个关键问题

1. **主产品状态**
   - 当前是 demo、内测、POC 还是已商用？
   - 已有哪些可核验客户或使用数据？
2. **融资与团队**
   - 本轮轮次、金额和资金用途分别是什么？
   - 核心团队成员分别负责什么？
3. **对象关系**
   - 用户描述的能力与现有产品是什么关系？
   - 参考工具是否只用于比较？`

	if got := countAIEmployeeClarificationQuestions(output); got != 3 {
		t.Fatalf("grouped question count=%d, want 3", got)
	}
}

func TestAIEmployeeMinimumProgressRejectsConditionalDeferralEvenWithFewQuestions(t *testing.T) {
	pipeline := &Pipeline{
		AIEmployeeID:           "employee-under-test",
		AIEmployeeOriginalTask: "请分析 https://example.com/product 并形成初稿",
	}
	step := &PipelineStep{Name: "final"}
	output := `我已确认资料可读取。

1. 请确认目标读者？
2. 请确认时间范围？

你回复后我会开始生成并交付结果。`

	if issues := aiEmployeeMinimumProgressIssues(pipeline, step, output); len(issues) == 0 {
		t.Fatal("conditional delivery deferral with few questions was accepted")
	}
}

func TestAIEmployeeMinimumProgressAllowsCompletedDraftWithFutureFollowUp(t *testing.T) {
	pipeline := &Pipeline{
		AIEmployeeID:           "employee-under-test",
		AIEmployeeOriginalTask: "请根据 https://example.com/source 直接形成一份完整初稿。",
	}
	step := &PipelineStep{Name: "final"}
	output := `# 完整初稿

这里已经交付当前可形成的定位、方案、目录和逐章正文。

## 三个待确认问题

1. 当前客户是谁？
2. 是否已有收入？
3. 本轮目标金额是多少？

在你回复这 3 个问题之前，本初稿已经可以用于内部讨论；回复后可继续优化下一版。`

	if issues := aiEmployeeMinimumProgressIssues(pipeline, step, output); len(issues) != 0 {
		t.Fatalf("completed current draft with optional future iteration was rejected: %v", issues)
	}
}

func TestConditionalDeliveryDeferralUsesLocalSentenceScope(t *testing.T) {
	complete := `当前已完成并交付完整初稿。

在你回复这 3 个问题之前，本初稿已经可以使用。

附录说明了输出格式。`
	if containsConditionalDeliveryDeferral(complete) {
		t.Fatalf("unrelated delivery wording elsewhere in a complete document was treated as deferral")
	}

	for _, deferred := range []string{
		"你回复后我会开始生成并交付结果。",
		"请确认后：\n- 开始撰写完整报告",
	} {
		if !containsConditionalDeliveryDeferral(deferred) {
			t.Fatalf("actual conditional deferral was missed: %q", deferred)
		}
	}
}

func TestAIEmployeeMinimumProgressAllowsExplicitlyRequestedChoice(t *testing.T) {
	pipeline := &Pipeline{
		AIEmployeeID: "employee-under-test",
		AIEmployeeOriginalTask: `请先让我选择信息起点，再开始正式分析：
https://example.com/product
①联网检索；②读取材料；③两者结合。`,
	}
	step := &PipelineStep{Name: "final"}
	output := `## 请选择信息起点

① 我主动联网检索公开资料并标注来源
② 读取你接下来提供的材料
③ 两者结合

请直接回复 ①、② 或 ③。你选择后，我再按对应路径开始事实核验。`

	if issues := aiEmployeeMinimumProgressIssues(pipeline, step, output); len(issues) != 0 {
		t.Fatalf("explicitly requested choice was treated as delivery deferral: %v", issues)
	}
}

func TestAIEmployeeMinimumProgressStillRejectsEmptyInteractiveDeferral(t *testing.T) {
	pipeline := &Pipeline{
		AIEmployeeID: "employee-under-test",
		AIEmployeeOriginalTask: `请先让我选择信息起点，再开始正式分析：
https://example.com/product`,
	}
	step := &PipelineStep{Name: "final"}
	output := "等你回复后我会开始分析并交付结果。"

	if issues := aiEmployeeMinimumProgressIssues(pipeline, step, output); len(issues) == 0 {
		t.Fatal("empty deferral was accepted merely because the user requested an interaction")
	}
}

func TestAIEmployeeMinimumProgressPreservesExplicitMissingInputProtocol(t *testing.T) {
	pipeline := &Pipeline{
		AIEmployeeID:           "employee-under-test",
		AIEmployeeOriginalTask: "请处理这份材料",
	}
	step := &PipelineStep{Name: "intake", AllowMissingInput: true}
	output := "请提供材料。\nINPUT_STATUS: MISSING\n<!--PIPELINE_STOP-->"
	if issues := aiEmployeeMinimumProgressIssues(pipeline, step, output); len(issues) != 0 {
		t.Fatalf("explicit missing-input outcome was rejected: %v", issues)
	}
}

func TestAIEmployeeMinimumProgressRejectsInternalIntakeLedger(t *testing.T) {
	pipeline := &Pipeline{
		AIEmployeeID:           "employee-under-test",
		AIEmployeeOriginalTask: "请评估这些指标",
	}
	step := &PipelineStep{Name: "final"}
	output := `## 意图理解
目标结果：完成评估。

## 约束账本
系统硬约束如下。

## 执行边界
按规范我必须先等待脚本评分。`

	issues := aiEmployeeMinimumProgressIssues(pipeline, step, output)
	if len(issues) < 2 {
		t.Fatalf("internal execution framing was accepted: %v", issues)
	}
}

func TestAIEmployeeMinimumProgressAllowsRequestedScriptDiscussion(t *testing.T) {
	pipeline := &Pipeline{
		AIEmployeeID:           "employee-under-test",
		AIEmployeeOriginalTask: "请解释这个评分脚本为什么只能在八项指标齐全时运行",
	}
	step := &PipelineStep{Name: "final"}
	output := "这个脚本只能在八项指标齐全时运行，因为函数签名要求八个非空参数。"
	if issues := aiEmployeeMinimumProgressIssues(pipeline, step, output); len(issues) != 0 {
		t.Fatalf("requested script discussion was rejected: %v", issues)
	}
}

func TestMinimumProgressRepairPromptIsEmployeeAgnostic(t *testing.T) {
	prompt := buildAIEmployeeMinimumProgressRepairPrompt(
		"Analyze the supplied material.",
		[]string{"delivery deferred"},
		"Reply first and I will deliver later.",
	)
	for _, want := range []string{
		"best complete v0",
		"no more than three",
		"Do not condition the current deliverable",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("repair prompt missing %q: %s", want, prompt)
		}
	}
	for _, employeeSpecific := range []string{"fundraising", "融资", "PPT", "潜客"} {
		if strings.Contains(prompt, employeeSpecific) {
			t.Fatalf("repair prompt overfit employee %q: %s", employeeSpecific, prompt)
		}
	}
}

func TestStripAIEmployeeInternalMechanicLines(t *testing.T) {
	output := `# 发布审查
正文保留。
本次处理使用内部能力（example-policy-audit）。
use_skill 已成功执行。
公开产品名称 public-policy-suite 应当保留。
专业事项请复核。`
	cleaned := stripAIEmployeeInternalMechanicLines(output, "example-policy-audit")
	if strings.Contains(cleaned, "example-policy-audit") || strings.Contains(cleaned, "use_skill") {
		t.Fatalf("internal mechanics remained: %q", cleaned)
	}
	for _, want := range []string{"发布审查", "正文保留", "public-policy-suite", "专业事项请复核"} {
		if !strings.Contains(cleaned, want) {
			t.Fatalf("user-facing line %q was removed: %q", want, cleaned)
		}
	}
}

func TestStripAIEmployeeInternalMechanicLinesRemovesGenericControlMarkers(t *testing.T) {
	output := `# 可交付结果
正文保留。

INTAKE_STATUS: READY
SLOT_STATUS: FILLED
INPUT_STATUS: MISSING
<!--PIPELINE_STOP-->
结论保留。`

	cleaned := stripAIEmployeeInternalMechanicLines(output)
	for _, forbidden := range []string{
		"INTAKE_STATUS",
		"SLOT_STATUS",
		"INPUT_STATUS",
		"PIPELINE_STOP",
	} {
		if strings.Contains(cleaned, forbidden) {
			t.Fatalf("internal control marker %q remained: %q", forbidden, cleaned)
		}
	}
	for _, want := range []string{"可交付结果", "正文保留", "结论保留"} {
		if !strings.Contains(cleaned, want) {
			t.Fatalf("user-facing content %q was removed: %q", want, cleaned)
		}
	}
}

func TestSanitizeAIEmployeeTerminalOutputRunsBeforeQualityGate(t *testing.T) {
	task := "请根据现有公开材料给出一份完整的产品分析初稿。"
	output := `# 产品分析初稿

| 结论 | 来源性质 |
|---|---|
| 当前定位仍需验证 | 上游 Step 0 推断 |

这是基于现有材料形成的完整初稿。

## 当前 Pipeline 填充槽位
- slot_id：final_report

SLOT_STATUS: FILLED`

	cleaned := sanitizeAIEmployeeTerminalOutput(task, output)
	for _, forbidden := range []string{
		"上游 Step 0",
		"当前 Pipeline 填充槽位",
		"slot_id",
		"SLOT_STATUS",
	} {
		if strings.Contains(cleaned, forbidden) {
			t.Fatalf("internal terminal detail %q remained: %q", forbidden, cleaned)
		}
	}
	for _, want := range []string{
		"产品分析初稿",
		"当前定位仍需验证",
		"基于现有信息的推断",
		"这是基于现有材料形成的完整初稿",
	} {
		if !strings.Contains(cleaned, want) {
			t.Fatalf("user-facing content %q was removed: %q", want, cleaned)
		}
	}

	pipeline := &Pipeline{
		AIEmployeeID:           "employee-under-test",
		AIEmployeeOriginalTask: task,
	}
	if issues := aiEmployeeMinimumProgressIssues(pipeline, &PipelineStep{Name: "final"}, cleaned); len(issues) != 0 {
		t.Fatalf("sanitized complete draft was still rejected: %v\n%s", issues, cleaned)
	}
}

func TestSanitizeAIEmployeeTerminalOutputRewritesStandaloneInternalStepNumber(t *testing.T) {
	task := "请根据当前材料制作一份完整 BP。"
	output := `# BP v0

| 项目 | 取值 | 来源 |
|---|---|---|
| 文件版本 | BP v0（基于 Step 1 事实底稿） | 当前会话 |

这是可直接阅读的 BP 正文。`

	cleaned := sanitizeAIEmployeeTerminalOutput(task, output)
	if strings.Contains(strings.ToLower(cleaned), "step 1") {
		t.Fatalf("standalone internal step number survived terminal sanitization: %q", cleaned)
	}
	for _, want := range []string{"BP v0", "文件版本", "现有信息", "可直接阅读的 BP 正文"} {
		if !strings.Contains(cleaned, want) {
			t.Fatalf("user-facing content %q was lost while sanitizing step metadata: %q", want, cleaned)
		}
	}
}

func TestSanitizeAIEmployeeTerminalOutputPreservesRequestedInternalReview(t *testing.T) {
	task := "请审查 pipeline 中上游 Step 0 的推断是否正确。"
	output := "上游 Step 0 推断缺少可追溯证据。"
	cleaned := sanitizeAIEmployeeTerminalOutput(task, output)
	if cleaned != output {
		t.Fatalf("explicitly requested internal review was rewritten: %q", cleaned)
	}
}

func TestAIEmployeeUserFacingOutputRejectsInternalHandoffVocabulary(t *testing.T) {
	task := "先让我选择处理方式，再执行后面的工作。"
	output := `请选择处理方式。

- 下游约束账本已继承：先选择，再执行。
- 本步骤契约：现在输出三个选项。`
	issues := aiEmployeeUserFacingOutputIssues(task, output)
	if len(issues) == 0 {
		t.Fatalf("internal handoff vocabulary was accepted: %q", output)
	}

	internalReviewTask := "请审查 pipeline 的约束账本和本步骤契约。"
	if issues := aiEmployeeUserFacingOutputIssues(internalReviewTask, output); len(issues) != 0 {
		t.Fatalf("explicit internal-mechanism review was rejected: %q", issues)
	}

	stepOutput := "请回复 Step 0 已列出的字段。"
	if issues := aiEmployeeUserFacingOutputIssues(task, stepOutput); len(issues) == 0 {
		t.Fatalf("internal step number was accepted: %q", stepOutput)
	}
	if issues := aiEmployeeUserFacingOutputIssues("请审查 Step 0 的输出。", stepOutput); len(issues) != 0 {
		t.Fatalf("explicit step review was rejected: %q", issues)
	}
}

func TestAIEmployeeInteractiveHandoffRejectsAssumedChoiceAndOverexpansion(t *testing.T) {
	task := "请先让我选择处理方式，再执行正式工作。"
	assumed := "你已选择第三种方式，我现在等你确认其他字段。"
	if issues := aiEmployeeInteractiveHandoffOutputIssues(task, assumed); len(issues) != 1 {
		t.Fatalf("assumed user choice was not rejected: %q", issues)
	}

	overexpanded := "# 选择方式\n请选择一项。\n\n# 分析结果\n" +
		strings.Repeat("这是尚未经过用户选择的后续内容。", 100) +
		"\n\n# 后续交付\n稍后继续。"
	if issues := aiEmployeeInteractiveHandoffOutputIssues(task, overexpanded); len(issues) != 1 {
		t.Fatalf("overexpanded interaction handoff was not rejected: %q", issues)
	}

	concise := "请选择：①直接分析；②读取材料；③两者结合。"
	if issues := aiEmployeeInteractiveHandoffOutputIssues(task, concise); len(issues) != 0 {
		t.Fatalf("concise interaction handoff was rejected: %q", issues)
	}

	explicitChoiceTask := "我选择第三种方式，请先确认范围，再执行正式工作。"
	if issues := aiEmployeeInteractiveHandoffOutputIssues(explicitChoiceTask, assumed); len(issues) != 0 {
		t.Fatalf("an explicitly supplied user choice was rejected: %q", issues)
	}

	ordinaryTask := "直接分析材料并给出完整报告。"
	if issues := aiEmployeeInteractiveHandoffOutputIssues(ordinaryTask, overexpanded); len(issues) != 0 {
		t.Fatalf("non-interactive task received the handoff guard: %q", issues)
	}
}

func TestAIEmployeeTaskRequestsInteractiveHandoffHonorsNegation(t *testing.T) {
	if aiEmployeeTaskRequestsInteractiveHandoff(
		"fundraising-advisor",
		"请直接生成完整 BP，不要让我确认，也不要先问问题。",
	) {
		t.Fatal("negative confirmation instruction was treated as an interaction request")
	}
	if !aiEmployeeTaskRequestsInteractiveHandoff(
		"fundraising-advisor",
		"请先让我选择信息起点：①联网检索；②读取材料；③两者结合。",
	) {
		t.Fatal("explicit positive interaction request was not preserved")
	}
	if aiEmployeeTaskRequestsInteractiveHandoff(
		"fundraising-advisor",
		"请先主动获取产品和公司情况，再直接做一份 BP；未知信息标为待验证假设，不要停下来让我确认。",
	) {
		t.Fatal("a negated interaction phrase with intervening words was treated as a handoff request")
	}
	if !aiEmployeeTaskRequestsInteractiveHandoff(
		"fundraising-advisor",
		"资料不确定时请先问我，再写 BP。",
	) {
		t.Fatal("a positive conditional request was mistaken for interaction rejection")
	}
}

func TestFundraisingMinimumProgressDoesNotTreatNegativeConfirmationAsHandoff(t *testing.T) {
	pipeline := &Pipeline{
		AIEmployeeID:           "fundraising-advisor",
		AIEmployeeOriginalTask: "请直接生成完整 BP，不要让我确认，也不要先问问题。",
	}
	step := &PipelineStep{Name: "融资专业分析与交付"}
	output := `# BP v0

## 公司与产品
完整的当前版本正文。

## 市场与商业模式
未知信息使用 [待补]。

## 融资计划
轮次、金额与用途均为 [待补]。`
	if issues := aiEmployeeMinimumProgressIssues(pipeline, step, output); len(issues) != 0 {
		t.Fatalf("direct BP output was rejected as an interaction handoff: %q", issues)
	}
}

func TestAIEmployeeInteractiveHandoffCompactionPromptIsGeneric(t *testing.T) {
	prompt := buildAIEmployeeInteractiveHandoffCompactionPrompt(
		"original request with three choices",
		"long draft",
	)
	for _, want := range []string{
		"earliest unresolved choice or confirmation",
		"every option and every confirmation field explicitly requested",
		"Do not claim the user already selected",
		"under 1000 Chinese characters",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("compaction prompt missing %q:\n%s", want, prompt)
		}
	}
	for _, forbidden := range []string{"fundraising", "融资", "BP", "CSDN"} {
		if strings.Contains(prompt, forbidden) {
			t.Fatalf("compaction prompt overfit domain %q:\n%s", forbidden, prompt)
		}
	}
}

func TestAIEmployeeLeadingMetaPreambleIsRejectedAndStrippedAcrossDomains(t *testing.T) {
	pipeline := &Pipeline{
		AIEmployeeID:           "employee-under-test",
		AIEmployeeOriginalTask: "根据现有材料制定发布方案，并直接给出可执行初稿。",
	}
	step := &PipelineStep{Name: "final"}
	output := `I now have enough to respond. Based on the user's request, they want a launch plan.

The supplied materials describe a desktop client, an API, and an operations dashboard.

Several implementation choices cannot be inferred. Per rules, I must not invent them.

产品发布方案（v0）

一、当前可执行范围
先发布桌面端与 API，未知字段以待验证项标注，不得编造缺失数据。`

	if issues := aiEmployeeMinimumProgressIssues(pipeline, step, output); len(issues) == 0 {
		t.Fatal("model-facing response preamble was accepted")
	}
	cleaned := stripAIEmployeeLeadingMetaPreamble(output)
	if strings.Contains(cleaned, "I now have enough") ||
		strings.Contains(cleaned, "the user's request") ||
		strings.Contains(cleaned, "Per rules") {
		t.Fatalf("model-facing preamble remained: %q", cleaned)
	}
	for _, want := range []string{"产品发布方案（v0）", "一、当前可执行范围", "未知字段以待验证项标注", "不得编造缺失数据"} {
		if !strings.Contains(cleaned, want) {
			t.Fatalf("user-facing content %q was removed: %q", want, cleaned)
		}
	}
}

func TestAIEmployeeLeadingMetaPreambleUsesMarkdownAnswerBoundary(t *testing.T) {
	output := `I need to respond to the user's request. The provided URLs are sufficient.
I must not invent missing details.

# Migration Runbook

## Verified inputs
- Two service manifests are available.`

	cleaned := stripAIEmployeeLeadingMetaPreamble(output)
	if !strings.HasPrefix(cleaned, "# Migration Runbook") {
		t.Fatalf("markdown answer boundary was not preserved: %q", cleaned)
	}
	if strings.Contains(cleaned, "I need to respond") {
		t.Fatalf("leading response plan remained: %q", cleaned)
	}
}

func TestAIEmployeeLeadingMetaPreambleRejectsVerifiableContentVariant(t *testing.T) {
	output := `Now I have verifiable content from both URLs. Both pages cover different products. Here is the evidence table:

| Fact | Source | Status |
|---|---|---|
| The user supplied a separate direction | User input | Self-reported |`
	cleaned := stripAIEmployeeLeadingMetaPreamble(output)
	if strings.Contains(cleaned, "Now I have verifiable content") {
		t.Fatalf("verifiable-content planning preamble remained: %q", cleaned)
	}
	if !strings.HasPrefix(cleaned, "| Fact | Source | Status |") {
		t.Fatalf("evidence table boundary was not preserved: %q", cleaned)
	}
}

func TestAIEmployeeLeadingMetaPreambleRejectsEnoughVerifiedVariant(t *testing.T) {
	output := `I have enough verified content from both pages.

| Fact | Source | Status |
|---|---|---|
| The direction came from the user | User input | Self-reported |`
	cleaned := stripAIEmployeeLeadingMetaPreamble(output)
	if strings.Contains(cleaned, "I have enough verified content") {
		t.Fatalf("enough-verified planning preamble remained: %q", cleaned)
	}
	if !strings.HasPrefix(cleaned, "| Fact | Source | Status |") {
		t.Fatalf("evidence table boundary was not preserved: %q", cleaned)
	}
}

func TestAIEmployeeLeadingMetaPreambleDoesNotRemoveUserFacingEnglishIntroduction(t *testing.T) {
	output := `Based on the request, this report evaluates the current release risks without adding unsupported facts.

# Findings

The deployment manifest is internally consistent.`

	if detected, _ := aiEmployeeLeadingMetaPreamble(output); detected {
		t.Fatal("ordinary user-facing introduction was misclassified as private reasoning")
	}
	if cleaned := stripAIEmployeeLeadingMetaPreamble(output); cleaned != output {
		t.Fatalf("ordinary user-facing introduction changed: %q", cleaned)
	}
}
