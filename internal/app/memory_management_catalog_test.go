package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/datatypes"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
	memorycore "mac/internal/memory"
)

func readMemoryCatalog(t *testing.T, g *GatewayService, agent string) (personalMemoryCatalog, string) {
	t.Helper()
	w := memoryScopeRequest(t, g, http.MethodGet, "/api/memory/catalog?agent="+agent, nil)
	var result personalMemoryCatalog
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &result) != nil {
		t.Fatalf("catalogue GET failed: %d %s", w.Code, w.Body.String())
	}
	return result, w.Body.String()
}

func catalogSourcesByID(result personalMemoryCatalog) map[string]personalMemoryCatalogSource {
	sources := make(map[string]personalMemoryCatalogSource)
	for _, source := range result.Sources {
		sources[source.ID] = source
	}
	return sources
}

func assertCatalogCount(t *testing.T, source personalMemoryCatalogSource, expected int64) {
	t.Helper()
	if source.Counts.Verified == nil || *source.Counts.Verified != expected || !source.Available {
		t.Fatalf("%s count: %#v, expected %d", source.ID, source, expected)
	}
}

func TestPersonalMemoryCatalogUsesVerifiedOwnersAndOnlySafeMetadata(t *testing.T) {
	g := memorySourceFixture(t)
	runtime := &LLMAgent{workspace: g.currentProjectWorkspace(), hasMemory: true}
	g.engine.agentFactory.agents["first"] = runtime
	valid := memorycore.MemoryEvent{TenantID: "default", AgentName: "first", SessionID: "source-web", UserID: "fixture-user", UserChannel: "web", Status: "success",
		UserQuestion: "PRIVATE_QUESTION", FinalAnswer: "PRIVATE_ANSWER", UserName: "PRIVATE_USERNAME", SystemPrompt: "PRIVATE_SYSTEM",
		CallChain: datatypes.JSON(`[{"type":"tool","name":"read_file","success":true,"arguments":{"api_key":"FAKE_SECRET"},"error":"PRIVATE_PROVIDER_ERROR"},{"type":"skill","name":"write_report"}]`)}
	if err := g.db.Create(&valid).Error; err != nil {
		t.Fatal(err)
	}
	foreign := []ChatSession{
		{ID: "foreign-workspace", AgentName: "first", TenantID: "default", UserId: "fixture-user", Workspace: t.TempDir(), ProjectID: g.project.ProjectID},
		{ID: "foreign-tenant", AgentName: "first", TenantID: "other", UserId: "fixture-user", Workspace: g.currentProjectWorkspace(), ProjectID: g.project.ProjectID},
		{ID: "foreign-subject", AgentName: "first", TenantID: "default", SubjectID: "foreign", UserId: "fixture-user", Workspace: g.currentProjectWorkspace(), ProjectID: g.project.ProjectID},
		{ID: "foreign-media", AgentName: "first", TenantID: "default", MediaOwner: "foreign", UserId: "fixture-user", Workspace: g.currentProjectWorkspace(), ProjectID: g.project.ProjectID},
		{ID: "foreign-project", AgentName: "first", TenantID: "default", UserId: "fixture-user", Workspace: g.currentProjectWorkspace(), ProjectID: "foreign"},
		{ID: "foreign-agent", AgentName: "second", TenantID: "default", UserId: "fixture-user", Workspace: g.currentProjectWorkspace(), ProjectID: g.project.ProjectID},
		{ID: "agentos-session", AgentName: "first", TenantID: "default", UserId: "fixture-user", Workspace: g.currentProjectWorkspace(), ProjectID: g.project.ProjectID},
	}
	for _, session := range foreign {
		if err := g.db.Create(&session).Error; err != nil {
			t.Fatal(err)
		}
		event := valid
		event.ID, event.SessionID = 0, session.ID
		if err := g.db.Create(&event).Error; err != nil {
			t.Fatal(err)
		}
		if err := g.db.Create(&memorycore.Knowledge{TenantID: "default", AgentName: "first", KnowledgeID: session.ID, SourceID: strconv.FormatUint(uint64(event.ID), 10), Content: "FOREIGN_CONTENT"}).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := g.db.Create(&GatewayTaskRecord{TaskID: "fixture-os-task", SessionID: "agentos-session", QueueName: agentOSQueueName}).Error; err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*memorycore.MemoryEvent){
		func(e *memorycore.MemoryEvent) { e.TenantID = "other" },
		func(e *memorycore.MemoryEvent) { e.AgentName = "second" },
		func(e *memorycore.MemoryEvent) { e.UserID = "different-user" },
		func(e *memorycore.MemoryEvent) { e.UserChannel = "qq" },
		func(e *memorycore.MemoryEvent) { e.Status = "running" },
	} {
		event := valid
		event.ID = 0
		change(&event)
		if err := g.db.Create(&event).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, sessionID := range []string{"", "missing-session"} {
		event := valid
		event.ID, event.SessionID = 0, sessionID
		if err := g.db.Create(&event).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := g.db.Create(&memorycore.Knowledge{TenantID: "default", AgentName: "first", KnowledgeID: "owned-knowledge", SourceID: strconv.FormatUint(uint64(valid.ID), 10), Content: "PRIVATE_KNOWLEDGE"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := g.db.Create(&memorycore.UserProfile{TenantID: "default", UserName: "PRIVATE_PROFILE_USER"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := g.db.Create(&memorycore.ToolSkillStat{TenantID: "default", AgentName: "first", Name: "PRIVATE_STAT", TotalCount: 99999}).Error; err != nil {
		t.Fatal(err)
	}
	// Enforce read-only SQL, rather than merely inspecting endpoint code.
	sqlDB, _ := g.db.DB()
	sqlDB.SetMaxOpenConns(1)
	if err := g.db.Exec("PRAGMA query_only = ON").Error; err != nil {
		t.Fatal(err)
	}
	result, body := readMemoryCatalog(t, g, "first")
	sources := catalogSourcesByID(result)
	if result.Agent != "first" || result.Scope != "current_personal_workspace" || !result.ReadOnly || !result.CountsBounded || result.SessionsVerified != 1 || len(sources) != 10 {
		t.Fatalf("invalid catalogue boundary: %#v", result)
	}
	assertCatalogCount(t, sources["events"], 1)
	if sources["events"].Counts.Unverified != nil || !containsCatalogNote(sources["events"], "incomplete_ownership") {
		t.Fatal("orphan and foreign records have no personal ownership proof, even for an aggregate count")
	}
	assertCatalogCount(t, sources["knowledge"], 1)
	assertCatalogCount(t, sources["tool_usage"], 2)
	assertCatalogCount(t, sources["entries"], 0)
	if sources["profiles"].Counts.Verified != nil || sources["profiles"].Counts.Unverified != nil || sources["profiles"].Status != "unverified" || sources["rag"].Counts.Verified != nil || sources["session_context"].Counts.Verified != nil {
		t.Fatal("unproven profile, RAG or SCM ownership must remain unknown")
	}
	for _, forbidden := range []string{"PRIVATE_", "FAKE_SECRET", "FOREIGN_CONTENT", "source-web", "foreign-workspace", "fixture-user", "api_key", "arguments", g.currentProjectWorkspace()} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("catalogue disclosed private execution data: %s", forbidden)
		}
	}
	if !reflect.DeepEqual(sources["knowledge_graph"].Storage.Tables, []string{"triples", "fact_evidence", "verbatim_memories", "entity_aliases"}) {
		t.Fatal("graph catalogue must use the actual schema table names")
	}
	if _, err := os.Stat(filepath.Join(g.currentProjectWorkspace(), ".openkitty")); !os.IsNotExist(err) {
		t.Fatal("directory GET created workspace state", err)
	}
}

func TestPersonalMemoryCatalogDoesNotImportAndCountsActualRuntimeStores(t *testing.T) {
	g := memorySourceFixture(t)
	workspace := g.currentProjectWorkspace()
	legacy := memorycore.NewAgentMemoryStore(workspace, "first")
	if err := legacy.Remember("legacy-only", "PRIVATE_LEGACY_VALUE", memorycore.MemoryUser, 5, nil, ""); err != nil {
		t.Fatal(err)
	}
	service := memorycore.NewService(g.db, workspace, "first")
	owner := memorycore.Owner{TenantID: "default", PartitionID: "default", ImportLegacy: false}
	if err := service.Record(context.Background(), owner, "manual", "PRIVATE_MANUAL", memorycore.MemoryUser, "fact", "active", 5, nil, 0); err != nil {
		t.Fatal(err)
	}
	owner.SessionID = "source-web"
	if err := service.CreateSessionNote(context.Background(), owner, "note", "PRIVATE_SESSION", "fact"); err != nil {
		t.Fatal(err)
	}
	if err := service.SaveSummary(context.Background(), owner, "PRIVATE_SUMMARY"); err != nil {
		t.Fatal(err)
	}
	if err := memorycore.NewGlobalService(g.db, workspace).CreateWithSource(context.Background(), owner, "shared", "PRIVATE_SHARED", memorycore.MemoryGlobal, "fact", 5, nil, memorycore.WriteSource{Type: "user_explicit"}); err != nil {
		t.Fatal(err)
	}
	foreignOwner := owner
	foreignOwner.SubjectID = "foreign"
	if err := service.Record(context.Background(), foreignOwner, "foreign", "PRIVATE_FOREIGN", memorycore.MemoryUser, "fact", "active", 5, nil, 0); err != nil {
		t.Fatal(err)
	}
	scm := memorycore.NewSessionContextManager(filepath.Join(workspace, ".openkitty", "context"))
	scm.SetWorkspace(workspace)
	if err := scm.Set("owned", "PRIVATE_CONTEXT", "fact", 5, "source-web"); err != nil {
		t.Fatal(err)
	}
	if err := scm.Set("foreign", "PRIVATE_FOREIGN_CONTEXT", "fact", 5, "foreign-session"); err != nil {
		t.Fatal(err)
	}
	g.engine.agentFactory.agents["first"] = &LLMAgent{workspace: workspace, hasMemory: false, scm: scm}
	beforeFile, err := os.ReadFile(filepath.Join(scm.StoreDir(), "context.json"))
	if err != nil {
		t.Fatal(err)
	}
	var before, after int64
	g.db.Model(&memorycore.Entry{}).Count(&before)
	sqlDB, _ := g.db.DB()
	sqlDB.SetMaxOpenConns(1)
	if err := g.db.Exec("PRAGMA query_only = ON").Error; err != nil {
		t.Fatal(err)
	}
	result, body := readMemoryCatalog(t, g, "first")
	sources := catalogSourcesByID(result)
	assertCatalogCount(t, sources["entries"], 3)
	assertCatalogCount(t, sources["summaries"], 1)
	assertCatalogCount(t, sources["session_context"], 1)
	for _, source := range result.Sources {
		if source.Recall.Mode == "automatic" && (source.Recall.Enabled == nil || *source.Recall.Enabled || !containsCatalogNote(source, "memory_disabled")) {
			t.Fatal("actual disabled runtime was presented as automatic memory enabled", source.ID)
		}
	}
	g.db.Model(&memorycore.Entry{}).Count(&after)
	afterFile, err := os.ReadFile(filepath.Join(scm.StoreDir(), "context.json"))
	if err != nil || before != after || string(beforeFile) != string(afterFile) || strings.Contains(body, "PRIVATE_") || strings.Contains(body, "legacy-only") {
		t.Fatal("directory GET imported or changed an existing store", err)
	}
	var migrations int64
	if err := g.db.Table("memory_service_migrations").Count(&migrations).Error; err != nil || migrations != 0 {
		t.Fatal("catalogue must not create migration markers", err, migrations)
	}
}

func containsCatalogNote(source personalMemoryCatalogSource, code string) bool {
	for _, note := range source.NoteCodes {
		if note == code {
			return true
		}
	}
	return false
}

func TestPersonalMemoryCatalogReadOnlyBoundaryAndActualSQLiteLocation(t *testing.T) {
	g := newMemoryScopeGateway(t)
	for _, test := range []struct {
		name, method, path, remote, host, origin, site string
		status                                         int
	}{
		{name: "remote", remote: "192.0.2.1:4000", status: 403},
		{name: "foreign_origin", origin: "http://attacker.invalid", status: 403},
		{name: "foreign_host", host: "attacker.invalid", status: 403},
		{name: "cross_site", site: "cross-site", status: 403},
		{name: "post", method: "POST", status: 405},
		{name: "put", method: "PUT", status: 405},
		{name: "unknown_agent", path: "?agent=foreign", status: 503},
		{name: "spoof_owner", path: "?agent=first&subject_id=foreign", status: 400},
		{name: "spoof_workspace", path: "?agent=first&workspace=foreign", status: 400},
		{name: "session_parameter", path: "?agent=first&session_id=source-web", status: 400},
		{name: "duplicate_agent", path: "?agent=first&agent=second", status: 400},
		{name: "malformed_query", path: "?agent=%ZZ", status: 400},
	} {
		t.Run(test.name, func(t *testing.T) {
			method, remote, host, origin := test.method, test.remote, test.host, test.origin
			if method == "" {
				method = "GET"
			}
			if remote == "" {
				remote = "127.0.0.1:4000"
			}
			if host == "" {
				host = "localhost:8090"
			}
			if origin == "" {
				origin = "http://localhost:8090"
			}
			r := httptest.NewRequest(method, "http://localhost:8090/api/memory/catalog"+test.path, nil)
			r.RemoteAddr, r.Host = remote, host
			r.Header.Set("Origin", origin)
			r.Header.Set("Sec-Fetch-Site", test.site)
			w := httptest.NewRecorder()
			g.handlePersonalMemory(w, r)
			if w.Code != test.status || w.Header().Get("Cache-Control") != "no-store" || test.status == 405 && w.Header().Get("Allow") != "GET" {
				t.Fatalf("unexpected boundary: %d %s", w.Code, w.Body.String())
			}
		})
	}
	result, _ := readMemoryCatalog(t, g, "first")
	external := catalogSourcesByID(result)["entries"]
	if len(external.Storage.Paths) != 0 || !containsCatalogNote(external, "custom_sqlite_file") {
		t.Fatal("external SQLite location must be redacted, not guessed as the workspace default")
	}
	path := filepath.Join(g.currentProjectWorkspace(), "custom-local.db")
	db, err := gorm.Open(sqlite.Open(path), &gorm.Config{Logger: gormlogger.Default.LogMode(gormlogger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := memorycore.InitMemoryDB(db); err != nil {
		t.Fatal(err)
	}
	g.db = db
	result, _ = readMemoryCatalog(t, g, "first")
	local := catalogSourcesByID(result)["entries"]
	if !reflect.DeepEqual(local.Storage.Paths, []string{"custom-local.db"}) || containsCatalogNote(local, "custom_sqlite_file") {
		t.Fatal("catalogue failed to report the actual in-workspace SQLite override")
	}
	if relative := personalMemoryCatalogRelativePath(g.currentProjectWorkspace(), filepath.Join(t.TempDir(), "private.db")); relative != "" {
		t.Fatal("external path escaped relative-location boundary")
	}
}

func TestPersonalMemoryCatalogToolCallsRemainBounded(t *testing.T) {
	g := memorySourceFixture(t)
	for index := 0; index < 55; index++ {
		if err := g.db.Create(&memorycore.MemoryEvent{TenantID: "default", AgentName: "first", SessionID: "source-web", UserID: "fixture-user", Status: "success", CallChain: datatypes.JSON(`[{"type":"tool","name":"read_file"}]`)}).Error; err != nil {
			t.Fatal(err)
		}
	}
	result, _ := readMemoryCatalog(t, g, "first")
	sources := catalogSourcesByID(result)
	assertCatalogCount(t, sources["events"], 55)
	assertCatalogCount(t, sources["tool_usage"], 50)
	if sources["tool_usage"].Counts.Unit != "calls" || !sources["tool_usage"].Counts.Bounded || !result.CountsBounded {
		t.Fatal("bounded execution receipts were presented as lifetime tool statistics")
	}
}
