package binary

import (
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"mac/internal/projectctx"
	"mac/internal/routing"
	"mac/internal/toolcontract"
	contenttools "mac/internal/tools/content"
)

var (
	binaryArtifactDeliverySignal = regexp.MustCompile(`(?i)(?:最终|最后|输出|交付|生成|制作|创建|导出|保存|产出|提供|给我|帮我做|请做|deliver|create|generate|produce|export|save|output|provide|build)`)
	docxArtifactSignal           = regexp.MustCompile(`(?i)(?:\.docx\b|\bword\b|word\s*文档|微软文档)`)
	xlsxArtifactSignal           = regexp.MustCompile(`(?i)(?:\.xlsx\b|\bexcel\b|电子表格|工作簿)`)
)

func MentionedFormats(input string) []string {
	text := input
	if text == "" || !binaryArtifactDeliverySignal.MatchString(text) {
		return nil
	}
	formats := make([]string, 0, 2)
	if docxArtifactSignal.MatchString(text) {
		formats = append(formats, "docx")
	}
	if xlsxArtifactSignal.MatchString(text) {
		formats = append(formats, "xlsx")
	}
	return formats
}

func Prompt(formats []string) string {
	if len(formats) == 0 {
		return ""
	}
	sort.Strings(formats)
	var steps []string
	if artifactFormatContains(formats, "docx") {
		steps = append(steps, "- DOCX: keep the substantial prose in a bounded Markdown source, then call convert with that input path and a .docx output path. Do not inline a document generator.")
	}
	if artifactFormatContains(formats, "xlsx") {
		steps = append(steps, "- XLSX: put the decision tables in the same bounded Markdown source and call convert with a .xlsx output path, or call write_file on a .xlsx path with compact comma-delimited rows. Both routes create a real OOXML workbook; never rename CSV bytes to .xlsx.")
	}
	return "## Current-turn Binary Artifact Contract\n" +
		"The user authorized real " + strings.ToUpper(strings.Join(formats, " + ")) + " deliverables. Treat this as file production, not a request to print the full document or a large generator script in a model response.\n" +
		strings.Join(steps, "\n") + "\n" +
		"Do not run project_map for a standalone business/document deliverable. If live retrieval is unavailable and the user allowed assumptions, proceed with labelled assumptions instead of spending the artifact budget on repeated searches. Before claiming completion, read/extract each exact binary path and confirm it is non-empty and parseable."
}

func artifactFormatContains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func binaryArtifactDeliveredFormats(results []string) map[string]bool {
	delivered := make(map[string]bool)
	for _, result := range results {
		record, ok := toolcontract.ParseRecord(result)
		if !ok || !record.Succeeded {
			continue
		}
		switch strings.ToLower(filepath.Ext(strings.TrimSpace(record.ArtifactPath))) {
		case ".docx":
			delivered["docx"] = true
		case ".xlsx":
			delivered["xlsx"] = true
		}
	}
	return delivered
}

func latestSuccessfulMarkdownArtifact(results []string) string {
	for index := len(results) - 1; index >= 0; index-- {
		record, ok := toolcontract.ParseRecord(results[index])
		if !ok || !record.Succeeded || !strings.EqualFold(filepath.Ext(record.ArtifactPath), ".md") {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(record.Tool)) {
		case "write_file", "edit_file":
			return strings.TrimSpace(record.ArtifactPath)
		}
	}
	return ""
}

// ensureBinaryArtifactConversionCalls closes a deterministic delivery gap:
// after the agent has written one bounded Markdown source, the host can append
// the exact convert calls that the user already authorized. This avoids asking
// the model to emit a large one-off document generator and keeps the conversion
// on the same sandboxed, receipt-producing tool path as any model-issued call.
func EnsureConversionCalls(formats []string, results []string, proposed []toolcontract.Call, iteration int) ([]toolcontract.Call, int) {
	if len(formats) == 0 {
		return proposed, 0
	}
	source := latestSuccessfulMarkdownArtifact(results)
	if source == "" {
		return proposed, 0
	}

	delivered := binaryArtifactDeliveredFormats(results)
	for _, call := range proposed {
		if !strings.EqualFold(strings.TrimSpace(call.Name), "convert") &&
			!strings.EqualFold(strings.TrimSpace(call.Name), "write_file") {
			continue
		}
		ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(routing.ToolArtifactPath(call))), ".")
		if ext == "docx" || ext == "xlsx" {
			delivered[ext] = true
		}
	}

	base := strings.TrimSuffix(source, filepath.Ext(source))
	added := 0
	for _, format := range formats {
		if delivered[format] {
			continue
		}
		proposed = append(proposed, toolcontract.Call{
			ID:   fmt.Sprintf("host_binary_%d_%s", iteration, format),
			Name: "convert",
			Arguments: map[string]interface{}{
				"input":  source,
				"output": base + "." + format,
			},
		})
		delivered[format] = true
		added++
	}
	return proposed, added
}

func DeliveryIssues(formats []string, results []string, workspace string) []string {
	if len(formats) == 0 {
		return nil
	}
	delivered := binaryArtifactDeliveredFormats(results)
	var issues []string
	for _, format := range formats {
		if !delivered[format] {
			issues = append(issues, "missing successful ."+format+" artifact receipt")
		}
	}
	issues = append(issues, receiptValidationIssues(formats, results, workspace)...)
	sort.Strings(issues)
	return issues
}

func receiptValidationIssues(formats []string, results []string, workspace string) []string {
	workspace = strings.TrimSpace(workspace)
	if workspace == "" {
		return nil
	}
	required := make(map[string]bool, len(formats))
	for _, format := range formats {
		required[strings.ToLower(strings.TrimSpace(format))] = true
	}
	seen := make(map[string]bool)
	var issues []string
	for _, result := range results {
		record, ok := toolcontract.ParseRecord(result)
		if !ok || !record.Succeeded || strings.TrimSpace(record.ArtifactPath) == "" {
			continue
		}
		extension := strings.TrimPrefix(strings.ToLower(filepath.Ext(record.ArtifactPath)), ".")
		if !required[extension] || !isOfficeOpenXMLPath(record.ArtifactPath) {
			continue
		}
		path := strings.Trim(strings.TrimSpace(record.ArtifactPath), `"`)
		if !filepath.IsAbs(path) {
			path = filepath.Join(workspace, filepath.FromSlash(strings.ReplaceAll(path, "\\", "/")))
		}
		path = filepath.Clean(path)
		key := projectctx.IdentityKey(path)
		if seen[key] {
			continue
		}
		seen[key] = true
		if err := contenttools.ValidateOfficeOpenXMLPackage(path); err != nil {
			label := filepath.ToSlash(record.ArtifactPath)
			issues = append(issues, fmt.Sprintf("invalid .%s artifact %s: %v", extension, label, err))
		}
	}
	return issues
}

func RepairPromptContent(issues []string) string {
	return fmt.Sprintf(`[BINARY ARTIFACT DELIVERY REQUIRED]
The current user turn requires real binary deliverables, but host receipts still show: %s.

If a bounded Markdown source already exists, call convert once for each missing extension. Otherwise write that bounded source first, then convert it. Do not emit a document generator, inspect tool schemas, restart research, or merely claim that a file exists. A conversion is complete only when the requested output file is actually created.`, strings.Join(issues, "; "))
}

func AppendDeliveryFailure(output string, issues []string) string {
	message := "Binary artifact delivery was not completed: " + strings.Join(issues, "; ") + "."
	if strings.TrimSpace(output) == "" {
		return message
	}
	return strings.TrimSpace(output) + "\n\n## Delivery verification failed\n" + message
}

func isOfficeOpenXMLPath(path string) bool {
	switch strings.ToLower(filepath.Ext(strings.TrimSpace(path))) {
	case ".docx", ".xlsx", ".pptx":
		return true
	default:
		return false
	}
}
