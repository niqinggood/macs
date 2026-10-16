package modelruntime

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestLocalModelAuthenticationIsConsistentAcrossCallsAndHealth(t *testing.T) {
	for _, key := range []string{"", "local-token"} {
		t.Run(key, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				header, exists := r.Header["Authorization"]
				if key == "" && exists {
					t.Errorf("anonymous request carried an Authorization header: %v", header)
				}
				if key != "" && r.Header.Get("Authorization") != "Bearer "+key {
					t.Error("configured local credential was not forwarded")
				}
				w.Header().Set("Content-Type", "application/json")
				io.WriteString(w, `{"choices":[{"message":{"content":"ok"}}]}`)
			}))
			defer server.Close()
			_, err := CallMessages(context.Background(), server.Client(),
				CompletionConfig{APIURL: server.URL + "/chat/completions", APIKey: key, ModelName: "local", APIKeyOptional: true},
				[]map[string]interface{}{{"role": "user", "content": "test"}}, 32, 0,
				CallPolicy{AttemptTimeout: time.Second, MaxAttempts: 1})
			if err != nil {
				t.Fatal(err)
			}
			if err := ProbeChatCompletion(context.Background(), server.Client(), ChatEndpoint{BaseURL: server.URL, APIKey: key, ModelName: "local"}); err != nil {
				t.Fatal(err)
			}
			if calls != 2 {
				t.Fatalf("provider calls=%d, want 2", calls)
			}
		})
	}
}
