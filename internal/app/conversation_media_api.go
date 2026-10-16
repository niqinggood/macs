package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"
	"mac/internal/attachments"
	"mac/internal/conversation"
	"mac/internal/modelruntime"
)

type conversationMediaInput struct {
	RequestID    string             `json:"request_id"`
	SessionID    string             `json:"session_id"`
	Capability   string             `json:"capability"`
	Model        string             `json:"model"`
	Prompt       string             `json:"prompt"`
	AspectRatio  string             `json:"aspect_ratio,omitempty"`
	Duration     int                `json:"duration,omitempty"`
	Resolution   string             `json:"resolution,omitempty"`
	Lyrics       string             `json:"lyrics,omitempty"`
	Instrumental bool               `json:"instrumental,omitempty"`
	Attachments  []attachments.File `json:"attachments,omitempty"`
}

// mediaResponse captures the existing authenticated, fixed-route bridge. It is
// deliberately used only for bounded JSON replies, never audio or arbitrary URLs.
type mediaResponse struct {
	header http.Header
	bytes.Buffer
	status int
}

func (w *mediaResponse) Header() http.Header { return w.header }
func (w *mediaResponse) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
}
func (w *mediaResponse) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.status = 200
	}
	if w.Len()+len(p) > 1<<20 {
		return 0, errors.New("media response too large")
	}
	return w.Buffer.Write(p)
}

func (g *GatewayService) mediaOwner(r *http.Request) string {
	if g.accountHandler == nil {
		return ""
	}
	return g.accountHandler.PlatformAccountSubject(r)
}

func (g *GatewayService) callMediaBridge(r *http.Request, method, path, contentType string, body io.Reader) *mediaResponse {
	request := r.Clone(r.Context())
	target := *r.URL
	target.Path = "/api/platform/media/" + path
	target.RawQuery = ""
	request.URL = &target
	request.Method = method
	request.Body = io.NopCloser(body)
	request.Header = r.Header.Clone()
	request.Header.Set("Content-Type", contentType)
	w := &mediaResponse{header: make(http.Header)}
	g.accountHandler.HandlePlatformMedia(w, request)
	return w
}

func (g *GatewayService) handleConversationMedia(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "private, no-store")
	fail := func(status int, code string) {
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"ok": false, "code": code})
	}
	if !personalMemoryLocalRequest(r) || r.URL.RawQuery != "" {
		fail(403, "local_only")
		return
	}
	if r.Method != "GET" && r.Method != "POST" {
		fail(405, "method_not_allowed")
		return
	}
	owner := g.mediaOwner(r)
	if owner == "" {
		fail(401, "login_required")
		return
	}
	if g.db == nil {
		fail(503, "storage_unavailable")
		return
	}
	key := strings.TrimPrefix(r.URL.Path, "/api/conversation/media")
	key = strings.TrimPrefix(key, "/")
	imageRead := r.Method == "GET" && strings.HasSuffix(key, "/image")
	if imageRead || r.Method == "POST" && strings.HasSuffix(key, "/reference") {
		id := strings.TrimSuffix(strings.TrimSuffix(key, "/reference"), "/image")
		if !validMediaRequestID(id) {
			fail(400, "invalid_request")
			return
		}
		var message ChatMessage
		if g.db.Where("media_request_id = ? AND media_owner = ?", id, owner).First(&message).Error != nil || message.Generation == nil || message.Generation.Capability != "image" || message.Generation.Task.Status != "completed" {
			fail(404, "not_found")
			return
		}
		if _, err := g.bindSessionWorkspace(message.SessionID, "", "", false); err != nil {
			fail(403, "session_unavailable")
			return
		}
		data, mime, code := g.accountHandler.ReadPlatformImage(r.Context(), message.Generation.Task.ResultURL)
		if code != "" {
			fail(502, code)
			return
		}
		if imageRead {
			w.Header().Set("Content-Type", mime)
			w.Header().Set("X-Content-Type-Options", "nosniff")
			_, _ = w.Write(data)
			return
		}
		if len(data) > 10<<20 {
			fail(400, "invalid_image")
			return
		}
		target, _ := url.Parse(message.Generation.Task.ResultURL)
		attachment, err := attachments.SaveWorkspaceUpload(g.attachmentWorkspaceRoot(), "reference-"+id+filepath.Ext(target.Path), mime, bytes.NewReader(data))
		if err != nil {
			fail(503, "storage_unavailable")
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"ok": true, "attachment": attachment})
		return
	}
	if r.Method == "GET" {
		if !validMediaRequestID(key) {
			fail(400, "invalid_request")
			return
		}
		var message ChatMessage
		if g.db.Where("media_request_id = ? AND media_owner = ?", key, owner).First(&message).Error != nil || message.Generation == nil {
			fail(404, "not_found")
			return
		}
		if _, err := g.bindSessionWorkspace(message.SessionID, "", "", false); err != nil {
			fail(403, "session_unavailable")
			return
		}
		if message.Generation.Task.ID != 0 && !message.Generation.Task.Terminal() {
			response := g.callMediaBridge(r, "GET", message.Generation.Capability+"/"+uintString(message.Generation.Task.ID), "application/json", strings.NewReader(""))
			var result struct {
				OK   bool                        `json:"ok"`
				Task conversation.GenerationTask `json:"task"`
				Code string                      `json:"code"`
			}
			if json.Unmarshal(response.Bytes(), &result) != nil || !result.OK {
				fail(response.status, result.Code)
				return
			}
			message.Generation.Task = result.Task
			if message.Generation.Task.Prompt == "" {
				message.Generation.Task.Prompt = message.Content
			}
			message.Generation.ErrorCode = ""
			if err := g.persistMediaSnapshot(&message); err != nil {
				fail(503, "storage_unavailable")
				return
			}
		}
		if message.Generation.Task.ID == 0 && message.Generation.Task.Status == "submitting" {
			message.Generation.Task.Status = "submission_unknown"
			message.Generation.ErrorCode = "submission_unknown"
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"ok": true, "message": message})
		return
	}
	if key != "" {
		fail(400, "invalid_request")
		return
	}
	var input conversationMediaInput
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&input) != nil || decoder.Decode(new(interface{})) != io.EOF || !validMediaRequestID(input.RequestID) || !strings.HasPrefix(input.SessionID, "web_") || strings.TrimSpace(input.Prompt) == "" || len([]rune(input.Prompt)) > 4000 || len([]rune(input.Lyrics)) > 3500 || !map[string]bool{"image": true, "video": true, "music": true}[input.Capability] {
		fail(400, "invalid_request")
		return
	}
	if _, err := g.bindSessionWorkspace(input.SessionID, "", "", false); err != nil || g.isAgentOSSessionID(input.SessionID) {
		fail(403, "session_unavailable")
		return
	}
	input.Prompt = strings.TrimSpace(input.Prompt)
	encoded, _ := json.Marshal(input)
	sum := sha256.Sum256(encoded)
	fingerprint := hex.EncodeToString(sum[:])
	var existing ChatMessage
	if g.db.Where("media_request_id = ?", input.RequestID).First(&existing).Error == nil {
		if existing.MediaOwner != owner || existing.MediaPayloadHash != fingerprint {
			fail(409, "request_conflict")
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"ok": true, "message": existing})
		return
	}
	base, err := g.accountHandler.PlatformModelBaseURL()
	if err != nil {
		fail(503, "platform_unavailable")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()
	models, code := modelruntime.ReadPlatformModelCatalog(ctx, nil, base)
	if code != "" {
		fail(502, "platform_catalog_unavailable")
		return
	}
	var selected *modelruntime.ChatCatalogModel
	for i := range models {
		if models[i].ID == input.Model && models[i].Capability == input.Capability && models[i].Available && (models[i].DirectGeneration || input.Capability == "video") {
			selected = &models[i]
			break
		}
	}
	if selected == nil {
		fail(400, "invalid_model")
		return
	}
	if input.Capability == "music" && len(input.Attachments) > 0 || len(input.Attachments) > 1 {
		fail(400, "unsupported_attachments")
		return
	}
	files, err := attachments.Normalize(input.Attachments, g.attachmentWorkspaceRoot(), false)
	if err != nil {
		fail(400, "invalid_attachment")
		return
	}
	for _, file := range files {
		if !strings.HasPrefix(file.Path, "storage/") || file.Kind != "image" || file.Size <= 0 || file.Size > 10<<20 {
			fail(400, "invalid_image")
			return
		}
	}
	if input.AspectRatio == "" {
		input.AspectRatio = "1:1"
	}
	if !map[string]bool{"1:1": true, "16:9": true, "9:16": true, "4:3": true, "3:4": true}[input.AspectRatio] {
		fail(400, "invalid_request")
		return
	}
	if input.Capability == "video" && (input.Duration < 1 || input.Duration > 30 || !map[string]bool{"720p": true, "768p": true, "1080p": true, "480p": true}[input.Resolution]) {
		fail(400, "invalid_request")
		return
	}
	var message ChatMessage
	err = g.db.Transaction(func(tx *gorm.DB) error {
		var session ChatSession
		if err := tx.Where("id = ?", input.SessionID).First(&session).Error; err != nil {
			return err
		}
		if session.MediaOwner != "" && session.MediaOwner != owner {
			return ErrSessionOwnerConflict
		}
		binding := tx.Model(&session).Where("media_owner = '' OR media_owner IS NULL OR media_owner = ?", owner).Updates(map[string]interface{}{"media_owner": owner, "updated_at": time.Now(), "message_count": gorm.Expr("message_count + 2")})
		if binding.Error != nil {
			return binding.Error
		}
		if binding.RowsAffected != 1 {
			return ErrSessionOwnerConflict
		}
		user := ChatMessage{SessionID: session.ID, TenantID: normalizePersonalOwnerTenantID(session.TenantID), SubjectID: session.SubjectID, Role: "user", Content: input.Prompt, Attachments: files, Status: "completed", Channel: "web", MediaOwner: owner, CreatedAt: time.Now()}
		if err := tx.Create(&user).Error; err != nil {
			return err
		}
		message = ChatMessage{SessionID: session.ID, TenantID: user.TenantID, SubjectID: user.SubjectID, Role: "assistant", AgentName: selected.Name, Status: "pending", Channel: "web", MediaOwner: owner, MediaRequestID: &input.RequestID, MediaPayloadHash: fingerprint, CreatedAt: time.Now(), Generation: &conversation.Generation{RequestID: input.RequestID, Capability: input.Capability, Model: input.Model, ModelName: selected.Name, Task: conversation.GenerationTask{Capability: input.Capability, Status: "submitting", Prompt: input.Prompt}}}
		return tx.Create(&message).Error
	})
	if err != nil {
		if g.db.Where("media_request_id = ? AND media_owner = ? AND media_payload_hash = ?", input.RequestID, owner, fingerprint).First(&existing).Error == nil {
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"ok": true, "message": existing})
			return
		}
		fail(409, "request_conflict")
		return
	}
	// From this point, errors are saved in the assistant message. The same local
	// request can only return that snapshot and can never invoke a provider twice.
	payload := map[string]interface{}{"model": input.Model, "prompt": input.Prompt, "aspect_ratio": input.AspectRatio, "duration": input.Duration, "resolution": input.Resolution, "instrumental": input.Instrumental, "lyrics": input.Lyrics}
	var references []string
	for _, file := range files {
		reference, uploadCode := g.uploadMediaReference(r, file)
		if uploadCode != "" {
			message.Generation.Task.Status = "failed"
			message.Generation.ErrorCode = uploadCode
			if err := g.persistMediaSnapshot(&message); err != nil {
				fail(503, "storage_unavailable")
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"ok": true, "message": message})
			return
		}
		references = append(references, reference)
	}
	payload["reference_images"] = references
	body, _ := json.Marshal(payload)
	response := g.callMediaBridge(r, "POST", input.Capability, "application/json", bytes.NewReader(body))
	var result struct {
		OK   bool                        `json:"ok"`
		Code string                      `json:"code"`
		Task conversation.GenerationTask `json:"task"`
	}
	if json.Unmarshal(response.Bytes(), &result) == nil && result.OK {
		message.Generation.Task = result.Task
		if message.Generation.Task.Prompt == "" {
			message.Generation.Task.Prompt = input.Prompt
		}
	} else {
		message.Generation.ErrorCode = result.Code
		message.Generation.Task.Status = "failed"
		if result.Code == "" || result.Code == "submission_unknown" {
			message.Generation.ErrorCode = "submission_unknown"
			message.Generation.Task.Status = "submission_unknown"
		}
	}
	if err := g.persistMediaSnapshot(&message); err != nil {
		fail(503, "submission_unknown")
		return
	}
	g.broadcastSessionListChanged()
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"ok": true, "message": message})
}

func validMediaRequestID(id string) bool {
	if len(id) != 32 {
		return false
	}
	data, err := hex.DecodeString(id)
	return err == nil && len(data) == 16
}
func uintString(value uint64) string { return strconv.FormatUint(value, 10) }

func (g *GatewayService) persistMediaSnapshot(message *ChatMessage) error {
	message.Status = "pending"
	if message.Generation.Task.Terminal() {
		message.Status = message.Generation.Task.Status
	}
	message.Content = message.Generation.Task.Prompt
	data, _ := json.Marshal(message.Generation)
	return g.db.Model(&ChatMessage{}).Where("id = ? AND media_owner = ?", message.ID, message.MediaOwner).Updates(map[string]interface{}{"generation": string(data), "status": message.Status, "content": message.Content}).Error
}

func (g *GatewayService) uploadMediaReference(r *http.Request, file attachments.File) (string, string) {
	source, err := os.Open(filepath.Join(g.attachmentWorkspaceRoot(), filepath.FromSlash(file.Path)))
	if err != nil {
		return "", "invalid_image"
	}
	defer source.Close()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", file.Name)
	if err != nil {
		return "", "upload_failed"
	}
	if _, err = io.Copy(part, io.LimitReader(source, (10<<20)+1)); err != nil {
		return "", "upload_failed"
	}
	_ = writer.Close()
	response := g.callMediaBridge(r, "POST", "upload", writer.FormDataContentType(), &body)
	var result struct {
		OK   bool   `json:"ok"`
		URL  string `json:"url"`
		Code string `json:"code"`
	}
	if json.Unmarshal(response.Bytes(), &result) != nil || !result.OK {
		if result.Code == "" {
			result.Code = "upload_failed"
		}
		return "", result.Code
	}
	return result.URL, ""
}
