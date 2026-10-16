package content

import (
	"archive/zip"
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"mac/internal/processutil"
	"mac/internal/toolcontract"
)

// ==================== extract tool ====================

type ExtractTool struct {
	workspace    string
	autoInstall  bool
	dependencies ExtractDependencies
}

type ExtractDependencies struct {
	EnforcePath        func(context.Context, string, string) error
	UnstructuredScript func() (string, func(), error)
	PDFScript          func() (string, func(), error)
}

func NewExtractTool(workspace string, autoInstallDeps bool, dependencies ...ExtractDependencies) *ExtractTool {
	tool := &ExtractTool{workspace: workspace, autoInstall: autoInstallDeps}
	if len(dependencies) > 0 {
		tool.dependencies = dependencies[0]
	}
	return tool
}

func (t *ExtractTool) Name() string { return "extract" }

func (t *ExtractTool) SandboxCapabilities() toolcontract.SandboxCapabilities {
	return toolcontract.SandboxCapabilities{Process: true, Network: t.autoInstall}
}

func (t *ExtractTool) Description() string {
	return "Extract text content from PPTX, PDF, DOCX, audio, and video files into markdown for the agent to analyze. " +
		"Also inspects and fills DOCX templates while preserving layout: automatic LIMS/enterprise field mapping with confidence, stable table-cell addresses and inferred candidates, split-run placeholders, content controls including checkboxes, MERGEFIELD fields, fillable bookmarks, underlined fields, and repeating table rows."
}

func (t *ExtractTool) ToolSearchMetadata() toolcontract.ToolSearchMetadata {
	return toolcontract.ToolSearchMetadata{
		Aliases: []string{"word_template_fill", "docx_template_fill", "word_mail_merge", "Word模板填充", "Word表格填写", "Word下划线填写", "Word邮件合并", "LIMS报告填充"},
		Tags:    []string{"word", "docx", "document", "template", "placeholder", "underline", "content control", "checkbox", "mergefield", "bookmark", "repeat rows", "field mapping", "lims", "fill", "文档", "模板", "表格", "下划线", "字段映射", "填写"},
	}
}

func (t *ExtractTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"file": map[string]interface{}{
				"type": "string", "description": "Path to the PPTX, PDF, DOCX, audio, or video file",
			},
			"output": map[string]interface{}{
				"type": "string", "description": "Optional output path: .md for extraction or .docx for template filling. DOCX filling defaults to <template>_filled.docx.",
			},
			"template_data": map[string]interface{}{
				"type": "object", "description": "For DOCX template filling: key/value data for {{key}} placeholders, Word content controls, MERGEFIELD fields, and fillable bookmarks. Boolean values update checkbox content controls. Placeholders may span multiple Word runs.",
			},
			"inspect_template": map[string]interface{}{
				"type": "boolean", "description": "For a DOCX file, return a structured inventory of placeholders, typed content controls, MERGEFIELD fields, bookmarks, underlined blanks, and table rows before filling. With source_data, also previews automatic field mappings. When true, no output file is written.",
			},
			"source_data": map[string]interface{}{
				"type": "object", "description": "Optional enterprise/LIMS data object. Scalar fields are automatically matched to template fields; arrays can be consumed by repeat_rows.",
			},
			"field_mapping": map[string]interface{}{
				"type": "object", "description": "Optional explicit mapping of template field or underline label → dot-separated source_data path. Overrides automatic matching and replaces per-template code with a declarative mapping.",
			},
			"preview_mapping": map[string]interface{}{
				"type": "boolean", "description": "Inspect the DOCX and return mapping confidence, ambiguities, unmatched template fields, and unused source paths without writing output.",
			},
			"mapping_threshold": map[string]interface{}{
				"type": "number", "minimum": 0, "maximum": 1, "description": "Minimum confidence for automatic field mapping; default 0.88. Explicit field_mapping is always accepted when its source path exists.",
			},
			"use_template_profile": map[string]interface{}{
				"type": "boolean", "description": "Load the reusable mapping plan saved for this exact DOCX structure. If omitted, a saved profile is auto-used when source_data is provided without field_mapping, table_cells, or repeat_rows. Set false to disable automatic reuse; set true to require a matching saved profile.",
			},
			"save_template_profile": map[string]interface{}{
				"type": "boolean", "description": "After a successful DOCX fill, save reusable field paths, fixed-cell addresses, and repeat-row rules under .openkitty/document-templates. Experiment/sample values are never stored.",
			},
			"template_profile_name": map[string]interface{}{
				"type": "string", "description": "Optional human-readable name saved with a reusable DOCX template profile, for example 企业A-原料检验报告.",
			},
			"repeat_rows": map[string]interface{}{
				"type": "array", "description": "Declarative repeating-table plans. Each plan clones one 1-based template row for every object from rows or source_data[data_path], preserving row/cell formatting and merged-cell properties. Row fields may use {{key}}, content controls, MERGEFIELD, or fillable bookmarks.",
				"items": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"part":                     map[string]interface{}{"type": "string", "description": "DOCX XML part from inspect_template; defaults to word/document.xml"},
						"table_index":              map[string]interface{}{"type": "integer", "minimum": 1, "description": "1-based table index from inspect_template"},
						"template_row":             map[string]interface{}{"type": "integer", "minimum": 1, "description": "1-based row to clone"},
						"data_path":                map[string]interface{}{"type": "string", "description": "Dot-separated path to an array in source_data"},
						"rows":                     map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "object"}, "description": "Direct row objects; use instead of data_path"},
						"column_keys":              map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "description": "Optional source field per table cell. Empty entries keep that cell; $index emits 1-based sequence. Without this, placeholders in the template row are mapped automatically."},
						"remove_template_if_empty": map[string]interface{}{"type": "boolean", "description": "Remove the template row when the selected data array is empty; default false"},
					},
					"required": []string{"table_index", "template_row"},
				},
			},
			"table_cells": map[string]interface{}{
				"type": "array", "description": "Declarative fixed-cell plans. Addresses come from inspect_template and remain template-relative; values come from source_data paths or an explicit scalar. This supports arbitrary forms without per-template code.",
				"items": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"part":        map[string]interface{}{"type": "string", "description": "DOCX XML part from inspect_template; defaults to word/document.xml"},
						"table_index": map[string]interface{}{"type": "integer", "minimum": 1, "description": "1-based table index from inspect_template"},
						"row":         map[string]interface{}{"type": "integer", "minimum": 1, "description": "1-based physical row index from inspect_template"},
						"cell":        map[string]interface{}{"type": "integer", "minimum": 1, "description": "1-based physical cell index from inspect_template"},
						"source_path": map[string]interface{}{"type": "string", "description": "Dot-separated scalar path in source_data; reusable profiles store this path, never its resolved value"},
						"value":       map[string]interface{}{"description": "Direct scalar value; use instead of source_path for one-off fills"},
					},
					"required": []string{"table_index", "row", "cell"},
				},
			},
			"underline_data": map[string]interface{}{
				"type": "object", "description": "Optional DOCX field map: visible label → value. Fills the underscore blank or formatted underlined blank following each label while preserving its run style and table layout.",
			},
			"underline_values": map[string]interface{}{
				"type": "array", "items": map[string]interface{}{"type": "string"}, "description": "Optional ordered values for remaining underscore or formatted-underlined blanks, in document order. Prefer underline_data when labels are available.",
			},
			"strict": map[string]interface{}{
				"type": "boolean", "description": "If true, fail without replacing output when mappings are ambiguous/missing, supplied fields cannot be matched, or detectable template fields remain blank. Also enables completeness verification.",
			},
			"verify_complete": map[string]interface{}{
				"type": "boolean", "description": "Fail without replacing output if generated DOCX still contains placeholders, default-empty content controls, or blank/underscore underlined fields. strict=true also enables this check.",
			},
		},
		"required": []string{"file"},
	}
}

func (t *ExtractTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	file, _ := params["file"].(string)
	output, _ := params["output"].(string)
	templateData, _ := params["template_data"].(map[string]interface{})
	inspectTemplate, _ := params["inspect_template"].(bool)
	sourceData, _ := params["source_data"].(map[string]interface{})
	fieldMappingRaw, _ := params["field_mapping"].(map[string]interface{})
	previewMapping, _ := params["preview_mapping"].(bool)
	mappingThreshold, _ := params["mapping_threshold"].(float64)
	useTemplateProfile, useTemplateProfileSet := params["use_template_profile"].(bool)
	saveTemplateProfile, _ := params["save_template_profile"].(bool)
	templateProfileName, _ := params["template_profile_name"].(string)
	tableCellsRaw, _ := params["table_cells"].([]interface{})
	repeatRowsRaw, _ := params["repeat_rows"].([]interface{})
	underlineData, _ := params["underline_data"].(map[string]interface{})
	underlineValues, _ := params["underline_values"].([]interface{})
	strict, _ := params["strict"].(bool)
	verifyComplete, _ := params["verify_complete"].(bool)

	if file == "" {
		return "", fmt.Errorf("file path is required")
	}
	if t.dependencies.EnforcePath != nil {
		if err := t.dependencies.EnforcePath(ctx, t.workspace, file); err != nil {
			return "", err
		}
	}
	if strings.TrimSpace(output) != "" {
		if t.dependencies.EnforcePath != nil {
			if err := t.dependencies.EnforcePath(ctx, t.workspace, output); err != nil {
				return "", err
			}
		}
	}

	path := file
	if !filepath.IsAbs(path) {
		path = filepath.Join(t.workspace, path)
	}
	output = strings.TrimSpace(output)
	if output != "" && !filepath.IsAbs(output) {
		output = filepath.Join(t.workspace, output)
	}

	ext := strings.ToLower(filepath.Ext(path))
	if (saveTemplateProfile || (useTemplateProfileSet && useTemplateProfile)) && ext != ".docx" {
		return "", fmt.Errorf("DOCX template profiles require a .docx file")
	}

	explicitFieldMapping := stringifyFieldMapping(fieldMappingRaw)
	profileFingerprint := ""
	profileSummary := ""
	shouldTryProfile := ext == ".docx" && ((useTemplateProfileSet && useTemplateProfile) ||
		(!useTemplateProfileSet && len(sourceData) > 0 && len(explicitFieldMapping) == 0 && len(tableCellsRaw) == 0 && len(repeatRowsRaw) == 0))
	if shouldTryProfile {
		var fingerprintErr error
		profileFingerprint, fingerprintErr = docxTemplateFingerprint(path)
		if fingerprintErr != nil {
			return "", fingerprintErr
		}
		profile, found, profileErr := loadDocxTemplateProfile(t.workspace, profileFingerprint)
		if profileErr != nil {
			return "", profileErr
		}
		if !found && useTemplateProfileSet && useTemplateProfile {
			return "", fmt.Errorf("no saved DOCX template profile matches fingerprint %s", shortDocxFingerprint(profileFingerprint))
		}
		if found {
			explicitFieldMapping = mergeDocxFieldMappings(profile.FieldMapping, explicitFieldMapping)
			if len(tableCellsRaw) == 0 {
				tableCellsRaw = docxProfileTableCellsRaw(profile.TableCells)
			}
			if len(repeatRowsRaw) == 0 {
				repeatRowsRaw = docxProfileRepeatRowsRaw(profile.RepeatRows)
			}
			if mappingThreshold <= 0 {
				mappingThreshold = profile.MappingThreshold
			}
			profileSummary = "; profile=loaded:" + shortDocxFingerprint(profileFingerprint)
		}
	}
	var tableCells []docxTableCellPlan
	if len(tableCellsRaw) > 0 {
		if ext != ".docx" {
			return "", fmt.Errorf("table_cells requires a .docx file")
		}
		var tableCellsErr error
		tableCells, tableCellsErr = parseDocxTableCellPlans(tableCellsRaw, sourceData)
		if tableCellsErr != nil {
			return "", tableCellsErr
		}
	}

	var repeatRows []docxRepeatRowPlan
	if len(repeatRowsRaw) > 0 {
		if ext != ".docx" {
			return "", fmt.Errorf("repeat_rows requires a .docx file")
		}
		var repeatRowsErr error
		repeatRows, repeatRowsErr = parseDocxRepeatRowPlans(repeatRowsRaw, sourceData)
		if repeatRowsErr != nil {
			return "", repeatRowsErr
		}
	}
	if err := validateDocxTablePlanConflicts(tableCells, repeatRows); err != nil {
		return "", err
	}
	if inspectTemplate || previewMapping {
		if ext != ".docx" {
			return "", fmt.Errorf("DOCX template inspection requires a .docx file")
		}
		if len(sourceData) > 0 || previewMapping {
			inspection, inspectErr := analyzeDocxTemplate(path)
			if inspectErr != nil {
				return "", inspectErr
			}
			_, _, mappingReport := buildDocxDataMapping(
				inspectionWithoutRepeatTargets(inspection, repeatRows), sourceData, explicitFieldMapping, templateData, underlineData, mappingThreshold,
			)
			return encodeDocxAutomationPreview(inspection, mappingReport)
		}
		return t.inspectDocxTemplate(path)
	}

	mappingSummary := ""
	var mappingReport docxDataMappingReport
	if len(sourceData) > 0 && ext == ".docx" {
		inspection, inspectErr := analyzeDocxTemplate(path)
		if inspectErr != nil {
			return "", inspectErr
		}
		templateData, underlineData, mappingReport = buildDocxDataMapping(
			inspectionWithoutRepeatTargets(inspection, repeatRows), sourceData, explicitFieldMapping, templateData, underlineData, mappingThreshold,
		)
		if profileFingerprint == "" {
			profileFingerprint = inspection.Fingerprint
		}
		unresolved := unresolvedDocxMappings(mappingReport)
		if strict && len(unresolved) > 0 {
			return "", fmt.Errorf("DOCX data mapping unresolved: %s; run preview_mapping=true or provide field_mapping", strings.Join(unresolved, ", "))
		}
		mappingSummary = fmt.Sprintf("; auto-mapped fields=%d; unresolved mappings=%d", countMappedDocxFields(mappingReport), len(unresolved))
	}
	if len(sourceData) > 0 && ext == ".docx" && len(templateData) == 0 && len(underlineData) == 0 && len(underlineValues) == 0 && len(tableCells) == 0 && len(repeatRows) == 0 {
		return "", fmt.Errorf("no DOCX fields could be filled from source_data; run preview_mapping=true and provide field_mapping, table_cells, or repeat_rows")
	}

	var profileToSave *docxTemplateProfile
	if saveTemplateProfile {
		if profileFingerprint == "" {
			var fingerprintErr error
			profileFingerprint, fingerprintErr = docxTemplateFingerprint(path)
			if fingerprintErr != nil {
				return "", fingerprintErr
			}
		}
		profile, profileErr := buildDocxTemplateProfile(path, profileFingerprint, templateProfileName, mappingReport, tableCells, repeatRows, mappingThreshold)
		if profileErr != nil {
			return "", profileErr
		}
		profileToSave = &profile
	}

	// Template filling mode
	if (len(templateData) > 0 || len(underlineData) > 0 || len(underlineValues) > 0 || len(tableCells) > 0 || len(repeatRows) > 0) && ext == ".docx" {
		result, fillErr := t.fillDocxTemplateWithRepeatRows(path, templateData, underlineData, underlineValues, tableCells, repeatRows, output, strict, verifyComplete || strict)
		if fillErr != nil {
			return "", fillErr
		}
		if profileToSave != nil {
			profilePath, profileErr := saveDocxTemplateProfile(t.workspace, *profileToSave)
			if profileErr != nil {
				return "", fmt.Errorf("DOCX was filled at %s, but its reusable template profile could not be saved: %w", output, profileErr)
			}
			profileSummary += "; profile=saved:" + shortDocxFingerprint(profileFingerprint) + " at " + profilePath
		}
		return result + mappingSummary + profileSummary, nil
	}

	// Extraction mode
	switch ext {
	case ".pptx":
		text, err := ExtractPPTX(path)
		if err != nil {
			return "", err
		}
		return t.finalize(text, path, output, "native-pptx"), nil
	case ".pdf":
		return t.ExtractPDF(ctx, path, output)
	case ".docx", ".doc":
		return t.ExtractDocx(ctx, path, output)
	case ".mp3", ".wav", ".m4a", ".ogg", ".flac":
		return t.extractAudio(ctx, path, output)
	case ".mp4", ".mov", ".avi", ".mkv", ".webm":
		return t.extractVideo(ctx, path, output)
	default:
		return "", fmt.Errorf("unsupported format: %s (use .pptx .pdf .docx .mp3 .wav .mp4)", ext)
	}
}

// ========== PDF Extraction ==========

func (t *ExtractTool) ExtractPDF(ctx context.Context, path, output string) (string, error) {
	type extractionMethod struct {
		name    string
		minimum int
		run     func() (string, error)
	}
	methods := []extractionMethod{
		{
			name:    "native-go",
			minimum: 50,
			run:     func() (string, error) { return ExtractPDFWithLayout(path) },
		},
		{
			name:    "pdftotext",
			minimum: 50,
			run:     func() (string, error) { return t.ExtractPDFWithPdfToText(ctx, path) },
		},
		{
			name:    "pdf.js",
			minimum: 50,
			run:     func() (string, error) { return t.ExtractPDFWithNode(ctx, path) },
		},
		{
			name:    "unstructured",
			minimum: 50,
			run:     func() (string, error) { return t.extractPDFWithUnstructured(ctx, path) },
		},
		{
			name:    "pdfplumber",
			minimum: 50,
			run:     func() (string, error) { return t.extractPDFWithPdfPlumber(ctx, path) },
		},
		{
			name:    "pypdf",
			minimum: 50,
			run:     func() (string, error) { return t.extractPDFWithPyPDF(ctx, path) },
		},
		{
			name:    "raw",
			minimum: 20,
			run:     func() (string, error) { return t.ExtractPDFRaw(path) },
		},
	}

	failures := make([]string, 0, len(methods))
	for _, method := range methods {
		if err := ctx.Err(); err != nil {
			return "", fmt.Errorf("PDF extraction canceled: %w", err)
		}
		text, err := method.run()
		text = strings.TrimSpace(text)
		if err == nil && len([]rune(text)) > method.minimum {
			return t.finalize(text, path, output, method.name), nil
		}
		if err != nil {
			failures = append(failures, method.name+": "+err.Error())
		} else {
			failures = append(
				failures,
				fmt.Sprintf("%s: only %d extracted characters", method.name, len([]rune(text))),
			)
		}
	}

	return "", fmt.Errorf("PDF extraction failed (%s)", strings.Join(failures, "; "))
}

func (t *ExtractTool) extractPDFWithUnstructured(ctx context.Context, path string) (string, error) {
	python, err := exec.LookPath("python")
	if err != nil {
		python, err = exec.LookPath("python3")
	}
	if err != nil {
		return "", fmt.Errorf("python not found")
	}
	if t.dependencies.UnstructuredScript == nil {
		return "", fmt.Errorf("unstructured script resolver is not configured")
	}
	script, cleanup, err := t.dependencies.UnstructuredScript()
	if err != nil {
		return "", fmt.Errorf("unstructured script: %w", err)
	}
	defer cleanup()
	cmd := exec.CommandContext(ctx, python, script, path)
	cmd.Env = os.Environ()
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("unstructured: %w", err)
	}
	return string(out), nil
}

func (t *ExtractTool) extractPDFWithPdfPlumber(ctx context.Context, path string) (string, error) {
	python, err := exec.LookPath("python")
	if err != nil {
		python, err = exec.LookPath("python3")
	}
	if err != nil {
		return "", fmt.Errorf("python not found")
	}
	if t.dependencies.PDFScript == nil {
		return "", fmt.Errorf("pdf extraction script resolver is not configured")
	}
	script, cleanup, err := t.dependencies.PDFScript()
	if err != nil {
		return "", fmt.Errorf("pdfplumber script: %w", err)
	}
	defer cleanup()
	cmd := exec.CommandContext(ctx, python, script, path)
	cmd.Env = os.Environ()
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("pdfplumber: %w", err)
	}
	return string(out), nil
}

func (t *ExtractTool) extractPDFWithPyPDF(ctx context.Context, path string) (string, error) {
	python, err := exec.LookPath("python")
	if err != nil {
		python, err = exec.LookPath("python3")
	}
	if err != nil {
		return "", fmt.Errorf("python not found")
	}
	const script = `import sys
from pypdf import PdfReader

reader = PdfReader(sys.argv[1])
for index, page in enumerate(reader.pages, start=1):
    print(f"\n--- Page {index} ---\n")
    print(page.extract_text() or "")
`
	cmd := exec.CommandContext(ctx, python, "-c", script, path)
	cmd.Env = os.Environ()
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("pypdf: %w", err)
	}
	return string(out), nil
}

func (t *ExtractTool) ExtractPDFWithPdfToText(ctx context.Context, path string) (string, error) {
	cmd := exec.CommandContext(ctx, "pdftotext", "-layout", "-nopgbrk", path, "-")
	cmd.Env = os.Environ()
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

func (t *ExtractTool) ExtractPDFWithNode(ctx context.Context, path string) (string, error) {
	absPath, _ := filepath.Abs(path)
	absPath = strings.ReplaceAll(absPath, "\\", "\\\\")
	script := fmt.Sprintf(`
const fs=require('fs');
try{const p=require('pdfjs-dist/legacy/build/pdf.js');}catch(e){}
const pdfjsLib=require('pdfjs-dist/legacy/build/pdf.js');
(async()=>{
  const data=new Uint8Array(fs.readFileSync('%s'));
  const doc=await pdfjsLib.getDocument({data}).promise;
  let text='';
  for(let i=1;i<=doc.numPages;i++){
    const page=await doc.getPage(i);
    const content=await page.getTextContent();
    text+=content.items.map(it=>it.str).join(' ')+'\n';
  }
  console.log(text);
})()`, absPath)
	cmd := exec.CommandContext(ctx, "node", "-e", script)
	cmd.Dir = t.workspace
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

func (t *ExtractTool) ExtractPDFRaw(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	content := string(data)
	// Basic extraction: find text between stream/endstream and decode
	var result strings.Builder
	for {
		si := strings.Index(content, "BT\n")
		if si < 0 {
			break
		}
		ei := strings.Index(content[si:], "\nET")
		if ei < 0 {
			break
		}
		block := content[si+3 : si+ei]
		// Extract text between parentheses in Tj/TJ operators
		for _, line := range strings.Split(block, "\n") {
			if strings.Contains(line, "Tj") || strings.Contains(line, "TJ") {
				line = strings.ReplaceAll(line, "\\(", "(")
				for {
					ps := strings.Index(line, "(")
					pe := strings.Index(line, ")")
					if ps >= 0 && pe > ps {
						result.WriteString(line[ps+1:pe] + " ")
						line = line[pe+1:]
					} else {
						break
					}
				}
			}
		}
		content = content[si+ei+3:]
	}
	return strings.TrimSpace(result.String()), nil
}

// ========== DOCX Extraction ==========

func (t *ExtractTool) extractDocxWithUnstructured(ctx context.Context, path string) (string, error) {
	python, err := exec.LookPath("python")
	if err != nil {
		python, err = exec.LookPath("python3")
	}
	if err != nil {
		return "", fmt.Errorf("python not found")
	}
	if t.dependencies.UnstructuredScript == nil {
		return "", fmt.Errorf("unstructured script resolver is not configured")
	}
	script, cleanup, err := t.dependencies.UnstructuredScript()
	if err != nil {
		return "", fmt.Errorf("unstructured script: %w", err)
	}
	defer cleanup()
	cmd := exec.CommandContext(ctx, python, script, path)
	cmd.Env = os.Environ()
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("unstructured docx: %w", err)
	}
	return string(out), nil
}

func (t *ExtractTool) ExtractDocx(ctx context.Context, path, output string) (string, error) {
	// Prefer the in-process ZIP/XML parser. A regular DOCX should not require a
	// Python environment merely to expose its paragraphs and headings.
	text, nativeErr := t.extractDocxNative(path)
	if nativeErr == nil && strings.TrimSpace(text) != "" {
		return t.finalize(text, path, output, "docx-parser"), nil
	}

	// Unstructured remains the high-compatibility fallback for unusual OOXML
	// packages that the compact native parser cannot interpret.
	fallback, fallbackErr := t.extractDocxWithUnstructured(ctx, path)
	if fallbackErr == nil && len(strings.TrimSpace(fallback)) > 50 {
		return t.finalize(fallback, path, output, "unstructured"), nil
	}
	if nativeErr != nil {
		return "", fmt.Errorf("DOCX extraction failed (native-go: %v; unstructured: %v)", nativeErr, fallbackErr)
	}
	return "", fmt.Errorf("DOCX extraction failed (native-go returned no text; unstructured: %v)", fallbackErr)
}

func (t *ExtractTool) extractDocxNative(path string) (string, error) {
	reader, err := zip.OpenReader(path)
	if err != nil {
		return "", fmt.Errorf("open docx: %w", err)
	}
	defer reader.Close()

	var docXML []byte
	for _, f := range reader.File {
		if f.Name == "word/document.xml" {
			rc, err := f.Open()
			if err != nil {
				continue
			}
			docXML, err = io.ReadAll(rc)
			rc.Close()
			if err != nil {
				continue
			}
			break
		}
	}

	if len(docXML) == 0 {
		return "", fmt.Errorf("document.xml not found in docx")
	}

	// Parse paragraphs and headings
	return t.parseDocxXML(string(docXML)), nil
}

func (t *ExtractTool) parseDocxXML(xml string) string {
	var result strings.Builder
	// Simple state-machine parser for w:p (paragraphs) and w:t (text runs)
	inParagraph := false
	inText := false
	paraText := ""
	var headingLevel int

	lines := strings.Split(xml, "<")
	for _, line := range lines {
		if strings.HasPrefix(line, "w:p ") || strings.HasPrefix(line, "w:p>") {
			inParagraph = true
			paraText = ""
			headingLevel = 0
			// Check for heading style
			if strings.Contains(line, "Heading1") || strings.Contains(line, "heading1") {
				headingLevel = 1
			} else if strings.Contains(line, "Heading2") || strings.Contains(line, "heading2") {
				headingLevel = 2
			} else if strings.Contains(line, "Heading3") || strings.Contains(line, "heading3") {
				headingLevel = 3
			}
		}
		if strings.HasPrefix(line, "/w:p") {
			if inParagraph && paraText != "" {
				if headingLevel > 0 {
					result.WriteString(strings.Repeat("#", headingLevel) + " " + paraText + "\n\n")
				} else {
					result.WriteString(paraText + "\n\n")
				}
			}
			inParagraph = false
		}
		if inParagraph && (strings.HasPrefix(line, "w:t ") || strings.HasPrefix(line, "w:t>")) {
			// Extract text from w:t elements
			inText = true
			if idx := strings.Index(line, ">"); idx >= 0 {
				text := strings.TrimSpace(line[idx+1:])
				if text != "" {
					paraText += text
				}
			}
		}
		_ = inText
	}

	// Clean up
	resultStr := strings.TrimSpace(result.String())
	resultStr = strings.ReplaceAll(resultStr, "\n\n\n", "\n\n")
	return resultStr
}

// ========== Audio Extraction (Speech-to-Text) ==========

func (t *ExtractTool) extractAudio(ctx context.Context, path, output string) (string, error) {
	var text string
	var err error

	// Windows: try built-in SAPI first (free, no install)
	if runtime.GOOS == "windows" {
		text, err = t.transcribeWithWindowsSAPI(ctx, path)
		if err == nil && len(text) > 10 {
			return t.finalize(text, path, output, "windows-sapi"), nil
		}
	}

	// Try whisper CLI (if pre-installed, any platform)
	text, err = t.transcribeWithWhisper(ctx, path)
	if err == nil && len(text) > 20 {
		return t.finalize(text, path, output, "whisper.cpp"), nil
	}

	// Try Python whisper — auto-install if user allowed it in config
	text, err = t.transcribeWithPython(ctx, path, t.autoInstall)
	if err == nil && len(text) > 20 {
		return t.finalize(text, path, output, "whisper"), nil
	}

	return "", fmt.Errorf("语音识别不可用。运行: pip install faster-whisper")
}

func (t *ExtractTool) extractVideo(ctx context.Context, path, output string) (string, error) {
	ffmpegPath, err := processutil.ResolveFFmpegBinary(ctx, t.workspace, "ffmpeg", true)
	if err != nil {
		return "", fmt.Errorf("video extraction requires ffmpeg: %w", err)
	}

	tmpAudio := strings.TrimSuffix(path, filepath.Ext(path)) + ".extract.wav"
	defer os.Remove(tmpAudio)

	cmd := exec.CommandContext(ctx, ffmpegPath, "-y", "-i", path, "-vn", "-ac", "1", "-ar", "16000", "-acodec", "pcm_s16le", tmpAudio)
	cmd.Env = os.Environ()
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("extract audio from video failed: %w: %s", err, strings.TrimSpace(string(out)))
	}

	return t.extractAudio(ctx, tmpAudio, output)
}

func (t *ExtractTool) transcribeWithWhisper(ctx context.Context, path string) (string, error) {
	// Try whisper CLI
	cmd := exec.CommandContext(ctx, "whisper", path, "--model", "base", "--output_format", "txt", "--output_dir", os.TempDir())
	cmd.Env = os.Environ()
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

func (t *ExtractTool) transcribeWithPython(ctx context.Context, path string, autoInstall bool) (string, error) {
	absPath, _ := filepath.Abs(path)
	absPath = strings.ReplaceAll(absPath, "\\", "\\\\")
	script := fmt.Sprintf(`
import sys
try:
    import faster_whisper
    model = faster_whisper.WhisperModel("tiny", device="cpu", compute_type="int8")
    segments, _ := model.transcribe(r"%s")
    for seg in segments:
        print(seg.text, end="")
except ImportError:
    try:
        import whisper
        model = whisper.load_model("tiny")
        result = model.transcribe(r"%s")
        print(result["text"])
    except ImportError:
        print("WHISPER_NOT_INSTALLED")
    except Exception as e:
        print(f"ERROR: {e}")
except Exception as e:
    print(f"ERROR: {e}")
`, absPath, absPath)
	cmd := exec.CommandContext(ctx, "python", "-c", script)
	cmd.Env = os.Environ()
	out, err := cmd.Output()
	if err != nil {
		cmd = exec.CommandContext(ctx, "python3", "-c", script)
		out, err = cmd.Output()
	}

	result := strings.TrimSpace(string(out))

	// Auto-install if missing and allowed
	if strings.Contains(result, "WHISPER_NOT_INSTALLED") && autoInstall {
		log.Printf("[INFO] 正在自动安装 faster-whisper...")
		pipCmd := "pip"
		if runtime.GOOS != "windows" {
			pipCmd = "pip3" // Mac/Linux prefer pip3
		}
		installCmd := exec.CommandContext(ctx, pipCmd, "install", "faster-whisper")
		installCmd.Env = os.Environ()
		if installOut, installErr := installCmd.CombinedOutput(); installErr != nil {
			// Fallback: try pip
			installCmd2 := exec.CommandContext(ctx, "pip", "install", "faster-whisper")
			installCmd2.Env = os.Environ()
			if out2, err2 := installCmd2.CombinedOutput(); err2 != nil {
				return "", fmt.Errorf("安装失败: %v\n%s", err2, string(out2))
			}
			_ = installOut
		}
		log.Printf("[INFO] faster-whisper 安装成功，重试转录...")
		// Retry after install
		cmd2 := exec.CommandContext(ctx, "python", "-c", script)
		cmd2.Env = os.Environ()
		out2, err2 := cmd2.Output()
		if err2 != nil {
			return "", err2
		}
		result = strings.TrimSpace(string(out2))
	}

	if err != nil && strings.Contains(result, "WHISPER_NOT_INSTALLED") {
		return "", fmt.Errorf("whisper not installed. Run: pip install faster-whisper")
	}
	if strings.HasPrefix(result, "ERROR:") {
		return "", fmt.Errorf("%s", result)
	}
	if err != nil {
		return "", err
	}
	return result, nil
}

func (t *ExtractTool) transcribeWithWindowsSAPI(ctx context.Context, path string) (string, error) {
	absPath, _ := filepath.Abs(path)
	absPath = strings.ReplaceAll(absPath, "\\", "\\\\")
	script := fmt.Sprintf(`
Add-Type -AssemblyName System.Speech
$r = New-Object System.Speech.Recognition.SpeechRecognitionEngine
$r.SetInputToWaveFile('%s')
$r.LoadGrammar((New-Object System.Speech.Recognition.DictationGrammar))
$result = $r.Recognize()
if ($result) { Write-Host $result.Text } else { Write-Host 'NO_RESULT' }
$r.Dispose()
`, absPath)
	cmd := exec.CommandContext(ctx, "powershell", "-Command", script)
	cmd.Env = os.Environ()
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	result := strings.TrimSpace(string(out))
	if result == "NO_RESULT" || result == "" {
		return "", fmt.Errorf("no speech recognized")
	}
	return result, nil
}

// ========== Speak Tool (Text-to-Speech) ==========

type SpeakTool struct {
	workspace string
}

func NewSpeakTool(workspace string) *SpeakTool {
	return &SpeakTool{workspace: workspace}
}

func (t *SpeakTool) Name() string { return "speak" }

func (t *SpeakTool) Description() string {
	return "Speak text aloud with a cute sweet girl voice (Xiaoyi). Pass the COMPLETE content — do NOT summarize. Long texts are fully supported."
}

func (t *SpeakTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"text": map[string]interface{}{
				"type":        "string",
				"description": "Text to convert to speech",
			},
			"output": map[string]interface{}{
				"type":        "string",
				"description": "Output audio file path (e.g. 'storage/speech.mp3'). Default: storage/speak_<time>.mp3",
			},
			"voice": map[string]interface{}{
				"type":        "string",
				"description": "Voice to use. For edge-tts: zh-CN-XiaoyiNeural (cute sweet girl voice), en-US-JennyNeural (English female). Default: auto-detect.",
			},
		},
		"required": []string{"text"},
	}
}

func (t *SpeakTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	text, _ := params["text"].(string)
	output, _ := params["output"].(string)
	voice, _ := params["voice"].(string)

	if text == "" {
		return "", fmt.Errorf("text is required")
	}
	if output == "" {
		// Organize by task: .openkitty/storage/{task_id}/speak/
		taskID := ""
		if id, ok := ctx.Value("task_id").(string); ok {
			taskID = id
		} else {
			taskID = fmt.Sprintf("speak_%s", time.Now().Format("150405"))
		}
		output = filepath.Join(t.workspace, ".openkitty", "storage", taskID, fmt.Sprintf("speak_%s.mp3", time.Now().Format("150405")))
	}
	if !filepath.IsAbs(output) {
		output = filepath.Join(t.workspace, output)
	}
	os.MkdirAll(filepath.Dir(output), 0755)

	fileSize := func() string {
		if fi, err := os.Stat(output); err == nil {
			return fmt.Sprintf(" (%.1f KB)", float64(fi.Size())/1024)
		}
		return ""
	}

	// edge-tts first: uses XiaoyiNeural (cute sweet voice) — best quality
	if err := t.speakWithEdgeTTS(ctx, text, output, voice); err == nil {
		t.playAudio(ctx, output)
		return fmt.Sprintf("🔊 Speaked:%s %s", fileSize(), output), nil
	}

	// Fallback: system TTS (Windows SAPI direct speak, macOS say, Linux espeak)
	if err := t.speakWithSystem(ctx, text, output); err == nil {
		t.playAudio(ctx, output)
		return fmt.Sprintf("🔊 Speaked (system): %s", output), nil
	}

	return "", fmt.Errorf("TTS failed. Install edge-tts: pip install edge-tts")
}

func (t *SpeakTool) speakWithEdgeTTS(ctx context.Context, text, output, voice string) error {
	if voice == "" {
		if hasChinese(text) {
			voice = "zh-CN-XiaoyiNeural"
		} else {
			voice = "en-US-JennyNeural"
		}
	}
	// edge-tts outputs MP3; convert to WAV for Windows compatibility
	tmpMP3 := strings.TrimSuffix(output, filepath.Ext(output)) + "_tmp.mp3"
	var cmd *exec.Cmd
	if len(text) > 500 {
		// Long text: use --file to avoid CLI arg length limits
		tmpText := filepath.Join(os.TempDir(), "edge_tts_text.txt")
		os.WriteFile(tmpText, []byte(text), 0644)
		defer os.Remove(tmpText)
		cmd = exec.CommandContext(ctx, "edge-tts", "--voice", voice, "--file", tmpText, "--write-media", tmpMP3)
	} else {
		cmd = exec.CommandContext(ctx, "edge-tts", "--voice", voice, "--text", text, "--write-media", tmpMP3)
	}
	cmd.Env = os.Environ()
	if err := cmd.Run(); err != nil {
		return err
	}
	defer os.Remove(tmpMP3)

	// Convert MP3 to WAV via ffmpeg if available
	if ffmpegPath, err := processutil.ResolveFFmpegBinary(ctx, t.workspace, "ffmpeg", false); err == nil {
		wavOut := strings.TrimSuffix(output, filepath.Ext(output)) + ".wav"
		conv := exec.CommandContext(ctx, ffmpegPath, "-i", tmpMP3, "-y", wavOut)
		if conv.Run() == nil {
			// Rename wav to the requested output if different
			if wavOut != output {
				os.Rename(wavOut, output)
			}
			return nil
		}
	}
	// Fallback: just copy the mp3
	data, _ := os.ReadFile(tmpMP3)
	return os.WriteFile(output, data, 0644)
}

func (t *SpeakTool) speakWithSystem(ctx context.Context, text, output string) error {
	// macOS: say -o output.aiff, then convert to mp3
	if _, err := exec.LookPath("say"); err == nil {
		aiff := strings.TrimSuffix(output, filepath.Ext(output)) + ".aiff"
		cmd := exec.CommandContext(ctx, "say", "-o", aiff, text)
		if err := cmd.Run(); err != nil {
			return err
		}
		// Convert to mp3 if ffmpeg available
		if ffmpegPath, err := processutil.ResolveFFmpegBinary(ctx, t.workspace, "ffmpeg", false); err == nil {
			exec.CommandContext(ctx, ffmpegPath, "-i", aiff, "-codec:a", "libmp3lame", "-q:a", "2", output).Run()
			os.Remove(aiff)
		}
		return nil
	}
	// Linux: espeak-ng or espeak
	if _, err := exec.LookPath("espeak-ng"); err == nil {
		wavOut := strings.TrimSuffix(output, filepath.Ext(output)) + ".wav"
		cmd := exec.CommandContext(ctx, "espeak-ng", text, "-w", wavOut, "-v", "zh")
		if err := cmd.Run(); err != nil {
			// Try without voice flag
			cmd = exec.CommandContext(ctx, "espeak-ng", text, "-w", wavOut)
			if err := cmd.Run(); err != nil {
				return err
			}
		}
		if wavOut != output {
			os.Rename(wavOut, output)
		}
		return nil
	}
	if _, err := exec.LookPath("espeak"); err == nil {
		wavOut := strings.TrimSuffix(output, filepath.Ext(output)) + ".wav"
		cmd := exec.CommandContext(ctx, "espeak", text, "-w", wavOut)
		if err := cmd.Run(); err != nil {
			return err
		}
		if wavOut != output {
			os.Rename(wavOut, output)
		}
		return nil
	}
	// Windows: SAPI via PowerShell — speak directly AND save to file
	if _, err := exec.LookPath("powershell"); err == nil {
		escapedText := strings.ReplaceAll(text, "'", "''")
		escapedPath := strings.ReplaceAll(output, "'", "''")
		// First: speak directly to speakers (instant audio)
		script := fmt.Sprintf(
			`Add-Type -AssemblyName System.Speech; $s=New-Object System.Speech.Synthesis.SpeechSynthesizer; $s.Speak('%s')`,
			escapedText,
		)
		cmd := exec.CommandContext(ctx, "powershell", "-Command", script)
		cmd.Run() // Fire and forget — user hears it immediately
		// Second: save to file for reference
		saveScript := fmt.Sprintf(
			`Add-Type -AssemblyName System.Speech; $s=New-Object System.Speech.Synthesis.SpeechSynthesizer; $s.SetOutputToWaveFile('%s'); $s.Speak('%s')`,
			escapedPath, escapedText,
		)
		exec.CommandContext(ctx, "powershell", "-Command", saveScript).Run()
		return nil
	}
	return fmt.Errorf("no TTS engine available")
}

// playAudio plays the generated audio file using the system's default player.
func (t *SpeakTool) playAudio(ctx context.Context, path string) {
	ext := strings.ToLower(filepath.Ext(path))
	absPath, _ := filepath.Abs(path)

	// ffplay: plays audio without window, auto-exits when done (best UX)
	if ffplayPath, err := processutil.ResolveFFmpegBinary(ctx, t.workspace, "ffplay", false); err == nil {
		cmd := exec.CommandContext(context.Background(), ffplayPath, "-nodisp", "-autoexit", "-loglevel", "quiet", absPath)
		cmd.Start()
		go func() { cmd.Wait() }()
		return
	}

	// Windows PowerShell: reliable playback for WAV
	if _, err := exec.LookPath("powershell"); err == nil {
		if ext == ".wav" {
			script := fmt.Sprintf(
				`$player = New-Object System.Media.SoundPlayer; $player.SoundLocation = '%s'; $player.PlaySync()`,
				strings.ReplaceAll(absPath, "'", "''"),
			)
			exec.CommandContext(context.Background(), "powershell", "-Command", script).Start()
			return
		}
		// For MP3: use Speech.Synthesizer to speak the file
		// Fallback: open in default player
		exec.CommandContext(ctx, "powershell", "-Command",
			fmt.Sprintf(`$wmp = New-Object -ComObject WMPlayer.OCX; $wmp.URL = '%s'; Start-Sleep -Seconds 3`,
				strings.ReplaceAll(absPath, "'", "''"),
			),
		).Start()
		return
	}

	// macOS: afplay
	if _, err := exec.LookPath("afplay"); err == nil {
		exec.CommandContext(ctx, "afplay", absPath).Start()
		return
	}

	// Linux: try multiple players
	for _, player := range []string{"paplay", "aplay", "mpv", "gst-play-1.0"} {
		if _, err := exec.LookPath(player); err == nil {
			args := []string{absPath}
			if player == "mpv" {
				args = []string{"--no-video", "--really-quiet", absPath}
			}
			exec.CommandContext(ctx, player, args...).Start()
			return
		}
	}
}

func hasChinese(s string) bool {
	for _, r := range s {
		if r >= 0x4e00 && r <= 0x9fff {
			return true
		}
	}
	return false
}

// ========== Template Filling ==========

func (t *ExtractTool) fillDocxTemplate(templatePath string, data map[string]interface{}, output string) (string, error) {
	return t.fillDocxTemplateAdvanced(templatePath, data, nil, nil, output, false)
}

// ========== Helpers ==========

func (t *ExtractTool) finalize(text, sourcePath, outputPath, method string) string {
	text = strings.TrimSpace(text)
	text = strings.ReplaceAll(text, "\r\n", "\n")

	header := fmt.Sprintf("# Extracted from: %s\n**Method**: %s\n**Time**: %s\n\n",
		filepath.Base(sourcePath), method, time.Now().Format("2006-01-02 15:04"))
	result := header + text

	// Save to output file if specified
	if outputPath != "" {
		if !filepath.IsAbs(outputPath) {
			outputPath = filepath.Join(t.workspace, outputPath)
		}
		os.MkdirAll(filepath.Dir(outputPath), 0755)
		os.WriteFile(outputPath, []byte(result), 0644)
	}

	// Auto-chunk and store as knowledge for RAG retrieval
	t.chunkAndStore(text, sourcePath)

	preview := result
	if len(preview) > 5000 {
		preview = preview[:5000] + "\n\n...(truncated)"
	}
	return preview
}

// chunkAndStore splits extracted text into semantic chunks and stores in knowledge base.
func (t *ExtractTool) chunkAndStore(text, sourcePath string) {
	chunks := semanticChunk(text, 800, 2000)
	knowledgeDir := filepath.Join(t.workspace, "knowledge_chunks")
	os.MkdirAll(knowledgeDir, 0755)

	docName := strings.TrimSuffix(filepath.Base(sourcePath), filepath.Ext(sourcePath))
	chunkFile := filepath.Join(knowledgeDir, docName+"_chunks.md")

	var out strings.Builder
	out.WriteString(fmt.Sprintf("# Chunks from: %s\n**Date**: %s\n\n",
		sourcePath, time.Now().Format("2006-01-02 15:04")))
	for i, chunk := range chunks {
		out.WriteString(fmt.Sprintf("## Chunk %d\n\n%s\n\n---\n\n", i+1, chunk))
	}
	os.WriteFile(chunkFile, []byte(out.String()), 0644)
}

// semanticChunk splits text by headings and paragraph boundaries, aiming for targetSize each.
func semanticChunk(text string, minSize, maxSize int) []string {
	var chunks []string
	// Split by headings first
	sections := strings.Split(text, "\n#")
	for i, s := range sections {
		if i > 0 {
			s = "#" + s
		}
		if len(s) < minSize {
			continue
		}
		// If section is small enough, keep as one chunk
		if len(s) <= maxSize {
			chunks = append(chunks, strings.TrimSpace(s))
			continue
		}
		// Split large section by paragraph boundaries
		paragraphs := strings.Split(s, "\n\n")
		var current strings.Builder
		for _, p := range paragraphs {
			if current.Len()+len(p) > maxSize && current.Len() > minSize {
				chunks = append(chunks, strings.TrimSpace(current.String()))
				current.Reset()
			}
			if current.Len() > 0 {
				current.WriteString("\n\n")
			}
			current.WriteString(p)
		}
		if current.Len() >= minSize {
			chunks = append(chunks, strings.TrimSpace(current.String()))
		}
	}
	return chunks
}

var _ = exec.ErrNotFound
