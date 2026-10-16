package pipeline

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"mac/internal/textutil"
)

const (
	OutputViolationFail     = "fail"
	OutputViolationDegraded = "degraded"
)

// PipelineOutputContract is a deterministic acceptance gate for a model step.
// It deliberately checks only properties the host can verify without another
// model call. An omitted on_violation defaults to fail.
type PipelineOutputContract struct {
	MinChars            int      `json:"min_chars,omitempty" yaml:"min_chars,omitempty"`
	RequiredSections    []string `json:"required_sections,omitempty" yaml:"required_sections,omitempty"`
	ReportedStatusField string   `json:"reported_status_field,omitempty" yaml:"reported_status_field,omitempty"`
	OnViolation         string   `json:"on_violation,omitempty" yaml:"on_violation,omitempty"` // fail | degraded
}

// PipelineOutputContractViolation records every deterministic gap found in one
// output. It never embeds the model output itself, so it is safe for logs and
// downstream status metadata.
type PipelineOutputContractViolation struct {
	MinChars            int
	ActualChars         int
	MissingSections     []string
	ReportedStatusIssue string
}

func (v *PipelineOutputContractViolation) Error() string {
	if v == nil {
		return ""
	}
	parts := make([]string, 0, 2)
	if v.MinChars > 0 && v.ActualChars < v.MinChars {
		parts = append(parts, fmt.Sprintf("min_chars not met (got %d, require %d)", v.ActualChars, v.MinChars))
	}
	if len(v.MissingSections) > 0 {
		parts = append(parts, "missing required_sections: "+strings.Join(v.MissingSections, ", "))
	}
	if v.ReportedStatusIssue != "" {
		parts = append(parts, v.ReportedStatusIssue)
	}
	if len(parts) == 0 {
		return "output contract violation"
	}
	return "output contract violation: " + strings.Join(parts, "; ")
}

func OutputContractAction(contract *PipelineOutputContract) string {
	if contract == nil {
		return ""
	}
	action := strings.ToLower(strings.TrimSpace(contract.OnViolation))
	if action == "" {
		return OutputViolationFail
	}
	return action
}

// ValidateOutputContractConfig validates one output contract declaration.
// Traversal of application-specific step trees remains in the application layer.
func ValidateOutputContractConfig(contract *PipelineOutputContract, path string) error {
	if contract == nil {
		return nil
	}
	if contract.MinChars < 0 {
		return fmt.Errorf("%s output_contract min_chars must be non-negative", path)
	}
	if contract.MinChars == 0 && len(contract.RequiredSections) == 0 && strings.TrimSpace(contract.ReportedStatusField) == "" {
		return fmt.Errorf("%s output_contract must define min_chars, required_sections, or reported_status_field", path)
	}
	action := OutputContractAction(contract)
	if action != OutputViolationFail && action != OutputViolationDegraded {
		return fmt.Errorf("%s output_contract on_violation must be fail or degraded", path)
	}
	seen := make(map[string]struct{}, len(contract.RequiredSections))
	for index, section := range contract.RequiredSections {
		normalized := NormalizeOutputSection(section)
		if normalized == "" {
			return fmt.Errorf("%s output_contract required_sections[%d] must not be empty", path, index)
		}
		if _, exists := seen[normalized]; exists {
			return fmt.Errorf("%s output_contract required section %q is duplicated", path, strings.TrimSpace(section))
		}
		seen[normalized] = struct{}{}
	}
	if contract.ReportedStatusField != "" && NormalizeOutputSection(contract.ReportedStatusField) == "" {
		return fmt.Errorf("%s output_contract reported_status_field must not be empty", path)
	}
	return nil
}

// validatePipelineStepOutput checks a generated output without mutating it.
// Character counts use Unicode code points rather than UTF-8 bytes.
func ValidateStepOutput(contract *PipelineOutputContract, output string) *PipelineOutputContractViolation {
	if contract == nil {
		return nil
	}
	trimmed := strings.TrimSpace(output)
	actualChars := utf8.RuneCountInString(trimmed)
	violation := &PipelineOutputContractViolation{ActualChars: actualChars}
	if contract.MinChars > 0 && actualChars < contract.MinChars {
		violation.MinChars = contract.MinChars
	}
	for _, section := range contract.RequiredSections {
		if !OutputContainsSection(trimmed, section) {
			violation.MissingSections = append(violation.MissingSections, strings.TrimSpace(section))
		}
	}
	if field := strings.TrimSpace(contract.ReportedStatusField); field != "" {
		_, issue := ParseReportedStatus(output, field)
		violation.ReportedStatusIssue = issue
	}
	if violation.MinChars == 0 && len(violation.MissingSections) == 0 && violation.ReportedStatusIssue == "" {
		return nil
	}
	return violation
}

// applyPipelineOutputContract applies the configured policy after generation.
// Degraded policy preserves the original output and appends an explicit,
// machine-independent quality gate disclosure.
func ApplyOutputContract(contract *PipelineOutputContract, output string) (string, bool, error) {
	violation := ValidateStepOutput(contract, output)
	if violation != nil {
		if OutputContractAction(contract) == OutputViolationDegraded {
			return AppendOutputContractDegradation(output, violation), true, nil
		}
		return output, false, violation
	}
	if contract != nil && strings.TrimSpace(contract.ReportedStatusField) != "" {
		status, _ := ParseReportedStatus(output, contract.ReportedStatusField)
		if status == "degraded" || status == "no_decision" {
			return output, true, nil
		}
	}
	return output, false, nil
}

func OutputContractDegradedReason(contract *PipelineOutputContract, output string) string {
	parts := make([]string, 0, 2)
	if contract != nil && strings.TrimSpace(contract.ReportedStatusField) != "" {
		status, issue := ParseReportedStatus(output, contract.ReportedStatusField)
		if issue == "" && (status == "degraded" || status == "no_decision") {
			parts = append(parts, "reported_status_"+status)
		}
	}
	if violation := ValidateStepOutput(contract, output); violation != nil {
		parts = append(parts, violation.Error())
	}
	return strings.Join(parts, "; ")
}

func AppendOutputContractDegradation(output string, violation *PipelineOutputContractViolation) string {
	var sb strings.Builder
	if original := strings.TrimSpace(output); original != "" {
		sb.WriteString(original)
		sb.WriteString("\n\n---\n\n")
	}
	sb.WriteString("## OpenKitty 输出质量门（DEGRADED）\n\n")
	sb.WriteString("- 状态：degraded\n")
	if violation.MinChars > 0 && violation.ActualChars < violation.MinChars {
		fmt.Fprintf(&sb, "- 缺口：正文字符数 %d，低于契约要求 %d。\n", violation.ActualChars, violation.MinChars)
	}
	for _, section := range violation.MissingSections {
		fmt.Fprintf(&sb, "- 缺口：缺少必需章节 `%s`。\n", section)
	}
	if violation.ReportedStatusIssue != "" {
		fmt.Fprintf(&sb, "- 缺口：%s。\n", violation.ReportedStatusIssue)
	}
	sb.WriteString("- 下游规则：保留现有内容，但必须披露上述缺口，不得将其视为完整验收结果。")
	return strings.TrimSpace(sb.String())
}

func OutputContainsSection(output, required string) bool {
	want := NormalizeOutputSection(required)
	if want == "" {
		return false
	}
	for _, line := range strings.Split(strings.ReplaceAll(output, "\r\n", "\n"), "\n") {
		if NormalizeOutputSection(line) == want {
			return true
		}
	}
	return false
}

func NormalizeOutputSection(section string) string {
	section = strings.TrimSpace(section)
	section = strings.TrimLeft(section, "#")
	section = strings.TrimSpace(section)
	section = strings.Trim(section, "*_`")
	section = strings.TrimSpace(section)
	section = strings.TrimRight(section, ":：")
	section = strings.TrimSpace(section)
	return strings.ToLower(strings.Join(strings.Fields(section), " "))
}

func ParseReportedStatus(output, configuredField string) (string, string) {
	want := NormalizeOutputSection(configuredField)
	if want == "" {
		return "", "reported_status_field is empty"
	}
	var values []string
	for _, rawLine := range strings.Split(strings.ReplaceAll(output, "\r\n", "\n"), "\n") {
		line := strings.TrimSpace(rawLine)
		colon, separatorLength := strings.Index(line, ":"), 1
		fullWidthColon := strings.Index(line, "：")
		if colon < 0 || (fullWidthColon >= 0 && fullWidthColon < colon) {
			colon = fullWidthColon
			separatorLength = len("：")
		}
		if colon < 0 || NormalizeOutputSection(line[:colon]) != want {
			continue
		}
		values = append(values, strings.TrimSpace(line[colon+separatorLength:]))
	}
	field := strings.TrimSpace(configuredField)
	if len(values) == 0 {
		return "", fmt.Sprintf("reported status field %q is missing", field)
	}
	if len(values) > 1 {
		return "", fmt.Sprintf("reported status field %q appears more than once", field)
	}
	status := strings.ToLower(strings.TrimSpace(values[0]))
	status = strings.TrimSpace(strings.Trim(status, "*_`"))
	switch status {
	case "completed", "degraded", "no_decision":
		return status, ""
	default:
		return "", fmt.Sprintf("reported status field %q has invalid value %q (want completed, degraded, or no_decision)", field, textutil.Truncate(status, 80))
	}
}
