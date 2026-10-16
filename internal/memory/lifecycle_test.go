package memory

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestAgentMemoryCorrectionAndDeletionSurviveReloadAndMerge(t *testing.T) {
	store := NewAgentMemoryStore(t.TempDir(), "assistant")
	tool := NewAgentMemoryTool(store, "")
	ctx := memoryTestContext("original")
	if _, err := tool.Execute(ctx, map[string]interface{}{"action": "remember", "scope": "user", "key": "language", "value": "English"}); err != nil {
		t.Fatal(err)
	}
	if got := store.List(MemoryUser, "", 1)[0].SourceSessionID; got != "original" {
		t.Fatalf("source session = %q", got)
	}
	correction := map[string]interface{}{"action": "correct", "scope": "user", "key": "language", "value": "中文", "expected_version": 1}
	if _, err := tool.Execute(memoryTestContext("correction"), correction); err != nil {
		t.Fatal(err)
	}
	if _, err := tool.Execute(ctx, correction); err == nil {
		t.Fatal("stale correction overwrote the new version")
	}
	if got := store.Recall("language"); got != "中文" {
		t.Fatalf("corrected value = %q", got)
	}
	if _, err := tool.Execute(ctx, map[string]interface{}{"action": "forget", "scope": "user", "key": "language"}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(store.filePath())
	if err != nil || strings.Contains(string(data), "English") || strings.Contains(string(data), "中文") {
		t.Fatalf("deleted content retained: %s, %v", data, err)
	}
	reloaded := &AgentMemoryStore{agentMemoryState: &agentMemoryState{memories: make(map[string]*AgentMemory)}, storeDir: store.storeDir, agentName: store.agentName}
	reloaded.load()
	old := NewAgentMemoryStore(t.TempDir(), "assistant")
	for range 10 {
		if err := old.Remember("language", "old copy", MemoryUser, 5, nil, ""); err != nil {
			t.Fatal(err)
		}
	}
	if err := reloaded.Merge(old); err != nil {
		t.Fatal(err)
	}
	if reloaded.Recall("language") != "" || len(reloaded.List("", "", 0)) != 0 || reloaded.Snapshot(10) != "" {
		t.Fatal("deleted memory resurfaced after reload/merge")
	}
	if err := reloaded.Remember("language", "explicit new choice", MemoryUser, 5, nil, "new"); err != nil {
		t.Fatal(err)
	}
	if reloaded.Recall("language") != "explicit new choice" {
		t.Fatal("explicit re-remember was blocked")
	}
}

func TestAgentMemoryCorrectionFailureRollsBack(t *testing.T) {
	store := NewAgentMemoryStore(t.TempDir(), "assistant")
	if err := store.Remember("key", "before", MemoryProject, 5, nil, ""); err != nil {
		t.Fatal(err)
	}
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, []byte("block"), 0600); err != nil {
		t.Fatal(err)
	}
	store.storeDir = filepath.Join(blocker, "child")
	if err := store.CorrectForSession("key", "after", MemoryProject, "", 1); err == nil {
		t.Fatal("failed write reported success")
	}
	if store.Recall("key") != "before" || store.List(MemoryProject, "", 1)[0].Version != 1 {
		t.Fatal("failed correction changed memory/version")
	}
}

func TestMemoryToolsHonorHostAccessAndAllowRemoval(t *testing.T) {
	ctx := memoryTestContext("one")
	access := func(context.Context, bool) bool { return false }
	store := NewAgentMemoryStore(t.TempDir(), "assistant")
	if err := store.Remember("key", "secret", MemoryUser, 5, nil, "one"); err != nil {
		t.Fatal(err)
	}
	tool := NewAgentMemoryToolWithAccess(store, "", access)
	for _, action := range []string{"remember", "correct", "recall", "list", "snapshot", "forget"} {
		if got, err := tool.Execute(ctx, map[string]interface{}{"action": action, "scope": "user", "key": "key", "value": "changed", "enabled": true}); err == nil || strings.Contains(got, "secret") {
			t.Fatalf("host denial bypassed by %s: %q, %v", action, got, err)
		}
	}
	tool.access = func(_ context.Context, removal bool) bool { return removal }
	if _, err := tool.Execute(ctx, map[string]interface{}{"action": "forget", "scope": "user", "key": "key"}); err != nil {
		t.Fatal(err)
	}
	manager := NewSessionContextManager(filepath.Join(t.TempDir(), "context"))
	if err := manager.SetDurable("key", "secret", "fact", 5); err != nil {
		t.Fatal(err)
	}
	contextTool := NewSessionContextToolWithAccess(manager, func(_ context.Context, removal bool) bool { return removal })
	for _, action := range []string{"set", "get", "list", "inject"} {
		if _, err := contextTool.Execute(ctx, map[string]interface{}{"action": action, "key": "key", "value": "changed"}); err == nil {
			t.Fatalf("disabled session context allowed %s", action)
		}
	}
	if _, err := contextTool.Execute(ctx, map[string]interface{}{"action": "delete", "scope": "durable", "key": "key"}); err != nil || manager.GetDurable("key") != "" {
		t.Fatalf("disabled memory removal failed: %v", err)
	}
}

func TestKnowledgeFactsKeepMultipleValuesAndAtomicEvidence(t *testing.T) {
	kg, err := NewKnowledgeGraph(filepath.Join(t.TempDir(), ".openkitty"))
	if err != nil {
		t.Fatal(err)
	}
	defer kg.Close()
	for _, language := range []string{"Go", "Python"} {
		if err := kg.AddTriple("Project", "uses", language, "manual", "2026-01-01", "", 1); err != nil {
			t.Fatal(err)
		}
	}
	facts, err := kg.QueryEntity("Project", "")
	if err != nil || len(facts) != 2 {
		t.Fatalf("multi-valued relation overwritten: %#v, %v", facts, err)
	}
	fact := Fact{Subject: "User", Predicate: "prefers", Object: "中文", Source: ConversationSessionSource("one"), Confidence: 1}
	for range 2 {
		if err := kg.AddFactWithEvidence(context.Background(), fact, "以后请用中文回答"); err != nil {
			t.Fatal(err)
		}
	}
	facts, _ = kg.QueryEntity("User", "")
	if len(facts) != 1 {
		t.Fatalf("duplicate extraction: %#v", facts)
	}
	if quote, err := kg.FactEvidence(facts[0].ID); err != nil || quote != "以后请用中文回答" {
		t.Fatalf("evidence = %q, %v", quote, err)
	}
	if _, err := kg.db.Exec(`CREATE TRIGGER reject_evidence BEFORE INSERT ON fact_evidence BEGIN SELECT RAISE(ABORT, 'simulated evidence failure'); END`); err != nil {
		t.Fatal(err)
	}
	fact.Object = "failed-value"
	if err := kg.AddFactWithEvidence(context.Background(), fact, "supporting quote"); err == nil {
		t.Fatal("evidence failure reported success")
	}
	facts, _ = kg.QueryEntity("User", "")
	if len(facts) != 1 {
		t.Fatal("orphan fact committed without evidence")
	}
	if kg.BelongsToWorkspace(t.TempDir()) {
		t.Fatal("graph accepted a foreign workspace")
	}
}

func TestDeriverRejectsAssistantOnlyOrFabricatedEvidence(t *testing.T) {
	kg, err := NewKnowledgeGraph(filepath.Join(t.TempDir(), ".openkitty"))
	if err != nil {
		t.Fatal(err)
	}
	defer kg.Close()
	deriver := NewDeriver(DeriverDependencies{Knowledge: kg, SessionSource: ConversationSessionSource, CallLLM: func(context.Context, []map[string]interface{}, int) (string, error) {
		facts := []DeriveFact{
			{Subject: "User", Predicate: "prefers", Object: "中文", Verbatim: "以后请用中文回答", Confidence: 1},
			{Subject: "User", Predicate: "job", Object: "医生", Verbatim: "你是一名医生", Confidence: 1},
			{Subject: "User", Predicate: "location", Object: "杭州", Verbatim: "我住在杭州", Confidence: 1},
			{Subject: "User", Predicate: "age", Object: "20", Confidence: 1},
		}
		data, _ := json.Marshal(facts)
		return string(data), nil
	}})
	deriver.Derive("one", "以后请用中文回答", "你是一名医生", nil)
	facts, _ := kg.QueryEntity("User", "")
	if len(facts) != 1 || facts[0].Object != "中文" {
		t.Fatalf("unsupported facts persisted: %#v", facts)
	}
	if strings.Contains(kg.BuildContextPromptForSession("two", 10), "中文") {
		t.Fatal("derived fact leaked to another session")
	}
	enabled := true
	deriver.enabled = func() bool { return enabled }
	deriver.callLLM = func(context.Context, []map[string]interface{}, int) (string, error) {
		enabled = false
		return `[{"subject":"User","predicate":"prefers","object":"new","verbatim":"以后请用中文回答"}]`, nil
	}
	deriver.Derive("one", "以后请用中文回答", "", nil)
	facts, _ = kg.QueryEntity("User", "")
	if len(facts) != 1 {
		t.Fatal("disabled derivation committed a late result")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	deriver.enabled = nil
	deriver.callLLM = func(context.Context, []map[string]interface{}, int) (string, error) {
		t.Error("cancelled derivation called model")
		return "", errors.New("cancelled")
	}
	deriver.DeriveContext(ctx, "one", "user text", "", nil)
}

func TestMemoryCorrectionsUseTenantPartition(t *testing.T) {
	if len(DetectDurableUserCorrections("这次不要用 cli")) != 0 || len(DetectDurableUserCorrections("以后不要用 cli")) == 0 {
		t.Fatal("one-off and durable corrections were not distinguished")
	}
	database, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	if err := InitMemoryDB(database); err != nil {
		t.Fatal(err)
	}
	store := NewMemoryStore(t.TempDir(), database, "assistant", 10)
	if err := store.ApplyCorrectionsForTenant("tenant-a", "same-name", []UserCorrection{{Type: "forbidden_tool", Value: "cli"}}); err != nil {
		t.Fatal(err)
	}
	profile, err := store.GetUserProfileForTenant("tenant-a", "same-name")
	if err != nil || !strings.Contains(string(profile.Constraints), "cli") {
		t.Fatalf("personal correction missing: %#v, %v", profile, err)
	}
	for _, tenant := range []string{"tenant-b", defaultExecutionTenantID} {
		profile, _ := store.GetUserProfileForTenant(tenant, "same-name")
		if profile != nil && strings.Contains(string(profile.Constraints), "cli") {
			t.Fatalf("correction leaked to %s", tenant)
		}
	}
	if err := store.ApplyCorrectionsForTenant("tenant-a", "same-name", []UserCorrection{{Type: "preferred_tool", Value: "cli"}}); err != nil {
		t.Fatal(err)
	}
	profile, _ = store.GetUserProfileForTenant("tenant-a", "same-name")
	var constraints map[string][]string
	if err := json.Unmarshal(profile.Constraints, &constraints); err != nil || len(constraints["forbidden"]) != 0 || len(constraints["preferred"]) != 1 {
		t.Fatalf("opposing stale preference retained: %s, %v", profile.Constraints, err)
	}
}
