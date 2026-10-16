package middleware

import "strings"

// GuardrailBlockError signals that middleware deliberately blocked an action.
type GuardrailBlockError struct {
	ToolName string
	Reason   string
	Code     string
}

func NewGuardrailBlockError(toolName, reason, code string) *GuardrailBlockError {
	return &GuardrailBlockError{ToolName: toolName, Reason: reason, Code: code}
}

func (err *GuardrailBlockError) Error() string {
	if err == nil {
		return "guardrail blocked"
	}
	return "guardrail blocked '" + err.ToolName + "': " + err.Reason
}

func (err *GuardrailBlockError) MiddlewareErrorCode() string {
	if err == nil || strings.TrimSpace(err.Code) == "" {
		return "guardrail_blocked"
	}
	return err.Code
}
