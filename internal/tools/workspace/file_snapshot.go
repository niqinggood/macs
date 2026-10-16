package workspace

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"mac/internal/fsutil"
	"mac/internal/textutil"
)

type deferSnapshotTurnContextKey struct{}

func WithDeferredSnapshotTurn(ctx context.Context) context.Context {
	return context.WithValue(ctx, deferSnapshotTurnContextKey{}, true)
}

func SnapshotTurnDeferred(ctx context.Context) bool {
	deferred, _ := ctx.Value(deferSnapshotTurnContextKey{}).(bool)
	return deferred
}

// FileSnapshot records the state of a file before modification.
type FileSnapshot struct {
	OriginalPath string    `json:"original_path"`
	SnapshotPath string    `json:"snapshot_path"`
	ToolName     string    `json:"tool_name"` // write_file, edit_file
	Existed      bool      `json:"existed"`
	FileMode     uint32    `json:"file_mode,omitempty"`
	FileSize     int64     `json:"file_size"`
	CreatedAt    time.Time `json:"created_at"`
	Restored     bool      `json:"restored"`
	Turn         int       `json:"turn"` // which conversation turn this snapshot belongs to
}

// TurnSummary summarizes all file changes in a single turn.
type TurnSummary struct {
	Turn      int       `json:"turn"`
	FileCount int       `json:"file_count"`
	Files     []string  `json:"files"`
	CreatedAt time.Time `json:"created_at"`
}

// SnapshotManager manages file snapshots for undo capability.
// Claude Code borrowed: fileHistoryMakeSnapshot in utils/fileHistory.ts
type SnapshotManager struct {
	mu          sync.Mutex
	baseDir     string
	sessionID   string
	snapshots   []*FileSnapshot
	currentTurn int
	turnHistory []TurnSummary
}

type snapshotManifest struct {
	Version     int             `json:"version"`
	SessionID   string          `json:"session_id"`
	CurrentTurn int             `json:"current_turn"`
	Snapshots   []*FileSnapshot `json:"snapshots"`
	TurnHistory []TurnSummary   `json:"turn_history"`
}

func NewSnapshotManager(baseDir, sessionID string) *SnapshotManager {
	dir := filepath.Join(baseDir, ".openkitty", "snapshots", sessionID)
	os.MkdirAll(dir, 0755)
	manager := &SnapshotManager{
		baseDir:   baseDir,
		sessionID: sessionID,
	}
	if sessionID != "" {
		if err := manager.loadManifest(filepath.Join(dir, "manifest.json")); err != nil && !os.IsNotExist(err) {
			log.Printf("[SNAPSHOT] Failed to load session manifest %s: %v", sessionID, err)
		}
	}
	return manager
}

// SnapshotDir returns the directory where snapshots are stored.
func (sm *SnapshotManager) SnapshotDir() string {
	return filepath.Join(sm.baseDir, ".openkitty", "snapshots", sm.sessionID)
}

func (sm *SnapshotManager) manifestPath() string {
	return filepath.Join(sm.SnapshotDir(), "manifest.json")
}

func (sm *SnapshotManager) persistLocked() {
	if sm == nil || sm.sessionID == "" {
		return
	}
	manifest := snapshotManifest{
		Version:     1,
		SessionID:   sm.sessionID,
		CurrentTurn: sm.currentTurn,
		Snapshots:   sm.snapshots,
		TurnHistory: sm.turnHistory,
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		log.Printf("[SNAPSHOT] marshal manifest failed: %v", err)
		return
	}
	if err := os.MkdirAll(sm.SnapshotDir(), 0755); err != nil {
		log.Printf("[SNAPSHOT] create manifest directory failed: %v", err)
		return
	}
	if err := fsutil.WriteAtomic(sm.manifestPath(), data, 0644); err != nil {
		log.Printf("[SNAPSHOT] persist manifest failed: %v", err)
	}
}

func (sm *SnapshotManager) loadManifest(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var manifest snapshotManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return err
	}
	if manifest.Version > 1 {
		return fmt.Errorf("snapshot manifest version %d is not supported", manifest.Version)
	}
	if manifest.SessionID != "" && sm.sessionID != "" && manifest.SessionID != sm.sessionID {
		return fmt.Errorf("snapshot manifest belongs to session %s", manifest.SessionID)
	}
	sm.snapshots = manifest.Snapshots
	sm.currentTurn = manifest.CurrentTurn
	sm.turnHistory = manifest.TurnHistory
	return nil
}

// SaveSnapshot saves a copy of a file before modification.
func (sm *SnapshotManager) SaveSnapshot(filePath, toolName string) (*FileSnapshot, error) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if absolute, err := filepath.Abs(filePath); err == nil {
		filePath = absolute
	}
	filePath = filepath.Clean(filePath)

	// Check if file exists
	info, err := os.Stat(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			// Record a tombstone so undo removes a file created by this tool.
			snap := &FileSnapshot{
				OriginalPath: filePath,
				ToolName:     toolName,
				Existed:      false,
				CreatedAt:    time.Now(),
				Turn:         sm.currentTurn,
			}
			sm.snapshots = append(sm.snapshots, snap)
			sm.persistLocked()
			return snap, nil
		}
		return nil, fmt.Errorf("stat '%s': %w", filePath, err)
	}

	// Skip directories
	if info.IsDir() {
		return nil, nil
	}

	// Read original content
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("read '%s': %w", filePath, err)
	}

	// Create snapshot filename: {original_basename}_{timestamp}.snap
	base := filepath.Base(filePath)
	snapName := fmt.Sprintf("%s_%d.snap", base, time.Now().UnixNano())
	snapPath := filepath.Join(sm.SnapshotDir(), snapName)

	if err := os.MkdirAll(sm.SnapshotDir(), 0755); err != nil {
		return nil, fmt.Errorf("create snapshot directory: %w", err)
	}
	if err := os.WriteFile(snapPath, data, 0644); err != nil {
		return nil, fmt.Errorf("write snapshot: %w", err)
	}

	snap := &FileSnapshot{
		OriginalPath: filePath,
		SnapshotPath: snapPath,
		ToolName:     toolName,
		Existed:      true,
		FileMode:     uint32(info.Mode().Perm()),
		FileSize:     info.Size(),
		CreatedAt:    time.Now(),
		Turn:         sm.currentTurn,
	}
	sm.snapshots = append(sm.snapshots, snap)
	sm.persistLocked()

	return snap, nil
}

// FinalizeSnapshots removes pre-mutation records whose target did not actually
// change. Run it after every tool attempt so partial writes remain recoverable
// while rejected and no-op calls do not pollute checkpoint history.
func (sm *SnapshotManager) FinalizeSnapshots(candidates []*FileSnapshot) {
	if sm == nil || len(candidates) == 0 {
		return
	}
	sm.mu.Lock()
	defer sm.mu.Unlock()

	candidateSet := make(map[*FileSnapshot]bool, len(candidates))
	for _, candidate := range candidates {
		if candidate != nil {
			candidateSet[candidate] = true
		}
	}
	kept := sm.snapshots[:0]
	for _, snapshot := range sm.snapshots {
		if !candidateSet[snapshot] || SnapshotChanged(snapshot) {
			kept = append(kept, snapshot)
			continue
		}
		if snapshot.SnapshotPath != "" {
			_ = os.Remove(snapshot.SnapshotPath)
		}
	}
	sm.snapshots = kept
	sm.persistLocked()
}

func SnapshotChanged(snapshot *FileSnapshot) bool {
	if snapshot == nil {
		return false
	}
	info, err := os.Stat(snapshot.OriginalPath)
	if !snapshot.Existed {
		return err == nil
	}
	if err != nil || info.IsDir() {
		return true
	}
	original, err := os.ReadFile(snapshot.SnapshotPath)
	if err != nil {
		// Keep evidence rather than silently lose a possibly recoverable change.
		return true
	}
	current, err := os.ReadFile(snapshot.OriginalPath)
	if err != nil {
		return true
	}
	if string(original) != string(current) {
		return true
	}
	return snapshot.FileMode != 0 && uint32(info.Mode().Perm()) != snapshot.FileMode
}

// EndTurn marks the end of a conversation turn. All snapshots taken since the last
// EndTurn() call are grouped together. Call this after the agent finishes responding.
func (sm *SnapshotManager) EndTurn() {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	// Collect files modified in this turn
	var files []string
	seen := make(map[string]bool)
	for i := len(sm.snapshots) - 1; i >= 0; i-- {
		s := sm.snapshots[i]
		if s.Turn != sm.currentTurn {
			break
		}
		if !seen[s.OriginalPath] {
			seen[s.OriginalPath] = true
			files = append(files, s.OriginalPath)
		}
	}

	if len(files) > 0 {
		sm.turnHistory = append(sm.turnHistory, TurnSummary{
			Turn:      sm.currentTurn,
			FileCount: len(files),
			Files:     files,
			CreatedAt: time.Now(),
		})
	}

	sm.currentTurn++
	sm.persistLocked()
	log.Printf("[SNAPSHOT] Turn %d ended — %d files modified", sm.currentTurn-1, len(files))
}

// CurrentTurn returns the current turn number.
func (sm *SnapshotManager) CurrentTurn() int {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	return sm.currentTurn
}

// ListTurns returns a summary of all recorded turns.
func (sm *SnapshotManager) ListTurns() []TurnSummary {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	result := make([]TurnSummary, len(sm.turnHistory))
	copy(result, sm.turnHistory)
	return result
}

// RewindToTurn restores all files to their state at the START of the given turn.
// This effectively undoes all changes from turn N onward.
func (sm *SnapshotManager) RewindToTurn(targetTurn int) (int, error) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	// Find the earliest preimage for each file changed from targetTurn onward.
	restoreMap := make(map[string]*FileSnapshot)
	for _, s := range sm.snapshots {
		if s.Turn < targetTurn || s.Restored {
			continue
		}
		if _, ok := restoreMap[s.OriginalPath]; !ok {
			restoreMap[s.OriginalPath] = s
		}
	}

	if len(restoreMap) == 0 {
		return 0, fmt.Errorf("no snapshots found from turn %d onward", targetTurn)
	}

	restored := 0
	for _, snap := range restoreMap {
		if err := restoreFileSnapshot(snap); err != nil {
			continue
		}
		for _, candidate := range sm.snapshots {
			if candidate.OriginalPath == snap.OriginalPath && candidate.Turn >= targetTurn {
				candidate.Restored = true
			}
		}
		restored++
		log.Printf("[SNAPSHOT] Rewound '%s' to turn %d state", snap.OriginalPath, targetTurn)
	}

	// Continuing after rewind starts a new history branch at targetTurn.
	keptTurns := sm.turnHistory[:0]
	for _, turn := range sm.turnHistory {
		if turn.Turn < targetTurn {
			keptTurns = append(keptTurns, turn)
		}
	}
	sm.turnHistory = keptTurns
	sm.currentTurn = targetTurn

	sm.persistLocked()
	return restored, nil
}

// RestoreSnapshot restores a file from its snapshot.
func (sm *SnapshotManager) RestoreSnapshot(originalPath string) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	if absolute, err := filepath.Abs(originalPath); err == nil {
		originalPath = absolute
	}
	originalPath = filepath.Clean(originalPath)

	// Find the latest snapshot for this file
	var target *FileSnapshot
	for i := len(sm.snapshots) - 1; i >= 0; i-- {
		if sm.snapshots[i].OriginalPath == originalPath && !sm.snapshots[i].Restored {
			target = sm.snapshots[i]
			break
		}
	}

	if target == nil {
		return fmt.Errorf("no snapshot found for '%s'", originalPath)
	}

	if err := restoreFileSnapshot(target); err != nil {
		return err
	}

	target.Restored = true
	sm.persistLocked()
	log.Printf("[SNAPSHOT] Restored '%s' from snapshot", originalPath)
	return nil
}

// ListSnapshots returns all snapshots for the current session.
func (sm *SnapshotManager) ListSnapshots() []*FileSnapshot {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	result := make([]*FileSnapshot, len(sm.snapshots))
	copy(result, sm.snapshots)
	return result
}

// LastSnapshot returns the most recent snapshot.
func (sm *SnapshotManager) LastSnapshot() *FileSnapshot {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	if len(sm.snapshots) == 0 {
		return nil
	}
	return sm.snapshots[len(sm.snapshots)-1]
}

// LoadSnapshots loads snapshots from a previous session directory.
func (sm *SnapshotManager) LoadSnapshots(sessionDir string) (int, error) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	if err := sm.loadManifest(filepath.Join(sessionDir, "manifest.json")); err == nil {
		return len(sm.snapshots), nil
	} else if !os.IsNotExist(err) {
		return 0, err
	}
	entries, err := os.ReadDir(sessionDir)
	if err != nil {
		return 0, err
	}
	count := 0
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".snap") {
			continue
		}
		snapPath := filepath.Join(sessionDir, entry.Name())
		info, err := entry.Info()
		if err != nil {
			continue
		}
		// Reconstruct original path from snapshot name: {orig}_{timestamp}.snap
		name := strings.TrimSuffix(entry.Name(), ".snap")
		parts := strings.SplitN(name, "_", 2)
		origName := name
		if len(parts) > 0 {
			origName = parts[0]
		}
		snap := &FileSnapshot{
			OriginalPath: origName,
			SnapshotPath: snapPath,
			ToolName:     "loaded",
			Existed:      true,
			FileMode:     uint32(info.Mode().Perm()),
			FileSize:     info.Size(),
			CreatedAt:    info.ModTime(),
		}
		sm.snapshots = append(sm.snapshots, snap)
		count++
	}
	return count, nil
}

// Cleanup removes all snapshot files for this session.
func (sm *SnapshotManager) Cleanup() {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	dir := sm.SnapshotDir()
	os.RemoveAll(dir)
	sm.snapshots = nil
	sm.turnHistory = nil
	sm.currentTurn = 0
	log.Printf("[SNAPSHOT] Cleaned up session '%s'", sm.sessionID)
}

// SnapshotSummary returns a human-readable summary of snapshots.
func (sm *SnapshotManager) SnapshotSummary() string {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if len(sm.snapshots) == 0 {
		return "No snapshots taken this session."
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Snapshots for session %s:\n", sm.sessionID))
	for _, s := range sm.snapshots {
		status := "active"
		if s.Restored {
			status = "restored"
		}
		changeType := s.ToolName
		if !s.Existed {
			changeType += " (created)"
		}
		sb.WriteString(fmt.Sprintf("  [%s] %s ← %s (%d bytes, %s)\n",
			status, s.OriginalPath, changeType, s.FileSize, s.CreatedAt.Format("15:04:05")))
	}
	return sb.String()
}

// DiffSnapshot computes the difference between a snapshot and the current file.
func (sm *SnapshotManager) DiffSnapshot(originalPath string) (string, error) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	if absolute, err := filepath.Abs(originalPath); err == nil {
		originalPath = absolute
	}
	originalPath = filepath.Clean(originalPath)
	var target *FileSnapshot
	for i := len(sm.snapshots) - 1; i >= 0; i-- {
		if sm.snapshots[i].OriginalPath == originalPath && !sm.snapshots[i].Restored {
			target = sm.snapshots[i]
			break
		}
	}
	if target == nil {
		return "", fmt.Errorf("no snapshot found for '%s'", originalPath)
	}
	current, err := os.ReadFile(originalPath)
	if err != nil {
		return "", fmt.Errorf("read current: %w", err)
	}
	if !target.Existed {
		return computeDiff("", string(current)), nil
	}
	snapshotData, err := os.ReadFile(target.SnapshotPath)
	if err != nil {
		return "", fmt.Errorf("read snapshot: %w", err)
	}
	return computeDiff(string(snapshotData), string(current)), nil
}

func computeDiff(oldContent, newContent string) string {
	oldLines := SplitLines(oldContent)
	newLines := SplitLines(newContent)
	var result strings.Builder
	result.WriteString(fmt.Sprintf("Diff: %d -> %d lines\n", len(oldLines), len(newLines)))
	maxLen := len(oldLines)
	if len(newLines) > maxLen {
		maxLen = len(newLines)
	}
	changes := 0
	for i := 0; i < maxLen; i++ {
		var oldLine, newLine string
		if i < len(oldLines) {
			oldLine = oldLines[i]
		}
		if i < len(newLines) {
			newLine = newLines[i]
		}
		if oldLine != newLine {
			changes++
			if changes <= 20 {
				if oldLine != "" && newLine != "" {
					result.WriteString(fmt.Sprintf("  L%d: -%s\n", i+1, textutil.Truncate(oldLine, 100)))
					result.WriteString(fmt.Sprintf("  L%d: +%s\n", i+1, textutil.Truncate(newLine, 100)))
				} else if newLine != "" {
					result.WriteString(fmt.Sprintf("  L%d: +%s\n", i+1, textutil.Truncate(newLine, 100)))
				} else {
					result.WriteString(fmt.Sprintf("  L%d: -%s\n", i+1, textutil.Truncate(oldLine, 100)))
				}
			}
		}
	}
	if changes > 20 {
		result.WriteString(fmt.Sprintf("  ... (%d more changes) ...\n", changes-20))
	}
	result.WriteString(fmt.Sprintf("Total: %d changed lines\n", changes))
	return result.String()
}

func SplitLines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

// BatchRestore restores all files that have snapshots.
func (sm *SnapshotManager) BatchRestore() (int, error) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	restoreMap := make(map[string]*FileSnapshot)
	for _, snap := range sm.snapshots {
		if snap.Restored {
			continue
		}
		if _, exists := restoreMap[snap.OriginalPath]; !exists {
			restoreMap[snap.OriginalPath] = snap
		}
	}
	restored := 0
	for _, snap := range restoreMap {
		if err := restoreFileSnapshot(snap); err != nil {
			continue
		}
		for _, candidate := range sm.snapshots {
			if candidate.OriginalPath == snap.OriginalPath {
				candidate.Restored = true
			}
		}
		restored++
	}
	sm.persistLocked()
	return restored, nil
}

// ExportSnapshots saves all snapshots to a portable archive directory.
func (sm *SnapshotManager) ExportSnapshots(exportDir string) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	os.MkdirAll(exportDir, 0755)
	for _, snap := range sm.snapshots {
		if snap.Restored {
			continue
		}
		if !snap.Existed {
			continue
		}
		data, err := os.ReadFile(snap.SnapshotPath)
		if err != nil {
			return err
		}
		destName := snap.CreatedAt.Format("150405") + "_" + filepath.Base(snap.OriginalPath) + ".snap"
		if err := os.WriteFile(filepath.Join(exportDir, destName), data, 0644); err != nil {
			return err
		}
	}
	return nil
}

func restoreFileSnapshot(snapshot *FileSnapshot) error {
	if snapshot == nil {
		return fmt.Errorf("snapshot is nil")
	}
	if !snapshot.Existed {
		info, err := os.Lstat(snapshot.OriginalPath)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("inspect created path: %w", err)
		}
		if info.IsDir() {
			return fmt.Errorf("refusing to remove directory recorded as a created file: %s", snapshot.OriginalPath)
		}
		if err := os.Remove(snapshot.OriginalPath); err != nil {
			return fmt.Errorf("remove created file: %w", err)
		}
		return nil
	}

	data, err := os.ReadFile(snapshot.SnapshotPath)
	if err != nil {
		return fmt.Errorf("read snapshot: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(snapshot.OriginalPath), 0755); err != nil {
		return fmt.Errorf("create restore directory: %w", err)
	}
	mode := os.FileMode(snapshot.FileMode)
	if mode == 0 {
		mode = 0644
	}
	if err := os.WriteFile(snapshot.OriginalPath, data, mode); err != nil {
		return fmt.Errorf("restore file: %w", err)
	}
	if err := os.Chmod(snapshot.OriginalPath, mode); err != nil {
		return fmt.Errorf("restore file mode: %w", err)
	}
	return nil
}

// ==================== undo_snapshot tool ====================

type UndoSnapshotTool struct {
	manager *SnapshotManager
}

func NewUndoSnapshotTool(manager *SnapshotManager) *UndoSnapshotTool {
	return &UndoSnapshotTool{manager: manager}
}

func (t *UndoSnapshotTool) Name() string { return "undo_snapshot" }

func (t *UndoSnapshotTool) Description() string {
	return "Restore files from snapshots. " +
		"Actions: " +
		"restore(path) — restore a single file to its pre-edit state; " +
		"list — show all saved turns and modified files; " +
		"rewind(turn) — restore ALL files to their state at the start of a given turn, undoing all changes since; " +
		"diff(path) — show the difference between snapshot and current file."
}

func (t *UndoSnapshotTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"action": map[string]interface{}{
				"type":        "string",
				"description": "Action: restore (single file), rewind (all files to a turn), list (show turn history), diff (compare)",
				"enum":        []string{"restore", "rewind", "list", "diff"},
			},
			"path": map[string]interface{}{
				"type":        "string",
				"description": "File path (for restore/diff actions)",
			},
			"turn": map[string]interface{}{
				"type":        "number",
				"description": "Turn number to rewind to (for rewind action)",
			},
		},
		"required": []string{"action"},
	}
}

func (t *UndoSnapshotTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	if t.manager == nil {
		return "Error: snapshot manager not available", nil
	}

	action, _ := params["action"].(string)
	if action == "" {
		action = "restore"
	}

	switch action {
	case "list":
		turns := t.manager.ListTurns()
		if len(turns) == 0 {
			return "No file changes recorded in this session.", nil
		}
		var sb strings.Builder
		sb.WriteString("## Turn History\n\n")
		for _, turn := range turns {
			sb.WriteString(fmt.Sprintf("### Turn %d (%d files)\n", turn.Turn, turn.FileCount))
			for _, f := range turn.Files {
				sb.WriteString(fmt.Sprintf("- %s\n", f))
			}
			sb.WriteString("\n")
		}
		return sb.String(), nil

	case "rewind":
		turnNum := 0
		if n, ok := params["turn"].(float64); ok {
			turnNum = int(n)
		}
		if turnNum <= 0 {
			// Default: rewind to the beginning (undo ALL edits)
			turnNum = -1
		}
		if turnNum == -1 {
			// Rewind all: restore every file from its earliest snapshot
			count, err := t.manager.BatchRestore()
			if err != nil {
				return fmt.Sprintf("Error: %v", err), nil
			}
			return fmt.Sprintf("Rewound all %d files to their pre-edit state.", count), nil
		}
		count, err := t.manager.RewindToTurn(turnNum)
		if err != nil {
			return fmt.Sprintf("Error: %v", err), nil
		}
		return fmt.Sprintf("Rewound %d files to turn %d state.", count, turnNum), nil

	case "diff":
		path, _ := params["path"].(string)
		if path == "" {
			return "Error: path is required for diff action", nil
		}
		diff, err := t.manager.DiffSnapshot(path)
		if err != nil {
			return fmt.Sprintf("Error: %v", err), nil
		}
		return diff, nil

	default: // restore
		path, _ := params["path"].(string)
		if path == "" {
			return "Error: path is required for restore action", nil
		}
		if err := t.manager.RestoreSnapshot(path); err != nil {
			return fmt.Sprintf("Error restoring snapshot: %v", err), nil
		}
		return fmt.Sprintf("Restored '%s' from snapshot.", path), nil
	}
}
