package app

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDocumentAutomationInspectAndGenerate(t *testing.T) {
	workspace := t.TempDir()
	templatePath := filepath.Join(workspace, "enterprise-template.docx")
	writeDocumentAutomationTestDOCX(t, templatePath)
	gateway := &GatewayService{config: &Config{Workspace: workspace}}

	inspectBody := map[string]interface{}{
		"template_path": "enterprise-template.docx",
		"source_data":   map[string]interface{}{"report_number": "R-2026-09"},
	}
	inspectRecorder := httptest.NewRecorder()
	gateway.handleDocumentAutomationInspect(inspectRecorder, documentAutomationJSONRequest(t, http.MethodPost, inspectBody))
	if inspectRecorder.Code != http.StatusOK {
		t.Fatalf("inspect status = %d, body = %s", inspectRecorder.Code, inspectRecorder.Body.String())
	}
	var inspectResponse struct {
		OK      bool `json:"ok"`
		Preview struct {
			Inspection struct {
				Fingerprint string `json:"fingerprint"`
			} `json:"inspection"`
			Mapping struct {
				Fields []struct {
					Target     string `json:"target"`
					SourcePath string `json:"source_path"`
					Status     string `json:"status"`
				} `json:"fields"`
			} `json:"mapping"`
		} `json:"preview"`
	}
	if err := json.Unmarshal(inspectRecorder.Body.Bytes(), &inspectResponse); err != nil {
		t.Fatalf("decode inspect response: %v", err)
	}
	if !inspectResponse.OK || inspectResponse.Preview.Inspection.Fingerprint == "" {
		t.Fatalf("unexpected inspect response: %#v", inspectResponse)
	}
	if len(inspectResponse.Preview.Mapping.Fields) != 1 ||
		inspectResponse.Preview.Mapping.Fields[0].Target != "report_number" ||
		inspectResponse.Preview.Mapping.Fields[0].SourcePath != "report_number" ||
		inspectResponse.Preview.Mapping.Fields[0].Status != "mapped" {
		t.Fatalf("unexpected mapping: %#v", inspectResponse.Preview.Mapping.Fields)
	}

	generateBody := map[string]interface{}{
		"template_path":         "enterprise-template.docx",
		"source_data":           map[string]interface{}{"report_number": "R-2026-09"},
		"strict":                true,
		"verify_complete":       true,
		"save_template_profile": true,
		"template_profile_name": "Enterprise report",
		"output_name":           "report-result.docx",
	}
	generateRecorder := httptest.NewRecorder()
	gateway.handleDocumentAutomationGenerate(generateRecorder, documentAutomationJSONRequest(t, http.MethodPost, generateBody))
	if generateRecorder.Code != http.StatusOK {
		t.Fatalf("generate status = %d, body = %s", generateRecorder.Code, generateRecorder.Body.String())
	}
	var generateResponse struct {
		OK    bool `json:"ok"`
		Count int  `json:"count"`
		Files []struct {
			Path        string `json:"path"`
			DownloadURL string `json:"download_url"`
			Receipt     string `json:"receipt"`
		} `json:"files"`
	}
	if err := json.Unmarshal(generateRecorder.Body.Bytes(), &generateResponse); err != nil {
		t.Fatalf("decode generate response: %v", err)
	}
	if !generateResponse.OK || generateResponse.Count != 1 || len(generateResponse.Files) != 1 {
		t.Fatalf("unexpected generate response: %#v", generateResponse)
	}
	if !strings.Contains(generateResponse.Files[0].Receipt, "completeness=verified") {
		t.Fatalf("receipt does not include completeness verification: %q", generateResponse.Files[0].Receipt)
	}
	if !strings.HasPrefix(generateResponse.Files[0].DownloadURL, "/api/files/download?path=") {
		t.Fatalf("unexpected download URL: %q", generateResponse.Files[0].DownloadURL)
	}
	generatedPath := filepath.Join(workspace, filepath.FromSlash(strings.TrimPrefix(generateResponse.Files[0].Path, "/")))
	if _, err := os.Stat(generatedPath); err != nil {
		t.Fatalf("generated DOCX missing: %v", err)
	}

	profilesRecorder := httptest.NewRecorder()
	gateway.handleDocumentAutomationProfiles(profilesRecorder, httptest.NewRequest(http.MethodGet, "/api/document-automation/profiles", nil))
	if profilesRecorder.Code != http.StatusOK || !strings.Contains(profilesRecorder.Body.String(), "Enterprise report") {
		t.Fatalf("profile list status = %d, body = %s", profilesRecorder.Code, profilesRecorder.Body.String())
	}
}

func TestDecodeDocumentAutomationRequestKeepsToolProtocolNumbers(t *testing.T) {
	recorder := httptest.NewRecorder()
	request, ok := decodeDocumentAutomationRequest(recorder, documentAutomationJSONRequest(t, http.MethodPost, map[string]interface{}{
		"template_path": "template.docx",
		"table_cells": []interface{}{map[string]interface{}{
			"table_index": 1,
			"row":         2,
			"cell":        3,
			"source_path": "header.department",
		}},
		"repeat_rows": []interface{}{map[string]interface{}{
			"table_index":  2,
			"template_row": 3,
		}},
	}))
	if !ok {
		t.Fatalf("decode failed: %s", recorder.Body.String())
	}
	row, ok := request.RepeatRows[0].(map[string]interface{})
	if !ok {
		t.Fatalf("repeat row type = %T", request.RepeatRows[0])
	}
	if _, ok := row["table_index"].(float64); !ok {
		t.Fatalf("table_index type = %T, want float64 for extract tool compatibility", row["table_index"])
	}
	cell, ok := request.TableCells[0].(map[string]interface{})
	if !ok {
		t.Fatalf("table cell type = %T", request.TableCells[0])
	}
	for _, key := range []string{"table_index", "row", "cell"} {
		if _, ok := cell[key].(float64); !ok {
			t.Fatalf("table cell %s type = %T, want float64 for extract tool compatibility", key, cell[key])
		}
	}
}

func documentAutomationJSONRequest(t *testing.T, method string, body interface{}) *http.Request {
	t.Helper()
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(method, "/api/document-automation", bytes.NewReader(encoded))
	request.Header.Set("Content-Type", "application/json")
	return request
}

func writeDocumentAutomationTestDOCX(t *testing.T, path string) {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(file)
	parts := map[string]string{
		"[Content_Types].xml": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">
  <Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>
  <Default Extension="xml" ContentType="application/xml"/>
  <Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/>
</Types>`,
		"_rels/.rels": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
  <Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/>
</Relationships>`,
		"word/document.xml": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:r><w:t>Report: {{report_number}}</w:t></w:r></w:p><w:sectPr/></w:body></w:document>`,
	}
	for name, content := range parts {
		part, createErr := writer.Create(name)
		if createErr != nil {
			t.Fatal(createErr)
		}
		if _, copyErr := io.Copy(part, strings.NewReader(content)); copyErr != nil {
			t.Fatal(copyErr)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}
