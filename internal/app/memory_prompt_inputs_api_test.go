package app

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	memorycore "mac/internal/memory"
	"mac/internal/projectctx"
)

func savePromptSourceFixture(t *testing.T, g *GatewayService, locator memorycore.PromptLocator, excerpt string) *memorycore.PromptInput {
	t.Helper()
	service := memorycore.NewService(g.db, g.currentProjectWorkspace(), "first")
	input, err := service.SavePromptInput(context.Background(), memorycore.Owner{TenantID: "default", PartitionID: "default", SessionID: "source-web"},
		memorycore.PromptInput{SessionID: "source-web", Agent: "first", TaskID: "real-task", CallIndex: 1, Status: "attempted",
			Sources: []memorycore.PromptSource{{Kind: locator.Kind, Title: "An actual input source", SourceLabel: "original-record", Excerpt: excerpt,
				CapturedRevision: promptSourceDigest(excerpt), Locator: locator}}})
	if err != nil {
		t.Fatal(err)
	}
	return input
}

func sourceActionFor(input *memorycore.PromptInput, revision, action, value string) map[string]interface{} {
	return map[string]interface{}{"agent": "first", "input_id": input.ID, "source_id": input.Sources[0].ID, "action": action,
		"expected_revision": revision, "value": value}
}

func promptSourceCurrentRevision(t *testing.T, g *GatewayService, input *memorycore.PromptInput) string {
	t.Helper()
	w := memoryScopeRequest(t, g, http.MethodGet, "/api/memory/inputs?agent=first&input_id="+input.ID, nil)
	var response struct {
		Current map[string]memorycore.CurrentSource `json:"current_sources"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &response) != nil {
		t.Fatalf("source current revision missing: %d %s", w.Code, w.Body.String())
	}
	return response.Current[input.Sources[0].ID].Revision
}

func TestPersonalPromptInputsModifyOriginalHistoryAndKeepSentReceipt(t *testing.T) {
	g := memorySourceFixture(t)
	message := ChatMessage{TenantID: "default", UserId: "fixture-user", AgentName: "first", SessionID: "source-web", Role: "user", Content: "I prefer concise answers."}
	if err := g.db.Create(&message).Error; err != nil {
		t.Fatal(err)
	}
	input := savePromptSourceFixture(t, g, memorycore.PromptLocator{Kind: "chat_message", ID: strconv.FormatUint(uint64(message.ID), 10), SessionID: message.SessionID, Field: "content"}, message.Content)
	w := memoryScopeRequest(t, g, http.MethodGet, "/api/memory/inputs?agent=first", nil)
	var response struct {
		Input   *memorycore.PromptInput
		Current map[string]memorycore.CurrentSource `json:"current_sources"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &response) != nil || response.Input == nil || response.Current[input.Sources[0].ID].Content != message.Content {
		t.Fatalf("actual receipt/source missing: %d %s", w.Code, w.Body.String())
	}
	for _, private := range []string{"Locator", "SessionID", "Namespace", "\"locators\"", "\"path\""} {
		if strings.Contains(w.Body.String(), private) {
			t.Fatal("private server address leaked: " + private)
		}
	}
	revision := response.Current[input.Sources[0].ID].Revision
	if w := memoryScopeRequest(t, g, http.MethodPost, "/api/memory/inputs", sourceActionFor(input, revision, "update", "I prefer detailed answers.")); w.Code != 200 {
		t.Fatalf("original source update failed: %d %s", w.Code, w.Body.String())
	}
	var original ChatMessage
	if err := g.db.First(&original, message.ID).Error; err != nil || original.Content != "I prefer detailed answers." {
		t.Fatal("the original row was not changed", err)
	}
	if history := buildGatewaySessionHistory([]ChatMessage{original}); len(history.Messages) != 1 || history.Messages[0]["content"] != original.Content {
		t.Fatal("the next history loader still uses old content")
	}
	if w := memoryScopeRequest(t, g, http.MethodPost, "/api/memory/inputs", sourceActionFor(input, revision, "delete", "")); w.Code != 409 {
		t.Fatal("stale original-source deletion was accepted", w.Code)
	}
	service := memorycore.NewService(g.db, g.currentProjectWorkspace(), "first")
	stored, err := service.GetPromptInput(context.Background(), memorycore.Owner{TenantID: "default", PartitionID: "default"}, input.ID)
	if err != nil || stored.Sources[0].Excerpt != message.Content {
		t.Fatal("editing the source rewrote the historical request", err)
	}
	if w := memoryScopeRequest(t, g, http.MethodPost, "/api/memory/inputs", sourceActionFor(input, promptSourceDigest(original.Content), "delete", "")); w.Code != 200 {
		t.Fatalf("original deletion failed: %d %s", w.Code, w.Body.String())
	}
	var count int64
	g.db.Model(&ChatMessage{}).Where("id = ?", message.ID).Count(&count)
	if count != 0 {
		t.Fatal("deleted history still exists")
	}
	g.db.Model(&memorycore.Entry{}).Count(&count)
	if count != 0 {
		t.Fatal("source edit secretly wrote an unrelated long-term entry")
	}
}

func TestPersonalPromptInputsFilterAuthorizedConversation(t *testing.T) {
	g := memorySourceFixture(t)
	input := savePromptSourceFixture(t, g, memorycore.PromptLocator{Kind: "summary", SessionID: "source-web"}, "saved summary")
	otherSession := ChatSession{ID: "another-web", AgentName: "first", UserId: "fixture-user", TenantID: "default", Workspace: g.currentProjectWorkspace(), ProjectID: g.project.ProjectID}
	if err := g.db.Create(&otherSession).Error; err != nil {
		t.Fatal(err)
	}
	service := memorycore.NewService(g.db, g.currentProjectWorkspace(), "first")
	if _, err := service.SavePromptInput(context.Background(), memorycore.Owner{TenantID: "default", PartitionID: "default", SessionID: otherSession.ID},
		memorycore.PromptInput{SessionID: otherSession.ID, Agent: "first", TaskID: "another-task", CallIndex: 1, Status: "succeeded"}); err != nil {
		t.Fatal(err)
	}
	w := memoryScopeRequest(t, g, http.MethodGet, "/api/memory/inputs?agent=first&session_id=source-web", nil)
	var response struct {
		Inputs []memorycore.PromptInput
		Input  *memorycore.PromptInput
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &response) != nil || len(response.Inputs) != 1 || response.Input == nil || response.Input.ID != input.ID {
		t.Fatalf("authorized conversation filter did not select its own request: %d %s", w.Code, w.Body.String())
	}
	if w := memoryScopeRequest(t, g, http.MethodGet, "/api/memory/inputs?agent=first&session_id="+otherSession.ID+"&input_id="+input.ID, nil); w.Code != 404 {
		t.Fatal("receipt from another conversation accepted by filter", w.Code)
	}
}

func TestPersonalPromptInputsEditLargeUnicodeSummary(t *testing.T) {
	g := memorySourceFixture(t)
	service := memorycore.NewService(g.db, g.currentProjectWorkspace(), "first")
	owner := memorycore.Owner{TenantID: "default", PartitionID: "default", SessionID: "source-web"}
	original := strings.Repeat("摘要", 24000)
	if err := service.SaveSummary(context.Background(), owner, original); err != nil {
		t.Fatal(err)
	}
	input := savePromptSourceFixture(t, g, memorycore.PromptLocator{Kind: "summary", SessionID: owner.SessionID}, "摘要摘录")
	corrected := "已核实" + strings.TrimPrefix(original, "摘要摘要")
	w := memoryScopeRequest(t, g, http.MethodPost, "/api/memory/inputs", sourceActionFor(input, promptSourceCurrentRevision(t, g, input), "update", corrected))
	if w.Code != 200 {
		t.Fatalf("editable full Unicode summary was rejected: %d %s", w.Code, w.Body.String())
	}
	loaded, err := service.LoadSummary(context.Background(), owner)
	if err != nil || loaded != corrected {
		t.Fatal("large summary edit did not change the actual next-loop source", err)
	}
}

func TestPersonalPromptInputsRejectForeignSourcesAndClientLocators(t *testing.T) {
	g := memorySourceFixture(t)
	message := ChatMessage{TenantID: "default", UserId: "someone-else", AgentName: "first", SessionID: "source-web", Role: "user", Content: "FOREIGN_PRIVATE_TEXT"}
	if err := g.db.Create(&message).Error; err != nil {
		t.Fatal(err)
	}
	input := savePromptSourceFixture(t, g, memorycore.PromptLocator{Kind: "chat_message", ID: strconv.FormatUint(uint64(message.ID), 10), SessionID: message.SessionID, Field: "content"}, "previous authorized excerpt")
	w := memoryScopeRequest(t, g, http.MethodGet, "/api/memory/inputs?agent=first&input_id="+input.ID, nil)
	if w.Code != 200 || strings.Contains(w.Body.String(), message.Content) {
		t.Fatal("foreign current source leaked", w.Code)
	}
	if w := memoryScopeRequest(t, g, http.MethodPost, "/api/memory/inputs", sourceActionFor(input, promptSourceDigest(message.Content), "delete", "")); w.Code != 409 && w.Code != 404 {
		t.Fatalf("foreign source edited: %d %s", w.Code, w.Body.String())
	}
	if w := memoryScopeRequest(t, g, http.MethodGet, "/api/memory/inputs?agent=second&input_id="+input.ID, nil); w.Code != 404 {
		t.Fatal("foreign agent read another namespace's request", w.Code)
	}
	action := sourceActionFor(input, "fake", "update", "new")
	action["locator"] = map[string]string{"table": "memory_events", "path": "../private"}
	if w := memoryScopeRequest(t, g, http.MethodPost, "/api/memory/inputs", action); w.Code != 400 {
		t.Fatal("client supplied its own source address", w.Code)
	}
	foreignSession := ChatSession{ID: "other-workspace", AgentName: "first", TenantID: "default", ProjectID: g.project.ProjectID, Workspace: t.TempDir()}
	if err := g.db.Create(&foreignSession).Error; err != nil {
		t.Fatal(err)
	}
	if w := memoryScopeRequest(t, g, http.MethodGet, "/api/memory/inputs?agent=first&session_id="+foreignSession.ID, nil); w.Code != 400 {
		t.Fatal("foreign workspace accepted", w.Code)
	}
}

func TestPersonalPromptInputsChangeActualProjectInstructions(t *testing.T) {
	g := memorySourceFixture(t)
	path := filepath.Join(g.currentProjectWorkspace(), "AGENTS.md")
	old := "# Project\nAlways check the source.\n"
	if err := os.WriteFile(path, []byte(old), 0600); err != nil {
		t.Fatal(err)
	}
	loader := projectctx.NewProjectInstructionLoader(t.TempDir())
	set, err := loader.Load(g.currentProjectWorkspace(), g.currentProjectWorkspace())
	if err != nil {
		t.Fatal(err)
	}
	before := set.Prompt()
	if !strings.Contains(before, "Always check the source.") {
		t.Fatal("fixture instructions not loaded")
	}
	input := savePromptSourceFixture(t, g, memorycore.PromptLocator{Kind: "project_instruction", Path: path, Field: "content"}, old)
	updated := "# Project\nPrefer verified facts.\n"
	if w := memoryScopeRequest(t, g, http.MethodPost, "/api/memory/inputs", sourceActionFor(input, promptSourceDigest(old), "update", updated)); w.Code != 200 {
		t.Fatalf("project source update failed: %d %s", w.Code, w.Body.String())
	}
	set, err = loader.Load(g.currentProjectWorkspace(), g.currentProjectWorkspace())
	if err != nil {
		t.Fatal(err)
	}
	if after := set.Prompt(); !strings.Contains(after, "Prefer verified facts.") || strings.Contains(after, "Always check the source.") {
		t.Fatal("the next dynamic prompt used cached old instructions")
	}
	if w := memoryScopeRequest(t, g, http.MethodPost, "/api/memory/inputs", sourceActionFor(input, promptSourceDigest(old), "delete", "")); w.Code != 409 {
		t.Fatal("stale file revision accepted", w.Code)
	}
	outside := filepath.Join(t.TempDir(), "AGENTS.md")
	if err := os.WriteFile(outside, []byte("Global source"), 0600); err != nil {
		t.Fatal(err)
	}
	outsideReceipt := savePromptSourceFixture(t, g, memorycore.PromptLocator{Kind: "project_instruction", Path: outside, Field: "content"}, "Global source")
	if w := memoryScopeRequest(t, g, http.MethodPost, "/api/memory/inputs", sourceActionFor(outsideReceipt, promptSourceDigest("Global source"), "delete", "")); w.Code != 409 && w.Code != 400 {
		t.Fatal("external global instruction was mutable", w.Code)
	}
	if data, err := os.ReadFile(outside); err != nil || string(data) != "Global source" {
		t.Fatal("external source changed")
	}
}

func TestPersonalPromptInputsDeleteExperienceWithoutFallbackResurrection(t *testing.T) {
	g := memorySourceFixture(t)
	event := memorycore.MemoryEvent{TenantID: "default", AgentName: "first", UserID: "fixture-user", UserChannel: "web", SessionID: "source-web", Status: "success",
		UserQuestion: "Original request", FinalAnswer: "Actual answer", LearnNote: "Original lesson", Insights: []byte(`["Fallback old lesson"]`)}
	if err := g.db.Create(&event).Error; err != nil {
		t.Fatal(err)
	}
	input := savePromptSourceFixture(t, g, memorycore.PromptLocator{Kind: "memory_event", ID: strconv.FormatUint(uint64(event.ID), 10), SessionID: event.SessionID, Field: "learn_note"}, event.LearnNote)
	revision := promptSourceCurrentRevision(t, g, input)
	if err := g.db.Model(&event).Update("insights", `["New concurrent lesson"]`).Error; err != nil {
		t.Fatal(err)
	}
	if w := memoryScopeRequest(t, g, http.MethodPost, "/api/memory/inputs", sourceActionFor(input, revision, "delete", "")); w.Code != 409 {
		t.Fatal("concurrently changed fallback was erased", w.Code)
	}
	if w := memoryScopeRequest(t, g, http.MethodPost, "/api/memory/inputs", sourceActionFor(input, promptSourceCurrentRevision(t, g, input), "delete", "")); w.Code != 200 {
		t.Fatalf("experience delete failed: %d %s", w.Code, w.Body.String())
	}
	w := memoryScopeRequest(t, g, http.MethodGet, "/api/memory?agent=first&view=long", nil)
	if w.Code != 200 || strings.Contains(w.Body.String(), "Fallback old lesson") || strings.Contains(w.Body.String(), "Original lesson") {
		t.Fatal("old fallback revived deleted experience")
	}
	answer := savePromptSourceFixture(t, g, memorycore.PromptLocator{Kind: "memory_event", ID: strconv.FormatUint(uint64(event.ID), 10), SessionID: event.SessionID, Field: "final_answer"}, event.FinalAnswer)
	if w := memoryScopeRequest(t, g, http.MethodPost, "/api/memory/inputs", sourceActionFor(answer, promptSourceDigest(event.FinalAnswer), "delete", "")); w.Code != 200 {
		t.Fatalf("answer source delete failed: %d %s", w.Code, w.Body.String())
	}
	w = memoryScopeRequest(t, g, http.MethodGet, "/api/memory?agent=first&scope=session&session_id=source-web", nil)
	if w.Code != 200 || strings.Contains(w.Body.String(), `"source_type":"task_execution"`) {
		t.Fatal("request text impersonated the deleted task answer")
	}
}

func TestPersonalPromptInputsHideCapturedForeignSessionExcerpt(t *testing.T) {
	g := memorySourceFixture(t)
	session := ChatSession{ID: "foreign-source", AgentName: "first", TenantID: "default", ProjectID: g.project.ProjectID, Workspace: t.TempDir()}
	if err := g.db.Create(&session).Error; err != nil {
		t.Fatal(err)
	}
	input := savePromptSourceFixture(t, g, memorycore.PromptLocator{Kind: "chat_message", ID: "7", Field: "content", SessionID: session.ID}, "FOREIGN_CAPTURED_EXCERPT")
	w := memoryScopeRequest(t, g, http.MethodGet, "/api/memory/inputs?agent=first&input_id="+input.ID, nil)
	if w.Code != 200 || strings.Contains(w.Body.String(), "FOREIGN_CAPTURED_EXCERPT") {
		t.Fatal("captured foreign-session excerpt bypassed current audience check")
	}
}

func TestPersonalPromptInputsDeleteExperienceWithNullLegacyInsights(t *testing.T) {
	g := memorySourceFixture(t)
	event := memorycore.MemoryEvent{TenantID: "default", AgentName: "first", UserID: "fixture-user", UserChannel: "web", SessionID: "source-web", Status: "success", LearnNote: "A legacy lesson"}
	if err := g.db.Create(&event).Error; err != nil {
		t.Fatal(err)
	}
	input := savePromptSourceFixture(t, g, memorycore.PromptLocator{Kind: "memory_event", ID: strconv.FormatUint(uint64(event.ID), 10), SessionID: event.SessionID, Field: "learn_note"}, event.LearnNote)
	if w := memoryScopeRequest(t, g, http.MethodPost, "/api/memory/inputs", sourceActionFor(input, promptSourceCurrentRevision(t, g, input), "delete", "")); w.Code != 200 {
		t.Fatalf("NULL fallback prevented original experience deletion: %d %s", w.Code, w.Body.String())
	}
}

func TestPersonalPromptInputsModifyAndRemoveOriginalLearningSource(t *testing.T) {
	g := memorySourceFixture(t)
	event := memorycore.MemoryEvent{TenantID: "default", AgentName: "first", UserID: "fixture-user", UserChannel: "web", SessionID: "source-web", Status: "success"}
	if err := g.db.Create(&event).Error; err != nil {
		t.Fatal(err)
	}
	knowledge := memorycore.Knowledge{TenantID: "default", AgentName: "first", KnowledgeID: "actual-knowledge-fixture", Title: "A learned source", Summary: "Old learned summary", Content: "Original body",
		SourceID: strconv.FormatUint(uint64(event.ID), 10)}
	if err := g.db.Create(&knowledge).Error; err != nil {
		t.Fatal(err)
	}
	input := savePromptSourceFixture(t, g, memorycore.PromptLocator{Kind: "knowledge", ID: strconv.FormatUint(uint64(knowledge.ID), 10), SessionID: event.SessionID, Field: "summary"}, knowledge.Summary)
	if w := memoryScopeRequest(t, g, http.MethodPost, "/api/memory/inputs", sourceActionFor(input, promptSourceCurrentRevision(t, g, input), "update", "New learned summary")); w.Code != 200 {
		t.Fatalf("original learning update failed: %d %s", w.Code, w.Body.String())
	}
	var row memorycore.Knowledge
	if err := g.db.First(&row, knowledge.ID).Error; err != nil || row.Summary != "New learned summary" || row.Content != knowledge.Content {
		t.Fatal("learning source update changed the wrong record or field", err)
	}
	if w := memoryScopeRequest(t, g, http.MethodPost, "/api/memory/inputs", sourceActionFor(input, promptSourceCurrentRevision(t, g, input), "delete", "")); w.Code != 200 {
		t.Fatalf("original learning delete failed: %d %s", w.Code, w.Body.String())
	}
	var count int64
	g.db.Model(&memorycore.Knowledge{}).Where("id = ?", knowledge.ID).Count(&count)
	if count != 0 {
		t.Fatal("deleted learning source remains in future reads")
	}
}

func TestPersonalPromptInputsBlockInstructionEscape(t *testing.T) {
	root, external := t.TempDir(), t.TempDir()
	file := filepath.Join(external, "private.md")
	if err := os.WriteFile(file, []byte("PRIVATE_OUTSIDE_TEXT"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "AGENTS.md")
	if err := os.Symlink(file, link); err == nil {
		if _, err := projectctx.ReadInstructionSource(root, link); err == nil {
			t.Fatal("replacement symlink source accepted")
		}
	}
	reserved := filepath.Join(root, ".openkitty", "tenants", "test", "AGENTS.md")
	if err := os.MkdirAll(filepath.Dir(reserved), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(reserved, []byte("TENANT_INSTRUCTIONS"), 0600); err != nil {
		t.Fatal(err)
	}
	if source, err := projectctx.ReadInstructionSource(root, reserved); err != nil || source.Editable {
		t.Fatal("reserved tenant instruction source became mutable", err)
	}
}
