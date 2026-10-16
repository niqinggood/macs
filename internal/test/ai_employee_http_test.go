package app

import (
	"bytes"
	"context"
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

func newAEHTTPTestGateway(t *testing.T) *GatewayService {
	t.Helper()
	workspace := t.TempDir()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: gormlogger.Default.LogMode(gormlogger.Silent)})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("sqlite db handle: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	if err := db.AutoMigrate(&ChatMessage{}, &ChatSession{}, &GatewayTaskRecord{}, &PipelineExecutionRecord{}, &AEWorkRecordDB{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	db.Exec("CREATE TABLE IF NOT EXISTS de_notifications (employee_id TEXT, supervisor TEXT, channel TEXT, message TEXT, created_at DATETIME)")
	oldRegistry := globalProviderRegistry
	t.Cleanup(func() { globalProviderRegistry = oldRegistry })
	globalProviderRegistry = &ModelProviderRegistry{
		providers: map[string]*ModelProvider{
			"test": {
				Name:    "test",
				Label:   "Test",
				Type:    "openai",
				BaseURL: "https://example.test/v1",
				APIKey:  "sk-test-valid-key",
				Models:  []ProviderModel{{Name: "test-text", Capability: "text-to-text"}},
			},
		},
		logger: log.New(io.Discard, "", 0),
	}
	gw := &GatewayService{
		db:               db,
		logger:           log.New(io.Discard, "", 0),
		sseClients:       make(map[string]map[string]chan Event),
		tasks:            make(map[string]*GatewayTask),
		taskStreams:      make(map[string]chan map[string]interface{}),
		taskProgressLogs: make(map[string][]string),
		project:          NewProjectContext(workspace),
		config: &Config{Workspace: workspace, Pipeline: PipelineConfig{
			Pipelines: make(map[string]Pipeline),
		}},
	}
	gw.aeManager = NewAIEmployeeManager(gw, log.New(io.Discard, "", 0))
	gw.aeManager.Register([]AIEmployeeConfig{{
		ID:     "lead-researcher",
		Name:   "潜客研究员",
		AtName: "潜客研究员",
		Direct: &DirectConfig{Prompt: "test"},
	}})
	return gw
}

func seedAEHTTPWebSession(t *testing.T, gw *GatewayService, sessionID string) {
	t.Helper()
	workspace := gw.currentProjectWorkspace()
	session := ChatSession{
		ID: sessionID, Name: sessionID, Workspace: workspace, ProjectID: gw.project.ProjectID,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	if err := gw.db.Create(&session).Error; err != nil {
		t.Fatalf("seed server-created web session %s: %v", sessionID, err)
	}
}

func TestAEListCodexSessionAliveDoesNotMeanBusy(t *testing.T) {
	gw := newAEHTTPTestGateway(t)
	gw.aeManager.Register([]AIEmployeeConfig{{
		ID:     "codex-specialist",
		Name:   "codex专员",
		AtName: "codex专员",
		Direct: &DirectConfig{Prompt: "codex"},
	}})
	gw.codexInteractive = &CodexInteractiveManager{
		gateway: gw,
		logger:  log.New(io.Discard, "", 0),
		active: map[string]*CodexInteractiveSession{
			"s1": {
				SessionID: "s1",
				TaskID:    "s1-codex-de",
				WorkDir:   t.TempDir(),
				lastUsed:  time.Now(),
				done:      make(chan struct{}),
			},
		},
	}

	rec := httptest.NewRecorder()
	gw.handleAEList(rec, httptest.NewRequest(http.MethodGet, "/api/ae/list?include_hidden=1", nil))

	var body struct {
		Employees []struct {
			ID           string `json:"id"`
			Status       string `json:"status"`
			SessionAlive bool   `json:"session_alive"`
			ActiveTurns  int    `json:"active_turns"`
		} `json:"employees"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	var codex *struct {
		ID           string `json:"id"`
		Status       string `json:"status"`
		SessionAlive bool   `json:"session_alive"`
		ActiveTurns  int    `json:"active_turns"`
	}
	for i := range body.Employees {
		if body.Employees[i].ID == "codex-specialist" {
			codex = &body.Employees[i]
			break
		}
	}
	if codex == nil {
		t.Fatalf("codex employee missing from response: %+v", body.Employees)
	}
	if codex.Status != "idle" || !codex.SessionAlive || codex.ActiveTurns != 0 {
		t.Fatalf("codex status = %+v, want idle with live session and no active turn", *codex)
	}

	gw.codexInteractive.active["s1"].activeTurnID = "turn-1"
	rec = httptest.NewRecorder()
	gw.handleAEList(rec, httptest.NewRequest(http.MethodGet, "/api/ae/list?include_hidden=1", nil))
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode busy list: %v", err)
	}
	codex = nil
	for i := range body.Employees {
		if body.Employees[i].ID == "codex-specialist" {
			codex = &body.Employees[i]
			break
		}
	}
	if codex == nil || codex.Status != "busy" || codex.ActiveTurns != 1 {
		t.Fatalf("codex active turn status = %+v, want busy with one active turn", codex)
	}
}

func TestLoadWorkRecordsReconcilesStaleCodexRunningRecords(t *testing.T) {
	gw := newAEHTTPTestGateway(t)
	now := time.Now()
	records := []AEWorkRecordDB{
		{RecordID: "r-stale", EmployeeID: "codex-specialist", Task: "old", Pipeline: "external_agent", ExecID: "external_codex_stale", Status: "running", CreatedAt: now, UpdatedAt: now},
		{RecordID: "r-done", EmployeeID: "codex-specialist", Task: "done", Pipeline: "external_agent", ExecID: "external_codex_done", Status: "running", CreatedAt: now, UpdatedAt: now},
	}
	if err := gw.db.Create(&records).Error; err != nil {
		t.Fatalf("seed work records: %v", err)
	}
	if err := gw.db.Create(&GatewayTaskRecord{
		TaskID:    "external_codex_done-de",
		TaskType:  "codex_interactive",
		Status:    "completed",
		Result:    "done result",
		CreatedAt: now,
		UpdatedAt: now,
	}).Error; err != nil {
		t.Fatalf("seed gateway task: %v", err)
	}

	gw.aeManager.loadWorkRecordsFromDB()

	byRecordID := map[string]*AEWorkRecord{}
	for _, rec := range gw.aeManager.workLog["codex-specialist"] {
		byRecordID[rec.ID] = rec
	}
	if got := byRecordID["r-stale"]; got == nil || got.Status != "stopped" {
		t.Fatalf("stale record = %+v, want stopped", got)
	}
	if got := byRecordID["r-done"]; got == nil || got.Status != "completed" || got.Result != "done result" {
		t.Fatalf("completed record = %+v, want completed with gateway result", got)
	}

	var staleDB AEWorkRecordDB
	if err := gw.db.Where("record_id = ?", "r-stale").First(&staleDB).Error; err != nil {
		t.Fatalf("load stale db record: %v", err)
	}
	if staleDB.Status != "stopped" {
		t.Fatalf("stale db status = %q, want stopped", staleDB.Status)
	}
}

type staticAEHTTPAgent struct {
	output string
}

func (a *staticAEHTTPAgent) Name() string { return "static" }

func (a *staticAEHTTPAgent) Generate(ctx context.Context, input string) (string, error) {
	return a.output, nil
}

func (a *staticAEHTTPAgent) GetType() AgentType { return AgentTypeAutoGenAssistant }

func TestHandleChatMentionPersistsUserMessageBeforeDispatchFailure(t *testing.T) {
	gw := newAEHTTPTestGateway(t)
	seedAEHTTPWebSession(t, gw, "ae_http_session")
	body := []byte(`{
		"message":"@潜客研究员 帮我寻找AI平台的潜在客户",
		"session_id":"ae_http_session",
		"agent_name":"Researcher",
		"channel":"web",
		"user_id":"u1"
	}`)

	req := httptest.NewRequest(http.MethodPost, "/api/chat", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	gw.handleChat(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	var resp map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["type"] != "error" {
		t.Fatalf("response type = %v, want error", resp["type"])
	}

	var msg ChatMessage
	if err := gw.db.Where("session_id = ? AND role = ?", "ae_http_session", "user").First(&msg).Error; err != nil {
		t.Fatalf("@mention user message not persisted: %v", err)
	}
	if !strings.Contains(msg.Content, "@潜客研究员") {
		t.Fatalf("saved content = %q", msg.Content)
	}
	if msg.Status != "failed" {
		t.Fatalf("message status = %q, want failed", msg.Status)
	}
	if !strings.HasPrefix(msg.TaskID, "ae_pending_") {
		t.Fatalf("task id = %q, want provisional ae_pending_*", msg.TaskID)
	}
}

func TestHandleChatMentionRunsDirectEmployeeAndSavesAssistantResult(t *testing.T) {
	gw := newAEHTTPTestGateway(t)
	seedAEHTTPWebSession(t, gw, "ae_http_success")
	factory := &AgentFactory{
		subAgents: make(map[string]Agent),
		testSubAgentCreator: func(config AgentConfig, parentTask string) (Agent, error) {
			return &staticAEHTTPAgent{output: "AI员工：潜客研究员\n已生成潜客报告。"}, nil
		},
	}
	gw.pipelineExec = NewPipelineExecutor(factory, gw, log.New(io.Discard, "", 0))

	body := []byte(`{
		"message":"@潜客研究员 帮我寻找AI平台的潜在客户",
		"session_id":"ae_http_success",
		"agent_name":"Researcher",
		"channel":"web",
		"user_id":"u1"
	}`)

	req := httptest.NewRequest(http.MethodPost, "/api/chat", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	gw.handleChat(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	var resp GatewayResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Status != "running" || resp.TaskID == "" {
		t.Fatalf("unexpected response: %#v", resp)
	}

	var userMsg ChatMessage
	if err := gw.db.Where("session_id = ? AND role = ?", "ae_http_success", "user").First(&userMsg).Error; err != nil {
		t.Fatalf("user message not persisted: %v", err)
	}
	if userMsg.TaskID != resp.TaskID {
		t.Fatalf("user task id = %q, response task id = %q", userMsg.TaskID, resp.TaskID)
	}

	deadline := time.Now().Add(7 * time.Second)
	var assistantMsg ChatMessage
	for time.Now().Before(deadline) {
		err := gw.db.Where("session_id = ? AND role = ?", "ae_http_success", "assistant").First(&assistantMsg).Error
		if err == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if assistantMsg.ID == 0 {
		t.Fatal("assistant result was not persisted")
	}
	if assistantMsg.TaskID != resp.TaskID {
		t.Fatalf("assistant task id = %q, response task id = %q", assistantMsg.TaskID, resp.TaskID)
	}
	if assistantMsg.Status != "completed" {
		t.Fatalf("assistant status = %q, want completed", assistantMsg.Status)
	}
	if !strings.Contains(assistantMsg.Content, "潜客报告") {
		t.Fatalf("assistant content = %q", assistantMsg.Content)
	}
}
