package app

import (
	"fmt"
	"strings"
	"testing"
)

const newsDeliveryPrompt = "整理今天（2026-07-11）值得关注的8条中国与全球科技/商业热点。每条必须包含：事件发生时间、媒体发布时间、核心事实、为什么重要、至少一个可打开的原始或权威来源。对其中最重要的3条再找第二来源核对。最后给一个150字以内的管理者摘要。"

func TestCurrentNewsDeliveryContractExtraction(t *testing.T) {
	contract, ok := currentNewsDeliveryContract(newsDeliveryPrompt)
	if !ok || contract.ItemCount != 8 || contract.CrossCheckCount != 3 || !contract.RequiresLinks || len(contract.RequiredLabels) != 4 || contract.SummaryMaxChars != 150 {
		t.Fatalf("unexpected news contract: ok=%v contract=%#v", ok, contract)
	}
}

func TestCurrentNewsDeliveryContractPrefersExplicitImportantSubset(t *testing.T) {
	input := "整理今天的8条科技商业新闻，每条给来源；对最重要的3条再找第二来源核对。"
	contract, ok := currentNewsDeliveryContract(input)
	if !ok || contract.ItemCount != 8 || contract.CrossCheckCount != 3 {
		t.Fatalf("important subset was confused with total item count: ok=%v contract=%#v", ok, contract)
	}
}

func TestResearchDeliveryIssuesRejectsZeroLinks(t *testing.T) {
	var answer strings.Builder
	answer.WriteString("| # | 事件发生时间 | 媒体发布时间 | 核心事实 | 为什么重要 |\n|---|---|---|---|---|\n")
	for i := 1; i <= 8; i++ {
		fmt.Fprintf(&answer, "| %d | today | today | fact | impact |\n", i)
	}
	answer.WriteString("\n## 管理者摘要\n摘要。")
	issues := researchDeliveryIssues(newsDeliveryPrompt, answer.String())
	if len(issues) == 0 || !strings.Contains(strings.Join(issues, " "), "article URL") {
		t.Fatalf("zero-link news answer passed delivery contract: %v", issues)
	}
}

func TestResearchDeliveryIssuesRejectsTableOnlyNewsBrief(t *testing.T) {
	var answer strings.Builder
	answer.WriteString("| # | 事件发生时间 | 媒体发布时间 | 核心事实 | 为什么重要 | 来源 |\n|---|---|---|---|---|---|\n")
	for i := 1; i <= 8; i++ {
		fmt.Fprintf(&answer, "| %d | 2026-07-11 | 2026-07-11 09:00 +0800 | fact | impact | https://publisher%d.example/news/article-%d |\n", i, i, i)
	}
	answer.WriteString("\n## 管理者摘要\n表格看似完整，但没有可逐项验证的编号正文。")
	issues := strings.Join(researchDeliveryIssues(newsDeliveryPrompt, answer.String()), " ")
	if !strings.Contains(issues, "answer contains only 0 clearly numbered item(s); 8 requested") {
		t.Fatalf("table-only answer bypassed numbered-item validation: %s", issues)
	}
}

func TestResearchDeliveryIssuesAcceptsEightItemsAndThreeIndependentChecks(t *testing.T) {
	var answer strings.Builder
	for i := 1; i <= 8; i++ {
		fmt.Fprintf(&answer, "## %d. Event\n\n- 事件发生时间：2026-07-11\n- 媒体发布时间：2026-07-11\n- 核心事实：verified\n- 为什么重要：impact\n- 来源：https://publisher%d.example/news/article-%d\n", i, i, i)
		if i <= 3 {
			fmt.Fprintf(&answer, "- 第二来源：https://independent%d.example/report/check-%d\n", i, i)
		}
		answer.WriteString("\n")
	}
	answer.WriteString("\n## 管理者摘要\n八条事件均按证据整理。")
	if issues := researchDeliveryIssues(newsDeliveryPrompt, answer.String()); len(issues) != 0 {
		t.Fatalf("complete news answer failed delivery contract: %v\n%s", issues, answer.String())
	}
}

func TestCurrentNewsCircledItemMarkersNormalizeToNumberedSections(t *testing.T) {
	markers := []rune("①②③④⑤⑥⑦⑧")
	var answer strings.Builder
	for index, marker := range markers {
		item := index + 1
		fmt.Fprintf(&answer, "### %c Event\n事件发生时间：2026-07-11\n媒体发布时间：2026-07-11\n核心事实：verified\n为什么重要：impact\n来源：https://publisher%d.example/news/a-%d\n", marker, item, item)
		if item <= 3 {
			fmt.Fprintf(&answer, "第二来源：https://cross%d.example/story/b-%d\n", item, item)
		}
	}
	answer.WriteString("\n## 150字以内 · 管理者摘要\n八条事件均按证据整理。")
	normalized, changed := normalizeCurrentNewsItemMarkers(answer.String())
	if changed != 8 || countResearchItems(normalized) != 8 {
		t.Fatalf("circled markers were not normalized: changed=%d items=%d\n%s", changed, countResearchItems(normalized), normalized)
	}
	if issues := researchDeliveryIssues(newsDeliveryPrompt, answer.String()); len(issues) != 0 {
		t.Fatalf("natural circled-marker layout failed delivery: %v", issues)
	}
}

func TestResearchDeliveryIssuesAllowsOneRoundupURLForDistinctEvents(t *testing.T) {
	var answer strings.Builder
	for i := 1; i <= 8; i++ {
		primaryIndex := i
		if i == 2 {
			primaryIndex = 1
		}
		fmt.Fprintf(&answer, "## %d. Event\n事件发生时间：2026-07-11\n媒体发布时间：2026-07-11\n核心事实：verified\n为什么重要：impact\n来源：https://publisher.example/news/roundup-%d\n", i, primaryIndex)
		if i <= 3 {
			fmt.Fprintf(&answer, "第二来源：https://independent%d.example/report/check-%d\n", i, i)
		}
	}
	answer.WriteString("\n## 管理者摘要\n八条独立事件均有逐条来源。")
	if issues := researchDeliveryIssues(newsDeliveryPrompt, answer.String()); len(issues) != 0 {
		t.Fatalf("one roundup URL supporting two distinct events was rejected: %v", issues)
	}
}

func TestResearchDeliveryIssuesAcceptsSemanticFieldAliases(t *testing.T) {
	var answer strings.Builder
	for i := 1; i <= 8; i++ {
		fmt.Fprintf(&answer, "## %d. Event\n事件时间：2026-07-11\n报道时间：2026-07-11\n关键事实：verified\n重要性：impact\n来源：https://publisher%d.example/news/a-%d\n", i, i, i)
		if i <= 3 {
			fmt.Fprintf(&answer, "复核：https://cross%d.example/story/b-%d\n", i, i)
		}
	}
	answer.WriteString("\n## 管理者摘要\n语义等价字段名不应触发机械失败。")
	if issues := researchDeliveryIssues(newsDeliveryPrompt, answer.String()); len(issues) != 0 {
		t.Fatalf("semantic field aliases failed delivery contract: %v", issues)
	}
}

func TestNormalizeCurrentNewsFieldLabelsAcceptsSemanticEmojiLayout(t *testing.T) {
	var answer strings.Builder
	for i := 1; i <= 8; i++ {
		fmt.Fprintf(&answer, "### %d. Event\n- ⏰ 媒体发布时间:2026-07-11\n- 📌 verified fact\n- 💡 material impact\n- 🔗 https://publisher%d.example/news/a-%d\n", i, i, i)
		if i <= 3 {
			fmt.Fprintf(&answer, "- 复核：https://cross%d.example/story/b-%d\n", i, i)
		}
	}
	answer.WriteString("\n## 管理者摘要\n语义化图标布局也应形成完整字段。")
	normalized, inserted := normalizeCurrentNewsFieldLabels(newsDeliveryPrompt, answer.String())
	if inserted != 8 || strings.Count(normalized, "**事件发生时间**") != 8 || strings.Count(normalized, "**核心事实**") != 8 || strings.Count(normalized, "**为什么重要**") != 8 {
		t.Fatalf("semantic field normalization incomplete: inserted=%d\n%s", inserted, normalized)
	}
	if issues := researchDeliveryIssues(newsDeliveryPrompt, normalized); len(issues) != 0 {
		t.Fatalf("normalized semantic layout failed delivery: %v", issues)
	}
}

func TestResearchDeliveryIssuesRejectsOversizeSummary(t *testing.T) {
	var answer strings.Builder
	for i := 1; i <= 8; i++ {
		fmt.Fprintf(&answer, "## %d. Event\n事件时间：2026-07-11\n媒体发布时间：2026-07-11\n核心事实：verified\n为什么重要：impact\n来源：https://publisher%d.example/news/a-%d\n", i, i, i)
		if i <= 3 {
			fmt.Fprintf(&answer, "复核：https://cross%d.example/story/b-%d\n", i, i)
		}
	}
	answer.WriteString("\n## 管理者摘要（≤150 字）\n")
	answer.WriteString(strings.Repeat("要", 151))
	issues := strings.Join(researchDeliveryIssues(newsDeliveryPrompt, answer.String()), " ")
	if !strings.Contains(issues, "summary contains 151") {
		t.Fatalf("oversize summary passed delivery contract: %s", issues)
	}
	normalized, changed, before, after := enforceCurrentNewsSummaryLimit(newsDeliveryPrompt, answer.String())
	if !changed || before != 151 || after > 150 {
		t.Fatalf("summary compaction = changed:%v before:%d after:%d", changed, before, after)
	}
	if issues := researchDeliveryIssues(newsDeliveryPrompt, normalized); len(issues) != 0 {
		t.Fatalf("deterministically compacted answer still failed delivery: %v", issues)
	}
}

func TestEnforceCurrentNewsSummaryLimitUsesSentenceBoundary(t *testing.T) {
	answer := "## 管理者摘要（100字以内）\n\n" + strings.Repeat("甲", 70) + "。" + strings.Repeat("乙", 50) + "。"
	input := "整理今天的3条科技新闻并给来源，最后给一个100字以内的管理者摘要。"
	normalized, changed, before, after := enforceCurrentNewsSummaryLimit(input, answer)
	if !changed || before <= 100 || after > 100 || !strings.HasSuffix(normalized, "。") || strings.Contains(normalized, "乙") {
		t.Fatalf("sentence-aware summary compaction failed: changed=%v before=%d after=%d output=%q", changed, before, after, normalized)
	}
}

func TestEnforceCurrentNewsSummaryLimitPreservesBoldApproximateLengthHeading(t *testing.T) {
	input := "整理今天的8条科技商业热点，每条给出来源，最后给一个150字以内的管理者摘要。"
	body := strings.Repeat("甲", 80) + "。" + strings.Repeat("乙", 80) + "。"
	answer := "**管理者摘要（约120字）**\n" + body

	normalized, changed, before, after := enforceCurrentNewsSummaryLimit(input, answer)
	if !changed || before <= 150 || after > 150 {
		t.Fatalf("bold approximate-length summary was not compacted safely: changed=%v before=%d after=%d output=%q", changed, before, after, normalized)
	}
	if !strings.HasPrefix(normalized, "**管理者摘要（约120字）**\n") {
		t.Fatalf("summary compaction damaged the bold heading: %q", normalized)
	}
	if !currentNewsSummaryDelimitersBalanced(normalized) {
		t.Fatalf("summary compaction left an unclosed delimiter: %q", normalized)
	}
}

func TestNewsSummaryDetectionRequiresActualBodyAndHeading(t *testing.T) {
	for _, answer := range []string{
		"## 管理者摘要\n",
		"## 管理者摘要\n\n## 来源\n正文",
	} {
		if count, present := newsSummaryVisibleCharCount(answer); present || count != 0 {
			t.Fatalf("empty summary was treated as present: count=%d answer=%q", count, answer)
		}
	}
	answer := "条目提到管理者摘要工具,但这里不是摘要。\n\n## 摘要\n真正摘要"
	if count, present := newsSummaryVisibleCharCount(answer); !present || count != len([]rune("真正摘要")) {
		t.Fatalf("summary heading was confused by prose mention: present=%v count=%d", present, count)
	}
	for _, answer := range []string{
		"## 管理者摘要：内联摘要",
		"## 🧭 管理者摘要（150字以内）\r\n\r\n跨行摘要",
		"## 150 字管理者摘要\n今日重点：资本、治理与供应链。",
		"## 二、管理者摘要(150字内)\n中文章节编号也应识别。",
		"## 2. 管理者摘要\n阿拉伯章节编号也应识别。",
	} {
		if count, present := newsSummaryVisibleCharCount(answer); !present || count <= 0 {
			t.Fatalf("valid inline/CRLF summary was missed: present=%v count=%d answer=%q", present, count, answer)
		}
	}
}

func TestEnforceCurrentNewsSummaryLimitPreservesFollowingHeadingAndFlattensMarkdown(t *testing.T) {
	input := "整理今天的3条科技新闻并给来源，最后给一个100字以内的管理者摘要。"
	body := strings.Repeat("甲", 70) + "。[来源](https://example.com/very/long/source) **" + strings.Repeat("乙", 60) + "**。"
	answer := "## 管理者摘要（100字以内）\n\n" + body + "\n\n## 来源\n- https://example.com/source"
	normalized, changed, _, after := enforceCurrentNewsSummaryLimit(input, answer)
	if !changed || after > 100 || !strings.Contains(normalized, "\n\n## 来源\n") {
		t.Fatalf("summary compaction damaged following heading: changed=%v after=%d output=%q", changed, after, normalized)
	}
	summaryEnd := strings.Index(normalized, "\n\n## 来源")
	summary := normalized[:summaryEnd]
	if strings.Contains(summary, "](") || strings.Contains(summary, "http") || strings.Count(summary, "**")%2 != 0 {
		t.Fatalf("summary compaction left broken Markdown: %q", summary)
	}
}

func TestCompactNewsSummaryRemovesDanglingDeclaredCount(t *testing.T) {
	for _, prefix := range []string{"今日三条主线", "今日三大主线", "今日 8 条热点覆盖三类信号"} {
		body := prefix + "：(1)算力链发生重要变化；(2)AI竞争升级并进入司法阶段；(3)平台治理与用户权利边界继续调整，管理层需关注后续监管。"
		compacted := compactNewsSummaryText(body, 45)
		if strings.Contains(compacted, prefix) {
			t.Fatalf("compacted summary kept an unfulfilled declared count: %q", compacted)
		}
		if count := len([]rune(newsSummaryFormatPattern.ReplaceAllString(compacted, ""))); count > 45 {
			t.Fatalf("compacted summary has %d visible chars, want <=45: %q", count, compacted)
		}
		if !strings.HasSuffix(compacted, "。") || strings.Contains(compacted, "…") {
			t.Fatalf("mid-summary compaction should close at a complete clause: %q", compacted)
		}
	}
}

func TestCompactNewsSummaryUsesTopLevelClauseInsteadOfCuttingMidParenthesis(t *testing.T) {
	body := "今日热点集中在三条主线:AI算力驱动存储超级周期(SK海力士美股史上最大外国IPO、南亚Q2毛利率79.5%),头部AI巨头法律与治理摩擦升级(Apple诉OpenAI窃密、Meta撤回Instagram AI deepfake),以及中美在硬科技前沿持续对位(中国首次回收轨道级可复用火箭、长鑫科技承销团亮相、小米汽车中大型SUV申报、U-Boot 13年漏洞曝光)。建议关注供应链估值与AI合规两条传导链。"
	compacted := compactNewsSummaryText(body, 150)
	if count := len([]rune(newsSummaryFormatPattern.ReplaceAllString(compacted, ""))); count > 150 {
		t.Fatalf("compacted summary has %d visible chars, want <=150: %q", count, compacted)
	}
	if strings.Contains(compacted, "三条主线") || !strings.HasSuffix(compacted, "。") {
		t.Fatalf("compacted summary kept a false count or incomplete ending: %q", compacted)
	}
	if strings.Contains(compacted, "长鑫科技承。") || !strings.Contains(compacted, "deepfake") {
		t.Fatalf("summary was cut mid-claim instead of using the last top-level clause: %q", compacted)
	}
	answer := "## 管理者摘要（150字以内）\n" + compacted
	if !currentNewsSummaryDelimitersBalanced(answer) {
		t.Fatalf("compacted summary has unbalanced delimiters: %q", compacted)
	}
}

func TestCompactNewsSummaryNeutralizesDeclaredThemeCountAfterClauseCut(t *testing.T) {
	body := "今日 8 条呈现三条主线：资本端，SK 海力士完成大型 IPO，存储价格变化值得关注；合规与产品端，Apple 起诉 OpenAI，Meta 调整 AI 功能；产业端，火箭回收与固件安全出现新进展。" + strings.Repeat("补充", 80)
	compacted := compactNewsSummaryText(body, 150)
	if strings.Contains(compacted, "三条主线") || !strings.HasPrefix(compacted, "今日重点：") {
		t.Fatalf("compaction retained an unfulfilled declared theme count: %q", compacted)
	}
	if !strings.HasSuffix(compacted, "。") || len([]rune(newsSummaryFormatPattern.ReplaceAllString(compacted, ""))) > 150 {
		t.Fatalf("compacted summary is incomplete or over limit: %q", compacted)
	}
}

func TestCompactNewsSummaryDoesNotTreatDecimalOrAcronymDotAsSentenceEnd(t *testing.T) {
	body := "今日重点：存储厂商毛利率达到79.5%并继续变化，U.S.市场出现新的资本动作，平台治理也有新的进展，后续仍需结合权威来源持续观察。" + strings.Repeat("补充", 80)
	compacted := compactNewsSummaryText(body, 70)
	if strings.HasSuffix(compacted, "79.") || strings.HasSuffix(compacted, "U.") || !strings.HasSuffix(compacted, "。") {
		t.Fatalf("decimal or acronym dot was mistaken for a sentence boundary: %q", compacted)
	}
}

func TestCompactNewsSummaryFallbackHonorsSmallLimit(t *testing.T) {
	compacted := compactNewsSummaryText(strings.Repeat("甲", 40), 10)
	if count := len([]rune(newsSummaryFormatPattern.ReplaceAllString(compacted, ""))); count > 10 {
		t.Fatalf("fallback has %d visible chars, want <=10: %q", count, compacted)
	}
	if compacted != "重点见上文。" {
		t.Fatalf("unsafe no-boundary input did not use complete fallback: %q", compacted)
	}
}

func TestResearchDeliveryIssuesRejectsSummaryTruncatedInsideParenthesis(t *testing.T) {
	answer := strings.Builder{}
	for item := 1; item <= 8; item++ {
		fmt.Fprintf(&answer, "## %d. Event\n事件发生时间：不详\n媒体发布时间：2026-07-11\n核心事实：fact\n为什么重要：impact\n来源：https://publisher%d.example/news/%d\n", item, item, item)
		if item <= 3 {
			fmt.Fprintf(&answer, "第二来源：https://cross%d.example/report/%d\n", item, item)
		}
	}
	answer.WriteString("## 管理者摘要（150字以内）\n今日重点：算力与治理(存储、诉讼。")
	issues := strings.Join(researchDeliveryIssues(newsDeliveryPrompt, answer.String()), " | ")
	if !strings.Contains(issues, "unclosed delimiter") {
		t.Fatalf("truncated summary passed delivery contract: %s", issues)
	}
}

func TestResearchDeliveryIssuesRejectsGenericWhyItMattersPlaceholder(t *testing.T) {
	var answer strings.Builder
	for item := 1; item <= 8; item++ {
		why := "分析：该事实为何重要。"
		if item == 4 {
			why = "分析：该进展的实际影响仍需后续权威披露验证。"
		}
		fmt.Fprintf(&answer, "## %d. Event\n事件发生时间：不详\n媒体发布时间：2026-07-11\n核心事实：fact\n为什么重要：%s\n来源：https://publisher%d.example/news/%d\n", item, why, item, item)
		if item <= 3 {
			fmt.Fprintf(&answer, "第二来源：https://cross%d.example/news/%d\n", item, item)
		}
	}
	answer.WriteString("## 管理者摘要\n摘要。")
	issues := strings.Join(researchDeliveryIssues(newsDeliveryPrompt, answer.String()), " | ")
	if !strings.Contains(issues, "item 4 why-it-matters field is only a generic verification placeholder") {
		t.Fatalf("generic why-it-matters non-answer passed: %s", issues)
	}
}

func TestEnforceCurrentNewsSummaryLimitCompletesDanglingFragmentBelowLimit(t *testing.T) {
	input := "整理今天的3条科技新闻并给来源，最后给一个150字以内的管理者摘要。"
	answer := "## 管理者摘要（150字以内）\n今日三条主线：①算力投资继续；②平台监管升级；③某公司下一款产品将于…\n\n## 来源\n- https://example.com/news/1"
	normalized, changed, before, after := enforceCurrentNewsSummaryLimit(input, answer)
	if !changed || before > 150 || after >= before {
		t.Fatalf("dangling summary was not completed: changed=%v before=%d after=%d output=%q", changed, before, after, normalized)
	}
	if strings.Contains(normalized, "三条主线") || strings.Contains(normalized, "…") || strings.Contains(normalized, "③") {
		t.Fatalf("summary retained a false count or fragment: %q", normalized)
	}
	if !strings.Contains(normalized, "②平台监管升级。\n\n## 来源") {
		t.Fatalf("summary did not close at the prior complete clause or damaged the next heading: %q", normalized)
	}
}

func TestCurrentNewsDuplicateIssuesFlagsUndifferentiatedCrossReference(t *testing.T) {
	answer := "## 1. Launch\n事实。\n## 2. Fuel angle\n与第1条协同，是同一次发布的另一角度。"
	issues := strings.Join(currentNewsDuplicateIssues(answer), " ")
	if !strings.Contains(issues, "item 2 explicitly overlaps item 1") {
		t.Fatalf("explicit event overlap was not detected: %s", issues)
	}
	distinct := "## 1. Launch\n事实。\n## 2. Separate policy\n不同于第1条，这是独立事件。"
	if issues := currentNewsDuplicateIssues(distinct); len(issues) != 0 {
		t.Fatalf("explicitly distinct event was flagged: %v", issues)
	}
	comparison := "## 1. Overseas IPO\n事实。\n## 2. Domestic listing\n与第1条形成对照，两家公司分别上市。"
	if issues := currentNewsDuplicateIssues(comparison); len(issues) != 0 {
		t.Fatalf("comparison between distinct events was flagged: %v", issues)
	}
}

func TestCurrentNewsBundledEventIssuesFlagsExplicitMultiEventSlots(t *testing.T) {
	slashBundled := "## 1. OpenAI推出智能体 / COO因病离职\n核心事实：两件事同日披露。"
	if issues := currentNewsBundledEventIssues(slashBundled); len(issues) != 1 {
		t.Fatalf("slash-bundled events were not detected: %v", issues)
	}
	sameDayBundled := "## 8. Meta关停AI功能（FCC同时打击DJI规避禁令）\n核心事实：Meta关闭功能；同日，FCC启动执法打击。"
	if issues := currentNewsBundledEventIssues(sameDayBundled); len(issues) != 1 {
		t.Fatalf("same-day bundled events were not detected: %v", issues)
	}
	singleEvent := "## 1. Apple起诉OpenAI窃取AI/硬件商业秘密\n核心事实：Apple提起诉讼并发布声明。"
	if issues := currentNewsBundledEventIssues(singleEvent); len(issues) != 0 {
		t.Fatalf("one event with a slash in a term was falsely flagged: %v", issues)
	}
	dualPublisherTimes := "## 2. SK海力士IPO募资265亿美元\n媒体发布时间：TechCrunch 17:17；同日 The Verge 14:54\n核心事实：SK海力士完成IPO，报道同时披露建厂要求。"
	if issues := currentNewsBundledEventIssues(dualPublisherTimes); len(issues) != 0 {
		t.Fatalf("second-source publication times were falsely flagged: %v", issues)
	}
	productLifecycle := "## 4. Meta下线Instagram AI生图功能\n媒体发布时间：TechCrunch 23:55；同日 The Verge 19:56\n核心事实：Meta本周发布该功能，遭反弹后宣布关闭。"
	if issues := currentNewsBundledEventIssues(productLifecycle); len(issues) != 0 {
		t.Fatalf("two actions in one product lifecycle were falsely flagged: %v", issues)
	}
	roundupTail := "## 6. 德国大众拟大裁员\n核心事实：大众拟裁员12万人；同日36氪还提到百亿私募数量创新高、高盛谈套利交易。"
	if issues := currentNewsBundledEventIssues(roundupTail); len(issues) != 1 {
		t.Fatalf("roundup tail with unrelated headlines was not detected: %v", issues)
	}
	sameWeekPersonnel := "## 2. OpenAI推出ChatGPT智能体\n核心事实：OpenAI推出智能体，与COO离职等高管调整同周发生。"
	if issues := currentNewsBundledEventIssues(sameWeekPersonnel); len(issues) != 1 {
		t.Fatalf("same-week unrelated personnel event was not detected: %v", issues)
	}
	slashVisit := "## 8. 大众裁员 / 三星李在镕月底访问英伟达\n核心事实：两条商业消息。"
	if issues := currentNewsBundledEventIssues(slashVisit); len(issues) != 1 {
		t.Fatalf("slash-bundled layoff and executive visit were not detected: %v", issues)
	}
	visitMetric := "## 1. 平台发布访问量报告 / CEO专访解读\n核心事实：同一份报告的解读。"
	if issues := currentNewsBundledEventIssues(visitMetric); len(issues) != 0 {
		t.Fatalf("traffic metric or interview was mistaken for a visit event: %v", issues)
	}
	sameActionSameWeek := "## 3. A公司发布模型\n核心事实：A公司发布模型，与B公司发布芯片同周发生。"
	if issues := currentNewsBundledEventIssues(sameActionSameWeek); len(issues) != 1 {
		t.Fatalf("same-week events using the same action verb were missed: %v", issues)
	}
	yearOverYear := "## 4. 平台发布季度报告\n核心事实：平台发布报告，访问量与去年同期相比增长20%。"
	if issues := currentNewsBundledEventIssues(yearOverYear); len(issues) != 0 {
		t.Fatalf("year-over-year comparison was mistaken for a time-window bundle: %v", issues)
	}
}

func TestStripCurrentNewsExplicitBundledTailsKeepsPrimaryEvent(t *testing.T) {
	answer := "## 8. OpenAI COO因病离职\n- 核心事实：36氪列出OpenAI COO因病离职，与苹果诉讼、ChatGPT Work推出同期发生\n"
	cleaned, changed := stripCurrentNewsExplicitBundledTails(answer)
	if changed != 1 || !strings.Contains(cleaned, "COO因病离职。") || strings.Contains(cleaned, "苹果诉讼") || strings.Contains(cleaned, "Work推出") {
		t.Fatalf("explicit bundle tail was not safely removed: changed=%d output=%q", changed, cleaned)
	}
	comparison := "## 1. 平台发布报告\n- 核心事实：平台发布报告，访问量与去年同期相比增长20%。\n"
	if got, changed := stripCurrentNewsExplicitBundledTails(comparison); changed != 0 || got != comparison {
		t.Fatalf("year-over-year comparison was modified: changed=%d output=%q", changed, got)
	}
	roundupTail := "## 1. SK海力士IPO\n- 核心事实：SK海力士完成募资；36氪同一聚合标题另外点名 OpenAI 推出 ChatGPT 智能体。\n"
	cleanedRoundup, changedRoundup := stripCurrentNewsExplicitBundledTails(roundupTail)
	if changedRoundup != 1 || !strings.Contains(cleanedRoundup, "SK海力士完成募资。") || strings.Contains(cleanedRoundup, "OpenAI") {
		t.Fatalf("unrelated roundup-title tail was not stripped: changed=%d output=%q", changedRoundup, cleanedRoundup)
	}
}

func TestResearchNumberedItemsAcceptLeadingCrossCheckStars(t *testing.T) {
	answer := "### ★ 1. First cross-checked event\n- 来源：https://publisher-a.example/1\n\n" +
		"### ⭐ 2. Second cross-checked event\n- 来源：https://publisher-b.example/2\n\n" +
		"### 3. Ordinary event\n- 来源：https://publisher-c.example/3\n"
	if got := countResearchItems(answer); got != 3 {
		t.Fatalf("starred numbered headings counted as %d items, want 3", got)
	}
	sections := numberedResearchItemSections(answer)
	if len(sections) != 3 || sections["1"] == "" || sections["2"] == "" || sections["3"] == "" {
		t.Fatalf("starred numbered headings were not sectioned: %#v", sections)
	}
}

func TestCurrentNewsFreshnessConvertsPublicationTimesToShanghai(t *testing.T) {
	tests := []struct {
		name  string
		value string
		fresh bool
	}{
		{name: "previous UTC date crosses midnight", value: "2026-07-10 17:17 UTC", fresh: true},
		{name: "previous UTC date remains previous local day", value: "2026-07-10 15:46 UTC", fresh: false},
		{name: "previous Shanghai date is stale", value: "2026-07-10 07:43 +0800", fresh: false},
		{name: "previous EDT date crosses midnight", value: "2026-07-10 18:33 EDT", fresh: true},
		{name: "explicit current Shanghai date", value: "2026-07-11 09:24 +08:00", fresh: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			answer := fmt.Sprintf("## 1. Event\n媒体发布时间：%s\n", test.value)
			issues := currentNewsFreshnessIssues(newsDeliveryPrompt, answer)
			if test.fresh && len(issues) != 0 {
				t.Fatalf("fresh timestamp was rejected: %v", issues)
			}
			if !test.fresh && (len(issues) != 1 || !strings.Contains(issues[0], "does not resolve to target local date 2026-07-11")) {
				t.Fatalf("stale timestamp was not rejected: %v", issues)
			}
		})
	}
}

func TestCurrentNewsFreshnessRejectsUnparseablePublicationTime(t *testing.T) {
	answer := "## 1. Event\n媒体发布时间：稍早时候（具体日期不详）\n"
	issues := currentNewsFreshnessIssues(newsDeliveryPrompt, answer)
	if len(issues) != 1 || !strings.Contains(issues[0], "cannot be parsed") {
		t.Fatalf("unparseable media publication time passed freshness validation: %v", issues)
	}
}

func TestCurrentNewsFreshnessAcceptsTargetYearImplicitMonthDayTimes(t *testing.T) {
	answer := "## 1. Event\n媒体发布时间：TechCrunch 07-11 05:00 / The Verge 07-11 06:33\n"
	if issues := currentNewsFreshnessIssues(newsDeliveryPrompt, answer); len(issues) != 0 {
		t.Fatalf("target-year implicit month/day timestamps should be accepted: %#v", issues)
	}
}

func TestCurrentNewsFreshnessDoesNotReinterpretExplicitOldYear(t *testing.T) {
	answer := "## 1. Event\n媒体发布时间：2025-07-11 09:24 +0800\n"
	issues := currentNewsFreshnessIssues(newsDeliveryPrompt, answer)
	if len(issues) == 0 {
		t.Fatal("an explicit old year must not be reinterpreted using the target year")
	}
}

func TestResearchDeliveryBundledItemPatchTargetsOnlyLocalDefects(t *testing.T) {
	issues := []string{
		"item 6 explicitly bundles multiple underlying events; keep exactly one event in each numbered item",
		"item 4 explicitly bundles multiple underlying events; keep exactly one event in each numbered item",
	}
	items, ok := researchDeliveryBundledItemNumbers(issues)
	if !ok || fmt.Sprint(items) != "[4 6]" {
		t.Fatalf("unexpected local patch targets: ok=%v items=%v", ok, items)
	}
	if _, ok := researchDeliveryBundledItemNumbers(append(issues, "missing requested summary")); ok {
		t.Fatal("mixed delivery defects must not use the bundle-only local patch")
	}

	draft := `开场

## 1. 保留事件
核心事实：保留一。

## 2. 混合事件
核心事实：主事件；同日其他要点：无关事件。
来源：https://publisher.example/news/main-event

## 3. 继续保留
核心事实：保留三。

## 管理者摘要
摘要保留。`
	patch := `## 2. 混合事件
事件发生时间：2026-07-11
媒体发布时间：2026-07-11
核心事实：只保留主事件。
为什么重要：影响。
来源：https://publisher.example/news/main-event`
	merged, applied := applyResearchDeliveryItemPatch(newsDeliveryPrompt, draft, patch, []int{2})
	if !applied {
		t.Fatal("valid local item patch was rejected")
	}
	for _, want := range []string{"开场", "保留一", "只保留主事件", "保留三", "摘要保留"} {
		if !strings.Contains(merged, want) {
			t.Fatalf("merged draft lost %q: %s", want, merged)
		}
	}
	if strings.Contains(merged, "无关事件") || countResearchItems(merged) != 3 {
		t.Fatalf("local patch did not replace exactly one numbered section: %s", merged)
	}
	if got := buildResearchDeliveryItemPatchPrompt([]int{2})["content"].(string); !strings.Contains(got, "only numbered item(s) 2") || !strings.Contains(got, "Do not call tools") {
		t.Fatalf("local patch prompt is not sufficiently bounded: %s", got)
	}
	compact := buildResearchDeliveryItemPatchMessages([]map[string]interface{}{
		{"role": "system", "content": "base policy"},
		{"role": "tool", "content": strings.Repeat("large feed", 1000)},
	}, newsDeliveryPrompt, draft, []int{2})
	if len(compact) != 4 || compact[0]["content"] != "base policy" || strings.Contains(fmt.Sprint(compact), "large feed") {
		t.Fatalf("local patch context retained large tool evidence: %#v", compact)
	}
	if _, ok := applyResearchDeliveryItemPatch(newsDeliveryPrompt, draft, "<think>2. 伪条目</think>\n"+patch, []int{2}); ok {
		t.Fatal("reasoning-tagged patch must not be spliced into the user-facing draft")
	}
	if _, ok := applyResearchDeliveryItemPatch(newsDeliveryPrompt, draft, patch+"\n\n## 2. 重复目标\n核心事实：重复。", []int{2}); ok {
		t.Fatal("duplicate target sections must not be silently accepted")
	}
	changedURL := strings.ReplaceAll(patch, "https://publisher.example/news/main-event", "https://other.example/news/different-event")
	if _, ok := applyResearchDeliveryItemPatch(newsDeliveryPrompt, draft, changedURL, []int{2}); ok {
		t.Fatal("local patch must preserve at least one original item citation")
	}
	addedURL := patch + "\n第二来源：https://other.example/news/different-event"
	if _, ok := applyResearchDeliveryItemPatch(newsDeliveryPrompt, draft, addedURL, []int{2}); ok {
		t.Fatal("local patch must not add a citation from another event")
	}
	mentionedFieldOnly := strings.ReplaceAll(patch, "事件发生时间：2026-07-11", "核心事实：事件发生时间尚未确认")
	if _, ok := applyResearchDeliveryItemPatch(newsDeliveryPrompt, draft, mentionedFieldOnly, []int{2}); ok {
		t.Fatal("a field name mentioned inside another field value must not satisfy the patch contract")
	}
}

func TestLastResearchItemDoesNotBorrowSummaryLinksOrCrossReferences(t *testing.T) {
	input := "整理今天8条热点，每条包含事件发生时间、媒体发布时间、核心事实、为什么重要和来源；最重要的3条再找第二来源核对；不要把同一事件算成多条；最后给管理者摘要。"
	var answer strings.Builder
	for item := 1; item <= 8; item++ {
		fmt.Fprintf(&answer, "## %d. Event\n事件发生时间：2026-07-11\n媒体发布时间：2026-07-11\n核心事实：fact\n为什么重要：impact\n", item)
		if item < 8 {
			fmt.Fprintf(&answer, "来源：https://publisher%d.example/news/item-%d\n", item, item)
		}
		if item <= 3 {
			fmt.Fprintf(&answer, "第二来源：https://cross%d.example/story/check-%d\n", item, item)
		}
	}
	answer.WriteString("\n## 管理者摘要\n与第2条协同说明市场变化。https://summary.example/news/manager-view\n")

	issues := strings.Join(researchDeliveryIssues(input, answer.String()), " ")
	if !strings.Contains(issues, "only 7/8 numbered item(s) contain an article URL") {
		t.Fatalf("item 8 borrowed the summary URL: %s", issues)
	}
	if strings.Contains(issues, "explicitly overlaps") {
		t.Fatalf("summary cross-reference was attributed to item 8: %s", issues)
	}
	section := numberedResearchItemSections(answer.String())["8"]
	if strings.Contains(section, "管理者摘要") || strings.Contains(section, "summary.example") {
		t.Fatalf("last item section swallowed summary: %q", section)
	}
}

func TestResearchDeliverySourceCountIgnoresDetachedAppendixLinks(t *testing.T) {
	input := "整理今天（2026-07-11）的8条科技热点，每条包含事件发生时间、媒体发布时间、核心事实、为什么重要和来源，最后给管理者摘要。"
	var answer strings.Builder
	for item := 1; item <= 8; item++ {
		urlItem := item
		if item == 8 {
			urlItem = 1
		}
		fmt.Fprintf(&answer, "## %d. Event\n事件发生时间：2026-07-11\n媒体发布时间：2026-07-11 09:00 +0800\n核心事实：fact\n为什么重要：impact\n来源：https://publisher.example/news/item-%d\n", item, urlItem)
	}
	// Even a deeper heading after the last item is a detached appendix, not a
	// sub-section whose URL item 8 may borrow.
	answer.WriteString("\n### 交叉核对附录\n补充来源：https://appendix.example/news/eighth-url\n")
	answer.WriteString("\n## 管理者摘要\n正文条目只有七个不同文章链接。")
	issues := strings.Join(researchDeliveryIssues(input, answer.String()), " ")
	if !strings.Contains(issues, "only 7 distinct article URL(s) inside numbered items; at least 8 required") {
		t.Fatalf("detached appendix URL incorrectly satisfied item source count: %s", issues)
	}
}

func TestResearchItemSectionStopsAtPlainBoldAndQuotedSummary(t *testing.T) {
	for _, summaryLine := range []string{
		"管理者摘要：结论。",
		"**管理者摘要**：结论。",
		"> ## 管理者摘要：结论。",
	} {
		answer := "## 8. Last event\r\n核心事实：事件。\r\n来源：https://publisher.example/news/last\r\n\r\n" + summaryLine + "\r\n"
		section := numberedResearchItemSections(answer)["8"]
		if strings.Contains(section, "管理者摘要") || strings.Contains(section, "结论") {
			t.Fatalf("last item swallowed %q summary: %q", summaryLine, section)
		}
	}
}

func TestResearchDeliveryIssuesRejectsEmptyOrMentionOnlyFields(t *testing.T) {
	var answer strings.Builder
	for item := 1; item <= 8; item++ {
		fmt.Fprintf(&answer, "## %d. Event\n事件发生时间：2026-07-11\n媒体发布时间：2026-07-11\n核心事实：fact\n为什么重要：impact\n来源：https://publisher%d.example/news/%d\n", item, item, item)
		if item <= 3 {
			fmt.Fprintf(&answer, "复核：https://cross%d.example/story/%d\n", item, item)
		}
	}
	answer.WriteString("## 管理者摘要\n摘要。")
	broken := strings.Replace(answer.String(), "事件发生时间：2026-07-11", "事件发生时间：\n核心事实：事件发生时间尚未确认", 1)
	issues := strings.Join(researchDeliveryIssues(newsDeliveryPrompt, broken), " ")
	if !strings.Contains(issues, "7/8 numbered item(s) contain requested field: 事件发生时间") {
		t.Fatalf("empty field or mention-only value passed delivery contract: %s", issues)
	}
}

func TestResearchDeliveryIssuesRequiresFieldsAndLinkInEveryNumberedItem(t *testing.T) {
	var answer strings.Builder
	for i := 1; i <= 8; i++ {
		fmt.Fprintf(&answer, "## %d. Event\n事件时间：2026-07-11\n媒体发布时间：2026-07-11\n", i)
		if i != 8 {
			answer.WriteString("核心事实：verified\n")
		}
		answer.WriteString("为什么重要：impact\n")
		if i != 7 {
			fmt.Fprintf(&answer, "来源：https://publisher%d.example/news/a-%d\n", i, i)
		}
		if i <= 3 {
			fmt.Fprintf(&answer, "复核：https://cross%d.example/story/b-%d\n", i, i)
		}
	}
	answer.WriteString("\n## 管理者摘要\n逐条字段与链接必须完整。")
	issues := strings.Join(researchDeliveryIssues(newsDeliveryPrompt, answer.String()), " ")
	for _, want := range []string{"7/8 numbered item(s) contain requested field: 核心事实", "7/8 numbered item(s) contain an article URL"} {
		if !strings.Contains(issues, want) {
			t.Fatalf("per-item delivery gap %q was not detected: %s", want, issues)
		}
	}
}

func TestCurrentNewsPreflightReconcileAndCompactAllowsCompleteCandidate(t *testing.T) {
	var answer strings.Builder
	var evidence strings.Builder
	evidence.WriteString("Source: https://publisher-feed.example/rss\n")
	for i := 1; i <= 8; i++ {
		primary := fmt.Sprintf("https://publisher%d.example/news/article-%d", i, i)
		fmt.Fprintf(&answer, "## %d. Event\n事件时间：2026-07-11\n媒体发布时间：2026-07-11\n核心事实：verified\n为什么重要：impact\n来源：%s\n", i, primary)
		fmt.Fprintf(&evidence, "[%s](%s)\n", primary, primary)
		if i <= 3 {
			cross := fmt.Sprintf("https://cross%d.example/report/check-%d", i, i)
			fmt.Fprintf(&answer, "第二来源：%s\n", cross)
			fmt.Fprintf(&evidence, "[%s](%s)\n", cross, cross)
		}
	}
	answer.WriteString("附加误拼链接：https://publisher7.example/news/article-backbacklash\n")
	answer.WriteString("\n## 管理者摘要（≤150 字）\n")
	answer.WriteString(strings.Repeat("甲", 100) + "。" + strings.Repeat("乙", 90) + "。")
	toolResult := toolResultForFinalization(
		ToolCall{Name: "social_trending", Arguments: map[string]interface{}{"source": "rss", "url": "https://publisher-feed.example/rss"}},
		evidence.String(),
		true,
	)
	cleaned, _, removed := reconcileExternalEvidenceURLs(answer.String(), []string{toolResult})
	if len(removed) != 1 || strings.Contains(cleaned, "backbacklash") {
		t.Fatalf("preflight did not remove unsupported typo URL: removed=%v", removed)
	}
	cleaned, changed, _, after := enforceCurrentNewsSummaryLimit(newsDeliveryPrompt, cleaned)
	if !changed || after > 150 {
		t.Fatalf("preflight did not compact summary: changed=%v after=%d", changed, after)
	}
	if issues := researchDeliveryIssues(newsDeliveryPrompt, cleaned); len(issues) != 0 {
		t.Fatalf("complete preflight candidate failed delivery: %v\n%s", issues, cleaned)
	}
}

func TestResearchDeliveryIssuesRejectsSamePublisherAsSecondSource(t *testing.T) {
	var answer strings.Builder
	for i := 1; i <= 8; i++ {
		fmt.Fprintf(&answer, "## %d. Event\n事件发生时间：today\n媒体发布时间：today\n核心事实：fact\n为什么重要：impact\n来源：https://publisher%d.example/news/a-%d\n", i, i, i)
		if i <= 3 {
			fmt.Fprintf(&answer, "第二来源：https://www.publisher%d.example/news/b-%d\n", i, i)
		}
	}
	answer.WriteString("## 管理者摘要\n摘要。")
	issues := strings.Join(researchDeliveryIssues(newsDeliveryPrompt, answer.String()), " ")
	if !strings.Contains(issues, "independent publisher domains") {
		t.Fatalf("same-publisher second sources passed delivery contract: %s", issues)
	}
}

func TestArticleCitationCoverageRejectsHomepagesFeedsAndNewsRedirects(t *testing.T) {
	answer := strings.Join([]string{
		"https://www.ithome.com/",
		"https://www.36kr.com/feed",
		"https://feeds.arstechnica.com/arstechnica/index",
		"https://news.google.com/rss/articles/opaque",
		"https://news.cctv.com/tech/",
		"https://publisher.example/politics/",
		"https://publisher.example/science/",
		"https://publisher.example/ai/",
		"https://publisher.example/markets/",
		"https://www.36kr.com/information/technology/",
		"https://techcrunch.com/category/artificial-intelligence/",
		"https://publisher.example/news/real-article",
		"https://publisher.example/science/2026/07/real-discovery",
	}, "\n")
	urls, publishers := newsArticleCitationCoverage(answer)
	if len(urls) != 2 || !strings.Contains(strings.Join(urls, " "), "news/real-article") || !strings.Contains(strings.Join(urls, " "), "science/2026/07/real-discovery") || len(publishers) != 1 {
		t.Fatalf("article coverage accepted non-article URLs: urls=%v publishers=%v", urls, publishers)
	}
}

func TestArticleCitationCoverageAcceptsQueryIdentifiedPermalinks(t *testing.T) {
	answer := strings.Join([]string{
		"https://publisher.example/?p=12345",
		"https://publisher.example/news?id=67890",
		"https://publisher.example/",
		"https://publisher.example/news",
	}, "\n")
	urls, _ := newsArticleCitationCoverage(answer)
	if len(urls) != 2 || !strings.Contains(strings.Join(urls, " "), "p=12345") || !strings.Contains(strings.Join(urls, " "), "id=67890") {
		t.Fatalf("query-identified article permalinks were not recognized: %v", urls)
	}
}

func TestResearchDeliveryRepairIsRewriteOnly(t *testing.T) {
	message := buildResearchDeliveryRepairPrompt([]string{"missing citations"})
	content, _ := message["content"].(string)
	for _, want := range []string{"only delivery rewrite", "Do not call any tool", "use only exact URLs already present"} {
		if !strings.Contains(content, want) {
			t.Fatalf("delivery repair prompt missing %q: %s", want, content)
		}
	}
	if got := appendResearchDeliveryFailure("partial answer", nil); !strings.Contains(got, "final evidence repair did not complete") {
		t.Fatalf("empty issue fallback was ambiguous: %s", got)
	}
}
