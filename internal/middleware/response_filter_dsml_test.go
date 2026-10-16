package middleware

import (
	"strings"
	"testing"
)

func TestStripToolCallsRemovesCompleteDSMLBlockAndKeepsAnswer(t *testing.T) {
	input := `<｜｜DSML｜｜tool_calls>
{"name":"read_file","arguments":{"path":"README.md"}}
</｜｜DSML｜｜tool_calls>
README 的主要用途是介绍项目。`

	if got, want := StripToolCalls(input), "README 的主要用途是介绍项目。"; got != want {
		t.Fatalf("StripToolCalls() = %q, want %q", got, want)
	}
}

func TestResponseFilterRemovesMultipleDSMLVariants(t *testing.T) {
	input := "开始\n<||dsml||tool_calls>{\"name\":\"one\"}</||DSML||tool_calls>\n中间\n" +
		"< ｜｜DSML ｜｜ tool_calls >{\"name\":\"two\"}</ ｜｜DSML｜｜tool_calls >\n完成"
	ctx := &Context{Messages: []map[string]interface{}{
		{"role": "assistant", "content": input},
	}}

	if err := NewResponseFilterMiddleware().AfterModel(ctx); err != nil {
		t.Fatal(err)
	}
	got := ctx.Messages[0]["content"].(string)
	if strings.Contains(strings.ToLower(got), "dsml") || strings.Contains(got, `"name"`) {
		t.Fatalf("DSML protocol leaked through middleware: %q", got)
	}
	for _, want := range []string{"开始", "中间", "完成"} {
		if !strings.Contains(got, want) {
			t.Fatalf("ordinary answer text %q was lost: %q", want, got)
		}
	}
}

func TestStripToolCallsFailsClosedOnMalformedDSML(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{name: "unclosed", input: `<｜｜DSML｜｜tool_calls>{"name":"read_file"}`},
		{name: "stray close", input: `answer</｜｜DSML｜｜tool_calls>`},
		{name: "truncated tag", input: `answer<｜｜DSML｜｜tool_calls`},
		{name: "nested", input: `<｜｜DSML｜｜tool_calls><｜｜DSML｜｜tool_calls></｜｜DSML｜｜tool_calls>`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := StripToolCalls(tt.input); got != "" {
				t.Fatalf("malformed DSML must fail closed, got %q", got)
			}
		})
	}
}

func TestStripToolCallsPreservesProtocolExamplesInsideMarkdownFences(t *testing.T) {
	input := "示例：\n```json\n<｜｜DSML｜｜tool_calls>\n" +
		`{"name":"read_file","arguments":{"path":"README.md"}}` + "\n" +
		"</｜｜DSML｜｜tool_calls>\n<tool_call>also an example</tool_call>\n```\n说明结束。"

	if got := StripToolCalls(input); got != input {
		t.Fatalf("fenced protocol example changed:\n got: %q\nwant: %q", got, input)
	}
}

func TestStripToolCallsStillCleansOutsideMarkdownFences(t *testing.T) {
	input := "```text\n<｜｜DSML｜｜tool_calls>example</｜｜DSML｜｜tool_calls>\n```\n" +
		"<｜｜DSML｜｜tool_calls>real protocol</｜｜DSML｜｜tool_calls>\n最终答复"
	want := "```text\n<｜｜DSML｜｜tool_calls>example</｜｜DSML｜｜tool_calls>\n```\n\n最终答复"

	if got := StripToolCalls(input); got != want {
		t.Fatalf("StripToolCalls() = %q, want %q", got, want)
	}
}
