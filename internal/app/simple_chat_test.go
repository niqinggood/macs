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
	"testing"

	"gopkg.in/yaml.v3"
)

func TestSimpleChatCallsSelectedModelWithHistoryAndNoTools(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var payload map[string]interface{}
		_ = json.NewDecoder(r.Body).Decode(&payload)
		if r.URL.Path != "/v1/chat/completions" || payload["model"] != "chosen-model" || payload["tools"] != nil || payload["tool_choice"] != nil {
			t.Errorf("unexpected model request %v", payload)
		}
		if r.Header.Get("Authorization") != "Bearer fixture-key" {
			t.Error("selected model key not resolved")
		}
		messages := payload["messages"].([]interface{})
		if len(messages) != 4 {
			t.Errorf("history count=%d", len(messages))
		}
		if messages[len(messages)-1].(map[string]interface{})["content"] != "@skill /pipeline explain this as text" {
			t.Error("simple question became an Agent command")
		}
		_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"direct answer"}}]}`)
	}))
	defer server.Close()
	t.Setenv("MACS_QA_FIXTURE_KEY", "fixture-key")
	config := &Config{DefaultModel: "chosen", ModelConfig: map[string]ModelConfig{"chosen": {Type: "openai", ModelName: "chosen-model", BaseURL: server.URL + "/v1", APIKeyEnv: "MACS_QA_FIXTURE_KEY"}}}
	data, _ := yaml.Marshal(config)
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	g := &GatewayService{configpath: path}
	answer, err := g.simpleChat(context.Background(), "@skill /pipeline explain this as text", []map[string]interface{}{{"role": "user", "content": "hello"}, {"role": "assistant", "content": "hi"}, {"role": "tool", "content": "private tool artifact"}})
	if err != nil || answer != "direct answer" || calls != 1 {
		t.Fatalf("answer=%q err=%v calls=%d", answer, err, calls)
	}
}

func TestSimpleChatRejectsSkillAndAttachmentsBeforeRouting(t *testing.T) {
	g := &GatewayService{logger: log.New(io.Discard, "", 0)}
	for _, body := range []string{`{"task_type":"qa","message":"hello","skill_run":{"skill_name":"sample"}}`, `{"task_type":"qa","message":"hello","attachments":[{"name":"sample.png"}]}`} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("POST", "/api/chat", strings.NewReader(body))
		g.handleChat(w, r)
		if w.Code != 400 {
			t.Fatalf("invalid QA request reached Agent routing: %d %s", w.Code, w.Body.String())
		}
	}
}
