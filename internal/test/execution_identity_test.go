package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestBoundedIdentityValuePreservesUTF8AndRemovesControls(t *testing.T) {
	value := " \x00" + strings.Repeat("租", 64) + "\x7f "
	bounded := boundedIdentityValue(value, 64)
	if !utf8.ValidString(bounded) {
		t.Fatalf("bounded identity is invalid UTF-8: %q", bounded)
	}
	if len(bounded) > 64 {
		t.Fatalf("bounded identity uses %d bytes, want at most 64", len(bounded))
	}
	if strings.ContainsAny(bounded, "\x00\x7f") {
		t.Fatalf("bounded identity retained control characters: %q", bounded)
	}
}

func TestNormalizeExecutionIdentityDefaultsSubjectToUser(t *testing.T) {
	identity := normalizeExecutionIdentity(ExecutionIdentity{
		TenantID:  "tenant-a",
		UserID:    "service:crm",
		ServiceID: "crm",
	})
	if identity.SubjectID != identity.UserID {
		t.Fatalf("subject=%q, want user fallback %q", identity.SubjectID, identity.UserID)
	}
}

func TestAuthenticateExecutionRequestSeparatesServiceActorAndDefaultSubject(t *testing.T) {
	const token = "identity_service_token_123456789"
	gateway := &GatewayService{config: &Config{
		ServiceClients: []AgentServiceClientConfig{{
			ID: "crm-service", TenantID: "tenant-a",
			Token: token, Enabled: true, RequireUserID: true,
			Scopes: []string{agentOSScopeTasksSubmit, agentOSScopeUsersActAs},
		}},
	}}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/agent/tasks", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	ctx, err := gateway.authenticateExecutionRequest(request, "agent_os")
	if err != nil {
		t.Fatal(err)
	}
	identity, ok := executionIdentityFromContext(ctx)
	if !ok {
		t.Fatal("authenticated identity missing from context")
	}
	if identity.ServiceID != "crm-service" ||
		identity.UserID != "service:crm-service" ||
		identity.SubjectID != "service:crm-service" ||
		!identity.RequireUserID {
		t.Fatalf("service actor/default subject mismatch: %+v", identity)
	}
}

func TestAuthenticateExecutionRequestUsesStableTenantSessionSubject(t *testing.T) {
	const token = "tenant-session-subject-test"
	tenantSessionsMu.Lock()
	tenantSessions[token] = &tenantSession{
		Token: token, TenantID: "tenant-a", Username: "alice",
		SubjectID: "usr_01_stable_alice", Expires: time.Now().Add(time.Hour),
	}
	tenantSessionsMu.Unlock()
	t.Cleanup(func() {
		tenantSessionsMu.Lock()
		delete(tenantSessions, token)
		tenantSessionsMu.Unlock()
	})

	request := httptest.NewRequest(http.MethodPost, "/api/v1/agent/tasks", nil)
	request.Header.Set("X-Auth-Token", token)
	ctx, err := (&GatewayService{}).authenticateExecutionRequest(request, "agent_os")
	if err != nil {
		t.Fatal(err)
	}
	identity, ok := executionIdentityFromContext(ctx)
	if !ok {
		t.Fatal("tenant identity missing from context")
	}
	if identity.UserID != "alice" ||
		identity.SubjectID != "usr_01_stable_alice" ||
		identity.ServiceID != "user:alice" ||
		!identity.SubjectScoped {
		t.Fatalf("tenant session identity mismatch: %+v", identity)
	}

	fallback := withExecutionIdentity(context.Background(), ExecutionIdentity{UserID: "legacy-user"})
	fallbackIdentity, ok := executionIdentityFromContext(fallback)
	if !ok || fallbackIdentity.SubjectID != "legacy-user" {
		t.Fatalf("legacy subject fallback mismatch: %+v ok=%v", fallbackIdentity, ok)
	}
}
