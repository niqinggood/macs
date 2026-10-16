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
	"strings"
	"sync"
	"testing"

	"gopkg.in/yaml.v3"
	authcore "mac/internal/auth"
)

func TestPlatformAccountFlowsThroughQuestionAgentAndStreaming(t *testing.T) {
	t.Setenv("OPENKITTY_ACCOUNT_SERVICE_URL", "")
	var mu sync.Mutex
	var owners []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/nova/login" {
			var input struct{ Username string }
			_ = json.NewDecoder(r.Body).Decode(&input)
			http.SetCookie(w, &http.Cookie{Name: "auth_token", Value: input.Username + "-fixture-login", HttpOnly: true})
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "success", "user": input.Username})
			return
		}
		if r.URL.Path == "/nova/logout" {
			_, _ = io.WriteString(w, `{}`)
			return
		}
		if r.URL.Path != "/api/macs/v1/chat/completions" {
			t.Errorf("unexpected platform path %s", r.URL.Path)
			w.WriteHeader(404)
			return
		}
		owner := strings.TrimSuffix(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "), "-fixture-login")
		if owner != "alice" && owner != "bob" || r.Header.Get("Idempotency-Key") == "" {
			t.Error("wrong account identity or missing billing call identity")
		}
		mu.Lock()
		owners = append(owners, owner)
		mu.Unlock()
		var input struct{ Stream bool }
		_ = json.NewDecoder(r.Body).Decode(&input)
		if input.Stream {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"answer\"},\"finish_reason\":null}]}\n\ndata: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
		} else {
			_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"answer"},"finish_reason":"stop"}]}`)
		}
	}))
	defer server.Close()
	h := authcore.NewAccountHandler(authcore.AccountConfig{ServiceURL: server.URL})
	login := func(user string) *http.Cookie {
		r := httptest.NewRequest("POST", "http://localhost:8082/api/account/login", strings.NewReader(`{"username":"`+user+`","password":"fixture"}`))
		w := httptest.NewRecorder()
		h.HandleLogin(w, r)
		if w.Code != 200 || len(w.Result().Cookies()) != 1 {
			t.Fatal("fixture login failed")
		}
		return w.Result().Cookies()[0]
	}
	alice, bob := login("alice"), login("bob")
	config := &Config{DefaultModel: "platform", DefaultAgent: "Assistant", ModelConfig: map[string]ModelConfig{"platform": {Type: "openai", BaseURL: server.URL + "/api/macs/v1", ModelName: "platform:default", MaxTokens: 32}}, Agents: []AgentConfig{{Name: "Assistant", Type: AgentTypeCustom}}}
	if err := validateConfig(config); err != nil {
		t.Fatal("account model rejected without API key", err)
	}
	data, _ := yaml.Marshal(config)
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(configPath, data, 0600); err != nil {
		t.Fatal(err)
	}
	g := &GatewayService{accountHandler: h, configpath: configPath, logger: log.New(io.Discard, "", 0)}
	bind := func(cookie *http.Cookie) (*authcore.PlatformAccountSession, error) {
		r := httptest.NewRequest("POST", "http://localhost:8082/api/chat", nil)
		r.RemoteAddr = "127.0.0.1:1234"
		if cookie != nil {
			r.AddCookie(cookie)
		}
		return g.captureWebPlatformAccount(r, &MessageRequest{TaskType: "qa"})
	}
	if _, err := bind(nil); err == nil {
		t.Fatal("anonymous platform task accepted")
	}
	a, err := bind(alice)
	if err != nil {
		t.Fatal(err)
	}
	b, err := bind(bob)
	if err != nil {
		t.Fatal(err)
	}
	for _, account := range []*authcore.PlatformAccountSession{a, b} {
		if answer, err := g.simpleChat(account.Context(context.Background()), "hello", nil); err != nil || answer != "answer" {
			t.Fatalf("question failed: %q %v", answer, err)
		}
	}
	model := config.ModelConfig["platform"]
	payload, _ := json.Marshal(map[string]interface{}{"model": model})
	check := httptest.NewRequest("POST", "http://localhost:8082/api/config/model-test", strings.NewReader(string(payload)))
	check.RemoteAddr = "127.0.0.1:1234"
	check.AddCookie(alice)
	checked := httptest.NewRecorder()
	g.handleSettingsModelTest(checked, check)
	if checked.Code != 200 || !strings.Contains(checked.Body.String(), `"ok":true`) {
		t.Fatalf("login connection test failed: %d %s", checked.Code, checked.Body.String())
	}
	factory := NewAgentFactory(config.ModelConfig, nil, config, g.logger)
	created, err := factory.CreateEphemeralAgent(config.Agents[0])
	if err != nil {
		t.Fatal(err)
	}
	agent := created.(*LLMAgent)
	messages := []map[string]interface{}{{"role": "user", "content": "hello"}}
	if result, err := agent.callLLM(a.Context(context.Background()), messages); err != nil || result.Content != "answer" {
		t.Fatalf("Agent login failed: %v %v", result, err)
	}
	if result, err := agent.callLLMStream(b.Context(context.Background()), messages, nil, func(StreamChunk) {}); err != nil || result.Content != "answer" {
		t.Fatalf("stream login failed: %v %v", result, err)
	}
	logout := httptest.NewRequest("POST", "http://localhost:8082/api/account/logout", nil)
	logout.AddCookie(alice)
	h.HandleLogout(httptest.NewRecorder(), logout)
	if _, err := g.simpleChat(a.Context(context.Background()), "hello", nil); err == nil {
		t.Fatal("old task kept charging after logout")
	}
	if _, err := g.simpleChat(b.Context(context.Background()), "hello", nil); err != nil {
		t.Fatal("other account affected by logout", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if strings.Join(owners, ",") != "alice,bob,alice,alice,bob,bob" {
		t.Fatalf("task credentials crossed accounts: %v", owners)
	}
	if model := config.ModelConfig["platform"]; model.APIKey != "" || model.APIKeyEnv != "" {
		t.Fatal("login credential leaked into model configuration")
	}
}
