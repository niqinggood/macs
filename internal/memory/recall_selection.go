package memory

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
)

// Pinned preferences are selected by the user through the management surface,
// never inferred from priority or exposed as a model-controlled tool argument.
const MaxPinnedPreferences = 5

func (s *Service) SetPinned(ctx context.Context, owner Owner, key string, scope MemoryScope, version int, pinned bool) error {
	if s == nil || scope != s.preferenceScope() || version < 1 {
		return errors.New("only current user preferences can be pinned")
	}
	if err := s.validateRecord(key, "pin", scope, owner.SessionID); err != nil {
		return err
	}
	if err := s.Import(ctx, owner); err != nil {
		return err
	}
	query, namespace, err := s.scoped(ctx, owner)
	if err != nil {
		return err
	}
	query = query.Model(&Entry{}).Where("key = ? AND scope = ? AND kind = ? AND state = ? AND version = ?", key, scope, "preference", "active", version)
	if pinned {
		// Check the cap in the same statement as the write, not in a prior read.
		query = query.Where("(pinned = ? OR (SELECT COUNT(*) FROM memory_entries WHERE namespace = ? AND scope = ? AND kind = ? AND state = ? AND pinned = ?) < ?)", true, namespace, scope, "preference", "active", true, MaxPinnedPreferences)
	}
	result := query.Updates(map[string]interface{}{"pinned": pinned, "version": gorm.Expr("version + 1"), "updated_at": time.Now()})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return errors.New("memory changed, is not an active user preference, or the pinned limit was reached")
	}
	return nil
}

func (s *Service) pinnedEntries(ctx context.Context, owner Owner) ([]Entry, error) {
	query, _, err := s.scoped(ctx, owner)
	if err != nil {
		return nil, err
	}
	var entries []Entry
	err = query.Where("scope = ? AND kind = ? AND state = ? AND pinned = ?", s.preferenceScope(), "preference", "active", true).
		Order("updated_at DESC, id ASC").Limit(MaxPinnedPreferences).Find(&entries).Error
	return entries, err
}

// RecallDecision intentionally has no content, key, question or raw session ID.
// Selected means included in the envelope, not proven use by the model.
type RecallDecision struct {
	ID        string `json:"id"`
	Source    string `json:"source"`
	Version   int    `json:"version"`
	Relevance int    `json:"relevance"`
	Rank      int    `json:"rank"`
	Reason    string `json:"reason"`
	Selected  bool   `json:"selected"`
	Tokens    int    `json:"tokens"`
}

const maxRecallDecisions = 512

func (r *RecallResult) decide(item RecallItem, reason string, selected bool, tokens int) {
	if len(r.Decisions) >= maxRecallDecisions {
		r.OmittedDecisions++
		if selected {
			for i := len(r.Decisions) - 1; i >= 0; i-- {
				if !r.Decisions[i].Selected {
					r.Decisions[i] = RecallDecision{ID: item.ID, Source: item.Source, Version: item.Version, Relevance: item.Relevance, Rank: item.Rank, Reason: reason, Selected: true, Tokens: tokens}
					break
				}
			}
		}
		return
	}
	r.Decisions = append(r.Decisions, RecallDecision{ID: item.ID, Source: item.Source, Version: item.Version,
		Relevance: item.Relevance, Rank: item.Rank, Reason: reason, Selected: selected, Tokens: tokens})
}
