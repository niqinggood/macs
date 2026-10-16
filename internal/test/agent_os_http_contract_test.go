package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const validAgentOSTestTraceparent = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"

func TestAgentOSRequestMiddlewarePropagatesSafeCorrelationHeaders(t *testing.T) {
	const requestID = "crm.sync-42:attempt_1"
	called := false
	handler := agentOSRequestMiddleware(func(w http.ResponseWriter, r *http.Request) {
		called = true
		if got := agentOSRequestIDFromContext(r.Context()); got != requestID {
			t.Fatalf("request id in context = %q, want %q", got, requestID)
		}
		if got := agentOSTraceparentFromContext(r.Context()); got != validAgentOSTestTraceparent {
			t.Fatalf("traceparent in context = %q, want %q", got, validAgentOSTestTraceparent)
		}
		w.WriteHeader(http.StatusNoContent)
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/agent/tasks/task-1", nil)
	req.Header.Set(agentOSRequestIDHeader, requestID)
	req.Header.Set(agentOSTraceparentHeader, strings.ToUpper(validAgentOSTestTraceparent))
	recorder := httptest.NewRecorder()

	handler(recorder, req)

	if !called {
		t.Fatal("wrapped handler was not called")
	}
	if got := recorder.Header().Get(agentOSRequestIDHeader); got != requestID {
		t.Fatalf("response request id = %q, want %q", got, requestID)
	}
	if got := recorder.Header().Get(agentOSTraceparentHeader); got != validAgentOSTestTraceparent {
		t.Fatalf("response traceparent = %q, want %q", got, validAgentOSTestTraceparent)
	}
}

func TestAgentOSRequestMiddlewareRejectsUnsafeCorrelationHeaders(t *testing.T) {
	handler := agentOSRequestMiddleware(func(w http.ResponseWriter, r *http.Request) {
		if got := agentOSRequestIDFromContext(r.Context()); got == "" || got == "unsafe request id" {
			t.Fatalf("generated request id = %q", got)
		}
		if got := agentOSTraceparentFromContext(r.Context()); got != "" {
			t.Fatalf("invalid traceparent reached context: %q", got)
		}
		w.WriteHeader(http.StatusNoContent)
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/agent/tasks/task-1", nil)
	req.Header.Set(agentOSRequestIDHeader, "unsafe request id")
	req.Header.Set(agentOSTraceparentHeader, "00-not-a-traceparent")
	recorder := httptest.NewRecorder()

	handler(recorder, req)

	responseID := recorder.Header().Get(agentOSRequestIDHeader)
	if !validAgentOSRequestID(responseID) || responseID == "unsafe request id" {
		t.Fatalf("unsafe response request id %q", responseID)
	}
	if got := recorder.Header().Get(agentOSTraceparentHeader); got != "" {
		t.Fatalf("invalid traceparent was reflected: %q", got)
	}
}

func TestSendAgentOSJSONErrorHasStableContractAndPreservesRetryAfter(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/agent/tasks", nil)
	req = req.WithContext(withAgentOSHTTPTrace(req.Context(), "upstream-42", validAgentOSTestTraceparent))
	recorder := httptest.NewRecorder()
	recorder.Header().Set("Retry-After", "2")

	sendAgentOSJSONError(
		recorder,
		req,
		"queue_full",
		"AgentOS task queue is full",
		http.StatusTooManyRequests,
		true,
	)

	if recorder.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusTooManyRequests)
	}
	if got := recorder.Header().Get("Retry-After"); got != "2" {
		t.Fatalf("Retry-After = %q, want 2", got)
	}
	if got := recorder.Header().Get(agentOSRequestIDHeader); got != "upstream-42" {
		t.Fatalf("X-Request-ID = %q, want upstream-42", got)
	}
	var response agentOSErrorResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Error != response.Message || response.Message != "AgentOS task queue is full" {
		t.Fatalf("compatibility fields differ: %+v", response)
	}
	if response.Code != "queue_full" || response.RequestID != "upstream-42" || !response.Retryable {
		t.Fatalf("unexpected error contract: %+v", response)
	}
}

func TestSendAgentOSJSONErrorDoesNotLeakInternalDetails(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/agent/tasks", nil)
	recorder := httptest.NewRecorder()

	sendAgentOSJSONError(
		recorder,
		req,
		"database_failure",
		`dial mysql tenant_secret@10.0.0.8: password="do-not-leak"`,
		http.StatusInternalServerError,
		true,
	)

	var response agentOSErrorResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Message != "internal server error" || response.Error != response.Message {
		t.Fatalf("internal message was not sanitized: %+v", response)
	}
	for _, sensitive := range []string{"mysql", "10.0.0.8", "do-not-leak", "tenant_secret"} {
		if strings.Contains(recorder.Body.String(), sensitive) {
			t.Fatalf("response leaked %q: %s", sensitive, recorder.Body.String())
		}
	}
	if response.RequestID == "" || recorder.Header().Get(agentOSRequestIDHeader) != response.RequestID {
		t.Fatalf("request id is missing or inconsistent: header=%q body=%q",
			recorder.Header().Get(agentOSRequestIDHeader), response.RequestID)
	}
}

func TestAgentOSTraceContextPersistsInTaskRequestPayload(t *testing.T) {
	db := openAgentOSScenarioDB(t)
	now := time.Now().Round(0)
	task := &GatewayTask{
		ID:        "agt_trace_contract",
		TenantID:  defaultExecutionTenantID,
		RoleID:    "Researcher",
		Status:    "pending",
		CreatedAt: now,
		Request: &MessageRequest{
			TaskID:      "agt_trace_contract",
			TaskType:    "chat",
			SessionId:   "trace-session",
			TenantID:    defaultExecutionTenantID,
			RoleID:      "Researcher",
			Message:     "trace me",
			AgentName:   "Researcher",
			Channel:     "api",
			CreatedAt:   now,
			RequestID:   "caller-request-7",
			Traceparent: validAgentOSTestTraceparent,
		},
	}
	if err := SaveGatewayTaskToDB(db, task); err != nil {
		t.Fatalf("save task: %v", err)
	}
	var record GatewayTaskRecord
	if err := db.Where("task_id = ?", task.ID).First(&record).Error; err != nil {
		t.Fatalf("load task record: %v", err)
	}
	restored := gatewayTaskFromRecord(record)
	if restored.Request == nil {
		t.Fatal("restored task request is nil")
	}
	if restored.Request.RequestID != task.Request.RequestID {
		t.Fatalf("request id = %q, want %q", restored.Request.RequestID, task.Request.RequestID)
	}
	if restored.Request.Traceparent != validAgentOSTestTraceparent {
		t.Fatalf("traceparent = %q, want %q", restored.Request.Traceparent, validAgentOSTestTraceparent)
	}
	ctx := withAgentOSHTTPTrace(context.Background(), restored.Request.RequestID, restored.Request.Traceparent)
	if got := agentOSTraceparentFromContext(ctx); got != validAgentOSTestTraceparent {
		t.Fatalf("restored context traceparent = %q", got)
	}
}

func TestAgentOSRejectedAuthenticationIsRateLimitedByPeer(t *testing.T) {
	gateway := &GatewayService{config: &Config{Gateway: GatewayConfig{AuthEnabled: true}}}
	handler := gateway.agentOSRateLimitMiddleware(func(w http.ResponseWriter, r *http.Request) {
		sendAgentOSStatusError(w, r, "unauthorized", http.StatusUnauthorized)
	})
	for attempt := 1; attempt <= 11; attempt++ {
		request := httptest.NewRequest(http.MethodGet, "/api/v1/agent/roles", nil)
		request.RemoteAddr = "198.51.100.77:43210"
		request.Header.Set("Authorization", "Bearer invalid-token")
		recorder := httptest.NewRecorder()
		handler(recorder, request)
		if attempt <= 10 && recorder.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d status=%d body=%s", attempt, recorder.Code, recorder.Body.String())
		}
		if attempt == 11 && recorder.Code != http.StatusTooManyRequests {
			t.Fatalf("rate limit status=%d body=%s", recorder.Code, recorder.Body.String())
		}
	}
}

func TestAgentOSSSEConnectionLimitIsPerIdentityAndReleasable(t *testing.T) {
	gateway := &GatewayService{}
	identity := ExecutionIdentity{
		TenantID: "tenant-a", ServiceID: "crm-service",
		Service: true, TenantScoped: true,
	}
	releases := make([]func(), 0, maxAgentOSSSEPerIdentity)
	for index := 0; index < maxAgentOSSSEPerIdentity; index++ {
		release, ok := gateway.acquireAgentOSSSEConnection(identity)
		if !ok {
			t.Fatalf("connection %d was rejected before the configured limit", index+1)
		}
		releases = append(releases, release)
	}
	if _, ok := gateway.acquireAgentOSSSEConnection(identity); ok {
		t.Fatal("connection above the per-identity limit was accepted")
	}
	releases[0]()
	release, ok := gateway.acquireAgentOSSSEConnection(identity)
	if !ok {
		t.Fatal("released connection capacity was not reusable")
	}
	release()
	for _, release := range releases[1:] {
		release()
	}
}

func TestAgentOSSubmitRejectsTrailingJSONValues(t *testing.T) {
	gateway, _ := newAgentOSAPIScenarioGateway(
		t,
		&agentOSAPIScenarioAgent{result: "unused"},
	)
	req := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/agent/tasks",
		strings.NewReader(
			`{"message":"first","agent_name":"Researcher"} {"message":"second"}`,
		),
	)
	recorder := httptest.NewRecorder()
	gateway.handleAgentOSTasks(recorder, req)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", recorder.Code, recorder.Body.String())
	}
	var response agentOSErrorResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if response.Code != "invalid_request" {
		t.Fatalf("error code = %q, want invalid_request", response.Code)
	}
}

func TestAgentOSTaskResponsePreservesAccountInfoInLiveResultAndError(t *testing.T) {
	gateway := &GatewayService{}
	task := &GatewayTask{
		ID: "secret-result-task", QueueName: agentOSQueueName,
		Status: "failed",
		Result: map[string]interface{}{
			"api_key": "super-secret-response-value",
		},
		Error: fmt.Errorf("provider rejected Bearer abcdefghijklmnopqrstuvwxyz"),
	}
	response := gateway.agentOSTaskResponse(task, false)
	encoded, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	text := string(encoded)
	for _, accountValue := range []string{
		"super-secret-response-value",
		"abcdefghijklmnopqrstuvwxyz",
	} {
		if !strings.Contains(text, accountValue) {
			t.Fatalf("live task response lost %q: %s", accountValue, text)
		}
	}
	if strings.Contains(text, "[REDACTED]") || strings.Contains(text, "[SCRUBBED]") {
		t.Fatalf("live task response contains a forced-redaction marker: %s", text)
	}
}
