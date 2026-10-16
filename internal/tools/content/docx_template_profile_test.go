package content

import (
	"archive/zip"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDocxTemplateFingerprintIgnoresPackageMetadataParts(t *testing.T) {
	dir := t.TempDir()
	first := filepath.Join(dir, "first.docx")
	second := filepath.Join(dir, "second.docx")
	changed := filepath.Join(dir, "changed.docx")
	documentXML := `<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:r><w:t>{{report_no}}</w:t></w:r></w:p></w:body></w:document>`
	writeTestDocx(t, first, documentXML)
	copyTestDocxWithExtraPart(t, first, second, "docProps/core.xml", `<cp:coreProperties xmlns:cp="urn:test"><cp:revision>99</cp:revision></cp:coreProperties>`)
	writeTestDocx(t, changed, strings.Replace(documentXML, "report_no", "sample_no", 1))

	firstFingerprint, err := docxTemplateFingerprint(first)
	if err != nil {
		t.Fatalf("fingerprint first template: %v", err)
	}
	secondFingerprint, err := docxTemplateFingerprint(second)
	if err != nil {
		t.Fatalf("fingerprint repackaged template: %v", err)
	}
	changedFingerprint, err := docxTemplateFingerprint(changed)
	if err != nil {
		t.Fatalf("fingerprint changed template: %v", err)
	}
	if firstFingerprint != secondFingerprint {
		t.Fatalf("non-document package metadata changed fingerprint: %s != %s", firstFingerprint, secondFingerprint)
	}
	if firstFingerprint == changedFingerprint {
		t.Fatalf("changed document structure retained fingerprint %s", firstFingerprint)
	}
}

func TestExtractToolSavesAndReusesDocxTemplateProfileWithoutDataValues(t *testing.T) {
	dir := t.TempDir()
	templatePath := filepath.Join(dir, "enterprise-template.docx")
	firstOutput := filepath.Join(dir, "batch-one.docx")
	secondOutput := filepath.Join(dir, "batch-two.docx")
	documentXML := `<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body>
<w:p><w:r><w:t>报告编号：{{报告编号}}</w:t></w:r></w:p>
<w:tbl><w:tr><w:tc><w:p><w:r><w:t>序号</w:t></w:r></w:p></w:tc><w:tc><w:p><w:r><w:t>样品编号</w:t></w:r></w:p></w:tc><w:tc><w:p><w:r><w:t>结果</w:t></w:r></w:p></w:tc></w:tr>
<w:tr><w:trPr><w:cantSplit/></w:trPr><w:tc><w:p><w:r><w:t>{{$index}}</w:t></w:r></w:p></w:tc><w:tc><w:p><w:r><w:t>{{sample_no}}</w:t></w:r></w:p></w:tc><w:tc><w:p><w:r><w:t>{{result}}</w:t></w:r></w:p></w:tc></w:tr></w:tbl>
</w:body></w:document>`
	writeTestDocx(t, templatePath, documentXML)

	tool := NewExtractTool(dir, false)
	firstReceipt, err := tool.Execute(context.Background(), map[string]interface{}{
		"file":                  templatePath,
		"output":                firstOutput,
		"save_template_profile": true,
		"template_profile_name": "企业甲-检验报告",
		"source_data": map[string]interface{}{
			"header": map[string]interface{}{"report_no": "R-SECRET-001"},
			"records": []interface{}{
				map[string]interface{}{"sample_id": "S-SECRET-001", "test_result": "7.20"},
			},
		},
		"repeat_rows": []interface{}{
			map[string]interface{}{
				"table_index": float64(1), "template_row": float64(2), "data_path": "records",
			},
		},
		"strict": true,
	})
	if err != nil {
		t.Fatalf("fill and save template profile: %v", err)
	}
	if !strings.Contains(firstReceipt, "profile=saved:") {
		t.Fatalf("save receipt missing profile evidence: %s", firstReceipt)
	}

	fingerprint, err := docxTemplateFingerprint(templatePath)
	if err != nil {
		t.Fatalf("fingerprint template: %v", err)
	}
	profilePath := docxTemplateProfilePath(dir, fingerprint)
	profileJSON, err := os.ReadFile(profilePath)
	if err != nil {
		t.Fatalf("read saved profile: %v", err)
	}
	profileText := string(profileJSON)
	for _, forbidden := range []string{"R-SECRET-001", "S-SECRET-001", "7.20"} {
		if strings.Contains(profileText, forbidden) {
			t.Fatalf("saved profile leaked experiment value %q:\n%s", forbidden, profileText)
		}
	}
	for _, want := range []string{`"header.report_no"`, `"data_path": "records"`, `"name": "企业甲-检验报告"`} {
		if !strings.Contains(profileText, want) {
			t.Fatalf("saved profile missing reusable rule %s:\n%s", want, profileText)
		}
	}

	secondReceipt, err := tool.Execute(context.Background(), map[string]interface{}{
		"file":                 templatePath,
		"output":               secondOutput,
		"use_template_profile": true,
		"source_data": map[string]interface{}{
			"header": map[string]interface{}{"report_no": "R-NEW-002"},
			"records": []interface{}{
				map[string]interface{}{"sample_id": "S-NEW-101", "test_result": "0.13%"},
				map[string]interface{}{"sample_id": "S-NEW-102", "test_result": "0.15%"},
			},
		},
		"strict": true,
	})
	if err != nil {
		t.Fatalf("reuse saved template profile: %v", err)
	}
	if !strings.Contains(secondReceipt, "profile=loaded:") || !strings.Contains(secondReceipt, "repeated rows=2") {
		t.Fatalf("reuse receipt missing evidence: %s", secondReceipt)
	}
	filled := readTestDocxPart(t, secondOutput, "word/document.xml")
	for _, want := range []string{"R-NEW-002", "S-NEW-101", "S-NEW-102", "0.13%", "0.15%"} {
		if !strings.Contains(filled, want) {
			t.Fatalf("reused profile output missing %q:\n%s", want, filled)
		}
	}
	for _, oldValue := range []string{"R-SECRET-001", "S-SECRET-001", "7.20"} {
		if strings.Contains(filled, oldValue) {
			t.Fatalf("reused output retained prior experiment value %q:\n%s", oldValue, filled)
		}
	}
}

func TestExtractToolRequiredDocxTemplateProfileReportsMissingMatch(t *testing.T) {
	dir := t.TempDir()
	templatePath := filepath.Join(dir, "unseen-template.docx")
	writeTestDocx(t, templatePath, `<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:r><w:t>{{sample_no}}</w:t></w:r></w:p></w:body></w:document>`)

	tool := NewExtractTool(dir, false)
	_, err := tool.Execute(context.Background(), map[string]interface{}{
		"file":                 templatePath,
		"source_data":          map[string]interface{}{"sample_no": "S-1"},
		"use_template_profile": true,
	})
	if err == nil || !strings.Contains(err.Error(), "no saved DOCX template profile matches fingerprint") {
		t.Fatalf("expected clear missing-profile error, got %v", err)
	}
}

func copyTestDocxWithExtraPart(t *testing.T, source, destination, extraName, extraContent string) {
	t.Helper()
	reader, err := zip.OpenReader(source)
	if err != nil {
		t.Fatalf("open source DOCX: %v", err)
	}
	defer reader.Close()

	file, err := os.Create(destination)
	if err != nil {
		t.Fatalf("create copied DOCX: %v", err)
	}
	writer := zip.NewWriter(file)
	for _, sourcePart := range reader.File {
		partReader, openErr := sourcePart.Open()
		if openErr != nil {
			t.Fatalf("open source DOCX part: %v", openErr)
		}
		content, readErr := io.ReadAll(partReader)
		_ = partReader.Close()
		if readErr != nil {
			t.Fatalf("read source DOCX part: %v", readErr)
		}
		header := sourcePart.FileHeader
		entry, createErr := writer.CreateHeader(&header)
		if createErr != nil {
			t.Fatalf("copy DOCX part: %v", createErr)
		}
		if _, writeErr := entry.Write(content); writeErr != nil {
			t.Fatalf("write copied DOCX part: %v", writeErr)
		}
	}
	extra, err := writer.Create(extraName)
	if err != nil {
		t.Fatalf("create extra DOCX part: %v", err)
	}
	if _, err := extra.Write([]byte(extraContent)); err != nil {
		t.Fatalf("write extra DOCX part: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close copied DOCX zip: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close copied DOCX: %v", err)
	}
}
