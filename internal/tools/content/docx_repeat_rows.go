package content

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"sort"
	"strings"
)

type docxRepeatRowPlan struct {
	ID                    int
	Part                  string
	TableIndex            int
	TemplateRow           int
	DataPath              string
	Rows                  []map[string]interface{}
	ColumnKeys            []string
	RemoveTemplateIfEmpty bool
}

type docxRepeatRowStats struct {
	appliedPlans      map[int]bool
	rowsGenerated     int
	cellValues        int
	placeholderValues int
}

func newDocxRepeatRowStats() *docxRepeatRowStats {
	return &docxRepeatRowStats{appliedPlans: make(map[int]bool)}
}

func parseDocxRepeatRowPlans(raw []interface{}, sourceData map[string]interface{}) ([]docxRepeatRowPlan, error) {
	plans := make([]docxRepeatRowPlan, 0, len(raw))
	addresses := make(map[string]bool)
	for index, item := range raw {
		definition, ok := item.(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("repeat_rows[%d] must be an object", index)
		}
		plan := docxRepeatRowPlan{
			ID:          index + 1,
			Part:        stringMapValue(definition, "part", "word/document.xml"),
			TableIndex:  intMapValue(definition, "table_index"),
			TemplateRow: intMapValue(definition, "template_row"),
			DataPath:    stringMapValue(definition, "data_path", ""),
		}
		plan.RemoveTemplateIfEmpty, _ = definition["remove_template_if_empty"].(bool)
		plan.ColumnKeys = interfaceStringSlice(definition["column_keys"])
		if plan.TableIndex <= 0 || plan.TemplateRow <= 0 {
			return nil, fmt.Errorf("repeat_rows[%d] requires positive 1-based table_index and template_row", index)
		}
		address := wordTableAddress(normalizeDocxPartName(plan.Part), plan.TableIndex, plan.TemplateRow)
		if addresses[address] {
			return nil, fmt.Errorf("repeat_rows[%d] duplicates %s", index, address)
		}
		addresses[address] = true
		if directRows, exists := definition["rows"]; exists {
			rows, rowErr := interfaceRecordSlice(directRows)
			if rowErr != nil {
				return nil, fmt.Errorf("repeat_rows[%d].rows: %w", index, rowErr)
			}
			plan.Rows = rows
		} else if plan.DataPath != "" {
			value, exists := lookupNestedDocxValue(sourceData, plan.DataPath)
			if !exists {
				return nil, fmt.Errorf("repeat_rows[%d] data_path %q not found", index, plan.DataPath)
			}
			rows, rowErr := interfaceRecordSlice(value)
			if rowErr != nil {
				return nil, fmt.Errorf("repeat_rows[%d] data_path %q: %w", index, plan.DataPath, rowErr)
			}
			plan.Rows = rows
		} else {
			return nil, fmt.Errorf("repeat_rows[%d] requires rows or data_path", index)
		}
		plans = append(plans, plan)
	}
	return plans, nil
}

func applyDocxRepeatRows(part, partName string, plans []docxRepeatRowPlan, stats *docxRepeatRowStats) (string, []string) {
	var issues []string
	relevant := make([]docxRepeatRowPlan, 0)
	for _, plan := range plans {
		if normalizeDocxPartName(plan.Part) == normalizeDocxPartName(partName) {
			relevant = append(relevant, plan)
		}
	}
	// Descending addresses keep lower table/row indices stable as XML grows.
	sort.SliceStable(relevant, func(i, j int) bool {
		if relevant[i].TableIndex != relevant[j].TableIndex {
			return relevant[i].TableIndex > relevant[j].TableIndex
		}
		return relevant[i].TemplateRow > relevant[j].TemplateRow
	})

	for _, plan := range relevant {
		tables := topLevelWordXMLSpans(wordElementSpans(part, wordTableTagPattern))
		if plan.TableIndex > len(tables) {
			issues = append(issues, wordTableAddress(plan.Part, plan.TableIndex, plan.TemplateRow)+": table not found")
			continue
		}
		tableSpan := tables[plan.TableIndex-1]
		tableXML := part[tableSpan.start:tableSpan.end]
		rows := topLevelWordXMLSpans(wordElementSpans(tableXML, wordRowTagPattern))
		if plan.TemplateRow > len(rows) {
			issues = append(issues, wordTableAddress(plan.Part, plan.TableIndex, plan.TemplateRow)+": template row not found")
			continue
		}

		rowSpan := rows[plan.TemplateRow-1]
		templateRow := tableXML[rowSpan.start:rowSpan.end]
		if len(plan.Rows) == 0 && !plan.RemoveTemplateIfEmpty {
			stats.appliedPlans[plan.ID] = true
			continue
		}
		var generated strings.Builder
		for rowIndex, record := range plan.Rows {
			rowXML, rowIssues, cellValues, placeholderValues := fillDocxRepeatRow(templateRow, record, plan.ColumnKeys, rowIndex+1)
			generated.WriteString(rowXML)
			stats.rowsGenerated++
			stats.cellValues += cellValues
			stats.placeholderValues += placeholderValues
			for _, issue := range rowIssues {
				issues = append(issues, fmt.Sprintf("%s.generated[%d]: %s", wordTableAddress(plan.Part, plan.TableIndex, plan.TemplateRow), rowIndex+1, issue))
			}
		}
		tableXML = tableXML[:rowSpan.start] + generated.String() + tableXML[rowSpan.end:]
		part = part[:tableSpan.start] + tableXML + part[tableSpan.end:]
		stats.appliedPlans[plan.ID] = true
	}
	return part, issues
}

func fillDocxRepeatRow(templateRow string, record map[string]interface{}, columnKeys []string, rowNumber int) (string, []string, int, int) {
	row := templateRow
	var issues []string
	cellValues := 0
	placeholderValues := 0

	if len(columnKeys) > 0 {
		cells := topLevelWordXMLSpans(wordElementSpans(row, wordCellTagPattern))
		for columnIndex, key := range columnKeys {
			key = strings.TrimSpace(key)
			if key == "" {
				continue
			}
			if columnIndex >= len(cells) {
				issues = append(issues, fmt.Sprintf("column_keys[%d]=%s has no table cell", columnIndex, key))
				continue
			}
			value, ok := resolveDocxRepeatValue(record, key, rowNumber)
			if !ok {
				issues = append(issues, fmt.Sprintf("column %d source field %q not found", columnIndex+1, key))
				continue
			}
			cellSpan := cells[columnIndex]
			cellXML := setDocxElementText(row[cellSpan.start:cellSpan.end], fmt.Sprint(value))
			row = row[:cellSpan.start] + cellXML + row[cellSpan.end:]
			cellValues++
			// Recompute spans because replacement lengths may change.
			cells = topLevelWordXMLSpans(wordElementSpans(row, wordCellTagPattern))
		}
	}

	fields := make(map[string]string)
	for _, templateKey := range wordFragmentTemplateKeys(row) {
		value, ok := resolveDocxRepeatValue(record, repeatPlaceholderSourceKey(templateKey), rowNumber)
		if !ok {
			issues = append(issues, fmt.Sprintf("template field %q has no matching row field", templateKey))
			continue
		}
		fields[templateKey] = fmt.Sprint(value)
	}
	if len(fields) > 0 {
		matches := make(map[string]int)
		row = fillWordContentControls(row, fields, matches)
		row = fillWordMergeFields(row, fields, matches)
		row = fillWordBookmarks(row, fields, matches)
		row = fillWordPlaceholders(row, fields, matches)
		placeholderValues = sumMatchCounts(matches)
	}
	return row, uniqueStringValues(issues), cellValues, placeholderValues
}

func setDocxElementText(element, value string) string {
	nodes := parseWordTextNodes(element)
	if len(nodes) > 0 {
		nodes[0].text = value
		for index := 1; index < len(nodes); index++ {
			nodes[index].text = ""
		}
		return renderWordTextNodes(element, nodes)
	}
	if paragraph := wordParagraphPattern.FindStringIndex(element); paragraph != nil {
		paragraphXML := element[paragraph[0]:paragraph[1]]
		closing := strings.LastIndex(strings.ToLower(paragraphXML), "</w:p>")
		if closing >= 0 {
			var escaped bytes.Buffer
			_ = xml.EscapeText(&escaped, []byte(value))
			insert := "<w:r><w:t>" + escaped.String() + "</w:t></w:r>"
			paragraphXML = paragraphXML[:closing] + insert + paragraphXML[closing:]
			return element[:paragraph[0]] + paragraphXML + element[paragraph[1]:]
		}
	}
	closing := strings.LastIndex(strings.ToLower(element), "</w:tc>")
	if closing < 0 {
		return element
	}
	var escaped bytes.Buffer
	_ = xml.EscapeText(&escaped, []byte(value))
	insert := "<w:p><w:r><w:t>" + escaped.String() + "</w:t></w:r></w:p>"
	return element[:closing] + insert + element[closing:]
}

func resolveDocxRepeatValue(record map[string]interface{}, requested string, rowNumber int) (interface{}, bool) {
	requested = strings.TrimSpace(requested)
	if requested == "$index" || requested == "@index" || requested == "index" || requested == "序号" {
		return rowNumber, true
	}
	if value, ok := lookupNestedDocxValue(record, requested); ok {
		return value, true
	}
	sources := flattenDocxSourceData(record)
	candidates := scoreDocxSourceFields(requested, sources)
	if len(candidates) == 0 || candidates[0].confidence < defaultDocxMappingThreshold {
		return nil, false
	}
	if len(candidates) > 1 && candidates[0].confidence-candidates[1].confidence < 0.03 {
		return nil, false
	}
	return candidates[0].field.value, true
}

func repeatPlaceholderSourceKey(value string) string {
	value = strings.TrimSpace(value)
	value = strings.ReplaceAll(value, "[]", "")
	if index := strings.LastIndex(value, "."); index >= 0 {
		return value[index+1:]
	}
	return value
}

func lookupNestedDocxValue(root map[string]interface{}, path string) (interface{}, bool) {
	path = strings.TrimSpace(path)
	if path == "" {
		return root, true
	}
	var current interface{} = root
	for _, segment := range strings.Split(path, ".") {
		object, ok := current.(map[string]interface{})
		if !ok {
			return nil, false
		}
		value, found := lookupInterfaceMapFold(object, strings.TrimSpace(segment))
		if !found {
			return nil, false
		}
		current = value
	}
	return current, true
}

func interfaceRecordSlice(value interface{}) ([]map[string]interface{}, error) {
	items, ok := value.([]interface{})
	if !ok {
		if records, direct := value.([]map[string]interface{}); direct {
			return records, nil
		}
		return nil, fmt.Errorf("must be an array of objects")
	}
	records := make([]map[string]interface{}, 0, len(items))
	for index, item := range items {
		record, ok := item.(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("item %d must be an object", index)
		}
		records = append(records, record)
	}
	return records, nil
}

func interfaceStringSlice(value interface{}) []string {
	switch items := value.(type) {
	case []interface{}:
		result := make([]string, 0, len(items))
		for _, item := range items {
			result = append(result, strings.TrimSpace(fmt.Sprint(item)))
		}
		return result
	case []string:
		return append([]string{}, items...)
	default:
		return nil
	}
}

func intMapValue(values map[string]interface{}, key string) int {
	switch value := values[key].(type) {
	case int:
		return value
	case float64:
		return int(value)
	default:
		return 0
	}
}

func stringMapValue(values map[string]interface{}, key, fallback string) string {
	if value, ok := values[key].(string); ok && strings.TrimSpace(value) != "" {
		return strings.TrimSpace(value)
	}
	return fallback
}

func normalizeDocxPartName(value string) string {
	return strings.ToLower(strings.ReplaceAll(strings.TrimSpace(value), "\\", "/"))
}

func inspectionWithoutRepeatTargets(inspection docxTemplateInspection, plans []docxRepeatRowPlan) docxTemplateInspection {
	if len(plans) == 0 {
		return inspection
	}
	repeatedKeys := make(map[string]bool)
	for _, plan := range plans {
		for _, table := range inspection.Tables {
			if normalizeDocxPartName(table.Part) != normalizeDocxPartName(plan.Part) || table.Index != plan.TableIndex {
				continue
			}
			for _, row := range table.Rows {
				if row.Index == plan.TemplateRow {
					for _, key := range row.FieldKeys {
						repeatedKeys[strings.ToLower(strings.TrimSpace(key))] = true
					}
				}
			}
		}
	}
	filtered := inspection.Placeholders[:0:0]
	for _, item := range inspection.Placeholders {
		if !repeatedKeys[strings.ToLower(strings.TrimSpace(item.Key))] {
			filtered = append(filtered, item)
		}
	}
	inspection.Placeholders = filtered
	inspection.ContentControls = filterDocxInspectionItemsByKeys(inspection.ContentControls, repeatedKeys)
	inspection.MergeFields = filterDocxInspectionItemsByKeys(inspection.MergeFields, repeatedKeys)
	inspection.Bookmarks = filterDocxInspectionItemsByKeys(inspection.Bookmarks, repeatedKeys)
	return inspection
}

func filterDocxInspectionItemsByKeys(items []docxTemplateInspectionItem, excluded map[string]bool) []docxTemplateInspectionItem {
	filtered := items[:0:0]
	for _, item := range items {
		if !excluded[strings.ToLower(strings.TrimSpace(item.Key))] {
			filtered = append(filtered, item)
		}
	}
	return filtered
}
