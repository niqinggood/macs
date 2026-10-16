package conversation

import "time"

// MicroCompaction replaces old tool results without invoking an LLM.
type MicroCompaction struct {
	window time.Duration
}

func NewMicroCompaction(window time.Duration) *MicroCompaction {
	if window <= 0 {
		window = 10 * time.Minute
	}
	return &MicroCompaction{window: window}
}

func (compaction *MicroCompaction) Compact(messages []map[string]interface{}) int {
	now := time.Now()
	compacted := 0
	for i, message := range messages {
		role, _ := message["role"].(string)
		if role != "tool" {
			continue
		}
		timestamp, exists := message["_timestamp"].(time.Time)
		if !exists {
			messages[i]["_timestamp"] = now
			continue
		}
		if now.Sub(timestamp) > compaction.window {
			toolName, _ := message["name"].(string)
			callID, _ := message["tool_call_id"].(string)
			messages[i] = map[string]interface{}{
				"role":         "tool",
				"tool_call_id": callID,
				"name":         toolName,
				"content":      "[Old tool result content cleared after " + formatDuration(now.Sub(timestamp)) + "]",
				"status":       true,
				"_compacted":   true,
				"_timestamp":   timestamp,
			}
			compacted++
		}
	}
	return compacted
}

func (compaction *MicroCompaction) ShouldCompact(messages []map[string]interface{}) bool {
	now := time.Now()
	for _, message := range messages {
		if timestamp, ok := message["_timestamp"].(time.Time); ok && now.Sub(timestamp) > compaction.window {
			return true
		}
	}
	return false
}

func StampToolResults(messages []map[string]interface{}) {
	now := time.Now()
	for i, message := range messages {
		role, _ := message["role"].(string)
		if role == "tool" {
			if _, exists := message["_timestamp"]; !exists {
				messages[i]["_timestamp"] = now
			}
		}
	}
}

func formatDuration(duration time.Duration) string {
	if duration < time.Minute {
		return duration.Truncate(time.Second).String()
	}
	if duration < time.Hour {
		return duration.Truncate(time.Minute).String()
	}
	return duration.Truncate(time.Hour).String()
}
