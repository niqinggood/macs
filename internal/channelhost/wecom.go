package channelhost

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	channelcore "mac/internal/channels"
)

type WeWorkChannelConfig = channelcore.WeWorkChannelConfig

// WeWorkChannel binds Enterprise WeChat events to the application runtime.
type WeWorkChannel struct {
	*channelcore.WeWorkChannel
	runtime Runtime
	msgSeq  atomic.Uint64
}

func NewWeWorkChannel(name string, config WeWorkChannelConfig, workspace string) *WeWorkChannel {
	inner := channelcore.NewWeWorkChannel(name, config, workspace)
	channel := &WeWorkChannel{WeWorkChannel: inner}
	inner.SetMessageHandler(channel.processUserMessage)
	return channel
}

func (channel *WeWorkChannel) SetRuntime(runtime channelcore.Runtime) {
	channel.WeWorkChannel.SetRuntime(runtime)
	channel.runtime = runtimeOf(runtime)
}

func (channel *WeWorkChannel) processUserMessage(message channelcore.WeComMessage) {
	runtime := channel.runtime
	sessionID := fmt.Sprintf("wework_%s_%s", message.ChatType, message.ChatID)
	if runtime != nil && runtime.TryInjectInterrupt(sessionID, channel.Name(), message.UserID, message.Content) {
		_ = channel.SendStreamReply(message, "收到，正在处理中，消息已追加...")
		return
	}
	if runtime != nil {
		handled, taskID, reply, err := runtime.TryCodex(
			context.Background(), sessionID, channel.Name(), message.UserID, message.ChatID, message.Content,
		)
		if handled {
			if err != nil {
				_ = channel.SendStreamReply(message, fmt.Sprintf("Codex interactive failed: %v", err))
				return
			}
			runtime.SaveChatMessage(sessionID, "user", "", message.Content, "completed", taskID, channel.Name(), message.UserID)
			if strings.TrimSpace(reply) != "" {
				_ = channel.SendStreamReply(message, reply)
			}
			return
		}

		handled, employeeName, taskID, err := runtime.TryEmployee(
			context.Background(), message.Content, sessionID, channel.Name(), message.ChatID, message.UserID,
		)
		if handled {
			if err != nil {
				_ = channel.SendStreamReply(message, fmt.Sprintf("AI员工 %s 启动失败: %v", employeeName, err))
				return
			}
			runtime.SaveChatMessage(sessionID, "user", "", message.Content, "pending", taskID, channel.Name(), message.UserID)
			_ = channel.SendStreamReply(message, fmt.Sprintf("%s 已收到任务，开始处理...", employeeName))
			return
		}
	}
	if runtime == nil || !runtime.Available() {
		_ = channel.SendStreamReply(message, "收到消息: "+message.Content)
		return
	}
	taskID := fmt.Sprintf("wework_task_%d_%d", time.Now().UnixNano(), channel.msgSeq.Add(1))
	runtime.SaveChatMessage(sessionID, "user", "", message.Content, "pending", taskID, channel.Name(), message.UserID)
	interruptCh := runtime.RegisterInterrupt(sessionID)
	go channel.processAIResponse(message, sessionID, taskID, interruptCh)
}

func (channel *WeWorkChannel) processAIResponse(message channelcore.WeComMessage, sessionID, taskID string, interruptCh chan string) {
	runtime := channel.runtime
	if runtime == nil {
		return
	}
	defer runtime.UnregisterInterrupt(sessionID, interruptCh)
	agentName, agent, ok := runtime.DefaultAgent()
	if !ok || agent == nil {
		_ = channel.SendStreamReply(message, "系统错误：没有可用的 Agent")
		return
	}
	input := runtime.HistoryContext(sessionID, message.UserID, taskID) + message.Content
	ctx := channelcore.AgentContext(context.Background(), taskID, sessionID, channel.Name(), message.UserID)
	result, err := channelcore.GenerateWithInterrupts(ctx, agent, input, interruptCh, func(interruptMessage string) {
		runtime.SaveChatMessage(sessionID, "user", "", interruptMessage, "pending", taskID, channel.Name(), message.UserID)
	})
	if err != nil {
		runtime.SaveChatMessage(sessionID, "assistant", agentName, err.Error(), "failed", taskID, channel.Name(), message.UserID)
		_ = channel.SendStreamReply(message, fmt.Sprintf("处理失败: %v", err))
		return
	}
	runtime.SaveChatMessage(sessionID, "assistant", agentName, result, "completed", taskID, channel.Name(), message.UserID)
	_ = channel.SendStreamReply(message, result)
}
