package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gorm.io/datatypes"
	memorycore "mac/internal/memory"
)

type memoryViewTestResponse struct {
	View         string
	Entries      []memorycore.Entry
	Supplemental []memorycore.WorkingReference
	Sessions     []personalMemorySession
	Total        int64
	SourcesTotal *int64 `json:"sources_total"`
}

func memoryViewRead(t *testing.T, g *GatewayService, query string) (memoryViewTestResponse, string) {
	t.Helper()
	w := memoryScopeRequest(t, g, http.MethodGet, "/api/memory?agent=first&"+query, nil)
	var result memoryViewTestResponse
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &result) != nil {
		t.Fatalf("view failed: %s %d %s", query, w.Code, w.Body.String())
	}
	return result, w.Body.String()
}

func TestPersonalMemoryViewsSeparateWorkingStateExperiencePreferencesAndHistory(t *testing.T) {
	g := memorySourceFixture(t)
	workspace := g.currentProjectWorkspace()
	scm := memorycore.NewSessionContextManager(filepath.Join(workspace, ".openkitty", "context"))
	scm.SetWorkspace(workspace)
	if err := scm.Set("progress", "CURRENT_WORKING_STATE", "fact", 5, "source-web"); err != nil {
		t.Fatal(err)
	}
	g.engine.agentFactory.agents["first"] = &LLMAgent{workspace: workspace, scm: scm, hasMemory: true}
	service := memorycore.NewService(g.db, workspace, "first")
	owner := memorycore.Owner{TenantID: "default", PartitionID: "default", ImportLegacy: false}
	for _, entry := range []struct {
		key   string
		scope memorycore.MemoryScope
		kind  string
	}{
		{"durable-fact", memorycore.MemoryUser, "fact"}, {"durable-experience", memorycore.MemoryUser, "experience"},
		{"durable-preference", memorycore.MemoryUser, "preference"}, {"project-fact", memorycore.MemoryProject, "fact"},
	} {
		if err := service.Record(context.Background(), owner, entry.key, entry.key, entry.scope, entry.kind, "active", 5, nil, 0); err != nil {
			t.Fatal(err)
		}
	}
	owner.SessionID = "source-web"
	if err := service.CreateSessionNote(context.Background(), owner, "session-note", "SESSION_NOTE", "fact"); err != nil {
		t.Fatal(err)
	}
	if err := service.SaveSummary(context.Background(), owner, "REAL_SESSION_SUMMARY"); err != nil {
		t.Fatal(err)
	}
	if err := g.db.Create(&ChatMessage{TenantID: "default", AgentName: "first", UserId: "fixture-user", SessionID: "source-web", Role: "user", Content: "我偏好每次先写结论。"}).Error; err != nil {
		t.Fatal(err)
	}
	event := memorycore.MemoryEvent{TenantID: "default", AgentName: "first", SessionID: "source-web", UserID: "fixture-user", Status: "success", UserQuestion: "TASK_QUESTION", FinalAnswer: "TASK_RESULT", LearnNote: "DURABLE_TASK_INSIGHT"}
	if err := g.db.Create(&event).Error; err != nil {
		t.Fatal(err)
	}
	short, body := memoryViewRead(t, g, "view=short&session_id=source-web")
	if short.Total != 1 || len(short.Entries) != 1 || short.Entries[0].Key != "session-note" || len(short.Supplemental) != 2 || strings.Contains(body, "DURABLE_TASK_INSIGHT") || strings.Contains(body, "TASK_RESULT") {
		t.Fatal("short-term view includes durable experience or raw task history", body)
	}
	for _, ref := range short.Supplemental {
		if ref.SourceType != "session_context" && ref.SourceType != "summary" {
			t.Fatal("wrong short source", ref.SourceType)
		}
	}
	long, body := memoryViewRead(t, g, "view=long&limit=1&offset=1")
	if long.Total != 2 || len(long.Entries) != 1 || long.Entries[0].Kind == "preference" || len(long.Supplemental) != 1 || long.Supplemental[0].SourceType != "task_insight" || strings.Contains(body, "CURRENT_WORKING_STATE") || strings.Contains(body, "TASK_RESULT") {
		t.Fatal("long view still duplicates raw short/history sources", body)
	}
	prefs, body := memoryViewRead(t, g, "view=preferences")
	if prefs.Total != 1 || len(prefs.Entries) != 1 || prefs.Entries[0].Kind != "preference" || len(prefs.Supplemental) != 1 || prefs.Supplemental[0].SourceType != "user_statement" {
		t.Fatal("real explicit preferences missing", body)
	}
	project, body := memoryViewRead(t, g, "view=project")
	if project.Total != 1 || project.Entries[0].Scope != memorycore.MemoryProject || len(project.Supplemental) != 1 || strings.Contains(body, "TASK_RESULT") {
		t.Fatal("project view includes user or raw history content", body)
	}
	history, body := memoryViewRead(t, g, "view=history&session_id=source-web")
	if history.Total != 0 || len(history.Entries) != 0 || len(history.Supplemental) != 1 || history.Supplemental[0].SourceType != "task_execution" || strings.Contains(body, "DURABLE_TASK_INSIGHT") || strings.Contains(body, "REAL_SESSION_SUMMARY") {
		t.Fatal("history includes long-term or short-state sources", body)
	}
	for _, view := range []string{"short", "history"} {
		discovery, _ := memoryViewRead(t, g, "view="+view)
		if len(discovery.Sessions) != 1 || len(discovery.Entries) != 0 || len(discovery.Supplemental) != 0 {
			t.Fatal("unselected session view read unscoped state")
		}
	}
}

func TestPersonalMemoryViewsGlobalWorkspaceInstructionsAndAgentMemoriesAreReadOnly(t *testing.T) {
	g := memorySourceFixture(t)
	workspace := g.currentProjectWorkspace()
	g.engine.agentFactory.agents["first"] = &LLMAgent{workspace: workspace}
	g.engine.agentFactory.agents["second"] = &LLMAgent{workspace: workspace}
	owner := memorycore.Owner{TenantID: "default", PartitionID: "default", ImportLegacy: false}
	for _, name := range []string{"first", "second"} {
		if err := memorycore.NewService(g.db, workspace, name).Record(context.Background(), owner, name+"-private", "PRIVATE_OF_"+name, memorycore.MemoryUser, "fact", "active", 5, nil, 0); err != nil {
			t.Fatal(err)
		}
	}
	foreign := owner
	foreign.SubjectID = "other"
	if err := memorycore.NewService(g.db, workspace, "second").Record(context.Background(), foreign, "foreign", "FOREIGN_MUST_NOT_BE_EXPOSED", memorycore.MemoryUser, "fact", "active", 5, nil, 0); err != nil {
		t.Fatal(err)
	}
	if err := memorycore.NewService(g.db, t.TempDir(), "second").Record(context.Background(), owner, "elsewhere", "ELSEWHERE_MUST_NOT_BE_EXPOSED", memorycore.MemoryUser, "fact", "active", 5, nil, 0); err != nil {
		t.Fatal(err)
	}
	if err := memorycore.NewGlobalService(g.db, workspace).CreateWithSource(context.Background(), owner, "shared", "EXPLICIT_SHARED", memorycore.MemoryGlobal, "fact", 5, nil, memorycore.WriteSource{Type: "user_explicit"}); err != nil {
		t.Fatal(err)
	}
	content := "# Workspace rules\nReview outputs before delivery.\napi_key=fixture_private_credential_12345\n"
	path := filepath.Join(workspace, "AGENTS.md")
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	beforeEntries := int64(0)
	g.db.Model(&memorycore.Entry{}).Count(&beforeEntries)
	sqlDB, _ := g.db.DB()
	sqlDB.SetMaxOpenConns(1)
	if err := g.db.Exec("PRAGMA query_only = ON").Error; err != nil {
		t.Fatal(err)
	}
	result, body := memoryViewRead(t, g, "view=global")
	if result.Total != 1 || len(result.Entries) != 1 || result.Entries[0].Scope != memorycore.MemoryGlobal || len(result.Supplemental) != 3 {
		t.Fatal("global view omitted actual file or configured agents", body)
	}
	seen := make(map[string]bool)
	for _, ref := range result.Supplemental {
		if !ref.ReadOnly || ref.OwnershipVerified == nil || !*ref.OwnershipVerified {
			t.Fatal("global reference masquerades as editable/shared memory")
		}
		if ref.SourceType == "workspace_instructions" {
			if ref.SourceLocation != "AGENTS.md" || ref.AgentName != "" || ref.CreatedAt != nil {
				t.Fatal("workspace file identity or invented creation time")
			}
		} else if ref.SourceType == "agent_memory" {
			seen[ref.AgentName] = true
		}
	}
	if !seen["first"] || !seen["second"] || strings.Contains(body, "fixture_private_credential_12345") || strings.Contains(body, "FOREIGN_MUST_NOT") || strings.Contains(body, "ELSEWHERE_MUST_NOT") {
		t.Fatal("global view lost labels or exposed other owners")
	}
	var afterEntries int64
	g.db.Model(&memorycore.Entry{}).Count(&afterEntries)
	data, err := os.ReadFile(path)
	if err != nil || string(data) != content || afterEntries != beforeEntries {
		t.Fatal("view modified AGENTS or activated private entries")
	}
	if _, err := os.Stat(filepath.Join(workspace, ".openkitty", "context")); !os.IsNotExist(err) {
		t.Fatal("view created SCM state", err)
	}
	// A configured agent in a different workspace is excluded, even with the
	// same name and locally stored rows in the selected workspace namespace.
	g.engine.agentFactory.agents["second"] = &LLMAgent{workspace: t.TempDir()}
	_, body = memoryViewRead(t, g, "view=global")
	if strings.Contains(body, "PRIVATE_OF_second") {
		t.Fatal("foreign runtime workspace agent included in global archive")
	}
}

func TestPersonalMemoryViewsProfileNeedsOriginalEvidenceAndUnambiguousOwner(t *testing.T) {
	for _, test := range []struct {
		name, quote, profileTenant, constraints string
		foreignEvent, expectedProfile           bool
	}{
		{name: "explicit_tool_constraint", quote: "以后应该用 browser 更好", constraints: `{"preferred":["browser"],"forbidden":["PRIVATE_OTHER_CONSTRAINT"]}`, expectedProfile: true},
		{name: "explicit_artifact_constraint", quote: "以后每次 Word 文档采用宋体、小四字号", constraints: `{"artifact_format_preferences":{"document":{"scope":"document","font_family":"宋体","font_size":"小四","page_size":"PRIVATE_OTHER_CONSTRAINT"}}}`, expectedProfile: true},
		{name: "artifact_constraint_disagrees_with_user", quote: "以后每次 Word 文档采用宋体、小四字号", constraints: `{"artifact_format_preferences":{"document":{"scope":"document","font_family":"Arial","font_size":"小四"}}}`},
		{name: "unrelated_constraint", quote: "我偏好每次先列结论", constraints: `{"preferred":["browser"]}`},
		{name: "defaults_not_preferences", quote: "我偏好每次先列结论", constraints: `{}`},
		{name: "foreign_workspace_same_name", quote: "以后应该用 browser 更好", constraints: `{"preferred":["browser"]}`, foreignEvent: true},
		{name: "foreign_tenant", quote: "以后应该用 browser 更好", constraints: `{"preferred":["browser"]}`, profileTenant: "other"},
	} {
		t.Run(test.name, func(t *testing.T) {
			g := memorySourceFixture(t)
			if err := g.db.Create(&ChatMessage{TenantID: "default", AgentName: "first", SessionID: "source-web", UserId: "fixture-user", Role: "user", Content: test.quote}).Error; err != nil {
				t.Fatal(err)
			}
			if err := g.db.Create(&memorycore.MemoryEvent{TenantID: "default", AgentName: "first", SessionID: "source-web", UserID: "fixture-user", UserName: "verified-name", Status: "success", UserQuestion: "PROCESSED_TASK_INPUT"}).Error; err != nil {
				t.Fatal(err)
			}
			if test.foreignEvent {
				if err := g.db.Create(&ChatSession{ID: "foreign-profile-session", TenantID: "default", AgentName: "first", UserId: "fixture-user", ProjectID: g.project.ProjectID, Workspace: t.TempDir()}).Error; err != nil {
					t.Fatal(err)
				}
				if err := g.db.Create(&memorycore.MemoryEvent{TenantID: "default", AgentName: "first", SessionID: "foreign-profile-session", UserID: "fixture-user", UserName: "verified-name", Status: "success"}).Error; err != nil {
					t.Fatal(err)
				}
			}
			tenant := test.profileTenant
			if tenant == "" {
				tenant = "default"
			}
			if err := g.db.Create(&memorycore.UserProfile{TenantID: tenant, UserName: "verified-name", Language: "zh", ResponseStyle: "balanced", Constraints: datatypes.JSON(test.constraints)}).Error; err != nil {
				t.Fatal(err)
			}
			result, body := memoryViewRead(t, g, "view=preferences")
			if len(result.Entries) != 0 || result.Total != 0 || len(result.Supplemental) != 1 || result.Supplemental[0].Value != test.quote {
				t.Fatal("preference original quote was replaced or omitted", body)
			}
			isProfile := result.Supplemental[0].SourceType == "user_profile_constraint"
			if isProfile != test.expectedProfile || strings.Contains(body, "PRIVATE_OTHER_CONSTRAINT") || strings.Contains(body, "balanced") || strings.Contains(body, "verified-name") || strings.Contains(body, "PROCESSED_TASK_INPUT") {
				t.Fatal("profile default, unproven owner or unrelated constraint exposed", body)
			}
		})
	}
}

func TestPersonalMemoryViewsProfileDoesNotPromoteProcessedTaskInput(t *testing.T) {
	g := memorySourceFixture(t)
	if err := g.db.Create(&memorycore.MemoryEvent{TenantID: "default", AgentName: "first", SessionID: "source-web", UserID: "fixture-user", UserName: "verified-name", Status: "success", UserQuestion: "以后应该用 browser 更好"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := g.db.Create(&memorycore.UserProfile{TenantID: "default", UserName: "verified-name", Constraints: datatypes.JSON(`{"preferred":["browser"]}`)}).Error; err != nil {
		t.Fatal(err)
	}
	result, body := memoryViewRead(t, g, "view=preferences")
	if len(result.Supplemental) != 0 || len(result.Entries) != 0 || result.Total != 0 {
		t.Fatal("processed task input or profile default manufactured an explicit preference", body)
	}
}

func TestPersonalMemoryViewsRejectForeignSessionsAndSpoofedParameters(t *testing.T) {
	g := memorySourceFixture(t)
	if err := g.db.Create(&ChatSession{ID: "foreign-view-session", TenantID: "default", AgentName: "first", UserId: "fixture-user", Workspace: t.TempDir(), ProjectID: g.project.ProjectID}).Error; err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{"view=unknown", "view=short&session_id=foreign-view-session", "view=history&session_id=foreign-view-session", "view=long&scope=project", "view=global&session_id=source-web", "view=preferences&user_name=spoof", "view=preferences&kind=fact", "view=knowledge&knowledge_source=unknown", "view=long&knowledge_source=all", "view=long&view=short", "view=long&subject_id=other", "view=long&workspace=other"} {
		w := memoryScopeRequest(t, g, http.MethodGet, "/api/memory?agent=first&"+query, nil)
		if w.Code != 400 {
			t.Fatalf("spoofed or mismatched view accepted: %s %d %s", query, w.Code, w.Body.String())
		}
	}
	for _, change := range []func(*http.Request){
		func(r *http.Request) { r.RemoteAddr = "192.0.2.1:4000" },
		func(r *http.Request) { r.Header.Set("Origin", "http://attacker.invalid") },
		func(r *http.Request) { r.Header.Set("Sec-Fetch-Site", "cross-site") },
	} {
		r := httptest.NewRequest("GET", "http://localhost:8090/api/memory?view=global", nil)
		r.RemoteAddr = "127.0.0.1:4000"
		change(r)
		w := httptest.NewRecorder()
		g.handlePersonalMemory(w, r)
		if w.Code != 403 {
			t.Fatal("foreign request read global workspace instructions")
		}
	}
	if _, err := os.Stat(filepath.Join(g.currentProjectWorkspace(), ".openkitty")); !os.IsNotExist(err) {
		t.Fatal("view validation created state", err)
	}
}

func TestPersonalMemoryViewsKnowledgeKeepsServerSearchPaginationAndUnknownAgent(t *testing.T) {
	g := memorySourceFixture(t)
	kg, err := memorycore.NewKnowledgeGraph(filepath.Join(g.currentProjectWorkspace(), ".openkitty"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = kg.Close() })
	previous := globalKG
	globalKG = kg
	t.Cleanup(func() { globalKG = previous })
	// SQL must search the complete graph value, not just the returned excerpt.
	if err := kg.AddTriple("Legacy subject", "Legacy predicate", strings.Repeat("x", 1400)+"unique-late-search-marker", "manual", "", "", 1); err != nil {
		t.Fatal(err)
	}
	result, body := memoryViewRead(t, g, "view=knowledge&knowledge_source=graph&search=unique-late-search-marker&limit=1")
	if result.Total != 0 || result.SourcesTotal == nil || *result.SourcesTotal != 1 || len(result.Entries) != 0 || len(result.Supplemental) != 1 {
		t.Fatal("view reapplied search to truncated text and lost a real paged record", body)
	}
	if ref := result.Supplemental[0]; ref.AgentName != "" || ref.OwnershipVerified == nil || *ref.OwnershipVerified || !ref.ReadOnly {
		t.Fatal("unverified workspace knowledge was attributed to the selected Agent", ref)
	}
	if _, err := parsePersonalMemoryViewQuery(httptest.NewRequest("GET", "http://localhost/api/memory?view=knowledge&knowledge_source=quotes", nil)); err != nil {
		t.Fatal("official quotes knowledge category was rejected", err)
	}
	if _, err := parsePersonalMemoryViewQuery(httptest.NewRequest("GET", "http://localhost/api/memory?view=knowledge&knowledge_source=curated", nil)); err != nil {
		t.Fatal("verified knowledge category was rejected", err)
	}
}

func TestPersonalMemoryViewsGlobalInstructionsRejectOutsideSymlink(t *testing.T) {
	g := memorySourceFixture(t)
	outside := filepath.Join(t.TempDir(), "outside-instructions.md")
	if err := os.WriteFile(outside, []byte("FOREIGN_WORKSPACE_INSTRUCTIONS"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(g.currentProjectWorkspace(), "AGENTS.md")); err != nil {
		t.Skip("symlink creation unavailable on this host")
	}
	result, body := memoryViewRead(t, g, "view=global")
	if len(result.Supplemental) != 0 || strings.Contains(body, "FOREIGN_WORKSPACE_INSTRUCTIONS") {
		t.Fatal("AGENTS.md followed a symlink outside the current workspace", body)
	}
}
