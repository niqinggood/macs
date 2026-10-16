package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	memorycore "mac/internal/memory"
)

func TestPersonalMemoryLearningKnowledgeUsesExistingVerifiedSource(t *testing.T) {
	g := memorySourceFixture(t)
	event := memorycore.MemoryEvent{TenantID: "default", AgentName: "first", UserID: "fixture-user", UserChannel: "web", SessionID: "source-web", Status: "success", UserQuestion: "核对项目部署", FinalAnswer: "已有任务结果"}
	if err := g.db.Create(&event).Error; err != nil {
		t.Fatal(err)
	}
	failed := memorycore.MemoryEvent{TenantID: "default", AgentName: "first", UserID: "fixture-user", UserChannel: "web", SessionID: "source-web", Status: "failed"}
	if err := g.db.Create(&failed).Error; err != nil {
		t.Fatal(err)
	}
	created := time.Date(2026, 8, 1, 9, 10, 11, 0, time.UTC)
	for _, row := range []memorycore.Knowledge{
		{TenantID: "default", AgentName: "first", KnowledgeID: "verified-lesson", SourceID: strconv.FormatUint(uint64(event.ID), 10), SourceType: "learning", Category: "lesson", Title: "先检查配置", Summary: "已有学习经验", Content: "摘要后面的完整材料不应自动展开", UseCount: 7, CreatedAt: created, UpdatedAt: created.Add(time.Hour)},
		{TenantID: "default", AgentName: "first", KnowledgeID: "verified-fact", SourceID: strconv.FormatUint(uint64(event.ID), 10), SourceType: "extraction", Category: "fact", Title: "项目说明", Content: "已有项目资料", UseCount: 3, CreatedAt: created, UpdatedAt: created},
		{TenantID: "default", AgentName: "first", KnowledgeID: "verified-failure", SourceID: strconv.FormatUint(uint64(failed.ID), 10), SourceType: "feedback", Category: "lesson", Title: "失败后的经验", Content: "已有失败经验", UseCount: 2, CreatedAt: created, UpdatedAt: created},
	} {
		if err := g.db.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
	}
	w := memoryScopeRequest(t, g, http.MethodGet, "/api/memory?agent=first", nil)
	var response struct {
		Supplemental []memorycore.WorkingReference
		Entries      []memorycore.Entry
		Total        int64
	}
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &response) != nil {
		t.Fatalf("learning sources unavailable: %d %s", w.Code, w.Body.String())
	}
	learned := 0
	for _, item := range response.Supplemental {
		if item.SourceType != "learning_knowledge" {
			continue
		}
		learned++
		if !item.ReadOnly || item.SessionID != "source-web" || item.CreatedAt == nil || !item.CreatedAt.Equal(created) || !strings.HasPrefix(item.ID, "learning:") {
			t.Fatalf("learning source ownership or time lost: %#v", item)
		}
		if item.Key == "先检查配置" && (item.Value != "已有学习经验" || item.Kind != "experience") || item.Key == "项目说明" && (item.Value != "已有项目资料" || item.Kind != "fact") {
			t.Fatalf("wrong learning source content or category: %#v", item)
		}
		if item.Key == "先检查配置" && item.OriginalContent != "摘要后面的完整材料不应自动展开" {
			t.Fatalf("original knowledge body unavailable for explicit reading: %#v", item)
		}
	}
	// OriginalContent is a separate existing read-only detail field. Value
	// remains the bounded summary used by recall; no Entry is promoted.
	if learned != 3 || response.Total != 0 || len(response.Entries) != 0 {
		t.Fatal("old knowledge was lost, expanded or promoted to an Entry")
	}
	for _, path := range []string{"/api/memory?agent=first&scope=global", "/api/memory?agent=first&state=candidate", "/api/memory?agent=first&kind=preference"} {
		w := memoryScopeRequest(t, g, http.MethodGet, path, nil)
		if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &response) != nil {
			t.Fatalf("filter unavailable: %s %d", path, w.Code)
		}
		for _, item := range response.Supplemental {
			if item.SourceType == "learning_knowledge" {
				t.Fatal("learning knowledge escaped global/candidate/preference filter")
			}
		}
	}
	var rows []memorycore.Knowledge
	if err := g.db.Find(&rows).Error; err != nil || len(rows) != 3 {
		t.Fatal("viewing memory updated the legacy knowledge store", err)
	}
	expected := map[string]int{"verified-lesson": 7, "verified-fact": 3, "verified-failure": 2}
	for _, row := range rows {
		if count, found := expected[row.KnowledgeID]; !found || row.UseCount != count {
			t.Fatal("viewing memory updated an existing knowledge usage count")
		}
	}
}

func TestPersonalMemoryLearningKnowledgeRejectsUnverifiableOwners(t *testing.T) {
	g := memorySourceFixture(t)
	valid := memorycore.MemoryEvent{TenantID: "default", AgentName: "first", UserID: "fixture-user", SessionID: "source-web", UserChannel: "web", Status: "success"}
	if err := g.db.Create(&valid).Error; err != nil {
		t.Fatal(err)
	}
	foreignWorkspace := ChatSession{ID: "other-learning-workspace", TenantID: "default", AgentName: "first", UserId: "fixture-user", Workspace: t.TempDir(), ProjectID: g.project.ProjectID}
	if err := g.db.Create(&foreignWorkspace).Error; err != nil {
		t.Fatal(err)
	}
	foreignEvents := []memorycore.MemoryEvent{
		{TenantID: "other", AgentName: "first", UserID: "fixture-user", SessionID: "source-web", Status: "success"},
		{TenantID: "default", AgentName: "second", UserID: "fixture-user", SessionID: "source-web", Status: "success"},
		{TenantID: "default", AgentName: "first", UserID: "other", SessionID: "source-web", Status: "success"},
		{TenantID: "default", AgentName: "first", UserID: "fixture-user", SessionID: "source-web", UserChannel: "api", Status: "success"},
		{TenantID: "default", AgentName: "first", UserID: "fixture-user", SessionID: "source-web", Status: "pending"},
		{TenantID: "default", AgentName: "first", UserID: "fixture-user", SessionID: "unbound", Status: "success"},
		{TenantID: "default", AgentName: "first", UserID: "fixture-user", SessionID: foreignWorkspace.ID, Status: "success"},
	}
	for index, event := range foreignEvents {
		if err := g.db.Create(&event).Error; err != nil {
			t.Fatal(err)
		}
		row := memorycore.Knowledge{TenantID: "default", AgentName: "first", KnowledgeID: fmt.Sprintf("foreign-event-%d", index), SourceID: strconv.FormatUint(uint64(event.ID), 10), Title: "UNREADABLE", Content: "FOREIGN_KNOWLEDGE"}
		if err := g.db.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, row := range []memorycore.Knowledge{
		{TenantID: "other", AgentName: "first", KnowledgeID: "other-tenant", SourceID: strconv.FormatUint(uint64(valid.ID), 10)},
		{TenantID: "default", AgentName: "second", KnowledgeID: "other-agent", SourceID: strconv.FormatUint(uint64(valid.ID), 10)},
		{TenantID: "default", AgentName: "first", KnowledgeID: "no-source"},
		{TenantID: "default", AgentName: "first", KnowledgeID: "ambiguous-source", SourceID: "01"},
	} {
		row.Title, row.Content = "UNREADABLE", "FOREIGN_KNOWLEDGE"
		if err := g.db.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
	}
	deleted := memorycore.Knowledge{TenantID: "default", AgentName: "first", KnowledgeID: "deleted", SourceID: strconv.FormatUint(uint64(valid.ID), 10), Title: "DELETED_KNOWLEDGE", Content: "DELETED_KNOWLEDGE"}
	if err := g.db.Create(&deleted).Error; err != nil {
		t.Fatal(err)
	}
	if err := g.db.Delete(&deleted).Error; err != nil {
		t.Fatal(err)
	}
	w := memoryScopeRequest(t, g, http.MethodGet, "/api/memory?agent=first", nil)
	if w.Code != http.StatusOK || strings.Contains(w.Body.String(), "FOREIGN_KNOWLEDGE") || strings.Contains(w.Body.String(), "DELETED_KNOWLEDGE") || strings.Contains(w.Body.String(), "learning_knowledge") {
		t.Fatalf("unverifiable legacy knowledge was exposed: %d %s", w.Code, w.Body.String())
	}
}

func TestPersonalMemoryLearningKnowledgeReadsRemainBounded(t *testing.T) {
	g := memorySourceFixture(t)
	event := memorycore.MemoryEvent{TenantID: "default", AgentName: "first", UserID: "fixture-user", SessionID: "source-web", Status: "success"}
	if err := g.db.Create(&event).Error; err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 55; index++ {
		row := memorycore.Knowledge{TenantID: "default", AgentName: "first", KnowledgeID: fmt.Sprintf("bounded-%d", index), SourceID: strconv.FormatUint(uint64(event.ID), 10), Title: "已有经验", Content: strings.Repeat("经验", 2000)}
		if err := g.db.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
	}
	request := httptest.NewRequest(http.MethodGet, "http://localhost:8090/api/memory", nil)
	items, err := g.personalMemorySourceReferences(request, "first", memorycore.MemoryUser, "", "", "")
	if err != nil || len(items) != 50 {
		t.Fatalf("learning sources are unbounded or missing: count=%d err=%v", len(items), err)
	}
	for _, item := range items {
		if item.SourceType != "learning_knowledge" || len([]rune(item.Value)) > 1200 || !item.ReadOnly {
			t.Fatal("bounded source contract lost")
		}
	}
	var entries, count int64
	g.db.Model(&memorycore.Entry{}).Count(&entries)
	g.db.Model(&memorycore.Knowledge{}).Count(&count)
	if entries != 0 || count != 55 {
		t.Fatal("reading learned knowledge changed saved memory")
	}
}
