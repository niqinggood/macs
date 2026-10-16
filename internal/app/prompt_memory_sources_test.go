package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"gorm.io/datatypes"
	memorycore "mac/internal/memory"
	"mac/internal/projectctx"
)

func promptSourceReadRequest(t *testing.T) *http.Request {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, "http://localhost:8082/api/memory/prompt-sources?agent=first", nil)
	r.RemoteAddr = "127.0.0.1:40000"
	r.Header.Set("Origin", "http://localhost:8082")
	return r
}

func promptSourceReadRuntime(t *testing.T, g *GatewayService, enabled bool) {
	t.Helper()
	g.engine.agentFactory.agents["first"] = &LLMAgent{name: "first", workspace: g.currentProjectWorkspace(), hasMemory: enabled,
		projectInstructions: projectctx.NewProjectInstructionLoader(t.TempDir())}
}

func TestPersonalPromptSourcesReadOnlyRealPathsAndOriginalFields(t *testing.T) {
	g := memorySourceFixture(t)
	promptSourceReadRuntime(t, g, true)
	workspace := g.currentProjectWorkspace()
	if err := os.WriteFile(filepath.Join(workspace, "AGENTS.md"), []byte("Use fixture project conventions.\n@./fixture-rules.md\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "fixture-rules.md"), []byte("fixture imported instruction\n"), 0600); err != nil {
		t.Fatal(err)
	}
	message := ChatMessage{SessionID: "source-web", AgentName: "first", UserId: "fixture-user", TenantID: "default", Role: "user", Content: "quartz historical question"}
	if err := g.db.Create(&message).Error; err != nil {
		t.Fatal(err)
	}
	event := webMemoryTestEvent(t, g, "source-web", "fixture-user", "quartz task", "quartz historical result")
	if err := g.db.Model(&memorycore.MemoryEvent{}).Where("id = ?", event.ID).Update("insights", datatypes.JSON(`["quartz original lesson"]`)).Error; err != nil {
		t.Fatal(err)
	}
	knowledge := memorycore.Knowledge{TenantID: "default", AgentName: "first", SourceID: strconv.FormatUint(uint64(event.ID), 10), KnowledgeID: "fixture-learning", Title: "quartz knowledge", Summary: "quartz learning summary", UseCount: 7}
	if err := g.db.Create(&knowledge).Error; err != nil {
		t.Fatal(err)
	}
	foreign := ChatMessage{SessionID: "source-web", AgentName: "first", UserId: "foreign-user", TenantID: "default", Role: "user", Content: "FOREIGN_PROMPT_BODY"}
	if err := g.db.Create(&foreign).Error; err != nil {
		t.Fatal(err)
	}
	r := promptSourceReadRequest(t)
	sessions, err := g.personalMemorySessions(r, "first")
	if err != nil {
		t.Fatal(err)
	}
	owner := memorycore.Owner{TenantID: "default", PartitionID: "default", SessionID: "source-web", ImportLegacy: false}
	// Database-level protection proves the gatherer cannot create migrations,
	// imports, use counts, traces, input receipts or background job records.
	if err := g.db.Exec("PRAGMA query_only=ON").Error; err != nil {
		t.Fatal(err)
	}
	sources, providers, err := g.personalPromptSources(r, "first", owner, sessions, owner.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]int{}
	for _, source := range sources {
		found[source.Locator.Kind]++
		if source.CodePath == "" || source.Injection == "" || source.CapturedRevision != source.Locator.Digest || source.Locator.Digest == "" {
			t.Fatalf("source lacks its real read path or raw source revision: %+v", source)
		}
		if source.Kind == "task_insight" && source.Locator.Field != "insights" {
			t.Fatal("fallback lesson did not retain its actual original JSON field")
		}
		if source.Locator.Kind == "chat_message" && (source.Mode != "direct" || source.Injection != "history_messages" || source.Locator.Digest != promptMemoryDigest(message.Content)) {
			t.Fatal("actual conversation history was mistaken for a conditional recall")
		}
		if source.Locator.Kind == "memory_event" && source.Mode != "relevant" {
			t.Fatal("old event was claimed to be unconditionally injected")
		}
	}
	if found["chat_message"] != 1 || found["memory_event"] != 2 || found["knowledge"] != 1 || found["project_instruction"] != 2 || len(providers) != 3 {
		t.Fatalf("wrong actual source inventory: kinds=%v providers=%d", found, len(providers))
	}
	public, _ := json.Marshal(sources)
	for _, forbidden := range []string{"FOREIGN_PROMPT_BODY", workspace, `"locator"`, `"Locator"`} {
		if strings.Contains(string(public), forbidden) {
			t.Fatal("foreign content or internal source addressing leaked")
		}
	}
	var receipts int64
	if err := g.db.Table("memory_prompt_inputs").Count(&receipts).Error; err != nil || receipts != 0 {
		t.Fatal("gathering sources generated a new model input receipt", err)
	}
	if err := g.db.First(&knowledge, knowledge.ID).Error; err != nil || knowledge.UseCount != 7 {
		t.Fatal("gathering sources changed learning usage metadata", err)
	}
	if _, err := os.Stat(filepath.Join(workspace, ".openkitty", "context")); !os.IsNotExist(err) {
		t.Fatal("a read-only source inventory created an SCM directory")
	}
}

func TestPersonalPromptSourcesMemoryOffAndIdentityBoundaries(t *testing.T) {
	g := memorySourceFixture(t)
	promptSourceReadRuntime(t, g, false)
	if err := os.WriteFile(filepath.Join(g.currentProjectWorkspace(), "AGENTS.md"), []byte("fixture instruction with memory disabled"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := g.db.Create(&ChatMessage{SessionID: "source-web", AgentName: "first", UserId: "fixture-user", TenantID: "default", Role: "assistant", Content: "ordinary stored answer"}).Error; err != nil {
		t.Fatal(err)
	}
	webMemoryTestEvent(t, g, "source-web", "fixture-user", "quartz", "memory-disabled stored event")
	r := promptSourceReadRequest(t)
	sessions, _ := g.personalMemorySessions(r, "first")
	owner := memorycore.Owner{TenantID: "default", PartitionID: "default", SessionID: "source-web"}
	sources, _, err := g.personalPromptSources(r, "first", owner, sessions, owner.SessionID)
	if err != nil || len(sources) != 2 {
		t.Fatalf("history/instructions must survive memory disabled, event recalls must not: n=%d err=%v", len(sources), err)
	}
	for _, source := range sources {
		if source.Mode != "direct" || source.Locator.Kind == "memory_event" {
			t.Fatal("memory-disabled old events remained eligible")
		}
	}
	for _, name := range []string{"remote", "cross_origin", "foreign_session", "foreign_workspace", "foreign_agent", "agentos", "foreign_owner"} {
		t.Run(name, func(t *testing.T) {
			request := promptSourceReadRequest(t)
			agent, session, copyOwner := "first", owner.SessionID, owner
			switch name {
			case "remote":
				request.RemoteAddr = "192.0.2.10:40000"
			case "cross_origin":
				request.Header.Set("Origin", "https://example.invalid")
			case "foreign_session":
				session = "not-a-real-session"
			case "foreign_workspace":
				err := g.db.Model(&ChatSession{}).Where("id = ?", owner.SessionID).Update("workspace", t.TempDir()).Error
				if err != nil {
					t.Fatal(err)
				}
				defer g.db.Model(&ChatSession{}).Where("id = ?", owner.SessionID).Update("workspace", g.currentProjectWorkspace())
			case "foreign_agent":
				agent = "not-configured"
			case "agentos":
				request = request.WithContext(withExecutionIdentity(context.Background(), ExecutionIdentity{Source: "agent_os", Service: true}))
			case "foreign_owner":
				copyOwner.SubjectID = "foreign-subject"
			}
			if values, _, err := g.personalPromptSources(request, agent, copyOwner, sessions, session); err == nil || len(values) != 0 {
				t.Fatal("an invalid source authority returned historical prompt data")
			}
		})
	}
}
