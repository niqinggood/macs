package content

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

var (
	wordGridSpanPattern = regexp.MustCompile(`(?i)<w:gridSpan\b[^>]*\bw:val="(\d+)"`)
	wordVMergePattern   = regexp.MustCompile(`(?i)<w:vMerge\b([^>]*)/?>`)
	wordValPattern      = regexp.MustCompile(`(?i)\bw:val="([^"]+)"`)
)

// docxTableCellInspection describes a physical cell in the source OOXML. The
// address is deliberately based on the original template, so an agent can
// produce a reusable plan without knowing anything about the template's text.
type docxTableCellInspection struct {
	Index         int     `json:"index"`
	Address       string  `json:"address"`
	Text          string  `json:"text,omitempty"`
	GridColumn    int     `json:"grid_column"`
	GridSpan      int     `json:"grid_span,omitempty"`
	VerticalMerge string  `json:"vertical_merge,omitempty"`
	Candidate     bool    `json:"fillable_candidate,omitempty"`
	Label         string  `json:"inferred_label,omitempty"`
	Confidence    float64 `json:"confidence,omitempty"`
	Reason        string  `json:"reason,omitempty"`
}

// docxTableCellPlan is a data-driven write instruction. It contains no
// template-specific rules: the address comes from inspect_template and the
// value comes from either source_data or an explicit caller value.
type docxTableCellPlan struct {
	ID         int
	Part       string
	TableIndex int
	Row        int
	Cell       int
	SourcePath string
	Value      interface{}
	HasValue   bool
}

type docxTableCellStats struct {
	appliedPlans map[int]bool
	valuesFilled int
}

func newDocxTableCellStats() *docxTableCellStats {
	return &docxTableCellStats{appliedPlans: make(map[int]bool)}
}

func parseDocxTableCellPlans(raw []interface{}, sourceData map[string]interface{}) ([]docxTableCellPlan, error) {
	plans := make([]docxTableCellPlan, 0, len(raw))
	addresses := make(map[string]bool)
	for index, item := range raw {
		definition, ok := item.(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("table_cells[%d] must be an object", index)
		}
		plan := docxTableCellPlan{
			ID:         index + 1,
			Part:       stringMapValue(definition, "part", "word/document.xml"),
			TableIndex: intMapValue(definition, "table_index"),
			Row:        intMapValue(definition, "row"),
			Cell:       intMapValue(definition, "cell"),
			SourcePath: stringMapValue(definition, "source_path", ""),
		}
		plan.Value, plan.HasValue = definition["value"]
		if plan.TableIndex <= 0 || plan.Row <= 0 || plan.Cell <= 0 {
			return nil, fmt.Errorf("table_cells[%d] requires positive 1-based table_index, row, and cell", index)
		}
		if plan.SourcePath != "" && plan.HasValue {
			return nil, fmt.Errorf("table_cells[%d] must use either source_path or value, not both", index)
		}
		if plan.SourcePath == "" && !plan.HasValue {
			return nil, fmt.Errorf("table_cells[%d] requires source_path or value", index)
		}
		if plan.SourcePath != "" {
			value, found := lookupNestedDocxValue(sourceData, plan.SourcePath)
			if !found {
				return nil, fmt.Errorf("table_cells[%d] source_path %q not found", index, plan.SourcePath)
			}
			plan.Value = value
			plan.HasValue = true
		}
		if !isDocxScalarValue(plan.Value) {
			return nil, fmt.Errorf("table_cells[%d] value must be scalar", index)
		}

		address := wordTableCellAddress(normalizeDocxPartName(plan.Part), plan.TableIndex, plan.Row, plan.Cell)
		if addresses[address] {
			return nil, fmt.Errorf("table_cells[%d] duplicates %s", index, address)
		}
		addresses[address] = true
		plans = append(plans, plan)
	}
	return plans, nil
}

func validateDocxTablePlanConflicts(cells []docxTableCellPlan, repeats []docxRepeatRowPlan) error {
	for _, cell := range cells {
		for _, repeat := range repeats {
			if normalizeDocxPartName(cell.Part) == normalizeDocxPartName(repeat.Part) &&
				cell.TableIndex == repeat.TableIndex && cell.Row == repeat.TemplateRow {
				return fmt.Errorf("%s overlaps a repeat_rows template row; use row placeholders or column_keys for repeated data", wordTableCellAddress(cell.Part, cell.TableIndex, cell.Row, cell.Cell))
			}
		}
	}
	return nil
}

func isDocxScalarValue(value interface{}) bool {
	switch value.(type) {
	case nil, string, bool,
		int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64,
		float32, float64:
		return true
	default:
		return false
	}
}

func applyDocxTableCells(part, partName string, plans []docxTableCellPlan, stats *docxTableCellStats) (string, []string) {
	var issues []string
	relevant := make([]docxTableCellPlan, 0)
	for _, plan := range plans {
		if normalizeDocxPartName(plan.Part) == normalizeDocxPartName(partName) {
			relevant = append(relevant, plan)
		}
	}
	// Descending source addresses keep all earlier indices stable while cell
	// text changes the length of the XML document.
	sort.SliceStable(relevant, func(i, j int) bool {
		if relevant[i].TableIndex != relevant[j].TableIndex {
			return relevant[i].TableIndex > relevant[j].TableIndex
		}
		if relevant[i].Row != relevant[j].Row {
			return relevant[i].Row > relevant[j].Row
		}
		return relevant[i].Cell > relevant[j].Cell
	})

	for _, plan := range relevant {
		address := wordTableCellAddress(plan.Part, plan.TableIndex, plan.Row, plan.Cell)
		tables := topLevelWordXMLSpans(wordElementSpans(part, wordTableTagPattern))
		if plan.TableIndex > len(tables) {
			issues = append(issues, address+": table not found")
			continue
		}
		tableSpan := tables[plan.TableIndex-1]
		tableXML := part[tableSpan.start:tableSpan.end]
		rows := topLevelWordXMLSpans(wordElementSpans(tableXML, wordRowTagPattern))
		if plan.Row > len(rows) {
			issues = append(issues, address+": row not found")
			continue
		}
		rowSpan := rows[plan.Row-1]
		rowXML := tableXML[rowSpan.start:rowSpan.end]
		cells := topLevelWordXMLSpans(wordElementSpans(rowXML, wordCellTagPattern))
		if plan.Cell > len(cells) {
			issues = append(issues, address+": cell not found")
			continue
		}
		cellSpan := cells[plan.Cell-1]
		cellXML := rowXML[cellSpan.start:cellSpan.end]
		if docxVerticalMergeState(cellXML) == "continue" {
			issues = append(issues, address+": vertical-merge continuation cannot be filled; target the restart cell")
			continue
		}
		value := ""
		if plan.Value != nil {
			value = fmt.Sprint(plan.Value)
		}
		cellXML = setDocxElementText(cellXML, value)
		rowXML = rowXML[:cellSpan.start] + cellXML + rowXML[cellSpan.end:]
		tableXML = tableXML[:rowSpan.start] + rowXML + tableXML[rowSpan.end:]
		part = part[:tableSpan.start] + tableXML + part[tableSpan.end:]
		stats.appliedPlans[plan.ID] = true
		stats.valuesFilled++
	}
	return part, issues
}

func inspectDocxCell(cellXML, part string, tableIndex, rowIndex, cellIndex, gridColumn int) docxTableCellInspection {
	gridSpan := 1
	if match := wordGridSpanPattern.FindStringSubmatch(cellXML); len(match) > 1 {
		if parsed, err := strconv.Atoi(match[1]); err == nil && parsed > 0 {
			gridSpan = parsed
		}
	}
	return docxTableCellInspection{
		Index:         cellIndex,
		Address:       wordTableCellAddress(part, tableIndex, rowIndex, cellIndex),
		Text:          compactDocxFieldContext(wordNodesText(parseWordTextNodes(cellXML))),
		GridColumn:    gridColumn,
		GridSpan:      gridSpan,
		VerticalMerge: docxVerticalMergeState(cellXML),
	}
}

func inferDocxTableCellCandidates(rows []docxRowInspection) {
	for rowIndex := range rows {
		for cellIndex := range rows[rowIndex].CellDetails {
			cell := &rows[rowIndex].CellDetails[cellIndex]
			if strings.TrimSpace(cell.Text) != "" || cell.VerticalMerge == "continue" {
				continue
			}
			if cellIndex > 0 {
				label := strings.TrimSpace(rows[rowIndex].CellDetails[cellIndex-1].Text)
				if label != "" {
					cell.Candidate = true
					cell.Label = label
					cell.Confidence = 0.94
					cell.Reason = "blank cell follows a labeled cell in the same row"
					continue
				}
			}
			if rowIndex > 0 {
				above := docxCellAtGridColumn(rows[rowIndex-1].CellDetails, cell.GridColumn)
				label := ""
				if above != nil {
					label = strings.TrimSpace(above.Text)
				}
				if label != "" {
					cell.Candidate = true
					cell.Label = label
					cell.Confidence = 0.78
					cell.Reason = "blank cell is below a non-empty cell in the same physical column"
				}
			}
		}
	}
}

func docxCellAtGridColumn(cells []docxTableCellInspection, gridColumn int) *docxTableCellInspection {
	for index := range cells {
		span := cells[index].GridSpan
		if span <= 0 {
			span = 1
		}
		if gridColumn >= cells[index].GridColumn && gridColumn < cells[index].GridColumn+span {
			return &cells[index]
		}
	}
	return nil
}

func docxVerticalMergeState(cellXML string) string {
	match := wordVMergePattern.FindStringSubmatch(cellXML)
	if len(match) == 0 {
		return ""
	}
	if value := wordValPattern.FindStringSubmatch(match[0]); len(value) > 1 {
		if strings.EqualFold(strings.TrimSpace(value[1]), "restart") {
			return "restart"
		}
	}
	return "continue"
}

func wordTableCellAddress(part string, tableIndex, rowIndex, cellIndex int) string {
	return fmt.Sprintf("%s#table[%d].row[%d].cell[%d]", part, tableIndex, rowIndex, cellIndex)
}
