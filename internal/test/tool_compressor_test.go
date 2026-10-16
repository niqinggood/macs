package app

import (
	"context"
	"log"
	"strings"
	"testing"
)

type compressorCapabilityTool struct {
	name    string
	result  string
	maxSize int
}

func (t *compressorCapabilityTool) Name() string        { return t.name }
func (t *compressorCapabilityTool) Description() string { return "test" }
func (t *compressorCapabilityTool) Parameters() map[string]interface{} {
	return map[string]interface{}{"type": "object"}
}
func (t *compressorCapabilityTool) Execute(context.Context, map[string]interface{}) (string, error) {
	return t.result, nil
}
func (t *compressorCapabilityTool) MaxResultSize() int { return t.maxSize }
func (t *compressorCapabilityTool) IsReadOnly() bool   { return true }
func (t *compressorCapabilityTool) IsNeverParallel() bool {
	return true
}
func (t *compressorCapabilityTool) ScopePath(map[string]interface{}) string { return "scoped.txt" }
func (t *compressorCapabilityTool) SchemaRepairAdvice(map[string]interface{}) string {
	return "repair"
}
func (t *compressorCapabilityTool) ResultIsContentPayload() bool { return true }

func TestCompressedToolPreservesCapabilitiesAndDeclaredInstructionBudget(t *testing.T) {
	full := "FAST_QA_COMMAND\n" + strings.Repeat("skill instruction\n", 1200)
	inner := &compressorCapabilityTool{name: "use_skill", result: full, maxSize: len(full) + 1024}
	wrapped := WrapWithCompressor(inner, CompressorConfig{Enabled: true, MaxChars: 8000, SummaryLen: 1500}, log.Default())
	tool, ok := wrapped.(*compressedTool)
	if !ok {
		t.Fatalf("unexpected wrapper type %T", wrapped)
	}
	if tool.MaxResultSize() != inner.maxSize || !tool.IsReadOnly() || !tool.IsNeverParallel() ||
		tool.ScopePath(nil) != "scoped.txt" || tool.SchemaRepairAdvice(nil) != "repair" || !tool.ResultIsContentPayload() {
		t.Fatalf("compression wrapper dropped an optional capability")
	}
	got, err := tool.Execute(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != full || strings.Contains(got, "[compressed]") {
		t.Fatal("tool-specific instruction budget was replaced by the generic compressor limit")
	}
}

func TestCompressorRegistryDoesNotWrapUseSkill(t *testing.T) {
	registry := NewToolRegistry()
	registry.Register(&compressorCapabilityTool{name: "use_skill", result: strings.Repeat("x", 12000), maxSize: 20000})
	compressed := CompressorRegistry(registry, CompressorConfig{Enabled: true, MaxChars: 8000, SummaryLen: 1500}, log.Default())
	tool, ok := compressed.Get("use_skill")
	if !ok {
		t.Fatal("use_skill missing after compressor registry rebuild")
	}
	if _, wrapped := tool.(*compressedTool); wrapped {
		t.Fatal("use_skill instructions must bypass generic output compression")
	}
}
