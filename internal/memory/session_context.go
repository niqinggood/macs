package memory

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"mac/internal/configvalue"
	"mac/internal/fsutil"
	"mac/internal/portablepath"
	"mac/internal/textutil"
)

// ContextItem is a single piece of persistent context.
type ContextItem struct {
	Key       string    `json:"key"`
	Value     string    `json:"value"`
	Category  string    `json:"category"` // "preference", "decision", "fact", "todo", "link"
	Priority  int       `json:"priority"` // higher = more important
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	SessionID string    `json:"session_id"`
	Durable   bool      `json:"durable,omitempty"`
}

// SessionContextManager persists important context across sessions.
// Claude Code borrowed: memdir system + auto-memory
type SessionContextManager struct {
	mu        sync.RWMutex
	items     map[string]*ContextItem
	storeDir  string
	workspace string
	lastErr   error
	deriveSem chan struct{}
}

// durableContextSessionID is retained only for migrating files written by the
// short-lived sentinel-based format. Runtime callers must never gain durable
// access merely by choosing this public session ID.
const durableContextSessionID = "__durable__"

const (
	contextStorageVersionPrefix = "v2:"
	contextPromptDefaultLimit   = 10
)

var sessionContextManagerRegistry = struct {
	sync.Mutex
	byStoreDir map[string]*SessionContextManager
}{byStoreDir: make(map[string]*SessionContextManager)}

func NewSessionContextManager(storeDir string) *SessionContextManager {
	if abs, err := filepath.Abs(filepath.Clean(storeDir)); err == nil {
		storeDir = abs
	}
	mkdirErr := os.MkdirAll(storeDir, 0755)
	storeDir = canonicalContextStoreDir(storeDir)
	sessionContextManagerRegistry.Lock()
	defer sessionContextManagerRegistry.Unlock()
	if existing := sessionContextManagerRegistry.byStoreDir[storeDir]; existing != nil {
		return existing
	}

	scm := &SessionContextManager{
		items:     make(map[string]*ContextItem),
		storeDir:  storeDir,
		deriveSem: make(chan struct{}, 1),
	}
	if mkdirErr != nil {
		scm.lastErr = fmt.Errorf("create session context directory: %w", mkdirErr)
		log.Printf("[SessionContext] %v", scm.lastErr)
	} else if err := scm.load(); err != nil {
		scm.lastErr = err
		log.Printf("[SessionContext] load failed: %v", err)
	}
	sessionContextManagerRegistry.byStoreDir[storeDir] = scm
	return scm
}

func canonicalContextStoreDir(storeDir string) string {
	if abs, err := filepath.Abs(filepath.Clean(storeDir)); err == nil {
		storeDir = abs
	} else {
		storeDir = filepath.Clean(storeDir)
	}
	if resolved, err := filepath.EvalSymlinks(storeDir); err == nil {
		storeDir = resolved
	}
	if runtime.GOOS == "windows" {
		storeDir = strings.ToLower(storeDir)
	}
	return storeDir
}

func (scm *SessionContextManager) SetWorkspace(workspace string) {
	scm.mu.Lock()
	defer scm.mu.Unlock()
	scm.workspace = normalizeContextPath(workspace)
}

func (scm *SessionContextManager) load() error {
	scm.mu.Lock()
	defer scm.mu.Unlock()

	path := filepath.Join(scm.storeDir, "context.json")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read %s: %w", path, err)
	}
	var persisted map[string]*ContextItem
	if err := json.Unmarshal(data, &persisted); err != nil {
		return fmt.Errorf("decode %s: %w", path, err)
	}

	migrated := make(map[string]*ContextItem, len(persisted))
	changed := false
	for persistedKey, item := range persisted {
		if item == nil {
			changed = true
			continue
		}

		logicalKey := item.Key
		sessionID := item.SessionID
		durable := item.Durable
		decodedSession, decodedKey, decodedDurable, compositeKey := decodeContextStorageKey(persistedKey)
		if compositeKey && item.SessionID == decodedSession && item.Key == decodedKey && item.Durable == decodedDurable {
			// The composite key is authoritative. In particular, a forged public
			// session named "__durable__" stays session-scoped after reload.
			sessionID, logicalKey, durable = decodedSession, decodedKey, decodedDurable
		} else {
			if logicalKey == "" && persistedKey != "" {
				logicalKey = persistedKey
			}
			// Migrate both historical explicit-tool encodings. Automatic legacy
			// keys remain non-durable and therefore cannot leak into new sessions.
			legacyItem := *item
			legacyItem.Key = logicalKey
			if sessionID == durableContextSessionID || (sessionID == "" && !isLegacyUnscopedAutomaticContextItem(&legacyItem)) {
				durable = true
				sessionID = ""
			}
			changed = true
		}

		copyItem := *item
		copyItem.Key = logicalKey
		copyItem.SessionID = sessionID
		copyItem.Durable = durable
		storageKey := contextStorageKey(logicalKey, sessionID, durable)
		if existing := migrated[storageKey]; existing == nil || copyItem.UpdatedAt.After(existing.UpdatedAt) {
			migrated[storageKey] = &copyItem
		}
	}
	scm.items = migrated
	if changed {
		if err := scm.saveLocked(); err != nil {
			return fmt.Errorf("persist migrated session context: %w", err)
		}
	}
	return nil
}

// saveLocked writes items to disk. Caller must hold scm.mu write lock.
func (scm *SessionContextManager) saveLocked() error {
	path := filepath.Join(scm.storeDir, "context.json")
	data, err := json.MarshalIndent(scm.items, "", "  ")
	if err != nil {
		return fmt.Errorf("encode context: %w", err)
	}
	if err := os.MkdirAll(scm.storeDir, 0755); err != nil {
		return fmt.Errorf("create context directory: %w", err)
	}
	tmp, err := os.CreateTemp(scm.storeDir, ".context-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary context file: %w", err)
	}
	tmpPath := tmp.Name()
	keepTemp := true
	defer func() {
		if keepTemp {
			_ = os.Remove(tmpPath)
		}
	}()
	if err := tmp.Chmod(0644); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("chmod temporary context file: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temporary context file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync temporary context file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temporary context file: %w", err)
	}
	if err := fsutil.RenameAtomicTempFile(tmpPath, path); err != nil {
		return fmt.Errorf("atomically replace context file: %w", err)
	}
	keepTemp = false
	return nil
}

func (scm *SessionContextManager) persistLocked() error {
	err := scm.saveLocked()
	scm.lastErr = err
	if err != nil {
		log.Printf("[SessionContext] persist failed: %v", err)
	}
	return err
}

func (scm *SessionContextManager) LastError() error {
	scm.mu.RLock()
	defer scm.mu.RUnlock()
	return scm.lastErr
}

// derivationSemaphore bounds background memory derivation across every agent
// sharing this workspace manager. Busy derivations are skipped by Generate.
func (scm *SessionContextManager) derivationSemaphore() chan struct{} {
	return scm.deriveSem
}

func (scm *SessionContextManager) DerivationSemaphore() chan struct{} {
	if scm == nil {
		return nil
	}
	return scm.derivationSemaphore()
}

func (scm *SessionContextManager) StoreDir() string {
	if scm == nil {
		return ""
	}
	return scm.storeDir
}

func contextStorageKey(key, sessionID string, durable bool) string {
	encodedKey := base64.RawURLEncoding.EncodeToString([]byte(key))
	if durable {
		return contextStorageVersionPrefix + "d:" + encodedKey
	}
	encodedSession := base64.RawURLEncoding.EncodeToString([]byte(sessionID))
	return contextStorageVersionPrefix + "s:" + encodedSession + ":" + encodedKey
}

func decodeContextStorageKey(storageKey string) (sessionID, key string, durable bool, ok bool) {
	parts := strings.Split(storageKey, ":")
	if len(parts) == 3 && parts[0] == "v2" && parts[1] == "d" {
		decodedKey, err := base64.RawURLEncoding.DecodeString(parts[2])
		if err != nil {
			return "", "", false, false
		}
		return "", string(decodedKey), true, true
	}
	if len(parts) == 4 && parts[0] == "v2" && parts[1] == "s" {
		decodedSession, errSession := base64.RawURLEncoding.DecodeString(parts[2])
		decodedKey, errKey := base64.RawURLEncoding.DecodeString(parts[3])
		if errSession != nil || errKey != nil {
			return "", "", false, false
		}
		return string(decodedSession), string(decodedKey), false, true
	}
	return "", "", false, false
}

// Set stores an automatic, session-scoped context item. Session identifiers are
// data, never capabilities: even "__durable__" remains an ordinary session.
func (scm *SessionContextManager) Set(key, value, category string, priority int, sessionID string) error {
	return scm.set(key, value, category, priority, sessionID, false)
}

// SetDurable stores context explicitly intended to cross session boundaries.
func (scm *SessionContextManager) SetDurable(key, value, category string, priority int) error {
	return scm.set(key, value, category, priority, "", true)
}

func (scm *SessionContextManager) set(key, value, category string, priority int, sessionID string, durable bool) error {
	scm.mu.Lock()
	defer scm.mu.Unlock()

	now := time.Now()
	storageKey := contextStorageKey(key, sessionID, durable)
	if existing, ok := scm.items[storageKey]; ok {
		existing.Value = value
		existing.Category = category
		existing.UpdatedAt = now
		existing.Priority = priority
		existing.SessionID = sessionID
		existing.Durable = durable
	} else {
		scm.items[storageKey] = &ContextItem{
			Key: key, Value: value, Category: category,
			Priority: priority, CreatedAt: now, UpdatedAt: now,
			SessionID: sessionID, Durable: durable,
		}
	}
	return scm.persistLocked()
}

// Get is the administrative compatibility lookup. Scoped runtime code must use
// GetVisible/GetForSession so it cannot observe another session's value.
func (scm *SessionContextManager) Get(key string) string {
	scm.mu.RLock()
	defer scm.mu.RUnlock()
	if item := scm.items[contextStorageKey(key, "", true)]; item != nil {
		return item.Value
	}
	var newest *ContextItem
	for _, item := range scm.items {
		if item == nil || item.Key != key {
			continue
		}
		if newest == nil || item.UpdatedAt.After(newest.UpdatedAt) {
			newest = item
		}
	}
	if newest != nil {
		return newest.Value
	}
	return ""
}

// GetForSession returns the current session's value, falling back to an
// explicitly durable value with the same key.
func (scm *SessionContextManager) GetForSession(key, sessionID string) string {
	scm.mu.RLock()
	defer scm.mu.RUnlock()
	if item := scm.items[contextStorageKey(key, sessionID, false)]; item != nil {
		return item.Value
	}
	if item := scm.items[contextStorageKey(key, "", true)]; item != nil {
		return item.Value
	}
	return ""
}

func (scm *SessionContextManager) GetSession(key, sessionID string) string {
	scm.mu.RLock()
	defer scm.mu.RUnlock()
	if item := scm.items[contextStorageKey(key, sessionID, false)]; item != nil {
		return item.Value
	}
	return ""
}

// GetVisible is retained as the prompt-facing name.
func (scm *SessionContextManager) GetVisible(key, sessionID string) string {
	return scm.GetForSession(key, sessionID)
}

func (scm *SessionContextManager) GetDurable(key string) string {
	scm.mu.RLock()
	defer scm.mu.RUnlock()
	if item := scm.items[contextStorageKey(key, "", true)]; item != nil {
		return item.Value
	}
	return ""
}

// Delete is the administrative compatibility operation and removes every
// scope for a logical key. Runtime tools must use the scoped delete methods.
func (scm *SessionContextManager) Delete(key string) error {
	scm.mu.Lock()
	defer scm.mu.Unlock()
	for storageKey, item := range scm.items {
		if item != nil && item.Key == key {
			delete(scm.items, storageKey)
		}
	}
	return scm.persistLocked()
}

func (scm *SessionContextManager) DeleteForSession(key, sessionID string) (bool, error) {
	return scm.deleteStorageKey(contextStorageKey(key, sessionID, false))
}

func (scm *SessionContextManager) DeleteDurable(key string) (bool, error) {
	return scm.deleteStorageKey(contextStorageKey(key, "", true))
}

// DeleteVisible is intentionally session-only. Reading may fall back to a
// durable value, but deleting durable context always requires explicit scope.
func (scm *SessionContextManager) DeleteVisible(key, sessionID string) (bool, error) {
	return scm.DeleteForSession(key, sessionID)
}

func (scm *SessionContextManager) deleteStorageKey(storageKey string) (bool, error) {
	scm.mu.Lock()
	defer scm.mu.Unlock()
	if _, ok := scm.items[storageKey]; !ok {
		return false, nil
	}
	delete(scm.items, storageKey)
	return true, scm.persistLocked()
}

// List returns items filtered by category, sorted by priority.
func (scm *SessionContextManager) List(category string, limit int) []*ContextItem {
	scm.mu.RLock()
	defer scm.mu.RUnlock()

	var items []*ContextItem
	for _, item := range scm.items {
		if item != nil && (category == "" || item.Category == category) {
			items = append(items, cloneContextItem(item))
		}
	}
	sortContextItems(items)
	if limit > 0 && len(items) > limit {
		items = items[:limit]
	}
	return items
}

// ListVisible returns explicitly durable context plus automatic context owned
// by the current session. A session-local value overrides a durable value with
// the same logical key.
func (scm *SessionContextManager) ListVisible(category, sessionID string, limit int) []*ContextItem {
	scm.mu.RLock()
	defer scm.mu.RUnlock()

	selected := make(map[string]*ContextItem)
	for _, item := range scm.items {
		if item == nil || (category != "" && item.Category != category) {
			continue
		}
		if !item.Durable && item.SessionID != sessionID {
			continue
		}
		copyItem := cloneContextItem(item)
		if existing := selected[item.Key]; existing == nil || (!copyItem.Durable && existing.Durable) {
			selected[item.Key] = copyItem
		}
	}
	items := make([]*ContextItem, 0, len(selected))
	for _, item := range selected {
		items = append(items, item)
	}
	sortContextItems(items)
	if limit > 0 && len(items) > limit {
		items = items[:limit]
	}
	return items
}

func cloneContextItem(item *ContextItem) *ContextItem {
	if item == nil {
		return nil
	}
	copyItem := *item
	return &copyItem
}

func sortContextItems(items []*ContextItem) {
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Priority != items[j].Priority {
			return items[i].Priority > items[j].Priority
		}
		if !items[i].UpdatedAt.Equal(items[j].UpdatedAt) {
			return items[i].UpdatedAt.After(items[j].UpdatedAt)
		}
		if items[i].Key != items[j].Key {
			return items[i].Key < items[j].Key
		}
		return items[i].SessionID < items[j].SessionID
	})
}

func isLegacyUnscopedAutomaticContextItem(item *ContextItem) bool {
	if item == nil || item.SessionID != "" || item.Durable {
		return false
	}
	key := strings.ToLower(strings.TrimSpace(item.Key))
	return key == "project_path" || strings.HasPrefix(key, "preference_") || strings.HasPrefix(key, "remembered_")
}

// BuildContextPrompt generates a system prompt snippet with relevant context.
func (scm *SessionContextManager) BuildContextPrompt(maxItems int) string {
	return scm.BuildContextPromptForSession("", maxItems)
}

// BuildContextPromptForSession keeps task-local facts inside their originating
// session while retaining explicitly stored durable context.
func (scm *SessionContextManager) BuildContextPromptForSession(sessionID string, maxItems int) string {
	return scm.buildContextPrompt(sessionID, maxItems, false)
}

// BuildSessionOnlyPrompt leaves durable facts to the unified memory service.
func (scm *SessionContextManager) BuildSessionOnlyPrompt(sessionID string, maxItems int) string {
	return scm.buildContextPrompt(sessionID, maxItems, true)
}

func (scm *SessionContextManager) buildContextPrompt(sessionID string, maxItems int, sessionOnly bool) string {
	workspace := scm.Workspace()
	candidateLimit := maxItems
	if workspace != "" && candidateLimit > 0 {
		candidateLimit = maxItems * 3
	}
	items := scm.ListVisible("", sessionID, candidateLimit)
	if sessionOnly {
		filtered := items[:0]
		for _, item := range items {
			if !item.Durable {
				filtered = append(filtered, item)
			}
		}
		items = filtered
	}
	items = filterInternalExecutionContextItems(items)
	if workspace != "" {
		items = filterContextItemsForWorkspace(items, workspace)
	}
	if maxItems > 0 && len(items) > maxItems {
		items = items[:maxItems]
	}
	if len(items) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("## Session Context (persisted across sessions)\n")
	if workspace != "" {
		sb.WriteString(fmt.Sprintf("Current workspace: %s\nIgnore persisted file paths outside this workspace unless the user explicitly asks for them.\n\n", workspace))
	}

	categories := map[string]string{
		"preference": "Preferences",
		"decision":   "Decisions",
		"fact":       "Facts",
		"todo":       "Action Items",
		"link":       "Links",
	}

	for _, cat := range []string{"preference", "decision", "fact", "todo", "link"} {
		var catItems []string
		for _, item := range items {
			if item.Category == cat {
				catItems = append(catItems, fmt.Sprintf("- %s: %s", item.Key, item.Value))
			}
		}
		if len(catItems) > 0 {
			sb.WriteString(fmt.Sprintf("### %s\n", categories[cat]))
			for _, ci := range catItems {
				sb.WriteString(ci + "\n")
			}
			sb.WriteString("\n")
		}
	}

	return sb.String()
}

// filterInternalExecutionContextItems is a compatibility filter for context
// written by older builds that accidentally treated pipeline stage prompts as
// user preferences. Runtime-generated preference_* and remembered_* entries
// may be recalled, but engine control prompts must never become instructions
// for a later user turn. Explicit durable context is left untouched.
func filterInternalExecutionContextItems(items []*ContextItem) []*ContextItem {
	filtered := make([]*ContextItem, 0, len(items))
	for _, item := range items {
		if isInternalExecutionContextItem(item) {
			continue
		}
		filtered = append(filtered, item)
	}
	return filtered
}

func isInternalExecutionContextItem(item *ContextItem) bool {
	if item == nil || item.Durable {
		return false
	}
	key := strings.ToLower(strings.TrimSpace(item.Key))
	if !strings.HasPrefix(key, "preference_") && !strings.HasPrefix(key, "remembered_") {
		return false
	}
	value := strings.ToLower(strings.TrimSpace(item.Value))
	return strings.Contains(value, "## 当前步骤唯一任务") ||
		strings.Contains(value, "[internal control") ||
		strings.Contains(value, "本步骤只形成执行简报，不做业务研究") ||
		(strings.Contains(value, "后续步骤与权限边界") && strings.Contains(value, "pipeline："))
}

func (scm *SessionContextManager) Workspace() string {
	scm.mu.RLock()
	defer scm.mu.RUnlock()
	return scm.workspace
}

func filterContextItemsForWorkspace(items []*ContextItem, workspace string) []*ContextItem {
	if workspace == "" {
		return items
	}
	filtered := make([]*ContextItem, 0, len(items))
	for _, item := range items {
		if item == nil {
			continue
		}
		if shouldDropContextItemForWorkspace(item, workspace) {
			continue
		}
		filtered = append(filtered, item)
	}
	return filtered
}

func shouldDropContextItemForWorkspace(item *ContextItem, workspace string) bool {
	key := strings.ToLower(strings.TrimSpace(item.Key))
	value := strings.TrimSpace(item.Value)
	if key == "project_path" || key == "workspace" || key == "project_root" {
		return !contextPathWithinWorkspace(value, workspace)
	}
	return false
}

func contextPathWithinWorkspace(pathValue, workspace string) bool {
	pathValue = normalizeContextPath(pathValue)
	workspace = normalizeContextPath(workspace)
	if pathValue == "" || workspace == "" {
		return false
	}
	_, ok := portablepath.RelativeTo(workspace, pathValue)
	return ok
}

func normalizeContextPath(pathValue string) string {
	pathValue = strings.TrimSpace(pathValue)
	pathValue = strings.Trim(pathValue, "`\"'")
	if pathValue == "" {
		return ""
	}
	if strings.HasPrefix(pathValue, "~") {
		if home, err := os.UserHomeDir(); err == nil {
			pathValue = filepath.Join(home, strings.TrimPrefix(pathValue, "~"))
		}
	}
	if portablepath.IsAbs(pathValue) && !filepath.IsAbs(pathValue) {
		return pathValue
	}
	if abs, err := filepath.Abs(filepath.Clean(pathValue)); err == nil {
		return abs
	}
	return filepath.Clean(pathValue)
}

// ==================== session_context tool ====================

type SessionContextTool struct {
	manager *SessionContextManager
	access  ToolAccess
	service *Service
	owner   func(context.Context) (Owner, error)
}

func NewSessionContextTool(manager *SessionContextManager) *SessionContextTool {
	return &SessionContextTool{manager: manager}
}

func NewSessionContextToolWithAccess(manager *SessionContextManager, access ToolAccess) *SessionContextTool {
	return &SessionContextTool{manager: manager, access: access}
}

func (t *SessionContextTool) Name() string { return "session_context" }

func (t *SessionContextTool) Description() string {
	description := "Manage persistent session context that carries across conversations. " +
		"Use this to remember user preferences, project decisions, important facts, and action items. " +
		"Explicitly saved values are durable by default; session-scoped values are isolated to the current conversation. " +
		"Categories: preference (user preferences), decision (project decisions), fact (important facts), todo (action items), link (references)."
	if t.service != nil {
		description += " Durable writes become active only when the current user request starts with a remember instruction whose payload exactly matches the value; otherwise they remain candidates and are excluded from automatic recall."
	}
	return description
}

func (t *SessionContextTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"action": map[string]interface{}{
				"type":        "string",
				"description": "Action: 'set' (save context), 'get' (retrieve), 'list' (all context), 'delete' (remove), 'inject' (get context prompt for system message)",
				"enum":        []string{"set", "get", "list", "delete", "inject"},
			},
			"key": map[string]interface{}{
				"type":        "string",
				"description": "Context key/name",
			},
			"value": map[string]interface{}{
				"type":        "string",
				"description": "Context value",
			},
			"category": map[string]interface{}{
				"type":        "string",
				"description": "Category: preference, decision, fact, todo, link",
				"enum":        []string{"preference", "decision", "fact", "todo", "link"},
			},
			"priority": map[string]interface{}{
				"type":        "integer",
				"description": "Priority (1-10, higher = more important). Default: 5",
			},
			"scope": map[string]interface{}{
				"type":        "string",
				"description": "Scope: durable (explicitly shared across conversations), session (only the current conversation), or visible (read current session with durable fallback). Set defaults to durable; delete defaults to current session and requires scope=durable for global deletion.",
				"enum":        []string{"durable", "session", "visible"},
			},
		},
		"required": []string{"action"},
	}
}

func (t *SessionContextTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	action := configvalue.String(params, "action", "list")
	if !memoryToolAllowed(t.access, ctx, action == "delete") {
		return "", fmt.Errorf("memory is disabled for this execution; only delete is available")
	}
	if t.service != nil {
		return t.executeService(ctx, params)
	}
	sessionID := sessionContextIDFromContext(ctx)

	if t.manager == nil {
		return "Session context manager not available", nil
	}

	switch action {
	case "set":
		key := configvalue.String(params, "key", "")
		value := configvalue.String(params, "value", "")
		if key == "" || value == "" {
			return "Error: key and value are required", nil
		}
		category := configvalue.String(params, "category", "fact")
		priority := configvalue.Int(params, "priority", 5)
		if priority < 1 {
			priority = 1
		}
		if priority > 10 {
			priority = 10
		}
		scope := configvalue.String(params, "scope", "durable")
		var err error
		switch scope {
		case "durable", "":
			err = t.manager.SetDurable(key, value, category, priority)
		case "session":
			if sessionID == "" {
				return "Error: session scope requires an active session", nil
			}
			err = t.manager.Set(key, value, category, priority, sessionID)
		case "visible":
			return "Error: visible is a read scope; use durable or session when setting context", nil
		default:
			return fmt.Sprintf("Error: unknown scope: %s", scope), nil
		}
		if err != nil {
			return "", fmt.Errorf("save session context: %w", err)
		}
		return fmt.Sprintf("Context saved: **%s** (%s, priority=%d) = %s", key, category, priority, value), nil

	case "get":
		key := configvalue.String(params, "key", "")
		if key == "" {
			return "Error: key is required", nil
		}
		scope := configvalue.String(params, "scope", "visible")
		var value string
		switch scope {
		case "visible", "":
			value = t.manager.GetForSession(key, sessionID)
		case "session":
			value = t.manager.GetSession(key, sessionID)
		case "durable":
			value = t.manager.GetDurable(key)
		default:
			return fmt.Sprintf("Error: unknown scope: %s", scope), nil
		}
		if value == "" {
			return fmt.Sprintf("No context found for key: %s", key), nil
		}
		block := fmt.Sprintf("**%s**: %s", key, value)
		if scope != "durable" {
			item := promptSessionContextSnapshot(t.manager, key, sessionID)
			if item != nil && item.Value == value {
				observePromptContextToolResult(ctx, item, sessionID, block, value)
			}
		}
		return block, nil

	case "list":
		category := configvalue.String(params, "category", "")
		items := t.manager.ListVisible(category, sessionID, 20)
		if len(items) == 0 {
			return "No context items stored. Use 'set' to save important information.", nil
		}
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("Context Items (%d):\n", len(items)))
		for _, item := range items {
			sb.WriteString(fmt.Sprintf("- [%s] **%s** = %s (p%d)\n", item.Category, item.Key, textutil.Truncate(item.Value, 100), item.Priority))
		}
		block := sb.String()
		for _, item := range items {
			observePromptContextToolResult(ctx, item, sessionID, block, textutil.Truncate(item.Value, 100))
		}
		return block, nil

	case "delete":
		key := configvalue.String(params, "key", "")
		if key == "" {
			return "Error: key is required", nil
		}
		scope := configvalue.String(params, "scope", "visible")
		var (
			deleted bool
			err     error
		)
		switch scope {
		case "visible", "":
			deleted, err = t.manager.DeleteVisible(key, sessionID)
		case "session":
			deleted, err = t.manager.DeleteForSession(key, sessionID)
		case "durable":
			deleted, err = t.manager.DeleteDurable(key)
		default:
			return fmt.Sprintf("Error: unknown scope: %s", scope), nil
		}
		if err != nil {
			return "", fmt.Errorf("delete session context: %w", err)
		}
		if !deleted {
			return fmt.Sprintf("No context found for key: %s", key), nil
		}
		return fmt.Sprintf("Context '%s' deleted", key), nil

	case "inject":
		prompt := t.manager.BuildContextPromptForSession(sessionID, contextPromptDefaultLimit)
		if prompt == "" {
			return "No context to inject.", nil
		}
		return prompt, nil
	}

	return fmt.Sprintf("Unknown action: %s", action), nil
}

func sessionContextIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	sessionID, _ := ctx.Value("session_id").(string)
	return sessionID
}

const (
	DurableContextSessionID   = durableContextSessionID
	ContextPromptDefaultLimit = contextPromptDefaultLimit
)

func DecodeContextStorageKey(storageKey string) (sessionID, key string, durable bool, ok bool) {
	return decodeContextStorageKey(storageKey)
}

func ContextPathWithinWorkspace(pathValue, workspace string) bool {
	return contextPathWithinWorkspace(pathValue, workspace)
}

func NormalizeContextPath(pathValue string) string {
	return normalizeContextPath(pathValue)
}

func SessionContextIDFromContext(ctx context.Context) string {
	return sessionContextIDFromContext(ctx)
}
