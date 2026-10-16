package app

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"mac/internal/skillpath"
	skilldomain "mac/internal/skills"
)

func testGatewayWithLoadedSkill(t *testing.T) *GatewayService {
	t.Helper()
	loader := NewSkillsLoader(t.TempDir(), "test-agent", "")
	loader.RegisterSkill("builtin-demo", &skilldomain.Skill{
		Metadata:  skilldomain.SkillMetadata{Name: "builtin-demo", Aliases: []string{"demo"}, Description: "loaded from the embedded catalog"},
		Interface: skillpath.SkillInterfaceMetadata{DisplayName: "Builtin Demo", ShortDescription: "Friendly summary", DefaultPrompt: "Use $builtin-demo."},
		Source:    "builtin",
		Path:      "builtin/builtin-demo/SKILL.md",
		Raw:       "---\nname: builtin-demo\ndescription: loaded from the embedded catalog\n---\n# Demo\n",
	})
	agent := &LLMAgent{name: "test-agent", skillsLoader: loader}
	factory := &AgentFactory{agents: map[string]Agent{"test-agent": agent}}
	return &GatewayService{
		config: &Config{Workspace: t.TempDir()},
		engine: &MultiAgentEngine{agentFactory: factory},
	}
}

func TestSkillAPINameRejectsTraversalAndUnsafeComponents(t *testing.T) {
	invalid := []string{"", ".", "..", "../escape", `..\escape`, "a/b", `a\b`, "name:stream", "line\nbreak", "CON", "NUL.txt"}
	for _, name := range invalid {
		if _, ok := skilldomain.NormalizeName(name); ok {
			t.Errorf("unsafe skill name accepted: %q", name)
		}
	}
	for _, name := range []string{"report-writer", "数据审计", "team_skill.v2"} {
		if _, ok := skilldomain.NormalizeName(name); !ok {
			t.Errorf("safe skill name rejected: %q", name)
		}
	}
}

func TestSkillMutationRejectsTraversalAndPreservesWorkspace(t *testing.T) {
	workspace := t.TempDir()
	gateway := &GatewayService{config: &Config{Workspace: workspace}}
	sentinel := filepath.Join(workspace, "sentinel.txt")
	if err := os.WriteFile(sentinel, []byte("preserve me"), 0600); err != nil {
		t.Fatal(err)
	}

	saveBody := bytes.NewBufferString(`{"name":"..","content":"malicious"}`)
	saveRecorder := httptest.NewRecorder()
	gateway.handleSkillSave(saveRecorder, httptest.NewRequest(http.MethodPost, "/api/skill/save", saveBody))
	if saveRecorder.Code != http.StatusBadRequest {
		t.Fatalf("traversal save status=%d body=%s", saveRecorder.Code, saveRecorder.Body.String())
	}

	deleteRecorder := httptest.NewRecorder()
	gateway.handleSkillDelete(deleteRecorder, httptest.NewRequest(http.MethodPost, "/api/skill/delete?name=..", nil))
	if deleteRecorder.Code != http.StatusBadRequest {
		t.Fatalf("traversal delete status=%d body=%s", deleteRecorder.Code, deleteRecorder.Body.String())
	}
	data, err := os.ReadFile(sentinel)
	if err != nil || string(data) != "preserve me" {
		t.Fatalf("workspace sentinel changed after rejected traversal: data=%q err=%v", data, err)
	}
	if _, err := os.Stat(filepath.Join(workspace, "SKILL.md")); !os.IsNotExist(err) {
		t.Fatalf("traversal save created a file outside skills directory: %v", err)
	}
}

func TestSkillSaveAndDeleteStayInsideWorkspaceSkillDirectory(t *testing.T) {
	workspace := t.TempDir()
	gateway := &GatewayService{config: &Config{Workspace: workspace}}
	body := bytes.NewBufferString(`{"name":"数据审计","content":"# 数据审计\n"}`)
	saveRecorder := httptest.NewRecorder()
	gateway.handleSkillSave(saveRecorder, httptest.NewRequest(http.MethodPost, "/api/skill/save", body))
	if saveRecorder.Code != http.StatusOK {
		t.Fatalf("valid save status=%d body=%s", saveRecorder.Code, saveRecorder.Body.String())
	}
	skillFile := filepath.Join(workspace, "skills", "数据审计", "SKILL.md")
	if data, err := os.ReadFile(skillFile); err != nil || string(data) != "# 数据审计\n" {
		t.Fatalf("skill not saved inside workspace: data=%q err=%v", data, err)
	}

	deleteRecorder := httptest.NewRecorder()
	gateway.handleSkillDelete(deleteRecorder, httptest.NewRequest(http.MethodPost, "/api/skill/delete?name=%E6%95%B0%E6%8D%AE%E5%AE%A1%E8%AE%A1", nil))
	if deleteRecorder.Code != http.StatusOK {
		t.Fatalf("valid delete status=%d body=%s", deleteRecorder.Code, deleteRecorder.Body.String())
	}
	if _, err := os.Stat(filepath.Dir(skillFile)); !os.IsNotExist(err) {
		t.Fatalf("skill directory still exists after delete: %v", err)
	}
}

func TestSkillDetailUsesHTTPErrorStatuses(t *testing.T) {
	gateway := testGatewayWithLoadedSkill(t)
	invalid := httptest.NewRecorder()
	gateway.handleSkillDetail(invalid, httptest.NewRequest(http.MethodGet, "/api/skill/detail?name=..", nil))
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("invalid detail status=%d body=%s", invalid.Code, invalid.Body.String())
	}
	missing := httptest.NewRecorder()
	gateway.handleSkillDetail(missing, httptest.NewRequest(http.MethodGet, "/api/skill/detail?name=missing-skill", nil))
	if missing.Code != http.StatusNotFound {
		t.Fatalf("missing detail status=%d body=%s", missing.Code, missing.Body.String())
	}
}

func TestSkillListReturnsLoadedBuiltinsWhenUserDirectoryIsMissing(t *testing.T) {
	gateway := testGatewayWithLoadedSkill(t)
	recorder := httptest.NewRecorder()
	gateway.handleSkillList(recorder, httptest.NewRequest("GET", "/api/skills", nil))

	var response struct {
		OK     bool `json:"ok"`
		Skills []struct {
			Name             string   `json:"name"`
			Source           string   `json:"source"`
			Aliases          []string `json:"aliases"`
			DisplayName      string   `json:"display_name"`
			ShortDescription string   `json:"short_description"`
			DefaultPrompt    string   `json:"default_prompt"`
		} `json:"skills"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if !response.OK {
		t.Fatalf("skill list failed: %s", recorder.Body.String())
	}
	if len(response.Skills) != 1 || response.Skills[0].Name != "builtin-demo" || response.Skills[0].Source != "builtin" {
		t.Fatalf("loaded builtin skill missing from response: %s", recorder.Body.String())
	}
	if len(response.Skills[0].Aliases) != 1 || response.Skills[0].Aliases[0] != "demo" {
		t.Fatalf("loaded Skill aliases missing from response: %s", recorder.Body.String())
	}
	if response.Skills[0].DisplayName != "Builtin Demo" || response.Skills[0].ShortDescription != "Friendly summary" || response.Skills[0].DefaultPrompt != "Use $builtin-demo." {
		t.Fatalf("loaded Skill interface metadata missing from response: %s", recorder.Body.String())
	}
}

func TestSkillDetailReturnsLoadedBuiltinContent(t *testing.T) {
	gateway := testGatewayWithLoadedSkill(t)
	recorder := httptest.NewRecorder()
	gateway.handleSkillDetail(recorder, httptest.NewRequest("GET", "/api/skill/detail?name=builtin-demo", nil))

	var response struct {
		OK               bool   `json:"ok"`
		Name             string `json:"name"`
		Content          string `json:"content"`
		DisplayName      string `json:"display_name"`
		ShortDescription string `json:"short_description"`
		DefaultPrompt    string `json:"default_prompt"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if !response.OK || response.Name != "builtin-demo" || response.Content == "" ||
		response.DisplayName != "Builtin Demo" || response.ShortDescription != "Friendly summary" || response.DefaultPrompt != "Use $builtin-demo." {
		t.Fatalf("loaded builtin detail missing: %s", recorder.Body.String())
	}
}

func TestSkillListMergesInMemoryBuiltinAndUserDirectory(t *testing.T) {
	gateway := testGatewayWithLoadedSkill(t)
	workspace := t.TempDir()
	gateway.config.Workspace = workspace
	userDir := filepath.Join(workspace, "skills", "user-demo")
	if err := os.MkdirAll(userDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(userDir, "SKILL.md"), []byte("---\nname: user-demo\ndescription: user supplied\n---\n# User demo\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(userDir, "agents"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(userDir, "agents", "openai.yaml"), []byte("interface:\n  display_name: User Demo\n  short_description: User-facing summary\n  default_prompt: Use $user-demo.\n"), 0644); err != nil {
		t.Fatal(err)
	}

	recorder := httptest.NewRecorder()
	gateway.handleSkillList(recorder, httptest.NewRequest("GET", "/api/skills", nil))
	var response struct {
		OK     bool `json:"ok"`
		Skills []struct {
			Name             string `json:"name"`
			Source           string `json:"source"`
			DisplayName      string `json:"display_name"`
			ShortDescription string `json:"short_description"`
			DefaultPrompt    string `json:"default_prompt"`
		} `json:"skills"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	sources := make(map[string]string)
	interfaces := make(map[string][3]string)
	for _, skill := range response.Skills {
		sources[skill.Name] = skill.Source
		interfaces[skill.Name] = [3]string{skill.DisplayName, skill.ShortDescription, skill.DefaultPrompt}
	}
	if !response.OK || sources["builtin-demo"] != "builtin" || sources["user-demo"] != "user" {
		t.Fatalf("expected both builtin and user skills with distinct sources: %s", recorder.Body.String())
	}
	if got := interfaces["user-demo"]; got != [3]string{"User Demo", "User-facing summary", "Use $user-demo."} {
		t.Fatalf("disk Skill interface metadata missing from response: got=%v body=%s", got, recorder.Body.String())
	}
}
