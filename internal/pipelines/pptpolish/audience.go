package pptpolish

import (
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

var (
	pptPolishBriefTitlePattern = regexp.MustCompile(`(?im)^#\s+(.+?)\s*$`)
	pptPolishPagePattern       = regexp.MustCompile(`(?im)^###\s+(?:(?:Page|Slide|P|S)\s*|第\s*)?(\d+)\s*(?:页)?\s*(?:[.、:：]\s*)?(?:[—–-]\s*)?(.+?)\s*$`)
	pptPolishFieldPattern      = regexp.MustCompile(`^\s*-\s+\*\*([^*]+)\*\*\s*(?:[:：]\s*(.*))?$`)
	pptPolishListPattern       = regexp.MustCompile(`^\s*(?:[-*+•]\s+|\d+[.)、]\s+)(.+)$`)
	pptGoogleStylePattern      = regexp.MustCompile(`(?i)(?:google|谷歌).{0,16}(?:style|theme|clean|风格|风|样式|清爽|白底)|(?:style|theme|clean|风格|风|样式|清爽|白底).{0,16}(?:google|谷歌)`)
	pptPolishDirectionPattern  = regexp.MustCompile(`(?i)(?:左上|右上|左下|右下|上半|下半|页首|页脚|小字|大字|字号|图表|配图|图标|卡片|chip|双栏|三栏|四栏|四象限|时间轴|流程图|对比表|表头|描边|底色|短横线|横向|纵向|竖排|布局|版式|视觉|每步|每卡|每栏|占位|圆角矩形|\d+\s*行短句|\d+\s*张|\d+\s*个.*(?:场景|takeaway))`)
	pptPolishQuotedTextPattern = regexp.MustCompile(`["“『](.+?)["”』]`)
)

func presentationLimitStrings(values []string, limit int) []string {
	seen := make(map[string]bool)
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		result = append(result, value)
		if limit > 0 && len(result) >= limit {
			break
		}
	}
	return result
}

type pptPolishAudiencePage struct {
	Number  int
	Title   string
	Claim   string
	Support []string
}

// buildPPTPolishFallbackMarkdown converts the internal production brief into
// audience-facing source material for the deterministic PPT fallback. The
// production brief itself must never be rendered as a deck: it contains
// routing metadata, speaker guidance, visual instructions and relaxation
// rules that are useful to the builder but meaningless to the audience.
func buildPPTPolishFallbackMarkdown(brief string) (string, error) {
	brief = strings.ReplaceAll(brief, "\r\n", "\n")
	title := "演示文稿"
	if match := pptPolishBriefTitlePattern.FindStringSubmatch(brief); len(match) == 2 {
		title = cleanPPTPolishAudienceText(match[1])
		title = regexp.MustCompile(`(?i)^PPT\s*制作简报\s*(?:[：:]|[—–-])\s*`).ReplaceAllString(title, "")
		title = regexp.MustCompile(`(?i)\s+(?:PPT\s*)?(?:[·•|｜—–-]\s*)?制作简报\s*$`).ReplaceAllString(title, "")
		if title == "" {
			title = "演示文稿"
		}
	}

	matches := pptPolishPagePattern.FindAllStringSubmatchIndex(brief, -1)
	pages := make([]pptPolishAudiencePage, 0, len(matches))
	for index, match := range matches {
		start := match[1]
		end := len(brief)
		if index+1 < len(matches) {
			end = matches[index+1][0]
		}
		number, _ := strconv.Atoi(brief[match[2]:match[3]])
		page := pptPolishAudiencePage{
			Number: number,
			Title:  cleanPPTPolishAudienceText(brief[match[4]:match[5]]),
		}
		parsePPTPolishAudiencePageFields(brief[start:end], &page)
		if page.Title != "" && (page.Claim != "" || len(page.Support) > 0 || page.Number == 1) {
			pages = append(pages, page)
		}
	}
	if len(pages) < 2 {
		pages = parsePPTPolishAudiencePlanTable(brief)
	}
	if len(pages) < 2 {
		return "", fmt.Errorf("PPT fallback brief has fewer than two usable audience pages in section or table form")
	}
	enrichPPTPolishAudiencePlanSupport(brief, pages)
	sort.SliceStable(pages, func(i, j int) bool { return pages[i].Number < pages[j].Number })

	var out strings.Builder
	out.WriteString("<!-- OPENKITTY_THEME:mckinsey -->\n\n")
	out.WriteString("# " + title + "\n\n")
	for _, page := range pages {
		// Page 1 is the cover specification. The renderer already creates the
		// cover from the document title, so rendering it again would duplicate it.
		if page.Number == 1 {
			continue
		}
		out.WriteString("## " + page.Title + "\n\n")
		if page.Claim != "" {
			out.WriteString("> " + page.Claim + "\n\n")
		}
		for _, item := range presentationLimitStrings(page.Support, 8) {
			out.WriteString("- " + item + "\n")
		}
		out.WriteString("\n")
	}
	markdown := strings.TrimSpace(out.String()) + "\n"
	if violations := pptPolishAudienceContentViolations(markdown); len(violations) > 0 {
		return "", fmt.Errorf("PPT fallback audience source still contains production metadata: %s", strings.Join(violations, ", "))
	}
	return markdown, nil
}

func parsePPTPolishAudiencePlanTable(brief string) []pptPolishAudiencePage {
	lines := strings.Split(strings.ReplaceAll(brief, "\r\n", "\n"), "\n")
	for lineIndex, line := range lines {
		if !strings.Contains(line, "|") {
			continue
		}
		headers := splitPPTPolishMarkdownTableRow(line)
		numberIndex := pptPolishTableHeaderIndex(headers, "#", "序号", "页码", "page", "slide")
		titleIndex := pptPolishTableHeaderIndex(headers, "页面", "页标题", "标题", "page title", "slide title")
		claimIndex := pptPolishTableHeaderIndex(headers, "唯一核心观点", "核心观点", "观点", "结论", "takeaway", "claim")
		supportIndex := pptPolishTableHeaderIndex(headers, "支持内容要点", "支持内容", "内容要点", "要点", "support", "evidence")
		if numberIndex < 0 || titleIndex < 0 || claimIndex < 0 {
			continue
		}

		var pages []pptPolishAudiencePage
		for _, dataLine := range lines[lineIndex+1:] {
			if strings.TrimSpace(dataLine) == "" {
				if len(pages) > 0 {
					break
				}
				continue
			}
			if !strings.Contains(dataLine, "|") {
				if len(pages) > 0 {
					break
				}
				continue
			}
			cells := splitPPTPolishMarkdownTableRow(dataLine)
			if pptPolishMarkdownSeparatorRow(cells) ||
				numberIndex >= len(cells) ||
				titleIndex >= len(cells) ||
				claimIndex >= len(cells) {
				continue
			}
			numberText := cleanPPTPolishAudienceText(cells[numberIndex])
			numberMatch := regexp.MustCompile(`\d+`).FindString(numberText)
			number, err := strconv.Atoi(numberMatch)
			if err != nil || number <= 0 {
				continue
			}
			page := pptPolishAudiencePage{
				Number: number,
				Title:  cleanPPTPolishAudienceText(cells[titleIndex]),
				Claim:  cleanPPTPolishAudienceText(cells[claimIndex]),
			}
			if supportIndex >= 0 && supportIndex < len(cells) {
				support := regexp.MustCompile(`(?i)<br\s*/?>`).ReplaceAllString(cells[supportIndex], "；")
				page.Support = appendPPTPolishAudienceSupport(page.Support, support)
			}
			if page.Title != "" && (page.Claim != "" || len(page.Support) > 0 || page.Number == 1) {
				pages = append(pages, page)
			}
		}
		if len(pages) >= 2 {
			return pages
		}
	}
	return nil
}

func pptPolishAudiencePageCount(brief string) int {
	matches := pptPolishPagePattern.FindAllStringSubmatch(brief, -1)
	if len(matches) >= 2 {
		return len(matches)
	}
	return len(parsePPTPolishAudiencePlanTable(brief))
}

// enrichPPTPolishAudiencePlanSupport merges a separate, numbered per-page
// support section into a compact page-plan table. Models commonly keep the
// table readable by putting evidence in a later "单页支持内容" section. The
// fallback should preserve that audience content without rendering production
// directions such as layout, speaker notes, or image placement.
func enrichPPTPolishAudiencePlanSupport(brief string, pages []pptPolishAudiencePage) {
	if len(pages) == 0 {
		return
	}
	sectionPattern := regexp.MustCompile(`(?im)^##\s+[^\n]*(?:单页|逐页)[^\n]*支持内容[^\n]*\s*$`)
	section := sectionPattern.FindStringIndex(brief)
	if len(section) != 2 {
		return
	}
	body := brief[section[1]:]
	if next := regexp.MustCompile(`(?m)^##\s+`).FindStringIndex(body); len(next) == 2 {
		body = body[:next[0]]
	}
	linePattern := regexp.MustCompile(`(?i)^\s*(?:[-*+]\s*)?\*{0,2}P\s*(\d+)(?:\s+[^*：:\n]+)?\*{0,2}\s*[：:]\s*(.+?)\s*$`)
	byNumber := make(map[int]int, len(pages))
	for index := range pages {
		byNumber[pages[index].Number] = index
	}
	for _, raw := range strings.Split(body, "\n") {
		match := linePattern.FindStringSubmatch(raw)
		if len(match) != 3 {
			continue
		}
		number, err := strconv.Atoi(match[1])
		index, ok := byNumber[number]
		if err != nil || !ok {
			continue
		}
		pages[index].Support = presentationLimitStrings(
			appendPPTPolishAudienceSupport(pages[index].Support, match[2]),
			8,
		)
	}
}

func splitPPTPolishMarkdownTableRow(line string) []string {
	line = strings.TrimSpace(line)
	line = strings.TrimPrefix(line, "|")
	line = strings.TrimSuffix(line, "|")
	raw := strings.Split(line, "|")
	cells := make([]string, 0, len(raw))
	for _, cell := range raw {
		cells = append(cells, cleanPPTPolishAudienceText(cell))
	}
	return cells
}

func pptPolishTableHeaderIndex(headers []string, candidates ...string) int {
	for index, header := range headers {
		normalized := strings.ToLower(cleanPPTPolishAudienceText(header))
		for _, candidate := range candidates {
			candidate = strings.ToLower(strings.TrimSpace(candidate))
			if normalized == candidate || (len([]rune(candidate)) >= 2 && strings.Contains(normalized, candidate)) {
				return index
			}
		}
	}
	return -1
}

func pptPolishMarkdownSeparatorRow(cells []string) bool {
	if len(cells) == 0 {
		return false
	}
	for _, cell := range cells {
		cell = strings.TrimSpace(cell)
		if !regexp.MustCompile(`^:?-{3,}:?$`).MatchString(cell) {
			return false
		}
	}
	return true
}

func selectPPTPolishFallbackTheme(userInput, brief string) string {
	styleText := strings.ToLower(userInput + "\n" + brief)
	if pptGoogleStylePattern.MatchString(styleText) {
		return "google_style"
	}
	for _, keyword := range []string{
		"google_style",
		"google style",
		"google-style",
		"google clean",
		"谷歌风",
		"谷歌风格",
		"谷歌配色",
		"清爽白底",
		"明亮产品风",
	} {
		if strings.Contains(styleText, keyword) {
			return "google_style"
		}
	}
	return "mckinsey"
}

func applyPPTPolishFallbackTheme(markdown, theme string) string {
	theme = strings.TrimSpace(theme)
	if theme == "" {
		theme = "mckinsey"
	}
	return strings.Replace(
		markdown,
		"<!-- OPENKITTY_THEME:mckinsey -->",
		"<!-- OPENKITTY_THEME:"+theme+" -->",
		1,
	)
}

func pptPolishFallbackThemeLabel(theme string) string {
	if strings.EqualFold(strings.TrimSpace(theme), "google_style") {
		return "Google 清爽商务"
	}
	return "深色咨询商务"
}

func parsePPTPolishAudiencePageFields(block string, page *pptPolishAudiencePage) {
	if page == nil {
		return
	}
	currentField := ""
	for _, raw := range strings.Split(block, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		if strings.Contains(line, "|") {
			cells := splitPPTPolishMarkdownTableRow(line)
			if len(cells) >= 2 && !pptPolishMarkdownSeparatorRow(cells) {
				field := cleanPPTPolishAudienceText(cells[0])
				value := cleanPPTPolishAudienceText(strings.Join(cells[1:], "；"))
				switch {
				case pptPolishAudienceClaimField(field):
					page.Claim = value
					currentField = ""
				case pptPolishAudienceSupportField(field):
					page.Support = appendPPTPolishAudienceSupport(page.Support, value)
					currentField = ""
				}
			}
			continue
		}
		if match := pptPolishFieldPattern.FindStringSubmatch(line); len(match) == 3 {
			currentField = strings.TrimSpace(match[1])
			value := cleanPPTPolishAudienceText(match[2])
			switch {
			case pptPolishAudienceClaimField(currentField):
				page.Claim = value
			case pptPolishAudienceSupportField(currentField):
				page.Support = appendPPTPolishAudienceSupport(page.Support, value)
			default:
				currentField = ""
			}
			continue
		}
		if !pptPolishAudienceSupportField(currentField) {
			continue
		}
		if match := pptPolishListPattern.FindStringSubmatch(line); len(match) == 2 {
			page.Support = appendPPTPolishAudienceSupport(page.Support, match[1])
		}
	}
	page.Support = presentationLimitStrings(page.Support, 8)
}

func pptPolishAudienceClaimField(field string) bool {
	field = strings.ToLower(cleanPPTPolishAudienceText(field))
	for _, candidate := range []string{"唯一核心观点", "核心观点", "观点", "结论", "takeaway", "claim"} {
		if field == candidate {
			return true
		}
	}
	return false
}

func pptPolishAudienceSupportField(field string) bool {
	field = strings.ToLower(cleanPPTPolishAudienceText(field))
	for _, marker := range []string{"支持内容", "结论金句", "横向能力", "里程碑标注"} {
		if strings.Contains(field, marker) {
			return true
		}
	}
	return false
}

func appendPPTPolishAudienceSupport(items []string, value string) []string {
	value = regexp.MustCompile(`(?i)<br\s*/?>`).ReplaceAllString(value, "；")
	if value == "" {
		return items
	}
	// Semicolons in the production brief usually separate audience-facing
	// evidence points. Splitting them produces readable bullets without
	// exposing layout or speaking instructions.
	for _, part := range regexp.MustCompile(`[；;]\s*`).Split(value, -1) {
		part = cleanPPTPolishAudienceSupportText(part)
		if part != "" {
			items = append(items, part)
		}
	}
	return items
}

func cleanPPTPolishAudienceSupportText(value string) string {
	value = cleanPPTPolishAudienceText(value)
	if value == "" {
		return ""
	}
	if separator := strings.IndexAny(value, ":："); separator > 0 {
		prefix := strings.TrimSpace(value[:separator])
		if pptPolishDirectionPattern.MatchString(prefix) {
			separatorWidth := 1
			if strings.HasPrefix(value[separator:], "：") {
				separatorWidth = len("：")
			}
			value = cleanPPTPolishAudienceText(value[separator+separatorWidth:])
		}
	}
	if pptPolishDirectionPattern.MatchString(value) {
		if quoted := pptPolishQuotedTextPattern.FindStringSubmatch(value); len(quoted) == 2 {
			return cleanPPTPolishAudienceText(quoted[1])
		}
		return ""
	}
	return value
}

func cleanPPTPolishAudienceText(value string) string {
	value = strings.TrimSpace(value)
	value = strings.Trim(value, "`")
	value = strings.ReplaceAll(value, "**", "")
	value = strings.ReplaceAll(value, "__", "")
	return strings.Join(strings.Fields(value), " ")
}

// pptPolishAudienceContentViolations finds production-only fields that should
// never be visible in an audience-facing presentation. These markers are
// intentionally workflow-generic rather than tied to a topic or customer.
func pptPolishAudienceContentViolations(text string) []string {
	type marker struct {
		label    string
		pattern  *regexp.Regexp
		critical bool
	}
	markers := []marker{
		{label: "SLOT_ID", pattern: regexp.MustCompile(`(?i)\bSLOT_ID\b`), critical: true},
		{label: "PPT_INPUT_MODE", pattern: regexp.MustCompile(`(?i)\bPPT_INPUT_MODE\b`), critical: true},
		{label: "internal storage path", pattern: regexp.MustCompile(`(?i)\.openkitty[/\\]storage`)},
		{label: "production save path", pattern: regexp.MustCompile(`真实保存路径`), critical: true},
		{label: "downstream production note", pattern: regexp.MustCompile(`下游不提前执行`), critical: true},
		{label: "workflow relaxation rules", pattern: regexp.MustCompile(`放宽路径（结果不足时`)},
		{label: "workflow delivery preview", pattern: regexp.MustCompile(`交付物预告`)},
		{label: "workflow gap check", pattern: regexp.MustCompile(`决定性缺口检查`)},
		{label: "presentation direction label", pattern: regexp.MustCompile(`(?i)(?:左上|右上|左下|右下).{0,6}(?:小字|注|图标)|\d+\s*行短句|(?:图表|配图)\s*[/／]\s*(?:配图|图表)`), critical: true},
		{label: "serialized configuration/action payload", pattern: regexp.MustCompile(`(?is)(?:^|\s)[\[{]\s*['"]?(?:name|owner)['"]?\s*:\s*[^{}\[\]\r\n]{0,180}['"]?(?:section|tagline|action)['"]?\s*:`), critical: true},
		{label: "likely mojibake/garbled text", pattern: regexp.MustCompile(`(?is)(?:�|Ã.|Â.|â(?:€|€™|€“|€”|€¦)|ðŸ|(?:锛|銆|鈥|鈫|鍒|瀹|鍔|绔|璇|鐨|鍙|浠|鏂|缁|鎴|娴|绠|鐢|妫|瑙|纭|杈|寮|鎵|璐).{0,24}(?:锛|銆|鈥|鈫|鍒|瀹|鍔|绔|璇|鐨|鍙|浠|鏂|缁|鎴|娴|绠|鐢|妫|瑙|纭|杈|寮|鎵|璐).{0,24}(?:锛|銆|鈥|鈫|鍒|瀹|鍔|绔|璇|鐨|鍙|浠|鏂|缁|鎴|娴|绠|鐢|妫|瑙|纭|杈|寮|鎵|璐))`), critical: true},
	}
	seen := make(map[string]bool)
	var violations []string
	criticalFound := false
	for _, item := range markers {
		if item.pattern.MatchString(text) && !seen[item.label] {
			seen[item.label] = true
			violations = append(violations, item.label)
			criticalFound = criticalFound || item.critical
		}
	}
	// A single contextual phrase such as a storage path may be legitimate in a
	// technical deck. Block it only when a decisive production marker is
	// present or multiple workflow-only markers occur together.
	if !criticalFound && len(violations) < 2 {
		return nil
	}
	return violations
}

func pptPolishUserVisiblePath(target, workspace string) string {
	target = filepath.Clean(target)
	workspace = filepath.Clean(workspace)
	if relative, err := filepath.Rel(workspace, target); err == nil &&
		relative != "." && relative != ".." &&
		!strings.HasPrefix(relative, ".."+string(filepath.Separator)) &&
		!filepath.IsAbs(relative) {
		return filepath.ToSlash(relative)
	}
	return filepath.ToSlash(filepath.Base(target))
}
