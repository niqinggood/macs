package aiemployee

import "regexp"

// The exported entry points keep the policy implementation reusable without
// exposing its internal parsing helpers.
func DomainOutputIssues(pipeline *Pipeline, output string) []string {
	return aiEmployeeDomainOutputIssues(pipeline, output)
}

func EnsureFundraisingTrailingDescriptionCoverage(task, output string) string {
	return ensureFundraisingTrailingDescriptionCoverage(task, output)
}

func EnsureFundraisingRelationshipQuestionCoverage(task, output string) string {
	return ensureFundraisingRelationshipQuestionCoverage(task, output)
}

func StripFundraisingDirectDraftQuestions(task, output string) string {
	return stripFundraisingDirectDraftQuestions(task, output)
}

func BuildDomainOutputRepairPrompt(basePrompt string, issues []string, previous string) string {
	return buildAIEmployeeDomainOutputRepairPrompt(basePrompt, issues, previous)
}

func BuildDomainRepairPrompt(pipeline *Pipeline, basePrompt string, issues []string, previous string) string {
	return buildAIEmployeeDomainRepairPrompt(pipeline, basePrompt, issues, previous)
}

func StripUnsupportedFundraisingBenchmarkLines(output string, originalTask ...string) string {
	return stripUnsupportedFundraisingBenchmarkLines(output, originalTask...)
}

func RemoveEmptyMarkdownSections(markdown string) string {
	return removeEmptyMarkdownSections(markdown)
}

func FundraisingNegativeArtifactDirectivePattern() *regexp.Regexp {
	return fundraisingNegativeArtifactDirectiveRe
}

func MarkdownOrderedListItemPattern() *regexp.Regexp {
	return markdownOrderedListItemRe
}

func FundraisingBPOnlyRequest(task string) bool {
	return fundraisingBPOnlyRequest(task)
}
