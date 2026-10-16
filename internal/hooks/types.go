package hooks

import (
	"encoding/json"
	"time"
)

// ====================== Hook System Type Definitions ======================
// Claude Code 借鉴: 29 lifecycle events + 6 executor types
//
// Type naming avoids conflicts with:
//   - shell_manager.go: HookType (shell-script hook event names)
//   - security/hook_chain.go: security HookEvent struct
//   - event_log.go: EventError constant
//   - permission_rules.go: matchGlob function (reused)

// LifecycleEvent enumerates agent lifecycle hook points.
type LifecycleEvent string

const (
	// Tool lifecycle
	LCEventPreToolUse      LifecycleEvent = "PreToolUse"
	LCEventPostToolUse     LifecycleEvent = "PostToolUse"
	LCEventPostToolFailure LifecycleEvent = "PostToolFailure"

	// Model lifecycle
	LCEventBeforeModel LifecycleEvent = "BeforeModel"
	LCEventAfterModel  LifecycleEvent = "AfterModel"

	// Session lifecycle
	LCEventSessionStart LifecycleEvent = "SessionStart"
	LCEventSessionEnd   LifecycleEvent = "SessionEnd"

	// User interaction
	LCEventUserPromptSubmit  LifecycleEvent = "UserPromptSubmit"
	LCEventPermissionRequest LifecycleEvent = "PermissionRequest"
	LCEventPermissionDenied  LifecycleEvent = "PermissionDenied"
	LCEventElicitation       LifecycleEvent = "Elicitation"

	// Agent lifecycle
	LCEventSubagentStart LifecycleEvent = "SubagentStart"
	LCEventSubagentStop  LifecycleEvent = "SubagentStop"

	// Context management
	LCEventPreCompact  LifecycleEvent = "PreCompact"
	LCEventPostCompact LifecycleEvent = "PostCompact"

	// Notifications
	LCEventNotification LifecycleEvent = "Notification"

	// Configuration
	LCEventSettingsLoaded LifecycleEvent = "SettingsLoaded"
	LCEventConfigChange   LifecycleEvent = "ConfigChange"

	// Pipeline
	LCEventPipelineStepStart LifecycleEvent = "PipelineStepStart"
	LCEventPipelineStepEnd   LifecycleEvent = "PipelineStepEnd"

	// System
	LCEventStop        LifecycleEvent = "Stop"
	LCEventSystemError LifecycleEvent = "Error"
)

// allLifecycleEvents lists every known lifecycle event, used for validation and listing.
var allLifecycleEvents = []LifecycleEvent{
	LCEventPreToolUse, LCEventPostToolUse, LCEventPostToolFailure,
	LCEventBeforeModel, LCEventAfterModel,
	LCEventSessionStart, LCEventSessionEnd,
	LCEventUserPromptSubmit, LCEventPermissionRequest, LCEventPermissionDenied, LCEventElicitation,
	LCEventSubagentStart, LCEventSubagentStop,
	LCEventPreCompact, LCEventPostCompact,
	LCEventNotification,
	LCEventSettingsLoaded, LCEventConfigChange,
	LCEventPipelineStepStart, LCEventPipelineStepEnd,
	LCEventStop, LCEventSystemError,
}

// IsValidLifecycleEvent checks if a string is a recognized lifecycle event.
func IsValidLifecycleEvent(s string) bool {
	for _, e := range allLifecycleEvents {
		if string(e) == s {
			return true
		}
	}
	return false
}

// matchQueryForLifecycleEvent returns the default match key for an event type.
func matchQueryForLifecycleEvent(evt LifecycleEvent, ctx *HookContextV2) string {
	switch evt {
	case LCEventPreToolUse, LCEventPostToolUse, LCEventPostToolFailure:
		if ctx.ToolName != "" {
			return ctx.ToolName
		}
		return "*"
	case LCEventSessionStart:
		return ctx.Channel
	case LCEventUserPromptSubmit:
		return ctx.Channel
	default:
		return "*"
	}
}

// ====================== Hook Executor Types ======================

// HookExecutorType identifies how a hook executes.
type HookExecutorType string

const (
	HookExecShell    HookExecutorType = "shell"
	HookExecHTTP     HookExecutorType = "http"
	HookExecCallback HookExecutorType = "callback"
	HookExecPrompt   HookExecutorType = "prompt"
	HookExecAgent    HookExecutorType = "agent"
)

// ====================== Hook Configuration ======================

// V2HookConfig defines a single hook loaded from settings.
type V2HookConfig struct {
	Type    HookExecutorType `json:"type" yaml:"type"`
	Match   string           `json:"match,omitempty" yaml:"match,omitempty"`     // glob pattern for tool name/channel
	Command string           `json:"command,omitempty" yaml:"command,omitempty"` // shell script path or callback ID
	URL     string           `json:"url,omitempty" yaml:"url,omitempty"`         // HTTP endpoint
	Timeout int              `json:"timeout,omitempty" yaml:"timeout,omitempty"` // seconds, default 30
}

// V2HookSettings is the top-level hooks block in settings.json/yaml.
type V2HookSettings struct {
	Enabled map[LifecycleEvent][]V2HookConfig `json:"enabled" yaml:"enabled"`
}

// ====================== Hook Context ======================

// HookContextV2 is passed to hook executors with full context.
type HookContextV2 struct {
	Event     LifecycleEvent         `json:"event"`
	AgentName string                 `json:"agent_name"`
	TaskID    string                 `json:"task_id,omitempty"`
	SessionID string                 `json:"session_id,omitempty"`
	TenantID  string                 `json:"tenant_id,omitempty"`
	ToolName  string                 `json:"tool_name,omitempty"`
	Channel   string                 `json:"channel,omitempty"`
	Params    map[string]interface{} `json:"params,omitempty"`
	Result    string                 `json:"result,omitempty"`
	Error     string                 `json:"error,omitempty"`
	Message   string                 `json:"message,omitempty"`
	Timestamp int64                  `json:"timestamp"`
}

// ToJSON marshals the context for shell/http hooks.
func (c *HookContextV2) ToJSON() []byte {
	b, _ := json.Marshal(c)
	return b
}

// ====================== Hook Result ======================

// HookDecision is the action a hook wants taken.
type HookDecision string

const (
	HookDecisionAllow       HookDecision = "allow"
	HookDecisionBlock       HookDecision = "block"
	HookDecisionPassthrough HookDecision = "passthrough"
)

// HookResultV2 is the structured output from a hook execution.
type HookResultV2 struct {
	Decision          HookDecision           `json:"decision"`
	Message           string                 `json:"message,omitempty"`
	AdditionalContext string                 `json:"additional_context,omitempty"`
	ModifiedInput     map[string]interface{} `json:"modified_input,omitempty"`
	ModifiedResult    string                 `json:"modified_result,omitempty"`

	// Execution metadata (not from hook output)
	ExecutorType HookExecutorType `json:"-"`
	ConfigID     string           `json:"-"`
	Duration     time.Duration    `json:"-"`
	RawOutput    string           `json:"-"`
}

// ====================== Hook Callback Registration ======================

// HookCallbackFunc is the signature for Go-level hook callbacks.
// Return nil to allow, or a HookResultV2 with Decision=Block to deny.
type HookCallbackFunc func(ctx *HookContextV2) *HookResultV2

// ====================== Hook Executor Interface ======================

// HookExecutorV2 executes a single hook configuration and returns a result.
type HookExecutorV2 interface {
	Execute(cfg V2HookConfig, hookCtx *HookContextV2) (*HookResultV2, error)
}
