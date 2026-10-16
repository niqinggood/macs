package app

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mac/internal/skillpath"
	skilldomain "mac/internal/skills"
)

func newRemoteSkillTestGateway(t *testing.T, registry *RemoteSkillRegistry) (*GatewayService, string, *SkillsLoader) {
	t.Helper()
	base := t.TempDir()
	installDir := filepath.Join(base, "openkitty-bin", ".openkitty", "skills")
	loader := NewSkillsLoader(base, "test-agent", "")
	if err := os.MkdirAll(installDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := loader.ReloadUserSkills([]string{installDir}); err != nil {
		t.Fatal(err)
	}
	agent := &LLMAgent{name: "test-agent", workspace: base, skillsLoader: loader}
	factory := &AgentFactory{agents: map[string]Agent{"test-agent": agent}}
	return &GatewayService{
		config:              &Config{Workspace: filepath.Join(base, "workspace")},
		engine:              &MultiAgentEngine{agentFactory: factory},
		skillInstallDir:     installDir,
		remoteSkillRegistry: registry,
	}, installDir, loader
}

func TestRemoteTencentSkillSearchInstallAndHotReloadTextOnly(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/skills":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"code": 0,
				"data": map[string]interface{}{
					"skills": []map[string]interface{}{{
						"name":           "Lesson Helper",
						"slug":           "lesson-helper",
						"description":    "Plan lessons",
						"description_zh": "规划课程",
						"downloads":      42,
						"version":        "1.2.0",
						"verified":       true,
						"namespace":      map[string]string{"handle": "teacher"},
					}},
				},
			})
		case "/api/v1/skills/lesson-helper":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"latestVersion": map[string]string{"version": "1.2.0"},
				"namespace":     map[string]string{"handle": "teacher"},
				"skill": map[string]string{
					"displayName": "Lesson Helper",
					"summary_zh":  "规划课程",
				},
				"securityReports": map[string]interface{}{
					"scanner": map[string]string{"status": "safe", "statusText": "clean"},
				},
			})
		case "/api/v1/skills/lesson-helper/file":
			if r.URL.Query().Get("path") != "SKILL.md" || r.URL.Query().Get("version") != "1.2.0" {
				t.Errorf("unexpected Tencent file query: %s", r.URL.RawQuery)
			}
			w.Header().Set("Content-Type", "text/markdown")
			_, _ = io.WriteString(w, `---
name: lesson-helper
description: Plan a clear lesson.
type: shell
entrypoint: install.sh
always: true
read_when:
  - When the user asks for a lesson plan
---

# Lesson Helper

Prepare goals, examples, exercises, and a recap.
`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	registry := skilldomain.NewRemoteSkillRegistry(skilldomain.RemoteSkillRegistryOptions{
		Client:         upstream.Client(),
		TencentAPIBase: upstream.URL,
		ClawHubAPIBase: upstream.URL,
	})
	gateway, installDir, loader := newRemoteSkillTestGateway(t, registry)

	searchRecorder := httptest.NewRecorder()
	gateway.handleRemoteSkillSearch(
		searchRecorder,
		httptest.NewRequest(http.MethodGet, "/api/skills/search?source=tencent&q=lesson", nil),
	)
	if searchRecorder.Code != http.StatusOK || !strings.Contains(searchRecorder.Body.String(), `"slug":"lesson-helper"`) ||
		!strings.Contains(searchRecorder.Body.String(), `"installed":false`) {
		t.Fatalf("search status=%d body=%s", searchRecorder.Code, searchRecorder.Body.String())
	}

	crossOriginBody := bytes.NewBufferString(`{"source":"tencent","slug":"lesson-helper","version":"1.2.0"}`)
	crossOriginRequest := httptest.NewRequest(http.MethodPost, "/api/skills/install", crossOriginBody)
	crossOriginRequest.Header.Set("Origin", "https://attacker.example")
	crossOriginRecorder := httptest.NewRecorder()
	gateway.handleRemoteSkillInstall(crossOriginRecorder, crossOriginRequest)
	if crossOriginRecorder.Code != http.StatusForbidden {
		t.Fatalf("cross-origin install status=%d body=%s", crossOriginRecorder.Code, crossOriginRecorder.Body.String())
	}

	installBody := bytes.NewBufferString(`{"source":"tencent","slug":"lesson-helper","version":"1.2.0"}`)
	installRecorder := httptest.NewRecorder()
	gateway.handleRemoteSkillInstall(
		installRecorder,
		httptest.NewRequest(http.MethodPost, "/api/skills/install", installBody),
	)
	if installRecorder.Code != http.StatusCreated {
		t.Fatalf("install status=%d body=%s", installRecorder.Code, installRecorder.Body.String())
	}
	var response map[string]interface{}
	if err := json.Unmarshal(installRecorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response["text_only"] != true || int(response["reloaded"].(float64)) != 1 ||
		filepath.Clean(response["install_dir"].(string)) != filepath.Clean(installDir) {
		t.Fatalf("unexpected install response: %#v", response)
	}

	skillFile := filepath.Join(installDir, "lesson-helper", "SKILL.md")
	data, err := os.ReadFile(skillFile)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if !strings.Contains(content, "type: builtin") || strings.Contains(content, "entrypoint:") ||
		strings.Contains(content, "always: true") {
		t.Fatalf("remote executable metadata was not removed:\n%s", content)
	}
	if _, err := os.Stat(filepath.Join(installDir, "lesson-helper", "install.sh")); !os.IsNotExist(err) {
		t.Fatalf("installer downloaded a remote script: %v", err)
	}
	skill, err := loader.GetSkill("lesson-helper")
	if err != nil {
		t.Fatal(err)
	}
	if skill.Metadata.Type != skilldomain.SkillTypeBuiltin || skill.Metadata.Always || skill.RequiresRuntime {
		t.Fatalf("installed skill is executable or always-on: %#v", skill)
	}
	if origin, ok := readRemoteSkillOrigin(filepath.Dir(skillFile)); !ok || !origin.TextOnly ||
		origin.Source != skilldomain.SourceTencent || origin.Slug != "lesson-helper" {
		t.Fatalf("installed origin metadata is invalid: %#v ok=%t", origin, ok)
	}

	duplicateRecorder := httptest.NewRecorder()
	gateway.handleRemoteSkillInstall(
		duplicateRecorder,
		httptest.NewRequest(
			http.MethodPost,
			"/api/skills/install",
			bytes.NewBufferString(`{"source":"tencent","slug":"lesson-helper","version":"1.2.0"}`),
		),
	)
	if duplicateRecorder.Code != http.StatusConflict {
		t.Fatalf("duplicate install status=%d body=%s", duplicateRecorder.Code, duplicateRecorder.Body.String())
	}

	searchAgain := httptest.NewRecorder()
	gateway.handleRemoteSkillSearch(
		searchAgain,
		httptest.NewRequest(http.MethodGet, "/api/skills/search?source=tencent&q=lesson", nil),
	)
	if !strings.Contains(searchAgain.Body.String(), `"installed":true`) {
		t.Fatalf("installed skill was not marked in search: %s", searchAgain.Body.String())
	}
}

func TestRemoteClawHubSkillUsesOwnerScanAndActualSkillName(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/search":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"results": []map[string]interface{}{{
					"slug": "robot-drive", "displayName": "Robot Drive", "summary": "Drive a robot",
					"version": "2.0.0", "ownerHandle": "robotics",
				}},
			})
		case "/api/v1/skills/robot-drive":
			if r.URL.Query().Get("ownerHandle") != "robotics" {
				t.Errorf("missing ClawHub owner: %s", r.URL.RawQuery)
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"latestVersion": map[string]string{"version": "2.0.0"},
				"owner":         map[string]string{"handle": "robotics"},
				"skill":         map[string]string{"summary": "Drive a robot"},
			})
		case "/api/v1/skills/robot-drive/scan":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"security": map[string]string{"status": "benign"},
			})
		case "/api/v1/skills/robot-drive/file":
			_, _ = io.WriteString(w, `---
name: hardware-robot
description: Map robot requests to generic hardware tools.
---

# Hardware Robot

Use the active hardware specification to call uart, ros2, or plc_modbus.
`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	registry := skilldomain.NewRemoteSkillRegistry(skilldomain.RemoteSkillRegistryOptions{
		Client:         upstream.Client(),
		TencentAPIBase: upstream.URL,
		ClawHubAPIBase: upstream.URL,
	})
	gateway, installDir, loader := newRemoteSkillTestGateway(t, registry)

	search := httptest.NewRecorder()
	gateway.handleRemoteSkillSearch(
		search,
		httptest.NewRequest(http.MethodGet, "/api/skills/search?source=openclaw&q=robot", nil),
	)
	if search.Code != http.StatusOK || !strings.Contains(search.Body.String(), `"owner":"robotics"`) {
		t.Fatalf("ClawHub search status=%d body=%s", search.Code, search.Body.String())
	}

	install := httptest.NewRecorder()
	gateway.handleRemoteSkillInstall(
		install,
		httptest.NewRequest(
			http.MethodPost,
			"/api/skills/install",
			bytes.NewBufferString(`{"source":"clawhub","slug":"robot-drive","owner":"robotics","version":"2.0.0"}`),
		),
	)
	if install.Code != http.StatusCreated {
		t.Fatalf("ClawHub install status=%d body=%s", install.Code, install.Body.String())
	}
	if _, err := os.Stat(filepath.Join(installDir, "hardware-robot", "SKILL.md")); err != nil {
		t.Fatalf("skill was not installed under its validated actual name: %v", err)
	}
	if _, err := loader.GetSkill("hardware-robot"); err != nil {
		t.Fatalf("ClawHub skill was not hot-loaded: %v", err)
	}
}

func TestRemoteSkillUnsafeScanBlocksInstall(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/skills/danger":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"latestVersion": map[string]string{"version": "1.0.0"},
				"owner":         map[string]string{"handle": "attacker"},
			})
		case "/api/v1/skills/danger/scan":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"security": map[string]string{"status": "suspicious"},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	registry := skilldomain.NewRemoteSkillRegistry(skilldomain.RemoteSkillRegistryOptions{
		Client:         upstream.Client(),
		TencentAPIBase: upstream.URL,
		ClawHubAPIBase: upstream.URL,
	})
	gateway, installDir, _ := newRemoteSkillTestGateway(t, registry)
	recorder := httptest.NewRecorder()
	gateway.handleRemoteSkillInstall(
		recorder,
		httptest.NewRequest(
			http.MethodPost,
			"/api/skills/install",
			bytes.NewBufferString(`{"source":"clawhub","slug":"danger","owner":"attacker"}`),
		),
	)
	if recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("unsafe install status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if entries, err := os.ReadDir(installDir); err == nil && len(entries) != 0 {
		t.Fatalf("unsafe skill left installed files: %#v", entries)
	}
}

func TestExecutableSkillsDirIsBesideStartupFile(t *testing.T) {
	executable := filepath.Join(t.TempDir(), "release", "openkitty")
	got, err := skillpath.ForExecutable(executable)
	if err != nil {
		t.Fatal(err)
	}
	expected := filepath.Join(filepath.Dir(executable), ".openkitty", "skills")
	if filepath.Clean(got) != filepath.Clean(expected) {
		t.Fatalf("skills dir=%q want=%q", got, expected)
	}
	gateway := &GatewayService{
		config:          &Config{Workspace: filepath.Join(t.TempDir(), "unrelated-workspace")},
		skillInstallDir: got,
	}
	if filepath.Clean(gateway.writableSkillDir()) != filepath.Clean(expected) {
		t.Fatalf("gateway install dir=%q want=%q", gateway.writableSkillDir(), expected)
	}
}

func TestRemoteSkillPanelIncludesBothMarketplacesAndTextOnlyPolicy(t *testing.T) {
	data, err := embeddedWebFS.ReadFile("web/skill_panel.js")
	if err != nil {
		t.Fatal(err)
	}
	script := string(data)
	for _, expected := range []string{
		"腾讯 SkillHub",
		"OpenClaw / ClawHub",
		"/api/skills/sources",
		"/api/skills/search",
		"/api/skills/install",
		"只下载并校验",
		"不会下载或执行远端脚本",
	} {
		if !strings.Contains(script, expected) {
			t.Fatalf("skill panel is missing %q", expected)
		}
	}
}
