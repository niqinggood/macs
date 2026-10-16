package memory

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ProcessingJob is scoped and durable. Claims use a lease token; a crashed
// process can be retried after expiry without accepting a stale completion.
type ProcessingJob struct {
	ID             string `gorm:"primaryKey;size:64"`
	Namespace      string `gorm:"index;size:64"`
	SessionID      string `gorm:"type:text"`
	Kind           string `gorm:"size:16"`
	Payload        string `gorm:"type:text"`
	State          string `gorm:"index;size:16"`
	Attempts       int
	SourceRevision int64  `gorm:"not null;default:0"`
	CommitRevision int64  `gorm:"not null;default:0"`
	LastError      string `gorm:"size:32"`
	LeaseToken     string `gorm:"size:64"`
	LeaseUntil     time.Time
	AvailableAt    time.Time
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

func (ProcessingJob) TableName() string { return "memory_processing_jobs" }

func (s *Service) Enqueue(ctx context.Context, owner Owner, kind string, payload interface{}) error {
	if s != nil && s.global {
		return errors.New("shared memory does not accept automatic processing")
	}
	_, namespace, err := s.scoped(ctx, owner)
	if err != nil {
		return err
	}
	if owner.SessionID == "" || kind != "summary" && kind != "derive" {
		return errors.New("invalid memory processing job")
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if len(data) > 2*1024*1024 {
		return errors.New("memory processing payload exceeds 2 MiB")
	}
	now := time.Now()
	job := ProcessingJob{ID: memoryHash(namespace, owner.SessionID, kind, string(data)), Namespace: namespace, SessionID: owner.SessionID,
		Kind: kind, Payload: string(data), State: "pending", AvailableAt: now, CreatedAt: now, UpdatedAt: now}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		insert := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&job)
		if insert.Error != nil || insert.RowsAffected == 0 {
			return insert.Error
		}
		if kind == "summary" {
			revision, err := nextSummaryRevision(tx, namespace, owner.SessionID)
			if err != nil {
				return err
			}
			return tx.Model(&ProcessingJob{}).Where("id = ? AND namespace = ?", job.ID, namespace).Update("source_revision", revision).Error
		}
		return nil
	})
}

// ProcessPending handles a bounded batch. It is called on the next permitted
// user turn as well as after enqueue, so no independent unbounded worker exists.
func (s *Service) ProcessPending(ctx context.Context, owner Owner, handle func(context.Context, ProcessingJob) error) error {
	return s.processPending(ctx, owner, "", handle)
}

func (s *Service) processPending(ctx context.Context, owner Owner, kind string, handle func(context.Context, ProcessingJob) error) error {
	query, namespace, err := s.scoped(ctx, owner)
	if err != nil {
		return err
	}
	if handle == nil {
		return errors.New("memory job handler is unavailable")
	}
	now := time.Now()
	// Retire exhausted jobs only after a prior lease expires. Never invalidate
	// a still-running worker merely because its attempt counter is at the cap.
	if err := s.db.WithContext(ctx).Model(&ProcessingJob{}).
		Where("namespace = ? AND attempts >= ? AND (state = ? OR (state = ? AND lease_until <= ?))", namespace, MaxProcessingAttempts, "pending", "running", now).
		Updates(map[string]interface{}{"state": "failed", "lease_token": "", "last_error": "attempts_exhausted", "updated_at": now}).Error; err != nil {
		return err
	}
	var jobs []ProcessingJob
	if kind != "" {
		query = query.Where("kind = ?", kind)
	} else {
		// The legacy handler knows summary/derive only. It must not claim a
		// review job and accidentally send its payload to the graph deriver.
		query = query.Where("kind IN ?", []string{"summary", "derive"})
	}
	if err := query.Where("attempts < ? AND ((state = ? AND available_at <= ?) OR (state = ? AND lease_until <= ?))", MaxProcessingAttempts, "pending", now, "running", now).
		Order("attempts ASC, available_at ASC, created_at ASC, id ASC").Limit(4).Find(&jobs).Error; err != nil {
		return err
	}
	var handledErr error
	for _, job := range jobs {
		if err := ctx.Err(); err != nil {
			return err
		}
		claimTime := time.Now()
		var tokenBytes [16]byte
		if _, err := rand.Read(tokenBytes[:]); err != nil {
			return err
		}
		token := hex.EncodeToString(tokenBytes[:])
		claim := s.db.WithContext(ctx).Model(&ProcessingJob{}).Where("namespace = ? AND id = ? AND attempts < ? AND ((state = ? AND available_at <= ?) OR (state = ? AND lease_until <= ?))", namespace, job.ID, MaxProcessingAttempts, "pending", claimTime, "running", claimTime).
			Updates(map[string]interface{}{"state": "running", "lease_token": token, "lease_until": claimTime.Add(3 * time.Minute), "attempts": gorm.Expr("attempts + 1"), "updated_at": claimTime})
		if claim.Error != nil {
			return claim.Error
		}
		if claim.RowsAffected == 0 {
			continue
		}
		// A competing worker may have retried since our batch was selected.
		// Read the claimed row, including its actual attempt counter.
		if err := s.db.WithContext(ctx).Where("id = ? AND namespace = ? AND state = ? AND lease_token = ?", job.ID, namespace, "running", token).First(&job).Error; err != nil {
			return err
		}
		workCtx, cancel := context.WithTimeout(context.WithValue(ctx, processingClaimKey{}, job), 2*time.Minute)
		workErr := handle(workCtx, job)
		cancel()
		updates := map[string]interface{}{"state": "done", "payload": "", "lease_token": "", "last_error": "", "updated_at": time.Now()}
		if workErr != nil {
			updates["state"] = "pending"
			updates["payload"] = job.Payload
			updates["available_at"] = time.Now().Add(processingRetryDelay(job.Attempts))
			updates["last_error"] = "handler_failed"
			if errors.Is(workErr, context.DeadlineExceeded) {
				updates["last_error"] = "timeout"
			} else if errors.Is(workErr, context.Canceled) {
				updates["last_error"] = "cancelled"
			}
			if errors.Is(workErr, ErrMemorySuperseded) {
				updates["state"], updates["payload"], updates["last_error"] = "superseded", "", ""
			} else if errors.Is(workErr, errPermanentProcessing) || job.Attempts >= MaxProcessingAttempts {
				updates["state"] = "failed"
				if errors.Is(workErr, errPermanentProcessing) {
					updates["last_error"] = "invalid_payload"
				}
			}
			if errors.Is(workErr, ErrReviewBudget) {
				updates["state"], updates["last_error"] = "pending", "daily_budget"
				updates["attempts"] = job.Attempts - 1
				updates["available_at"] = time.Now().UTC().Truncate(24 * time.Hour).Add(24 * time.Hour)
			}
		}
		// Cancellation leaves the durable claim for lease recovery. Never detach
		// a caller's deadline merely to make a database write appear successful.
		if err := ctx.Err(); err != nil {
			return err
		}
		result := s.db.WithContext(ctx).Model(&ProcessingJob{}).Where("namespace = ? AND id = ? AND state = ? AND lease_token = ?", namespace, job.ID, "running", token).Updates(updates)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return errors.New("memory processing lease was superseded")
		}
		if workErr != nil && !errors.Is(workErr, ErrMemorySuperseded) {
			handledErr = errors.Join(handledErr, fmt.Errorf("memory %s job %s: %s", job.Kind, job.ID, updates["last_error"]))
		}
	}
	return handledErr
}

const MaxProcessingAttempts = 5

type processingClaimKey struct{}

var errPermanentProcessing = errors.New("permanent memory processing failure")

// PermanentProcessingError is for payload/schema errors, not provider outages.
func PermanentProcessingError(err error) error {
	return fmt.Errorf("%w: %v", errPermanentProcessing, err)
}

func processingRetryDelay(attempt int) time.Duration {
	return min(15*time.Minute, 30*time.Second*time.Duration(1<<min(5, max(0, attempt-1))))
}
