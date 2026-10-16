package memory

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// A transactional per-namespace/session counter orders newly queued snapshots
// without relying on wall-clock order or completion time.
type processingSequence struct {
	ID       string `gorm:"primaryKey;size:64"`
	Revision int64  `gorm:"not null;default:0"`
}

func (processingSequence) TableName() string { return "memory_processing_sequences" }

func nextSummaryRevision(tx *gorm.DB, namespace, session string) (int64, error) {
	row := processingSequence{ID: memoryHash(namespace, session, "summary"), Revision: 1}
	if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "id"}}, DoUpdates: clause.Assignments(map[string]interface{}{"revision": gorm.Expr("revision + 1")})}).Create(&row).Error; err != nil {
		return 0, err
	}
	if err := tx.First(&row, "id = ?", row.ID).Error; err != nil {
		return 0, err
	}
	return row.Revision, nil
}

var ErrMemorySuperseded = errors.New("memory source snapshot was superseded")
var ErrMemoryLeaseLost = errors.New("memory processing lease is no longer current")

func (s *Service) LoadSummary(ctx context.Context, owner Owner) (string, error) {
	query, _, err := s.scoped(ctx, owner)
	if err != nil {
		return "", err
	}
	if owner.SessionID == "" {
		return "", errors.New("summary requires a session")
	}
	var summary Summary
	err = query.Where("session_id = ?", owner.SessionID).First(&summary).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "", nil
	}
	return summary.Content, err
}

// SaveSummary is the queued summary's authoritative commit. The worker's
// private context binds its lease and source revision to the write. Checking
// the lease by UPDATE in the same transaction also serializes lease takeover.
func (s *Service) SaveSummary(ctx context.Context, owner Owner, content string) error {
	if s != nil && s.global {
		return errors.New("shared memory does not accept automatic summaries")
	}
	_, namespace, err := s.scoped(ctx, owner)
	if err != nil {
		return err
	}
	if owner.SessionID == "" {
		return errors.New("summary requires a session")
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now := time.Now()
		summary := Summary{ID: memoryHash(namespace, owner.SessionID), Namespace: namespace, SessionID: owner.SessionID, Content: truncateSessionRunes(content, 48000), UpdatedAt: now, SourceCreatedAt: now}
		if claim, ok := ctx.Value(processingClaimKey{}).(ProcessingJob); ok {
			if claim.Namespace != namespace || claim.SessionID != owner.SessionID || claim.Kind != "summary" || claim.LeaseToken == "" {
				return ErrMemoryLeaseLost
			}
			fence := tx.Model(&ProcessingJob{}).Where("id = ? AND namespace = ? AND state = ? AND lease_token = ? AND lease_until > ?", claim.ID, namespace, "running", claim.LeaseToken, now).
				UpdateColumn("lease_token", claim.LeaseToken)
			if fence.Error != nil {
				return fence.Error
			}
			if fence.RowsAffected != 1 {
				return ErrMemoryLeaseLost
			}
			summary.SourceRevision, summary.SourceCreatedAt, summary.SourceJobID = claim.SourceRevision, claim.CreatedAt, claim.ID
		} else {
			revision, err := nextSummaryRevision(tx, namespace, owner.SessionID)
			if err != nil {
				return err
			}
			summary.SourceRevision = revision
		}
		// Legacy queued rows have revision zero. They may replace only older
		// legacy summaries, never a versioned write. UpdatedAt is a conservative
		// watermark for pre-migration summaries without source metadata.
		condition := `memory_summaries.source_revision < excluded.source_revision OR
            (memory_summaries.source_revision = excluded.source_revision AND excluded.source_revision > 0 AND memory_summaries.source_job_id = excluded.source_job_id) OR
            (memory_summaries.source_revision = 0 AND excluded.source_revision = 0 AND
              ((COALESCE(memory_summaries.source_job_id, '') <> '' AND memory_summaries.source_created_at < excluded.source_created_at) OR
               (COALESCE(memory_summaries.source_job_id, '') = '' AND memory_summaries.updated_at < excluded.source_created_at)))`
		result := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "id"}}, DoUpdates: clause.AssignmentColumns([]string{"content", "source_revision", "source_created_at", "source_job_id", "updated_at"}), Where: clause.Where{Exprs: []clause.Expression{clause.Expr{SQL: condition}}}}).Create(&summary)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrMemorySuperseded
		}
		return nil
	})
}
