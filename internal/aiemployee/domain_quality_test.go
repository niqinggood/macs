package aiemployee

import (
	"strings"
	"testing"
)

func TestFundraisingDomainQualityFindsUnsupportedStageBenchmarks(t *testing.T) {
	pipeline := &Pipeline{
		AIEmployeeID:           "fundraising-advisor",
		AIEmployeeOriginalTask: "@投融资顾问 评估融资准备度",
	}
	output := `ARR 1800 万、同比 70%，属于 A 轮讨论区间。
这是机构最常见的一票否决项之一。
没有可比来源时，不得宣称达到某轮次门槛。`
	issues := aiEmployeeDomainOutputIssues(pipeline, output)
	if len(issues) != 2 {
		t.Fatalf("issues = %v, want two positive unsupported claims and no caution", issues)
	}
}

func TestFundraisingDomainQualityFindsUnsupportedRelativeAndConsensusClaims(t *testing.T) {
	pipeline := &Pipeline{
		AIEmployeeID:           "fundraising-advisor",
		AIEmployeeOriginalTask: "@投融资顾问 评估融资准备度",
	}
	output := `毛利率 78% 在企业软件里偏上（未对照任何外部基准）。
A 轮机构对此高度敏感，是常见否决信号。
没有可比样本时，不能据此判断毛利率偏上。`
	issues := aiEmployeeDomainOutputIssues(pipeline, output)
	if len(issues) != 2 {
		t.Fatalf("issues = %v, want unsupported comparison and consensus claim only", issues)
	}
}

func TestFundraisingDomainQualityFindsUnsupportedMetricCausalityAndThresholds(t *testing.T) {
	pipeline := &Pipeline{
		AIEmployeeID:           "fundraising-advisor",
		AIEmployeeOriginalTask: "@融资专家 说明这些指标能支持什么，不能支持什么",
	}
	output := `ARR 1800 万元，同比增长 70%，通常意味着已完成产品-市场早期验证。
毛利率 78% 处于较高水平，说明产品具备较好的复制经济性，单位交付成本控制良好，支持“软件而非项目”这一定性。
连续 2 轮提价或净价留存维持 NRR > 100% 才有意义。
没有可比来源时，不能据此判断这些指标高低。`
	issues := aiEmployeeDomainOutputIssues(pipeline, output)
	if len(issues) != 3 {
		t.Fatalf("issues = %v, want three unsupported metric claims", issues)
	}
}

func TestFundraisingDomainQualityCarriesMetricHeadingIntoClaimEvidenceWindow(t *testing.T) {
	pipeline := &Pipeline{
		AIEmployeeID:           "fundraising-advisor",
		AIEmployeeOriginalTask: "@融资专员 说明这些指标能支持什么、不能支持什么，不使用无来源门槛",
	}
	output := `### 1. ARR 1800 万元、YoY 70%
- **能支持**：已形成规模化的订阅收入基础。
- **能支持**：增长进入投资人愿意看的体量区间。
- **不能支持**：没有可比来源时，不能判断是否达到某轮次门槛。

### 2. 毛利率 78%
- **能支持**：高于典型纯软件 60–70% 的中位水平。
- **不能支持**：没有可比样本时，不能判断毛利率高低。`
	issues := aiEmployeeDomainOutputIssues(pipeline, output)
	if len(issues) != 3 {
		t.Fatalf("issues = %v, want scale, investor-behavior, and relative benchmark claims", issues)
	}
}

func TestFundraisingDomainQualityFindsComposedStageAndInstitutionRules(t *testing.T) {
	pipeline := &Pipeline{
		AIEmployeeID:           "fundraising-advisor",
		AIEmployeeOriginalTask: "@投融资顾问 评估融资准备度",
	}
	output := `ARR 1800 万元，属企业软件 A 轮语境下的早期体量（推断，非 benchmark）。
P0：缺失即难以启动 DD。
这几项是 A 轮投资人决定是否进入首次正式会面的关键材料。
以下 P0/P1/P2 是本次分析建议的补充优先级，并非机构规则。`
	issues := aiEmployeeDomainOutputIssues(pipeline, output)
	if len(issues) != 3 {
		t.Fatalf("issues = %v, want three composed unsupported claims and the labelled analyst priority preserved", issues)
	}
}

func TestFundraisingDomainQualityRejectsInvestorPredictionsAndMissingToNegative(t *testing.T) {
	pipeline := &Pipeline{
		AIEmployeeID:           "fundraising-advisor",
		AIEmployeeOriginalTask: "@投融资顾问 ARR 1800 万元，请评估融资准备度",
	}
	output := `A 类——阻断级（缺失则投资人无法进入正式尽调）。
多数机构将 IP 权属瑕疵列为 Deal Killer。
缺少单位经济会导致估值打折或条款收紧。
这六项是融资启动的入场券，缺一即停。
这些指标未经审计或第三方验证。
本次分析建议优先补齐现金流和收入结构，以便验证跑道与增长质量。`
	issues := aiEmployeeDomainOutputIssues(pipeline, output)
	if len(issues) != 5 {
		t.Fatalf("issues = %v, want five unsupported market/absence claims and the labelled recommendation preserved", issues)
	}
}

func TestFundraisingDomainQualityRejectsPlaceholderThresholds(t *testing.T) {
	pipeline := &Pipeline{
		AIEmployeeID:           "fundraising-advisor",
		AIEmployeeOriginalTask: "@投融资顾问 评估融资准备度",
	}
	output := `请补充 ARR ≥ 一定金额的客户数量。
建议按客单价分层统计客户数量。`
	issues := aiEmployeeDomainOutputIssues(pipeline, output)
	if len(issues) != 1 {
		t.Fatalf("issues = %v, want only the invented placeholder threshold", issues)
	}
}

func TestFundraisingDomainQualityAllowsRequestedCitedBenchmark(t *testing.T) {
	pipeline := &Pipeline{
		AIEmployeeID:           "fundraising-advisor",
		AIEmployeeOriginalTask: "@投融资顾问 请做行业基准和同业对标",
	}
	output := "根据可比样本，指标处于该样本经验区间。[来源](https://example.com/report)"
	if issues := aiEmployeeDomainOutputIssues(pipeline, output); len(issues) != 0 {
		t.Fatalf("explicit cited benchmark should pass the bounded host gate: %v", issues)
	}
}

func TestFundraisingDomainQualityFindsUncitedExternalQuantitiesAndRoundPlacement(t *testing.T) {
	pipeline := &Pipeline{
		AIEmployeeID:           "fundraising-advisor",
		AIEmployeeOriginalTask: "@融资专家 我们有 2 家意向试点，拟融资 1500 万元，请评价并重写",
	}
	output := `按公开可见的早期融资实践，金额从数十万到数百万人民币都有出现。
如果试点只是口头意向，需要把融资额下调到种子轮 50–300 万区间。
本分析建议把 5–8 家付费客户作为待验证目标，而不是行业门槛。
拟融资 1500 万元是用户提供的计划值，不代表轮次适配。`
	issues := aiEmployeeDomainOutputIssues(pipeline, output)
	if len(issues) != 2 {
		t.Fatalf("issues = %v, want uncited practice range and invented round range only", issues)
	}
}

func TestFundraisingDomainQualityPreservesCritiqueAndTransparentAssumptions(t *testing.T) {
	pipeline := &Pipeline{
		AIEmployeeID:           "fundraising-advisor",
		AIEmployeeOriginalTask: "@融资专家 原文写了行业唯一、转化率提升 300%，但都没有证据",
	}
	output := `“行业唯一、转化率提升 300%”在当前证据下站不住，应删除该表述。
本分析建议把 5–8 家付费客户作为待验证目标，不把它写成行业标准。
若按 12–18 个月跑道做情景假设，需要再用真实预算倒推融资额。`
	if issues := aiEmployeeDomainOutputIssues(pipeline, output); len(issues) != 0 {
		t.Fatalf("explicit critique and labelled analyst assumptions should pass: %v", issues)
	}
}

func TestFundraisingDomainQualityFindsUncitedInvestorBehaviorPrediction(t *testing.T) {
	pipeline := &Pipeline{
		AIEmployeeID:           "fundraising-advisor",
		AIEmployeeOriginalTask: "@融资专家 评价这份 BP",
	}
	output := `这个绝对化说法缺少证据，建议删除。
一线投资人会在首次会面当场挑战这一表述。
更稳妥的写法是：当前材料无法支持该结论。`
	issues := aiEmployeeDomainOutputIssues(pipeline, output)
	if len(issues) != 1 {
		t.Fatalf("issues = %v, want only the investor-behavior prediction", issues)
	}
}

func TestFundraisingDomainQualityFindsUngroundedMetricPlacementAndQualitativeGrowth(t *testing.T) {
	pipeline := &Pipeline{
		AIEmployeeID:           "fundraising-advisor",
		AIEmployeeOriginalTask: "@融资专员 ARR 1800 万元，同比增长 70%，请说明这些指标能支持什么",
	}
	output := `ARR 规模量级属于商业化早期验证区间。
同比增长 70%，在已有基数上仍保持高速扩张。
按口径仅说明 ARR 为正且同比增长为正，不能单独证明 PMF 或可持续性。`
	issues := aiEmployeeDomainOutputIssues(pipeline, output)
	if len(issues) != 2 {
		t.Fatalf("issues = %v, want ungrounded placement and qualitative growth only", issues)
	}
}

func TestFundraisingDomainQualityFindsMetricEvaluationAndBusinessCausalityByStructure(t *testing.T) {
	pipeline := &Pipeline{
		AIEmployeeID:           "fundraising-advisor",
		AIEmployeeOriginalTask: "@融资专家 ARR 1800 万元，同比增长 70%，毛利率 78%，请做中性解读",
	}
	output := `ARR 1800 万元意味着公司已经脱离一次性项目阶段。
同比增长 70% 属于健康 SaaS 增长的方向性特征。
毛利率 78% 通常代表订阅收入占比高、实施成本受控。
按口径仅说明同比增长为正；没有可比来源时不能判断增长是否健康。`
	issues := aiEmployeeDomainOutputIssues(pipeline, output)
	if len(issues) != 3 {
		t.Fatalf("issues = %v, want three unsupported metric inferences and the neutral definition preserved", issues)
	}
}

func TestFundraisingDomainQualityRejectsBusinessInferenceDisguisedAsNeutralSupport(t *testing.T) {
	pipeline := &Pipeline{
		AIEmployeeID:           "fundraising-advisor",
		AIEmployeeOriginalTask: "@融资专员 ARR 1800 万元、同比增长 70%、毛利率 78%，说明能支持和不能支持什么",
	}
	output := `ARR 1800 万元可以作为已建立可重复销售模式的初证。
同比增长 70% 说明需求侧尚未明显饱和（仅为算术描述）。
毛利率 78% 作为算术数值，说明软件属性占比可能较高。
按口径仅说明同比增长率为正，不能据此判断需求或市场饱和度。`
	issues := aiEmployeeDomainOutputIssues(pipeline, output)
	if len(issues) != 3 {
		t.Fatalf("business inferences disguised as neutral support were not rejected: %v", issues)
	}
}

func TestFundraisingDomainQualityDoesNotUpgradeUserInputToVerified(t *testing.T) {
	pipeline := &Pipeline{
		AIEmployeeID:           "fundraising-advisor",
		AIEmployeeOriginalTask: "@融资专员 ARR 1800 万元，请做中性解读",
	}
	output := `### ARR 1800 万元【用户已核验事实】
用户口径为 ARR 1800 万元，尚未外部核验。`
	issues := aiEmployeeDomainOutputIssues(pipeline, output)
	if len(issues) != 1 {
		t.Fatalf("user-supplied input was incorrectly allowed to become verified: %v", issues)
	}
}

func TestFundraisingDomainQualityRejectsContradictoryVerifiedUserInputLabel(t *testing.T) {
	pipeline := &Pipeline{
		AIEmployeeID:           "fundraising-advisor",
		AIEmployeeOriginalTask: "@融资专员 ARR 1800 万元，请做中性解读",
	}
	output := `- **已核验事实（来自你提供的输入）：** ARR 1,800 万元。
- **用户自报：** ARR 1,800 万元，尚待独立核验。`
	issues := aiEmployeeDomainOutputIssues(pipeline, output)
	if len(issues) != 1 {
		t.Fatalf("contradictory verified-user-input label was accepted: %v", issues)
	}
}

func TestFundraisingDomainQualityRejectsVagueOriginalTextAttribution(t *testing.T) {
	pipeline := &Pipeline{
		AIEmployeeID:           "fundraising-advisor",
		AIEmployeeOriginalTask: "@融资专家 核验两篇文章并摘录关键事实。我另外计划做潜客挖掘和 AIGC 获客。",
	}
	output := `原文显示，项目已形成“潜客挖掘 + AIGC 音乐视频获客”闭环。
用户在本次对话中提出“潜客挖掘 + AIGC 音乐视频获客”，两篇原文未提供此项来源。
原文显示产品支持任务编排。[来源](https://example.com/article)`
	issues := aiEmployeeDomainOutputIssues(pipeline, output)
	if len(issues) != 1 {
		t.Fatalf("issues = %v, want only the vague unsupported original-text attribution", issues)
	}
}

func TestFundraisingDomainQualityRejectsVaguePublicEvidenceLabels(t *testing.T) {
	pipeline := &Pipeline{
		AIEmployeeID:           "fundraising-advisor",
		AIEmployeeOriginalTask: "@融资专家 请给出关键事实、来源、可信度、核验状态和局限。",
	}
	output := `## 关键事实 / 来源 / 可信度 / 局限

| 事实 | 来源 | 核验状态 | 局限 |
|---|---|---|---|
| 市场已有多种同类产品 | 公开资料 | 已核验事实 | 产品状态可能变化 |
| 常见定价包含席位费 | 行业一般做法 | 已核验事实 | 未限定市场 |
| 项目拟做自动化获客 | 用户本轮正文 | 用户陈述 | 尚无客户数据 |
| 该方向可能适合作为产品假设 | 未提供具体来源 | 待核验的分析假设 | 需客户访谈 |
| 示例产品当前提供公开页面 | [产品页](https://example.com/product) | 已核验事实 | 仅核验页面存在 |`
	issues := aiEmployeeDomainOutputIssues(pipeline, output)
	if len(issues) != 2 {
		t.Fatalf("issues = %v, want two vague public-source rows only", issues)
	}
}

func TestFundraisingDomainQualityRequiresConcreteProvenancePerEvidenceRow(t *testing.T) {
	pipeline := &Pipeline{
		AIEmployeeID:           "fundraising-advisor",
		AIEmployeeOriginalTask: "@融资专家 请给出关键事实、来源、可信度、核验状态和局限。",
	}
	output := `## 关键事实 / 来源 / 可信度 / 局限

| # | 事实 | 出处 | 可信度 | 局限 |
|---|---|---|---|---|
| F1 | 两个页面未检出业务描述 | 本轮抓取比对 | 高 | 抓取可能受限 |
| F2 | 赛道已有代表性产品 | 本轮检索命中的行业评论 | 中 | 产品状态可能变化 |
| F3 | 项目计划做自动化获客 | 用户本轮输入 | 用户陈述 | 尚无客户数据 |
| F4 | 可以先把闭环作为定位候选 | 基于现有信息的推断 | 待验证假设 | 需客户访谈 |
| F5 | 示例页面当前可以访问 | https://example.com/product | 已核验事实 | 仅核验页面存在 |
| F6 | 当前未发现公开来源可核验收入 | 未发现公开来源 | 未知 | 检索范围有限 |`
	issues := aiEmployeeDomainOutputIssues(pipeline, output)
	if len(issues) != 2 {
		t.Fatalf("issues = %v, want only the two opaque retrieval-source rows", issues)
	}
}

func TestFundraisingDomainQualityScopesEvidenceRowsAndPreservesQuestions(t *testing.T) {
	pipeline := &Pipeline{
		AIEmployeeID:           "fundraising-advisor",
		AIEmployeeOriginalTask: "@融资专家 请给出来源证据表、BP 目录和三个待确认问题。",
	}
	output := `## 关键事实 / 来源 / 可信度 / 局限

| 事实 | 来源 | 核验状态 | 局限 |
|---|---|---|---|
| 项目拟做自动化获客 | 用户本轮输入 | 用户陈述 | 尚无客户数据 |

## BP 目录

| # | 章节 | 用途 |
|---|---|---|
| 1 | 产品定位 | 说明目标客户与产品假设 |
| 2 | 商业模式 | 列出待验证的收费路径 |
| 3 | 团队 | 标注未知字段 |

## 三个待确认问题

3. 本轮目标金额、轮次、币种分别是什么，是否已有投资人意向？——决定融资计划如何从占位符升级。`
	if issues := aiEmployeeDomainOutputIssues(pipeline, output); len(issues) != 0 {
		t.Fatalf("ordinary non-evidence table rows or a real clarification question were rejected: %v", issues)
	}
}

func TestFundraisingDomainQualityDoesNotRewriteExplicitInteractionMenu(t *testing.T) {
	pipeline := &Pipeline{
		AIEmployeeID: "fundraising-advisor",
		AIEmployeeOriginalTask: `请先只让我选择信息起点：
①你主动联网检索公开资料并标注来源；
②读取我接下来上传的材料；
③两者结合。
不要假设我已经选择，也不要提前写正文。`,
	}
	output := `请选择一种信息起点：

① 主动联网检索行业、市场、可比公司和机构偏好，并标注来源。
② 读取你接下来上传的材料。
③ 两者结合。`
	if issues := aiEmployeeDomainOutputIssues(pipeline, output); len(issues) != 0 {
		t.Fatalf("an explicitly requested interaction menu entered later-stage fact review: %v", issues)
	}
	cleaned := stripUnsupportedFundraisingBenchmarkLines(output, pipeline.AIEmployeeOriginalTask)
	for _, option := range []string{"①", "②", "③"} {
		if !strings.Contains(cleaned, option) {
			t.Fatalf("user-authored option %q was removed: %q", option, cleaned)
		}
	}
}

func TestFundraisingDomainRepairPromptRequiresClaimLevelProvenance(t *testing.T) {
	prompt := buildAIEmployeeDomainOutputRepairPrompt(
		"base",
		[]string{"原文显示一个未引用的能力。"},
		"previous",
	)
	for _, want := range []string{
		"Audit provenance claim by claim",
		"common knowledge",
		"are not traceable sources",
		"Text the user typed next to a URL is user-provided context",
		"fact, provenance, verification status or credibility, and limitations",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("repair prompt missing provenance rule %q: %q", want, prompt)
		}
	}
}

func TestFundraisingDomainQualityAllowsExplicitBoundariesAndProfessionalReview(t *testing.T) {
	pipeline := &Pipeline{
		AIEmployeeID:           "fundraising-advisor",
		AIEmployeeOriginalTask: "@融资专家 制作融资准备清单",
	}
	output := `> 标注规则：所有行业惯例均带来源；本清单不引入未经核验的行业门槛，不自创机构偏好。
- 本清单不保证融资结果、估值水平或交易结果。
- 重大条款、正式交易文件、税务结构和合规判断须由律师、会计师、税务师或持牌机构复核。`
	if issues := aiEmployeeDomainOutputIssues(pipeline, output); len(issues) != 0 {
		t.Fatalf("explicit safety boundaries were rejected as positive external claims: %v", issues)
	}
}

func TestRemoveEmptyMarkdownSectionsCleansAdjacentOrphanHeadings(t *testing.T) {
	output := `## 验证清单

### A. 有内容
- 保留这一项。

### B. 空分组

### C. 也是空分组

---

## 结论
保留结论。`
	cleaned := removeEmptyMarkdownSections(output)
	for _, forbidden := range []string{"B. 空分组", "C. 也是空分组"} {
		if strings.Contains(cleaned, forbidden) {
			t.Fatalf("orphan heading %q remained: %q", forbidden, cleaned)
		}
	}
	for _, want := range []string{"A. 有内容", "保留这一项", "结论", "保留结论"} {
		if !strings.Contains(cleaned, want) {
			t.Fatalf("non-empty content %q was removed: %q", want, cleaned)
		}
	}
}

func TestFundraisingDomainQualityFindsUnlabelledPrescriptiveQuantities(t *testing.T) {
	pipeline := &Pipeline{
		AIEmployeeID:           "fundraising-advisor",
		AIEmployeeOriginalTask: "@融资专家 已有 3 家免费试用、3 人团队、拟融资 800 万元，请给条件式结论",
	}
	output := `至少满足 3/5 才进入条款阶段。
至少 1 家试用客户应进入付费 POC。
核心 1–2 人必须有相关行业经验。
本分析建议把至少 1 家付费 POC 作为待验证目标，不代表机构门槛。`
	issues := aiEmployeeDomainOutputIssues(pipeline, output)
	if len(issues) != 3 {
		t.Fatalf("issues = %v, want three unlabelled thresholds and the labelled recommendation preserved", issues)
	}
}

func TestFundraisingDomainQualityFindsUnsupportedCommercialSuperlatives(t *testing.T) {
	pipeline := &Pipeline{
		AIEmployeeID:           "fundraising-advisor",
		AIEmployeeOriginalTask: "@融资专家 根据公开材料写 BP",
	}
	output := `我们是中国第一批把三层能力做成闭环的团队。
这是国内唯一一家同时交付入口、工厂和引擎的平台。
“行业唯一”没有证据支持，应删除。
根据公开名录，这是首个平台。[来源](https://example.com/list)`
	issues := aiEmployeeDomainOutputIssues(pipeline, output)
	if len(issues) != 2 {
		t.Fatalf("issues = %v, want two unsupported superlatives while critique and cited claim pass", issues)
	}
}

func TestFundraisingDomainQualityFindsUncitedInvestorQuestionPrediction(t *testing.T) {
	pipeline := &Pipeline{
		AIEmployeeID:           "fundraising-advisor",
		AIEmployeeOriginalTask: "@融资专家 评价这份 BP",
	}
	output := `投资人最早要问的就是试点是否付费。
本次分析建议优先补充试点是否付费，以便验证商业闭环。`
	issues := aiEmployeeDomainOutputIssues(pipeline, output)
	if len(issues) != 1 {
		t.Fatalf("issues = %v, want only the investor-question prediction", issues)
	}
}

func TestFundraisingDomainQualityFindsGeneralInstitutionBehaviorPredictions(t *testing.T) {
	pipeline := &Pipeline{
		AIEmployeeID:           "fundraising-advisor",
		AIEmployeeOriginalTask: "@融资顾问 评价没有来源的 BP 文案并给出修改建议",
	}
	output := `一线机构合规会卡掉，FA 一般也会建议主动删掉。
这个说法会被尽调和投委会直接挑穿。
本分析建议把这句话删掉，改为可核验的项目方陈述。
该断言并不代表机构会拒绝项目。`
	issues := aiEmployeeDomainOutputIssues(pipeline, output)
	if len(issues) != 2 {
		t.Fatalf("issues = %v, want two unsupported institution-behavior predictions only", issues)
	}
}

func TestFundraisingDomainQualityRecognizesSemanticEvidenceHeading(t *testing.T) {
	pipeline := &Pipeline{
		AIEmployeeID:           "fundraising-advisor",
		AIEmployeeOriginalTask: "@融资顾问 这句话没有任何来源，请评价并列证据材料",
	}
	output := `## 证据可信度速查表

| 判断 | 来源 | 状态 | 局限 |
|---|---|---|---|
| “行业第一”有合规风险 | 现行法律的一般规制 | 已核验 | 未提供法条链接 |
| “行业第一”来自当前 BP | 用户本轮输入 | 项目方陈述 | 尚无外部证据 |`
	issues := aiEmployeeDomainOutputIssues(pipeline, output)
	if len(issues) != 1 {
		t.Fatalf("issues = %v, want only the evidence row without traceable provenance", issues)
	}
}

func TestStripUnsupportedFundraisingBenchmarkLinesKeepsEvidenceBoundaries(t *testing.T) {
	output := `结论：现有指标方向积极。
- ARR 属于 A 轮讨论区间。
- 毛利率 78% 在企业软件里偏上。
- 机构对此高度敏感，是常见否决信号。
- ARR 1800 万元属于 A 轮早期体量。
- P0：缺失即难以启动 DD。
- 用户提供的指标未经审计或第三方验证。
- 毛利率和 NRR 只能视为局部信号。
没有可比来源时，不得宣称达到某轮次门槛。`
	cleaned := stripUnsupportedFundraisingBenchmarkLines(output)
	if strings.Contains(cleaned, "讨论区间") ||
		strings.Contains(cleaned, "偏上") ||
		strings.Contains(cleaned, "高度敏感") ||
		strings.Contains(cleaned, "早期体量") ||
		strings.Contains(cleaned, "难以启动") ||
		strings.Contains(cleaned, "未经审计") {
		t.Fatalf("unsupported benchmark line remained: %q", cleaned)
	}
	for _, want := range []string{"结论：现有指标方向积极", "只能视为局部信号", "不得宣称达到某轮次门槛"} {
		if !strings.Contains(cleaned, want) {
			t.Fatalf("cleaned output lost %q: %q", want, cleaned)
		}
	}
}

func TestStripUnsupportedFundraisingClaimsPreservesTableRows(t *testing.T) {
	task := `@融资顾问 评价“转化率提升 300%，行业第一”，最后列 3 项补证材料。`
	output := `## 关键补证材料清单（3 项）

| # | 材料 | 用途 | 核验方式 |
|---|---|---|---|
| **1** | 原始数据导出 | 核对指标口径 | 投资人可要求抽样复算；可信度取决于原始日志 |
| **2** | 对照实验记录 | 区分同时发生的变化 | 保留实验起止日期与分流记录 |
| **3** | 行业对标池 | 核对比较范围 | 列出 3–5 家可比公司；未找到公开来源的部分明确标注 |`
	cleaned := stripUnsupportedFundraisingBenchmarkLines(output, task)
	for _, item := range []string{"**1**", "**2**", "**3**", "原始数据导出", "行业对标池"} {
		if !strings.Contains(cleaned, item) {
			t.Fatalf("requested table row %q was removed: %q", item, cleaned)
		}
	}
	for _, rejected := range []string{"投资人可要求", "3–5 家"} {
		if strings.Contains(cleaned, rejected) {
			t.Fatalf("unsupported clause %q remained: %q", rejected, cleaned)
		}
	}
	if issues := aiEmployeeDomainOutputIssues(&Pipeline{
		AIEmployeeID:           "fundraising-advisor",
		AIEmployeeOriginalTask: task,
	}, cleaned); len(issues) != 0 {
		t.Fatalf("repaired table still has domain issues: %v\n%s", issues, cleaned)
	}
}

func TestFundraisingDomainQualityScopesInstitutionBehaviorToTableCells(t *testing.T) {
	pipeline := &Pipeline{
		AIEmployeeID:           "fundraising-advisor",
		AIEmployeeOriginalTask: `@融资顾问 评价“行业第一”，把原句标为项目方陈述并列补证材料。`,
	}
	output := `## 补证材料

| # | 材料 | 可接受形式 | 当前状态 |
|---|---|---|---|
| 3 | “行业第一”对应的排名来源 | 第三方机构榜单或可比公司披露 | 未提供公开来源；本分析建议无来源时删除该表述 |`
	if issues := aiEmployeeDomainOutputIssues(pipeline, output); len(issues) != 0 {
		t.Fatalf("independent table cells were combined into a false institution-behavior claim: %v", issues)
	}
	cleaned := stripUnsupportedFundraisingBenchmarkLines(output, pipeline.AIEmployeeOriginalTask)
	if !strings.Contains(cleaned, "| 3 |") || !strings.Contains(cleaned, "排名来源") {
		t.Fatalf("bounded evidence checklist row was removed: %q", cleaned)
	}
}

func TestFundraisingDomainQualityPreservesArbitraryMarkdownTableHeaders(t *testing.T) {
	pipeline := &Pipeline{
		AIEmployeeID:           "fundraising-advisor",
		AIEmployeeOriginalTask: `@融资专家 请给出来源、可信度与局限。`,
	}
	output := `## 证据可信度清单

| ID | 核验对象 / 当前陈述 | 证据位置 / 出处 | 状态等级 | 使用限制 |
|:---|:------------------|:----------------|:---------|:---------|
| F1 | 业务方向来自本轮输入 | 用户本轮输入 | 项目方陈述 | 未独立核验 |`
	if issues := aiEmployeeDomainOutputIssues(pipeline, output); len(issues) != 0 {
		t.Fatalf("structural Markdown header was treated as an evidence data row: %v", issues)
	}
	cleaned := stripUnsupportedFundraisingBenchmarkLines(output, pipeline.AIEmployeeOriginalTask)
	if !strings.Contains(cleaned, "核验对象 / 当前陈述") {
		t.Fatalf("Markdown table header was removed: %q", cleaned)
	}
}

func TestStripUnsupportedFundraisingBenchmarkLinesRenumbersOrderedList(t *testing.T) {
	output := `# 融资评估

## 一句话总结
投资人通常会因此拒绝项目。

## 决定性问题

1. 投资人最早要问的是试点是否付费。
   *对应影响：这会改变下一版叙事与材料结构。*
2. 护城河中第一个可验证的证据是什么？
3. 本轮资金对应的十二个月里程碑是什么？`
	cleaned := stripUnsupportedFundraisingBenchmarkLines(output)
	if strings.Contains(cleaned, "投资人最早要问") {
		t.Fatalf("unsupported investor behavior remained: %q", cleaned)
	}
	if strings.Contains(cleaned, "对应影响") {
		t.Fatalf("continuation text from a removed ordered item remained: %q", cleaned)
	}
	if strings.Contains(cleaned, "一句话总结") {
		t.Fatalf("empty heading remained after its unsupported content was removed: %q", cleaned)
	}
	for _, want := range []string{
		"1. 护城河中第一个可验证的证据是什么？",
		"2. 本轮资金对应的十二个月里程碑是什么？",
	} {
		if !strings.Contains(cleaned, want) {
			t.Fatalf("renumbered output missing %q: %q", want, cleaned)
		}
	}
	if strings.Contains(cleaned, "3. 本轮资金") {
		t.Fatalf("ordered list retained a numbering gap: %q", cleaned)
	}
}

func TestFundraisingDomainQualityUsesPurposeScopedHeadingContext(t *testing.T) {
	pipeline := &Pipeline{
		AIEmployeeID:           "fundraising-advisor",
		AIEmployeeOriginalTask: "@融资专家 写 BP，最多三个决定性问题",
	}
	output := `## 三个关键追问（决定下一步材料深度）
1. **首批验证客户与场景**：是否已有可署名试点？如果没有，本分析建议先定义设计合作伙伴的验证口径。`
	if issues := aiEmployeeDomainOutputIssues(pipeline, output); len(issues) != 0 {
		t.Fatalf("unrelated heading words leaked into the item claim: %v", issues)
	}
}

func TestFundraisingDomainQualityNormalizesEquivalentMultiplierNotation(t *testing.T) {
	pipeline := &Pipeline{
		AIEmployeeID:           "fundraising-advisor",
		AIEmployeeOriginalTask: "@融资顾问 解释 1x 非参与型清算优先权，投资人投 1000 万",
	}
	output := `## 机制定义
投资人在 1 倍优先金额（1000 万）与转换后按比例分配之间二选一。`
	if issues := aiEmployeeDomainOutputIssues(pipeline, output); len(issues) != 0 {
		t.Fatalf("equivalent 1x and 1 倍 notation should not create a new quantity: %v", issues)
	}
}

func TestFundraisingDomainQualityLaterDisclaimerDoesNotExcuseBenchmark(t *testing.T) {
	pipeline := &Pipeline{
		AIEmployeeID:           "fundraising-advisor",
		AIEmployeeOriginalTask: "@融资专家 评估一个 3 人种子期团队",
	}
	output := `3 人创始团队——人数属于种子轮常见规模区间；不能仅凭人数推断团队完整度或质量。`
	if issues := aiEmployeeDomainOutputIssues(pipeline, output); len(issues) != 1 {
		t.Fatalf("later disclaimer incorrectly excused the earlier benchmark: %v", issues)
	}
}

func TestFundraisingDomainQualityRejectsRecommendationLabelLaunderingMarketClaim(t *testing.T) {
	pipeline := &Pipeline{
		AIEmployeeID:           "fundraising-advisor",
		AIEmployeeOriginalTask: "@融资专家 评估种子期项目，不使用无来源行业门槛",
	}
	output := `本分析建议的“种子轮 800 万元属于常见区间”只是相对量级判断，不是机构门槛。
本分析建议把 800 万元作为本项目的待验证融资目标，不代表市场惯例。`
	issues := aiEmployeeDomainOutputIssues(pipeline, output)
	if len(issues) != 1 {
		t.Fatalf("recommendation label laundered an external benchmark: %v", issues)
	}
}

func TestFundraisingDomainQualityRequiresRequestedEvidenceBalance(t *testing.T) {
	pipeline := &Pipeline{
		AIEmployeeID:           "fundraising-advisor",
		AIEmployeeOriginalTask: "@融资专家 说明这些指标分别能支持什么、不能支持什么",
	}
	output := strings.Repeat("背景说明仅用于形成完整交付长度。\n", 50) + `### ARR 1800 万元
- **不能支持**：不能单独判断轮次。`
	issues := aiEmployeeDomainOutputIssues(pipeline, output)
	if len(issues) != 1 || !strings.Contains(issues[0], "requested evidence coverage") {
		t.Fatalf("missing positive side was not detected: %v", issues)
	}
	balanced := strings.Repeat("背景说明仅用于形成完整交付长度。\n", 50) + `### ARR 1800 万元
- **能支持**：按用户给出的口径，报告值为 ARR 1800 万元。
- **不能支持**：不能单独判断轮次。`
	if issues := aiEmployeeDomainOutputIssues(pipeline, balanced); len(issues) != 0 {
		t.Fatalf("balanced neutral treatment should pass: %v", issues)
	}
}

func TestStripUnsupportedFundraisingBenchmarkLinesRepairsEmptyBlocks(t *testing.T) {
	output := `# 示例

---

---

## 支持与不支持

**支持**

**不支持**
- 不能据此判断轮次。

## 决定性问题

1. **资金用途与里程碑**

2. **客户证据**：是否有付费客户？`
	cleaned := stripUnsupportedFundraisingBenchmarkLines(output)
	if strings.Contains(cleaned, "**支持**") || strings.Contains(cleaned, "资金用途与里程碑") {
		t.Fatalf("empty structural blocks remained: %q", cleaned)
	}
	if strings.Count(cleaned, "---") != 1 {
		t.Fatalf("duplicate markdown rules remained: %q", cleaned)
	}
	if !strings.Contains(cleaned, "1. **客户证据**") {
		t.Fatalf("remaining question was not renumbered: %q", cleaned)
	}
}

func TestFundraisingDomainQualityDoesNotTreatRequestedInputAsBenchmarkClaim(t *testing.T) {
	pipeline := &Pipeline{
		AIEmployeeID:           "fundraising-advisor",
		AIEmployeeOriginalTask: "请先确认融资阶段与目标，再开始后续工作。",
	}
	output := `## 请确认
3. **融资阶段与目标**：本轮所处阶段（Seed / Pre-A / A 等）、拟融资金额与币种、是否已有估值锚点。`
	if issues := aiEmployeeDomainOutputIssues(pipeline, output); len(issues) != 0 {
		t.Fatalf("a requested user-input field was treated as an asserted benchmark: %v", issues)
	}

	withBadClaim := output + "\nPre-A 项目通常都适合融资 3000 万元。"
	cleaned := stripUnsupportedFundraisingBenchmarkLines(withBadClaim, pipeline.AIEmployeeOriginalTask)
	if !strings.Contains(cleaned, "融资阶段与目标") {
		t.Fatalf("requested input was removed while cleaning a separate claim: %q", cleaned)
	}
	if strings.Contains(cleaned, "通常都适合") {
		t.Fatalf("unsupported claim remained while preserving requested input: %q", cleaned)
	}
}

func TestFundraisingDomainQualityRejectsNonParticipatingPayoutStack(t *testing.T) {
	pipeline := &Pipeline{
		AIEmployeeID: "fundraising-advisor",
		AIEmployeeOriginalTask: "@融资专家 解释 1x non-participating 清算优先权：" +
			"公司退出 5000 万元，投资人投入 1000 万元取得 20%。",
	}
	output := `## 选项 A：行使优先清算权
投资人先拿 1000 万元，再按 20% 分配剩余 4000 万元。
投资人 = LP + (Exit − LP) × p = 1800 万元。
| A. 行使 1× 优先权 | 1000 + (5000 - 1000) × 20% | 1800 万元 |`

	issues := aiEmployeeDomainOutputIssues(pipeline, output)
	if len(issues) != 3 {
		t.Fatalf("non-participating payout stack was not rejected line by line: %v", issues)
	}
}

func TestFundraisingDomainQualityAcceptsNonParticipatingEitherOrCalculation(t *testing.T) {
	pipeline := &Pipeline{
		AIEmployeeID: "fundraising-advisor",
		AIEmployeeOriginalTask: "@融资专家 解释 1x 非参与型清算优先权：" +
			"公司退出 5000 万元，投资人投入 1000 万元取得 20%。",
	}
	output := `非参与型投资人在优先金额 1000 万元与转换后按 20% 取得 1000 万元之间二选一。
选择优先金额后不再参与剩余分配，普通股东取得剩余 4000 万元。`

	if issues := aiEmployeeDomainOutputIssues(pipeline, output); len(issues) != 0 {
		t.Fatalf("correct non-participating either-or calculation was rejected: %v", issues)
	}
}

func TestFundraisingDomainQualityRejectsReversedNonParticipatingCrossover(t *testing.T) {
	pipeline := &Pipeline{
		AIEmployeeID: "fundraising-advisor",
		AIEmployeeOriginalTask: "@融资专家 解释 1x 非参与型清算优先权：" +
			"公司退出 2000 万元，投资人投入 800 万元取得 25%。",
	}
	wrong := `临界点说明：仅当退出总价低于临界点时，转换路径反超；退出总价高于临界点时，优先金额占优。`
	if issues := aiEmployeeDomainOutputIssues(pipeline, wrong); len(issues) == 0 {
		t.Fatal("reversed non-participating crossover direction was accepted")
	}

	correct := `临界点说明：退出总价高于临界点时，转换路径反超；退出总价低于临界点时，优先金额占优。`
	if issues := aiEmployeeDomainOutputIssues(pipeline, correct); len(issues) != 0 {
		t.Fatalf("correct non-participating crossover direction was rejected: %v", issues)
	}
}

func TestFundraisingDomainQualityPreservesDerivedInvestorTransactionAmounts(t *testing.T) {
	pipeline := &Pipeline{
		AIEmployeeID: "fundraising-advisor",
		AIEmployeeOriginalTask: "@融资专家 公司以 2000 万元出售，投资人投入 800 万元取得 25%，" +
			"请计算 1 倍非参与型清算优先权。",
	}
	output := `投资人转股所得 = 2000 万元 × 25% = 500 万元。
| 投资人 | 800 万元 | 500 万元 | 300 万元 |`

	if issues := aiEmployeeDomainOutputIssues(pipeline, output); len(issues) != 0 {
		t.Fatalf("derived user-transaction amounts were treated as external benchmarks: %v", issues)
	}
	cleaned := stripUnsupportedFundraisingBenchmarkLines(output, pipeline.AIEmployeeOriginalTask)
	for _, want := range []string{"500 万元", "300 万元"} {
		if !strings.Contains(cleaned, want) {
			t.Fatalf("derived transaction result %q was removed: %q", want, cleaned)
		}
	}
}

func TestFundraisingDomainQualityPreservesTraceablePreAndPostMoneyCalculation(t *testing.T) {
	pipeline := &Pipeline{
		AIEmployeeID: "fundraising-advisor",
		AIEmployeeOriginalTask: "@融资专家 公司本轮融资800万元，投资人投后持股25%。" +
			"请直接计算投前估值和投后估值并写明公式。",
	}
	output := `- 投后估值 = 800 ÷ 0.25 = 3,200 万元
- 投前估值 = 3,200 − 800 = 2,400 万元
- 一致性校验：2,400 + 800 = 3,200 万元。`

	if issues := aiEmployeeDomainOutputIssues(pipeline, output); len(issues) != 0 {
		t.Fatalf("traceable valuation arithmetic was treated as an external benchmark: %v", issues)
	}
	cleaned := stripUnsupportedFundraisingBenchmarkLines(output, pipeline.AIEmployeeOriginalTask)
	for _, want := range []string{"3,200 万元", "2,400 万元"} {
		if !strings.Contains(cleaned, want) {
			t.Fatalf("derived valuation result %q was removed: %q", want, cleaned)
		}
	}
}

func TestFundraisingDomainQualityCalculationExceptionDoesNotAllowBenchmarks(t *testing.T) {
	pipeline := &Pipeline{
		AIEmployeeID:           "fundraising-advisor",
		AIEmployeeOriginalTask: "@融资专家 用 800 万元计算投前估值。",
	}
	output := "行业通常融资 800 万元即可达到 A 轮门槛，行业平均估值 = 800 × 5 = 4000 万元。"
	if issues := aiEmployeeDomainOutputIssues(pipeline, output); len(issues) == 0 {
		t.Fatal("a traceable arithmetic line incorrectly bypassed unsupported benchmark checks")
	}
}

func TestFundraisingDomainQualityRejectsUnconfirmedParallelProductLines(t *testing.T) {
	pipeline := &Pipeline{
		AIEmployeeID: "fundraising-advisor",
		AIEmployeeOriginalTask: "@融资专家 我有 OpenKitty 产品资料、Saddle 参考资料，" +
			"还提到潜客挖掘和 AIGC 社媒获客，请做 BP。",
	}
	output := `三条产品线共享同一 Agent 引擎。
### 业务线 A：OpenKitty 通用 Agent
我们提供三类应用方向，覆盖 Agent、潜客挖掘和 AIGC 获客。
## 解决方案与四条产品方向`

	issues := aiEmployeeDomainOutputIssues(pipeline, output)
	if len(issues) < 5 {
		t.Fatalf("unconfirmed product architecture was silently promoted to fact: %v", issues)
	}
}

func TestFundraisingDomainQualityRejectsTentativeButAllowsUserConfirmedProductLines(t *testing.T) {
	tentative := &Pipeline{
		AIEmployeeID: "fundraising-advisor",
		AIEmployeeOriginalTask: "@融资专家 我有 OpenKitty 产品资料、Saddle 参考资料，" +
			"还提到潜客挖掘和 AIGC 社媒获客，请做 BP。",
	}
	tentativeOutput := `本分析建议的待确认架构：OpenKitty 可作为平台层，潜客挖掘和 AIGC 获客可作为应用模块；Saddle 仅为参考产品。`
	if issues := aiEmployeeDomainOutputIssues(tentative, tentativeOutput); len(issues) == 0 {
		t.Fatal("a tentative architecture proposal was accepted before the relationship was confirmed")
	}

	confirmed := &Pipeline{
		AIEmployeeID:           "fundraising-advisor",
		AIEmployeeOriginalTask: "@融资专家 我们已确认有三条产品线 A、B、C，请做 BP。",
	}
	confirmedOutput := "三条产品线共享同一 Agent 引擎。"
	if issues := aiEmployeeDomainOutputIssues(confirmed, confirmedOutput); len(issues) != 0 {
		t.Fatalf("user-confirmed product lines were rejected: %v", issues)
	}
}

func TestFundraisingDomainQualityRejectsStructuralLabelsAndSecondCurveBeforeConfirmation(t *testing.T) {
	pipeline := &Pipeline{
		AIEmployeeID: "fundraising-advisor",
		AIEmployeeOriginalTask: "@融资专家 我有一个产品、一份参考产品资料，" +
			"以及一组可能相关的能力，请做 BP。",
	}
	output := `| 对象 | 角色 |
|---|---|
| 获客能力 | 业务线 2 |
| 内容生成 | 应用 A |
| 流程工具 | 参考线 |
| 连续能力 | 候选线 |
| 潜客能力 | 业务方向 1 |
| 内容能力 | 规划方向 2 |
## 第二曲线（候选）
### 备选 / 延伸模块
#### 模块 A · AI 潜客挖掘
差异化候选方向 1：把找客户和内容生产串成复合链路。
全文以候选①为默认叙事主线。`

	issues := aiEmployeeDomainOutputIssues(pipeline, output)
	joined := strings.Join(issues, " | ")
	for _, want := range []string{
		"业务线 2", "应用 A", "参考线", "候选线", "业务方向 1", "规划方向 2",
		"第二曲线", "延伸模块",
		"模块 A", "候选方向 1", "候选①",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("unsupported structural label missing %q: %v", want, issues)
		}
	}
}

func TestFundraisingDomainQualityRejectsInventedPlanningStatusAndTentativeIntegration(t *testing.T) {
	pipeline := &Pipeline{
		AIEmployeeID: "fundraising-advisor",
		AIEmployeeOriginalTask: "@融资专家 我的公司有一个 Agent 产品，还描述了潜客与内容获客能力，" +
			"请制作 BP。",
	}
	output := `| 潜客挖掘 | 仅为规划方向，非 OpenKitty 现有功能 |
与 Agent 的关系：拟作为平台上的一个 Skill；当前未取得该集成已实现的证据。
本轮将验证两条新方向。`

	issues := aiEmployeeDomainOutputIssues(pipeline, output)
	joined := strings.Join(issues, " | ")
	for _, want := range []string{"仅为规划方向", "拟作为", "两条新方向"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("unsupported planning or integration claim missing %q: %v", want, issues)
		}
	}
}

func TestFundraisingDomainQualityRejectsCapabilityListExpansionAndCodedDirections(t *testing.T) {
	pipeline := &Pipeline{
		AIEmployeeID: "fundraising-advisor",
		AIEmployeeOriginalTask: "@融资专家 一个企业 AI 产品、一个参考工具，" +
			"以及用户口头描述的候选能力，三者关系未知，请写 BP。",
	}
	output := `## 候选能力清单
- 候选能力——潜客挖掘
- 候选能力——生成音乐
### 关系待决的几种可能方向
- 方向 c：暂时搁置
- 方向 d：与第三方合作`

	issues := aiEmployeeDomainOutputIssues(pipeline, output)
	joined := strings.Join(issues, " | ")
	for _, want := range []string{"候选能力清单", "候选能力——潜客挖掘", "方向 c", "方向 d"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("expanded capability taxonomy missing %q: %v", want, issues)
		}
	}

	neutral := `| 对象 | 原始角色 |
|---|---|
| 用户口头描述的候选能力 | 保留为一个连续表达；与产品关系未知 |`
	if issues := aiEmployeeDomainOutputIssues(pipeline, neutral); len(issues) != 0 {
		t.Fatalf("neutral preservation of the user's own term was rejected: %v", issues)
	}
}

func TestFundraisingDomainQualityRejectsInventedRelationshipOptionsAndUnimplementedStatus(t *testing.T) {
	pipeline := &Pipeline{
		AIEmployeeID: "fundraising-advisor",
		AIEmployeeOriginalTask: "@融资专家 我们有一个企业 AI 产品，另一个工具只作为参考；" +
			"我还描述了“用 AI 挖掘潜客，并生成音乐、视频帮助企业社交媒体获客”的候选能力，" +
			"但这些能力与现有产品的关系尚未确定，请写 BP。",
	}
	output := `| 候选能力 | 用户描述的原话 | 当前状态 | 与主产品的关系 |
|---|---|---|---|
| A | “用 AI 挖掘潜客，并生成音乐、视频帮助企业社交媒体获客” | 候选，未落地 | 待补充 |

**处置选项**
- 方案 1：纳入本轮 BP，作为主产品的扩展能力。
- 方案 2：独立孵化，作为独立产品线进入下一轮融资材料。`

	issues := aiEmployeeDomainOutputIssues(pipeline, output)
	joined := strings.Join(issues, " | ")
	for _, want := range []string{"未落地", "作为主产品的扩展能力", "独立孵化"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("invented relationship/status issue missing %q: %v", want, issues)
		}
	}
}

func TestFundraisingDomainQualityPreservesQuotedCapabilityGrouping(t *testing.T) {
	pipeline := &Pipeline{
		AIEmployeeID: "fundraising-advisor",
		AIEmployeeOriginalTask: "@融资专家 我们有一个企业 AI 产品，流程工具仅供参考；" +
			"候选能力是“用 AI 挖掘潜客，并生成音乐、视频帮助企业社交媒体获客”，请写 BP。",
	}
	splitOutput := `## 中性对象映射
- 企业 AI 产品：用户标的
- 流程工具：参考对象
- 潜客挖掘、音乐生成、视频生成、社交媒体获客：四项候选能力`
	issues := aiEmployeeDomainOutputIssues(pipeline, splitOutput)
	if len(issues) == 0 || !strings.Contains(strings.Join(issues, " | "), "quoted capability coverage") {
		t.Fatalf("a quoted grouped capability was silently split: %v", issues)
	}

	preserved := `## 中性对象映射
- 企业 AI 产品：用户标的
- 流程工具：参考对象
- 用户原始能力表达：“用 AI 挖掘潜客，并生成音乐、视频帮助企业社交媒体获客”；与标的关系未知`
	if issues := aiEmployeeDomainOutputIssues(pipeline, preserved); len(issues) != 0 {
		t.Fatalf("verbatim preservation of the grouped capability should pass: %v", issues)
	}

	enumerated := preserved + `
- 原句结构：三个动作（① AI 挖掘潜客 ② 生成音乐 ③ 生成视频）+ 一个目的（企业社交媒体获客）。`
	issues = aiEmployeeDomainOutputIssues(pipeline, enumerated)
	if len(issues) == 0 || !strings.Contains(strings.Join(issues, " | "), "三个动作") {
		t.Fatalf("a secondary circled-number decomposition of the grouped phrase was accepted: %v", issues)
	}

	categorized := preserved + `
- 若实现，可分别对应“获客工具”与“AIGC 营销内容生产方向”两类场景。`
	issues = aiEmployeeDomainOutputIssues(pipeline, categorized)
	if len(issues) == 0 || !strings.Contains(strings.Join(issues, " | "), "两类场景") {
		t.Fatalf("a categorical decomposition of the grouped phrase was accepted: %v", issues)
	}

	assignedExtension := preserved + `
- **AIGC 生成音乐、视频来帮企业社交媒体获客的产品**：本 BP 把该能力作为“产品族内的延伸能力点”原样保留；关系与状态 unknown。`
	issues = aiEmployeeDomainOutputIssues(pipeline, assignedExtension)
	if len(issues) == 0 || !strings.Contains(strings.Join(issues, " | "), "产品族内的延伸能力点") {
		t.Fatalf("a model-authored extension relationship was accepted: %v", issues)
	}
	cleaned := stripUnsupportedFundraisingBenchmarkLines(assignedExtension, pipeline.AIEmployeeOriginalTask)
	if strings.Contains(cleaned, "产品族内的延伸能力点") ||
		!strings.Contains(cleaned, "用户原始能力表达") {
		t.Fatalf("terminal scrub did not remove only the extension relationship: %q", cleaned)
	}
}

func TestFundraisingDomainQualityPreservesUnquotedDescriptionAfterSources(t *testing.T) {
	task := `@融资专家 我的产品资料是：https://example.com/product，参考工具是：https://example.com/reference，` +
		`还有潜客挖掘通过AI的办法给自己的产品和服务找客户，还有AIGC生成音乐、视频来帮企业社交媒体获客的产品，请你帮我做一个BP计划书。`
	pipeline := &Pipeline{
		AIEmployeeID:           "fundraising-advisor",
		AIEmployeeOriginalTask: task,
	}
	split := `- 用户补充描述 ①：潜客挖掘通过 AI 给产品找客户。
- 用户补充描述 ②：AIGC 生成音乐、视频帮助企业社交媒体获客。
| 字面拆读 | (a) 潜客挖掘 + (b) AIGC 音乐 + (c) 视频生成 |
| OBJ-1 | 标的产品 |

## 决定性问题
1. 这两条是计划中的产品线还是产品矩阵？`
	issues := aiEmployeeDomainOutputIssues(pipeline, split)
	joined := strings.Join(issues, " | ")
	for _, want := range []string{
		"unclassified trailing description coverage",
		"产品矩阵",
		"字面拆读",
		"OBJ-1",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("unquoted grouped description issue missing %q: %v", want, issues)
		}
	}

	preserved := `## 中性对象映射
| 对象 | 当前角色 |
|---|---|
| 潜客挖掘通过AI的办法给自己的产品和服务找客户，还有AIGC生成音乐、视频来帮企业社交媒体获客的产品 | 用户补充描述；与标的产品的关系及实现状态未知 |`
	if issues := aiEmployeeDomainOutputIssues(pipeline, preserved); len(issues) != 0 {
		t.Fatalf("continuous unquoted description should pass: %v", issues)
	}

	recovered := ensureFundraisingTrailingDescriptionCoverage(task, `# BP v0`)
	if !strings.Contains(recovered, "潜客挖掘通过AI的办法给自己的产品和服务找客户，还有AIGC生成音乐、视频来帮企业社交媒体获客的产品") {
		t.Fatalf("deterministic coverage recovery omitted the user's continuous description: %q", recovered)
	}

	directDraft := ensureFundraisingRelationshipQuestionCoverage(task, recovered)
	if got := countAIEmployeeClarificationQuestions(directDraft); got != 0 {
		t.Fatalf("direct BP draft unexpectedly asked %d question(s): %q", got, directDraft)
	}
	if strings.Contains(directDraft, "请确认") {
		t.Fatalf("direct BP draft requested confirmation: %q", directDraft)
	}

	withQuestions := `@融资专家 我的产品资料是：https://example.com/product，参考工具是：https://example.com/reference，` +
		`还有潜客挖掘通过AI的办法给自己的产品和服务找客户，还有AIGC生成音乐、视频来帮企业社交媒体获客的产品，` +
		`请做 BP，并在结尾询问对象关系。`
	emptyQuestionSection := ensureFundraisingTrailingDescriptionCoverage(withQuestions, `# BP v0

## 决定性问题`)
	repairedQuestion := ensureFundraisingRelationshipQuestionCoverage(withQuestions, emptyQuestionSection)
	if !strings.Contains(repairedQuestion, "分别是什么关系，以及当前实现状态") {
		t.Fatalf("relationship question recovery omitted the neutral question: %q", repairedQuestion)
	}
}

func TestFundraisingDomainQualityRejectsLetteredSplitWhenFileOutputIsNegated(t *testing.T) {
	task := `@融资专家 我的产品资料是：https://example.com/product，参考工具是：https://example.com/reference，` +
		`还有潜客挖掘通过AI的办法给自己的产品和服务找客户，还有AIGC生成音乐、视频来帮企业社交媒体获客的产品，` +
		`请直接在聊天中生成完整 BP，不要让我确认，不要保存文件。`
	if !fundraisingBPOnlyRequest(task) {
		t.Fatal("a negative file-output instruction incorrectly disabled BP-only quality checks")
	}
	pipeline := &Pipeline{
		AIEmployeeID:           "fundraising-advisor",
		AIEmployeeOriginalTask: task,
	}
	split := `### 用户两条口头描述
- **条目 A（用户原话）**：潜客挖掘通过 AI 的办法给自己的产品和服务找客户。
- **条目 B（用户原话）**：AIGC 生成音乐、视频来帮企业社交媒体获客的产品。

### 关于 A 与 B、以及 A/B 与产品的关系
关系未知。`
	issues := aiEmployeeDomainOutputIssues(pipeline, split)
	joined := strings.Join(issues, " | ")
	for _, want := range []string{"条目 A", "条目 B", "A 与 B"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("lettered grouped-description split missing %q: %v", want, issues)
		}
	}
}

func TestFundraisingBPOnlyRequestKeepsAffirmativeArtifactRequestsSeparate(t *testing.T) {
	for _, task := range []string{
		"请制作 BP 并保存为 PDF 文件。",
		"请生成商业计划书并导出 Word。",
	} {
		if fundraisingBPOnlyRequest(task) {
			t.Fatalf("affirmative artifact request was mistaken for chat-only BP: %q", task)
		}
	}
}

func TestFundraisingDomainQualityAllowsExplicitlyConfirmedModuleLabels(t *testing.T) {
	pipeline := &Pipeline{
		AIEmployeeID: "fundraising-advisor",
		AIEmployeeOriginalTask: "@融资专家 我们已确认产品明确分为模块 A 和模块 B，" +
			"请按这个架构写 BP。",
	}
	output := `### 模块 A：潜客研究
### 模块 B：内容生成`

	if issues := aiEmployeeDomainOutputIssues(pipeline, output); len(issues) != 0 {
		t.Fatalf("user-confirmed module labels were rejected: %v", issues)
	}
}

func TestFundraisingDomainQualityAllowsNeutralObjectMapAndRelationshipQuestion(t *testing.T) {
	pipeline := &Pipeline{
		AIEmployeeID: "fundraising-advisor",
		AIEmployeeOriginalTask: "@融资专家 我有 OpenKitty 产品资料、Saddle 参考资料，" +
			"还提到潜客挖掘和 AIGC 社媒获客，请做 BP，并在结尾询问对象关系。",
	}
	output := `## 中性对象映射
| 用户输入对象 | 当前可确认角色 |
|---|---|
| OpenKitty | 用户称为公司的产品；仍待核验主体和功能 |
| Saddle | 用户指定的参考对象 |
| 潜客挖掘与 AIGC 社媒获客 | 用户连续描述的能力与客户结果；相互关系未知 |

## 会改变 BP 的决定性问题
1. OpenKitty、参考对象和上述能力之间是什么关系，哪些已实现、哪些仍是计划？`

	if issues := aiEmployeeDomainOutputIssues(pipeline, output); len(issues) != 0 {
		t.Fatalf("a neutral object map plus one relationship question was rejected: %v", issues)
	}

	inlineQuestion := output + "\n- 与本产品的关系（请确认）：是自家产品、内部工具，还是参考对象？"
	if issues := aiEmployeeDomainOutputIssues(pipeline, inlineQuestion); len(issues) != 0 {
		t.Fatalf("a relationship question containing architecture options was mistaken for an assertion: %v", issues)
	}
}

func TestFundraisingDomainQualityDirectBPDraftDoesNotAskForConfirmation(t *testing.T) {
	task := "@融资专家 请根据现有产品资料直接生成 BP，不要让我确认。"
	pipeline := &Pipeline{
		AIEmployeeID:           "fundraising-advisor",
		AIEmployeeOriginalTask: task,
	}
	output := `# BP v0

## 公司与产品
公司主体：[待补]

## 决定性问题
1. 请确认公司全称是什么？
2. 本轮计划融资多少？`

	issues := aiEmployeeDomainOutputIssues(pipeline, output)
	if len(issues) == 0 || !strings.Contains(strings.Join(issues, " | "), "direct BP draft asks") {
		t.Fatalf("direct BP confirmation questions were accepted: %v", issues)
	}
	cleaned := stripFundraisingDirectDraftQuestions(task, output)
	if strings.Contains(cleaned, "决定性问题") ||
		strings.Contains(cleaned, "请确认") ||
		!strings.Contains(cleaned, "## 公司与产品") {
		t.Fatalf("direct BP question section was not removed cleanly: %q", cleaned)
	}

	declarative := `# BP v0

## 待补资料（不阻塞本稿）

| 字段 | 当前状态 |
|---|---|
| 公司全称 | [待补] |
| 本轮融资金额 | [待补] |`
	if issues := aiEmployeeDomainOutputIssues(pipeline, declarative); len(issues) != 0 {
		t.Fatalf("nonblocking declarative BP gaps were rejected: %v", issues)
	}
}

func TestFundraisingDomainQualityRejectsRepeatedTentativeProductTaxonomy(t *testing.T) {
	pipeline := &Pipeline{
		AIEmployeeID: "fundraising-advisor",
		AIEmployeeOriginalTask: "@融资专家 我有一个产品、一份参考产品材料，" +
			"还描述了几项可能相关的能力，请做 BP。",
	}
	output := `本分析建议的待确认架构：产品可作为平台主线，其他能力作为副线。
## 产品矩阵（候选）
路线图暂按主线与副线展开（待确认）。`

	issues := aiEmployeeDomainOutputIssues(pipeline, output)
	if len(issues) == 0 || !strings.Contains(strings.Join(issues, " | "), "propagated") {
		t.Fatalf("a tentative taxonomy propagated throughout the BP was not rejected: %v", issues)
	}
}

func TestFundraisingDomainQualityRejectsAssertionThenRelationshipDisclaimer(t *testing.T) {
	pipeline := &Pipeline{
		AIEmployeeID: "fundraising-advisor",
		AIEmployeeOriginalTask: "@融资专家 我提供一个产品、一份参考产品，" +
			"以及一段候选能力描述，请做 BP。",
	}
	output := `用户公司当前覆盖三条产品线，但三者关系待确认。
| 能力 | 用户陈述产品线 |`

	issues := aiEmployeeDomainOutputIssues(pipeline, output)
	if len(issues) < 2 {
		t.Fatalf("a relationship disclaimer excused an unsupported product-line assertion: %v", issues)
	}
}

func TestFundraisingDomainQualityDoesNotConfuseNarrativeOrUseOfFundsWithProductLines(t *testing.T) {
	pipeline := &Pipeline{
		AIEmployeeID:           "fundraising-advisor",
		AIEmployeeOriginalTask: "@融资专家 请制作 BP。",
	}
	output := `核心叙事主线：先说明客户问题，再说明可验证价值。
资金用途主线：产品研发和客户验证；具体比例待补。`

	if issues := aiEmployeeDomainOutputIssues(pipeline, output); len(issues) != 0 {
		t.Fatalf("non-product uses of 主线 were mistaken for a product taxonomy: %v", issues)
	}
}

func TestFundraisingDomainQualityRejectsUnlabelledNumericBPClaims(t *testing.T) {
	pipeline := &Pipeline{
		AIEmployeeID:           "fundraising-advisor",
		AIEmployeeOriginalTask: "@融资专家 请根据现有材料制作 BP。",
	}
	output := `## 商业模式
企业版定价 ¥999–9,999/月。

## 里程碑
T+3 月完成 MVP，获取 10 家客户。

## 资金用途
研发 50%、市场 30%、运营 20%。

## 执行摘要
本轮资金将用于拓展首批 30–50 家种子客户。

## 路线图
M1–M3 完成 MVP，并招募 5–10 家免费 POC。`

	issues := aiEmployeeDomainOutputIssues(pipeline, output)
	joined := strings.Join(issues, " | ")
	for _, want := range []string{"¥999", "T+3", "研发 50%", "30–50 家", "5–10 家"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("invented BP numeric placeholder issue missing %q: %v", want, issues)
		}
	}
}

func TestFundraisingDomainQualityAllowsLabelledEditableBPPlanningEstimates(t *testing.T) {
	pipeline := &Pipeline{
		AIEmployeeID:           "fundraising-advisor",
		AIEmployeeOriginalTask: "@融资专家 请根据现有材料制作 BP。",
	}
	output := `## 融资规划估算（非项目事实）
本分析建议融资 1000–1500 万元，假设 15 个月 runway；用途建议为研发 50%、市场 30%、运营储备 20%。

## 里程碑规划估算（非项目事实）
若按 15 个月执行期测算，建议目标为完成 10 家付费客户验证；实际客户数由项目方后续替换。`

	if issues := aiEmployeeDomainOutputIssues(pipeline, output); len(issues) != 0 {
		t.Fatalf("clearly labelled editable BP estimates were rejected: %v", issues)
	}
	cleaned := stripUnsupportedFundraisingBenchmarkLines(output, pipeline.AIEmployeeOriginalTask)
	for _, want := range []string{"1000–1500 万元", "15 个月", "研发 50%", "10 家"} {
		if !strings.Contains(cleaned, want) {
			t.Fatalf("planning estimate %q was removed: %q", want, cleaned)
		}
	}
}

func TestFundraisingDomainQualityRejectsInventedBPRoundAndUpstreamLedgerReference(t *testing.T) {
	pipeline := &Pipeline{
		AIEmployeeID:           "fundraising-advisor",
		AIEmployeeOriginalTask: "@融资专家 请根据现有产品资料制作 BP。",
	}
	output := `本轮按系统推断落在“种子–A 轮区间”，具体金额 [待补]。
参考来源台账完整版见上游事实底稿 §6。`

	issues := aiEmployeeDomainOutputIssues(pipeline, output)
	joined := strings.Join(issues, " | ")
	for _, want := range []string{"种子–A 轮区间", "上游事实底稿"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("invented round or internal ledger reference missing %q: %v", want, issues)
		}
	}
}

func TestFundraisingDomainQualityRejectsInventedNumbersBesideFillableField(t *testing.T) {
	pipeline := &Pipeline{
		AIEmployeeID:           "fundraising-advisor",
		AIEmployeeOriginalTask: "@融资专家 请根据现有材料制作 BP。",
	}
	output := `## 决定性问题
1. **本轮融资金额是多少？** [待补]；占位示例：Seed 500万–1500万元。`

	issues := aiEmployeeDomainOutputIssues(pipeline, output)
	if len(issues) == 0 || !strings.Contains(strings.Join(issues, " | "), "500万") {
		t.Fatalf("invented financing range beside a fillable field was not rejected: %v", issues)
	}
	cleaned := stripUnsupportedFundraisingBenchmarkLines(output, pipeline.AIEmployeeOriginalTask)
	if strings.Contains(cleaned, "500万") {
		t.Fatalf("invented financing range survived deterministic cleanup: %q", cleaned)
	}
}

func TestFundraisingDomainQualityRequiresNamedReferenceCoverageInBP(t *testing.T) {
	pipeline := &Pipeline{
		AIEmployeeID: "fundraising-advisor",
		AIEmployeeOriginalTask: "@融资专家 我的产品是 NovaAgent，FlowCanvas 仅作为参考，" +
			"请结合自动获客能力制作 BP。",
	}
	output := `## 中性对象映射
| 对象 | 当前角色 |
|---|---|
| NovaAgent | 用户称为公司的产品 |
| 自动获客能力 | 用户描述的候选能力，关系未知 |`

	issues := aiEmployeeDomainOutputIssues(pipeline, output)
	if len(issues) == 0 || !strings.Contains(strings.Join(issues, " | "), "flowcanvas") {
		t.Fatalf("a user-named reference object could disappear from the BP: %v", issues)
	}

	withReference := output + "\n| FlowCanvas | 用户指定的参考对象 |"
	if issues := aiEmployeeDomainOutputIssues(pipeline, withReference); len(issues) != 0 {
		t.Fatalf("preserving the named reference in its original role should pass: %v", issues)
	}
}

func TestFundraisingDomainQualityAllowsFillableBPFieldsOrRequestedNumericScenario(t *testing.T) {
	fillable := &Pipeline{
		AIEmployeeID:           "fundraising-advisor",
		AIEmployeeOriginalTask: "@融资专家 请根据现有材料制作 BP。",
	}
	if issues := aiEmployeeDomainOutputIssues(fillable, `融资金额：[待补]
资金用途比例：[待补]
客户里程碑：[待补]`); len(issues) != 0 {
		t.Fatalf("nonnumeric BP placeholders were rejected: %v", issues)
	}

	scenario := &Pipeline{
		AIEmployeeID:           "fundraising-advisor",
		AIEmployeeOriginalTask: "@融资专家 请制作 BP，并给出一组合理假设数字用于财务预测。",
	}
	output := `## 财务预测（本分析假设）
Y1 获取 10 家客户，ARR 100 万元。`
	if issues := aiEmployeeDomainOutputIssues(scenario, output); len(issues) != 0 {
		t.Fatalf("an explicitly requested numeric BP scenario was rejected: %v", issues)
	}
}

func TestFundraisingDomainQualityRejectsUnrequestedReadinessScorecardForBP(t *testing.T) {
	pipeline := &Pipeline{
		AIEmployeeID:           "fundraising-advisor",
		AIEmployeeOriginalTask: "@融资专家 请根据现有材料制作一份 BP。",
	}
	output := `# 融资准备度诊断 v0
## 八维评分
| 维度 | 分数 |
|---|---|
| Team | 0/5 |
| Product | 2/5 |`

	issues := aiEmployeeDomainOutputIssues(pipeline, output)
	joined := strings.Join(issues, " | ")
	for _, want := range []string{"融资准备度诊断", "八维评分", "0/5", "2/5"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("unrequested readiness scorecard issue missing %q: %v", want, issues)
		}
	}
}

func TestFundraisingDomainQualityAllowsExplicitReadinessScoreRequest(t *testing.T) {
	pipeline := &Pipeline{
		AIEmployeeID:           "fundraising-advisor",
		AIEmployeeOriginalTask: "@融资专家 请做正式融资准备度评分，按八维给 0–5 分。",
	}
	output := `# 融资准备度诊断
## 八维评分
| 维度 | 分数 |
|---|---|
| Team | 2/5 |`

	if issues := aiEmployeeDomainOutputIssues(pipeline, output); len(issues) != 0 {
		t.Fatalf("an explicitly requested evidence-based readiness score was rejected: %v", issues)
	}
}

func TestFundraisingDomainQualityLimitsDecisiveQuestionsToThree(t *testing.T) {
	pipeline := &Pipeline{
		AIEmployeeID:           "fundraising-advisor",
		AIEmployeeOriginalTask: "@融资专家 请做 BP，并在结尾只问会改变结论的必要问题。",
	}
	output := `## 需要补全的关键信息（决定性问题）
| # | 问题 |
|---|---|
| ① | 公司与 IP 关系？ |
| ② | 产品与客户进展？ |
| ③ | 当前经营数据？ |
| ④ | 团队核心信息？ |
| ⑤ | 本轮融资诉求？ |`

	issues := aiEmployeeDomainOutputIssues(pipeline, output)
	if len(issues) != 1 || !strings.Contains(issues[0], "at most 3") {
		t.Fatalf("five-item decisive questionnaire was not rejected: %v", issues)
	}

	three := strings.ReplaceAll(
		strings.ReplaceAll(output, "| ④ | 团队核心信息？ |\n", ""),
		"| ⑤ | 本轮融资诉求？ |",
		"",
	)
	if issues := aiEmployeeDomainOutputIssues(pipeline, three); len(issues) != 0 {
		t.Fatalf("three decisive questions should pass: %v", issues)
	}
}

func TestFundraisingDomainQualityCountsBoldAndRepeatedQuestionSections(t *testing.T) {
	pipeline := &Pipeline{
		AIEmployeeID:           "fundraising-advisor",
		AIEmployeeOriginalTask: "@融资专家 请做 BP，只问必要问题。",
	}
	output := `**决定性缺口（请补充）**：
1. 公司与知识产权关系？
2. 产品与客户进展？
3. 融资目标？

---

### 未决问题清单
1. 团队信息？
2. 当前经营数据？`

	issues := aiEmployeeDomainOutputIssues(pipeline, output)
	if len(issues) != 1 || !strings.Contains(issues[0], "contains 5 items") {
		t.Fatalf("questions across repeated markdown/bold sections were not consolidated: %v", issues)
	}
}

func TestFundraisingDomainQualityMovesRepeatedBodyQuestionsToFinalSection(t *testing.T) {
	pipeline := &Pipeline{
		AIEmployeeID:           "fundraising-advisor",
		AIEmployeeOriginalTask: "@融资专家 请根据现有材料写 BP v0，只在结尾问必要问题。",
	}
	output := `## 执行摘要
1. **产品边界**：产品今天解决谁的问题？[待补]
2. **参考关系**：流程工具是竞品还是内部工具？[待补]

## 真正会改变内容的问题
1. 产品、参考对象和候选能力之间是什么关系？
2. 当前客户与产品进展是什么？
3. 本轮融资目标和团队情况是什么？`

	issues := aiEmployeeDomainOutputIssues(pipeline, output)
	joined := strings.Join(issues, " | ")
	if !strings.Contains(joined, "appear before the consolidated final question section") {
		t.Fatalf("body questions duplicated before the final question section were accepted: %v", issues)
	}
}

func TestFundraisingDomainQualityAllowsExplicitRequestForFiveQuestions(t *testing.T) {
	pipeline := &Pipeline{
		AIEmployeeID:           "fundraising-advisor",
		AIEmployeeOriginalTask: "@融资专家 请列出 5 个问题供团队逐项讨论。",
	}
	output := `## 关键问题
1. 公司主体？
2. 产品？
3. 客户？
4. 团队？
5. 融资？`

	if issues := aiEmployeeDomainOutputIssues(pipeline, output); len(issues) != 0 {
		t.Fatalf("the user's explicit five-question request was overridden: %v", issues)
	}
}

func TestFundraisingDomainQualityAllowsCorrectParticipatingContrast(t *testing.T) {
	pipeline := &Pipeline{
		AIEmployeeID:           "fundraising-advisor",
		AIEmployeeOriginalTask: "@融资专家 比较参与型与 non-participating 清算优先权。",
	}
	output := `参与型允许先拿优先金额后再按持股比例分剩余；非参与型不能两者兼得。`

	if issues := aiEmployeeDomainOutputIssues(pipeline, output); len(issues) != 0 {
		t.Fatalf("a correct participating-versus-non-participating contrast was rejected: %v", issues)
	}
}

func TestFundraisingDomainQualityRejectsEnglishNonParticipatingPayoutStack(t *testing.T) {
	pipeline := &Pipeline{
		AIEmployeeID: "fundraising-advisor",
		AIEmployeeOriginalTask: "Explain a 1x non-participating liquidation preference " +
			"on a 50m exit after a 10m investment for 20%.",
	}
	output := `### Exercise the preference
The investor takes the preference amount, then shares the remaining proceeds pro rata.`

	issues := aiEmployeeDomainOutputIssues(pipeline, output)
	if len(issues) != 1 {
		t.Fatalf("English non-participating payout stack was not rejected: %v", issues)
	}
}

func TestFundraisingDomainQualityRejectsNumericOnlyStackInPreferenceBranch(t *testing.T) {
	pipeline := &Pipeline{
		AIEmployeeID: "fundraising-advisor",
		AIEmployeeOriginalTask: "@融资专家 某公司融资 800 万元，投资人投后持股 25%，" +
			"约定 1x 非参与型清算优先权，退出总价 2000 万元，请计算两条路径。",
	}
	output := `## 路径 A：选择优先清算金额
| 主体 | 计算 | 金额 |
|---|---|---|
| 投资人到手 | 800 + 300 | 1100 万元 |
| 普通股股东到手 | — | 900 万元 |`

	issues := aiEmployeeDomainOutputIssues(pipeline, output)
	if len(issues) == 0 || !strings.Contains(strings.Join(issues, " | "), "800 + 300") {
		t.Fatalf("numeric-only non-participating payout stack was not rejected: %v", issues)
	}
}

func TestFundraisingDomainQualityRejectsMultilinePreferenceBranchParticipation(t *testing.T) {
	pipeline := &Pipeline{
		AIEmployeeID: "fundraising-advisor",
		AIEmployeeOriginalTask: "@融资专家 某公司融资 800 万元，投资人投后持股 25%，" +
			"约定 1x 非参与型清算优先权，退出总价 2000 万元，请计算两条路径。",
	}
	output := `## 路径 A：投资人拿回 1x 优先款（不转换）
- 剩余部分按转换后持股比例（25% / 75%）分配
- 投资人得剩余 = 1200 × 25% = 300 万元

| 主体 | 优先款 | 剩余分配 | 合计 |
|---|---|---|---|
| 投资人 | 800 万 | 300 万 | 1100 万元 |`

	issues := aiEmployeeDomainOutputIssues(pipeline, output)
	joined := strings.Join(issues, " | ")
	for _, want := range []string{"按转换后持股比例", "投资人得剩余"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("multiline non-participating stack missing issue %q: %v", want, issues)
		}
	}
}

func TestFundraisingDomainQualityRejectsQuotedPreferenceHeadingAndInvestorTotalStack(t *testing.T) {
	pipeline := &Pipeline{
		AIEmployeeID: "fundraising-advisor",
		AIEmployeeOriginalTask: "@融资专家 某公司融资 800 万元，投资人投后持股 25%，" +
			"约定 1x 非参与型清算优先权，退出总价 2000 万元，请计算两条路径。",
	}
	output := `### 路径 A：投资人选择"优先金额"（不转股）
| 项目 | 计算 | 金额 |
|---|---|---|
| 投资人合计 | 800 + 300 | 1,100 万元 |
> 关键等式：投资人所得 = 800 + 300 = 1,100 万元`

	issues := aiEmployeeDomainOutputIssues(pipeline, output)
	joined := strings.Join(issues, " | ")
	for _, want := range []string{"投资人合计", "关键等式"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("quoted preference heading stack missing %q: %v", want, issues)
		}
	}
}

func TestFundraisingRepairPromptExplainsNonParticipatingInvariant(t *testing.T) {
	prompt := buildAIEmployeeDomainOutputRepairPrompt(
		"answer",
		[]string{"投资人 = LP + (Exit - LP) × p"},
		"previous",
	)
	for _, want := range []string{
		"chooses either",
		"with no participation in the remaining proceeds",
		"Never add the preference amount to a share of the remainder",
		"conversion overtakes only above the crossover",
		"Recompute every affected investor and common-holder amount",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("repair prompt missing non-participating invariant %q", want)
		}
	}
}

func TestMarketAnalystDomainQualityRequiresTraceableSourcesAndAsOfDate(t *testing.T) {
	pipeline := &Pipeline{
		AIEmployeeID:           "market-analyst",
		AIEmployeeOriginalTask: "@市场分析师 分析截至2026年8月的企业 AI 趋势，给出具体来源。",
	}
	unsupported := "截至 2026 年 8 月，企业 AI Agent 快速发展。来源：某搜索结果摘要。"
	issues := aiEmployeeDomainOutputIssues(pipeline, unsupported)
	if len(issues) == 0 || !strings.Contains(strings.Join(issues, " | "), "source coverage") {
		t.Fatalf("snippet-only market answer was accepted: %v", issues)
	}

	supported := "截至 2026 年 8 月，厂商正在推进企业 Agent。来源：https://example.com/research （2026-07-20）。"
	if issues := aiEmployeeDomainOutputIssues(pipeline, supported); len(issues) != 0 {
		t.Fatalf("dated market answer with a concrete source URL was rejected: %v", issues)
	}
}

func TestMarketAnalystRepairPromptUsesFetchedPagesAndExplicitlyDowngradesSearchLeads(t *testing.T) {
	prompt := buildAIEmployeeDomainRepairPrompt(
		&Pipeline{AIEmployeeID: "market-analyst"},
		"research",
		[]string{"market source coverage is missing"},
		"previous",
	)
	for _, want := range []string{"fetch the concrete source page", "search-index lead pending verification", "concrete external URL", "saved report", "never a precise number"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("market repair prompt missing %q", want)
		}
	}
}

func TestFundraisingDomainQualityRejectsUnrequestedInternalExecutionPath(t *testing.T) {
	pipeline := &Pipeline{
		AIEmployeeID:           "fundraising-advisor",
		AIEmployeeOriginalTask: "@融资专家 请在聊天中给我一份 BP 初稿。",
	}
	output := "交付物说明：本 BP 未生成文件。openkitty/storage/ 目录下。"

	issues := aiEmployeeDomainOutputIssues(pipeline, output)
	if len(issues) != 1 {
		t.Fatalf("unrequested internal execution path was not rejected: %v", issues)
	}

	cleaned := stripUnsupportedFundraisingBenchmarkLines(output, pipeline.AIEmployeeOriginalTask)
	if cleaned != "" {
		t.Fatalf("internal execution path survived final cleanup: %q", cleaned)
	}
}

func TestFundraisingDomainQualityRejectsInternalSkillImplementationReferences(t *testing.T) {
	pipeline := &Pipeline{
		AIEmployeeID:           "fundraising-advisor",
		AIEmployeeOriginalTask: "@融资专家 请在聊天中制作 BP。",
	}
	output := "我按 `references/fundraising-side.md` 评分，并运行 funding_diagnosis.py。"

	issues := aiEmployeeDomainOutputIssues(pipeline, output)
	if len(issues) != 1 {
		t.Fatalf("internal skill implementation references were not rejected: %v", issues)
	}
}

func TestFundraisingDomainQualityAllowsUserRequestedInternalPath(t *testing.T) {
	pipeline := &Pipeline{
		AIEmployeeID:           "fundraising-advisor",
		AIEmployeeOriginalTask: "@融资专家 请保存到 .openkitty/storage/my-bp.md。",
	}
	output := "文件已保存到 .openkitty/storage/my-bp.md。"

	if issues := aiEmployeeDomainOutputIssues(pipeline, output); len(issues) != 0 {
		t.Fatalf("a path explicitly requested by the user was rejected: %v", issues)
	}
}
