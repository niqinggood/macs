package memory

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	knowledgetools "mac/internal/tools/knowledge"

	"gorm.io/gorm"
)

// These deterministic vectors test retrieval plumbing and lifecycle, not a
// real model's semantic quality. The lexical benchmark remains independent.
func semanticTestBackend(calls *[]string) RecallEmbedding {
	return RecallEmbedding{ModelID: "deterministic-test-model", Embed: func(ctx context.Context, text string) ([]float64, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		*calls = append(*calls, text)
		if strings.Contains(text, "freight") || strings.Contains(text, "refrigerated") {
			return []float64{1, 0, 0}, nil
		}
		return []float64{0, 1, 0}, nil
	}}
}

func TestSemanticRecallParaphrasePersistenceAndModelChange(t *testing.T) {
	ctx := context.Background()
	path, workspace := filepath.Join(t.TempDir(), "memory.db"), t.TempDir()
	var calls []string
	backend := semanticTestBackend(&calls)
	option := WithRecallEmbedding(func() RecallEmbedding { return backend })
	s := NewService(serviceTestDB(t, path), workspace, "assistant", option)
	owner := Owner{TenantID: "local", PartitionID: "alice", SessionID: "chat"}
	if err := s.Record(ctx, owner, "dispatch", "Use refrigerated lorries.", MemoryProject, "fact", "active", 5, nil, 0); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveSummary(ctx, owner, "The refrigerated shipment needs a loading permit."); err != nil {
		t.Fatal(err)
	}
	check := func(s *Service) RecallResult {
		t.Helper()
		result, err := s.Recall(ctx, owner, "freight", 1000, nil)
		if err != nil || result.SemanticStatus != "ready" || len(result.Items) != 2 {
			t.Fatalf("semantic recall: %#v %v", result, err)
		}
		for _, item := range result.Items {
			if item.Reason != "semantic" {
				t.Fatalf("expected paraphrase-only hit: %#v", item)
			}
		}
		return result
	}
	check(s)
	if len(calls) != 3 {
		t.Fatalf("query + two documents: %v", calls)
	}
	calls = nil
	check(s)
	if len(calls) != 0 {
		t.Fatalf("warm repeated recall still called the provider: %v", calls)
	}
	reopened := NewService(serviceTestDB(t, path), workspace, "assistant", option)
	result := check(reopened)
	if len(calls) != 1 || calls[0] != "freight" {
		t.Fatalf("persisted index not reused: %v", calls)
	}
	if err := reopened.SaveRecallTrace(ctx, owner, "turn", "ready", result); err != nil {
		t.Fatal(err)
	}
	traces, err := reopened.RecentRecallTraces(ctx, owner, 1)
	if err != nil || len(traces) != 1 || traces[0].SemanticStatus != "ready" {
		t.Fatalf("semantic diagnostic lost: %#v %v", traces, err)
	}
	backend.ModelID = "replacement-model"
	calls = nil
	check(reopened)
	if len(calls) != 3 {
		t.Fatalf("model change reused old vectors: %v", calls)
	}
	var count int64
	if err := s.db.Model(&recallVector{}).Count(&count).Error; err != nil || count != 2 {
		t.Fatalf("obsolete models accumulated: %d %v", count, err)
	}
}

func TestSemanticRecallNamespaceStateAndSessionIsolation(t *testing.T) {
	ctx := context.Background()
	var calls []string
	backend := semanticTestBackend(&calls)
	s := NewService(serviceTestDB(t, filepath.Join(t.TempDir(), "memory.db")), t.TempDir(), "assistant", WithRecallEmbedding(func() RecallEmbedding { return backend }))
	owner := Owner{TenantID: "tenant", SubjectID: "alice", PartitionID: "alice", SessionID: "chat-a"}
	for _, tc := range []struct {
		owner      Owner
		key, state string
		scope      MemoryScope
	}{
		{owner, "allowed", "active", MemoryProject},
		{owner, "candidate-secret", "candidate", MemoryProject},
		{Owner{TenantID: "tenant", SubjectID: "bob", PartitionID: "alice"}, "owner-secret", "active", MemoryProject},
		{Owner{TenantID: "other", SubjectID: "alice", PartitionID: "alice"}, "tenant-secret", "active", MemoryProject},
		{Owner{TenantID: "tenant", SubjectID: "alice", PartitionID: "alice", SessionID: "chat-b"}, "session-secret", "active", MemorySession},
	} {
		if err := s.Record(ctx, tc.owner, tc.key, "refrigerated "+tc.key, tc.scope, "fact", tc.state, 5, nil, 0); err != nil {
			t.Fatal(err)
		}
	}
	for _, foreign := range []*Service{NewService(s.db, t.TempDir(), "assistant"), NewService(s.db, s.workspace, "another-agent")} {
		if err := foreign.Record(ctx, owner, "foreign-secret", "refrigerated foreign-secret", MemoryProject, "fact", "active", 5, nil, 0); err != nil {
			t.Fatal(err)
		}
	}
	result, err := s.Recall(ctx, owner, "freight", 1000, nil)
	if err != nil || len(result.Items) != 1 || !strings.Contains(result.Prompt, "allowed") || strings.Contains(result.Prompt, "secret") {
		t.Fatalf("isolated recall: %#v %v", result, err)
	}
	for _, call := range calls {
		if strings.Contains(call, "secret") {
			t.Fatalf("foreign/ineligible content sent to embedder: %s", call)
		}
	}
}

func TestSemanticRecallCorrectionDeletionAndInFlightFence(t *testing.T) {
	ctx := context.Background()
	var calls []string
	backend := semanticTestBackend(&calls)
	s := NewService(serviceTestDB(t, filepath.Join(t.TempDir(), "memory.db")), t.TempDir(), "assistant", WithRecallEmbedding(func() RecallEmbedding { return backend }))
	owner := Owner{PartitionID: "alice", SessionID: "chat"}
	record := func(value string) {
		t.Helper()
		if err := s.Record(ctx, owner, "dispatch", value, MemoryProject, "fact", "active", 5, nil, 0); err != nil {
			t.Fatal(err)
		}
	}
	record("refrigerated lorries")
	if err := s.SaveSummary(ctx, owner, "refrigerated lorries"); err != nil {
		t.Fatal(err)
	}
	if result, err := s.Recall(ctx, owner, "freight", 1000, nil); err != nil || len(result.Items) == 0 {
		t.Fatalf("seed recall: %#v %v", result, err)
	}
	record("Use rail instead.")
	result, err := s.Recall(ctx, owner, "freight", 1000, nil)
	if err != nil || len(result.Items) != 0 {
		t.Fatalf("corrected value or retracted summary resurfaced: %#v %v", result, err)
	}
	if _, err := s.Forget(ctx, owner, "dispatch", MemoryProject); err != nil {
		t.Fatal(err)
	}
	var count int64
	if err := s.db.Model(&recallVector{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("forgotten derived data retained: %d %v", count, err)
	}
	// Simulate a correction arriving during an external embedding request.
	record("refrigerated replacement")
	original := backend.Embed
	backend.Embed = func(ctx context.Context, text string) ([]float64, error) {
		if strings.Contains(text, "replacement") {
			if _, err := s.Forget(ctx, owner, "dispatch", MemoryProject); err != nil {
				t.Fatal(err)
			}
		}
		return original(ctx, text)
	}
	result, err = s.Recall(ctx, owner, "freight", 1000, nil)
	if err != nil || len(result.Items) != 0 || result.SemanticStatus != "partial" {
		t.Fatalf("in-flight stale vector recalled: %#v %v", result, err)
	}
	if err := s.db.Model(&recallVector{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("in-flight stale vector persisted: %d %v", count, err)
	}
}

func TestSemanticRecallBeyondRecentPoolAndBoundedBackfill(t *testing.T) {
	ctx := context.Background()
	var calls []string
	backend := semanticTestBackend(&calls)
	s := NewService(serviceTestDB(t, filepath.Join(t.TempDir(), "memory.db")), t.TempDir(), "assistant", WithRecallEmbedding(func() RecallEmbedding { return backend }))
	owner := Owner{PartitionID: "alice"}
	namespace, _ := s.namespace(owner)
	var entries []Entry
	for i := 0; i < 260; i++ {
		entries = append(entries, Entry{ID: fmt.Sprintf("entry-%03d", i), Namespace: namespace, Key: fmt.Sprintf("note-%03d", i), Value: "gardening", Scope: MemoryProject, State: "active", Kind: "fact", Version: 1, UpdatedAt: time.Now()})
	}
	needle := &entries[259]
	needle.Value, needle.UpdatedAt = "refrigerated lorries", time.Now().Add(-365*24*time.Hour)
	if err := s.db.Create(&entries).Error; err != nil {
		t.Fatal(err)
	}
	if err := s.saveRecallVector(ctx, namespace, memoryHash("recall-v1", backend.ModelID), semanticEntryDocument(needle), []float64{1, 0, 0}); err != nil {
		t.Fatal(err)
	}
	result, err := s.Recall(ctx, owner, "freight", 1000, nil)
	if err != nil || len(result.Items) != 1 || result.Items[0].ID != needle.ID || result.SemanticStatus != "partial" {
		t.Fatalf("old semantic match missing: %#v %v", result, err)
	}
	if len(calls) != 1+semanticBackfillPerSource {
		t.Fatalf("unbounded backfill: %d embedding calls", len(calls))
	}
	calls = nil
	if _, err := s.Recall(ctx, owner, "freight", 1000, nil); err != nil {
		t.Fatal(err)
	}
	var count int64
	if err := s.db.Model(&recallVector{}).Count(&count).Error; err != nil || count != 1+2*semanticBackfillPerSource {
		t.Fatalf("backfill did not advance: %d %v", count, err)
	}
}

func TestSemanticRecallColdHistoryMakesProgressWithBoundedReads(t *testing.T) {
	ctx := context.Background()
	var calls []string
	backend := semanticTestBackend(&calls)
	s := NewService(serviceTestDB(t, filepath.Join(t.TempDir(), "memory.db")), t.TempDir(), "assistant", WithRecallEmbedding(func() RecallEmbedding { return backend }))
	owner := Owner{PartitionID: "alice"}
	namespace, _ := s.namespace(owner)
	var entries []Entry
	for i := 0; i < 1000; i++ {
		entries = append(entries, Entry{ID: fmt.Sprintf("entry-%04d", i), Namespace: namespace, Key: fmt.Sprintf("note-%04d", i), Value: "gardening", Scope: MemoryProject, State: "active", Kind: "fact", Version: 1})
	}
	if err := s.db.CreateInBatches(&entries, 100).Error; err != nil {
		t.Fatal(err)
	}
	// Observe actual database rows read by the semantic path. A large cold
	// history must not be materialized before the bounded work can be saved.
	var rowsRead int64
	const callback = "test:semantic_read_budget"
	if err := s.db.Callback().Query().After("gorm:query").Register(callback, func(tx *gorm.DB) {
		if tx.Statement.Table == (Entry{}).TableName() {
			rowsRead += max(0, tx.RowsAffected)
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.db.Callback().Query().Remove(callback) })
	for turn := 1; turn <= 2; turn++ {
		rowsRead = 0
		result := s.semanticRecall(ctx, owner, "freight", func(string) bool { return false })
		if result.status != "partial" {
			t.Fatalf("incomplete history reported ready: %#v", result)
		}
		if rowsRead > int64(semanticRecallPageSize+(turn-1)*semanticBackfillPerSource) {
			t.Fatalf("cold history scanned before backfill: %d rows", rowsRead)
		}
		var count int64
		if err := s.db.Model(&recallVector{}).Count(&count).Error; err != nil || count != int64(turn*semanticBackfillPerSource) {
			t.Fatalf("backfill progress: %d %v", count, err)
		}
	}
	if len(calls) != 1+2*semanticBackfillPerSource {
		t.Fatalf("query or document work repeated: %v", calls)
	}
}

func TestSemanticRecallFallbackAndCancellation(t *testing.T) {
	ctx := context.Background()
	backend := RecallEmbedding{ModelID: "unavailable", Embed: func(context.Context, string) ([]float64, error) { return nil, errors.New("offline") }}
	s := NewService(serviceTestDB(t, filepath.Join(t.TempDir(), "memory.db")), t.TempDir(), "assistant", WithRecallEmbedding(func() RecallEmbedding { return backend }))
	owner := Owner{PartitionID: "alice"}
	if err := s.Record(ctx, owner, "railway", "Use rail transport.", MemoryProject, "fact", "active", 5, nil, 0); err != nil {
		t.Fatal(err)
	}
	result, err := s.Recall(ctx, owner, "railway", 1000, nil)
	if err != nil || len(result.Items) != 1 || result.SemanticStatus != "unavailable" {
		t.Fatalf("lexical fallback: %#v %v", result, err)
	}
	backend.Embed = func(ctx context.Context, _ string) ([]float64, error) { <-ctx.Done(); return nil, ctx.Err() }
	canceled, cancel := context.WithTimeout(ctx, 30*time.Millisecond)
	defer cancel()
	if _, err := s.Recall(canceled, owner, "railway", 1000, nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("parent cancellation lost: %v", err)
	}
	started := time.Now()
	result, err = s.Recall(ctx, owner, "railway", 1000, nil)
	if err != nil || len(result.Items) != 1 || result.SemanticStatus != "unavailable" || time.Since(started) > 6*time.Second {
		t.Fatalf("embedding timeout did not fall back: %#v %v", result, err)
	}
}

func TestSemanticRecallInvalidVectors(t *testing.T) {
	for _, vector := range [][]float64{nil, {0, 0}, {math.NaN()}, {math.Inf(1)}, make([]float64, maxRecallVectorDimension+1)} {
		if _, ok := normalizedRecallVector(vector); ok {
			t.Fatalf("invalid vector accepted: len=%d", len(vector))
		}
	}
	input := []float64{3, 4}
	got, ok := normalizedRecallVector(input)
	if !ok || math.Abs(got[0]-0.6) > 1e-9 || input[0] != 3 {
		t.Fatalf("normalization mutates provider-owned storage: %v %v", input, got)
	}
}

type snapshotTestEmbedder struct{}

func (snapshotTestEmbedder) Embed(context.Context, string) ([]float64, error) {
	return []float64{1, 0}, nil
}
func (snapshotTestEmbedder) Dimension() int { return 2 }
func (snapshotTestEmbedder) Close() error   { return nil }

func TestSemanticRecallConfiguredBackendSnapshot(t *testing.T) {
	cfg := knowledgetools.EmbeddingConfig{Mode: knowledgetools.EmbeddingModeAPI, ModelName: "model-a", APIKey: "test-only", BaseURL: "https://embedding.invalid", Dimension: 2}
	restore := knowledgetools.SetEmbedderForTesting(snapshotTestEmbedder{}, cfg)
	defer restore()
	previous := memoryEmbedEnabled.Load()
	defer memoryEmbedEnabled.Store(previous)
	SetMemoryEmbed("api")
	first := configuredRecallEmbedding()
	if first.Embed == nil || first.ModelID == "" || strings.Contains(first.ModelID, cfg.APIKey) {
		t.Fatalf("invalid provider identity: %#v", first)
	}
	cfg.APIKey = "rotated-test-only"
	restoreKey := knowledgetools.SetEmbedderForTesting(snapshotTestEmbedder{}, cfg)
	if configuredRecallEmbedding().ModelID != first.ModelID {
		t.Fatal("credential rotation invalidated model identity")
	}
	restoreKey()
	cfg.ModelName = "model-b"
	restoreModel := knowledgetools.SetEmbedderForTesting(snapshotTestEmbedder{}, cfg)
	if configuredRecallEmbedding().ModelID == first.ModelID {
		t.Fatal("different model reused cache identity")
	}
	restoreModel()
	SetMemoryEmbed("none")
	if configuredRecallEmbedding().Embed != nil {
		t.Fatal("disabled memory embedding still enabled")
	}
}

func TestSemanticRecallSnapshotTracksLoadedModelRatherThanChangedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test-model.onnx")
	if err := os.WriteFile(path, []byte("original-test-model"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg := knowledgetools.EmbeddingConfig{Mode: knowledgetools.EmbeddingModeLocalONNX, ONNXModelPath: path, Dimension: 2}
	restore := knowledgetools.SetEmbedderForTesting(snapshotTestEmbedder{}, cfg)
	defer restore()
	_, loadedID := knowledgetools.EmbeddingSnapshot()
	if err := os.WriteFile(path, []byte("a replacement test model"), 0600); err != nil {
		t.Fatal(err)
	}
	_, stillLoadedID := knowledgetools.EmbeddingSnapshot()
	if stillLoadedID != loadedID {
		t.Fatal("existing provider was relabeled with a file it has not loaded")
	}
	restoreReloaded := knowledgetools.SetEmbedderForTesting(snapshotTestEmbedder{}, cfg)
	defer restoreReloaded()
	_, reloadedID := knowledgetools.EmbeddingSnapshot()
	if reloadedID == loadedID {
		t.Fatal("reloaded replacement model reused the original cache")
	}
}

func TestSemanticRecallRejectsMismatchedDimensionsAndRepairsCorruptCache(t *testing.T) {
	ctx := context.Background()
	var calls []string
	backend := semanticTestBackend(&calls)
	option := WithRecallEmbedding(func() RecallEmbedding { return backend })
	s := NewService(serviceTestDB(t, filepath.Join(t.TempDir(), "memory.db")), t.TempDir(), "assistant", option)
	owner := Owner{PartitionID: "alice"}
	if err := s.Record(ctx, owner, "dispatch", "refrigerated lorries", MemoryProject, "fact", "active", 5, nil, 0); err != nil {
		t.Fatal(err)
	}
	original := backend.Embed
	backend.Embed = func(ctx context.Context, text string) ([]float64, error) {
		if text == "freight" {
			return []float64{1, 0}, nil
		}
		return original(ctx, text)
	}
	result, err := s.Recall(ctx, owner, "freight", 1000, nil)
	if err != nil || result.SemanticStatus != "partial" || len(result.Items) != 0 {
		t.Fatalf("dimension mismatch accepted: %#v %v", result, err)
	}
	backend.Embed = original
	backend.ModelID = "compatible-dimension-model"
	if _, err := s.Recall(ctx, owner, "freight", 1000, nil); err != nil {
		t.Fatal(err)
	}
	namespace, _ := s.namespace(owner)
	if err := s.db.Model(&recallVector{}).Where("namespace = ?", namespace).Update("vector", "corrupt-json").Error; err != nil {
		t.Fatal(err)
	}
	calls = nil
	result, err = s.Recall(ctx, owner, "freight", 1000, nil)
	if err != nil || len(result.Items) != 1 || len(calls) != 1 || result.SemanticStatus != "ready" {
		t.Fatalf("cache not repaired: %#v %v calls=%v", result, err, calls)
	}
}

func TestSemanticRecallCachedHitsSurviveSlowBackfill(t *testing.T) {
	ctx := context.Background()
	backend := RecallEmbedding{ModelID: "slow-backfill", Embed: func(ctx context.Context, text string) ([]float64, error) {
		if text == "freight" {
			return []float64{1, 0}, nil
		}
		<-ctx.Done()
		return nil, ctx.Err()
	}}
	s := NewService(serviceTestDB(t, filepath.Join(t.TempDir(), "memory.db")), t.TempDir(), "assistant", WithRecallEmbedding(func() RecallEmbedding { return backend }))
	owner := Owner{PartitionID: "alice", SessionID: "chat"}
	namespace, _ := s.namespace(owner)
	// The missing vector sorts first. Its timeout must not hide the indexed
	// document in a later page or the cached summary in the other source.
	var entries []Entry
	for i := 0; i <= semanticRecallPageSize; i++ {
		entries = append(entries, Entry{ID: fmt.Sprintf("entry-%03d", i), Namespace: namespace, Key: fmt.Sprintf("note-%03d", i), Value: "orchards", Scope: MemoryProject, State: "active", Kind: "fact", Version: 1})
	}
	needle := &entries[len(entries)-1]
	needle.Value = "refrigerated lorries"
	if err := s.db.Create(&entries).Error; err != nil {
		t.Fatal(err)
	}
	if err := s.SaveSummary(ctx, owner, "Loading permit required."); err != nil {
		t.Fatal(err)
	}
	var summary Summary
	if err := s.db.Where("namespace = ?", namespace).First(&summary).Error; err != nil {
		t.Fatal(err)
	}
	for _, doc := range []semanticDocument{semanticEntryDocument(needle), semanticSummaryDocument(&summary)} {
		if err := s.saveRecallVector(ctx, namespace, memoryHash("recall-v1", backend.ModelID), doc, []float64{1, 0}); err != nil {
			t.Fatal(err)
		}
	}
	started := time.Now()
	result, err := s.Recall(ctx, owner, "freight", 1000, nil)
	if err != nil || len(result.Items) != 2 || result.SemanticStatus != "partial" {
		t.Fatalf("cached hits lost during slow backfill: %#v %v", result, err)
	}
	if elapsed := time.Since(started); elapsed >= 2*semanticRecallTimeout {
		t.Fatalf("backfill exceeded the recall deadline: %v", elapsed)
	}
}
