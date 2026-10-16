package memory

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func promptTestService(t *testing.T) (*Service, Owner) {
	t.Helper()
	workspace := t.TempDir()
	service := NewService(serviceTestDB(t, filepath.Join(t.TempDir(), "memory.db")), workspace, "assistant")
	return service, Owner{TenantID: "company", SubjectID: "alice", PartitionID: "alice-workbench", SessionID: "chat-one"}
}

func promptTestInput(owner Owner) PromptInput {
	return PromptInput{SessionID: owner.SessionID, TaskID: "task-one", Agent: "assistant", CallIndex: 1, Status: "attempted", Sources: []PromptSource{{ID: "original-id", Kind: "entry", Title: "Formatting", SourceLabel: "Saved memory", Excerpt: "Use a compact table.", CapturedRevision: "captured-digest", Locator: PromptLocator{Kind: "entry", ID: "internal-record", Key: "private-key", Path: "private-source-path", SessionID: "private-session", Version: 7}}}}
}

func TestPromptInputsOwnedReceiptsHideLocatorsAndKeepSnapshots(t *testing.T) {
	ctx := context.Background()
	service, owner := promptTestService(t)
	input := promptTestInput(owner)
	saved, err := service.SavePromptInput(ctx, owner, input)
	if err != nil {
		t.Fatal(err)
	}
	if saved.ID == "" || saved.Sources[0].ID == input.Sources[0].ID || input.Sources[0].ID != "original-id" {
		t.Fatal("opaque IDs were not assigned independently of caller data")
	}
	loaded, err := service.GetPromptInput(ctx, owner, saved.ID)
	if err != nil || loaded.Sources[0].Locator != input.Sources[0].Locator || loaded.Sources[0].Excerpt != input.Sources[0].Excerpt {
		t.Fatalf("receipt round trip: %#v %v", loaded, err)
	}
	encoded, err := json.Marshal(loaded)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"Locator", "locator", "private-key", "private-source-path", "private-session", "internal-record", "Namespace"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("HTTP receipt includes internal locator %q", forbidden)
		}
	}
	for _, foreign := range []struct {
		service *Service
		owner   Owner
	}{
		{service, Owner{TenantID: "other", SubjectID: owner.SubjectID, PartitionID: owner.PartitionID}},
		{service, Owner{TenantID: owner.TenantID, SubjectID: "bob", PartitionID: owner.PartitionID}},
		{service, Owner{TenantID: owner.TenantID, SubjectID: owner.SubjectID, PartitionID: "other-partition"}},
		{NewService(service.db, t.TempDir(), "assistant"), owner},
		{NewService(service.db, service.workspace, "other-agent"), owner},
	} {
		if _, err := foreign.service.GetPromptInput(ctx, foreign.owner, saved.ID); !errors.Is(err, gorm.ErrRecordNotFound) {
			t.Fatalf("foreign receipt lookup: %v", err)
		}
		items, err := foreign.service.RecentPromptInputs(ctx, foreign.owner, "", 20)
		if err != nil || len(items) != 0 {
			t.Fatalf("foreign receipt list: %#v %v", items, err)
		}
	}
	items, err := service.RecentPromptInputs(ctx, owner, owner.SessionID, 20)
	if err != nil || len(items) != 1 || items[0].Sources[0].Locator.ID != "internal-record" {
		t.Fatalf("recent receipt: %#v %v", items, err)
	}
	if _, err := service.RecentPromptInputs(ctx, owner, "foreign-session", 20); err == nil {
		t.Fatal("untrusted session selector accepted")
	}
}

func TestPromptInputsBoundsRetentionAndReadDoesNotMigrate(t *testing.T) {
	ctx := context.Background()
	service, owner := promptTestService(t)
	for _, change := range []func(*PromptInput){
		func(i *PromptInput) { i.Agent = "other" },
		func(i *PromptInput) { i.SessionID = "foreign-session" },
		func(i *PromptInput) { i.Status = "used-by-model" },
		func(i *PromptInput) { i.CallIndex = 0 },
		func(i *PromptInput) { i.Sources = make([]PromptSource, maxPromptInputSources+1) },
		func(i *PromptInput) { i.Sources[0].Excerpt = strings.Repeat("x", maxPromptInputBytes) },
	} {
		input := promptTestInput(owner)
		change(&input)
		if _, err := service.SavePromptInput(ctx, owner, input); err == nil {
			t.Fatal("invalid or unbounded input accepted")
		}
	}
	namespace, _ := service.namespace(owner)
	foreignNamespace, _ := service.namespace(Owner{TenantID: "other", SubjectID: "bob", PartitionID: "other"})
	for _, row := range []promptInputRecord{
		{ID: "expired-owned", Namespace: namespace, CreatedAt: time.Now().Add(-31 * 24 * time.Hour)},
		{ID: "expired-foreign", Namespace: foreignNamespace, CreatedAt: time.Now().Add(-31 * 24 * time.Hour)},
	} {
		if err := service.db.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
	}
	rows := make([]promptInputRecord, maxPromptInputs)
	for i := range rows {
		rows[i] = promptInputRecord{ID: memoryHash("test-receipt", string(rune(i))), Namespace: namespace, SessionID: owner.SessionID, Agent: "assistant", Status: "attempted", Sources: "[]", Locators: "{}", CreatedAt: time.Now().Add(-time.Hour)}
	}
	if err := service.db.CreateInBatches(rows, 100).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := service.SavePromptInput(ctx, owner, promptTestInput(owner)); err != nil {
		t.Fatal(err)
	}
	var count int64
	if err := service.db.Model(&promptInputRecord{}).Where("namespace = ?", namespace).Count(&count).Error; err != nil || count != maxPromptInputs {
		t.Fatalf("receipt retention count: %d %v", count, err)
	}
	if err := service.db.Model(&promptInputRecord{}).Where("id = ?", "expired-foreign").Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("retention touched another owner: %d %v", count, err)
	}
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "uninitialized.db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	empty := NewService(db, t.TempDir(), "assistant")
	if _, err := empty.RecentPromptInputs(ctx, owner, "", 10); err == nil {
		t.Fatal("missing schema read pretended success")
	}
	if db.Migrator().HasTable(&promptInputRecord{}) {
		t.Fatal("read-only receipt GET created a table")
	}
}

func TestPromptInputsStatusUpdateOnlyChangesOwnedReceiptStatus(t *testing.T) {
	ctx := context.Background()
	service, owner := promptTestService(t)
	input, err := service.SavePromptInput(ctx, owner, promptTestInput(owner))
	if err != nil {
		t.Fatal(err)
	}
	var original promptInputRecord
	if err := service.db.First(&original, "id = ?", input.ID).Error; err != nil {
		t.Fatal(err)
	}
	for _, status := range []string{"succeeded", "succeeded", "failed", "attempted"} {
		if err := service.UpdatePromptInputStatus(ctx, owner, input.ID, status); err != nil {
			t.Fatal(err)
		}
		var current promptInputRecord
		if err := service.db.First(&current, "id = ?", input.ID).Error; err != nil {
			t.Fatal(err)
		}
		if current.Status != status || current.Sources != original.Sources || current.Locators != original.Locators || !current.CreatedAt.Equal(original.CreatedAt) || current.SessionID != original.SessionID || current.TaskID != original.TaskID || current.Agent != original.Agent || current.CallIndex != original.CallIndex {
			t.Fatal("status update modified a captured input snapshot")
		}
	}
	foreign := owner
	foreign.SubjectID = "someone-else"
	if err := service.UpdatePromptInputStatus(ctx, foreign, input.ID, "succeeded"); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("foreign owner status update: %v", err)
	}
	if err := service.UpdatePromptInputStatus(ctx, owner, "missing-receipt", "failed"); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("missing receipt status update: %v", err)
	}
	for _, status := range []string{"", "provider_accepted", "used_by_model"} {
		if err := service.UpdatePromptInputStatus(ctx, owner, input.ID, status); err == nil {
			t.Fatal("unrecognized status accepted")
		}
	}
	if err := service.UpdatePromptInputStatus(ctx, Owner{}, input.ID, "failed"); err == nil {
		t.Fatal("status update accepted an unavailable trusted owner")
	}
	if err := NewGlobalService(service.db, service.workspace).UpdatePromptInputStatus(ctx, owner, input.ID, "failed"); err == nil {
		t.Fatal("global service accepted an input status update")
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err := service.UpdatePromptInputStatus(cancelled, owner, input.ID, "failed"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled update: %v", err)
	}
	loaded, err := service.GetPromptInput(ctx, owner, input.ID)
	if err != nil || loaded.Status != "attempted" || loaded.Sources[0].Locator != input.Sources[0].Locator {
		t.Fatalf("rejected status update changed the receipt: %#v %v", loaded, err)
	}
}

func TestPromptInputsRecentCallsUseCallIndexWhenTimestampsCollide(t *testing.T) {
	ctx := context.Background()
	service, owner := promptTestService(t)
	namespace, _ := service.namespace(owner)
	created := time.Now().Add(-time.Minute).Truncate(time.Millisecond)
	for _, row := range []promptInputRecord{
		{ID: "z-call-one", CallIndex: 1, CreatedAt: created},
		{ID: "m-call-two", CallIndex: 2, CreatedAt: created},
		{ID: "a-call-three", CallIndex: 3, CreatedAt: created},
		{ID: "older-high-call-index", CallIndex: 100, CreatedAt: created.Add(-time.Millisecond)},
	} {
		row.Namespace, row.SessionID, row.TaskID, row.Agent, row.Status = namespace, owner.SessionID, "same-task", "assistant", "attempted"
		row.Sources, row.Locators = "[]", "{}"
		if err := service.db.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
	}
	inputs, err := service.RecentPromptInputs(ctx, owner, owner.SessionID, 3)
	if err != nil || len(inputs) != 3 {
		t.Fatalf("recent collided calls: %#v %v", inputs, err)
	}
	for i, input := range inputs {
		if input.CallIndex != 3-i || input.ID == "older-high-call-index" {
			t.Fatalf("random IDs or older timestamps changed call order: %#v", inputs)
		}
	}
}

func TestPromptSourceEntryChangesOriginalAndFencesVersion(t *testing.T) {
	ctx := context.Background()
	service, owner := promptTestService(t)
	if err := service.CreateWithSource(ctx, owner, "format", "old table preference", MemoryUser, "preference", 7, []string{"documents"}, WriteSource{Type: "user_explicit"}); err != nil {
		t.Fatal(err)
	}
	entry, _ := service.Lookup(ctx, owner, "format", MemoryUser)
	locator := PromptLocator{Kind: "entry", ID: entry.ID, Version: entry.Version, Digest: promptContentDigest(entry.Value)}
	current, err := service.ReadPromptSource(ctx, owner, locator)
	if err != nil || !current.Editable || current.Digest != locator.Digest {
		t.Fatalf("original entry: %#v %v", current, err)
	}
	saved, err := service.SavePromptInput(ctx, owner, PromptInput{SessionID: owner.SessionID, Agent: "assistant", Status: "attempted", CallIndex: 1, Sources: []PromptSource{{Kind: "entry", Excerpt: entry.Value, Locator: locator}}})
	if err != nil {
		t.Fatal(err)
	}
	updated, err := service.ChangePromptSource(ctx, owner, locator, current.Revision, "update", "new compact table preference")
	if err != nil || updated.Content != "new compact table preference" || updated.Revision == current.Revision {
		t.Fatalf("change original: %#v %v", updated, err)
	}
	entry, _ = service.Lookup(ctx, owner, "format", MemoryUser)
	if entry.Version != 2 || entry.Value != updated.Content || entry.Tags != "[\"documents\"]" {
		t.Fatalf("original entry not updated: %#v", entry)
	}
	if _, err := service.ChangePromptSource(ctx, owner, locator, current.Revision, "delete", ""); !errors.Is(err, ErrPromptSourceConflict) {
		t.Fatalf("stale deletion: %v", err)
	}
	foreign := owner
	foreign.SubjectID = "bob"
	other, err := service.ReadPromptSource(ctx, foreign, locator)
	if err != nil || !other.Missing || other.Content != "" {
		t.Fatalf("foreign original leaked: %#v %v", other, err)
	}
	deleted, err := service.ChangePromptSource(ctx, owner, locator, updated.Revision, "delete", "")
	if err != nil || !deleted.Missing {
		t.Fatalf("original forget: %#v %v", deleted, err)
	}
	result, err := service.Recall(ctx, owner, "new compact table preference", 2000, nil)
	if err != nil || strings.Contains(result.Prompt, "new compact table preference") {
		t.Fatalf("forgotten source still recalled: %#v %v", result, err)
	}
	receipt, err := service.GetPromptInput(ctx, owner, saved.ID)
	if err != nil || receipt.Sources[0].Excerpt != "old table preference" {
		t.Fatalf("historical request receipt was rewritten: %#v %v", receipt, err)
	}
}

func TestPromptSourceContextUsesSharedTupleCASAndRollsBackFailedSave(t *testing.T) {
	ctx := context.Background()
	service, owner := promptTestService(t)
	scm := NewSessionContextManager(filepath.Join(service.workspace, ".openkitty", "context"))
	scm.SetWorkspace(service.workspace)
	if err := service.SetContextStore(scm); err != nil {
		t.Fatal(err)
	}
	for _, session := range []string{owner.SessionID, "foreign-session"} {
		if err := scm.Set("same-key", session+" original", "fact", 5, session); err != nil {
			t.Fatal(err)
		}
	}
	if err := scm.SetDurable("same-key", "durable original", "preference", 7); err != nil {
		t.Fatal(err)
	}
	locator := PromptLocator{Kind: "context", Key: "same-key", SessionID: owner.SessionID, Field: "session"}
	current, err := service.ReadPromptSource(ctx, owner, locator)
	if err != nil {
		t.Fatal(err)
	}
	changed, err := service.ChangePromptSource(ctx, owner, locator, current.Revision, "update", "changed working note")
	if err != nil {
		t.Fatal(err)
	}
	if shared := NewSessionContextManager(scm.StoreDir()); shared != scm || shared.GetSession("same-key", owner.SessionID) != changed.Content {
		t.Fatal("runtime manager did not see original-context edit")
	}
	if scm.GetSession("same-key", "foreign-session") != "foreign-session original" || scm.GetDurable("same-key") != "durable original" {
		t.Fatal("exact source change touched another context tuple")
	}
	if _, err := service.ChangePromptSource(ctx, owner, locator, current.Revision, "delete", ""); !errors.Is(err, ErrPromptSourceConflict) {
		t.Fatalf("stale context deletion: %v", err)
	}
	// Metadata-only updates must invalidate a review token too (same-value ABA).
	if err := scm.Set("same-key", changed.Content, "decision", 8, owner.SessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ChangePromptSource(ctx, owner, locator, changed.Revision, "delete", ""); !errors.Is(err, ErrPromptSourceConflict) {
		t.Fatalf("same-value context revision not fenced: %v", err)
	}
	current, _ = service.ReadPromptSource(ctx, owner, locator)
	originalDir := scm.storeDir
	blocked := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blocked, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	scm.mu.Lock()
	scm.storeDir = blocked
	scm.mu.Unlock()
	if _, err := service.ChangePromptSource(ctx, owner, locator, current.Revision, "update", "must not become runtime content"); err == nil {
		t.Fatal("failed context persistence reported success")
	}
	if scm.GetSession("same-key", owner.SessionID) != current.Content {
		t.Fatal("failed persistence changed the running manager")
	}
	scm.mu.Lock()
	scm.storeDir = originalDir
	scm.mu.Unlock()
	deleted, err := service.ChangePromptSource(ctx, owner, locator, current.Revision, "delete", "")
	if err != nil || !deleted.Missing || scm.GetSession("same-key", owner.SessionID) != "" || scm.GetDurable("same-key") != "durable original" {
		t.Fatalf("exact context delete: %#v %v", deleted, err)
	}
	locator.SessionID = "foreign-session"
	if _, err := service.ReadPromptSource(ctx, owner, locator); !errors.Is(err, ErrPromptSourceUnsupported) {
		t.Fatalf("foreign session context read: %v", err)
	}
}

func TestPromptSourceSummaryCASStopsOldQueuedRecreation(t *testing.T) {
	ctx := context.Background()
	service, owner := promptTestService(t)
	if err := service.SaveSummary(ctx, owner, "old factual summary"); err != nil {
		t.Fatal(err)
	}
	namespace, _ := service.namespace(owner)
	old := ProcessingJob{ID: "old-running-summary", Namespace: namespace, SessionID: owner.SessionID, Kind: "summary", State: "running", LeaseToken: "fixture-token", LeaseUntil: time.Now().Add(time.Hour), SourceRevision: 1, CreatedAt: time.Now().Add(-time.Minute)}
	if err := service.db.Create(&old).Error; err != nil {
		t.Fatal(err)
	}
	locator := PromptLocator{Kind: "summary", SessionID: owner.SessionID}
	current, err := service.ReadPromptSource(ctx, owner, locator)
	if err != nil {
		t.Fatal(err)
	}
	updated, err := service.ChangePromptSource(ctx, owner, locator, current.Revision, "update", "user corrected original summary")
	if err != nil {
		t.Fatal(err)
	}
	workerCtx := context.WithValue(ctx, processingClaimKey{}, old)
	if err := service.SaveSummary(workerCtx, owner, "obsolete worker content"); !errors.Is(err, ErrMemorySuperseded) {
		t.Fatalf("old worker overwrote correction: %v", err)
	}
	if _, err := service.ChangePromptSource(ctx, owner, locator, current.Revision, "delete", ""); !errors.Is(err, ErrPromptSourceConflict) {
		t.Fatalf("stale summary deletion: %v", err)
	}
	deleted, err := service.ChangePromptSource(ctx, owner, locator, updated.Revision, "delete", "")
	if err != nil || !deleted.Missing {
		t.Fatalf("delete summary: %#v %v", deleted, err)
	}
	if err := service.SaveSummary(workerCtx, owner, "obsolete resurrected summary"); !errors.Is(err, ErrMemorySuperseded) {
		t.Fatalf("old worker recreated deleted summary: %v", err)
	}
	var tombstone Summary
	if err := service.db.Where("namespace = ? AND session_id = ?", namespace, owner.SessionID).First(&tombstone).Error; err != nil || tombstone.Content != "" || tombstone.SourceRevision < 3 {
		t.Fatalf("summary revision barrier missing: %#v %v", tombstone, err)
	}
	content, err := service.LoadSummary(ctx, owner)
	if err != nil || content != "" {
		t.Fatalf("deleted summary loaded for future prompt: %q %v", content, err)
	}
	foreign := owner
	foreign.SessionID = "foreign-session"
	if _, err := service.ReadPromptSource(ctx, foreign, locator); !errors.Is(err, ErrPromptSourceUnsupported) {
		t.Fatalf("foreign summary session: %v", err)
	}
}

func TestPromptSourceSessionAndGlobalEntriesKeepOriginalNamespaces(t *testing.T) {
	ctx := context.Background()
	service, owner := promptTestService(t)
	if err := service.CreateSessionNote(ctx, owner, "plan", "current private session plan", "decision"); err != nil {
		t.Fatal(err)
	}
	entry, err := service.Lookup(ctx, owner, "plan", MemorySession)
	if err != nil {
		t.Fatal(err)
	}
	locator := PromptLocator{Kind: "entry", ID: entry.ID, SessionID: owner.SessionID}
	for _, session := range []string{"", "foreign-session"} {
		foreign := owner
		foreign.SessionID = session
		current, err := service.ReadPromptSource(ctx, foreign, locator)
		if err != nil || !current.Missing || current.Content != "" {
			t.Fatalf("foreign session entry: %#v %v", current, err)
		}
	}
	global := NewGlobalService(service.db, service.workspace)
	if err := global.CreateWithSource(ctx, owner, "shared-plan", "workspace shared plan", MemoryGlobal, "decision", 5, nil, WriteSource{Type: "user_explicit"}); err != nil {
		t.Fatal(err)
	}
	shared, err := global.Lookup(ctx, owner, "shared-plan", MemoryGlobal)
	if err != nil {
		t.Fatal(err)
	}
	sharedLocator := PromptLocator{Kind: "entry", ID: shared.ID, Field: "global"}
	if current, err := service.ReadPromptSource(ctx, owner, sharedLocator); err != nil || !current.Missing {
		t.Fatalf("ordinary namespace read shared entry: %#v %v", current, err)
	}
	current, err := global.ReadPromptSource(ctx, owner, sharedLocator)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := global.ChangePromptSource(ctx, owner, sharedLocator, current.Revision, "update", "Use concise shared tables"); err != nil {
		t.Fatal(err)
	}
	otherAgent := NewService(service.db, service.workspace, "other-agent")
	result, err := otherAgent.Recall(ctx, owner, "Use concise shared tables", 2000, nil)
	if err != nil || !strings.Contains(result.Prompt, "Use concise shared tables") {
		t.Fatalf("shared original correction did not reach another agent recall: %#v %v", result, err)
	}
	foreign := owner
	foreign.PartitionID = "other-owner"
	if current, err := global.ReadPromptSource(ctx, foreign, sharedLocator); err != nil || !current.Missing {
		t.Fatalf("shared source crossed owner: %#v %v", current, err)
	}
}
