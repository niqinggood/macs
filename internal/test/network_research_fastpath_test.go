package app

import (
	"context"
	"strings"
	"testing"
)

func TestFastReadOnlyResearchGuidanceTargetsSingleAnswerLookups(t *testing.T) {
	input := "请联网查询上海未来7天天气，核对两个来源并给出通勤建议"
	guidance := fastReadOnlyResearchGuidance(input)
	for _, want := range []string{"FAST READ-ONLY RESEARCH", "Do not call plan_task", "web_fetch first", "assess source quality and provenance", "1800 characters", "Never infer finer timing"} {
		if !strings.Contains(guidance, want) {
			t.Fatalf("guidance missing %q: %s", want, guidance)
		}
	}
}

func TestFastReadOnlyResearchGuidanceDoesNotOverrideArtifactWork(t *testing.T) {
	for _, input := range []string{
		"联网调研新能源行业并生成PPT",
		"查询最新油价并保存为Excel文件",
		"修改项目中的天气查询代码",
		"解释这个本地函数为什么报错",
	} {
		if got := fastReadOnlyResearchGuidance(input); got != "" {
			t.Fatalf("artifact/non-research request %q received fast lookup guidance: %s", input, got)
		}
	}
}

func TestArtifactResearchUsesBoundedOfficialDiscoveryWorkflow(t *testing.T) {
	input := `联网比较三款AI客服产品的官方功能和价格，并保存到 E:\work\selection.md`
	if !isArtifactResearchTurn(input) {
		t.Fatal("explicit Markdown research artifact was not recognized")
	}
	guidance := artifactResearchGuidance(input)
	for _, want := range []string{"ARTIFACT-BACKED OFFICIAL RESEARCH", "query-only mode", "do not guess", "exact official URLs visible in discovery results", "write the requested artifact once"} {
		if !strings.Contains(guidance, want) {
			t.Fatalf("artifact research guidance missing %q: %s", want, guidance)
		}
	}
	ctx := withArtifactResearchTurn(context.Background(), input)
	if got := artifactResearchRequestFromContext(ctx); got != input {
		t.Fatalf("artifact research request = %q", got)
	}
	if got := readOnlyResearchRequestFromContext(ctx); got != "" {
		t.Fatalf("artifact research must not activate chat-only mutation blocking: %q", got)
	}
}

func TestPipelineStepDoesNotInheritTopLevelResearchFastPaths(t *testing.T) {
	input := `Research current vendor information and save the sourced report to E:\work\report.md`
	ctx := withReadOnlyResearchTurn(context.Background(), input)
	ctx = withArtifactResearchTurn(ctx, input)
	if artifactResearchRequestFromContext(ctx) == "" {
		t.Fatal("ordinary turn did not activate artifact research before pipeline scoping")
	}

	ctx = context.WithValue(ctx, "pipeline_step_execution", true)
	if got := artifactResearchRequestFromContext(ctx); got != "" {
		t.Fatalf("pipeline step inherited top-level artifact research request: %q", got)
	}
	if got := readOnlyResearchRequestFromContext(ctx); got != "" {
		t.Fatalf("pipeline step inherited top-level read-only research request: %q", got)
	}
	if got := researchTurnRequestFromContext(ctx); got != "" {
		t.Fatalf("pipeline step inherited a combined research fast path: %q", got)
	}
}

func TestArtifactResearchNormalizesDiscoveryAndRejectsGuessedVerificationURLs(t *testing.T) {
	discoveryCalls := []ToolCall{
		{Name: "web_fetch", Arguments: map[string]interface{}{"query": "site:vendor.example official pricing", "url": "https://vendor.example/guessed"}},
		{Name: "write_file", Arguments: map[string]interface{}{"path": "report.md"}},
	}
	normalized := normalizeArtifactResearchDiscoveryCalls(discoveryCalls)
	if len(normalized) != 1 || normalized[0].Name != "web_fetch" {
		t.Fatalf("normalized discovery calls = %#v", normalized)
	}
	if _, hasURL := normalized[0].Arguments["url"]; hasURL {
		t.Fatalf("discovery call retained a guessed direct URL: %#v", normalized[0])
	}
	if normalized[0].Arguments["engine"] != "brave" || normalized[0].Arguments["maxResults"] != 10 {
		t.Fatalf("artifact discovery did not use the higher-yield bounded engine: %#v", normalized[0].Arguments)
	}

	discovery := []string{toolResultForFinalization(
		ToolCall{Name: "web_fetch", Arguments: map[string]interface{}{"query": "vendor", "engine": "brave"}},
		"1. Pricing\nURL: https://vendor.example/real-pricing", true,
	)}
	verification := filterArtifactResearchVerificationCalls([]ToolCall{
		{Name: "web_fetch", Arguments: map[string]interface{}{"url": "https://vendor.example/real-pricing"}},
		{Name: "web_fetch", Arguments: map[string]interface{}{"url": "https://vendor.example/guessed-security"}},
	}, discovery, "compare vendor")
	if len(verification) != 1 || verification[0].Arguments["url"] != "https://vendor.example/real-pricing" {
		t.Fatalf("verification calls were not grounded in discovery: %#v", verification)
	}
}

func TestArtifactResearchSupplementsTwoDiscoveredURLsPerNamedVendor(t *testing.T) {
	input := "比较 Zendesk、Intercom、Salesforce 三款产品，每家至少2条官方页面直链"
	var discovery []string
	for _, pair := range [][]string{
		{"zendesk", "https://www.zendesk.com/pricing/", "https://www.zendesk.com/trust-center/"},
		{"intercom", "https://www.intercom.com/pricing", "https://www.intercom.com/fin"},
		{"salesforce", "https://www.salesforce.com/service/cloud/", "https://www.salesforce.com/service/cloud/pricing/"},
	} {
		call := ToolCall{Name: "web_fetch", Arguments: map[string]interface{}{"query": pair[0], "engine": "brave"}}
		discovery = append(discovery, toolResultForFinalization(call, "1. Official\nURL: "+pair[1]+"\n2. Official\nURL: "+pair[2], true))
	}
	got := filterArtifactResearchVerificationCalls(nil, discovery, input)
	if len(got) != 6 {
		t.Fatalf("supplemented verification calls = %d, want 6: %#v", len(got), got)
	}
	counts := map[string]int{}
	for _, call := range got {
		rawURL, _ := call.Arguments["url"].(string)
		counts[artifactVendorRootHost(rawURL)]++
	}
	for _, root := range []string{"zendesk.com", "intercom.com", "salesforce.com"} {
		if counts[root] != 2 {
			t.Fatalf("vendor %s received %d direct URLs, want 2", root, counts[root])
		}
	}
}

func TestArtifactResearchKeepsOneVerificationURLPerDiscoveryTopic(t *testing.T) {
	input := "比较 Vendor 一款产品，每家至少2条官方页面直链"
	results := []string{
		toolResultForFinalization(
			ToolCall{Name: "web_fetch", Arguments: map[string]interface{}{"query": "product site:vendor.example", "engine": "brave"}},
			"URL: https://vendor.example/product/features\nURL: https://vendor.example/blog/ai", true,
		),
		toolResultForFinalization(
			ToolCall{Name: "web_fetch", Arguments: map[string]interface{}{"query": "pricing security site:vendor.example", "engine": "brave"}},
			"URL: https://vendor.example/security/pricing\nURL: https://vendor.example/pricing", true,
		),
	}
	got := filterArtifactResearchVerificationCalls(nil, results, input)
	if len(got) != 2 {
		t.Fatalf("verification calls = %d, want one per discovery topic: %#v", len(got), got)
	}
	if got[0].Arguments["url"] != "https://vendor.example/product/features" ||
		got[1].Arguments["url"] != "https://vendor.example/pricing" {
		t.Fatalf("topic-diverse verification selection = %#v", got)
	}
}

func TestArtifactVerificationScorePrefersCanonicalShortPricingPath(t *testing.T) {
	canonical := artifactVerificationURLScore("https://vendor.example/pricing/")
	nested := artifactVerificationURLScore("https://vendor.example/pricing/employee-service-pricing/")
	securitySKU := artifactVerificationURLScore("https://vendor.example/platform/cloud-data-security/pricing/")
	if canonical <= nested || canonical <= securitySKU {
		t.Fatalf("canonical pricing score=%d must exceed nested=%d and security SKU=%d", canonical, nested, securitySKU)
	}
}

func TestArtifactResearchReplacementSelectsOnlyMissingVendorUnusedURL(t *testing.T) {
	input := "比较 Zendesk、Intercom、Salesforce 三款产品，每家至少2条官方页面直链"
	var results []string
	for _, pair := range [][]string{
		{"zendesk", "https://www.zendesk.com/product", "https://www.zendesk.com/pricing", "https://www.zendesk.com/security"},
		{"intercom", "https://www.intercom.com/product", "https://www.intercom.com/pricing", "https://www.intercom.com/security"},
		{"salesforce", "https://www.salesforce.com/service/cloud/", "https://trailhead.salesforce.com/content/learn/broken", "https://www.salesforce.com/service/pricing/"},
	} {
		call := ToolCall{Name: "web_fetch", Arguments: map[string]interface{}{"query": pair[0], "engine": "brave"}}
		results = append(results, toolResultForFinalization(call, "URL: "+pair[1]+"\nURL: "+pair[2]+"\nURL: "+pair[3], true))
	}
	for _, rawURL := range []string{
		"https://www.zendesk.com/product", "https://www.zendesk.com/pricing",
		"https://www.intercom.com/product", "https://www.intercom.com/pricing",
		"https://www.salesforce.com/service/cloud/",
	} {
		results = append(results, toolResultForFinalization(ToolCall{Name: "web_fetch", Arguments: map[string]interface{}{"url": rawURL}}, "official", true))
	}
	results = append(results, toolResultForFinalization(
		ToolCall{Name: "web_fetch", Arguments: map[string]interface{}{"url": "https://trailhead.salesforce.com/content/learn/broken"}},
		"404", false,
	))
	got := filterArtifactResearchVerificationCalls(nil, results, input)
	if len(got) != 1 || got[0].Arguments["url"] != "https://www.salesforce.com/service/pricing/" {
		t.Fatalf("replacement should target only unused Salesforce URL, got %#v", got)
	}
}

func TestArtifactResearchBalancesDiscoveryAcrossVendorSitesBeforeBudget(t *testing.T) {
	var calls []ToolCall
	for _, site := range []string{"zendesk.com", "intercom.com", "salesforce.com"} {
		for _, topic := range []string{"product", "pricing", "security", "deployment"} {
			calls = append(calls, ToolCall{Name: "web_fetch", Arguments: map[string]interface{}{
				"query": "site:" + site + " official " + topic,
			}})
		}
	}
	got := normalizeArtifactResearchDiscoveryCalls(calls)
	if len(got) != 6 {
		t.Fatalf("balanced discovery call count = %d, want 6", len(got))
	}
	counts := map[string]int{}
	for _, call := range got {
		query, _ := call.Arguments["query"].(string)
		match := artifactDiscoverySite.FindStringSubmatch(query)
		if len(match) != 2 {
			t.Fatalf("balanced call lost site group: %#v", call)
		}
		counts[match[1]]++
	}
	for _, site := range []string{"zendesk.com", "intercom.com", "salesforce.com"} {
		if counts[site] != 2 {
			t.Fatalf("site %s received %d discovery slots, want 2; calls=%#v", site, counts[site], got)
		}
	}
}

func TestArtifactResearchRequiresPerVendorDirectCoverage(t *testing.T) {
	input := `联网比较三款产品，每家至少保留2条官方页面直链，保存到 E:\work\selection.md`
	var results []string
	for _, rawURL := range []string{
		"https://a.example/product", "https://a.example/pricing",
		"https://b.example/product", "https://b.example/security",
		"https://c.example/product", "https://c.example/pricing",
	} {
		call := ToolCall{Name: "web_fetch", Arguments: map[string]interface{}{"url": rawURL}}
		results = append(results, toolResultForFinalization(call, "official evidence "+rawURL, true))
	}
	if artifactResearchDirectCoverageSatisfied(input, results[:5]) {
		t.Fatal("five pages did not satisfy two direct pages for each of three vendors")
	}
	if !artifactResearchDirectCoverageSatisfied(input, results) {
		t.Fatal("six direct pages across three vendor hosts did not satisfy coverage")
	}
}

func TestArtifactResearchRejectsBlockedDirectPageFromCoverage(t *testing.T) {
	input := `联网比较一款产品，每家至少2条官方页面直链，写成 Markdown 到 E:\work\selection.md`
	good := toolResultForFinalization(
		ToolCall{Name: "web_fetch", Arguments: map[string]interface{}{"url": "https://vendor.example/product"}},
		"# Product\nuseful official evidence", true,
	)
	blocked := toolResultForFinalization(
		ToolCall{Name: "web_fetch", Arguments: map[string]interface{}{"url": "https://vendor.example/pricing.pdf"}},
		"HTTP 403 / 抓取被拒；爬虫拦截", true,
	)
	if artifactResearchDirectCoverageSatisfied(input, []string{good, blocked}) {
		t.Fatal("403/WAF page must not satisfy direct-source coverage")
	}
}

func TestArtifactResearchDoesNotCountQueryVariantsAsDistinctPages(t *testing.T) {
	input := `联网比较一款产品，每家至少2条官方页面直链，写成 Markdown 到 E:\work\selection.md`
	var results []string
	for _, rawURL := range []string{
		"https://vendor.example/pricing",
		"https://vendor.example/pricing?current_tab=all#plans",
	} {
		results = append(results, toolResultForFinalization(
			ToolCall{Name: "web_fetch", Arguments: map[string]interface{}{"url": rawURL}},
			"official pricing evidence", true,
		))
	}
	if artifactResearchDirectCoverageSatisfied(input, results) {
		t.Fatal("query/fragment variants of one page counted as two official pages")
	}
}

func TestArtifactResearchDirectEvidenceAllowsOrdinaryFailureWordsInPageText(t *testing.T) {
	result := toolResultForFinalization(
		ToolCall{Name: "web_fetch", Arguments: map[string]interface{}{"url": "https://vendor.example/reliability"}},
		"# Reliability\nConfigure session timeout and failed-login recovery without service interruption.", true,
	)
	if !artifactResearchDirectEvidenceUsable(result) {
		t.Fatal("ordinary product prose containing timeout/failed was treated as blocked retrieval")
	}
}

func TestArtifactResearchDirectEvidenceAllowsSubstantivePartialPage(t *testing.T) {
	result := toolResultForFinalization(
		ToolCall{Name: "web_fetch", Arguments: map[string]interface{}{"url": "https://vendor.example/pricing"}},
		"[RETRIEVAL_STATUS: PARTIAL]\n# Vendor pricing\n"+strings.Repeat("This official page verifies pricing and plan fields; unrelated deployment fields remain unknown. ", 4), true,
	)
	if !artifactResearchDirectEvidenceUsable(result) {
		t.Fatal("substantive partial page was treated as a blocked/failed retrieval")
	}
}

func TestArtifactResearchDirectEvidenceAllowsSubstantiveContentAnomalyWarning(t *testing.T) {
	result := toolResultForFinalization(
		ToolCall{Name: "web_fetch", Arguments: map[string]interface{}{"url": "https://vendor.example/pricing"}},
		"## ⚠️ 内容可能不完整\n问题类型：内容异常（疑似不完整）\n**Source:** https://vendor.example/pricing\n"+
			strings.Repeat("Official plan, price, billing, feature, and integration facts. ", 20), true,
	)
	if !artifactResearchDirectEvidenceUsable(result) {
		t.Fatal("substantive official page was rejected solely for a conservative content-anomaly warning")
	}
}

func TestFastReadOnlyResearchTreatsPublicationTimeAsMetadata(t *testing.T) {
	input := "整理今天（2026-07-11）的8条科技商业新闻，每条给出事件发生时间、媒体发布时间和可打开的权威来源。"
	guidance := fastReadOnlyResearchGuidance(input)
	if guidance == "" {
		t.Fatal("publication-time metadata incorrectly turned a chat-only news brief into artifact work")
	}
	for _, want := range []string{"one user-facing answer", "Do not call write_file", "answer in chat", "social_trending", "at most one narrow event-title check", "exact article URL already visible", "local timezone", "previous local calendar day"} {
		if !strings.Contains(guidance, want) {
			t.Fatalf("chat-only news guidance missing %q: %s", want, guidance)
		}
	}
	if got := adaptiveLLMOutputTokenLimit(input); got != currentNewsResearchOutputTokenLimit {
		t.Fatalf("news brief output token limit = %d, want %d", got, currentNewsResearchOutputTokenLimit)
	}
}

func TestCurrentNewsSynthesisContinuationClosesRetrieval(t *testing.T) {
	prompt := currentNewsSynthesisContinuationPrompt()
	content, _ := prompt["content"].(string)
	for _, want := range []string{"Do not call any tool", "exactly one underlying event", "complete summary", "preserve their labels exactly", "Quotation marks", "same sentence", "selected numbered items", "discarded candidate", "Write directly", "1800 visible Chinese characters"} {
		if !strings.Contains(content, want) {
			t.Fatalf("current-news continuation missing %q: %s", want, content)
		}
	}
}

func TestFastReadOnlyResearchStillRecognizesRealPublishActions(t *testing.T) {
	for _, input := range []string{
		"搜索今天的科技新闻并发布到微信公众号",
		"查找最新热点，保存为 Markdown 文件",
		"research today's AI news and publish the report",
	} {
		if isFastReadOnlyResearchTurn(input) {
			t.Fatalf("real artifact or publish action %q was treated as chat-only", input)
		}
	}
}

func TestVendorResearchExportCapabilityIsNotMistakenForFileExport(t *testing.T) {
	input := "为一家50人电商团队调研当前可落地的企业客服AI方案，至少比较3款产品的官方功能、计费口径、人工接管、知识库、数据安全、部署限制和导出能力，最后给试点风险清单。"
	if !isFastReadOnlyResearchTurn(input) {
		t.Fatal("vendor research was not recognized as chat-only research")
	}
	guidance := fastReadOnlyResearchGuidance(input)
	for _, want := range []string{"multi-vendor/product research", "do not guess", "A 404 is not a source"} {
		if !strings.Contains(guidance, want) {
			t.Fatalf("vendor guidance missing %q: %s", want, guidance)
		}
	}
	if isFastReadOnlyResearchTurn(input + " 并导出为 customer_service_ai.pptx") {
		t.Fatal("real PPTX export was incorrectly treated as chat-only research")
	}
}

func TestFastReadOnlyResearchSynthesisUsesCompactClosedEvidence(t *testing.T) {
	results := []string{
		toolResultForFinalization(ToolCall{Name: "web_fetch", Arguments: map[string]interface{}{"url": "https://a.example/official"}}, "Vendor A https://a.example/official", true),
		toolResultForFinalization(ToolCall{Name: "web_fetch", Arguments: map[string]interface{}{"url": "https://b.example/official"}}, "Vendor B https://b.example/official", true),
		toolResultForFinalization(ToolCall{Name: "web_fetch", Arguments: map[string]interface{}{"url": "https://c.example/official"}}, "Vendor C https://c.example/official", true),
	}
	if got := fastReadOnlyResearchEvidenceCount(results); got != 3 {
		t.Fatalf("evidence count = %d, want 3", got)
	}
	messages := buildFastReadOnlyResearchSynthesisMessages("比较三款产品", results)
	if len(messages) != 3 {
		t.Fatalf("compact messages = %d, want 3", len(messages))
	}
	combined := messages[0]["content"].(string) + messages[2]["content"].(string)
	for _, want := range []string{"Do not call tools", "at least two distinct official pages", "https://a.example/official", "https://b.example/official", "https://c.example/official"} {
		if !strings.Contains(combined, want) {
			t.Fatalf("compact synthesis missing %q: %s", want, combined)
		}
	}
}

func TestFastReadOnlyResearchEvidenceModesSeparateDiscoveryFromDirect(t *testing.T) {
	results := []string{
		toolResultForFinalization(ToolCall{Name: "web_fetch", Arguments: map[string]interface{}{"engine": "sogou", "query": "vendor"}}, "results", true),
		toolResultForFinalization(ToolCall{Name: "web_fetch", Arguments: map[string]interface{}{"url": "https://vendor.example/product"}}, "official", true),
	}
	discovery, direct := fastReadOnlyResearchEvidenceModes(results)
	if discovery != 1 || direct != 1 {
		t.Fatalf("modes discovery=%d direct=%d", discovery, direct)
	}
	prompt := fastReadOnlyResearchDirectEvidencePrompt()["content"].(string)
	for _, want := range []string{"up to six", "direct web_fetch", "Do not guess URL paths", "only direct verification window"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("direct window prompt missing %q: %s", want, prompt)
		}
	}
}

func TestCompactResearchDiscoveryEvidenceKeepsCandidatesNotSensitiveSnippets(t *testing.T) {
	got := compactResearchDiscoveryEvidence("1. Vendor product\nURL: https://vendor.example/product\nmedical or unrelated sensitive snippet\n2. Pricing\nURL: https://vendor.example/pricing")
	if !strings.Contains(got, "Vendor product") || !strings.Contains(got, "https://vendor.example/pricing") {
		t.Fatalf("candidate identity was lost: %s", got)
	}
	if strings.Contains(got, "sensitive snippet") {
		t.Fatalf("discovery snippet leaked into synthesis: %s", got)
	}
}

func TestReadOnlyResearchContextGuidesWithoutBecomingPermissionGate(t *testing.T) {
	ctx := withReadOnlyResearchTurn(context.Background(), "整理今天的8条科技新闻，并在聊天中回答")
	agent := &LLMAgent{}
	for _, name := range []string{"write_file", "read_file", "plan_task", "notepad", "cli", "shell_analyze", "email", "wechat_article_bundle"} {
		if _, err, handled := agent.checkToolSafety(ctx, ToolCall{ID: name, Name: name}); handled || err != nil {
			t.Fatalf("research heuristic blocked configured tool %q: handled=%v err=%v", name, handled, err)
		}
	}
}

func TestReadOnlyResearchToolDefinitionsKeepConfiguredCapabilities(t *testing.T) {
	registry := NewToolRegistry()
	registry.Register(NewWriteFileTool(t.TempDir()))
	registry.Register(NewWebFetchTool(3, "", false, nil, false))
	registry.Register(NewShellAnalyzeTool())
	registry.Register(NewListDirTool(t.TempDir()))
	agent := &LLMAgent{toolRegistry: registry}
	ctx := withReadOnlyResearchTurn(context.Background(), "查询今天的科技新闻并直接回答")
	names := make(map[string]bool)
	for _, definition := range agent.toolDefinitionsForContext(ctx) {
		function, _ := definition["function"].(map[string]interface{})
		name, _ := function["name"].(string)
		names[name] = true
	}
	for _, want := range []string{"write_file", "shell_analyze", "list_dir", "web_fetch"} {
		if !names[want] {
			t.Fatalf("configured tool schema %q was hidden by research intent", want)
		}
	}
}
