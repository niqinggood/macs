package toolcontract

import (
	"fmt"
	"sort"
	"strings"

	"mac/internal/textutil"
)

// searchTools finds tools matching a keyword in name or description.
func SearchToolSummaries(summaries []map[string]string, query string) string {
	if summaries == nil {
		return "No tools available"
	}
	query = strings.TrimSpace(query)
	if query == "" {
		return "No tools found for empty query. Try a tool name or capability keyword."
	}
	lower := strings.ToLower(query)
	var matches []toolSearchMatch
	for _, s := range summaries {
		name := strings.ToLower(s["name"])
		desc := strings.ToLower(s["desc"])
		aliases := strings.ToLower(s["aliases"])
		tags := strings.ToLower(s["tags"])
		if score := scoreToolSearchMetadata(lower, name, desc, aliases, tags); score > 0 {
			matches = append(matches, toolSearchMatch{
				name:  s["name"],
				desc:  s["desc"],
				score: score,
			})
		}
	}
	if len(matches) == 0 {
		return fmt.Sprintf("No tools found for '%s'. Try different keywords.", query)
	}
	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].score != matches[j].score {
			return matches[i].score > matches[j].score
		}
		return matches[i].name < matches[j].name
	})
	total := len(matches)
	if len(matches) > 10 {
		matches = matches[:10]
	}
	lines := make([]string, 0, len(matches))
	for _, match := range matches {
		lines = append(lines, fmt.Sprintf("- **%s**: %s", match.name, textutil.Truncate(match.desc, 80)))
	}
	shown := ""
	if total > len(matches) {
		shown = fmt.Sprintf("\nShowing top %d of %d matches.", len(matches), total)
	}
	return fmt.Sprintf("Found %d tools matching '%s':\n%s\n\nUse tool_detail(action=get, tool_name=<name>) for full schema.",
		total, query, strings.Join(lines, "\n")+shown)
}

type toolSearchMatch struct {
	name  string
	desc  string
	score int
}

func scoreToolSearch(query, name, desc string) int {
	return scoreToolSearchMetadata(query, name, desc, "", "")
}

func scoreToolSearchMetadata(query, name, desc, aliases, tags string) int {
	if query == name {
		return 1000
	}
	normalizedQuery := normalizeToolSearchText(query)
	normalizedName := normalizeToolSearchText(name)
	normalizedAliases := normalizeToolSearchText(aliases)
	compactQuery := strings.ReplaceAll(normalizedQuery, " ", "")
	compactName := strings.ReplaceAll(normalizedName, " ", "")
	switch {
	case strings.HasPrefix(name, query), strings.HasPrefix(normalizedName, normalizedQuery):
		return 900
	case strings.Contains(name, query), strings.Contains(normalizedName, normalizedQuery):
		return 800
	case compactQuery != "" && strings.Contains(compactName, compactQuery):
		return 700
	case containsToolSearchPhrase(normalizedAliases, normalizedQuery):
		return 600
	case strings.Contains(desc, query):
		return 300
	case strings.Contains(tags, query):
		return 250
	}

	terms := toolSearchTerms(query)
	score := 0
	if len(terms) > 0 {
		allTermsMatched := true
		for _, term := range terms {
			switch {
			case term == name || term == normalizedName:
				score += 90
			case containsToolSearchTerm(normalizedName, term):
				score += 60
			case strings.Contains(compactName, term):
				score += 45
			case containsToolSearchTerm(normalizedAliases, term):
				score += 35
			case strings.Contains(desc, term):
				score += 20
			case strings.Contains(tags, term):
				score += 15
			default:
				allTermsMatched = false
			}
		}
		if allTermsMatched && score > 0 {
			return score
		}
	}

	return scoreToolSearchConcepts(query, normalizedName, desc, normalizedAliases, tags)
}

type toolSearchConcept struct {
	terms []string
}

// These groups bridge common Chinese/English capability vocabulary. They are
// deliberately about capabilities (not filler words such as "tool" or
// "search"), so a query like "查找 Word 模板填写工具" still matches a tool
// advertised in English as "fill DOCX templates".
var toolSearchConcepts = []toolSearchConcept{
	{terms: []string{"word", "docx", "document", "文档", "文书", "报告", "公文"}},
	{terms: []string{"excel", "xlsx", "spreadsheet", "workbook", "电子表格", "工作簿"}},
	{terms: []string{"powerpoint", "ppt", "pptx", "presentation", "slides", "幻灯片", "演示文稿"}},
	{terms: []string{"pdf", "便携式文档"}},
	{terms: []string{"template", "模板", "模版"}},
	{terms: []string{"fill", "filling", "populate", "replace", "填写", "填充", "替换"}},
	{terms: []string{"table", "form", "表格", "表单"}},
	{terms: []string{"placeholder", "占位符", "占位"}},
	{terms: []string{"underline", "underlined", "下划线"}},
	{terms: []string{"browser", "website", "webpage", "网页", "网站", "浏览器"}},
	{terms: []string{"image", "picture", "photo", "图片", "图像", "照片"}},
	{terms: []string{"video", "视频"}},
	{terms: []string{"database", "sql", "数据库"}},
	{terms: []string{"email", "mail", "邮件", "邮箱"}},
	{terms: []string{"calendar", "schedule", "日历", "日程"}},
	{terms: []string{"memory", "preference", "记忆", "偏好"}},
	{terms: []string{"terminal", "shell", "command", "命令行", "终端"}},
	{terms: []string{"tool_search", "search_tools", "tool discovery", "工具搜索", "工具查找", "能力发现"}},
}

func scoreToolSearchConcepts(query, name, desc, aliases, tags string) int {
	query = strings.ToLower(query)
	matchedConcepts := 0
	score := 0
	for _, concept := range toolSearchConcepts {
		if !containsAnyToolSearchAlias(query, concept.terms) {
			continue
		}
		matchedConcepts++
		switch {
		case containsAnyToolSearchAlias(name, concept.terms):
			score += 70
		case containsAnyToolSearchAlias(aliases, concept.terms):
			score += 55
		case containsAnyToolSearchAlias(tags, concept.terms):
			score += 40
		case containsAnyToolSearchAlias(desc, concept.terms):
			score += 30
		default:
			return 0
		}
	}
	if matchedConcepts == 0 {
		return 0
	}
	return score
}

func containsAnyToolSearchAlias(text string, aliases []string) bool {
	for _, alias := range aliases {
		if containsToolSearchAlias(text, alias) {
			return true
		}
	}
	return false
}

func containsToolSearchAlias(text, alias string) bool {
	text = strings.ToLower(text)
	alias = strings.ToLower(strings.TrimSpace(alias))
	if alias == "" {
		return false
	}
	if hasNonASCII(alias) || strings.Contains(alias, " ") || strings.Contains(alias, "_") {
		return strings.Contains(text, alias)
	}
	for offset := 0; offset <= len(text)-len(alias); {
		relative := strings.Index(text[offset:], alias)
		if relative < 0 {
			return false
		}
		start := offset + relative
		end := start + len(alias)
		beforeBoundary := start == 0 || !isToolSearchASCIIWordByte(text[start-1])
		afterBoundary := end == len(text) || !isToolSearchASCIIWordByte(text[end])
		if beforeBoundary && afterBoundary {
			return true
		}
		offset = start + 1
	}
	return false
}

func isToolSearchASCIIWordByte(value byte) bool {
	return value >= 'a' && value <= 'z' || value >= '0' && value <= '9' || value == '_'
}

func hasNonASCII(value string) bool {
	for _, r := range value {
		if r > 127 {
			return true
		}
	}
	return false
}

func containsToolSearchPhrase(text, phrase string) bool {
	if strings.TrimSpace(phrase) == "" {
		return false
	}
	return strings.Contains(text, phrase)
}

func normalizeToolSearchText(s string) string {
	return strings.Join(toolSearchTerms(s), " ")
}

func toolSearchTerms(s string) []string {
	return strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		switch r {
		case ' ', '\t', '\n', '\r', '_', '-', '.', '/', '\\', ':':
			return true
		default:
			return false
		}
	})
}

func containsToolSearchTerm(text, term string) bool {
	for _, part := range strings.Fields(text) {
		if part == term || strings.Contains(part, term) {
			return true
		}
	}
	return false
}
