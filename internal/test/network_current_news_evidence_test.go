package app

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

const currentNewsEvidenceTestInput = "整理今天（2026-07-11）值得关注的8条科技热点，每条包含媒体发布时间和来源链接。"

func TestParseCurrentNewsPublishedTimeConvertsToShanghai(t *testing.T) {
	tests := []struct {
		name  string
		raw   string
		fresh bool
	}{
		{name: "previous UTC date after cutoff", raw: "Fri, 10 Jul 2026 17:17:00 +0000", fresh: true},
		{name: "previous UTC date before cutoff", raw: "Fri, 10 Jul 2026 15:46:18 +0000", fresh: false},
		{name: "previous domestic day", raw: "2026-07-10 18:23:02 +0800", fresh: false},
		{name: "previous EDT evening", raw: "2026-07-10 18:33 EDT", fresh: true},
		{name: "Chinese month GMT", raw: "周五, 10 7月 2026 23:36:00 GMT", fresh: true},
	}
	target, ok := requestedCurrentNewsLocalDate(currentNewsEvidenceTestInput)
	if !ok {
		t.Fatal("target date was not detected")
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			parsed, ok := parseCurrentNewsPublishedTime(test.raw)
			if !ok {
				t.Fatalf("failed to parse %q", test.raw)
			}
			if got := sameCurrentNewsLocalDate(parsed, target); got != test.fresh {
				t.Fatalf("fresh=%v, want %v; parsed=%s", got, test.fresh, parsed)
			}
		})
	}
}

func TestCurrentNewsPublishedTimePrefersAtomPublishedAndIgnoresUpdated(t *testing.T) {
	block := "📅 Published: 2026-07-10T21:36:51Z\n🔄 Updated: 2026-07-10T22:33:47Z"
	parsed, ok := currentNewsPublishedTime(block)
	if !ok || parsed.UTC().Format(time.RFC3339) != "2026-07-10T21:36:51Z" {
		t.Fatalf("Atom published time was not preserved: parsed=%v ok=%v", parsed, ok)
	}
	if _, ok := currentNewsPublishedTime("🔄 Updated: 2026-07-10T22:33:47Z"); ok {
		t.Fatal("Atom updated-only timestamp was misclassified as publication time")
	}
}

func TestNormalizeCurrentNewsUnsupportedEventTimesDoesNotUseReportingDate(t *testing.T) {
	appleURL := "https://publisher.example/apple-openai"
	movieURL := "https://publisher.example/movie-release"
	roundupURL := "https://publisher.example/daily-roundup"
	record := toolResultForFinalization(
		ToolCall{Name: "social_trending", Arguments: map[string]interface{}{"source": "rss", "url": "https://publisher.example/feed"}},
		"Source: https://publisher.example/feed\n\n### 1. Apple sues OpenAI\n["+appleURL+"]("+appleURL+")\n📅 Published: 2026-07-10T21:00:29Z\nApple alleges theft of hardware secrets.\n\n### 2. Movie opens today\n["+movieURL+"]("+movieURL+")\n📅 Published: 2026-07-11T07:42:13+08:00\nThe movie officially opens today.\n\n### 3. Daily roundup names an IPO\n["+roundupURL+"]("+roundupURL+")\n📅 Published: 2026-07-11T09:24:00+08:00\n[CURRENT_NEWS_ROUNDUP_FILTER] Only title events are eligible for 2026-07-11.",
		true,
	)
	answer := "1. Apple诉OpenAI\n- 事件发生时间：2026-07-10（美东报道日，原事件时间未披露）\n- 来源：" + appleURL + "\n\n" +
		"2. Movie release\n- 事件发生时间：2026-07-11（上映日）\n- 来源：" + movieURL + "\n\n" +
		"3. IPO roundup title\n- 事件发生时间：2026-07-11（具体时点未给出）\n- 来源：" + roundupURL
	input := currentNewsEvidenceTestInput + "每条还必须包含事件发生时间。"
	normalized, changed := normalizeCurrentNewsUnsupportedEventTimes(input, answer, []string{record})
	if changed != 2 || !strings.Contains(normalized, "1. Apple诉OpenAI\n- 事件发生时间： 不详（当前证据未披露事件发生时间）") {
		t.Fatalf("unsupported reporting date was not normalized: changed=%d\n%s", changed, normalized)
	}
	if !strings.Contains(normalized, "2. Movie release\n- 事件发生时间：2026-07-11（上映日）") {
		t.Fatalf("evidence-supported event date was removed:\n%s", normalized)
	}
	if !strings.Contains(normalized, "3. IPO roundup title\n- 事件发生时间： 不详（当前证据未披露事件发生时间）") {
		t.Fatalf("internal roundup target date was mistaken for event evidence:\n%s", normalized)
	}
}

func TestNormalizeCurrentNewsEventTimeRejectsPublicationDayWhenRowSaysLastNight(t *testing.T) {
	articleURL := "https://publisher.example/car-filing"
	record := toolResultForFinalization(
		ToolCall{Name: "social_trending", Arguments: map[string]interface{}{"source": "rss", "url": "https://publisher.example/feed"}},
		"Source: https://publisher.example/feed\n\n### 1. New car photos\n["+articleURL+"]("+articleURL+")\n📅 Published: 2026-07-11T08:45:43+08:00\nIT之家 7 月 11 日消息，今日博主发布实车图，该车昨晚已经亮相工信部申报公示。",
		true,
	)
	answer := "1. 新车工信部申报\n- 事件发生时间：2026-07-11（工信部公示）\n- 来源：" + articleURL
	input := currentNewsEvidenceTestInput + "每条还必须包含事件发生时间。"
	normalized, changed := normalizeCurrentNewsUnsupportedEventTimes(input, answer, []string{record})
	if changed != 1 || !strings.Contains(normalized, "事件发生时间： 不详（当前证据未披露事件发生时间）") {
		t.Fatalf("publication day was attached to a previous-night action: changed=%d\n%s", changed, normalized)
	}
}

func TestNormalizeCurrentNewsEvidenceScopedClaimsDowngradesUnsupportedScope(t *testing.T) {
	securityURL := "https://publisher.example/security"
	listingURL := "https://publisher.example/listing"
	memoryURL := "https://publisher.example/memory"
	record := toolResultForFinalization(
		ToolCall{Name: "social_trending", Arguments: map[string]interface{}{"source": "rss", "url": "https://publisher.example/feed"}},
		"Source: https://publisher.example/feed\n\n### 1. Security report\n["+securityURL+"]("+securityURL+")\n📅 Published: 2026-07-11\n两个漏洞可导致任意代码执行。\n\n### 2. Listing team\n["+listingURL+"]("+listingURL+")\n📅 Published: 2026-07-11\n国产存储第一股公布承销团阵容。\n\n### 3. Memory results\n["+memoryURL+"]("+memoryURL+")\n📅 Published: 2026-07-11\nDRAM 销售量与 Q1 持平，平均售价环比上升。",
		true,
	)
	answer := "1. Security\n- 核心事实：2 个漏洞可远程执行任意代码。\n- 为什么重要：分析：将重塑 API 安全格局。\n- 来源：" + securityURL + "\n\n" +
		"2. Listing\n- 核心事实：多家媒体称其公布承销团。\n- 为什么重要：分析：将重塑 DRAM/NAND 定价。\n- 来源：" + listingURL + "\n\n" +
		"3. Memory\n- 核心事实：销量持平、售价上升。\n- 为什么重要：分析：量增与价升同步延续。\n- 来源：" + memoryURL
	normalized, changed := normalizeCurrentNewsEvidenceScopedClaims(currentNewsEvidenceTestInput, answer, []string{record})
	if changed < 5 {
		t.Fatalf("changed=%d, want all unsupported scope claims downgraded:\n%s", changed, normalized)
	}
	for _, forbidden := range []string{"远程执行任意代码", "多家媒体", "DRAM/NAND", "量增与价升", "重塑 API"} {
		if strings.Contains(normalized, forbidden) {
			t.Fatalf("unsupported wording %q survived:\n%s", forbidden, normalized)
		}
	}
	if !strings.Contains(normalized, "可导致任意代码执行") {
		t.Fatalf("safe deterministic capability downgrade missing:\n%s", normalized)
	}
	for _, want := range []string{"风险评估与修复安排", "承销信息关系到发行推进", "融资进度、经营表现和市场定价"} {
		if !strings.Contains(normalized, want) {
			t.Fatalf("evidence-safe importance fallback missing %q:\n%s", want, normalized)
		}
	}
	if strings.Contains(normalized, "实际影响仍需后续权威披露验证") {
		t.Fatalf("generic non-answer survived importance downgrade:\n%s", normalized)
	}
}

func TestCurrentNewsSafeImportanceFallbackExplainsEventClass(t *testing.T) {
	tests := []struct {
		section string
		want    string
	}{
		{section: "核心事实：企业公布承销团阵容。", want: "发行推进、市场询价和后续定价"},
		{section: "核心事实：企业 IPO 募资并披露开盘价。", want: "资本体量和市场定价"},
		{section: "核心事实：轨道级火箭助推器完成回收。", want: "检验可重复使用能力"},
		{section: "核心事实：AI 数字化重现古代壁画，不做物理修复。", want: "不能替代实物保护"},
		{section: "核心事实：智能调度骑手配送 AED。", want: "更快到达现场"},
		{section: "核心事实：论文发现一种新的生命机制。", want: "后续验证和应用研究"},
	}
	for _, test := range tests {
		got := currentNewsSafeImportanceFallback(test.section)
		if !strings.Contains(got, test.want) || newsImportancePlaceholder.MatchString(strings.TrimSpace(strings.TrimPrefix(got, "分析："))) {
			t.Fatalf("event-class fallback is not meaningful for %q: %q", test.section, got)
		}
	}
}

func TestNormalizeCurrentNewsDiscardedCandidateNotesRemovesDetachedNinthEvent(t *testing.T) {
	var answer strings.Builder
	for item := 1; item <= 8; item++ {
		fmt.Fprintf(&answer, "## %d. Event\n- 核心事实：fact %d\n- 来源：https://publisher.example/news/%d\n\n", item, item, item)
	}
	answer.WriteString("> 备注：汇总标题亦点名 OpenAI 推出 ChatGPT 智能体，本表未单列，避免填充。\n\n---\n\n## 管理者摘要\n八条事件详见上文。")
	input := "整理今天（2026-07-11）的8条科技热点，每条给核心事实和来源，最后给管理者摘要。"

	normalized, removed := normalizeCurrentNewsDiscardedCandidateNotes(input, answer.String())
	if removed != 1 || strings.Contains(normalized, "ChatGPT") || strings.Contains(normalized, "未单列") {
		t.Fatalf("discarded candidate note survived: removed=%d\n%s", removed, normalized)
	}
	if countResearchItems(normalized) != 8 || !strings.Contains(normalized, "## 管理者摘要") {
		t.Fatalf("note removal damaged the numbered brief or summary:\n%s", normalized)
	}
}

func TestNormalizeCurrentNewsSummaryScopeRebuildsFromSelectedCoreFacts(t *testing.T) {
	urls := map[string]string{
		"sk1":     "https://publisher-one.com/news/sk-listing",
		"sk2":     "https://publisher-two.net/news/sk-listing",
		"apple1":  "https://publisher-one.com/news/apple-lawsuit",
		"apple2":  "https://publisher-two.net/news/apple-lawsuit",
		"rocket1": "https://publisher-one.com/news/rocket-recovery",
		"rocket2": "https://publisher-two.net/news/rocket-recovery",
		"meta":    "https://publisher-three.org/news/meta-instagram",
		"ddr5":    "https://publisher-three.org/news/ddr5-speed",
	}
	record := toolResultForFinalization(
		ToolCall{Name: "social_trending", Arguments: map[string]interface{}{"source": "rss", "url": "https://publisher-one.com/feed"}},
		"### 1. SK listing\n["+urls["sk1"]+"]("+urls["sk1"]+")\nSK Hynix listed in the US.\n\n"+
			"### 2. SK listing check\n["+urls["sk2"]+"]("+urls["sk2"]+")\nSK Hynix listed in the US.\n\n"+
			"### 3. Apple lawsuit\n["+urls["apple1"]+"]("+urls["apple1"]+")\nApple sued OpenAI.\n\n"+
			"### 4. Apple lawsuit check\n["+urls["apple2"]+"]("+urls["apple2"]+")\nApple sued OpenAI.\n\n"+
			"### 5. Rocket recovery\n["+urls["rocket1"]+"]("+urls["rocket1"]+")\nChina recovered an orbital booster.\n\n"+
			"### 6. Rocket recovery check\n["+urls["rocket2"]+"]("+urls["rocket2"]+")\nChina recovered an orbital booster.\n\n"+
			"### 7. Meta Instagram feature removed\n["+urls["meta"]+"]("+urls["meta"]+")\nMeta removed an Instagram feature.\n\n"+
			"### 8. DDR5 reaches 8600MT/s\n["+urls["ddr5"]+"]("+urls["ddr5"]+")\nDDR5 reached 8600MT/s.",
		true,
	)
	var answer strings.Builder
	for item := 1; item <= 8; item++ {
		fact := fmt.Sprintf("第%d条已核实事实", item)
		sources := fmt.Sprintf("https://selected.example/news/%d", item)
		switch item {
		case 1:
			fact = "SK 海力士在美上市"
			sources = urls["sk1"] + "；" + urls["sk2"]
		case 2:
			fact = "Apple 起诉 OpenAI，指控其工程师窃取硬件机密"
			sources = urls["apple1"] + "；" + urls["apple2"]
		case 3:
			fact = "中国航天企业宣布回收一枚可重复使用轨道级助推器"
			sources = urls["rocket1"] + "；" + urls["rocket2"]
		}
		fmt.Fprintf(&answer, "## %d. Event\n- 核心事实：%s。\n- 来源：%s\n\n", item, fact, sources)
	}
	answer.WriteString("## 管理者摘要（约120字）\n今日 8 条热点覆盖三类信号：Apple 与 Meta 博弈升级；SK 海力士登陆美股；国产 DDR5 冲刺 8600MT/s。")
	input := "整理今天（2026-07-11）的8条科技商业热点，每条给出核心事实和来源；最重要的3条再找第二来源核对；最后给一个150字以内的管理者摘要。"

	normalized, leaked := normalizeCurrentNewsSummaryScope(input, answer.String(), []string{record})
	if len(leaked) < 2 || strings.Contains(normalized, "Meta") || strings.Contains(normalized, "DDR5") || strings.Contains(normalized, "8600MT") {
		t.Fatalf("out-of-scope feed candidates survived summary rebuild: leaked=%v\n%s", leaked, normalized)
	}
	for _, want := range []string{"管理重点：", "SK 海力士在美上市", "Apple 起诉 OpenAI", "回收一枚可重复使用轨道级助推器"} {
		if !strings.Contains(normalized, want) {
			t.Fatalf("selected core fact missing from rebuilt summary (%q):\n%s", want, normalized)
		}
	}
	if count, present := newsSummaryVisibleCharCount(normalized); !present || count > 150 || !currentNewsSummaryDelimitersBalanced(normalized) {
		t.Fatalf("rebuilt summary violates delivery shape: present=%v count=%d\n%s", present, count, normalized)
	}

	original := answer.String()
	summaryAt := strings.Index(original, "## 管理者摘要")
	if summaryAt < 0 {
		t.Fatal("test fixture has no management summary")
	}
	scoped := original[:summaryAt] + "## 管理者摘要（约120字）\nSK 海力士在美上市；Apple 起诉 OpenAI；中国航天企业回收轨道级助推器。"
	unchanged, safeLeaks := normalizeCurrentNewsSummaryScope(input, scoped, []string{record})
	if len(safeLeaks) != 0 || unchanged != scoped {
		t.Fatalf("summary limited to selected items was modified: leaked=%v\n%s", safeLeaks, unchanged)
	}
}

func TestFilterCurrentNewsEvidenceDropsPreviousShanghaiDayRows(t *testing.T) {
	content := `Source: https://example.com/feed

## Example

### 1. after UTC cutoff
[https://example.com/2026/07/10/fresh](https://example.com/2026/07/10/fresh)
📅 Fri, 10 Jul 2026 17:17:00 +0000
fresh body

### 2. before UTC cutoff
[https://example.com/2026/07/10/stale](https://example.com/2026/07/10/stale)
📅 Fri, 10 Jul 2026 15:46:18 +0000
stale body

### 3. domestic previous day
[https://example.com/p/3?id=3](https://example.com/p/3?id=3)
📅 2026-07-10 18:23:02 +0800
old body`
	filtered, removed := filterCurrentNewsEvidence(currentNewsEvidenceTestInput, "social_trending", content)
	if removed != 2 {
		t.Fatalf("removed=%d, want 2\n%s", removed, filtered)
	}
	if !strings.Contains(filtered, "/fresh") || strings.Contains(filtered, "/stale") || strings.Contains(filtered, "domestic previous day") {
		t.Fatalf("freshness filter retained the wrong rows:\n%s", filtered)
	}
	if !strings.Contains(filtered, "Removed 2 row(s)") {
		t.Fatalf("missing deterministic filter receipt: %s", filtered)
	}
}

func TestFilterCurrentNewsToolCallEvidenceLeavesDirectArticleUntouched(t *testing.T) {
	article := `1. A numbered paragraph
Published: 2026-07-10 15:46 UTC
2. Another numbered paragraph`
	call := ToolCall{
		Name:      "web_fetch",
		Arguments: map[string]interface{}{"url": "https://example.com/2026/07/10/article"},
	}
	filtered, removed := filterCurrentNewsToolCallEvidence(currentNewsEvidenceTestInput, call, article)
	if removed != 0 || filtered != article {
		t.Fatalf("direct article was treated as a discovery listing: removed=%d output=%q", removed, filtered)
	}
}

func TestFilterCurrentNewsToolCallEvidenceFiltersWebFetchDiscoveryRows(t *testing.T) {
	listing := `1. stale result
   URL: https://example.com/old?id=1
   Published: 2026-07-10 07:43:00 +0800

2. fresh result
   URL: https://example.com/fresh?id=2
   Published: 2026-07-10 17:17:00 UTC`
	call := ToolCall{
		Name:      "web_fetch",
		Arguments: map[string]interface{}{"query": "today technology news"},
	}
	filtered, removed := filterCurrentNewsToolCallEvidence(currentNewsEvidenceTestInput, call, listing)
	if removed != 1 || strings.Contains(filtered, "old?id=1") || !strings.Contains(filtered, "fresh?id=2") {
		t.Fatalf("discovery listing freshness filter failed: removed=%d\n%s", removed, filtered)
	}
}

func TestFilterCurrentNewsEvidenceRestrictsFreshRoundupToTitle(t *testing.T) {
	content := `Source: https://www.36kr.com/feed

## 36氪

### 1. 9点1氪丨长鑫科技公布承销团；SK海力士上市；OpenAI推出智能体
[https://36kr.com/p/3890553690192384?f=rss](https://36kr.com/p/3890553690192384?f=rss)
📅 2026-07-11 09:24:06 +0800

今日热点导览
德国大众拟裁员12万人
OpenAI COO因病离职`
	filtered, changed := filterCurrentNewsEvidence(currentNewsEvidenceTestInput, "social_trending", content)
	if changed != 1 || !strings.Contains(filtered, "长鑫科技公布承销团") || !strings.Contains(filtered, "3890553690192384") {
		t.Fatalf("fresh roundup title evidence was lost: changed=%d\n%s", changed, filtered)
	}
	if strings.Contains(filtered, "大众拟裁员") || strings.Contains(filtered, "COO因病离职") {
		t.Fatalf("nested roundup bullets remained eligible:\n%s", filtered)
	}
}

func TestFilterCurrentNewsToolCallEvidenceRestrictsDirectRoundupBody(t *testing.T) {
	content := `# 9点1氪丨长鑫科技公布承销团；SK海力士上市；OpenAI推出智能体-36氪

**Source:** https://36kr.com/p/3890553690192384

# 9点1氪丨长鑫科技公布承销团；SK海力士上市；OpenAI推出智能体

## 今日热点导览
德国大众拟裁员12万人
OpenAI COO因病离职`
	call := ToolCall{Name: "web_fetch", Arguments: map[string]interface{}{"url": "https://36kr.com/p/3890553690192384"}}
	filtered, changed := filterCurrentNewsToolCallEvidence(currentNewsEvidenceTestInput, call, content)
	if changed != 1 || !strings.Contains(filtered, "Source:") || !strings.Contains(filtered, "长鑫科技公布承销团") {
		t.Fatalf("direct roundup title/source evidence was lost: changed=%d\n%s", changed, filtered)
	}
	if strings.Contains(filtered, "大众拟裁员") || strings.Contains(filtered, "COO因病离职") || strings.Contains(filtered, "今日热点导览") {
		t.Fatalf("direct roundup body remained eligible:\n%s", filtered)
	}
}

func TestCurrentNewsEvidenceFreshnessIssuesRejectsRelabelledOldURL(t *testing.T) {
	oldURL := "https://36kr.com/p/3889551814916614?f=rss"
	freshURL := "https://tech.example.com/2026/07/10/after-cutoff"
	record := `OPENKITTY_TOOL_RECORD={"tool":"social_trending","succeeded":true,"search_mode":"direct","source_url":"https://www.36kr.com/feed"}` + "\n" + fmt.Sprintf(`### 1. old
[%[1]s](%[1]s)
📅 2026-07-10 18:23:02 +0800

### 2. fresh
[%[2]s](%[2]s)
📅 Fri, 10 Jul 2026 17:17:00 +0000`, oldURL, freshURL)
	answer := fmt.Sprintf(`## 1. 模型把旧稿写成今天
- **事件时间**：2026-07-11
- **媒体发布时间**：2026-07-11 09:24 +0800
- **核心事实**：事实
- **为什么重要**：重要
- **来源**：%s

## 2. 真正按北京时间今天发布
- **事件时间**：2026-07-11
- **媒体发布时间**：2026-07-10 17:17 UTC
- **核心事实**：事实
- **为什么重要**：重要
- **来源**：%s`, oldURL, freshURL)
	issues := currentNewsEvidenceFreshnessIssues(currentNewsEvidenceTestInput, answer, []string{record})
	if len(issues) != 1 || !strings.Contains(issues[0], "item 1") || !strings.Contains(issues[0], oldURL) {
		t.Fatalf("unexpected freshness issues: %#v", issues)
	}
}

func TestNormalizeCurrentNewsMediaPublicationTimesUsesCitedEvidence(t *testing.T) {
	techCrunch := "https://techcrunch.com/2026/07/10/sk-hynix-raises-26-5b/"
	verge := "https://www.theverge.com/tech/964121/sk-hynix-debut"
	record := `OPENKITTY_TOOL_RECORD={"tool":"social_trending","succeeded":true,"search_mode":"direct","source_url":"https://techcrunch.com/feed/"}` + "\n" + fmt.Sprintf(`### 1. SK Hynix
[%[1]s](%[1]s)
📅 Fri, 10 Jul 2026 17:17:12 +0000

### 2. SK Hynix second source
[%[2]s](%[2]s)
📅 2026-07-10T14:54:55-04:00`, techCrunch, verge)
	answer := fmt.Sprintf("## 1. Event\n- 媒体发布时间：TechCrunch 2026-07-11 01:17 UTC\n- 来源：%s； %s\n", techCrunch, verge)
	normalized, changed := normalizeCurrentNewsMediaPublicationTimes(currentNewsEvidenceTestInput, answer, []string{record})
	if changed != 1 || !strings.Contains(normalized, "techcrunch.com 2026-07-11 01:17 +08:00") || !strings.Contains(normalized, "theverge.com 2026-07-11 02:54 +08:00") {
		t.Fatalf("media publication times were not rebuilt from evidence: changed=%d\n%s", changed, normalized)
	}
	if strings.Contains(normalized, "01:17 UTC") {
		t.Fatalf("model's mismatched timezone label survived normalization: %s", normalized)
	}
}

func TestCurrentNewsEvidenceReadyForSynthesisRequiresArticleAndPublisherMargin(t *testing.T) {
	const crossCheckInput = "整理今天（2026-07-11）的8条科技商业新闻，每条给来源；对最重要的3条再找第二来源核对。"
	buildRecord := func(host string, start, count int) string {
		record := `OPENKITTY_TOOL_RECORD={"tool":"social_trending","succeeded":true,"search_mode":"direct","source_url":"https://` + host + `/feed"}` + "\n"
		for item := start; item < start+count; item++ {
			record += fmt.Sprintf("https://%s/news/article-%d\n", host, item)
		}
		return record
	}
	onePublisher := []string{buildRecord("publisher-a.com", 1, 11)}
	if currentNewsEvidenceReadyForSynthesis(crossCheckInput, onePublisher) {
		t.Fatal("one publisher incorrectly satisfied a three-item cross-check margin")
	}
	twoPublishers := []string{buildRecord("publisher-a.com", 1, 8), buildRecord("publisher-b.com", 9, 3)}
	if !currentNewsEvidenceReadyForSynthesis(crossCheckInput, twoPublishers) {
		t.Fatal("sufficient multi-publisher filtered evidence did not close retrieval")
	}
	insufficient := []string{buildRecord("publisher-a.com", 1, 7), buildRecord("publisher-b.com", 8, 3)}
	if currentNewsEvidenceReadyForSynthesis(crossCheckInput, insufficient) {
		t.Fatal("too few article URLs incorrectly closed retrieval")
	}
}

func TestCurrentNewsEvidenceAssociationRejectsCitationAttachedToDifferentEvent(t *testing.T) {
	skURL := "https://techcrunch.com/2026/07/10/sk-hynix-raises-26-5b-in-us-ipo/"
	appleURL := "https://techcrunch.com/2026/07/10/apple-sues-openai-over-trade-secret-theft/"
	rss := toolResultForFinalization(
		ToolCall{Name: "social_trending", Arguments: map[string]interface{}{"source": "rss", "url": "https://techcrunch.com/feed/"}},
		"Source: https://techcrunch.com/feed/\n\n### 1. SK Hynix raises $26.5B in US IPO\n["+skURL+"]("+skURL+")\nPublished: 2026-07-11 01:17 +0800\n\n### 2. Apple sues OpenAI over hardware trade-secret theft\n["+appleURL+"]("+appleURL+")\nPublished: 2026-07-11 05:00 +0800",
		true,
	)
	answer := "1. SK Hynix IPO\n- 核心事实：SK Hynix 完成 IPO。\n- 来源：" + appleURL + "\n\n" +
		"2. Apple sues OpenAI\n- 核心事实：Apple 起诉 OpenAI 窃取 hardware trade secrets。\n- 来源：" + appleURL
	issues := currentNewsEvidenceAssociationIssues("Give me 8 items of today's technology news.", answer, []string{rss})
	if len(issues) != 1 || !strings.Contains(issues[0], "item 1") || !strings.Contains(issues[0], "item 2") {
		t.Fatalf("wrong-event citation was not rejected: %v", issues)
	}
}

func TestCurrentNewsEvidenceAssociationAllowsMatchingEventURLs(t *testing.T) {
	skURL := "https://techcrunch.com/2026/07/10/sk-hynix-raises-26-5b-in-us-ipo/"
	appleURL := "https://techcrunch.com/2026/07/10/apple-sues-openai-over-trade-secret-theft/"
	rss := toolResultForFinalization(
		ToolCall{Name: "social_trending", Arguments: map[string]interface{}{"source": "rss", "url": "https://techcrunch.com/feed/"}},
		"Source: https://techcrunch.com/feed/\n\n### 1. SK Hynix raises $26.5B in US IPO\n["+skURL+"]("+skURL+")\nPublished: 2026-07-11 01:17 +0800\n\n### 2. Apple sues OpenAI over hardware trade-secret theft\n["+appleURL+"]("+appleURL+")\nPublished: 2026-07-11 05:00 +0800",
		true,
	)
	answer := "1. SK Hynix IPO\n- 核心事实：SK Hynix 完成 IPO。\n- 来源：" + skURL + "\n\n" +
		"2. Apple sues OpenAI\n- 核心事实：Apple 起诉 OpenAI 窃取 hardware trade secrets。\n- 来源：" + appleURL
	if issues := currentNewsEvidenceAssociationIssues("Give me 8 items of today's technology news.", answer, []string{rss}); len(issues) != 0 {
		t.Fatalf("matching citations were rejected: %v", issues)
	}
}

func TestCurrentNewsEvidenceAssociationAllowsMultipleEventsNamedInRoundupTitle(t *testing.T) {
	roundupURL := "https://publisher.example/daily-roundup"
	rss := toolResultForFinalization(
		ToolCall{Name: "social_trending", Arguments: map[string]interface{}{"source": "rss", "url": "https://publisher.example/feed"}},
		"Source: https://publisher.example/feed\n\n### 1. Daily roundup: SK Hynix IPO; ChangXin Technology underwriting team\n["+roundupURL+"]("+roundupURL+")\nPublished: 2026-07-11 09:24 +0800\n[CURRENT_NEWS_ROUNDUP_FILTER] Only events literally named in this title are eligible.",
		true,
	)
	answer := "1. SK Hynix IPO\n- 核心事实：SK Hynix IPO。\n- 来源：" + roundupURL + "\n\n" +
		"2. ChangXin Technology underwriting team\n- 核心事实：ChangXin Technology公布承销团。\n- 来源：" + roundupURL
	if issues := currentNewsEvidenceAssociationIssues("Give me 8 items of today's technology news.", answer, []string{rss}); len(issues) != 0 {
		t.Fatalf("two eligible roundup-title events were forced into one-to-one URL binding: %v", issues)
	}
}

func TestCurrentNewsUnsupportedQuotationRequiresVerbatimEvidence(t *testing.T) {
	url := "https://publisher.example/meta-feature"
	record := toolResultForFinalization(
		ToolCall{Name: "web_fetch", Arguments: map[string]interface{}{"url": url}},
		"# Meta turns off feature\n\n**Source:** "+url+"\n\nMeta said the feature missed the mark and was turned off.",
		true,
	)
	input := "Give me 8 items of today's technology news."
	bad := `1. Meta update: the company said “public content should never be referenced this way”. 来源：` + url
	if issues := currentNewsUnsupportedQuotationIssues(input, bad, []string{record}); len(issues) != 1 {
		t.Fatalf("invented quotation was not rejected: %v", issues)
	}
	good := `1. Meta update: the company said the feature “missed the mark”. 来源：` + url
	if issues := currentNewsUnsupportedQuotationIssues(input, good, []string{record}); len(issues) != 0 {
		t.Fatalf("verbatim quotation was rejected: %v", issues)
	}
}

func TestNormalizeCurrentNewsUnsupportedQuotationsIsLocalAndToolless(t *testing.T) {
	url := "https://publisher.example/meta-feature"
	record := toolResultForFinalization(
		ToolCall{Name: "web_fetch", Arguments: map[string]interface{}{"url": url}},
		"# Meta feature\n\n**Source:** "+url+"\n\nThe feature missed the mark. The 三高 test facility opened.",
		true,
	)
	answer := `1. Meta called it “missed the mark” but described it as “完全可控的创作工具”; the “三高”（高温/高寒/高原）facility opened. 来源：` + url
	normalized, changed := normalizeCurrentNewsUnsupportedQuotations("Give me 8 items of today's technology news.", answer, []string{record})
	if changed != 2 || !strings.Contains(normalized, `“missed the mark”`) || strings.Contains(normalized, `“完全可控的创作工具”`) || strings.Contains(normalized, `高温/高寒/高原`) {
		t.Fatalf("unsupported wording was not locally normalized: changed=%d answer=%s", changed, normalized)
	}
	if !strings.Contains(normalized, "完全可控的创作工具") {
		t.Fatalf("paraphrase content was removed instead of only its quotation claim: %s", normalized)
	}
}

func TestNormalizeCurrentNewsUnsupportedExchangeVenuesUsesNeutralMarket(t *testing.T) {
	url := "https://publisher.example/sk-hynix-ipo"
	record := toolResultForFinalization(
		ToolCall{Name: "web_fetch", Arguments: map[string]interface{}{"url": url}},
		"# SK Hynix Wall Street debut\n\n**Source:** "+url+"\n\nThe company launched on Wall Street Friday.",
		true,
	)
	answer := "1. SK Hynix\n- 核心事实：公司在纽交所挂牌。\n- 来源：" + url
	normalized, changed := normalizeCurrentNewsUnsupportedExchangeVenues("Give me 8 items of today's technology news.", answer, []string{record})
	if changed != 1 || strings.Contains(normalized, "纽交所") || !strings.Contains(normalized, "在美国资本市场挂牌") {
		t.Fatalf("unsupported exchange venue was not neutralized: changed=%d answer=%s", changed, normalized)
	}
	supportedRecord := toolResultForFinalization(
		ToolCall{Name: "web_fetch", Arguments: map[string]interface{}{"url": url}},
		"# SK Hynix Nasdaq debut\n\n**Source:** "+url+"\n\nThe company listed on NASDAQ.",
		true,
	)
	supportedAnswer := "1. SK Hynix\n- 核心事实：公司在纳斯达克挂牌。\n- 来源：" + url
	if got, changed := normalizeCurrentNewsUnsupportedExchangeVenues("Give me 8 items of today's technology news.", supportedAnswer, []string{supportedRecord}); changed != 0 || got != supportedAnswer {
		t.Fatalf("evidence-supported venue was changed: changed=%d answer=%s", changed, got)
	}
}
