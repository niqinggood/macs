package workspace

import (
	"archive/zip" // 新增：用于打包 ZIP（DOCX 本质是 ZIP）
	"bytes"
	"context"
	"encoding/base64"
	"encoding/csv"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"html"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"mac/internal/fsutil"
	"mac/internal/portablepath"
	"mac/internal/runtimeutil"
	"mac/internal/spreadsheet"
	"mac/internal/validationhint"

	"github.com/xuri/excelize/v2"
	"golang.org/x/text/encoding/simplifiedchinese"  // 用于 Big5
	"golang.org/x/text/encoding/traditionalchinese" // 用于 Big5
	"golang.org/x/text/transform"
	"gopkg.in/yaml.v3"
)

type FileFormat string

const (
	FormatText     FileFormat = "txt"
	FormatMarkdown FileFormat = "md"
	FormatJSON     FileFormat = "json"
	FormatYAML     FileFormat = "yaml"
	FormatXML      FileFormat = "xml"
	FormatCSV      FileFormat = "csv"
	FormatHTML     FileFormat = "html"
	FormatDOCX     FileFormat = "docx" // Word
	FormatXLSX     FileFormat = "xlsx" // Excel
	FormatPDF      FileFormat = "pdf"
	FormatPPTX     FileFormat = "pptx" // PowerPoint
	FormatPNG      FileFormat = "png"  // 图片
	FormatJPG      FileFormat = "jpg"  // 图片
	FormatMP3      FileFormat = "mp3"  // 音频
	FormatWAV      FileFormat = "wav"  // 音频
)

type WriteFileTool struct {
	workspace string
	deps      FileToolDependencies
}

// FileToolDependencies keeps authenticated workspace policy and application
// post-edit diagnostics outside the reusable read/write implementations.
type FileToolDependencies struct {
	EnforcePath       func(context.Context, string, string) error
	IsProtectedPath   func(string) bool
	ProtectionWarning func(string) string
	SafeJoin          func(string, string) (string, error)
	AtomicWrite       func(string, []byte, os.FileMode) error
	FinalizeWrite     func(workspace, filePath, writerID string) (string, error)
	CountTokens       func(string) int
	IdentityKey       func(string) string
}

func defaultFileToolDependencies() FileToolDependencies {
	return FileToolDependencies{
		EnforcePath:       func(context.Context, string, string) error { return nil },
		IsProtectedPath:   func(string) bool { return false },
		ProtectionWarning: func(string) string { return "" },
		SafeJoin:          safeJoin,
		AtomicWrite:       fsutil.WriteAtomic,
		FinalizeWrite:     finalizeWrite,
		CountTokens:       runtimeutil.EstimateTokens,
		IdentityKey: func(path string) string {
			absolute, err := filepath.Abs(filepath.Clean(path))
			if err != nil {
				absolute = filepath.Clean(path)
			}
			if runtime.GOOS == "windows" {
				return strings.ToLower(absolute)
			}
			return absolute
		},
	}
}

func NewWriteFileTool(workspace string, dependencies ...FileToolDependencies) *WriteFileTool {
	deps := defaultFileToolDependencies()
	if len(dependencies) > 0 {
		deps = mergeFileToolDependencies(deps, dependencies[0])
	}
	return &WriteFileTool{workspace: workspace, deps: deps}
}

func mergeFileToolDependencies(defaults, supplied FileToolDependencies) FileToolDependencies {
	if supplied.EnforcePath != nil {
		defaults.EnforcePath = supplied.EnforcePath
	}
	if supplied.IsProtectedPath != nil {
		defaults.IsProtectedPath = supplied.IsProtectedPath
	}
	if supplied.ProtectionWarning != nil {
		defaults.ProtectionWarning = supplied.ProtectionWarning
	}
	if supplied.SafeJoin != nil {
		defaults.SafeJoin = supplied.SafeJoin
	}
	if supplied.AtomicWrite != nil {
		defaults.AtomicWrite = supplied.AtomicWrite
	}
	if supplied.FinalizeWrite != nil {
		defaults.FinalizeWrite = supplied.FinalizeWrite
	}
	if supplied.CountTokens != nil {
		defaults.CountTokens = supplied.CountTokens
	}
	if supplied.IdentityKey != nil {
		defaults.IdentityKey = supplied.IdentityKey
	}
	return defaults
}

func safeJoin(base, path string) (string, error) {
	path = strings.TrimSpace(path)
	if portablepath.IsAbs(path) {
		if filepath.IsAbs(path) {
			return filepath.Abs(filepath.Clean(path))
		}
		return path, nil
	}
	path = filepath.Clean(path)
	baseAbsolute, err := filepath.Abs(base)
	if err != nil {
		return "", err
	}
	cleanPath := filepath.Clean(filepath.Join(baseAbsolute, path))
	relative, err := filepath.Rel(baseAbsolute, cleanPath)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("路径 '%s' 尝试逃逸出工作空间", path)
	}
	return cleanPath, nil
}

func finalizeWrite(workspace, filePath, writerID string) (string, error) {
	fingerprint, err := FingerprintFile(filePath)
	if err != nil {
		return "", fmt.Errorf("fingerprint final edited file: %w", err)
	}
	GetFileStateRegistry().NoteWrite(filePath, writerID)
	displayPath := filePath
	if relative, relErr := filepath.Rel(workspace, filePath); relErr == nil && relative != "" && !strings.HasPrefix(relative, "..") {
		displayPath = relative
	}
	return fmt.Sprintf("\n[Edit receipt] path=%s sha256=%s bytes=%d lsp=not-installed", displayPath, fingerprint.ContentSHA256, fingerprint.Size), nil
}

func (t *WriteFileTool) Name() string { return "write_file" }

// ScopePath implements PathScopedTool interface for parallel safety
func (t *WriteFileTool) ScopePath(args map[string]interface{}) string {
	if p, ok := args["path"].(string); ok {
		if filepath.IsAbs(p) {
			return filepath.Clean(p)
		}
		return filepath.Join(t.workspace, p)
	}
	if p, ok := args["file"].(string); ok {
		if filepath.IsAbs(p) {
			return filepath.Clean(p)
		}
		return filepath.Join(t.workspace, p)
	}
	return ""
}

func (t *WriteFileTool) SnapshotPaths(args map[string]interface{}) []string {
	if path := t.ScopePath(args); path != "" {
		return []string{path}
	}
	return nil
}

func (t *WriteFileTool) Description() string {
	return "Create a new file or generated artifact. Prefer edit_file for existing source files after reading them first. " +
		"Auto-detects format from extension; supports .txt .md .json .yaml .csv .html .docx .xlsx .pdf. " +
		"For .xlsx, provide RFC 4180-style comma-delimited rows; the host writes a real Excel OOXML workbook. " +
		"Place generated reports and task artifacts under .openkitty/storage/. Parent directories are created automatically. " +
		"Always emit the required path argument before content. For content above roughly 6,000 characters, write bounded text/source chunks with append=true; for JSON, create a small valid skeleton and use json_patch instead of appending invalid fragments."
}

func (t *WriteFileTool) SchemaRepairAdvice(args map[string]interface{}) string {
	return "Write protocol: emit path before content. If content is large, send a bounded first chunk and append bounded text/source chunks; for JSON, create a small valid skeleton and use json_patch. Never guess a missing destination path."
}

func (t *WriteFileTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"path": map[string]interface{}{
				"type":        "string",
				"description": "Required first argument. Emit this path before generating content (e.g. '.openkitty/storage/report.md'). Format is auto-detected from the extension.",
			},
			"content": map[string]interface{}{
				"type":        "string",
				"description": "Content to write after path. Keep each payload below roughly 6,000 characters when practical. Append bounded chunks for text/source; use json_patch for structural changes to an existing JSON document.",
			},
			"append": map[string]interface{}{
				"type":        "boolean",
				"description": "Append to existing file instead of overwriting (text formats only). Default: false.",
			},
		},
		"required": []string{"path", "content"},
	}
}
func (t *WriteFileTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	path, _ := params["path"].(string)
	if path == "" {
		path, _ = params["file"].(string)
	}
	content, _ := params["content"].(string)
	appendMode := false
	if a, ok := params["append"].(bool); ok {
		appendMode = a
	}

	if path == "" {
		return "", fmt.Errorf("path is required")
	}
	if content == "" {
		return "", fmt.Errorf("content is required")
	}
	if err := t.deps.EnforcePath(ctx, t.workspace, path); err != nil {
		return "", err
	}

	// System directory protection
	if t.deps.IsProtectedPath(path) {
		return "", fmt.Errorf("BLOCKED: '%s' is a system-protected path. %s", path, t.deps.ProtectionWarning(path))
	}

	// === openclaw式内容清理：自动净化LLM输出的代码内容 ===

	// 1. 去除 markdown 代码块包裹
	if strings.HasPrefix(strings.TrimSpace(content), "```") {
		content = strings.TrimSpace(content)
		if idx := strings.Index(content, "\n"); idx > 0 {
			content = content[idx+1:]
		}
		if strings.HasSuffix(strings.TrimSpace(content), "```") {
			content = strings.TrimSpace(content)
			content = content[:len(content)-3]
		}
	}

	// 2. HTML文件特殊处理：提取纯净HTML
	ext := strings.ToLower(filepath.Ext(path))
	if ext == ".html" || ext == ".htm" {
		// 提取完整的HTML：找到第一个<html和最后一个</html>之间的全部内容
		htmlStart := strings.Index(content, "<html")
		htmlEnd := strings.LastIndex(content, "</html>")
		if htmlStart >= 0 && htmlEnd > htmlStart {
			content = content[htmlStart : htmlEnd+7]
		}
		// 解码 HTML 实体 + 去除残留的 <p> 包裹
		content = html.UnescapeString(content)
		// 智能去嵌套<p>: 只去掉包裹HTML标签的假段落
		content = regexp.MustCompile(`<p>\s*(<(html|head|meta|title|style|body|link|script|div|!DOCTYPE)[^>]*>)`).ReplaceAllString(content, "$1")
		content = regexp.MustCompile(`(</(html|head|body|style|script|title)>\s*)</p>`).ReplaceAllString(content, "$1")
	}

	// 3. 通用清理
	content = strings.TrimSpace(content)

	// Auto-detect format from extension
	fileFormat := detectFormat(path, "")

	// Resolve path with traversal protection
	fullPath := path
	if !filepath.IsAbs(fullPath) {
		fullPath = filepath.Join(t.workspace, fullPath)
	}
	if safePath, err := t.deps.SafeJoin(t.workspace, fullPath); err == nil {
		fullPath = safePath
	}
	GetFileStateRegistry().LockPath(fullPath)
	defer GetFileStateRegistry().UnlockPath(fullPath)
	// File coordination: check if another agent modified since our read
	if stale := GetFileStateRegistry().CheckBeforeWrite(fullPath, ReaderIDFromContext(ctx)); !stale.OK {
		return "", fmt.Errorf("%s", stale.Message)
	}

	// Ensure parent directory exists
	if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
		return fmt.Sprintf("create dir failed: %v", err), nil
	}

	var writeErr error
	var size int
	switch fileFormat {
	case FormatText, FormatMarkdown:
		size, writeErr = t.writeTextFile(ctx, fullPath, content, "utf-8", appendMode)
	case FormatJSON:
		size, writeErr = t.writeJSONFile(ctx, fullPath, content, nil)
	case FormatYAML:
		size, writeErr = t.writeYAMLFile(ctx, fullPath, content, nil)
	case FormatCSV:
		size, writeErr = t.writeCSVFile(ctx, fullPath, content, nil)
	case FormatHTML:
		size, writeErr = t.writeHTMLFile(ctx, fullPath, content, nil)
	case FormatDOCX:
		size, writeErr = t.WriteDOCXFile(fullPath, content, nil)
	case FormatXLSX:
		size, writeErr = t.writeXLSXFile(ctx, fullPath, content, nil)
	case FormatPDF:
		size, writeErr = t.writePDFFile(ctx, fullPath, content, nil)
	case FormatPPTX:
		writeErr = fmt.Errorf("write_file cannot create binary PowerPoint files directly; use a presentation Skill with convert, python-pptx, PptxGenJS, or the bundled upstream PPT Master workflow")
	default:
		size, writeErr = t.writeTextFile(ctx, fullPath, content, "utf-8", appendMode)
	}

	if writeErr != nil {
		return fmt.Sprintf("write failed: %v", writeErr), nil
	}
	postEditReport, postEditErr := t.deps.FinalizeWrite(t.workspace, fullPath, ReaderIDFromContext(ctx))
	if postEditErr != nil {
		return "", fmt.Errorf("file was written, but its final state could not be verified: %w", postEditErr)
	}

	result := fmt.Sprintf("成功写入文件 %s (%s, %d 字节 %d token)", path, fileFormat, size, t.deps.CountTokens(content))

	// Auto-validate HTML files after write
	if strings.HasSuffix(strings.ToLower(path), ".html") {
		validateScript := filepath.Join("skills", "web-design", "validate.py")
		if _, err := os.Stat(validateScript); err == nil {
			validateCmd := exec.Command("python3", "-X", "utf8", validateScript, fullPath)
			validateOut, validateErr := validateCmd.CombinedOutput()
			if validateErr != nil {
				// Validation failed — surface errors prominently
				result += fmt.Sprintf("\n\n[VALIDATION FAILED]\n%s\nFix the issues above and re-write the file.", string(validateOut))
			} else if len(validateOut) > 0 {
				result += fmt.Sprintf("\n\n[Validation]\n%s", string(validateOut))
			}
		}
	}

	result += postEditReport
	if hint := validationhint.SuggestedForPathsInWorkspace(t.workspace, []string{fullPath}); hint != "" {
		result += hint
	}
	log.Printf("[INFO] %s", result)
	return result, nil
}

// 检测文件格式
func detectFormat(path string, forcedFormat string) FileFormat {
	// 如果强制指定了格式，优先使用
	if forcedFormat != "" {
		return FileFormat(strings.ToLower(forcedFormat))
	}

	// 从文件扩展名推断
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(path), "."))
	switch ext {
	case "txt", "text":
		return FormatText
	case "md", "markdown":
		return FormatMarkdown
	case "json":
		return FormatJSON
	case "yaml", "yml":
		return FormatYAML
	case "csv":
		return FormatCSV
	case "html", "htm":
		return FormatHTML
	case "docx":
		return FormatDOCX
	case "xlsx":
		return FormatXLSX
	case "pdf":
		return FormatPDF
	case "pptx", "ppt":
		return FormatPPTX
	case "png":
		return FormatPNG
	case "jpg", "jpeg":
		return FormatJPG
	case "mp3":
		return FormatMP3
	case "wav":
		return FormatWAV
	default:
		return FormatText
	}
}

// 写入文本文件
func (t *WriteFileTool) writeTextFile(ctx context.Context, path, content, encoding string, append_mode bool) (int, error) {
	var data []byte
	var err error

	// 处理编码
	switch strings.ToLower(encoding) {
	case "gbk", "gb2312":
		data, err = t.utf8ToGBK([]byte(content))
	case "big5":
		data, err = t.utf8ToBig5([]byte(content))
	default: // utf-8
		data = []byte(content)
	}

	if err != nil {
		return 0, err
	}

	// 非追加模式：原子写入，崩溃不会留下半截文件
	if !append_mode {
		PreEditBackup(path, ctx) // file history: backup before write
		if err := t.deps.AtomicWrite(path, data, 0644); err != nil {
			log.Println("err, path:", path)
			return 0, err
		}
		return len(data), nil
	}

	// 追加模式：OpenFile
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		log.Println("err, path:", path)
		return 0, err
	}
	defer f.Close()

	n, err := f.Write(data)
	return n, err
}
func (t *WriteFileTool) writeJSONFile(ctx context.Context, path, content string, style map[string]interface{}) (int, error) {
	// 尝试解析content为JSON
	var jsonData interface{}
	if err := json.Unmarshal([]byte(content), &jsonData); err != nil {
		// 如果不是有效JSON，作为字符串处理
		jsonData = content
	}

	// 格式化输出
	var data []byte
	var err error

	// 检查样式中的缩进设置
	indent := "  "
	if pretty, ok := style["pretty"].(bool); ok && pretty {
		data, err = json.MarshalIndent(jsonData, "", indent)
	} else {
		data, err = json.Marshal(jsonData)
	}

	if err != nil {
		return 0, err
	}

	return t.writeTextFile(ctx, path, string(data), "utf-8", false)
}

// 写入YAML文件
func (t *WriteFileTool) writeYAMLFile(ctx context.Context, path, content string, style map[string]interface{}) (int, error) {
	// 尝试解析content为YAML
	var yamlData interface{}
	if err := yaml.Unmarshal([]byte(content), &yamlData); err != nil {
		// 如果不是有效YAML，作为字符串处理
		yamlData = content
	}

	// 转换为YAML
	data, err := yaml.Marshal(yamlData)
	if err != nil {
		return 0, err
	}

	return t.writeTextFile(ctx, path, string(data), "utf-8", false)
}

// 写入CSV文件
func (t *WriteFileTool) writeCSVFile(ctx context.Context, path, content string, style map[string]interface{}) (int, error) {
	// 解析CSV内容
	delimiter := ','
	if d, ok := style["delimiter"].(string); ok && len(d) == 1 {
		delimiter = rune(d[0])
	}

	hasHeader := true
	if h, ok := style["has_header"].(bool); ok {
		hasHeader = h
	}

	// 简单的CSV写入（实际项目可以使用encoding/csv）
	var builder strings.Builder

	lines := strings.Split(content, "\n")
	for i, line := range lines {
		if i == 0 && !hasHeader {
			continue
		}
		if strings.TrimSpace(line) == "" {
			continue
		}

		// 分割字段
		fields := strings.Split(line, ",")
		for j, field := range fields {
			if j > 0 {
				builder.WriteRune(delimiter)
			}
			// 处理引号
			if strings.ContainsAny(field, "\n\r,"+string(delimiter)) {
				builder.WriteString(`"` + strings.ReplaceAll(field, `"`, `""`) + `"`)
			} else {
				builder.WriteString(field)
			}
		}
		builder.WriteString("\n")
	}

	return t.writeTextFile(ctx, path, builder.String(), "utf-8", false)
}

// 写入HTML文件
func (t *WriteFileTool) writeHTMLFile(ctx context.Context, path, content string, style map[string]interface{}) (int, error) {
	// If content is already complete HTML, write directly without wrapping
	if strings.HasPrefix(strings.TrimSpace(content), "<!DOCTYPE") || strings.HasPrefix(strings.TrimSpace(content), "<html") {
		return t.writeTextFile(ctx, path, content, "utf-8", false)
	}
	var builder strings.Builder

	// 获取样式参数
	title := "Document"
	if t, ok := style["title"].(string); ok {
		title = t
	}

	author := ""
	if a, ok := style["author"].(string); ok {
		author = a
	}

	// 构建HTML
	builder.WriteString("<!DOCTYPE html>\n")
	builder.WriteString("<html>\n")
	builder.WriteString("<head>\n")
	builder.WriteString(fmt.Sprintf("  <title>%s</title>\n", title))
	builder.WriteString("  <meta charset=\"UTF-8\">\n")

	// 添加CSS样式
	builder.WriteString("  <style>\n")
	builder.WriteString("    body { font-family: Arial, sans-serif; line-height: 1.6; padding: 2em; }\n")
	builder.WriteString("    h1 { color: #333; }\n")
	builder.WriteString("    table { border-collapse: collapse; width: 100%; }\n")
	builder.WriteString("    th, td { border: 1px solid #ddd; padding: 8px; text-align: left; }\n")
	builder.WriteString("    th { background-color: #f2f2f2; }\n")
	builder.WriteString("  </style>\n")
	builder.WriteString("</head>\n")
	builder.WriteString("<body>\n")

	if title != "Document" {
		builder.WriteString(fmt.Sprintf("<h1>%s</h1>\n", title))
	}

	if author != "" {
		builder.WriteString(fmt.Sprintf("<p><em>Author: %s</em></p>\n", author))
	}

	// 将Markdown内容转换为HTML（简单处理）
	htmlContent := t.markdownToHTML(content)
	builder.WriteString(htmlContent)

	builder.WriteString("\n</body>\n")
	builder.WriteString("</html>")

	return t.writeTextFile(ctx, path, builder.String(), "utf-8", false)
}

// Markdown到HTML的简单转换
func (t *WriteFileTool) markdownToHTML(md string) string {
	lines := strings.Split(md, "\n")
	var html strings.Builder

	for _, line := range lines {
		line = strings.TrimRight(line, " \t")
		if strings.HasPrefix(line, "# ") {
			html.WriteString(fmt.Sprintf("<h1>%s</h1>\n", strings.TrimPrefix(line, "# ")))
		} else if strings.HasPrefix(line, "## ") {
			html.WriteString(fmt.Sprintf("<h2>%s</h2>\n", strings.TrimPrefix(line, "## ")))
		} else if strings.HasPrefix(line, "### ") {
			html.WriteString(fmt.Sprintf("<h3>%s</h3>\n", strings.TrimPrefix(line, "### ")))
		} else if strings.HasPrefix(line, "- ") {
			html.WriteString(fmt.Sprintf("<li>%s</li>\n", strings.TrimPrefix(line, "- ")))
		} else if line == "" {
			html.WriteString("<br>\n")
		} else {
			html.WriteString(fmt.Sprintf("<p>%s</p>\n", line))
		}
	}

	return html.String()
}

// 修复乱码版：标准 OpenXML 格式，完美支持中文，无乱码
func (t *WriteFileTool) WriteDOCXFile(path, content string, style map[string]interface{}) (int, error) {
	// 1. 处理样式参数
	title, _ := style["title"].(string)
	author, _ := style["author"].(string)
	date, _ := style["date"].(string)

	// 2. 构建完整的 Word XML 内容（UTF-8 编码 + 完整命名空间）
	var docXML strings.Builder
	// 核心：XML 声明必须指定 UTF-8，且命名空间完整
	docXML.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"
            xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"
            xmlns:w14="http://schemas.microsoft.com/office/word/2010/wordml">
  <w:body>`)

	// 标题（加粗+大号字体）
	if title != "" {
		docXML.WriteString(fmt.Sprintf(`
    <w:p>
      <w:pPr>
        <w:spacing w:after="200"/>
      </w:pPr>
      <w:r>
        <w:rPr>
          <w:b/>
          <w:sz w:val="36"/>
        </w:rPr>
        <w:t>%s</w:t>
      </w:r>
    </w:p>`, t.escapeXML(title)))
	}

	// 作者+日期（小号字体）
	if author != "" || date != "" {
		docXML.WriteString(`<w:p><w:pPr><w:spacing w:after="150"/></w:pPr>`)
		if author != "" {
			docXML.WriteString(fmt.Sprintf(`<w:r><w:rPr><w:sz w:val="20"/></w:rPr><w:t>作者：%s</w:t></w:r>`, t.escapeXML(author)))
		}
		if date != "" {
			docXML.WriteString(fmt.Sprintf(`<w:r><w:rPr><w:sz w:val="20"/></w:rPr><w:t> ｜ 日期：%s</w:t></w:r>`, t.escapeXML(date)))
		}
		docXML.WriteString(`</w:p>`)
	}

	// 正文（按行处理，保留换行）
	lines := strings.Split(content, "\n")
	for _, line := range lines {
		line = strings.TrimRight(line, " \t") // 只去掉右侧空格，保留左侧缩进
		if line == "" {
			// 空行：必须用 <w:br/> 或空段落，不能只写空格
			docXML.WriteString(`<w:p><w:r><w:br/></w:r></w:p>`)
			continue
		}

		// 列表项处理
		if strings.HasPrefix(line, "- ") || strings.HasPrefix(line, "* ") {
			itemText := strings.TrimPrefix(strings.TrimPrefix(line, "- "), "* ")
			docXML.WriteString(fmt.Sprintf(`
    <w:p>
      <w:r>
        <w:t>• %s</w:t>
      </w:r>
    </w:p>`, t.escapeXML(itemText)))
		} else {
			// 普通文本
			docXML.WriteString(fmt.Sprintf(`
    <w:p>
      <w:r>
        <w:t>%s</w:t>
      </w:r>
    </w:p>`, t.escapeXML(line)))
		}
	}

	// 必须闭合标签
	docXML.WriteString(`
  </w:body>
</w:document>`)

	// 3. 创建临时目录（避免权限问题）
	tempDir, err := os.MkdirTemp("", "docx-*")
	if err != nil {
		return 0, fmt.Errorf("创建临时目录失败: %v", err)
	}
	defer os.RemoveAll(tempDir) // 函数结束自动清理

	// 4. 构建完整的 DOCX 目录结构（缺失会导致乱码/无法打开）
	dirs := []string{
		filepath.Join(tempDir, "word"),
		filepath.Join(tempDir, "_rels"),
		filepath.Join(tempDir, "word", "_rels"),
	}
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return 0, fmt.Errorf("创建目录 %s 失败: %v", dir, err)
		}
	}

	// 5. 写入核心文件（缺一不可）
	// 5.1 写入文档内容（核心）
	docPath := filepath.Join(tempDir, "word", "document.xml")
	err = os.WriteFile(docPath, []byte(docXML.String()), 0644)
	if err != nil {
		return 0, fmt.Errorf("写入 document.xml 失败: %v", err)
	}

	// 5.2 写入 [Content_Types].xml（必须，否则Word识别失败）
	contentTypes := `<?xml version="1.0" encoding="UTF-8"?>
<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">
  <Default Extension="xml" ContentType="application/xml"/>
  <Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>
  <Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/>
</Types>`
	err = os.WriteFile(filepath.Join(tempDir, "[Content_Types].xml"), []byte(contentTypes), 0644)
	if err != nil {
		return 0, fmt.Errorf("写入 Content_Types.xml 失败: %v", err)
	}

	// 5.3 写入关系文件（.rels）：缺失会导致乱码/格式错误
	relsContent := `<?xml version="1.0" encoding="UTF-8"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
  <Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/>
</Relationships>`
	err = os.WriteFile(filepath.Join(tempDir, "_rels", ".rels"), []byte(relsContent), 0644)
	if err != nil {
		return 0, fmt.Errorf("写入 .rels 失败: %v", err)
	}

	// 6. 打包为 ZIP（DOCX 本质）：关键是 ZIP 编码为 UTF-8
	outFile, err := os.Create(path)
	if err != nil {
		return 0, fmt.Errorf("创建 DOCX 文件失败: %v", err)
	}
	defer outFile.Close()

	zipWriter := zip.NewWriter(outFile)
	defer zipWriter.Close()

	// 遍历临时目录，写入 ZIP（确保路径是 UTF-8）
	err = filepath.Walk(tempDir, func(filePath string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		// 计算 ZIP 内相对路径（必须用 / 分隔，Windows 下替换 \）
		relPath, _ := filepath.Rel(tempDir, filePath)
		relPath = strings.ReplaceAll(relPath, "\\", "/")

		// 核心：ZIP 条目必须用 UTF-8 编码
		writer, err := zipWriter.Create(relPath)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(filePath)
		if err != nil {
			return err
		}
		_, err = writer.Write(data)
		return err
	})

	if err != nil {
		return 0, fmt.Errorf("打包 ZIP 失败: %v", err)
	}

	// 7. 获取文件大小
	fileInfo, err := os.Stat(path)
	if err != nil {
		return 0, fmt.Errorf("获取文件信息失败: %v", err)
	}

	return int(fileInfo.Size()), nil
}

// 辅助函数：XML 特殊字符转义（解决中文乱码核心）
func (t *WriteFileTool) escapeXML(s string) string {
	// 必须转义的 XML 特殊字符
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, "\"", "&quot;")
	s = strings.ReplaceAll(s, "'", "&apos;")
	return s
}

func (t *WriteFileTool) convertHTMLToDocXML(html string) string {
	var xml strings.Builder

	lines := strings.Split(html, "\n")
	inList := false

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			xml.WriteString(`<w:p><w:r><w:br/></w:r></w:p>`)
			continue
		}

		if strings.HasPrefix(line, "<h1>") {
			text := strings.TrimSuffix(strings.TrimPrefix(line, "<h1>"), "</h1>")
			xml.WriteString(fmt.Sprintf(`<w:p><w:pPr><w:pStyle w:val="Heading1"/></w:pPr><w:r><w:t>%s</w:t></w:r></w:p>`, text))
		} else if strings.HasPrefix(line, "<h2>") {
			text := strings.TrimSuffix(strings.TrimPrefix(line, "<h2>"), "</h2>")
			xml.WriteString(fmt.Sprintf(`<w:p><w:pPr><w:pStyle w:val="Heading2"/></w:pPr><w:r><w:t>%s</w:t></w:r></w:p>`, text))
		} else if strings.HasPrefix(line, "<li>") {
			if !inList {
				inList = true
			}
			text := strings.TrimSuffix(strings.TrimPrefix(line, "<li>"), "</li>")
			xml.WriteString(fmt.Sprintf(`<w:p><w:pPr><w:numPr><w:ilvl w:val="0"/><w:numId w:val="1"/></w:numPr></w:pPr><w:r><w:t>• %s</w:t></w:r></w:p>`, text))
		} else {
			xml.WriteString(fmt.Sprintf(`<w:p><w:r><w:t>%s</w:t></w:r></w:p>`, line))
		}
	}

	return xml.String()
}

func (t *WriteFileTool) markdownToSimpleHTML(md string) string {
	lines := strings.Split(md, "\n")
	var html strings.Builder

	for _, line := range lines {
		line = strings.TrimRight(line, " \t")

		if strings.HasPrefix(line, "# ") {
			html.WriteString(fmt.Sprintf("<h1>%s</h1>\n", strings.TrimPrefix(line, "# ")))
		} else if strings.HasPrefix(line, "## ") {
			html.WriteString(fmt.Sprintf("<h2>%s</h2>\n", strings.TrimPrefix(line, "## ")))
		} else if strings.HasPrefix(line, "### ") {
			html.WriteString(fmt.Sprintf("<h3>%s</h3>\n", strings.TrimPrefix(line, "### ")))
		} else if strings.HasPrefix(line, "- ") || strings.HasPrefix(line, "* ") {
			html.WriteString(fmt.Sprintf("<li>%s</li>\n", strings.TrimPrefix(strings.TrimPrefix(line, "- "), "* ")))
		} else if line == "" {
			html.WriteString("<br>\n")
		} else {
			// 检查是否包含粗体
			line = strings.ReplaceAll(line, "**", "")
			html.WriteString(fmt.Sprintf("<p>%s</p>\n", line))
		}
	}

	return html.String()
}

// 写入Excel文件（XLSX）
func (t *WriteFileTool) writeXLSXFile(ctx context.Context, path, content string, style map[string]interface{}) (int, error) {
	hasHeader := true
	if h, ok := style["has_header"].(bool); ok {
		hasHeader = h
	}
	reader := csv.NewReader(strings.NewReader(content))
	reader.FieldsPerRecord = -1
	reader.LazyQuotes = true
	reader.TrimLeadingSpace = true
	rows, err := reader.ReadAll()
	if err != nil {
		return 0, fmt.Errorf("parse xlsx rows: %w", err)
	}
	filtered := rows[:0]
	for _, row := range rows {
		nonEmpty := false
		for _, value := range row {
			if strings.TrimSpace(value) != "" {
				nonEmpty = true
				break
			}
		}
		if nonEmpty {
			filtered = append(filtered, row)
		}
	}
	return spreadsheet.WriteXLSXWorkbook(ctx, path, filtered, hasHeader)
}

// 写入PDF文件
func (t *WriteFileTool) writePDFFile(ctx context.Context, path, content string, style map[string]interface{}) (int, error) {
	// 实际项目应该使用 gofpdf 或类似的库
	// 这里创建一个HTML然后返回说明（需要额外安装库）

	infoMsg := fmt.Sprintf(`PDF generation requires additional libraries.
Content to be saved:
%s

Please install a PDF library like github.com/jung-kurt/gofpdf for actual PDF generation.`, content)

	return t.writeTextFile(ctx, path+".txt", infoMsg, "utf-8", false)
}

// 写入图片文件
func (t *WriteFileTool) writeImageFile(path, content string, format FileFormat, style map[string]interface{}) (int, error) {
	// 实际项目应该使用 image 包生成图片
	// 这里假设content是base64编码的图片数据或简单的文本

	// 检查是否是base64编码
	var imageData []byte

	if strings.HasPrefix(content, "data:image") {
		// 处理data URL
		parts := strings.SplitN(content, ",", 2)
		if len(parts) == 2 {
			var err error
			imageData, err = base64.StdEncoding.DecodeString(parts[1])
			if err != nil {
				return 0, fmt.Errorf("解码base64图片失败: %v", err)
			}
		}
	} else if t.isBase64(content) {
		// 纯base64
		var err error
		imageData, err = base64.StdEncoding.DecodeString(content)
		if err != nil {
			return 0, fmt.Errorf("解码base64失败: %v", err)
		}
	} else {
		// 作为文本创建简单的图片
		return 0, fmt.Errorf("图片格式需要base64编码的数据")
	}

	// 应用图片样式（这里简化处理）
	// 实际应该调整图片大小、质量等

	return len(imageData), os.WriteFile(path, imageData, 0644)
}

// 写入音频文件
func (t *WriteFileTool) writeAudioFile(path, content string, format FileFormat, style map[string]interface{}) (int, error) {
	// 实际项目应该使用音频库
	// 这里假设content是base64编码的音频数据

	var audioData []byte
	var err error

	if t.isBase64(content) {
		audioData, err = base64.StdEncoding.DecodeString(content)
		if err != nil {
			return 0, fmt.Errorf("解码base64音频失败: %v", err)
		}
	} else {
		return 0, fmt.Errorf("音频格式需要base64编码的数据")
	}

	return len(audioData), os.WriteFile(path, audioData, 0644)
}

// 辅助函数：检查是否是base64
func (t *WriteFileTool) isBase64(s string) bool {
	// 简单检查：长度是4的倍数，且只包含base64字符
	if len(s)%4 != 0 {
		return false
	}
	for _, r := range s {
		if !(r >= 'A' && r <= 'Z') && !(r >= 'a' && r <= 'z') &&
			!(r >= '0' && r <= '9') && r != '+' && r != '/' && r != '=' {
			return false
		}
	}
	return true
}

// UTF-8到GBK转换
func (t *WriteFileTool) utf8ToGBK(data []byte) ([]byte, error) {
	encoder := simplifiedchinese.GBK.NewEncoder()
	return io.ReadAll(transform.NewReader(bytes.NewReader(data), encoder))
}

// UTF-8到Big5转换
func (t *WriteFileTool) utf8ToBig5(data []byte) ([]byte, error) {
	encoder := traditionalchinese.Big5.NewEncoder()
	return io.ReadAll(transform.NewReader(bytes.NewReader(data), encoder))
}

func (t *ReadFileTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	path, _ := params["path"].(string)
	if path == "" {
		return "", errors.New("path 参数必须提供")
	}
	if err := t.deps.EnforcePath(ctx, t.workspace, path); err != nil {
		return "", err
	}
	// 安全：路径检查，避免越出 workspace
	fullPath, err := t.deps.SafeJoin(t.workspace, path)
	if err != nil {
		// 跨盘绝对路径：与 list_dir 行为一致，允许读取
		if filepath.IsAbs(path) {
			if _, statErr := os.Stat(path); statErr == nil {
				fullPath = path
			} else {
				return "", err
			}
		} else {
			return "", err
		}
	}
	if SensitiveReadPath(fullPath) {
		return "", fmt.Errorf("read_file denied sensitive credential path: %s", path)
	}

	if _, err := os.Stat(fullPath); err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("file does not exist: %s%s", path, suggestReadFilePath(t.workspace, path))
		}
		return "", err
	}

	// 1. 检测格式（复用 WriteFileTool 的 detectFormat）
	format := detectFormat(fullPath, "") // 自己实现一个或复用

	// 2. 根据格式调用不同读取器
	var content string
	switch format {
	case FormatDOCX:
		content, err = ReadDOCXText(fullPath)
	case FormatXLSX:
		rowsLimit := 0 // 从 params 提取 "max_rows"
		if limit, ok := params["max_rows"].(float64); ok {
			rowsLimit = int(limit)
		}
		content, err = ReadXLSX(fullPath, rowsLimit)
	case FormatPDF, FormatPPTX:
		return "", fmt.Errorf("read_file cannot safely read binary %s files as text: %s. Use extract/convert or a document-specific tool instead", format, path)
	case FormatCSV:
		linesRange, _ := params["lines_range"].(string)
		content, err = t.readCSV(fullPath, linesRange)
	default:
		// 图片文件：提示使用 vision 工具
		if isImageFile(fullPath) {
			return fmt.Sprintf("[Image: %s] Use the vision tool to analyze this image: vision(path=\"%s\")", fullPath, path), nil
		}
		// 文本文件
		data, e := os.ReadFile(fullPath)
		if e != nil {
			return "", e
		}
		encodingName, _ := params["encoding"].(string)
		content, err = decodeTextData(data, encodingName)
		if err != nil {
			return "", err
		}
		linesRange, _ := params["lines_range"].(string)
		if linesRange != "" {
			content, err = SelectTextLineRange(content, linesRange)
			if err != nil {
				return "", err
			}
		}
	}

	if err != nil {
		return "", fmt.Errorf("读取 %s 文件失败: %v", format, err)
	}
	GetFileStateRegistry().RecordRead(fullPath, ReaderIDFromContext(ctx))
	return content, nil
}

var (
	sensitivePathCamelBoundary = regexp.MustCompile(`([a-z0-9])([A-Z])`)
	sensitivePathTokenSplit    = regexp.MustCompile(`[^a-zA-Z0-9]+`)
)

func SensitiveReadPath(pathValue string) bool {
	rawBase := strings.TrimSpace(filepath.Base(filepath.Clean(pathValue)))
	base := strings.ToLower(rawBase)
	if base == "" {
		return false
	}
	switch base {
	case ".env", ".env.local", ".env.production", ".env.development", ".env.test",
		"chatroom_key.txt", "id_rsa", "id_ed25519":
		return true
	}
	switch strings.ToLower(filepath.Ext(base)) {
	case ".key", ".pem", ".p12", ".pfx":
		return true
	}
	// Credential markers must be complete filename tokens, not arbitrary
	// substrings. For example, "secretary-notes.md" is ordinary user content
	// and must not be rejected merely because "secretary" starts with
	// "secret". Normalize camelCase and separators so real names such as
	// clientSecret.json and access-token.txt remain protected.
	normalized := sensitivePathCamelBoundary.ReplaceAllString(rawBase, `${1}_${2}`)
	tokens := sensitivePathTokenSplit.Split(strings.ToLower(normalized), -1)
	filtered := tokens[:0]
	for _, token := range tokens {
		if token != "" {
			filtered = append(filtered, token)
		}
	}
	for i, token := range filtered {
		switch token {
		case "credential", "credentials", "secret", "secrets":
			return true
		}
		if i+1 < len(filtered) {
			pair := token + "_" + filtered[i+1]
			switch pair {
			case "access_token", "refresh_token", "private_key":
				return true
			}
		}
	}
	return false
}

func decodeTextData(data []byte, encodingName string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(encodingName)) {
	case "", "utf-8", "utf8":
		return string(data), nil
	case "gbk", "gb2312":
		decoded, err := io.ReadAll(transform.NewReader(bytes.NewReader(data), simplifiedchinese.GBK.NewDecoder()))
		if err != nil {
			return "", fmt.Errorf("decode %s: %w", encodingName, err)
		}
		return string(decoded), nil
	case "big5":
		decoded, err := io.ReadAll(transform.NewReader(bytes.NewReader(data), traditionalchinese.Big5.NewDecoder()))
		if err != nil {
			return "", fmt.Errorf("decode big5: %w", err)
		}
		return string(decoded), nil
	default:
		return "", fmt.Errorf("unsupported encoding %q; supported: utf-8, gbk, gb2312, big5", encodingName)
	}
}

func SelectTextLineRange(content, rangeSpec string) (string, error) {
	lines := strings.Split(content, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	total := len(lines)
	if total == 0 {
		return "", nil
	}

	start, end, err := parseTextLineRange(rangeSpec, total)
	if err != nil {
		return "", err
	}
	selected := strings.Join(lines[start-1:end], "\n")
	return fmt.Sprintf("[Lines %d-%d of %d]\n%s", start, end, total, selected), nil
}

func parseTextLineRange(rangeSpec string, total int) (int, int, error) {
	rangeSpec = strings.TrimSpace(rangeSpec)
	if rangeSpec == "" {
		return 1, total, nil
	}
	if strings.HasPrefix(rangeSpec, "head:") {
		limit := parseInt(strings.TrimPrefix(rangeSpec, "head:"))
		if limit <= 0 {
			return 0, 0, fmt.Errorf("invalid lines_range %q; head limit must be positive", rangeSpec)
		}
		if limit > total {
			limit = total
		}
		return 1, limit, nil
	}
	if strings.HasPrefix(rangeSpec, "tail:") {
		limit := parseInt(strings.TrimPrefix(rangeSpec, "tail:"))
		if limit <= 0 {
			return 0, 0, fmt.Errorf("invalid lines_range %q; tail limit must be positive", rangeSpec)
		}
		if limit > total {
			limit = total
		}
		return total - limit + 1, total, nil
	}
	if strings.Contains(rangeSpec, "-") {
		parts := strings.SplitN(rangeSpec, "-", 2)
		start := parseInt(parts[0])
		end := parseInt(parts[1])
		if start <= 0 || end <= 0 || start > end {
			return 0, 0, fmt.Errorf("invalid lines_range %q; use head:N, tail:N, or M-N", rangeSpec)
		}
		if start > total {
			return 0, 0, fmt.Errorf("lines_range %q starts after end of file (%d lines)", rangeSpec, total)
		}
		if end > total {
			end = total
		}
		return start, end, nil
	}
	return 0, 0, fmt.Errorf("unsupported lines_range %q; use head:N, tail:N, or M-N", rangeSpec)
}

func suggestReadFilePath(workspace, requested string) string {
	if workspace == "" {
		return ""
	}
	best, ok := findSimilarWorkspaceFile(workspace, requested, 3000)
	if !ok {
		return ""
	}
	return fmt.Sprintf(". Did you mean %q?", best)
}

func findSimilarWorkspaceFile(workspace, requested string, maxFiles int) (string, bool) {
	targetName := strings.ToLower(filepath.Base(requested))
	if targetName == "." || targetName == "" {
		return "", false
	}

	bestScore := 1 << 30
	best := ""
	seen := 0
	_ = filepath.WalkDir(workspace, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if path == workspace {
			return nil
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", "vendor", "dist", "build", ".next", ".venv", "venv", "__pycache__", ".openkitty", "logs", "coverage", "target":
				return filepath.SkipDir
			}
			return nil
		}
		seen++
		if seen > maxFiles {
			return filepath.SkipAll
		}

		name := strings.ToLower(d.Name())
		score := boundedEditDistance(targetName, name, 32)
		if strings.Contains(name, targetName) || strings.Contains(targetName, name) {
			score -= 4
		}
		if filepath.Ext(name) == filepath.Ext(targetName) {
			score--
		}
		if score < bestScore {
			if rel, relErr := filepath.Rel(workspace, path); relErr == nil {
				bestScore = score
				best = filepath.ToSlash(rel)
			}
		}
		return nil
	})
	if best == "" {
		return "", false
	}

	threshold := max(2, len(targetName)/2)
	return best, bestScore <= threshold
}

func boundedEditDistance(a, b string, limit int) int {
	ar := []rune(a)
	br := []rune(b)
	if len(ar) == 0 {
		return len(br)
	}
	if len(br) == 0 {
		return len(ar)
	}
	if absInt(len(ar)-len(br)) > limit {
		return limit + 1
	}

	prev := make([]int, len(br)+1)
	curr := make([]int, len(br)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ar); i++ {
		curr[0] = i
		rowMin := curr[0]
		for j := 1; j <= len(br); j++ {
			cost := 0
			if ar[i-1] != br[j-1] {
				cost = 1
			}
			curr[j] = min(min(curr[j-1]+1, prev[j]+1), prev[j-1]+cost)
			if curr[j] < rowMin {
				rowMin = curr[j]
			}
		}
		if rowMin > limit {
			return limit + 1
		}
		prev, curr = curr, prev
	}
	return prev[len(br)]
}

func absInt(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

type ReadFileTool struct {
	workspace string
	deps      FileToolDependencies
}

func isImageFile(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".bmp", ".ico", ".svg":
		return true
	}
	return false
}

func imageMimeType(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".bmp":
		return "image/bmp"
	case ".svg":
		return "image/svg+xml"
	default:
		return "image/png"
	}
}

func base64Encode(data []byte) string {
	return base64.StdEncoding.EncodeToString(data)
}

func NewReadFileTool(workspace string, dependencies ...FileToolDependencies) *ReadFileTool {
	deps := defaultFileToolDependencies()
	if len(dependencies) > 0 {
		deps = mergeFileToolDependencies(deps, dependencies[0])
	}
	return &ReadFileTool{workspace: workspace, deps: deps}
}
func (t *ReadFileTool) Name() string { return "read_file" }
func (t *ReadFileTool) ResultIsContentPayload() bool {
	return true
}

// ScopePath implements PathScopedTool interface for parallel safety
func (t *ReadFileTool) ScopePath(args map[string]interface{}) string {
	if p, ok := args["path"].(string); ok {
		if filepath.IsAbs(p) {
			return filepath.Clean(p)
		}
		return filepath.Join(t.workspace, p)
	}
	return ""
}
func (t *ReadFileTool) MaxResultSize() int { return 0 } // 借鉴hermes: 0=无限, read_file不能被截断

func (t *ReadFileTool) Description() string {
	return "Read a file before editing it or relying on its contents. Paths may be absolute or relative to the workspace. " +
		"Use lines_range for large files and read nearby tests, configs, and README files for code tasks. " +
		"Supports text, CSV, Excel, Word, and image metadata/content previews. Use extract/convert or a document-specific tool for PDF and PowerPoint files."
}

func (t *ReadFileTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"path": map[string]interface{}{
				"type":        "string",
				"description": "要读取的文件路径",
			},
			"lines_range": map[string]interface{}{
				"type":        "string",
				"description": "读取行范围，如 'head:5' 或 'tail:5' 或 '1-10'",
			},
			"max_rows": map[string]interface{}{ // 为 Excel 添加
				"type":        "integer",
				"description": "Excel 最大读取行数",
			},
			"encoding": map[string]interface{}{ // 文本编码
				"type":        "string",
				"description": "文本文件编码，如 utf-8, gbk, big5",
				"enum":        []string{"utf-8", "gbk", "gb2312", "big5"},
			},
		},
		"required": []string{"path"},
	}
}

func (t *ReadFileTool) readCSV(path, linesRange string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()

	reader := csv.NewReader(file)
	// 设置允许字段数量不一致
	reader.FieldsPerRecord = -1    // -1 表示允许不同行有不同字段数
	reader.TrimLeadingSpace = true // 自动去除前导空格

	var records [][]string
	var totalRows int
	var parseErr error

	// 先统计总行数并记录表头
	var header []string
	var sampleRows [][]string
	maxSampleRows := 5 // 最多展示5行示例

	if linesRange == "" {
		// 尝试读取所有行，但遇到错误时返回部分数据
		allRecords := make([][]string, 0)
		rowNum := 0

		for {
			row, err := reader.Read()
			if err == io.EOF {
				break
			}
			if err != nil {
				// 记录错误但继续读取（跳过错误行）
				parseErr = fmt.Errorf("第 %d 行解析失败: %v", rowNum+1, err)
				continue
			}

			allRecords = append(allRecords, row)
			rowNum++

			if rowNum > 1000 {
				allRecords = allRecords[:1000]
				parseErr = fmt.Errorf("文件超过1000行，仅展示前1000行数据")
				break
			}
		}

		totalRows = rowNum
		records = allRecords

		if len(records) == 0 {
			return "（CSV文件为空）", nil
		}

		// 提取表头（第一行）
		header = records[0]

		// 如果数据行数>1，提取前几行作为示例
		if len(records) > 1 {
			sampleEnd := maxSampleRows + 1
			if sampleEnd > len(records) {
				sampleEnd = len(records)
			}
			sampleRows = records[1:sampleEnd]
		}

		// 格式化输出（包含统计信息）
		return t.formatCSVWithStats(records, header, sampleRows, totalRows, parseErr), nil
	} else {
		// 部分读取
		records, err = t.readPartialCSV(reader, linesRange)
		if err != nil {
			return "", err
		}
		return formatCSV(records), nil
	}
}

func (t *ReadFileTool) formatCSVWithStats(records [][]string, header []string, sampleRows [][]string, totalRows int, parseErr error) string {
	var result strings.Builder

	// 1. 文件基本信息
	result.WriteString("📊 CSV 文件分析结果\n")
	result.WriteString(strings.Repeat("=", 60) + "\n\n")

	// 2. 统计信息
	result.WriteString(fmt.Sprintf("📈 统计信息:\n"))
	result.WriteString(fmt.Sprintf("   • 总行数: %d 行\n", totalRows))
	result.WriteString(fmt.Sprintf("   • 数据行数: %d 行\n", totalRows-1))
	result.WriteString(fmt.Sprintf("   • 列数: %d 列\n", len(header)))

	// 3. 列信息（表头）
	result.WriteString(fmt.Sprintf("\n📋 列结构 (共 %d 列):\n", len(header)))
	for i, col := range header {
		// 统计该列的非空数据
		nonEmptyCount := 0
		for _, row := range records[1:] {
			if i < len(row) && strings.TrimSpace(row[i]) != "" {
				nonEmptyCount++
			}
		}
		result.WriteString(fmt.Sprintf("   %d. %s (非空率: %.1f%%)\n",
			i+1,
			col,
			float64(nonEmptyCount)/float64(totalRows-1)*100))
	}

	// 4. 数据预览（前几行）
	if len(sampleRows) > 0 {
		result.WriteString(fmt.Sprintf("\n🔍 数据预览 (前 %d 行):\n", len(sampleRows)))
		result.WriteString(strings.Repeat("-", 60) + "\n")

		// 计算每列的最大宽度用于对齐
		colWidths := make([]int, len(header))
		for i, col := range header {
			colWidths[i] = len(col)
		}
		for _, row := range sampleRows {
			for i, col := range row {
				if i < len(colWidths) && len(col) > colWidths[i] {
					colWidths[i] = len(col)
				}
			}
		}

		// 打印表头
		for i, col := range header {
			result.WriteString(fmt.Sprintf("| %-*s ", colWidths[i], col))
		}
		result.WriteString("|\n")

		// 打印分隔线
		for _, width := range colWidths {
			result.WriteString("+-" + strings.Repeat("-", width) + "-")
		}
		result.WriteString("+\n")

		// 打印数据行
		for _, row := range sampleRows {
			for i := 0; i < len(header); i++ {
				cell := ""
				if i < len(row) {
					cell = row[i]
				}
				result.WriteString(fmt.Sprintf("| %-*s ", colWidths[i], cell))
			}
			result.WriteString("|\n")
		}
	}

	// 5. 使用建议（如果文件很大或有问题）
	if totalRows > 100 {
		result.WriteString(fmt.Sprintf("\n💡 Tip: Large file (%d rows), use lines_range parameter to view specific ranges:\n", totalRows))
		result.WriteString("• View first 10 rows: lines_range='head:10'\n")
		result.WriteString("• View last 10 rows: lines_range='tail:10'\n")
		result.WriteString("• View specific range: lines_range='100-200'\n")
		result.WriteString(fmt.Sprintf("• Load all %d rows: lines_range='1-%d'\n", totalRows, totalRows))
	}

	if parseErr != nil {
		result.WriteString(fmt.Sprintf("\n⚠️  Warning: %v\n", parseErr))
		result.WriteString("Suggestions to fix CSV format:\n")
		result.WriteString("• Fields containing commas should be wrapped in quotes\n")
		result.WriteString("• Ensure consistent number of fields across rows\n")
		result.WriteString("• Escape special characters properly\n")
		result.WriteString("• Use CSV validator tools to check file structure\n")
	}

	return result.String()
}

func formatCSV(records [][]string) string {
	if len(records) == 0 {
		return "（空文件）"
	}

	var result strings.Builder

	// 计算每列的最大宽度（用于对齐）
	colWidths := make([]int, len(records[0]))
	for _, row := range records {
		for i, col := range row {
			if len(col) > colWidths[i] {
				colWidths[i] = len(col)
			}
		}
	}

	// 添加分隔线
	separator := strings.Builder{}
	for _, width := range colWidths {
		separator.WriteString(strings.Repeat("-", width+2) + "+")
	}

	// 输出数据
	for i, row := range records {
		if i == 0 {
			// 表头
			result.WriteString("📊 ")
		}

		for j, col := range row {
			// 左对齐，用空格填充到指定宽度
			padding := colWidths[j] - len(col)
			result.WriteString(" " + col + strings.Repeat(" ", padding) + " |")
		}
		result.WriteString("\n")

		if i == 0 {
			// 表头后加分隔线
			result.WriteString(separator.String() + "\n")
		}
	}

	return result.String()
}

func (t *ReadFileTool) readPartialCSV(r *csv.Reader, rangeSpec string) ([][]string, error) {
	// 设置宽松的解析模式
	r.FieldsPerRecord = -1
	r.TrimLeadingSpace = true

	// 情况1: head:5 - 读取前5行
	if strings.HasPrefix(rangeSpec, "head:") {
		limitStr := strings.TrimPrefix(rangeSpec, "head:")
		limit := parseInt(limitStr)
		if limit <= 0 {
			limit = 10
		}

		records := make([][]string, 0, limit)
		var parseErrors []string

		for i := 0; i < limit; i++ {
			row, err := r.Read()
			if err == io.EOF {
				break
			}
			if err != nil {
				parseErrors = append(parseErrors, fmt.Sprintf("第 %d 行: %v", i+1, err))
				continue // 跳过错误行，继续读取
			}
			records = append(records, row)
		}

		if len(parseErrors) > 0 {
			return records, fmt.Errorf("部分行解析失败: %s", strings.Join(parseErrors, "; "))
		}
		return records, nil
	}

	// 情况2: tail:5 - 读取最后5行
	if strings.HasPrefix(rangeSpec, "tail:") {
		limitStr := strings.TrimPrefix(rangeSpec, "tail:")
		limit := parseInt(limitStr)
		if limit <= 0 {
			limit = 10
		}

		// 先读取所有行
		all, err := r.ReadAll()
		if err != nil {
			return nil, fmt.Errorf("读取 CSV 失败: %v", err)
		}

		if len(all) <= limit {
			return all, nil
		}
		return all[len(all)-limit:], nil
	}

	// 情况3: start-end 格式
	if strings.Contains(rangeSpec, "-") {
		parts := strings.Split(rangeSpec, "-")
		start := 0
		end := -1

		if len(parts) >= 1 && parts[0] != "" {
			start = parseInt(parts[0])
			if start > 0 {
				start-- // 转换为0索引
			}
		}

		if len(parts) >= 2 && parts[1] != "" {
			end = parseInt(parts[1])
			if end > 0 {
				end-- // 转换为0索引
			}
		}

		records := make([][]string, 0)
		rowIndex := 0
		var parseErrors []string

		for {
			row, err := r.Read()
			if err == io.EOF {
				break
			}
			if err != nil {
				parseErrors = append(parseErrors, fmt.Sprintf("第 %d 行: %v", rowIndex+1, err))
				rowIndex++
				continue
			}

			if rowIndex >= start && (end == -1 || rowIndex <= end) {
				records = append(records, row)
			}

			rowIndex++
			if end != -1 && rowIndex > end {
				break
			}
		}

		if len(parseErrors) > 0 {
			return records, fmt.Errorf("部分行解析失败: %s", strings.Join(parseErrors, "; "))
		}
		return records, nil
	}

	return nil, fmt.Errorf("不支持的行范围格式: %s，支持格式: head:N, tail:N, M-N", rangeSpec)
}

func parseInt(s string) int {
	// 去除空格
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}

	// 处理负数
	negative := false
	if s[0] == '-' {
		negative = true
		s = s[1:]
	}

	var result int
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0 // 非数字字符
		}
		result = result*10 + int(s[i]-'0')
	}

	if negative {
		result = -result
	}
	return result
}

func ReadDOCXText(path string) (string, error) {
	r, err := zip.OpenReader(path)
	if err != nil {
		return "", err
	}
	defer r.Close()

	var content strings.Builder
	for _, f := range r.File {
		if f.Name == "word/document.xml" {
			rc, err := f.Open()
			if err != nil {
				return "", err
			}
			defer rc.Close()

			// 用 xml 包解析，提取 <w:t> 标签内容
			decoder := xml.NewDecoder(rc)
			for {
				t, err := decoder.Token()
				if err == io.EOF {
					break
				}
				if err != nil {
					return "", err
				}
				if elem, ok := t.(xml.StartElement); ok && elem.Name.Local == "t" {
					var txt string
					if err := decoder.DecodeElement(&txt, &elem); err == nil {
						content.WriteString(txt)
					}
				}
			}
			break
		}
	}
	return content.String(), nil
}

func ReadXLSX(path string, rowsLimit int) (string, error) {
	f, err := excelize.OpenFile(path)
	if err != nil {
		return "", fmt.Errorf("打开 Excel 文件失败: %v", err)
	}
	defer f.Close()

	// 获取第一个工作表名称
	sheetName := f.GetSheetName(0)
	if sheetName == "" {
		return "", fmt.Errorf("Excel 文件中没有工作表")
	}

	// 获取所有行
	rows, err := f.Rows(sheetName)
	if err != nil {
		return "", fmt.Errorf("读取行数据失败: %v", err)
	}
	defer rows.Close()

	var out strings.Builder
	rowCount := 0

	for rows.Next() {
		if rowsLimit > 0 && rowCount >= rowsLimit {
			out.WriteString(fmt.Sprintf("\n... (共超过 %d 行，仅展示前 %d 行)", rowsLimit, rowsLimit))
			break
		}

		cols, err := rows.Columns()
		if err != nil {
			continue // 跳过有问题的行
		}

		// 每行用制表符分隔
		out.WriteString(strings.Join(cols, "\t"))
		out.WriteString("\n")
		rowCount++
	}

	// 检查是否有错误（注意：excelize 的 Rows 没有 Err()，需要通过其他方式）
	if err := rows.Error(); err != nil {
		return out.String(), fmt.Errorf("读取行时出错: %v", err)
	}

	if rowCount == 0 {
		return "（空工作表）", nil
	}

	return out.String(), nil
}
