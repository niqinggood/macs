package network

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAPITestToolRequestAssertionsAndStableHeaders(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("X-Zeta", "last")
		writer.Header().Set("X-Alpha", "first")
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusCreated)
		_, _ = writer.Write([]byte(`{"data":{"id":42},"ok":true}`))
	}))
	defer server.Close()

	report, err := NewAPITestTool(t.TempDir()).Execute(context.Background(), map[string]interface{}{
		"action":        "request",
		"url":           server.URL,
		"method":        "post",
		"assert_status": 201,
		"assert_body":   `"ok":true`,
		"assert_json":   `{"data.id":42,"ok":true}`,
		"assert_header": "content-type: application/json",
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	for _, expected := range []string{"All assertions passed", "data.id = 42", "ok = true"} {
		if !strings.Contains(report, expected) {
			t.Fatalf("report does not contain %q:\n%s", expected, report)
		}
	}
	if strings.Index(report, "| x-alpha |") > strings.Index(report, "| x-zeta |") {
		t.Fatalf("response headers are not rendered in stable order:\n%s", report)
	}
}

func TestAPITestToolScenarioResolvesFileAgainstWorkspaceWithoutURLParameter(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	workspace := t.TempDir()
	scenario := fmt.Sprintf(`{
  "base_url": %q,
  "steps": [
    {"name":"first","method":"GET","path":"/one","assert":{"status":200,"body_contains":"ok"}},
    {"name":"second","method":"GET","path":"/two","assert":{"status":200,"body_contains":"true"}}
  ]
}`, server.URL)
	if err := os.WriteFile(filepath.Join(workspace, "scenario.json"), []byte(scenario), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	report, err := NewAPITestTool(workspace).Execute(context.Background(), map[string]interface{}{
		"action":        "scenario",
		"scenario_file": "scenario.json",
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !strings.Contains(report, "2/2 steps passed") {
		t.Fatalf("scenario did not pass:\n%s", report)
	}
}

func TestAPITestToolRequestHonorsCanceledContext(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		<-request.Context().Done()
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := NewAPITestTool(t.TempDir()).Execute(ctx, map[string]interface{}{
		"action": "request",
		"url":    server.URL,
	})
	if err == nil || !strings.Contains(err.Error(), "context canceled") {
		t.Fatalf("Execute() error = %v, want context canceled", err)
	}
}

func TestAPITestToolSchemaDoesNotRequireURLForScenario(t *testing.T) {
	required, _ := NewAPITestTool(t.TempDir()).Parameters()["required"].([]string)
	if len(required) != 1 || required[0] != "action" {
		t.Fatalf("required schema fields = %#v, want action only", required)
	}
}
