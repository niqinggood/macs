package app

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"

	memorycore "mac/internal/memory"
)

type provenanceViewResponse struct {
	Entries         []memorycore.Entry
	EntryProvenance []memorycore.RecordProvenance `json:"entry_provenance"`
	Supplemental    []memorycore.WorkingReference
}

func readProvenanceView(t *testing.T, g *GatewayService, query string) provenanceViewResponse {
	t.Helper()
	w := memoryScopeRequest(t, g, http.MethodGet, "/api/memory?agent=first&"+query, nil)
	var result provenanceViewResponse
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &result) != nil {
		t.Fatalf("provenance view failed: %d %s", w.Code, w.Body.String())
	}
	return result
}

func TestPersonalMemoryProvenanceDistinguishesEventInputFromOriginalQuoteAndTask(t *testing.T) {
	g := memorySourceFixture(t)
	g.engine.agentFactory.agents["first"] = &LLMAgent{workspace: g.currentProjectWorkspace(), hasMemory: true}
	event := memorycore.MemoryEvent{TenantID: "default", AgentName: "first", UserID: "fixture-user", SessionID: "source-web", Status: "success",
		UserQuestion: "I prefer this PROCESSED_ONLY instruction", FinalAnswer: "A recorded answer", LearnNote: "A model-derived insight"}
	if err := g.db.Create(&event).Error; err != nil {
		t.Fatal(err)
	}
	knowledge := memorycore.Knowledge{TenantID: "default", AgentName: "first", Title: "A derived lesson", Summary: "An existing learning row", SourceID: strconv.FormatUint(uint64(event.ID), 10), KnowledgeID: "fixture-provenance-lesson"}
	if err := g.db.Create(&knowledge).Error; err != nil {
		t.Fatal(err)
	}
	long := readProvenanceView(t, g, "view=long")
	if len(long.Supplemental) != 2 {
		t.Fatal("expected real existing insight and learning rows", long)
	}
	for _, ref := range long.Supplemental {
		p := ref.Provenance
		if p == nil || p.Identity != "verified" || p.Content != "model_generated" || p.Evidence.UserQuote != "missing" || p.Evidence.TaskLink != "missing" || p.Evidence.EventLink != "verified" || p.Origin.EventID != strconv.FormatUint(uint64(event.ID), 10) || p.Recall.Eligibility != "conditional" || p.Recall.Enabled == nil || !*p.Recall.Enabled {
			t.Fatalf("processed input was promoted or source lost: %#v", p)
		}
	}
	if prefs := readProvenanceView(t, g, "view=preferences"); len(prefs.Supplemental) != 0 {
		t.Fatal("event input fabricated an original user preference")
	}
	message := ChatMessage{TenantID: "default", AgentName: "first", UserId: "fixture-user", SessionID: "source-web", Role: "user", Content: "I prefer concise conclusions every time.", TaskID: "original-web-task"}
	if err := g.db.Create(&message).Error; err != nil {
		t.Fatal(err)
	}
	if err := g.db.Create(&GatewayTaskRecord{TenantID: "default", TaskID: message.TaskID, SessionID: message.SessionID, UserID: message.UserId,
		AgentName: "first", ResolvedWorkspace: g.currentProjectWorkspace(), Status: "completed"}).Error; err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := g.db.DB()
	sqlDB.SetMaxOpenConns(1)
	if err := g.db.Exec("PRAGMA query_only = ON").Error; err != nil {
		t.Fatal(err)
	}
	prefs := readProvenanceView(t, g, "view=preferences")
	if len(prefs.Supplemental) != 1 {
		t.Fatal("real raw preference missing", prefs)
	}
	p := prefs.Supplemental[0].Provenance
	if p == nil || p.Evidence.UserQuote != "verified" || p.Evidence.VerifiedQuote != message.Content || p.Evidence.TaskLink != "verified" || p.Origin.MessageID != strconv.FormatUint(uint64(message.ID), 10) || p.Origin.TaskID != "original-web-task" || p.Origin.Location != "chat_messages" || p.Content != "user_statement" {
		t.Fatalf("exact original quote and task were not traced: %#v", p)
	}
}

func TestPersonalMemoryProvenanceEntryQuoteNeedsExactAuthorizedSessionOwner(t *testing.T) {
	for _, test := range []struct {
		name, user, workspace, media, tenant, agent string
		verified                                    bool
	}{
		{name: "same_owner", verified: true}, {name: "foreign_user", user: "other"}, {name: "foreign_workspace", workspace: "foreign"},
		{name: "foreign_media", media: "someone-else"}, {name: "foreign_tenant", tenant: "other"}, {name: "foreign_agent", agent: "second"},
	} {
		t.Run(test.name, func(t *testing.T) {
			g := memorySourceFixture(t)
			user, workspace, tenant, agent := "fixture-user", g.currentProjectWorkspace(), "default", "first"
			if test.user != "" {
				user = test.user
			}
			if test.workspace != "" {
				workspace = t.TempDir()
			}
			if test.tenant != "" {
				tenant = test.tenant
			}
			if test.agent != "" {
				agent = test.agent
			}
			session := ChatSession{ID: "quote-session", UserId: "fixture-user", Workspace: workspace, AgentName: "first", TenantID: "default", ProjectID: g.project.ProjectID}
			if err := g.db.Create(&session).Error; err != nil {
				t.Fatal(err)
			}
			quote := "a specific original preference quote"
			if err := g.db.Create(&ChatMessage{SessionID: session.ID, TenantID: tenant, AgentName: agent, UserId: user, MediaOwner: test.media, Role: "user", Content: quote}).Error; err != nil {
				t.Fatal(err)
			}
			service := memorycore.NewService(g.db, g.currentProjectWorkspace(), "first")
			owner := memorycore.Owner{TenantID: "default", PartitionID: "default", SessionID: session.ID, ImportLegacy: false}
			if err := service.CreateWithSource(context.Background(), owner, "manually-saved", "A user saved value", memorycore.MemoryUser, "fact", 5, nil,
				memorycore.WriteSource{Type: "user_explicit", Ref: "local-memory-manager", Quote: quote}); err != nil {
				t.Fatal(err)
			}
			result := readProvenanceView(t, g, "view=long")
			if len(result.EntryProvenance) != 1 {
				t.Fatal("entry provenance array missing", result)
			}
			p := result.EntryProvenance[0]
			if p.RecordKind != "entry" || p.ID != result.Entries[0].ID || p.Origin.RecordID != p.ID || p.Origin.Location != "memory_entries" || p.Content != "user_saved" || p.Identity != "verified" || p.Version != result.Entries[0].Version || p.Recall.Eligibility != "conditional" {
				t.Fatal("entry identity was confused with content or state", p)
			}
			if (p.Evidence.UserQuote == "verified") != test.verified || (!test.verified && p.Origin.MessageID != "") {
				t.Fatal("foreign quote provided provenance evidence", p)
			}
			if !test.verified && p.Evidence.UserQuote != "stored_unverified" {
				t.Fatal("unverified stored quote was hidden as absent", p)
			}
			if test.verified && p.Evidence.VerifiedQuote != quote || !test.verified && p.Evidence.VerifiedQuote != "" {
				t.Fatal("exact matched quote was replaced by unsupported stored text", p)
			}
		})
	}
}

func TestPersonalMemoryProvenanceReturnsOnlyTheQuoteActuallyMatched(t *testing.T) {
	for _, test := range []struct{ name, source, confirmation, matched string }{
		{name: "real_source_fake_confirmation", source: "A real original user statement", confirmation: "A different unsupported confirmation quote", matched: "A real original user statement"},
		{name: "fake_source_real_confirmation", source: "An unsupported processed input quote", confirmation: "A real original user statement", matched: "A real original user statement"},
		{name: "neither_is_raw", source: "An unsupported processed input quote", confirmation: "A different unsupported confirmation quote"},
	} {
		t.Run(test.name, func(t *testing.T) {
			g := memorySourceFixture(t)
			message := ChatMessage{TenantID: "default", AgentName: "first", UserId: "fixture-user", SessionID: "source-web", Role: "user", Content: "A real original user statement"}
			if err := g.db.Create(&message).Error; err != nil {
				t.Fatal(err)
			}
			service := memorycore.NewService(g.db, g.currentProjectWorkspace(), "first")
			owner := memorycore.Owner{TenantID: "default", PartitionID: "default", SessionID: "source-web", ImportLegacy: false}
			if err := service.RecordWithSource(context.Background(), owner, "quoted-candidate", "A claim requiring review", memorycore.MemoryUser, "fact", "candidate", 5, nil, 0,
				memorycore.WriteSource{Type: "agent_candidate", Ref: "fixture-extraction", Quote: test.source}); err != nil {
				t.Fatal(err)
			}
			if _, err := service.ConfirmWithSource(context.Background(), owner, "quoted-candidate", memorycore.MemoryUser, 1, memorycore.WriteSource{Ref: "local-memory-manager", Quote: test.confirmation}); err != nil {
				t.Fatal(err)
			}
			result := readProvenanceView(t, g, "view=long")
			if len(result.EntryProvenance) != 1 || result.Entries[0].SourceQuote != test.source || result.Entries[0].ConfirmedQuote != test.confirmation {
				t.Fatal("original metadata was rewritten", result)
			}
			p := result.EntryProvenance[0]
			if p.Content != "user_confirmed" || p.Evidence.VerifiedQuote != test.matched || (p.Evidence.UserQuote == "verified") != (test.matched != "") {
				t.Fatalf("display quote is not the exact legal original: %#v", p)
			}
		})
	}
}

func TestPersonalMemoryProvenanceDoesNotLinkForeignTaskOrActivateOtherAgents(t *testing.T) {
	g := memorySourceFixture(t)
	g.engine.agentFactory.agents["first"] = &LLMAgent{workspace: g.currentProjectWorkspace()}
	g.engine.agentFactory.agents["second"] = &LLMAgent{workspace: g.currentProjectWorkspace(), hasMemory: true}
	if err := g.db.Create(&ChatMessage{TenantID: "default", AgentName: "first", UserId: "fixture-user", SessionID: "source-web", Role: "user", Content: "I prefer a clear summary", TaskID: "foreign-task-id"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := g.db.Create(&GatewayTaskRecord{TenantID: "default", TaskID: "foreign-task-id", SessionID: "source-web", AgentName: "first", UserID: "other", ResolvedWorkspace: g.currentProjectWorkspace()}).Error; err != nil {
		t.Fatal(err)
	}
	prefs := readProvenanceView(t, g, "view=preferences")
	p := prefs.Supplemental[0].Provenance
	if p.Evidence.UserQuote != "verified" || p.Evidence.TaskLink != "missing" || p.Origin.TaskID != "" {
		t.Fatal("foreign task joined through a message task_id", p)
	}
	owner := memorycore.Owner{TenantID: "default", PartitionID: "default", ImportLegacy: false}
	if err := memorycore.NewService(g.db, g.currentProjectWorkspace(), "second").CreateWithSource(context.Background(), owner, "saved-other-agent", "Only this source agent can recall me", memorycore.MemoryUser, "fact", 5, nil, memorycore.WriteSource{Type: "user_explicit"}); err != nil {
		t.Fatal(err)
	}
	global := readProvenanceView(t, g, "view=global")
	if len(global.Supplemental) != 1 || global.Supplemental[0].Provenance == nil {
		t.Fatal("expected real other-Agent entry source")
	}
	p = global.Supplemental[0].Provenance
	if p.RecordKind != "source" || p.Recall.Eligibility != "agent_only" || p.Content != "user_saved" || !p.ReadOnly || p.Recall.Enabled == nil || !*p.Recall.Enabled {
		t.Fatal("global view activated or mislabeled an Agent's private entry", p)
	}
	data, _ := json.Marshal(p)
	if strings.Contains(string(data), "foreign-task-id") {
		t.Fatal("foreign task leaked")
	}
}
