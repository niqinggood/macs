package workflowlearn

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"
)

var (
	selectorName = regexp.MustCompile(`(?i)(?:#|\[name=["']?|\[id=["']?)([a-z][a-z0-9_-]*)`)
)

func Compile(req CompileRequest) (Recipe, error) {
	name := NormalizeName(req.Name)
	if !validNormalizedRecipeName(name) {
		return Recipe{}, fmt.Errorf("workflow name must contain 2-63 letters, numbers, dashes, or underscores")
	}
	surface := strings.ToLower(strings.TrimSpace(req.Trace.Surface))
	if surface != "browser" && surface != "desktop" {
		return Recipe{}, fmt.Errorf("trace surface must be browser or desktop")
	}
	now := req.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	recipe := Recipe{
		Version:     RecipeVersion,
		Revision:    1,
		Name:        name,
		Description: strings.TrimSpace(req.Description),
		Objective:   strings.TrimSpace(req.Trace.Objective),
		Surface:     surface,
		StartURL:    strings.TrimSpace(req.Trace.URL),
		SourceTrace: strings.TrimSpace(req.Trace.ID),
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	hints := normalizedHints(req.Variables)
	inputNames := make(map[string]int)
	matchedHints := make(map[string]bool)
	bindings := make(map[string]string)
	for _, hint := range hints {
		bindings[hint.Value] = hint.Name
	}
	for index, step := range req.Trace.Steps {
		operation := strings.ToLower(strings.TrimSpace(step.Operation))
		if operation == "" || operation == "observe" || operation == "verify" || operation == "status" || operation == "capabilities" {
			continue
		}
		if step.Status != "" && !strings.EqualFold(step.Status, "completed") && !strings.EqualFold(step.Status, "success") {
			recipe.Warnings = append(recipe.Warnings, fmt.Sprintf("omitted failed demonstration step %d (%s)", step.Number, operation))
			continue
		}
		arguments := cloneMap(step.Arguments)
		for _, hint := range hints {
			if replaceLiteralValue(arguments, hint.Value, inputPlaceholder(hint.Name)) > 0 {
				matchedHints[hint.Name] = true
				addInput(&recipe, inputNames, InputDefinition{
					Name: hint.Name, Type: "string", Description: hint.Description,
					Required: hint.Required, Source: fmt.Sprintf("step_%d", index+1),
				})
			}
		}
		autoParameterize(&recipe, inputNames, bindings, operation, arguments, index+1)
		if usesUnstableIndex(arguments) {
			recipe.Warnings = append(recipe.Warnings, fmt.Sprintf("step_%d uses a DOM index; record a stable selector when possible", index+1))
		}
		if surface == "desktop" && usesCoordinates(arguments) {
			recipe.Warnings = append(recipe.Warnings, fmt.Sprintf("step_%d uses screen coordinates; replay should re-observe after layout changes", index+1))
		}
		recipe.Steps = append(recipe.Steps, RecipeStep{
			ID:        fmt.Sprintf("step_%d", len(recipe.Steps)+1),
			Operation: operation,
			Arguments: arguments,
		})
	}
	if len(recipe.Steps) == 0 {
		return Recipe{}, fmt.Errorf("demonstration contains no successful replayable steps")
	}
	for literal, inputName := range bindings {
		placeholder := inputPlaceholder(inputName)
		matches := replaceStringLiteral(&recipe.Description, literal, placeholder)
		matches += replaceStringLiteral(&recipe.Objective, literal, placeholder)
		matches += replaceStringLiteral(&recipe.StartURL, literal, placeholder)
		if matches > 0 {
			matchedHints[inputName] = true
		}
	}
	for _, hint := range hints {
		if matchedHints[hint.Name] {
			addInput(&recipe, inputNames, InputDefinition{
				Name: hint.Name, Type: "string", Description: hint.Description,
				Required: hint.Required, Source: "workflow_metadata",
			})
		}
		if !matchedHints[hint.Name] {
			recipe.Warnings = append(recipe.Warnings, fmt.Sprintf("variable hint %q did not match a demonstrated value", hint.Name))
		}
	}
	for _, assertion := range append(traceAssertions(req.Trace.Assertions), req.Assertions...) {
		for literal, inputName := range bindings {
			replaceStringLiteral(&assertion.Expected, literal, inputPlaceholder(inputName))
		}
		addAssertion(&recipe, assertion)
	}
	recipe.Warnings = uniqueStrings(recipe.Warnings)
	return recipe, nil
}

func NormalizeName(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	previousDash := false
	for _, r := range value {
		switch {
		case unicode.IsLetter(r), unicode.IsDigit(r):
			b.WriteRune(r)
			previousDash = false
		case r == '-' || r == '_' || unicode.IsSpace(r):
			if b.Len() > 0 && !previousDash {
				b.WriteByte('-')
				previousDash = true
			}
		}
	}
	return strings.Trim(b.String(), "-_")
}

func validNormalizedRecipeName(value string) bool {
	runes := []rune(value)
	if len(runes) < 2 || len(runes) > 63 {
		return false
	}
	for _, r := range runes {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '-' && r != '_' {
			return false
		}
	}
	return true
}

func normalizedHints(values []VariableHint) []VariableHint {
	result := make([]VariableHint, 0, len(values))
	seen := make(map[string]bool)
	for _, value := range values {
		value.Name = normalizeInputName(value.Name)
		value.Value = strings.TrimSpace(value.Value)
		if value.Name == "" || value.Value == "" || seen[value.Name] {
			continue
		}
		seen[value.Name] = true
		result = append(result, value)
	}
	return result
}

func autoParameterize(recipe *Recipe, names map[string]int, bindings map[string]string, operation string, arguments map[string]interface{}, step int) {
	fields := []string{}
	switch operation {
	case "input", "type", "select":
		fields = []string{"text", "value"}
	case "upload", "open":
		fields = []string{"files", "target_path"}
	}
	for _, field := range fields {
		value, exists := arguments[field]
		if !exists {
			continue
		}
		if text, ok := value.(string); ok {
			if text == "" || strings.HasPrefix(text, "${inputs.") {
				continue
			}
			if existingName, exists := bindings[text]; exists {
				arguments[field] = inputPlaceholder(existingName)
				continue
			}
			name := inferredInputName(arguments, operation, field, step)
			name = uniqueInputName(name, names)
			arguments[field] = inputPlaceholder(name)
			bindings[text] = name
			addInput(recipe, names, InputDefinition{Name: name, Type: "string", Required: true, Source: fmt.Sprintf("step_%d.%s", step, field)})
			continue
		}
		if field == "files" {
			items, ok := stringItems(value)
			if ok && len(items) > 0 {
				name := uniqueInputName(fmt.Sprintf("file_path_%d", step), names)
				arguments[field] = []interface{}{inputPlaceholder(name)}
				bindings[items[0]] = name
				addInput(recipe, names, InputDefinition{Name: name, Type: "string", Required: true, Source: fmt.Sprintf("step_%d.files", step)})
			}
		}
	}
}

func stringItems(value interface{}) ([]string, bool) {
	switch typed := value.(type) {
	case []string:
		return typed, true
	case []interface{}:
		items := make([]string, 0, len(typed))
		for _, item := range typed {
			text, ok := item.(string)
			if !ok {
				return nil, false
			}
			items = append(items, text)
		}
		return items, true
	default:
		return nil, false
	}
}

func inferredInputName(arguments map[string]interface{}, operation, field string, step int) string {
	if selector, _ := arguments["selector"].(string); selector != "" {
		if match := selectorName.FindStringSubmatch(selector); len(match) == 2 {
			return normalizeInputName(match[1])
		}
	}
	if operation == "upload" || field == "files" || field == "target_path" {
		return fmt.Sprintf("file_path_%d", step)
	}
	if index, ok := numericIndex(arguments["index"]); ok {
		return fmt.Sprintf("%s_%d", operation, index)
	}
	return fmt.Sprintf("%s_%d", field, step)
}

func normalizeInputName(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	previousUnderscore := false
	for _, r := range value {
		if unicode.IsLetter(r) && r <= unicode.MaxASCII || unicode.IsDigit(r) {
			b.WriteRune(r)
			previousUnderscore = false
		} else if b.Len() > 0 && !previousUnderscore {
			b.WriteByte('_')
			previousUnderscore = true
		}
	}
	return strings.Trim(b.String(), "_")
}

func uniqueInputName(base string, names map[string]int) string {
	base = normalizeInputName(base)
	if base == "" {
		base = "value"
	}
	if _, exists := names[base]; !exists {
		return base
	}
	names[base]++
	return fmt.Sprintf("%s_%d", base, names[base])
}

func addInput(recipe *Recipe, names map[string]int, input InputDefinition) {
	for index := range recipe.Inputs {
		if recipe.Inputs[index].Name == input.Name {
			if input.Description != "" {
				recipe.Inputs[index].Description = input.Description
			}
			if input.Required {
				recipe.Inputs[index].Required = true
			}
			return
		}
	}
	names[input.Name] = 1
	recipe.Inputs = append(recipe.Inputs, input)
}

func traceAssertions(values []TraceAssertion) []Assertion {
	result := make([]Assertion, 0, len(values))
	for _, value := range values {
		if value.Status == "" || strings.EqualFold(value.Status, "completed") || strings.EqualFold(value.Status, "success") {
			result = append(result, Assertion{Kind: value.Kind, Expected: value.Expected})
		}
	}
	return result
}

func addAssertion(recipe *Recipe, assertion Assertion) {
	assertion.Kind = strings.ToLower(strings.TrimSpace(assertion.Kind))
	assertion.Expected = strings.TrimSpace(assertion.Expected)
	if assertion.Kind == "" || assertion.Expected == "" {
		return
	}
	for _, existing := range recipe.Assertions {
		if existing.Kind == assertion.Kind && existing.Expected == assertion.Expected {
			return
		}
	}
	recipe.Assertions = append(recipe.Assertions, assertion)
}

func replaceLiteralValue(value interface{}, wanted, replacement string) int {
	matched := 0
	var visit func(interface{}) interface{}
	visit = func(current interface{}) interface{} {
		switch typed := current.(type) {
		case string:
			matched += replaceStringLiteral(&typed, wanted, replacement)
			return typed
		case []interface{}:
			for index := range typed {
				typed[index] = visit(typed[index])
			}
			return typed
		case map[string]interface{}:
			for key := range typed {
				typed[key] = visit(typed[key])
			}
			return typed
		default:
			return current
		}
	}
	if object, ok := value.(map[string]interface{}); ok {
		visit(object)
	}
	return matched
}

func replaceStringLiteral(value *string, wanted, replacement string) int {
	if value == nil || wanted == "" || *value == "" {
		return 0
	}
	if *value == wanted {
		*value = replacement
		return 1
	}
	if len([]rune(wanted)) < 3 || !strings.Contains(*value, wanted) {
		return 0
	}
	count := strings.Count(*value, wanted)
	*value = strings.ReplaceAll(*value, wanted, replacement)
	return count
}

func inputPlaceholder(name string) string { return "${inputs." + name + "}" }

func usesUnstableIndex(arguments map[string]interface{}) bool {
	_, hasIndex := numericIndex(arguments["index"])
	selector, _ := arguments["selector"].(string)
	return hasIndex && strings.TrimSpace(selector) == ""
}

func numericIndex(value interface{}) (int, bool) {
	switch typed := value.(type) {
	case int:
		return typed, true
	case float64:
		return int(typed), true
	default:
		return 0, false
	}
}

func usesCoordinates(arguments map[string]interface{}) bool {
	_, x := arguments["x"]
	_, y := arguments["y"]
	return x || y
}

func cloneMap(value map[string]interface{}) map[string]interface{} {
	if value == nil {
		return nil
	}
	copy := make(map[string]interface{}, len(value))
	for key, item := range value {
		copy[key] = cloneValue(item)
	}
	return copy
}

func cloneValue(value interface{}) interface{} {
	switch typed := value.(type) {
	case map[string]interface{}:
		return cloneMap(typed)
	case []interface{}:
		copy := make([]interface{}, len(typed))
		for index := range typed {
			copy[index] = cloneValue(typed[index])
		}
		return copy
	default:
		return typed
	}
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]bool)
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" && !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	sort.Strings(result)
	return result
}
