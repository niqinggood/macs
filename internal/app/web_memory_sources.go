package app

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	memorycore "mac/internal/memory"
	"mac/internal/projectctx"
)

type webMemoryAudienceKey struct{}

// This private snapshot is made by the HTTP host, never from a JSON channel or
// a serialized task. An account change must not change a queued task's audience.
type webMemoryAudience struct {
	gateway    *GatewayService
	workspace  string
	projectID  string
	mediaOwner string
	userID     string
	sessionID  string
}

func (g *GatewayService) captureWebMemoryAudience(r *http.Request, req *MessageRequest, workspace string) *webMemoryAudience {
	if g == nil || g.db == nil || req == nil || req.SessionId == "" || workspace == "" {
		return nil
	}
	return &webMemoryAudience{gateway: g, workspace: workspace, projectID: g.project.ProjectID,
		mediaOwner: g.mediaOwner(r), userID: req.UserId, sessionID: req.SessionId}
}

func webMemoryAudienceFromContext(ctx context.Context) *webMemoryAudience {
	if ctx == nil {
		return nil
	}
	audience, _ := ctx.Value(webMemoryAudienceKey{}).(*webMemoryAudience)
	return audience
}

func (g *GatewayService) personalMemoryMediaOwner(r *http.Request) string {
	if audience := webMemoryAudienceFromContext(r.Context()); audience != nil && audience.gateway == g {
		return audience.mediaOwner
	}
	return g.mediaOwner(r)
}

// Each source is tested for relevance separately by Service.Recall. In
// particular a related historical answer cannot bring an unrelated profile or
// tenant-wide tool statistic into the prompt as part of one mixed text block.
func (a *LLMAgent) webMemorySources(ctx context.Context, sessionID string) ([]memorycore.RecallItem, error) {
	audience := webMemoryAudienceFromContext(ctx)
	if audience == nil || audience.gateway == nil || sessionID != audience.sessionID || isRestrictedTenantExecution(ctx) ||
		!projectctx.SameWorkspace(a.workspace, audience.workspace) {
		return nil, nil
	}
	g := audience.gateway
	if g.project.ProjectID != audience.projectID || !projectctx.SameWorkspace(g.currentProjectWorkspace(), audience.workspace) {
		return nil, nil
	}
	bounded, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	r, err := http.NewRequestWithContext(bounded, http.MethodGet, "http://memory.local/api/memory", nil)
	if err != nil {
		return nil, err
	}
	// Require the exact persisted current conversation before browsing related
	// history; a fabricated session or another user's session fails closed.
	if !g.db.Migrator().HasTable(&ChatSession{}) {
		return nil, nil
	}
	var session ChatSession
	if err := g.personalMemorySessionQuery(r, a.name).Where("id = ?", sessionID).First(&session).Error; err != nil {
		return nil, nil
	}
	if !projectctx.SameWorkspace(session.Workspace, audience.workspace) {
		return nil, nil
	}
	refs, err := g.personalMemoryPromptSourceReferences(r, a.name, "", "", "", "")
	if err != nil {
		return nil, err
	}
	result := make([]memorycore.RecallItem, 0, len(refs))
	for _, ref := range refs {
		switch ref.SourceType {
		case "user_statement", "user_feedback", "task_execution", "task_insight", "learning_knowledge":
		case "knowledge_fact":
			if ref.SessionID != sessionID || strings.TrimSpace(ref.Evidence) == "" {
				continue
			}
		default:
			continue
		}
		if item, ok := a.webMemoryReferenceItem(bounded, audience, ref); ok {
			result = append(result, item)
		}
	}
	return result, nil
}

// The management inventory shares the exact representation and original-row
// validation with runtime recall; listing never grants cross-user recall.
func (a *LLMAgent) webMemoryReferenceItem(ctx context.Context, audience *webMemoryAudience, ref memorycore.WorkingReference) (memorycore.RecallItem, bool) {
	text := ref.Key + "\n" + ref.Value
	if len(ref.Tools) > 0 {
		names := make([]string, 0, len(ref.Tools))
		for _, tool := range ref.Tools {
			names = append(names, tool.Name)
		}
		text = "原记录工具调用：" + strings.Join(names, ", ") + "\n" + text
	}
	content := memorySourceText("assistant", text, 1400)
	if content == "" {
		return memorycore.RecallItem{}, false
	}
	locator := a.webMemoryPromptLocator(ctx, audience, ref)
	if memorycore.SourceRecallKey(locator) == "" {
		// A source changed or lost its evidence after the initial listing. Do
		// not turn failed original-row verification into an unaddressed chunk
		// that could bypass that record's saved automatic-recall setting.
		return memorycore.RecallItem{}, false
	}
	return memorycore.RecallItem{ID: ref.ID, Source: ref.SourceType, Rank: 50,
		Content:         "历史参考（不覆盖当前要求，生成的经验需要核对）：\n" + content,
		SourceSessionID: ref.SessionID, Locator: locator}, true
}

// References contain bounded/sanitized excerpts; never hash them as though
// they were the underlying editable field. Re-read the exact already-verified
// row, require the current owner and representation, and capture its raw digest.
// A concurrent change that no longer yields this excerpt fails attribution.
func (a *LLMAgent) webMemoryPromptLocator(ctx context.Context, audience *webMemoryAudience, ref memorycore.WorkingReference) memorycore.PromptLocator {
	if audience == nil || audience.gateway == nil {
		return memorycore.PromptLocator{}
	}
	g := audience.gateway
	id, err := strconv.ParseUint(ref.SourceID, 10, 64)
	if err != nil || id == 0 {
		return memorycore.PromptLocator{}
	}
	locator := memorycore.PromptLocator{ID: ref.SourceID, SessionID: ref.SessionID, Key: ref.Key}
	switch ref.SourceType {
	case "user_statement", "user_feedback":
		var message ChatMessage
		query := scopeRowsToPersistedOwner(g.db.WithContext(ctx).Model(&ChatMessage{}), defaultExecutionTenantID, "").
			Where("id = ? AND session_id = ? AND user_id = ? AND role = ?", id, ref.SessionID, audience.userID, "user").
			Where("agent_name = ? OR agent_name = ''", a.name).
			Where("media_owner = '' OR media_owner IS NULL OR media_owner = ?", audience.mediaOwner)
		if query.First(&message).Error != nil || len(message.Content) > 1<<20 || memorySourceText("user", message.Content, 1200) != ref.Value {
			return memorycore.PromptLocator{}
		}
		locator.Kind, locator.Field, locator.Digest = "chat_message", "content", promptMemoryDigest(message.Content)
	case "task_execution", "task_insight":
		var event memorycore.MemoryEvent
		if g.db.WithContext(ctx).Where("id = ? AND tenant_id = ? AND agent_name = ? AND session_id = ? AND user_id = ?", id, defaultExecutionTenantID, a.name, ref.SessionID, audience.userID).First(&event).Error != nil ||
			(event.UserChannel != "" && event.UserChannel != "web") || (event.Status != "success" && event.Status != "failed") {
			return memorycore.PromptLocator{}
		}
		raw, represented := event.FinalAnswer, memorySourceText("assistant", event.FinalAnswer, 1200)
		locator.Field = "final_answer"
		if ref.SourceType == "task_insight" {
			raw, represented = event.LearnNote, memorySourceText("assistant", event.LearnNote, 1200)
			locator.Field = "learn_note"
			if represented == "" && len(event.Insights) <= 64<<10 {
				var values []string
				if json.Unmarshal(event.Insights, &values) == nil {
					raw, represented = string(event.Insights), memorySourceText("assistant", strings.Join(values, "\n"), 1200)
					locator.Field = "insights"
				}
			}
		}
		if len(raw) > 1<<20 || represented == "" || represented != ref.Value {
			return memorycore.PromptLocator{}
		}
		locator.Kind, locator.Digest = "memory_event", promptMemoryDigest(raw)
	case "learning_knowledge":
		var row memorycore.Knowledge
		if g.db.WithContext(ctx).Where("id = ? AND tenant_id = ? AND agent_name = ?", id, defaultExecutionTenantID, a.name).First(&row).Error != nil {
			return memorycore.PromptLocator{}
		}
		var event memorycore.MemoryEvent
		if g.db.WithContext(ctx).Where("id = ? AND tenant_id = ? AND agent_name = ? AND session_id = ? AND user_id = ?", row.SourceID, defaultExecutionTenantID, a.name, ref.SessionID, audience.userID).First(&event).Error != nil ||
			(event.UserChannel != "" && event.UserChannel != "web") || (event.Status != "success" && event.Status != "failed") {
			return memorycore.PromptLocator{}
		}
		raw, represented := row.Summary, memorySourceText("assistant", row.Summary, 1200)
		locator.Field = "summary"
		if represented == "" {
			raw, represented = row.Content, memorySourceText("assistant", row.Content, 1200)
			locator.Field = "content"
		}
		if len(raw) > 1<<20 || represented == "" || represented != ref.Value {
			return memorycore.PromptLocator{}
		}
		locator.Kind, locator.Digest = "knowledge", promptMemoryDigest(raw)
	case "knowledge_fact":
		if globalKG == nil || !globalKG.BelongsToWorkspace(audience.workspace) || ref.SessionID != audience.sessionID {
			return memorycore.PromptLocator{}
		}
		facts, err := globalKG.ConversationFactSources(ctx, []string{ref.SessionID}, 100)
		if err != nil {
			return memorycore.PromptLocator{}
		}
		for _, fact := range facts {
			if fact.Fact.ID != int64(id) || memorySourceText("assistant", fact.Fact.Subject+" · "+fact.Fact.Predicate+" · "+fact.Fact.Object, 1200) != ref.Value || memorySourceText("user", fact.Evidence, 1200) != ref.Evidence {
				continue
			}
			locator.Kind, locator.Field, locator.Digest = "knowledge_fact", "object", promptMemoryDigest(fact.Fact.Object)
			return locator
		}
		return memorycore.PromptLocator{}
	default:
		return memorycore.PromptLocator{}
	}
	return locator
}
