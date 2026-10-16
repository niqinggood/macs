package content

import (
	"archive/zip"
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"mac/internal/portablepath"
	"mac/internal/textutil"

	"github.com/ZeroHawkeye/wordZero/pkg/document"
	"github.com/razvandimescu/gopdf/pdf"
	"github.com/xuri/excelize/v2"
)

func safeJoin(base, path string) (string, error) {
	path = strings.TrimSpace(path)
	if portablepath.IsAbs(path) {
		if filepath.IsAbs(path) {
			return filepath.Abs(filepath.Clean(path))
		}
		return path, nil
	}
	baseAbsolute, err := filepath.Abs(base)
	if err != nil {
		return "", err
	}
	target := filepath.Clean(filepath.Join(baseAbsolute, filepath.Clean(path)))
	relative, err := filepath.Rel(baseAbsolute, target)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path %q escapes the workspace", path)
	}
	return target, nil
}

// DocAnalyzer analyzes source code documents and extracts structural information
type DocAnalyzer struct {
	workspace string
	logger    *log.Logger
	mu        sync.RWMutex
	cache     map[string]*DocumentAnalysis
}

// DocumentAnalysis represents complete analysis result of a document
type DocumentAnalysis struct {
	Path          string           `json:"path"`
	Language      string           `json:"language"`
	Size          int64            `json:"size"`
	Summary       *DocSummary      `json:"summary"`
	Outline       *DocOutline      `json:"outline"`
	Functions     []*FunctionInfo  `json:"functions"`
	Structs       []*StructInfo    `json:"structs"`
	Interfaces    []*InterfaceInfo `json:"interfaces"`
	Variables     []*VariableInfo  `json:"variables"`
	Constants     []*ConstantInfo  `json:"constants"`
	Imports       []*ImportInfo    `json:"imports"`
	CodeBlocks    []*CodeBlock     `json:"code_blocks"`
	Tables        []*TableInfo     `json:"tables,omitempty"`
	Sheets        []*SheetInfo     `json:"sheets,omitempty"`
	Images        []*ImageInfo     `json:"images,omitempty"`
	LastAnalyzed  time.Time        `json:"last_analyzed"`
	TokenEstimate int              `json:"token_estimate"`
	Modified      bool             `json:"modified"`
	Sections      []*Section       `json:"sections,omitempty"` // 用于Markdown/Text
}

type TableInfo struct {
	Name    string   `json:"name"`
	Columns []string `json:"columns"`
	Rows    int      `json:"rows"`
}

type SheetInfo struct {
	Name string `json:"name"`
	Rows int    `json:"rows"`
	Cols int    `json:"cols"`
}

type ImageInfo struct {
	Name   string `json:"name"`
	Type   string `json:"type"`
	Width  int    `json:"width,omitempty"`
	Height int    `json:"height,omitempty"`
}

type DocSummary struct {
	Title          string     `json:"title"`
	PackageName    string     `json:"package_name"`
	Description    string     `json:"description"`
	MainPurpose    string     `json:"main_purpose"`
	KeyFeatures    []string   `json:"key_features"`
	Dependencies   []string   `json:"dependencies"`
	Complexity     string     `json:"complexity"`
	TotalLines     int        `json:"total_lines"`
	EffectiveLines int        `json:"effective_lines"`
	Sections       []*Section `json:"sections,omitempty"` // 用于Markdown/Text
}

type DocOutline struct {
	MainFunction    *OutlineItem   `json:"main_function,omitempty"`
	GlobalFunctions []*OutlineItem `json:"global_functions,omitempty"`
	Structs         []*OutlineItem `json:"structs,omitempty"`
	Interfaces      []*OutlineItem `json:"interfaces,omitempty"`
	PackageName     string         `json:"package_name,omitempty"`
	TotalBlocks     int            `json:"total_blocks"`
	LargestBlock    string         `json:"largest_block,omitempty"` // 最大的代码块名称\
	Sheets          []*SheetInfo   `json:"sheets,omitempty"`
	Sections        []*Section     `json:"sections,omitempty"` // 用于Markdown/Text

	Functions []*FunctionInfo `json:"functions"`
	Images    []*ImageInfo    `json:"images,omitempty"`
	Tables    []*TableInfo    `json:"tables,omitempty"` // 添加这个字段
	Variables []*VariableInfo `json:"variables"`
	Constants []*ConstantInfo `json:"constants"`
}

type OutlineItem struct {
	Name      string `json:"name"`
	Type      string `json:"type"`
	LineStart int    `json:"line_start"`
	LineEnd   int    `json:"line_end"`
	Scope     string `json:"scope,omitempty"` // 对于方法，记录所属结构
	Signature string `json:"signature,omitempty"`
	Summary   string `json:"summary,omitempty"`
}

type Section struct {
	Title       string     `json:"title"`
	Level       int        `json:"level"`
	LineStart   int        `json:"line_start"`
	LineEnd     int        `json:"line_end"`
	SubSections []*Section `json:"sub_sections,omitempty"`
}

type FunctionInfo struct {
	Name       string                 `json:"name"`
	Receiver   string                 `json:"receiver,omitempty"` // 如果是方法，记录所属类型
	Scope      string                 `json:"scope"`              // "global", "struct", "package"
	Parameters []*ParamInfo           `json:"parameters"`
	Returns    []*ReturnInfo          `json:"returns"`
	LineStart  int                    `json:"line_start"`
	LineEnd    int                    `json:"line_end"`
	Body       string                 `json:"body,omitempty"`
	Doc        string                 `json:"doc,omitempty"`
	Calls      []string               `json:"calls,omitempty"` // 调用的函数列表
	IsExported bool                   `json:"is_exported"`     // 是否公开（大写开头）
	IsMain     bool                   `json:"is_main"`         // 是否是 main 函数
	Metadata   map[string]interface{} `json:"metadata,omitempty"`
}

type ParamInfo struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

type ReturnInfo struct {
	Name string `json:"name,omitempty"`
	Type string `json:"type"`
}

type StructInfo struct {
	Name       string                 `json:"name"`
	Fields     []*FieldInfo           `json:"fields"`
	Methods    []string               `json:"methods"` // 只是方法名列表
	LineStart  int                    `json:"line_start"`
	LineEnd    int                    `json:"line_end"`
	Doc        string                 `json:"doc,omitempty"`
	IsExported bool                   `json:"is_exported"`
	Metadata   map[string]interface{} `json:"metadata,omitempty"`
}

type FieldInfo struct {
	Name string `json:"name"`
	Type string `json:"type"`
	Tag  string `json:"tag,omitempty"`
}

type InterfaceInfo struct {
	Name      string       `json:"name"`
	Methods   []*MethodSig `json:"methods"`
	LineStart int          `json:"line_start"`
	LineEnd   int          `json:"line_end"`
	Doc       string       `json:"doc,omitempty"`
}

type MethodSig struct {
	Name       string        `json:"name"`
	Parameters []*ParamInfo  `json:"parameters"`
	Returns    []*ReturnInfo `json:"returns"`
}

type VariableInfo struct {
	Name      string `json:"name"`
	Type      string `json:"type"`
	Value     string `json:"value,omitempty"`
	LineStart int    `json:"line_start"`
	LineEnd   int    `json:"line_end"`
	IsConst   bool   `json:"is_const"`
}

type ConstantInfo struct {
	Name  string `json:"name"`
	Value string `json:"value"`
	Line  int    `json:"line"`
}

type ImportInfo struct {
	Path  string `json:"path"`
	Alias string `json:"alias,omitempty"`
}

type CodeBlock struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Type      string `json:"type"`
	StartLine int    `json:"start_line"`
	EndLine   int    `json:"end_line"`
	Content   string `json:"content,omitempty"`
}

type BatchAnalysisResult struct {
	TotalFiles    int                 `json:"total_files"`
	AnalyzedFiles int                 `json:"analyzed_files"`
	FailedFiles   int                 `json:"failed_files"`
	TotalTokens   int                 `json:"total_tokens"`
	ByLanguage    map[string]int      `json:"by_language"`
	Files         []*DocumentAnalysis `json:"files"`
	Errors        map[string]string   `json:"errors,omitempty"`
	Summary       string              `json:"summary"`
}

func NewDocAnalyzer(workspace string, logger *log.Logger) *DocAnalyzer {
	return &DocAnalyzer{
		workspace: workspace,
		logger:    logger,
		cache:     make(map[string]*DocumentAnalysis),
	}
}

func (da *DocAnalyzer) resolvePath(path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	resolved := filepath.Join(da.workspace, path)
	// Path traversal protection
	if safePath, err := safeJoin(da.workspace, resolved); err == nil {
		return safePath
	}
	return resolved
}

func (da *DocAnalyzer) AnalyzeDocument(ctx context.Context, filePath string) (*DocumentAnalysis, error) {
	da.mu.RLock()
	if analysis, exists := da.cache[filePath]; exists && !analysis.Modified {
		da.mu.RUnlock()
		return analysis, nil
	}
	da.mu.RUnlock()

	fullPath := da.resolvePath(filePath)

	fileInfo, err := os.Stat(fullPath)
	if err != nil {
		return nil, fmt.Errorf("failed to stat file: %w", err)
	}

	content, err := os.ReadFile(fullPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}
	maxSize := 1024 * 1024    // 1MB
	previewSize := 100 * 1024 // 100KB

	if len(content) > maxSize {
		da.logger.Printf("Warning: file %s is large (%d bytes), analyzing only first %d bytes",
			filePath, len(content), previewSize)
		content = content[:previewSize]
	}

	ext := strings.ToLower(filepath.Ext(fullPath))
	var analysis *DocumentAnalysis

	switch ext {
	case ".go":
		analysis, err = da.analyzeGoFile(fullPath, content)
	case ".py":
		analysis, err = da.analyzePythonFile(fullPath, content)
	case ".js", ".ts", ".jsx", ".tsx":
		analysis, err = da.analyzeJSFile(fullPath, content)
	case ".md", ".markdown":
		analysis, err = da.analyzeMarkdownFile(fullPath, content)
	case ".txt", ".text":
		analysis, err = da.analyzeTextFile(fullPath, content)
	case ".csv":
		analysis, err = da.analyzeCSVFile(fullPath, content)
	case ".xlsx", ".xls":
		analysis, err = da.analyzeExcelFile(fullPath)

	case ".json":
		analysis, err = da.analyzeJSONFile(fullPath, content)
	case ".yaml", ".yml":
		analysis, err = da.analyzeYAMLFile(fullPath, content)

	case ".docx", ".doc":
		analysis, err = da.analyzeWordFile(fullPath)
	case ".ppt", ".pptx":
		analysis, err = da.analyzePPTFile(fullPath)
	case ".pdf":
		analysis, err = da.analyzePDFFile(fullPath)
	default:
		analysis, err = da.analyzeGenericFile(fullPath, content)
	}

	if err != nil {
		return nil, err
	}

	analysis.Path = filePath
	analysis.Size = fileInfo.Size()
	analysis.LastAnalyzed = time.Now()
	analysis.TokenEstimate = da.estimateTokens(analysis)
	analysis.Modified = false

	da.mu.Lock()
	da.cache[filePath] = analysis
	da.mu.Unlock()

	return analysis, nil
}

func (da *DocAnalyzer) InvalidateCache(path string) {
	da.mu.Lock()
	delete(da.cache, path)
	da.mu.Unlock()
}

func (da *DocAnalyzer) UpdateCache(path string, analysis *DocumentAnalysis) {
	da.mu.Lock()
	analysis.Modified = false
	da.cache[path] = analysis
	da.mu.Unlock()
}

func (da *DocAnalyzer) ClearCache(path string) {
	da.mu.Lock()
	delete(da.cache, path)
	da.mu.Unlock()
}

func (da *DocAnalyzer) ClearAllCache() {
	da.mu.Lock()
	da.cache = make(map[string]*DocumentAnalysis)
	da.mu.Unlock()
}

// 构建简化的 Outline
func (da *DocAnalyzer) buildSimplifiedOutline(analysis *DocumentAnalysis, packageName, largestBlock string, maxLines int) *DocOutline {
	outline := &DocOutline{
		PackageName:     packageName,
		GlobalFunctions: make([]*OutlineItem, 0),
		Structs:         make([]*OutlineItem, 0),
		Interfaces:      make([]*OutlineItem, 0),
		TotalBlocks:     len(analysis.Functions) + len(analysis.Structs),
		LargestBlock:    largestBlock,
	}

	// 找出 main 函数
	for _, fn := range analysis.Functions {
		item := &OutlineItem{
			Name:      fn.Name,
			Type:      "function",
			LineStart: fn.LineStart,
			LineEnd:   fn.LineEnd,
			Scope:     fn.Scope,
		}

		if fn.IsMain {
			outline.MainFunction = item
		} else if fn.Scope == "global" {
			outline.GlobalFunctions = append(outline.GlobalFunctions, item)
		} else if fn.Scope == "method" {
			// 方法不放在顶层 outline，只记录在 struct 中
		}
	}

	// 添加结构体（不包含方法详情）
	for _, st := range analysis.Structs {
		outline.Structs = append(outline.Structs, &OutlineItem{
			Name:      st.Name,
			Type:      "struct",
			LineStart: st.LineStart,
			LineEnd:   st.LineEnd,
		})
	}

	// 添加接口
	for _, iface := range analysis.Interfaces {
		outline.Interfaces = append(outline.Interfaces, &OutlineItem{
			Name:      iface.Name,
			Type:      "interface",
			LineStart: iface.LineStart,
			LineEnd:   iface.LineEnd,
		})
	}

	return outline
}

func (da *DocAnalyzer) analyzeYAMLFile(filePath string, content []byte) (*DocumentAnalysis, error) {
	analysis := &DocumentAnalysis{
		Language: "yaml",
	}

	analysis.Summary = &DocSummary{
		Title: getDocTitle(analysis.Sections, filePath),
	}

	return analysis, nil
}

func (da *DocAnalyzer) buildOutline(analysis *DocumentAnalysis) *DocOutline {
	outline := &DocOutline{
		PackageName:     analysis.Summary.PackageName,
		GlobalFunctions: make([]*OutlineItem, 0),
		Structs:         make([]*OutlineItem, 0),
		Interfaces:      make([]*OutlineItem, 0),
		Variables:       make([]*VariableInfo, 0),
		Constants:       make([]*ConstantInfo, 0),
		Sheets:          make([]*SheetInfo, 0),
		Sections:        make([]*Section, 0),
		Images:          make([]*ImageInfo, 0),
		// 不初始化 Functions 和 Variables/Constants 的指针切片，保持 nil
	}

	// 统计总块数和找最大块
	totalBlocks := 0
	maxLines := 0
	largestBlock := ""

	// 处理函数：区分 main 函数、全局函数和方法
	for _, fn := range analysis.Functions {
		totalBlocks++

		// 计算代码行数
		lines := fn.LineEnd - fn.LineStart + 1
		if lines > maxLines {
			maxLines = lines
			largestBlock = fn.Name
		}

		// 判断是否是 main 函数
		if fn.IsMain {
			outline.MainFunction = &OutlineItem{
				Name:      fn.Name,
				Type:      "function",
				LineStart: fn.LineStart,
				LineEnd:   fn.LineEnd,
				Scope:     fn.Scope,
			}
		} else if fn.Receiver == "" && fn.Scope != "method" {
			// 没有 receiver 且不是方法的是全局函数
			outline.GlobalFunctions = append(outline.GlobalFunctions, &OutlineItem{
				Name:      fn.Name,
				Type:      "function",
				LineStart: fn.LineStart,
				LineEnd:   fn.LineEnd,
				Scope:     fn.Scope,
			})
		}
		// 方法不放在顶层 outline
	}

	// 处理结构体
	for _, st := range analysis.Structs {
		totalBlocks++
		outline.Structs = append(outline.Structs, &OutlineItem{
			Name:      st.Name,
			Type:      "struct",
			LineStart: st.LineStart,
			LineEnd:   st.LineEnd,
		})
	}

	// 处理接口
	for _, iface := range analysis.Interfaces {
		totalBlocks++
		outline.Interfaces = append(outline.Interfaces, &OutlineItem{
			Name:      iface.Name,
			Type:      "interface",
			LineStart: iface.LineStart,
			LineEnd:   iface.LineEnd,
		})
	}

	// 处理变量
	for _, v := range analysis.Variables {
		outline.Variables = append(outline.Variables, v)
	}

	// 处理常量
	for _, c := range analysis.Constants {
		outline.Constants = append(outline.Constants, c)
	}

	// 处理 Excel sheets
	if len(analysis.Sheets) > 0 {
		outline.Sheets = analysis.Sheets
	}

	// 处理图片
	if len(analysis.Images) > 0 {
		outline.Images = analysis.Images
	}

	// 处理 Markdown/Text 的 sections
	if analysis.Summary != nil && len(analysis.Summary.Sections) > 0 {
		outline.Sections = analysis.Summary.Sections
	}

	outline.TotalBlocks = totalBlocks
	outline.LargestBlock = largestBlock

	return outline
}

func (da *DocAnalyzer) buildSummary(analysis *DocumentAnalysis, content []byte) {
	if analysis.Summary == nil {
		analysis.Summary = &DocSummary{}
	}

	lines := strings.Split(string(content), "\n")
	analysis.Summary.TotalLines = len(lines)
	analysis.Summary.EffectiveLines = da.countEffectiveLines(lines)

	totalFunctions := len(analysis.Functions)
	totalStructs := len(analysis.Structs)

	if len(analysis.Functions) > 0 {
		firstFunc := analysis.Functions[0]
		analysis.Summary.MainPurpose = fmt.Sprintf("Main function: %s", firstFunc.Name)
	}

	features := make([]string, 0)
	if totalFunctions > 0 {
		features = append(features, fmt.Sprintf("%d functions", totalFunctions))
	}
	if totalStructs > 0 {
		features = append(features, fmt.Sprintf("%d structs", totalStructs))
	}
	if len(analysis.Interfaces) > 0 {
		features = append(features, fmt.Sprintf("%d interfaces", len(analysis.Interfaces)))
	}
	analysis.Summary.KeyFeatures = features

	for _, imp := range analysis.Imports {
		analysis.Summary.Dependencies = append(analysis.Summary.Dependencies, imp.Path)
	}
}

func (da *DocAnalyzer) analyzeMarkdownFile(filePath string, content []byte) (*DocumentAnalysis, error) {
	analysis := &DocumentAnalysis{
		Language:   "markdown",
		CodeBlocks: make([]*CodeBlock, 0),
	}

	lines := strings.Split(string(content), "\n")
	sections := make([]*Section, 0)
	stack := make([]*Section, 0)
	headingRegex := regexp.MustCompile(`^(#{1,6})\s+(.*)$`)
	codeBlockRegex := regexp.MustCompile("^```(\\w*)$")

	inCodeBlock := false
	codeBlockStart := 0
	codeBlockLang := ""

	for i, line := range lines {
		if match := headingRegex.FindStringSubmatch(line); match != nil && !inCodeBlock {
			level := len(match[1])
			title := match[2]
			section := &Section{
				Title:     title,
				Level:     level,
				LineStart: i + 1,
			}

			for len(stack) > 0 && stack[len(stack)-1].Level >= level {
				stack = stack[:len(stack)-1]
			}
			if len(stack) > 0 {
				stack[len(stack)-1].SubSections = append(stack[len(stack)-1].SubSections, section)
			} else {
				sections = append(sections, section)
			}
			stack = append(stack, section)
		}

		if match := codeBlockRegex.FindStringSubmatch(line); match != nil {
			if !inCodeBlock {
				inCodeBlock = true
				codeBlockStart = i + 1
				codeBlockLang = match[1]
			} else {
				inCodeBlock = false
				analysis.CodeBlocks = append(analysis.CodeBlocks, &CodeBlock{
					ID:        fmt.Sprintf("codeblock_%d", len(analysis.CodeBlocks)+1),
					Name:      fmt.Sprintf("codeblock_%d", len(analysis.CodeBlocks)+1),
					Type:      codeBlockLang,
					StartLine: codeBlockStart,
					EndLine:   i + 1,
				})
			}
		}
	}

	var updateEndLines func(*Section)
	updateEndLines = func(s *Section) {
		for _, sub := range s.SubSections {
			updateEndLines(sub)
		}
		if len(s.SubSections) > 0 {
			s.LineEnd = s.SubSections[len(s.SubSections)-1].LineEnd
		} else {
			s.LineEnd = s.LineStart
		}
	}
	for _, s := range sections {
		updateEndLines(s)
	}

	analysis.Summary = &DocSummary{
		TotalLines:     len(lines),
		EffectiveLines: da.countEffectiveLines(lines),
		Title:          filepath.Base(filePath),
	}
	analysis.Outline = &DocOutline{
		Sections: sections,
	}

	return analysis, nil
}

func (da *DocAnalyzer) analyzeGenericFile(filePath string, content []byte) (*DocumentAnalysis, error) {
	analysis := &DocumentAnalysis{
		Language: "text",
	}

	lines := strings.Split(string(content), "\n")
	headingRegex := regexp.MustCompile(`^(?:#{1,6}\s+|(?:第[一二三四五六七八九十]+章|Chapter\s+\d+)\s*)(.*)$`)
	sections := make([]*Section, 0)

	for i, line := range lines {
		if match := headingRegex.FindStringSubmatch(line); match != nil {
			sections = append(sections, &Section{
				Title:     strings.TrimSpace(match[1]),
				LineStart: i + 1,
				Level:     1,
			})
		}
	}

	analysis.Summary = &DocSummary{
		TotalLines:     len(lines),
		EffectiveLines: da.countEffectiveLines(lines),
		Title:          filepath.Base(filePath),
	}
	analysis.Outline = &DocOutline{
		Sections: sections,
	}

	return analysis, nil
}

type AnalysisModifyFile struct {
	analyzer *DocAnalyzer
	logger   *log.Logger
}

func NewAnalysisModifyFile(workspace string, logger *log.Logger) *AnalysisModifyFile {
	return &AnalysisModifyFile{
		analyzer: NewDocAnalyzer(workspace, logger),
		logger:   logger,
	}
}

func (m *AnalysisModifyFile) Name() string { return "find_replace_doc" }

func (m *AnalysisModifyFile) Description() string {
	return `A comprehensive document manipulation tool for analyzing, navigating, and modifying large documents.

Supported formats: Go, Python, JavaScript/TypeScript, Markdown, Text, CSV, Excel, Word, JSON, YAML`
}

func (m *AnalysisModifyFile) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"action": map[string]interface{}{
				"type":        "string",
				"enum":        []string{"analyze", "analyze_batch", "outline", "get_block", "replace_block", "delete_block", "insert_block", "search", "list_files"},
				"description": "Operation type",
			},
			"path": map[string]interface{}{
				"type":        "string",
				"description": "File path (absolute or relative to workspace)",
			},
			"paths": map[string]interface{}{
				"type":        "array",
				"description": "List of file paths for batch operations",
				"items": map[string]interface{}{
					"type": "string",
				},
			},
			"pattern": map[string]interface{}{
				"type":        "string",
				"description": "Glob pattern for batch analysis",
			},
			"name": map[string]interface{}{
				"type":        "string",
				"description": "Name of function, struct, or code block to operate on",
			},
			"block_type": map[string]interface{}{
				"type":        "string",
				"enum":        []string{"function", "struct", "interface", "section", "codeblock"},
				"description": "Type of block when searching by name",
			},
			"line_start": map[string]interface{}{
				"type":        "integer",
				"description": "Starting line number (1-indexed)",
			},
			"line_end": map[string]interface{}{
				"type":        "integer",
				"description": "Ending line number (1-indexed)",
			},
			"new_content": map[string]interface{}{
				"type":        "string",
				"description": "New content for replace_block operation",
			},
			"content": map[string]interface{}{
				"type":        "string",
				"description": "Content to insert for insert_block operation",
			},
			"position": map[string]interface{}{
				"type":        "string",
				"enum":        []string{"before", "after", "at_start", "at_end"},
				"description": "Insert position relative to target",
			},
			"search_term": map[string]interface{}{
				"type":        "string",
				"description": "Search pattern (case-insensitive substring)",
			},
			"context_lines": map[string]interface{}{
				"type":        "integer",
				"description": "Number of context lines to show in search results (default: 2)",
			},
			"summary_only": map[string]interface{}{
				"type":        "boolean",
				"description": "For analyze: return only summary to save tokens (default: false)",
			},
			"recursive": map[string]interface{}{
				"type":        "boolean",
				"description": "For list_files: search recursively (default: true)",
			},
		},
		"required": []string{"action"},
	}
}

func (m *AnalysisModifyFile) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	action, _ := params["action"].(string)

	switch action {
	case "analyze":
		path, _ := params["path"].(string)
		if path == "" {
			return "", fmt.Errorf("path parameter is required for analyze action")
		}
		return m.analyzeDocument(path, params)

	case "analyze_batch":
		return m.analyzeBatch(ctx, params)

	case "outline":
		path, _ := params["path"].(string)
		if path == "" {
			return "", fmt.Errorf("path parameter is required for outline action")
		}
		return m.getOutline(path, params)

	case "get_block":
		path, _ := params["path"].(string)
		if path == "" {
			return "", fmt.Errorf("path parameter is required for get_block action")
		}
		return m.getBlock(path, params)

	case "replace_block":
		path, _ := params["path"].(string)
		if path == "" {
			return "", fmt.Errorf("path parameter is required for replace_block action")
		}
		return m.replaceBlock(path, params)

	case "delete_block":
		path, _ := params["path"].(string)
		if path == "" {
			return "", fmt.Errorf("path parameter is required for delete_block action")
		}
		return m.deleteBlock(path, params)

	case "insert_block":
		path, _ := params["path"].(string)
		if path == "" {
			return "", fmt.Errorf("path parameter is required for insert_block action")
		}
		return m.insertBlock(path, params)

	case "search":
		path, _ := params["path"].(string)
		if path == "" {
			return "", fmt.Errorf("path parameter is required for search action")
		}
		return m.searchDocument(path, params)

	case "list_files":
		return m.listFiles(params)

	default:
		return "", fmt.Errorf("unsupported action: %s", action)
	}
}

func (m *AnalysisModifyFile) analyzeBatch(ctx context.Context, params map[string]interface{}) (string, error) {
	var paths []string

	if p, ok := params["paths"].([]interface{}); ok {
		for _, v := range p {
			if s, ok := v.(string); ok {
				paths = append(paths, s)
			}
		}
	}

	if pattern, ok := params["pattern"].(string); ok && pattern != "" {
		matches, err := filepath.Glob(m.analyzer.resolvePath(pattern))
		if err != nil {
			return "", fmt.Errorf("failed to glob pattern: %w", err)
		}
		paths = append(paths, matches...)
	}

	if len(paths) == 0 {
		return "", fmt.Errorf("no files to analyze: provide either 'paths' array or 'pattern'")
	}

	result := &BatchAnalysisResult{
		Files:      make([]*DocumentAnalysis, 0),
		Errors:     make(map[string]string),
		ByLanguage: make(map[string]int),
	}

	for _, path := range paths {
		analysis, err := m.analyzer.AnalyzeDocument(ctx, path)
		if err != nil {
			result.FailedFiles++
			result.Errors[path] = err.Error()
			m.logger.Printf("Failed to analyze %s: %v", path, err)
			continue
		}

		result.AnalyzedFiles++
		result.TotalTokens += analysis.TokenEstimate
		result.ByLanguage[analysis.Language]++
		result.Files = append(result.Files, analysis)
	}

	result.TotalFiles = len(paths)
	result.Summary = m.formatBatchSummary(result)

	if summaryOnly, ok := params["summary_only"].(bool); ok && summaryOnly {
		return result.Summary, nil
	}

	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return result.Summary, nil
	}
	return string(data), nil
}

func (m *AnalysisModifyFile) formatBatchSummary(result *BatchAnalysisResult) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Batch Analysis Summary\n\n"))
	sb.WriteString(fmt.Sprintf("Total Files: %d\n", result.TotalFiles))
	sb.WriteString(fmt.Sprintf("Analyzed: %d\n", result.AnalyzedFiles))
	sb.WriteString(fmt.Sprintf("Failed: %d\n", result.FailedFiles))
	sb.WriteString(fmt.Sprintf("Total Tokens: ~%d\n\n", result.TotalTokens))

	sb.WriteString("By Language\n")
	for lang, count := range result.ByLanguage {
		sb.WriteString(fmt.Sprintf("- %s: %d files\n", lang, count))
	}

	if len(result.Errors) > 0 {
		sb.WriteString("\nErrors\n")
		for path, err := range result.Errors {
			sb.WriteString(fmt.Sprintf("- %s: %s\n", path, err))
		}
	}

	sb.WriteString("\nFiles Analyzed\n")
	for _, file := range result.Files {
		sb.WriteString(fmt.Sprintf("- %s (%s, %d tokens)\n",
			filepath.Base(file.Path), file.Language, file.TokenEstimate))
	}

	return sb.String()
}

func (m *AnalysisModifyFile) listFiles(params map[string]interface{}) (string, error) {
	pattern, _ := params["pattern"].(string)
	recursive := true
	if r, ok := params["recursive"].(bool); ok {
		recursive = r
	}

	type FileInfo struct {
		Path string
		Size int64
	}

	files := make([]FileInfo, 0)

	if recursive {
		err := filepath.Walk(m.analyzer.workspace, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return nil
			}
			if !info.IsDir() {
				if pattern == "" || strings.Contains(info.Name(), pattern) {
					rel, _ := filepath.Rel(m.analyzer.workspace, path)
					files = append(files, FileInfo{
						Path: rel,
						Size: info.Size(),
					})
				}
			}
			return nil
		})
		if err != nil {
			return "", err
		}
	} else {
		entries, err := os.ReadDir(m.analyzer.workspace)
		if err != nil {
			return "", err
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				info, err := entry.Info()
				if err != nil {
					continue
				}
				if pattern == "" || strings.Contains(entry.Name(), pattern) {
					files = append(files, FileInfo{
						Path: entry.Name(),
						Size: info.Size(),
					})
				}
			}
		}
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Files in workspace\n\nFound %d file(s):\n\n", len(files)))

	// 可选：按大小排序显示
	// sort.Slice(files, func(i, j int) bool { return files[i].Size > files[j].Size })

	for _, f := range files {
		// 格式化文件大小
		var sizeStr string
		bytes := f.Size
		switch {
		case bytes >= 1024*1024*1024:
			sizeStr = fmt.Sprintf("%.2f GB", float64(bytes)/(1024*1024*1024))
		case bytes >= 1024*1024:
			sizeStr = fmt.Sprintf("%.2f MB", float64(bytes)/(1024*1024))
		case bytes >= 1024:
			sizeStr = fmt.Sprintf("%.2f KB", float64(bytes)/1024)
		default:
			sizeStr = fmt.Sprintf("%d B", bytes)
		}

		sb.WriteString(fmt.Sprintf("- %s (%s)\n", f.Path, sizeStr))
	}

	// 添加统计信息
	var totalSize int64
	for _, f := range files {
		totalSize += f.Size
	}

	var totalSizeStr string
	switch {
	case totalSize >= 1024*1024*1024:
		totalSizeStr = fmt.Sprintf("%.2f GB", float64(totalSize)/(1024*1024*1024))
	case totalSize >= 1024*1024:
		totalSizeStr = fmt.Sprintf("%.2f MB", float64(totalSize)/(1024*1024))
	case totalSize >= 1024:
		totalSizeStr = fmt.Sprintf("%.2f KB", float64(totalSize)/1024)
	default:
		totalSizeStr = fmt.Sprintf("%d B", totalSize)
	}

	sb.WriteString(fmt.Sprintf("\nTotal size: %s\n", totalSizeStr))

	return sb.String(), nil
}

func (m *AnalysisModifyFile) analyzeDocument(path string, params map[string]interface{}) (string, error) {
	summaryOnly, _ := params["summary_only"].(bool)

	analysis, err := m.analyzer.AnalyzeDocument(context.Background(), path)
	if err != nil {
		return "", err
	}

	if summaryOnly {
		return m.formatSummary(analysis, path), nil
	}

	data, err := json.MarshalIndent(analysis, "", "  ")
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func (m *AnalysisModifyFile) formatSummary(analysis *DocumentAnalysis, path string) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Document Summary: %s\n\n", filepath.Base(path)))
	sb.WriteString(fmt.Sprintf("Language: %s\n", analysis.Language))
	sb.WriteString(fmt.Sprintf("Size: %d bytes\n", analysis.Size))

	if analysis.Summary != nil {
		if analysis.Summary.PackageName != "" {
			sb.WriteString(fmt.Sprintf("Package: %s\n", analysis.Summary.PackageName))
		}

		// 只对代码文件显示行数信息
		if isCodeFile(analysis.Language) {
			sb.WriteString(fmt.Sprintf("Total Lines: %d\n", analysis.Summary.TotalLines))
			sb.WriteString(fmt.Sprintf("Effective Lines: %d\n", analysis.Summary.EffectiveLines))
		}

		// 只有当 complexity 不为空时才显示
		if analysis.Summary.Complexity != "" && isCodeFile(analysis.Language) {
			sb.WriteString(fmt.Sprintf("Complexity: %s\n", analysis.Summary.Complexity))
		}

		// 显示自定义特性（如 CSV 的 Rows/Columns）
		if len(analysis.Summary.KeyFeatures) > 0 {
			sb.WriteString("\n")
			for _, feature := range analysis.Summary.KeyFeatures {
				// 避免显示格式化的空行
				if feature != "" && feature != "\n" {
					sb.WriteString(fmt.Sprintf("%s\n", feature))
				}
			}
		}
	}

	sb.WriteString(fmt.Sprintf("\nEstimated Tokens: %d\n", analysis.TokenEstimate))

	// 只对代码文件显示统计信息
	if isCodeFile(analysis.Language) {
		sb.WriteString("\nStatistics\n")
		sb.WriteString(fmt.Sprintf("- Functions: %d\n", len(analysis.Functions)))
		sb.WriteString(fmt.Sprintf("- Structs: %d\n", len(analysis.Structs)))
		sb.WriteString(fmt.Sprintf("- Interfaces: %d\n", len(analysis.Interfaces)))
		sb.WriteString(fmt.Sprintf("- Variables: %d\n", len(analysis.Variables)))
		sb.WriteString(fmt.Sprintf("- Constants: %d\n", len(analysis.Constants)))
	}

	// 只对代码文件显示依赖
	if len(analysis.Imports) > 0 && isCodeFile(analysis.Language) {
		sb.WriteString("\nDependencies\n")
		for i, imp := range analysis.Imports {
			if i >= 10 {
				sb.WriteString(fmt.Sprintf("... and %d more\n", len(analysis.Imports)-10))
				break
			}
			sb.WriteString(fmt.Sprintf("- %s\n", imp.Path))
		}
	}

	// 对 Excel 文件显示 Sheets
	if len(analysis.Sheets) > 0 {
		sb.WriteString("\nSheets\n")
		for _, sheet := range analysis.Sheets {
			sb.WriteString(fmt.Sprintf("- %s (%d rows, %d cols)\n", sheet.Name, sheet.Rows, sheet.Cols))
		}
	}

	// 对 CSV 文件显示 Tables 详情
	if len(analysis.Tables) > 0 && analysis.Language == "csv" {
		for _, table := range analysis.Tables {
			sb.WriteString(fmt.Sprintf("\nTable: %s\n", table.Name))
			sb.WriteString(fmt.Sprintf("Total Rows: %d\n", table.Rows))
			sb.WriteString(fmt.Sprintf("Columns: %s\n", strings.Join(table.Columns, ", ")))
		}
	}

	return sb.String()
}

func (m *AnalysisModifyFile) replaceBlock(path string, params map[string]interface{}) (string, error) {
	name, _ := params["name"].(string)
	blockType, _ := params["block_type"].(string)

	var lineStart, lineEnd int
	if ls, ok := params["line_start"]; ok {
		switch v := ls.(type) {
		case float64:
			lineStart = int(v)
		case int:
			lineStart = v
		}
	}
	if le, ok := params["line_end"]; ok {
		switch v := le.(type) {
		case float64:
			lineEnd = int(v)
		case int:
			lineEnd = v
		}
	}
	newContent, _ := params["new_content"].(string)

	if newContent == "" {
		return "", fmt.Errorf("new_content is required for replace_block")
	}

	fullPath := m.analyzer.resolvePath(path)

	var start, end int
	var err error

	if name != "" {
		start, end, err = m.findBlockBoundaries(path, name, blockType)
		if err != nil {
			return "", err
		}
	} else if lineStart > 0 {
		start = lineStart - 1
		if lineEnd > 0 {
			end = lineEnd - 1
		} else {
			end = start
		}
	} else {
		return "", fmt.Errorf("need either 'name' or 'line_start' parameter")
	}

	content, err := os.ReadFile(fullPath)
	if err != nil {
		return "", err
	}

	lines := strings.Split(string(content), "\n")
	if start < 0 || start >= len(lines) || end < 0 || end >= len(lines) {
		return "", fmt.Errorf("line range out of bounds: %d-%d (file has %d lines)", start+1, end+1, len(lines))
	}

	newLines := make([]string, 0)
	newLines = append(newLines, lines[:start]...)
	newLines = append(newLines, strings.Split(newContent, "\n")...)
	newLines = append(newLines, lines[end+1:]...)

	if err := os.WriteFile(fullPath, []byte(strings.Join(newLines, "\n")), 0644); err != nil {
		return "", err
	}

	m.analyzer.InvalidateCache(path)

	return fmt.Sprintf("Successfully replaced block (L%d-L%d)\nNew content length: %d characters", start+1, end+1, len(newContent)), nil
}

func (m *AnalysisModifyFile) deleteBlock(path string, params map[string]interface{}) (string, error) {
	name, _ := params["name"].(string)
	blockType, _ := params["block_type"].(string)

	var lineStart, lineEnd int
	if ls, ok := params["line_start"]; ok {
		switch v := ls.(type) {
		case float64:
			lineStart = int(v)
		case int:
			lineStart = v
		}
	}
	if le, ok := params["line_end"]; ok {
		switch v := le.(type) {
		case float64:
			lineEnd = int(v)
		case int:
			lineEnd = v
		}
	}

	fullPath := m.analyzer.resolvePath(path)

	var start, end int
	var err error

	if name != "" {
		start, end, err = m.findBlockBoundaries(path, name, blockType)
		if err != nil {
			return "", err
		}
	} else if lineStart > 0 {
		start = lineStart - 1
		if lineEnd > 0 {
			end = lineEnd - 1
		} else {
			end = start
		}
	} else {
		return "", fmt.Errorf("need either 'name' or 'line_start' parameter")
	}

	content, err := os.ReadFile(fullPath)
	if err != nil {
		return "", err
	}

	lines := strings.Split(string(content), "\n")
	if start < 0 || start >= len(lines) || end < 0 || end >= len(lines) {
		return "", fmt.Errorf("line range out of bounds")
	}

	newLines := make([]string, 0)
	newLines = append(newLines, lines[:start]...)
	newLines = append(newLines, lines[end+1:]...)

	if err := os.WriteFile(fullPath, []byte(strings.Join(newLines, "\n")), 0644); err != nil {
		return "", err
	}

	m.analyzer.InvalidateCache(path)
	return fmt.Sprintf("Successfully deleted block (L%d-L%d)", start+1, end+1), nil
}

func (m *AnalysisModifyFile) findBlockBoundaries(path, name, blockType string) (int, int, error) {
	analysis, err := m.analyzer.AnalyzeDocument(context.Background(), path)
	if err != nil {
		return 0, 0, err
	}

	switch blockType {
	case "function", "":
		for _, fn := range analysis.Functions {
			if fn.Name == name {
				return fn.LineStart - 1, fn.LineEnd - 1, nil
			}
		}
	case "struct":
		for _, st := range analysis.Structs {
			if st.Name == name {
				return st.LineStart - 1, st.LineEnd - 1, nil
			}
		}
	case "interface":
		for _, iface := range analysis.Interfaces {
			if iface.Name == name {
				return iface.LineStart - 1, iface.LineEnd - 1, nil
			}
		}
	}

	for _, fn := range analysis.Functions {
		if strings.Contains(strings.ToLower(fn.Name), strings.ToLower(name)) {
			return fn.LineStart - 1, fn.LineEnd - 1, nil
		}
	}

	return 0, 0, fmt.Errorf("block '%s' not found", name)
}

func (m *AnalysisModifyFile) insertBlock(path string, params map[string]interface{}) (string, error) {
	content, _ := params["content"].(string)
	position, _ := params["position"].(string)
	targetName, _ := params["name"].(string)
	blockType, _ := params["block_type"].(string)

	if content == "" {
		return "", fmt.Errorf("content is required for insert_block")
	}
	if position == "" {
		position = "at_end"
	}

	fullPath := m.analyzer.resolvePath(path)

	fileContent, err := os.ReadFile(fullPath)
	if err != nil {
		return "", err
	}

	lines := strings.Split(string(fileContent), "\n")
	insertLines := strings.Split(content, "\n")
	newLines := make([]string, 0)

	switch position {
	case "before":
		if targetName == "" {
			return "", fmt.Errorf("target name required for 'before' position")
		}
		start, _, err := m.findBlockBoundaries(path, targetName, blockType)
		if err != nil {
			return "", err
		}
		newLines = append(newLines, lines[:start]...)
		newLines = append(newLines, insertLines...)
		newLines = append(newLines, lines[start:]...)

	case "after":
		if targetName == "" {
			return "", fmt.Errorf("target name required for 'after' position")
		}
		_, end, err := m.findBlockBoundaries(path, targetName, blockType)
		if err != nil {
			return "", err
		}
		newLines = append(newLines, lines[:end+1]...)
		newLines = append(newLines, insertLines...)
		newLines = append(newLines, lines[end+1:]...)

	case "at_start":
		newLines = append(newLines, insertLines...)
		newLines = append(newLines, lines...)

	default:
		newLines = append(newLines, lines...)
		if len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) != "" {
			newLines = append(newLines, "")
		}
		newLines = append(newLines, insertLines...)
	}

	if err := os.WriteFile(fullPath, []byte(strings.Join(newLines, "\n")), 0644); err != nil {
		return "", err
	}

	m.analyzer.InvalidateCache(path)
	return fmt.Sprintf("Successfully inserted content (%d lines) at position: %s", len(insertLines), position), nil
}

func (m *AnalysisModifyFile) searchDocument(path string, params map[string]interface{}) (string, error) {
	searchTerm, _ := params["search_term"].(string)
	contextLines := 2
	if cl, ok := params["context_lines"]; ok {
		switch v := cl.(type) {
		case float64:
			contextLines = int(v)
		case int:
			contextLines = v
		}
	}

	if searchTerm == "" {
		return "", fmt.Errorf("search_term is required")
	}

	fullPath := m.analyzer.resolvePath(path)
	content, err := os.ReadFile(fullPath)
	if err != nil {
		return "", err
	}

	lines := strings.Split(string(content), "\n")
	searchLower := strings.ToLower(searchTerm)

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Search results for '%s' in %s\n\n", searchTerm, filepath.Base(path)))

	matches := 0
	for i, line := range lines {
		if strings.Contains(strings.ToLower(line), searchLower) {
			matches++
			start := i - contextLines
			if start < 0 {
				start = 0
			}
			end := i + contextLines + 1
			if end > len(lines) {
				end = len(lines)
			}

			sb.WriteString(fmt.Sprintf("\n--- Match #%d at line %d ---\n", matches, i+1))
			for j := start; j < end; j++ {
				marker := "  "
				if j == i {
					marker = "->"
				}
				sb.WriteString(fmt.Sprintf("%s %4d: %s\n", marker, j+1, lines[j]))
			}
		}
	}

	sb.WriteString(fmt.Sprintf("\n---\nFound %d match(es)", matches))
	return sb.String(), nil
}

type WordDocument struct {
	Body WordBody `xml:"body"`
}

type WordBody struct {
	Paragraphs []WordPara `xml:"p"`
}

type WordPara struct {
	ParaProps *WordParaProps `xml:"pPr"`
	Runs      []WordRun      `xml:"r"`
}

type WordParaProps struct {
	ParaStyle *WordParaStyle `xml:"pStyle"`
}

type WordParaStyle struct {
	Val string `xml:"val,attr"`
}

type WordRun struct {
	Text *string `xml:"t"`
	// 可以扩展到粗体、斜体等
}

// 增强的 readDocxText 返回段落和样式信息
type WordParagraph struct {
	Text    string
	Style   string // 样式名，如 Heading1, Heading2, Normal
	IsTitle bool
	Level   int // 1-6 表示标题级别
	LineNum int
}

func (da *DocAnalyzer) isLikelyHeading(text string) bool {
	// 排除技术特征
	if strings.Contains(text, "=") &&
		(strings.Contains(text, "TYPE=") ||
			strings.Contains(text, "IPADDR=") ||
			strings.Contains(text, "NETMASK=")) {
		return false // 网络配置行不是标题
	}

	// 排除命令行
	if strings.HasPrefix(text, "$ ") ||
		strings.HasPrefix(text, "# ") ||
		strings.HasPrefix(text, "[root@") {
		return false
	}

	// 排除包名（包含版本号）
	if matched, _ := regexp.MatchString(`\d+\.\d+\.\d+`, text); matched {
		return false
	}

	// 排除过长的行（标题通常较短）
	if len([]rune(text)) > 60 {
		return false
	}

	// 原有用例的标题特征...
	return len(text) < 80 && !strings.HasSuffix(text, ".")
}

// 从样式名获取标题级别
func (da *DocAnalyzer) getHeadingLevel(style string) int {
	styleLower := strings.ToLower(style)

	// 标准 Word 样式
	if strings.Contains(styleLower, "heading") || strings.Contains(styleLower, "标题") {
		// 提取数字
		re := regexp.MustCompile(`(\d+)`)
		matches := re.FindStringSubmatch(style)
		if len(matches) > 0 {
			if level, err := strconv.Atoi(matches[1]); err == nil && level >= 1 && level <= 9 {
				return level
			}
		}
		return 1
	}

	// 标题 1-9 的另一种格式
	for i := 1; i <= 9; i++ {
		if style == fmt.Sprintf("Heading%d", i) || style == fmt.Sprintf("标题%d", i) {
			return i
		}
	}

	return 0
}

func (da *DocAnalyzer) analyzeWordFile(filePath string) (*DocumentAnalysis, error) {
	doc, err := document.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("open failed: %w", err)
	}

	analysis := &DocumentAnalysis{
		Language:   "word",
		Path:       filePath,
		Tables:     []*TableInfo{},
		Images:     []*ImageInfo{},
		Sections:   []*Section{},
		CodeBlocks: []*CodeBlock{},
		Functions:  []*FunctionInfo{},
		Variables:  []*VariableInfo{},
		Constants:  []*ConstantInfo{},
		Imports:    []*ImportInfo{},
	}

	if info, err := os.Stat(filePath); err == nil {
		analysis.Size = info.Size()
	}

	// 遍历元素
	var paragraphs []*document.Paragraph
	var tables []*document.Table

	for _, elem := range doc.Body.Elements {
		switch e := elem.(type) {
		case *document.Paragraph:
			paragraphs = append(paragraphs, e)
		case *document.Table:
			tables = append(tables, e)
		}
	}

	// ===================== 处理段落（合并连续空行）=====================
	var sectionStack []*Section
	var processedBlocks []*CodeBlock // 使用这个变量存储处理后的块
	var lastWasEmpty bool = false
	var emptyBlockStart int = 0

	for idx, para := range paragraphs {
		lineNum := idx + 1
		text := getParagraphText(para)
		trimmed := strings.TrimSpace(text)

		// 【增强】尝试从样式中获取标题级别
		level := getHeadingLevelFromStyle(para)
		isTitle := level > 0

		// 如果样式没有提供标题信息，使用文本模式匹配作为备选
		if !isTitle {
			level, isTitle = detectHeadingByText(text)
		}

		isEmpty := trimmed == "" && !isTitle

		if isEmpty {
			// 空段落：如果是第一个空段落，记录开始位置
			if !lastWasEmpty {
				lastWasEmpty = true
				emptyBlockStart = lineNum
			}
			// 继续下一个段落
			continue
		}

		// 遇到非空段落，先处理之前累积的空行块
		if lastWasEmpty {
			// 将连续的空段落合并成一个空行块
			processedBlocks = append(processedBlocks, &CodeBlock{
				ID:        fmt.Sprintf("empty_%d", emptyBlockStart),
				Name:      "空行",
				Type:      "empty_line",
				StartLine: emptyBlockStart,
				EndLine:   lineNum - 1,
				Content:   "", // 空内容
			})
			lastWasEmpty = false
		}

		// 章节结构
		if isTitle && level > 0 {
			sec := &Section{
				Title:       text,
				Level:       level,
				LineStart:   lineNum,
				SubSections: []*Section{},
			}

			// 维护层级栈
			for len(sectionStack) > 0 && sectionStack[len(sectionStack)-1].Level >= level {
				sectionStack = sectionStack[:len(sectionStack)-1]
			}

			if len(sectionStack) > 0 {
				sectionStack[len(sectionStack)-1].SubSections = append(sectionStack[len(sectionStack)-1].SubSections, sec)
			} else {
				analysis.Sections = append(analysis.Sections, sec)
			}
			sectionStack = append(sectionStack, sec)
		}

		// 代码块 - 注意：这里应该添加到 processedBlocks，而不是 analysis.CodeBlocks
		blockType := "paragraph"
		if isTitle {
			blockType = "heading"
		}
		processedBlocks = append(processedBlocks, &CodeBlock{
			ID:        fmt.Sprintf("p%d", lineNum),
			Name:      fmt.Sprintf("段落 %d", lineNum),
			Type:      blockType,
			StartLine: lineNum,
			EndLine:   lineNum,
			Content:   text,
		})
	}

	// 处理文件末尾的空行
	if lastWasEmpty {
		processedBlocks = append(processedBlocks, &CodeBlock{
			ID:        fmt.Sprintf("empty_%d", emptyBlockStart),
			Name:      "空行",
			Type:      "empty_line",
			StartLine: emptyBlockStart,
			EndLine:   len(paragraphs),
			Content:   "",
		})
	}

	// 将处理后的块赋值给 analysis.CodeBlocks
	analysis.CodeBlocks = processedBlocks

	// ===================== 处理表格 =====================
	for i, table := range tables {
		var cols []string
		if len(table.Rows) > 0 {
			for _, cell := range table.Rows[0].Cells {
				cols = append(cols, getCellText(&cell))
			}
		}

		// 提取表格数据（前3行作为预览）
		tableData := make([][]string, 0)
		for rowIdx, row := range table.Rows {
			if rowIdx >= 3 { // 只取前3行作为预览，避免token过大
				break
			}
			rowData := make([]string, 0)
			for _, cell := range row.Cells {
				rowData = append(rowData, getCellText(&cell))
			}
			tableData = append(tableData, rowData)
		}

		ti := &TableInfo{
			Name:    fmt.Sprintf("表格%d", i+1),
			Columns: cols,
			Rows:    len(table.Rows),
		}
		analysis.Tables = append(analysis.Tables, ti)

		// 表格作为 CodeBlock，并包含预览数据
		tableContent := fmt.Sprintf("表格 %d: %d行 x %d列\n", i+1, ti.Rows, len(cols))
		if len(tableData) > 0 {
			tableContent += "预览数据:\n"
			for _, row := range tableData {
				tableContent += "| " + strings.Join(row, " | ") + " |\n"
			}
		}

		analysis.CodeBlocks = append(analysis.CodeBlocks, &CodeBlock{
			ID:        fmt.Sprintf("table%d", i+1),
			Name:      ti.Name,
			Type:      "table",
			StartLine: 0,
			EndLine:   0,
			Content:   tableContent,
		})
	}

	// ===================== 提取图片 =====================
	images := extractImagesFromDocx(filePath)
	analysis.Images = images

	for i, img := range images {
		analysis.CodeBlocks = append(analysis.CodeBlocks, &CodeBlock{
			ID:        fmt.Sprintf("img%d", i+1),
			Name:      img.Name,
			Type:      "image",
			StartLine: -1, // -1 表示位置不可用
			EndLine:   -1,
			Content:   fmt.Sprintf("图片: %s (%s)", img.Name, img.Type),
		})
	}

	// ===================== 全文本统计（只统计非空段落）=====================
	var fullSb strings.Builder
	nonEmptyCount := 0
	for _, p := range paragraphs {
		text := getParagraphText(p)
		if strings.TrimSpace(text) != "" {
			fullSb.WriteString(text)
			fullSb.WriteString("\n")
			nonEmptyCount++
		}
	}
	charCount := len([]rune(fullSb.String()))

	// ===================== 计算有效块数（排除空行块）=====================
	effectiveBlocks := 0
	for _, block := range analysis.CodeBlocks {
		if block.Type != "empty_line" && block.Type != "table" && block.Type != "image" {
			effectiveBlocks++
		}
	}

	// ===================== 摘要 =====================
	analysis.Summary = &DocSummary{
		Title:          getDocTitle(analysis.Sections, filePath),
		TotalLines:     len(paragraphs),
		EffectiveLines: nonEmptyCount,
		KeyFeatures: []string{
			fmt.Sprintf("格式: Microsoft Word (.docx)"),
			fmt.Sprintf("有效段落数: %d", nonEmptyCount),
			fmt.Sprintf("章节数: %d", countSections(analysis.Sections)),
			fmt.Sprintf("表格数: %d", len(tables)),
			fmt.Sprintf("图片数: %d", len(images)),
			fmt.Sprintf("字符数: %d", charCount),
		},
	}

	analysis.Outline = &DocOutline{
		Sections: analysis.Sections,
		Tables:   analysis.Tables,
		Images:   analysis.Images,
	}

	// 修复章节结束行
	for i, section := range analysis.Sections {
		if i+1 < len(analysis.Sections) {
			nextSectionStart := analysis.Sections[i+1].LineStart
			if nextSectionStart > section.LineStart {
				section.LineEnd = nextSectionStart - 1
			} else {
				section.LineEnd = section.LineStart
			}
		} else {
			section.LineEnd = len(paragraphs)
		}
		fixSubSectionLineEnds(section, section.LineEnd)
	}

	// Token 估算
	analysis.TokenEstimate = charCount / 4
	analysis.LastAnalyzed = time.Now()

	return analysis, nil
}

func getParagraphText(p *document.Paragraph) string {
	var sb strings.Builder
	for _, run := range p.Runs {
		sb.WriteString(run.Text.Content)
	}
	return sb.String()
}

func getCellText(cell *document.TableCell) string {
	var sb strings.Builder
	for _, p := range cell.Paragraphs {
		for _, run := range p.Runs {
			sb.WriteString(run.Text.Content)
		}
		sb.WriteString(" ")
	}
	return strings.TrimSpace(sb.String())
}

func getHeadingLevelFromStyle(p *document.Paragraph) int {
	if p.Properties == nil || p.Properties.ParagraphStyle == nil {
		return 0
	}

	styleName := p.Properties.ParagraphStyle.Val
	styleLower := strings.ToLower(styleName)

	// 匹配 Heading1-9
	for i := 1; i <= 9; i++ {
		if styleLower == fmt.Sprintf("heading%d", i) {
			return i
		}
	}

	// 匹配中文标题样式
	for i := 1; i <= 9; i++ {
		if styleLower == fmt.Sprintf("标题%d", i) {
			return i
		}
	}

	return 0
}

// detectHeadingByText 通过文本模式检测标题
func detectHeadingByText(text string) (int, bool) {
	// 模式1: # 标题
	if strings.HasPrefix(text, "#") {
		level := 0
		for _, ch := range text {
			if ch == '#' {
				level++
			} else {
				break
			}
		}
		if level > 0 && level <= 6 && len(text) > level {
			return level, true
		}
	}

	if matched, _ := regexp.MatchString(`^\d+\.\d+\s+`, text); matched {
		return 2, true
	}

	// 新增：检测 (1) 格式
	if matched, _ := regexp.MatchString(`^\(\d+\)\s+`, text); matched {
		return 2, true
	}

	// 新增：检测 1) 格式
	if matched, _ := regexp.MatchString(`^\d+\)\s+`, text); matched {
		return 2, true
	}

	// 模式2: 第X章
	if matched, _ := regexp.MatchString(`^第[一二三四五六七八九十百千万]+章`, text); matched {
		return 1, true
	}

	// 模式3: 数字编号 1. 1.1 等
	if matched, _ := regexp.MatchString(`^\d+(\.\d+)*\s+`, text); matched {
		// 简单判断：如果有点号，可能是二级标题
		if strings.Contains(text, ".") {
			return 2, true
		}
		return 1, true
	}

	// 模式4: 中文编号 一、 二、 等
	if matched, _ := regexp.MatchString(`^[一二三四五六七八九十]+、`, text); matched {
		return 1, true
	}

	// 模式5: 短文本全大写（通常是标题）
	if len(text) < 50 && strings.ToUpper(text) == text && len(text) > 3 {
		return 1, true
	}

	return 0, false
}

// extractImagesFromDocx 从 docx 文件中提取图片信息
func extractImagesFromDocx(filePath string) []*ImageInfo {
	images := []*ImageInfo{}

	// 打开 zip 文件
	r, err := zip.OpenReader(filePath)
	if err != nil {
		return images
	}
	defer r.Close()

	// 遍历所有文件，查找 media 目录下的图片
	for _, f := range r.File {
		if strings.HasPrefix(f.Name, "word/media/") {
			ext := filepath.Ext(f.Name)
			imgType := strings.TrimPrefix(ext, ".")

			images = append(images, &ImageInfo{
				Name: filepath.Base(f.Name),
				Type: imgType,
			})
		}
	}

	return images
}

func getDocTitle(sections []*Section, filePath string) string {
	for _, s := range sections {
		if s.Level == 1 {
			return s.Title
		}
	}
	if len(sections) > 0 {
		return sections[0].Title
	}
	return filepath.Base(filePath)
}

// countSections 统计章节数量
func countSections(sections []*Section) int {
	cnt := len(sections)
	for _, s := range sections {
		cnt += countSections(s.SubSections)
	}
	return cnt
}

func updateSectionEndLines(sections []*Section, totalLines int) {
	for _, s := range sections {
		updateSectionEndLine(s, totalLines)
	}
}
func updateSectionEndLine(s *Section, totalLines int) int {
	if len(s.SubSections) > 0 {
		maxLine := s.LineStart
		for _, sub := range s.SubSections {
			endLine := updateSectionEndLine(sub, totalLines)
			if endLine > maxLine {
				maxLine = endLine
			}
		}
		s.LineEnd = maxLine
	} else {
		// 如果没有子章节，结束行是下一章节开始前
		// 这里先用起始行，稍后在主函数中统一处理
		s.LineEnd = s.LineStart
	}
	return s.LineEnd
}

func fixSubSectionLineEnds(section *Section, parentEnd int) {
	if len(section.SubSections) == 0 {
		return
	}
	for i, sub := range section.SubSections {
		if i+1 < len(section.SubSections) {
			sub.LineEnd = section.SubSections[i+1].LineStart - 1
		} else {
			sub.LineEnd = parentEnd
		}
		fixSubSectionLineEnds(sub, sub.LineEnd)
	}
}

func mergeParagraphBlocks(blocks []*CodeBlock) []*CodeBlock {
	merged := []*CodeBlock{}
	var current *CodeBlock

	for _, block := range blocks {
		if block.Type == "heading" {
			if current != nil {
				merged = append(merged, current)
				current = nil
			}
			merged = append(merged, block)
		} else if block.Type == "paragraph" {
			if current == nil {
				current = &CodeBlock{
					ID:        fmt.Sprintf("section_%d", len(merged)+1),
					Name:      "内容块",
					Type:      "content",
					StartLine: block.StartLine,
					Content:   block.Content,
				}
			} else {
				current.Content += "\n" + block.Content
				current.EndLine = block.EndLine
			}
		} else {
			if current != nil {
				merged = append(merged, current)
				current = nil
			}
			merged = append(merged, block)
		}
	}

	if current != nil {
		merged = append(merged, current)
	}

	return merged
}

//#################################pdf

func (da *DocAnalyzer) analyzePDFFile(filePath string) (*DocumentAnalysis, error) {
	analysis := &DocumentAnalysis{
		Language:   "pdf",
		Path:       filePath,
		Sections:   make([]*Section, 0),
		Tables:     make([]*TableInfo, 0),
		Images:     make([]*ImageInfo, 0),
		CodeBlocks: make([]*CodeBlock, 0),
	}

	// 文件大小
	if info, err := os.Stat(filePath); err == nil {
		analysis.Size = info.Size()
	}

	// 打开 PDF
	doc, err := pdf.OpenFile(filePath)
	if err != nil {
		da.logger.Printf("open pdf failed: %v", err)
		return da.analyzePDFFallback(filePath, analysis)
	}

	totalPages := doc.NumPages()
	var fullText strings.Builder
	totalChars := 0

	// 逐页提取文本，每页内容截断
	for pageNum := 0; pageNum < totalPages; pageNum++ {
		page := doc.Page(pageNum)

		lines, err := page.TextLines()
		if err != nil {
			da.logger.Printf("page %d TextLines error: %v", pageNum+1, err)
			continue
		}

		// 构建当页完整文本
		var pageText strings.Builder
		for _, line := range lines {
			pageText.WriteString(line.Text)
			pageText.WriteString("\n")
			totalChars += len([]rune(line.Text))
		}

		pageStr := pageText.String()

		// 截断每页内容到 300 字符（保留概览信息）
		previewContent := pageStr
		if len(previewContent) > 300 {
			previewContent = previewContent[:300] + "...\n[Page truncated, total length: " +
				fmt.Sprintf("%d", len(pageStr)) + " chars]"
		}

		// 存储截断后的内容作为 CodeBlock
		analysis.CodeBlocks = append(analysis.CodeBlocks, &CodeBlock{
			ID:        fmt.Sprintf("page_%d", pageNum+1),
			Name:      fmt.Sprintf("Page %d", pageNum+1),
			Type:      "page",
			StartLine: 1,
			EndLine:   strings.Count(pageStr, "\n"),
			Content:   previewContent, // 使用截断内容
		})

		// 仍然构建完整文本用于摘要统计
		fullText.WriteString(pageStr)
		fullText.WriteString("\n\n")
	}

	textContent := fullText.String()

	// 标题
	title := filepath.Base(filePath)

	// 预览（只取前500字符）
	preview := textContent
	if len(preview) > 500 {
		preview = preview[:500] + "..."
	}

	// 摘要
	analysis.Summary = &DocSummary{
		Title:          title,
		TotalLines:     strings.Count(textContent, "\n"),
		EffectiveLines: countNonEmptyLines(textContent),
		KeyFeatures: []string{
			fmt.Sprintf("Format: PDF"),
			fmt.Sprintf("Pages: %d", totalPages),
			fmt.Sprintf("Size: %d bytes", analysis.Size),
			fmt.Sprintf("Characters: %d", totalChars),
			fmt.Sprintf("Structure Preview (first 500 chars):\n%s", preview),
		},
	}

	analysis.TokenEstimate = totalChars / 4
	analysis.LastAnalyzed = time.Now()

	da.logger.Printf("pdf analyzed: pages=%d, chars=%d, code_blocks=%d (truncated previews)",
		totalPages, totalChars, len(analysis.CodeBlocks))

	return analysis, nil
}

// 统计非空行
func countNonEmptyLines(text string) int {
	lines := strings.Split(text, "\n")
	count := 0
	for _, line := range lines {
		if strings.TrimSpace(line) != "" {
			count++
		}
	}
	return count
}

func (da *DocAnalyzer) analyzePDFFallback(filePath string, analysis *DocumentAnalysis) (*DocumentAnalysis, error) {
	analysis.Summary = &DocSummary{
		Title: filepath.Base(filePath),
		KeyFeatures: []string{
			fmt.Sprintf("Size: %d bytes", analysis.Size),
			"Format: PDF",
			"Text extraction limited",
		},
	}
	return analysis, nil
}

// 清理 PDF 文本（如果需要）
func (da *DocAnalyzer) cleanPDFText(text string) string {
	// 移除控制字符
	re := regexp.MustCompile(`[\x00-\x08\x0B\x0C\x0E-\x1F\x7F]`)
	text = re.ReplaceAllString(text, "")

	// 修复常见 PDF 乱码
	text = strings.ReplaceAll(text, "�", "")

	// 合并被错误分割的单词
	text = regexp.MustCompile(`(\w+)-\n(\w+)`).ReplaceAllString(text, "$1$2")

	return strings.TrimSpace(text)
}

//###############################code

func (da *DocAnalyzer) extractFuncInfoAdvanced(fset *token.FileSet, fn *ast.FuncDecl, structMap map[string]*StructInfo) *FunctionInfo {
	info := &FunctionInfo{
		Name:       fn.Name.Name,
		LineStart:  fset.Position(fn.Pos()).Line,
		LineEnd:    fset.Position(fn.End()).Line,
		Parameters: make([]*ParamInfo, 0),
		Returns:    make([]*ReturnInfo, 0),
		Calls:      make([]string, 0),
		Metadata:   make(map[string]interface{}),
	}

	// 提取接收者
	if fn.Recv != nil && len(fn.Recv.List) > 0 {
		recv := fn.Recv.List[0]
		switch t := recv.Type.(type) {
		case *ast.Ident:
			info.Receiver = t.Name
		case *ast.StarExpr:
			if ident, ok := t.X.(*ast.Ident); ok {
				info.Receiver = ident.Name
			}
		}
	}

	// 提取参数
	if fn.Type.Params != nil {
		for _, field := range fn.Type.Params.List {
			paramType := da.extractType(field.Type)
			for _, name := range field.Names {
				info.Parameters = append(info.Parameters, &ParamInfo{
					Name: name.Name,
					Type: paramType,
				})
			}
			if len(field.Names) == 0 {
				info.Parameters = append(info.Parameters, &ParamInfo{
					Type: paramType,
				})
			}
		}
	}

	// 提取返回值
	if fn.Type.Results != nil {
		for _, field := range fn.Type.Results.List {
			returnType := da.extractType(field.Type)
			if len(field.Names) > 0 {
				for _, name := range field.Names {
					info.Returns = append(info.Returns, &ReturnInfo{
						Name: name.Name,
						Type: returnType,
					})
				}
			} else {
				info.Returns = append(info.Returns, &ReturnInfo{
					Type: returnType,
				})
			}
		}
	}

	// 提取文档
	if fn.Doc != nil {
		info.Doc = fn.Doc.Text()
	}

	// 提取函数调用关系
	if fn.Body != nil {
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			if call, ok := n.(*ast.CallExpr); ok {
				switch fun := call.Fun.(type) {
				case *ast.Ident:
					info.Calls = append(info.Calls, fun.Name)
				case *ast.SelectorExpr:
					if ident, ok := fun.X.(*ast.Ident); ok {
						info.Calls = append(info.Calls, ident.Name+"."+fun.Sel.Name)
					} else {
						info.Calls = append(info.Calls, fun.Sel.Name)
					}
				}
			}
			return true
		})
		// 去重
		info.Calls = textutil.UniqueStrings(info.Calls)
	}

	return info
}

func (da *DocAnalyzer) analyzeGoFile(filePath string, content []byte) (*DocumentAnalysis, error) {
	fset := token.NewFileSet()
	node, err := parser.ParseFile(fset, filePath, content, parser.ParseComments)
	if err != nil {
		return nil, fmt.Errorf("failed to parse Go file: %w", err)
	}

	analysis := &DocumentAnalysis{
		Language:   "go",
		Functions:  make([]*FunctionInfo, 0),
		Structs:    make([]*StructInfo, 0),
		Interfaces: make([]*InterfaceInfo, 0),
		Variables:  make([]*VariableInfo, 0),
		Constants:  make([]*ConstantInfo, 0),
		Imports:    make([]*ImportInfo, 0),
		CodeBlocks: make([]*CodeBlock, 0),
	}

	// 包名
	packageName := ""
	if node.Name != nil {
		packageName = node.Name.Name
		analysis.Summary = &DocSummary{
			PackageName: packageName,
		}
	}

	// 解析导入
	for _, imp := range node.Imports {
		importInfo := &ImportInfo{
			Path: strings.Trim(imp.Path.Value, "\""),
		}
		if imp.Name != nil {
			importInfo.Alias = imp.Name.Name
		}
		analysis.Imports = append(analysis.Imports, importInfo)
	}

	// 第一遍：收集结构体，建立映射
	structMap := make(map[string]*StructInfo)

	// 解析声明
	for _, decl := range node.Decls {
		switch d := decl.(type) {
		case *ast.GenDecl:
			switch d.Tok {
			case token.TYPE:
				for _, spec := range d.Specs {
					typeSpec := spec.(*ast.TypeSpec)
					switch t := typeSpec.Type.(type) {
					case *ast.StructType:
						structInfo := da.extractStructInfo(fset, typeSpec.Name.Name, t, d.Doc)
						structInfo.IsExported = ast.IsExported(typeSpec.Name.Name)
						structMap[typeSpec.Name.Name] = structInfo
						analysis.Structs = append(analysis.Structs, structInfo)
					case *ast.InterfaceType:
						ifaceInfo := da.extractInterfaceInfo(fset, typeSpec.Name.Name, t, d.Doc)
						analysis.Interfaces = append(analysis.Interfaces, ifaceInfo)
					}
				}
			}
		}
	}

	// 第二遍：解析函数，关联到结构体
	largestBlock := ""
	maxLines := 0

	for _, decl := range node.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			funcInfo := da.extractFuncInfoAdvanced(fset, d, structMap)

			// 判断作用域
			if funcInfo.Receiver != "" {
				funcInfo.Scope = "method"
				// 将方法添加到对应的结构体
				if structInfo, exists := structMap[funcInfo.Receiver]; exists {
					structInfo.Methods = append(structInfo.Methods, funcInfo.Name)
				}
			} else if funcInfo.Name == "main" && packageName == "main" {
				funcInfo.Scope = "entry"
				funcInfo.IsMain = true
			} else {
				funcInfo.Scope = "global"
			}

			funcInfo.IsExported = ast.IsExported(funcInfo.Name)

			// 计算代码行数，找最大的块
			lines := funcInfo.LineEnd - funcInfo.LineStart + 1
			if lines > maxLines {
				maxLines = lines
				largestBlock = funcInfo.Name
			}

			analysis.Functions = append(analysis.Functions, funcInfo)
			analysis.CodeBlocks = make([]*CodeBlock, 0)
		}
	}

	// 构建简化的 Outline
	analysis.Outline = da.buildSimplifiedOutline(analysis, packageName, largestBlock, maxLines)
	da.buildSummary(analysis, content)

	return analysis, nil
}

func (da *DocAnalyzer) extractFuncInfo(fset *token.FileSet, fn *ast.FuncDecl) *FunctionInfo {
	info := &FunctionInfo{
		Name:       fn.Name.Name,
		LineStart:  fset.Position(fn.Pos()).Line,
		LineEnd:    fset.Position(fn.End()).Line,
		Parameters: make([]*ParamInfo, 0),
		Returns:    make([]*ReturnInfo, 0),
		Calls:      make([]string, 0),
		Metadata:   make(map[string]interface{}),
	}

	if fn.Recv != nil && len(fn.Recv.List) > 0 {
		recv := fn.Recv.List[0]
		switch t := recv.Type.(type) {
		case *ast.Ident:
			info.Receiver = t.Name
		case *ast.StarExpr:
			if ident, ok := t.X.(*ast.Ident); ok {
				info.Receiver = "*" + ident.Name
			}
		}
	}

	if fn.Type.Params != nil {
		for _, field := range fn.Type.Params.List {
			paramType := da.extractType(field.Type)
			for _, name := range field.Names {
				info.Parameters = append(info.Parameters, &ParamInfo{
					Name: name.Name,
					Type: paramType,
				})
			}
			if len(field.Names) == 0 {
				info.Parameters = append(info.Parameters, &ParamInfo{
					Type: paramType,
				})
			}
		}
	}

	if fn.Type.Results != nil {
		for _, field := range fn.Type.Results.List {
			returnType := da.extractType(field.Type)
			if len(field.Names) > 0 {
				for _, name := range field.Names {
					info.Returns = append(info.Returns, &ReturnInfo{
						Name: name.Name,
						Type: returnType,
					})
				}
			} else {
				info.Returns = append(info.Returns, &ReturnInfo{
					Type: returnType,
				})
			}
		}
	}

	if fn.Doc != nil {
		info.Doc = fn.Doc.Text()
	}

	if fn.Body != nil {
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			if call, ok := n.(*ast.CallExpr); ok {
				if fun, ok := call.Fun.(*ast.Ident); ok {
					info.Calls = append(info.Calls, fun.Name)
				}
			}
			return true
		})
	}

	return info
}
func (da *DocAnalyzer) extractStructInfo(fset *token.FileSet, name string, st *ast.StructType, doc *ast.CommentGroup) *StructInfo {
	info := &StructInfo{
		Name:      name,
		Fields:    make([]*FieldInfo, 0),
		Methods:   make([]string, 0),
		LineStart: fset.Position(st.Pos()).Line,
		LineEnd:   fset.Position(st.End()).Line,
		Metadata:  make(map[string]interface{}),
	}

	if doc != nil {
		info.Doc = doc.Text()
	}

	if st.Fields != nil {
		for _, field := range st.Fields.List {
			fieldInfo := &FieldInfo{
				Type: da.extractType(field.Type),
			}
			if len(field.Names) > 0 {
				fieldInfo.Name = field.Names[0].Name
			}
			if field.Tag != nil {
				fieldInfo.Tag = field.Tag.Value
			}
			info.Fields = append(info.Fields, fieldInfo)
		}
	}

	return info
}

func (da *DocAnalyzer) extractInterfaceInfo(fset *token.FileSet, name string, it *ast.InterfaceType, doc *ast.CommentGroup) *InterfaceInfo {
	info := &InterfaceInfo{
		Name:      name,
		Methods:   make([]*MethodSig, 0),
		LineStart: fset.Position(it.Pos()).Line,
		LineEnd:   fset.Position(it.End()).Line,
	}

	if doc != nil {
		info.Doc = doc.Text()
	}

	if it.Methods != nil {
		for _, method := range it.Methods.List {
			if len(method.Names) > 0 {
				funcType := method.Type.(*ast.FuncType)
				methodSig := &MethodSig{
					Name:       method.Names[0].Name,
					Parameters: make([]*ParamInfo, 0),
					Returns:    make([]*ReturnInfo, 0),
				}

				if funcType.Params != nil {
					for _, param := range funcType.Params.List {
						paramType := da.extractType(param.Type)
						for _, name := range param.Names {
							methodSig.Parameters = append(methodSig.Parameters, &ParamInfo{
								Name: name.Name,
								Type: paramType,
							})
						}
					}
				}

				if funcType.Results != nil {
					for _, result := range funcType.Results.List {
						returnType := da.extractType(result.Type)
						methodSig.Returns = append(methodSig.Returns, &ReturnInfo{
							Type: returnType,
						})
					}
				}

				info.Methods = append(info.Methods, methodSig)
			}
		}
	}

	return info
}

func (da *DocAnalyzer) extractType(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		return "*" + da.extractType(t.X)
	case *ast.ArrayType:
		return "[]" + da.extractType(t.Elt)
	case *ast.MapType:
		return "map[" + da.extractType(t.Key) + "]" + da.extractType(t.Value)
	case *ast.SelectorExpr:
		return da.extractType(t.X) + "." + t.Sel.Name
	case *ast.InterfaceType:
		return "interface{}"
	case *ast.FuncType:
		return "func"
	case *ast.ChanType:
		return "chan " + da.extractType(t.Value)
	default:
		return "unknown"
	}
}

func (da *DocAnalyzer) extractValue(expr ast.Expr) string {
	switch v := expr.(type) {
	case *ast.BasicLit:
		return v.Value
	case *ast.Ident:
		return v.Name
	case *ast.CompositeLit:
		return "{...}"
	case *ast.UnaryExpr:
		return da.extractValue(v.X)
	default:
		return "?"
	}
}

func (da *DocAnalyzer) estimateComplexity(body *ast.BlockStmt) int {
	if body == nil {
		return 1
	}
	complexity := 1
	ast.Inspect(body, func(n ast.Node) bool {
		switch n.(type) {
		case *ast.IfStmt, *ast.ForStmt, *ast.RangeStmt, *ast.CaseClause:
			complexity++
		}
		return true
	})
	return complexity
}
func (da *DocAnalyzer) countEffectiveLines(lines []string) int {
	count := 0
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" && !strings.HasPrefix(trimmed, "//") && !strings.HasPrefix(trimmed, "/*") {
			count++
		}
	}
	return count
}

func (da *DocAnalyzer) buildFunctionSignature(fn *FunctionInfo) string {
	var parts []string

	if fn.Receiver != "" {
		parts = append(parts, fmt.Sprintf("func (%s) %s(", fn.Receiver, fn.Name))
	} else {
		parts = append(parts, fmt.Sprintf("func %s(", fn.Name))
	}

	params := make([]string, 0)
	for _, p := range fn.Parameters {
		if p.Name != "" {
			params = append(params, fmt.Sprintf("%s %s", p.Name, p.Type))
		} else {
			params = append(params, p.Type)
		}
	}
	parts = append(parts, strings.Join(params, ", "))
	parts = append(parts, ")")

	if len(fn.Returns) > 0 {
		returns := make([]string, 0)
		for _, r := range fn.Returns {
			if r.Name != "" {
				returns = append(returns, fmt.Sprintf("%s %s", r.Name, r.Type))
			} else {
				returns = append(returns, r.Type)
			}
		}
		if len(returns) == 1 {
			parts = append(parts, " "+returns[0])
		} else {
			parts = append(parts, " ("+strings.Join(returns, ", ")+")")
		}
	}

	return strings.Join(parts, "")
}

func (da *DocAnalyzer) estimateTokens(analysis *DocumentAnalysis) int {
	tokens := 0
	for _, fn := range analysis.Functions {
		tokens += len(fn.Name) + len(fn.Doc)/4
		for _, p := range fn.Parameters {
			tokens += len(p.Name) + len(p.Type)
		}
	}
	for _, st := range analysis.Structs {
		tokens += len(st.Name) + len(st.Doc)/4
	}
	return tokens / 4
}

func (da *DocAnalyzer) analyzePythonFile(filePath string, content []byte) (*DocumentAnalysis, error) {
	analysis := &DocumentAnalysis{
		Language:   "python",
		Functions:  make([]*FunctionInfo, 0),
		Structs:    make([]*StructInfo, 0),
		Variables:  make([]*VariableInfo, 0),
		Imports:    make([]*ImportInfo, 0),
		CodeBlocks: make([]*CodeBlock, 0),
	}

	lines := strings.Split(string(content), "\n")

	// 正则表达式
	funcRegex := regexp.MustCompile(`^(\s*)def\s+(\w+)\s*\(([^)]*)\)`)
	classRegex := regexp.MustCompile(`^(\s*)class\s+(\w+)`)
	importRegex := regexp.MustCompile(`^(?:from\s+(\S+)\s+)?import\s+(\S+)`)
	callRegex := regexp.MustCompile(`\b(\w+)\s*\(`)

	currentClass := ""
	classIndent := -1

	// 第一遍：收集类信息
	for i, line := range lines {

		// 检测类定义
		if classMatch := classRegex.FindStringSubmatch(line); classMatch != nil {
			className := classMatch[2]
			classIndent = len(classMatch[1])

			structInfo := &StructInfo{
				Name:       className,
				LineStart:  i + 1,
				LineEnd:    i + 1,
				Methods:    make([]string, 0),
				Fields:     make([]*FieldInfo, 0),
				IsExported: ast.IsExported(className),
			}
			analysis.Structs = append(analysis.Structs, structInfo)
		}
	}

	// 第二遍：解析函数和调用关系
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)

		// 检测类定义，更新当前类上下文
		if classMatch := classRegex.FindStringSubmatch(line); classMatch != nil {
			currentClass = classMatch[2]
			classIndent = len(classMatch[1])
			continue
		}

		// 检测函数定义
		if fnMatch := funcRegex.FindStringSubmatch(line); fnMatch != nil {
			funcName := fnMatch[2]
			funcIndent := len(fnMatch[1])

			// 跳过私有方法（以_开头但不是__init__）
			if strings.HasPrefix(funcName, "_") && funcName != "__init__" && funcName != "__str__" && funcName != "__repr__" {
				continue
			}

			funcInfo := &FunctionInfo{
				Name:       funcName,
				LineStart:  i + 1,
				LineEnd:    i + 1,
				Parameters: make([]*ParamInfo, 0),
				Returns:    make([]*ReturnInfo, 0),
				Calls:      make([]string, 0),
				IsExported: !strings.HasPrefix(funcName, "_"),
			}

			// 判断是否在类内部
			if currentClass != "" && funcIndent > classIndent {
				funcInfo.Receiver = currentClass
				funcInfo.Scope = "method"
				funcInfo.IsMain = false

				// 添加到类的方法列表
				for _, st := range analysis.Structs {
					if st.Name == currentClass {
						st.Methods = append(st.Methods, funcName)
						break
					}
				}
			} else {
				funcInfo.Scope = "global"
				funcInfo.IsMain = (funcName == "main")
			}

			// 解析参数
			params := fnMatch[3]
			if params != "" {
				for _, p := range strings.Split(params, ",") {
					p = strings.TrimSpace(p)
					if p == "" || p == "self" || p == "cls" {
						continue
					}
					// 处理参数默认值
					paramName := strings.Split(p, "=")[0]
					paramName = strings.TrimSpace(paramName)
					// 处理类型注解
					if colonIdx := strings.Index(paramName, ":"); colonIdx > 0 {
						paramName = strings.TrimSpace(paramName[:colonIdx])
					}
					if paramName != "" {
						funcInfo.Parameters = append(funcInfo.Parameters, &ParamInfo{
							Name: paramName,
							Type: "", // Python 无静态类型，留空
						})
					}
				}
			}

			// 找到函数结束位置并提取调用关系
			funcBody := ""
			for j := i + 1; j < len(lines); j++ {
				nextLine := lines[j]
				nextTrimmed := strings.TrimSpace(nextLine)

				// 计算下一行的缩进
				nextIndent := len(nextLine) - len(strings.TrimLeft(nextLine, " \t"))

				// 如果遇到缩进减少且非空行，函数结束
				if nextTrimmed != "" && !strings.HasPrefix(nextTrimmed, "#") {
					if nextIndent <= funcIndent {
						funcInfo.LineEnd = j
						break
					}
					// 收集函数体用于调用分析
					funcBody += nextLine + "\n"
				}
				if j == len(lines)-1 {
					funcInfo.LineEnd = j + 1
				}
			}

			// 提取函数调用
			if funcBody != "" {
				matches := callRegex.FindAllStringSubmatch(funcBody, -1)
				for _, match := range matches {
					if len(match) > 1 {
						callName := match[1]
						// 过滤掉 Python 关键字和常见内置函数
						if !isPythonBuiltin(callName) && callName != funcName {
							funcInfo.Calls = append(funcInfo.Calls, callName)
						}
					}
				}
				// 去重
				funcInfo.Calls = textutil.UniqueStrings(funcInfo.Calls)
			}

			analysis.Functions = append(analysis.Functions, funcInfo)
			analysis.CodeBlocks = make([]*CodeBlock, 0)
			continue
		}

		// 检测类结束（当缩进回到0且不是空行时）
		if currentClass != "" && classIndent >= 0 {
			indent := len(line) - len(strings.TrimLeft(line, " \t"))
			if indent <= classIndent && strings.TrimSpace(line) != "" && !strings.HasPrefix(strings.TrimSpace(line), "#") {
				// 更新类的结束行
				for _, st := range analysis.Structs {
					if st.Name == currentClass && st.LineEnd == st.LineStart {
						st.LineEnd = i
						break
					}
				}
				currentClass = ""
				classIndent = -1
			}
		}

		// 解析导入
		if importMatch := importRegex.FindStringSubmatch(trimmed); importMatch != nil {
			importPath := importMatch[2]
			if importMatch[1] != "" {
				importPath = importMatch[1] + "." + importPath
			}

			// 处理多个导入在一行的情况
			if strings.Contains(importPath, ",") {
				for _, singleImport := range strings.Split(importPath, ",") {
					singleImport = strings.TrimSpace(singleImport)
					if singleImport != "" {
						analysis.Imports = append(analysis.Imports, &ImportInfo{
							Path: singleImport,
						})
					}
				}
			} else if importPath != "" {
				analysis.Imports = append(analysis.Imports, &ImportInfo{
					Path: importPath,
				})
			}
		}
	}

	// 确保所有类都有正确的结束行
	for _, st := range analysis.Structs {
		if st.LineEnd == st.LineStart {
			st.LineEnd = st.LineStart
		}
	}

	// 构建统计信息
	globalFuncs := 0
	methods := 0
	for _, fn := range analysis.Functions {
		if fn.Scope == "global" {
			globalFuncs++
		} else {
			methods++
		}
	}

	analysis.Summary = &DocSummary{
		TotalLines:     len(lines),
		EffectiveLines: da.countEffectiveLines(lines),
		PackageName:    "", // Python 没有包名概念
		KeyFeatures: []string{
			fmt.Sprintf("%d global functions", globalFuncs),
			fmt.Sprintf("%d methods", methods),
			fmt.Sprintf("%d classes", len(analysis.Structs)),
			fmt.Sprintf("%d imports", len(analysis.Imports)),
		},
	}

	// 找到 main 函数
	for _, fn := range analysis.Functions {
		if fn.Name == "main" && fn.Scope == "global" {
			analysis.Summary.MainPurpose = "Entry point: main()"
			break
		}
	}

	// 构建 outline
	analysis.Outline = da.buildOutline(analysis)

	return analysis, nil
}

// 判断是否是 Python 内置函数
func isPythonBuiltin(name string) bool {
	builtins := map[string]bool{
		"print": true, "len": true, "str": true, "int": true, "float": true,
		"list": true, "dict": true, "set": true, "tuple": true, "range": true,
		"enumerate": true, "zip": true, "map": true, "filter": true, "sum": true,
		"min": true, "max": true, "sorted": true, "reversed": true, "open": true,
		"type": true, "isinstance": true, "hasattr": true, "getattr": true,
		"setattr": true, "delattr": true, "property": true, "staticmethod": true,
		"classmethod": true, "super": true, "__init__": true,
	}
	return builtins[name]
}

func (da *DocAnalyzer) analyzeJSFile(filePath string, content []byte) (*DocumentAnalysis, error) {
	analysis := &DocumentAnalysis{
		Language:   "javascript",
		Functions:  make([]*FunctionInfo, 0),
		Variables:  make([]*VariableInfo, 0),
		CodeBlocks: make([]*CodeBlock, 0),
	}

	lines := strings.Split(string(content), "\n")
	funcRegex := regexp.MustCompile(`(?:function\s+(\w+)|(\w+)\s*=\s*(?:async\s*)?\([^)]*\)\s*=>)`)

	for i, line := range lines {
		if match := funcRegex.FindStringSubmatch(line); match != nil {
			name := match[1]
			if name == "" {
				name = match[2]
			}
			if name != "" {
				funcInfo := &FunctionInfo{
					Name:      name,
					LineStart: i + 1,
					LineEnd:   i + 1,
				}
				analysis.Functions = append(analysis.Functions, funcInfo)
				analysis.CodeBlocks = append(analysis.CodeBlocks, &CodeBlock{
					ID:        fmt.Sprintf("func_%s", name),
					Name:      name,
					Type:      "function",
					StartLine: i + 1,
					EndLine:   i + 1,
				})
			}
		}
	}

	analysis.Summary = &DocSummary{TotalLines: len(lines), EffectiveLines: da.countEffectiveLines(lines)}
	analysis.Outline = da.buildOutline(analysis)
	return analysis, nil
}

// 辅助函数：判断是否是代码文件
func isCodeFile(language string) bool {
	codeLanguages := map[string]bool{
		"go": true, "python": true, "javascript": true, "typescript": true,
		"java": true, "c": true, "cpp": true, "rust": true,
	}
	return codeLanguages[language]
}

func (m *AnalysisModifyFile) getOutline(path string, params map[string]interface{}) (string, error) {
	analysis, err := m.analyzer.AnalyzeDocument(context.Background(), path)
	if err != nil {
		return "", err
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Outline: %s\n\n", filepath.Base(path)))
	sb.WriteString(fmt.Sprintf("Token estimate for this outline: ~%d (use get_block for full content)\n\n", analysis.TokenEstimate/3))

	if len(analysis.Outline.Functions) > 0 {
		sb.WriteString("Functions\n\n")
		for _, fn := range analysis.Outline.Functions {
			sb.WriteString(fmt.Sprintf("- %s (L%d-L%d)\n", fn.Name, fn.LineStart, fn.LineEnd))

		}
		sb.WriteString("\n")
	}

	if len(analysis.Outline.Structs) > 0 {
		sb.WriteString("Structs/Classes\n\n")
		for _, st := range analysis.Outline.Structs {
			sb.WriteString(fmt.Sprintf("- %s (L%d-L%d)\n", st.Name, st.LineStart, st.LineEnd))
		}
		sb.WriteString("\n")
	}

	if len(analysis.Outline.Interfaces) > 0 {
		sb.WriteString("Interfaces\n\n")
		for _, iface := range analysis.Outline.Interfaces {
			sb.WriteString(fmt.Sprintf("- %s (L%d-L%d)\n", iface.Name, iface.LineStart, iface.LineEnd))
		}
		sb.WriteString("\n")
	}

	if len(analysis.Outline.Variables) > 0 {
		sb.WriteString("Global Variables\n\n")
		for _, v := range analysis.Outline.Variables {
			sb.WriteString(fmt.Sprintf("- %s (L%d)\n", v.Name, v.LineStart))
		}
		sb.WriteString("\n")
	}

	if len(analysis.Outline.Sheets) > 0 {
		sb.WriteString("Sheets\n\n")
		for _, sheet := range analysis.Outline.Sheets {
			sb.WriteString(fmt.Sprintf("- %s (%d rows, %d cols)\n", sheet.Name, sheet.Rows, sheet.Cols))
		}
		sb.WriteString("\n")
	}

	if len(analysis.Outline.Sections) > 0 {
		sb.WriteString("Sections\n\n")
		m.formatSections(&sb, analysis.Outline.Sections, 0)
		sb.WriteString("\n")
	}

	sb.WriteString("\nUsage Tips:\n")
	sb.WriteString("- Use get_block with name or line range to retrieve full content\n")
	sb.WriteString("- Use replace_block to modify specific blocks\n")
	sb.WriteString("- Use search to find patterns\n")

	return sb.String(), nil
}

func (m *AnalysisModifyFile) formatSections(sb *strings.Builder, sections []*Section, indent int) {
	prefix := strings.Repeat("  ", indent)
	for _, s := range sections {
		sb.WriteString(fmt.Sprintf("%s- %s (L%d-L%d)\n", prefix, s.Title, s.LineStart, s.LineEnd))
		m.formatSections(sb, s.SubSections, indent+1)
	}
}

func (m *AnalysisModifyFile) getBlock(path string, params map[string]interface{}) (string, error) {
	name, _ := params["name"].(string)
	blockType, _ := params["block_type"].(string)

	var lineStart, lineEnd int
	if ls, ok := params["line_start"]; ok {
		switch v := ls.(type) {
		case float64:
			lineStart = int(v)
		case int:
			lineStart = v
		}
	}
	if le, ok := params["line_end"]; ok {
		switch v := le.(type) {
		case float64:
			lineEnd = int(v)
		case int:
			lineEnd = v
		}
	}

	if name == "" && lineStart == 0 {
		return "", fmt.Errorf("need either 'name' or 'line_start' parameter")
	}

	ext := strings.ToLower(filepath.Ext(path))
	binaryFormats := map[string]bool{
		".pdf": true, ".docx": true, ".doc": true,
		".pptx": true, ".ppt": true, ".xlsx": true,
	}

	if binaryFormats[ext] {
		// 对于二进制文档，使用专门的获取逻辑
		if name == "" {
			return "", fmt.Errorf("name parameter is required for binary documents (use 'name' to specify section, table, or image)")
		}
		return m.getBinaryBlock(path, params)
	}

	content, err := os.ReadFile(m.analyzer.resolvePath(path))
	if err != nil {
		return "", err
	}

	lines := strings.Split(string(content), "\n")
	var start, end int

	if name != "" {
		analysis, err := m.analyzer.AnalyzeDocument(context.Background(), path)
		if err != nil {
			return "", err
		}

		found := false
		switch blockType {
		case "function", "":
			for _, fn := range analysis.Functions {
				if fn.Name == name {
					start = fn.LineStart - 1
					end = fn.LineEnd - 1
					found = true
					break
				}
			}
		case "struct":
			for _, st := range analysis.Structs {
				if st.Name == name {
					start = st.LineStart - 1
					end = st.LineEnd - 1
					found = true
					break
				}
			}
		case "interface":
			for _, iface := range analysis.Interfaces {
				if iface.Name == name {
					start = iface.LineStart - 1
					end = iface.LineEnd - 1
					found = true
					break
				}
			}
		case "section":
			start, end = m.findSection(analysis.Outline.Sections, name)
			if start > 0 {
				// findSection returns 0-based? Let's check
				found = true
			}
		}

		if !found {
			// Try fuzzy matching
			for _, fn := range analysis.Functions {
				if strings.Contains(strings.ToLower(fn.Name), strings.ToLower(name)) {
					start = fn.LineStart - 1
					end = fn.LineEnd - 1
					found = true
					break
				}
			}
		}

		if !found {
			return "", fmt.Errorf("block '%s' not found", name)
		}
	} else {
		// Convert 1-indexed to 0-indexed
		start = lineStart - 1
		if start < 0 {
			start = 0
		}
		if lineEnd > 0 {
			end = lineEnd - 1
		} else {
			end = start
		}
		if end >= len(lines) {
			end = len(lines) - 1
		}
	}

	// Ensure bounds
	if start < 0 {
		start = 0
	}
	if start >= len(lines) {
		return "", fmt.Errorf("start line %d out of range (file has %d lines)", start+1, len(lines))
	}
	if end < start {
		end = start
	}
	if end >= len(lines) {
		end = len(lines) - 1
	}

	result := strings.Join(lines[start:end+1], "\n")
	blockName := name
	if blockName == "" {
		blockName = fmt.Sprintf("L%d-L%d", start+1, end+1)
	}

	return fmt.Sprintf("Block: %s\n\n```%s\n%s\n```\n\nToken estimate: ~%d",
		blockName, m.getLanguageHint(path), result, len(result)/4), nil
}

func (m *AnalysisModifyFile) getBinaryBlock(path string, params map[string]interface{}) (string, error) {
	name, _ := params["name"].(string)
	blockType, _ := params["block_type"].(string)

	if name == "" {
		return "", fmt.Errorf("name parameter is required for binary document block retrieval")
	}

	// 先分析文档获取结构
	analysis, err := m.analyzer.AnalyzeDocument(context.Background(), path)
	if err != nil {
		return "", err
	}

	// 根据 block_type 查找对应的内容
	switch blockType {
	case "section", "heading", "slide":
		for _, section := range analysis.Sections {
			if strings.Contains(strings.ToLower(section.Title), strings.ToLower(name)) {
				return m.extractSectionContent(analysis, section), nil
			}
		}
		// 递归搜索子章节
		for _, section := range analysis.Sections {
			if content := m.searchSectionRecursive(section, name); content != "" {
				return content, nil
			}
		}

	case "table":
		for _, table := range analysis.Tables {
			if strings.Contains(strings.ToLower(table.Name), strings.ToLower(name)) {
				return m.formatTableContent(table), nil
			}
		}

	case "image":
		for _, img := range analysis.Images {
			if strings.Contains(strings.ToLower(img.Name), strings.ToLower(name)) {
				return m.formatImageInfo(img), nil
			}
		}

	case "codeblock", "block":
		for _, block := range analysis.CodeBlocks {
			if block.Name == name || strings.Contains(strings.ToLower(block.Name), strings.ToLower(name)) {
				if block.Content != "" {
					return fmt.Sprintf("Block: %s\nType: %s\nLines: %d-%d\n\n%s",
						block.Name, block.Type, block.StartLine, block.EndLine, block.Content), nil
				}
				return fmt.Sprintf("Block: %s\nType: %s\nLines: %d-%d\n(Content not available in preview)",
					block.Name, block.Type, block.StartLine, block.EndLine), nil
			}
		}

	default:
		// 默认搜索所有类型
		for _, section := range analysis.Sections {
			if strings.Contains(strings.ToLower(section.Title), strings.ToLower(name)) {
				return m.extractSectionContent(analysis, section), nil
			}
		}
		for _, table := range analysis.Tables {
			if strings.Contains(strings.ToLower(table.Name), strings.ToLower(name)) {
				return m.formatTableContent(table), nil
			}
		}
	}

	return "", fmt.Errorf("block '%s' not found in document (checked sections, tables, images, codeblocks)", name)
}

// searchSectionRecursive 递归搜索章节
func (m *AnalysisModifyFile) searchSectionRecursive(section *Section, name string) string {
	if strings.Contains(strings.ToLower(section.Title), strings.ToLower(name)) {
		return fmt.Sprintf("Section: %s (Level %d, Lines %d-%d)\n\nContent preview available via analyze command",
			section.Title, section.Level, section.LineStart, section.LineEnd)
	}
	for _, sub := range section.SubSections {
		if content := m.searchSectionRecursive(sub, name); content != "" {
			return content
		}
	}
	return ""
}

// extractSectionContent 提取章节内容
func (m *AnalysisModifyFile) extractSectionContent(analysis *DocumentAnalysis, section *Section) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Section: %s (Level %d)\n", section.Title, section.Level))
	sb.WriteString(fmt.Sprintf("Lines: %d-%d\n", section.LineStart, section.LineEnd))
	sb.WriteString("\n")

	// 对于 Word/PPT 文档，从 CodeBlocks 中提取对应段落
	if analysis.Language == "word" || analysis.Language == "powerpoint" {
		for _, block := range analysis.CodeBlocks {
			if block.StartLine >= section.LineStart && block.EndLine <= section.LineEnd {
				if block.Type == "heading" || block.Type == "paragraph" {
					if block.Content != "" {
						sb.WriteString(block.Content)
						sb.WriteString("\n")
					}
				}
			}
		}
	}

	// 显示子章节列表
	if len(section.SubSections) > 0 {
		sb.WriteString("\nSub-sections:\n")
		for _, sub := range section.SubSections {
			sb.WriteString(fmt.Sprintf("  - %s (Lines %d-%d)\n", sub.Title, sub.LineStart, sub.LineEnd))
		}
	}

	return sb.String()
}

// formatTableContent 格式化表格内容
func (m *AnalysisModifyFile) formatTableContent(table *TableInfo) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Table: %s\n", table.Name))
	sb.WriteString(fmt.Sprintf("Rows: %d\n", table.Rows))
	sb.WriteString(fmt.Sprintf("Columns: %d\n", len(table.Columns)))
	sb.WriteString("\n")

	if len(table.Columns) > 0 {
		sb.WriteString("Columns:\n")
		for i, col := range table.Columns {
			sb.WriteString(fmt.Sprintf("  %d. %s\n", i+1, col))
		}
	}

	sb.WriteString("\nNote: Use 'analyze' action with summary_only=false to get full table data")
	return sb.String()
}

// formatImageInfo 格式化图片信息
func (m *AnalysisModifyFile) formatImageInfo(img *ImageInfo) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Image: %s\n", img.Name))
	sb.WriteString(fmt.Sprintf("Type: %s\n", img.Type))
	if img.Width > 0 {
		sb.WriteString(fmt.Sprintf("Dimensions: %dx%d pixels\n", img.Width, img.Height))
	}
	sb.WriteString("\n(Image binary data not included in analysis)")
	return sb.String()
}

func (m *AnalysisModifyFile) findSection(sections []*Section, name string) (int, int) {
	for _, s := range sections {
		if strings.EqualFold(s.Title, name) || strings.Contains(strings.ToLower(s.Title), strings.ToLower(name)) {
			return s.LineStart - 1, s.LineEnd
		}
		if subStart, subEnd := m.findSection(s.SubSections, name); subStart > 0 {
			return subStart, subEnd
		}
	}
	return 0, 0
}

func (m *AnalysisModifyFile) getLanguageHint(path string) string {
	ext := filepath.Ext(path)
	switch ext {
	case ".go":
		return "go"
	case ".py":
		return "python"
	case ".js":
		return "javascript"
	case ".ts":
		return "typescript"
	case ".md", ".markdown":
		return "markdown"
	case ".json":
		return "json"
	case ".yaml", ".yml":
		return "yaml"
	case ".csv":
		return "csv"
	default:
		return "text"
	}
}

func (da *DocAnalyzer) analyzeExcelFile(filePath string) (*DocumentAnalysis, error) {
	f, err := excelize.OpenFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to open Excel file: %w", err)
	}
	defer f.Close()

	analysis := &DocumentAnalysis{
		Language: "excel",
		Sheets:   make([]*SheetInfo, 0),
		Tables:   make([]*TableInfo, 0),
	}

	sheets := f.GetSheetList()
	for _, sheet := range sheets {
		rows, err := f.Rows(sheet)
		if err != nil {
			continue
		}

		rowCount := 0
		colCount := 0
		for rows.Next() {
			row, err := rows.Columns()
			if err == nil && len(row) > colCount {
				colCount = len(row)
			}
			rowCount++
		}
		rows.Close()

		sheetInfo := &SheetInfo{
			Name: sheet,
			Rows: rowCount,
			Cols: colCount,
		}
		analysis.Sheets = append(analysis.Sheets, sheetInfo)

		if rowCount > 0 {
			rows, _ := f.Rows(sheet)
			if rows.Next() {
				headers, _ := rows.Columns()
				analysis.Tables = append(analysis.Tables, &TableInfo{
					Name:    sheet,
					Columns: headers,
					Rows:    rowCount - 1,
				})
			}
			rows.Close()
		}
	}

	analysis.Summary = &DocSummary{
		Title: filepath.Base(filePath),
	}
	analysis.Outline = &DocOutline{
		Sheets: analysis.Sheets,
	}
	analysis.TokenEstimate = len(analysis.Sheets) * 100

	return analysis, nil
}

func (da *DocAnalyzer) analyzeCSVFile(filePath string, content []byte) (*DocumentAnalysis, error) {
	analysis := &DocumentAnalysis{
		Language: "csv",
	}

	lines := strings.Split(string(content), "\n")
	if len(lines) == 0 {
		return analysis, nil
	}

	// 解析 CSV（简单处理，支持引号内的逗号较复杂，先做基础版）
	parseRow := func(line string) []string {
		// 基础 CSV 解析，按逗号分割，去除引号
		var row []string
		var field strings.Builder
		inQuote := false

		for _, ch := range line {
			switch ch {
			case '"':
				inQuote = !inQuote
			case ',':
				if !inQuote {
					row = append(row, strings.TrimSpace(field.String()))
					field.Reset()
				} else {
					field.WriteRune(ch)
				}
			default:
				field.WriteRune(ch)
			}
		}
		row = append(row, strings.TrimSpace(field.String()))
		return row
	}

	// 分析表头
	headers := parseRow(lines[0])

	// 分析列类型（采样前100行或整个文件）
	columnTypes := make([]string, len(headers))
	columnSamples := make([][]string, len(headers))

	sampleLimit := 100
	dataStart := 1
	dataRows := 0

	for i := dataStart; i < len(lines) && i-dataStart < sampleLimit; i++ {
		if strings.TrimSpace(lines[i]) == "" {
			continue
		}
		row := parseRow(lines[i])
		dataRows++
		for colIdx, val := range row {
			if colIdx >= len(columnSamples) {
				continue
			}
			if len(columnSamples[colIdx]) < 3 {
				columnSamples[colIdx] = append(columnSamples[colIdx], val)
			}
			// 类型推断
			if columnTypes[colIdx] == "" {
				if _, err := strconv.Atoi(strings.TrimSpace(val)); err == nil {
					columnTypes[colIdx] = "integer"
				} else if _, err := strconv.ParseFloat(strings.TrimSpace(val), 64); err == nil {
					columnTypes[colIdx] = "float"
				} else if strings.HasPrefix(strings.TrimSpace(val), "true") || strings.HasPrefix(strings.TrimSpace(val), "false") {
					columnTypes[colIdx] = "boolean"
				} else {
					columnTypes[colIdx] = "string"
				}
			}
		}
	}

	// 实际总行数统计
	totalRows := 0
	for i := dataStart; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) != "" {
			totalRows++
		}
	}

	// 构建前3行示例数据
	previewRows := make([][]string, 0)
	for i := dataStart; i < len(lines) && i-dataStart < 3; i++ {
		if strings.TrimSpace(lines[i]) != "" {
			previewRows = append(previewRows, parseRow(lines[i]))
		}
	}

	analysis.Summary = &DocSummary{
		Title:      filepath.Base(filePath),
		TotalLines: len(lines),
	}

	// 构建 CSV 特有信息
	analysis.Tables = []*TableInfo{
		{
			Name:    filepath.Base(filePath),
			Columns: headers,
			Rows:    totalRows,
		},
	}

	// 添加 CSV 元数据到 summary
	analysis.Summary.KeyFeatures = []string{
		fmt.Sprintf("Rows: %d", totalRows),
		fmt.Sprintf("Columns: %d", len(headers)),
	}

	// 构建详细的列信息（可以通过 Metadata 存储）
	analysis.Summary.MainPurpose = "CSV Data File"

	// 将详细列信息存入 summary 的额外字段（需要扩展 DocSummary 结构）
	colInfo := make([]string, 0)
	for i, h := range headers {
		colType := columnTypes[i]
		if colType == "" {
			colType = "unknown"
		}
		colInfo = append(colInfo, fmt.Sprintf("  - %s (%s)", h, colType))
	}

	// 存储到扩展信息（可以暂时放在 KeyFeatures 中）
	analysis.Summary.KeyFeatures = append(analysis.Summary.KeyFeatures, "\nColumns:")
	analysis.Summary.KeyFeatures = append(analysis.Summary.KeyFeatures, colInfo...)

	// 添加前3行示例
	analysis.Summary.KeyFeatures = append(analysis.Summary.KeyFeatures, "\nPreview (first 3 rows):")
	for _, row := range previewRows {
		previewStr := strings.Join(row, ", ")
		if len(previewStr) > 100 {
			previewStr = previewStr[:100] + "..."
		}
		analysis.Summary.KeyFeatures = append(analysis.Summary.KeyFeatures, fmt.Sprintf("  [%s]", previewStr))
	}

	// analysis.Outline = &DocOutline{} // CSV 不需要详细 outline

	return analysis, nil
}
func (da *DocAnalyzer) analyzeTextFile(filePath string, content []byte) (*DocumentAnalysis, error) {
	analysis := &DocumentAnalysis{
		Language: "text",
	}

	lines := strings.Split(string(content), "\n")

	sections := make([]*Section, 0)
	headingRegex := regexp.MustCompile(`^(?:#{1,6}\s+|(?:第[一二三四五六七八九十]+章|Chapter\s+\d+)\s*)(.*)$`)

	for i, line := range lines {
		if match := headingRegex.FindStringSubmatch(line); match != nil {
			sections = append(sections, &Section{
				Title:     strings.TrimSpace(match[1]),
				LineStart: i + 1,
				Level:     1,
			})
		}
	}

	analysis.Summary = &DocSummary{
		Title:          filepath.Base(filePath),
		TotalLines:     len(lines),
		EffectiveLines: da.countEffectiveLines(lines),
	}
	analysis.Outline = &DocOutline{
		Sections: sections,
	}

	return analysis, nil
}

func (da *DocAnalyzer) analyzeJSONFile(filePath string, content []byte) (*DocumentAnalysis, error) {
	analysis := &DocumentAnalysis{
		Language: "json",
	}

	var data interface{}
	if err := json.Unmarshal(content, &data); err == nil {
		analysis.Summary = &DocSummary{
			Title: filepath.Base(filePath),
		}

		switch v := data.(type) {
		case map[string]interface{}:
			keys := make([]string, 0, len(v))
			for k := range v {
				keys = append(keys, k)
			}
			analysis.Summary.KeyFeatures = keys
		case []interface{}:
			analysis.Summary.KeyFeatures = []string{fmt.Sprintf("Array with %d items", len(v))}
		}
	}

	return analysis, nil
}

//############################################

type PPTXMetadataInfo struct {
	Title      string `json:"title"`
	Author     string `json:"author"`
	Created    string `json:"created"`
	Modified   string `json:"modified"`
	LastAuthor string `json:"last_author"`
}

type PPTXImage struct {
	Name string
	Type string
}

type PPTXMetadata struct {
	Title      string
	Author     string
	Created    string
	Modified   string
	LastAuthor string
}

func (da *DocAnalyzer) parsePPTXAdvanced(path string) (*PPTXDocument, error) {
	r, err := zip.OpenReader(path)
	if err != nil {
		return nil, err
	}
	defer r.Close()

	doc := &PPTXDocument{
		Slides: make([]PPTXSlide, 0),
		Images: make([]PPTXImage, 0),
	}

	// 收集所有幻灯片文件
	slideFiles := make(map[int]*zip.File)

	for _, f := range r.File {
		if strings.HasPrefix(f.Name, "ppt/slides/slide") && strings.HasSuffix(f.Name, ".xml") {
			re := regexp.MustCompile(`slide(\d+)\.xml`)
			matches := re.FindStringSubmatch(f.Name)
			if len(matches) == 2 {
				num, err := strconv.Atoi(matches[1])
				if err == nil {
					slideFiles[num] = f
				}
			}
		}

		if strings.HasPrefix(f.Name, "ppt/media/") {
			ext := filepath.Ext(f.Name)
			doc.Images = append(doc.Images, PPTXImage{
				Name: filepath.Base(f.Name),
				Type: strings.TrimPrefix(ext, "."),
			})
		}

		if f.Name == "docProps/core.xml" {
			da.parsePPTXMetadata(f, &doc.Metadata)
		}
	}

	if len(slideFiles) == 0 {
		return nil, fmt.Errorf("no slides found in PPTX file")
	}

	// 找到最大幻灯片编号
	maxSlideNum := 0
	for num := range slideFiles {
		if num > maxSlideNum {
			maxSlideNum = num
		}
	}

	// 按编号顺序解析幻灯片
	for i := 1; i <= maxSlideNum; i++ {
		if f, ok := slideFiles[i]; ok {
			slide := da.parsePPTXSlide(f, i)
			if slide != nil {
				doc.Slides = append(doc.Slides, *slide) // 现在类型匹配了
			}
		}
	}

	return doc, nil
}

func (da *DocAnalyzer) parsePPTXSlide(file *zip.File, number int) *PPTXSlide {
	rc, err := file.Open()
	if err != nil {
		da.logger.Printf("Warning: failed to open slide %d: %v", number, err)
		return nil
	}
	defer rc.Close()

	slide := &PPTXSlide{
		Number:       number,
		Content:      make([]string, 0),
		BulletPoints: make([]string, 0),
	}

	decoder := xml.NewDecoder(rc)
	var currentText strings.Builder
	var inPara bool

	for {
		t, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			da.logger.Printf("Warning: error parsing slide %d: %v", number, err)
			return slide
		}

		switch elem := t.(type) {
		case xml.StartElement:
			switch elem.Name.Local {
			case "p":
				inPara = true
				currentText.Reset()
			case "t", "a:t":
				var text string
				if err := decoder.DecodeElement(&text, &elem); err == nil {
					currentText.WriteString(text)
				}
			case "br", "a:br":
				currentText.WriteString("\n")
			case "tab", "a:tab":
				currentText.WriteString("  ")
			}

		case xml.EndElement:
			switch elem.Name.Local {
			case "p":
				if inPara {
					text := strings.TrimSpace(currentText.String())
					if text != "" {
						if slide.Title == "" && len(slide.Content) == 0 && len(text) < 100 {
							slide.Title = text
						} else {
							if da.isBulletPoint(text) {
								slide.BulletPoints = append(slide.BulletPoints, text)
							} else {
								slide.Content = append(slide.Content, text)
							}
						}
					}
					inPara = false
				}
			}
		}
	}

	if slide.Title == "" && len(slide.Content) > 0 {
		slide.Title = slide.Content[0]
		if len(slide.Title) > 60 {
			slide.Title = slide.Title[:57] + "..."
		}
	}

	return slide
}

func (da *DocAnalyzer) parsePPTXNotes(file *zip.File, doc *PPTXDocumentInfo) {
	rc, err := file.Open()
	if err != nil {
		return
	}
	defer rc.Close()

	decoder := xml.NewDecoder(rc)

	// 提取幻灯片编号
	re := regexp.MustCompile(`notesSlide(\d+)\.xml`)
	matches := re.FindStringSubmatch(file.Name)
	if len(matches) != 2 {
		return
	}
	slideNum, _ := strconv.Atoi(matches[1])

	var noteText strings.Builder

	for {
		t, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return
		}

		if elem, ok := t.(xml.StartElement); ok && (elem.Name.Local == "t" || elem.Name.Local == "a:t") {
			var text string
			if err := decoder.DecodeElement(&text, &elem); err == nil {
				noteText.WriteString(text)
				noteText.WriteString(" ")
			}
		}
	}

	if noteText.Len() > 0 {
		doc.Notes = append(doc.Notes, PPTXNoteInfo{
			SlideNum: slideNum,
			Text:     noteText.String(),
		})
	}
}

func (da *DocAnalyzer) analyzePPTFileFallback(filePath string, analysis *DocumentAnalysis) (*DocumentAnalysis, error) {
	slides, err := readPPTXText(filePath)
	if err != nil {
		analysis.Summary = &DocSummary{
			Title: filepath.Base(filePath),
			KeyFeatures: []string{
				fmt.Sprintf("Size: %d bytes", analysis.Size),
				"Format: PPTX",
				"Text extraction failed",
			},
		}
		return analysis, nil
	}

	var content strings.Builder
	for num, text := range slides {
		content.WriteString(fmt.Sprintf("Slide %d: %s\n\n", num, text))
	}

	analysis.Summary = &DocSummary{
		Title:          filepath.Base(filePath),
		TotalLines:     len(slides),
		EffectiveLines: len(slides),
	}

	return analysis, nil
}

// 构建 PPTX 预览
func (da *DocAnalyzer) buildPPTXPreview(slides []PPTXSlide) string {
	var preview strings.Builder

	// 显示前 5 张幻灯片的预览
	for i, slide := range slides {
		if i >= 5 {
			break
		}

		preview.WriteString(fmt.Sprintf("\nSlide %d: %s\n", slide.Number, slide.Title))

		// 显示前 3 个项目符号或内容
		itemsShown := 0
		for _, bp := range slide.BulletPoints {
			if itemsShown >= 3 {
				break
			}
			if len(bp) > 80 {
				bp = bp[:77] + "..."
			}
			preview.WriteString(fmt.Sprintf("  • %s\n", bp))
			itemsShown++
		}

		if len(slide.BulletPoints) > 3 {
			preview.WriteString(fmt.Sprintf("  ... and %d more items\n", len(slide.BulletPoints)-3))
		}
	}

	return preview.String()
}

// 解析 PPTX 元数据
func (da *DocAnalyzer) parsePPTXMetadata(file *zip.File, metadata *PPTXMetadata) error {
	rc, err := file.Open()
	if err != nil {
		return err
	}
	defer rc.Close()

	decoder := xml.NewDecoder(rc)

	for {
		t, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}

		switch elem := t.(type) {
		case xml.StartElement:
			var content string
			if err := decoder.DecodeElement(&content, &elem); err == nil {
				switch elem.Name.Local {
				case "title":
					metadata.Title = content
				case "creator":
					metadata.Author = content
				case "created":
					metadata.Created = content
				case "modified":
					metadata.Modified = content
				case "lastModifiedBy":
					metadata.LastAuthor = content
				}
			}
		}
	}

	return nil
}

// 判断是否是项目符号
func (da *DocAnalyzer) isBulletPoint(text string) bool {
	// 项目符号模式
	patterns := []string{
		`^[•·▪➢➣➤►○●■□]\s+`,     // 符号项目符号
		`^\d+\.\s+`,             // 数字编号
		`^[a-zA-Z]\.\s+`,        // 字母编号
		`^[一二三四五六七八九十]+[、.]\s+`, // 中文编号
		`^-\s+`,  // 短横线
		`^\*\s+`, // 星号
	}

	for _, pattern := range patterns {
		if matched, _ := regexp.MatchString(pattern, text); matched {
			return true
		}
	}

	return false
}

func (da *DocAnalyzer) analyzePPTFile(filePath string) (*DocumentAnalysis, error) {
	analysis := &DocumentAnalysis{
		Language: "powerpoint",
		Sections: make([]*Section, 0),
		Images:   make([]*ImageInfo, 0),
	}

	fileInfo, err := os.Stat(filePath)
	if err == nil {
		analysis.Size = fileInfo.Size()
	}

	// 使用改进的解析器
	pptDoc, err := da.parsePPTXAdvanced(filePath)
	if err != nil {
		da.logger.Printf("Warning: advanced PPTX parsing failed: %v", err)
		return da.analyzePPTFileFallback(filePath, analysis)
	}

	// 构建章节（每张幻灯片作为一个章节）
	sections := make([]*Section, 0)
	for i, slide := range pptDoc.Slides {
		section := &Section{
			Title:     fmt.Sprintf("Slide %d", slide.Number),
			Level:     1,
			LineStart: i + 1,
			LineEnd:   i + 1,
		}

		// 使用幻灯片的标题作为章节名
		if slide.Title != "" {
			section.Title = fmt.Sprintf("Slide %d: %s", slide.Number, slide.Title)
		}

		// 添加子章节（基于项目符号）
		if len(slide.BulletPoints) > 0 {
			for _, bp := range slide.BulletPoints {
				subSection := &Section{
					Title: bp,
					Level: 2,
				}
				section.SubSections = append(section.SubSections, subSection)
			}
		}

		sections = append(sections, section)
	}

	analysis.Sections = sections // 赋值给 Sections

	// 提取图片信息
	for _, img := range pptDoc.Images {
		analysis.Images = append(analysis.Images, &ImageInfo{
			Name: img.Name,
			Type: img.Type,
		})
	}

	// 构建摘要
	summary := &DocSummary{
		Title:          filepath.Base(filePath),
		TotalLines:     len(pptDoc.Slides),
		EffectiveLines: len(pptDoc.Slides),
		KeyFeatures: []string{
			fmt.Sprintf("Size: %d bytes", analysis.Size),
			"Format: Microsoft PowerPoint",
			fmt.Sprintf("Slides: %d", len(pptDoc.Slides)),
			fmt.Sprintf("Images: %d", len(pptDoc.Images)),
			fmt.Sprintf("Total content length: %d characters", pptDoc.TotalChars),
		},
	}

	// 添加元数据
	if pptDoc.Metadata.Title != "" {
		summary.Title = pptDoc.Metadata.Title
		summary.KeyFeatures = append(summary.KeyFeatures, fmt.Sprintf("Title: %s", pptDoc.Metadata.Title))
	}
	if pptDoc.Metadata.Author != "" {
		summary.KeyFeatures = append(summary.KeyFeatures, fmt.Sprintf("Author: %s", pptDoc.Metadata.Author))
	}

	// 构建预览
	preview := da.buildPPTXPreview(pptDoc.Slides)
	if preview != "" {
		summary.KeyFeatures = append(summary.KeyFeatures, fmt.Sprintf("\nContent preview:\n%s", preview))
	}

	analysis.Summary = summary
	analysis.Outline = &DocOutline{
		Sections: sections,
		Images:   analysis.Images,
	}

	analysis.TokenEstimate = pptDoc.TotalChars / 4

	return analysis, nil
}

// ----------------------------------------------------------------------------------------
// PPTX 文档结构
type PPTXDocument struct {
	Slides     []PPTXSlide
	Images     []PPTXImage
	Metadata   PPTXMetadata
	TotalChars int
}

type PPTXSlide struct {
	Number       int
	Title        string
	Subtitle     string
	Content      []string
	BulletPoints []string
	Notes        string
	Layout       string
	Transition   string
	Duration     int
	HasAnimation bool
}

type PPTXDocumentInfo struct {
	Slides     []PPTXSlideInfo
	Sections   []*Section
	Images     []*ImageInfo
	Metadata   PPTXMetadataInfo
	Notes      []PPTXNoteInfo
	TotalChars int
	TotalWords int
}

type PPTXSlideInfo struct {
	Number       int
	Title        string
	Subtitle     string
	Content      []PPTXContentItem
	Layout       string
	Notes        string
	Transition   string
	Duration     int
	HasAnimation bool
}

type PPTXNoteInfo struct {
	SlideNum int
	Text     string
}

type PPTXContentItem struct {
	Type  string // "text", "bullet", "image", "table", "chart"
	Level int    // 缩进级别
	Text  string
}

type PPTXImageInfo struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

type PPTXShape struct {
	TextBody PPTXTextBody `xml:"txBody"`
}

type PPTXTextBody struct {
	Paragraphs []PPTXParagraph `xml:"p"`
}

type PPTXParagraph struct {
	Runs []PPTXRun `xml:"r"`
}

type PPTXRun struct {
	Text string `xml:"t"`
}

// 读取 PPTX 的所有幻灯片文本
func readPPTXText(path string) (map[int]string, error) {
	r, err := zip.OpenReader(path)
	if err != nil {
		return nil, err
	}
	defer r.Close()

	slides := make(map[int]string)
	slidePattern := regexp.MustCompile(`ppt/slides/slide(\d+)\.xml`)

	for _, f := range r.File {
		matches := slidePattern.FindStringSubmatch(f.Name)
		if len(matches) != 2 {
			continue
		}

		slideNum, _ := strconv.Atoi(matches[1])

		rc, err := f.Open()
		if err != nil {
			continue
		}
		defer rc.Close()

		var content strings.Builder
		decoder := xml.NewDecoder(rc)

		for {
			t, err := decoder.Token()
			if err == io.EOF {
				break
			}
			if err != nil {
				break
			}

			// 提取 <a:t> 或 <t> 标签中的文本
			if elem, ok := t.(xml.StartElement); ok && (elem.Name.Local == "t" || elem.Name.Local == "a:t") {
				var txt string
				if err := decoder.DecodeElement(&txt, &elem); err == nil {
					if strings.TrimSpace(txt) != "" {
						content.WriteString(txt)
						content.WriteString(" ")
					}
				}
			}
		}

		slides[slideNum] = content.String()
	}

	return slides, nil
}
