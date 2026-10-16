package app

import (
	"fmt"
	"net/http"

	authcore "mac/internal/auth"
)

// Capture only the submitting browser's session. Other channels and recovered
// tasks must use their own configured credentials, never the last Web login.
func (g *GatewayService) captureWebPlatformAccount(r *http.Request, request *MessageRequest, selections ...*webModelSelection) (*authcore.PlatformAccountSession, error) {
	var account *authcore.PlatformAccountSession
	if g.accountHandler != nil && personalMemoryLocalRequest(r) {
		account, _ = g.accountHandler.CapturePlatformAccount(r)
	}
	var selection *webModelSelection
	if len(selections) > 0 {
		selection = selections[0]
	}
	if request.ModelConfig != "" && selection == nil {
		var err error
		selection, _, err = g.captureWebModelSelection(r, request)
		if err != nil {
			return nil, err
		}
	}
	if selection != nil {
		if modelConfigUsesAccount(selection.model) && account == nil {
			return nil, fmt.Errorf("请先登录平台账号，再使用平台模型；也可以选择自己的模型")
		}
		return account, nil
	}
	config, err := LoadConfig(g.configpath)
	if err != nil {
		return account, nil
	}
	modelName := selectedModelKey(config)
	if request.TaskType != "qa" {
		agentName := request.AgentName
		if agentName == "" {
			agentName = config.DefaultAgent
		}
		for _, agent := range config.Agents {
			if agent.Name == agentName {
				modelName = effectiveAgentModelKey(config, agent)
				break
			}
		}
	}
	if model, ok := config.ModelConfig[modelName]; ok && modelConfigUsesAccount(model) && account == nil {
		return nil, fmt.Errorf("请先登录平台账号，再使用平台模型；也可以选择自己的模型")
	}
	return account, nil
}
