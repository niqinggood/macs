package memory

import (
	"context"
	"errors"
	"strings"

	"gorm.io/gorm"
)

// MemoryGlobal is explicitly shared by assistants in one owner's workspace.
// It does not change the legacy user/project/session namespace or tool scopes.
const MemoryGlobal MemoryScope = "global"

// NewGlobalService has no legacy import or automatic context/summary writer.
// Only the host's explicit memory manager may write this shared collection.
func NewGlobalService(db *gorm.DB, workspace string, options ...ServiceOption) *Service {
	s := &Service{db: db, global: true, recallEmbedding: configuredRecallEmbedding}
	if strings.TrimSpace(workspace) != "" {
		s.workspace = canonicalContextStoreDir(workspace)
	}
	for _, option := range options {
		if option != nil {
			option(s)
		}
	}
	return s
}

func (s *Service) validScope(scope MemoryScope) bool {
	if s == nil {
		return false
	}
	if s.global {
		return scope == MemoryGlobal
	}
	return validAgentMemoryScope(scope)
}

func (s *Service) preferenceScope() MemoryScope {
	if s.global {
		return MemoryGlobal
	}
	return MemoryUser
}

// CreateSessionNote is the host's explicit creation flow after validating an
// actual owned conversation. Ordinary durable creation keeps its old contract.
func (s *Service) CreateSessionNote(ctx context.Context, owner Owner, key, value, kind string) error {
	return s.recordWithSource(ctx, owner, key, value, MemorySession, kind, "active", 5, nil, 0, WriteSource{Type: "user_explicit", Ref: "local-memory-manager"}, true)
}

func combinedSemanticStatus(first, second string) string {
	if first == second {
		return first
	}
	if first == "partial" || second == "partial" || first == "ready" || second == "ready" {
		return "partial"
	}
	if first == "unavailable" || second == "unavailable" {
		return "unavailable"
	}
	return "disabled"
}

type recallEntryExclusion struct {
	Entry  Entry
	Reason string
}

// Recheck every stored candidate after provider callbacks. Ordinary and shared
// records remain in their independently derived owner/workspace namespaces.
// Synthetic current-session SCM items are added only after this database fence.
func (s *Service) currentRecallEntries(ctx context.Context, owner Owner, entries []Entry) ([]Entry, []recallEntryExclusion, error) {
	if _, _, err := s.scoped(ctx, owner); err != nil {
		return nil, nil, err
	}
	current := make(map[string]Entry)
	namespaces := make(map[bool]string)
	for _, shared := range []bool{false, true} {
		service := s
		if shared && !s.global {
			service = s.shared
		}
		if service == nil || service.global != shared {
			continue
		}
		query, namespace, err := service.scoped(ctx, owner)
		if err != nil {
			return nil, nil, err
		}
		namespaces[shared] = namespace
		var ids []string
		seen := make(map[string]bool)
		for _, entry := range entries {
			if (entry.Scope == MemoryGlobal) == shared && entry.Namespace == namespace && entry.Version > 0 && !seen[entry.ID] {
				ids = append(ids, entry.ID)
				seen[entry.ID] = true
			}
		}
		if len(ids) == 0 {
			continue
		}
		if shared {
			query = query.Where("scope = ?", MemoryGlobal)
		} else {
			query = query.Where("scope IN ? AND (scope <> ? OR session_id = ?)", []MemoryScope{MemoryUser, MemoryProject, MemorySession}, MemorySession, owner.SessionID)
		}
		var rows []Entry
		if err := query.Where("id IN ? AND state = ?", ids, "active").Find(&rows).Error; err != nil {
			return nil, nil, err
		}
		for _, entry := range rows {
			current[entry.ID] = entry
		}
	}
	result := make([]Entry, 0, len(entries))
	excluded := make([]recallEntryExclusion, 0)
	for _, entry := range entries {
		latest, exists := current[entry.ID]
		reason := "stale"
		if exists && entry.Version != latest.Version {
			reason = "version_changed"
		} else if exists && entry.Version > 0 && entry.Namespace == namespaces[entry.Scope == MemoryGlobal] &&
			entry.Namespace == latest.Namespace && entry.State == "active" && entry.Scope == latest.Scope && entry.SessionID == latest.SessionID &&
			entry.TenantID == latest.TenantID && entry.OwnerID == latest.OwnerID && entry.AgentName == latest.AgentName &&
			entry.Key == latest.Key && entry.Value == latest.Value && entry.WhenToUse == latest.WhenToUse && entry.Kind == latest.Kind && entry.Tags == latest.Tags &&
			entry.Priority == latest.Priority && entry.Pinned == latest.Pinned {
			result = append(result, latest)
			continue
		}
		excluded = append(excluded, recallEntryExclusion{Entry: entry, Reason: reason})
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	return result, excluded, nil
}

// PageWithFilters applies kind filtering in SQL before counting and pagination.
// Session entries require an explicit session; hosts must validate that session.
func (s *Service) PageWithFilters(ctx context.Context, owner Owner, scope MemoryScope, kind, state, search string, offset, limit int, excludedKinds ...string) ([]Entry, int64, error) {
	if scope != "" && !s.validScope(scope) || scope == MemorySession && strings.TrimSpace(owner.SessionID) == "" {
		return nil, 0, errors.New("invalid management scope or session")
	}
	if kind != "" && kind != "preference" && kind != "fact" && kind != "decision" && kind != "experience" {
		return nil, 0, errors.New("invalid memory kind")
	}
	if state != "" && state != "active" && state != "candidate" {
		return nil, 0, errors.New("invalid memory state")
	}
	for _, excluded := range excludedKinds {
		if excluded != "preference" && excluded != "fact" && excluded != "decision" && excluded != "experience" {
			return nil, 0, errors.New("invalid excluded memory kind")
		}
	}
	if offset < 0 || offset > 100000 || limit < 1 || limit > 100 || len([]rune(search)) > 100 {
		return nil, 0, errors.New("invalid memory page")
	}
	if err := s.Import(ctx, owner); err != nil {
		return nil, 0, err
	}
	query, _, err := s.scoped(ctx, owner)
	if err != nil {
		return nil, 0, err
	}
	scopes := []MemoryScope{MemoryUser, MemoryProject}
	if s.global {
		scopes = []MemoryScope{MemoryGlobal}
	} else if scope == MemorySession {
		scopes = []MemoryScope{MemorySession}
		query = query.Where("session_id = ?", owner.SessionID)
	}
	query = query.Model(&Entry{}).Where("scope IN ? AND state IN ?", scopes, []string{"active", "candidate"})
	if scope != "" {
		query = query.Where("scope = ?", scope)
	}
	if kind != "" {
		query = query.Where("kind = ?", kind)
	}
	if len(excludedKinds) > 0 {
		query = query.Where("kind NOT IN ?", excludedKinds)
	}
	if state != "" {
		query = query.Where("state = ?", state)
	}
	if search = strings.TrimSpace(search); search != "" {
		literal := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(strings.ToLower(search))
		pattern := "%" + literal + "%"
		query = query.Where(`(LOWER(key) LIKE ? ESCAPE '\' OR LOWER(value) LIKE ? ESCAPE '\')`, pattern, pattern)
	}
	var count int64
	if err := query.Count(&count).Error; err != nil {
		return nil, 0, err
	}
	var entries []Entry
	err = query.Order("updated_at DESC, id ASC").Offset(offset).Limit(limit).Find(&entries).Error
	if err == nil {
		err = s.attachReviewEvidence(ctx, owner, entries)
	}
	return entries, count, err
}
