package app

import (
	"bytes"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"mac/internal/toolregistry"
)

func teamTemplatesTestGateway(t *testing.T) *GatewayService {
	t.Helper()
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOME", home)
	t.Setenv("TEAM_TEMPLATES_FIXTURE_KEY", "fake-team-key-not-a-real-credential")
	config := selectionFixture()
	config.Workspace = t.TempDir()
	config.TeamName = "Preserve unrelated team name"
	config.Task = Task{Description: "Preserve unrelated task"}
	model := config.ModelConfig["one"]
	model.APIKey = ""
	model.APIKeyEnv = "TEAM_TEMPLATES_FIXTURE_KEY"
	config.ModelConfig["one"] = model
	config.TeamTemplates = map[string][]TeamRole{"existing": {{Role: "审阅", SystemPrompt: "原职责", Model: "two"}}}
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := SaveConfig(path, config); err != nil {
		t.Fatal(err)
	}
	return &GatewayService{configpath: path, config: config, logger: log.New(io.Discard, "", 0), engine: selectionTestEngine(config)}
}

func teamTemplatesRequest(g *GatewayService, method, path string, body []byte) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "http://127.0.0.1:8082"+path, bytes.NewReader(body))
	r.RemoteAddr = "127.0.0.1:23456"
	r.Header.Set("Origin", "http://127.0.0.1:8082")
	w := httptest.NewRecorder()
	g.handleTeamTemplates(w, r)
	return w
}

func teamTemplatesInput(t *testing.T, g *GatewayService, id string, roles []TeamRole) []byte {
	t.Helper()
	revision, err := configFileRevision(g.configpath)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(map[string]interface{}{"id": id, "roles": roles, "config_revision": revision})
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestTeamTemplatesCatalogAndDurableEdit(t *testing.T) {
	g := teamTemplatesTestGateway(t)
	w := teamTemplatesRequest(g, http.MethodGet, "/api/team-templates", nil)
	var catalog struct {
		Teams    []teamTemplateDTO `json:"teams"`
		Revision string            `json:"config_revision"`
	}
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &catalog) != nil || len(catalog.Teams) != 9 || len(catalog.Revision) != 64 {
		t.Fatalf("invalid real catalog: %d %s", w.Code, w.Body.String())
	}
	for _, team := range catalog.Teams {
		if team.ID == "ppt" && (!reflect.DeepEqual(team.Roles, defaultTeamTemplates["ppt"]) || len(team.Roles) != 4 || team.Roles[0].Role != "艺术总监") {
			t.Fatal("PPT catalog lost actual director, prompts, or skills")
		}
		if team.ID == "fullstack" && (team.Mode != "hybrid" || !team.RoleCountLocked) {
			t.Fatal("fixed mixed-stage contract missing")
		}
	}
	roles := append([]TeamRole(nil), defaultTeamTemplates["ppt"]...)
	roles[0].SystemPrompt = "修改后的艺术指导职责"
	w = teamTemplatesRequest(g, http.MethodPut, "/api/team-templates", teamTemplatesInput(t, g, "ppt", roles))
	if w.Code != http.StatusOK {
		t.Fatalf("edit failed: %d %s", w.Code, w.Body.String())
	}
	custom := []TeamRole{{Role: "核对员", SystemPrompt: "核对已有资料，不编造参数", Skill: "ppt-art-director", Model: "two"}}
	w = teamTemplatesRequest(g, http.MethodPut, "/api/team-templates", teamTemplatesInput(t, g, "我的专家团队", custom))
	if w.Code != http.StatusOK {
		t.Fatalf("new team failed: %d %s", w.Code, w.Body.String())
	}
	saved, err := LoadConfig(g.configpath)
	if err != nil || !reflect.DeepEqual(saved.TeamTemplates["我的专家团队"], custom) || saved.TeamTemplates["ppt"][0].SystemPrompt != roles[0].SystemPrompt || saved.TeamTemplates["existing"][0].Model != "two" {
		t.Fatal("single-team edits did not persist without losing other templates", err)
	}
	if saved.TeamName != "Preserve unrelated team name" || saved.Task.Description != "Preserve unrelated task" || len(saved.ModelConfig) != 2 || len(saved.Agents) != 2 {
		t.Fatal("unrelated config fields overwritten")
	}
	raw, err := os.ReadFile(g.configpath)
	if err != nil || !bytes.Contains(raw, []byte("TEAM_TEMPLATES_FIXTURE_KEY")) || bytes.Contains(raw, []byte("fake-team-key-not-a-real-credential")) {
		t.Fatal("environment credential reference was replaced by its resolved value", err)
	}
	if g.config.TeamTemplates["我的专家团队"][0].SystemPrompt != custom[0].SystemPrompt || g.engine.config.TeamTemplates["我的专家团队"][0].SystemPrompt != custom[0].SystemPrompt {
		t.Fatal("saved team was not hot-reloaded into the engine")
	}
	for _, response := range []string{w.Body.String(), string(mustTeamCatalog(t, g))} {
		if strings.Contains(response, "fixture-one-key") || strings.Contains(response, "fixture-two-key") || strings.Contains(response, "fake-team-key") || strings.Contains(response, "api_key") || strings.Contains(response, "localhost:1234") {
			t.Fatal("team DTO leaked provider configuration")
		}
	}
}

func mustTeamCatalog(t *testing.T, g *GatewayService) []byte {
	t.Helper()
	w := teamTemplatesRequest(g, http.MethodGet, "/api/team-templates", nil)
	if w.Code != http.StatusOK {
		t.Fatal(w.Body.String())
	}
	return w.Body.Bytes()
}

func TestTeamTemplatesCASAndParallelSaves(t *testing.T) {
	g := teamTemplatesTestGateway(t)
	roles := []TeamRole{{Role: "审阅", SystemPrompt: "更新职责"}}
	stale := teamTemplatesInput(t, g, "existing", roles)
	external, err := LoadConfig(g.configpath)
	if err != nil {
		t.Fatal(err)
	}
	external.TeamName = "Concurrent unrelated edit"
	if err := SaveConfig(g.configpath, external); err != nil {
		t.Fatal(err)
	}
	w := teamTemplatesRequest(g, http.MethodPut, "/api/team-templates", stale)
	if w.Code != http.StatusConflict {
		t.Fatalf("stale save accepted: %d", w.Code)
	}
	a, b := teamTemplatesInput(t, g, "alpha", roles), teamTemplatesInput(t, g, "beta", roles)
	var wg sync.WaitGroup
	codes := make(chan int, 2)
	for _, input := range [][]byte{a, b} {
		wg.Add(1)
		go func(body []byte) {
			defer wg.Done()
			codes <- teamTemplatesRequest(g, http.MethodPut, "/api/team-templates", body).Code
		}(input)
	}
	wg.Wait()
	close(codes)
	counts := map[int]int{}
	for code := range codes {
		counts[code]++
	}
	if counts[http.StatusOK] != 1 || counts[http.StatusConflict] != 1 {
		t.Fatalf("competing CAS saves must have one winner: %v", counts)
	}
	saved, err := LoadConfig(g.configpath)
	if err != nil || saved.TeamName != external.TeamName || saved.TeamTemplates["existing"][0].SystemPrompt != "原职责" {
		t.Fatal("conflict overwrote newer or unrelated fields", err)
	}
	// Omission preserves the existing compatibility-only per-role model field.
	w = teamTemplatesRequest(g, http.MethodPut, "/api/team-templates", teamTemplatesInput(t, g, "existing", roles))
	if w.Code != http.StatusOK || g.config.TeamTemplates["existing"][0].Model != "two" {
		t.Fatal("editing duties dropped an existing model reference")
	}
}

func TestTeamTemplatesLocalBoundaryAndInvalidInputs(t *testing.T) {
	g := teamTemplatesTestGateway(t)
	before, _ := os.ReadFile(g.configpath)
	roles := []TeamRole{{Role: "审阅", SystemPrompt: "职责"}}
	good := teamTemplatesInput(t, g, "valid", roles)
	for _, test := range []struct{ name, remote, host, origin, site string }{
		{"remote", "192.0.2.10:1234", "127.0.0.1:8082", "", ""},
		{"foreign_origin", "127.0.0.1:1234", "127.0.0.1:8082", "https://foreign.invalid", ""},
		{"foreign_host", "127.0.0.1:1234", "foreign.invalid", "", ""},
		{"cross_site", "127.0.0.1:1234", "127.0.0.1:8082", "", "cross-site"},
	} {
		t.Run(test.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPut, "http://127.0.0.1:8082/api/team-templates", bytes.NewReader(good))
			r.RemoteAddr, r.Host = test.remote, test.host
			r.Header.Set("Origin", test.origin)
			r.Header.Set("Sec-Fetch-Site", test.site)
			w := httptest.NewRecorder()
			g.handleTeamTemplates(w, r)
			if w.Code != http.StatusForbidden {
				t.Fatalf("boundary not enforced: %d", w.Code)
			}
		})
	}
	for _, test := range []struct {
		name, id string
		roles    []TeamRole
	}{
		{"reserved", "auto", roles}, {"long_name", strings.Repeat("字", 81), roles}, {"name_path", "../escape", roles},
		{"empty_roles", "empty", nil}, {"role_path", "valid", []TeamRole{{Role: "../role", SystemPrompt: "x"}}},
		{"duplicate_role", "valid", []TeamRole{{Role: "same", SystemPrompt: "a"}, {Role: "same", SystemPrompt: "b"}}},
		{"empty_prompt", "valid", []TeamRole{{Role: "same"}}},
		{"long_prompt", "valid", []TeamRole{{Role: "same", SystemPrompt: strings.Repeat("字", 8001)}}},
		{"skill_path", "valid", []TeamRole{{Role: "same", SystemPrompt: "x", Skill: "../secret"}}},
		{"skill_url", "valid", []TeamRole{{Role: "same", SystemPrompt: "x", Skill: "https://foreign.invalid"}}},
		{"fixed_fullstack", "fullstack", roles}, {"fixed_competitor", "competitor", append(roles, roles...)},
	} {
		t.Run(test.name, func(t *testing.T) {
			w := teamTemplatesRequest(g, http.MethodPut, "/api/team-templates", teamTemplatesInput(t, g, test.id, test.roles))
			if w.Code != http.StatusBadRequest {
				t.Fatalf("invalid team accepted: %d %s", w.Code, w.Body.String())
			}
		})
	}
	for _, extra := range []string{`"scope":"global"`, `"op":"delete"`, `"mode":"parallel"`, `"name":"rename"`} {
		body := append(append([]byte(nil), good[:len(good)-1]...), []byte(","+extra+"}")...)
		if w := teamTemplatesRequest(g, http.MethodPut, "/api/team-templates", body); w.Code != http.StatusBadRequest {
			t.Fatalf("unknown field accepted: %s", extra)
		}
	}
	if w := teamTemplatesRequest(g, http.MethodPut, "/api/team-templates", append(append([]byte(nil), good...), []byte(`{}`)...)); w.Code != http.StatusBadRequest {
		t.Fatal("trailing JSON accepted")
	}
	if w := teamTemplatesRequest(g, http.MethodPut, "/api/team-templates", bytes.Repeat([]byte(" "), (512<<10)+1)); w.Code != http.StatusBadRequest {
		t.Fatal("oversized request accepted")
	}
	if w := teamTemplatesRequest(g, http.MethodGet, "/api/team-templates?scope=global", nil); w.Code != http.StatusBadRequest {
		t.Fatal("unknown query scope accepted")
	}
	if w := teamTemplatesRequest(g, http.MethodPost, "/api/team-templates", good); w.Code != http.StatusMethodNotAllowed {
		t.Fatal("unsupported operation accepted")
	}
	after, _ := os.ReadFile(g.configpath)
	if !bytes.Equal(before, after) {
		t.Fatal("rejected request modified configuration")
	}
}

func TestTeamTemplatesReloadFailureIsNotSuccess(t *testing.T) {
	g := teamTemplatesTestGateway(t)
	// A disconnected runtime can still persist settings, but cannot activate a
	// changed team. Reload must report this actual engine failure to the UI.
	g.engine = nil
	roles := []TeamRole{{Role: "审阅", SystemPrompt: "已保存但未热加载"}}
	w := teamTemplatesRequest(g, http.MethodPut, "/api/team-templates", teamTemplatesInput(t, g, "failure", roles))
	var response struct {
		Status, Message string
		Revision        string `json:"config_revision"`
	}
	if w.Code != http.StatusInternalServerError || json.Unmarshal(w.Body.Bytes(), &response) != nil || response.Status != "saved_reload_failed" || len(response.Revision) != 64 {
		t.Fatalf("reload failure misreported: %d %s", w.Code, w.Body.String())
	}
	saved, err := LoadConfig(g.configpath)
	if err != nil || saved.TeamTemplates["failure"][0].SystemPrompt != roles[0].SystemPrompt || len(g.config.TeamTemplates["failure"]) != 0 {
		t.Fatal("failure did not accurately distinguish persisted and running config", err)
	}
	if strings.Contains(response.Message, "引擎未初始化") || strings.Contains(response.Message, g.configpath) {
		t.Fatal("unsafe internal error returned")
	}
}

func TestTeamTemplatesRuntimeRefreshAndWorkspaceBoundary(t *testing.T) {
	g := teamTemplatesTestGateway(t)
	cfg := g.config
	registry := toolregistry.New(toolregistry.Dependencies{})
	team := NewAssembleTeamTool(cfg.Workspace, g.logger, nil, registry, cfg)
	registry.Register(team)
	model := cfg.ModelConfig["one"]
	agent := &LLMAgent{name: "Assistant", modelConfig: &model, globalConfig: cfg, toolRegistry: registry}
	g.engine.agentFactory.agents["Assistant"] = agent
	updated := *cfg
	updated.TeamTemplates = map[string][]TeamRole{"custom-live": {{Role: "核对", SystemPrompt: "新的职责"}}}
	if !g.needUpdateEngine(cfg, &updated) || !(&ClawBot{}).needUpdateEngine(cfg, &updated) {
		t.Fatal("team edits must trigger both existing host reload paths")
	}
	if err := g.engine.UpdateConfig(&updated); err != nil {
		t.Fatal(err)
	}
	if roles := team.configuredRoles("custom-live"); len(roles) != 1 || roles[0].SystemPrompt != "新的职责" {
		t.Fatal("cached assemble_team retained old config")
	}
	properties := team.Parameters()["properties"].(map[string]interface{})
	names := properties["template"].(map[string]interface{})["enum"].([]string)
	found := false
	for _, name := range names {
		found = found || name == "custom-live"
	}
	if !found || teamTemplateForConfig(&updated, "custom-live").Mode != "parallel" || teamTemplateForConfig(&updated, "academic").Mode != "sequential" {
		t.Fatal("catalog/schema do not reflect real custom template execution mode")
	}
	// Task-scoped runtimes must not regain unscoped collaboration during reload.
	scopedRegistry := toolregistry.New(toolregistry.Dependencies{})
	scopedRegistry.Register(NewAssembleTeamTool(t.TempDir(), g.logger, nil, scopedRegistry, cfg))
	scoped := &LLMAgent{name: "Scoped", taskScopedRuntime: true, toolRegistry: scopedRegistry}
	g.engine.registerRuntimeToolsForAgentWithWorkspace(scoped, updated.Workspace, g.logger)
	if _, exists := scopedRegistry.Get("assemble_team"); exists {
		t.Fatal("team refresh bypassed task-scoped workspace boundary")
	}
	// Configuration reads are copied; callers cannot mutate the saved roles.
	copyRoles := team.configuredRoles("custom-live")
	copyRoles[0].SystemPrompt = "unexpected mutation"
	if team.configuredRoles("custom-live")[0].SystemPrompt != "新的职责" {
		t.Fatal("runtime leaked mutable template storage")
	}
}
