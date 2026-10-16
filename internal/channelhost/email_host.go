package channelhost

import (
	"fmt"
	"time"

	channelcore "mac/internal/channels"
)

const (
	emailSupervisorSystemMessage = "你是一个系统管理员助手，负责执行系统命令和管理任务。回答应简洁、专业、准确。"
	emailUserSystemMessage       = "你是一个友好的 AI 助手，负责通过邮件与用户交流。回答应热情、详细、有帮助，并且不可泄露密码、令牌等敏感信息。"
)

type EmailChannelConfig = channelcore.EmailChannelConfig

// EmailChannel connects the email transport to host persistence and agent orchestration.
type EmailChannel struct {
	*channelcore.EmailChannel
	runtime Runtime
}

func NewEmailChannel(name string, config EmailChannelConfig) *EmailChannel {
	inner := channelcore.NewEmailChannel(name, config)
	channel := &EmailChannel{EmailChannel: inner}
	inner.SetMessageHandler(channel.processIncomingMessage)
	return channel
}

func (channel *EmailChannel) SetRuntime(runtime channelcore.Runtime) {
	channel.EmailChannel.SetRuntime(runtime)
	channel.runtime = runtimeOf(runtime)
}

func (channel *EmailChannel) processIncomingMessage(message channelcore.EmailMessage) {
	runtime := channel.runtime
	if runtime == nil || !runtime.Available() {
		return
	}
	systemMessage := emailUserSystemMessage
	if message.IsSupervisor {
		systemMessage = emailSupervisorSystemMessage
	}
	userMessage := fmt.Sprintf("主题: %s\n\n%s", message.Subject, message.Body)
	sessionID := fmt.Sprintf("email_%s", message.From)
	taskID := fmt.Sprintf("email_task_%d", time.Now().UnixNano())
	runtime.SaveChatMessage(sessionID, "user", "", userMessage, "pending", taskID, channel.Name(), message.From)
	go channel.processAIResponse(message, sessionID, taskID, userMessage, systemMessage)
}

func (channel *EmailChannel) processAIResponse(message channelcore.EmailMessage, sessionID, taskID, userMessage, systemMessage string) {
	runtime := channel.runtime
	if runtime == nil {
		return
	}
	agentName, agent, ok := runtime.DefaultAgent()
	if !ok || agent == nil {
		_ = channel.SendReply(message.From, "Re: "+message.Subject, "系统错误：没有可用的 Agent", message.MessageID)
		return
	}
	finalMessage := fmt.Sprintf("【系统指令】%s\n\n%s", systemMessage, userMessage)
	if history := runtime.HistoryContext(sessionID, message.From, taskID); history != "" {
		finalMessage = history + finalMessage
	}
	result, err := agent.Generate(channel.AgentContext(sessionID, taskID, message.From), finalMessage)
	if err != nil {
		_ = channel.SendReply(message.From, "Re: "+message.Subject, fmt.Sprintf("处理失败: %v", err), message.MessageID)
		runtime.SaveChatMessage(sessionID, "assistant", agentName, err.Error(), "failed", taskID, channel.Name(), message.From)
		return
	}
	runtime.SaveChatMessage(sessionID, "assistant", agentName, result, "completed", taskID, channel.Name(), message.From)
	_ = channel.SendReply(message.From, "Re: "+message.Subject, result, message.MessageID)
}
