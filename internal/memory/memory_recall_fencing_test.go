package memory

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// The first query embedding blocks only on channels. The test mutates a
// temporary database after Recall captured its lexical pool, before selection.
type recallFenceGate struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
	resume  sync.Once
}

func newRecallFenceGate(t *testing.T) *recallFenceGate {
	t.Helper()
	gate := &recallFenceGate{started: make(chan struct{}), release: make(chan struct{})}
	t.Cleanup(gate.unblock)
	return gate
}

func (g *recallFenceGate) unblock() { g.resume.Do(func() { close(g.release) }) }

func (g *recallFenceGate) backend() RecallEmbedding {
	return RecallEmbedding{ModelID: "recall-fence-fake", Embed: func(ctx context.Context, text string) ([]float64, error) {
		first := false
		g.once.Do(func() { first = true; close(g.started) })
		if first {
			select {
			case <-g.release:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return []float64{1, 0}, nil
	}}
}

type recallFenceOutcome struct {
	result RecallResult
	err    error
}

func startFencedRecall(t *testing.T, ctx context.Context, service *Service, owner Owner, gate *recallFenceGate) <-chan recallFenceOutcome {
	t.Helper()
	done := make(chan recallFenceOutcome, 1)
	go func() {
		result, err := service.Recall(ctx, owner, "quartz requested", 2000, nil)
		done <- recallFenceOutcome{result, err}
	}()
	select {
	case <-gate.started:
	case outcome := <-done:
		t.Fatalf("recall finished before query embedding: %v", outcome.err)
	case <-time.After(5 * time.Second):
		t.Fatal("query embedding did not start")
	}
	return done
}

func finishFencedRecall(t *testing.T, done <-chan recallFenceOutcome, gate *recallFenceGate) recallFenceOutcome {
	t.Helper()
	gate.unblock()
	select {
	case outcome := <-done:
		return outcome
	case <-time.After(5 * time.Second):
		t.Fatal("recall did not finish")
		return recallFenceOutcome{}
	}
}

func fenceDecision(result RecallResult, id, reason string) bool {
	for _, decision := range result.Decisions {
		if decision.ID == id && decision.Reason == reason && !decision.Selected {
			return true
		}
	}
	return false
}

func TestRecallFencingConcurrentEntryChanges(t *testing.T) {
	ctx := context.Background()
	owner := Owner{TenantID: "tenant-a", SubjectID: "user-a", PartitionID: "web-user-a", SessionID: "session-a"}
	for _, tc := range []struct {
		name   string
		scope  MemoryScope
		reason string
		change func(*testing.T, *Service, Entry)
	}{
		{"user_correct", MemoryUser, "version_changed", fenceCorrect},
		{"project_correct", MemoryProject, "version_changed", fenceCorrect},
		{"session_correct", MemorySession, "version_changed", fenceCorrect},
		{"global_correct", MemoryGlobal, "version_changed", fenceCorrect},
		{"project_forget", MemoryProject, "stale", fenceForget},
		{"global_forget", MemoryGlobal, "stale", fenceForget},
		{"physical_delete", MemoryProject, "stale", func(t *testing.T, s *Service, e Entry) {
			if err := s.db.Where("namespace = ? AND id = ?", e.Namespace, e.ID).Delete(&Entry{}).Error; err != nil {
				t.Fatal(err)
			}
		}},
		{"candidate_state", MemoryProject, "stale", func(t *testing.T, s *Service, e Entry) {
			fenceUpdate(t, s, e, map[string]interface{}{"state": "candidate", "version": e.Version + 1})
		}},
		{"version_only", MemoryProject, "version_changed", func(t *testing.T, s *Service, e Entry) {
			fenceUpdate(t, s, e, map[string]interface{}{"version": e.Version + 1})
		}},
		{"foreign_session", MemorySession, "stale", func(t *testing.T, s *Service, e Entry) {
			fenceUpdate(t, s, e, map[string]interface{}{"session_id": "session-b"})
		}},
		{"foreign_tenant", MemoryProject, "stale", func(t *testing.T, s *Service, e Entry) {
			foreign := owner
			foreign.TenantID = "tenant-b"
			namespace, err := s.namespace(foreign)
			if err != nil {
				t.Fatal(err)
			}
			fenceUpdate(t, s, e, map[string]interface{}{"namespace": namespace, "tenant_id": foreign.TenantID})
		}},
		{"owner_metadata", MemoryProject, "stale", func(t *testing.T, s *Service, e Entry) {
			fenceUpdate(t, s, e, map[string]interface{}{"owner_id": "user-b"})
		}},
		{"scope_changed", MemoryProject, "stale", func(t *testing.T, s *Service, e Entry) {
			fenceUpdate(t, s, e, map[string]interface{}{"scope": MemoryUser})
		}},
		{"same_version_value_changed", MemoryProject, "stale", func(t *testing.T, s *Service, e Entry) {
			fenceUpdate(t, s, e, map[string]interface{}{"value": "quartz altered without revision"})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gate := newRecallFenceGate(t)
			backend := gate.backend()
			service := NewService(serviceTestDB(t, filepath.Join(t.TempDir(), "memory.db")), t.TempDir(), "assistant", WithRecallEmbedding(func() RecallEmbedding { return backend }))
			writer := service
			if tc.scope == MemoryGlobal {
				writer = service.shared
			}
			if err := writer.RecordWithSource(ctx, owner, "quartz private-key", "quartz PRIVATE_OLD_BODY", tc.scope, "fact", "active", 5, nil, 0, WriteSource{Type: "user_explicit"}); err != nil {
				t.Fatal(err)
			}
			entry, err := writer.Lookup(ctx, owner, "quartz private-key", tc.scope)
			if err != nil || entry == nil {
				t.Fatalf("seed: %v", err)
			}
			done := startFencedRecall(t, ctx, service, owner, gate)
			tc.change(t, writer, *entry)
			var changed Entry
			beforeRead := writer.db.Where("id = ?", entry.ID).Find(&changed)
			if beforeRead.Error != nil {
				t.Fatal(beforeRead.Error)
			}
			outcome := finishFencedRecall(t, done, gate)
			if outcome.err != nil || strings.Contains(outcome.result.Prompt, "PRIVATE_OLD_BODY") || len(outcome.result.Items) != 0 {
				t.Fatalf("stale lexical candidate selected: items=%d err=%v", len(outcome.result.Items), outcome.err)
			}
			if !fenceDecision(outcome.result, entry.ID, tc.reason) {
				t.Fatalf("missing exclusion %q: %#v", tc.reason, outcome.result.Decisions)
			}
			var after Entry
			afterRead := writer.db.Where("id = ?", entry.ID).Find(&after)
			if afterRead.Error != nil || afterRead.RowsAffected != beforeRead.RowsAffected ||
				after.Namespace != changed.Namespace || after.State != changed.State || after.Version != changed.Version || after.Value != changed.Value {
				t.Fatal("recall changed or restored a concurrently updated record")
			}
			if err := service.SaveRecallTrace(ctx, owner, "private-task-ref", "empty", outcome.result); err != nil {
				t.Fatal(err)
			}
			traces, err := service.RecentRecallTraces(ctx, owner, 1)
			if err != nil || len(traces) != 1 || len(traces[0].Decisions) == 0 || traces[0].Decisions[0].Reason != tc.reason {
				t.Fatalf("fence reason lost from trace: %#v %v", traces, err)
			}
			encoded, _ := json.Marshal(traces)
			for _, secret := range []string{"PRIVATE_OLD_BODY", "quartz private-key", owner.SessionID, "private-task-ref"} {
				if strings.Contains(string(encoded), secret) {
					t.Fatal("recall trace leaked private content or raw reference")
				}
			}
			// A later turn reads a corrected revision, never the captured one.
			if strings.HasSuffix(tc.name, "correct") {
				next, err := service.Recall(ctx, owner, "quartz requested", 2000, nil)
				if err != nil || !strings.Contains(next.Prompt, "CURRENT_NEW_BODY") || strings.Contains(next.Prompt, "PRIVATE_OLD_BODY") {
					t.Fatalf("corrected revision unavailable on next turn: %v", err)
				}
			}
		})
	}
}

func fenceCorrect(t *testing.T, service *Service, entry Entry) {
	t.Helper()
	owner := Owner{TenantID: "tenant-a", SubjectID: "user-a", PartitionID: "web-user-a", SessionID: "session-a"}
	if err := service.RecordWithSource(context.Background(), owner, entry.Key, "quartz CURRENT_NEW_BODY", entry.Scope, entry.Kind, "active", entry.Priority, nil, entry.Version, WriteSource{Type: "user_explicit"}); err != nil {
		t.Fatal(err)
	}
}

func fenceForget(t *testing.T, service *Service, entry Entry) {
	t.Helper()
	owner := Owner{TenantID: "tenant-a", SubjectID: "user-a", PartitionID: "web-user-a", SessionID: "session-a"}
	if forgotten, err := service.ForgetExpected(context.Background(), owner, entry.Key, entry.Scope, entry.Version); err != nil || !forgotten {
		t.Fatalf("forget: %v %v", forgotten, err)
	}
}

func fenceUpdate(t *testing.T, service *Service, entry Entry, changes map[string]interface{}) {
	t.Helper()
	result := service.db.Model(&Entry{}).Where("namespace = ? AND id = ?", entry.Namespace, entry.ID).Updates(changes)
	if result.Error != nil || result.RowsAffected != 1 {
		t.Fatalf("fixture update: rows=%d %v", result.RowsAffected, result.Error)
	}
}

func TestRecallFencingPreservesCurrentWorkingContext(t *testing.T) {
	ctx := context.Background()
	owner := Owner{TenantID: "tenant-a", SubjectID: "user-a", PartitionID: "web-user-a", SessionID: "session-a"}
	gate := newRecallFenceGate(t)
	backend := gate.backend()
	workspace := t.TempDir()
	service := NewService(serviceTestDB(t, filepath.Join(t.TempDir(), "memory.db")), workspace, "assistant", WithRecallEmbedding(func() RecallEmbedding { return backend }))
	scm := NewSessionContextManager(filepath.Join(workspace, ".openkitty", "context"))
	scm.SetWorkspace(workspace)
	if err := service.SetContextStore(scm); err != nil {
		t.Fatal(err)
	}
	if err := scm.Set("working", "CURRENT_SCM", "fact", 5, owner.SessionID); err != nil {
		t.Fatal(err)
	}
	if err := scm.Set("foreign", "FOREIGN_SCM", "fact", 5, "session-b"); err != nil {
		t.Fatal(err)
	}
	if err := service.Record(ctx, owner, "quartz old", "quartz OLD_DURABLE", MemoryProject, "fact", "active", 5, nil, 0); err != nil {
		t.Fatal(err)
	}
	entry, err := service.Lookup(ctx, owner, "quartz old", MemoryProject)
	if err != nil || entry == nil {
		t.Fatal(err)
	}
	done := startFencedRecall(t, ctx, service, owner, gate)
	fenceForget(t, service, *entry)
	outcome := finishFencedRecall(t, done, gate)
	if outcome.err != nil || !strings.Contains(outcome.result.Prompt, "CURRENT_SCM") || strings.Contains(outcome.result.Prompt, "FOREIGN_SCM") || strings.Contains(outcome.result.Prompt, "OLD_DURABLE") {
		t.Fatalf("current SCM discarded or foreign/stale context leaked: %v", outcome.err)
	}
	if len(outcome.result.Items) != 1 || outcome.result.Items[0].Version != 0 || outcome.result.Items[0].Reason != "session_working" {
		t.Fatalf("SCM synthetic contract changed: %#v", outcome.result.Items)
	}
}

func TestRecallFencingAppliesRetractionsToOrdinaryValues(t *testing.T) {
	ctx := context.Background()
	owner := Owner{PartitionID: "alice", SessionID: "session-a"}
	gate := newRecallFenceGate(t)
	backend := gate.backend()
	service := NewService(serviceTestDB(t, filepath.Join(t.TempDir(), "memory.db")), t.TempDir(), "assistant", WithRecallEmbedding(func() RecallEmbedding { return backend }))
	for _, key := range []string{"original", "duplicate"} {
		if err := service.Record(ctx, owner, key, "quartz RETRACTED_VALUE", MemoryProject, "fact", "active", 5, nil, 0); err != nil {
			t.Fatal(err)
		}
	}
	duplicate, err := service.Lookup(ctx, owner, "duplicate", MemoryProject)
	if err != nil || duplicate == nil {
		t.Fatal(err)
	}
	done := startFencedRecall(t, ctx, service, owner, gate)
	if _, err := service.Forget(ctx, owner, "original", MemoryProject); err != nil {
		t.Fatal(err)
	}
	outcome := finishFencedRecall(t, done, gate)
	if outcome.err != nil || len(outcome.result.Items) != 0 || !fenceDecision(outcome.result, duplicate.ID, "retracted") {
		t.Fatalf("ordinary duplicate revived retracted content: %#v %v", outcome.result.Decisions, outcome.err)
	}
	current, err := service.Lookup(ctx, owner, "duplicate", MemoryProject)
	if err != nil || current == nil || current.State != "active" || current.Version != duplicate.Version {
		t.Fatalf("read fence altered remaining record: %v", err)
	}
}

func TestRecallFencingCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	gate := newRecallFenceGate(t)
	backend := gate.backend()
	service := NewService(serviceTestDB(t, filepath.Join(t.TempDir(), "memory.db")), t.TempDir(), "assistant", WithRecallEmbedding(func() RecallEmbedding { return backend }))
	owner := Owner{PartitionID: "alice"}
	if err := service.Record(ctx, owner, "quartz", "quartz PRIVATE_VALUE", MemoryProject, "fact", "active", 5, nil, 0); err != nil {
		t.Fatal(err)
	}
	done := startFencedRecall(t, ctx, service, owner, gate)
	cancel()
	outcome := finishFencedRecall(t, done, gate)
	if !errors.Is(outcome.err, context.Canceled) || outcome.result.Prompt != "" || len(outcome.result.Items) != 0 {
		t.Fatalf("cancelled recall produced context: %v", outcome.err)
	}
}

func TestRecallFencingNamespacesAndSyntheticCandidates(t *testing.T) {
	ctx := context.Background()
	db := serviceTestDB(t, filepath.Join(t.TempDir(), "memory.db"))
	workspace := t.TempDir()
	service := NewService(db, workspace, "assistant")
	owner := Owner{TenantID: "tenant-a", SubjectID: "user-a", PartitionID: "web-user-a", SessionID: "session-a"}
	var candidates []Entry
	for _, tc := range []struct {
		service *Service
		owner   Owner
		scope   MemoryScope
	}{
		{service, Owner{TenantID: "tenant-b", SubjectID: owner.SubjectID, PartitionID: owner.PartitionID}, MemoryProject},
		{service, Owner{TenantID: owner.TenantID, SubjectID: "user-b", PartitionID: owner.PartitionID}, MemoryProject},
		{service, Owner{TenantID: owner.TenantID, SubjectID: owner.SubjectID, PartitionID: owner.PartitionID, SessionID: "session-b"}, MemorySession},
		{NewService(db, t.TempDir(), "assistant"), owner, MemoryProject},
		{NewService(db, workspace, "foreign-agent"), owner, MemoryProject},
		{service.shared, Owner{TenantID: "tenant-b", SubjectID: owner.SubjectID, PartitionID: owner.PartitionID}, MemoryGlobal},
	} {
		if err := tc.service.RecordWithSource(ctx, tc.owner, "quartz", "quartz FOREIGN_VALUE", tc.scope, "fact", "active", 5, nil, 0, WriteSource{Type: "user_explicit"}); err != nil {
			t.Fatal(err)
		}
		entry, err := tc.service.Lookup(ctx, tc.owner, "quartz", tc.scope)
		if err != nil || entry == nil {
			t.Fatal(err)
		}
		candidates = append(candidates, *entry)
	}
	namespace, err := service.namespace(owner)
	if err != nil {
		t.Fatal(err)
	}
	candidates = append(candidates, Entry{ID: "fabricated-scm", Namespace: namespace, Scope: MemorySession, SessionID: owner.SessionID, State: "active", Value: "quartz FOREIGN_VALUE", Version: 0})
	current, excluded, err := service.currentRecallEntries(ctx, owner, candidates)
	if err != nil || len(current) != 0 || len(excluded) != len(candidates) {
		t.Fatalf("namespace or synthetic entry accepted: current=%d excluded=%d err=%v", len(current), len(excluded), err)
	}
	for _, exclusion := range excluded {
		if exclusion.Reason != "stale" {
			t.Fatalf("unexpected foreign candidate reason: %q", exclusion.Reason)
		}
	}
}

func TestRecallFencingTrustedWebTraceSources(t *testing.T) {
	for _, source := range []string{"user_statement", "user_feedback", "task_execution", "task_insight", "learning_knowledge", "knowledge_fact"} {
		if traceSource(source) != source {
			t.Fatalf("trusted source lost: %q", source)
		}
	}
	if traceSource("untrusted-private-body") != "supplemental" {
		t.Fatal("trace accepted unrecognized source")
	}
}
