package middleware

import (
	"context"
	"strings"
)

// ClarificationMiddleware intercepts the ask_clarification tool call and
// interrupts the agent run, returning the model's question to the user
// without executing any further tools.
//
// Pattern (borrowed from DeerFlow): the model calls ask_clarification with
// a question, and the middleware intercepts it — converting a tool call into
// a conversation pause. This prevents the model from guessing/assuming
// and reduces wasted tool calls on wrong assumptions.
//
// The ask_clarification tool is registered as a hidden tool so the model
// knows it exists, but the middleware intercepts it before actual execution.

// AskClarificationTool lets the model ask the user for clarification.
// When called, the clarification middleware intercepts it and pauses the run.
type AskClarificationTool struct{}

func (t *AskClarificationTool) Name() string { return "ask_clarification" }
func (t *AskClarificationTool) Description() string {
	return "Ask a concise question only when missing information materially affects the result or the user explicitly requested review. Use reasonable defaults for routine details and continue work already authorized; task complexity alone is not a reason to ask again. Configured approval requirements still apply."
}
func (t *AskClarificationTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"question": map[string]interface{}{
				"type":        "string",
				"description": "The question to ask the user. Be specific and offer options if applicable.",
			},
		},
		"required": []string{"question"},
	}
}
func (t *AskClarificationTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	question, _ := params["question"].(string)
	return "[CLARIFICATION] " + question, nil
}

// ClarificationMiddleware — intercepts ask_clarification tool calls and
// signals the caller to pause the agent run for user input.
type ClarificationMiddleware struct {
	Base
}

func NewClarificationMiddleware() *ClarificationMiddleware {
	return &ClarificationMiddleware{Base: NewBase("clarification")}
}

func (m *ClarificationMiddleware) AfterTool(ctx *Context) error {
	if ctx.ToolCall.Name != "ask_clarification" {
		return nil
	}

	// Mark the run as needing user clarification
	ctx.SetMeta("clarification.needed", true)

	if ctx.ToolResult != nil {
		content := ctx.ToolResult.Content
		// Strip [CLARIFICATION] prefix for clean user display
		if strings.HasPrefix(content, "[CLARIFICATION] ") {
			ctx.ToolResult.Content = content[len("[CLARIFICATION] "):]
		}
	}

	return nil
}

// NeedsClarification checks if the context has a pending clarification request.
func NeedsClarification(ctx *Context) bool {
	v, ok := ctx.GetMeta("clarification.needed")
	return ok && v == true
}

// GetClarificationQuestion extracts the question text from a clarification result.
func GetClarificationQuestion(ctx *Context) string {
	if ctx.ToolResult != nil {
		return ctx.ToolResult.Content
	}
	return ""
}
