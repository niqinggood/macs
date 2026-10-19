package network

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"mac/internal/toolcontract"
)

const newsDeliveryPrompt = "整理今天值得关注的中国与全球科技/商业热点，并提供原始或权威来源。"

type webFetchTestRegistry struct{ tool toolcontract.Tool }

func (r webFetchTestRegistry) Get(name string) (toolcontract.Tool, bool) {
	if r.tool != nil && r.tool.Name() == name {
		return r.tool, true
	}
	return nil, false
}

type webFetchRoundTripFunc func(*http.Request) (*http.Response, error)

func (fn webFetchRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

type webFetchTimeoutError struct{}

func (webFetchTimeoutError) Error() string   { return "synthetic timeout" }
func (webFetchTimeoutError) Timeout() bool   { return true }
func (webFetchTimeoutError) Temporary() bool { return true }

func TestWebFetchConstructorBoundsDefaultResultLimit(t *testing.T) {
	if got := NewWebFetchTool(0, "", false, nil, false).MaxResults(); got != defaultMaxResults {
		t.Fatalf("zero maxResults = %d, want default %d", got, defaultMaxResults)
	}
	if got := NewWebFetchTool(50000, "", false, nil, false).MaxResults(); got != 20 {
		t.Fatalf("oversized maxResults = %d, want bounded 20", got)
	}
}

func TestWebFetchPrivateNetworkProtectionIsOptIn(t *testing.T) {
	privateURLs := []string{
		"http://192.168.1.119:8001/music-skill.md",
		"http://127.0.0.1:8080/health",
		"http://localhost:3000/",
	}

	unprotected := NewWebFetchTool(3, "", false, nil, false)
	if description := unprotected.Description(); !strings.Contains(description, "URLs are allowed") {
		t.Fatalf("default tool description does not advertise allowed private URLs: %q", description)
	}
	for _, target := range privateURLs {
		if err := unprotected.CheckPrivateNetworkAccess(target); err != nil {
			t.Errorf("default web_fetch unexpectedly blocked %s: %v", target, err)
		}
	}

	protected := NewWebFetchTool(3, "", false, nil, true)
	if description := protected.Description(); !strings.Contains(description, "protection is enabled") {
		t.Fatalf("protected tool description does not advertise configured restriction: %q", description)
	}
	for _, target := range privateURLs {
		err := protected.CheckPrivateNetworkAccess(target)
		if !errors.Is(err, errPrivateNetworkAccessBlocked) {
			t.Errorf("protected web_fetch error for %s = %v, want private-network denial", target, err)
		}
	}
	if err := protected.CheckPrivateNetworkAccess("https://example.com/"); err != nil {
		t.Fatalf("private-network protection must allow public URL: %v", err)
	}
}

func TestWebFetchMarkdownUsesDirectHTTPFastPath(t *testing.T) {
	requestCount := 0
	document := `# 音乐工坊 API 使用指南

## 认证

POST /api/login
Body: {"username":"user1","password":"user1"}

## 生成

登录后调用歌曲生成接口，并轮询任务状态直到音频文件可用。
` + strings.Repeat("流行音乐生成参数说明，必须完整保留所有字段。\n", 300) + "END_OF_MUSIC_SKILL\n"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte(document))
	}))
	defer server.Close()

	tool := NewWebFetchTool(3, "", false, nil, false)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	result, err := tool.Execute(ctx, map[string]interface{}{"url": server.URL + "/music-skill.md"})
	if err != nil {
		t.Fatalf("fetch Markdown over direct HTTP: %v", err)
	}
	if requestCount != 1 {
		t.Fatalf("Markdown fast path made %d HTTP requests, want 1", requestCount)
	}
	for _, want := range []string{"音乐工坊 API 使用指南", "POST /api/login", "歌曲生成接口"} {
		if !strings.Contains(result, want) {
			t.Fatalf("direct Markdown result missing %q", want)
		}
	}
	if !strings.Contains(result, document) || !strings.Contains(result, "END_OF_MUSIC_SKILL") {
		t.Fatal("direct Markdown fast path did not preserve the complete document")
	}
}

func TestWebFetchExtractsPDFBeforeReturningToolContent(t *testing.T) {
	pdfBody := append([]byte("%PDF-1.7\n"), []byte{0x00, 0xff, 0x00, 0xfe, '\n'}...)
	pdfBody = append(pdfBody, []byte("binary payload that must never reach the model")...)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/pdf")
		_, _ = w.Write(pdfBody)
	}))
	defer server.Close()

	tool := NewWebFetchTool(3, "", false, nil, false)
	tool.SetPDFTextExtractor(func(path string) string {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read temporary PDF: %v", err)
		}
		if !looksLikePDFBytes(data) {
			t.Fatalf("temporary file is not the fetched PDF: %q", data)
		}
		return "2025 annual report revenue was 100 million yuan."
	})
	result, err := tool.Execute(context.Background(), map[string]interface{}{"url": server.URL + "/report.pdf"})
	if err != nil {
		t.Fatalf("fetch PDF: %v", err)
	}
	if !strings.Contains(result, "annual report revenue") || !strings.Contains(result, server.URL) {
		t.Fatalf("PDF text/source missing from result: %q", result)
	}
	if strings.Contains(result, "%PDF-") || strings.Contains(result, "binary payload") || !utf8.ValidString(result) {
		t.Fatalf("raw PDF binary leaked into tool result: %q", result)
	}
}

func TestWebFetchPDFExtractionFailureDoesNotReturnBinary(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/pdf")
		_, _ = w.Write([]byte("%PDF-1.7\nraw-binary-marker"))
	}))
	defer server.Close()

	tool := NewWebFetchTool(3, "", false, nil, false)
	tool.SetPDFTextExtractor(func(string) string { return "" })
	result, err := tool.Execute(context.Background(), map[string]interface{}{"url": server.URL + "/scan.pdf"})
	if err == nil || result != "" {
		t.Fatalf("unextractable PDF = result %q err %v, want a real tool error without binary", result, err)
	}
	if strings.Contains(err.Error(), "%PDF-") || strings.Contains(err.Error(), "raw-binary-marker") {
		t.Fatalf("raw PDF binary leaked into error: %v", err)
	}
}

func TestIsDirectWebDocument(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		content string
		want    bool
	}{
		{name: "markdown", url: "http://host/skill.md", content: "# Skill\nInstructions", want: true},
		{name: "plain text", url: "http://host/docs", content: "plain API instructions", want: true},
		{name: "json", url: "http://host/api", content: `{"status":"ok"}`, want: true},
		{name: "static html", url: "http://host/page", content: "<!doctype html><html><body>content</body></html>", want: false},
		{name: "html behind markdown path", url: "http://host/skill.md", content: "<html><body>login required</body></html>", want: false},
		{name: "empty", url: "http://host/empty.txt", content: "", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isDirectWebDocument(tt.url, tt.content); got != tt.want {
				t.Fatalf("isDirectWebDocument(%q, %q) = %v, want %v", tt.url, tt.content, got, tt.want)
			}
		})
	}
}

func TestNeedsJavaScriptDoesNotPenalizeShortStaticPages(t *testing.T) {
	tool := NewWebFetchTool(3, "", false, nil, false)
	tests := []struct {
		name string
		html string
		want bool
	}{
		{
			name: "short login form",
			html: `<html><body><form><label>Username</label><input name="username"><button>Login</button></form></body></html>`,
			want: false,
		},
		{
			name: "server rendered root",
			html: `<html><body><div id="root"><h1>Music Studio</h1><p>Sign in to generate a song.</p></div><script src="app.js"></script></body></html>`,
			want: false,
		},
		{
			name: "empty application shell",
			html: `<html><body><div id="root"></div><script src="app.js"></script></body></html>`,
			want: true,
		},
		{
			name: "loading shell",
			html: `<html><body><div>Loading...</div><script src="bundle.js"></script></body></html>`,
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tool.NeedsJavaScript(tt.html); got != tt.want {
				t.Fatalf("needsJavaScript() = %v, want %v for %s", got, tt.want, tt.html)
			}
		})
	}
}

func TestWebFetchPrivateNetworkProtectionDenialIsToolError(t *testing.T) {
	tool := NewWebFetchTool(3, "", false, nil, true)
	result, err := tool.Execute(context.Background(), map[string]interface{}{
		"url": "http://192.168.1.119:8001/music-skill.md",
	})
	if result != "" {
		t.Fatalf("policy denial result = %q, want empty result", result)
	}
	if !errors.Is(err, errPrivateNetworkAccessBlocked) {
		t.Fatalf("policy denial error = %v, want private-network tool error", err)
	}
}

func TestWebFetchCanceledRequestsReturnToolErrors(t *testing.T) {
	tool := NewWebFetchTool(3, "", false, nil, false)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result, err := tool.Execute(ctx, map[string]interface{}{
		"url": "http://127.0.0.1:1/document.md",
	})
	if result != "" || err == nil {
		t.Fatalf("canceled URL fetch = result %q err %v, want real tool error", result, err)
	}

	result, err = tool.Execute(ctx, map[string]interface{}{
		"engine": "sogou",
		"query":  "OpenKitty",
	})
	if result != "" || err == nil {
		t.Fatalf("canceled search = result %q err %v, want real tool error", result, err)
	}
}

func TestWebFetchIsParallelSafeReadOnly(t *testing.T) {
	registry := webFetchTestRegistry{tool: NewWebFetchTool(3, "", false, nil, false)}
	first := toolcontract.ToolCall{ID: "one", Name: "web_fetch", Arguments: map[string]interface{}{"engine": "sogou", "query": "alpha"}}
	second := toolcontract.ToolCall{ID: "two", Name: "web_fetch", Arguments: map[string]interface{}{"url": "https://example.com/"}}
	if class := toolcontract.ClassifyToolParallel(first.Name, first.Arguments, registry, nil); class != toolcontract.SafeParallel {
		t.Fatalf("web_fetch parallel class = %v, want SafeParallel", class)
	}
	if !toolcontract.CanRunConcurrently(first, second, registry, nil) {
		t.Fatal("independent read-only web_fetch calls were forced to run serially")
	}

	mutatingRegistry := webFetchTestRegistry{tool: NewWebFetchTool(3, "", true, nil, false)}
	if class := toolcontract.ClassifyToolParallel(first.Name, first.Arguments, mutatingRegistry, nil); class != toolcontract.MustSerial {
		t.Fatalf("auto-installing web_fetch parallel class = %v, want MustSerial", class)
	}
}

func TestWebFetchQueryOnlyUsesContextAwareDefaultEngine(t *testing.T) {
	if got := defaultWebSearchEngine(withWebFetchRetrievalGoal(context.Background(), newsDeliveryPrompt), "Apple OpenAI"); got != "bing_news" {
		t.Fatalf("current-news default engine = %q", got)
	}
	if got := defaultWebSearchEngine(context.Background(), "上海科技公司"); got != "sogou" {
		t.Fatalf("Chinese default engine = %q", got)
	}
	if got := defaultWebSearchEngine(context.Background(), "OpenAI release notes"); got != "duckduckgo" {
		t.Fatalf("English default engine = %q", got)
	}

	var requestedURL string
	fixture := `<?xml version="1.0"?><rss><channel><item><title>Verified event</title><link>https://publisher.example/news/verified-event</link><description>facts</description><pubDate>Fri, 10 Jul 2026 12:00:00 GMT</pubDate></item></channel></rss>`
	tool := NewWebFetchTool(5, "", false, nil, false)
	tool.SetHTTPClient(&http.Client{Transport: webFetchRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		requestedURL = req.URL.String()
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/xml"}}, Body: io.NopCloser(strings.NewReader(fixture)), Request: req}, nil
	})})
	result, err := tool.Execute(withWebFetchRetrievalGoal(context.Background(), newsDeliveryPrompt), map[string]interface{}{"query": "verified event"})
	if err != nil || !strings.Contains(result, "https://publisher.example/news/verified-event") || !strings.Contains(requestedURL, "cn.bing.com/search") {
		t.Fatalf("query-only current-news routing failed: url=%q result=%q err=%v", requestedURL, result, err)
	}
}

func TestWebFetchNetworkFailuresDoNotRetry(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{name: "dns", err: &net.DNSError{Err: "no such host", Name: "missing.example", IsNotFound: true}},
		{name: "timeout", err: webFetchTimeoutError{}},
		{name: "deadline", err: context.DeadlineExceeded},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			tool := NewWebFetchTool(3, "", false, nil, false)
			tool.SetHTTPClient(&http.Client{Transport: webFetchRoundTripFunc(func(req *http.Request) (*http.Response, error) {
				calls++
				return nil, test.err
			})})
			started := time.Now()
			if _, err := tool.FetchURLWithRetry(context.Background(), "https://missing.example/article", 0); err == nil {
				t.Fatal("expected network error")
			}
			if calls != 1 || time.Since(started) > 200*time.Millisecond {
				t.Fatalf("network failure retried or stalled: calls=%d elapsed=%v", calls, time.Since(started))
			}
		})
	}
}

func TestWebFetch404DoesNotLaunchBrowserFallback(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()
	tool := NewWebFetchTool(3, "", false, nil, false)
	started := time.Now()
	if _, err := tool.GenericFetchToMarkdown(context.Background(), server.URL+"/missing"); err == nil || !strings.Contains(err.Error(), "HTTP error: 404") {
		t.Fatalf("404 did not surface directly: %v", err)
	}
	if time.Since(started) > 500*time.Millisecond {
		t.Fatalf("404 triggered a slow browser fallback: %v", time.Since(started))
	}
}

func TestFastResearchSkipsLLMWebCompressionWithUTF8SafeExcerpt(t *testing.T) {
	compressionCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		compressionCalls++
		http.Error(w, "compression should not be called", http.StatusInternalServerError)
	}))
	defer server.Close()
	tool := NewWebFetchTool(3, "", false, &ModelConfig{ModelName: "compressor", BaseURL: server.URL}, false)
	tool.SetHTTPClient(server.Client())
	content := "# 来源\n\n" + strings.Repeat("科技事实甲乙丙。", 1000) + "\nTAIL-VERIFIED"
	result := tool.CompressContentLLM(withWebFetchRetrievalGoal(context.Background(), newsDeliveryPrompt), content, "https://publisher.example/article")
	if compressionCalls != 0 {
		t.Fatalf("fast research called the compression model %d time(s)", compressionCalls)
	}
	if len(result) > 7600 || !utf8.ValidString(result) || !strings.Contains(result, "deterministic excerpt") || !strings.Contains(result, "TAIL-VERIFIED") {
		t.Fatalf("unexpected deterministic excerpt: bytes=%d utf8=%v tail=%v", len(result), utf8.ValidString(result), strings.Contains(result, "TAIL-VERIFIED"))
	}
}

func TestWebFetchLiveNewsDiscoveryDiagnostic(t *testing.T) {
	if os.Getenv("OPENKITTY_LIVE_WEB_NEWS") != "1" {
		t.Skip("set OPENKITTY_LIVE_WEB_NEWS=1 to inspect public news discovery results")
	}
	tool := NewWebFetchTool(15, "", false, nil, false)
	queries := []struct{ engine, query string }{
		{"bing_news", "科技 商业"},
		{"bing_news", "technology business"},
	}
	for _, item := range queries {
		started := time.Now()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		result, err := tool.Execute(ctx, map[string]interface{}{"engine": item.engine, "query": item.query, "maxResults": float64(15)})
		cancel()
		if err != nil {
			t.Fatalf("engine=%s query=%q error=%v", item.engine, item.query, err)
		}
		if elapsed := time.Since(started); elapsed > 10*time.Second {
			t.Fatalf("engine=%s query=%q took %v", item.engine, item.query, elapsed)
		}
		if strings.Count(result, "URL: http") < 3 || strings.Count(result, "Publisher:") < 3 || strings.Contains(result, "bing.com/news/apiclick") {
			t.Fatalf("engine=%s query=%q did not return structured original article rows:\n%s", item.engine, item.query, result)
		}
		t.Logf("engine=%s query=%q chars=%d\n%s", item.engine, item.query, len(result), result)
	}
}

func requireWebFetchIntegration(t testing.TB) {
	t.Helper()
	if os.Getenv("OPENKITTY_RUN_WEB_INTEGRATION") != "1" {
		t.Skip("set OPENKITTY_RUN_WEB_INTEGRATION=1 to run live web_fetch integration tests")
	}
}

func TestWebFetchToolLocalGenericFetch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<!doctype html>
<html>
<head><title>OpenKitty Local Fixture</title></head>
<body>
<main>
<h1>OpenKitty Local Fixture</h1>
<p>RAG routing, citation checks, and delivery summaries are available in this fixture page.</p>
<p>The page is intentionally static so the test does not depend on live network services.</p>
</main>
</body>
</html>`))
	}))
	defer server.Close()

	tool := NewWebFetchTool(3, "", false, nil, false)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	result, err := tool.GenericFetch(ctx, server.URL)
	if err != nil {
		t.Fatalf("generic fetch local page: %v", err)
	}
	for _, want := range []string{"OpenKitty Local Fixture", "RAG routing", "delivery summaries"} {
		if !strings.Contains(result, want) {
			t.Fatalf("result missing %q:\n%s", want, result)
		}
	}
}

func TestURLEncoding(t *testing.T) {
	tests := []struct {
		query string
		want  string
	}{
		{"上海浦东新区创业园", "%E4%B8%8A%E6%B5%B7%E6%B5%A6%E4%B8%9C%E6%96%B0%E5%8C%BA%E5%88%9B%E4%B8%9A%E5%9B%AD"},
		{"python tutorial", "python+tutorial"},
		{"startup+incubator", "startup%2Bincubator"},
		{"创业园 租金 便宜", "%E5%88%9B%E4%B8%9A%E5%9B%AD+%E7%A7%9F%E9%87%91+%E4%BE%BF%E5%AE%9C"},
	}
	for _, tt := range tests {
		t.Run(tt.query, func(t *testing.T) {
			if got := url.QueryEscape(tt.query); got != tt.want {
				t.Fatalf("QueryEscape(%q) = %q, want %q", tt.query, got, tt.want)
			}
		})
	}
}

func TestDifferentEngines(t *testing.T) {
	tool := NewWebFetchTool(5, "", false, nil, false)
	query := "上海浦东新区创业园"
	tests := []struct {
		engine string
		want   string
	}{
		{"baidu", "https://www.baidu.com/s?wd="},
		{"360", "https://www.so.com/s?q="},
		{"sogou", "https://www.sogou.com/web?query="},
		{"bing", "https://www.bing.com/search?q="},
		{"bing_news", "https://cn.bing.com/search?format=RSS&q="},
	}
	for _, tt := range tests {
		t.Run(tt.engine, func(t *testing.T) {
			got := tool.BuildSearchURL(tt.engine, query)
			if !strings.HasPrefix(got, tt.want) {
				t.Fatalf("buildSearchURL(%q) = %q, want prefix %q", tt.engine, got, tt.want)
			}
			if !strings.Contains(got, url.QueryEscape(query)) {
				t.Fatalf("buildSearchURL(%q) did not include escaped query: %q", tt.engine, got)
			}
		})
	}
}

func TestParseBingNewsResultsPreservesPublicationAndOriginalArticleURL(t *testing.T) {
	fixture := `<?xml version="1.0" encoding="utf-8"?>
<rss version="2.0" xmlns:News="https://www.bing.com/news/search"><channel>
<item><title>Apple &amp; OpenAI court update</title>
<link>http://www.bing.com/news/apiclick.aspx?ref=FexRss&amp;url=https%3A%2F%2Fapnews.com%2Farticle%2Fapple-openai-case%3Futm_source%3Dbing</link>
<description><![CDATA[The filing was published on Friday.]]></description>
<pubDate>Fri, 10 Jul 2026 18:31:00 GMT</pubDate><News:Source>AP News</News:Source></item>
</channel></rss>`
	results, err := parseBingNewsResults(fixture, 10)
	if err != nil {
		t.Fatalf("parse Bing News RSS: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("results = %d, want 1: %#v", len(results), results)
	}
	result := results[0]
	combined := result.Title + "\n" + result.URL + "\n" + result.Content
	for _, want := range []string{"Apple & OpenAI", "https://apnews.com/article/apple-openai-case?utm_source=bing", "Published: Fri, 10 Jul 2026 18:31:00 GMT", "Publisher: AP News", "filing was published"} {
		if !strings.Contains(combined, want) {
			t.Fatalf("Bing News result missing %q: %#v", want, result)
		}
	}
}

func TestBingAliasUsesDeterministicSearchParser(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<html><body><ol><li class="b_algo"><h2><a href="https://publisher.example/news/exact-article">Exact article</a></h2><p>Deterministic Bing snippet.</p></li></ol></body></html>`))
	}))
	defer server.Close()
	tool := NewWebFetchTool(5, "", false, nil, false)
	result, err := tool.SearchWithEngine(context.Background(), "bing", server.URL, 5)
	if err != nil {
		t.Fatalf("bing alias search: %v", err)
	}
	for _, want := range []string{"Exact article", "https://publisher.example/news/exact-article", "Deterministic Bing snippet"} {
		if !strings.Contains(result, want) {
			t.Fatalf("bing alias did not use structured search parser; missing %q:\n%s", want, result)
		}
	}
}

func TestSearchBingNewsRejectsHTMLResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html><body>news landing page</body></html>"))
	}))
	defer server.Close()
	tool := NewWebFetchTool(5, "", false, nil, false)
	tool.SetHTTPClient(server.Client())
	if _, err := tool.SearchBingNews(context.Background(), server.URL, 5); err == nil || !strings.Contains(err.Error(), "non-RSS") {
		t.Fatalf("HTML landing page was accepted as Bing News RSS: %v", err)
	}
}

func TestWebFetchTool(t *testing.T) {
	requireWebFetchIntegration(t)
	tool := NewWebFetchTool(10, "", false, nil, false)
	testCases := []struct {
		name string
		url  string
	}{
		{name: "tencent_cloud_domain", url: "https://cloud.tencent.com/product/domain"},
		{name: "sicurrent_products", url: "https://www.sicurrent.com/products/"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			result, err := tool.Execute(ctx, map[string]interface{}{
				"url":        tc.url,
				"maxResults": 5,
			})
			if err != nil {
				t.Fatalf("fetch %s: %v", tc.url, err)
			}
			if strings.TrimSpace(result) == "" {
				t.Fatalf("fetch %s returned empty result", tc.url)
			}
			t.Logf("fetch %s returned %d chars", tc.url, len(result))
		})
	}
}

func TestFetchSpecificURL(t *testing.T) {
	requireWebFetchIntegration(t)
	tool := NewWebFetchTool(10, "", false, nil, false)
	target := "https://www.so.com/link?m=bxXoE6C%2B%2FVh%2BhUNH0GE21TagpZxMTLBBCuAO39326a0YpGmk1SP1loDlWS%2BY2n3I8ILxjwkDKJt6fXRzyTO5SwrzdyQ5HpLDyQ6Bmm2kgAeBdOEZdGxa8Pjp6Mnnmk6hMs7RFqHFeezgiK8Tu7b8BDATC0MzuYvPoQhNUNcFzpNeI01SD0RUc7GDctQgyUvUVLtEoFqgQzMMPE64U4hqdRrXWUYkPC2k7qJ2SVAvxLNnburfvnxfMek5Xh2zbhTELUI5XEqfaXZ6MvMrroqJHcCddugqrxzhFsd8h5tSJpbsBCu2OVHSYjJLBCFpPqXZHkv1iNA%3D%3D"

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	result, err := tool.Execute(ctx, map[string]interface{}{
		"url":        target,
		"maxResults": 5,
	})
	if err != nil {
		t.Fatalf("fetch specific URL: %v", err)
	}
	if strings.TrimSpace(result) == "" {
		t.Fatal("fetch specific URL returned empty result")
	}
}

func TestWithProxy(t *testing.T) {
	proxyURL := os.Getenv("OPENKITTY_WEB_PROXY")
	if proxyURL == "" {
		t.Skip("set OPENKITTY_WEB_PROXY to run proxy fetch test")
	}
	tool := NewWebFetchTool(5, proxyURL, false, nil, false)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	result, err := tool.Execute(ctx, map[string]interface{}{
		"url":        "https://www.google.com/search?q=test",
		"maxResults": 3,
	})
	if err != nil {
		t.Fatalf("proxy fetch failed: %v", err)
	}
	if strings.TrimSpace(result) == "" {
		t.Fatal("proxy fetch returned empty result")
	}
}

func TestWebCompressionPromptUsesTurnGoalAndExcludesUnrelatedPageChrome(t *testing.T) {
	ctx := withWebFetchRetrievalGoal(context.Background(), "查询上海未来7天天气，只要逐日气温和降雨")
	goal := webFetchRetrievalGoal(ctx)
	systemPrompt, userPrompt := webCompressionPrompts(goal, "上海天气", "https://weather.example/shanghai", "forecast and unrelated news", 5000)

	for _, want := range []string{
		"retrieval-focused content compressor",
		"Excludes unrelated navigation",
		"Never infers, converts units, upgrades a condition into a warning/advisory",
		"target/location/entity mismatches",
	} {
		if !strings.Contains(systemPrompt, want) {
			t.Fatalf("compression system prompt missing %q: %s", want, systemPrompt)
		}
	}
	for _, want := range []string{goal, "https://weather.example/shanghai", "<retrieval_goal>", "<page_content>"} {
		if !strings.Contains(userPrompt, want) {
			t.Fatalf("compression user prompt missing %q: %s", want, userPrompt)
		}
	}
}

func TestWebCompressionOutputStripsHiddenReasoning(t *testing.T) {
	raw := "<think>include every unrelated sidebar</think>\n\n## Relevant forecast\n- 07/11: rain"
	clean := cleanWebCompressionOutput(raw)
	if strings.Contains(clean, "<think>") || strings.Contains(clean, "unrelated sidebar") {
		t.Fatalf("compression reasoning leaked: %q", clean)
	}
	if !strings.Contains(clean, "Relevant forecast") {
		t.Fatalf("compression answer was lost: %q", clean)
	}
}

func TestWebFetchRetrievalGoalIsBoundedAndTurnLocal(t *testing.T) {
	longGoal := strings.Repeat("天气", 1000)
	ctx := withWebFetchRetrievalGoal(context.Background(), longGoal)
	if got := webFetchRetrievalGoal(ctx); got == "" || len([]rune(got)) > 1300 {
		t.Fatalf("bounded goal rune length = %d", len([]rune(got)))
	}
	if got := webFetchRetrievalGoal(context.Background()); got != "" {
		t.Fatalf("goal leaked into unrelated context: %q", got)
	}
}

func BenchmarkWebFetch(b *testing.B) {
	requireWebFetchIntegration(b)
	tool := NewWebFetchTool(5, "", false, nil, false)
	for i := 0; i < b.N; i++ {
		ctx := context.Background()
		_, err := tool.Execute(ctx, map[string]interface{}{
			"engine":     "360",
			"query":      "测试",
			"maxResults": 3,
		})
		if err != nil {
			b.Logf("fetch failed: %v", err)
		}
	}
}

func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "...[truncated]"
}

func RunManualTests() {
	fmt.Println("=== Manual WebFetchTool tests ===")
	tool := NewWebFetchTool(10, "", false, nil, false)
	testSearch(tool, "360", "上海浦东新区创业园", 5)
	time.Sleep(3 * time.Second)
	testSearch(tool, "sogou", "创业园 租金", 5)
	time.Sleep(3 * time.Second)
	testSearch(tool, "baidu", "浦东 注册公司", 5)
}

func testSearch(tool *WebFetchTool, engine, query string, maxResults int) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	result, err := tool.Execute(ctx, map[string]interface{}{
		"engine":     engine,
		"query":      query,
		"maxResults": maxResults,
	})
	if err != nil {
		fmt.Printf("error: %v\n", err)
		return
	}
	fmt.Println(truncateString(result, 800))
}
