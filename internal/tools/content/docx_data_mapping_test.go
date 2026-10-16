package content

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildDocxDataMappingMatchesLaboratoryAliases(t *testing.T) {
	inspection := docxTemplateInspection{
		Placeholders:    []docxTemplateInspectionItem{{Key: "样品编号"}},
		ContentControls: []docxTemplateInspectionItem{{Key: "检测人"}},
		Underlined:      []docxTemplateInspectionItem{{Label: "批号"}},
	}
	source := map[string]interface{}{
		"lims": map[string]interface{}{
			"sample_no": "S-001",
			"operator":  "张三",
			"lot_no":    "LOT-8",
		},
	}
	templateData, underlineData, report := buildDocxDataMapping(inspection, source, nil, nil, nil, 0)
	if templateData["样品编号"] != "S-001" || templateData["检测人"] != "张三" || underlineData["批号"] != "LOT-8" {
		t.Fatalf("laboratory aliases were not mapped: template=%#v underline=%#v report=%#v", templateData, underlineData, report)
	}
	if got := countMappedDocxFields(report); got != 3 {
		t.Fatalf("expected 3 mapped fields, got %d: %#v", got, report)
	}
	if len(unresolvedDocxMappings(report)) != 0 {
		t.Fatalf("unexpected unresolved mappings: %#v", report)
	}
}

func TestBuildDocxDataMappingReportsAmbiguityAndExplicitMappingResolvesIt(t *testing.T) {
	inspection := docxTemplateInspection{Placeholders: []docxTemplateInspectionItem{{Key: "样品编号"}}}
	source := map[string]interface{}{"sample_no": "S-1", "specimen_id": "S-2"}
	_, _, ambiguous := buildDocxDataMapping(inspection, source, nil, nil, nil, 0)
	if len(ambiguous.AmbiguousFields) != 1 || ambiguous.Fields[0].Status != "ambiguous" {
		t.Fatalf("expected ambiguous alias candidates: %#v", ambiguous)
	}

	templateData, _, resolved := buildDocxDataMapping(
		inspection, source, map[string]string{"样品编号": "specimen_id"}, nil, nil, 0,
	)
	if templateData["样品编号"] != "S-2" || resolved.Fields[0].Status != "mapped" || resolved.Fields[0].Confidence != 1 {
		t.Fatalf("explicit mapping did not resolve ambiguity: data=%#v report=%#v", templateData, resolved)
	}
}

func TestExtractToolAutoMapsLIMSDataAndFillsDocx(t *testing.T) {
	dir := t.TempDir()
	templatePath := filepath.Join(dir, "auto-map.docx")
	outputPath := filepath.Join(dir, "auto-map-filled.docx")
	documentXML := `<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body>
<w:p><w:r><w:t>样品：{{样品编号}}</w:t></w:r></w:p>
<w:p><w:r><w:t>检测人：</w:t></w:r><w:r><w:rPr><w:u w:val="single"/></w:rPr><w:t/></w:r></w:p>
</w:body></w:document>`
	writeTestDocx(t, templatePath, documentXML)

	tool := NewExtractTool(dir, false)
	receipt, err := tool.Execute(context.Background(), map[string]interface{}{
		"file":   templatePath,
		"output": outputPath,
		"source_data": map[string]interface{}{
			"sample_no": "S-2026-99",
			"operator":  "李四",
		},
		"strict": true,
	})
	if err != nil {
		t.Fatalf("auto-map and fill: %v", err)
	}
	if !strings.Contains(receipt, "auto-mapped fields=2") {
		t.Fatalf("mapping receipt missing: %s", receipt)
	}
	filled := readTestDocxPart(t, outputPath, "word/document.xml")
	if !strings.Contains(filled, "S-2026-99") || !strings.Contains(filled, "李四") {
		t.Fatalf("mapped values missing from DOCX: %s", filled)
	}

	previewOutput := filepath.Join(dir, "must-not-exist.docx")
	preview, err := tool.Execute(context.Background(), map[string]interface{}{
		"file":            templatePath,
		"output":          previewOutput,
		"source_data":     map[string]interface{}{"sample_no": "S-1"},
		"preview_mapping": true,
	})
	if err != nil {
		t.Fatalf("preview mapping: %v", err)
	}
	if !strings.Contains(preview, `"mapping"`) || !strings.Contains(preview, `"confidence"`) {
		t.Fatalf("preview is missing mapping evidence: %s", preview)
	}
	if _, statErr := os.Stat(previewOutput); !os.IsNotExist(statErr) {
		t.Fatalf("preview unexpectedly wrote output: %v", statErr)
	}
}

func TestBuildDocxDataMappingIncludesLegacyWordFields(t *testing.T) {
	inspection := docxTemplateInspection{
		MergeFields: []docxTemplateInspectionItem{{Key: "报告编号"}, {Key: "样品名称"}},
		Bookmarks:   []docxTemplateInspectionItem{{Key: "委托单位", Fillable: true}},
		ContentControls: []docxTemplateInspectionItem{
			{Key: "是否合格", Type: "checkbox"},
		},
	}
	source := map[string]interface{}{
		"report_no":    "R-88",
		"product_name": "标准样品",
		"company":      "某企业",
		"是否合格":         true,
	}
	templateData, _, report := buildDocxDataMapping(inspection, source, nil, nil, nil, 0)
	for key, want := range map[string]interface{}{
		"报告编号": "R-88", "样品名称": "标准样品", "委托单位": "某企业", "是否合格": true,
	} {
		if got := templateData[key]; got != want {
			t.Fatalf("legacy field %s=%#v want %#v; report=%#v", key, got, want, report)
		}
	}
	if got := countMappedDocxFields(report); got != 4 || len(unresolvedDocxMappings(report)) != 0 {
		t.Fatalf("legacy mappings=%d unresolved=%v report=%#v", got, unresolvedDocxMappings(report), report)
	}
}

func TestBuildDocxDataMappingIgnoresNavigationBookmarks(t *testing.T) {
	inspection := docxTemplateInspection{
		Bookmarks: []docxTemplateInspectionItem{{Key: "ResultsSection", Context: "第三章 检测结果", Fillable: false}},
	}
	templateData, _, report := buildDocxDataMapping(
		inspection,
		map[string]interface{}{"ResultsSection": "不应覆盖章节标题"},
		nil,
		nil,
		nil,
		0,
	)
	if len(templateData) != 0 || len(report.Fields) != 0 || len(report.UnmatchedFields) != 0 {
		t.Fatalf("navigation bookmark was treated as a fill target: data=%#v report=%#v", templateData, report)
	}
}
