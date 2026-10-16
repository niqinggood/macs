package approval

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"mac/internal/agentos"
	"mac/internal/permissionpolicy"
	"mac/internal/textutil"
	"mac/internal/toolcontract"

	"gorm.io/gorm"
)

const (
	StatusPending   = "pending"
	StatusApproved  = "approved"
	StatusDenied    = "denied"
	StatusConsumed  = "consumed"
	StatusExpired   = "expired"
	StatusCancelled = "cancelled"

	DefaultTTL               = 24 * time.Hour
	maxApprovalArgumentBytes = 4096
)

// ApprovalRequestRecord is a durable, one-shot authorization for exactly one
// task/tool/argument tuple. ApprovalKey plus Generation is host-generated and
// unique so two workers cannot create independent grants for the same attempt.
type ApprovalRequestRecord struct {
	ID               uint       `gorm:"primarykey" json:"-"`
	ApprovalID       string     `gorm:"uniqueIndex;size:64" json:"approval_id"`
	ApprovalKey      string     `gorm:"uniqueIndex:idx_approval_generation,priority:1;size:64" json:"-"`
	Generation       int        `gorm:"uniqueIndex:idx_approval_generation,priority:2" json:"generation"`
	TaskID           string     `gorm:"index;size:64" json:"task_id"`
	TenantID         string     `gorm:"index;size:64;default:default" json:"tenant_id"`
	RoleID           string     `gorm:"index;size:128" json:"role_id,omitempty"`
	RoleRevision     string     `gorm:"index;size:80" json:"role_revision,omitempty"`
	ToolCallID       string     `gorm:"index;size:128" json:"tool_call_id,omitempty"`
	ToolName         string     `gorm:"index;size:128" json:"tool_name"`
	ArgumentsHash    string     `gorm:"size:64" json:"arguments_hash"`
	ArgumentsPreview string     `gorm:"type:text" json:"arguments_preview,omitempty"`
	Reason           string     `gorm:"type:text" json:"reason"`
	Status           string     `gorm:"index;size:20" json:"status"`
	RequestedAt      time.Time  `gorm:"index" json:"requested_at"`
	ExpiresAt        *time.Time `gorm:"index" json:"expires_at,omitempty"`
	DecidedAt        *time.Time `json:"decided_at,omitempty"`
	DecidedBy        string     `gorm:"size:128" json:"decided_by,omitempty"`
	DecisionReason   string     `gorm:"type:text" json:"decision_reason,omitempty"`
	ConsumedAt       *time.Time `json:"consumed_at,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

// ApprovalRequiredError is a control-flow signal. AgentOS converts it into a
// durable waiting_approval state instead of treating it as task failure.
type ApprovalRequiredError struct {
	Request ApprovalRequestRecord
}

func (e *ApprovalRequiredError) Error() string {
	if e == nil {
		return "tool approval is required"
	}
	if e.Request.ApprovalID == "" {
		return fmt.Sprintf("approval is required for tool %q", e.Request.ToolName)
	}
	return fmt.Sprintf("approval %s is required for tool %q", e.Request.ApprovalID, e.Request.ToolName)
}

func (e *ApprovalRequiredError) MiddlewareErrorCode() string {
	return "approval_required"
}

func AsApprovalRequiredError(err error) (*ApprovalRequiredError, bool) {
	var approvalErr *ApprovalRequiredError
	ok := errors.As(err, &approvalErr)
	return approvalErr, ok
}

func AuthorizeExecutionPolicyApproval(ctx context.Context, call toolcontract.Call, reason string) error {
	runtime := toolApprovalRuntimeFromContext(ctx)
	if runtime == nil || runtime.broker == nil || strings.TrimSpace(taskIDFromContext(ctx)) == "" {
		if strings.TrimSpace(reason) == "" {
			reason = fmt.Sprintf("tool %q requires approval", call.Name)
		}
		return &executionPolicyApprovalUnavailableError{reason: reason}
	}
	return AuthorizeToolApproval(ctx, call, reason)
}

type executionPolicyApprovalUnavailableError struct{ reason string }

func (e *executionPolicyApprovalUnavailableError) Error() string { return e.reason }

func (e *executionPolicyApprovalUnavailableError) MiddlewareErrorCode() string {
	return "approval_required"
}

type ApprovalDeniedError struct {
	ToolName string
	Reason   string
}

func (e *ApprovalDeniedError) Error() string {
	if e == nil {
		return "tool approval was denied"
	}
	if strings.TrimSpace(e.Reason) != "" {
		return e.Reason
	}
	return fmt.Sprintf("approval for tool %q was denied", e.ToolName)
}

func (e *ApprovalDeniedError) MiddlewareErrorCode() string {
	return "approval_denied"
}

type toolApprovalBroker interface {
	Authorize(context.Context, string, toolcontract.Call, string) (ApprovalRequestRecord, error)
	Consume(context.Context, string) error
}

type toolApprovalRuntime struct {
	broker toolApprovalBroker

	mu     sync.Mutex
	grants map[string]string
}

type toolApprovalRuntimeContextKey struct{}

func WithToolApprovalBroker(ctx context.Context, broker toolApprovalBroker) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if broker == nil {
		return ctx
	}
	return context.WithValue(ctx, toolApprovalRuntimeContextKey{}, &toolApprovalRuntime{
		broker: broker,
		grants: make(map[string]string),
	})
}

func toolApprovalRuntimeFromContext(ctx context.Context) *toolApprovalRuntime {
	if ctx == nil {
		return nil
	}
	runtime, _ := ctx.Value(toolApprovalRuntimeContextKey{}).(*toolApprovalRuntime)
	return runtime
}

func RuntimeAvailable(ctx context.Context) bool {
	runtime := toolApprovalRuntimeFromContext(ctx)
	return runtime != nil && runtime.broker != nil && strings.TrimSpace(taskIDFromContext(ctx)) != ""
}

func approvalFingerprint(taskID string, call toolcontract.Call) (string, error) {
	argumentsHash, err := agentos.ToolCallArgumentsHash(call)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(strings.TrimSpace(taskID) + "\x00" + argumentsHash))
	return hex.EncodeToString(sum[:]), nil
}

func (r *toolApprovalRuntime) grant(fingerprint, approvalID string) {
	if r == nil || fingerprint == "" || approvalID == "" {
		return
	}
	r.mu.Lock()
	r.grants[fingerprint] = approvalID
	r.mu.Unlock()
}

func (r *toolApprovalRuntime) granted(fingerprint string) (string, bool) {
	if r == nil || fingerprint == "" {
		return "", false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	approvalID, ok := r.grants[fingerprint]
	return approvalID, ok
}

func (r *toolApprovalRuntime) takeGrant(fingerprint string) string {
	if r == nil || fingerprint == "" {
		return ""
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	approvalID := r.grants[fingerprint]
	delete(r.grants, fingerprint)
	return approvalID
}

func AuthorizeToolApproval(ctx context.Context, call toolcontract.Call, reason string) error {
	taskID := taskIDFromContext(ctx)
	fingerprint, err := approvalFingerprint(taskID, call)
	if err != nil {
		return fmt.Errorf("build approval fingerprint: %w", err)
	}
	runtime := toolApprovalRuntimeFromContext(ctx)
	if runtime != nil {
		if _, ok := runtime.granted(fingerprint); ok {
			return nil
		}
	}
	if runtime == nil || runtime.broker == nil || strings.TrimSpace(taskID) == "" {
		return &ApprovalRequiredError{Request: ApprovalRequestRecord{
			TaskID: taskID, ToolCallID: call.ID, ToolName: call.Name,
			Reason: strings.TrimSpace(reason), Status: StatusPending,
		}}
	}

	request, err := runtime.broker.Authorize(ctx, taskID, call, reason)
	if err != nil {
		return err
	}
	switch request.Status {
	case StatusApproved:
		runtime.grant(fingerprint, request.ApprovalID)
		return nil
	case StatusPending:
		return &ApprovalRequiredError{Request: request}
	case StatusDenied:
		reason := strings.TrimSpace(request.DecisionReason)
		if reason == "" {
			reason = fmt.Sprintf("approval %s for tool %q was denied", request.ApprovalID, call.Name)
		}
		return &ApprovalDeniedError{ToolName: call.Name, Reason: reason}
	case StatusConsumed:
		return &ApprovalDeniedError{
			ToolName: call.Name,
			Reason:   fmt.Sprintf("approval %s for tool %q was already consumed", request.ApprovalID, call.Name),
		}
	case StatusExpired:
		return &ApprovalDeniedError{
			ToolName: call.Name,
			Reason:   fmt.Sprintf("approval %s for tool %q expired", request.ApprovalID, call.Name),
		}
	case StatusCancelled:
		return &ApprovalDeniedError{
			ToolName: call.Name,
			Reason:   fmt.Sprintf("approval %s for tool %q was cancelled", request.ApprovalID, call.Name),
		}
	default:
		return fmt.Errorf("approval %s has unsupported status %q", request.ApprovalID, request.Status)
	}
}

func ConsumeToolApproval(ctx context.Context, call toolcontract.Call) {
	runtime := toolApprovalRuntimeFromContext(ctx)
	if runtime == nil || runtime.broker == nil {
		return
	}
	fingerprint, err := approvalFingerprint(taskIDFromContext(ctx), call)
	if err != nil {
		return
	}
	approvalID := runtime.takeGrant(fingerprint)
	if approvalID == "" {
		return
	}
	consumeCtx := context.WithoutCancel(ctx)
	_ = runtime.broker.Consume(consumeCtx, approvalID)
}

func ToolCallsUseApprovalPolicy(permissions *permissionpolicy.PermissionEngine, calls []toolcontract.Call) bool {
	if permissions == nil {
		return false
	}
	for _, call := range calls {
		_, approvalRequired, _ := permissions.Check(call.Name, call.Arguments)
		if approvalRequired {
			return true
		}
	}
	return false
}

type gormToolApprovalBroker struct {
	db  *gorm.DB
	ttl time.Duration
}

func NewGormToolApprovalBroker(db *gorm.DB, ttlOverride ...time.Duration) *gormToolApprovalBroker {
	if db == nil {
		return nil
	}
	ttl := DefaultTTL
	if len(ttlOverride) > 0 && ttlOverride[0] > 0 {
		ttl = ttlOverride[0]
	}
	return &gormToolApprovalBroker{db: db, ttl: ttl}
}

func approvalArgumentsPreview(arguments map[string]interface{}) string {
	payload, err := json.Marshal(arguments)
	if err != nil {
		return ""
	}
	if len(payload) > maxApprovalArgumentBytes {
		return textutil.TruncateUTF8Bytes(string(payload), maxApprovalArgumentBytes)
	}
	return string(payload)
}

func newApprovalID(now time.Time) string {
	var suffix [4]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return fmt.Sprintf("apr_%s_%x", now.Format("060102150405"), now.UnixNano()&0xffffffff)
	}
	return "apr_" + now.Format("060102150405") + "_" + hex.EncodeToString(suffix[:])
}

func (b *gormToolApprovalBroker) Authorize(ctx context.Context, taskID string, call toolcontract.Call, reason string) (ApprovalRequestRecord, error) {
	if b == nil || b.db == nil {
		return ApprovalRequestRecord{}, fmt.Errorf("approval store is unavailable")
	}
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return ApprovalRequestRecord{}, fmt.Errorf("task id is required for approval")
	}
	argumentsHash, err := agentos.ToolCallArgumentsHash(call)
	if err != nil {
		return ApprovalRequestRecord{}, err
	}
	key, err := approvalFingerprint(taskID, call)
	if err != nil {
		return ApprovalRequestRecord{}, err
	}

	var existing ApprovalRequestRecord
	lookup := b.db.WithContext(ctx).Where("approval_key = ?", key).
		Order("generation desc").
		First(&existing)
	if lookup.Error == nil {
		existing, err = b.expireIfNecessary(ctx, existing)
		if err != nil || existing.Status != StatusConsumed {
			return existing, err
		}
	}
	if !errors.Is(lookup.Error, gorm.ErrRecordNotFound) {
		if lookup.Error != nil {
			return ApprovalRequestRecord{}, lookup.Error
		}
	}

	var task approvalTaskRecord
	if err := b.db.WithContext(ctx).Where("task_id = ?", taskID).First(&task).Error; err != nil {
		return ApprovalRequestRecord{}, fmt.Errorf("load approval task: %w", err)
	}
	now := time.Now().Round(0)
	expiresAt := now.Add(b.ttl)
	request := ApprovalRequestRecord{
		ApprovalID: newApprovalID(now), ApprovalKey: key,
		Generation: existing.Generation + 1,
		TaskID:     taskID, TenantID: task.TenantID, RoleID: task.RoleID,
		RoleRevision: task.RoleRevision,
		ToolCallID:   call.ID, ToolName: call.Name,
		ArgumentsHash: argumentsHash, ArgumentsPreview: approvalArgumentsPreview(call.Arguments),
		Reason: strings.TrimSpace(reason),
		Status: StatusPending, RequestedAt: now, ExpiresAt: &expiresAt,
		CreatedAt: now, UpdatedAt: now,
	}
	if request.TenantID == "" {
		request.TenantID = "default"
	}
	if request.Generation <= 0 {
		request.Generation = 1
	}
	if err := b.db.WithContext(ctx).Create(&request).Error; err != nil {
		// Another worker may have won this generation's unique-key race.
		if lookupErr := b.db.WithContext(ctx).
			Where("approval_key = ? AND generation = ?", key, request.Generation).
			First(&existing).Error; lookupErr == nil {
			return b.expireIfNecessary(ctx, existing)
		}
		return ApprovalRequestRecord{}, err
	}
	return request, nil
}

func (b *gormToolApprovalBroker) expireIfNecessary(ctx context.Context, request ApprovalRequestRecord) (ApprovalRequestRecord, error) {
	if request.ExpiresAt == nil || time.Now().Before(*request.ExpiresAt) ||
		(request.Status != StatusPending && request.Status != StatusApproved) {
		return request, nil
	}
	now := time.Now().Round(0)
	result := b.db.WithContext(ctx).Model(&ApprovalRequestRecord{}).
		Where("approval_id = ? AND status IN ?", request.ApprovalID, []string{StatusPending, StatusApproved}).
		Updates(map[string]interface{}{"status": StatusExpired, "updated_at": now})
	if result.Error != nil {
		return ApprovalRequestRecord{}, result.Error
	}
	request.Status = StatusExpired
	request.UpdatedAt = now
	return request, nil
}

func (b *gormToolApprovalBroker) Consume(ctx context.Context, approvalID string) error {
	if b == nil || b.db == nil || strings.TrimSpace(approvalID) == "" {
		return nil
	}
	now := time.Now().Round(0)
	return b.db.WithContext(ctx).Model(&ApprovalRequestRecord{}).
		Where("approval_id = ? AND status = ?", approvalID, StatusApproved).
		Updates(map[string]interface{}{
			"status": StatusConsumed, "consumed_at": now, "updated_at": now,
		}).Error
}

func ExpireAgentOSApprovals(db *gorm.DB, now time.Time) (int64, error) {
	if db == nil {
		return 0, fmt.Errorf("database unavailable")
	}
	now = now.Round(0)
	var taskIDs []string
	var expired int64
	err := db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&ApprovalRequestRecord{}).
			Where("status = ? AND expires_at IS NOT NULL AND expires_at <= ?", StatusPending, now).
			Distinct("task_id").
			Pluck("task_id", &taskIDs).Error; err != nil {
			return err
		}
		if len(taskIDs) == 0 {
			return nil
		}
		expiredTaskIDs, expiredCount, err := expireAgentOSApprovalTasks(tx, taskIDs, now)
		if err != nil {
			return err
		}
		expired = expiredCount
		if len(expiredTaskIDs) == 0 {
			return nil
		}
		if err := tx.Model(&approvalTaskRecord{}).
			Where("task_id IN ? AND status = ?", expiredTaskIDs, "waiting_approval").
			Updates(map[string]interface{}{
				"status": "failed", "current_phase": "approval_expired",
				"error_msg": "task approval expired", "completed_at": now,
				"heartbeat_at": now, "updated_at": now,
			}).Error; err != nil {
			return err
		}
		if err := tx.Model(&ApprovalRequestRecord{}).
			Where("task_id IN ? AND status = ?", expiredTaskIDs, StatusApproved).
			Updates(map[string]interface{}{
				"status": StatusCancelled, "updated_at": now,
			}).Error; err != nil {
			return err
		}
		return tx.Model(&approvalChatMessage{}).
			Where("task_id IN ? AND status = ?", expiredTaskIDs, "waiting_approval").
			Update("status", "failed").Error
	})
	return expired, err
}

func expireAgentOSApprovalTasks(
	tx *gorm.DB,
	taskIDs []string,
	now time.Time,
) ([]string, int64, error) {
	expiredTaskIDs := make([]string, 0, len(taskIDs))
	var expired int64
	for _, taskID := range taskIDs {
		result := tx.Model(&ApprovalRequestRecord{}).
			Where(
				"task_id = ? AND status = ? AND expires_at IS NOT NULL AND expires_at <= ?",
				taskID, StatusPending, now,
			).
			Updates(map[string]interface{}{"status": StatusExpired, "updated_at": now})
		if result.Error != nil {
			return nil, 0, result.Error
		}
		if result.RowsAffected == 0 {
			// Approval or denial won the race after the sweep selected this
			// task. Its pending run must retain the open follow-up gate.
			continue
		}
		expired += result.RowsAffected
		expiredTaskIDs = append(expiredTaskIDs, taskID)
		if err := agentos.EnsureTaskFollowupState(tx, taskID, true, now); err != nil {
			return nil, 0, err
		}
		if err := agentos.CancelTaskFollowups(tx, taskID, now); err != nil {
			return nil, 0, err
		}
	}
	return expiredTaskIDs, expired, nil
}

type approvalTaskRecord struct {
	TaskID       string
	TenantID     string
	RoleID       string
	RoleRevision string
}

func (approvalTaskRecord) TableName() string { return "gateway_task_records" }

type approvalChatMessage struct{}

func (approvalChatMessage) TableName() string { return "chat_messages" }

func taskIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	taskID, _ := ctx.Value("task_id").(string)
	return strings.TrimSpace(taskID)
}
