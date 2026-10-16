package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"mac/internal/configvalue"
)

func TestConfigHealthReturnsPathAndActionableValidation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "openkitty-config.yaml")
	config := &Config{}
	if err := SaveConfig(path, config); err != nil {
		t.Fatal(err)
	}
	gateway := &GatewayService{config: config, configpath: path}
	recorder := httptest.NewRecorder()
	gateway.handleConfigHealth(recorder, httptest.NewRequest(http.MethodGet, "/api/config/health", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var payload struct {
		OK     bool   `json:"ok"`
		Error  string `json:"error"`
		Path   string `json:"path"`
		Status struct {
			Errors []string `json:"errors"`
		} `json:"config_status"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.OK {
		t.Fatal("empty config must report its missing model configuration")
	}
	if payload.Path != configvalue.ResolvePath(path) {
		t.Fatalf("path=%q want=%q", payload.Path, configvalue.ResolvePath(path))
	}
	if !strings.Contains(payload.Error, "模型") || len(payload.Status.Errors) == 0 {
		t.Fatalf("validation is not actionable: %#v", payload)
	}
}

func TestConfigHealthRejectsMutationMethods(t *testing.T) {
	gateway := &GatewayService{}
	recorder := httptest.NewRecorder()
	gateway.handleConfigHealth(recorder, httptest.NewRequest(http.MethodPost, "/api/config/health", nil))
	if recorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status=%d want=%d", recorder.Code, http.StatusMethodNotAllowed)
	}
}
