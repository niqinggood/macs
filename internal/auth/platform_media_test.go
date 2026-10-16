package auth

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func mediaFixture(t *testing.T, upstream http.HandlerFunc) (*AccountHandler, *http.Cookie, *atomic.Int32) {
	t.Helper()
	t.Setenv("OPENKITTY_ACCOUNT_SERVICE_URL", "")
	calls := &atomic.Int32{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		cookie, err := r.Cookie("auth_token")
		if err != nil || cookie.Value != "media-owner-token" || r.Header.Get("Authorization") != "" {
			t.Error("wrong account credential forwarded")
		}
		upstream(w, r)
	}))
	t.Cleanup(server.Close)
	h := NewAccountHandler(AccountConfig{ServiceURL: server.URL})
	h.sessions.Store("media-local-session", &accountSession{Username: "owner", ProviderToken: "media-owner-token", ExpiresAt: time.Now().Add(time.Hour)})
	return h, &http.Cookie{Name: accountSessionCookieName, Value: "media-local-session"}, calls
}

func mediaRequest(h *AccountHandler, cookie *http.Cookie, method, path, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "http://localhost:8082"+path, strings.NewReader(body))
	if cookie != nil {
		r.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	h.HandlePlatformMedia(w, r)
	return w
}

func TestPlatformMediaSubmitsSelectedModelWithoutAgentOrCredentials(t *testing.T) {
	for _, kind := range []string{"image", "music"} {
		t.Run(kind, func(t *testing.T) {
			h, cookie, calls := mediaFixture(t, func(w http.ResponseWriter, r *http.Request) {
				want := "/api/macs/v1/creation/generate"
				if kind == "music" {
					want = "/api/macs/v1/music/jobs"
				}
				if r.URL.Path != want || r.Method != http.MethodPost {
					t.Errorf("unexpected endpoint %s", r.URL.Path)
				}
				var payload map[string]interface{}
				_ = json.NewDecoder(r.Body).Decode(&payload)
				if payload["model_config_ref"] != "platform:7" || payload["api_key"] != nil || payload["user_id"] != nil || payload["cost"] != nil {
					t.Errorf("unsafe generation payload: %v", payload)
				}
				if kind == "image" {
					w.WriteHeader(202)
					_, _ = io.WriteString(w, `{"status":"success","task":{"id":42,"capability":"image","status":"queued","cost":3,"api_key":"private","provider_token":"media-owner-token"}}`)
				} else {
					if payload["generation_mode"] != "generate_audio" || payload["lyrics_density"] != "none" {
						t.Error("instrumental mode invoked lyric drafting")
					}
					_, _ = io.WriteString(w, `{"status":"success","task_id":42,"cost":0,"api_key":"private"}`)
				}
			})
			response := mediaRequest(h, cookie, http.MethodPost, "/api/platform/media/"+kind, `{"model":"platform:7","prompt":"calm sea","instrumental":true}`)
			if response.Code < 200 || response.Code > 202 || !strings.Contains(response.Body.String(), `"id":42`) || !strings.Contains(response.Body.String(), `"status":"queued"`) {
				t.Fatalf("invalid submit response %d %s", response.Code, response.Body.String())
			}
			if calls.Load() != 1 || strings.Contains(response.Body.String(), "private") || strings.Contains(response.Body.String(), "media-owner-token") {
				t.Fatal("generation replayed or exposed credentials")
			}
		})
	}
}

func TestPlatformMediaRejectsForeignOriginCredentialsAndArbitraryTargets(t *testing.T) {
	h, cookie, calls := mediaFixture(t, func(w http.ResponseWriter, r *http.Request) { t.Error("invalid request contacted platform") })
	for _, test := range []struct {
		method, path, body, origin string
		cookie                     bool
		status                     int
	}{
		{"POST", "/api/platform/media/image", `{"model":"platform:7","prompt":"sea"}`, "https://untrusted.example", true, 403},
		{"POST", "/api/platform/media/image", `{"model":"platform:7","prompt":"sea"}`, "", false, 401},
		{"POST", "/api/platform/media/image", `{"model":"platform:7","prompt":"sea","url":"http://localhost/private"}`, "", true, 400},
		{"POST", "/api/platform/media/image", `{"model":"platform:7","prompt":"sea","api_key":"forged"}`, "", true, 400},
		{"POST", "/api/platform/media/image", `{"model":"user:7","prompt":"sea"}`, "", true, 400},
		{"GET", "/api/platform/media/image/../wallet", "", "", true, 400},
		{"GET", "/api/platform/media/image/7?username=other", "", "", true, 400},
		{"POST", "/api/platform/media/music/7", "{}", "", true, 400},
		{"DELETE", "/api/platform/media/image", "", "", true, 405},
	} {
		r := httptest.NewRequest(test.method, "http://localhost:8082"+test.path, strings.NewReader(test.body))
		r.Header.Set("Origin", test.origin)
		if test.cookie {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		h.HandlePlatformMedia(w, r)
		if w.Code != test.status {
			t.Fatalf("%s %s status=%d want=%d", test.method, test.path, w.Code, test.status)
		}
	}
	if calls.Load() != 0 {
		t.Fatal("boundary rejection called platform")
	}
}

func TestPlatformMediaNormalizesResultsAndAuthenticatedAudioRange(t *testing.T) {
	h, cookie, _ := mediaFixture(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/macs/v1/creation/tasks":
			_, _ = io.WriteString(w, `{"status":"success","tasks":[{"id":1,"capability":"image","status":"completed","result_url":"javascript:alert(1)"},{"id":2,"capability":"video","status":"completed"}]}`)
		case "/api/macs/v1/music/jobs/7":
			_, _ = io.WriteString(w, `{"status":"completed","task_id":7,"payment_status":"free","result":{"audio_url":"https://provider.example/private?key=secret","lyrics":"hello"},"request":{"api_key":"secret"}}`)
		case "/api/macs/v1/music/jobs/7/audio":
			if r.Header.Get("Range") != "bytes=0-2" {
				t.Error("audio Range lost")
			}
			w.Header().Set("Content-Type", "audio/mpeg")
			w.Header().Set("Content-Range", "bytes 0-2/10")
			w.WriteHeader(206)
			_, _ = io.WriteString(w, "mp3")
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	})
	image := mediaRequest(h, cookie, "GET", "/api/platform/media/image", "")
	if strings.Contains(image.Body.String(), "javascript") || strings.Contains(image.Body.String(), `"id":2`) {
		t.Fatalf("unsafe image result %s", image.Body.String())
	}
	music := mediaRequest(h, cookie, "GET", "/api/platform/media/music/7", "")
	if music.Code != 200 || strings.Contains(music.Body.String(), "secret") || !strings.Contains(music.Body.String(), "/api/platform/media/music/7/audio") {
		t.Fatalf("unsafe music result %s", music.Body.String())
	}
	r := httptest.NewRequest("GET", "http://localhost:8082/api/platform/media/music/7/audio", nil)
	r.AddCookie(cookie)
	r.Header.Set("Range", "bytes=0-2")
	w := httptest.NewRecorder()
	h.HandlePlatformMedia(w, r)
	if w.Code != 206 || w.Body.String() != "mp3" || w.Header().Get("Content-Range") != "bytes 0-2/10" {
		t.Fatalf("audio response %d %s", w.Code, w.Body.String())
	}
}

func TestPlatformMediaNeverReplaysSubmissionAndExpiresUnauthorizedSession(t *testing.T) {
	for _, status := range []int{302, 401, 402, 404, 500} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			h, cookie, calls := mediaFixture(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Location", "/private")
				w.WriteHeader(status)
			})
			w := mediaRequest(h, cookie, "POST", "/api/platform/media/image", `{"model":"platform:7","prompt":"sea"}`)
			if calls.Load() != 1 || strings.Contains(w.Body.String(), "media-owner-token") {
				t.Fatal("submission replayed or leaked key")
			}
			if status == 401 {
				if _, session := h.accountSessionFromRequest(func() *http.Request { r := httptest.NewRequest("GET", "/", nil); r.AddCookie(cookie); return r }()); session != nil {
					t.Fatal("expired account retained session")
				}
			}
		})
	}
}

func TestPlatformMediaPropagatesCancellation(t *testing.T) {
	h, cookie, calls := mediaFixture(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		select {
		case <-r.Context().Done():
		case <-time.After(time.Second):
		}
	})
	r := httptest.NewRequest("POST", "http://localhost:8082/api/platform/media/image", strings.NewReader(`{"model":"platform:7","prompt":"sea"}`))
	r.AddCookie(cookie)
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Millisecond)
	defer cancel()
	r = r.WithContext(ctx)
	w := httptest.NewRecorder()
	h.HandlePlatformMedia(w, r)
	if calls.Load() != 1 || !strings.Contains(w.Body.String(), "submission_unknown") {
		t.Fatalf("uncertain submission %d %s", w.Code, w.Body.String())
	}
}
