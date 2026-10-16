package permissionpolicy

// GuardrailCheck carries context for a pre-tool-call authorization check.
type GuardrailCheck struct {
	ToolName   string                 `json:"tool_name"`
	ToolArgs   map[string]interface{} `json:"tool_args"`
	AgentName  string                 `json:"agent_name,omitempty"`
	SessionID  string                 `json:"session_id,omitempty"`
	UserID     string                 `json:"user_id,omitempty"`
	TenantID   string                 `json:"tenant_id,omitempty"`
	IsSubagent bool                   `json:"is_subagent,omitempty"`
}

// GuardrailVerdict is a provider's allow or deny decision.
type GuardrailVerdict struct {
	Allow   bool   `json:"allow"`
	Reason  string `json:"reason,omitempty"`
	Code    string `json:"code,omitempty"`
	Warning bool   `json:"warning,omitempty"`
}

// GuardrailProvider authorizes individual tool calls.
type GuardrailProvider interface {
	Name() string
	Check(req GuardrailCheck) GuardrailVerdict
}

// AllowlistProvider blocks tools outside an optional allowlist and warns when
// the host classifies an allowed tool as high risk.
type AllowlistProvider struct {
	enabled      bool
	enabledTools map[string]bool
	isHighRisk   func(string) bool
}

func NewAllowlistProvider(enabledTools []string, enabled bool, isHighRisk func(string) bool) *AllowlistProvider {
	tools := make(map[string]bool, len(enabledTools))
	for _, toolName := range enabledTools {
		tools[toolName] = true
	}
	return &AllowlistProvider{enabled: enabled, enabledTools: tools, isHighRisk: isHighRisk}
}

func (p *AllowlistProvider) Name() string { return "allowlist" }

func (p *AllowlistProvider) Check(req GuardrailCheck) GuardrailVerdict {
	if p == nil || !p.enabled {
		return GuardrailVerdict{Allow: true}
	}
	if len(p.enabledTools) > 0 && !p.enabledTools[req.ToolName] {
		return GuardrailVerdict{
			Allow:  false,
			Code:   "tool_disabled",
			Reason: "tool '" + req.ToolName + "' is not enabled for this agent",
		}
	}
	if p.isHighRisk != nil && p.isHighRisk(req.ToolName) {
		return GuardrailVerdict{
			Allow:   true,
			Warning: true,
			Code:    "high_risk",
			Reason:  "tool '" + req.ToolName + "' is classified as high-risk",
		}
	}
	return GuardrailVerdict{Allow: true}
}
