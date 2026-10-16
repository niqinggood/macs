package workspace

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// ====================== File History — Pre-Edit Backup Tracker ======================
// Complements SnapshotManager (full-session snapshots) with lightweight per-edit
// versioned backups. Called BEFORE each write/edit to enable fine-grained undo.

// FileHistoryVersion represents one backup of a file.
type FileHistoryVersion struct {
	Version    int       `json:"version"`
	Path       string    `json:"path"`
	BackupPath string    `json:"backup_path"`
	Size       int64     `json:"size"`
	CreatedAt  time.Time `json:"created_at"`
}

// FileHistoryManager manages versioned pre-edit file backups.
type FileHistoryManager struct {
	mu            sync.Mutex
	backupDir     string
	sessionID     string
	versions      map[string][]*FileHistoryVersion // absPath → versions
	pendingBackup map[string]bool                  // files already backed up this round
}

func NewFileHistoryManager(workspace, sessionID string) *FileHistoryManager {
	dir := filepath.Join(workspace, ".openkitty", "file-history", sessionID)
	os.MkdirAll(dir, 0755)
	return &FileHistoryManager{
		backupDir:     dir,
		sessionID:     sessionID,
		versions:      make(map[string][]*FileHistoryVersion),
		pendingBackup: make(map[string]bool),
	}
}

// BackupDir returns the run-scoped directory that stores pre-edit versions.
func (m *FileHistoryManager) BackupDir() string {
	if m == nil {
		return ""
	}
	return m.backupDir
}

// BackupBeforeEdit creates a pre-edit backup of the file. Call BEFORE write/edit.
func (m *FileHistoryManager) BackupBeforeEdit(filePath string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	absPath, err := filepath.Abs(filePath)
	if err != nil {
		return err
	}

	info, err := os.Stat(absPath)
	if err != nil {
		return nil // file doesn't exist yet, no backup needed
	}

	if m.pendingBackup[absPath] {
		return nil
	}
	m.pendingBackup[absPath] = true

	hash := sha256.Sum256([]byte(absPath))
	hashPrefix := fmt.Sprintf("%x", hash[:8])
	version := len(m.versions[absPath]) + 1
	backupName := fmt.Sprintf("%s@v%d", hashPrefix, version)
	backupPath := filepath.Join(m.backupDir, backupName)

	data, err := os.ReadFile(absPath)
	if err != nil {
		return err
	}
	if err := os.WriteFile(backupPath, data, 0644); err != nil {
		return err
	}

	m.versions[absPath] = append(m.versions[absPath], &FileHistoryVersion{
		Version:    version,
		Path:       absPath,
		BackupPath: backupPath,
		Size:       info.Size(),
		CreatedAt:  time.Now(),
	})
	return nil
}

// UndoLast restores the most recent backup of a file.
func (m *FileHistoryManager) UndoLast(filePath string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	absPath, _ := filepath.Abs(filePath)
	versions := m.versions[absPath]
	if len(versions) == 0 {
		return "", fmt.Errorf("no backup history for %s", filePath)
	}

	last := versions[len(versions)-1]
	backupData, err := os.ReadFile(last.BackupPath)
	if err != nil {
		return "", fmt.Errorf("read backup: %w", err)
	}
	if err := os.WriteFile(absPath, backupData, 0644); err != nil {
		return "", fmt.Errorf("restore: %w", err)
	}

	m.versions[absPath] = versions[:len(versions)-1]
	delete(m.pendingBackup, absPath)

	return fmt.Sprintf("Restored %s to v%d (%d bytes, %s)", filePath, last.Version, last.Size, last.CreatedAt.Format("15:04:05")), nil
}

// GetHistory returns version history for a file.
func (m *FileHistoryManager) GetHistory(filePath string) []*FileHistoryVersion {
	m.mu.Lock()
	defer m.mu.Unlock()
	absPath, _ := filepath.Abs(filePath)
	versions := make([]*FileHistoryVersion, len(m.versions[absPath]))
	copy(versions, m.versions[absPath])
	sort.Slice(versions, func(i, j int) bool { return versions[i].Version > versions[j].Version })
	return versions
}

// ListAll returns all files with history.
func (m *FileHistoryManager) ListAll() map[string]int {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make(map[string]int)
	for path, versions := range m.versions {
		result[path] = len(versions)
	}
	return result
}

// ====================== Run-scoped context ======================

type fileHistoryContextKey struct{}

type fileHistoryContextValue struct {
	manager *FileHistoryManager
}

// withFileHistoryManager binds the manager owned by one LLMAgent run. The
// wrapper deliberately records nil too, so a child run cannot accidentally
// inherit its parent's manager.
func WithFileHistoryManager(ctx context.Context, manager *FileHistoryManager) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, fileHistoryContextKey{}, fileHistoryContextValue{manager: manager})
}

func FileHistoryManagerFromContext(ctx context.Context) *FileHistoryManager {
	if ctx == nil {
		return nil
	}
	value, ok := ctx.Value(fileHistoryContextKey{}).(fileHistoryContextValue)
	if !ok {
		return nil
	}
	return value.manager
}

// PreEditBackup is called by edit/write tools before modifying a file.
func PreEditBackup(filePath string, runContext context.Context) {
	if manager := FileHistoryManagerFromContext(runContext); manager != nil {
		_ = manager.BackupBeforeEdit(filePath)
	}
}

// ClearPending resets the pending backup tracker each API round.
func (m *FileHistoryManager) ClearPending() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pendingBackup = make(map[string]bool)
}
