package modelruntime

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestReadChatModelCatalogUsesSafeSummariesAndNoRedirects(t *testing.T) {
	for _, mode := range []string{"valid", "redirect", "unauthorized", "invalid"} {
		t.Run(mode, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/macs/v1/models" || r.Header.Get("Authorization") != "Bearer fixture-key" {
					t.Error("incorrect catalog request")
				}
				switch mode {
				case "redirect":
					w.Header().Set("Location", "/unexpected")
					w.WriteHeader(302)
				case "unauthorized":
					w.WriteHeader(401)
				case "invalid":
					_, _ = w.Write([]byte(`{"data":[{"name":"missing id"}]}`))
				default:
					_, _ = w.Write([]byte(`{"data":[{"id":"platform:1","name":"MiniMax","api_key":"should-be-omitted","billing":{"price":2.5,"unit":"per_success"}}]}`))
				}
			}))
			defer server.Close()
			models, code := ReadChatModelCatalog(context.Background(), server.Client(), ChatEndpoint{BaseURL: server.URL + "/api/macs/v1", APIKey: "fixture-key"})
			if mode == "valid" {
				if code != "" || len(models) != 1 || models[0].Billing.Price == nil || *models[0].Billing.Price != 2.5 || models[0].ID != "platform:1" {
					t.Fatalf("models=%+v code=%s", models, code)
				}
			} else if code == "" {
				t.Fatal("invalid catalog accepted")
			}
		})
	}
}

func TestPlatformHealthCheckReadsCatalogWithoutPaidGeneration(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || !strings.HasSuffix(r.URL.Path, "/models") {
			t.Error("health check attempted generation")
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"platform:1"}]}`))
	}))
	defer server.Close()
	if err := ProbeChatCompletion(context.Background(), server.Client(), ChatEndpoint{BaseURL: server.URL + "/api/macs/v1", ModelName: "platform:default"}); err != nil {
		t.Fatal(err)
	}
}

func TestPlatformCallIdentityKeepsRetriesDistinctFromNewWork(t *testing.T) {
	endpoint := "https://platform.example/api/macs/v1/chat/completions"
	ctx := WithPlatformCallIdentity(context.Background(), endpoint)
	identity := func(ctx context.Context, payload string) string {
		req, _ := http.NewRequestWithContext(ctx, "POST", endpoint, strings.NewReader(payload))
		SetPlatformCallIdentity(req, []byte(payload))
		return req.Header.Get("Idempotency-Key")
	}
	first := identity(ctx, "work")
	if first == "" || first != identity(ctx, "work") || first == identity(ctx, "repair") || first == identity(WithPlatformCallIdentity(context.Background(), endpoint), "work") {
		t.Fatal("call identities fail retry/round separation")
	}
	req, _ := http.NewRequest("POST", "https://custom.example/v1/chat/completions", nil)
	SetPlatformCallIdentity(req, []byte("work"))
	if req.Header.Get("Idempotency-Key") != "" {
		t.Fatal("modified custom provider request")
	}
}
