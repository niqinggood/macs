package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	gatewayhttp "mac/internal/gateway/httpx"
)

type platformMediaTask struct {
	ID            uint64   `json:"id"`
	Capability    string   `json:"capability"`
	Status        string   `json:"status"`
	Progress      int      `json:"progress"`
	Title         string   `json:"title,omitempty"`
	Prompt        string   `json:"prompt,omitempty"`
	ResultURL     string   `json:"result_url,omitempty"`
	Lyrics        string   `json:"lyrics,omitempty"`
	ErrorMessage  string   `json:"error_message,omitempty"`
	PaymentStatus string   `json:"payment_status,omitempty"`
	Cost          *float64 `json:"cost"`
}

// HandlePlatformMedia is a fixed-route account bridge, not a generic URL proxy.
// The existing platform owns persistent generation tasks and wallet settlement.
// A submission is sent once; an uncertain response is never automatically retried.
func (h *AccountHandler) HandlePlatformMedia(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	replyError := func(status int, code string) {
		if r.Method == http.MethodPost && (code == "invalid_response" || code == "platform_unavailable") {
			code = "submission_unknown"
		}
		writeAccountJSON(w, status, map[string]interface{}{"ok": false, "code": code})
	}
	if !gatewayhttp.RequestHasNoOriginOrSameHost(r) {
		replyError(http.StatusForbidden, "forbidden_origin")
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		w.Header().Set("Allow", "GET, POST")
		replyError(http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/platform/media/"), "/")
	if len(parts) == 1 && (parts[0] == "upload" || parts[0] == "config") {
		h.handlePlatformMediaSupport(w, r, parts[0])
		return
	}
	if r.URL.RawQuery != "" || len(parts) < 1 || len(parts) > 3 || (parts[0] != "image" && parts[0] != "music" && parts[0] != "video") {
		replyError(http.StatusBadRequest, "invalid_request")
		return
	}
	kind := parts[0]
	endpoint := "/api/macs/v1/creation/tasks"
	if kind == "music" {
		endpoint = "/api/macs/v1/music/jobs"
	}
	audio := len(parts) == 3 && kind == "music" && parts[2] == "audio"
	if len(parts) > 1 {
		id, err := strconv.ParseUint(parts[1], 10, 64)
		if err != nil || id == 0 || (len(parts) == 3 && !audio) || r.Method != http.MethodGet {
			replyError(http.StatusBadRequest, "invalid_request")
			return
		}
		endpoint += "/" + strconv.FormatUint(id, 10)
		if audio {
			endpoint += "/audio"
		}
	}
	sessionToken, session := h.accountSessionFromRequest(r)
	if session == nil {
		clearAccountSessionCookie(w, r)
		replyError(http.StatusUnauthorized, "login_required")
		return
	}
	var body []byte
	if r.Method == http.MethodPost {
		var input struct {
			Model           string   `json:"model"`
			Prompt          string   `json:"prompt"`
			AspectRatio     string   `json:"aspect_ratio,omitempty"`
			Lyrics          string   `json:"lyrics,omitempty"`
			Instrumental    bool     `json:"instrumental,omitempty"`
			ReferenceImages []string `json:"reference_images,omitempty"`
			Duration        int      `json:"duration,omitempty"`
			Resolution      string   `json:"resolution,omitempty"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&input) != nil || decoder.Decode(new(interface{})) != io.EOF || strings.TrimSpace(input.Prompt) == "" || len([]rune(input.Prompt)) > 4000 || len([]rune(input.Lyrics)) > 3500 {
			replyError(http.StatusBadRequest, "invalid_request")
			return
		}
		id, err := strconv.ParseUint(strings.TrimPrefix(input.Model, "platform:"), 10, 64)
		if !strings.HasPrefix(input.Model, "platform:") || err != nil || id == 0 {
			replyError(http.StatusBadRequest, "invalid_model")
			return
		}
		payload := map[string]interface{}{"model_config_ref": "platform:" + strconv.FormatUint(id, 10), "prompt": strings.TrimSpace(input.Prompt)}
		if kind == "image" || kind == "video" {
			if input.AspectRatio == "" {
				input.AspectRatio = "1:1"
			}
			if !map[string]bool{"1:1": true, "16:9": true, "9:16": true, "4:3": true, "3:4": true}[input.AspectRatio] {
				replyError(http.StatusBadRequest, "invalid_request")
				return
			}
			if len(input.ReferenceImages) > 5 {
				replyError(400, "invalid_request")
				return
			}
			for _, reference := range input.ReferenceImages {
				parsed, parseErr := url.Parse(reference)
				if parseErr != nil || parsed.IsAbs() || !strings.HasPrefix(reference, "/uploads/ai-creation/references/") || strings.Contains(reference, "..") || strings.Contains(reference, "\\") || parsed.RawQuery != "" || parsed.Fragment != "" {
					replyError(400, "invalid_request")
					return
				}
			}
			mode := "text_to_" + kind
			if len(input.ReferenceImages) > 0 {
				mode = "image_to_" + kind
			}
			payload["mode"], payload["aspect_ratio"] = mode, input.AspectRatio
			payload["reference_images"] = input.ReferenceImages
			if kind == "video" {
				if input.Duration < 1 || input.Duration > 30 || !map[string]bool{"720p": true, "768p": true, "1080p": true, "480p": true}[input.Resolution] {
					replyError(400, "invalid_request")
					return
				}
				payload["duration"], payload["resolution"] = input.Duration, input.Resolution
			}
			endpoint = "/api/macs/v1/creation/generate"
		} else {
			if len(input.ReferenceImages) > 0 {
				replyError(400, "unsupported_attachments")
				return
			}
			payload["music_prompt"] = strings.TrimSpace(input.Prompt)
			payload["lyrics"] = strings.TrimSpace(input.Lyrics)
			payload["generation_mode"] = "generate_all"
			if input.Instrumental {
				payload["lyrics_density"], payload["generation_mode"], payload["lyrics"] = "none", "generate_audio", ""
			} else if input.Lyrics != "" {
				payload["generation_mode"] = "generate_audio"
			}
		}
		body, _ = json.Marshal(payload)
	}
	providerURL, err := accountServiceEndpointURL(h.config, endpoint)
	if err != nil {
		replyError(http.StatusServiceUnavailable, "platform_unavailable")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, r.Method, providerURL, bytes.NewReader(body))
	if err != nil {
		replyError(http.StatusServiceUnavailable, "platform_unavailable")
		return
	}
	request.AddCookie(&http.Cookie{Name: "auth_token", Value: session.ProviderToken})
	request.Header.Set("Content-Type", "application/json")
	if audio && r.Header.Get("Range") != "" {
		request.Header.Set("Range", r.Header.Get("Range"))
	}
	client := *h.client
	client.Timeout = 45 * time.Second
	response, err := client.Do(request)
	if err != nil {
		code := "platform_unavailable"
		if r.Method == http.MethodPost {
			code = "submission_unknown"
		}
		replyError(http.StatusBadGateway, code)
		return
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusUnauthorized {
		h.sessions.Delete(sessionToken)
		clearAccountSessionCookie(w, r)
		replyError(http.StatusUnauthorized, "login_required")
		return
	}
	if audio && (response.StatusCode == http.StatusOK || response.StatusCode == http.StatusPartialContent) && strings.HasPrefix(response.Header.Get("Content-Type"), "audio/") {
		for _, name := range []string{"Content-Type", "Content-Length", "Accept-Ranges", "Content-Range"} {
			if value := response.Header.Get(name); value != "" {
				w.Header().Set(name, value)
			}
		}
		w.WriteHeader(response.StatusCode)
		_, _ = io.Copy(w, io.LimitReader(response.Body, 128<<20))
		return
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		code, status := "platform_unavailable", http.StatusBadGateway
		switch response.StatusCode {
		case 402:
			code, status = "insufficient_funds", 402
		case 404:
			code, status = "platform_not_ready", 404
		case 400:
			code, status = "invalid_generation", 400
		case 409:
			code, status = "generation_in_progress", 409
		case 429:
			code, status = "rate_limited", 429
		}
		replyError(status, code)
		return
	}
	if audio {
		replyError(http.StatusBadGateway, "invalid_response")
		return
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, accountResponseBodyLimit+1))
	if err != nil || int64(len(data)) > accountResponseBodyLimit {
		replyError(http.StatusBadGateway, "invalid_response")
		return
	}
	var envelope struct {
		Status string            `json:"status"`
		Task   json.RawMessage   `json:"task"`
		Tasks  []json.RawMessage `json:"tasks"`
		TaskID uint64            `json:"task_id"`
	}
	if json.Unmarshal(data, &envelope) != nil || envelope.Status == "error" {
		replyError(http.StatusBadGateway, "invalid_response")
		return
	}
	parseTask := func(raw json.RawMessage) *platformMediaTask {
		var native struct {
			platformMediaTask
			TaskID   uint64 `json:"task_id"`
			ErrorMsg string `json:"error_msg"`
			Result   struct {
				Lyrics string `json:"lyrics"`
			} `json:"result"`
		}
		if json.Unmarshal(raw, &native) != nil {
			return nil
		}
		task := native.platformMediaTask
		if task.ID == 0 {
			task.ID = native.TaskID
		}
		if task.ID == 0 {
			return nil
		}
		if kind != "music" && task.Capability != kind {
			return nil
		}
		task.Capability = kind
		if kind == "music" {
			task.Lyrics, task.ErrorMessage = native.Result.Lyrics, native.ErrorMsg
			if task.Status == "completed" {
				task.ResultURL = "/api/platform/media/music/" + strconv.FormatUint(task.ID, 10) + "/audio"
			}
		} else if task.ResultURL != "" {
			base, _ := url.Parse(providerURL)
			target, parseErr := url.Parse(task.ResultURL)
			if parseErr != nil || target.User != nil {
				task.ResultURL = ""
			} else {
				target = base.ResolveReference(target)
				if target.Scheme == "http" || target.Scheme == "https" {
					task.ResultURL = target.String()
				} else {
					task.ResultURL = ""
				}
			}
		}
		return &task
	}
	if r.Method == http.MethodGet && len(parts) == 1 {
		if envelope.Tasks == nil {
			replyError(http.StatusBadGateway, "invalid_response")
			return
		}
		tasks := make([]*platformMediaTask, 0, len(envelope.Tasks))
		for _, raw := range envelope.Tasks {
			if task := parseTask(raw); task != nil {
				tasks = append(tasks, task)
			}
		}
		writeAccountJSON(w, http.StatusOK, map[string]interface{}{"ok": true, "tasks": tasks})
		return
	}
	raw := envelope.Task
	if kind == "music" {
		raw = data
	}
	task := parseTask(raw)
	if task == nil {
		replyError(http.StatusBadGateway, "invalid_response")
		return
	}
	if r.Method == http.MethodPost && task.Status == "success" {
		task.Status = "queued"
	}
	writeAccountJSON(w, response.StatusCode, map[string]interface{}{"ok": true, "task": task})
}
