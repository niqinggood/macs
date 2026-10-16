package memory

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkspaceKnowledgeArchivePagesWithoutChangingRecall(t *testing.T) {
	kg, err := NewKnowledgeGraph(filepath.Join(t.TempDir(), ".openkitty"))
	if err != nil {
		t.Fatal(err)
	}
	defer kg.Close()
	for i := 0; i < 155; i++ {
		if err := kg.AddTriple(fmt.Sprintf("archive-%03d", i), "contains", "literal_%' value", "conversation", "", "", 1); err != nil {
			t.Fatal(err)
		}
	}
	for _, source := range []string{ConversationSessionSource("mine"), ConversationSessionSource("foreign"), "manual"} {
		if err := kg.AddTriple(source, "scope", "fixture", source, "", "", 1); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := kg.db.Exec("PRAGMA query_only = ON"); err != nil {
		t.Fatal(err)
	}
	first, count, err := kg.WorkspaceKnowledgeArchive(context.Background(), []string{"mine"}, false, "", 0, 100)
	if err != nil || count != 157 || len(first) != 100 {
		t.Fatalf("first archive page: %d %d %v", count, len(first), err)
	}
	second, secondCount, err := kg.WorkspaceKnowledgeArchive(context.Background(), []string{"mine"}, false, "", 100, 100)
	if err != nil || secondCount != count || len(second) != 57 {
		t.Fatalf("second archive page: %d %d %v", secondCount, len(second), err)
	}
	seen := map[int64]bool{}
	for _, item := range append(first, second...) {
		if seen[item.ID] || item.Source == ConversationSessionSource("foreign") || item.CreatedAt == "" {
			t.Fatalf("duplicate, foreign audience or fake time: %#v", item)
		}
		seen[item.ID] = true
	}
	items, count, err := kg.WorkspaceKnowledgeArchive(context.Background(), nil, false, "_%'", 0, 20)
	if err != nil || count != 155 || len(items) != 20 {
		t.Fatalf("literal search changed audience: %d %d %v", count, len(items), err)
	}
	items, count, err = kg.WorkspaceKnowledgeArchive(context.Background(), []string{"mine' OR 1=1 --"}, false, "scope", 0, 20)
	if err != nil || count != 1 || len(items) != 1 || items[0].Source != "manual" {
		t.Fatal("archive audience injection", count, err)
	}
	// Seeing a quarantined record must not make it eligible for future prompts.
	if strings.Contains(kg.BuildContextPromptForSession("mine", 200), "literal_%'") {
		t.Fatal("archive changed legacy conversation recall eligibility")
	}
	var after int64
	if err := kg.db.QueryRow("SELECT COUNT(*) FROM triples").Scan(&after); err != nil || after != 158 {
		t.Fatal("archive modified persisted graph", after, err)
	}
}

func TestWorkspaceKnowledgeArchiveQuotesExcludeConversationAudiences(t *testing.T) {
	kg, err := NewKnowledgeGraph(filepath.Join(t.TempDir(), ".openkitty"))
	if err != nil {
		t.Fatal(err)
	}
	defer kg.Close()
	for _, source := range []string{"conversation", "manual", ConversationSessionSource("mine"), ConversationSessionSource("foreign")} {
		if err := kg.AddVerbatim("fixture", "quotation "+source, source); err != nil {
			t.Fatal(err)
		}
	}
	items, count, err := kg.WorkspaceKnowledgeArchive(context.Background(), []string{"mine"}, true, "", 0, 100)
	if err != nil || count != 2 || len(items) != 2 {
		t.Fatal("unproven session quotations exposed", count, len(items), err)
	}
	for _, item := range items {
		if strings.HasPrefix(item.Source, "conversation:session:") {
			t.Fatal("quotation cannot be treated as verified original user text")
		}
	}
	for _, limits := range [][2]int{{-1, 1}, {0, 0}, {0, 101}, {100001, 1}} {
		if _, _, err := kg.WorkspaceKnowledgeArchive(context.Background(), nil, false, "", limits[0], limits[1]); err == nil {
			t.Fatal("unbounded archive request accepted")
		}
	}
	if _, _, err := kg.WorkspaceKnowledgeArchive(context.Background(), []string{""}, false, "", 0, 1); err == nil {
		t.Fatal("invalid archive audience accepted")
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := kg.WorkspaceKnowledgeArchive(cancelled, nil, false, "", 0, 1); err == nil {
		t.Fatal("cancelled archive query continued")
	}
}

func TestWorkspaceKnowledgeArchiveReportsContentLimit(t *testing.T) {
	kg, err := NewKnowledgeGraph(filepath.Join(t.TempDir(), ".openkitty"))
	if err != nil {
		t.Fatal(err)
	}
	defer kg.Close()
	content := strings.Repeat("字", 8001)
	if err := kg.AddVerbatim("long quote", content, "manual"); err != nil {
		t.Fatal(err)
	}
	if err := kg.AddTriple("long fact", "contains", content, "manual", "", "", 1); err != nil {
		t.Fatal(err)
	}
	if _, err := kg.db.Exec("PRAGMA query_only = ON"); err != nil {
		t.Fatal(err)
	}
	for _, quotes := range []bool{true, false} {
		items, count, err := kg.WorkspaceKnowledgeArchive(context.Background(), nil, quotes, "", 0, 20)
		if err != nil || count != 1 || len(items) != 1 || !items[0].ContentTruncated {
			t.Fatal("bounded reader omitted truncation state", quotes, count, err)
		}
		value := items[0].Content
		if !quotes {
			value = items[0].Object
		}
		if len([]rune(value)) != 8000 {
			t.Fatal("archive limit is not bounded in characters", quotes, len([]rune(value)))
		}
	}
}
