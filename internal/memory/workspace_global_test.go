package memory

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkspaceGlobalSharingAndIsolation(t *testing.T) {
	ctx := context.Background()
	db, workspace := serviceTestDB(t, filepath.Join(t.TempDir(), "memory.db")), t.TempDir()
	owner := Owner{TenantID: "tenant-a", SubjectID: "alice", PartitionID: "alice", ImportLegacy: true}
	first, second := NewService(db, workspace, "first"), NewService(db, workspace, "second")
	global := NewGlobalService(db, workspace)
	source := WriteSource{Type: "user_explicit", Ref: "local-memory-manager"}
	if err := global.CreateWithSource(ctx, owner, "shared-style", "concise shared style", MemoryGlobal, "preference", 5, nil, source); err != nil {
		t.Fatal(err)
	}
	for _, agent := range []*Service{first, second} {
		result, err := agent.Recall(ctx, owner, "shared style", 1000, nil)
		if err != nil || !strings.Contains(result.Prompt, "concise shared style") || !strings.Contains(result.Prompt, "long_term:global:preference") {
			t.Fatalf("shared recall: %#v %v", result, err)
		}
	}
	for _, other := range []Owner{
		{TenantID: "tenant-a", SubjectID: "bob", PartitionID: "alice"},
		{TenantID: "tenant-b", SubjectID: "alice", PartitionID: "alice"},
		{TenantID: "tenant-a", SubjectID: "alice", PartitionID: "bob"},
	} {
		result, err := second.Recall(ctx, other, "shared style", 1000, nil)
		if err != nil || result.Prompt != "" {
			t.Fatalf("owner leaked: %#v %v", result, err)
		}
	}
	foreign := NewService(db, t.TempDir(), "first")
	if result, err := foreign.Recall(ctx, owner, "shared style", 1000, nil); err != nil || result.Prompt != "" {
		t.Fatalf("workspace leaked: %#v %v", result, err)
	}
	if err := first.Record(ctx, owner, "private", "private preference", MemoryUser, "preference", "active", 5, nil, 0); err != nil {
		t.Fatal(err)
	}
	if entry, _ := second.Lookup(ctx, owner, "private", MemoryUser); entry != nil {
		t.Fatal("legacy agent namespace became shared")
	}
	namespace, _ := first.namespace(owner)
	if namespace != memoryHash(owner.PartitionID, owner.TenantID, owner.SubjectID, first.workspace, "first") {
		t.Fatal("legacy namespace changed")
	}
	rows, total, err := global.Page(ctx, owner, "", "", "", 0, 20)
	if err != nil || total != 1 || len(rows) != 1 || rows[0].AgentName != "" {
		t.Fatalf("global imported private entries: %#v %d %v", rows, total, err)
	}
}

func TestWorkspaceGlobalExplicitWritesAndToolBoundary(t *testing.T) {
	ctx := context.Background()
	db, workspace := serviceTestDB(t, filepath.Join(t.TempDir(), "memory.db")), t.TempDir()
	owner := Owner{PartitionID: "alice", SessionID: "chat"}
	service, global := NewService(db, workspace, "assistant"), NewGlobalService(db, workspace)
	if err := service.Record(ctx, owner, "bad", "bad", MemoryGlobal, "fact", "active", 5, nil, 0); err == nil {
		t.Fatal("ordinary service wrote shared memory")
	}
	if err := global.Record(ctx, owner, "bad", "bad", MemoryGlobal, "fact", "active", 5, nil, 0); err == nil {
		t.Fatal("automatic writer wrote shared memory")
	}
	for _, target := range []*Service{service, global} {
		tool := NewAgentMemoryServiceTool(target, func(context.Context) (Owner, error) { return owner, nil }, nil)
		if _, err := tool.Execute(WithUserTurn(ctx, "remember bad"), map[string]interface{}{"action": "remember", "key": "bad", "value": "bad", "scope": "global"}); err == nil {
			t.Fatal("model tool acquired shared write permission")
		}
	}
	if err := global.SaveSummary(ctx, owner, "automatic"); err == nil {
		t.Fatal("automatic summary entered shared memory")
	}
	if err := global.Enqueue(ctx, owner, "summary", map[string]string{"text": "automatic"}); err == nil {
		t.Fatal("automatic job entered shared memory")
	}
	if err := global.SetContextStore(NewSessionContextManager(filepath.Join(workspace, ".openkitty", "context"))); err == nil {
		t.Fatal("SCM attached to shared memory")
	}
}

func TestWorkspaceGlobalCorrectionForgetAndSemanticFencing(t *testing.T) {
	ctx := context.Background()
	db, workspace := serviceTestDB(t, filepath.Join(t.TempDir(), "memory.db")), t.TempDir()
	owner := Owner{PartitionID: "alice", SessionID: "chat"}
	var calls []string
	backend := semanticTestBackend(&calls)
	option := WithRecallEmbedding(func() RecallEmbedding { return backend })
	global, service := NewGlobalService(db, workspace, option), NewService(db, workspace, "assistant", option)
	source := WriteSource{Type: "user_explicit"}
	if err := global.CreateWithSource(ctx, owner, "dispatch", "refrigerated lorries", MemoryGlobal, "fact", 5, nil, source); err != nil {
		t.Fatal(err)
	}
	before, _ := global.Lookup(ctx, owner, "dispatch", MemoryGlobal)
	result, err := service.Recall(ctx, owner, "freight", 1000, nil)
	if err != nil || !strings.Contains(result.Prompt, "refrigerated lorries") || result.Items[0].Reason != "semantic" {
		t.Fatalf("global semantic recall: %#v %v", result, err)
	}
	if err := service.SaveRecallTrace(ctx, owner, "turn", "ready", result); err != nil {
		t.Fatal(err)
	}
	traces, _ := service.RecentRecallTraces(ctx, owner, 1)
	if len(traces) != 1 || traces[0].Decisions[0].Source != "long_term:global:fact" {
		t.Fatalf("global trace lost: %#v", traces)
	}
	if err := global.RecordWithSource(ctx, owner, "dispatch", "rail delivery", MemoryGlobal, "fact", "active", 5, nil, 99, source); err == nil {
		t.Fatal("stale correction accepted")
	}
	if err := global.RecordWithSource(ctx, owner, "dispatch", "rail delivery", MemoryGlobal, "fact", "active", 5, nil, 1, source); err != nil {
		t.Fatal(err)
	}
	after, _ := global.Lookup(ctx, owner, "dispatch", MemoryGlobal)
	if after.Version != 2 || !before.CreatedAt.Equal(after.CreatedAt) || after.UpdatedAt.Before(before.UpdatedAt) {
		t.Fatalf("correction metadata: %#v %#v", before, after)
	}
	result, err = service.Recall(ctx, owner, "freight", 1000, []RecallItem{{ID: "old-adapter", Source: "history", Content: "freight used refrigerated lorries"}})
	if err != nil || strings.Contains(result.Prompt, "refrigerated lorries") {
		t.Fatalf("shared retraction failed: %#v %v", result, err)
	}
	if _, err := global.ForgetExpected(ctx, owner, "dispatch", MemoryGlobal, 1); err == nil {
		t.Fatal("stale deletion accepted")
	}
	if _, err := global.ForgetExpected(ctx, owner, "dispatch", MemoryGlobal, 2); err != nil {
		t.Fatal(err)
	}
	if err := global.CreateWithSource(ctx, owner, "dispatch", "resurrected", MemoryGlobal, "fact", 5, nil, source); !errors.Is(err, ErrMemoryKeyExists) {
		t.Fatalf("forgotten shared key recreated: %v", err)
	}
	if err := global.CreateWithSource(ctx, owner, "new-dispatch", "refrigerated replacement", MemoryGlobal, "fact", 5, nil, source); err != nil {
		t.Fatal(err)
	}
	baseEmbed := backend.Embed
	backend.Embed = func(ctx context.Context, text string) ([]float64, error) {
		if strings.Contains(text, "new-dispatch: refrigerated replacement") {
			if _, err := global.ForgetExpected(ctx, owner, "new-dispatch", MemoryGlobal, 1); err != nil {
				t.Fatal(err)
			}
		}
		return baseEmbed(ctx, text)
	}
	result, err = service.Recall(ctx, owner, "freight replacement", 1000, []RecallItem{{ID: "in-flight-old", Source: "history", Content: "freight refrigerated replacement"}})
	if err != nil || result.Prompt != "" {
		t.Fatalf("in-flight shared deletion resurrected: %#v %v", result, err)
	}
	namespace, _ := global.namespace(owner)
	var vectors int64
	if err := db.Model(&recallVector{}).Where("namespace = ?", namespace).Count(&vectors).Error; err != nil || vectors != 0 {
		t.Fatalf("stale global vector persisted: %d %v", vectors, err)
	}
}

func TestWorkspaceGlobalPrecedencePinningAndSingleBudget(t *testing.T) {
	ctx := context.Background()
	db, workspace := serviceTestDB(t, filepath.Join(t.TempDir(), "memory.db")), t.TempDir()
	owner := Owner{PartitionID: "alice", SessionID: "chat"}
	service, global := NewService(db, workspace, "assistant"), NewGlobalService(db, workspace)
	source := WriteSource{Type: "user_explicit"}
	if err := global.CreateWithSource(ctx, owner, "format", "global format", MemoryGlobal, "preference", 5, nil, source); err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct {
		scope MemoryScope
		value string
	}{{MemoryUser, "user format"}, {MemoryProject, "project format"}, {MemorySession, "session format"}} {
		if err := service.Record(ctx, owner, "format", item.value, item.scope, "fact", "active", 5, nil, 0); err != nil {
			t.Fatal(err)
		}
	}
	result, err := service.Recall(ctx, owner, "format", 500, nil)
	if err != nil || len(result.Items) != 1 || !strings.Contains(result.Prompt, "session format") || result.Tokens > 500 {
		t.Fatalf("merged precedence/budget: %#v %v", result, err)
	}
	for index := 0; index < 6; index++ {
		key := fmt.Sprintf("pin-%d", index)
		if err := global.CreateWithSource(ctx, owner, key, "compact preferred tone", MemoryGlobal, "preference", 5, nil, source); err != nil {
			t.Fatal(err)
		}
		err := global.SetPinned(ctx, owner, key, MemoryGlobal, 1, true)
		if (index < 5) != (err == nil) {
			t.Fatalf("shared pinned cap at %d: %v", index, err)
		}
	}
	otherAgent := NewService(db, workspace, "other")
	result, err = otherAgent.Recall(ctx, owner, "unrelated weather", 1000, nil)
	if err != nil || len(result.Items) == 0 || result.Tokens > 1000 {
		t.Fatalf("shared pinned recall: %#v %v", result, err)
	}
	for _, item := range result.Items {
		if item.Reason != "pinned" || item.Source != "long_term:global:preference" {
			t.Fatalf("unpinned/unrelated selected: %#v", item)
		}
	}
	var output bytes.Buffer
	if err := global.Export(ctx, owner, &output); err != nil || strings.Contains(output.String(), "session format") || !strings.Contains(output.String(), `"scope":"global"`) {
		t.Fatalf("shared export: %s %v", output.String(), err)
	}
}

func TestMemoryManagementKindAndSessionNotes(t *testing.T) {
	ctx := context.Background()
	service := NewService(serviceTestDB(t, filepath.Join(t.TempDir(), "memory.db")), t.TempDir(), "assistant")
	owner := Owner{PartitionID: "alice", SessionID: "chat-a"}
	for index := 0; index < 25; index++ {
		kind := "fact"
		if index == 0 {
			kind = "preference"
		}
		if err := service.Record(ctx, owner, fmt.Sprintf("key-%02d", index), "stored value", MemoryUser, kind, "active", 5, nil, 0); err != nil {
			t.Fatal(err)
		}
	}
	rows, total, err := service.PageWithFilters(ctx, owner, MemoryUser, "preference", "", "", 0, 1)
	if err != nil || total != 1 || len(rows) != 1 || rows[0].Kind != "preference" {
		t.Fatalf("kind filter after page: %#v %d %v", rows, total, err)
	}
	if _, _, err := service.PageWithFilters(ctx, owner, "", "unknown", "", "", 0, 20); err == nil {
		t.Fatal("invalid kind accepted")
	}
	if err := service.CreateSessionNote(ctx, owner, "note", "first conversation note", "fact"); err != nil {
		t.Fatal(err)
	}
	if err := service.CreateSessionNote(ctx, owner, "note", "overwrite", "fact"); !errors.Is(err, ErrMemoryKeyExists) {
		t.Fatalf("note overwritten: %v", err)
	}
	other := owner
	other.SessionID = "chat-b"
	if err := service.CreateSessionNote(ctx, other, "note", "other conversation note", "fact"); err != nil {
		t.Fatal(err)
	}
	rows, total, err = service.PageWithFilters(ctx, owner, MemorySession, "", "", "", 0, 20)
	if err != nil || total != 1 || rows[0].Value != "first conversation note" {
		t.Fatalf("session page leaked: %#v %d %v", rows, total, err)
	}
	result, err := service.Recall(ctx, owner, "unrelated question", 1000, nil)
	if err != nil || !strings.Contains(result.Prompt, "first conversation note") || strings.Contains(result.Prompt, "other conversation note") {
		t.Fatalf("session recall: %#v %v", result, err)
	}
	owner.SessionID = ""
	if err := service.CreateSessionNote(ctx, owner, "bad", "missing conversation", "fact"); err == nil {
		t.Fatal("unscoped session note accepted")
	}
}
