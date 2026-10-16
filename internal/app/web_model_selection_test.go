package app

import (
	"context"
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
	"time"

	authcore "mac/internal/auth"
	"mac/internal/projectctx"
)

func webModelSelectionGateway(t *testing.T, baseURL string) *GatewayService {
	t.Helper()
	config := selectionFixture()
	config.Workspace = t.TempDir()
	config.Agents[0].Type = AgentTypeCustom
	config.Agents[0].Model = "one"
	config.Agents[0].SystemMessage = "Keep the original assistant role."
	config.Agents[0].EnabledTools = []string{"read_file"}
	config.Runtime.LLMCallTimeoutSeconds = 8
	for key, model := range config.ModelConfig {
		model.BaseURL = baseURL + "/v1"
		config.ModelConfig[key] = model
	}
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := SaveConfig(configPath, config); err != nil {
		t.Fatal(err)
	}
	logger := log.New(io.Discard, "", 0)
	factory := NewAgentFactory(config.ModelConfig, nil, config, logger)
	if _, err := factory.GetOrCreateAgent(config.Agents[0]); err != nil {
		t.Fatal(err)
	}
	engine := &MultiAgentEngine{config: config, agentFactory: factory, logger: logger, fileLogger: logger}
	return &GatewayService{config: config, configpath: configPath, engine: engine, logger: logger}
}

func webModelSelectionRequest(request *MessageRequest) *http.Request {
	body, _ := json.Marshal(request)
	r := httptest.NewRequest(http.MethodPost, "http://localhost:8082/api/chat", strings.NewReader(string(body)))
	r.RemoteAddr = "127.0.0.1:1200"
	r.Header.Set("Origin", "http://localhost:8082")
	return r
}

func TestWebModelSelectionFrozenModelAndRoleForQuestionAgentAndSkill(t *testing.T) {
	var mu sync.Mutex
	var called []string
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Model  string `json:"model"`
			Stream bool   `json:"stream"`
		}
		_ = json.NewDecoder(r.Body).Decode(&payload)
		if payload.Model != "model-two" || r.Header.Get("Authorization") != "Bearer fixture-two-key" {
			t.Error("accepted selection changed its model or credential after configuration reload")
		}
		mu.Lock()
		called = append(called, payload.Model)
		mu.Unlock()
		if payload.Stream {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"selected answer\"},\"finish_reason\":null}]}\n\ndata: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
		} else {
			_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"selected answer"},"finish_reason":"stop"}]}`)
		}
	}))
	defer provider.Close()
	g := webModelSelectionGateway(t, provider.URL)
	before, _ := os.ReadFile(g.configpath)
	originalAgents := append([]AgentConfig(nil), g.config.Agents...)
	var tasks []*GatewayTask
	for _, taskType := range []string{"qa", "chat", "skill"} {
		req := &MessageRequest{TaskType: taskType, AgentName: "Assistant", ModelConfig: "two", Channel: "web", Message: "fixture question"}
		if taskType == "skill" {
			req.SkillRun = &SkillRunRequest{SkillName: "fixture-skill"}
		}
		selection, _, err := g.captureWebModelSelection(webModelSelectionRequest(req), req)
		if err != nil {
			t.Fatal(err)
		}
		tasks = append(tasks, &GatewayTask{Request: req, webModelSelection: selection})
		if data, _ := json.Marshal(selection); string(data) != "{}" {
			t.Fatal("private model snapshot is serializable")
		}
	}
	afterCapture, _ := os.ReadFile(g.configpath)
	if string(afterCapture) != string(before) {
		t.Fatal("capturing a Web selection wrote settings")
	}
	// Change the configured alias, role and cached model after acceptance.
	changed, err := LoadConfig(g.configpath)
	if err != nil {
		t.Fatal(err)
	}
	changed.ModelConfig["two"] = changed.ModelConfig["one"]
	changed.Agents[0].SystemMessage = "Changed after acceptance"
	changed.Agents[0].EnabledTools = []string{"write_file"}
	if err := SaveConfig(g.configpath, changed); err != nil {
		t.Fatal(err)
	}
	afterChange, _ := os.ReadFile(g.configpath)
	g.engine.agentFactory.modelConfigs["two"].APIKey = "rotated-after-acceptance"
	for _, task := range tasks {
		if err := validateWebModelTaskSnapshot(task); err != nil {
			t.Fatal(err)
		}
		if task.Request.TaskType == "qa" {
			ctx := withWebModelSelection(context.Background(), task.webModelSelection)
			if answer, err := g.simpleChat(ctx, "fixture question", nil); err != nil || answer != "selected answer" {
				t.Fatalf("question failed: %q %v", answer, err)
			}
			continue
		}
		workspace := t.TempDir()
		name, created, found, cleanup, err := g.resolveChatAgentForGatewayTask(task, "Assistant", workspace)
		if err != nil || !found || name != "Assistant" {
			t.Fatalf("selected Agent could not be created: %v", err)
		}
		if cleanup != nil {
			defer cleanup()
		}
		agent := created.(*LLMAgent)
		if agent.modelKey != "two" || agent.systemMessage != originalAgents[0].SystemMessage || !reflect.DeepEqual(task.webModelSelection.agent.EnabledTools, originalAgents[0].EnabledTools) {
			t.Fatal("snapshot lost the selected model or original role/tool settings")
		}
		messages := []map[string]interface{}{{"role": "user", "content": "fixture question"}}
		if result, err := agent.callLLM(context.Background(), messages); err != nil || result.Content != "selected answer" {
			t.Fatalf("Agent or Skill call failed: %v", err)
		}
		if task.Request.TaskType == "chat" {
			if _, err := agent.callLLMStream(context.Background(), messages, nil, func(StreamChunk) {}); err != nil {
				t.Fatal(err)
			}
		}
	}
	if !reflect.DeepEqual(g.config.Agents, originalAgents) || g.config.DefaultModel != "one" {
		t.Fatal("a Web choice changed stored Agent bindings or defaults")
	}
	cached, _ := g.engine.agentFactory.GetAgent("Assistant")
	if cached.(*LLMAgent).modelKey != "one" {
		t.Fatal("a Web choice replaced the cached Agent")
	}
	after, _ := os.ReadFile(g.configpath)
	if string(after) != string(afterChange) {
		t.Fatal("executing a frozen choice wrote settings")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(called) != 4 {
		t.Fatalf("expected Q&A, Agent, streaming and Skill calls; got %d", len(called))
	}
}

func TestWebModelSelectionAcceptedQuestionUsesPrivateSnapshotAndPersistsOnlyAlias(t *testing.T) {
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct{ Model string }
		_ = json.NewDecoder(r.Body).Decode(&payload)
		if payload.Model != "model-two" {
			t.Error("accepted Web task did not attach its selected model context")
		}
		_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"accepted answer"},"finish_reason":"stop"}]}`)
	}))
	defer provider.Close()
	g := webModelSelectionGateway(t, provider.URL)
	g.db = openAgentOSFollowupAppDB(t)
	g.tasks = make(map[string]*GatewayTask)
	g.initSSE()
	g.project = projectctx.New(g.config.Workspace)
	before, _ := os.ReadFile(g.configpath)
	request := &MessageRequest{TaskType: "qa", ModelConfig: "two", Channel: "web", Message: "fixture question"}
	w := httptest.NewRecorder()
	g.handleChat(w, webModelSelectionRequest(request))
	if w.Code != http.StatusOK {
		t.Fatalf("task rejected: %d %s", w.Code, w.Body.String())
	}
	var accepted GatewayResponse
	if err := json.Unmarshal(w.Body.Bytes(), &accepted); err != nil || accepted.TaskID == "" {
		t.Fatal("Web submission did not return a task")
	}
	g.tasksMu.RLock()
	task := g.tasks[accepted.TaskID]
	g.tasksMu.RUnlock()
	select {
	case response := <-task.ResultCh:
		result := response.(GatewayResponse)
		if result.Status != "completed" || result.Result != "accepted answer" {
			t.Fatalf("accepted task failed: %#v", result)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("accepted task did not complete")
	}
	var stored GatewayTaskRecord
	if err := g.db.Where("task_id = ?", accepted.TaskID).First(&stored).Error; err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stored.RequestPayload, `"model_config":"two"`) || strings.Contains(stored.RequestPayload, "fixture-two-key") || strings.Contains(stored.RequestPayload, "base_url") || stored.RoleSnapshot != "" {
		t.Fatal("private Web snapshot was lost or serialized as credentials/AgentOS role")
	}
	after, _ := os.ReadFile(g.configpath)
	if string(before) != string(after) {
		t.Fatal("accepted task modified YAML bindings")
	}
}

func TestWebModelSelectionFreezesEnvironmentCredentialAndDefaultAgent(t *testing.T) {
	t.Setenv("MACS_WEB_MODEL_TEST_KEY", "accepted-test-key")
	g := webModelSelectionGateway(t, "http://localhost:1234")
	model := g.config.ModelConfig["two"]
	model.APIKey, model.APIKeyEnv = "", "MACS_WEB_MODEL_TEST_KEY"
	g.config.ModelConfig["two"] = model
	if err := SaveConfig(g.configpath, g.config); err != nil {
		t.Fatal(err)
	}
	req := &MessageRequest{TaskType: "chat", ModelConfig: "two", Channel: "web"}
	selection, _, err := g.captureWebModelSelection(webModelSelectionRequest(req), req)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("MACS_WEB_MODEL_TEST_KEY", "rotated-after-acceptance")
	g.config.DefaultAgent = "Specialist"
	if selection.model.APIKey != "accepted-test-key" || req.AgentName != "Assistant" || selection.agentName != "Assistant" {
		t.Fatal("task did not freeze the effective credential and default Agent")
	}
}

func TestWebModelSelectionRejectsInvalidAndForeignContracts(t *testing.T) {
	g := webModelSelectionGateway(t, "http://localhost:1234")
	g.config.ModelConfig["unavailable"] = ModelConfig{Type: "openai", ModelName: "missing-key", BaseURL: "https://model.example.test/v1"}
	if err := SaveConfig(g.configpath, g.config); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		status int
		mutate func(*http.Request, *MessageRequest)
	}{
		{"unknown", 400, func(_ *http.Request, req *MessageRequest) { req.ModelConfig = "unknown" }},
		{"unavailable", 400, func(_ *http.Request, req *MessageRequest) { req.ModelConfig = "unavailable" }},
		{"URL injection", 400, func(_ *http.Request, req *MessageRequest) { req.ModelConfig = "https://other.example.test/v1" }},
		{"remote", 403, func(r *http.Request, _ *MessageRequest) { r.RemoteAddr = "192.0.2.1:1234" }},
		{"foreign origin", 403, func(r *http.Request, _ *MessageRequest) { r.Header.Set("Origin", "https://foreign.example.test") }},
		{"cross site", 403, func(r *http.Request, _ *MessageRequest) { r.Header.Set("Sec-Fetch-Site", "cross-site") }},
		{"non-Web channel", 403, func(_ *http.Request, req *MessageRequest) { req.Channel = "qq" }},
		{"service identity", 403, func(r *http.Request, _ *MessageRequest) {
			*r = *r.WithContext(withExecutionIdentity(r.Context(), ExecutionIdentity{Service: true, Source: "agent_os", TenantID: "tenant", SubjectID: "alice"}))
		}},
		{"AgentOS route", 403, func(r *http.Request, _ *MessageRequest) { r.URL.Path = "/api/v1/agent/tasks" }},
		{"role spoof", 403, func(_ *http.Request, req *MessageRequest) { req.RoleID = "published" }},
		{"tenant spoof", 403, func(_ *http.Request, req *MessageRequest) { req.TenantID = "other" }},
		{"unsupported task", 400, func(_ *http.Request, req *MessageRequest) { req.TaskType = "task" }},
		{"collaboration", 400, func(_ *http.Request, req *MessageRequest) { req.CollaborationType = "parallel" }},
		{"pipeline", 400, func(_ *http.Request, req *MessageRequest) { req.Message = "/pipeline weekly fixture" }},
		{"unknown Agent", 400, func(_ *http.Request, req *MessageRequest) { req.AgentName = "unknown" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			req := &MessageRequest{TaskType: "chat", ModelConfig: "two", AgentName: "Assistant", Channel: "web"}
			r := webModelSelectionRequest(req)
			test.mutate(r, req)
			if _, status, err := g.captureWebModelSelection(r, req); err == nil || status != test.status {
				t.Fatalf("boundary accepted request: status=%d err=%v", status, err)
			}
		})
	}
	for _, body := range []string{`{"task_type":"chat","message":"fixture","model_config":{"base_url":"https://foreign.example.test","api_key":"injected"}}`, `{"task_type":"chat","message":"fixture","model_config":17}`} {
		r := httptest.NewRequest(http.MethodPost, "http://localhost:8082/api/chat", strings.NewReader(body))
		r.RemoteAddr = "127.0.0.1:1234"
		w := httptest.NewRecorder()
		g.handleChat(w, r)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("invalid model_config type reached routing: %d", w.Code)
		}
	}
	request := &MessageRequest{TaskType: "chat", Channel: "qq"}
	r := webModelSelectionRequest(request)
	r.RemoteAddr = "192.0.2.1:1234"
	if selection, _, err := g.captureWebModelSelection(r, request); err != nil || selection != nil {
		t.Fatal("legacy non-Web traffic changed without an explicit model")
	}
	for _, task := range []*GatewayTask{
		{Request: &MessageRequest{ModelConfig: "two", Channel: "web"}},
		{Request: &MessageRequest{ModelConfig: "two", Channel: "qq"}, webModelSelection: &webModelSelection{alias: "two"}},
		{QueueName: agentOSQueueName, Request: &MessageRequest{ModelConfig: "two", Channel: "web"}, webModelSelection: &webModelSelection{alias: "two"}},
	} {
		if validateWebModelTaskSnapshot(task) == nil {
			t.Fatal("missing or foreign snapshot could silently use another model")
		}
	}
}

func TestWebModelSelectionPlatformChargesSubmittingAccount(t *testing.T) {
	t.Setenv("OPENKITTY_ACCOUNT_SERVICE_URL", "")
	var mu sync.Mutex
	var owners []string
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/nova/login" {
			var input struct{ Username string }
			_ = json.NewDecoder(r.Body).Decode(&input)
			http.SetCookie(w, &http.Cookie{Name: "auth_token", Value: input.Username + "-fixture-login", HttpOnly: true})
			_, _ = io.WriteString(w, `{"status":"success"}`)
			return
		}
		var input struct{ Model string }
		_ = json.NewDecoder(r.Body).Decode(&input)
		if r.URL.Path != "/api/macs/v1/chat/completions" || input.Model != "platform:chosen" || r.Header.Get("Idempotency-Key") == "" {
			t.Error("explicit platform model or billing identity was lost")
		}
		owner := strings.TrimSuffix(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "), "-fixture-login")
		mu.Lock()
		owners = append(owners, owner)
		mu.Unlock()
		_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"platform answer"},"finish_reason":"stop"}]}`)
	}))
	defer provider.Close()
	g := webModelSelectionGateway(t, provider.URL)
	g.config.ModelConfig["platform"] = ModelConfig{Type: "openai", ModelName: "platform:chosen", BaseURL: provider.URL + "/api/macs/v1"}
	if err := SaveConfig(g.configpath, g.config); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(g.configpath)
	g.accountHandler = authcore.NewAccountHandler(authcore.AccountConfig{ServiceURL: provider.URL})
	for _, user := range []string{"alice", "bob"} {
		login := httptest.NewRequest(http.MethodPost, "http://localhost:8082/api/account/login", strings.NewReader(`{"username":"`+user+`","password":"fixture"}`))
		logged := httptest.NewRecorder()
		g.accountHandler.HandleLogin(logged, login)
		if logged.Code != 200 || len(logged.Result().Cookies()) != 1 {
			t.Fatal("fixture login failed")
		}
		cookie := logged.Result().Cookies()[0]
		for _, kind := range []string{"qa", "chat", "skill"} {
			req := &MessageRequest{TaskType: kind, AgentName: "Assistant", ModelConfig: "platform", Channel: "web"}
			r := webModelSelectionRequest(req)
			selection, _, err := g.captureWebModelSelection(r, req)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := g.captureWebPlatformAccount(r, req, selection); err == nil {
				t.Fatal("explicit account model followed old custom Agent and bypassed login")
			}
			r.AddCookie(cookie)
			account, err := g.captureWebPlatformAccount(r, req, selection)
			if err != nil {
				t.Fatal(err)
			}
			ctx := withWebModelSelection(account.Context(context.Background()), selection)
			if kind == "qa" {
				if _, err := g.simpleChat(ctx, "fixture", nil); err != nil {
					t.Fatal(err)
				}
			} else {
				task := &GatewayTask{Request: req, webModelSelection: selection}
				_, created, _, cleanup, err := g.resolveChatAgentForGatewayTask(task, "Assistant", t.TempDir())
				if cleanup != nil {
					defer cleanup()
				}
				if err != nil {
					t.Fatal(err)
				}
				if _, err := created.(*LLMAgent).callLLM(ctx, []map[string]interface{}{{"role": "user", "content": "fixture"}}); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	after, _ := os.ReadFile(g.configpath)
	if string(before) != string(after) || g.config.Agents[0].Model != "one" || g.config.Agents[1].Model != "one" {
		t.Fatal("explicit platform selection changed YAML Agent bindings")
	}
	mu.Lock()
	defer mu.Unlock()
	if strings.Join(owners, ",") != "alice,alice,alice,bob,bob,bob" {
		t.Fatalf("platform calls crossed submitting accounts: %v", owners)
	}
}
