package auth

import (
	"bytes"
	"encoding/base64"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPlatformMediaVideoReferenceModesAndBoundary(t *testing.T) {
	h, cookie, calls := mediaFixture(t, func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(data), `"mode":"image_to_video"`) || !strings.Contains(string(data), `"duration":6`) || !strings.Contains(string(data), `"reference_images":["/uploads/ai-creation/references/1/ref.png"]`) {
			t.Errorf("wrong video payload %s", data)
		}
		_, _ = io.WriteString(w, `{"status":"success","task":{"id":5,"capability":"video","status":"queued"}}`)
	})
	valid := `{"model":"platform:5","prompt":"sea","duration":6,"resolution":"720p","reference_images":["/uploads/ai-creation/references/1/ref.png"]}`
	w := mediaRequest(h, cookie, "POST", "/api/platform/media/video", valid)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	for _, reference := range []string{"https://untrusted.example/image.png", "/uploads/ai-creation/references/1/../secret.png", "file:///etc/passwd"} {
		bad := strings.ReplaceAll(valid, "/uploads/ai-creation/references/1/ref.png", reference)
		w = mediaRequest(h, cookie, "POST", "/api/platform/media/video", bad)
		if w.Code != 400 {
			t.Fatal("unsafe reference accepted")
		}
	}
	if calls.Load() != 1 {
		t.Fatal("invalid reference contacted platform")
	}
}

func TestPlatformMediaUploadAuthenticatesValidatesAndStripsSecrets(t *testing.T) {
	h, cookie, calls := mediaFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/macs/v1/creation/upload" {
			t.Error("wrong upload destination")
		}
		_, _ = io.WriteString(w, `{"status":"success","url":"/uploads/ai-creation/references/1/input.png","absolute_url":"https://private.example/key","api_key":"secret-value"}`)
	})
	image, _ := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+/l9kAAAAASUVORK5CYII=")
	for _, test := range []struct {
		data     []byte
		loggedIn bool
		status   int
	}{{image, true, 200}, {[]byte("not an image"), true, 400}, {image, false, 401}} {
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		part, _ := writer.CreateFormFile("file", "image.png")
		_, _ = part.Write(test.data)
		_ = writer.Close()
		r := httptest.NewRequest("POST", "http://localhost:8082/api/platform/media/upload", &body)
		r.Header.Set("Content-Type", writer.FormDataContentType())
		if test.loggedIn {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		h.HandlePlatformMedia(w, r)
		if w.Code != test.status {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
		if strings.Contains(w.Body.String(), "secret-value") || strings.Contains(w.Body.String(), "private.example") {
			t.Fatal("upload leaked private metadata")
		}
	}
	if calls.Load() != 1 {
		t.Fatal("invalid or anonymous upload reached platform")
	}
}
