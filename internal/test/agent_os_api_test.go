package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"
)

func TestAgentOSRequestFingerprintPreservesLegacyNonActAsPayload(t *testing.T) {
	submitted := agentOSTaskSubmitRequest{
		Message:    "create invoice",
		SessionID:  "customer/session",
		UserID:     "service:billing",
		Parameters: map[string]interface{}{"invoice_id": "INV-1"},
	}
	got, err := agentOSRequestFingerprint(
		submitted,
		"billing",
		"revision-1",
		"service:billing",
		"",
	)
	if err != nil {
		t.Fatal(err)
	}
	legacyPayload, err := json.Marshal(struct {
		RoleID       string              `json:"role_id"`
		RoleRevision string              `json:"role_revision"`
		Message      string              `json:"message"`
		SessionID    string              `json:"session_id,omitempty"`
		UserID       string              `json:"user_id,omitempty"`
		Attachments  []MessageAttachment `json:"attachments,omitempty"`
		Parameters   interface{}         `json:"parameters,omitempty"`
	}{
		RoleID: "billing", RoleRevision: "revision-1",
		Message: "create invoice", SessionID: "customer_session",
		UserID:     "service:billing",
		Parameters: map[string]interface{}{"invoice_id": "INV-1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(legacyPayload)
	want := hex.EncodeToString(digest[:])
	if got != want {
		t.Fatalf("non-act-as fingerprint changed across upgrade: got %s want %s", got, want)
	}
}

type agentOSAPIScenarioAgent struct {
	result             string
	err                error
	started            chan struct{}
	finished           chan struct{}
	release            <-chan struct{}
	ignoreCancellation bool
	calls              atomic.Int32
	startOnce          sync.Once
	finishOnce         sync.Once
	inputsMu           sync.Mutex
	inputs             []string
}

type agentOSApprovalScenarioAgent struct {
	calls atomic.Int32
	call  ToolCall
}

func (a *agentOSApprovalScenarioAgent) Name() string { return "Researcher" }
func (a *agentOSApprovalScenarioAgent) GetType() AgentType {
	return AgentTypeCustom
}
func (a *agentOSApprovalScenarioAgent) Generate(ctx context.Context, input string) (string, error) {
	a.calls.Add(1)
	if err := authorizeToolApproval(ctx, a.call, "production mutation requires operator approval"); err != nil {
		return "", err
	}
	consumeToolApproval(ctx, a.call)
	return "approved operation completed", nil
}

func (a *agentOSAPIScenarioAgent) Name() string { return "Researcher" }
func (a *agentOSAPIScenarioAgent) Generate(ctx context.Context, input string) (string, error) {
	a.calls.Add(1)
	a.inputsMu.Lock()
	a.inputs = append(a.inputs, input)
	a.inputsMu.Unlock()
	if a.started != nil {
		a.startOnce.Do(func() { close(a.started) })
	}
	defer func() {
		if a.finished != nil {
			a.finishOnce.Do(func() { close(a.finished) })
		}
	}()
	if a.release != nil {
		if a.ignoreCancellation {
			<-a.release
		} else {
			select {
			case <-a.release:
			case <-ctx.Done():
				return "", ctx.Err()
			}
		}
	}
	return a.result, a.err
}
func (a *agentOSAPIScenarioAgent) GetType() AgentType { return AgentTypeCustom }
func (a *agentOSAPIScenarioAgent) inputSnapshot() []string {
	a.inputsMu.Lock()
	defer a.inputsMu.Unlock()
	return append([]string(nil), a.inputs...)
}

func newAgentOSAPIScenarioGateway(t *testing.T, agent Agent) (*GatewayService, string) {
	t.Helper()
	db := openAgentOSScenarioDB(t)
	workspace := t.TempDir()
	cfg := &Config{Workspace: workspace}
	logger := log.New(io.Discard, "", 0)
	factory := &AgentFactory{
		agents:       map[string]Agent{"Researcher": agent},
		subAgents:    make(map[string]Agent),
		globalConfig: cfg,
		workspace:    workspace,
		fileLogger:   logger,
	}
	engine := &MultiAgentEngine{
		config: cfg, agentFactory: factory, logger: logger, fileLogger: logger,
	}
	gateway := &GatewayService{
		config: cfg, engine: engine, logger: logger, db: db,
		project: NewProjectContext(workspace),
		tasks:   make(map[string]*GatewayTask),
	}
	gateway.initSSE()
	return gateway, workspace
}

type agentOSHTTPResult struct {
	code     int
	location string
	body     map[string]interface{}
	raw      string
}

func performAgentOSRequest(g *GatewayService, method, target string, payload interface{}, headers map[string]string) agentOSHTTPResult {
	var body io.Reader
	if payload != nil {
		encoded, _ := json.Marshal(payload)
		body = bytes.NewReader(encoded)
	}
	req := httptest.NewRequest(method, target, body)
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	recorder := httptest.NewRecorder()
	if target == "/api/v1/agent/tasks" || strings.HasPrefix(target, "/api/v1/agent/tasks?") {
		g.handleAgentOSTasks(recorder, req)
	} else {
		g.handleAgentOSTaskByID(recorder, req)
	}
	result := agentOSHTTPResult{
		code: recorder.Code, location: recorder.Header().Get("Location"), raw: recorder.Body.String(),
	}
	_ = json.Unmarshal(recorder.Body.Bytes(), &result.body)
	return result
}

func submitAgentOSScenario(g *GatewayService, caller, key string, overrides map[string]interface{}) agentOSHTTPResult {
	payload := map[string]interface{}{
		"message":    "solve a complex task",
		"agent_name": "Researcher",
	}
	for field, value := range overrides {
		payload[field] = value
	}
	headers := make(map[string]string)
	if caller != "" {
		headers["X-Caller-ID"] = caller
	}
	if key != "" {
		headers["Idempotency-Key"] = key
	}
	return performAgentOSRequest(g, http.MethodPost, "/api/v1/agent/tasks", payload, headers)
}

func waitForAgentOSTask(t *testing.T, g *GatewayService, taskID string, statuses ...string) *GatewayTask {
	t.Helper()
	wanted := make(map[string]bool, len(statuses))
	for _, status := range statuses {
		wanted[status] = true
	}
	deadline := time.Now().Add(6 * time.Second)
	for time.Now().Before(deadline) {
		if task, ok := g.gatewayTaskSnapshotWithDB(taskID); ok {
			if len(wanted) == 0 && isTerminalGatewayTaskStatus(task.Status) || wanted[task.Status] {
				return task
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	task, _ := g.gatewayTaskSnapshotWithDB(taskID)
	t.Fatalf("task %s did not reach %v; last=%+v", taskID, statuses, task)
	return nil
}

func waitForAgentOSSignal(t *testing.T, signal <-chan struct{}, label string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", label)
	}
}

func agentOSTaskIDFromResponse(t *testing.T, result agentOSHTTPResult) string {
	t.Helper()
	taskID, _ := result.body["task_id"].(string)
	if taskID == "" {
		t.Fatalf("response has no task_id: code=%d body=%s", result.code, result.raw)
	}
	return taskID
}

func TestScopeAgentOSSessionIDPreservesUTF8AndStorageLimit(t *testing.T) {
	scoped := scopeAgentOSSessionID("tenant-a", strings.Repeat("会", 64))
	if !utf8.ValidString(scoped) {
		t.Fatalf("scoped session id is invalid UTF-8: %q", scoped)
	}
	if len(scoped) > 64 {
		t.Fatalf("scoped session id uses %d bytes, want at most 64", len(scoped))
	}
	alice := scopeAgentOSSessionID("tenant-a", "shared-session", "alice")
	bob := scopeAgentOSSessionID("tenant-a", "shared-session", "bob")
	if alice == bob || !strings.HasPrefix(alice, personalAgentOSSessionPrefix("tenant-a", "alice")) {
		t.Fatalf("subject session partitions collided: alice=%q bob=%q", alice, bob)
	}
	longPrefix := strings.Repeat("x", 56)
	first := scopeAgentOSSessionID("tenant-a", longPrefix+"-one", "alice")
	second := scopeAgentOSSessionID("tenant-a", longPrefix+"-two", "alice")
	if first == second || len(first) > 64 || len(second) > 64 {
		t.Fatalf("long personal sessions collided or overflowed: first=%q second=%q", first, second)
	}
	if first != scopeAgentOSSessionID("tenant-a", longPrefix+"-one", "alice") {
		t.Fatalf("long personal session scope is not deterministic: %q", first)
	}
}

func TestAgentOSApprovalPausesAuthorizesAndResumesTask(t *testing.T) {
	agent := &agentOSApprovalScenarioAgent{call: ToolCall{
		ID: "publish-call", Name: "publish_customer_export",
		Arguments: map[string]interface{}{
			"target": "production",
			"token":  "secret-value-that-must-not-be-stored",
		},
	}}
	g, _ := newAgentOSAPIScenarioGateway(t, agent)
	t.Cleanup(func() {
		if g.agentOSQueue == nil {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		g.agentOSQueue.Stop(ctx)
	})

	submitted := submitAgentOSScenario(g, "approval-test", "approval-once", nil)
	if submitted.code != http.StatusAccepted {
		t.Fatalf("submit status=%d body=%s", submitted.code, submitted.raw)
	}
	taskID := agentOSTaskIDFromResponse(t, submitted)
	waiting := waitForAgentOSTask(t, g, taskID, "waiting_approval")
	if waiting.CurrentPhase != "waiting_approval" || waiting.CompletedAt != nil {
		t.Fatalf("waiting task=%+v", waiting)
	}
	if agent.calls.Load() != 1 {
		t.Fatalf("agent calls before approval=%d, want 1", agent.calls.Load())
	}

	listed := performAgentOSRequest(
		g, http.MethodGet,
		"/api/v1/agent/tasks/"+taskID+"/approvals",
		nil, nil,
	)
	if listed.code != http.StatusOK {
		t.Fatalf("list approvals status=%d body=%s", listed.code, listed.raw)
	}
	rawApprovals, ok := listed.body["approvals"].([]interface{})
	if !ok || len(rawApprovals) != 1 {
		t.Fatalf("approval list=%#v", listed.body)
	}
	approval, _ := rawApprovals[0].(map[string]interface{})
	approvalID, _ := approval["approval_id"].(string)
	if approvalID == "" || approval["status"] != approvalStatusPending {
		t.Fatalf("approval=%#v", approval)
	}
	preview, _ := approval["arguments_preview"].(string)
	if !strings.Contains(preview, "secret-value-that-must-not-be-stored") {
		t.Fatalf("approval workflow preview lost the original argument: %q", preview)
	}
	if strings.Contains(preview, "[REDACTED]") || strings.Contains(preview, "[SCRUBBED]") {
		t.Fatalf("approval workflow preview was forcibly redacted: %q", preview)
	}

	// Tenant peers can observe their task, but only an explicitly delegated
	// operator credential may decide an approval.
	g.config.Gateway.AuthEnabled = true
	g.config.ServiceClients = []AgentServiceClientConfig{
		{
			ID: "viewer", TenantID: defaultExecutionTenantID,
			Token: "viewer_service_token_1234567890", Enabled: true,
		},
		{
			ID: "operator", TenantID: defaultExecutionTenantID,
			Token: "operator_service_token_1234567890", Enabled: true, CanApprove: true,
		},
		{
			ID: "wrong-role-operator", TenantID: defaultExecutionTenantID,
			Token: "wrong_role_operator_token_1234567890", Enabled: true, CanApprove: true,
			AllowedRoles: []string{"unrelated-role"},
		},
		{
			ID: "other-tenant", TenantID: "tenant-b",
			Token: "other_tenant_service_token_1234567890", Enabled: true, CanApprove: true,
		},
	}
	viewerHeaders := map[string]string{"Authorization": "Bearer viewer_service_token_1234567890"}
	forbidden := performAgentOSRequest(
		g, http.MethodPost,
		"/api/v1/agent/tasks/"+taskID+"/approvals/"+approvalID+"/approve",
		nil, viewerHeaders,
	)
	if forbidden.code != http.StatusForbidden {
		t.Fatalf("viewer approval status=%d body=%s", forbidden.code, forbidden.raw)
	}
	wrongRole := performAgentOSRequest(
		g, http.MethodPost,
		"/api/v1/agent/tasks/"+taskID+"/approvals/"+approvalID+"/approve",
		nil, map[string]string{"Authorization": "Bearer wrong_role_operator_token_1234567890"},
	)
	if wrongRole.code != http.StatusForbidden {
		t.Fatalf("wrong-role approval status=%d body=%s", wrongRole.code, wrongRole.raw)
	}
	hidden := performAgentOSRequest(
		g, http.MethodGet,
		"/api/v1/agent/tasks/"+taskID+"/approvals",
		nil, map[string]string{"Authorization": "Bearer other_tenant_service_token_1234567890"},
	)
	if hidden.code != http.StatusNotFound {
		t.Fatalf("cross-tenant approval list status=%d body=%s", hidden.code, hidden.raw)
	}

	approved := performAgentOSRequest(
		g, http.MethodPost,
		"/api/v1/agent/tasks/"+taskID+"/approvals/"+approvalID+"/approve",
		map[string]interface{}{"reason": "change ticket CR-42"},
		map[string]string{"Authorization": "Bearer operator_service_token_1234567890"},
	)
	if approved.code != http.StatusOK {
		t.Fatalf("approve status=%d body=%s", approved.code, approved.raw)
	}
	completed := waitForAgentOSTask(t, g, taskID, "completed")
	if completed.Result != "approved operation completed" || completed.RunAttempt != 2 {
		t.Fatalf("completed task=%+v", completed)
	}
	if agent.calls.Load() != 2 {
		t.Fatalf("agent calls after approval=%d, want 2", agent.calls.Load())
	}

	var durable ApprovalRequestRecord
	if err := g.db.Where("approval_id = ?", approvalID).First(&durable).Error; err != nil {
		t.Fatal(err)
	}
	if durable.Status != approvalStatusConsumed || durable.DecidedBy != "operator" {
		t.Fatalf("durable approval=%+v", durable)
	}
}

func TestAgentOSApprovalDenialTerminatesWithoutExecuting(t *testing.T) {
	agent := &agentOSApprovalScenarioAgent{call: ToolCall{
		ID: "delete-call", Name: "delete_customer_export",
		Arguments: map[string]interface{}{"target": "production"},
	}}
	g, _ := newAgentOSAPIScenarioGateway(t, agent)
	t.Cleanup(func() {
		if g.agentOSQueue == nil {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		g.agentOSQueue.Stop(ctx)
	})
	submitted := submitAgentOSScenario(g, "approval-test", "approval-denial", nil)
	taskID := agentOSTaskIDFromResponse(t, submitted)
	_ = waitForAgentOSTask(t, g, taskID, "waiting_approval")

	var approval ApprovalRequestRecord
	if err := g.db.Where("task_id = ?", taskID).First(&approval).Error; err != nil {
		t.Fatal(err)
	}
	tooLong := performAgentOSRequest(
		g, http.MethodPost,
		"/api/v1/agent/tasks/"+taskID+"/approvals/"+approval.ApprovalID+"/deny",
		map[string]interface{}{"reason": strings.Repeat("r", 1025)}, nil,
	)
	if tooLong.code != http.StatusBadRequest {
		t.Fatalf("oversized denial reason status=%d body=%s", tooLong.code, tooLong.raw)
	}
	if err := g.db.Where("approval_id = ?", approval.ApprovalID).First(&approval).Error; err != nil {
		t.Fatal(err)
	}
	if approval.Status != approvalStatusPending {
		t.Fatalf("oversized reason changed approval status to %q", approval.Status)
	}
	denied := performAgentOSRequest(
		g, http.MethodPost,
		"/api/v1/agent/tasks/"+taskID+"/approvals/"+approval.ApprovalID+"/deny",
		map[string]interface{}{"reason": "change window is closed"}, nil,
	)
	if denied.code != http.StatusOK {
		t.Fatalf("deny status=%d body=%s", denied.code, denied.raw)
	}
	failed := waitForAgentOSTask(t, g, taskID, "failed")
	if failed.CurrentPhase != "approval_denied" ||
		failed.Error == nil ||
		!strings.Contains(failed.Error.Error(), "change window is closed") {
		t.Fatalf("failed task=%+v", failed)
	}
	if agent.calls.Load() != 1 {
		t.Fatalf("agent calls=%d, denied task must not resume", agent.calls.Load())
	}
	if err := g.db.Where("approval_id = ?", approval.ApprovalID).First(&approval).Error; err != nil {
		t.Fatal(err)
	}
	if approval.Status != approvalStatusDenied {
		t.Fatalf("approval status=%q, want denied", approval.Status)
	}
}

func TestAgentOSExternalAPITenantAndRoleIsolation(t *testing.T) {
	agent := &agentOSAPIScenarioAgent{result: "tenant scoped result"}
	g, _ := newAgentOSAPIScenarioGateway(t, agent)
	// Explicitly published enterprise roles must have a complete immutable
	// Agent/Model contract. Keep this authorization scenario gateway-only so it
	// validates admission and isolation without making an external model call.
	g.config.Gateway.AgentOSMode = "gateway"
	g.config.Agents = []AgentConfig{{
		Name: "Researcher", Type: AgentTypeCustom, Model: "isolation-test-model",
		SystemMessage: "Execute only the tenant-scoped research role.",
		EnabledTools:  []string{"read_file"},
	}}
	g.config.ModelConfig = map[string]ModelConfig{
		"isolation-test-model": {
			Type: "openai", APIKey: "test-only-live-model-credential",
			ModelName: "isolation-test-model", BaseURL: "https://llm.example.test/v1",
		},
	}
	manager := NewTenantManager(filepath.Join(t.TempDir(), "tenants"), log.New(io.Discard, "", 0))
	if err := manager.Init([]TenantConfig{
		{ID: "tenant-a", Name: "Tenant A", Token: "tenant-a-service-token"},
		{ID: "tenant-b", Name: "Tenant B", Token: "tenant-b-service-token"},
	}, "root-service-token"); err != nil {
		t.Fatal(err)
	}
	g.tenantMgr = manager
	g.config.Gateway.AuthEnabled = true
	g.config.ServiceClients = []AgentServiceClientConfig{
		{
			ID: "crm-a-service", TenantID: "tenant-a",
			Token: "svc_tenant_a_crm_token_1234567890", Enabled: true,
			AllowedRoles: []string{"customer-research"},
		},
		{
			ID: "crm-b-service", TenantID: "tenant-b",
			Token: "svc_tenant_b_crm_token_1234567890", Enabled: true,
			AllowedRoles: []string{"customer-research"},
		},
		{
			ID: "billing-service", TenantID: "tenant-a",
			Token: "svc_tenant_a_billing_token_123456", Enabled: true,
			AllowedRoles: []string{"customer-research"},
		},
	}
	g.config.ServiceRoles = []AgentServiceRoleConfig{{
		ID: "customer-research", AgentName: "Researcher", Enabled: true,
		AllowedTenants: []string{"tenant-a", "tenant-b"},
		AllowedCallers: []string{"crm-a-service", "crm-b-service"},
	}}

	payload := map[string]interface{}{
		"message": "perform assigned work",
		"role_id": "customer-research",
	}
	headersA := map[string]string{
		"Authorization":   "Bearer svc_tenant_a_crm_token_1234567890",
		"X-Caller-ID":     "spoofed-billing-service",
		"Idempotency-Key": "shared-operation",
	}
	createdA := performAgentOSRequest(g, http.MethodPost, "/api/v1/agent/tasks", payload, headersA)
	if createdA.code != http.StatusAccepted {
		t.Fatalf("tenant A submit code=%d body=%s", createdA.code, createdA.raw)
	}
	taskA := agentOSTaskIDFromResponse(t, createdA)
	if createdA.body["tenant_id"] != "tenant-a" || createdA.body["role_id"] != "customer-research" ||
		createdA.body["agent_name"] != "Researcher" {
		t.Fatalf("tenant/role missing from response: %#v", createdA.body)
	}
	queuedA, ok := g.gatewayTaskSnapshotWithDB(taskA)
	expectedWorkspaceA := personalAgentOSWorkspace(
		manager.TenantDir("tenant-a"),
		"tenant-a",
		"service:crm-a-service",
	)
	if !ok || queuedA.Request == nil ||
		!sameProjectWorkspace(queuedA.ResolvedWorkspace, expectedWorkspaceA) {
		t.Fatalf("task did not use personal tenant workspace: %+v", queuedA)
	}
	var persisted GatewayTaskRecord
	if err := g.db.Where("task_id = ?", taskA).First(&persisted).Error; err != nil {
		t.Fatal(err)
	}
	if persisted.TenantID != "tenant-a" ||
		persisted.SubjectID != "service:crm-a-service" ||
		persisted.RoleID != "customer-research" {
		t.Fatalf("tenant/role were not persisted: %+v", persisted)
	}

	headersB := map[string]string{
		"Authorization": "Bearer svc_tenant_b_crm_token_1234567890",
		"X-Caller-ID":   "spoofed-other-service",
	}
	hidden := performAgentOSRequest(g, http.MethodGet, "/api/v1/agent/tasks/"+taskA, nil, headersB)
	if hidden.code != http.StatusNotFound {
		t.Fatalf("cross-tenant task lookup code=%d body=%s", hidden.code, hidden.raw)
	}
	listB := performAgentOSRequest(g, http.MethodGet, "/api/v1/agent/tasks", nil, headersB)
	if listB.code != http.StatusOK || int(listB.body["count"].(float64)) != 0 {
		t.Fatalf("tenant B list leaked tenant A tasks: code=%d body=%s", listB.code, listB.raw)
	}

	headersB["Idempotency-Key"] = "shared-operation"
	createdB := performAgentOSRequest(g, http.MethodPost, "/api/v1/agent/tasks", payload, headersB)
	if createdB.code != http.StatusAccepted {
		t.Fatalf("tenant B submit code=%d body=%s", createdB.code, createdB.raw)
	}
	taskB := agentOSTaskIDFromResponse(t, createdB)
	if taskA == taskB {
		t.Fatalf("idempotency key crossed tenant boundary: %s", taskA)
	}

	unauthorized := performAgentOSRequest(g, http.MethodGet, "/api/v1/agent/tasks", nil, nil)
	if unauthorized.code != http.StatusUnauthorized {
		t.Fatalf("missing tenant credentials code=%d body=%s", unauthorized.code, unauthorized.raw)
	}

	wrongCaller := performAgentOSRequest(g, http.MethodPost, "/api/v1/agent/tasks", payload, map[string]string{
		"Authorization": "Bearer svc_tenant_a_billing_token_123456",
		"X-Caller-ID":   "crm-a-service",
	})
	if wrongCaller.code != http.StatusBadRequest || !strings.Contains(wrongCaller.raw, "caller is not authorized") {
		t.Fatalf("service role caller restriction was not enforced: code=%d body=%s", wrongCaller.code, wrongCaller.raw)
	}
}

func TestAgentOSServiceActAsSubjectsIsolateSessionWorkspaceAndIdempotency(t *testing.T) {
	agent := &agentOSAPIScenarioAgent{result: "subject scoped result"}
	g, _ := newAgentOSAPIScenarioGateway(t, agent)
	manager := NewTenantManager(filepath.Join(t.TempDir(), "tenants"), log.New(io.Discard, "", 0))
	if err := manager.Init([]TenantConfig{{
		ID: "tenant-a", Name: "Tenant A", Token: "tenant-a-admin-token",
	}}, "root-token"); err != nil {
		t.Fatal(err)
	}
	g.tenantMgr = manager
	g.config.Gateway.AuthEnabled = true
	const token = "shared_frontend_service_token_123456789"
	g.config.ServiceClients = []AgentServiceClientConfig{{
		ID: "shared-frontend", TenantID: "tenant-a",
		Token: token, Enabled: true, RequireUserID: true,
		AllowedRoles: []string{"Researcher"},
		Scopes: []string{
			agentOSScopeTasksSubmit,
			agentOSScopeTasksReadOwn,
			agentOSScopeTasksCancelOwn,
			agentOSScopeUsersActAs,
		},
	}}
	submit := func(subject, key, message string) agentOSHTTPResult {
		return performAgentOSRequest(
			g,
			http.MethodPost,
			"/api/v1/agent/tasks",
			map[string]interface{}{
				"message":    message,
				"agent_name": "Researcher",
				"session_id": "same-browser-session",
				"user_id":    subject,
			},
			map[string]string{
				"Authorization":     "Bearer " + token,
				"Idempotency-Key":   key,
				agentOSUserIDHeader: subject,
			},
		)
	}

	alice := submit("alice", "same-browser-operation", "subject private request")
	bob := submit("bob", "same-browser-operation", "subject private request")
	if alice.code != http.StatusAccepted || bob.code != http.StatusAccepted {
		t.Fatalf("subject submissions failed: alice=%d/%s bob=%d/%s", alice.code, alice.raw, bob.code, bob.raw)
	}
	aliceTaskID := agentOSTaskIDFromResponse(t, alice)
	bobTaskID := agentOSTaskIDFromResponse(t, bob)
	if aliceTaskID == bobTaskID {
		t.Fatalf("subjects shared idempotent task %q", aliceTaskID)
	}
	if alice.body["subject_id"] != "alice" || alice.body["user_id"] != "alice" ||
		bob.body["subject_id"] != "bob" || bob.body["user_id"] != "bob" {
		t.Fatalf("subject response metadata mismatch: alice=%#v bob=%#v", alice.body, bob.body)
	}
	aliceSession, _ := alice.body["session_id"].(string)
	bobSession, _ := bob.body["session_id"].(string)
	if aliceSession == "" || bobSession == "" || aliceSession == bobSession {
		t.Fatalf("subjects shared session: alice=%q bob=%q", aliceSession, bobSession)
	}
	aliceFollowup := submit("alice", "alice-followup", "continue the same conversation")
	if aliceFollowup.code != http.StatusAccepted {
		t.Fatalf("alice follow-up code=%d body=%s", aliceFollowup.code, aliceFollowup.raw)
	}
	if followupSession, _ := aliceFollowup.body["session_id"].(string); followupSession != aliceSession {
		t.Fatalf(
			"same user did not reuse conversation: first=%q followup=%q",
			aliceSession,
			followupSession,
		)
	}
	if agentOSTaskIDFromResponse(t, aliceFollowup) == aliceTaskID {
		t.Fatal("different conversation turn reused the first task")
	}

	aliceTask, aliceOK := g.gatewayTaskSnapshotWithDB(aliceTaskID)
	bobTask, bobOK := g.gatewayTaskSnapshotWithDB(bobTaskID)
	if !aliceOK || !bobOK || aliceTask.Request == nil || bobTask.Request == nil {
		t.Fatalf("subject tasks unavailable: alice=%+v bob=%+v", aliceTask, bobTask)
	}
	if aliceTask.SubjectID != "alice" || aliceTask.Request.SubjectID != "alice" ||
		bobTask.SubjectID != "bob" || bobTask.Request.SubjectID != "bob" {
		t.Fatalf("subject was not propagated to tasks: alice=%+v bob=%+v", aliceTask, bobTask)
	}
	if sameProjectWorkspace(aliceTask.ResolvedWorkspace, bobTask.ResolvedWorkspace) {
		t.Fatalf("subjects shared workspace %q", aliceTask.ResolvedWorkspace)
	}
	wantAliceWorkspace := personalAgentOSWorkspace(manager.TenantDir("tenant-a"), "tenant-a", "alice")
	wantBobWorkspace := personalAgentOSWorkspace(manager.TenantDir("tenant-a"), "tenant-a", "bob")
	if !sameProjectWorkspace(aliceTask.ResolvedWorkspace, wantAliceWorkspace) ||
		!sameProjectWorkspace(bobTask.ResolvedWorkspace, wantBobWorkspace) {
		t.Fatalf("personal workspaces mismatch: alice=%q bob=%q", aliceTask.ResolvedWorkspace, bobTask.ResolvedWorkspace)
	}

	var idempotencyRecords []AgentTaskRequestRecord
	if err := g.db.Where("caller_id = ?", "shared-frontend").
		Order("subject_id").
		Find(&idempotencyRecords).Error; err != nil {
		t.Fatal(err)
	}
	if len(idempotencyRecords) != 3 ||
		idempotencyRecords[0].SubjectID != "alice" ||
		idempotencyRecords[1].SubjectID != "alice" ||
		idempotencyRecords[2].SubjectID != "bob" {
		t.Fatalf("subject idempotency records mismatch: %+v", idempotencyRecords)
	}

	aliceReplay := submit("alice", "same-browser-operation", "subject private request")
	if aliceReplay.code != http.StatusOK ||
		agentOSTaskIDFromResponse(t, aliceReplay) != aliceTaskID ||
		aliceReplay.body["idempotent_replay"] != true {
		t.Fatalf("same-subject idempotent replay failed: code=%d body=%s", aliceReplay.code, aliceReplay.raw)
	}
}

func TestAgentOSServiceCannotActAsSubjectWithoutScope(t *testing.T) {
	agent := &agentOSAPIScenarioAgent{result: "must not run"}
	g, _ := newAgentOSAPIScenarioGateway(t, agent)
	manager := NewTenantManager(filepath.Join(t.TempDir(), "tenants"), log.New(io.Discard, "", 0))
	if err := manager.Init([]TenantConfig{{
		ID: "tenant-a", Name: "Tenant A", Token: "tenant-a-admin-token",
	}}, "root-token"); err != nil {
		t.Fatal(err)
	}
	g.tenantMgr = manager
	g.config.Gateway.AuthEnabled = true
	const token = "service_without_act_as_token_123456789"
	g.config.ServiceClients = []AgentServiceClientConfig{{
		ID: "plain-service", TenantID: "tenant-a",
		Token: token, Enabled: true, AllowedRoles: []string{"Researcher"},
	}}
	response := performAgentOSRequest(
		g,
		http.MethodPost,
		"/api/v1/agent/tasks",
		map[string]interface{}{
			"message": "must not impersonate", "agent_name": "Researcher", "user_id": "alice",
		},
		map[string]string{"Authorization": "Bearer " + token},
	)
	if response.code != http.StatusForbidden || !strings.Contains(response.raw, agentOSScopeUsersActAs) {
		t.Fatalf("act-as without scope code=%d body=%s", response.code, response.raw)
	}
	if agent.calls.Load() != 0 {
		t.Fatalf("unauthorized act-as executed agent %d time(s)", agent.calls.Load())
	}
	headerResponse := performAgentOSRequest(
		g,
		http.MethodPost,
		"/api/v1/agent/tasks",
		map[string]interface{}{
			"message":    "must not delegate through a header",
			"agent_name": "Researcher",
		},
		map[string]string{
			"Authorization":     "Bearer " + token,
			agentOSUserIDHeader: "alice",
		},
	)
	if headerResponse.code != http.StatusForbidden ||
		headerResponse.body["code"] != "user_act_as_forbidden" {
		t.Fatalf(
			"header act-as without scope code=%d body=%s",
			headerResponse.code,
			headerResponse.raw,
		)
	}
}

func TestAgentOSServiceCanRequireUserIDForEverySubmission(t *testing.T) {
	agent := &agentOSAPIScenarioAgent{result: "subject scoped result"}
	g, _ := newAgentOSAPIScenarioGateway(t, agent)
	manager := NewTenantManager(filepath.Join(t.TempDir(), "tenants"), log.New(io.Discard, "", 0))
	if err := manager.Init([]TenantConfig{{
		ID: "tenant-a", Name: "Tenant A", Token: "tenant-a-admin-token",
	}}, "root-token"); err != nil {
		t.Fatal(err)
	}
	g.tenantMgr = manager
	g.config.Gateway.AuthEnabled = true
	const token = "service_requiring_user_id_token_123456789"
	g.config.ServiceClients = []AgentServiceClientConfig{{
		ID: "multi-user-service", TenantID: "tenant-a",
		Token: token, Enabled: true, RequireUserID: true,
		AllowedRoles: []string{"Researcher"},
		Scopes: []string{
			agentOSScopeTasksSubmit,
			agentOSScopeTasksReadOwn,
			agentOSScopeUsersActAs,
		},
	}}
	headers := map[string]string{"Authorization": "Bearer " + token}

	missing := performAgentOSRequest(
		g,
		http.MethodPost,
		"/api/v1/agent/tasks",
		map[string]interface{}{
			"message":    "must not enter a shared service partition",
			"agent_name": "Researcher",
			"session_id": "shared-session",
		},
		headers,
	)
	if missing.code != http.StatusBadRequest ||
		missing.body["code"] != "user_id_required" {
		t.Fatalf("missing user_id code=%d body=%s", missing.code, missing.raw)
	}
	var count int64
	if err := g.db.Model(&GatewayTaskRecord{}).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 || agent.calls.Load() != 0 {
		t.Fatalf("missing user_id persisted=%d executed=%d", count, agent.calls.Load())
	}

	bodyOnly := performAgentOSRequest(
		g,
		http.MethodPost,
		"/api/v1/agent/tasks",
		map[string]interface{}{
			"message":    "body identity alone must not weaken read isolation",
			"agent_name": "Researcher",
			"user_id":    "alice",
		},
		headers,
	)
	if bodyOnly.code != http.StatusBadRequest ||
		bodyOnly.body["code"] != "user_id_required" {
		t.Fatalf("body-only user_id code=%d body=%s", bodyOnly.code, bodyOnly.raw)
	}

	aliceHeaders := map[string]string{
		"Authorization":        "Bearer " + token,
		agentOSUserIDHeader:    "alice",
		"Idempotency-Key":      "alice-private-request",
		agentOSRequestIDHeader: "alice-private-request",
	}
	accepted := performAgentOSRequest(
		g,
		http.MethodPost,
		"/api/v1/agent/tasks",
		map[string]interface{}{
			"message":    "use the caller's private partition",
			"agent_name": "Researcher",
			"session_id": "shared-session",
			"user_id":    "alice",
		},
		aliceHeaders,
	)
	if accepted.code != http.StatusAccepted ||
		accepted.body["subject_id"] != "alice" {
		t.Fatalf("explicit user_id code=%d body=%s", accepted.code, accepted.raw)
	}

	mismatch := performAgentOSRequest(
		g,
		http.MethodPost,
		"/api/v1/agent/tasks",
		map[string]interface{}{
			"message":    "must not switch the delegated subject",
			"agent_name": "Researcher",
			"user_id":    "bob",
		},
		aliceHeaders,
	)
	if mismatch.code != http.StatusBadRequest ||
		mismatch.body["code"] != "user_id_mismatch" {
		t.Fatalf("mismatched user_id code=%d body=%s", mismatch.code, mismatch.raw)
	}
}

func TestAgentOSServiceRejectsSubjectIDThatWouldBeTruncated(t *testing.T) {
	agent := &agentOSAPIScenarioAgent{result: "must not run"}
	g, _ := newAgentOSAPIScenarioGateway(t, agent)
	manager := NewTenantManager(filepath.Join(t.TempDir(), "tenants"), log.New(io.Discard, "", 0))
	if err := manager.Init([]TenantConfig{{
		ID: "tenant-a", Name: "Tenant A", Token: "tenant-a-admin-token",
	}}, "root-token"); err != nil {
		t.Fatal(err)
	}
	g.tenantMgr = manager
	g.config.Gateway.AuthEnabled = true
	const token = "service_with_act_as_token_123456789"
	g.config.ServiceClients = []AgentServiceClientConfig{{
		ID: "trusted-service", TenantID: "tenant-a",
		Token: token, Enabled: true, AllowedRoles: []string{"Researcher"},
		Scopes: []string{agentOSScopeTasksSubmit, agentOSScopeUsersActAs},
	}}
	response := performAgentOSRequest(
		g,
		http.MethodPost,
		"/api/v1/agent/tasks",
		map[string]interface{}{
			"message": "must reject ambiguous owner", "agent_name": "Researcher",
			"user_id": strings.Repeat("x", 129),
		},
		map[string]string{"Authorization": "Bearer " + token},
	)
	if response.code != http.StatusBadRequest || !strings.Contains(response.raw, "128") {
		t.Fatalf("oversized subject code=%d body=%s", response.code, response.raw)
	}
	if agent.calls.Load() != 0 {
		t.Fatalf("oversized subject executed agent %d time(s)", agent.calls.Load())
	}
	var count int64
	if err := g.db.Model(&GatewayTaskRecord{}).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("oversized subject persisted %d task(s)", count)
	}
}

func TestAgentOSExternalAPIScenarioMatrix(t *testing.T) {
	t.Run("async submission returns durable handle then completes", func(t *testing.T) {
		agent := &agentOSAPIScenarioAgent{result: "complex answer complete"}
		g, workspace := newAgentOSAPIScenarioGateway(t, agent)
		response := submitAgentOSScenario(g, "erp-service", "job-001", nil)
		if response.code != http.StatusAccepted {
			t.Fatalf("submit code=%d body=%s", response.code, response.raw)
		}
		taskID := agentOSTaskIDFromResponse(t, response)
		if !strings.HasPrefix(taskID, "agt_") || response.body["status"] != "pending" {
			t.Fatalf("unexpected accepted task representation: %#v", response.body)
		}
		links, _ := response.body["links"].(map[string]interface{})
		if links["self"] == "" || links["events"] == "" || links["cancel"] == "" || response.location == "" {
			t.Fatalf("task discovery links missing: body=%#v location=%q", response.body, response.location)
		}

		completed := waitForAgentOSTask(t, g, taskID, "completed")
		if fmt.Sprint(completed.Result) != "complex answer complete" || completed.RunAttempt != 1 || completed.CurrentPhase != "completed" {
			t.Fatalf("unexpected completed task: %+v", completed)
		}
		var record GatewayTaskRecord
		if err := g.db.Where("task_id = ?", taskID).First(&record).Error; err != nil {
			t.Fatal(err)
		}
		if record.CallerID != "erp-service" || !sameProjectWorkspace(record.ResolvedWorkspace, workspace) || record.HeartbeatAt == nil {
			t.Fatalf("durable runtime fields missing: %+v", record)
		}
	})

	t.Run("same caller and idempotency key returns exactly one task", func(t *testing.T) {
		agent := &agentOSAPIScenarioAgent{result: "one result"}
		g, _ := newAgentOSAPIScenarioGateway(t, agent)
		first := submitAgentOSScenario(g, "crm", "same-operation", nil)
		firstID := agentOSTaskIDFromResponse(t, first)
		_ = waitForAgentOSTask(t, g, firstID, "completed")
		second := submitAgentOSScenario(g, "crm", "same-operation", nil)
		secondID := agentOSTaskIDFromResponse(t, second)
		if firstID != secondID || second.code != http.StatusOK || second.body["idempotent_replay"] != true {
			t.Fatalf("idempotent replay mismatch: first=%#v second=%#v", first.body, second.body)
		}
		if got := agent.calls.Load(); got != 1 {
			t.Fatalf("agent calls=%d, want 1", got)
		}
	})

	t.Run("same key from different callers remains isolated", func(t *testing.T) {
		agent := &agentOSAPIScenarioAgent{result: "caller scoped"}
		g, _ := newAgentOSAPIScenarioGateway(t, agent)
		left := submitAgentOSScenario(g, "app-left", "shared-key", nil)
		right := submitAgentOSScenario(g, "app-right", "shared-key", nil)
		leftID := agentOSTaskIDFromResponse(t, left)
		rightID := agentOSTaskIDFromResponse(t, right)
		if leftID == rightID {
			t.Fatalf("different callers shared task %s", leftID)
		}
		_ = waitForAgentOSTask(t, g, leftID, "completed")
		_ = waitForAgentOSTask(t, g, rightID, "completed")
		if got := agent.calls.Load(); got != 2 {
			t.Fatalf("agent calls=%d, want 2", got)
		}
	})

	t.Run("concurrent duplicate submissions converge on one running task", func(t *testing.T) {
		started := make(chan struct{})
		release := make(chan struct{})
		agent := &agentOSAPIScenarioAgent{result: "deduplicated", started: started, release: release}
		g, _ := newAgentOSAPIScenarioGateway(t, agent)
		const clients = 8
		results := make(chan agentOSHTTPResult, clients)
		for i := 0; i < clients; i++ {
			go func() { results <- submitAgentOSScenario(g, "batch-runner", "fan-in-key", nil) }()
		}
		responses := make([]agentOSHTTPResult, 0, clients)
		for i := 0; i < clients; i++ {
			select {
			case response := <-results:
				responses = append(responses, response)
			case <-time.After(6 * time.Second):
				close(release)
				t.Fatal("concurrent submit did not return")
			}
		}
		ids := make(map[string]bool)
		accepted := 0
		for _, response := range responses {
			if response.code != http.StatusAccepted && response.code != http.StatusOK {
				close(release)
				t.Fatalf("duplicate submit code=%d body=%s", response.code, response.raw)
			}
			if response.code == http.StatusAccepted {
				accepted++
			}
			ids[agentOSTaskIDFromResponse(t, response)] = true
		}
		if len(ids) != 1 || accepted != 1 {
			close(release)
			t.Fatalf("ids=%v accepted=%d, want one id and one creator", ids, accepted)
		}
		waitForAgentOSSignal(t, started, "deduplicated agent start")
		if got := agent.calls.Load(); got != 1 {
			close(release)
			t.Fatalf("agent calls=%d, want 1", got)
		}
		close(release)
		for id := range ids {
			_ = waitForAgentOSTask(t, g, id, "completed")
		}
	})

	t.Run("failed validation releases reserved idempotency key", func(t *testing.T) {
		agent := &agentOSAPIScenarioAgent{result: "valid retry"}
		g, _ := newAgentOSAPIScenarioGateway(t, agent)
		bad := submitAgentOSScenario(g, "workflow", "reusable-after-error", map[string]interface{}{"session_id": "missing-session"})
		if bad.code != http.StatusConflict || !strings.Contains(bad.raw, "unknown web session") {
			t.Fatalf("unknown session response: code=%d body=%s", bad.code, bad.raw)
		}
		good := submitAgentOSScenario(g, "workflow", "reusable-after-error", nil)
		if good.code != http.StatusAccepted || good.body["idempotent_replay"] == true {
			t.Fatalf("released key was not reusable: code=%d body=%s", good.code, good.raw)
		}
		_ = waitForAgentOSTask(t, g, agentOSTaskIDFromResponse(t, good), "completed")
	})

	t.Run("restart removes idempotency reservation without durable task", func(t *testing.T) {
		agent := &agentOSAPIScenarioAgent{result: "accepted after restart cleanup"}
		g, _ := newAgentOSAPIScenarioGateway(t, agent)
		caller, key := "crashed-client", "reserved-before-crash"
		orphan := AgentTaskRequestRecord{
			KeyHash: agentTaskIdempotencyHash(caller, key), CallerID: caller,
			TaskID: "agt_never_persisted", CreatedAt: time.Now().Add(-10 * time.Minute),
		}
		if err := g.db.Create(&orphan).Error; err != nil {
			t.Fatal(err)
		}
		fresh := AgentTaskRequestRecord{
			KeyHash: agentTaskIdempotencyHash("other-live-pod", "in-flight"), CallerID: "other-live-pod",
			TaskID: "agt_still_being_created", CreatedAt: time.Now(),
		}
		if err := g.db.Create(&fresh).Error; err != nil {
			t.Fatal(err)
		}
		removed, err := recoverOrphanAgentTaskRequests(g.db)
		if err != nil || removed != 1 {
			t.Fatalf("orphan reservation cleanup = %d, %v; want 1, nil", removed, err)
		}
		var freshCount int64
		if err := g.db.Model(&AgentTaskRequestRecord{}).Where("task_id = ?", fresh.TaskID).Count(&freshCount).Error; err != nil || freshCount != 1 {
			t.Fatalf("fresh reservation was removed: count=%d err=%v", freshCount, err)
		}
		response := submitAgentOSScenario(g, caller, key, nil)
		if response.code != http.StatusAccepted || response.body["idempotent_replay"] == true {
			t.Fatalf("cleaned key could not be submitted: code=%d body=%s", response.code, response.raw)
		}
		_ = waitForAgentOSTask(t, g, agentOSTaskIDFromResponse(t, response), "completed")
	})

	t.Run("omitted session is server generated and workspace bound", func(t *testing.T) {
		agent := &agentOSAPIScenarioAgent{result: "bound"}
		g, workspace := newAgentOSAPIScenarioGateway(t, agent)
		response := submitAgentOSScenario(g, "desktop-app", "session-create", nil)
		sessionID, _ := response.body["session_id"].(string)
		if response.code != http.StatusAccepted || !strings.HasPrefix(sessionID, "web_") {
			t.Fatalf("server session not returned: %#v", response.body)
		}
		var session ChatSession
		if err := g.db.Where("id = ?", sessionID).First(&session).Error; err != nil {
			t.Fatal(err)
		}
		if !sameProjectWorkspace(session.Workspace, workspace) || session.ProjectID != g.project.ProjectID {
			t.Fatalf("session is not project-bound: %+v project=%+v", session, g.project)
		}
		_ = waitForAgentOSTask(t, g, agentOSTaskIDFromResponse(t, response), "completed")
	})

	t.Run("existing session cannot silently cross workspace", func(t *testing.T) {
		agent := &agentOSAPIScenarioAgent{result: "first workspace"}
		g, _ := newAgentOSAPIScenarioGateway(t, agent)
		first := submitAgentOSScenario(g, "ide", "workspace-one", nil)
		sessionID, _ := first.body["session_id"].(string)
		_ = waitForAgentOSTask(t, g, agentOSTaskIDFromResponse(t, first), "completed")

		otherWorkspace := t.TempDir()
		g.project = NewProjectContext(otherWorkspace)
		g.config.Workspace = otherWorkspace
		second := submitAgentOSScenario(g, "ide", "workspace-two", map[string]interface{}{"session_id": sessionID})
		if second.code != http.StatusConflict || !strings.Contains(second.raw, "cannot run") {
			t.Fatalf("cross-workspace session was not rejected: code=%d body=%s", second.code, second.raw)
		}
		if got := agent.calls.Load(); got != 1 {
			t.Fatalf("agent calls=%d, cross-workspace request should not execute", got)
		}
	})

	t.Run("status exposes live model phase heartbeat and run attempt", func(t *testing.T) {
		started := make(chan struct{})
		release := make(chan struct{})
		agent := &agentOSAPIScenarioAgent{result: "after wait", started: started, release: release}
		g, _ := newAgentOSAPIScenarioGateway(t, agent)
		submitted := submitAgentOSScenario(g, "monitor", "live-status", nil)
		taskID := agentOSTaskIDFromResponse(t, submitted)
		waitForAgentOSSignal(t, started, "model phase")
		response := performAgentOSRequest(g, http.MethodGet, "/api/v1/agent/tasks/"+taskID, nil, nil)
		if response.code != http.StatusOK || response.body["status"] != "running" ||
			response.body["phase"] != "正在分析" || response.body["phase_code"] != "model" {
			close(release)
			t.Fatalf("unexpected live status: code=%d body=%s", response.code, response.raw)
		}
		if response.body["run_attempt"] != float64(1) || response.body["heartbeat_at"] == nil || response.body["heartbeat_age_ms"] == nil {
			close(release)
			t.Fatalf("runtime liveness fields missing: %#v", response.body)
		}
		close(release)
		_ = waitForAgentOSTask(t, g, taskID, "completed")
	})

	t.Run("cancel wins over a late model result", func(t *testing.T) {
		started := make(chan struct{})
		finished := make(chan struct{})
		release := make(chan struct{})
		agent := &agentOSAPIScenarioAgent{
			result: "late result that must not win", started: started, finished: finished,
			release: release, ignoreCancellation: true,
		}
		g, _ := newAgentOSAPIScenarioGateway(t, agent)
		submitted := submitAgentOSScenario(g, "ops", "cancel-race", nil)
		taskID := agentOSTaskIDFromResponse(t, submitted)
		waitForAgentOSSignal(t, started, "late-result agent start")
		canceled := performAgentOSRequest(g, http.MethodPost, "/api/v1/agent/tasks/"+taskID+"/cancel", nil, nil)
		if canceled.code != http.StatusOK || canceled.body["status"] != "stopped" {
			close(release)
			t.Fatalf("cancel failed: code=%d body=%s", canceled.code, canceled.raw)
		}
		close(release)
		waitForAgentOSSignal(t, finished, "late agent finish")
		time.Sleep(30 * time.Millisecond)
		stopped := waitForAgentOSTask(t, g, taskID, "stopped")
		if stopped.Status != "stopped" || fmt.Sprint(stopped.Result) == "late result that must not win" {
			t.Fatalf("late result overwrote cancellation: %+v", stopped)
		}
		restarted := &GatewayService{db: g.db, logger: log.New(io.Discard, "", 0), tasks: make(map[string]*GatewayTask)}
		restarted.initSSE()
		durable := performAgentOSRequest(restarted, http.MethodGet, "/api/v1/agent/tasks/"+taskID, nil, nil)
		if durable.code != http.StatusOK || durable.body["status"] != "stopped" {
			t.Fatalf("cancellation was not durable before response: code=%d body=%s", durable.code, durable.raw)
		}
		again := performAgentOSRequest(g, http.MethodDelete, "/api/v1/agent/tasks/"+taskID, nil, nil)
		if again.code != http.StatusConflict {
			t.Fatalf("second cancel code=%d, want 409: %s", again.code, again.raw)
		}
	})

	t.Run("failed agent remains queryable with durable error", func(t *testing.T) {
		agent := &agentOSAPIScenarioAgent{result: "partial diagnostic", err: errors.New("simulated model exhaustion")}
		g, _ := newAgentOSAPIScenarioGateway(t, agent)
		submitted := submitAgentOSScenario(g, "scheduler", "failed-task", nil)
		taskID := agentOSTaskIDFromResponse(t, submitted)
		failed := waitForAgentOSTask(t, g, taskID, "failed")
		if failed.Error == nil || !strings.Contains(failed.Error.Error(), "simulated model exhaustion") {
			t.Fatalf("failed task lost error: %+v", failed)
		}
		queried := performAgentOSRequest(g, http.MethodGet, "/api/v1/agent/tasks/"+taskID, nil, nil)
		if queried.code != http.StatusOK || queried.body["status"] != "failed" || !strings.Contains(fmt.Sprint(queried.body["error"]), "simulated model exhaustion") {
			t.Fatalf("failed status not queryable: %s", queried.raw)
		}
	})

	t.Run("completed task remains queryable after gateway restart", func(t *testing.T) {
		agent := &agentOSAPIScenarioAgent{result: "persisted across restart"}
		g, _ := newAgentOSAPIScenarioGateway(t, agent)
		submitted := submitAgentOSScenario(g, "daemon", "restart-query", nil)
		taskID := agentOSTaskIDFromResponse(t, submitted)
		_ = waitForAgentOSTask(t, g, taskID, "completed")

		restarted := &GatewayService{db: g.db, logger: log.New(io.Discard, "", 0), tasks: make(map[string]*GatewayTask)}
		restarted.initSSE()
		queried := performAgentOSRequest(restarted, http.MethodGet, "/api/v1/agent/tasks/"+taskID, nil, nil)
		if queried.code != http.StatusOK || queried.body["status"] != "completed" || fmt.Sprint(queried.body["result"]) != "persisted across restart" {
			t.Fatalf("durable restart query failed: code=%d body=%s", queried.code, queried.raw)
		}
	})

	t.Run("caller and status filters isolate task listing", func(t *testing.T) {
		agent := &agentOSAPIScenarioAgent{result: "unused"}
		g, _ := newAgentOSAPIScenarioGateway(t, agent)
		now := time.Now()
		records := []GatewayTaskRecord{
			{TaskID: "list-a-complete", CallerID: "app-a", QueueName: agentOSQueueName, Status: "completed", CurrentPhase: "completed", CreatedAt: now.Add(-time.Minute), UpdatedAt: now},
			{TaskID: "list-a-failed", CallerID: "app-a", QueueName: agentOSQueueName, Status: "failed", CurrentPhase: "failed", CreatedAt: now.Add(-time.Second), UpdatedAt: now},
			{TaskID: "list-b-complete", CallerID: "app-b", QueueName: agentOSQueueName, Status: "completed", CurrentPhase: "completed", CreatedAt: now, UpdatedAt: now},
		}
		for i := range records {
			if err := g.db.Create(&records[i]).Error; err != nil {
				t.Fatal(err)
			}
		}
		response := performAgentOSRequest(g, http.MethodGet, "/api/v1/agent/tasks?caller_id=app-a&status=completed", nil, nil)
		tasks, _ := response.body["tasks"].([]interface{})
		if response.code != http.StatusOK || response.body["count"] != float64(1) || len(tasks) != 1 {
			t.Fatalf("filtered list mismatch: code=%d body=%s", response.code, response.raw)
		}
		item, _ := tasks[0].(map[string]interface{})
		if item["task_id"] != "list-a-complete" {
			t.Fatalf("wrong filtered task: %#v", item)
		}
	})

	t.Run("valid attachment is normalized and reaches agent manifest", func(t *testing.T) {
		agent := &agentOSAPIScenarioAgent{result: "attachment processed"}
		g, workspace := newAgentOSAPIScenarioGateway(t, agent)
		inputDir := filepath.Join(workspace, "inputs")
		if err := os.MkdirAll(inputDir, 0o755); err != nil {
			t.Fatal(err)
		}
		inputPath := filepath.Join(inputDir, "requirements.txt")
		if err := os.WriteFile(inputPath, []byte("complex requirements"), 0o644); err != nil {
			t.Fatal(err)
		}
		response := submitAgentOSScenario(g, "document-app", "attachment", map[string]interface{}{
			"attachments": []map[string]interface{}{{"path": "inputs/requirements.txt"}},
		})
		if response.code != http.StatusAccepted {
			t.Fatalf("attachment submit failed: code=%d body=%s", response.code, response.raw)
		}
		_ = waitForAgentOSTask(t, g, agentOSTaskIDFromResponse(t, response), "completed")
		inputs := agent.inputSnapshot()
		if len(inputs) != 1 || !strings.Contains(inputs[0], "OpenKitty 用户附件清单") || !strings.Contains(inputs[0], "inputs/requirements.txt") {
			t.Fatalf("agent did not receive normalized attachment manifest: %#v", inputs)
		}
	})

	t.Run("attachment outside workspace is rejected before execution", func(t *testing.T) {
		agent := &agentOSAPIScenarioAgent{result: "must not execute"}
		g, _ := newAgentOSAPIScenarioGateway(t, agent)
		outside := filepath.Join(t.TempDir(), "outside.txt")
		if err := os.WriteFile(outside, []byte("outside"), 0o644); err != nil {
			t.Fatal(err)
		}
		response := submitAgentOSScenario(g, "document-app", "outside-attachment", map[string]interface{}{
			"attachments": []map[string]interface{}{{"path": outside}},
		})
		if response.code != http.StatusBadRequest || !strings.Contains(response.raw, "outside the workspace") {
			t.Fatalf("outside attachment response: code=%d body=%s", response.code, response.raw)
		}
		if agent.calls.Load() != 0 {
			t.Fatal("outside attachment reached agent")
		}
	})

	t.Run("distinct submissions receive distinct server ids", func(t *testing.T) {
		agent := &agentOSAPIScenarioAgent{result: "unique"}
		g, _ := newAgentOSAPIScenarioGateway(t, agent)
		first := submitAgentOSScenario(g, "app", "unique-1", nil)
		second := submitAgentOSScenario(g, "app", "unique-2", nil)
		firstTask, secondTask := agentOSTaskIDFromResponse(t, first), agentOSTaskIDFromResponse(t, second)
		firstSession, _ := first.body["session_id"].(string)
		secondSession, _ := second.body["session_id"].(string)
		if firstTask == secondTask || firstSession == secondSession || firstSession == "" || secondSession == "" {
			t.Fatalf("server ids were reused: tasks=%q/%q sessions=%q/%q", firstTask, secondTask, firstSession, secondSession)
		}
		_ = waitForAgentOSTask(t, g, firstTask, "completed")
		_ = waitForAgentOSTask(t, g, secondTask, "completed")
	})

	t.Run("input contract rejects malformed and ambiguous requests", func(t *testing.T) {
		agent := &agentOSAPIScenarioAgent{result: "must not execute"}
		g, _ := newAgentOSAPIScenarioGateway(t, agent)
		cases := []struct {
			name    string
			payload interface{}
			header  map[string]string
			want    int
		}{
			{name: "empty", payload: map[string]interface{}{"agent_name": "Researcher"}, want: http.StatusBadRequest},
			{name: "unknown field", payload: map[string]interface{}{"message": "x", "agent_name": "Researcher", "surprise": true}, want: http.StatusBadRequest},
			{name: "path-like agent", payload: map[string]interface{}{"message": "x", "agent_name": "../Researcher"}, want: http.StatusBadRequest},
			{name: "oversized idempotency key", payload: map[string]interface{}{"message": "x", "agent_name": "Researcher"}, header: map[string]string{"Idempotency-Key": strings.Repeat("k", 257)}, want: http.StatusBadRequest},
			{name: "oversized session", payload: map[string]interface{}{"message": "x", "agent_name": "Researcher", "session_id": strings.Repeat("s", 65)}, want: http.StatusBadRequest},
			{name: "oversized user", payload: map[string]interface{}{"message": "x", "agent_name": "Researcher", "user_id": strings.Repeat("u", 129)}, want: http.StatusBadRequest},
			{name: "oversized caller", payload: map[string]interface{}{"message": "x", "agent_name": "Researcher", "caller_id": strings.Repeat("c", 129)}, want: http.StatusBadRequest},
			{name: "oversized role", payload: map[string]interface{}{"message": "x", "agent_name": "Researcher", "role_id": strings.Repeat("r", 129)}, want: http.StatusBadRequest},
			{name: "oversized agent", payload: map[string]interface{}{"message": "x", "agent_name": strings.Repeat("a", 101)}, want: http.StatusBadRequest},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				response := performAgentOSRequest(g, http.MethodPost, "/api/v1/agent/tasks", tc.payload, tc.header)
				if response.code != tc.want {
					t.Fatalf("code=%d want=%d body=%s", response.code, tc.want, response.raw)
				}
			})
		}
		if agent.calls.Load() != 0 {
			t.Fatalf("invalid requests executed agent %d times", agent.calls.Load())
		}
	})

	t.Run("unknown task action and missing task return 404", func(t *testing.T) {
		agent := &agentOSAPIScenarioAgent{result: "unused"}
		g, _ := newAgentOSAPIScenarioGateway(t, agent)
		missing := performAgentOSRequest(g, http.MethodGet, "/api/v1/agent/tasks/not-found", nil, nil)
		unknownAction := performAgentOSRequest(g, http.MethodGet, "/api/v1/agent/tasks/not-found/unknown", nil, nil)
		if missing.code != http.StatusNotFound || unknownAction.code != http.StatusNotFound {
			t.Fatalf("missing/action codes=%d/%d", missing.code, unknownAction.code)
		}
	})
}

type lockedAgentOSSSERecorder struct {
	mu     sync.Mutex
	header http.Header
	code   int
	body   bytes.Buffer
}

func newLockedAgentOSSSERecorder() *lockedAgentOSSSERecorder {
	return &lockedAgentOSSSERecorder{header: make(http.Header), code: http.StatusOK}
}
func (r *lockedAgentOSSSERecorder) Header() http.Header  { return r.header }
func (r *lockedAgentOSSSERecorder) WriteHeader(code int) { r.code = code }
func (r *lockedAgentOSSSERecorder) Write(data []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.body.Write(data)
}
func (r *lockedAgentOSSSERecorder) Flush() {}
func (r *lockedAgentOSSSERecorder) String() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.body.String()
}

func TestAgentOSTaskEventsScenarioStreamsOnlyRequestedTask(t *testing.T) {
	agent := &agentOSAPIScenarioAgent{result: "unused"}
	g, _ := newAgentOSAPIScenarioGateway(t, agent)
	now := time.Now()
	task := &GatewayTask{
		ID: "agt_sse_target", Status: "running", CurrentPhase: "model", CreatedAt: now, HeartbeatAt: &now,
		Request:   &MessageRequest{TaskID: "agt_sse_target", TaskType: "chat", SessionId: "sse-session", AgentName: "Researcher"},
		QueueName: agentOSQueueName,
	}
	g.tasksMu.Lock()
	g.tasks[task.ID] = task
	g.tasksMu.Unlock()
	if err := SaveGatewayTaskToDB(g.db, task); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodGet, "/api/v1/agent/tasks/"+task.ID+"/events?client_id=scenario-client", nil).WithContext(ctx)
	recorder := newLockedAgentOSSSERecorder()
	done := make(chan struct{})
	go func() {
		g.handleAgentOSTaskByID(recorder, req)
		close(done)
	}()
	deadline := time.Now().Add(3 * time.Second)
	for !strings.Contains(recorder.String(), "event: connected") && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if !strings.Contains(recorder.String(), "event: connected") {
		cancel()
		t.Fatal("SSE connection event not emitted")
	}
	g.broadcastTaskUpdate("other-task", "failed", nil, errors.New("must not leak"))
	g.broadcastTaskUpdate(task.ID, "running", nil, nil)
	time.Sleep(20 * time.Millisecond)
	cancel()
	waitForAgentOSSignal(t, done, "SSE handler shutdown")
	stream := recorder.String()
	if !strings.Contains(stream, "event: task_update") || !strings.Contains(stream, task.ID) {
		t.Fatalf("target task update missing from SSE stream: %s", stream)
	}
	if strings.Contains(stream, "other-task") || strings.Contains(stream, "must not leak") {
		t.Fatalf("unrelated task leaked into scoped SSE stream: %s", stream)
	}
}

func TestAgentOSTaskEventsCloseAfterTerminalSnapshot(t *testing.T) {
	g, _ := newAgentOSAPIScenarioGateway(
		t,
		&agentOSAPIScenarioAgent{result: "unused"},
	)
	now := time.Now().Round(0)
	task := &GatewayTask{
		ID: "agt_sse_terminal", TenantID: defaultExecutionTenantID,
		Status: "running", CurrentPhase: "model", CreatedAt: now,
		HeartbeatAt: &now, QueueName: agentOSQueueName,
		Request: &MessageRequest{
			TaskID: "agt_sse_terminal", TaskType: "chat",
			SessionId: "sse-terminal-session", AgentName: "Researcher",
			TenantID: defaultExecutionTenantID,
		},
	}
	g.tasks[task.ID] = task
	if err := SaveGatewayTaskToDB(g.db, task); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(
		http.MethodGet,
		"/api/v1/agent/tasks/"+task.ID+"/events",
		nil,
	)
	recorder := newLockedAgentOSSSERecorder()
	done := make(chan struct{})
	go func() {
		g.handleAgentOSTaskByID(recorder, req)
		close(done)
	}()
	deadline := time.Now().Add(3 * time.Second)
	for !strings.Contains(recorder.String(), "event: connected") &&
		time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	completedAt := time.Now().Round(0)
	if err := g.db.Model(&GatewayTaskRecord{}).
		Where("task_id = ? AND queue_name = ?", task.ID, agentOSQueueName).
		Updates(map[string]interface{}{
			"status": "completed", "current_phase": "completed",
			"completed_at": completedAt, "heartbeat_at": completedAt,
			"result": `"done"`, "updated_at": completedAt,
		}).Error; err != nil {
		t.Fatal(err)
	}
	waitForAgentOSSignal(t, done, "terminal SSE shutdown")
	stream := recorder.String()
	if !strings.Contains(stream, "event: task_snapshot") ||
		!strings.Contains(stream, `"status":"completed"`) {
		t.Fatalf("terminal snapshot missing before stream close: %s", stream)
	}
}

func TestAgentOSTaskEventsCloseWhenCredentialIsRevoked(t *testing.T) {
	g, _ := newAgentOSAPIScenarioGateway(
		t,
		&agentOSAPIScenarioAgent{result: "unused"},
	)
	g.config.Gateway.AuthEnabled = true
	g.config.ServiceClients = []AgentServiceClientConfig{{
		ID: "stream-service", TenantID: "tenant-a",
		Token: "stream_service_token_1234567890", Enabled: true,
		AllowedRoles: []string{"role-a"},
	}}
	now := time.Now().Round(0)
	task := &GatewayTask{
		ID: "agt_sse_revoked", TenantID: "tenant-a", RoleID: "role-a",
		CallerID: "stream-service", Status: "running", CurrentPhase: "model",
		CreatedAt: now, HeartbeatAt: &now, QueueName: agentOSQueueName,
		Request: &MessageRequest{
			TaskID: "agt_sse_revoked", TaskType: "chat",
			SessionId: "sse-revoked-session", AgentName: "Researcher",
			TenantID: "tenant-a", RoleID: "role-a",
		},
	}
	g.tasks[task.ID] = task
	if err := SaveGatewayTaskToDB(g.db, task); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(
		http.MethodGet,
		"/api/v1/agent/tasks/"+task.ID+"/events",
		nil,
	)
	req.Header.Set("Authorization", "Bearer stream_service_token_1234567890")
	recorder := newLockedAgentOSSSERecorder()
	done := make(chan struct{})
	go func() {
		g.handleAgentOSTaskByID(recorder, req)
		close(done)
	}()
	deadline := time.Now().Add(3 * time.Second)
	for !strings.Contains(recorder.String(), "event: connected") &&
		time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if !strings.Contains(recorder.String(), "event: connected") {
		t.Fatal("SSE stream did not connect before credential revocation")
	}
	g.config.ServiceClients = nil
	waitForAgentOSSignal(t, done, "revoked SSE shutdown")
}
