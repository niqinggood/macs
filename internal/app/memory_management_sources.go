package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	memorycore "mac/internal/memory"
	"mac/internal/projectctx"
	sessioncore "mac/internal/session"
)

const personalMemorySourceLimit = 120

func sourceTime(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	copy := value
	return &copy
}

func memorySourceText(role, text string, limit int) string {
	text = sessioncore.CleanCarryoverContent(role, text, limit)
	text = redactAccountInfoTextForExport(text)
	if limit > 0 {
		runes := []rune(text)
		if len(runes) > limit {
			text = string(runes[:limit-1]) + "…"
		}
	}
	return text
}

func explicitMemoryPreference(text string) bool {
	if len(memorycore.DetectArtifactFormatPreferences(text)) > 0 || len(memorycore.DetectDurableUserCorrections(text)) > 0 {
		return true
	}
	lower := strings.ToLower(text)
	for _, marker := range []string{"我偏好", "我习惯", "我喜欢", "我不喜欢", "i prefer ", "my preference is ", "i always ", "i don't like "} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func explicitMemoryFeedback(text string) bool {
	for _, correction := range memorycore.DetectUserCorrections(text) {
		if correction.Type == "general_feedback" {
			return true
		}
	}
	return false
}

func memorySourceTools(raw []byte) []memorycore.SourceTool {
	var chain []map[string]interface{}
	if len(raw) > 1<<20 || json.Unmarshal(raw, &chain) != nil {
		return nil
	}
	tools := make([]memorycore.SourceTool, 0)
	for _, step := range chain {
		name, _ := step["name"].(string)
		typeName, _ := step["type"].(string)
		if typeName != "tool" && typeName != "skill" || name == "" || len(name) > 200 || strings.IndexFunc(name, unicode.IsControl) >= 0 {
			continue
		}
		tool := memorycore.SourceTool{Type: typeName, Name: name}
		if value, ok := step["success"].(bool); ok {
			tool.Success = &value
		}
		if value, ok := step["duration_ms"].(float64); ok && value >= 0 && value <= 86400000 {
			milliseconds := int64(value)
			tool.DurationMS = &milliseconds
		}
		tools = append(tools, tool)
		if len(tools) >= 50 {
			break
		}
	}
	return tools
}

func memorySourceMatches(reference memorycore.WorkingReference, kind, search string) bool {
	if kind != "" && reference.Kind != kind {
		return false
	}
	search = strings.ToLower(strings.TrimSpace(search))
	return search == "" || strings.Contains(strings.ToLower(reference.Key+" "+reference.Value+" "+reference.Feedback), search)
}

// Sources are read-only evidence from already persisted, verified Web tasks.
// Listing does not run a model, promote memories or backfill an old store.
func (g *GatewayService) personalMemorySourceReferences(r *http.Request, agent string, scope memorycore.MemoryScope, sessionID, kind, search string) ([]memorycore.WorkingReference, error) {
	return g.personalMemorySourceReferencesWithPolicy(r, agent, scope, sessionID, kind, search, false)
}

// Both automatic recall and the management candidate inventory use this
// policy. The ordinary historical browser still exposes the original records.
func (g *GatewayService) personalMemoryPromptSourceReferences(r *http.Request, agent string, scope memorycore.MemoryScope, sessionID, kind, search string) ([]memorycore.WorkingReference, error) {
	return g.personalMemorySourceReferencesWithPolicy(r, agent, scope, sessionID, kind, search, true)
}

func (g *GatewayService) personalMemorySourceReferencesWithPolicy(r *http.Request, agent string, scope memorycore.MemoryScope, sessionID, kind, search string, promptCandidates bool) ([]memorycore.WorkingReference, error) {
	result := make([]memorycore.WorkingReference, 0)
	if scope == memorycore.MemoryGlobal || !g.db.Migrator().HasTable(&ChatSession{}) {
		return result, nil
	}
	query := g.personalMemorySessionQuery(r, agent)
	if sessionID != "" {
		query = query.Where("id = ?", sessionID)
	}
	var rows []ChatSession
	if err := query.Order("updated_at DESC, id ASC").Limit(200).Find(&rows).Error; err != nil {
		return nil, err
	}
	sessions := make(map[string]ChatSession)
	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		if row.Workspace != "" && projectctx.SameWorkspace(row.Workspace, g.currentProjectWorkspace()) {
			sessions[row.ID] = row
			ids = append(ids, row.ID)
		}
	}
	if len(ids) == 0 {
		return result, nil
	}
	appendSource := func(reference memorycore.WorkingReference) {
		if memorySourceMatches(reference, kind, search) {
			result = append(result, reference)
		}
	}
	evidence := make(map[string][]string)
	var messages []ChatMessage
	if g.db.Migrator().HasTable(&ChatMessage{}) {
		messageQuery := scopeRowsToPersistedOwner(g.db.WithContext(r.Context()).Model(&ChatMessage{}), defaultExecutionTenantID, "").
			Select("id,session_id,tenant_id,subject_id,user_id,agent_name,role,status,media_owner,created_at,substr(content,1,8000) AS content").
			Where("session_id IN ? AND role IN ?", ids, []string{"user", "assistant"}).
			Where("media_owner = '' OR media_owner IS NULL OR media_owner = ?", g.personalMemoryMediaOwner(r))
		if err := messageQuery.Order("created_at DESC,id DESC").Limit(600).Find(&messages).Error; err != nil {
			return nil, err
		}
	}
	for _, message := range messages {
		session := sessions[message.SessionID]
		if message.UserId != session.UserId || message.AgentName != "" && message.AgentName != agent {
			continue
		}
		text := memorySourceText(message.Role, message.Content, 1200)
		if text == "" || message.Role != "user" {
			continue
		}
		evidence[message.SessionID] = append(evidence[message.SessionID], text)
		ref := memorycore.WorkingReference{ID: fmt.Sprintf("statement:%d", message.ID), SourceID: strconv.FormatUint(uint64(message.ID), 10),
			Value: text, Scope: memorycore.MemorySession, SessionID: message.SessionID, CreatedAt: sourceTime(message.CreatedAt), UpdatedAt: message.CreatedAt, ReadOnly: true, Evidence: text}
		if explicitMemoryPreference(text) {
			ref.Key, ref.Kind, ref.SourceType = "明确偏好原话", "preference", "user_statement"
			appendSource(ref)
		} else if explicitMemoryFeedback(text) {
			ref.Key, ref.Kind, ref.SourceType, ref.Feedback = "用户反馈原话", "experience", "user_feedback", text
			appendSource(ref)
		}
	}
	var events []memorycore.MemoryEvent
	if g.db.Migrator().HasTable(&memorycore.MemoryEvent{}) {
		if err := g.db.WithContext(r.Context()).Model(&memorycore.MemoryEvent{}).
			Select("id,tenant_id,agent_name,session_id,user_id,user_channel,status,created_at,updated_at,user_rating,substr(user_question,1,8000) AS user_question,substr(final_answer,1,8000) AS final_answer,substr(learn_note,1,8000) AS learn_note,substr(user_feedback,1,8000) AS user_feedback,substr(error_msg,1,8000) AS error_msg,substr(output_meta,1,65537) AS output_meta,substr(insights,1,65536) AS insights,substr(call_chain,1,1048576) AS call_chain").
			Where("tenant_id = ? AND agent_name = ? AND session_id IN ?", defaultExecutionTenantID, agent, ids).
			Order("created_at DESC,id DESC").Limit(50).Find(&events).Error; err != nil {
			return nil, err
		}
	}
	hasEvent := make(map[string]bool)
	verifiedEvents := make(map[string]memorycore.MemoryEvent)
	verifiedEvent := func(event memorycore.MemoryEvent) bool {
		session, found := sessions[event.SessionID]
		return found && event.UserID == session.UserId && (event.UserChannel == "" || event.UserChannel == "web") && (event.Status == "success" || event.Status == "failed")
	}
	var learningParents map[uint]memorycore.MemoryEvent
	if promptCandidates {
		parentIDs := make([]uint, 0)
		seen := make(map[uint]bool)
		for _, event := range events {
			if !verifiedEvent(event) {
				continue
			}
			if id := memorycore.TaskReferenceLearningSource(event); id != 0 && !seen[id] {
				parentIDs, seen[id] = append(parentIDs, id), true
			}
		}
		learningParents = make(map[uint]memorycore.MemoryEvent)
		if len(parentIDs) > 0 {
			var parents []memorycore.MemoryEvent
			if err := g.db.WithContext(r.Context()).Model(&memorycore.MemoryEvent{}).
				Select("id,tenant_id,agent_name,session_id,user_id,user_channel,status,substr(call_chain,1,1048576) AS call_chain").
				Where("id IN ? AND tenant_id = ? AND agent_name = ? AND session_id IN ?", parentIDs, defaultExecutionTenantID, agent, ids).
				Limit(50).Find(&parents).Error; err != nil {
				return nil, err
			}
			for _, parent := range parents {
				if verifiedEvent(parent) {
					learningParents[parent.ID] = parent
				}
			}
		}
	}
	for _, event := range events {
		if !verifiedEvent(event) {
			continue
		}
		verifiedEvents[strconv.FormatUint(uint64(event.ID), 10)] = event
		selection := memorycore.TaskReferenceSelection{IncludeResult: true, IncludeInsight: true}
		if promptCandidates {
			var parent *memorycore.MemoryEvent
			if original, ok := learningParents[memorycore.TaskReferenceLearningSource(event)]; ok {
				parent = &original
			}
			selection = memorycore.SelectTaskMemoryReferences(event, parent)
		}
		question, answer := memorySourceText("user", event.UserQuestion, 240), memorySourceText("assistant", event.FinalAnswer, 1200)
		if question == "" && answer == "" {
			continue
		}
		hasEvent[event.SessionID] = true
		ref := memorycore.WorkingReference{ID: fmt.Sprintf("event:%d", event.ID), SourceID: strconv.FormatUint(uint64(event.ID), 10),
			Key: "任务结果 · " + question, Value: answer, Scope: memorycore.MemorySession, Kind: "experience", SourceType: "task_execution",
			SessionID: event.SessionID, CreatedAt: sourceTime(event.CreatedAt), UpdatedAt: event.UpdatedAt, ReadOnly: true, TaskStatus: event.Status,
			Tools: memorySourceTools(event.CallChain), Feedback: memorySourceText("user", event.UserFeedback, 1200)}
		if event.UserRating != nil && *event.UserRating >= 1 && *event.UserRating <= 5 {
			ref.Rating = event.UserRating
		}
		if answer != "" && selection.IncludeResult {
			appendSource(ref)
		}
		insight := memorySourceText("assistant", event.LearnNote, 1200)
		if insight == "" && len(event.Insights) <= 64<<10 {
			var items []string
			if json.Unmarshal(event.Insights, &items) == nil {
				insight = memorySourceText("assistant", strings.Join(items, "\n"), 1200)
			}
		}
		if insight != "" && selection.IncludeInsight {
			ref.ID, ref.Key, ref.Value, ref.SourceType = fmt.Sprintf("insight:%d", event.ID), "已有任务经验 · "+question, insight, "task_insight"
			appendSource(ref)
		}
	}
	learned, err := g.personalMemoryLearnedKnowledgeReferences(r, agent, verifiedEvents)
	if err != nil {
		return nil, err
	}
	for _, reference := range learned {
		appendSource(reference)
	}
	// If no task event exists, use a real persisted answer excerpt. It has no
	// invented tool receipts and is explicitly labelled a conversation source.
	for _, message := range messages {
		session := sessions[message.SessionID]
		if hasEvent[message.SessionID] || message.Role != "assistant" || message.UserId != session.UserId || message.AgentName != "" && message.AgentName != agent || message.Status != "completed" {
			continue
		}
		value := memorySourceText("assistant", message.Content, 1200)
		if value != "" {
			appendSource(memorycore.WorkingReference{ID: fmt.Sprintf("conversation:%d", message.ID), SourceID: strconv.FormatUint(uint64(message.ID), 10),
				Key: "对话回答摘录", Value: value, Scope: memorycore.MemorySession, Kind: "experience", SourceType: "conversation",
				SessionID: message.SessionID, CreatedAt: sourceTime(message.CreatedAt), UpdatedAt: message.CreatedAt, ReadOnly: true})
		}
	}
	if globalKG != nil && globalKG.BelongsToWorkspace(g.currentProjectWorkspace()) {
		facts, err := globalKG.ConversationFactSources(r.Context(), ids, 100)
		if err != nil {
			return nil, err
		}
		for _, item := range facts {
			id := strings.TrimPrefix(item.Fact.Source, "conversation:session:")
			quote := memorySourceText("user", item.Evidence, 1200)
			if len([]rune(quote)) < 4 {
				continue
			}
			supported := false
			for _, original := range evidence[id] {
				if strings.Contains(original, quote) {
					supported = true
					break
				}
			}
			if !supported {
				continue
			}
			kind := "fact"
			if explicitMemoryPreference(quote) {
				kind = "preference"
			}
			created, _ := time.Parse("2006-01-02 15:04:05", item.Fact.CreatedAt)
			confidence := item.Fact.Confidence
			value := memorySourceText("assistant", item.Fact.Subject+" · "+item.Fact.Predicate+" · "+item.Fact.Object, 1200)
			appendSource(memorycore.WorkingReference{ID: fmt.Sprintf("fact:%d", item.Fact.ID), SourceID: strconv.FormatInt(item.Fact.ID, 10), Key: "有原文依据的图谱事实",
				Value: value, Scope: memorycore.MemorySession, Kind: kind, SourceType: "knowledge_fact", SessionID: id,
				CreatedAt: sourceTime(created), UpdatedAt: created, ReadOnly: true, Evidence: quote,
				Graph: &memorycore.GraphSource{Subject: memorySourceText("assistant", item.Fact.Subject, 240), Predicate: memorySourceText("assistant", item.Fact.Predicate, 240),
					Object: memorySourceText("assistant", item.Fact.Object, 1200), Origin: item.Fact.Source, ValidFrom: item.Fact.ValidFrom, ValidTo: item.Fact.ValidTo,
					Confidence: &confidence, EvidenceAvailability: "quote_verified", QuotedEvidence: quote}})
		}
	}
	sort.SliceStable(result, func(i, j int) bool { return result[i].UpdatedAt.After(result[j].UpdatedAt) })
	if len(result) > personalMemorySourceLimit {
		result = result[:personalMemorySourceLimit]
	}
	return result, nil
}

// A legacy Knowledge row has no workspace or user column. Its SourceID must
// identify an event already verified against the current Web session owner.
// This is a read-only adapter; no old record is migrated or made authoritative.
func (g *GatewayService) personalMemoryLearnedKnowledgeReferences(r *http.Request, agent string, events map[string]memorycore.MemoryEvent) ([]memorycore.WorkingReference, error) {
	result := make([]memorycore.WorkingReference, 0)
	if len(events) == 0 || !g.db.Migrator().HasTable(&memorycore.Knowledge{}) {
		return result, nil
	}
	ids := make([]string, 0, len(events))
	for id := range events {
		ids = append(ids, id)
	}
	var rows []memorycore.Knowledge
	if err := g.db.WithContext(r.Context()).Model(&memorycore.Knowledge{}).
		Select("id,tenant_id,agent_name,source_id,category,created_at,updated_at,substr(tags,1,5000) AS tags,substr(title,1,500) AS title,substr(summary,1,8000) AS summary,substr(content,1,8001) AS content").
		Where("tenant_id = ? AND agent_name = ? AND source_id IN ?", defaultExecutionTenantID, agent, ids).
		Order("updated_at DESC,id DESC").Limit(50).Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		event, found := events[row.SourceID]
		if !found {
			continue
		}
		value := memorySourceText("assistant", row.Summary, 1200)
		if value == "" {
			value = memorySourceText("assistant", row.Content, 1200)
		}
		if value == "" {
			continue
		}
		kind := "experience"
		if row.Category == "fact" || row.Category == "knowledge" || row.Category == "tutorial" {
			kind = "fact"
		}
		title := memorySourceText("assistant", row.Title, 240)
		if title == "" {
			title = "Agent 学习知识"
		}
		var tags []string
		if json.Unmarshal(row.Tags, &tags) != nil {
			tags = nil
		}
		if len(tags) > 20 {
			tags = tags[:20]
		}
		for i := range tags {
			tags[i] = memorySourceText("assistant", tags[i], 80)
		}
		result = append(result, memorycore.WorkingReference{ID: fmt.Sprintf("learning:%d", row.ID), SourceID: strconv.FormatUint(uint64(row.ID), 10),
			Key: title, Value: value, Scope: memorycore.MemorySession, Kind: kind, SourceType: "learning_knowledge", SessionID: event.SessionID,
			OriginalContent: memorySourceText("assistant", row.Content, 8000), ContentTruncated: len([]rune(row.Content)) > 8000,
			CreatedAt: sourceTime(row.CreatedAt), UpdatedAt: row.UpdatedAt, ReadOnly: true, Tags: tags})
	}
	return result, nil
}
