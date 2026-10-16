package channels

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	qrcodepkg "github.com/skip2/go-qrcode"
)

const (
	weixinDefaultBaseURL       = "https://ilinkai.weixin.qq.com"
	weixinDefaultBotAgent      = "OpenKitty/1.0 OpenClawCompatible/2.4.6"
	weixinDefaultChannelVer    = "2.4.6"
	weixinIlinkAppID           = "bot"
	weixinIlinkClientVersion   = "132102" // 2.4.6 encoded as 0x00020406
	weixinDefaultPollTimeoutMS = 35000
	weixinDefaultAPITimeoutMS  = 15000
	weixinMessageTypeUser      = 1
	weixinMessageTypeBot       = 2
	weixinMessageStateFinish   = 2
	weixinItemTypeText         = 1
	weixinItemTypeVoice        = 3
)

const WeixinDefaultBaseURL = weixinDefaultBaseURL
const WeixinItemTypeText = weixinItemTypeText
const WeixinItemTypeVoice = weixinItemTypeVoice

type WeixinChannelConfig struct {
	AccountID        string   `json:"account_id" yaml:"account_id"`
	Token            string   `json:"token" yaml:"token"`
	BaseURL          string   `json:"base_url" yaml:"base_url"`
	BotAgent         string   `json:"bot_agent" yaml:"bot_agent"`
	AllowedUsers     []string `json:"allowed_users" yaml:"allowed_users"`
	StrictAllowed    bool     `json:"strict_allowed_users" yaml:"strict_allowed_users"`
	PollTimeoutMS    int      `json:"poll_timeout_ms" yaml:"poll_timeout_ms"`
	RequestTimeoutMS int      `json:"request_timeout_ms" yaml:"request_timeout_ms"`
}

type WeixinChannel struct {
	*Base
	config        WeixinChannelConfig
	workspace     string
	httpClient    *http.Client
	ctx           context.Context
	cancel        context.CancelFunc
	mu            sync.RWMutex
	running       bool
	getUpdatesBuf string
	contextTokens map[string]string
	processed     map[string]time.Time
	allowedUsers  map[string]struct{}
	onMessage     func(userID, recipient, messageID, message string, metadata map[string]interface{})
}

func NewWeixinChannel(name string, config WeixinChannelConfig, workspace string) *WeixinChannel {
	config = normalizeWeixinChannelConfig(config)
	return &WeixinChannel{
		Base:          NewBase(name, "weixin"),
		config:        config,
		workspace:     workspace,
		httpClient:    &http.Client{},
		contextTokens: make(map[string]string),
		processed:     make(map[string]time.Time),
		allowedUsers:  makeWeixinAllowedSet(config.AllowedUsers),
	}
}

func (c *WeixinChannel) SetMessageHandler(handler func(userID, recipient, messageID, message string, metadata map[string]interface{})) {
	c.onMessage = handler
}

func NewWeixinChannelFromMap(name string, raw map[string]interface{}, workspace string) *WeixinChannel {
	return NewWeixinChannel(name, ParseWeixinChannelConfig(raw), workspace)
}

func ParseWeixinChannelConfig(raw map[string]interface{}) WeixinChannelConfig {
	cfg := WeixinChannelConfig{
		AccountID:        firstNonEmptyString(mapString(raw, "account_id", ""), mapString(raw, "accountId", "")),
		Token:            mapString(raw, "token", ""),
		BaseURL:          firstNonEmptyString(mapString(raw, "base_url", ""), mapString(raw, "baseUrl", ""), weixinDefaultBaseURL),
		BotAgent:         firstNonEmptyString(mapString(raw, "bot_agent", ""), mapString(raw, "botAgent", ""), weixinDefaultBotAgent),
		AllowedUsers:     firstNonEmptyStringList(mapStringSlice(raw, "allowed_users", nil), mapStringSlice(raw, "allow_from", nil), splitWeixinCSV(mapString(raw, "allowed_users", "")), splitWeixinCSV(mapString(raw, "allow_from", ""))),
		StrictAllowed:    mapBool(raw, "strict_allowed_users", mapBool(raw, "strict_allow_from", false)),
		PollTimeoutMS:    mapInt(raw, "poll_timeout_ms", weixinDefaultPollTimeoutMS),
		RequestTimeoutMS: mapInt(raw, "request_timeout_ms", weixinDefaultAPITimeoutMS),
	}
	return normalizeWeixinChannelConfig(cfg)
}

func normalizeWeixinChannelConfig(cfg WeixinChannelConfig) WeixinChannelConfig {
	cfg.AccountID = strings.TrimSpace(cfg.AccountID)
	cfg.Token = strings.TrimSpace(cfg.Token)
	cfg.BaseURL = strings.TrimSpace(cfg.BaseURL)
	cfg.BotAgent = sanitizeWeixinBotAgent(cfg.BotAgent)
	if cfg.BaseURL == "" {
		cfg.BaseURL = weixinDefaultBaseURL
	}
	if cfg.PollTimeoutMS <= 0 {
		cfg.PollTimeoutMS = weixinDefaultPollTimeoutMS
	}
	if cfg.RequestTimeoutMS <= 0 {
		cfg.RequestTimeoutMS = weixinDefaultAPITimeoutMS
	}
	cfg.AllowedUsers = uniqueTrimmedStrings(cfg.AllowedUsers)
	return cfg
}

func (c *WeixinChannel) Start() error {
	if c.config.Token == "" || c.config.AccountID == "" {
		return fmt.Errorf("weixin channel is not configured: missing account_id or token; use /api/weixin/login/start")
	}
	c.mu.Lock()
	if c.cancel != nil {
		c.cancel()
	}
	c.ctx, c.cancel = context.WithCancel(context.Background())
	c.running = true
	c.mu.Unlock()

	c.loadState()
	if len(c.allowedUsers) == 0 {
		c.logger.Println("allowed_users is empty; accepting direct messages from any Weixin user")
	}

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Duration(c.config.RequestTimeoutMS)*time.Millisecond)
		defer cancel()
		if err := c.notifyLifecycle(ctx, "ilink/bot/msg/notifystart"); err != nil {
			c.logger.Printf("notifyStart failed: %v", err)
		}
	}()

	go c.pollLoop(c.ctx)
	c.logger.Printf("weixin channel started: account=%s base_url=%s", c.config.AccountID, c.config.BaseURL)
	return nil
}

func (c *WeixinChannel) Stop() error {
	c.mu.Lock()
	cancel := c.cancel
	c.cancel = nil
	c.running = false
	c.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if c.config.AccountID != "" {
		c.saveState()
	}
	if c.config.Token != "" && c.config.AccountID != "" {
		ctx, done := context.WithTimeout(context.Background(), time.Duration(c.config.RequestTimeoutMS)*time.Millisecond)
		defer done()
		if err := c.notifyLifecycle(ctx, "ilink/bot/msg/notifystop"); err != nil {
			c.logger.Printf("notifyStop failed: %v", err)
		}
	}
	return nil
}

func (c *WeixinChannel) IsRunning() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.running
}

func (c *WeixinChannel) SendMessage(recipient string, content string) error {
	recipient = strings.TrimSpace(recipient)
	content = strings.TrimSpace(content)
	if recipient == "" {
		return fmt.Errorf("weixin recipient is empty")
	}
	if content == "" {
		return nil
	}
	if c.config.Token == "" {
		return fmt.Errorf("weixin channel is not configured: missing token")
	}

	c.mu.RLock()
	contextToken := c.contextTokens[recipient]
	c.mu.RUnlock()

	for _, chunk := range splitRunesForWeixin(content, 3900) {
		req := map[string]interface{}{
			"msg": map[string]interface{}{
				"from_user_id":  "",
				"to_user_id":    recipient,
				"client_id":     "openkitty-weixin-" + randomHexID(8),
				"message_type":  weixinMessageTypeBot,
				"message_state": weixinMessageStateFinish,
				"item_list": []map[string]interface{}{
					{
						"type":      weixinItemTypeText,
						"text_item": map[string]string{"text": chunk},
					},
				},
				"context_token": contextToken,
			},
			"base_info": c.baseInfo(),
		}
		if contextToken == "" {
			delete(req["msg"].(map[string]interface{}), "context_token")
		}
		var resp struct {
			Ret    int    `json:"ret,omitempty"`
			ErrMsg string `json:"errmsg,omitempty"`
		}
		if err := weixinPostJSON(context.Background(), c.httpClient, c.config.BaseURL, "ilink/bot/sendmessage", c.config.Token, c.config.RequestTimeoutMS, req, &resp); err != nil {
			return err
		}
		if resp.Ret != 0 {
			return fmt.Errorf("sendmessage ret=%d errmsg=%s", resp.Ret, resp.ErrMsg)
		}
	}
	return nil
}

func (c *WeixinChannel) sendReply(recipient, messageID, content string, metadata map[string]interface{}) {
	if err := c.SendMessage(recipient, content); err != nil {
		c.logger.Printf("send reply failed: %v", err)
	}
}

func (c *WeixinChannel) sendError(recipient, messageID, errMsg string) {
	if err := c.SendMessage(recipient, errMsg); err != nil {
		c.logger.Printf("send error failed: %v", err)
	}
}

func (c *WeixinChannel) pollLoop(ctx context.Context) {
	failures := 0
	for {
		if ctx.Err() != nil {
			return
		}

		resp, err := c.getUpdates(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			failures++
			c.logger.Printf("getUpdates failed (%d): %v", failures, err)
			sleepWithContext(ctx, retryDelayForWeixin(failures))
			continue
		}
		failures = 0

		if resp.LongPollingTimeoutMS > 0 {
			c.config.PollTimeoutMS = resp.LongPollingTimeoutMS
		}
		if resp.GetUpdatesBuf != "" {
			c.mu.Lock()
			c.getUpdatesBuf = resp.GetUpdatesBuf
			c.mu.Unlock()
			c.saveState()
		}

		for _, msg := range resp.Messages {
			c.handleInboundMessage(msg)
		}
	}
}

func (c *WeixinChannel) getUpdates(parent context.Context) (*weixinGetUpdatesResp, error) {
	c.mu.RLock()
	buf := c.getUpdatesBuf
	c.mu.RUnlock()

	req := map[string]interface{}{
		"get_updates_buf": buf,
		"base_info":       c.baseInfo(),
	}
	var resp weixinGetUpdatesResp
	if err := weixinPostJSON(parent, c.httpClient, c.config.BaseURL, "ilink/bot/getupdates", c.config.Token, c.config.PollTimeoutMS+5000, req, &resp); err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(parent.Err(), context.Canceled) {
			return &weixinGetUpdatesResp{}, nil
		}
		return nil, err
	}
	if (resp.Ret != 0) || (resp.ErrCode != 0) {
		return nil, fmt.Errorf("getUpdates ret=%d errcode=%d errmsg=%s", resp.Ret, resp.ErrCode, resp.ErrMsg)
	}
	return &resp, nil
}

func (c *WeixinChannel) handleInboundMessage(msg weixinMessage) {
	if msg.MessageType == weixinMessageTypeBot {
		return
	}
	if msg.MessageType != 0 && msg.MessageType != weixinMessageTypeUser {
		return
	}
	from := strings.TrimSpace(msg.FromUserID)
	if from == "" {
		return
	}
	if !c.ShouldAcceptUser(from) {
		c.logger.Printf("drop message from unauthorized user=%s allowed_users=%s", from, strings.Join(c.allowedUserIDs(), ","))
		return
	}
	if !c.isAllowedUser(from) {
		c.logger.Printf("allowed_users mismatch; accepting message because strict_allowed_users=false: user=%s allowed_users=%s", from, strings.Join(c.allowedUserIDs(), ","))
	}
	messageID := msg.messageIDString()
	if c.seenMessage(from, messageID, msg.Seq) {
		return
	}

	body := strings.TrimSpace(weixinExtractText(msg.ItemList))
	if body == "" {
		return
	}
	if msg.ContextToken != "" {
		c.mu.Lock()
		c.contextTokens[from] = msg.ContextToken
		c.mu.Unlock()
		c.saveState()
	}

	if c.onMessage == nil {
		c.logger.Printf("gateway is nil; cannot process inbound message from=%s", from)
		return
	}
	c.onMessage(from, from, messageID, body, map[string]interface{}{
		"account_id":    c.config.AccountID,
		"context_token": msg.ContextToken,
		"channel":       c.name,
	})
}

func (c *WeixinChannel) seenMessage(from, messageID string, seq int64) bool {
	key := from + ":" + messageID
	if messageID == "" && seq > 0 {
		key = from + ":seq:" + strconv.FormatInt(seq, 10)
	}
	if key == from+":" {
		return false
	}
	now := time.Now()
	c.mu.Lock()
	defer c.mu.Unlock()
	for k, t := range c.processed {
		if now.Sub(t) > 24*time.Hour {
			delete(c.processed, k)
		}
	}
	if _, ok := c.processed[key]; ok {
		return true
	}
	c.processed[key] = now
	return false
}

func (c *WeixinChannel) isAllowedUser(userID string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if len(c.allowedUsers) == 0 {
		return true
	}
	_, ok := c.allowedUsers[userID]
	return ok
}

func (c *WeixinChannel) ShouldAcceptUser(userID string) bool {
	if c.isAllowedUser(userID) {
		return true
	}
	return !c.config.StrictAllowed
}

func (c *WeixinChannel) allowedUserIDs() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	ids := make([]string, 0, len(c.allowedUsers))
	for id := range c.allowedUsers {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func (c *WeixinChannel) notifyLifecycle(ctx context.Context, endpoint string) error {
	return weixinPostJSON(ctx, c.httpClient, c.config.BaseURL, endpoint, c.config.Token, c.config.RequestTimeoutMS, map[string]interface{}{
		"base_info": c.baseInfo(),
	}, nil)
}

func (c *WeixinChannel) baseInfo() map[string]string {
	return map[string]string{
		"channel_version": weixinDefaultChannelVer,
		"bot_agent":       c.config.BotAgent,
	}
}

type weixinChannelState struct {
	GetUpdatesBuf string            `json:"get_updates_buf"`
	ContextTokens map[string]string `json:"context_tokens"`
}

func (c *WeixinChannel) statePath() string {
	workspace := strings.TrimSpace(c.workspace)
	if workspace == "" {
		workspace = "."
	}
	return filepath.Join(workspace, ".openkitty", "weixin", sanitizeWeixinFileName(c.config.AccountID)+".json")
}

func (c *WeixinChannel) loadState() {
	path := c.statePath()
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var state weixinChannelState
	if err := json.Unmarshal(data, &state); err != nil {
		c.logger.Printf("load state failed: %v", err)
		return
	}
	c.mu.Lock()
	c.getUpdatesBuf = state.GetUpdatesBuf
	if state.ContextTokens != nil {
		c.contextTokens = state.ContextTokens
	}
	c.mu.Unlock()
}

func (c *WeixinChannel) saveState() {
	path := c.statePath()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		c.logger.Printf("create state dir failed: %v", err)
		return
	}
	c.mu.RLock()
	state := weixinChannelState{
		GetUpdatesBuf: c.getUpdatesBuf,
		ContextTokens: make(map[string]string, len(c.contextTokens)),
	}
	for k, v := range c.contextTokens {
		state.ContextTokens[k] = v
	}
	c.mu.RUnlock()
	data, _ := json.MarshalIndent(state, "", "  ")
	if err := os.WriteFile(path, data, 0600); err != nil {
		c.logger.Printf("save state failed: %v", err)
	}
}

type weixinGetUpdatesResp struct {
	Ret                  int             `json:"ret,omitempty"`
	ErrCode              int             `json:"errcode,omitempty"`
	ErrMsg               string          `json:"errmsg,omitempty"`
	Messages             []weixinMessage `json:"msgs,omitempty"`
	GetUpdatesBuf        string          `json:"get_updates_buf,omitempty"`
	LongPollingTimeoutMS int             `json:"longpolling_timeout_ms,omitempty"`
}

type weixinMessage struct {
	Seq          int64               `json:"seq,omitempty"`
	MessageID    json.RawMessage     `json:"message_id,omitempty"`
	FromUserID   string              `json:"from_user_id,omitempty"`
	ToUserID     string              `json:"to_user_id,omitempty"`
	ClientID     string              `json:"client_id,omitempty"`
	CreateTimeMS int64               `json:"create_time_ms,omitempty"`
	MessageType  int                 `json:"message_type,omitempty"`
	MessageState int                 `json:"message_state,omitempty"`
	ItemList     []weixinMessageItem `json:"item_list,omitempty"`
	ContextToken string              `json:"context_token,omitempty"`
	RunID        string              `json:"run_id,omitempty"`
}

func (m weixinMessage) messageIDString() string {
	if len(m.MessageID) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(m.MessageID, &s) == nil {
		return s
	}
	return strings.TrimSpace(string(m.MessageID))
}

type weixinMessageItem struct {
	Type      int              `json:"type,omitempty"`
	TextItem  *weixinTextItem  `json:"text_item,omitempty"`
	VoiceItem *weixinVoiceItem `json:"voice_item,omitempty"`
}

type weixinTextItem struct {
	Text string `json:"text,omitempty"`
}

type weixinVoiceItem struct {
	Text string `json:"text,omitempty"`
}

func weixinExtractText(items []weixinMessageItem) string {
	for _, item := range items {
		if item.Type == weixinItemTypeText && item.TextItem != nil {
			return item.TextItem.Text
		}
		if item.Type == weixinItemTypeVoice && item.VoiceItem != nil && item.VoiceItem.Text != "" {
			return item.VoiceItem.Text
		}
	}
	return ""
}

type weixinQRSession struct {
	SessionKey     string
	QRCode         string
	QRDataURL      string
	StartedAt      time.Time
	CurrentBaseURL string
}

var weixinQRLogins = struct {
	sync.Mutex
	items map[string]*weixinQRSession
}{items: make(map[string]*weixinQRSession)}

type weixinQRStartOptions struct {
	ChannelName string
	LocalTokens []string
	Force       bool
}

type weixinQRStartResult struct {
	OK         bool      `json:"ok"`
	SessionKey string    `json:"session_key"`
	QRCode     string    `json:"qrcode,omitempty"`
	QRDataURL  string    `json:"qr_data_url,omitempty"`
	Message    string    `json:"message"`
	ExpiresAt  time.Time `json:"expires_at"`
}

func startWeixinQRLogin(ctx context.Context, opts weixinQRStartOptions) (*weixinQRStartResult, error) {
	purgeExpiredWeixinQRLogins()
	sessionKey := strings.TrimSpace(opts.ChannelName)
	if sessionKey == "" {
		sessionKey = "weixin-" + randomHexID(8)
	}
	if !opts.Force {
		weixinQRLogins.Lock()
		existing := weixinQRLogins.items[sessionKey]
		weixinQRLogins.Unlock()
		if existing != nil && time.Since(existing.StartedAt) < 5*time.Minute {
			return &weixinQRStartResult{
				OK:         true,
				SessionKey: sessionKey,
				QRCode:     existing.QRCode,
				QRDataURL:  existing.QRDataURL,
				Message:    "QR login already started.",
				ExpiresAt:  existing.StartedAt.Add(5 * time.Minute),
			}, nil
		}
	}

	body := map[string]interface{}{"local_token_list": uniqueTrimmedStrings(opts.LocalTokens)}
	var resp struct {
		QRCode           string `json:"qrcode"`
		QRCodeImgContent string `json:"qrcode_img_content"`
	}
	if err := weixinPostJSON(ctx, http.DefaultClient, weixinDefaultBaseURL, "ilink/bot/get_bot_qrcode?bot_type=3", "", weixinDefaultAPITimeoutMS, body, &resp); err != nil {
		return nil, err
	}
	if resp.QRCode == "" || resp.QRCodeImgContent == "" {
		return nil, fmt.Errorf("weixin QR response is missing qrcode")
	}
	qrDataURL := normalizeWeixinQRImage(resp.QRCodeImgContent)
	if qrDataURL == "" || strings.HasPrefix(strings.ToLower(qrDataURL), "http://") || strings.HasPrefix(strings.ToLower(qrDataURL), "https://") {
		if generated := generateWeixinQRScanDataURL(resp.QRCodeImgContent); generated != "" {
			qrDataURL = generated
		}
	}
	if qrDataURL == "" {
		return nil, fmt.Errorf("weixin QR image content is unsupported: len=%d prefix=%q", len(resp.QRCodeImgContent), truncateWeixinString(resp.QRCodeImgContent, 80))
	}

	session := &weixinQRSession{
		SessionKey:     sessionKey,
		QRCode:         resp.QRCode,
		QRDataURL:      qrDataURL,
		StartedAt:      time.Now(),
		CurrentBaseURL: weixinDefaultBaseURL,
	}
	weixinQRLogins.Lock()
	weixinQRLogins.items[sessionKey] = session
	weixinQRLogins.Unlock()
	return &weixinQRStartResult{
		OK:         true,
		SessionKey: sessionKey,
		QRCode:     resp.QRCode,
		QRDataURL:  qrDataURL,
		Message:    "Scan the QR code with WeChat.",
		ExpiresAt:  session.StartedAt.Add(5 * time.Minute),
	}, nil
}

func generateWeixinQRScanDataURL(scanData string) string {
	scanData = strings.TrimSpace(scanData)
	if scanData == "" {
		return ""
	}
	png, err := qrcodepkg.Encode(scanData, qrcodepkg.Medium, 256)
	if err != nil || len(png) == 0 {
		return ""
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(png)
}

func normalizeWeixinQRImage(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	if unquoted, err := strconv.Unquote(s); err == nil {
		s = strings.TrimSpace(unquoted)
	}
	if src := extractWeixinImageSrc(s); src != "" {
		s = src
	}
	if unescaped, err := url.QueryUnescape(s); err == nil && unescaped != s {
		s = strings.TrimSpace(unescaped)
	}
	lower := strings.ToLower(s)
	if strings.HasPrefix(lower, "data:image/") || strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") {
		return s
	}
	if strings.HasPrefix(lower, "base64://") {
		s = strings.TrimSpace(s[len("base64://"):])
		lower = strings.ToLower(s)
	}
	if strings.HasPrefix(lower, "base64,") {
		s = strings.TrimSpace(s[len("base64,"):])
	}
	if strings.HasPrefix(strings.TrimSpace(s), "<svg") || strings.HasPrefix(strings.TrimSpace(s), "<?xml") {
		return "data:image/svg+xml;base64," + base64.StdEncoding.EncodeToString([]byte(s))
	}
	compact := strings.NewReplacer("\r", "", "\n", "", "\t", "", " ", "").Replace(s)
	if decoded, err := base64.StdEncoding.DecodeString(compact); err == nil && len(decoded) > 0 {
		decodedTrimmed := strings.TrimSpace(string(decoded))
		if strings.HasPrefix(decodedTrimmed, "<svg") || strings.HasPrefix(decodedTrimmed, "<?xml") {
			return "data:image/svg+xml;base64," + base64.StdEncoding.EncodeToString(decoded)
		}
		if len(decoded) >= 8 && decoded[0] == 0x89 && decoded[1] == 'P' && decoded[2] == 'N' && decoded[3] == 'G' {
			return "data:image/png;base64," + compact
		}
		if len(decoded) >= 3 && decoded[0] == 0xff && decoded[1] == 0xd8 && decoded[2] == 0xff {
			return "data:image/jpeg;base64," + compact
		}
		if len(decoded) >= 6 && (string(decoded[:6]) == "GIF87a" || string(decoded[:6]) == "GIF89a") {
			return "data:image/gif;base64," + compact
		}
		if len(decoded) >= 12 && string(decoded[:4]) == "RIFF" && string(decoded[8:12]) == "WEBP" {
			return "data:image/webp;base64," + compact
		}
	}
	return ""
}

func extractWeixinImageSrc(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	lower := strings.ToLower(s)
	if !strings.Contains(lower, "<img") || !strings.Contains(lower, "src") {
		return ""
	}
	idx := strings.Index(lower, "src")
	if idx < 0 {
		return ""
	}
	rest := strings.TrimSpace(s[idx+len("src"):])
	if !strings.HasPrefix(rest, "=") {
		return ""
	}
	rest = strings.TrimSpace(rest[1:])
	if rest == "" {
		return ""
	}
	quote := rest[0]
	if quote == '"' || quote == '\'' {
		end := strings.IndexByte(rest[1:], quote)
		if end >= 0 {
			return strings.TrimSpace(rest[1 : 1+end])
		}
		return ""
	}
	end := strings.IndexAny(rest, " >\t\r\n")
	if end < 0 {
		return strings.TrimSpace(rest)
	}
	return strings.TrimSpace(rest[:end])
}

type weixinQRWaitResult struct {
	OK               bool   `json:"ok"`
	Connected        bool   `json:"connected"`
	AlreadyConnected bool   `json:"already_connected,omitempty"`
	Status           string `json:"status,omitempty"`
	AccountID        string `json:"account_id,omitempty"`
	Token            string `json:"token,omitempty"`
	BaseURL          string `json:"base_url,omitempty"`
	UserID           string `json:"user_id,omitempty"`
	Message          string `json:"message"`
}

func waitWeixinQRLoginOnce(ctx context.Context, sessionKey string, verifyCode string) (*weixinQRWaitResult, error) {
	purgeExpiredWeixinQRLogins()
	weixinQRLogins.Lock()
	session := weixinQRLogins.items[sessionKey]
	weixinQRLogins.Unlock()
	if session == nil {
		return &weixinQRWaitResult{OK: false, Connected: false, Status: "missing", Message: "QR login session not found or expired."}, nil
	}

	endpoint := "ilink/bot/get_qrcode_status?qrcode=" + urlQueryEscape(session.QRCode)
	if strings.TrimSpace(verifyCode) != "" {
		endpoint += "&verify_code=" + urlQueryEscape(strings.TrimSpace(verifyCode))
	}
	var resp struct {
		Status       string `json:"status"`
		BotToken     string `json:"bot_token"`
		IlinkBotID   string `json:"ilink_bot_id"`
		BaseURL      string `json:"baseurl"`
		IlinkUserID  string `json:"ilink_user_id"`
		RedirectHost string `json:"redirect_host"`
	}
	if err := weixinGetJSON(ctx, http.DefaultClient, session.CurrentBaseURL, endpoint, weixinDefaultAPITimeoutMS+20000, &resp); err != nil {
		return nil, err
	}

	switch resp.Status {
	case "confirmed":
		weixinQRLogins.Lock()
		delete(weixinQRLogins.items, sessionKey)
		weixinQRLogins.Unlock()
		baseURL := firstNonEmptyString(resp.BaseURL, session.CurrentBaseURL, weixinDefaultBaseURL)
		return &weixinQRWaitResult{
			OK:        true,
			Connected: true,
			Status:    resp.Status,
			AccountID: resp.IlinkBotID,
			Token:     resp.BotToken,
			BaseURL:   baseURL,
			UserID:    resp.IlinkUserID,
			Message:   "Weixin login confirmed.",
		}, nil
	case "binded_redirect":
		weixinQRLogins.Lock()
		delete(weixinQRLogins.items, sessionKey)
		weixinQRLogins.Unlock()
		return &weixinQRWaitResult{OK: true, AlreadyConnected: true, Status: resp.Status, Message: "This bot is already connected."}, nil
	case "expired", "verify_code_blocked":
		weixinQRLogins.Lock()
		delete(weixinQRLogins.items, sessionKey)
		weixinQRLogins.Unlock()
		return &weixinQRWaitResult{OK: false, Connected: false, Status: resp.Status, Message: "QR login expired or blocked."}, nil
	case "scaned_but_redirect":
		if resp.RedirectHost != "" {
			weixinQRLogins.Lock()
			session.CurrentBaseURL = "https://" + strings.TrimPrefix(resp.RedirectHost, "https://")
			weixinQRLogins.Unlock()
		}
		return &weixinQRWaitResult{OK: true, Connected: false, Status: resp.Status, Message: "QR scanned; switched polling region."}, nil
	case "scaned":
		return &weixinQRWaitResult{OK: true, Connected: false, Status: resp.Status, Message: "QR scanned; waiting for confirmation."}, nil
	case "need_verifycode":
		return &weixinQRWaitResult{OK: true, Connected: false, Status: resp.Status, Message: "Verification code is required."}, nil
	default:
		if resp.Status == "" {
			resp.Status = "wait"
		}
		return &weixinQRWaitResult{OK: true, Connected: false, Status: resp.Status, Message: "Waiting for QR scan."}, nil
	}
}

func purgeExpiredWeixinQRLogins() {
	cutoff := time.Now().Add(-5 * time.Minute)
	weixinQRLogins.Lock()
	defer weixinQRLogins.Unlock()
	for key, item := range weixinQRLogins.items {
		if item.StartedAt.Before(cutoff) {
			delete(weixinQRLogins.items, key)
		}
	}
}

func weixinPostJSON(parent context.Context, client *http.Client, baseURL, endpoint, token string, timeoutMS int, payload interface{}, out interface{}) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, cancel, err := weixinNewRequest(parent, http.MethodPost, baseURL, endpoint, token, timeoutMS, bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer cancel()
	req.Header.Set("Content-Type", "application/json")
	return weixinDoJSON(client, req, out)
}

func weixinGetJSON(parent context.Context, client *http.Client, baseURL, endpoint string, timeoutMS int, out interface{}) error {
	req, cancel, err := weixinNewRequest(parent, http.MethodGet, baseURL, endpoint, "", timeoutMS, nil)
	if err != nil {
		return err
	}
	defer cancel()
	return weixinDoJSON(client, req, out)
}

func weixinNewRequest(parent context.Context, method, baseURL, endpoint, token string, timeoutMS int, body io.Reader) (*http.Request, context.CancelFunc, error) {
	if parent == nil {
		parent = context.Background()
	}
	if timeoutMS <= 0 {
		timeoutMS = weixinDefaultAPITimeoutMS
	}
	ctx, cancel := context.WithTimeout(parent, time.Duration(timeoutMS)*time.Millisecond)
	url := strings.TrimRight(firstNonEmptyString(baseURL, weixinDefaultBaseURL), "/") + "/" + strings.TrimLeft(endpoint, "/")
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		cancel()
		return nil, nil, err
	}
	req.Header.Set("iLink-App-Id", weixinIlinkAppID)
	req.Header.Set("iLink-App-ClientVersion", weixinIlinkClientVersion)
	req.Header.Set("X-WECHAT-UIN", randomWeixinUIN())
	if method == http.MethodPost || token != "" {
		req.Header.Set("AuthorizationType", "ilink_bot_token")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return req, cancel, nil
}

func weixinDoJSON(client *http.Client, req *http.Request, out interface{}) error {
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("weixin api %s: %s", resp.Status, truncateWeixinString(string(raw), 500))
	}
	if out == nil || len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("decode weixin api response: %w; raw=%s", err, truncateWeixinString(string(raw), 500))
	}
	return nil
}

func writeWeixinJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func randomWeixinUIN() string {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return base64.StdEncoding.EncodeToString([]byte(strconv.FormatInt(time.Now().UnixNano(), 10)))
	}
	n := binary.BigEndian.Uint32(b[:])
	return base64.StdEncoding.EncodeToString([]byte(strconv.FormatUint(uint64(n), 10)))
}

func randomHexID(n int) string {
	if n <= 0 {
		n = 8
	}
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	return hex.EncodeToString(b)
}

func urlQueryEscape(s string) string {
	return url.QueryEscape(s)
}

func makeWeixinAllowedSet(users []string) map[string]struct{} {
	out := make(map[string]struct{})
	for _, user := range uniqueTrimmedStrings(users) {
		out[user] = struct{}{}
	}
	return out
}

func splitWeixinCSV(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	parts := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ';' || r == '\n' || r == '\r' || r == '\t' || r == ' '
	})
	return uniqueTrimmedStrings(parts)
}

func firstNonEmptyString(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func firstNonEmptyStringList(values ...[]string) []string {
	for _, v := range values {
		if len(uniqueTrimmedStrings(v)) > 0 {
			return uniqueTrimmedStrings(v)
		}
	}
	return nil
}

func uniqueTrimmedStrings(values []string) []string {
	seen := make(map[string]struct{})
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func sanitizeWeixinBotAgent(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return weixinDefaultBotAgent
	}
	if len(raw) > 256 {
		raw = raw[:256]
	}
	var b strings.Builder
	for _, r := range raw {
		if r >= 32 && r <= 126 && r != '(' && r != ')' {
			b.WriteRune(r)
		}
	}
	out := strings.TrimSpace(b.String())
	if out == "" {
		return weixinDefaultBotAgent
	}
	return out
}

func sanitizeWeixinFileName(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		raw = "default"
	}
	var b strings.Builder
	for _, r := range raw {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
	}
	return strings.Trim(b.String(), "-")
}

func splitRunesForWeixin(s string, limit int) []string {
	if limit <= 0 {
		return []string{s}
	}
	runes := []rune(s)
	if len(runes) <= limit {
		return []string{s}
	}
	chunks := make([]string, 0, len(runes)/limit+1)
	for len(runes) > 0 {
		n := limit
		if len(runes) < n {
			n = len(runes)
		}
		chunks = append(chunks, string(runes[:n]))
		runes = runes[n:]
	}
	return chunks
}

func retryDelayForWeixin(failures int) time.Duration {
	if failures < 3 {
		return 2 * time.Second
	}
	return 30 * time.Second
}

func sleepWithContext(ctx context.Context, d time.Duration) {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}

func maskWeixinToken(token string) string {
	token = strings.TrimSpace(token)
	if token == "" {
		return ""
	}
	if len(token) <= 8 {
		return "****"
	}
	return token[:4] + "..." + token[len(token)-4:]
}

func truncateWeixinString(s string, limit int) string {
	if limit <= 0 || len(s) <= limit {
		return s
	}
	return s[:limit] + "..."
}

type WeixinMessageItem = weixinMessageItem
type WeixinTextItem = weixinTextItem
type WeixinVoiceItem = weixinVoiceItem
type WeixinQRStartOptions = weixinQRStartOptions
type WeixinQRStartResult = weixinQRStartResult
type WeixinQRWaitResult = weixinQRWaitResult

func WeixinExtractText(items []WeixinMessageItem) string { return weixinExtractText(items) }
func NormalizeWeixinQRImage(raw string) string           { return normalizeWeixinQRImage(raw) }
func GenerateWeixinQRScanDataURL(scanData string) string {
	return generateWeixinQRScanDataURL(scanData)
}
func StartWeixinQRLogin(ctx context.Context, opts WeixinQRStartOptions) (*WeixinQRStartResult, error) {
	return startWeixinQRLogin(ctx, opts)
}
func WaitWeixinQRLoginOnce(ctx context.Context, sessionKey, verifyCode string) (*WeixinQRWaitResult, error) {
	return waitWeixinQRLoginOnce(ctx, sessionKey, verifyCode)
}
func MaskWeixinToken(token string) string { return maskWeixinToken(token) }
func IsWeixinChannelType(value string) bool {
	return strings.EqualFold(strings.TrimSpace(value), "weixin") || strings.EqualFold(strings.TrimSpace(value), "openclaw-weixin")
}
func SplitWeixinCSV(value string) []string        { return splitWeixinCSV(value) }
func FirstNonEmptyString(values ...string) string { return firstNonEmptyString(values...) }
func FirstNonEmptyStringList(values ...[]string) []string {
	return firstNonEmptyStringList(values...)
}

func mapString(values map[string]interface{}, key, fallback string) string {
	value, ok := values[key]
	if !ok || value == nil {
		return fallback
	}
	if text, ok := value.(string); ok {
		return text
	}
	return fmt.Sprint(value)
}

func mapStringSlice(values map[string]interface{}, key string, fallback []string) []string {
	value, ok := values[key]
	if !ok || value == nil {
		return fallback
	}
	switch list := value.(type) {
	case []string:
		return append([]string(nil), list...)
	case []interface{}:
		result := make([]string, 0, len(list))
		for _, item := range list {
			result = append(result, strings.TrimSpace(fmt.Sprint(item)))
		}
		return result
	case string:
		return splitWeixinCSV(list)
	default:
		return fallback
	}
}

func mapBool(values map[string]interface{}, key string, fallback bool) bool {
	value, ok := values[key]
	if !ok || value == nil {
		return fallback
	}
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		parsed, err := strconv.ParseBool(strings.TrimSpace(typed))
		if err == nil {
			return parsed
		}
	}
	return fallback
}

func mapInt(values map[string]interface{}, key string, fallback int) int {
	value, ok := values[key]
	if !ok || value == nil {
		return fallback
	}
	switch typed := value.(type) {
	case int:
		return typed
	case float64:
		return int(typed)
	case string:
		parsed, err := strconv.Atoi(strings.TrimSpace(typed))
		if err == nil {
			return parsed
		}
	}
	return fallback
}
