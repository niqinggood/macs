package middleware

import (
	"crypto/md5"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"unicode/utf8"

	"mac/internal/projectctx"
	"mac/internal/toolcontract"
)

// ToolOutputBudgetConfig controls per-tool output persistence.
// When enabled, tool outputs exceeding MaxChars are saved to disk
// and replaced with a preview stub, rather than truncated.
type ToolOutputBudgetConfig struct {
	Enabled       bool   `json:"enabled" yaml:"enabled"`
	MaxChars      int    `json:"max_chars" yaml:"max_chars"`             // default 8000
	PreviewHead   int    `json:"preview_head" yaml:"preview_head"`       // chars to show at start (default 500)
	PreviewTail   int    `json:"preview_tail" yaml:"preview_tail"`       // chars to show at end (default 500)
	MaxResultsDir string `json:"max_results_dir" yaml:"max_results_dir"` // where to store overflow files
}

func DefaultToolOutputBudgetConfig() ToolOutputBudgetConfig {
	return ToolOutputBudgetConfig{
		Enabled:     false,
		MaxChars:    8000,
		PreviewHead: 500,
		PreviewTail: 500,
	}
}

// ToolOutputBudgetMiddleware saves oversized tool results to disk and
// replaces them with a preview stub so the LLM can decide whether to
// read the full output, rather than blindly truncating.
type ToolOutputBudgetMiddleware struct {
	Base
	config       ToolOutputBudgetConfig
	filesWritten map[string]int // sessionID → count
	mu           sync.Mutex
}

func NewToolOutputBudgetMiddleware(cfg ToolOutputBudgetConfig) *ToolOutputBudgetMiddleware {
	if !cfg.Enabled {
		return nil
	}
	if cfg.MaxChars <= 0 {
		cfg.MaxChars = 8000
	}
	if cfg.PreviewHead <= 0 {
		cfg.PreviewHead = 500
	}
	if cfg.PreviewTail <= 0 {
		cfg.PreviewTail = 500
	}
	if cfg.MaxResultsDir == "" {
		cfg.MaxResultsDir = "storage/tool_results"
	}
	return &ToolOutputBudgetMiddleware{
		Base:         NewBase("tool_output_budget"),
		config:       cfg,
		filesWritten: make(map[string]int),
	}
}

func (m *ToolOutputBudgetMiddleware) AfterTool(ctx *Context) error {
	if m == nil || ctx.ToolResult == nil {
		return nil
	}

	result := ctx.ToolResult.Content
	ctx.ToolResult.Content = result
	origLen := utf8.RuneCountInString(result)
	if origLen <= m.config.MaxChars {
		return nil
	}
	// use_skill returns executable instructions. The normalizer deliberately
	// keeps a bounded Skill inline so the model receives the Runtime Directory
	// and exact scaffold commands in the same tool turn. Offloading it again at
	// the generic middleware threshold defeats use_skill and causes reload loops.
	if strings.EqualFold(strings.TrimSpace(ctx.ToolCall.Name), "use_skill") &&
		origLen <= toolcontract.DefaultToolResultNormalizerConfig().SkillMaxInlineChars {
		return nil
	}

	sessionID := m.sessionKey(ctx)
	if sessionID == "" {
		return nil
	}

	resultsRoot, displayRoot := m.resultsRoot(ctx)
	safeSessionID := toolSanitizeFilename(sessionID)
	dir := filepath.Join(resultsRoot, safeSessionID)
	if err := os.MkdirAll(dir, 0755); err != nil {
		ctx.ToolResult.Content = m.fallbackTruncate(result, origLen)
		return nil
	}

	m.mu.Lock()
	m.filesWritten[sessionID]++
	fileNumber := m.filesWritten[sessionID]
	m.mu.Unlock()
	filename := fmt.Sprintf("%s_%d.txt", toolSanitizeFilename(ctx.ToolCall.Name), fileNumber)
	filePath := filepath.Join(dir, filename)

	if err := os.WriteFile(filePath, []byte(result), 0644); err != nil {
		ctx.ToolResult.Content = m.fallbackTruncate(result, origLen)
		return nil
	}

	displayPath := filepath.Join(displayRoot, safeSessionID, filename)
	ctx.ToolResult.Content = m.buildPreviewStub(result, origLen, filepath.ToSlash(displayPath))
	ctx.SetMeta("tool_output_budget.file", filePath)
	ctx.SetMeta("tool_output_budget.original_len", origLen)

	return nil
}

func (m *ToolOutputBudgetMiddleware) resultsRoot(ctx *Context) (string, string) {
	configured := filepath.Clean(m.config.MaxResultsDir)
	if filepath.IsAbs(configured) || ctx == nil {
		return configured, configured
	}
	workspace := strings.TrimSpace(projectctx.ResolvedWorkspace(ctx.Context))
	if workspace == "" {
		return configured, configured
	}
	return filepath.Join(workspace, configured), configured
}

func (m *ToolOutputBudgetMiddleware) sessionKey(ctx *Context) string {
	if ctx.SessionID != "" {
		return ctx.SessionID
	}
	return ctx.TaskID
}

func (m *ToolOutputBudgetMiddleware) buildPreviewStub(full string, origLen int, filePath string) string {
	runes := []rune(full)
	head := m.config.PreviewHead
	tail := m.config.PreviewTail

	var headText, tailText string
	if head > len(runes) {
		headText = string(runes)
	} else {
		headText = string(runes[:head])
	}
	if tail < len(runes) {
		start := len(runes) - tail
		if start < head {
			start = head
		}
		tailText = string(runes[start:])
	}

	omitted := origLen - m.config.PreviewHead - m.config.PreviewTail
	if omitted < 0 {
		omitted = 0
	}

	return fmt.Sprintf(
		`[Output saved to %s (%s, ~%d tokens)]

Preview (first %d chars):
%s

[... %d chars omitted ...]

Preview (last %d chars):
%s

Use read_file to read the full output if needed.`,
		filePath,
		formatOutputSize(origLen),
		origLen/4,
		m.config.PreviewHead,
		headText,
		omitted,
		m.config.PreviewTail,
		tailText,
	)
}

func (m *ToolOutputBudgetMiddleware) fallbackTruncate(full string, origLen int) string {
	runes := []rune(full)
	keep := m.config.PreviewHead
	if keep > len(runes) {
		keep = len(runes)
	}
	head := string(runes[:keep])
	footer := fmt.Sprintf(
		"\n\n---\n[truncated %s → %.1fKB (disk unavailable)]",
		formatOutputSize(origLen),
		float64(keep)/1024,
	)
	return head + footer
}

func (m *ToolOutputBudgetMiddleware) Cleanup(sessionID string) error {
	if m == nil || sessionID == "" {
		return nil
	}
	return os.RemoveAll(filepath.Join(m.config.MaxResultsDir, sessionID))
}

// --- helpers ---

func formatOutputSize(chars int) string {
	if chars < 1024 {
		return fmt.Sprintf("%d chars", chars)
	}
	return fmt.Sprintf("%.1fKB", float64(chars)/1024)
}

func toolSanitizeFilename(name string) string {
	r := strings.NewReplacer(
		"/", "_", "\\", "_", ":", "_", "*", "_", "?", "_",
		"\"", "_", "<", "_", ">", "_", "|", "_", " ", "_",
	)
	return r.Replace(name)
}

func fastHash(s string) string {
	h := md5.Sum([]byte(s))
	return fmt.Sprintf("%x", h[:8])
}
