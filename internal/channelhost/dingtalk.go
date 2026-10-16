package channelhost

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	channelcore "mac/internal/channels"
)

type DingTalkChannelConfig = channelcore.DingTalkChannelConfig

// DingTalkChannel binds DingTalk transport events to the application runtime.
type DingTalkChannel struct {
	*channelcore.DingTalkChannel
	runtime Runtime
	msgSeq  atomic.Uint64
}

func NewDingTalkChannel(name string, config DingTalkChannelConfig, workspace string) *DingTalkChannel {
	inner := channelcore.NewDingTalkChannel(name, config, workspace)
	channel := &DingTalkChannel{DingTalkChannel: inner}
	inner.SetMessageHandler(channel.processUserMessage)
	return channel
}

func (channel *DingTalkChannel) SetRuntime(runtime channelcore.Runtime) {
	channel.DingTalkChannel.SetRuntime(runtime)
	channel.runtime = runtimeOf(runtime)
}

func (channel *DingTalkChannel) processUserMessage(message channelcore.DingTalkMessage) {
	runtime := channel.runtime
	sessionID := fmt.Sprintf("dingtalk_%s_%s", message.ChatType, message.ChatID)
	if runtime != nil && runtime.TryInjectInterrupt(sessionID, channel.Name(), message.UserID, message.Content) {
		_ = channel.SendMessage(message.ChatID, "收到，正在处理中，消息已追加...")
		return
	}
	if runtime != nil {
		handled, employeeName, taskID, err := runtime.TryEmployee(
			context.Background(), message.Content, sessionID, channel.Name(), message.ChatID, message.UserID,
		)
		if handled {
			if err != nil {
				_ = channel.SendMessage(message.ChatID, fmt.Sprintf("AI员工 %s 启动失败: %v", employeeName, err))
				return
			}
			runtime.SaveChatMessage(sessionID, "user", "", message.Content, "pending", taskID, channel.Name(), message.UserID)
			_ = channel.SendMessage(message.ChatID, fmt.Sprintf("%s 已收到任务，开始处理...", employeeName))
			return
		}
	}
	if runtime == nil || !runtime.Available() {
		_ = channel.SendMessage(message.ChatID, "收到消息: "+message.Content)
		return
	}
	taskID := fmt.Sprintf("dingtalk_task_%d_%d", time.Now().UnixNano(), channel.msgSeq.Add(1))
	runtime.SaveChatMessage(sessionID, "user", "", message.Content, "pending", taskID, channel.Name(), message.UserID)
	interruptCh := runtime.RegisterInterrupt(sessionID)
	go channel.processAIResponse(message, sessionID, taskID, interruptCh)
}

func (channel *DingTalkChannel) processAIResponse(message channelcore.DingTalkMessage, sessionID, taskID string, interruptCh chan string) {
	runtime := channel.runtime
	if runtime == nil {
		return
	}
	defer runtime.UnregisterInterrupt(sessionID, interruptCh)
	agentName, agent, ok := runtime.DefaultAgent()
	if !ok || agent == nil {
		_ = channel.SendMessage(message.ChatID, "系统错误：没有可用的 Agent")
		return
	}
	input := runtime.HistoryContext(sessionID, message.UserID, taskID) + message.Content
	ctx := channelcore.AgentContext(context.Background(), taskID, sessionID, channel.Name(), message.UserID)
	result, err := channelcore.GenerateWithInterrupts(ctx, agent, input, interruptCh, func(interruptMessage string) {
		runtime.SaveChatMessage(sessionID, "user", "", interruptMessage, "pending", taskID, channel.Name(), message.UserID)
	})
	if err != nil {
		runtime.SaveChatMessage(sessionID, "assistant", agentName, err.Error(), "failed", taskID, channel.Name(), message.UserID)
		_ = channel.SendMessage(message.ChatID, fmt.Sprintf("处理失败: %v", err))
		return
	}
	runtime.SaveChatMessage(sessionID, "assistant", agentName, result, "completed", taskID, channel.Name(), message.UserID)
	_ = channel.SendMessage(message.ChatID, channelcore.FormatContentForTextFallback(runtime.ArtifactWorkspace(), result))
}
