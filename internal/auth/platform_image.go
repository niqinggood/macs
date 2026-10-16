package auth

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ReadPlatformImage reads a saved platform upload without sending credentials.
// The host must first authorize the persisted message and its workspace; callers
// cannot supply a URL through a public query or request body.
func (h *AccountHandler) ReadPlatformImage(parent context.Context, savedURL string) ([]byte, string, string) {
	base, err := h.PlatformModelBaseURL()
	if err != nil {
		return nil, "", "platform_unavailable"
	}
	origin, err := url.Parse(base)
	if err != nil {
		return nil, "", "platform_unavailable"
	}
	target, err := url.Parse(savedURL)
	if err != nil || target.User != nil || target.Scheme != origin.Scheme || target.Host != origin.Host || !strings.HasPrefix(target.Path, "/uploads/") || strings.Contains(target.Path, "..") || strings.Contains(target.Path, "\\") || target.RawQuery != "" || target.Fragment != "" {
		return nil, "", "reference_unavailable"
	}
	ctx, cancel := context.WithTimeout(parent, 20*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return nil, "", "reference_unavailable"
	}
	client := &http.Client{Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(request)
	if err != nil {
		return nil, "", "reference_unavailable"
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, (20<<20)+1))
	mime := http.DetectContentType(data)
	if err != nil || response.StatusCode != http.StatusOK || len(data) == 0 || len(data) > 20<<20 || !map[string]bool{"image/png": true, "image/jpeg": true, "image/webp": true, "image/gif": true}[mime] {
		return nil, "", "invalid_image"
	}
	return data, mime, ""
}
