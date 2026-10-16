package app

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

func newSessionListTestGateway(t *testing.T) (*GatewayService, *gorm.DB) {
	t.Helper()
	db, err := gorm.Open(
		sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"),
		&gorm.Config{Logger: gormlogger.Default.LogMode(gormlogger.Silent)},
	)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get database handle: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })

	if err := db.AutoMigrate(&ChatSession{}, &ChatMessage{}, &GatewayTaskRecord{}); err != nil {
		t.Fatalf("migrate chat tables: %v", err)
	}
	return &GatewayService{db: db, logger: log.New(io.Discard, "", 0)}, db
}

func TestListSessionsIncludesFirstUserQuestion(t *testing.T) {
	gateway, db := newSessionListTestGateway(t)

	now := time.Now()
	session := ChatSession{
		ID:           "session_first_question",
		Name:         "session_first_question",
		MessageCount: 3,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := db.Create(&session).Error; err != nil {
		t.Fatalf("create session: %v", err)
	}
	messages := []ChatMessage{
		{SessionID: session.ID, Role: "assistant", Content: "欢迎使用", CreatedAt: now.Add(-3 * time.Minute)},
		{SessionID: session.ID, Role: "user", Content: "再补充现金流分析", CreatedAt: now.Add(-time.Minute)},
		{SessionID: session.ID, Role: "user", Content: "请帮我分析这份季度经营报告", CreatedAt: now.Add(-2 * time.Minute)},
	}
	if err := db.Create(&messages).Error; err != nil {
		t.Fatalf("create messages: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/sessions", nil)
	rec := httptest.NewRecorder()
	gateway.handleListSessions(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("list sessions status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var payload struct {
		Sessions []ChatSession `json:"sessions"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(payload.Sessions) != 1 {
		t.Fatalf("sessions count = %d, want 1", len(payload.Sessions))
	}
	if got := payload.Sessions[0].FirstQuestion; got != messages[2].Content {
		t.Fatalf("first_question = %q, want %q", got, messages[2].Content)
	}
}

func TestSessionSearchIncludesFirstUserQuestion(t *testing.T) {
	gateway, db := newSessionListTestGateway(t)

	now := time.Now()
	session := ChatSession{
		ID:           "session_search_first_question",
		Name:         "session_search_first_question",
		MessageCount: 3,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := db.Create(&session).Error; err != nil {
		t.Fatalf("create session: %v", err)
	}
	messages := []ChatMessage{
		{SessionID: session.ID, Role: "user", Content: "Please analyze the quarterly operations report", CreatedAt: now.Add(-3 * time.Minute)},
		{SessionID: session.ID, Role: "assistant", Content: "Initial analysis", CreatedAt: now.Add(-2 * time.Minute)},
		{SessionID: session.ID, Role: "user", Content: "Add a cash flow view", CreatedAt: now.Add(-time.Minute)},
	}
	if err := db.Create(&messages).Error; err != nil {
		t.Fatalf("create messages: %v", err)
	}
	if err := db.Exec("CREATE VIRTUAL TABLE chat_messages_fts USING fts5(session_id, content, tokenize='unicode61')").Error; err != nil {
		t.Fatalf("create search index: %v", err)
	}
	if err := db.Exec("INSERT INTO chat_messages_fts(rowid, session_id, content) SELECT id, session_id, content FROM chat_messages").Error; err != nil {
		t.Fatalf("populate search index: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/sessions/search?q=cash", nil)
	rec := httptest.NewRecorder()
	gateway.handleSessionSearch(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("search sessions status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var payload struct {
		Sessions []struct {
			SessionID     string `json:"session_id"`
			Snippet       string `json:"snippet"`
			FirstQuestion string `json:"first_question"`
		} `json:"sessions"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(payload.Sessions) != 1 {
		t.Fatalf("sessions count = %d, want 1", len(payload.Sessions))
	}
	if got := payload.Sessions[0].FirstQuestion; got != messages[0].Content {
		t.Fatalf("first_question = %q, want %q", got, messages[0].Content)
	}
	if got := payload.Sessions[0].Snippet; got != messages[2].Content {
		t.Fatalf("search snippet = %q, want matching message %q", got, messages[2].Content)
	}
}

func TestLegacySessionAPIsHideAgentOSSessions(t *testing.T) {
	gateway, db := newSessionListTestGateway(t)
	now := time.Now().Round(0)
	sessions := []ChatSession{
		{
			ID: "legacy-session", Name: "Legacy", ProjectID: gateway.project.ProjectID,
			MessageCount: 1, CreatedAt: now, UpdatedAt: now,
		},
		{
			ID: "agentos-session", Name: "AgentOS", ProjectID: gateway.project.ProjectID,
			MessageCount: 1, CreatedAt: now, UpdatedAt: now,
		},
		{
			ID: "mixed-session", Name: "Mixed", ProjectID: gateway.project.ProjectID,
			MessageCount: 2, CreatedAt: now, UpdatedAt: now,
		},
	}
	if err := db.Create(&sessions).Error; err != nil {
		t.Fatal(err)
	}
	messages := []ChatMessage{
		{
			SessionID: "legacy-session", Role: "user",
			Content: "legacy boundary keyword", TaskID: "legacy-task", CreatedAt: now,
		},
		{
			SessionID: "agentos-session", Role: "user",
			Content: "agentos boundary keyword secret", TaskID: "agentos-task", CreatedAt: now,
		},
		{
			SessionID: "mixed-session", Role: "user",
			Content: "mixed boundary keyword secret", TaskID: "mixed-agentos-task", CreatedAt: now,
		},
	}
	if err := db.Create(&messages).Error; err != nil {
		t.Fatal(err)
	}
	tasks := []GatewayTaskRecord{
		{
			TaskID: "agentos-task", SessionID: "agentos-session",
			Status: "completed", QueueName: agentOSQueueName, CreatedAt: now,
		},
		{
			TaskID: "mixed-agentos-task", SessionID: "mixed-session",
			Status: "completed", QueueName: agentOSQueueName, CreatedAt: now,
		},
	}
	if err := db.Create(&tasks).Error; err != nil {
		t.Fatal(err)
	}

	listed := httptest.NewRecorder()
	gateway.handleListSessions(
		listed,
		httptest.NewRequest(http.MethodGet, "/api/sessions", nil),
	)
	if listed.Code != http.StatusOK ||
		!strings.Contains(listed.Body.String(), "legacy-session") ||
		strings.Contains(listed.Body.String(), "agentos-session") ||
		strings.Contains(listed.Body.String(), "mixed-session") {
		t.Fatalf("session list crossed AgentOS boundary: status=%d body=%s", listed.Code, listed.Body.String())
	}

	for name, testCase := range map[string]struct {
		handler http.HandlerFunc
		target  string
	}{
		"messages": {gateway.handleSessionMessages, "/api/session/agentos-session"},
		"export":   {gateway.handleSessionExport, "/api/session/export/agentos-session"},
	} {
		recorder := httptest.NewRecorder()
		testCase.handler(
			recorder,
			httptest.NewRequest(http.MethodGet, testCase.target, nil),
		)
		if recorder.Code != http.StatusNotFound {
			t.Fatalf("%s exposed AgentOS session: status=%d body=%s", name, recorder.Code, recorder.Body.String())
		}
	}

	if err := db.Exec("CREATE VIRTUAL TABLE chat_messages_fts USING fts5(session_id, content, tokenize='unicode61')").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO chat_messages_fts(rowid, session_id, content) SELECT id, session_id, content FROM chat_messages").Error; err != nil {
		t.Fatal(err)
	}
	searched := httptest.NewRecorder()
	gateway.handleSessionSearch(
		searched,
		httptest.NewRequest(http.MethodGet, "/api/sessions/search?q=boundary", nil),
	)
	if searched.Code != http.StatusOK ||
		!strings.Contains(searched.Body.String(), "legacy-session") ||
		strings.Contains(searched.Body.String(), "agentos-session") ||
		strings.Contains(searched.Body.String(), "mixed-session") {
		t.Fatalf("session search crossed AgentOS boundary: status=%d body=%s", searched.Code, searched.Body.String())
	}
}
