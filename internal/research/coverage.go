package research

import (
	"context"
	"fmt"
	"mac/internal/agentloop"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	securitycore "mac/internal/security"
	"mac/internal/toolcontract"
	networktools "mac/internal/tools/network"
)

var (
	isoDatePattern                = regexp.MustCompile(`\b(20[0-9]{2})-([01]?[0-9])-([0-3]?[0-9])\b`)
	futureDaysPattern             = regexp.MustCompile(`(?i)(?:未来|接下来|往后|future|next)\s*([0-9]{1,2})\s*(?:天|日|days?)`)
	slashDatePattern              = regexp.MustCompile(`\b([01]?[0-9])/([0-3]?[0-9])\b`)
	chineseDatePattern            = regexp.MustCompile(`([01]?[0-9])月([0-3]?[0-9])日`)
	chineseDayOnly                = regexp.MustCompile(`(?:^|[^0-9])([0-3]?[0-9])日`)
	timeAfterDatePattern          = regexp.MustCompile(`^(?:[0-2]?[0-9]\s*:\s*[0-5][0-9]|[0-2]?[0-9]\s*[-~至]\s*[0-2]?[0-9]\s*:)`)
	temperatureValue              = regexp.MustCompile(`-?[0-9]{1,3}(?:\.[0-9]+)?\s*(?:℃|°C|°F|°)`)
	temperaturePair               = regexp.MustCompile(`-?[0-9]{1,3}(?:\.[0-9]+)?\s*(?:℃|°C|°F|°)?\s*[/~～]\s*-?[0-9]{1,3}(?:\.[0-9]+)?\s*(?:℃|°C|°F|°)`)
	retrievalStatusToken          = regexp.MustCompile(`(?i)^\[RETRIEVAL_STATUS:\s*(COMPLETE|PARTIAL|MISMATCH|BLOCKED)[^\]]*\]`)
	searchSourceCountAfterNumber  = regexp.MustCompile(`(?i)([0-9]{1,2}|[一二两三四五六七八九十]{1,3})\s*(?:个|家|种|条|份)?\s*(?:(?:相互|彼此|独立|不同|权威|可靠|可信|原始|官方|结构化|网页|数据|公开|气象|primary|independent|authoritative|reliable|structured|web|data)[、，,\s和与及]*(?:的)?\s*){0,6}(?:来源|信源|出处|source(?:s)?|reference(?:s)?)`)
	searchSourceCountBeforeNumber = regexp.MustCompile(`(?i)(?:来源|信源|出处|source(?:s)?|reference(?:s)?)\s*(?:数量|数|count)?\s*(?:至少|不少于|at\s+least|minimum(?:\s+of)?)?\s*[:：]?\s*([0-9]{1,2}|[一二两三四五六七八九十]{1,3})`)
)

type expectedResearchDate struct {
	monthDay string
	dayOnly  string
}

func expectedResearchDates(input string) []expectedResearchDate {
	dateMatch := isoDatePattern.FindStringSubmatch(input)
	windowMatch := futureDaysPattern.FindStringSubmatch(input)
	if len(dateMatch) < 4 || len(windowMatch) < 2 {
		return nil
	}
	year, _ := strconv.Atoi(dateMatch[1])
	month, _ := strconv.Atoi(dateMatch[2])
	day, _ := strconv.Atoi(dateMatch[3])
	days, _ := strconv.Atoi(windowMatch[1])
	if days < 2 || days > 31 {
		return nil
	}
	start := time.Date(year, time.Month(month), day, 0, 0, 0, 0, time.Local)
	if start.Year() != year || int(start.Month()) != month || start.Day() != day {
		return nil
	}
	result := make([]expectedResearchDate, 0, days)
	for i := 0; i < days; i++ {
		date := start.AddDate(0, 0, i)
		result = append(result, expectedResearchDate{
			monthDay: fmt.Sprintf("%02d-%02d", date.Month(), date.Day()),
			dayOnly:  fmt.Sprintf("day-%02d", date.Day()),
		})
	}
	return result
}

func evidenceDateKeysForContent(content string) []string {
	seen := make(map[string]bool)
	add := func(month, day int) {
		if month >= 1 && month <= 12 && day >= 1 && day <= 31 {
			seen[fmt.Sprintf("%02d-%02d", month, day)] = true
			seen[fmt.Sprintf("day-%02d", day)] = true
		}
	}
	for _, match := range isoDatePattern.FindAllStringSubmatch(content, -1) {
		month, _ := strconv.Atoi(match[2])
		day, _ := strconv.Atoi(match[3])
		add(month, day)
	}
	for _, match := range slashDatePattern.FindAllStringSubmatch(content, -1) {
		month, _ := strconv.Atoi(match[1])
		day, _ := strconv.Atoi(match[2])
		add(month, day)
	}
	for _, match := range chineseDatePattern.FindAllStringSubmatch(content, -1) {
		month, _ := strconv.Atoi(match[1])
		day, _ := strconv.Atoi(match[2])
		add(month, day)
	}
	for _, match := range chineseDayOnly.FindAllStringSubmatch(content, -1) {
		day, _ := strconv.Atoi(match[1])
		if day >= 1 && day <= 31 {
			seen[fmt.Sprintf("day-%02d", day)] = true
		}
	}
	result := make([]string, 0, len(seen))
	for key := range seen {
		result = append(result, key)
	}
	sort.Strings(result)
	return result
}

func EvidenceDateKeysForToolResult(call toolcontract.Call, content string) []string {
	if !securitycore.IsExternalEvidenceToolCall(call) {
		return nil
	}
	return evidenceDateKeysForContent(content)
}

type datedContentOccurrence struct {
	start int
	end   int
	keys  []string
}

func evidenceDailyDateKeysForContent(content string) []string {
	var occurrences []datedContentOccurrence
	addOccurrence := func(start, end int, month, day int, withMonth bool) {
		if day < 1 || day > 31 {
			return
		}
		keys := []string{fmt.Sprintf("day-%02d", day)}
		if withMonth && month >= 1 && month <= 12 {
			keys = append(keys, fmt.Sprintf("%02d-%02d", month, day))
		}
		occurrences = append(occurrences, datedContentOccurrence{start: start, end: end, keys: keys})
	}
	for _, index := range isoDatePattern.FindAllStringSubmatchIndex(content, -1) {
		month, _ := strconv.Atoi(content[index[4]:index[5]])
		day, _ := strconv.Atoi(content[index[6]:index[7]])
		addOccurrence(index[0], index[1], month, day, true)
	}
	for _, index := range slashDatePattern.FindAllStringSubmatchIndex(content, -1) {
		month, _ := strconv.Atoi(content[index[2]:index[3]])
		day, _ := strconv.Atoi(content[index[4]:index[5]])
		addOccurrence(index[0], index[1], month, day, true)
	}
	for _, index := range chineseDatePattern.FindAllStringSubmatchIndex(content, -1) {
		month, _ := strconv.Atoi(content[index[2]:index[3]])
		day, _ := strconv.Atoi(content[index[4]:index[5]])
		addOccurrence(index[0], index[1], month, day, true)
	}
	for _, index := range chineseDayOnly.FindAllStringSubmatchIndex(content, -1) {
		dayStart, dayEnd := index[2], index[3]
		overlapped := false
		for _, existing := range occurrences {
			if dayStart >= existing.start && dayEnd <= existing.end {
				overlapped = true
				break
			}
		}
		if !overlapped {
			day, _ := strconv.Atoi(content[dayStart:dayEnd])
			addOccurrence(index[0], index[1], 0, day, false)
		}
	}
	sort.Slice(occurrences, func(i, j int) bool {
		if occurrences[i].start == occurrences[j].start {
			return occurrences[i].end < occurrences[j].end
		}
		return occurrences[i].start < occurrences[j].start
	})

	seen := make(map[string]bool)
	for i, occurrence := range occurrences {
		afterDate := strings.TrimSpace(content[occurrence.end:min(len(content), occurrence.end+20)])
		if timeAfterDatePattern.MatchString(afterDate) {
			continue
		}
		segmentEnd := len(content)
		if i+1 < len(occurrences) {
			segmentEnd = occurrences[i+1].start
		}
		segment := content[occurrence.end:segmentEnd]
		hasTemperaturePair := len(temperatureValue.FindAllString(segment, 3)) >= 2 || temperaturePair.MatchString(segment)
		if !hasTemperaturePair {
			continue
		}
		for _, key := range occurrence.keys {
			seen[key] = true
		}
	}
	result := make([]string, 0, len(seen))
	for key := range seen {
		result = append(result, key)
	}
	sort.Strings(result)
	return result
}

func EvidenceDailyDateKeysForToolResult(call toolcontract.Call, content string) []string {
	if !securitycore.IsExternalEvidenceToolCall(call) {
		return nil
	}
	return evidenceDailyDateKeysForContent(content)
}

func countExpectedDateCoverage(expected []expectedResearchDate, evidenceKeys []string) int {
	keys := make(map[string]bool, len(evidenceKeys))
	for _, key := range evidenceKeys {
		keys[key] = true
	}
	covered := 0
	for _, date := range expected {
		if keys[date.monthDay] || keys[date.dayOnly] {
			covered++
		}
	}
	return covered
}

func AnnotateTimeSeriesRetrievalFit(ctx context.Context, content string) string {
	if ctx == nil {
		return content
	}
	expected := expectedResearchDates(networktools.RetrievalGoal(ctx))
	if len(expected) == 0 {
		return content
	}
	content = strings.TrimSpace(retrievalStatusToken.ReplaceAllString(strings.TrimSpace(content), ""))
	coverage := countExpectedDateCoverage(expected, evidenceDailyDateKeysForContent(content))
	status := "PARTIAL"
	if coverage >= len(expected) {
		status = "COMPLETE"
	}
	return fmt.Sprintf("[RETRIEVAL_STATUS: %s date_coverage=%d/%d]\n\n%s", status, coverage, len(expected), content)
}

func SourceURLForToolCall(call toolcontract.Call) string {
	if strings.EqualFold(strings.TrimSpace(call.Name), "agent_reach") {
		action := strings.ToLower(strings.TrimSpace(firstStringArg(call.Arguments, "action")))
		if action != "read" && action != "transcribe" && action != "trending" {
			return ""
		}
		target := strings.TrimSpace(firstStringArg(call.Arguments, "target", "url"))
		if !networktools.IsHTTPURL(target) {
			return ""
		}
		return target
	}
	if strings.EqualFold(strings.TrimSpace(call.Name), "social_trending") {
		source, _ := call.Arguments["source"].(string)
		if strings.EqualFold(strings.TrimSpace(source), "rss") {
			raw, _ := call.Arguments["url"].(string)
			return strings.TrimSpace(raw)
		}
		return ""
	}
	if strings.EqualFold(strings.TrimSpace(call.Name), "weather_forecast") {
		provider, _ := call.Arguments["provider"].(string)
		switch strings.ToLower(strings.TrimSpace(provider)) {
		case networktools.OpenMeteoProvider:
			return "https://api.open-meteo.com/v1/forecast"
		case networktools.SevenTimerProvider:
			return "https://www.7timer.info/bin/api.pl"
		}
		return ""
	}
	if strings.EqualFold(strings.TrimSpace(call.Name), "fuel_price") {
		provider, _ := call.Arguments["provider"].(string)
		switch strings.ToLower(strings.TrimSpace(provider)) {
		case "regional_official":
			return "https://fgw.sh.gov.cn/fgw_jggl/index.html"
		case "nxvav":
			return "https://api.nxvav.cn/api/fuel-price/"
		case "tuanyou":
			return "https://www.tuanyou.net/youjia/"
		}
		return ""
	}
	if strings.EqualFold(strings.TrimSpace(call.Name), "stock_facts") {
		provider, _ := call.Arguments["provider"].(string)
		symbol, _ := call.Arguments["symbol"].(string)
		if strings.TrimSpace(symbol) == "" {
			symbol, _ = call.Arguments["ticker"].(string)
		}
		symbol = strings.TrimSpace(symbol)
		prefix, secid := "sz", "0."
		routed := false
		if normalized, exchangeHint, err := networktools.NormalizeStockFactsSymbol(symbol); err == nil {
			symbol = normalized
			if _, explicitPrefix, explicitSecID, _, _, exchangeErr := networktools.StockFactsExchange(symbol, exchangeHint); exchangeErr == nil {
				prefix, secid = explicitPrefix, strings.TrimSuffix(explicitSecID, symbol)
				routed = true
			}
		}
		if !routed {
			switch {
			case strings.HasPrefix(symbol, "5"), strings.HasPrefix(symbol, "6"):
				prefix = "sh"
				secid = "1."
			case strings.HasPrefix(symbol, "4"), strings.HasPrefix(symbol, "8"), strings.HasPrefix(symbol, "9"):
				prefix = "bj"
			}
		}
		switch strings.ToLower(strings.TrimSpace(provider)) {
		case "tencent_market":
			return fmt.Sprintf("https://web.ifzq.gtimg.cn/appstock/app/fqkline/get?param=%s%s,day,,,80,none", prefix, symbol)
		case "eastmoney_market":
			return fmt.Sprintf("https://push2delay.eastmoney.com/api/qt/stock/get?secid=%s%s", secid, symbol)
		case "official_report":
			return fmt.Sprintf("https://www.cninfo.com.cn/new/disclosure/stock?stockCode=%s", symbol)
		}
		return ""
	}
	if !securitycore.IsExternalEvidenceSearchToolName(call.Name) || securitycore.ExternalEvidenceSearchMode(call) != "direct" {
		return ""
	}
	raw, _ := call.Arguments["url"].(string)
	return strings.TrimSpace(raw)
}

func SourceHost(rawURL string) string {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return ""
	}
	return strings.TrimPrefix(strings.ToLower(parsed.Hostname()), "www.")
}

func sourceProvenanceKey(rawURL string) string {
	host := SourceHost(rawURL)
	switch {
	case host == "weather.com.cn", host == "nmc.cn", host == "weather.cma.cn", strings.HasSuffix(host, ".cma.cn"):
		return "cma-official-network"
	default:
		return host
	}
}

type researchCoverageAssessment struct {
	RequestedSources   int
	ExpectedDates      int
	SubstantialSources int
	FullSources        int
}

func assessResearchCoverage(input string, toolResults []string) researchCoverageAssessment {
	expected := expectedResearchDates(input)
	requested := RequestedSourceCount(input)
	assessment := researchCoverageAssessment{RequestedSources: requested, ExpectedDates: len(expected)}
	if requested < 2 || len(expected) < 2 {
		return assessment
	}
	threshold := max(2, len(expected)-2)
	bestByProvenance := make(map[string]int)
	for _, result := range toolResults {
		record, ok := toolcontract.ParseRecord(result)
		if !ok || !record.Succeeded || record.SearchMode != "direct" || record.SourceURL == "" {
			continue
		}
		provenance := sourceProvenanceKey(record.SourceURL)
		if provenance == "" {
			continue
		}
		coverage := countExpectedDateCoverage(expected, record.EvidenceDailyDateKeys)
		if coverage > bestByProvenance[provenance] {
			bestByProvenance[provenance] = coverage
		}
	}
	for _, coverage := range bestByProvenance {
		if coverage >= threshold {
			assessment.SubstantialSources++
		}
		if coverage >= len(expected) {
			assessment.FullSources++
		}
	}
	return assessment
}

func ResearchCoverageIssues(input string, toolResults []string) []string {
	assessment := assessResearchCoverage(input, toolResults)
	if assessment.RequestedSources < 2 || assessment.ExpectedDates < 2 {
		return nil
	}
	var issues []string
	if assessment.SubstantialSources < assessment.RequestedSources {
		issues = append(issues, fmt.Sprintf("only %d distinct direct source(s) substantially cover the requested %d-date window; %d requested", assessment.SubstantialSources, assessment.ExpectedDates, assessment.RequestedSources))
	}
	if assessment.FullSources < 1 {
		issues = append(issues, fmt.Sprintf("no direct source covers all %d requested dates", assessment.ExpectedDates))
	}
	return issues
}

func timeSeriesCoverageSatisfied(input string, toolResults []string) bool {
	assessment := assessResearchCoverage(input, toolResults)
	return assessment.RequestedSources >= 2 && assessment.ExpectedDates >= 2 &&
		assessment.SubstantialSources >= assessment.RequestedSources && assessment.FullSources >= 1
}

func BuildResearchCoverageRepairPrompt(issues []string) map[string]interface{} {
	return agentloop.NewInternalControlMessage("SOURCE COVERAGE REPAIR", fmt.Sprintf(`[SOURCE COVERAGE INCOMPLETE]
The current evidence does not support the requested cross-check: %s.

Do not finalize or call these sources "independent confirmation" yet. Without creating a plan, notepad, or broad search, make at most one direct-URL read-only fetch to a distinct source that actually contains the requested target, dates, and fields. A current-date page, climate overview, search snippet, blocked page, or source with no overlapping detailed rows does not count. If no suitable direct source remains within budget, return an honest coverage gap instead of pretending the requirement was met.`, strings.Join(issues, "; ")))
}

func RequestedSourceCount(input string) int {
	best := 0
	for _, pattern := range []*regexp.Regexp{searchSourceCountAfterNumber, searchSourceCountBeforeNumber} {
		for _, match := range pattern.FindAllStringSubmatch(input, -1) {
			if len(match) < 2 {
				continue
			}
			if value := parseRequestedCount(match[1]); value > best {
				best = value
			}
		}
	}
	return best
}

func parseRequestedCount(raw string) int {
	raw = strings.TrimSpace(raw)
	if value, err := strconv.Atoi(raw); err == nil {
		return value
	}
	digits := map[rune]int{'一': 1, '二': 2, '两': 2, '三': 3, '四': 4, '五': 5, '六': 6, '七': 7, '八': 8, '九': 9}
	runes := []rune(raw)
	if len(runes) == 1 {
		return digits[runes[0]]
	}
	for i, r := range runes {
		if r != '十' {
			continue
		}
		tens := 1
		if i > 0 {
			tens = digits[runes[i-1]]
		}
		ones := 0
		if i+1 < len(runes) {
			ones = digits[runes[i+1]]
		}
		return tens*10 + ones
	}
	return 0
}

func firstStringArg(arguments map[string]interface{}, keys ...string) string {
	for _, key := range keys {
		if value, ok := arguments[key].(string); ok && strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
