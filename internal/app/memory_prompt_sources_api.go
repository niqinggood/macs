package app

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"time"

	"gorm.io/gorm"
	memorycore "mac/internal/memory"
	"mac/internal/projectctx"
	knowledgetools "mac/internal/tools/knowledge"
)

// These addresses live only in this Gateway's short-lived cache. A source
// inventory is not a model-call receipt and does not write memory_prompt_inputs.
type personalPromptSourceView struct {
	Agent, Workspace, ProjectID, MediaOwner, SessionID string
	ExpiresAt                                          time.Time
	Sources                                            map[string]memorycore.PromptLocator
	RecallSources                                      map[string]bool
}

type personalPromptSourceMutation struct {
	Agent                 string `json:"agent"`
	ViewID                string `json:"view_id"`
	SourceID              string `json:"source_id"`
	Action                string `json:"action"`
	ExpectedRevision      string `json:"expected_revision"`
	Value                 string `json:"value"`
	RecallEnabled         *bool  `json:"recall_enabled,omitempty"`
	ExpectedRecallVersion *int   `json:"expected_recall_version,omitempty"`
}

func (g *GatewayService) personalPromptMemoryEnabled(agent string) bool {
	if runtime := g.personalMemoryCatalogRuntime(agent); runtime != nil {
		return runtime.hasMemory
	}
	if g.config != nil {
		for _, configured := range g.config.Agents {
			if configured.Name == agent {
				return g.config.MemoryConfig.Enabled && (configured.HasMemory == nil || *configured.HasMemory)
			}
		}
	}
	return false
}

func (g *GatewayService) promptInventoryService(agent string) *memorycore.Service {
	if runtime := g.personalMemoryCatalogRuntime(agent); runtime != nil && runtime.memoryService != nil {
		return runtime.memoryService
	}
	// No manager creation, legacy import, model/embedding call or schema change.
	return memorycore.NewService(g.db, g.currentProjectWorkspace(), agent, memorycore.WithRecallEmbedding(nil))
}

func (g *GatewayService) rememberPromptSourceView(r *http.Request, agent, sessionID string, sources []personalPromptHistorySource) string {
	view := personalPromptSourceView{Agent: agent, Workspace: g.currentProjectWorkspace(), ProjectID: g.project.ProjectID,
		MediaOwner: g.personalMemoryMediaOwner(r), SessionID: sessionID, ExpiresAt: time.Now().Add(15 * time.Minute), Sources: make(map[string]memorycore.PromptLocator), RecallSources: make(map[string]bool)}
	for _, source := range sources {
		view.Sources[source.ID] = source.Locator
		view.RecallSources[source.ID] = source.Recall != nil && source.Injection == "memory_prefix" && memorycore.SourceRecallKey(source.Locator) != ""
	}
	id := rand.Text()
	g.promptSourceViewsMu.Lock()
	defer g.promptSourceViewsMu.Unlock()
	if g.promptSourceViews == nil {
		g.promptSourceViews = make(map[string]personalPromptSourceView)
	}
	for key, previous := range g.promptSourceViews {
		if time.Now().After(previous.ExpiresAt) {
			delete(g.promptSourceViews, key)
		}
	}
	// Each view contains at most 256 sources: sixteen views bound the cache.
	if len(g.promptSourceViews) >= 16 {
		oldestID := ""
		var oldest time.Time
		for key, previous := range g.promptSourceViews {
			if oldestID == "" || previous.ExpiresAt.Before(oldest) {
				oldestID, oldest = key, previous.ExpiresAt
			}
		}
		delete(g.promptSourceViews, oldestID)
	}
	g.promptSourceViews[id] = view
	return id
}

func (g *GatewayService) promptViewSource(r *http.Request, agent string, sessions []personalMemorySession, action personalPromptSourceMutation) (memorycore.PromptLocator, bool) {
	g.promptSourceViewsMu.Lock()
	view, found := g.promptSourceViews[action.ViewID]
	g.promptSourceViewsMu.Unlock()
	if !found || time.Now().After(view.ExpiresAt) || view.Agent != agent ||
		view.ProjectID != g.project.ProjectID || !projectctx.SameWorkspace(view.Workspace, g.currentProjectWorkspace()) ||
		view.MediaOwner != g.personalMemoryMediaOwner(r) || view.SessionID != "" && !promptInputLegalSession(sessions, view.SessionID) {
		return memorycore.PromptLocator{}, false
	}
	locator, found := view.Sources[action.SourceID]
	if action.Action == "set_recall_enabled" && !view.RecallSources[action.SourceID] {
		return memorycore.PromptLocator{}, false
	}
	if !found || locator.SessionID != "" && !promptInputLegalSession(sessions, locator.SessionID) {
		return memorycore.PromptLocator{}, false
	}
	return locator, true
}

func (g *GatewayService) handlePersonalPromptSources(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if !personalMemoryLocalRequest(r) {
		sendJSONError(w, "Personal memory management is available on localhost only", http.StatusForbidden)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		w.Header().Set("Allow", "GET, POST")
		sendJSONError(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	agent := r.URL.Query().Get("agent")
	var action personalPromptSourceMutation
	if r.Method == http.MethodPost {
		if r.Header.Get("Content-Type") != "application/json" {
			sendJSONError(w, "JSON request required", http.StatusUnsupportedMediaType)
			return
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 512<<10))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&action) != nil || decoder.Decode(&struct{}{}) != io.EOF ||
			action.ViewID == "" || len(action.ViewID) > 64 || action.SourceID == "" || len(action.SourceID) > 128 ||
			action.ExpectedRevision == "" || len(action.ExpectedRevision) > 256 || len(action.Value) > 192<<10 ||
			(action.Action != "update" && action.Action != "delete" && action.Action != "set_recall_enabled") {
			sendJSONError(w, "Invalid source edit", http.StatusBadRequest)
			return
		}
		if action.Action == "set_recall_enabled" {
			if action.RecallEnabled == nil || action.ExpectedRecallVersion == nil || *action.ExpectedRecallVersion < 0 || action.Value != "" {
				sendJSONError(w, "Current recall version and enabled setting required", http.StatusBadRequest)
				return
			}
		} else if action.RecallEnabled != nil || action.ExpectedRecallVersion != nil {
			sendJSONError(w, "Recall settings require a recall action", http.StatusBadRequest)
			return
		}
		agent = action.Agent
	}
	agent, names, err := g.personalMemoryAgent(agent)
	if err != nil {
		sendJSONError(w, "Memory agent unavailable", http.StatusBadRequest)
		return
	}
	sessions, err := g.personalMemorySessions(r, agent)
	if err != nil {
		sendJSONError(w, "Unable to verify memory conversations", http.StatusInternalServerError)
		return
	}
	service := g.promptInventoryService(agent)
	owner := memorycore.Owner{TenantID: defaultExecutionTenantID, PartitionID: defaultExecutionTenantID, ImportLegacy: false}
	if r.Method == http.MethodPost {
		locator, found := g.promptViewSource(r, agent, sessions, action)
		if !found {
			sendJSONError(w, "Source view expired or is unavailable; refresh first", http.StatusNotFound)
			return
		}
		if action.Action == "set_recall_enabled" {
			// Revalidate the original row's current identity and revision. The
			// opaque view is a bounded address cache, never lasting permission.
			current, err := g.readPersonalPromptSource(r, agent, service, owner, sessions, locator)
			if err != nil || current == nil || current.Missing {
				sendJSONError(w, "Original source is unavailable; refresh first", http.StatusNotFound)
				return
			}
			if current.Revision != action.ExpectedRevision {
				sendJSONError(w, "Original source changed; refresh first", http.StatusConflict)
				return
			}
			setting, err := service.SetSourceRecallEnabled(r.Context(), owner, locator, *action.RecallEnabled, *action.ExpectedRecallVersion)
			if err != nil {
				status := http.StatusInternalServerError
				if errors.Is(err, memorycore.ErrPromptSourceConflict) {
					status = http.StatusConflict
				}
				sendJSONError(w, "Recall setting changed or is unavailable; refresh first", status)
				return
			}
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"ok": true, "recall": setting})
			return
		}
		current, err := g.changePersonalPromptSource(r, agent, service, owner, sessions, locator,
			personalPromptSourceAction{Action: action.Action, ExpectedRevision: action.ExpectedRevision, Value: action.Value})
		if err != nil {
			status := http.StatusBadRequest
			if errors.Is(err, memorycore.ErrPromptSourceConflict) || errors.Is(err, projectctx.ErrInstructionConflict) {
				status = http.StatusConflict
			} else if errors.Is(err, gorm.ErrRecordNotFound) {
				status = http.StatusNotFound
			}
			sendJSONError(w, "Source changed or is unavailable; refresh before editing", status)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"ok": true, "current": current})
		return
	}
	for key, values := range r.URL.Query() {
		if (key != "agent" && key != "session_id" && key != "include_learning" && key != "mode") || len(values) != 1 {
			sendJSONError(w, "Invalid source context", http.StatusBadRequest)
			return
		}
	}
	if value := r.URL.Query().Get("include_learning"); value != "" && value != "1" {
		sendJSONError(w, "Invalid learning source option", http.StatusBadRequest)
		return
	}
	mode := r.URL.Query().Get("mode")
	if mode != "" && mode != "management" || mode == "management" && r.URL.Query().Has("session_id") {
		sendJSONError(w, "Invalid memory management context", http.StatusBadRequest)
		return
	}
	management := mode == "management"
	sessionID := r.URL.Query().Get("session_id")
	if sessionID != "" && !promptInputLegalSession(sessions, sessionID) {
		sendJSONError(w, "Conversation is unavailable", http.StatusNotFound)
		return
	}
	if !management && sessionID == "" && len(sessions) > 0 {
		sessionID = sessions[0].ID
	}
	owner.SessionID = sessionID
	sources, providers, err := g.personalPromptSources(r, agent, owner, sessions, sessionID)
	if err != nil {
		sendJSONError(w, "Unable to read historical prompt sources", http.StatusInternalServerError)
		return
	}
	if management {
		availableProviders := providers[:0]
		for _, provider := range providers {
			if provider.ID != "session_history" {
				availableProviders = append(availableProviders, provider)
			}
		}
		providers = availableProviders
	}
	if management && g.personalPromptMemoryEnabled(agent) {
		history, err := g.personalPromptManagementReferences(r, agent, sessions)
		if err != nil {
			sendJSONError(w, "Unable to read authorized memory sources", http.StatusInternalServerError)
			return
		}
		sources = append(sources, history...)
		for i := range providers {
			if providers[i].ID == "web_history" {
				providers[i].Available, providers[i].Count = true, len(history)
				providers[i].Description = "管理本机已授权的任务结果、总结、明确原话和关联知识。实际对话仍按原用户、工作区、相关性及长度限制选用；这里列出不代表不同用户之间共享。"
			}
		}
	}
	if g.personalPromptMemoryEnabled(agent) {
		allowed := make([]string, 0, len(sessions))
		for _, session := range sessions {
			allowed = append(allowed, session.ID)
		}
		items, err := service.PromptSourceInventory(r.Context(), owner, allowed, 256)
		if err != nil {
			sendJSONError(w, "Unable to read memory prefix sources", http.StatusInternalServerError)
			return
		}
		for _, item := range items {
			sources = append(sources, personalPromptHistorySource{PromptSource: promptMemoryRecallSource(item.Item), Mode: item.Mode,
				Injection: "memory_prefix", CodePath: item.CodePath, CreatedAt: item.CreatedAt, UpdatedAt: item.UpdatedAt})
		}
		providers = append(providers, personalPromptProvider{ID: "unified_memory", Label: "偏好、工作记录与摘要", Mode: "relevant",
			CodePath: "Generate → buildMemoryPrompt → Service.Recall", Description: "当前会话工作记录和常驻偏好参与上下文；其他活跃记忆及摘要按问题召回，受范围、安全与长度预算限制。",
			Count: len(items), Available: true})
		if management {
			providers[len(providers)-1].Description = "已保存的个人、项目、共享记忆及合法对话摘要。实际使用保持各自归属和适用范围，按问题、安全与长度预算选择；不包含当前会话的临时上下文。"
		}
	}
	if r.URL.Query().Get("include_learning") == "1" {
		workspace := g.currentProjectWorkspace()
		notes, err := memorycore.ListLearningNotes(r.Context(), workspace, knowledgetools.LearningMemoryDirectory(workspace), 50)
		learningAvailable := err == nil
		for _, note := range notes {
			relative, err := filepath.Rel(workspace, note.Path)
			if err != nil {
				continue
			}
			locator := memorycore.PromptLocator{Kind: "learning_note", Path: note.Path, Field: "body", Digest: note.Current.Digest}
			sources = append(sources, personalPromptHistorySource{PromptSource: memorycore.PromptSource{Kind: "tool_learning", Category: "learning", Title: memorySourceText("assistant", note.Title, 240),
				SourceLabel: filepath.ToSlash(relative), Excerpt: memorySourceText("assistant", note.Content, 1200), Locator: locator},
				Mode: "on_demand", Injection: "tool_result", CodePath: "learn(action=insights) / read_file → tool result → next model call", UpdatedAt: note.UpdatedAt})
		}
		providers = append(providers, personalPromptProvider{ID: "tool_learning", Label: "AI 自己积累的学习笔记", Mode: "on_demand",
			CodePath: "learn → LearningMemoryDirectory → insights / read_file", Description: "读取 learn 工具真实保存的笔记。工具查阅后作为结果进入后续回合，不自动把整目录加入提示词。", Count: len(notes), Available: learningAvailable})
	}
	currentSources := make(map[string]interface{})
	visible := make([]personalPromptHistorySource, 0, len(sources))
	seen := make(map[memorycore.PromptLocator]bool)
	for _, source := range sources {
		if source.Locator.Kind == "" || seen[source.Locator] || source.Locator.SessionID != "" && !promptInputLegalSession(sessions, source.Locator.SessionID) {
			continue
		}
		current, readErr := g.readPersonalPromptSource(r, agent, service, owner, sessions, source.Locator)
		if readErr != nil || current == nil || current.Missing {
			continue
		}
		seen[source.Locator] = true
		source.ID = rand.Text()
		source.CapturedRevision = source.Locator.Digest
		currentSources[source.ID] = current
		visible = append(visible, source)
		if len(visible) >= 256 {
			break
		}
	}
	locators := make([]memorycore.PromptLocator, 0, len(visible))
	for _, source := range visible {
		if source.Injection == "memory_prefix" && memorycore.SourceRecallKey(source.Locator) != "" {
			locators = append(locators, source.Locator)
		}
	}
	settings, err := service.SourceRecallSettings(r.Context(), owner, locators)
	if err != nil {
		sendJSONError(w, "Unable to read source recall settings", http.StatusInternalServerError)
		return
	}
	for i := range visible {
		if visible[i].Injection == "memory_prefix" {
			if setting, found := settings[memorycore.SourceRecallKey(visible[i].Locator)]; found {
				visible[i].Recall = &setting
			}
		}
	}
	viewID := g.rememberPromptSourceView(r, agent, sessionID, visible)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"agent": agent, "agents": names, "sessions": sessions,
		"session_id": sessionID, "view_id": viewID, "sources": visible, "current_sources": currentSources, "providers": providers,
		"mode": "prompt_source_inventory", "management": management, "memory_enabled": g.personalPromptMemoryEnabled(agent), "bounded": true})
}
