package memory

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (s *Service) commitReview(ctx context.Context, owner Owner, inputs []reviewInput, proposals []ReviewProposal) error {
	_, namespace, err := s.scoped(ctx, owner)
	if err != nil {
		return err
	}
	claim, ok := ctx.Value(processingClaimKey{}).(ProcessingJob)
	if !ok || claim.Kind != ReviewJobKind || claim.Namespace != namespace || claim.LeaseToken == "" {
		return ErrMemoryLeaseLost
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if s.reviewAllowed != nil && !s.reviewAllowed() {
			return ErrMemorySuperseded
		}
		// A real increment fences the write on MySQL as well as SQLite; do not
		// depend on affected-row behavior of a no-op UPDATE.
		fence := tx.Model(&ProcessingJob{}).Where("id = ? AND namespace = ? AND state = ? AND lease_token = ? AND lease_until > ?",
			claim.ID, namespace, "running", claim.LeaseToken, time.Now()).UpdateColumn("commit_revision", gorm.Expr("commit_revision + 1"))
		if fence.Error != nil {
			return fence.Error
		}
		if fence.RowsAffected != 1 {
			return ErrMemoryLeaseLost
		}
		for _, proposal := range proposals {
			evidence, valid := validateReviewProposal(proposal, inputs)
			if !valid {
				continue // Unsupported claims do not invalidate supported ones.
			}
			for _, proof := range evidence {
				var source reviewSource
				if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("namespace = ? AND id = ?", namespace, proof.SourceID).First(&source).Error; err != nil {
					return err
				}
				if source.Retired || source.Digest != proof.SourceDigest {
					return ErrMemorySuperseded
				}
			}
			content, when := strings.TrimSpace(proposal.Content), strings.TrimSpace(proposal.WhenToUse)
			scope := MemoryProject
			if proposal.Kind == "preference" {
				scope = MemoryUser
			}
			fingerprint := memoryHash(string(scope), proposal.Kind, normalizedMemory(content), normalizedMemory(when))
			key := truncateSessionRunes(content, 60) + " · " + fingerprint[:8]
			id := memoryHash(namespace, agentMemoryStorageKey(key, scope, ""))
			var old Entry
			err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("namespace = ? AND id = ?", namespace, id).First(&old).Error
			if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			if old.ID != "" && (old.State != "candidate" || old.Value != content || old.WhenToUse != when) {
				continue // Never overwrite a confirmation, edit or tombstone.
			}
			if old.ID == "" {
				now := time.Now().UTC()
				tags := []string{"agent-review"}
				if proposal.Habit {
					tags = append(tags, "habit-candidate")
				}
				encodedTags, _ := json.Marshal(tags)
				entry := Entry{ID: id, Namespace: namespace, TenantID: owner.TenantID, OwnerID: owner.SubjectID, AgentName: s.agent,
					Key: key, Value: content, WhenToUse: when, Scope: scope, Kind: proposal.Kind, State: "candidate", Priority: 5, Version: 1,
					Tags: string(encodedTags), SourceSessionID: inputs[0].source.SessionID, SourceType: "agent_candidate",
					SourceRef: inputs[0].source.TaskID, SourceQuote: evidence[0].Quote, CreatedAt: now, UpdatedAt: now}
				insert := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&entry)
				if insert.Error != nil {
					return insert.Error
				}
				if insert.RowsAffected != 1 {
					return ErrMemorySuperseded
				}
				if err := saveMemoryChange(tx, namespace, id, 1, "proposed"); err != nil {
					return err
				}
			}
			var count int64
			if err := tx.Model(&Evidence{}).Where("namespace = ? AND entry_id = ?", namespace, id).Count(&count).Error; err != nil {
				return err
			}
			for _, proof := range evidence {
				if count >= 60 {
					break
				}
				proof.EntryID = id
				proof.ID = memoryHash(namespace, id, proof.SourceID, proof.Role, normalizedMemory(proof.Quote))
				insert := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&proof)
				if insert.Error != nil {
					return insert.Error
				}
				count += insert.RowsAffected
			}
		}
		return nil
	})
}

func validateReviewProposal(proposal ReviewProposal, inputs []reviewInput) ([]Evidence, bool) {
	content, when := strings.TrimSpace(proposal.Content), strings.TrimSpace(proposal.WhenToUse)
	if content == "" || when == "" || len([]rune(content)) > 1000 || len([]rune(when)) > 300 ||
		proposal.Kind != "preference" && proposal.Kind != "fact" && proposal.Kind != "experience" ||
		len(proposal.Evidence) == 0 || len(proposal.Evidence) > 12 ||
		explicitMemoryCredential.MatchString(content+" "+when) || explicitMemoryInjection.Scan(content+" "+when, "review_proposal").Severity != 0 {
		return nil, false
	}
	var result []Evidence
	tasks := map[string]bool{}
	current := false
	for _, quote := range proposal.Evidence {
		if len([]rune(quote.Quote)) < 3 || len([]rune(quote.Quote)) > 300 || strings.TrimSpace(quote.Quote) == "" || explicitMemoryCredential.MatchString(quote.Quote) {
			return nil, false
		}
		found := false
		for index, input := range inputs {
			if input.Ref != quote.SourceRef {
				continue
			}
			for _, proof := range input.Proofs {
				if proof.Ref != quote.ProofRef || !strings.Contains(proof.Text, quote.Quote) || (proposal.Kind == "preference" || proposal.Habit) && proof.Role != "user" {
					continue
				}
				if proposal.Kind == "preference" && !proposal.Habit && !reviewDurablePreference(proof.Text) {
					continue
				}
				result = append(result, Evidence{Namespace: input.source.Namespace, SourceID: input.source.ID, SourceDigest: input.source.Digest,
					TaskID: input.source.TaskID, SessionID: input.source.SessionID, Role: proof.Role, Quote: quote.Quote, CreatedAt: input.source.CreatedAt})
				tasks[input.source.TaskID] = true
				current = current || index == 0
				found = true
				break
			}
			break
		}
		if !found {
			return nil, false
		}
	}
	return result, current && (!proposal.Habit || proposal.Kind == "preference" && len(tasks) >= 3)
}

func reviewDurablePreference(text string) bool {
	if _, _, _, explicit := explicitUserMemoryInstruction(strings.TrimSpace(text)); explicit {
		return true
	}
	lower := strings.ToLower(text)
	for _, marker := range []string{"以后", "今后", "从现在开始", "going forward", "from now on"} {
		if strings.Contains(lower, marker) {
			return true // Complex future instructions still require confirmation.
		}
	}
	for _, marker := range []string{"这次", "本次", "暂时", "临时", "this time", "for now", "temporarily"} {
		if strings.Contains(lower, marker) {
			return false
		}
	}
	for _, marker := range []string{"我喜欢", "我偏好", "我的习惯", "i prefer", "my preference"} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

// EvidenceForEntry authorizes via the service namespace before exposing quotes.
// It does not import data or run any model.
func (s *Service) EvidenceForEntry(ctx context.Context, owner Owner, id string) ([]Evidence, error) {
	query, namespace, err := s.scoped(ctx, owner)
	if err != nil {
		return nil, err
	}
	var entry Entry
	if err := query.Where("id = ? AND state <> ? AND (scope <> ? OR session_id = ?)", id, "deleted", MemorySession, owner.SessionID).First(&entry).Error; err != nil {
		return nil, err
	}
	var evidence []Evidence
	err = s.db.WithContext(ctx).Where("namespace = ? AND entry_id = ?", namespace, id).Order("created_at DESC, id ASC").Limit(60).Find(&evidence).Error
	return evidence, err
}

func saveMemoryChange(tx *gorm.DB, namespace, id string, version int, action string) error {
	return tx.Create(&memoryChange{Namespace: namespace, EntryID: id, Version: version, Action: action, CreatedAt: time.Now().UTC()}).Error
}

func (s *Service) attachReviewEvidence(ctx context.Context, owner Owner, entries []Entry) error {
	_, namespace, err := s.scoped(ctx, owner)
	if err != nil || len(entries) == 0 {
		return err
	}
	var ids []string
	indices := map[string]int{}
	for i, entry := range entries {
		if entry.SourceType == "agent_candidate" {
			ids = append(ids, entry.ID)
			indices[entry.ID] = i
		}
	}
	if len(ids) == 0 {
		return nil
	}
	var evidence []Evidence
	if err := s.db.WithContext(ctx).Where("namespace = ? AND entry_id IN ?", namespace, ids).Order("created_at ASC, id ASC").Limit(60 * len(ids)).Find(&evidence).Error; err != nil {
		return err
	}
	for _, proof := range evidence {
		if i, ok := indices[proof.EntryID]; ok {
			entries[i].Evidence = append(entries[i].Evidence, proof)
		}
	}
	return nil
}

// Retire the original review inputs, not just a generated Entry ID. Their
// stable task identity survives clearing payloads, restart and reprocessing.
// Other existing memories from those tasks are retained; new automatic
// learning from the retired task is conservatively stopped.
func retireReviewEvidence(tx *gorm.DB, namespace, id string) error {
	var sources []string
	if err := tx.Model(&Evidence{}).Where("namespace = ? AND entry_id = ?", namespace, id).Distinct("source_id").Pluck("source_id", &sources).Error; err != nil {
		return err
	}
	if len(sources) > 0 {
		if err := tx.Model(&reviewSource{}).Where("namespace = ? AND id IN ?", namespace, sources).
			Updates(map[string]interface{}{"retired": true, "payload": ""}).Error; err != nil {
			return err
		}
	}
	return tx.Where("namespace = ? AND entry_id = ?", namespace, id).Delete(&Evidence{}).Error
}
