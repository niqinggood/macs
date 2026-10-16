package app

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	memorycore "mac/internal/memory"
	knowledgetools "mac/internal/tools/knowledge"
)

type promptSourceResponse struct {
	ViewID         string                              `json:"view_id"`
	SessionID      string                              `json:"session_id"`
	Sources        []personalPromptHistorySource       `json:"sources"`
	CurrentSources map[string]memorycore.CurrentSource `json:"current_sources"`
}

func readPromptSourceView(t *testing.T, g *GatewayService, path string) promptSourceResponse {
	t.Helper()
	w := memoryScopeRequest(t, g, http.MethodGet, path, nil)
	var response promptSourceResponse
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &response) != nil {
		t.Fatalf("prompt sources unavailable: status=%d %s", w.Code, w.Body.String())
	}
	return response
}

func TestPersonalPromptSourcesShowHistoricalInputsWithoutModelCall(t *testing.T) {
	g := memorySourceFixture(t)
	message := ChatMessage{TenantID: "default", UserId: "fixture-user", AgentName: "first", SessionID: "source-web", Role: "user", Content: "An old request carried into the next prompt."}
	if err := g.db.Create(&message).Error; err != nil {
		t.Fatal(err)
	}
	response := readPromptSourceView(t, g, "/api/memory/prompt-sources?agent=first")
	label := "chat_messages#" + strconv.FormatUint(uint64(message.ID), 10) + ".content"
	var source *personalPromptHistorySource
	for i := range response.Sources {
		if response.Sources[i].SourceLabel == label {
			source = &response.Sources[i]
		}
	}
	if source == nil || source.Mode != "direct" || source.Injection != "history_messages" || response.SessionID != message.SessionID ||
		response.CurrentSources[source.ID].Content != message.Content || response.ViewID == "" {
		t.Fatal("an existing historical input was hidden behind an empty call log")
	}
	var count int64
	if err := g.db.Table("memory_prompt_inputs").Count(&count).Error; err != nil || count != 0 {
		t.Fatal("source inventory created a fake model call", err)
	}
	encoded, _ := json.Marshal(response)
	for _, private := range []string{"\"Locator\"", "\"Namespace\"", "\"Path\""} {
		if strings.Contains(string(encoded), private) {
			t.Fatal("source locator leaked", private)
		}
	}
	action := map[string]interface{}{"agent": "first", "view_id": response.ViewID, "source_id": source.ID,
		"action": "update", "expected_revision": response.CurrentSources[source.ID].Revision, "value": "Corrected original history."}
	w := memoryScopeRequest(t, g, http.MethodPost, "/api/memory/prompt-sources", action)
	if w.Code != 200 {
		t.Fatalf("original historical input edit failed: %d %s", w.Code, w.Body.String())
	}
	var original ChatMessage
	if err := g.db.First(&original, message.ID).Error; err != nil || original.Content != action["value"] {
		t.Fatal("edit did not change the original next-loop input", err)
	}
	action["action"] = "delete"
	if w := memoryScopeRequest(t, g, http.MethodPost, "/api/memory/prompt-sources", action); w.Code != 409 {
		t.Fatal("stale source deletion accepted", w.Code)
	}
	action["expected_revision"] = promptSourceDigest(original.Content)
	if w := memoryScopeRequest(t, g, http.MethodPost, "/api/memory/prompt-sources", action); w.Code != 200 {
		t.Fatal("fresh original source deletion failed", w.Code)
	}
	g.db.Model(&ChatMessage{}).Where("id = ?", message.ID).Count(&count)
	if count != 0 {
		t.Fatal("deleted historical prompt input remains")
	}
}

func TestPersonalPromptSourcesRejectForeignExpiredAndClientLocators(t *testing.T) {
	g := memorySourceFixture(t)
	message := ChatMessage{TenantID: "default", UserId: "fixture-user", AgentName: "first", SessionID: "source-web", Role: "user", Content: "owned history"}
	if err := g.db.Create(&message).Error; err != nil {
		t.Fatal(err)
	}
	response := readPromptSourceView(t, g, "/api/memory/prompt-sources?agent=first&session_id=source-web")
	var source personalPromptHistorySource
	for _, item := range response.Sources {
		if item.Injection == "history_messages" {
			source = item
			break
		}
	}
	if source.ID == "" {
		t.Fatal("missing history source")
	}
	action := map[string]interface{}{"agent": "second", "view_id": response.ViewID, "source_id": source.ID,
		"action": "delete", "expected_revision": promptSourceDigest(message.Content)}
	if w := memoryScopeRequest(t, g, http.MethodPost, "/api/memory/prompt-sources", action); w.Code != 404 {
		t.Fatal("another agent accepted the private view", w.Code)
	}
	action["agent"] = "first"
	action["locator"] = map[string]string{"path": "../private"}
	if w := memoryScopeRequest(t, g, http.MethodPost, "/api/memory/prompt-sources", action); w.Code != 400 {
		t.Fatal("client supplied arbitrary source locator", w.Code)
	}
	delete(action, "locator")
	g.promptSourceViewsMu.Lock()
	delete(g.promptSourceViews, response.ViewID)
	g.promptSourceViewsMu.Unlock()
	if w := memoryScopeRequest(t, g, http.MethodPost, "/api/memory/prompt-sources", action); w.Code != 404 {
		t.Fatal("expired source view accepted", w.Code)
	}
}

func TestPersonalPromptSourcesLearningNotesAreOnDemandAndEditRealFile(t *testing.T) {
	g := memorySourceFixture(t)
	directory := knowledgetools.LearningMemoryDirectory(g.currentProjectWorkspace())
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "lesson.md")
	if err := os.WriteFile(path, []byte("---\nname: A learned workflow\nmetadata:\n  source: learn_tool\n---\n\nOriginal lesson body."), 0600); err != nil {
		t.Fatal(err)
	}
	response := readPromptSourceView(t, g, "/api/memory/prompt-sources?agent=first&include_learning=1")
	var source personalPromptHistorySource
	for _, item := range response.Sources {
		if item.Kind == "tool_learning" {
			source = item
			break
		}
	}
	if source.ID == "" || source.Mode != "on_demand" || source.Injection != "tool_result" || response.CurrentSources[source.ID].Content != "Original lesson body." {
		t.Fatal("saved learning note was hidden or claimed to be automatically injected")
	}
	action := map[string]interface{}{"agent": "first", "view_id": response.ViewID, "source_id": source.ID, "action": "update", "expected_revision": response.CurrentSources[source.ID].Revision, "value": "Corrected saved lesson."}
	if w := memoryScopeRequest(t, g, http.MethodPost, "/api/memory/prompt-sources", action); w.Code != 200 {
		t.Fatalf("original learning note edit failed: %d %s", w.Code, w.Body.String())
	}
	data, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(data), "Corrected saved lesson.") || !strings.Contains(string(data), "source: learn_tool") {
		t.Fatal("original note or its metadata was lost", err)
	}
	var receipts int64
	g.db.Table("memory_prompt_inputs").Count(&receipts)
	if receipts != 0 {
		t.Fatal("learning viewer created a fake model request")
	}
}
