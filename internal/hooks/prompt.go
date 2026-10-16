package hooks

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// ====================== Prompt Hook Executor ======================
// Sends hook context to an LLM and parses the structured response.
// Used by both HookExecPrompt and HookExecAgent types.

type PromptCaller func(ctx context.Context, messages []map[string]interface{}, maxTokens int) (string, error)

type HookPromptExecutor struct {
	callLLM    PromptCaller
	maxTimeout time.Duration
}

func NewHookPromptExecutor(callLLM PromptCaller) *HookPromptExecutor {
	return &HookPromptExecutor{
		callLLM:    callLLM,
		maxTimeout: 60 * time.Second,
	}
}

func (e *HookPromptExecutor) Execute(cfg V2HookConfig, hookCtx *HookContextV2) (*HookResultV2, error) {
	return e.ExecuteContext(context.Background(), cfg, hookCtx)
}

func (e *HookPromptExecutor) ExecuteContext(parent context.Context, cfg V2HookConfig, hookCtx *HookContextV2) (*HookResultV2, error) {
	if e.callLLM == nil {
		return nil, fmt.Errorf("prompt executor has no LLM caller")
	}

	timeout := 60 * time.Second
	if cfg.Timeout > 0 {
		timeout = time.Duration(cfg.Timeout) * time.Second
	}
	if timeout > e.maxTimeout {
		timeout = e.maxTimeout
	}

	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()

	systemPrompt := `You are a hook decision engine. Based on the hook context, decide whether to allow, block, or passthrough the action.
Respond with ONLY a JSON object:
{
  "decision": "allow" | "block" | "passthrough",
  "message": "reason for the decision",
  "additional_context": "any context to inject (optional)"
}`

	userPrompt := fmt.Sprintf("Hook event: %s\nContext: %s", hookCtx.Event, string(hookCtx.ToJSON()))

	raw, err := e.callLLM(ctx, []map[string]interface{}{
		{"role": "system", "content": systemPrompt},
		{"role": "user", "content": userPrompt},
	}, 512)

	if err != nil {
		if parent.Err() != nil {
			return nil, fmt.Errorf("prompt hook canceled: %w", parent.Err())
		}
		return &HookResultV2{
			Decision:     HookDecisionPassthrough,
			Message:      fmt.Sprintf("prompt hook LLM call failed: %v", err),
			ExecutorType: HookExecPrompt,
		}, nil
	}

	result, parseErr := ValidateHookOutput([]byte(raw))
	if parseErr != nil {
		return &HookResultV2{
			Decision:     HookDecisionAllow,
			Message:      fmt.Sprintf("prompt hook response parse warning: %v", parseErr),
			ExecutorType: HookExecPrompt,
			RawOutput:    raw,
		}, nil
	}

	result.ExecutorType = HookExecPrompt
	result.RawOutput = raw

	// Allow hook to modify tool params via structured output
	if result.AdditionalContext != "" {
		var extra map[string]interface{}
		if json.Unmarshal([]byte(result.AdditionalContext), &extra) == nil {
			if modified, ok := extra["modified_input"]; ok {
				if m, ok := modified.(map[string]interface{}); ok {
					result.ModifiedInput = m
				}
			}
		}
	}

	return result, nil
}
