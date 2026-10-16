package modelruntime

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestProbeChatCompletion(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer secret" {
			t.Fatalf("Authorization = %q", request.Header.Get("Authorization"))
		}
		if !strings.HasSuffix(request.URL.Path, "/chat/completions") {
			t.Fatalf("path = %q", request.URL.Path)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	err := ProbeChatCompletion(context.Background(), server.Client(), ChatEndpoint{APIKey: "secret", ModelName: "test-model", BaseURL: server.URL + "/"})
	if err != nil {
		t.Fatalf("ProbeChatCompletion: %v", err)
	}
}

func TestProbeChatCompletionRejectsHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusUnauthorized) }))
	defer server.Close()
	if err := ProbeChatCompletion(context.Background(), server.Client(), ChatEndpoint{BaseURL: server.URL}); err == nil {
		t.Fatal("HTTP error was accepted")
	}
}
