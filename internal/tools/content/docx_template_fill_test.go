package content

import (
	"archive/zip"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFillDocxTemplateHandlesSplitRunsContentControlsAndUnderlines(t *testing.T) {
	dir := t.TempDir()
	templatePath := filepath.Join(dir, "lims-template.docx")
	outputPath := filepath.Join(dir, "lims-filled.docx")
	documentXML := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body>
<w:tbl><w:tr><w:tc><w:p><w:r><w:t>样品：{{样</w:t></w:r><w:r><w:rPr><w:b/></w:rPr><w:t>品编号}}</w:t></w:r></w:p></w:tc></w:tr></w:tbl>
<w:sdt><w:sdtPr><w:tag w:val="检测人"/></w:sdtPr><w:sdtContent><w:p><w:r><w:t>点击此处输入</w:t></w:r></w:p></w:sdtContent></w:sdt>
<w:p><w:r><w:t>批号：</w:t></w:r><w:r><w:rPr><w:u w:val="single"/></w:rPr><w:t/></w:r></w:p>
<w:p><w:r><w:t>复核日期：</w:t></w:r><w:r><w:t>________</w:t></w:r></w:p>
</w:body></w:document>`
	writeTestDocx(t, templatePath, documentXML)

	tool := NewExtractTool(dir, false)
	receipt, err := tool.fillDocxTemplateAdvanced(
		templatePath,
		map[string]interface{}{"样品编号": "S-2026-001", "检测人": "张三"},
		map[string]interface{}{"批号": "LOT-88"},
		[]interface{}{"2026-09-13"},
		outputPath,
		true,
	)
	if err != nil {
		t.Fatalf("fill complex DOCX template: %v", err)
	}
	for _, want := range []string{"template/content-control replacements=2", "underlined fields=2"} {
		if !strings.Contains(receipt, want) {
			t.Fatalf("receipt missing %q: %s", want, receipt)
		}
	}

	filledXML := readTestDocxPart(t, outputPath, "word/document.xml")
	for _, want := range []string{"S-2026-001", "张三", "LOT-88", "2026-09-13", "<w:b/>", `<w:u w:val="single"/>`} {
		if !strings.Contains(filledXML, want) {
			t.Fatalf("filled document missing %q:\n%s", want, filledXML)
		}
	}
	for _, unwanted := range []string{"{{样品编号}}", "点击此处输入", "________"} {
		if strings.Contains(filledXML, unwanted) {
			t.Fatalf("filled document retained %q:\n%s", unwanted, filledXML)
		}
	}
}

func TestFillDocxTemplateGroupsSplitUnderlineRunsAndKeepsLabelsSeparate(t *testing.T) {
	dir := t.TempDir()
	templatePath := filepath.Join(dir, "split-underline-template.docx")
	outputPath := filepath.Join(dir, "split-underline-filled.docx")
	documentXML := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p>
<w:r><w:t>申请人：</w:t></w:r>
<w:r><w:rPr><w:u w:val="single"/></w:rPr><w:t xml:space="preserve">        </w:t></w:r>
<w:r><w:rPr><w:u w:val="single"/></w:rPr><w:t xml:space="preserve">  </w:t></w:r>
<w:r><w:t xml:space="preserve">  联系电话：</w:t></w:r>
<w:r><w:rPr><w:u w:val="single"/></w:rPr><w:t xml:space="preserve">          </w:t></w:r>
<w:r><w:t xml:space="preserve">  申请日期：</w:t></w:r>
<w:r><w:rPr><w:u w:val="single"/></w:rPr><w:t xml:space="preserve">      </w:t></w:r>
<w:r><w:rPr><w:u w:val="single"/></w:rPr><w:t xml:space="preserve">  </w:t></w:r>
</w:p></w:body></w:document>`
	writeTestDocx(t, templatePath, documentXML)

	inspection, err := analyzeDocxTemplate(templatePath)
	if err != nil {
		t.Fatalf("inspect split underline template: %v", err)
	}
	if len(inspection.Underlined) != 3 {
		t.Fatalf("underlined fields=%d, want 3: %#v", len(inspection.Underlined), inspection.Underlined)
	}
	for index, want := range []string{"申请人", "联系电话", "申请日期"} {
		if inspection.Underlined[index].Label != want {
			t.Fatalf("underlined field %d label=%q, want %q", index, inspection.Underlined[index].Label, want)
		}
	}

	receipt, err := NewExtractTool(dir, false).fillDocxTemplateAdvanced(
		templatePath,
		nil,
		map[string]interface{}{
			"申请人":  "林测试",
			"联系电话": "010-5550-0101",
			"申请日期": "2026-09-13",
		},
		nil,
		outputPath,
		true,
	)
	if err != nil {
		t.Fatalf("fill split underline template: %v", err)
	}
	if !strings.Contains(receipt, "underlined fields=3") || !strings.Contains(receipt, "completeness=verified") {
		t.Fatalf("unexpected receipt: %s", receipt)
	}
	filledXML := readTestDocxPart(t, outputPath, "word/document.xml")
	for _, want := range []string{"林测试", "010-5550-0101", "2026-09-13"} {
		if strings.Count(filledXML, want) != 1 {
			t.Fatalf("filled document value %q count != 1:\n%s", want, filledXML)
		}
	}
}

func TestFillDocxTemplateStrictModeKeepsExistingOutputOnUnmatchedField(t *testing.T) {
	dir := t.TempDir()
	templatePath := filepath.Join(dir, "template.docx")
	outputPath := filepath.Join(dir, "existing.docx")
	writeTestDocx(t, templatePath, `<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:r><w:t>{{known}}</w:t></w:r></w:p></w:body></w:document>`)
	if err := os.WriteFile(outputPath, []byte("original-output"), 0o644); err != nil {
		t.Fatalf("write existing output: %v", err)
	}

	tool := NewExtractTool(dir, false)
	_, err := tool.fillDocxTemplateAdvanced(
		templatePath,
		map[string]interface{}{"missing": "value"},
		nil,
		nil,
		outputPath,
		true,
	)
	if err == nil || !strings.Contains(err.Error(), "template:missing") {
		t.Fatalf("strict mode should identify unmatched key, got %v", err)
	}
	content, readErr := os.ReadFile(outputPath)
	if readErr != nil {
		t.Fatalf("read existing output: %v", readErr)
	}
	if string(content) != "original-output" {
		t.Fatalf("strict failure replaced existing output: %q", content)
	}
}

func TestFillDocxTemplateStrictModeRejectsTemplateFieldsMissingFromInput(t *testing.T) {
	dir := t.TempDir()
	templatePath := filepath.Join(dir, "reverse-strict.docx")
	outputPath := filepath.Join(dir, "existing.docx")
	writeTestDocx(t, templatePath, `<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:r><w:t>{{known}} / {{required_but_missing}}</w:t></w:r></w:p></w:body></w:document>`)
	if err := os.WriteFile(outputPath, []byte("keep-me"), 0o644); err != nil {
		t.Fatalf("write existing output: %v", err)
	}

	tool := NewExtractTool(dir, false)
	_, err := tool.fillDocxTemplateAdvanced(templatePath, map[string]interface{}{"known": "filled"}, nil, nil, outputPath, true)
	if err == nil || !strings.Contains(err.Error(), "remaining placeholder:required_but_missing") {
		t.Fatalf("strict completeness should reject an unprovided template field, got %v", err)
	}
	content, readErr := os.ReadFile(outputPath)
	if readErr != nil || string(content) != "keep-me" {
		t.Fatalf("strict completeness replaced existing output: content=%q err=%v", content, readErr)
	}
}

func TestInspectDocxTemplateInventoriesComplexFieldsWithoutWriting(t *testing.T) {
	dir := t.TempDir()
	templatePath := filepath.Join(dir, "inspect.docx")
	documentXML := `<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body>
<w:p><w:r><w:t>{{样</w:t></w:r><w:r><w:t>品编号}}</w:t></w:r></w:p>
<w:sdt><w:sdtPr><w:alias w:val="检测人"/></w:sdtPr><w:sdtContent><w:p><w:r><w:t>请输入</w:t></w:r></w:p></w:sdtContent></w:sdt>
<w:p><w:r><w:t>批号：</w:t></w:r><w:r><w:rPr><w:u w:val="single"/></w:rPr><w:t/></w:r></w:p>
<w:tbl><w:tr><w:tc><w:p><w:r><w:t>检测项目</w:t></w:r></w:p></w:tc><w:tc><w:p><w:r><w:t>结果</w:t></w:r></w:p></w:tc></w:tr>
<w:tr><w:tc><w:p><w:r><w:t>{{test_item}}</w:t></w:r></w:p></w:tc><w:tc><w:p><w:r><w:t>{{result}}</w:t></w:r></w:p></w:tc></w:tr></w:tbl>
<w:p><w:r><w:t>日期：________</w:t></w:r></w:p>
</w:body></w:document>`
	writeTestDocx(t, templatePath, documentXML)

	tool := NewExtractTool(dir, false)
	result, err := tool.inspectDocxTemplate(templatePath)
	if err != nil {
		t.Fatalf("inspect DOCX template: %v", err)
	}
	for _, want := range []string{`"key": "样品编号"`, `"key": "检测人"`, `"label": "批号"`, `"label": "日期"`, `"tables"`, `"likely_repeatable": true`, `"test_item"`} {
		if !strings.Contains(result, want) {
			t.Fatalf("inspection missing %s:\n%s", want, result)
		}
	}
}

func TestFillDocxTemplateHandlesMergeFieldsBookmarksAndCheckboxes(t *testing.T) {
	dir := t.TempDir()
	templatePath := filepath.Join(dir, "legacy-fields.docx")
	outputPath := filepath.Join(dir, "legacy-fields-filled.docx")
	documentXML := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main" xmlns:w14="http://schemas.microsoft.com/office/word/2010/wordml"><w:body>
<w:p><w:fldSimple w:instr=" MERGEFIELD report_no \\* MERGEFORMAT "><w:r><w:t>«report_no»</w:t></w:r></w:fldSimple></w:p>
<w:p>
  <w:r><w:fldChar w:fldCharType="begin"/></w:r>
  <w:r><w:instrText xml:space="preserve"> MERGEFIELD &quot;sample_name&quot; \\* MERGEFORMAT </w:instrText></w:r>
  <w:r><w:fldChar w:fldCharType="separate"/></w:r>
  <w:r><w:rPr><w:b/></w:rPr><w:t>«sample_name»</w:t></w:r>
  <w:r><w:fldChar w:fldCharType="end"/></w:r>
</w:p>
<w:p><w:r><w:t>客户：</w:t></w:r><w:bookmarkStart w:id="7" w:name="customer"/><w:r><w:rPr><w:i/></w:rPr><w:t>待填写</w:t></w:r><w:bookmarkEnd w:id="7"/></w:p>
<w:sdt><w:sdtPr><w:tag w:val="qualified"/><w14:checkbox><w14:checked w14:val="0"/><w14:checkedState w14:val="2612"/><w14:uncheckedState w14:val="2610"/></w14:checkbox></w:sdtPr><w:sdtContent><w:r><w:sym w:font="MS Gothic" w:char="2610"/><w:t>☐</w:t></w:r></w:sdtContent></w:sdt>
</w:body></w:document>`
	writeTestDocx(t, templatePath, documentXML)

	inspection, err := NewExtractTool(dir, false).inspectDocxTemplate(templatePath)
	if err != nil {
		t.Fatalf("inspect legacy fields: %v", err)
	}
	for _, want := range []string{`"merge_fields"`, `"key": "report_no"`, `"key": "sample_name"`, `"bookmarks"`, `"key": "customer"`, `"type": "checkbox"`, `"context": "unchecked"`} {
		if !strings.Contains(inspection, want) {
			t.Fatalf("legacy field inspection missing %q:\n%s", want, inspection)
		}
	}

	receipt, err := NewExtractTool(dir, false).fillDocxTemplateAdvanced(
		templatePath,
		map[string]interface{}{
			"report_no":   "R-2026-100",
			"sample_name": "标准物质",
			"customer":    "某检测企业",
			"qualified":   true,
		},
		nil,
		nil,
		outputPath,
		true,
	)
	if err != nil {
		t.Fatalf("fill legacy DOCX fields: %v", err)
	}
	if !strings.Contains(receipt, "template/content-control replacements=4") {
		t.Fatalf("legacy field receipt=%s", receipt)
	}

	filledXML := readTestDocxPart(t, outputPath, "word/document.xml")
	for _, want := range []string{"R-2026-100", "标准物质", "某检测企业", `<w14:checked w14:val="1"/>`, `w:char="2612"`, "☒", "<w:b/>", "<w:i/>"} {
		if !strings.Contains(filledXML, want) {
			t.Fatalf("filled legacy document missing %q:\n%s", want, filledXML)
		}
	}
	for _, unwanted := range []string{"«report_no»", "«sample_name»", "待填写", `w:char="2610"`, ">☐<", "MERGEFIELD", "<w:fldSimple", "<w:fldChar"} {
		if strings.Contains(filledXML, unwanted) {
			t.Fatalf("filled legacy document retained %q:\n%s", unwanted, filledXML)
		}
	}
}

func TestFillWordCheckboxCanClearAnExistingSelection(t *testing.T) {
	block := `<w:sdt xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main" xmlns:w14="http://schemas.microsoft.com/office/word/2010/wordml"><w:sdtPr><w:tag w:val="selected"/><w14:checkbox><w14:checked w14:val="1"/><w14:checkedState w14:val="2611"/><w14:uncheckedState w14:val="2610"/></w14:checkbox></w:sdtPr><w:sdtContent><w:r><w:sym w:char="2611"/><w:t>☑</w:t></w:r></w:sdtContent></w:sdt>`
	updated, ok := fillWordCheckbox(block, "false")
	if !ok {
		t.Fatal("checkbox false value was not recognized")
	}
	for _, want := range []string{`w14:checked w14:val="0"`, `w:char="2610"`, "☐"} {
		if !strings.Contains(updated, want) {
			t.Fatalf("cleared checkbox missing %q: %s", want, updated)
		}
	}
	for _, unwanted := range []string{`w14:checked w14:val="1"`, `w:char="2611"`, "☑"} {
		if strings.Contains(updated, unwanted) {
			t.Fatalf("cleared checkbox retained %q: %s", unwanted, updated)
		}
	}
}

func TestFillDocxTemplateFindsContentControlsNestedInRepeatingSections(t *testing.T) {
	dir := t.TempDir()
	templatePath := filepath.Join(dir, "nested-content-control.docx")
	outputPath := filepath.Join(dir, "nested-content-control-filled.docx")
	documentXML := `<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main" xmlns:w15="http://schemas.microsoft.com/office/word/2012/wordml"><w:body>
<w:sdt><w:sdtPr><w15:repeatingSection/></w:sdtPr><w:sdtContent><w:tbl><w:tr><w:tc><w:p>
<w:sdt><w:sdtPr><w:tag w:val="检测结论"/></w:sdtPr><w:sdtContent><w:r><w:rPr><w:b/></w:rPr><w:t>点击此处输入</w:t></w:r></w:sdtContent></w:sdt>
</w:p></w:tc></w:tr></w:tbl></w:sdtContent></w:sdt>
</w:body></w:document>`
	writeTestDocx(t, templatePath, documentXML)

	tool := NewExtractTool(dir, false)
	inspection, err := tool.inspectDocxTemplate(templatePath)
	if err != nil || !strings.Contains(inspection, `"key": "检测结论"`) {
		t.Fatalf("nested content control was not inspected: err=%v inspection=%s", err, inspection)
	}
	_, err = tool.fillDocxTemplateAdvanced(
		templatePath,
		map[string]interface{}{"检测结论": "符合要求"},
		nil,
		nil,
		outputPath,
		true,
	)
	if err != nil {
		t.Fatalf("fill nested content control: %v", err)
	}
	filled := readTestDocxPart(t, outputPath, "word/document.xml")
	if !strings.Contains(filled, "符合要求") || strings.Contains(filled, "点击此处输入") || !strings.Contains(filled, "<w15:repeatingSection/>") || !strings.Contains(filled, "<w:b/>") {
		t.Fatalf("nested content control fill damaged structure:\n%s", filled)
	}
}

func TestFillWordDropdownPreservesItsListDefinition(t *testing.T) {
	part := `<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:sdt><w:sdtPr><w:tag w:val="检测结论"/><w:dropDownList><w:listItem w:displayText="合格" w:value="pass"/><w:listItem w:displayText="不合格" w:value="fail"/></w:dropDownList></w:sdtPr><w:sdtContent><w:r><w:rPr><w:color w:val="008000"/></w:rPr><w:t>请选择</w:t></w:r></w:sdtContent></w:sdt></w:p></w:body></w:document>`
	matches := make(map[string]int)
	updated := fillWordContentControls(part, map[string]string{"检测结论": "合格"}, matches)
	for _, want := range []string{"合格", `w:displayText="合格"`, `w:displayText="不合格"`, `<w:color w:val="008000"/>`} {
		if !strings.Contains(updated, want) {
			t.Fatalf("filled dropdown missing %q: %s", want, updated)
		}
	}
	if strings.Contains(updated, ">请选择<") || matches["检测结论"] != 1 {
		t.Fatalf("dropdown was not filled exactly once: matches=%v xml=%s", matches, updated)
	}
}

func writeTestDocx(t *testing.T, path, documentXML string) {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("create test DOCX: %v", err)
	}
	writer := zip.NewWriter(file)
	for name, content := range map[string]string{
		"[Content_Types].xml": `<?xml version="1.0"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/></Types>`,
		"_rels/.rels":         `<?xml version="1.0"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/></Relationships>`,
		"word/document.xml":   documentXML,
	} {
		entry, createErr := writer.Create(name)
		if createErr != nil {
			t.Fatalf("create DOCX part: %v", createErr)
		}
		if _, writeErr := entry.Write([]byte(content)); writeErr != nil {
			t.Fatalf("write DOCX part: %v", writeErr)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close DOCX zip: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close DOCX file: %v", err)
	}
}

func readTestDocxPart(t *testing.T, path, partName string) string {
	t.Helper()
	reader, err := zip.OpenReader(path)
	if err != nil {
		t.Fatalf("open output DOCX: %v", err)
	}
	defer reader.Close()
	for _, file := range reader.File {
		if file.Name != partName {
			continue
		}
		part, openErr := file.Open()
		if openErr != nil {
			t.Fatalf("open DOCX part: %v", openErr)
		}
		content, readErr := io.ReadAll(part)
		_ = part.Close()
		if readErr != nil {
			t.Fatalf("read DOCX part: %v", readErr)
		}
		return string(content)
	}
	t.Fatalf("DOCX part %s not found", partName)
	return ""
}
