package modelruntime

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// ChatCatalogModel deliberately omits provider credentials and internal URLs.
type ChatCatalogModel struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	Model            string `json:"model,omitempty"`
	Capability       string `json:"capability,omitempty"`
	Source           string `json:"source,omitempty"`
	Available        bool   `json:"available,omitempty"`
	DirectGeneration bool   `json:"direct_generation,omitempty"`
	Billing          struct {
		// A missing price is unknown, rather than an explicit zero-point price.
		Price    *float64 `json:"price"`
		Currency string   `json:"currency,omitempty"`
		Unit     string   `json:"unit,omitempty"`
	} `json:"billing"`
}

func ReadChatModelCatalog(ctx context.Context, client *http.Client, endpoint ChatEndpoint) ([]ChatCatalogModel, string) {
	return readModelCatalog(ctx, client, endpoint, "/models")
}

// Public discovery never sends a stored account key to the platform.
func ReadPlatformModelCatalog(ctx context.Context, client *http.Client, baseURL string) ([]ChatCatalogModel, string) {
	return readModelCatalog(ctx, client, ChatEndpoint{BaseURL: baseURL}, "/catalog?type=all")
}

func readModelCatalog(ctx context.Context, client *http.Client, endpoint ChatEndpoint, suffix string) ([]ChatCatalogModel, string) {
	parsed, err := url.Parse(endpoint.BaseURL)
	if err != nil || parsed.Hostname() == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil, "invalid_config"
	}
	if client == nil {
		client = http.DefaultClient
	}
	bounded := *client
	bounded.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(endpoint.BaseURL, "/")+suffix, nil)
	if err != nil {
		return nil, "invalid_config"
	}
	if endpoint.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+endpoint.APIKey)
	}
	resp, err := bounded.Do(req)
	if err != nil {
		return nil, "connection_failed"
	}
	defer resp.Body.Close()
	if resp.StatusCode == 401 || resp.StatusCode == 403 {
		return nil, "unauthorized"
	}
	if resp.StatusCode != 200 {
		return nil, "connection_failed"
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
	var reply struct {
		Data []ChatCatalogModel `json:"data"`
	}
	if err != nil || len(body) > 1<<20 || json.Unmarshal(body, &reply) != nil || reply.Data == nil || len(reply.Data) > 500 {
		return nil, "invalid_response"
	}
	for i := range reply.Data {
		model := &reply.Data[i]
		if strings.TrimSpace(model.ID) == "" {
			return nil, "invalid_response"
		}
		if model.Name == "" {
			model.Name = model.ID
		}
	}
	return reply.Data, ""
}
