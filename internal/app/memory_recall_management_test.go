package app

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	memorycore "mac/internal/memory"
)

func TestPersonalMemoryPinAndRecallRecords(t *testing.T) {
	agent, database := newMemoryServiceTestAgentWithDB(t)
	// The local manager and the local runtime share the same trusted partition.
	config := &Config{Workspace: agent.workspace, Agents: []AgentConfig{{Name: "assistant"}, {Name: "other"}}}
	gateway := &GatewayService{config: config, engine: &MultiAgentEngine{config: config, agentFactory: &AgentFactory{agents: map[string]Agent{"assistant": agent, "other": &LLMAgent{}}}}, db: database}
	service, owner, _, err := gateway.personalMemoryService("assistant")
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Record(context.Background(), owner, "style", "secret concise preference", memorycore.MemoryUser, "preference", "active", 5, nil, 0); err != nil {
		t.Fatal(err)
	}
	request := func(method, path, body, remote, origin string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "http://localhost:8090"+path, strings.NewReader(body))
		r.RemoteAddr = remote
		r.Header.Set("Origin", origin)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		gateway.handlePersonalMemory(w, r)
		return w
	}
	action := func(name string, version int) *httptest.ResponseRecorder {
		body, _ := json.Marshal(map[string]interface{}{"agent": "assistant", "action": name, "key": "style", "scope": "user", "expected_version": version})
		return request("POST", "/api/memory", string(body), "127.0.0.1:5000", "http://localhost:8090")
	}
	if w := action("pin", 1); w.Code != http.StatusOK {
		t.Fatalf("pin: %d %s", w.Code, w.Body.String())
	}
	if w := action("unpin", 1); w.Code != http.StatusConflict {
		t.Fatalf("stale unpin: %d", w.Code)
	}
	ctx := context.WithValue(context.WithValue(context.Background(), "session_id", "private-session"), "task_id", "private-task")
	if prompt := agent.buildMemoryPrompt(ctx, "", "private-session", "nebula astronomy"); !strings.Contains(prompt, "secret concise preference") {
		t.Fatalf("pinned runtime: %s", prompt)
	}
	path := "/api/memory/recalls?agent=assistant"
	w := request("GET", path, "", "127.0.0.1:5000", "http://localhost:8090")
	var response struct {
		Traces []memorycore.RecallTrace `json:"traces"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &response) != nil || len(response.Traces) != 1 || response.Traces[0].Status != "ready" {
		t.Fatalf("trace: %d %s", w.Code, w.Body.String())
	}
	for _, secret := range []string{"secret concise preference", "private-session", "private-task", "nebula astronomy"} {
		if bytes.Contains(w.Body.Bytes(), []byte(secret)) {
			t.Fatalf("trace contains %q", secret)
		}
	}
	if w := request("GET", path, "", "192.0.2.1:5000", "http://localhost:8090"); w.Code != 403 {
		t.Fatal("remote traces exposed")
	}
	if w := request("GET", path, "", "127.0.0.1:5000", "https://foreign.test"); w.Code != 403 {
		t.Fatal("cross-origin traces exposed")
	}
	w = request("GET", "/api/memory/recalls?agent=other", "", "127.0.0.1:5000", "http://localhost:8090")
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &response) != nil || len(response.Traces) != 0 {
		t.Fatalf("foreign agent trace leaked: %s", w.Body.String())
	}
	if w := action("unpin", 2); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if prompt := agent.buildMemoryPrompt(ctx, "", "private-session", "nebula astronomy"); prompt != "" {
		t.Fatalf("unrelated unpinned recall: %s", prompt)
	}
	agent.hasMemory = false
	agent.buildMemoryPrompt(ctx, "", "private-session", "nebula astronomy")
	traces, err := service.RecentRecallTraces(context.Background(), owner, 20)
	if err != nil || len(traces) != 2 || traces[0].Status != "empty" {
		t.Fatalf("disabled trace or missing empty trace: %+v %v", traces, err)
	}
}
