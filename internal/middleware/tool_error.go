package middleware

import (
	"fmt"
	"strings"

	"mac/internal/toolcontract"
)

// ToolErrorMiddleware normalizes tool execution errors into LLM-readable
// messages. Without it, a raw Go error (e.g. "connection refused") would
// reach the LLM without context and without recovery hints.
//
// After a tool fails, this middleware rewrites ctx.ToolResult to a canonical
// failed Tool Result. Callers can then rely on status: failed instead of
// guessing from arbitrary payload text.
//
// This also tracks per-tool failure counts so downstream middleware
// (e.g. loop detection) can react.

const maxErrorContentLen = 800

type ToolErrorMiddleware struct {
	Base
}

func NewToolErrorMiddleware() *ToolErrorMiddleware {
	return &ToolErrorMiddleware{Base: NewBase("tool_error")}
}

func (m *ToolErrorMiddleware) AfterTool(ctx *Context) error {
	if ctx.ToolErr == nil {
		return nil
	}

	// Replace raw error with structured, LLM-friendly message
	ctx.ToolResult = &toolcontract.Result{
		ToolCallID: ctx.ToolCall.ID,
		Name:       ctx.ToolCall.Name,
		Content:    m.BuildErrorContent(ctx.ToolCall.Name, ctx.ToolErr),
	}

	// Track failure count for downstream middleware
	key := "tool_error." + ctx.ToolCall.Name + ".fail_count"
	count, _ := ctx.GetMeta(key)
	var n int
	if count != nil {
		n = count.(int)
	}
	ctx.SetMeta(key, n+1)

	// Clear the Go error so callers don't double-handle
	ctx.ToolErr = nil

	return nil
}

func (m *ToolErrorMiddleware) BuildErrorContent(toolName string, err error) string {
	detail := strings.TrimSpace(err.Error())
	if detail == "" {
		detail = "unknown error"
	}
	detailRunes := []rune(detail)
	if len(detailRunes) > maxErrorContentLen {
		detail = string(detailRunes[:maxErrorContentLen-3]) + "..."
	}
	return fmt.Sprintf(
		"Tool Result\ntool: %s\nstatus: failed\nerror: %s\nrecovery: Continue with available context or try a different approach.",
		toolName, detail,
	)
}
