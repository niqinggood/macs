package memory

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"mac/internal/runtimeutil"
)

func TestServiceManagementPageExportAndFencedForget(t *testing.T) {
	ctx := context.Background()
	service := NewService(serviceTestDB(t, filepath.Join(t.TempDir(), "memory.db")), t.TempDir(), "assistant")
	owner := Owner{TenantID: "local", PartitionID: "alice", SessionID: "chat-1"}
	other := Owner{TenantID: "local", PartitionID: "bob", SessionID: "chat-1"}
	for _, item := range []struct {
		key, value, state string
		scope             MemoryScope
	}{
		{"language", "Chinese", "active", MemoryUser},
		{"plan", "Ship soon", "candidate", MemoryProject},
		{"scratch", "private working note", "active", MemorySession},
	} {
		if err := service.Record(ctx, owner, item.key, item.value, item.scope, "", item.state, 5, nil, 0); err != nil {
			t.Fatal(err)
		}
	}
	if err := service.Record(ctx, other, "secret", "other owner", MemoryUser, "", "active", 5, nil, 0); err != nil {
		t.Fatal(err)
	}
	page, total, err := service.Page(ctx, owner, "", "", "", 0, 1)
	if err != nil || total != 2 || len(page) != 1 {
		t.Fatalf("page: %#v, %d, %v", page, total, err)
	}
	filtered, total, err := service.Page(ctx, owner, MemoryProject, "candidate", "Ship", 0, 20)
	if err != nil || total != 1 || len(filtered) != 1 || filtered[0].Key != "plan" {
		t.Fatalf("filter: %#v, %d, %v", filtered, total, err)
	}
	if _, _, err := service.Page(ctx, owner, MemorySession, "", "", 0, 20); err == nil {
		t.Fatal("session scope accepted for management")
	}
	if wildcard, total, err := service.Page(ctx, owner, "", "", "%", 0, 20); err != nil || total != 0 || len(wildcard) != 0 {
		t.Fatalf("search wildcard was not literal: %#v, %d, %v", wildcard, total, err)
	}
	if _, err := service.ForgetExpected(ctx, owner, "language", MemoryUser, 2); err == nil {
		t.Fatal("stale deletion succeeded")
	}
	if deleted, err := service.ForgetExpected(ctx, owner, "language", MemoryUser, 1); err != nil || !deleted {
		t.Fatalf("forget: %v, %v", deleted, err)
	}
	var exported bytes.Buffer
	if err := service.Export(ctx, owner, &exported); err != nil {
		t.Fatal(err)
	}
	var entries []Entry
	if err := json.Unmarshal(exported.Bytes(), &entries); err != nil {
		t.Fatalf("invalid export: %v", err)
	}
	if len(entries) != 1 || entries[0].Key != "plan" || strings.Contains(exported.String(), "Chinese") || strings.Contains(exported.String(), "private working note") || strings.Contains(exported.String(), "other owner") {
		t.Fatalf("export leaked data: %s", exported.String())
	}
}

func TestServiceCreateWithSourceNeverOverwritesExistingKey(t *testing.T) {
	ctx := context.Background()
	service := NewService(serviceTestDB(t, filepath.Join(t.TempDir(), "memory.db")), t.TempDir(), "assistant")
	owner := Owner{TenantID: "local", PartitionID: "alice"}
	source := WriteSource{Type: "user_explicit", Ref: "local-memory-manager"}
	if err := service.CreateWithSource(ctx, owner, "format", "PDF", MemoryUser, "preference", 5, nil, source); err != nil {
		t.Fatal(err)
	}
	if err := service.CreateWithSource(ctx, owner, "format", "DOCX", MemoryUser, "preference", 5, nil, source); !errors.Is(err, ErrMemoryKeyExists) {
		t.Fatalf("duplicate create: %v", err)
	}
	if err := service.CreateWithSource(ctx, owner, "format", "Project DOCX", MemoryProject, "fact", 5, nil, source); err != nil {
		t.Fatalf("different scope was blocked: %v", err)
	}
	entry, err := service.Lookup(ctx, owner, "format", MemoryUser)
	if err != nil || entry == nil || entry.Value != "PDF" || entry.Version != 1 || entry.State != "active" || entry.SourceType != "user_explicit" {
		t.Fatalf("original changed: %#v %v", entry, err)
	}
	if _, err := service.ForgetExpected(ctx, owner, "format", MemoryUser, 1); err != nil {
		t.Fatal(err)
	}
	if err := service.CreateWithSource(ctx, owner, "format", "resurrected", MemoryUser, "preference", 5, nil, source); !errors.Is(err, ErrMemoryKeyExists) {
		t.Fatalf("forgotten key resurrected: %v", err)
	}
	if err := service.CreateWithSource(ctx, owner, "scratch", "working", MemorySession, "fact", 5, nil, source); err == nil {
		t.Fatal("session scope accepted")
	}
	other := Owner{TenantID: "local", PartitionID: "bob"}
	if err := service.CreateWithSource(ctx, other, "format", "DOCX", MemoryUser, "preference", 5, nil, source); err != nil {
		t.Fatalf("other owner could not create: %v", err)
	}
}

func TestServiceSkippedProjectMemoryDoesNotHideUserPreference(t *testing.T) {
	ctx := context.Background()
	service := NewService(serviceTestDB(t, filepath.Join(t.TempDir(), "memory.db")), t.TempDir(), "assistant")
	owner := Owner{TenantID: "local", PartitionID: "alice"}
	if err := service.Record(ctx, owner, "timezone", "UTC+8", MemoryUser, "preference", "active", 5, nil, 0); err != nil {
		t.Fatal(err)
	}
	if err := service.Record(ctx, owner, "timezone", "Mars time", MemoryProject, "fact", "active", 1, nil, 0); err != nil {
		t.Fatal(err)
	}
	result, err := service.Recall(ctx, owner, "What is my UTC+8 setting?", 500, nil)
	if err != nil || !strings.Contains(result.Prompt, "timezone: UTC+8") || strings.Contains(result.Prompt, "Mars time") {
		t.Fatalf("user preference was hidden by skipped project memory: %#v, %v", result, err)
	}
}

type preEvidenceEntry struct {
	ID              string      `gorm:"primaryKey;size:64"`
	Namespace       string      `gorm:"index;size:64;not null"`
	TenantID        string      `gorm:"size:64;not null"`
	OwnerID         string      `gorm:"size:128"`
	AgentName       string      `gorm:"size:100"`
	Key             string      `gorm:"type:text"`
	Value           string      `gorm:"type:text"`
	Scope           MemoryScope `gorm:"size:16"`
	Kind            string      `gorm:"size:16"`
	State           string      `gorm:"size:16;index"`
	Priority        int
	Version         int
	Tags            string `gorm:"type:text"`
	SessionID       string `gorm:"type:text"`
	SourceSessionID string `gorm:"type:text"`
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

func (preEvidenceEntry) TableName() string { return "memory_entries" }

func TestServiceEvidenceColumnsMigrateOldSQLite(t *testing.T) {
	ctx := context.Background()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "memory.db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := db.AutoMigrate(&preEvidenceEntry{}); err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	service := NewService(db, workspace, "assistant")
	owner := Owner{TenantID: "default", PartitionID: "local", SessionID: "one"}
	namespace, err := service.namespace(owner)
	if err != nil {
		t.Fatal(err)
	}
	old := preEvidenceEntry{ID: memoryHash(namespace, agentMemoryStorageKey("old", MemoryUser, "")), Namespace: namespace, TenantID: owner.TenantID,
		Key: "old", Value: "original", Scope: MemoryUser, Kind: "preference", State: "active", Priority: 5, Version: 1, Tags: "[]", SourceSessionID: "one"}
	if err := db.Create(&old).Error; err != nil {
		t.Fatal(err)
	}
	if err := InitMemoryDB(db); err != nil {
		t.Fatal(err)
	}
	entry, err := service.Lookup(ctx, owner, "old", MemoryUser)
	if err != nil || entry.Value != "original" || entry.SourceType != "" {
		t.Fatalf("old memory lost during migration: %#v %v", entry, err)
	}
	if err := service.RecordWithSource(ctx, owner, "old", "updated", MemoryUser, "preference", "active", 5, nil, 1,
		WriteSource{Type: "user_explicit", Ref: "turn-2", Quote: "updated"}); err != nil {
		t.Fatal(err)
	}
	entry, err = service.Lookup(ctx, owner, "old", MemoryUser)
	if err != nil || entry.Version != 2 || entry.SourceType != "user_explicit" || entry.SourceRef != "turn-2" {
		t.Fatalf("new evidence columns not usable: %#v %v", entry, err)
	}
}

func TestServiceToolDurableWriteEvidence(t *testing.T) {
	base := context.WithValue(context.Background(), "task_id", "turn-1")
	workspace := t.TempDir()
	service := NewService(serviceTestDB(t, filepath.Join(t.TempDir(), "memory.db")), workspace, "assistant")
	owner := Owner{TenantID: "default", PartitionID: "local", SessionID: "one"}
	resolve := func(context.Context) (Owner, error) { return owner, nil }
	tool := NewAgentMemoryServiceTool(service, resolve, nil)
	scm := NewSessionContextManager(filepath.Join(workspace, ".openkitty", "context"))
	scm.SetWorkspace(workspace)
	if err := service.SetContextStore(scm); err != nil {
		t.Fatal(err)
	}
	contextTool := NewSessionContextServiceTool(scm, service, resolve, nil)
	if receipt, err := contextTool.Execute(base, map[string]interface{}{"action": "set", "scope": "durable", "category": "preference", "key": "drink", "value": "Tea"}); err != nil || !strings.Contains(receipt, "candidate") {
		t.Fatalf("unverified context was not a candidate: %s %v", receipt, err)
	}
	if found, err := contextTool.Execute(base, map[string]interface{}{"action": "get", "scope": "durable", "key": "drink"}); err != nil || strings.Contains(found, "Tea") {
		t.Fatalf("candidate leaked through context get: %s %v", found, err)
	}
	if receipt, err := contextTool.Execute(WithUserTurn(base, "请记住 Tea"), map[string]interface{}{"action": "set", "scope": "durable", "category": "preference", "key": "drink", "value": "Tea"}); err != nil || !strings.Contains(receipt, "active") {
		t.Fatalf("explicit context not activated: %s %v", receipt, err)
	}
	write := func(ctx context.Context, key, value string) {
		t.Helper()
		if _, err := tool.Execute(ctx, map[string]interface{}{"action": "remember", "scope": "user", "key": key, "value": value}); err != nil {
			t.Fatal(err)
		}
	}
	write(base, "timezone", "Asia/Shanghai")
	entry, err := service.Lookup(base, owner, "timezone", MemoryUser)
	if err != nil || entry.State != "candidate" || entry.SourceType != "agent_candidate" || entry.SourceRef != "turn-1" {
		t.Fatalf("model claim became active: %#v %v", entry, err)
	}
	write(WithUserTurn(base, "请记住 Asia/Shanghai"), "timezone", "Asia/Shanghai")
	entry, err = service.Lookup(base, owner, "timezone", MemoryUser)
	if err != nil || entry.State != "active" || entry.SourceType != "user_explicit" || entry.SourceQuote != "Asia/Shanghai" || entry.SourceRef != "turn-1" {
		t.Fatalf("explicit request not recorded: %#v %v", entry, err)
	}
	timezoneID := entry.ID
	if err := service.Record(base, owner, "format", "Word", MemoryProject, "fact", "active", 5, nil, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := tool.Execute(base, map[string]interface{}{"action": "correct", "scope": "project", "key": "format", "value": "PDF", "expected_version": 1}); err == nil {
		t.Fatal("model corrected active long-term memory without user request")
	}
	if _, err := tool.Execute(WithUserTurn(base, "请记住 PDF"), map[string]interface{}{"action": "correct", "scope": "project", "key": "format", "value": "PDF", "expected_version": 1}); err != nil {
		t.Fatal(err)
	}
	entry, err = service.Lookup(base, owner, "format", MemoryProject)
	if err != nil || entry.Value != "PDF" || entry.SourceType != "user_explicit" {
		t.Fatalf("explicit correction missing: %#v %v", entry, err)
	}
	write(WithUserTurn(base, "一般来说时区是 UTC"), "guess", "UTC")
	entry, err = service.Lookup(base, owner, "guess", MemoryUser)
	if err != nil || entry.State != "candidate" {
		t.Fatalf("plain mention activated memory: %#v %v", entry, err)
	}
	write(WithUserTurn(base, "请记住不要使用 Word"), "format_guess", "Word")
	entry, err = service.Lookup(base, owner, "format_guess", MemoryUser)
	if err != nil || entry.State != "candidate" {
		t.Fatalf("partial quote ignored a negation: %#v %v", entry, err)
	}
	if deleted, err := service.Forget(base, owner, "timezone", MemoryUser); err != nil || !deleted {
		t.Fatalf("forget: %v %v", deleted, err)
	}
	var tombstone Entry
	if err := service.db.Where("id = ?", timezoneID).First(&tombstone).Error; err != nil || tombstone.SourceType != "" || tombstone.SourceQuote != "" || tombstone.SourceRef != "" {
		t.Fatalf("forgotten evidence retained: %#v %v", tombstone, err)
	}
}

func TestServiceRecallFindsOlderRelevantEntryAndSummary(t *testing.T) {
	ctx := context.Background()
	service := NewService(serviceTestDB(t, filepath.Join(t.TempDir(), "memory.db")), t.TempDir(), "assistant")
	owner := Owner{TenantID: "default", PartitionID: "local", SessionID: "one"}
	if err := service.Record(ctx, owner, "old_project", "蓝天计划使用 SQLite", MemoryProject, "decision", "active", 1, nil, 0); err != nil {
		t.Fatal(err)
	}
	if err := service.SaveSummary(ctx, owner, "蓝天计划最初选用 SQLite"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 210; i++ {
		if err := service.Record(ctx, owner, fmt.Sprintf("other_%03d", i), "unrelated note", MemoryUser, "preference", "active", 10, nil, 0); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 55; i++ {
		owner.SessionID = fmt.Sprintf("session_%03d", i)
		if err := service.SaveSummary(ctx, owner, "无关会话摘要"); err != nil {
			t.Fatal(err)
		}
	}
	result, err := service.Recall(ctx, owner, "蓝天计划", 700, nil)
	if err != nil || !strings.Contains(result.Prompt, "old_project: 蓝天计划使用 SQLite") || !strings.Contains(result.Prompt, "蓝天计划最初选用 SQLite") || result.Tokens > 700 {
		t.Fatalf("older relevant memory missing: %#v %v", result, err)
	}
}

func TestServiceCandidateConfirmationLifecycle(t *testing.T) {
	ctx := context.Background()
	service := NewService(serviceTestDB(t, filepath.Join(t.TempDir(), "memory.db")), t.TempDir(), "assistant")
	owner := Owner{TenantID: "default", SubjectID: "alice", PartitionID: "alice", SessionID: "source"}
	if err := service.Record(ctx, owner, "format", "Word", MemoryUser, "preference", "candidate", 7, []string{"documents"}, 0); err != nil {
		t.Fatal(err)
	}
	owner.SessionID = "review"
	tool := NewAgentMemoryServiceTool(service, func(context.Context) (Owner, error) { return owner, nil }, nil)
	result, err := service.Recall(ctx, owner, "format", 2000, nil)
	if err != nil || result.Prompt != "" {
		t.Fatalf("candidate auto-recalled: %#v %v", result, err)
	}
	listed, err := tool.Execute(ctx, map[string]interface{}{"action": "list", "state": "candidate"})
	var entries []Entry
	if err != nil || json.Unmarshal([]byte(listed), &entries) != nil || len(entries) != 1 || entries[0].Version != 1 {
		t.Fatalf("candidate list: %s %v", listed, err)
	}
	for _, params := range []map[string]interface{}{
		{"action": "confirm", "key": "format", "scope": "user"},
		{"action": "confirm", "key": "format", "expected_version": 1},
		{"action": "confirm", "key": "format", "scope": "user", "expected_version": 2},
	} {
		if _, err := tool.Execute(ctx, params); err == nil {
			t.Fatalf("invalid confirmation accepted: %#v", params)
		}
	}
	if err := service.Record(ctx, owner, "format", "PDF", MemoryUser, "", "candidate", 7, []string{"documents"}, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Confirm(ctx, owner, "format", MemoryUser, 1); err == nil {
		t.Fatal("stale candidate was confirmed")
	}
	foreign := owner
	foreign.SubjectID = "bob"
	if _, err := service.Confirm(ctx, foreign, "format", MemoryUser, 2); err == nil {
		t.Fatal("foreign candidate was confirmed")
	}
	blocked := NewAgentMemoryServiceTool(service, func(context.Context) (Owner, error) { return owner, nil }, func(context.Context, bool) bool { return false })
	if _, err := blocked.Execute(ctx, map[string]interface{}{"action": "confirm", "key": "format", "scope": "user", "expected_version": 2}); err == nil {
		t.Fatal("disabled memory permitted confirmation")
	}
	before, err := service.Lookup(ctx, owner, "format", MemoryUser)
	if err != nil {
		t.Fatal(err)
	}
	owner.SessionID = "confirmation"
	approvedCtx := WithUserTurn(context.WithValue(ctx, "task_id", "confirm-task"), "确认记忆 format")
	if value, err := tool.Execute(approvedCtx, map[string]interface{}{"action": "confirm", "key": "format", "scope": "user", "expected_version": 2}); err != nil || !strings.Contains(value, "v3") {
		t.Fatalf("confirm: %s %v", value, err)
	}
	after, err := service.Lookup(ctx, owner, "format", MemoryUser)
	if err != nil || after.State != "active" || after.Version != 3 || after.Value != before.Value || after.Tags != before.Tags || after.SourceSessionID != before.SourceSessionID || after.ConfirmedRef != "confirm-task" || after.ConfirmedQuote != "format" || !after.CreatedAt.Equal(before.CreatedAt) {
		t.Fatalf("confirmation rewrote evidence: %#v %v", after, err)
	}
	result, err = service.Recall(ctx, owner, "format", 2000, nil)
	if err != nil || !strings.Contains(result.Prompt, "format: PDF") {
		t.Fatalf("confirmed memory absent: %#v %v", result, err)
	}
	if _, err := service.Confirm(ctx, owner, "format", MemoryUser, 3); err == nil {
		t.Fatal("active entry accepted confirmation")
	}
	for _, version := range []int{0, 3} {
		if err := service.Record(ctx, owner, "format", "inferred HTML", MemoryUser, "", "candidate", 10, nil, version); err == nil {
			t.Fatal("candidate replaced active memory")
		}
	}
	if deleted, err := service.Forget(ctx, owner, "format", MemoryUser); err != nil || !deleted {
		t.Fatalf("forget: %v %v", deleted, err)
	}
	if err := service.Record(ctx, owner, "format", "PDF", MemoryUser, "", "candidate", 10, nil, 0); err == nil {
		t.Fatal("inference resurrected forgotten memory")
	}
	if _, err := service.Confirm(ctx, owner, "format", MemoryUser, 4); err == nil {
		t.Fatal("deleted entry was confirmed")
	}
	if err := service.Record(ctx, owner, "local", "temporary", MemorySession, "fact", "candidate", 5, nil, 0); err != nil {
		t.Fatal(err)
	}
	otherSession := owner
	otherSession.SessionID = "another"
	if _, err := service.Confirm(ctx, otherSession, "local", MemorySession, 1); err == nil {
		t.Fatal("foreign session candidate confirmed")
	}
	if _, err := tool.Execute(ctx, map[string]interface{}{"action": "list", "state": "deleted"}); err == nil {
		t.Fatal("invalid list state accepted")
	}
}

func TestServiceCandidatesDoNotCrowdActiveRecall(t *testing.T) {
	ctx := context.Background()
	service := NewService(serviceTestDB(t, filepath.Join(t.TempDir(), "memory.db")), t.TempDir(), "assistant")
	owner := Owner{TenantID: "default", PartitionID: "local", SessionID: "one"}
	if err := service.Record(ctx, owner, "language", "中文", MemoryUser, "preference", "active", 1, nil, 0); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 205; i++ {
		if err := service.Record(ctx, owner, fmt.Sprintf("guess_%d", i), "unconfirmed", MemoryUser, "preference", "candidate", 10, nil, 0); err != nil {
			t.Fatal(err)
		}
	}
	result, err := service.Recall(ctx, owner, "language", 2000, nil)
	if err != nil || !strings.Contains(result.Prompt, "language: 中文") || strings.Contains(result.Prompt, "unconfirmed") {
		t.Fatalf("candidate flood displaced active memory: %#v %v", result, err)
	}
	entries, err := service.ListByState(ctx, owner, "", "", "active", 20)
	if err != nil || len(entries) != 1 || entries[0].Key != "language" {
		t.Fatalf("active list: %#v %v", entries, err)
	}
}

func serviceTestDB(t *testing.T, path string) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(path), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	if err := InitMemoryDB(db); err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	return db
}

func TestServiceMigrationCorrectionForgetAndReopen(t *testing.T) {
	ctx := context.Background()
	workspace := t.TempDir()
	path := filepath.Join(t.TempDir(), "memory.db")
	db := serviceTestDB(t, path)
	legacy := NewAgentMemoryStore(workspace, "assistant")
	if err := legacy.Remember("format", "old_pdf_preference", MemoryUser, 7, nil, "first"); err != nil {
		t.Fatal(err)
	}
	service := NewService(db, workspace, "assistant")
	owner := Owner{TenantID: "company", SubjectID: "alice", PartitionID: "alice-partition", SessionID: "second", ImportLegacy: true}
	result, err := service.Recall(ctx, owner, "format", 2000, nil)
	if err != nil || !strings.Contains(result.Prompt, "old_pdf_preference") || result.Items[0].SourceSessionID != "first" {
		t.Fatalf("legacy recall: %#v %v", result, err)
	}
	if err := service.SaveSummary(ctx, owner, "format old_pdf_preference"); err != nil {
		t.Fatal(err)
	}
	if err := service.Record(ctx, owner, "format", "new_word_preference", MemoryUser, "preference", "active", 7, nil, 1); err != nil {
		t.Fatal(err)
	}
	if err := service.Record(ctx, owner, "format", "stale_update", MemoryUser, "", "", 7, nil, 1); err == nil {
		t.Fatal("stale correction succeeded")
	}
	result, err = service.Recall(ctx, owner, "format", 2000, []RecallItem{{ID: "old", Source: "history", Content: "曾经的格式是 old_pdf_preference"}})
	if err != nil || !strings.Contains(result.Prompt, "new_word_preference") || strings.Contains(result.Prompt, "old_pdf_preference") {
		t.Fatalf("stale recall: %#v %v", result, err)
	}
	deleted, err := service.Forget(ctx, owner, "format", MemoryUser)
	if err != nil || !deleted {
		t.Fatalf("forget: %v %v", deleted, err)
	}
	// New service/process reads the database, never the unchanged JSON again.
	reopened := NewService(serviceTestDB(t, path), workspace, "assistant")
	result, err = reopened.Recall(ctx, owner, "format", 2000, []RecallItem{{ID: "recent", Source: "history", Content: "format new_word_preference"}})
	if err != nil || result.Prompt != "" {
		t.Fatalf("deleted record resurrected: %#v %v", result, err)
	}
	entry, err := reopened.Lookup(ctx, owner, "format", MemoryUser)
	if err != nil || entry != nil {
		t.Fatalf("deleted lookup: %#v %v", entry, err)
	}
	var tombstone Entry
	if err := db.First(&tombstone).Error; err != nil || tombstone.Value != "" || tombstone.State != "deleted" || tombstone.Version != 3 {
		t.Fatalf("tombstone: %#v %v", tombstone, err)
	}
}

func TestServiceOwnerScopeCandidatesAndBudget(t *testing.T) {
	ctx := context.Background()
	db := serviceTestDB(t, filepath.Join(t.TempDir(), "memory.db"))
	workspace := t.TempDir()
	service := NewService(db, workspace, "assistant")
	alice := Owner{TenantID: "a", SubjectID: "alice", PartitionID: "a-alice", SessionID: "one"}
	for _, item := range []struct {
		key, value, state string
		scope             MemoryScope
	}{
		{"language", "English", "active", MemoryUser}, {"language", "中文", "active", MemorySession},
		{"personality", "unconfirmed_inference", "candidate", MemoryUser}, {"long", strings.Repeat("详细内容", 1000), "active", MemoryUser},
	} {
		if err := service.Record(ctx, alice, item.key, item.value, item.scope, "", item.state, 8, nil, 0); err != nil {
			t.Fatal(err)
		}
	}
	if err := service.SaveSummary(ctx, alice, "独特项目采用SQLite部署"); err != nil {
		t.Fatal(err)
	}
	result, err := service.Recall(ctx, alice, "SQLite部署", 800, nil)
	if err != nil || !strings.Contains(result.Prompt, "中文") || strings.Contains(result.Prompt, "English") || strings.Contains(result.Prompt, "unconfirmed_inference") || !strings.Contains(result.Prompt, "summary") {
		t.Fatalf("scoped recall: %#v %v", result, err)
	}
	if result.Tokens > 800 || runtimeutil.EstimateTokens(result.Prompt) > 800 {
		t.Fatalf("budget exceeded: %d", result.Tokens)
	}
	next := alice
	next.SessionID = "two"
	result, err = service.Recall(ctx, next, "language", 2000, nil)
	if err != nil || !strings.Contains(result.Prompt, "English") || strings.Contains(result.Prompt, "中文") {
		t.Fatalf("session leaked: %#v %v", result, err)
	}
	for _, owner := range []Owner{
		{TenantID: "a", SubjectID: "bob", PartitionID: "a-alice", SessionID: "one"},
		{TenantID: "b", SubjectID: "alice", PartitionID: "a-alice", SessionID: "one"},
	} {
		result, err := service.Recall(ctx, owner, "SQLite部署", 2000, nil)
		if err != nil || result.Prompt != "" {
			t.Fatalf("owner leak: %#v %v", result, err)
		}
	}
	foreign := NewService(db, t.TempDir(), "assistant")
	foreignSCM := NewSessionContextManager(filepath.Join(t.TempDir(), "context"))
	foreignSCM.SetWorkspace(t.TempDir())
	if err := service.SetContextStore(foreignSCM); err == nil {
		t.Fatal("foreign context store accepted")
	}
	result, err = foreign.Recall(ctx, alice, "language", 2000, nil)
	if err != nil || result.Prompt != "" {
		t.Fatal("workspace leak")
	}
	foreign = NewService(db, workspace, "other-agent")
	result, err = foreign.Recall(ctx, alice, "language", 2000, nil)
	if err != nil || result.Prompt != "" {
		t.Fatal("agent leak")
	}
	if _, err := service.Recall(ctx, Owner{}, "language", 2000, nil); err == nil {
		t.Fatal("untrusted owner accepted")
	}
}

func TestServiceContextToolSharesAuthorityAndMigration(t *testing.T) {
	ctx := context.WithValue(context.Background(), "session_id", "one")
	workspace := t.TempDir()
	service := NewService(serviceTestDB(t, filepath.Join(t.TempDir(), "memory.db")), workspace, "assistant")
	scm := NewSessionContextManager(filepath.Join(workspace, ".openkitty", "context"))
	scm.SetWorkspace(workspace)
	if err := scm.SetDurable("database", "SQLite", "fact", 8); err != nil {
		t.Fatal(err)
	}
	service.SetContextStore(scm)
	owner := Owner{TenantID: "default", PartitionID: "default", SessionID: "one", ImportLegacy: true}
	resolve := func(context.Context) (Owner, error) { return owner, nil }
	contextTool := NewSessionContextServiceTool(scm, service, resolve, nil)
	memoryTool := NewAgentMemoryServiceTool(service, resolve, nil)
	if value, err := contextTool.Execute(ctx, map[string]interface{}{"action": "get", "key": "database"}); err != nil || !strings.Contains(value, "SQLite") {
		t.Fatalf("migration: %s %v", value, err)
	}
	if _, err := memoryTool.Execute(WithUserTurn(ctx, "请记住 Postgres"), map[string]interface{}{"action": "correct", "key": "database", "scope": "project", "value": "Postgres", "expected_version": 1}); err != nil {
		t.Fatal(err)
	}
	if value, err := contextTool.Execute(ctx, map[string]interface{}{"action": "get", "scope": "durable", "key": "database"}); err != nil || strings.Contains(value, "SQLite") || !strings.Contains(value, "Postgres") {
		t.Fatalf("diverging tools: %s %v", value, err)
	}
	if _, err := contextTool.Execute(ctx, map[string]interface{}{"action": "delete", "scope": "durable", "key": "database"}); err != nil {
		t.Fatal(err)
	}
	if value, err := memoryTool.Execute(ctx, map[string]interface{}{"action": "recall", "key": "database"}); err != nil || strings.Contains(value, "Postgres") {
		t.Fatalf("forgotten context returned: %s %v", value, err)
	}
	if _, err := contextTool.Execute(ctx, map[string]interface{}{"action": "set", "scope": "session", "key": "temporary", "value": "once"}); err != nil {
		t.Fatal(err)
	}
	if err := service.Record(ctx, owner, "temporary", "durable", MemoryUser, "preference", "active", 5, nil, 0); err != nil {
		t.Fatal(err)
	}
	result, err := service.Recall(ctx, owner, "unrelated question", 2000, nil)
	if err != nil || !strings.Contains(result.Prompt, "temporary: once") || strings.Contains(result.Prompt, "temporary: durable") {
		t.Fatalf("working override: %#v %v", result, err)
	}
	if prompt := scm.BuildSessionOnlyPrompt("two", 10); strings.Contains(prompt, "once") || strings.Contains(prompt, "SQLite") {
		t.Fatalf("legacy durable/session leak: %s", prompt)
	}
}

func TestServicePersistentJobsRetryIsolationAndLeaseFencing(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "memory.db")
	db := serviceTestDB(t, path)
	workspace := t.TempDir()
	service := NewService(db, workspace, "assistant")
	owner := Owner{TenantID: "a", SubjectID: "alice", PartitionID: "alice", SessionID: "one"}
	if err := service.Enqueue(ctx, owner, "summary", map[string]string{"content": "test"}); err != nil {
		t.Fatal(err)
	}
	if err := service.Enqueue(ctx, owner, "summary", map[string]string{"content": "test"}); err != nil {
		t.Fatal(err)
	}
	var count int64
	db.Model(&ProcessingJob{}).Count(&count)
	if count != 1 {
		t.Fatalf("duplicate jobs: %d", count)
	}
	bob := owner
	bob.SubjectID = "bob"
	if err := service.ProcessPending(ctx, bob, func(context.Context, ProcessingJob) error { t.Fatal("foreign job processed"); return nil }); err != nil {
		t.Fatal(err)
	}
	if err := service.ProcessPending(ctx, owner, func(context.Context, ProcessingJob) error { return errors.New("temporary failure") }); err == nil || !strings.Contains(err.Error(), "handler_failed") || strings.Contains(err.Error(), "temporary failure") {
		t.Fatalf("handler failure was not reported: %v", err)
	}
	var job ProcessingJob
	db.First(&job)
	if job.State != "pending" || job.Attempts != 1 || job.Payload == "" || job.LastError != "handler_failed" {
		t.Fatalf("retry lost: %#v", job)
	}
	db.Model(&ProcessingJob{}).Where("id = ?", job.ID).Updates(map[string]interface{}{"state": "running", "lease_until": time.Now().Add(-time.Minute), "available_at": time.Now().Add(-time.Minute)})
	reopened := NewService(serviceTestDB(t, path), workspace, "assistant")
	if err := reopened.ProcessPending(ctx, owner, func(ctx context.Context, j ProcessingJob) error { return nil }); err != nil {
		t.Fatal(err)
	}
	db.First(&job)
	if job.State != "done" || job.Payload != "" || job.Attempts != 2 || job.LastError != "" {
		t.Fatalf("restart recovery: %#v", job)
	}
	if err := service.Enqueue(ctx, owner, "derive", "fence"); err != nil {
		t.Fatal(err)
	}
	err := service.ProcessPending(ctx, owner, func(ctx context.Context, j ProcessingJob) error {
		return db.Model(&ProcessingJob{}).Where("id = ?", j.ID).Update("lease_token", "new-worker").Error
	})
	if err == nil || !strings.Contains(err.Error(), "superseded") {
		t.Fatalf("stale worker completed: %v", err)
	}
}

func TestServiceCorruptImportAndCancellationFailClosed(t *testing.T) {
	workspace := t.TempDir()
	path := filepath.Join(workspace, ".openkitty", "memories", "assistant.json")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("broken-json"), 0600); err != nil {
		t.Fatal(err)
	}
	db := serviceTestDB(t, filepath.Join(t.TempDir(), "memory.db"))
	service := NewService(db, workspace, "assistant")
	owner := Owner{TenantID: "default", PartitionID: "default", ImportLegacy: true, SessionID: "one"}
	if err := service.Record(context.Background(), owner, "key", "value", MemoryUser, "", "", 5, nil, 0); err == nil {
		t.Fatal("corrupt import overwritten")
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "broken-json" {
		t.Fatalf("legacy changed: %s %v", data, err)
	}
	var count int64
	db.Model(&memoryMigration{}).Count(&count)
	if count != 0 {
		t.Fatal("failed migration committed marker")
	}
	owner.ImportLegacy = false
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := service.Record(ctx, owner, "key", "value", MemoryUser, "", "", 5, nil, 0); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled operation: %v", err)
	}
	if _, err := service.Recall(nil, owner, "key", 2000, nil); err == nil {
		t.Fatal("nil context accepted")
	}
}
