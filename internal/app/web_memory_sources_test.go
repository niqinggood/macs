package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"gorm.io/datatypes"
	memorycore "mac/internal/memory"
)

func webMemoryTestContext(t *testing.T, g *GatewayService, sessionID, userID string) (context.Context, *webMemoryAudience) {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "http://localhost:8090/api/chat", nil)
	r.RemoteAddr = "127.0.0.1:40000"
	r.Header.Set("Origin", "http://localhost:8090")
	request := &MessageRequest{SessionId: sessionID, UserId: userID}
	audience := g.captureWebMemoryAudience(r, request, g.currentProjectWorkspace())
	if audience == nil {
		t.Fatal("host failed to freeze source audience")
	}
	ctx := context.WithValue(context.Background(), webMemoryAudienceKey{}, audience)
	ctx = context.WithValue(ctx, "session_id", sessionID)
	ctx = context.WithValue(ctx, "channel", "web")
	return ctx, audience
}

func webMemoryTestAgent(g *GatewayService) *LLMAgent {
	workspace := g.currentProjectWorkspace()
	return &LLMAgent{name: "first", workspace: workspace, hasMemory: true,
		memory:        memorycore.NewMemoryStore(filepath.Join(workspace, ".openkitty", "memories", "first"), g.db, "first", 10),
		memoryService: memorycore.NewService(g.db, workspace, "first", memorycore.WithRecallEmbedding(func() memorycore.RecallEmbedding { return memorycore.RecallEmbedding{} }))}
}

func webMemoryTestEvent(t *testing.T, g *GatewayService, sessionID, userID, question, answer string) memorycore.MemoryEvent {
	t.Helper()
	event := memorycore.MemoryEvent{TenantID: "default", AgentName: "first", UserID: userID, UserName: userID, UserChannel: "web", SessionID: sessionID,
		UserQuestion: question, FinalAnswer: answer, Status: "success",
		CallChain: datatypes.JSON(`[{"type":"tool","name":"read_file","success":true}]`)}
	if err := g.db.Create(&event).Error; err != nil {
		t.Fatal(err)
	}
	return event
}

func TestWebMemorySourcesRejectUnverifiedOriginalLocator(t *testing.T) {
	g := memorySourceFixture(t)
	agent := webMemoryTestAgent(g)
	ctx, audience := webMemoryTestContext(t, g, "source-web", "fixture-user")
	event := webMemoryTestEvent(t, g, "source-web", "fixture-user", "quartz task", "quartz ORIGINAL_RESULT")
	ref := memorycore.WorkingReference{ID: "event-reference", SourceType: "task_execution", SourceID: strconv.FormatUint(uint64(event.ID), 10),
		SessionID: event.SessionID, Key: event.UserQuestion, Value: event.FinalAnswer}
	valid, ok := agent.webMemoryReferenceItem(ctx, audience, ref)
	if !ok || memorycore.SourceRecallKey(valid.Locator) == "" {
		t.Fatal("authorized original source was rejected")
	}
	owner, err := agent.memoryOwner(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := agent.memoryService.SetSourceRecallEnabled(ctx, owner, valid.Locator, false, 0); err != nil {
		t.Fatal(err)
	}
	for _, failure := range []string{"missing-audience", "missing-id", "missing-session", "changed-original", "foreign-user", "missing-row"} {
		t.Run(failure, func(t *testing.T) {
			badRef, badAudience := ref, audience
			switch failure {
			case "missing-audience":
				badAudience = nil
			case "missing-id":
				badRef.SourceID = ""
			case "missing-session":
				badRef.SessionID = ""
			case "changed-original":
				badRef.Value = "quartz stale excerpt"
			case "foreign-user":
				copy := *audience
				copy.userID = "another-user"
				badAudience = &copy
			case "missing-row":
				badRef.SourceID = "9999999"
			}
			item, accepted := agent.webMemoryReferenceItem(ctx, badAudience, badRef)
			if accepted || item.Content != "" {
				t.Fatal("failed original validation became an unaddressed recall candidate")
			}
		})
	}
	// Valid references still pass through the domain setting gate, so an
	// invalid-source fallback cannot restore their old contents either.
	result, err := agent.memoryService.Recall(ctx, owner, "quartz", 2000, []memorycore.RecallItem{valid})
	if err != nil || result.Prompt != "" || len(result.Items) != 0 {
		t.Fatalf("disabled verified original bypassed setting: %v", err)
	}
}

func TestWebMemorySourcesPrivateAudienceAndFrozenAccount(t *testing.T) {
	g := memorySourceFixture(t)
	if err := g.db.Model(&ChatSession{}).Where("id = ?", "source-web").Update("media_owner", "accepted-account").Error; err != nil {
		t.Fatal(err)
	}
	webMemoryTestEvent(t, g, "source-web", "fixture-user", "quartz accepted", "ACCEPTED_BODY")
	r := httptest.NewRequest(http.MethodPost, "http://localhost:8090/api/chat", nil)
	request := &MessageRequest{SessionId: "source-web", UserId: "fixture-user"}
	audience := g.captureWebMemoryAudience(r, request, g.currentProjectWorkspace())
	if audience == nil {
		t.Fatal("missing accepted audience")
	}
	// This stands in for a host-resolved account fingerprint; no actual login
	// or credential is created. The active account handler now resolves empty.
	audience.mediaOwner = "accepted-account"
	request.UserId, request.SessionId = "another-user", "another-session"
	serialized, err := json.Marshal(audience)
	if err != nil || string(serialized) != "{}" || audience.userID != "fixture-user" || audience.sessionID != "source-web" {
		t.Fatal("audience is serializable or aliases mutable request fields")
	}
	ctx := context.WithValue(context.Background(), webMemoryAudienceKey{}, audience)
	if g.mediaOwner(r) != "" || g.personalMemoryMediaOwner(r.WithContext(ctx)) != "accepted-account" {
		t.Fatal("source audience switched to the currently active account")
	}
	items, err := webMemoryTestAgent(g).webMemorySources(ctx, "source-web")
	if err != nil || len(items) != 1 || !strings.Contains(items[0].Content, "ACCEPTED_BODY") {
		t.Fatalf("accepted account not honored: count=%d err=%v", len(items), err)
	}
	for _, field := range []string{"session", "user", "project", "workspace", "media_owner", "gateway"} {
		t.Run(field, func(t *testing.T) {
			copy := *audience
			callSession := "source-web"
			switch field {
			case "session":
				callSession = "other-session"
			case "user":
				copy.userID = "another-user"
			case "project":
				copy.projectID = "another-project"
			case "workspace":
				copy.workspace = t.TempDir()
			case "media_owner":
				copy.mediaOwner = "another-account"
			case "gateway":
				copy.gateway = &GatewayService{}
			}
			items, err := webMemoryTestAgent(g).webMemorySources(context.WithValue(context.Background(), webMemoryAudienceKey{}, &copy), callSession)
			if err != nil || len(items) != 0 {
				t.Fatalf("changed audience obtained history: count=%d err=%v", len(items), err)
			}
		})
	}
	for _, request := range []*MessageRequest{nil, {}, {UserId: "fixture-user"}} {
		if g.captureWebMemoryAudience(r, request, g.currentProjectWorkspace()) != nil {
			t.Fatal("missing persistent session obtained an audience")
		}
	}
}

func TestWebMemorySourcesIndependentReferencesAndRelevance(t *testing.T) {
	g := memorySourceFixture(t)
	for _, content := range []string{"我偏好 quartz 风格。", "quartz 这个结果很好。"} {
		if err := g.db.Create(&ChatMessage{SessionID: "source-web", AgentName: "first", TenantID: "default", UserId: "fixture-user", Role: "user", Content: content}).Error; err != nil {
			t.Fatal(err)
		}
	}
	related := webMemoryTestEvent(t, g, "source-web", "fixture-user", "quartz task", "RELATED_RESULT")
	if err := g.db.Model(&memorycore.MemoryEvent{}).Where("id = ?", related.ID).Update("learn_note", "RELATED_INSIGHT").Error; err != nil {
		t.Fatal(err)
	}
	webMemoryTestEvent(t, g, "source-web", "fixture-user", "gardening roses", "UNRELATED_RESULT")
	knowledge := memorycore.Knowledge{TenantID: "default", AgentName: "first", KnowledgeID: "related-lesson", SourceID: strconv.FormatUint(uint64(related.ID), 10), Title: "quartz lesson", Summary: "RELATED_LEARNING", UseCount: 7}
	if err := g.db.Create(&knowledge).Error; err != nil {
		t.Fatal(err)
	}
	ctx, _ := webMemoryTestContext(t, g, "source-web", "fixture-user")
	agent := webMemoryTestAgent(g)
	items, err := agent.webMemorySources(ctx, "source-web")
	if err != nil {
		t.Fatal(err)
	}
	types, ids := map[string]int{}, map[string]bool{}
	for _, item := range items {
		types[item.Source]++
		if ids[item.ID] || item.Source == "history_and_profile" {
			t.Fatal("separate sources collapsed into a mixed adapter or duplicate ID")
		}
		ids[item.ID] = true
	}
	for _, source := range []string{"user_statement", "user_feedback", "task_execution", "task_insight", "learning_knowledge"} {
		if types[source] == 0 {
			t.Fatalf("independent source missing: %s", source)
		}
	}
	owner, err := agent.memoryOwner(ctx)
	if err != nil {
		t.Fatal(err)
	}
	owner.SessionID = "source-web"
	result, err := agent.memoryService.Recall(ctx, owner, "quartz", 2000, items)
	if err != nil || strings.Contains(result.Prompt, "UNRELATED_RESULT") || !strings.Contains(result.Prompt, "RELATED_RESULT") || !strings.Contains(result.Prompt, "RELATED_INSIGHT") || !strings.Contains(result.Prompt, "RELATED_LEARNING") {
		t.Fatalf("one related source admitted unrelated data or lost real evidence: %v", err)
	}
	var current memorycore.Knowledge
	if err := g.db.First(&current, knowledge.ID).Error; err != nil || current.UseCount != 7 {
		t.Fatal("automatic source adapter changed legacy usage count", err)
	}
	var count int64
	if err := g.db.Model(&memorycore.Entry{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatal("reading sources promoted a durable Entry", err)
	}
}

func TestWebMemorySourcesForeignSessionsAndEventsExcluded(t *testing.T) {
	g := memorySourceFixture(t)
	webMemoryTestEvent(t, g, "source-web", "fixture-user", "quartz permitted", "PERMITTED_RESULT")
	for _, session := range []ChatSession{
		{ID: "past-owned", AgentName: "first", UserId: "fixture-user", TenantID: "default", Workspace: g.currentProjectWorkspace(), ProjectID: g.project.ProjectID},
		{ID: "foreign-user", AgentName: "first", UserId: "other-user", TenantID: "default", Workspace: g.currentProjectWorkspace(), ProjectID: g.project.ProjectID},
		{ID: "foreign-agent", AgentName: "second", UserId: "fixture-user", TenantID: "default", Workspace: g.currentProjectWorkspace(), ProjectID: g.project.ProjectID},
		{ID: "foreign-tenant", AgentName: "first", UserId: "fixture-user", TenantID: "other", Workspace: g.currentProjectWorkspace(), ProjectID: g.project.ProjectID},
		{ID: "foreign-subject", AgentName: "first", UserId: "fixture-user", TenantID: "default", SubjectID: "other", Workspace: g.currentProjectWorkspace(), ProjectID: g.project.ProjectID},
		{ID: "foreign-workspace", AgentName: "first", UserId: "fixture-user", TenantID: "default", Workspace: t.TempDir(), ProjectID: g.project.ProjectID},
		{ID: "foreign-project", AgentName: "first", UserId: "fixture-user", TenantID: "default", Workspace: g.currentProjectWorkspace(), ProjectID: "other"},
		{ID: "foreign-account", AgentName: "first", UserId: "fixture-user", TenantID: "default", Workspace: g.currentProjectWorkspace(), ProjectID: g.project.ProjectID, MediaOwner: "other"},
		{ID: "agentos-session", AgentName: "first", UserId: "fixture-user", TenantID: "default", Workspace: g.currentProjectWorkspace(), ProjectID: g.project.ProjectID},
	} {
		if err := g.db.Create(&session).Error; err != nil {
			t.Fatal(err)
		}
		marker := "FOREIGN_RESULT"
		if session.ID == "past-owned" {
			marker = "OWNED_PAST_RESULT"
		}
		webMemoryTestEvent(t, g, session.ID, session.UserId, "quartz past", marker)
	}
	if err := g.db.Create(&GatewayTaskRecord{TaskID: "isolated-agentos", SessionID: "agentos-session", QueueName: agentOSQueueName}).Error; err != nil {
		t.Fatal(err)
	}
	for _, event := range []memorycore.MemoryEvent{
		{TenantID: "other", AgentName: "first", UserID: "fixture-user", UserChannel: "web", SessionID: "source-web"},
		{TenantID: "default", AgentName: "second", UserID: "fixture-user", UserChannel: "web", SessionID: "source-web"},
		{TenantID: "default", AgentName: "first", UserID: "other-user", UserChannel: "web", SessionID: "source-web"},
		{TenantID: "default", AgentName: "first", UserID: "fixture-user", UserChannel: "api", SessionID: "source-web"},
		{TenantID: "default", AgentName: "first", UserID: "fixture-user", UserChannel: "web", SessionID: "unbound"},
	} {
		event.UserQuestion, event.FinalAnswer, event.Status = "quartz foreign", "FOREIGN_RESULT", "success"
		if err := g.db.Create(&event).Error; err != nil {
			t.Fatal(err)
		}
	}
	ctx, _ := webMemoryTestContext(t, g, "source-web", "fixture-user")
	agent := webMemoryTestAgent(g)
	items, err := agent.webMemorySources(ctx, "source-web")
	if err != nil || len(items) != 2 {
		t.Fatalf("owned history count=%d err=%v", len(items), err)
	}
	encoded, _ := json.Marshal(items)
	if strings.Contains(string(encoded), "FOREIGN_RESULT") || !strings.Contains(string(encoded), "OWNED_PAST_RESULT") {
		t.Fatal("foreign history exposed or legitimate past session hidden")
	}
	for _, sessionID := range []string{"unbound", "foreign-user", "foreign-agent", "foreign-tenant", "foreign-subject", "foreign-workspace", "foreign-project", "foreign-account", "agentos-session"} {
		foreignContext, _ := webMemoryTestContext(t, g, sessionID, "fixture-user")
		items, err := agent.webMemorySources(foreignContext, sessionID)
		if err != nil || len(items) != 0 {
			t.Fatalf("unowned current session obtained historical context: %s count=%d err=%v", sessionID, len(items), err)
		}
	}
}

func TestWebMemorySourcesUnsealedWebKeepsEntriesWithoutLegacyFallback(t *testing.T) {
	g := memorySourceFixture(t)
	agent := webMemoryTestAgent(g)
	webMemoryTestEvent(t, g, "source-web", "fixture-user", "quartz legacy", "LEGACY_EVENT_BODY")
	if err := g.db.Create(&memorycore.UserProfile{TenantID: "default", UserName: "fixture-user", Constraints: datatypes.JSON(`{"preferred":["quartz PROFILE_BODY"]}`)}).Error; err != nil {
		t.Fatal(err)
	}
	if err := g.db.Create(&memorycore.ToolSkillStat{TenantID: "default", AgentName: "first", Type: "tool", Name: "quartz_STAT_BODY", TotalCount: 10, FailCount: 10, SuccessRate: 0}).Error; err != nil {
		t.Fatal(err)
	}
	kg, err := memorycore.NewKnowledgeGraph(filepath.Join(g.currentProjectWorkspace(), ".openkitty"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = kg.Close() })
	previous := globalKG
	globalKG = kg
	t.Cleanup(func() { globalKG = previous })
	if err := kg.AddTriple("quartz", "is", "LEGACY_GRAPH_BODY", "manual", "", "", 1); err != nil {
		t.Fatal(err)
	}
	legacy := agent.memory.GetMemoryContextForTenant("default", "fixture-user", "source-web", "quartz")
	if !strings.Contains(legacy, "PROFILE_BODY") || !strings.Contains(legacy, "LEGACY_EVENT_BODY") || !strings.Contains(legacy, "STAT_BODY") {
		t.Fatal("negative-control legacy sources were not readable")
	}
	ctx := context.WithValue(context.Background(), "channel", "web")
	ctx = context.WithValue(ctx, "session_id", "source-web")
	owner, err := agent.memoryOwner(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := agent.memoryService.Record(ctx, owner, "quartz saved", "OWNED_ENTRY_BODY", memorycore.MemoryProject, "fact", "active", 5, nil, 0); err != nil {
		t.Fatal(err)
	}
	prompt := agent.buildMemoryPrompt(ctx, "fixture-user", "source-web", "quartz")
	if !strings.Contains(prompt, "OWNED_ENTRY_BODY") {
		t.Fatal("unsealed Web also lost properly scoped durable memory")
	}
	for _, forbidden := range []string{"LEGACY_EVENT_BODY", "PROFILE_BODY", "STAT_BODY", "LEGACY_GRAPH_BODY"} {
		if strings.Contains(prompt, forbidden) {
			t.Fatal("client channel marker gained unsealed legacy fallback: " + forbidden)
		}
	}
	sealed, _ := webMemoryTestContext(t, g, "source-web", "fixture-user")
	prompt = agent.buildMemoryPrompt(sealed, "fixture-user", "source-web", "quartz")
	if !strings.Contains(prompt, "LEGACY_EVENT_BODY") || strings.Contains(prompt, "PROFILE_BODY") || strings.Contains(prompt, "STAT_BODY") || strings.Contains(prompt, "LEGACY_GRAPH_BODY") {
		t.Fatal("sealed Web did not separate authorized task evidence from old profile/stat/graph adapters")
	}
}

func TestWebMemorySourcesGraphRequiresCurrentVerifiedOriginalQuote(t *testing.T) {
	g := memorySourceFixture(t)
	kg, err := memorycore.NewKnowledgeGraph(filepath.Join(g.currentProjectWorkspace(), ".openkitty"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = kg.Close() })
	previous := globalKG
	globalKG = kg
	t.Cleanup(func() { globalKG = previous })
	quote := "quartz 项目采用蓝色方案。"
	past := ChatSession{ID: "past-graph", AgentName: "first", UserId: "fixture-user", TenantID: "default", Workspace: g.currentProjectWorkspace(), ProjectID: g.project.ProjectID}
	if err := g.db.Create(&past).Error; err != nil {
		t.Fatal(err)
	}
	for _, sessionID := range []string{"source-web", "past-graph"} {
		if err := g.db.Create(&ChatMessage{SessionID: sessionID, AgentName: "first", TenantID: "default", UserId: "fixture-user", Role: "user", Content: quote}).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		subject, source, evidence string
	}{
		{"CURRENT_VERIFIED", memorycore.ConversationSessionSource("source-web"), quote},
		{"OTHER_OWNED_SESSION", memorycore.ConversationSessionSource("past-graph"), quote},
		{"OLD_UNSCOPED_GRAPH", "conversation", quote},
		{"NO_RAW_QUOTE", memorycore.ConversationSessionSource("source-web"), "quartz fabricated claim without original evidence"},
	} {
		if err := kg.AddFactWithEvidence(context.Background(), memorycore.Fact{Subject: tc.subject, Predicate: "quartz uses", Object: "blue", Source: tc.source}, tc.evidence); err != nil {
			t.Fatal(err)
		}
	}
	if err := kg.AddTriple("MISSING_EVIDENCE", "quartz uses", "blue", memorycore.ConversationSessionSource("source-web"), "", "", 1); err != nil {
		t.Fatal(err)
	}
	ctx, _ := webMemoryTestContext(t, g, "source-web", "fixture-user")
	items, err := webMemoryTestAgent(g).webMemorySources(ctx, "source-web")
	if err != nil || len(items) != 1 || items[0].Source != "knowledge_fact" || !strings.Contains(items[0].Content, "CURRENT_VERIFIED") {
		t.Fatalf("graph source evidence/current-session fence failed: count=%d err=%v", len(items), err)
	}
	encoded, _ := json.Marshal(items)
	for _, forbidden := range []string{"OTHER_OWNED_SESSION", "OLD_UNSCOPED_GRAPH", "NO_RAW_QUOTE", "MISSING_EVIDENCE"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatal("unverified graph assertion became personal Web recall")
		}
	}
	// Removing the original message invalidates evidence without deleting or
	// relabeling the historical graph itself.
	if err := g.db.Where("session_id = ?", "source-web").Delete(&ChatMessage{}).Error; err != nil {
		t.Fatal(err)
	}
	items, err = webMemoryTestAgent(g).webMemorySources(ctx, "source-web")
	if err != nil || len(items) != 0 {
		t.Fatalf("removed original evidence was still trusted: count=%d err=%v", len(items), err)
	}
}
