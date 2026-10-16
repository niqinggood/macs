package content

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"html"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

var (
	wordParagraphPattern  = regexp.MustCompile(`(?s)<w:p(?:\s[^>]*)?>.*?</w:p>`)
	wordTextPattern       = regexp.MustCompile(`(?s)<w:t(?:\s[^>]*)?>(.*?)</w:t>`)
	wordSDTTagPattern     = regexp.MustCompile(`(?i)<(/?)w:sdt(?:\s[^>]*)?>`)
	wordSDTKeyPattern     = regexp.MustCompile(`(?i)<w:(?:tag|alias)\b[^>]*\bw:val="([^"]+)"`)
	wordSDTContentStart   = regexp.MustCompile(`(?s)<w:sdtContent(?:\s[^>]*)?>`)
	wordUnderlinePattern  = regexp.MustCompile(`(?i)<w:u\b[^>]*>`)
	wordEmptyTextPattern  = regexp.MustCompile(`(?i)<w:t(?:\s[^>]*)?/>`)
	wordPlaceholder       = regexp.MustCompile(`\{\{([^{}]+)\}\}`)
	underscorePattern     = regexp.MustCompile(`_{2,}`)
	wordUnderlineLabelGap = regexp.MustCompile(`[ \t\r\n]{2,}`)
)

type docxTemplateFillStats struct {
	templateMatches  map[string]int
	underlineMatches map[string]int
	orderedMatched   int
}

type docxTemplateInspection struct {
	File            string                       `json:"file"`
	Fingerprint     string                       `json:"fingerprint"`
	Placeholders    []docxTemplateInspectionItem `json:"placeholders,omitempty"`
	ContentControls []docxTemplateInspectionItem `json:"content_controls,omitempty"`
	MergeFields     []docxTemplateInspectionItem `json:"merge_fields,omitempty"`
	Bookmarks       []docxTemplateInspectionItem `json:"bookmarks,omitempty"`
	Underlined      []docxTemplateInspectionItem `json:"underlined_fields,omitempty"`
	Tables          []docxTableInspection        `json:"tables,omitempty"`
}

type docxTemplateInspectionItem struct {
	Key      string `json:"key,omitempty"`
	Label    string `json:"label,omitempty"`
	Part     string `json:"part"`
	Context  string `json:"context,omitempty"`
	Index    int    `json:"index,omitempty"`
	Type     string `json:"type,omitempty"`
	Fillable bool   `json:"fillable,omitempty"`
}

func newDocxTemplateFillStats() *docxTemplateFillStats {
	return &docxTemplateFillStats{
		templateMatches:  make(map[string]int),
		underlineMatches: make(map[string]int),
	}
}

func (t *ExtractTool) inspectDocxTemplate(templatePath string) (string, error) {
	inspection, err := analyzeDocxTemplate(templatePath)
	if err != nil {
		return "", err
	}
	encoded, err := json.MarshalIndent(inspection, "", "  ")
	if err != nil {
		return "", fmt.Errorf("encode DOCX template inspection: %w", err)
	}
	return string(encoded), nil
}

func analyzeDocxTemplate(templatePath string) (docxTemplateInspection, error) {
	reader, err := zip.OpenReader(templatePath)
	if err != nil {
		return docxTemplateInspection{}, fmt.Errorf("open template: %w", err)
	}
	defer reader.Close()

	inspection := docxTemplateInspection{File: templatePath}
	fingerprintParts := make(map[string][]byte)
	underlineIndex := 0
	for _, file := range reader.File {
		if !isDocxTextPart(file.Name) {
			continue
		}
		content, readErr := readZipFile(file)
		if readErr != nil {
			return docxTemplateInspection{}, fmt.Errorf("read template part %s: %w", file.Name, readErr)
		}
		fingerprintParts[normalizeDocxPartName(file.Name)] = content
		part := wordEmptyTextPattern.ReplaceAllStringFunc(string(content), func(tag string) string {
			return strings.TrimSuffix(tag, "/>") + "></w:t>"
		})
		inspection.Tables = append(inspection.Tables, inspectDocxTables(part, file.Name)...)
		for _, span := range leafWordContentControlSpans(part) {
			block := part[span.start:span.end]
			keyMatch := wordSDTKeyPattern.FindStringSubmatch(block)
			if len(keyMatch) < 2 {
				continue
			}
			inspection.ContentControls = append(inspection.ContentControls, docxTemplateInspectionItem{
				Key:     html.UnescapeString(keyMatch[1]),
				Part:    file.Name,
				Context: contentControlInspectionContext(block),
				Type:    wordContentControlType(block),
			})
		}
		for _, field := range inspectWordMergeFields(part) {
			inspection.MergeFields = append(inspection.MergeFields, docxTemplateInspectionItem{
				Key: field.key, Part: file.Name, Context: field.context, Type: field.fieldType,
			})
		}
		for _, field := range inspectWordBookmarks(part) {
			fillable := field.fillable && isLikelyFillableBookmark(field.key, field.context)
			fieldType := "bookmark_anchor"
			if fillable {
				fieldType = "bookmark"
			}
			inspection.Bookmarks = append(inspection.Bookmarks, docxTemplateInspectionItem{
				Key: field.key, Part: file.Name, Context: field.context, Type: fieldType,
				Fillable: fillable,
			})
		}
		for _, paragraph := range wordParagraphPattern.FindAllString(part, -1) {
			nodes := parseWordTextNodes(paragraph)
			plain := wordNodesText(nodes)
			for _, match := range wordPlaceholder.FindAllStringSubmatch(plain, -1) {
				inspection.Placeholders = append(inspection.Placeholders, docxTemplateInspectionItem{
					Key:     strings.TrimSpace(match[1]),
					Part:    file.Name,
					Context: compactDocxFieldContext(plain),
				})
			}
			underscoreLocations := underscorePattern.FindAllStringIndex(plain, -1)
			for _, location := range underscoreLocations {
				underlineIndex++
				inspection.Underlined = append(inspection.Underlined, docxTemplateInspectionItem{
					Label:   inferDocxUnderlineLabel(plain[:location[0]]),
					Part:    file.Name,
					Context: compactDocxFieldContext(plain),
					Index:   underlineIndex,
				})
			}
			for _, region := range formattedBlankUnderlineRegions(nodes) {
				underlineIndex++
				inspection.Underlined = append(inspection.Underlined, docxTemplateInspectionItem{
					Label:   inferDocxUnderlineLabel(plain[:region.start]),
					Part:    file.Name,
					Context: compactDocxFieldContext(plain),
					Index:   underlineIndex,
				})
			}
		}
	}
	inspection.Placeholders = uniqueDocxInspectionItems(inspection.Placeholders)
	inspection.ContentControls = uniqueDocxInspectionItems(inspection.ContentControls)
	inspection.MergeFields = uniqueDocxInspectionItems(inspection.MergeFields)
	inspection.Bookmarks = uniqueDocxInspectionItems(inspection.Bookmarks)
	inspection.Fingerprint = fingerprintDocxTextParts(fingerprintParts)
	return inspection, nil
}

func contentControlInspectionContext(block string) string {
	if state := wordCheckboxState(block); state != "" {
		return state
	}
	return compactDocxFieldContext(wordNodesText(parseWordTextNodes(block)))
}

func compactDocxFieldContext(value string) string {
	value = strings.Join(strings.Fields(value), " ")
	runes := []rune(value)
	if len(runes) <= 120 {
		return value
	}
	return string(runes[:120]) + "…"
}

func inferDocxUnderlineLabel(value string) string {
	value = strings.TrimSpace(value)
	segments := wordUnderlineLabelGap.Split(value, -1)
	for index := len(segments) - 1; index >= 0; index-- {
		if candidate := strings.TrimSpace(segments[index]); candidate != "" {
			value = candidate
			break
		}
	}
	value = strings.TrimRight(value, "：:，,；;。.|/ ")
	runes := []rune(value)
	if len(runes) > 40 {
		runes = runes[len(runes)-40:]
	}
	return strings.TrimSpace(string(runes))
}

func uniqueDocxInspectionItems(items []docxTemplateInspectionItem) []docxTemplateInspectionItem {
	seen := make(map[string]bool, len(items))
	result := make([]docxTemplateInspectionItem, 0, len(items))
	for _, item := range items {
		key := item.Key + "\x00" + item.Label + "\x00" + item.Part + "\x00" + item.Context
		if seen[key] {
			continue
		}
		seen[key] = true
		result = append(result, item)
	}
	return result
}

func (t *ExtractTool) fillDocxTemplateAdvanced(
	templatePath string,
	templateData map[string]interface{},
	underlineData map[string]interface{},
	underlineValues []interface{},
	output string,
	strict bool,
) (string, error) {
	return t.fillDocxTemplateWithRepeatRows(templatePath, templateData, underlineData, underlineValues, nil, nil, output, strict, strict)
}

func (t *ExtractTool) fillDocxTemplateWithRepeatRows(
	templatePath string,
	templateData map[string]interface{},
	underlineData map[string]interface{},
	underlineValues []interface{},
	tableCells []docxTableCellPlan,
	repeatRows []docxRepeatRowPlan,
	output string,
	strict bool,
	verifyComplete bool,
) (string, error) {
	if output == "" {
		output = strings.TrimSuffix(templatePath, filepath.Ext(templatePath)) + "_filled.docx"
	}
	output = filepath.Clean(output)
	if err := os.MkdirAll(filepath.Dir(output), 0o755); err != nil {
		return "", fmt.Errorf("create output directory: %w", err)
	}

	templateFields := stringifyTemplateFields(templateData)
	underlineFields := stringifyTemplateFields(underlineData)
	orderedValues := stringifyTemplateValues(underlineValues)
	stats := newDocxTemplateFillStats()
	cellStats := newDocxTableCellStats()
	repeatStats := newDocxRepeatRowStats()
	var cellIssues []string
	var repeatIssues []string

	reader, err := zip.OpenReader(templatePath)
	if err != nil {
		return "", fmt.Errorf("open template: %w", err)
	}
	defer reader.Close()

	tempFile, err := os.CreateTemp(filepath.Dir(output), ".openkitty-docx-fill-*.tmp")
	if err != nil {
		return "", fmt.Errorf("create temporary output: %w", err)
	}
	tempPath := tempFile.Name()
	committed := false
	defer func() {
		if !committed {
			_ = os.Remove(tempPath)
		}
	}()

	writer := zip.NewWriter(tempFile)
	remainingOrdered := orderedValues
	for _, file := range reader.File {
		content, readErr := readZipFile(file)
		if readErr != nil {
			_ = writer.Close()
			_ = tempFile.Close()
			return "", fmt.Errorf("read template part %s: %w", file.Name, readErr)
		}
		if isDocxTextPart(file.Name) {
			part, issues := applyDocxTableCells(string(content), file.Name, tableCells, cellStats)
			cellIssues = append(cellIssues, issues...)
			part, issues = applyDocxRepeatRows(part, file.Name, repeatRows, repeatStats)
			content = []byte(part)
			repeatIssues = append(repeatIssues, issues...)
			content, remainingOrdered = fillDocxXMLPart(content, templateFields, underlineFields, remainingOrdered, stats)
		}
		header := file.FileHeader
		entry, createErr := writer.CreateHeader(&header)
		if createErr != nil {
			_ = writer.Close()
			_ = tempFile.Close()
			return "", fmt.Errorf("create output part %s: %w", file.Name, createErr)
		}
		if _, writeErr := entry.Write(content); writeErr != nil {
			_ = writer.Close()
			_ = tempFile.Close()
			return "", fmt.Errorf("write output part %s: %w", file.Name, writeErr)
		}
	}
	if err := writer.Close(); err != nil {
		_ = tempFile.Close()
		return "", fmt.Errorf("finish DOCX package: %w", err)
	}
	if err := tempFile.Close(); err != nil {
		return "", fmt.Errorf("close DOCX output: %w", err)
	}
	if err := ValidateOfficeOpenXMLPackage(tempPath); err != nil {
		return "", fmt.Errorf("generated DOCX package validation failed: %w", err)
	}
	// Close the source before replacing an in-place output. Windows does not
	// allow renaming an open DOCX package.
	if err := reader.Close(); err != nil {
		return "", fmt.Errorf("close DOCX template: %w", err)
	}

	for _, plan := range repeatRows {
		if !repeatStats.appliedPlans[plan.ID] {
			repeatIssues = append(repeatIssues, wordTableAddress(plan.Part, plan.TableIndex, plan.TemplateRow)+": repeat plan was not applied")
		}
	}
	for _, plan := range tableCells {
		if !cellStats.appliedPlans[plan.ID] {
			cellIssues = append(cellIssues, wordTableCellAddress(plan.Part, plan.TableIndex, plan.Row, plan.Cell)+": table-cell plan was not applied")
		}
	}
	unmatched := collectUnmatchedDocxFields(templateFields, underlineFields, len(orderedValues), stats)
	unmatched = append(unmatched, cellIssues...)
	unmatched = append(unmatched, repeatIssues...)
	if verifyComplete {
		completenessIssues, completenessErr := validateDocxOutputCompleteness(tempPath)
		if completenessErr != nil {
			return "", completenessErr
		}
		unmatched = append(unmatched, completenessIssues...)
	}
	unmatched = uniqueStringValues(unmatched)
	if (strict || verifyComplete) && len(unmatched) > 0 {
		return "", fmt.Errorf("DOCX template fields not matched: %s", strings.Join(unmatched, ", "))
	}
	if err := replaceFileFromTemp(tempPath, output); err != nil {
		return "", err
	}
	committed = true

	info, _ := os.Stat(output)
	size := ""
	if info != nil {
		size = fmt.Sprintf(" (%.1f KB)", float64(info.Size())/1024)
	}
	templateCount := sumMatchCounts(stats.templateMatches)
	underlineCount := sumMatchCounts(stats.underlineMatches) + stats.orderedMatched
	receipt := fmt.Sprintf("✅ Template filled: %s%s; template/content-control replacements=%d; table cells=%d; underlined fields=%d; repeated rows=%d; opc=verified", output, size, templateCount, cellStats.valuesFilled, underlineCount, repeatStats.rowsGenerated)
	if verifyComplete {
		receipt += "; completeness=verified"
	}
	if len(unmatched) > 0 {
		receipt += "; unmatched=" + strings.Join(unmatched, ", ")
	}
	return receipt, nil
}

func validateDocxOutputCompleteness(path string) ([]string, error) {
	inspection, err := analyzeDocxTemplate(path)
	if err != nil {
		return nil, fmt.Errorf("validate generated DOCX: %w", err)
	}
	var issues []string
	for _, item := range inspection.Placeholders {
		issues = append(issues, "remaining placeholder:"+item.Key)
	}
	for _, item := range inspection.ContentControls {
		if item.Type == "checkbox" {
			continue
		}
		context := strings.ToLower(strings.TrimSpace(item.Context))
		if context == "" || containsAnyText(context, []string{"点击此处输入", "单击或点击此处输入", "click or tap here", "choose an item", "请选择"}) {
			issues = append(issues, "empty content control:"+item.Key)
		}
	}
	for _, item := range inspection.MergeFields {
		context := strings.ToLower(strings.TrimSpace(item.Context))
		if context == "" || strings.Contains(context, "«"+strings.ToLower(item.Key)+"»") {
			issues = append(issues, "empty merge field:"+item.Key)
		}
	}
	for _, item := range inspection.Bookmarks {
		if !item.Fillable {
			continue
		}
		context := strings.ToLower(strings.TrimSpace(item.Context))
		if context == "" || containsAnyText(context, []string{"点击此处输入", "click or tap here", "{{", "«"}) {
			issues = append(issues, "empty bookmark:"+item.Key)
		}
	}
	for _, item := range inspection.Underlined {
		label := strings.TrimSpace(item.Label)
		if label == "" {
			label = fmt.Sprintf("index=%d", item.Index)
		}
		issues = append(issues, "remaining underline:"+label)
	}
	return uniqueStringValues(issues), nil
}

func isLikelyFillableBookmark(key, context string) bool {
	context = strings.TrimSpace(context)
	if context == "" || strings.EqualFold(context, strings.TrimSpace(key)) {
		return true
	}
	return containsAnyText(strings.ToLower(context), []string{
		"待填写", "请输入", "点击此处", "click or tap here", "type text", "{{", "«", "__",
	})
}

func containsAnyText(value string, candidates []string) bool {
	for _, candidate := range candidates {
		if strings.Contains(value, strings.ToLower(candidate)) {
			return true
		}
	}
	return false
}

func stringifyTemplateFields(values map[string]interface{}) map[string]string {
	result := make(map[string]string, len(values))
	for key, value := range values {
		key = strings.TrimSpace(key)
		if key != "" {
			result[key] = fmt.Sprint(value)
		}
	}
	return result
}

func stringifyTemplateValues(values []interface{}) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		result = append(result, fmt.Sprint(value))
	}
	return result
}

func readZipFile(file *zip.File) ([]byte, error) {
	reader, err := file.Open()
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	return io.ReadAll(reader)
}

func isDocxTextPart(name string) bool {
	name = strings.ToLower(filepath.ToSlash(name))
	if !strings.HasPrefix(name, "word/") || !strings.HasSuffix(name, ".xml") {
		return false
	}
	base := filepath.Base(name)
	return base == "document.xml" || base == "footnotes.xml" || base == "endnotes.xml" || base == "comments.xml" ||
		strings.HasPrefix(base, "header") || strings.HasPrefix(base, "footer")
}

func fillDocxXMLPart(
	content []byte,
	templateFields map[string]string,
	underlineFields map[string]string,
	orderedValues []string,
	stats *docxTemplateFillStats,
) ([]byte, []string) {
	part := string(content)
	part = wordEmptyTextPattern.ReplaceAllStringFunc(part, func(tag string) string {
		return strings.TrimSuffix(tag, "/>") + "></w:t>"
	})
	part = fillWordContentControls(part, templateFields, stats.templateMatches)
	part = fillWordMergeFields(part, templateFields, stats.templateMatches)
	part = fillWordBookmarks(part, templateFields, stats.templateMatches)
	part = wordParagraphPattern.ReplaceAllStringFunc(part, func(paragraph string) string {
		paragraph = fillWordPlaceholders(paragraph, templateFields, stats.templateMatches)
		paragraph = fillNamedWordUnderlines(paragraph, underlineFields, stats.underlineMatches)
		if len(orderedValues) > 0 {
			var used int
			paragraph, used = fillOrderedWordUnderlines(paragraph, orderedValues)
			orderedValues = orderedValues[used:]
			stats.orderedMatched += used
		}
		return paragraph
	})
	return []byte(part), orderedValues
}

func fillWordContentControls(part string, fields map[string]string, matches map[string]int) string {
	if len(fields) == 0 {
		return part
	}
	spans := leafWordContentControlSpans(part)
	sort.Slice(spans, func(i, j int) bool { return spans[i].start > spans[j].start })
	for _, span := range spans {
		block := part[span.start:span.end]
		keyMatch := wordSDTKeyPattern.FindStringSubmatch(block)
		if len(keyMatch) < 2 {
			continue
		}
		key, value, ok := lookupTemplateField(fields, html.UnescapeString(keyMatch[1]))
		if !ok {
			continue
		}
		if updated, filled := fillWordCheckbox(block, value); filled {
			matches[key]++
			part = part[:span.start] + updated + part[span.end:]
			continue
		}
		contentStart := wordSDTContentStart.FindStringIndex(block)
		contentEnd := strings.LastIndex(block, "</w:sdtContent>")
		if contentStart == nil || contentEnd < contentStart[1] {
			continue
		}
		inner := block[contentStart[1]:contentEnd]
		nodes := parseWordTextNodes(inner)
		if len(nodes) == 0 {
			continue
		}
		nodes[0].text = value
		for index := 1; index < len(nodes); index++ {
			nodes[index].text = ""
		}
		inner = renderWordTextNodes(inner, nodes)
		matches[key]++
		updated := block[:contentStart[1]] + inner + block[contentEnd:]
		part = part[:span.start] + updated + part[span.end:]
	}
	return part
}

func fillWordPlaceholders(fragment string, fields map[string]string, matches map[string]int) string {
	if len(fields) == 0 {
		return fragment
	}
	nodes := parseWordTextNodes(fragment)
	if len(nodes) == 0 {
		return fragment
	}
	keys := sortedTemplateKeys(fields)
	for _, key := range keys {
		needle := "{{" + key + "}}"
		for replaceLiteralInWordNodes(nodes, needle, fields[key]) {
			matches[key]++
		}
	}
	return renderWordTextNodes(fragment, nodes)
}

func fillNamedWordUnderlines(fragment string, fields map[string]string, matches map[string]int) string {
	if len(fields) == 0 {
		return fragment
	}
	nodes := parseWordTextNodes(fragment)
	if len(nodes) == 0 {
		return fragment
	}
	for _, key := range sortedTemplateKeys(fields) {
		if fillNamedWordUnderline(nodes, key, fields[key]) {
			matches[key]++
		}
	}
	return renderWordTextNodes(fragment, nodes)
}

func fillNamedWordUnderline(nodes []wordTextNode, label, value string) bool {
	plain := wordNodesText(nodes)
	labelStart := strings.Index(strings.ToLower(plain), strings.ToLower(label))
	if labelStart < 0 {
		return false
	}
	searchStart := labelStart + len(label)
	searchEnd := searchStart + 200
	if searchEnd > len(plain) {
		searchEnd = len(plain)
	}
	var candidate wordXMLSpan
	var formattedCandidate formattedUnderlineRegion
	found := false
	formatted := false
	if location := underscorePattern.FindStringIndex(plain[searchStart:searchEnd]); location != nil {
		candidate = wordXMLSpan{start: searchStart + location[0], end: searchStart + location[1]}
		found = true
	}
	for _, region := range formattedBlankUnderlineRegions(nodes) {
		if region.start < searchStart || region.start > searchEnd {
			continue
		}
		if !found || region.start < candidate.start {
			candidate = region.wordXMLSpan
			formattedCandidate = region
			found = true
			formatted = true
		}
		break
	}
	if found {
		if formatted {
			fillFormattedUnderlineRegion(nodes, formattedCandidate, value)
			return true
		}
		return replaceWordNodeRange(nodes, candidate.start, candidate.end, value)
	}
	return false
}

func fillOrderedWordUnderlines(fragment string, values []string) (string, int) {
	if len(values) == 0 {
		return fragment, 0
	}
	nodes := parseWordTextNodes(fragment)
	if len(nodes) == 0 {
		return fragment, 0
	}
	used := 0
	for used < len(values) {
		plain := wordNodesText(nodes)
		var candidate wordXMLSpan
		var formattedCandidate formattedUnderlineRegion
		found := false
		formatted := false
		if location := underscorePattern.FindStringIndex(plain); location != nil {
			candidate = wordXMLSpan{start: location[0], end: location[1]}
			found = true
		}
		if regions := formattedBlankUnderlineRegions(nodes); len(regions) > 0 && (!found || regions[0].start < candidate.start) {
			candidate = regions[0].wordXMLSpan
			formattedCandidate = regions[0]
			found = true
			formatted = true
		}
		if !found {
			break
		}
		if formatted {
			fillFormattedUnderlineRegion(nodes, formattedCandidate, values[used])
		} else if !replaceWordNodeRange(nodes, candidate.start, candidate.end, values[used]) {
			break
		}
		used++
	}
	return renderWordTextNodes(fragment, nodes), used
}

type formattedUnderlineRegion struct {
	wordXMLSpan
	firstNode int
	endNode   int
}

func formattedBlankUnderlineRegions(nodes []wordTextNode) []formattedUnderlineRegion {
	offsets := wordNodeOffsets(nodes)
	regions := make([]formattedUnderlineRegion, 0)
	for index := 0; index < len(nodes); {
		if !isFormattedUnderlineNode(nodes[index]) {
			index++
			continue
		}
		end := index + 1
		allBlank := strings.TrimSpace(nodes[index].text) == ""
		for end < len(nodes) && isFormattedUnderlineNode(nodes[end]) {
			allBlank = allBlank && strings.TrimSpace(nodes[end].text) == ""
			end++
		}
		if allBlank {
			regions = append(regions, formattedUnderlineRegion{
				wordXMLSpan: wordXMLSpan{start: offsets[index], end: offsets[end]},
				firstNode:   index,
				endNode:     end,
			})
		}
		index = end
	}
	return regions
}

func isFormattedUnderlineNode(node wordTextNode) bool {
	if !node.underlined || node.text == "_" || strings.Contains(node.text, "__") {
		return false
	}
	return true
}

func fillFormattedUnderlineRegion(nodes []wordTextNode, region formattedUnderlineRegion, value string) {
	nodes[region.firstNode].text = value
	for index := region.firstNode + 1; index < region.endNode; index++ {
		nodes[index].text = ""
	}
}

type wordTextNode struct {
	start      int
	end        int
	open       string
	close      string
	text       string
	underlined bool
}

func parseWordTextNodes(fragment string) []wordTextNode {
	locations := wordTextPattern.FindAllStringSubmatchIndex(fragment, -1)
	nodes := make([]wordTextNode, 0, len(locations))
	for _, location := range locations {
		if len(location) < 4 || location[2] < 0 || location[3] < 0 {
			continue
		}
		runStart := strings.LastIndex(fragment[:location[0]], "<w:r")
		runEndBefore := strings.LastIndex(fragment[:location[0]], "</w:r>")
		underlined := false
		if runStart >= 0 && runStart > runEndBefore {
			runPrefix := fragment[runStart:location[0]]
			underlineTag := wordUnderlinePattern.FindString(runPrefix)
			underlined = underlineTag != "" && !strings.Contains(strings.ToLower(underlineTag), `w:val="none"`)
		}
		nodes = append(nodes, wordTextNode{
			start:      location[0],
			end:        location[1],
			open:       fragment[location[0]:location[2]],
			close:      fragment[location[3]:location[1]],
			text:       html.UnescapeString(fragment[location[2]:location[3]]),
			underlined: underlined,
		})
	}
	return nodes
}

func renderWordTextNodes(fragment string, nodes []wordTextNode) string {
	if len(nodes) == 0 {
		return fragment
	}
	var output strings.Builder
	output.Grow(len(fragment) + 64)
	position := 0
	for _, node := range nodes {
		output.WriteString(fragment[position:node.start])
		output.WriteString(wordTextOpenTag(node.open, node.text))
		var escaped bytes.Buffer
		_ = xml.EscapeText(&escaped, []byte(node.text))
		output.Write(escaped.Bytes())
		output.WriteString(node.close)
		position = node.end
	}
	output.WriteString(fragment[position:])
	return output.String()
}

func wordTextOpenTag(open, value string) string {
	if value == "" || (!strings.HasPrefix(value, " ") && !strings.HasSuffix(value, " ")) || strings.Contains(open, "xml:space=") {
		return open
	}
	return strings.TrimSuffix(open, ">") + ` xml:space="preserve">`
}

func replaceLiteralInWordNodes(nodes []wordTextNode, needle, replacement string) bool {
	plain := wordNodesText(nodes)
	start := strings.Index(plain, needle)
	if start < 0 {
		return false
	}
	return replaceWordNodeRange(nodes, start, start+len(needle), replacement)
}

func replaceWordNodeRange(nodes []wordTextNode, start, end int, replacement string) bool {
	if start < 0 || end <= start {
		return false
	}
	offsets := wordNodeOffsets(nodes)
	startNode, endNode := -1, -1
	for index := range nodes {
		if startNode < 0 && start >= offsets[index] && start < offsets[index+1] {
			startNode = index
		}
		if end > offsets[index] && end <= offsets[index+1] {
			endNode = index
			break
		}
	}
	if startNode < 0 || endNode < startNode {
		return false
	}
	startOffset := start - offsets[startNode]
	endOffset := end - offsets[endNode]
	if startNode == endNode {
		nodes[startNode].text = nodes[startNode].text[:startOffset] + replacement + nodes[startNode].text[endOffset:]
		return true
	}
	nodes[startNode].text = nodes[startNode].text[:startOffset] + replacement
	for index := startNode + 1; index < endNode; index++ {
		nodes[index].text = ""
	}
	nodes[endNode].text = nodes[endNode].text[endOffset:]
	return true
}

func wordNodesText(nodes []wordTextNode) string {
	var text strings.Builder
	for _, node := range nodes {
		text.WriteString(node.text)
	}
	return text.String()
}

func wordNodeOffsets(nodes []wordTextNode) []int {
	offsets := make([]int, len(nodes)+1)
	for index := range nodes {
		offsets[index+1] = offsets[index] + len(nodes[index].text)
	}
	return offsets
}

func lookupTemplateField(fields map[string]string, requested string) (string, string, bool) {
	if value, ok := fields[requested]; ok {
		return requested, value, true
	}
	for key, value := range fields {
		if strings.EqualFold(strings.TrimSpace(key), strings.TrimSpace(requested)) {
			return key, value, true
		}
	}
	return "", "", false
}

func sortedTemplateKeys(fields map[string]string) []string {
	keys := make([]string, 0, len(fields))
	for key := range fields {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if len(keys[i]) != len(keys[j]) {
			return len(keys[i]) > len(keys[j])
		}
		return keys[i] < keys[j]
	})
	return keys
}

func collectUnmatchedDocxFields(templateFields, underlineFields map[string]string, orderedCount int, stats *docxTemplateFillStats) []string {
	var unmatched []string
	for _, key := range sortedTemplateKeys(templateFields) {
		if stats.templateMatches[key] == 0 {
			unmatched = append(unmatched, "template:"+key)
		}
	}
	for _, key := range sortedTemplateKeys(underlineFields) {
		if stats.underlineMatches[key] == 0 {
			unmatched = append(unmatched, "underline:"+key)
		}
	}
	for index := stats.orderedMatched; index < orderedCount; index++ {
		unmatched = append(unmatched, fmt.Sprintf("underline_values[%d]", index))
	}
	return unmatched
}

func sumMatchCounts(matches map[string]int) int {
	total := 0
	for _, count := range matches {
		total += count
	}
	return total
}

func replaceFileFromTemp(tempPath, output string) error {
	if _, err := os.Stat(output); os.IsNotExist(err) {
		if err := os.Rename(tempPath, output); err != nil {
			return fmt.Errorf("commit DOCX output: %w", err)
		}
		return nil
	} else if err != nil {
		return fmt.Errorf("inspect DOCX output: %w", err)
	}

	backup := fmt.Sprintf("%s.openkitty-backup-%d", output, time.Now().UnixNano())
	if err := os.Rename(output, backup); err != nil {
		return fmt.Errorf("prepare existing DOCX output: %w", err)
	}
	if err := os.Rename(tempPath, output); err != nil {
		_ = os.Rename(backup, output)
		return fmt.Errorf("commit DOCX output: %w", err)
	}
	_ = os.Remove(backup)
	return nil
}
