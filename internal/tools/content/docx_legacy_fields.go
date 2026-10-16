package content

import (
	"bytes"
	"encoding/xml"
	"html"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

var (
	wordSimpleFieldPattern     = regexp.MustCompile(`(?is)<w:fldSimple\b[^>]*>.*?</w:fldSimple>`)
	wordFieldCharPattern       = regexp.MustCompile(`(?is)<w:fldChar\b[^>]*>`)
	wordInstructionTextPattern = regexp.MustCompile(`(?is)<w:instrText\b[^>]*>(.*?)</w:instrText>`)
	wordMergeFieldPattern      = regexp.MustCompile(`(?i)\bMERGEFIELD\s+(?:"([^"]+)"|'([^']+)'|([^\s\\]+))`)
	wordRunPattern             = regexp.MustCompile(`(?is)<w:r(?:\s[^>]*)?>.*?</w:r>`)
	wordBookmarkStartPattern   = regexp.MustCompile(`(?is)<w:bookmarkStart\b[^>]*>`)
	wordBookmarkEndPattern     = regexp.MustCompile(`(?is)<w:bookmarkEnd\b[^>]*>`)
	wordAttributeDoublePattern = regexp.MustCompile(`(?is)\b(?:w:)?([A-Za-z][A-Za-z0-9]*):?([A-Za-z][A-Za-z0-9]*)?\s*=\s*"([^"]*)"`)
	wordAttributeSinglePattern = regexp.MustCompile(`(?is)\b(?:w:)?([A-Za-z][A-Za-z0-9]*):?([A-Za-z][A-Za-z0-9]*)?\s*=\s*'([^']*)'`)
	wordCheckboxPattern        = regexp.MustCompile(`(?is)<w14:checkbox\b`)
	wordCheckedValuePattern    = regexp.MustCompile(`(?is)(<w14:checked\b[^>]*\bw14:val\s*=\s*")([^"]*)(")`)
	wordCheckedStatePattern    = regexp.MustCompile(`(?is)<w14:checkedState\b[^>]*\bw14:val\s*=\s*"([^"]+)"`)
	wordUncheckedStatePattern  = regexp.MustCompile(`(?is)<w14:uncheckedState\b[^>]*\bw14:val\s*=\s*"([^"]+)"`)
	wordSymbolCharacterPattern = regexp.MustCompile(`(?is)(<w:sym\b[^>]*\bw:char\s*=\s*")[^"]*(")`)
)

type wordTemplateField struct {
	key       string
	context   string
	fieldType string
	fillable  bool
}

type wordComplexField struct {
	key         string
	context     string
	fieldStart  int
	fieldEnd    int
	resultStart int
	resultEnd   int
}

type wordBookmarkField struct {
	key       string
	context   string
	bodyStart int
	bodyEnd   int
	inline    bool
}

func inspectWordMergeFields(fragment string) []wordTemplateField {
	fields := make([]wordTemplateField, 0)
	for _, block := range wordSimpleFieldPattern.FindAllString(fragment, -1) {
		key := mergeFieldKey(wordAttribute(block, "instr"))
		if key == "" {
			continue
		}
		fields = append(fields, wordTemplateField{key: key, context: compactDocxFieldContext(wordNodesText(parseWordTextNodes(block))), fieldType: "merge_field"})
	}
	for _, field := range complexWordFields(fragment) {
		fields = append(fields, wordTemplateField{key: field.key, context: field.context, fieldType: "merge_field"})
	}
	return uniqueWordTemplateFields(fields)
}

func inspectWordBookmarks(fragment string) []wordTemplateField {
	fields := make([]wordTemplateField, 0)
	for _, bookmark := range wordBookmarkFields(fragment) {
		fields = append(fields, wordTemplateField{key: bookmark.key, context: bookmark.context, fieldType: "bookmark", fillable: bookmark.inline})
	}
	return uniqueWordTemplateFields(fields)
}

func uniqueWordTemplateFields(fields []wordTemplateField) []wordTemplateField {
	seen := make(map[string]bool, len(fields))
	result := make([]wordTemplateField, 0, len(fields))
	for _, field := range fields {
		identity := strings.ToLower(strings.TrimSpace(field.fieldType)) + "\x00" + strings.ToLower(strings.TrimSpace(field.key))
		if field.key == "" || seen[identity] {
			continue
		}
		seen[identity] = true
		result = append(result, field)
	}
	return result
}

func fillWordMergeFields(fragment string, fields map[string]string, matches map[string]int) string {
	if len(fields) == 0 {
		return fragment
	}
	fragment = wordSimpleFieldPattern.ReplaceAllStringFunc(fragment, func(block string) string {
		key := mergeFieldKey(wordAttribute(block, "instr"))
		matchedKey, value, ok := lookupTemplateField(fields, key)
		if !ok {
			return block
		}
		openingEnd := strings.Index(block, ">")
		closingStart := strings.LastIndex(strings.ToLower(block), "</w:fldsimple>")
		if openingEnd < 0 || closingStart <= openingEnd {
			return block
		}
		body := setWordRangeText(block[openingEnd+1:closingStart], value)
		matches[matchedKey]++
		return body
	})

	complex := complexWordFields(fragment)
	sort.Slice(complex, func(i, j int) bool { return complex[i].fieldStart > complex[j].fieldStart })
	for _, field := range complex {
		matchedKey, value, ok := lookupTemplateField(fields, field.key)
		if !ok || field.fieldStart < 0 || field.resultStart < field.fieldStart || field.resultEnd < field.resultStart || field.fieldEnd < field.resultEnd || field.fieldEnd > len(fragment) {
			continue
		}
		body := setWordRangeText(fragment[field.resultStart:field.resultEnd], value)
		fragment = fragment[:field.fieldStart] + body + fragment[field.fieldEnd:]
		matches[matchedKey]++
	}
	return fragment
}

func fillWordBookmarks(fragment string, fields map[string]string, matches map[string]int) string {
	if len(fields) == 0 {
		return fragment
	}
	bookmarks := wordBookmarkFields(fragment)
	sort.Slice(bookmarks, func(i, j int) bool { return bookmarks[i].bodyStart > bookmarks[j].bodyStart })
	for _, bookmark := range bookmarks {
		if !bookmark.inline {
			continue
		}
		matchedKey, value, ok := lookupTemplateField(fields, bookmark.key)
		if !ok || bookmark.bodyStart < 0 || bookmark.bodyEnd < bookmark.bodyStart || bookmark.bodyEnd > len(fragment) {
			continue
		}
		body := setWordRangeText(fragment[bookmark.bodyStart:bookmark.bodyEnd], value)
		fragment = fragment[:bookmark.bodyStart] + body + fragment[bookmark.bodyEnd:]
		matches[matchedKey]++
	}
	return fragment
}

func setWordRangeText(fragment, value string) string {
	nodes := parseWordTextNodes(fragment)
	if len(nodes) > 0 {
		nodes[0].text = value
		for index := 1; index < len(nodes); index++ {
			nodes[index].text = ""
		}
		return renderWordTextNodes(fragment, nodes)
	}
	var escaped bytes.Buffer
	_ = xml.EscapeText(&escaped, []byte(value))
	return fragment + "<w:r><w:t>" + escaped.String() + "</w:t></w:r>"
}

func complexWordFields(fragment string) []wordComplexField {
	type frame struct {
		begin     wordXMLSpan
		separator wordXMLSpan
	}
	stack := make([]frame, 0)
	result := make([]wordComplexField, 0)
	for _, location := range wordFieldCharPattern.FindAllStringIndex(fragment, -1) {
		tag := fragment[location[0]:location[1]]
		kind := strings.ToLower(wordAttribute(tag, "fldCharType"))
		span := wordXMLSpan{start: location[0], end: location[1]}
		switch kind {
		case "begin":
			stack = append(stack, frame{begin: span, separator: wordXMLSpan{start: -1, end: -1}})
		case "separate":
			if len(stack) > 0 && stack[len(stack)-1].separator.start < 0 {
				stack[len(stack)-1].separator = span
			}
		case "end":
			if len(stack) == 0 {
				continue
			}
			current := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if current.separator.start < 0 {
				continue
			}
			beginRun, beginOK := containingWordRun(fragment, current.begin.start)
			separatorRun, separatorOK := containingWordRun(fragment, current.separator.start)
			endRun, endOK := containingWordRun(fragment, span.start)
			if !beginOK || !separatorOK || !endOK || separatorRun.end > endRun.start || beginRun.end > separatorRun.start {
				continue
			}
			instruction := wordFieldInstruction(fragment[beginRun.end:separatorRun.start])
			key := mergeFieldKey(instruction)
			if key == "" {
				continue
			}
			body := fragment[separatorRun.end:endRun.start]
			result = append(result, wordComplexField{
				key: key, context: compactDocxFieldContext(wordNodesText(parseWordTextNodes(body))),
				fieldStart: beginRun.start, fieldEnd: endRun.end,
				resultStart: separatorRun.end, resultEnd: endRun.start,
			})
		}
	}
	return result
}

func wordBookmarkFields(fragment string) []wordBookmarkField {
	endsByID := make(map[string][]wordXMLSpan)
	for _, location := range wordBookmarkEndPattern.FindAllStringIndex(fragment, -1) {
		tag := fragment[location[0]:location[1]]
		id := wordAttribute(tag, "id")
		if id != "" {
			endsByID[id] = append(endsByID[id], wordXMLSpan{start: location[0], end: location[1]})
		}
	}
	result := make([]wordBookmarkField, 0)
	for _, location := range wordBookmarkStartPattern.FindAllStringIndex(fragment, -1) {
		tag := fragment[location[0]:location[1]]
		id := wordAttribute(tag, "id")
		name := html.UnescapeString(strings.TrimSpace(wordAttribute(tag, "name")))
		if id == "" || name == "" || strings.HasPrefix(name, "_") {
			continue
		}
		for _, end := range endsByID[id] {
			if end.start < location[1] {
				continue
			}
			body := fragment[location[1]:end.start]
			startParagraph, startOK := containingWordParagraph(fragment, location[0])
			endParagraph, endOK := containingWordParagraph(fragment, end.start)
			result = append(result, wordBookmarkField{
				key: name, context: compactDocxFieldContext(wordNodesText(parseWordTextNodes(body))),
				bodyStart: location[1], bodyEnd: end.start,
				inline: startOK && endOK && startParagraph.start == endParagraph.start && startParagraph.end == endParagraph.end,
			})
			break
		}
	}
	return result
}

func containingWordParagraph(fragment string, position int) (wordXMLSpan, bool) {
	for _, location := range wordParagraphPattern.FindAllStringIndex(fragment, -1) {
		if location[0] <= position && position < location[1] {
			return wordXMLSpan{start: location[0], end: location[1]}, true
		}
	}
	return wordXMLSpan{}, false
}

func containingWordRun(fragment string, position int) (wordXMLSpan, bool) {
	for _, location := range wordRunPattern.FindAllStringIndex(fragment, -1) {
		if location[0] <= position && position < location[1] {
			return wordXMLSpan{start: location[0], end: location[1]}, true
		}
	}
	return wordXMLSpan{}, false
}

func wordFieldInstruction(fragment string) string {
	var instruction strings.Builder
	for _, match := range wordInstructionTextPattern.FindAllStringSubmatch(fragment, -1) {
		if len(match) > 1 {
			instruction.WriteString(html.UnescapeString(match[1]))
		}
	}
	return instruction.String()
}

func mergeFieldKey(instruction string) string {
	instruction = html.UnescapeString(instruction)
	match := wordMergeFieldPattern.FindStringSubmatch(instruction)
	if len(match) < 4 {
		return ""
	}
	for _, candidate := range match[1:] {
		if candidate = strings.TrimSpace(candidate); candidate != "" {
			return candidate
		}
	}
	return ""
}

func wordAttribute(tag, localName string) string {
	wanted := strings.ToLower(strings.TrimSpace(localName))
	for _, pattern := range []*regexp.Regexp{wordAttributeDoublePattern, wordAttributeSinglePattern} {
		for _, match := range pattern.FindAllStringSubmatch(tag, -1) {
			if len(match) < 4 {
				continue
			}
			name := match[1]
			value := match[3]
			if match[2] != "" {
				name = match[2]
			}
			if strings.EqualFold(name, wanted) {
				return value
			}
		}
	}
	return ""
}

func wordFragmentTemplateKeys(fragment string) []string {
	keys := make([]string, 0)
	plain := wordNodesText(parseWordTextNodes(fragment))
	for _, match := range wordPlaceholder.FindAllStringSubmatch(plain, -1) {
		if len(match) > 1 {
			keys = append(keys, strings.TrimSpace(match[1]))
		}
	}
	for _, span := range leafWordContentControlSpans(fragment) {
		block := fragment[span.start:span.end]
		if match := wordSDTKeyPattern.FindStringSubmatch(block); len(match) > 1 {
			keys = append(keys, html.UnescapeString(match[1]))
		}
	}
	for _, field := range inspectWordMergeFields(fragment) {
		keys = append(keys, field.key)
	}
	for _, field := range inspectWordBookmarks(fragment) {
		keys = append(keys, field.key)
	}
	return uniqueStringValues(keys)
}

func leafWordContentControlSpans(fragment string) []wordXMLSpan {
	spans := wordElementSpans(fragment, wordSDTTagPattern)
	leaves := make([]wordXMLSpan, 0, len(spans))
	for index, span := range spans {
		hasNested := false
		for otherIndex, other := range spans {
			if index == otherIndex {
				continue
			}
			if span.start < other.start && other.end < span.end {
				hasNested = true
				break
			}
		}
		if !hasNested {
			leaves = append(leaves, span)
		}
	}
	return leaves
}

func wordContentControlType(block string) string {
	lower := strings.ToLower(block)
	switch {
	case wordCheckboxPattern.MatchString(block):
		return "checkbox"
	case strings.Contains(lower, "<w:dropdownlist"):
		return "dropdown"
	case strings.Contains(lower, "<w:combobox"):
		return "combobox"
	case strings.Contains(lower, "<w:date"):
		return "date"
	case strings.Contains(lower, "<w:picture"):
		return "picture"
	default:
		return "text"
	}
}

func wordCheckboxState(block string) string {
	if wordContentControlType(block) != "checkbox" {
		return ""
	}
	match := wordCheckedValuePattern.FindStringSubmatch(block)
	if len(match) < 3 {
		return "unchecked"
	}
	if value, ok := parseWordBoolean(match[2]); ok && value {
		return "checked"
	}
	return "unchecked"
}

func fillWordCheckbox(block, rawValue string) (string, bool) {
	checked, ok := parseWordBoolean(rawValue)
	if !ok || wordContentControlType(block) != "checkbox" {
		return block, false
	}
	value := "0"
	state := "2610"
	display := "☐"
	if checked {
		value = "1"
		state = "2612"
		display = "☒"
		if match := wordCheckedStatePattern.FindStringSubmatch(block); len(match) > 1 && strings.TrimSpace(match[1]) != "" {
			state = match[1]
		}
	} else if match := wordUncheckedStatePattern.FindStringSubmatch(block); len(match) > 1 && strings.TrimSpace(match[1]) != "" {
		state = match[1]
	}
	display = wordSymbolText(state, display)
	block = wordCheckedValuePattern.ReplaceAllString(block, `${1}`+value+`${3}`)
	if wordSymbolCharacterPattern.MatchString(block) {
		block = wordSymbolCharacterPattern.ReplaceAllString(block, `${1}`+state+`${2}`)
	}
	contentStart := wordSDTContentStart.FindStringIndex(block)
	contentEnd := strings.LastIndex(block, "</w:sdtContent>")
	if contentStart != nil && contentEnd >= contentStart[1] {
		inner := block[contentStart[1]:contentEnd]
		nodes := parseWordTextNodes(inner)
		if len(nodes) > 0 {
			nodes[0].text = display
			for index := 1; index < len(nodes); index++ {
				nodes[index].text = ""
			}
			inner = renderWordTextNodes(inner, nodes)
			block = block[:contentStart[1]] + inner + block[contentEnd:]
		}
	}
	return block, true
}

func wordSymbolText(hexValue, fallback string) string {
	value, err := strconv.ParseInt(strings.TrimSpace(hexValue), 16, 32)
	if err != nil || value <= 0 {
		return fallback
	}
	return string(rune(value))
}

func parseWordBoolean(value string) (bool, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "y", "on", "是", "选中", "勾选", "已选":
		return true, true
	case "0", "false", "no", "n", "off", "否", "未选中", "不勾选", "未选":
		return false, true
	default:
		return false, false
	}
}
