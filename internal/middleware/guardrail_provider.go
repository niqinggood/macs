package middleware

import (
	"mac/internal/permissionpolicy"
	"mac/internal/toolcontract"
)

type GuardrailCheck = permissionpolicy.GuardrailCheck
type GuardrailVerdict = permissionpolicy.GuardrailVerdict
type GuardrailProvider = permissionpolicy.GuardrailProvider
type AllowlistProvider = permissionpolicy.AllowlistProvider

func NewAllowlistProvider(enabledTools []string, cfg toolcontract.GuardrailConfig, highRisk func(string) bool) *AllowlistProvider {
	return permissionpolicy.NewAllowlistProvider(enabledTools, cfg.Enabled, highRisk)
}

// GuardrailProviderMiddleware wraps any GuardrailProvider as an AgentMiddleware.
// It hooks BeforeTool to check each tool call before execution.
type GuardrailProviderMiddleware struct {
	Base
	provider GuardrailProvider
}

func NewGuardrailProviderMiddleware(provider GuardrailProvider) *GuardrailProviderMiddleware {
	if provider == nil {
		return nil
	}
	return &GuardrailProviderMiddleware{
		Base:     NewBase("guardrail:" + provider.Name()),
		provider: provider,
	}
}

func (m *GuardrailProviderMiddleware) BeforeTool(ctx *Context) error {
	req := GuardrailCheck{
		ToolName:  ctx.ToolCall.Name,
		ToolArgs:  ctx.ToolCall.Arguments,
		AgentName: ctx.AgentName,
		SessionID: ctx.SessionID,
		UserID:    ctx.UserID,
		TenantID:  ctx.TenantID,
	}

	verdict := m.provider.Check(req)
	if !verdict.Allow {
		return NewGuardrailBlockError(req.ToolName, verdict.Reason, verdict.Code)
	}

	if verdict.Warning {
		ctx.SetMeta("guardrail.warning", verdict)
	}

	return nil
}
