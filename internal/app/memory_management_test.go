package app

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
	memorycore "mac/internal/memory"
)

func TestPersonalMemoryLocalManagement(t *testing.T) {
	workspace := t.TempDir()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "memory.db")), &gorm.Config{Logger: gormlogger.Default.LogMode(gormlogger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	if err := memorycore.InitMemoryDB(db); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		sqlDB, _ := db.DB()
		if sqlDB != nil {
			_ = sqlDB.Close()
		}
	})
	config := &Config{Workspace: workspace, Agents: []AgentConfig{{Name: "assistant"}}}
	gateway := &GatewayService{config: config, engine: &MultiAgentEngine{config: config, agentFactory: &AgentFactory{agents: map[string]Agent{"assistant": &LLMAgent{}}}}, db: db}
	service := memorycore.NewService(db, workspace, "assistant")
	owner := memorycore.Owner{TenantID: defaultExecutionTenantID, PartitionID: defaultExecutionTenantID}
	if err := service.Record(context.Background(), owner, "format", "PDF", memorycore.MemoryUser, "preference", "candidate", 5, nil, 0); err != nil {
		t.Fatal(err)
	}
	request := func(method, path string, body []byte, remote string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, "http://localhost:8090"+path, bytes.NewReader(body))
		r.RemoteAddr = remote
		r.Header.Set("Origin", "http://localhost:8090")
		if method == http.MethodPost {
			r.Header.Set("Content-Type", "application/json")
		}
		w := httptest.NewRecorder()
		gateway.handlePersonalMemory(w, r)
		return w
	}
	if got := request(http.MethodGet, "/api/memory", nil, "192.0.2.1:5000").Code; got != http.StatusForbidden {
		t.Fatalf("remote access: %d", got)
	}
	foreignOrigin := httptest.NewRequest(http.MethodGet, "http://localhost:8090/api/memory", nil)
	foreignOrigin.RemoteAddr = "127.0.0.1:5000"
	foreignOrigin.Header.Set("Origin", "https://example.test")
	foreignResponse := httptest.NewRecorder()
	gateway.handlePersonalMemory(foreignResponse, foreignOrigin)
	if foreignResponse.Code != http.StatusForbidden {
		t.Fatalf("cross-origin access: %d", foreignResponse.Code)
	}
	if got := request(http.MethodGet, "/api/memory?agent=foreign", nil, "127.0.0.1:5000").Code; got == http.StatusOK {
		t.Fatal("foreign agent accepted")
	}
	w := request(http.MethodGet, "/api/memory?state=candidate", nil, "127.0.0.1:5000")
	if w.Code != http.StatusOK {
		t.Fatalf("list: %d %s", w.Code, w.Body.String())
	}
	var result struct {
		Entries []memorycore.Entry `json:"entries"`
		Total   int                `json:"total"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || result.Total != 1 || len(result.Entries) != 1 {
		t.Fatalf("list: %#v %v", result, err)
	}
	action := func(name, value string, version int) *httptest.ResponseRecorder {
		body, _ := json.Marshal(map[string]interface{}{"agent": "assistant", "action": name, "key": "format", "scope": "user", "value": value, "expected_version": version})
		return request(http.MethodPost, "/api/memory", body, "127.0.0.1:5000")
	}
	created, _ := json.Marshal(map[string]interface{}{"agent": "assistant", "action": "create", "key": "manual", "value": "Keep this", "scope": "project", "kind": "fact"})
	if got := request(http.MethodPost, "/api/memory", created, "127.0.0.1:5000").Code; got != http.StatusOK {
		t.Fatalf("create: %d", got)
	}
	if got := request(http.MethodPost, "/api/memory", created, "127.0.0.1:5000").Code; got != http.StatusConflict {
		t.Fatalf("duplicate create: %d", got)
	}
	manual, err := service.Lookup(context.Background(), owner, "manual", memorycore.MemoryProject)
	if err != nil || manual == nil || manual.Value != "Keep this" || manual.Version != 1 || manual.State != "active" {
		t.Fatalf("manual memory: %#v %v", manual, err)
	}
	if got := action("confirm", "", 2).Code; got != http.StatusConflict {
		t.Fatalf("stale confirm: %d", got)
	}
	if got := action("confirm", "", 1).Code; got != http.StatusOK {
		t.Fatalf("confirm: %d", got)
	}
	if got := action("correct", "DOCX", 2).Code; got != http.StatusOK {
		t.Fatalf("correct: %d", got)
	}
	if got := action("forget", "", 2).Code; got != http.StatusConflict {
		t.Fatalf("stale forget: %d", got)
	}
	if got := action("forget", "", 3).Code; got != http.StatusOK {
		t.Fatalf("forget: %d", got)
	}
	w = request(http.MethodGet, "/api/memory/export", nil, "127.0.0.1:5000")
	if w.Code != http.StatusOK || bytes.Contains(w.Body.Bytes(), []byte("DOCX")) {
		t.Fatalf("export after forget: %d %s", w.Code, w.Body.String())
	}
}
