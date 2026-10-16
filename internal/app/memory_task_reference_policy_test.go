package app

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"gorm.io/datatypes"
	memorycore "mac/internal/memory"
)

func TestPromptTaskReferencePolicyPreservesHistoryAndSourceAuthority(t *testing.T) {
	g := memorySourceFixture(t)
	r := httptest.NewRequest(http.MethodGet, "http://localhost:8090/api/memory", nil)
	r.RemoteAddr = "127.0.0.1:40000"
	create := func(event memorycore.MemoryEvent) memorycore.MemoryEvent {
		t.Helper()
		event.TenantID, event.AgentName, event.SessionID, event.UserChannel = "default", "first", "source-web", "web"
		if event.UserID == "" {
			event.UserID = "fixture-user"
		}
		if event.Status == "" {
			event.Status = "success"
		}
		if err := g.db.Create(&event).Error; err != nil {
			t.Fatal(err)
		}
		return event
	}
	plain := create(memorycore.MemoryEvent{UserQuestion: "hi", FinalAnswer: "PLAIN_CHAT", LearnNote: "PLAIN_CHAT"})
	tool := create(memorycore.MemoryEvent{UserQuestion: "好", FinalAnswer: "VERIFIED_REPORT saved and checked", LearnNote: "VERIFIED_REPORT", CallChain: datatypes.JSON(`[{"type":"tool","name":"write_file","success":true}]`)})
	failed := create(memorycore.MemoryEvent{Status: "failed", FinalAnswer: "FAILURE_RESULT", ErrorMsg: "verified failure", LearnNote: "INDEPENDENT_LESSON"})
	foreign := create(memorycore.MemoryEvent{UserID: "foreign-user", FinalAnswer: "FOREIGN_RESULT", CallChain: datatypes.JSON(`[{"type":"tool","name":"read_file","success":true}]`)})
	quote := "以后每次 Word 报告使用宋体。"
	if err := g.db.Create(&ChatMessage{TenantID: "default", AgentName: "first", SessionID: "source-web", UserId: "fixture-user", Role: "user", Content: quote}).Error; err != nil {
		t.Fatal(err)
	}
	knowledge := memorycore.Knowledge{TenantID: "default", AgentName: "first", KnowledgeID: "independent-knowledge", SourceID: strconv.FormatUint(uint64(plain.ID), 10), Title: "A separately saved procedure", Summary: "SAVED_KNOWLEDGE"}
	if err := g.db.Create(&knowledge).Error; err != nil {
		t.Fatal(err)
	}
	history, err := g.personalMemorySourceReferences(r, "first", "", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	rows, err := g.personalMemoryPromptSourceReferences(r, "first", "", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	historical, selected := map[string]bool{}, map[string]bool{}
	for _, row := range history {
		historical[row.SourceType+":"+row.SourceID] = true
	}
	for _, row := range rows {
		selected[row.SourceType+":"+row.SourceID] = true
		if strings.Contains(row.Value, "FOREIGN_RESULT") {
			t.Fatal("foreign source leaked")
		}
	}
	key := func(kind string, id uint) string { return kind + ":" + strconv.FormatUint(uint64(id), 10) }
	if !historical[key("task_execution", plain.ID)] || !historical[key("task_insight", plain.ID)] {
		t.Fatal("ordinary historical record was removed")
	}
	if selected[key("task_execution", plain.ID)] || selected[key("task_insight", plain.ID)] || selected[key("task_execution", foreign.ID)] {
		t.Fatal("unqualified task became a candidate")
	}
	if !selected[key("task_execution", tool.ID)] || selected[key("task_insight", tool.ID)] || !selected[key("task_execution", failed.ID)] || !selected[key("task_insight", failed.ID)] {
		t.Fatal("useful execution/lesson selection or fragment deduplication failed")
	}
	seenStatement, seenKnowledge := false, false
	for _, row := range rows {
		seenStatement = seenStatement || row.SourceType == "user_statement" && row.Value == quote
		seenKnowledge = seenKnowledge || row.SourceType == "learning_knowledge" && row.Value == "SAVED_KNOWLEDGE"
	}
	if !seenStatement || !seenKnowledge {
		t.Fatal("independent explicit statement or original knowledge mapping was filtered")
	}
	var persisted memorycore.MemoryEvent
	if err := g.db.First(&persisted, plain.ID).Error; err != nil || persisted.FinalAnswer != "PLAIN_CHAT" {
		t.Fatal("candidate filtering changed persisted history", err)
	}
}

func TestPromptTaskReferencePolicyVerifiesDerivedParent(t *testing.T) {
	g := memorySourceFixture(t)
	r := httptest.NewRequest(http.MethodGet, "http://localhost:8090/api/memory", nil)
	r.RemoteAddr = "127.0.0.1:40000"
	parent := memorycore.MemoryEvent{TenantID: "default", AgentName: "first", UserID: "fixture-user", UserChannel: "web", SessionID: "source-web", Status: "failed", FinalAnswer: "original execution", CallChain: datatypes.JSON(`[{"type":"tool","name":"browser","success":false}]`)}
	if err := g.db.Create(&parent).Error; err != nil {
		t.Fatal(err)
	}
	lesson := memorycore.NewLearningEventWithEvidence(&parent, "failure_lesson", "derived", "ORIGINAL_FAILURE_LESSON")
	if err := g.db.Create(lesson).Error; err != nil {
		t.Fatal(err)
	}
	read := func() bool {
		t.Helper()
		rows, err := g.personalMemoryPromptSourceReferences(r, "first", "", "", "", "")
		if err != nil {
			t.Fatal(err)
		}
		for _, row := range rows {
			if row.SourceID == strconv.FormatUint(uint64(lesson.ID), 10) && row.SourceType == "task_execution" {
				return true
			}
		}
		return false
	}
	if !read() {
		t.Fatal("host-derived failure lesson omitted")
	}
	if err := g.db.Model(&memorycore.MemoryEvent{}).Where("id = ?", parent.ID).Update("user_id", "foreign-user").Error; err != nil {
		t.Fatal(err)
	}
	if read() {
		t.Fatal("lesson retained authority after its parent changed owner")
	}
}
