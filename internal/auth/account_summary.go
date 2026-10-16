package auth

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sync"
	"time"

	gatewayhttp "mac/internal/gateway/httpx"
)

type accountKeyCounts struct {
	Active int `json:"active"`
	Total  int `json:"total"`
}

// readAccountSummaryJSON keeps the provider credential on the server and decodes
// only the fields needed for the account card. Provider responses are never relayed.
func (h *AccountHandler) readAccountSummaryJSON(ctx context.Context, session *accountSession, endpoint string, payload interface{}) (bool, error) {
	providerURL, err := accountServiceEndpointURL(h.config, endpoint)
	if err != nil {
		return false, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, providerURL, nil)
	if err != nil {
		return false, err
	}
	request.Header.Set("Accept", "application/json")
	request.AddCookie(&http.Cookie{Name: "auth_token", Value: session.ProviderToken})
	response, err := h.client.Do(request)
	if err != nil {
		return false, err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusUnauthorized {
		return true, errors.New("account session expired")
	}
	if response.StatusCode != http.StatusOK {
		return false, errors.New("account summary unavailable")
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, accountResponseBodyLimit+1))
	if err != nil || int64(len(body)) > accountResponseBodyLimit {
		return false, errors.New("invalid account summary response")
	}
	return false, json.Unmarshal(body, payload)
}

// HandleSummary reads the authenticated user's wallet and API-key counts using
// the same server-side session created by HandleLogin.
func (h *AccountHandler) HandleSummary(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodGet {
		writeAccountJSON(w, http.StatusMethodNotAllowed, map[string]interface{}{"ok": false, "error": "method not allowed"})
		return
	}
	if !gatewayhttp.RequestHasNoOriginOrSameHost(r) {
		writeAccountJSON(w, http.StatusForbidden, map[string]interface{}{"ok": false, "error": "forbidden origin"})
		return
	}
	sessionToken, session := h.accountSessionFromRequest(r)
	if session == nil {
		clearAccountSessionCookie(w, r)
		writeAccountJSON(w, http.StatusUnauthorized, map[string]interface{}{"ok": false, "authenticated": false})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	var wallet struct {
		Status         string    `json:"status"`
		Balance        *float64  `json:"balance"`
		PaymentMethods *[]string `json:"payment_methods"`
	}
	var keys struct {
		Status string `json:"status"`
		Keys   *[]struct {
			Active bool `json:"is_active"`
		} `json:"api_keys"`
	}
	var walletErr, keysErr error
	var walletExpired, keysExpired bool
	var workers sync.WaitGroup
	workers.Add(2)
	go func() {
		defer workers.Done()
		walletExpired, walletErr = h.readAccountSummaryJSON(ctx, session, "/nova/wallet", &wallet)
	}()
	go func() {
		defer workers.Done()
		keysExpired, keysErr = h.readAccountSummaryJSON(ctx, session, "/nova/wallet/api-keys", &keys)
	}()
	workers.Wait()
	if walletExpired || keysExpired {
		h.sessions.Delete(sessionToken)
		clearAccountSessionCookie(w, r)
		writeAccountJSON(w, http.StatusUnauthorized, map[string]interface{}{"ok": false, "authenticated": false})
		return
	}
	var balance *float64
	if walletErr == nil && wallet.Status == "success" {
		balance = wallet.Balance
		// The platform WalletResponse omits a zero balance, while payment_methods
		// is always present. Other incomplete responses stay unavailable.
		if balance == nil && wallet.PaymentMethods != nil {
			zero := 0.0
			balance = &zero
		}
	}
	var keyCounts *accountKeyCounts
	if keysErr == nil && keys.Status == "success" && keys.Keys != nil {
		keyCounts = &accountKeyCounts{Total: len(*keys.Keys)}
		for _, key := range *keys.Keys {
			if key.Active {
				keyCounts.Active++
			}
		}
	}
	writeAccountJSON(w, http.StatusOK, map[string]interface{}{
		"ok":            true,
		"authenticated": true,
		"user":          map[string]string{"username": session.Username, "source": "sicurrent"},
		"balance":       balance,
		"api_keys":      keyCounts,
		"partial":       balance == nil || keyCounts == nil,
	})
}
