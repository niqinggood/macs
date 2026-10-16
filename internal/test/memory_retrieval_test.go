package app

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	knowledgetools "mac/internal/tools/knowledge"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func newMemoryRetrievalTestStore(t *testing.T) (*MemoryStore, *gorm.DB) {
	t.Helper()
	dsn := "file:" + strings.NewReplacer("/", "_", "\\", "_", " ", "_").Replace(t.Name()) +
		"?mode=memory&cache=shared"
	database, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.AutoMigrate(
		&MemoryEvent{},
		&UserProfile{},
		&Knowledge{},
		&ToolSkillStat{},
		&AgentLog{},
	); err != nil {
		t.Fatal(err)
	}
	return NewMemoryStore(filepath.Join(t.TempDir(), "memory"), database, "Researcher", 10), database
}

func addMemoryRetrievalEvent(t *testing.T, database *gorm.DB, event MemoryEvent) {
	t.Helper()
	if event.TenantID == "" {
		event.TenantID = defaultExecutionTenantID
	}
	if event.AgentName == "" {
		event.AgentName = "Researcher"
	}
	if event.Status == "" {
		event.Status = "success"
	}
	if err := database.Create(&event).Error; err != nil {
		t.Fatal(err)
	}
}

func TestMemoryFTSHandlesNaturalLanguagePunctuationAndRanksQuestionMatch(t *testing.T) {
	store, database := newMemoryRetrievalTestStore(t)
	addMemoryRetrievalEvent(t, database, MemoryEvent{
		SessionID: "question-hit", UserName: "alice",
		UserQuestion: "deploy alpha service", FinalAnswer: "completed with systemd",
	})
	addMemoryRetrievalEvent(t, database, MemoryEvent{
		SessionID: "answer-hit", UserName: "alice",
		UserQuestion: "unrelated historical task", FinalAnswer: "mentioned deploy alpha service in passing",
	})

	results := store.SearchMemoryFTSForTenant(
		defaultExecutionTenantID,
		`deploy (alpha)? "service" OR`,
		"alice",
		10,
	)
	if len(results) < 2 {
		t.Fatalf("punctuated natural-language FTS results = %#v, want both matching rows", results)
	}
	if results[0].SessionID != "question-hit" {
		t.Fatalf("question-field match ranked after answer-only match: %#v", results)
	}
	if results[0].ScoreKind != memoryScoreBM25 || results[0].Score > results[1].Score {
		t.Fatalf("BM25 score semantics are not explicit/lower-is-better: %#v", results)
	}
}

func TestMemoryContextRecallsCrossSessionAndSkipsCurrentSession(t *testing.T) {
	store, database := newMemoryRetrievalTestStore(t)
	addMemoryRetrievalEvent(t, database, MemoryEvent{
		SessionID: "older-session", UserName: "alice",
		UserQuestion: "deploy alpha service", FinalAnswer: "older-session-answer-with-systemd",
	})
	addMemoryRetrievalEvent(t, database, MemoryEvent{
		SessionID: "current-session", UserName: "alice",
		UserQuestion: "deploy alpha service", FinalAnswer: "current-session-answer-must-not-self-reference",
	})

	contextText := store.GetMemoryContextForTenant(
		defaultExecutionTenantID,
		"alice",
		"current-session",
		"deploy alpha service?",
	)
	if !strings.Contains(contextText, "older-session-answer-with-systemd") {
		t.Fatalf("cross-session match was not injected: %q", contextText)
	}
	referenceIndex := strings.Index(contextText, "## 📌 参考")
	if referenceIndex < 0 {
		t.Fatalf("cross-session reference section is missing: %q", contextText)
	}
	if strings.Contains(contextText[referenceIndex:], "current-session-answer-must-not-self-reference") {
		t.Fatalf("current session was injected as its own historical reference: %q", contextText[referenceIndex:])
	}
	if strings.Contains(contextText, "相关度:") {
		t.Fatalf("raw BM25/RRF score was exposed as a misleading percentage: %q", contextText)
	}
}

func TestMemoryContextLooksPastSeveralCurrentSessionMatches(t *testing.T) {
	store, database := newMemoryRetrievalTestStore(t)
	addMemoryRetrievalEvent(t, database, MemoryEvent{
		SessionID: "older-session", UserName: "alice",
		UserQuestion: "deploy alpha service", FinalAnswer: "older-session-answer-remains-visible",
	})
	for index := 0; index < 8; index++ {
		addMemoryRetrievalEvent(t, database, MemoryEvent{
			SessionID: "current-session", UserName: "alice",
			UserQuestion: "deploy alpha service", FinalAnswer: "current-session-progress",
		})
	}

	contextText := store.GetMemoryContextForTenant(
		defaultExecutionTenantID,
		"alice",
		"current-session",
		"deploy alpha service",
	)
	if !strings.Contains(contextText, "older-session-answer-remains-visible") {
		t.Fatalf("current-session matches exhausted the retrieval window: %q", contextText)
	}
}

func TestMemoryHybridFindsChineseSubstringWhenFTSTokenDoesNot(t *testing.T) {
	store, database := newMemoryRetrievalTestStore(t)
	addMemoryRetrievalEvent(t, database, MemoryEvent{
		SessionID: "ui-history", UserName: "alice",
		UserQuestion: "上次把对话按钮改成浅灰色", FinalAnswer: "按钮颜色已调整为浅灰色",
	})

	results := store.SearchMemoryHybridForTenant(
		defaultExecutionTenantID,
		"还记得浅灰色按钮怎么调的吗？",
		"alice",
		5,
	)
	if len(results) == 0 || results[0].SessionID != "ui-history" {
		t.Fatalf("Chinese lexical fallback did not recover the relevant memory: %#v", results)
	}
	if !memoryTestContainsString(results[0].MatchMethods, memoryMatchLexical) {
		t.Fatalf("hybrid result does not record lexical provenance: %#v", results[0])
	}
}

type deterministicMemoryEmbedder struct{}

func (deterministicMemoryEmbedder) Embed(_ context.Context, text string) ([]float64, error) {
	text = strings.ToLower(text)
	if strings.Contains(text, "pay this bill") || strings.Contains(text, "settle invoice balance") {
		return []float64{1, 0}, nil
	}
	return []float64{0, 1}, nil
}

func (deterministicMemoryEmbedder) Dimension() int { return 2 }
func (deterministicMemoryEmbedder) Close() error   { return nil }

func TestMemoryHybridUsesSemanticRecallWhenEnabled(t *testing.T) {
	store, database := newMemoryRetrievalTestStore(t)
	addMemoryRetrievalEvent(t, database, MemoryEvent{
		SessionID: "billing-history", UserName: "alice",
		UserQuestion: "settle invoice balance", FinalAnswer: "used the accounting reconciliation workflow",
	})
	addMemoryRetrievalEvent(t, database, MemoryEvent{
		SessionID: "travel-history", UserName: "alice",
		UserQuestion: "book mountain hotel", FinalAnswer: "reserved a room",
	})

	oldEnabled := memoryEmbedEnabled
	memoryEmbedCacheMu.Lock()
	oldCache := memoryEmbedCache
	memoryEmbedCache = map[string][]float64{}
	memoryEmbedCacheMu.Unlock()
	restoreEmbedder := knowledgetools.SetEmbedderForTesting(
		deterministicMemoryEmbedder{},
		knowledgetools.EmbeddingConfig{Mode: knowledgetools.EmbeddingModeAPI, Dimension: 2},
	)
	memoryEmbedEnabled = true
	t.Cleanup(func() {
		restoreEmbedder()
		memoryEmbedEnabled = oldEnabled
		memoryEmbedCacheMu.Lock()
		memoryEmbedCache = oldCache
		memoryEmbedCacheMu.Unlock()
	})

	results := store.SearchMemoryHybridForTenant(
		defaultExecutionTenantID,
		"how can I pay this bill",
		"alice",
		5,
	)
	if len(results) == 0 || results[0].SessionID != "billing-history" {
		t.Fatalf("semantic-only memory was not recalled: %#v", results)
	}
	if !memoryTestContainsString(results[0].MatchMethods, memoryMatchVector) {
		t.Fatalf("hybrid result does not record vector provenance: %#v", results[0])
	}
}

func TestSetMemoryEmbedUsesInitializedBackendMode(t *testing.T) {
	// This is a memory-layer wiring test. The deterministic fake proves that an
	// already initialized non-API backend is accepted; it deliberately does not
	// claim that the optional ONNX runtime is present in the default build.
	oldEnabled := memoryEmbedEnabled
	restoreEmbedder := knowledgetools.SetEmbedderForTesting(
		deterministicMemoryEmbedder{},
		knowledgetools.EmbeddingConfig{Mode: knowledgetools.EmbeddingModeLocalONNX, Dimension: 2},
	)
	t.Cleanup(func() {
		restoreEmbedder()
		memoryEmbedEnabled = oldEnabled
	})

	SetMemoryEmbed(string(knowledgetools.EmbeddingModeLocalONNX))
	if !memoryEmbedEnabled {
		t.Fatal("local ONNX embedder was available but memory vector recall stayed disabled")
	}
	SetMemoryEmbed(string(knowledgetools.EmbeddingModeAPI))
	if memoryEmbedEnabled {
		t.Fatal("memory vector recall enabled for a mode that did not match the initialized backend")
	}
	SetMemoryEmbed("unsupported")
	if memoryEmbedEnabled {
		t.Fatal("unsupported memory embed mode enabled vector recall")
	}
}

func TestMemoryHybridFallsBackWhenVectorRecallDisabled(t *testing.T) {
	store, database := newMemoryRetrievalTestStore(t)
	addMemoryRetrievalEvent(t, database, MemoryEvent{
		SessionID: "fallback-history", UserName: "alice",
		UserQuestion: "deploy fallback service", FinalAnswer: "completed with systemd",
	})

	oldEnabled := memoryEmbedEnabled
	memoryEmbedEnabled = false
	t.Cleanup(func() { memoryEmbedEnabled = oldEnabled })
	results := store.SearchMemoryHybridForTenant(
		defaultExecutionTenantID,
		"deploy fallback service",
		"alice",
		5,
	)
	if len(results) == 0 || results[0].SessionID != "fallback-history" {
		t.Fatalf("FTS/lexical fallback stopped working without vectors: %#v", results)
	}
	if memoryTestContainsString(results[0].MatchMethods, memoryMatchVector) {
		t.Fatalf("disabled vector recall was reported as a match: %#v", results[0])
	}
}

func memoryTestContainsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
