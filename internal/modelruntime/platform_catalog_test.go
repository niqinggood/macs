package modelruntime

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPublicPlatformCatalogKeepsCapabilitiesAndNeverSendsKeys(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/macs/v1/catalog" || r.URL.Query().Get("type") != "all" || r.Header.Get("Authorization") != "" {
			t.Error("invalid public discovery request")
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"platform:1","name":"Text","capability":"text","source":"platform","available":true,"api_key":"secret"},{"id":"platform:2","name":"Image","capability":"image","source":"platform","available":true,"billing":{"price":3.5}}]}`))
	}))
	defer server.Close()
	models, code := ReadPlatformModelCatalog(context.Background(), server.Client(), server.URL+"/api/macs/v1")
	if code != "" || len(models) != 2 || models[0].Capability != "text" || models[1].Capability != "image" || models[1].Billing.Price == nil || *models[1].Billing.Price != 3.5 || !models[1].Available {
		t.Fatalf("models=%+v code=%s", models, code)
	}
}

func TestPublicPlatformCatalogPreservesUnknownPrices(t *testing.T) {
	for _, test := range []struct {
		name, billing, wantPrice string
	}{
		{"missing", `{"currency":"POINT","unit":"per_success"}`, "null"},
		{"null", `{"price":null,"currency":"POINT","unit":"per_success"}`, "null"},
		{"zero", `{"price":0,"currency":"POINT","unit":"per_success"}`, "0"},
		{"positive", `{"price":2.75,"currency":"POINT","unit":"per_success"}`, "2.75"},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != "/api/macs/v1/catalog" || r.URL.Query().Get("type") != "all" || r.Header.Get("Authorization") != "" {
					t.Error("price discovery must only read the public catalog")
				}
				_, _ = w.Write([]byte(`{"data":[{"id":"platform:1","billing":` + test.billing + `}]}`))
			}))
			defer server.Close()
			models, code := ReadPlatformModelCatalog(context.Background(), server.Client(), server.URL+"/api/macs/v1")
			if code != "" || len(models) != 1 {
				t.Fatalf("models=%+v code=%s", models, code)
			}
			encoded, err := json.Marshal(models)
			if err != nil {
				t.Fatal(err)
			}
			var forwarded []struct {
				Billing struct {
					Price    json.RawMessage `json:"price"`
					Unit     string          `json:"unit"`
					Currency string          `json:"currency"`
				} `json:"billing"`
			}
			if err := json.Unmarshal(encoded, &forwarded); err != nil {
				t.Fatal(err)
			}
			if len(forwarded) != 1 || string(forwarded[0].Billing.Price) != test.wantPrice || forwarded[0].Billing.Unit != "per_success" || forwarded[0].Billing.Currency != "POINT" {
				t.Fatalf("forwarded catalog=%s; want price=%s with unchanged billing unit and currency", encoded, test.wantPrice)
			}
		})
	}
}
