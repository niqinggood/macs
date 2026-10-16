package app

import (
	"context"
	"strings"
	"testing"
)

type validationMockTool struct {
	calls int
}

func (m *validationMockTool) Name() string        { return "validation_mock" }
func (m *validationMockTool) Description() string { return "mock validation tool" }
func (m *validationMockTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"name": map[string]interface{}{"type": "string"},
			"mode": map[string]interface{}{"type": "string", "enum": []string{"fast", "full"}},
			"n":    map[string]interface{}{"type": "integer"},
		},
		"required": []string{"name"},
	}
}
func (m *validationMockTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	m.calls++
	return "ok", nil
}

type openValidationMockTool struct {
	validationMockTool
}

func (m *openValidationMockTool) Name() string { return "open_validation_mock" }
func (m *openValidationMockTool) Parameters() map[string]interface{} {
	schema := m.validationMockTool.Parameters()
	schema["additionalProperties"] = true
	return schema
}

type nestedValidationMockTool struct {
	calls int
}

func (m *nestedValidationMockTool) Name() string        { return "nested_validation_mock" }
func (m *nestedValidationMockTool) Description() string { return "mock nested validation tool" }
func (m *nestedValidationMockTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"items": map[string]interface{}{
				"type": "array",
				"items": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"title": map[string]interface{}{"type": "string"},
						"done":  map[string]interface{}{"type": "boolean"},
					},
					"required": []string{"title"},
				},
			},
		},
		"required": []string{"items"},
	}
}
func (m *nestedValidationMockTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	m.calls++
	return "ok", nil
}

type hugeValidationMockTool struct {
	calls int
}

func (m *hugeValidationMockTool) Name() string        { return "huge_validation_mock" }
func (m *hugeValidationMockTool) Description() string { return "mock huge validation tool" }
func (m *hugeValidationMockTool) Parameters() map[string]interface{} {
	values := make([]string, 0, 300)
	for i := 0; i < 300; i++ {
		values = append(values, strings.Repeat("x", 20))
	}
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"mode": map[string]interface{}{"type": "string", "enum": values},
		},
	}
}
func (m *hugeValidationMockTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	m.calls++
	return "ok", nil
}

func TestToolRegistryRejectsMissingRequiredArgument(t *testing.T) {
	tool := &validationMockTool{}
	reg := NewToolRegistry()
	reg.Register(tool)

	result, err := reg.ExecuteRaw(context.Background(), ToolCall{
		ID:        "1",
		Name:      "validation_mock",
		Arguments: map[string]interface{}{"mode": "fast"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if tool.calls != 0 {
		t.Fatal("tool should not execute when required argument is missing")
	}
	if !strings.Contains(result.Content, "[invalid_args]") || !strings.Contains(result.Content, "missing required parameter") {
		t.Fatalf("expected invalid_args required error, got %q", result.Content)
	}
	for _, want := range []string{
		"[TOOL SCHEMA REPAIR]",
		"Tool: validation_mock",
		"Missing: name",
		"Required top-level parameters: name:string",
		"Received parameters: mode",
		"one complete call",
		"do not repeat this incomplete payload",
	} {
		if !strings.Contains(result.Content, want) {
			t.Fatalf("schema repair observation missing %q:\n%s", want, result.Content)
		}
	}
}

func TestWriteFileMissingPathGetsBoundedWriteRepairAdvice(t *testing.T) {
	reg := NewToolRegistry()
	reg.Register(NewWriteFileTool(t.TempDir()))
	result, err := reg.ExecuteRaw(context.Background(), ToolCall{
		ID:        "write-missing-path",
		Name:      "write_file",
		Arguments: map[string]interface{}{"content": strings.Repeat("x", 900)},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`Missing: path`,
		`Required top-level parameters: path:string, content:string`,
		`emit path before content`,
		`bounded first chunk`,
		`use json_patch`,
		`Never guess a missing destination path`,
	} {
		if !strings.Contains(result.Content, want) {
			t.Fatalf("write repair advice missing %q:\n%s", want, result.Content)
		}
	}
}

func TestToolRegistryRejectsWrongArgumentType(t *testing.T) {
	tool := &validationMockTool{}
	reg := NewToolRegistry()
	reg.Register(tool)

	result, err := reg.ExecuteRaw(context.Background(), ToolCall{
		ID:        "1",
		Name:      "validation_mock",
		Arguments: map[string]interface{}{"name": "demo", "n": "3"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if tool.calls != 0 {
		t.Fatal("tool should not execute when argument type is wrong")
	}
	if !strings.Contains(result.Content, `parameter "n" must be integer`) {
		t.Fatalf("expected type error, got %q", result.Content)
	}
}

func TestToolRegistryRejectsInvalidEnum(t *testing.T) {
	tool := &validationMockTool{}
	reg := NewToolRegistry()
	reg.Register(tool)

	result, err := reg.ExecuteRaw(context.Background(), ToolCall{
		ID:        "1",
		Name:      "validation_mock",
		Arguments: map[string]interface{}{"name": "demo", "mode": "slow"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if tool.calls != 0 {
		t.Fatal("tool should not execute when enum value is invalid")
	}
	if !strings.Contains(result.Content, `parameter "mode" must be one of [fast, full]`) {
		t.Fatalf("expected enum error, got %q", result.Content)
	}
}

func TestToolRegistryRejectsUnknownArgument(t *testing.T) {
	tool := &validationMockTool{}
	reg := NewToolRegistry()
	reg.Register(tool)

	result, err := reg.ExecuteRaw(context.Background(), ToolCall{
		ID:        "1",
		Name:      "validation_mock",
		Arguments: map[string]interface{}{"name": "demo", "nam": "typo"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if tool.calls != 0 {
		t.Fatal("tool should not execute when an unknown argument is present")
	}
	for _, want := range []string{"[invalid_args]", `unknown parameter "nam"`, "allowed parameters"} {
		if !strings.Contains(result.Content, want) {
			t.Fatalf("expected unknown parameter error containing %q, got %q", want, result.Content)
		}
	}
}

func TestToolRegistryAllowsUnknownArgumentWhenSchemaIsOpen(t *testing.T) {
	tool := &openValidationMockTool{}
	reg := NewToolRegistry()
	reg.Register(tool)

	result, err := reg.ExecuteRaw(context.Background(), ToolCall{
		ID:        "1",
		Name:      "open_validation_mock",
		Arguments: map[string]interface{}{"name": "demo", "extra": "allowed"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if tool.calls != 1 {
		t.Fatalf("tool should execute once, got %d", tool.calls)
	}
	if result.Content != "ok" {
		t.Fatalf("expected ok, got %q", result.Content)
	}
}

func TestToolRegistryRejectsNestedMissingRequiredArgument(t *testing.T) {
	tool := &nestedValidationMockTool{}
	reg := NewToolRegistry()
	reg.Register(tool)

	result, err := reg.ExecuteRaw(context.Background(), ToolCall{
		ID:   "1",
		Name: "nested_validation_mock",
		Arguments: map[string]interface{}{
			"items": []interface{}{
				map[string]interface{}{"done": true},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if tool.calls != 0 {
		t.Fatal("tool should not execute when nested required argument is missing")
	}
	if !strings.Contains(result.Content, `missing required parameter "items[0].title"`) {
		t.Fatalf("expected nested required error, got %q", result.Content)
	}
}

func TestToolRegistryRejectsNestedWrongArgumentType(t *testing.T) {
	tool := &nestedValidationMockTool{}
	reg := NewToolRegistry()
	reg.Register(tool)

	result, err := reg.ExecuteRaw(context.Background(), ToolCall{
		ID:   "1",
		Name: "nested_validation_mock",
		Arguments: map[string]interface{}{
			"items": []interface{}{
				map[string]interface{}{"title": "one", "done": "yes"},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if tool.calls != 0 {
		t.Fatal("tool should not execute when nested argument type is wrong")
	}
	if !strings.Contains(result.Content, `parameter "items[0].done" must be boolean`) {
		t.Fatalf("expected nested type error, got %q", result.Content)
	}
}

func TestToolRegistryRejectsNestedUnknownArgument(t *testing.T) {
	tool := &nestedValidationMockTool{}
	reg := NewToolRegistry()
	reg.Register(tool)

	result, err := reg.ExecuteRaw(context.Background(), ToolCall{
		ID:   "1",
		Name: "nested_validation_mock",
		Arguments: map[string]interface{}{
			"items": []interface{}{
				map[string]interface{}{"title": "one", "titel": "typo"},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if tool.calls != 0 {
		t.Fatal("tool should not execute when nested argument is unknown")
	}
	if !strings.Contains(result.Content, `unknown parameter "items[0].titel"`) {
		t.Fatalf("expected nested unknown parameter error, got %q", result.Content)
	}
}

func TestToolRegistryAcceptsValidArguments(t *testing.T) {
	tool := &validationMockTool{}
	reg := NewToolRegistry()
	reg.Register(tool)

	result, err := reg.ExecuteRaw(context.Background(), ToolCall{
		ID:        "1",
		Name:      "validation_mock",
		Arguments: map[string]interface{}{"name": "demo", "mode": "fast", "n": float64(3)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if tool.calls != 1 {
		t.Fatalf("tool should execute once, got %d", tool.calls)
	}
	if result.Content != "ok" {
		t.Fatalf("expected ok, got %q", result.Content)
	}
}

func TestToolRegistryNormalizesLargeValidationError(t *testing.T) {
	tool := &hugeValidationMockTool{}
	reg := NewToolRegistry()
	reg.SetResultNormalizer(NewToolResultNormalizerWithConfig(t.TempDir(), ToolResultNormalizerConfig{
		MaxInlineChars:    120,
		KeyOutputMaxChars: 200,
	}))
	reg.Register(tool)

	result, err := reg.ExecuteRaw(context.Background(), ToolCall{
		ID:        "1",
		Name:      "huge_validation_mock",
		Arguments: map[string]interface{}{"mode": "bad"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if tool.calls != 0 {
		t.Fatal("tool should not execute when validation fails")
	}
	for _, want := range []string{"Tool Result", "status: failed_compacted", "details_file:", "[invalid_args]"} {
		if !strings.Contains(result.Content, want) {
			t.Fatalf("expected normalized validation error to contain %q, got:\n%s", want, result.Content)
		}
	}
	if strings.Count(result.Content, "x") > 260 {
		t.Fatalf("normalized validation error leaked too much enum text: %d x chars", strings.Count(result.Content, "x"))
	}
}
