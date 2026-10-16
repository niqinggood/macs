package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"mac/internal/fsutil"
)

// MemoryScope defines where a memory is stored and shared.
// Claude Code borrowed: agentMemory.ts — user/project/local scopes
type MemoryScope string

const (
	MemoryUser    MemoryScope = "user"    // durable preferences in this workspace
	MemoryProject MemoryScope = "project" // durable project facts in this workspace
	MemorySession MemoryScope = "session" // current session only
)

// AgentMemory represents a single remembered piece of context.
type AgentMemory struct {
	Key             string      `json:"key"`
	Value           string      `json:"value"`
	Scope           MemoryScope `json:"scope"`
	AgentName       string      `json:"agent_name"`
	Priority        int         `json:"priority"`
	Tags            []string    `json:"tags,omitempty"`
	Version         int         `json:"version"`
	CreatedAt       time.Time   `json:"created_at"`
	UpdatedAt       time.Time   `json:"updated_at"`
	SessionID       string      `json:"session_id"`
	SourceSessionID string      `json:"source_session_id,omitempty"`
	Deleted         bool        `json:"deleted,omitempty"`
}

// AgentMemoryStore manages persistent agent memories across sessions.
type AgentMemoryStore struct {
	*agentMemoryState
	storeDir  string
	agentName string
}

type agentMemoryState struct {
	mu       sync.RWMutex
	memories map[string]*AgentMemory // scope/session/key -> memory
	loadErr  error
}

var agentMemoryStates = struct {
	sync.Mutex
	items map[string]*agentMemoryState
}{items: make(map[string]*agentMemoryState)}

func NewAgentMemoryStore(storeDir, agentName string) *AgentMemoryStore {
	dir := canonicalContextStoreDir(filepath.Join(storeDir, ".openkitty", "memories"))
	ams := &AgentMemoryStore{
		agentMemoryState: &agentMemoryState{memories: make(map[string]*AgentMemory)},
		storeDir:         dir,
		agentName:        agentName,
	}
	if strings.TrimSpace(agentName) == "" || agentName == "." || agentName == ".." || strings.ContainsAny(agentName, `/\:`) {
		ams.loadErr = fmt.Errorf("invalid agent memory name")
		return ams
	}
	agentMemoryStates.Lock()
	defer agentMemoryStates.Unlock()
	path := canonicalContextStoreDir(ams.filePath())
	if state := agentMemoryStates.items[path]; state != nil {
		ams.agentMemoryState = state
	} else {
		ams.load()
		agentMemoryStates.items[path] = ams.agentMemoryState
	}
	return ams
}

func (ams *AgentMemoryStore) filePath() string {
	return filepath.Join(ams.storeDir, ams.agentName+".json")
}

func (ams *AgentMemoryStore) load() {
	data, err := os.ReadFile(ams.filePath())
	if err != nil {
		if !os.IsNotExist(err) {
			ams.loadErr = fmt.Errorf("read agent memory: %w", err)
		}
		return
	}
	var legacy map[string]*AgentMemory
	if err := json.Unmarshal(data, &legacy); err != nil {
		ams.loadErr = fmt.Errorf("decode agent memory: %w", err)
		return
	}
	for oldKey, memory := range legacy {
		if memory == nil {
			continue
		}
		if memory.Key == "" {
			memory.Key = oldKey
		}
		// Unscoped legacy session records remain on disk but are never visible.
		key := agentMemoryStorageKey(memory.Key, memory.Scope, memory.SessionID)
		if !validAgentMemoryScope(memory.Scope) || memory.Scope == MemorySession && memory.SessionID == "" {
			if strings.HasPrefix(oldKey, "legacy:") {
				key = oldKey
			} else {
				key = "legacy:" + oldKey
			}
		}
		ams.memories[key] = memory
	}
}

func validAgentMemoryScope(scope MemoryScope) bool {
	return scope == MemoryUser || scope == MemoryProject || scope == MemorySession
}

func agentMemoryStorageKey(key string, scope MemoryScope, sessionID string) string {
	if scope != MemorySession {
		sessionID = ""
	}
	encoded, _ := json.Marshal([]string{string(scope), sessionID, key})
	return string(encoded)
}

func (ams *AgentMemoryStore) saveLocked() error {
	if ams.loadErr != nil {
		return ams.loadErr // Never replace unreadable or corrupt existing data.
	}
	data, err := json.MarshalIndent(ams.memories, "", "  ")
	if err != nil {
		return fmt.Errorf("encode agent memory: %w", err)
	}
	if err := os.MkdirAll(ams.storeDir, 0755); err != nil {
		return fmt.Errorf("create agent memory directory: %w", err)
	}
	if err := fsutil.WriteAtomic(ams.filePath(), data, 0600); err != nil {
		return fmt.Errorf("persist agent memory: %w", err)
	}
	return nil
}

// Remember stores a new memory or updates an existing one.
func (ams *AgentMemoryStore) Remember(key, value string, scope MemoryScope, priority int, tags []string, sessionID string) error {
	sourceSessionID := sessionID
	if !validAgentMemoryScope(scope) {
		return fmt.Errorf("unknown memory scope: %s", scope)
	}
	if scope == MemorySession && strings.TrimSpace(sessionID) == "" {
		return fmt.Errorf("session memory requires an active session")
	}
	if strings.TrimSpace(key) == "" || strings.TrimSpace(value) == "" {
		return fmt.Errorf("memory key and value are required")
	}
	if scope != MemorySession {
		sessionID = ""
	}
	if priority < 1 {
		priority = 1
	} else if priority > 10 {
		priority = 10
	}
	ams.mu.Lock()
	defer ams.mu.Unlock()

	now := time.Now()
	storageKey := agentMemoryStorageKey(key, scope, sessionID)
	previous := ams.memories[storageKey]
	if previous != nil {
		existing := cloneAgentMemory(previous)
		existing.Value = value
		existing.Priority = priority
		existing.Tags = append([]string(nil), tags...)
		existing.Version++
		existing.UpdatedAt = now
		ams.memories[storageKey] = existing
	} else {
		ams.memories[storageKey] = &AgentMemory{
			Key: key, Value: value, Scope: scope,
			AgentName: ams.agentName, Priority: priority,
			Tags: append([]string(nil), tags...), Version: 1,
			CreatedAt: now, UpdatedAt: now, SessionID: sessionID,
		}
	}
	ams.memories[storageKey].Deleted = false
	ams.memories[storageKey].SourceSessionID = sourceSessionID
	if err := ams.saveLocked(); err != nil {
		if previous == nil {
			delete(ams.memories, storageKey)
		} else {
			ams.memories[storageKey] = previous
		}
		return err
	}
	return nil
}

// CorrectForSession replaces exactly one visible version. No old value is kept
// in a second history store; a stale correction cannot overwrite a newer write.
func (ams *AgentMemoryStore) CorrectForSession(key, value string, scope MemoryScope, sessionID string, expectedVersion int) error {
	if !validAgentMemoryScope(scope) || scope == MemorySession && strings.TrimSpace(sessionID) == "" {
		return fmt.Errorf("correct requires an explicit scope and an active session for session memory")
	}
	if strings.TrimSpace(key) == "" || strings.TrimSpace(value) == "" || expectedVersion < 1 {
		return fmt.Errorf("correct requires key, value, and expected_version from list")
	}
	ams.mu.Lock()
	defer ams.mu.Unlock()
	storageKey := agentMemoryStorageKey(key, scope, sessionID)
	previous := ams.memories[storageKey]
	if previous == nil || previous.Deleted {
		return fmt.Errorf("no active memory found for correction")
	}
	if previous.Version != expectedVersion {
		return fmt.Errorf("memory version changed; list again before correcting")
	}
	updated := cloneAgentMemory(previous)
	updated.Value = value
	updated.Version++
	updated.UpdatedAt = time.Now()
	updated.SourceSessionID = sessionID
	ams.memories[storageKey] = updated
	if err := ams.saveLocked(); err != nil {
		ams.memories[storageKey] = previous
		return err
	}
	return nil
}

// Recall is the compatibility lookup for durable workspace memories only.
func (ams *AgentMemoryStore) Recall(key string) string {
	return ams.RecallForSession(key, "", "")
}

// RecallForSession prefers session, then project, then user when scope is empty.
func (ams *AgentMemoryStore) RecallForSession(key string, scope MemoryScope, sessionID string) string {
	if memory := ams.recallEntryForSession(key, scope, sessionID); memory != nil {
		return memory.Value
	}
	return ""
}

func (ams *AgentMemoryStore) recallEntryForSession(key string, scope MemoryScope, sessionID string) *AgentMemory {
	ams.mu.RLock()
	defer ams.mu.RUnlock()
	for _, candidate := range []MemoryScope{MemorySession, MemoryProject, MemoryUser} {
		if scope != "" && candidate != scope || candidate == MemorySession && sessionID == "" {
			continue
		}
		if memory := ams.memories[agentMemoryStorageKey(key, candidate, sessionID)]; memory != nil && !memory.Deleted {
			return cloneAgentMemory(memory)
		}
	}
	return nil
}

// Forget is the compatibility deletion for an unambiguous durable key.
func (ams *AgentMemoryStore) Forget(key string) error {
	ams.mu.RLock()
	scope := MemoryScope("")
	for _, candidate := range []MemoryScope{MemoryProject, MemoryUser} {
		if memory := ams.memories[agentMemoryStorageKey(key, candidate, "")]; memory != nil && !memory.Deleted {
			if scope != "" {
				ams.mu.RUnlock()
				return fmt.Errorf("ambiguous memory key; specify scope")
			}
			scope = candidate
		}
	}
	ams.mu.RUnlock()
	if scope == "" {
		return nil
	}
	_, err := ams.ForgetForSession(key, scope, "")
	return err
}

func (ams *AgentMemoryStore) ForgetForSession(key string, scope MemoryScope, sessionID string) (bool, error) {
	if !validAgentMemoryScope(scope) || scope == MemorySession && sessionID == "" {
		return false, fmt.Errorf("forget requires a valid scope and an active session for session memory")
	}
	ams.mu.Lock()
	defer ams.mu.Unlock()
	storageKey := agentMemoryStorageKey(key, scope, sessionID)
	previous := ams.memories[storageKey]
	if previous == nil || previous.Deleted {
		return false, nil
	}
	tombstone := cloneAgentMemory(previous)
	tombstone.Value = ""
	tombstone.Tags = nil
	tombstone.SourceSessionID = ""
	tombstone.Deleted = true
	tombstone.Version++
	tombstone.UpdatedAt = time.Now()
	ams.memories[storageKey] = tombstone
	if err := ams.saveLocked(); err != nil {
		ams.memories[storageKey] = previous
		return false, err
	}
	return true, nil
}

// List returns memories filtered by scope and tags.
func (ams *AgentMemoryStore) List(scope MemoryScope, tag string, limit int) []*AgentMemory {
	return ams.ListForSession(scope, tag, limit, "")
}

func cloneAgentMemory(memory *AgentMemory) *AgentMemory {
	copy := *memory
	copy.Tags = append([]string(nil), memory.Tags...)
	return &copy
}

func (ams *AgentMemoryStore) ListForSession(scope MemoryScope, tag string, limit int, sessionID string) []*AgentMemory {
	ams.mu.RLock()
	defer ams.mu.RUnlock()

	var result []*AgentMemory
	for _, m := range ams.memories {
		if m == nil || m.Deleted || !validAgentMemoryScope(m.Scope) || m.Scope == MemorySession && (sessionID == "" || m.SessionID != sessionID) {
			continue
		}
		if scope != "" && m.Scope != scope {
			continue
		}
		if tag != "" {
			found := false
			for _, t := range m.Tags {
				if t == tag {
					found = true
					break
				}
			}
			if !found {
				continue
			}
		}
		result = append(result, cloneAgentMemory(m))
	}

	sort.Slice(result, func(i, j int) bool {
		if result[i].Priority != result[j].Priority {
			return result[i].Priority > result[j].Priority
		}
		if result[i].Scope != result[j].Scope {
			return result[i].Scope < result[j].Scope
		}
		return result[i].Key < result[j].Key
	})

	if limit > 0 && len(result) > limit {
		result = result[:limit]
	}
	return result
}

// Snapshot creates a context prompt from the most relevant memories.
func (ams *AgentMemoryStore) Snapshot(maxItems int) string {
	return ams.SnapshotForSession(maxItems, "")
}

func (ams *AgentMemoryStore) SnapshotForSession(maxItems int, sessionID string) string {
	memories := ams.ListForSession("", "", maxItems, sessionID)
	if len(memories) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("## Agent Memory (persisted across sessions)\n")
	sb.WriteString("The following memories are visible in the current workspace and session:\n\n")

	scopeLabels := map[MemoryScope]string{
		MemoryUser:    "User preferences (workspace)",
		MemoryProject: "Project",
		MemorySession: "Session",
	}

	currentScope := MemoryScope("")
	for _, m := range memories {
		if m.Scope != currentScope {
			currentScope = m.Scope
			sb.WriteString(fmt.Sprintf("### %s\n", scopeLabels[currentScope]))
		}
		sb.WriteString(fmt.Sprintf("- **%s**: %s", m.Key, m.Value))
		if len(m.Tags) > 0 {
			sb.WriteString(fmt.Sprintf(" [%s]", strings.Join(m.Tags, ", ")))
		}
		sb.WriteString("\n")
	}
	sb.WriteString("\nYou can update these memories using the `agent_memory` tool.\n")
	return sb.String()
}

// Diff detects memories that changed between versions.
func (ams *AgentMemoryStore) Diff(other *AgentMemoryStore) []string {
	mine, theirs := ams.allMemories(), other.allMemories()
	var changes []string
	for key, otherMem := range theirs {
		if my, ok := mine[key]; ok {
			if otherMem.Version > my.Version {
				changes = append(changes, fmt.Sprintf("updated: %s (v%d → v%d)", key, my.Version, otherMem.Version))
			}
		} else {
			changes = append(changes, fmt.Sprintf("new: %s", key))
		}
	}
	sort.Strings(changes)
	return changes
}

func (ams *AgentMemoryStore) allMemories() map[string]*AgentMemory {
	ams.mu.RLock()
	defer ams.mu.RUnlock()
	result := make(map[string]*AgentMemory, len(ams.memories))
	for key, memory := range ams.memories {
		result[key] = cloneAgentMemory(memory)
	}
	return result
}

// Merge imports memories from another store (higher version wins).
func (ams *AgentMemoryStore) Merge(other *AgentMemoryStore) error {
	if ams.agentMemoryState == other.agentMemoryState {
		return nil
	}
	memories := other.allMemories()
	ams.mu.Lock()
	defer ams.mu.Unlock()
	previous := ams.memories
	merged := make(map[string]*AgentMemory, len(previous)+len(memories))
	for key, memory := range previous {
		merged[key] = memory
	}
	for key, otherMem := range memories {
		if myMem, ok := ams.memories[key]; ok {
			// Import/merge is not a new user request to remember a deleted key.
			if !myMem.Deleted && otherMem.Version > myMem.Version {
				merged[key] = otherMem
			}
		} else {
			merged[key] = otherMem
		}
	}
	ams.memories = merged
	if err := ams.saveLocked(); err != nil {
		ams.memories = previous
		return err
	}
	return nil
}

// ==================== agent_memory tool ====================

type AgentMemoryTool struct {
	store     *AgentMemoryStore
	sessionID string
	access    ToolAccess
	service   *Service
	owner     func(context.Context) (Owner, error)
}

func NewAgentMemoryTool(store *AgentMemoryStore, sessionID string) *AgentMemoryTool {
	return &AgentMemoryTool{store: store, sessionID: sessionID}
}

func NewAgentMemoryToolWithAccess(store *AgentMemoryStore, sessionID string, access ToolAccess) *AgentMemoryTool {
	return &AgentMemoryTool{store: store, sessionID: sessionID, access: access}
}

func stringParameter(params map[string]interface{}, key, fallback string) string {
	if value, ok := params[key].(string); ok {
		return value
	}
	return fallback
}

func intParameter(params map[string]interface{}, key string, fallback int) int {
	switch value := params[key].(type) {
	case int:
		return value
	case float64:
		return int(value)
	default:
		return fallback
	}
}

func truncateValue(value string, maximum int) string {
	runes := []rune(value)
	if maximum <= 0 || len(runes) <= maximum {
		return value
	}
	return string(runes[:maximum]) + "..."
}

func (t *AgentMemoryTool) Name() string { return "agent_memory" }

func (t *AgentMemoryTool) Description() string {
	description := "Manage persistent agent memory across sessions. " +
		"Scopes: user (durable preferences in this workspace), project (durable project facts), session (current session only). " +
		"Specify scope explicitly when forgetting or correcting. Correct requires the version returned by recall or list. Session memory takes precedence on recall. " +
		"Use this to remember user preferences, project decisions, important facts, and action items across conversations. " +
		"Use state=candidate for inferred information; save durable active memory only for explicit long-term requirements or verified facts. Temporary requirements belong to session scope."
	if t.service != nil {
		description += " Durable tool writes become active only when this user turn starts with a remember instruction whose payload exactly matches the value; other writes remain candidates. Inspect pending candidates with list state=candidate. Confirm requires a user request naming the key, explicit scope and expected_version. A candidate must not replace an active or forgotten key. Snapshot selects relevant memory using query; without query it only includes working state and user-pinned preferences. Use list or recall to inspect stored entries. Pinning is managed by the user, not this tool."
	}
	return description
}

func (t *AgentMemoryTool) Parameters() map[string]interface{} {
	actions := []string{"remember", "correct", "recall", "forget", "list", "snapshot"}
	if t.service != nil {
		actions = append(actions, "confirm")
	}
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"action": map[string]interface{}{
				"type":        "string",
				"description": "Action: remember (save), correct (replace an existing version), recall (get by key), forget (delete), list (all), snapshot (context prompt). Database service also supports confirm (activate an inspected candidate without changing its content).",
				"enum":        actions,
			},
			"key": map[string]interface{}{
				"type": "string", "description": "Memory key/name",
			},
			"value": map[string]interface{}{
				"type": "string", "description": "Memory value/content",
			},
			"scope": map[string]interface{}{
				"type": "string", "description": "user (workspace preferences), project (workspace facts), session (current). Required for forget; remember defaults to session; recall/list default to visible scopes.",
				"enum": []string{"user", "project", "session"},
			},
			"priority": map[string]interface{}{
				"type": "integer", "description": "Priority 1-10, default 5",
				"minimum": 1, "maximum": 10,
			},
			"expected_version": map[string]interface{}{
				"type": "integer", "minimum": 1, "description": "Required for correct/confirm: current version returned by recall/list; stale versions are rejected.",
			},
			"tags": map[string]interface{}{
				"type": "string", "description": "Comma-separated tags for filtering",
			},
			"kind":  map[string]interface{}{"type": "string", "enum": []string{"preference", "fact", "decision", "experience"}},
			"state": map[string]interface{}{"type": "string", "enum": []string{"active", "candidate"}, "description": "Write state, or optional list filter. Inferred memories must be candidates; candidates are excluded from automatic recall. Database-backed memory only."},
			"query": map[string]interface{}{"type": "string", "description": "Optional question for a relevant snapshot."},
		},
		"required": []string{"action"},
	}
}

func (t *AgentMemoryTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	action := stringParameter(params, "action", "list")
	if !memoryToolAllowed(t.access, ctx, action == "forget") {
		return "", fmt.Errorf("memory is disabled for this execution; only forget is available")
	}
	if t.service != nil {
		return t.executeService(ctx, params)
	}
	if t.store == nil {
		return "Agent memory store not available", nil
	}
	t.store.mu.RLock()
	loadErr := t.store.loadErr
	t.store.mu.RUnlock()
	if loadErr != nil {
		return "", loadErr
	}
	sessionID := t.sessionID
	if ctx != nil {
		if current, ok := ctx.Value("session_id").(string); ok {
			sessionID = current
		}
	}
	sessionID = strings.TrimSpace(sessionID)
	scope := MemoryScope(stringParameter(params, "scope", ""))
	if scope != "" && !validAgentMemoryScope(scope) {
		return "", fmt.Errorf("unknown memory scope: %s", scope)
	}

	switch action {
	case "correct":
		key := stringParameter(params, "key", "")
		value := stringParameter(params, "value", "")
		version := intParameter(params, "expected_version", 0)
		if err := t.store.CorrectForSession(key, value, scope, sessionID, version); err != nil {
			return "", err
		}
		return fmt.Sprintf("Corrected: **%s** (%s, v%d)", key, scope, version+1), nil
	case "remember":
		key := stringParameter(params, "key", "")
		value := stringParameter(params, "value", "")
		if key == "" || value == "" {
			return "Error: key and value are required", nil
		}
		if scope == "" {
			scope = MemorySession
		}
		priority := intParameter(params, "priority", 5)
		if priority < 1 {
			priority = 1
		} else if priority > 10 {
			priority = 10
		}
		tags := strings.Split(stringParameter(params, "tags", ""), ",")
		for i := range tags {
			tags[i] = strings.TrimSpace(tags[i])
		}
		if err := t.store.Remember(key, value, scope, priority, tags, sessionID); err != nil {
			return "", err
		}
		return fmt.Sprintf("Remembered: **%s** (%s, p%d)", key, scope, priority), nil

	case "recall":
		key := stringParameter(params, "key", "")
		if key == "" {
			return "Error: key is required", nil
		}
		memory := t.store.recallEntryForSession(key, scope, sessionID)
		if memory == nil {
			return fmt.Sprintf("No memory found for: %s", key), nil
		}
		return fmt.Sprintf("**%s**: %s (%s, v%d, source session=%s)", key, memory.Value, memory.Scope, memory.Version, memory.SourceSessionID), nil

	case "forget":
		key := stringParameter(params, "key", "")
		if key == "" {
			return "Error: key is required", nil
		}
		if scope == "" {
			return "", fmt.Errorf("forget requires an explicit scope: user, project, or session")
		}
		deleted, err := t.store.ForgetForSession(key, scope, sessionID)
		if err != nil {
			return "", err
		}
		if !deleted {
			return fmt.Sprintf("No memory found for: %s", key), nil
		}
		return fmt.Sprintf("Forgotten: %s", key), nil

	case "list":
		tag := stringParameter(params, "tags", "")
		memories := t.store.ListForSession(scope, tag, 20, sessionID)
		if len(memories) == 0 {
			return "No memories stored. Use 'remember' to save context.", nil
		}
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("Agent Memories (%d):\n", len(memories)))
		for _, m := range memories {
			sb.WriteString(fmt.Sprintf("- [%s] **%s** = %s (v%d, p%d, source session=%s)\n",
				m.Scope, m.Key, truncateValue(m.Value, 80), m.Version, m.Priority, m.SourceSessionID))
		}
		return sb.String(), nil

	case "snapshot":
		prompt := t.store.SnapshotForSession(15, sessionID)
		if prompt == "" {
			return "No memories to snapshot.", nil
		}
		return prompt, nil
	}
	return fmt.Sprintf("Unknown action: %s", action), nil
}
