package memory

import (
	"context"
	"path/filepath"
	"testing"
)

func TestConversationFactSourcesRequireAudienceAndEvidence(t *testing.T) {
	kg, err := NewKnowledgeGraph(filepath.Join(t.TempDir(), ".openkitty"))
	if err != nil {
		t.Fatal(err)
	}
	defer kg.Close()
	for _, source := range []string{ConversationSessionSource("mine"), ConversationSessionSource("other"), "conversation", "manual"} {
		if err := kg.AddFactWithEvidence(context.Background(), Fact{Subject: "fixture", Predicate: "prefers", Object: source, Source: source}, "original user evidence"); err != nil {
			t.Fatal(err)
		}
	}
	if err := kg.AddTriple("unsupported", "prefers", "no evidence", ConversationSessionSource("mine"), "", "", 1); err != nil {
		t.Fatal(err)
	}
	var before, after int
	if err := kg.db.QueryRow("SELECT COUNT(*) FROM triples").Scan(&before); err != nil {
		t.Fatal(err)
	}
	items, err := kg.ConversationFactSources(context.Background(), []string{"mine"}, 10)
	if err != nil || len(items) != 1 || items[0].Fact.Source != ConversationSessionSource("mine") || items[0].Evidence != "original user evidence" {
		t.Fatalf("unscoped facts leaked: %#v %v", items, err)
	}
	if err := kg.db.QueryRow("SELECT COUNT(*) FROM triples").Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Fatal("source query modified graph")
	}
	for _, limit := range []int{0, 101} {
		if _, err := kg.ConversationFactSources(context.Background(), []string{"mine"}, limit); err == nil {
			t.Fatal("unbounded query allowed")
		}
	}
	if _, err := kg.ConversationFactSources(context.Background(), []string{""}, 1); err == nil {
		t.Fatal("empty audience allowed")
	}
	if _, err := kg.ConversationFactSources(context.Background(), []string{"mine' OR 1=1 --"}, 10); err != nil {
		t.Fatal(err)
	}
}
