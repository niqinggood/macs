package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"time"

	"mac/internal/modelruntime"
)

// PlatformAccountSession is an in-memory binding to the submitting account.
// It deliberately exposes neither a serializable token nor a replacement user.
type PlatformAccountSession struct {
	baseURL string
	token   func() (string, error)
}

// PlatformAccountSubject is stable across logins but never exposes a credential.
func (h *AccountHandler) PlatformAccountSubject(r *http.Request) string {
	_, session := h.accountSessionFromRequest(r)
	if session == nil {
		return ""
	}
	base, err := h.PlatformModelBaseURL()
	if err != nil {
		return ""
	}
	sum := sha256.Sum256([]byte(base + "\x00" + session.Username))
	return hex.EncodeToString(sum[:])
}

func (s *PlatformAccountSession) Context(ctx context.Context) context.Context {
	if s == nil {
		return ctx
	}
	return modelruntime.WithPlatformAccount(ctx, s.baseURL, s.token)
}

func (h *AccountHandler) CapturePlatformAccount(r *http.Request) (*PlatformAccountSession, error) {
	id, original := h.accountSessionFromRequest(r)
	if original == nil {
		return nil, errors.New("请先登录平台账号，再使用平台模型")
	}
	baseURL, err := h.PlatformModelBaseURL()
	if err != nil {
		return nil, errors.New("账号平台地址尚未正确配置")
	}
	return &PlatformAccountSession{baseURL: baseURL, token: func() (string, error) {
		current, ok := h.sessions.Load(id)
		if !ok || current != original || time.Now().After(original.ExpiresAt) || original.ProviderToken == "" {
			return "", errors.New("提交任务时的登录已失效，请重新登录后提交；任务不会改用其他账号扣费")
		}
		return original.ProviderToken, nil
	}}, nil
}
