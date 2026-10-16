package app

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"
)

const (
	scopeTestTokenA        = "scope_test_service_a_token_1234567890"
	scopeTestTokenB        = "scope_test_service_b_token_1234567890"
	scopeTestObserverToken = "scope_test_observer_token_1234567890"
	scopeTestOperatorToken = "scope_test_operator_token_1234567890"
)

func seedAgentOSScopeTask(
	t *testing.T,
	g *GatewayService,
	taskID, tenantID, roleID, callerID, status string,
) {
	t.Helper()
	now := time.Now().Round(0)
	record := GatewayTaskRecord{
		TaskID: taskID, TenantID: tenantID, RoleID: roleID,
		SessionID: "session-" + taskID, UserID: "service:" + callerID,
		TaskType: "chat", Status: status, Message: "scope test",
		AgentName: "Researcher", Channel: "api", CallerID: callerID,
		QueueName: agentOSQueueName, CurrentPhase: status,
		AvailableAt: &now, HeartbeatAt: &now, CreatedAt: now, UpdatedAt: now,
	}
	if err := g.db.Create(&record).Error; err != nil {
		t.Fatalf("seed task %s: %v", taskID, err)
	}
}

func configureAgentOSScopeClients(g *GatewayService, clients ...AgentServiceClientConfig) {
	g.config.Gateway.AuthEnabled = true
	g.config.ServiceClients = clients
}

func scopeTestHeaders(token string) map[string]string {
	return map[string]string{"Authorization": "Bearer " + token}
}

func listedAgentOSTaskIDs(t *testing.T, response agentOSHTTPResult) []string {
	t.Helper()
	raw, ok := response.body["tasks"].([]interface{})
	if !ok {
		t.Fatalf("task list is malformed: code=%d body=%s", response.code, response.raw)
	}
	ids := make([]string, 0, len(raw))
	for _, item := range raw {
		task, ok := item.(map[string]interface{})
		if !ok {
			t.Fatalf("task item is malformed: %#v", item)
		}
		ids = append(ids, fmt.Sprint(task["task_id"]))
	}
	sort.Strings(ids)
	return ids
}

func TestAgentOSServiceClientDefaultsIsolateSameTenantCallers(t *testing.T) {
	g, _ := newAgentOSAPIScenarioGateway(t, &agentOSAPIScenarioAgent{result: "unused"})
	configureAgentOSScopeClients(g,
		AgentServiceClientConfig{
			ID: "service-a", TenantID: "tenant-a", Token: scopeTestTokenA,
			Enabled: true, AllowedRoles: []string{"role-a"},
		},
		AgentServiceClientConfig{
			ID: "service-b", TenantID: "tenant-a", Token: scopeTestTokenB,
			Enabled: true, AllowedRoles: []string{"role-a"},
		},
	)
	seedAgentOSScopeTask(t, g, "task-a", "tenant-a", "role-a", "service-a", "pending")
	seedAgentOSScopeTask(t, g, "task-b", "tenant-a", "role-a", "service-b", "pending")

	own := performAgentOSRequest(
		g, http.MethodGet, "/api/v1/agent/tasks/task-a", nil, scopeTestHeaders(scopeTestTokenA),
	)
	if own.code != http.StatusOK {
		t.Fatalf("owner read code=%d body=%s", own.code, own.raw)
	}
	peer := performAgentOSRequest(
		g, http.MethodGet, "/api/v1/agent/tasks/task-b", nil, scopeTestHeaders(scopeTestTokenA),
	)
	if peer.code != http.StatusNotFound {
		t.Fatalf("same-tenant peer task leaked: code=%d body=%s", peer.code, peer.raw)
	}

	listed := performAgentOSRequest(
		g, http.MethodGet, "/api/v1/agent/tasks", nil, scopeTestHeaders(scopeTestTokenA),
	)
	if got := fmt.Sprint(listedAgentOSTaskIDs(t, listed)); got != "[task-a]" {
		t.Fatalf("default list=%s, want only caller-owned task", got)
	}
	spoofedFilter := performAgentOSRequest(
		g,
		http.MethodGet,
		"/api/v1/agent/tasks?caller_id=service-b",
		nil,
		scopeTestHeaders(scopeTestTokenA),
	)
	if ids := listedAgentOSTaskIDs(t, spoofedFilter); len(ids) != 0 {
		t.Fatalf("caller_id query expanded authorization: %v", ids)
	}

	cancelPeer := performAgentOSRequest(
		g, http.MethodPost, "/api/v1/agent/tasks/task-b/cancel", nil, scopeTestHeaders(scopeTestTokenA),
	)
	if cancelPeer.code != http.StatusNotFound {
		t.Fatalf("same-tenant peer cancellation code=%d body=%s", cancelPeer.code, cancelPeer.raw)
	}
	cancelOwn := performAgentOSRequest(
		g, http.MethodPost, "/api/v1/agent/tasks/task-a/cancel", nil, scopeTestHeaders(scopeTestTokenA),
	)
	if cancelOwn.code != http.StatusOK || cancelOwn.body["status"] != "stopped" {
		t.Fatalf("owner cancellation code=%d body=%s", cancelOwn.code, cancelOwn.raw)
	}
}

func TestAgentOSExplicitTaskScopesRemainRoleBounded(t *testing.T) {
	g, _ := newAgentOSAPIScenarioGateway(t, &agentOSAPIScenarioAgent{result: "unused"})
	configureAgentOSScopeClients(g,
		AgentServiceClientConfig{
			ID: "observer", TenantID: "tenant-a", Token: scopeTestObserverToken,
			Enabled:      true,
			AllowedRoles: []string{"role-a"},
			Scopes: []string{
				agentOSScopeTasksReadRole,
				agentOSScopeTasksCancelAny,
			},
		},
		AgentServiceClientConfig{
			ID: "operator", TenantID: "tenant-a", Token: scopeTestOperatorToken,
			Enabled:      true,
			AllowedRoles: []string{"role-a"},
			Scopes: []string{
				agentOSScopeTasksReadAny,
				agentOSScopeTasksCancelAny,
			},
		},
	)
	seedAgentOSScopeTask(t, g, "role-a-peer", "tenant-a", "role-a", "service-a", "pending")
	seedAgentOSScopeTask(t, g, "role-b-peer", "tenant-a", "role-b", "service-b", "pending")

	roleReader := performAgentOSRequest(
		g, http.MethodGet, "/api/v1/agent/tasks/role-a-peer", nil, scopeTestHeaders(scopeTestObserverToken),
	)
	if roleReader.code != http.StatusOK {
		t.Fatalf("tasks:read:role code=%d body=%s", roleReader.code, roleReader.raw)
	}
	roleHidden := performAgentOSRequest(
		g, http.MethodGet, "/api/v1/agent/tasks/role-b-peer", nil, scopeTestHeaders(scopeTestObserverToken),
	)
	if roleHidden.code != http.StatusNotFound {
		t.Fatalf("AllowedRoles did not bound role reader: code=%d body=%s", roleHidden.code, roleHidden.raw)
	}

	anyList := performAgentOSRequest(
		g, http.MethodGet, "/api/v1/agent/tasks", nil, scopeTestHeaders(scopeTestOperatorToken),
	)
	if got := fmt.Sprint(listedAgentOSTaskIDs(t, anyList)); got != "[role-a-peer]" {
		t.Fatalf("tasks:read:any ignored AllowedRoles: %s", got)
	}
	cancelOther := performAgentOSRequest(
		g, http.MethodPost, "/api/v1/agent/tasks/role-a-peer/cancel", nil, scopeTestHeaders(scopeTestOperatorToken),
	)
	if cancelOther.code != http.StatusOK {
		t.Fatalf("tasks:cancel:any code=%d body=%s", cancelOther.code, cancelOther.raw)
	}
	cancelWrongRole := performAgentOSRequest(
		g, http.MethodPost, "/api/v1/agent/tasks/role-b-peer/cancel", nil, scopeTestHeaders(scopeTestOperatorToken),
	)
	if cancelWrongRole.code != http.StatusNotFound {
		t.Fatalf("tasks:cancel:any escaped AllowedRoles: code=%d body=%s", cancelWrongRole.code, cancelWrongRole.raw)
	}
}

func TestAgentOSDataPlaneDoesNotExposeLegacyWebTasks(t *testing.T) {
	g, _ := newAgentOSAPIScenarioGateway(t, &agentOSAPIScenarioAgent{result: "unused"})
	now := time.Now().Round(0)
	legacy := GatewayTaskRecord{
		TaskID: "legacy-web-task", TenantID: defaultExecutionTenantID,
		RoleID: "Researcher", SessionID: "legacy-web-session",
		TaskType: "chat", Status: "pending", Message: "private browser chat",
		AgentName: "Researcher", Channel: "web", CallerID: "anonymous",
		QueueName: "", CurrentPhase: "pending",
		CreatedAt: now, UpdatedAt: now,
	}
	if err := g.db.Create(&legacy).Error; err != nil {
		t.Fatalf("seed legacy task: %v", err)
	}

	listed := performAgentOSRequest(g, http.MethodGet, "/api/v1/agent/tasks", nil, nil)
	if ids := listedAgentOSTaskIDs(t, listed); len(ids) != 0 {
		t.Fatalf("AgentOS list exposed legacy Web tasks: %v", ids)
	}
	detail := performAgentOSRequest(
		g, http.MethodGet, "/api/v1/agent/tasks/legacy-web-task", nil, nil,
	)
	if detail.code != http.StatusNotFound {
		t.Fatalf("AgentOS detail exposed legacy Web task: code=%d body=%s", detail.code, detail.raw)
	}
	cancelled := performAgentOSRequest(
		g, http.MethodPost, "/api/v1/agent/tasks/legacy-web-task/cancel", nil, nil,
	)
	if cancelled.code != http.StatusNotFound {
		t.Fatalf("AgentOS cancel reached legacy Web task: code=%d body=%s", cancelled.code, cancelled.raw)
	}
	approvals := performAgentOSRequest(
		g, http.MethodGet, "/api/v1/agent/tasks/legacy-web-task/approvals", nil, nil,
	)
	if approvals.code != http.StatusNotFound {
		t.Fatalf("AgentOS approvals reached legacy Web task: code=%d body=%s", approvals.code, approvals.raw)
	}
}

func TestAgentOSSubmitScopeIsRequiredWhenScopesAreExplicit(t *testing.T) {
	g, _ := newAgentOSAPIScenarioGateway(t, &agentOSAPIScenarioAgent{result: "unused"})
	configureAgentOSScopeClients(g, AgentServiceClientConfig{
		ID: "read-only", TenantID: "tenant-a",
		Token:   "scope_test_read_only_token_123456789",
		Enabled: true, AllowedRoles: []string{"Researcher"},
		Scopes: []string{agentOSScopeTasksReadOwn},
	})
	response := performAgentOSRequest(
		g,
		http.MethodPost,
		"/api/v1/agent/tasks",
		map[string]interface{}{"message": "must not run", "agent_name": "Researcher"},
		scopeTestHeaders("scope_test_read_only_token_123456789"),
	)
	if response.code != http.StatusForbidden {
		t.Fatalf("submission without tasks:submit code=%d body=%s", response.code, response.raw)
	}
}

func seedAgentOSScopeApproval(t *testing.T, g *GatewayService, taskID, approvalID string) {
	t.Helper()
	now := time.Now().Round(0)
	approval := ApprovalRequestRecord{
		ApprovalID: approvalID, ApprovalKey: "key-" + approvalID, Generation: 1,
		TaskID: taskID, TenantID: "tenant-a", RoleID: "role-a",
		ToolCallID: "call-" + approvalID, ToolName: "publish",
		ArgumentsHash: "hash-" + approvalID, ArgumentsPreview: `{"target":"production"}`,
		Reason: "operator decision required", Status: approvalStatusPending,
		RequestedAt: now, CreatedAt: now, UpdatedAt: now,
	}
	if err := g.db.Create(&approval).Error; err != nil {
		t.Fatalf("seed approval %s: %v", approvalID, err)
	}
}

func TestAgentOSApprovalScopesAndCanApproveCompatibility(t *testing.T) {
	g, _ := newAgentOSAPIScenarioGateway(t, &agentOSAPIScenarioAgent{result: "unused"})
	const (
		ownerToken      = "scope_test_owner_token_1234567890123"
		readerToken     = "scope_test_approval_reader_token_123456"
		deciderToken    = "scope_test_approval_decider_token_12345"
		canApproveToken = "scope_test_can_approve_token_123456789"
	)
	configureAgentOSScopeClients(g,
		AgentServiceClientConfig{
			ID: "owner", TenantID: "tenant-a", Token: ownerToken,
			Enabled: true, AllowedRoles: []string{"role-a"},
		},
		AgentServiceClientConfig{
			ID: "approval-reader", TenantID: "tenant-a", Token: readerToken,
			Enabled: true, AllowedRoles: []string{"role-a"},
			Scopes: []string{agentOSScopeApprovalsRead},
		},
		AgentServiceClientConfig{
			ID: "approval-decider", TenantID: "tenant-a", Token: deciderToken,
			Enabled: true, AllowedRoles: []string{"role-a"},
			Scopes: []string{agentOSScopeApprovalsDecide},
		},
		AgentServiceClientConfig{
			ID: "legacy-operator", TenantID: "tenant-a", Token: canApproveToken,
			Enabled: true, CanApprove: true, AllowedRoles: []string{"role-a"},
		},
	)
	seedAgentOSScopeTask(t, g, "approval-task-1", "tenant-a", "role-a", "owner", "waiting_approval")
	seedAgentOSScopeApproval(t, g, "approval-task-1", "approval-1")

	ownerTask := performAgentOSRequest(
		g, http.MethodGet, "/api/v1/agent/tasks/approval-task-1", nil, scopeTestHeaders(ownerToken),
	)
	if ownerTask.code != http.StatusOK {
		t.Fatalf("owner task read code=%d body=%s", ownerTask.code, ownerTask.raw)
	}
	if _, leaked := ownerTask.body["pending_approval"]; leaked {
		t.Fatalf("task response leaked approval details without approvals:read: %s", ownerTask.raw)
	}
	ownerApprovals := performAgentOSRequest(
		g, http.MethodGet, "/api/v1/agent/tasks/approval-task-1/approvals", nil, scopeTestHeaders(ownerToken),
	)
	if ownerApprovals.code != http.StatusForbidden {
		t.Fatalf("approval list without scope code=%d body=%s", ownerApprovals.code, ownerApprovals.raw)
	}

	readApprovals := performAgentOSRequest(
		g, http.MethodGet, "/api/v1/agent/tasks/approval-task-1/approvals", nil, scopeTestHeaders(readerToken),
	)
	if readApprovals.code != http.StatusOK || readApprovals.body["count"] != float64(1) {
		t.Fatalf("approvals:read code=%d body=%s", readApprovals.code, readApprovals.raw)
	}
	readerDecision := performAgentOSRequest(
		g,
		http.MethodPost,
		"/api/v1/agent/tasks/approval-task-1/approvals/approval-1/deny",
		nil,
		scopeTestHeaders(readerToken),
	)
	if readerDecision.code != http.StatusForbidden {
		t.Fatalf("approval reader decided request: code=%d body=%s", readerDecision.code, readerDecision.raw)
	}
	decided := performAgentOSRequest(
		g,
		http.MethodPost,
		"/api/v1/agent/tasks/approval-task-1/approvals/approval-1/deny",
		map[string]interface{}{"reason": "outside change window"},
		scopeTestHeaders(deciderToken),
	)
	if decided.code != http.StatusOK {
		t.Fatalf("approvals:decide code=%d body=%s", decided.code, decided.raw)
	}

	seedAgentOSScopeTask(t, g, "approval-task-2", "tenant-a", "role-a", "owner", "waiting_approval")
	seedAgentOSScopeApproval(t, g, "approval-task-2", "approval-2")
	legacyList := performAgentOSRequest(
		g, http.MethodGet, "/api/v1/agent/tasks/approval-task-2/approvals", nil, scopeTestHeaders(canApproveToken),
	)
	if legacyList.code != http.StatusOK {
		t.Fatalf("CanApprove did not map to approvals:read: code=%d body=%s", legacyList.code, legacyList.raw)
	}
	legacyDecision := performAgentOSRequest(
		g,
		http.MethodPost,
		"/api/v1/agent/tasks/approval-task-2/approvals/approval-2/deny",
		nil,
		scopeTestHeaders(canApproveToken),
	)
	if legacyDecision.code != http.StatusOK {
		t.Fatalf("CanApprove did not map to approvals:decide: code=%d body=%s", legacyDecision.code, legacyDecision.raw)
	}
}

func TestAgentOSControlPlaneAndLegacyNoAuthRetainTaskAccess(t *testing.T) {
	defaultTenantTask := &GatewayTask{
		ID: "legacy-task", TenantID: defaultExecutionTenantID,
		RoleID: "role-a", CallerID: "another-caller",
	}
	legacy := ExecutionIdentity{
		TenantID: defaultExecutionTenantID, ServiceID: "legacy-caller",
		Service: true, TenantScoped: false,
	}
	if !executionIdentityCanReadTask(legacy, defaultTenantTask) ||
		!executionIdentityCanCancelTask(legacy, defaultTenantTask) {
		t.Fatal("legacy no-auth identity lost its task access")
	}
	controlPlane := ExecutionIdentity{
		TenantID: defaultExecutionTenantID, ServiceID: "control",
		ControlPlane: true, AllowedRoles: []string{"different-role"},
	}
	tenantTask := &GatewayTask{
		ID: "tenant-task", TenantID: "tenant-b",
		RoleID: "role-b", CallerID: "another-caller",
	}
	if !executionIdentityCanReadTask(controlPlane, tenantTask) ||
		!executionIdentityCanCancelTask(controlPlane, tenantTask) ||
		!executionIdentityCanReadApproval(controlPlane, tenantTask) ||
		!executionIdentityCanDecideApproval(controlPlane, tenantTask) {
		t.Fatal("control-plane identity lost its unrestricted task access")
	}
}

func TestAgentOSServiceClientScopeValidation(t *testing.T) {
	config := singleAgentConfigWithoutCollaborationPlan()
	config.Tenants = []TenantConfig{{ID: "tenant-a", Name: "Tenant A"}}
	config.ServiceClients = []AgentServiceClientConfig{{
		ID: "scoped-service", TenantID: "tenant-a",
		Token:   "scope_validation_token_123456789012",
		Enabled: true, RequireUserID: true, AllowedRoles: []string{"Assistant"},
		Scopes: []string{
			agentOSScopeTasksSubmit,
			agentOSScopeTasksReadOwn,
			agentOSScopeTasksReadRole,
			agentOSScopeTasksReadAny,
			agentOSScopeTasksCancelOwn,
			agentOSScopeTasksCancelAny,
			agentOSScopeApprovalsRead,
			agentOSScopeApprovalsDecide,
			agentOSScopeUsersActAs,
		},
	}}
	if err := validateConfig(config); err != nil {
		t.Fatalf("supported scopes were rejected: %v", err)
	}
	config.ServiceClients[0].Scopes = []string{agentOSScopeTasksSubmit}
	if err := validateConfig(config); err == nil ||
		!strings.Contains(err.Error(), agentOSScopeUsersActAs) {
		t.Fatalf("require_user_id without users:act_as was accepted: %v", err)
	}
	config.ServiceClients[0].Scopes = []string{"tasks:become-admin"}
	if err := validateConfig(config); err == nil {
		t.Fatal("unsupported service scope was accepted")
	}
}

func TestAgentOSUsersActAsScopeIsNeverGrantedByDefault(t *testing.T) {
	defaultIdentity := ExecutionIdentity{
		TenantID: "tenant-a", UserID: "service:crm", SubjectID: "service:crm",
		ServiceID: "crm", Service: true, TenantScoped: true,
	}
	if executionIdentityHasAgentOSScope(defaultIdentity, agentOSScopeUsersActAs) {
		t.Fatal("default service scopes unexpectedly allow acting as a user")
	}
	defaultIdentity.Scopes = []string{agentOSScopeUsersActAs}
	if !executionIdentityHasAgentOSScope(defaultIdentity, agentOSScopeUsersActAs) {
		t.Fatal("explicit users:act_as scope was not honored")
	}
}

func TestAgentOSDirectUserSubjectScopeCannotCrossNaturalPeople(t *testing.T) {
	identity := ExecutionIdentity{
		TenantID: "tenant-a", UserID: "alice", SubjectID: "user:1",
		ServiceID: "user:alice", Service: true, TenantScoped: true,
		SubjectScoped: true,
		Scopes: []string{
			agentOSScopeTasksReadAny,
			agentOSScopeTasksCancelAny,
		},
	}
	alice := &GatewayTask{
		ID: "alice-task", TenantID: "tenant-a", SubjectID: "user:1",
		TenantScoped: true, RoleID: "role-a", CallerID: "user:alice",
	}
	bob := &GatewayTask{
		ID: "bob-task", TenantID: "tenant-a", SubjectID: "user:2",
		TenantScoped: true, RoleID: "role-a", CallerID: "user:alice",
	}
	if !executionIdentityCanReadTask(identity, alice) ||
		!executionIdentityCanCancelTask(identity, alice) {
		t.Fatal("direct user lost access to their own subject")
	}
	if executionIdentityCanReadTask(identity, bob) ||
		executionIdentityCanCancelTask(identity, bob) {
		t.Fatal("broad task scopes crossed the direct user's subject boundary")
	}

	gateway, _ := newAgentOSAPIScenarioGateway(
		t,
		&agentOSAPIScenarioAgent{result: "unused"},
	)
	now := time.Now()
	for _, task := range []*GatewayTask{alice, bob} {
		task.Status = "completed"
		task.QueueName = agentOSQueueName
		task.CreatedAt = now
		task.Request = &MessageRequest{
			TaskID: task.ID, TaskType: "chat",
			SessionId: task.ID + "-session",
			TenantID:  task.TenantID, SubjectID: task.SubjectID,
			RoleID: task.RoleID, UserId: task.SubjectID,
		}
		if err := SaveGatewayTaskToDB(gateway.db, task); err != nil {
			t.Fatal(err)
		}
	}
	var records []GatewayTaskRecord
	if err := scopeAgentOSTaskListQuery(
		gateway.db.Model(&GatewayTaskRecord{}),
		identity,
	).Find(&records).Error; err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].TaskID != alice.ID {
		t.Fatalf("subject-scoped list returned %+v", records)
	}
}

func TestAgentOSDelegatedUserHeaderScopesEveryTaskOperation(t *testing.T) {
	g, _ := newAgentOSAPIScenarioGateway(t, &agentOSAPIScenarioAgent{result: "unused"})
	configureAgentOSScopeClients(g, AgentServiceClientConfig{
		ID: "multi-user-service", TenantID: "tenant-a",
		Token: scopeTestTokenA, Enabled: true, RequireUserID: true,
		AllowedRoles: []string{"role-a"},
		Scopes: []string{
			agentOSScopeTasksSubmit,
			agentOSScopeTasksReadOwn,
			agentOSScopeTasksCancelOwn,
			agentOSScopeUsersActAs,
		},
	})
	seedAgentOSScopeTask(t, g, "alice-task", "tenant-a", "role-a", "multi-user-service", "pending")
	seedAgentOSScopeTask(t, g, "bob-task", "tenant-a", "role-a", "multi-user-service", "pending")
	for taskID, subjectID := range map[string]string{
		"alice-task": "alice",
		"bob-task":   "bob",
	} {
		if err := g.db.Model(&GatewayTaskRecord{}).
			Where("task_id = ?", taskID).
			Updates(map[string]interface{}{
				"subject_id":    subjectID,
				"user_id":       subjectID,
				"tenant_scoped": true,
			}).Error; err != nil {
			t.Fatal(err)
		}
	}

	missing := performAgentOSRequest(
		g, http.MethodGet, "/api/v1/agent/tasks/alice-task", nil,
		scopeTestHeaders(scopeTestTokenA),
	)
	if missing.code != http.StatusBadRequest ||
		missing.body["code"] != "user_id_required" {
		t.Fatalf("missing delegated user code=%d body=%s", missing.code, missing.raw)
	}

	aliceHeaders := scopeTestHeaders(scopeTestTokenA)
	aliceHeaders[agentOSUserIDHeader] = "alice"
	bobHeaders := scopeTestHeaders(scopeTestTokenA)
	bobHeaders[agentOSUserIDHeader] = "bob"

	own := performAgentOSRequest(
		g, http.MethodGet, "/api/v1/agent/tasks/alice-task", nil, aliceHeaders,
	)
	if own.code != http.StatusOK || own.body["subject_id"] != "alice" {
		t.Fatalf("delegated owner read code=%d body=%s", own.code, own.raw)
	}
	crossRead := performAgentOSRequest(
		g, http.MethodGet, "/api/v1/agent/tasks/alice-task", nil, bobHeaders,
	)
	if crossRead.code != http.StatusNotFound {
		t.Fatalf("cross-user task read code=%d body=%s", crossRead.code, crossRead.raw)
	}
	crossEvents := performAgentOSRequest(
		g, http.MethodGet, "/api/v1/agent/tasks/alice-task/events", nil, bobHeaders,
	)
	if crossEvents.code != http.StatusNotFound {
		t.Fatalf("cross-user task events code=%d body=%s", crossEvents.code, crossEvents.raw)
	}
	crossCancel := performAgentOSRequest(
		g, http.MethodPost, "/api/v1/agent/tasks/alice-task/cancel", nil, bobHeaders,
	)
	if crossCancel.code != http.StatusNotFound {
		t.Fatalf("cross-user task cancellation code=%d body=%s", crossCancel.code, crossCancel.raw)
	}

	listed := performAgentOSRequest(
		g, http.MethodGet, "/api/v1/agent/tasks", nil, aliceHeaders,
	)
	if got := fmt.Sprint(listedAgentOSTaskIDs(t, listed)); got != "[alice-task]" {
		t.Fatalf("delegated user list=%s, want only alice task", got)
	}
	cancelOwn := performAgentOSRequest(
		g, http.MethodPost, "/api/v1/agent/tasks/alice-task/cancel", nil, aliceHeaders,
	)
	if cancelOwn.code != http.StatusOK || cancelOwn.body["status"] != "stopped" {
		t.Fatalf("delegated owner cancellation code=%d body=%s", cancelOwn.code, cancelOwn.raw)
	}
}

func TestAgentOSScopeAuthenticationSealsConfiguredGrants(t *testing.T) {
	g, _ := newAgentOSAPIScenarioGateway(t, &agentOSAPIScenarioAgent{result: "unused"})
	configureAgentOSScopeClients(g, AgentServiceClientConfig{
		ID: "scoped", TenantID: "tenant-a",
		Token: scopeTestOperatorToken, Enabled: true, CanApprove: true,
		AllowedRoles: []string{"role-a"},
		Scopes:       []string{agentOSScopeTasksReadRole},
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/agent/tasks", nil)
	req.Header.Set("Authorization", "Bearer "+scopeTestOperatorToken)
	ctx, err := g.authenticateExecutionRequest(req, "agent_os")
	if err != nil {
		t.Fatal(err)
	}
	identity, ok := executionIdentityFromContext(ctx)
	if !ok {
		t.Fatal("authenticated identity was not sealed into context")
	}
	for _, scope := range []string{
		agentOSScopeTasksReadRole,
		agentOSScopeApprovalsRead,
		agentOSScopeApprovalsDecide,
	} {
		if !executionIdentityHasAgentOSScope(identity, scope) {
			t.Fatalf("authenticated identity is missing scope %q: %+v", scope, identity)
		}
	}
	if executionIdentityHasAgentOSScope(identity, agentOSScopeTasksSubmit) {
		t.Fatalf("explicit scope set was widened with default submission: %+v", identity)
	}
}
