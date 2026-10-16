package app

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	authcore "mac/internal/auth"
	"mac/internal/meetingminutes"
)

func selectionFixture() *Config {
	return &Config{
		DefaultAgent: "Assistant", DefaultModel: "one",
		ModelConfig: map[string]ModelConfig{
			"one": {Type: "openai", ModelName: "model-one", BaseURL: "http://localhost:1234/v1", APIKey: "fixture-one-key"},
			"two": {Type: "openai", ModelName: "model-two", BaseURL: "http://localhost:1234/v1", APIKey: "fixture-two-key"},
		},
		Agents: []AgentConfig{
			{Name: "Assistant", Type: AgentTypeAutoGenAssistant},
			{Name: "Specialist", Type: AgentTypeAutoGenAssistant, Model: "one", SystemMessage: "Keep this role", EnabledTools: []string{"read_file"}},
		},
	}
}

func TestComposerPlatformSelectionInstallsAccountModelAndCallsIt(t *testing.T) {
	t.Setenv("OPENKITTY_ACCOUNT_SERVICE_URL", "")
	var calls []string
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/nova/login":
			http.SetCookie(w, &http.Cookie{Name: "auth_token", Value: "alice-fixture-login", HttpOnly: true})
			_, _ = io.WriteString(w, `{"status":"success","user":"alice"}`)
		case "/api/macs/v1/catalog":
			if r.Method != http.MethodGet || r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
				t.Error("selection must use public discovery without credentials or generation")
			}
			_, _ = io.WriteString(w, `{"data":[{"id":"platform:chat","name":"Actual platform chat model","capability":"text","available":true},{"id":"platform:image","capability":"image","available":true},{"id":"platform:disabled","capability":"text","available":false}]}`)
		case "/api/macs/v1/chat/completions":
			var input struct{ Model string }
			_ = json.NewDecoder(r.Body).Decode(&input)
			if r.Header.Get("Authorization") != "Bearer alice-fixture-login" || r.Header.Get("Idempotency-Key") == "" {
				t.Error("selected platform model did not use the submitting login and billing identity")
			}
			calls = append(calls, input.Model)
			_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"answer"},"finish_reason":"stop"}]}`)
		default:
			t.Errorf("unexpected endpoint: %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer provider.Close()
	h := authcore.NewAccountHandler(authcore.AccountConfig{ServiceURL: provider.URL})
	config := selectionFixture()
	config.Gateway.AccountServiceURL = provider.URL
	// A configured model with a different billing identity must remain intact.
	config.ModelConfig["platform:chat"] = ModelConfig{Type: "openai", BaseURL: provider.URL + "/api/macs/v1", ModelName: "platform:chat", APIKey: "another-owner-key"}
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := SaveConfig(path, config); err != nil {
		t.Fatal(err)
	}
	g := &GatewayService{configpath: path, config: config, accountHandler: h, logger: log.New(io.Discard, "", 0), engine: selectionTestEngine(config)}
	put := func(fields map[string]string) *httptest.ResponseRecorder {
		body, _ := json.Marshal(fields)
		r := httptest.NewRequest(http.MethodPut, "http://localhost:8082/api/models", bytes.NewReader(body))
		r.RemoteAddr = "127.0.0.1:1234"
		w := httptest.NewRecorder()
		g.handleComposerModels(w, r)
		return w
	}
	revision, _ := configFileRevision(path)
	before, _ := os.ReadFile(path)
	for _, id := range []string{"platform:image", "platform:disabled", "platform:missing", "personal:private"} {
		if w := put(map[string]string{"platform_model": id, "config_revision": revision}); w.Code != 400 {
			t.Fatalf("invalid catalog selection %s: %d %s", id, w.Code, w.Body.String())
		}
	}
	if w := put(map[string]string{"platform_model": "platform:chat", "default_model": "one", "config_revision": revision}); w.Code != 400 {
		t.Fatal("ambiguous selection accepted")
	}
	if w := put(map[string]string{"platform_model": "platform:chat", "config_revision": "stale"}); w.Code != 409 {
		t.Fatal("stale catalog selection accepted")
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) || len(calls) != 0 {
		t.Fatal("invalid selection changed settings or called a paid model")
	}
	if w := put(map[string]string{"platform_model": "platform:chat", "config_revision": revision}); w.Code != 200 {
		t.Fatalf("platform selection failed: %d %s", w.Code, w.Body.String())
	}
	saved, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	selected := saved.ModelConfig[saved.DefaultModel]
	if saved.DefaultModel != "platform:chat-2" || selected.ModelName != "platform:chat" || selected.BaseURL != provider.URL+"/api/macs/v1" || selected.APIKey != "" || selected.APIKeyEnv != "" {
		t.Fatalf("wrong selected platform config: %s", saved.DefaultModel)
	}
	if saved.ModelConfig["platform:chat"].APIKey != "another-owner-key" || saved.ModelConfig["one"].APIKey != "fixture-one-key" || saved.Agents[1].Model != "one" {
		t.Fatal("selection overwrote custom credentials or Agent override")
	}
	revision, _ = configFileRevision(path)
	if w := put(map[string]string{"platform_model": "platform:chat", "config_revision": revision}); w.Code != 200 {
		t.Fatal("reselect failed")
	}
	reselected, _ := LoadConfig(path)
	if len(reselected.ModelConfig) != len(saved.ModelConfig) || reselected.DefaultModel != saved.DefaultModel || len(calls) != 0 {
		t.Fatal("selection duplicated configuration or generated a paid request")
	}
	login := httptest.NewRecorder()
	h.HandleLogin(login, httptest.NewRequest("POST", "http://localhost:8082/api/account/login", strings.NewReader(`{"username":"alice","password":"fixture"}`)))
	if login.Code != 200 || len(login.Result().Cookies()) != 1 {
		t.Fatal("fixture login failed")
	}
	r := httptest.NewRequest("POST", "http://localhost:8082/api/chat", nil)
	r.RemoteAddr = "127.0.0.1:1234"
	r.AddCookie(login.Result().Cookies()[0])
	bound, err := g.captureWebPlatformAccount(r, &MessageRequest{TaskType: "qa"})
	if err != nil {
		t.Fatal(err)
	}
	ctx := bound.Context(context.Background())
	if answer, err := g.simpleChat(ctx, "hello", nil); err != nil || answer != "answer" {
		t.Fatalf("selected model not used by simple chat: %q %v", answer, err)
	}
	_, agent, _, err := g.engine.NewAgentForTask("Assistant")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := agent.Generate(ctx, "hello"); err != nil {
		t.Fatal("selected model not used by Agent", err)
	}
	if strings.Join(calls, ",") != "platform:chat,platform:chat" {
		t.Fatalf("wrong model used: %v", calls)
	}
	if _, err := agent.Generate(context.Background(), "anonymous"); err == nil || len(calls) != 2 {
		t.Fatal("AutoGen assistant called the account model without login")
	}
}

func TestComposerPlatformSelectionUnavailableAndConcurrentEdit(t *testing.T) {
	t.Setenv("OPENKITTY_ACCOUNT_SERVICE_URL", "")
	path := filepath.Join(t.TempDir(), "config.yaml")
	config := selectionFixture()
	mode := "unavailable"
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/macs/v1/catalog" || r.Method != http.MethodGet {
			t.Fatal("selection called a non-discovery endpoint")
		}
		if mode == "unavailable" {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		// Another settings writer changes the file while discovery is in flight.
		changed := *config
		changed.TeamName = "Concurrent edit must survive"
		if err := SaveConfig(path, &changed); err != nil {
			t.Error(err)
		}
		_, _ = io.WriteString(w, `{"data":[{"id":"platform:chat","capability":"text","available":true}]}`)
	}))
	defer provider.Close()
	config.Gateway.AccountServiceURL = provider.URL
	if err := SaveConfig(path, config); err != nil {
		t.Fatal(err)
	}
	g := &GatewayService{configpath: path, config: config, logger: log.New(io.Discard, "", 0), engine: selectionTestEngine(config)}
	revision, _ := configFileRevision(path)
	before, _ := os.ReadFile(path)
	for _, test := range []struct {
		mode   string
		status int
	}{{"unavailable", 502}, {"concurrent", 409}} {
		mode = test.mode
		body, _ := json.Marshal(map[string]string{"platform_model": "platform:chat", "config_revision": revision})
		r := httptest.NewRequest(http.MethodPut, "http://localhost:8082/api/models", bytes.NewReader(body))
		r.RemoteAddr = "127.0.0.1:1234"
		w := httptest.NewRecorder()
		g.handleComposerModels(w, r)
		if w.Code != test.status {
			t.Fatalf("%s status = %d: %s", mode, w.Code, w.Body.String())
		}
		if mode == "unavailable" {
			after, _ := os.ReadFile(path)
			if !bytes.Equal(before, after) {
				t.Fatal("unavailable catalog changed configuration")
			}
		}
	}
	saved, err := LoadConfig(path)
	if err != nil || saved.TeamName != "Concurrent edit must survive" || saved.DefaultModel != "one" || len(saved.ModelConfig) != 2 {
		t.Fatal("selection overwrote the concurrent edit", err)
	}
}

func selectionTestEngine(config *Config) *MultiAgentEngine {
	logger := log.New(io.Discard, "", 0)
	factory := NewAgentFactory(config.ModelConfig, nil, config, logger)
	return &MultiAgentEngine{
		config: config, agentFactory: factory, logger: logger, fileLogger: logger,
		recorder: &ExecutionRecorder{logger: logger, meetingMinutes: meetingminutes.NewEnhancedMeetingMinutes()},
	}
}

func TestModelSelectionInheritanceAndRealCalls(t *testing.T) {
	var called []string
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Model string `json:"model"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		called = append(called, request.Model)
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"OK"}}]}`)
	}))
	defer provider.Close()
	config := selectionFixture()
	for name, model := range config.ModelConfig {
		model.BaseURL = provider.URL + "/v1"
		config.ModelConfig[name] = model
	}
	engine := selectionTestEngine(config)
	factory := engine.agentFactory
	oldAgent, err := factory.CreateEphemeralAgent(config.Agents[0])
	if err != nil {
		t.Fatal(err)
	}
	updated := *config
	updated.DefaultModel = "two"
	if err := engine.UpdateConfig(&updated); err != nil {
		t.Fatal(err)
	}
	newAgent, err := factory.CreateEphemeralAgent(config.Agents[0])
	if err != nil {
		t.Fatal(err)
	}
	explicitAgent, err := factory.CreateEphemeralAgent(config.Agents[1])
	if err != nil {
		t.Fatal(err)
	}
	for _, agent := range []Agent{oldAgent, newAgent, explicitAgent} {
		if _, err := agent.Generate(context.Background(), "Fixture message"); err != nil {
			t.Fatal(err)
		}
	}
	if strings.Join(called, ",") != "model-one,model-two,model-one" {
		t.Fatalf("calls = %v", called)
	}
	if config.Agents[0].Model != "" || config.Agents[1].Model != "one" {
		t.Fatal("inheritance modified stored agent overrides")
	}
	if err := validateConfig(&updated); err != nil {
		t.Fatal(err)
	}
	updated.DefaultModel = "missing"
	if err := validateConfig(&updated); err == nil {
		t.Fatal("invalid selection silently fell back")
	}
	if err := validateSettingsConfigForSave(&updated); err == nil {
		t.Fatal("settings accepted invalid selection")
	}
	updated.DefaultModel = ""
	updated.Agents[0].Model = "two"
	if selectedModelKey(&updated) != "two" {
		t.Fatal("legacy Assistant default was lost")
	}
}

func TestComposerModelsSafeCatalogAndSelection(t *testing.T) {
	config := selectionFixture()
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := SaveConfig(configPath, config); err != nil {
		t.Fatal(err)
	}
	g := &GatewayService{configpath: configPath, config: config, logger: log.New(io.Discard, "", 0), engine: selectionTestEngine(config)}
	request := func(method, body, remote, origin string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "http://localhost:8082/api/models", strings.NewReader(body))
		r.RemoteAddr = remote
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		w := httptest.NewRecorder()
		g.handleComposerModels(w, r)
		return w
	}
	catalog := request(http.MethodGet, "", "127.0.0.1:9999", "")
	if catalog.Code != 200 || strings.Contains(catalog.Body.String(), "fixture-one-key") || strings.Contains(catalog.Body.String(), "base_url") || strings.Contains(catalog.Body.String(), "api_key") {
		t.Fatalf("unsafe catalog: %s", catalog.Body.String())
	}
	var listing struct {
		Revision string `json:"config_revision"`
		Agents   []struct {
			Model    string `json:"model_config"`
			Override string `json:"model_override"`
		} `json:"agents"`
	}
	if err := json.Unmarshal(catalog.Body.Bytes(), &listing); err != nil {
		t.Fatal(err)
	}
	if listing.Agents[0].Model != "one" || listing.Agents[0].Override != "" || listing.Agents[1].Override != "one" {
		t.Fatal("incorrect catalog inheritance")
	}
	for _, test := range []struct{ remote, origin string }{{"192.0.2.1:9999", ""}, {"127.0.0.1:9999", "https://untrusted.example"}} {
		if w := request(http.MethodPut, `{}`, test.remote, test.origin); w.Code != http.StatusForbidden {
			t.Fatalf("boundary failed: %d", w.Code)
		}
	}
	before, _ := os.ReadFile(configPath)
	for _, body := range []string{`{"default_model":"missing","config_revision":"fixture"}`, `{"default_model":"two"}`, `{"default_model":"two","config_revision":"fixture","api_key":"injected"}`, `{"default_model":"two","config_revision":"fixture"} {}`} {
		if w := request(http.MethodPut, body, "127.0.0.1:9999", ""); w.Code != 400 {
			t.Fatalf("invalid write: %d %s", w.Code, w.Body.String())
		}
	}
	stale := request(http.MethodPut, `{"default_model":"two","config_revision":"stale"}`, "127.0.0.1:9999", "")
	if stale.Code != http.StatusConflict {
		t.Fatalf("stale revision = %d", stale.Code)
	}
	after, _ := os.ReadFile(configPath)
	if string(before) != string(after) {
		t.Fatal("rejected request wrote configuration")
	}
	body, _ := json.Marshal(map[string]string{"default_model": "two", "config_revision": listing.Revision, "inherit_agent": "Specialist"})
	if result := request(http.MethodPut, string(body), "127.0.0.1:9999", ""); result.Code != http.StatusOK {
		t.Fatalf("selection failed: %d %s", result.Code, result.Body.String())
	}
	saved, err := LoadConfig(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if saved.DefaultModel != "two" || saved.Agents[0].Model != "" || saved.Agents[1].Model != "" || saved.Agents[1].SystemMessage != "Keep this role" || saved.ModelConfig["one"].APIKey != "fixture-one-key" {
		t.Fatal("selection lost settings or did not persist")
	}
	_, agent, _, err := g.engine.NewAgentForTask("Specialist")
	if err != nil || agent.(*AutoGenAssistant).modelConfig.ModelName != "model-two" {
		t.Fatalf("next task did not use the selected model: %v", err)
	}
}
