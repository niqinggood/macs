package llmapp

import (
	"context"
	"fmt"
	"strings"
)

func (m *LLMAppManager) processManagedLLMAppItem(
	ctx context.Context,
	run *llmAppManagedRun,
	definition LLMAppDefinition,
	sharedInput string,
	item llmAppWorkItem,
	plan LLMAppRunPlan,
	caller DetailedModelCaller,
) (LLMAppItemResult, error) {
	result := LLMAppItemResult{
		Index: item.Index, ID: item.ID, Metadata: item.Metadata, PlanRevision: plan.Revision,
	}
	if strings.TrimSpace(item.Text) == "" {
		result.Error = "item has no extractable text; OCR or another extractor may be required"
		return result, m.settleItemState(run, item, result)
	}
	if caller == nil {
		result.Error = "model caller is unavailable"
		return result, m.settleItemState(run, item, result)
	}

	userPrompt := renderLLMAppPrompt(definition.PromptTemplate, definition.PromptVariables, sharedInput, item)
	messages := make([]map[string]interface{}, 0, 4)
	if definition.SystemPrompt != "" {
		messages = append(messages, map[string]interface{}{"role": "system", "content": definition.SystemPrompt})
	}
	messages = append(messages, map[string]interface{}{"role": "user", "content": userPrompt})
	validationAttempt := 0
	if state, err := m.readItemState(run.snapshot().RunID, item); err == nil && len(state.Messages) > 0 {
		messages = cloneLLMAppMessages(state.Messages)
		validationAttempt = state.ValidationAttempt
	}

	for ; validationAttempt <= definition.Health.ValidationRetries; validationAttempt++ {
		inputTokens := estimateLLMAppTokens(llmAppMessageCharacters(messages))
		effectID := fmt.Sprintf("%s:model:%s:%d", run.snapshot().RunID, item.ID, validationAttempt)
		if err := m.checkpointItemEffect(
			run, item, plan, validationAttempt, messages, effectID, llmAppReplaySafe,
		); err != nil {
			return result, fmt.Errorf("persist model-call intent: %w", err)
		}
		if err := m.reserveModelCall(run, inputTokens, definition.MaxTokens); err != nil {
			_ = m.checkpointItemRetry(run, item, validationAttempt, messages)
			return result, err
		}

		response, callErr := caller(ctx, definition.Model, messages, definition.MaxTokens, definition.Temperature)
		result.ModelCalls++
		result.InputTokens += response.PromptTokens
		result.OutputTokens += response.CompletionTokens
		if requestID := strings.TrimSpace(response.RequestID); requestID != "" {
			result.ProviderRequestIDs = append(result.ProviderRequestIDs, requestID)
		}
		settleErr := m.settleModelCall(run, inputTokens, definition.MaxTokens, response, callErr)
		if settleErr != nil {
			return result, settleErr
		}
		if callErr != nil {
			result.Error = callErr.Error()
			return result, m.settleItemState(run, item, result)
		}

		output := response.Content
		validationErr := validateLLMAppOutput(output, definition.OutputSchema)
		if validationErr == nil {
			result.Output = output
			if len(definition.OutputSchema) > 0 {
				value, decodeErr := decodeLLMAppStructuredOutput(output)
				if decodeErr != nil {
					result.Error = decodeErr.Error()
					return result, m.settleItemState(run, item, result)
				}
				result.Value = value
			}
			return result, m.settleItemState(run, item, result)
		}
		if validationAttempt == definition.Health.ValidationRetries {
			result.Error = "output validation failed: " + validationErr.Error()
			return result, m.settleItemState(run, item, result)
		}

		messages = append(messages,
			map[string]interface{}{"role": "assistant", "content": output},
			map[string]interface{}{
				"role": "user",
				"content": fmt.Sprintf(
					"The previous output did not satisfy the required JSON schema (%v). Return only one valid JSON value that satisfies it.",
					validationErr,
				),
			},
		)
		if err := m.checkpointItemRetry(run, item, validationAttempt+1, messages); err != nil {
			return result, fmt.Errorf("persist validation retry checkpoint: %w", err)
		}
	}
	return result, m.settleItemState(run, item, result)
}

func llmAppMessageCharacters(messages []map[string]interface{}) int {
	total := 0
	for _, message := range messages {
		total += len(fmt.Sprint(message["role"])) + len(fmt.Sprint(message["content"]))
	}
	return total
}
