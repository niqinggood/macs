package session

import (
	"os"
	"strings"
	"testing"
)

const syntheticTranscriptAPIKey = "sk-openkitt-unit-test-extra-segments-0123456789abcdef"

func TestSessionTranscriptFinalizePreservesAccountInfo(t *testing.T) {
	transcript := NewSessionTranscript("secret-finalize", "tester", t.TempDir())
	transcript.RecordUserMessage("user supplied " + syntheticTranscriptAPIKey)
	transcript.RecordToolCall("example", map[string]interface{}{
		"api_key": syntheticTranscriptAPIKey,
	})
	transcript.RecordToolResult("example", "tool returned "+syntheticTranscriptAPIKey, true, 1)

	if err := transcript.Finalize("final output "+syntheticTranscriptAPIKey, "success"); err != nil {
		t.Fatalf("Finalize() error = %v", err)
	}

	assertPersistedTranscriptAccountInfoPreserved(t, transcript.FilePath())
	assertPersistedTranscriptAccountInfoPreserved(t, transcript.JournalPath())
}

func TestSessionTranscriptFinalizeWithInternalRecapPreservesAccountInfo(t *testing.T) {
	transcript := NewSessionTranscript("secret-recap", "tester", t.TempDir())

	if err := transcript.FinalizeWithInternalRecap(
		"safe final output",
		"failed",
		"internal recap "+syntheticTranscriptAPIKey,
	); err != nil {
		t.Fatalf("FinalizeWithInternalRecap() error = %v", err)
	}

	assertPersistedTranscriptAccountInfoPreserved(t, transcript.FilePath())
	assertPersistedTranscriptAccountInfoPreserved(t, transcript.JournalPath())
}

func assertPersistedTranscriptAccountInfoPreserved(t *testing.T, path string) {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%q) error = %v", path, err)
	}
	text := string(data)
	if !strings.Contains(text, syntheticTranscriptAPIKey) {
		t.Fatal("persisted workflow transcript lost the synthetic API key")
	}
	if strings.Contains(text, "[REDACTED]") || strings.Contains(text, "[SCRUBBED]") {
		t.Fatalf("persisted workflow transcript contains a forced-redaction marker: %s", text)
	}
}
