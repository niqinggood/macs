package modelruntime

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestCallMessagesDetailedReturnsUsageAndRequestID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer secret" {
			t.Fatalf("Authorization = %q", request.Header.Get("Authorization"))
		}
		w.Header().Set("X-Request-ID", "provider-request-1")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"done"}}],"usage":{"prompt_tokens":17,"completion_tokens":5}}`))
	}))
	defer server.Close()

	result, err := CallMessagesDetailed(
		context.Background(),
		server.Client(),
		CompletionConfig{APIURL: server.URL, APIKey: "secret", ModelName: "test-model"},
		[]map[string]interface{}{{"role": "user", "content": "work"}},
		32,
		0,
		CallPolicy{AttemptTimeout: time.Second, MaxAttempts: 1},
	)
	if err != nil {
		t.Fatalf("CallMessagesDetailed: %v", err)
	}
	if result.Content != "done" || result.PromptTokens != 17 || result.CompletionTokens != 5 {
		t.Fatalf("unexpected completion result: %#v", result)
	}
	if result.RequestID != "provider-request-1" {
		t.Fatalf("RequestID = %q", result.RequestID)
	}
}

func TestCallMessagesCompatibilityWrapperReturnsContent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"compatible"}}]}`))
	}))
	defer server.Close()

	content, err := CallMessages(
		context.Background(),
		server.Client(),
		CompletionConfig{APIURL: server.URL, APIKey: "secret", ModelName: "test-model"},
		[]map[string]interface{}{{"role": "user", "content": "work"}},
		32,
		0,
		CallPolicy{AttemptTimeout: time.Second, MaxAttempts: 1},
	)
	if err != nil {
		t.Fatalf("CallMessages: %v", err)
	}
	if content != "compatible" {
		t.Fatalf("content = %q", content)
	}
}
