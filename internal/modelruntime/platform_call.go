package modelruntime

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/url"
	"strings"
)

type platformCallKey struct{}

func IsPlatformModelURL(endpoint string) bool {
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return false
	}
	path := strings.TrimRight(parsed.Path, "/")
	return strings.HasSuffix(path, "/api/macs/v1") || strings.HasSuffix(path, "/api/macs/v1/chat/completions")
}

// One model round has one private nonce. Identical transport retries keep their
// key; protocol repairs with a different payload form a different paid call.
func WithPlatformCallIdentity(ctx context.Context, endpoint string) context.Context {
	if !IsPlatformModelURL(endpoint) {
		return ctx
	}
	return context.WithValue(ctx, platformCallKey{}, rand.Text())
}

func SetPlatformCallIdentity(req *http.Request, payload []byte) {
	if !IsPlatformModelURL(req.URL.String()) {
		return
	}
	nonce, _ := req.Context().Value(platformCallKey{}).(string)
	if nonce == "" {
		nonce = rand.Text()
	}
	hash := sha256.New()
	_, _ = hash.Write([]byte(nonce))
	_, _ = hash.Write(payload)
	req.Header.Set("Idempotency-Key", hex.EncodeToString(hash.Sum(nil)))
}
