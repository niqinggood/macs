package research

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	securitycore "mac/internal/security"
	"mac/internal/toolcontract"
)

type currentNewsVenueRule struct {
	pattern *regexp.Regexp
	neutral string
}

var (
	currentNewsISODatePattern    = regexp.MustCompile(`(?i)\b(20\d{2})[-/.年](\d{1,2})[-/.月](\d{1,2})(?:日)?\b`)
	currentNewsRSSItemPattern    = regexp.MustCompile(`(?m)^###\s+[1-9][0-9]*[.、]\s*`)
	currentNewsSearchItemPattern = regexp.MustCompile(`(?m)^[1-9][0-9]*[.、]\s+\S`)
	currentNewsPublishedLine     = regexp.MustCompile(`(?mi)^\s*(?:📅\s*(?:Published\s*[:：]\s*)?|Published\s*[:：]\s*)(.+?)\s*$`)
	currentNewsChineseMonthTime  = regexp.MustCompile(`(?i)(\d{1,2})\s+(\d{1,2})月\s+(20\d{2})\s+(\d{1,2}):(\d{2})(?::(\d{2}))?\s*([A-Z]{2,5}|[+-]\d{4})?`)
	currentNewsTrailingZone      = regexp.MustCompile(`(?i)\s+(UTC|GMT|EDT|EST|CDT|CST|MDT|MST|PDT|PST)\s*$`)
	currentNewsRoundupTitle      = regexp.MustCompile(`(?i)(?:[0-9一二三四五六七八九十]{1,3}\s*点\s*1\s*氪|氪星晚报|(?:新闻|科技|商业|财经)?\s*(?:早报|晚报)|morning\s+brief|evening\s+brief|daily\s+(?:roundup|digest))`)
	currentNewsIdentityToken     = regexp.MustCompile(`[A-Za-z][A-Za-z0-9]{1,}`)
	currentNewsQuotedWording     = []*regexp.Regexp{
		regexp.MustCompile(`"([^"\r\n]{1,120})"`),
		regexp.MustCompile(`“([^”\r\n]{1,120})”`),
		regexp.MustCompile(`「([^」\r\n]{1,120})」`),
		regexp.MustCompile(`『([^』\r\n]{1,120})』`),
	}
	currentNewsQuotedExpansion        = regexp.MustCompile(`["”」』]\s*([（(][^）)\r\n]{3,60}[）)])`)
	currentNewsAcronym                = regexp.MustCompile(`\b[A-Z][A-Z0-9]{1,}(?:-[A-Z0-9]+)?\b`)
	currentNewsRemoteCodeClaim        = regexp.MustCompile(`(?:可(?:导致)?\s*)?远程(?:执行)?任意代码|(?:可(?:导致)?\s*)?远程代码执行`)
	currentNewsArbitraryCodeFact      = regexp.MustCompile(`(?i)(?:任意代码执行|arbitrary\s+code\s+execution)`)
	currentNewsRemoteCodeFact         = regexp.MustCompile(`(?i)(?:远程|remote)`)
	currentNewsStrongInference        = regexp.MustCompile(`(?:重塑|锚定|必然|势必|证明(?:了)?|验证(?:了|成功)?|已经验证|直接拉动|确定将)`)
	currentNewsHistoricalFirst        = regexp.MustCompile(`(?:首次|首个|第一次)`)
	currentNewsHistoricalFirstFact    = regexp.MustCompile(`(?i)(?:首次|首个|第一次|\bfirst\b)`)
	currentNewsVolumeFlatFact         = regexp.MustCompile(`(?i)(?:(?:销售量|销量|出货量|shipments?|sales\s+volume|volume)[^，。；;\n]{0,24}(?:持平|相同|flat|unchanged)|(?:持平|相同|flat|unchanged)[^，。；;\n]{0,24}(?:销售量|销量|出货量|shipments?|sales\s+volume|volume))`)
	currentNewsVolumeGrowthClaim      = regexp.MustCompile(`(?:量价齐升|量增|(?:销售量|销量|出货量)[^，。；;\n]{0,12}(?:增长|上升|增加))`)
	currentNewsLegalImportance        = regexp.MustCompile(`(?i)(?:起诉|诉讼|法院|司法|监管|禁令|罚款|lawsuit|sues?|court|regulat)`)
	currentNewsSecurityImportance     = regexp.MustCompile(`(?i)(?:漏洞|攻击|安全|泄露|隐私|vulnerab|security|attack|breach)`)
	currentNewsFinanceImportance      = regexp.MustCompile(`(?i)(?:融资|募资|上市|发行|承销|财报|营收|毛利|售价|价格|销量|利润|估值|\bIPO\b|funding|raises?|revenue|margin|price|sales)`)
	currentNewsTechnologyImportance   = regexp.MustCompile(`(?i)(?:火箭|回收|发射|研究|技术|工程|芯片|系统|产品|功能|发布|推出|关闭|\bAI\b|rocket|research|technology|launch|product)`)
	currentNewsUnderwritingImportance = regexp.MustCompile(`(?i)(?:承销|保荐|招股|上市申请|underwrit)`)
	currentNewsListingImportance      = regexp.MustCompile(`(?i)(?:募资|上市|发行价|开盘价|交易价|估值|\bIPO\b|listing|listed|raises?)`)
	currentNewsRocketImportance       = regexp.MustCompile(`(?i)(?:火箭|助推器|发射|轨道级|可重复使用|rocket|booster|orbital)`)
	currentNewsCultureImportance      = regexp.MustCompile(`(?i)(?:文物|文保|壁画|古壁|数字化重现|文化遗产|heritage|mural)`)
	currentNewsEmergencyImportance    = regexp.MustCompile(`(?i)(?:\bAED\b|急救|救护|应急|急救圈|emergency)`)
	currentNewsResearchImportance     = regexp.MustCompile(`(?i)(?:研究|论文|科学|实验|发现|机制|定义|study|research|paper|science)`)
	currentNewsDiscardedNoteLead      = regexp.MustCompile(`(?i)^\s*>?\s*(?:[-*]\s*)?(?:备注|说明|注|候选(?:取舍)?|未选(?:候选)?)[：:]`)
	currentNewsDiscardedNoteCue       = regexp.MustCompile(`(?:未单列|未列入|未纳入|未收录|未选入|不单列|不列入|没有单列|不作为(?:独立)?条目)`)
	currentNewsEventDateCue           = regexp.MustCompile(`(?i)(?:\b20\d{2}[-/.]\d{1,2}[-/.]\d{1,2}\b|20\d{2}\s*年|\d{1,2}\s*月\s*\d{1,2}\s*日|今天|今日|昨日|前日|\btoday\b|\byesterday\b|\b(?:monday|tuesday|wednesday|thursday|friday|saturday|sunday)\b|星期[一二三四五六日天]|周[一二三四五六日天])`)
	currentNewsPreviousDayCue         = regexp.MustCompile(`(?i)(?:昨日|昨天|昨晚|前一日|前一天|\byesterday\b|\blast\s+night\b|\bprevious\s+day\b)`)
	currentNewsPublisherDateLead      = regexp.MustCompile(`(?i)^\s*IT之家\s*\d{1,2}\s*月\s*\d{1,2}\s*日\s*消息\s*[,，:：]?\s*`)
	currentNewsFreshnessToolNames     = map[string]bool{
		"agent_reach":     true,
		"social_trending": true,
		"web_search":      true,
		"web_fetch":       true,
	}
	currentNewsIdentityStopWords = map[string]bool{
		"about": true, "after": true, "article": true, "business": true, "com": true,
		"from": true, "http": true, "https": true, "media": true, "news": true,
		"published": true, "source": true, "tech": true, "technology": true, "the": true,
		"this": true, "today": true, "with": true, "www": true, "ai": true,
		"techcrunch": true, "theverge": true, "arstechnica": true, "ithome": true,
	}
	currentNewsVenueRules = []currentNewsVenueRule{
		{pattern: regexp.MustCompile(`(?i)纽交所|纽约证券交易所|\bNYSE\b`), neutral: "美国资本市场"},
		{pattern: regexp.MustCompile(`(?i)纳斯达克(?:证券交易所)?|\bNASDAQ\b`), neutral: "美国资本市场"},
	}
)

var currentNewsShanghaiLocation = time.FixedZone("Asia/Shanghai", 8*60*60)

// RequestedCurrentNewsLocalDate resolves the date that "today" means for a
// current-news request. Explicit user dates win; otherwise a literal today/今日
// request uses the current Asia/Shanghai date.
func RequestedCurrentNewsLocalDate(input string) (time.Time, bool) {
	if _, ok := CurrentNewsDeliveryContract(input); !ok {
		return time.Time{}, false
	}
	if match := currentNewsISODatePattern.FindStringSubmatch(input); len(match) >= 4 {
		year, _ := strconv.Atoi(match[1])
		month, _ := strconv.Atoi(match[2])
		day, _ := strconv.Atoi(match[3])
		candidate := time.Date(year, time.Month(month), day, 0, 0, 0, 0, currentNewsShanghaiLocation)
		if candidate.Year() == year && int(candidate.Month()) == month && candidate.Day() == day {
			return candidate, true
		}
	}
	if containsAnyFold(input, "今天", "今日", "本日", "today") {
		now := time.Now().In(currentNewsShanghaiLocation)
		return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, currentNewsShanghaiLocation), true
	}
	return time.Time{}, false
}

func SameCurrentNewsLocalDate(value, target time.Time) bool {
	local := value.In(currentNewsShanghaiLocation)
	return local.Year() == target.Year() && local.Month() == target.Month() && local.Day() == target.Day()
}

// filterCurrentNewsEvidence removes explicitly dated RSS/search rows that are
// outside the requested Asia/Shanghai day before they reach the model. The raw
// network response may contain a rolling 24-hour feed; handing all of it to the
// model both wastes context and makes yesterday's stories look eligible.
func filterCurrentNewsEvidence(input, toolName, content string) (string, int) {
	target, ok := RequestedCurrentNewsLocalDate(input)
	if !ok || !currentNewsFreshnessToolNames[strings.ToLower(strings.TrimSpace(toolName))] || strings.TrimSpace(content) == "" {
		return content, 0
	}

	itemPattern := currentNewsRSSItemPattern
	indexes := itemPattern.FindAllStringIndex(content, -1)
	if len(indexes) == 0 {
		itemPattern = currentNewsSearchItemPattern
		indexes = itemPattern.FindAllStringIndex(content, -1)
	}
	if len(indexes) == 0 {
		return content, 0
	}

	header := strings.TrimSpace(content[:indexes[0][0]])
	kept := make([]string, 0, len(indexes))
	removed := 0
	trimmedRoundups := 0
	for index, bounds := range indexes {
		end := len(content)
		if index+1 < len(indexes) {
			end = indexes[index+1][0]
		}
		block := strings.TrimSpace(content[bounds[0]:end])
		published, parsed := currentNewsPublishedTime(block)
		if parsed && SameCurrentNewsLocalDate(published, target) {
			if currentNewsRoundupContent(block) {
				block = trimCurrentNewsRoundupContent(block, target)
				trimmedRoundups++
			}
			kept = append(kept, block)
			continue
		}
		// Listing rows without an explicit publication timestamp cannot prove a
		// same-day brief. Direct article fetches do not use this numbered-row path.
		removed++
	}
	if removed == 0 && trimmedRoundups == 0 {
		return content, 0
	}

	parts := make([]string, 0, len(kept)+2)
	if header != "" {
		parts = append(parts, header)
	}
	parts = append(parts, kept...)
	if removed > 0 {
		parts = append(parts, fmt.Sprintf("[CURRENT_NEWS_FRESHNESS_FILTER] Removed %d row(s) outside Asia/Shanghai date %s.", removed, target.Format("2006-01-02")))
	}
	if trimmedRoundups > 0 {
		parts = append(parts, fmt.Sprintf("[CURRENT_NEWS_ROUNDUP_FILTER] Restricted %d digest/roundup row(s) to their explicit RSS title, URL, and publication time; nested body bullets are not eligible events.", trimmedRoundups))
	}
	return strings.TrimSpace(strings.Join(parts, "\n\n")), removed + trimmedRoundups
}

// filterCurrentNewsToolCallEvidence limits row filtering to discovery/listing
// calls. A direct web_fetch article may legitimately contain numbered prose;
// treating that prose as search rows would destroy useful evidence.
func FilterCurrentNewsToolCallEvidence(input string, call toolcontract.Call, content string) (string, int) {
	name := strings.ToLower(strings.TrimSpace(call.Name))
	isListing := name == "social_trending" || securitycore.ExternalEvidenceSearchMode(call) == "discovery"
	if name == "agent_reach" && strings.EqualFold(strings.TrimSpace(firstStringArg(call.Arguments, "action")), "trending") {
		isListing = true
	}
	if !isListing {
		if target, ok := RequestedCurrentNewsLocalDate(input); ok && currentNewsRoundupContent(content) {
			return trimCurrentNewsRoundupContent(content, target), 1
		}
		return content, 0
	}
	return filterCurrentNewsEvidence(input, name, content)
}

func currentNewsRoundupContent(content string) bool {
	nonEmpty := 0
	for _, line := range strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		nonEmpty++
		if currentNewsRoundupTitle.MatchString(line) {
			return true
		}
		if nonEmpty >= 4 {
			break
		}
	}
	return false
}

func trimCurrentNewsRoundupContent(content string, target time.Time) string {
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	kept := make([]string, 0, 5)
	seenTitle, seenURL := false, false
	for _, rawLine := range lines {
		line := strings.TrimSpace(rawLine)
		if line == "" {
			continue
		}
		if currentNewsRoundupTitle.MatchString(line) {
			kept = append(kept, line)
			seenTitle = true
			continue
		}
		if !seenTitle {
			continue
		}
		if !seenURL && (strings.Contains(line, "Source:") || strings.Contains(line, "Source：") || securitycore.ExternalURLPattern().MatchString(line)) {
			kept = append(kept, line)
			seenURL = true
			continue
		}
		if currentNewsPublishedLine.MatchString(line) {
			kept = append(kept, line)
			break
		}
		if seenURL {
			break
		}
	}
	kept = append(kept, fmt.Sprintf("[CURRENT_NEWS_ROUNDUP_FILTER] Only events literally named in this digest title are eligible for %s; body sections and bullet lists were intentionally omitted.", target.Format("2006-01-02")))
	return strings.TrimSpace(strings.Join(kept, "\n"))
}

func currentNewsPublishedTime(text string) (time.Time, bool) {
	match := currentNewsPublishedLine.FindStringSubmatch(text)
	if len(match) < 2 {
		return time.Time{}, false
	}
	return ParseCurrentNewsPublishedTime(match[1])
}

func ParseCurrentNewsPublishedTime(raw string) (time.Time, bool) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return time.Time{}, false
	}
	if match := currentNewsChineseMonthTime.FindStringSubmatch(value); len(match) >= 7 {
		day, _ := strconv.Atoi(match[1])
		month, _ := strconv.Atoi(match[2])
		year, _ := strconv.Atoi(match[3])
		hour, _ := strconv.Atoi(match[4])
		minute, _ := strconv.Atoi(match[5])
		second := 0
		if match[6] != "" {
			second, _ = strconv.Atoi(match[6])
		}
		zone := strings.TrimSpace(match[7])
		if zone == "" {
			zone = "+0800"
		}
		value = fmt.Sprintf("%04d-%02d-%02d %02d:%02d:%02d %s", year, month, day, hour, minute, second, numericCurrentNewsZone(zone))
	}
	if zone := currentNewsTrailingZone.FindStringSubmatch(value); len(zone) >= 2 {
		value = currentNewsTrailingZone.ReplaceAllString(value, " "+numericCurrentNewsZone(zone[1]))
	}
	value = strings.Join(strings.Fields(value), " ")
	layouts := []string{
		time.RFC3339,
		"Mon, 02 Jan 2006 15:04:05 -0700",
		"Mon, 2 Jan 2006 15:04:05 -0700",
		"2006-01-02 15:04:05 -0700",
		"2006-01-02 15:04 -0700",
		"2006-01-02 15:04:05",
		"2006-01-02 15:04",
	}
	for _, layout := range layouts {
		if parsed, err := time.Parse(layout, value); err == nil {
			if !strings.Contains(layout, "-0700") && layout != time.RFC3339 {
				parsed = time.Date(parsed.Year(), parsed.Month(), parsed.Day(), parsed.Hour(), parsed.Minute(), parsed.Second(), parsed.Nanosecond(), currentNewsShanghaiLocation)
			}
			return parsed, true
		}
	}
	return time.Time{}, false
}

func numericCurrentNewsZone(zone string) string {
	switch strings.ToUpper(strings.TrimSpace(zone)) {
	case "UTC", "GMT":
		return "+0000"
	case "EDT":
		return "-0400"
	case "EST":
		return "-0500"
	case "CDT":
		return "-0500"
	case "CST":
		return "-0600"
	case "MDT":
		return "-0600"
	case "MST":
		return "-0700"
	case "PDT":
		return "-0700"
	case "PST":
		return "-0800"
	default:
		return strings.TrimSpace(zone)
	}
}

// CurrentNewsEvidenceFreshnessIssues cross-checks every cited article against
// publication timestamps that were actually present in this turn's tool
// evidence. This catches a model relabelling an old RSS article as published
// today even when the answer's own timestamp looks syntactically valid.
func CurrentNewsEvidenceFreshnessIssues(input, answer string, toolResults []string) []string {
	target, ok := RequestedCurrentNewsLocalDate(input)
	if !ok || strings.TrimSpace(answer) == "" || len(toolResults) == 0 {
		return nil
	}
	evidenceDates := currentNewsEvidenceDatesByURL(toolResults)
	if len(evidenceDates) == 0 {
		return nil
	}
	sections := numberedResearchItemSections(answer)
	keys := make([]int, 0, len(sections))
	for key := range sections {
		if number := parseRequestedCount(key); number > 0 {
			keys = append(keys, number)
		}
	}
	sort.Ints(keys)
	issues := make([]string, 0)
	for _, number := range keys {
		section := sections[strconv.Itoa(number)]
		urls, _ := newsArticleCitationCoverage(section)
		stale := make([]string, 0, 1)
		for _, articleURL := range urls {
			for _, published := range evidenceDates[CanonicalCitationURLKey(articleURL)] {
				if !SameCurrentNewsLocalDate(published, target) {
					stale = append(stale, articleURL)
					break
				}
			}
		}
		if len(stale) > 0 {
			issues = append(issues, fmt.Sprintf("item %d cites evidence published outside Asia/Shanghai date %s: %s", number, target.Format("2006-01-02"), strings.Join(stale, ", ")))
		}
	}
	return issues
}

// CurrentNewsEvidenceAssociationIssues catches a citation attached to the
// wrong numbered event. URL allowlisting alone proves only that a page appeared
// in the evidence set; it does not prove that an Apple/OpenAI page supports an
// SK Hynix item. The check is deliberately conservative: it rejects a citation
// only when its evidence identity matches another numbered item substantially
// better, so ordinary English-to-Chinese headline translation is not penalized.
func CurrentNewsEvidenceAssociationIssues(input, answer string, toolResults []string) []string {
	if _, ok := CurrentNewsDeliveryContract(input); !ok || strings.TrimSpace(answer) == "" || len(toolResults) == 0 {
		return nil
	}
	evidenceText := currentNewsEvidenceIdentityTextByURL(toolResults)
	if len(evidenceText) == 0 {
		return nil
	}
	sections := numberedResearchItemSections(answer)
	if len(sections) < 2 {
		return nil
	}
	sectionTokens := make(map[int]map[string]bool, len(sections))
	sectionText := make(map[int]string, len(sections))
	keys := make([]int, 0, len(sections))
	for rawNumber, section := range sections {
		number := parseRequestedCount(rawNumber)
		if number <= 0 {
			continue
		}
		keys = append(keys, number)
		withoutURLs := securitycore.ExternalURLPattern().ReplaceAllString(section, " ")
		sectionText[number] = section
		sectionTokens[number] = currentNewsIdentityTokens(withoutURLs)
	}
	sort.Ints(keys)
	issues := make([]string, 0, 1)
	for _, number := range keys {
		urls, _ := newsArticleCitationCoverage(sectionText[number])
		for _, articleURL := range urls {
			identity := evidenceText[CanonicalCitationURLKey(articleURL)]
			if identity == "" {
				continue
			}
			// One digest URL may legitimately support several events literally
			// named in its title. The roundup filter has already frozen that title
			// and removed nested body bullets, so a one-URL/one-item competition
			// would create a false mismatch between two eligible title events.
			if strings.Contains(identity, "[CURRENT_NEWS_ROUNDUP_FILTER]") {
				continue
			}
			evidenceTokens := currentNewsIdentityTokens(identity)
			if len(evidenceTokens) < 2 {
				continue
			}
			currentScore := currentNewsIdentityOverlap(evidenceTokens, sectionTokens[number])
			bestNumber, bestScore := 0, 0
			for _, other := range keys {
				if other == number {
					continue
				}
				if score := currentNewsIdentityOverlap(evidenceTokens, sectionTokens[other]); score > bestScore {
					bestNumber, bestScore = other, score
				}
			}
			if bestNumber > 0 && bestScore >= 2 && bestScore >= currentScore+2 {
				issues = append(issues, fmt.Sprintf("item %d cites an evidence URL whose headline identity matches item %d instead: %s", number, bestNumber, articleURL))
				break
			}
		}
	}
	return issues
}

func CurrentNewsUnsupportedQuotationIssues(input, answer string, toolResults []string) []string {
	replacements := currentNewsUnsupportedWordingReplacements(input, answer, toolResults)
	issues := make([]string, 0, 1)
	seen := make(map[string]bool)
	for _, replacement := range replacements {
		quoted := strings.TrimSpace(replacement.value)
		if quoted == "" || seen[quoted] {
			continue
		}
		seen[quoted] = true
		issues = append(issues, fmt.Sprintf("quoted wording is not verbatim in its cited evidence; paraphrase without quotation marks: %s", quoted))
	}
	return issues
}

type currentNewsWordingReplacement struct {
	start int
	end   int
	value string
}

func NormalizeCurrentNewsUnsupportedQuotations(input, answer string, toolResults []string) (string, int) {
	replacements := currentNewsUnsupportedWordingReplacements(input, answer, toolResults)
	sort.Slice(replacements, func(i, j int) bool { return replacements[i].start > replacements[j].start })
	for _, replacement := range replacements {
		answer = answer[:replacement.start] + replacement.value + answer[replacement.end:]
	}
	return answer, len(replacements)
}

// NormalizeCurrentNewsEvidenceScopedClaims applies cheap, general semantic
// downgrades inside each numbered item's own citation boundary. It deliberately
// handles only high-signal contradictions that are unsafe to leave to a free-
// form rewrite: capability upgrades, unsupported technical acronyms, strong
// causal language, source plurality, and an explicit flat-vs-growth conflict.
func NormalizeCurrentNewsEvidenceScopedClaims(input, answer string, toolResults []string) (string, int) {
	if _, ok := CurrentNewsDeliveryContract(input); !ok || strings.TrimSpace(answer) == "" {
		return answer, 0
	}
	evidenceByURL := currentNewsEvidenceIdentityTextByURL(toolResults)
	type replacement struct {
		start int
		end   int
		value string
	}
	replacements := make([]replacement, 0, 4)
	changed := 0
	matches := researchNumberedItemPattern.FindAllStringSubmatchIndex(answer, -1)
	for index, match := range matches {
		sectionEnd := len(answer)
		if index+1 < len(matches) {
			sectionEnd = matches[index+1][0]
		}
		sectionEnd = researchItemSectionEnd(answer, match[0], match[2], sectionEnd)
		section := answer[match[0]:sectionEnd]
		urls, _ := newsArticleCitationCoverage(section)
		var evidence strings.Builder
		for _, articleURL := range urls {
			evidence.WriteString(evidenceByURL[CanonicalCitationURLKey(articleURL)])
			evidence.WriteByte('\n')
		}
		evidenceText := evidence.String()
		evidenceLower := strings.ToLower(evidenceText)
		for lineStart := 0; lineStart < len(section); {
			lineEnd := strings.IndexByte(section[lineStart:], '\n')
			if lineEnd < 0 {
				lineEnd = len(section)
			} else {
				lineEnd += lineStart
			}
			line := section[lineStart:lineEnd]
			updated := line
			separator := strings.IndexAny(line, ":：")
			isCore, isImportance := false, false
			if separator >= 0 {
				label := line[:separator]
				isCore = newsAnswerContainsRequiredLabel(label, "核心事实")
				isImportance = newsAnswerContainsRequiredLabel(label, "为什么重要")
			}

			if !securitycore.ExternalURLPattern().MatchString(line) && currentNewsRemoteCodeClaim.MatchString(updated) && currentNewsArbitraryCodeFact.MatchString(evidenceText) && !currentNewsRemoteCodeFact.MatchString(evidenceText) {
				count := len(currentNewsRemoteCodeClaim.FindAllStringIndex(updated, -1))
				updated = currentNewsRemoteCodeClaim.ReplaceAllString(updated, "可导致任意代码执行")
				changed += count
			}
			if !securitycore.ExternalURLPattern().MatchString(updated) && strings.Contains(updated, "多家媒体") && !strings.Contains(evidenceText, "多家媒体") && len(urls) < 2 {
				updated = strings.ReplaceAll(updated, "多家媒体", "该来源")
				changed++
			}

			if isImportance && separator >= 0 {
				unsupported := false
				for _, token := range currentNewsAcronym.FindAllString(updated[separator+1:], -1) {
					if !strings.Contains(evidenceLower, strings.ToLower(token)) {
						unsupported = true
						break
					}
				}
				if !unsupported {
					if claim := currentNewsStrongInference.FindString(updated[separator+1:]); claim != "" && !strings.Contains(evidenceText, claim) {
						unsupported = true
					}
				}
				if !unsupported && currentNewsHistoricalFirst.MatchString(updated[separator+1:]) && !currentNewsHistoricalFirstFact.MatchString(evidenceText) {
					unsupported = true
				}
				if !unsupported && currentNewsVolumeFlatFact.MatchString(evidenceText) && currentNewsVolumeGrowthClaim.MatchString(updated[separator+1:]) {
					unsupported = true
				}
				if unsupported {
					_, size := utf8.DecodeRuneInString(updated[separator:])
					prefix := updated[:separator+size]
					fallback := " " + currentNewsSafeImportanceFallback(section)
					if strings.Contains(prefix, "分析") {
						fallback = " " + strings.TrimPrefix(currentNewsSafeImportanceFallback(section), "分析：")
					}
					updated = prefix + fallback
					changed++
				}
			}
			if isCore && currentNewsVolumeFlatFact.MatchString(evidenceText) && strings.Contains(updated, "量价齐升") {
				updated = strings.ReplaceAll(updated, "量价齐升", "平均售价上升、销量持平")
				changed++
			}

			if updated != line {
				replacements = append(replacements, replacement{start: match[0] + lineStart, end: match[0] + lineEnd, value: updated})
			}
			if lineEnd >= len(section) {
				break
			}
			lineStart = lineEnd + 1
		}
	}
	sort.Slice(replacements, func(i, j int) bool { return replacements[i].start > replacements[j].start })
	for _, item := range replacements {
		answer = answer[:item.start] + item.value + answer[item.end:]
	}
	return answer, changed
}

// NormalizeCurrentNewsSummaryScope keeps the management summary inside the
// numbered brief. A synthesis model sees more eligible RSS rows than it finally
// selects and can accidentally summarize a discarded candidate. When an
// identity token occurs in an uncited evidence row but nowhere in the selected
// items, rebuild the summary from the final core facts of the cross-checked
// items. This is a local packaging invariant and never triggers another model
// call.
func NormalizeCurrentNewsSummaryScope(input, answer string, toolResults []string) (string, []string) {
	contract, ok := CurrentNewsDeliveryContract(input)
	if !ok || strings.TrimSpace(answer) == "" || len(toolResults) == 0 {
		return answer, nil
	}
	start, end, found := newsSummaryBodyRange(answer)
	if !found || start >= end {
		return answer, nil
	}

	sections := numberedResearchItemSections(answer)
	var selectedText strings.Builder
	selectedURLs := make(map[string]bool)
	for item := 1; item <= contract.ItemCount; item++ {
		section := sections[strconv.Itoa(item)]
		if section == "" {
			continue
		}
		selectedText.WriteString(section)
		selectedText.WriteByte('\n')
		urls, _ := newsArticleCitationCoverage(section)
		for _, articleURL := range urls {
			if key := CanonicalCitationURLKey(articleURL); key != "" {
				selectedURLs[key] = true
			}
		}
	}
	selectedTokens := currentNewsIdentityTokens(selectedText.String())
	unselectedEvidenceTokens := make(map[string]bool)
	for key, evidence := range currentNewsEvidenceIdentityTextByURL(toolResults) {
		if selectedURLs[key] {
			continue
		}
		for token := range currentNewsIdentityTokens(evidence) {
			if len([]rune(token)) >= 4 {
				unselectedEvidenceTokens[token] = true
			}
		}
	}

	leaked := make([]string, 0, 3)
	for token := range currentNewsIdentityTokens(answer[start:end]) {
		if len([]rune(token)) >= 4 && !selectedTokens[token] && unselectedEvidenceTokens[token] {
			leaked = append(leaked, token)
		}
	}
	if len(leaked) == 0 {
		return answer, nil
	}
	sort.Strings(leaked)
	replacement := buildCurrentNewsSelectedFactsSummary(contract, sections)
	if replacement == "" {
		return answer, nil
	}
	return replaceCurrentNewsSummaryBody(answer, start, end, replacement), leaked
}

type currentNewsSummaryFact struct {
	item         int
	value        string
	crossChecked bool
}

func buildCurrentNewsSelectedFactsSummary(contract researchDeliveryContract, sections map[string]string) string {
	candidates := make([]currentNewsSummaryFact, 0, contract.ItemCount)
	for item := 1; item <= contract.ItemCount; item++ {
		section := sections[strconv.Itoa(item)]
		values := currentNewsFieldValues(section, "核心事实")
		if section == "" || len(values) == 0 {
			continue
		}
		_, publishers := newsArticleCitationCoverage(section)
		candidates = append(candidates, currentNewsSummaryFact{
			item:         item,
			value:        values[0],
			crossChecked: len(publishers) >= 2,
		})
	}
	if len(candidates) == 0 {
		return ""
	}
	desired := contract.CrossCheckCount
	if desired <= 0 {
		desired = min(3, len(candidates))
	}
	desired = min(desired, len(candidates))
	selected := make([]currentNewsSummaryFact, 0, desired)
	used := make(map[int]bool)
	for _, candidate := range candidates {
		if candidate.crossChecked && len(selected) < desired {
			selected = append(selected, candidate)
			used[candidate.item] = true
		}
	}
	for _, candidate := range candidates {
		if len(selected) >= desired {
			break
		}
		if !used[candidate.item] {
			selected = append(selected, candidate)
			used[candidate.item] = true
		}
	}
	if len(selected) == 0 {
		return ""
	}

	maxChars := contract.SummaryMaxChars
	if maxChars <= 0 {
		maxChars = 150
	}
	const prefix = "管理重点："
	perFactLimit := (maxChars - len([]rune(prefix)) - len(selected)) / len(selected)
	perFactLimit = max(18, perFactLimit)
	facts := make([]string, 0, len(selected))
	for _, candidate := range selected {
		fact := compactNewsSummaryText(candidate.value, perFactLimit)
		fact = strings.TrimSpace(strings.TrimRight(fact, "。！？!?;；"))
		if fact == "" || fact == "今日重点详见以上新闻条目" || fact == "重点见上文" {
			continue
		}
		facts = append(facts, fact)
	}
	if len(facts) == 0 {
		return ""
	}
	summary := prefix + strings.Join(facts, "；") + "。"
	if len([]rune(newsSummaryFormatPattern.ReplaceAllString(summary, ""))) > maxChars {
		summary = compactNewsSummaryText(summary, maxChars)
	}
	return summary
}

func currentNewsSafeImportanceFallback(section string) string {
	switch {
	case currentNewsUnderwritingImportance.MatchString(section):
		return "分析：承销信息关系到发行推进、市场询价和后续定价，是判断上市进度的直接信号。"
	case currentNewsListingImportance.MatchString(section):
		return "分析：募资规模与交易价格反映了本次上市的资本体量和市场定价，也为后续估值比较提供基准。"
	case currentNewsLegalImportance.MatchString(section):
		return "分析：诉讼结果会影响相关技术、人员或商业秘密的使用边界，并可能改变双方后续业务安排。"
	case currentNewsSecurityImportance.MatchString(section):
		return "分析：该问题关系到相关系统的风险评估与修复安排，实际影响范围仍需确认。"
	case currentNewsRocketImportance.MatchString(section):
		return "分析：助推器回收是检验可重复使用能力的关键环节，后续复用次数和发射表现将决定工程成熟度。"
	case currentNewsCultureImportance.MatchString(section):
		return "分析：数字化重现拓展了文物研究和展示方式，同时明确了 AI 应用不能替代实物保护的边界。"
	case currentNewsEmergencyImportance.MatchString(section):
		return "分析：智能调度与即时配送接入急救流程，直接关系到 AED 能否更快到达现场及该模式能否规模化。"
	case currentNewsResearchImportance.MatchString(section):
		return "分析：新机制的发现扩展了对相关科学过程的理解，也为后续验证和应用研究提供实验起点。"
	case currentNewsFinanceImportance.MatchString(section):
		return "分析：该信息直接影响对企业融资进度、经营表现和市场定价的判断，后续披露可用于验证趋势。"
	case currentNewsTechnologyImportance.MatchString(section):
		return "分析：该技术能否从单次进展走向稳定应用，取决于后续性能验证、部署范围和持续运行表现。"
	default:
		return "分析：该事实改变了相关主体的当前行动状态，后续公开进展将影响对其执行效果的判断。"
	}
}

// NormalizeCurrentNewsDiscardedCandidateNotes removes model-authored selection
// commentary that explicitly names a candidate and then says it was not chosen.
// Such a note is still a ninth, field-incomplete news fact from the user's point
// of view. Only detached note paragraphs between the last numbered item and the
// management summary are eligible for removal.
func NormalizeCurrentNewsDiscardedCandidateNotes(input, answer string) (string, int) {
	contract, ok := CurrentNewsDeliveryContract(input)
	if !ok || contract.ItemCount <= 0 || strings.TrimSpace(answer) == "" {
		return answer, 0
	}
	lines := strings.Split(strings.ReplaceAll(answer, "\r\n", "\n"), "\n")
	lastItemLine := -1
	summaryLine := -1
	for index, line := range lines {
		if match := researchNumberedItemPattern.FindStringSubmatch(line); len(match) >= 2 && parseRequestedCount(match[1]) == contract.ItemCount {
			lastItemLine = index
		}
		if summaryLine < 0 {
			if _, _, found := newsSummaryMarkerInLine(line, true); found {
				summaryLine = index
			}
		}
	}
	if lastItemLine < 0 || summaryLine <= lastItemLine {
		return answer, 0
	}

	out := make([]string, 0, len(lines))
	removed := 0
	for index := 0; index < len(lines); {
		if index > lastItemLine && index < summaryLine && currentNewsDiscardedNoteLead.MatchString(lines[index]) {
			end := index + 1
			for end < summaryLine && strings.TrimSpace(lines[end]) != "" {
				end++
			}
			paragraph := strings.Join(lines[index:end], "\n")
			if currentNewsDiscardedNoteCue.MatchString(paragraph) {
				removed++
				index = end
				continue
			}
		}
		out = append(out, lines[index])
		index++
	}
	if removed == 0 {
		return answer, 0
	}
	return strings.Join(out, "\n"), removed
}

func currentNewsUnsupportedWordingReplacements(input, answer string, toolResults []string) []currentNewsWordingReplacement {
	if _, ok := CurrentNewsDeliveryContract(input); !ok || strings.TrimSpace(answer) == "" {
		return nil
	}
	evidenceByURL := currentNewsEvidenceIdentityTextByURL(toolResults)
	matches := researchNumberedItemPattern.FindAllStringSubmatchIndex(answer, -1)
	replacements := make([]currentNewsWordingReplacement, 0, 2)
	for index, match := range matches {
		if len(match) < 4 {
			continue
		}
		sectionEnd := len(answer)
		if index+1 < len(matches) {
			sectionEnd = matches[index+1][0]
		}
		sectionEnd = researchItemSectionEnd(answer, match[0], match[2], sectionEnd)
		section := answer[match[0]:sectionEnd]
		urls, _ := newsArticleCitationCoverage(section)
		var evidence strings.Builder
		for _, articleURL := range urls {
			evidence.WriteString(evidenceByURL[CanonicalCitationURLKey(articleURL)])
			evidence.WriteByte('\n')
		}
		comparableEvidence := currentNewsComparableVerbatim(evidence.String())
		for _, pattern := range currentNewsQuotedWording {
			for _, quote := range pattern.FindAllStringSubmatchIndex(section, -1) {
				if len(quote) < 4 {
					continue
				}
				inner := strings.TrimSpace(section[quote[2]:quote[3]])
				if inner == "" || securitycore.ExternalURLPattern().MatchString(inner) || strings.Contains(comparableEvidence, currentNewsComparableVerbatim(inner)) {
					continue
				}
				replacements = append(replacements, currentNewsWordingReplacement{
					start: match[0] + quote[0],
					end:   match[0] + quote[1],
					value: inner,
				})
			}
		}
		for _, expansion := range currentNewsQuotedExpansion.FindAllStringSubmatchIndex(section, -1) {
			if len(expansion) < 4 {
				continue
			}
			value := section[expansion[2]:expansion[3]]
			inner := strings.Trim(value, "（）() ")
			if inner == "" || strings.Contains(comparableEvidence, currentNewsComparableVerbatim(inner)) {
				continue
			}
			replacements = append(replacements, currentNewsWordingReplacement{
				start: match[0] + expansion[2],
				end:   match[0] + expansion[3],
				value: "",
			})
		}
	}
	return replacements
}

func currentNewsComparableVerbatim(value string) string {
	var result strings.Builder
	for _, char := range strings.ToLower(value) {
		if !unicode.IsSpace(char) {
			result.WriteRune(char)
		}
	}
	return result.String()
}

func NormalizeCurrentNewsUnsupportedExchangeVenues(input, answer string, toolResults []string) (string, int) {
	if _, ok := CurrentNewsDeliveryContract(input); !ok || strings.TrimSpace(answer) == "" {
		return answer, 0
	}
	evidenceByURL := currentNewsEvidenceIdentityTextByURL(toolResults)
	type replacement struct {
		start int
		end   int
		value string
	}
	replacements := make([]replacement, 0, 1)
	changed := 0
	matches := researchNumberedItemPattern.FindAllStringSubmatchIndex(answer, -1)
	for index, match := range matches {
		sectionEnd := len(answer)
		if index+1 < len(matches) {
			sectionEnd = matches[index+1][0]
		}
		sectionEnd = researchItemSectionEnd(answer, match[0], match[2], sectionEnd)
		section := answer[match[0]:sectionEnd]
		urls, _ := newsArticleCitationCoverage(section)
		var evidence strings.Builder
		for _, articleURL := range urls {
			evidence.WriteString(evidenceByURL[CanonicalCitationURLKey(articleURL)])
			evidence.WriteByte('\n')
		}
		for lineStart := 0; lineStart < len(section); {
			lineEnd := strings.IndexByte(section[lineStart:], '\n')
			if lineEnd < 0 {
				lineEnd = len(section)
			} else {
				lineEnd += lineStart
			}
			line := section[lineStart:lineEnd]
			updated := line
			if !securitycore.ExternalURLPattern().MatchString(line) {
				for _, rule := range currentNewsVenueRules {
					if rule.pattern.MatchString(updated) && !rule.pattern.MatchString(evidence.String()) {
						count := len(rule.pattern.FindAllStringIndex(updated, -1))
						updated = rule.pattern.ReplaceAllString(updated, rule.neutral)
						changed += count
					}
				}
			}
			if updated != line {
				replacements = append(replacements, replacement{start: match[0] + lineStart, end: match[0] + lineEnd, value: updated})
			}
			if lineEnd >= len(section) {
				break
			}
			lineStart = lineEnd + 1
		}
	}
	sort.Slice(replacements, func(i, j int) bool { return replacements[i].start > replacements[j].start })
	for _, item := range replacements {
		answer = answer[:item.start] + item.value + answer[item.end:]
	}
	return answer, changed
}

func currentNewsEvidenceIdentityTextByURL(toolResults []string) map[string]string {
	result := make(map[string]string)
	for _, toolResult := range toolResults {
		record, ok := toolcontract.ParseRecord(toolResult)
		if !ok || !record.Succeeded || !securitycore.IsExternalEvidenceRecord(record) {
			continue
		}
		if strings.EqualFold(record.Tool, "social_trending") || strings.EqualFold(record.SearchMode, "discovery") {
			itemPattern := currentNewsRSSItemPattern
			indexes := itemPattern.FindAllStringIndex(toolResult, -1)
			if len(indexes) == 0 {
				itemPattern = currentNewsSearchItemPattern
				indexes = itemPattern.FindAllStringIndex(toolResult, -1)
			}
			for index, bounds := range indexes {
				end := len(toolResult)
				if index+1 < len(indexes) {
					end = indexes[index+1][0]
				}
				block := toolResult[bounds[0]:end]
				for _, rawURL := range securitycore.ExternalURLPattern().FindAllString(block, -1) {
					articleURL := NormalizeCitationURL(rawURL)
					key := CanonicalCitationURLKey(articleURL)
					if key != "" && IsArticleCitationURL(articleURL) && result[key] == "" {
						result[key] = block
					}
				}
			}
		}
		citationURL := CitationURLFromToolResult(toolResult)
		if citationURL == "" {
			citationURL = strings.TrimSpace(record.SourceURL)
		}
		key := CanonicalCitationURLKey(citationURL)
		if key != "" && IsArticleCitationURL(citationURL) && result[key] == "" {
			identity := toolResult
			if len(identity) > 2400 {
				identity = identity[:2400]
			}
			result[key] = identity
		}
	}
	return result
}

func currentNewsIdentityTokens(value string) map[string]bool {
	result := make(map[string]bool)
	for _, match := range currentNewsIdentityToken.FindAllString(value, -1) {
		token := strings.ToLower(match)
		if len(token) < 2 || currentNewsIdentityStopWords[token] {
			continue
		}
		result[token] = true
	}
	return result
}

func currentNewsIdentityOverlap(left, right map[string]bool) int {
	count := 0
	for token := range left {
		if right[token] {
			count++
		}
	}
	return count
}

// NormalizeCurrentNewsMediaPublicationTimes rebuilds the requested media-time
// field from the exact cited URLs and their RSS/search timestamps. Models often
// convert a UTC value to China time but accidentally keep the "UTC" label; the
// host can make this field exact without another model call.
func NormalizeCurrentNewsMediaPublicationTimes(input, answer string, toolResults []string) (string, int) {
	contract, ok := CurrentNewsDeliveryContract(input)
	if !ok || !researchContractRequiresLabel(contract, "媒体发布时间") || strings.TrimSpace(answer) == "" {
		return answer, 0
	}
	evidenceDates := currentNewsEvidenceDatesByURL(toolResults)
	if len(evidenceDates) == 0 {
		return answer, 0
	}
	type replacement struct {
		start int
		end   int
		value string
	}
	replacements := make([]replacement, 0, contract.ItemCount)
	matches := researchNumberedItemPattern.FindAllStringSubmatchIndex(answer, -1)
	for index, match := range matches {
		if len(match) < 4 {
			continue
		}
		item := parseRequestedCount(answer[match[2]:match[3]])
		if item < 1 || item > contract.ItemCount {
			continue
		}
		sectionEnd := len(answer)
		if index+1 < len(matches) {
			sectionEnd = matches[index+1][0]
		}
		sectionEnd = researchItemSectionEnd(answer, match[0], match[2], sectionEnd)
		section := answer[match[0]:sectionEnd]
		urls, _ := newsArticleCitationCoverage(section)
		entries := make([]string, 0, len(urls))
		seen := make(map[string]bool)
		for _, articleURL := range urls {
			key := CanonicalCitationURLKey(articleURL)
			dates := evidenceDates[key]
			if key == "" || len(dates) == 0 || seen[key] {
				continue
			}
			seen[key] = true
			published := dates[0].In(currentNewsShanghaiLocation)
			publisher := newsPublisherKey(articleURL)
			if publisher == "" {
				publisher = SourceHost(articleURL)
			}
			entries = append(entries, fmt.Sprintf("%s %s", publisher, published.Format("2006-01-02 15:04 -07:00")))
		}
		if len(entries) == 0 {
			continue
		}
		for lineStart := 0; lineStart < len(section); {
			lineEnd := strings.IndexByte(section[lineStart:], '\n')
			if lineEnd < 0 {
				lineEnd = len(section)
			} else {
				lineEnd += lineStart
			}
			line := section[lineStart:lineEnd]
			separator := strings.IndexAny(line, ":：")
			if separator >= 0 && newsAnswerContainsRequiredLabel(line[:separator], "媒体发布时间") {
				_, size := utf8.DecodeRuneInString(line[separator:])
				replacements = append(replacements, replacement{
					start: match[0] + lineStart + separator + size,
					end:   match[0] + lineEnd,
					value: " " + strings.Join(entries, "；"),
				})
				break
			}
			if lineEnd >= len(section) {
				break
			}
			lineStart = lineEnd + 1
		}
	}
	sort.Slice(replacements, func(i, j int) bool { return replacements[i].start > replacements[j].start })
	for _, replacement := range replacements {
		answer = answer[:replacement.start] + replacement.value + answer[replacement.end:]
	}
	return answer, len(replacements)
}

// NormalizeCurrentNewsUnsupportedEventTimes prevents a publication/reporting
// date from masquerading as the event occurrence time. A concrete event date is
// retained only when at least one cited evidence row contains an event-date cue
// outside its feed timestamp and publisher date boilerplate. Otherwise the
// requested field is answered honestly as unknown without another model call.
func NormalizeCurrentNewsUnsupportedEventTimes(input, answer string, toolResults []string) (string, int) {
	contract, ok := CurrentNewsDeliveryContract(input)
	if !ok || !researchContractRequiresLabel(contract, "事件发生时间") || strings.TrimSpace(answer) == "" {
		return answer, 0
	}
	evidenceText := currentNewsEvidenceIdentityTextByURL(toolResults)
	evidenceDates := currentNewsEvidenceDatesByURL(toolResults)
	if len(evidenceText) == 0 {
		return answer, 0
	}
	type replacement struct {
		start int
		end   int
	}
	replacements := make([]replacement, 0, 2)
	matches := researchNumberedItemPattern.FindAllStringSubmatchIndex(answer, -1)
	for index, match := range matches {
		if len(match) < 4 {
			continue
		}
		sectionEnd := len(answer)
		if index+1 < len(matches) {
			sectionEnd = matches[index+1][0]
		}
		sectionEnd = researchItemSectionEnd(answer, match[0], match[2], sectionEnd)
		section := answer[match[0]:sectionEnd]
		urls, _ := newsArticleCitationCoverage(section)
		evidenceHasEventDate := false
		for _, articleURL := range urls {
			if currentNewsEvidenceHasEventDateCue(evidenceText[CanonicalCitationURLKey(articleURL)]) {
				evidenceHasEventDate = true
				break
			}
		}
		for lineStart := 0; lineStart < len(section); {
			lineEnd := strings.IndexByte(section[lineStart:], '\n')
			if lineEnd < 0 {
				lineEnd = len(section)
			} else {
				lineEnd += lineStart
			}
			line := section[lineStart:lineEnd]
			separator := strings.IndexAny(line, ":：")
			if separator >= 0 && newsAnswerContainsRequiredLabel(line[:separator], "事件发生时间") {
				_, size := utf8.DecodeRuneInString(line[separator:])
				value := strings.TrimSpace(line[separator+size:])
				reportingPlaceholder := containsAnyFold(value, "报道日", "媒体报道", "发布时间", "刊发日") && containsAnyFold(value, "未披露", "不详", "未知", "unknown")
				conflictingRelativeDate := currentNewsCandidateConflictsWithPreviousDayCue(value, urls, evidenceText, evidenceDates)
				if currentNewsEventDateCue.MatchString(value) && (!evidenceHasEventDate || reportingPlaceholder || conflictingRelativeDate) {
					replacements = append(replacements, replacement{
						start: match[0] + lineStart + separator + size,
						end:   match[0] + lineEnd,
					})
				}
				break
			}
			if lineEnd >= len(section) {
				break
			}
			lineStart = lineEnd + 1
		}
	}
	sort.Slice(replacements, func(i, j int) bool { return replacements[i].start > replacements[j].start })
	for _, item := range replacements {
		answer = answer[:item.start] + " 不详（当前证据未披露事件发生时间）" + answer[item.end:]
	}
	return answer, len(replacements)
}

// A feed row can describe two different actions as "today" and "last night".
// If the draft assigns the publication day to the event while the cited row
// also contains a previous-day cue, the host cannot safely infer which action
// the model meant. Preserve an explicitly stated calendar date from another
// cited row; otherwise return unknown instead of attaching the wrong date.
func currentNewsCandidateConflictsWithPreviousDayCue(value string, urls []string, evidenceText map[string]string, evidenceDates map[string][]time.Time) bool {
	match := currentNewsISODatePattern.FindStringSubmatch(value)
	if len(match) < 4 {
		return false
	}
	year, _ := strconv.Atoi(match[1])
	month, _ := strconv.Atoi(match[2])
	day, _ := strconv.Atoi(match[3])
	candidate := time.Date(year, time.Month(month), day, 0, 0, 0, 0, currentNewsShanghaiLocation)
	if candidate.Year() != year || int(candidate.Month()) != month || candidate.Day() != day {
		return false
	}
	conflict := false
	explicitSupport := false
	for _, articleURL := range urls {
		key := CanonicalCitationURLKey(articleURL)
		evidence := evidenceText[key]
		if strings.TrimSpace(evidence) == "" {
			continue
		}
		if currentNewsEvidenceExplicitlyMentionsCalendarDate(evidence, candidate) {
			explicitSupport = true
		}
		if !currentNewsPreviousDayCue.MatchString(evidence) {
			continue
		}
		for _, published := range evidenceDates[key] {
			if SameCurrentNewsLocalDate(published, candidate) {
				conflict = true
				break
			}
		}
	}
	return conflict && !explicitSupport
}

func currentNewsEvidenceExplicitlyMentionsCalendarDate(evidence string, candidate time.Time) bool {
	for _, rawLine := range strings.Split(strings.ReplaceAll(evidence, "\r\n", "\n"), "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" || currentNewsPublishedLine.MatchString(line) || strings.HasPrefix(line, "🔄") || strings.HasPrefix(line, "[CURRENT_NEWS_") || strings.HasPrefix(line, "[EVIDENCE_") || securitycore.ExternalURLPattern().MatchString(line) {
			continue
		}
		line = currentNewsPublisherDateLead.ReplaceAllString(line, "")
		for _, rawDate := range currentNewsISODatePattern.FindAllString(line, -1) {
			parts := currentNewsISODatePattern.FindStringSubmatch(rawDate)
			if len(parts) < 4 {
				continue
			}
			year, _ := strconv.Atoi(parts[1])
			month, _ := strconv.Atoi(parts[2])
			day, _ := strconv.Atoi(parts[3])
			if year == candidate.Year() && time.Month(month) == candidate.Month() && day == candidate.Day() {
				return true
			}
		}
		monthDay := fmt.Sprintf("%d月%d日", candidate.Month(), candidate.Day())
		if strings.Contains(strings.ReplaceAll(line, " ", ""), monthDay) {
			return true
		}
	}
	return false
}

func currentNewsEvidenceHasEventDateCue(evidence string) bool {
	if strings.TrimSpace(evidence) == "" {
		return false
	}
	for _, rawLine := range strings.Split(strings.ReplaceAll(evidence, "\r\n", "\n"), "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" || currentNewsPublishedLine.MatchString(line) || strings.HasPrefix(line, "🔄") || strings.HasPrefix(line, "[CURRENT_NEWS_") || strings.HasPrefix(line, "[EVIDENCE_") || securitycore.ExternalURLPattern().MatchString(line) {
			continue
		}
		line = currentNewsPublisherDateLead.ReplaceAllString(line, "")
		if currentNewsEventDateCue.MatchString(line) {
			return true
		}
	}
	return false
}

func currentNewsEvidenceDatesByURL(toolResults []string) map[string][]time.Time {
	result := make(map[string][]time.Time)
	seen := make(map[string]map[int64]bool)
	for _, toolResult := range toolResults {
		record, ok := toolcontract.ParseRecord(toolResult)
		if !ok || !record.Succeeded || !securitycore.IsExternalEvidenceRecord(record) {
			continue
		}
		itemPattern := currentNewsRSSItemPattern
		indexes := itemPattern.FindAllStringIndex(toolResult, -1)
		if len(indexes) == 0 {
			itemPattern = currentNewsSearchItemPattern
			indexes = itemPattern.FindAllStringIndex(toolResult, -1)
		}
		for index, bounds := range indexes {
			end := len(toolResult)
			if index+1 < len(indexes) {
				end = indexes[index+1][0]
			}
			block := toolResult[bounds[0]:end]
			published, parsed := currentNewsPublishedTime(block)
			if !parsed {
				continue
			}
			for _, rawURL := range securitycore.ExternalURLPattern().FindAllString(block, -1) {
				articleURL := NormalizeCitationURL(rawURL)
				if !IsArticleCitationURL(articleURL) {
					continue
				}
				key := CanonicalCitationURLKey(articleURL)
				if key == "" {
					continue
				}
				if seen[key] == nil {
					seen[key] = make(map[int64]bool)
				}
				stamp := published.Unix()
				if seen[key][stamp] {
					continue
				}
				seen[key][stamp] = true
				result[key] = append(result[key], published)
			}
		}
	}
	return result
}

// currentNewsEvidenceReadyForSynthesis is a cheap sufficiency gate, not a
// semantic news judge. It closes retrieval only when the filtered evidence set
// already has enough distinct article URLs for every requested item plus the
// requested cross-check margin, spanning multiple publishers when needed.
func currentNewsEvidenceReadyForSynthesis(input string, toolResults []string) bool {
	contract, ok := CurrentNewsDeliveryContract(input)
	if !ok {
		return false
	}
	articleKeys := make(map[string]bool)
	publishers := make(map[string]bool)
	for _, result := range toolResults {
		record, ok := toolcontract.ParseRecord(result)
		if !ok || !record.Succeeded || !securitycore.IsExternalEvidenceRecord(record) {
			continue
		}
		for _, rawURL := range securitycore.ExternalURLPattern().FindAllString(result, -1) {
			articleURL := NormalizeCitationURL(rawURL)
			if !IsArticleCitationURL(articleURL) {
				continue
			}
			key := CanonicalCitationURLKey(articleURL)
			if key == "" {
				continue
			}
			articleKeys[key] = true
			if publisher := newsPublisherKey(articleURL); publisher != "" {
				publishers[publisher] = true
			}
		}
	}
	requiredArticles := contract.ItemCount + contract.CrossCheckCount
	if len(articleKeys) < requiredArticles {
		return false
	}
	return contract.CrossCheckCount == 0 || len(publishers) >= 2
}
