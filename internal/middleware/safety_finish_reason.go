package middleware

import (
	"strings"
)

// SafetyFinishReasonMiddleware prevents execute-garbage→retry loops caused by
// model providers that safety-terminate a response mid-stream.
//
// Problem: providers (DeepSeek content_filter, or similar) can stop generation
// partway while still returning partially-formed tool_calls. The agent sees
// non-empty tool_calls and dispatches them — but with truncated/malformed args.
// The tool fails, the agent retries, gets filtered again, and loops.
//
// Solution: in AfterModel, detect safety termination signals in the last
// assistant message. If the model was safety-terminated AND the message
// contains tool_calls, strip the tool_calls and append an explanation so
// the agent produces a text answer instead of executing garbage.

type SafetyFinishReasonMiddleware struct {
	Base
}

func NewSafetyFinishReasonMiddleware() *SafetyFinishReasonMiddleware {
	return &SafetyFinishReasonMiddleware{Base: NewBase("safety_finish_reason")}
}

// Safety termination indicators in model responses.
// These patterns match finish_reason values or model output markers.
var safetyTerminationPatterns = []string{
	"content_filter",
	"content_filtered",
	"safety",
	"SAFETY",
	"refusal",
	"blocked",
	"moderation",
}

func (m *SafetyFinishReasonMiddleware) AfterModel(ctx *Context) error {
	if len(ctx.Messages) == 0 {
		return nil
	}

	// Find the last assistant message
	for i := len(ctx.Messages) - 1; i >= 0; i-- {
		msg := ctx.Messages[i]
		role, _ := msg["role"].(string)
		if role != "assistant" {
			continue
		}

		// Check for tool_calls
		toolCallsRaw, hasToolCalls := msg["tool_calls"]
		if !hasToolCalls {
			return nil
		}
		tcList, ok := toolCallsRaw.([]interface{})
		if !ok || len(tcList) == 0 {
			return nil
		}

		// Check for safety termination signal
		if !m.isSafetyTerminated(msg) {
			return nil
		}

		// Strip tool_calls and annotate
		delete(msg, "tool_calls")
		delete(msg, "function_call")

		// Clean up any raw provider tool-call metadata in additional_kwargs
		if ak, ok := msg["additional_kwargs"]; ok {
			if akMap, ok := ak.(map[string]interface{}); ok {
				delete(akMap, "tool_calls")
				delete(akMap, "function_call")
			}
		}

		content, _ := msg["content"].(string)
		if strings.TrimSpace(content) == "" {
			content = "I was unable to complete this response."
		}
		msg["content"] = "[Safety termination — model stopped mid-response] " + content

		// Mark in metadata so downstream middleware / consumers can observe
		msg["safety_terminated"] = true

		ctx.Messages[i] = msg
		break
	}
	return nil
}

func (m *SafetyFinishReasonMiddleware) isSafetyTerminated(msg map[string]interface{}) bool {
	// Check finish_reason in the message metadata
	finishReason, _ := msg["finish_reason"].(string)
	finishReasonLower := strings.ToLower(finishReason)

	for _, pattern := range safetyTerminationPatterns {
		if strings.Contains(finishReasonLower, strings.ToLower(pattern)) {
			return true
		}
	}

	// Also check response_metadata for provider-specific signals
	if rm, ok := msg["response_metadata"]; ok {
		if rmMap, ok := rm.(map[string]interface{}); ok {
			if fr, ok := rmMap["finish_reason"].(string); ok {
				frLower := strings.ToLower(fr)
				for _, pattern := range safetyTerminationPatterns {
					if strings.Contains(frLower, strings.ToLower(pattern)) {
						return true
					}
				}
			}
		}
	}

	return false
}
