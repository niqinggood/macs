package httpx

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestWebAssetHeadersDeclareUTF8JavaScript(t *testing.T) {
	handler := WithWebAssetHeaders(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("window.label = '系统设置';"))
	}))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/web/i18n.js", nil))
	if got := recorder.Header().Get("Content-Type"); got != "application/javascript; charset=utf-8" {
		t.Fatalf("JavaScript content type=%q", got)
	}
	if got := recorder.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("X-Content-Type-Options=%q", got)
	}
}
