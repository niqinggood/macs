package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAgentOSDataHandlerExposesOnlyServiceDataPlane(t *testing.T) {
	gateway, _ := newAgentOSAPIScenarioGateway(
		t,
		&agentOSAPIScenarioAgent{result: "ok"},
	)
	handler := gateway.agentOSDataHandler()

	roles := httptest.NewRecorder()
	handler.ServeHTTP(
		roles,
		httptest.NewRequest(http.MethodGet, "/api/v1/agent/roles", nil),
	)
	if roles.Code != http.StatusOK {
		t.Fatalf("roles status=%d body=%s", roles.Code, roles.Body.String())
	}
	if roles.Header().Get(agentOSRequestIDHeader) == "" {
		t.Fatal("AgentOS data plane did not return a request id")
	}
	var body map[string]interface{}
	if err := json.Unmarshal(roles.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	items, _ := body["roles"].([]interface{})
	if len(items) != 1 {
		t.Fatalf("roles=%#v", body["roles"])
	}
	role, _ := items[0].(map[string]interface{})
	if role["role_id"] != "Researcher" ||
		role["agent_name"] != "Researcher" ||
		role["enabled"] != true {
		t.Fatalf("role discovery contract is malformed: %#v", role)
	}
	revision, _ := role["role_revision"].(string)
	if len(revision) != 67 || revision[:3] != "r1-" {
		t.Fatalf("role revision is malformed: %q", revision)
	}

	admin := httptest.NewRecorder()
	handler.ServeHTTP(
		admin,
		httptest.NewRequest(http.MethodGet, "/api/config", nil),
	)
	if admin.Code != http.StatusNotFound {
		t.Fatalf("administrative route leaked onto data plane: status=%d", admin.Code)
	}
}

func TestAgentOSDataHandlerUsesErrorContractForUnknownAPIEndpoint(t *testing.T) {
	gateway, _ := newAgentOSAPIScenarioGateway(
		t,
		&agentOSAPIScenarioAgent{result: "unused"},
	)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/agent/unknown", nil)
	recorder := httptest.NewRecorder()
	gateway.agentOSDataHandler().ServeHTTP(recorder, request)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", recorder.Code, recorder.Body.String())
	}
	var response agentOSErrorResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode error contract: %v", err)
	}
	if response.Code != "not_found" || response.RequestID == "" {
		t.Fatalf("unexpected error contract: %+v", response)
	}
	if got := recorder.Header().Get(agentOSRequestIDHeader); got != response.RequestID {
		t.Fatalf("request id header/body mismatch: %q / %q", got, response.RequestID)
	}
}

func TestAgentOSNonLoopbackDataPlaneRequiresAuthentication(t *testing.T) {
	tests := []struct {
		name         string
		mainAddress  string
		agentAddress string
		authEnabled  bool
		apiKey       string
		wantError    bool
	}{
		{
			name:        "dedicated wildcard without auth",
			mainAddress: "127.0.0.1:8080", agentAddress: ":8081",
			wantError: true,
		},
		{
			name:        "dedicated public with auth",
			mainAddress: "127.0.0.1:8080", agentAddress: "0.0.0.0:8081",
			authEnabled: true,
		},
		{
			name:        "loopback dedicated without auth",
			mainAddress: "127.0.0.1:8080", agentAddress: "127.0.0.1:8081",
		},
		{
			name:        "public shared listener without auth",
			mainAddress: "0.0.0.0:8080",
			wantError:   true,
		},
		{
			name:        "public shared listener with control key",
			mainAddress: "0.0.0.0:8080", apiKey: "long-control-plane-key",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := singleAgentConfigWithoutCollaborationPlan()
			config.Gateway.Enabled = true
			config.Gateway.ListenAddr = test.mainAddress
			config.Gateway.AgentOSListenAddr = test.agentAddress
			config.Gateway.AuthEnabled = test.authEnabled
			config.Gateway.APIKey = test.apiKey
			err := validateConfig(config)
			if test.wantError && err == nil {
				t.Fatal("non-loopback AgentOS data plane was accepted without authentication")
			}
			if !test.wantError && err != nil {
				t.Fatalf("valid AgentOS listener configuration was rejected: %v", err)
			}
		})
	}
}
