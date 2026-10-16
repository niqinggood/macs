package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"mac/internal/toolcontract"
	networktools "mac/internal/tools/network"
)

const socialToolsNewsDeliveryPrompt = "整理今天（2026-07-11）值得关注的8条中国与全球科技/商业热点。每条必须包含事件时间、发布时间、核心事实、重要性和来源。"

func TestSocialTrendingRSSPreservesArticleLinksAndPublicationTime(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/rss+xml; charset=utf-8")
		_, _ = w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?><rss version="2.0"><channel><title>Newsroom</title><item><title>Verified launch</title><link>https://publisher.example/news/verified-launch</link><description><![CDATA[<p>Primary facts from the publisher.</p>]]></description><pubDate>Sat, 11 Jul 2026 00:22:00 GMT</pubDate></item></channel></rss>`))
	}))
	defer server.Close()

	tool := networktools.NewSocialTrendingToolWithClient(server.Client())
	result, err := tool.Execute(context.Background(), map[string]interface{}{
		"source": "rss", "url": server.URL + "/feed", "limit": float64(5),
	})
	if err != nil {
		t.Fatalf("RSS execute: %v", err)
	}
	for _, want := range []string{server.URL + "/feed", "Verified launch", "https://publisher.example/news/verified-launch", "Sat, 11 Jul 2026 00:22:00 GMT", "Primary facts"} {
		if !strings.Contains(result, want) {
			t.Fatalf("RSS result missing %q:\n%s", want, result)
		}
	}
}

func TestSocialTrendingCurrentNewsCapsRSSRows(t *testing.T) {
	var feed strings.Builder
	feed.WriteString(`<?xml version="1.0"?><rss><channel>`)
	for i := 1; i <= 15; i++ {
		fmt.Fprintf(&feed, `<item><title>Event %d</title><link>https://publisher.example/news/%d</link><description>%s</description><pubDate>2026-07-11</pubDate></item>`, i, i, strings.Repeat("事实", 200))
	}
	feed.WriteString(`</channel></rss>`)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(feed.String()))
	}))
	defer server.Close()
	tool := networktools.NewSocialTrendingToolWithClient(server.Client())
	ctx := withWebFetchRetrievalGoal(context.Background(), socialToolsNewsDeliveryPrompt)
	result, err := tool.Execute(ctx, map[string]interface{}{"source": "rss", "url": server.URL + "/feed", "limit": float64(25)})
	if err != nil {
		t.Fatalf("current-news RSS: %v", err)
	}
	if count := strings.Count(result, "### "); count != 10 {
		t.Fatalf("current-news RSS rows = %d, want 10", count)
	}
	if !strings.Contains(result, strings.Repeat("事实", 140)) {
		t.Fatal("current-news RSS description did not retain enough adjacent evidence")
	}
	if strings.Contains(result, strings.Repeat("事实", 160)) {
		t.Fatal("current-news RSS description was not bounded")
	}
}

func TestSocialTrendingCurrentNewsKeepsEnglishEvidenceCompact(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintf(w, `<?xml version="1.0"?><rss><channel><item><title>Event</title><link>https://publisher.example/news/1</link><description>%s</description><pubDate>2026-07-11</pubDate></item></channel></rss>`, strings.Repeat("fact ", 100))
	}))
	defer server.Close()
	tool := networktools.NewSocialTrendingToolWithClient(server.Client())
	ctx := withWebFetchRetrievalGoal(context.Background(), socialToolsNewsDeliveryPrompt)
	result, err := tool.Execute(ctx, map[string]interface{}{"source": "rss", "url": server.URL + "/feed", "limit": float64(10)})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, strings.Repeat("fact ", 30)) || strings.Contains(result, strings.Repeat("fact ", 40)) {
		t.Fatalf("English current-news evidence did not retain the 180-rune cap:\n%s", result)
	}
}

func TestSocialTrendingAtomPreservesHrefLink(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/atom+xml; charset=utf-8")
		_, _ = w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?><feed xmlns="http://www.w3.org/2005/Atom"><title>Tech Feed</title><entry><title>New product</title><link rel="self" href="/atom/entry.xml"/><link rel="alternate" href="https://publisher.example/tech/new-product"/><published>2026-07-10T23:45:00Z</published><updated>2026-07-11T00:30:00Z</updated><summary>Product facts.</summary></entry></feed>`))
	}))
	defer server.Close()

	tool := networktools.NewSocialTrendingToolWithClient(server.Client())
	result, err := tool.Execute(context.Background(), map[string]interface{}{
		"source": "rss", "url": server.URL + "/atom", "limit": float64(5),
	})
	if err != nil {
		t.Fatalf("Atom execute: %v", err)
	}
	for _, want := range []string{"New product", "https://publisher.example/tech/new-product", "📅 Published: 2026-07-10T23:45:00Z", "🔄 Updated: 2026-07-11T00:30:00Z"} {
		if !strings.Contains(result, want) {
			t.Fatalf("Atom result missing %q:\n%s", want, result)
		}
	}
}

func TestSocialTrendingRSSLabelsPubDateAsPublished(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<?xml version="1.0"?><rss><channel><item><title>Event</title><link>https://publisher.example/news/event</link><pubDate>Sat, 11 Jul 2026 00:22:00 GMT</pubDate></item></channel></rss>`))
	}))
	defer server.Close()
	tool := networktools.NewSocialTrendingToolWithClient(server.Client())
	result, err := tool.Execute(context.Background(), map[string]interface{}{"source": "rss", "url": server.URL, "limit": float64(1)})
	if err != nil {
		t.Fatalf("RSS execute: %v", err)
	}
	if !strings.Contains(result, "📅 Published: Sat, 11 Jul 2026 00:22:00 GMT") {
		t.Fatalf("RSS pubDate semantics were not explicit:\n%s", result)
	}
}

func TestSocialTrendingIsCoreParallelDirectEvidence(t *testing.T) {
	registry := NewToolRegistry()
	registry.Register(networktools.NewSocialTrendingTool())
	call := ToolCall{Name: "social_trending", Arguments: map[string]interface{}{
		"source": "rss", "url": "https://publisher.example/feed.xml", "limit": float64(8),
	}}
	if !CoreTools["social_trending"] {
		t.Fatal("social_trending is referenced by the live-news prompt but its schema is not core-visible")
	}
	if class := toolcontract.ClassifyToolParallel(call.Name, call.Arguments, registry, nil); class != toolcontract.SafeParallel {
		t.Fatalf("social_trending parallel class = %v, want SafeParallel", class)
	}
	if mode := searchModeForToolCall(call); mode != "direct" {
		t.Fatalf("social_trending search mode = %q, want direct", mode)
	}
	if source := sourceURLForToolCall(call); source != "https://publisher.example/feed.xml" {
		t.Fatalf("social_trending source URL = %q", source)
	}
}

func TestSocialTrendingRSSRejectsHTTPFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()
	tool := networktools.NewSocialTrendingToolWithClient(server.Client())
	if _, err := tool.Execute(context.Background(), map[string]interface{}{"source": "rss", "url": server.URL}); err == nil || !strings.Contains(err.Error(), "HTTP 503") {
		t.Fatalf("RSS HTTP failure was not surfaced: %v", err)
	}
}

func TestSocialTrendingRSSRejectsHTMLAndResolvesRelativeArticleLinks(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/html" {
			_, _ = w.Write([]byte("<html><body>not a feed</body></html>"))
			return
		}
		_, _ = w.Write([]byte(`<?xml version="1.0"?><rss><channel><item><title>Relative</title><link>/articles/relative</link></item></channel></rss>`))
	}))
	defer server.Close()
	tool := networktools.NewSocialTrendingToolWithClient(server.Client())
	if _, err := tool.Execute(context.Background(), map[string]interface{}{"source": "rss", "url": server.URL + "/html"}); err == nil {
		t.Fatal("HTML response was accepted as direct RSS evidence")
	}
	result, err := tool.Execute(context.Background(), map[string]interface{}{"source": "rss", "url": server.URL + "/feed"})
	if err != nil || !strings.Contains(result, server.URL+"/articles/relative") {
		t.Fatalf("relative RSS link was not normalized: result=%q err=%v", result, err)
	}
}

func TestSocialTrendingRSSHonorsPrivateNetworkProtection(t *testing.T) {
	tool := networktools.NewSocialTrendingTool(true)
	result, err := tool.Execute(context.Background(), map[string]interface{}{"source": "rss", "url": "http://127.0.0.1:18090/feed"})
	if result != "" || !errors.Is(err, errPrivateNetworkAccessBlocked) {
		t.Fatalf("private RSS URL was not blocked: result=%q err=%v", result, err)
	}
}

func TestSocialTrendingLivePublisherFeeds(t *testing.T) {
	if os.Getenv("OPENKITTY_LIVE_WEB_NEWS") != "1" {
		t.Skip("set OPENKITTY_LIVE_WEB_NEWS=1 to validate publisher feeds")
	}
	feeds := []string{
		"https://www.ithome.com/rss/",
		"https://www.36kr.com/feed",
		"https://techcrunch.com/feed/",
		"https://www.theverge.com/rss/index.xml",
		"https://feeds.arstechnica.com/arstechnica/index",
		"https://www.cnbc.com/id/19854910/device/rss/rss.html",
	}
	tool := networktools.NewSocialTrendingTool()
	type feedResult struct {
		feed    string
		result  string
		err     error
		elapsed time.Duration
	}
	results := make(chan feedResult, len(feeds))
	for _, feed := range feeds {
		go func(feed string) {
			started := time.Now()
			result, err := tool.Execute(context.Background(), map[string]interface{}{"source": "rss", "url": feed, "limit": float64(12)})
			results <- feedResult{feed: feed, result: result, err: err, elapsed: time.Since(started)}
		}(feed)
	}
	succeeded := 0
	for range feeds {
		outcome := <-results
		feed, result, err := outcome.feed, outcome.result, outcome.err
		if err != nil {
			t.Logf("feed=%s error=%v", feed, err)
			continue
		}
		if outcome.elapsed > networktools.RSSRequestTimeout+time.Second {
			t.Errorf("feed=%s took %v", feed, outcome.elapsed)
		}
		if strings.Count(result, "](http") < 3 || !strings.Contains(result, "Source: "+feed) {
			t.Logf("feed=%s unusable structured result:\n%s", feed, result)
			continue
		}
		succeeded++
	}
	if succeeded < 5 {
		t.Fatalf("only %d/%d publisher feeds returned structured article rows", succeeded, len(feeds))
	}
}
