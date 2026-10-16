package configvalue

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// ParseError distinguishes malformed configuration from read/access failures.
// Only malformed startup configuration is eligible for automatic recovery.
type ParseError struct {
	Path string
	Err  error
}

func (err *ParseError) Error() string {
	return fmt.Sprintf("解析配置文件失败 %s: %v", err.Path, err.Err)
}

func (err *ParseError) Unwrap() error {
	return err.Err
}

func ResolvePath(path string) string {
	absolute, err := filepath.Abs(path)
	if err == nil {
		return filepath.Clean(absolute)
	}
	return filepath.Clean(path)
}

type DefaultBytesProvider func(configPath string) ([]byte, error)
type RecoveredFileValidator func(configPath string) error
type AtomicFileWriter func(path string, data []byte, permission fs.FileMode) error

// RecoverCorruptFile preserves a malformed file byte-for-byte, installs a
// validated default and restores the original if any later step fails.
func RecoverCorruptFile(
	configPath string,
	defaultBytes DefaultBytesProvider,
	validate RecoveredFileValidator,
	write AtomicFileWriter,
) (string, error) {
	configPath = ResolvePath(configPath)
	if defaultBytes == nil || validate == nil || write == nil {
		return "", fmt.Errorf("config recovery dependencies are incomplete")
	}
	replacement, err := defaultBytes(configPath)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(configPath)
	if err != nil {
		return "", fmt.Errorf("inspect corrupt config %q: %w", configPath, err)
	}
	backupPath, err := nextCorruptBackupPath(configPath, time.Now())
	if err != nil {
		return "", err
	}
	if err := os.Rename(configPath, backupPath); err != nil {
		return "", fmt.Errorf("backup corrupt config %q to %q: %w", configPath, backupPath, err)
	}

	permission := info.Mode().Perm()
	if permission == 0 {
		permission = 0o600
	}
	if err := write(configPath, replacement, permission); err != nil {
		if rollbackErr := restoreCorruptFile(configPath, backupPath); rollbackErr != nil {
			return backupPath, fmt.Errorf("write default config %q: %v; restore original: %w", configPath, err, rollbackErr)
		}
		return "", fmt.Errorf("write default config %q: %w (original restored)", configPath, err)
	}
	if err := validate(configPath); err != nil {
		if rollbackErr := restoreCorruptFile(configPath, backupPath); rollbackErr != nil {
			return backupPath, fmt.Errorf("verify regenerated config %q: %v; restore original: %w", configPath, err, rollbackErr)
		}
		return "", fmt.Errorf("verify regenerated config %q: %w (original restored)", configPath, err)
	}
	return backupPath, nil
}

func nextCorruptBackupPath(configPath string, now time.Time) (string, error) {
	base := configPath + ".broken-" + now.Format("20060102-150405.000000000") + ".bak"
	for attempt := 0; attempt < 1000; attempt++ {
		candidate := base
		if attempt > 0 {
			candidate = fmt.Sprintf("%s-%d", base, attempt)
		}
		_, err := os.Stat(candidate)
		switch {
		case errors.Is(err, os.ErrNotExist):
			return candidate, nil
		case err != nil:
			return "", fmt.Errorf("inspect config backup path %q: %w", candidate, err)
		}
	}
	return "", fmt.Errorf("cannot allocate backup path for %q", configPath)
}

func restoreCorruptFile(configPath, backupPath string) error {
	if err := os.Remove(configPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove regenerated config %q: %w", configPath, err)
	}
	if err := os.Rename(backupPath, configPath); err != nil {
		return fmt.Errorf("rename backup %q to %q: %w", backupPath, configPath, err)
	}
	return nil
}
