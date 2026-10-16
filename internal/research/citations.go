package research

import (
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"

	securitycore "mac/internal/security"
	"mac/internal/toolcontract"
)

var (
	requestedSourceLinksPattern = regexp.MustCompile(`(?i)(?:来源|原文|参考|证据).{0,10}(?:链接|网址|URL|直链)|(?:可打开|可访问|直接打开).{0,8}(?:来源|原文|出处|页面)|(?:原始|权威|官方).{0,4}(?:来源|原文|出处)|(?:链接|网址|直链)|(?:source|citation|reference).{0,10}(?:link|url)|\bURLs?\b`)
	sourceLinePattern           = regexp.MustCompile(`(?i)^\s*\*{0,2}(?:source|来源)\*{0,2}\s*[:：]`)
)

func RequestsSourceLinks(input string) bool {
	return requestedSourceLinksPattern.MatchString(input)
}

// EnsureRequestedDirectSourceLinks deterministically restores citations that
// a model omitted while rewriting a researched answer. It only uses successful,
// direct, substantive host records from the current turn and never invents a
// URL or promotes a failed/low-yield fetch into evidence.
func EnsureRequestedDirectSourceLinks(request, answer string, toolResults []string) (string, []string) {
	answer = strings.TrimSpace(answer)
	if answer == "" || !requestedSourceLinksPattern.MatchString(request) {
		return answer, nil
	}

	missing := make([]string, 0, 4)
	for _, citationURL := range successfulDirectCitationURLs(toolResults) {
		if answerContainsEquivalentURL(answer, citationURL) {
			continue
		}
		missing = append(missing, citationURL)
		if len(missing) >= 8 {
			break
		}
	}
	if len(missing) == 0 {
		return answer, nil
	}

	var appendix strings.Builder
	appendix.WriteString("\n\n## 可直接打开的来源链接\n\n")
	for _, citationURL := range missing {
		appendix.WriteString("- <")
		appendix.WriteString(citationURL)
		appendix.WriteString(">\n")
	}
	return strings.TrimSpace(answer + appendix.String()), missing
}

// ReconcileExternalEvidenceURLs replaces a same-host shortened/hallucinated
// URL with the exact successful citation URL, and removes unsupported external
// URLs that have no unambiguous current-turn evidence.
func ReconcileExternalEvidenceURLs(answer string, toolResults []string) (string, []string, []string) {
	answer = strings.TrimSpace(answer)
	if answer == "" {
		return answer, nil, nil
	}
	citations := successfulDirectCitationURLs(toolResults)
	allowed := make(map[string]bool)
	allowedURLs := make(map[string]string)
	for _, result := range toolResults {
		record, ok := toolcontract.ParseRecord(result)
		if !ok || !record.Succeeded || !securitycore.IsExternalEvidenceRecord(record) {
			continue
		}
		for _, rawURL := range securitycore.ExternalURLPattern().FindAllString(result, -1) {
			candidate := NormalizeCitationURL(rawURL)
			if !IsArticleCitationURL(candidate) {
				continue
			}
			if key := CanonicalCitationURLKey(candidate); key != "" {
				allowed[key] = true
				allowedURLs[key] = candidate
			}
		}
	}
	if len(allowed) == 0 && len(citations) == 0 {
		return answer, nil, nil
	}
	hostCitation := make(map[string]string)
	ambiguousHost := make(map[string]bool)
	for _, citation := range citations {
		key := CanonicalCitationURLKey(citation)
		if key == "" {
			continue
		}
		allowed[key] = true
		allowedURLs[key] = citation
		parsed, _ := url.Parse(citation)
		host := strings.ToLower(parsed.Hostname())
		if existing := hostCitation[host]; existing != "" && CanonicalCitationURLKey(existing) != key {
			ambiguousHost[host] = true
		} else {
			hostCitation[host] = citation
		}
	}

	matches := securitycore.ExternalURLPattern().FindAllStringIndex(answer, -1)
	if len(matches) == 0 {
		return answer, nil, nil
	}
	var out strings.Builder
	replaced := make([]string, 0, 2)
	removed := make([]string, 0, 2)
	last := 0
	for _, match := range matches {
		out.WriteString(answer[last:match[0]])
		raw := answer[match[0]:match[1]]
		core := NormalizeCitationURL(raw)
		suffix := raw[len(core):]
		hasPlaceholder := citationURLHasPlaceholder(raw)
		key := CanonicalCitationURLKey(core)
		if allowed[key] {
			out.WriteString(core)
		} else if parsed, err := url.Parse(core); err == nil {
			host := strings.ToLower(parsed.Hostname())
			if replacement := equivalentEvidenceArticleURL(raw, allowedURLs); replacement != "" {
				out.WriteString(replacement)
				replaced = append(replaced, core)
				if hasPlaceholder {
					suffix = ""
				}
			} else if replacement := hostCitation[host]; replacement != "" && !ambiguousHost[host] && hasPlaceholder {
				out.WriteString(replacement)
				replaced = append(replaced, core)
				suffix = ""
			} else {
				out.WriteString("（未验证链接已移除）")
				removed = append(removed, core)
			}
		} else {
			out.WriteString("（未验证链接已移除）")
			removed = append(removed, core)
		}
		out.WriteString(suffix)
		last = match[1]
	}
	out.WriteString(answer[last:])
	return strings.TrimSpace(cleanUnsupportedCitationSegments(out.String())), replaced, removed
}

// equivalentEvidenceArticleURL repairs punctuation-only URL drift without
// guessing which same-host article the model meant. For example, publisher
// slugs sometimes encode a decimal as either "26.5b" or "26-5b". Both paths
// have the exact same ordered alphanumeric tokens. Reordering words, changing a
// number, or adding/removing a word does not qualify.
func equivalentEvidenceArticleURL(rawURL string, allowedURLs map[string]string) string {
	allowTerminalPrefix := citationURLHasTerminalPathPlaceholder(rawURL)
	parsed, err := url.Parse(NormalizeCitationURL(rawURL))
	if err != nil || parsed.Hostname() == "" {
		return ""
	}
	wanted := citationURLPathTokens(parsed.Path)
	if len(wanted) == 0 {
		return ""
	}
	host := strings.ToLower(parsed.Hostname())
	match := ""
	for _, candidate := range allowedURLs {
		candidateParsed, candidateErr := url.Parse(candidate)
		if candidateErr != nil || candidateParsed == nil || !strings.EqualFold(candidateParsed.Hostname(), host) {
			continue
		}
		candidateTokens := citationURLPathTokens(candidateParsed.Path)
		tokensMatch := equalCitationTokens(wanted, candidateTokens)
		if !tokensMatch && allowTerminalPrefix && len(wanted) >= 4 {
			tokensMatch = citationTokensHavePrefix(candidateTokens, wanted)
		}
		if !tokensMatch {
			continue
		}
		if match != "" && CanonicalCitationURLKey(match) != CanonicalCitationURLKey(candidate) {
			return ""
		}
		match = candidate
	}
	return match
}

func citationTokensHavePrefix(tokens, prefix []string) bool {
	if len(prefix) == 0 || len(tokens) < len(prefix) {
		return false
	}
	for index := range prefix {
		if tokens[index] != prefix[index] {
			return false
		}
	}
	return true
}

func citationURLPathTokens(path string) []string {
	parts := strings.FieldsFunc(strings.ToLower(path), func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < '0' || r > '9')
	})
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if part != "" {
			result = append(result, part)
		}
	}
	return result
}

func equalCitationTokens(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func citationURLHasPlaceholder(rawURL string) bool {
	lower := strings.ToLower(rawURL)
	return strings.Contains(lower, "...") || strings.Contains(lower, "%e2%80%a6") || strings.Contains(lower, "{...") || strings.Contains(lower, "%7b")
}

func citationURLHasTerminalPathPlaceholder(rawURL string) bool {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return false
	}
	path := strings.ToLower(strings.TrimSuffix(parsed.EscapedPath(), "/"))
	return strings.HasSuffix(path, "...") || strings.HasSuffix(path, "%e2%80%a6")
}

// cleanUnsupportedCitationSegments keeps the safety guard's removal explicit
// when it was the only claimed source, but avoids leaking a broken publisher
// fragment into an otherwise valid source line.
func cleanUnsupportedCitationSegments(answer string) string {
	const removedMarker = "（未验证链接已移除）"
	lines := strings.Split(strings.ReplaceAll(answer, "\r\n", "\n"), "\n")
	for index, line := range lines {
		if !strings.Contains(line, removedMarker) || !containsAnyFold(line, "来源", "source", "citation", "reference") {
			continue
		}
		segments := strings.FieldsFunc(line, func(r rune) bool {
			return r == '；' || r == ';' || r == '·'
		})
		if len(segments) < 2 {
			continue
		}
		kept := make([]string, 0, len(segments))
		for _, segment := range segments {
			if !strings.Contains(segment, removedMarker) {
				kept = append(kept, strings.TrimSpace(segment))
			}
		}
		if len(kept) == 0 {
			continue
		}
		joined := strings.Join(kept, "； ")
		if len(securitycore.ExternalURLPattern().FindAllString(joined, -1)) == 0 {
			continue
		}
		if strings.Contains(segments[0], removedMarker) {
			if colon := strings.IndexAny(line, ":："); colon >= 0 {
				_, size := utf8.DecodeRuneInString(line[colon:])
				joined = line[:colon+size] + strings.TrimSpace(joined)
			}
		}
		lines[index] = strings.TrimSpace(joined)
	}
	return strings.Join(lines, "\n")
}

func successfulDirectCitationURLs(toolResults []string) []string {
	seen := make(map[string]bool)
	result := make([]string, 0, 4)
	for _, toolResult := range toolResults {
		record, ok := substantiveDirectEvidenceRecord(toolResult)
		if !ok {
			continue
		}
		citationURL := CitationURLFromToolResult(toolResult)
		if citationURL == "" {
			citationURL = strings.TrimSpace(record.SourceURL)
		}
		citationURL = NormalizeCitationURL(citationURL)
		key := CanonicalCitationURLKey(citationURL)
		if key == "" || !SafePublicCitationURL(citationURL) || (strings.EqualFold(record.Tool, "social_trending") && !IsArticleCitationURL(citationURL)) || seen[key] {
			continue
		}
		seen[key] = true
		result = append(result, citationURL)
		if len(result) >= 8 {
			break
		}
	}
	return result
}

func substantiveDirectEvidenceRecord(result string) (toolcontract.Record, bool) {
	record, ok := toolcontract.ParseRecord(result)
	if !ok || !record.Succeeded || !securitycore.IsExternalEvidenceRecord(record) || record.SearchMode != "direct" || strings.TrimSpace(record.SourceURL) == "" {
		return toolcontract.Record{}, false
	}
	if hasExplicitEvidenceFailureStatus(result) {
		return toolcontract.Record{}, false
	}
	return record, true
}

func hasExplicitEvidenceFailureStatus(result string) bool {
	lower := strings.ToLower(strings.TrimSpace(result))
	for _, marker := range []string{
		"[retrieval_status: partial]", "[retrieval_status: mismatch]", "[retrieval_status: blocked]", "[retrieval_status: failed]",
		"retrieval_status: partial", "retrieval_status: mismatch", "retrieval_status: blocked", "retrieval_status: failed",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	if newline := strings.IndexByte(lower, '\n'); newline >= 0 {
		lower = strings.TrimSpace(lower[newline+1:])
	}
	for _, prefix := range []string{
		"no results found", "no search results", "empty response", "tool error", "request failed",
		"access denied", "captcha", "verify you are human", "web 应用防火墙", "人机识别", "人机验证",
		"未找到", "无搜索结果", "没有结果", "请求失败", "内容可能不完整", "内容异常",
	} {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	return false
}

func NormalizeCitationURL(rawURL string) string {
	return strings.TrimRight(strings.TrimSpace(rawURL), ".,，。;；:`")
}

func CanonicalCitationURLKey(rawURL string) string {
	parsed, err := url.Parse(NormalizeCitationURL(rawURL))
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" {
		return ""
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	parsed.Host = strings.ToLower(parsed.Host)
	parsed.Fragment = ""
	parsed.RawFragment = ""
	if parsed.Path != "/" {
		parsed.Path = strings.TrimSuffix(parsed.Path, "/")
		parsed.RawPath = ""
	}
	query := parsed.Query()
	for key, values := range query {
		lower := strings.ToLower(key)
		if strings.HasPrefix(lower, "utm_") || lower == "spm" || lower == "gclid" || lower == "fbclid" || lower == "mc_cid" || lower == "mc_eid" {
			query.Del(key)
			continue
		}
		if lower == "f" && len(values) > 0 && strings.EqualFold(strings.TrimSpace(values[0]), "rss") {
			query.Del(key)
		}
	}
	parsed.RawQuery = query.Encode()
	return parsed.String()
}

func answerContainsEquivalentURL(answer, citationURL string) bool {
	target := CanonicalCitationURLKey(citationURL)
	if target == "" {
		return false
	}
	for _, rawURL := range securitycore.ExternalURLPattern().FindAllString(answer, -1) {
		if CanonicalCitationURLKey(rawURL) == target {
			return true
		}
	}
	return false
}

func CitationURLFromToolResult(result string) string {
	for _, line := range strings.Split(result, "\n") {
		if !sourceLinePattern.MatchString(line) {
			continue
		}
		if rawURL := securitycore.ExternalURLPattern().FindString(line); rawURL != "" {
			return rawURL
		}
	}
	return ""
}

func SafePublicCitationURL(rawURL string) bool {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" {
		return false
	}
	for key := range parsed.Query() {
		lower := strings.ToLower(key)
		switch lower {
		case "key", "api_key", "apikey", "token", "access_token", "refresh_token", "secret", "client_secret", "password", "passwd", "authorization", "auth":
			return false
		}
		if strings.Contains(lower, "secret") || strings.Contains(lower, "password") || strings.HasSuffix(lower, "_token") || strings.HasSuffix(lower, "_key") {
			return false
		}
	}
	return true
}

func IsArticleCitationURL(rawURL string) bool {
	parsed, err := url.Parse(rawURL)
	if err != nil || !SafePublicCitationURL(rawURL) {
		return false
	}
	host := strings.TrimPrefix(strings.ToLower(parsed.Hostname()), "www.")
	path := strings.Trim(strings.ToLower(parsed.EscapedPath()), "/")
	queryIdentifiesArticle := hasArticleIDQuery(parsed)
	if (path == "" && !queryIdentifiesArticle) || host == "bing.com" || host == "news.google.com" {
		return false
	}
	if isLikelyNewsIndexPath(path) && !queryIdentifiesArticle {
		return false
	}
	if strings.HasPrefix(host, "feeds.") && !queryIdentifiesArticle {
		lastSegment := path
		if separator := strings.LastIndexByte(lastSegment, '/'); separator >= 0 {
			lastSegment = lastSegment[separator+1:]
		}
		switch lastSegment {
		case "", "index", "rss", "feed", "rss.xml", "feed.xml", "atom.xml", "index.xml":
			return false
		}
	}
	for _, suffix := range []string{"rss", "rss.xml", "feed", "feed.xml", "atom.xml", "index.xml"} {
		if path == suffix || strings.HasSuffix(path, "/"+suffix) {
			return false
		}
	}
	return true
}

func hasArticleIDQuery(parsed *url.URL) bool {
	if parsed == nil {
		return false
	}
	for key, values := range parsed.Query() {
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "p", "id", "aid", "article", "article_id", "articleid", "story", "story_id", "storyid", "newsid", "news_id":
			for _, value := range values {
				if strings.TrimSpace(value) != "" {
					return true
				}
			}
		}
	}
	return false
}

func isLikelyNewsIndexPath(path string) bool {
	segments := strings.FieldsFunc(strings.Trim(strings.ToLower(path), "/"), func(r rune) bool { return r == '/' })
	if len(segments) == 0 || len(segments) > 2 {
		return false
	}
	joined := strings.Join(segments, "/")
	for _, indexPath := range []string{
		"tech", "technology", "business", "news", "latest", "world", "finance",
		"politics", "science", "ai", "markets", "information/technology", "business/tech",
		"news/technology", "news/business", "index.html", "index.htm",
	} {
		if joined == indexPath {
			return true
		}
	}
	switch segments[0] {
	case "category", "tag", "topic", "topics", "section", "channel":
		return true
	default:
		return false
	}
}
