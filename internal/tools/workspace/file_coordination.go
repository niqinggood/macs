package workspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// ====================== File State Coordination ======================
// 借鉴 Hermes tools/file_state.py — FileStateRegistry
// 防止并行子Agent互相覆盖写入，跟踪每个文件的读/写状态

// FileReadStamp records when a specific agent read a file.
type FileReadStamp struct {
	ReaderID      string    // agent name or "sub_agent:name"
	Path          string    // absolute resolved path
	Mtime         time.Time // file mtime at read time
	Size          int64     // file size at read time
	Exists        bool      // whether the path existed at read time
	ContentSHA256 string    // content identity at read time
	Timestamp     time.Time // when the read occurred
}

type FileFingerprint struct {
	Exists        bool
	Mtime         time.Time
	Size          int64
	ContentSHA256 string
}

// FileStateRegistry provides process-wide file coordination.
// It tracks reads and writes per-resolved-path so write_file/patch
// can detect stale reads before mutating.
type FileStateRegistry struct {
	mu sync.RWMutex

	// Per-path: last read stamps (readerID -> stamp)
	reads map[string]map[string]FileReadStamp // resolvedPath -> readerID -> stamp

	// Per-path: last writer globally
	lastWriter map[string]string // resolvedPath -> writerID

	// Per-path: write timestamp
	lastWriteTime map[string]time.Time // resolvedPath -> timestamp

	// Per-path lock for critical sections (write-after-check)
	locks map[string]*sync.Mutex

	logf func(format string, args ...interface{})
}

var globalFileState = &FileStateRegistry{
	reads:         make(map[string]map[string]FileReadStamp),
	lastWriter:    make(map[string]string),
	lastWriteTime: make(map[string]time.Time),
	locks:         make(map[string]*sync.Mutex),
	logf:          func(format string, args ...interface{}) { log.Printf(format, args...) },
}

// GetFileStateRegistry returns the global registry.
func GetFileStateRegistry() *FileStateRegistry {
	return globalFileState
}

// resolvePath normalizes a path for registry lookup.
func (r *FileStateRegistry) resolvePath(p string) string {
	abs, err := filepath.Abs(p)
	if err != nil {
		return p
	}
	return filepath.Clean(abs)
}

// getLock returns the per-path mutex (lazy-init).
func (r *FileStateRegistry) getLock(resolved string) *sync.Mutex {
	r.mu.Lock()
	defer r.mu.Unlock()
	if lk, ok := r.locks[resolved]; ok {
		return lk
	}
	lk := &sync.Mutex{}
	r.locks[resolved] = lk
	return lk
}

// RecordRead records that a reader read a file. Call after successful read_file.
func (r *FileStateRegistry) RecordRead(path, readerID string) {
	resolved := r.resolvePath(path)
	fingerprint, err := FingerprintFile(resolved)
	if err != nil {
		r.logf("[FileState] RecordRead fingerprint failed for %s: %v", resolved, err)
	}
	now := time.Now()

	r.mu.Lock()
	if r.reads[resolved] == nil {
		r.reads[resolved] = make(map[string]FileReadStamp)
	}
	r.reads[resolved][readerID] = FileReadStamp{
		ReaderID:      readerID,
		Path:          resolved,
		Mtime:         fingerprint.Mtime,
		Size:          fingerprint.Size,
		Exists:        fingerprint.Exists,
		ContentSHA256: fingerprint.ContentSHA256,
		Timestamp:     now,
	}
	r.mu.Unlock()

	r.logf("[FileState] RecordRead: %s by %s (sha256=%s)", resolved, readerID, ShortContentHash(fingerprint.ContentSHA256))
}

// NoteWrite records that a writer wrote to a file. Call after successful write_file/patch.
func (r *FileStateRegistry) NoteWrite(path, writerID string) {
	resolved := r.resolvePath(path)
	now := time.Now()
	fingerprint, err := FingerprintFile(resolved)

	r.mu.Lock()
	r.lastWriter[resolved] = writerID
	r.lastWriteTime[resolved] = now
	if err == nil {
		if r.reads[resolved] == nil {
			r.reads[resolved] = make(map[string]FileReadStamp)
		}
		// The writer knows the final on-disk state, so follow-up edits by the
		// same agent do not require a redundant read. Any later external change
		// still produces a different content fingerprint and is rejected.
		r.reads[resolved][writerID] = FileReadStamp{
			ReaderID:      writerID,
			Path:          resolved,
			Mtime:         fingerprint.Mtime,
			Size:          fingerprint.Size,
			Exists:        fingerprint.Exists,
			ContentSHA256: fingerprint.ContentSHA256,
			Timestamp:     now,
		}
	}
	r.mu.Unlock()

	if err != nil {
		r.logf("[FileState] NoteWrite fingerprint failed for %s: %v", resolved, err)
	}
	r.logf("[FileState] NoteWrite: %s by %s (sha256=%s)", resolved, writerID, ShortContentHash(fingerprint.ContentSHA256))
}

// CheckStale returns an error if the file was modified by someone else since
// the reader last read it. Returns nil if it's safe to write.
func (r *FileStateRegistry) CheckStale(path, readerID string) error {
	result := r.checkBeforeWrite(path, readerID)
	if result.OK {
		return nil
	}
	return fmt.Errorf("%s", result.Message)
}

// LockPath acquires the per-path lock for atomic read-check-write.
// Use when you need to read, check, and write without another agent interleaving.
func (r *FileStateRegistry) LockPath(path string) {
	resolved := r.resolvePath(path)
	lk := r.getLock(resolved)
	lk.Lock()
}

// UnlockPath releases the per-path lock.
func (r *FileStateRegistry) UnlockPath(path string) {
	resolved := r.resolvePath(path)
	lk := r.getLock(resolved)
	lk.Unlock()
}

// GetLastWriter returns the last writer of a file, or empty string.
func (r *FileStateRegistry) GetLastWriter(path string) string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.lastWriter[r.resolvePath(path)]
}

// GetReaders returns all reader IDs for a file.
func (r *FileStateRegistry) GetReaders(path string) []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	resolved := r.resolvePath(path)
	readers, ok := r.reads[resolved]
	if !ok {
		return nil
	}
	ids := make([]string, 0, len(readers))
	for id := range readers {
		if id != "" {
			ids = append(ids, id)
		}
	}
	return ids
}

// Summary returns a human-readable summary of all tracked files.
func (r *FileStateRegistry) Summary() string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if len(r.reads) == 0 && len(r.lastWriter) == 0 {
		return "FileRegistry: empty (no tracked files)"
	}

	out := fmt.Sprintf("FileRegistry: %d files tracked\n", len(r.reads))
	for path, writer := range r.lastWriter {
		writeTime := r.lastWriteTime[path]
		readers := r.GetReaders(path)
		out += fmt.Sprintf("  %s <- %s at %s (readers: %v)\n",
			path, writer, writeTime.Format("15:04:05"), readers)
	}
	return out
}

// readerIDFromCtx extracts a reader/writer identifier from context.
func ReaderIDFromContext(ctx context.Context) string {
	if name, ok := ctx.Value("agent_name").(string); ok && name != "" {
		return name
	}
	if id, ok := ctx.Value("task_id").(string); ok && id != "" {
		return id
	}
	if name, ok := ctx.Value("ae_employee_id").(string); ok && name != "" {
		return "de:" + name
	}
	return "unknown"
}

// StaleCheckResult is returned by CheckBeforeWrite for structured handling.
type StaleCheckResult struct {
	OK             bool
	Path           string
	LastWriter     string
	OurLastRead    time.Time
	LastWrite      time.Time
	ExpectedSHA256 string
	CurrentSHA256  string
	Message        string
}

// MustHaveRead returns an error if the reader has never read this file.
// Used by edit_file to enforce read-before-edit (does not apply to write_file,
// since write_file often creates new files).
func (r *FileStateRegistry) MustHaveRead(path, readerID string) error {
	resolved := r.resolvePath(path)

	r.mu.RLock()
	defer r.mu.RUnlock()

	readerReads, ok := r.reads[resolved]
	if !ok {
		return fmt.Errorf("file has not been read yet: %s. Use read_file first to understand the content before editing.", path)
	}
	if _, ok := readerReads[readerID]; !ok {
		return fmt.Errorf("file has not been read by you: %s. Use read_file first to understand the content before editing.", path)
	}
	return nil
}

// CheckBeforeWrite is a convenience wrapper for the write_file/patch tools.
// It returns a structured result that can be used to warn or block the write.
func (r *FileStateRegistry) CheckBeforeWrite(path, readerID string) StaleCheckResult {
	return r.checkBeforeWrite(path, readerID)
}

func (r *FileStateRegistry) checkBeforeWrite(path, readerID string) StaleCheckResult {
	resolved := r.resolvePath(path)
	r.mu.RLock()
	lastWriter := r.lastWriter[resolved]
	lastWrite := r.lastWriteTime[resolved]
	readerStamp, hasRead := r.reads[resolved][readerID]
	r.mu.RUnlock()

	result := StaleCheckResult{
		OK:             true,
		Path:           resolved,
		LastWriter:     lastWriter,
		LastWrite:      lastWrite,
		OurLastRead:    readerStamp.Timestamp,
		ExpectedSHA256: readerStamp.ContentSHA256,
	}
	if !hasRead {
		// write_file may intentionally create a new file without reading it.
		// Mutators that require a baseline call MustHaveRead before this check.
		return result
	}

	current, err := FingerprintFile(resolved)
	if err != nil {
		result.OK = false
		result.Message = fmt.Sprintf(
			"[STALE FILE] cannot verify the current content of %s: %v. Re-read the file and retry the edit.",
			resolved, err,
		)
		return result
	}
	result.CurrentSHA256 = current.ContentSHA256
	if sameFileFingerprint(readerStamp, current) {
		return result
	}

	modifier := lastWriter
	if modifier == "" || modifier == readerID || !lastWrite.After(readerStamp.Timestamp) {
		modifier = "an external process"
	}
	result.OK = false
	result.Message = fmt.Sprintf(
		"[STALE FILE] %s changed after it was read by %q (expected sha256=%s, current sha256=%s, modifier=%s). "+
			"No changes were written. Re-read the file and recompute the edit against the current content.",
		resolved,
		readerID,
		ShortContentHash(readerStamp.ContentSHA256),
		ShortContentHash(current.ContentSHA256),
		modifier,
	)
	return result
}

func sameFileFingerprint(stamp FileReadStamp, current FileFingerprint) bool {
	if stamp.Exists != current.Exists || stamp.Size != current.Size {
		return false
	}
	return stamp.ContentSHA256 != "" && stamp.ContentSHA256 == current.ContentSHA256
}

func FingerprintFile(path string) (FileFingerprint, error) {
	for attempt := 0; attempt < 2; attempt++ {
		file, err := os.Open(path)
		if os.IsNotExist(err) {
			return FileFingerprint{Exists: false}, nil
		}
		if err != nil {
			return FileFingerprint{}, err
		}
		before, err := file.Stat()
		if err != nil {
			file.Close()
			return FileFingerprint{}, err
		}
		hash := sha256.New()
		_, copyErr := io.Copy(hash, file)
		after, statErr := file.Stat()
		closeErr := file.Close()
		if copyErr != nil {
			return FileFingerprint{}, copyErr
		}
		if statErr != nil {
			return FileFingerprint{}, statErr
		}
		if closeErr != nil {
			return FileFingerprint{}, closeErr
		}
		if before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
			continue
		}
		return FileFingerprint{
			Exists:        true,
			Mtime:         after.ModTime(),
			Size:          after.Size(),
			ContentSHA256: hex.EncodeToString(hash.Sum(nil)),
		}, nil
	}
	return FileFingerprint{}, fmt.Errorf("file changed while its content fingerprint was being calculated")
}

func ShortContentHash(hash string) string {
	if len(hash) <= 12 {
		if hash == "" {
			return "none"
		}
		return hash
	}
	return hash[:12]
}
