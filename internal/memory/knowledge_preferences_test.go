package memory

import (
	"context"
	"path/filepath"
	"testing"
)

func TestWorkspacePreferenceArchiveExactRelationsKeepScopeAndExpiredDates(t *testing.T) {
	kg, err := NewKnowledgeGraph(filepath.Join(t.TempDir(), ".openkitty"))
	if err != nil {
		t.Fatal(err)
	}
	defer kg.Close()
	for _, row := range []struct{ subject, predicate, source, validTo string }{
		{"workspace", "prefers", "conversation", ""},
		{"owned", "prefers_report_format", ConversationSessionSource("mine"), ""},
		{"expired", "prefers_tone", "manual", "2020-01-01"},
		{"foreign", "prefers", ConversationSessionSource("foreign"), ""},
		{"not-preference", "has_capability", "manual", ""},
		{"not-whitelisted", "prefers_anything", "manual", ""},
	} {
		if err := kg.AddTriple(row.subject, row.predicate, "prefers is just object text", row.source, "", row.validTo, 0.5); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := kg.db.Exec("PRAGMA query_only=ON"); err != nil {
		t.Fatal(err)
	}
	first, total, err := kg.WorkspacePreferenceArchive(context.Background(), []string{"mine"}, "", 0, 2)
	if err != nil || total != 3 || len(first) != 2 {
		t.Fatal("preference archive did not apply exact predicates and original audience", total, len(first), err)
	}
	second, nextTotal, err := kg.WorkspacePreferenceArchive(context.Background(), []string{"mine"}, "", 2, 2)
	if err != nil || nextTotal != total || len(second) != 1 {
		t.Fatal("preference pagination lost records", nextTotal, len(second), err)
	}
	foundExpired := false
	for _, row := range append(first, second...) {
		if row.Subject == "foreign" || row.Predicate == "has_capability" || row.Predicate == "prefers_anything" {
			t.Fatal("unrelated content or foreign session entered preference archive")
		}
		if row.Subject == "expired" {
			foundExpired = row.ValidTo == "2020-01-01"
		}
	}
	if !foundExpired {
		t.Fatal("expired state was dropped or incorrectly made active")
	}
	for _, sessionIDs := range [][]string{{""}, {"mine' OR 1=1 --"}} {
		rows, count, err := kg.WorkspacePreferenceArchive(context.Background(), sessionIDs, "", 0, 200)
		if sessionIDs[0] == "" && err == nil || sessionIDs[0] != "" && (err != nil || count != 2 || len(rows) != 2) {
			t.Fatal("invalid or injected audience widened archive", count, err)
		}
	}
	if _, _, err := kg.WorkspacePreferenceArchive(context.Background(), nil, "", 0, 201); err == nil {
		t.Fatal("unbounded preference archive accepted")
	}
	if facts, err := kg.ConversationFactSources(context.Background(), []string{"mine"}, 100); err != nil || len(facts) != 0 {
		t.Fatal("viewing historical preferences activated recall", len(facts), err)
	}
}
