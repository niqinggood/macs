package channels

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/tencent-connect/botgo"
	"github.com/tencent-connect/botgo/dto"
	"github.com/tencent-connect/botgo/dto/keyboard"
	"github.com/tencent-connect/botgo/dto/message"
	"github.com/tencent-connect/botgo/event"
	"github.com/tencent-connect/botgo/openapi"
	"github.com/tencent-connect/botgo/token"
	"github.com/tencent-connect/botgo/websocket"
	"golang.org/x/oauth2"
)

type QQChannelConfig struct {
	AppID     string `json:"app_id" yaml:"app_id"`
	AppSecret string `json:"app_secret" yaml:"app_secret"`
}

// QQMessage removes BotGo DTOs from the application-facing message boundary.
type QQMessage struct {
	ChatID    string
	UserID    string
	MessageID string
	Content   string
	ChatType  string
}

// QQChannel owns BotGo setup, event decoding, attachments, approval keyboards
// and outbound API calls. Gateway orchestration is injected with callbacks.
type QQChannel struct {
	*Base
	config          QQChannelConfig
	api             openapi.OpenAPI
	tokenSource     oauth2.TokenSource
	ctx             context.Context
	cancel          context.CancelFunc
	started         chan struct{}
	startedOnce     sync.Once
	mu              sync.Mutex
	processedIDs    sync.Map
	msgSeq          uint32
	voiceHandler    *ChannelVoiceHandler
	messageHandler  func(QQMessage)
	resolveApproval func(id, decision string) (*ApprovalRequest, bool)
}

func NewQQChannel(name string, config QQChannelConfig) *QQChannel {
	ctx, cancel := context.WithCancel(context.Background())
	return &QQChannel{
		Base:    NewBase(name, "qq"),
		config:  config,
		ctx:     ctx,
		cancel:  cancel,
		started: make(chan struct{}),
	}
}

func (channel *QQChannel) SetMessageHandler(handler func(QQMessage)) {
	channel.messageHandler = handler
}

func (channel *QQChannel) SetApprovalResolver(resolver func(id, decision string) (*ApprovalRequest, bool)) {
	channel.resolveApproval = resolver
}

func (channel *QQChannel) Start() error {
	credentials := &token.QQBotCredentials{AppID: channel.config.AppID, AppSecret: channel.config.AppSecret}
	channel.tokenSource = token.NewQQBotTokenSource(credentials)
	if err := token.StartRefreshAccessToken(channel.ctx, channel.tokenSource); err != nil {
		return fmt.Errorf("start QQ token refresh: %w", err)
	}
	channel.api = botgo.NewOpenAPI(channel.config.AppID, channel.tokenSource).WithTimeout(10 * time.Second)
	wsInfo, err := channel.api.WS(channel.ctx, nil, "")
	if err != nil {
		return fmt.Errorf("get QQ websocket endpoint: %w", err)
	}
	intent := websocket.RegisterHandlers(
		channel.c2cMessageHandler(),
		channel.groupMessageHandler(),
		channel.channelMessageHandler(),
		channel.interactionHandler(),
	)
	go func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				channel.logger.Printf("QQ websocket panic: %v", recovered)
			}
		}()
		if err := botgo.NewSessionManager().Start(wsInfo, channel.tokenSource, &intent); err != nil {
			channel.logger.Printf("QQ websocket failed: %v", err)
		}
	}()
	time.Sleep(3 * time.Second)
	channel.startedOnce.Do(func() { close(channel.started) })
	return nil
}

func (channel *QQChannel) c2cMessageHandler() event.C2CMessageEventHandler {
	return func(_ *dto.WSPayload, data *dto.WSC2CMessageData) error {
		if data == nil || channel.isDuplicate(data.ID) {
			return nil
		}
		content := strings.TrimSpace(data.Content)
		if content != "" {
			channel.deliver(QQMessage{ChatID: data.Author.ID, UserID: data.Author.ID, MessageID: data.ID, Content: content, ChatType: "c2c"})
		}
		return nil
	}
}

func (channel *QQChannel) groupMessageHandler() event.GroupATMessageEventHandler {
	return func(_ *dto.WSPayload, data *dto.WSGroupATMessageData) error {
		if data == nil || channel.isDuplicate(data.ID) {
			return nil
		}
		content := channel.contentWithAttachments(data.Content, data.Attachments)
		if content != "" {
			channel.deliver(QQMessage{ChatID: data.GroupID, UserID: data.Author.ID, MessageID: data.ID, Content: message.ETLInput(content), ChatType: "group"})
		}
		return nil
	}
}

func (channel *QQChannel) channelMessageHandler() event.ATMessageEventHandler {
	return func(_ *dto.WSPayload, data *dto.WSATMessageData) error {
		if data == nil || channel.isDuplicate(data.ID) {
			return nil
		}
		content := channel.contentWithAttachments(data.Content, data.Attachments)
		if content != "" {
			channel.deliver(QQMessage{ChatID: data.ChannelID, UserID: data.Author.ID, MessageID: data.ID, Content: message.ETLInput(content), ChatType: "channel"})
		}
		return nil
	}
}

func (channel *QQChannel) deliver(incoming QQMessage) {
	if channel.messageHandler == nil {
		_ = channel.SendChatMessage(incoming.ChatID, incoming.MessageID, "收到消息: "+incoming.Content, incoming.ChatType)
		return
	}
	channel.messageHandler(incoming)
}

func (channel *QQChannel) isDuplicate(messageID string) bool {
	if _, duplicate := channel.processedIDs.Load(messageID); duplicate {
		return true
	}
	channel.processedIDs.Store(messageID, time.Now())
	go channel.cleanupProcessedIDs()
	return false
}

func (channel *QQChannel) contentWithAttachments(raw string, attachments []*dto.MessageAttachment) string {
	content := strings.TrimSpace(raw)
	for _, attachment := range attachments {
		if attachment == nil {
			continue
		}
		if attachment.ContentType == "voice" || strings.HasPrefix(attachment.ContentType, "audio/") {
			return channel.processVoiceAttachment(attachment.URL, attachment.ContentType)
		}
		if attachment.URL == "" {
			continue
		}
		savedPath, err := channel.downloadAttachment(attachment.URL, attachment.FileName, attachment.ContentType)
		if err != nil {
			channel.logger.Printf("QQ attachment download failed: %v", err)
			continue
		}
		if content == "" {
			content = fmt.Sprintf("[用户发送了文件: %s (%s)，已保存到 %s]", attachment.FileName, attachment.ContentType, savedPath)
		} else {
			content += fmt.Sprintf("\n[附件: %s -> %s]", attachment.FileName, savedPath)
		}
	}
	return content
}

func (channel *QQChannel) interactionHandler() event.InteractionEventHandler {
	return func(_ *dto.WSPayload, data *dto.WSInteractionData) error {
		if data == nil || data.Data == nil || data.Data.Type != dto.InteractionDataTypeInlineKeyboardClick {
			return nil
		}
		var resolved dto.Resolved
		if len(data.Data.Resolved) > 0 {
			if err := json.Unmarshal(data.Data.Resolved, &resolved); err != nil {
				channel.logger.Printf("QQ interaction parse failed: %v", err)
				return nil
			}
		}
		decision, approvalID, ok := ParseQQApprovalButtonData(resolved.ButtonData)
		if !ok || channel.resolveApproval == nil {
			return nil
		}
		request, resolvedOK := channel.resolveApproval(approvalID, decision)
		channel.ackInteraction(data.ID, resolvedOK)
		if !resolvedOK {
			recipient, chatType := qqInteractionTarget(data, nil)
			if recipient != "" {
				_ = channel.SendChatMessage(recipient, "", "Codex approval request is already resolved or expired.", chatType)
			}
			return nil
		}
		recipient, chatType := qqInteractionTarget(data, request)
		if recipient == "" {
			return nil
		}
		label := "approved"
		if decision == "decline" {
			label = "rejected"
		} else if decision == "acceptForSession" {
			label = "approved for this session"
		}
		return channel.SendChatMessage(recipient, "", fmt.Sprintf("Codex approval %s: %s.", label, request.ID), chatType)
	}
}

func (channel *QQChannel) ackInteraction(interactionID string, ok bool) {
	if channel.api == nil || strings.TrimSpace(interactionID) == "" {
		return
	}
	code := 0
	if !ok {
		code = 1
	}
	if err := channel.api.PutInteraction(context.Background(), interactionID, fmt.Sprintf(`{"code":%d}`, code)); err != nil {
		channel.logger.Printf("QQ interaction acknowledgement failed: %v", err)
	}
}

func qqInteractionTarget(data *dto.WSInteractionData, request *ApprovalRequest) (string, string) {
	recipient, chatType := "", ""
	if request != nil {
		recipient = strings.TrimSpace(request.Recipient)
		chatType = QQChatTypeFromSessionID(request.SessionID)
	}
	if data != nil {
		if chatType == "" {
			switch strings.ToLower(strings.TrimSpace(data.Scene)) {
			case "group":
				chatType = "group"
			case "c2c":
				chatType = "c2c"
			case "guild", "channel":
				chatType = "channel"
			}
		}
		if chatType == "" {
			switch data.ChatType {
			case 1:
				chatType = "group"
			case 2:
				chatType = "c2c"
			default:
				chatType = "channel"
			}
		}
		if recipient == "" {
			switch chatType {
			case "group":
				recipient = strings.TrimSpace(data.GroupOpenID)
			case "c2c":
				recipient = strings.TrimSpace(data.UserOpenID)
			default:
				recipient = strings.TrimSpace(data.ChannelID)
			}
		}
	}
	if chatType == "" {
		chatType = "c2c"
	}
	return recipient, chatType
}

func QQChatTypeFromSessionID(sessionID string) string {
	switch {
	case strings.HasPrefix(sessionID, "qq_group_"):
		return "group"
	case strings.HasPrefix(sessionID, "qq_c2c_"):
		return "c2c"
	case strings.HasPrefix(sessionID, "qq_channel_"):
		return "channel"
	default:
		return ""
	}
}

func normalizeQQApprovalDecision(decision string) string {
	switch strings.ToLower(strings.TrimSpace(decision)) {
	case "accept", "approve", "approved", "once":
		return "accept"
	case "acceptforsession", "session", "always":
		return "acceptForSession"
	case "decline", "deny", "reject", "rejected", "cancel":
		return "decline"
	default:
		return ""
	}
}

func QQApprovalButtonData(approvalID, decision string) string {
	return "codex_approval|" + approvalID + "|" + normalizeQQApprovalDecision(decision)
}

func ParseQQApprovalButtonData(data string) (decision, approvalID string, ok bool) {
	parts := strings.Split(strings.TrimSpace(data), "|")
	if len(parts) != 3 || parts[0] != "codex_approval" {
		return "", "", false
	}
	approvalID = strings.TrimSpace(parts[1])
	decision = normalizeQQApprovalDecision(parts[2])
	return decision, approvalID, approvalID != "" && decision != ""
}

func FormatQQApprovalText(request *ApprovalRequest) string {
	if request == nil {
		return "Codex approval requested."
	}
	var text strings.Builder
	text.WriteString("Codex approval requested\nID: ")
	text.WriteString(request.ID)
	if request.Kind != "" {
		text.WriteString("\nType: ")
		text.WriteString(request.Kind)
	}
	if request.CWD != "" {
		text.WriteString("\nCWD: ")
		text.WriteString(request.CWD)
	}
	if request.ShellAnalysis != nil {
		text.WriteString("\n\nShell analysis:\n- Class: ")
		text.WriteString(request.ShellAnalysis.Class)
		text.WriteString("\n- Risk: ")
		text.WriteString(request.ShellAnalysis.Risk)
		text.WriteString(fmt.Sprintf("\n- Blocked: %v", request.ShellAnalysis.Blocked))
		if request.ShellAnalysis.BlockReason != "" {
			text.WriteString("\n- Reason: ")
			text.WriteString(request.ShellAnalysis.BlockReason)
		}
		if request.ShellAnalysis.SuggestedRule != "" {
			text.WriteString("\n- Suggested rule: ")
			text.WriteString(request.ShellAnalysis.SuggestedRule)
		}
	}
	if request.Summary != "" {
		text.WriteString("\n\n")
		text.WriteString(truncateLog(request.Summary, 1200))
	}
	text.WriteString("\n\nUse the buttons below, or reply /approve ")
	text.WriteString(request.ID)
	text.WriteString(" / /reject ")
	text.WriteString(request.ID)
	return text.String()
}

func BuildQQApprovalKeyboard(request *ApprovalRequest) *keyboard.MessageKeyboard {
	if request == nil {
		return nil
	}
	return &keyboard.MessageKeyboard{Content: &keyboard.CustomKeyboard{
		Style: &keyboard.KeyboardStyle{FontSize: "middle"},
		Rows: []*keyboard.Row{{Buttons: []*keyboard.Button{
			qqApprovalButton("codex_approve_"+request.ID, "Approve", "Approved", 4, request.ID, request.ID, "accept"),
			qqApprovalButton("codex_reject_"+request.ID, "Reject", "Rejected", 3, request.ID, request.ID, "decline"),
		}}},
	}}
}

func qqApprovalButton(id, label, visitedLabel string, style int, groupID, approvalID, decision string) *keyboard.Button {
	return &keyboard.Button{
		ID: id, GroupID: groupID,
		RenderData: &keyboard.RenderData{Label: label, VisitedLabel: visitedLabel, Style: style},
		Action: &keyboard.Action{
			Type: keyboard.ActionTypeCallback, ClickLimit: 1,
			Permission: &keyboard.Permission{Type: keyboard.PermissionTypAll},
			Data:       QQApprovalButtonData(approvalID, decision),
		},
	}
}

func (channel *QQChannel) SendApprovalCard(recipient string, request *ApprovalRequest) error {
	if request == nil {
		return fmt.Errorf("empty QQ approval request")
	}
	if strings.TrimSpace(recipient) == "" {
		return fmt.Errorf("empty QQ recipient")
	}
	chatType := QQChatTypeFromSessionID(request.SessionID)
	if chatType == "" {
		chatType = "c2c"
	}
	return channel.postMessage(recipient, chatType, &dto.MessageToCreate{
		Content: FormatQQApprovalText(request), MsgType: dto.TextMsg,
		Keyboard: BuildQQApprovalKeyboard(request), MsgSeq: channel.nextMsgSeq(),
	})
}

func (channel *QQChannel) nextMsgSeq() uint32 {
	channel.mu.Lock()
	defer channel.mu.Unlock()
	channel.msgSeq++
	return channel.msgSeq
}

func (channel *QQChannel) postMessage(chatID, chatType string, outbound *dto.MessageToCreate) error {
	if channel.api == nil {
		return fmt.Errorf("QQ API is not initialized")
	}
	switch chatType {
	case "group":
		_, err := channel.api.PostGroupMessage(context.Background(), chatID, outbound)
		return err
	case "c2c":
		_, err := channel.api.PostC2CMessage(context.Background(), chatID, outbound)
		return err
	default:
		_, err := channel.api.PostMessage(context.Background(), chatID, outbound)
		return err
	}
}

func (channel *QQChannel) SendChatMessage(chatID, messageID, content, chatType string) error {
	if strings.TrimSpace(chatID) == "" {
		return fmt.Errorf("empty QQ chat ID")
	}
	return channel.postMessage(chatID, chatType, &dto.MessageToCreate{
		Content: content, MsgID: messageID, MsgSeq: channel.nextMsgSeq(),
	})
}

func (channel *QQChannel) SendMessage(recipient, content string) error {
	return channel.SendChatMessage(recipient, "", content, "c2c")
}

func (channel *QQChannel) cleanupProcessedIDs() {
	now := time.Now()
	channel.processedIDs.Range(func(key, value interface{}) bool {
		if processedAt, ok := value.(time.Time); ok && now.Sub(processedAt) > 10*time.Minute {
			channel.processedIDs.Delete(key)
		}
		return true
	})
}

func (channel *QQChannel) Stop() error {
	if channel.cancel != nil {
		channel.cancel()
	}
	return nil
}

func (channel *QQChannel) IsConnected() bool {
	select {
	case <-channel.started:
		return true
	default:
		return false
	}
}

func (channel *QQChannel) GetConnectionStatus() string {
	if channel.IsConnected() {
		return "已启动（WebSocket连接）"
	}
	return "启动中"
}

func (channel *QQChannel) processVoiceAttachment(mediaURL, contentType string) string {
	if channel.voiceHandler == nil {
		channel.voiceHandler = NewChannelVoiceHandler("qq_media", channel.logger, nil)
	}
	channel.logger.Printf("QQ voice attachment: type=%s", contentType)
	return channel.voiceHandler.ProcessURL(mediaURL, "qq_voice.amr")
}

func (channel *QQChannel) downloadAttachment(mediaURL, fileName, contentType string) (string, error) {
	if mediaURL == "" {
		return "", fmt.Errorf("empty attachment URL")
	}
	if fileName == "" {
		fileName = fmt.Sprintf("qq_attachment_%d", time.Now().UnixNano())
	}
	saveDir := filepath.Join("storage", "qq_attachments")
	if err := os.MkdirAll(saveDir, 0755); err != nil {
		return "", err
	}
	response, err := http.Get(mediaURL)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("attachment HTTP status %d", response.StatusCode)
	}
	savePath := filepath.Join(saveDir, filepath.Base(fileName))
	file, err := os.Create(savePath)
	if err != nil {
		return "", err
	}
	defer file.Close()
	if _, err := io.Copy(file, response.Body); err != nil {
		return "", err
	}
	channel.logger.Printf("QQ attachment saved: %s (%s)", savePath, contentType)
	return savePath, nil
}

func (channel *QQChannel) InitVoiceHandler(workspace string, resolver BinaryResolver) {
	channel.voiceHandler = NewChannelVoiceHandler(workspace, channel.logger, resolver)
}
