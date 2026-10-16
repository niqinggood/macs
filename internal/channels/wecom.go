package channels

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

const wecomWebSocketURL = "wss://openws.work.weixin.qq.com"

type WeWorkChannelConfig struct {
	BotID  string `json:"bot_id" yaml:"bot_id"`
	Secret string `json:"secret" yaml:"secret"`
}

// WeComMessage is the application-facing form of an Enterprise WeChat
// callback. Protocol frame details remain inside this package.
type WeComMessage struct {
	ChatID        string
	UserID        string
	MessageID     string
	ResponseMsgID string
	Content       string
	ChatType      string
	RequestID     string
}

type wecomRequestFrame struct {
	Cmd     string                 `json:"cmd"`
	Headers map[string]string      `json:"headers,omitempty"`
	Body    map[string]interface{} `json:"body,omitempty"`
}

type wecomResponseFrame struct {
	Cmd     string                 `json:"cmd"`
	Headers map[string]string      `json:"headers"`
	Body    map[string]interface{} `json:"body"`
	ErrCode int                    `json:"errcode"`
	ErrMsg  string                 `json:"errmsg"`
}

type wecomMessageCallback struct {
	MsgID          string `json:"msgid"`
	ChatID         string `json:"chatid"`
	ConversationID string `json:"conversation_id"`
	ChatType       string `json:"chattype"`
	ResponseMsgID  string `json:"response_msgid"`
	From           struct {
		UserID string `json:"userid"`
	} `json:"from"`
	MsgType string `json:"msgtype"`
	Text    struct {
		Content string `json:"content"`
	} `json:"text,omitempty"`
	Voice struct {
		MediaID  string `json:"media_id"`
		MediaURL string `json:"media_url"`
		Format   string `json:"format"`
	} `json:"voice,omitempty"`
	Image struct {
		MediaID  string `json:"media_id"`
		MediaURL string `json:"media_url"`
	} `json:"image,omitempty"`
}

func (message *wecomMessageCallback) chatID() string {
	if message.ChatID != "" {
		return message.ChatID
	}
	return message.ConversationID
}

type wecomEventCallback struct {
	ChatID string `json:"chatid"`
	From   struct {
		UserID string `json:"userid"`
	} `json:"from"`
	Event struct {
		EventType string `json:"eventtype"`
	} `json:"event"`
}

// WeWorkChannel owns the Enterprise WeChat websocket protocol, reconnect,
// heartbeat, callback decoding, media handling and response framing.
type WeWorkChannel struct {
	*Base
	config         WeWorkChannelConfig
	conn           *websocket.Conn
	ctx            context.Context
	cancel         context.CancelFunc
	started        chan struct{}
	startedOnce    sync.Once
	mu             sync.RWMutex
	processedIDs   sync.Map
	mediaHandler   *ChannelMediaHandler
	requestSeq     uint64
	messageHandler func(WeComMessage)
}

func NewWeWorkChannel(name string, config WeWorkChannelConfig, workspace string) *WeWorkChannel {
	ctx, cancel := context.WithCancel(context.Background())
	return &WeWorkChannel{
		Base:         NewBase(name, "wework"),
		config:       config,
		ctx:          ctx,
		cancel:       cancel,
		started:      make(chan struct{}),
		mediaHandler: NewChannelMediaHandler(workspace, log.New(os.Stdout, "[WeWorkMedia] ", log.LstdFlags), nil),
	}
}

func (channel *WeWorkChannel) SetMessageHandler(handler func(WeComMessage)) {
	channel.messageHandler = handler
}

func (channel *WeWorkChannel) Start() error {
	go channel.connectAndSubscribe()
	time.Sleep(3 * time.Second)
	channel.startedOnce.Do(func() { close(channel.started) })
	return nil
}

func (channel *WeWorkChannel) connectAndSubscribe() {
	if channel.ctx.Err() != nil {
		return
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			channel.logger.Printf("Enterprise WeChat connection panic: %v", recovered)
		}
	}()
	connection, _, err := websocket.DefaultDialer.Dial(wecomWebSocketURL, nil)
	if err != nil {
		channel.logger.Printf("Enterprise WeChat websocket failed: %v", err)
		channel.reconnectAfterDelay()
		return
	}
	channel.mu.Lock()
	channel.conn = connection
	channel.mu.Unlock()
	if err := channel.sendFrame(wecomRequestFrame{
		Cmd:     "aibot_subscribe",
		Headers: map[string]string{"req_id": channel.generateRequestID()},
		Body:    map[string]interface{}{"bot_id": channel.config.BotID, "secret": channel.config.Secret},
	}); err != nil {
		channel.logger.Printf("Enterprise WeChat subscribe failed: %v", err)
		_ = connection.Close()
		channel.reconnectAfterDelay()
		return
	}
	go channel.receiveLoop(connection)
	go channel.heartbeatLoop(connection)
}

func (channel *WeWorkChannel) reconnectAfterDelay() {
	select {
	case <-channel.ctx.Done():
		return
	case <-time.After(5 * time.Second):
		go channel.connectAndSubscribe()
	}
}

func (channel *WeWorkChannel) sendFrame(frame wecomRequestFrame) error {
	channel.mu.Lock()
	defer channel.mu.Unlock()
	if channel.conn == nil {
		return fmt.Errorf("Enterprise WeChat connection is not established")
	}
	return channel.conn.WriteJSON(frame)
}

func (channel *WeWorkChannel) receiveLoop(connection *websocket.Conn) {
	for channel.ctx.Err() == nil {
		var response wecomResponseFrame
		if err := connection.ReadJSON(&response); err != nil {
			channel.logger.Printf("Enterprise WeChat receive failed: %v", err)
			channel.reconnectAfterDelay()
			return
		}
		channel.handleResponse(&response)
	}
}

func (channel *WeWorkChannel) handleResponse(response *wecomResponseFrame) {
	if response.ErrCode != 0 {
		channel.logger.Printf("Enterprise WeChat error: code=%d message=%s", response.ErrCode, response.ErrMsg)
	}
	switch response.Cmd {
	case "aibot_msg_callback":
		channel.handleMessageCallback(response)
	case "aibot_event_callback":
		channel.handleEventCallback(response)
	}
}

func (channel *WeWorkChannel) handleMessageCallback(response *wecomResponseFrame) {
	body, err := json.Marshal(response.Body)
	if err != nil {
		return
	}
	var callback wecomMessageCallback
	if err := json.Unmarshal(body, &callback); err != nil {
		channel.logger.Printf("Enterprise WeChat callback parse failed: %v", err)
		return
	}
	if callback.MsgID != "" {
		if _, duplicate := channel.processedIDs.Load(callback.MsgID); duplicate {
			return
		}
		channel.processedIDs.Store(callback.MsgID, time.Now())
		go channel.cleanupProcessedIDs()
	}

	content := strings.TrimSpace(callback.Text.Content)
	if content == "" && callback.MsgType == "voice" && callback.Voice.MediaURL != "" {
		content = channel.processVoiceMessage(callback.Voice.MediaURL, callback.Voice.Format)
	}
	if content == "" && callback.MsgType == "image" && callback.Image.MediaURL != "" {
		content = channel.processImageMessage(callback.Image.MediaURL)
	}
	if content == "" {
		return
	}
	chatID := callback.chatID()
	if chatID == "" {
		chatID = callback.From.UserID
	}
	chatType := "c2c"
	if callback.ChatType == "group" {
		chatType = "group"
	}
	incoming := WeComMessage{
		ChatID: chatID, UserID: callback.From.UserID, MessageID: callback.MsgID,
		ResponseMsgID: callback.ResponseMsgID, Content: content, ChatType: chatType,
		RequestID: response.Headers["req_id"],
	}
	if channel.messageHandler == nil {
		_ = channel.SendStreamReply(incoming, "收到消息: "+content)
		return
	}
	channel.messageHandler(incoming)
}

func (channel *WeWorkChannel) handleEventCallback(response *wecomResponseFrame) {
	body, err := json.Marshal(response.Body)
	if err != nil {
		return
	}
	var event wecomEventCallback
	if err := json.Unmarshal(body, &event); err != nil {
		return
	}
	if event.Event.EventType == "enter_chat" {
		_ = channel.sendWelcomeMessage(event.ChatID, "欢迎使用智能助手！有什么可以帮助你的吗？")
	}
}

func (channel *WeWorkChannel) replyMessageID(message WeComMessage) string {
	if message.ResponseMsgID != "" {
		return message.ResponseMsgID
	}
	return message.MessageID
}

func (channel *WeWorkChannel) SendStreamReply(message WeComMessage, content string) error {
	streamID := fmt.Sprintf("stream_%d_%s", time.Now().UnixNano(), message.MessageID)
	if err := channel.StartStreamReply(message, streamID, "正在思考中..."); err != nil {
		return err
	}
	time.Sleep(100 * time.Millisecond)
	return channel.FinishStreamReply(message, streamID, content)
}

func (channel *WeWorkChannel) StartStreamReply(message WeComMessage, streamID, content string) error {
	return channel.sendStreamFrame(message, streamID, content, false)
}

func (channel *WeWorkChannel) FinishStreamReply(message WeComMessage, streamID, content string) error {
	return channel.sendStreamFrame(message, streamID, content, true)
}

func (channel *WeWorkChannel) sendStreamFrame(message WeComMessage, streamID, content string, finish bool) error {
	body := map[string]interface{}{
		"msgid": channel.replyMessageID(message), "msgtype": "stream",
		"stream": map[string]interface{}{"id": streamID, "finish": finish, "content": content},
	}
	if message.ChatID != "" {
		body["chatid"] = message.ChatID
	}
	return channel.sendFrame(wecomRequestFrame{
		Cmd: "aibot_respond_msg", Headers: map[string]string{"req_id": message.RequestID}, Body: body,
	})
}

func (channel *WeWorkChannel) SendTextReply(message WeComMessage, content string) error {
	body := map[string]interface{}{
		"msgid": channel.replyMessageID(message), "msgtype": "text",
		"text": map[string]string{"content": content},
	}
	if message.ChatID != "" {
		body["chatid"] = message.ChatID
	}
	return channel.sendFrame(wecomRequestFrame{
		Cmd: "aibot_respond_msg", Headers: map[string]string{"req_id": message.RequestID}, Body: body,
	})
}

func (channel *WeWorkChannel) sendWelcomeMessage(chatID, content string) error {
	return channel.sendFrame(wecomRequestFrame{
		Cmd:     "aibot_respond_welcome_msg",
		Headers: map[string]string{"req_id": channel.generateRequestID()},
		Body: map[string]interface{}{
			"chatid": chatID, "msgtype": "text", "text": map[string]string{"content": content},
		},
	})
}

func (channel *WeWorkChannel) heartbeatLoop(connection *websocket.Conn) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-channel.ctx.Done():
			return
		case <-ticker.C:
			channel.mu.RLock()
			current := channel.conn
			channel.mu.RUnlock()
			if current != connection {
				return
			}
			if err := channel.sendFrame(wecomRequestFrame{Cmd: "ping", Headers: map[string]string{"req_id": channel.generateRequestID()}}); err != nil {
				return
			}
		}
	}
}

func (channel *WeWorkChannel) cleanupProcessedIDs() {
	now := time.Now()
	channel.processedIDs.Range(func(key, value interface{}) bool {
		if processedAt, ok := value.(time.Time); ok && now.Sub(processedAt) > 10*time.Minute {
			channel.processedIDs.Delete(key)
		}
		return true
	})
}

func (channel *WeWorkChannel) generateRequestID() string {
	sequence := atomic.AddUint64(&channel.requestSeq, 1)
	return fmt.Sprintf("req_%d_%d", time.Now().UnixNano(), sequence)
}

func (channel *WeWorkChannel) Stop() error {
	if channel.cancel != nil {
		channel.cancel()
	}
	channel.mu.Lock()
	defer channel.mu.Unlock()
	if channel.conn != nil {
		return channel.conn.Close()
	}
	return nil
}

func (channel *WeWorkChannel) SendMessage(recipient, content string) error {
	return channel.sendFrame(wecomRequestFrame{
		Cmd:     "aibot_send_msg",
		Headers: map[string]string{"req_id": channel.generateRequestID()},
		Body: map[string]interface{}{
			"chatid": recipient, "chattype": "single", "msgtype": "text",
			"text": map[string]string{"content": content},
		},
	})
}

func (channel *WeWorkChannel) IsConnected() bool {
	select {
	case <-channel.started:
		channel.mu.RLock()
		defer channel.mu.RUnlock()
		return channel.conn != nil
	default:
		return false
	}
}

func (channel *WeWorkChannel) GetConnectionStatus() string {
	if channel.IsConnected() {
		return "已启动（长连接）"
	}
	select {
	case <-channel.started:
		return "已启动（等待连接）"
	default:
		return "启动中"
	}
}

func (channel *WeWorkChannel) processVoiceMessage(mediaURL, format string) string {
	result, err := channel.mediaHandler.DownloadAndProcess(mediaURL, "voice."+format, "audio/"+format)
	if err != nil {
		return fmt.Sprintf("[语音] 处理失败: %v", err)
	}
	return result
}

func (channel *WeWorkChannel) processImageMessage(mediaURL string) string {
	result, err := channel.mediaHandler.DownloadAndProcess(mediaURL, "image.jpg", "image/jpeg")
	if err != nil {
		return fmt.Sprintf("[图片] 处理失败: %v", err)
	}
	return result
}
