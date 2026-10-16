package toolcontract

import (
	"context"
	"fmt"
	"log"
	"strings"
	"unicode/utf8"
)

// ====================== Tool Compression Middleware ======================

// CompressorConfig controls output compression per tool or globally.
type CompressorConfig struct {
	MaxChars   int  `json:"max_chars" yaml:"max_chars"`     // hard truncation limit
	SummaryLen int  `json:"summary_len" yaml:"summary_len"` // chars to keep at start before truncation marker
	Enabled    bool `json:"enabled" yaml:"enabled"`
}

var defaultCompressorConfig = CompressorConfig{
	MaxChars:   8000,
	SummaryLen: 1500,
	Enabled:    true,
}

// CompressedTool wraps any Tool and auto-compresses large outputs.
type CompressedTool struct {
	inner              Tool
	config             CompressorConfig
	logger             *log.Logger
	readOnlyClassifier func(string) bool
}

// WrapWithCompressor wraps a tool with output compression.
func WrapWithCompressor(tool Tool, cfg CompressorConfig, logger *log.Logger, classifiers ...func(string) bool) Tool {
	if !cfg.Enabled {
		return tool
	}
	if cfg.MaxChars <= 0 {
		cfg.MaxChars = defaultCompressorConfig.MaxChars
	}
	if cfg.SummaryLen <= 0 {
		cfg.SummaryLen = defaultCompressorConfig.SummaryLen
	}
	if logger == nil {
		logger = log.Default()
	}
	var classifier func(string) bool
	if len(classifiers) > 0 {
		classifier = classifiers[0]
	}
	return &CompressedTool{inner: tool, config: cfg, logger: logger, readOnlyClassifier: classifier}
}

func (t *CompressedTool) Name() string                       { return t.inner.Name() }
func (t *CompressedTool) Description() string                { return t.inner.Description() }
func (t *CompressedTool) Parameters() map[string]interface{} { return t.inner.Parameters() }

// Preserve every optional tool capability across the compression wrapper.
// ToolRegistry makes execution and outcome decisions from these interfaces;
// losing them changes semantics (for example list_skills prose can be treated
// as a failure, and use_skill can be clipped at the generic 8 KB limit).
func (t *CompressedTool) MaxResultSize() int {
	if limited, ok := t.inner.(SizeLimitedTool); ok {
		return limited.MaxResultSize()
	}
	return t.config.MaxChars
}

func (t *CompressedTool) IsReadOnly() bool {
	if readOnly, ok := t.inner.(ReadOnlyTool); ok {
		return readOnly.IsReadOnly()
	}
	return t.readOnlyClassifier != nil && t.readOnlyClassifier(t.inner.Name())
}

func (t *CompressedTool) IsNeverParallel() bool {
	if serial, ok := t.inner.(NeverParallelTool); ok {
		return serial.IsNeverParallel()
	}
	return false
}

func (t *CompressedTool) ScopePath(args map[string]interface{}) string {
	if scoped, ok := t.inner.(PathScopedTool); ok {
		return scoped.ScopePath(args)
	}
	return ""
}

func (t *CompressedTool) SnapshotPaths(args map[string]interface{}) []string {
	if snapshotter, ok := t.inner.(SnapshotPathsTool); ok {
		return snapshotter.SnapshotPaths(args)
	}
	return nil
}

func (t *CompressedTool) SchemaRepairAdvice(args map[string]interface{}) string {
	if advisor, ok := t.inner.(SchemaRepairAdvisor); ok {
		return advisor.SchemaRepairAdvice(args)
	}
	return ""
}

func (t *CompressedTool) ResultIsContentPayload() bool {
	payload, ok := t.inner.(ContentPayloadTool)
	return ok && payload.ResultIsContentPayload()
}

func (t *CompressedTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	result, err := t.inner.Execute(ctx, params)
	if err != nil {
		return result, err
	}
	if limited, ok := t.inner.(SizeLimitedTool); ok {
		declaredBytes := limited.MaxResultSize()
		// Zero means intentionally unlimited. A positive declaration is the
		// tool-specific context budget and takes precedence over this wrapper's
		// generic default.
		if declaredBytes == 0 || len(result) <= declaredBytes {
			return result, nil
		}
	}

	origLen := utf8.RuneCountInString(result)
	if origLen <= t.config.MaxChars {
		return result, nil
	}

	// Compress: keep first SummaryLen chars + compression footer
	keep := t.config.SummaryLen
	runes := []rune(result)
	if keep > len(runes) {
		keep = len(runes)
	}
	compressed := string(runes[:keep])

	// Add compression footer with stats and truncation note
	footer := fmt.Sprintf("\n\n---\n[compressed] %.1fKB -> %.1fKB (%.0f%%) | Use `read_file` or targeted `grep` to inspect full output.",
		float64(origLen)/1024, float64(keep)/1024, float64(keep)/float64(origLen)*100)

	// Append a brief summary of the last portion if available
	tailStart := origLen - 200
	if tailStart > keep && tailStart < origLen {
		tail := string(runes[tailStart:])
		// Extract up to 3 lines from the tail for context
		lines := strings.Split(tail, "\n")
		if len(lines) > 4 {
			lines = lines[len(lines)-4:]
		}
		footer += "\n[...tail context...]\n" + strings.Join(lines, "\n")
	}

	t.logger.Printf("[Compressor] %s: %d -> %d chars (%.0f%%)",
		t.inner.Name(), origLen, keep, float64(keep)/float64(origLen)*100)

	return compressed + footer, nil
}
