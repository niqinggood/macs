package app

import (
	"context"
	"errors"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

type generateLeaseTestTransport struct {
	mu        sync.Mutex
	active    int
	maxActive int
	requests  int
	entered   chan int
	release   <-chan struct{}
}

func newGenerateLeaseTestTransport(release <-chan struct{}) *generateLeaseTestTransport {
	return &generateLeaseTestTransport{
		entered: make(chan int, 8),
		release: release,
	}
}

func (transport *generateLeaseTestTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	transport.mu.Lock()
	transport.requests++
	requestNumber := transport.requests
	transport.active++
	if transport.active > transport.maxActive {
		transport.maxActive = transport.active
	}
	transport.mu.Unlock()

	select {
	case transport.entered <- requestNumber:
	case <-request.Context().Done():
		transport.leave()
		return nil, request.Context().Err()
	}

	select {
	case <-transport.release:
	case <-request.Context().Done():
		transport.leave()
		return nil, request.Context().Err()
	}
	transport.leave()

	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body: io.NopCloser(strings.NewReader(
			`{"choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"这是完整的最终答复。"}}]}`,
		)),
		Request: request,
	}, nil
}

func (transport *generateLeaseTestTransport) leave() {
	transport.mu.Lock()
	transport.active--
	transport.mu.Unlock()
}

func (transport *generateLeaseTestTransport) snapshot() (requests, maxActive int) {
	transport.mu.Lock()
	defer transport.mu.Unlock()
	return transport.requests, transport.maxActive
}

func newGenerateLeaseTestAgent(t *testing.T, transport http.RoundTripper) *LLMAgent {
	t.Helper()
	workspace := t.TempDir()
	return &LLMAgent{
		name: "generate-lease-test",
		modelConfig: &ModelConfig{
			Type:      "openai",
			APIKey:    "test-api-key-long-enough",
			ModelName: "test-model",
			BaseURL:   "http://unit.test/v1",
			MaxTokens: 512,
		},
		maxTokens:     512,
		httpClient:    &http.Client{Transport: transport},
		maxIterations: 3,
		workspace:     workspace,
		fileLogger:    log.New(io.Discard, "", 0),
		globalConfig: &Config{
			Workspace: workspace,
			Runtime: RuntimeConfig{
				LLMCallTimeoutSeconds: 15,
				LLMMaxRetries:         1,
				CompressMsgThreshold:  1000,
				MaxContextChars:       1 << 28,
			},
		},
	}
}

type generateLeaseTestResult struct {
	output string
	err    error
}

func generateLeaseTaskContext(taskID string) context.Context {
	return context.WithValue(context.Background(), "task_id", taskID)
}

func waitForGenerateLeaseEntry(t *testing.T, entered <-chan int) int {
	t.Helper()
	select {
	case requestNumber := <-entered:
		return requestNumber
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for model request")
		return 0
	}
}

func waitForGenerateLeaseResult(t *testing.T, result <-chan generateLeaseTestResult) generateLeaseTestResult {
	t.Helper()
	select {
	case value := <-result:
		return value
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for Generate to return")
		return generateLeaseTestResult{}
	}
}

func TestLLMAgentGenerateSerializesSameInstance(t *testing.T) {
	release := make(chan struct{})
	transport := newGenerateLeaseTestTransport(release)
	agent := newGenerateLeaseTestAgent(t, transport)

	firstDone := make(chan generateLeaseTestResult, 1)
	go func() {
		output, err := agent.Generate(generateLeaseTaskContext("lease-task-1"), "first request")
		firstDone <- generateLeaseTestResult{output: output, err: err}
	}()
	if requestNumber := waitForGenerateLeaseEntry(t, transport.entered); requestNumber != 1 {
		t.Fatalf("first transport request number = %d, want 1", requestNumber)
	}

	secondStarted := make(chan struct{})
	secondDone := make(chan generateLeaseTestResult, 1)
	go func() {
		close(secondStarted)
		output, err := agent.Generate(generateLeaseTaskContext("lease-task-2"), "second request")
		secondDone <- generateLeaseTestResult{output: output, err: err}
	}()
	<-secondStarted
	select {
	case requestNumber := <-transport.entered:
		t.Fatalf("same LLMAgent overlapped Generate calls; request %d entered before the first turn released", requestNumber)
	case <-time.After(150 * time.Millisecond):
	}

	close(release)
	for index, result := range []generateLeaseTestResult{
		waitForGenerateLeaseResult(t, firstDone),
		waitForGenerateLeaseResult(t, secondDone),
	} {
		if result.err != nil || result.output != "这是完整的最终答复。" {
			t.Fatalf("Generate result %d = (%q, %v)", index+1, result.output, result.err)
		}
	}
	requests, maxActive := transport.snapshot()
	if requests != 2 || maxActive != 1 {
		t.Fatalf("transport requests/max concurrency = %d/%d, want 2/1", requests, maxActive)
	}
}

func TestLLMAgentGenerateLeaseWaitHonorsContextCancellation(t *testing.T) {
	release := make(chan struct{})
	transport := newGenerateLeaseTestTransport(release)
	agent := newGenerateLeaseTestAgent(t, transport)

	firstDone := make(chan generateLeaseTestResult, 1)
	go func() {
		output, err := agent.Generate(generateLeaseTaskContext("lease-holder"), "holder request")
		firstDone <- generateLeaseTestResult{output: output, err: err}
	}()
	waitForGenerateLeaseEntry(t, transport.entered)

	waitingCtx, cancelWaiting := context.WithCancel(generateLeaseTaskContext("lease-waiter"))
	waiterStarted := make(chan struct{})
	waiterDone := make(chan generateLeaseTestResult, 1)
	go func() {
		close(waiterStarted)
		output, err := agent.Generate(waitingCtx, "waiting request")
		waiterDone <- generateLeaseTestResult{output: output, err: err}
	}()
	<-waiterStarted
	cancelWaiting()

	waiterResult := waitForGenerateLeaseResult(t, waiterDone)
	if waiterResult.output != "" || !errors.Is(waiterResult.err, context.Canceled) {
		t.Fatalf("canceled waiter result = (%q, %v), want empty/context.Canceled", waiterResult.output, waiterResult.err)
	}
	if requests, _ := transport.snapshot(); requests != 1 {
		t.Fatalf("canceled waiter reached the model transport; requests=%d", requests)
	}

	close(release)
	if holderResult := waitForGenerateLeaseResult(t, firstDone); holderResult.err != nil {
		t.Fatalf("lease holder failed after release: %v", holderResult.err)
	}
}

func TestLLMAgentGenerateRejectsSameInstanceReentry(t *testing.T) {
	agent := &LLMAgent{}
	leasedCtx, release, err := agent.acquireGenerateLease(context.Background())
	if err != nil {
		t.Fatalf("acquire initial lease: %v", err)
	}
	defer release()

	done := make(chan error, 1)
	go func() {
		_, generateErr := agent.Generate(leasedCtx, "recursive request")
		done <- generateErr
	}()
	select {
	case generateErr := <-done:
		if !errors.Is(generateErr, errLLMAgentGenerateReentry) {
			t.Fatalf("recursive Generate error = %v, want %v", generateErr, errLLMAgentGenerateReentry)
		}
	case <-time.After(time.Second):
		t.Fatal("same-instance recursive Generate deadlocked on its own lease")
	}
}

func TestLLMAgentGenerateAllowsDistinctInstancesToOverlap(t *testing.T) {
	release := make(chan struct{})
	transport := newGenerateLeaseTestTransport(release)
	firstAgent := newGenerateLeaseTestAgent(t, transport)
	secondAgent := newGenerateLeaseTestAgent(t, transport)

	results := make(chan generateLeaseTestResult, 2)
	go func() {
		output, err := firstAgent.Generate(generateLeaseTaskContext("ephemeral-1"), "first")
		results <- generateLeaseTestResult{output: output, err: err}
	}()
	go func() {
		output, err := secondAgent.Generate(generateLeaseTaskContext("ephemeral-2"), "second")
		results <- generateLeaseTestResult{output: output, err: err}
	}()

	waitForGenerateLeaseEntry(t, transport.entered)
	waitForGenerateLeaseEntry(t, transport.entered)
	if _, maxActive := transport.snapshot(); maxActive != 2 {
		t.Fatalf("distinct LLMAgent instances did not overlap; max concurrency=%d", maxActive)
	}
	close(release)
	for range 2 {
		if result := waitForGenerateLeaseResult(t, results); result.err != nil {
			t.Fatalf("distinct-instance Generate failed: %v", result.err)
		}
	}
}
