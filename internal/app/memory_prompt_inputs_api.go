package app

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"gorm.io/gorm"
	memorycore "mac/internal/memory"
	"mac/internal/projectctx"
)

type personalPromptSourceAction struct {
	Agent            string `json:"agent"`
	InputID          string `json:"input_id"`
	SourceID         string `json:"source_id"`
	Action           string `json:"action"`
	ExpectedRevision string `json:"expected_revision"`
	Value            string `json:"value"`
}

func promptSourceDigest(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

func promptInputLegalSession(sessions []personalMemorySession, id string) bool {
	for _, session := range sessions {
		if session.ID == id {
			return true
		}
	}
	return false
}

func (g *GatewayService) handlePersonalMemoryInputs(w http.ResponseWriter, r *http.Request) {
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
	var action personalPromptSourceAction
	agent := r.URL.Query().Get("agent")
	if r.Method == http.MethodPost {
		if !strings.HasPrefix(strings.ToLower(r.Header.Get("Content-Type")), "application/json") {
			sendJSONError(w, "JSON request required", http.StatusUnsupportedMediaType)
			return
		}
		// Session summaries support 48,000 Unicode characters. Allow their
		// UTF-8 size and JSON escaping while keeping this request bounded.
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 512<<10))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&action) != nil || decoder.Decode(&struct{}{}) != io.EOF ||
			len(action.InputID) > 64 || len(action.SourceID) > 128 || action.InputID == "" || action.SourceID == "" ||
			len(action.ExpectedRevision) > 256 || action.ExpectedRevision == "" || len(action.Value) > 192<<10 ||
			(action.Action != "update" && action.Action != "delete") {
			sendJSONError(w, "Invalid source edit", http.StatusBadRequest)
			return
		}
		agent = action.Agent
	}
	service, owner, names, err := g.personalMemoryService(agent)
	if err != nil {
		sendJSONError(w, "Memory service unavailable", http.StatusServiceUnavailable)
		return
	}
	agent = serviceAgent(names, agent)
	sessions, err := g.personalMemorySessions(r, agent)
	if err != nil {
		sendJSONError(w, "Unable to read conversations", http.StatusInternalServerError)
		return
	}
	inputID := r.URL.Query().Get("input_id")
	if r.Method == http.MethodPost {
		inputID = action.InputID
	}
	sessionID := r.URL.Query().Get("session_id")
	if len(inputID) > 64 || sessionID != "" && !promptInputLegalSession(sessions, sessionID) {
		sendJSONError(w, "Invalid memory context", http.StatusBadRequest)
		return
	}
	var selected *memorycore.PromptInput
	if inputID != "" {
		selected, err = service.GetPromptInput(r.Context(), owner, inputID)
		if err != nil || selected == nil || !promptInputLegalSession(sessions, selected.SessionID) ||
			sessionID != "" && selected.SessionID != sessionID {
			sendJSONError(w, "Input reference is unavailable", http.StatusNotFound)
			return
		}
	}
	if r.Method == http.MethodPost {
		var source *memorycore.PromptSource
		for i := range selected.Sources {
			if selected.Sources[i].ID == action.SourceID {
				source = &selected.Sources[i]
				break
			}
		}
		if source == nil {
			sendJSONError(w, "Source reference is unavailable", http.StatusNotFound)
			return
		}
		current, err := g.changePersonalPromptSource(r, agent, service, owner, sessions, source.Locator, action)
		if err != nil {
			status := http.StatusBadRequest
			if errors.Is(err, memorycore.ErrPromptSourceConflict) || errors.Is(err, projectctx.ErrInstructionConflict) {
				status = http.StatusConflict
			} else if errors.Is(err, gorm.ErrRecordNotFound) {
				status = http.StatusNotFound
			}
			sendJSONError(w, "Source changed or is unavailable; reload before editing", status)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"ok": true, "current": current})
		return
	}
	// Bind only the session already authorized by the host above. The domain
	// reader rejects a filter that does not match its trusted owner context.
	owner.SessionID = sessionID
	inputs, err := service.RecentPromptInputs(r.Context(), owner, sessionID, 50)
	if err != nil {
		sendJSONError(w, "Unable to read model input references", http.StatusInternalServerError)
		return
	}
	visible := make([]memorycore.PromptInput, 0, len(inputs))
	for _, input := range inputs {
		if !promptInputLegalSession(sessions, input.SessionID) {
			continue
		}
		if selected == nil {
			copy := input
			selected = &copy
		}
		// The selector needs metadata. Only the selected request sends excerpts.
		input.Sources = nil
		visible = append(visible, input)
	}
	currentSources := make(map[string]interface{})
	if selected != nil {
		// A captured excerpt still contains source data. Revalidate its source
		// audience before returning it, not only before reading the live row.
		visibleSources := make([]memorycore.PromptSource, 0, len(selected.Sources))
		for _, source := range selected.Sources {
			if source.Locator.SessionID == "" || promptInputLegalSession(sessions, source.Locator.SessionID) {
				visibleSources = append(visibleSources, source)
			}
		}
		selected.Sources = visibleSources
		for _, source := range selected.Sources {
			current, readErr := g.readPersonalPromptSource(r, agent, service, owner, sessions, source.Locator)
			if readErr != nil {
				currentSources[source.ID] = map[string]interface{}{"editable": false, "deletable": false, "error": "source_unavailable"}
			} else {
				currentSources[source.ID] = current
			}
		}
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"agents": names, "agent": agent, "sessions": sessions,
		"inputs": visible, "input": selected, "current_sources": currentSources})
}

func (g *GatewayService) promptSourceSession(r *http.Request, agent string, sessions []personalMemorySession, locator memorycore.PromptLocator) (*ChatSession, error) {
	if !promptInputLegalSession(sessions, locator.SessionID) {
		return nil, gorm.ErrRecordNotFound
	}
	var session ChatSession
	err := g.personalMemorySessionQuery(r, agent).Where("id = ?", locator.SessionID).First(&session).Error
	return &session, err
}

func (g *GatewayService) promptMessageQuery(r *http.Request, agent string, session *ChatSession, locator memorycore.PromptLocator) *gorm.DB {
	return scopeRowsToPersistedOwner(g.db.WithContext(r.Context()).Model(&ChatMessage{}), defaultExecutionTenantID, "").
		Where("id = ? AND session_id = ? AND user_id = ?", locator.ID, session.ID, session.UserId).
		Where("agent_name = ? OR agent_name = ''", agent).
		Where("media_owner = '' OR media_owner IS NULL OR media_owner = ?", g.personalMemoryMediaOwner(r))
}

func (g *GatewayService) promptEventQuery(r *http.Request, agent string, session *ChatSession, id string) *gorm.DB {
	return g.db.WithContext(r.Context()).Model(&memorycore.MemoryEvent{}).
		Where("id = ? AND tenant_id = ? AND agent_name = ? AND session_id = ? AND user_id = ?", id, defaultExecutionTenantID, agent, session.ID, session.UserId).
		Where("user_channel IN ?", []string{"", "web"}).Where("status IN ?", []string{"success", "failed"})
}

func memoryEventSourceValue(event memorycore.MemoryEvent, field string) (string, bool) {
	switch field {
	case "final_answer":
		return event.FinalAnswer, true
	case "learn_note":
		return event.LearnNote, true
	case "insights":
		return string(event.Insights), true
	case "user_feedback":
		return event.UserFeedback, true
	}
	return "", false
}

func memoryEventSourceRevision(event memorycore.MemoryEvent, field, digest string) string {
	if field == "learn_note" || field == "insights" {
		// Both fields participate in experience fallback. A deletion clears
		// both, so neither field may have changed since the user's last read.
		data, _ := json.Marshal([]string{event.LearnNote, string(event.Insights)})
		return digest + ":" + promptSourceDigest(string(data))
	}
	return digest
}

func (g *GatewayService) readPersonalPromptSource(r *http.Request, agent string, service *memorycore.Service, owner memorycore.Owner, sessions []personalMemorySession, locator memorycore.PromptLocator) (*memorycore.CurrentSource, error) {
	if locator.SessionID != "" && !promptInputLegalSession(sessions, locator.SessionID) {
		return nil, gorm.ErrRecordNotFound
	}
	owner.SessionID = locator.SessionID
	if locator.Kind == "entry" && locator.Field == "global" {
		service = memorycore.NewGlobalService(g.db, g.currentProjectWorkspace())
	}
	switch locator.Kind {
	case "learning_note":
		note, err := memorycore.ReadLearningNote(g.currentProjectWorkspace(), locator.Path)
		if err != nil {
			return nil, err
		}
		return &note.Current, nil
	case "entry", "context", "summary":
		return service.ReadPromptSource(r.Context(), owner, locator)
	case "project_instruction":
		current, err := projectctx.ReadInstructionSource(g.currentProjectWorkspace(), locator.Path)
		if err != nil {
			return nil, err
		}
		return &memorycore.CurrentSource{Content: current.Content, Revision: current.Revision, Digest: current.Revision,
			Editable: current.Editable, Deletable: current.Editable, Missing: current.Missing}, nil
	case "knowledge_fact":
		if globalKG == nil || !globalKG.BelongsToWorkspace(g.currentProjectWorkspace()) || locator.Field != "object" || locator.SessionID == "" {
			return nil, errors.New("fact source is unavailable")
		}
		id, err := strconv.ParseInt(locator.ID, 10, 64)
		if err != nil {
			return nil, err
		}
		return globalKG.ReadPromptFactSource(r.Context(), locator.SessionID, id)
	case "chat_message", "memory_event", "knowledge":
		session, err := g.promptSourceSession(r, agent, sessions, locator)
		if err != nil {
			return nil, err
		}
		var content string
		var revision string
		if locator.Kind == "chat_message" && locator.Field == "content" {
			var message ChatMessage
			err = g.promptMessageQuery(r, agent, session, locator).First(&message).Error
			content = message.Content
		} else if locator.Kind == "memory_event" {
			var event memorycore.MemoryEvent
			err = g.promptEventQuery(r, agent, session, locator.ID).First(&event).Error
			var valid bool
			content, valid = memoryEventSourceValue(event, locator.Field)
			if !valid {
				return nil, errors.New("unsupported event source")
			}
			revision = memoryEventSourceRevision(event, locator.Field, promptSourceDigest(content))
		} else if locator.Kind == "knowledge" && (locator.Field == "summary" || locator.Field == "content") {
			var knowledge memorycore.Knowledge
			err = g.promptKnowledgeQuery(r, agent, session, locator).First(&knowledge).Error
			content = knowledge.Content
			if locator.Field == "summary" {
				content = knowledge.Summary
			}
		} else {
			return nil, errors.New("unsupported source field")
		}
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return &memorycore.CurrentSource{Missing: true}, nil
		}
		if err != nil || len(content) > 96<<10 {
			return nil, errors.New("source content is unavailable")
		}
		digest := promptSourceDigest(content)
		if revision == "" {
			revision = digest
		}
		missing := content == ""
		if locator.Kind == "memory_event" && locator.Field == "insights" {
			var items []string
			missing = json.Unmarshal([]byte(content), &items) == nil && len(items) == 0
		}
		return &memorycore.CurrentSource{Content: content, Revision: revision, Digest: digest, Editable: true, Deletable: true, Missing: missing}, nil
	}
	return nil, errors.New("source has no safe management adapter")
}

func (g *GatewayService) promptKnowledgeQuery(r *http.Request, agent string, session *ChatSession, locator memorycore.PromptLocator) *gorm.DB {
	// A learning row has no user/session columns; only an exact authorized
	// originating event can confer permission. Its numeric ID alone cannot.
	events := g.db.WithContext(r.Context()).Model(&memorycore.MemoryEvent{}).Select("CAST(id AS CHAR)").
		Where("tenant_id = ? AND agent_name = ? AND session_id = ? AND user_id = ?", defaultExecutionTenantID, agent, session.ID, session.UserId).
		Where("user_channel IN ?", []string{"", "web"}).Where("status IN ?", []string{"success", "failed"})
	return g.db.WithContext(r.Context()).Model(&memorycore.Knowledge{}).
		Where("id = ? AND tenant_id = ? AND agent_name = ?", locator.ID, defaultExecutionTenantID, agent).
		Where("source_id IN (?)", events)
}

func (g *GatewayService) changePersonalPromptSource(r *http.Request, agent string, service *memorycore.Service, owner memorycore.Owner, sessions []personalMemorySession, locator memorycore.PromptLocator, action personalPromptSourceAction) (*memorycore.CurrentSource, error) {
	current, err := g.readPersonalPromptSource(r, agent, service, owner, sessions, locator)
	if err != nil {
		return nil, err
	}
	if current.Missing || !current.Editable || action.Action == "delete" && !current.Deletable || current.Revision != action.ExpectedRevision {
		return nil, memorycore.ErrPromptSourceConflict
	}
	owner.SessionID = locator.SessionID
	if locator.Kind == "entry" && locator.Field == "global" {
		service = memorycore.NewGlobalService(g.db, g.currentProjectWorkspace())
	}
	switch locator.Kind {
	case "entry", "context", "summary":
		return service.ChangePromptSource(r.Context(), owner, locator, action.ExpectedRevision, action.Action, action.Value)
	case "learning_note":
		return memorycore.ChangeLearningNote(g.currentProjectWorkspace(), locator.Path, action.ExpectedRevision, action.Action, action.Value)
	case "project_instruction":
		_, err = projectctx.ChangeInstructionSource(g.currentProjectWorkspace(), locator.Path, action.ExpectedRevision, action.Action, action.Value)
	case "knowledge_fact":
		id, parseErr := strconv.ParseInt(locator.ID, 10, 64)
		if parseErr != nil || globalKG == nil || !globalKG.BelongsToWorkspace(g.currentProjectWorkspace()) {
			return nil, errors.New("fact source is unavailable")
		}
		return globalKG.ChangePromptFactSource(r.Context(), locator.SessionID, id, action.ExpectedRevision, action.Action, action.Value)
	case "chat_message", "memory_event", "knowledge":
		if action.Action == "update" && strings.TrimSpace(action.Value) == "" {
			return nil, errors.New("source text must not be blank")
		}
		session, readErr := g.promptSourceSession(r, agent, sessions, locator)
		if readErr != nil {
			return nil, readErr
		}
		var result *gorm.DB
		if locator.Kind == "chat_message" {
			query := g.promptMessageQuery(r, agent, session, locator).Where("content = ?", current.Content)
			if action.Action == "delete" {
				result = query.Delete(&ChatMessage{})
			} else {
				result = query.Update("content", action.Value)
			}
		} else if locator.Kind == "memory_event" {
			query := g.promptEventQuery(r, agent, session, locator.ID).Where(locator.Field+" = ?", current.Content)
			if locator.Field == "learn_note" || locator.Field == "insights" {
				var event memorycore.MemoryEvent
				if err := g.promptEventQuery(r, agent, session, locator.ID).First(&event).Error; err != nil {
					return nil, err
				}
				text, _ := memoryEventSourceValue(event, locator.Field)
				if memoryEventSourceRevision(event, locator.Field, promptSourceDigest(text)) != action.ExpectedRevision {
					return nil, memorycore.ErrPromptSourceConflict
				}
				query = query.Where("COALESCE(learn_note, '') = ? AND COALESCE(insights, '') = ?", event.LearnNote, string(event.Insights))
			}
			updates := map[string]interface{}{locator.Field: action.Value}
			if action.Action == "delete" {
				updates[locator.Field] = ""
				if locator.Field == "learn_note" || locator.Field == "insights" {
					updates["learn_note"], updates["insights"] = "", "[]"
				}
			} else if locator.Field == "insights" {
				var items []string
				if json.Unmarshal([]byte(action.Value), &items) != nil {
					return nil, errors.New("insights source requires a JSON string array")
				}
			}
			result = query.Updates(updates)
		} else {
			query := g.promptKnowledgeQuery(r, agent, session, locator).Where(locator.Field+" = ?", current.Content)
			if action.Action == "delete" {
				result = query.Delete(&memorycore.Knowledge{})
			} else {
				result = query.Update(locator.Field, action.Value)
			}
		}
		if result.Error != nil {
			return nil, result.Error
		}
		if result.RowsAffected != 1 {
			return nil, memorycore.ErrPromptSourceConflict
		}
	default:
		return nil, errors.New("source cannot be changed")
	}
	if err != nil {
		return nil, err
	}
	return g.readPersonalPromptSource(r, agent, service, owner, sessions, locator)
}
