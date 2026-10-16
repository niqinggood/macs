package middleware

import "mac/internal/toolcontract"

// DanglingToolCallMiddleware patches message history before each LLM call.
//
// Problem: when a run is interrupted (user cancels, timeout, crash) while the
// LLM returned tool_calls, the conversation has an AIMessage with tool_calls
// but no matching ToolMessage. Next LLM call fails — strict API validators
// require every tool_call_id to have a corresponding tool result message.
//
// Solution: scan history for AIMessages with tool_calls that lack matching
// ToolMessages, and inject synthetic ToolMessages immediately after each
// dangling AIMessage. The synthetic content is "[Interrupted]" so the LLM
// knows the tool never ran and should not trust any expected output.

type DanglingToolCallMiddleware struct {
	Base
}

func NewDanglingToolCallMiddleware() *DanglingToolCallMiddleware {
	return &DanglingToolCallMiddleware{Base: NewBase("dangling_tool_call")}
}

func (m *DanglingToolCallMiddleware) BeforeModel(ctx *Context) error {
	if len(ctx.Messages) == 0 {
		return nil
	}

	// Collect all tool_call_ids that have already been answered
	answered := make(map[string]bool)
	for _, msg := range ctx.Messages {
		role, _ := msg["role"].(string)
		if role == "tool" {
			if id, ok := msg["tool_call_id"].(string); ok && id != "" {
				answered[id] = true
			}
		}
	}

	// Find dangling AIMessages and patch them
	var patched []map[string]interface{}
	for i, msg := range ctx.Messages {
		patched = append(patched, msg)

		role, _ := msg["role"].(string)
		if role != "assistant" {
			continue
		}

		tcList := toolcontract.ToolCallMaps(msg["tool_calls"])
		if len(tcList) == 0 {
			continue
		}

		// Count how many tool_calls are dangling
		var danglingIDs []string
		for _, call := range tcList {
			id, _ := call["id"].(string)
			if id == "" {
				continue
			}
			if !answered[id] {
				danglingIDs = append(danglingIDs, id)
			}
		}

		if len(danglingIDs) == 0 {
			continue
		}

		// Check that next messages aren't already synthetic patches for these IDs
		if i+1 < len(ctx.Messages) {
			nextRole, _ := ctx.Messages[i+1]["role"].(string)
			nextContent, _ := ctx.Messages[i+1]["content"].(string)
			if nextRole == "tool" && nextContent == "[Interrupted]" {
				continue // already patched
			}
		}

		// Insert synthetic ToolMessages for each dangling tool call
		for _, id := range danglingIDs {
			synthetic := map[string]interface{}{
				"role":         "tool",
				"tool_call_id": id,
				"content":      "[Interrupted] This tool call was cancelled before execution. Do not assume it produced any result.",
				"name":         "interrupted",
			}
			patched = append(patched, synthetic)
		}
	}

	ctx.Messages = patched
	return nil
}
