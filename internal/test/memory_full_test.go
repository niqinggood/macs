package app

import (
	"strings"
	"sync"
	"testing"
	"time"

	memorycore "mac/internal/memory"
	"mac/internal/runtimeutil"
	contenttools "mac/internal/tools/content"
	operationstools "mac/internal/tools/operations"
)

// ====================== Knowledge Graph Tests ======================

func TestKG_AddAndQuery(t *testing.T) {
	kg, err := NewKnowledgeGraph(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer kg.Close()

	// Add facts
	tests := []struct{ subj, pred, obj string }{
		{"Alice", "loves", "coding"},
		{"Alice", "works_at", "OpenAI"},
		{"Bob", "loves", "design"},
		{"Bob", "friend_of", "Alice"},
	}
	for _, tt := range tests {
		if err := kg.AddTriple(tt.subj, tt.pred, tt.obj, "test", "", "", 1.0); err != nil {
			t.Fatal(err)
		}
	}

	// Query
	facts, err := kg.QueryEntity("Alice", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(facts) != 2 {
		t.Errorf("expected 2 facts for Alice, got %d", len(facts))
	}

	// Search
	facts, err = kg.SearchFacts("coding", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(facts) != 1 {
		t.Errorf("expected 1 fact matching 'coding', got %d", len(facts))
	}

	// AllFacts
	all, err := kg.AllFacts(100)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 4 {
		t.Errorf("expected 4 total facts, got %d", len(all))
	}

	t.Log("KG CRUD: OK")
}

func TestKG_TemporalValidity(t *testing.T) {
	kg, err := NewKnowledgeGraph(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer kg.Close()

	// Add with time range
	kg.AddTriple("ProjectX", "status", "active", "test", "2025-01-01", "", 1.0)
	kg.AddTriple("ProjectX", "status", "completed", "test", "2026-01-01", "", 1.0)

	// Query at different times
	mid2025, _ := kg.QueryEntity("ProjectX", "2025-06-01")
	if len(mid2025) != 1 {
		t.Errorf("mid-2025: expected 1 fact, got %d", len(mid2025))
	}
	if mid2025[0].Object != "active" {
		t.Errorf("mid-2025: expected 'active', got %s", mid2025[0].Object)
	}

	now, _ := kg.QueryEntity("ProjectX", "2026-06-01")
	if len(now) != 1 {
		t.Errorf("2026: expected 1 fact, got %d", len(now))
	}
	if now[0].Object != "completed" {
		t.Errorf("2026: expected 'completed', got %s", now[0].Object)
	}

	t.Log("KG Temporal: OK")
}

func TestKG_Aliases(t *testing.T) {
	kg, err := NewKnowledgeGraph(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer kg.Close()

	kg.AddTriple("niqing", "email", "niqing@test.com", "test", "", "", 1.0)
	kg.AddAlias("niqing", "niqinggood")
	kg.AddAlias("niqing", "niqin")

	// Query by alias
	facts, _ := kg.QueryEntity("niqinggood", "")
	if len(facts) != 1 {
		t.Error("alias query failed")
	}

	facts, _ = kg.QueryEntity("niqin", "")
	if len(facts) != 1 {
		t.Error("second alias query failed")
	}

	t.Log("KG Aliases: OK")
}

func TestKG_MergeEntities(t *testing.T) {
	kg, err := NewKnowledgeGraph(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer kg.Close()

	kg.AddTriple("Alpha", "color", "red", "test", "", "", 1.0)
	kg.AddTriple("Alpha", "size", "large", "test", "", "", 1.0)
	kg.MergeEntities("Alpha", "Beta")

	// Beta's old facts should now be under Alpha
	facts, _ := kg.QueryEntity("Alpha", "")
	if len(facts) != 2 {
		t.Errorf("merge: expected 2 facts, got %d", len(facts))
	}

	t.Log("KG Merge: OK")
}

func TestKGBuildContextPromptSuppressesInternalFacts(t *testing.T) {
	kg, err := NewKnowledgeGraph(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer kg.Close()

	kg.AddTriple("verification_56300", "agent_role", "verification sub-agent", "test", "", "", 1.0)
	kg.AddTriple("openkitty-A", "created_post", "Post #1870", "test", "", "", 1.0)
	kg.AddTriple("User", "prefers", "concise answers", "test", "", "", 1.0)

	got := kg.BuildContextPrompt(10)
	if strings.Contains(got, "verification_56300") || strings.Contains(got, "openkitty-A") {
		t.Fatalf("internal or forbidden identity facts leaked into KG prompt: %q", got)
	}
	if !strings.Contains(got, "concise answers") {
		t.Fatalf("expected normal fact in KG prompt: %q", got)
	}
}

func TestKG_Invalidate(t *testing.T) {
	kg, err := NewKnowledgeGraph(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer kg.Close()

	kg.AddTriple("X", "is", "valid", "test", "", "", 1.0)
	kg.Invalidate("X", "is", "valid")

	// Query at future time should find nothing
	future := time.Now().AddDate(1, 0, 0).Format("2006-01-02")
	facts, _ := kg.QueryEntity("X", future)
	if len(facts) != 0 {
		t.Errorf("expected 0 facts after invalidation, got %d", len(facts))
	}

	t.Log("KG Invalidate: OK")
}

func TestKG_ConcurrentAccess(t *testing.T) {
	kg, err := NewKnowledgeGraph(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer kg.Close()

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			kg.AddTriple("Concurrent", "test", string(rune('A'+n)), "test", "", "", 1.0)
		}(i)
	}
	wg.Wait()

	facts, _ := kg.AllFacts(100)
	if len(facts) != 10 {
		t.Errorf("concurrent: expected 10 facts, got %d", len(facts))
	}

	t.Log("KG Concurrent: OK")
}

// ====================== Memory Decay Tests ======================

func TestDecay_Scoring(t *testing.T) {
	// Fresh fact: score near 1.0
	today := time.Now().Format("2006-01-02")
	score1 := MemoryDecay(today, 7)
	if score1 < 0.9 || score1 > 1.1 {
		t.Errorf("today score should be ~1.0, got %f", score1)
	}

	// 7 days old: score near 0.5
	weekAgo := time.Now().AddDate(0, 0, -7).Format("2006-01-02")
	score2 := MemoryDecay(weekAgo, 7)
	if score2 < 0.4 || score2 > 0.6 {
		t.Errorf("7-day score should be ~0.5, got %f", score2)
	}

	// 30 days old with 30-day half-life: score near 0.5
	monthAgo := time.Now().AddDate(0, 0, -30).Format("2006-01-02")
	score3 := MemoryDecay(monthAgo, 30)
	if score3 < 0.4 || score3 > 0.6 {
		t.Errorf("30-day score should be ~0.5, got %f", score3)
	}

	t.Log("Decay Scoring: OK")
}

func TestKG_WeightedQuery(t *testing.T) {
	kg, err := NewKnowledgeGraph(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer kg.Close()

	today := time.Now().Format("2006-01-02")
	weekAgo := time.Now().AddDate(0, 0, -7).Format("2006-01-02")

	kg.AddTriple("User", "prefers", "dark_theme", "test", weekAgo, "", 1.0)
	kg.AddTriple("User", "project", "chatroom", "test", today, "", 1.0)

	weighted, err := kg.QueryWeighted("User", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(weighted) != 2 {
		t.Fatalf("expected 2 weighted facts, got %d", len(weighted))
	}
	// Today's fact should score higher than last week's
	if weighted[0].Score <= weighted[1].Score {
		t.Error("today's fact should rank higher than last week's")
	}

	t.Log("KG Weighted: OK")
}

// ====================== Deriver Tests ======================

func TestDeriver_ParseFacts(t *testing.T) {
	// Test JSON array parsing
	raw := `[{"subject":"User","predicate":"prefers","object":"dark mode","confidence":0.9}]`
	facts, err := memorycore.ParseDeriveFacts(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(facts) != 1 {
		t.Fatalf("expected 1 fact, got %d", len(facts))
	}
	if facts[0].Subject != "User" || facts[0].Predicate != "prefers" {
		t.Error("parsed fact mismatch")
	}

	// Test with markdown code fence
	raw2 := "```json\n[{\"subject\":\"X\",\"predicate\":\"does\",\"object\":\"Y\",\"confidence\":1.0}]\n```"
	facts2, err := memorycore.ParseDeriveFacts(raw2)
	if err != nil {
		t.Fatal(err)
	}
	if len(facts2) != 1 {
		t.Fatalf("expected 1 fact from fenced JSON, got %d", len(facts2))
	}

	// Test empty
	facts3, err := memorycore.ParseDeriveFacts("[]")
	if err != nil {
		t.Fatal(err)
	}
	if len(facts3) != 0 {
		t.Errorf("expected 0 facts from [], got %d", len(facts3))
	}

	t.Log("Deriver Parse: OK")
}

// ====================== Shared State Tests ======================

func TestSharedState_Concurrent(t *testing.T) {
	ss := runtimeutil.NewSharedState()
	var wg sync.WaitGroup

	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			key := "key" + string(rune('A'+n%26))
			ss.Set(key, n)
		}(i)
	}
	wg.Wait()

	snap := ss.Snapshot()
	if len(snap) == 0 {
		t.Error("concurrent writes produced empty state")
	}

	t.Log("SharedState Concurrent: OK")
}

func TestSharedState_MergeAndClear(t *testing.T) {
	ss := runtimeutil.NewSharedState()
	ss.Merge(map[string]interface{}{"a": 1, "b": "hello"})
	if len(ss.Snapshot()) != 2 {
		t.Error("merge failed")
	}
	ss.Clear()
	if ss.Get("a") != nil {
		t.Error("clear failed")
	}

	t.Log("SharedState Merge/Clear: OK")
}

// ====================== AG-UI Tests ======================

func TestAGUI_EventMapping(t *testing.T) {
	// Test that internal events map correctly to AG-UI format
	evt := Event{Type: "thinking", Data: "Hello"}
	agui := mapToAGUI(evt)
	if agui == nil || agui.Type != AGUITextMessageContent {
		t.Error("thinking event should map to TEXT_MESSAGE_CONTENT")
	}

	evt2 := Event{Type: "state_change", Data: map[string]interface{}{"key": "val"}}
	agui2 := mapToAGUI(evt2)
	if agui2 == nil || agui2.Type != AGUIStateDelta {
		t.Error("state_change event should map to STATE_DELTA")
	}

	t.Log("AG-UI Mapping: OK")
}

// ====================== Tool Search Tests ======================

func TestToolDetail_Search(t *testing.T) {
	reg := NewToolRegistry()
	reg.RegisterBuiltinTools()
	reg.Register(&CodebaseTool{})
	reg.Register(operationstools.NewSharedStateTool(runtimeutil.NewSharedState()))
	reg.Register(&contenttools.GenUITool{})

	td := &ToolDetailTool{registry: reg}

	// Search should find tools
	result := td.searchTools("code")
	if result == "" {
		t.Error("search should find codebase tool")
	}
	if td.searchTools("nonexistent_xyz_123") == "" {
		t.Error("should return message even for no results")
	}

	t.Log("Tool Search: OK")
}

func TestKG_SearchVerbatim(t *testing.T) {
	kg, err := NewKnowledgeGraph(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer kg.Close()

	kg.AddVerbatim("project", "AI客服系统需要支持多语言", "chat1")
	kg.AddVerbatim("project", "用户喜欢深色主题的UI", "chat2")
	kg.AddVerbatim("project", "数据库用PostgreSQL不用MySQL", "chat1")
	kg.AddVerbatim("tools", "Python脚本处理Excel导入", "chat3")

	results, err := kg.SearchVerbatim("数据库", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) == 0 {
		t.Error("should find database-related memory")
	}
	if results[0].Score <= 0 {
		t.Error("score should be positive")
	}

	results2, _ := kg.SearchVerbatim("nonexistent_xyz", 10)
	if len(results2) != 0 {
		t.Error("should return empty for no match")
	}

	results3, _ := kg.SearchVerbatim("UI 主题", 10)
	if len(results3) == 0 {
		t.Error("should find UI-related memory")
	}

	t.Log("KG Verbatim Search: OK")
}
