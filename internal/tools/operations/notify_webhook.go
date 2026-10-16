package operations

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// WebhookNotifier sends notifications to external services.
type WebhookNotifier struct {
	webhooks map[string]WebhookConfig
	client   *http.Client
	mu       sync.RWMutex
}

// WebhookConfig defines a notification endpoint.
type WebhookConfig struct {
	Name    string `json:"name" yaml:"name"`
	URL     string `json:"url" yaml:"url"`
	Type    string `json:"type" yaml:"type"` // "generic", "feishu", "dingtalk", "weixin"
	Enabled bool   `json:"enabled" yaml:"enabled"`
}

// Notification is a message to be sent.
type Notification struct {
	Title   string `json:"title"`
	Content string `json:"content"`
	Level   string `json:"level"`  // "info", "warn", "error", "success"
	Source  string `json:"source"` // "cron", "code_review", "test_runner", "system"
	Time    string `json:"time"`
}

func NewWebhookNotifier() *WebhookNotifier {
	return &WebhookNotifier{
		webhooks: make(map[string]WebhookConfig),
		client:   &http.Client{Timeout: 10 * time.Second},
	}
}

func (wn *WebhookNotifier) Register(cfg WebhookConfig) {
	wn.mu.Lock()
	defer wn.mu.Unlock()
	if cfg.Enabled {
		wn.webhooks[cfg.Name] = cfg
	}
}

func (wn *WebhookNotifier) Remove(name string) {
	wn.mu.Lock()
	defer wn.mu.Unlock()
	delete(wn.webhooks, name)
}

func (wn *WebhookNotifier) List() []WebhookConfig {
	wn.mu.RLock()
	defer wn.mu.RUnlock()
	var list []WebhookConfig
	for _, cfg := range wn.webhooks {
		list = append(list, cfg)
	}
	return list
}

// Send sends a notification to all registered webhooks.
func (wn *WebhookNotifier) Send(notif Notification) []string {
	wn.mu.RLock()
	defer wn.mu.RUnlock()

	if notif.Time == "" {
		notif.Time = time.Now().Format("2006-01-02 15:04:05")
	}

	var results []string
	for name, cfg := range wn.webhooks {
		err := wn.sendToWebhook(cfg, notif)
		if err != nil {
			results = append(results, fmt.Sprintf("%s: failed - %v", name, err))
		} else {
			results = append(results, fmt.Sprintf("%s: sent", name))
		}
	}
	return results
}

func (wn *WebhookNotifier) sendToWebhook(cfg WebhookConfig, notif Notification) error {
	var body []byte

	switch cfg.Type {
	case "feishu":
		body = wn.BuildFeishuPayload(notif)
	case "dingtalk":
		body = wn.BuildDingTalkPayload(notif)
	default:
		body = wn.BuildGenericPayload(notif)
	}

	resp, err := wn.client.Post(cfg.URL, "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(respBody))
	}
	return nil
}

func (wn *WebhookNotifier) BuildGenericPayload(notif Notification) []byte {
	data, _ := json.Marshal(map[string]interface{}{
		"title":     notif.Title,
		"content":   notif.Content,
		"level":     notif.Level,
		"source":    notif.Source,
		"timestamp": notif.Time,
	})
	return data
}

func (wn *WebhookNotifier) BuildFeishuPayload(notif Notification) []byte {
	levelColor := map[string]string{
		"info": "blue", "warn": "yellow", "error": "red", "success": "green",
	}
	color := levelColor[notif.Level]
	if color == "" {
		color = "blue"
	}

	data, _ := json.Marshal(map[string]interface{}{
		"msg_type": "interactive",
		"card": map[string]interface{}{
			"header": map[string]interface{}{
				"title":    map[string]interface{}{"content": notif.Title, "tag": "plain_text"},
				"template": color,
			},
			"elements": []map[string]interface{}{
				{"tag": "div", "text": map[string]interface{}{"content": notif.Content, "tag": "lark_md"}},
				{"tag": "hr"},
				{"tag": "note", "elements": []map[string]interface{}{
					{"tag": "plain_text", "content": fmt.Sprintf("[%s] %s | %s", notif.Level, notif.Source, notif.Time)},
				}},
			},
		},
	})
	return data
}

func (wn *WebhookNotifier) BuildDingTalkPayload(notif Notification) []byte {
	title := fmt.Sprintf("[%s] %s", strings.ToUpper(notif.Level), notif.Title)
	data, _ := json.Marshal(map[string]interface{}{
		"msgtype": "markdown",
		"markdown": map[string]interface{}{
			"title": title,
			"text":  fmt.Sprintf("## %s\n\n%s\n\n---\n*%s | %s*", notif.Title, notif.Content, notif.Source, notif.Time),
		},
	})
	return data
}

// ==================== notify tool ====================

type NotifyTool struct {
	notifier *WebhookNotifier
}

func NewNotifyTool(notifier *WebhookNotifier) *NotifyTool {
	return &NotifyTool{notifier: notifier}
}

func (t *NotifyTool) Name() string { return "notify" }

func (t *NotifyTool) Description() string {
	return "Send notifications to configured webhooks (Feishu/DingTalk/generic). " +
		"Use this to alert users about task completion, errors, or important findings. " +
		"Also supports listing and managing webhook configurations."
}

func (t *NotifyTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"action": map[string]interface{}{
				"type":        "string",
				"description": "Action: 'send' (send notification), 'webhooks' (list configured), 'add_webhook' (configure new), 'remove_webhook'",
				"enum":        []string{"send", "webhooks", "add_webhook", "remove_webhook"},
			},
			"title": map[string]interface{}{
				"type":        "string",
				"description": "Notification title (for send action)",
			},
			"content": map[string]interface{}{
				"type":        "string",
				"description": "Notification content/message (for send action)",
			},
			"level": map[string]interface{}{
				"type":        "string",
				"description": "Severity level: info, warn, error, success",
				"enum":        []string{"info", "warn", "error", "success"},
			},
			"source": map[string]interface{}{
				"type":        "string",
				"description": "Source system: cron, code_review, test_runner, system",
			},
			"webhook_name": map[string]interface{}{
				"type":        "string",
				"description": "Webhook name (for add_webhook/remove_webhook)",
			},
			"webhook_url": map[string]interface{}{
				"type":        "string",
				"description": "Webhook URL (for add_webhook)",
			},
			"webhook_type": map[string]interface{}{
				"type":        "string",
				"description": "Webhook type: generic, feishu, dingtalk",
				"enum":        []string{"generic", "feishu", "dingtalk"},
			},
		},
		"required": []string{"action"},
	}
}

func (t *NotifyTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	action := getString(params, "action", "webhooks")

	if t.notifier == nil {
		return "Notification system not available", nil
	}

	switch action {
	case "send":
		title := getString(params, "title", "")
		content := getString(params, "content", "")
		if title == "" || content == "" {
			return "Error: title and content are required", nil
		}
		notif := Notification{
			Title:   title,
			Content: content,
			Level:   getString(params, "level", "info"),
			Source:  getString(params, "source", "system"),
		}
		results := t.notifier.Send(notif)
		return fmt.Sprintf("Notification sent:\n%s", strings.Join(results, "\n")), nil

	case "webhooks":
		webhooks := t.notifier.List()
		if len(webhooks) == 0 {
			return "No webhooks configured. Use 'add_webhook' to configure one.", nil
		}
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("Webhooks (%d):\n", len(webhooks)))
		for _, wh := range webhooks {
			status := "active"
			if !wh.Enabled {
				status = "disabled"
			}
			sb.WriteString(fmt.Sprintf("- **%s** (%s) [%s]: %s\n", wh.Name, wh.Type, status, wh.URL))
		}
		return sb.String(), nil

	case "add_webhook":
		name := getString(params, "webhook_name", "")
		url := getString(params, "webhook_url", "")
		whType := getString(params, "webhook_type", "generic")
		if name == "" || url == "" {
			return "Error: webhook_name and webhook_url are required", nil
		}
		t.notifier.Register(WebhookConfig{Name: name, URL: url, Type: whType, Enabled: true})
		return fmt.Sprintf("Webhook '%s' (%s) registered", name, whType), nil

	case "remove_webhook":
		name := getString(params, "webhook_name", "")
		if name == "" {
			return "Error: webhook_name is required", nil
		}
		t.notifier.Remove(name)
		return fmt.Sprintf("Webhook '%s' removed", name), nil
	}

	return fmt.Sprintf("Unknown action: %s", action), nil
}

func getString(values map[string]interface{}, key, fallback string) string {
	if value, ok := values[key].(string); ok && strings.TrimSpace(value) != "" {
		return value
	}
	return fallback
}
