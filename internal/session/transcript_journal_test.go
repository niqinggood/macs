package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSessionTranscriptJournalRecoversRunningStateAndIgnoresPartialTail(t *testing.T) {
	transcript := NewSessionTranscript("recover-running", "tester", t.TempDir())
	transcript.RecordUserMessage("hello")
	transcript.RecordToolCall("read_file", map[string]interface{}{"path": "README.md"})

	journalPath := transcript.JournalPath()
	file, err := os.OpenFile(journalPath, os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString(`{"version":1,"sequence":`); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	recovered, err := RecoverSessionTranscriptFromJournal(journalPath)
	if err != nil {
		t.Fatalf("RecoverSessionTranscriptFromJournal failed: %v", err)
	}
	if recovered.SessionID != "recover-running" || recovered.AgentName != "tester" || recovered.Status != "running" {
		t.Fatalf("unexpected recovered identity/state: %#v", recovered)
	}
	if len(recovered.Entries) != 2 || recovered.Entries[0].Type != "user" || recovered.Entries[1].Type != "tool_call" {
		t.Fatalf("unexpected recovered entries: %#v", recovered.Entries)
	}
}

func TestSessionTranscriptJournalRecoversFinalState(t *testing.T) {
	transcript := NewSessionTranscript("recover-final", "tester", t.TempDir())
	transcript.RecordUserMessage("hello")
	transcript.RecordAssistantMessage("done")
	if err := transcript.FinalizeWithInternalRecap("final answer", "success", "internal recap"); err != nil {
		t.Fatal(err)
	}

	recovered, err := RecoverSessionTranscriptFromJournal(transcript.JournalPath())
	if err != nil {
		t.Fatal(err)
	}
	if recovered.Status != "success" || recovered.FinalOutput != "final answer" || recovered.InternalRecap != "internal recap" {
		t.Fatalf("unexpected recovered final state: %#v", recovered)
	}
	if recovered.EndTime.IsZero() || len(recovered.Entries) != 2 {
		t.Fatalf("incomplete recovered final transcript: %#v", recovered)
	}
}

func TestSessionTranscriptPathsStayInsideOutputDirectory(t *testing.T) {
	outputDir := t.TempDir()
	transcript := NewSessionTranscript(`..\..\outside/session`, "tester", outputDir)
	for _, path := range []string{transcript.FilePath(), transcript.JournalPath(), transcript.CheckpointPath()} {
		rel, err := filepath.Rel(outputDir, path)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
			t.Fatalf("transcript path escaped output directory: path=%q rel=%q err=%v", path, rel, err)
		}
	}
}
