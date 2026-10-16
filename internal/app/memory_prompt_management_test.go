package app

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	memorycore "mac/internal/memory"
	knowledgetools "mac/internal/tools/knowledge"
)

func managementSourceContents(response promptSourceResponse) []string {
	values := make([]string, 0, len(response.Sources))
	for _, source := range response.Sources {
		values = append(values, source.SourceLabel+"|"+response.CurrentSources[source.ID].Content)
	}
	sort.Strings(values)
	return values
}

func TestPersonalPromptManagementDoesNotDependOnLatestConversation(t *testing.T) {
	g := memorySourceFixture(t)
	promptSourceReadRuntime(t, g, true)
	workspace := g.currentProjectWorkspace()
	second := ChatSession{ID: "another-local-user", AgentName: "first", UserId: "another-user", TenantID: "default", Workspace: workspace, ProjectID: g.project.ProjectID}
	if err := g.db.Create(&second).Error; err != nil {
		t.Fatal(err)
	}
	for _, session := range []struct{ id, user string }{{"source-web", "fixture-user"}, {second.ID, second.UserId}} {
		webMemoryTestEvent(t, g, session.id, session.user, "owned task "+session.id, "RESULT_"+session.id)
		if err := g.db.Create(&ChatMessage{TenantID: "default", AgentName: "first", UserId: session.user, SessionID: session.id, Role: "user", Content: "CURRENT_DIALOG_" + session.id}).Error; err != nil {
			t.Fatal(err)
		}
	}
	service := g.promptInventoryService("first")
	g.engine.agentFactory.agents["first"].(*LLMAgent).memoryService = service
	owner := memorycore.Owner{TenantID: "default", PartitionID: "default", ImportLegacy: false}
	for _, scope := range []memorycore.MemoryScope{memorycore.MemoryUser, memorycore.MemoryProject} {
		if err := service.Record(context.Background(), owner, string(scope), "SAVED_"+string(scope), scope, "fact", "active", 5, nil, 0); err != nil {
			t.Fatal(err)
		}
	}
	if err := memorycore.NewGlobalService(g.db, workspace).CreateWithSource(context.Background(), owner, "global", "SAVED_global", memorycore.MemoryGlobal, "fact", 5, nil, memorycore.WriteSource{Type: "user_explicit"}); err != nil {
		t.Fatal(err)
	}
	owner.SessionID = "source-web"
	if err := service.CreateSessionNote(context.Background(), owner, "temporary", "SESSION_ONLY_NOTE", "fact"); err != nil {
		t.Fatal(err)
	}
	if err := service.SaveSummary(context.Background(), owner, "SAVED_SUMMARY"); err != nil {
		t.Fatal(err)
	}
	contextStore := memorycore.NewSessionContextManager(filepath.Join(workspace, ".openkitty", "context"))
	contextStore.SetWorkspace(workspace)
	if err := contextStore.Set("temporary", "SESSION_CONTEXT", "fact", 5, owner.SessionID); err != nil {
		t.Fatal(err)
	}
	if err := service.SetContextStore(contextStore); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "AGENTS.md"), []byte("PROJECT_INSTRUCTIONS"), 0600); err != nil {
		t.Fatal(err)
	}
	notes := knowledgetools.LearningMemoryDirectory(workspace)
	if err := os.MkdirAll(notes, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(notes, "lesson.md"), []byte("---\nname: Saved note\nmetadata:\n  source: learn_tool\n---\n\nSAVED_LEARNING_NOTE"), 0600); err != nil {
		t.Fatal(err)
	}
	path := "/api/memory/prompt-sources?agent=first&mode=management&include_learning=1"
	first := readPromptSourceView(t, g, path)
	if w := memoryScopeRequest(t, g, http.MethodGet, path, nil); strings.Contains(w.Body.String(), "\"id\":\"session_history\"") {
		t.Fatal("management still advertises current conversation history")
	}
	values := strings.Join(managementSourceContents(first), "\n")
	for _, expected := range []string{"RESULT_source-web", "RESULT_another-local-user", "SAVED_user", "SAVED_project", "SAVED_global", "SAVED_SUMMARY", "PROJECT_INSTRUCTIONS", "SAVED_LEARNING_NOTE"} {
		if !strings.Contains(values, expected) {
			t.Fatalf("management omitted authorized saved content: %s", expected)
		}
	}
	for _, excluded := range []string{"CURRENT_DIALOG_", "SESSION_ONLY_NOTE", "SESSION_CONTEXT"} {
		if strings.Contains(values, excluded) {
			t.Fatalf("management includes current conversation state: %s", excluded)
		}
	}
	if first.SessionID != "" || g.promptSourceViews[first.ViewID].SessionID != "" {
		t.Fatal("management bound itself to a current conversation")
	}
	for index, sessionID := range []string{"source-web", second.ID} {
		if err := g.db.Model(&ChatSession{}).Where("id = ?", sessionID).Update("updated_at", time.Now().Add(time.Duration(index+1)*time.Hour)).Error; err != nil {
			t.Fatal(err)
		}
		after := readPromptSourceView(t, g, path)
		if after.SessionID != "" || !reflect.DeepEqual(managementSourceContents(first), managementSourceContents(after)) {
			t.Fatal("changing the latest conversation changed the memory management inventory")
		}
	}
	// Management keeps both authorized originals; runtime still follows the
	// actual request user's audience and cannot use another user's histories.
	ctx, _ := webMemoryTestContext(t, g, "source-web", "fixture-user")
	items, err := webMemoryTestAgent(g).webMemorySources(ctx, "source-web")
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range items {
		if item.SourceSessionID == second.ID {
			t.Fatal("management expanded runtime recall to another user")
		}
	}
	legacy := readPromptSourceView(t, g, "/api/memory/prompt-sources?agent=first")
	if legacy.SessionID != second.ID {
		t.Fatal("legacy latest-conversation API changed")
	}
	legacy = readPromptSourceView(t, g, "/api/memory/prompt-sources?agent=first&session_id=source-web")
	if legacy.SessionID != "source-web" || !strings.Contains(strings.Join(managementSourceContents(legacy), "\n"), "CURRENT_DIALOG_source-web") {
		t.Fatal("explicit conversation inventory compatibility changed")
	}
}

func TestPersonalPromptManagementRejectsForeignSourcesAndMixedContext(t *testing.T) {
	g := memorySourceFixture(t)
	promptSourceReadRuntime(t, g, true)
	webMemoryTestEvent(t, g, "source-web", "fixture-user", "owned", "OWNED_RESULT")
	for _, kind := range []string{"tenant", "subject", "agent", "project", "workspace", "media", "agentos"} {
		session := ChatSession{ID: "foreign-" + kind, AgentName: "first", UserId: "foreign-user", TenantID: "default", Workspace: g.currentProjectWorkspace(), ProjectID: g.project.ProjectID}
		switch kind {
		case "tenant":
			session.TenantID = "foreign"
		case "subject":
			session.SubjectID = "foreign"
		case "agent":
			session.AgentName = "second"
		case "project":
			session.ProjectID = "foreign"
		case "workspace":
			session.Workspace = t.TempDir()
		case "media":
			session.MediaOwner = "foreign"
		}
		if err := g.db.Create(&session).Error; err != nil {
			t.Fatal(err)
		}
		webMemoryTestEvent(t, g, session.ID, session.UserId, "foreign", "FORBIDDEN_"+kind)
	}
	if err := g.db.Create(&GatewayTaskRecord{TaskID: "management-agentos", SessionID: "foreign-agentos", QueueName: agentOSQueueName}).Error; err != nil {
		t.Fatal(err)
	}
	webMemoryTestEvent(t, g, "source-web", "wrong-user", "forged owner", "FORBIDDEN_OWNER")
	response := readPromptSourceView(t, g, "/api/memory/prompt-sources?agent=first&mode=management")
	values := strings.Join(managementSourceContents(response), "\n")
	if !strings.Contains(values, "OWNED_RESULT") || strings.Contains(values, "FORBIDDEN_") {
		t.Fatal("management lost owned data or exposed a foreign source")
	}
	for _, query := range []string{"mode=management&session_id=source-web", "mode=management&session_id=", "mode=unknown", "mode=management&mode=management"} {
		if w := memoryScopeRequest(t, g, http.MethodGet, "/api/memory/prompt-sources?agent=first&"+query, nil); w.Code != http.StatusBadRequest {
			t.Fatalf("mixed or invalid management context accepted: %s status=%d", query, w.Code)
		}
	}
	if err := g.db.Exec("PRAGMA query_only=ON").Error; err != nil {
		t.Fatal(err)
	}
	readPromptSourceView(t, g, "/api/memory/prompt-sources?agent=first&mode=management")
}

func TestPersonalPromptManagementEditsRevalidateOriginalOwnerAndRevision(t *testing.T) {
	g := memorySourceFixture(t)
	promptSourceReadRuntime(t, g, true)
	event := webMemoryTestEvent(t, g, "source-web", "fixture-user", "owned", "ORIGINAL_BODY")
	response := readPromptSourceView(t, g, "/api/memory/prompt-sources?agent=first&mode=management")
	var source personalPromptHistorySource
	for _, item := range response.Sources {
		if item.Kind == "task_execution" {
			source = item
		}
	}
	if source.ID == "" {
		t.Fatal("missing editable source")
	}
	action := map[string]interface{}{"agent": "first", "view_id": response.ViewID, "source_id": source.ID, "action": "update", "expected_revision": response.CurrentSources[source.ID].Revision, "value": "UPDATED_BODY"}
	w := memoryScopeRequest(t, g, http.MethodPost, "/api/memory/prompt-sources", action)
	if w.Code != http.StatusOK {
		t.Fatalf("management cannot edit the original source: %d %s", w.Code, w.Body.String())
	}
	if w := memoryScopeRequest(t, g, http.MethodPost, "/api/memory/prompt-sources", action); w.Code != http.StatusConflict {
		t.Fatal("management accepted a stale original revision", w.Code)
	}
	action["expected_revision"] = promptSourceDigest("UPDATED_BODY")
	if err := g.db.Model(&ChatSession{}).Where("id = ?", "source-web").Update("user_id", "changed-owner").Error; err != nil {
		t.Fatal(err)
	}
	if w := memoryScopeRequest(t, g, http.MethodPost, "/api/memory/prompt-sources", action); w.Code == http.StatusOK {
		t.Fatal("management edit crossed the original user's ownership boundary")
	}
	if err := g.db.First(&event, event.ID).Error; err != nil || event.FinalAnswer != "UPDATED_BODY" {
		t.Fatal("rejected edit modified original source", err)
	}
}
