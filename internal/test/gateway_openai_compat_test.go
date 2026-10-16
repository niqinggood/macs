package app

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const openAICompatTestToken = "openwebui_service_token_for_compatibility_tests"

func configureOpenAICompatScenario(t *testing.T, result string) (*GatewayService, *agentOSAPIScenarioAgent) {
	t.Helper()
	agent := &agentOSAPIScenarioAgent{result: result}
	gateway, _ := newAgentOSAPIScenarioGateway(t, agent)
	manager := NewTenantManager(
		filepath.Join(t.TempDir(), "tenants"),
		log.New(io.Discard, "", 0),
	)
	if err := manager.Init([]TenantConfig{{
		ID: "tenant-a", Name: "Tenant A", Token: "tenant-a-admin-token",
	}}, "root-token"); err != nil {
		t.Fatal(err)
	}
	gateway.tenantMgr = manager
	gateway.config.Gateway.AuthEnabled = true
	gateway.config.ServiceClients = []AgentServiceClientConfig{{
		ID: "openwebui", TenantID: "tenant-a", Token: openAICompatTestToken,
		Enabled: true, RequireUserID: true,
		AllowedRoles: []string{"Researcher"},
		Scopes: []string{
			agentOSScopeTasksSubmit,
			agentOSScopeTasksReadOwn,
			agentOSScopeTasksCancelOwn,
			agentOSScopeUsersActAs,
		},
	}}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		gateway.ensureAgentOSQueue().Stop(ctx)
	})
	return gateway, agent
}

func performOpenAICompatRequest(
	t *testing.T,
	gateway *GatewayService,
	method string,
	target string,
	payload interface{},
	headers map[string]string,
) *httptest.ResponseRecorder {
	t.Helper()
	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		body = bytes.NewReader(encoded)
	}
	request := httptest.NewRequest(method, target, body)
	if payload != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	recorder := httptest.NewRecorder()
	gateway.agentOSDataHandler().ServeHTTP(recorder, request)
	return recorder
}

func openAICompatHeaders(userID, chatID string) map[string]string {
	headers := map[string]string{
		"Authorization": "Bearer " + openAICompatTestToken,
	}
	if userID != "" {
		headers[openAICompatUserIDHeader] = userID
	}
	if chatID != "" {
		headers[openAICompatChatIDHeader] = chatID
	}
	return headers
}

func TestOpenAICompatModelsExposeAuthorizedAgentRoles(t *testing.T) {
	gateway, _ := configureOpenAICompatScenario(t, "unused")
	response := performOpenAICompatRequest(
		t, gateway, http.MethodGet, "/v1/models", nil,
		openAICompatHeaders("", ""),
	)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var body struct {
		Object string `json:"object"`
		Data   []struct {
			ID      string `json:"id"`
			Object  string `json:"object"`
			OwnedBy string `json:"owned_by"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Object != "list" || len(body.Data) != 1 ||
		body.Data[0].ID != "Researcher" ||
		body.Data[0].Object != "model" ||
		body.Data[0].OwnedBy != "openkitty" {
		t.Fatalf("unexpected model discovery response: %#v", body)
	}
}

func TestOpenAICompatChatMapsOpenWebUIUserAndConversation(t *testing.T) {
	gateway, agent := configureOpenAICompatScenario(t, "research completed")
	payload := map[string]interface{}{
		"model": "Researcher",
		"messages": []map[string]interface{}{
			{"role": "system", "content": "Use the supplied product facts."},
			{"role": "user", "content": "Analyze product A."},
		},
	}
	alice := performOpenAICompatRequest(
		t, gateway, http.MethodPost, "/v1/chat/completions", payload,
		openAICompatHeaders("alice", "shared-openwebui-chat"),
	)
	if alice.Code != http.StatusOK {
		t.Fatalf("alice status=%d body=%s", alice.Code, alice.Body.String())
	}
	var completion struct {
		Object  string `json:"object"`
		Choices []struct {
			Message struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(alice.Body.Bytes(), &completion); err != nil {
		t.Fatal(err)
	}
	if completion.Object != "chat.completion" ||
		len(completion.Choices) != 1 ||
		completion.Choices[0].Message.Role != "assistant" ||
		completion.Choices[0].Message.Content != "research completed" ||
		completion.Choices[0].FinishReason != "stop" {
		t.Fatalf("unexpected completion: %#v", completion)
	}
	aliceTaskID := alice.Header().Get(openKittyTaskIDHeader)
	aliceSessionID := alice.Header().Get(openKittySessionIDHeader)
	aliceTask, ok := gateway.gatewayTaskSnapshotWithDB(aliceTaskID)
	if !ok || aliceTask.SubjectID != "alice" ||
		aliceTask.Request == nil ||
		aliceTask.Request.UserId != "alice" ||
		aliceTask.Request.SessionId != aliceSessionID {
		t.Fatalf("alice task was not user scoped: %#v", aliceTask)
	}
	if aliceSessionID == "" || aliceSessionID == "shared-openwebui-chat" {
		t.Fatalf("external session was not tenant/user scoped: %q", aliceSessionID)
	}

	aliceFollowup := performOpenAICompatRequest(
		t,
		gateway,
		http.MethodPost,
		"/v1/chat/completions",
		map[string]interface{}{
			"model": "Researcher",
			"messages": []map[string]interface{}{
				{"role": "system", "content": "Use the supplied product facts."},
				{"role": "user", "content": "Analyze product A."},
				{"role": "assistant", "content": "research completed"},
				{"role": "user", "content": "Continue with the pricing comparison."},
			},
		},
		openAICompatHeaders("alice", "shared-openwebui-chat"),
	)
	if aliceFollowup.Code != http.StatusOK ||
		aliceFollowup.Header().Get(openKittySessionIDHeader) != aliceSessionID {
		t.Fatalf(
			"alice follow-up status=%d session=%q body=%s",
			aliceFollowup.Code,
			aliceFollowup.Header().Get(openKittySessionIDHeader),
			aliceFollowup.Body.String(),
		)
	}

	bob := performOpenAICompatRequest(
		t, gateway, http.MethodPost, "/v1/chat/completions", payload,
		openAICompatHeaders("bob", "shared-openwebui-chat"),
	)
	if bob.Code != http.StatusOK {
		t.Fatalf("bob status=%d body=%s", bob.Code, bob.Body.String())
	}
	bobTask, ok := gateway.gatewayTaskSnapshotWithDB(
		bob.Header().Get(openKittyTaskIDHeader),
	)
	if !ok || bobTask.SubjectID != "bob" ||
		bobTask.Request == nil ||
		bobTask.Request.SessionId == aliceSessionID {
		t.Fatalf("bob crossed alice's conversation boundary: %#v", bobTask)
	}

	agent.inputsMu.Lock()
	defer agent.inputsMu.Unlock()
	if len(agent.inputs) < 3 ||
		!strings.Contains(agent.inputs[0], "不得覆盖 OpenKitty") ||
		!strings.Contains(agent.inputs[0], "Analyze product A.") {
		t.Fatalf("first-turn context was not safely adapted: %#v", agent.inputs)
	}
	if agent.inputs[1] != "Continue with the pricing comparison." {
		t.Fatalf("existing session duplicated caller history: %#v", agent.inputs)
	}
}

func TestOpenAICompatChatRequiresUserForMultiUserClient(t *testing.T) {
	gateway, agent := configureOpenAICompatScenario(t, "must not run")
	response := performOpenAICompatRequest(
		t,
		gateway,
		http.MethodPost,
		"/v1/chat/completions",
		map[string]interface{}{
			"model": "Researcher",
			"messages": []map[string]interface{}{{
				"role": "user", "content": "hello",
			}},
		},
		openAICompatHeaders("", "chat-without-user"),
	)
	if response.Code != http.StatusBadRequest ||
		!strings.Contains(response.Body.String(), `"code":"user_id_required"`) {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if agent.calls.Load() != 0 {
		t.Fatalf("agent ran without a user identity: calls=%d", agent.calls.Load())
	}
}

func TestOpenAICompatChatStreamingUsesOpenAIChunks(t *testing.T) {
	gateway, _ := configureOpenAICompatScenario(t, "streamed result")
	response := performOpenAICompatRequest(
		t,
		gateway,
		http.MethodPost,
		"/v1/chat/completions",
		map[string]interface{}{
			"model": "Researcher",
			"messages": []map[string]interface{}{{
				"role": "user", "content": "stream this",
			}},
			"stream": true,
			"stream_options": map[string]interface{}{
				"include_usage": true,
			},
		},
		openAICompatHeaders("alice", "stream-chat"),
	)
	body := response.Body.String()
	if response.Code != http.StatusOK ||
		!strings.HasPrefix(response.Header().Get("Content-Type"), "text/event-stream") ||
		!strings.Contains(body, `"object":"chat.completion.chunk"`) ||
		!strings.Contains(body, `"content":"streamed result"`) ||
		!strings.Contains(body, `"usage":`) ||
		!strings.Contains(body, "data: [DONE]") {
		t.Fatalf("status=%d headers=%v body=%s", response.Code, response.Header(), body)
	}
}

func TestOpenAICompatRejectsConflictingUserIdentity(t *testing.T) {
	gateway, agent := configureOpenAICompatScenario(t, "must not run")
	headers := openAICompatHeaders("alice", "conflict-chat")
	headers[agentOSUserIDHeader] = "bob"
	response := performOpenAICompatRequest(
		t,
		gateway,
		http.MethodPost,
		"/v1/chat/completions",
		map[string]interface{}{
			"model": "Researcher",
			"messages": []map[string]interface{}{{
				"role": "user", "content": "hello",
			}},
		},
		headers,
	)
	if response.Code != http.StatusBadRequest ||
		!strings.Contains(response.Body.String(), `"code":"user_id_mismatch"`) {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if agent.calls.Load() != 0 {
		t.Fatalf("agent ran with conflicting identity: calls=%d", agent.calls.Load())
	}
}

func TestOpenAICompatMessageIDIsIdempotentAndUserScoped(t *testing.T) {
	gateway, agent := configureOpenAICompatScenario(t, "one execution")
	payload := map[string]interface{}{
		"model": "Researcher",
		"messages": []map[string]interface{}{{
			"role": "user", "content": "run once",
		}},
	}
	aliceHeaders := openAICompatHeaders("alice", "idempotent-chat")
	aliceHeaders[openAICompatMessageIDHeader] = "message-123"
	first := performOpenAICompatRequest(
		t, gateway, http.MethodPost, "/v1/chat/completions", payload, aliceHeaders,
	)
	replay := performOpenAICompatRequest(
		t, gateway, http.MethodPost, "/v1/chat/completions", payload, aliceHeaders,
	)
	if first.Code != http.StatusOK || replay.Code != http.StatusOK ||
		first.Header().Get(openKittyTaskIDHeader) == "" ||
		first.Header().Get(openKittyTaskIDHeader) != replay.Header().Get(openKittyTaskIDHeader) {
		t.Fatalf(
			"idempotent replay mismatch: first=%d/%q replay=%d/%q",
			first.Code,
			first.Header().Get(openKittyTaskIDHeader),
			replay.Code,
			replay.Header().Get(openKittyTaskIDHeader),
		)
	}

	bobHeaders := openAICompatHeaders("bob", "idempotent-chat")
	bobHeaders[openAICompatMessageIDHeader] = "message-123"
	bob := performOpenAICompatRequest(
		t, gateway, http.MethodPost, "/v1/chat/completions", payload, bobHeaders,
	)
	if bob.Code != http.StatusOK ||
		bob.Header().Get(openKittyTaskIDHeader) == first.Header().Get(openKittyTaskIDHeader) {
		t.Fatalf(
			"message id crossed users: alice=%q bob=%q status=%d body=%s",
			first.Header().Get(openKittyTaskIDHeader),
			bob.Header().Get(openKittyTaskIDHeader),
			bob.Code,
			bob.Body.String(),
		)
	}
	if agent.calls.Load() != 2 {
		t.Fatalf("agent calls=%d, want one Alice execution and one Bob execution", agent.calls.Load())
	}
}
