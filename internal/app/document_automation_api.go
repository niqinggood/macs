package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const documentAutomationMaxRequestBytes = 16 << 20

var unsafeDocumentOutputName = regexp.MustCompile(`[<>:"/\\|?*\x00-\x1f]+`)

type documentAutomationRequest struct {
	TemplatePath        string                   `json:"template_path"`
	SourceData          map[string]interface{}   `json:"source_data"`
	Records             []map[string]interface{} `json:"records,omitempty"`
	FieldMapping        map[string]interface{}   `json:"field_mapping,omitempty"`
	TableCells          []interface{}            `json:"table_cells,omitempty"`
	RepeatRows          []interface{}            `json:"repeat_rows,omitempty"`
	MappingThreshold    float64                  `json:"mapping_threshold,omitempty"`
	UseTemplateProfile  *bool                    `json:"use_template_profile,omitempty"`
	SaveTemplateProfile bool                     `json:"save_template_profile,omitempty"`
	TemplateProfileName string                   `json:"template_profile_name,omitempty"`
	OutputName          string                   `json:"output_name,omitempty"`
	Strict              bool                     `json:"strict,omitempty"`
	VerifyComplete      bool                     `json:"verify_complete,omitempty"`
}

type documentAutomationGeneratedFile struct {
	Name        string `json:"name"`
	Path        string `json:"path"`
	DownloadURL string `json:"download_url"`
	Receipt     string `json:"receipt"`
}

type documentAutomationProfileSummary struct {
	Fingerprint  string  `json:"fingerprint"`
	Name         string  `json:"name,omitempty"`
	TemplateName string  `json:"template_name"`
	FieldCount   int     `json:"field_count"`
	CellCount    int     `json:"cell_count"`
	RepeatCount  int     `json:"repeat_count"`
	Threshold    float64 `json:"mapping_threshold"`
	UpdatedAt    string  `json:"updated_at"`
}

// handleDocumentAutomationInspect exposes the same DOCX inspection and mapping
// engine used by the extract tool to the first-party Web workbench.
func (g *GatewayService) handleDocumentAutomationInspect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		sendJSONError(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	request, ok := decodeDocumentAutomationRequest(w, r)
	if !ok {
		return
	}
	if err := validateDocumentAutomationTemplate(request.TemplatePath); err != nil {
		sendJSONError(w, err.Error(), http.StatusBadRequest)
		return
	}

	params := documentAutomationToolParams(request, documentAutomationPreviewSource(request))
	params["inspect_template"] = true
	if len(request.SourceData) > 0 || len(request.Records) > 0 {
		params["preview_mapping"] = true
	}

	result, err := NewExtractTool(g.attachmentWorkspaceRoot(), false).Execute(r.Context(), params)
	if err != nil {
		sendJSONError(w, err.Error(), http.StatusBadRequest)
		return
	}

	var preview interface{}
	if err := json.Unmarshal([]byte(result), &preview); err != nil {
		sendJSONError(w, "DOCX engine returned an invalid inspection", http.StatusInternalServerError)
		return
	}
	writeDocumentAutomationJSON(w, http.StatusOK, map[string]interface{}{
		"ok":      true,
		"preview": preview,
	})
}

// handleDocumentAutomationGenerate fills one document, or a batch when records
// is supplied, and returns workspace-safe download URLs for every generated file.
func (g *GatewayService) handleDocumentAutomationGenerate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		sendJSONError(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	request, ok := decodeDocumentAutomationRequest(w, r)
	if !ok {
		return
	}
	if err := validateDocumentAutomationTemplate(request.TemplatePath); err != nil {
		sendJSONError(w, err.Error(), http.StatusBadRequest)
		return
	}

	records := request.Records
	if len(records) == 0 {
		if len(request.SourceData) == 0 {
			sendJSONError(w, "source_data or records is required", http.StatusBadRequest)
			return
		}
		records = []map[string]interface{}{request.SourceData}
	}

	outputDir := filepath.Join(g.attachmentWorkspaceRoot(), ".openkitty", "storage", "document-automation")
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		sendJSONError(w, "create document output directory: "+err.Error(), http.StatusInternalServerError)
		return
	}
	baseName := documentAutomationOutputBase(request.OutputName, request.TemplatePath)
	stamp := time.Now().Format("20060102-150405")
	files := make([]documentAutomationGeneratedFile, 0, len(records))

	for index, record := range records {
		select {
		case <-r.Context().Done():
			sendJSONError(w, r.Context().Err().Error(), http.StatusRequestTimeout)
			return
		default:
		}

		name := baseName + "-" + stamp
		if len(records) > 1 {
			name += fmt.Sprintf("-%03d", index+1)
		}
		name += ".docx"
		outputPath := filepath.Join(outputDir, name)
		params := documentAutomationToolParams(request, record)
		params["output"] = outputPath
		// Saving once is sufficient because a batch shares one template plan.
		params["save_template_profile"] = request.SaveTemplateProfile && index == 0

		receipt, err := NewExtractTool(g.attachmentWorkspaceRoot(), false).Execute(r.Context(), params)
		if err != nil {
			sendJSONError(w, fmt.Sprintf("generate document %d: %v", index+1, err), http.StatusBadRequest)
			return
		}
		relativePath, err := filepath.Rel(g.attachmentWorkspaceRoot(), outputPath)
		if err != nil {
			sendJSONError(w, "resolve generated document path: "+err.Error(), http.StatusInternalServerError)
			return
		}
		apiPath := "/" + filepath.ToSlash(relativePath)
		files = append(files, documentAutomationGeneratedFile{
			Name:        name,
			Path:        apiPath,
			DownloadURL: "/api/files/download?path=" + url.QueryEscape(apiPath),
			Receipt:     receipt,
		})
	}

	writeDocumentAutomationJSON(w, http.StatusOK, map[string]interface{}{
		"ok":    true,
		"count": len(files),
		"files": files,
	})
}

func (g *GatewayService) handleDocumentAutomationProfiles(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		sendJSONError(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	dir := filepath.Join(g.attachmentWorkspaceRoot(), ".openkitty", "document-templates")
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		writeDocumentAutomationJSON(w, http.StatusOK, map[string]interface{}{"ok": true, "profiles": []interface{}{}})
		return
	}
	if err != nil {
		sendJSONError(w, "list document profiles: "+err.Error(), http.StatusInternalServerError)
		return
	}

	profiles := make([]documentAutomationProfileSummary, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".json") {
			continue
		}
		content, readErr := os.ReadFile(filepath.Join(dir, entry.Name()))
		if readErr != nil {
			continue
		}
		var raw struct {
			Fingerprint      string            `json:"fingerprint"`
			Name             string            `json:"name"`
			TemplateName     string            `json:"template_name"`
			FieldMapping     map[string]string `json:"field_mapping"`
			TableCells       []interface{}     `json:"table_cells"`
			RepeatRows       []interface{}     `json:"repeat_rows"`
			MappingThreshold float64           `json:"mapping_threshold"`
			UpdatedAt        string            `json:"updated_at"`
		}
		if json.Unmarshal(content, &raw) != nil || strings.TrimSpace(raw.Fingerprint) == "" {
			continue
		}
		profiles = append(profiles, documentAutomationProfileSummary{
			Fingerprint: raw.Fingerprint, Name: raw.Name, TemplateName: raw.TemplateName,
			FieldCount: len(raw.FieldMapping), CellCount: len(raw.TableCells), RepeatCount: len(raw.RepeatRows),
			Threshold: raw.MappingThreshold, UpdatedAt: raw.UpdatedAt,
		})
	}
	sort.Slice(profiles, func(i, j int) bool { return profiles[i].UpdatedAt > profiles[j].UpdatedAt })
	writeDocumentAutomationJSON(w, http.StatusOK, map[string]interface{}{"ok": true, "profiles": profiles})
}

func decodeDocumentAutomationRequest(w http.ResponseWriter, r *http.Request) (documentAutomationRequest, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, documentAutomationMaxRequestBytes)
	decoder := json.NewDecoder(r.Body)
	var request documentAutomationRequest
	if err := decoder.Decode(&request); err != nil {
		sendJSONError(w, "invalid request: "+err.Error(), http.StatusBadRequest)
		return documentAutomationRequest{}, false
	}
	return request, true
}

func validateDocumentAutomationTemplate(path string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return fmt.Errorf("template_path is required")
	}
	if !strings.EqualFold(filepath.Ext(path), ".docx") {
		return fmt.Errorf("template_path must be a .docx file")
	}
	return nil
}

func documentAutomationPreviewSource(request documentAutomationRequest) map[string]interface{} {
	if len(request.SourceData) > 0 {
		return request.SourceData
	}
	if len(request.Records) > 0 {
		return request.Records[0]
	}
	return nil
}

func documentAutomationToolParams(request documentAutomationRequest, source map[string]interface{}) map[string]interface{} {
	params := map[string]interface{}{
		"file":                  request.TemplatePath,
		"source_data":           source,
		"field_mapping":         request.FieldMapping,
		"table_cells":           request.TableCells,
		"repeat_rows":           request.RepeatRows,
		"mapping_threshold":     request.MappingThreshold,
		"template_profile_name": request.TemplateProfileName,
		"strict":                request.Strict,
		"verify_complete":       request.VerifyComplete,
	}
	if request.UseTemplateProfile != nil {
		params["use_template_profile"] = *request.UseTemplateProfile
	}
	return params
}

func documentAutomationOutputBase(requested, templatePath string) string {
	base := strings.TrimSpace(requested)
	if base == "" {
		base = strings.TrimSuffix(filepath.Base(templatePath), filepath.Ext(templatePath)) + "-已填写"
	}
	base = strings.TrimSuffix(filepath.Base(base), filepath.Ext(base))
	base = strings.TrimSpace(unsafeDocumentOutputName.ReplaceAllString(base, "-"))
	base = strings.Trim(base, ". ")
	if base == "" {
		return "自动化文档"
	}
	if len([]rune(base)) > 80 {
		base = string([]rune(base)[:80])
	}
	return base
}

func writeDocumentAutomationJSON(w http.ResponseWriter, status int, payload interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
