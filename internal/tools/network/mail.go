package network

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"io"
	"log"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	stdmail "net/mail"
	"net/smtp"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/emersion/go-imap"
	"github.com/emersion/go-imap/client"
	"github.com/gomarkdown/markdown"
	"github.com/gomarkdown/markdown/html"
	"github.com/gomarkdown/markdown/parser"
)

type EmailConfig struct {
	Enabled     bool   `json:"enabled" yaml:"enabled"`
	SMTPHost    string `json:"smtp_host" yaml:"smtp_host"`
	SMTPPort    int    `json:"smtp_port" yaml:"smtp_port"`
	IMAPHost    string `json:"imap_host" yaml:"imap_host"`
	IMAPPort    int    `json:"imap_port" yaml:"imap_port"`
	Username    string `json:"username" yaml:"username"`
	Password    string `json:"password" yaml:"password"`
	FromAddress string `json:"from_address" yaml:"from_address"`
	UseTLS      bool   `json:"use_tls" yaml:"use_tls"`
	FromName    string `json:"from_name" yaml:"from_name"`
}

type EmailTool struct {
	config *EmailConfig
	logger *log.Logger
	mu     sync.Mutex
}

func NewEmailTool(config *EmailConfig, logger *log.Logger) *EmailTool {
	return &EmailTool{
		config: config,
		logger: logger,
	}
}

func (t *EmailTool) Name() string { return "email" }

func (t *EmailTool) Description() string {
	return `邮件收发工具，支持发送和接收邮件，自动美化 Markdown 内容。

功能：
- send: 发送邮件（支持 Markdown 格式，自动转换为美观的 HTML，支持附件）
- receive: 接收邮件（查看收件箱）
- list: 列出邮件列表
- read: 读取指定邮件

需要先在配置文件中配置邮箱信息。`
}

func (t *EmailTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"action": map[string]interface{}{
				"type":        "string",
				"enum":        []string{"send", "receive", "list", "read"},
				"description": "操作类型",
			},
			"to": map[string]interface{}{
				"type":        "string",
				"description": "收件人地址（send时必填）",
			},
			"subject": map[string]interface{}{
				"type":        "string",
				"description": "邮件主题（send时必填）",
			},
			"body": map[string]interface{}{
				"type":        "string",
				"description": "邮件正文，支持 Markdown 格式（send时必填）",
			},
			"cc": map[string]interface{}{
				"type":        "string",
				"description": "抄送地址",
			},
			"bcc": map[string]interface{}{
				"type":        "string",
				"description": "密送地址",
			},
			"attachments": map[string]interface{}{
				"type":        "array",
				"description": "附件路径列表，支持本地文件路径",
				"items": map[string]interface{}{
					"type": "string",
				},
			},
			"is_markdown": map[string]interface{}{
				"type":        "boolean",
				"description": "是否为 Markdown 格式（默认自动检测）",
			},
			"summary_info": map[string]interface{}{
				"type":        "string",
				"description": "邮件摘要信息，会显示在邮件顶部",
			},
			"limit": map[string]interface{}{
				"type":        "integer",
				"description": "获取邮件数量（默认10）",
				"minimum":     1,
				"maximum":     100,
			},
			"folder": map[string]interface{}{
				"type":        "string",
				"description": "邮件文件夹（默认INBOX）",
			},
			"unread_only": map[string]interface{}{
				"type":        "boolean",
				"description": "仅显示未读邮件",
			},
			"message_id": map[string]interface{}{
				"type":        "integer",
				"description": "邮件序号（read时使用）",
			},
		},
		"required": []string{"action"},
	}
}

func (t *EmailTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	if t.config == nil || !t.config.Enabled {
		return "", fmt.Errorf("邮件服务未配置或未启用，请在配置文件中添加 email_config")
	}

	action, _ := params["action"].(string)

	switch action {
	case "send":
		return t.sendEmail(params)
	case "receive", "list":
		return t.listEmails(params)
	case "read":
		return t.readEmail(params)
	default:
		return "", fmt.Errorf("不支持的操作: %s", action)
	}
}

// generateHTMLContent 生成美观的 HTML 邮件内容
func (t *EmailTool) generateHTMLContent(body string, isMarkdown bool, summaryInfo string) string {
	style := `
	<style>
		body {
			font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Helvetica, Arial, sans-serif;
			line-height: 1.6;
			color: #24292e;
			max-width: 800px;
			margin: 0 auto;
			padding: 20px;
			background-color: #f6f8fa;
		}
		.email-container {
			background-color: #ffffff;
			border-radius: 8px;
			padding: 30px;
			box-shadow: 0 1px 3px rgba(0,0,0,0.1);
		}
		.summary-box {
			background: linear-gradient(135deg, #667eea 0%, #764ba2 100%);
			color: white;
			padding: 15px 20px;
			border-radius: 8px;
			margin-bottom: 25px;
			font-size: 14px;
		}
		h1 { 
			color: #24292e;
			border-bottom: 2px solid #e1e4e8;
			padding-bottom: 0.3em;
			margin-top: 24px;
			margin-bottom: 16px;
			font-size: 2em;
		}
		h2 {
			color: #24292e;
			border-bottom: 1px solid #e1e4e8;
			padding-bottom: 0.3em;
			margin-top: 24px;
			margin-bottom: 16px;
			font-size: 1.5em;
		}
		h3 { 
			color: #24292e;
			margin-top: 24px;
			margin-bottom: 16px;
			font-size: 1.25em;
		}
		h4 { 
			color: #24292e;
			margin-top: 24px;
			margin-bottom: 16px;
			font-size: 1em;
		}
		p { 
			margin-bottom: 16px;
			font-size: 14px;
		}
		code {
			background-color: #f6f8fa;
			border-radius: 3px;
			font-family: 'SF Mono', 'Monaco', 'Consolas', monospace;
			font-size: 85%;
			padding: 0.2em 0.4em;
		}
		pre {
			background-color: #f6f8fa;
			border-radius: 6px;
			padding: 16px;
			overflow: auto;
			font-size: 85%;
			line-height: 1.45;
		}
		pre code {
			background-color: transparent;
			padding: 0;
		}
		blockquote {
			border-left: 4px solid #dfe2e5;
			color: #6a737d;
			padding-left: 1em;
			margin-left: 0;
		}
		table {
			border-collapse: collapse;
			width: 100%;
			margin-bottom: 16px;
		}
		th, td {
			border: 1px solid #e1e4e8;
			padding: 8px 12px;
			text-align: left;
		}
		th {
			background-color: #f6f8fa;
			font-weight: 600;
		}
		tr:nth-child(even) {
			background-color: #f6f8fa;
		}
		ul, ol {
			padding-left: 2em;
			margin-bottom: 16px;
		}
		li {
			margin-bottom: 4px;
		}
		hr {
			border: none;
			border-top: 1px solid #e1e4e8;
			margin: 24px 0;
		}
		a {
			color: #0366d6;
			text-decoration: none;
		}
		a:hover {
			text-decoration: underline;
		}
		.attachments-note {
			margin-top: 30px;
			padding: 15px;
			background-color: #f0f7ff;
			border-left: 4px solid #0366d6;
			border-radius: 4px;
			font-size: 13px;
			color: #24292e;
		}
		.attachment-item {
			display: inline-block;
			margin: 5px;
			padding: 5px 10px;
			background-color: #f6f8fa;
			border: 1px solid #e1e4e8;
			border-radius: 4px;
			font-size: 12px;
		}
		.footer {
			margin-top: 30px;
			padding-top: 20px;
			border-top: 1px solid #e1e4e8;
			font-size: 12px;
			color: #6a737d;
			text-align: center;
		}
		.badge {
			display: inline-block;
			padding: 2px 6px;
			font-size: 12px;
			font-weight: 500;
			background-color: #f1f8ff;
			color: #0366d6;
			border-radius: 3px;
		}
	</style>
	`

	var bodyContent string

	if isMarkdown || t.isMarkdownContent(body) {
		bodyContent = t.markdownToHTML(body)
	} else {
		bodyContent = fmt.Sprintf("<pre style='white-space: pre-wrap; font-family: inherit;'>%s</pre>",
			strings.ReplaceAll(htmlEscape(body), "\n", "<br>"))
	}

	summaryHTML := ""
	if summaryInfo != "" {
		summaryHTML = fmt.Sprintf(`<div class="summary-box">%s</div>`, htmlEscape(summaryInfo))
	}

	htmlContent := fmt.Sprintf(`<!DOCTYPE html>
<html>
<head>
	<meta charset="UTF-8">
	<meta name="viewport" content="width=device-width, initial-scale=1.0">
	%s
</head>
<body>
	<div class="email-container">
		%s
		%s
		<div class="footer">
			<span class="badge">✨ 由系统自动发送</span>
		</div>
	</div>
</body>
</html>`, style, summaryHTML, bodyContent)

	return htmlContent
}

// markdownToHTML 将 Markdown 转换为 HTML
func (t *EmailTool) markdownToHTML(md string) string {
	extensions := parser.CommonExtensions |
		parser.AutoHeadingIDs |
		parser.NoEmptyLineBeforeBlock |
		parser.Tables |
		parser.FencedCode |
		parser.Strikethrough |
		parser.SpaceHeadings |
		parser.DefinitionLists |
		parser.Footnotes

	p := parser.NewWithExtensions(extensions)
	doc := p.Parse([]byte(md))

	htmlFlags := html.CommonFlags | html.HrefTargetBlank
	opts := html.RendererOptions{
		Flags:          htmlFlags,
		RenderNodeHook: nil,
	}
	renderer := html.NewRenderer(opts)

	htmlBytes := markdown.Render(doc, renderer)
	return string(htmlBytes)
}

// isMarkdownContent 检测内容是否为 Markdown 格式
func (t *EmailTool) isMarkdownContent(content string) bool {
	markdownPatterns := []string{
		`^#{1,6}\s`,
		`^[\*\-+]\s`,
		`^\d+\.\s`,
		`\[.+\]\(.+\)`,
		`!\[.+\]\(.+\)`,
		`^>\s`,
		"```",
		"`[^`]+`",
		`^\|\s*.+\s*\|`,
		`^---$`,
		`\*\*[^*]+\*\*`,
		`\*[^*]+\*`,
	}

	lines := strings.Split(content, "\n")
	for i := 0; i < len(lines) && i < 10; i++ {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		for _, pattern := range markdownPatterns {
			if matched, _ := regexp.MatchString(pattern, line); matched {
				return true
			}
		}
	}

	markdownCount := 0
	for _, pattern := range markdownPatterns {
		if matched, _ := regexp.MatchString(pattern, content); matched {
			markdownCount++
		}
	}

	return markdownCount >= 2
}

// buildFinalEmail 最终构建邮件（支持附件）
func (t *EmailTool) buildFinalEmail(to, subject, plainText, htmlContent string, attachments []string, cc, bcc string) ([]byte, error) {
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)

	// 邮件头
	from := stdmail.Address{Name: t.config.FromName, Address: t.config.FromAddress}
	toAddr := stdmail.Address{Address: to}

	buf.WriteString(fmt.Sprintf("From: %s\r\n", from.String()))
	buf.WriteString(fmt.Sprintf("To: %s\r\n", toAddr.String()))
	buf.WriteString(fmt.Sprintf("Subject: %s\r\n", mime.BEncoding.Encode("UTF-8", subject)))

	if cc != "" {
		ccAddr := stdmail.Address{Address: cc}
		buf.WriteString(fmt.Sprintf("Cc: %s\r\n", ccAddr.String()))
	}
	if bcc != "" {
		bccAddr := stdmail.Address{Address: bcc}
		buf.WriteString(fmt.Sprintf("Bcc: %s\r\n", bccAddr.String()))
	}

	buf.WriteString("MIME-Version: 1.0\r\n")
	buf.WriteString(fmt.Sprintf("Content-Type: multipart/mixed; boundary=%s\r\n", writer.Boundary()))
	buf.WriteString("\r\n")

	// 创建 multipart/alternative 部分（包含纯文本和HTML）
	altWriter := multipart.NewWriter(&buf)
	altPart, err := writer.CreatePart(map[string][]string{
		"Content-Type": {fmt.Sprintf("multipart/alternative; boundary=%s", altWriter.Boundary())},
	})
	if err != nil {
		return nil, fmt.Errorf("创建 alternative 部分失败: %v", err)
	}

	// 纯文本部分
	textPart, err := altWriter.CreatePart(map[string][]string{
		"Content-Type":              {"text/plain; charset=UTF-8"},
		"Content-Transfer-Encoding": {"quoted-printable"},
	})
	if err != nil {
		return nil, fmt.Errorf("创建纯文本部分失败: %v", err)
	}
	qpWriter := quotedprintable.NewWriter(textPart)
	qpWriter.Write([]byte(plainText))
	qpWriter.Close()

	// HTML 部分
	htmlPart, err := altWriter.CreatePart(map[string][]string{
		"Content-Type":              {"text/html; charset=UTF-8"},
		"Content-Transfer-Encoding": {"quoted-printable"},
	})
	if err != nil {
		return nil, fmt.Errorf("创建HTML部分失败: %v", err)
	}
	htmlQPWriter := quotedprintable.NewWriter(htmlPart)
	htmlQPWriter.Write([]byte(htmlContent))
	htmlQPWriter.Close()

	altWriter.Close()

	// 添加附件
	for _, attachmentPath := range attachments {
		filename := filepath.Base(attachmentPath)

		fileData, err := os.ReadFile(attachmentPath)
		if err != nil {
			return nil, fmt.Errorf("读取附件 %s 失败: %v", attachmentPath, err)
		}

		mimeType := mime.TypeByExtension(filepath.Ext(attachmentPath))
		if mimeType == "" {
			mimeType = "application/octet-stream"
		}

		attachmentPart, err := writer.CreatePart(map[string][]string{
			"Content-Type":              {fmt.Sprintf(`%s; name="%s"`, mimeType, filename)},
			"Content-Transfer-Encoding": {"base64"},
			"Content-Disposition":       {fmt.Sprintf(`attachment; filename="%s"`, filename)},
		})
		if err != nil {
			return nil, fmt.Errorf("创建附件部分失败: %v", err)
		}

		encoder := base64.NewEncoder(base64.StdEncoding, attachmentPart)
		encoder.Write(fileData)
		encoder.Close()
	}

	// 关闭 multipart writer 以写入结束边界
	// 注意：先关闭 altWriter 完成 alternative 部分的写入
	altPart.Write(buf.Bytes())

	// 重新构建正确格式的缓冲区
	// 由于 multipart.Writer 的嵌套使用比较复杂，这里重新构建
	finalBuf, err := t.rebuildMultipartEmail(to, subject, plainText, htmlContent, attachments, cc, bcc)
	if err != nil {
		return nil, err
	}

	return finalBuf, nil
}

// rebuildMultipartEmail 重新构建 multipart 邮件（修正版）
func (t *EmailTool) rebuildMultipartEmail(to, subject, plainText, htmlContent string, attachments []string, cc, bcc string) ([]byte, error) {
	var buf bytes.Buffer

	// 创建主 multipart/mixed writer
	writer := multipart.NewWriter(&buf)
	boundary := writer.Boundary()

	// 写入邮件头
	from := stdmail.Address{Name: t.config.FromName, Address: t.config.FromAddress}
	toAddr := stdmail.Address{Address: to}

	buf.WriteString(fmt.Sprintf("From: %s\r\n", from.String()))
	buf.WriteString(fmt.Sprintf("To: %s\r\n", toAddr.String()))
	buf.WriteString(fmt.Sprintf("Subject: %s\r\n", mime.BEncoding.Encode("UTF-8", subject)))

	if cc != "" {
		ccAddr := stdmail.Address{Address: cc}
		buf.WriteString(fmt.Sprintf("Cc: %s\r\n", ccAddr.String()))
	}
	if bcc != "" {
		bccAddr := stdmail.Address{Address: bcc}
		buf.WriteString(fmt.Sprintf("Bcc: %s\r\n", bccAddr.String()))
	}

	buf.WriteString("MIME-Version: 1.0\r\n")
	buf.WriteString(fmt.Sprintf("Content-Type: multipart/mixed; boundary=%s\r\n", boundary))
	buf.WriteString("\r\n")

	// 创建 multipart/alternative 部分
	altWriter := multipart.NewWriter(&buf)
	altBoundary := altWriter.Boundary()

	// 手动写入 alternative boundary
	buf.WriteString(fmt.Sprintf("--%s\r\n", boundary))
	buf.WriteString(fmt.Sprintf("Content-Type: multipart/alternative; boundary=%s\r\n", altBoundary))
	buf.WriteString("\r\n")

	// 纯文本部分
	buf.WriteString(fmt.Sprintf("--%s\r\n", altBoundary))
	buf.WriteString("Content-Type: text/plain; charset=UTF-8\r\n")
	buf.WriteString("Content-Transfer-Encoding: quoted-printable\r\n")
	buf.WriteString("\r\n")

	qpWriter := quotedprintable.NewWriter(&buf)
	qpWriter.Write([]byte(plainText))
	qpWriter.Close()
	buf.WriteString("\r\n")

	// HTML 部分
	buf.WriteString(fmt.Sprintf("--%s\r\n", altBoundary))
	buf.WriteString("Content-Type: text/html; charset=UTF-8\r\n")
	buf.WriteString("Content-Transfer-Encoding: quoted-printable\r\n")
	buf.WriteString("\r\n")

	htmlQPWriter := quotedprintable.NewWriter(&buf)
	htmlQPWriter.Write([]byte(htmlContent))
	htmlQPWriter.Close()
	buf.WriteString("\r\n")

	// 结束 alternative
	buf.WriteString(fmt.Sprintf("--%s--\r\n", altBoundary))

	// 添加附件
	for _, attachmentPath := range attachments {
		filename := filepath.Base(attachmentPath)

		fileData, err := os.ReadFile(attachmentPath)
		if err != nil {
			return nil, fmt.Errorf("读取附件 %s 失败: %v", attachmentPath, err)
		}

		mimeType := mime.TypeByExtension(filepath.Ext(attachmentPath))
		if mimeType == "" {
			mimeType = "application/octet-stream"
		}

		buf.WriteString(fmt.Sprintf("--%s\r\n", boundary))
		buf.WriteString(fmt.Sprintf("Content-Type: %s; name=\"%s\"\r\n", mimeType, filename))
		buf.WriteString("Content-Transfer-Encoding: base64\r\n")
		buf.WriteString(fmt.Sprintf("Content-Disposition: attachment; filename=\"%s\"\r\n", filename))
		buf.WriteString("\r\n")

		// Base64 编码（每76个字符换行）
		encoded := base64.StdEncoding.EncodeToString(fileData)
		for i := 0; i < len(encoded); i += 76 {
			end := i + 76
			if end > len(encoded) {
				end = len(encoded)
			}
			buf.WriteString(encoded[i:end] + "\r\n")
		}
	}

	// 结束主 boundary
	buf.WriteString(fmt.Sprintf("--%s--\r\n", boundary))

	return buf.Bytes(), nil
}

// addAttachmentsNote 在 HTML 中添加附件提示
func (t *EmailTool) addAttachmentsNote(htmlContent string, attachmentNames []string) string {
	if len(attachmentNames) == 0 {
		return htmlContent
	}

	attachmentsHTML := `<div class="attachments-note">
		<strong>📎 附件列表：</strong><br>`
	for _, name := range attachmentNames {
		attachmentsHTML += fmt.Sprintf(`<span class="attachment-item">📄 %s</span>`, htmlEscape(name))
	}
	attachmentsHTML += `</div>`

	// 在 footer 之前插入附件信息
	htmlContent = strings.Replace(htmlContent, `<div class="footer">`, attachmentsHTML+`<div class="footer">`, 1)
	return htmlContent
}

// sendEmail 发送邮件（支持附件）
func (t *EmailTool) sendEmail(params map[string]interface{}) (string, error) {
	to, _ := params["to"].(string)
	subject, _ := params["subject"].(string)
	body, _ := params["body"].(string)
	cc, _ := params["cc"].(string)
	bcc, _ := params["bcc"].(string)
	summaryInfo, _ := params["summary_info"].(string)
	isMarkdown, hasMarkdownParam := params["is_markdown"].(bool)

	// 获取附件列表
	var attachments []string
	if attPaths, ok := params["attachments"].([]interface{}); ok {
		for _, path := range attPaths {
			if pathStr, ok := path.(string); ok {
				attachments = append(attachments, pathStr)
			}
		}
	}

	if to == "" || subject == "" || body == "" {
		return "", fmt.Errorf("发送邮件需要 to, subject, body 参数")
	}

	// 验证附件
	for _, attachmentPath := range attachments {
		if _, err := os.Stat(attachmentPath); os.IsNotExist(err) {
			return "", fmt.Errorf("附件不存在: %s", attachmentPath)
		}
	}

	// 判断是否为 Markdown
	var useMarkdown bool
	if hasMarkdownParam {
		useMarkdown = isMarkdown
	} else {
		useMarkdown = t.isMarkdownContent(body)
	}

	// 生成 HTML 内容
	htmlContent := t.generateHTMLContent(body, useMarkdown, summaryInfo)

	// 构建纯文本版本
	plainText := body
	if useMarkdown {
		plainText = t.markdownToPlainText(body)
	}

	// 获取附件名称列表用于HTML显示
	attachmentNames := make([]string, len(attachments))
	for i, path := range attachments {
		attachmentNames[i] = filepath.Base(path)
	}

	// 在HTML中添加附件信息
	htmlContent = t.addAttachmentsNote(htmlContent, attachmentNames)

	// 构建邮件
	var msg []byte
	var err error

	if len(attachments) > 0 {
		msg, err = t.rebuildMultipartEmail(to, subject, plainText, htmlContent, attachments, cc, bcc)
	} else {
		msg, err = t.buildSimpleEmail(to, subject, plainText, htmlContent, cc, bcc)
	}

	if err != nil {
		return "", fmt.Errorf("构建邮件失败: %v", err)
	}

	// 收集所有收件人
	recipients := []string{to}
	if cc != "" {
		recipients = append(recipients, cc)
	}
	if bcc != "" {
		recipients = append(recipients, bcc)
	}

	// 设置认证
	auth := smtp.PlainAuth("", t.config.Username, t.config.Password, t.config.SMTPHost)
	addr := fmt.Sprintf("%s:%d", t.config.SMTPHost, t.config.SMTPPort)

	// 发送邮件
	if t.config.SMTPPort == 465 {
		tlsConfig := &tls.Config{
			ServerName: t.config.SMTPHost,
			MinVersion: tls.VersionTLS12,
		}
		conn, err := tls.Dial("tcp", addr, tlsConfig)
		if err != nil {
			return "", fmt.Errorf("TLS连接失败: %v", err)
		}
		defer conn.Close()

		smtpClient, err := smtp.NewClient(conn, t.config.SMTPHost)
		if err != nil {
			return "", fmt.Errorf("创建SMTP客户端失败: %v", err)
		}
		defer smtpClient.Close()

		if err = smtpClient.Auth(auth); err != nil {
			return "", fmt.Errorf("认证失败: %v", err)
		}

		if err = smtpClient.Mail(t.config.Username); err != nil {
			return "", fmt.Errorf("设置发件人失败: %v", err)
		}

		for _, rcpt := range recipients {
			if err = smtpClient.Rcpt(rcpt); err != nil {
				return "", fmt.Errorf("设置收件人 %s 失败: %v", rcpt, err)
			}
		}

		w, err := smtpClient.Data()
		if err != nil {
			return "", fmt.Errorf("准备数据失败: %v", err)
		}

		_, err = w.Write(msg)
		if err != nil {
			return "", fmt.Errorf("写入邮件失败: %v", err)
		}

		err = w.Close()
		if err != nil {
			return "", fmt.Errorf("关闭数据失败: %v", err)
		}
	} else {
		err = smtp.SendMail(addr, auth, t.config.Username, recipients, msg)
		if err != nil {
			return "", fmt.Errorf("发送失败: %v", err)
		}
	}

	// 构建结果信息
	result := fmt.Sprintf("✅ 邮件已发送\n📧 收件人: %s\n📝 主题: %s\n📏 正文长度: %d 字符\n✨ 格式: %s",
		to, subject, len(body), map[bool]string{true: "Markdown/HTML", false: "纯文本"}[useMarkdown])

	if cc != "" {
		result += fmt.Sprintf("\n📋 抄送: %s", cc)
	}
	if bcc != "" {
		result += fmt.Sprintf("\n🔒 密送: %s", bcc)
	}
	if len(attachments) > 0 {
		result += fmt.Sprintf("\n📎 附件: %d 个文件", len(attachments))
		for _, path := range attachments {
			result += fmt.Sprintf("\n   - %s", filepath.Base(path))
		}
	}

	t.logger.Printf("[Email] 邮件发送成功: to=%s, subject=%s, isMarkdown=%v, attachments=%d",
		to, subject, useMarkdown, len(attachments))
	return result, nil
}

// buildSimpleEmail 构建简单的 multipart/alternative 邮件（无附件）
func (t *EmailTool) buildSimpleEmail(to, subject, plainText, htmlContent, cc, bcc string) ([]byte, error) {
	var buf bytes.Buffer

	from := stdmail.Address{Name: t.config.FromName, Address: t.config.FromAddress}
	toAddr := stdmail.Address{Address: to}

	buf.WriteString(fmt.Sprintf("From: %s\r\n", from.String()))
	buf.WriteString(fmt.Sprintf("To: %s\r\n", toAddr.String()))
	buf.WriteString(fmt.Sprintf("Subject: %s\r\n", mime.BEncoding.Encode("UTF-8", subject)))

	if cc != "" {
		ccAddr := stdmail.Address{Address: cc}
		buf.WriteString(fmt.Sprintf("Cc: %s\r\n", ccAddr.String()))
	}
	if bcc != "" {
		bccAddr := stdmail.Address{Address: bcc}
		buf.WriteString(fmt.Sprintf("Bcc: %s\r\n", bccAddr.String()))
	}

	buf.WriteString("MIME-Version: 1.0\r\n")
	buf.WriteString("Content-Type: multipart/alternative; boundary=alternativeBoundary\r\n")
	buf.WriteString("\r\n")

	// 纯文本版本
	buf.WriteString("--alternativeBoundary\r\n")
	buf.WriteString("Content-Type: text/plain; charset=UTF-8\r\n")
	buf.WriteString("Content-Transfer-Encoding: quoted-printable\r\n")
	buf.WriteString("\r\n")

	qpWriter := quotedprintable.NewWriter(&buf)
	qpWriter.Write([]byte(plainText))
	qpWriter.Close()
	buf.WriteString("\r\n")

	// HTML 版本
	buf.WriteString("--alternativeBoundary\r\n")
	buf.WriteString("Content-Type: text/html; charset=UTF-8\r\n")
	buf.WriteString("Content-Transfer-Encoding: quoted-printable\r\n")
	buf.WriteString("\r\n")

	htmlQPWriter := quotedprintable.NewWriter(&buf)
	htmlQPWriter.Write([]byte(htmlContent))
	htmlQPWriter.Close()
	buf.WriteString("\r\n")

	buf.WriteString("--alternativeBoundary--\r\n")

	return buf.Bytes(), nil
}

// markdownToPlainText 将 Markdown 转换为纯文本
func (t *EmailTool) markdownToPlainText(md string) string {
	text := md

	linkRegex := regexp.MustCompile(`\[([^\]]+)\]\([^\)]+\)`)
	text = linkRegex.ReplaceAllString(text, "$1")

	imageRegex := regexp.MustCompile(`!\[([^\]]*)\]\([^\)]+\)`)
	text = imageRegex.ReplaceAllString(text, "[图片: $1]")

	codeBlockRegex := regexp.MustCompile("```[a-z]*\n([\\s\\S]*?)\n```")
	text = codeBlockRegex.ReplaceAllString(text, "$1")

	inlineCodeRegex := regexp.MustCompile("`([^`]+)`")
	text = inlineCodeRegex.ReplaceAllString(text, "$1")

	boldRegex := regexp.MustCompile(`\*\*([^*]+)\*\*`)
	text = boldRegex.ReplaceAllString(text, "$1")

	italicRegex := regexp.MustCompile(`\*([^*]+)\*`)
	text = italicRegex.ReplaceAllString(text, "$1")

	headerRegex := regexp.MustCompile(`(?m)^#{1,6}\s+(.*)$`)
	text = headerRegex.ReplaceAllString(text, "$1\n$1\n")

	listRegex := regexp.MustCompile(`(?m)^[\*\-+]\s+(.*)$`)
	text = listRegex.ReplaceAllString(text, "• $1")

	hrRegex := regexp.MustCompile(`(?m)^---+\s*$`)
	text = hrRegex.ReplaceAllString(text, "--------")

	return strings.TrimSpace(text)
}

// htmlEscape HTML 转义
func htmlEscape(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, "\"", "&quot;")
	return s
}

// listEmails 列出邮件
func (t *EmailTool) listEmails(params map[string]interface{}) (string, error) {
	limit := 10
	if l, ok := params["limit"].(float64); ok {
		limit = int(l)
	}
	folder := "INBOX"
	if f, ok := params["folder"].(string); ok && f != "" {
		folder = f
	}
	unreadOnly := false
	if u, ok := params["unread_only"].(bool); ok {
		unreadOnly = u
	}

	// 连接 IMAP 服务器
	imapAddr := fmt.Sprintf("%s:%d", t.config.IMAPHost, t.config.IMAPPort)

	var c *client.Client
	var err error

	if t.config.UseTLS || t.config.IMAPPort == 993 {
		tlsConfig := &tls.Config{ServerName: t.config.IMAPHost}
		c, err = client.DialTLS(imapAddr, tlsConfig)
	} else {
		c, err = client.Dial(imapAddr)
	}

	if err != nil {
		return "", fmt.Errorf("IMAP连接失败: %v", err)
	}
	defer c.Logout()

	if err = c.Login(t.config.Username, t.config.Password); err != nil {
		return "", fmt.Errorf("IMAP登录失败: %v", err)
	}

	// 选择文件夹
	mbox, err := c.Select(folder, false)
	if err != nil {
		return "", fmt.Errorf("选择文件夹失败: %v", err)
	}

	// 计算获取范围
	from := uint32(1)
	to := mbox.Messages
	if mbox.Messages > uint32(limit) {
		from = mbox.Messages - uint32(limit) + 1
	}

	// 搜索条件
	criteria := imap.NewSearchCriteria()
	if unreadOnly {
		criteria.WithoutFlags = []string{imap.SeenFlag}
	}

	seqSet := new(imap.SeqSet)
	seqSet.AddRange(from, to)

	// 获取邮件
	messages := make(chan *imap.Message, limit)
	section := &imap.BodySectionName{Peek: true}
	items := []imap.FetchItem{imap.FetchEnvelope, imap.FetchFlags, section.FetchItem()}

	go func() {
		if err := c.Fetch(seqSet, items, messages); err != nil {
			t.logger.Printf("获取邮件失败: %v", err)
		}
	}()

	var result strings.Builder
	count := 0

	for msg := range messages {
		if msg.Envelope == nil {
			continue
		}

		subject := msg.Envelope.Subject
		if decoded, err := decodeRFC2047(subject); err == nil {
			subject = decoded
		}

		from := ""
		if len(msg.Envelope.From) > 0 {
			from = msg.Envelope.From[0].Address()
		}

		date := msg.Envelope.Date.Format("2006-01-02 15:04")
		flags := ""
		if containsFlag(msg.Flags, imap.SeenFlag) {
			flags = "✓"
		} else {
			flags = "✗"
		}

		hasAttachment := false
		// 简单检测附件
		if body, ok := msg.Body[section]; ok {
			contentType := strings.ToLower(fmt.Sprintf("%s", body))
			if strings.Contains(contentType, "multipart/mixed") ||
				strings.Contains(contentType, "attachment") ||
				strings.Contains(contentType, "name=") {
				hasAttachment = true
			}
		}

		attachmentStr := ""
		if hasAttachment {
			attachmentStr = " 📎"
		}

		result.WriteString(fmt.Sprintf("#%d %s [%s] %s - %s%s\n",
			msg.SeqNum, flags, date, from, subject, attachmentStr))
		count++
	}

	if count == 0 {
		return "📭 没有找到邮件", nil
	}

	return fmt.Sprintf("📬 邮件列表 (%d封):\n\n%s", count, result.String()), nil
}

// readEmail 读取指定邮件
func (t *EmailTool) readEmail(params map[string]interface{}) (string, error) {
	messageID := uint32(0)
	if mid, ok := params["message_id"].(float64); ok {
		messageID = uint32(mid)
	}
	if messageID == 0 {
		return "", fmt.Errorf("需要指定有效的 message_id")
	}

	// 连接 IMAP
	imapAddr := fmt.Sprintf("%s:%d", t.config.IMAPHost, t.config.IMAPPort)

	var c *client.Client
	var err error

	if t.config.UseTLS || t.config.IMAPPort == 993 {
		tlsConfig := &tls.Config{ServerName: t.config.IMAPHost}
		c, err = client.DialTLS(imapAddr, tlsConfig)
	} else {
		c, err = client.Dial(imapAddr)
	}

	if err != nil {
		return "", fmt.Errorf("IMAP连接失败: %v", err)
	}
	defer c.Logout()

	if err = c.Login(t.config.Username, t.config.Password); err != nil {
		return "", fmt.Errorf("IMAP登录失败: %v", err)
	}

	// 选择收件箱
	if _, err = c.Select("INBOX", false); err != nil {
		return "", fmt.Errorf("选择收件箱失败: %v", err)
	}

	// 获取指定邮件
	seqSet := new(imap.SeqSet)
	seqSet.AddNum(messageID)

	messages := make(chan *imap.Message, 1)
	section := &imap.BodySectionName{}
	items := []imap.FetchItem{imap.FetchEnvelope, imap.FetchFlags, imap.FetchBodyStructure, section.FetchItem()}

	go func() {
		c.Fetch(seqSet, items, messages)
	}()

	msg := <-messages
	if msg == nil || msg.Envelope == nil {
		return "", fmt.Errorf("邮件 #%d 不存在", messageID)
	}

	// 解析邮件内容
	subject := msg.Envelope.Subject
	if decoded, err := decodeRFC2047(subject); err == nil {
		subject = decoded
	}

	from := ""
	if len(msg.Envelope.From) > 0 {
		addr := msg.Envelope.From[0]
		if addr.PersonalName != "" {
			from = fmt.Sprintf("%s <%s>", addr.PersonalName, addr.Address())
		} else {
			from = addr.Address()
		}
	}

	to := ""
	if len(msg.Envelope.To) > 0 {
		addr := msg.Envelope.To[0]
		if addr.PersonalName != "" {
			to = fmt.Sprintf("%s <%s>", addr.PersonalName, addr.Address())
		} else {
			to = addr.Address()
		}
	}

	date := msg.Envelope.Date.Format("2006-01-02 15:04:05")

	body, _ := t.extractBody(msg)

	// 检测附件
	var attachments []string
	if msg.BodyStructure != nil {
		attachments = t.extractAttachmentInfo(msg.BodyStructure)
	}

	// 标记为已读
	flags := []interface{}{imap.SeenFlag}
	c.Store(seqSet, imap.FormatFlagsOp(imap.AddFlags, true), flags, nil)

	result := fmt.Sprintf("📧 邮件 #%d\n", messageID)
	result += fmt.Sprintf("👤 发件人: %s\n", from)
	if to != "" {
		result += fmt.Sprintf("📥 收件人: %s\n", to)
	}
	result += fmt.Sprintf("📝 主题: %s\n", subject)
	result += fmt.Sprintf("📅 日期: %s\n", date)

	if len(attachments) > 0 {
		result += fmt.Sprintf("\n📎 附件:\n")
		for _, att := range attachments {
			result += fmt.Sprintf("   - %s\n", att)
		}
	}

	result += fmt.Sprintf("\n--- 正文 ---\n%s", body)

	return result, nil
}

// extractBody 提取邮件正文
func (t *EmailTool) extractBody(msg *imap.Message) (string, error) {
	section := &imap.BodySectionName{}
	if body, ok := msg.Body[section]; ok {
		bodyStr := fmt.Sprintf("%s", body)
		return t.parseMultipartBody(bodyStr)
	}
	return "", fmt.Errorf("无法提取邮件正文")
}

// parseMultipartBody 解析 multipart 邮件正文
func (t *EmailTool) parseMultipartBody(content string) (string, error) {
	if strings.Contains(content, "multipart/alternative") {
		return t.extractPlainTextFromBody(content)
	}

	if strings.Contains(content, "text/plain") {
		return t.extractPlainTextFromBody(content)
	}

	return content, nil
}

// extractPlainTextFromBody 从邮件正文中提取纯文本
func (t *EmailTool) extractPlainTextFromBody(content string) (string, error) {
	if idx := strings.Index(content, "Content-Type: text/plain"); idx != -1 {
		bodyStart := strings.Index(content[idx:], "\n\n")
		if bodyStart != -1 {
			bodyContent := content[idx+bodyStart+2:]
			if boundaryIdx := strings.Index(bodyContent, "--"); boundaryIdx != -1 {
				bodyContent = bodyContent[:boundaryIdx]
			}
			return strings.TrimSpace(bodyContent), nil
		}
	}

	return content, nil
}

// extractAttachmentInfo 提取附件信息
func (t *EmailTool) extractAttachmentInfo(bs *imap.BodyStructure) []string {
	var attachments []string

	var extract func(*imap.BodyStructure)
	extract = func(part *imap.BodyStructure) {
		if part == nil {
			return
		}

		// 检查是否是附件
		if strings.HasPrefix(strings.ToLower(part.Disposition), "attachment") ||
			(part.Disposition == "" && part.Params["name"] != "") {
			name := part.Params["name"]
			if name != "" {
				attachments = append(attachments, name)
			}
		}

		// 递归检查子部分
		for _, child := range part.Parts {
			extract(child)
		}
	}

	extract(bs)
	return attachments
}

func containsFlag(flags []string, flag string) bool {
	for _, f := range flags {
		if f == flag {
			return true
		}
	}
	return false
}

func decodeRFC2047(s string) (string, error) {
	dec := mime.WordDecoder{CharsetReader: func(charset string, input io.Reader) (io.Reader, error) {
		return input, nil
	}}
	return dec.DecodeHeader(s)
}
