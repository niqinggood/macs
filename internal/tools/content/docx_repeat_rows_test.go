package content

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestSetDocxElementTextReusesExistingEmptyParagraph(t *testing.T) {
	cell := `<w:tc><w:tcPr><w:tcW w:w="2400"/></w:tcPr><w:p><w:pPr><w:spacing w:after="0"/></w:pPr></w:p></w:tc>`
	filled := setDocxElementText(cell, "林测试")
	if strings.Count(filled, "<w:p") != 2 { // one w:p and one w:pPr
		t.Fatalf("filling an empty cell added a paragraph: %s", filled)
	}
	for _, want := range []string{"林测试", `<w:spacing w:after="0"/>`, `<w:tcW w:w="2400"/>`} {
		if !strings.Contains(filled, want) {
			t.Fatalf("filled cell missing %q: %s", want, filled)
		}
	}
}

func TestExtractToolRepeatsTemplateRowFromLIMSArray(t *testing.T) {
	dir := t.TempDir()
	templatePath := filepath.Join(dir, "repeat-template.docx")
	outputPath := filepath.Join(dir, "repeat-filled.docx")
	documentXML := `<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body>
<w:tbl><w:tblPr><w:tblStyle w:val="LabGrid"/></w:tblPr>
<w:tr><w:tc><w:p><w:r><w:t>序号</w:t></w:r></w:p></w:tc><w:tc><w:p><w:r><w:t>样品编号</w:t></w:r></w:p></w:tc><w:tc><w:p><w:r><w:t>项目</w:t></w:r></w:p></w:tc><w:tc><w:p><w:r><w:t>结果</w:t></w:r></w:p></w:tc></w:tr>
<w:tr><w:trPr><w:cantSplit/></w:trPr><w:tc><w:tcPr><w:shd w:fill="EEEEEE"/></w:tcPr><w:p><w:r><w:t>{{$index}}</w:t></w:r></w:p></w:tc><w:tc><w:p><w:r><w:t>{{sample_no}}</w:t></w:r></w:p></w:tc><w:tc><w:p><w:r><w:t>{{assay}}</w:t></w:r></w:p></w:tc><w:tc><w:p><w:r><w:t>{{result}}</w:t></w:r></w:p></w:tc></w:tr>
</w:tbl></w:body></w:document>`
	writeTestDocx(t, templatePath, documentXML)

	tool := NewExtractTool(dir, false)
	receipt, err := tool.Execute(context.Background(), map[string]interface{}{
		"file":   templatePath,
		"output": outputPath,
		"source_data": map[string]interface{}{
			"records": []interface{}{
				map[string]interface{}{"sample_id": "S-001", "test_item": "pH", "test_result": "7.20"},
				map[string]interface{}{"sample_id": "S-002", "test_item": "水分", "test_result": "0.13%"},
			},
		},
		"repeat_rows": []interface{}{
			map[string]interface{}{"table_index": float64(1), "template_row": float64(2), "data_path": "records"},
		},
		"strict": true,
	})
	if err != nil {
		t.Fatalf("repeat LIMS rows: %v", err)
	}
	if !strings.Contains(receipt, "repeated rows=2") {
		t.Fatalf("repeat receipt missing: %s", receipt)
	}

	filled := readTestDocxPart(t, outputPath, "word/document.xml")
	for _, want := range []string{"S-001", "S-002", "pH", "水分", "7.20", "0.13%", `<w:shd w:fill="EEEEEE"/>`, "<w:cantSplit/>"} {
		if !strings.Contains(filled, want) {
			t.Fatalf("repeated output missing %q:\n%s", want, filled)
		}
	}
	if strings.Count(filled, "<w:tr>") != 3 {
		t.Fatalf("expected header plus two generated rows:\n%s", filled)
	}
	if strings.Contains(filled, "{{") {
		t.Fatalf("generated rows retained placeholders:\n%s", filled)
	}
}

func TestExtractToolRepeatsUnmarkedLegacyRowUsingColumnKeys(t *testing.T) {
	dir := t.TempDir()
	templatePath := filepath.Join(dir, "legacy-template.docx")
	outputPath := filepath.Join(dir, "legacy-filled.docx")
	documentXML := `<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:tbl>
<w:tr><w:tc><w:p><w:r><w:t>序号</w:t></w:r></w:p></w:tc><w:tc><w:p><w:r><w:t>样品</w:t></w:r></w:p></w:tc><w:tc><w:p><w:r><w:t>结果</w:t></w:r></w:p></w:tc></w:tr>
<w:tr><w:tc><w:p><w:r><w:rPr><w:b/></w:rPr><w:t>1</w:t></w:r></w:p></w:tc><w:tc><w:p><w:r><w:t>待填</w:t></w:r></w:p></w:tc><w:tc><w:p><w:r><w:t>待填</w:t></w:r></w:p></w:tc></w:tr>
</w:tbl></w:body></w:document>`
	writeTestDocx(t, templatePath, documentXML)

	tool := NewExtractTool(dir, false)
	_, err := tool.Execute(context.Background(), map[string]interface{}{
		"file":   templatePath,
		"output": outputPath,
		"repeat_rows": []interface{}{
			map[string]interface{}{
				"table_index": float64(1), "template_row": float64(2),
				"rows": []interface{}{
					map[string]interface{}{"sample_id": "A-1", "result": "合格"},
					map[string]interface{}{"sample_id": "A-2", "result": "不合格"},
				},
				"column_keys": []interface{}{"$index", "sample_id", "result"},
			},
		},
		"strict": true,
	})
	if err != nil {
		t.Fatalf("repeat unmarked legacy row: %v", err)
	}
	filled := readTestDocxPart(t, outputPath, "word/document.xml")
	for _, want := range []string{"A-1", "A-2", "合格", "不合格", "<w:b/>"} {
		if !strings.Contains(filled, want) {
			t.Fatalf("legacy repeated row missing %q:\n%s", want, filled)
		}
	}
	if strings.Contains(filled, "待填") {
		t.Fatalf("legacy placeholders were not replaced:\n%s", filled)
	}
}

func TestExtractToolRepeatsRowsWithMergeFieldsAndBookmarks(t *testing.T) {
	dir := t.TempDir()
	templatePath := filepath.Join(dir, "repeat-legacy-fields.docx")
	outputPath := filepath.Join(dir, "repeat-legacy-fields-filled.docx")
	documentXML := `<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:tbl>
<w:tr><w:tc><w:p><w:r><w:t>样品</w:t></w:r></w:p></w:tc><w:tc><w:p><w:r><w:t>结果</w:t></w:r></w:p></w:tc></w:tr>
<w:tr><w:tc><w:p><w:fldSimple w:instr=" MERGEFIELD sample_no "><w:r><w:rPr><w:b/></w:rPr><w:t>«sample_no»</w:t></w:r></w:fldSimple></w:p></w:tc><w:tc><w:p><w:bookmarkStart w:id="9" w:name="result"/><w:r><w:t>待填写</w:t></w:r><w:bookmarkEnd w:id="9"/></w:p></w:tc></w:tr>
</w:tbl></w:body></w:document>`
	writeTestDocx(t, templatePath, documentXML)

	inspection, err := NewExtractTool(dir, false).inspectDocxTemplate(templatePath)
	if err != nil || !strings.Contains(inspection, `"field_keys"`) || !strings.Contains(inspection, `"likely_repeatable": true`) {
		t.Fatalf("legacy repeat row was not identified: err=%v inspection=%s", err, inspection)
	}

	receipt, err := NewExtractTool(dir, false).Execute(context.Background(), map[string]interface{}{
		"file":   templatePath,
		"output": outputPath,
		"source_data": map[string]interface{}{
			"records": []interface{}{
				map[string]interface{}{"sample_id": "S-10", "test_result": "合格"},
				map[string]interface{}{"sample_id": "S-11", "test_result": "复检"},
			},
		},
		"repeat_rows": []interface{}{
			map[string]interface{}{"table_index": float64(1), "template_row": float64(2), "data_path": "records"},
		},
		"strict": true,
	})
	if err != nil {
		t.Fatalf("repeat rows with legacy fields: %v", err)
	}
	if !strings.Contains(receipt, "repeated rows=2") {
		t.Fatalf("repeat receipt=%s", receipt)
	}
	filled := readTestDocxPart(t, outputPath, "word/document.xml")
	for _, want := range []string{"S-10", "S-11", "合格", "复检", "<w:b/>"} {
		if !strings.Contains(filled, want) {
			t.Fatalf("legacy repeated output missing %q:\n%s", want, filled)
		}
	}
	for _, unwanted := range []string{"«sample_no»", "待填写", "MERGEFIELD", "<w:fldSimple"} {
		if strings.Contains(filled, unwanted) {
			t.Fatalf("legacy repeated output retained %q:\n%s", unwanted, filled)
		}
	}
}
