package configvalue

import (
	"fmt"
	"strings"
)

func String(values map[string]interface{}, key, fallback string) string {
	if values == nil {
		return fallback
	}
	value, ok := values[key]
	if !ok {
		return fallback
	}
	switch typed := value.(type) {
	case string:
		return typed
	case fmt.Stringer:
		return typed.String()
	default:
		return fmt.Sprintf("%v", typed)
	}
}

func Bool(values map[string]interface{}, key string, fallback bool) bool {
	if values == nil {
		return fallback
	}
	value, ok := values[key]
	if !ok {
		return fallback
	}
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		switch strings.ToLower(typed) {
		case "true", "1", "yes", "on":
			return true
		case "false", "0", "no", "off":
			return false
		default:
			return fallback
		}
	case int:
		return typed != 0
	case int64:
		return typed != 0
	case float64:
		return typed != 0
	default:
		return fallback
	}
}

func Int(values map[string]interface{}, key string, fallback int) int {
	if values == nil {
		return fallback
	}
	value, ok := values[key]
	if !ok {
		return fallback
	}
	switch typed := value.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	case string:
		var result int
		_, _ = fmt.Sscanf(typed, "%d", &result)
		return result
	default:
		return fallback
	}
}

func Float(values map[string]interface{}, key string, fallback float64) float64 {
	if values == nil {
		return fallback
	}
	value, ok := values[key]
	if !ok {
		return fallback
	}
	switch typed := value.(type) {
	case float64:
		return typed
	case float32:
		return float64(typed)
	case int:
		return float64(typed)
	case int64:
		return float64(typed)
	case string:
		var result float64
		_, _ = fmt.Sscanf(typed, "%f", &result)
		return result
	default:
		return fallback
	}
}

func StringSlice(values map[string]interface{}, key string, fallback []string) []string {
	if values == nil {
		return fallback
	}
	value, ok := values[key]
	if !ok {
		return fallback
	}
	switch typed := value.(type) {
	case []string:
		return typed
	case []interface{}:
		result := make([]string, 0, len(typed))
		for _, item := range typed {
			if text, ok := item.(string); ok {
				result = append(result, text)
			} else {
				result = append(result, fmt.Sprintf("%v", item))
			}
		}
		return result
	default:
		return fallback
	}
}

func Map(values map[string]interface{}, key string, fallback map[string]interface{}) map[string]interface{} {
	if values == nil {
		return fallback
	}
	value, ok := values[key]
	if !ok {
		return fallback
	}
	if typed, ok := value.(map[string]interface{}); ok {
		return typed
	}
	return fallback
}
