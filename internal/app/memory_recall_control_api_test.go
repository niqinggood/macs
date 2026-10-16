package app

import (
	"net/http"
	"strconv"
	"strings"
	"testing"

	memorycore "mac/internal/memory"
)

func findRecallControlSource(t *testing.T, g *GatewayService, label string) (promptSourceResponse, personalPromptHistorySource) {
	t.Helper()
	view := readPromptSourceView(t, g, "/api/memory/prompt-sources?agent=first&mode=management")
	for _, source := range view.Sources {
		if source.SourceLabel == label {
			return view, source
		}
	}
	t.Fatalf("recall source missing: %s", label)
	return view, personalPromptHistorySource{}
}

func TestPromptSourceRecallControlChangesFuturePromptWithoutErasingHistory(t *testing.T) {
	g := memorySourceFixture(t)
	promptSourceReadRuntime(t, g, true)
	event := webMemoryTestEvent(t, g, "source-web", "fixture-user", "quartz task", "quartz verified execution result")
	label := "memory_events#" + strconv.FormatUint(uint64(event.ID), 10) + ".final_answer"
	view, source := findRecallControlSource(t, g, label)
	if source.Recall == nil || !source.Recall.Enabled || source.Recall.Version != 0 {
		t.Fatal("automatic source must expose its default enabled setting")
	}
	ctx, _ := webMemoryTestContext(t, g, "source-web", "fixture-user")
	agent := webMemoryTestAgent(g)
	if prompt := agent.buildMemoryPrompt(ctx, "fixture-user", "source-web", "quartz"); !strings.Contains(prompt, event.FinalAnswer) {
		t.Fatal("eligible source absent before disabling")
	}
	action := map[string]interface{}{"agent": "first", "view_id": view.ViewID, "source_id": source.ID,
		"action": "set_recall_enabled", "expected_revision": view.CurrentSources[source.ID].Revision,
		"expected_recall_version": 0, "recall_enabled": false}
	if w := memoryScopeRequest(t, g, http.MethodPost, "/api/memory/prompt-sources", action); w.Code != 200 {
		t.Fatalf("disable failed: %d %s", w.Code, w.Body.String())
	}
	if prompt := agent.buildMemoryPrompt(ctx, "fixture-user", "source-web", "quartz"); strings.Contains(prompt, event.FinalAnswer) {
		t.Fatal("disabled source still reaches the actual memory prefix")
	}
	if w := memoryScopeRequest(t, g, http.MethodPost, "/api/memory/prompt-sources", action); w.Code != 409 {
		t.Fatal("stale setting version accepted", w.Code)
	}
	view, source = findRecallControlSource(t, g, label)
	if source.Recall == nil || source.Recall.Enabled || source.Recall.Version != 1 || view.CurrentSources[source.ID].Content != event.FinalAnswer {
		t.Fatal("disabled source must remain readable and re-enableable")
	}
	action["view_id"], action["source_id"] = view.ViewID, source.ID
	action["expected_revision"] = view.CurrentSources[source.ID].Revision
	action["expected_recall_version"], action["recall_enabled"] = 1, true
	if w := memoryScopeRequest(t, g, http.MethodPost, "/api/memory/prompt-sources", action); w.Code != 200 {
		t.Fatalf("re-enable failed: %d %s", w.Code, w.Body.String())
	}
	if prompt := agent.buildMemoryPrompt(ctx, "fixture-user", "source-web", "quartz"); !strings.Contains(prompt, event.FinalAnswer) {
		t.Fatal("re-enabled source did not return to automatic recall")
	}
	var original memorycore.MemoryEvent
	if err := g.db.First(&original, event.ID).Error; err != nil || original.FinalAnswer != event.FinalAnswer || !original.UpdatedAt.Equal(event.UpdatedAt) || string(original.CallChain) != string(event.CallChain) {
		t.Fatal("recall controls changed the original history", err)
	}
}

func TestPromptSourceRecallControlRevalidatesIdentityAndBothVersions(t *testing.T) {
	g := memorySourceFixture(t)
	promptSourceReadRuntime(t, g, true)
	event := webMemoryTestEvent(t, g, "source-web", "fixture-user", "quartz task", "quartz result")
	label := "memory_events#" + strconv.FormatUint(uint64(event.ID), 10) + ".final_answer"
	view, source := findRecallControlSource(t, g, label)
	action := map[string]interface{}{"agent": "first", "view_id": view.ViewID, "source_id": source.ID, "action": "set_recall_enabled",
		"expected_revision": view.CurrentSources[source.ID].Revision, "expected_recall_version": 0, "recall_enabled": false}
	for _, test := range []struct {
		field  string
		value  interface{}
		status int
	}{{"agent", "second", 404}, {"view_id", "forged-view", 404}, {"source_id", "forged-source", 404},
		{"expected_revision", "stale", 409}, {"expected_recall_version", -1, 400}, {"expected_recall_version", nil, 400}, {"recall_enabled", nil, 400}, {"value", "unexpected edit", 400}, {"locator", map[string]string{"id": "123"}, 400}} {
		t.Run(test.field, func(t *testing.T) {
			changed := make(map[string]interface{}, len(action)+1)
			for key, value := range action {
				changed[key] = value
			}
			changed[test.field] = test.value
			if w := memoryScopeRequest(t, g, http.MethodPost, "/api/memory/prompt-sources", changed); w.Code != test.status {
				t.Fatalf("unexpected validation status %d: %s", w.Code, w.Body.String())
			}
		})
	}
	if err := g.db.Model(&memorycore.MemoryEvent{}).Where("id = ?", event.ID).Update("user_id", "other-user").Error; err != nil {
		t.Fatal(err)
	}
	if w := memoryScopeRequest(t, g, http.MethodPost, "/api/memory/prompt-sources", action); w.Code != 404 {
		t.Fatal("view retained permission after source ownership changed", w.Code)
	}
	var count int64
	if err := g.db.Table("memory_source_recall_settings").Count(&count).Error; err != nil || count != 0 {
		t.Fatal("invalid actions persisted settings", err)
	}
}

func TestPromptSourceRecallControlDoesNotPretendToDisableConversationHistory(t *testing.T) {
	g := memorySourceFixture(t)
	message := ChatMessage{TenantID: "default", UserId: "fixture-user", AgentName: "first", SessionID: "source-web", Role: "user", Content: "quartz ordinary message"}
	if err := g.db.Create(&message).Error; err != nil {
		t.Fatal(err)
	}
	view := readPromptSourceView(t, g, "/api/memory/prompt-sources?agent=first&session_id=source-web")
	for _, source := range view.Sources {
		if source.Injection != "history_messages" {
			continue
		}
		if source.Recall != nil {
			t.Fatal("direct conversation history advertised an automatic recall switch")
		}
		action := map[string]interface{}{"agent": "first", "view_id": view.ViewID, "source_id": source.ID, "action": "set_recall_enabled",
			"expected_revision": view.CurrentSources[source.ID].Revision, "expected_recall_version": 0, "recall_enabled": false}
		if w := memoryScopeRequest(t, g, http.MethodPost, "/api/memory/prompt-sources", action); w.Code != 404 {
			t.Fatal("raw conversation handle obtained a recall switch", w.Code)
		}
		return
	}
	t.Fatal("raw conversation source missing")
}
