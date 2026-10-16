package app

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
	memorycore "mac/internal/memory"
	"mac/internal/projectctx"
)

func newMemoryScopeGateway(t *testing.T) *GatewayService {
	t.Helper()
	workspace := t.TempDir()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "memory.db")), &gorm.Config{Logger: gormlogger.Default.LogMode(gormlogger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	if err := memorycore.InitMemoryDB(db); err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&ChatSession{}, &GatewayTaskRecord{}); err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	config := &Config{Workspace: workspace, Agents: []AgentConfig{{Name: "first"}, {Name: "second"}}}
	return &GatewayService{config: config, project: projectctx.New(workspace), db: db, engine: &MultiAgentEngine{config: config, agentFactory: &AgentFactory{agents: map[string]Agent{"first": &LLMAgent{}, "second": &LLMAgent{}}}}}
}

func memoryScopeRequest(t *testing.T, gateway *GatewayService, method, path string, payload interface{}) *httptest.ResponseRecorder {
	t.Helper()
	var body io.Reader
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		body = bytes.NewReader(data)
	}
	r := httptest.NewRequest(method, "http://localhost:8090"+path, body)
	r.RemoteAddr = "127.0.0.1:40000"
	r.Header.Set("Origin", "http://localhost:8090")
	if method == http.MethodPost {
		r.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	gateway.handlePersonalMemory(w, r)
	return w
}

func TestPersonalMemoryGlobalSharedActions(t *testing.T) {
	gateway := newMemoryScopeGateway(t)
	payload := map[string]interface{}{"agent": "first", "action": "create", "key": "voice", "value": "shared compact style", "scope": "global", "kind": "preference"}
	if w := memoryScopeRequest(t, gateway, http.MethodPost, "/api/memory", payload); w.Code != 200 {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	var response struct {
		Entries []memorycore.Entry
		Total   int64
		Agent   string
	}
	w := memoryScopeRequest(t, gateway, http.MethodGet, "/api/memory?agent=second&scope=global&kind=preference", nil)
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil || w.Code != 200 || response.Total != 1 || response.Agent != "second" || response.Entries[0].AgentName != "" {
		t.Fatalf("cross-assistant list: %d %#v %v", w.Code, response, err)
	}
	if w := memoryScopeRequest(t, gateway, http.MethodGet, "/api/memory/export?agent=second&scope=global", nil); w.Code != 200 || !strings.Contains(w.Body.String(), "shared compact style") {
		t.Fatalf("global export: %d %s", w.Code, w.Body.String())
	}
	payload["agent"], payload["action"], payload["expected_version"] = "second", "pin", 1
	if w := memoryScopeRequest(t, gateway, http.MethodPost, "/api/memory", payload); w.Code != 200 {
		t.Fatalf("pin: %d %s", w.Code, w.Body.String())
	}
	payload["action"], payload["value"] = "correct", "revised shared style"
	if w := memoryScopeRequest(t, gateway, http.MethodPost, "/api/memory", payload); w.Code != 409 {
		t.Fatalf("stale edit: %d", w.Code)
	}
	payload["expected_version"] = 2
	if w := memoryScopeRequest(t, gateway, http.MethodPost, "/api/memory", payload); w.Code != 200 {
		t.Fatalf("edit: %d %s", w.Code, w.Body.String())
	}
	payload["action"], payload["expected_version"] = "forget", 2
	if w := memoryScopeRequest(t, gateway, http.MethodPost, "/api/memory", payload); w.Code != 409 {
		t.Fatalf("stale forget: %d", w.Code)
	}
	payload["expected_version"] = 3
	if w := memoryScopeRequest(t, gateway, http.MethodPost, "/api/memory", payload); w.Code != 200 {
		t.Fatalf("forget: %d %s", w.Code, w.Body.String())
	}
	payload["action"], payload["expected_version"] = "create", 0
	if w := memoryScopeRequest(t, gateway, http.MethodPost, "/api/memory", payload); w.Code != 409 {
		t.Fatalf("resurrection: %d", w.Code)
	}
	for _, query := range []string{"scope=global&session_id=forged", "scope=global&kind=forged", "scope=session&session_id=forged"} {
		if w := memoryScopeRequest(t, gateway, http.MethodGet, "/api/memory?agent=first&"+query, nil); w.Code != 400 {
			t.Fatalf("invalid context %s: %d %s", query, w.Code, w.Body.String())
		}
	}
}

func TestPersonalMemorySessionNotesRequireOwnedWebConversation(t *testing.T) {
	gateway := newMemoryScopeGateway(t)
	workspace := gateway.currentProjectWorkspace()
	now := time.Now()
	base := ChatSession{AgentName: "first", Workspace: workspace, ProjectID: gateway.project.ProjectID, TenantID: "default", CreatedAt: now, UpdatedAt: now}
	for _, item := range []struct{ id, field, value string }{
		{"web_allowed", "", ""}, {"web_foreign-agent", "agent", "second"}, {"web_foreign-tenant", "tenant", "other"},
		{"web_foreign-subject", "subject", "bob"}, {"web_foreign-workspace", "workspace", t.TempDir()},
		{"web_foreign-project", "project", "foreign"}, {"web_foreign-media", "media", "foreign-account"}, {"web_agentos", "", ""},
	} {
		session := base
		session.ID, session.Name = item.id, item.id
		switch item.field {
		case "agent":
			session.AgentName = item.value
		case "tenant":
			session.TenantID = item.value
		case "subject":
			session.SubjectID = item.value
		case "workspace":
			session.Workspace = item.value
		case "project":
			session.ProjectID = item.value
		case "media":
			session.MediaOwner = item.value
		}
		if err := gateway.db.Create(&session).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := gateway.db.Create(&GatewayTaskRecord{TaskID: "agt_fixture", SessionID: "web_agentos", QueueName: agentOSQueueName, TenantID: "default"}).Error; err != nil {
		t.Fatal(err)
	}
	var listing struct {
		Entries      []memorycore.Entry
		Sessions     []personalMemorySession
		Supplemental []memorycore.WorkingReference
	}
	w := memoryScopeRequest(t, gateway, http.MethodGet, "/api/memory?agent=first&scope=session", nil)
	if err := json.Unmarshal(w.Body.Bytes(), &listing); err != nil || w.Code != 200 || len(listing.Sessions) != 1 || listing.Sessions[0].ID != "web_allowed" || len(listing.Entries) != 0 {
		t.Fatalf("session discovery: %d %#v %v", w.Code, listing, err)
	}
	payload := map[string]interface{}{"agent": "first", "action": "create", "key": "note", "value": "manual conversation note", "scope": "session", "kind": "fact"}
	for _, id := range []string{"", "unknown", "web_foreign-agent", "web_foreign-tenant", "web_foreign-subject", "web_foreign-workspace", "web_foreign-project", "web_foreign-media", "web_agentos"} {
		payload["session_id"] = id
		if w := memoryScopeRequest(t, gateway, http.MethodPost, "/api/memory", payload); w.Code != 400 {
			t.Fatalf("foreign write %q: %d %s", id, w.Code, w.Body.String())
		}
		if id != "" {
			if w := memoryScopeRequest(t, gateway, http.MethodGet, "/api/memory?agent=first&scope=session&session_id="+id, nil); w.Code != 400 {
				t.Fatalf("foreign read %q: %d %s", id, w.Code, w.Body.String())
			}
		}
	}
	payload["session_id"] = "web_allowed"
	if w := memoryScopeRequest(t, gateway, http.MethodPost, "/api/memory", payload); w.Code != 200 {
		t.Fatalf("note create: %d %s", w.Code, w.Body.String())
	}
	payload["action"], payload["expected_version"], payload["value"] = "correct", 1, "edited conversation note"
	if w := memoryScopeRequest(t, gateway, http.MethodPost, "/api/memory", payload); w.Code != 200 {
		t.Fatalf("note correction: %d %s", w.Code, w.Body.String())
	}
	w = memoryScopeRequest(t, gateway, http.MethodGet, "/api/memory?agent=first&scope=session&session_id=web_allowed&kind=fact", nil)
	if err := json.Unmarshal(w.Body.Bytes(), &listing); err != nil || w.Code != 200 || len(listing.Entries) != 1 || listing.Entries[0].Value != "edited conversation note" || listing.Entries[0].Version != 2 {
		t.Fatalf("note read: %d %#v %v", w.Code, listing, err)
	}
	payload["action"] = "forget"
	if w := memoryScopeRequest(t, gateway, http.MethodPost, "/api/memory", payload); w.Code != 409 {
		t.Fatalf("stale note forget: %d", w.Code)
	}
	payload["expected_version"] = 2
	if w := memoryScopeRequest(t, gateway, http.MethodPost, "/api/memory", payload); w.Code != 200 {
		t.Fatalf("note forget: %d %s", w.Code, w.Body.String())
	}
	if w := memoryScopeRequest(t, gateway, http.MethodGet, "/api/memory/export?agent=first&scope=session", nil); w.Code != 400 {
		t.Fatalf("working state exported: %d", w.Code)
	}
}

func TestPersonalMemoryAutomaticWorkingReferencesAreReadOnly(t *testing.T) {
	gateway := newMemoryScopeGateway(t)
	workspace := gateway.currentProjectWorkspace()
	if err := gateway.db.Create(&ChatSession{ID: "web_fixture", Name: "Fixture conversation", AgentName: "first", TenantID: "default", Workspace: workspace, ProjectID: gateway.project.ProjectID}).Error; err != nil {
		t.Fatal(err)
	}
	scm := memorycore.NewSessionContextManager(filepath.Join(workspace, ".openkitty", "context"))
	scm.SetWorkspace(workspace)
	if err := scm.Set("progress", "automatic working state", "todo", 5, "web_fixture"); err != nil {
		t.Fatal(err)
	}
	if err := scm.Set("foreign", "foreign working state", "todo", 5, "other_session"); err != nil {
		t.Fatal(err)
	}
	service := memorycore.NewService(gateway.db, workspace, "first")
	owner := memorycore.Owner{TenantID: "default", PartitionID: "default", SessionID: "web_fixture"}
	if err := service.SaveSummary(context.Background(), owner, "automatic summary"); err != nil {
		t.Fatal(err)
	}
	w := memoryScopeRequest(t, gateway, http.MethodGet, "/api/memory?agent=first&scope=session&session_id=web_fixture", nil)
	var listing struct{ Supplemental []memorycore.WorkingReference }
	if err := json.Unmarshal(w.Body.Bytes(), &listing); err != nil || w.Code != 200 || len(listing.Supplemental) != 2 {
		t.Fatalf("references: %d %#v %v", w.Code, listing, err)
	}
	for _, item := range listing.Supplemental {
		if !item.ReadOnly || item.SessionID != "web_fixture" || item.UpdatedAt.IsZero() || strings.Contains(item.Value, "foreign") {
			t.Fatalf("reference metadata/isolation: %#v", item)
		}
		if item.SourceType == "summary" && item.CreatedAt != nil {
			t.Fatal("summary fabricated creation time")
		}
		payload := map[string]interface{}{"agent": "first", "action": "correct", "scope": "session", "session_id": "web_fixture", "key": item.Key, "value": "overwrite automatic source", "expected_version": 1}
		if w := memoryScopeRequest(t, gateway, http.MethodPost, "/api/memory", payload); w.Code != 409 {
			t.Fatalf("automatic source changed: %d %s", w.Code, w.Body.String())
		}
	}
	if content, err := service.LoadSummary(context.Background(), owner); err != nil || content != "automatic summary" {
		t.Fatalf("summary modified: %q %v", content, err)
	}
	if got := scm.GetSession("progress", "web_fixture"); got != "automatic working state" {
		t.Fatalf("SCM modified: %s", got)
	}
}

func TestMemoryServiceGlobalRecallRespectsRuntimeBoundaries(t *testing.T) {
	agent, db := newMemoryServiceTestAgentWithDB(t)
	ctx := context.WithValue(context.Background(), "session_id", "chat")
	owner, err := agent.memoryOwner(ctx)
	if err != nil {
		t.Fatal(err)
	}
	global := memorycore.NewGlobalService(db, agent.workspace)
	if err := global.CreateWithSource(ctx, owner, "global-fact", "global-fixture-value", memorycore.MemoryGlobal, "fact", 5, nil, memorycore.WriteSource{Type: "user_explicit"}); err != nil {
		t.Fatal(err)
	}
	if got := agent.buildMemoryPrompt(ctx, "", "chat", "global-fact"); !strings.Contains(got, "global-fixture-value") {
		t.Fatalf("runtime did not recall shared memory: %s", got)
	}
	agent.hasMemory = false
	if got := agent.buildMemoryPrompt(ctx, "", "chat", "global-fact"); got != "" {
		t.Fatal("disabled runtime recalled shared memory")
	}
	agent.hasMemory = true
	isolated := context.WithValue(ctx, "pipeline_isolated_context", true)
	if got := agent.buildMemoryPrompt(isolated, "", "chat", "global-fact"); got != "" {
		t.Fatal("isolated internal task recalled shared memory")
	}
	authenticated := projectctx.WithResolvedWorkspace(ctx, agent.workspace)
	authenticated = withExecutionIdentity(authenticated, ExecutionIdentity{TenantID: "tenant-a", SubjectID: "alice", UserID: "alice", TenantScoped: true, SubjectScoped: true})
	personalOwner, err := agent.memoryOwner(authenticated)
	if err != nil {
		t.Fatal(err)
	}
	if err := global.CreateWithSource(authenticated, personalOwner, "private-global", "alice-global-value", memorycore.MemoryGlobal, "fact", 5, nil, memorycore.WriteSource{Type: "user_explicit"}); err != nil {
		t.Fatal(err)
	}
	if got := agent.buildMemoryPrompt(authenticated, "alice", "chat", "global-fact private-global"); !strings.Contains(got, "alice-global-value") || strings.Contains(got, "global-fixture-value") {
		t.Fatalf("authenticated global namespace: %s", got)
	}
	foreign := withExecutionIdentity(authenticated, ExecutionIdentity{TenantID: "tenant-a", SubjectID: "bob", UserID: "bob", TenantScoped: true, SubjectScoped: true})
	if got := agent.buildMemoryPrompt(foreign, "bob", "chat", "private-global"); got != "" {
		t.Fatal("another user adopted shared personal-workspace memory")
	}
}

func TestPersonalMemoryUnicodeLengthAndBoundedBody(t *testing.T) {
	gateway := newMemoryScopeGateway(t)
	payload := map[string]interface{}{"agent": "first", "action": "create", "key": strings.Repeat("界", 256), "scope": "project", "kind": "fact", "value": strings.Repeat("界", 8000)}
	if w := memoryScopeRequest(t, gateway, http.MethodPost, "/api/memory", payload); w.Code != 200 {
		t.Fatalf("8000 Unicode characters rejected: %d %s", w.Code, w.Body.String())
	}
	payload["key"], payload["value"] = "emoji", strings.Repeat("😀", 8000)
	if w := memoryScopeRequest(t, gateway, http.MethodPost, "/api/memory", payload); w.Code != 200 {
		t.Fatalf("8000 four-byte characters rejected: %d %s", w.Code, w.Body.String())
	}
	payload["key"], payload["value"] = "too-long", strings.Repeat("界", 8001)
	if w := memoryScopeRequest(t, gateway, http.MethodPost, "/api/memory", payload); w.Code != 400 {
		t.Fatalf("character limit bypassed: %d", w.Code)
	}
	payload["value"] = strings.Repeat("x", 70<<10)
	if w := memoryScopeRequest(t, gateway, http.MethodPost, "/api/memory", payload); w.Code != 400 {
		t.Fatalf("body limit bypassed: %d", w.Code)
	}
}
