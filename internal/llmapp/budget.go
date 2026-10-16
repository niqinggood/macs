package llmapp

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
)

var ErrLLMAppBudgetExceeded = errors.New("llm_app run budget exceeded")

// LLMAppBudgetPolicy bounds the complete run, not one provider request.
// Prices are explicit application metadata because provider-compatible APIs do
// not expose a trustworthy universal pricing table.
type LLMAppBudgetPolicy struct {
	MaxModelCalls              int     `json:"max_model_calls,omitempty"`
	MaxTotalTokens             int     `json:"max_total_tokens,omitempty"`
	MaxDurationSeconds         int     `json:"max_duration_seconds,omitempty"`
	MaxCostUSD                 float64 `json:"max_cost_usd,omitempty"`
	InputCostPerMillionTokens  float64 `json:"input_cost_per_million_tokens,omitempty"`
	OutputCostPerMillionTokens float64 `json:"output_cost_per_million_tokens,omitempty"`
}

type LLMAppBudgetSnapshot struct {
	Policy           LLMAppBudgetPolicy `json:"policy"`
	ReservedTokens   int                `json:"reserved_tokens,omitempty"`
	ReservedCostUSD  float64            `json:"reserved_cost_usd,omitempty"`
	RemainingCalls   int                `json:"remaining_model_calls,omitempty"`
	RemainingTokens  int                `json:"remaining_tokens,omitempty"`
	RemainingCostUSD float64            `json:"remaining_cost_usd,omitempty"`
	Exhausted        bool               `json:"exhausted"`
	Reason           string             `json:"reason,omitempty"`
}

func normalizeLLMAppBudget(policy *LLMAppBudgetPolicy, maxItems, validationRetries int) error {
	if policy == nil {
		return nil
	}
	if policy.MaxModelCalls < 0 || policy.MaxTotalTokens < 0 || policy.MaxDurationSeconds < 0 ||
		policy.MaxCostUSD < 0 || policy.InputCostPerMillionTokens < 0 || policy.OutputCostPerMillionTokens < 0 {
		return errors.New("llm_app budget values cannot be negative")
	}
	if policy.MaxModelCalls == 0 {
		calls := maxItems * (validationRetries + 1)
		if calls <= 0 {
			calls = 1
		}
		if calls > 10000 {
			calls = 10000
		}
		policy.MaxModelCalls = calls
	}
	if policy.MaxModelCalls > 100000 {
		return errors.New("budget.max_model_calls cannot exceed 100000")
	}
	if policy.MaxTotalTokens > 2_000_000_000 {
		return errors.New("budget.max_total_tokens cannot exceed 2000000000")
	}
	if policy.MaxDurationSeconds == 0 {
		policy.MaxDurationSeconds = 3600
	}
	if policy.MaxDurationSeconds > 7*24*60*60 {
		return errors.New("budget.max_duration_seconds cannot exceed 604800")
	}
	for name, value := range map[string]float64{
		"max_cost_usd":                   policy.MaxCostUSD,
		"input_cost_per_million_tokens":  policy.InputCostPerMillionTokens,
		"output_cost_per_million_tokens": policy.OutputCostPerMillionTokens,
	} {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return fmt.Errorf("budget.%s must be finite", name)
		}
	}
	if policy.MaxCostUSD > 0 && policy.InputCostPerMillionTokens == 0 && policy.OutputCostPerMillionTokens == 0 {
		return errors.New("budget.max_cost_usd requires an input or output token price")
	}
	return nil
}

func llmAppTokenCost(policy LLMAppBudgetPolicy, inputTokens, outputTokens int) float64 {
	return (float64(inputTokens)*policy.InputCostPerMillionTokens +
		float64(outputTokens)*policy.OutputCostPerMillionTokens) / 1_000_000
}

func llmAppBudgetUsageTokens(usage LLMAppUsage) int {
	actual := usage.InputTokens + usage.OutputTokens
	estimated := usage.EstimatedInputTokens + usage.EstimatedOutputTokens
	if actual > estimated {
		return actual
	}
	return estimated
}

func (m *LLMAppManager) reserveModelCall(
	run *llmAppManagedRun,
	inputTokens int,
	maxOutputTokens int,
) error {
	if run == nil {
		return errors.New("llm_app run is unavailable")
	}
	if inputTokens < 0 {
		inputTokens = 0
	}
	if maxOutputTokens < 0 {
		maxOutputTokens = 0
	}
	now := time.Now()
	run.mu.Lock()
	policy := run.budget
	reason := ""
	if policy.MaxDurationSeconds > 0 && !run.record.CreatedAt.IsZero() &&
		now.Sub(run.record.CreatedAt) >= time.Duration(policy.MaxDurationSeconds)*time.Second {
		reason = "maximum run duration reached"
	} else if policy.MaxModelCalls > 0 && run.record.Usage.ModelCalls >= policy.MaxModelCalls {
		reason = "maximum model calls reached"
	} else if policy.MaxTotalTokens > 0 &&
		llmAppBudgetUsageTokens(run.record.Usage)+run.reservedOutputTokens+inputTokens+maxOutputTokens > policy.MaxTotalTokens {
		reason = "maximum token budget would be exceeded"
	} else if policy.MaxCostUSD > 0 {
		reservedCost := llmAppTokenCost(policy, inputTokens, maxOutputTokens)
		if run.record.Usage.CostUSD+run.reservedCostUSD+reservedCost > policy.MaxCostUSD+1e-12 {
			reason = "maximum cost budget would be exceeded"
		}
	}
	if reason != "" {
		run.record.Budget = buildLLMAppBudgetSnapshot(policy, run.record.Usage, run.reservedOutputTokens, run.reservedCostUSD, reason)
		run.mu.Unlock()
		_ = m.persistRunRecord(run)
		return fmt.Errorf("%w: %s", ErrLLMAppBudgetExceeded, reason)
	}
	run.record.Usage.ModelCalls++
	run.record.Usage.EstimatedInputTokens += inputTokens
	run.reservedOutputTokens += maxOutputTokens
	reservedCost := llmAppTokenCost(policy, inputTokens, maxOutputTokens)
	run.reservedCostUSD += reservedCost
	run.record.Budget = buildLLMAppBudgetSnapshot(policy, run.record.Usage, run.reservedOutputTokens, run.reservedCostUSD, "")
	run.record.LastHeartbeatAt = now
	run.mu.Unlock()
	return m.persistRunRecord(run)
}

func (m *LLMAppManager) settleModelCall(
	run *llmAppManagedRun,
	inputEstimate int,
	maxOutputTokens int,
	response ModelResponse,
	callErr error,
) error {
	if run == nil {
		return errors.New("llm_app run is unavailable")
	}
	if inputEstimate < 0 {
		inputEstimate = 0
	}
	if maxOutputTokens < 0 {
		maxOutputTokens = 0
	}
	outputEstimate := estimateLLMAppTokens(len(response.Content))
	run.mu.Lock()
	if run.reservedOutputTokens >= maxOutputTokens {
		run.reservedOutputTokens -= maxOutputTokens
	} else {
		run.reservedOutputTokens = 0
	}
	reservedCost := llmAppTokenCost(run.budget, inputEstimate, maxOutputTokens)
	run.reservedCostUSD -= reservedCost
	if run.reservedCostUSD < 0 {
		run.reservedCostUSD = 0
	}
	run.record.Usage.EstimatedOutputTokens += outputEstimate
	if response.PromptTokens > 0 {
		run.record.Usage.InputTokens += response.PromptTokens
	}
	if response.CompletionTokens > 0 {
		run.record.Usage.OutputTokens += response.CompletionTokens
	}
	cost := response.CostUSD
	if cost == 0 {
		inputTokens := response.PromptTokens
		if inputTokens == 0 {
			inputTokens = inputEstimate
		}
		outputTokens := response.CompletionTokens
		if outputTokens == 0 {
			outputTokens = outputEstimate
		}
		cost = llmAppTokenCost(run.budget, inputTokens, outputTokens)
	}
	if cost > 0 {
		run.record.Usage.CostUSD += cost
	}
	if callErr != nil {
		run.record.Usage.UnknownModelCalls++
	}
	if requestID := strings.TrimSpace(response.RequestID); requestID != "" {
		run.record.Usage.LastProviderRequestID = requestID
	}
	reason := ""
	if run.budget.MaxTotalTokens > 0 && llmAppBudgetUsageTokens(run.record.Usage) > run.budget.MaxTotalTokens {
		reason = "maximum token budget exceeded by provider-reported usage"
	}
	if run.budget.MaxCostUSD > 0 && run.record.Usage.CostUSD > run.budget.MaxCostUSD+1e-12 {
		reason = "maximum cost budget exceeded by provider-reported usage"
	}
	run.record.Budget = buildLLMAppBudgetSnapshot(run.budget, run.record.Usage, run.reservedOutputTokens, run.reservedCostUSD, reason)
	run.record.LastHeartbeatAt = time.Now()
	run.mu.Unlock()
	if err := m.persistRunRecord(run); err != nil {
		return err
	}
	if reason != "" {
		return fmt.Errorf("%w: %s", ErrLLMAppBudgetExceeded, reason)
	}
	return nil
}

func buildLLMAppBudgetSnapshot(
	policy LLMAppBudgetPolicy,
	usage LLMAppUsage,
	reservedTokens int,
	reservedCostUSD float64,
	reason string,
) LLMAppBudgetSnapshot {
	snapshot := LLMAppBudgetSnapshot{
		Policy: policy, ReservedTokens: reservedTokens, ReservedCostUSD: reservedCostUSD,
		Reason: reason, Exhausted: reason != "",
	}
	if policy.MaxModelCalls > 0 {
		snapshot.RemainingCalls = policy.MaxModelCalls - usage.ModelCalls
		if snapshot.RemainingCalls < 0 {
			snapshot.RemainingCalls = 0
		}
	}
	if policy.MaxTotalTokens > 0 {
		snapshot.RemainingTokens = policy.MaxTotalTokens - llmAppBudgetUsageTokens(usage) - reservedTokens
		if snapshot.RemainingTokens < 0 {
			snapshot.RemainingTokens = 0
		}
	}
	if policy.MaxCostUSD > 0 {
		snapshot.RemainingCostUSD = policy.MaxCostUSD - usage.CostUSD - reservedCostUSD
		if snapshot.RemainingCostUSD < 0 {
			snapshot.RemainingCostUSD = 0
		}
	}
	return snapshot
}
