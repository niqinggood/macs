package content

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

var (
	wordTableTagPattern = regexp.MustCompile(`(?i)<(/?)w:tbl(?:\s[^>]*)?>`)
	wordRowTagPattern   = regexp.MustCompile(`(?i)<(/?)w:tr(?:\s[^>]*)?>`)
	wordCellTagPattern  = regexp.MustCompile(`(?i)<(/?)w:tc(?:\s[^>]*)?>`)
)

type docxTableInspection struct {
	Part  string              `json:"part"`
	Index int                 `json:"index"`
	Rows  []docxRowInspection `json:"rows"`
}

type docxRowInspection struct {
	Index            int                       `json:"index"`
	Cells            []string                  `json:"cells"`
	CellDetails      []docxTableCellInspection `json:"cell_details,omitempty"`
	PlaceholderKeys  []string                  `json:"placeholder_keys,omitempty"`
	FieldKeys        []string                  `json:"field_keys,omitempty"`
	LikelyRepeatable bool                      `json:"likely_repeatable,omitempty"`
}

type wordXMLSpan struct {
	start int
	end   int
}

func inspectDocxTables(part, partName string) []docxTableInspection {
	tableSpans := topLevelWordXMLSpans(wordElementSpans(part, wordTableTagPattern))
	tables := make([]docxTableInspection, 0, len(tableSpans))
	for tableIndex, tableSpan := range tableSpans {
		tableXML := part[tableSpan.start:tableSpan.end]
		rowSpans := topLevelWordXMLSpans(wordElementSpans(tableXML, wordRowTagPattern))
		table := docxTableInspection{Part: partName, Index: tableIndex + 1}
		for rowIndex, rowSpan := range rowSpans {
			rowXML := tableXML[rowSpan.start:rowSpan.end]
			cellSpans := topLevelWordXMLSpans(wordElementSpans(rowXML, wordCellTagPattern))
			row := docxRowInspection{Index: rowIndex + 1}
			gridColumn := 1
			for cellIndex, cellSpan := range cellSpans {
				cellXML := rowXML[cellSpan.start:cellSpan.end]
				cellText := compactDocxFieldContext(wordNodesText(parseWordTextNodes(cellXML)))
				row.Cells = append(row.Cells, cellText)
				detail := inspectDocxCell(cellXML, partName, tableIndex+1, rowIndex+1, cellIndex+1, gridColumn)
				row.CellDetails = append(row.CellDetails, detail)
				gridColumn += detail.GridSpan
				for _, match := range wordPlaceholder.FindAllStringSubmatch(cellText, -1) {
					if len(match) > 1 {
						row.PlaceholderKeys = append(row.PlaceholderKeys, strings.TrimSpace(match[1]))
					}
				}
				row.FieldKeys = append(row.FieldKeys, wordFragmentTemplateKeys(cellXML)...)
			}
			row.PlaceholderKeys = uniqueStringValues(row.PlaceholderKeys)
			row.FieldKeys = uniqueStringValues(row.FieldKeys)
			row.LikelyRepeatable = len(row.FieldKeys) > 0 && rowIndex > 0
			table.Rows = append(table.Rows, row)
		}
		inferDocxTableCellCandidates(table.Rows)
		tables = append(tables, table)
	}
	return tables
}

func wordElementSpans(input string, pattern *regexp.Regexp) []wordXMLSpan {
	matches := pattern.FindAllStringSubmatchIndex(input, -1)
	stack := make([]int, 0)
	spans := make([]wordXMLSpan, 0)
	for _, match := range matches {
		if len(match) < 4 {
			continue
		}
		closing := match[2] >= 0 && input[match[2]:match[3]] == "/"
		tag := input[match[0]:match[1]]
		selfClosing := strings.HasSuffix(strings.TrimSpace(tag), "/>")
		if !closing && !selfClosing {
			stack = append(stack, match[0])
			continue
		}
		if closing && len(stack) > 0 {
			start := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			spans = append(spans, wordXMLSpan{start: start, end: match[1]})
		}
	}
	sort.Slice(spans, func(i, j int) bool {
		if spans[i].start != spans[j].start {
			return spans[i].start < spans[j].start
		}
		return spans[i].end > spans[j].end
	})
	return spans
}

func topLevelWordXMLSpans(spans []wordXMLSpan) []wordXMLSpan {
	result := make([]wordXMLSpan, 0, len(spans))
	for index, span := range spans {
		nested := false
		for otherIndex, other := range spans {
			if index == otherIndex {
				continue
			}
			if other.start < span.start && other.end >= span.end {
				nested = true
				break
			}
		}
		if !nested {
			result = append(result, span)
		}
	}
	return result
}

func uniqueStringValues(values []string) []string {
	seen := make(map[string]bool, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		result = append(result, value)
	}
	return result
}

func wordTableAddress(part string, tableIndex, rowIndex int) string {
	return fmt.Sprintf("%s#table[%d].row[%d]", part, tableIndex, rowIndex)
}
