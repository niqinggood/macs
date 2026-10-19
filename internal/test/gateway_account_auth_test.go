package app

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestAccountAuthProxyLoginMeAndLogout(t *testing.T) {
	var logoutCalls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/nova/login":
			var body map[string]string
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode provider login: %v", err)
			}
			if body["username"] != "test-user" || body["password"] != "test-password" {
				http.Error(w, `{"status":"error","message":"bad credentials"}`, http.StatusUnauthorized)
				return
			}
			http.SetCookie(w, &http.Cookie{Name: "auth_token", Value: "provider-secret", Path: "/", HttpOnly: true})
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"status":"success","user":"test-user"}`))
		case "/nova/logout":
			cookie, err := r.Cookie("auth_token")
			if err != nil || cookie.Value != "provider-secret" {
				t.Fatalf("provider logout cookie = %#v, err = %v", cookie, err)
			}
			logoutCalls.Add(1)
			_, _ = w.Write([]byte(`{"status":"success"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer provider.Close()

	gateway := &GatewayService{config: &Config{Gateway: GatewayConfig{
		AccountServiceURL:  provider.URL,
		AccountRegisterURL: "https://sicurrent.example/register",
	}}}

	loginRequest := httptest.NewRequest(http.MethodPost, "/api/account/login", bytes.NewBufferString(`{"username":"test-user","password":"test-password"}`))
	loginRecorder := httptest.NewRecorder()
	gateway.handleAccountLogin(loginRecorder, loginRequest)
	if loginRecorder.Code != http.StatusOK {
		t.Fatalf("login status = %d, body = %s", loginRecorder.Code, loginRecorder.Body.String())
	}
	var loginPayload struct {
		OK   bool `json:"ok"`
		User struct {
			Username string `json:"username"`
		} `json:"user"`
	}
	if err := json.Unmarshal(loginRecorder.Body.Bytes(), &loginPayload); err != nil {
		t.Fatalf("decode login response: %v", err)
	}
	if !loginPayload.OK || loginPayload.User.Username != "test-user" {
		t.Fatalf("unexpected login payload: %#v", loginPayload)
	}

	var sessionCookie *http.Cookie
	for _, cookie := range loginRecorder.Result().Cookies() {
		if cookie.Name == accountSessionCookieName {
			sessionCookie = cookie
			break
		}
	}
	if sessionCookie == nil {
		t.Fatal("login did not set the OpenKitty account session cookie")
	}
	if !sessionCookie.HttpOnly || sessionCookie.SameSite != http.SameSiteLaxMode {
		t.Fatalf("session cookie security flags = HttpOnly:%v SameSite:%v", sessionCookie.HttpOnly, sessionCookie.SameSite)
	}
	if strings.Contains(loginRecorder.Body.String(), "provider-secret") {
		t.Fatal("provider token must never be exposed to the browser")
	}

	meRequest := httptest.NewRequest(http.MethodGet, "/api/account/me", nil)
	meRequest.AddCookie(sessionCookie)
	meRecorder := httptest.NewRecorder()
	gateway.handleAccountMe(meRecorder, meRequest)
	if meRecorder.Code != http.StatusOK || !strings.Contains(meRecorder.Body.String(), `"authenticated":true`) || !strings.Contains(meRecorder.Body.String(), `"username":"test-user"`) {
		t.Fatalf("me status = %d, body = %s", meRecorder.Code, meRecorder.Body.String())
	}

	logoutRequest := httptest.NewRequest(http.MethodPost, "/api/account/logout", bytes.NewBufferString(`{}`))
	logoutRequest.AddCookie(sessionCookie)
	logoutRecorder := httptest.NewRecorder()
	gateway.handleAccountLogout(logoutRecorder, logoutRequest)
	if logoutRecorder.Code != http.StatusOK || logoutCalls.Load() != 1 {
		t.Fatalf("logout status = %d, provider calls = %d, body = %s", logoutRecorder.Code, logoutCalls.Load(), logoutRecorder.Body.String())
	}

	meAfterLogoutRequest := httptest.NewRequest(http.MethodGet, "/api/account/me", nil)
	meAfterLogoutRequest.AddCookie(sessionCookie)
	meAfterLogoutRecorder := httptest.NewRecorder()
	gateway.handleAccountMe(meAfterLogoutRecorder, meAfterLogoutRequest)
	if meAfterLogoutRecorder.Code != http.StatusUnauthorized {
		t.Fatalf("me after logout status = %d, body = %s", meAfterLogoutRecorder.Code, meAfterLogoutRecorder.Body.String())
	}
}

func TestAccountAuthProxyRejectsInvalidCredentials(t *testing.T) {
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"status":"error","message":"账号或密码错误"}`))
	}))
	defer provider.Close()

	gateway := &GatewayService{config: &Config{Gateway: GatewayConfig{AccountServiceURL: provider.URL}}}
	request := httptest.NewRequest(http.MethodPost, "/api/account/login", bytes.NewBufferString(`{"username":"test-user","password":"wrong"}`))
	recorder := httptest.NewRecorder()
	gateway.handleAccountLogin(recorder, request)
	if recorder.Code != http.StatusUnauthorized || !strings.Contains(recorder.Body.String(), "账号或密码错误") {
		t.Fatalf("login status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	for _, cookie := range recorder.Result().Cookies() {
		if cookie.Name == accountSessionCookieName && cookie.Value != "" {
			t.Fatal("invalid login must not create a session cookie")
		}
	}
}

func TestAccountConfigReturnsExternalRegistrationURL(t *testing.T) {
	gateway := &GatewayService{config: &Config{Gateway: GatewayConfig{
		AccountServiceURL:  "https://sicurrent.example",
		AccountRegisterURL: "https://sicurrent.example/register",
	}}}
	request := httptest.NewRequest(http.MethodGet, "/api/account/config", nil)
	recorder := httptest.NewRecorder()
	gateway.handleAccountConfig(recorder, request)
	if recorder.Code != http.StatusOK ||
		!strings.Contains(recorder.Body.String(), `"register_url":"https://sicurrent.example/register"`) ||
		!strings.Contains(recorder.Body.String(), `"account_center_url":"https://sicurrent.example/login/"`) ||
		!strings.Contains(recorder.Body.String(), `"login_url":"https://sicurrent.example/nova/login"`) ||
		!strings.Contains(recorder.Body.String(), `"check_login_url":"https://sicurrent.example/nova/check-login"`) ||
		!strings.Contains(recorder.Body.String(), `"logout_url":"https://sicurrent.example/nova/logout"`) ||
		recorder.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("config status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
}

func TestAccountCenterURLCanBeConfiguredExplicitly(t *testing.T) {
	t.Setenv("OPENKITTY_ACCOUNT_CENTER_URL", "")
	config := &Config{Gateway: GatewayConfig{
		AccountServiceURL: "https://www.sicurrent.com/nova/login",
		AccountCenterURL:  "https://sicurrent.com/account/",
	}}
	if got := accountCenterURL(config); got != "https://sicurrent.com/account/" {
		t.Fatalf("account center URL = %q", got)
	}
}

func TestDefaultAccountServiceUsesNovaProductionOrigin(t *testing.T) {
	t.Setenv("OPENKITTY_ACCOUNT_SERVICE_URL", "")
	loginURL, err := accountServiceEndpointURL(nil, "/nova/login")
	if err != nil {
		t.Fatalf("default account login URL: %v", err)
	}
	if loginURL != "https://www.sicurrent.com/nova/login" {
		t.Fatalf("default account login URL = %q", loginURL)
	}
}

func TestAccountServiceEndpointAcceptsAFullLoginURL(t *testing.T) {
	config := &Config{Gateway: GatewayConfig{AccountServiceURL: "https://www.sicurrent.com/nova/login"}}
	checkURL, err := accountServiceEndpointURL(config, "/nova/check-login")
	if err != nil {
		t.Fatalf("derive check-login URL: %v", err)
	}
	if checkURL != "https://www.sicurrent.com/nova/check-login" {
		t.Fatalf("derived check-login URL = %q", checkURL)
	}
}
