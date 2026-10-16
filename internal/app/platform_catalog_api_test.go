package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestPlatformCatalogLocalBoundarySafeMetadataAndCapabilities(t *testing.T) {
	t.Setenv("OPENKITTY_ACCOUNT_SERVICE_URL", "")
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != http.MethodGet || r.URL.Path != "/api/macs/v1/catalog" || r.Header.Get("Authorization") != "" {
			t.Error("public discovery forwarded secrets or called generation")
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"platform:1","name":"Text","capability":"text","available":true,"api_key":"upstream-secret"},{"id":"platform:2","name":"Image","capability":"image","available":true,"direct_generation":true,"billing":{"price":3.5,"unit":"per_success"}},{"id":"personal:3","name":"Private","capability":"text"},{"id":"platform:4","name":"Video","capability":"video"},{"id":"platform:5","name":"Music","capability":"music","available":true,"direct_generation":true,"billing":{"price":0,"unit":"free"}}]}`))
	}))
	defer server.Close()
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	config := &Config{ModelConfig: map[string]ModelConfig{"platform": {Type: "openai", ModelName: "platform:default", BaseURL: "https://unrelated-model.example/api/macs/v1", APIKey: "private-account-key"}}}
	config.Gateway.AccountServiceURL = server.URL
	data, _ := yaml.Marshal(config)
	if err := os.WriteFile(configPath, data, 0600); err != nil {
		t.Fatal(err)
	}
	g := &GatewayService{configpath: configPath}
	for _, test := range []struct {
		method, query, remote, origin string
		status                        int
	}{
		{http.MethodGet, "", "127.0.0.1:1000", "", 200},
		{http.MethodGet, "", "192.0.2.1:1000", "", 403},
		{http.MethodGet, "", "127.0.0.1:1000", "https://untrusted.example", 403},
		{http.MethodPost, "", "127.0.0.1:1000", "", 405},
		{http.MethodGet, "?url=http://localhost/private", "127.0.0.1:1000", "", 400},
	} {
		request := httptest.NewRequest(test.method, "http://localhost:8082/api/platform/models"+test.query, nil)
		request.RemoteAddr = test.remote
		request.Header.Set("Origin", test.origin)
		recorder := httptest.NewRecorder()
		g.handlePlatformModelCatalog(recorder, request)
		if recorder.Code != test.status {
			t.Fatalf("status=%d want=%d body=%s", recorder.Code, test.status, recorder.Body.String())
		}
		if test.status == 200 {
			var reply struct {
				Models []struct {
					Capability string `json:"capability"`
				} `json:"models"`
			}
			if json.Unmarshal(recorder.Body.Bytes(), &reply) != nil || len(reply.Models) != 4 || reply.Models[1].Capability != "image" || reply.Models[2].Capability != "video" || reply.Models[3].Capability != "music" {
				t.Fatalf("unexpected catalog: %s", recorder.Body.String())
			}
			for _, secret := range []string{"upstream-secret", "private-account-key", "personal:", server.URL} {
				if strings.Contains(recorder.Body.String(), secret) {
					t.Fatalf("unsafe catalog: %s", recorder.Body.String())
				}
			}
		}
	}
	if calls != 1 {
		t.Fatalf("rejected discovery fetched upstream: %d", calls)
	}
}
