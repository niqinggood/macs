package modelruntime

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestChatConnectionValidatesReplyAndStatus(t *testing.T) {
	for _, test := range []struct {
		name, body, want string
		status           int
	}{
		{"reply", `{"choices":[{"message":{"role":"assistant","content":"OK"}}]}`, "", 200},
		{"website", "<html>Login</html>", "invalid_response", 200},
		{"empty", `{}`, "invalid_response", 200},
		{"missing message", `{"choices":[{}]}`, "invalid_response", 200},
		{"key", "private upstream details", "unauthorized", 401},
		{"model", "private upstream details", "model_unavailable", 404},
		{"quota", "private upstream details", "rate_limited", 429},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer test-only" {
					t.Error("incorrect endpoint or key")
				}
				w.WriteHeader(test.status)
				_, _ = w.Write([]byte(test.body))
			}))
			defer server.Close()
			if code := TestChatConnection(context.Background(), server.Client(), ChatEndpoint{BaseURL: server.URL + "/v1", ModelName: "test", APIKey: "test-only"}); code != test.want {
				t.Fatalf("code = %q, want %q", code, test.want)
			}
		})
	}
}

func TestChatConnectionDoesNotRedirectSecrets(t *testing.T) {
	var calls atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }))
	defer target.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer server.Close()
	code := TestChatConnection(context.Background(), server.Client(), ChatEndpoint{BaseURL: server.URL, ModelName: "test", APIKey: "test-only"})
	if code != "connection_failed" || calls.Load() != 0 {
		t.Fatalf("code = %q, redirected requests = %d", code, calls.Load())
	}
}

func TestChatConnectionRejectsUnsafeURLsAndCancellation(t *testing.T) {
	for _, address := range []string{"file:///private", "https://user:secret@example.com", "https://example.com?key=private", "https://example.com#fragment"} {
		if code := TestChatConnection(context.Background(), nil, ChatEndpoint{BaseURL: address, ModelName: "test"}); code != "invalid_config" {
			t.Fatalf("code = %q", code)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if code := TestChatConnection(ctx, nil, ChatEndpoint{BaseURL: "http://127.0.0.1:1", ModelName: "test"}); code != "connection_failed" {
		t.Fatalf("cancelled request code = %q", code)
	}
}
