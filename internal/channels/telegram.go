package channels

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// TelegramChannel 电报渠道
type TelegramChannel struct {
	*Base
	config       TelegramChannelConfig
	httpClient   *http.Client
	updateOffset atomic.Int64
	mediaHandler *ChannelMediaHandler
}

type TelegramChannelConfig struct {
	BotToken string `json:"bot_token" yaml:"bot_token"`
}

func NewTelegramChannel(name string, config TelegramChannelConfig, workspace string) *TelegramChannel {
	return &TelegramChannel{
		Base:         NewBase(name, "telegram"),
		config:       config,
		httpClient:   &http.Client{Timeout: 30 * time.Second},
		mediaHandler: NewChannelMediaHandler(workspace, nil, nil),
	}
}

func (c *TelegramChannel) Start() error {
	c.logger.Println("Telegram渠道启动")
	if c.config.BotToken == "" {
		return fmt.Errorf("bot_token 不能为空")
	}
	// 长轮询模式
	c.apiCall("deleteWebhook", map[string]string{"drop_pending_updates": "true"})
	go c.pollLoop()
	return nil
}

func (c *TelegramChannel) Stop() error {
	c.stopCh <- struct{}{}
	return nil
}

func (c *TelegramChannel) apiURL(method string) string {
	return fmt.Sprintf("https://api.telegram.org/bot%s/%s", c.config.BotToken, method)
}

func (c *TelegramChannel) apiCall(method string, params map[string]string) (*TelegramResponse, error) {
	url := c.apiURL(method)
	var body io.Reader
	if params != nil {
		jsonData, _ := json.Marshal(params)
		body = bytes.NewReader(jsonData)
	}
	req, _ := http.NewRequest("POST", url, body)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var result TelegramResponse
	json.NewDecoder(resp.Body).Decode(&result)
	if !result.Ok {
		return nil, fmt.Errorf("API错误: %s", result.Description)
	}
	return &result, nil
}

func (c *TelegramChannel) apiCallFile(method string, params map[string]string) (*TelegramFileResponse, error) {
	url := c.apiURL(method)
	var body io.Reader
	if params != nil {
		jsonData, _ := json.Marshal(params)
		body = bytes.NewReader(jsonData)
	}
	req, _ := http.NewRequest("POST", url, body)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var result TelegramFileResponse
	json.NewDecoder(resp.Body).Decode(&result)
	if !result.Ok {
		return nil, fmt.Errorf("API错误: %s", result.Description)
	}
	return &result, nil
}

func (c *TelegramChannel) pollLoop() {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-c.stopCh:
			return
		case <-ticker.C:
			c.fetchUpdates()
		}
	}
}

func (c *TelegramChannel) fetchUpdates() {
	offset := c.updateOffset.Load()
	resp, err := c.apiCall("getUpdates", map[string]string{
		"offset": strconv.FormatInt(offset, 10), "timeout": "1", "limit": "10",
	})
	if err != nil || resp == nil {
		return
	}
	for _, upd := range resp.Updates {
		c.updateOffset.Store(upd.UpdateID + 1)
		c.handleUpdate(&upd)
	}
}

func (c *TelegramChannel) handleUpdate(upd *TelegramUpdate) {
	msg := upd.Message
	if msg == nil || msg.Chat == nil {
		return
	}

	chatID := strconv.FormatInt(msg.Chat.ID, 10)
	userID := strconv.FormatInt(msg.From.ID, 10)
	userName := msg.From.FirstName
	if msg.From.Username != "" {
		userName = "@" + msg.From.Username
	}

	// 解析消息内容
	userMessage := c.parseMessage(msg)
	if userMessage == "" {
		return
	}

	c.logger.Printf("收到: chat=%s user=%s msg=%s", chatID, userName, truncateLog(userMessage, 80))

	if c.runtime == nil {
		c.sendSimple(chatID, "Gateway未初始化")
		return
	}

	sessionID := c.GetOrCreateSession(userID)
	if c.runtime.TryInjectInterrupt(sessionID, c.name, userID, userMessage) {
		c.sendSimple(chatID, "收到，正在处理中，消息已追加...")
		return
	}
	taskID := fmt.Sprintf("tg_%d", time.Now().UnixNano())
	c.runtime.SaveChatMessage(sessionID, "user", "", userMessage, "pending", taskID, c.name, userID)
	interruptCh := c.runtime.RegisterInterrupt(sessionID)

	// 异步处理AI回复
	go c.processAI(chatID, userID, sessionID, userMessage, taskID, interruptCh)
}

func (c *TelegramChannel) parseMessage(msg *TelegramMessage) string {
	switch {
	case msg.Text != "":
		return msg.Text
	case msg.Document != nil:
		return c.downloadFile(msg.Document.FileID, msg.Document.FileName, "doc")
	case len(msg.Photo) > 0:
		p := msg.Photo[len(msg.Photo)-1]
		caption := msg.Caption
		url := c.getFileURL(p.FileID)
		if url != "" {
			result, _ := c.mediaHandler.DownloadAndProcess(url, "photo.jpg", "image/jpeg")
			if caption != "" {
				return caption + "\n" + result
			}
			return result
		}
		return "[图片] " + caption
	case msg.Voice != nil:
		url := c.getFileURL(msg.Voice.FileID)
		if url != "" {
			result, _ := c.mediaHandler.DownloadAndProcess(url, "voice.ogg", "audio/ogg")
			return result
		}
		return "[语音]"
	default:
		return ""
	}
}

func (c *TelegramChannel) getFileURL(fileID string) string {
	resp, err := c.apiCallFile("getFile", map[string]string{"file_id": fileID})
	if err != nil || resp == nil || resp.FileInfo.FilePath == "" {
		return ""
	}
	return fmt.Sprintf("https://api.telegram.org/file/bot%s/%s", c.config.BotToken, resp.FileInfo.FilePath)
}

func (c *TelegramChannel) downloadFile(fileID, fileName, label string) string {
	url := c.getFileURL(fileID)
	if url == "" {
		return fmt.Sprintf("[%s] 获取下载链接失败", label)
	}
	result, err := c.mediaHandler.DownloadAndProcess(url, fileName, "application/octet-stream")
	if err != nil {
		return fmt.Sprintf("[%s] 处理失败: %v", label, err)
	}
	return result
}

func (c *TelegramChannel) processAI(chatID, userID, sessionID, message, taskID string, interruptCh chan string) {
	defer c.runtime.UnregisterInterrupt(sessionID, interruptCh)
	if c.runtime == nil {
		c.sendSimple(chatID, "服务未就绪")
		return
	}
	agentName, agent, ok := c.runtime.DefaultAgent()
	if !ok {
		c.sendSimple(chatID, "无可用Agent")
		return
	}

	ctx := AgentContext(context.Background(), taskID, sessionID, c.name, userID)
	result, err := GenerateWithInterrupts(ctx, agent, message, interruptCh, func(interruptMsg string) {
		c.logger.Printf("Telegram任务收到追加消息: %s", truncateLog(interruptMsg, 100))
		c.runtime.SaveChatMessage(sessionID, "user", "", interruptMsg, "pending", taskID, c.name, userID)
	})
	if err != nil {
		c.sendSimple(chatID, "处理失败: "+err.Error())
		c.runtime.SaveChatMessage(sessionID, "assistant", agentName, err.Error(), "failed", taskID, c.name, userID)
		return
	}

	c.sendSimple(chatID, result)
	c.runtime.SaveChatMessage(sessionID, "assistant", agentName, result, "completed", taskID, c.name, userID)
}

func (c *TelegramChannel) sendSimple(chatID, content string) {
	maxLen := 4000
	parts := splitTG(content, maxLen)
	for _, p := range parts {
		_, err := c.apiCall("sendMessage", map[string]string{
			"chat_id": chatID, "text": p,
		})
		if err != nil {
			c.logger.Printf("发送失败: %v", err)
		}
		if len(parts) > 1 {
			time.Sleep(200 * time.Millisecond)
		}
	}
}

func (c *TelegramChannel) SendMessage(recipient string, content string) error {
	c.sendSimple(recipient, content)
	return nil
}

func splitTG(content string, maxLen int) []string {
	if len(content) <= maxLen {
		return []string{content}
	}
	var parts []string
	lines := strings.Split(content, "\n")
	current := ""
	for _, line := range lines {
		if len(current)+len(line)+1 > maxLen {
			if current != "" {
				parts = append(parts, current)
				current = line
			} else {
				for len(line) > maxLen {
					parts = append(parts, line[:maxLen])
					line = line[maxLen:]
				}
				current = line
			}
		} else if current == "" {
			current = line
		} else {
			current += "\n" + line
		}
	}
	if current != "" {
		parts = append(parts, current)
	}
	return parts
}

// Telegram API types
type TelegramResponse struct {
	Ok          bool             `json:"ok"`
	Description string           `json:"description"`
	Updates     []TelegramUpdate `json:"result"`
}

type TelegramFileResponse struct {
	Ok          bool         `json:"ok"`
	Description string       `json:"description"`
	FileInfo    TelegramFile `json:"result"`
}

type TelegramFile struct {
	FilePath string `json:"file_path"`
}

type TelegramUpdate struct {
	UpdateID int64            `json:"update_id"`
	Message  *TelegramMessage `json:"message"`
}

type TelegramMessage struct {
	MessageID int64             `json:"message_id"`
	From      *TelegramUser     `json:"from"`
	Chat      *TelegramChat     `json:"chat"`
	Text      string            `json:"text"`
	Caption   string            `json:"caption"`
	Document  *TelegramDocument `json:"document"`
	Photo     []TelegramPhoto   `json:"photo"`
	Voice     *TelegramVoice    `json:"voice"`
}

type TelegramUser struct {
	ID        int64  `json:"id"`
	FirstName string `json:"first_name"`
	Username  string `json:"username"`
}

type TelegramChat struct {
	ID int64 `json:"id"`
}

type TelegramDocument struct {
	FileID   string `json:"file_id"`
	FileName string `json:"file_name"`
}

type TelegramPhoto struct {
	FileID string `json:"file_id"`
}

type TelegramVoice struct {
	FileID string `json:"file_id"`
}
