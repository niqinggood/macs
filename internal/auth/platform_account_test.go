package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"mac/internal/modelruntime"
)

func TestPlatformAccountBindingExpiresAndCannotSwitchUsers(t *testing.T) {
	t.Setenv("OPENKITTY_ACCOUNT_SERVICE_URL", "")
	h := NewAccountHandler(AccountConfig{ServiceURL: "https://platform.example"})
	owner := &accountSession{Username: "alice", ProviderToken: "alice-fixture-token", ExpiresAt: time.Now().Add(time.Hour)}
	h.sessions.Store("local-login", owner)
	r := httptest.NewRequest("POST", "http://localhost/api/chat", nil)
	r.AddCookie(&http.Cookie{Name: accountSessionCookieName, Value: "local-login"})
	binding, err := h.CapturePlatformAccount(r)
	if err != nil {
		t.Fatal(err)
	}
	ctx := binding.Context(context.Background())
	request := func() (*http.Request, error) {
		req, _ := http.NewRequestWithContext(ctx, "POST", "https://platform.example/api/macs/v1/chat/completions", nil)
		return req, modelruntime.AuthorizeModelRequest(req, "", true)
	}
	if req, err := request(); err != nil || req.Header.Get("Authorization") != "Bearer alice-fixture-token" {
		t.Fatal("submitting account missing", err)
	}
	h.sessions.Delete("local-login")
	if req, err := request(); err == nil || req.Header.Get("Authorization") != "" {
		t.Fatal("logout did not revoke future model calls")
	}
	h.sessions.Store("local-login", &accountSession{Username: "bob", ProviderToken: "bob-fixture-token", ExpiresAt: time.Now().Add(time.Hour)})
	if req, err := request(); err == nil || req.Header.Get("Authorization") != "" {
		t.Fatal("old task charged a replacement account")
	}
	owner.ExpiresAt = time.Now().Add(-time.Minute)
	h.sessions.Store("local-login", owner)
	if req, err := request(); err == nil || req.Header.Get("Authorization") != "" {
		t.Fatal("expired login used for a paid call")
	}
}
