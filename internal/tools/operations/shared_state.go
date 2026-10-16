package operations

import (
	"context"
	"encoding/json"
	"fmt"

	"mac/internal/runtimeutil"
)

// SharedStateTool lets agents read and update the UI-shared runtime state.
type SharedStateTool struct {
	State *runtimeutil.SharedState
}

func NewSharedStateTool(state *runtimeutil.SharedState) *SharedStateTool {
	return &SharedStateTool{State: state}
}

func (t *SharedStateTool) Name() string { return "shared_state" }

func (t *SharedStateTool) Description() string {
	return "Read and write shared state between the agent and the web UI. " +
		"Use get(action='get') to read a key, set(key='...', value='...') to update. " +
		"The UI reacts to state changes in real-time."
}

func (t *SharedStateTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"action": map[string]interface{}{
				"type": "string", "enum": []string{"get", "set", "merge", "clear"},
				"description": "get (read key or all), set (update key), merge (update multiple), clear (reset)",
			},
			"key": map[string]interface{}{
				"type": "string", "description": "State key to read/write",
			},
			"value": map[string]interface{}{
				"type": "string", "description": "Value to set (JSON string for complex values)",
			},
		},
		"required": []string{"action"},
	}
}

func (t *SharedStateTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	if t == nil || t.State == nil {
		return "shared_state unavailable", nil
	}
	action := stringValue(params, "action", "get")
	key := stringValue(params, "key", "")
	value := stringValue(params, "value", "")

	switch action {
	case "get":
		if key != "" {
			encoded, _ := json.Marshal(t.State.Get(key))
			return fmt.Sprintf("shared_state.%s = %s", key, string(encoded)), nil
		}
		encoded, _ := json.Marshal(t.State.Snapshot())
		return fmt.Sprintf("shared_state = %s", string(encoded)), nil
	case "set":
		if key == "" {
			return "key required", nil
		}
		var parsed interface{}
		if err := json.Unmarshal([]byte(value), &parsed); err != nil {
			parsed = value
		}
		t.State.Set(key, parsed)
		return fmt.Sprintf("shared_state.%s = %v", key, parsed), nil
	case "merge":
		var updates map[string]interface{}
		if err := json.Unmarshal([]byte(value), &updates); err != nil {
			return fmt.Sprintf("invalid JSON: %v", err), nil
		}
		t.State.Merge(updates)
		return "shared_state merged", nil
	case "clear":
		t.State.Clear()
		return "shared_state cleared", nil
	default:
		return "unknown action", nil
	}
}

func stringValue(values map[string]interface{}, key, fallback string) string {
	if value, ok := values[key].(string); ok {
		return value
	}
	return fallback
}
