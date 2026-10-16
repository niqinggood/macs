package network

import (
	"errors"
	"net/url"
	"strings"
	"testing"
)

func TestExtractSearchQuery(t *testing.T) {
	query := extractSearchQuery("https://html.duckduckgo.com/html/?q=" + url.QueryEscape("tech news July 2026"))
	if query != "tech news July 2026" {
		t.Fatalf("expected decoded query, got %q", query)
	}

	query = extractSearchQuery("https://search.yahoo.com/search?p=" + url.QueryEscape("贵州燃气"))
	if query != "贵州燃气" {
		t.Fatalf("expected yahoo query, got %q", query)
	}
}

func TestSearchFallbackCandidatesForDuckDuckGo(t *testing.T) {
	tool := NewWebFetchTool(5, "", false, nil, false)
	candidates := tool.SearchFallbackCandidates(EngineDuckDuckGo, "tech news")
	if len(candidates) < 3 {
		t.Fatalf("expected fallback candidates, got %d", len(candidates))
	}
	want := []SearchEngine{EngineBingINT, EngineBrave, EngineYahoo}
	for i, engine := range want {
		if candidates[i].Engine != engine {
			t.Fatalf("candidate %d engine: expected %s, got %s", i, engine, candidates[i].Engine)
		}
		if !strings.Contains(candidates[i].URL, "tech+news") {
			t.Fatalf("candidate URL should include encoded query: %s", candidates[i].URL)
		}
	}
}

func TestSearchFallbackCandidatesForSogou(t *testing.T) {
	tool := NewWebFetchTool(5, "", false, nil, false)
	candidates := tool.SearchFallbackCandidates(EngineSogou, "AI平台 采购")
	if len(candidates) < 3 {
		t.Fatalf("expected fallback candidates, got %d", len(candidates))
	}
	if candidates[0].Engine != EngineBingINT {
		t.Fatalf("first fallback should be bing_int, got %s", candidates[0].Engine)
	}
	for _, candidate := range candidates {
		if candidate.Engine == EngineSogou {
			t.Fatalf("fallback candidates should not include original engine: %+v", candidates)
		}
	}
}

func TestSearchFallbackCandidatesForBraveStartWithBing(t *testing.T) {
	tool := NewWebFetchTool(5, "", false, nil, false)
	candidates := tool.SearchFallbackCandidates(EngineBrave, "official vendor pricing")
	if len(candidates) == 0 || candidates[0].Engine != EngineBingINT {
		t.Fatalf("Brave should degrade to Bing first, got %+v", candidates)
	}
	for _, candidate := range candidates {
		if candidate.Engine == EngineBrave {
			t.Fatalf("fallback candidates must not retry Brave: %+v", candidates)
		}
	}
}

func TestIsUsableSearchResult(t *testing.T) {
	if isUsableSearchResult("No results found") {
		t.Fatal("empty search result should not be usable")
	}
	if isUsableSearchResult("No safe search results found. Search results were filtered because they matched unsafe or irrelevant adult-content patterns.") {
		t.Fatal("fully filtered unsafe search result should not be usable")
	}
	if !isUsableSearchResult("1. Example\n   URL: https://example.com\n") {
		t.Fatal("numbered search result should be usable")
	}
}

func TestIsUsableSearchResultForSiteRestrictedIntent(t *testing.T) {
	good := "1. Vendor pricing\n   URL: https://www.vendor.example/pricing\n   Official plans and prices"
	if !isUsableSearchResultForQuery(good, "pricing plans site:vendor.example") {
		t.Fatal("matching official pricing result should be usable")
	}
	thirdParty := "1. Vendor review\n   URL: https://reviews.example/vendor\n   pricing overview"
	if isUsableSearchResultForQuery(thirdParty, "pricing plans site:vendor.example") {
		t.Fatal("third-party result should not satisfy a site-restricted query")
	}
	loginOnly := "1. Customer login\n   URL: https://login.vendor.example/\n   Sign in to your account"
	if isUsableSearchResultForQuery(loginOnly, "pricing plans site:vendor.example") {
		t.Fatal("domain-matching login result should not satisfy pricing intent")
	}
}

func TestFormatResultsFiltersUnsafeAdultSearchResults(t *testing.T) {
	tool := NewWebFetchTool(5, "", false, nil, false)
	result := tool.FormatResults([]SearchResult{
		{
			Title:   "Breckie Hill Leaked OnlyFans & Viral XXX Videos",
			URL:     "https://viralxxxporn.com/models/breckie-hill",
			Content: "Watch leaked porn videos and OnlyFans clips.",
		},
		{
			Title:   "新安股份 600596 行情走势",
			URL:     "https://quote.eastmoney.com/sh600596.html",
			Content: "提供新安股份股票行情走势、新闻资讯、公告和资金流分析。",
		},
	}, 5)

	for _, notWant := range []string{"OnlyFans", "viralxxxporn", "porn videos"} {
		if strings.Contains(result, notWant) {
			t.Fatalf("unsafe search result leaked %q in:\n%s", notWant, result)
		}
	}
	if !strings.Contains(result, "新安股份") || !strings.Contains(result, "eastmoney.com") {
		t.Fatalf("safe finance result should be preserved:\n%s", result)
	}
}

func TestFormatResultsReturnsUnusableWhenAllResultsUnsafe(t *testing.T) {
	tool := NewWebFetchTool(5, "", false, nil, false)
	result := tool.FormatResults([]SearchResult{
		{Title: "OnlyFans leaked nude content", URL: "https://example.com", Content: "xxx videos"},
	}, 5)
	if !strings.Contains(result, "No safe search results found") {
		t.Fatalf("expected safe-filter message, got:\n%s", result)
	}
	if isUsableSearchResult(result) {
		t.Fatalf("safe-filter message should trigger fallback, got usable:\n%s", result)
	}
}

func TestFormatFallbackSearchResult(t *testing.T) {
	result := formatFallbackSearchResult(EngineDuckDuckGo, EngineBingINT, errors.New("dial tcp4 157.240.3.50:443: i/o timeout"), "1. Example\n")
	for _, want := range []string{"Search fallback", "duckduckgo", "bing_int", "157.240.3.50", "1. Example"} {
		if !strings.Contains(result, want) {
			t.Fatalf("fallback result missing %q: %s", want, result)
		}
	}
}

func TestParseAgentReachExaOutput(t *testing.T) {
	raw := []byte(`{"content":[{"type":"text","text":"Title: Pricing\\nURL: https://vendor.example/pricing"}],"_meta":{"searchTime":12.3}}`)
	got, err := parseAgentReachExaOutput(raw)
	if err != nil {
		t.Fatalf("parse Exa output: %v", err)
	}
	if !strings.Contains(got, "https://vendor.example/pricing") {
		t.Fatalf("parsed Exa output lost URL: %s", got)
	}
}
