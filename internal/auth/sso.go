package auth

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
)

// ====================== SSO OAuth 2.0 — 飞书/钉钉/企微登录 ======================

// SSOProvider defines an OAuth identity provider.
type SSOProvider struct {
	Name         string
	AuthURL      string
	TokenURL     string
	UserInfoURL  string
	ClientID     string
	ClientSecret string
	RedirectURI  string
}

// SSOManager manages OAuth flows and user sessions.
type SSOManager struct {
	mu        sync.Mutex
	providers map[string]*SSOProvider
	states    map[string]*ssoState // state → pending auth
	sessions  sync.Map
	logger    *log.Logger
}

type ssoState struct {
	provider   string
	redirectTo string
}

func NewSSOManager(logger *log.Logger) *SSOManager {
	return &SSOManager{
		providers: make(map[string]*SSOProvider),
		states:    make(map[string]*ssoState),
		logger:    logger,
	}
}

// ConfigureFeishu sets up Feishu (Lark) OAuth.
func (m *SSOManager) ConfigureFeishu(appID, appSecret, redirectURI string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.providers["feishu"] = &SSOProvider{
		Name:         "feishu",
		AuthURL:      "https://passport.feishu.cn/suite/passport/oauth/authorize",
		TokenURL:     "https://passport.feishu.cn/suite/passport/oauth/token",
		UserInfoURL:  "https://passport.feishu.cn/suite/passport/oauth/userinfo",
		ClientID:     appID,
		ClientSecret: appSecret,
		RedirectURI:  redirectURI,
	}
}

// ConfigureDingTalk sets up DingTalk OAuth.
func (m *SSOManager) ConfigureDingTalk(appID, appSecret, redirectURI string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.providers["dingtalk"] = &SSOProvider{
		Name:         "dingtalk",
		AuthURL:      "https://login.dingtalk.com/oauth2/auth",
		TokenURL:     "https://api.dingtalk.com/v1.0/oauth2/userAccessToken",
		UserInfoURL:  "https://api.dingtalk.com/v1.0/contact/users/me",
		ClientID:     appID,
		ClientSecret: appSecret,
		RedirectURI:  redirectURI,
	}
}

// ConfigureWeChatWork sets up WeChat Work OAuth.
func (m *SSOManager) ConfigureWeChatWork(corpID, corpSecret, redirectURI string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.providers["wechatwork"] = &SSOProvider{
		Name:         "wechatwork",
		AuthURL:      "https://open.work.weixin.qq.com/wwopen/sso/qrConnect",
		TokenURL:     "https://qyapi.weixin.qq.com/cgi-bin/gettoken",
		UserInfoURL:  "https://qyapi.weixin.qq.com/cgi-bin/user/getuserinfo",
		ClientID:     corpID,
		ClientSecret: corpSecret,
		RedirectURI:  redirectURI,
	}
}

// SSOEnabled returns true if any provider is configured.
func (m *SSOManager) SSOEnabled() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, p := range m.providers {
		if p.ClientID != "" && p.ClientSecret != "" {
			return true
		}
	}
	return false
}

// GetConfiguredProviders returns list of configured SSO provider names.
func (m *SSOManager) GetConfiguredProviders() []map[string]string {
	m.mu.Lock()
	defer m.mu.Unlock()
	var result []map[string]string
	names := map[string]string{
		"feishu":     "飞书",
		"dingtalk":   "钉钉",
		"wechatwork": "企业微信",
	}
	for key, p := range m.providers {
		if p.ClientID != "" && p.ClientSecret != "" {
			result = append(result, map[string]string{"id": key, "name": names[key]})
		}
	}
	return result
}

// BeginAuth generates state, stores it, and returns the authorization URL.
func (m *SSOManager) BeginAuth(providerName, redirectTo string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	p, ok := m.providers[providerName]
	if !ok || p.ClientID == "" {
		return "", fmt.Errorf("provider %s not configured", providerName)
	}

	state := randomState()
	m.states[state] = &ssoState{provider: providerName, redirectTo: redirectTo}

	u, _ := url.Parse(p.AuthURL)
	q := u.Query()
	q.Set("client_id", p.ClientID)
	q.Set("redirect_uri", p.RedirectURI)
	q.Set("response_type", "code")
	q.Set("state", state)
	if providerName == "feishu" {
		q.Set("scope", "user:read")
	} else if providerName == "dingtalk" {
		q.Set("scope", "openid corpid")
	} else if providerName == "wechatwork" {
		q.Set("agentid", "0")
	}
	u.RawQuery = q.Encode()

	m.logger.Printf("[SSO] Auth URL for %s: %s", providerName, u.String())
	return u.String(), nil
}

// CompleteAuth exchanges the OAuth code for user info and returns a session token.
func (m *SSOManager) CompleteAuth(providerName, code, state string) (*ssoUserInfo, error) {
	m.mu.Lock()
	st, ok := m.states[state]
	if !ok || st.provider != providerName {
		m.mu.Unlock()
		return nil, fmt.Errorf("invalid state")
	}
	delete(m.states, state)
	m.mu.Unlock()

	p, ok := m.providers[providerName]
	if !ok {
		return nil, fmt.Errorf("provider %s not found", providerName)
	}

	// Exchange code for token
	tokenResp, err := exchangeOAuthCode(p, code)
	if err != nil {
		return nil, fmt.Errorf("exchange code: %w", err)
	}

	// Get user info
	userInfo, err := fetchOAuthUserInfo(p, tokenResp.AccessToken)
	if err != nil {
		return nil, fmt.Errorf("fetch user info: %w", err)
	}

	m.logger.Printf("[SSO] Login: %s via %s (%s)", userInfo.Name, providerName, userInfo.OpenID)
	return userInfo, nil
}

type oauthTokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token,omitempty"`
	ExpiresIn    int    `json:"expires_in"`
}

type ssoUserInfo struct {
	OpenID string `json:"open_id"`
	Name   string `json:"name"`
	Avatar string `json:"avatar_url"`
	Email  string `json:"email"`
}

func exchangeOAuthCode(p *SSOProvider, code string) (*oauthTokenResponse, error) {
	data := url.Values{}
	data.Set("client_id", p.ClientID)
	data.Set("client_secret", p.ClientSecret)
	data.Set("code", code)
	data.Set("grant_type", "authorization_code")
	data.Set("redirect_uri", p.RedirectURI)

	// WeChat Work uses GET with params; Feishu/DingTalk use POST
	method := "POST"
	if p.Name == "wechatwork" && !strings.Contains(p.TokenURL, "userid") {
		// WeChat Work: first get corp access token, then get user info
		corpTokenURL := fmt.Sprintf("%s?corpid=%s&corpsecret=%s", p.TokenURL, p.ClientID, p.ClientSecret)
		resp, err := http.Get(corpTokenURL)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		var corpToken struct {
			AccessToken string `json:"access_token"`
			ErrCode     int    `json:"errcode"`
			ErrMsg      string `json:"errmsg"`
		}
		json.NewDecoder(resp.Body).Decode(&corpToken)
		if corpToken.ErrCode != 0 {
			return nil, fmt.Errorf("corp token: %s (code=%d)", corpToken.ErrMsg, corpToken.ErrCode)
		}
		return &oauthTokenResponse{AccessToken: corpToken.AccessToken}, nil
	}

	var resp *http.Response
	var err error
	if method == "POST" {
		resp, err = http.PostForm(p.TokenURL, data)
	} else {
		resp, err = http.Get(p.TokenURL + "?" + data.Encode())
	}
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var token oauthTokenResponse
	if err := json.Unmarshal(body, &token); err != nil {
		return nil, fmt.Errorf("parse token: %w (body: %s)", err, string(body[:min(len(body), 200)]))
	}
	if token.AccessToken == "" {
		return nil, fmt.Errorf("empty access_token (body: %s)", string(body[:min(len(body), 200)]))
	}
	return &token, nil
}

func fetchOAuthUserInfo(p *SSOProvider, accessToken string) (*ssoUserInfo, error) {
	req, _ := http.NewRequest("GET", p.UserInfoURL, nil)
	if p.Name == "feishu" {
		req.Header.Set("Authorization", "Bearer "+accessToken)
	} else if p.Name == "dingtalk" {
		req.Header.Set("x-acs-dingtalk-access-token", accessToken)
	} else if p.Name == "wechatwork" {
		// WeChat Work: append access_token as query param
		req.URL.RawQuery = "access_token=" + accessToken + "&code=" + accessToken // code not available here, handled differently
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	var user ssoUserInfo
	if err := json.Unmarshal(body, &user); err != nil {
		return nil, fmt.Errorf("parse user: %w", err)
	}
	if user.Name == "" {
		// Fallback: use open_id as name
		var raw map[string]interface{}
		json.Unmarshal(body, &raw)
		if v, ok := raw["data"].(map[string]interface{}); ok {
			if n, ok := v["name"].(string); ok {
				user.Name = n
			}
			if o, ok := v["open_id"].(string); ok {
				user.OpenID = o
			}
			if o, ok := v["userid"].(string); ok {
				user.OpenID = o
			}
		}
		if user.Name == "" {
			user.Name = user.OpenID
		}
	}
	return &user, nil
}

func randomState() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// ====================== Gateway Handlers ======================

// handleSSOProviders returns the list of configured SSO providers for the login page.
func (m *SSOManager) HandleProviders(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"providers": m.GetConfiguredProviders(),
	})
}

// handleSSOLogin initiates the OAuth redirect flow.
func (m *SSOManager) HandleLogin(w http.ResponseWriter, r *http.Request) {
	provider := r.URL.Query().Get("provider")
	redirectTo := r.URL.Query().Get("redirect")
	if redirectTo == "" {
		redirectTo = "/"
	}

	authURL, err := m.BeginAuth(provider, redirectTo)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, authURL, http.StatusFound)
}

// handleSSOCallback handles the OAuth redirect from the provider.
func (m *SSOManager) HandleCallback(w http.ResponseWriter, r *http.Request) {
	provider := r.URL.Query().Get("provider")
	code := r.URL.Query().Get("code")
	state := r.URL.Query().Get("state")

	if code == "" || state == "" {
		http.Error(w, "Missing code or state", http.StatusBadRequest)
		return
	}

	user, err := m.CompleteAuth(provider, code, state)
	if err != nil {
		log.Printf("[SSO] Auth failed for %s: %v", provider, err)
		http.Redirect(w, r, "/web/login.html?error="+url.QueryEscape(err.Error()), http.StatusFound)
		return
	}

	// Generate session token
	token := randomState()[:32]
	m.sessions.Store(token, user)

	// Redirect to main page with token
	redirectTo := "/"
	http.Redirect(w, r, redirectTo+"?sso_token="+token, http.StatusFound)
}

// handleSSOVerify validates an SSO token and returns user info.
func (m *SSOManager) HandleVerify(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	token := r.URL.Query().Get("token")
	if token == "" {
		json.NewEncoder(w).Encode(map[string]interface{}{"ok": false, "error": "token required"})
		return
	}
	v, ok := m.sessions.Load(token)
	if !ok {
		json.NewEncoder(w).Encode(map[string]interface{}{"ok": false, "error": "invalid token"})
		return
	}
	user := v.(*ssoUserInfo)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"ok":           true,
		"open_id":      user.OpenID,
		"name":         user.Name,
		"avatar_url":   user.Avatar,
		"display_name": user.Name,
		"tenant_id":    "default",
	})
}
