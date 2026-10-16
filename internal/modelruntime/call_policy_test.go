package modelruntime

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"
)

func TestResolveCallPolicyDefaultsAndClamps(t *testing.T) {
	tests := []struct {
		name           string
		timeoutSeconds int
		maxAttempts    int
		wantTimeout    time.Duration
		wantAttempts   int
	}{
		{name: "defaults", wantTimeout: 300 * time.Second, wantAttempts: 2},
		{name: "minimum", timeoutSeconds: 1, maxAttempts: 1, wantTimeout: 15 * time.Second, wantAttempts: 1},
		{name: "maximum", timeoutSeconds: 999, maxAttempts: 99, wantTimeout: 300 * time.Second, wantAttempts: 3},
		{name: "custom", timeoutSeconds: 45, maxAttempts: 2, wantTimeout: 45 * time.Second, wantAttempts: 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			policy := ResolveCallPolicy(tt.timeoutSeconds, tt.maxAttempts)
			if policy.AttemptTimeout != tt.wantTimeout || policy.MaxAttempts != tt.wantAttempts {
				t.Fatalf("policy = %#v, want timeout %v and attempts %d", policy, tt.wantTimeout, tt.wantAttempts)
			}
		})
	}
}

func TestCallPolicyContextOverrides(t *testing.T) {
	base := CallPolicy{AttemptTimeout: 45 * time.Second, MaxAttempts: 3}
	ctx := WithCallTimeoutOverride(context.Background(), 60*time.Second)
	ctx = WithCallMaxAttemptsOverride(ctx, 1)
	got := CallPolicyForContext(ctx, base)
	if got.AttemptTimeout != 60*time.Second || got.MaxAttempts != 1 {
		t.Fatalf("overridden policy = %#v", got)
	}
	shorter := CallPolicyForContext(WithCallTimeoutOverride(context.Background(), 30*time.Second), base)
	if shorter != base {
		t.Fatalf("shorter timeout reduced policy: got %#v want %#v", shorter, base)
	}
}

func TestRetryDelayAndStatusPolicy(t *testing.T) {
	policy := CallPolicy{AttemptTimeout: time.Minute, MaxAttempts: 2}
	now := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	if got := policy.RetryDelay(1, "7", now); got != 7*time.Second {
		t.Fatalf("numeric Retry-After = %v", got)
	}
	if got := policy.RetryDelay(1, "120", now); got != MaxRetryDelay {
		t.Fatalf("bounded Retry-After = %v", got)
	}
	if !IsRetryableHTTPStatus(http.StatusTooManyRequests, nil) || IsRetryableHTTPStatus(http.StatusBadRequest, nil) {
		t.Fatal("unexpected HTTP retry classification")
	}
	headers := http.Header{"X-Should-Retry": []string{"false"}}
	if IsRetryableHTTPStatus(http.StatusServiceUnavailable, headers) {
		t.Fatal("provider retry directive was ignored")
	}
}

func TestAttemptContextAndRetryWaitHonorParent(t *testing.T) {
	parent, cancelParent := context.WithTimeout(context.Background(), time.Second)
	defer cancelParent()
	parentDeadline, _ := parent.Deadline()
	ctx, cancel := AttemptContext(parent, 30*time.Second)
	defer cancel()
	deadline, ok := ctx.Deadline()
	if !ok || deadline.Sub(parentDeadline) < -5*time.Millisecond || deadline.Sub(parentDeadline) > 5*time.Millisecond {
		t.Fatalf("attempt context did not preserve parent deadline")
	}

	canceled, cancelCanceled := context.WithCancel(context.Background())
	cancelCanceled()
	if err := WaitForRetry(canceled, time.Minute); !errors.Is(err, context.Canceled) {
		t.Fatalf("WaitForRetry error = %v", err)
	}
}

func TestHTTPErrorMetadata(t *testing.T) {
	wantHeader := http.Header{"Retry-After": []string{"2"}}
	err := &HTTPStatusError{StatusCode: http.StatusTooManyRequests, Header: wantHeader, Message: "busy"}
	status, header, ok := HTTPErrorMetadata(err)
	if !ok || status != http.StatusTooManyRequests || header.Get("Retry-After") != "2" {
		t.Fatalf("metadata = %d %#v %v", status, header, ok)
	}
}
