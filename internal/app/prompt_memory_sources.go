package app

import (
	"context"
	"errors"
	"net/http"
	"os"
	"strconv"
	"time"

	memorycore "mac/internal/memory"
	"mac/internal/projectctx"
)

// Sources describe existing read paths, not an assertion that a request has
// already been submitted or that every relevant candidate will be selected.
type personalPromptHistorySource struct {
	memorycore.PromptSource
	Mode      string                          `json:"mode"`
	Injection string                          `json:"injection"`
	CodePath  string                          `json:"code_path"`
	Role      string                          `json:"role,omitempty"`
	CreatedAt *time.Time                      `json:"created_at,omitempty"`
	UpdatedAt time.Time                       `json:"updated_at,omitempty"`
	Recall    *memorycore.SourceRecallSetting `json:"recall,omitempty"`
}

type personalPromptProvider struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Mode        string `json:"mode"`
	CodePath    string `json:"code_path"`
	Description string `json:"description"`
	Count       int    `json:"count"`
	Available   bool   `json:"available"`
}

func personalPromptExcerpt(value string) string {
	if runes := []rune(value); len(runes) > 1200 {
		return string(runes[:1200])
	}
	return value
}

// Management is a local inventory across authorized source conversations, not
// the audience of one pending model request. Every original row retains its
// own conversation and user, and graph facts remain in the session-only path.
func (g *GatewayService) personalPromptManagementReferences(r *http.Request, agent string, sessions []personalMemorySession) ([]personalPromptHistorySource, error) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	r = r.WithContext(ctx)
	refs, err := g.personalMemoryPromptSourceReferences(r, agent, "", "", "", "")
	if err != nil {
		return nil, err
	}
	reader := &LLMAgent{name: agent, workspace: g.currentProjectWorkspace(), hasMemory: true}
	audiences := make(map[string]*webMemoryAudience)
	sources := make([]personalPromptHistorySource, 0, len(refs))
	for _, ref := range refs {
		switch ref.SourceType {
		case "user_statement", "user_feedback", "task_execution", "task_insight", "learning_knowledge":
		default:
			continue
		}
		if !promptInputLegalSession(sessions, ref.SessionID) {
			continue
		}
		audience, found := audiences[ref.SessionID]
		if !found {
			var original ChatSession
			if err := g.personalMemorySessionQuery(r, agent).Where("id = ?", ref.SessionID).First(&original).Error; err != nil {
				continue
			}
			if !projectctx.SameWorkspace(original.Workspace, reader.workspace) {
				continue
			}
			audience = g.captureWebMemoryAudience(r, &MessageRequest{SessionId: original.ID, UserId: original.UserId}, reader.workspace)
			audiences[ref.SessionID] = audience
		}
		item, ok := reader.webMemoryReferenceItem(ctx, audience, ref)
		if !ok || item.Locator.Kind == "" {
			continue
		}
		source := promptMemoryRecallSource(item)
		source.Excerpt = personalPromptExcerpt(source.Excerpt)
		sources = append(sources, personalPromptHistorySource{PromptSource: source, Mode: "relevant", Injection: "memory_prefix",
			CodePath: "Generate → buildMemoryPrompt → webMemorySources → Service.Recall", CreatedAt: ref.CreatedAt, UpdatedAt: ref.UpdatedAt})
	}
	return sources, ctx.Err()
}

// personalPromptSources reverses the actual ordinary Web prompt read paths.
// It never constructs an Agent/SCM, runs Recall/embedding, imports old stores,
// schedules processing, writes a trace/receipt or calls a model. The caller's
// list is a limit, not permission: the exact current session is revalidated.
func (g *GatewayService) personalPromptSources(r *http.Request, agent string, owner memorycore.Owner, sessions []personalMemorySession, currentSession string) ([]personalPromptHistorySource, []personalPromptProvider, error) {
	sources := make([]personalPromptHistorySource, 0)
	// Keep the trusted local control boundary independent from all serialized
	// client identities or model-supplied source locators.
	if g == nil || !personalMemoryLocalRequest(r) || owner.TenantID != defaultExecutionTenantID || owner.PartitionID != defaultExecutionTenantID || owner.SubjectID != "" {
		return nil, nil, errors.New("historical prompt sources require the ordinary local Web owner")
	}
	if identity, ok := executionIdentityFromContext(r.Context()); ok && (identity.Service || identity.TenantScoped || identity.Source == "agent_os") {
		return nil, nil, errors.New("AgentOS sources are not ordinary Web sources")
	}
	if resolved, _, err := g.personalMemoryAgent(agent); err != nil || resolved != agent {
		return nil, nil, errors.New("unknown prompt source agent")
	}
	workspace := g.currentProjectWorkspace()
	providers := []personalPromptProvider{
		{ID: "session_history", Label: "会话历史消息", Mode: "direct", CodePath: "gateway.processTaskWithSession → buildGatewaySessionHistory → Generate.history_messages", Description: "当前合法会话最近50条用户/助手消息，清理后直接作为历史消息；后续压缩可能缩减。", Available: currentSession != ""},
		{ID: "web_history", Label: "已存任务结果和经验", Mode: "relevant", CodePath: "Generate → buildMemoryPrompt → webMemorySources → Service.Recall", Description: "核实归属后的任务结果、经验、明确原话及关联学习知识，按问题、撤回规则、预算和安全筛选。", Available: g.personalPromptMemoryEnabled(agent) && currentSession != ""},
		{ID: "project_instructions", Label: "项目说明", Mode: "direct", CodePath: "Generate → buildSystemPromptDynamicForContext → ProjectInstructionLoader.Load", Description: "实际说明加载器解析到的项目/全局文件，进入动态系统消息；全局外部文件保持只读。", Available: true},
	}

	var current ChatSession
	if currentSession != "" {
		if !promptInputLegalSession(sessions, currentSession) || g.personalMemorySessionQuery(r, agent).Where("id = ?", currentSession).First(&current).Error != nil || !projectctx.SameWorkspace(current.Workspace, workspace) {
			return nil, nil, errors.New("prompt source conversation is unavailable")
		}
		var rows []ChatMessage
		query := scopeRowsToPersistedOwner(g.db.WithContext(r.Context()).Model(&ChatMessage{}), defaultExecutionTenantID, "").
			Where("session_id = ? AND role IN ?", currentSession, []string{"user", "assistant"}).
			Where("agent_name = ? OR agent_name = ''", agent).
			Where("user_id = ? OR user_id = ''", current.UserId).
			Where("media_owner = '' OR media_owner IS NULL OR media_owner = ?", g.personalMemoryMediaOwner(r))
		if err := query.Order("created_at DESC,id DESC").Limit(50).Find(&rows).Error; err != nil {
			return nil, nil, err
		}
		for i, j := 0, len(rows)-1; i < j; i, j = i+1, j-1 {
			rows[i], rows[j] = rows[j], rows[i]
		}
		metadata := make(map[string]ChatMessage, len(rows))
		for _, row := range rows {
			metadata[strconv.FormatUint(uint64(row.ID), 10)] = row
		}
		for _, candidate := range buildGatewaySessionHistory(rows).Sources {
			source := candidate.source
			source.Excerpt = personalPromptExcerpt(source.Excerpt)
			row := metadata[source.Locator.ID]
			sources = append(sources, personalPromptHistorySource{PromptSource: source, Mode: "direct", Injection: "history_messages", Role: candidate.role,
				CodePath: providers[0].CodePath, CreatedAt: sourceTime(row.CreatedAt), UpdatedAt: row.CreatedAt})
			providers[0].Count++
		}

		if providers[1].Available {
			audience := g.captureWebMemoryAudience(r, &MessageRequest{SessionId: currentSession, UserId: current.UserId}, workspace)
			ctx := context.WithValue(r.Context(), webMemoryAudienceKey{}, audience)
			ctx = context.WithValue(ctx, "channel", "web")
			reader := &LLMAgent{name: agent, workspace: workspace, hasMemory: true}
			items, err := reader.webMemorySources(ctx, currentSession)
			if err != nil {
				return nil, nil, err
			}
			// Only locatable representations survive. This is a current source
			// inventory, not a saved receipt or an unowned archival aggregate.
			refs, err := g.personalMemoryPromptSourceReferences(r.WithContext(ctx), agent, "", "", "", "")
			if err != nil {
				return nil, nil, err
			}
			metadata := make(map[string]memorycore.WorkingReference, len(refs))
			for _, ref := range refs {
				metadata[ref.ID] = ref
			}
			for _, item := range items {
				if item.Locator.Kind == "" || !promptInputLegalSession(sessions, item.SourceSessionID) {
					continue
				}
				source := promptMemoryRecallSource(item)
				source.Excerpt = personalPromptExcerpt(source.Excerpt)
				ref := metadata[item.ID]
				sources = append(sources, personalPromptHistorySource{PromptSource: source, Mode: "relevant", Injection: "memory_prefix", CodePath: providers[1].CodePath,
					CreatedAt: ref.CreatedAt, UpdatedAt: ref.UpdatedAt})
				providers[1].Count++
			}
		}
	}

	loader := projectctx.NewProjectInstructionLoader(OpenKittyHome())
	if runtime := g.personalMemoryCatalogRuntime(agent); runtime != nil && runtime.projectInstructions != nil {
		loader = runtime.projectInstructions
	}
	instructions, err := loader.Load(workspace, workspace)
	if err != nil {
		return nil, nil, err
	}
	for _, candidate := range promptMemoryInstructionCandidates(instructions, instructions.Prompt()) {
		source := candidate.source
		source.Excerpt = personalPromptExcerpt(source.Excerpt)
		modified := time.Time{}
		if info, err := os.Stat(source.Locator.Path); err == nil {
			modified = info.ModTime()
		}
		sources = append(sources, personalPromptHistorySource{PromptSource: source, Mode: "direct", Injection: "system", Role: "system", CodePath: providers[2].CodePath, UpdatedAt: modified})
		providers[2].Count++
	}
	return sources, providers, r.Context().Err()
}
