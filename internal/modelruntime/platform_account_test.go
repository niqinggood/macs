package modelruntime

import (
	"context"
	"errors"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestPlatformAccountCredentialsStayAtBoundEndpoint(t *testing.T) {
	ctx := WithPlatformAccount(context.Background(), "https://platform.example/api/macs/v1", func() (string, error) { return "fixture-login-token", nil })
	for _, endpoint := range []string{"https://foreign.example/api/macs/v1/chat/completions", "http://platform.example/api/macs/v1/chat/completions", "https://platform.example/other/chat/completions", "https://platform.example/api/macs/v1/chat/completions?x=1"} {
		req, _ := http.NewRequestWithContext(ctx, "POST", endpoint, nil)
		if err := AuthorizeModelRequest(req, "", true); !errors.Is(err, ErrPlatformAddressMismatch) || req.Header.Get("Authorization") != "" {
			t.Fatalf("credential escaped to %s: %v", endpoint, err)
		}
	}
	req, _ := http.NewRequestWithContext(ctx, "POST", "https://platform.example/api/macs/v1/chat/completions", nil)
	req.Header.Set("Cookie", "auth_token=other-fixture-account")
	if err := AuthorizeModelRequest(req, "", true); err != nil || req.Header.Get("Authorization") != "Bearer fixture-login-token" || req.Header.Get("Cookie") != "" {
		t.Fatal("bound credential unavailable", err)
	}
	custom, _ := http.NewRequestWithContext(ctx, "POST", "https://custom.example/chat/completions", nil)
	if err := AuthorizeModelRequest(custom, "own-provider-key", false); err != nil || custom.Header.Get("Authorization") != "Bearer own-provider-key" {
		t.Fatal("login changed custom model credentials")
	}
}

func TestPlatformAccountCompletionRejectsMissingLoginAndRedirect(t *testing.T) {
	var calls, forwarded atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { forwarded.Add(1) }))
	defer target.Close()
	platform := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Authorization") != "Bearer fixture-login-token" || r.Header.Get("Idempotency-Key") == "" || r.Header.Get("Cookie") != "" {
			t.Error("login or billing call identity missing")
		}
		http.Redirect(w, r, target.URL+"/redirected", http.StatusTemporaryRedirect)
	}))
	defer platform.Close()
	config := CompletionConfig{APIURL: platform.URL + "/api/macs/v1/chat/completions", ModelName: "platform:1", AccountAuth: true}
	policy := ResolveCallPolicy(5, 1)
	messages := []map[string]interface{}{{"role": "user", "content": "hello"}}
	if _, err := CallMessages(context.Background(), nil, config, messages, 8, 0, policy); !errors.Is(err, ErrPlatformLoginRequired) || calls.Load() != 0 {
		t.Fatal("anonymous platform call executed", err)
	}
	ctx := WithPlatformAccount(context.Background(), platform.URL+"/api/macs/v1", func() (string, error) { return "fixture-login-token", nil })
	jar, _ := cookiejar.New(nil)
	request, _ := http.NewRequest("POST", config.APIURL, nil)
	jar.SetCookies(request.URL, []*http.Cookie{{Name: "auth_token", Value: "other-fixture-account"}})
	client := &http.Client{Jar: jar}
	if _, err := CallMessages(ctx, client, config, messages, 8, 0, policy); err == nil || calls.Load() != 1 || forwarded.Load() != 0 || client.Jar != jar {
		t.Fatalf("redirect followed: %v calls=%d forwarded=%d", err, calls.Load(), forwarded.Load())
	}
}
