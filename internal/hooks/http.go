package hooks

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"time"
)

// ====================== HTTP Hook Executor ======================
// Sends hook context as JSON POST to a URL and parses the response.

type HookHTTPExecutor struct {
	client     *http.Client
	maxTimeout time.Duration
}

func NewHookHTTPExecutor() *HookHTTPExecutor {
	return &HookHTTPExecutor{
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
		maxTimeout: 30 * time.Second,
	}
}

func (e *HookHTTPExecutor) Execute(cfg V2HookConfig, hookCtx *HookContextV2) (*HookResultV2, error) {
	return e.ExecuteContext(context.Background(), cfg, hookCtx)
}

func (e *HookHTTPExecutor) ExecuteContext(parent context.Context, cfg V2HookConfig, hookCtx *HookContextV2) (*HookResultV2, error) {
	if cfg.URL == "" {
		return nil, fmt.Errorf("HTTP hook requires 'url' field")
	}

	timeout := 30 * time.Second
	if cfg.Timeout > 0 {
		timeout = time.Duration(cfg.Timeout) * time.Second
	}
	if timeout > e.maxTimeout {
		timeout = e.maxTimeout
	}

	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()

	body := hookCtx.ToJSON()
	req, err := http.NewRequestWithContext(ctx, "POST", cfg.URL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("http hook create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "OpenKitty-Hook/2.0")

	start := time.Now()
	resp, err := e.client.Do(req)
	duration := time.Since(start)

	if err != nil {
		if parent.Err() != nil {
			return nil, fmt.Errorf("http hook canceled: %w", parent.Err())
		}
		if ctx.Err() == context.DeadlineExceeded {
			return &HookResultV2{
				Decision:     HookDecisionBlock,
				Message:      fmt.Sprintf("HTTP hook timed out after %v", timeout),
				ExecutorType: HookExecHTTP,
				Duration:     duration,
			}, nil
		}
		return nil, fmt.Errorf("http hook call: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	if resp.StatusCode >= 400 {
		return &HookResultV2{
			Decision:     HookDecisionBlock,
			Message:      fmt.Sprintf("HTTP hook returned status %d: %s", resp.StatusCode, string(respBody)),
			ExecutorType: HookExecHTTP,
			Duration:     duration,
			RawOutput:    string(respBody),
		}, nil
	}

	result, parseErr := ValidateHookOutput(respBody)
	if parseErr != nil {
		return &HookResultV2{
			Decision:     HookDecisionAllow,
			Message:      fmt.Sprintf("hook response parse warning: %v (raw: %.200s)", parseErr, string(respBody)),
			ExecutorType: HookExecHTTP,
			Duration:     duration,
			RawOutput:    string(respBody),
		}, nil
	}

	result.ExecutorType = HookExecHTTP
	result.Duration = duration
	result.RawOutput = string(respBody)
	return result, nil
}
