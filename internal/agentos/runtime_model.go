package agentos

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"mac/internal/toolcontract"
)

var ErrIdempotencyPayloadMismatch = errors.New("idempotency key was already used for a different request")

// ToolExecutionReceipt is the durable boundary around one concrete tool call.
type ToolExecutionReceipt struct {
	ID              uint       `gorm:"column:id;primarykey" json:"-"`
	TaskID          string     `gorm:"column:task_id;uniqueIndex:idx_tool_receipt_call,priority:1;size:64" json:"task_id"`
	ToolCallID      string     `gorm:"column:tool_call_id;uniqueIndex:idx_tool_receipt_call,priority:2;size:128" json:"tool_call_id"`
	ToolName        string     `gorm:"column:tool_name;index;size:128" json:"tool_name"`
	ArgumentsHash   string     `gorm:"column:arguments_hash;size:64" json:"arguments_hash"`
	LeaseToken      string     `gorm:"column:lease_token;index;size:64" json:"-"`
	ReadOnly        bool       `gorm:"column:read_only" json:"read_only"`
	Status          string     `gorm:"column:status;index;size:20" json:"status"`
	ResultContent   string     `gorm:"column:result_content;type:text" json:"-"`
	ResultTruncated bool       `gorm:"column:result_truncated" json:"result_truncated,omitempty"`
	ErrorMsg        string     `gorm:"column:error_msg;type:text" json:"error,omitempty"`
	DurationMs      int64      `gorm:"column:duration_ms" json:"duration_ms,omitempty"`
	StartedAt       time.Time  `gorm:"column:started_at" json:"started_at"`
	CompletedAt     *time.Time `gorm:"column:completed_at" json:"completed_at,omitempty"`
	UpdatedAt       time.Time  `gorm:"column:updated_at" json:"updated_at"`
}

func (ToolExecutionReceipt) TableName() string { return "tool_execution_receipts" }

// AgentTaskRequestRecord maps an idempotency key hash to one generated task ID.
type AgentTaskRequestRecord struct {
	ID          uint      `gorm:"column:id;primarykey" json:"-"`
	KeyHash     string    `gorm:"column:key_hash;uniqueIndex;size:64" json:"-"`
	RequestHash string    `gorm:"column:request_hash;size:64" json:"-"`
	TenantID    string    `gorm:"column:tenant_id;index;size:64;default:default" json:"tenant_id"`
	RoleID      string    `gorm:"column:role_id;index;size:128" json:"role_id,omitempty"`
	CallerID    string    `gorm:"column:caller_id;index;size:128" json:"caller_id"`
	SubjectID   string    `gorm:"column:subject_id;index;size:128" json:"subject_id,omitempty"`
	TaskID      string    `gorm:"column:task_id;index;size:64" json:"task_id"`
	CreatedAt   time.Time `gorm:"column:created_at" json:"created_at"`
}

func (AgentTaskRequestRecord) TableName() string { return "agent_task_request_records" }

func ToolCallArgumentsHash(call toolcontract.Call) (string, error) {
	payload, err := json.Marshal(struct {
		Name      string                 `json:"name"`
		Arguments map[string]interface{} `json:"arguments"`
	}{Name: call.Name, Arguments: call.Arguments})
	if err != nil {
		return "", fmt.Errorf("marshal tool arguments: %w", err)
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:]), nil
}

// TaskIdempotencyHash namespaces a key by tenant, caller and optional subject.
func TaskIdempotencyHash(defaultTenant, tenantID, callerID string, subjectAndKey ...string) string {
	tenantID = strings.TrimSpace(tenantID)
	if tenantID == "" {
		tenantID = strings.TrimSpace(defaultTenant)
	}
	subjectID := ""
	key := ""
	switch len(subjectAndKey) {
	case 0:
	case 1:
		key = subjectAndKey[0]
	default:
		subjectID = subjectAndKey[0]
		key = subjectAndKey[1]
	}
	namespace := tenantID + "\x00" + strings.TrimSpace(callerID) + "\x00"
	if strings.TrimSpace(subjectID) != "" {
		namespace += strings.TrimSpace(subjectID) + "\x00"
	}
	sum := sha256.Sum256([]byte(namespace + strings.TrimSpace(key)))
	return hex.EncodeToString(sum[:])
}
