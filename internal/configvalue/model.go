package configvalue

import (
	"fmt"
	"strings"
)

// Model contains the provider fields required for configuration validation.
type Model struct {
	APIKey         string
	BaseURL        string
	ModelName      string
	APIKeyOptional bool
}

// ValidateModel checks for empty fields, placeholder credentials, and an
// invalid provider URL.
func ValidateModel(name string, cfg *Model) error {
	if cfg == nil {
		return fmt.Errorf("模型 '%s' 未配置。请在 openkitty-config.yaml 的 model_cfg 中添加配置", name)
	}

	apiKey := strings.TrimSpace(cfg.APIKey)
	baseURL := strings.TrimSpace(cfg.BaseURL)
	modelName := strings.TrimSpace(cfg.ModelName)
	if apiKey == "" && !cfg.APIKeyOptional {
		return fmt.Errorf("模型 '%s' 的 api_key 为空。请填入你的 API Key", name)
	}
	if baseURL == "" {
		return fmt.Errorf("模型 '%s' 的 base_url 为空", name)
	}
	if modelName == "" {
		return fmt.Errorf("模型 '%s' 的 model_name 为空", name)
	}

	if !cfg.APIKeyOptional {
		lower := strings.ToLower(apiKey)
		for _, pattern := range []string{
			"your-api-key", "your_api_key", "sk-your", "sk-xxx",
			"api-key-here", "your-key", "your_key", "placeholder",
			"xxxxx", "change-me", "change_me", "todo",
		} {
			if strings.Contains(lower, pattern) {
				return fmt.Errorf("模型 '%s' 的 api_key 看起来是占位符（'%s'）。请填入真实的 API Key", name, truncateModelValue(apiKey, 20))
			}
		}
		if len(apiKey) < 16 {
			return fmt.Errorf("模型 '%s' 的 api_key 太短（%d 字符），不像是有效的 Key", name, len(apiKey))
		}
	}
	if !strings.HasPrefix(baseURL, "http") {
		return fmt.Errorf("模型 '%s' 的 base_url 格式错误，应以 http:// 或 https:// 开头", name)
	}
	return nil
}

// ModelConfigHelp returns user-friendly model configuration instructions.
func ModelConfigHelp() string {
	return `📋 模型配置指南：
1. 打开 openkitty-config.yaml 文件
2. 在 model_cfg 下找到或添加模型配置：
   model_cfg:
     my_model:
       type: deepseek
       api_key: sk-xxxxxxxxxxxxx    ← 填入你的 API Key
       model_name: deepseek-v4-pro
       base_url: https://api.deepseek.com/v1

3. 在 agents 下指定使用的模型：
   agents:
     - name: my-agent
       model: my_model    ← 模型名称要和 model_cfg 里的 key 一致

常用模型供应商：
• DeepSeek: https://platform.deepseek.com → API Keys → 复制 key
• 豆包（火山引擎）: https://console.volcengine.com/ark → 创建 API Key
• 通义千问: https://dashscope.console.aliyun.com → API-KEY 管理`
}

func truncateModelValue(value string, max int) string {
	if len(value) <= max {
		return value
	}
	return value[:max] + "..."
}
