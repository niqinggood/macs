// Package conversation contains provider-neutral conversation lifecycle helpers.
package conversation

import (
	"fmt"
	"time"
)

// CompactBoundary records one compaction operation.
type CompactBoundary struct {
	Trigger    string `json:"trigger"`
	PreTokens  int    `json:"pre_tokens"`
	PostTokens int    `json:"post_tokens"`
	Timestamp  int64  `json:"timestamp"`
	CompactID  string `json:"compact_id"`
}

func CreateCompactBoundary(trigger string, preTokens int) map[string]interface{} {
	boundary := CompactBoundary{
		Trigger:   trigger,
		PreTokens: preTokens,
		Timestamp: time.Now().Unix(),
		CompactID: fmt.Sprintf("compact_%d", time.Now().UnixNano()),
	}
	return map[string]interface{}{
		"role":    "system",
		"content": fmt.Sprintf("[Conversation compacted: %s. Pre-compaction tokens: %d]", trigger, preTokens),
		"subtype": "compact_boundary",
		"compact_metadata": map[string]interface{}{
			"trigger":    boundary.Trigger,
			"pre_tokens": boundary.PreTokens,
			"compact_id": boundary.CompactID,
		},
	}
}

func IsCompactBoundary(message map[string]interface{}) bool {
	subtype, _ := message["subtype"].(string)
	if subtype == "compact_boundary" {
		return true
	}
	metadata, ok := message["compact_metadata"].(map[string]interface{})
	return ok && metadata != nil
}

func GetMessagesAfterCompactBoundary(messages []map[string]interface{}) []map[string]interface{} {
	for i := len(messages) - 1; i >= 0; i-- {
		if IsCompactBoundary(messages[i]) {
			return messages[i:]
		}
	}
	return messages
}

func CountCompactBoundaries(messages []map[string]interface{}) int {
	count := 0
	for _, message := range messages {
		if IsCompactBoundary(message) {
			count++
		}
	}
	return count
}

func StripCompactBoundaries(messages []map[string]interface{}) []map[string]interface{} {
	result := make([]map[string]interface{}, 0, len(messages))
	for _, message := range messages {
		if !IsCompactBoundary(message) {
			result = append(result, message)
		}
	}
	return result
}
