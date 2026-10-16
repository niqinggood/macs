package app

import (
	"context"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	memorycore "mac/internal/memory"
)

func TestSessionMemoryExtractionDoesNotBlockAgentLoop(t *testing.T) {
	workspace := t.TempDir()
	release := make(chan struct{})
	finished := make(chan struct{})
	cfg := memorycore.DefaultSessionMemoryConfig()
	cfg.MinimumTokensToInit = 1
	cfg.MinimumTokensBetweenUpdate = 1
	cfg.ToolCallsBetweenUpdates = 1
	extractor := memorycore.NewSessionMemoryExtractor(workspace, cfg, memorycore.SessionExtractorDependencies{
		CallLLM: func(context.Context, []map[string]interface{}, int) (string, error) {
			<-release
			close(finished)
			return "# Session Memory\nfinished", nil
		},
		Logger: log.New(io.Discard, "", 0),
	})
	extractor.SetSession("async-session")
	agent := &LLMAgent{name: "Researcher", sessionMemory: extractor, fileLogger: log.New(io.Discard, "", 0)}
	messages := []map[string]interface{}{{"role": "user", "content": strings.Repeat("context ", 20)}}

	returned := make(chan struct{})
	go func() {
		agent.tryExtractSessionMemory(messages, 1, "async-session")
		close(returned)
	}()
	select {
	case <-returned:
	case <-time.After(250 * time.Millisecond):
		t.Fatal("session-memory helper blocked the agent loop")
	}
	close(release)
	select {
	case <-finished:
	case <-time.After(2 * time.Second):
		t.Fatal("background session-memory extraction did not finish")
	}
	memoryPath := filepath.Join(workspace, ".openkitty", "session-memory-async-session.md")
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(memoryPath); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("background session-memory file was not persisted: %s", memoryPath)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
