package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestServiceSelectiveRecallAndPinnedLifecycle(t *testing.T) {
	ctx := context.Background()
	s := NewService(serviceTestDB(t, filepath.Join(t.TempDir(), "memory.db")), t.TempDir(), "assistant")
	owner := Owner{TenantID: "a", SubjectID: "alice", PartitionID: "local", SessionID: "one"}
	for _, scope := range []MemoryScope{MemoryUser, MemoryProject} {
		if err := s.Record(ctx, owner, string(scope), "typescript configuration", scope, "preference", "active", 10, nil, 0); err != nil {
			t.Fatal(err)
		}
	}
	for _, query := range []string{"", "nebula astronomy"} {
		result, err := s.Recall(ctx, owner, query, 2000, []RecallItem{{ID: "old", Source: "history", Content: "typescript configuration"}})
		if err != nil || result.Prompt != "" || result.Tokens != 0 || len(result.Decisions) != 3 {
			t.Fatalf("unrelated recall: %+v %v", result, err)
		}
		for _, decision := range result.Decisions {
			if decision.Selected || decision.Reason != "not_relevant" {
				t.Fatalf("unexpected decision: %+v", decision)
			}
		}
	}
	if err := s.SetPinned(ctx, owner, "user", MemoryUser, 1, true); err != nil {
		t.Fatal(err)
	}
	if err := s.SetPinned(ctx, owner, "user", MemoryUser, 1, false); err == nil {
		t.Fatal("stale unpin accepted")
	}
	foreign := owner
	foreign.SubjectID = "bob"
	if err := s.SetPinned(ctx, foreign, "user", MemoryUser, 2, false); err == nil {
		t.Fatal("foreign pin changed")
	}
	result, err := s.Recall(ctx, owner, "nebula astronomy", 2000, nil)
	if err != nil || len(result.Items) != 1 || result.Items[0].Reason != "pinned" || result.Items[0].Version != 2 {
		t.Fatalf("pinned recall: %+v %v", result, err)
	}
	// Pinned entries are fetched even if the recent/high-priority pool is full.
	for i := 0; i < 205; i++ {
		if err := s.Record(ctx, owner, fmt.Sprintf("noise_%03d", i), "unrelated filler", MemoryUser, "fact", "active", 10, nil, 0); err != nil {
			t.Fatal(err)
		}
	}
	result, err = s.Recall(ctx, owner, "nebula astronomy", 2000, nil)
	if err != nil || len(result.Items) != 1 || result.Items[0].Reason != "pinned" {
		t.Fatalf("pin displaced: %+v %v", result, err)
	}
	if err := s.Record(ctx, owner, "user", "new preference", MemoryUser, "preference", "active", 10, nil, 2); err != nil {
		t.Fatal(err)
	}
	current, _ := s.Lookup(ctx, owner, "user", MemoryUser)
	if current.Pinned {
		t.Fatal("content correction retained pin")
	}
	result, err = s.Recall(ctx, owner, "nebula astronomy", 2000, nil)
	if err != nil || result.Prompt != "" {
		t.Fatalf("corrected pin still recalled: %+v %v", result, err)
	}
	if err := s.SetPinned(ctx, owner, "project", MemoryProject, 1, true); err == nil {
		t.Fatal("project pin accepted")
	}
	for i := 0; i < MaxPinnedPreferences+1; i++ {
		key := fmt.Sprintf("pin_%d", i)
		if err := s.Record(ctx, owner, key, "concise", MemoryUser, "preference", "active", 1, nil, 0); err != nil {
			t.Fatal(err)
		}
		err := s.SetPinned(ctx, owner, key, MemoryUser, 1, true)
		if (i < MaxPinnedPreferences && err != nil) || (i == MaxPinnedPreferences && err == nil) {
			t.Fatalf("pin cap at %d: %v", i, err)
		}
	}
	for _, query := range []string{"", "nebula astronomy"} {
		result, err := s.Recall(ctx, owner, query, 400, nil)
		if err != nil || len(result.Items) == 0 || len(result.Items) >= MaxPinnedPreferences || result.Tokens > 400 {
			t.Fatalf("pinned budget recall: %+v %v", result, err)
		}
		pinnedTokens, excluded := 0, 0
		for _, decision := range result.Decisions {
			if decision.Selected {
				if decision.Reason != "pinned" {
					t.Fatalf("unexpected unqualified selection: %+v", decision)
				}
				pinnedTokens += decision.Tokens
			}
			if decision.Reason == "pinned_budget" {
				excluded++
			}
		}
		if pinnedTokens > 400/3 || excluded == 0 {
			t.Fatalf("pinned preferences exceeded budget: %d tokens, %d exclusions", pinnedTokens, excluded)
		}
	}
	if _, err := s.ForgetExpected(ctx, owner, "pin_0", MemoryUser, 2); err != nil {
		t.Fatal(err)
	}
	if err := s.SetPinned(ctx, owner, "pin_0", MemoryUser, 3, true); err == nil {
		t.Fatal("forgotten pin accepted")
	}
	if err := s.SetPinned(ctx, owner, "pin_5", MemoryUser, 1, true); err != nil {
		t.Fatal(err)
	}
}

func TestServiceRecallDecisionsScopeBudgetAndPrivacy(t *testing.T) {
	ctx := context.Background()
	s := NewService(serviceTestDB(t, filepath.Join(t.TempDir(), "memory.db")), t.TempDir(), "assistant")
	owner := Owner{TenantID: "a", SubjectID: "alice", PartitionID: "local", SessionID: "private-session"}
	for _, item := range []struct {
		key, value string
		scope      MemoryScope
	}{
		{"theme", "orchid old", MemoryUser}, {"theme", "temporary override", MemorySession}, {"large", strings.Repeat("orchid ", 170), MemoryProject},
	} {
		if err := s.Record(ctx, owner, item.key, item.value, item.scope, "fact", "active", 5, nil, 0); err != nil {
			t.Fatal(err)
		}
	}
	result, err := s.Recall(ctx, owner, "orchid", 150, []RecallItem{{ID: "private-source", Source: "history", Content: "orchid brief"}, {ID: "duplicate", Source: "history", Content: "orchid brief"}})
	if err != nil {
		t.Fatal(err)
	}
	reasons := map[string]int{}
	for _, decision := range result.Decisions {
		reasons[decision.Reason]++
	}
	if reasons["scope_override"] != 1 || reasons["budget"] == 0 || reasons["duplicate"] != 1 || strings.Contains(result.Prompt, "orchid old") || result.Tokens > 150 {
		t.Fatalf("decisions: %+v / %+v", reasons, result)
	}
	if err := s.SaveRecallTrace(ctx, owner, "private-task", "ready", result); err != nil {
		t.Fatal(err)
	}
	reopened := NewService(s.db, s.workspace, "assistant")
	traces, err := reopened.RecentRecallTraces(ctx, owner, 20)
	if err != nil || len(traces) != 1 || len(traces[0].Decisions) != len(result.Decisions) {
		t.Fatalf("trace reopen: %+v %v", traces, err)
	}
	data, _ := json.Marshal(traces)
	for _, secret := range []string{"orchid", "private-session", "private-task", "private-source", "temporary override"} {
		if strings.Contains(string(data), secret) {
			t.Fatalf("trace retained %q", secret)
		}
	}
	for _, other := range []Owner{
		{TenantID: "a", SubjectID: "bob", PartitionID: "local", SessionID: owner.SessionID},
		{TenantID: "other", SubjectID: "alice", PartitionID: "local", SessionID: owner.SessionID},
		{TenantID: "a", SubjectID: "alice", PartitionID: "other", SessionID: owner.SessionID},
		{TenantID: "a", SubjectID: "alice", PartitionID: "local", SessionID: "other"},
	} {
		traces, err := s.RecentRecallTraces(ctx, other, 20)
		if err != nil || len(traces) != 0 {
			t.Fatalf("foreign trace leaked: %+v %v", traces, err)
		}
	}
	for _, other := range []*Service{NewService(s.db, t.TempDir(), "assistant"), NewService(s.db, s.workspace, "other")} {
		traces, err := other.RecentRecallTraces(ctx, owner, 20)
		if err != nil || len(traces) != 0 {
			t.Fatalf("namespace trace leaked: %+v %v", traces, err)
		}
	}
	if err := s.SaveRecallTrace(ctx, owner, "private-task", "blocked", result); err != nil {
		t.Fatal(err)
	}
	traces, err = s.RecentRecallTraces(ctx, owner, 1)
	if err != nil || len(traces) != 1 || traces[0].Status != "blocked" || traces[0].Tokens != 0 {
		t.Fatalf("blocked trace: %+v %v", traces, err)
	}
	for _, decision := range traces[0].Decisions {
		if decision.Selected {
			t.Fatal("blocked memory marked included")
		}
	}
}

func TestServiceRecallTraceRetentionAndBound(t *testing.T) {
	ctx := context.Background()
	s := NewService(serviceTestDB(t, filepath.Join(t.TempDir(), "memory.db")), t.TempDir(), "assistant")
	owner := Owner{PartitionID: "local", SessionID: "one"}
	namespace, _ := s.namespace(owner)
	for i := 0; i < 103; i++ {
		record := recallTraceRecord{ID: fmt.Sprintf("old%03d", i), Namespace: namespace, Status: "empty", Decisions: "[]", CreatedAt: time.Now().Add(-time.Duration(i+1) * time.Minute)}
		if i == 0 {
			record.CreatedAt = time.Now().Add(-8 * 24 * time.Hour)
		}
		if err := s.db.Create(&record).Error; err != nil {
			t.Fatal(err)
		}
	}
	result := RecallResult{}
	for i := 0; i < 600; i++ {
		result.decide(RecallItem{ID: fmt.Sprint(i), Source: "untrusted-source"}, "not_relevant", false, 0)
	}
	result.decide(RecallItem{ID: "selected", Source: "history"}, "relevant", true, 10)
	if len(result.Decisions) != 512 || result.OmittedDecisions != 89 {
		t.Fatalf("unbounded trace: %d %d", len(result.Decisions), result.OmittedDecisions)
	}
	if err := s.SaveRecallTrace(ctx, owner, "", "ready", result); err != nil {
		t.Fatal(err)
	}
	var count int64
	s.db.Model(&recallTraceRecord{}).Where("namespace = ?", namespace).Count(&count)
	if count != 100 {
		t.Fatalf("retained %d traces", count)
	}
	traces, err := s.RecentRecallTraces(ctx, owner, 1)
	if err != nil || len(traces) != 1 {
		t.Fatalf("trace: %+v %v", traces, err)
	}
	selected := 0
	for _, decision := range traces[0].Decisions {
		if decision.Selected {
			selected++
		}
		if decision.Source == "untrusted-source" {
			t.Fatal("untrusted metadata persisted")
		}
	}
	if selected != 1 {
		t.Fatal("selected decision crowded out")
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err := s.SaveRecallTrace(cancelled, owner, "", "empty", RecallResult{}); err == nil {
		t.Fatal("cancelled trace write succeeded")
	}
}
