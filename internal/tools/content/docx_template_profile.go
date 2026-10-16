package content

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"mac/internal/fsutil"
)

const docxTemplateProfileVersion = 1

type docxTemplateProfile struct {
	Version          int                    `json:"version"`
	Fingerprint      string                 `json:"fingerprint"`
	Name             string                 `json:"name,omitempty"`
	TemplateName     string                 `json:"template_name"`
	FieldMapping     map[string]string      `json:"field_mapping,omitempty"`
	TableCells       []docxTableCellProfile `json:"table_cells,omitempty"`
	RepeatRows       []docxRepeatRowProfile `json:"repeat_rows,omitempty"`
	MappingThreshold float64                `json:"mapping_threshold"`
	CreatedAt        string                 `json:"created_at"`
	UpdatedAt        string                 `json:"updated_at"`
}

// docxTableCellProfile stores only an address and a future data path. Resolved
// experiment/customer values are intentionally never persisted.
type docxTableCellProfile struct {
	Part       string `json:"part"`
	TableIndex int    `json:"table_index"`
	Row        int    `json:"row"`
	Cell       int    `json:"cell"`
	SourcePath string `json:"source_path"`
}

// docxRepeatRowProfile intentionally omits resolved row values. A reusable
// profile records where future rows come from, never the current experiment data.
type docxRepeatRowProfile struct {
	Part                  string   `json:"part"`
	TableIndex            int      `json:"table_index"`
	TemplateRow           int      `json:"template_row"`
	DataPath              string   `json:"data_path"`
	ColumnKeys            []string `json:"column_keys,omitempty"`
	RemoveTemplateIfEmpty bool     `json:"remove_template_if_empty,omitempty"`
}

func docxTemplateFingerprint(templatePath string) (string, error) {
	reader, err := zip.OpenReader(templatePath)
	if err != nil {
		return "", fmt.Errorf("open template for fingerprint: %w", err)
	}
	defer reader.Close()

	parts := make(map[string][]byte)
	for _, file := range reader.File {
		if !isDocxTextPart(file.Name) {
			continue
		}
		content, readErr := readZipFile(file)
		if readErr != nil {
			return "", fmt.Errorf("read template fingerprint part %s: %w", file.Name, readErr)
		}
		parts[normalizeDocxPartName(file.Name)] = content
	}
	if len(parts) == 0 {
		return "", fmt.Errorf("DOCX template has no document text parts")
	}
	return fingerprintDocxTextParts(parts), nil
}

func fingerprintDocxTextParts(parts map[string][]byte) string {
	names := make([]string, 0, len(parts))
	for name := range parts {
		names = append(names, normalizeDocxPartName(name))
	}
	sort.Strings(names)

	hash := sha256.New()
	for _, name := range names {
		_, _ = hash.Write([]byte(name))
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write(parts[name])
		_, _ = hash.Write([]byte{0})
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func docxTemplateProfilePath(workspace, fingerprint string) string {
	workspace = strings.TrimSpace(workspace)
	if workspace == "" {
		workspace = "."
	}
	return filepath.Join(workspace, ".openkitty", "document-templates", fingerprint+".json")
}

func shortDocxFingerprint(fingerprint string) string {
	if len(fingerprint) <= 12 {
		return fingerprint
	}
	return fingerprint[:12]
}

func loadDocxTemplateProfile(workspace, fingerprint string) (docxTemplateProfile, bool, error) {
	path := docxTemplateProfilePath(workspace, fingerprint)
	content, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return docxTemplateProfile{}, false, nil
	}
	if err != nil {
		return docxTemplateProfile{}, false, fmt.Errorf("read DOCX template profile: %w", err)
	}

	var profile docxTemplateProfile
	if err := json.Unmarshal(content, &profile); err != nil {
		return docxTemplateProfile{}, false, fmt.Errorf("decode DOCX template profile %s: %w", path, err)
	}
	if err := validateDocxTemplateProfile(profile, fingerprint); err != nil {
		return docxTemplateProfile{}, false, fmt.Errorf("invalid DOCX template profile %s: %w", path, err)
	}
	return profile, true, nil
}

func validateDocxTemplateProfile(profile docxTemplateProfile, expectedFingerprint string) error {
	if profile.Version != docxTemplateProfileVersion {
		return fmt.Errorf("unsupported version %d", profile.Version)
	}
	if profile.Fingerprint == "" || !strings.EqualFold(profile.Fingerprint, expectedFingerprint) {
		return fmt.Errorf("fingerprint does not match the current template")
	}
	for index, row := range profile.RepeatRows {
		if row.TableIndex <= 0 || row.TemplateRow <= 0 || strings.TrimSpace(row.DataPath) == "" {
			return fmt.Errorf("repeat_rows[%d] requires table_index, template_row, and data_path", index)
		}
	}
	for index, cell := range profile.TableCells {
		if cell.TableIndex <= 0 || cell.Row <= 0 || cell.Cell <= 0 || strings.TrimSpace(cell.SourcePath) == "" {
			return fmt.Errorf("table_cells[%d] requires table_index, row, cell, and source_path", index)
		}
	}
	return nil
}

func saveDocxTemplateProfile(workspace string, profile docxTemplateProfile) (string, error) {
	if err := validateDocxTemplateProfile(profile, profile.Fingerprint); err != nil {
		return "", err
	}
	path := docxTemplateProfilePath(workspace, profile.Fingerprint)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", fmt.Errorf("create DOCX template profile directory: %w", err)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	if existing, found, err := loadDocxTemplateProfile(workspace, profile.Fingerprint); err != nil {
		return "", err
	} else if found {
		profile.CreatedAt = existing.CreatedAt
	}
	if profile.CreatedAt == "" {
		profile.CreatedAt = now
	}
	profile.UpdatedAt = now
	encoded, err := json.MarshalIndent(profile, "", "  ")
	if err != nil {
		return "", fmt.Errorf("encode DOCX template profile: %w", err)
	}
	encoded = append(encoded, '\n')

	stagingPath := filepath.Join(filepath.Dir(path), fmt.Sprintf(".%s-%d.stage", shortDocxFingerprint(profile.Fingerprint), time.Now().UnixNano()))
	defer os.Remove(stagingPath)
	if err := fsutil.WriteAtomic(stagingPath, encoded, 0o644); err != nil {
		return "", fmt.Errorf("stage DOCX template profile: %w", err)
	}
	if err := replaceFileFromTemp(stagingPath, path); err != nil {
		return "", fmt.Errorf("commit DOCX template profile: %w", err)
	}
	return path, nil
}

func buildDocxTemplateProfile(
	templatePath, fingerprint, name string,
	report docxDataMappingReport,
	tableCells []docxTableCellPlan,
	repeatRows []docxRepeatRowPlan,
	mappingThreshold float64,
) (docxTemplateProfile, error) {
	if mappingThreshold <= 0 || mappingThreshold > 1 {
		mappingThreshold = defaultDocxMappingThreshold
	}
	if report.Threshold > 0 && report.Threshold <= 1 {
		mappingThreshold = report.Threshold
	}
	profile := docxTemplateProfile{
		Version:          docxTemplateProfileVersion,
		Fingerprint:      fingerprint,
		Name:             strings.TrimSpace(name),
		TemplateName:     filepath.Base(templatePath),
		FieldMapping:     successfulDocxFieldMappings(report),
		MappingThreshold: mappingThreshold,
	}
	for _, plan := range tableCells {
		if strings.TrimSpace(plan.SourcePath) == "" {
			continue
		}
		profile.TableCells = append(profile.TableCells, docxTableCellProfile{
			Part: normalizeDocxPartName(plan.Part), TableIndex: plan.TableIndex,
			Row: plan.Row, Cell: plan.Cell, SourcePath: plan.SourcePath,
		})
	}
	for _, plan := range repeatRows {
		if strings.TrimSpace(plan.DataPath) == "" {
			continue
		}
		profile.RepeatRows = append(profile.RepeatRows, docxRepeatRowProfile{
			Part:                  normalizeDocxPartName(plan.Part),
			TableIndex:            plan.TableIndex,
			TemplateRow:           plan.TemplateRow,
			DataPath:              plan.DataPath,
			ColumnKeys:            append([]string{}, plan.ColumnKeys...),
			RemoveTemplateIfEmpty: plan.RemoveTemplateIfEmpty,
		})
	}
	if len(profile.FieldMapping) == 0 && len(profile.TableCells) == 0 && len(profile.RepeatRows) == 0 {
		return docxTemplateProfile{}, fmt.Errorf("no reusable DOCX mapping was produced; provide source_data mappings, table_cells with source_path, or repeat_rows with data_path")
	}
	return profile, nil
}

func docxProfileTableCellsRaw(cells []docxTableCellProfile) []interface{} {
	result := make([]interface{}, 0, len(cells))
	for _, cell := range cells {
		result = append(result, map[string]interface{}{
			"part": cell.Part, "table_index": cell.TableIndex,
			"row": cell.Row, "cell": cell.Cell, "source_path": cell.SourcePath,
		})
	}
	return result
}

func successfulDocxFieldMappings(report docxDataMappingReport) map[string]string {
	mapping := make(map[string]string)
	for _, field := range report.Fields {
		if field.Status != "mapped" || strings.TrimSpace(field.SourcePath) == "" {
			continue
		}
		mapping[field.Target] = field.SourcePath
	}
	return mapping
}

func mergeDocxFieldMappings(base, override map[string]string) map[string]string {
	merged := make(map[string]string, len(base)+len(override))
	for target, source := range base {
		merged[target] = source
	}
	for target, source := range override {
		for existing := range merged {
			if strings.EqualFold(strings.TrimSpace(existing), strings.TrimSpace(target)) {
				delete(merged, existing)
			}
		}
		merged[target] = source
	}
	return merged
}

func docxProfileRepeatRowsRaw(rows []docxRepeatRowProfile) []interface{} {
	result := make([]interface{}, 0, len(rows))
	for _, row := range rows {
		result = append(result, map[string]interface{}{
			"part":                     row.Part,
			"table_index":              row.TableIndex,
			"template_row":             row.TemplateRow,
			"data_path":                row.DataPath,
			"column_keys":              append([]string{}, row.ColumnKeys...),
			"remove_template_if_empty": row.RemoveTemplateIfEmpty,
		})
	}
	return result
}
