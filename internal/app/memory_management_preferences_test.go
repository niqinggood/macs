package app

import (
	"context"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	memorycore "mac/internal/memory"
)

func TestPersonalMemoryStructuredPreferencesVisibleWithoutActivation(t *testing.T) {
	g := memorySourceFixture(t)
	kg, err := memorycore.NewKnowledgeGraph(filepath.Join(g.currentProjectWorkspace(), ".openkitty"))
	if err != nil {
		t.Fatal(err)
	}
	previous := globalKG
	globalKG = kg
	t.Cleanup(func() { globalKG = previous; _ = kg.Close() })
	for _, kind := range []string{"tenant", "workspace", "media", "agentos"} {
		session := ChatSession{ID: "foreign-" + kind, AgentName: "first", TenantID: "default", Workspace: g.currentProjectWorkspace(), ProjectID: g.project.ProjectID}
		switch kind {
		case "tenant":
			session.TenantID = "foreign"
		case "workspace":
			session.Workspace = t.TempDir()
		case "media":
			session.MediaOwner = "foreign"
		}
		if err := g.db.Create(&session).Error; err != nil {
			t.Fatal(err)
		}
		if err := kg.AddTriple("FORBIDDEN_"+kind, "prefers", "FORBIDDEN_PREFERENCE", memorycore.ConversationSessionSource(session.ID), "", "", 0.8); err != nil {
			t.Fatal(err)
		}
	}
	if err := g.db.Create(&GatewayTaskRecord{TaskID: "preference-agentos", SessionID: "foreign-agentos", QueueName: agentOSQueueName}).Error; err != nil {
		t.Fatal(err)
	}
	for _, row := range []struct{ subject, predicate, source, validTo string }{
		{"historical customer", "prefers", "conversation", ""},
		{"original task customer", "prefers_report_format", memorycore.ConversationSessionSource("source-web"), ""},
		{"expired customer", "prefers_style", "conversation", "2020-01-01"},
		{"unrelated", "has_capability", "conversation", ""},
	} {
		if err := kg.AddTriple(row.subject, row.predicate, "saved original preference content", row.source, "", row.validTo, 0.8); err != nil {
			t.Fatal(err)
		}
	}
	if err := g.db.Exec("PRAGMA query_only=ON").Error; err != nil {
		t.Fatal(err)
	}
	result, body := memoryViewRead(t, g, "view=preferences")
	if len(result.Supplemental) != 3 || result.Total != 0 || len(result.Entries) != 0 || strings.Contains(body, "FORBIDDEN_") || strings.Contains(body, "has_capability") {
		t.Fatal("saved preferences hidden, promoted, or foreign material exposed", body)
	}
	expired := false
	for _, ref := range result.Supplemental {
		if !ref.ReadOnly || ref.Kind != "preference" || ref.Graph == nil || ref.Graph.Object != "saved original preference content" ||
			ref.OwnershipVerified == nil || *ref.OwnershipVerified || ref.Provenance == nil || ref.Provenance.Recall.Eligibility != "archive_only" || ref.CreatedAt == nil {
			t.Fatal("structured preference lost original fields or was claimed active", ref)
		}
		if ref.Graph.Subject == "expired customer" {
			expired = ref.Graph.ValidTo == "2020-01-01"
		}
	}
	if !expired {
		t.Fatal("expired preference date was lost")
	}
	if facts, err := kg.ConversationFactSources(context.Background(), []string{"source-web"}, 100); err != nil || len(facts) != 0 {
		t.Fatal("viewing preferences changed recall", len(facts), err)
	}
	var count int64
	if err := g.db.Table("memory_entries").Count(&count).Error; err != nil || count != 0 {
		t.Fatal("archive generated active entries", count, err)
	}
	for _, mode := range []string{"state=candidate", "search=unmatched-archive"} {
		filtered, body := memoryViewRead(t, g, "view=preferences&"+mode)
		if len(filtered.Supplemental) != 0 {
			t.Fatal("preference archive ignored search/state", body)
		}
	}
	foreignKG, err := memorycore.NewKnowledgeGraph(filepath.Join(t.TempDir(), ".openkitty"))
	if err != nil {
		t.Fatal(err)
	}
	defer foreignKG.Close()
	globalKG = foreignKG
	if err := foreignKG.AddTriple("foreign", "prefers", "FORBIDDEN_WORKSPACE", "manual", "", "", 1); err != nil {
		t.Fatal(err)
	}
	if w := memoryScopeRequest(t, g, http.MethodGet, "/api/memory?agent=first&view=preferences", nil); w.Code != 200 || strings.Contains(w.Body.String(), "FORBIDDEN_WORKSPACE") {
		t.Fatal("another workspace graph entered preference view", w.Code)
	}
}
