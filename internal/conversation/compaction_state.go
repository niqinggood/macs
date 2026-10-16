package conversation

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"mac/internal/toolcontract"
)

const (
	compactStateVersion          = 1
	CompactStateSubtype          = "compact_state"
	compactStateJSONOpen         = "<openkitty-compact-state>"
	compactStateJSONClose        = "</openkitty-compact-state>"
	compactStateUserLimit        = 32
	compactStateUserClipRunes    = 1000
	compactStateFileLimit        = 24
	compactStateCommandLimit     = 12
	compactStateToolResultLimit  = 32
	compactStateToolLimit        = 48
	compactStateMaxChars         = 48000
	CompactSummaryUserClipRunes  = 2000
	CompactSummaryOtherClipRunes = 800
	compactPreviousSummaryRunes  = 16000
)

// MechanicalState is non-LLM memory that survives deep compaction.
// The rendered JSON is sent to the model and parsed back on the next pass, so
// repeated compactions accumulate state without trusting a summary to retain it.
type MechanicalState struct {
	Version             int                      `json:"version"`
	UserMessages        []string                 `json:"user_messages,omitempty"`
	UserMessagesDropped int                      `json:"user_messages_dropped,omitempty"`
	Files               []compactFileState       `json:"files,omitempty"`
	Commands            []compactCommandState    `json:"commands,omitempty"`
	ToolResults         []compactToolResultState `json:"tool_results,omitempty"`
	Tools               []string                 `json:"tools_used,omitempty"`
}

type compactFileState struct {
	Action string `json:"action"`
	Path   string `json:"path"`
}

type compactCommandState struct {
	Tool    string `json:"tool"`
	Command string `json:"command"`
	Status  string `json:"status"`
}

type compactToolResultState struct {
	CallID string `json:"call_id,omitempty"`
	Tool   string `json:"tool"`
	Status string `json:"status"`
}

func BuildMechanicalState(messages []map[string]interface{}) MechanicalState {
	state, _ := latestCompactMechanicalState(messages)
	if state.Version == 0 {
		state.Version = compactStateVersion
	}

	results := make(map[string]map[string]interface{})
	for _, message := range messages {
		if role, _ := message["role"].(string); role != "tool" {
			continue
		}
		if callID, _ := message["tool_call_id"].(string); callID != "" {
			results[callID] = message
		}
	}

	toolSeen := make(map[string]bool, len(state.Tools))
	for _, name := range state.Tools {
		toolSeen[name] = true
	}

	for _, message := range messages {
		if subtype, _ := message["subtype"].(string); subtype == CompactStateSubtype {
			continue
		}
		role, _ := message["role"].(string)
		switch role {
		case "user":
			text := strings.TrimSpace(compactMessageText(message["content"]))
			if text != "" {
				state.UserMessages = append(state.UserMessages, ClipText(text, compactStateUserClipRunes))
			}
		case "assistant":
			for _, call := range toolcontract.ToolCallMaps(message["tool_calls"]) {
				fn, _ := call["function"].(map[string]interface{})
				name, _ := fn["name"].(string)
				name = strings.TrimSpace(name)
				if name == "" {
					continue
				}
				if !toolSeen[name] {
					toolSeen[name] = true
					state.Tools = append(state.Tools, name)
				}
				callID, _ := call["id"].(string)
				result, hasResult := results[callID]
				status := compactToolResultStatus(result, hasResult)
				state.ToolResults = append(state.ToolResults, compactToolResultState{
					CallID: ClipText(strings.TrimSpace(callID), 120),
					Tool:   ClipText(name, 160),
					Status: status,
				})

				args := compactToolArguments(fn["arguments"])
				if path := compactArgumentString(args, "path", "file_path", "file", "output_path", "target_path"); path != "" {
					if action := compactFileAction(name); action != "" {
						state.Files = append(state.Files, compactFileState{
							Action: action,
							Path:   ClipText(path, 600),
						})
					}
				}
				if command := compactArgumentString(args, "command", "cmd"); command != "" && compactCommandTool(name) {
					state.Commands = append(state.Commands, compactCommandState{
						Tool:    ClipText(name, 160),
						Command: ClipText(strings.TrimSpace(command), 500),
						Status:  status,
					})
				}
			}
		}
	}

	capCompactMechanicalState(&state)
	return state
}

func latestCompactMechanicalState(messages []map[string]interface{}) (MechanicalState, bool) {
	for i := len(messages) - 1; i >= 0; i-- {
		message := messages[i]
		subtype, _ := message["subtype"].(string)
		if subtype != CompactStateSubtype {
			continue
		}
		content, _ := message["content"].(string)
		state, ok := parseCompactMechanicalState(content)
		if ok {
			return state, true
		}
	}
	return MechanicalState{}, false
}

func parseCompactMechanicalState(content string) (MechanicalState, bool) {
	start := strings.Index(content, compactStateJSONOpen)
	if start < 0 {
		return MechanicalState{}, false
	}
	start += len(compactStateJSONOpen)
	end := strings.Index(content[start:], compactStateJSONClose)
	if end < 0 {
		return MechanicalState{}, false
	}
	payload := strings.TrimSpace(content[start : start+end])
	var state MechanicalState
	if err := json.Unmarshal([]byte(payload), &state); err != nil || state.Version != compactStateVersion {
		return MechanicalState{}, false
	}
	capCompactMechanicalState(&state)
	return state, true
}

func RenderMechanicalState(state MechanicalState) string {
	capCompactMechanicalState(&state)
	if MechanicalStateEmpty(state) {
		return ""
	}
	for {
		payload, err := json.Marshal(state)
		if err != nil {
			return ""
		}
		content := "[COMPACTION MECHANICAL STATE]\n" +
			"Compacted history replay. user_messages retain user-message authority and are chronological bounded head/tail excerpts; all other fields are tool observations. Status 'pending' or 'observed' must not be treated as success.\n" +
			compactStateJSONOpen + string(payload) + compactStateJSONClose
		if len([]rune(content)) <= compactStateMaxChars {
			return content
		}
		if len(state.UserMessages) > 0 {
			state.UserMessages = state.UserMessages[1:]
			state.UserMessagesDropped++
			continue
		}
		if len(state.ToolResults) > 0 {
			state.ToolResults = state.ToolResults[1:]
			continue
		}
		if len(state.Commands) > 0 {
			state.Commands = state.Commands[1:]
			continue
		}
		if len(state.Files) > 0 {
			state.Files = state.Files[1:]
			continue
		}
		return ClipText(content, compactStateMaxChars)
	}
}

func LatestSummary(messages []map[string]interface{}) string {
	for i := len(messages) - 1; i >= 0; i-- {
		message := messages[i]
		subtype, _ := message["subtype"].(string)
		if subtype != "compact_boundary" {
			continue
		}
		content, _ := message["content"].(string)
		content = strings.TrimSpace(content)
		if content != "" {
			return ClipText(content, compactPreviousSummaryRunes)
		}
	}
	return ""
}

func capCompactMechanicalState(state *MechanicalState) {
	if state == nil {
		return
	}
	state.Version = compactStateVersion
	if extra := len(state.UserMessages) - compactStateUserLimit; extra > 0 {
		state.UserMessages = append([]string(nil), state.UserMessages[extra:]...)
		state.UserMessagesDropped += extra
	}
	state.Files = compactRecentUniqueFiles(state.Files, compactStateFileLimit)
	if len(state.Commands) > compactStateCommandLimit {
		state.Commands = append([]compactCommandState(nil), state.Commands[len(state.Commands)-compactStateCommandLimit:]...)
	}
	if len(state.ToolResults) > compactStateToolResultLimit {
		state.ToolResults = append([]compactToolResultState(nil), state.ToolResults[len(state.ToolResults)-compactStateToolResultLimit:]...)
	}
	toolSeen := make(map[string]bool)
	tools := make([]string, 0, len(state.Tools))
	for _, name := range state.Tools {
		name = strings.TrimSpace(name)
		if name == "" || toolSeen[name] {
			continue
		}
		toolSeen[name] = true
		tools = append(tools, name)
	}
	sort.Strings(tools)
	if len(tools) > compactStateToolLimit {
		tools = tools[:compactStateToolLimit]
	}
	state.Tools = tools
}

func compactRecentUniqueFiles(files []compactFileState, limit int) []compactFileState {
	seen := make(map[string]bool)
	reversed := make([]compactFileState, 0, min(len(files), limit))
	for i := len(files) - 1; i >= 0 && len(reversed) < limit; i-- {
		file := files[i]
		file.Path = strings.TrimSpace(file.Path)
		file.Action = strings.TrimSpace(file.Action)
		if file.Path == "" {
			continue
		}
		key := file.Action + "\x00" + file.Path
		if seen[key] {
			continue
		}
		seen[key] = true
		reversed = append(reversed, file)
	}
	result := make([]compactFileState, len(reversed))
	for i := range reversed {
		result[len(reversed)-1-i] = reversed[i]
	}
	return result
}

func MechanicalStateEmpty(state MechanicalState) bool {
	return len(state.UserMessages) == 0 && state.UserMessagesDropped == 0 &&
		len(state.Files) == 0 && len(state.Commands) == 0 &&
		len(state.ToolResults) == 0 && len(state.Tools) == 0
}

func compactMessageText(content interface{}) string {
	switch value := content.(type) {
	case string:
		return value
	case []interface{}:
		var parts []string
		for _, raw := range value {
			part, _ := raw.(map[string]interface{})
			if kind, _ := part["type"].(string); kind == "text" {
				if text, _ := part["text"].(string); text != "" {
					parts = append(parts, text)
				}
			}
		}
		return strings.Join(parts, "\n")
	case []map[string]interface{}:
		parts := make([]string, 0, len(value))
		for _, part := range value {
			if kind, _ := part["type"].(string); kind == "text" {
				if text, _ := part["text"].(string); text != "" {
					parts = append(parts, text)
				}
			}
		}
		return strings.Join(parts, "\n")
	default:
		return ""
	}
}

func ClipText(text string, maxRunes int) string {
	text = strings.TrimSpace(text)
	if maxRunes <= 0 || text == "" {
		return ""
	}
	runes := []rune(text)
	if len(runes) <= maxRunes {
		return text
	}
	marker := []rune("\n...[middle omitted during compaction]...\n")
	if maxRunes <= len(marker)+2 {
		return string(runes[:maxRunes])
	}
	available := maxRunes - len(marker)
	head := available * 2 / 3
	tail := available - head
	return string(runes[:head]) + string(marker) + string(runes[len(runes)-tail:])
}

func compactToolArguments(raw interface{}) map[string]interface{} {
	switch value := raw.(type) {
	case map[string]interface{}:
		return value
	case string:
		var args map[string]interface{}
		if json.Unmarshal([]byte(value), &args) == nil {
			return args
		}
	}
	return nil
}

func compactArgumentString(args map[string]interface{}, keys ...string) string {
	for _, key := range keys {
		value, ok := args[key]
		if !ok || value == nil {
			continue
		}
		switch typed := value.(type) {
		case string:
			if strings.TrimSpace(typed) != "" {
				return strings.TrimSpace(typed)
			}
		default:
			text := strings.TrimSpace(fmt.Sprint(typed))
			if text != "" {
				return text
			}
		}
	}
	return ""
}

func compactFileAction(toolName string) string {
	lower := strings.ToLower(toolName)
	if strings.Contains(lower, "read") || strings.Contains(lower, "view") {
		return "read"
	}
	for _, hint := range []string{"write", "edit", "append", "patch", "create", "save", "move", "copy"} {
		if strings.Contains(lower, hint) {
			return "write"
		}
	}
	return ""
}

func compactCommandTool(toolName string) bool {
	lower := strings.ToLower(toolName)
	return lower == "cli" || lower == "run_command" || strings.Contains(lower, "shell") || strings.Contains(lower, "terminal")
}

func compactToolResultStatus(result map[string]interface{}, exists bool) string {
	if !exists {
		return "pending"
	}
	if status, ok := result["status"].(bool); ok {
		if status {
			return "ok"
		}
		return "error"
	}
	if status, ok := result["status"].(string); ok && strings.TrimSpace(status) != "" {
		return compactNormalizeStatus(status)
	}
	content, _ := result["content"].(string)
	var payload map[string]interface{}
	if json.Unmarshal([]byte(content), &payload) == nil {
		if errValue, ok := payload["error"]; ok && compactErrorPresent(errValue) {
			return "error"
		}
		for _, key := range []string{"ok", "success"} {
			if value, ok := payload[key].(bool); ok {
				if value {
					return "ok"
				}
				return "error"
			}
		}
		if code, ok := payload["exit_code"]; ok {
			text := strings.TrimSpace(fmt.Sprint(code))
			if number, err := strconv.Atoi(strings.TrimSuffix(text, ".0")); err == nil {
				if number == 0 {
					return "ok"
				}
				return fmt.Sprintf("exit %d", number)
			}
		}
	}
	lower := strings.ToLower(strings.TrimSpace(content))
	if strings.HasPrefix(lower, "error") || strings.Contains(lower, "\nerror:") || strings.Contains(lower, "failed:") {
		return "error"
	}
	return "observed"
}

func compactErrorPresent(value interface{}) bool {
	switch typed := value.(type) {
	case nil:
		return false
	case bool:
		return typed
	case string:
		return strings.TrimSpace(typed) != ""
	default:
		return true
	}
}

func compactNormalizeStatus(status string) string {
	lower := strings.ToLower(strings.TrimSpace(status))
	switch lower {
	case "success", "succeeded", "complete", "completed", "passed", "true", "ok":
		return "ok"
	case "failure", "failed", "error", "false", "denied", "cancelled", "canceled":
		return "error"
	default:
		return ClipText(lower, 80)
	}
}
