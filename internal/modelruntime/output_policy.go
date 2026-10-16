package modelruntime

import (
	"context"
	"strings"
	"sync"

	"mac/internal/toolcontract"
)

// Fast read-only research should not inherit a model's full advertised output
// window. Model/provider configurations often use that field as a capability
// ceiling (and users sometimes mistake it for context size), while these turns
// need one compact answer. A turn-local limit keeps the configured ceiling
// intact for artifact/code work and avoids mutating a shared LLMAgent.
const FastReadOnlyResearchOutputTokenLimit = 4096
const CurrentNewsResearchOutputTokenLimit = 5632
const CurrentNewsLocalPatchOutputTokenLimit = 2048

type llmOutputTokenLimitContextKey struct{}
type llmTemperatureOverrideContextKey struct{}
type llmThinkingDisabledContextKey struct{}

type llmTemperatureOverride struct {
	value float64
}

func WithOutputTokenLimit(ctx context.Context, limit int) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if limit <= 0 {
		return ctx
	}
	return context.WithValue(ctx, llmOutputTokenLimitContextKey{}, limit)
}

func EffectiveMaxTokens(ctx context.Context, configured int) int {
	limit := 0
	if ctx != nil {
		limit, _ = ctx.Value(llmOutputTokenLimitContextKey{}).(int)
	}
	if limit <= 0 {
		return configured
	}
	if configured <= 0 || limit < configured {
		return limit
	}
	return configured
}

func WithTemperatureOverride(ctx context.Context, temperature float64) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if temperature < 0 {
		return ctx
	}
	return context.WithValue(ctx, llmTemperatureOverrideContextKey{}, llmTemperatureOverride{value: temperature})
}

func EffectiveTemperature(ctx context.Context, configured float64) float64 {
	if ctx != nil {
		if override, ok := ctx.Value(llmTemperatureOverrideContextKey{}).(llmTemperatureOverride); ok {
			return override.value
		}
	}
	return configured
}

func WithThinkingDisabled(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, llmThinkingDisabledContextKey{}, true)
}

func thinkingDisabled(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	disabled, _ := ctx.Value(llmThinkingDisabledContextKey{}).(bool)
	return disabled
}

const (
	ThinkingModeDisabled = "disabled"
	ThinkingModeEnabled  = "enabled"
	ThinkingModeAuto     = "auto"
)

type ThinkingModel struct {
	BaseURL          string
	ModelName        string
	ThinkingMode     string
	SupportsThinking bool
}

type reasoningReplayHistoryShape struct {
	hasReasoning             bool
	hasToolCallWithoutReason bool
}

// adaptiveLLMThinkingDisabled is a process-local capability cache. A provider
// protocol failure for one endpoint/model must not disable thinking for every
// other model, so entries are isolated by normalized BaseURL + ModelName.
var adaptiveLLMThinkingDisabled sync.Map

func thinkingCompatibilityKey(config ThinkingModel) string {
	model := strings.ToLower(strings.TrimSpace(config.ModelName))
	if model == "" {
		return ""
	}
	endpoint := strings.ToLower(strings.TrimRight(strings.TrimSpace(config.BaseURL), "/"))
	return endpoint + "\x00" + model
}

func MarkAdaptiveThinkingDisabled(config ThinkingModel) bool {
	key := thinkingCompatibilityKey(config)
	if key == "" {
		return false
	}
	_, loaded := adaptiveLLMThinkingDisabled.LoadOrStore(key, struct{}{})
	return !loaded
}

func IsAdaptiveThinkingDisabled(config ThinkingModel) bool {
	key := thinkingCompatibilityKey(config)
	if key == "" {
		return false
	}
	_, disabled := adaptiveLLMThinkingDisabled.Load(key)
	return disabled
}

func normalizedThinkingMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case ThinkingModeDisabled:
		return ThinkingModeDisabled
	case ThinkingModeEnabled:
		return ThinkingModeEnabled
	case ThinkingModeAuto:
		return ThinkingModeAuto
	default:
		return ""
	}
}

// defaultsToToggleableThinking identifies provider models observed to enable
// thinking when the field is omitted. Keep this deliberately narrow: ordinary
// OpenAI-compatible endpoints may reject an unknown `thinking` parameter.
func (m ThinkingModel) defaultsToToggleableThinking() bool {
	model := strings.ToLower(strings.TrimSpace(m.ModelName))
	return strings.Contains(model, "minimax-m3") ||
		strings.Contains(model, "deepseek-v4-pro")
}

func inspectReasoningReplayHistory(messages []map[string]interface{}) reasoningReplayHistoryShape {
	var shape reasoningReplayHistoryShape
	for _, message := range messages {
		if reasoning, _ := message["reasoning_content"].(string); reasoning != "" {
			shape.hasReasoning = true
		}
		role, _ := message["role"].(string)
		if role != "assistant" || len(toolcontract.ToolCallMaps(message["tool_calls"])) == 0 {
			continue
		}
		if reasoning, _ := message["reasoning_content"].(string); reasoning == "" {
			shape.hasToolCallWithoutReason = true
		}
	}
	return shape
}

// effectiveLLMThinkingMode returns the wire-level mode. Empty means the
// provider receives no thinking field. Safe-off is the default: thinking is
// enabled only by an explicit `enabled` value or the opt-in legacy `auto`
// heuristic. A mixed reasoning history cannot be replayed safely in thinking
// mode, so it is always downgraded before the provider can reject it.
func EffectiveThinkingMode(ctx context.Context, config ThinkingModel, messages []map[string]interface{}) string {
	if IsAdaptiveThinkingDisabled(config) {
		return ThinkingModeDisabled
	}
	configured := normalizedThinkingMode(config.ThinkingMode)
	history := inspectReasoningReplayHistory(messages)
	configuredThinking := configured == ThinkingModeEnabled ||
		(configured == ThinkingModeAuto && config.SupportsThinking)
	protocolGap := history.hasToolCallWithoutReason &&
		(history.hasReasoning || configuredThinking || config.defaultsToToggleableThinking())

	if protocolGap {
		return ThinkingModeDisabled
	}
	if thinkingDisabled(ctx) {
		if configured == ThinkingModeDisabled || configuredThinking ||
			config.defaultsToToggleableThinking() || config.SupportsThinking {
			return ThinkingModeDisabled
		}
		return ""
	}

	switch configured {
	case ThinkingModeDisabled, ThinkingModeEnabled:
		return configured
	case ThinkingModeAuto:
		if config.SupportsThinking {
			return ThinkingModeEnabled
		}
		return ""
	default:
		if config.defaultsToToggleableThinking() {
			return ThinkingModeDisabled
		}
		return ""
	}
}

func ApplyThinkingPolicy(ctx context.Context, config ThinkingModel, messages []map[string]interface{}, requestBody map[string]interface{}) string {
	if requestBody == nil {
		return ""
	}
	if mode := EffectiveThinkingMode(ctx, config, messages); mode != "" {
		requestBody["thinking"] = map[string]interface{}{"type": mode}
		return mode
	}
	return ""
}
