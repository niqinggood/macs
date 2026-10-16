package network

import (
	"fmt"
	"html"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
)

// StripTags removes HTML tags while retaining image sources as Markdown.
func StripTags(text string) string {
	imageDoubleQuote := regexp.MustCompile(`(?i)<img[^>]+src="([^"]+)"[^>]*>`)
	text = imageDoubleQuote.ReplaceAllString(text, "![image]($1)")
	imageSingleQuote := regexp.MustCompile(`(?i)<img[^>]+src='([^']+)'[^>]*>`)
	text = imageSingleQuote.ReplaceAllString(text, "![image]($1)")
	text = regexp.MustCompile(`<[^>]+>`).ReplaceAllString(text, "")
	return strings.TrimSpace(html.UnescapeString(text))
}

func stripTags(text string) string {
	return StripTags(text)
}

// StructuredMarkdownResult 结构化的Markdown结果
type StructuredMarkdownResult struct {
	Title       string
	URL         string
	PublishDate string
	Author      string
	Content     string
	Metadata    map[string]string
	Links       []LinkInfo
}

// LinkInfo 链接信息
type LinkInfo struct {
	Text  string
	URL   string
	Title string
}

// EnhancedHTMLParser 增强的HTML解析器
type EnhancedHTMLParser struct {
	htmlContent string
	url         string
	title       string
	author      string
	publishDate string
	doc         *goquery.Document
	links       []LinkInfo
}

// NewEnhancedHTMLParser 创建增强解析器
func NewEnhancedHTMLParser(htmlContent, url string) *EnhancedHTMLParser {
	parser := &EnhancedHTMLParser{
		htmlContent: htmlContent,
		url:         url,
		links:       make([]LinkInfo, 0),
	}

	// 初始化goquery文档
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(htmlContent))
	if err == nil {
		parser.doc = doc
	}

	// 提取标题
	parser.title = parser.ExtractTitle()

	// 提取作者
	parser.author = parser.ExtractAuthor()

	// 提取发布日期
	parser.publishDate = parser.ExtractPublishDate()

	return parser
}

// Document returns the parsed HTML document for callers that want to apply a
// specialized readability pass before using the full structured converter.
func (p *EnhancedHTMLParser) Document() *goquery.Document {
	if p == nil {
		return nil
	}
	return p.doc
}

// ExtractAuthor 提取作者信息
func (p *EnhancedHTMLParser) ExtractAuthor() string {
	if p.doc == nil {
		return ""
	}

	authorSelectors := []string{
		"[rel='author']",
		"[itemprop='author']",
		".author",
		".byline",
		".writer",
		"meta[name='author']",
	}

	for _, selector := range authorSelectors {
		if strings.Contains(selector, "meta") {
			if content, exists := p.doc.Find(selector).Attr("content"); exists && content != "" {
				return strings.TrimSpace(content)
			}
		} else {
			text := p.doc.Find(selector).First().Text()
			if text != "" {
				return strings.TrimSpace(text)
			}
		}
	}

	return ""
}

// ExtractPublishDate 提取发布日期
func (p *EnhancedHTMLParser) ExtractPublishDate() string {
	if p.doc == nil {
		return ""
	}

	dateSelectors := []string{
		"[itemprop='datePublished']",
		"time[datetime]",
		".publish-date",
		".post-date",
		".date",
		"meta[name='article:published_time']",
		"meta[property='article:published_time']",
	}

	for _, selector := range dateSelectors {
		if strings.Contains(selector, "meta") {
			if content, exists := p.doc.Find(selector).Attr("content"); exists && content != "" {
				return p.formatDate(content)
			}
		} else {
			selection := p.doc.Find(selector).First()
			if datetime, exists := selection.Attr("datetime"); exists && datetime != "" {
				return p.formatDate(datetime)
			}
			text := selection.Text()
			if text != "" {
				return strings.TrimSpace(text)
			}
		}
	}

	return ""
}

// formatDate 格式化日期
func (p *EnhancedHTMLParser) formatDate(dateStr string) string {
	formats := []string{
		time.RFC3339,
		"2006-01-02T15:04:05Z",
		"2006-01-02",
		"2006-01-02 15:04:05",
	}

	for _, format := range formats {
		if t, err := time.Parse(format, dateStr); err == nil {
			return t.Format("2006-01-02 15:04:05")
		}
	}

	return dateStr
}

// ExtractTitle 提取网页标题
func (p *EnhancedHTMLParser) ExtractTitle() string {
	if p.doc != nil {
		if title := p.doc.Find("title").First().Text(); title != "" {
			return strings.TrimSpace(title)
		}
	}

	titleRegex := regexp.MustCompile(`<title[^>]*>([\s\S]*?)</title>`)
	match := titleRegex.FindStringSubmatch(p.htmlContent)
	if len(match) >= 2 {
		return strings.TrimSpace(stripTags(match[1]))
	}

	return ""
}

// ExtractPlainText 提取纯文本
func (p *EnhancedHTMLParser) ExtractPlainText(maxLength int) string {
	text := regexp.MustCompile(`(?i)<script[\s\S]*?</script>`).ReplaceAllString(p.htmlContent, "")
	text = regexp.MustCompile(`(?i)<style[\s\S]*?</style>`).ReplaceAllString(text, "")
	text = regexp.MustCompile(`<!--[\s\S]*?-->`).ReplaceAllString(text, "")

	text = regexp.MustCompile(`<[^>]+>`).ReplaceAllString(text, " ")

	text = regexp.MustCompile(`\s+`).ReplaceAllString(text, " ")
	text = strings.TrimSpace(text)

	if maxLength > 0 && len(text) > maxLength {
		text = text[:maxLength] + "..."
	}

	return text
}

// ParseToStructuredMarkdown 解析为结构化Markdown
func (p *EnhancedHTMLParser) ParseToStructuredMarkdown() (*StructuredMarkdownResult, error) {
	result := &StructuredMarkdownResult{
		Title:       p.title,
		URL:         p.url,
		PublishDate: p.publishDate,
		Author:      p.author,
		Metadata:    make(map[string]string),
		Links:       make([]LinkInfo, 0),
	}

	var contentBuilder strings.Builder

	// ==============================
	// 修复1：同时尝试结构化解析和降级解析，取内容更丰富的
	// ==============================
	if p.doc != nil {
		mainContent := p.extractMainContent()
		if mainContent != nil && mainContent.Length() > 0 {
			p.parseContentToStructuredMarkdown(mainContent, &contentBuilder, result)
		}
	}

	structuredContent := strings.TrimSpace(contentBuilder.String())

	// 始终也运行降级解析（提取链接）
	fallbackContent := p.fallbackParseWithLinks(result)

	var finalContent string
	// 选择内容更丰富的结果
	if len(structuredContent) > len(fallbackContent) && len(structuredContent) > 200 {
		// 结构化结果更好，但需要合并链接信息
		finalContent = structuredContent
	} else if len(fallbackContent) > 100 {
		finalContent = fallbackContent
	} else {
		// 两者都不理想，使用原始的FallbackParseToMarkdown
		finalContent = p.FallbackParseToMarkdown()
	}

	// 清理内容
	cleanContent := strings.TrimSpace(finalContent)
	titlePattern := regexp.MustCompile(`(?s)^# .*?\n\n`)
	cleanContent = titlePattern.ReplaceAllString(cleanContent, "")
	sourcePattern := regexp.MustCompile(`(?s)^\*\*来源\*\*: .*?\n\n`)
	cleanContent = sourcePattern.ReplaceAllString(cleanContent, "")
	timePattern := regexp.MustCompile(`(?s)^\*\*转换时间\*\*: .*?\n\n`)
	cleanContent = timePattern.ReplaceAllString(cleanContent, "")
	cleanContent = strings.TrimSpace(cleanContent)

	result.Content = cleanContent
	if p.doc != nil {
		p.extractLinksFromScripts(result)
	}
	return result, nil
}

func (p *EnhancedHTMLParser) fallbackParseWithLinks(result *StructuredMarkdownResult) string {
	var builder strings.Builder

	// 获取整个body内容
	var bodyHTML string
	if p.doc != nil {
		bodyHTML, _ = p.doc.Find("body").Html()
	} else {
		bodyHTML = p.htmlContent
	}

	if bodyHTML == "" {
		bodyHTML = p.htmlContent
	}

	// 清理脚本、样式、noscript、注释
	bodyHTML = regexp.MustCompile(`(?is)<script[^>]*>.*?</script>`).ReplaceAllString(bodyHTML, "")
	bodyHTML = regexp.MustCompile(`(?is)<style[^>]*>.*?</style>`).ReplaceAllString(bodyHTML, "")
	bodyHTML = regexp.MustCompile(`(?is)<noscript[^>]*>.*?</noscript>`).ReplaceAllString(bodyHTML, "")
	bodyHTML = regexp.MustCompile(`(?is)<!--.*?-->`).ReplaceAllString(bodyHTML, "")
	bodyHTML = regexp.MustCompile(`(?is)<svg[^>]*>.*?</svg>`).ReplaceAllString(bodyHTML, "")

	// 移除常见的导航、页脚等噪音
	noiseSelectors := []string{
		`(?is)<nav[^>]*>.*?</nav>`,
		`(?is)<footer[^>]*>.*?</footer>`,
	}
	for _, pattern := range noiseSelectors {
		bodyHTML = regexp.MustCompile(pattern).ReplaceAllString(bodyHTML, "")
	}

	// ==============================
	// 处理所有类型的链接
	// ==============================
	linkMap := make(map[string]string)

	// 处理标准链接
	linkRegex := regexp.MustCompile(`(?i)<a[^>]*href="([^"]*)"[^>]*>(.*?)</a>`)
	bodyHTML = linkRegex.ReplaceAllStringFunc(bodyHTML, func(match string) string {
		subMatches := linkRegex.FindStringSubmatch(match)
		if len(subMatches) >= 3 {
			href := subMatches[1]
			linkText := subMatches[2]

			// 如果链接文本包含其他HTML标签，提取纯文本
			text := strings.TrimSpace(stripTags(linkText))

			// 如果文本为空但包含img，提取alt
			if text == "" {
				imgAltRegex := regexp.MustCompile(`(?i)alt="([^"]*)"`)
				if imgMatch := imgAltRegex.FindStringSubmatch(linkText); len(imgMatch) >= 2 {
					text = strings.TrimSpace(imgMatch[1])
				}
			}

			// 如果文本仍然为空，尝试从href提取文件名或使用合适的默认值
			if text == "" {
				if strings.Contains(href, "download") || strings.Contains(href, ".zip") ||
					strings.Contains(href, ".exe") || strings.Contains(href, ".tar") {
					text = "下载"
				} else if strings.Contains(href, "buy") || strings.Contains(href, "order") ||
					strings.Contains(href, "purchase") {
					text = "购买"
				} else {
					text = extractTextFromURL(href)
				}
			}

			if text != "" && !strings.HasPrefix(href, "javascript:") && href != "#" && href != "" {
				absURL := p.makeAbsoluteURL(href)
				key := text + "|" + absURL
				if _, exists := linkMap[key]; !exists {
					linkMap[key] = absURL
					result.Links = append(result.Links, LinkInfo{
						Text: text,
						URL:  absURL,
					})
				}
				return fmt.Sprintf("[%s](%s)", text, absURL)
			}
		}
		return match
	})

	// 处理按钮式元素
	buttonRegex := regexp.MustCompile(`(?i)<(?:div|span|button)[^>]*(?:data-url|data-href|onclick)="([^"]*)"[^>]*>(.*?)</(?:div|span|button)>`)
	bodyHTML = buttonRegex.ReplaceAllStringFunc(bodyHTML, func(match string) string {
		subMatches := buttonRegex.FindStringSubmatch(match)
		if len(subMatches) >= 3 {
			urlAttr := subMatches[1]
			text := strings.TrimSpace(stripTags(subMatches[2]))
			urlMatch := regexp.MustCompile(`(?:location\.href|window\.open)\s*\(\s*['"]([^'"]+)['"]`).FindStringSubmatch(urlAttr)
			var finalURL string
			if len(urlMatch) >= 2 {
				finalURL = urlMatch[1]
			} else if strings.HasPrefix(urlAttr, "http") {
				finalURL = urlAttr
			}
			if finalURL != "" && text != "" {
				absURL := p.makeAbsoluteURL(finalURL)
				result.Links = append(result.Links, LinkInfo{
					Text: text,
					URL:  absURL,
				})
				return fmt.Sprintf("[%s](%s)", text, absURL)
			}
		}
		return match
	})

	// 处理图片
	imgRegex := regexp.MustCompile(`(?i)<img[^>]*src="([^"]*)"[^>]*>`)
	bodyHTML = imgRegex.ReplaceAllStringFunc(bodyHTML, func(match string) string {
		subMatches := imgRegex.FindStringSubmatch(match)
		if len(subMatches) >= 2 {
			src := subMatches[1]
			altRegex := regexp.MustCompile(`(?i)alt="([^"]*)"`)
			titleRegex := regexp.MustCompile(`(?i)title="([^"]*)"`)
			alt := ""
			title := ""
			if altMatch := altRegex.FindStringSubmatch(match); len(altMatch) >= 2 {
				alt = altMatch[1]
			}
			if titleMatch := titleRegex.FindStringSubmatch(match); len(titleMatch) >= 2 {
				title = titleMatch[1]
			}
			if alt == "" && title == "" {
				alt = "图片"
			}
			absURL := p.makeAbsoluteURL(src)
			// 忽略小图标和装饰图
			if strings.Contains(src, "icon") || strings.Contains(src, "logo") ||
				strings.Contains(src, "avatar") || strings.Contains(src, "button") {
				return ""
			}
			if title != "" {
				return fmt.Sprintf("![%s](%s \"%s\")", alt, absURL, title)
			}
			return fmt.Sprintf("![%s](%s)", alt, absURL)
		}
		return match
	})

	// 处理标题 - 修复：不使用反向引用
	bodyHTML = replaceHeadings(bodyHTML)

	// 处理列表
	listRegex := regexp.MustCompile(`(?is)<(ul|ol)[^>]*>(.*?)</(ul|ol)>`)
	bodyHTML = listRegex.ReplaceAllStringFunc(bodyHTML, func(match string) string {
		subMatches := listRegex.FindStringSubmatch(match)
		if len(subMatches) >= 3 {
			isOrdered := subMatches[1] == "ol"
			items := regexp.MustCompile(`(?is)<li[^>]*>(.*?)</li>`).FindAllStringSubmatch(subMatches[2], -1)
			if len(items) == 0 {
				return match
			}
			var result strings.Builder
			for i, item := range items {
				if len(item) >= 2 {
					text := strings.TrimSpace(stripTags(item[1]))
					if text != "" {
						if isOrdered {
							result.WriteString(fmt.Sprintf("%d. %s\n", i+1, text))
						} else {
							result.WriteString(fmt.Sprintf("- %s\n", text))
						}
					}
				}
			}
			return result.String()
		}
		return match
	})

	// 处理段落
	paragraphRegex := regexp.MustCompile(`(?i)<p[^>]*>([\s\S]*?)</p>`)
	bodyHTML = paragraphRegex.ReplaceAllStringFunc(bodyHTML, func(match string) string {
		subMatches := paragraphRegex.FindStringSubmatch(match)
		if len(subMatches) >= 2 {
			text := strings.TrimSpace(stripTags(subMatches[1]))
			text = p.cleanText(text)
			if text != "" && len(text) > 5 {
				return text + "\n\n"
			}
		}
		return match
	})

	// 处理表格
	tableRegex := regexp.MustCompile(`(?is)<table[^>]*>(.*?)</table>`)
	bodyHTML = tableRegex.ReplaceAllStringFunc(bodyHTML, func(match string) string {
		var tableBuilder strings.Builder
		rows := regexp.MustCompile(`(?is)<tr[^>]*>(.*?)</tr>`).FindAllStringSubmatch(match, -1)
		if len(rows) > 0 {
			tableBuilder.WriteString("\n")
			for i, row := range rows {
				if len(row) >= 2 {
					cells := regexp.MustCompile(`(?is)<t[dh][^>]*>(.*?)</t[dh]>`).FindAllStringSubmatch(row[1], -1)
					if len(cells) > 0 {
						tableBuilder.WriteString("|")
						for _, cell := range cells {
							if len(cell) >= 2 {
								text := strings.TrimSpace(stripTags(cell[1]))
								tableBuilder.WriteString(fmt.Sprintf(" %s |", text))
							}
						}
						tableBuilder.WriteString("\n")
						if i == 0 {
							tableBuilder.WriteString("|")
							for range cells {
								tableBuilder.WriteString(" --- |")
							}
							tableBuilder.WriteString("\n")
						}
					}
				}
			}
			return tableBuilder.String()
		}
		return match
	})

	// 处理 div/section/article 等容器
	bodyHTML = regexp.MustCompile(`(?i)</?(?:div|section|article|header|footer|nav|aside|main|span|strong|em|b|i|u|code|pre|form|input|select|option|textarea)[^>]*>`).ReplaceAllString(bodyHTML, " ")

	// 处理换行
	bodyHTML = regexp.MustCompile(`(?i)<br\s*/?>`).ReplaceAllString(bodyHTML, "\n")

	// 移除剩余的HTML标签
	bodyHTML = regexp.MustCompile(`<[^>]+>`).ReplaceAllString(bodyHTML, " ")

	// 解码HTML实体
	bodyHTML = htmlUnescape(bodyHTML)

	// 清理空白
	bodyHTML = regexp.MustCompile(`\n{3,}`).ReplaceAllString(bodyHTML, "\n\n")
	bodyHTML = regexp.MustCompile(` {2,}`).ReplaceAllString(bodyHTML, " ")
	bodyHTML = strings.TrimSpace(bodyHTML)

	// 按行整理
	lines := strings.Split(bodyHTML, "\n")
	var cleanLines []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" && !isNoiseLine(line) {
			cleanLines = append(cleanLines, line)
		}
	}

	builder.WriteString(strings.Join(cleanLines, "\n\n"))
	return builder.String()
}

func (p *EnhancedHTMLParser) extractLinksFromScripts(result *StructuredMarkdownResult) {
	if p.doc == nil {
		return
	}

	// 查找所有script标签
	p.doc.Find("script").Each(func(i int, s *goquery.Selection) {
		scriptContent := s.Text()

		// 查找常见的下载链接模式
		patterns := []string{
			`["'](https?://[^"']*download[^"']*)["']`,
			`["'](https?://[^"']*\.(?:zip|rar|7z|tar\.gz|exe|msi|dmg|pkg)["'])`,
			`["'](/[^"']*download[^"']*)["']`,
			`["'](/[^"']*\.(?:zip|rar|7z|tar\.gz)["'])`,
		}

		for _, pattern := range patterns {
			re := regexp.MustCompile(pattern)
			matches := re.FindAllStringSubmatch(scriptContent, -1)
			for _, match := range matches {
				if len(match) >= 2 {
					url := strings.Trim(match[1], `"'`)
					absURL := p.makeAbsoluteURL(url)

					// 检查是否已经存在
					exists := false
					for _, link := range result.Links {
						if link.URL == absURL {
							exists = true
							break
						}
					}

					if !exists {
						result.Links = append(result.Links, LinkInfo{
							Text: "下载链接",
							URL:  absURL,
						})
					}
				}
			}
		}

		// 查找与按钮文本关联的链接映射
		// 例如：{"立即下载": "/download/file.zip", "查看文档": "/docs/file"}
		buttonLinkPattern := regexp.MustCompile(`["']([^"']*(?:下载|download|文档|doc|查看)[^"']*)["']\s*[:=]\s*["']([^"']+)["']`)
		buttonMatches := buttonLinkPattern.FindAllStringSubmatch(scriptContent, -1)
		for _, match := range buttonMatches {
			if len(match) >= 3 {
				text := match[1]
				url := strings.Trim(match[2], `"'`)
				absURL := p.makeAbsoluteURL(url)

				// 检查是否已存在
				exists := false
				for _, link := range result.Links {
					if link.URL == absURL {
						exists = true
						break
					}
				}

				if !exists {
					result.Links = append(result.Links, LinkInfo{
						Text: text,
						URL:  absURL,
					})
				}
			}
		}
	})
}

// replaceHeadings 替换HTML标题为Markdown标题（避免反向引用问题）
func replaceHeadings(html string) string {
	// 分别处理每个级别的标题
	for level := 1; level <= 6; level++ {
		// 构建正则表达式，分别匹配开始和结束标签
		openPattern := fmt.Sprintf(`(?i)<h%d[^>]*>`, level)
		closePattern := fmt.Sprintf(`(?i)</h%d>`, level)

		// 使用非正则方式处理：找到所有h标签
		html = replaceHeadingByLevel(html, level, openPattern, closePattern)
	}
	return html
}

// replaceHeadingByLevel 替换指定级别的标题
func replaceHeadingByLevel(html string, level int, openPattern, closePattern string) string {
	openRegex := regexp.MustCompile(openPattern)
	closeRegex := regexp.MustCompile(closePattern)

	var result strings.Builder
	lastIndex := 0

	for {
		// 查找开始标签
		openLoc := openRegex.FindStringIndex(html[lastIndex:])
		if openLoc == nil {
			result.WriteString(html[lastIndex:])
			break
		}

		openStart := lastIndex + openLoc[0]
		openEnd := lastIndex + openLoc[1]

		// 写入开始标签之前的内容
		result.WriteString(html[lastIndex:openStart])

		// 查找对应的结束标签
		remaining := html[openEnd:]
		closeLoc := closeRegex.FindStringIndex(remaining)
		if closeLoc == nil {
			// 没有找到结束标签，写入开始标签并继续
			result.WriteString(html[openStart:openEnd])
			lastIndex = openEnd
			continue
		}

		closeStart := openEnd + closeLoc[0]
		closeEnd := openEnd + closeLoc[1]

		// 提取标题内容
		content := html[openEnd:closeStart]
		text := strings.TrimSpace(stripTags(content))

		if text != "" {
			prefix := strings.Repeat("#", level)
			result.WriteString(fmt.Sprintf("\n%s %s\n", prefix, text))
		}

		lastIndex = closeEnd
	}

	return result.String()
}

func extractTextFromURL(href string) string {
	// 移除协议和域名
	text := regexp.MustCompile(`^https?://[^/]+`).ReplaceAllString(href, "")
	// 移除查询参数
	text = regexp.MustCompile(`\?.*$`).ReplaceAllString(text, "")
	// 移除锚点
	text = regexp.MustCompile(`#.*$`).ReplaceAllString(text, "")
	// 获取路径部分
	text = strings.Trim(text, "/")

	if text == "" {
		return "链接"
	}

	parts := strings.Split(text, "/")
	lastPart := parts[len(parts)-1]

	// 移除扩展名
	lastPart = regexp.MustCompile(`\.[^.]+$`).ReplaceAllString(lastPart, "")
	// 替换连字符和下划线为空格
	lastPart = strings.ReplaceAll(lastPart, "-", " ")
	lastPart = strings.ReplaceAll(lastPart, "_", " ")

	if len(lastPart) > 30 {
		lastPart = lastPart[:30] + "..."
	}

	if lastPart != "" {
		// 首字母大写每个单词
		words := strings.Fields(lastPart)
		for i, word := range words {
			if len(word) > 0 {
				if len(word) == 1 {
					words[i] = strings.ToUpper(word)
				} else {
					words[i] = strings.ToUpper(word[:1]) + strings.ToLower(word[1:])
				}
			}
		}
		return strings.Join(words, " ")
	}

	return "链接"
}

func isNoiseLine(line string) bool {
	noisePatterns := []string{
		`^[\s\p{P}]*$`, // 只有标点符号
		`^©\s*\d{4}`,   // 版权声明
		`^All Rights Reserved`,
		`^\{.*\}`, // JSON数据
		`^\d+$`,   // 纯数字
	}

	for _, pattern := range noisePatterns {
		if matched, _ := regexp.MatchString(pattern, line); matched {
			return true
		}
	}
	return false
}

// HTML实体解码
func htmlUnescape(text string) string {
	entities := map[string]string{
		"&amp;":   "&",
		"&lt;":    "<",
		"&gt;":    ">",
		"&quot;":  "\"",
		"&#39;":   "'",
		"&nbsp;":  " ",
		"&mdash;": "—",
		"&ndash;": "–",
		"&lsquo;": "'",
		"&rsquo;": "'",
		"&ldquo;": "\"",
		"&rdquo;": "\"",
	}

	for entity, char := range entities {
		text = strings.ReplaceAll(text, entity, char)
	}

	// 处理数字实体
	numEntityRegex := regexp.MustCompile(`&#(\d+);`)
	text = numEntityRegex.ReplaceAllStringFunc(text, func(match string) string {
		subMatch := numEntityRegex.FindStringSubmatch(match)
		if len(subMatch) >= 2 {
			if code, err := fmt.Sscanf(subMatch[1], "%d", new(int)); err == nil && code > 0 {
				var num int
				fmt.Sscanf(subMatch[1], "%d", &num)
				return string(rune(num))
			}
		}
		return match
	})

	return text
}

// ParseToMarkdown 解析为结构化Markdown字符串（兼容旧接口）
func (p *EnhancedHTMLParser) ParseToMarkdown() (string, error) {
	result, err := p.ParseToStructuredMarkdown()
	if err != nil {
		return "", err
	}

	return p.formatStructuredMarkdown(result), nil
}

// formatStructuredMarkdown 格式化结构化的Markdown输出
func (p *EnhancedHTMLParser) formatStructuredMarkdown(result *StructuredMarkdownResult) string {
	var builder strings.Builder

	builder.WriteString(fmt.Sprintf("# %s\n\n", result.Title))

	builder.WriteString("---\n")
	builder.WriteString(fmt.Sprintf("**原文链接**: [%s](%s)\n", result.URL, result.URL))
	if result.Author != "" {
		builder.WriteString(fmt.Sprintf("**作者**: %s\n", result.Author))
	}
	if result.PublishDate != "" {
		builder.WriteString(fmt.Sprintf("**发布日期**: %s\n", result.PublishDate))
	}
	builder.WriteString(fmt.Sprintf("**转换时间**: %s\n", time.Now().Format("2006-01-02 15:04:05")))
	builder.WriteString("---\n\n")

	builder.WriteString(result.Content)
	builder.WriteString("\n")

	return builder.String()
}

// extractMainContent 提取主要内容区域
func (p *EnhancedHTMLParser) extractMainContent() *goquery.Selection {
	contentSelectors := []string{
		"article",
		"main",
		"[role='main']",
		".post-content",
		".article-content",
		".content",
		".main-content",
		"#content",
		"#main-content",
		".post-body",
		".entry-content",
		".blog-post",
		".page-content",
		".markdown-body",
		".rich-text",
		".body-content",
		"section.content",
		"div[itemprop='articleBody']",
	}

	for _, selector := range contentSelectors {
		selection := p.doc.Find(selector)
		if selection.Length() > 0 {
			text := selection.Text()
			if len(strings.TrimSpace(text)) > 200 {
				return selection
			}
		}
	}

	return p.smartContentDetection()
}

// smartContentDetection 智能内容检测（基于文本密度）
func (p *EnhancedHTMLParser) smartContentDetection() *goquery.Selection {
	var bestMatch *goquery.Selection
	maxTextLen := 0

	p.doc.Find("div, section, main, article").Each(func(i int, s *goquery.Selection) {
		if p.isNoiseElement(s) {
			return
		}

		textLen := len(strings.TrimSpace(s.Text()))
		linkText := s.Find("a").Text()
		linkDensity := float64(len(linkText)) / float64(textLen+1)

		if textLen > 500 && textLen > maxTextLen && linkDensity < 0.3 {
			maxTextLen = textLen
			bestMatch = s
		}
	})

	if bestMatch == nil {
		return p.doc.Find("body")
	}

	return bestMatch
}

// isNoiseElement 判断是否为噪音元素
func (p *EnhancedHTMLParser) isNoiseElement(s *goquery.Selection) bool {
	noiseKeywords := []string{
		"nav", "menu", "sidebar", "footer", "header",
		"ad", "advertisement", "banner", "popup",
		"comment", "share", "social", "related",
		"recommend", "subscribe", "newsletter",
	}

	class, _ := s.Attr("class")
	id, _ := s.Attr("id")
	combined := strings.ToLower(class + " " + id)

	for _, keyword := range noiseKeywords {
		if strings.Contains(combined, keyword) {
			return true
		}
	}

	return false
}

// parseContentToStructuredMarkdown 解析内容为结构化Markdown
func (p *EnhancedHTMLParser) parseContentToStructuredMarkdown(selection *goquery.Selection, builder *strings.Builder, result *StructuredMarkdownResult) {
	p.processNodeStructured(selection, builder, 0, result)
}

// processNodeStructured 递归处理DOM节点（结构化版本）
func (p *EnhancedHTMLParser) processNodeStructured(s *goquery.Selection, builder *strings.Builder, level int, result *StructuredMarkdownResult) {
	nodeName := goquery.NodeName(s)

	switch nodeName {
	case "h1", "h2", "h3", "h4", "h5", "h6":
		headingLevel := int(nodeName[1] - '0')
		text := strings.TrimSpace(s.Text())
		if text != "" {
			prefix := strings.Repeat("#", headingLevel)
			builder.WriteString(fmt.Sprintf("\n%s %s\n\n", prefix, text))
		}

	case "p":
		text := p.cleanText(s.Text())
		if text != "" {
			p.processParagraphWithLinks(s, builder, result)
			builder.WriteString("\n\n")
		}

	case "img":
		if src, exists := s.Attr("src"); exists {
			alt, _ := s.Attr("alt")
			title, _ := s.Attr("title")
			imgURL := p.makeAbsoluteURL(src)
			// 忽略小图标和装饰图
			if strings.Contains(src, "icon") || strings.Contains(src, "logo") {
				return
			}
			if alt == "" {
				alt = "图片"
			}
			if title != "" {
				builder.WriteString(fmt.Sprintf("![%s](%s \"%s\")\n\n", alt, imgURL, title))
			} else {
				builder.WriteString(fmt.Sprintf("![%s](%s)\n\n", alt, imgURL))
			}
		}

	case "a":
		href, exists := s.Attr("href")
		// 获取链接文本，包括子元素中的文本（如button内的文本）
		text := strings.TrimSpace(s.Text())
		title, _ := s.Attr("title")

		if exists && !strings.HasPrefix(href, "javascript:") && href != "#" && href != "" {
			// 如果文本为空，尝试从子元素获取
			if text == "" {
				// 检查是否有img子元素
				if img := s.Find("img"); img.Length() > 0 {
					if alt, exists := img.Attr("alt"); exists && alt != "" {
						text = alt
					}
				}
				// 检查是否有button/span/div子元素
				if text == "" {
					for _, tag := range []string{"button", "span", "div", "strong"} {
						if childText := strings.TrimSpace(s.Find(tag).First().Text()); childText != "" {
							text = childText
							break
						}
					}
				}
				// 从URL提取有意义的文本
				if text == "" {
					text = extractTextFromURL(href)
				}
			}

			if text != "" {
				absURL := p.makeAbsoluteURL(href)
				result.Links = append(result.Links, LinkInfo{
					Text:  text,
					URL:   absURL,
					Title: title,
				})
				if title != "" {
					builder.WriteString(fmt.Sprintf("[%s](%s \"%s\")", text, absURL, title))
				} else {
					builder.WriteString(fmt.Sprintf("[%s](%s)", text, absURL))
				}
			}
		} else {
			builder.WriteString(text)
		}

	case "ul", "ol":
		isOrdered := nodeName == "ol"
		s.Find("li").Each(func(i int, li *goquery.Selection) {
			liText := p.processInlineLinks(li, result)
			liText = p.cleanText(liText)
			if liText != "" {
				if isOrdered {
					builder.WriteString(fmt.Sprintf("%d. %s\n", i+1, liText))
				} else {
					builder.WriteString(fmt.Sprintf("- %s\n", liText))
				}
			}
		})
		builder.WriteString("\n")

	case "pre", "code":
		code := s.Text()
		language := p.detectCodeLanguage(s)
		builder.WriteString(fmt.Sprintf("```%s\n%s\n```\n\n", language, strings.TrimSpace(code)))

	case "blockquote":
		text := p.processInlineLinks(s, result)
		text = p.cleanText(text)
		if text != "" {
			lines := strings.Split(text, "\n")
			for _, line := range lines {
				line = strings.TrimSpace(line)
				if line != "" {
					builder.WriteString(fmt.Sprintf("> %s\n", line))
				}
			}
			builder.WriteString("\n")
		}

	case "table":
		p.parseTableStructured(s, builder)

	case "div", "section", "article", "span", "main", "button":
		handled := false

		// 1. 首先检查自身是否是按钮，且是否有链接相关的属性
		if nodeName == "button" {
			// 检查自身属性
			for _, attr := range []string{"data-url", "data-href", "data-link", "data-download", "href"} {
				if url, exists := s.Attr(attr); exists && url != "" {
					text := strings.TrimSpace(s.Text())
					if text != "" {
						absURL := p.makeAbsoluteURL(url)
						result.Links = append(result.Links, LinkInfo{
							Text: text,
							URL:  absURL,
						})
						builder.WriteString(fmt.Sprintf("[%s](%s)\n\n", text, absURL))
						handled = true
						break
					}
				}
			}

			// 检查onclick
			if !handled {
				if onclick, exists := s.Attr("onclick"); exists {
					urlMatch := regexp.MustCompile(`(?:location\.href|window\.open)\s*\(\s*['"]([^'"]+)['"]`).FindStringSubmatch(onclick)
					if len(urlMatch) >= 2 {
						text := strings.TrimSpace(s.Text())
						if text != "" {
							absURL := p.makeAbsoluteURL(urlMatch[1])
							result.Links = append(result.Links, LinkInfo{
								Text: text,
								URL:  absURL,
							})
							builder.WriteString(fmt.Sprintf("[%s](%s)\n\n", text, absURL))
							handled = true
						}
					}
				}
			}

			// 关键：查找父级是否在<a>标签内
			if !handled {
				parent := s.Parent()
				for parent.Length() > 0 {
					if goquery.NodeName(parent) == "a" {
						if href, exists := parent.Attr("href"); exists && href != "" {
							text := strings.TrimSpace(s.Text())
							if text != "" {
								absURL := p.makeAbsoluteURL(href)
								result.Links = append(result.Links, LinkInfo{
									Text: text,
									URL:  absURL,
								})
								builder.WriteString(fmt.Sprintf("[%s](%s)\n\n", text, absURL))
								handled = true
							}
						}
						break
					}
					parent = parent.Parent()
				}
			}

			// 如果还没找到，尝试在父级的后续兄弟元素中查找隐藏的链接
			if !handled {
				// 有些React应用会在按钮后面放隐藏的<a>标签
				if nextSibling := s.Next(); nextSibling.Length() > 0 {
					if goquery.NodeName(nextSibling) == "a" {
						if href, exists := nextSibling.Attr("href"); exists && href != "" {
							text := strings.TrimSpace(s.Text())
							if text != "" {
								absURL := p.makeAbsoluteURL(href)
								result.Links = append(result.Links, LinkInfo{
									Text: text,
									URL:  absURL,
								})
								builder.WriteString(fmt.Sprintf("[%s](%s)\n\n", text, absURL))
								handled = true
							}
						}
					}
				}
			}
		}

		// 2. 检查 data-url 等属性（针对div/span等容器）
		if !handled {
			for _, attr := range []string{"data-url", "data-href", "data-link", "data-download"} {
				if dataURL, exists := s.Attr(attr); exists && dataURL != "" {
					text := strings.TrimSpace(s.Text())
					if text != "" {
						absURL := p.makeAbsoluteURL(dataURL)
						result.Links = append(result.Links, LinkInfo{
							Text: text,
							URL:  absURL,
						})
						builder.WriteString(fmt.Sprintf("[%s](%s)\n\n", text, absURL))
						handled = true
					}
					break
				}
			}
		}

		// 3. 检查是否有onclick且未被处理
		if !handled {
			if onclick, exists := s.Attr("onclick"); exists {
				urlMatch := regexp.MustCompile(`(?:location\.href|window\.open)\s*\(\s*['"]([^'"]+)['"]`).FindStringSubmatch(onclick)
				if len(urlMatch) >= 2 {
					text := strings.TrimSpace(s.Text())
					if text != "" {
						absURL := p.makeAbsoluteURL(urlMatch[1])
						result.Links = append(result.Links, LinkInfo{
							Text: text,
							URL:  absURL,
						})
						builder.WriteString(fmt.Sprintf("[%s](%s)\n\n", text, absURL))
						handled = true
					}
				}
			}
		}

		// 4. 递归处理所有子元素
		s.Children().Each(func(i int, child *goquery.Selection) {
			p.processNodeStructured(child, builder, level+1, result)
		})

	default:
		directText := p.getDirectText(s)
		if directText != "" {
			builder.WriteString(directText)
		}
		// 也处理子元素
		s.Children().Each(func(i int, child *goquery.Selection) {
			p.processNodeStructured(child, builder, level+1, result)
		})
	}
}

// processParagraphWithLinks 处理段落中的链接
func (p *EnhancedHTMLParser) processParagraphWithLinks(s *goquery.Selection, builder *strings.Builder, result *StructuredMarkdownResult) {
	s.Clone().Contents().Each(func(i int, node *goquery.Selection) {
		if goquery.NodeName(node) == "a" {
			href, exists := node.Attr("href")
			text := strings.TrimSpace(node.Text())
			title, _ := node.Attr("title")

			if exists && text != "" {
				absURL := p.makeAbsoluteURL(href)
				result.Links = append(result.Links, LinkInfo{
					Text:  text,
					URL:   absURL,
					Title: title,
				})
				if title != "" {
					builder.WriteString(fmt.Sprintf("[%s](%s \"%s\")", text, absURL, title))
				} else {
					builder.WriteString(fmt.Sprintf("[%s](%s)", text, absURL))
				}
			} else {
				builder.WriteString(text)
			}
		} else if goquery.NodeName(node) == "#text" {
			builder.WriteString(node.Text())
		} else {
			p.processInlineLinks(node, result)
		}
	})
}

// processInlineLinks 处理内联链接
func (p *EnhancedHTMLParser) processInlineLinks(s *goquery.Selection, result *StructuredMarkdownResult) string {
	var builder strings.Builder

	s.Contents().Each(func(i int, node *goquery.Selection) {
		if goquery.NodeName(node) == "a" {
			href, exists := node.Attr("href")
			text := strings.TrimSpace(node.Text())
			title, _ := node.Attr("title")

			if exists && text != "" {
				absURL := p.makeAbsoluteURL(href)
				result.Links = append(result.Links, LinkInfo{
					Text:  text,
					URL:   absURL,
					Title: title,
				})
				if title != "" {
					builder.WriteString(fmt.Sprintf("[%s](%s \"%s\")", text, absURL, title))
				} else {
					builder.WriteString(fmt.Sprintf("[%s](%s)", text, absURL))
				}
			} else {
				builder.WriteString(text)
			}
		} else if goquery.NodeName(node) == "#text" {
			builder.WriteString(node.Text())
		} else {
			builder.WriteString(node.Text())
		}
	})

	return builder.String()
}

// cleanText 清理文本内容
func (p *EnhancedHTMLParser) cleanText(text string) string {
	text = regexp.MustCompile(`\s+`).ReplaceAllString(text, " ")
	text = strings.TrimSpace(text)

	noisePatterns := []string{
		`(?i)点击.*查看`,
		`(?i)阅读原文`,
		`(?i)查看更多`,
		`(?i)分享到.*`,
		`^广告$`,
		`(?i)相关推荐`,
	}

	for _, pattern := range noisePatterns {
		text = regexp.MustCompile(pattern).ReplaceAllString(text, "")
	}

	return strings.TrimSpace(text)
}

// getDirectText 获取节点的直接文本
func (p *EnhancedHTMLParser) getDirectText(s *goquery.Selection) string {
	var texts []string
	for _, node := range s.Nodes {
		if node.Type == 3 {
			text := strings.TrimSpace(node.Data)
			if text != "" {
				texts = append(texts, text)
			}
		}
	}

	if s.Children().Length() > 0 {
		return ""
	}

	return strings.Join(texts, " ")
}

// parseTableStructured 解析表格为Markdown
func (p *EnhancedHTMLParser) parseTableStructured(table *goquery.Selection, builder *strings.Builder) {
	var rows [][]string

	hasHeader := table.Find("thead").Length() > 0

	if hasHeader {
		table.Find("thead tr").Each(func(i int, tr *goquery.Selection) {
			var row []string
			tr.Find("th").Each(func(j int, cell *goquery.Selection) {
				row = append(row, p.cleanText(cell.Text()))
			})
			if len(row) > 0 {
				rows = append(rows, row)
			}
		})

		table.Find("tbody tr").Each(func(i int, tr *goquery.Selection) {
			var row []string
			tr.Find("td").Each(func(j int, cell *goquery.Selection) {
				row = append(row, p.cleanText(cell.Text()))
			})
			if len(row) > 0 {
				rows = append(rows, row)
			}
		})
	} else {
		table.Find("tr").Each(func(i int, tr *goquery.Selection) {
			var row []string
			tr.Find("th, td").Each(func(j int, cell *goquery.Selection) {
				row = append(row, p.cleanText(cell.Text()))
			})
			if len(row) > 0 {
				rows = append(rows, row)
			}
		})
	}

	if len(rows) == 0 {
		return
	}

	maxCols := 0
	for _, row := range rows {
		if len(row) > maxCols {
			maxCols = len(row)
		}
	}

	for i := range rows {
		for len(rows[i]) < maxCols {
			rows[i] = append(rows[i], "")
		}
	}

	builder.WriteString("\n")
	for i, row := range rows {
		builder.WriteString("|")
		for j := 0; j < maxCols; j++ {
			builder.WriteString(fmt.Sprintf(" %s |", row[j]))
		}
		builder.WriteString("\n")

		if i == 0 {
			builder.WriteString("|")
			for j := 0; j < maxCols; j++ {
				builder.WriteString(" --- |")
			}
			builder.WriteString("\n")
		}
	}
	builder.WriteString("\n")
}

// detectCodeLanguage 检测代码语言
func (p *EnhancedHTMLParser) detectCodeLanguage(codeEl *goquery.Selection) string {
	if class, exists := codeEl.Attr("class"); exists {
		langPatterns := map[string][]string{
			"go":         {"language-go", "lang-go", "go"},
			"python":     {"language-python", "lang-python", "python", "py"},
			"javascript": {"language-javascript", "lang-javascript", "javascript", "js"},
			"java":       {"language-java", "lang-java", "java"},
			"html":       {"language-html", "lang-html", "html"},
			"css":        {"language-css", "lang-css", "css"},
			"sql":        {"language-sql", "lang-sql", "sql"},
			"json":       {"language-json", "lang-json", "json"},
			"bash":       {"language-bash", "lang-bash", "bash", "shell"},
			"yaml":       {"language-yaml", "lang-yaml", "yaml", "yml"},
		}

		classLower := strings.ToLower(class)
		for lang, patterns := range langPatterns {
			for _, pattern := range patterns {
				if strings.Contains(classLower, pattern) {
					return lang
				}
			}
		}
	}

	return ""
}

// isInternalLink 判断是否为内部链接
func (p *EnhancedHTMLParser) isInternalLink(href string) bool {
	if href == "" || strings.HasPrefix(href, "javascript:") || href == "#" {
		return true
	}

	if strings.HasPrefix(href, "/") {
		return true
	}

	return false
}

// makeAbsoluteURL 转换为绝对URL
func (p *EnhancedHTMLParser) makeAbsoluteURL(href string) string {
	if strings.HasPrefix(href, "http://") || strings.HasPrefix(href, "https://") {
		return href
	}

	baseURL, err := url.Parse(p.url)
	if err != nil {
		return href
	}

	relativeURL, err := url.Parse(href)
	if err != nil {
		return href
	}

	return baseURL.ResolveReference(relativeURL).String()
}

// FallbackParseToMarkdown 降级解析Markdown
func (p *EnhancedHTMLParser) FallbackParseToMarkdown() string {
	var builder strings.Builder

	if p.title != "" {
		builder.WriteString(fmt.Sprintf("# %s\n\n", p.title))
	}

	builder.WriteString(fmt.Sprintf("**来源**: %s\n\n", p.url))

	paragraphs := regexp.MustCompile(`(?i)<p[^>]*>([\s\S]*?)</p>`).FindAllStringSubmatch(p.htmlContent, -1)
	for _, match := range paragraphs {
		if len(match) >= 2 {
			text := stripTags(match[1])
			text = strings.TrimSpace(text)
			if text != "" && len(text) > 20 {
				builder.WriteString(fmt.Sprintf("%s\n\n", text))
			}
		}
	}

	lists := regexp.MustCompile(`(?i)<ul[^>]*>([\s\S]*?)</ul>`).FindAllStringSubmatch(p.htmlContent, -1)
	for _, listMatch := range lists {
		if len(listMatch) >= 2 {
			items := regexp.MustCompile(`(?i)<li[^>]*>([\s\S]*?)</li>`).FindAllStringSubmatch(listMatch[1], -1)
			for _, item := range items {
				if len(item) >= 2 {
					text := stripTags(item[1])
					text = strings.TrimSpace(text)
					if text != "" {
						builder.WriteString(fmt.Sprintf("- %s\n", text))
					}
				}
			}
			builder.WriteString("\n")
		}
	}

	return builder.String()
}
