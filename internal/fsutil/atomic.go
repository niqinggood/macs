// Package fsutil provides low-level filesystem operations shared by runtime modules.
package fsutil

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// WriteAtomic writes data through a temporary file in the destination directory,
// syncs it, applies the requested permissions, and then renames it into place.
func WriteAtomic(path string, data []byte, permissions os.FileMode) error {
	directory := filepath.Dir(path)
	temporary, err := os.CreateTemp(directory, ".atomic-*")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	temporaryPath := temporary.Name()
	cleanup := func() { _ = os.Remove(temporaryPath) }

	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		cleanup()
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		cleanup()
		return fmt.Errorf("sync temp file: %w", err)
	}
	if err := temporary.Close(); err != nil {
		cleanup()
		return fmt.Errorf("close temp file: %w", err)
	}
	if err := os.Chmod(temporaryPath, permissions); err != nil {
		cleanup()
		return fmt.Errorf("chmod temp file: %w", err)
	}
	if err := RenameAtomicTempFile(temporaryPath, path); err != nil {
		cleanup()
		return fmt.Errorf("rename temp file: %w", err)
	}
	return nil
}

// RenameAtomicTempFile retries transient Windows sharing and lock failures.
func RenameAtomicTempFile(temporaryPath, destinationPath string) error {
	const maximumAttempts = 8

	var err error
	for attempt := 0; attempt < maximumAttempts; attempt++ {
		err = os.Rename(temporaryPath, destinationPath)
		if err == nil {
			return nil
		}
		if !IsRetryableAtomicRenameError(err) {
			return err
		}
		time.Sleep(time.Duration(attempt+1) * 10 * time.Millisecond)
	}
	return err
}

// IsRetryableAtomicRenameError reports whether a failed Windows rename may
// succeed after a short sharing-lock retry.
func IsRetryableAtomicRenameError(err error) bool {
	if runtime.GOOS != "windows" || err == nil {
		return false
	}
	if os.IsPermission(err) {
		return true
	}

	message := strings.ToLower(err.Error())
	return strings.Contains(message, "access is denied") ||
		strings.Contains(message, "sharing violation") ||
		strings.Contains(message, "being used by another process") ||
		strings.Contains(message, "lock violation")
}

// FirstExistingPath returns the absolute path of the first filesystem entry
// that exists, or an empty string when none of the candidates exist.
func FirstExistingPath(paths ...string) string {
	for _, path := range paths {
		if _, err := os.Stat(path); err != nil {
			continue
		}
		absolutePath, err := filepath.Abs(path)
		if err == nil {
			return absolutePath
		}
		return path
	}
	return ""
}
