package app

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	memorycore "mac/internal/memory"
)

func TestMemoryServiceRuntimeSummaryUsesDatabaseCommit(t *testing.T) {
	agent, db := newMemoryServiceTestAgentWithDB(t)
	ctx, cancel := context.WithTimeout(context.WithValue(context.Background(), "session_id", "one"), 5*time.Second)
	defer cancel()
	prompts := make(chan string, 1)
	agent.sessionMemory = memorycore.NewSessionMemoryExtractor(agent.workspace, memorycore.DefaultSessionMemoryConfig(), memorycore.SessionExtractorDependencies{
		CallLLM: func(_ context.Context, messages []map[string]interface{}, _ int) (string, error) {
			prompt, _ := messages[1]["content"].(string)
			prompts <- prompt
			return "quartz latest summary", nil
		},
	})
	owner, err := agent.memoryOwner(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := agent.memoryService.SaveSummary(ctx, owner, "quartz current database"); err != nil {
		t.Fatal(err)
	}
	legacyPath := agent.sessionMemory.ForSession("one").MemoryPath()
	if err := os.WriteFile(legacyPath, []byte("obsolete markdown"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := agent.enqueueMemoryJob(ctx, "one", "summary", memoryProcessingPayload{Messages: []map[string]interface{}{{"role": "user", "content": "quartz new facts"}}}); err != nil {
		t.Fatal(err)
	}
	agent.processMemoryJobs(ctx)
	// The host takes its semaphore before starting the worker and releases it
	// after persisting completion. Wait for the whole job, not just the model.
	sem := agent.scm.DerivationSemaphore()
	select {
	case sem <- struct{}{}:
		<-sem
	case <-ctx.Done():
		t.Fatal("summary worker did not finish")
	}
	select {
	case prompt := <-prompts:
		if !strings.Contains(prompt, "quartz current database") || strings.Contains(prompt, "obsolete markdown") {
			t.Fatalf("wrong summary source: %s", prompt)
		}
	default:
		t.Fatal("summary model not called")
	}
	content, err := agent.memoryService.LoadSummary(ctx, owner)
	if err != nil || content != "quartz latest summary" {
		t.Fatalf("summary: %q %v", content, err)
	}
	var job memorycore.ProcessingJob
	if err := db.First(&job).Error; err != nil {
		t.Fatal(err)
	}
	if job.State != "done" || job.SourceRevision != 2 || job.Payload != "" {
		t.Fatalf("job not completed: %+v", job)
	}
	data, err := os.ReadFile(legacyPath)
	if err != nil || string(data) != "obsolete markdown" {
		t.Fatalf("legacy file changed: %q %v", data, err)
	}
}
