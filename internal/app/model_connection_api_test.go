package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestSettingsModelCatalogEnforcesLocalBoundaryAndRedacts(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer catalog-fixture" {
			t.Error("catalog credential missing")
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"platform:1","name":"Fixture","api_key":"catalog-fixture"}]}`))
	}))
	defer server.Close()
	for _, remote := range []string{"127.0.0.1:1000", "192.0.2.1:1000"} {
		body, _ := json.Marshal(map[string]interface{}{"model": map[string]string{"type": "openai", "api_key": "catalog-fixture", "base_url": server.URL}})
		req := httptest.NewRequest(http.MethodPost, "http://localhost:8082/api/config/model-catalog", strings.NewReader(string(body)))
		req.RemoteAddr = remote
		w := httptest.NewRecorder()
		(&GatewayService{}).handleSettingsModelCatalog(w, req)
		want := http.StatusOK
		if remote != "127.0.0.1:1000" {
			want = http.StatusForbidden
		}
		if w.Code != want || strings.Contains(w.Body.String(), "catalog-fixture") {
			t.Fatalf("status=%d response=%s", w.Code, w.Body.String())
		}
	}
}

func TestSettingsModelTestRejectsRemoteAndCrossSiteRequests(t *testing.T) {
	for _, test := range []struct{ remote, host, origin string }{
		{"192.0.2.1:1234", "localhost:8082", ""},
		{"127.0.0.1:1234", "localhost:8082", "https://untrusted.example"},
		{"127.0.0.1:1234", "untrusted.example", ""},
	} {
		req := httptest.NewRequest(http.MethodPost, "http://localhost:8082/api/config/model-test", strings.NewReader(`{}`))
		req.RemoteAddr, req.Host = test.remote, test.host
		req.Header.Set("Origin", test.origin)
		w := httptest.NewRecorder()
		(&GatewayService{}).handleSettingsModelTest(w, req)
		if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "local_only") {
			t.Fatalf("status = %d, response = %s", w.Code, w.Body.String())
		}
	}
}

func TestSettingsModelTestKeepsSavedKeyAtOriginalEndpoint(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Authorization") != "Bearer test-only-key" {
			t.Error("saved key not resolved")
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"OK"}}]}`))
	}))
	defer server.Close()
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	config := &Config{ModelConfig: map[string]ModelConfig{"fixture": {Type: "openai", ModelName: "fixture", BaseURL: server.URL, APIKey: "test-only-key"}}}
	data, err := yaml.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, data, 0600); err != nil {
		t.Fatal(err)
	}
	for _, changed := range []bool{false, true} {
		address := server.URL
		if changed {
			address += "/changed"
		}
		body, _ := json.Marshal(map[string]interface{}{"name": "fixture", "model": map[string]string{"type": "openai", "model_name": "fixture", "base_url": address, "api_key": "[REDACTED]"}})
		req := httptest.NewRequest(http.MethodPost, "http://localhost:8082/api/config/model-test", strings.NewReader(string(body)))
		req.RemoteAddr = "127.0.0.1:1234"
		w := httptest.NewRecorder()
		(&GatewayService{configpath: configPath}).handleSettingsModelTest(w, req)
		want := http.StatusOK
		if changed {
			want = http.StatusBadRequest
		}
		if w.Code != want || strings.Contains(w.Body.String(), "test-only-key") {
			t.Fatalf("status = %d, response = %s", w.Code, w.Body.String())
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("outbound requests = %d", calls.Load())
	}
}

func TestSettingsModelTestValidatesLocalDraft(t *testing.T) {
	for _, body := range []string{`{`, `{"model":{"type":"openai","model_name":"test"}}`, `{"model":{"api_key":"[REDACTED]"}}`} {
		req := httptest.NewRequest(http.MethodPost, "http://localhost:8082/api/config/model-test", strings.NewReader(body))
		req.RemoteAddr = "127.0.0.1:1234"
		w := httptest.NewRecorder()
		(&GatewayService{}).handleSettingsModelTest(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, response = %s", w.Code, w.Body.String())
		}
	}
}
