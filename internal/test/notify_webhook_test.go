package app

import (
	"strings"
	"testing"

	operationstools "mac/internal/tools/operations"
)

func TestWebhookNotifierRegister(t *testing.T) {
	wn := operationstools.NewWebhookNotifier()
	wn.Register(operationstools.WebhookConfig{Name: "test", URL: "http://example.com/hook", Type: "generic", Enabled: true})

	list := wn.List()
	if len(list) != 1 {
		t.Errorf("expected 1 webhook, got %d", len(list))
	}
	if list[0].Name != "test" || list[0].Type != "generic" {
		t.Error("webhook config mismatch")
	}
}

func TestWebhookNotifierRemove(t *testing.T) {
	wn := operationstools.NewWebhookNotifier()
	wn.Register(operationstools.WebhookConfig{Name: "test", URL: "http://example.com/hook", Type: "generic", Enabled: true})
	wn.Remove("test")

	if len(wn.List()) != 0 {
		t.Error("should be empty after remove")
	}
}

func TestWebhookNotifierListEmpty(t *testing.T) {
	wn := operationstools.NewWebhookNotifier()
	list := wn.List()
	if len(list) != 0 {
		t.Error("should be empty initially")
	}
}

func TestWebhookNotifierSendNoHooks(t *testing.T) {
	wn := operationstools.NewWebhookNotifier()
	results := wn.Send(operationstools.Notification{Title: "test", Content: "hello", Level: "info"})
	if len(results) != 0 {
		t.Error("no results expected when no webhooks registered")
	}
}

func TestWebhookNotifierDisabled(t *testing.T) {
	wn := operationstools.NewWebhookNotifier()
	wn.Register(operationstools.WebhookConfig{Name: "test", URL: "http://example.com/hook", Type: "generic", Enabled: false})
	if len(wn.List()) != 0 {
		t.Error("disabled webhooks should not appear in list")
	}
}

func TestBuildFeishuPayload(t *testing.T) {
	wn := operationstools.NewWebhookNotifier()
	payload := wn.BuildFeishuPayload(operationstools.Notification{Title: "Alert", Content: "Something happened", Level: "error"})
	if !strings.Contains(string(payload), "Alert") {
		t.Error("feishu payload should contain title")
	}
	if !strings.Contains(string(payload), "red") {
		t.Error("error level should be red")
	}
}

func TestBuildDingTalkPayload(t *testing.T) {
	wn := operationstools.NewWebhookNotifier()
	payload := wn.BuildDingTalkPayload(operationstools.Notification{Title: "Info", Content: "All good", Level: "info"})
	if !strings.Contains(string(payload), "Info") {
		t.Error("dingtalk payload should contain title")
	}
}

func TestBuildGenericPayload(t *testing.T) {
	wn := operationstools.NewWebhookNotifier()
	payload := wn.BuildGenericPayload(operationstools.Notification{Title: "Test", Content: "Body", Level: "success", Source: "cron"})
	if !strings.Contains(string(payload), "Test") {
		t.Error("generic payload should contain title")
	}
	if !strings.Contains(string(payload), "cron") {
		t.Error("generic payload should contain source")
	}
}

func TestNotifyToolActions(t *testing.T) {
	wn := operationstools.NewWebhookNotifier()
	tool := operationstools.NewNotifyTool(wn)

	// List empty
	result, _ := tool.Execute(nil, map[string]interface{}{"action": "webhooks"})
	if !strings.Contains(result, "No webhooks") {
		t.Error("should indicate no webhooks")
	}

	// Add webhook
	result, _ = tool.Execute(nil, map[string]interface{}{
		"action": "add_webhook", "webhook_name": "h1",
		"webhook_url": "http://example.com/hook", "webhook_type": "generic",
	})
	if !strings.Contains(result, "registered") {
		t.Errorf("add should succeed: %s", result)
	}

	// List with webhook
	result, _ = tool.Execute(nil, map[string]interface{}{"action": "webhooks"})
	if !strings.Contains(result, "h1") {
		t.Error("list should show registered webhook")
	}

	// Remove
	result, _ = tool.Execute(nil, map[string]interface{}{"action": "remove_webhook", "webhook_name": "h1"})
	if !strings.Contains(result, "removed") {
		t.Error("remove should succeed")
	}

	// Send notification (no webhooks configured)
	result, _ = tool.Execute(nil, map[string]interface{}{
		"action": "send", "title": "Test", "content": "Body",
	})
	if !strings.Contains(result, "sent") {
		t.Logf("send result: %s", result)
	}
}

func TestNotifyToolSendRequiresTitle(t *testing.T) {
	wn := operationstools.NewWebhookNotifier()
	tool := operationstools.NewNotifyTool(wn)

	result, _ := tool.Execute(nil, map[string]interface{}{"action": "send"})
	if !strings.Contains(result, "Error") {
		t.Error("should require title and content")
	}
}

func TestNotificationDefaults(t *testing.T) {
	n := operationstools.Notification{Title: "T", Content: "C"}
	if n.Time == "" {
		wn := operationstools.NewWebhookNotifier()
		wn.Send(n) // should auto-fill time
	}
}
