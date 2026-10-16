package modelruntime

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
)

type platformAccountKey struct{}

var ErrPlatformLoginRequired = errors.New("请先登录平台账号，再使用平台模型")
var ErrPlatformAddressMismatch = errors.New("平台模型地址与登录服务不一致，请使用当前账号平台的模型")

type platformAccount struct {
	baseURL string
	token   func() (string, error)
}

// WithPlatformAccount binds one originating login session to a task. The
// resolver is checked on each model round; no credential is stored in config.
func WithPlatformAccount(ctx context.Context, baseURL string, token func() (string, error)) context.Context {
	return context.WithValue(ctx, platformAccountKey{}, platformAccount{baseURL: strings.TrimRight(baseURL, "/"), token: token})
}

func platformAccountToken(ctx context.Context, endpoint string) (string, error) {
	account, ok := ctx.Value(platformAccountKey{}).(platformAccount)
	if !ok || account.token == nil {
		return "", ErrPlatformLoginRequired
	}
	actual, err := url.Parse(endpoint)
	expected, baseErr := url.Parse(account.baseURL + "/chat/completions")
	if err != nil || baseErr != nil || actual.User != nil || actual.RawQuery != "" || actual.Fragment != "" ||
		actual.Scheme != expected.Scheme || !strings.EqualFold(actual.Host, expected.Host) || actual.EscapedPath() != expected.EscapedPath() {
		return "", ErrPlatformAddressMismatch
	}
	return account.token()
}

// AuthorizeModelRequest uses the task's own login or an explicitly configured
// API key. Login tokens may only go to the exact configured platform endpoint.
func AuthorizeModelRequest(req *http.Request, apiKey string, accountAuth bool) error {
	if accountAuth {
		token, err := platformAccountToken(req.Context(), req.URL.String())
		if err != nil {
			return err
		}
		if strings.TrimSpace(token) == "" {
			return errors.New("平台登录已失效，请重新登录")
		}
		req.Header.Del("Cookie")
		req.Header.Set("Authorization", "Bearer "+token)
	} else if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	return nil
}

// Account requests must not follow redirects, including redirects on the same
// host. Keep the caller's client unchanged for concurrent custom-model calls.
func ModelHTTPClient(client *http.Client, accountAuth bool) *http.Client {
	if client == nil {
		client = http.DefaultClient
	}
	if !accountAuth {
		return client
	}
	bounded := *client
	bounded.Jar = nil
	bounded.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	return &bounded
}
