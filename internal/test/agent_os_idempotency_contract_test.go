package app

import (
	"io"
	"log"
	"net/http"
	"path/filepath"
	"testing"
)

func TestAgentOSIdempotencyRejectsDifferentRequestPayload(t *testing.T) {
	gateway, _ := newAgentOSAPIScenarioGateway(
		t,
		&agentOSAPIScenarioAgent{result: "ok"},
	)
	first := submitAgentOSScenario(
		gateway,
		"crm-service",
		"customer-42-v1",
		map[string]interface{}{
			"message":    "research customer 42",
			"parameters": map[string]interface{}{"customer_id": "42"},
		},
	)
	if first.code != http.StatusAccepted {
		t.Fatalf("first submit status=%d body=%s", first.code, first.raw)
	}

	replay := submitAgentOSScenario(
		gateway,
		"crm-service",
		"customer-42-v1",
		map[string]interface{}{
			"message":    "research customer 42",
			"parameters": map[string]interface{}{"customer_id": "42"},
		},
	)
	if replay.code != http.StatusOK || replay.body["idempotent_replay"] != true {
		t.Fatalf("same request was not replayed: status=%d body=%s", replay.code, replay.raw)
	}

	mismatch := submitAgentOSScenario(
		gateway,
		"crm-service",
		"customer-42-v1",
		map[string]interface{}{
			"message":    "research customer 99",
			"parameters": map[string]interface{}{"customer_id": "99"},
		},
	)
	if mismatch.code != http.StatusConflict ||
		mismatch.body["code"] != "idempotency_payload_mismatch" ||
		mismatch.body["retryable"] != false {
		t.Fatalf("different request reused the key: status=%d body=%s", mismatch.code, mismatch.raw)
	}

	var reservation AgentTaskRequestRecord
	if err := gateway.db.First(&reservation).Error; err != nil {
		t.Fatal(err)
	}
	if len(reservation.RequestHash) != 64 {
		t.Fatalf("request fingerprint was not persisted: %q", reservation.RequestHash)
	}
}

func TestAgentOSIdempotentReplayRechecksCurrentReadScope(t *testing.T) {
	gateway, _ := newAgentOSAPIScenarioGateway(
		t,
		&agentOSAPIScenarioAgent{result: "ok"},
	)
	const token = "idempotency_scope_test_token_1234567890"
	tenantManager := NewTenantManager(
		filepath.Join(t.TempDir(), "tenants"),
		gateway.logger,
	)
	if err := tenantManager.Init([]TenantConfig{{
		ID: "tenant-a", Name: "Tenant A", Token: "tenant_admin_token_1234567890",
	}}, ""); err != nil {
		t.Fatal(err)
	}
	gateway.tenantMgr = tenantManager
	gateway.config.Gateway.AuthEnabled = true
	gateway.config.ServiceClients = []AgentServiceClientConfig{{
		ID: "crm-service", TenantID: "tenant-a",
		Token: token, Enabled: true, AllowedRoles: []string{"Researcher"},
	}}
	headers := map[string]string{
		"Authorization":   "Bearer " + token,
		"Idempotency-Key": "permission-change-v1",
	}
	payload := map[string]interface{}{
		"message": "research customer 42", "role_id": "Researcher",
	}
	first := performAgentOSRequest(
		gateway,
		http.MethodPost,
		"/api/v1/agent/tasks",
		payload,
		headers,
	)
	if first.code != http.StatusAccepted {
		t.Fatalf("first submit status=%d body=%s", first.code, first.raw)
	}

	// Keep submission permission and the role grant, but revoke task reads.
	gateway.config.ServiceClients[0].Scopes = []string{agentOSScopeTasksSubmit}
	replay := performAgentOSRequest(
		gateway,
		http.MethodPost,
		"/api/v1/agent/tasks",
		payload,
		headers,
	)
	if replay.code != http.StatusNotFound {
		t.Fatalf("replay bypassed current read scope: status=%d body=%s", replay.code, replay.raw)
	}
}

func TestAgentOSIdempotentReplayKeepsAcceptedRoleRevision(t *testing.T) {
	gateway := newAgentOSRoleRevisionGateway(t)
	gateway.db = openAgentOSScenarioDB(t)
	gateway.config.Gateway.AgentOSMode = "gateway"
	gateway.config.ServiceRoles[0].AllowedTenants = []string{defaultExecutionTenantID}
	gateway.config.ServiceRoles[0].AllowedCallers = []string{"crm-service"}
	gateway.logger = log.New(io.Discard, "", 0)
	gateway.tasks = make(map[string]*GatewayTask)
	gateway.initSSE()
	gateway.engine.agentFactory.mu.Lock()
	gateway.engine.agentFactory.agents["CustomerResearchAgent"] =
		&agentOSAPIScenarioAgent{result: "unused"}
	gateway.engine.agentFactory.mu.Unlock()

	headers := map[string]string{
		"X-Caller-ID":     "crm-service",
		"Idempotency-Key": "stable-role-revision-v1",
	}
	payload := map[string]interface{}{
		"message": "research customer 42",
		"role_id": "customer-research",
		"parameters": map[string]interface{}{
			"customer_id": "42",
		},
	}
	first := performAgentOSRequest(
		gateway,
		http.MethodPost,
		"/api/v1/agent/tasks",
		payload,
		headers,
	)
	if first.code != http.StatusAccepted {
		t.Fatalf("first submit status=%d body=%s", first.code, first.raw)
	}
	acceptedRevision, _ := first.body["role_revision"].(string)
	if acceptedRevision == "" {
		t.Fatalf("accepted task has no role revision: %s", first.raw)
	}

	gateway.config.Agents[0].SystemMessage =
		"A newly published responsibility that must only affect new tasks."
	currentRevision, _, err := gateway.captureAgentOSRoleRevision(
		"customer-research",
		"CustomerResearchAgent",
	)
	if err != nil {
		t.Fatal(err)
	}
	if currentRevision == acceptedRevision {
		t.Fatal("test setup did not publish a new role revision")
	}

	replay := performAgentOSRequest(
		gateway,
		http.MethodPost,
		"/api/v1/agent/tasks",
		payload,
		headers,
	)
	if replay.code != http.StatusOK || replay.body["idempotent_replay"] != true {
		t.Fatalf("role update broke idempotent replay: status=%d body=%s", replay.code, replay.raw)
	}
	if replay.body["task_id"] != first.body["task_id"] ||
		replay.body["role_revision"] != acceptedRevision {
		t.Fatalf("replay did not preserve accepted task contract: first=%s replay=%s", first.raw, replay.raw)
	}
}
