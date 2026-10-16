package middleware

import (
	"context"

	hookscore "mac/internal/hooks"
)

func middlewareRuntimeContext(ctx *Context) context.Context {
	if ctx != nil && ctx.Context != nil {
		return ctx.Context
	}
	return context.Background()
}

// HookV2Middleware integrates HookManagerV2 with the AgentMiddleware chain.
// It fires PreToolUse/PostToolUse/PostToolFailure hooks around tool execution,
// and BeforeModel/AfterModel hooks around LLM calls.
//
// Claude Code 借鉴: settings.json-driven hooks injected via middleware chain.
type HookV2Middleware struct {
	Base
	manager *hookscore.HookManagerV2
}

func NewHookV2Middleware(manager *hookscore.HookManagerV2) *HookV2Middleware {
	if manager == nil {
		return nil
	}
	return &HookV2Middleware{
		Base:    NewBase("hooks_v2"),
		manager: manager,
	}
}

func (m *HookV2Middleware) BeforeTool(ctx *Context) error {
	if m == nil || m.manager == nil {
		return nil
	}

	result := m.manager.Fire(middlewareRuntimeContext(ctx), hookscore.LCEventPreToolUse,
		ctx.ToolCall.Name, &hookscore.HookContextV2{
			AgentName: ctx.AgentName,
			TaskID:    ctx.TaskID,
			SessionID: ctx.SessionID,
			TenantID:  ctx.TenantID,
			ToolName:  ctx.ToolCall.Name,
			Params:    ctx.ToolCall.Arguments,
		})

	if result.Blocked {
		return NewGuardrailBlockError(ctx.ToolCall.Name, "blocked by pre_tool_use hook: "+result.BlockReason, "hook_pre_tool_use")
	}

	// Apply modified params from first hook that provided them
	for _, r := range result.Results {
		if r.ModifiedInput != nil {
			ctx.ToolCall.Arguments = r.ModifiedInput
			ctx.SetMeta("hooks.params_modified", true)
			break
		}
	}

	// Inject additional context
	if result.AdditionalContext != "" {
		ctx.SetMeta("hooks.pre_tool_context", result.AdditionalContext)
	}

	return nil
}

func (m *HookV2Middleware) AfterTool(ctx *Context) error {
	if m == nil || m.manager == nil || ctx.ToolResult == nil {
		return nil
	}

	event := hookscore.LCEventPostToolUse
	if ctx.ToolErr != nil {
		event = hookscore.LCEventPostToolFailure
	}

	resultContent := ""
	if ctx.ToolResult != nil {
		resultContent = ctx.ToolResult.Content
	}
	errStr := ""
	if ctx.ToolErr != nil {
		errStr = ctx.ToolErr.Error()
	}

	result := m.manager.Fire(middlewareRuntimeContext(ctx), event,
		ctx.ToolCall.Name, &hookscore.HookContextV2{
			AgentName: ctx.AgentName,
			TaskID:    ctx.TaskID,
			SessionID: ctx.SessionID,
			TenantID:  ctx.TenantID,
			ToolName:  ctx.ToolCall.Name,
			Params:    ctx.ToolCall.Arguments,
			Result:    resultContent,
			Error:     errStr,
		})

	// Apply modified result from first hook that provided it
	for _, r := range result.Results {
		if r.ModifiedResult != "" && ctx.ToolResult != nil {
			ctx.ToolResult.Content = r.ModifiedResult
			break
		}
	}

	// Inject additional context
	if result.AdditionalContext != "" {
		existing, _ := ctx.GetMeta("hooks.post_tool_context")
		if s, ok := existing.(string); ok {
			result.AdditionalContext = s + "\n" + result.AdditionalContext
		}
		ctx.SetMeta("hooks.post_tool_context", result.AdditionalContext)
	}

	return nil
}

func (m *HookV2Middleware) BeforeModel(ctx *Context) error {
	if m == nil || m.manager == nil {
		return nil
	}

	result := m.manager.Fire(middlewareRuntimeContext(ctx), hookscore.LCEventBeforeModel,
		"*", &hookscore.HookContextV2{
			AgentName: ctx.AgentName,
			SessionID: ctx.SessionID,
			TaskID:    ctx.TaskID,
			TenantID:  ctx.TenantID,
		})

	if result.Blocked {
		return NewGuardrailBlockError("llm_call", "blocked by before_model hook: "+result.BlockReason, "hook_before_model")
	}

	if result.AdditionalContext != "" {
		ctx.SetMeta("hooks.before_model_context", result.AdditionalContext)
	}

	return nil
}

func (m *HookV2Middleware) AfterModel(ctx *Context) error {
	if m == nil || m.manager == nil {
		return nil
	}

	result := m.manager.Fire(middlewareRuntimeContext(ctx), hookscore.LCEventAfterModel,
		"*", &hookscore.HookContextV2{
			AgentName: ctx.AgentName,
			SessionID: ctx.SessionID,
			TaskID:    ctx.TaskID,
			TenantID:  ctx.TenantID,
		})

	if result.AdditionalContext != "" {
		ctx.SetMeta("hooks.after_model_context", result.AdditionalContext)
	}

	return nil
}
