package agentos

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type taskFollowupLeaseFixture struct {
	TaskID     string `gorm:"column:task_id;primaryKey"`
	QueueName  string `gorm:"column:queue_name"`
	Status     string `gorm:"column:status"`
	LeaseToken string `gorm:"column:lease_token"`
}

func (taskFollowupLeaseFixture) TableName() string { return "gateway_task_records" }

type taskFollowupAuditFixture struct {
	ID         uint   `gorm:"primaryKey"`
	FollowupID string `gorm:"uniqueIndex"`
	Message    string
}

func openTaskFollowupTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared&_pragma=busy_timeout(5000)", NewLeaseToken())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(
		&taskFollowupLeaseFixture{},
		&TaskFollowupState{},
		&TaskFollowupRecord{},
		&taskFollowupAuditFixture{},
	); err != nil {
		t.Fatal(err)
	}
	return db
}

func seedTaskFollowup(t *testing.T, db *gorm.DB, taskID, leaseToken, message string) TaskFollowupRecord {
	t.Helper()
	now := time.Now().Round(0)
	if err := EnsureTaskFollowupState(db, taskID, true, now); err != nil {
		t.Fatal(err)
	}
	record := TaskFollowupRecord{
		FollowupID: NewTaskFollowupID(now), TaskID: taskID,
		TenantID: "tenant-a", SubjectID: "alice", RoleID: "assistant",
		CallerID: "crm", Message: message, Status: TaskFollowupStatusPending,
		RequestHash: TaskFollowupRequestHash(message), CreatedAt: now, UpdatedAt: now,
	}
	if err := db.Transaction(func(tx *gorm.DB) error {
		if err := ReserveTaskFollowupSlot(tx, taskID, now); err != nil {
			return err
		}
		return tx.Create(&record).Error
	}); err != nil {
		t.Fatal(err)
	}
	return record
}

func TestTaskFollowupMustBeAppliedBeforeCompletionGateCloses(t *testing.T) {
	db := openTaskFollowupTestDB(t)
	const taskID = "agt-followup-order"
	const leaseToken = "lease-current"
	if err := db.Create(&taskFollowupLeaseFixture{
		TaskID: taskID, QueueName: QueueName, Status: "running", LeaseToken: leaseToken,
	}).Error; err != nil {
		t.Fatal(err)
	}
	record := seedTaskFollowup(t, db, taskID, leaseToken, "use the corrected sample id")

	decision, err := TryCloseTaskFollowups(db, taskID, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if decision != TaskFollowupClosePending {
		t.Fatalf("close decision = %v, want pending", decision)
	}

	claimed, ok, err := ClaimNextTaskFollowup(context.Background(), db, taskID, leaseToken, time.Now())
	if err != nil || !ok {
		t.Fatalf("claim ok=%v err=%v", ok, err)
	}
	if claimed.FollowupID != record.FollowupID || claimed.DeliveryAttempt != 1 {
		t.Fatalf("claimed = %#v", claimed)
	}
	applied, fresh, err := ApplyClaimedTaskFollowup(
		context.Background(), db, taskID, record.FollowupID, leaseToken, time.Now(),
		func(tx *gorm.DB, stored *TaskFollowupRecord) error {
			return tx.Create(&taskFollowupAuditFixture{
				FollowupID: stored.FollowupID, Message: stored.Message,
			}).Error
		},
	)
	if err != nil || !fresh || applied.Status != TaskFollowupStatusApplied {
		t.Fatalf("apply fresh=%v record=%#v err=%v", fresh, applied, err)
	}

	decision, err = TryCloseTaskFollowups(db, taskID, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if decision != TaskFollowupCloseDrained {
		t.Fatalf("close decision = %v, want drained", decision)
	}
	if err := ReserveTaskFollowupSlot(db, taskID, time.Now()); !errors.Is(err, ErrTaskFollowupsClosed) {
		t.Fatalf("reserve after close = %v, want ErrTaskFollowupsClosed", err)
	}
}

func TestTaskFollowupCompletionGateSerializesConcurrentAcceptance(t *testing.T) {
	t.Run("accepted input commits first", func(t *testing.T) {
		db := openTaskFollowupTestDB(t)
		const taskID = "agt-followup-race-accept"
		now := time.Now().Round(0)
		if err := EnsureTaskFollowupState(db, taskID, true, now); err != nil {
			t.Fatal(err)
		}

		reserved := make(chan struct{})
		commitAcceptance := make(chan struct{})
		acceptResult := make(chan error, 1)
		go func() {
			acceptResult <- db.Transaction(func(tx *gorm.DB) error {
				if err := ReserveTaskFollowupSlot(tx, taskID, now); err != nil {
					return err
				}
				close(reserved)
				<-commitAcceptance
				return tx.Create(&TaskFollowupRecord{
					FollowupID: NewTaskFollowupID(now), TaskID: taskID,
					Message: "accepted before completion", Status: TaskFollowupStatusPending,
					CreatedAt: now, UpdatedAt: now,
				}).Error
			})
		}()
		<-reserved

		type closeResult struct {
			decision TaskFollowupCloseDecision
			err      error
		}
		closeStarted := make(chan struct{})
		closed := make(chan closeResult, 1)
		go func() {
			close(closeStarted)
			decision, err := TryCloseTaskFollowups(db, taskID, time.Now())
			closed <- closeResult{decision: decision, err: err}
		}()
		<-closeStarted
		close(commitAcceptance)

		if err := <-acceptResult; err != nil {
			t.Fatalf("accept follow-up: %v", err)
		}
		result := <-closed
		if result.err != nil || result.decision != TaskFollowupClosePending {
			t.Fatalf("completion decision=%v err=%v, want pending", result.decision, result.err)
		}
	})

	t.Run("completion commits first", func(t *testing.T) {
		db := openTaskFollowupTestDB(t)
		const taskID = "agt-followup-race-complete"
		now := time.Now().Round(0)
		if err := EnsureTaskFollowupState(db, taskID, true, now); err != nil {
			t.Fatal(err)
		}

		gateClosed := make(chan struct{})
		commitCompletion := make(chan struct{})
		completionResult := make(chan error, 1)
		go func() {
			completionResult <- db.Transaction(func(tx *gorm.DB) error {
				decision, err := TryCloseTaskFollowups(tx, taskID, now)
				if err != nil {
					return err
				}
				if decision != TaskFollowupCloseDrained {
					return fmt.Errorf("completion decision = %v, want drained", decision)
				}
				close(gateClosed)
				<-commitCompletion
				return nil
			})
		}()
		<-gateClosed

		acceptStarted := make(chan struct{})
		acceptResult := make(chan error, 1)
		go func() {
			close(acceptStarted)
			acceptResult <- db.Transaction(func(tx *gorm.DB) error {
				return ReserveTaskFollowupSlot(tx, taskID, time.Now())
			})
		}()
		<-acceptStarted
		close(commitCompletion)

		if err := <-completionResult; err != nil {
			t.Fatalf("complete task: %v", err)
		}
		if err := <-acceptResult; !errors.Is(err, ErrTaskFollowupsClosed) {
			t.Fatalf("accept after completion = %v, want ErrTaskFollowupsClosed", err)
		}
	})
}

func TestTaskFollowupApplicationIsAtomicWithHostPersistence(t *testing.T) {
	db := openTaskFollowupTestDB(t)
	const taskID = "agt-followup-atomic"
	const leaseToken = "lease-current"
	if err := db.Create(&taskFollowupLeaseFixture{
		TaskID: taskID, QueueName: QueueName, Status: "running", LeaseToken: leaseToken,
	}).Error; err != nil {
		t.Fatal(err)
	}
	record := seedTaskFollowup(t, db, taskID, leaseToken, "do not lose this")
	if _, ok, err := ClaimNextTaskFollowup(context.Background(), db, taskID, leaseToken, time.Now()); err != nil || !ok {
		t.Fatalf("claim ok=%v err=%v", ok, err)
	}

	wantErr := errors.New("transcript unavailable")
	_, fresh, err := ApplyClaimedTaskFollowup(
		context.Background(), db, taskID, record.FollowupID, leaseToken, time.Now(),
		func(*gorm.DB, *TaskFollowupRecord) error { return wantErr },
	)
	if !errors.Is(err, wantErr) || fresh {
		t.Fatalf("apply fresh=%v err=%v", fresh, err)
	}
	var stored TaskFollowupRecord
	if err := db.Where("followup_id = ?", record.FollowupID).First(&stored).Error; err != nil {
		t.Fatal(err)
	}
	if stored.Status != TaskFollowupStatusDelivering || stored.AppliedAt != nil {
		t.Fatalf("record escaped rollback: %#v", stored)
	}
	var state TaskFollowupState
	if err := db.Where("task_id = ?", taskID).First(&state).Error; err != nil {
		t.Fatal(err)
	}
	if state.AcceptedCount != 1 || state.AppliedCount != 0 {
		t.Fatalf("state escaped rollback: %#v", state)
	}
}

func TestTaskFollowupLeaseFencingAllowsNewWorkerToReclaim(t *testing.T) {
	db := openTaskFollowupTestDB(t)
	const taskID = "agt-followup-fence"
	if err := db.Create(&taskFollowupLeaseFixture{
		TaskID: taskID, QueueName: QueueName, Status: "running", LeaseToken: "lease-old",
	}).Error; err != nil {
		t.Fatal(err)
	}
	record := seedTaskFollowup(t, db, taskID, "lease-old", "new direction")
	if _, ok, err := ClaimNextTaskFollowup(context.Background(), db, taskID, "lease-old", time.Now()); err != nil || !ok {
		t.Fatalf("old claim ok=%v err=%v", ok, err)
	}
	if err := db.Model(&taskFollowupLeaseFixture{}).
		Where("task_id = ?", taskID).
		Update("lease_token", "lease-new").Error; err != nil {
		t.Fatal(err)
	}
	if _, _, err := ApplyClaimedTaskFollowup(
		context.Background(), db, taskID, record.FollowupID, "lease-old", time.Now(), nil,
	); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("old lease apply = %v, want ErrLeaseLost", err)
	}
	claimed, ok, err := ClaimNextTaskFollowup(context.Background(), db, taskID, "lease-new", time.Now())
	if err != nil || !ok {
		t.Fatalf("new claim ok=%v err=%v", ok, err)
	}
	if claimed.DeliveryAttempt != 2 {
		t.Fatalf("delivery attempt = %d, want 2", claimed.DeliveryAttempt)
	}
	if _, fresh, err := ApplyClaimedTaskFollowup(
		context.Background(), db, taskID, record.FollowupID, "lease-new", time.Now(), nil,
	); err != nil || !fresh {
		t.Fatalf("new lease apply fresh=%v err=%v", fresh, err)
	}
}

func TestCancelTaskFollowupsClosesGateAndCancelsOutstandingRecords(t *testing.T) {
	db := openTaskFollowupTestDB(t)
	const taskID = "agt-followup-cancel"
	if err := db.Create(&taskFollowupLeaseFixture{
		TaskID: taskID, QueueName: QueueName, Status: "running", LeaseToken: "lease-current",
	}).Error; err != nil {
		t.Fatal(err)
	}
	record := seedTaskFollowup(t, db, taskID, "lease-current", "still pending")
	if err := CancelTaskFollowups(db, taskID, time.Now()); err != nil {
		t.Fatal(err)
	}
	var stored TaskFollowupRecord
	if err := db.Where("followup_id = ?", record.FollowupID).First(&stored).Error; err != nil {
		t.Fatal(err)
	}
	if stored.Status != TaskFollowupStatusCancelled || stored.CancelledAt == nil {
		t.Fatalf("cancelled record = %#v", stored)
	}
	if err := ReserveTaskFollowupSlot(db, taskID, time.Now()); !errors.Is(err, ErrTaskFollowupsClosed) {
		t.Fatalf("reserve after cancellation = %v", err)
	}
}

func TestBackfillTaskFollowupStatesKeepsTerminalTasksClosed(t *testing.T) {
	db := openTaskFollowupTestDB(t)
	for _, fixture := range []taskFollowupLeaseFixture{
		{TaskID: "running-task", QueueName: QueueName, Status: "running", LeaseToken: "lease"},
		{TaskID: "completed-task", QueueName: QueueName, Status: "completed"},
		{TaskID: "legacy-task", QueueName: "", Status: "running"},
	} {
		if err := db.Create(&fixture).Error; err != nil {
			t.Fatal(err)
		}
	}
	created, err := BackfillTaskFollowupStates(db, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if created != 2 {
		t.Fatalf("created = %d, want 2", created)
	}
	var running, completed TaskFollowupState
	if err := db.Where("task_id = ?", "running-task").First(&running).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Where("task_id = ?", "completed-task").First(&completed).Error; err != nil {
		t.Fatal(err)
	}
	if !running.Open || completed.Open {
		t.Fatalf("backfilled states: running=%#v completed=%#v", running, completed)
	}
}
