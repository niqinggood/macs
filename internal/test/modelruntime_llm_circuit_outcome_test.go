package app

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"mac/internal/runtimeutil"
)

type llmCircuitTransportOutcome struct {
	statusCode  int
	contentType string
	body        string
	header      http.Header
	err         error
}

type llmCircuitTransport struct {
	mu       sync.Mutex
	outcomes []llmCircuitTransportOutcome
	requests int
}

func (t *llmCircuitTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	t.mu.Lock()
	index := t.requests
	t.requests++
	outcome := t.outcomes[len(t.outcomes)-1]
	if index < len(t.outcomes) {
		outcome = t.outcomes[index]
	}
	t.mu.Unlock()

	if outcome.err != nil {
		return nil, outcome.err
	}
	statusCode := outcome.statusCode
	if statusCode == 0 {
		statusCode = http.StatusOK
	}
	header := outcome.header.Clone()
	if header == nil {
		header = make(http.Header)
	}
	if outcome.contentType != "" {
		header.Set("Content-Type", outcome.contentType)
	}
	return &http.Response{
		StatusCode: statusCode,
		Header:     header,
		Body:       io.NopCloser(strings.NewReader(outcome.body)),
		Request:    req,
	}, nil
}

func (t *llmCircuitTransport) RequestCount() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.requests
}

func llmCircuitSuccessOutcome(t *testing.T, streaming bool) llmCircuitTransportOutcome {
	t.Helper()
	if streaming {
		return llmCircuitTransportOutcome{
			contentType: "text/event-stream",
			body:        completionSSE(t, "stop", "ok"),
		}
	}
	return llmCircuitTransportOutcome{
		contentType: "application/json",
		body:        completionJSON(t, "stop", "ok"),
	}
}

func llmCircuitFailureOutcome(statusCode int) llmCircuitTransportOutcome {
	return llmCircuitTransportOutcome{
		statusCode:  statusCode,
		contentType: "application/json",
		body:        `{"error":{"message":"provider unavailable"}}`,
		header:      http.Header{"Retry-After": []string{"0"}},
	}
}

func newLLMCircuitTestAgent(t *testing.T, streaming bool, transport http.RoundTripper, breaker *runtimeutil.CircuitBreaker, attempts int) *LLMAgent {
	t.Helper()
	agent := newIncompleteResponseTestAgent(t, transport, streaming, 2)
	agent.circuitBreaker = breaker
	agent.globalConfig.Runtime.LLMMaxRetries = attempts
	return agent
}

func callLLMCircuitTestPath(agent *LLMAgent, streaming bool, ctx context.Context) (*AgentResponse, error) {
	messages := []map[string]interface{}{{"role": "user", "content": "reply ok"}}
	if streaming {
		return agent.callLLMStream(ctx, messages, nil, nil)
	}
	return agent.callLLM(ctx, messages)
}

func circuitStat(t *testing.T, breaker *runtimeutil.CircuitBreaker, key string) int64 {
	t.Helper()
	value := breaker.Stats()[key]
	switch typed := value.(type) {
	case int:
		return int64(typed)
	case int64:
		return typed
	default:
		t.Fatalf("circuit stat %q has type %T", key, value)
		return 0
	}
}

func TestLLMCircuitOutcomeRecordsOneSuccessForBothPaths(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		t.Run(map[bool]string{false: "non-streaming", true: "streaming"}[streaming], func(t *testing.T) {
			transport := &llmCircuitTransport{outcomes: []llmCircuitTransportOutcome{llmCircuitSuccessOutcome(t, streaming)}}
			breaker := runtimeutil.NewCircuitBreaker(2, 1, time.Minute)
			agent := newLLMCircuitTestAgent(t, streaming, transport, breaker, 1)

			response, err := callLLMCircuitTestPath(agent, streaming, context.Background())
			if err != nil || response == nil {
				t.Fatalf("successful model call = response:%#v err:%v", response, err)
			}
			if got := circuitStat(t, breaker, "total_successes"); got != 1 {
				t.Fatalf("total successes = %d, want 1", got)
			}
			if got := circuitStat(t, breaker, "total_failures"); got != 0 {
				t.Fatalf("total failures = %d, want 0", got)
			}
			if transport.RequestCount() != 1 || breaker.State() != runtimeutil.CircuitClosed {
				t.Fatalf("requests=%d state=%s", transport.RequestCount(), breaker.State())
			}
		})
	}
}

func TestLLMCircuitOutcomeRetriesSettleOnceForBothPaths(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		t.Run(map[bool]string{false: "non-streaming", true: "streaming"}[streaming], func(t *testing.T) {
			transport := &llmCircuitTransport{outcomes: []llmCircuitTransportOutcome{
				llmCircuitFailureOutcome(http.StatusInternalServerError),
				llmCircuitSuccessOutcome(t, streaming),
			}}
			breaker := runtimeutil.NewCircuitBreaker(1, 1, time.Minute)
			agent := newLLMCircuitTestAgent(t, streaming, transport, breaker, 2)

			response, err := callLLMCircuitTestPath(agent, streaming, context.Background())
			if err != nil || response == nil {
				t.Fatalf("retrying model call = response:%#v err:%v", response, err)
			}
			if transport.RequestCount() != 2 {
				t.Fatalf("provider requests = %d, want 2", transport.RequestCount())
			}
			if got := circuitStat(t, breaker, "total_successes"); got != 1 {
				t.Fatalf("logical call successes = %d, want 1", got)
			}
			if got := circuitStat(t, breaker, "total_failures"); got != 0 {
				t.Fatalf("a recovered attempt leaked into logical failure accounting: %d", got)
			}
			if breaker.State() != runtimeutil.CircuitClosed {
				t.Fatalf("recovered logical call left breaker %s", breaker.State())
			}
		})
	}
}

func TestLLMCircuitOutcomeOpensForHTTPAndTransportFailuresOnBothPaths(t *testing.T) {
	failureCases := []struct {
		name    string
		outcome llmCircuitTransportOutcome
	}{
		{name: "http-500", outcome: llmCircuitFailureOutcome(http.StatusInternalServerError)},
		{name: "transport", outcome: llmCircuitTransportOutcome{err: errors.New("connection reset by peer")}},
	}
	for _, streaming := range []bool{false, true} {
		for _, failureCase := range failureCases {
			t.Run(map[bool]string{false: "non-streaming", true: "streaming"}[streaming]+"/"+failureCase.name, func(t *testing.T) {
				transport := &llmCircuitTransport{outcomes: []llmCircuitTransportOutcome{failureCase.outcome}}
				breaker := runtimeutil.NewCircuitBreaker(2, 1, time.Minute)
				agent := newLLMCircuitTestAgent(t, streaming, transport, breaker, 1)

				for call := 0; call < 2; call++ {
					if _, err := callLLMCircuitTestPath(agent, streaming, context.Background()); err == nil {
						t.Fatalf("failure call %d unexpectedly succeeded", call+1)
					}
				}
				if breaker.State() != runtimeutil.CircuitOpen || circuitStat(t, breaker, "total_failures") != 2 {
					t.Fatalf("state=%s stats=%v", breaker.State(), breaker.Stats())
				}
				if _, err := callLLMCircuitTestPath(agent, streaming, context.Background()); err == nil || !strings.Contains(err.Error(), "circuit breaker") {
					t.Fatalf("open breaker did not reject the next logical call: %v", err)
				}
				if transport.RequestCount() != 2 {
					t.Fatalf("open breaker leaked a provider request: %d", transport.RequestCount())
				}
			})
		}
	}
}

func TestLLMCircuitOutcomeIgnoresPermanentHTTPFailuresOnBothPaths(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden} {
			t.Run(map[bool]string{false: "non-streaming", true: "streaming"}[streaming]+"/"+http.StatusText(status), func(t *testing.T) {
				transport := &llmCircuitTransport{outcomes: []llmCircuitTransportOutcome{llmCircuitFailureOutcome(status)}}
				breaker := runtimeutil.NewCircuitBreaker(1, 1, time.Minute)
				agent := newLLMCircuitTestAgent(t, streaming, transport, breaker, 1)

				if _, err := callLLMCircuitTestPath(agent, streaming, context.Background()); err == nil {
					t.Fatal("permanent provider response should still fail the model call")
				}
				if breaker.State() != runtimeutil.CircuitClosed || circuitStat(t, breaker, "total_failures") != 0 {
					t.Fatalf("permanent status polluted provider-health state: %v", breaker.Stats())
				}
			})
		}
	}
}

func TestLLMCircuitOutcomePermanentHalfOpenResponseReleasesProbe(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		t.Run(map[bool]string{false: "non-streaming", true: "streaming"}[streaming], func(t *testing.T) {
			transport := &llmCircuitTransport{outcomes: []llmCircuitTransportOutcome{
				llmCircuitFailureOutcome(http.StatusUnauthorized),
				llmCircuitSuccessOutcome(t, streaming),
			}}
			breaker := runtimeutil.NewCircuitBreaker(1, 1, time.Millisecond)
			breaker.RecordFailure(http.StatusInternalServerError)
			time.Sleep(5 * time.Millisecond)
			agent := newLLMCircuitTestAgent(t, streaming, transport, breaker, 1)

			if _, err := callLLMCircuitTestPath(agent, streaming, context.Background()); err == nil {
				t.Fatal("401 probe should fail the model call")
			}
			if breaker.State() != runtimeutil.CircuitHalfOpen || circuitStat(t, breaker, "total_failures") != 1 {
				t.Fatalf("permanent probe should be neutral and leave half-open available: %v", breaker.Stats())
			}
			if _, err := callLLMCircuitTestPath(agent, streaming, context.Background()); err != nil {
				t.Fatalf("permanent response leaked the half-open lease: %v", err)
			}
			if breaker.State() != runtimeutil.CircuitClosed || transport.RequestCount() != 2 {
				t.Fatalf("follow-up probe state=%s requests=%d", breaker.State(), transport.RequestCount())
			}
		})
	}
}

func TestLLMCircuitOutcomeHalfOpenSettlesForBothPaths(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		for _, probeSucceeds := range []bool{false, true} {
			name := map[bool]string{false: "failure", true: "success"}[probeSucceeds]
			t.Run(map[bool]string{false: "non-streaming", true: "streaming"}[streaming]+"/"+name, func(t *testing.T) {
				probeOutcome := llmCircuitFailureOutcome(http.StatusInternalServerError)
				if probeSucceeds {
					probeOutcome = llmCircuitSuccessOutcome(t, streaming)
				}
				transport := &llmCircuitTransport{outcomes: []llmCircuitTransportOutcome{
					llmCircuitFailureOutcome(http.StatusInternalServerError),
					probeOutcome,
				}}
				breaker := runtimeutil.NewCircuitBreaker(1, 1, time.Millisecond)
				agent := newLLMCircuitTestAgent(t, streaming, transport, breaker, 1)

				if _, err := callLLMCircuitTestPath(agent, streaming, context.Background()); err == nil || breaker.State() != runtimeutil.CircuitOpen {
					t.Fatalf("setup failure did not open breaker: err=%v state=%s", err, breaker.State())
				}
				time.Sleep(5 * time.Millisecond)
				_, probeErr := callLLMCircuitTestPath(agent, streaming, context.Background())
				if probeSucceeds {
					if probeErr != nil || breaker.State() != runtimeutil.CircuitClosed {
						t.Fatalf("successful half-open probe did not close breaker: err=%v state=%s", probeErr, breaker.State())
					}
				} else {
					if probeErr == nil || breaker.State() != runtimeutil.CircuitOpen {
						t.Fatalf("failed half-open probe did not reopen breaker: err=%v state=%s", probeErr, breaker.State())
					}
				}
				if transport.RequestCount() != 2 {
					t.Fatalf("provider requests = %d, want 2", transport.RequestCount())
				}
			})
		}
	}
}

func TestLLMCircuitOutcomeNeutralCallsReleaseHalfOpenProbe(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		for _, neutralCase := range []string{"no-provider-request", "parent-cancelled"} {
			t.Run(map[bool]string{false: "non-streaming", true: "streaming"}[streaming]+"/"+neutralCase, func(t *testing.T) {
				transport := &llmCircuitTransport{outcomes: []llmCircuitTransportOutcome{llmCircuitSuccessOutcome(t, streaming)}}
				breaker := runtimeutil.NewCircuitBreaker(1, 1, time.Millisecond)
				breaker.RecordFailure(http.StatusInternalServerError)
				time.Sleep(5 * time.Millisecond)
				agent := newLLMCircuitTestAgent(t, streaming, transport, breaker, 1)

				ctx := context.Background()
				if neutralCase == "no-provider-request" {
					agent.modelConfig.BaseURL = "://invalid"
				} else {
					cancelled, cancel := context.WithCancel(ctx)
					cancel()
					ctx = cancelled
				}
				if _, err := callLLMCircuitTestPath(agent, streaming, ctx); err == nil {
					t.Fatal("neutral local call should return its local error")
				}
				if transport.RequestCount() != 0 || circuitStat(t, breaker, "total_failures") != 1 {
					t.Fatalf("neutral call changed health accounting: requests=%d stats=%v", transport.RequestCount(), breaker.Stats())
				}

				agent.modelConfig.BaseURL = "http://unit.test/v1"
				if _, err := callLLMCircuitTestPath(agent, streaming, context.Background()); err != nil {
					t.Fatalf("half-open lease was not released for the next probe: %v", err)
				}
				if breaker.State() != runtimeutil.CircuitClosed || circuitStat(t, breaker, "total_successes") != 1 {
					t.Fatalf("follow-up probe did not close breaker: %v", breaker.Stats())
				}
			})
		}
	}
}
