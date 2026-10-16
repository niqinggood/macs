package content

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDocxTableCellsInspectFillAndReuseProfile(t *testing.T) {
	dir := t.TempDir()
	templatePath := filepath.Join(dir, "arbitrary-form.docx")
	firstOutput := filepath.Join(dir, "first.docx")
	secondOutput := filepath.Join(dir, "second.docx")
	documentXML := `<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:tbl>
<w:tr><w:tc><w:p><w:r><w:t>Department</w:t></w:r></w:p></w:tc><w:tc><w:tcPr><w:gridSpan w:val="2"/><w:vMerge w:val="restart"/></w:tcPr><w:p><w:pPr><w:spacing w:after="0"/></w:pPr></w:p></w:tc></w:tr>
<w:tr><w:tc><w:p><w:r><w:t>Continuation</w:t></w:r></w:p></w:tc><w:tc><w:tcPr><w:vMerge/></w:tcPr><w:p/></w:tc></w:tr>
</w:tbl></w:body></w:document>`
	writeTestDocx(t, templatePath, documentXML)

	inspection, err := analyzeDocxTemplate(templatePath)
	if err != nil {
		t.Fatalf("inspect table cells: %v", err)
	}
	cell := inspection.Tables[0].Rows[0].CellDetails[1]
	if cell.Address != "word/document.xml#table[1].row[1].cell[2]" || !cell.Candidate || cell.Label != "Department" {
		t.Fatalf("unexpected inferred table cell: %+v", cell)
	}
	if cell.GridSpan != 2 || cell.VerticalMerge != "restart" {
		t.Fatalf("table structure not exposed: %+v", cell)
	}
	continuation := inspection.Tables[0].Rows[1].CellDetails[1]
	if continuation.Candidate || continuation.VerticalMerge != "continue" {
		t.Fatalf("merge continuation must not be a candidate: %+v", continuation)
	}
	_, _, preview := buildDocxDataMapping(inspection, map[string]interface{}{
		"experiment": map[string]interface{}{"department": "preview"},
	}, nil, nil, nil, defaultDocxMappingThreshold)
	if len(preview.TableCellSuggestions) != 1 || preview.TableCellSuggestions[0].Status != "suggested" || preview.TableCellSuggestions[0].SourcePath != "experiment.department" {
		t.Fatalf("unexpected table-cell mapping suggestion: %+v", preview.TableCellSuggestions)
	}

	tool := NewExtractTool(dir, false)
	firstReceipt, err := tool.Execute(context.Background(), map[string]interface{}{
		"file":                  templatePath,
		"output":                firstOutput,
		"save_template_profile": true,
		"source_data": map[string]interface{}{
			"experiment": map[string]interface{}{"department": "SECRET-A"},
		},
		"table_cells": []interface{}{
			map[string]interface{}{
				"table_index": float64(1), "row": float64(1), "cell": float64(2),
				"source_path": "experiment.department",
			},
		},
		"strict": true,
	})
	if err != nil {
		t.Fatalf("fill fixed table cell: %v", err)
	}
	if !strings.Contains(firstReceipt, "table cells=1") || !strings.Contains(firstReceipt, "profile=saved:") {
		t.Fatalf("table-cell receipt missing evidence: %s", firstReceipt)
	}
	firstXML := readTestDocxPart(t, firstOutput, "word/document.xml")
	for _, want := range []string{"SECRET-A", `<w:gridSpan w:val="2"/>`, `<w:vMerge w:val="restart"/>`, `<w:spacing w:after="0"/>`} {
		if !strings.Contains(firstXML, want) {
			t.Fatalf("filled cell missing %q:\n%s", want, firstXML)
		}
	}

	fingerprint, err := docxTemplateFingerprint(templatePath)
	if err != nil {
		t.Fatalf("fingerprint template: %v", err)
	}
	profileJSON, err := os.ReadFile(docxTemplateProfilePath(dir, fingerprint))
	if err != nil {
		t.Fatalf("read table-cell profile: %v", err)
	}
	if strings.Contains(string(profileJSON), "SECRET-A") || !strings.Contains(string(profileJSON), `"source_path": "experiment.department"`) {
		t.Fatalf("profile must store the path but not the resolved value:\n%s", profileJSON)
	}

	secondReceipt, err := tool.Execute(context.Background(), map[string]interface{}{
		"file":                 templatePath,
		"output":               secondOutput,
		"use_template_profile": true,
		"source_data": map[string]interface{}{
			"experiment": map[string]interface{}{"department": "NEW-B"},
		},
		"strict": true,
	})
	if err != nil {
		t.Fatalf("reuse table-cell profile: %v", err)
	}
	if !strings.Contains(secondReceipt, "profile=loaded:") || !strings.Contains(secondReceipt, "table cells=1") {
		t.Fatalf("profile reuse receipt missing evidence: %s", secondReceipt)
	}
	secondXML := readTestDocxPart(t, secondOutput, "word/document.xml")
	if !strings.Contains(secondXML, "NEW-B") || strings.Contains(secondXML, "SECRET-A") {
		t.Fatalf("profile reuse did not resolve fresh source data:\n%s", secondXML)
	}
}

func TestDocxTableCellsRejectVerticalMergeContinuation(t *testing.T) {
	dir := t.TempDir()
	templatePath := filepath.Join(dir, "merged-form.docx")
	outputPath := filepath.Join(dir, "merged-filled.docx")
	documentXML := `<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:tbl>
<w:tr><w:tc><w:tcPr><w:vMerge w:val="restart"/></w:tcPr><w:p/></w:tc></w:tr>
<w:tr><w:tc><w:tcPr><w:vMerge/></w:tcPr><w:p/></w:tc></w:tr>
</w:tbl></w:body></w:document>`
	writeTestDocx(t, templatePath, documentXML)

	_, err := NewExtractTool(dir, false).Execute(context.Background(), map[string]interface{}{
		"file": templatePath, "output": outputPath,
		"table_cells": []interface{}{
			map[string]interface{}{
				"table_index": float64(1), "row": float64(2), "cell": float64(1), "value": "invalid target",
			},
		},
		"strict": true,
	})
	if err == nil || !strings.Contains(err.Error(), "vertical-merge continuation") {
		t.Fatalf("expected structural merge error, got %v", err)
	}
	if _, statErr := os.Stat(outputPath); !os.IsNotExist(statErr) {
		t.Fatalf("failed strict fill must not replace output; stat=%v", statErr)
	}
}

func TestDocxTableCellsRejectRepeatRowOverlap(t *testing.T) {
	err := validateDocxTablePlanConflicts(
		[]docxTableCellPlan{{Part: "word/document.xml", TableIndex: 2, Row: 4, Cell: 3}},
		[]docxRepeatRowPlan{{Part: "word/document.xml", TableIndex: 2, TemplateRow: 4}},
	)
	if err == nil || !strings.Contains(err.Error(), "overlaps a repeat_rows template row") {
		t.Fatalf("expected structural plan conflict, got %v", err)
	}
}
