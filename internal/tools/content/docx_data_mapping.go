package content

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"unicode"
)

const defaultDocxMappingThreshold = 0.88

type docxDataMappingReport struct {
	Threshold            float64                          `json:"threshold"`
	Fields               []docxFieldMappingResult         `json:"fields"`
	TableCellSuggestions []docxTableCellMappingSuggestion `json:"table_cell_suggestions,omitempty"`
	UnmatchedFields      []string                         `json:"unmatched_fields,omitempty"`
	AmbiguousFields      []string                         `json:"ambiguous_fields,omitempty"`
	UnusedSourcePaths    []string                         `json:"unused_source_paths,omitempty"`
}

// docxTableCellMappingSuggestion is advisory: it helps the agent turn a
// structural table candidate into a declarative table_cells plan. It is not
// silently applied, because visually blank cells can carry business meaning
// that cannot be proven from OOXML alone.
type docxTableCellMappingSuggestion struct {
	Address      string   `json:"address"`
	Label        string   `json:"label"`
	SourcePath   string   `json:"source_path,omitempty"`
	Confidence   float64  `json:"confidence"`
	Status       string   `json:"status"`
	Reason       string   `json:"reason"`
	Alternatives []string `json:"alternatives,omitempty"`
}

type docxFieldMappingResult struct {
	Target       string   `json:"target"`
	Kind         string   `json:"kind"`
	SourcePath   string   `json:"source_path,omitempty"`
	Confidence   float64  `json:"confidence"`
	Status       string   `json:"status"`
	Reason       string   `json:"reason"`
	Alternatives []string `json:"alternatives,omitempty"`
}

type docxAutomationPreview struct {
	Inspection docxTemplateInspection `json:"inspection"`
	Mapping    docxDataMappingReport  `json:"mapping"`
}

type docxMappingTarget struct {
	key  string
	kind string
}

type docxSourceField struct {
	path  string
	leaf  string
	value interface{}
}

type scoredDocxSource struct {
	field      docxSourceField
	confidence float64
	reason     string
}

var docxFieldAliasGroups = map[string][]string{
	"sample_id":     {"sample_id", "sample_no", "sample_number", "specimen_id", "specimen_no", "样品编号", "样本编号", "样品号", "样本号", "送检编号"},
	"batch_id":      {"batch_id", "batch_no", "lot_no", "lot_number", "批号", "批次", "批次号", "生产批号"},
	"serial_number": {"serial_number", "sequence", "seq", "index", "序号", "编号"},
	"company":       {"company", "enterprise", "customer", "client", "organization", "企业", "企业名称", "单位名称", "委托单位", "客户名称", "送检单位"},
	"product":       {"product", "product_name", "sample_name", "specimen_name", "产品名称", "样品名称", "样本名称", "品名"},
	"test_item":     {"test_item", "test_name", "assay", "analysis_item", "检测项目", "检验项目", "试验项目", "分析项目", "项目名称"},
	"test_result":   {"test_result", "result", "measured_value", "value", "检测结果", "检验结果", "试验结果", "实测值", "结果"},
	"unit":          {"unit", "measurement_unit", "result_unit", "单位", "计量单位"},
	"method":        {"method", "test_method", "analysis_method", "检测方法", "检验方法", "试验方法", "分析方法"},
	"standard":      {"standard", "basis", "reference", "specification", "检测依据", "检验依据", "判定依据", "执行标准", "标准"},
	"instrument":    {"instrument", "equipment", "device", "instrument_name", "仪器", "设备", "检测仪器", "仪器名称", "设备名称"},
	"operator":      {"operator", "tester", "analyst", "inspector", "performed_by", "检测人", "检验员", "操作人", "分析员", "试验人员"},
	"reviewer":      {"reviewer", "checker", "verified_by", "复核人", "审核人", "校核人", "批准人"},
	"test_date":     {"test_date", "inspection_date", "analysis_date", "date", "检测日期", "检验日期", "试验日期", "分析日期", "日期"},
	"report_number": {"report_number", "report_no", "certificate_no", "报告编号", "报告号", "证书编号"},
	"conclusion":    {"conclusion", "judgement", "assessment", "判定", "结论", "检测结论", "检验结论"},
}

func buildDocxDataMapping(
	inspection docxTemplateInspection,
	sourceData map[string]interface{},
	explicitMapping map[string]string,
	templateData map[string]interface{},
	underlineData map[string]interface{},
	threshold float64,
) (map[string]interface{}, map[string]interface{}, docxDataMappingReport) {
	if threshold <= 0 || threshold > 1 {
		threshold = defaultDocxMappingThreshold
	}
	resolvedTemplate := cloneInterfaceMap(templateData)
	resolvedUnderline := cloneInterfaceMap(underlineData)
	targets := docxMappingTargets(inspection)
	sources := flattenDocxSourceData(sourceData)
	usedSources := make(map[string]bool)
	report := docxDataMappingReport{Threshold: threshold}
	report.TableCellSuggestions = suggestDocxTableCellMappings(inspection, sources, threshold)

	for _, target := range targets {
		if existingValue, exists := providedDocxTargetValue(target, resolvedTemplate, resolvedUnderline); exists {
			_ = existingValue
			report.Fields = append(report.Fields, docxFieldMappingResult{
				Target: target.key, Kind: target.kind, Confidence: 1, Status: "mapped", Reason: "provided directly",
			})
			continue
		}

		if sourcePath, explicit := lookupExplicitDocxMapping(explicitMapping, target.key); explicit {
			if source, ok := lookupDocxSourcePath(sources, sourcePath); ok {
				setDocxTargetValue(target, source.value, resolvedTemplate, resolvedUnderline)
				usedSources[source.path] = true
				report.Fields = append(report.Fields, docxFieldMappingResult{
					Target: target.key, Kind: target.kind, SourcePath: source.path, Confidence: 1, Status: "mapped", Reason: "explicit field_mapping",
				})
			} else {
				report.Fields = append(report.Fields, docxFieldMappingResult{
					Target: target.key, Kind: target.kind, SourcePath: sourcePath, Status: "unmatched", Reason: "explicit source path not found",
				})
				report.UnmatchedFields = append(report.UnmatchedFields, target.kind+":"+target.key)
			}
			continue
		}

		candidates := scoreDocxSourceFields(target.key, sources)
		if len(candidates) == 0 || candidates[0].confidence < threshold {
			result := docxFieldMappingResult{Target: target.key, Kind: target.kind, Status: "unmatched", Reason: "no source field reached mapping threshold"}
			if len(candidates) > 0 {
				result.SourcePath = candidates[0].field.path
				result.Confidence = roundDocxConfidence(candidates[0].confidence)
				result.Alternatives = docxMappingAlternatives(candidates, 3)
			}
			report.Fields = append(report.Fields, result)
			report.UnmatchedFields = append(report.UnmatchedFields, target.kind+":"+target.key)
			continue
		}
		if len(candidates) > 1 && candidates[0].confidence-candidates[1].confidence < 0.03 {
			report.Fields = append(report.Fields, docxFieldMappingResult{
				Target: target.key, Kind: target.kind, SourcePath: candidates[0].field.path,
				Confidence: roundDocxConfidence(candidates[0].confidence), Status: "ambiguous",
				Reason: "multiple source fields have nearly equal scores", Alternatives: docxMappingAlternatives(candidates, 3),
			})
			report.AmbiguousFields = append(report.AmbiguousFields, target.kind+":"+target.key)
			continue
		}

		winner := candidates[0]
		setDocxTargetValue(target, winner.field.value, resolvedTemplate, resolvedUnderline)
		usedSources[winner.field.path] = true
		report.Fields = append(report.Fields, docxFieldMappingResult{
			Target: target.key, Kind: target.kind, SourcePath: winner.field.path,
			Confidence: roundDocxConfidence(winner.confidence), Status: "mapped", Reason: winner.reason,
			Alternatives: docxMappingAlternatives(candidates[1:], 2),
		})
	}

	for _, source := range sources {
		if !usedSources[source.path] {
			report.UnusedSourcePaths = append(report.UnusedSourcePaths, source.path)
		}
	}
	sort.Strings(report.UnmatchedFields)
	sort.Strings(report.AmbiguousFields)
	sort.Strings(report.UnusedSourcePaths)
	return resolvedTemplate, resolvedUnderline, report
}

func suggestDocxTableCellMappings(inspection docxTemplateInspection, sources []docxSourceField, threshold float64) []docxTableCellMappingSuggestion {
	var suggestions []docxTableCellMappingSuggestion
	for _, table := range inspection.Tables {
		for _, row := range table.Rows {
			for _, cell := range row.CellDetails {
				if !cell.Candidate || strings.TrimSpace(cell.Label) == "" {
					continue
				}
				suggestion := docxTableCellMappingSuggestion{
					Address: cell.Address, Label: cell.Label, Status: "unmatched",
					Reason: cell.Reason + "; no source field reached mapping threshold",
				}
				candidates := scoreDocxSourceFields(cell.Label, sources)
				if len(candidates) > 0 {
					suggestion.SourcePath = candidates[0].field.path
					suggestion.Confidence = roundDocxConfidence(candidates[0].confidence)
					suggestion.Alternatives = docxMappingAlternatives(candidates[1:], 2)
				}
				if len(candidates) > 1 && candidates[0].confidence >= threshold && candidates[0].confidence-candidates[1].confidence < 0.03 {
					suggestion.Status = "ambiguous"
					suggestion.Reason = cell.Reason + "; multiple source fields have nearly equal scores"
				} else if len(candidates) > 0 && candidates[0].confidence >= threshold {
					suggestion.Status = "suggested"
					suggestion.Reason = cell.Reason + "; " + candidates[0].reason
				}
				suggestions = append(suggestions, suggestion)
			}
		}
	}
	return suggestions
}

func docxMappingTargets(inspection docxTemplateInspection) []docxMappingTarget {
	seen := make(map[string]bool)
	var targets []docxMappingTarget
	appendTarget := func(kind, key string) {
		key = strings.TrimSpace(key)
		identity := kind + "\x00" + strings.ToLower(key)
		if key == "" || seen[identity] {
			return
		}
		seen[identity] = true
		targets = append(targets, docxMappingTarget{key: key, kind: kind})
	}
	for _, item := range inspection.Placeholders {
		appendTarget("template", item.Key)
	}
	for _, item := range inspection.ContentControls {
		appendTarget("template", item.Key)
	}
	for _, item := range inspection.MergeFields {
		appendTarget("template", item.Key)
	}
	for _, item := range inspection.Bookmarks {
		if item.Fillable {
			appendTarget("template", item.Key)
		}
	}
	for _, item := range inspection.Underlined {
		appendTarget("underline", item.Label)
	}
	sort.Slice(targets, func(i, j int) bool {
		if targets[i].kind != targets[j].kind {
			return targets[i].kind < targets[j].kind
		}
		return targets[i].key < targets[j].key
	})
	return targets
}

func flattenDocxSourceData(source map[string]interface{}) []docxSourceField {
	var fields []docxSourceField
	var walk func(string, interface{})
	walk = func(path string, value interface{}) {
		switch typed := value.(type) {
		case map[string]interface{}:
			keys := make([]string, 0, len(typed))
			for key := range typed {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			for _, key := range keys {
				childPath := key
				if path != "" {
					childPath = path + "." + key
				}
				walk(childPath, typed[key])
			}
		case []interface{}:
			// Arrays are consumed by repeat_rows and are intentionally excluded
			// from one-value field matching.
		default:
			leaf := path
			if index := strings.LastIndex(path, "."); index >= 0 {
				leaf = path[index+1:]
			}
			if path != "" {
				fields = append(fields, docxSourceField{path: path, leaf: leaf, value: value})
			}
		}
	}
	walk("", source)
	return fields
}

func scoreDocxSourceFields(target string, sources []docxSourceField) []scoredDocxSource {
	candidates := make([]scoredDocxSource, 0, len(sources))
	for _, source := range sources {
		confidence, reason := scoreDocxFieldNames(target, source.leaf)
		if confidence <= 0 {
			confidence, reason = scoreDocxFieldNames(target, source.path)
		}
		if confidence > 0 {
			candidates = append(candidates, scoredDocxSource{field: source, confidence: confidence, reason: reason})
		}
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].confidence != candidates[j].confidence {
			return candidates[i].confidence > candidates[j].confidence
		}
		return candidates[i].field.path < candidates[j].field.path
	})
	return candidates
}

func scoreDocxFieldNames(target, source string) (float64, string) {
	targetNormalized := normalizeDocxFieldName(target)
	sourceNormalized := normalizeDocxFieldName(source)
	if targetNormalized == "" || sourceNormalized == "" {
		return 0, ""
	}
	if targetNormalized == sourceNormalized {
		return 0.99, "normalized exact match"
	}
	targetCanonical := canonicalDocxFieldName(targetNormalized)
	sourceCanonical := canonicalDocxFieldName(sourceNormalized)
	if targetCanonical != "" && targetCanonical == sourceCanonical {
		return 0.94, "laboratory field alias match"
	}
	shorter := min(len([]rune(targetNormalized)), len([]rune(sourceNormalized)))
	if shorter >= 2 && (strings.Contains(targetNormalized, sourceNormalized) || strings.Contains(sourceNormalized, targetNormalized)) {
		return 0.84, "field name containment"
	}
	similarity := runeLevenshteinSimilarity(targetNormalized, sourceNormalized)
	if similarity >= 0.72 {
		return math.Min(0.87, 0.55+similarity*0.35), "field name similarity"
	}
	return 0, ""
}

func normalizeDocxFieldName(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var normalized strings.Builder
	for _, r := range value {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			normalized.WriteRune(r)
		}
	}
	return strings.TrimSuffix(strings.TrimSuffix(normalized.String(), "字段"), "field")
}

func canonicalDocxFieldName(normalized string) string {
	for canonical, aliases := range docxFieldAliasGroups {
		for _, alias := range aliases {
			if normalized == normalizeDocxFieldName(alias) {
				return canonical
			}
		}
	}
	return ""
}

func runeLevenshteinSimilarity(left, right string) float64 {
	a := []rune(left)
	b := []rune(right)
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	previous := make([]int, len(b)+1)
	for index := range previous {
		previous[index] = index
	}
	for i, leftRune := range a {
		current := make([]int, len(b)+1)
		current[0] = i + 1
		for j, rightRune := range b {
			cost := 0
			if leftRune != rightRune {
				cost = 1
			}
			current[j+1] = min(previous[j+1]+1, current[j]+1, previous[j]+cost)
		}
		previous = current
	}
	distance := previous[len(b)]
	maximum := max(len(a), len(b))
	return 1 - float64(distance)/float64(maximum)
}

func cloneInterfaceMap(source map[string]interface{}) map[string]interface{} {
	result := make(map[string]interface{}, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func providedDocxTargetValue(target docxMappingTarget, templateData, underlineData map[string]interface{}) (interface{}, bool) {
	if target.kind == "underline" {
		return lookupInterfaceMapFold(underlineData, target.key)
	}
	return lookupInterfaceMapFold(templateData, target.key)
}

func setDocxTargetValue(target docxMappingTarget, value interface{}, templateData, underlineData map[string]interface{}) {
	if target.kind == "underline" {
		underlineData[target.key] = value
		return
	}
	templateData[target.key] = value
}

func lookupInterfaceMapFold(values map[string]interface{}, key string) (interface{}, bool) {
	if value, ok := values[key]; ok {
		return value, true
	}
	for candidate, value := range values {
		if strings.EqualFold(strings.TrimSpace(candidate), strings.TrimSpace(key)) {
			return value, true
		}
	}
	return nil, false
}

func lookupExplicitDocxMapping(mapping map[string]string, target string) (string, bool) {
	if path, ok := mapping[target]; ok {
		return strings.TrimSpace(path), true
	}
	for key, path := range mapping {
		if strings.EqualFold(strings.TrimSpace(key), strings.TrimSpace(target)) {
			return strings.TrimSpace(path), true
		}
	}
	return "", false
}

func lookupDocxSourcePath(sources []docxSourceField, requested string) (docxSourceField, bool) {
	requested = strings.TrimSpace(requested)
	for _, source := range sources {
		if source.path == requested || strings.EqualFold(source.path, requested) {
			return source, true
		}
	}
	return docxSourceField{}, false
}

func docxMappingAlternatives(candidates []scoredDocxSource, limit int) []string {
	if limit <= 0 || len(candidates) == 0 {
		return nil
	}
	if len(candidates) < limit {
		limit = len(candidates)
	}
	result := make([]string, 0, limit)
	for _, candidate := range candidates[:limit] {
		result = append(result, fmt.Sprintf("%s (%.2f)", candidate.field.path, roundDocxConfidence(candidate.confidence)))
	}
	return result
}

func roundDocxConfidence(value float64) float64 {
	return math.Round(value*100) / 100
}

func stringifyFieldMapping(values map[string]interface{}) map[string]string {
	result := make(map[string]string, len(values))
	for target, source := range values {
		target = strings.TrimSpace(target)
		if target != "" {
			result[target] = strings.TrimSpace(fmt.Sprint(source))
		}
	}
	return result
}

func encodeDocxAutomationPreview(inspection docxTemplateInspection, report docxDataMappingReport) (string, error) {
	encoded, err := json.MarshalIndent(docxAutomationPreview{Inspection: inspection, Mapping: report}, "", "  ")
	if err != nil {
		return "", fmt.Errorf("encode DOCX automation preview: %w", err)
	}
	return string(encoded), nil
}

func unresolvedDocxMappings(report docxDataMappingReport) []string {
	unresolved := append([]string{}, report.UnmatchedFields...)
	unresolved = append(unresolved, report.AmbiguousFields...)
	sort.Strings(unresolved)
	return uniqueStringValues(unresolved)
}

func countMappedDocxFields(report docxDataMappingReport) int {
	count := 0
	for _, field := range report.Fields {
		if field.Status == "mapped" {
			count++
		}
	}
	return count
}
