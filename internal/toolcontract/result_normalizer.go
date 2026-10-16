package toolcontract

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/text/encoding/simplifiedchinese"
	textunicode "golang.org/x/text/encoding/unicode"
)

var researchDiscoveryResultLine = regexp.MustCompile(`^\d+\.`)

type ToolResultNormalizerConfig struct {
	MaxInlineChars      int
	SkillMaxInlineChars int
	KeyLineLimit        int
	KeyOutputMaxChars   int
	HeadLines           int
	TailLines           int
}

type ToolResultNormalizer struct {
	workspace string
	config    ToolResultNormalizerConfig
}

func DefaultToolResultNormalizerConfig() ToolResultNormalizerConfig {
	return ToolResultNormalizerConfig{
		MaxInlineChars:      8000,
		SkillMaxInlineChars: 48000,
		KeyLineLimit:        16,
		KeyOutputMaxChars:   4000,
		HeadLines:           12,
		TailLines:           12,
	}
}

func NewToolResultNormalizer(workspace string) *ToolResultNormalizer {
	return NewToolResultNormalizerWithConfig(workspace, DefaultToolResultNormalizerConfig())
}

func NewToolResultNormalizerWithConfig(workspace string, cfg ToolResultNormalizerConfig) *ToolResultNormalizer {
	if strings.TrimSpace(workspace) == "" {
		workspace = "."
	}
	def := DefaultToolResultNormalizerConfig()
	if cfg.MaxInlineChars <= 0 {
		cfg.MaxInlineChars = def.MaxInlineChars
	}
	if cfg.SkillMaxInlineChars <= 0 {
		cfg.SkillMaxInlineChars = def.SkillMaxInlineChars
	}
	if cfg.KeyLineLimit <= 0 {
		cfg.KeyLineLimit = def.KeyLineLimit
	}
	if cfg.KeyOutputMaxChars <= 0 {
		cfg.KeyOutputMaxChars = def.KeyOutputMaxChars
	}
	if cfg.HeadLines <= 0 {
		cfg.HeadLines = def.HeadLines
	}
	if cfg.TailLines <= 0 {
		cfg.TailLines = def.TailLines
	}
	return &ToolResultNormalizer{workspace: workspace, config: cfg}
}

func (n *ToolResultNormalizer) Normalize(toolName, content string) string {
	return n.normalize(toolName, content, nil)
}

// NormalizeWithOutcome compacts output using the registry's known execution
// outcome instead of inferring status from untrusted payload text.
func (n *ToolResultNormalizer) NormalizeWithOutcome(toolName, content string, succeeded bool) string {
	return n.normalize(toolName, content, &succeeded)
}

func (n *ToolResultNormalizer) normalize(toolName, content string, executionSucceeded *bool) string {
	if n == nil || strings.TrimSpace(content) == "" {
		return content
	}
	rawContent := []byte(content)
	if hasKnownBinaryToolMagic(rawContent) {
		return n.normalizeBinaryOutput(toolName, rawContent)
	}
	if decoded, ok := decodeTextToolOutput(rawContent); ok {
		content = decoded
	} else if looksLikeBinaryToolOutput(rawContent) {
		return n.normalizeBinaryOutput(toolName, rawContent)
	}
	if isNormalizedToolResult(content) {
		return content
	}
	origLen := utf8.RuneCountInString(content)
	// Built-in skills are executable instructions, not ordinary noisy tool
	// output.  Keeping a bounded, medium-sized skill intact preserves its
	// Runtime Directory and exact commands.  Compacting these documents at the
	// generic 8K limit previously made presentation skills look failed and left
	// the model without the scaffold/render entry points it had just requested.
	if strings.EqualFold(strings.TrimSpace(toolName), "use_skill") && origLen <= n.config.SkillMaxInlineChars {
		return content
	}
	if origLen <= n.config.MaxInlineChars {
		return content
	}

	_, displayPath, err := n.writeFullResult(toolName, content)
	if err != nil {
		return n.inlineCompact(toolName, content, origLen, fmt.Sprintf("failed to save full output: %v", err))
	}

	failed := ToolResultLooksFailedForTool(toolName, content)
	if executionSucceeded != nil {
		failed = !*executionSucceeded
	}
	keyOutput := ""
	if strings.EqualFold(strings.TrimSpace(toolName), "use_skill") && !failed {
		keyOutput = strings.TrimSpace(n.extractSkillKeyOutput(content))
	} else if strings.EqualFold(strings.TrimSpace(toolName), "web_fetch") && !failed {
		keyOutput = strings.TrimSpace(n.extractWebFetchKeyOutput(content))
	} else {
		keyOutput = strings.TrimSpace(n.extractKeyOutput(content))
	}
	if keyOutput == "" {
		keyOutput = strings.TrimSpace(n.headTailOutput(content))
	}
	keyOutput = n.limitPreview(keyOutput)
	status := "compacted"
	if failed {
		status = "failed_compacted"
	}

	var b strings.Builder
	b.WriteString("Tool Result\n")
	b.WriteString("normalized_by: openkitty_tool_result_normalizer\n")
	b.WriteString(fmt.Sprintf("tool: %s\n", toolName))
	b.WriteString(fmt.Sprintf("status: %s\n", status))
	b.WriteString(fmt.Sprintf("summary: output exceeded %d chars; full output saved for targeted inspection.\n", n.config.MaxInlineChars))
	b.WriteString(fmt.Sprintf("details_file: %s\n", displayPath))
	b.WriteString(fmt.Sprintf("original_chars: %d\n", origLen))
	if keyOutput != "" {
		b.WriteString("key_output:\n")
		b.WriteString(keyOutput)
		if !strings.HasSuffix(keyOutput, "\n") {
			b.WriteString("\n")
		}
	}
	return b.String()
}

func looksLikeBinaryToolOutput(content []byte) bool {
	trimmed := bytes.TrimSpace(content)
	if len(trimmed) == 0 {
		return false
	}
	if hasKnownBinaryToolMagic(trimmed) {
		return true
	}
	_, text := decodeTextToolOutput(trimmed)
	return !text
}

func hasKnownBinaryToolMagic(content []byte) bool {
	trimmed := bytes.TrimSpace(content)
	for _, magic := range [][]byte{
		[]byte("%PDF-"),
		{0x50, 0x4b, 0x03, 0x04}, // ZIP, including DOCX/XLSX/PPTX
		{0x89, 0x50, 0x4e, 0x47}, // PNG
		{0xff, 0xd8, 0xff},       // JPEG
	} {
		if bytes.HasPrefix(trimmed, magic) {
			return true
		}
	}
	return false
}

// decodeTextToolOutput repairs the encodings commonly produced when a UTF-8
// host wrapper captures Windows child-process output. The wrapper prefix may
// be UTF-8 while cmd/PowerShell appends GBK, so rejecting the whole payload on
// the first invalid byte incorrectly labels ordinary diagnostics as binary.
func decodeTextToolOutput(content []byte) (string, bool) {
	if len(content) == 0 {
		return "", true
	}
	if utf8.Valid(content) {
		text := string(content)
		return text, looksLikeHumanText(text)
	}
	if decoded, ok := decodeUTF16ToolText(content); ok {
		return decoded, true
	}

	firstInvalid := firstInvalidUTF8Byte(content)
	if firstInvalid >= 0 {
		if suffix, err := simplifiedchinese.GB18030.NewDecoder().Bytes(content[firstInvalid:]); err == nil {
			candidate := string(content[:firstInvalid]) + string(suffix)
			if utf8.ValidString(candidate) && looksLikeHumanText(candidate) {
				return candidate, true
			}
		}
	}
	if decoded, err := simplifiedchinese.GB18030.NewDecoder().Bytes(content); err == nil {
		candidate := string(decoded)
		if utf8.ValidString(candidate) && looksLikeHumanText(candidate) {
			return candidate, true
		}
	}
	return "", false
}

func firstInvalidUTF8Byte(content []byte) int {
	for index := 0; index < len(content); {
		_, size := utf8.DecodeRune(content[index:])
		if size == 1 && content[index] >= utf8.RuneSelf {
			return index
		}
		index += size
	}
	return -1
}

func decodeUTF16ToolText(content []byte) (string, bool) {
	if len(content) < 4 || len(content)%2 != 0 {
		return "", false
	}
	littleEndian := bytes.HasPrefix(content, []byte{0xff, 0xfe})
	bigEndian := bytes.HasPrefix(content, []byte{0xfe, 0xff})
	if !littleEndian && !bigEndian {
		evenNUL, oddNUL := 0, 0
		for index, value := range content {
			if value != 0 {
				continue
			}
			if index%2 == 0 {
				evenNUL++
			} else {
				oddNUL++
			}
		}
		pairs := len(content) / 2
		littleEndian = oddNUL > pairs/5
		bigEndian = evenNUL > pairs/5
	}
	if !littleEndian && !bigEndian {
		return "", false
	}
	endian := textunicode.LittleEndian
	if bigEndian {
		endian = textunicode.BigEndian
	}
	decoded, err := textunicode.UTF16(endian, textunicode.UseBOM).NewDecoder().Bytes(content)
	if err != nil {
		return "", false
	}
	text := strings.TrimPrefix(string(decoded), "\ufeff")
	return text, utf8.ValidString(text) && looksLikeHumanText(text)
}

func looksLikeHumanText(content string) bool {
	if content == "" {
		return true
	}
	controls, runes := 0, 0
	for _, value := range content {
		runes++
		if value < 0x20 && value != '\n' && value != '\r' && value != '\t' {
			controls++
		}
	}
	return runes > 0 && controls*50 <= runes
}

func (n *ToolResultNormalizer) normalizeBinaryOutput(toolName string, content []byte) string {
	_, displayPath, err := n.writeFullResult(toolName, string(content))
	var b strings.Builder
	b.WriteString("Tool Result\n")
	b.WriteString("normalized_by: openkitty_tool_result_normalizer\n")
	b.WriteString(fmt.Sprintf("tool: %s\n", toolName))
	b.WriteString("status: unsupported_binary\n")
	b.WriteString(fmt.Sprintf("summary: tool returned %d bytes of binary data; binary content was not exposed to the model.\n", len(content)))
	if err == nil {
		b.WriteString(fmt.Sprintf("details_file: %s\n", displayPath))
		b.WriteString("details_file_scope: operator_debug_only\n")
	} else {
		b.WriteString(fmt.Sprintf("details_file_error: %v\n", err))
	}
	b.WriteString("action: use a content-type-aware extraction tool or return a structured unsupported-format error.\n")
	return b.String()
}

func (n *ToolResultNormalizer) extractWebFetchKeyOutput(content string) string {
	lines := strings.Split(content, "\n")
	selected := make([]string, 0, n.config.KeyLineLimit*2)
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		lower := strings.ToLower(trimmed)
		if strings.HasPrefix(lower, "title:") || strings.HasPrefix(lower, "url:") ||
			strings.HasPrefix(lower, "**source:**") || strings.HasPrefix(lower, "**来源:**") ||
			researchDiscoveryResultLine.MatchString(trimmed) {
			selected = append(selected, trimmed)
			if len(selected) >= n.config.KeyLineLimit*2 {
				break
			}
		}
	}
	parts := make([]string, 0, 2)
	if len(selected) > 0 {
		parts = append(parts, strings.Join(selected, "\n"))
	}
	if headTail := strings.TrimSpace(n.headTailOutput(content)); headTail != "" {
		parts = append(parts, headTail)
	}
	return strings.Join(parts, "\n---\n")
}

func (n *ToolResultNormalizer) writeFullResult(toolName, content string) (string, string, error) {
	root := filepath.Join(n.workspace, ".openkitty", "runtime", "tool-results")
	if err := os.MkdirAll(root, 0755); err != nil {
		return "", "", err
	}
	name := sanitizeResultLogName(toolName)
	if name == "" {
		name = "tool"
	}
	fullPath := filepath.Join(root, fmt.Sprintf("%s_%d.log", name, time.Now().UnixNano()))
	if err := os.WriteFile(fullPath, []byte(content), 0644); err != nil {
		return "", "", err
	}
	displayPath := fullPath
	if rel, err := filepath.Rel(n.workspace, fullPath); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel) {
		displayPath = filepath.ToSlash(rel)
	}
	return fullPath, displayPath, nil
}

func (n *ToolResultNormalizer) inlineCompact(toolName, content string, origLen int, note string) string {
	var b strings.Builder
	b.WriteString("Tool Result\n")
	b.WriteString("normalized_by: openkitty_tool_result_normalizer\n")
	b.WriteString(fmt.Sprintf("tool: %s\n", toolName))
	b.WriteString("status: compacted\n")
	b.WriteString(fmt.Sprintf("summary: output exceeded %d chars; %s.\n", n.config.MaxInlineChars, note))
	b.WriteString(fmt.Sprintf("original_chars: %d\n", origLen))
	b.WriteString("key_output:\n")
	b.WriteString(n.limitPreview(n.headTailOutput(content)))
	return b.String()
}

func (n *ToolResultNormalizer) extractKeyOutput(content string) string {
	lines := strings.Split(content, "\n")
	keywords := []string{
		"error", "failed", "failure", "panic", "fatal", "exception", "traceback",
		"fail:",
		"assertion", "undefined", "cannot find", "not found", "exit status",
		"compilation failed", "build failed", "test failed",
	}
	fileLine := regexp.MustCompile(`(?i)([A-Za-z]:)?[^:\s]+(\.go|\.py|\.js|\.ts|\.tsx|\.jsx|\.java|\.rs|\.cpp|\.c|\.h):\d+`)

	var out []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		lower := strings.ToLower(trimmed)
		match := fileLine.MatchString(trimmed)
		for _, kw := range keywords {
			if strings.Contains(lower, kw) {
				match = true
				break
			}
		}
		if match {
			out = append(out, trimmed)
			if len(out) >= n.config.KeyLineLimit {
				break
			}
		}
	}
	return strings.Join(out, "\n")
}

// extractSkillKeyOutput preserves the executable handoff of an oversized
// skill. Generic error-line extraction is actively harmful here: words such as
// "failed" and "fallback" are common in workflow guidance, while the runtime
// directory and exact commands are what the agent needs to continue.
func (n *ToolResultNormalizer) extractSkillKeyOutput(content string) string {
	lines := strings.Split(content, "\n")
	commandPattern := regexp.MustCompile(`(?i)(?:^|\s)(?:python3?|powershell(?:\.exe)?|pwsh|node|bun|deno|copy-item)\s+`)
	var selected []string
	seen := make(map[string]bool)
	appendLine := func(line string) {
		line = strings.TrimSpace(line)
		if line == "" || seen[line] {
			return
		}
		seen[line] = true
		selected = append(selected, line)
	}
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		lower := strings.ToLower(trimmed)
		if strings.HasPrefix(trimmed, "技能:") || strings.HasPrefix(trimmed, "描述:") ||
			strings.HasPrefix(trimmed, "运行时目录:") || strings.HasPrefix(lower, "runtime directory:") ||
			strings.HasPrefix(trimmed, "路径规则:") || strings.HasPrefix(lower, "path rules:") ||
			commandPattern.MatchString(trimmed) ||
			strings.Contains(lower, "scaffold_task_pptx.py") || strings.Contains(lower, "build_task_pptx.py") ||
			strings.Contains(lower, "render_pptx.ps1") || strings.Contains(lower, "render_pptx.py") {
			appendLine(trimmed)
		}
	}
	if len(selected) == 0 {
		return n.headTailOutput(content)
	}
	return strings.Join(selected, "\n")
}

func (n *ToolResultNormalizer) headTailOutput(content string) string {
	lines := strings.Split(content, "\n")
	if len(lines) <= n.config.HeadLines+n.config.TailLines+1 {
		return content
	}
	head := lines[:n.config.HeadLines]
	tail := lines[len(lines)-n.config.TailLines:]
	var b strings.Builder
	b.WriteString(strings.Join(head, "\n"))
	b.WriteString(fmt.Sprintf("\n... [%d lines omitted] ...\n", len(lines)-n.config.HeadLines-n.config.TailLines))
	b.WriteString(strings.Join(tail, "\n"))
	return b.String()
}

func (n *ToolResultNormalizer) limitPreview(content string) string {
	if n == nil || content == "" {
		return content
	}
	maxChars := n.config.KeyOutputMaxChars
	if maxChars <= 0 || utf8.RuneCountInString(content) <= maxChars {
		return content
	}
	runes := []rune(content)
	if maxChars < 64 {
		return string(runes[:maxChars])
	}
	headLen := maxChars * 2 / 3
	tailLen := maxChars - headLen
	return fmt.Sprintf("%s\n... [preview truncated to %d chars] ...\n%s",
		string(runes[:headLen]),
		maxChars,
		string(runes[len(runes)-tailLen:]),
	)
}

func isNormalizedToolResult(content string) bool {
	if !strings.HasPrefix(content, "Tool Result\n") {
		return false
	}
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "normalized_by: openkitty_tool_result_normalizer" || strings.HasPrefix(trimmed, "details_file: ") {
			return true
		}
	}
	return false
}

func sanitizeResultLogName(value string) string {
	return strings.Map(func(char rune) rune {
		if char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || char == '-' || char == '_' {
			return char
		}
		return '_'
	}, value)
}
