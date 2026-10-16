package modelruntime

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// TestChatConnection verifies a bounded OpenAI-compatible reply without tools
// or conversation persistence. It never follows redirects carrying a key.
func TestChatConnection(ctx context.Context, client *http.Client, endpoint ChatEndpoint) string {
	parsed, err := url.Parse(endpoint.BaseURL)
	if err != nil || parsed.Hostname() == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || strings.TrimSpace(endpoint.ModelName) == "" {
		return "invalid_config"
	}
	if client == nil {
		client = http.DefaultClient
	}
	boundedClient := *client
	boundedClient.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	payload, _ := json.Marshal(map[string]interface{}{
		"model": endpoint.ModelName, "messages": []map[string]string{{"role": "user", "content": "Reply with OK."}}, "max_tokens": 8,
	})
	ctx = WithPlatformCallIdentity(ctx, endpoint.BaseURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(endpoint.BaseURL, "/")+"/chat/completions", strings.NewReader(string(payload)))
	if err != nil {
		return "invalid_config"
	}
	req.Header.Set("Content-Type", "application/json")
	if err := AuthorizeModelRequest(req, endpoint.APIKey, endpoint.AccountAuth); err != nil {
		if errors.Is(err, ErrPlatformAddressMismatch) {
			return "platform_address_mismatch"
		}
		return "login_required"
	}
	SetPlatformCallIdentity(req, payload)
	resp, err := boundedClient.Do(req)
	if err != nil {
		return "connection_failed"
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		return "unauthorized"
	case http.StatusBadRequest, http.StatusNotFound:
		return "model_unavailable"
	case http.StatusTooManyRequests, http.StatusPaymentRequired:
		return "rate_limited"
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "connection_failed"
	}
	var reply struct {
		Choices []struct {
			Message json.RawMessage `json:"message"`
		} `json:"choices"`
	}
	decoder := json.NewDecoder(io.LimitReader(resp.Body, 64<<10))
	if decoder.Decode(&reply) != nil || len(reply.Choices) == 0 {
		return "invalid_response"
	}
	var extra interface{}
	if !errors.Is(decoder.Decode(&extra), io.EOF) {
		return "invalid_response"
	}
	var message map[string]interface{}
	if json.Unmarshal(reply.Choices[0].Message, &message) != nil || message["role"] != "assistant" {
		return "invalid_response"
	}
	return ""
}
