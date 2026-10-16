package agentos

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"mac/internal/textutil"
	"mac/internal/toolcontract"

	"gorm.io/gorm"
)

const maxStoredToolReceiptResult = 64 * 1024

const orphanToolReceiptError = "tool execution interrupted because the previous MACS process exited before completion"

const QueueName = "agentos"

var ErrLeaseLost = errors.New("AgentOS task lease was lost to another worker")

type ReceiptDecision struct {
	Execute bool
	Cached  *toolcontract.Result
}

type ToolExecutionLedger interface {
	Begin(context.Context, string, toolcontract.Call, bool) (ReceiptDecision, error)
	Finish(context.Context, string, toolcontract.Call, toolcontract.Result, error, int64) error
}

type TaskRuntimeObserver interface {
	ObserveTaskRuntime(taskID, phase, toolCallID string)
}

type toolExecutionLedgerContextKey struct{}
type taskRuntimeObserverContextKey struct{}
type agentOSLeaseTokenContextKey struct{}
type toolExecutionReceiptTaskIDContextKey struct{}

func WithToolExecutionLedger(ctx context.Context, ledger ToolExecutionLedger) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if ledger == nil {
		return ctx
	}
	return context.WithValue(ctx, toolExecutionLedgerContextKey{}, ledger)
}

func ToolExecutionLedgerFromContext(ctx context.Context) ToolExecutionLedger {
	if ctx == nil {
		return nil
	}
	ledger, _ := ctx.Value(toolExecutionLedgerContextKey{}).(ToolExecutionLedger)
	return ledger
}

// withToolExecutionReceiptTaskID keeps a resumed tool call in the receipt
// namespace of the interrupted task. The new task still owns progress and
// transcript events; only idempotency lookup uses the original task ID.
func WithToolExecutionReceiptTaskID(ctx context.Context, taskID string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return ctx
	}
	return context.WithValue(ctx, toolExecutionReceiptTaskIDContextKey{}, taskID)
}

func ToolExecutionReceiptTaskIDFromContext(ctx context.Context, fallback string) string {
	if ctx != nil {
		if taskID, _ := ctx.Value(toolExecutionReceiptTaskIDContextKey{}).(string); strings.TrimSpace(taskID) != "" {
			return strings.TrimSpace(taskID)
		}
	}
	return strings.TrimSpace(fallback)
}

func WithTaskRuntimeObserver(ctx context.Context, observer TaskRuntimeObserver) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if observer == nil {
		return ctx
	}
	return context.WithValue(ctx, taskRuntimeObserverContextKey{}, observer)
}

func TaskRuntimeObserverFromContext(ctx context.Context) TaskRuntimeObserver {
	if ctx == nil {
		return nil
	}
	observer, _ := ctx.Value(taskRuntimeObserverContextKey{}).(TaskRuntimeObserver)
	return observer
}

func WithLeaseToken(ctx context.Context, leaseToken string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if strings.TrimSpace(leaseToken) == "" {
		return ctx
	}
	return context.WithValue(ctx, agentOSLeaseTokenContextKey{}, strings.TrimSpace(leaseToken))
}

func LeaseTokenFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	value, _ := ctx.Value(agentOSLeaseTokenContextKey{}).(string)
	return strings.TrimSpace(value)
}

type GormToolExecutionLedger struct{ db *gorm.DB }

type taskLeaseRecord struct{}

func (taskLeaseRecord) TableName() string { return "gateway_task_records" }

func NewGormToolExecutionLedger(db *gorm.DB) *GormToolExecutionLedger {
	if db == nil {
		return nil
	}
	return &GormToolExecutionLedger{db: db}
}

func (l *GormToolExecutionLedger) Begin(ctx context.Context, taskID string, call toolcontract.Call, readOnly bool) (ReceiptDecision, error) {
	if l == nil || l.db == nil || strings.TrimSpace(taskID) == "" || strings.TrimSpace(call.ID) == "" {
		return ReceiptDecision{Execute: true}, nil
	}
	hash, err := ToolCallArgumentsHash(call)
	if err != nil {
		return ReceiptDecision{}, err
	}
	now := time.Now()
	leaseToken := LeaseTokenFromContext(ctx)
	if leaseToken != "" {
		var owned int64
		if err := l.db.WithContext(ctx).Model(&taskLeaseRecord{}).
			Where("task_id = ? AND queue_name = ? AND status = ? AND lease_token = ?", taskID, QueueName, "running", leaseToken).
			Count(&owned).Error; err != nil {
			return ReceiptDecision{}, err
		}
		if owned != 1 {
			return ReceiptDecision{}, ErrLeaseLost
		}
	}
	var decision ReceiptDecision
	err = l.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var receipt ToolExecutionReceipt
		lookup := tx.Where("task_id = ? AND tool_call_id = ?", taskID, call.ID).First(&receipt)
		if errors.Is(lookup.Error, gorm.ErrRecordNotFound) {
			receipt = ToolExecutionReceipt{
				TaskID: taskID, ToolCallID: call.ID, ToolName: call.Name,
				ArgumentsHash: hash, LeaseToken: leaseToken, ReadOnly: readOnly, Status: "running",
				StartedAt: now, UpdatedAt: now,
			}
			if err := tx.Create(&receipt).Error; err != nil {
				return err
			}
			decision.Execute = true
			return nil
		}
		if lookup.Error != nil {
			return lookup.Error
		}
		if receipt.ToolName != call.Name || receipt.ArgumentsHash != hash {
			return fmt.Errorf("tool call id %q was already used with different arguments", call.ID)
		}
		switch receipt.Status {
		case "succeeded":
			succeeded := true
			content := receipt.ResultContent
			if receipt.ResultTruncated {
				content += "\n[... cached tool result was truncated by the execution receipt store ...]"
			}
			decision.Cached = &toolcontract.Result{
				ToolCallID: call.ID, Name: call.Name,
				Content: content, ExecutionSucceeded: &succeeded,
			}
			return nil
		case "running", "failed", "interrupted":
			if receipt.ReadOnly != readOnly {
				return fmt.Errorf("tool %q changed read-only classification since its receipt was created; automatic replay is blocked", call.Name)
			}
			if !readOnly {
				return fmt.Errorf("mutating tool %q has a previous %s receipt; automatic replay is blocked because its external outcome may be uncertain", call.Name, receipt.Status)
			}
			decision.Execute = true
			return tx.Model(&receipt).Updates(map[string]interface{}{
				"status": "running", "started_at": now, "completed_at": nil,
				"error_msg": "", "lease_token": leaseToken, "updated_at": now,
			}).Error
		default:
			return fmt.Errorf("tool call %q has unsupported receipt status %q", call.ID, receipt.Status)
		}
	})
	return decision, err
}

func (l *GormToolExecutionLedger) Finish(ctx context.Context, taskID string, call toolcontract.Call, result toolcontract.Result, runErr error, durationMs int64) error {
	if l == nil || l.db == nil || strings.TrimSpace(taskID) == "" || strings.TrimSpace(call.ID) == "" {
		return nil
	}
	leaseToken := LeaseTokenFromContext(ctx)
	if leaseToken != "" {
		var owned int64
		if err := l.db.WithContext(ctx).Model(&taskLeaseRecord{}).
			Where("task_id = ? AND queue_name = ? AND status = ? AND lease_token = ?", taskID, QueueName, "running", leaseToken).
			Count(&owned).Error; err != nil {
			return err
		}
		if owned != 1 {
			return ErrLeaseLost
		}
	}
	succeeded := toolcontract.ToolResultExecutionSucceeded(result, runErr)
	status := "failed"
	if succeeded {
		status = "succeeded"
	}
	content := result.Content
	truncated := false
	if len(content) > maxStoredToolReceiptResult {
		content = textutil.TruncateUTF8Bytes(content, maxStoredToolReceiptResult)
		truncated = true
	}
	errorMsg := ""
	if runErr != nil {
		errorMsg = runErr.Error()
	}
	now := time.Now()
	query := l.db.WithContext(ctx).Model(&ToolExecutionReceipt{}).
		Where("task_id = ? AND tool_call_id = ?", taskID, call.ID)
	if leaseToken != "" {
		query = query.Where("lease_token = ?", leaseToken)
	}
	resultUpdate := query.
		Updates(map[string]interface{}{
			"status": status, "result_content": content, "result_truncated": truncated,
			"error_msg": errorMsg, "duration_ms": durationMs,
			"completed_at": now, "updated_at": now,
		})
	if resultUpdate.Error != nil {
		return resultUpdate.Error
	}
	if leaseToken != "" && resultUpdate.RowsAffected == 0 {
		return ErrLeaseLost
	}
	return nil
}

// recoverOrphanToolReceipts marks attempts that were in flight when the
// process stopped. Read-only calls may be retried by Begin; mutating calls stay
// blocked because the host cannot prove whether their external side effect
// happened before the process exited.
func RecoverOrphanToolReceipts(db *gorm.DB, recoveredAt time.Time) (int64, error) {
	if db == nil {
		return 0, fmt.Errorf("database connection is nil")
	}
	recoveredAt = recoveredAt.Round(0)
	// In a multi-instance deployment, another Pod may still own a valid task
	// lease. Starting this process must not mark that Pod's in-flight tool call
	// as interrupted. Legacy/non-queued tasks retain the original recovery
	// behavior; queue workers reconcile receipts after winning an expired lease.
	activeLeasedTasks := db.Model(&taskLeaseRecord{}).
		Select("task_id").
		Where("queue_name = ? AND status = ? AND lease_until IS NOT NULL AND lease_until > ?", QueueName, "running", recoveredAt)
	result := db.Model(&ToolExecutionReceipt{}).
		Where("status = ? AND task_id NOT IN (?)", "running", activeLeasedTasks).
		Updates(map[string]interface{}{
			"status":       "interrupted",
			"error_msg":    orphanToolReceiptError,
			"completed_at": recoveredAt,
			"updated_at":   recoveredAt,
		})
	if result.Error != nil {
		return 0, fmt.Errorf("recover orphan tool receipts: %w", result.Error)
	}
	return result.RowsAffected, nil
}

func RecoverTaskToolReceipts(db *gorm.DB, taskID string, recoveredAt time.Time) (int64, error) {
	if db == nil || strings.TrimSpace(taskID) == "" {
		return 0, nil
	}
	recoveredAt = recoveredAt.Round(0)
	result := db.Model(&ToolExecutionReceipt{}).
		Where("task_id = ? AND status = ?", taskID, "running").
		Updates(map[string]interface{}{
			"status":       "interrupted",
			"error_msg":    orphanToolReceiptError,
			"completed_at": recoveredAt,
			"updated_at":   recoveredAt,
		})
	if result.Error != nil {
		return 0, fmt.Errorf("recover task tool receipts: %w", result.Error)
	}
	return result.RowsAffected, nil
}

// recoverOrphanAgentTaskRequests removes idempotency reservations that never
// reached durable task creation (for example, a process crash between the two
// writes). Keeping them would poison that caller/key forever after restart.
func RecoverOrphanTaskRequests(db *gorm.DB) (int64, error) {
	if db == nil {
		return 0, fmt.Errorf("database connection is nil")
	}
	durableTaskIDs := db.Model(&taskLeaseRecord{}).Select("task_id")
	// A second Pod may start while the first Pod is between reserving an
	// idempotency key and inserting its task. Only old reservations are orphaned;
	// deleting fresh rows would let two live submissions execute the same key.
	cutoff := time.Now().Add(-5 * time.Minute)
	result := db.Where("created_at < ? AND task_id NOT IN (?)", cutoff, durableTaskIDs).Delete(&AgentTaskRequestRecord{})
	if result.Error != nil {
		return 0, fmt.Errorf("recover orphan agent task requests: %w", result.Error)
	}
	return result.RowsAffected, nil
}
