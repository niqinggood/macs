package modelruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"mac/internal/textnormalize"
)

// CompletionConfig contains the provider values needed for a prompt-transparent
// chat completion request.
type CompletionConfig struct {
	APIURL    string
	APIKey    string
	ModelName string
	MaxTokens int
	// APIKeyOptional is selected by the host's configured provider policy.
	// The default keeps cloud model calls authenticated.
	APIKeyOptional bool
	AccountAuth    bool
}

// CompletionResult is the provider-settled result of one prompt-transparent
// completion attempt. Usage fields are zero when a compatible provider omits
// them; callers may retain their own estimates in that case.
type CompletionResult struct {
	Content          string
	PromptTokens     int
	CompletionTokens int
	RequestID        string
}

// CallMessages sends exactly the supplied messages to an OpenAI-compatible
// chat completion endpoint and retries according to policy.
func CallMessages(
	ctx context.Context,
	client *http.Client,
	config CompletionConfig,
	messages []map[string]interface{},
	maxTokens int,
	temperature float64,
	policy CallPolicy,
) (string, error) {
	result, err := CallMessagesDetailed(ctx, client, config, messages, maxTokens, temperature, policy)
	return result.Content, err
}

// CallMessagesDetailed is CallMessages with provider usage and request
// correlation metadata. Keeping the detailed form beside the compatibility
// wrapper lets managed runtimes enforce run-wide budgets without changing
// ordinary model callers.
func CallMessagesDetailed(
	ctx context.Context,
	client *http.Client,
	config CompletionConfig,
	messages []map[string]interface{},
	maxTokens int,
	temperature float64,
	policy CallPolicy,
) (CompletionResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	apiKey := strings.TrimSpace(config.APIKey)
	if apiKey == "" && !config.APIKeyOptional && !config.AccountAuth {
		return CompletionResult{}, fmt.Errorf("model %q has no API key", config.ModelName)
	}
	if config.ModelName == "" {
		return CompletionResult{}, errors.New("model name is required")
	}
	if config.APIURL == "" {
		return CompletionResult{}, errors.New("model API URL is required")
	}
	if len(messages) == 0 {
		return CompletionResult{}, errors.New("at least one model message is required")
	}
	if maxTokens <= 0 {
		maxTokens = config.MaxTokens
	}
	if maxTokens <= 0 {
		maxTokens = 1024
	}
	if client == nil {
		client = &http.Client{Timeout: policy.AttemptTimeout}
	}
	client = ModelHTTPClient(client, config.AccountAuth)

	requestBody := map[string]interface{}{
		"model":       config.ModelName,
		"messages":    messages,
		"max_tokens":  maxTokens,
		"temperature": temperature,
		"stream":      false,
	}
	jsonData, err := json.Marshal(requestBody)
	if err != nil {
		return CompletionResult{}, err
	}
	ctx = WithPlatformCallIdentity(ctx, config.APIURL)

	var lastErr error
	var nextRetryDelay time.Duration
	hasNextRetryDelay := false
	for attempt := 0; attempt < policy.MaxAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return CompletionResult{}, err
		}
		if attempt > 0 {
			delay := nextRetryDelay
			if !hasNextRetryDelay {
				delay = policy.RetryBackoff(attempt)
			}
			nextRetryDelay = 0
			hasNextRetryDelay = false
			if err := WaitForRetry(ctx, delay); err != nil {
				return CompletionResult{}, err
			}
		}

		attemptCtx, cancel := AttemptContext(ctx, policy.AttemptTimeout)
		req, err := http.NewRequestWithContext(attemptCtx, http.MethodPost, config.APIURL, bytes.NewReader(jsonData))
		if err != nil {
			cancel()
			return CompletionResult{}, err
		}
		req.Header.Set("Content-Type", "application/json")
		if err := AuthorizeModelRequest(req, apiKey, config.AccountAuth); err != nil {
			cancel()
			return CompletionResult{}, err
		}
		req.Header.Set("X-Retry-Count", fmt.Sprintf("%d", attempt))
		SetPlatformCallIdentity(req, jsonData)

		resp, err := client.Do(req)
		if err != nil {
			cancel()
			lastErr = err
			continue
		}
		body, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		cancel()
		if readErr != nil {
			lastErr = readErr
			continue
		}
		if resp.StatusCode != http.StatusOK {
			lastErr = &HTTPStatusError{
				StatusCode: resp.StatusCode,
				Header:     resp.Header.Clone(),
				Message:    fmt.Sprintf("internal LLM API error %d: %s", resp.StatusCode, string(body)),
			}
			if !IsRetryableHTTPStatus(resp.StatusCode, resp.Header) {
				return CompletionResult{}, lastErr
			}
			nextRetryDelay = policy.RetryDelay(attempt+1, resp.Header.Get("Retry-After"), time.Now())
			hasNextRetryDelay = true
			continue
		}

		var result struct {
			Choices []struct {
				Message struct {
					Content string `json:"content"`
				} `json:"message"`
			} `json:"choices"`
			Usage struct {
				PromptTokens     int `json:"prompt_tokens"`
				CompletionTokens int `json:"completion_tokens"`
			} `json:"usage"`
		}
		if err := textnormalize.DecodeProviderJSON(body, &result); err != nil {
			lastErr = err
			continue
		}
		if len(result.Choices) == 0 {
			lastErr = errors.New("empty LLM response")
			continue
		}
		requestID := firstCompletionRequestID(resp.Header)
		return CompletionResult{
			Content:          result.Choices[0].Message.Content,
			PromptTokens:     result.Usage.PromptTokens,
			CompletionTokens: result.Usage.CompletionTokens,
			RequestID:        requestID,
		}, nil
	}
	if lastErr == nil {
		lastErr = errors.New("internal LLM call exhausted attempts")
	}
	return CompletionResult{}, lastErr
}

func firstCompletionRequestID(header http.Header) string {
	for _, name := range []string{"X-Request-ID", "Request-ID", "X-Trace-ID", "Trace-ID"} {
		if value := header.Get(name); value != "" {
			return value
		}
	}
	return ""
}
