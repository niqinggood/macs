package app

import (
	"strings"
	"testing"
)

func sourceLinkWeatherResult(provider, source string, succeeded bool) string {
	call := ToolCall{Name: "weather_forecast", Arguments: map[string]interface{}{
		"provider": provider, "location": "上海", "days": 7,
	}}
	content := "Source: " + source + "\n2026-07-11 rain 31℃ / 26℃"
	return toolResultForFinalization(call, content, succeeded)
}

func TestEnsureRequestedDirectSourceLinksAppendsExactSuccessfulURLs(t *testing.T) {
	openMeteo := "https://api.open-meteo.com/v1/forecast?latitude=31.2&daily=temperature_2m_max"
	sevenTimer := "https://www.7timer.info/bin/api.pl?lat=31.2&product=civil"
	results := []string{
		sourceLinkWeatherResult(openMeteoProvider, openMeteo, true),
		sourceLinkWeatherResult(sevenTimerProvider, sevenTimer, true),
	}
	answer, added := ensureRequestedDirectSourceLinks("给出两个来源链接", "天气表已完成。", results)
	if len(added) != 2 || !strings.Contains(answer, openMeteo) || !strings.Contains(answer, sevenTimer) {
		t.Fatalf("deterministic links missing: added=%v answer=%s", added, answer)
	}
}

func TestEnsureRequestedDirectSourceLinksDoesNotDuplicateExistingURL(t *testing.T) {
	first := "https://api.open-meteo.com/v1/forecast?latitude=31.2"
	second := "https://www.7timer.info/bin/api.pl?lat=31.2"
	results := []string{
		sourceLinkWeatherResult(openMeteoProvider, first, true),
		sourceLinkWeatherResult(sevenTimerProvider, second, true),
	}
	answer, added := ensureRequestedDirectSourceLinks("请提供来源网址", "已有来源："+first, results)
	if len(added) != 1 || added[0] != second || strings.Count(answer, first) != 1 {
		t.Fatalf("unexpected deduplication: added=%v answer=%s", added, answer)
	}
}

func TestEnsureRequestedDirectSourceLinksDeduplicatesEquivalentQueryOrder(t *testing.T) {
	exact := "https://www.7timer.info/bin/api.pl?lat=31.2&output=json&product=civil"
	equivalent := "https://www.7timer.info/bin/api.pl?product=civil&lat=31.2&output=json"
	results := []string{sourceLinkWeatherResult(sevenTimerProvider, exact, true)}
	answer, added := ensureRequestedDirectSourceLinks("请提供来源网址", "已有来源："+equivalent, results)
	if len(added) != 0 || strings.Count(answer, "7timer.info") != 1 {
		t.Fatalf("equivalent URL was duplicated: added=%v answer=%s", added, answer)
	}
}

func TestRequestedSourceLinksRecognizesOpenableAuthoritativeSourceWording(t *testing.T) {
	exact := "https://example.com/news/article-1"
	result := toolResultForFinalization(
		ToolCall{Name: "web_fetch", Arguments: map[string]interface{}{"url": exact}},
		"# Article\n\n**Source:** "+exact+"\n\nSubstantive current news evidence with publication date and facts.",
		true,
	)
	answer, added := ensureRequestedDirectSourceLinks("每条给一个可打开的原始或权威来源", "已完成简报。", []string{result})
	if len(added) != 1 || !strings.Contains(answer, exact) {
		t.Fatalf("openable authoritative-source wording did not restore citation: added=%v answer=%s", added, answer)
	}
}

func TestReconcileExternalEvidenceURLsReplacesShortenedAndRemovesUnknown(t *testing.T) {
	exactOpenMeteo := "https://api.open-meteo.com/v1/forecast?latitude=31.2&models=ecmwf_ifs025"
	exactSevenTimer := "https://www.7timer.info/bin/api.pl?lat=31.2&output=json&product=civil"
	results := []string{
		sourceLinkWeatherResult(openMeteoProvider, exactOpenMeteo, true),
		sourceLinkWeatherResult(sevenTimerProvider, exactSevenTimer, true),
	}
	answer := "A: https://api.open-meteo.com/v1/forecast?...&models=ecmwf_ifs025 B: https://www.7timer.info/bin/api.pl?product=civil&lat=31.2&output=json C: https://invented.example/data"
	cleaned, replaced, removed := reconcileExternalEvidenceURLs(answer, results)
	if len(replaced) != 1 || len(removed) != 1 || !strings.Contains(cleaned, exactOpenMeteo) || !strings.Contains(cleaned, "未验证链接已移除") {
		t.Fatalf("unexpected reconciliation: replaced=%v removed=%v answer=%s", replaced, removed, cleaned)
	}
	if strings.Contains(cleaned, "forecast?...") || strings.Contains(cleaned, "invented.example") {
		t.Fatalf("unsupported URL survived reconciliation: %s", cleaned)
	}
	if strings.Count(cleaned, "7timer.info") != 1 {
		t.Fatalf("semantically equivalent URL was changed or duplicated: %s", cleaned)
	}
}

func TestReconcileExternalEvidenceURLsKeepsExactPublisherRSSArticles(t *testing.T) {
	exact := "https://techcrunch.com/2026/07/10/meta-removes-feature-after-backlash/?f=rss&utm_source=feed"
	clean := "https://techcrunch.com/2026/07/10/meta-removes-feature-after-backlash"
	rss := toolResultForFinalization(
		ToolCall{Name: "social_trending", Arguments: map[string]interface{}{"source": "rss", "url": "https://techcrunch.com/feed/"}},
		"Source: https://techcrunch.com/feed/\nA timeout story headline\n["+exact+"]("+exact+")\nPublished: 2026-07-10",
		true,
	)
	answer := "exact: " + clean + " typo: https://techcrunch.com/2026/07/10/meta-removes-feature-after-backbacklash/"
	cleaned, _, removed := reconcileExternalEvidenceURLs(answer, []string{rss})
	if !strings.Contains(cleaned, clean) || strings.Contains(cleaned, "backbacklash") || len(removed) != 1 {
		t.Fatalf("RSS citation reconciliation failed: removed=%v answer=%s", removed, cleaned)
	}
}

func TestReconcileExternalEvidenceURLsRemovesUniqueNearRSSSlug(t *testing.T) {
	exact := "https://techcrunch.com/2026/07/10/meta-removes-controversial-ai-feature-on-instagram-after-backlash/"
	unrelated := "https://techcrunch.com/2026/07/10/apple-sues-openai-over-alleged-trade-secret-theft/"
	rss := toolResultForFinalization(
		ToolCall{Name: "social_trending", Arguments: map[string]interface{}{"source": "rss", "url": "https://techcrunch.com/feed/"}},
		"Source: https://techcrunch.com/feed/\n[Meta]("+exact+")\n[Apple]("+unrelated+")",
		true,
	)
	typo := "https://techcrunch.com/2026/07/10/meta-removes-controversial-ai-feature-on-instagram-after-backdrop/"
	cleaned, replaced, removed := reconcileExternalEvidenceURLs("来源："+typo, []string{rss})
	if len(replaced) != 0 || len(removed) != 1 || strings.Contains(cleaned, exact) || strings.Contains(cleaned, "backdrop") {
		t.Fatalf("unsupported near RSS slug was not removed: replaced=%v removed=%v answer=%s", replaced, removed, cleaned)
	}
}

func TestReconcileExternalEvidenceURLsRepairsPunctuationOnlyRSSSlugDrift(t *testing.T) {
	exact := "https://techcrunch.com/2026/07/10/sk-hynix-raises-26-5b-in-the-biggest-foreign-ipo-in-us-history/"
	rss := toolResultForFinalization(
		ToolCall{Name: "social_trending", Arguments: map[string]interface{}{"source": "rss", "url": "https://techcrunch.com/feed/"}},
		"Source: https://techcrunch.com/feed/\n[SK Hynix] ("+exact+")",
		true,
	)
	drifted := "https://techcrunch.com/2026/07/10/sk-hynix-raises-26.5b-in-the-biggest-foreign-ipo-in-us-history/"
	cleaned, replaced, removed := reconcileExternalEvidenceURLs("来源："+drifted, []string{rss})
	if len(replaced) != 1 || len(removed) != 0 || !strings.Contains(cleaned, exact) || strings.Contains(cleaned, "26.5b") {
		t.Fatalf("punctuation-only slug drift was not repaired safely: replaced=%v removed=%v answer=%s", replaced, removed, cleaned)
	}
}

func TestReconcileExternalEvidenceURLsCompletesUniqueTerminalRSSPlaceholder(t *testing.T) {
	exact := "https://techcrunch.com/2026/07/10/sk-hynix-raises-26-5b-in-the-biggest-foreign-ipo-in-us-history-is-urged-to-build-new-us-fabs/"
	other := "https://techcrunch.com/2026/07/10/apple-sues-openai-over-alleged-trade-secret-theft/"
	rss := toolResultForFinalization(
		ToolCall{Name: "social_trending", Arguments: map[string]interface{}{"source": "rss", "url": "https://techcrunch.com/feed/"}},
		"Source: https://techcrunch.com/feed/\n[SK]("+exact+")\n[Apple]("+other+")",
		true,
	)
	shortened := "https://techcrunch.com/2026/07/10/sk-hynix-raises-26-5b-..."
	cleaned, replaced, removed := reconcileExternalEvidenceURLs("来源："+shortened, []string{rss})
	if len(replaced) != 1 || len(removed) != 0 || !strings.Contains(cleaned, exact) || strings.Contains(cleaned, "...") {
		t.Fatalf("unique terminal placeholder was not completed safely: replaced=%v removed=%v answer=%s", replaced, removed, cleaned)
	}
}

func TestReconcileExternalEvidenceURLsRepairsShortenedStockFactsEndpointBeforeGrounding(t *testing.T) {
	exact := "https://push2delay.eastmoney.com/api/qt/stock/get?fields=f43%2Cf44%2Cf45%2Cf46%2Cf47%2Cf48%2Cf57%2Cf58%2Cf60%2Cf86%2Cf116%2Cf162%2Cf163%2Cf164%2Cf167&secid=1.600596"
	results := []string{toolResultForFinalization(
		ToolCall{Name: "stock_facts", Arguments: map[string]interface{}{"provider": "eastmoney_market", "symbol": "600596"}},
		"OPENKITTY_STOCK_FACTS_RESULT={\"provider\":\"eastmoney_market\",\"source_url\":\""+exact+"\"}\nSource: "+exact,
		true,
	)}
	candidate := "| S2 | 东方财富行情 | `https://push2delay.eastmoney.com/api/qt/stock/get?fields=...&secid=1.600596` |"

	cleaned, replaced, removed := reconcileExternalEvidenceURLs(candidate, results)
	if len(replaced) != 1 || len(removed) != 0 || !strings.Contains(cleaned, exact) || strings.Contains(cleaned, "fields=...") {
		t.Fatalf("shortened stock_facts URL was not repaired: replaced=%v removed=%v answer=%s", replaced, removed, cleaned)
	}
	if issues := unsupportedHighRiskEvidenceClaims(cleaned, results); len(issues) != 0 {
		t.Fatalf("reconciled stock_facts URL must pass the grounding gate: %v", issues)
	}
}

func TestReconcileExternalEvidenceURLsNeverMapsCompleteSameHostArticleToUnrelatedDirectFetch(t *testing.T) {
	apple := "https://techcrunch.com/2026/07/10/apple-sues-openai-over-alleged-trade-secret-theft/"
	direct := toolResultForFinalization(
		ToolCall{Name: "web_fetch", Arguments: map[string]interface{}{"url": apple}},
		"# Apple sues OpenAI\n\n**Source:** "+apple+"\n\nTrade-secret lawsuit evidence.",
		true,
	)
	unsupportedSK := "https://techcrunch.com/2026/07/10/sk-hynix-raises-26.5b-in-us-ipo/"
	cleaned, replaced, removed := reconcileExternalEvidenceURLs("SK来源："+unsupportedSK, []string{direct})
	if len(replaced) != 0 || len(removed) != 1 || strings.Contains(cleaned, apple) {
		t.Fatalf("unrelated same-host article was substituted: replaced=%v removed=%v answer=%s", replaced, removed, cleaned)
	}
}

func TestReconcileExternalEvidenceURLsDoesNotFuzzyMapReorderedOrChangedRSSSlugs(t *testing.T) {
	exact := "https://techcrunch.com/2026/07/10/apple-sues-openai-over-7-trade-secrets/"
	rss := toolResultForFinalization(
		ToolCall{Name: "social_trending", Arguments: map[string]interface{}{"source": "rss", "url": "https://techcrunch.com/feed/"}},
		"Source: https://techcrunch.com/feed/\n[Apple]("+exact+")",
		true,
	)
	unsafe := []string{
		"https://techcrunch.com/2026/07/10/openai-sues-apple-over-7-trade-secrets/",
		"https://techcrunch.com/2026/07/10/apple-sues-openai-over-9-trade-secrets/",
		"https://techcrunch.com/2026/07/10/meta-sues-openai-over-7-trade-secrets/",
	}
	cleaned, replaced, removed := reconcileExternalEvidenceURLs(strings.Join(unsafe, "\n"), []string{rss})
	if len(replaced) != 0 || len(removed) != len(unsafe) {
		t.Fatalf("unsafe fuzzy URL was mapped: replaced=%v removed=%v answer=%s", replaced, removed, cleaned)
	}
	for _, rawURL := range unsafe {
		if strings.Contains(cleaned, rawURL) {
			t.Fatalf("unsupported URL survived reconciliation: %s", cleaned)
		}
	}
	if strings.Contains(cleaned, exact) {
		t.Fatalf("unsupported URL was rewritten to evidence URL: %s", cleaned)
	}
}

func TestReconcileExternalEvidenceURLsDropsBrokenSegmentWhenValidSourceRemains(t *testing.T) {
	valid := "https://www.theverge.com/tech/964416/meta-instagram-ai-muse-image-deepfakes"
	exactTechCrunch := "https://techcrunch.com/2026/07/10/meta-removes-controversial-ai-feature-on-instagram-after-backlash/"
	invalidTechCrunch := "https://techcrunch.com/2026/07/10/meta-removes-controversial-ai-feature-on-instagram-after-backlash-2/"
	record := `OPENKITTY_TOOL_RECORD={"tool":"social_trending","succeeded":true,"search_mode":"direct","source_url":"https://techcrunch.com/feed/"}` + "\n" +
		"[" + valid + "](" + valid + ")\n[" + exactTechCrunch + "](" + exactTechCrunch + ")"
	answer := "- 来源：The Verge " + valid + "； TechCrunch " + invalidTechCrunch + " 2/"
	cleaned, _, removed := reconcileExternalEvidenceURLs(answer, []string{record})
	if len(removed) != 1 || !strings.Contains(cleaned, valid) {
		t.Fatalf("unexpected reconciliation: removed=%v output=%q", removed, cleaned)
	}
	if strings.Contains(cleaned, "未验证链接已移除") || strings.Contains(cleaned, "TechCrunch") || strings.Contains(cleaned, "2/") {
		t.Fatalf("broken citation segment leaked into user-facing output: %q", cleaned)
	}
}

func TestReconcileExternalEvidenceURLsKeepsRootDirectAPI(t *testing.T) {
	rootAPI := "https://api.example.com/?q=current"
	article := "https://publisher.example/news/article"
	results := []string{
		toolResultForFinalization(ToolCall{Name: "web_fetch", Arguments: map[string]interface{}{"url": rootAPI}}, "Source: "+rootAPI+"\nsubstantive data", true),
		toolResultForFinalization(ToolCall{Name: "web_fetch", Arguments: map[string]interface{}{"url": article}}, "Source: "+article+"\nsubstantive article", true),
	}
	answer := rootAPI + "\n" + article
	cleaned, _, removed := reconcileExternalEvidenceURLs(answer, results)
	if len(removed) != 0 || !strings.Contains(cleaned, rootAPI) || !strings.Contains(cleaned, article) {
		t.Fatalf("direct API citation was removed: removed=%v answer=%s", removed, cleaned)
	}
}

func TestReconcileExternalEvidenceURLsIgnoresOrdinaryFailureWordsInArticle(t *testing.T) {
	first := "https://publisher.example/news/timeout-analysis"
	second := "https://other.example/story/control"
	results := []string{
		toolResultForFinalization(ToolCall{Name: "web_fetch", Arguments: map[string]interface{}{"url": first}}, "Source: "+first+"\nThis valid article analyzes timeout and access denied errors.", true),
		toolResultForFinalization(ToolCall{Name: "web_fetch", Arguments: map[string]interface{}{"url": second}}, "Source: "+second+"\nControl article.", true),
	}
	cleaned, _, removed := reconcileExternalEvidenceURLs(first+"\n"+second, results)
	if len(removed) != 0 || !strings.Contains(cleaned, first) || !strings.Contains(cleaned, second) {
		t.Fatalf("ordinary article prose triggered low-yield removal: removed=%v answer=%s", removed, cleaned)
	}
}

func TestEnsureRequestedDirectSourceLinksSkipsUnrequestedFailedAndLowYield(t *testing.T) {
	failed := sourceLinkWeatherResult(openMeteoProvider, "https://failed.example/data", false)
	lowYield := toolResultForFinalization(ToolCall{Name: "web_fetch", Arguments: map[string]interface{}{"url": "https://partial.example/data"}}, "[RETRIEVAL_STATUS: PARTIAL]\nSource: https://partial.example/data", true)
	if answer, added := ensureRequestedDirectSourceLinks("总结天气", "完成。", []string{failed, lowYield}); answer != "完成。" || len(added) != 0 {
		t.Fatalf("unrequested links changed answer: added=%v answer=%s", added, answer)
	}
	if answer, added := ensureRequestedDirectSourceLinks("给出来源链接", "完成。", []string{failed, lowYield}); answer != "完成。" || len(added) != 0 {
		t.Fatalf("failed/low-yield links appended: added=%v answer=%s", added, answer)
	}
}

func TestSafePublicCitationURLRejectsSecrets(t *testing.T) {
	if safePublicCitationURL("https://example.com/data?api_key=secret") {
		t.Fatal("secret-bearing citation URL was accepted")
	}
	if !safePublicCitationURL("https://example.com/data?latitude=31.2") {
		t.Fatal("ordinary public citation URL was rejected")
	}
}
