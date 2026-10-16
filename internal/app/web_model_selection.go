package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// A Web choice is an in-memory execution contract, never a settings mutation
// or durable credential record. Keep every field private so even an accidental
// JSON encoding of this value cannot expose model credentials or role prompts.
type webModelSelection struct {
	alias          string
	model          ModelConfig
	agentName      string
	agent          *AgentConfig
	timeoutSeconds int
}

type webModelSelectionContextKey struct{}

func withWebModelSelection(ctx context.Context, selection *webModelSelection) context.Context {
	if selection == nil {
		return ctx
	}
	return context.WithValue(ctx, webModelSelectionContextKey{}, selection)
}

func webModelSelectionFromContext(ctx context.Context) *webModelSelection {
	if ctx == nil {
		return nil
	}
	selection, _ := ctx.Value(webModelSelectionContextKey{}).(*webModelSelection)
	return selection
}

func (g *GatewayService) captureWebModelSelection(r *http.Request, request *MessageRequest) (*webModelSelection, int, error) {
	if request == nil || strings.TrimSpace(request.ModelConfig) == "" {
		return nil, 0, nil
	}
	if !personalMemoryLocalRequest(r) {
		return nil, http.StatusForbidden, errors.New("web_model_selection_local_only")
	}
	if identity, ok := executionIdentityFromContext(r.Context()); ok && (identity.Service || identity.TenantScoped || identity.Source == "agent_os") {
		return nil, http.StatusForbidden, errors.New("web_model_selection_not_allowed_for_agentos")
	}
	if strings.HasPrefix(r.URL.Path, "/api/v1/") || request.TenantID != "" || request.SubjectID != "" || request.RoleID != "" {
		return nil, http.StatusForbidden, errors.New("web_model_selection_not_allowed_for_agentos")
	}
	if request.Channel != "" && request.Channel != "web" {
		return nil, http.StatusForbidden, errors.New("web_model_selection_local_only")
	}
	switch request.TaskType {
	case "", "chat", "qa", "skill":
	default:
		return nil, http.StatusBadRequest, errors.New("web_model_selection_unsupported_task")
	}
	if request.Collaboration != nil || request.CollaborationType != "" {
		return nil, http.StatusBadRequest, errors.New("web_model_selection_unsupported_route")
	}
	message := strings.TrimSpace(request.Message)
	if request.TaskType != "qa" && request.SkillRun == nil && (strings.HasPrefix(message, "/pipeline ") || strings.HasPrefix(message, "/run ")) {
		return nil, http.StatusBadRequest, errors.New("工作流使用各步骤的模型配置；请通过工作流入口启动")
	}

	g.configWriteMu.Lock()
	defer g.configWriteMu.Unlock()
	config, err := LoadConfig(g.configpath)
	if err != nil {
		return nil, http.StatusInternalServerError, errors.New("invalid_config")
	}
	alias := strings.TrimSpace(request.ModelConfig)
	model, ok := config.ModelConfig[alias]
	if !ok || !isModelConfigAvailable(model) {
		return nil, http.StatusBadRequest, errors.New("web_model_selection_unavailable")
	}
	selection := &webModelSelection{alias: alias, model: model, timeoutSeconds: config.Runtime.LLMCallTimeoutSeconds}
	if request.TaskType != "qa" {
		var role AgentConfig
		var found bool
		if request.AgentName == "" {
			role, found = preferredConfiguredAgent(config)
		} else {
			for _, configured := range config.Agents {
				if configured.Name == request.AgentName {
					role, found = configured, true
					break
				}
			}
		}
		if !found {
			return nil, http.StatusBadRequest, errors.New("web_model_selection_agent_unavailable")
		}
		cloned, err := cloneAgentConfigForRevision(role)
		if err != nil {
			return nil, http.StatusInternalServerError, errors.New("web_model_selection_snapshot_failed")
		}
		cloned.Model = alias
		selection.agent, selection.agentName = cloned, role.Name
		request.AgentName = role.Name
	}
	request.ModelConfig = alias
	return selection, 0, nil
}

// Web snapshots intentionally do not survive process restart. Never silently
// substitute a newly configured model for a recovered task's original choice.
func validateWebModelTaskSnapshot(task *GatewayTask) error {
	if task == nil || task.Request == nil || task.Request.ModelConfig == "" {
		return nil
	}
	if task.QueueName == agentOSQueueName || task.Request.Channel != "web" {
		return errors.New("web_model_selection_not_allowed_for_agentos_or_channel")
	}
	if task.webModelSelection == nil || task.webModelSelection.alias != task.Request.ModelConfig {
		return errors.New("本次网页模型选择已失效，请重新提交任务")
	}
	return nil
}

// Both immutable AgentOS revisions and private Web snapshots use the existing
// ephemeral factory and runtime-tool registration, rather than cloning tools
// or modifying cached Agents. AgentOS retains its stricter personal boundary.
func (g *GatewayService) newAgentFromTaskModel(agentName string, agentConfig AgentConfig, model ModelConfig, workspace string, runtimeConfig *Config, personal bool) (string, Agent, bool, func(), error) {
	if g == nil || g.engine == nil || g.engine.agentFactory == nil {
		return "", nil, false, nil, fmt.Errorf("agent factory is not initialized")
	}
	parentFactory := g.engine.agentFactory
	revisionFactory := &AgentFactory{
		modelConfigs: map[string]*ModelConfig{agentConfig.Model: &model},
		callback:     parentFactory.callback, workspace: workspace,
		globalConfig: runtimeConfig, fileLogger: parentFactory.fileLogger,
		agents: make(map[string]Agent), subAgents: make(map[string]Agent),
	}
	cloned, err := cloneAgentConfigForRevision(agentConfig)
	if err != nil {
		return "", nil, false, nil, err
	}
	agent, err := revisionFactory.CreateEphemeralAgentInWorkspace(*cloned, workspace)
	if err != nil {
		return agentName, nil, true, nil, err
	}
	if llmAgent, ok := agent.(*LLMAgent); ok && personal {
		llmAgent.markTaskScopedRuntime()
	}
	g.engine.registerRuntimeToolsForAgentWithWorkspace(agent, workspace, g.engine.fileLogger)
	return agentName, agent, true, newAgentRuntimeCleanup(agent), nil
}
