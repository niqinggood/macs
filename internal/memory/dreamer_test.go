package memory

import (
	"context"
	"testing"
)

type dreamKnowledgeStub struct {
	entities []string
	facts    []Fact
	merges   [][2]string
}

func (stub *dreamKnowledgeStub) DurableEntityNames() ([]string, error) {
	return append([]string(nil), stub.entities...), nil
}

func (stub *dreamKnowledgeStub) MergeEntities(canonical, alias string) error {
	stub.merges = append(stub.merges, [2]string{canonical, alias})
	return nil
}

func (stub *dreamKnowledgeStub) RecentFacts(int) ([]Fact, error) {
	return append([]Fact(nil), stub.facts...), nil
}

func TestDreamerDeduplicatesEntityCase(t *testing.T) {
	knowledge := &dreamKnowledgeStub{entities: []string{"OpenKitty", "openkitty", "Another"}}
	dreamer := NewDreamer(DreamerDependencies{
		Knowledge: knowledge,
		CallLLM: func(context.Context, []map[string]interface{}, int) (string, error) {
			return "", nil
		},
	})
	if merged := dreamer.dedupEntities(); merged != 1 {
		t.Fatalf("merged = %d, want 1", merged)
	}
	if len(knowledge.merges) != 1 || knowledge.merges[0][1] != "openkitty" {
		t.Fatalf("merges = %#v", knowledge.merges)
	}
}

func TestDreamerDiscoversVisibleInsights(t *testing.T) {
	knowledge := &dreamKnowledgeStub{facts: []Fact{
		{Subject: "User", Predicate: "prefers", Object: "Go"},
		{Subject: "User", Predicate: "uses", Object: "Git"},
		{Subject: "Project", Predicate: "uses", Object: "SQLite"},
		{Subject: "Project", Predicate: "targets", Object: "Windows"},
		{Subject: "Project", Predicate: "needs", Object: "tests"},
	}}
	dreamer := NewDreamer(DreamerDependencies{
		Knowledge: knowledge,
		CallLLM: func(context.Context, []map[string]interface{}, int) (string, error) {
			return "The project consistently favors local Go tooling.", nil
		},
		FactVisible: func(Fact) bool { return true },
	})
	insights := dreamer.discoverInsights()
	if len(insights) != 1 {
		t.Fatalf("insights = %#v", insights)
	}
}

func TestDreamerCanStopAndRestartBeforeInitialDelay(t *testing.T) {
	dreamer := NewDreamer(DreamerDependencies{
		Knowledge: &dreamKnowledgeStub{},
		CallLLM: func(context.Context, []map[string]interface{}, int) (string, error) {
			return "", nil
		},
	})
	dreamer.Start()
	dreamer.Stop()
	dreamer.Start()
	dreamer.Stop()
}
