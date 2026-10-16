package memory

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// SourceRecallSetting controls automatic recall of an original record. It does
// not erase that record, restrict explicit tool reads, or hide conversation
// messages. An absent setting is enabled at version zero.
type SourceRecallSetting struct {
	Enabled   bool      `json:"enabled"`
	Version   int       `json:"version"`
	UpdatedAt time.Time `json:"updated_at,omitempty"`
}

// Only hashes of the trusted namespace and original address are persisted;
// copied source text, keys, session identifiers and prompt digests are not.
type sourceRecallRecord struct {
	ID        string `gorm:"primaryKey;size:64"`
	Namespace string `gorm:"index;size:64;not null"`
	SourceKey string `gorm:"size:64;not null"`
	Enabled   bool   `gorm:"not null"`
	Version   int    `gorm:"not null"`
	UpdatedAt time.Time
}

func (sourceRecallRecord) TableName() string { return "memory_source_recall_settings" }

func sourceRecallKind(kind string) bool {
	switch kind {
	case "memory_event", "knowledge", "knowledge_fact", "summary", "chat_message":
		return true
	default:
		return false
	}
}

func validSourceRecallIdentifier(value string) bool {
	return value != "" && len(value) <= 256 && utf8.ValidString(value) && strings.TrimSpace(value) == value &&
		strings.IndexFunc(value, unicode.IsControl) == -1
}

// SourceRecallKey is a stable original-record identity, not an authorization
// token. The host must resolve and authorize a PromptLocator before using it.
// All fields of the record share one setting: editing a digest or changing
// from an event's final answer to its insights cannot reactivate recall.
// Unsupported or malformed locators return an empty key.
func SourceRecallKey(locator PromptLocator) string {
	if !sourceRecallKind(locator.Kind) || !validSourceRecallIdentifier(locator.ID) || !validSourceRecallIdentifier(locator.SessionID) || locator.Path != "" {
		return ""
	}
	if locator.Kind != "summary" {
		id, err := strconv.ParseUint(locator.ID, 10, 64)
		if err != nil || id == 0 || strconv.FormatUint(id, 10) != locator.ID {
			return ""
		}
	}
	return memoryHash("source-recall-v1", locator.Kind, locator.ID, locator.SessionID)
}

// SourceRecallSettings reads one bounded batch without changing source data or
// importing legacy state. Settings use the service's existing owner namespace.
func (s *Service) SourceRecallSettings(ctx context.Context, owner Owner, locators []PromptLocator) (map[string]SourceRecallSetting, error) {
	_, namespace, err := s.scoped(ctx, owner)
	if err != nil {
		return nil, err
	}
	if s.global || len(locators) > 4096 {
		return nil, ErrPromptSourceUnsupported
	}
	settings := make(map[string]SourceRecallSetting, len(locators))
	ids := make([]string, 0, len(locators))
	for _, locator := range locators {
		key := SourceRecallKey(locator)
		if key == "" {
			return nil, ErrPromptSourceUnsupported
		}
		if _, exists := settings[key]; !exists {
			settings[key] = SourceRecallSetting{Enabled: true}
			ids = append(ids, memoryHash(namespace, key))
		}
	}
	for start := 0; start < len(ids); start += 200 {
		var rows []sourceRecallRecord
		if err := s.db.WithContext(ctx).Where("namespace = ? AND id IN ?", namespace, ids[start:min(start+200, len(ids))]).Find(&rows).Error; err != nil {
			return nil, err
		}
		for _, row := range rows {
			if _, requested := settings[row.SourceKey]; !requested || row.ID != memoryHash(namespace, row.SourceKey) || row.Version < 1 {
				return nil, errors.New("invalid stored source recall setting")
			}
			settings[row.SourceKey] = SourceRecallSetting{Enabled: row.Enabled, Version: row.Version, UpdatedAt: row.UpdatedAt}
		}
	}
	return settings, nil
}

// SetSourceRecallEnabled uses mandatory compare-and-swap, including the absent
// version zero. It changes only the setting; original rows and vector caches
// remain available for an explicit read or a later re-enable.
func (s *Service) SetSourceRecallEnabled(ctx context.Context, owner Owner, locator PromptLocator, enabled bool, expectedVersion int) (SourceRecallSetting, error) {
	_, namespace, err := s.scoped(ctx, owner)
	if err != nil {
		return SourceRecallSetting{}, err
	}
	key := SourceRecallKey(locator)
	if s.global || key == "" {
		return SourceRecallSetting{}, ErrPromptSourceUnsupported
	}
	if expectedVersion < 0 || expectedVersion == int(^uint(0)>>1) {
		return SourceRecallSetting{}, errors.New("source recall requires a nonnegative current version")
	}
	now := time.Now().UTC()
	record := sourceRecallRecord{ID: memoryHash(namespace, key), Namespace: namespace, SourceKey: key, Enabled: enabled, Version: expectedVersion + 1, UpdatedAt: now}
	if expectedVersion == 0 {
		// A real INSERT, rather than ON CONFLICT DO NOTHING, preserves version
		// zero CAS on MySQL even when clientFoundRows reports matched no-ops as
		// affected rows. The primary key arbitrates concurrent initial writers.
		result := s.db.WithContext(ctx).Create(&record)
		if result.Error != nil {
			var count int64
			if s.db.WithContext(ctx).Model(&sourceRecallRecord{}).Where("namespace = ? AND id = ?", namespace, record.ID).Count(&count).Error == nil && count > 0 {
				return SourceRecallSetting{}, ErrPromptSourceConflict
			}
			return SourceRecallSetting{}, result.Error
		}
		if result.RowsAffected != 1 {
			return SourceRecallSetting{}, ErrPromptSourceConflict
		}
	} else {
		result := s.db.WithContext(ctx).Model(&sourceRecallRecord{}).
			Where("namespace = ? AND id = ? AND version = ?", namespace, record.ID, expectedVersion).
			Updates(map[string]interface{}{"enabled": enabled, "version": record.Version, "updated_at": now})
		if result.Error != nil {
			return SourceRecallSetting{}, result.Error
		}
		if result.RowsAffected != 1 {
			return SourceRecallSetting{}, ErrPromptSourceConflict
		}
	}
	return SourceRecallSetting{Enabled: enabled, Version: record.Version, UpdatedAt: now}, nil
}

// Read settings after slow providers and candidate refresh, immediately before
// prompt assembly. A failed settings read fails recall closed, never enabled.
func (s *Service) filterSourceRecallItems(ctx context.Context, owner Owner, items []RecallItem, result *RecallResult) ([]RecallItem, error) {
	var locators []PromptLocator
	for _, item := range items {
		if sourceRecallKind(item.Locator.Kind) {
			locators = append(locators, item.Locator)
		}
	}
	if len(locators) == 0 {
		return items, nil
	}
	settings, err := s.SourceRecallSettings(ctx, owner, locators)
	if err != nil {
		return nil, err
	}
	filtered := make([]RecallItem, 0, len(items))
	for _, item := range items {
		if setting, exists := settings[SourceRecallKey(item.Locator)]; exists && !setting.Enabled {
			result.decide(item, "disabled_by_user", false, 0)
			continue
		}
		filtered = append(filtered, item)
	}
	return filtered, nil
}
