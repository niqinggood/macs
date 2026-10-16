package channels

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	lark "github.com/larksuite/oapi-sdk-go/v3"
	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher"
	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher/callback"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	larkws "github.com/larksuite/oapi-sdk-go/v3/ws"
)

type FeishuChannel struct {
	*Base
	config          FeishuChannelConfig
	client          *lark.Client
	wsClient        *larkws.Client
	ctx             context.Context
	cancel          context.CancelFunc
	started         chan struct{}
	startedOnce     sync.Once
	mediaHandler    *ChannelMediaHandler
	onMessage       func(chatID, message string)
	resolveApproval func(id, decision string) (*ApprovalRequest, bool)
}

type FeishuChannelConfig struct {
	AppID     string `json:"app_id" yaml:"app_id"`
	AppSecret string `json:"app_secret" yaml:"app_secret"`
}

func NewFeishuChannel(name string, config FeishuChannelConfig, workspace string) *FeishuChannel {
	log.Printf("创建飞书渠道: %s, AppID: %s", name, config.AppID)
	ctx, cancel := context.WithCancel(context.Background())
	return &FeishuChannel{
		Base:         NewBase(name, "feishu"),
		config:       config,
		ctx:          ctx,
		cancel:       cancel,
		started:      make(chan struct{}),
		mediaHandler: NewChannelMediaHandler(workspace, log.Default(), nil),
	}
}

func (c *FeishuChannel) SetMessageHandler(handler func(chatID, message string)) {
	c.onMessage = handler
}

func (c *FeishuChannel) SetApprovalResolver(resolver func(id, decision string) (*ApprovalRequest, bool)) {
	c.resolveApproval = resolver
}

func (c *FeishuChannel) Start() error {
	c.logger.Println("========== 飞书渠道启动 ==========")
	c.logger.Printf("AppID: %s", c.config.AppID)

	// 创建 LarkClient 对象
	c.client = lark.NewClient(c.config.AppID, c.config.AppSecret)

	// 注册事件处理器
	eventHandler := dispatcher.NewEventDispatcher("", "").
		OnP2MessageReceiveV1(func(ctx context.Context, event *larkim.P2MessageReceiveV1) error {
			c.logger.Printf("[OnP2MessageReceiveV1] 收到消息事件")
			// 打印完整事件用于调试
			eventJSON, _ := json.Marshal(event)
			c.logger.Printf("事件详情: %s", string(eventJSON))
			c.handleMessage(event)
			return nil
		}).
		OnP2CardActionTrigger(func(ctx context.Context, event *callback.CardActionTriggerEvent) (*callback.CardActionTriggerResponse, error) {
			return c.handleCardAction(ctx, event)
		})

	// 创建长连接客户端
	c.wsClient = larkws.NewClient(c.config.AppID, c.config.AppSecret,
		larkws.WithEventHandler(eventHandler),
		larkws.WithLogLevel(larkcore.LogLevelDebug),
	)

	// 启动长连接
	go func() {
		defer func() {
			if r := recover(); r != nil {
				c.logger.Printf("飞书长连接发生 panic: %v", r)
			}
		}()

		c.logger.Println("启动飞书长连接...")

		c.startedOnce.Do(func() {
			close(c.started)
		})

		// 长连接主循环，异常退出后自动重连
		backoff := 1 * time.Second
		maxBackoff := 5 * time.Minute
		for {
			if c.ctx.Err() != nil {
				return
			}

			c.logger.Printf("飞书长连接建立中 (重试间隔 %v)...", backoff)
			ctx := context.Background()
			err := c.wsClient.Start(ctx)
			if err != nil {
				c.logger.Printf("飞书长连接异常退出[app_id:%s]: %v", c.config.AppID, err)
			} else {
				c.logger.Println("飞书长连接正常退出")
			}

			if c.ctx.Err() != nil {
				return
			}
			c.logger.Printf("飞书长连接 %v 后重连...", backoff)
			time.Sleep(backoff)
			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
		}
	}()

	// 等待连接建立
	time.Sleep(3 * time.Second)
	c.logger.Println("飞书渠道启动完成")
	c.logger.Println("========== 启动完成 ==========")

	return nil
}

// handleMessage 处理消息
func (c *FeishuChannel) handleMessage(event *larkim.P2MessageReceiveV1) {
	// 检查必要字段
	if event == nil || event.Event == nil || event.Event.Message == nil {
		c.logger.Printf("事件或消息对象为空")
		return
	}

	message := event.Event.Message

	// 打印消息的关键信息
	c.logger.Printf("消息类型: %v", message.MessageType)
	c.logger.Printf("聊天类型: %v", message.ChatType)
	c.logger.Printf("消息ID: %v", message.MessageId)
	c.logger.Printf("聊天ID: %v", message.ChatId)
	c.logger.Printf("消息内容: %v", message.Content)

	// 获取消息内容 — 支持 text, image, file, audio
	var userMessage string
	msgType := ""
	if message.MessageType != nil {
		msgType = *message.MessageType
	}

	switch msgType {
	case "text":
		if message.Content != nil && *message.Content != "" {
			var contentMap map[string]interface{}
			if err := json.Unmarshal([]byte(*message.Content), &contentMap); err != nil {
				c.logger.Printf("解析消息内容失败: %v", err)
				userMessage = "解析消息失败"
			} else if text, ok := contentMap["text"].(string); ok {
				userMessage = text
				c.logger.Printf("提取到的用户消息: %s", userMessage)
			}
		}

	case "image":
		userMessage = c.handleImageMessage(message)

	case "file":
		userMessage = c.handleFileMessage(message)

	case "audio":
		userMessage = c.handleAudioMessage(message)

	default:
		userMessage = fmt.Sprintf("[%s消息] 暂不支持此类型，请发送文本、图片、文件或语音", msgType)
		c.logger.Printf("不支持的消息类型: %s", msgType)
	}

	if userMessage == "" {
		userMessage = "无法解析消息内容"
	}

	// 获取必要信息
	var chatID, messageID string
	if message.ChatId != nil {
		chatID = *message.ChatId
		c.logger.Printf("聊天ID: %s", chatID)
	}
	if message.MessageId != nil {
		messageID = *message.MessageId
		c.logger.Printf("消息ID: %s", messageID)
	}

	if chatID == "" {
		c.logger.Printf("chatID 为空，无法处理消息: messageID=%s", messageID)
		return
	}
	if c.onMessage != nil {
		c.onMessage(chatID, userMessage)
	}
}

// handleImageMessage downloads and processes an image message.
func (c *FeishuChannel) handleImageMessage(message *larkim.EventMessage) string {
	if message.Content == nil {
		return "[图片] 无法获取图片内容"
	}
	var contentMap map[string]interface{}
	if err := json.Unmarshal([]byte(*message.Content), &contentMap); err != nil {
		return fmt.Sprintf("[图片] 解析失败: %v", err)
	}
	imageKey, _ := contentMap["image_key"].(string)
	if imageKey == "" {
		return "[图片] 缺少 image_key"
	}

	// Download image via Lark SDK
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	resp, err := c.client.Im.Image.Get(ctx,
		larkim.NewGetImageReqBuilder().ImageKey(imageKey).Build())
	if err != nil {
		c.logger.Printf("下载图片失败: %v", err)
		return fmt.Sprintf("[图片] 下载失败: %v", err)
	}
	if !resp.Success() {
		return fmt.Sprintf("[图片] API错误: code=%d, msg=%s", resp.Code, resp.Msg)
	}

	// Process with media handler
	result, err := c.mediaHandler.DownloadAndProcess(
		fmt.Sprintf("https://open.feishu.cn/open-apis/im/v1/images/%s", imageKey),
		fmt.Sprintf("feishu_image_%s", imageKey),
		"image/jpeg",
	)
	if err != nil {
		return fmt.Sprintf("[图片] 处理失败: %v", err)
	}
	return result
}

// handleFileMessage downloads and extracts content from a file message.
func (c *FeishuChannel) handleFileMessage(message *larkim.EventMessage) string {
	if message.Content == nil {
		return "[文件] 无法获取文件内容"
	}
	var contentMap map[string]interface{}
	if err := json.Unmarshal([]byte(*message.Content), &contentMap); err != nil {
		return fmt.Sprintf("[文件] 解析失败: %v", err)
	}
	fileKey, _ := contentMap["file_key"].(string)
	fileName, _ := contentMap["file_name"].(string)
	if fileKey == "" {
		return "[文件] 缺少 file_key"
	}
	if fileName == "" {
		fileName = "unknown_file"
	}

	// Download file via Lark SDK
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	resp, err := c.client.Im.File.Get(ctx,
		larkim.NewGetFileReqBuilder().FileKey(fileKey).Build())
	if err != nil {
		c.logger.Printf("下载文件失败: %v", err)
		return fmt.Sprintf("[文件] 下载失败: %v", err)
	}
	if !resp.Success() {
		return fmt.Sprintf("[文件] API错误: code=%d, msg=%s", resp.Code, resp.Msg)
	}

	// Process with media handler
	result, err := c.mediaHandler.DownloadAndProcess(
		fmt.Sprintf("https://open.feishu.cn/open-apis/im/v1/files/%s", fileKey),
		fileName,
		"application/octet-stream",
	)
	if err != nil {
		return fmt.Sprintf("[文件] 处理失败: %v", err)
	}
	return result
}

// handleAudioMessage downloads and transcribes a voice/audio message.
func (c *FeishuChannel) handleAudioMessage(message *larkim.EventMessage) string {
	if message.Content == nil {
		return "[语音] 无法获取语音内容"
	}
	var contentMap map[string]interface{}
	if err := json.Unmarshal([]byte(*message.Content), &contentMap); err != nil {
		return fmt.Sprintf("[语音] 解析失败: %v", err)
	}
	fileKey, _ := contentMap["file_key"].(string)
	if fileKey == "" {
		return "[语音] 缺少 file_key"
	}

	// Download audio via Lark SDK
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	resp, err := c.client.Im.File.Get(ctx,
		larkim.NewGetFileReqBuilder().FileKey(fileKey).Build())
	if err != nil {
		c.logger.Printf("下载语音失败: %v", err)
		return fmt.Sprintf("[语音] 下载失败: %v", err)
	}
	if !resp.Success() {
		return fmt.Sprintf("[语音] API错误: code=%d, msg=%s", resp.Code, resp.Msg)
	}

	// Process with media handler (transcribe)
	result, err := c.mediaHandler.DownloadAndProcess(
		fmt.Sprintf("https://open.feishu.cn/open-apis/im/v1/files/%s", fileKey),
		fmt.Sprintf("feishu_audio_%s.amr", fileKey),
		"audio/amr",
	)
	if err != nil {
		return fmt.Sprintf("[语音] 转写失败: %v", err)
	}
	return result
}

// hasMarkdown checks if content contains markdown formatting
func hasMarkdown(content string) bool {
	if len(content) < 10 {
		return false
	}
	indicators := []string{"**", "#", "```", "|", "- ", "1. ", "![", "["}
	for _, ind := range indicators {
		if strings.Contains(content, ind) {
			return true
		}
	}
	return false
}

// truncateForCard trims content to fit Feishu card markdown limit (~30KB)
func truncateForCard(content string, maxLen int) string {
	if len(content) <= maxLen {
		return content
	}
	return content[:maxLen] + "\n\n*(内容过长已截断)*"
}

// feishuCard v2 schema for interactive card
type feishuCard struct {
	Schema string           `json:"schema"`
	Config feishuCardConfig `json:"config"`
	Body   feishuCardBody   `json:"body"`
}
type feishuCardConfig struct {
	WideScreenMode bool `json:"wide_screen_mode"`
}
type feishuCardBody struct {
	Elements []feishuCardElement `json:"elements"`
}
type feishuCardElement struct {
	Tag     string `json:"tag"`
	Content string `json:"content"`
}

// sendInteractiveCard sends a markdown-formatted message as a Feishu interactive card
func (c *FeishuChannel) sendInteractiveCard(chatID, content string) {
	const maxCardLen = 28000
	if len(content) > maxCardLen {
		content = truncateForCard(content, maxCardLen)
	}

	card := feishuCard{
		Schema: "2.0",
		Config: feishuCardConfig{WideScreenMode: true},
		Body: feishuCardBody{
			Elements: []feishuCardElement{
				{Tag: "markdown", Content: content},
			},
		},
	}
	cardBytes, _ := json.Marshal(card)
	contentStr := string(cardBytes)

	c.logger.Printf("发送交互卡片, len=%d", len(contentStr))

	resp, err := c.client.Im.Message.Create(context.Background(),
		larkim.NewCreateMessageReqBuilder().
			ReceiveIdType(larkim.ReceiveIdTypeChatId).
			Body(larkim.NewCreateMessageReqBodyBuilder().
				MsgType(larkim.MsgTypeInteractive).
				ReceiveId(chatID).
				Content(contentStr).
				Build()).
			Build())

	if err != nil {
		c.logger.Printf("发送交互卡片失败: %v, 回退到文本消息", err)
		c.sendPlainText(chatID, content)
	} else if !resp.Success() {
		c.logger.Printf("发送交互卡片失败: code=%d, msg=%s, 回退到文本消息", resp.Code, resp.Msg)
		c.sendPlainText(chatID, content)
	} else {
		c.logger.Printf("交互卡片发送成功")
	}
}

type ApprovalRequest struct {
	ID            string
	SessionID     string
	Recipient     string
	Kind          string
	Summary       string
	Command       string
	CWD           string
	ShellAnalysis *ApprovalShellAnalysis
}

type ApprovalShellAnalysis struct {
	Class         string
	Risk          string
	Blocked       bool
	BlockReason   string
	SuggestedRule string
}

func (c *FeishuChannel) SendApprovalCard(recipient string, req *ApprovalRequest) error {
	if c == nil || c.client == nil {
		return fmt.Errorf("feishu client is not initialized")
	}
	if strings.TrimSpace(recipient) == "" {
		return fmt.Errorf("empty feishu recipient")
	}
	if req == nil {
		return fmt.Errorf("empty codex approval request")
	}
	return c.sendFeishuCardPayload(recipient, buildFeishuApprovalCard(req))
}

func (c *FeishuChannel) sendFeishuCardPayload(chatID string, card interface{}) error {
	cardBytes, err := json.Marshal(card)
	if err != nil {
		return err
	}
	resp, err := c.client.Im.Message.Create(context.Background(),
		larkim.NewCreateMessageReqBuilder().
			ReceiveIdType(larkim.ReceiveIdTypeChatId).
			Body(larkim.NewCreateMessageReqBodyBuilder().
				MsgType(larkim.MsgTypeInteractive).
				ReceiveId(chatID).
				Content(string(cardBytes)).
				Build()).
			Build())
	if err != nil {
		return err
	}
	if !resp.Success() {
		return fmt.Errorf("feishu card send failed: code=%d msg=%s", resp.Code, resp.Msg)
	}
	return nil
}

func buildFeishuApprovalCard(req *ApprovalRequest) map[string]interface{} {
	return buildFeishuApprovalCardWithStatus(req, "")
}

func BuildFeishuApprovalCard(req *ApprovalRequest) map[string]interface{} {
	return buildFeishuApprovalCard(req)
}

func buildFeishuApprovalResolvedCard(req *ApprovalRequest, decision string) map[string]interface{} {
	return buildFeishuApprovalCardWithStatus(req, decision)
}

func buildFeishuApprovalCardWithStatus(req *ApprovalRequest, decision string) map[string]interface{} {
	decision = normalizeApprovalDecision(decision)
	resolved := decision != ""
	template := "orange"
	title := "Codex 审批请求"
	if resolved {
		title = "Codex 审批已处理"
		if decision == "decline" {
			template = "red"
		} else {
			template = "green"
		}
	}
	elements := []interface{}{
		map[string]interface{}{
			"tag":     "markdown",
			"content": feishuApprovalMarkdown(req, decision),
		},
	}
	if !resolved && req != nil {
		elements = append(elements, map[string]interface{}{
			"tag":    "action",
			"layout": "bisected",
			"actions": []interface{}{
				feishuApprovalButton("同意", "primary", req.ID, "accept"),
				feishuApprovalButton("拒绝", "danger", req.ID, "decline"),
			},
		})
	}
	return map[string]interface{}{
		"schema": "2.0",
		"config": map[string]interface{}{
			"wide_screen_mode": true,
		},
		"header": map[string]interface{}{
			"template": template,
			"title": map[string]string{
				"tag":     "plain_text",
				"content": title,
			},
		},
		"body": map[string]interface{}{
			"elements": elements,
		},
	}
}

func feishuApprovalButton(label, buttonType, approvalID, decision string) map[string]interface{} {
	return map[string]interface{}{
		"tag":  "button",
		"type": buttonType,
		"text": map[string]string{
			"tag":     "plain_text",
			"content": label,
		},
		"value": map[string]string{
			"action":      "codex_approval",
			"approval_id": approvalID,
			"decision":    decision,
		},
	}
}

func normalizeApprovalDecision(decision string) string {
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

func feishuApprovalMarkdown(req *ApprovalRequest, decision string) string {
	if req == nil {
		return "Codex requested approval."
	}
	var b strings.Builder
	if decision != "" {
		label := "approved"
		if normalizeApprovalDecision(decision) == "decline" {
			label = "rejected"
		} else if normalizeApprovalDecision(decision) == "acceptForSession" {
			label = "approved for this session"
		}
		b.WriteString("**Status**: ")
		b.WriteString(label)
		b.WriteString("\n\n")
	}
	b.WriteString("**ID**: `")
	b.WriteString(req.ID)
	b.WriteString("`")
	if req.Kind != "" {
		b.WriteString("\n**Type**: ")
		b.WriteString(req.Kind)
	}
	if req.CWD != "" {
		b.WriteString("\n**CWD**: `")
		b.WriteString(req.CWD)
		b.WriteString("`")
	}
	if req.ShellAnalysis != nil {
		b.WriteString("\n\n**Shell analysis**")
		b.WriteString("\n- Class: `")
		b.WriteString(string(req.ShellAnalysis.Class))
		b.WriteString("`")
		b.WriteString("\n- Risk: `")
		b.WriteString(req.ShellAnalysis.Risk)
		b.WriteString("`")
		b.WriteString(fmt.Sprintf("\n- Blocked: `%v`", req.ShellAnalysis.Blocked))
		if req.ShellAnalysis.BlockReason != "" {
			b.WriteString("\n- Reason: ")
			b.WriteString(req.ShellAnalysis.BlockReason)
		}
		if req.ShellAnalysis.SuggestedRule != "" {
			b.WriteString("\n- Suggested rule: `")
			b.WriteString(req.ShellAnalysis.SuggestedRule)
			b.WriteString("`")
		}
	}
	if req.Summary != "" {
		b.WriteString("\n\n")
		b.WriteString(truncateForCard(req.Summary, 6000))
	}
	if decision == "" {
		b.WriteString("\n\nFallback: reply `/approve ")
		b.WriteString(req.ID)
		b.WriteString("` or `/reject ")
		b.WriteString(req.ID)
		b.WriteString("`.")
	}
	return b.String()
}

func (c *FeishuChannel) handleCardAction(ctx context.Context, event *callback.CardActionTriggerEvent) (*callback.CardActionTriggerResponse, error) {
	if event == nil || event.Event == nil || event.Event.Action == nil {
		return feishuApprovalToast("warning", "Unsupported card action"), nil
	}
	value := event.Event.Action.Value
	if mapString(value, "action", "") != "codex_approval" {
		return feishuApprovalToast("info", "Ignored"), nil
	}
	approvalID := mapString(value, "approval_id", "")
	decision := normalizeApprovalDecision(mapString(value, "decision", ""))
	if approvalID == "" || decision == "" {
		return feishuApprovalToast("warning", "Invalid approval action"), nil
	}
	if c.resolveApproval == nil {
		return feishuApprovalToast("warning", "Approval resolver is unavailable"), nil
	}
	req, ok := c.resolveApproval(approvalID, decision)
	if !ok {
		return feishuApprovalToast("warning", "Approval request is already resolved or expired"), nil
	}
	c.logger.Printf("[CodexApproval] Feishu card resolved: id=%s decision=%s", req.ID, decision)
	label := "Approved"
	if decision == "decline" {
		label = "Rejected"
	} else if decision == "acceptForSession" {
		label = "Approved for this session"
	}
	return &callback.CardActionTriggerResponse{
		Toast: &callback.Toast{Type: "success", Content: label},
		Card: &callback.Card{
			Type: "card_json",
			Data: buildFeishuApprovalResolvedCard(req, decision),
		},
	}, nil
}

func feishuApprovalToast(toastType, content string) *callback.CardActionTriggerResponse {
	return &callback.CardActionTriggerResponse{
		Toast: &callback.Toast{Type: toastType, Content: content},
	}
}

// sendPlainText sends a plain text message (no markdown support)
func (c *FeishuChannel) sendPlainText(chatID, content string) {
	contentJSON, _ := json.Marshal(map[string]string{
		"text": content,
	})
	contentStr := string(contentJSON)

	resp, err := c.client.Im.Message.Create(context.Background(),
		larkim.NewCreateMessageReqBuilder().
			ReceiveIdType(larkim.ReceiveIdTypeChatId).
			Body(larkim.NewCreateMessageReqBodyBuilder().
				MsgType(larkim.MsgTypeText).
				ReceiveId(chatID).
				Content(contentStr).
				Build()).
			Build())

	if err != nil {
		c.logger.Printf("发送文本消息失败: %v", err)
	} else if !resp.Success() {
		c.logger.Printf("发送文本消息失败: code=%d, msg=%s", resp.Code, resp.Msg)
	} else {
		c.logger.Printf("文本消息发送成功")
	}
}

// sendGeneratedFiles scans AI response for file paths and sends them asynchronously
func (c *FeishuChannel) sendGeneratedFiles(chatID, response string) {
	// Feishu file size limit: 20MB
	const maxSize = 20 * 1024 * 1024
	// Max files to send in one response
	const maxFiles = 3

	re := regexp.MustCompile(`(?:storage/|output/|charts/)?[\w./-]+\.(pptx|pdf|docx|xlsx|csv|zip|html)`)
	matches := re.FindAllString(response, -1)
	seen := make(map[string]bool)
	sent := 0

	for _, filePath := range matches {
		if sent >= maxFiles {
			break
		}
		if seen[filePath] {
			continue
		}
		seen[filePath] = true

		// Try paths
		tryPaths := []string{filePath}
		if !filepath.IsAbs(filePath) {
			tryPaths = append(tryPaths, filepath.Join("storage", filePath))
		}

		for _, p := range tryPaths {
			info, err := os.Stat(p)
			if err != nil {
				continue
			}
			if info.IsDir() {
				continue
			}
			if info.Size() > maxSize {
				c.logger.Printf("文件过大跳过(%s): %d > %d", p, info.Size(), maxSize)
				c.recordError(chatID, fmt.Errorf("文件过大 %s: %d > %d", filepath.Base(p), info.Size(), maxSize))
				c.sendPlainText(chatID, fmt.Sprintf("文件 %s 超过飞书20MB限制，已跳过", filepath.Base(p)))
				continue
			}

			c.logger.Printf("发送文件: %s (%d bytes)", p, info.Size())
			if err := c.SendFile(chatID, p, ""); err != nil {
				c.logger.Printf("发送文件失败: %s, err=%v", p, err)
			} else {
				sent++
			}
			break
		}
	}
}

// sendSimpleMessage 发送消息，自动检测markdown并选择最佳格式
func (c *FeishuChannel) sendSimpleMessage(chatID, content string) {
	c.logger.Printf("发送消息: chatID=%s, len=%d", chatID, len(content))

	if chatID == "" {
		c.logger.Printf("chatID 为空，无法发送消息")
		return
	}

	if hasMarkdown(content) {
		c.sendInteractiveCard(chatID, content)
	} else {
		c.sendPlainText(chatID, content)
	}
}

// uploadFile uploads a file to Feishu and returns the file_key
func (c *FeishuChannel) uploadFile(filePath, fileType, fileName string) (string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", fmt.Errorf("打开文件失败: %w", err)
	}
	defer file.Close()

	if fileName == "" {
		fileName = filepath.Base(filePath)
	}
	if fileType == "" {
		fileType = "stream" // generic binary
	}

	resp, err := c.client.Im.File.Create(context.Background(),
		larkim.NewCreateFileReqBuilder().
			Body(larkim.NewCreateFileReqBodyBuilder().
				FileType(fileType).
				FileName(fileName).
				File(file).
				Build()).
			Build())

	if err != nil {
		return "", fmt.Errorf("上传文件失败: %w", err)
	}
	if !resp.Success() {
		return "", fmt.Errorf("上传文件失败: code=%d, msg=%s", resp.Code, resp.Msg)
	}
	if resp.Data == nil || resp.Data.FileKey == nil {
		return "", fmt.Errorf("上传文件成功未返回file_key")
	}
	return *resp.Data.FileKey, nil
}

// uploadImage uploads an image to Feishu and returns the image_key
func (c *FeishuChannel) uploadImage(imgPath string) (string, error) {
	file, err := os.Open(imgPath)
	if err != nil {
		return "", fmt.Errorf("打开图片失败: %w", err)
	}
	defer file.Close()

	resp, err := c.client.Im.Image.Create(context.Background(),
		larkim.NewCreateImageReqBuilder().
			Body(larkim.NewCreateImageReqBodyBuilder().
				ImageType("message").
				Image(file).
				Build()).
			Build())

	if err != nil {
		return "", fmt.Errorf("上传图片失败: %w", err)
	}
	if !resp.Success() {
		return "", fmt.Errorf("上传图片失败: code=%d, msg=%s", resp.Code, resp.Msg)
	}
	if resp.Data == nil || resp.Data.ImageKey == nil {
		return "", fmt.Errorf("上传图片成功未返回image_key")
	}
	return *resp.Data.ImageKey, nil
}

// SendFile uploads and sends a file to a Feishu chat
func (c *FeishuChannel) SendFile(chatID, filePath, fileType string) error {
	c.logger.Printf("发送文件: chatID=%s, path=%s", chatID, filePath)

	fileName := filepath.Base(filePath)
	ext := strings.ToLower(filepath.Ext(fileName))

	// Map extension to Feishu file type
	if fileType == "" {
		switch ext {
		case ".pdf":
			fileType = "pdf"
		case ".doc", ".docx":
			fileType = "doc"
		case ".xls", ".xlsx":
			fileType = "xls"
		case ".ppt", ".pptx":
			fileType = "ppt"
		case ".csv":
			fileType = "csv"
		case ".zip", ".gz", ".tar":
			fileType = "zip"
		case ".mp3", ".wav", ".amr":
			fileType = "opus"
		case ".mp4", ".mov", ".avi":
			fileType = "mp4"
		default:
			fileType = "stream"
		}
	}

	// Regular file sends keep image-style delivery for explicitly shared images.
	// Generated image artifacts call SendImage, which sends a file attachment to
	// avoid Feishu bubble cropping on diagrams and long technical images.
	imgExts := map[string]bool{".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".bmp": true, ".webp": true}
	if imgExts[ext] {
		return c.sendImageFile(chatID, filePath)
	}

	return c.sendFileAttachment(chatID, filePath, fileType)
}

func (c *FeishuChannel) sendFileAttachment(chatID, filePath, fileType string) error {
	fileName := filepath.Base(filePath)
	if strings.TrimSpace(fileType) == "" {
		switch strings.ToLower(filepath.Ext(fileName)) {
		case ".pdf":
			fileType = "pdf"
		case ".doc", ".docx":
			fileType = "doc"
		case ".xls", ".xlsx":
			fileType = "xls"
		case ".ppt", ".pptx":
			fileType = "ppt"
		case ".csv":
			fileType = "csv"
		case ".zip", ".gz", ".tar":
			fileType = "zip"
		case ".mp3", ".wav", ".amr":
			fileType = "opus"
		case ".mp4", ".mov", ".avi":
			fileType = "mp4"
		default:
			fileType = "stream"
		}
	}

	fileKey, err := c.uploadFile(filePath, fileType, fileName)
	if err != nil {
		c.logger.Printf("上传文件失败: %v", err)
		c.handleFileUploadError(chatID, err)
		return err
	}

	contentJSON, _ := json.Marshal(map[string]string{"file_key": fileKey})
	contentStr := string(contentJSON)

	resp, err := c.client.Im.Message.Create(context.Background(),
		larkim.NewCreateMessageReqBuilder().
			ReceiveIdType(larkim.ReceiveIdTypeChatId).
			Body(larkim.NewCreateMessageReqBodyBuilder().
				MsgType(larkim.MsgTypeFile).
				ReceiveId(chatID).
				Content(contentStr).
				Build()).
			Build())

	if err != nil {
		return fmt.Errorf("发送文件消息失败: %w", err)
	}
	if !resp.Success() {
		return fmt.Errorf("发送文件消息失败: code=%d, msg=%s", resp.Code, resp.Msg)
	}
	c.logger.Printf("文件发送成功: %s", fileName)
	return nil
}

// recordError writes an error to the persistent log file
func (c *FeishuChannel) recordError(chatID string, err error) {
	entry := map[string]string{
		"time":    time.Now().Format("2006-01-02 15:04:05"),
		"chat_id": chatID,
		"error":   err.Error(),
	}
	data, _ := json.Marshal(entry)
	// Append to storage/feishu_errors.jsonl
	logPath := filepath.Join("storage", "error", "feishu_errors.jsonl")
	os.MkdirAll(filepath.Dir(logPath), 0755)
	f, openErr := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if openErr == nil {
		defer f.Close()
		f.WriteString(string(data) + "\n")
	}
}

// handleFileUploadError logs, records, and notifies user in chat for all failures
func (c *FeishuChannel) handleFileUploadError(chatID string, err error) {
	errStr := err.Error()
	c.logger.Printf("文件上传失败(chat=%s): %v", chatID, err)
	c.recordError(chatID, err)

	switch {
	case strings.Contains(errStr, "99991672") || strings.Contains(errStr, "Access denied"):
		link := ""
		if idx := strings.Index(errStr, "https://"); idx >= 0 {
			link = errStr[idx:]
			if end := strings.IndexAny(link, " \n\r\t"); end > 0 {
				link = link[:end]
			}
		}
		if link != "" {
			c.sendPlainText(chatID, fmt.Sprintf("文件发送失败：应用缺少权限。\n请点击链接开通：\n%s", link))
		} else {
			c.sendPlainText(chatID, "文件发送失败：应用缺少权限，请前往飞书开放平台开通 im:resource:upload")
		}
	case strings.Contains(errStr, "timeout") || strings.Contains(errStr, "connection") || strings.Contains(errStr, "network"):
		c.sendPlainText(chatID, fmt.Sprintf("文件发送失败：网络问题，请稍后重试。\n(%v)", err))
	default:
		c.sendPlainText(chatID, fmt.Sprintf("文件发送失败：%v", err))
	}
}

// sendImageFile uploads and sends an image to a Feishu chat
func (c *FeishuChannel) sendImageFile(chatID, imgPath string) error {
	imageKey, err := c.uploadImage(imgPath)
	if err != nil {
		c.logger.Printf("上传图片失败: %v", err)
		c.handleFileUploadError(chatID, err)
		return err
	}

	contentJSON, _ := json.Marshal(map[string]string{"image_key": imageKey})
	contentStr := string(contentJSON)

	resp, err := c.client.Im.Message.Create(context.Background(),
		larkim.NewCreateMessageReqBuilder().
			ReceiveIdType(larkim.ReceiveIdTypeChatId).
			Body(larkim.NewCreateMessageReqBodyBuilder().
				MsgType(larkim.MsgTypeImage).
				ReceiveId(chatID).
				Content(contentStr).
				Build()).
			Build())

	if err != nil {
		return fmt.Errorf("发送图片消息失败: %w", err)
	}
	if !resp.Success() {
		return fmt.Errorf("发送图片消息失败: code=%d, msg=%s", resp.Code, resp.Msg)
	}
	c.logger.Printf("图片发送成功: %s", imgPath)
	return nil
}

func (c *FeishuChannel) Stop() error {
	c.logger.Println("飞书渠道停止中...")
	if c.cancel != nil {
		c.cancel()
	}
	time.Sleep(1 * time.Second)
	return nil
}

func (c *FeishuChannel) SendMessage(recipient string, content string) error {
	if recipient == "" {
		return fmt.Errorf("[Feishu] chatID is empty, cannot send message")
	}
	c.sendSimpleMessage(recipient, content)
	return nil
}

func (c *FeishuChannel) SendImage(recipient string, imagePath string) error {
	if recipient == "" {
		return fmt.Errorf("[Feishu] chatID is empty, cannot send image")
	}
	if strings.TrimSpace(imagePath) == "" {
		return fmt.Errorf("[Feishu] image path is empty")
	}
	return c.sendFileAttachment(recipient, imagePath, "stream")
}

func (c *FeishuChannel) sendReply(recipient, messageID, content string, metadata map[string]interface{}) {
	if err := c.SendMessage(recipient, content); err != nil {
		c.logger.Printf("send reply failed: %v", err)
	}
}

func (c *FeishuChannel) sendError(recipient, messageID, errMsg string) {
	c.sendSimpleMessage(recipient, errMsg)
}

func (c *FeishuChannel) SendGeneratedFiles(chatID, response string) {
	c.sendGeneratedFiles(chatID, response)
}

func (c *FeishuChannel) IsConnected() bool {
	if c.wsClient == nil {
		return false
	}
	select {
	case <-c.started:
		return true
	default:
		return false
	}
}

func (c *FeishuChannel) GetConnectionStatus() string {
	if c.wsClient == nil {
		return "未初始化"
	}
	select {
	case <-c.started:
		return "已启动"
	default:
		return "启动中"
	}
}
