package configvalue

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestRecoverCorruptFileBacksUpAndValidatesReplacement(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	original := []byte("broken: [")
	if err := os.WriteFile(path, original, 0o640); err != nil {
		t.Fatal(err)
	}
	backup, err := RecoverCorruptFile(
		path,
		func(string) ([]byte, error) { return []byte("valid: true\n"), nil },
		func(candidate string) error {
			content, readErr := os.ReadFile(candidate)
			if readErr != nil {
				return readErr
			}
			if string(content) != "valid: true\n" {
				return errors.New("unexpected replacement")
			}
			return nil
		},
		func(candidate string, data []byte, permission os.FileMode) error {
			return os.WriteFile(candidate, data, permission)
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	backupContent, err := os.ReadFile(backup)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(backupContent, original) {
		t.Fatalf("backup = %q", backupContent)
	}
}

func TestRecoverCorruptFileRestoresOriginalAfterValidationFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	original := []byte("broken: [")
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	backup, err := RecoverCorruptFile(
		path,
		func(string) ([]byte, error) { return []byte("replacement\n"), nil },
		func(string) error { return errors.New("invalid replacement") },
		func(candidate string, data []byte, permission os.FileMode) error {
			return os.WriteFile(candidate, data, permission)
		},
	)
	if err == nil || backup != "" {
		t.Fatalf("backup=%q err=%v", backup, err)
	}
	restored, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if !bytes.Equal(restored, original) {
		t.Fatalf("restored = %q", restored)
	}
}
