package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	gatewayhttp "mac/internal/gateway/httpx"
)

func TestSensitiveDebugAccessRequiresLoopbackSameOriginOrAPIKey(t *testing.T) {
	gateway := &GatewayService{config: &Config{}}

	loopback := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8080/api/monitor/cache/prompt?id=1", nil)
	loopback.RemoteAddr = "127.0.0.1:50000"
	loopback.Header.Set("Origin", "http://127.0.0.1:8080")
	if !gateway.allowSensitiveDebugRequest(loopback) {
		t.Fatal("same-origin loopback request should be allowed when no API key is configured")
	}

	crossOrigin := loopback.Clone(loopback.Context())
	crossOrigin.Header.Set("Origin", "https://example.invalid")
	if gateway.allowSensitiveDebugRequest(crossOrigin) {
		t.Fatal("cross-origin browser request must not read prompt dumps")
	}

	remote := loopback.Clone(loopback.Context())
	remote.RemoteAddr = "192.0.2.10:50000"
	if gateway.allowSensitiveDebugRequest(remote) {
		t.Fatal("remote request must not read prompt dumps without an API key")
	}

	gateway.config.Gateway.APIKey = "synthetic-gateway-key"
	withKey := remote.Clone(remote.Context())
	withKey.Header.Set("X-API-Key", "synthetic-gateway-key")
	if !gateway.allowSensitiveDebugRequest(withKey) {
		t.Fatal("configured API key should authorize the sensitive debug endpoint")
	}
}

func TestConfigAPIValueRedactsSecretsWithoutMutatingSource(t *testing.T) {
	secret := "synthetic-config-secret-123456"
	source := map[string]interface{}{
		"model_config": map[string]interface{}{
			"primary": map[string]interface{}{"api_key": secret, "model_name": "synthetic-model"},
		},
		"gateway": map[string]interface{}{"auth_token": secret, "listen_addr": "127.0.0.1:8080"},
		"service_clients": []interface{}{map[string]interface{}{
			"id": "crm-service", "tenant_id": "tenant-a", "token": secret,
		}},
		"channels": []interface{}{map[string]interface{}{"app_secret": secret, "enabled": false}},
	}
	redacted := redactedConfigAPIValue(source)
	data, err := json.Marshal(redacted)
	if err != nil {
		t.Fatalf("marshal redacted config: %v", err)
	}
	text := string(data)
	if strings.Contains(text, secret) || !strings.Contains(text, "[REDACTED]") {
		t.Fatalf("config API value leaked a secret: %s", text)
	}
	if !strings.Contains(text, "synthetic-model") || !strings.Contains(text, "127.0.0.1:8080") {
		t.Fatalf("config API value lost non-sensitive settings: %s", text)
	}
	if source["gateway"].(map[string]interface{})["auth_token"] != secret {
		t.Fatal("redaction mutated the live config source")
	}
}

func TestGatewayListenAddressDefaultsToLoopbackAndHonorsExplicitHost(t *testing.T) {
	for _, tc := range []struct {
		input    string
		wantHost string
		wantPort int
	}{
		{"", "127.0.0.1", 8080},
		{":8090", "127.0.0.1", 8090},
		{"127.0.0.1:18090", "127.0.0.1", 18090},
		{"0.0.0.0:9000", "0.0.0.0", 9000},
	} {
		host, port := gatewayListenHostAndPort(tc.input, 8080)
		if host != tc.wantHost || port != tc.wantPort {
			t.Errorf("gatewayListenHostAndPort(%q) = (%q,%d), want (%q,%d)", tc.input, host, port, tc.wantHost, tc.wantPort)
		}
	}
}

func TestGatewayWebUIURLMatchesActualListenerHost(t *testing.T) {
	for _, tc := range []struct {
		host string
		port int
		want string
	}{
		{"127.0.0.1", 18090, "http://127.0.0.1:18090/web"},
		{"localhost", 8080, "http://localhost:8080/web"},
		{"192.0.2.15", 9000, "http://192.0.2.15:9000/web"},
		{"::1", 8081, "http://[::1]:8081/web"},
	} {
		if got := gatewayWebUIURL(tc.host, tc.port); got != tc.want {
			t.Errorf("gatewayWebUIURL(%q,%d) = %q, want %q", tc.host, tc.port, got, tc.want)
		}
	}
	if got := gatewayWebUIURL("0.0.0.0", 8080); strings.Contains(got, "0.0.0.0") {
		t.Fatalf("wildcard listener must advertise a reachable host, got %q", got)
	}
}

func TestHeadlessGatewayDoesNotAutoOpenBrowser(t *testing.T) {
	if shouldAutoOpenGatewayBrowser(&Config{Headless: true}) {
		t.Fatal("headless gateway must not auto-open a browser")
	}
	if !shouldAutoOpenGatewayBrowser(&Config{Headless: false}) {
		t.Fatal("interactive gateway should auto-open its Web UI")
	}
}

func TestBrowserOriginMustMatchGatewayHost(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8080/ws", nil)
	if !gatewayhttp.RequestHasNoOriginOrSameHost(req) {
		t.Fatal("non-browser request without Origin should be allowed")
	}
	req.Header.Set("Origin", "http://127.0.0.1:8080")
	if !gatewayhttp.RequestHasNoOriginOrSameHost(req) {
		t.Fatal("same-origin browser request should be allowed")
	}
	req.Header.Set("Origin", "https://example.invalid")
	if gatewayhttp.RequestHasNoOriginOrSameHost(req) {
		t.Fatal("cross-origin browser request should be rejected")
	}
}

func TestMonitorCachePromptRejectsTraversalID(t *testing.T) {
	gateway := &GatewayService{config: &Config{}}
	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8080/api/monitor/cache/prompt?id=..%2Fconfig", nil)
	req.RemoteAddr = "127.0.0.1:50000"
	req.Header.Set("Origin", "http://127.0.0.1:8080")
	response := httptest.NewRecorder()

	gateway.handleMonitorCachePrompt(response, req)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("traversal id status = %d, want %d", response.Code, http.StatusBadRequest)
	}
}
