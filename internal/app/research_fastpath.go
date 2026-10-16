package app

import (
	"context"
	"fmt"
	"mac/internal/agentloop"
	stockartifact "mac/internal/artifacts/stock"
	"mac/internal/modelruntime"
	"mac/internal/permissionpolicy"
	investmentadvisory "mac/internal/pipelines/investmentadvisory"
	researchcore "mac/internal/research"
	"mac/internal/routing"
	securitycore "mac/internal/security"
	"mac/internal/textutil"
	businesstools "mac/internal/tools/business"
	networktools "mac/internal/tools/network"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

var (
	researchDiscoveryResultLine  = regexp.MustCompile(`^\d+\.`)
	artifactPerVendorSourceCount = regexp.MustCompile(`(?i)(?:每家|每个(?:厂商|品牌|产品)?|per\s+(?:vendor|product))[^。\n]{0,24}?([0-9]{1,2}|[一二两三四五六七八九十]{1,3})\s*(?:条|个|份|pages?|links?|sources?)?[^。\n]{0,16}?(?:官方)?(?:页面|直链|链接|来源|pages?|links?|sources?)`)
	artifactDiscoverySite        = regexp.MustCompile(`(?i)\bsite:\s*([a-z0-9.-]+)`)
)

type readOnlyResearchTurnContextKey struct{}
type artifactResearchTurnContextKey struct{}

func withReadOnlyResearchTurn(ctx context.Context, input string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if !researchcore.IsFastReadOnlyResearchTurn(input) {
		return ctx
	}
	return context.WithValue(ctx, readOnlyResearchTurnContextKey{}, strings.TrimSpace(input))
}

func isArtifactResearchTurn(input string) bool {
	return requestedTextArtifactPath(input) != "" && researchcore.IsResearchRequest(input)
}

func withArtifactResearchTurn(ctx context.Context, input string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if !isArtifactResearchTurn(input) {
		return ctx
	}
	return context.WithValue(ctx, artifactResearchTurnContextKey{}, strings.TrimSpace(input))
}

func artifactResearchRequestFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	// Pipeline prompts intentionally embed the user's full original request so
	// each bounded stage can preserve user-authored restrictions. That context
	// must not activate top-level artifact orchestration inside a sub-step:
	// intake/review stages may be no-tools, while later research/write stages
	// already have explicit capability and artifact contracts.
	if pipelineStep, _ := ctx.Value("pipeline_step_execution").(bool); pipelineStep {
		return ""
	}
	request, _ := ctx.Value(artifactResearchTurnContextKey{}).(string)
	return strings.TrimSpace(request)
}

func researchTurnRequestFromContext(ctx context.Context) string {
	if request := readOnlyResearchRequestFromContext(ctx); request != "" {
		return request
	}
	return artifactResearchRequestFromContext(ctx)
}

func artifactResearchGuidance(input string) string {
	if !isArtifactResearchTurn(input) {
		return ""
	}
	return `[ARTIFACT-BACKED OFFICIAL RESEARCH]
This turn requests live research plus a text-file deliverable. Use a bounded evidence workflow:
- First discover real canonical official pages with web_fetch in query-only mode (query + engine, no URL). For a multi-vendor comparison, every discovery query must include a site: restriction for that named vendor's official root domain; do not guess /pricing, /security, /features, or similar URL paths.
- Then fetch only exact official URLs visible in discovery results, in one compact parallel verification batch. A 404 is not evidence and must not be retried by guessing another path.
- Preserve distinct official URLs and mark unsupported pricing, residency, regional availability, security, or deployment fields unknown.
- After the bounded verification batch, write the requested artifact once. Do not create plans, notes, or housekeeping shell calls.`
}

func normalizeArtifactResearchDiscoveryCalls(calls []ToolCall) []ToolCall {
	const maxDiscoveryCalls = 6
	groups := make(map[string][]ToolCall)
	groupOrder := make([]string, 0)
	seenQueries := make(map[string]bool)
	for _, call := range calls {
		if !strings.EqualFold(strings.TrimSpace(call.Name), "web_fetch") {
			continue
		}
		query, _ := call.Arguments["query"].(string)
		if strings.TrimSpace(query) == "" {
			continue
		}
		siteMatch := artifactDiscoverySite.FindStringSubmatch(query)
		if len(siteMatch) != 2 {
			continue
		}
		queryKey := strings.ToLower(strings.Join(strings.Fields(query), " "))
		if seenQueries[queryKey] {
			continue
		}
		seenQueries[queryKey] = true
		delete(call.Arguments, "url")
		call.Arguments["engine"] = "brave"
		call.Arguments["maxResults"] = 10
		group := strings.ToLower(strings.TrimPrefix(siteMatch[1], "www."))
		if _, ok := groups[group]; !ok {
			groupOrder = append(groupOrder, group)
		}
		groups[group] = append(groups[group], call)
	}

	// Interleave vendor-specific site groups before the search-budget gate so
	// one verbose vendor cannot starve the remaining vendors just because the
	// model emitted all of its calls first.
	balanced := make([]ToolCall, 0, maxDiscoveryCalls)
	for round := 0; len(balanced) < maxDiscoveryCalls; round++ {
		added := false
		for _, group := range groupOrder {
			if round >= len(groups[group]) {
				continue
			}
			balanced = append(balanced, groups[group][round])
			added = true
			if len(balanced) == maxDiscoveryCalls {
				break
			}
		}
		if !added {
			break
		}
	}
	return balanced
}

func artifactResearchDirectCoverageSatisfied(input string, results []string) bool {
	requiredVendors := maxRequestedCount(input, searchItemCountAfterNumber, searchTopCount)
	if requiredVendors < 1 {
		requiredVendors = 1
	}
	requiredPerVendor := 1
	if match := artifactPerVendorSourceCount.FindStringSubmatch(input); len(match) == 2 {
		if parsed := parseRequestedCount(match[1]); parsed > 0 {
			requiredPerVendor = parsed
		}
	}
	urlsByHost := make(map[string]map[string]bool)
	for _, result := range results {
		record, ok := parseOpenKittyToolRecord(result)
		if !ok || !artifactResearchDirectEvidenceUsable(result) || record.SourceURL == "" {
			continue
		}
		host := artifactVendorRootHost(record.SourceURL)
		if host == "" {
			continue
		}
		if urlsByHost[host] == nil {
			urlsByHost[host] = make(map[string]bool)
		}
		urlsByHost[host][artifactCanonicalPageURL(record.SourceURL)] = true
	}
	covered := 0
	for _, urls := range urlsByHost {
		if len(urls) >= requiredPerVendor {
			covered++
		}
	}
	return covered >= requiredVendors
}

func artifactCanonicalPageURL(rawURL string) string {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return strings.TrimSpace(rawURL)
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	parsed.Host = strings.ToLower(parsed.Host)
	parsed.Fragment = ""
	parsed.RawQuery = ""
	parsed.ForceQuery = false
	parsed.Path = strings.TrimSuffix(parsed.Path, "/")
	return parsed.String()
}

func artifactResearchDirectEvidenceUsable(result string) bool {
	record, ok := parseOpenKittyToolRecord(result)
	if !ok || !record.Succeeded || !strings.EqualFold(record.SearchMode, "direct") || record.SourceURL == "" {
		return false
	}
	lower := strings.ToLower(result)
	for _, marker := range []string{
		"执行失败:", "status: failed", "status: failed_compacted",
		"retrieval_status: mismatch", "retrieval_status: blocked",
		"http 403", "http error: 403", "403 forbidden", "access denied", "captcha",
		"verify you are human",
		"web 应用防火墙", "人机识别", "人机验证", "抓取被拒", "爬虫拦截",
	} {
		if strings.Contains(lower, marker) {
			return false
		}
	}
	return true
}

func artifactVendorRootHost(rawURL string) string {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return ""
	}
	host := strings.ToLower(strings.TrimPrefix(parsed.Hostname(), "www."))
	parts := strings.Split(host, ".")
	if len(parts) <= 2 {
		return host
	}
	// Small deterministic public-suffix guard for common vendor domains. This
	// keeps docs.vendor.com and www.vendor.com in one coverage bucket.
	lastTwo := strings.Join(parts[len(parts)-2:], ".")
	if lastTwo == "com.cn" || lastTwo == "com.hk" || lastTwo == "co.uk" || lastTwo == "com.au" || lastTwo == "co.jp" {
		return strings.Join(parts[len(parts)-3:], ".")
	}
	return lastTwo
}

func artifactVendorLabel(rawURL string) string {
	root := artifactVendorRootHost(rawURL)
	if root == "" {
		return ""
	}
	return strings.Split(root, ".")[0]
}

func artifactVerificationURLScore(rawURL string) int {
	lower := strings.ToLower(rawURL)
	score := 0
	containsAny := func(tokens ...string) bool {
		for _, token := range tokens {
			if strings.Contains(lower, token) {
				return true
			}
		}
		return false
	}
	// Topic families are mutually exclusive. Additive scoring made deeply
	// nested, unrelated SKUs (for example an employee-service pricing page)
	// outrank the canonical pricing page merely by containing more keywords.
	switch {
	case containsAny("pricing", "price", "plans"):
		score = 100
	case containsAny("security", "trust", "privacy", "compliance"):
		score = 80
	case containsAny("product", "feature", "service", "platform", "ai", "fin"):
		score = 20
	}
	for _, token := range []string{"/blog/", "trailhead.", "/content/learn/", "/community/"} {
		if strings.Contains(lower, token) {
			score -= 60
		}
	}
	for _, token := range []string{"login", "signin", "signup", "auth", "community"} {
		if strings.Contains(lower, token) {
			score -= 200
		}
	}
	if parsed, err := url.Parse(rawURL); err == nil {
		depth := len(strings.FieldsFunc(strings.Trim(parsed.Path, "/"), func(r rune) bool { return r == '/' }))
		score -= depth
	}
	return score
}

func filterArtifactResearchVerificationCalls(_ []ToolCall, discoveryResults []string, input string) []ToolCall {
	requiredVendors := maxRequestedCount(input, searchItemCountAfterNumber, searchTopCount)
	if requiredVendors < 1 {
		requiredVendors = 1
	}
	requiredPerVendor := 1
	if match := artifactPerVendorSourceCount.FindStringSubmatch(input); len(match) == 2 {
		if parsed := parseRequestedCount(match[1]); parsed > 0 {
			requiredPerVendor = parsed
		}
	}
	if requiredPerVendor > 2 {
		requiredPerVendor = 2
	}

	inputLower := strings.ToLower(input)
	attempted := make(map[string]bool)
	succeededByRoot := make(map[string]int)
	for _, result := range discoveryResults {
		record, ok := parseOpenKittyToolRecord(result)
		if !ok || !strings.EqualFold(record.SearchMode, "direct") || record.SourceURL == "" {
			continue
		}
		parsed, err := url.Parse(record.SourceURL)
		if err == nil {
			attempted[artifactCanonicalPageURL(parsed.String())] = true
		}
		if artifactResearchDirectEvidenceUsable(result) {
			succeededByRoot[artifactVendorRootHost(record.SourceURL)]++
		}
	}
	genericLabels := map[string]bool{
		"cloud": true, "service": true, "support": true, "pricing": true,
		"trust": true, "login": true, "google": true, "youtube": true,
		"linkedin": true, "microsoft": true,
	}
	primaryCandidates := make(map[string][]string)
	extraCandidates := make(map[string][]string)
	seenByRoot := make(map[string]map[string]bool)
	for _, result := range discoveryResults {
		record, ok := parseOpenKittyToolRecord(result)
		if !ok || !record.Succeeded || !strings.EqualFold(record.SearchMode, "discovery") {
			continue
		}
		packetCandidates := make(map[string][]string)
		for _, rawURL := range securitycore.ExternalURLPattern().FindAllString(result, -1) {
			rawURL = strings.TrimRight(rawURL, ".,，。;；:`")
			label := artifactVendorLabel(rawURL)
			root := artifactVendorRootHost(rawURL)
			if len(label) < 4 || root == "" || genericLabels[label] || !strings.Contains(inputLower, label) || artifactVerificationURLScore(rawURL) < 0 {
				continue
			}
			parsed, err := url.Parse(rawURL)
			if err != nil || parsed.Scheme == "" || parsed.Hostname() == "" {
				continue
			}
			canonical := artifactCanonicalPageURL(parsed.String())
			if attempted[canonical] {
				continue
			}
			if seenByRoot[root] == nil {
				seenByRoot[root] = make(map[string]bool)
			}
			if !seenByRoot[root][canonical] {
				seenByRoot[root][canonical] = true
				packetCandidates[root] = append(packetCandidates[root], rawURL)
			}
		}
		for root, packet := range packetCandidates {
			sort.SliceStable(packet, func(i, j int) bool {
				return artifactVerificationURLScore(packet[i]) > artifactVerificationURLScore(packet[j])
			})
			primaryCandidates[root] = append(primaryCandidates[root], packet[0])
			if len(packet) > 1 {
				extraCandidates[root] = append(extraCandidates[root], packet[1:]...)
			}
		}
	}
	candidates := make(map[string][]string)
	for root, primary := range primaryCandidates {
		candidates[root] = append(candidates[root], primary...)
		candidates[root] = append(candidates[root], extraCandidates[root]...)
	}
	for root := range candidates {
		// Initial verification preserves one winner per vendor-specific query,
		// providing product/topic diversity. A replacement window instead ranks
		// all unused URLs so it chooses the strongest remaining page for the gap.
		if succeededByRoot[root] > 0 {
			sort.SliceStable(candidates[root], func(i, j int) bool {
				return artifactVerificationURLScore(candidates[root][i]) > artifactVerificationURLScore(candidates[root][j])
			})
		}
	}

	// One vendor label may have several country domains. Keep the strongest
	// discovered root, preferring the canonical .com site on equal coverage.
	bestRootByLabel := make(map[string]string)
	for root := range candidates {
		label := artifactVendorLabel("https://" + root)
		current := bestRootByLabel[label]
		if current == "" || len(candidates[root]) > len(candidates[current]) ||
			(len(candidates[root]) == len(candidates[current]) && strings.HasSuffix(root, ".com") && !strings.HasSuffix(current, ".com")) {
			bestRootByLabel[label] = root
		}
	}
	labels := make([]string, 0, len(bestRootByLabel))
	for label := range bestRootByLabel {
		labels = append(labels, label)
	}
	sort.Strings(labels)
	if len(labels) > requiredVendors {
		labels = labels[:requiredVendors]
	}
	filtered := make([]ToolCall, 0, 6)
	for round := 0; round < requiredPerVendor && len(filtered) < 6; round++ {
		for _, label := range labels {
			root := bestRootByLabel[label]
			needed := max(0, requiredPerVendor-succeededByRoot[root])
			if round >= needed || round >= len(candidates[root]) {
				continue
			}
			filtered = append(filtered, ToolCall{Name: "web_fetch", Arguments: map[string]interface{}{"url": candidates[root][round]}})
			if len(filtered) == 6 {
				break
			}
		}
	}
	if len(filtered) > 6 {
		filtered = filtered[:6]
	}
	return filtered
}

func readOnlyResearchRequestFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	// A pipeline step is governed by its step-scoped tool registry. Inheriting
	// a top-level research fast path here can hide an explicitly assigned write
	// tool or force retrieval inside a no-tools intake/review stage.
	if pipelineStep, _ := ctx.Value("pipeline_step_execution").(bool); pipelineStep {
		return ""
	}
	request, _ := ctx.Value(readOnlyResearchTurnContextKey{}).(string)
	return strings.TrimSpace(request)
}

func fastReadOnlyResearchGuidance(input string) string {
	if !researchcore.IsFastReadOnlyResearchTurn(input) {
		return ""
	}
	guidance := `[FAST READ-ONLY RESEARCH]
This turn asks for one user-facing answer and no file artifact or external side effect. Take the direct path:
- Do not call plan_task, update_plan_step, or notepad. Multiple requested fields or sources still form one research answer, not a tracked multi-phase project.
- Do not call write_file/edit_file or save interim notes merely because the answer is long. Keep evidence in tool results and answer in chat; create a file only when the user explicitly requests a file artifact.
- Use the most specific live/read-only data tool. For a public page or known canonical URL, use web_fetch first; use CDP browser tools only after web_fetch explicitly reports that JavaScript or interaction is required and a browser is available.
- For multi-vendor/product research, do not guess predictable /price, /security, or /feature URLs. Discover each vendor's real official pages first, then fetch distinct canonical pages needed for the comparison. A 404 is not a source and should not trigger repeated housekeeping updates.
- Batch distinct sources when safe, assess source quality and provenance, then answer. Do not create housekeeping work around the research.
- Synthesize once. Put requested source links and their exact time semantics near the top, use one compact comparison table plus short recommendations, and normally keep a Chinese answer within about 1800 characters unless the requested facts cannot fit.
- Never infer finer timing or precision than the evidence provides: daily/aggregate rows do not establish morning, afternoon, evening, hourly, or all-day conditions.`
	if researchcore.IsCurrentNewsBrief(input) {
		guidance += `

[CURRENT NEWS BRIEF]
- Use exactly one compact parallel discovery batch of at most five distinct publisher RSS feeds through ` + "`social_trending(source=\"rss\", limit=10)`" + `. Useful general technology/business feeds include https://www.ithome.com/rss/, https://www.36kr.com/feed, https://techcrunch.com/feed/, https://www.theverge.com/rss/index.xml, and https://feeds.arstechnica.com/arstechnica/index.
- A publisher RSS row may support only its explicit headline, publication time, snippet-level fact, and exact article URL. Select the requested number of distinct events from those rows; do not refetch every item merely to restate those limited facts.
- Preserve the source's fact labels exactly. Do not turn an opening price into an issue price, an update timestamp into a publication timestamp, an expected outcome into a confirmed one, or add a venue, person, count, cause, superlative, or date that the retained row does not state. If a detail is not explicit, omit it instead of completing it from memory.
- Quotation marks claim verbatim source wording. Use them only when those exact words appear in the retained evidence; otherwise paraphrase without quotation marks.
- For a digest/roundup RSS entry, an event is eligible only when it is literally named in that entry's RSS title. Do not promote an older item found only inside a roundup's body, "热点导览", "大公司", or "其他消息" list into a new event for today.
- For requested cross-checks, use at most one narrow event-title check per selected top event. Two distinct publisher RSS rows already qualify as a snippet-level second-source check; do not spend extra calls merely to find a third publisher. A direct fetch is allowed only for an exact article URL already visible in RSS/search evidence—never guess Reuters, Bloomberg, or any other URL from a headline. After the bounded cross-check stage, stop all web retrieval and write the answer.
- Do not use a news homepage or section page as proof for an individual event.
- If the user requests second-source checks, search each selected event and use a different publisher/domain; two titles or pages from the same publisher are not independent confirmation. Show factual conflicts instead of calling them consistent.
- Cluster candidates by the underlying real-world event before selecting them: one launch, lawsuit, financing/listing, product announcement, or policy action counts once even when publishers cover different angles. Merge those angles into one item instead of filling two slots.
- Keep exactly one underlying event in each numbered item. Never combine unrelated product, personnel, lawsuit, enforcement, financing, launch, or policy headlines in one slot merely because they share a company, date, or feed; select one and use another distinct event for the next slot.
- Keep publication time separate from event time. If an article does not state when the event occurred, label the event time unknown rather than copying the publication time.
- Never fill an event-time field with the requested target date merely because the RSS row was published that day. A roundup's publication date proves only publication, not when each nested event happened.
- Resolve relative words such as today, yesterday, and last night against that exact row's publication timestamp, and bind the date to the same actor and action named in the event-time field. If one row mentions different actions on different relative days and the requested event cannot be bound unambiguously, report the event time as unknown.
- Interpret "today" in the user's local timezone. Exclude a publisher timestamp that is plainly on the previous local calendar day (for example a +08:00 domestic roundup from yesterday). A previous UTC or American calendar date may qualify only when its stated time actually converts into today locally; keep the original timezone visible so the conversion is auditable.
- The host may remove rolling-feed rows that fall outside the requested local day. Treat the retained rows as the complete eligible set: never reconstruct, cite, or relabel a removed older row from memory.
- Make every numbered item visibly contain every requested field; natural equivalent labels such as "事件时间" for "事件发生时间" are acceptable. Treat an explicit summary length as a hard limit and aim at least 20 percent below it before responding.
- Treat each item's cited row(s) as an isolated evidence packet. Do not borrow a number or detail from an uncited row elsewhere in the batch, even when it concerns the same company.
- Preserve technical scope and modality exactly: arbitrary versus remote, reference/use versus training, announced/planned versus completed, and first completion versus proven reuse or cost reduction are not interchangeable.
- An RSS snippet ending in "..." has an unavailable tail. Never complete that tail from memory. Do not combine a person from one sentence with an action from a headline or another sentence unless the cited evidence itself links that person and action in the same sentence.
- Keep the core-fact field to one short evidence-bound sentence. Keep each "why it matters" line to one short, cautious sentence explicitly introduced as analysis. It may explain the supported fact's immediate relevance, but must not introduce a new number, named product/category, price band, technical mechanism, venue, historical-first claim, or causal market conclusion absent from that item's cited row(s). Use "可能/值得关注/仍需观察" for inference. If no specific analysis is safely supported, write "分析：该进展的实际影响仍需后续权威披露验证。"
- Before emitting the answer, silently audit every numbered item: locate each subject-action relationship, number, comparison direction, and qualifier in that item's cited row(s). Delete anything you cannot locate. The summary may compress only the supported core facts from events that actually appear in the selected numbered items; never mention an unselected feed candidate or speculative wording from "why it matters".
- Return exactly the requested numbered items and the management summary in chat, with an article URL attached to every item and both publisher URLs inside each cross-checked item. Do not mention discarded candidates, candidate-selection rationale, or an extra "not selected" note outside the numbered items. Do not claim that every item was independently multi-source verified when the user requested cross-checks for only a subset. Do not write an interim or final report file unless the user explicitly requested a file.`
	}
	return guidance
}

func fastReadOnlyResearchEvidenceCount(results []string) int {
	count := 0
	for _, result := range results {
		record, ok := parseOpenKittyToolRecord(result)
		if !ok || !record.Succeeded || !substantiveArtifactEvidenceTool(record.Tool) {
			continue
		}
		count++
	}
	return count
}

func fastReadOnlyResearchEvidenceModes(results []string) (discovery, direct int) {
	for _, result := range results {
		record, ok := parseOpenKittyToolRecord(result)
		if !ok || !record.Succeeded || !substantiveArtifactEvidenceTool(record.Tool) {
			continue
		}
		if strings.EqualFold(record.SearchMode, "direct") {
			direct++
		} else if strings.EqualFold(record.SearchMode, "discovery") {
			discovery++
		}
	}
	return discovery, direct
}

func fastReadOnlyResearchDirectEvidencePrompt() map[string]interface{} {
	return agentloop.NewInternalControlMessage("OFFICIAL SOURCE VERIFICATION WINDOW", `Discovery is complete but search snippets are not proof of the requested vendor fields. From URLs already visible in the discovery results, issue one parallel batch of up to six distinct direct web_fetch calls to canonical official vendor pages. Cover at least three retained vendors and prioritize product/features plus pricing, security, deployment, or export documentation. Do not guess URL paths, plan, write a file, or run another search query. This is the only direct verification window; after it, missing fields remain unknown.`)
}

func buildFastReadOnlyResearchSynthesisMessages(input string, results []string) []map[string]interface{} {
	var evidence strings.Builder
	kept := 0
	for _, result := range results {
		record, ok := parseOpenKittyToolRecord(result)
		if !ok || !record.Succeeded || !substantiveArtifactEvidenceTool(record.Tool) {
			continue
		}
		content := result
		if idx := strings.Index(content, "\n"); idx >= 0 {
			content = content[idx+1:]
		}
		if strings.EqualFold(record.SearchMode, "discovery") {
			content = compactResearchDiscoveryEvidence(content)
		}
		fmt.Fprintf(&evidence, "[%s]\n%s\n\n", record.Tool, textutil.TruncateUTF8Bytes(strings.TrimSpace(content), 4200))
		kept++
		if kept >= 8 || evidence.Len() >= 24000 {
			break
		}
	}
	return []map[string]interface{}{
		{
			"role": "system",
			"content": `[CLOSED RESEARCH SYNTHESIS]
Answer the user's research request now from the evidence packets below. Do not call tools, plan, save a file, or narrate the process. Cover every requested comparison field and the requested number of products/sources. Separate official facts from third-party commentary, attach only URLs present in the packets, include the exact query/as-of date, and mark missing price/security/export/deployment details unknown instead of guessing. For a comparison of three or more vendors, normally cite at least two distinct official pages per retained vendor (for example product/features plus pricing/security/deployment) when those pages are present in the evidence. Keep the answer decision-ready and compact.`,
		},
		{"role": "user", "content": strings.TrimSpace(input)},
		{"role": "system", "content": "Current-turn evidence packets:\n\n" + evidence.String()},
	}
}

func compactResearchDiscoveryEvidence(content string) string {
	var kept []string
	for _, raw := range strings.Split(content, "\n") {
		line := strings.TrimSpace(raw)
		lower := strings.ToLower(line)
		if researchDiscoveryResultLine.MatchString(line) ||
			strings.HasPrefix(lower, "url:") ||
			strings.HasPrefix(lower, "source url:") ||
			strings.HasPrefix(lower, "used fallback engine") {
			kept = append(kept, textutil.TruncateUTF8Bytes(line, 500))
		}
	}
	return strings.Join(kept, "\n")
}

func currentNewsSynthesisContinuationPrompt() map[string]interface{} {
	return agentloop.NewInternalControlMessage("CURRENT NEWS SYNTHESIS REQUIRED", `The bounded retrieval stage is closed. Your previous sentence about changing keywords or searching again was only a progress note, not the requested answer.
Do not call any tool, propose another search, or discuss your process. The publisher RSS evidence already collected remains valid even when a supplemental search was low-yield. Produce the complete user-facing current-news brief now from the closed evidence set: the requested number of distinct events, exactly one underlying event per numbered item, every requested field, an article URL on every item, only genuinely independent second-source checks, honest unknown event times instead of copied publication dates, and a complete summary below the requested character limit. A digest entry supports only events literally named in its RSS title, not older bullets nested in its body.
Use each item's cited row(s) as that item's complete and isolated evidence packet. Use only facts explicit in those rows, preserve their labels exactly, and preserve technical scope exactly: never swap publication/update times, opening/issue prices, arbitrary/remote capability, reference/training use, expected/confirmed status, venue, people, counts, comparison direction, causes, or dates. A snippet ending in "..." has no usable hidden tail. Never attach a person from one sentence to an action from the title or another sentence unless the evidence itself links them in the same sentence. Resolve relative dates against the cited row's publication time and the same actor/action; use unknown when two dated actions cannot be bound unambiguously. Do not borrow from uncited rows or add remembered detail. Quotation marks are allowed only for words present verbatim in the evidence.

Write directly without first listing candidates or narrating an audit. Return only the selected numbered items and the management summary; never add a note naming a discarded candidate or explaining why it was not selected. Keep each core-fact field to at most two short evidence-bound clauses. Keep "why it matters" to one short sentence starting as analysis, with no new number, product/category, price band, mechanism, venue, historical-first claim, or causal market conclusion. If a specific inference is not safely supported, use: "分析：该进展的实际影响仍需后续权威披露验证。" Build the management summary only from supported core facts of the events actually present in the selected numbered items; never summarize an unselected feed candidate. Target 120 Chinese characters when the user allows 150. Keep the full brief compact—normally no more than about 1800 visible Chinese characters excluding URLs—so every sentence finishes in this response.`)
}

func adaptiveLLMOutputTokenLimit(input string) int {
	if !researchcore.IsFastReadOnlyResearchTurn(input) {
		return 0
	}
	if researchcore.IsCurrentNewsBrief(input) {
		return modelruntime.CurrentNewsResearchOutputTokenLimit
	}
	return modelruntime.FastReadOnlyResearchOutputTokenLimit
}

var (
	explicitAShareCodePattern           = regexp.MustCompile(`\b([036][0-9]{5})\b`)
	explicitInvestmentPositionPattern   = regexp.MustCompile(`(?:空仓|无持仓|持有|已持仓|已买入|套牢)|(?:持仓|仓位|成本价|持仓成本|账户规模|本金|可用资金)[^。；;\n]{0,12}[0-9]`)
	missingInvestmentPositionPattern    = regexp.MustCompile(`(?:(?:未|尚未|没有|并未)(?:明确)?(?:提供|说明|告知|披露)[^。；;\n]{0,48}(?:持仓|仓位|成本|账户规模|本金|可用资金))|(?:(?:持仓|仓位|成本|账户规模|本金|可用资金)[^。；;\n]{0,24}(?:未提供|未知|没有说明|未告知))`)
	investmentPositionLinePattern       = regexp.MustCompile(`(?m)^(?:-\s*)?position_state\s*[:：][^\r\n]*`)
	investmentDeliveryStatusLinePattern = regexp.MustCompile(`(?m)^[ \t]*(?:[-+]\s*)?(?:\*\*|__)?交付状态(?:\*\*|__)?\s*[:：][^\r\n]*`)
	investmentParallelBranchPattern     = regexp.MustCompile(`(?m)^###\s+[^\r\n]+`)
	investmentParallelFailurePattern    = regexp.MustCompile(`(?m)^DATA_STATUS:\s*fetch_failed\s*$`)
	investmentParallelReasonPattern     = regexp.MustCompile(`(?m)^原因：[^\r\n]*$`)
	investmentDecisionSectionPattern    = regexp.MustCompile(`(?m)^##\s+([^\r\n]+)\s*$`)
	investmentEvidenceCompanyPattern    = regexp.MustCompile(`(?m)^##\s+([036][0-9]{5})\s+([^\r\n]+)\s*$`)
	investmentObservedCountPattern      = regexp.MustCompile(`五项核心指标\s*(?:observed|已提取)=(\d+)/5`)
)

func investmentPositionState(input string) string {
	text := strings.ToLower(strings.TrimSpace(input))
	if explicitInvestmentPositionPattern.MatchString(text) {
		return "provided"
	}
	if missingInvestmentPositionPattern.MatchString(text) {
		return "not_provided"
	}
	for _, marker := range []string{"持仓", "仓位", "成本价", "持有", "空仓", "账户规模", "本金", "可用资金"} {
		if strings.Contains(text, strings.ToLower(marker)) {
			return "provided"
		}
	}
	return "not_provided"
}

func normalizeInvestmentPositionStateOutput(output, state string) string {
	output = strings.TrimSpace(output)
	if state != "provided" && state != "not_provided" {
		state = "not_provided"
	}
	canonical := "position_state: " + state
	if investmentPositionLinePattern.MatchString(output) {
		return investmentPositionLinePattern.ReplaceAllString(output, canonical)
	}
	if statusLine := investmentDeliveryStatusLinePattern.FindStringIndex(output); statusLine != nil {
		return output[:statusLine[1]] + "\n\n" + canonical + output[statusLine[1]:]
	}
	if output == "" {
		return canonical
	}
	return canonical + "\n\n" + output
}

func explicitAShareCodes(input string) []string {
	seen := make(map[string]bool)
	var codes []string
	for _, match := range explicitAShareCodePattern.FindAllStringSubmatch(input, -1) {
		if len(match) != 2 || seen[match[1]] {
			continue
		}
		seen[match[1]] = true
		codes = append(codes, match[1])
		if len(codes) >= 4 {
			break
		}
	}
	return codes
}

func investmentStockResolutionQuery(input string) string {
	input = strings.TrimSpace(input)
	if marker := strings.LastIndex(input, "\n---\n"); marker >= 0 {
		input = strings.TrimSpace(input[marker+len("\n---\n"):])
	}
	lines := strings.Split(input, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" || line == "---" {
			continue
		}
		input = line
		break
	}
	return textutil.TruncateUTF8Bytes(strings.TrimSpace(input), 160)
}

func resolveAShareCodesFromSearch(ctx context.Context, search Tool, input string) ([]string, error) {
	if search == nil {
		return nil, nil
	}
	query := investmentStockResolutionQuery(input)
	if query == "" {
		return nil, nil
	}
	result, err := search.Execute(ctx, map[string]interface{}{
		"query":      query + " A股 证券代码",
		"engine":     "sogou",
		"maxResults": 5,
	})
	if err != nil {
		return nil, err
	}
	codes := explicitAShareCodes(result)
	if len(codes) > 1 {
		codes = codes[:1]
	}
	return codes, nil
}

func investmentInputMentionsCompany(input, companyName string) bool {
	input = strings.ReplaceAll(strings.TrimSpace(input), " ", "")
	companyName = strings.ReplaceAll(strings.TrimSpace(companyName), " ", "")
	if input == "" || companyName == "" {
		return false
	}
	if strings.Contains(input, companyName) {
		return true
	}
	for _, suffix := range []string{"股份有限公司", "集团有限公司", "有限公司"} {
		alias := strings.TrimSuffix(companyName, suffix)
		if len([]rune(alias)) >= 3 && strings.Contains(input, alias) {
			return true
		}
	}
	return false
}

func (e *PipelineExecutor) hostCollectInvestmentEvidence(ctx context.Context, exec *PipelineExecution, step *PipelineStep, stepIndex int, input string) (string, bool, error) {
	if e == nil || exec == nil || step == nil || exec.PipelineName != "investment-advisory" || step.Name != "自主判断与证据采集" {
		return "", false, nil
	}
	profile := investmentProfileFromExecution(exec, stepIndex, input)
	if !investmentadvisory.InvestmentIntentUsesStockFacts(profile.Intent) {
		// Market, sector, fund/ETF, event and education requests must not take
		// the A-share security-code resolver detour. Their evidence collector is
		// the model step with the tools declared by the active pipeline.
		return "", false, nil
	}
	registry := e.buildStepRegistry([]string{"stock_facts", "web_search"}, exec, stepIndex)
	codes := explicitAShareCodes(input)
	resolvedBySearch := false
	if len(codes) == 0 {
		if search, ok := registry.Get("web_search"); ok {
			resolveCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			resolved, _ := resolveAShareCodesFromSearch(resolveCtx, search, input)
			cancel()
			if len(resolved) > 0 {
				codes = resolved
				resolvedBySearch = true
			}
		}
	}
	if len(codes) == 0 {
		return "", false, nil
	}
	tool, ok := registry.Get("stock_facts")
	if !ok {
		return "", false, nil
	}

	type request struct {
		code     string
		provider string
	}
	providers := []string{networktools.StockFactsProviderTencentMarket, networktools.StockFactsProviderEastmoney, networktools.StockFactsProviderOfficialReport}
	requests := make([]request, 0, len(codes)*len(providers))
	for _, code := range codes {
		for _, provider := range providers {
			requests = append(requests, request{code: code, provider: provider})
		}
	}

	// The structured providers have their own bounded HTTP timeouts. This outer
	// deadline prevents one PDF extraction from holding the whole employee task.
	collectCtx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	results := make([]string, len(requests))
	errs := make([]error, len(requests))
	var wg sync.WaitGroup
	for i, req := range requests {
		wg.Add(1)
		go func(index int, item request) {
			defer wg.Done()
			params := map[string]interface{}{
				"symbol":        item.code,
				"provider":      item.provider,
				"as_of":         time.Now().Format("2006-01-02"),
				"lookback_days": 250,
			}
			results[index], errs[index] = tool.Execute(collectCtx, params)
		}(i, req)
	}
	wg.Wait()

	wrapped := make([]string, 0, len(results))
	var failures []string
	for i, result := range results {
		call := ToolCall{Name: "stock_facts", Arguments: map[string]interface{}{
			"symbol": requests[i].code, "provider": requests[i].provider,
		}}
		if errs[i] != nil {
			failures = append(failures, fmt.Sprintf("%s/%s: %v", requests[i].code, requests[i].provider, errs[i]))
			continue
		}
		wrapped = append(wrapped, toolResultForFinalization(call, result, true))
	}
	grouped := structuredStockFactsFromResults(wrapped)
	if resolvedBySearch && len(grouped) > 0 {
		validated := make(map[string]map[string]networktools.StockFactsResult)
		validatedCodes := make([]string, 0, len(codes))
		for _, code := range codes {
			providers := grouped[code]
			companyName := providers[networktools.StockFactsProviderTencentMarket].CompanyName
			if companyName == "" {
				companyName = providers[networktools.StockFactsProviderEastmoney].CompanyName
			}
			if companyName == "" {
				companyName = providers[networktools.StockFactsProviderOfficialReport].CompanyName
			}
			if !investmentInputMentionsCompany(input, companyName) {
				continue
			}
			validated[code] = providers
			validatedCodes = append(validatedCodes, code)
		}
		grouped = validated
		codes = validatedCodes
	}
	if len(grouped) == 0 {
		if len(failures) == 0 {
			return "", false, nil
		}
		return "", true, fmt.Errorf("structured A-share evidence unavailable: %s", strings.Join(failures, "; "))
	}

	var out strings.Builder
	out.WriteString("HOST_STRUCTURED_A_SHARE_EVIDENCE: READY\n")
	if resolvedBySearch {
		fmt.Fprintf(&out, "本证据包由宿主将用户输入中的证券简称解析为 %s，并用结构化行情返回的公司简称完成一致性核验后统一并行采集。\n", strings.Join(codes, ","))
	} else {
		out.WriteString("本证据包由宿主按用户明确给出的 A 股代码统一并行采集。\n")
	}
	out.WriteString("采集编排未调用技能、Shell、普通行情网页或模型。\n")
	out.WriteString("行情为未复权价格回报，不含现金分红；不同提供方的估值标签原样并列，不平均。\n\n")
	fmt.Fprintf(&out, "- task_type=%s\n", map[bool]string{true: "single_stock", false: "multi_stock"}[len(codes) == 1])
	fmt.Fprintf(&out, "- position_state=%s；只有用户明确给出的持仓、成本和账户约束才可进入风险预算。\n\n", investmentPositionState(input))
	ordered := append([]string(nil), codes...)
	for _, code := range ordered {
		facts := grouped[code]
		market := facts[networktools.StockFactsProviderTencentMarket]
		secondary := facts[networktools.StockFactsProviderEastmoney]
		official := facts[networktools.StockFactsProviderOfficialReport]
		if market.Valuation == nil {
			market.Valuation = &networktools.StockValuationFacts{}
		}
		if secondary.Valuation == nil {
			secondary.Valuation = &networktools.StockValuationFacts{}
		}
		company := market.CompanyName
		if company == "" {
			company = secondary.CompanyName
		}
		fmt.Fprintf(&out, "## %s %s\n", code, company)
		if market.Quote != nil {
			fmt.Fprintf(&out, "- 行情事实：trade_date=%s；quote_time=%s；market_state=%s；close=%s CNY。\n",
				market.Quote.TradeDate, market.Quote.QuoteTime, market.Quote.MarketState, stockartifact.OptionalRawNumber(market.Quote.Close))
		}
		if market.History != nil {
			fmt.Fprintf(&out, "- 未复权价格表现：5D=%s；20D=%s；60D=%s；全样本期=%s（%d个交易日，近一年口径）；MA5/20/60=%.2f/%.2f/%.2f；样本=%s..%s。\n",
				stockartifact.FormatPercent(market.History.Return5DPct), stockartifact.FormatPercent(market.History.Return20DPct), stockartifact.FormatPercent(market.History.Return60DPct), stockartifact.FormatPercent(market.History.ReturnPeriodPct), market.History.Observations,
				market.History.MA5, market.History.MA20, market.History.MA60, market.History.FirstDate, market.History.LastDate)
		}
		fmt.Fprintf(&out, "- 估值标签（腾讯）：PE_TTM=%s；PB=%s；market_cap_CNY=%s。\n",
			stockartifact.OptionalRawNumber(market.Valuation.PETTM), stockartifact.OptionalRawNumber(market.Valuation.PB), stockartifact.OptionalRawNumber(market.Valuation.MarketCap))
		fmt.Fprintf(&out, "- 估值标签（东方财富）：PE_TTM=%s；PB=%s；market_cap_CNY=%s。\n",
			stockartifact.OptionalRawNumber(secondary.Valuation.PETTM), stockartifact.OptionalRawNumber(secondary.Valuation.PB), stockartifact.OptionalRawNumber(secondary.Valuation.MarketCap))
		fmt.Fprintf(&out, "- 行情直接来源：%s；%s\n", market.SourceURL, secondary.SourceURL)
		if official.Report != nil {
			fmt.Fprintf(&out, "- 最新官方报告：%s；period=%s；announcement_date=%s；source=%s\n",
				official.Report.Title, official.Report.Period, official.Report.AnnouncementDate, official.Report.SourceURL)
			for _, metric := range []string{"revenue", "parent_net_profit", "operating_cash_flow", "eps", "roe"} {
				fmt.Fprintf(&out, "  - %s=%s\n", metric, stockartifact.RawReportMetric(official.Report.Metrics[metric]))
			}
		}
		if len(official.Announcements) > 0 {
			out.WriteString("- 近期官方公告：\n")
			limit := len(official.Announcements)
			if limit > 3 {
				limit = 3
			}
			for _, announcement := range official.Announcements[:limit] {
				fmt.Fprintf(&out, "  - %s｜%s｜%s\n", announcement.Date, announcement.Title, announcement.URL)
			}
		}
		out.WriteString("\n")
	}
	if len(failures) > 0 {
		sort.Strings(failures)
		out.WriteString("## 采集失败（下游必须标为 fetch_failed/unknown，不得补造）\n")
		for _, failure := range failures {
			fmt.Fprintf(&out, "- %s\n", failure)
		}
	}
	out.WriteString("\n## 下游分析边界\n")
	out.WriteString("- 可直接陈述以上结构化事实；公司行业逻辑、排序和风险触发器若非证据直接陈述，必须标为 transparent_inference。\n")
	out.WriteString("- 官方字段状态为 not_found_after_pdf_extraction 时属于明确 unknown，不启动泛搜填数。\n")
	out.WriteString("- 覆盖矩阵：价格/技术=verified；估值=verified 或 source_conflict；基本面=以官方报告 observed 状态为准；资金面=unknown；公告/新闻/舆情=partial；短线情报=partial。\n")
	out.WriteString("- 公告标题只能作为事件线索，正文主张、事件发生日和情绪驱动必须由下游绑定 claim_id/source_id 后才能使用。\n")
	out.WriteString("- 用户指定的最终路径只是交付目标；本阶段没有创建或写入最终文件。\n")
	out.WriteString("\n<!--HOST_FALLBACK_DECISION_BEGIN-->\n")
	out.WriteString(buildDeterministicInvestmentFallback(grouped, ordered, investmentPositionState(input)))
	out.WriteString("\n<!--HOST_FALLBACK_DECISION_END-->\n")
	return strings.TrimSpace(out.String()), true, nil
}

func buildDeterministicInvestmentFallback(grouped map[string]map[string]networktools.StockFactsResult, codes []string, positionState string) string {
	type candidate struct {
		code         string
		name         string
		facts        networktools.StockFactsResult
		secondary    networktools.StockFactsResult
		official     networktools.StockFactsResult
		return60     float64
		observed     int
		marketCapGap float64
	}
	var candidates []candidate
	for _, code := range codes {
		providers := grouped[code]
		market := providers[networktools.StockFactsProviderTencentMarket]
		secondary := providers[networktools.StockFactsProviderEastmoney]
		official := providers[networktools.StockFactsProviderOfficialReport]
		if market.History == nil || market.Quote == nil {
			continue
		}
		if market.Valuation == nil {
			market.Valuation = &networktools.StockValuationFacts{}
		}
		if secondary.Valuation == nil {
			secondary.Valuation = &networktools.StockValuationFacts{}
		}
		item := candidate{code: code, name: market.CompanyName, facts: market, secondary: secondary, official: official, return60: market.History.Return60DPct}
		if official.Report != nil {
			for _, metric := range official.Report.Metrics {
				if metric.Status == "observed" {
					item.observed++
				}
			}
		}
		if market.Valuation.MarketCap != nil && secondary.Valuation.MarketCap != nil && *market.Valuation.MarketCap != 0 {
			item.marketCapGap = (*secondary.Valuation.MarketCap - *market.Valuation.MarketCap) / *market.Valuation.MarketCap * 100
			if item.marketCapGap < 0 {
				item.marketCapGap = -item.marketCapGap
			}
		}
		candidates = append(candidates, item)
	}
	sort.SliceStable(candidates, func(i, j int) bool { return candidates[i].return60 > candidates[j].return60 })

	var out strings.Builder
	if positionState == "" {
		positionState = "not_provided"
	}
	out.WriteString("## 证据覆盖与质量门\n\n")
	if len(candidates) > 0 {
		out.WriteString("交付状态: degraded\n")
	} else {
		out.WriteString("交付状态: no_decision\n")
	}
	fmt.Fprintf(&out, "- position_state: %s\n", positionState)
	out.WriteString("- 降级原因：质量合成未完整通过，已启用宿主旧版兜底；旧版主流程仍基于宿主可核验事实形成条件式后市观察，但不把它包装成确定性投资决策。\n\n")
	out.WriteString("| 信号模块 | 状态 | 证据边界 |\n|---|---|---|\n")
	out.WriteString("| 基本面 | partial/unknown | 仅使用最新官方报告中可结构化 observed 的指标 |\n")
	out.WriteString("| 估值 | verified/conflict | 腾讯与东方财富标签原样并列，不平均 |\n")
	out.WriteString("| 技术/短线 | partial | 仅有未复权日线、均线和区间数据，无量价/资金/回测 |\n")
	out.WriteString("| 新闻/情绪 | unknown | 公告标题不是正文证据，未形成可解释情绪结论 |\n\n")

	out.WriteString("## 基本面与估值\n\n")
	for _, item := range candidates {
		fmt.Fprintf(&out, "### %s %s\n\n", item.code, item.name)
		fmt.Fprintf(&out, "- 腾讯估值标签：PE_TTM=%s，PB=%s；东方财富估值标签：PE_TTM=%s，PB=%s。两源只作口径核对，不能仅凭倍数高低判定贵贱。\n",
			stockartifact.OptionalRawNumber(item.facts.Valuation.PETTM), stockartifact.OptionalRawNumber(item.facts.Valuation.PB),
			stockartifact.OptionalRawNumber(item.secondary.Valuation.PETTM), stockartifact.OptionalRawNumber(item.secondary.Valuation.PB))
		if item.official.Report == nil {
			out.WriteString("- 最新官方财报：unknown；没有官方报告结构化结果，不推断盈利质量。\n")
		} else {
			fmt.Fprintf(&out, "- 最新官方财报：%s（period=%s，announcement_date=%s）；五项核心指标 observed=%d/5，其余为 unknown。\n",
				item.official.Report.Title, item.official.Report.Period, item.official.Report.AnnouncementDate, item.observed)
			for _, metric := range []struct{ key, label string }{
				{"revenue", "营业收入"}, {"parent_net_profit", "归母净利润"}, {"operating_cash_flow", "经营现金流"}, {"roe", "ROE"},
			} {
				value := item.official.Report.Metrics[metric.key]
				if value.Status == "observed" {
					fmt.Fprintf(&out, "  - %s：%s\n", metric.label, stockartifact.RawReportMetric(value))
				}
			}
			revenue := item.official.Report.Metrics["revenue"]
			profit := item.official.Report.Metrics["parent_net_profit"]
			if revenue.Status == "observed" && profit.Status == "observed" && revenue.YoYPct != nil && profit.YoYPct != nil {
				if *profit.YoYPct > *revenue.YoYPct {
					fmt.Fprintf(&out, "- 基本面解读：归母净利润同比 %.2f%%，明显快于营收同比 %.2f%%，显示盈利弹性正在释放；但仍需经营现金流和后续季度持续性验证。\n", *profit.YoYPct, *revenue.YoYPct)
				} else {
					fmt.Fprintf(&out, "- 基本面解读：营收同比 %.2f%%、归母净利润同比 %.2f%%，利润增速未明显领先收入，后续重点看盈利质量和现金流。\n", *revenue.YoYPct, *profit.YoYPct)
				}
			}
		}
		if item.facts.Valuation.PETTM != nil {
			fmt.Fprintf(&out, "- 估值含义：PE_TTM=%s，意味着股价对盈利预期变化较敏感；缺少历史分位和同业统一口径时，不直接贴高估或低估标签。\n", stockartifact.OptionalRawNumber(item.facts.Valuation.PETTM))
		}
		out.WriteString("\n#### 盈利驱动与验证链条\n\n")
		if item.official.Report == nil {
			out.WriteString("- **当前证据**：官方定期报告暂未形成结构化结果，业务景气、盈利弹性和现金流质量均保持 unknown。\n")
		} else {
			revenue := item.official.Report.Metrics["revenue"]
			profit := item.official.Report.Metrics["parent_net_profit"]
			cashFlow := item.official.Report.Metrics["operating_cash_flow"]
			if revenue.Status == "observed" && profit.Status == "observed" && revenue.YoYPct != nil && profit.YoYPct != nil {
				fmt.Fprintf(&out, "- **当前证据**：收入同比 %.2f%%、归母净利润同比 %.2f%%；利润增速相对收入的差值约 %.2f 个百分点，属于盈利弹性线索，不等于全年趋势已经确认。\n", *revenue.YoYPct, *profit.YoYPct, *profit.YoYPct-*revenue.YoYPct)
			}
			if cashFlow.Status == "observed" {
				fmt.Fprintf(&out, "- **质量验证**：经营现金流已取得，需与净利润方向和幅度交叉检查：%s。\n", stockartifact.RawReportMetric(cashFlow))
			} else {
				out.WriteString("- **最强反证缺口**：经营现金流未取得，利润增速尚不能直接提升为盈利质量改善；后续需看现金回款、毛利率和下一报告期持续性。\n")
			}
		}
		out.WriteString("- **估值传导**：当前估值对盈利预期较敏感；若后续盈利兑现弱于预期，估值与价格可能同时承压，若盈利和现金流同步验证，才构成更完整的正向证据链。\n")
	}

	out.WriteString("\n## 金融信息与情绪\n\n")
	for _, item := range candidates {
		fmt.Fprintf(&out, "- %s %s：", item.code, item.name)
		if len(item.official.Announcements) == 0 {
			out.WriteString("近期公告与新闻覆盖 unknown；不得据此宣称没有事件。\n")
			continue
		}
		limit := len(item.official.Announcements)
		if limit > 3 {
			limit = 3
		}
		fmt.Fprintf(&out, "已取得 %d 条近期官方公告线索；公告正文尚未逐项核验，先作为催化与风险观察清单：\n", limit)
		for i := 0; i < limit; i++ {
			announcement := item.official.Announcements[i]
			fmt.Fprintf(&out, "  - %s｜%s\n", announcement.Date, announcement.Title)
		}
		hasForecast := false
		hasGuarantee := false
		for _, announcement := range item.official.Announcements[:limit] {
			hasForecast = hasForecast || strings.Contains(announcement.Title, "预增") || strings.Contains(announcement.Title, "业绩预告")
			hasGuarantee = hasGuarantee || strings.Contains(announcement.Title, "担保")
		}
		out.WriteString("\n  **催化与验证清单**\n")
		if hasForecast {
			out.WriteString("  - 业绩预增属于正向事件线索；必须核验预告区间、同比基数、利润来源和扣非口径，才能判断市场是否已充分反映。\n")
		}
		if hasGuarantee {
			out.WriteString("  - 担保公告属于风险线索；需核验被担保方、余额、期限、反担保和逾期情况，标题本身不能推出风险已经发生。\n")
		}
		out.WriteString("  - 若公告正文与当前盈利或风险假设冲突，应优先下调结论置信度；媒体或社区转载不能替代公告原文。\n")
	}

	out.WriteString("\n## 短线交易情报\n\n")
	out.WriteString("> 仅是条件观察，不是订单、胜率或收益预测；缺少成交量、资金流和回测时不得补造。\n\n")
	for _, item := range candidates {
		h := item.facts.History
		q := item.facts.Quote
		structure := "多空分歧、偏震荡"
		if q.Close != nil && *q.Close >= h.MA20 && *q.Close >= h.MA60 && h.Return20DPct >= 0 {
			structure = "中期结构偏强"
		} else if q.Close != nil && *q.Close < h.MA20 && *q.Close < h.MA60 && h.Return20DPct < 0 {
			structure = "中期结构偏弱"
		}
		fmt.Fprintf(&out, "- **%s %s**：截至 %s 收盘 %s；5/20/60日未复权回报=%s/%s/%s；MA5/20/60=%.2f/%.2f/%.2f，当前判定为**%s**。%s。只有收盘站稳关键均线/区间并得到量价确认，偏强场景才延续；跌破关键支撑则转入防守观察。量能、资金和事件窗口不足时不提高置信度。\n",
			item.code, item.name, q.TradeDate, stockartifact.OptionalRawNumber(q.Close), stockartifact.FormatPercent(h.Return5DPct), stockartifact.FormatPercent(h.Return20DPct), stockartifact.FormatPercent(h.Return60DPct), h.MA5, h.MA20, h.MA60, structure, investmentTechnicalLevelSummary(q.Close, h))
		fmt.Fprintf(&out, "\n#### 三种条件情景（%s %s）\n\n", item.code, item.name)
		fmt.Fprintf(&out, "| 情景 | 需要出现的证据 | 判断含义 | 失效条件 |\n|---|---|---|---|\n")
		fmt.Fprintf(&out, "| 改善 | 收盘依次收复 MA5 %.2f、MA60 %.2f、MA20 %.2f，并有量价或事件确认 | 弱势修复逐步升级，但不是单日突破即确认反转 | 再次跌回关键均线下方，或催化正文弱于预期 |\n", h.MA5, h.MA60, h.MA20)
		fmt.Fprintf(&out, "| 基准 | 维持在 60 日区间低点 %.2f 上方，但未能有效收复主要均线 | 继续按震荡或偏弱观察，等待盈利与量价证据补齐 | 放量突破主要压力，或有效跌破区间低点 |\n", h.Range60Low)
		fmt.Fprintf(&out, "| 转弱 | 收盘有效跌破 60 日区间低点 %.2f，且后续不能快速收回 | 下行风险上升，原有修复假设失效 | 重新站回区间并得到新证据确认 |\n", h.Range60Low)
	}

	out.WriteString("\n## 风险预算与失效条件\n\n")
	if positionState == "not_provided" {
		out.WriteString("- 用户未提供持仓、成本、账户规模和单笔容忍损失，不能计算仓位百分比或下单数量；只给风险触发器。\n\n")
	} else {
		out.WriteString("- 用户提到了持仓或账户上下文，但宿主证据包未验证完整风险参数；在账户规模、成本和容忍损失齐备前不计算仓位或数量。\n\n")
	}
	for _, item := range candidates {
		h := item.facts.History
		q := item.facts.Quote
		fmt.Fprintf(&out, "### %s %s\n", item.code, item.name)
		fmt.Fprintf(&out, "1. **趋势风险**：60日未复权价格回报 %s，收盘 %s、MA60 %.2f。触发器：后续收盘跌破60日区间低点 %.2f，或持续低于 MA60。\n", stockartifact.FormatPercent(item.return60), stockartifact.OptionalRawNumber(q.Close), h.MA60, h.Range60Low)
		if item.marketCapGap >= 2 {
			fmt.Fprintf(&out, "2. **估值口径风险**：两行情商总市值标签相差约 %.1f%%（source_conflict）。触发器：用市值衍生结论前仍无法由官方股本口径解释差异；PE/PB原样并列，不平均。\n", item.marketCapGap)
		} else {
			fmt.Fprintf(&out, "2. **估值重定价风险**：当前 PE_TTM=%s、PB=%s。触发器：盈利预期变化导致 PE/PB 与价格方向背离；跨行业倍数不可直接判定高低估。\n", stockartifact.OptionalRawNumber(item.facts.Valuation.PETTM), stockartifact.OptionalRawNumber(item.facts.Valuation.PB))
		}
		if item.observed < 5 {
			fmt.Fprintf(&out, "3. **财报可见性风险**：最近官方报告五项核心指标仅 %d 项结构化 observed，其余为 unknown。触发器：下一报告仍无法核验，或补取后核心指标弱于当前观察假设。\n\n", item.observed)
		} else {
			fmt.Fprintf(&out, "3. **盈利兑现风险**：最近官方报告五项指标可核验。触发器：下一报告营收/归母净利同比转负，或 ROE/经营现金流明显弱化。\n\n")
		}
		out.WriteString("#### 标的风险矩阵\n\n")
		out.WriteString("| 风险维度 | 当前证据 | 触发器 | 传导路径 | 缓释/复核 |\n|---|---|---|---|---|\n")
		fmt.Fprintf(&out, "| 趋势 | 60日回报 %s，MA60 %.2f | 跌破 %.2f 或持续弱于 MA60 | 风险偏好下降、估值承压 | 收复均线并有量价确认 |\n", stockartifact.FormatPercent(item.return60), h.MA60, h.Range60Low)
		fmt.Fprintf(&out, "| 估值 | PE_TTM=%s、PB=%s | 盈利预期下修或口径冲突扩大 | 高敏感估值压缩 | 用历史分位、同业口径和官方股本复核 |\n", stockartifact.OptionalRawNumber(item.facts.Valuation.PETTM), stockartifact.OptionalRawNumber(item.facts.Valuation.PB))
		fmt.Fprintf(&out, "| 盈利质量 | 官方核心指标覆盖 %d/5 | 现金流、ROE 或下一期利润弱化 | 盈利弹性无法兑现 | 补齐现金流、扣非利润和后续季度 |\n", item.observed)
		if len(item.official.Announcements) > 0 {
			out.WriteString("| 事件与治理 | 近期公告仅完成标题级覆盖 | 公告正文出现超预期负担、担保或治理事项 | 风险溢价上升 | 回到公告原文核验金额、对象和期限 |\n")
		} else {
			out.WriteString("| 事件与治理 | 近期公告覆盖 unknown | 后续出现重大未核验事件 | 风险溢价上升 | 补齐交易所和公司公告原文 |\n")
		}
		out.WriteString("| 数据完整性 | 资金、量价或回测覆盖不足 | 缺口长期未补齐却继续提高置信度 | 判断失真 | 保持部分覆盖标记，新增证据后再评估 |\n\n")
	}

	out.WriteString("## 综合结论\n\n")
	if len(candidates) > 1 {
		out.WriteString("同口径60日价格表现只能形成技术强弱观察，不能替代基本面、估值和事件核验，也不能作为综合投资排序。")
	} else if len(candidates) == 1 {
		item := candidates[0]
		h := item.facts.History
		q := item.facts.Quote
		outlook := "后市暂按震荡和信号分歧处理"
		if q.Close != nil && *q.Close >= h.MA20 && *q.Close >= h.MA60 && h.Return20DPct >= 0 {
			outlook = "后市处于偏强观察区，但需要量价和事件确认"
		} else if q.Close != nil && *q.Close < h.MA20 && *q.Close < h.MA60 && h.Return20DPct < 0 {
			outlook = "后市处于偏弱观察区，反转前先控制下行风险"
		}
		fmt.Fprintf(&out, "对 %s %s 的条件式判断：%s。%s；最新官方财报覆盖 %d/5 项核心指标，需结合其同比方向判断盈利改善能否兑现。该判断会在趋势失效、下一份财报转弱、重大公告正文与当前假设冲突，或资金/量价证据明显反向时改变。",
			item.code, item.name, outlook, investmentTechnicalLevelSummary(q.Close, h), item.observed)
		out.WriteString("最强支持证据是已核验财报与价格位置能够形成一致的观察框架；最强反证是现金流、资金面和公告正文仍不完整。短期先看关键均线与区间，后续一至两个报告期重点看盈利、现金流和估值是否同向验证。")
	} else {
		out.WriteString("主证据也不可用，当前只能 no_decision，不能形成后市方向判断。")
	}
	out.WriteString("补齐公告正文、行业供需、量价/资金、完整财报可比口径和用户风险约束后应重新评估；当前结果不得直接转化为真实交易指令，且未执行任何订单。")
	return strings.TrimSpace(out.String())
}

func investmentTechnicalLevelSummary(close *float64, history *networktools.StockHistoryFacts) string {
	if close == nil || history == nil {
		return "关键支撑与阻力暂无法核验"
	}
	type level struct {
		name  string
		value float64
	}
	all := []level{
		{name: "MA5", value: history.MA5},
		{name: "MA20", value: history.MA20},
		{name: "MA60", value: history.MA60},
		{name: "20日区间高点", value: history.Range20High},
		{name: "60日区间低点", value: history.Range60Low},
	}
	var supports, resistances []level
	for _, item := range all {
		if item.value <= 0 {
			continue
		}
		if item.value < *close {
			supports = append(supports, item)
		} else if item.value > *close {
			resistances = append(resistances, item)
		}
	}
	sort.SliceStable(supports, func(i, j int) bool { return supports[i].value > supports[j].value })
	sort.SliceStable(resistances, func(i, j int) bool { return resistances[i].value < resistances[j].value })
	format := func(items []level, limit int) string {
		if len(items) > limit {
			items = items[:limit]
		}
		parts := make([]string, 0, len(items))
		for _, item := range items {
			parts = append(parts, fmt.Sprintf("%s %.2f", item.name, item.value))
		}
		return strings.Join(parts, "、")
	}
	parts := make([]string, 0, 2)
	if len(resistances) > 0 {
		parts = append(parts, "现价上方阻力看 "+format(resistances, 4))
	}
	if len(supports) > 0 {
		parts = append(parts, "现价下方支撑看 "+format(supports, 3))
	}
	if len(parts) == 0 {
		return "现价恰处于已核验关键均线附近，需等待方向确认"
	}
	return strings.Join(parts, "；")
}

func investmentDeliveryTitle(evidence string) string {
	match := investmentEvidenceCompanyPattern.FindStringSubmatch(evidence)
	if len(match) != 3 {
		return "# 投资顾问分析报告"
	}
	return fmt.Sprintf("# %s（%s）投资分析", strings.TrimSpace(match[2]), match[1])
}

func investmentDecisionSections(decision string) map[string]string {
	matches := investmentDecisionSectionPattern.FindAllStringSubmatchIndex(decision, -1)
	sections := make(map[string]string, len(matches))
	for i, match := range matches {
		start := match[1]
		end := len(decision)
		if i+1 < len(matches) {
			end = matches[i+1][0]
		}
		name := strings.TrimSpace(decision[match[2]:match[3]])
		sections[name] = strings.TrimSpace(decision[start:end])
	}
	return sections
}

func cleanInvestmentDeliverySection(body string) string {
	var kept []string
	for _, line := range strings.Split(strings.TrimSpace(body), "\n") {
		trimmed := strings.TrimSpace(line)
		lower := strings.ToLower(trimmed)
		if investmentDeliveryStatusLinePattern.MatchString(trimmed) ||
			investmentPositionLinePattern.MatchString(trimmed) ||
			strings.Contains(trimmed, "已启用宿主旧版兜底") ||
			strings.HasPrefix(trimmed, "- 降级原因：") {
			continue
		}
		if strings.Trim(trimmed, "*_` ") == "" {
			continue
		}
		if strings.Contains(lower, "position_state") {
			continue
		}
		line = strings.ReplaceAll(line, "verified/conflict", "已核验（存在口径差异）")
		line = strings.ReplaceAll(line, "partial/unknown", "部分覆盖")
		line = strings.ReplaceAll(line, "其余为 unknown", "其余暂未提取")
		line = strings.ReplaceAll(line, "状态为 unknown", "暂不作方向判断")
		line = strings.ReplaceAll(line, "| unknown |", "| 暂未核验 |")
		line = strings.ReplaceAll(line, "unverified", "尚未核验")
		line = strings.ReplaceAll(line, "verified", "已核验")
		line = strings.ReplaceAll(line, "coverage_gap", "暂缺资料")
		line = strings.ReplaceAll(line, "fetch_failed", "暂未取得")
		line = strings.ReplaceAll(line, "unknown", "暂未取得")
		line = strings.ReplaceAll(line, "partial", "部分覆盖")
		line = strings.ReplaceAll(line, "degraded", "部分资料暂未取得")
		line = strings.ReplaceAll(line, "| partial |", "| 部分覆盖 |")
		line = strings.ReplaceAll(line, "observed=", "已提取=")
		line = strings.ReplaceAll(line, " observed", " 已提取")
		line = investmentObservedCountPattern.ReplaceAllString(line, "五项核心指标中已提取 $1 项")
		line = strings.ReplaceAll(line, "结构化 已提取", "结构化提取")
		line = strings.ReplaceAll(line, "可结构化 已提取 的指标", "可结构化提取的指标")
		line = strings.ReplaceAll(line, "period=", "报告期=")
		line = strings.ReplaceAll(line, "announcement_date=", "公告日期=")
		line = strings.ReplaceAll(line, "event_at 与 sentiment_drivers", "事件时间与情绪驱动")
		line = strings.ReplaceAll(line, "source_conflict", "来源口径冲突")
		line = strings.ReplaceAll(line, "DATA_STATUS: fetch_failed", "数据暂未取得")
		line = strings.ReplaceAll(line, "no_decision", "暂不形成方向判断")
		kept = append(kept, line)
	}
	return strings.TrimSpace(strings.Join(kept, "\n"))
}

func stripInvestmentInternalQualityGate(output string) (string, bool) {
	const marker = "## MACS 输出质量门（DEGRADED）"
	index := strings.Index(output, marker)
	if index < 0 {
		return strings.TrimSpace(output), false
	}
	return strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(output[:index]), "---")), true
}

func unusableInvestmentDecision(output string) bool {
	output = strings.TrimSpace(output)
	if output == "" {
		return true
	}
	return strings.Contains(output, tenantWorkspaceBoundaryRefusal) ||
		strings.Contains(output, tenantFilesystemEvidenceFailure) ||
		strings.Contains(output, unexecutedToolTranscriptFailure) ||
		(strings.Contains(output, "无法访问或披露") && strings.Contains(output, "当前用户工作目录之外")) ||
		strings.Contains(output, "没有取得可验证的当前工作目录结果") ||
		isPipelineStageFailurePlaceholder(output) ||
		strings.Contains(output, "iteration_budget_degraded") ||
		strings.Contains(output, "Upstream rework exhausted")
}

func investmentPlainLanguageGapReasons(decision string) []string {
	seen := make(map[string]bool)
	reasons := make([]string, 0, 3)
	for _, raw := range strings.Split(decision, "\n") {
		lower := strings.ToLower(raw)
		marker := strings.Index(lower, "coverage_gap")
		if marker < 0 {
			continue
		}
		reason := raw[marker+len("coverage_gap"):]
		reason = strings.TrimSpace(strings.TrimLeft(reason, "*_`：:- "))
		reason = strings.ReplaceAll(reason, "unknown", "暂未取得")
		reason = strings.ReplaceAll(reason, "未知", "暂未取得")
		reason = strings.ReplaceAll(reason, "未采集", "暂未取得")
		reason = strings.TrimRight(reason, "。；; ")
		if reason == "" || seen[reason] {
			continue
		}
		seen[reason] = true
		reasons = append(reasons, textutil.TruncateUTF8Bytes(reason, 180))
		if len(reasons) >= 3 {
			break
		}
	}
	return reasons
}

func investmentPlainLanguageDataNotice(decision string) string {
	return investmentPlainLanguageDataNoticeForIntent(decision, "")
}

func investmentPlainLanguageDataNoticeForIntent(decision, intent string) string {
	reasons := investmentPlainLanguageGapReasons(decision)
	if len(reasons) == 0 {
		switch strings.TrimSpace(intent) {
		case investmentadvisory.InvestmentIntentMarketBrief:
			return "> 资料说明：部分指数、成交、市场广度、板块或资金字段尚未完成交叉核验；以上仅保留可验证事实和中性观察，不据缺失项推断涨跌原因。"
		case investmentadvisory.InvestmentIntentSectorAnalysis:
			return "> 资料说明：部分行业指数、成分表现、资金、估值或事件字段尚未完成交叉核验；结论仅覆盖已核验的行业证据。"
		case investmentadvisory.InvestmentIntentFundETFAnalysis:
			return "> 资料说明：部分净值、跟踪误差、费率、规模、持仓或流动性字段尚未完成交叉核验；结论仅覆盖已核验的产品信息。"
		default:
			return "> 资料说明：本次分析已形成，但经营现金流、资金面和近期公告正文等资料暂未完整取得，因此相关判断采用谨慎口径。"
		}
	}
	return "> 资料说明：本次分析已形成，但以下资料暂未取得：" + strings.Join(reasons, "；") + "。因此相关判断采用谨慎口径。"
}

func investmentDecisionForDelivery(decision string) string {
	sections := investmentDecisionSections(decision)
	if len(sections) == 0 {
		return cleanInvestmentDeliverySection(decision)
	}
	type deliverySection struct {
		title  string
		source string
	}
	order := []deliverySection{
		{title: "结论先行", source: "综合结论"},
		{title: "基本面与估值", source: "基本面与估值"},
		{title: "走势与关键位置", source: "短线交易情报"},
		{title: "近期事件与催化", source: "金融信息与情绪"},
		{title: "主要风险与失效条件", source: "风险预算与失效条件"},
		{title: "数据边界", source: "证据覆盖与质量门"},
	}
	var out strings.Builder
	for _, item := range order {
		body := cleanInvestmentDeliverySection(sections[item.source])
		if body == "" {
			continue
		}
		if out.Len() > 0 {
			out.WriteString("\n\n")
		}
		fmt.Fprintf(&out, "## %s\n\n%s", item.title, body)
	}
	if out.Len() == 0 {
		return cleanInvestmentDeliverySection(decision)
	}
	return strings.TrimSpace(out.String())
}

func cleanInvestmentEvidenceAppendix(appendix string) string {
	replacements := []struct{ old, new string }{
		{"trade_date=", "交易日="},
		{"quote_time=", "行情时间="},
		{"market_state=closed", "市场状态=已收盘"},
		{"market_state=open", "市场状态=交易中"},
		{"close=", "收盘价="},
		{"period=", "报告期="},
		{"announcement_date=", "公告日期="},
		{"source=", "来源="},
	}
	for _, replacement := range replacements {
		appendix = strings.ReplaceAll(appendix, replacement.old, replacement.new)
	}
	return strings.TrimSpace(appendix)
}

func splitHostFallbackDecision(evidence string) (string, string) {
	const begin = "<!--HOST_FALLBACK_DECISION_BEGIN-->"
	const end = "<!--HOST_FALLBACK_DECISION_END-->"
	start := strings.Index(evidence, begin)
	finish := strings.Index(evidence, end)
	if start < 0 || finish <= start {
		return strings.TrimSpace(evidence), ""
	}
	decision := strings.TrimSpace(evidence[start+len(begin) : finish])
	cleanEvidence := strings.TrimSpace(evidence[:start] + evidence[finish+len(end):])
	return cleanEvidence, decision
}

// hostFallbackInvestmentQualityGate keeps the restored investment-advisory
// workflow deliverable when the final synthesis model times out, returns an
// empty answer, or is cut off by its completion budget. The evidence step
// already froze a deterministic, source-bounded fallback, so this path does
// not invent facts or depend on any optional enhancement branch.
func (e *PipelineExecutor) hostFallbackInvestmentQualityGate(exec *PipelineExecution, step *PipelineStep, input string) (string, bool) {
	if e == nil || exec == nil || step == nil || exec.PipelineName != "investment-advisory" || step.Name != "投研合成与质量门" {
		return "", false
	}
	exec.mu.Lock()
	steps := append([]StepResult(nil), exec.Steps...)
	exec.mu.Unlock()
	evidenceIndex := -1
	for i := range steps {
		if steps[i].Name == "自主判断与证据采集" {
			evidenceIndex = i
			break
		}
	}
	if evidenceIndex < 0 {
		return "", false
	}
	_, fallback := splitHostFallbackDecision(steps[evidenceIndex].Output)
	if strings.TrimSpace(fallback) == "" {
		return "", false
	}
	fallback = normalizeInvestmentPositionStateOutput(fallback, investmentPositionState(input))
	return strings.TrimSpace(fallback), true
}

func pipelineStepResultIndex(exec *PipelineExecution, name string) int {
	if exec == nil {
		return -1
	}
	for i := range exec.Steps {
		if exec.Steps[i].Name == name {
			return i
		}
	}
	return -1
}

func isPipelineStageFailurePlaceholder(output string) bool {
	output = strings.TrimSpace(output)
	return strings.HasPrefix(output, "## 阶段降级") &&
		strings.Contains(output, "## 后续步骤使用规则") &&
		strings.Contains(output, "本阶段未形成完整结果")
}

func investmentParallelStageAllFailed(output string) bool {
	branches := len(investmentParallelBranchPattern.FindAllString(output, -1))
	failures := len(investmentParallelFailurePattern.FindAllString(output, -1))
	return branches > 0 && failures >= branches
}

func sanitizeInvestmentParallelFailures(output string) string {
	if !investmentParallelFailurePattern.MatchString(output) {
		return output
	}
	return investmentParallelReasonPattern.ReplaceAllString(output, "原因：可选增强支路不可用（已降级，不影响旧版主流程交付）")
}

func compactInvestmentEvidenceAppendix(evidence string) string {
	var kept []string
	announcementLines := 0
	for _, raw := range strings.Split(evidence, "\n") {
		line := strings.TrimSpace(raw)
		switch {
		case strings.HasPrefix(line, "## ") && explicitAShareCodePattern.MatchString(line):
			kept = append(kept, line)
		case strings.HasPrefix(line, "- 行情事实："), strings.HasPrefix(line, "- 行情直接来源："), strings.HasPrefix(line, "- 最新官方报告："):
			kept = append(kept, line)
		case strings.HasPrefix(line, "- 近期官方公告："):
			kept = append(kept, line)
			announcementLines = 3
		case announcementLines > 0 && strings.HasPrefix(line, "- 20"):
			kept = append(kept, "  "+line)
			announcementLines--
		}
	}
	if len(kept) == 0 {
		return "关键来源未形成可压缩摘要；以上结论仅保留已标注的证据边界。"
	}
	return strings.Join(kept, "\n")
}

func (e *PipelineExecutor) hostDeliverInvestmentReport(ctx context.Context, exec *PipelineExecution, step *PipelineStep, stepIndex int, input string) (string, bool, error) {
	if e == nil || exec == nil || step == nil || exec.PipelineName != "investment-advisory" ||
		(step.Name != "报告交付" && step.Name != "结果交付") {
		return "", false, nil
	}
	profile := investmentProfileFromExecution(exec, stepIndex, input)
	evidenceIndex := pipelineStepResultIndex(exec, "自主判断与证据采集")
	intelligenceIndex := pipelineStepResultIndex(exec, "金融信息与短线情报")
	analysisIndex := pipelineStepResultIndex(exec, "并行投研分析")
	fusedAnalysisIndex := pipelineStepResultIndex(exec, "旧版深度投研与融合分析")
	if adaptiveIndex := pipelineStepResultIndex(exec, "分类型研究与研判"); adaptiveIndex >= 0 {
		fusedAnalysisIndex = adaptiveIndex
	}
	decisionIndex := pipelineStepResultIndex(exec, "投研合成与质量门")
	fusedWorkflow := fusedAnalysisIndex >= 0
	if fusedWorkflow {
		intelligenceIndex = fusedAnalysisIndex
		analysisIndex = fusedAnalysisIndex
	}
	if evidenceIndex < 0 || intelligenceIndex < 0 || analysisIndex < 0 || decisionIndex < 0 || stepIndex <= decisionIndex {
		return "", false, nil
	}
	evidence := strings.TrimSpace(exec.Steps[evidenceIndex].Output)
	evidenceDegraded := exec.Steps[evidenceIndex].Status == "degraded" || exec.Steps[evidenceIndex].Error != "" ||
		evidence == "" || isPipelineStageFailurePlaceholder(evidence)
	evidence, fallbackDecision := splitHostFallbackDecision(evidence)
	intelligence := strings.TrimSpace(sanitizeInvestmentParallelFailures(cleanPipelineUserOutput(exec.Steps[intelligenceIndex].Output)))
	analysis := strings.TrimSpace(sanitizeInvestmentParallelFailures(cleanPipelineUserOutput(exec.Steps[analysisIndex].Output)))
	decision := strings.TrimSpace(cleanPipelineUserOutput(exec.Steps[decisionIndex].Output))
	var internalQualityGate bool
	decision, internalQualityGate = stripInvestmentInternalQualityGate(decision)
	intelligenceDegraded := exec.Steps[intelligenceIndex].Status == "degraded" || exec.Steps[intelligenceIndex].Error != "" || strings.Contains(intelligence, "DATA_STATUS: fetch_failed")
	analysisDegraded := exec.Steps[analysisIndex].Status == "degraded" || exec.Steps[analysisIndex].Error != "" || strings.Contains(analysis, "DATA_STATUS: fetch_failed")
	decisionDegraded := exec.Steps[decisionIndex].Status == "degraded" || exec.Steps[decisionIndex].Error != "" || internalQualityGate || unusableInvestmentDecision(decision)
	decisionFallbackUsed := strings.Contains(decision, "已启用宿主旧版兜底")
	if unusableInvestmentDecision(decision) {
		decisionFallbackUsed = true
		decision = fallbackDecision
		if decision == "" {
			decision = "## 证据覆盖与质量门\n\n交付状态: no_decision\n\nposition_state: not_provided\n\n- 质量合成未完整通过且宿主证据不足；以下仅交付已核验事实，不形成投资结论。"
		}
	} else if decisionDegraded {
		decision = "> 投研质量门状态：degraded。以下为未完全通过结构验收的阶段结果，必须结合缺口阅读，不得提升为确定性结论。\n\n" + decision
	}
	decision = normalizeInvestmentPositionStateOutput(decision, investmentPositionState(input))
	fusedAnalysisUnavailable := fusedWorkflow && (analysis == "" || isPipelineStageFailurePlaceholder(analysis))
	if intelligence == "" || isPipelineStageFailurePlaceholder(intelligence) || investmentParallelStageAllFailed(intelligence) {
		intelligence = "## 金融信息事件账本\n\ncoverage_gap：本轮未形成可用事件账本。\n\n## 短线交易情报\n\nunknown：仅保留宿主结构化行情，未补齐量价、资金、事件窗口或回测。\n\n## 覆盖缺口与冲突\n\n新闻/情绪与短线增强阶段不可用。"
		intelligenceDegraded = true
	}
	if fusedAnalysisUnavailable {
		analysis = "## 旧版深度投研阶段状态\n\n本轮模型未形成完整深度研判；宿主仍依据结构化行情、估值和官方财报生成前面的条件式结论。AI Hedge、投研经理和金信方法属于可选增强，其不可用不会删除旧版事实底座，也不会阻断交付。"
		analysisDegraded = true
	} else if analysis == "" || isPipelineStageFailurePlaceholder(analysis) || investmentParallelStageAllFailed(analysis) {
		analysis = "## 基本面、估值与多因子分析\n\nunknown：新增的 AI Hedge 多因子与投研经理方法支路未形成可用输出。旧版主流程不会用默认分数、虚构因子或模型占位值替代；基本面、估值、质量、成长与动量只保留上游可核验事实。\n\n## 金融信息、情绪与短线分析\n\nunknown：金信事件情绪、短线条件场景或风险分析支路不可用。该失败不等于没有事件、没有风险或没有交易信号，只表示本轮增强覆盖不足。\n\n## 风险与监测触发器\n\nno_decision：未取得足够支路证据时不计算概率、目标价、仓位或风险预算；用户可依据后续补齐的直接来源重新评估。"
		analysisDegraded = true
	}
	if evidence == "" || isPipelineStageFailurePlaceholder(evidence) {
		evidence = "## 旧版主流程证据状态\n\n- DATA_STATUS: fetch_failed\n- 本轮结构化证据采集未形成可用结果；这不会阻断报告交付，但所有行情、估值、财报、资金面与近期事件均必须视为 unknown。\n- 未核验不代表不存在，也不得据此生成确定性买卖结论、目标价、胜率或仓位建议。\n- 建议后续优先补齐：最新可获得交易日与市场状态、行情双源、最新官方财报/公告、量价与资金面、标的特有风险及其直接来源。"
		evidenceDegraded = true
	}

	overallStatus := "completed"
	if evidenceDegraded || intelligenceDegraded || analysisDegraded || decisionDegraded {
		overallStatus = "degraded"
	}
	answer := investmentDecisionForDelivery(decision)
	if overallStatus == "degraded" {
		answer += "\n\n" + investmentPlainLanguageDataNoticeForIntent(decision, profile.Intent)
	}
	answer += "\n\n> 风险提示：以上用于研究与风险识别，不承诺收益，不构成确定性买卖指令，也未执行任何真实交易。"
	_ = decisionFallbackUsed
	if evidenceDegraded {
		switch profile.Intent {
		case investmentadvisory.InvestmentIntentMarketBrief:
			// The contextual notice above already states the exact market-data
			// boundary. Do not turn a simple market brief into a second failure
			// report or ask the user for unrelated personal information.
		case investmentadvisory.InvestmentIntentSectorAnalysis:
			answer += `

---

## 数据缺口

- 本轮没有完整取得行业指数、成分表现、资金流、估值或关键事件中的部分数据。
- 缺失项不转换成个股代码追问；当前结论仅覆盖已经核验的行业证据。`
		case investmentadvisory.InvestmentIntentFundETFAnalysis:
			answer += `

---

## 数据缺口

- 本轮没有完整取得基金或 ETF 的净值、跟踪误差、费率、规模、持仓或流动性中的部分数据。
- 缺失项不会转换成股票持仓追问；需要精确产品比较时再补充具体基金或 ETF 代码。`
		default:
			nextStep := "若当前请求只给了证券简称，补充准确代码可以减少同名歧义；若标的已经明确，可在行情网络恢复后按同一标的重新执行。"
			if codes := explicitAShareCodes(input); len(codes) > 0 {
				nextStep = "证券代码已识别为 " + strings.Join(codes, "、") + "，无需重复提供；请在行情网络恢复后按同一标的重新执行。"
			}
			answer += `

---

## 当前无法核验的关键问题

- 尚未取得可交叉验证的最新行情，因此无法确认现价、趋势、支撑位和阻力位。
- 尚未取得最新官方定期报告，营收、利润、现金流、ROE 与估值数据均不能可靠引用。
- 尚未核验近期公告正文，不能判断业绩、产能、担保、诉讼或股东行为对公司的实际影响。

## 下一步需要什么

` + nextStep + `

只有要求结合本人持仓形成个性化操作方案时，才需要成本、仓位比例、持有周期和风险承受能力。`
		}
	} else {
		answer += "\n\n---\n\n## 关键来源与数据口径\n\n" + cleanInvestmentEvidenceAppendix(compactInvestmentEvidenceAppendix(evidence)) + "\n"
	}

	request := pipelineUserPolicyInput(ctx, exec)
	if request == "" {
		request = input
	}
	if !investmentRequestWantsArtifact(profile, request) {
		return strings.TrimSpace(answer), true, nil
	}

	report := investmentDeliveryTitle(evidence) + "\n\n" + answer
	path := requestedTextArtifactPath(request)
	if path == "" {
		workspace := "."
		if e.gateway != nil && e.gateway.config != nil && strings.TrimSpace(e.gateway.config.Workspace) != "" {
			workspace = e.gateway.config.Workspace
		}
		path = filepath.Join(workspace, ".openkitty", "storage", "investment-report-"+time.Now().Format("20060102")+".md")
	}
	if step.ArtifactContract != nil && step.ArtifactContract.MaterializeOutput {
		// The generic pipeline artifact contract owns persistence and validation.
		// Returning the complete report here avoids a second investment-specific
		// success path that could bypass provenance, stat, and size checks.
		return report, true, nil
	}

	registry := e.buildStepRegistry([]string{"write_file"}, exec, stepIndex)
	tool, ok := registry.Get("write_file")
	if !ok {
		return "", true, fmt.Errorf("write_file is unavailable for investment report delivery")
	}
	result, err := tool.Execute(ctx, map[string]interface{}{"path": path, "content": report, "append": false})
	if err != nil {
		return "", true, err
	}
	info, statErr := os.Stat(path)
	if statErr != nil || info.IsDir() || info.Size() < 1000 {
		return "", true, fmt.Errorf("investment report validation failed: %s", path)
	}
	return fmt.Sprintf("%s\n\n---\n\n投资报告已保存：%s\n- 状态：%s\n- 文件大小：%d bytes\n- 写入回执：%s", answer, path, overallStatus, info.Size(), truncate(result, 240)), true, nil
}

func investmentProfileFromExecution(exec *PipelineExecution, beforeStep int, input string) investmentadvisory.InvestmentRequestProfile {
	profile := investmentadvisory.ClassifyInvestmentRequest(input)
	if exec == nil {
		return profile
	}
	exec.mu.Lock()
	steps := append([]StepResult(nil), exec.Steps...)
	exec.mu.Unlock()
	if beforeStep > len(steps) {
		beforeStep = len(steps)
	}
	for i := beforeStep - 1; i >= 0; i-- {
		text := steps[i].Output
		if value := investmentProfileMarker(text, "INVESTMENT_INTENT"); value != "" {
			profile.Intent = value
		}
		if value := investmentProfileMarker(text, "DECISION_LEVEL"); value != "" {
			profile.DecisionLevel = value
		}
		if value := investmentProfileMarker(text, "ANSWER_DEPTH"); value != "" {
			profile.AnswerDepth = value
		}
		if value := investmentProfileMarker(text, "PERSONALIZATION_REQUIRED"); value != "" {
			profile.PersonalizationRequired = strings.EqualFold(value, "true") || value == "是"
		}
		if strings.Contains(text, "INVESTMENT_INTENT:") {
			break
		}
	}
	return profile
}

func investmentProfileMarker(text, name string) string {
	pattern := regexp.MustCompile(`(?mi)^\s*` + regexp.QuoteMeta(name) + `\s*[:：]\s*([^\r\n]+)`)
	match := pattern.FindStringSubmatch(text)
	if len(match) != 2 {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(match[1]))
}

func investmentRequestWantsArtifact(profile investmentadvisory.InvestmentRequestProfile, input string) bool {
	if permissionpolicy.DeriveUserToolPolicy(input).ForbidFileMutations {
		return false
	}
	if profile.AnswerDepth == "deep" {
		return true
	}
	return containsAnyText(strings.ToLower(input), []string{
		"保存", "写入", "落盘", "生成文件", "markdown", ".md", "报告路径",
	})
}

type leadResearchOutputRequirement struct {
	label string
	anyOf []string
}

func validateLeadResearchStageOutput(exec *PipelineExecution, step *PipelineStep, output string) error {
	if exec == nil || step == nil || !strings.EqualFold(strings.TrimSpace(exec.PipelineName), "lead-research") {
		return nil
	}
	// Product identity/positioning ambiguity is a normal waiting-for-input
	// outcome, not a malformed research deliverable. Let the step's stop route
	// return the clarification request to the user instead of forcing a contract
	// repair that would encourage the model to invent a product profile.
	if strings.TrimSpace(step.Name) == "产品画像与SWOT" && pipelineOutputWaitsForInput(output) {
		return nil
	}
	if strings.TrimSpace(step.Name) == "产品画像与SWOT" || strings.TrimSpace(step.Name) == "报告整理" {
		if err := validateLeadResearchProductIdentityClaims(exec, output); err != nil {
			return err
		}
	}

	var marker string
	var requirements []leadResearchOutputRequirement
	switch strings.TrimSpace(step.Name) {
	case "产品画像与SWOT":
		marker = "PRODUCT_PROFILE_STATUS: READY"
		for _, forbidden := range []string{
			".openkitty", ".exe", ".db", ".bak", ".pyc", "dbfix", "pipeline_metrics", "chatroom_key",
		} {
			if leadResearchOutputClaimsRuntimeEvidence(output, forbidden) {
				return fmt.Errorf("lead-research stage %q contains inadmissible runtime evidence %q", step.Name, forbidden)
			}
		}
		requirements = []leadResearchOutputRequirement{
			{label: "产品维度表", anyOf: []string{"产品维度", "产品画像"}},
			{label: "证据账本", anyOf: []string{"证据账本"}},
			{label: "SWOT", anyOf: []string{"SWOT"}},
			{label: "优势", anyOf: []string{"优势"}},
			{label: "劣势", anyOf: []string{"劣势", "缺点"}},
			{label: "机会", anyOf: []string{"机会"}},
			{label: "威胁", anyOf: []string{"威胁"}},
			{label: "竞品或替代方案", anyOf: []string{"竞品", "替代方案"}},
			{label: "负面ICP", anyOf: []string{"负面 ICP", "负面ICP"}},
			{label: "实体分类", anyOf: []string{"实体分类", "购买方、合作伙伴、竞品"}},
		}
	case "搜索策略":
		marker = "SEARCH_PLAN_STATUS: READY"
		requirements = []leadResearchOutputRequirement{
			{label: "搜索假设", anyOf: []string{"搜索假设"}},
			{label: "关键词矩阵", anyOf: []string{"关键词矩阵", "检索式矩阵"}},
			{label: "负面关键词", anyOf: []string{"负面关键词", "排除关键词"}},
			{label: "多源渠道计划", anyOf: []string{"多源渠道", "来源计划", "渠道计划"}},
			{label: "接纳/淘汰规则", anyOf: []string{"接纳", "淘汰"}},
			{label: "证据有效性标准", anyOf: []string{"证据有效性", "证据门槛"}},
		}
	case "多源搜索与核验":
		if !containsFold(output, "RESEARCH_STATUS: READY") && !containsFold(output, "RESEARCH_STATUS: DEGRADED") {
			return fmt.Errorf("lead-research stage %q missing RESEARCH_STATUS: READY or RESEARCH_STATUS: DEGRADED", step.Name)
		}
		requirements = []leadResearchOutputRequirement{
			{label: "检索执行记录", anyOf: []string{"检索执行记录", "搜索执行记录"}},
			{label: "实体分类表", anyOf: []string{"实体分类表", "实体分类"}},
			{label: "已核验潜客表", anyOf: []string{"已核验潜客", "核验潜客表"}},
			{label: "排除对象表", anyOf: []string{"排除对象"}},
			{label: "证据有效性审计", anyOf: []string{"证据有效性审计", "证据审计"}},
		}
	case "报告整理":
		requirements = []leadResearchOutputRequirement{
			{label: "产品结论与SWOT", anyOf: []string{"产品结论", "SWOT"}},
			{label: "检索关键词", anyOf: []string{"检索关键词", "搜索关键词"}},
			{label: "已核验潜客优先级表", anyOf: []string{"已核验潜客", "潜客优先级表"}},
			{label: "排除对象表", anyOf: []string{"排除对象"}},
			{label: "证据有效性审计", anyOf: []string{"证据有效性审计", "证据审计"}},
			{label: "下一轮检索计划", anyOf: []string{"下一轮检索", "下一轮搜索"}},
		}
	default:
		return nil
	}

	if marker != "" && !containsFold(output, marker) {
		return fmt.Errorf("lead-research stage %q missing required completion marker %q", step.Name, marker)
	}
	var missing []string
	for _, requirement := range requirements {
		found := false
		for _, candidate := range requirement.anyOf {
			if containsFold(output, candidate) {
				found = true
				break
			}
		}
		if !found {
			missing = append(missing, requirement.label)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("lead-research stage %q missing required sections: %s", step.Name, strings.Join(missing, "、"))
	}
	return nil
}

var leadResearchIdentityTokenPattern = regexp.MustCompile(`[A-Za-z][A-Za-z0-9._-]{2,}`)

func validateLeadResearchProductIdentityClaims(exec *PipelineExecution, output string) error {
	for _, claim := range leadResearchIdentityClaimLines(output) {
		if leadResearchIdentityClaimHasDirectSupport(exec, claim) {
			continue
		}
		return fmt.Errorf(
			"lead-research contains an unverified product identity/brand relationship; require the user task or a directly opened first-party/product file that explicitly binds both identities: %s",
			textutil.TruncateUTF8Bytes(claim, 240),
		)
	}
	return nil
}

func leadResearchIdentityClaimLines(output string) []string {
	var claims []string
	for _, raw := range strings.Split(strings.ReplaceAll(output, "\r\n", "\n"), "\n") {
		line := strings.TrimSpace(raw)
		lower := strings.ToLower(line)
		if line == "" {
			continue
		}
		if containsAnyText(lower, []string{
			"品牌面", "旗下品牌", "子品牌", "母品牌", "社区品牌", "开源品牌", "商业品牌",
			"原名为", "原名是", "更名为", "更名成", "别名为", "别名是", "前身为", "前身是",
			"brand of", "sub-brand", "formerly known as", "renamed to", "also known as",
		}) {
			claims = append(claims, line)
		}
	}
	return claims
}

func leadResearchIdentityClaimHasDirectSupport(exec *PipelineExecution, claim string) bool {
	if exec == nil {
		return false
	}
	tokens := leadResearchIdentityTokens(claim)
	if len(tokens) < 2 {
		return false
	}
	task := leadResearchTask(exec)
	if leadResearchIdentityEvidenceTextSupports(task, tokens) &&
		len(leadResearchIdentityClaimLines(task)) > 0 {
		return true
	}
	exec.mu.Lock()
	defer exec.mu.Unlock()
	for _, step := range exec.Steps {
		if strings.TrimSpace(step.Name) != "产品画像与SWOT" {
			continue
		}
		for _, call := range step.ToolCalls {
			switch strings.ToLower(strings.TrimSpace(call.ToolName)) {
			case "read_file", "web_fetch", "web_page", "web_navigate":
			default:
				continue
			}
			if pipelineToolCallRecordFailed(call) {
				continue
			}
			if leadResearchIdentityEvidenceTextSupports(call.Result, tokens) &&
				len(leadResearchIdentityClaimLines(call.Result)) > 0 {
				return true
			}
		}
	}
	return false
}

func leadResearchIdentityTokens(text string) []string {
	ignored := map[string]bool{
		"agent": true, "agentos": true, "api": true, "b2b": true,
		"crm": true, "erp": true, "github": true, "llm": true,
		"open": true, "os": true, "rpa": true, "saas": true, "swot": true,
	}
	seen := make(map[string]bool)
	var tokens []string
	for _, token := range leadResearchIdentityTokenPattern.FindAllString(text, -1) {
		normalized := strings.ToLower(strings.TrimSpace(token))
		if ignored[normalized] || seen[normalized] {
			continue
		}
		seen[normalized] = true
		tokens = append(tokens, normalized)
	}
	return tokens
}

func leadResearchIdentityEvidenceTextSupports(text string, tokens []string) bool {
	lower := strings.ToLower(text)
	for _, token := range tokens {
		if !strings.Contains(lower, token) {
			return false
		}
	}
	return true
}

func containsFold(text, part string) bool {
	return strings.Contains(strings.ToLower(text), strings.ToLower(part))
}

func leadResearchOutputClaimsRuntimeEvidence(output, forbidden string) bool {
	for _, line := range strings.Split(strings.ReplaceAll(output, "\r\n", "\n"), "\n") {
		if !containsFold(line, forbidden) {
			continue
		}
		lower := strings.ToLower(strings.TrimSpace(line))
		exclusion := false
		for _, marker := range []string{
			"不是产品证据", "不作为证据", "不得作为", "不能作为", "未作为", "未使用",
			"排除", "过滤", "拒绝", "忽略", "禁止读取", "不可读取",
			"not evidence", "must not use", "cannot use", "excluded", "filtered", "denied", "ignored",
		} {
			if strings.Contains(lower, marker) {
				exclusion = true
				break
			}
		}
		if !exclusion {
			return true
		}
	}
	return false
}

// LeadResearchProductSource is an optional, operator-configured identity hint.
// The generic lead-research employee has no built-in knowledge of any product.
// URLs remain candidates: the product-profile step must still open and verify
// the page before promoting page content to product facts.
type LeadResearchProductSource = routing.LeadResearchProductSource

func configuredLeadResearchProductSources(gateway *GatewayService) []LeadResearchProductSource {
	if gateway == nil || gateway.config == nil {
		return nil
	}
	return routing.DedupeLeadResearchProductSources(gateway.config.LeadResearchProducts)
}

func leadResearchProductSourcesForTask(gateway *GatewayService, task string) []LeadResearchProductSource {
	if gateway == nil || gateway.config == nil {
		return nil
	}
	return routing.MatchLeadResearchProductSources(gateway.config.LeadResearchProducts, task)
}

func appendLeadResearchProductSourceContext(prompt string, gateway *GatewayService, exec *PipelineExecution, step *PipelineStep, input string) string {
	if exec == nil || step == nil ||
		!strings.EqualFold(strings.TrimSpace(exec.PipelineName), "lead-research") ||
		strings.TrimSpace(step.Name) != "产品画像与SWOT" {
		return prompt
	}
	task := strings.TrimSpace(input)
	if exec.RuntimePipeline != nil && strings.TrimSpace(exec.RuntimePipeline.AIEmployeeOriginalTask) != "" {
		task = strings.TrimSpace(exec.RuntimePipeline.AIEmployeeOriginalTask)
	}
	sources := leadResearchProductSourcesForTask(gateway, task)
	if len(sources) == 0 {
		return prompt
	}

	var sb strings.Builder
	sb.WriteString("\n\n---\n\n## 宿主提供的目标产品身份候选\n")
	sb.WriteString("以下条目由产品方/管理员按需配置，只用于产品身份消歧；它们不是页面内容已经核验的声明，也不是完成画像的必要条件。命中目标产品时可优先打开这些精确 URL，不要重新猜官网、域名、GitHub 账号或路径。\n")
	for _, source := range sources {
		sb.WriteString(fmt.Sprintf("- 产品：%s\n", source.Name))
		if len(source.Aliases) > 0 {
			sb.WriteString(fmt.Sprintf("  - 别名：%s\n", strings.Join(source.Aliases, "、")))
		}
		for _, officialURL := range source.OfficialURLs {
			sb.WriteString(fmt.Sprintf("  - 官方候选 URL：%s\n", officialURL))
		}
		if strings.TrimSpace(source.Note) != "" {
			sb.WriteString(fmt.Sprintf("  - 说明：%s\n", strings.TrimSpace(source.Note)))
		}
	}
	sb.WriteString("可先用 web_fetch 打开候选 URL；直接抓取失败时可用 web_navigate + web_page 浏览页面。只有冲突会实质改变产品定位、ICP 或检索方向且无法通过其他证据消歧时，才触发门禁。\n")
	return prompt + sb.String()
}

func leadResearchTask(exec *PipelineExecution) string {
	if exec == nil {
		return ""
	}
	if exec.RuntimePipeline != nil && strings.TrimSpace(exec.RuntimePipeline.AIEmployeeOriginalTask) != "" {
		return strings.TrimSpace(exec.RuntimePipeline.AIEmployeeOriginalTask)
	}
	return strings.TrimSpace(exec.Input)
}

func leadResearchDirectURLAllowed(gateway *GatewayService, exec *PipelineExecution, rawURL string) bool {
	normalized := routing.NormalizeLeadResearchURL(rawURL)
	if exec == nil || normalized == "" {
		return false
	}
	task := leadResearchTask(exec)
	if routing.LeadResearchTextContainsURL(task, normalized) {
		return true
	}
	for _, source := range leadResearchProductSourcesForTask(gateway, task) {
		for _, officialURL := range source.OfficialURLs {
			if routing.NormalizeLeadResearchURL(officialURL) == normalized {
				return true
			}
		}
	}

	exec.mu.Lock()
	defer exec.mu.Unlock()
	for _, step := range exec.Steps {
		for _, call := range step.ToolCalls {
			if !strings.EqualFold(strings.TrimSpace(call.ToolName), "web_search") || pipelineToolCallRecordFailed(call) {
				continue
			}
			if routing.LeadResearchTextContainsURL(call.Result, normalized) {
				return true
			}
		}
	}
	return false
}

func guardLeadResearchEvidenceTool(tool Tool, execution *PipelineExecution, stepIndex int, gateways ...*GatewayService) Tool {
	if tool == nil || execution == nil || !strings.EqualFold(strings.TrimSpace(execution.PipelineName), "lead-research") {
		return tool
	}
	if execution.RuntimePipeline == nil || stepIndex < 0 || stepIndex >= len(execution.RuntimePipeline.Steps) {
		return tool
	}
	if strings.TrimSpace(execution.RuntimePipeline.Steps[stepIndex].Name) != "产品画像与SWOT" {
		return tool
	}
	switch strings.ToLower(strings.TrimSpace(tool.Name())) {
	case "read_file", "list_dir", "glob", "web_fetch", "web_navigate":
		var gateway *GatewayService
		if len(gateways) > 0 {
			gateway = gateways[0]
		}
		return businesstools.NewLeadResearchEvidenceGuard(tool, businesstools.LeadResearchEvidenceOptions{
			DirectURLAllowed: func(url string) bool {
				return leadResearchDirectURLAllowed(gateway, execution, url)
			},
			CurrentOutputAllowed: func(path string) bool {
				return leadResearchCurrentUpstreamOutput(execution, stepIndex, path)
			},
			SensitivePath: sensitiveReadPath,
		})
	default:
		return tool
	}
}

func leadResearchCurrentUpstreamOutput(execution *PipelineExecution, stepIndex int, pathValue string) bool {
	if execution == nil || stepIndex <= 0 || strings.TrimSpace(pathValue) == "" {
		return false
	}
	candidate := filepath.Clean(strings.TrimSpace(pathValue))
	if filepath.IsAbs(candidate) {
		workspace := strings.TrimSpace(execution.ResolvedWorkspace)
		if workspace == "" {
			return false
		}
		relative, err := filepath.Rel(workspace, candidate)
		if err != nil || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || relative == ".." {
			return false
		}
		candidate = relative
	}
	candidate = strings.ToLower(filepath.ToSlash(filepath.Clean(candidate)))
	limit := stepIndex
	if limit > len(execution.Steps) {
		limit = len(execution.Steps)
	}
	for index := 0; index < limit; index++ {
		step := execution.Steps[index]
		if strings.TrimSpace(step.Status) != "completed" || strings.TrimSpace(step.Output) == "" {
			continue
		}
		expected := strings.ToLower(filepath.ToSlash(filepath.Clean(buildStepOutputPath(execution, index, step.Name))))
		if candidate == expected {
			return true
		}
	}
	return false
}
