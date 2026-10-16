package memory

import (
	"context"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const syntheticPersistenceSecret = "sk-openkitty-persistence-1234567890abcdef"

func TestSessionMemoryUsesPerSessionFileAndPreservesAccountInfo(t *testing.T) {
	workspace := t.TempDir()
	extractor := NewSessionMemoryExtractor(
		workspace,
		DefaultSessionMemoryConfig(),
		SessionExtractorDependencies{
			CallLLM: func(context.Context, []map[string]interface{}, int) (string, error) {
				return "# Session Memory\ncredential=" + syntheticPersistenceSecret, nil
			},
			Logger: log.New(io.Discard, "", 0),
		},
	)
	extractor.SetSession("web/session-1")

	if err := extractor.Extract([]map[string]interface{}{{"role": "user", "content": "remember this"}}, "Researcher"); err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	path := filepath.Join(workspace, ".openkitty", "session-memory-web-session-1.md")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("session memory file missing: %v", err)
	}
	if !info.Mode().IsRegular() {
		t.Fatalf("session memory path is not a regular file: %s", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read session memory: %v", err)
	}
	if !strings.Contains(string(data), syntheticPersistenceSecret) {
		t.Fatalf("session memory lost workflow account information: %s", data)
	}
	if strings.Contains(string(data), "[REDACTED]") || strings.Contains(string(data), "[SCRUBBED]") {
		t.Fatalf("session memory contains a forced-redaction marker: %s", data)
	}
}

func TestSessionMemoryExtractionPreservesFailedToolStatus(t *testing.T) {
	extractor := NewSessionMemoryExtractor(
		t.TempDir(),
		DefaultSessionMemoryConfig(),
		SessionExtractorDependencies{Logger: log.New(io.Discard, "", 0)},
	)
	got := extractor.BuildExtractionText([]map[string]interface{}{
		{
			"role":    "tool",
			"name":    "read_file",
			"status":  false,
			"content": `执行失败: lead-research evidence guard denied runtime or historical artifact: E:\demo\.openkitty\storage\step0.txt`,
		},
	})
	if !strings.Contains(got, "[tool-failed:read_file]") || !strings.Contains(got, "执行失败") {
		t.Fatalf("failed tool status missing from extraction text: %q", got)
	}
	if strings.Contains(got, "[tool-artifacts:read_file]") {
		t.Fatalf("failed tool path was promoted to a successful artifact: %q", got)
	}
}

func TestSessionMemorySystemPromptForbidsPromotingFailedTools(t *testing.T) {
	for _, want := range []string{"Tool status is authoritative", "did NOT succeed", "never say the file was read"} {
		if !strings.Contains(SessionMemorySystemPrompt, want) {
			t.Fatalf("session memory prompt missing failed-tool rule %q", want)
		}
	}
}
