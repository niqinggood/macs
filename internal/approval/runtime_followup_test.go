package approval

import (
	"fmt"
	"testing"
	"time"

	"mac/internal/agentos"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func openApprovalFollowupTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf(
		"file:%s?mode=memory&cache=shared&_pragma=busy_timeout(5000)",
		agentos.NewLeaseToken(),
	)
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(
		&ApprovalRequestRecord{},
		&agentos.TaskFollowupState{},
		&agentos.TaskFollowupRecord{},
	); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestExpireAgentOSApprovalTasksClosesOnlyWonApprovals(t *testing.T) {
	now := time.Now().Round(0)
	expiresAt := now.Add(-time.Minute)

	t.Run("approved race winner keeps gate open", func(t *testing.T) {
		db := openApprovalFollowupTestDB(t)
		const taskID = "agt-approval-already-won"
		if err := agentos.EnsureTaskFollowupState(db, taskID, true, now); err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&ApprovalRequestRecord{
			ApprovalID: "approval-won", ApprovalKey: "key-won", Generation: 1,
			TaskID: taskID, Status: StatusApproved, ExpiresAt: &expiresAt,
			CreatedAt: now, UpdatedAt: now,
		}).Error; err != nil {
			t.Fatal(err)
		}

		var expiredTaskIDs []string
		var expired int64
		if err := db.Transaction(func(tx *gorm.DB) error {
			var err error
			expiredTaskIDs, expired, err = expireAgentOSApprovalTasks(tx, []string{taskID}, now)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		if expired != 0 || len(expiredTaskIDs) != 0 {
			t.Fatalf("expired=%d task_ids=%v, want no won expiry", expired, expiredTaskIDs)
		}
		var state agentos.TaskFollowupState
		if err := db.Where("task_id = ?", taskID).First(&state).Error; err != nil {
			t.Fatal(err)
		}
		if !state.Open {
			t.Fatal("approval winner lost the task follow-up gate")
		}
	})

	t.Run("expired winner closes gate and cancels pending input", func(t *testing.T) {
		db := openApprovalFollowupTestDB(t)
		const taskID = "agt-approval-expired"
		if err := agentos.EnsureTaskFollowupState(db, taskID, true, now); err != nil {
			t.Fatal(err)
		}
		record := agentos.TaskFollowupRecord{
			FollowupID: agentos.NewTaskFollowupID(now), TaskID: taskID,
			Message: "pending input", Status: agentos.TaskFollowupStatusPending,
			CreatedAt: now, UpdatedAt: now,
		}
		if err := db.Transaction(func(tx *gorm.DB) error {
			if err := agentos.ReserveTaskFollowupSlot(tx, taskID, now); err != nil {
				return err
			}
			return tx.Create(&record).Error
		}); err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&ApprovalRequestRecord{
			ApprovalID: "approval-expired", ApprovalKey: "key-expired", Generation: 1,
			TaskID: taskID, Status: StatusPending, ExpiresAt: &expiresAt,
			CreatedAt: now, UpdatedAt: now,
		}).Error; err != nil {
			t.Fatal(err)
		}

		var expiredTaskIDs []string
		var expired int64
		if err := db.Transaction(func(tx *gorm.DB) error {
			var err error
			expiredTaskIDs, expired, err = expireAgentOSApprovalTasks(tx, []string{taskID}, now)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		if expired != 1 || len(expiredTaskIDs) != 1 || expiredTaskIDs[0] != taskID {
			t.Fatalf("expired=%d task_ids=%v", expired, expiredTaskIDs)
		}
		var state agentos.TaskFollowupState
		if err := db.Where("task_id = ?", taskID).First(&state).Error; err != nil {
			t.Fatal(err)
		}
		if state.Open {
			t.Fatal("expired approval left the task follow-up gate open")
		}
		var stored agentos.TaskFollowupRecord
		if err := db.Where("followup_id = ?", record.FollowupID).First(&stored).Error; err != nil {
			t.Fatal(err)
		}
		if stored.Status != agentos.TaskFollowupStatusCancelled || stored.CancelledAt == nil {
			t.Fatalf("pending follow-up was not cancelled: %#v", stored)
		}
	})
}
