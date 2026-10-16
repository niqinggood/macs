package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"time"
)

// These routes only forward authenticated uploads and public capability settings.
// They never accept an upstream URL, provider key, price or account identity.
func (h *AccountHandler) handlePlatformMediaSupport(w http.ResponseWriter, r *http.Request, action string) {
	fail := func(status int, code string) {
		writeAccountJSON(w, status, map[string]interface{}{"ok": false, "code": code})
	}
	if r.URL.RawQuery != "" || action == "upload" && r.Method != "POST" || action == "config" && r.Method != "GET" {
		fail(400, "invalid_request")
		return
	}
	sessionID, session := h.accountSessionFromRequest(r)
	if session == nil {
		fail(401, "login_required")
		return
	}
	var body bytes.Buffer
	contentType := "application/json"
	if action == "upload" {
		r.Body = http.MaxBytesReader(w, r.Body, 11<<20)
		if r.ParseMultipartForm(11<<20) != nil {
			fail(400, "invalid_image")
			return
		}
		if r.MultipartForm != nil {
			defer r.MultipartForm.RemoveAll()
		}
		file, header, err := r.FormFile("file")
		if err != nil {
			fail(400, "invalid_image")
			return
		}
		defer file.Close()
		data, err := io.ReadAll(io.LimitReader(file, (10<<20)+1))
		if err != nil || len(data) == 0 || len(data) > 10<<20 {
			fail(400, "invalid_image")
			return
		}
		mime := http.DetectContentType(data)
		if mime != "image/png" && mime != "image/jpeg" && mime != "image/webp" && mime != "image/gif" {
			fail(400, "invalid_image")
			return
		}
		writer := multipart.NewWriter(&body)
		part, err := writer.CreateFormFile("file", header.Filename)
		if err != nil {
			fail(500, "upload_failed")
			return
		}
		_, _ = part.Write(data)
		_ = writer.Close()
		contentType = writer.FormDataContentType()
	}
	endpoint, err := accountServiceEndpointURL(h.config, "/api/macs/v1/creation/"+action)
	if err != nil {
		fail(503, "platform_unavailable")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, r.Method, endpoint, &body)
	if err != nil {
		fail(503, "platform_unavailable")
		return
	}
	request.Header.Set("Content-Type", contentType)
	request.AddCookie(&http.Cookie{Name: "auth_token", Value: session.ProviderToken})
	response, err := h.client.Do(request)
	if err != nil {
		fail(502, "platform_unavailable")
		return
	}
	defer response.Body.Close()
	if response.StatusCode == 401 {
		h.sessions.Delete(sessionID)
		clearAccountSessionCookie(w, r)
		fail(401, "login_required")
		return
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		fail(response.StatusCode, "upload_failed")
		return
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, accountResponseBodyLimit+1))
	if err != nil || int64(len(data)) > accountResponseBodyLimit {
		fail(502, "invalid_response")
		return
	}
	if action == "upload" {
		var result struct {
			Status string `json:"status"`
			URL    string `json:"url"`
		}
		if json.Unmarshal(data, &result) != nil || result.Status != "success" || !strings.HasPrefix(result.URL, "/uploads/ai-creation/references/") || strings.ContainsAny(result.URL, "\\?#") || strings.Contains(result.URL, "..") {
			fail(502, "invalid_response")
			return
		}
		writeAccountJSON(w, 200, map[string]interface{}{"ok": true, "url": result.URL})
		return
	}
	var config struct {
		Status            string          `json:"status"`
		Available         map[string]bool `json:"available"`
		VideoCapabilities *struct {
			DurationOptions     []int    `json:"durationOptions"`
			DefaultDuration     int      `json:"defaultDuration"`
			Resolutions         []string `json:"resolutions"`
			SupportsTextToVideo bool     `json:"supportsTextToVideo"`
			SupportsSingleImage bool     `json:"supportsSingleImage"`
			MaxReferenceImages  int      `json:"maxReferenceImages"`
		} `json:"videoCapabilities"`
	}
	if json.Unmarshal(data, &config) != nil || config.Status != "success" {
		fail(502, "invalid_response")
		return
	}
	writeAccountJSON(w, 200, map[string]interface{}{"ok": true, "available": config.Available, "videoCapabilities": config.VideoCapabilities})
}
