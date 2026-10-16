package network

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// APICallTool lets DE agents call external CRM/ERP APIs with pre-configured tokens.
type APICallTool struct {
	tokens map[string]string // token_name -> token_value
	client *http.Client
}

// NewAPICallTool creates an API caller with the given tokens.
func NewAPICallTool(tokens map[string]string) *APICallTool {
	return &APICallTool{
		tokens: tokens,
		client: &http.Client{Timeout: 30 * time.Second},
	}
}

func (t *APICallTool) Name() string { return "api_call" }

func (t *APICallTool) Description() string {
	return "Call an external API (CRM, ERP, etc) with a pre-configured auth token. " +
		"Use this to look up customer info, check order status, create records, etc."
}

func (t *APICallTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"url": map[string]interface{}{
				"type": "string", "description": "Full API URL to call",
			},
			"method": map[string]interface{}{
				"type": "string", "description": "HTTP method (GET, POST, PUT, DELETE)",
				"enum": []string{"GET", "POST", "PUT", "DELETE", "PATCH"},
			},
			"token_name": map[string]interface{}{
				"type": "string", "description": "Name of the pre-configured API token to use for auth",
			},
			"body": map[string]interface{}{
				"type": "string", "description": "JSON request body (for POST/PUT/PATCH)",
			},
		},
		"required": []string{"url", "method"},
	}
}

func (t *APICallTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	url, _ := params["url"].(string)
	method, _ := params["method"].(string)
	tokenName, _ := params["token_name"].(string)
	bodyStr, _ := params["body"].(string)

	if url == "" {
		return "", fmt.Errorf("api_call: url required")
	}
	if method == "" {
		method = "GET"
	}

	var reqBody io.Reader
	if bodyStr != "" {
		reqBody = bytes.NewReader([]byte(bodyStr))
	}

	req, err := http.NewRequestWithContext(ctx, method, url, reqBody)
	if err != nil {
		return "", fmt.Errorf("api_call: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	// Apply token
	if tokenName != "" {
		token, ok := t.tokens[tokenName]
		if !ok {
			return "", fmt.Errorf("api_call: token '%s' not configured", tokenName)
		}
		if strings.HasPrefix(token, "Bearer ") {
			req.Header.Set("Authorization", token)
		} else {
			req.Header.Set("Authorization", "Bearer "+token)
		}
	}

	resp, err := t.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("api_call: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	// Truncate long responses
	result := string(body)
	if len(result) > 4000 {
		result = result[:4000] + "...(truncated)"
	}

	// Try to pretty-print JSON
	var parsed interface{}
	if json.Unmarshal(body, &parsed) == nil {
		pretty, _ := json.MarshalIndent(parsed, "", "  ")
		if len(pretty) < 4000 {
			result = string(pretty)
		}
	}

	return fmt.Sprintf("HTTP %d\n%s", resp.StatusCode, result), nil
}
