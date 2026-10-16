package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func accountSummaryFixture(t *testing.T, walletStatus int, walletBody string, keyStatus int, keyBody string) (*AccountHandler, *http.Cookie, *atomic.Int32) {
	t.Helper()
	t.Setenv("OPENKITTY_ACCOUNT_SERVICE_URL", "")
	calls := &atomic.Int32{}
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path == "/nova/login" {
			http.SetCookie(w, &http.Cookie{Name: "auth_token", Value: "fixture-provider-token", HttpOnly: true})
			_, _ = w.Write([]byte(`{"status":"success","user":"fixture-user"}`))
			return
		}
		cookie, err := r.Cookie("auth_token")
		if err != nil || cookie.Value != "fixture-provider-token" {
			t.Error("provider request did not carry the logged-in user's credential")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/nova/wallet":
			w.WriteHeader(walletStatus)
			_, _ = w.Write([]byte(walletBody))
		case "/nova/wallet/api-keys":
			w.WriteHeader(keyStatus)
			_, _ = w.Write([]byte(keyBody))
		case "/nova/logout":
			_, _ = w.Write([]byte(`{"status":"success"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(provider.Close)
	handler := NewAccountHandler(AccountConfig{ServiceURL: provider.URL})
	login := httptest.NewRecorder()
	handler.HandleLogin(login, httptest.NewRequest(http.MethodPost, "/api/account/login", strings.NewReader(`{"username":"fixture-user","password":"fixture-password"}`)))
	if login.Code != http.StatusOK {
		t.Fatalf("login status %d: %s", login.Code, login.Body.String())
	}
	for _, cookie := range login.Result().Cookies() {
		if cookie.Name == accountSessionCookieName {
			if !cookie.HttpOnly || cookie.SameSite != http.SameSiteLaxMode {
				t.Fatal("local login cookie lost its security flags")
			}
			if strings.Contains(login.Body.String(), "fixture-provider-token") {
				t.Fatal("login exposed the provider token")
			}
			return handler, cookie, calls
		}
	}
	t.Fatal("login did not create a local account session")
	return nil, nil, nil
}

func TestAccountSummaryUsesLoginSessionAndExposesOnlyCounts(t *testing.T) {
	handler, cookie, calls := accountSummaryFixture(t, http.StatusOK, `{"status":"success","balance":126.5}`, http.StatusOK, `{"status":"success","api_keys":[{"is_active":true,"key":"fixture-private-key","secret":"fixture-private-secret"},{"is_active":false}]}`)
	request := httptest.NewRequest(http.MethodGet, "/api/account/summary?username=another-user", nil)
	request.AddCookie(cookie)
	response := httptest.NewRecorder()
	handler.HandleSummary(response, request)
	var payload struct {
		Authenticated bool             `json:"authenticated"`
		Balance       float64          `json:"balance"`
		Keys          accountKeyCounts `json:"api_keys"`
		User          struct {
			Username string `json:"username"`
		} `json:"user"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusOK || !payload.Authenticated || payload.User.Username != "fixture-user" || payload.Balance != 126.5 || payload.Keys.Active != 1 || payload.Keys.Total != 2 {
		t.Fatalf("unexpected summary: %s", response.Body.String())
	}
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("account data must not be cached")
	}
	for _, secret := range []string{"fixture-provider-token", "fixture-private-key", "fixture-private-secret"} {
		if strings.Contains(response.Body.String(), secret) {
			t.Fatal("summary exposed a provider credential")
		}
	}
	logout := httptest.NewRequest(http.MethodPost, "/api/account/logout", nil)
	logout.AddCookie(cookie)
	handler.HandleLogout(httptest.NewRecorder(), logout)
	before := calls.Load()
	response = httptest.NewRecorder()
	handler.HandleSummary(response, request)
	if response.Code != http.StatusUnauthorized || calls.Load() != before {
		t.Fatal("logged-out session must not read platform account data")
	}
}

func TestAccountSummaryPartialFailuresAndZeroBalance(t *testing.T) {
	for _, test := range []struct {
		name, walletBody, keyBody string
		walletStatus, keyStatus   int
		balance, keys             string
	}{
		{"zero", `{"status":"success","balance":0}`, `{"status":"success","api_keys":[]}`, 200, 200, `"balance":0`, `"api_keys":{"active":0,"total":0}`},
		{"omitted zero", `{"status":"success","payment_methods":[]}`, `{"status":"success","api_keys":[]}`, 200, 200, `"balance":0`, `"api_keys":{"active":0,"total":0}`},
		{"incomplete wallet", `{"status":"success"}`, `{"status":"success","api_keys":[]}`, 200, 200, `"balance":null`, `"api_keys":{"active":0,"total":0}`},
		{"wallet unavailable", `{}`, `{"status":"success","api_keys":[]}`, 503, 200, `"balance":null`, `"api_keys":{"active":0,"total":0}`},
		{"keys unavailable", `{"status":"success","balance":42}`, `{}`, 200, 503, `"balance":42`, `"api_keys":null`},
		{"invalid wallet", `invalid JSON`, `{"status":"success","api_keys":[]}`, 200, 200, `"balance":null`, `"api_keys":{"active":0,"total":0}`},
		{"oversize wallet", `{"status":"success","balance":42}` + strings.Repeat(" ", int(accountResponseBodyLimit)), `{"status":"success","api_keys":[]}`, 200, 200, `"balance":null`, `"api_keys":{"active":0,"total":0}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			handler, cookie, _ := accountSummaryFixture(t, test.walletStatus, test.walletBody, test.keyStatus, test.keyBody)
			request := httptest.NewRequest(http.MethodGet, "/api/account/summary", nil)
			request.AddCookie(cookie)
			response := httptest.NewRecorder()
			handler.HandleSummary(response, request)
			if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), test.balance) || !strings.Contains(response.Body.String(), test.keys) {
				t.Fatalf("unexpected summary: %d %s", response.Code, response.Body.String())
			}
		})
	}
}

func TestAccountSummaryRejectsInvalidSessionOriginAndMethod(t *testing.T) {
	handler, cookie, calls := accountSummaryFixture(t, 200, `{}`, 200, `{}`)
	for _, test := range []struct {
		name, method, origin string
		cookie               *http.Cookie
		status               int
	}{
		{"missing session", http.MethodGet, "", nil, http.StatusUnauthorized},
		{"forged session", http.MethodGet, "", &http.Cookie{Name: accountSessionCookieName, Value: "forged"}, http.StatusUnauthorized},
		{"other origin", http.MethodGet, "http://other.example", cookie, http.StatusForbidden},
		{"wrong method", http.MethodPost, "", cookie, http.StatusMethodNotAllowed},
	} {
		t.Run(test.name, func(t *testing.T) {
			before := calls.Load()
			request := httptest.NewRequest(test.method, "/api/account/summary", nil)
			if test.cookie != nil {
				request.AddCookie(test.cookie)
			}
			request.Header.Set("Origin", test.origin)
			response := httptest.NewRecorder()
			handler.HandleSummary(response, request)
			if response.Code != test.status || calls.Load() != before {
				t.Fatalf("request reached provider or wrong status: %d", response.Code)
			}
		})
	}
}

func TestAccountSummaryProviderExpiryInvalidatesLocalSession(t *testing.T) {
	handler, cookie, calls := accountSummaryFixture(t, 401, `{}`, 200, `{"status":"success","api_keys":[]}`)
	request := httptest.NewRequest(http.MethodGet, "/api/account/summary", nil)
	request.AddCookie(cookie)
	response := httptest.NewRecorder()
	handler.HandleSummary(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("expired platform session: %d %s", response.Code, response.Body.String())
	}
	before := calls.Load()
	handler.HandleSummary(httptest.NewRecorder(), request)
	if calls.Load() != before {
		t.Fatal("expired session was reused")
	}
}

func TestAccountSummaryCancellationAndRedirectBoundary(t *testing.T) {
	handler, cookie, _ := accountSummaryFixture(t, 200, `{}`, 200, `{}`)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	request := httptest.NewRequest(http.MethodGet, "/api/account/summary", nil).WithContext(ctx)
	request.AddCookie(cookie)
	started := time.Now()
	handler.HandleSummary(httptest.NewRecorder(), request)
	if time.Since(started) > time.Second {
		t.Fatal("summary did not honor request cancellation")
	}
	var targetCalls atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { targetCalls.Add(1) }))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer redirect.Close()
	handler.config.ServiceURL = redirect.URL
	request = httptest.NewRequest(http.MethodGet, "/api/account/summary", nil)
	request.AddCookie(cookie)
	response := httptest.NewRecorder()
	handler.HandleSummary(response, request)
	if targetCalls.Load() != 0 || !strings.Contains(response.Body.String(), `"balance":null`) {
		t.Fatal("account credentials must not follow provider redirects")
	}
}
