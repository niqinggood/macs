package development

import (
	"context"
	"fmt"
	"strings"

	"mac/internal/configvalue"
)

// DebugTool turns runtime errors and stack traces into structured guidance.
type DebugTool struct{}

func NewDebugTool() *DebugTool { return &DebugTool{} }

func (tool *DebugTool) Name() string { return "debug" }

func (tool *DebugTool) Description() string {
	return "Analyze runtime errors, stack traces, panic messages, and logs. " +
		"Extracts: error type, file locations, stack trace, root cause suggestions. " +
		"Paste the error output and get structured analysis."
}

func (tool *DebugTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"error_text": map[string]interface{}{
				"type":        "string",
				"description": "The error message, stack trace, or log output to analyze",
			},
		},
		"required": []string{"error_text"},
	}
}

func (tool *DebugTool) IsReadOnly() bool { return true }

func (tool *DebugTool) Execute(_ context.Context, params map[string]interface{}) (string, error) {
	errorText := configvalue.String(params, "error_text", "")
	if errorText == "" {
		return "Error: error_text is required", nil
	}
	errorType := DetectErrorType(errorText)
	var report strings.Builder
	report.WriteString("## Debug Analysis\n\n")
	report.WriteString(fmt.Sprintf("**Error Type**: %s\n\n", errorType))
	locations := ExtractFileLocations(errorText)
	if len(locations) > 0 {
		report.WriteString(fmt.Sprintf("### Locations (%d)\n", len(locations)))
		for index, location := range locations {
			if index >= 10 {
				report.WriteString(fmt.Sprintf("... (%d more)\n", len(locations)-10))
				break
			}
			report.WriteString(fmt.Sprintf("- %s\n", location))
		}
		report.WriteString("\n")
	}
	stack := ExtractStackTrace(errorText)
	if len(stack) > 0 {
		report.WriteString(fmt.Sprintf("### Stack Trace (%d frames)\n", len(stack)))
		for index, frame := range stack {
			if index >= 15 {
				report.WriteString(fmt.Sprintf("... (%d more frames)\n", len(stack)-15))
				break
			}
			report.WriteString(fmt.Sprintf("%d. %s\n", index+1, frame))
		}
		report.WriteString("\n")
	}
	causes := SuggestRootCauses(errorText, errorType)
	if len(causes) > 0 {
		report.WriteString("### Possible Root Causes\n")
		for _, cause := range causes {
			report.WriteString(fmt.Sprintf("- %s\n", cause))
		}
		report.WriteString("\n")
	}
	actions := SuggestDebugActions(errorText, errorType)
	if len(actions) > 0 {
		report.WriteString("### Recommended Actions\n")
		for _, action := range actions {
			report.WriteString(fmt.Sprintf("- [ ] %s\n", action))
		}
		report.WriteString("\n")
	}
	return report.String(), nil
}

func DetectErrorType(text string) string {
	lower := strings.ToLower(text)
	switch {
	case strings.Contains(lower, "nil pointer") || strings.Contains(lower, "null pointer") || strings.Contains(lower, "nil map"):
		return "Nil/Null Pointer Dereference"
	case strings.Contains(lower, "index out of range") || strings.Contains(lower, "slice bounds"):
		return "Index Out of Bounds"
	case strings.Contains(lower, "panic"):
		return "Panic"
	case strings.Contains(lower, "segmentation fault") || strings.Contains(lower, "segv"):
		return "Segmentation Fault"
	case strings.Contains(lower, "deadlock"):
		return "Deadlock Detected"
	case strings.Contains(lower, "race"):
		return "Race Condition"
	case strings.Contains(lower, "timeout") || strings.Contains(lower, "deadline exceeded"):
		return "Timeout"
	case strings.Contains(lower, "connection refused") || strings.Contains(lower, "no such host"):
		return "Network Error"
	case strings.Contains(lower, "permission denied") || strings.Contains(lower, "access denied"):
		return "Permission Error"
	case strings.Contains(lower, "out of memory") || strings.Contains(lower, "oom"):
		return "Out of Memory"
	case strings.Contains(lower, "stack overflow"):
		return "Stack Overflow"
	case strings.Contains(lower, "syntax error") || strings.Contains(lower, "unexpected"):
		return "Syntax/Parse Error"
	case strings.Contains(lower, "typeerror") || strings.Contains(lower, "type error"):
		return "Type Error"
	default:
		return "Unknown/Other"
	}
}

func ExtractFileLocations(text string) []string {
	var locations []string
	seen := make(map[string]bool)
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if FindFileLineColumn(line) >= 0 && !seen[line] {
			locations = append(locations, line)
			seen[line] = true
		}
	}
	return locations
}

func FindFileLineColumn(line string) int {
	for _, extension := range []string{".go:", ".py:", ".ts:", ".tsx:", ".js:", ".jsx:", ".rs:", ".java:", ".cpp:", ".c:", ".rb:"} {
		if index := strings.Index(line, extension); index >= 0 {
			return index
		}
	}
	return -1
}

func ExtractStackTrace(text string) []string {
	var frames []string
	inStack := false
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			if inStack {
				break
			}
			continue
		}
		if strings.HasPrefix(line, "goroutine ") {
			inStack = true
			frames = append(frames, line)
			continue
		}
		if inStack && (strings.Contains(line, ".go:") || strings.Contains(line, ".py:") || strings.Contains(line, ".ts:") || strings.Contains(line, ".rs:") || strings.HasPrefix(line, "created by ")) {
			frames = append(frames, line)
		}
	}
	return frames
}

func SuggestRootCauses(text, errorType string) []string {
	lower := strings.ToLower(text)
	var causes []string
	switch errorType {
	case "Nil/Null Pointer Dereference":
		causes = append(causes, "Accessing a field/method on a nil/uninitialized variable", "Map access without initialization (use make())")
		if strings.Contains(lower, "return") {
			causes = append(causes, "Function may return nil without proper nil check")
		}
	case "Index Out of Bounds":
		causes = append(causes, "Accessing slice/array beyond its length", "Loop index exceeding collection size")
	case "Panic":
		causes = append(causes, "Unexpected runtime condition — check the panic message")
		if strings.Contains(lower, "send on closed channel") {
			causes = append(causes, "Sending on a closed channel — ensure channel lifecycle management")
		}
	case "Deadlock Detected":
		causes = append(causes, "All goroutines are waiting for each other", "Check mutex lock ordering, channel operations")
	case "Timeout":
		causes = append(causes, "Operation taking longer than expected", "Check network connectivity, API rate limits, or infinite loops")
	case "Race Condition":
		causes = append(causes, "Concurrent access to shared data without proper synchronization", "Run with -race flag to detect: `go test -race ./...`")
	}
	return causes
}

func SuggestDebugActions(_ string, errorType string) []string {
	actions := []string{
		"Read the file at the error location to understand context",
		"Use lsp_hover at the error line for type information",
	}
	switch errorType {
	case "Nil/Null Pointer Dereference":
		actions = append(actions, "Add nil check before accessing the value", "Initialize maps with make() before use")
	case "Index Out of Bounds":
		actions = append(actions, "Add bounds check: `if idx < len(slice)`")
	case "Deadlock Detected":
		actions = append(actions, "Use `go test -race` to detect race conditions", "Review mutex lock ordering — always acquire in same order")
	case "Race Condition":
		actions = append(actions, "Run: `go test -race -run TestName ./...`", "Add sync.Mutex or use channels for synchronization")
	case "Timeout":
		actions = append(actions, "Increase timeout or add context deadline", "Check for infinite loops or blocking operations")
	}
	return actions
}
