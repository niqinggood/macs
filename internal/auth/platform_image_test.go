package auth

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestReadPlatformImageUsesSavedOriginAndNoCredentials(t *testing.T) {
	t.Setenv("OPENKITTY_ACCOUNT_SERVICE_URL", "")
	var calls atomic.Int32
	image, _ := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+/l9kAAAAASUVORK5CYII=")
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" {
			t.Error("asset read sent credentials")
		}
		if r.URL.Path == "/uploads/redirect.png" {
			http.Redirect(w, r, "/uploads/result.png", http.StatusFound)
			return
		}
		if r.URL.Path == "/uploads/invalid.png" {
			w.Write([]byte("not an image"))
			return
		}
		w.Write(image)
	}))
	defer provider.Close()
	handler := NewAccountHandler(AccountConfig{ServiceURL: provider.URL})
	data, mime, code := handler.ReadPlatformImage(context.Background(), provider.URL+"/uploads/result.png")
	if code != "" || mime != "image/png" || string(data) != string(image) {
		t.Fatal("valid image read failed", code, mime)
	}
	for _, path := range []string{"/uploads/redirect.png", "/uploads/invalid.png"} {
		_, _, code := handler.ReadPlatformImage(context.Background(), provider.URL+path)
		if code == "" {
			t.Fatal("redirect or invalid image allowed")
		}
	}
	before := calls.Load()
	for _, target := range []string{"https://untrusted.example/uploads/result.png", provider.URL + "/uploads/../private.png", provider.URL + "/uploads/%2e%2e/private.png", provider.URL + "/private.png", provider.URL + "/uploads/result.png?token=fixture", strings.Replace(provider.URL, "://", "://fixture@", 1) + "/uploads/result.png"} {
		_, _, code := handler.ReadPlatformImage(context.Background(), target)
		if code == "" {
			t.Fatal("unsafe asset URL accepted")
		}
	}
	if calls.Load() != before {
		t.Fatal("unsafe URL reached network or redirect was followed")
	}
}
