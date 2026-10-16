package research

import (
	"fmt"
	"mac/internal/agentloop"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	securitycore "mac/internal/security"

	"golang.org/x/net/publicsuffix"
)

var (
	readOnlyResearchSignal         = regexp.MustCompile(`(?i)(联网|网上|全网|调研|研究|实时|最新|天气|油价|股价|股票|行情|汇率|新闻|热点|热搜|航班|列车|酒店|景点|价格|research|look\s*up|search\s+(?:the\s+)?web|online|weather|news|stock|price|exchange\s+rate)`)
	researchArtifactSignal         = regexp.MustCompile(`(?i)(ppt|powerpoint|word|docx|pdf|excel|xlsx|csv|文件|保存|写入|落盘|下载|导出|发布|发送|发到|发给|同步到|上传|推送|转发|邮件|部署|修改|创建|(?:生成|写|整理|产出|输出).{0,12}(?:报告|文档|表格|幻灯片|文件|清单|方案|报价|架构)|create|save|write|export|publish|send|deploy|modify)`)
	researchArtifactMetadataSignal = regexp.MustCompile(`(?i)(?:(?:媒体)?(?:发布|发行|发送|修改|创建|更新)(?:日期|时间)|(?:发布|发行|更新)于\s*[0-9]{4}|(?:publication|publish(?:ed)?|release|sent|modified|created|updated)(?:\s+at)?\s+(?:date|time)|publication\s+date)`)
	researchCapabilityFieldSignal  = regexp.MustCompile(`(?i)(?:(?:会话|数据|记录|工单|报表|文件|聊天)?导出(?:能力|功能|格式|接口|限制|支持|口径)|部署(?:限制|模式|方式|能力|要求|选项|支持)|export\s+(?:capabilit(?:y|ies)|feature|format|support|limit|api)|deployment\s+(?:limit|mode|option|requirement|support))`)
	currentNewsBriefSignal         = regexp.MustCompile(`(?i)(?:新闻|热点|要闻|快讯|资讯|news|headline|briefing)`)
	searchItemCountAfterNumber     = regexp.MustCompile(`(?i)([0-9]{1,2}|[一二两三四五六七八九十]{1,3})\s*(?:条|项|个|家|款|篇|则|组|份|点|例|案例|候选(?:项)?|结果|items?|examples?|options?|entries?|results?|candidates?)`)
	searchTopCount                 = regexp.MustCompile(`(?i)(?:top|前)\s*([0-9]{1,2}|[一二两三四五六七八九十]{1,3})`)

	researchNumberedItemPattern = regexp.MustCompile(`(?m)^\s*(?:#{1,6}\s*)?(?:\*{0,2})?(?:[★⭐]\s*)?([1-9][0-9]?)\s*[.、．)]`)
	researchTableItemPattern    = regexp.MustCompile(`(?m)^\s*\|\s*([1-9][0-9]?)\s*\|`)
	newsCircledItemPattern      = regexp.MustCompile(`(?m)^(\s*(?:#{1,6}\s*)?(?:\*{0,2})?(?:[★⭐]\s*)?)([①②③④⑤⑥⑦⑧⑨⑩⑪⑫⑬⑭⑮⑯⑰⑱⑲⑳])`)
	newsCrossCheckCountPattern  = regexp.MustCompile(`(?i)(?:最重要的\s*)?([0-9]{1,2}|[一二两三四五六七八九十]{1,3})\s*条.{0,32}(?:第二(?:个|条)?来源|再找.{0,8}来源|交叉核对|二次核对|second\s+source|cross.?check)`)
	newsImportantCrossCheck     = regexp.MustCompile(`(?i)(?:其中|对其中|对)?\s*(?:最重要的?|重点)\s*([0-9]{1,2}|[一二两三四五六七八九十]{1,3})\s*条.{0,32}(?:第二(?:个|条)?来源|再找.{0,8}来源|交叉核对|二次核对|second\s+source|cross.?check)`)
	newsSummaryLimitPattern     = regexp.MustCompile(`(?:摘要.{0,16}?([0-9]{2,4})\s*字以内|([0-9]{2,4})\s*字以内.{0,16}?摘要)`)
	newsSummaryPrefixPattern    = regexp.MustCompile(`^[\s：:（(\[【≤<0-9约字以内)）\]】>*_-]*`)
	newsSummaryFormatPattern    = regexp.MustCompile("[\\s*_`]+")
	newsSummaryLeadingLimit     = regexp.MustCompile(`^(?:≤|<)?\s*[0-9]{2,4}\s*字(?:以内)?$`)
	newsSummaryAllowedPrefix    = regexp.MustCompile(`^(?:(?:第)?[一二三四五六七八九十0-9]{1,4}\s*[、.．)]\s*)?(?:(?:≤|<)?\s*[0-9]{2,4}\s*字(?:以内)?\s*)?$`)
	newsCoreEmojiPattern        = regexp.MustCompile(`(?m)^(\s*[-*]\s*)📌\s*(?:核心事实\s*[:：]\s*)?`)
	newsImportanceEmojiPattern  = regexp.MustCompile(`(?m)^(\s*[-*]\s*)💡\s*(?:为什么重要\s*[:：]\s*)?`)
	newsImportancePlaceholder   = regexp.MustCompile(`^(?:分析\s*[:：]\s*)?(?:该进展的实际影响仍需后续权威披露验证|这些数据为观察相关企业经营、融资或市场定价变化提供了新依据|该进展为评估相关技术或产品的成熟度与后续应用提供了新依据|该进展为判断相关主体的后续安排及行业变化提供了新依据)[。.]?$`)
	newsMediaEmojiPattern       = regexp.MustCompile(`(?m)^(\s*[-*]\s*)⏰\s*媒体发布时间\s*[:：]\s*`)
	newsEventEmojiPattern       = regexp.MustCompile(`(?m)^(\s*[-*]\s*)⏰\s*(?:事件发生时间|事件时间)\s*[:：]\s*`)
	newsItemCrossReference      = regexp.MustCompile(`(?:与|同)(?:第\s*)?([1-9][0-9]?)\s*条`)
	newsSummaryMarkdownLink     = regexp.MustCompile(`\[([^\]\n]+)\]\(https?://[^\s\)]+\)`)
	newsEventActionCuePattern   = regexp.MustCompile(`(?i)(推出|发布|宣布|披露|离职|辞职|起诉|关闭|关停|下架|打击|融资|募资|上市|发行|回收|发射|裁员|获奖|收购|启动|禁令|罚款|会晤|会见|出访|来访|访问|缺货|断供|launch(?:ed)?|sues?|shuts?\s+down|resigns?|raises?|ipo)`)
	newsBundledSameDayPattern   = regexp.MustCompile(`(?:；|;|。)\s*(?:同日|同时|另外|此外)\s*[,，:：]?`)
	newsBundledRoundupTail      = regexp.MustCompile(`(?:；|;|。)\s*(?:同日|同时|另外|此外)[^。\n]{0,160}(?:其他(?:要点|消息|事件|标题)|、)`)
	newsBundledTimeWindow       = regexp.MustCompile(`(?:与|及)[^。\n]{0,160}(?:同日|同周|同期|同时)\s*(?:发生|披露|公布)`)
	newsExplicitBundledTail     = regexp.MustCompile(`[,，；;]\s*(?:与|及)[^。\n]{0,160}(?:同日|同周|同期|同时)\s*(?:发生|披露|公布)[^。\n]*`)
	newsExplicitRoundupTail     = regexp.MustCompile(`[,，；;]\s*[^。\n]{0,48}(?:聚合|摘要|早报|晚报|标题)[^。\n]{0,24}(?:另(?:外)?|还|同时)?\s*(?:点名|提及|提到|列出|包括)[^。\n]*`)
	newsSummaryDeclaredCount    = regexp.MustCompile(`^((?:今日|本日)?)(?:\s*[一二两三四五六七八九十0-9]+\s*条)?\s*(?:(?:新闻|热点|要闻|内容)\s*)?(?:(?:呈现|显示|形成|归纳为|可归纳为|集中在|聚焦于|围绕|分为|覆盖)\s*)?(?:(?:热点|重点)(?:集中在|归纳为|可归纳为|聚焦于|围绕)?\s*)?(?:最关键的?)?[一二两三四五六七八九十0-9]+(?:(?:条|大)(?:主线|重点|要点)|类(?:信号|趋势|方向|主题)|(?:个)?(?:方面|方向|信号))\s*[:：]`)
	newsBundledIssuePattern     = regexp.MustCompile(`^item\s+([1-9][0-9]?)\s+explicitly bundles multiple underlying events`)
	newsPublicationTimePattern  = regexp.MustCompile(`(?i)([12][0-9]{3})\s*(?:-|/|年)\s*(1[0-2]|0?[1-9])\s*(?:-|/|月)\s*(3[01]|[12][0-9]|0?[1-9])(?:\s*日)?(?:[T\s]+([01]?[0-9]|2[0-3]):([0-5][0-9])(?::([0-5][0-9]))?)?(?:\s*(Z|UTC|GMT|EDT|EST|CDT|CST|MDT|MST|PDT|PST|[+-](?:0[0-9]|1[0-4]):?[0-5][0-9]|Asia/Shanghai|北京时间|中国标准时间))?`)
	newsPublicationMonthDay     = regexp.MustCompile(`(?i)\b(1[0-2]|0?[1-9])\s*(?:-|/|月)\s*(3[01]|[12][0-9]|0?[1-9])(?:\s*日)?(?:[T\s]+([01]?[0-9]|2[0-3]):([0-5][0-9])(?::([0-5][0-9]))?)?(?:\s*(Z|UTC|GMT|EDT|EST|CDT|CST|MDT|MST|PDT|PST|[+-](?:0[0-9]|1[0-4]):?[0-5][0-9]|Asia/Shanghai|北京时间|中国标准时间))?`)
)

type researchDeliveryContract struct {
	ItemCount       int
	CrossCheckCount int
	RequiresLinks   bool
	RequiredLabels  []string
	SummaryMaxChars int
}

func IsResearchRequest(input string) bool {
	return readOnlyResearchSignal.MatchString(strings.TrimSpace(input))
}

func IsCurrentNewsBrief(input string) bool {
	return currentNewsBriefSignal.MatchString(input)
}

func IsFastReadOnlyResearchTurn(input string) bool {
	input = strings.TrimSpace(input)
	artifactProbe := researchArtifactMetadataSignal.ReplaceAllString(input, "")
	artifactProbe = researchCapabilityFieldSignal.ReplaceAllString(artifactProbe, "")
	return input != "" && readOnlyResearchSignal.MatchString(input) && !researchArtifactSignal.MatchString(artifactProbe)
}

func maxRequestedCount(input string, patterns ...*regexp.Regexp) int {
	best := 0
	for _, pattern := range patterns {
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

func CurrentNewsDeliveryContract(input string) (researchDeliveryContract, bool) {
	if !IsFastReadOnlyResearchTurn(input) || !currentNewsBriefSignal.MatchString(input) {
		return researchDeliveryContract{}, false
	}
	itemCount := maxRequestedCount(input, searchItemCountAfterNumber, searchTopCount)
	if itemCount < 3 {
		return researchDeliveryContract{}, false
	}
	contract := researchDeliveryContract{
		ItemCount:     itemCount,
		RequiresLinks: RequestsSourceLinks(input) || strings.Contains(input, "来源"),
	}
	if match := newsImportantCrossCheck.FindStringSubmatch(input); len(match) > 1 {
		contract.CrossCheckCount = parseRequestedCount(match[1])
	} else if match := newsCrossCheckCountPattern.FindStringSubmatch(input); len(match) > 1 {
		contract.CrossCheckCount = parseRequestedCount(match[1])
	}
	if match := newsSummaryLimitPattern.FindStringSubmatch(input); len(match) > 2 {
		for _, raw := range match[1:] {
			if parsed := parseRequestedCount(raw); parsed > 0 {
				contract.SummaryMaxChars = parsed
				break
			}
		}
	}
	for _, label := range []string{"事件发生时间", "媒体发布时间", "核心事实", "为什么重要"} {
		if strings.Contains(input, label) {
			contract.RequiredLabels = append(contract.RequiredLabels, label)
		}
	}
	return contract, true
}

func ResearchDeliveryIssues(input, answer string) []string {
	contract, ok := CurrentNewsDeliveryContract(input)
	if !ok {
		return nil
	}
	answer, _ = normalizeCurrentNewsItemMarkers(answer)
	issues := make([]string, 0, 6)
	if count := countResearchItems(answer); count < contract.ItemCount {
		issues = append(issues, fmt.Sprintf("answer contains only %d clearly numbered item(s); %d requested", count, contract.ItemCount))
	}
	itemSections := numberedResearchItemSections(answer)
	if len(itemSections) >= contract.ItemCount {
		for _, label := range contract.RequiredLabels {
			present := 0
			for item := 1; item <= contract.ItemCount; item++ {
				if currentNewsFieldHasValue(itemSections[fmt.Sprintf("%d", item)], label) {
					present++
				}
			}
			if present < contract.ItemCount {
				issues = append(issues, fmt.Sprintf("only %d/%d numbered item(s) contain requested field: %s", present, contract.ItemCount, label))
			}
		}
		if researchContractRequiresLabel(contract, "为什么重要") {
			for item := 1; item <= contract.ItemCount; item++ {
				for _, value := range currentNewsFieldValues(itemSections[fmt.Sprintf("%d", item)], "为什么重要") {
					if newsImportancePlaceholder.MatchString(strings.TrimSpace(value)) {
						issues = append(issues, fmt.Sprintf("item %d why-it-matters field is only a generic verification placeholder", item))
						break
					}
				}
			}
		}
	} else {
		for _, label := range contract.RequiredLabels {
			if !currentNewsFieldHasValue(answer, label) {
				issues = append(issues, "missing required field label: "+label)
			}
		}
	}
	if strings.Contains(input, "摘要") {
		count, summaryPresent := newsSummaryVisibleCharCount(answer)
		if !summaryPresent {
			issues = append(issues, "missing requested summary")
		} else if currentNewsSummaryEndsIncomplete(answer) {
			issues = append(issues, "summary ends with an incomplete ellipsis")
		} else if !currentNewsSummaryDelimitersBalanced(answer) {
			issues = append(issues, "summary contains an unclosed delimiter and appears truncated")
		} else if contract.SummaryMaxChars > 0 {
			if count > contract.SummaryMaxChars {
				issues = append(issues, fmt.Sprintf("summary contains %d visible character(s); at most %d requested", count, contract.SummaryMaxChars))
			}
		}
	}
	if contract.RequiresLinks {
		articleURLs := numberedResearchArticleURLs(itemSections, contract.ItemCount)
		// Per-item link checks and independent-domain cross-check checks below
		// enforce the actual user contract. A publisher roundup may legitimately
		// support two distinct events, so requiring itemCount+crossCheckCount
		// globally unique URLs is stricter than the request and rejects good briefs.
		// Only numbered item bodies count here: a summary, appendix, or detached
		// cross-check section must not manufacture enough sources for a pass.
		requiredURLs := contract.ItemCount
		if len(articleURLs) < requiredURLs {
			issues = append(issues, fmt.Sprintf("only %d distinct article URL(s) inside numbered items; at least %d required", len(articleURLs), requiredURLs))
		}
		if checked := countCrossCheckedResearchItems(answer); checked < contract.CrossCheckCount {
			issues = append(issues, fmt.Sprintf("only %d item(s) contain citations from two independent publisher domains; %d requested", checked, contract.CrossCheckCount))
		}
		if len(itemSections) >= contract.ItemCount {
			linked := 0
			for item := 1; item <= contract.ItemCount; item++ {
				if urls, _ := newsArticleCitationCoverage(itemSections[fmt.Sprintf("%d", item)]); len(urls) > 0 {
					linked++
				}
			}
			if linked < contract.ItemCount {
				issues = append(issues, fmt.Sprintf("only %d/%d numbered item(s) contain an article URL", linked, contract.ItemCount))
			}
		}
	}
	if strings.Contains(input, "同一事件") && strings.Contains(input, "多条") {
		issues = append(issues, currentNewsDuplicateIssues(answer)...)
	}
	issues = append(issues, currentNewsFreshnessIssues(input, answer)...)
	issues = append(issues, currentNewsBundledEventIssues(answer)...)
	sort.Strings(issues)
	return issues
}

func currentNewsDuplicateIssues(answer string) []string {
	sections := numberedResearchItemSections(answer)
	issues := make([]string, 0, 2)
	for item, section := range sections {
		current := parseRequestedCount(item)
		for _, match := range newsItemCrossReference.FindAllStringSubmatchIndex(section, -1) {
			if len(match) < 4 {
				continue
			}
			referenced := parseRequestedCount(section[match[2]:match[3]])
			if referenced <= 0 || referenced == current {
				continue
			}
			windowStart := max(0, match[0]-16)
			windowEnd := min(len(section), match[1]+32)
			window := section[windowStart:windowEnd]
			if containsAnyFold(window,
				"不同于", "独立于", "并非同一", "不是同一",
				"形成对照", "对照", "对比", "相比", "区别于", "另一事件",
			) {
				continue
			}
			// A bare cross-reference is often a useful comparison between two
			// independent events. Only fail closed when the answer itself uses
			// language that admits the two items are facets of one event.
			if !containsAnyFold(window,
				"同一", "同次", "同场", "同批", "同轮",
				"协同", "同一次", "同一发布", "同一交易", "同一诉讼", "同一发射",
				"另一角度", "共同源于", "均源于",
			) {
				continue
			}
			issues = append(issues, fmt.Sprintf("item %d explicitly overlaps item %d; merge the same underlying event and use a distinct event for the freed slot", current, referenced))
			break
		}
	}
	return issues
}

// currentNewsBundledEventIssues catches explicit self-admissions that a
// numbered slot contains two separate headlines. It intentionally does not
// try to infer semantic similarity: a slash or "同日/同时" only fails when
// both sides also carry distinct event-action cues.
func currentNewsBundledEventIssues(answer string) []string {
	sections := numberedResearchItemSections(answer)
	issues := make([]string, 0, 2)
	for item, section := range sections {
		match := researchNumberedItemPattern.FindStringSubmatchIndex(section)
		if len(match) < 4 {
			continue
		}
		headingEnd := strings.IndexByte(section[match[0]:], '\n')
		if headingEnd < 0 {
			headingEnd = len(section) - match[0]
		}
		heading := strings.TrimSpace(section[match[0] : match[0]+headingEnd])
		bundled := false
		for _, separator := range []string{" / ", "／"} {
			parts := strings.SplitN(heading, separator, 2)
			if len(parts) == 2 && newsEventActionCueOccurrenceCount(parts[0]) > 0 && newsEventActionCueOccurrenceCount(parts[1]) > 0 {
				bundled = true
				break
			}
		}
		headingActionCount := distinctNewsEventActionCueCount(heading)
		coreFact := currentNewsCoreFactLine(section)
		coreActionCount := distinctNewsEventActionCueCount(coreFact)
		if !bundled && headingActionCount >= 2 && containsAnyFold(heading, "同时", "同日") {
			bundled = true
		}
		if !bundled && coreActionCount >= 2 && newsBundledSameDayPattern.MatchString(coreFact) {
			bundled = true
		}
		if !bundled && newsBundledRoundupTail.MatchString(coreFact) {
			bundled = true
		}
		if !bundled && newsEventActionCueOccurrenceCount(coreFact) >= 2 && newsBundledTimeWindow.MatchString(coreFact) {
			bundled = true
		}
		if bundled {
			issues = append(issues, fmt.Sprintf("item %d explicitly bundles multiple underlying events; keep exactly one event in each numbered item", parseRequestedCount(item)))
		}
	}
	return issues
}

// StripCurrentNewsExplicitBundledTails removes only an explicit comparison
// tail that names other events after an otherwise complete core fact. It is a
// cheap packaging repair for "COO离职，与诉讼、产品发布同期发生" and does not
// rewrite or infer the primary event itself.
func StripCurrentNewsExplicitBundledTails(answer string) (string, int) {
	lines := strings.Split(strings.ReplaceAll(answer, "\r\n", "\n"), "\n")
	changed := 0
	for index, line := range lines {
		separator := strings.IndexAny(line, ":：")
		if separator < 0 || !newsAnswerContainsRequiredLabel(line[:separator], "核心事实") {
			continue
		}
		match := newsExplicitBundledTail.FindStringIndex(line)
		if roundupMatch := newsExplicitRoundupTail.FindStringIndex(line); roundupMatch != nil && (match == nil || roundupMatch[0] < match[0]) {
			match = roundupMatch
		}
		if len(match) < 2 {
			continue
		}
		primary := strings.TrimRight(strings.TrimSpace(line[:match[0]]), ",，;；")
		tail := line[match[0]:match[1]]
		if currentNewsFieldHasValue(primary, "核心事实") && newsEventActionCueOccurrenceCount(primary) > 0 && newsEventActionCueOccurrenceCount(tail) > 0 {
			if !strings.HasSuffix(primary, "。") && !strings.HasSuffix(primary, ".") {
				primary += "。"
			}
			lines[index] = primary
			changed++
		}
	}
	return strings.Join(lines, "\n"), changed
}

func distinctNewsEventActionCueCount(text string) int {
	seen := make(map[string]bool)
	for _, cue := range newsEventActionCues(text) {
		seen[strings.ToLower(strings.TrimSpace(cue))] = true
	}
	return len(seen)
}

func newsEventActionCueOccurrenceCount(text string) int {
	return len(newsEventActionCues(text))
}

func newsEventActionCues(text string) []string {
	matches := newsEventActionCuePattern.FindAllStringIndex(text, -1)
	cues := make([]string, 0, len(matches))
	for _, match := range matches {
		if len(match) < 2 || match[0] < 0 || match[1] > len(text) {
			continue
		}
		cue := text[match[0]:match[1]]
		if cue == "访问" && newsVisitIsMetricOrTechnicalUsage(text[match[1]:]) {
			continue
		}
		cues = append(cues, cue)
	}
	return cues
}

// "访问" is an event action in executive/diplomatic news, but the same token
// also begins common metrics and technical nouns. Those nouns must not turn a
// feature report, interview, or traffic metric into a second bundled event.
func newsVisitIsMetricOrTechnicalUsage(suffix string) bool {
	suffix = strings.TrimLeft(suffix, " \t*_`")
	return containsAnyFoldPrefix(suffix,
		"量", "率", "数", "数据", "次数", "流量", "页面", "网页", "网站", "链接", "请求", "权限", "用户",
	)
}

func containsAnyFoldPrefix(text string, candidates ...string) bool {
	lower := strings.ToLower(text)
	for _, candidate := range candidates {
		if strings.HasPrefix(lower, strings.ToLower(candidate)) {
			return true
		}
	}
	return false
}

// ResearchDeliveryBundledItemNumbers returns a compact patch target only when
// every remaining contract problem is a specific bundled numbered item. Other
// delivery failures still use the full bounded rewrite path.
func ResearchDeliveryBundledItemNumbers(issues []string) ([]int, bool) {
	if len(issues) == 0 {
		return nil, false
	}
	seen := make(map[int]bool)
	items := make([]int, 0, len(issues))
	for _, issue := range issues {
		match := newsBundledIssuePattern.FindStringSubmatch(strings.TrimSpace(issue))
		if len(match) < 2 {
			return nil, false
		}
		item := parseRequestedCount(match[1])
		if item <= 0 || seen[item] {
			continue
		}
		seen[item] = true
		items = append(items, item)
	}
	if len(items) == 0 {
		return nil, false
	}
	sort.Ints(items)
	return items, true
}

func BuildResearchDeliveryItemPatchPrompt(items []int) map[string]interface{} {
	labels := make([]string, 0, len(items))
	for _, item := range items {
		labels = append(labels, fmt.Sprintf("%d", item))
	}
	return agentloop.NewInternalControlMessage("RESEARCH ITEM PATCH", fmt.Sprintf(`[LOCAL DELIVERY PATCH REQUIRED]
The previous draft is otherwise complete. Correct only numbered item(s) %s.

Preserve each item's original main headline event and its existing supported article URL(s). Return only those corrected numbered section(s), using their original numbers and the same required field labels. Each corrected section must describe exactly one underlying event: delete unrelated roundup tails, but never swap in an event already used by another numbered item. Do not repeat any other numbered item, introduction, cross-check note, or management summary. Do not call tools. Start directly with the first requested item number; output no analysis, reasoning, or <think> block. Keep each corrected section concise enough to finish immediately.`, strings.Join(labels, ", ")))
}

func BuildResearchDeliveryItemPatchMessages(existing []map[string]interface{}, input, draft string, items []int) []map[string]interface{} {
	compact := make([]map[string]interface{}, 0, 4)
	for _, message := range existing {
		if role, _ := message["role"].(string); role == "system" {
			compact = append(compact, message)
			break
		}
	}
	compact = append(compact,
		map[string]interface{}{"role": "user", "content": input},
		map[string]interface{}{"role": "assistant", "content": draft},
		BuildResearchDeliveryItemPatchPrompt(items),
	)
	return compact
}

// ApplyResearchDeliveryItemPatch splices only explicitly requested numbered
// sections into a known-complete draft. This avoids asking a model to regenerate
// thousands of already-valid characters to fix one local contract defect.
func ApplyResearchDeliveryItemPatch(input, draft, patch string, items []int) (string, bool) {
	if strings.TrimSpace(draft) == "" || strings.TrimSpace(patch) == "" || len(items) == 0 {
		return draft, false
	}
	if containsAnyFold(patch, "<think", "</think") {
		return draft, false
	}
	contract, ok := CurrentNewsDeliveryContract(input)
	if !ok {
		return draft, false
	}
	patchSections := numberedResearchItemSections(patch)
	draftSections := numberedResearchItemSections(draft)
	patchCounts := make(map[string]int)
	for _, match := range researchNumberedItemPattern.FindAllStringSubmatch(patch, -1) {
		if len(match) > 1 {
			patchCounts[match[1]]++
		}
	}
	targets := make(map[string]bool, len(items))
	for _, item := range items {
		key := fmt.Sprintf("%d", item)
		section := strings.TrimSpace(patchSections[key])
		if section == "" || patchCounts[key] != 1 || countResearchItems(section) != 1 || strings.HasSuffix(section, "…") || strings.HasSuffix(section, "...") {
			return draft, false
		}
		for _, label := range contract.RequiredLabels {
			if !newsAnswerContainsRequiredLabel(section, label) || !currentNewsFieldHasValue(section, label) {
				return draft, false
			}
		}
		if contract.RequiresLinks {
			patchURLs, _ := newsArticleCitationCoverage(section)
			originalURLs, _ := newsArticleCitationCoverage(draftSections[key])
			if !citationURLSetIsNonEmptySubset(patchURLs, originalURLs) {
				return draft, false
			}
		}
		if len(currentNewsBundledEventIssues(section)) > 0 {
			return draft, false
		}
		targets[key] = true
	}

	type replacement struct {
		start   int
		end     int
		content string
	}
	matches := researchNumberedItemPattern.FindAllStringSubmatchIndex(draft, -1)
	replacements := make([]replacement, 0, len(items))
	for index, match := range matches {
		if len(match) < 4 {
			continue
		}
		key := draft[match[2]:match[3]]
		if !targets[key] {
			continue
		}
		end := len(draft)
		if index+1 < len(matches) {
			end = matches[index+1][0]
		}
		end = researchItemSectionEnd(draft, match[0], match[2], end)
		replacements = append(replacements, replacement{
			start:   match[0],
			end:     end,
			content: strings.TrimSpace(patchSections[key]) + "\n\n",
		})
	}
	if len(replacements) != len(targets) {
		return draft, false
	}
	sort.Slice(replacements, func(i, j int) bool { return replacements[i].start > replacements[j].start })
	merged := draft
	for _, replacement := range replacements {
		merged = merged[:replacement.start] + replacement.content + merged[replacement.end:]
	}
	return strings.TrimSpace(merged), true
}

func currentNewsFieldHasValue(section, requested string) bool {
	for _, line := range strings.Split(strings.ReplaceAll(section, "\r\n", "\n"), "\n") {
		separator := strings.IndexAny(line, ":：")
		if separator < 0 || !newsAnswerContainsRequiredLabel(line[:separator], requested) {
			continue
		}
		_, separatorSize := utf8.DecodeRuneInString(line[separator:])
		if strings.Trim(strings.TrimSpace(line[separator+separatorSize:]), "*_`") != "" {
			return true
		}
	}
	return false
}

// currentNewsFreshnessIssues verifies the media-publication field against the
// explicit date requested by the user. Source timestamps are interpreted in
// their stated zone, then compared in Asia/Shanghai. This deliberately allows
// a previous UTC/American calendar date when it crosses midnight in China.
func currentNewsFreshnessIssues(input, answer string) []string {
	contract, ok := CurrentNewsDeliveryContract(input)
	if !ok || !researchContractRequiresLabel(contract, "媒体发布时间") {
		return nil
	}
	target, ok := RequestedCurrentNewsLocalDate(input)
	if !ok {
		return nil
	}
	sections := numberedResearchItemSections(answer)
	issues := make([]string, 0, 2)
	for item := 1; item <= contract.ItemCount; item++ {
		section := sections[strconv.Itoa(item)]
		if section == "" {
			continue
		}
		values := currentNewsFieldValues(section, "媒体发布时间")
		if len(values) == 0 {
			// The regular required-field check owns missing/empty labels.
			continue
		}
		parsedAny := false
		fresh := false
		for _, value := range values {
			for _, published := range parseNewsPublicationTimes(value, target) {
				parsedAny = true
				if SameCurrentNewsLocalDate(published, target) {
					fresh = true
					break
				}
			}
			if fresh {
				break
			}
		}
		if fresh {
			continue
		}
		if !parsedAny {
			issues = append(issues, fmt.Sprintf("item %d media publication time cannot be parsed for target local date %s (Asia/Shanghai)", item, target.Format("2006-01-02")))
			continue
		}
		issues = append(issues, fmt.Sprintf("item %d media publication time does not resolve to target local date %s (Asia/Shanghai)", item, target.Format("2006-01-02")))
	}
	return issues
}

func researchContractRequiresLabel(contract researchDeliveryContract, requested string) bool {
	for _, label := range contract.RequiredLabels {
		if label == requested {
			return true
		}
	}
	return false
}

func currentNewsFieldValues(section, requested string) []string {
	values := make([]string, 0, 2)
	for _, line := range strings.Split(strings.ReplaceAll(section, "\r\n", "\n"), "\n") {
		separator := strings.IndexAny(line, ":：")
		if separator < 0 || !newsAnswerContainsRequiredLabel(line[:separator], requested) {
			continue
		}
		_, separatorSize := utf8.DecodeRuneInString(line[separator:])
		value := strings.Trim(strings.TrimSpace(line[separator+separatorSize:]), "*_`")
		if value != "" {
			values = append(values, value)
		}
	}
	return values
}

func parseNewsPublicationTimes(value string, target time.Time) []time.Time {
	normalized := strings.NewReplacer(
		"（", " ", "）", " ", "(", " ", ")", " ",
		"美国东部时间", " ", "美东时间", " ",
		"协调世界时", " UTC ",
	).Replace(value)
	matches := newsPublicationTimePattern.FindAllStringSubmatch(normalized, -1)
	parsed := make([]time.Time, 0, len(matches))
	for _, match := range matches {
		if published, ok := parseNewsPublicationTimeMatch(match); ok {
			parsed = append(parsed, published)
		}
	}
	// A brief whose heading establishes the requested year commonly writes
	// compact local timestamps such as "07-11 09:24". They are unambiguous in
	// that context and should not fail merely because the repeated year is
	// omitted. Only use this fallback when no full-year timestamp was present,
	// so a stated 2025 date cannot be silently reinterpreted as 2026.
	if len(parsed) == 0 {
		for _, match := range newsPublicationMonthDay.FindAllStringSubmatch(normalized, -1) {
			if published, ok := parseNewsPublicationMonthDayMatch(match, target.Year()); ok {
				parsed = append(parsed, published)
			}
		}
	}
	return parsed
}

func parseNewsPublicationMonthDayMatch(match []string, year int) (time.Time, bool) {
	if len(match) < 7 || year < 2000 {
		return time.Time{}, false
	}
	month, monthErr := strconv.Atoi(match[1])
	day, dayErr := strconv.Atoi(match[2])
	hour, hourErr := strconv.Atoi(match[3])
	minute, minuteErr := strconv.Atoi(match[4])
	if monthErr != nil || dayErr != nil || hourErr != nil || minuteErr != nil {
		return time.Time{}, false
	}
	second := 0
	if match[5] != "" {
		var err error
		second, err = strconv.Atoi(match[5])
		if err != nil {
			return time.Time{}, false
		}
	}
	zone := normalizeNewsPublicationZone(match[6])
	canonical := fmt.Sprintf("%04d-%02d-%02d %02d:%02d:%02d %s", year, month, day, hour, minute, second, zone)
	return ParseCurrentNewsPublishedTime(canonical)
}

func parseNewsPublicationTimeMatch(match []string) (time.Time, bool) {
	if len(match) < 8 {
		return time.Time{}, false
	}
	year, yearErr := strconv.Atoi(match[1])
	month, monthErr := strconv.Atoi(match[2])
	day, dayErr := strconv.Atoi(match[3])
	if yearErr != nil || monthErr != nil || dayErr != nil {
		return time.Time{}, false
	}
	hour, minute, second := 0, 0, 0
	var err error
	if match[4] != "" {
		hour, err = strconv.Atoi(match[4])
		if err != nil {
			return time.Time{}, false
		}
	}
	if match[5] != "" {
		minute, err = strconv.Atoi(match[5])
		if err != nil {
			return time.Time{}, false
		}
	}
	if match[6] != "" {
		second, err = strconv.Atoi(match[6])
		if err != nil {
			return time.Time{}, false
		}
	}
	zone := normalizeNewsPublicationZone(match[7])
	canonical := fmt.Sprintf("%04d-%02d-%02d %02d:%02d:%02d %s", year, month, day, hour, minute, second, zone)
	return ParseCurrentNewsPublishedTime(canonical)
}

func normalizeNewsPublicationZone(raw string) string {
	zone := strings.TrimSpace(raw)
	switch strings.ToUpper(zone) {
	case "", "ASIA/SHANGHAI", "北京时间", "中国标准时间":
		return "+0800"
	case "Z":
		return "+0000"
	}
	if len(zone) == 6 && (zone[0] == '+' || zone[0] == '-') && zone[3] == ':' {
		return zone[:3] + zone[4:]
	}
	return zone
}

func citationURLSetIsNonEmptySubset(candidates, allowed []string) bool {
	if len(candidates) == 0 || len(allowed) == 0 {
		return false
	}
	allowedKeys := make(map[string]bool, len(allowed))
	for _, rawURL := range allowed {
		if key := CanonicalCitationURLKey(rawURL); key != "" {
			allowedKeys[key] = true
		}
	}
	for _, rawURL := range candidates {
		key := CanonicalCitationURLKey(rawURL)
		if key == "" || !allowedKeys[key] {
			return false
		}
	}
	return true
}

func currentNewsCoreFactLine(section string) string {
	for _, line := range strings.Split(strings.ReplaceAll(section, "\r\n", "\n"), "\n") {
		if containsAnyFold(line, "核心事实", "关键事实", "core fact", "📌") {
			return strings.TrimSpace(line)
		}
	}
	return ""
}

func normalizeCurrentNewsItemMarkers(answer string) (string, int) {
	matches := newsCircledItemPattern.FindAllStringSubmatchIndex(answer, -1)
	changed := 0
	for index := len(matches) - 1; index >= 0; index-- {
		match := matches[index]
		if len(match) < 6 || match[4] < 0 || match[5] > len(answer) {
			continue
		}
		marker := []rune(answer[match[4]:match[5]])
		if len(marker) != 1 {
			continue
		}
		number := 0
		for item, candidate := range []rune("①②③④⑤⑥⑦⑧⑨⑩⑪⑫⑬⑭⑮⑯⑰⑱⑲⑳") {
			if marker[0] == candidate {
				number = item + 1
				break
			}
		}
		if number == 0 {
			continue
		}
		answer = answer[:match[4]] + strconv.Itoa(number) + "." + answer[match[5]:]
		changed++
	}
	return answer, changed
}

func countResearchItems(answer string) int {
	return len(numberedResearchItemSections(answer))
}

func numberedResearchArticleURLs(sections map[string]string, itemCount int) []string {
	seen := make(map[string]bool)
	articleURLs := make([]string, 0, itemCount)
	for item := 1; item <= itemCount; item++ {
		urls, _ := newsArticleCitationCoverage(sections[strconv.Itoa(item)])
		for _, rawURL := range urls {
			key := CanonicalCitationURLKey(rawURL)
			if key == "" || seen[key] {
				continue
			}
			seen[key] = true
			articleURLs = append(articleURLs, rawURL)
		}
	}
	return articleURLs
}

func numberedResearchItemSections(answer string) map[string]string {
	sections := make(map[string]string)
	matches := researchNumberedItemPattern.FindAllStringSubmatchIndex(answer, -1)
	for index, match := range matches {
		if len(match) < 4 {
			continue
		}
		item := answer[match[2]:match[3]]
		if _, exists := sections[item]; exists {
			continue
		}
		end := len(answer)
		if index+1 < len(matches) {
			end = matches[index+1][0]
		}
		end = researchItemSectionEnd(answer, match[0], match[2], end)
		sections[item] = answer[match[0]:end]
	}
	return sections
}

// researchItemSectionEnd keeps a numbered item's subheadings, but stops at a
// later peer or parent heading such as "管理者摘要" or "来源". Without this,
// the final numbered item can borrow fields, links, or cross-references from
// the summary and incorrectly pass delivery validation.
func researchItemSectionEnd(answer string, start, numberPosition, defaultEnd int) int {
	if start < 0 || numberPosition < start || defaultEnd <= numberPosition || defaultEnd > len(answer) {
		return defaultEnd
	}
	headingLineStart := start
	if relative := strings.LastIndexByte(answer[start:numberPosition], '\n'); relative >= 0 {
		headingLineStart = start + relative + 1
	}
	firstLineEnd := strings.IndexByte(answer[headingLineStart:defaultEnd], '\n')
	if firstLineEnd < 0 {
		return defaultEnd
	}
	firstLine := strings.TrimSpace(answer[headingLineStart : headingLineStart+firstLineEnd])
	itemHeadingLevel := 0
	for itemHeadingLevel < len(firstLine) && firstLine[itemHeadingLevel] == '#' {
		itemHeadingLevel++
	}
	for lineStart := headingLineStart + firstLineEnd + 1; lineStart < defaultEnd; {
		lineEnd := strings.IndexByte(answer[lineStart:defaultEnd], '\n')
		if lineEnd < 0 {
			lineEnd = defaultEnd
		} else {
			lineEnd += lineStart
		}
		line := strings.TrimSpace(answer[lineStart:lineEnd])
		if _, _, ok := newsSummaryMarkerInLine(line, true); ok {
			return lineStart
		}
		if _, _, ok := newsSummaryMarkerInLine(line, false); ok {
			return lineStart
		}
		level := 0
		for level < len(line) && line[level] == '#' {
			level++
		}
		if level > 0 && currentNewsDetachedSectionHeading(line[level:]) {
			return lineStart
		}
		if level > 0 && (itemHeadingLevel == 0 || level <= itemHeadingLevel) {
			return lineStart
		}
		if lineEnd >= defaultEnd {
			break
		}
		lineStart = lineEnd + 1
	}
	return defaultEnd
}

func currentNewsDetachedSectionHeading(heading string) bool {
	heading = strings.TrimSpace(strings.Trim(heading, "*_`：:[]【】()（）"))
	return containsAnyFold(heading,
		"交叉核对", "交叉验证", "二次核对", "复核附录", "来源附录", "来源汇总",
	) || strings.HasPrefix(heading, "附录")
}

func newsAnswerContainsRequiredLabel(answer, requested string) bool {
	switch requested {
	case "事件发生时间":
		return containsAnyFold(answer, "事件发生时间", "发生时间", "事件时间", "事发时间", "事件日期", "event time")
	case "媒体发布时间":
		return containsAnyFold(answer, "媒体发布时间", "发布时间", "发布于", "报道时间", "刊发时间", "publication time", "published at")
	case "核心事实":
		return containsAnyFold(answer, "核心事实", "事实", "关键事实", "core fact")
	case "为什么重要":
		return containsAnyFold(answer, "为什么重要", "重要性", "影响", "why it matters", "significance")
	default:
		return strings.Contains(answer, requested)
	}
}

func containsAnyFold(text string, candidates ...string) bool {
	lower := strings.ToLower(text)
	for _, candidate := range candidates {
		if strings.Contains(lower, strings.ToLower(candidate)) {
			return true
		}
	}
	return false
}

func newsSummaryMarkerInLine(line string, managementOnly bool) (int, int, bool) {
	markers := []string{"管理者摘要"}
	if !managementOnly {
		markers = []string{"摘要"}
	}
	for _, markerText := range markers {
		marker := strings.Index(line, markerText)
		if marker < 0 {
			continue
		}
		prefix := strings.TrimSpace(line[:marker])
		prefix = strings.Trim(prefix, "#*>-—:：|[]【】()（）·•")
		prefix = strings.TrimSpace(prefix)
		if strings.IndexFunc(prefix, func(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }) >= 0 && !newsSummaryLeadingLimit.MatchString(prefix) && !newsSummaryAllowedPrefix.MatchString(prefix) {
			continue
		}
		after := line[marker+len(markerText):]
		after = strings.TrimLeft(after, "*_`")
		if after != "" {
			first, _ := utf8.DecodeRuneInString(after)
			if !unicode.IsSpace(first) && !strings.ContainsRune(":：(（[【<≤-—", first) {
				continue
			}
		}
		return marker, len(markerText), true
	}
	return 0, 0, false
}

func NormalizeCurrentNewsFieldLabels(input, answer string) (string, int) {
	contract, ok := CurrentNewsDeliveryContract(input)
	if !ok || strings.TrimSpace(answer) == "" {
		return answer, 0
	}
	normalized, _ := normalizeCurrentNewsItemMarkers(answer)
	normalized = newsCoreEmojiPattern.ReplaceAllString(normalized, `${1}**核心事实**：`)
	normalized = newsImportanceEmojiPattern.ReplaceAllString(normalized, `${1}**为什么重要**：`)
	normalized = newsMediaEmojiPattern.ReplaceAllString(normalized, `${1}**媒体发布时间**：`)
	normalized = newsEventEmojiPattern.ReplaceAllString(normalized, `${1}**事件发生时间**：`)

	type insertion struct {
		position int
		text     string
	}
	insertions := make([]insertion, 0, contract.ItemCount)
	seen := make(map[int]bool)
	matches := researchNumberedItemPattern.FindAllStringSubmatchIndex(normalized, -1)
	for index, match := range matches {
		if len(match) < 4 {
			continue
		}
		item := parseRequestedCount(normalized[match[2]:match[3]])
		if item < 1 || item > contract.ItemCount || seen[item] {
			continue
		}
		seen[item] = true
		end := len(normalized)
		if index+1 < len(matches) {
			end = matches[index+1][0]
		}
		end = researchItemSectionEnd(normalized, match[0], match[2], end)
		section := normalized[match[0]:end]
		if newsAnswerContainsRequiredLabel(section, "事件发生时间") {
			continue
		}
		headingEnd := strings.IndexByte(section, '\n')
		if headingEnd < 0 {
			headingEnd = len(section)
		}
		insertions = append(insertions, insertion{
			position: match[0] + headingEnd,
			text:     "\n- **事件发生时间**：不详（当前证据仅提供媒体发布时间）",
		})
	}
	for index := len(insertions) - 1; index >= 0; index-- {
		item := insertions[index]
		normalized = normalized[:item.position] + item.text + normalized[item.position:]
	}
	return normalized, len(insertions)
}

func newsSummaryVisibleCharCount(answer string) (int, bool) {
	lines := strings.Split(strings.ReplaceAll(answer, "\r\n", "\n"), "\n")
	summaryLine := -1
	summaryMarker := -1
	summaryMarkerLen := 0
	for index, line := range lines {
		if marker, markerLen, ok := newsSummaryMarkerInLine(line, true); ok {
			summaryLine, summaryMarker, summaryMarkerLen = index, marker, markerLen
			break
		}
	}
	if summaryLine < 0 {
		for index, line := range lines {
			if marker, markerLen, ok := newsSummaryMarkerInLine(line, false); ok {
				summaryLine = index
				summaryMarker = marker
				summaryMarkerLen = markerLen
				break
			}
		}
	}
	if summaryLine < 0 {
		return 0, false
	}
	line := lines[summaryLine]
	index := summaryLine
	marker := summaryMarker
	markerLen := summaryMarkerLen
	{
		body := make([]string, 0, len(lines)-index)
		if inline := newsSummaryPrefixPattern.ReplaceAllString(strings.TrimSpace(line[marker+markerLen:]), ""); inline != "" {
			body = append(body, inline)
		}
		for _, following := range lines[index+1:] {
			trimmed := strings.TrimSpace(following)
			if strings.HasPrefix(trimmed, "#") {
				break
			}
			if trimmed != "" && !strings.HasPrefix(trimmed, "#") {
				body = append(body, trimmed)
			}
		}
		if len(body) == 0 {
			return 0, false
		}
		visible := newsSummaryFormatPattern.ReplaceAllString(strings.Join(body, ""), "")
		if visible == "" {
			return 0, false
		}
		return len([]rune(visible)), true
	}
}

func EnforceCurrentNewsSummaryLimit(input, answer string) (string, bool, int, int) {
	contract, ok := CurrentNewsDeliveryContract(input)
	if !ok || contract.SummaryMaxChars <= 0 {
		return answer, false, 0, 0
	}
	before, present := newsSummaryVisibleCharCount(answer)
	if !present {
		return answer, false, before, before
	}
	originalBefore := before
	start, end, found := newsSummaryBodyRange(answer)
	if !found || start >= end {
		return answer, false, before, before
	}
	if completed, changed := completeDanglingCurrentNewsSummary(answer[start:end]); changed {
		answer = replaceCurrentNewsSummaryBody(answer, start, end, completed)
		before, _ = newsSummaryVisibleCharCount(answer)
		if before <= contract.SummaryMaxChars {
			return answer, true, originalBefore, before
		}
		start, end, found = newsSummaryBodyRange(answer)
		if !found || start >= end {
			return answer, false, originalBefore, before
		}
	}
	if before <= contract.SummaryMaxChars {
		return answer, false, before, before
	}
	originalBody := answer[start:end]
	compacted := compactNewsSummaryText(originalBody, contract.SummaryMaxChars)
	if compacted == "" {
		return answer, false, originalBefore, before
	}
	normalized := replaceCurrentNewsSummaryBody(answer, start, end, compacted)
	after, _ := newsSummaryVisibleCharCount(normalized)
	if after > contract.SummaryMaxChars {
		return answer, false, originalBefore, before
	}
	return normalized, true, originalBefore, after
}

func replaceCurrentNewsSummaryBody(answer string, start, end int, replacement string) string {
	originalBody := answer[start:end]
	trimmedBody := strings.TrimRight(originalBody, " \t\r\n")
	separator := originalBody[len(trimmedBody):]
	if separator == "" && end < len(answer) {
		separator = "\n\n"
	}
	return answer[:start] + strings.TrimSpace(replacement) + separator + answer[end:]
}

func currentNewsSummaryEndsIncomplete(answer string) bool {
	start, end, found := newsSummaryBodyRange(answer)
	if !found || start >= end {
		return false
	}
	probe := strings.TrimSpace(strings.TrimRight(answer[start:end], "*_`"))
	return strings.HasSuffix(probe, "…") || strings.HasSuffix(probe, "...")
}

func currentNewsSummaryDelimitersBalanced(answer string) bool {
	start, end, found := newsSummaryBodyRange(answer)
	if !found || start >= end {
		return true
	}
	stack := make([]rune, 0, 4)
	pairs := map[rune]rune{')': '(', '）': '（', ']': '[', '】': '【', '}': '{', '》': '《', '”': '“', '’': '‘'}
	for _, r := range answer[start:end] {
		switch r {
		case '(', '（', '[', '【', '{', '《', '“', '‘':
			stack = append(stack, r)
		case ')', '）', ']', '】', '}', '》':
			if len(stack) == 0 || stack[len(stack)-1] != pairs[r] {
				return false
			}
			stack = stack[:len(stack)-1]
		}
	}
	return len(stack) == 0
}

// completeDanglingCurrentNewsSummary removes a model-authored trailing fragment
// only when an earlier complete clause is available. A semicolon becomes a full
// stop, and an advertised count is neutralized because fewer clauses may remain.
func completeDanglingCurrentNewsSummary(body string) (string, bool) {
	probe := strings.TrimSpace(strings.TrimRight(body, "*_`"))
	core := probe
	if strings.HasSuffix(core, "…") {
		core = strings.TrimSpace(strings.TrimRight(core, "…"))
	} else if strings.HasSuffix(core, "...") {
		core = strings.TrimSpace(strings.TrimSuffix(core, "..."))
	} else {
		return body, false
	}
	boundary := strings.LastIndexAny(core, "。！？!?；;")
	if boundary <= 0 {
		return body, false
	}
	r, size := utf8.DecodeRuneInString(core[boundary:])
	completed := strings.TrimSpace(core[:boundary+size])
	if r == '；' || r == ';' {
		completed = strings.TrimRight(completed, "；;") + "。"
	}
	completed = strings.NewReplacer("**", "", "__", "", "`", "").Replace(completed)
	completed = newsSummaryDeclaredCount.ReplaceAllString(completed, `${1}重点：`)
	return completed, completed != ""
}

func newsSummaryBodyRange(answer string) (int, int, bool) {
	type markerLocation struct {
		lineStart int
		lineEnd   int
		marker    int
		markerLen int
	}
	find := func(managementOnly bool) (markerLocation, bool) {
		for lineStart := 0; lineStart <= len(answer); {
			lineEnd := strings.IndexByte(answer[lineStart:], '\n')
			if lineEnd < 0 {
				lineEnd = len(answer)
			} else {
				lineEnd += lineStart
			}
			line := answer[lineStart:lineEnd]
			if marker, markerLen, ok := newsSummaryMarkerInLine(line, managementOnly); ok {
				return markerLocation{lineStart: lineStart, lineEnd: lineEnd, marker: marker, markerLen: markerLen}, true
			}
			if lineEnd >= len(answer) {
				break
			}
			lineStart = lineEnd + 1
		}
		return markerLocation{}, false
	}
	location, ok := find(true)
	if !ok {
		location, ok = find(false)
	}
	if !ok {
		return 0, 0, false
	}
	line := answer[location.lineStart:location.lineEnd]
	inlineRaw := line[location.marker+location.markerLen:]
	inline := newsSummaryPrefixPattern.ReplaceAllString(strings.TrimSpace(inlineRaw), "")
	bodyStart := location.lineEnd
	if inline != "" {
		if relative := strings.Index(inlineRaw, inline); relative >= 0 {
			bodyStart = location.lineStart + location.marker + location.markerLen + relative
		}
	} else if bodyStart < len(answer) && answer[bodyStart] == '\n' {
		bodyStart++
	}
	for bodyStart < len(answer) && (answer[bodyStart] == '\r' || answer[bodyStart] == '\n' || answer[bodyStart] == ' ' || answer[bodyStart] == '\t') {
		bodyStart++
	}
	bodyEnd := len(answer)
	for lineStart := bodyStart; lineStart < len(answer); {
		lineEnd := strings.IndexByte(answer[lineStart:], '\n')
		if lineEnd < 0 {
			lineEnd = len(answer)
		} else {
			lineEnd += lineStart
		}
		if strings.HasPrefix(strings.TrimSpace(answer[lineStart:lineEnd]), "#") {
			bodyEnd = lineStart
			break
		}
		if lineEnd >= len(answer) {
			break
		}
		lineStart = lineEnd + 1
	}
	return bodyStart, bodyEnd, bodyStart < bodyEnd && strings.TrimSpace(answer[bodyStart:bodyEnd]) != ""
}

func compactNewsSummaryText(body string, maxChars int) string {
	body = strings.TrimSpace(body)
	if body == "" || maxChars <= 0 {
		return ""
	}
	// Item-level citations remain in the brief. Flatten summary-only Markdown
	// before rune truncation so compaction cannot create half a link, URL, or
	// emphasis marker.
	body = newsSummaryMarkdownLink.ReplaceAllString(body, `$1`)
	body = securitycore.ExternalURLPattern().ReplaceAllString(body, "")
	body = strings.NewReplacer("**", "", "__", "", "`", "").Replace(body)
	body = newsSummaryDeclaredCount.ReplaceAllString(body, `${1}重点：`)
	runes := []rune(body)
	visible := 0
	cut := len(runes)
	for index, r := range runes {
		if unicode.IsSpace(r) || r == '*' || r == '_' || r == '`' {
			continue
		}
		visible++
		if visible == maxChars {
			cut = index + 1
			break
		}
	}
	if visible < maxChars && cut == len(runes) {
		return body
	}
	candidate := runes[:cut]
	minimumBoundary := max(10, maxChars/3)
	visible = 0
	lastSentenceEnd := -1
	lastClauseEnd := -1
	lastSoftClauseEnd := -1
	brackets := make([]rune, 0, 4)
	pairs := map[rune]rune{')': '(', '）': '（', ']': '[', '】': '【', '}': '{', '》': '《', '”': '“', '’': '‘'}
	for index, r := range candidate {
		if !unicode.IsSpace(r) && r != '*' && r != '_' && r != '`' {
			visible++
		}
		switch r {
		case '(', '（', '[', '【', '{', '《', '“', '‘':
			brackets = append(brackets, r)
		case ')', '）', ']', '】', '}', '》':
			if len(brackets) > 0 && brackets[len(brackets)-1] == pairs[r] {
				brackets = brackets[:len(brackets)-1]
			}
		}
		sentenceBoundary := strings.ContainsRune("。！？!?", r)
		if r == '.' {
			previousAlphaNumeric := index > 0 && (unicode.IsLetter(runes[index-1]) || unicode.IsNumber(runes[index-1]))
			nextAlphaNumeric := index+1 < len(runes) && (unicode.IsLetter(runes[index+1]) || unicode.IsNumber(runes[index+1]))
			// A dot between letters/digits belongs to a decimal, acronym, or domain,
			// not to a completed sentence.
			sentenceBoundary = !(previousAlphaNumeric && nextAlphaNumeric)
		}
		if len(brackets) == 0 && visible >= minimumBoundary && sentenceBoundary {
			lastSentenceEnd = index + 1
		}
		if len(brackets) == 0 && visible >= minimumBoundary && strings.ContainsRune("；;", r) {
			lastClauseEnd = index + 1
		}
		if len(brackets) == 0 && visible >= minimumBoundary && strings.ContainsRune("，,", r) {
			lastSoftClauseEnd = index + 1
		}
	}
	if lastSentenceEnd > 0 {
		return strings.TrimSpace(string(candidate[:lastSentenceEnd]))
	}
	if lastClauseEnd > 0 {
		return strings.TrimRight(strings.TrimSpace(string(candidate[:lastClauseEnd])), "；;") + "。"
	}
	if lastSoftClauseEnd > 0 {
		return strings.TrimRight(strings.TrimSpace(string(candidate[:lastSoftClauseEnd])), "，,") + "。"
	}
	// With no safe sentence or top-level clause boundary, character slicing can
	// fabricate a grammatical-looking half-claim. Fail compactly and honestly
	// instead of returning a truncated word, URL, or unmatched parenthesis.
	fallback := "今日重点详见以上新闻条目。"
	if len([]rune(fallback)) > maxChars {
		fallback = "重点见上文。"
	}
	if len([]rune(fallback)) > maxChars {
		return "。"
	}
	return fallback
}

func newsArticleCitationCoverage(answer string) ([]string, map[string]bool) {
	seen := make(map[string]bool)
	publishers := make(map[string]bool)
	articleURLs := make([]string, 0, 12)
	for _, rawURL := range securitycore.ExternalURLPattern().FindAllString(answer, -1) {
		candidate := NormalizeCitationURL(rawURL)
		if !IsArticleCitationURL(candidate) {
			continue
		}
		key := CanonicalCitationURLKey(candidate)
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		articleURLs = append(articleURLs, candidate)
		if publisher := newsPublisherKey(candidate); publisher != "" {
			publishers[publisher] = true
		}
	}
	return articleURLs, publishers
}

func newsPublisherKey(rawURL string) string {
	host := SourceHost(rawURL)
	if host == "" {
		return ""
	}
	if domain, err := publicsuffix.EffectiveTLDPlusOne(host); err == nil && domain != "" {
		return strings.ToLower(domain)
	}
	return host
}

func countCrossCheckedResearchItems(answer string) int {
	checked := make(map[string]bool)
	matches := researchNumberedItemPattern.FindAllStringSubmatchIndex(answer, -1)
	for i, match := range matches {
		if len(match) < 4 {
			continue
		}
		end := len(answer)
		if i+1 < len(matches) {
			end = matches[i+1][0]
		}
		end = researchItemSectionEnd(answer, match[0], match[2], end)
		_, publishers := newsArticleCitationCoverage(answer[match[0]:end])
		if len(publishers) >= 2 {
			checked[answer[match[2]:match[3]]] = true
		}
	}
	for _, line := range strings.Split(answer, "\n") {
		match := researchTableItemPattern.FindStringSubmatch(line)
		if len(match) < 2 {
			continue
		}
		_, publishers := newsArticleCitationCoverage(line)
		if len(publishers) >= 2 {
			checked[match[1]] = true
		}
	}
	return len(checked)
}

func BuildResearchDeliveryRepairPrompt(issues []string) map[string]interface{} {
	return agentloop.NewInternalControlMessage("RESEARCH DELIVERY REPAIR", fmt.Sprintf(`[RESEARCH DELIVERY CONTRACT NOT MET]
The draft cannot be released as a completed current-news brief: %s.

This is the only delivery rewrite. Do not call any tool and do not start another search: use only exact URLs already present in the current tool evidence. Rewrite the full answer, attach the matching article URL to every numbered item, and put two URLs from different publisher domains inside each genuinely cross-checked item. Each numbered item must cover exactly one underlying event: never bundle an unrelated launch, lawsuit, personnel change, product release, enforcement action, financing, or policy into the same slot merely because it happened on the same day or appeared in the same feed. Merge multiple angles of the same event into one item, and replace any bundled or freed slot with one distinct event already present in the evidence. Never use a homepage as event evidence, never count two pages from one publisher as independent, and never write the answer to a file. Preserve conflicts and unknown event times honestly. If the evidence cannot satisfy a field, state that specific gap rather than inventing it. Then return the complete user-facing answer in chat.`, strings.Join(issues, "; ")))
}

func AppendResearchDeliveryFailure(answer string, issues []string) string {
	answer = strings.TrimSpace(answer)
	if len(issues) == 0 {
		issues = []string{"final evidence repair did not complete"}
	}
	gap := "## 未完成的证据要求\n\n- " + strings.Join(issues, "\n- ")
	if answer == "" {
		return gap
	}
	return answer + "\n\n" + gap
}
