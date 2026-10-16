package memory

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"
)

const (
	maxPromptInputSources = 128
	maxPromptInputBytes   = 1 << 20
	maxPromptInputs       = 1000
	promptInputRetention  = 30 * 24 * time.Hour
)

var (
	ErrPromptSourceConflict    = errors.New("prompt source changed; read the original source again")
	ErrPromptSourceUnsupported = errors.New("prompt source is not managed by this memory service")
)

// PromptLocator is a host-created original-source address. It is deliberately
// absent from HTTP JSON; callers must resolve it from an owned saved receipt.
// Its identifiers are data, not proof of permission to read another owner.
type PromptLocator struct {
	Kind      string
	ID        string
	SessionID string
	Key       string
	Field     string
	Path      string
	Version   int
	Revision  int64
	Digest    string
}

type PromptSource struct {
	ID               string        `json:"id"`
	Kind             string        `json:"kind"`
	Category         string        `json:"category,omitempty"`
	Title            string        `json:"title"`
	SourceLabel      string        `json:"source_label"`
	Excerpt          string        `json:"excerpt"`
	CapturedRevision string        `json:"captured_revision,omitempty"`
	SourceSessionID  string        `json:"source_session_id,omitempty"`
	Locator          PromptLocator `json:"-"`
}

// PromptInput is a receipt of source excerpts matched in one outbound request.
// Attempted means a request was about to be sent, not that the provider accepted
// it or that the model adopted any of the included information.
type PromptInput struct {
	ID        string         `json:"id"`
	SessionID string         `json:"session_id"`
	TaskID    string         `json:"task_id,omitempty"`
	Agent     string         `json:"agent"`
	CallIndex int            `json:"call_index"`
	CreatedAt time.Time      `json:"created_at"`
	Status    string         `json:"status"`
	Sources   []PromptSource `json:"sources"`
}

type promptInputRecord struct {
	ID        string `gorm:"primaryKey;size:64"`
	Namespace string `gorm:"index;size:64;not null"`
	SessionID string `gorm:"type:text"`
	TaskID    string `gorm:"size:128"`
	Agent     string `gorm:"size:100"`
	CallIndex int
	Status    string `gorm:"size:16"`
	Sources   string `gorm:"type:text"`
	Locators  string `gorm:"type:text"`
	CreatedAt time.Time
}

func (promptInputRecord) TableName() string { return "memory_prompt_inputs" }

// SavePromptInput stores only matched source excerpts. It never stores the
// complete provider request, messages, credentials, or unselected candidates.
func (s *Service) SavePromptInput(ctx context.Context, owner Owner, input PromptInput) (*PromptInput, error) {
	_, namespace, err := s.scoped(ctx, owner)
	if err != nil {
		return nil, err
	}
	if s.global || owner.SessionID == "" || input.SessionID != owner.SessionID || input.Agent != s.agent || len(input.SessionID) > 256 || len(input.TaskID) > 128 || input.CallIndex < 1 || input.CallIndex > 4096 {
		return nil, errors.New("prompt input requires the current service agent and session")
	}
	if input.Status != "attempted" && input.Status != "succeeded" && input.Status != "failed" {
		return nil, errors.New("invalid prompt input status")
	}
	if len(input.Sources) > maxPromptInputSources {
		return nil, errors.New("prompt input contains too many sources")
	}
	input.ID = rand.Text()
	input.CreatedAt = time.Now()
	input.Sources = append([]PromptSource{}, input.Sources...)
	locators := make(map[string]PromptLocator, len(input.Sources))
	for i := range input.Sources {
		source := &input.Sources[i]
		if strings.TrimSpace(source.Kind) == "" || len(source.Kind) > 64 || len(source.Category) > 64 || len([]rune(source.Title)) > 512 || len(source.SourceLabel) > 1024 || len(source.SourceSessionID) > 256 || len(source.CapturedRevision) > 256 || len(source.Locator.Kind) > 64 || len(source.Locator.ID) > 256 || len(source.Locator.SessionID) > 256 || len(source.Locator.Key) > 2048 || len(source.Locator.Field) > 64 || len(source.Locator.Path) > 4096 || len(source.Locator.Digest) > 256 || source.Locator.Version < 0 || source.Locator.Revision < 0 {
			return nil, errors.New("invalid prompt source metadata")
		}
		source.ID = rand.Text()
		locators[source.ID] = source.Locator
	}
	sources, err := json.Marshal(input.Sources)
	if err != nil {
		return nil, err
	}
	addresses, err := json.Marshal(locators)
	if err != nil {
		return nil, err
	}
	if len(sources)+len(addresses) > maxPromptInputBytes {
		return nil, errors.New("prompt input exceeds 1 MiB")
	}
	record := promptInputRecord{ID: input.ID, Namespace: namespace, SessionID: input.SessionID, TaskID: input.TaskID, Agent: input.Agent, CallIndex: input.CallIndex, Status: input.Status, Sources: string(sources), Locators: string(addresses), CreatedAt: input.CreatedAt}
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&record).Error; err != nil {
			return err
		}
		if err := tx.Where("namespace = ? AND created_at < ?", namespace, input.CreatedAt.Add(-promptInputRetention)).Delete(&promptInputRecord{}).Error; err != nil {
			return err
		}
		var keep []string
		if err := tx.Model(&promptInputRecord{}).Where("namespace = ?", namespace).Order("created_at DESC, call_index DESC, id DESC").Limit(maxPromptInputs).Pluck("id", &keep).Error; err != nil {
			return err
		}
		return tx.Where("namespace = ? AND id NOT IN ?", namespace, keep).Delete(&promptInputRecord{}).Error
	})
	if err != nil {
		return nil, err
	}
	return &input, nil
}

func decodePromptInput(record promptInputRecord) (*PromptInput, error) {
	input := &PromptInput{ID: record.ID, SessionID: record.SessionID, TaskID: record.TaskID, Agent: record.Agent, CallIndex: record.CallIndex, Status: record.Status, CreatedAt: record.CreatedAt}
	if len(record.Sources)+len(record.Locators) > maxPromptInputBytes {
		return nil, errors.New("stored prompt input exceeds its read limit")
	}
	if err := json.Unmarshal([]byte(record.Sources), &input.Sources); err != nil {
		return nil, errors.New("stored prompt input sources are invalid")
	}
	var locators map[string]PromptLocator
	if err := json.Unmarshal([]byte(record.Locators), &locators); err != nil {
		return nil, errors.New("stored prompt input locators are invalid")
	}
	if len(input.Sources) > maxPromptInputSources {
		return nil, errors.New("stored prompt input contains too many sources")
	}
	for i := range input.Sources {
		input.Sources[i].Locator = locators[input.Sources[i].ID]
	}
	return input, nil
}

// RecentPromptInputs only reads an already migrated table. It never initializes
// schema, imports legacy memories, or constructs another context manager.
func (s *Service) RecentPromptInputs(ctx context.Context, owner Owner, sessionID string, limit int) ([]PromptInput, error) {
	query, _, err := s.scoped(ctx, owner)
	if err != nil {
		return nil, err
	}
	if sessionID != "" && (owner.SessionID == "" || sessionID != owner.SessionID) {
		return nil, errors.New("prompt input session does not match the trusted owner")
	}
	limit = min(100, max(1, limit))
	query = query.Where("created_at >= ?", time.Now().Add(-promptInputRetention))
	if sessionID != "" {
		query = query.Where("session_id = ?", sessionID)
	}
	var records []promptInputRecord
	if err := query.Order("created_at DESC, call_index DESC, id DESC").Limit(limit).Find(&records).Error; err != nil {
		return nil, err
	}
	inputs := make([]PromptInput, 0, len(records))
	for _, record := range records {
		input, err := decodePromptInput(record)
		if err != nil {
			return nil, err
		}
		inputs = append(inputs, *input)
	}
	return inputs, nil
}

func (s *Service) GetPromptInput(ctx context.Context, owner Owner, id string) (*PromptInput, error) {
	query, _, err := s.scoped(ctx, owner)
	if err != nil {
		return nil, err
	}
	var record promptInputRecord
	err = query.Where("id = ? AND created_at >= ?", id, time.Now().Add(-promptInputRetention)).First(&record).Error
	if err != nil {
		return nil, err
	}
	return decodePromptInput(record)
}

// UpdatePromptInputStatus records the outcome of the same provider request.
// It cannot replace its captured excerpts or internal source locators.
func (s *Service) UpdatePromptInputStatus(ctx context.Context, owner Owner, id, status string) error {
	_, namespace, err := s.scoped(ctx, owner)
	if err != nil {
		return err
	}
	if s.global || id == "" || len(id) > 64 || status != "attempted" && status != "succeeded" && status != "failed" {
		return errors.New("invalid prompt input status update")
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var record promptInputRecord
		if err := tx.Select("id", "status").Where("namespace = ? AND id = ?", namespace, id).First(&record).Error; err != nil {
			return err
		}
		if record.Status == status {
			return nil
		}
		result := tx.Model(&promptInputRecord{}).Where("namespace = ? AND id = ? AND status = ?", namespace, id, record.Status).UpdateColumn("status", status)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrPromptSourceConflict
		}
		return nil
	})
}

type CurrentSource struct {
	Content   string `json:"content"`
	Revision  string `json:"revision"`
	Digest    string `json:"digest"`
	Editable  bool   `json:"editable"`
	Deletable bool   `json:"deletable"`
	Missing   bool   `json:"missing"`
}

func promptContentDigest(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}

func promptEntrySource(entry Entry) *CurrentSource {
	if entry.State == "deleted" {
		return &CurrentSource{Missing: true}
	}
	digest := promptContentDigest(entry.Value)
	return &CurrentSource{Content: entry.Value, Revision: strconv.Itoa(entry.Version) + ":" + digest, Digest: digest, Editable: true, Deletable: true}
}

func promptSummarySource(summary Summary) *CurrentSource {
	digest := promptContentDigest(summary.Content)
	return &CurrentSource{Content: summary.Content, Revision: strconv.FormatInt(summary.SourceRevision, 10) + ":" + digest, Digest: digest, Editable: summary.Content != "", Deletable: summary.Content != "", Missing: summary.Content == ""}
}

func promptContextSource(item *ContextItem) *CurrentSource {
	if item == nil {
		return &CurrentSource{Missing: true}
	}
	data, _ := json.Marshal(item)
	return &CurrentSource{Content: item.Value, Revision: promptContentDigest(string(data)), Digest: promptContentDigest(item.Value), Editable: true, Deletable: true}
}

func (s *Service) promptContext(owner Owner, locator PromptLocator) (*SessionContextManager, error) {
	if s.global || s.contextStore == nil || locator.Key == "" || locator.SessionID == "" || locator.SessionID != owner.SessionID || locator.Field != "" && locator.Field != "session" {
		return nil, ErrPromptSourceUnsupported
	}
	if canonicalContextStoreDir(s.contextStore.Workspace()) != s.workspace {
		return nil, errors.New("prompt context belongs to another workspace")
	}
	return s.contextStore, nil
}

func (s *Service) promptEntry(ctx context.Context, owner Owner, locator PromptLocator) (*Entry, error) {
	query, _, err := s.scoped(ctx, owner)
	if err != nil {
		return nil, err
	}
	var entry Entry
	if err := query.Where("id = ?", locator.ID).First(&entry).Error; err != nil {
		return nil, err
	}
	if !s.validScope(entry.Scope) || entry.Scope == MemorySession && (owner.SessionID == "" || entry.SessionID != owner.SessionID || locator.SessionID != "" && locator.SessionID != owner.SessionID) {
		return nil, gorm.ErrRecordNotFound
	}
	return &entry, nil
}

func (s *Service) promptSummary(ctx context.Context, owner Owner, locator PromptLocator) (*Summary, error) {
	query, _, err := s.scoped(ctx, owner)
	if err != nil {
		return nil, err
	}
	if s.global || owner.SessionID == "" || locator.SessionID != owner.SessionID {
		return nil, ErrPromptSourceUnsupported
	}
	query = query.Where("session_id = ?", owner.SessionID)
	if locator.ID != "" {
		query = query.Where("id = ?", locator.ID)
	}
	var summary Summary
	if err := query.First(&summary).Error; err != nil {
		return nil, err
	}
	return &summary, nil
}

// ReadPromptSource resolves the original live record, not the historical
// excerpt. The host must separately validate legal Web sessions for locators.
func (s *Service) ReadPromptSource(ctx context.Context, owner Owner, locator PromptLocator) (*CurrentSource, error) {
	if _, _, err := s.scoped(ctx, owner); err != nil {
		return nil, err
	}
	switch locator.Kind {
	case "entry":
		entry, err := s.promptEntry(ctx, owner, locator)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return &CurrentSource{Missing: true}, nil
		}
		if err != nil {
			return nil, err
		}
		return promptEntrySource(*entry), nil
	case "context":
		scm, err := s.promptContext(owner, locator)
		if err != nil {
			return nil, err
		}
		scm.mu.RLock()
		defer scm.mu.RUnlock()
		return promptContextSource(scm.items[contextStorageKey(locator.Key, locator.SessionID, false)]), nil
	case "summary":
		summary, err := s.promptSummary(ctx, owner, locator)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return &CurrentSource{Missing: true}, nil
		}
		if err != nil {
			return nil, err
		}
		return promptSummarySource(*summary), nil
	default:
		return nil, ErrPromptSourceUnsupported
	}
}

// ChangePromptSource uses a revision obtained from ReadPromptSource. A captured
// version may be old: the locator identifies a source, while the freshly read
// revision authorizes changing exactly the current version reviewed by a user.
func (s *Service) ChangePromptSource(ctx context.Context, owner Owner, locator PromptLocator, expectedRevision, action, value string) (*CurrentSource, error) {
	if _, _, err := s.scoped(ctx, owner); err != nil {
		return nil, err
	}
	if expectedRevision == "" || action != "update" && action != "delete" || action == "update" && (strings.TrimSpace(value) == "" || len([]rune(value)) > 48000) {
		return nil, errors.New("source change requires a current revision and valid action/value")
	}
	switch locator.Kind {
	case "entry":
		entry, err := s.promptEntry(ctx, owner, locator)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, ErrPromptSourceConflict
			}
			return nil, err
		}
		if entry.State == "deleted" || promptEntrySource(*entry).Revision != expectedRevision {
			return nil, ErrPromptSourceConflict
		}
		owner.ImportLegacy = false
		if action == "delete" {
			_, err = s.ForgetExpected(ctx, owner, entry.Key, entry.Scope, entry.Version)
		} else {
			var tags []string
			if err = json.Unmarshal([]byte(entry.Tags), &tags); err != nil {
				return nil, errors.New("original memory tags are invalid")
			}
			err = s.RecordWithSource(ctx, owner, entry.Key, value, entry.Scope, entry.Kind, entry.State, entry.Priority, tags, entry.Version, WriteSource{Type: "user_explicit", Ref: "prompt-source-manager"})
		}
		if err != nil {
			current, readErr := s.ReadPromptSource(ctx, owner, locator)
			if readErr == nil && current.Revision != expectedRevision {
				return nil, ErrPromptSourceConflict
			}
			return nil, err
		}
		return s.ReadPromptSource(ctx, owner, locator)
	case "context":
		scm, err := s.promptContext(owner, locator)
		if err != nil {
			return nil, err
		}
		return scm.changePromptContext(ctx, locator.Key, locator.SessionID, expectedRevision, action, value)
	case "summary":
		return s.changePromptSummary(ctx, owner, locator, expectedRevision, action, value)
	default:
		return nil, ErrPromptSourceUnsupported
	}
}

func (scm *SessionContextManager) changePromptContext(ctx context.Context, key, sessionID, expectedRevision, action, value string) (*CurrentSource, error) {
	scm.mu.Lock()
	defer scm.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	storageKey := contextStorageKey(key, sessionID, false)
	previous := scm.items[storageKey]
	if previous == nil || promptContextSource(previous).Revision != expectedRevision {
		return nil, ErrPromptSourceConflict
	}
	if action == "delete" {
		delete(scm.items, storageKey)
	} else {
		updated := cloneContextItem(previous)
		updated.Value, updated.UpdatedAt = value, time.Now()
		scm.items[storageKey] = updated
	}
	if err := scm.persistLocked(); err != nil {
		// Persist is atomic. Restore the same in-memory source if the file write
		// failed so the running agent cannot consume a change reported as failed.
		scm.items[storageKey] = previous
		return nil, err
	}
	return promptContextSource(scm.items[storageKey]), nil
}

func (s *Service) changePromptSummary(ctx context.Context, owner Owner, locator PromptLocator, expectedRevision, action, value string) (*CurrentSource, error) {
	if _, err := s.promptSummary(ctx, owner, locator); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrPromptSourceConflict
		}
		return nil, err
	}
	_, namespace, err := s.scoped(ctx, owner)
	if err != nil {
		return nil, err
	}
	var current Summary
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		query := tx.Where("namespace = ? AND session_id = ?", namespace, owner.SessionID)
		if locator.ID != "" {
			query = query.Where("id = ?", locator.ID)
		}
		if err := query.First(&current).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrPromptSourceConflict
			}
			return err
		}
		if current.Content == "" || promptSummarySource(current).Revision != expectedRevision {
			return ErrPromptSourceConflict
		}
		revision, err := nextSummaryRevision(tx, namespace, owner.SessionID)
		if err != nil {
			return err
		}
		// Legacy or externally imported summaries may have a revision ahead of
		// the counter. Raise it transactionally before committing the barrier.
		if revision <= current.SourceRevision {
			revision = current.SourceRevision + 1
			if err := tx.Model(&processingSequence{}).Where("id = ?", memoryHash(namespace, owner.SessionID, "summary")).Update("revision", revision).Error; err != nil {
				return err
			}
		}
		content := value
		if action == "delete" {
			content = ""
		}
		now := time.Now()
		result := tx.Model(&Summary{}).Where("namespace = ? AND id = ? AND COALESCE(source_revision, 0) = ? AND content = ?", namespace, current.ID, current.SourceRevision, current.Content).
			Updates(map[string]interface{}{"content": content, "source_revision": revision, "source_job_id": "", "source_created_at": now, "updated_at": now})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrPromptSourceConflict
		}
		if err := tx.Where("namespace = ? AND source = ?", namespace, "summary").Delete(&recallVector{}).Error; err != nil {
			return err
		}
		// Keep the row (including an empty deletion tombstone) so already
		// queued/running older snapshots cannot recreate it via INSERT/upsert.
		current.Content, current.SourceRevision, current.SourceJobID, current.SourceCreatedAt, current.UpdatedAt = content, revision, "", now, now
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("change original summary: %w", err)
	}
	return promptSummarySource(current), nil
}
