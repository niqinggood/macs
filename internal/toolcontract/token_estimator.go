package toolcontract

import "mac/internal/runtimeutil"

func EstimateMessageTokens(messages []map[string]interface{}) int {
	total := 0
	for _, message := range messages {
		if content, ok := message["content"].(string); ok {
			total += runtimeutil.EstimateTokens(content)
		}
		if reasoning, ok := message["reasoning_content"].(string); ok {
			total += runtimeutil.EstimateTokens(reasoning)
		}
		if calls, ok := message["tool_calls"]; ok {
			total += len(ToolCallMaps(calls)) * 50
		}
	}
	return total
}
