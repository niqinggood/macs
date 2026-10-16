package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenerateWaitsForAllExplicitArtifactReceipts(t *testing.T) {
	const (
		dataPath   = "runs/job/normalized.json"
		reportPath = "runs/job/recovery_report.md"
	)
	transport := &scriptedLLMTransport{responses: []scriptedLLMHTTPResponse{
		{contentType: "application/json", body: verifiedFileToolCallJSON(t, "report", "write_file", `{"path":"`+reportPath+`","content":"# recovery report"}`)},
		{contentType: "application/json", body: verifiedFileToolCallJSON(t, "data", "write_file", `{"path":"`+dataPath+`","content":"[{\"id\":1}]"}`)},
		{contentType: "application/json", body: completionJSON(t, "stop", "this third model call must not happen")},
	}}
	agent := newIncompleteResponseTestAgent(t, transport, false, 6)
	agent.toolRegistry = NewToolRegistry()
	agent.toolRegistry.Register(NewWriteFileTool(agent.workspace))
	agent.toolRegistry.Register(NewReadFileTool(agent.workspace))

	ctx := withResolvedWorkspace(context.Background(), agent.workspace)
	input := "Convert fixtures/data/source.csv to " + dataPath + " and write " + reportPath
	output, err := agent.Generate(ctx, input)
	if err != nil {
		t.Fatalf("Generate returned an error: %v\n%s", err, output)
	}
	if got := transport.requestCount(); got != 2 {
		t.Fatalf("model request count = %d, want 2: the first artifact must not complete the turn and the second must", got)
	}
	for _, relative := range []string{dataPath, reportPath} {
		data, readErr := os.ReadFile(filepath.Join(agent.workspace, filepath.FromSlash(relative)))
		if readErr != nil || len(data) == 0 {
			t.Fatalf("requested artifact %q was not persisted: bytes=%d err=%v", relative, len(data), readErr)
		}
	}
	if !strings.Contains(output, dataPath) {
		t.Fatalf("terminal receipt did not identify the final requested artifact: %s", output)
	}
}

func TestGeneratePreservesSingleArtifactTerminalFastPath(t *testing.T) {
	const reportPath = "runs/job/report.md"
	transport := &scriptedLLMTransport{responses: []scriptedLLMHTTPResponse{
		{contentType: "application/json", body: verifiedFileToolCallJSON(t, "report", "write_file", `{"path":"`+reportPath+`","content":"# report"}`)},
		{contentType: "application/json", body: completionJSON(t, "stop", "this second model call must not happen")},
	}}
	agent := newIncompleteResponseTestAgent(t, transport, false, 4)
	agent.toolRegistry = NewToolRegistry()
	agent.toolRegistry.Register(NewWriteFileTool(agent.workspace))

	ctx := withResolvedWorkspace(context.Background(), agent.workspace)
	output, err := agent.Generate(ctx, "Write the report to "+reportPath)
	if err != nil {
		t.Fatalf("Generate returned an error: %v\n%s", err, output)
	}
	if got := transport.requestCount(); got != 1 {
		t.Fatalf("single requested artifact used %d model calls, want the original one-call terminal behavior", got)
	}
	if !strings.Contains(output, reportPath) {
		t.Fatalf("single-artifact terminal receipt missing path: %s", output)
	}
}
