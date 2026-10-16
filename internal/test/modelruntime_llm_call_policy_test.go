package app

import (
	"context"
	"errors"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestResolveLLMCallPolicyDefaultsAndClamps(t *testing.T) {
	tests := []struct {
		name     string
		runtime  RuntimeConfig
		timeout  time.Duration
		attempts int
	}{
		{name: "defaults", runtime: RuntimeConfig{}, timeout: 300 * time.Second, attempts: 2},
		{name: "negative uses defaults", runtime: RuntimeConfig{LLMCallTimeoutSeconds: -1, LLMMaxRetries: -1}, timeout: 300 * time.Second, attempts: 2},
		{name: "minimum timeout", runtime: RuntimeConfig{LLMCallTimeoutSeconds: 1, LLMMaxRetries: 1}, timeout: 15 * time.Second, attempts: 1},
		{name: "maximums", runtime: RuntimeConfig{LLMCallTimeoutSeconds: 999, LLMMaxRetries: 99}, timeout: 300 * time.Second, attempts: 3},
		{name: "custom", runtime: RuntimeConfig{LLMCallTimeoutSeconds: 45, LLMMaxRetries: 2}, timeout: 45 * time.Second, attempts: 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			policy := resolveLLMCallPolicy(tt.runtime)
			if policy.AttemptTimeout != tt.timeout || policy.MaxAttempts != tt.attempts {
				t.Fatalf("policy = {%v, %d}, want {%v, %d}", policy.AttemptTimeout, policy.MaxAttempts, tt.timeout, tt.attempts)
			}
		})
	}
}

func TestLLMCallPolicyDefaultTotalWaitUpperBound(t *testing.T) {
	policy := resolveLLMCallPolicy(RuntimeConfig{})
	// Two 300-second attempts plus the bounded Retry-After ceiling.
	if got, want := policy.totalWaitUpperBound(), 630*time.Second; got != want {
		t.Fatalf("totalWaitUpperBound() = %v, want %v", got, want)
	}
}

func TestNormalizeLLMRuntimeConfig(t *testing.T) {
	runtime := RuntimeConfig{LLMCallTimeoutSeconds: 2, LLMMaxRetries: 20}
	normalizeLLMRuntimeConfig(&runtime)
	if runtime.LLMCallTimeoutSeconds != 15 || runtime.LLMMaxRetries != 3 {
		t.Fatalf("normalized runtime = timeout %d attempts %d", runtime.LLMCallTimeoutSeconds, runtime.LLMMaxRetries)
	}
}

func TestLoadConfigNormalizesLLMCallPolicy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("runtime:\n  semaphore_limit: 4\n"), 0600); err != nil {
		t.Fatal(err)
	}
	config, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if config.Runtime.LLMCallTimeoutSeconds != 300 || config.Runtime.LLMMaxRetries != 2 {
		t.Fatalf("loaded runtime = timeout %d attempts %d", config.Runtime.LLMCallTimeoutSeconds, config.Runtime.LLMMaxRetries)
	}
}

func TestBundledConfigTemplateUsesFiveMinuteLLMCallTimeout(t *testing.T) {
	const path = "openkitty-config.template.yaml"
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if !strings.Contains(string(data), "    llm_call_timeout_seconds: 300") {
		t.Fatalf("%s must keep the five-minute timeout under runtime", path)
	}
}

func TestLLMCallAttemptContextKeepsEarlierParentDeadline(t *testing.T) {
	parent, parentCancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer parentCancel()
	parentDeadline, _ := parent.Deadline()

	callCtx, cancel := llmCallAttemptContext(parent, 30*time.Second)
	defer cancel()
	callDeadline, ok := callCtx.Deadline()
	if !ok {
		t.Fatal("call context has no deadline")
	}
	if delta := callDeadline.Sub(parentDeadline); delta < -5*time.Millisecond || delta > 5*time.Millisecond {
		t.Fatalf("call deadline changed stricter parent deadline by %v", delta)
	}
}

func TestLLMCallPolicyContextOverrideOnlyRaisesAttemptTimeout(t *testing.T) {
	base := llmCallPolicy{AttemptTimeout: 45 * time.Second, MaxAttempts: 1}
	overridden := llmCallPolicyForContext(withLLMCallTimeoutOverride(context.Background(), 60*time.Second), base)
	if overridden.AttemptTimeout != 60*time.Second || overridden.MaxAttempts != 1 {
		t.Fatalf("overridden policy = %#v, want 60s and one attempt", overridden)
	}
	notReduced := llmCallPolicyForContext(withLLMCallTimeoutOverride(context.Background(), 30*time.Second), base)
	if notReduced != base {
		t.Fatalf("shorter override reduced configured policy: got %#v want %#v", notReduced, base)
	}
}

func TestLLMCallPolicyContextOverridesMaxAttemptsExactly(t *testing.T) {
	base := llmCallPolicy{AttemptTimeout: 45 * time.Second, MaxAttempts: 3}
	ctx := withLLMCallMaxAttemptsOverride(context.Background(), 1)
	overridden := llmCallPolicyForContext(ctx, base)
	if overridden.AttemptTimeout != base.AttemptTimeout || overridden.MaxAttempts != 1 {
		t.Fatalf("overridden policy = %#v, want original timeout and one attempt", overridden)
	}

	invalid := llmCallPolicyForContext(withLLMCallMaxAttemptsOverride(context.Background(), 0), base)
	if invalid != base {
		t.Fatalf("non-positive attempt override changed policy: got %#v want %#v", invalid, base)
	}
}

func TestPresentationConstructionPolicyKeepsLongerDefaultAndOneAttempt(t *testing.T) {
	base := resolveLLMCallPolicy(RuntimeConfig{})
	ctx := withLLMCallTimeoutOverride(context.Background(), 120*time.Second)
	ctx = withLLMCallMaxAttemptsOverride(ctx, 1)
	policy := llmCallPolicyForContext(ctx, base)
	if policy.AttemptTimeout != 300*time.Second || policy.MaxAttempts != 1 {
		t.Fatalf("construction policy = %#v, want 300s and one attempt", policy)
	}
}

func TestWaitForLLMRetryHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	started := time.Now()
	err := waitForLLMRetry(ctx, time.Minute)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("waitForLLMRetry error = %v, want context.Canceled", err)
	}
	if elapsed := time.Since(started); elapsed > 100*time.Millisecond {
		t.Fatalf("canceled retry wait took %v", elapsed)
	}
}

func TestLLMRetryDelayHonorsAndBoundsRetryAfter(t *testing.T) {
	policy := llmCallPolicy{AttemptTimeout: time.Minute, MaxAttempts: 2}
	now := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	if got := policy.retryDelay(1, "7", now); got != 7*time.Second {
		t.Fatalf("numeric Retry-After delay = %v, want 7s", got)
	}
	if got := policy.retryDelay(1, "120", now); got != maxLLMRetryDelay {
		t.Fatalf("bounded Retry-After delay = %v, want %v", got, maxLLMRetryDelay)
	}
	if got := policy.retryDelay(1, now.Add(9*time.Second).Format(http.TimeFormat), now); got != 9*time.Second {
		t.Fatalf("date Retry-After delay = %v, want 9s", got)
	}
}

func TestLLMRetryableHTTPStatusRespectsProviderDirective(t *testing.T) {
	if !isRetryableLLMHTTPStatus(http.StatusRequestTimeout, nil) ||
		!isRetryableLLMHTTPStatus(http.StatusTooManyRequests, nil) ||
		!isRetryableLLMHTTPStatus(http.StatusServiceUnavailable, nil) {
		t.Fatal("transient HTTP status was not retryable")
	}
	if isRetryableLLMHTTPStatus(http.StatusBadRequest, nil) {
		t.Fatal("400 must not be retried")
	}
	headers := http.Header{"X-Should-Retry": []string{"false"}}
	if isRetryableLLMHTTPStatus(http.StatusServiceUnavailable, headers) {
		t.Fatal("X-Should-Retry=false must prevent a retry")
	}
}

type sequentialLLMTransport struct {
	mu        sync.Mutex
	responses []*http.Response
	requests  int
	bodies    []string
}

func (t *sequentialLLMTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	body, _ := io.ReadAll(req.Body)
	t.bodies = append(t.bodies, string(body))
	index := t.requests
	t.requests++
	if index >= len(t.responses) {
		return nil, errors.New("unexpected request")
	}
	response := t.responses[index]
	response.Request = req
	return response, nil
}

func llmTestResponse(status int, body string, headers http.Header) *http.Response {
	if headers == nil {
		headers = make(http.Header)
	}
	return &http.Response{
		StatusCode: status,
		Header:     headers,
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func TestCallLLMRetriesTransientStatusAgainstSameModel(t *testing.T) {
	transport := &sequentialLLMTransport{responses: []*http.Response{
		llmTestResponse(http.StatusServiceUnavailable, `{"error":"busy"}`, http.Header{"Retry-After": []string{"0"}}),
		llmTestResponse(http.StatusOK, `{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}]}`, nil),
	}}
	agent := newDeadlineTestAgent(transport, RuntimeConfig{LLMCallTimeoutSeconds: 30, LLMMaxRetries: 2})
	response, err := agent.callLLM(context.Background(), []map[string]interface{}{{"role": "user", "content": "hello"}})
	if err != nil {
		t.Fatal(err)
	}
	if response.Content != "ok" || transport.requests != 2 {
		t.Fatalf("response=%#v requests=%d, want ok after 2 attempts", response, transport.requests)
	}
	for _, body := range transport.bodies {
		if !strings.Contains(body, `"model":"test-model"`) {
			t.Fatalf("retry changed selected model: %s", body)
		}
	}
}

func TestCallLLMDoesNotRetryPermanentBadRequest(t *testing.T) {
	transport := &sequentialLLMTransport{responses: []*http.Response{
		llmTestResponse(http.StatusBadRequest, `{"error":"invalid request"}`, nil),
	}}
	agent := newDeadlineTestAgent(transport, RuntimeConfig{LLMCallTimeoutSeconds: 30, LLMMaxRetries: 3})
	_, err := agent.callLLM(context.Background(), []map[string]interface{}{{"role": "user", "content": "hello"}})
	if err == nil {
		t.Fatal("callLLM unexpectedly succeeded")
	}
	if transport.requests != 1 {
		t.Fatalf("permanent 400 requests = %d, want 1", transport.requests)
	}
}

func TestCallLLMStreamRetriesBeforeFirstChunk(t *testing.T) {
	stream := "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"
	transport := &sequentialLLMTransport{responses: []*http.Response{
		llmTestResponse(http.StatusTooManyRequests, `{"error":"rate limited"}`, http.Header{"Retry-After": []string{"0"}}),
		llmTestResponse(http.StatusOK, stream, http.Header{"Content-Type": []string{"text/event-stream"}}),
	}}
	agent := newDeadlineTestAgent(transport, RuntimeConfig{LLMCallTimeoutSeconds: 30, LLMMaxRetries: 2})
	response, err := agent.callLLMStream(context.Background(), []map[string]interface{}{{"role": "user", "content": "hello"}}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if response.Content != "ok" || transport.requests != 2 {
		t.Fatalf("stream response=%#v requests=%d, want ok after 2 attempts", response, transport.requests)
	}
}

func TestCallLLMInternalUsesSameBoundedRetryPolicy(t *testing.T) {
	transport := &sequentialLLMTransport{responses: []*http.Response{
		llmTestResponse(http.StatusBadGateway, `{"error":"upstream reset"}`, http.Header{"Retry-After": []string{"0"}}),
		llmTestResponse(http.StatusOK, `{"choices":[{"message":{"content":"summary-ok"}}]}`, nil),
	}}
	agent := newDeadlineTestAgent(transport, RuntimeConfig{LLMCallTimeoutSeconds: 30, LLMMaxRetries: 2})
	content, err := agent.callLLMInternal(context.Background(), []map[string]interface{}{{"role": "user", "content": "summarize"}}, 64)
	if err != nil {
		t.Fatal(err)
	}
	if content != "summary-ok" || transport.requests != 2 {
		t.Fatalf("content=%q requests=%d, want summary-ok after 2 attempts", content, transport.requests)
	}
	for _, body := range transport.bodies {
		if !strings.Contains(body, `"model":"test-model"`) {
			t.Fatalf("internal retry changed selected model: %s", body)
		}
	}
}

type captureDeadlineTransport struct {
	requests  int
	deadline  time.Time
	hasExpiry bool
}

func (t *captureDeadlineTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	t.requests++
	t.deadline, t.hasExpiry = req.Context().Deadline()
	return nil, errors.New("test transport stop")
}

func newDeadlineTestAgent(transport http.RoundTripper, runtime RuntimeConfig) *LLMAgent {
	return &LLMAgent{
		name: "deadline-test",
		modelConfig: &ModelConfig{
			Type:        "openai",
			APIKey:      "test-api-key-long-enough",
			ModelName:   "test-model",
			BaseURL:     "http://unit.test/v1",
			MaxTokens:   64,
			Temperature: 0,
		},
		httpClient:   &http.Client{Transport: transport},
		fileLogger:   log.New(io.Discard, "", 0),
		globalConfig: &Config{Runtime: runtime},
	}
}

func assertDeadlineNear(t *testing.T, deadline time.Time, want time.Duration) {
	t.Helper()
	remaining := time.Until(deadline)
	if remaining < want-time.Second || remaining > want+time.Second {
		t.Fatalf("deadline remaining = %v, want about %v", remaining, want)
	}
}

func TestCallLLMUsesConfiguredAttemptDeadlineAndAttemptCount(t *testing.T) {
	transport := &captureDeadlineTransport{}
	agent := newDeadlineTestAgent(transport, RuntimeConfig{LLMCallTimeoutSeconds: 30, LLMMaxRetries: 1})

	_, err := agent.callLLM(context.Background(), []map[string]interface{}{{"role": "user", "content": "hello"}})
	if err == nil {
		t.Fatal("callLLM unexpectedly succeeded")
	}
	if transport.requests != 1 {
		t.Fatalf("requests = %d, want 1", transport.requests)
	}
	if !transport.hasExpiry {
		t.Fatal("non-streaming request has no deadline")
	}
	assertDeadlineNear(t, transport.deadline, 30*time.Second)
}

func TestCallLLMUsesPresentationConstructionContextPolicy(t *testing.T) {
	transport := &captureDeadlineTransport{}
	agent := newDeadlineTestAgent(transport, RuntimeConfig{})
	ctx := withLLMCallTimeoutOverride(context.Background(), 120*time.Second)
	ctx = withLLMCallMaxAttemptsOverride(ctx, 1)

	_, err := agent.callLLM(ctx, []map[string]interface{}{{"role": "user", "content": "build the deck"}})
	if err == nil {
		t.Fatal("callLLM unexpectedly succeeded")
	}
	if transport.requests != 1 {
		t.Fatalf("requests = %d, want exactly one construction attempt", transport.requests)
	}
	if !transport.hasExpiry {
		t.Fatal("construction request has no deadline")
	}
	assertDeadlineNear(t, transport.deadline, 300*time.Second)
}

func TestCallLLMStreamUsesConfiguredAttemptDeadline(t *testing.T) {
	transport := &captureDeadlineTransport{}
	agent := newDeadlineTestAgent(transport, RuntimeConfig{LLMCallTimeoutSeconds: 45, LLMMaxRetries: 1})

	_, err := agent.callLLMStream(context.Background(), []map[string]interface{}{{"role": "user", "content": "hello"}}, nil, nil)
	if err == nil {
		t.Fatal("callLLMStream unexpectedly succeeded")
	}
	if transport.requests != 1 {
		t.Fatalf("requests = %d, want 1", transport.requests)
	}
	if !transport.hasExpiry {
		t.Fatal("streaming request has no deadline")
	}
	assertDeadlineNear(t, transport.deadline, 45*time.Second)
}
