package channels

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/open-dingtalk/dingtalk-stream-sdk-go/chatbot"
	"github.com/open-dingtalk/dingtalk-stream-sdk-go/client"
)

// DingTalkChannelConfig contains the credentials required by DingTalk Stream.
type DingTalkChannelConfig struct {
	ClientID     string `json:"client_id" yaml:"client_id"`
	ClientSecret string `json:"client_secret" yaml:"client_secret"`
	RobotCode    string `json:"robot_code" yaml:"robot_code"`
}

// DingTalkMessage is the platform-neutral message passed to the application
// composition layer. SDK-specific callback values do not escape this package.
type DingTalkMessage struct {
	ChatID    string
	UserID    string
	MessageID string
	Content   string
	ChatType  string
}

// DingTalkChannel owns the DingTalk SDK connection, callback decoding,
// duplicate suppression, media handling and outbound delivery.
type DingTalkChannel struct {
	*Base
	config         DingTalkChannelConfig
	client         *client.StreamClient
	ctx            context.Context
	cancel         context.CancelFunc
	started        chan struct{}
	startedOnce    sync.Once
	mu             sync.RWMutex
	processedIDs   sync.Map
	mediaHandler   *ChannelMediaHandler
	webhooks       map[string]string
	messageHandler func(DingTalkMessage)
}

func NewDingTalkChannel(name string, config DingTalkChannelConfig, workspace string) *DingTalkChannel {
	ctx, cancel := context.WithCancel(context.Background())
	base := NewBase(name, "dingtalk")
	return &DingTalkChannel{
		Base:         base,
		config:       config,
		ctx:          ctx,
		cancel:       cancel,
		started:      make(chan struct{}),
		mediaHandler: NewChannelMediaHandler(workspace, base.logger, nil),
		webhooks:     make(map[string]string),
	}
}

// SetMessageHandler connects transport events to application business logic.
func (channel *DingTalkChannel) SetMessageHandler(handler func(DingTalkMessage)) {
	channel.messageHandler = handler
}

func (channel *DingTalkChannel) Start() error {
	channel.logger.Printf("starting DingTalk stream for client %s", channel.config.ClientID)
	channel.client = client.NewStreamClient(
		client.WithAppCredential(
			client.NewAppCredentialConfig(channel.config.ClientID, channel.config.ClientSecret),
		),
	)
	channel.client.RegisterChatBotCallbackRouter(channel.onChatBotMessageReceived)

	go func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				channel.logger.Printf("DingTalk stream panic: %v", recovered)
			}
		}()
		if err := channel.client.Start(channel.ctx); err != nil {
			channel.logger.Printf("DingTalk stream failed: %v", err)
		}
	}()

	// The SDK does not expose a synchronous connection-ready signal. Preserve
	// the existing startup grace period before reporting the channel as started.
	time.Sleep(3 * time.Second)
	channel.startedOnce.Do(func() { close(channel.started) })
	return nil
}

func (channel *DingTalkChannel) onChatBotMessageReceived(_ context.Context, data *chatbot.BotCallbackDataModel) ([]byte, error) {
	if data == nil {
		return []byte("success"), nil
	}
	channel.logger.Printf("received DingTalk message: id=%s conversation=%s", data.MsgId, data.ConversationId)
	if data.MsgId != "" {
		if _, duplicate := channel.processedIDs.Load(data.MsgId); duplicate {
			return []byte("success"), nil
		}
		channel.processedIDs.Store(data.MsgId, time.Now())
		go channel.cleanupProcessedIDs()
	}

	content := channel.messageContent(data)
	if strings.TrimSpace(content) == "" {
		return []byte("success"), nil
	}

	userID := data.SenderStaffId
	if userID == "" {
		userID = data.SenderId
	}
	if userID == "" {
		userID = "unknown"
	}
	chatType := "c2c"
	if data.ConversationType == "1" {
		chatType = "group"
	}

	if data.SessionWebhook != "" {
		channel.mu.Lock()
		channel.webhooks[data.ConversationId] = data.SessionWebhook
		channel.mu.Unlock()
	}

	if channel.messageHandler == nil {
		channel.logger.Printf("DingTalk message handler is not configured")
		_ = channel.SendMessage(data.ConversationId, "收到消息: "+content)
		return []byte("success"), nil
	}
	channel.messageHandler(DingTalkMessage{
		ChatID:    data.ConversationId,
		UserID:    userID,
		MessageID: data.MsgId,
		Content:   content,
		ChatType:  chatType,
	})
	return []byte("success"), nil
}

func (channel *DingTalkChannel) messageContent(data *chatbot.BotCallbackDataModel) string {
	switch data.Msgtype {
	case "text":
		return strings.TrimSpace(data.Text.Content)
	case "image":
		return channel.extractMediaContent(data, "image")
	case "file":
		return channel.extractMediaContent(data, "file")
	case "audio", "voice":
		return channel.extractMediaContent(data, "audio")
	default:
		return fmt.Sprintf("[%s消息] 收到暂不支持的消息类型", data.Msgtype)
	}
}

func (channel *DingTalkChannel) extractMediaContent(data *chatbot.BotCallbackDataModel, mediaType string) string {
	if content, ok := data.Content.(map[string]interface{}); ok {
		if downloadURL, _ := content["downloadUrl"].(string); downloadURL != "" {
			return channel.processMediaFromURL(downloadURL, fmt.Sprintf("dingtalk_%s", mediaType), mediaType)
		}
		if mediaURL, _ := content["url"].(string); mediaURL != "" {
			return channel.processMediaFromURL(mediaURL, fmt.Sprintf("dingtalk_%s", mediaType), mediaType)
		}
		if mediaID, _ := content["mediaId"].(string); mediaID != "" {
			return fmt.Sprintf("[%s消息] media_id: %s\n请通过钉钉开放平台下载后分析", mediaType, mediaID)
		}
	}
	return fmt.Sprintf("[%s消息] 收到媒体内容，请补充文字描述", mediaType)
}

func (channel *DingTalkChannel) processMediaFromURL(mediaURL, fileName, mediaType string) string {
	result, err := channel.mediaHandler.DownloadAndProcess(mediaURL, fileName, "application/octet-stream")
	if err != nil {
		channel.logger.Printf("failed to process DingTalk %s: %v", mediaType, err)
		return fmt.Sprintf("[%s消息] 下载或处理失败: %v", mediaType, err)
	}
	return result
}

func (channel *DingTalkChannel) cleanupProcessedIDs() {
	now := time.Now()
	channel.processedIDs.Range(func(key, value interface{}) bool {
		if processedAt, ok := value.(time.Time); ok && now.Sub(processedAt) > 10*time.Minute {
			channel.processedIDs.Delete(key)
		}
		return true
	})
}

func (channel *DingTalkChannel) Stop() error {
	if channel.cancel != nil {
		channel.cancel()
	}
	if channel.client != nil {
		channel.client.Close()
	}
	return nil
}

func (channel *DingTalkChannel) SendMessage(recipient, content string) error {
	channel.mu.RLock()
	webhook := channel.webhooks[recipient]
	channel.mu.RUnlock()
	if webhook == "" {
		return fmt.Errorf("DingTalk webhook for %s is unavailable", recipient)
	}
	return chatbot.NewChatbotReplier().SimpleReplyText(context.Background(), webhook, []byte(content))
}

func (channel *DingTalkChannel) IsConnected() bool {
	select {
	case <-channel.started:
		return channel.client != nil
	default:
		return false
	}
}

func (channel *DingTalkChannel) GetConnectionStatus() string {
	select {
	case <-channel.started:
		if channel.client != nil {
			return "已启动（长连接）"
		}
		return "已启动（等待连接）"
	default:
		return "启动中"
	}
}
