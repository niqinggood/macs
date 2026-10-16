package app

import (
	"context"
	"errors"
	"net/http"
	"os"
	"strings"
	"time"

	"mac/internal/modelruntime"
)

// Simple chat shares conversation storage and cancellation with Web chat, but
// never creates an Agent, activates Skills or enters the model/tool loop.
func (g *GatewayService) simpleChat(ctx context.Context, input string, history []map[string]interface{}) (string, error) {
	var model ModelConfig
	var timeoutSeconds int
	if selection := webModelSelectionFromContext(ctx); selection != nil {
		model = selection.model
		timeoutSeconds = selection.timeoutSeconds
	} else {
		config, err := LoadConfig(g.configpath)
		if err != nil {
			return "", errors.New("无法读取模型配置")
		}
		var exists bool
		model, exists = config.ModelConfig[selectedModelKey(config)]
		if !exists || !isModelConfigAvailable(model) {
			return "", errors.New("请先选择并配置对话模型")
		}
		if model.APIKey == "" && model.APIKeyEnv != "" {
			model.APIKey = os.Getenv(model.APIKeyEnv)
		}
		timeoutSeconds = config.Runtime.LLMCallTimeoutSeconds
	}
	messages := []map[string]interface{}{{"role": "system", "content": "你是 macs 的问答助手。直接回答用户的问题。你没有工具访问权限；不要声称执行了命令、修改了文件或调用了外部应用。"}}
	for _, item := range history {
		role, _ := item["role"].(string)
		content, _ := item["content"].(string)
		if (role == "user" || role == "assistant") && strings.TrimSpace(content) != "" {
			messages = append(messages, map[string]interface{}{"role": role, "content": content})
		}
	}
	messages = append(messages, map[string]interface{}{"role": "user", "content": input})
	policy := modelruntime.ResolveCallPolicy(timeoutSeconds, 1)
	if policy.AttemptTimeout > 120*time.Second {
		policy.AttemptTimeout = 120 * time.Second
	}
	client := &http.Client{Timeout: policy.AttemptTimeout, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	return modelruntime.CallMessages(ctx, client, completionConfig(&model), messages, model.MaxTokens, 0.3, policy)
}
