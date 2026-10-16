package conversation

import "fmt"

// StripMediaFromMessages replaces large media blocks with compact text markers.
func StripMediaFromMessages(messages []map[string]interface{}) []map[string]interface{} {
	result := make([]map[string]interface{}, len(messages))
	for index, message := range messages {
		content, ok := message["content"].([]interface{})
		if !ok {
			result[index] = message
			continue
		}
		changed := false
		filtered := make([]interface{}, 0, len(content))
		for _, block := range content {
			blockMap, ok := block.(map[string]interface{})
			if !ok {
				filtered = append(filtered, block)
				continue
			}
			blockType, _ := blockMap["type"].(string)
			if blockType == "image" || blockType == "document" || blockType == "image_url" {
				changed = true
				filtered = append(filtered, map[string]interface{}{"type": "text", "text": fmt.Sprintf("[%s]", blockType)})
			} else {
				filtered = append(filtered, block)
			}
		}
		if !changed {
			result[index] = message
			continue
		}
		clone := make(map[string]interface{}, len(message))
		for key, value := range message {
			clone[key] = value
		}
		clone["content"] = filtered
		result[index] = clone
	}
	return result
}
