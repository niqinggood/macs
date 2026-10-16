package app

import (
	"context"
	"fmt"
	"io"
	"log"
	"strings"
	"testing"

	"mac/internal/runtimeutil"
)

func TestRestorePostCompactStatePreservesWorkspaceCoordinateSystem(t *testing.T) {
	sc := &SmartCompactor{}
	messages := []map[string]interface{}{{"role": "system", "content": "base"}}
	workspace := `E:\adebug2\0713\task-workspace`

	restored := sc.restorePostCompactState(messages, &CompactSessionContext{
		Workspace:   workspace,
		RecentFiles: []string{`.openkitty/storage/build_deck.py`},
	})

	joined := messagesText(restored)
	for _, want := range []string{
		"[WORKSPACE CONTEXT]",
		"Current workspace: " + workspace,
		"canonical base directory after context compaction",
		".openkitty/storage/build_deck.py",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("restored compact state is missing %q:\n%s", want, joined)
		}
	}
}

func TestSmartCompactorInjectsBoundedMechanicalState(t *testing.T) {
	criticalSuffix := "CRITICAL-SUFFIX-KEEP-EXACT-FILENAME report-final.md"
	longDirective := "Build the report. " + strings.Repeat("pasted context ", 120) + criticalSuffix
	messages := []map[string]interface{}{
		{"role": "system", "content": "system"},
		{"role": "user", "content": longDirective},
		{
			"role": "assistant",
			"tool_calls": []map[string]interface{}{
				{
					"id": "write-1", "type": "function",
					"function": map[string]interface{}{
						"name": "write_file", "arguments": `{"path":"reports/report-final.md","content":"draft"}`,
					},
				},
				{
					"id": "shell-1", "type": "function",
					"function": map[string]interface{}{
						"name": "run_shell", "arguments": `{"command":"go test ./..."}`,
					},
				},
			},
		},
		{"role": "tool", "tool_call_id": "write-1", "name": "write_file", "content": `{"ok":true}`},
		{"role": "tool", "tool_call_id": "shell-1", "name": "run_shell", "content": `{"exit_code":0}`},
	}
	for i := 0; i < 12; i++ {
		role := "assistant"
		content := fmt.Sprintf("follow-up-%02d", i)
		if i%2 == 0 {
			role = "user"
		} else {
			content = fmt.Sprintf("assistant-analysis-%02d %s", i, strings.Repeat("verbose evidence ", 100))
		}
		messages = append(messages, map[string]interface{}{
			"role": role, "content": content,
		})
	}

	var summarizerPrompt string
	cfg := DefaultCompressConfig()
	cfg.TailProtectMessages = 4
	sc := NewSmartCompactor(cfg, func(_ context.Context, prompt []map[string]interface{}, _ int) (string, error) {
		summarizerPrompt, _ = prompt[1]["content"].(string)
		return "summary-one", nil
	}, nil, log.New(io.Discard, "", 0))

	compressed, boundary, err := sc.Compact(messages, nil)
	if err != nil {
		t.Fatal(err)
	}
	if boundary == nil {
		t.Fatal("missing compact boundary")
	}
	spine := compactStateMessage(compressed)
	if spine == nil {
		t.Fatalf("compressed messages missing %q subtype: %#v", compactStateSubtype, compressed)
	}
	if spine["role"] != "user" {
		t.Fatalf("mechanical state role = %v, want user to preserve authority", spine["role"])
	}
	content, _ := spine["content"].(string)
	for _, want := range []string{
		"[COMPACTION MECHANICAL STATE]",
		criticalSuffix,
		"reports/report-final.md",
		"go test ./...",
		`"status":"ok"`,
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("mechanical state missing %q:\n%s", want, content)
		}
	}
	if len([]rune(content)) > compactStateMaxChars {
		t.Fatalf("mechanical state has %d chars, want <= %d", len([]rune(content)), compactStateMaxChars)
	}
	if !strings.Contains(summarizerPrompt, criticalSuffix) {
		t.Fatalf("summarizer prompt lost the directive suffix:\n%s", summarizerPrompt)
	}
	if boundary.PostTokens <= runtimeutil.EstimateTokens("summary-one") {
		t.Fatalf("post token count omitted mechanical state: %+v", boundary)
	}
	beforeTokens := estimateTokensForMessages(messages)
	afterTokens := estimateTokensForMessages(compressed)
	t.Logf("compaction token estimate: before=%d after=%d reduction=%.1f%%", beforeTokens, afterTokens, (1-float64(afterTokens)/float64(beforeTokens))*100)
	if afterTokens*100 >= beforeTokens*80 {
		t.Fatalf("mechanical state did not materially compact the long history: before=%d after=%d", beforeTokens, afterTokens)
	}
}

func TestSmartCompactorRepeatedCompactionMergesPriorSummaryAndState(t *testing.T) {
	criticalSuffix := "OLDEST-CONSTRAINT: retain customer-visible-name.csv"
	messages := []map[string]interface{}{{"role": "system", "content": "system"}}
	for i := 0; i < 16; i++ {
		role := "assistant"
		content := fmt.Sprintf("first-pass-%02d", i)
		if i%2 == 0 {
			role = "user"
		}
		if i == 0 {
			content = "Initial request " + strings.Repeat("bulk ", 260) + criticalSuffix
		}
		messages = append(messages, map[string]interface{}{"role": role, "content": content})
	}

	var prompts []string
	calls := 0
	cfg := DefaultCompressConfig()
	cfg.TailProtectMessages = 4
	sc := NewSmartCompactor(cfg, func(_ context.Context, prompt []map[string]interface{}, _ int) (string, error) {
		prompts = append(prompts, prompt[1]["content"].(string))
		calls++
		return fmt.Sprintf("summary-%d", calls), nil
	}, nil, log.New(io.Discard, "", 0))

	first, _, err := sc.Compact(messages, nil)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 12; i++ {
		role := "assistant"
		if i%2 == 0 {
			role = "user"
		}
		first = append(first, map[string]interface{}{
			"role": role, "content": fmt.Sprintf("second-pass-%02d", i),
		})
	}

	second, _, err := sc.Compact(first, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(prompts) != 2 || !strings.Contains(prompts[1], "summary-1") {
		t.Fatalf("second compaction did not fold the prior summary: %#v", prompts)
	}
	spine := compactStateMessage(second)
	if spine == nil {
		t.Fatal("second compaction lost mechanical state")
	}
	content, _ := spine["content"].(string)
	if !strings.Contains(content, criticalSuffix) {
		t.Fatalf("second compaction lost oldest user constraint:\n%s", content)
	}
	count := 0
	for _, message := range second {
		if message["subtype"] == compactStateSubtype {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("second compaction has %d mechanical states, want 1: %#v", count, second)
	}
}

func TestDeterministicCompactorUsesSameMechanicalState(t *testing.T) {
	criticalSuffix := "FALLBACK-CONSTRAINT: preserve ledger-source.xlsx"
	messages := []map[string]interface{}{
		{"role": "system", "content": "system"},
		{"role": "user", "content": "Fallback task " + strings.Repeat("bulk ", 240) + criticalSuffix},
		{
			"role": "assistant",
			"tool_calls": []map[string]interface{}{
				{
					"id": "edit-1", "type": "function",
					"function": map[string]interface{}{
						"name": "edit_file", "arguments": `{"path":"ledger-source.xlsx"}`,
					},
				},
			},
		},
		{"role": "tool", "tool_call_id": "edit-1", "name": "edit_file", "content": `{"success":true}`},
	}
	for i := 0; i < 16; i++ {
		role := "assistant"
		if i%2 == 0 {
			role = "user"
		}
		messages = append(messages, map[string]interface{}{
			"role": role, "content": fmt.Sprintf("fallback-follow-up-%02d", i),
		})
	}

	agent := &LLMAgent{fileLogger: log.New(io.Discard, "", 0)}
	compressed, err := agent.deterministicCompress(context.Background(), messages)
	if err != nil {
		t.Fatal(err)
	}
	spine := compactStateMessage(compressed)
	if spine == nil {
		t.Fatal("deterministic compaction did not inject mechanical state")
	}
	content, _ := spine["content"].(string)
	for _, want := range []string{criticalSuffix, "ledger-source.xlsx", `"status":"ok"`} {
		if !strings.Contains(content, want) {
			t.Fatalf("deterministic mechanical state missing %q:\n%s", want, content)
		}
	}
}

func TestCompactMechanicalStateCapsUsersAndTracksDroppedCount(t *testing.T) {
	messages := make([]map[string]interface{}, 0, compactStateUserLimit+9)
	for i := 0; i < compactStateUserLimit+9; i++ {
		messages = append(messages, map[string]interface{}{
			"role": "user", "content": fmt.Sprintf("directive-%02d", i),
		})
	}

	state := buildCompactMechanicalState(messages)
	if len(state.UserMessages) != compactStateUserLimit {
		t.Fatalf("kept %d user messages, want %d", len(state.UserMessages), compactStateUserLimit)
	}
	if state.UserMessagesDropped != 9 {
		t.Fatalf("dropped count = %d, want 9", state.UserMessagesDropped)
	}
	if state.UserMessages[0] != "directive-09" || state.UserMessages[len(state.UserMessages)-1] != "directive-40" {
		t.Fatalf("unexpected retained user window: %#v", state.UserMessages)
	}

	rendered := renderCompactMechanicalState(state)
	restored, ok := parseCompactMechanicalState(rendered)
	if !ok || restored.UserMessagesDropped != state.UserMessagesDropped ||
		len(restored.UserMessages) != len(state.UserMessages) {
		t.Fatalf("mechanical state did not round-trip: ok=%t restored=%+v", ok, restored)
	}
}

func TestCompactToolResultStatusDoesNotTreatNullErrorAsFailure(t *testing.T) {
	result := map[string]interface{}{
		"content": `{"error":null,"exit_code":0}`,
	}
	if got := compactToolResultStatus(result, true); got != "ok" {
		t.Fatalf("status with null error and exit zero = %q, want ok", got)
	}
	if got := compactToolResultStatus(nil, false); got != "pending" {
		t.Fatalf("missing result status = %q, want pending", got)
	}
}

func compactStateMessage(messages []map[string]interface{}) map[string]interface{} {
	for _, message := range messages {
		if message["subtype"] == compactStateSubtype {
			return message
		}
	}
	return nil
}

func TestRethinkChangesMethodWithoutWideningScope(t *testing.T) {
	agent := &LLMAgent{
		modelConfig: &ModelConfig{},
		fileLogger:  log.New(io.Discard, "", 0),
	}
	guidance := agent.doRethink(nil, "", nil, nil, nil, 44)

	for _, want := range []string{
		"smallest unresolved gap",
		"Reuse exact workspace paths",
		"Do not widen filesystem or network scope",
	} {
		if !strings.Contains(guidance, want) {
			t.Fatalf("rethink guidance is missing %q: %s", want, guidance)
		}
	}
	if strings.Contains(guidance, "try a completely different approach") {
		t.Fatalf("rethink guidance still contains the unbounded strategy switch: %s", guidance)
	}
}

func messagesText(messages []map[string]interface{}) string {
	var parts []string
	for _, message := range messages {
		if content, ok := message["content"].(string); ok {
			parts = append(parts, content)
		}
	}
	return strings.Join(parts, "\n")
}
