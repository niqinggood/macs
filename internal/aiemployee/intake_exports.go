package aiemployee

import "context"

const (
	IntakeStepName    = aiEmployeeIntakeStepName
	ContextPathMarker = aiEmployeeContextPathMarker
	ContextHeader     = aiEmployeeContextHeader
)

func WithIntakeSpec(ctx context.Context, spec IntakeSpec) context.Context {
	return withAIEmployeeIntakeSpec(ctx, spec)
}

func IntakeSpecFromContext(ctx context.Context) (IntakeSpec, bool) {
	return aiEmployeeIntakeSpecFromContext(ctx)
}

func PrependIntakeStep(pipeline Pipeline, spec IntakeSpec) Pipeline {
	return prependAIEmployeeIntakeStep(pipeline, spec)
}

func ToolListed(tools []string, want string) bool {
	return aiEmployeeToolListed(tools, want)
}

func BuildIntakePrompt(pipeline Pipeline, spec IntakeSpec) string {
	return buildAIEmployeeIntakePrompt(pipeline, spec)
}

func BuildDomainIntakeRule(pipeline Pipeline) string {
	return buildAIEmployeeDomainIntakeRule(pipeline)
}

func AppendClarificationProtocol(prompt string, pipeline Pipeline, step *PipelineStep) string {
	return appendAIEmployeeClarificationProtocol(prompt, pipeline, step)
}

func NormalizeStepWaitingInputOutput(output string, pipeline Pipeline, step *PipelineStep) (string, bool) {
	return normalizeAIEmployeeStepWaitingInputOutput(output, pipeline, step)
}

func NormalizeWaitingInputOutput(output string, pipeline Pipeline) string {
	return normalizeAIEmployeeWaitingInputOutput(output, pipeline)
}

func OutputRequiresFreshInvocation(output string, pipeline Pipeline) bool {
	return aiEmployeeOutputRequiresFreshInvocation(output, pipeline)
}

func NormalizeIntakeDecision(pipeline Pipeline, input, output string) (string, bool) {
	return normalizeAIEmployeeIntakeDecision(pipeline, input, output)
}

func AppendInteractiveHandoffInstruction(prompt string, pipeline Pipeline, step *PipelineStep, stepIndex int) string {
	return appendAIEmployeeInteractiveHandoffInstruction(prompt, pipeline, step, stepIndex)
}

func BuildDeterministicMissingInput(pipeline Pipeline, input string) (string, bool) {
	return buildAIEmployeeDeterministicMissingInput(pipeline, input)
}

func EnsureExplicitConfirmationCoverage(task, output string) string {
	return ensureAIEmployeeExplicitConfirmationCoverage(task, output)
}

func ExtractExplicitLabelRequirements(task string) []string {
	return extractAIEmployeeExplicitLabelRequirements(task)
}

func ExtractExplicitCircledOptions(task string) []string {
	return extractAIEmployeeExplicitCircledOptions(task)
}

func RequestIsSemanticallyEmpty(input string) bool {
	return aiEmployeeRequestIsSemanticallyEmpty(input)
}

func PPTRequestHasActionableInput(input string) bool {
	return aiEmployeePPTRequestHasActionableInput(input)
}

func UTF8RuneCount(value string) int {
	return utf8RuneCount(value)
}

func BuildIntakeFailureFallback(pipeline Pipeline, input string) string {
	return buildAIEmployeeIntakeFailureFallback(pipeline, input)
}

func BuildPipelineDeliveryStandards(steps []PipelineStep) string {
	return buildAEPipelineDeliveryStandards(steps)
}
