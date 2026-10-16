package fsutil

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteAtomicReplacesCompleteFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if err := WriteAtomic(path, []byte("new state"), 0o600); err != nil {
		t.Fatalf("WriteAtomic() error = %v", err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if string(content) != "new state" {
		t.Fatalf("content = %q, want %q", content, "new state")
	}
}

func TestRetryableAtomicRenameErrorRejectsMissingFile(t *testing.T) {
	if IsRetryableAtomicRenameError(os.ErrNotExist) {
		t.Fatal("missing-file error must not be retried")
	}
}

func TestFirstExistingPathSelectsFirstMatch(t *testing.T) {
	directory := t.TempDir()
	first := filepath.Join(directory, "first")
	second := filepath.Join(directory, "second")
	if err := os.WriteFile(first, []byte("first"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(second, []byte("second"), 0o600); err != nil {
		t.Fatal(err)
	}

	got := FirstExistingPath(filepath.Join(directory, "missing"), first, second)
	want, err := filepath.Abs(first)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("FirstExistingPath() = %q, want %q", got, want)
	}
}
