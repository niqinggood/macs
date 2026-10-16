package app

import (
	"context"
	"io"
	"log"
	"strings"
	"testing"

	memorycore "mac/internal/memory"
)

func TestPipelineExecutionContextsAreInternalMemoryTurns(t *testing.T) {
	for _, ctx := range []context.Context{
		context.WithValue(context.Background(), "pipeline_step_execution", true),
		context.WithValue(context.Background(), "pipeline_isolated_context", true),
	} {
		if !isInternalMemoryExecutionTurn(ctx) {
			t.Fatal("pipeline control contexts must not participate in user memory")
		}
	}
	if isInternalMemoryExecutionTurn(context.Background()) {
		t.Fatal("an ordinary user turn must remain eligible for user memory")
	}
}

func TestAutomaticSessionMemoryDoesNotPromoteTaskImperatives(t *testing.T) {
	oldSCM := globalSCM
	globalSCM = memorycore.NewSessionContextManager(t.TempDir())
	t.Cleanup(func() { globalSCM = oldSCM })

	agent := &LLMAgent{fileLogger: log.New(io.Discard, "", 0), scm: globalSCM}
	agent.extractSessionMemories(
		"session-one",
		"本次不要调用工具，必须只输出数字，禁止联网。",
		"7",
		nil,
	)
	if got := globalSCM.List("preference", 10); len(got) != 0 {
		t.Fatalf("task-local imperatives must not become preferences: %#v", got)
	}

	agent.extractSessionMemories(
		"session-one",
		"以后默认使用简洁中文回答。",
		"好的",
		nil,
	)
	prefs := globalSCM.List("preference", 10)
	if len(prefs) != 1 || prefs[0].SessionID != "session-one" {
		t.Fatalf("automatic preference should remain in its session: %#v", prefs)
	}
	if got := globalSCM.BuildContextPromptForSession("session-two", 10); strings.Contains(got, "简洁中文") {
		t.Fatalf("session preference leaked into a different session: %q", got)
	}
	if got := globalSCM.BuildContextPromptForSession("session-one", 10); !strings.Contains(got, "简洁中文") {
		t.Fatalf("same-session preference was lost: %q", got)
	}
}

func TestKnowledgeGraphConversationFactsAreSessionScoped(t *testing.T) {
	kg, err := NewKnowledgeGraph(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer kg.Close()

	if err := kg.AddTriple("legacy weather", "temperature", "25", "conversation", "2026-07-10", "", 1); err != nil {
		t.Fatal(err)
	}
	if err := kg.AddTriple("session one weather", "temperature", "26", conversationSessionSource("s1"), "2026-07-10", "", 1); err != nil {
		t.Fatal(err)
	}
	if err := kg.AddTriple("session two stock", "price", "88", conversationSessionSource("s2"), "2026-07-10", "", 1); err != nil {
		t.Fatal(err)
	}
	if err := kg.AddTriple("project convention", "language", "Chinese", "manual", "2026-07-10", "", 1); err != nil {
		t.Fatal(err)
	}

	s1 := kg.BuildContextPromptForSession("s1", 20)
	if !strings.Contains(s1, "session one weather") || !strings.Contains(s1, "project convention") {
		t.Fatalf("same-session and global facts should remain visible: %q", s1)
	}
	if strings.Contains(s1, "legacy weather") || strings.Contains(s1, "session two stock") {
		t.Fatalf("legacy or foreign-session facts leaked: %q", s1)
	}

	s2 := kg.BuildContextPromptForSession("s2", 20)
	if !strings.Contains(s2, "session two stock") || strings.Contains(s2, "session one weather") {
		t.Fatalf("session-two scope is incorrect: %q", s2)
	}
}

func TestSessionScopedTripleDoesNotInvalidateAnotherSession(t *testing.T) {
	kg, err := NewKnowledgeGraph(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer kg.Close()

	if err := kg.AddTriple("market", "status", "open", conversationSessionSource("s1"), "2026-07-10", "", 1); err != nil {
		t.Fatal(err)
	}
	if err := kg.AddTriple("market", "status", "closed", conversationSessionSource("s2"), "2026-07-10", "", 1); err != nil {
		t.Fatal(err)
	}

	facts, err := kg.QueryEntity("market", "2026-07-10")
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, fact := range facts {
		seen[fact.Object] = true
	}
	if !seen["open"] || !seen["closed"] {
		t.Fatalf("one session invalidated another session's fact: %#v", facts)
	}
}

func TestSessionScopedTripleSameDayUpdateKeepsOnlyNewestValue(t *testing.T) {
	kg, err := NewKnowledgeGraph(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer kg.Close()

	source := conversationSessionSource("s1")
	if err := kg.AddTriple("weather", "temperature", "25", source, "2026-07-10", "", 1); err != nil {
		t.Fatal(err)
	}
	if err := kg.AddTriple("weather", "temperature", "27", source, "2026-07-10", "", 1); err != nil {
		t.Fatal(err)
	}
	facts, err := kg.QueryEntity("weather", "2026-07-10")
	if err != nil {
		t.Fatal(err)
	}
	if len(facts) != 1 || facts[0].Object != "27" {
		t.Fatalf("same-day update should expose only the newest scoped fact: %#v", facts)
	}
}

func TestGlobalSourceDoesNotInvalidateSessionFact(t *testing.T) {
	kg, err := NewKnowledgeGraph(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer kg.Close()

	if err := kg.AddTriple("market", "status", "session-value", conversationSessionSource("s1"), "2026-07-10", "", 1); err != nil {
		t.Fatal(err)
	}
	if err := kg.AddTriple("market", "status", "manual-value", "manual", "2026-07-10", "", 1); err != nil {
		t.Fatal(err)
	}
	facts, err := kg.QueryEntity("market", "2026-07-10")
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, fact := range facts {
		seen[fact.Object] = true
	}
	if !seen["session-value"] || !seen["manual-value"] {
		t.Fatalf("independent scopes should remain valid: %#v", facts)
	}
}
