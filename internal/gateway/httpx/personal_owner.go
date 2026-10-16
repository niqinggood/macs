package httpx

import (
	"errors"
	"fmt"
	"strings"

	"gorm.io/gorm"
)

var (
	// ErrSessionOwnerConflict is intentionally owner-agnostic so a caller does
	// not learn which authenticated subject owns a session.
	ErrSessionOwnerConflict = errors.New("session owner conflict")
	// ErrTaskOwnerConflict prevents durable task updates from stripping or
	// changing the owner captured at submission time.
	ErrTaskOwnerConflict = errors.New("task owner conflict")
)

// AgentOSPersonalOwner is the persisted tenant/subject boundary attached to a
// task. Scoped distinguishes authenticated personal data from legacy rows.
type AgentOSPersonalOwner struct {
	TenantID  string
	SubjectID string
	Scoped    bool
}

func NormalizePersonalOwnerTenantID(tenantID, defaultTenantID string) string {
	tenantID = strings.TrimSpace(tenantID)
	if tenantID == "" {
		return strings.TrimSpace(defaultTenantID)
	}
	return tenantID
}

func SamePersistedPersonalOwner(
	leftTenantID, leftSubjectID, rightTenantID, rightSubjectID, defaultTenantID string,
) bool {
	return NormalizePersonalOwnerTenantID(leftTenantID, defaultTenantID) ==
		NormalizePersonalOwnerTenantID(rightTenantID, defaultTenantID) &&
		strings.TrimSpace(leftSubjectID) == strings.TrimSpace(rightSubjectID)
}

// ScopeChatHistoryForOwner isolates authenticated tasks to their exact owner.
// Missing owners fail closed. Legacy tasks can only see legacy unowned rows.
func ScopeChatHistoryForOwner(query *gorm.DB, owner AgentOSPersonalOwner, defaultTenantID string) *gorm.DB {
	if query == nil {
		return query
	}
	if owner.Scoped {
		tenantID := NormalizePersonalOwnerTenantID(owner.TenantID, defaultTenantID)
		subjectID := strings.TrimSpace(owner.SubjectID)
		if subjectID == "" {
			return query.Where("1 = 0")
		}
		return query.Where("tenant_id = ? AND subject_id = ?", tenantID, subjectID)
	}
	return query.Where(
		"tenant_id = ? AND (subject_id IS NULL OR subject_id = '')",
		strings.TrimSpace(defaultTenantID),
	)
}

func ScopeRowsToPersistedOwner(query *gorm.DB, tenantID, subjectID, defaultTenantID string) *gorm.DB {
	if query == nil {
		return query
	}
	subjectID = strings.TrimSpace(subjectID)
	tenantID = NormalizePersonalOwnerTenantID(tenantID, defaultTenantID)
	if subjectID == "" {
		return query.Where(
			"tenant_id = ? AND (subject_id IS NULL OR subject_id = '')",
			tenantID,
		)
	}
	return query.Where("tenant_id = ? AND subject_id = ?", tenantID, subjectID)
}

// LoadOwnedRecord loads one exact personal owner into destination. It remains
// generic so the reusable boundary package does not depend on the host's GORM
// record type.
func LoadOwnedRecord(
	db *gorm.DB,
	destination interface{},
	taskID, tenantID, subjectID, defaultTenantID string,
) error {
	if db == nil {
		return fmt.Errorf("database connection is nil")
	}
	taskID = strings.TrimSpace(taskID)
	subjectID = strings.TrimSpace(subjectID)
	if taskID == "" {
		return fmt.Errorf("task_id is required")
	}
	if subjectID == "" {
		return fmt.Errorf("subject_id is required")
	}
	return db.Where(
		"task_id = ? AND tenant_id = ? AND subject_id = ?",
		taskID,
		NormalizePersonalOwnerTenantID(tenantID, defaultTenantID),
		subjectID,
	).First(destination).Error
}
