package network

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode"

	"mac/internal/textutil"
)

// ====================== Social Trending Tool (unified) ======================

type SocialTrendingTool struct {
	client                   *http.Client
	privateNetworkProtection bool
}

const RSSRequestTimeout = 8 * time.Second

func NewSocialTrendingTool(privateNetworkProtection ...bool) *SocialTrendingTool {
	tool := &SocialTrendingTool{client: &http.Client{Timeout: 15 * time.Second}}
	if len(privateNetworkProtection) > 0 {
		tool.privateNetworkProtection = privateNetworkProtection[0]
	}
	return tool
}

// NewSocialTrendingToolWithClient creates a social trend tool with an injected
// HTTP client for controlled transports and hermetic tests.
func NewSocialTrendingToolWithClient(client *http.Client, privateNetworkProtection ...bool) *SocialTrendingTool {
	tool := NewSocialTrendingTool(privateNetworkProtection...)
	if client != nil {
		tool.client = client
	}
	return tool
}

func (t *SocialTrendingTool) Name() string       { return "social_trending" }
func (t *SocialTrendingTool) IsReadOnly() bool   { return true }
func (t *SocialTrendingTool) MaxResultSize() int { return 50000 }

func (t *SocialTrendingTool) Description() string {
	return "Get trending/hot topics from 16 Chinese and global platforms. " +
		"Sources: bilibili(B站), weibo(微博), zhihu(知乎), v2ex, hackernews, " +
		"twitter(推特 via Nitter RSS), xiaohongshu(小红书 搜索需cookie), " +
		"reddit, youtube(频道RSS), github(trending), douyin(抖音), " +
		"xueqiu(雪球 需cookie), boss(直聘), linkedin, xiaoyuzhou(小宇宙), rss(任意RSS). " +
		"Zero API key. Some platforms need optional cookie/url params. " +
		"Use to check internet trends, breaking news, public opinion, job market."
}

func (t *SocialTrendingTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"source": map[string]interface{}{
				"type":        "string",
				"description": "Platform: bilibili, weibo, zhihu, v2ex, hackernews, rss",
				"enum":        []string{"bilibili", "weibo", "zhihu", "v2ex", "hackernews", "twitter", "xiaohongshu", "reddit", "youtube", "github", "douyin", "xueqiu", "boss", "linkedin", "xiaoyuzhou", "rss"},
			},
			"limit": map[string]interface{}{
				"type":        "integer",
				"description": "Number of results (1-50, default 20)",
				"default":     20,
			},
			"url": map[string]interface{}{
				"type":        "string",
				"description": "RSS/Atom feed URL (required when source=rss). For twitter: optional nitter RSS URL (e.g. https://nitter.net/elonmusk/rss). For xiaohongshu: optional feed URL.",
			},
			"query": map[string]interface{}{
				"type":        "string",
				"description": "Search query. For twitter: search tweets. For xiaohongshu: search notes.",
			},
			"cookie": map[string]interface{}{
				"type":        "string",
				"description": "Optional: cookie string for platforms that need login (e.g. xiaohongshu). Obtain from browser dev tools after logging in.",
			},
		},
		"required": []string{"source"},
	}
}

func (t *SocialTrendingTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	source, _ := params["source"].(string)
	limit := clampLimit(params, 20, 50)
	if strings.EqualFold(strings.TrimSpace(source), "rss") && IsCurrentNewsBrief(RetrievalGoal(ctx)) && limit > 10 {
		limit = 10
	}

	switch strings.ToLower(source) {
	case "bilibili":
		return t.bilibiliHot(ctx, limit)
	case "weibo":
		return t.weiboHot(ctx, limit)
	case "zhihu":
		return t.zhihuHot(ctx, limit)
	case "v2ex":
		return t.v2exHot(ctx, limit)
	case "hackernews":
		return t.hackernewsHot(ctx, limit)
	case "twitter":
		query, _ := params["query"].(string)
		feedURL, _ := params["url"].(string)
		return t.twitterFeed(ctx, query, feedURL, limit)
	case "xiaohongshu":
		query, _ := params["query"].(string)
		cookie, _ := params["cookie"].(string)
		return t.xiaohongshuFeed(ctx, query, cookie, limit)
	case "reddit":
		query, _ := params["query"].(string)
		feedURL, _ := params["url"].(string)
		return t.redditFeed(ctx, query, feedURL, limit)
	case "youtube":
		query, _ := params["query"].(string)
		feedURL, _ := params["url"].(string)
		return t.youtubeFeed(ctx, query, feedURL, limit)
	case "github":
		query, _ := params["query"].(string)
		return t.githubTrending(ctx, query, limit)
	case "douyin":
		return t.douyinHot(ctx, limit)
	case "xueqiu":
		cookie, _ := params["cookie"].(string)
		return t.xueqiuHot(ctx, cookie, limit)
	case "boss":
		return t.bossHot(ctx, limit)
	case "linkedin":
		query, _ := params["query"].(string)
		return t.linkedinFeed(ctx, query, limit)
	case "xiaoyuzhou":
		query, _ := params["query"].(string)
		return t.xiaoyuzhouFeed(ctx, query, limit)
	case "rss":
		feedURL, _ := params["url"].(string)
		if feedURL == "" {
			return "", fmt.Errorf("url is required for rss source")
		}
		return t.rssFetch(ctx, feedURL, limit)
	default:
		return "", fmt.Errorf("unsupported source: %s (use: bilibili, weibo, zhihu, v2ex, hackernews, rss)", source)
	}
}

// ========== Bilibili ==========

func (t *SocialTrendingTool) bilibiliHot(ctx context.Context, limit int) (string, error) {
	body, err := t.httpGet(ctx,
		fmt.Sprintf("https://api.bilibili.com/x/web-interface/popular?pn=1&ps=%d", limit),
		map[string]string{"Referer": "https://www.bilibili.com/"},
	)
	if err != nil {
		return "", err
	}

	var r struct {
		Code int `json:"code"`
		Data struct {
			List []struct {
				Title string                `json:"title"`
				Owner struct{ Name string } `json:"owner"`
				Stat  struct {
					View     int64 `json:"view"`
					Danmaku  int64 `json:"danmaku"`
					Favorite int64 `json:"favorite"`
				} `json:"stat"`
				Desc string `json:"desc"`
			} `json:"list"`
		} `json:"data"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return rawFallback("B站热门", body, 2000), nil
	}
	if r.Code != 0 {
		return "", fmt.Errorf("bilibili: %s", r.Message)
	}

	var out strings.Builder
	out.WriteString("## B站热门视频\n\n")
	for i, v := range r.Data.List {
		out.WriteString(fmt.Sprintf("%d. **%s**\n", i+1, v.Title))
		out.WriteString(fmt.Sprintf("   UP: %s | 播放:%d 弹幕:%d 收藏:%d\n",
			v.Owner.Name, v.Stat.View, v.Stat.Danmaku, v.Stat.Favorite))
		if v.Desc != "" {
			desc := v.Desc
			if len([]rune(desc)) > 100 {
				desc = string([]rune(desc)[:100]) + "..."
			}
			out.WriteString(fmt.Sprintf("   %s\n", desc))
		}
		out.WriteString("\n")
	}
	return out.String(), nil
}

// ========== Weibo ==========

func (t *SocialTrendingTool) weiboHot(ctx context.Context, limit int) (string, error) {
	body, err := t.httpGet(ctx,
		"https://weibo.com/ajax/side/hotSearch",
		map[string]string{
			"Referer":          "https://weibo.com/",
			"X-Requested-With": "XMLHttpRequest",
		},
	)
	if err != nil {
		return "", err
	}

	var r struct {
		Ok   int `json:"ok"`
		Data struct {
			RealTime []struct {
				Word     string `json:"word"`
				WordRaw  string `json:"word_scheme"`
				Num      int64  `json:"num"`
				Category string `json:"category"`
				IconDesc string `json:"icon_desc"`
			} `json:"realtime"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return rawFallback("微博热搜", body, 2000), nil
	}
	if r.Ok != 1 || len(r.Data.RealTime) == 0 {
		return "", fmt.Errorf("weibo: no data")
	}

	var out strings.Builder
	out.WriteString("## 微博热搜\n\n")
	for i, v := range r.Data.RealTime {
		if i >= limit {
			break
		}
		word := v.Word
		if word == "" {
			word = v.WordRaw
		}
		tag := ""
		if v.IconDesc != "" {
			tag = fmt.Sprintf("[%s]", v.IconDesc)
		}
		hot := ""
		if v.Num > 0 {
			hot = fmt.Sprintf(" 🔥%d", v.Num)
		}
		out.WriteString(fmt.Sprintf("%d. %s **%s**%s\n", i+1, tag, word, hot))
	}
	return out.String(), nil
}

// ========== Zhihu ==========

func (t *SocialTrendingTool) zhihuHot(ctx context.Context, limit int) (string, error) {
	body, err := t.httpGet(ctx,
		fmt.Sprintf("https://www.zhihu.com/api/v3/feed/topstory/hot-lists/total?limit=%d", limit),
		map[string]string{"Referer": "https://www.zhihu.com/"},
	)
	if err != nil {
		return "", err
	}

	var r struct {
		Data []struct {
			Target struct {
				Title   string `json:"title"`
				Excerpt string `json:"excerpt"`
				URL     string `json:"url"`
			} `json:"target"`
			DetailText string `json:"detail_text"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return rawFallback("知乎热榜", body, 2000), nil
	}

	var out strings.Builder
	out.WriteString("## 知乎热榜\n\n")
	for i, v := range r.Data {
		out.WriteString(fmt.Sprintf("%d. **%s**\n", i+1, v.Target.Title))
		if v.DetailText != "" {
			out.WriteString(fmt.Sprintf("   %s\n", v.DetailText))
		}
		out.WriteString("\n")
	}
	return out.String(), nil
}

// ========== V2EX ==========

func (t *SocialTrendingTool) v2exHot(ctx context.Context, limit int) (string, error) {
	body, err := t.httpGet(ctx,
		"https://www.v2ex.com/api/topics/hot.json",
		map[string]string{"Referer": "https://www.v2ex.com/"},
	)
	if err != nil {
		return "", err
	}

	var topics []struct {
		Title   string `json:"title"`
		URL     string `json:"url"`
		Replies int    `json:"replies"`
		Node    struct {
			Title string `json:"title"`
		} `json:"node"`
	}
	if err := json.Unmarshal(body, &topics); err != nil {
		return rawFallback("V2EX热帖", body, 2000), nil
	}

	var out strings.Builder
	out.WriteString("## V2EX 热帖\n\n")
	for i, v := range topics {
		if i >= limit {
			break
		}
		out.WriteString(fmt.Sprintf("%d. **%s** [%s]\n", i+1, v.Title, v.Node.Title))
		out.WriteString(fmt.Sprintf("   %d 回复 | %s\n\n", v.Replies, v.URL))
	}
	return out.String(), nil
}

// ========== HackerNews ==========

func (t *SocialTrendingTool) hackernewsHot(ctx context.Context, limit int) (string, error) {
	// Use hnrss.org for reliable structured feed
	return t.rssFetch(ctx, "https://hnrss.org/frontpage?count="+fmt.Sprintf("%d", limit), limit)
}

// ========== RSS ==========

type rssFeed struct {
	XMLName xml.Name  `xml:"rss"`
	Title   string    `xml:"channel>title"`
	Desc    string    `xml:"channel>description"`
	Items   []rssItem `xml:"channel>item"`
}

type rssItem struct {
	Title   string `xml:"title"`
	Link    string `xml:"link"`
	Desc    string `xml:"description"`
	PubDate string `xml:"pubDate"`
	Creator string `xml:"http://purl.org/dc/elements/1.1/ creator"`
	Content string `xml:"http://purl.org/rss/1.0/modules/content/ encoded"`
}

type atomFeed struct {
	XMLName xml.Name    `xml:"feed"`
	Title   string      `xml:"title"`
	Entries []atomEntry `xml:"entry"`
}

type atomEntry struct {
	Title     string     `xml:"title"`
	Links     []atomLink `xml:"link"`
	Summary   string     `xml:"summary"`
	Published string     `xml:"published"`
	Updated   string     `xml:"updated"`
}

type atomLink struct {
	Href string `xml:"href,attr"`
	Rel  string `xml:"rel,attr"`
	Type string `xml:"type,attr"`
	Text string `xml:",chardata"`
}

func (t *SocialTrendingTool) rssFetch(ctx context.Context, feedURL string, limit int) (string, error) {
	feedURL = strings.TrimSpace(feedURL)
	parsedFeedURL, err := url.Parse(feedURL)
	if err != nil || (parsedFeedURL.Scheme != "http" && parsedFeedURL.Scheme != "https") || parsedFeedURL.Hostname() == "" {
		return "", fmt.Errorf("invalid RSS/Atom feed URL: %q", feedURL)
	}
	if t != nil && t.privateNetworkProtection && IsPrivateHost(parsedFeedURL.Host) {
		return "", fmt.Errorf("%w: 内网保护已开启，禁止访问内网或本机地址 (%s)", ErrPrivateNetworkAccessBlocked, parsedFeedURL.Host)
	}
	requestCtx, cancel := context.WithTimeout(ctx, RSSRequestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(requestCtx, "GET", feedURL, nil)
	if err != nil {
		return "", fmt.Errorf("rss request: %w", err)
	}
	req.Header.Set("User-Agent", "openkitty/1.0")
	req.Header.Set("Accept", "application/rss+xml, application/atom+xml, application/xml, text/xml")

	resp, err := t.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("rss fetch: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("rss fetch: HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return "", fmt.Errorf("rss read: %w", err)
	}

	// RSS
	currentNews := IsCurrentNewsBrief(RetrievalGoal(ctx))
	descriptionLimit := 300
	if currentNews {
		descriptionLimit = 180
	}
	var rss rssFeed
	if xml.Unmarshal(body, &rss) == nil && len(rss.Items) > 0 {
		var out strings.Builder
		validLinks := 0
		out.WriteString(fmt.Sprintf("Source: %s\n\n", feedURL))
		if rss.Title != "" {
			out.WriteString(fmt.Sprintf("## %s\n\n", rss.Title))
		}
		for i, item := range rss.Items {
			if i >= limit {
				break
			}
			out.WriteString(fmt.Sprintf("### %d. %s\n", i+1, item.Title))
			if itemURL := resolveFeedEntryURL(parsedFeedURL, item.Link); itemURL != "" {
				out.WriteString(fmt.Sprintf("[%s](%s)\n", itemURL, itemURL))
				validLinks++
			}
			if item.PubDate != "" {
				out.WriteString(fmt.Sprintf("📅 Published: %s\n", item.PubDate))
			}
			desc := item.Content
			if desc == "" {
				desc = item.Desc
			}
			if desc != "" {
				desc = textutil.StripHTML(desc)
				itemDescriptionLimit := currentNewsEvidenceDescriptionLimit(desc, descriptionLimit, currentNews)
				if len([]rune(desc)) > itemDescriptionLimit {
					desc = string([]rune(desc)[:itemDescriptionLimit]) + "..."
				}
				out.WriteString(fmt.Sprintf("%s\n", desc))
			}
			out.WriteString("\n")
		}
		if validLinks == 0 {
			return "", errors.New("RSS feed contains no usable HTTP(S) article links")
		}
		return out.String(), nil
	}

	// Atom
	var atom atomFeed
	if xml.Unmarshal(body, &atom) == nil && len(atom.Entries) > 0 {
		var out strings.Builder
		validLinks := 0
		out.WriteString(fmt.Sprintf("Source: %s\n\n", feedURL))
		if atom.Title != "" {
			out.WriteString(fmt.Sprintf("## %s\n\n", atom.Title))
		}
		for i, entry := range atom.Entries {
			if i >= limit {
				break
			}
			out.WriteString(fmt.Sprintf("### %d. %s\n", i+1, entry.Title))
			entryURL := preferredAtomEntryURL(parsedFeedURL, entry.Links)
			if entryURL != "" {
				out.WriteString(fmt.Sprintf("[%s](%s)\n", entryURL, entryURL))
				validLinks++
			}
			if entry.Published != "" {
				out.WriteString(fmt.Sprintf("📅 Published: %s\n", entry.Published))
			}
			if entry.Updated != "" {
				out.WriteString(fmt.Sprintf("🔄 Updated: %s\n", entry.Updated))
			}
			if entry.Summary != "" {
				desc := textutil.StripHTML(entry.Summary)
				itemDescriptionLimit := currentNewsEvidenceDescriptionLimit(desc, descriptionLimit, currentNews)
				if len([]rune(desc)) > itemDescriptionLimit {
					desc = string([]rune(desc)[:itemDescriptionLimit]) + "..."
				}
				out.WriteString(fmt.Sprintf("%s\n", desc))
			}
			out.WriteString("\n")
		}
		if validLinks == 0 {
			return "", errors.New("Atom feed contains no usable HTTP(S) article links")
		}
		return out.String(), nil
	}

	return "", errors.New("response is not a usable RSS or Atom feed")
}

func currentNewsEvidenceDescriptionLimit(description string, base int, currentNews bool) int {
	if !currentNews || base <= 0 {
		return base
	}
	// A Chinese full-text RSS paragraph packs subject, action, and comparison
	// qualifiers into a small rune budget; retaining 300 runes often captures
	// the adjacent sentence that says who acted or whether volume stayed flat.
	// English/Atom feeds remain at 180 runes to keep the five-feed synthesis well
	// below the model's one-minute latency budget.
	letters, han := 0, 0
	for _, r := range description {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			letters++
		}
		if unicode.In(r, unicode.Han) {
			han++
		}
	}
	if han >= 20 && letters > 0 && han*3 >= letters {
		return 300
	}
	return base
}

func resolveFeedEntryURL(base *url.URL, raw string) string {
	raw = strings.TrimSpace(raw)
	if base == nil || raw == "" {
		return ""
	}
	ref, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	resolved := base.ResolveReference(ref)
	if (resolved.Scheme != "http" && resolved.Scheme != "https") || resolved.Hostname() == "" {
		return ""
	}
	return resolved.String()
}

func preferredAtomEntryURL(base *url.URL, links []atomLink) string {
	for _, preferredRel := range []string{"alternate", ""} {
		for _, link := range links {
			if !strings.EqualFold(strings.TrimSpace(link.Rel), preferredRel) {
				continue
			}
			if candidate := resolveFeedEntryURL(base, firstNonEmptyFeedString(link.Href, link.Text)); candidate != "" {
				return candidate
			}
		}
	}
	for _, link := range links {
		if candidate := resolveFeedEntryURL(base, firstNonEmptyFeedString(link.Href, link.Text)); candidate != "" {
			return candidate
		}
	}
	return ""
}

func firstNonEmptyFeedString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

// ========== Twitter/X (via Nitter RSS) ==========

func (t *SocialTrendingTool) twitterFeed(ctx context.Context, query, feedURL string, limit int) (string, error) {
	if feedURL != "" {
		return t.rssFetch(ctx, feedURL, limit)
	}
	if query != "" {
		// Search via nitter RSS
		return t.rssFetch(ctx,
			fmt.Sprintf("https://nitter.net/search/rss?q=%s", url.QueryEscape(query)),
			limit)
	}
	return "", fmt.Errorf("twitter requires 'query' (search term) or 'url' (nitter RSS feed URL). Example nitter instances: nitter.net, nitter.poast.org, nitter.1d4.us")
}

// ========== Xiaohongshu (小红书) ==========

func (t *SocialTrendingTool) xiaohongshuFeed(ctx context.Context, query, cookie string, limit int) (string, error) {
	if query == "" {
		return t.rssFetch(ctx, "https://www.xiaohongshu.com/explore", limit)
	}

	searchURL := fmt.Sprintf("https://edith.xiaohongshu.com/api/sns/web/v1/search/notes?keyword=%s&page=1&page_size=%d&sort=general",
		url.QueryEscape(query), limit)

	headers := map[string]string{
		"Referer": "https://www.xiaohongshu.com/",
		"Origin":  "https://www.xiaohongshu.com",
		"Accept":  "application/json",
	}
	if cookie != "" {
		headers["Cookie"] = cookie
	}

	req, err := http.NewRequestWithContext(ctx, "GET", searchURL, nil)
	if err != nil {
		return "", fmt.Errorf("xiaohongshu request: %w", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := t.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("xiaohongshu fetch: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	var r struct {
		Success bool `json:"success"`
		Data    struct {
			Items []struct {
				NoteCard *struct {
					DisplayTitle string `json:"display_title"`
					Desc         string `json:"desc"`
					User         struct {
						Nickname string `json:"nickname"`
					} `json:"user"`
					InteractInfo struct {
						LikedCount     string `json:"liked_count"`
						CollectedCount string `json:"collected_count"`
					} `json:"interact_info"`
				} `json:"note_card"`
			} `json:"items"`
		} `json:"data"`
	}

	if err := json.Unmarshal(body, &r); err != nil || !r.Success {
		if strings.Contains(string(body), "登录") || strings.Contains(string(body), "login") {
			return "", fmt.Errorf("xiaohongshu requires login cookie. Get cookie from browser dev tools (Network tab → any xiaohongshu.com request → Cookie header) and pass as 'cookie' param")
		}
		return rawFallback("小红书 (raw)", body, 2000), nil
	}

	var out strings.Builder
	out.WriteString(fmt.Sprintf("## 小红书 · %s\n\n", query))
	for i, item := range r.Data.Items {
		if item.NoteCard == nil {
			continue
		}
		card := item.NoteCard
		title := card.DisplayTitle
		if title == "" {
			title = card.Desc
			if len([]rune(title)) > 80 {
				title = string([]rune(title)[:80])
			}
		}
		likes := card.InteractInfo.LikedCount
		if likes == "" {
			likes = "0"
		}
		out.WriteString(fmt.Sprintf("%d. **%s**\n", i+1, title))
		out.WriteString(fmt.Sprintf("   作者: %s | ❤️%s | ⭐%s\n\n",
			card.User.Nickname, likes, card.InteractInfo.CollectedCount))
	}
	return out.String(), nil
}

// ========== Reddit (via RSS) ==========

func (t *SocialTrendingTool) redditFeed(ctx context.Context, query, feedURL string, limit int) (string, error) {
	if feedURL != "" {
		return t.rssFetch(ctx, feedURL, limit)
	}
	if query != "" {
		// query can be a subreddit name like "golang" or "news"
		return t.rssFetch(ctx,
			fmt.Sprintf("https://www.reddit.com/r/%s/hot/.rss?limit=%d", url.QueryEscape(query), limit),
			limit)
	}
	// Default: Reddit front page
	return t.rssFetch(ctx,
		fmt.Sprintf("https://www.reddit.com/hot/.rss?limit=%d", limit),
		limit)
}

// ========== YouTube (via RSS) ==========

func (t *SocialTrendingTool) youtubeFeed(ctx context.Context, query, feedURL string, limit int) (string, error) {
	if feedURL != "" {
		return t.rssFetch(ctx, feedURL, limit)
	}
	if query != "" {
		// query can be channel ID like "UC_x5XG1OV2P6uZZ5FSM9Ttw" (Google Developers)
		return t.rssFetch(ctx,
			fmt.Sprintf("https://www.youtube.com/feeds/videos.xml?channel_id=%s", url.QueryEscape(query)),
			limit)
	}
	return "", fmt.Errorf("youtube requires 'query' (channel ID) or 'url' (channel RSS URL). Get channel ID from youtube.com/channel/xxx URL.")
}

// ========== GitHub Trending (via Atom RSS) ==========

func (t *SocialTrendingTool) githubTrending(ctx context.Context, query string, limit int) (string, error) {
	// GitHub trending as Atom feed
	feedURL := "https://github.com/trending.atom"
	if query != "" {
		feedURL = fmt.Sprintf("https://github.com/trending/%s?since=daily.atom", url.QueryEscape(query))
	}
	return t.rssFetch(ctx, feedURL, limit)
}

// ========== Douyin (抖音) Hot ==========

func (t *SocialTrendingTool) douyinHot(ctx context.Context, limit int) (string, error) {
	// Douyin hot search API (public, no auth needed)
	body, err := t.httpGet(ctx,
		"https://www.douyin.com/aweme/v1/web/hot/search/list/",
		map[string]string{
			"Referer": "https://www.douyin.com/",
		},
	)
	if err != nil {
		return "", err
	}

	var r struct {
		StatusCode int `json:"status_code"`
		Data       struct {
			WordList []struct {
				Word     string `json:"word"`
				HotValue int    `json:"hot_value"`
				Position int    `json:"position"`
			} `json:"word_list"`
			ActiveTime string `json:"active_time"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &r); err != nil || r.StatusCode != 0 {
		return rawFallback("抖音热搜 (raw)", body, 2000), nil
	}

	var out strings.Builder
	out.WriteString("## 抖音热搜\n\n")
	for i, v := range r.Data.WordList {
		if i >= limit {
			break
		}
		out.WriteString(fmt.Sprintf("%d. **%s**", i+1, v.Word))
		if v.HotValue > 0 {
			out.WriteString(fmt.Sprintf(" 🔥%d", v.HotValue))
		}
		out.WriteString("\n")
	}
	return out.String(), nil
}

// ========== Xueqiu (雪球) Hot ==========

func (t *SocialTrendingTool) xueqiuHot(ctx context.Context, cookie string, limit int) (string, error) {
	// Xueqiu hot posts API — may need cookie to bypass WAF
	headers := map[string]string{
		"Referer": "https://xueqiu.com/",
		"Accept":  "application/json",
	}
	if cookie != "" {
		headers["Cookie"] = cookie
	}
	// Try the hot list API
	body, err := t.httpGet(ctx,
		fmt.Sprintf("https://xueqiu.com/statuses/hot/listV2.json?page=1&size=%d", limit),
		headers,
	)
	if err != nil {
		return "", err
	}

	var r struct {
		Items []struct {
			Title       string `json:"title"`
			Description string `json:"description"`
			Text        string `json:"text"`
			ReplyCount  int    `json:"reply_count"`
			ViewCount   int64  `json:"view_count"`
			User        struct {
				ScreenName string `json:"screen_name"`
			} `json:"user"`
		} `json:"items"`
	}
	if err := json.Unmarshal(body, &r); err == nil && len(r.Items) > 0 {
		var out strings.Builder
		out.WriteString("## 雪球热帖\n\n")
		for i, item := range r.Items {
			text := item.Title
			if text == "" {
				text = item.Description
			}
			if text == "" {
				text = item.Text
			}
			if len([]rune(text)) > 120 {
				text = string([]rune(text)[:120]) + "..."
			}
			out.WriteString(fmt.Sprintf("%d. **%s**\n", i+1, text))
			out.WriteString(fmt.Sprintf("   %s | 💬%d | 👁%d\n\n",
				item.User.ScreenName, item.ReplyCount, item.ViewCount))
		}
		return out.String(), nil
	}

	// WAF blocked — user needs cookie
	if strings.Contains(string(body), "aliyun_waf") || strings.Contains(string(body), "waf") {
		return "", fmt.Errorf("雪球 API 被 WAF 拦截。请在浏览器登录 xueqiu.com 后，从 DevTools → Network → 任意请求 → 复制 Cookie 值，作为 'cookie' 参数传入")
	}
	return rawFallback("雪球 (raw)", body, 2000), nil
}

// ========== BOSS直聘 Hot ==========

func (t *SocialTrendingTool) bossHot(ctx context.Context, limit int) (string, error) {
	// BOSS直聘 doesn't have a public hot API. Use RSS/web search fallback.
	// Try to scrape the main page for hot jobs (limited, may need browser)
	return "", fmt.Errorf("BOSS直聘暂不支持零认证访问，需浏览器登录态。建议改用 LinkedIn 或直接搜索目标职位。可通过 RSS 方式跟踪特定职位搜索: https://www.zhipin.com 搜索结果页面的 RSS 订阅需第三方工具")
}

// ========== LinkedIn Feed ==========

func (t *SocialTrendingTool) linkedinFeed(ctx context.Context, query string, limit int) (string, error) {
	// LinkedIn requires login for most content
	// But we can use RSS for LinkedIn Pulse articles or company updates
	if query != "" {
		// Try LinkedIn company/page RSS if available
		return t.rssFetch(ctx,
			fmt.Sprintf("https://www.linkedin.com/company/%s", url.QueryEscape(query)),
			limit)
	}
	return "", fmt.Errorf("LinkedIn 需要登录。可通过 RSS 订阅特定公司动态: 传入 query=公司名。也可在浏览器登录后通过 linkedin.com/feed 查看")
}

// ========== Xiaoyuzhou (小宇宙) ==========

func (t *SocialTrendingTool) xiaoyuzhouFeed(ctx context.Context, query string, limit int) (string, error) {
	// Xiaoyuzhou podcast platform — try public API
	if query != "" {
		// Search podcasts
		body, err := t.httpGet(ctx,
			fmt.Sprintf("https://api.xiaoyuzhoufm.com/api/v1/discovery/search?q=%s&type=podcast&limit=%d",
				url.QueryEscape(query), limit),
			map[string]string{
				"Referer": "https://www.xiaoyuzhoufm.com/",
				"Origin":  "https://www.xiaoyuzhoufm.com",
			},
		)
		if err != nil {
			return "", err
		}
		var r struct {
			Data struct {
				Podcasts []struct {
					Title        string `json:"title"`
					Description  string `json:"description"`
					Author       string `json:"author"`
					EpisoaeCount int    `json:"episoaeCount"`
				} `json:"podcasts"`
			} `json:"data"`
		}
		if err := json.Unmarshal(body, &r); err == nil && len(r.Data.Podcasts) > 0 {
			var out strings.Builder
			out.WriteString(fmt.Sprintf("## 小宇宙 · %s\n\n", query))
			for i, p := range r.Data.Podcasts {
				desc := strings.TrimSpace(p.Description)
				if len([]rune(desc)) > 100 {
					desc = string([]rune(desc)[:100]) + "..."
				}
				out.WriteString(fmt.Sprintf("%d. **%s** — %s\n", i+1, p.Title, p.Author))
				out.WriteString(fmt.Sprintf("   %d集 | %s\n\n", p.EpisoaeCount, desc))
			}
			return out.String(), nil
		}
	}
	// Default: try hot/trending
	body, err := t.httpGet(ctx,
		fmt.Sprintf("https://api.xiaoyuzhoufm.com/api/v1/discovery/hot?limit=%d", limit),
		map[string]string{
			"Referer": "https://www.xiaoyuzhoufm.com/",
			"Origin":  "https://www.xiaoyuzhoufm.com",
		},
	)
	if err != nil {
		return "", fmt.Errorf("小宇宙暂不可用: %v。可尝试传入 query 搜索播客。", err)
	}

	var r struct {
		Data struct {
			Podcasts []struct {
				Title       string `json:"title"`
				Description string `json:"description"`
				Author      string `json:"author"`
			} `json:"podcasts"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &r); err == nil && len(r.Data.Podcasts) > 0 {
		var out strings.Builder
		out.WriteString("## 小宇宙热门播客\n\n")
		for i, p := range r.Data.Podcasts {
			out.WriteString(fmt.Sprintf("%d. **%s** — %s\n\n", i+1, p.Title, p.Author))
		}
		return out.String(), nil
	}
	return rawFallback("小宇宙 (raw)", body, 2000), nil
}

// ========== Helpers ==========

func (t *SocialTrendingTool) httpGet(ctx context.Context, urlStr string, headers map[string]string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", urlStr, nil)
	if err != nil {
		return nil, fmt.Errorf("request: %w", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")
	req.Header.Set("Accept", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := t.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch: %w", err)
	}
	defer resp.Body.Close()

	return io.ReadAll(io.LimitReader(resp.Body, 2<<20))
}

func rawFallback(label string, body []byte, maxLen int) string {
	s := string(body)
	if len(s) > maxLen {
		s = s[:maxLen] + "..."
	}
	return fmt.Sprintf("%s (raw):\n%s", label, s)
}
