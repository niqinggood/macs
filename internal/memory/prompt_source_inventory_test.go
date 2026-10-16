package memory

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"gorm.io/gorm"
)

func TestPromptSourceInventoryExistingOriginalsKeepNamespacesAndSessionBoundaries(t *testing.T) {
	ctx := context.Background()
	service, owner := promptTestService(t)
	for _, seed := range []struct {
		key, value, kind, state string
		scope                   MemoryScope
	}{
		{"format", "ordinary formatting preference", "preference", "active", MemoryUser},
		{"project", "project decision", "decision", "active", MemoryProject},
		{"candidate", "INACTIVE_CANDIDATE", "fact", "candidate", MemoryProject},
		{"forgotten", "DELETED_VALUE", "fact", "active", MemoryProject},
	} {
		if err := service.Record(ctx, owner, seed.key, seed.value, seed.scope, seed.kind, seed.state, 5, nil, 0); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := service.ForgetExpected(ctx, owner, "forgotten", MemoryProject, 1); err != nil {
		t.Fatal(err)
	}
	pinned, _ := service.Lookup(ctx, owner, "format", MemoryUser)
	if err := service.db.Model(&Entry{}).Where("id = ?", pinned.ID).Update("pinned", true).Error; err != nil {
		t.Fatal(err)
	}
	if err := service.CreateSessionNote(ctx, owner, "current", "current manual working note", "decision"); err != nil {
		t.Fatal(err)
	}
	otherSession := owner
	otherSession.SessionID = "chat-two"
	if err := service.CreateSessionNote(ctx, otherSession, "other", "OTHER_SESSION_ENTRY", "decision"); err != nil {
		t.Fatal(err)
	}
	foreign := owner
	foreign.SubjectID = "bob"
	if err := service.Record(ctx, foreign, "private", "FOREIGN_OWNER_ENTRY", MemoryUser, "fact", "active", 5, nil, 0); err != nil {
		t.Fatal(err)
	}
	if err := NewService(service.db, t.TempDir(), "assistant").Record(ctx, owner, "private", "FOREIGN_WORKSPACE_ENTRY", MemoryProject, "fact", "active", 5, nil, 0); err != nil {
		t.Fatal(err)
	}
	if err := NewService(service.db, service.workspace, "other-agent").Record(ctx, owner, "private", "FOREIGN_AGENT_ENTRY", MemoryProject, "fact", "active", 5, nil, 0); err != nil {
		t.Fatal(err)
	}
	global := NewGlobalService(service.db, service.workspace)
	if err := global.CreateWithSource(ctx, owner, "shared", "workspace shared instruction", MemoryGlobal, "fact", 5, nil, WriteSource{Type: "user_explicit"}); err != nil {
		t.Fatal(err)
	}
	if err := global.CreateWithSource(ctx, foreign, "private", "FOREIGN_GLOBAL_ENTRY", MemoryGlobal, "fact", 5, nil, WriteSource{Type: "user_explicit"}); err != nil {
		t.Fatal(err)
	}
	for _, summaryOwner := range []Owner{owner, otherSession, foreign} {
		if err := service.SaveSummary(ctx, summaryOwner, "summary for "+summaryOwner.SubjectID+" "+summaryOwner.SessionID); err != nil {
			t.Fatal(err)
		}
	}
	unapproved := owner
	unapproved.SessionID = "not-authorized"
	if err := service.SaveSummary(ctx, unapproved, "UNAUTHORIZED_SUMMARY"); err != nil {
		t.Fatal(err)
	}
	scm := NewSessionContextManager(filepath.Join(service.workspace, ".openkitty", "context"))
	scm.SetWorkspace(service.workspace)
	if err := service.SetContextStore(scm); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 12; i++ {
		if err := scm.Set("working-"+string(rune('a'+i)), "current stored context", "fact", 5, owner.SessionID); err != nil {
			t.Fatal(err)
		}
	}
	if err := scm.Set("foreign", "FOREIGN_CONTEXT", "fact", 5, "other-session"); err != nil {
		t.Fatal(err)
	}
	if err := scm.SetDurable("legacy", "UNSCOPED_LEGACY_PREFERENCE", "preference", 9); err != nil {
		t.Fatal(err)
	}
	items, err := service.PromptSourceInventory(ctx, owner, []string{owner.SessionID, otherSession.SessionID}, 100)
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	for _, candidate := range items {
		item := candidate.Item
		counts[item.Locator.Kind]++
		if strings.Contains(item.Content, "FOREIGN_") || strings.Contains(item.Content, "OTHER_SESSION_ENTRY") || strings.Contains(item.Content, "UNSCOPED_") || strings.Contains(item.Content, "INACTIVE_") || strings.Contains(item.Content, "DELETED_") || strings.Contains(item.Content, "UNAUTHORIZED_") {
			t.Fatalf("out-of-scope or inactive source entered inventory: %#v", candidate)
		}
		if item.Locator.Digest == "" || candidate.CodePath == "" || candidate.Mode != "direct" && candidate.Mode != "relevant" {
			t.Fatalf("original locator or eligibility missing: %#v", candidate)
		}
		switch item.Locator.Kind {
		case "context":
			if candidate.Mode != "direct" || candidate.CreatedAt == nil || item.Locator.SessionID != owner.SessionID || item.Locator.Digest != promptContentDigest("current stored context") {
				t.Fatal("current context lost original tuple/time/digest")
			}
		case "entry":
			if candidate.CreatedAt == nil || item.Locator.ID == "" || item.Locator.Version < 1 {
				t.Fatal("entry lost original ID/version/time")
			}
			if item.Locator.ID == pinned.ID && (!candidate.Pinned || candidate.Mode != "direct") {
				t.Fatal("pinned preference was mislabeled as a relevance-only candidate")
			}
			if item.Locator.Field == "global" && item.Locator.SessionID != "" {
				t.Fatal("global visibility was incorrectly bound to its creation session")
			}
		case "summary":
			if candidate.Mode != "relevant" || candidate.CreatedAt != nil || item.Locator.ID == "" || item.Locator.Revision < 1 || item.Locator.SessionID != owner.SessionID && item.Locator.SessionID != otherSession.SessionID {
				t.Fatal("summary lost its actual source revision or authorized session")
			}
		}
	}
	if counts["entry"] != 4 || counts["context"] != ContextPromptDefaultLimit || counts["summary"] != 2 {
		t.Fatalf("existing source inventory coverage: %#v", counts)
	}
}

func TestPromptSourceInventoryDoesNotImportEmbedWriteOrConstructContext(t *testing.T) {
	ctx := context.Background()
	base, owner := promptTestService(t)
	var embeddings atomic.Int32
	service := NewService(base.db, base.workspace, "assistant", WithRecallEmbedding(func() RecallEmbedding {
		embeddings.Add(1)
		return RecallEmbedding{ModelID: "fake", Embed: func(context.Context, string) ([]float64, error) {
			embeddings.Add(1)
			return []float64{1}, nil
		}}
	}))
	if err := service.Record(ctx, owner, "stored", "existing original", MemoryProject, "fact", "active", 5, nil, 0); err != nil {
		t.Fatal(err)
	}
	if err := service.legacy.Remember("legacy", "MUST_NOT_IMPORT", MemoryUser, 5, nil, ""); err != nil {
		t.Fatal(err)
	}
	legacyFile := service.legacy.filePath()
	before, err := os.ReadFile(legacyFile)
	if err != nil {
		t.Fatal(err)
	}
	var writes atomic.Int32
	countWrite := func(*gorm.DB) { writes.Add(1) }
	service.db.Callback().Create().Before("gorm:create").Register("inventory-count-create", countWrite)
	service.db.Callback().Update().Before("gorm:update").Register("inventory-count-update", countWrite)
	service.db.Callback().Delete().Before("gorm:delete").Register("inventory-count-delete", countWrite)
	owner.ImportLegacy = true // Inventory must not inherit the runtime import switch.
	items, err := service.PromptSourceInventory(ctx, owner, []string{owner.SessionID}, 20)
	if err != nil || len(items) != 1 || !strings.Contains(items[0].Item.Content, "existing original") {
		t.Fatalf("read-only inventory: %#v %v", items, err)
	}
	if writes.Load() != 0 || embeddings.Load() != 0 {
		t.Fatalf("inventory performed writes/embedding: %d/%d", writes.Load(), embeddings.Load())
	}
	after, err := os.ReadFile(legacyFile)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("inventory changed the legacy file")
	}
	if _, err := os.Stat(filepath.Join(service.workspace, ".openkitty", "context")); !os.IsNotExist(err) {
		t.Fatal("inventory constructed a new context directory")
	}
	for _, model := range []interface{}{&promptInputRecord{}, &recallTraceRecord{}, &memoryMigration{}, &recallVector{}} {
		var count int64
		if err := service.db.Model(model).Count(&count).Error; err != nil || count != 0 {
			t.Fatalf("inventory created a receipt/trace/import/vector: %d %v", count, err)
		}
	}
}

func TestPromptSourceInventoryBoundsAndUnapprovedCurrentSession(t *testing.T) {
	ctx := context.Background()
	service, owner := promptTestService(t)
	if err := service.CreateSessionNote(ctx, owner, "private", "CURRENT_SESSION_NOTE", "fact"); err != nil {
		t.Fatal(err)
	}
	if err := service.Record(ctx, owner, "durable", "existing durable input", MemoryProject, "fact", "active", 5, nil, 0); err != nil {
		t.Fatal(err)
	}
	items, err := service.PromptSourceInventory(ctx, owner, nil, 1)
	if err != nil || len(items) != 1 || strings.Contains(items[0].Item.Content, "CURRENT_SESSION_NOTE") || items[0].Item.Locator.SessionID != "" {
		t.Fatalf("unapproved session changed source visibility: %#v %v", items, err)
	}
	for _, sessions := range [][]string{make([]string, 201), {" padded "}, {""}} {
		if _, err := service.PromptSourceInventory(ctx, owner, sessions, 1); err == nil {
			t.Fatal("invalid/unbounded authorized sessions accepted")
		}
	}
	if _, err := service.PromptSourceInventory(ctx, Owner{}, nil, 1); err == nil {
		t.Fatal("inventory accepted an unavailable trusted owner")
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := service.PromptSourceInventory(cancelled, owner, nil, 1); err != context.Canceled {
		t.Fatalf("cancelled inventory: %v", err)
	}
}

func TestPromptSourceInventoryExcludesRetractedOriginalContentInAllSources(t *testing.T) {
	ctx := context.Background()
	service, owner := promptTestService(t)
	old := "obsolete source content"
	if err := service.Record(ctx, owner, "original", old, MemoryProject, "fact", "active", 5, nil, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ForgetExpected(ctx, owner, "original", MemoryProject, 1); err != nil {
		t.Fatal(err)
	}
	// An active copy can remain in another source. It must not be advertised
	// as eligible when the actual runtime would reject its retracted value.
	if err := service.Record(ctx, owner, "duplicate", old, MemoryUser, "preference", "active", 5, nil, 0); err != nil {
		t.Fatal(err)
	}
	if err := service.SaveSummary(ctx, owner, "A summary repeating "+old); err != nil {
		t.Fatal(err)
	}
	scm := NewSessionContextManager(filepath.Join(service.workspace, ".openkitty", "context"))
	scm.SetWorkspace(service.workspace)
	if err := service.SetContextStore(scm); err != nil {
		t.Fatal(err)
	}
	if err := scm.Set("duplicate", old, "fact", 5, owner.SessionID); err != nil {
		t.Fatal(err)
	}
	global := NewGlobalService(service.db, service.workspace)
	if err := global.CreateWithSource(ctx, owner, "cross-source-copy", old, MemoryGlobal, "fact", 5, nil, WriteSource{Type: "user_explicit"}); err != nil {
		t.Fatal(err)
	}
	sharedOld := "old shared source"
	if err := global.CreateWithSource(ctx, owner, "shared-original", sharedOld, MemoryGlobal, "fact", 5, nil, WriteSource{Type: "user_explicit"}); err != nil {
		t.Fatal(err)
	}
	if _, err := global.ForgetExpected(ctx, owner, "shared-original", MemoryGlobal, 1); err != nil {
		t.Fatal(err)
	}
	if err := service.Record(ctx, owner, "shared-duplicate", sharedOld, MemoryProject, "fact", "active", 5, nil, 0); err != nil {
		t.Fatal(err)
	}
	items, err := service.PromptSourceInventory(ctx, owner, []string{owner.SessionID}, 100)
	if err != nil || len(items) != 0 {
		t.Fatalf("retracted ordinary/shared content remained eligible: %#v %v", items, err)
	}
}
