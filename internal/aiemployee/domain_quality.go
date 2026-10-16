package aiemployee

import (
	"fmt"
	"regexp"
	"strings"

	pipelinecore "mac/internal/pipeline"
	"mac/internal/textutil"
)

type Pipeline = pipelinecore.Pipeline

func truncate(value string, maximum int) string {
	return textutil.Truncate(value, maximum)
}

func fundraisingBPOnlyRequest(task string) bool {
	normalized := strings.ToLower(strings.TrimSpace(task))
	if !containsAnyText(normalized, []string{
		"bp", "商业计划书", "计划书", "pitch deck", "business plan",
	}) {
		return false
	}
	artifactSensitive := fundraisingNegativeArtifactDirectiveRe.ReplaceAllString(normalized, " ")
	return !containsAnyText(artifactSensitive, []string{
		"准备度", "评分", "打分", "诊断", "测算", "计算", "cap table",
		"股权表", "稀释", "财务模型", "保存", "下载", "文件", "markdown",
		"pdf", "word", "ppt", "pptx",
	})
}

var (
	fundraisingRelativeBenchmarkRe         = regexp.MustCompile(`(?i)(?:高于|低于|超过|不及)[^。；;\n]{0,28}(?:平均|中位|基准|行业|同类|典型)|(?:行业|同类|典型)[^。；;\n]{0,28}(?:平均|中位|基准|水平|区间)`)
	fundraisingQuantityRe                  = regexp.MustCompile(`(?i)(?:\d+(?:[.,]\d+)?(?:\s*[–—~～\-至到]\s*\d+(?:[.,]\d+)?)?\s*(?:%|％|万(?:元)?|亿(?:元)?|个月|月|年|家|倍)|数十万|数百万|数千万|几十万|几百万|几千万)`)
	fundraisingThresholdQuantityRe         = regexp.MustCompile(`(?i)(?:\d+(?:[.,]\d+)?(?:\s*[–—~～\-至到]\s*\d+(?:[.,]\d+)?)?\s*(?:%|％|万(?:元)?|亿(?:元)?|个月|月|年|家|人|个|项|条|倍)|\d+\s*/\s*\d+)`)
	fundraisingSuperlativeRe               = regexp.MustCompile(`(?i)(?:(?:中国|国内|行业|市场|赛道|全球|同类)[^。；;\n]{0,10})?(?:唯一(?:一家|一个|一款|一个)?|第一(?:批|家|个|名)?|首个|领先|最大|最早)[^。；;\n]{0,24}(?:团队|平台|产品|公司|厂商|方案|系统|能力|闭环)`)
	fundraisingMultiplierRe                = regexp.MustCompile(`(?i)(\d+(?:[.,]\d+)?)\s*[x×]`)
	fundraisingPositiveSupportRe           = regexp.MustCompile(`(?m)^\s*(?:[-*]\s*)?\*{0,2}(?:能支持|支持)\*{0,2}\s*[:：]`)
	fundraisingNegativeSupportRe           = regexp.MustCompile(`(?m)^\s*(?:[-*]\s*)?\*{0,2}(?:不能支持|不支持)\*{0,2}\s*[:：]`)
	fundraisingExplicitManyQuestionsRe     = regexp.MustCompile(`(?i)(?:[4-9]|四|五|六|七|八|九)\s*(?:个|项|条)?\s*(?:问题|questions?)`)
	fundraisingQuestionIndexRe             = regexp.MustCompile(`^(?:[1-9]\d*|[①②③④⑤⑥⑦⑧⑨⑩])$`)
	fundraisingReadinessScoreRe            = regexp.MustCompile(`(?i)(?:^|[^\d])(?:[0-5](?:\.\d+)?)\s*/\s*5(?:[^\d]|$)`)
	fundraisingQuestionListItemRe          = regexp.MustCompile(`^\s*(?:\d+[.)、]|[①②③④⑤⑥⑦⑧⑨⑩])[ \t]+`)
	fundraisingTaxonomyCodeRe              = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])d[1-9](?:[^a-z0-9]|$)`)
	fundraisingObjectCodeRe                = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])obj[\s_-]?[1-9](?:[^a-z0-9]|$)`)
	fundraisingInlineEnumerationRe         = regexp.MustCompile(`(?i)(?:[(（][a-z1-9][)）]|[①②③④⑤⑥⑦⑧⑨⑩])`)
	fundraisingGroupedRecordLabelRe        = regexp.MustCompile(`(?i)(?:条目|对象|描述|能力|产品|业务|方向|场景)\s*[*_` + "`" + `]*\s*[a-z1-9①②③④⑤⑥⑦⑧⑨⑩](?:\b|[、·：:（(])`)
	fundraisingGroupedRecordRelationRe     = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])([a-z1-9])\s*(?:与|和|及|/|、|and)\s*([a-z1-9])(?:[^a-z0-9]|$)`)
	fundraisingTaxonomyLabelRe             = regexp.MustCompile(`(?i)(?:业务线|产品线|应用线|业务方向|规划方向|新方向|方向)\s*[a-z1-9①②③④⑤⑥⑦⑧⑨⑩]|应用\s*[a-c①②③]`)
	fundraisingModuleLabelRe               = regexp.MustCompile(`(?i)(?:模块|能力)\s*[a-z1-9①②③④⑤⑥⑦⑧⑨⑩](?:\b|[·：:（(])`)
	fundraisingSupplementLabelRe           = regexp.MustCompile(`(?i)用户补充描述\s*[a-z1-9①②③④⑤⑥⑦⑧⑨⑩](?:\b|[、·：:（(]|\s)`)
	fundraisingCandidateCountRe            = regexp.MustCompile(`(?i)(?:[2-9]|两|三|四|五|六|七|八|九)\s*项候选(?:能力|产品|方向|业务)`)
	fundraisingNegativeArtifactDirectiveRe = regexp.MustCompile(`(?i)(?:不要|别|无需|不需要|不用|不必|禁止)(?:再)?(?:保存|下载|导出|生成|创建|写入)(?:为|成|到|至)?(?:任何)?\s*(?:文件|文档|markdown|md|pdf|word|docx?|pptx?)?|(?:do\s+not|don't|without)\s+(?:save|download|export|create|write)(?:\s+(?:a|an|any|the))?\s*(?:file|document|markdown|pdf|word|pptx?)?`)
	fundraisingBPBusinessQuantityRe        = regexp.MustCompile(`(?i)(?:[¥￥$]\s*\d+(?:[.,]\d+)?(?:\s*[–—~～\-至到]\s*[¥￥$]?\s*\d+(?:[.,]\d+)?)?(?:\s*(?:万|亿|k|m))?(?:\s*/\s*(?:月|年))?|\d+(?:[.,]\d+)?(?:\s*[–—~～\-至到]\s*\d+(?:[.,]\d+)?)?\s*(?:%|％|万(?:元)?|亿(?:元)?|元|个月|月|年|家|人|倍)|t\s*\+\s*\d+|y[1-9]|m[1-9])`)
	fundraisingNamedReferenceBeforeRe      = regexp.MustCompile(`(?i)\b([a-z][a-z0-9._-]{2,31}(?:\s+ai)?)\s*(?:仅)?(?:作为)?(?:的)?(?:参考|对标|竞品)`)
	fundraisingNamedReferenceAfterRe       = regexp.MustCompile(`(?i)(?:参考(?:产品|对象|资料)?|对标(?:产品|对象)?|竞品)\s*(?:是|为|[:：])?\s*([a-z][a-z0-9._-]{2,31}(?:\s+ai)?)\b`)
	fundraisingQuotedSpanRe                = regexp.MustCompile(`[“"]([^”"\r\n]{6,160})[”"]`)
	fundraisingTaskURLRe                   = regexp.MustCompile(`(?i)https?://[^\s，,。；;）)\]}]+`)
	fundraisingNumericLiteralRe            = regexp.MustCompile(`\d+(?:[.,]\d+)?`)
	marketAnalystExternalURLRe             = regexp.MustCompile(`(?i)https?://[^\s，,。；;）)\]}]+`)
)

func aiEmployeeDomainOutputIssues(pipeline *Pipeline, output string) []string {
	if pipeline == nil {
		return nil
	}
	switch strings.TrimSpace(pipeline.AIEmployeeID) {
	case "market-analyst":
		return marketAnalystOutputIssues(pipeline.AIEmployeeOriginalTask, output)
	case "fundraising-advisor":
		// Continue with the financing-specific semantic checks below.
	default:
		return nil
	}
	task := strings.ToLower(strings.TrimSpace(pipeline.AIEmployeeOriginalTask))
	if fundraisingExplicitOptionHandoff(task, output) {
		// The user explicitly placed a choice before later business work. The
		// current answer is an interaction menu, not a factual fundraising
		// analysis. Applying later-stage claim gates here can delete an option
		// merely because its user-authored label mentions sources, institutions,
		// metrics, or another future research dimension.
		return nil
	}

	var issues []string
	sectionContext := ""
	lines := strings.Split(strings.ReplaceAll(output, "\r\n", "\n"), "\n")
	for index, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "#") {
			sectionContext = line
		}
		if fundraisingLineRequestsUserInput(line) {
			// A question or fillable row may still smuggle in an asserted
			// commercial scenario, e.g. "[待补]（示例：融资 1500 万）".
			// Preserve genuine input requests, but not invented BP numbers.
			if fundraisingUnsupportedBPAssumptionQuantity(task, sectionContext, line) {
				issues = append(issues, truncate(line, 220))
			}
			// A relationship question is useful, but it must not presuppose the
			// very product matrix or numbered taxonomy that the user is being
			// asked to decide.
			if fundraisingUnsupportedInferredProductLineClaim(task, line) {
				issues = append(issues, truncate(line, 220))
			}
			continue
		}
		if markdownTableHeaderAt(lines, index) {
			continue
		}
		claimText := fundraisingMetricClaimContext(sectionContext, line)
		evidenceText := fundraisingEvidenceLabelContext(sectionContext, line)
		if fundraisingLineHasUnsupportedClaim(task, sectionContext, line, claimText, evidenceText) {
			issues = append(issues, truncate(line, 220))
		}
	}
	issues = append(issues, fundraisingRequestedCoverageIssues(pipeline, output)...)
	issues = append(issues, fundraisingNamedReferenceCoverageIssues(task, output)...)
	issues = append(issues, fundraisingQuotedCapabilityCoverageIssues(task, output)...)
	issues = append(issues, fundraisingTrailingDescriptionCoverageIssues(task, output)...)
	issues = append(issues, fundraisingRepeatedUnconfirmedProductTaxonomyIssues(task, output)...)
	issues = append(issues, fundraisingDecisiveQuestionIssues(task, output)...)
	return uniqueSortedStrings(issues)
}

func fundraisingLineHasUnsupportedClaim(task, sectionContext, line, claimText, evidenceText string) bool {
	return fundraisingUnsupportedBenchmarkClaim(claimText) ||
		fundraisingUnsupportedInstitutionBehaviorClaim(line) ||
		fundraisingUnsupportedMetricInference(task, claimText) ||
		fundraisingUnsupportedProvenanceClaim(task, line) ||
		fundraisingUnsupportedSourceAttributionClaim(task, sectionContext, line) ||
		fundraisingUnsupportedMissingFactClaim(task, line) ||
		fundraisingUnsupportedExternalQuantification(task, evidenceText) ||
		fundraisingUnsupportedPrescriptiveQuantity(task, evidenceText) ||
		fundraisingUnsupportedRoundPlacementClaim(task, evidenceText) ||
		fundraisingUnsupportedNonParticipatingLine(task, sectionContext, line) ||
		fundraisingUnsupportedUnrequestedReadinessScore(task, sectionContext, line) ||
		fundraisingUnsupportedBPAssumptionQuantity(task, sectionContext, line) ||
		fundraisingUnsupportedInferredProductLineClaim(task, line) ||
		fundraisingLineSplitsTrailingDescription(task, line) ||
		fundraisingUnsupportedNonParticipatingThresholdDirection(task, line) ||
		(!fundraisingTaskConfirmsProductTaxonomy(task) &&
			fundraisingLineProposesObjectRelationship(line)) ||
		fundraisingUnsupportedInternalExecutionLine(task, line) ||
		fundraisingUnsupportedSuperlativeClaim(line)
}

func marketAnalystOutputIssues(task, output string) []string {
	normalizedTask := strings.ToLower(strings.TrimSpace(task))
	normalizedOutput := strings.ToLower(strings.TrimSpace(output))
	if normalizedOutput == "" {
		return []string{"market research answer is empty"}
	}
	requestsTraceableSources := containsAnyText(normalizedTask, []string{
		"来源", "出处", "引用", "链接", "证据", "数据支撑", "报告",
		"source", "citation", "reference", "link", "evidence",
	})
	if requestsTraceableSources && len(marketAnalystExternalURLRe.FindAllString(output, -1)) == 0 {
		return []string{"market source coverage is missing: include concrete external source URLs in both the answer and saved report; a search-result snippet or local download link is not evidence"}
	}
	if containsAnyText(normalizedTask, []string{"截至", "截止", "as of", "through ", "data to"}) &&
		!containsAnyText(normalizedOutput, []string{"截至", "截止", "as of", "through ", "数据日期", "检索日期"}) {
		return []string{"market evidence date coverage is missing: state the source date or explicit as-of date for time-sensitive claims"}
	}
	return nil
}

func fundraisingTaskConfirmsProductTaxonomy(task string) bool {
	normalized := strings.ToLower(strings.TrimSpace(task))
	hasTaxonomy := containsAnyText(normalized, []string{
		"业务线", "产品线", "三条线", "三条产品", "三个产品",
		"两个方向", "两条方向", "两条业务方向", "两个业务方向",
		"模块 a", "模块a", "模块 b", "模块b", "两个模块", "三个模块",
		"产品主线", "业务主线", "作为主线", "产品副线", "业务副线", "作为副线",
		"产品矩阵", "业务矩阵",
		"business line", "product line", "three products",
	})
	if !hasTaxonomy {
		return false
	}
	return containsAnyText(normalized, []string{
		"已确认", "确认有", "就是", "我们有", "公司有", "我们拥有", "公司拥有",
		"明确分为", "产品分为", "业务分为", "分别是", "由以下", "按照以下",
		"confirmed", "we have", "consists of", "divided into",
	})
}

// fundraisingUnsupportedInferredProductLineClaim prevents a BP rewrite from
// silently turning adjacent products, references, capabilities, scenarios, and
// business goals into a product or business architecture. Until the user or
// evidence establishes those relationships, even a visibly tentative
// architecture tends to be propagated as fact by later BP sections. The safe
// representation is a neutral object map plus a clarification question.
func fundraisingUnsupportedInferredProductLineClaim(task, line string) bool {
	normalizedTask := strings.ToLower(strings.TrimSpace(task))
	normalizedLine := strings.ToLower(strings.TrimSpace(line))
	if normalizedLine == "" {
		return false
	}
	if fundraisingTaskConfirmsProductTaxonomy(normalizedTask) {
		return false
	}
	architectureAssertion := containsAnyText(normalizedLine, []string{
		"三条产品线", "三条业务线", "三条线共享", "三类交付物",
		"业务线 a", "业务线 b", "业务线 c",
		"产品线 a", "产品线 b", "产品线 c",
		"三类应用方向", "三类业务方向", "三个方向", "三种方向", "三方向",
		"四条产品方向", "四个产品方向", "四类产品方向", "方向矩阵",
		"三条曲线", "四个方向", "产品矩阵", "业务矩阵",
		"两条应用线", "第一曲线", "第二曲线", "独立产品矩阵",
		"单产品序列架构", "三家公司各做一条线", "候选架构", "备选架构",
		"待确认架构", "并列模块", "延伸模块", "两条业务方向", "两条业务流",
		"平台—模块—应用", "平台-模块-应用", "参考线", "候选线", "主线定位",
		"两条新方向", "两个新方向", "两条规划方向",
		"用户陈述产品线", "用户陈述主线",
		"three product lines", "three business lines",
		"three application directions", "three business directions",
		"product matrix", "business matrix", "second curve",
	}) || fundraisingLineHasProductMainSecondaryTaxonomy(normalizedLine) ||
		fundraisingLineHasTentativeArchitecture(normalizedLine) ||
		fundraisingLineHasInventedDirectionTaxonomy(normalizedLine) ||
		fundraisingTaxonomyCodeRe.MatchString(normalizedLine) ||
		fundraisingObjectCodeRe.MatchString(normalizedLine) ||
		fundraisingTaxonomyLabelRe.MatchString(normalizedLine) ||
		fundraisingModuleLabelRe.MatchString(normalizedLine) ||
		fundraisingSupplementLabelRe.MatchString(normalizedLine) ||
		(fundraisingCandidateCountRe.MatchString(normalizedLine) &&
			!fundraisingCandidateCountRe.MatchString(normalizedTask))
	return architectureAssertion
}

// A repeated taxonomy issue remains useful as a whole-answer diagnostic, even
// though each unsupported architecture assertion is also rejected line by
// line. It tells the repair pass that the ontology, not merely one sentence,
// must be rebuilt as a neutral object map.
func fundraisingRepeatedUnconfirmedProductTaxonomyIssues(task, output string) []string {
	if fundraisingTaskConfirmsProductTaxonomy(task) {
		return nil
	}
	count := 0
	for _, raw := range strings.Split(strings.ReplaceAll(output, "\r\n", "\n"), "\n") {
		line := strings.ToLower(strings.TrimSpace(raw))
		if line == "" || fundraisingLineRequestsUserInput(line) {
			continue
		}
		if containsAnyText(line, []string{
			"产品矩阵", "业务矩阵", "方向矩阵",
			"三条产品线", "三条业务线", "三类应用方向", "三类业务方向",
			"四条产品方向", "四个产品方向", "四类产品方向",
			"三条曲线", "四个方向", "两条应用线", "第一曲线", "第二曲线",
			"独立产品矩阵", "单产品序列架构", "三家公司各做一条线",
			"候选架构", "备选架构", "待确认架构",
			"并列模块", "延伸模块", "两条业务方向", "两条业务流",
			"平台—模块—应用", "平台-模块-应用", "参考线", "候选线", "主线定位",
			"两条新方向", "两个新方向", "两条规划方向",
			"业务线 a", "业务线 b", "业务线 c",
			"产品线 a", "产品线 b", "产品线 c",
			"product matrix", "business matrix", "second curve",
		}) || fundraisingLineHasProductMainSecondaryTaxonomy(line) ||
			fundraisingLineHasTentativeArchitecture(line) ||
			fundraisingLineHasInventedDirectionTaxonomy(line) ||
			fundraisingTaxonomyCodeRe.MatchString(line) ||
			fundraisingObjectCodeRe.MatchString(line) ||
			fundraisingTaxonomyLabelRe.MatchString(line) ||
			fundraisingModuleLabelRe.MatchString(line) ||
			fundraisingSupplementLabelRe.MatchString(line) ||
			fundraisingCandidateCountRe.MatchString(line) {
			count++
		}
	}
	if count <= 1 {
		return nil
	}
	return []string{fmt.Sprintf(
		"unconfirmed product taxonomy was propagated across %d lines; replace it with a neutral object map and one consolidated relationship question",
		count,
	)}
}

func fundraisingLineHasInventedDirectionTaxonomy(line string) bool {
	normalized := strings.ToLower(strings.TrimSpace(line))
	if !containsAnyText(normalized, []string{"方向", "direction"}) {
		return false
	}
	if fundraisingLineIsTransparentAssumption(normalized) ||
		containsAnyText(normalized, []string{
			"来自本轮输入", "来自用户输入", "用户本轮输入", "用户原文",
			"项目方陈述", "用户陈述的原词", "from user input", "user-provided term",
		}) {
		return false
	}
	return containsAnyText(normalized, []string{
		"产品", "业务", "能力", "模块", "规划", "候选", "用户陈述", "用户口头", "新方向",
		"product", "business", "capability", "module", "candidate", "planned",
	})
}

func fundraisingLineHasTentativeArchitecture(line string) bool {
	normalized := strings.ToLower(strings.TrimSpace(line))
	if !containsAnyText(normalized, []string{
		"候选", "备选", "待确认", "暂按", "延伸", "延展", "拟作为", "计划作为", "设想为",
		"candidate", "alternative", "pending confirmation",
	}) {
		return false
	}
	if containsAnyText(normalized, []string{
		"用户原文", "用户称为", "用户陈述为", "用户描述的", "用户口头描述", "用户提供的原词",
		"user called", "user described", "user-provided term",
	}) && fundraisingLineStatesUnknownObjectRelationship(normalized) &&
		!fundraisingTaxonomyLabelRe.MatchString(normalized) &&
		!fundraisingModuleLabelRe.MatchString(normalized) &&
		!fundraisingLineProposesObjectRelationship(normalized) {
		return false
	}
	return containsAnyText(normalized, []string{
		"架构", "产品", "业务", "能力", "场景", "结果", "平台", "底座", "模块", "应用",
		"方向", "主线", "副线", "曲线", "链路", "矩阵",
		"architecture", "product", "business", "platform", "module",
		"capability", "scenario", "outcome", "application", "direction",
		"main line", "secondary line", "matrix",
	})
}

func fundraisingLineStatesUnknownObjectRelationship(line string) bool {
	normalized := strings.ToLower(strings.TrimSpace(line))
	return containsAnyText(normalized, []string{
		"关系未知", "关系尚未确定", "关系未确定", "关系待确认", "关系未确认",
		"与产品关系未知", "与标的关系未知", "相互关系未知",
		"relationship unknown", "relationship is unknown", "relationship not established",
	})
}

// A menu of proposed product relationships is still an architecture decision,
// even when each option is labelled tentative. When the task does not confirm
// the ontology, the safe output is the original objects plus one relationship
// question, not a set of model-authored integration choices.
func fundraisingLineProposesObjectRelationship(line string) bool {
	normalized := strings.ToLower(strings.TrimSpace(line))
	if normalized == "" || fundraisingLineRequestsUserInput(normalized) {
		return false
	}
	if containsAnyText(normalized, []string{
		"独立孵化", "独立产品线", "独立业务线", "并入主产品", "纳入主产品",
		"作为主产品的", "作为产品模块", "作为业务模块", "作为扩展能力",
		"集成为产品", "整合为产品", "进入下一轮融资材料",
		"spin off", "standalone product line", "integrate into the main product",
	}) {
		return true
	}
	proposalCue := containsAnyText(normalized, []string{
		"方案", "选项", "建议", "可考虑", "可以选择", "纳入", "并入", "作为",
		"option", "proposal", "recommend", "could be",
	})
	relationshipCue := containsAnyText(normalized, []string{
		"主产品", "产品线", "业务线", "产品模块", "业务模块", "扩展能力",
		"产品组成", "业务组成", "平台能力", "独立产品", "独立业务",
		"main product", "product line", "business line", "product module",
	})
	return proposalCue && relationshipCue
}

func fundraisingLineHasProductMainSecondaryTaxonomy(line string) bool {
	normalized := strings.ToLower(strings.TrimSpace(line))
	hasMainSecondary := containsAnyText(normalized, []string{
		"主线", "副线", "main line", "secondary line",
	})
	if !hasMainSecondary {
		return false
	}
	if containsAnyText(normalized, []string{
		"候选", "备选", "架构", "矩阵", "底座", "曲线",
		"candidate", "alternative", "architecture", "matrix",
	}) {
		return true
	}
	if containsAnyText(normalized, []string{
		"资金用途主线", "融资主线", "叙事主线", "论证主线",
		"use-of-funds", "fundraising thesis", "narrative",
	}) {
		return false
	}
	return containsAnyText(normalized, []string{
		"产品", "业务", "平台", "模块", "应用", "软件", "服务", "工具", "参考",
		"agent", "saas", "product", "business", "platform", "module",
		"application", "software", "service", "tool", "reference",
	})
}

// A grouped user description must remain one semantic record. Lettered or
// numbered inline decomposition adds a second, model-authored ontology even
// when the original sentence is also quoted elsewhere in the answer.
func fundraisingLineSplitsTrailingDescription(task, line string) bool {
	if fundraisingTrailingUserDescription(task) == "" &&
		!fundraisingTaskHasGroupedQuotedCapability(task) {
		return false
	}
	normalized := strings.ToLower(strings.TrimSpace(line))
	enumeratedSplit := len(fundraisingInlineEnumerationRe.FindAllString(line, -1)) >= 2
	letteredRecordSplit := fundraisingGroupedRecordLabelRe.MatchString(line) ||
		fundraisingGroupedRecordRelationRe.MatchString(line)
	categoricalSplit := containsAnyText(normalized, []string{"分别", "拆分", "分拆"}) &&
		containsAnyText(normalized, []string{
			"两类", "三类", "四类", "两种", "三种", "四种", "多个",
			"two categories", "three categories", "separately",
		})
	if !enumeratedSplit && !letteredRecordSplit && !categoricalSplit {
		return false
	}
	return containsAnyText(normalized, []string{
		"产品", "能力", "功能", "场景", "获客", "生成", "挖掘", "客户", "关系",
		"agent", "saas", " ai", "ai ",
	})
}

func fundraisingTaskHasGroupedQuotedCapability(task string) bool {
	for _, match := range fundraisingQuotedSpanRe.FindAllStringSubmatch(task, -1) {
		if len(match) < 2 {
			continue
		}
		span := strings.ToLower(strings.TrimSpace(match[1]))
		if containsAnyText(span, []string{
			"产品", "能力", "功能", "场景", "获客", "生成", "挖掘",
			"agent", "saas", " ai", "ai ",
		}) {
			return true
		}
	}
	return false
}

func fundraisingTaskRequestsReadinessScore(task string) bool {
	return containsAnyText(strings.ToLower(strings.TrimSpace(task)), []string{
		"融资准备度", "准备度评分", "正式评分", "八维评分", "打分", "评分",
		"readiness score", "readiness assessment", "scorecard", "score ",
	})
}

func fundraisingTaskAllowsHypotheticalBPNumbers(task string) bool {
	return containsAnyText(strings.ToLower(strings.TrimSpace(task)), []string{
		"假设数字", "假设数据", "示例数字", "数字示例", "给出数字",
		"给出区间", "合理假设", "情景测算", "情景分析", "财务预测",
		"financial projection", "numeric scenario", "scenario analysis",
	})
}

func fundraisingTaskRequestsArithmeticCalculation(task string) bool {
	normalized := strings.ToLower(strings.TrimSpace(task))
	return containsAnyText(normalized, []string{
		"计算", "算出", "算一下", "测算", "推导", "写明公式", "列出公式",
		"calculate", "calculation", "compute", "work out", "show the formula",
	})
}

// A user-requested calculation may legitimately introduce a result that was
// not literally present in the prompt. Preserve it only when the same line is
// auditable: it contains an arithmetic equation and reuses at least one numeric
// input from the task. Benchmark and transaction-definition checks still run,
// so this does not authorize unrelated industry numbers or an invalid payout
// formula.
func fundraisingLineIsTraceableArithmeticCalculation(task, line string) bool {
	if !fundraisingTaskRequestsArithmeticCalculation(task) {
		return false
	}
	normalizedLine := strings.ToLower(strings.TrimSpace(line))
	if !strings.ContainsAny(normalizedLine, "=＝") ||
		!containsAnyText(normalizedLine, []string{"+", "-", "−", "×", "*", "÷", "/"}) {
		return false
	}
	taskNumbers := make(map[string]struct{})
	for _, raw := range fundraisingNumericLiteralRe.FindAllString(task, -1) {
		number := strings.TrimLeft(strings.ReplaceAll(raw, ",", ""), "0")
		if number == "" {
			number = "0"
		}
		taskNumbers[number] = struct{}{}
	}
	for _, raw := range fundraisingNumericLiteralRe.FindAllString(normalizedLine, -1) {
		number := strings.TrimLeft(strings.ReplaceAll(raw, ",", ""), "0")
		if number == "" {
			number = "0"
		}
		if _, ok := taskNumbers[number]; ok {
			return true
		}
	}
	return false
}

// A BP draft with unknown commercial inputs should contain fillable fields,
// not invented prices, budgets, customer targets, financing ranges or
// forecasts. Labels such as "analysis assumption" do not change that rule;
// numeric scenarios remain available when the user explicitly asks for them.
func fundraisingUnsupportedBPAssumptionQuantity(task, sectionContext, line string) bool {
	if !fundraisingBPOnlyRequest(task) ||
		fundraisingTaskAllowsHypotheticalBPNumbers(task) ||
		fundraisingLineHasCitation(line) {
		return false
	}
	context := strings.ToLower(strings.TrimSpace(sectionContext + " " + line))
	if fundraisingLineIsTransparentAssumption(context) &&
		!fundraisingLineRequestsUserInput(line) {
		return false
	}
	if !containsAnyText(context, []string{
		"定价", "价格", "客单价", "融资金额", "估值", "资金用途", "预算",
		"研发", "市场与销售", "团队扩充", "运营储备", "里程碑", "客户",
		"用户", "arr", "收入", "毛利", "费用", "亏损", "runway", "稀释",
		"mvp", "beta", "poc", "ga", "轮次", "时间表", "预测",
		"pricing", "funding", "valuation", "budget", "milestone",
		"customer", "revenue", "margin", "expense", "loss", "forecast",
	}) {
		return false
	}
	normalizedTask := normalizeFundraisingQuantityText(task)
	for _, quantity := range fundraisingBPBusinessQuantityRe.FindAllString(line, -1) {
		normalizedQuantity := normalizeFundraisingQuantityText(quantity)
		if normalizedQuantity != "" && !strings.Contains(normalizedTask, normalizedQuantity) {
			return true
		}
	}
	return false
}

// A BP creation or review request does not implicitly authorize a readiness
// scorecard. Missing evidence must remain unknown rather than being converted
// into a low numeric score that looks like a factual assessment.
func fundraisingUnsupportedUnrequestedReadinessScore(task, sectionContext, line string) bool {
	if fundraisingTaskRequestsReadinessScore(task) {
		return false
	}
	normalizedLine := strings.ToLower(strings.TrimSpace(line))
	normalizedSection := strings.ToLower(strings.TrimSpace(sectionContext))
	if fundraisingReadinessScoreRe.MatchString(normalizedLine) {
		return true
	}
	return containsAnyText(normalizedLine, []string{
		"融资准备度诊断", "八维评分", "准备度评分", "readiness scorecard",
	}) || containsAnyText(normalizedSection, []string{
		"融资准备度诊断", "八维评分", "准备度评分", "readiness scorecard",
	})
}

func fundraisingDecisiveQuestionIssues(task, output string) []string {
	if fundraisingExplicitManyQuestionsRe.MatchString(strings.ToLower(strings.TrimSpace(task))) {
		return nil
	}
	directDraft := fundraisingBPOnlyRequest(task) &&
		!fundraisingTaskRequestsClarification(task)
	lines := strings.Split(strings.ReplaceAll(output, "\r\n", "\n"), "\n")
	inSection := false
	count := 0
	outsideCount := 0
	directQuestionCount := 0
	questionSectionSeen := false
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if heading, boundary := fundraisingQuestionSectionBoundary(line); boundary {
			inSection = fundraisingQuestionSectionHeading(heading)
			if inSection {
				questionSectionSeen = true
			}
			continue
		}
		if line == "---" || line == "***" || line == "___" {
			inSection = false
			continue
		}
		if line == "" {
			continue
		}
		if directDraft && fundraisingLineRequestsUserInput(line) {
			directQuestionCount++
		}
		if !inSection {
			isListItem := fundraisingQuestionListItemRe.MatchString(raw) ||
				strings.HasPrefix(line, "- ") ||
				strings.HasPrefix(line, "* ") ||
				strings.HasPrefix(line, "+ ")
			if fundraisingBPOnlyRequest(task) && isListItem &&
				fundraisingLineRequestsUserInput(line) {
				outsideCount++
			}
			continue
		}
		if fundraisingQuestionListItemRe.MatchString(raw) {
			count++
			continue
		}
		if strings.HasPrefix(line, "|") {
			cells := strings.Split(strings.Trim(line, "| "), "|")
			if len(cells) < 2 {
				continue
			}
			first := strings.Trim(strings.TrimSpace(cells[0]), "*_ ")
			if fundraisingQuestionIndexRe.MatchString(first) {
				count++
			}
		}
	}
	var issues []string
	if directDraft && directQuestionCount > 0 {
		issues = append(issues, fmt.Sprintf(
			"direct BP draft asks %d confirmation or clarification question(s); deliver the complete BP now and express missing facts only as non-blocking [待补] fields or declarative unknowns",
			directQuestionCount,
		))
		return issues
	}
	if count > 3 {
		issues = append(issues, fmt.Sprintf(
			"decisive question section contains %d items; merge related gaps and keep at most 3 questions",
			count,
		))
	}
	if questionSectionSeen && outsideCount > 0 {
		issues = append(issues, fmt.Sprintf(
			"%d clarification question(s) appear before the consolidated final question section; move decision-changing questions to the final section and remove duplicates",
			outsideCount,
		))
	}
	return issues
}

func fundraisingQuestionSectionBoundary(line string) (string, bool) {
	trimmed := strings.TrimSpace(line)
	if strings.HasPrefix(trimmed, "#") {
		return strings.ToLower(strings.Trim(strings.TrimLeft(trimmed, "#"), " \t*_：:")), true
	}
	if strings.HasPrefix(trimmed, "**") {
		if end := strings.LastIndex(trimmed[2:], "**"); end >= 0 {
			label := trimmed[2 : 2+end]
			return strings.ToLower(strings.Trim(label, " \t*_：:")), true
		}
	}
	return "", false
}

func fundraisingQuestionSectionHeading(heading string) bool {
	normalized := strings.ToLower(strings.TrimSpace(heading))
	if containsAnyText(normalized, []string{"会改变", "影响 bp", "影响bp"}) &&
		containsAnyText(normalized, []string{"问题", "待确认", "补充"}) {
		return true
	}
	return containsAnyText(normalized, []string{
		"决定性问题", "决定性缺口", "决定 bp 结论", "决定bp结论", "最少补全问题",
		"需要补全的关键信息", "需要补充", "关键待补问题", "待补问题",
		"需要用户补充", "用户补充的关键问题", "补充问题", "未决问题",
		"关键信息清单", "decisive questions",
		"critical questions", "key missing information", "questions to confirm",
	})
}

func fundraisingUnsupportedInternalExecutionLine(task, line string) bool {
	normalizedTask := strings.ToLower(strings.TrimSpace(task))
	normalizedLine := strings.ToLower(strings.TrimSpace(line))
	if normalizedLine == "" {
		return false
	}
	for _, marker := range []string{
		".openkitty/", "openkitty/storage/", "openkitty\\storage\\",
		"pipe_direct_", "runtime/builtin/", "runtime\\builtin\\",
		"上游事实底稿", "上游研究底稿", "上游步骤产出",
		"references/fundraising-side.md", "references/term-sheet-review.md",
		"references/investment-side.md", "funding_diagnosis.py",
		"huanzhi-fa-fundraising", "required_skills", "use_skill",
	} {
		if strings.Contains(normalizedLine, marker) && !strings.Contains(normalizedTask, marker) {
			return true
		}
	}
	return false
}

// fundraisingUnsupportedNonParticipatingLine enforces the defining semantic
// invariant of a non-participating liquidation preference. The preferred
// holder may take the preference amount or convert and share pro rata, but may
// not stack those two payouts. This is intentionally expressed as a relation
// between concepts and formula shapes rather than as a check for any specific
// example amount, percentage, company, or wording.
func fundraisingUnsupportedNonParticipatingLine(task, sectionContext, line string) bool {
	normalizedTask := strings.ToLower(strings.TrimSpace(task))
	if !containsAnyText(normalizedTask, []string{
		"非参与型", "不参与型", "non-participating", "non participating", "nonparticipating",
	}) {
		return false
	}

	normalizedLine := strings.ToLower(strings.TrimSpace(line))
	if normalizedLine == "" {
		return false
	}
	context := strings.ToLower(strings.TrimSpace(sectionContext))

	stackTerms := []string{
		"两者兼得", "二者兼得", "叠加", "先拿", "先取", "先收", "优先拿", "优先取",
		"再按", "再参与", "继续参与", "参与剩余", "分剩余",
		"lp+", "优先金额+", "优先额+", "清算优先金额+", "清算优先额+",
		"then share", "share the remainder", "share remaining proceeds",
		"participate in the remainder", "preference amount+",
	}
	if fundraisingRejectsNonParticipatingStack(normalizedLine, stackTerms) {
		return false
	}

	compact := strings.NewReplacer(
		" ", "",
		"\t", "",
		"`", "",
		"*", "",
		",", "",
		"，", "",
	).Replace(normalizedLine)
	hasRemainder := containsAnyText(compact, []string{
		"剩余", "余额", "exit-lp", "exit−lp", "exit–lp", "exit—lp",
		"remainder", "remainingproceeds", "residual",
	})
	hasProRataShare := containsAnyText(compact, []string{
		"再按", "继续按", "按持股比例", "按股权比例", "按比例分", "再参与", "继续参与",
		"按转换后持股比例", "持股比例", "股权比例", "参与剩余", "分剩余",
		"×p", "*p", "xp", "prorata", "pro-rata",
		"sharetheremainder", "shareremainingproceeds", "participateintheremainder",
	})
	hasPreferenceAction := containsAnyText(compact, []string{
		"行使优先", "选择优先", "拿回优先", "先拿", "先取", "先收", "优先拿", "优先取",
		"lp+", "优先金额+", "优先额+", "清算优先金额+", "清算优先额+",
		"takethepreference", "takesthepreference", "exercisethepreference",
		"exercisesthepreference", "preferenceamount+",
	})

	// Direct prose such as "take the preference, then share the remainder".
	if hasPreferenceAction && hasRemainder && hasProRataShare {
		return true
	}

	// Algebraic forms of the same participating-preferred calculation.
	if containsAnyText(compact, []string{
		"lp+(exit-lp)", "lp+(exit−lp)", "lp+(exit–lp)", "lp+(exit—lp)",
		"优先金额+(退出金额-优先金额)", "优先额+(退出金额-优先额)",
		"preferenceamount+(exit-preferenceamount)",
		"preferenceamount+(proceeds-preferenceamount)",
	}) {
		return true
	}

	// A table row can encode the invalid stack without a prose verb.
	if containsAnyText(compact, []string{
		"行使1x优先", "行使1×优先", "行使优先权", "选择优先权",
		"exercisethepreference", "exercisesthepreference",
		"takethepreference", "takesthepreference",
	}) &&
		strings.Contains(compact, "+") &&
		containsAnyText(compact, []string{"%", "％", "×", "*", "比例", "剩余"}) {
		return true
	}

	// Under an explicitly labelled preference-exercise branch, reducing the
	// common holders' remainder by their ordinary ownership percentage is the
	// same hidden double participation. In the preference branch, the entire
	// post-preference remainder belongs to the common side.
	preferenceBranch := containsAnyText(context, []string{
		"行使优先", "选择优先", "优先清算权", "优先清算金额", "优先金额路径",
		"拿回1x优先", "拿回 1x 优先", "拿回优先款", "优先款（不转换）", "优先款(不转换)",
		"take the preference",
		"exercise the preference", "preference option",
	}) || (containsAnyText(context, []string{"优先金额", "优先款", "preference amount"}) &&
		containsAnyText(context, []string{"路径", "选项", "选择", "path", "option", "choose"}))
	investorPayoutLine := containsAnyText(compact, []string{
		"投资人到手", "投资人所得", "投资人获得", "投资人分得", "投资人合计",
		"investorpayout", "investorproceeds", "investorreceives",
	})
	// A pure-number table formula can hide the same double dip without saying
	// "share the remainder", for example "investor payout = 800 + 300".
	// In a preference-exercise branch, any additive investor payout formula is
	// inconsistent with non-participating preferred.
	if preferenceBranch && investorPayoutLine && strings.Contains(compact, "+") {
		return true
	}
	investorActor := containsAnyText(compact, []string{
		"投资人", "优先股东", "preferredholder", "preferredinvestor", "investor",
	})
	if preferenceBranch && investorActor && hasRemainder {
		return true
	}
	if preferenceBranch && hasRemainder && hasProRataShare {
		return true
	}
	if preferenceBranch && investorActor &&
		len(fundraisingBPBusinessQuantityRe.FindAllString(normalizedLine, -1)) >= 3 {
		return true
	}
	commonHolderLine := containsAnyText(compact, []string{
		"创始人", "创始股东", "普通股东", "普通股", "common holder", "common shareholder",
	})
	if preferenceBranch && commonHolderLine && hasRemainder &&
		containsAnyText(compact, []string{"%", "％", "×", "*", "持股比例", "股权比例"}) {
		return true
	}

	return false
}

// The conversion payout is ownership percentage multiplied by total exit
// proceeds, so it increases with exit value. The preference amount is fixed.
// Therefore conversion can overtake only above (not below) the crossover
// preference/pct, and the preference branch can dominate only below it.
func fundraisingUnsupportedNonParticipatingThresholdDirection(task, line string) bool {
	normalizedTask := strings.ToLower(strings.TrimSpace(task))
	if !containsAnyText(normalizedTask, []string{
		"非参与型", "不参与型", "non-participating", "non participating", "nonparticipating",
	}) {
		return false
	}
	normalized := strings.ToLower(strings.TrimSpace(line))
	if normalized == "" ||
		!containsAnyText(normalized, []string{
			"临界点", "临界值", "盈亏平衡", "分界点", "threshold", "crossover", "break-even",
		}) ||
		containsAnyText(normalized, []string{
			"错误示例", "错误说法", "不正确", "incorrect", "wrong",
		}) {
		return false
	}
	for _, clause := range strings.FieldsFunc(normalized, func(r rune) bool {
		return r == '；' || r == ';' || r == '。' || r == '\n'
	}) {
		conversionWins := containsAnyText(clause, []string{
			"转换路径反超", "转换后反超", "转换普通股反超", "转换更高", "转换更优",
			"路径 b 反超", "路径b反超", "路径 b 才会反超", "路径b才会反超",
			"conversion overtakes", "conversion is higher", "conversion wins",
		})
		preferenceWins := containsAnyText(clause, []string{
			"优先路径反超", "优先金额反超", "优先金额占优", "优先路径占优",
			"路径 a 占优", "路径a占优", "preference wins", "preference is higher",
		})
		below := containsAnyText(clause, []string{
			"低于", "小于", "不超过", "以下", "below", "less than", "under the",
		})
		above := containsAnyText(clause, []string{
			"高于", "大于", "超过", "以上", "above", "greater than", "over the",
		})
		if (conversionWins && below) || (preferenceWins && above) {
			return true
		}
	}
	return false
}

func fundraisingRejectsNonParticipatingStack(line string, stackTerms []string) bool {
	if !containsAnyText(line, stackTerms) {
		return false
	}
	if containsAnyText(line, []string{
		"错误示例", "错误公式", "错误算法", "不正确", "并非", "不是", "区别于",
		"incorrect example", "incorrect formula", "wrong example", "wrong formula",
	}) {
		return true
	}
	if containsAnyText(line, []string{
		"不能", "不得", "不可", "不应", "禁止", "不再",
		"cannot", "must not", "does not", "do not",
	}) &&
		containsAnyText(line, []string{
			"非参与型", "不参与型", "non-participating", "non participating", "nonparticipating",
			"两者兼得", "二者兼得", "叠加", "先拿", "先取", "再按", "再参与", "剩余",
		}) {
		return true
	}
	// A correct contrast may explain the stacked payout as the definition of
	// participating preferred while rejecting it for non-participating.
	return containsAnyText(line, []string{"参与型允许", "参与型可以", "participating allows"}) &&
		containsAnyText(line, []string{
			"非参与型不能", "非参与型不得", "不参与型不能", "不参与型不得",
			"non-participating cannot", "non-participating does not",
		})
}

func fundraisingExplicitOptionHandoff(task, output string) bool {
	return aiEmployeeInteractiveIntakeSignal.MatchString(strings.TrimSpace(task)) &&
		len(extractAIEmployeeExplicitCircledOptions(task)) >= 2 &&
		aiEmployeeInteractiveResponseProvidesChoice(output)
}

func fundraisingUnsupportedProvenanceClaim(task, line string) bool {
	normalizedTask := strings.ToLower(strings.TrimSpace(task))
	normalizedLine := strings.ToLower(strings.TrimSpace(line))
	if normalizedLine == "" {
		return false
	}
	for _, claim := range []string{"用户已核验", "用户已验证", "用户确认已核验", "用户确认已验证"} {
		if strings.Contains(normalizedLine, claim) && !strings.Contains(normalizedTask, claim) {
			return true
		}
	}
	if containsAnyText(normalizedLine, []string{"已核验事实", "已验证事实", "已经核验", "已经验证"}) &&
		containsAnyText(normalizedLine, []string{"用户", "你提供", "您提供", "自报", "输入"}) &&
		!fundraisingLineHasCitation(normalizedLine) &&
		!containsAnyText(normalizedTask, []string{"已核验事实", "已验证事实", "已经核验", "已经验证"}) {
		return true
	}
	return false
}

// fundraisingUnsupportedSourceAttributionClaim rejects positive claims that
// vaguely attribute content to an article, page, link, or source material
// without identifying the source on the same claim line. This is deliberately
// provenance-shaped rather than topic-shaped: it applies equally to product
// features, market facts, customer evidence, and financial claims.
//
// Explicit user/self-reported provenance and precise attachment locations are
// allowed because they do not pretend to be independent public verification.
// Negative audits such as "the article did not provide a source" are allowed.
func fundraisingUnsupportedSourceAttributionClaim(task, sectionContext, line string) bool {
	normalized := strings.ToLower(strings.TrimSpace(line))
	if normalized == "" || fundraisingLineHasCitation(normalized) {
		return false
	}
	negativeAudit := containsAnyText(normalized, []string{
		"未提供来源", "没有提供来源", "未给出来源", "没有给出来源",
		"未提供具体来源", "没有具体来源", "来源待核验", "来源待补",
		"没有公开来源", "无公开来源", "未发现公开来源", "未检出公开来源",
		"原文未提", "原文没有", "文中未提", "文中没有",
		"文章未提", "文章没有", "资料未提", "资料没有",
		"不能归因", "不可归因", "不应归因", "并非来自",
	})
	if negativeAudit {
		return false
	}
	vaguePublicProvenance := containsAnyText(normalized, []string{
		"公开资料", "公开信息", "公开材料", "公开常识", "行业一般做法",
		"行业惯例", "平台性质", "public sources", "public information",
		"common knowledge", "industry practice",
	})
	taskRequestsTraceableEvidence := containsAnyText(strings.ToLower(strings.TrimSpace(task)), []string{
		"来源", "出处", "可信度", "核验", "验证", "关键事实", "事实摘录", "证据",
		"source", "provenance", "credibility", "verification", "evidence",
	})
	structuredEvidenceRow := strings.Count(normalized, "|") >= 3
	inEvidenceMatrix := fundraisingSectionIsEvidenceMatrix(sectionContext)
	claimsVerification := containsAnyText(normalized, []string{
		"已核验事实", "已验证事实", "已经核验", "已经验证", "核验通过",
		"verified fact", "verified", "validated",
	})
	directNonPublicProvenance := containsAnyText(normalized, []string{
		"用户提供", "用户陈述", "用户自述", "用户口述", "你提供", "您提供",
		"据你描述", "据您描述", "本次对话", "本轮输入", "创始人陈述", "项目方自述",
		"系统时钟", "system clock",
		"附件第", "文件第", "第1页", "第2页", "第3页", "第4页", "第5页",
		"第6页", "第7页", "第8页", "第9页", "第10页", "第11页", "第12页",
		"第13页", "第14页", "第15页", "第16页", "第17页", "第18页",
		"第19页", "第20页",
	})
	transparentAnalysisProvenance := containsAnyText(normalized, []string{
		"分析推断", "本分析推断", "分析假设", "待核验假设", "待验证假设",
		"基于现有信息的推断", "inference from available information",
	})
	transparentUnknownProvenance := containsAnyText(normalized, []string{
		"| 未提供 |", "|未提供|", "未知", "待补", "not provided", "unknown",
	})
	if taskRequestsTraceableEvidence &&
		inEvidenceMatrix &&
		fundraisingMarkdownEvidenceDataRow(normalized) &&
		!directNonPublicProvenance &&
		!(transparentAnalysisProvenance && !claimsVerification) &&
		!(transparentUnknownProvenance && !claimsVerification) {
		return true
	}
	if vaguePublicProvenance &&
		(claimsVerification || (taskRequestsTraceableEvidence && inEvidenceMatrix && structuredEvidenceRow)) {
		return true
	}
	if directNonPublicProvenance {
		return false
	}
	if !containsAnyText(normalized, []string{
		"原文", "文中", "文章", "网页", "链接", "资料", "公开材料", "公开信息",
	}) {
		return false
	}
	claimIndex := firstFundraisingClaimIndex(normalized, []string{
		"提到", "指出", "显示", "说明", "描述", "声称", "写明", "明确", "表明", "证明", "可见",
	})
	return claimIndex >= 0 && !fundraisingClaimIsRejectedOrNegated(normalized, claimIndex)
}

func fundraisingSectionIsEvidenceMatrix(sectionContext string) bool {
	section := strings.ToLower(strings.TrimSpace(sectionContext))
	if containsAnyText(section, []string{
		"证据表", "证据矩阵", "关键事实", "事实核验", "来源与可信度",
		"来源、可信度", "来源 · 可信度", "source matrix", "evidence matrix",
		"facts and sources", "provenance",
	}) {
		return true
	}

	// Generated headings vary freely ("证据可信度速查表", "来源核验清单",
	// and so on). Detect the structural meaning instead of enumerating titles.
	return (containsAnyText(section, []string{"证据", "来源", "出处", "evidence", "source"}) &&
		containsAnyText(section, []string{"可信度", "核验", "验证", "溯源", "credibility", "verification", "provenance"}))
}

func fundraisingMarkdownEvidenceDataRow(line string) bool {
	if strings.Count(line, "|") < 3 {
		return false
	}
	cells := make([]string, 0, 6)
	for _, raw := range strings.Split(strings.Trim(line, "| "), "|") {
		cell := strings.TrimSpace(raw)
		if cell != "" {
			cells = append(cells, cell)
		}
	}
	if len(cells) < 3 {
		return false
	}
	separator := true
	for _, cell := range cells {
		if strings.Trim(cell, "-: ") != "" {
			separator = false
			break
		}
	}
	if separator {
		return false
	}
	hasFactHeader := false
	hasSourceHeader := false
	headerSignals := 0
	for _, cell := range cells {
		switch strings.ToLower(strings.TrimSpace(cell)) {
		case "#", "序号":
			continue
		case "事实", "事实/陈述", "关键事实", "判断", "结论", "fact", "claim", "judgment":
			hasFactHeader = true
			headerSignals++
		case "来源", "出处", "source", "provenance":
			hasSourceHeader = true
			headerSignals++
		case "状态", "核验状态", "可信度", "局限", "限制", "status", "credibility", "limitation":
			headerSignals++
		}
	}
	return !(hasSourceHeader && (hasFactHeader || headerSignals >= 2))
}

func markdownTableHeaderAt(lines []string, index int) bool {
	if index < 0 || index >= len(lines) || strings.Count(lines[index], "|") < 2 {
		return false
	}
	next := index + 1
	for next < len(lines) && strings.TrimSpace(lines[next]) == "" {
		next++
	}
	if next >= len(lines) {
		return false
	}
	separator := strings.TrimSpace(lines[next])
	if strings.Count(separator, "|") < 2 {
		return false
	}
	cells := strings.Split(strings.Trim(separator, "| "), "|")
	if len(cells) < 2 {
		return false
	}
	for _, cell := range cells {
		if strings.Trim(strings.TrimSpace(cell), "-:") != "" ||
			!strings.Contains(cell, "-") {
			return false
		}
	}
	return true
}

func fundraisingRequestedCoverageIssues(pipeline *Pipeline, output string) []string {
	if pipeline == nil {
		return nil
	}
	task := strings.ToLower(strings.TrimSpace(pipeline.AIEmployeeOriginalTask))
	if !containsAnyText(task, []string{"能支持什么", "能支持", "可以支持", "可支持"}) ||
		!containsAnyText(task, []string{"不能支持什么", "不能支持", "不支持"}) ||
		len([]rune(output)) < 600 {
		return nil
	}
	var issues []string
	if !fundraisingPositiveSupportRe.MatchString(output) {
		issues = append(issues, "requested evidence coverage is missing: provide neutral definition/arithmetic under “能支持” instead of deleting the requested positive side")
	}
	if !fundraisingNegativeSupportRe.MatchString(output) {
		issues = append(issues, "requested evidence coverage is missing: provide at least one bounded limitation under “不能支持”")
	}
	return issues
}

// fundraisingNamedReferenceCoverageIssues keeps named reference or comparison
// objects in a BP even when they are not part of the user's own product. This
// is deliberately role-preserving: it requires the named object to remain
// visible, while the architecture guard prevents it from becoming a product
// line merely to satisfy coverage.
func fundraisingNamedReferenceCoverageIssues(task, output string) []string {
	if !fundraisingBPOnlyRequest(task) {
		return nil
	}
	var names []string
	for _, re := range []*regexp.Regexp{
		fundraisingNamedReferenceBeforeRe,
		fundraisingNamedReferenceAfterRe,
	} {
		for _, match := range re.FindAllStringSubmatch(task, -1) {
			if len(match) < 2 {
				continue
			}
			fields := strings.Fields(strings.ToLower(strings.TrimSpace(match[1])))
			if len(fields) == 0 {
				continue
			}
			name := strings.Trim(fields[0], "._-")
			if len(name) < 3 || containsAnyText(name, []string{
				"http", "https", "www", "com", "csdn", "blog", "article",
				"product", "reference", "compare",
			}) {
				continue
			}
			names = append(names, name)
		}
	}
	names = uniqueSortedStrings(names)
	if len(names) == 0 {
		return nil
	}
	normalizedOutput := strings.ToLower(output)
	var missing []string
	for _, name := range names {
		if !strings.Contains(normalizedOutput, name) {
			missing = append(missing, name)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return []string{fmt.Sprintf(
		"named reference coverage is missing: preserve %s as reference/comparison objects in the neutral object map",
		strings.Join(missing, ", "),
	)}
}

// fundraisingTrailingDescriptionCoverageIssues protects an unquoted,
// unclassified capability or use-case description supplied after one or more
// source URLs. In BP requests this is a common way to add project context. The
// absence of quotation marks must not authorize the model to split the clause
// into numbered products or business directions.
func fundraisingTrailingDescriptionCoverageIssues(task, output string) []string {
	tail := fundraisingTrailingUserDescription(task)
	if tail == "" {
		return nil
	}
	normalizedTail := normalizeFundraisingCoveragePhrase(tail)
	normalizedOutput := normalizeFundraisingCoveragePhrase(output)
	if normalizedTail == "" || strings.Contains(normalizedOutput, normalizedTail) {
		return nil
	}
	return []string{fmt.Sprintf(
		"unclassified trailing description coverage is missing: preserve this user-provided expression as one continuous neutral record instead of splitting it into numbered products, capabilities, or directions: %s",
		tail,
	)}
}

func fundraisingTrailingUserDescription(task string) string {
	if !fundraisingBPOnlyRequest(task) || fundraisingTaskConfirmsProductTaxonomy(task) {
		return ""
	}
	matches := fundraisingTaskURLRe.FindAllStringIndex(task, -1)
	if len(matches) == 0 {
		return ""
	}
	tail := strings.TrimSpace(task[matches[len(matches)-1][1]:])
	tail = strings.TrimLeft(tail, " \t\r\n，,。；;：:")
	for _, marker := range []string{
		"请你帮我", "请帮我", "请为我", "帮我做", "帮我写",
		"请制作", "请生成", "请起草",
	} {
		if index := strings.Index(tail, marker); index >= 0 {
			tail = strings.TrimSpace(tail[:index])
			break
		}
	}
	tail = strings.Trim(tail, " \t\r\n，,。；;：:")
	for _, connector := range []string{"还有", "以及", "并且", "另外"} {
		if strings.HasPrefix(tail, connector) {
			tail = strings.TrimSpace(strings.TrimPrefix(tail, connector))
			break
		}
	}
	if len([]rune(tail)) < 12 || !containsAnyText(strings.ToLower(tail), []string{
		"产品", "能力", "功能", "场景", "获客", "生成", "挖掘", "客户",
		"agent", "saas", " ai", "ai ",
	}) {
		return ""
	}
	return tail
}

func ensureFundraisingTrailingDescriptionCoverage(task, output string) string {
	if len(fundraisingTrailingDescriptionCoverageIssues(task, output)) == 0 {
		return strings.TrimSpace(output)
	}
	span := fundraisingTrailingUserDescription(task)
	if span == "" {
		return strings.TrimSpace(output)
	}
	block := "## 用户补充描述（关系与状态未知）\n\n" +
		span + "\n\n" +
		"该描述与标的产品、参考对象之间的关系及当前实现状态均未知；在用户确认前不拆分为产品、模块、业务或编号方向。"
	lines := strings.Split(strings.ReplaceAll(output, "\r\n", "\n"), "\n")
	insertAt := len(lines)
	for index, raw := range lines {
		if heading, boundary := fundraisingQuestionSectionBoundary(strings.TrimSpace(raw)); boundary &&
			fundraisingQuestionSectionHeading(heading) {
			insertAt = index
			break
		}
	}
	combined := make([]string, 0, len(lines)+5)
	combined = append(combined, lines[:insertAt]...)
	if insertAt > 0 && strings.TrimSpace(lines[insertAt-1]) != "" {
		combined = append(combined, "")
	}
	combined = append(combined, block, "")
	combined = append(combined, lines[insertAt:]...)
	return strings.TrimSpace(strings.Join(combined, "\n"))
}

func ensureFundraisingRelationshipQuestionCoverage(task, output string) string {
	if !fundraisingBPOnlyRequest(task) ||
		!fundraisingTaskRequestsClarification(task) ||
		countAIEmployeeClarificationQuestions(output) > 0 {
		return strings.TrimSpace(output)
	}
	needsRelationshipQuestion := fundraisingTrailingUserDescription(task) != "" ||
		containsAnyText(strings.ToLower(task), []string{
			"关系未知", "关系尚未确定", "关系未确定", "关系不确定",
			"relationship unknown", "relationship is unknown", "relationship not established",
		})
	if !needsRelationshipQuestion {
		return strings.TrimSpace(output)
	}
	question := "1. 请确认用户补充描述与标的产品、参考对象分别是什么关系，以及当前实现状态；在确认前，本稿继续将这些信息保持为未知。"
	lines := strings.Split(strings.ReplaceAll(output, "\r\n", "\n"), "\n")
	for index, raw := range lines {
		if heading, boundary := fundraisingQuestionSectionBoundary(strings.TrimSpace(raw)); boundary &&
			fundraisingQuestionSectionHeading(heading) {
			insertAt := index + 1
			combined := make([]string, 0, len(lines)+2)
			combined = append(combined, lines[:insertAt]...)
			combined = append(combined, "", question)
			combined = append(combined, lines[insertAt:]...)
			return strings.TrimSpace(strings.Join(combined, "\n"))
		}
	}
	return strings.TrimSpace(output) +
		"\n\n## 决定性问题\n\n" +
		question
}

func stripFundraisingDirectDraftQuestions(task, output string) string {
	if !fundraisingBPOnlyRequest(task) ||
		fundraisingTaskRequestsClarification(task) {
		return strings.TrimSpace(output)
	}
	lines := strings.Split(strings.ReplaceAll(output, "\r\n", "\n"), "\n")
	kept := make([]string, 0, len(lines))
	skipLevel := 0
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if level, title, ok := markdownHeadingLevelAndTitle(line); ok {
			if skipLevel > 0 {
				if level > skipLevel {
					continue
				}
				skipLevel = 0
			}
			if fundraisingQuestionSectionHeading(title) {
				skipLevel = level
				continue
			}
		}
		if skipLevel == 0 {
			kept = append(kept, raw)
		}
	}
	return strings.TrimSpace(strings.Join(kept, "\n"))
}

func normalizeFundraisingCoveragePhrase(text string) string {
	replacer := strings.NewReplacer(
		" ", "", "\t", "", "\r", "", "\n", "",
		"“", "", "”", "", `"`, "", "'", "",
		"，", ",", "、", ",", "；", ";", "。", ".",
		"（", "(", "）", ")",
	)
	return strings.ToLower(replacer.Replace(strings.TrimSpace(text)))
}

// fundraisingQuotedCapabilityCoverageIssues preserves a substantial capability
// or product expression that the user deliberately grouped in quotation marks.
// Keeping the grouped phrase visible prevents a later draft from silently
// splitting adjacent nouns into independently named businesses.
func fundraisingQuotedCapabilityCoverageIssues(task, output string) []string {
	if !fundraisingBPOnlyRequest(task) {
		return nil
	}
	normalizedOutput := strings.ToLower(strings.Join(strings.Fields(output), " "))
	var missing []string
	for _, match := range fundraisingQuotedSpanRe.FindAllStringSubmatch(task, -1) {
		if len(match) < 2 {
			continue
		}
		span := strings.TrimSpace(match[1])
		normalizedSpan := strings.ToLower(strings.Join(strings.Fields(span), " "))
		if normalizedSpan == "" || !containsAnyText(normalizedSpan, []string{
			"产品", "能力", "功能", "场景", "获客", "生成", "挖掘",
			"agent", "saas", " ai", "ai ",
		}) {
			continue
		}
		if !strings.Contains(normalizedOutput, normalizedSpan) {
			missing = append(missing, span)
		}
	}
	missing = uniqueSortedStrings(missing)
	if len(missing) == 0 {
		return nil
	}
	return []string{fmt.Sprintf(
		"quoted capability coverage is missing: preserve the user's grouped expression verbatim instead of splitting it into independent objects: %s",
		strings.Join(missing, " | "),
	)}
}

func fundraisingUnsupportedBenchmarkClaim(line string) bool {
	normalized := strings.ToLower(strings.TrimSpace(line))
	if normalized == "" {
		return false
	}
	if fundraisingLineHasCitation(normalized) {
		return false
	}
	if fundraisingLineIsAnalystGoalRecommendation(normalized) {
		return false
	}
	if fundraisingLineIsProfessionalReviewBoundary(normalized) {
		return false
	}
	directClaims := []string{
		"最低门槛",
		"一票否决",
		"内部第一道关卡",
		"过会的核心",
		"过会核心",
		"普遍要求",
		"惯用评估",
		"惯用框架",
		"高度敏感",
		"常见否决信号",
		"通常会否决",
		"几乎都会否决",
		"业内公认",
		"市场公认",
		"阻断级",
		"deal killer",
		"估值打折",
		"条款收紧",
		"按下限给估值",
		"入场券",
		"缺一即停",
	}
	for _, phrase := range directClaims {
		if claimIndex := strings.Index(normalized, phrase); claimIndex >= 0 &&
			!fundraisingClaimIsRejectedOrNegated(normalized, claimIndex) {
			return true
		}
	}

	// A naked relative assessment is also a benchmark claim. Metric values can
	// be repeated as absolute facts, but “high/low/leading” needs a named sample,
	// date, and comparison method. A disclaimer after the assertion does not
	// retroactively make the assertion evidence-based.
	metricTerms := []string{
		"arr", "毛利率", "净收入留存", "nrr", "增长", "增长率", "增速", "续约率",
		"流失率", "cac", "回收期", "burn multiple", "客单价", "ltv",
	}
	relativeTerms := []string{
		"偏上", "偏高", "偏下", "偏低", "高于平均", "低于平均",
		"领先水平", "优秀水平", "落后水平", "较高水平", "较低水平",
		"较好水平", "良好水平", "高水平", "低水平", "早期体量", "成熟体量",
		"高速增长", "高速扩张", "快速增长", "快速扩张", "强劲增长", "强劲扩张",
		"增长亮眼", "表现亮眼", "增长健康", "表现健康", "增长稳健", "表现稳健",
		"通常意味着", "产品-市场早期验证", "产品市场早期验证", "pmf",
		"复制经济性", "交付成本控制良好", "软件而非项目", "规模化",
	}
	if containsAnyText(normalized, metricTerms) {
		if claimIndex := fundraisingRelativeBenchmarkRe.FindStringIndex(normalized); claimIndex != nil &&
			!fundraisingClaimIsRejectedOrNegated(normalized, claimIndex[0]) {
			return true
		}
		for _, phrase := range relativeTerms {
			if claimIndex := strings.Index(normalized, phrase); claimIndex >= 0 &&
				!fundraisingClaimIsRejectedOrNegated(normalized, claimIndex) {
				return true
			}
		}
	}
	placementPredicates := []string{"属于", "进入", "处于", "达到", "已达", "视为", "算作", "定位为"}
	placementObjects := []string{
		"区间", "阶段", "体量", "水平", "准备度", "成熟度", "验证期", "验证阶段",
		"商业化早期", "商业化成熟", "早期验证", "规模验证",
	}
	if containsAnyText(normalized, metricTerms) &&
		containsAnyText(normalized, placementPredicates) &&
		containsAnyText(normalized, placementObjects) {
		claimIndex := firstFundraisingClaimIndex(normalized, placementPredicates)
		if claimIndex >= 0 && !fundraisingClaimIsRejectedOrNegated(normalized, claimIndex) {
			return true
		}
	}
	if containsAnyText(normalized, metricTerms) &&
		containsAnyText(normalized, []string{"一定金额", "某一金额", "某个金额", "待定金额", "待定阈值"}) {
		return true
	}
	if containsAnyText(normalized, metricTerms) &&
		containsAnyText(normalized, []string{"才有意义", "才算达标", "方可达标", "才具备意义"}) {
		return true
	}

	// Reject institutional-process rules assembled from otherwise ordinary
	// words. This catches a metric being placed into a financing stage or a
	// missing field being described as determinative of diligence, while still
	// allowing the advisor to label its own suggested priority order as such.
	institutionTerms := []string{
		"投资人", "投资机构", "机构", "尽调", "dd", "正式会面", "投决", "过会",
	}
	roundTerms := []string{
		"天使轮", "种子轮", "pre-a", "a轮", "a 轮", "b轮", "b 轮", "c轮", "c 轮",
	}
	stagePlacementTerms := []string{
		"达标", "合格", "早期体量", "成熟体量", "准备就绪", "可启动", "可进入",
	}
	decisionTerms := []string{
		"决定", "取决于", "必须", "首先要求", "要求", "需要", "难以",
		"无法", "不能", "阻断", "否决", "拒绝", "会被", "将被", "被视为", "认定为",
		"打折", "收紧", "下限", "停", "入场券", "愿意看", "会关注", "通常关注", "偏好",
		"质疑", "挑战", "追问", "询问", "提问", "会问", "要问", "指出", "认可", "接受", "推进", "进入",
	}
	if containsAnyText(normalized, metricTerms) &&
		containsAnyText(normalized, roundTerms) &&
		containsAnyText(normalized, stagePlacementTerms) {
		claimIndex := firstFundraisingClaimIndex(normalized, stagePlacementTerms)
		if claimIndex >= 0 && !fundraisingClaimIsRejectedOrNegated(normalized, claimIndex) {
			return true
		}
	}
	if containsAnyText(normalized, institutionTerms) && containsAnyText(normalized, decisionTerms) {
		claimIndex := firstFundraisingClaimIndex(normalized, decisionTerms)
		if claimIndex >= 0 && !fundraisingClaimIsRejectedOrNegated(normalized, claimIndex) {
			return true
		}
	}

	benchmarkTerms := []string{
		"门槛",
		"经验区间",
		"舒适带",
		"可被关注区间",
		"讨论区间",
		"行业常见",
	}
	hasBenchmarkTerm := false
	for _, phrase := range benchmarkTerms {
		if strings.Contains(normalized, phrase) {
			hasBenchmarkTerm = true
			break
		}
	}
	if strings.Contains(normalized, "常见") && strings.Contains(normalized, "区间") {
		hasBenchmarkTerm = true
	}
	if !hasBenchmarkTerm {
		return false
	}

	// Preserve explicit cautions such as “不得写成已达门槛”. A sentence that
	// positively predicates the benchmark remains invalid even if a later
	// parenthesis says that no benchmark was performed.
	predicateIndex := -1
	for _, predicate := range []string{"属于", "达到", "已达", "处于", "进入", "视为", "算作", "作为"} {
		if idx := strings.Index(line, predicate); idx >= 0 && (predicateIndex < 0 || idx < predicateIndex) {
			predicateIndex = idx
		}
	}
	if predicateIndex >= 0 {
		return !fundraisingClaimIsRejectedOrNegated(line, predicateIndex)
	}
	for _, negation := range []string{
		"不得", "不能", "不应", "不宜", "不要", "禁止", "不预设", "不引入", "不自创",
		"不保证", "不承诺", "不构成", "不视为", "不作为", "不就", "无法判断",
		"不是", "并非", "不代表", "不等于",
	} {
		if strings.Contains(line, negation) {
			return false
		}
	}
	return true
}

// fundraisingUnsupportedInstitutionBehaviorClaim detects uncited predictions
// about what an investor, committee, diligence process, or adviser will do.
// It is intentionally actor/action based rather than tied to a specific BP
// topic, so the same rule covers legal, market, metric, and product claims.
func fundraisingUnsupportedInstitutionBehaviorClaim(line string) bool {
	normalized := strings.ToLower(strings.TrimSpace(line))
	if normalized == "" ||
		fundraisingLineHasCitation(normalized) ||
		fundraisingLineIsAnalystGoalRecommendation(normalized) ||
		fundraisingLineIsProfessionalReviewBoundary(normalized) {
		return false
	}
	if strings.Count(normalized, "|") >= 3 {
		for _, cell := range strings.Split(strings.Trim(normalized, "| "), "|") {
			if fundraisingUnsupportedInstitutionBehaviorClaim(cell) {
				return true
			}
		}
		return false
	}
	actors := []string{
		"投资人", "投资机构", "机构", "投委", "投决", "尽调", "dd",
		"fa", "融资顾问", "财务顾问",
	}
	actions := []string{
		"卡", "删", "拒", "否决", "挑", "质疑", "追问", "要求", "建议",
		"打折", "收紧", "认可", "接受", "推进", "进入",
	}
	if !containsAnyText(normalized, actors) || !containsAnyText(normalized, actions) {
		return false
	}
	actorIndex := firstFundraisingClaimIndex(normalized, actors)
	claimIndex := firstFundraisingClaimIndex(normalized, actions)
	// "本分析建议……并非机构规则" describes the analysis's own action;
	// it is not a prediction by the institution named later in the sentence.
	return actorIndex >= 0 && claimIndex > actorIndex &&
		!fundraisingClaimIsRejectedOrNegated(normalized, claimIndex)
}

func fundraisingUnsupportedMissingFactClaim(task string, line string) bool {
	normalizedTask := strings.ToLower(strings.TrimSpace(task))
	normalizedLine := strings.ToLower(strings.TrimSpace(line))
	if normalizedLine == "" {
		return false
	}
	claims := []string{
		"未经审计",
		"未经过审计",
		"尚未审计",
		"没有审计",
		"未经独立审阅",
		"未经过独立审阅",
		"未经第三方验证",
		"未经过第三方验证",
		"仅为规划方向",
		"仍为规划方向",
		"处于规划阶段",
		"规划中",
		"尚未实现",
		"未实现",
		"尚未落地",
		"未落地",
		"尚未上线",
		"未上线",
		"待开发",
		"尚未立项",
	}
	for _, claim := range claims {
		if strings.Contains(normalizedLine, claim) && !strings.Contains(normalizedTask, claim) {
			return true
		}
	}
	if containsAnyText(normalizedLine, []string{"非", "不是", "不属于"}) &&
		containsAnyText(normalizedLine, []string{"现有功能", "当前功能", "已实现功能"}) &&
		!containsAnyText(normalizedTask, []string{"非现有功能", "不是现有功能", "不属于现有功能"}) {
		return true
	}
	return false
}

func fundraisingUnsupportedMetricInference(task string, line string) bool {
	normalizedTask := strings.ToLower(strings.TrimSpace(task))
	normalizedLine := strings.ToLower(strings.TrimSpace(line))
	if normalizedLine == "" ||
		fundraisingLineHasCitation(normalizedLine) ||
		fundraisingLineIsTransparentAssumption(normalizedLine) {
		return false
	}
	metricTerms := []string{
		"arr", "毛利率", "净收入留存", "nrr", "增长", "增速", "续约率",
		"流失率", "cac", "回收期", "burn multiple", "客单价", "ltv",
	}
	if !containsAnyText(normalizedLine, metricTerms) {
		return false
	}
	for _, adjective := range []string{
		"健康", "强劲", "亮眼", "优秀", "领先", "成熟", "可观", "稳健",
		"高速", "快速", "良好", "优质", "偏高", "偏低",
	} {
		if idx := strings.Index(normalizedLine, adjective); idx >= 0 &&
			!fundraisingClaimIsRejectedOrNegated(normalizedLine, idx) {
			return true
		}
	}
	causalPredicates := []string{"意味着", "代表", "说明", "表明", "反映", "体现", "证明", "显示", "可作为", "可以作为", "偏向"}
	claimIndex := firstFundraisingClaimIndex(normalizedLine, causalPredicates)
	if claimIndex < 0 || fundraisingClaimIsRejectedOrNegated(normalizedLine, claimIndex) {
		return false
	}
	tail := normalizedLine[claimIndex:]
	claimClause := tail
	if boundary := strings.IndexAny(claimClause, "，,；;。"); boundary >= 0 {
		claimClause = claimClause[:boundary]
	}
	businessOutcomes := []string{
		"业务", "收入", "客户", "产品", "市场", "商业", "订阅", "项目",
		"交付", "成本", "模式", "壁垒", "黏性", "粘性", "持续", "稳定",
		"验证", "成熟", "质量", "效率", "可重复", "扩张", "销售", "需求",
		"饱和", "软件", "硬件", "属性", "结构", "pmf",
	}
	if !containsAnyText(claimClause, businessOutcomes) {
		return false
	}
	for _, outcome := range businessOutcomes {
		if strings.Contains(claimClause, outcome) && !strings.Contains(normalizedTask, outcome) {
			return true
		}
	}
	return false
}

func fundraisingUnsupportedExternalQuantification(task string, line string) bool {
	normalizedTask := normalizeFundraisingQuantityText(task)
	normalizedLine := strings.ToLower(strings.TrimSpace(line))
	if normalizedLine == "" ||
		fundraisingLineHasCitation(normalizedLine) ||
		fundraisingLineIsTransparentAssumption(normalizedLine) ||
		fundraisingLineIsTraceableArithmeticCalculation(task, normalizedLine) {
		return false
	}
	externalContext := []string{
		"行业", "市场", "同类", "典型", "公开可见", "实践", "惯例", "通常", "一般",
		"投资机构", "机构", "融资", "估值", "轮", "种子", "天使", "pre-a", "pre a",
	}
	if !containsAnyText(normalizedLine, externalContext) {
		return false
	}
	for _, match := range fundraisingQuantityRe.FindAllStringIndex(normalizedLine, -1) {
		quantity := normalizedLine[match[0]:match[1]]
		normalizedQuantity := normalizeFundraisingQuantityText(quantity)
		if normalizedQuantity != "" &&
			!strings.Contains(normalizedTask, normalizedQuantity) &&
			!fundraisingClaimIsRejectedOrNegated(normalizedLine, match[0]) {
			return true
		}
	}
	return false
}

func fundraisingUnsupportedPrescriptiveQuantity(task string, line string) bool {
	normalizedTask := normalizeFundraisingQuantityText(task)
	normalizedLine := strings.ToLower(strings.TrimSpace(line))
	if normalizedLine == "" ||
		fundraisingLineHasCitation(normalizedLine) ||
		fundraisingLineIsTransparentAssumption(normalizedLine) ||
		fundraisingLineIsTraceableArithmeticCalculation(task, normalizedLine) {
		return false
	}
	if !containsAnyText(normalizedLine, []string{
		"至少", "至多", "不低于", "不高于", "不少于", "不超过",
		"满足", "门槛", "阈值", "条件", "才可", "方可", "必须",
	}) {
		return false
	}
	for _, match := range fundraisingThresholdQuantityRe.FindAllStringIndex(normalizedLine, -1) {
		quantity := normalizedLine[match[0]:match[1]]
		normalizedQuantity := normalizeFundraisingQuantityText(quantity)
		if normalizedQuantity != "" &&
			!strings.Contains(normalizedTask, normalizedQuantity) &&
			!fundraisingClaimIsRejectedOrNegated(normalizedLine, match[0]) {
			return true
		}
	}
	return false
}

func fundraisingUnsupportedSuperlativeClaim(line string) bool {
	normalized := strings.ToLower(strings.TrimSpace(line))
	if normalized == "" ||
		fundraisingLineHasCitation(normalized) ||
		fundraisingLineIsTransparentAssumption(normalized) {
		return false
	}
	match := fundraisingSuperlativeRe.FindStringIndex(normalized)
	return match != nil && !fundraisingClaimIsRejectedOrNegated(normalized, match[0])
}

func fundraisingUnsupportedRoundPlacementClaim(task string, line string) bool {
	normalizedTask := strings.ToLower(strings.TrimSpace(task))
	normalizedLine := strings.ToLower(strings.TrimSpace(line))
	if normalizedLine == "" ||
		fundraisingLineHasCitation(normalizedLine) {
		return false
	}
	if fundraisingLineIsTransparentAssumption(normalizedLine) &&
		!fundraisingBPOnlyRequest(task) {
		return false
	}
	rounds := []string{"天使轮", "种子轮", "pre-a", "pre a", "a轮", "a 轮", "b轮", "b 轮", "c轮", "c 轮"}
	placementTerms := []string{"区间", "属于", "适合", "匹配", "定位为", "阶段", "语境", "下调到", "上调到"}
	for _, round := range rounds {
		if strings.Contains(normalizedLine, round) &&
			!strings.Contains(normalizedTask, round) &&
			containsAnyText(normalizedLine, placementTerms) {
			claimIndex := firstFundraisingClaimIndex(normalizedLine, placementTerms)
			return claimIndex >= 0 && !fundraisingClaimIsRejectedOrNegated(normalizedLine, claimIndex)
		}
	}
	return false
}

func normalizeFundraisingQuantityText(value string) string {
	value = fundraisingMultiplierRe.ReplaceAllString(value, `${1}倍`)
	replacer := strings.NewReplacer(
		" ", "",
		",", "",
		"，", "",
		"％", "%",
		"–", "-",
		"—", "-",
		"～", "-",
		"~", "-",
		"至", "-",
		"到", "-",
	)
	return strings.ToLower(replacer.Replace(strings.TrimSpace(value)))
}

func fundraisingLineHasCitation(line string) bool {
	lower := strings.ToLower(line)
	return strings.Contains(lower, "http://") || strings.Contains(lower, "https://")
}

func fundraisingLineIsTransparentAssumption(line string) bool {
	return containsAnyText(strings.ToLower(line), []string{
		"本分析建议", "建议值", "建议目标", "建议区间", "作为目标", "目标值",
		"假设", "假定", "若按", "示例", "举例", "占位", "待验证", "推断",
	})
}

func fundraisingLineIsAnalystGoalRecommendation(line string) bool {
	return containsAnyText(strings.ToLower(line), []string{
		"本分析建议把", "本分析建议将", "本分析建议以",
		"建议值", "建议目标", "建议区间", "作为目标", "目标值", "待验证目标",
	})
}

func fundraisingLineIsProfessionalReviewBoundary(line string) bool {
	normalized := strings.ToLower(strings.TrimSpace(line))
	return containsAnyText(normalized, []string{
		"律师", "会计师", "税务师", "审计师", "持牌机构", "专业人士",
	}) && containsAnyText(normalized, []string{
		"复核", "审阅", "核验", "确认", "咨询",
	})
}

// fundraisingLineRequestsUserInput distinguishes a requested value from an
// asserted benchmark. A clarification field may contain round names, example
// formats, or numeric placeholders, but it makes no claim until the user
// supplies an answer.
func fundraisingLineRequestsUserInput(line string) bool {
	normalized := strings.ToLower(strings.TrimSpace(line))
	if normalized == "" {
		return false
	}
	if strings.HasSuffix(normalized, "？") || strings.HasSuffix(normalized, "?") {
		return true
	}
	if (strings.Contains(normalized, "？") || strings.Contains(normalized, "?")) &&
		containsAnyText(normalized, []string{
			"是否", "什么", "多少", "哪", "关系", "请提供", "请确认", "请选择",
			"how much", "which", "what", "whether", "please provide", "please confirm",
		}) {
		return true
	}
	structuredField := strings.Contains(normalized, "**") &&
		containsAnyText(normalized, []string{"：", ":"}) &&
		(markdownOrderedListItemRe.MatchString(line) ||
			strings.HasPrefix(normalized, "- ") ||
			strings.HasPrefix(normalized, "* ") ||
			strings.HasPrefix(normalized, "+ "))
	return structuredField && containsAnyText(normalized, []string{
		"是否", "是什么", "多少", "哪一", "哪种", "有无", "关系", "状态", "当前是", "还是",
	})
}

func fundraisingLineRejectsClaim(line string) bool {
	return containsAnyText(strings.ToLower(line), []string{
		"没有证据支持", "无证据支持", "缺乏证据", "缺少证据", "缺少来源", "未给出来源",
		"站不住", "不可验证", "不能据此", "不得", "不应", "不宜", "不要宣称",
		"应删除", "删除该", "移除该", "改写为", "绝对化断言",
		"不是行业", "并非行业", "不代表", "不等于",
	})
}

func fundraisingClaimIsRejectedOrNegated(line string, claimIndex int) bool {
	if hasFundraisingClaimNegationBefore(line, claimIndex) {
		return true
	}
	if claimIndex < 0 || claimIndex >= len(line) {
		return false
	}
	// A later, explicit rejection can invalidate a quoted or immediately
	// critiqued claim. A generic disclaimer about a different conclusion must
	// not retroactively legitimize an earlier benchmark assertion.
	tail := line[claimIndex:]
	return containsAnyText(tail, []string{
		"没有证据支持", "无证据支持", "缺乏证据", "缺少证据", "缺少来源",
		"未提供来源", "未提供公开来源", "没有来源", "无来源",
		"站不住", "不可验证", "应删除", "删除该", "移除该", "绝对化断言",
	})
}

func fundraisingMetricClaimContext(sectionContext, line string) string {
	if sectionContext == "" || sectionContext == line {
		return line
	}
	if containsAnyText(strings.ToLower(sectionContext), []string{
		"arr", "毛利率", "净收入留存", "nrr", "增长", "增长率", "增速",
		"续约率", "流失率", "cac", "回收期", "burn multiple", "客单价", "ltv",
	}) {
		return sectionContext + " " + line
	}
	return line
}

func fundraisingEvidenceLabelContext(sectionContext, line string) string {
	if sectionContext == "" || sectionContext == line {
		return line
	}
	if fundraisingLineIsTransparentAssumption(sectionContext) {
		return sectionContext + " " + line
	}
	return line
}

func firstFundraisingClaimIndex(line string, phrases []string) int {
	first := -1
	for _, phrase := range phrases {
		if idx := strings.Index(line, phrase); idx >= 0 && (first < 0 || idx < first) {
			first = idx
		}
	}
	return first
}

func hasFundraisingClaimNegationBefore(line string, claimIndex int) bool {
	if claimIndex < 0 {
		return false
	}
	for _, negation := range []string{
		"不得", "不能", "不应", "不宜", "不要", "禁止", "不预设", "不引入", "不自创",
		"不保证", "不承诺", "不构成", "不视为", "不作为", "不就", "无法判断",
		"不是", "并非", "不代表", "不等于",
	} {
		if idx := strings.Index(line, negation); idx >= 0 && idx <= claimIndex {
			return true
		}
	}
	return false
}

func buildAIEmployeeDomainOutputRepairPrompt(basePrompt string, issues []string, previous string) string {
	var b strings.Builder
	b.WriteString(basePrompt)
	b.WriteString("\n\n---\n\n[FINAL USER ANSWER QUALITY REPAIR]\n")
	b.WriteString("Rewrite the entire answer as the final user-facing response. Do not mention this repair, model steps, searches, internal files, or quality checks. Use only facts already available in the task and prior draft; do not call tools or introduce new facts.\n")
	b.WriteString("The prior draft made unsupported metric evaluations, business inferences, fundraising-stage benchmarks, institutional-practice claims, prescriptive numeric thresholds, or commercial superlatives. Remove those claims and every synonym. User-provided metrics may be described only through neutral definitions, arithmetic, and their explicitly uncovered dimensions unless a current comparable source, sample, date, and applicability are actually cited. Do not relabel a metric as healthy, strong, mature, leading, scalable, or evidence of a business structure.\n")
	b.WriteString("Do not predict that investors or institutions will reject, block, discount, tighten terms, require a particular document, or advance a meeting. Do not call a company first, unique, leading, largest, or earliest without direct current evidence. State priorities and any new numeric conditions explicitly as this analysis's recommendations, assumptions, examples, targets, or placeholders, and explain what each item would help verify. For BP creation, a useful editable planning estimate is allowed when it is clearly labelled 'planning estimate (not a project fact)', exposes its assumptions and derivation, and is kept separate from actual revenue, customers, contracts, team history or financing history. For an explicit calculation request, preserve every auditable formula and numeric result derived from the user's inputs; do not delete a result merely because it was not literally present in the prompt. Information not supplied by the user is unknown or pending verification; never turn silence into a negative fact such as unaudited, absent, or unverified.\n")
	b.WriteString("Label user-provided or self-reported inputs as user-provided or self-reported, not as verified facts, unless the task explicitly says they were independently verified. A label such as “verified facts from user input” is contradictory and must be corrected.\n")
	b.WriteString("Audit provenance claim by claim. Attribute a statement to an article, URL, attachment, or other source only when that exact source actually contains it, and identify the source on the same row or claim. Generic labels such as “public information”, “public sources”, “common knowledge”, or “industry practice” are not traceable sources and must never be labelled verified; provide the exact URL/document location or downgrade the statement to an unverified analysis hypothesis. Text the user typed next to a URL is user-provided context, not content from that URL. If no supplied or retrieved source contains a claim, label it as user-provided intent, project-side statement, analysis hypothesis, or no public source; never invent a citation or say that the original article mentioned it. When the user asks for sources, credibility, verification, or key-fact extracts, return a compact evidence matrix covering fact, provenance, verification status or credibility, and limitations.\n")
	b.WriteString("Do not expose or cite internal skill names, runtime directories, reference filenames, scripts, tool calls, or scoring mechanics. The skill is an internal method, not a source. For a BP creation or BP review request, do not add a financing-readiness diagnosis or 0–5 scorecard unless the user explicitly requested a readiness assessment or score. Missing evidence stays unknown and must never become 0/5 or another inferred score.\n")
	b.WriteString("Preserve the user's requested answer shape while removing unsupported claims. If the user asks what supplied evidence can and cannot support, every supplied metric must retain a useful “能支持” and “不能支持” treatment. A safe positive treatment reports the metric's neutral definition or arithmetic consequence under the stated calculation basis; it does not infer business quality, stage fit, investor behavior, or a market benchmark. Do not solve a quality issue by deleting an entire requested side, section, table row, or question.\n")
	b.WriteString("Preserve unresolved product architecture. Do not silently convert a company, product, reference product, technical capability, functional module, application scenario, customer outcome, or business goal into parallel product lines, business lines, numbered directions, tracks, offerings, first/second curves, or numbered D1/D2/D3 categories. Renaming an unsupported classification or adding 'candidate', 'hypothesis' or 'pending confirmation' does not make it valid. Labels such as OBJ-1/OBJ-2, reference line, candidate line, business direction 1/2, module A/B, candidate direction, or composite chain are the same unsupported classification; use natural role names such as target product, reference object, and user-provided description instead. A later phrase such as 'relationship unknown' does not cure an earlier architecture assertion. If the task or evidence does not establish the relationships, use only a neutral object map that preserves every user-provided target product, reference object and connected capability in its original role. Do not propose, select, or reuse a candidate architecture anywhere in the answer. A reference product must remain a reference rather than becoming part of the user's product matrix. Preserve any capability expression that the user grouped as one continuous or quoted phrase verbatim in one row or paragraph; do not follow it with a capability list, a lettered/numbered literal decomposition such as (a)/(b)/(c), or a categorical rewrite such as '分别对应两类场景' that splits its nouns into separate products, modules, directions, businesses, or pseudo-objects. If implementation status was not supplied, write status unknown; do not invent that an object is merely planned, unimplemented, or not a current feature.\n")
	b.WriteString("For a BP creation request, deliver the complete best-effort BP immediately. Unless the user explicitly asked for an interactive intake, do not ask confirmation or clarification questions anywhere and do not append a decisive-question section. Express missing information only as declarative unknowns, nonblocking [待补] fields, or a concise '待补资料（不阻塞本稿）' list. For non-BP work or an explicitly requested interactive intake, keep any questions genuinely necessary and concise.\n")
	b.WriteString("Enforce transaction-definition, arithmetic, and comparison consistency. For a non-participating liquidation preference, the preferred holder chooses either (a) the preference amount, with no participation in the remaining proceeds, or (b) conversion into common and a pro-rata share of the total proceeds. Never add the preference amount to a share of the remainder. The conversion payout increases with exit proceeds while the preference amount is fixed: conversion overtakes only above the crossover preference/ownership percentage, and the preference path dominates below it. Recompute every affected investor and common-holder amount, threshold direction, comparison, table row, formula, and conclusion so that no contradiction remains elsewhere in the answer.\n")
	b.WriteString("Detected claim lines:\n")
	for _, issue := range issues {
		fmt.Fprintf(&b, "- %s\n", issue)
	}
	b.WriteString("\nPrevious draft to rewrite:\n")
	b.WriteString(previous)
	return b.String()
}

func buildMarketAnalystOutputRepairPrompt(basePrompt string, issues []string, previous string) string {
	var b strings.Builder
	b.WriteString(basePrompt)
	b.WriteString("\n\n---\n\n[FINAL MARKET RESEARCH QUALITY REPAIR]\n")
	b.WriteString("Rewrite the complete final user-facing market analysis. Use web search to discover candidate evidence, then fetch the concrete source page whenever it is accessible. Prefer primary sources and official publications. Put a concrete external URL next to each material claim, and state the publication date or explicit as-of date for time-sensitive data. If a concrete page remains inaccessible after one effective fetch attempt, you may preserve only the exact title, publisher, date and URL returned by search, clearly labelled as a search-index lead pending verification; it may support a qualitative direction but never a precise number. Never invent or assemble a URL. If a precise number cannot be supported by an accessible source page, omit it. Preserve the requested trends, conclusions and downloadable report; the saved report must contain the same traceable links as the chat answer. Do not mention this repair or internal execution details.\n")
	b.WriteString("Detected issues:\n")
	for _, issue := range issues {
		fmt.Fprintf(&b, "- %s\n", issue)
	}
	b.WriteString("\nPrevious draft to rewrite:\n")
	b.WriteString(previous)
	return b.String()
}

func buildAIEmployeeDomainRepairPrompt(pipeline *Pipeline, basePrompt string, issues []string, previous string) string {
	if pipeline != nil && strings.TrimSpace(pipeline.AIEmployeeID) == "market-analyst" {
		return buildMarketAnalystOutputRepairPrompt(basePrompt, issues, previous)
	}
	return buildAIEmployeeDomainOutputRepairPrompt(basePrompt, issues, previous)
}

func stripUnsupportedFundraisingBenchmarkLines(output string, originalTask ...string) string {
	task := ""
	if len(originalTask) > 0 {
		task = originalTask[0]
	}
	if fundraisingExplicitOptionHandoff(task, output) {
		return strings.TrimSpace(output)
	}
	lines := strings.Split(strings.ReplaceAll(output, "\r\n", "\n"), "\n")
	kept := make([]string, 0, len(lines))
	sectionContext := ""
	removedOrderedListItem := false
	skipOrderedContinuation := false
	skipOrderedIndent := 0
	for index, line := range lines {
		trimmed := strings.TrimSpace(line)
		if skipOrderedContinuation {
			if trimmed == "" {
				continue
			}
			if strings.HasPrefix(trimmed, "#") {
				skipOrderedContinuation = false
			} else if match := markdownOrderedListItemRe.FindStringSubmatch(line); len(match) == 5 && len(match[1]) <= skipOrderedIndent {
				skipOrderedContinuation = false
			} else if len(line)-len(strings.TrimLeft(line, " \t")) > skipOrderedIndent {
				continue
			} else {
				skipOrderedContinuation = false
			}
		}
		if strings.HasPrefix(trimmed, "#") {
			sectionContext = trimmed
		}
		if fundraisingLineRequestsUserInput(trimmed) {
			if !fundraisingUnsupportedBPAssumptionQuantity(task, sectionContext, trimmed) &&
				!fundraisingUnsupportedInferredProductLineClaim(task, trimmed) &&
				!fundraisingLineSplitsTrailingDescription(task, trimmed) {
				kept = append(kept, line)
			}
			continue
		}
		if markdownTableHeaderAt(lines, index) {
			kept = append(kept, line)
			continue
		}
		claimText := fundraisingMetricClaimContext(sectionContext, trimmed)
		evidenceText := fundraisingEvidenceLabelContext(sectionContext, trimmed)
		if fundraisingLineHasUnsupportedClaim(task, sectionContext, trimmed, claimText, evidenceText) {
			if repaired, ok := scrubUnsupportedFundraisingTableRow(task, sectionContext, line); ok {
				kept = append(kept, repaired)
				continue
			}
			if match := markdownOrderedListItemRe.FindStringSubmatch(line); len(match) == 5 {
				removedOrderedListItem = true
				skipOrderedContinuation = true
				skipOrderedIndent = len(match[1])
			}
			continue
		}
		kept = append(kept, line)
	}
	cleaned := strings.TrimSpace(strings.Join(kept, "\n"))
	if removedOrderedListItem {
		cleaned = normalizeMarkdownOrderedListNumbers(cleaned)
	}
	cleaned = removeEmptyMarkdownSections(cleaned)
	cleaned = removeEmptyMarkdownLabelBlocks(cleaned)
	cleaned = removeEmptyMarkdownQuestionItems(cleaned)
	cleaned = normalizeMarkdownOrderedListNumbers(cleaned)
	return collapseDuplicateMarkdownRules(cleaned)
}

// scrubUnsupportedFundraisingTableRow preserves the user's requested table
// shape when only one semicolon-delimited clause is unsafe. Deleting an entire
// row can silently turn a requested three-item checklist into one item. The
// scrubber is limited to ordinary content tables; evidence matrices still
// require a complete traceable row and are never patched with placeholders.
func scrubUnsupportedFundraisingTableRow(task, sectionContext, line string) (string, bool) {
	if strings.Count(line, "|") < 3 || fundraisingSectionIsEvidenceMatrix(sectionContext) {
		return "", false
	}
	parts := strings.Split(strings.TrimSpace(line), "|")
	if len(parts) < 4 {
		return "", false
	}
	rowHasUnsupportedExternalQuantity := fundraisingUnsupportedExternalQuantification(task, line) ||
		fundraisingUnsupportedPrescriptiveQuantity(task, line)
	changed := false
	for i := 1; i < len(parts)-1; i++ {
		cell := strings.TrimSpace(parts[i])
		if cell == "" {
			continue
		}
		clauses := strings.FieldsFunc(cell, func(r rune) bool {
			return r == '；' || r == ';' || r == '。'
		})
		keptClauses := make([]string, 0, len(clauses))
		for _, clause := range clauses {
			clause = strings.TrimSpace(clause)
			if clause == "" {
				continue
			}
			claimText := fundraisingMetricClaimContext(sectionContext, clause)
			evidenceText := fundraisingEvidenceLabelContext(sectionContext, clause)
			unsupported := fundraisingLineHasUnsupportedClaim(
				task,
				sectionContext,
				clause,
				claimText,
				evidenceText,
			)
			if !unsupported && rowHasUnsupportedExternalQuantity &&
				fundraisingContainsNewQuantity(task, clause) {
				unsupported = true
			}
			if unsupported {
				changed = true
				continue
			}
			keptClauses = append(keptClauses, clause)
		}
		if len(keptClauses) == 0 {
			parts[i] = " 待补充可核验信息 "
			changed = true
			continue
		}
		parts[i] = " " + strings.Join(keptClauses, "；") + " "
	}
	if !changed {
		return "", false
	}
	repaired := strings.TrimSpace(strings.Join(parts, "|"))
	claimText := fundraisingMetricClaimContext(sectionContext, repaired)
	evidenceText := fundraisingEvidenceLabelContext(sectionContext, repaired)
	if fundraisingLineHasUnsupportedClaim(task, sectionContext, repaired, claimText, evidenceText) {
		return "", false
	}
	return repaired, true
}

func fundraisingContainsNewQuantity(task, line string) bool {
	normalizedTask := normalizeFundraisingQuantityText(task)
	normalizedLine := strings.ToLower(strings.TrimSpace(line))
	for _, match := range fundraisingQuantityRe.FindAllStringIndex(normalizedLine, -1) {
		quantity := normalizeFundraisingQuantityText(normalizedLine[match[0]:match[1]])
		if quantity != "" && !strings.Contains(normalizedTask, quantity) {
			return true
		}
	}
	return false
}

// normalizeMarkdownOrderedListNumbers repairs list numbering after a rejected
// claim line is removed. It operates on Markdown structure rather than domain
// wording and resets numbering at headings, rules, and ordinary top-level text.
func normalizeMarkdownOrderedListNumbers(markdown string) string {
	lines := strings.Split(strings.ReplaceAll(markdown, "\r\n", "\n"), "\n")
	nextByIndent := map[int]int{}
	activeByIndent := map[int]bool{}
	for i, line := range lines {
		match := markdownOrderedListItemRe.FindStringSubmatch(line)
		if len(match) == 5 {
			indent := len(match[1])
			next := nextByIndent[indent]
			if !activeByIndent[indent] || next <= 0 {
				next = 1
			}
			lines[i] = fmt.Sprintf("%s%d%s%s%s", match[1], next, match[2], match[3], match[4])
			nextByIndent[indent] = next + 1
			activeByIndent[indent] = true
			for otherIndent := range activeByIndent {
				if otherIndent > indent {
					delete(activeByIndent, otherIndent)
					delete(nextByIndent, otherIndent)
				}
			}
			continue
		}
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "#") ||
			strings.HasPrefix(trimmed, "---") ||
			(len(line) == len(strings.TrimLeft(line, " \t")) &&
				!strings.HasPrefix(trimmed, "-") &&
				!strings.HasPrefix(trimmed, "*") &&
				!strings.HasPrefix(trimmed, ">")) {
			nextByIndent = map[int]int{}
			activeByIndent = map[int]bool{}
		}
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

var markdownBoldLabelRe = regexp.MustCompile(`^\s*\*\*[^*\n]+\*\*\s*[:：]?\s*$`)

// removeEmptyMarkdownLabelBlocks removes a bold block label only when claim
// removal left it directly followed by another structural boundary.
func removeEmptyMarkdownLabelBlocks(markdown string) string {
	lines := strings.Split(strings.ReplaceAll(markdown, "\r\n", "\n"), "\n")
	remove := make(map[int]bool)
	for i, line := range lines {
		if !markdownBoldLabelRe.MatchString(line) {
			continue
		}
		for j := i + 1; j < len(lines); j++ {
			next := strings.TrimSpace(lines[j])
			if next == "" {
				continue
			}
			if markdownBoldLabelRe.MatchString(lines[j]) ||
				markdownHeadingRe.MatchString(next) ||
				strings.Trim(next, "-*_ ") == "" {
				remove[i] = true
			}
			break
		}
	}
	kept := make([]string, 0, len(lines))
	for i, line := range lines {
		if !remove[i] {
			kept = append(kept, line)
		}
	}
	return strings.TrimSpace(strings.Join(kept, "\n"))
}

func removeEmptyMarkdownQuestionItems(markdown string) string {
	lines := strings.Split(strings.ReplaceAll(markdown, "\r\n", "\n"), "\n")
	remove := make(map[int]bool)
	inQuestionSection := false
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if heading := markdownHeadingRe.FindStringSubmatch(trimmed); len(heading) == 2 {
			inQuestionSection = containsAnyText(strings.ToLower(trimmed), []string{
				"问题", "追问", "确认", "补充", "澄清",
			})
			continue
		}
		match := markdownOrderedListItemRe.FindStringSubmatch(line)
		if !inQuestionSection || len(match) != 5 {
			continue
		}
		itemIndent := len(match[1])
		hasContinuation := false
		for j := i + 1; j < len(lines); j++ {
			next := lines[j]
			nextTrimmed := strings.TrimSpace(next)
			if nextTrimmed == "" {
				continue
			}
			if markdownHeadingRe.MatchString(nextTrimmed) {
				break
			}
			if nextItem := markdownOrderedListItemRe.FindStringSubmatch(next); len(nextItem) == 5 && len(nextItem[1]) <= itemIndent {
				break
			}
			if len(next)-len(strings.TrimLeft(next, " \t")) > itemIndent {
				hasContinuation = true
			}
			break
		}
		if !hasContinuation &&
			!strings.ContainsAny(match[4], "?？") &&
			markdownQuestionItemIsLabelOnly(match[4]) {
			remove[i] = true
		}
	}
	kept := make([]string, 0, len(lines))
	for i, line := range lines {
		if !remove[i] {
			kept = append(kept, line)
		}
	}
	return strings.TrimSpace(strings.Join(kept, "\n"))
}

func markdownQuestionItemIsLabelOnly(body string) bool {
	body = strings.TrimSpace(body)
	if body == "" {
		return true
	}
	for _, delimiter := range []string{"：", ":"} {
		if index := strings.Index(body, delimiter); index >= 0 {
			return strings.Trim(strings.TrimSpace(body[index+len(delimiter):]), "*_` ") == ""
		}
	}
	if (strings.HasPrefix(body, "**") && strings.HasSuffix(body, "**")) ||
		(strings.HasPrefix(body, "__") && strings.HasSuffix(body, "__")) ||
		(strings.HasPrefix(body, "`") && strings.HasSuffix(body, "`")) {
		return true
	}
	return false
}
