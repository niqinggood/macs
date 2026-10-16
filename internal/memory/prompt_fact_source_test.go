package memory

import (
	"context"
	"errors"
	"testing"
)

func TestPromptFactOriginalSourceCASAndExactConversation(t *testing.T) {
	kg, err := NewKnowledgeGraph(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer kg.Close()
	ctx := context.Background()
	var ids []int64
	for _, session := range []string{"alice", "bob"} {
		result, err := kg.db.Exec(`INSERT INTO triples(subject,predicate,object,source) VALUES(?,?,?,?)`, "user", "prefers", "concise", ConversationSessionSource(session))
		if err != nil {
			t.Fatal(err)
		}
		id, _ := result.LastInsertId()
		ids = append(ids, id)
	}
	current, err := kg.ReadPromptFactSource(ctx, "alice", ids[0])
	if err != nil || current.Content != "concise" || !current.Editable || !current.Deletable {
		t.Fatal("actual fact source missing", err)
	}
	if foreign, err := kg.ReadPromptFactSource(ctx, "alice", ids[1]); err != nil || !foreign.Missing {
		t.Fatal("foreign fact source leaked", err)
	}
	updated, err := kg.ChangePromptFactSource(ctx, "alice", ids[0], current.Revision, "update", "detailed")
	if err != nil || updated.Content != "detailed" {
		t.Fatal("actual fact object not updated", err)
	}
	if _, err := kg.ChangePromptFactSource(ctx, "alice", ids[0], current.Revision, "delete", ""); !errors.Is(err, ErrPromptSourceConflict) {
		t.Fatal("stale fact revision allowed", err)
	}
	deleted, err := kg.ChangePromptFactSource(ctx, "alice", ids[0], updated.Revision, "delete", "")
	if err != nil || !deleted.Missing {
		t.Fatal("fact not invalidated", err)
	}
	other, err := kg.ReadPromptFactSource(ctx, "bob", ids[1])
	if err != nil || other.Content != "concise" || other.Missing {
		t.Fatal("deletion affected other matching SPO row", err)
	}
	facts, err := kg.AllFacts(20)
	if err != nil || len(facts) != 1 || facts[0].ID != ids[1] {
		t.Fatal("future context still exposes deleted fact", err)
	}
}
