package channels

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/mail"
	"net/smtp"
	"strings"
	"sync"
	"time"

	"github.com/emersion/go-imap"
	"github.com/emersion/go-imap/client"
	"github.com/gomarkdown/markdown"
	"github.com/gomarkdown/markdown/html"
	"github.com/gomarkdown/markdown/parser"
)

type EmailChannelConfig struct {
	Enabled       bool   `json:"enabled" yaml:"enabled"`
	SMTPHost      string `json:"smtp_host" yaml:"smtp_host"`
	SMTPPort      int    `json:"smtp_port" yaml:"smtp_port"`
	IMAPHost      string `json:"imap_host" yaml:"imap_host"`
	IMAPPort      int    `json:"imap_port" yaml:"imap_port"`
	Username      string `json:"username" yaml:"username"`
	Password      string `json:"password" yaml:"password"`
	FromAddress   string `json:"from_address" yaml:"from_address"`
	FromName      string `json:"from_name" yaml:"from_name"`
	Supervisor    string `json:"supervisor" yaml:"supervisor"`
	UseTLS        bool   `json:"use_tls" yaml:"use_tls"`
	CheckInterval int    `json:"check_interval" yaml:"check_interval"`
	MonitorFolder string `json:"monitor_folder" yaml:"monitor_folder"`
}

// EmailMessage is a decoded inbound email, independent of the IMAP library.
type EmailMessage struct {
	From         string
	Subject      string
	Body         string
	MessageID    string
	IsSupervisor bool
}

// EmailChannel owns IMAP polling, MIME decoding, SMTP delivery and HTML email
// rendering. Agent and persistence behavior is supplied by the application.
type EmailChannel struct {
	*Base
	config         EmailChannelConfig
	imapClient     *client.Client
	mu             sync.RWMutex
	processedIDs   map[string]bool
	ctx            context.Context
	cancel         context.CancelFunc
	messageHandler func(EmailMessage)
}

func NewEmailChannel(name string, config EmailChannelConfig) *EmailChannel {
	ctx, cancel := context.WithCancel(context.Background())
	return &EmailChannel{
		Base:         NewBase(name, "email"),
		config:       config,
		processedIDs: make(map[string]bool),
		ctx:          ctx,
		cancel:       cancel,
	}
}

func (channel *EmailChannel) SetMessageHandler(handler func(EmailMessage)) {
	channel.messageHandler = handler
}

func (channel *EmailChannel) AgentContext(sessionID, taskID, userID string) context.Context {
	return AgentContext(channel.ctx, taskID, sessionID, channel.Name(), userID)
}

func (channel *EmailChannel) Start() error {
	if err := channel.connectIMAP(); err != nil {
		return err
	}
	interval := channel.config.CheckInterval
	if interval <= 0 {
		interval = 30
	}
	go channel.monitorEmails(interval)
	return nil
}

func (channel *EmailChannel) connectIMAP() error {
	channel.mu.Lock()
	defer channel.mu.Unlock()
	address := fmt.Sprintf("%s:%d", channel.config.IMAPHost, channel.config.IMAPPort)
	var (
		imapClient *client.Client
		err        error
	)
	if channel.config.IMAPPort == 993 {
		imapClient, err = client.DialTLS(address, &tls.Config{
			ServerName: channel.config.IMAPHost, MinVersion: tls.VersionTLS12,
		})
	} else {
		imapClient, err = client.Dial(address)
	}
	if err != nil {
		return fmt.Errorf("connect IMAP: %w", err)
	}
	if err := imapClient.Login(channel.config.Username, channel.config.Password); err != nil {
		_ = imapClient.Close()
		return fmt.Errorf("login IMAP: %w", err)
	}
	channel.imapClient = imapClient
	return nil
}

func (channel *EmailChannel) monitorEmails(interval int) {
	ticker := time.NewTicker(time.Duration(interval) * time.Second)
	defer ticker.Stop()
	channel.checkNewEmails()
	for {
		select {
		case <-channel.ctx.Done():
			return
		case <-ticker.C:
			channel.checkNewEmails()
		}
	}
}

func (channel *EmailChannel) checkNewEmails() {
	channel.mu.RLock()
	connected := channel.imapClient != nil
	channel.mu.RUnlock()
	if !connected {
		if err := channel.connectIMAP(); err != nil {
			channel.logger.Printf("IMAP reconnect failed: %v", err)
			return
		}
	}

	folder := channel.config.MonitorFolder
	if folder == "" {
		folder = "INBOX"
	}
	channel.mu.Lock()
	mailbox, err := channel.imapClient.Select(folder, false)
	channel.mu.Unlock()
	if err != nil || mailbox.Messages == 0 {
		if err != nil {
			channel.logger.Printf("select IMAP folder %s: %v", folder, err)
		}
		return
	}

	sequenceSet := new(imap.SeqSet)
	start := uint32(1)
	if mailbox.Messages > 10 {
		start = mailbox.Messages - 9
	}
	sequenceSet.AddRange(start, mailbox.Messages)
	messages := make(chan *imap.Message, 10)
	done := make(chan error, 1)
	channel.mu.RLock()
	imapClient := channel.imapClient
	channel.mu.RUnlock()
	go func() {
		done <- imapClient.Fetch(sequenceSet, []imap.FetchItem{imap.FetchEnvelope, imap.FetchFlags, imap.FetchRFC822}, messages)
	}()
	for message := range messages {
		channel.processIncomingMessage(message)
	}
	if err := <-done; err != nil {
		channel.logger.Printf("fetch email: %v", err)
	}
}

func (channel *EmailChannel) processIncomingMessage(message *imap.Message) {
	if message == nil || message.Envelope == nil {
		return
	}
	messageID := message.Envelope.MessageId
	if messageID == "" {
		messageID = fmt.Sprintf("%d", message.SeqNum)
	}
	channel.mu.RLock()
	alreadyProcessed := channel.processedIDs[messageID]
	channel.mu.RUnlock()
	if alreadyProcessed || containsIMAPFlag(message.Flags, imap.SeenFlag) || channel.isAutoReply(message) {
		return
	}

	from := extractIMAPAddress(message.Envelope.From)
	subject := decodeEmailSubject(message.Envelope.Subject)
	body := channel.extractBody(message)
	if body == "" {
		body = "(无内容)"
	}
	channel.mu.Lock()
	channel.processedIDs[messageID] = true
	channel.mu.Unlock()
	incoming := EmailMessage{
		From: from, Subject: subject, Body: body, MessageID: messageID,
		IsSupervisor: channel.isSupervisor(from),
	}
	if channel.messageHandler != nil {
		channel.messageHandler(incoming)
	}
}

func containsIMAPFlag(flags []string, expected string) bool {
	for _, flag := range flags {
		if flag == expected {
			return true
		}
	}
	return false
}

func extractEmailAddress(address string) string {
	parsed, err := mail.ParseAddress(address)
	if err == nil && parsed.Address != "" {
		return parsed.Address
	}
	return strings.TrimSpace(address)
}

func (channel *EmailChannel) isSupervisor(from string) bool {
	return channel.config.Supervisor != "" && strings.EqualFold(
		extractEmailAddress(from), extractEmailAddress(channel.config.Supervisor),
	)
}

func (channel *EmailChannel) isAutoReply(message *imap.Message) bool {
	if message.Envelope == nil {
		return false
	}
	subject := strings.ToLower(decodeEmailSubject(message.Envelope.Subject))
	for _, keyword := range []string{"auto-reply", "automatic reply", "out of office", "自动回复", "外出", "不在办公室"} {
		if strings.Contains(subject, keyword) {
			return true
		}
	}
	for _, from := range message.Envelope.From {
		if strings.Contains(strings.ToLower(from.Address()), strings.ToLower(channel.config.Username)) {
			return true
		}
	}
	return false
}

// SendReply sends a MIME reply and preserves the source Message-ID.
func (channel *EmailChannel) SendReply(to, subject, content, inReplyTo string) error {
	return channel.sendEmail(to, subject, content, inReplyTo)
}

func (channel *EmailChannel) SendMessage(recipient, content string) error {
	return channel.sendEmail(recipient, "来自 AI 助手的消息", content, "")
}

func (channel *EmailChannel) sendEmail(to, subject, content, replyToID string) error {
	if strings.TrimSpace(to) == "" {
		return fmt.Errorf("empty email recipient")
	}
	message, err := channel.buildEmail(to, subject, content, generateEmailHTML(content), replyToID)
	if err != nil {
		return err
	}
	auth := smtp.PlainAuth("", channel.config.Username, channel.config.Password, channel.config.SMTPHost)
	address := fmt.Sprintf("%s:%d", channel.config.SMTPHost, channel.config.SMTPPort)
	if channel.config.SMTPPort != 465 {
		return smtp.SendMail(address, auth, channel.config.Username, []string{to}, []byte(message))
	}
	connection, err := tls.Dial("tcp", address, &tls.Config{
		ServerName: channel.config.SMTPHost, MinVersion: tls.VersionTLS12,
	})
	if err != nil {
		return err
	}
	defer connection.Close()
	smtpClient, err := smtp.NewClient(connection, channel.config.SMTPHost)
	if err != nil {
		return err
	}
	defer smtpClient.Close()
	if err := smtpClient.Auth(auth); err != nil {
		return err
	}
	if err := smtpClient.Mail(channel.config.Username); err != nil {
		return err
	}
	if err := smtpClient.Rcpt(to); err != nil {
		return err
	}
	writer, err := smtpClient.Data()
	if err != nil {
		return err
	}
	if _, err := writer.Write([]byte(message)); err != nil {
		_ = writer.Close()
		return err
	}
	return writer.Close()
}

func (channel *EmailChannel) buildEmail(to, subject, plainText, htmlContent, inReplyTo string) (string, error) {
	var buffer bytes.Buffer
	fromAddress := channel.config.FromAddress
	if fromAddress == "" {
		fromAddress = channel.config.Username
	}
	from := mail.Address{Name: channel.config.FromName, Address: fromAddress}
	recipient := mail.Address{Address: to}
	buffer.WriteString(fmt.Sprintf("From: %s\r\n", from.String()))
	buffer.WriteString(fmt.Sprintf("To: %s\r\n", recipient.String()))
	buffer.WriteString(fmt.Sprintf("Subject: %s\r\n", mime.BEncoding.Encode("UTF-8", subject)))
	buffer.WriteString("MIME-Version: 1.0\r\n")
	if inReplyTo != "" {
		buffer.WriteString(fmt.Sprintf("In-Reply-To: <%s>\r\nReferences: <%s>\r\n", inReplyTo, inReplyTo))
	}
	buffer.WriteString("Content-Type: multipart/alternative; boundary=altBoundary\r\n\r\n")
	buffer.WriteString("--altBoundary\r\nContent-Type: text/plain; charset=UTF-8\r\nContent-Transfer-Encoding: quoted-printable\r\n\r\n")
	plainWriter := quotedprintable.NewWriter(&buffer)
	if _, err := plainWriter.Write([]byte(plainText)); err != nil {
		return "", err
	}
	if err := plainWriter.Close(); err != nil {
		return "", err
	}
	buffer.WriteString("\r\n--altBoundary\r\nContent-Type: text/html; charset=UTF-8\r\nContent-Transfer-Encoding: quoted-printable\r\n\r\n")
	htmlWriter := quotedprintable.NewWriter(&buffer)
	if _, err := htmlWriter.Write([]byte(htmlContent)); err != nil {
		return "", err
	}
	if err := htmlWriter.Close(); err != nil {
		return "", err
	}
	buffer.WriteString("\r\n--altBoundary--\r\n")
	return buffer.String(), nil
}

func generateEmailHTML(content string) string {
	extensions := parser.CommonExtensions | parser.AutoHeadingIDs | parser.Tables | parser.FencedCode | parser.Strikethrough
	document := parser.NewWithExtensions(extensions).Parse([]byte(content))
	renderer := html.NewRenderer(html.RendererOptions{Flags: html.CommonFlags | html.HrefTargetBlank})
	body := string(markdown.Render(document, renderer))
	style := `<style>
body{font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Helvetica,Arial,sans-serif;line-height:1.6;color:#24292e;max-width:800px;margin:0 auto;padding:20px}.container{background:#fff;border-radius:8px;padding:30px}h1,h2,h3,h4{color:#24292e;margin-top:24px;margin-bottom:16px}code{background:#f6f8fa;border-radius:3px;padding:.2em .4em;font-family:monospace}pre{background:#f6f8fa;border-radius:6px;padding:16px;overflow:auto}blockquote{border-left:4px solid #dfe2e5;color:#6a737d;padding-left:1em;margin-left:0}table{border-collapse:collapse;width:100%;margin-bottom:16px}th,td{border:1px solid #e1e4e8;padding:8px 12px;text-align:left}th{background:#f6f8fa}.footer{margin-top:30px;padding-top:20px;border-top:1px solid #e1e4e8;font-size:12px;color:#6a737d;text-align:center}
</style>`
	return fmt.Sprintf(`<!DOCTYPE html><html><head><meta charset="UTF-8">%s</head><body><div class="container">%s<div class="footer">由 AI 助手自动回复</div></div></body></html>`, style, body)
}

func (channel *EmailChannel) extractBody(message *imap.Message) string {
	section := imap.BodySectionName{Peek: true}
	reader := message.GetBody(&section)
	if reader == nil {
		return ""
	}
	raw, err := io.ReadAll(reader)
	if err != nil {
		return ""
	}
	parsed, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		return string(raw)
	}
	body := parseEmailBody(parsed, parsed.Header.Get("Content-Type"))
	if body == "" {
		body = readEmailPart(parsed.Body, parsed.Header.Get("Content-Transfer-Encoding"))
	}
	return cleanEmailText(body)
}

func parseEmailBody(message *mail.Message, contentType string) string {
	if strings.Contains(contentType, "multipart/") {
		_, parameters, err := mime.ParseMediaType(contentType)
		if err != nil || parameters["boundary"] == "" {
			return ""
		}
		reader := multipart.NewReader(message.Body, parameters["boundary"])
		for {
			part, err := reader.NextPart()
			if err == io.EOF {
				return ""
			}
			if err != nil {
				continue
			}
			if strings.Contains(part.Header.Get("Content-Type"), "text/plain") {
				return readEmailPart(part, part.Header.Get("Content-Transfer-Encoding"))
			}
		}
	}
	if strings.Contains(contentType, "text/plain") {
		return readEmailPart(message.Body, message.Header.Get("Content-Transfer-Encoding"))
	}
	return ""
}

func readEmailPart(reader io.Reader, transferEncoding string) string {
	content, err := io.ReadAll(reader)
	if err != nil {
		return ""
	}
	if transferEncoding == "quoted-printable" {
		decoded, err := io.ReadAll(quotedprintable.NewReader(bytes.NewReader(content)))
		if err == nil {
			content = decoded
		}
	}
	return string(content)
}

func cleanEmailText(text string) string {
	lines := strings.Split(text, "\n")
	cleaned := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" || (len(cleaned) > 0 && cleaned[len(cleaned)-1] != "") {
			cleaned = append(cleaned, trimmed)
		}
	}
	return strings.TrimSpace(strings.Join(cleaned, "\n"))
}

func extractIMAPAddress(addresses []*imap.Address) string {
	if len(addresses) == 0 {
		return ""
	}
	address := addresses[0]
	if address.PersonalName != "" {
		return fmt.Sprintf("%s <%s@%s>", address.PersonalName, address.MailboxName, address.HostName)
	}
	return fmt.Sprintf("%s@%s", address.MailboxName, address.HostName)
}

func decodeEmailSubject(subject string) string {
	decoded, err := (&mime.WordDecoder{CharsetReader: func(_ string, input io.Reader) (io.Reader, error) {
		return input, nil
	}}).DecodeHeader(subject)
	if err != nil {
		return subject
	}
	return decoded
}

func (channel *EmailChannel) Stop() error {
	if channel.cancel != nil {
		channel.cancel()
	}
	channel.mu.Lock()
	defer channel.mu.Unlock()
	if channel.imapClient == nil {
		return nil
	}
	if err := channel.imapClient.Logout(); err != nil {
		_ = channel.imapClient.Close()
		return err
	}
	return channel.imapClient.Close()
}
