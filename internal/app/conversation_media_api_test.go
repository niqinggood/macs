package app

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
	"mac/internal/attachments"
	authcore "mac/internal/auth"
	"mac/internal/projectctx"
)

type conversationMediaFixture struct {
	g               *GatewayService
	provider        *httptest.Server
	login           func(*authcore.AccountHandler, string) *http.Cookie
	cookie          *http.Cookie
	images          []byte
	generate        atomic.Int32
	upload          atomic.Int32
	uncertain       atomic.Bool
	payload         map[string]interface{}
	generateGate    chan struct{}
	generateEntered chan struct{}
}

func newConversationMediaFixture(t *testing.T) *conversationMediaFixture {
	t.Helper()
	t.Setenv("OPENKITTY_ACCOUNT_SERVICE_URL", "")
	f := &conversationMediaFixture{}
	f.images, _ = base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+/l9kAAAAASUVORK5CYII=")
	f.provider = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/nova/login" {
			var input struct{ Username string }
			_ = json.NewDecoder(r.Body).Decode(&input)
			http.SetCookie(w, &http.Cookie{Name: "auth_token", Value: input.Username + "-fixture-token"})
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "success", "user": input.Username})
			return
		}
		if r.URL.Path == "/api/macs/v1/catalog" {
			if r.Header.Get("Cookie") != "" {
				t.Error("public discovery sent an account token")
			}
			_, _ = io.WriteString(w, `{"data":[{"id":"platform:1","name":"Image model","capability":"image","available":true,"direct_generation":true},{"id":"platform:2","name":"Video model","capability":"video","available":true},{"id":"platform:3","name":"Music model","capability":"music","available":true,"direct_generation":true}]}`)
			return
		}
		if r.URL.Path == "/uploads/results/test.png" {
			if r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" {
				t.Error("asset download forwarded a credential")
			}
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(f.images)
			return
		}
		cookie, err := r.Cookie("auth_token")
		if err != nil || cookie.Value != "alice-fixture-token" {
			t.Errorf("wrong generation owner: %v", cookie)
			w.WriteHeader(401)
			return
		}
		switch r.URL.Path {
		case "/api/macs/v1/creation/upload":
			f.upload.Add(1)
			file, _, err := r.FormFile("file")
			if err != nil {
				t.Error(err)
				w.WriteHeader(400)
				return
			}
			defer file.Close()
			data, _ := io.ReadAll(file)
			if !bytes.Equal(data, f.images) {
				t.Error("reference image bytes changed")
			}
			_, _ = io.WriteString(w, `{"status":"success","url":"/uploads/ai-creation/references/1/input.png","api_key":"must-not-leak"}`)
		case "/api/macs/v1/creation/generate":
			f.generate.Add(1)
			if f.generateGate != nil {
				f.generateEntered <- struct{}{}
				select {
				case <-f.generateGate:
				case <-r.Context().Done():
					return
				}
			}
			_ = json.NewDecoder(r.Body).Decode(&f.payload)
			if f.uncertain.Load() {
				w.WriteHeader(502)
				return
			}
			kind := "image"
			if strings.HasSuffix(f.payload["mode"].(string), "video") {
				kind = "video"
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"status": "success", "task": map[string]interface{}{"id": 42, "capability": kind, "status": "queued", "prompt": f.payload["prompt"]}})
		case "/api/macs/v1/creation/tasks/42":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"status": "success", "task": map[string]interface{}{"id": 42, "capability": "image", "status": "completed", "result_url": f.provider.URL + "/uploads/results/test.png", "payment_status": "charged", "cost": 1}})
		case "/api/macs/v1/music/jobs":
			f.generate.Add(1)
			_ = json.NewDecoder(r.Body).Decode(&f.payload)
			_, _ = io.WriteString(w, `{"status":"success","task_id":43}`)
		default:
			t.Errorf("unexpected platform request %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(f.provider.Close)
	workspace := t.TempDir()
	if err := os.Mkdir(filepath.Join(workspace, "storage"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "storage", "input.png"), f.images, 0600); err != nil {
		t.Fatal(err)
	}
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "media.db")), &gorm.Config{Logger: gormlogger.Default.LogMode(gormlogger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&ChatMessage{}, &ChatSession{}, &GatewayTaskRecord{}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sqlDB, _ := db.DB(); _ = sqlDB.Close() })
	f.g = &GatewayService{db: db, config: &Config{Workspace: workspace}, project: projectctx.New(workspace), logger: log.New(io.Discard, "", 0), accountHandler: authcore.NewAccountHandler(authcore.AccountConfig{ServiceURL: f.provider.URL})}
	if err := db.Create(&ChatSession{ID: "web_media", Name: "Media", TenantID: "default", Workspace: workspace, ProjectID: f.g.project.ProjectID, CreatedAt: time.Now(), UpdatedAt: time.Now()}).Error; err != nil {
		t.Fatal(err)
	}
	f.login = func(h *authcore.AccountHandler, username string) *http.Cookie {
		r := httptest.NewRequest("POST", "http://localhost:8082/api/account/login", strings.NewReader(`{"username":"`+username+`","password":"fixture"}`))
		w := httptest.NewRecorder()
		h.HandleLogin(w, r)
		if w.Code != 200 {
			t.Fatal("fixture login failed", w.Body.String())
		}
		return w.Result().Cookies()[0]
	}
	f.cookie = f.login(f.g.accountHandler, "alice")
	return f
}

func callConversationMedia(g *GatewayService, cookie *http.Cookie, method, path string, input interface{}) *httptest.ResponseRecorder {
	data, _ := json.Marshal(input)
	r := httptest.NewRequest(method, "http://localhost:8082"+path, bytes.NewReader(data))
	r.RemoteAddr = "127.0.0.1:1234"
	if cookie != nil {
		r.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	g.handleConversationMedia(w, r)
	return w
}

func TestConversationMediaReferenceGenerationPersistsAndResumesWithoutReplay(t *testing.T) {
	f := newConversationMediaFixture(t)
	input := conversationMediaInput{RequestID: strings.Repeat("a", 32), SessionID: "web_media", Capability: "image", Model: "platform:1", Prompt: "Make the reference blue", Attachments: []attachments.File{{Path: "storage/input.png"}}}
	for attempt := 0; attempt < 2; attempt++ {
		response := callConversationMedia(f.g, f.cookie, "POST", "/api/conversation/media", input)
		if response.Code != 200 || !strings.Contains(response.Body.String(), `"status":"queued"`) {
			t.Fatalf("submit failed %d: %s", response.Code, response.Body.String())
		}
	}
	if f.generate.Load() != 1 || f.upload.Load() != 1 || f.payload["mode"] != "image_to_image" || f.payload["model_config_ref"] != "platform:1" {
		t.Fatalf("incorrect reference route or replay: %v", f.payload)
	}
	if f.payload["reference_images"].([]interface{})[0] != "/uploads/ai-creation/references/1/input.png" {
		t.Fatal("wrong reference")
	}
	var count int64
	f.g.db.Model(&ChatMessage{}).Count(&count)
	if count != 2 {
		t.Fatalf("duplicate conversation messages: %d", count)
	}
	// A fresh account handler models service restart; the same account can resume.
	f.g.accountHandler = authcore.NewAccountHandler(authcore.AccountConfig{ServiceURL: f.provider.URL})
	f.cookie = f.login(f.g.accountHandler, "alice")
	response := callConversationMedia(f.g, f.cookie, "GET", "/api/conversation/media/"+input.RequestID, nil)
	if response.Code != 200 || !strings.Contains(response.Body.String(), `"status":"completed"`) || strings.Contains(response.Body.String(), "fixture-token") || strings.Contains(response.Body.String(), "media_owner") {
		t.Fatalf("resume failed: %s", response.Body.String())
	}
	var saved ChatMessage
	if err := f.g.db.Where("media_request_id = ?", input.RequestID).First(&saved).Error; err != nil || saved.Generation.Task.Status != "completed" {
		t.Fatal("completed snapshot not persisted", err)
	}
	response = callConversationMedia(f.g, f.cookie, "POST", "/api/conversation/media/"+input.RequestID+"/reference", nil)
	if response.Code != 200 || !strings.Contains(response.Body.String(), `"kind":"image"`) {
		t.Fatal("reference reuse failed", response.Body.String())
	}
	response = callConversationMedia(f.g, f.cookie, "GET", "/api/conversation/media/"+input.RequestID+"/image", nil)
	if response.Code != 200 || response.Header().Get("Content-Type") != "image/png" || !bytes.Equal(response.Body.Bytes(), f.images) || !strings.Contains(response.Header().Get("Cache-Control"), "no-store") {
		t.Fatal("owned image export read failed", response.Code)
	}
	var afterRead ChatMessage
	f.g.db.First(&afterRead, saved.ID)
	if afterRead.Generation.Task.ResultURL != saved.Generation.Task.ResultURL || f.generate.Load() != 1 {
		t.Fatal("export changed the saved result or called a paid model")
	}
	bob := f.login(f.g.accountHandler, "bob")
	for _, path := range []string{"/api/conversation/media/" + input.RequestID, "/api/conversation/media/" + input.RequestID + "/reference", "/api/conversation/media/" + input.RequestID + "/image"} {
		method := "GET"
		if strings.HasSuffix(path, "reference") {
			method = "POST"
		}
		response = callConversationMedia(f.g, bob, method, path, nil)
		if response.Code != 404 {
			t.Fatal("another account accessed media", response.Code)
		}
	}
	r := httptest.NewRequest("GET", "http://localhost:8082/api/session/web_media", nil)
	r.AddCookie(bob)
	w := httptest.NewRecorder()
	f.g.handleSessionMessages(w, r)
	if strings.Contains(w.Body.String(), "Make the reference blue") || strings.Contains(w.Body.String(), "result_url") {
		t.Fatal("another account read media history", w.Body.String())
	}
	if err := f.g.db.Exec("CREATE VIRTUAL TABLE chat_messages_fts USING fts5(session_id, content, tokenize='unicode61')").Error; err != nil {
		t.Fatal(err)
	}
	if err := f.g.db.Exec("INSERT INTO chat_messages_fts(rowid, session_id, content) SELECT id, session_id, content FROM chat_messages").Error; err != nil {
		t.Fatal(err)
	}
	for _, route := range []struct {
		path    string
		handler http.HandlerFunc
	}{
		{"/api/session/export/web_media", f.g.handleSessionExport},
		{"/api/sessions/search?q=reference", f.g.handleSessionSearch},
		{"/api/sessions", f.g.handleListSessions},
	} {
		for _, account := range []*http.Cookie{f.cookie, bob} {
			request := httptest.NewRequest("GET", "http://localhost:8082"+route.path, nil)
			request.AddCookie(account)
			response := httptest.NewRecorder()
			route.handler(response, request)
			if route.path == "/api/session/export/web_media" && account == f.cookie {
				if !strings.Contains(response.Body.String(), "data:image/png;base64,") || !strings.Contains(response.Body.String(), "扣费状态") || strings.Contains(response.Body.String(), saved.Generation.Task.ResultURL) {
					t.Fatal("export omitted embedded image or billing")
				}
			}
			visible := strings.Contains(response.Body.String(), "Make the reference blue") || strings.Contains(response.Body.String(), "web_media")
			if visible != (account == f.cookie) {
				t.Fatalf("incorrect media visibility in %s: %d %s", route.path, response.Code, response.Body.String())
			}
		}
	}
	f.g.db.First(&afterRead, saved.ID)
	if afterRead.Generation.Task.ResultURL != saved.Generation.Task.ResultURL || f.generate.Load() != 1 {
		t.Fatal("HTML export changed stored content or generated another result")
	}
	input.Prompt = "Different request"
	response = callConversationMedia(f.g, f.cookie, "POST", "/api/conversation/media", input)
	if response.Code != 409 || f.generate.Load() != 1 {
		t.Fatal("request identity collision caused a second generation")
	}
}

func TestConversationMediaConcurrentDuplicateDoesNotCallProviderTwice(t *testing.T) {
	f := newConversationMediaFixture(t)
	f.generateGate = make(chan struct{})
	f.generateEntered = make(chan struct{}, 1)
	closed := false
	defer func() {
		if !closed {
			close(f.generateGate)
		}
	}()
	input := conversationMediaInput{RequestID: strings.Repeat("e", 32), SessionID: "web_media", Capability: "image", Model: "platform:1", Prompt: "Only one paid generation"}
	first := make(chan *httptest.ResponseRecorder, 1)
	go func() { first <- callConversationMedia(f.g, f.cookie, "POST", "/api/conversation/media", input) }()
	select {
	case <-f.generateEntered:
	case <-time.After(5 * time.Second):
		t.Fatal("provider was not called")
	}
	duplicate := callConversationMedia(f.g, f.cookie, "POST", "/api/conversation/media", input)
	if duplicate.Code != 200 || !strings.Contains(duplicate.Body.String(), `"status":"submitting"`) {
		t.Fatalf("concurrent duplicate lost durable reservation: %d %s", duplicate.Code, duplicate.Body.String())
	}
	close(f.generateGate)
	closed = true
	select {
	case response := <-first:
		if response.Code != 200 {
			t.Fatal(response.Body.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("original request did not finish")
	}
	var count int64
	f.g.db.Model(&ChatMessage{}).Count(&count)
	if f.generate.Load() != 1 || count != 2 {
		t.Fatalf("duplicate provider call or messages: calls=%d messages=%d", f.generate.Load(), count)
	}
}

func TestConversationMediaVideoAndMusicUseExistingAPIsWithoutAgent(t *testing.T) {
	for _, kind := range []string{"video", "music"} {
		t.Run(kind, func(t *testing.T) {
			f := newConversationMediaFixture(t)
			model := "platform:2"
			if kind == "music" {
				model = "platform:3"
			}
			input := conversationMediaInput{RequestID: strings.Repeat("b", 32), SessionID: "web_media", Capability: kind, Model: model, Prompt: "Calm sea", Duration: 6, Resolution: "720p", Instrumental: true}
			if kind == "video" {
				input.Attachments = []attachments.File{{Path: "storage/input.png"}}
			}
			response := callConversationMedia(f.g, f.cookie, "POST", "/api/conversation/media", input)
			if response.Code != 200 || f.generate.Load() != 1 {
				t.Fatalf("generation failed: %s", response.Body.String())
			}
			if kind == "video" && (f.payload["mode"] != "image_to_video" || f.payload["duration"] != float64(6) || f.payload["resolution"] != "720p") {
				t.Fatalf("wrong video route %v", f.payload)
			}
			if kind == "music" && (f.payload["generation_mode"] != "generate_audio" || f.payload["lyrics_density"] != "none") {
				t.Fatalf("music started lyric drafting %v", f.payload)
			}
		})
	}
}

func TestConversationMediaUncertainAndInvalidSubmissionsNeverReplay(t *testing.T) {
	f := newConversationMediaFixture(t)
	f.uncertain.Store(true)
	input := conversationMediaInput{RequestID: strings.Repeat("c", 32), SessionID: "web_media", Capability: "image", Model: "platform:1", Prompt: "Uncertain image"}
	for attempt := 0; attempt < 3; attempt++ {
		response := callConversationMedia(f.g, f.cookie, "POST", "/api/conversation/media", input)
		if response.Code != 200 || !strings.Contains(response.Body.String(), "submission_unknown") {
			t.Fatal("uncertain result missing", response.Body.String())
		}
	}
	if f.generate.Load() != 1 {
		t.Fatal("uncertain paid request replayed")
	}
	input.RequestID = strings.Repeat("d", 32)
	input.Attachments = []attachments.File{{Path: "../secret.png"}}
	response := callConversationMedia(f.g, f.cookie, "POST", "/api/conversation/media", input)
	if response.Code != 400 || f.generate.Load() != 1 || f.upload.Load() != 0 {
		t.Fatal("unsafe attachment reached platform")
	}
	response = callConversationMedia(f.g, nil, "POST", "/api/conversation/media", input)
	if response.Code != 401 {
		t.Fatal("anonymous generation allowed")
	}
	r := httptest.NewRequest("POST", "http://localhost:8082/api/conversation/media", strings.NewReader(`{}`))
	r.Header.Set("Origin", "https://untrusted.example")
	r.AddCookie(f.cookie)
	w := httptest.NewRecorder()
	f.g.handleConversationMedia(w, r)
	if w.Code != 403 {
		t.Fatal("foreign origin allowed")
	}
}
