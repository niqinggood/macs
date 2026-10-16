package app

import (
	"net/http"
	"strconv"
	"strings"

	memorycore "mac/internal/memory"
	"mac/internal/projectctx"
)

type personalMemoryProvenanceIndex struct {
	sessions map[string]ChatSession
	messages map[string]ChatMessage
	quotes   map[string][]ChatMessage
	events   map[string]memorycore.MemoryEvent
	learning map[string]string
	tasks    map[string]GatewayTaskRecord
}

// This index reads already authorized sources. It does not resolve arbitrary
// SourceRef strings, repair old rows, import files, or invoke model extraction.
func (g *GatewayService) personalMemoryProvenanceIndex(r *http.Request, agent string, references []memorycore.WorkingReference) (personalMemoryProvenanceIndex, error) {
	index := personalMemoryProvenanceIndex{sessions: map[string]ChatSession{}, messages: map[string]ChatMessage{}, quotes: map[string][]ChatMessage{},
		events: map[string]memorycore.MemoryEvent{}, learning: map[string]string{}, tasks: map[string]GatewayTaskRecord{}}
	sessions, err := g.personalMemoryCatalogSessions(r, agent)
	if err != nil {
		return index, err
	}
	ids := make([]string, 0, len(sessions))
	for _, session := range sessions {
		index.sessions[session.ID] = session
		ids = append(ids, session.ID)
	}
	if len(ids) == 0 {
		return index, nil
	}
	taskIDs := make([]string, 0)
	if g.db.Migrator().HasTable(&ChatMessage{}) {
		var messages []ChatMessage
		err = scopeRowsToPersistedOwner(g.db.WithContext(r.Context()).Model(&ChatMessage{}), defaultExecutionTenantID, "").
			Select("id,session_id,user_id,agent_name,role,task_id,substr(content,1,8000) AS content").
			Where("session_id IN ? AND role IN ?", ids, []string{"user", "assistant"}).
			Where("media_owner = '' OR media_owner IS NULL OR media_owner = ?", g.personalMemoryMediaOwner(r)).
			Order("created_at DESC,id DESC").Limit(600).Find(&messages).Error
		if err != nil {
			return index, err
		}
		for _, message := range messages {
			if session, ok := index.sessions[message.SessionID]; !ok || message.UserId != session.UserId || message.AgentName != "" && message.AgentName != agent {
				continue
			}
			index.messages[strconv.FormatUint(uint64(message.ID), 10)] = message
			if message.Role == "user" {
				message.Content = memorySourceText("user", message.Content, 8000)
				index.quotes[message.SessionID] = append(index.quotes[message.SessionID], message)
			}
			if message.TaskID != "" && len(message.TaskID) <= 64 {
				taskIDs = append(taskIDs, message.TaskID)
			}
		}
	}
	if len(taskIDs) > 0 && g.db.Migrator().HasTable(&GatewayTaskRecord{}) {
		var tasks []GatewayTaskRecord
		err = scopeRowsToPersistedOwner(g.db.WithContext(r.Context()).Model(&GatewayTaskRecord{}), defaultExecutionTenantID, "").
			Select("task_id,session_id,user_id,agent_name,resolved_workspace").
			Where("task_id IN ? AND agent_name = ? AND tenant_scoped = ?", taskIDs, agent, false).
			Where("queue_name = '' OR queue_name IS NULL").Limit(600).Find(&tasks).Error
		if err != nil {
			return index, err
		}
		for _, task := range tasks {
			if session, ok := index.sessions[task.SessionID]; ok && task.UserID == session.UserId && task.ResolvedWorkspace != "" && projectctx.SameWorkspace(task.ResolvedWorkspace, g.currentProjectWorkspace()) {
				index.tasks[task.TaskID] = task
			}
		}
	}
	if g.db.Migrator().HasTable(&memorycore.MemoryEvent{}) {
		var events []memorycore.MemoryEvent
		if err = g.personalMemoryCatalogEvents(r, agent, sessions).Select("id,session_id").Order("created_at DESC,id DESC").Limit(50).Find(&events).Error; err != nil {
			return index, err
		}
		for _, event := range events {
			index.events[strconv.FormatUint(uint64(event.ID), 10)] = event
		}
	}
	learningIDs := make([]string, 0)
	for _, ref := range references {
		if ref.SourceType == "learning_knowledge" && ref.SourceID != "" {
			learningIDs = append(learningIDs, ref.SourceID)
		}
	}
	if len(learningIDs) > 0 && g.db.Migrator().HasTable(&memorycore.Knowledge{}) {
		var rows []memorycore.Knowledge
		if err = g.db.WithContext(r.Context()).Model(&memorycore.Knowledge{}).Select("id,source_id").
			Where("id IN ? AND tenant_id = ? AND agent_name = ?", learningIDs, defaultExecutionTenantID, agent).Limit(100).Find(&rows).Error; err != nil {
			return index, err
		}
		for _, row := range rows {
			if _, ok := index.events[row.SourceID]; ok {
				index.learning[strconv.FormatUint(uint64(row.ID), 10)] = row.SourceID
			}
		}
	}
	return index, nil
}

func (index personalMemoryProvenanceIndex) verifyQuote(sessionID, quote string) (ChatMessage, bool) {
	if len([]rune(strings.TrimSpace(quote))) < 4 {
		return ChatMessage{}, false
	}
	for _, message := range index.quotes[sessionID] {
		if strings.Contains(message.Content, quote) {
			return message, true
		}
	}
	return ChatMessage{}, false
}

func (index personalMemoryProvenanceIndex) linkMessage(facts *memorycore.ProvenanceFacts, message ChatMessage) {
	facts.Origin.MessageID = strconv.FormatUint(uint64(message.ID), 10)
	if task, ok := index.tasks[message.TaskID]; ok && task.SessionID == message.SessionID && task.UserID == message.UserId {
		facts.TaskLinked, facts.Origin.TaskID = true, task.TaskID
	}
}

func (g *GatewayService) personalMemoryRuntimeEnabled(agent string) *bool {
	if runtime := g.personalMemoryCatalogRuntime(agent); runtime != nil {
		value := runtime.hasMemory
		return &value
	}
	return nil
}

func (g *GatewayService) personalMemoryProvenance(r *http.Request, agent string, entries []memorycore.Entry, references []memorycore.WorkingReference) ([]memorycore.RecordProvenance, []memorycore.WorkingReference, error) {
	index, err := g.personalMemoryProvenanceIndex(r, agent, references)
	if err != nil {
		return nil, nil, err
	}
	enabled := g.personalMemoryRuntimeEnabled(agent)
	entryProvenance := make([]memorycore.RecordProvenance, 0, len(entries))
	for _, entry := range entries {
		facts := memorycore.ProvenanceFacts{Origin: memorycore.ProvenanceOrigin{Location: "memory_entries", RecordID: entry.ID}, RuntimeEnabled: enabled,
			QuoteStored: entry.SourceQuote != "" || entry.ConfirmedQuote != ""}
		sessionID := entry.SourceSessionID
		if sessionID == "" {
			sessionID = entry.SessionID
		}
		if _, ok := index.sessions[sessionID]; ok {
			facts.Origin.SessionID = sessionID
			for _, quote := range []string{entry.SourceQuote, entry.ConfirmedQuote} {
				if message, matched := index.verifyQuote(sessionID, quote); matched {
					facts.UserQuoteVerified = true
					facts.VerifiedQuote = quote
					index.linkMessage(&facts, message)
					break
				}
			}
		}
		entryProvenance = append(entryProvenance, memorycore.EntryProvenance(entry, facts))
	}
	for position := range references {
		ref := &references[position]
		if ref.Provenance != nil { // A global view carries the original Entry metadata.
			ref.Provenance.Recall.Enabled = g.personalMemoryRuntimeEnabled(ref.AgentName)
			continue
		}
		facts := memorycore.ProvenanceFacts{Origin: memorycore.ProvenanceOrigin{Location: ref.SourceLocation, RecordID: ref.SourceID}, RuntimeEnabled: enabled, QuoteStored: ref.Evidence != ""}
		if ref.Graph != nil && ref.Graph.QuotedEvidence != "" {
			facts.QuoteStored = true
		}
		if _, ok := index.sessions[ref.SessionID]; ok && ref.OwnershipVerified != nil && *ref.OwnershipVerified {
			facts.Origin.SessionID = ref.SessionID
			quote := ref.Evidence
			if quote == "" && ref.Graph != nil {
				quote = ref.Graph.QuotedEvidence
			}
			if message, matched := index.verifyQuote(ref.SessionID, quote); matched {
				facts.UserQuoteVerified = true
				facts.VerifiedQuote = quote
				index.linkMessage(&facts, message)
			}
		}
		switch ref.SourceType {
		case "task_execution", "task_insight":
			if event, ok := index.events[ref.SourceID]; ok && event.SessionID == ref.SessionID {
				facts.EventLinked, facts.Origin.EventID = true, ref.SourceID
			}
		case "learning_knowledge":
			if eventID, ok := index.learning[ref.SourceID]; ok {
				facts.EventLinked, facts.Origin.EventID = true, eventID
			}
		case "conversation", "user_statement", "user_feedback":
			if message, ok := index.messages[ref.SourceID]; ok && message.SessionID == ref.SessionID {
				index.linkMessage(&facts, message)
			}
		}
		p := memorycore.SourceProvenance(*ref, facts)
		ref.Provenance = &p
	}
	return entryProvenance, references, nil
}
