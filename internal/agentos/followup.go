package agentos

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	TaskFollowupStatusPending    = "pending"
	TaskFollowupStatusDelivering = "delivering"
	TaskFollowupStatusApplied    = "applied"
	TaskFollowupStatusCancelled  = "cancelled"
)

var (
	ErrTaskFollowupsClosed  = errors.New("AgentOS task no longer accepts follow-ups")
	ErrTaskFollowupNotFound = errors.New("AgentOS task follow-up was not found")
)

// TaskFollowupState is the serialization boundary shared by follow-up
// acceptance, application, task completion, and cancellation. Updating this
// row first makes those transitions race-safe on SQLite, MySQL, and Postgres
// without relying on dialect-specific SELECT FOR UPDATE behavior.
type TaskFollowupState struct {
	TaskID        string    `gorm:"column:task_id;primaryKey;size:64" json:"task_id"`
	Open          bool      `gorm:"column:open;index" json:"open"`
	AcceptedCount int64     `gorm:"column:accepted_count" json:"accepted_count"`
	AppliedCount  int64     `gorm:"column:applied_count" json:"applied_count"`
	CreatedAt     time.Time `gorm:"column:created_at" json:"created_at"`
	UpdatedAt     time.Time `gorm:"column:updated_at" json:"updated_at"`
}

func (TaskFollowupState) TableName() string { return "agent_task_followup_states" }

// TaskFollowupRecord is the durable receipt for one externally supplied
// instruction. DeliveryLeaseToken fences a claimed instruction to the worker
// that currently owns the parent task.
type TaskFollowupRecord struct {
	ID                 uint       `gorm:"column:id;primaryKey" json:"-"`
	FollowupID         string     `gorm:"column:followup_id;uniqueIndex;size:64" json:"followup_id"`
	TaskID             string     `gorm:"column:task_id;index:idx_task_followups_order,priority:1;size:64" json:"task_id"`
	TenantID           string     `gorm:"column:tenant_id;index;size:64;default:default" json:"tenant_id"`
	SubjectID          string     `gorm:"column:subject_id;index;size:128" json:"subject_id,omitempty"`
	RoleID             string     `gorm:"column:role_id;index;size:128" json:"role_id,omitempty"`
	CallerID           string     `gorm:"column:caller_id;index;size:128" json:"caller_id"`
	Message            string     `gorm:"column:message;type:text" json:"message"`
	Status             string     `gorm:"column:status;index;size:20" json:"status"`
	IdempotencyHash    *string    `gorm:"column:idempotency_hash;uniqueIndex;size:64" json:"-"`
	RequestHash        string     `gorm:"column:request_hash;size:64" json:"-"`
	DeliveryLeaseToken string     `gorm:"column:delivery_lease_token;index;size:64" json:"-"`
	DeliveryAttempt    int        `gorm:"column:delivery_attempt" json:"delivery_attempt"`
	ClaimedAt          *time.Time `gorm:"column:claimed_at" json:"claimed_at,omitempty"`
	AppliedAt          *time.Time `gorm:"column:applied_at" json:"applied_at,omitempty"`
	CancelledAt        *time.Time `gorm:"column:cancelled_at" json:"cancelled_at,omitempty"`
	CreatedAt          time.Time  `gorm:"column:created_at;index:idx_task_followups_order,priority:2" json:"created_at"`
	UpdatedAt          time.Time  `gorm:"column:updated_at" json:"updated_at"`
}

func (TaskFollowupRecord) TableName() string { return "agent_task_followups" }

type TaskFollowupCloseDecision int

const (
	TaskFollowupCloseAlreadyClosed TaskFollowupCloseDecision = iota
	TaskFollowupCloseDrained
	TaskFollowupClosePending
)

func NewTaskFollowupID(now time.Time) string {
	var suffix [4]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return fmt.Sprintf("fup_%s_%x", now.Format("060102150405"), now.UnixNano()&0xffffffff)
	}
	return "fup_" + now.Format("060102150405") + "_" + hex.EncodeToString(suffix[:])
}

func TaskFollowupRequestHash(message string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(message)))
	return hex.EncodeToString(sum[:])
}

// TaskFollowupIdempotencyHash places follow-up keys in a namespace distinct
// from task-submission keys and from every other parent task.
func TaskFollowupIdempotencyHash(
	defaultTenant, tenantID, callerID, subjectID, taskID, key string,
) string {
	return TaskIdempotencyHash(
		defaultTenant,
		tenantID,
		callerID,
		subjectID,
		"followup\x00"+strings.TrimSpace(taskID)+"\x00"+strings.TrimSpace(key),
	)
}

func EnsureTaskFollowupState(db *gorm.DB, taskID string, open bool, now time.Time) error {
	if db == nil {
		return fmt.Errorf("database connection is nil")
	}
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return fmt.Errorf("task id is required")
	}
	now = now.Round(0)
	state := TaskFollowupState{
		TaskID: taskID, Open: open, CreatedAt: now, UpdatedAt: now,
	}
	return db.Clauses(clause.OnConflict{DoNothing: true}).Create(&state).Error
}

// BackfillTaskFollowupStates makes tasks created before this capability obey
// the same completion gate. It is safe to run on every process startup.
func BackfillTaskFollowupStates(db *gorm.DB, now time.Time) (int64, error) {
	if db == nil {
		return 0, fmt.Errorf("database connection is nil")
	}
	var tasks []struct {
		TaskID string `gorm:"column:task_id"`
		Status string `gorm:"column:status"`
	}
	if err := db.Table((taskLeaseRecord{}).TableName()).
		Select("task_id", "status").
		Where("queue_name = ?", QueueName).
		Find(&tasks).Error; err != nil {
		return 0, fmt.Errorf("list AgentOS tasks for follow-up backfill: %w", err)
	}
	if len(tasks) == 0 {
		return 0, nil
	}
	now = now.Round(0)
	states := make([]TaskFollowupState, 0, len(tasks))
	for _, task := range tasks {
		states = append(states, TaskFollowupState{
			TaskID:    task.TaskID,
			Open:      !terminalTaskStatus(task.Status),
			CreatedAt: now,
			UpdatedAt: now,
		})
	}
	result := db.Clauses(clause.OnConflict{DoNothing: true}).CreateInBatches(states, 500)
	if result.Error != nil {
		return 0, fmt.Errorf("backfill AgentOS task follow-up states: %w", result.Error)
	}
	return result.RowsAffected, nil
}

func terminalTaskStatus(status string) bool {
	switch strings.TrimSpace(status) {
	case "completed", "degraded", "failed", "stopped":
		return true
	default:
		return false
	}
}

// ReserveTaskFollowupSlot must be called inside the same transaction that
// creates TaskFollowupRecord. The counter increment is rolled back if record
// creation or task validation fails.
func ReserveTaskFollowupSlot(db *gorm.DB, taskID string, now time.Time) error {
	if db == nil {
		return fmt.Errorf("database connection is nil")
	}
	result := db.Model(&TaskFollowupState{}).
		Where("task_id = ? AND open = ?", strings.TrimSpace(taskID), true).
		Updates(map[string]interface{}{
			"accepted_count": gorm.Expr("accepted_count + 1"),
			"updated_at":     now.Round(0),
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrTaskFollowupsClosed
	}
	return nil
}

// TryCloseTaskFollowups closes an input gate only when every accepted
// instruction is durably applied. Completion must update the task row in the
// same transaction after receiving TaskFollowupCloseDrained.
func TryCloseTaskFollowups(db *gorm.DB, taskID string, now time.Time) (TaskFollowupCloseDecision, error) {
	if db == nil {
		return TaskFollowupCloseAlreadyClosed, fmt.Errorf("database connection is nil")
	}
	taskID = strings.TrimSpace(taskID)
	result := db.Model(&TaskFollowupState{}).
		Where("task_id = ? AND open = ? AND accepted_count = applied_count", taskID, true).
		Updates(map[string]interface{}{"open": false, "updated_at": now.Round(0)})
	if result.Error != nil {
		return TaskFollowupCloseAlreadyClosed, result.Error
	}
	if result.RowsAffected == 1 {
		return TaskFollowupCloseDrained, nil
	}
	var state TaskFollowupState
	if err := db.Where("task_id = ?", taskID).First(&state).Error; err != nil {
		return TaskFollowupCloseAlreadyClosed, err
	}
	if !state.Open {
		return TaskFollowupCloseAlreadyClosed, nil
	}
	return TaskFollowupClosePending, nil
}

// CancelTaskFollowups closes the gate and gives every unapplied instruction a
// durable cancelled receipt. Callers must transition the parent task in the
// same transaction.
func CancelTaskFollowups(db *gorm.DB, taskID string, now time.Time) error {
	if db == nil {
		return fmt.Errorf("database connection is nil")
	}
	taskID = strings.TrimSpace(taskID)
	now = now.Round(0)
	if err := db.Model(&TaskFollowupState{}).
		Where("task_id = ?", taskID).
		Updates(map[string]interface{}{"open": false, "updated_at": now}).Error; err != nil {
		return err
	}
	return db.Model(&TaskFollowupRecord{}).
		Where("task_id = ? AND status IN ?", taskID, []string{
			TaskFollowupStatusPending, TaskFollowupStatusDelivering,
		}).
		Updates(map[string]interface{}{
			"status":               TaskFollowupStatusCancelled,
			"delivery_lease_token": "",
			"cancelled_at":         now,
			"updated_at":           now,
		}).Error
}

func ClaimNextTaskFollowup(
	ctx context.Context,
	db *gorm.DB,
	taskID, taskLeaseToken string,
	now time.Time,
) (*TaskFollowupRecord, bool, error) {
	if db == nil {
		return nil, false, fmt.Errorf("database connection is nil")
	}
	taskID = strings.TrimSpace(taskID)
	taskLeaseToken = strings.TrimSpace(taskLeaseToken)
	if taskID == "" || taskLeaseToken == "" {
		return nil, false, ErrLeaseLost
	}
	if ctx == nil {
		ctx = context.Background()
	}
	now = now.Round(0)
	var candidates []TaskFollowupRecord
	if err := db.WithContext(ctx).
		Where(
			"task_id = ? AND (status = ? OR (status = ? AND delivery_lease_token <> ?))",
			taskID,
			TaskFollowupStatusPending,
			TaskFollowupStatusDelivering,
			taskLeaseToken,
		).
		Order("created_at ASC, id ASC").
		Limit(16).
		Find(&candidates).Error; err != nil {
		return nil, false, err
	}
	for _, candidate := range candidates {
		taskLease := db.WithContext(ctx).Model(&taskLeaseRecord{}).
			Select("1").
			Where(
				"task_id = ? AND queue_name = ? AND status = ? AND lease_token = ?",
				taskID, QueueName, "running", taskLeaseToken,
			)
		result := db.WithContext(ctx).Model(&TaskFollowupRecord{}).
			Where("id = ? AND task_id = ?", candidate.ID, taskID).
			Where(
				"status = ? OR (status = ? AND delivery_lease_token <> ?)",
				TaskFollowupStatusPending,
				TaskFollowupStatusDelivering,
				taskLeaseToken,
			).
			Where("EXISTS (?)", taskLease).
			Updates(map[string]interface{}{
				"status":               TaskFollowupStatusDelivering,
				"delivery_lease_token": taskLeaseToken,
				"delivery_attempt":     gorm.Expr("delivery_attempt + 1"),
				"claimed_at":           now,
				"updated_at":           now,
			})
		if result.Error != nil {
			return nil, false, result.Error
		}
		if result.RowsAffected == 0 {
			continue
		}
		var claimed TaskFollowupRecord
		if err := db.WithContext(ctx).
			Where("id = ? AND delivery_lease_token = ?", candidate.ID, taskLeaseToken).
			First(&claimed).Error; err != nil {
			return nil, false, err
		}
		return &claimed, true, nil
	}
	return nil, false, nil
}

func ReleaseTaskFollowupClaim(
	ctx context.Context,
	db *gorm.DB,
	followupID, taskLeaseToken string,
	now time.Time,
) error {
	if db == nil {
		return fmt.Errorf("database connection is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return db.WithContext(ctx).Model(&TaskFollowupRecord{}).
		Where(
			"followup_id = ? AND status = ? AND delivery_lease_token = ?",
			strings.TrimSpace(followupID), TaskFollowupStatusDelivering, strings.TrimSpace(taskLeaseToken),
		).
		Updates(map[string]interface{}{
			"status":               TaskFollowupStatusPending,
			"delivery_lease_token": "",
			"claimed_at":           nil,
			"updated_at":           now.Round(0),
		}).Error
}

// ApplyClaimedTaskFollowup advances the state counter, marks the receipt, and
// invokes persist within one transaction. A host uses persist to append the
// instruction to its transcript without a crash window between those writes.
func ApplyClaimedTaskFollowup(
	ctx context.Context,
	db *gorm.DB,
	taskID, followupID, taskLeaseToken string,
	now time.Time,
	persist func(*gorm.DB, *TaskFollowupRecord) error,
) (*TaskFollowupRecord, bool, error) {
	if db == nil {
		return nil, false, fmt.Errorf("database connection is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	taskID = strings.TrimSpace(taskID)
	followupID = strings.TrimSpace(followupID)
	taskLeaseToken = strings.TrimSpace(taskLeaseToken)
	if taskID == "" || followupID == "" || taskLeaseToken == "" {
		return nil, false, ErrLeaseLost
	}
	now = now.Round(0)
	var applied TaskFollowupRecord
	newlyApplied := false
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var current TaskFollowupRecord
		if err := tx.Where("followup_id = ? AND task_id = ?", followupID, taskID).First(&current).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrTaskFollowupNotFound
			}
			return err
		}
		if current.Status == TaskFollowupStatusApplied {
			applied = current
			return nil
		}

		delivery := tx.Model(&TaskFollowupRecord{}).
			Select("1").
			Where(
				"followup_id = ? AND task_id = ? AND status = ? AND delivery_lease_token = ?",
				followupID, taskID, TaskFollowupStatusDelivering, taskLeaseToken,
			)
		taskLease := tx.Model(&taskLeaseRecord{}).
			Select("1").
			Where(
				"task_id = ? AND queue_name = ? AND status = ? AND lease_token = ?",
				taskID, QueueName, "running", taskLeaseToken,
			)
		stateUpdate := tx.Model(&TaskFollowupState{}).
			Where("task_id = ? AND open = ? AND applied_count < accepted_count", taskID, true).
			Where("EXISTS (?)", delivery).
			Where("EXISTS (?)", taskLease).
			Updates(map[string]interface{}{
				"applied_count": gorm.Expr("applied_count + 1"),
				"updated_at":    now,
			})
		if stateUpdate.Error != nil {
			return stateUpdate.Error
		}
		if stateUpdate.RowsAffected != 1 {
			var state TaskFollowupState
			if err := tx.Where("task_id = ?", taskID).First(&state).Error; err == nil && !state.Open {
				return ErrTaskFollowupsClosed
			}
			return ErrLeaseLost
		}

		recordUpdate := tx.Model(&TaskFollowupRecord{}).
			Where(
				"followup_id = ? AND task_id = ? AND status = ? AND delivery_lease_token = ?",
				followupID, taskID, TaskFollowupStatusDelivering, taskLeaseToken,
			).
			Updates(map[string]interface{}{
				"status":     TaskFollowupStatusApplied,
				"applied_at": now,
				"updated_at": now,
			})
		if recordUpdate.Error != nil {
			return recordUpdate.Error
		}
		if recordUpdate.RowsAffected != 1 {
			return ErrLeaseLost
		}
		if err := tx.Where("followup_id = ?", followupID).First(&applied).Error; err != nil {
			return err
		}
		if persist != nil {
			if err := persist(tx, &applied); err != nil {
				return err
			}
		}
		newlyApplied = true
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	return &applied, newlyApplied, nil
}

func ListAppliedTaskFollowups(ctx context.Context, db *gorm.DB, taskID string) ([]TaskFollowupRecord, error) {
	if db == nil {
		return nil, fmt.Errorf("database connection is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	var records []TaskFollowupRecord
	err := db.WithContext(ctx).
		Where("task_id = ? AND status = ?", strings.TrimSpace(taskID), TaskFollowupStatusApplied).
		Order("created_at ASC, id ASC").
		Find(&records).Error
	return records, err
}
