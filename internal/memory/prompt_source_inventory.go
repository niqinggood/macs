package memory

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"
)

const maxPromptSourceInventory = 300

// PromptInventoryItem describes existing input sources, not a send receipt or
// proof that a particular model request selected/used their content. Direct
// working/pinned candidates still share Recall's precedence and token budget.
type PromptInventoryItem struct {
	Item      RecallItem `json:"item"`
	Mode      string     `json:"mode"`
	Pinned    bool       `json:"pinned,omitempty"`
	CreatedAt *time.Time `json:"created_at,omitempty"`
	UpdatedAt time.Time  `json:"updated_at"`
	CodePath  string     `json:"code_path"`
}

func inventoryCreatedAt(created time.Time) *time.Time {
	if created.IsZero() {
		return nil
	}
	return &created
}

// PromptSourceInventory performs bounded reads of original records. The host
// supplies already authorized durable Web sessions; a public session string
// never proves ownership by itself. This method deliberately does not import,
// recall, embed, process jobs, build a context manager, or write any audit rows.
func (s *Service) PromptSourceInventory(ctx context.Context, owner Owner, allowedSessions []string, limit int) ([]PromptInventoryItem, error) {
	if _, _, err := s.scoped(ctx, owner); err != nil {
		return nil, err
	}
	if len(allowedSessions) > 200 {
		return nil, errors.New("too many authorized inventory sessions")
	}
	allowed := make(map[string]bool, len(allowedSessions))
	for _, session := range allowedSessions {
		if session == "" || len(session) > 256 || strings.TrimSpace(session) != session {
			return nil, errors.New("invalid authorized inventory session")
		}
		allowed[session] = true
	}
	limit = min(maxPromptSourceInventory, max(1, limit))
	currentSession := owner.SessionID
	if !allowed[currentSession] {
		currentSession = ""
	}
	services := []*Service{s}
	if s.shared != nil {
		if !s.shared.global || s.shared.workspace != s.workspace {
			return nil, errors.New("inventory shared memory belongs to another workspace")
		}
		services = append(services, s.shared)
	}
	namespaces := make([]string, 0, len(services))
	for _, service := range services {
		namespace, err := service.namespace(owner)
		if err != nil {
			return nil, err
		}
		namespaces = append(namespaces, namespace)
	}
	var retractions []memoryRetraction
	if s.db.Migrator().HasTable(&memoryRetraction{}) {
		if err := s.db.WithContext(ctx).Where("namespace IN ?", namespaces).Find(&retractions).Error; err != nil {
			return nil, err
		}
	}
	blocked := retractionFilter(ctx, retractions)
	items := make([]PromptInventoryItem, 0)
	if !s.global && currentSession != "" && s.contextStore != nil {
		if canonicalContextStoreDir(s.contextStore.Workspace()) != s.workspace {
			return nil, errors.New("inventory context belongs to another workspace")
		}
		working := filterContextItemsForWorkspace(filterInternalExecutionContextItems(s.contextStore.ListVisible("", currentSession, 0)), s.workspace)
		for _, item := range working {
			if item.Durable || item.SessionID != currentSession || blocked(item.Value) {
				continue
			}
			locator := PromptLocator{Kind: "context", Key: item.Key, SessionID: currentSession, Field: "session", Digest: promptContentDigest(item.Value)}
			items = append(items, PromptInventoryItem{Item: RecallItem{ID: contextStorageKey(item.Key, currentSession, false), Source: "session_context",
				Content: truncateSessionRunes(item.Key+": "+item.Value, 1200), SourceSessionID: currentSession, Locator: locator},
				Mode: "direct", CreatedAt: inventoryCreatedAt(item.CreatedAt), UpdatedAt: item.UpdatedAt, CodePath: "memory.Service.Recall/current-session-context"})
			if len(items) >= min(ContextPromptDefaultLimit, limit) {
				break
			}
		}
	}
	var candidates []PromptInventoryItem
	for _, service := range services {
		query, _, err := service.scoped(ctx, owner)
		if err != nil {
			return nil, err
		}
		query = query.Where("state = ?", "active")
		if service.global {
			query = query.Where("scope = ?", MemoryGlobal)
		} else if currentSession != "" {
			query = query.Where("scope IN ? OR (scope = ? AND session_id = ?)", []MemoryScope{MemoryUser, MemoryProject}, MemorySession, currentSession)
		} else {
			query = query.Where("scope IN ?", []MemoryScope{MemoryUser, MemoryProject})
		}
		var entries []Entry
		if err := query.Order("pinned DESC, updated_at DESC, id ASC").Limit(limit).Find(&entries).Error; err != nil {
			return nil, err
		}
		for _, entry := range entries {
			if blocked(entry.Value) {
				continue
			}
			locator := PromptLocator{Kind: "entry", ID: entry.ID, Key: entry.Key, Field: "value", Version: entry.Version, Digest: promptContentDigest(entry.Value)}
			if entry.Scope == MemorySession {
				locator.SessionID = entry.SessionID
			} else if entry.Scope == MemoryGlobal {
				locator.Field = "global"
			}
			mode := "relevant"
			pinned := entry.Pinned && entry.Kind == "preference" && (entry.Scope == MemoryUser || entry.Scope == MemoryGlobal)
			if pinned || entry.Scope == MemorySession {
				mode = "direct"
			}
			candidates = append(candidates, PromptInventoryItem{Item: RecallItem{ID: entry.ID, Source: "long_term:" + string(entry.Scope) + ":" + entry.Kind,
				Content: truncateSessionRunes(entry.Key+": "+entry.Value, 1200), Version: entry.Version, SourceSessionID: entry.SourceSessionID, Locator: locator},
				Mode: mode, Pinned: pinned, CreatedAt: inventoryCreatedAt(entry.CreatedAt), UpdatedAt: entry.UpdatedAt, CodePath: "memory.Service.Recall/active-entry"})
		}
	}
	if !s.global && len(allowed) != 0 {
		ids := make([]string, 0, len(allowed))
		for session := range allowed {
			ids = append(ids, session)
		}
		query, _, err := s.scoped(ctx, owner)
		if err != nil {
			return nil, err
		}
		var summaries []Summary
		if err := query.Where("session_id IN ? AND content <> ''", ids).Order("updated_at DESC, id ASC").Limit(limit).Find(&summaries).Error; err != nil {
			return nil, err
		}
		for _, summary := range summaries {
			if blocked(summary.Content) {
				continue
			}
			locator := PromptLocator{Kind: "summary", ID: summary.ID, SessionID: summary.SessionID, Field: "content", Revision: summary.SourceRevision, Digest: promptContentDigest(summary.Content)}
			candidates = append(candidates, PromptInventoryItem{Item: RecallItem{ID: summary.ID, Source: "summary", Content: truncateSessionRunes(summary.Content, 1200),
				SourceSessionID: summary.SessionID, Locator: locator}, Mode: "relevant", UpdatedAt: summary.UpdatedAt, CodePath: "memory.Service.Recall/related-summary"})
		}
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].Mode != candidates[j].Mode {
			return candidates[i].Mode == "direct"
		}
		if !candidates[i].UpdatedAt.Equal(candidates[j].UpdatedAt) {
			return candidates[i].UpdatedAt.After(candidates[j].UpdatedAt)
		}
		return candidates[i].Item.ID < candidates[j].Item.ID
	})
	for _, item := range candidates {
		if len(items) >= limit {
			break
		}
		items = append(items, item)
	}
	return items, nil
}
