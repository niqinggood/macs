package channelhost

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"mac/internal/agentresult"
	channelcore "mac/internal/channels"
)

type FeishuChannelConfig = channelcore.FeishuChannelConfig

// FeishuChannel binds Lark transport events to the application runtime.
type FeishuChannel struct {
	*channelcore.FeishuChannel
	runtime Runtime
}

func NewFeishuChannel(name string, config FeishuChannelConfig, workspace string) *FeishuChannel {
	inner := channelcore.NewFeishuChannel(name, config, workspace)
	channel := &FeishuChannel{FeishuChannel: inner}
	inner.SetMessageHandler(channel.processBusinessLogic)
	return channel
}

func (channel *FeishuChannel) SetRuntime(runtime channelcore.Runtime) {
	channel.FeishuChannel.SetRuntime(runtime)
	channel.runtime = runtimeOf(runtime)
	if channel.runtime != nil {
		channel.SetApprovalResolver(channel.runtime.ResolveApproval)
	}
}

func (channel *FeishuChannel) processBusinessLogic(chatID, userMessage string) {
	runtime := channel.runtime
	if runtime == nil {
		log.Printf("[Feishu] runtime is not initialized; dropping message for chat=%s", chatID)
		return
	}
	sessionID := fmt.Sprintf("feishu_%s", chatID)
	channelName := channel.Name()
	handled, taskID, reply, err := runtime.TryCodex(
		context.Background(), sessionID, channelName, sessionID, chatID, userMessage,
	)
	if handled {
		if err != nil {
			_ = channel.SendMessage(chatID, fmt.Sprintf("Codex interactive failed: %v", err))
			return
		}
		runtime.SaveChatMessage(sessionID, "user", "", userMessage, "completed", taskID, channelName, sessionID)
		if strings.TrimSpace(reply) != "" {
			_ = channel.SendMessage(chatID, reply)
		}
		return
	}
	handled, employeeName, taskID, err := runtime.TryEmployee(
		context.Background(), userMessage, sessionID, channelName, chatID, "",
	)
	if handled {
		if err != nil {
			_ = channel.SendMessage(chatID, fmt.Sprintf("AI员工 %s 启动失败: %v", employeeName, err))
			return
		}
		runtime.SaveChatMessage(sessionID, "user", "", userMessage, "pending", taskID, channelName, "")
		if employeeName != "codex-specialist" {
			_ = channel.SendMessage(chatID, fmt.Sprintf("%s 已收到任务，开始处理...", employeeName))
		}
		return
	}
	if !runtime.Available() {
		log.Printf("[Feishu] persistence is not initialized; dropping message for chat=%s", chatID)
		return
	}
	if runtime.TryInjectInterrupt(sessionID, channelName, "", userMessage) {
		_ = channel.SendMessage(chatID, "收到，正在处理中，消息已追加...")
		return
	}
	taskID = fmt.Sprintf("feishu_task_%d", time.Now().UnixNano())
	runtime.SaveChatMessage(sessionID, "user", "", userMessage, "pending", taskID, channelName, "")
	go channel.processAIResponse(chatID, sessionID, userMessage, taskID)
}

func (channel *FeishuChannel) processAIResponse(chatID, sessionID, userMessage, taskID string) {
	runtime := channel.runtime
	if runtime == nil {
		return
	}
	agentName, agent, ok := runtime.DefaultAgent()
	if !ok || agent == nil {
		_ = channel.SendMessage(chatID, "系统错误：没有可用的 Agent")
		return
	}
	finalMessage := runtime.HistoryContext(sessionID, "", taskID) + channelcore.PrepareMessageForAgent(userMessage)
	interruptCh := runtime.RegisterInterrupt(sessionID)
	defer runtime.UnregisterInterrupt(sessionID, interruptCh)
	ctx := channelcore.AgentContext(context.Background(), taskID, sessionID, channel.Name(), chatID)
	result, err := channelcore.GenerateWithInterrupts(ctx, agent, finalMessage, interruptCh, func(interruptMessage string) {
		runtime.SaveChatMessage(sessionID, "user", "", interruptMessage, "pending", taskID, channel.Name(), "")
	})
	if err != nil {
		failure := agentresult.FailureUserMessage(result, err)
		_ = channel.SendMessage(chatID, failure)
		runtime.SaveChatMessage(sessionID, "assistant", agentName, failure, "failed", taskID, channel.Name(), "")
		return
	}
	runtime.SaveChatMessage(sessionID, "assistant", agentName, result, "completed", taskID, channel.Name(), "")
	if err := channelcore.SendContent(runtime.ArtifactWorkspace(), channel, chatID, result, log.Default()); err != nil {
		log.Printf("[Feishu] send reply failed: %v", err)
	}
	go channel.SendGeneratedFiles(chatID, result)
}
