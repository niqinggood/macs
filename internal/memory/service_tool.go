package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

func NewAgentMemoryServiceTool(service *Service, owner func(context.Context) (Owner, error), access ToolAccess) *AgentMemoryTool {
	return &AgentMemoryTool{service: service, owner: owner, access: access}
}

func NewSessionContextServiceTool(manager *SessionContextManager, service *Service, owner func(context.Context) (Owner, error), access ToolAccess) *SessionContextTool {
	return &SessionContextTool{manager: manager, service: service, owner: owner, access: access}
}

// The compatibility tool routes durable writes to the same authority as
// agent_memory, while current-session working state remains in SCM.
func (t *SessionContextTool) executeService(ctx context.Context, params map[string]interface{}) (string, error) {
	owner, err := t.owner(ctx)
	if err != nil {
		return "", err
	}
	if err := t.service.Import(ctx, owner); err != nil {
		return "", err
	}
	action := stringParameter(params, "action", "list")
	scope := stringParameter(params, "scope", "visible")
	key := stringParameter(params, "key", "")
	if scope != "visible" && scope != "session" && scope != "durable" {
		return "", fmt.Errorf("unknown context scope")
	}
	// Session writes/deletes keep their existing boundary and failure semantics.
	if action == "set" && scope == "session" || action == "delete" && scope != "durable" || action == "get" && scope == "session" {
		legacy := NewSessionContextToolWithAccess(t.manager, t.access)
		return legacy.Execute(ctx, params)
	}
	if action == "set" {
		if scope == "visible" {
			if _, explicit := params["scope"]; explicit {
				return "", fmt.Errorf("visible is a read scope")
			}
		}
		category := stringParameter(params, "category", "fact")
		kind := category
		memoryScope := MemoryProject
		if category == "preference" {
			memoryScope = MemoryUser
		}
		if category == "todo" || category == "link" {
			kind = "fact"
		}
		value := stringParameter(params, "value", "")
		source, explicit := durableWriteSource(ctx, value)
		state := "candidate"
		if explicit {
			state = "active"
		}
		if err := t.service.RecordWithSource(ctx, owner, key, value, memoryScope, kind, state,
			intParameter(params, "priority", 5), []string{"context:" + category}, 0, source); err != nil {
			return "", err
		}
		if state == "candidate" {
			return fmt.Sprintf("Context proposed for review: **%s** (%s, candidate). It will not be recalled automatically until confirmed.", key, category), nil
		}
		return fmt.Sprintf("Context saved: **%s** (%s, active)", key, category), nil
	}
	if action == "get" {
		if key == "" {
			return "", fmt.Errorf("get requires key")
		}
		if scope != "durable" {
			if item := promptSessionContextSnapshot(t.manager, key, owner.SessionID); item != nil && item.Value != "" {
				block := fmt.Sprintf("**%s**: %s", key, item.Value)
				observePromptContextToolResult(ctx, item, owner.SessionID, block, item.Value)
				return block, nil
			}
		}
		for _, durableScope := range []MemoryScope{MemoryProject, MemoryUser} {
			entry, err := t.service.Lookup(ctx, owner, key, durableScope)
			if err != nil {
				return "", err
			}
			if entry != nil && entry.State == "active" {
				block := fmt.Sprintf("**%s**: %s (v%d)", key, entry.Value, entry.Version)
				observePromptEntryToolResult(ctx, *entry, block, false)
				return block, nil
			}
		}
		return fmt.Sprintf("No context found for key: %s", key), nil
	}
	if action == "delete" {
		if key == "" {
			return "", fmt.Errorf("delete requires key")
		}
		deleted := false
		for _, durableScope := range []MemoryScope{MemoryProject, MemoryUser} {
			found, err := t.service.Forget(ctx, owner, key, durableScope)
			if err != nil {
				return "", err
			}
			deleted = deleted || found
		}
		if !deleted {
			return fmt.Sprintf("No context found for key: %s", key), nil
		}
		return fmt.Sprintf("Context '%s' deleted from long-term memory; original records retained", key), nil
	}
	if action == "inject" {
		result, err := t.service.Recall(ctx, owner, "", DefaultRecallTokens, nil)
		return result.Prompt, err
	}
	if action == "list" {
		entries, err := t.service.List(ctx, owner, "", "", 0)
		if err != nil {
			return "", err
		}
		category := stringParameter(params, "category", "")
		var rows []interface{}
		for _, e := range entries {
			if e.Scope == MemorySession || scope == "session" {
				continue
			}
			if category != "" && !strings.Contains(e.Tags, "context:"+category) && e.Kind != category {
				continue
			}
			rows = append(rows, e)
			if len(rows) == 20 {
				break
			}
		}
		if len(rows) < 20 && scope != "durable" {
			for _, item := range t.manager.ListVisible(category, owner.SessionID, 0) {
				if !item.Durable {
					rows = append(rows, item)
					if len(rows) == 20 {
						break
					}
				}
			}
		}
		data, err := json.Marshal(rows)
		if err == nil {
			for _, row := range rows {
				switch item := row.(type) {
				case Entry:
					observePromptEntryToolResult(ctx, item, string(data), true)
				case *Entry:
					if item != nil {
						observePromptEntryToolResult(ctx, *item, string(data), true)
					}
				case *ContextItem:
					if item != nil {
						value, _ := json.Marshal(item.Value)
						observePromptContextToolResult(ctx, item, owner.SessionID, string(data), string(value))
					}
				}
			}
		}
		return string(data), err
	}
	return "", fmt.Errorf("unknown context action: %s", action)
}

func (t *AgentMemoryTool) executeService(ctx context.Context, params map[string]interface{}) (string, error) {
	if t.owner == nil {
		return "", fmt.Errorf("trusted memory owner resolver is unavailable")
	}
	owner, err := t.owner(ctx)
	if err != nil {
		return "", err
	}
	action := stringParameter(params, "action", "list")
	scope := MemoryScope(stringParameter(params, "scope", ""))
	if t.service.global || scope != "" && !validAgentMemoryScope(scope) {
		return "", fmt.Errorf("shared memory is managed only by the explicit memory manager")
	}
	key := stringParameter(params, "key", "")
	switch action {
	case "confirm":
		if !explicitCandidateConfirmation(ctx, key) {
			return "", fmt.Errorf("confirm requires an explicit user request naming this memory key")
		}
		source, _ := durableWriteSource(ctx, key)
		entry, err := t.service.ConfirmWithSource(ctx, owner, key, scope, intParameter(params, "expected_version", 0), source)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("Confirmed: **%s** (%s, v%d, active)", entry.Key, entry.Scope, entry.Version), nil
	case "remember", "correct":
		if action == "remember" && scope == "" {
			scope = MemorySession
		}
		version := 0
		if action == "correct" {
			version = intParameter(params, "expected_version", 0)
			if version < 1 {
				return "", fmt.Errorf("correct requires expected_version from recall/list")
			}
		}
		value := stringParameter(params, "value", "")
		state := stringParameter(params, "state", "active")
		source := WriteSource{Type: "session_working"}
		if scope != MemorySession {
			var explicit bool
			source, explicit = durableWriteSource(ctx, value)
			if !explicit {
				if action == "correct" && state == "active" {
					return "", fmt.Errorf("correcting active long-term memory requires a remember instruction whose payload exactly matches the new value")
				}
				state = "candidate"
			}
		}
		if err := t.service.RecordWithSource(ctx, owner, key, value, scope,
			stringParameter(params, "kind", ""), state, intParameter(params, "priority", 5),
			strings.Split(stringParameter(params, "tags", ""), ","), version, source); err != nil {
			return "", err
		}
		if action == "correct" {
			return fmt.Sprintf("Corrected: **%s** (%s, v%d, %s)", key, scope, version+1, state), nil
		}
		if state == "candidate" {
			return fmt.Sprintf("Candidate saved for review: **%s** (%s). It will not be recalled automatically until confirmed.", key, scope), nil
		}
		return fmt.Sprintf("Remembered: **%s** (%s, %s)", key, scope, state), nil
	case "forget":
		deleted, err := t.service.Forget(ctx, owner, key, scope)
		if err != nil {
			return "", err
		}
		if !deleted {
			return fmt.Sprintf("No memory found for: %s", key), nil
		}
		return fmt.Sprintf("Forgotten: %s. This removes the long-term entry; original conversation records are retained.", key), nil
	case "snapshot":
		result, err := t.service.Recall(ctx, owner, stringParameter(params, "query", ""), DefaultRecallTokens, nil)
		if err != nil {
			return "", err
		}
		if result.Prompt == "" {
			return "No memories to snapshot.", nil
		}
		return result.Prompt, nil
	case "list", "recall":
		if action == "recall" {
			if key == "" {
				return "", fmt.Errorf("recall requires key")
			}
			entry, err := t.service.Lookup(ctx, owner, key, scope)
			if err != nil {
				return "", err
			}
			if entry == nil {
				return fmt.Sprintf("No memory found for: %s", key), nil
			}
			data, err := json.Marshal(entry)
			if err == nil {
				observePromptEntryToolResult(ctx, *entry, string(data), true)
			}
			return string(data), err
		}
		entries, err := t.service.ListByState(ctx, owner, scope, stringParameter(params, "tags", ""), stringParameter(params, "state", ""), 20)
		if err != nil {
			return "", err
		}
		if len(entries) > 20 {
			entries = entries[:20]
		}
		data, err := json.Marshal(entries)
		if err == nil {
			for _, entry := range entries {
				observePromptEntryToolResult(ctx, entry, string(data), true)
			}
		}
		return string(data), err
	default:
		return "", fmt.Errorf("unknown memory action: %s", action)
	}
}
