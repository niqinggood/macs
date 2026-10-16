package app

import (
	"testing"

	"mac/internal/security"
)

func TestToolErrorMiddlewarePreservesBusinessFailurePayloadsAsData(t *testing.T) {
	payloads := []struct {
		tool    string
		content string
	}{
		{
			tool: "read_file",
			content: `{"status":"error","message":"AI未能从描述中解析出有效信息"}
{"status":"error","message":"第二首生成失败"}`,
		},
		{
			tool:    "cli",
			content: `{"status":"error","message":"API业务失败，但命令正常退出"}`,
		},
		{
			tool: "web_fetch",
			content: `# API 文档
失败示例：{"status":"error","message":"invalid payload"}
UTF-8 编码错误可能显示为乱码。`,
		},
	}

	middleware := NewToolErrorMiddleware()
	for _, item := range payloads {
		ctx := &AgentContext{
			ToolCall:   ToolCall{ID: "call-1", Name: item.tool},
			ToolResult: &ToolResult{ToolCallID: "call-1", Name: item.tool, Content: item.content},
		}
		if err := middleware.AfterTool(ctx); err != nil {
			t.Fatalf("%s middleware returned error: %v", item.tool, err)
		}
		if ctx.ToolErr != nil || ctx.ToolResult.Content != item.content {
			t.Fatalf("%s business payload was converted into tool failure: err=%v content=%q", item.tool, ctx.ToolErr, ctx.ToolResult.Content)
		}
		if !ToolExecutionSucceededForTool(item.tool, item.content, nil) {
			t.Fatalf("%s execution should remain successful so the model can inspect the business response", item.tool)
		}
	}
}

func TestPromptInjectionAllowsLeadingUTF8BOM(t *testing.T) {
	detector := security.NewPromptInjectionDetector()
	result := detector.Scan("\ufeff# 音乐生成接口\n正常的 UTF-8 文档", "tool:read_file")
	if result.Severity != security.SevNone {
		t.Fatalf("leading UTF-8 BOM must not be classified as injection: severity=%v pattern=%q", result.Severity, result.Pattern)
	}
}

func TestPromptInjectionStillRejectsEmbeddedZeroWidthCharacters(t *testing.T) {
	detector := security.NewPromptInjectionDetector()
	tests := []string{
		"normal\ufeffhidden",
		"normal\u200bhidden",
		"\ufeffnormal\ufeffhidden",
	}
	for _, content := range tests {
		result := detector.Scan(content, "tool:read_file")
		if result.Severity != security.SevHigh {
			t.Errorf("embedded zero-width character must remain high severity: content=%q severity=%v", content, result.Severity)
		}
	}
}
