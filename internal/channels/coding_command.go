package channels

import (
	"fmt"
	"strings"
)

func PrepareMessageForAgent(message string) string {
	agent, task, ok := ParseCodingCommand(message)
	if !ok {
		return message
	}
	return fmt.Sprintf(`用户通过外部通信渠道要求调用编码代理完成代码任务。

必须调用 external_agent 工具：
- agent: %s
- prompt: %s

执行要求：
1. 让 Claude Code / Codex 在当前 OpenKitty 工作区内完成任务。
2. 完成后必须把 external_agent 返回内容转述给用户，包括 Report 路径、Changed files 和 Claude/Codex 输出摘要。
3. 如果 external_agent 失败，要把失败原因和可执行的下一步回传给用户。

原始渠道消息：
%s`, agent, task, message)
}

func ParseCodingCommand(message string) (agent string, task string, ok bool) {
	trimmed := strings.TrimSpace(message)
	if trimmed == "" {
		return "", "", false
	}
	lower := strings.ToLower(trimmed)
	prefixes := []struct {
		prefix string
		agent  string
	}{
		{"/claude ", "claude"},
		{"/claudecode ", "claude"},
		{"/claude-code ", "claude"},
		{"@claude ", "claude"},
		{"claude code:", "claude"},
		{"claude:", "claude"},
		{"/codex ", "codex"},
		{"@codex ", "codex"},
		{"codex:", "codex"},
		{"/code ", "auto"},
		{"/coding ", "auto"},
	}
	for _, p := range prefixes {
		if strings.HasPrefix(lower, p.prefix) {
			task = strings.TrimSpace(trimmed[len(p.prefix):])
			if task == "" {
				task = "请根据上下文完成代码任务，并回传执行结果。"
			}
			return p.agent, task, true
		}
	}
	return "", "", false
}
