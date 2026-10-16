package app

import (
	"io"
	"log"
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func newAEPersistenceTestGateway(t *testing.T) *GatewayService {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&ChatMessage{}, &ChatSession{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return &GatewayService{
		db:         db,
		logger:     log.New(io.Discard, "", 0),
		sseClients: make(map[string]map[string]chan Event),
		tasks:      make(map[string]*GatewayTask),
	}
}

func TestAESavePipelineResultToChatUsesFinalStatus(t *testing.T) {
	gw := newAEPersistenceTestGateway(t)
	mgr := &AIEmployeeManager{
		employees: map[string]*AIEmployeeConfig{
			"lead": {ID: "lead", Name: "潜客研究员"},
		},
		gateway: gw,
		logger:  log.New(io.Discard, "", 0),
	}

	record := &AEWorkRecord{
		EmployeeID: "lead",
		SessionID:  "ae_session_1",
		ExecID:     "pipe_lead_123",
		Status:     "completed",
		UpdatedAt:  time.Now(),
	}
	userMsg, err := gw.saveChatMessage(record.SessionID, "user", "", "@潜客研究员 找客户", "running", "pipe_lead_123-de", "web", "")
	if err != nil {
		t.Fatalf("save user message: %v", err)
	}
	mgr.savePipelineResultToChat(record, "最终报告 <tool_call>secret</tool_call>")

	var msg ChatMessage
	if err := gw.db.Where("session_id = ? AND role = ?", "ae_session_1", "assistant").First(&msg).Error; err != nil {
		t.Fatalf("assistant message not saved: %v", err)
	}
	if msg.Status != "completed" {
		t.Fatalf("status = %q, want completed", msg.Status)
	}
	if msg.AgentName != "潜客研究员" {
		t.Fatalf("agent = %q", msg.AgentName)
	}
	if msg.TaskID != "pipe_lead_123-de" {
		t.Fatalf("task id = %q", msg.TaskID)
	}
	if strings.Contains(msg.Content, "tool_call") {
		t.Fatalf("tool calls should be stripped from saved content: %q", msg.Content)
	}
	var updatedUser ChatMessage
	if err := gw.db.First(&updatedUser, userMsg.ID).Error; err != nil {
		t.Fatalf("reload user message: %v", err)
	}
	if updatedUser.Status != "completed" {
		t.Fatalf("user status = %q, want completed", updatedUser.Status)
	}
}

func TestAEUserMessageTaskIsReboundToExecutionTask(t *testing.T) {
	gw := newAEPersistenceTestGateway(t)
	userMsg, err := gw.saveChatMessage("ae_session_2", "user", "", "@潜客研究员 做潜客报告", "pending", "ae_pending_1", "web", "")
	if err != nil {
		t.Fatalf("save user message: %v", err)
	}

	gw.updateAEUserMessageTask(userMsg, "pipe_lead-research_20260703_153550-de", "running")

	var msg ChatMessage
	if err := gw.db.First(&msg, userMsg.ID).Error; err != nil {
		t.Fatalf("reload message: %v", err)
	}
	if msg.TaskID != "pipe_lead-research_20260703_153550-de" {
		t.Fatalf("task id = %q", msg.TaskID)
	}
	if msg.Status != "running" {
		t.Fatalf("status = %q, want running", msg.Status)
	}
}
