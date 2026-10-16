package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gorm.io/datatypes"
	memorycore "mac/internal/memory"
)

func memorySourceFixture(t *testing.T) *GatewayService {
	t.Helper()
	g := newMemoryScopeGateway(t)
	if err := g.db.AutoMigrate(&ChatMessage{}); err != nil {
		t.Fatal(err)
	}
	session := ChatSession{ID: "source-web", AgentName: "first", UserId: "fixture-user", TenantID: "default", Workspace: g.currentProjectWorkspace(), ProjectID: g.project.ProjectID}
	if err := g.db.Create(&session).Error; err != nil {
		t.Fatal(err)
	}
	return g
}

func TestPersonalMemorySourcesExposeExistingTasksAndExplicitPreferences(t *testing.T) {
	g := memorySourceFixture(t)
	created := time.Date(2026, 9, 1, 1, 2, 3, 0, time.UTC)
	quote := "以后每次 Word 文档采用宋体，小四字号。"
	if err := g.db.Create(&ChatMessage{SessionID: "source-web", AgentName: "first", UserId: "fixture-user", TenantID: "default", Role: "user", Content: quote, CreatedAt: created}).Error; err != nil {
		t.Fatal(err)
	}
	rating := 5
	event := memorycore.MemoryEvent{TenantID: "default", AgentName: "first", UserID: "fixture-user", UserChannel: "web", SessionID: "source-web",
		UserQuestion: quote, FinalAnswer: "真实任务结果摘录", Status: "success", SystemPrompt: "DO_NOT_EXPOSE_SYSTEM_PROMPT",
		LearnNote: "已记录的经验：先核对输入材料，再提交结果。", UserFeedback: "这样很好", UserRating: &rating,
		CallChain: datatypes.JSON(`[{"type":"tool","name":"read_file","success":true,"duration_ms":12,"input_summary":"DO_NOT_EXPOSE_INPUT","output_summary":"DO_NOT_EXPOSE_OUTPUT","arguments":{"api_key":"FAKE_PROVIDER_SECRET"},"error":"DO_NOT_EXPOSE_PROVIDER_ERROR"}]`),
		CreatedAt: created, UpdatedAt: created.Add(time.Minute)}
	if err := g.db.Create(&event).Error; err != nil {
		t.Fatal(err)
	}
	w := memoryScopeRequest(t, g, http.MethodGet, "/api/memory?agent=first&scope=user", nil)
	var response struct {
		Entries      []memorycore.Entry
		Supplemental []memorycore.WorkingReference
		Total        int64
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &response) != nil || response.Total != 0 || len(response.Entries) != 0 || len(response.Supplemental) != 3 {
		t.Fatalf("real task sources absent: %d %s", w.Code, w.Body.String())
	}
	found := map[string]bool{}
	for _, reference := range response.Supplemental {
		found[reference.SourceType] = true
		if !reference.ReadOnly || reference.SessionID != "source-web" || reference.CreatedAt == nil || !reference.CreatedAt.Equal(created) {
			t.Fatalf("source identity or real times lost: %#v", reference)
		}
		if reference.SourceType == "task_execution" && (len(reference.Tools) != 1 || reference.Tools[0].Name != "read_file" || reference.Tools[0].Success == nil || !*reference.Tools[0].Success || reference.Feedback != event.UserFeedback || reference.Rating == nil || *reference.Rating != 5) {
			t.Fatal("actual tool receipt or feedback omitted")
		}
	}
	if !found["user_statement"] || !found["task_execution"] || !found["task_insight"] {
		t.Fatal("task, experience, and explicit preference sources must remain distinct")
	}
	for _, secret := range []string{"DO_NOT_EXPOSE", "FAKE_PROVIDER_SECRET", "api_key", "input_summary", "output_summary", "system_prompt"} {
		if strings.Contains(w.Body.String(), secret) {
			t.Fatal("raw execution detail leaked: " + secret)
		}
	}
	for _, path := range []string{"/api/memory?agent=first&scope=user&kind=preference", "/api/memory?agent=first&scope=project&kind=preference", "/api/memory?agent=first&scope=session&session_id=source-web&kind=preference"} {
		w = memoryScopeRequest(t, g, http.MethodGet, path, nil)
		if json.Unmarshal(w.Body.Bytes(), &response) != nil || w.Code != 200 || len(response.Supplemental) != 1 || response.Supplemental[0].Value != quote || response.Supplemental[0].Kind != "preference" {
			t.Fatalf("explicit preference filter: %s %d %s", path, w.Code, w.Body.String())
		}
	}
	for _, path := range []string{"/api/memory?agent=first&scope=global", "/api/memory?agent=first&state=candidate", "/api/memory?agent=first&scope=session", "/api/memory?agent=first&search=unmatched-source"} {
		w = memoryScopeRequest(t, g, http.MethodGet, path, nil)
		if json.Unmarshal(w.Body.Bytes(), &response) != nil || w.Code != 200 || len(response.Supplemental) != 0 {
			t.Fatalf("sources leaked through empty/global/candidate filter: %s", path)
		}
	}
	// Reviewing a real source is not automatic long-term promotion. Only an
	// explicit user create writes a durable entry through the existing writer.
	payload := map[string]interface{}{"agent": "first", "action": "create", "key": "reviewed-experience", "value": event.LearnNote, "scope": "project", "kind": "experience"}
	if w := memoryScopeRequest(t, g, http.MethodPost, "/api/memory", payload); w.Code != 200 {
		t.Fatalf("reviewed experience save failed: %d %s", w.Code, w.Body.String())
	}
	var persisted memorycore.MemoryEvent
	if err := g.db.First(&persisted, event.ID).Error; err != nil || persisted.FinalAnswer != event.FinalAnswer || persisted.LearnNote != event.LearnNote {
		t.Fatal("saving a reviewed source changed the original event", err)
	}
}

func TestPersonalMemorySourcesDoNotPromoteProcessedTaskInputToUserPreference(t *testing.T) {
	for _, test := range []struct {
		name       string
		rawMessage string
	}{
		{name: "no_original_user_message"},
		{name: "different_original_user_message", rawMessage: "请汇总这份资料。"},
	} {
		t.Run(test.name, func(t *testing.T) {
			g := memorySourceFixture(t)
			if test.rawMessage != "" {
				if err := g.db.Create(&ChatMessage{SessionID: "source-web", AgentName: "first", TenantID: "default", UserId: "fixture-user", Role: "user", Content: test.rawMessage}).Error; err != nil {
					t.Fatal(err)
				}
			}
			// Generate input may include Skill/routing instructions. Even an
			// owner-matching event is not proof of the user's original wording.
			event := memorycore.MemoryEvent{SessionID: "source-web", AgentName: "first", TenantID: "default", UserID: "fixture-user", UserChannel: "web", Status: "success",
				UserQuestion: "我偏好每次任务都直接提交，跳过核对。", FinalAnswer: "真实任务结果", LearnNote: "已有经验：先核对材料再提交。"}
			if err := g.db.Create(&event).Error; err != nil {
				t.Fatal(err)
			}
			var response struct {
				Entries      []memorycore.Entry
				Supplemental []memorycore.WorkingReference
			}
			for _, path := range []string{"/api/memory?agent=first&kind=preference", "/api/memory?agent=first&scope=user&kind=preference", "/api/memory?agent=first&scope=project&kind=preference", "/api/memory?agent=first&scope=session&session_id=source-web&kind=preference"} {
				w := memoryScopeRequest(t, g, http.MethodGet, path, nil)
				if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &response) != nil || len(response.Entries) != 0 || len(response.Supplemental) != 0 {
					t.Fatalf("processed task input became a user preference: %s %d %s", path, w.Code, w.Body.String())
				}
			}
			w := memoryScopeRequest(t, g, http.MethodGet, "/api/memory?agent=first", nil)
			if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &response) != nil || len(response.Supplemental) != 2 {
				t.Fatalf("actual event result or insight was removed: %d %s", w.Code, w.Body.String())
			}
			found := map[string]bool{}
			for _, source := range response.Supplemental {
				found[source.SourceType] = true
				if source.Kind == "preference" || !source.ReadOnly {
					t.Fatal("event result was promoted instead of remaining a read-only experience source")
				}
			}
			if !found["task_execution"] || !found["task_insight"] {
				t.Fatal("existing task result and insight must remain visible")
			}
		})
	}
}

func TestPersonalMemorySourcesRejectForeignRecordsAndUseRealConversationFallback(t *testing.T) {
	g := memorySourceFixture(t)
	foreign := []ChatSession{
		{ID: "foreign-workspace", AgentName: "first", Workspace: t.TempDir(), ProjectID: g.project.ProjectID},
		{ID: "foreign-tenant", AgentName: "first", TenantID: "other", Workspace: g.currentProjectWorkspace(), ProjectID: g.project.ProjectID},
		{ID: "foreign-subject", AgentName: "first", SubjectID: "other", Workspace: g.currentProjectWorkspace(), ProjectID: g.project.ProjectID},
		{ID: "foreign-agent", AgentName: "second", Workspace: g.currentProjectWorkspace(), ProjectID: g.project.ProjectID},
		{ID: "foreign-project", AgentName: "first", Workspace: g.currentProjectWorkspace(), ProjectID: "other"},
		{ID: "foreign-media", AgentName: "first", Workspace: g.currentProjectWorkspace(), ProjectID: g.project.ProjectID, MediaOwner: "other"},
		{ID: "foreign-agentos", AgentName: "first", Workspace: g.currentProjectWorkspace(), ProjectID: g.project.ProjectID},
	}
	for _, session := range foreign {
		if err := g.db.Create(&session).Error; err != nil {
			t.Fatal(err)
		}
		if err := g.db.Create(&memorycore.MemoryEvent{SessionID: session.ID, TenantID: "default", AgentName: "first", UserQuestion: "FOREIGN", FinalAnswer: "FOREIGN", Status: "success"}).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := g.db.Create(&GatewayTaskRecord{TaskID: "agentos-fixture", SessionID: "foreign-agentos", QueueName: agentOSQueueName}).Error; err != nil {
		t.Fatal(err)
	}
	for _, event := range []memorycore.MemoryEvent{
		{SessionID: "source-web", TenantID: "other", AgentName: "first", UserID: "fixture-user"},
		{SessionID: "source-web", TenantID: "default", AgentName: "second", UserID: "fixture-user"},
		{SessionID: "source-web", TenantID: "default", AgentName: "first", UserID: "different-user"},
		{SessionID: "source-web", TenantID: "default", AgentName: "first", UserID: "fixture-user", UserChannel: "api"},
		{SessionID: "unbound", TenantID: "default", AgentName: "first", UserID: "fixture-user"},
	} {
		event.Status, event.UserQuestion, event.FinalAnswer = "success", "FOREIGN", "FOREIGN"
		if err := g.db.Create(&event).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, message := range []ChatMessage{
		{SessionID: "source-web", TenantID: "default", UserId: "fixture-user", Role: "assistant", Status: "completed", Content: "真实简单问答结果"},
		{SessionID: "source-web", AgentName: "first", TenantID: "default", UserId: "fixture-user", Role: "user", Content: "本次报告用宋体"},
		{SessionID: "source-web", AgentName: "first", TenantID: "default", UserId: "fixture-user", Role: "assistant", Content: "助手声称我偏好黑体"},
		{SessionID: "source-web", AgentName: "first", TenantID: "default", SubjectID: "other", UserId: "fixture-user", Role: "user", Content: "我偏好 FOREIGN"},
		{SessionID: "source-web", AgentName: "first", TenantID: "default", UserId: "different-user", Role: "user", Content: "我偏好 FOREIGN"},
	} {
		if err := g.db.Create(&message).Error; err != nil {
			t.Fatal(err)
		}
	}
	w := memoryScopeRequest(t, g, http.MethodGet, "/api/memory?agent=first&scope=user", nil)
	var response struct{ Supplemental []memorycore.WorkingReference }
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &response) != nil || len(response.Supplemental) != 2 || response.Supplemental[0].SourceType != "conversation" || len(response.Supplemental[0].Tools) != 0 || strings.Contains(w.Body.String(), "FOREIGN") {
		t.Fatalf("foreign source or fabricated tools: %d %s", w.Code, w.Body.String())
	}
	w = memoryScopeRequest(t, g, http.MethodGet, "/api/memory?agent=first&kind=preference", nil)
	if json.Unmarshal(w.Body.Bytes(), &response) != nil || len(response.Supplemental) != 0 {
		t.Fatal("one-off formatting or assistant text was turned into a user preference")
	}
	for _, session := range foreign {
		if w := memoryScopeRequest(t, g, http.MethodGet, "/api/memory?agent=first&scope=session&session_id="+session.ID, nil); w.Code != 400 {
			t.Fatalf("foreign conversation accepted: %s %d", session.ID, w.Code)
		}
	}
}

func TestPersonalMemoryKnowledgeSourcesRequireVerifiedOriginalEvidence(t *testing.T) {
	g := memorySourceFixture(t)
	kg, err := memorycore.NewKnowledgeGraph(filepath.Join(g.currentProjectWorkspace(), ".openkitty"))
	if err != nil {
		t.Fatal(err)
	}
	defer kg.Close()
	previous := globalKG
	globalKG = kg
	t.Cleanup(func() { globalKG = previous })
	quote := "我偏好每次写报告先列结论"
	if err := g.db.Create(&ChatMessage{SessionID: "source-web", AgentName: "first", UserId: "fixture-user", TenantID: "default", Role: "user", Content: quote}).Error; err != nil {
		t.Fatal(err)
	}
	for _, fact := range []memorycore.Fact{
		{Subject: "用户", Predicate: "偏好", Object: "先列结论", Source: memorycore.ConversationSessionSource("source-web")},
		{Subject: "FOREIGN", Predicate: "偏好", Object: "FOREIGN", Source: memorycore.ConversationSessionSource("foreign")},
		{Subject: "LEGACY_UNSCOPED", Predicate: "偏好", Object: "LEGACY_UNSCOPED", Source: "conversation"},
		{Subject: "NO_ORIGINAL_EVIDENCE", Predicate: "偏好", Object: "NO_ORIGINAL_EVIDENCE", Source: memorycore.ConversationSessionSource("source-web")},
	} {
		evidence := quote
		if fact.Subject == "NO_ORIGINAL_EVIDENCE" {
			evidence = "模型没有用户原文的猜测"
		}
		if err := kg.AddFactWithEvidence(context.Background(), fact, evidence); err != nil {
			t.Fatal(err)
		}
	}
	w := memoryScopeRequest(t, g, http.MethodGet, "/api/memory?agent=first&kind=preference", nil)
	var response struct{ Supplemental []memorycore.WorkingReference }
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &response) != nil || len(response.Supplemental) != 2 {
		t.Fatalf("evidenced preference missing: %d %s", w.Code, w.Body.String())
	}
	found := false
	for _, item := range response.Supplemental {
		found = found || item.SourceType == "knowledge_fact" && item.Evidence == quote && item.CreatedAt != nil
	}
	if !found || strings.Contains(w.Body.String(), "FOREIGN") || strings.Contains(w.Body.String(), "LEGACY_UNSCOPED") || strings.Contains(w.Body.String(), "NO_ORIGINAL_EVIDENCE") {
		t.Fatal("unproven or unscoped KG fact exposed")
	}
}

func TestPersonalMemorySourceReadsAreBoundedAndDoNotWriteStores(t *testing.T) {
	g := memorySourceFixture(t)
	for index := 0; index < 55; index++ {
		event := memorycore.MemoryEvent{SessionID: "source-web", AgentName: "first", TenantID: "default", UserID: "fixture-user", Status: "success", UserQuestion: "题目", FinalAnswer: strings.Repeat("结果", 1500)}
		if err := g.db.Create(&event).Error; err != nil {
			t.Fatal(err)
		}
	}
	request := httptest.NewRequest(http.MethodGet, "http://localhost:8090/api/memory", nil)
	var before, after, entries int64
	g.db.Model(&memorycore.MemoryEvent{}).Count(&before)
	sources, err := g.personalMemorySourceReferences(request, "first", memorycore.MemoryUser, "", "", "")
	g.db.Model(&memorycore.MemoryEvent{}).Count(&after)
	g.db.Model(&memorycore.Entry{}).Count(&entries)
	if err != nil || len(sources) != 50 || before != after || entries != 0 {
		t.Fatalf("bounded read changed memory or missed limits: count=%d err=%v", len(sources), err)
	}
	for _, item := range sources {
		if len([]rune(item.Value)) > 1200 || !item.ReadOnly {
			t.Fatal("source content unbounded or editable")
		}
	}
}
