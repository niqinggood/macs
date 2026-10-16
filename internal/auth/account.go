package auth

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"mac/internal/security"
)

const (
	accountSessionCookieName            = "openkitty_account_session"
	defaultAccountServiceURL            = "https://www.sicurrent.com"
	defaultAccountRegistrationURL       = "https://www.sicurrent.com/register"
	accountSessionLifetime              = 24 * time.Hour
	accountResponseBodyLimit      int64 = 1 << 20
)

type accountSession struct {
	Username      string
	ProviderToken string
	ExpiresAt     time.Time
}

type AccountConfig struct {
	ServiceURL  string
	RegisterURL string
	CenterURL   string
}

type AccountHandler struct {
	config   AccountConfig
	sessions sync.Map
	client   *http.Client
}

func NewAccountHandler(config AccountConfig) *AccountHandler {
	return &AccountHandler{config: config, client: &http.Client{
		Timeout: 15 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}}
}

func accountServiceURL(config AccountConfig) (string, error) {
	rawURL := strings.TrimSpace(os.Getenv("OPENKITTY_ACCOUNT_SERVICE_URL"))
	if rawURL == "" {
		rawURL = strings.TrimSpace(config.ServiceURL)
	}
	if rawURL == "" {
		rawURL = defaultAccountServiceURL
	}

	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return "", fmt.Errorf("invalid account service URL")
	}
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return strings.TrimRight(parsed.String(), "/"), nil
}

func accountRegistrationURL(config AccountConfig) string {
	if value := strings.TrimSpace(os.Getenv("OPENKITTY_ACCOUNT_REGISTER_URL")); value != "" {
		return value
	}
	if value := strings.TrimSpace(config.RegisterURL); value != "" {
		return value
	}
	return defaultAccountRegistrationURL
}

func accountCenterURL(config AccountConfig) string {
	rawURL := strings.TrimSpace(os.Getenv("OPENKITTY_ACCOUNT_CENTER_URL"))
	if rawURL == "" {
		rawURL = strings.TrimSpace(config.CenterURL)
	}
	if rawURL == "" {
		serviceURL, err := accountServiceURL(config)
		if err != nil {
			return ""
		}
		for _, knownEndpoint := range []string{"/nova/login", "/nova/check-login", "/nova/logout"} {
			if strings.HasSuffix(strings.ToLower(serviceURL), knownEndpoint) {
				serviceURL = serviceURL[:len(serviceURL)-len(knownEndpoint)]
				break
			}
		}
		rawURL = strings.TrimRight(serviceURL, "/") + "/login/"
	}

	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return ""
	}
	parsed.Fragment = ""
	return parsed.String()
}

func accountServiceEndpointURL(config AccountConfig, endpoint string) (string, error) {
	baseURL, err := accountServiceURL(config)
	if err != nil {
		return "", err
	}
	for _, knownEndpoint := range []string{"/nova/login", "/nova/check-login", "/nova/logout"} {
		if strings.HasSuffix(strings.ToLower(baseURL), knownEndpoint) {
			baseURL = baseURL[:len(baseURL)-len(knownEndpoint)]
			break
		}
	}
	endpoint = "/" + strings.TrimLeft(strings.TrimSpace(endpoint), "/")
	return strings.TrimRight(baseURL, "/") + endpoint, nil
}

// Discovery and direct generation must refer to the same account service.
// Configured custom chat endpoints do not change where a logged-in user pays.
func PlatformModelBaseURL(config AccountConfig) (string, error) {
	return accountServiceEndpointURL(config, "/api/macs/v1")
}

func (h *AccountHandler) PlatformModelBaseURL() (string, error) {
	return PlatformModelBaseURL(h.config)
}

func accountCookieSecure(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	forwardedProto := strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-Proto"), ",")[0])
	return strings.EqualFold(forwardedProto, "https")
}

func setAccountSessionCookie(w http.ResponseWriter, r *http.Request, token string, expiresAt time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name:     accountSessionCookieName,
		Value:    token,
		Path:     "/",
		Expires:  expiresAt,
		MaxAge:   int(time.Until(expiresAt).Seconds()),
		HttpOnly: true,
		Secure:   accountCookieSecure(r),
		SameSite: http.SameSiteLaxMode,
	})
}

func clearAccountSessionCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     accountSessionCookieName,
		Value:    "",
		Path:     "/",
		Expires:  time.Unix(1, 0),
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   accountCookieSecure(r),
		SameSite: http.SameSiteLaxMode,
	})
}

func writeAccountJSON(w http.ResponseWriter, status int, payload interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func (h *AccountHandler) accountSessionFromRequest(r *http.Request) (string, *accountSession) {
	cookie, err := r.Cookie(accountSessionCookieName)
	if err != nil || strings.TrimSpace(cookie.Value) == "" {
		return "", nil
	}
	value, ok := h.sessions.Load(cookie.Value)
	if !ok {
		return cookie.Value, nil
	}
	session, ok := value.(*accountSession)
	if !ok || session == nil || time.Now().After(session.ExpiresAt) {
		h.sessions.Delete(cookie.Value)
		return cookie.Value, nil
	}
	return cookie.Value, session
}

func (h *AccountHandler) HandleConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAccountJSON(w, http.StatusMethodNotAllowed, map[string]interface{}{"ok": false, "error": "method not allowed"})
		return
	}
	loginURL, err := accountServiceEndpointURL(h.config, "/nova/login")
	checkLoginURL, checkErr := accountServiceEndpointURL(h.config, "/nova/check-login")
	logoutURL, logoutErr := accountServiceEndpointURL(h.config, "/nova/logout")
	platformBaseURL, _ := h.PlatformModelBaseURL()
	w.Header().Set("Cache-Control", "no-store")
	writeAccountJSON(w, http.StatusOK, map[string]interface{}{
		"ok":                      true,
		"enabled":                 err == nil && checkErr == nil && logoutErr == nil,
		"login_url":               loginURL,
		"check_login_url":         checkLoginURL,
		"logout_url":              logoutURL,
		"register_url":            accountRegistrationURL(h.config),
		"account_center_url":      accountCenterURL(h.config),
		"local_login_url":         "/api/account/login",
		"local_me_url":            "/api/account/me",
		"local_logout_url":        "/api/account/logout",
		"summary_url":             "/api/account/summary",
		"platform_model_base_url": platformBaseURL,
	})
}

func (h *AccountHandler) HandleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAccountJSON(w, http.StatusMethodNotAllowed, map[string]interface{}{"ok": false, "error": "method not allowed"})
		return
	}

	var requestBody struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, accountResponseBodyLimit))
	if err := decoder.Decode(&requestBody); err != nil {
		writeAccountJSON(w, http.StatusBadRequest, map[string]interface{}{"ok": false, "error": "请输入有效的账号和密码"})
		return
	}
	requestBody.Username = strings.TrimSpace(requestBody.Username)
	if requestBody.Username == "" || requestBody.Password == "" {
		writeAccountJSON(w, http.StatusBadRequest, map[string]interface{}{"ok": false, "error": "请输入账号和密码"})
		return
	}

	loginURL, err := accountServiceEndpointURL(h.config, "/nova/login")
	if err != nil {
		writeAccountJSON(w, http.StatusServiceUnavailable, map[string]interface{}{"ok": false, "error": "账号服务尚未配置"})
		return
	}
	payload, _ := json.Marshal(requestBody)
	providerRequest, err := http.NewRequestWithContext(r.Context(), http.MethodPost, loginURL, bytes.NewReader(payload))
	if err != nil {
		writeAccountJSON(w, http.StatusBadGateway, map[string]interface{}{"ok": false, "error": "无法连接账号服务"})
		return
	}
	providerRequest.Header.Set("Content-Type", "application/json")
	providerRequest.Header.Set("Accept", "application/json")

	providerResponse, err := h.client.Do(providerRequest)
	if err != nil {
		writeAccountJSON(w, http.StatusBadGateway, map[string]interface{}{"ok": false, "error": "账号服务暂时不可用"})
		return
	}
	defer providerResponse.Body.Close()
	responseBody, _ := io.ReadAll(io.LimitReader(providerResponse.Body, accountResponseBodyLimit))
	var providerPayload struct {
		Status  string `json:"status"`
		Message string `json:"message"`
		Error   string `json:"error"`
		User    string `json:"user"`
	}
	_ = json.Unmarshal(responseBody, &providerPayload)
	if providerResponse.StatusCode < http.StatusOK || providerResponse.StatusCode >= http.StatusMultipleChoices || providerPayload.Status != "success" {
		message := strings.TrimSpace(providerPayload.Message)
		if message == "" {
			message = strings.TrimSpace(providerPayload.Error)
		}
		if message == "" {
			message = "账号或密码错误"
		}
		status := providerResponse.StatusCode
		if status < http.StatusBadRequest || status > 599 {
			status = http.StatusBadGateway
		}
		writeAccountJSON(w, status, map[string]interface{}{"ok": false, "error": message})
		return
	}

	providerToken := ""
	for _, cookie := range providerResponse.Cookies() {
		if cookie.Name == "auth_token" {
			providerToken = cookie.Value
			break
		}
	}
	if providerToken == "" {
		writeAccountJSON(w, http.StatusBadGateway, map[string]interface{}{"ok": false, "error": "账号服务未返回有效会话"})
		return
	}

	username := strings.TrimSpace(providerPayload.User)
	if username == "" {
		username = requestBody.Username
	}
	expiresAt := time.Now().Add(accountSessionLifetime)
	sessionToken := security.GenerateToken(48)
	h.sessions.Store(sessionToken, &accountSession{
		Username:      username,
		ProviderToken: providerToken,
		ExpiresAt:     expiresAt,
	})
	setAccountSessionCookie(w, r, sessionToken, expiresAt)
	writeAccountJSON(w, http.StatusOK, map[string]interface{}{
		"ok": true,
		"user": map[string]string{
			"username": username,
			"source":   "sicurrent",
		},
	})
}

func (h *AccountHandler) HandleMe(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodGet {
		writeAccountJSON(w, http.StatusMethodNotAllowed, map[string]interface{}{"ok": false, "error": "method not allowed"})
		return
	}
	_, session := h.accountSessionFromRequest(r)
	if session == nil {
		clearAccountSessionCookie(w, r)
		writeAccountJSON(w, http.StatusUnauthorized, map[string]interface{}{"ok": false, "authenticated": false})
		return
	}
	writeAccountJSON(w, http.StatusOK, map[string]interface{}{
		"ok":            true,
		"authenticated": true,
		"user": map[string]string{
			"username": session.Username,
			"source":   "sicurrent",
		},
	})
}

func (h *AccountHandler) HandleLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAccountJSON(w, http.StatusMethodNotAllowed, map[string]interface{}{"ok": false, "error": "method not allowed"})
		return
	}
	sessionToken, session := h.accountSessionFromRequest(r)
	if sessionToken != "" {
		h.sessions.Delete(sessionToken)
	}
	clearAccountSessionCookie(w, r)

	if session != nil && session.ProviderToken != "" {
		if logoutURL, err := accountServiceEndpointURL(h.config, "/nova/logout"); err == nil {
			providerRequest, requestErr := http.NewRequestWithContext(r.Context(), http.MethodPost, logoutURL, bytes.NewReader([]byte("{}")))
			if requestErr == nil {
				providerRequest.Header.Set("Content-Type", "application/json")
				providerRequest.AddCookie(&http.Cookie{Name: "auth_token", Value: session.ProviderToken})
				if providerResponse, requestErr := h.client.Do(providerRequest); requestErr == nil {
					_, _ = io.Copy(io.Discard, io.LimitReader(providerResponse.Body, accountResponseBodyLimit))
					_ = providerResponse.Body.Close()
				}
			}
		}
	}

	writeAccountJSON(w, http.StatusOK, map[string]interface{}{"ok": true})
}
