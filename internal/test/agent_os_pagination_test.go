package app

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"testing"
	"time"
)

type agentOSTaskListTestPage struct {
	Tasks []struct {
		TaskID string `json:"task_id"`
	} `json:"tasks"`
	Count      int    `json:"count"`
	NextCursor string `json:"next_cursor"`
	Code       string `json:"code"`
}

func createAgentOSTaskListRecord(
	t *testing.T,
	gateway *GatewayService,
	taskID, tenantID, roleID, callerID string,
	createdAt time.Time,
) GatewayTaskRecord {
	t.Helper()
	record := GatewayTaskRecord{
		TaskID: taskID, TenantID: tenantID, RoleID: roleID,
		SessionID: "session-" + taskID, TaskType: "chat",
		Status: "completed", AgentName: roleID, Channel: "api",
		CallerID: callerID, QueueName: agentOSQueueName,
		CreatedAt: createdAt, UpdatedAt: createdAt,
	}
	if err := gateway.db.Create(&record).Error; err != nil {
		t.Fatalf("create task %s: %v", taskID, err)
	}
	return record
}

func requestAgentOSTaskListPage(
	t *testing.T,
	gateway *GatewayService,
	identity ExecutionIdentity,
	query url.Values,
) (int, agentOSTaskListTestPage) {
	t.Helper()
	target := "/api/v1/agent/tasks"
	if encoded := query.Encode(); encoded != "" {
		target += "?" + encoded
	}
	req := httptest.NewRequest(http.MethodGet, target, nil)
	req = req.WithContext(withExecutionIdentity(req.Context(), identity))
	recorder := httptest.NewRecorder()
	gateway.listAgentOSTasks(recorder, req)

	var page agentOSTaskListTestPage
	if err := json.Unmarshal(recorder.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode list response (%d): %v; body=%s", recorder.Code, err, recorder.Body.String())
	}
	return recorder.Code, page
}

func agentOSTaskIDsFromPage(page agentOSTaskListTestPage) []string {
	ids := make([]string, 0, len(page.Tasks))
	for _, task := range page.Tasks {
		ids = append(ids, task.TaskID)
	}
	return ids
}

func TestAgentOSTaskListCursorIsStableAcrossEqualTimestampsAndNewInserts(t *testing.T) {
	db := openAgentOSScenarioDB(t)
	gateway := &GatewayService{db: db, tasks: make(map[string]*GatewayTask)}
	identity := ExecutionIdentity{
		TenantID: "tenant-a", ServiceID: "service-a", TenantScoped: true,
		AllowedRoles: []string{"role-a"}, Scopes: []string{agentOSScopeTasksReadOwn},
	}
	sameCreatedAt := time.Date(2026, 7, 26, 12, 0, 0, 123456000, time.UTC)
	for _, taskID := range []string{"task-01", "task-02", "task-03"} {
		createAgentOSTaskListRecord(t, gateway, taskID, "tenant-a", "role-a", "service-a", sameCreatedAt)
	}
	createAgentOSTaskListRecord(
		t, gateway, "task-old", "tenant-a", "role-a", "service-a",
		sameCreatedAt.Add(-time.Minute),
	)

	status, first := requestAgentOSTaskListPage(
		t, gateway, identity, url.Values{"limit": {"2"}},
	)
	if status != http.StatusOK {
		t.Fatalf("first page status = %d", status)
	}
	if got, want := agentOSTaskIDsFromPage(first), []string{"task-03", "task-02"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("first page ids = %v, want %v", got, want)
	}
	if first.Count != 2 || first.NextCursor == "" {
		t.Fatalf("first page metadata = count %d cursor %q", first.Count, first.NextCursor)
	}

	// A newer row inserted after page one must not appear on page two and must
	// not push a row from page one onto a later page.
	createAgentOSTaskListRecord(
		t, gateway, "task-new", "tenant-a", "role-a", "service-a",
		sameCreatedAt.Add(time.Minute),
	)
	status, second := requestAgentOSTaskListPage(
		t,
		gateway,
		identity,
		url.Values{"limit": {"2"}, "cursor": {first.NextCursor}},
	)
	if status != http.StatusOK {
		t.Fatalf("second page status = %d", status)
	}
	if got, want := agentOSTaskIDsFromPage(second), []string{"task-01", "task-old"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("second page ids = %v, want %v", got, want)
	}
	if second.Count != 2 || second.NextCursor != "" {
		t.Fatalf("second page metadata = count %d cursor %q", second.Count, second.NextCursor)
	}
}

func TestAgentOSTaskListCursorKeepsAuthorizationAndRoleFiltersInSQL(t *testing.T) {
	db := openAgentOSScenarioDB(t)
	gateway := &GatewayService{db: db, tasks: make(map[string]*GatewayTask)}
	identity := ExecutionIdentity{
		TenantID: "tenant-a", ServiceID: "service-a", TenantScoped: true,
		AllowedRoles: []string{"role-a", "role-a-extra"},
		Scopes:       []string{agentOSScopeTasksReadOwn},
	}
	base := time.Date(2026, 7, 26, 13, 0, 0, 0, time.UTC)
	crossTenant := createAgentOSTaskListRecord(
		t, gateway, "cross-tenant", "tenant-b", "role-a", "service-a", base.Add(7*time.Minute),
	)
	createAgentOSTaskListRecord(
		t, gateway, "other-caller", "tenant-a", "role-a", "service-b", base.Add(6*time.Minute),
	)
	createAgentOSTaskListRecord(
		t, gateway, "own-new", "tenant-a", "role-a", "service-a", base.Add(5*time.Minute),
	)
	createAgentOSTaskListRecord(
		t, gateway, "other-role", "tenant-a", "role-a-extra", "service-a", base.Add(4*time.Minute),
	)
	createAgentOSTaskListRecord(
		t, gateway, "disallowed-role", "tenant-a", "role-b", "service-a", base.Add(3*time.Minute),
	)
	createAgentOSTaskListRecord(
		t, gateway, "own-old", "tenant-a", "role-a", "service-a", base.Add(2*time.Minute),
	)

	status, first := requestAgentOSTaskListPage(
		t,
		gateway,
		identity,
		url.Values{"limit": {"1"}, "role_id": {"role-a"}},
	)
	if status != http.StatusOK || !reflect.DeepEqual(agentOSTaskIDsFromPage(first), []string{"own-new"}) {
		t.Fatalf("filtered first page: status=%d ids=%v", status, agentOSTaskIDsFromPage(first))
	}
	status, second := requestAgentOSTaskListPage(
		t,
		gateway,
		identity,
		url.Values{"limit": {"1"}, "role_id": {"role-a"}, "cursor": {first.NextCursor}},
	)
	if status != http.StatusOK || !reflect.DeepEqual(agentOSTaskIDsFromPage(second), []string{"own-old"}) {
		t.Fatalf("filtered second page: status=%d ids=%v", status, agentOSTaskIDsFromPage(second))
	}

	// Even a syntactically valid cursor copied from another tenant only changes
	// the keyset boundary. Tenant, role, and caller predicates still apply.
	status, scoped := requestAgentOSTaskListPage(
		t,
		gateway,
		identity,
		url.Values{
			"limit":   {"100"},
			"role_id": {"role-a"},
			"cursor":  {encodeAgentOSTaskListCursor(crossTenant)},
		},
	)
	if status != http.StatusOK {
		t.Fatalf("stolen cursor status = %d", status)
	}
	if got, want := agentOSTaskIDsFromPage(scoped), []string{"own-new", "own-old"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("stolen cursor expanded authorization: got %v, want %v", got, want)
	}

	status, disallowed := requestAgentOSTaskListPage(
		t, gateway, identity, url.Values{"role_id": {"role-b"}},
	)
	if status != http.StatusOK || disallowed.Count != 0 {
		t.Fatalf("role filter expanded allowed roles: status=%d ids=%v", status, agentOSTaskIDsFromPage(disallowed))
	}
	status, otherCaller := requestAgentOSTaskListPage(
		t, gateway, identity, url.Values{"caller_id": {"service-b"}},
	)
	if status != http.StatusOK || otherCaller.Count != 0 {
		t.Fatalf("caller filter expanded own scope: status=%d ids=%v", status, agentOSTaskIDsFromPage(otherCaller))
	}
}

func TestAgentOSTaskListRejectsInvalidLimitAndCursor(t *testing.T) {
	db := openAgentOSScenarioDB(t)
	gateway := &GatewayService{db: db, tasks: make(map[string]*GatewayTask)}
	identity := ExecutionIdentity{ControlPlane: true}
	invalidCursorVersion := base64.RawURLEncoding.EncodeToString(
		[]byte(`{"v":2,"created_at":1785067200000000000,"task_id":"task-1"}`),
	)
	tests := []struct {
		name  string
		query url.Values
		code  string
	}{
		{name: "zero limit", query: url.Values{"limit": {"0"}}, code: "invalid_limit"},
		{name: "limit above maximum", query: url.Values{"limit": {"101"}}, code: "invalid_limit"},
		{name: "non numeric limit", query: url.Values{"limit": {"many"}}, code: "invalid_limit"},
		{name: "multiple limits", query: url.Values{"limit": {"1", "2"}}, code: "invalid_limit"},
		{name: "empty cursor", query: url.Values{"cursor": {""}}, code: "invalid_cursor"},
		{name: "multiple cursors", query: url.Values{"cursor": {"first", "second"}}, code: "invalid_cursor"},
		{name: "malformed cursor", query: url.Values{"cursor": {"not-base64!"}}, code: "invalid_cursor"},
		{name: "unsupported cursor version", query: url.Values{"cursor": {invalidCursorVersion}}, code: "invalid_cursor"},
		{name: "unknown status", query: url.Values{"status": {"queued"}}, code: "invalid_status"},
		{name: "multiple statuses", query: url.Values{"status": {"pending", "running"}}, code: "invalid_status"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			status, page := requestAgentOSTaskListPage(t, gateway, identity, test.query)
			if status != http.StatusBadRequest || page.Code != test.code {
				t.Fatalf("status/code = %d/%q, want 400/%q", status, page.Code, test.code)
			}
		})
	}
}

func TestParseAgentOSTaskListLimitDefaultsAndBounds(t *testing.T) {
	tests := []struct {
		target string
		want   int
		ok     bool
	}{
		{target: "/api/v1/agent/tasks", want: 50, ok: true},
		{target: "/api/v1/agent/tasks?limit=1", want: 1, ok: true},
		{target: "/api/v1/agent/tasks?limit=100", want: 100, ok: true},
		{target: "/api/v1/agent/tasks?limit=", ok: false},
	}
	for i, test := range tests {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, test.target, nil)
			got, err := parseAgentOSTaskListLimit(req)
			if (err == nil) != test.ok || test.ok && got != test.want {
				t.Fatalf("parse limit = %d, %v; want %d, ok=%v", got, err, test.want, test.ok)
			}
		})
	}
}
