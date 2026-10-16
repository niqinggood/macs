package channelhost

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	channelcore "mac/internal/channels"
	"mac/internal/processutil"
)

type QQChannelConfig = channelcore.QQChannelConfig

// QQChannel binds the BotGo transport to the application runtime.
type QQChannel struct {
	*channelcore.QQChannel
	runtime Runtime
	msgSeq  atomic.Uint64
}

func NewQQChannel(name string, config QQChannelConfig) *QQChannel {
	inner := channelcore.NewQQChannel(name, config)
	channel := &QQChannel{QQChannel: inner}
	inner.SetMessageHandler(channel.processUserMessage)
	return channel
}

func (channel *QQChannel) SetRuntime(runtime channelcore.Runtime) {
	channel.QQChannel.SetRuntime(runtime)
	channel.runtime = runtimeOf(runtime)
	if channel.runtime != nil {
		channel.SetApprovalResolver(channel.runtime.ResolveApproval)
	}
}

func (channel *QQChannel) InitVoiceHandler(workspace string) {
	channel.QQChannel.InitVoiceHandler(workspace, processutil.ResolveFFmpegBinary)
}

func (channel *QQChannel) processUserMessage(message channelcore.QQMessage) {
	runtime := channel.runtime
	sessionID := fmt.Sprintf("qq_%s_%s", message.ChatType, message.ChatID)
	if runtime != nil && runtime.TryInjectInterrupt(sessionID, channel.Name(), message.UserID, message.Content) {
		_ = channel.SendChatMessage(message.ChatID, message.MessageID, "收到，正在处理中，消息已追加...", message.ChatType)
		return
	}
	if runtime != nil {
		handled, employeeName, taskID, err := runtime.TryEmployee(
			context.Background(), message.Content, sessionID, channel.Name(), message.ChatID, message.UserID,
		)
		if handled {
			if err != nil {
				_ = channel.SendChatMessage(message.ChatID, message.MessageID, fmt.Sprintf("AI员工 %s 启动失败: %v", employeeName, err), message.ChatType)
				return
			}
			runtime.SaveChatMessage(sessionID, "user", "", message.Content, "pending", taskID, channel.Name(), message.UserID)
			_ = channel.SendChatMessage(message.ChatID, message.MessageID, fmt.Sprintf("%s 已收到任务，开始处理...", employeeName), message.ChatType)
			return
		}
	}
	if runtime == nil || !runtime.Available() {
		_ = channel.SendChatMessage(message.ChatID, message.MessageID, "收到消息: "+message.Content, message.ChatType)
		return
	}
	taskID := fmt.Sprintf("qq_task_%d_%d", time.Now().UnixNano(), channel.msgSeq.Add(1))
	runtime.SaveChatMessage(sessionID, "user", "", message.Content, "pending", taskID, channel.Name(), message.UserID)
	interruptCh := runtime.RegisterInterrupt(sessionID)
	go channel.processAIResponse(message, sessionID, taskID, interruptCh)
}

func (channel *QQChannel) processAIResponse(message channelcore.QQMessage, sessionID, taskID string, interruptCh chan string) {
	runtime := channel.runtime
	if runtime == nil {
		return
	}
	defer runtime.UnregisterInterrupt(sessionID, interruptCh)
	agentName, agent, ok := runtime.DefaultAgent()
	if !ok || agent == nil {
		_ = channel.SendChatMessage(message.ChatID, message.MessageID, "系统错误：没有可用的 Agent", message.ChatType)
		return
	}
	input := runtime.HistoryContext(sessionID, message.UserID, taskID) + message.Content
	ctx := channelcore.AgentContext(context.Background(), taskID, sessionID, channel.Name(), message.UserID)
	result, err := channelcore.GenerateWithInterrupts(ctx, agent, input, interruptCh, func(interruptMessage string) {
		runtime.SaveChatMessage(sessionID, "user", "", interruptMessage, "pending", taskID, channel.Name(), message.UserID)
	})
	if err != nil {
		runtime.SaveChatMessage(sessionID, "assistant", agentName, err.Error(), "failed", taskID, channel.Name(), message.UserID)
		_ = channel.SendChatMessage(message.ChatID, message.MessageID, fmt.Sprintf("处理失败: %v", err), message.ChatType)
		return
	}
	runtime.SaveChatMessage(sessionID, "assistant", agentName, result, "completed", taskID, channel.Name(), message.UserID)
	result = channelcore.FormatContentForTextFallback(runtime.ArtifactWorkspace(), result)
	_ = channel.SendChatMessage(message.ChatID, message.MessageID, result, message.ChatType)
}
