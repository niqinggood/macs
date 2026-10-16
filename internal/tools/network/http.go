package network

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// HTTP请求工具
type HTTPTool struct {
	client    *http.Client
	timeout   time.Duration
	userAgent string
	cookies   []*http.Cookie
	headers   map[string]string
}

func NewHTTPTool() *HTTPTool {
	return &HTTPTool{
		client: &http.Client{
			Timeout: 60 * time.Second,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) >= 10 {
					return http.ErrUseLastResponse
				}
				return nil
			},
		},
		userAgent: "Mozilla/5.0 (compatible; LobsterBot/1.0)",
		headers:   make(map[string]string),
	}
}

const (
	MaxResponseSize = 20 * 1024 // 30KB 限制
	WarningSize     = 10 * 1024 // 10KB 警告
)

func (t *HTTPTool) Name() string { return "http" }

func (t *HTTPTool) Description() string {
	return `HTTP request tool supporting GET, POST, PUT, DELETE methods.
Features: JSON/form data sending, file upload, Cookie management, session handling.

⚠️ IMPORTANT: 
- Use this tool for API calls only (REST APIs, JSON responses)
- For large web pages (HTML), use the browser tool instead
- Large HTML responses will be rejected to prevent token waste
- Large Response is automatically summarized/truncated`
}

func (t *HTTPTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"method": map[string]interface{}{
				"type":        "string",
				"enum":        []string{"GET", "POST", "PUT", "DELETE", "PATCH", "HEAD", "OPTIONS"},
				"description": "HTTP方法",
			},
			"url": map[string]interface{}{
				"type":        "string",
				"description": "请求URL",
			},

			// 请求数据
			"data": map[string]interface{}{
				"type":        "object",
				"description": "要发送的数据（自动转为JSON或表单）",
			},
			"json": map[string]interface{}{
				"type":        "object",
				"description": "JSON数据",
			},
			"form": map[string]interface{}{
				"type":        "object",
				"description": "表单数据",
			},
			"body": map[string]interface{}{
				"type":        "string",
				"description": "原始请求体",
			},

			// 文件上传
			"files": map[string]interface{}{
				"type":        "array",
				"description": "要上传的文件",
				"items": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"field": map[string]interface{}{"type": "string"},
						"path":  map[string]interface{}{"type": "string"},
					},
				},
			},

			// 请求头
			"headers": map[string]interface{}{
				"type":        "object",
				"description": "HTTP头",
			},
			"cookies": map[string]interface{}{
				"type":        "array",
				"description": "Cookie列表",
			},

			// 认证
			"auth": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"type": map[string]interface{}{
						"type": "string",
						"enum": []string{"basic", "bearer", "digest"},
					},
					"username": map[string]interface{}{"type": "string"},
					"password": map[string]interface{}{"type": "string"},
					"token":    map[string]interface{}{"type": "string"},
				},
			},

			// 查询参数
			"params": map[string]interface{}{
				"type":        "object",
				"description": "URL查询参数",
			},

			// 响应处理
			"response_type": map[string]interface{}{
				"type":        "string",
				"enum":        []string{"text", "json", "bytes", "save"},
				"description": "响应类型",
			},
			"save_path": map[string]interface{}{
				"type":        "string",
				"description": "保存响应的文件路径",
			},

			// 高级选项
			"timeout": map[string]interface{}{
				"type":        "integer",
				"description": "超时时间（秒）",
			},
			"follow_redirects": map[string]interface{}{
				"type":        "boolean",
				"description": "是否跟随重定向",
			},
			"max_redirects": map[string]interface{}{
				"type":        "integer",
				"description": "最大重定向次数",
			},
			"verify_ssl": map[string]interface{}{
				"type":        "boolean",
				"description": "是否验证SSL证书",
			},
			"proxy": map[string]interface{}{
				"type":        "string",
				"description": "代理服务器",
			},
		},
		"required": []string{"method", "url"},
	}
}

func (t *HTTPTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	method, _ := params["method"].(string)
	url, _ := params["url"].(string)

	// 创建请求
	req, err := t.buildRequest(ctx, method, url, params)
	if err != nil {
		return "", err
	}

	// 设置超时
	if timeout, ok := params["timeout"].(float64); ok {
		t.client.Timeout = time.Duration(timeout) * time.Second
	}

	// 执行请求
	resp, err := t.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	// 读取响应
	return t.handleResponse(resp, params)
}

func (t *HTTPTool) handleResponse(resp *http.Response, params map[string]interface{}) (string, error) {
	var result strings.Builder

	result.WriteString(fmt.Sprintf("📡 HTTP %s %s\n", resp.Proto, resp.Status))
	result.WriteString(fmt.Sprintf("状态码: %d\n", resp.StatusCode))
	result.WriteString("\n📋 响应头:\n")

	contentType := resp.Header.Get("Content-Type")
	contentLength := resp.ContentLength
	if strings.Contains(contentType, "text/html") {
		result.WriteString("\n⚠️  检测到 HTML 页面响应！\n")
		result.WriteString("建议: 使用 browser 工具访问网页，而不是 HTTP 工具\n")
		result.WriteString("HTTP 工具仅适用于 API 调用\n\n")

		// 对于大 HTML，直接返回错误
		if contentLength > MaxResponseSize {
			return "", fmt.Errorf("HTML response too large (%d bytes). Use browser tool instead", contentLength)
		}
	}
	if contentLength > MaxResponseSize && contentLength > 0 {
		return "", fmt.Errorf("response too large (%d bytes > %d bytes limit)",
			contentLength, MaxResponseSize)
	}
	limitedReader := io.LimitReader(resp.Body, MaxResponseSize)
	body, err := io.ReadAll(limitedReader)
	if err != nil {
		return result.String(), err
	}

	// 如果实际读取的大小等于限制，说明响应被截断
	if len(body) >= MaxResponseSize {
		result.WriteString(fmt.Sprintf("\n⚠️  响应过大，已截断 (限制: %d 字节)\n", MaxResponseSize))
	}

	for k, v := range resp.Header {
		result.WriteString(fmt.Sprintf("  %s: %s\n", k, strings.Join(v, ", ")))
	}

	// 处理响应
	responseType, _ := params["response_type"].(string)

	switch responseType {
	case "save":
		savePath, _ := params["save_path"].(string)
		if savePath == "" {
			savePath = filepath.Join("downloads",
				fmt.Sprintf("download_%s", time.Now().Format("20060102_150405")))
		}
		os.MkdirAll(filepath.Dir(savePath), 0755)
		os.WriteFile(savePath, body, 0644)

		result.WriteString(fmt.Sprintf("\n"))
		contetype := ""
		if strings.HasPrefix(contentType, "image/") {
			contetype = fmt.Sprintf(",type: %s\n", contentType)
		}
		result.WriteString(fmt.Sprintf("file saved,path: %s,size: %.2f KB %s\n", savePath, float64(len(body))/1024, contetype))

	case "json":
		var jsonData interface{}
		if err := json.Unmarshal(body, &jsonData); err == nil {
			prettyJSON, _ := json.MarshalIndent(jsonData, "", "  ")
			result.WriteString(fmt.Sprintf("\n📦 JSON响应:\n%s\n", prettyJSON))
		} else {
			result.WriteString(fmt.Sprintf("\n📝 响应内容:\n%s\n", truncate(string(body), 1000)))
		}

	default:
		// 尝试检测内容类型
		contentType := resp.Header.Get("Content-Type")
		if strings.Contains(contentType, "application/json") {
			var jsonData interface{}
			if err := json.Unmarshal(body, &jsonData); err == nil {
				prettyJSON, _ := json.MarshalIndent(jsonData, "", "  ")
				result.WriteString(fmt.Sprintf("\n📦 JSON响应:\n%s\n", prettyJSON))
			} else {
				result.WriteString(fmt.Sprintf("\n📝 响应内容:\n%s\n", truncate(string(body), 1000)))
			}
		} else if strings.Contains(contentType, "text/html") {
			// 提取有用信息
			result.WriteString(fmt.Sprintf("\n🌐 HTML页面 (长度: %d 字节)\n", len(body)))
			result.WriteString(t.extractHTMLInfo(body))
		} else {
			result.WriteString(fmt.Sprintf("\n📝 响应内容 (前1000字符):\n%s\n",
				truncate(string(body), 1000)))
		}
	}

	result.WriteString(fmt.Sprintf("\n📊 response size: %d byte", len(body)))

	return result.String(), nil
}

func (t *HTTPTool) buildRequest(ctx context.Context, method, urlStr string, params map[string]interface{}) (*http.Request, error) {
	var body io.Reader

	// 处理请求体
	if data, ok := params["json"].(map[string]interface{}); ok {
		// JSON数据
		jsonData, _ := json.Marshal(data)
		body = bytes.NewReader(jsonData)
	} else if form, ok := params["form"].(map[string]interface{}); ok {
		// 表单数据
		formData := url.Values{}
		for k, v := range form {
			formData.Set(k, fmt.Sprintf("%v", v))
		}
		body = strings.NewReader(formData.Encode())
	} else if files, ok := params["files"].([]interface{}); ok && len(files) > 0 {
		// 文件上传
		buf := &bytes.Buffer{}
		writer := multipart.NewWriter(buf)

		for _, f := range files {
			fileMap := f.(map[string]interface{})
			field, _ := fileMap["field"].(string)
			path, _ := fileMap["path"].(string)

			file, err := os.Open(path)
			if err != nil {
				return nil, err
			}
			defer file.Close()

			part, err := writer.CreateFormFile(field, filepath.Base(path))
			if err != nil {
				return nil, err
			}
			io.Copy(part, file)
		}

		// 添加普通表单字段
		if form, ok := params["form"].(map[string]interface{}); ok {
			for k, v := range form {
				writer.WriteField(k, fmt.Sprintf("%v", v))
			}
		}

		writer.Close()
		body = buf
	} else if bodyStr, ok := params["body"].(string); ok {
		// 原始body
		body = strings.NewReader(bodyStr)
	} else if data, ok := params["data"].(map[string]interface{}); ok {
		// 自动判断content-type
		if t.isJSONContent(params) {
			jsonData, _ := json.Marshal(data)
			body = bytes.NewReader(jsonData)
		} else {
			formData := url.Values{}
			for k, v := range data {
				formData.Set(k, fmt.Sprintf("%v", v))
			}
			body = strings.NewReader(formData.Encode())
		}
	}

	// 创建请求
	req, err := http.NewRequestWithContext(ctx, method, urlStr, body)
	if err != nil {
		return nil, err
	}

	// 设置查询参数
	if queryParams, ok := params["params"].(map[string]interface{}); ok {
		q := req.URL.Query()
		for k, v := range queryParams {
			q.Add(k, fmt.Sprintf("%v", v))
		}
		req.URL.RawQuery = q.Encode()
	}

	// 设置默认headers
	req.Header.Set("User-Agent", t.userAgent)

	// 设置自定义headers
	if headers, ok := params["headers"].(map[string]interface{}); ok {
		for k, v := range headers {
			req.Header.Set(k, fmt.Sprintf("%v", v))
		}
	}

	// 设置认证
	if auth, ok := params["auth"].(map[string]interface{}); ok {
		authType, _ := auth["type"].(string)
		switch authType {
		case "basic":
			username, _ := auth["username"].(string)
			password, _ := auth["password"].(string)
			req.SetBasicAuth(username, password)
		case "bearer":
			token, _ := auth["token"].(string)
			req.Header.Set("Authorization", "Bearer "+token)
		}
	}

	// 设置cookies
	if cookies, ok := params["cookies"].([]interface{}); ok {
		for _, c := range cookies {
			cookieMap := c.(map[string]interface{})
			cookie := &http.Cookie{
				Name:  cookieMap["name"].(string),
				Value: cookieMap["value"].(string),
			}
			req.AddCookie(cookie)
		}
	}

	return req, nil
}

// isJSONContent 判断请求体是否为JSON格式
func (t *HTTPTool) isJSONContent(params map[string]interface{}) bool {
	// 检查headers中是否指定了Content-Type
	if headers, ok := params["headers"].(map[string]interface{}); ok {
		for k, v := range headers {
			if strings.ToLower(k) == "content-type" {
				contentType := strings.ToLower(fmt.Sprintf("%v", v))
				return strings.Contains(contentType, "application/json")
			}
		}
	}

	// 如果没有指定，默认返回false（使用表单格式）
	return false
}

func (t *HTTPTool) extractHTMLInfo(body []byte) string {
	var result strings.Builder
	htmlContent := string(body)

	// 使用标准库的html包进行实体转义
	htmlContent = html.UnescapeString(htmlContent)

	// 移除script和style标签及其内容
	reScript := regexp.MustCompile(`(?s)<script[^>]*>.*?</script>`)
	htmlContent = reScript.ReplaceAllString(htmlContent, "")

	reStyle := regexp.MustCompile(`(?s)<style[^>]*>.*?</style>`)
	htmlContent = reStyle.ReplaceAllString(htmlContent, "")

	// 提取标题
	titleRegex := regexp.MustCompile(`<title>(.*?)</title>`)
	if matches := titleRegex.FindStringSubmatch(htmlContent); len(matches) > 1 {
		// 清理标题中的空白字符
		title := strings.TrimSpace(matches[1])
		title = regexp.MustCompile(`\s+`).ReplaceAllString(title, " ")
		if title != "" {
			result.WriteString(fmt.Sprintf("  标题: %s\n", title))
		}
	}

	// 提取meta描述
	descRegex := regexp.MustCompile(`<meta\s+name=["']description["']\s+content=["'](.*?)["']`)
	if matches := descRegex.FindStringSubmatch(htmlContent); len(matches) > 1 {
		desc := strings.TrimSpace(matches[1])
		desc = regexp.MustCompile(`\s+`).ReplaceAllString(desc, " ")
		if len(desc) > 200 {
			desc = desc[:200] + "..."
		}
		if desc != "" {
			result.WriteString(fmt.Sprintf("  描述: %s\n", desc))
		}
	}

	// 提取所有链接
	linkRegex := regexp.MustCompile(`<a\s+(?:[^>]*?\s+)?href=["'](.*?)["']`)
	links := linkRegex.FindAllStringSubmatch(htmlContent, -1)
	if len(links) > 0 {
		result.WriteString(fmt.Sprintf("  链接数: %d\n", len(links)))
		// 显示前5个链接
		shown := 0
		for _, match := range links {
			if shown >= 5 {
				result.WriteString("  ...\n")
				break
			}
			link := strings.TrimSpace(match[1])
			if link != "" && !strings.HasPrefix(link, "#") && !strings.HasPrefix(link, "javascript:") {
				if len(link) > 50 {
					link = link[:50] + "..."
				}
				result.WriteString(fmt.Sprintf("    - %s\n", link))
				shown++
			}
		}
	}

	// 提取h1标签
	h1Regex := regexp.MustCompile(`<h1[^>]*>(.*?)</h1>`)
	h1s := h1Regex.FindAllStringSubmatch(htmlContent, -1)
	if len(h1s) > 0 {
		result.WriteString(fmt.Sprintf("  H1标签数: %d\n", len(h1s)))
		// 显示第一个h1
		if len(h1s) > 0 {
			h1Text := strings.TrimSpace(h1s[0][1])
			h1Text = regexp.MustCompile(`\s+`).ReplaceAllString(h1Text, " ")
			if len(h1Text) > 100 {
				h1Text = h1Text[:100] + "..."
			}
			if h1Text != "" {
				result.WriteString(fmt.Sprintf("  主标题: %s\n", h1Text))
			}
		}
	}

	// 提取纯文本内容预览
	textContent := extractPlainText(htmlContent)
	if len(textContent) > 0 {
		preview := textContent
		if len(preview) > 200 {
			preview = preview[:200] + "..."
		}
		result.WriteString(fmt.Sprintf("  文本预览: %s\n", preview))
	}

	if result.Len() == 0 {
		result.WriteString("  无法提取结构化信息\n")
	}

	return result.String()
}

// extractPlainText 从HTML中提取纯文本
func extractPlainText(html string) string {
	// 移除所有HTML标签
	reTag := regexp.MustCompile(`<[^>]*>`)
	text := reTag.ReplaceAllString(html, " ")

	// 处理空白字符
	reSpace := regexp.MustCompile(`\s+`)
	text = reSpace.ReplaceAllString(text, " ")

	return strings.TrimSpace(text)
}

func (t *HTTPTool) extractTextFromHTML(htmlContent string) string {
	if htmlContent == "" {
		return ""
	}

	// 移除script标签及其内容
	reScript := regexp.MustCompile(`(?s)<script[^>]*>.*?</script>`)
	htmlContent = reScript.ReplaceAllString(htmlContent, "")

	// 移除style标签及其内容
	reStyle := regexp.MustCompile(`(?s)<style[^>]*>.*?</style>`)
	htmlContent = reStyle.ReplaceAllString(htmlContent, "")

	// 移除注释
	reComment := regexp.MustCompile(`(?s)<!--.*?-->`)
	htmlContent = reComment.ReplaceAllString(htmlContent, "")

	// 移除所有HTML标签
	reTag := regexp.MustCompile(`<[^>]*>`)
	htmlContent = reTag.ReplaceAllString(htmlContent, " ")

	// 处理HTML实体 - 使用标准库
	htmlContent = html.UnescapeString(htmlContent)

	// 处理空白字符
	reSpace := regexp.MustCompile(`\s+`)
	htmlContent = reSpace.ReplaceAllString(htmlContent, " ")

	return strings.TrimSpace(htmlContent)
}

func (t *HTTPTool) Get(url string, params map[string]interface{}) (string, error) {
	// 首先发送 HEAD 请求检查响应大小
	headReq, _ := http.NewRequest("HEAD", url, nil)
	headReq.Header.Set("User-Agent", t.userAgent)

	headResp, err := t.client.Do(headReq)
	if err == nil {
		defer headResp.Body.Close()

		contentLength := headResp.ContentLength
		contentType := headResp.Header.Get("Content-Type")

		// 检查是否为 HTML 且过大
		if strings.Contains(contentType, "text/html") && contentLength > WarningSize {
			return "", fmt.Errorf(
				"⚠️  Detected large HTML page (%.1f KB). Please use browser tool instead.\n"+
					"HTTP tool is designed for API calls, not web scraping.\n"+
					"URL: %s",
				float64(contentLength)/1024, url,
			)
		}

		if contentLength > MaxResponseSize {
			return "", fmt.Errorf(
				"response too large (%.1f KB > %d KB limit). Use HEAD request first or adjust parameters",
				float64(contentLength)/1024, MaxResponseSize/1024,
			)
		}
	}

	// 设置参数并执行
	if params == nil {
		params = make(map[string]interface{})
	}
	params["method"] = "GET"
	params["url"] = url

	return t.Execute(context.Background(), params)
}

// POST快捷方法
func (t *HTTPTool) Post(url string, data map[string]interface{}) (string, error) {
	params := map[string]interface{}{
		"method": "POST",
		"url":    url,
		"json":   data,
	}
	return t.Execute(context.Background(), params)
}

// 下载文件
func (t *HTTPTool) Download(url, savePath string) (string, error) {
	// 直接下载，不进行内容分析
	result, err := t.Execute(context.Background(), map[string]interface{}{
		"method":        "GET",
		"url":           url,
		"response_type": "save",
		"save_path":     savePath,
	})

	if err != nil {
		return "", err
	}

	// 只返回下载结果摘要
	return fmt.Sprintf("✅ 文件已下载: %s\n%s", savePath, result), nil
}

func formatJSON(data string) (string, error) {
	var jsonData interface{}
	if err := json.Unmarshal([]byte(data), &jsonData); err != nil {
		return data, nil // 如果不是JSON，返回原数据
	}
	pretty, err := json.MarshalIndent(jsonData, "", "  ")
	if err != nil {
		return data, nil
	}
	return string(pretty), nil
}
