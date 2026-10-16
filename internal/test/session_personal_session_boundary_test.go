package app

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mac/internal/projectctx"
	"mac/internal/security"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func validatedPersonalSubjectID(value string) (string, error) {
	return security.ValidatePersonalSubjectID(value)
}

func personalAgentOSPartitionID(tenantID, subjectID string) string {
	return security.PersonalPartitionID(tenantID, subjectID, defaultExecutionTenantID)
}

func personalAgentOSSessionPrefix(tenantID, subjectID string) string {
	return security.PersonalSessionPrefix(tenantID, subjectID, defaultExecutionTenantID)
}

func personalAgentOSWorkspace(tenantWorkspace, tenantID, subjectID string) string {
	return projectctx.PersonalWorkspace(tenantWorkspace, personalAgentOSPartitionID(tenantID, subjectID))
}

func newPersonalBoundaryGateway(t *testing.T) (*GatewayService, *gorm.DB) {
	t.Helper()
	db, err := gorm.Open(
		sqlite.Open(filepath.Join(t.TempDir(), "personal-boundary.db")),
		&gorm.Config{},
	)
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := db.AutoMigrate(&ChatSession{}, &ChatMessage{}, &GatewayTaskRecord{}); err != nil {
		t.Fatal(err)
	}
	gateway := &GatewayService{
		db:     db,
		logger: log.New(io.Discard, "", 0),
		tasks:  make(map[string]*GatewayTask),
	}
	return gateway, db
}

func TestBindSessionWorkspaceOwnedAtRejectsCrossSubjectReuse(t *testing.T) {
	gateway, db := newPersonalBoundaryGateway(t)
	workspaceA := t.TempDir()

	bound, err := gateway.bindSessionWorkspaceOwnedAt(
		"external-session", "agent", "default", "user-a", workspaceA, true,
	)
	if err != nil {
		t.Fatalf("bind user A session: %v", err)
	}
	if !sameProjectWorkspace(bound, workspaceA) {
		t.Fatalf("bound workspace = %q, want %q", bound, workspaceA)
	}
	if _, err := gateway.bindSessionWorkspaceOwnedAt(
		"external-session", "agent", "default", "user-a", workspaceA, false,
	); err != nil {
		t.Fatalf("same owner could not reuse session: %v", err)
	}

	_, err = gateway.bindSessionWorkspaceOwnedAt(
		"external-session", "agent", "default", "user-b", workspaceA, true,
	)
	if !errors.Is(err, ErrSessionOwnerConflict) {
		t.Fatalf("cross-subject reuse error = %v, want ErrSessionOwnerConflict", err)
	}
	_, err = gateway.bindSessionWorkspaceAt(
		"external-session", "agent", "legacy-user", workspaceA, false,
	)
	if !errors.Is(err, ErrSessionOwnerConflict) {
		t.Fatalf("legacy caller reused personal session: %v", err)
	}

	var session ChatSession
	if err := db.First(&session, "id = ?", "external-session").Error; err != nil {
		t.Fatal(err)
	}
	if session.TenantID != "default" || session.SubjectID != "user-a" {
		t.Fatalf("session owner changed: tenant=%q subject=%q", session.TenantID, session.SubjectID)
	}
}

func TestSaveChatMessageInheritsSessionOwner(t *testing.T) {
	gateway, db := newPersonalBoundaryGateway(t)
	session := ChatSession{
		ID: "owned-session", Name: "owned-session",
		TenantID: "default", SubjectID: "user-a", UserId: "legacy-a",
		Workspace: t.TempDir(), ProjectID: "project-a",
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	if err := db.Create(&session).Error; err != nil {
		t.Fatal(err)
	}

	message, err := gateway.saveChatMessage(
		session.ID,
		"assistant",
		"agent",
		"private result",
		"completed",
		"task-a",
		"api",
		"mutable-legacy-user",
	)
	if err != nil {
		t.Fatalf("save chat message: %v", err)
	}
	if message.TenantID != "default" || message.SubjectID != "user-a" {
		t.Fatalf("returned message owner = %q/%q", message.TenantID, message.SubjectID)
	}

	var stored ChatMessage
	if err := db.First(&stored, message.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.TenantID != "default" || stored.SubjectID != "user-a" {
		t.Fatalf("stored message owner = %q/%q", stored.TenantID, stored.SubjectID)
	}
	var storedSession ChatSession
	if err := db.First(&storedSession, "id = ?", session.ID).Error; err != nil {
		t.Fatal(err)
	}
	if storedSession.SubjectID != "user-a" || storedSession.MessageCount != 1 {
		t.Fatalf("session owner/count changed unexpectedly: %+v", storedSession)
	}
}

func TestGatewayTaskSubjectRoundTripAndOwnerIsImmutable(t *testing.T) {
	_, db := newPersonalBoundaryGateway(t)
	now := time.Now().Round(0)
	task := &GatewayTask{
		ID: "personal-task",
		Request: &MessageRequest{
			TaskID: "personal-task", TaskType: "chat",
			SessionId: "personal-session", TenantID: "default",
			SubjectID: "user-a", Message: "private prompt",
			AgentName: "agent", UserId: "legacy-a",
		},
		TenantID: "default", SubjectID: "user-a", TenantScoped: true,
		Status: "pending", QueueName: agentOSQueueName, CreatedAt: now,
	}
	if err := SaveGatewayTaskToDB(db, task); err != nil {
		t.Fatalf("save task: %v", err)
	}

	record, err := LoadTaskFromDBOwned(db, task.ID, "default", "user-a")
	if err != nil {
		t.Fatalf("load owned task: %v", err)
	}
	if record.SubjectID != "user-a" || !record.TenantScoped {
		t.Fatalf("record owner/scope lost: %+v", record)
	}
	if _, err := LoadTaskFromDBOwned(db, task.ID, "default", "user-b"); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("user B loaded user A task: %v", err)
	}

	restored := gatewayTaskFromRecord(*record)
	if restored.SubjectID != "user-a" || restored.Request == nil ||
		restored.Request.SubjectID != "user-a" || !restored.TenantScoped {
		t.Fatalf("task roundtrip lost owner: %+v", restored)
	}
	cloned := cloneGatewayTask(restored)
	if cloned.SubjectID != "user-a" || cloned.Request.SubjectID != "user-a" {
		t.Fatalf("task clone lost owner: %+v", cloned)
	}

	cloned.SubjectID = "user-b"
	cloned.Request.SubjectID = "user-b"
	if err := SaveGatewayTaskToDB(db, cloned); !errors.Is(err, ErrTaskOwnerConflict) {
		t.Fatalf("task owner mutation error = %v, want ErrTaskOwnerConflict", err)
	}
	record, err = LoadTaskFromDBOwned(db, task.ID, "default", "user-a")
	if err != nil || record.SubjectID != "user-a" {
		t.Fatalf("stored task owner changed: record=%+v err=%v", record, err)
	}
}

func TestGatewayTaskFromRecordUsesCanonicalRoutingColumns(t *testing.T) {
	persisted := MessageRequest{
		TaskID: "forged-task", TaskType: "forged-type",
		SessionId: "forged-session", TenantID: "forged-tenant",
		SubjectID: "forged-subject", RoleID: "forged-role",
		Message: "forged message", AgentName: "forged-agent",
		UserId: "forged-user", Channel: "forged-channel",
		ResolvedWorkspace: filepath.Join(t.TempDir(), "forged"),
		Parameters:        map[string]interface{}{"customer_id": "acme"},
		Attachments: []MessageAttachment{
			{Name: "brief.txt", Path: "inputs/brief.txt"},
		},
	}
	payload, err := json.Marshal(persisted)
	if err != nil {
		t.Fatal(err)
	}
	record := GatewayTaskRecord{
		TaskID: "canonical-task", TaskType: "chat",
		SessionID: "canonical-session", TenantID: "tenant-a",
		SubjectID: "person-a", RoleID: "support",
		Message: "canonical message", AgentName: "SupportAgent",
		UserID: "person-a", Channel: "api",
		ResolvedWorkspace: filepath.Join(t.TempDir(), "canonical"),
		RequestPayload:    string(payload),
		QueueName:         agentOSQueueName,
	}

	task := gatewayTaskFromRecord(record)
	if task == nil || task.Request == nil {
		t.Fatal("restored task request is nil")
	}
	request := task.Request
	if request.TaskID != record.TaskID ||
		request.TaskType != record.TaskType ||
		request.SessionId != record.SessionID ||
		request.TenantID != record.TenantID ||
		request.SubjectID != record.SubjectID ||
		request.RoleID != record.RoleID ||
		request.Message != record.Message ||
		request.AgentName != record.AgentName ||
		request.UserId != record.UserID ||
		request.Channel != record.Channel ||
		request.ResolvedWorkspace != record.ResolvedWorkspace {
		t.Fatalf("request payload overrode canonical routing columns: %+v", request)
	}
	parameters, ok := request.Parameters.(map[string]interface{})
	if !ok || parameters["customer_id"] != "acme" {
		t.Fatalf("extensible parameters were not restored: %#v", request.Parameters)
	}
	if len(request.Attachments) != 1 || request.Attachments[0].Name != "brief.txt" {
		t.Fatalf("attachments were not restored: %+v", request.Attachments)
	}
}

func TestSaveGatewayTaskRejectsTenantMismatch(t *testing.T) {
	_, db := newPersonalBoundaryGateway(t)
	now := time.Now().Round(0)
	task := &GatewayTask{
		ID: "tenant-owner-conflict",
		Request: &MessageRequest{
			TaskID: "tenant-owner-conflict", TaskType: "chat",
			TenantID: "tenant-b", SubjectID: "person-a",
		},
		TenantID: "tenant-a", SubjectID: "person-a",
		TenantScoped: true, QueueName: agentOSQueueName,
		Status: "pending", CreatedAt: now,
	}
	if err := SaveGatewayTaskToDB(db, task); !errors.Is(err, ErrTaskOwnerConflict) {
		t.Fatalf("tenant mismatch error = %v, want ErrTaskOwnerConflict", err)
	}
	var count int64
	if err := db.Model(&GatewayTaskRecord{}).
		Where("task_id = ?", task.ID).
		Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("persisted %d task rows after owner conflict", count)
	}
}

func TestSaveGatewayTaskBackfillsFallbackOwnerIntoMemory(t *testing.T) {
	_, db := newPersonalBoundaryGateway(t)
	now := time.Now().Round(0)
	task := &GatewayTask{
		ID: "fallback-owner-task", CallerID: "internal-scheduler",
		QueueName: agentOSQueueName, Status: "pending", CreatedAt: now,
		Request: &MessageRequest{
			TaskID: "fallback-owner-task", TaskType: "chat",
			SessionId: "fallback-owner-session",
		},
	}
	if err := SaveGatewayTaskToDB(db, task); err != nil {
		t.Fatal(err)
	}
	if task.TenantID != defaultExecutionTenantID ||
		strings.TrimSpace(task.SubjectID) == "" ||
		!task.TenantScoped ||
		task.Request.TenantID != task.TenantID ||
		task.Request.SubjectID != task.SubjectID {
		t.Fatalf("fallback owner was not synchronized to the live task: %+v request=%+v", task, task.Request)
	}
	var record GatewayTaskRecord
	if err := db.Where("task_id = ?", task.ID).First(&record).Error; err != nil {
		t.Fatal(err)
	}
	if record.TenantID != task.TenantID ||
		record.SubjectID != task.SubjectID ||
		!record.TenantScoped {
		t.Fatalf("durable/live fallback owners differ: record=%+v task=%+v", record, task)
	}
}

func TestSaveChatMessageForTaskRequiresExactPersonalSessionOwner(t *testing.T) {
	gateway, db := newPersonalBoundaryGateway(t)
	now := time.Now().Round(0)
	if err := db.Create(&ChatSession{
		ID: "bob-session", Name: "bob-session",
		TenantID: "default", SubjectID: "person-b",
		CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	for _, sessionID := range []string{"bob-session", "missing-session"} {
		task := &GatewayTask{
			ID: "alice-task", TenantID: "default", SubjectID: "person-a",
			TenantScoped: true, QueueName: agentOSQueueName,
			Request: &MessageRequest{
				TaskID: "alice-task", SessionId: sessionID,
				TenantID: "default", SubjectID: "person-a",
			},
		}
		_, err := gateway.saveChatMessageForTask(
			task, sessionID, "assistant", "agent", "private result",
			"completed", task.ID, "api", "person-a",
		)
		if !errors.Is(err, ErrSessionOwnerConflict) {
			t.Fatalf("session %q error = %v, want ErrSessionOwnerConflict", sessionID, err)
		}
	}
	var count int64
	if err := db.Model(&ChatMessage{}).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("wrote %d messages after personal session owner conflict", count)
	}
}

func TestPersonalTaskHistoryDoesNotAdoptEmptyOwner(t *testing.T) {
	_, db := newPersonalBoundaryGateway(t)
	rows := []ChatMessage{
		{SessionID: "same-session", TenantID: "default", SubjectID: "user-a", Role: "user", Content: "A"},
		{SessionID: "same-session", TenantID: "default", SubjectID: "", Role: "user", Content: "legacy"},
		{SessionID: "same-session", TenantID: "default", SubjectID: "user-b", Role: "user", Content: "B"},
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatal(err)
	}
	task := &GatewayTask{
		TenantID: "default", SubjectID: "user-a", TenantScoped: true,
		QueueName: agentOSQueueName,
	}
	var messages []ChatMessage
	query := db.Where("session_id = ?", "same-session")
	if err := scopeChatHistoryForTask(query, task).Order("id").Find(&messages).Error; err != nil {
		t.Fatal(err)
	}
	if len(messages) != 1 || messages[0].Content != "A" {
		t.Fatalf("personal history included another owner: %+v", messages)
	}

	task.SubjectID = ""
	messages = nil
	if err := scopeChatHistoryForTask(db.Where("session_id = ?", "same-session"), task).Find(&messages).Error; err != nil {
		t.Fatal(err)
	}
	if len(messages) != 0 {
		t.Fatalf("scoped task with empty owner adopted legacy history: %+v", messages)
	}
}
