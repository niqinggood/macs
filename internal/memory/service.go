package memory

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"
	"unicode"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const DefaultRecallTokens = 2000

var ErrMemoryKeyExists = errors.New("memory key already exists")

// Owner is supplied by the host, never by model tool arguments. Workspace and
// agent are bound when the service is constructed; personal owners stay private.
type Owner struct {
	TenantID     string
	SubjectID    string
	PartitionID  string
	SessionID    string
	ImportLegacy bool
}

type Entry struct {
	ID              string      `gorm:"primaryKey;size:64" json:"id"`
	Namespace       string      `gorm:"index;size:64;not null" json:"-"`
	TenantID        string      `gorm:"size:64;not null" json:"tenant_id"`
	OwnerID         string      `gorm:"size:128" json:"owner_id"`
	AgentName       string      `gorm:"size:100" json:"agent_name"`
	Key             string      `gorm:"type:text" json:"key"`
	Value           string      `gorm:"type:text" json:"value"`
	WhenToUse       string      `gorm:"type:text" json:"when_to_use,omitempty"`
	Evidence        []Evidence  `gorm:"-" json:"evidence,omitempty"`
	Scope           MemoryScope `gorm:"size:16" json:"scope"`
	Kind            string      `gorm:"size:16" json:"kind"`
	State           string      `gorm:"size:16;index" json:"state"`
	Priority        int         `json:"priority"`
	Pinned          bool        `gorm:"not null;default:false" json:"pinned"`
	Version         int         `json:"version"`
	Tags            string      `gorm:"type:text" json:"tags"`
	SessionID       string      `gorm:"type:text" json:"session_id,omitempty"`
	SourceSessionID string      `gorm:"type:text" json:"source_session_id,omitempty"`
	SourceType      string      `gorm:"size:32" json:"source_type,omitempty"`
	SourceRef       string      `gorm:"size:128" json:"source_ref,omitempty"`
	SourceQuote     string      `gorm:"type:text" json:"source_quote,omitempty"`
	ConfirmedRef    string      `gorm:"size:128" json:"confirmed_ref,omitempty"`
	ConfirmedQuote  string      `gorm:"type:text" json:"confirmed_quote,omitempty"`
	CreatedAt       time.Time   `json:"created_at"`
	UpdatedAt       time.Time   `json:"updated_at"`
}

func (Entry) TableName() string { return "memory_entries" }

type memoryMigration struct {
	Namespace string `gorm:"primaryKey;size:64"`
}

func (memoryMigration) TableName() string { return "memory_service_migrations" }

// Retractions retain only hashes. Exact old values are barred from supplemental
// recall, without duplicating forgotten text in a second plaintext store.
type memoryRetraction struct {
	ID        string `gorm:"primaryKey;size:64"`
	Namespace string `gorm:"index;size:64"`
	EntryID   string `gorm:"size:64"`
	Digest    string `gorm:"size:64"`
	Length    int
}

func (memoryRetraction) TableName() string { return "memory_retractions" }

type Summary struct {
	ID              string `gorm:"primaryKey;size:64"`
	Namespace       string `gorm:"index;size:64"`
	SessionID       string `gorm:"type:text"`
	Content         string `gorm:"type:text"`
	SourceRevision  int64  `gorm:"not null;default:0"`
	SourceCreatedAt time.Time
	SourceJobID     string `gorm:"size:64"`
	UpdatedAt       time.Time
}

func (Summary) TableName() string { return "memory_summaries" }

type Service struct {
	db              *gorm.DB
	workspace       string
	agent           string
	legacy          *AgentMemoryStore
	contextStore    *SessionContextManager
	recallEmbedding func() RecallEmbedding
	recallQueries   recallQueryCache
	global          bool
	shared          *Service
	reviewAllowed   func() bool
}

type ServiceOption func(*Service)

func NewService(db *gorm.DB, workspace, agent string, options ...ServiceOption) *Service {
	s := &Service{db: db, agent: agent, recallEmbedding: configuredRecallEmbedding}
	if strings.TrimSpace(workspace) != "" {
		s.workspace = canonicalContextStoreDir(workspace)
		s.legacy = NewAgentMemoryStore(workspace, agent)
	}
	for _, option := range options {
		if option != nil {
			option(s)
		}
	}
	s.shared = NewGlobalService(db, workspace, WithRecallEmbedding(s.recallEmbedding))
	return s
}

func (s *Service) SetContextStore(store *SessionContextManager) error {
	if s == nil || s.global || store == nil || strings.TrimSpace(store.Workspace()) == "" || canonicalContextStoreDir(store.Workspace()) != s.workspace {
		return errors.New("memory context store belongs to another workspace")
	}
	s.contextStore = store
	return nil
}

func (s *Service) namespace(owner Owner) (string, error) {
	if s == nil || s.db == nil || s.workspace == "" || !s.global && s.agent == "" || strings.TrimSpace(owner.PartitionID) == "" {
		return "", errors.New("memory service or trusted owner is unavailable")
	}
	if s.global {
		return memoryHash("workspace-global-v1", owner.PartitionID, owner.TenantID, owner.SubjectID, s.workspace), nil
	}
	return memoryHash(owner.PartitionID, owner.TenantID, owner.SubjectID, s.workspace, s.agent), nil
}

func memoryHash(parts ...string) string {
	data, _ := json.Marshal(parts)
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func (s *Service) scoped(ctx context.Context, owner Owner) (*gorm.DB, string, error) {
	namespace, err := s.namespace(owner)
	if err != nil {
		return nil, "", err
	}
	if ctx == nil {
		return nil, "", errors.New("memory operations require a context")
	}
	if err := ctx.Err(); err != nil {
		return nil, "", err
	}
	return s.db.WithContext(ctx).Where("namespace = ?", namespace), namespace, nil
}

// Import is transactional and once per owner/workspace/agent. A migration
// marker prevents old JSON from resurrecting corrected or deleted records.
func (s *Service) Import(ctx context.Context, owner Owner) error {
	_, namespace, err := s.scoped(ctx, owner)
	if err != nil || !owner.ImportLegacy || s.global {
		return err
	}
	var marker memoryMigration
	err = s.db.WithContext(ctx).First(&marker, "namespace = ?", namespace).Error
	if err == nil {
		return nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	s.legacy.mu.RLock()
	loadErr := s.legacy.loadErr
	s.legacy.mu.RUnlock()
	if loadErr != nil {
		return loadErr
	}
	memories := s.legacy.allMemories()
	if s.contextStore != nil {
		items := filterInternalExecutionContextItems(s.contextStore.ListVisible("", "", 0))
		items = filterContextItemsForWorkspace(items, s.workspace)
		for _, item := range items {
			if !item.Durable {
				continue
			}
			scope := MemoryProject
			if item.Category == "preference" {
				scope = MemoryUser
			}
			storageKey := agentMemoryStorageKey(item.Key, scope, "")
			if memories[storageKey] != nil {
				continue
			}
			memories[storageKey] = &AgentMemory{Key: item.Key, Value: item.Value, Scope: scope, AgentName: s.agent,
				Priority: item.Priority, Tags: []string{"context:" + item.Category}, Version: 1, CreatedAt: item.CreatedAt, UpdatedAt: item.UpdatedAt}
		}
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		claim := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&memoryMigration{Namespace: namespace})
		if claim.Error != nil || claim.RowsAffected == 0 {
			return claim.Error
		}
		for _, m := range memories {
			if m == nil || !validAgentMemoryScope(m.Scope) || m.Scope == MemorySession && m.SessionID == "" {
				continue
			}
			tags, _ := json.Marshal(m.Tags)
			state := "active"
			if m.Deleted {
				state = "deleted"
			}
			e := Entry{ID: memoryHash(namespace, agentMemoryStorageKey(m.Key, m.Scope, m.SessionID)), Namespace: namespace,
				TenantID: owner.TenantID, OwnerID: owner.SubjectID, AgentName: s.agent, Key: m.Key, Value: m.Value,
				Scope: m.Scope, Kind: "fact", State: state, Priority: m.Priority, Version: max(1, m.Version),
				Tags: string(tags), SessionID: m.SessionID, SourceSessionID: m.SourceSessionID, SourceType: "legacy", CreatedAt: m.CreatedAt, UpdatedAt: m.UpdatedAt}
			if m.Scope == MemoryUser {
				e.Kind = "preference"
			}
			for _, tag := range m.Tags {
				if tag == "context:decision" {
					e.Kind = "decision"
				}
			}
			if m.Deleted {
				e.Value = ""
				e.Tags = "[]"
				e.SourceSessionID = ""
				e.SourceType = ""
			}
			if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&e).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *Service) validateRecord(key, value string, scope MemoryScope, sessionID string) error {
	if !s.validScope(scope) || scope == MemorySession && strings.TrimSpace(sessionID) == "" {
		return errors.New("valid scope and an active session for session memory are required")
	}
	if strings.TrimSpace(key) == "" || strings.TrimSpace(value) == "" || len([]rune(key)) > 256 || len([]rune(value)) > 8000 {
		return errors.New("memory requires a key (up to 256 characters) and value (up to 8000 characters)")
	}
	return nil
}

// Record creates or replaces a keyed entry. expectedVersion > 0 is a strict
// compare-and-swap correction; ordinary remember is also fenced against races.
func (s *Service) Record(ctx context.Context, owner Owner, key, value string, scope MemoryScope, kind, state string, priority int, tags []string, expectedVersion int) error {
	return s.RecordWithSource(ctx, owner, key, value, scope, kind, state, priority, tags, expectedVersion, WriteSource{Type: "service"})
}

func (s *Service) RecordWithSource(ctx context.Context, owner Owner, key, value string, scope MemoryScope, kind, state string, priority int, tags []string, expectedVersion int, source WriteSource) error {
	return s.recordWithSource(ctx, owner, key, value, scope, kind, state, priority, tags, expectedVersion, source, false)
}

// CreateWithSource inserts a new key without replacing an existing or
// forgotten key. It is used by explicit user-facing creation flows.
func (s *Service) CreateWithSource(ctx context.Context, owner Owner, key, value string, scope MemoryScope, kind string, priority int, tags []string, source WriteSource) error {
	if !s.validScope(scope) || scope == MemorySession {
		return errors.New("invalid manual memory scope")
	}
	return s.recordWithSource(ctx, owner, key, value, scope, kind, "active", priority, tags, 0, source, true)
}

func (s *Service) recordWithSource(ctx context.Context, owner Owner, key, value string, scope MemoryScope, kind, state string, priority int, tags []string, expectedVersion int, source WriteSource, createOnly bool) error {
	if err := s.validateRecord(key, value, scope, owner.SessionID); err != nil {
		return err
	}
	if s.global && (source.Type != "user_explicit" || state != "active") {
		return errors.New("shared memory requires an explicit manual write")
	}
	defaultKind := kind == ""
	if defaultKind {
		kind = "fact"
		if scope == MemoryUser {
			kind = "preference"
		}
	}
	if kind != "preference" && kind != "fact" && kind != "decision" && kind != "experience" {
		return errors.New("invalid memory kind")
	}
	if state == "" {
		state = "active"
	}
	if state != "active" && state != "candidate" {
		return errors.New("invalid memory state")
	}
	if len([]rune(source.Ref)) > 128 || len([]rune(source.Quote)) > 300 || source.Type != "service" && source.Type != "agent_candidate" && source.Type != "user_explicit" && source.Type != "session_working" {
		return errors.New("invalid memory write source")
	}
	if err := s.Import(ctx, owner); err != nil {
		return err
	}
	_, namespace, err := s.scoped(ctx, owner)
	if err != nil {
		return err
	}
	sessionID := owner.SessionID
	if scope != MemorySession {
		sessionID = ""
	}
	id := memoryHash(namespace, agentMemoryStorageKey(key, scope, sessionID))
	cleanTags := make([]string, 0, len(tags))
	seenTags := map[string]bool{}
	for _, tag := range tags {
		tag = strings.TrimSpace(tag)
		if tag != "" && !seenTags[tag] {
			cleanTags = append(cleanTags, tag)
			seenTags[tag] = true
		}
	}
	encodedTags, _ := json.Marshal(cleanTags)
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var old Entry
		err := tx.Where("namespace = ? AND id = ?", namespace, id).First(&old).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if createOnly && old.ID != "" {
			return ErrMemoryKeyExists
		}
		if expectedVersion > 0 && (old.ID == "" || old.State == "deleted" || old.Version != expectedVersion) {
			return errors.New("memory version changed; list again before correcting")
		}
		if state == "candidate" && (old.State == "active" || old.State == "deleted") {
			return errors.New("a candidate cannot replace active or forgotten memory; use a separate key for the proposal")
		}
		now := time.Now()
		e := Entry{ID: id, Namespace: namespace, TenantID: owner.TenantID, OwnerID: owner.SubjectID, AgentName: s.agent,
			Key: key, Value: value, Scope: scope, Kind: kind, State: state, Priority: min(10, max(1, priority)), Version: 1,
			Tags: string(encodedTags), SessionID: sessionID, SourceSessionID: owner.SessionID, SourceType: source.Type, SourceRef: source.Ref, SourceQuote: source.Quote, CreatedAt: now, UpdatedAt: now}
		if old.ID == "" {
			if createOnly {
				result := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&e)
				if result.Error != nil {
					return result.Error
				}
				if result.RowsAffected == 0 {
					return ErrMemoryKeyExists
				}
				return nil
			}
			return tx.Create(&e).Error
		}
		e.Version = old.Version + 1
		e.CreatedAt = old.CreatedAt
		if defaultKind {
			e.Kind = old.Kind
		}
		result := tx.Model(&Entry{}).Where("namespace = ? AND id = ? AND version = ?", namespace, id, old.Version).
			Updates(map[string]interface{}{"value": e.Value, "kind": e.Kind, "state": e.State, "priority": e.Priority, "version": e.Version, "tags": e.Tags, "source_session_id": e.SourceSessionID, "source_type": e.SourceType, "source_ref": e.SourceRef, "source_quote": e.SourceQuote, "confirmed_ref": "", "confirmed_quote": "", "pinned": old.Pinned && old.Value == e.Value && e.Kind == "preference" && e.State == "active", "updated_at": now})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return errors.New("memory version changed; retry after reading current version")
		}
		if old.Value != value {
			if err := retireReviewEvidence(tx, namespace, id); err != nil {
				return err
			}
			if err := retractValue(tx, namespace, id, old.Value); err != nil {
				return err
			}
		}
		return saveMemoryChange(tx, namespace, id, e.Version, "edited")
	})
}

func normalizedMemory(value string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return unicode.ToLower(r)
	}, value)
}

func retractValue(tx *gorm.DB, namespace, id, value string) error {
	// Remove derived data in the same transaction as correction/deletion.
	// Summary vectors are also invalidated; retracted summaries cannot be indexed again.
	if err := tx.Where("namespace = ? AND (source_id = ? OR source = ?)", namespace, id, "summary").Delete(&recallVector{}).Error; err != nil {
		return err
	}
	normalized := normalizedMemory(value)
	if normalized == "" {
		return nil
	}
	digest := retractionDigest(normalized)
	return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&memoryRetraction{ID: memoryHash(namespace, id, digest), Namespace: namespace, EntryID: id, Digest: digest, Length: len([]rune(normalized))}).Error
}

func retractionDigest(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}

func (s *Service) Forget(ctx context.Context, owner Owner, key string, scope MemoryScope) (bool, error) {
	return s.forget(ctx, owner, key, scope, 0)
}

// ForgetExpected protects a user's review screen from deleting a version that
// changed after it was displayed.
func (s *Service) ForgetExpected(ctx context.Context, owner Owner, key string, scope MemoryScope, expectedVersion int) (bool, error) {
	if expectedVersion < 1 {
		return false, errors.New("deletion requires a current memory version")
	}
	return s.forget(ctx, owner, key, scope, expectedVersion)
}

func (s *Service) forget(ctx context.Context, owner Owner, key string, scope MemoryScope, expectedVersion int) (bool, error) {
	if err := s.validateRecord(key, "delete", scope, owner.SessionID); err != nil {
		return false, err
	}
	if err := s.Import(ctx, owner); err != nil {
		return false, err
	}
	_, namespace, err := s.scoped(ctx, owner)
	if err != nil {
		return false, err
	}
	id := memoryHash(namespace, agentMemoryStorageKey(key, scope, owner.SessionID))
	deleted := false
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var old Entry
		err := tx.Where("namespace = ? AND id = ?", namespace, id).First(&old).Error
		if errors.Is(err, gorm.ErrRecordNotFound) || old.State == "deleted" {
			if expectedVersion > 0 {
				return errors.New("memory version changed; refresh before deleting")
			}
			return nil
		}
		if err != nil {
			return err
		}
		if expectedVersion > 0 && old.Version != expectedVersion {
			return errors.New("memory version changed; refresh before deleting")
		}
		result := tx.Model(&Entry{}).Where("namespace = ? AND id = ? AND version = ?", namespace, id, old.Version).
			Updates(map[string]interface{}{"value": "", "tags": "[]", "source_session_id": "", "source_type": "", "source_ref": "", "source_quote": "", "confirmed_ref": "", "confirmed_quote": "", "state": "deleted", "pinned": false, "version": old.Version + 1, "updated_at": time.Now()})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return errors.New("memory version changed; retry deletion")
		}
		if err := retractValue(tx, namespace, id, old.Value); err != nil {
			return err
		}
		if err := retireReviewEvidence(tx, namespace, id); err != nil {
			return err
		}
		if err := saveMemoryChange(tx, namespace, id, old.Version+1, "deleted"); err != nil {
			return err
		}
		deleted = true
		return nil
	})
	return deleted, err
}

// Page returns durable entries for a review UI. Filters run in SQL before
// pagination, unlike the small automatic-recall candidate pool.
func (s *Service) Page(ctx context.Context, owner Owner, scope MemoryScope, state, search string, offset, limit int) ([]Entry, int64, error) {
	if scope == MemorySession {
		return nil, 0, errors.New("session notes require explicit filtered management")
	}
	return s.PageWithFilters(ctx, owner, scope, "", state, search, offset, limit)
}

// Export streams current durable entries without loading the whole collection
// into memory. Deleted values and session working state are excluded.
func (s *Service) Export(ctx context.Context, owner Owner, writer io.Writer) error {
	if writer == nil {
		return errors.New("memory export writer is unavailable")
	}
	if err := s.Import(ctx, owner); err != nil {
		return err
	}
	query, _, err := s.scoped(ctx, owner)
	if err != nil {
		return err
	}
	scopes := []MemoryScope{MemoryUser, MemoryProject}
	if s.global {
		scopes = []MemoryScope{MemoryGlobal}
	}
	rows, err := query.Model(&Entry{}).Where("scope IN ? AND state IN ?", scopes, []string{"active", "candidate"}).Order("updated_at DESC, id ASC").Rows()
	if err != nil {
		return err
	}
	defer rows.Close()
	if _, err := io.WriteString(writer, "[\n"); err != nil {
		return err
	}
	first := true
	for rows.Next() {
		if err := ctx.Err(); err != nil {
			return err
		}
		var entry Entry
		if err := s.db.ScanRows(rows, &entry); err != nil {
			return err
		}
		data, err := json.Marshal(entry)
		if err != nil {
			return err
		}
		if !first {
			if _, err := io.WriteString(writer, ",\n"); err != nil {
				return err
			}
		}
		if _, err := writer.Write(data); err != nil {
			return err
		}
		first = false
	}
	if err := rows.Err(); err != nil {
		return err
	}
	_, err = io.WriteString(writer, "\n]\n")
	return err
}

// Confirm promotes exactly the candidate version inspected by the caller. It
// preserves the original value, tags and source instead of rewriting evidence.
func (s *Service) Confirm(ctx context.Context, owner Owner, key string, scope MemoryScope, expectedVersion int) (*Entry, error) {
	return s.ConfirmWithSource(ctx, owner, key, scope, expectedVersion, WriteSource{})
}

func (s *Service) ConfirmWithSource(ctx context.Context, owner Owner, key string, scope MemoryScope, expectedVersion int, source WriteSource) (*Entry, error) {
	if err := s.validateRecord(key, "confirm", scope, owner.SessionID); err != nil {
		return nil, err
	}
	if len([]rune(source.Ref)) > 128 || len([]rune(source.Quote)) > 300 {
		return nil, errors.New("invalid memory confirmation source")
	}
	if expectedVersion < 1 {
		return nil, errors.New("confirm requires expected_version from recall/list")
	}
	if err := s.Import(ctx, owner); err != nil {
		return nil, err
	}
	_, namespace, err := s.scoped(ctx, owner)
	if err != nil {
		return nil, err
	}
	id := memoryHash(namespace, agentMemoryStorageKey(key, scope, owner.SessionID))
	var entry Entry
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("namespace = ? AND id = ?", namespace, id).First(&entry).Error; err != nil {
			return err
		}
		if entry.State != "candidate" || entry.Version != expectedVersion {
			return errors.New("candidate version changed or is no longer pending; recall/list again before confirming")
		}
		now := time.Now()
		result := tx.Model(&Entry{}).Where("namespace = ? AND id = ? AND state = ? AND version = ?", namespace, id, "candidate", expectedVersion).
			Updates(map[string]interface{}{"state": "active", "version": expectedVersion + 1, "confirmed_ref": source.Ref, "confirmed_quote": source.Quote, "updated_at": now})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return errors.New("candidate version changed; recall/list again before confirming")
		}
		entry.State, entry.Version, entry.UpdatedAt, entry.ConfirmedRef, entry.ConfirmedQuote = "active", expectedVersion+1, now, source.Ref, source.Quote
		return saveMemoryChange(tx, namespace, id, entry.Version, "confirmed")
	})
	if err != nil {
		return nil, err
	}
	return &entry, nil
}

func (s *Service) List(ctx context.Context, owner Owner, scope MemoryScope, tag string, limit int) ([]Entry, error) {
	return s.ListByState(ctx, owner, scope, tag, "", limit)
}

// ListByState filters before bounding the candidate set, so pending inferences
// cannot crowd active memories out of automatic recall.
func (s *Service) ListByState(ctx context.Context, owner Owner, scope MemoryScope, tag, state string, limit int) ([]Entry, error) {
	if state != "" && state != "active" && state != "candidate" {
		return nil, errors.New("invalid memory list state")
	}
	if scope != "" && !s.validScope(scope) {
		return nil, errors.New("invalid memory scope")
	}
	if err := s.Import(ctx, owner); err != nil {
		return nil, err
	}
	query, _, err := s.scoped(ctx, owner)
	if err != nil {
		return nil, err
	}
	query = query.Where("state <> ? AND (scope <> ? OR session_id = ?)", "deleted", MemorySession, owner.SessionID)
	if state != "" {
		query = query.Where("state = ?", state)
	}
	if scope != "" {
		query = query.Where("scope = ?", scope)
	}
	var entries []Entry
	// Bounded candidate set; full semantic indexing remains an optional layer.
	if err := query.Order("priority DESC, updated_at DESC, id ASC").Limit(200).Find(&entries).Error; err != nil {
		return nil, err
	}
	result := make([]Entry, 0, len(entries))
	for _, e := range entries {
		if e.Scope == MemorySession && owner.SessionID == "" {
			continue
		}
		if tag != "" {
			var tags []string
			_ = json.Unmarshal([]byte(e.Tags), &tags)
			found := false
			for _, t := range tags {
				if t == tag {
					found = true
				}
			}
			if !found {
				continue
			}
		}
		result = append(result, e)
		if limit > 0 && len(result) >= limit {
			break
		}
	}
	return result, nil
}

func (s *Service) Lookup(ctx context.Context, owner Owner, key string, scope MemoryScope) (*Entry, error) {
	if scope != "" && !s.validScope(scope) {
		return nil, errors.New("invalid memory scope")
	}
	if err := s.Import(ctx, owner); err != nil {
		return nil, err
	}
	query, _, err := s.scoped(ctx, owner)
	if err != nil {
		return nil, err
	}
	query = query.Where("key = ? AND state <> ? AND (scope <> ? OR session_id = ?)", key, "deleted", MemorySession, owner.SessionID)
	if scope != "" {
		query = query.Where("scope = ?", scope)
	}
	var entries []Entry
	if err := query.Find(&entries).Error; err != nil {
		return nil, err
	}
	var best *Entry
	for i := range entries {
		if entries[i].Scope == MemorySession && owner.SessionID == "" {
			continue
		}
		if best == nil || scopeRank(entries[i].Scope) > scopeRank(best.Scope) {
			best = &entries[i]
		}
	}
	return best, nil
}

func scopeRank(scope MemoryScope) int {
	if scope == MemoryGlobal {
		return 0
	}
	if scope == MemorySession {
		return 3
	}
	if scope == MemoryProject {
		return 2
	}
	return 1
}
