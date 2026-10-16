package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type agentOSBusinessMockModel struct {
	requests              atomic.Int32
	sawStructuredParams   atomic.Bool
	sawCustomerToolResult atomic.Bool
	sawSecondUserResult   atomic.Bool
}

func (m *agentOSBusinessMockModel) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || r.URL.Path != "/v1/chat/completions" {
		http.NotFound(w, r)
		return
	}
	m.requests.Add(1)
	var request struct {
		Messages []map[string]interface{} `json:"messages"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	isJudge := false
	hasToolResult := false
	company := "Acme Robotics"
	for _, message := range request.Messages {
		content := strings.TrimSpace(stringValue(message["content"]))
		if strings.Contains(content, "completion-state judge") {
			isJudge = true
		}
		if strings.Contains(content, `"customer_id":"42"`) {
			m.sawStructuredParams.Store(true)
		}
		if message["role"] == "tool" && message["name"] == "read_file" {
			hasToolResult = true
			if strings.Contains(content, "Acme Robotics") {
				m.sawCustomerToolResult.Store(true)
			}
			if strings.Contains(content, "Beta Dynamics") {
				company = "Beta Dynamics"
				m.sawSecondUserResult.Store(true)
			}
		}
	}

	message := map[string]interface{}{"role": "assistant"}
	finishReason := "stop"
	switch {
	case isJudge:
		message["content"] = `{"state":"final","confidence":0.99,"reason":"business result is complete"}`
	case hasToolResult:
		result, _ := json.Marshal(map[string]interface{}{
			"customer_id": "42",
			"company":     company,
			"risk":        "medium",
			"next_action": "Schedule a renewal review.",
			"source":      "customers/customer-42.json",
		})
		message["content"] = string(result)
	default:
		message["content"] = ""
		message["tool_calls"] = []interface{}{
			map[string]interface{}{
				"id": "read-customer-42", "type": "function",
				"function": map[string]interface{}{
					"name":      "read_file",
					"arguments": `{"path":"customers/customer-42.json"}`,
				},
			},
		}
		finishReason = "tool_calls"
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"id": "business-e2e", "object": "chat.completion",
		"choices": []interface{}{map[string]interface{}{
			"index": 0, "message": message, "finish_reason": finishReason,
		}},
		"usage": map[string]interface{}{
			"prompt_tokens": 10, "completion_tokens": 10, "total_tokens": 20,
		},
	})
}

func stringValue(value interface{}) string {
	text, _ := value.(string)
	return text
}

func performAgentOSBusinessRequest(
	t *testing.T,
	handler http.Handler,
	method, target, token string,
	payload interface{},
	headers map[string]string,
) (int, map[string]interface{}) {
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
	request.Header.Set("Authorization", "Bearer "+token)
	if payload != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	var decoded map[string]interface{}
	if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("%s %s returned invalid JSON (status=%d): %s", method, target, response.Code, response.Body.String())
	}
	return response.Code, decoded
}

func TestAgentOSBusinessLoopUsesPublishedRoleModelToolAndStructuredResult(t *testing.T) {
	workspace := t.TempDir()
	database := openAgentOSScenarioDB(t)
	previousDB := db
	db = database
	t.Cleanup(func() { db = previousDB })

	model := &agentOSBusinessMockModel{}
	modelServer := httptest.NewServer(model)
	t.Cleanup(modelServer.Close)

	const serviceToken = "not-a-real-agentos-service-token"
	const subjectID = "person-42"
	const secondSubjectID = "person-84"
	isolationSuffix := fmt.Sprintf("%d", time.Now().UnixNano())
	tenantID := "tenant-business-" + isolationSuffix
	serviceID := "crm-business-" + isolationSuffix
	config := &Config{
		TeamName:     "AgentOS Business E2E",
		DefaultAgent: "CustomerBriefAgent",
		Workspace:    workspace,
		ModelConfig: map[string]ModelConfig{
			"business-model": {
				Type: "openai", APIKey: "not-a-real-model-key",
				ModelName: "business-e2e", BaseURL: modelServer.URL + "/v1",
				Temperature: 0.1, MaxTokens: 1024, ThinkingMode: "disabled",
			},
		},
		Agents: []AgentConfig{{
			Name: "CustomerBriefAgent", Type: AgentTypeCustom, Model: "business-model",
			SystemMessage: "Read the customer record and return only the published JSON output.",
			EnabledTools:  []string{"read_file"}, MaxIterations: 4,
		}},
		ServiceRoles: []AgentServiceRoleConfig{{
			ID: "customer-brief", AgentName: "CustomerBriefAgent",
			Description: "Build a customer renewal brief.", Enabled: true,
			AllowedTenants: []string{tenantID}, AllowedCallers: []string{serviceID},
			InputSchema: map[string]interface{}{
				"type": "object", "additionalProperties": false,
				"required": []interface{}{"customer_id"},
				"properties": map[string]interface{}{
					"customer_id": map[string]interface{}{"type": "string"},
				},
			},
			OutputSchema: map[string]interface{}{
				"type": "object", "additionalProperties": false,
				"required": []interface{}{
					"customer_id", "company", "risk", "next_action", "source",
				},
				"properties": map[string]interface{}{
					"customer_id": map[string]interface{}{"type": "string"},
					"company":     map[string]interface{}{"type": "string"},
					"risk": map[string]interface{}{
						"type": "string", "enum": []interface{}{"low", "medium", "high"},
					},
					"next_action": map[string]interface{}{"type": "string"},
					"source":      map[string]interface{}{"type": "string"},
				},
			},
		}},
		Tenants: []TenantConfig{{
			ID: tenantID, Name: "Demo tenant", Token: "not-a-real-tenant-token",
		}},
		ServiceClients: []AgentServiceClientConfig{{
			ID: serviceID, TenantID: tenantID, Token: serviceToken, Enabled: true,
			RequireUserID: true,
			AllowedRoles:  []string{"customer-brief"},
			Scopes: []string{
				agentOSScopeTasksSubmit,
				agentOSScopeTasksReadOwn,
				agentOSScopeTasksCancelOwn,
				agentOSScopeUsersActAs,
			},
		}},
		ToolsConfig: ToolsConfig{Enabled: true},
		Runtime: RuntimeConfig{
			SemaphoreLimit: 1, LLMCallTimeoutSeconds: 5, LLMMaxRetries: 1,
		},
		Gateway: GatewayConfig{
			Enabled: true, AgentOSMode: "all", MaxTasks: 1, MaxQueuedTasks: 10,
			TaskLeaseSeconds: 10, TaskPollIntervalMS: 10,
		},
	}
	config.SkillsConfig.DisEnabled = true

	for _, customer := range []struct {
		subjectID string
		record    string
	}{
		{
			subjectID: subjectID,
			record:    `{"customer_id":"42","company":"Acme Robotics","account_health":"attention"}`,
		},
		{
			subjectID: secondSubjectID,
			record:    `{"customer_id":"42","company":"Beta Dynamics","account_health":"healthy"}`,
		},
	} {
		customerDir := filepath.Join(
			personalAgentOSWorkspace(
				filepath.Join(workspace, ".openkitty", "tenants", tenantID),
				tenantID,
				customer.subjectID,
			),
			"customers",
		)
		if err := os.MkdirAll(customerDir, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(
			filepath.Join(customerDir, "customer-42.json"),
			[]byte(customer.record),
			0644,
		); err != nil {
			t.Fatal(err)
		}
	}

	engine, err := NewMultiAgentEngine(config)
	if err != nil {
		t.Fatalf("initialize business agent: %v", err)
	}
	t.Cleanup(func() { _ = engine.Close() })
	logger := log.New(io.Discard, "", 0)
	gateway := &GatewayService{
		config: config, engine: engine, logger: logger, db: database,
		project: NewProjectContext(workspace), tasks: make(map[string]*GatewayTask),
	}
	gateway.initSSE()
	gateway.tenantMgr = NewTenantManager(
		filepath.Join(workspace, ".openkitty", "tenants"),
		logger,
	)
	if err := gateway.tenantMgr.Init(config.Tenants, ""); err != nil {
		t.Fatal(err)
	}
	queue := gateway.ensureAgentOSQueue()
	queue.Start()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		queue.Stop(ctx)
	})
	handler := gateway.agentOSDataHandler()

	status, discovered := performAgentOSBusinessRequest(
		t, handler, http.MethodGet, "/api/v1/agent/roles",
		serviceToken, nil, nil,
	)
	if status != http.StatusOK {
		t.Fatalf("role discovery status=%d response=%#v", status, discovered)
	}
	roles, ok := discovered["roles"].([]interface{})
	if !ok || len(roles) != 1 {
		t.Fatalf("unexpected role discovery: %#v", discovered)
	}
	role, _ := roles[0].(map[string]interface{})
	revision := stringValue(role["role_revision"])
	if role["role_id"] != "customer-brief" || revision == "" {
		t.Fatalf("unexpected published role: %#v", role)
	}

	submission := map[string]interface{}{
		"role_id": "customer-brief", "role_revision": revision,
		"user_id": subjectID, "session_id": "shared-customer-session",
		"message":    "Create the renewal brief for customer 42.",
		"parameters": map[string]interface{}{"customer_id": "42"},
	}
	headers := map[string]string{
		"Idempotency-Key":   "business-customer-42-v1",
		"X-Request-ID":      "crm-business-e2e",
		agentOSUserIDHeader: subjectID,
	}
	status, accepted := performAgentOSBusinessRequest(
		t, handler, http.MethodPost, "/api/v1/agent/tasks",
		serviceToken, submission, headers,
	)
	if status != http.StatusAccepted {
		t.Fatalf("task submit status=%d response=%#v", status, accepted)
	}
	taskID := stringValue(accepted["task_id"])
	if taskID == "" {
		t.Fatalf("accepted response has no task ID: %#v", accepted)
	}
	firstSessionID := stringValue(accepted["session_id"])
	if accepted["subject_id"] != subjectID || firstSessionID == "" {
		t.Fatalf("first task owner/session mismatch: %#v", accepted)
	}

	deadline := time.Now().Add(8 * time.Second)
	var completed map[string]interface{}
	for time.Now().Before(deadline) {
		status, completed = performAgentOSBusinessRequest(
			t, handler, http.MethodGet, "/api/v1/agent/tasks/"+taskID,
			serviceToken, nil, map[string]string{agentOSUserIDHeader: subjectID},
		)
		if status == http.StatusTooManyRequests {
			time.Sleep(1100 * time.Millisecond)
			continue
		}
		if status != http.StatusOK {
			t.Fatalf("task query status=%d response=%#v", status, completed)
		}
		if completed["status"] == "completed" {
			break
		}
		if completed["status"] == "failed" || completed["status"] == "stopped" {
			t.Fatalf("business task entered terminal failure: %#v", completed)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if completed["status"] != "completed" {
		t.Fatalf("business task did not complete: %#v", completed)
	}
	result, ok := completed["result"].(map[string]interface{})
	if !ok {
		t.Fatalf("result type=%T, want structured JSON object: %#v", completed["result"], completed)
	}
	if result["customer_id"] != "42" || result["company"] != "Acme Robotics" ||
		result["source"] != "customers/customer-42.json" {
		t.Fatalf("unexpected business result: %#v", result)
	}
	if !model.sawStructuredParams.Load() {
		t.Fatal("structured task parameters did not reach the model")
	}
	if !model.sawCustomerToolResult.Load() {
		t.Fatal("model did not receive the tenant customer record from read_file")
	}
	if model.requests.Load() < 2 {
		t.Fatalf("model request count=%d, want tool turn plus final turn", model.requests.Load())
	}

	secondSubmission := map[string]interface{}{
		"role_id": role["role_id"], "role_revision": revision,
		"user_id": secondSubjectID, "session_id": "shared-customer-session",
		"message":    "Create the renewal brief for customer 42.",
		"parameters": map[string]interface{}{"customer_id": "42"},
	}
	secondHeaders := map[string]string{
		"Idempotency-Key":   "business-customer-42-v1",
		"X-Request-ID":      "crm-business-e2e-second-user",
		agentOSUserIDHeader: secondSubjectID,
	}
	var secondAccepted map[string]interface{}
	for attempts := 0; attempts < 3; attempts++ {
		status, secondAccepted = performAgentOSBusinessRequest(
			t, handler, http.MethodPost, "/api/v1/agent/tasks",
			serviceToken, secondSubmission, secondHeaders,
		)
		if status != http.StatusTooManyRequests {
			break
		}
		time.Sleep(1100 * time.Millisecond)
	}
	if status != http.StatusAccepted {
		t.Fatalf("second user task submit status=%d response=%#v", status, secondAccepted)
	}
	secondTaskID := stringValue(secondAccepted["task_id"])
	secondSessionID := stringValue(secondAccepted["session_id"])
	if secondTaskID == "" || secondTaskID == taskID ||
		secondSessionID == "" || secondSessionID == firstSessionID ||
		secondAccepted["subject_id"] != secondSubjectID {
		t.Fatalf(
			"second user was not independently scoped: first=%#v second=%#v",
			accepted,
			secondAccepted,
		)
	}

	var secondCompleted map[string]interface{}
	deadline = time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		status, secondCompleted = performAgentOSBusinessRequest(
			t, handler, http.MethodGet, "/api/v1/agent/tasks/"+secondTaskID,
			serviceToken, nil, map[string]string{agentOSUserIDHeader: secondSubjectID},
		)
		if status == http.StatusTooManyRequests {
			time.Sleep(1100 * time.Millisecond)
			continue
		}
		if status != http.StatusOK {
			t.Fatalf("second task query status=%d response=%#v", status, secondCompleted)
		}
		if secondCompleted["status"] == "completed" {
			break
		}
		if secondCompleted["status"] == "failed" || secondCompleted["status"] == "stopped" {
			t.Fatalf("second user task entered terminal failure: %#v", secondCompleted)
		}
		time.Sleep(20 * time.Millisecond)
	}
	secondResult, secondOK := secondCompleted["result"].(map[string]interface{})
	if secondCompleted["status"] != "completed" || !secondOK ||
		secondResult["company"] != "Beta Dynamics" {
		t.Fatalf("second user did not read its personal file: %#v", secondCompleted)
	}
	if !model.sawSecondUserResult.Load() {
		t.Fatal("model did not receive the second user's personal customer record")
	}

	var owners []GatewayTaskRecord
	if err := database.Where("task_id IN ?", []string{taskID, secondTaskID}).
		Order("subject_id").
		Find(&owners).Error; err != nil {
		t.Fatal(err)
	}
	if len(owners) != 2 || owners[0].SubjectID == owners[1].SubjectID {
		t.Fatalf("durable task owners were not separated: %+v", owners)
	}

	var replay map[string]interface{}
	for attempts := 0; attempts < 3; attempts++ {
		status, replay = performAgentOSBusinessRequest(
			t, handler, http.MethodPost, "/api/v1/agent/tasks",
			serviceToken, submission, headers,
		)
		if status != http.StatusTooManyRequests {
			break
		}
		time.Sleep(1100 * time.Millisecond)
	}
	if status != http.StatusOK || replay["task_id"] != taskID ||
		replay["idempotent_replay"] != true {
		t.Fatalf("idempotent business replay failed: status=%d response=%#v", status, replay)
	}
}
