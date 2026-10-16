package macs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const maxJSONResponseBytes = 16 << 20

// Client calls a MACS AgentOS service.
type Client struct {
	baseURL     *url.URL
	httpClient  *http.Client
	bearerToken string
	apiKey      string
	actAsUserID string
	userAgent   string
}

// Option configures a Client.
type Option func(*Client) error

// WithBearerToken configures service Bearer authentication.
func WithBearerToken(token string) Option {
	return func(client *Client) error {
		client.bearerToken = strings.TrimSpace(token)
		return nil
	}
}

// WithAPIKey configures X-API-Key service authentication.
func WithAPIKey(key string) Option {
	return func(client *Client) error {
		client.apiKey = strings.TrimSpace(key)
		return nil
	}
}

// WithActAsUser configures the stable business user represented by a trusted
// service client. The server still decides whether users:act_as is allowed.
func WithActAsUser(userID string) Option {
	return func(client *Client) error {
		client.actAsUserID = strings.TrimSpace(userID)
		return nil
	}
}

// WithHTTPClient supplies the transport. A zero Timeout is recommended for SSE;
// use context deadlines to bound individual calls.
func WithHTTPClient(httpClient *http.Client) Option {
	return func(client *Client) error {
		if httpClient == nil {
			return errors.New("MACS SDK: HTTP client is nil")
		}
		client.httpClient = httpClient
		return nil
	}
}

// WithUserAgent overrides the default SDK User-Agent string.
func WithUserAgent(value string) Option {
	return func(client *Client) error {
		client.userAgent = strings.TrimSpace(value)
		return nil
	}
}

// NewClient constructs an AgentOS client.
func NewClient(rawBaseURL string, options ...Option) (*Client, error) {
	baseURL, err := url.Parse(strings.TrimSpace(rawBaseURL))
	if err != nil {
		return nil, fmt.Errorf("MACS SDK: parse base URL: %w", err)
	}
	if baseURL.Scheme != "http" && baseURL.Scheme != "https" {
		return nil, errors.New("MACS SDK: base URL must use http or https")
	}
	if baseURL.Host == "" || baseURL.User != nil || baseURL.RawQuery != "" || baseURL.Fragment != "" {
		return nil, errors.New("MACS SDK: base URL must contain only scheme, host, and optional path")
	}
	baseURL.Path = strings.TrimRight(baseURL.Path, "/") + "/"

	client := &Client{
		baseURL:    baseURL,
		httpClient: &http.Client{},
		userAgent:  "macs-sdk-go/preview",
	}
	for _, option := range options {
		if option == nil {
			continue
		}
		if err := option(client); err != nil {
			return nil, err
		}
	}
	if client.bearerToken != "" && client.apiKey != "" {
		return nil, errors.New("MACS SDK: configure either a Bearer token or an API key, not both")
	}
	return client, nil
}

// ListRoles discovers Agent roles visible to the configured service identity.
func (client *Client) ListRoles(ctx context.Context) (*RoleList, error) {
	var result RoleList
	if err := client.doJSON(ctx, http.MethodGet, "api/v1/agent/roles", nil, nil, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// SubmitTask creates a durable AgentOS task. idempotencyKey should be stable for
// one business operation and reused when retrying an uncertain submission.
func (client *Client) SubmitTask(ctx context.Context, request SubmitTaskRequest, idempotencyKey string) (*Task, error) {
	headers := make(http.Header)
	if value := strings.TrimSpace(idempotencyKey); value != "" {
		headers.Set("Idempotency-Key", value)
	}
	var result Task
	if err := client.doJSON(ctx, http.MethodPost, "api/v1/agent/tasks", headers, request, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// GetTask returns the current durable task snapshot.
func (client *Client) GetTask(ctx context.Context, taskID string) (*Task, error) {
	path, err := taskPath(taskID, "")
	if err != nil {
		return nil, err
	}
	var result Task
	if err := client.doJSON(ctx, http.MethodGet, path, nil, nil, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// ListTasks returns one cursor page of tasks.
func (client *Client) ListTasks(ctx context.Context, options ListTaskOptions) (*TaskList, error) {
	query := make(url.Values)
	if value := strings.TrimSpace(options.CallerID); value != "" {
		query.Set("caller_id", value)
	}
	if value := strings.TrimSpace(options.RoleID); value != "" {
		query.Set("role_id", value)
	}
	if value := strings.TrimSpace(options.Status); value != "" {
		query.Set("status", value)
	}
	if value := strings.TrimSpace(options.Cursor); value != "" {
		query.Set("cursor", value)
	}
	if options.Limit > 0 {
		query.Set("limit", strconv.Itoa(options.Limit))
	}
	path := "api/v1/agent/tasks"
	if encoded := query.Encode(); encoded != "" {
		path += "?" + encoded
	}
	var result TaskList
	if err := client.doJSON(ctx, http.MethodGet, path, nil, nil, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// CancelTask requests cancellation of a non-terminal task.
func (client *Client) CancelTask(ctx context.Context, taskID string) (*Task, error) {
	path, err := taskPath(taskID, "cancel")
	if err != nil {
		return nil, err
	}
	var result Task
	if err := client.doJSON(ctx, http.MethodPost, path, nil, struct{}{}, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// AddTaskFollowup durably adds an instruction to a pending or running task.
// Reuse idempotencyKey when retrying an uncertain HTTP response.
func (client *Client) AddTaskFollowup(
	ctx context.Context,
	taskID, message, idempotencyKey string,
) (*TaskFollowup, error) {
	path, err := taskPath(taskID, "followups")
	if err != nil {
		return nil, err
	}
	headers := make(http.Header)
	if value := strings.TrimSpace(idempotencyKey); value != "" {
		headers.Set("Idempotency-Key", value)
	}
	var result TaskFollowup
	request := struct {
		Message string `json:"message"`
	}{Message: message}
	if err := client.doJSON(ctx, http.MethodPost, path, headers, request, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// GetTaskFollowup returns one durable follow-up receipt.
func (client *Client) GetTaskFollowup(ctx context.Context, taskID, followupID string) (*TaskFollowup, error) {
	path, err := taskFollowupPath(taskID, followupID)
	if err != nil {
		return nil, err
	}
	var result TaskFollowup
	if err := client.doJSON(ctx, http.MethodGet, path, nil, nil, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// ListTaskFollowups returns the durable instructions accepted for a task.
func (client *Client) ListTaskFollowups(ctx context.Context, taskID string, limit int) (*TaskFollowupList, error) {
	path, err := taskPath(taskID, "followups")
	if err != nil {
		return nil, err
	}
	if limit > 0 {
		path += "?limit=" + strconv.Itoa(limit)
	}
	var result TaskFollowupList
	if err := client.doJSON(ctx, http.MethodGet, path, nil, nil, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (client *Client) doJSON(ctx context.Context, method, path string, headers http.Header, body, result any) error {
	var requestBody io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("MACS SDK: encode request: %w", err)
		}
		requestBody = bytes.NewReader(encoded)
	}
	request, err := client.newRequest(ctx, method, path, requestBody)
	if err != nil {
		return err
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	request.Header.Set("Accept", "application/json")
	for name, values := range headers {
		for _, value := range values {
			request.Header.Add(name, value)
		}
	}

	response, err := client.httpClient.Do(request)
	if err != nil {
		return fmt.Errorf("MACS SDK: %s %s: %w", method, request.URL.Redacted(), err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return decodeAPIError(response)
	}
	if result == nil {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxJSONResponseBytes))
		return nil
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, maxJSONResponseBytes+1))
	if err := decoder.Decode(result); err != nil {
		return fmt.Errorf("MACS SDK: decode HTTP %d response: %w", response.StatusCode, err)
	}
	return nil
}

func (client *Client) newRequest(ctx context.Context, method, path string, body io.Reader) (*http.Request, error) {
	relative, err := url.Parse(path)
	if err != nil {
		return nil, fmt.Errorf("MACS SDK: parse request path: %w", err)
	}
	if relative.IsAbs() || relative.Host != "" {
		return nil, errors.New("MACS SDK: request path must be relative")
	}
	endpoint := client.baseURL.ResolveReference(relative)
	request, err := http.NewRequestWithContext(ctx, method, endpoint.String(), body)
	if err != nil {
		return nil, fmt.Errorf("MACS SDK: create request: %w", err)
	}
	if client.bearerToken != "" {
		request.Header.Set("Authorization", "Bearer "+client.bearerToken)
	}
	if client.apiKey != "" {
		request.Header.Set("X-API-Key", client.apiKey)
	}
	if client.actAsUserID != "" {
		request.Header.Set("X-OpenKitty-User-ID", client.actAsUserID)
	}
	if client.userAgent != "" {
		request.Header.Set("User-Agent", client.userAgent)
	}
	return request, nil
}

func taskPath(taskID, action string) (string, error) {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return "", errors.New("MACS SDK: task ID is required")
	}
	path := "api/v1/agent/tasks/" + url.PathEscape(taskID)
	if action != "" {
		path += "/" + action
	}
	return path, nil
}

func taskFollowupPath(taskID, followupID string) (string, error) {
	path, err := taskPath(taskID, "followups")
	if err != nil {
		return "", err
	}
	followupID = strings.TrimSpace(followupID)
	if followupID == "" {
		return "", errors.New("MACS SDK: follow-up ID is required")
	}
	return path + "/" + url.PathEscape(followupID), nil
}

func decodeAPIError(response *http.Response) error {
	apiError := &APIError{StatusCode: response.StatusCode}
	if seconds, err := strconv.Atoi(strings.TrimSpace(response.Header.Get("Retry-After"))); err == nil && seconds > 0 {
		apiError.RetryAfter = time.Duration(seconds) * time.Second
	}
	limited := io.LimitReader(response.Body, maxJSONResponseBytes+1)
	var envelope struct {
		Error     string `json:"error"`
		Code      string `json:"code"`
		Message   string `json:"message"`
		RequestID string `json:"request_id"`
		Retryable bool   `json:"retryable"`
	}
	if err := json.NewDecoder(limited).Decode(&envelope); err == nil {
		apiError.Code = envelope.Code
		apiError.Message = envelope.Message
		if apiError.Message == "" {
			apiError.Message = envelope.Error
		}
		apiError.RequestID = envelope.RequestID
		apiError.Retryable = envelope.Retryable
	}
	if apiError.RequestID == "" {
		apiError.RequestID = response.Header.Get("X-Request-ID")
	}
	return apiError
}
