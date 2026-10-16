package workflowlearn

import (
	"fmt"
	"math"
	"strings"
	"time"
)

func exactInputPlaceholder(value string) (string, bool) {
	const prefix = "${inputs."
	const suffix = "}"
	if strings.HasPrefix(value, prefix) && strings.HasSuffix(value, suffix) {
		name := strings.TrimSuffix(strings.TrimPrefix(value, prefix), suffix)
		if name != "" && !strings.Contains(name, "${") {
			return name, true
		}
	}
	return "", false
}

// ResolveSteps substitutes supplied inputs while preserving non-string JSON
// values when a placeholder occupies the whole argument.
func ResolveSteps(recipe Recipe, inputs map[string]interface{}) ([]RecipeStep, error) {
	missing := make([]string, 0)
	for _, input := range recipe.Inputs {
		value, exists := inputs[input.Name]
		if input.Required && (!exists || value == nil || strings.TrimSpace(fmt.Sprint(value)) == "") {
			missing = append(missing, input.Name)
		}
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("missing required workflow inputs: %s", strings.Join(missing, ", "))
	}
	steps := make([]RecipeStep, len(recipe.Steps))
	for index, step := range recipe.Steps {
		steps[index] = step
		steps[index].Arguments = resolveMap(step.Arguments, inputs)
	}
	return steps, nil
}

// ResolveRecipe returns an execution copy with inputs substituted in steps,
// start URL, objective, description, and success assertions.
func ResolveRecipe(recipe Recipe, inputs map[string]interface{}) (Recipe, error) {
	steps, err := ResolveSteps(recipe, inputs)
	if err != nil {
		return Recipe{}, err
	}
	resolved := recipe
	resolved.Steps = steps
	resolved.StartURL = resolveText(recipe.StartURL, inputs)
	resolved.Objective = resolveText(recipe.Objective, inputs)
	resolved.Description = resolveText(recipe.Description, inputs)
	resolved.Assertions = append([]Assertion(nil), recipe.Assertions...)
	for index := range resolved.Assertions {
		resolved.Assertions[index].Expected = resolveText(resolved.Assertions[index].Expected, inputs)
	}
	return resolved, nil
}

func resolveText(value string, inputs map[string]interface{}) string {
	resolved, _ := resolveValue(value, inputs).(string)
	return resolved
}

func resolveMap(arguments map[string]interface{}, inputs map[string]interface{}) map[string]interface{} {
	result := make(map[string]interface{}, len(arguments))
	for key, value := range arguments {
		result[key] = resolveValue(value, inputs)
	}
	return result
}

func resolveValue(value interface{}, inputs map[string]interface{}) interface{} {
	switch typed := value.(type) {
	case string:
		if name, exact := exactInputPlaceholder(typed); exact {
			if replacement, ok := inputs[name]; ok {
				return replacement
			}
			return typed
		}
		for name, replacement := range inputs {
			typed = strings.ReplaceAll(typed, inputPlaceholder(name), fmt.Sprint(replacement))
		}
		return typed
	case []interface{}:
		result := make([]interface{}, len(typed))
		for index := range typed {
			result[index] = resolveValue(typed[index], inputs)
		}
		return result
	case map[string]interface{}:
		return resolveMap(typed, inputs)
	default:
		return typed
	}
}

func Evaluate(recipe Recipe, trace Trace, caseName string, now time.Time) EvaluationReport {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	report := EvaluationReport{
		RecipeName:  recipe.Name,
		CaseName:    strings.TrimSpace(caseName),
		TraceID:     trace.ID,
		EvaluatedAt: now,
	}
	actual := replayableTraceSteps(trace.Steps)
	passedSteps := 0
	for index, expected := range recipe.Steps {
		result := StepEvaluation{StepID: expected.ID, Operation: expected.Operation}
		switch {
		case index >= len(actual):
			result.Reason = "operation was not executed"
		case !strings.EqualFold(actual[index].Operation, expected.Operation):
			result.Reason = fmt.Sprintf("got operation %s", actual[index].Operation)
		case actual[index].Status != "" && !strings.EqualFold(actual[index].Status, "completed") && !strings.EqualFold(actual[index].Status, "success"):
			result.Reason = strings.TrimSpace(actual[index].Error)
			if result.Reason == "" {
				result.Reason = "operation failed"
			}
		default:
			result.Passed = true
			passedSteps++
		}
		report.Steps = append(report.Steps, result)
	}
	if len(recipe.Steps) > 0 {
		report.StepPassRate = float64(passedSteps) / float64(len(recipe.Steps))
	}

	passedAssertions := 0
	for _, expected := range recipe.Assertions {
		result := AssertionEvaluation{Kind: expected.Kind, Expected: expected.Expected}
		if assertionPassed(expected, trace) {
			result.Passed = true
			passedAssertions++
		} else {
			result.Reason = "expected evidence was not observed"
		}
		report.Assertions = append(report.Assertions, result)
	}
	if len(recipe.Assertions) > 0 {
		report.AssertionPassRate = float64(passedAssertions) / float64(len(recipe.Assertions))
		report.Score = 75*report.StepPassRate + 25*report.AssertionPassRate
	} else {
		report.AssertionPassRate = 1
		report.Score = 100 * report.StepPassRate
	}
	report.Score = math.Round(report.Score*10) / 10
	report.Passed = report.Score >= 90 && passedSteps == len(recipe.Steps) && passedAssertions == len(recipe.Assertions)
	return report
}

func replayableTraceSteps(steps []TraceStep) []TraceStep {
	result := make([]TraceStep, 0, len(steps))
	for _, step := range steps {
		switch strings.ToLower(strings.TrimSpace(step.Operation)) {
		case "", "observe", "verify", "status", "capabilities":
			continue
		default:
			result = append(result, step)
		}
	}
	return result
}

func assertionPassed(expected Assertion, trace Trace) bool {
	for _, actual := range trace.Assertions {
		if strings.EqualFold(actual.Kind, expected.Kind) && actual.Expected == expected.Expected &&
			(actual.Status == "" || strings.EqualFold(actual.Status, "completed") || strings.EqualFold(actual.Status, "success")) {
			return true
		}
	}
	if expected.Expected == "" {
		return false
	}
	switch strings.ToLower(expected.Kind) {
	case "visible_text", "expect_text", "text":
		return strings.Contains(strings.ToLower(trace.LastObservation), strings.ToLower(expected.Expected))
	case "active_window", "expect_window", "window":
		return strings.Contains(strings.ToLower(trace.LastObservation), strings.ToLower(expected.Expected))
	default:
		return false
	}
}
