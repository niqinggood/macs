package session

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"mac/internal/fsutil"
)

// TranscriptEntry is a single entry in the session transcript.
type TranscriptEntry struct {
	Timestamp   time.Time              `json:"timestamp"`
	Type        string                 `json:"type"` // "user", "assistant", "tool_call", "tool_result", "system", "thinking"
	AgentName   string                 `json:"agent,omitempty"`
	Content     string                 `json:"content,omitempty"`
	ToolName    string                 `json:"tool,omitempty"`
	ToolCallID  string                 `json:"tool_call_id,omitempty"`
	ToolParams  map[string]interface{} `json:"params,omitempty"`
	ToolResult  string                 `json:"result,omitempty"`
	ToolSuccess *bool                  `json:"success,omitempty"`
	DurationMs  int64                  `json:"duration_ms,omitempty"`
}

const (
	sessionJournalVersion          = 1
	maxSessionJournalRecoveryBytes = 64 << 20
)

// SessionJournalEvent is one durable, append-only lifecycle record. The JSONL
// journal is the crash-recovery source of truth; the final transcript JSON is a
// compact, human-friendly projection written when the turn finishes.
type SessionJournalEvent struct {
	Version       int                    `json:"version"`
	Sequence      uint64                 `json:"sequence"`
	Timestamp     time.Time              `json:"timestamp"`
	SessionID     string                 `json:"session_id"`
	AgentName     string                 `json:"agent_name"`
	Event         string                 `json:"event"`
	Entry         *TranscriptEntry       `json:"entry,omitempty"`
	Status        string                 `json:"status,omitempty"`
	FinalOutput   string                 `json:"final_output,omitempty"`
	InternalRecap string                 `json:"internal_recap,omitempty"`
	Checkpoint    *TurnCheckpointJournal `json:"checkpoint,omitempty"`
}

// SessionTranscript records the full session history for replay and debugging.
// Claude Code borrowed: session transcript in utils/sessionStorage.ts
type SessionTranscript struct {
	mu             sync.RWMutex      `json:"-"`
	SessionID      string            `json:"session_id"`
	AgentName      string            `json:"agent_name"`
	StartTime      time.Time         `json:"start_time"`
	EndTime        time.Time         `json:"end_time,omitempty"`
	Entries        []TranscriptEntry `json:"entries"`
	FinalOutput    string            `json:"final_output,omitempty"`
	InternalRecap  string            `json:"internal_recap,omitempty"`
	Status         string            `json:"status"` // "running", "success", "failed"
	Checkpoint     *TurnCheckpoint   `json:"turn_checkpoint,omitempty"`
	filePath       string
	journalPath    string
	checkpointPath string
	journalSeq     uint64
	journalErr     error
}

// TranscriptSnapshot is an immutable copy suitable for cross-package
// inspection without exposing the transcript's synchronization fields.
type TranscriptSnapshot struct {
	SessionID  string
	AgentName  string
	Status     string
	Journal    string
	Entries    []TranscriptEntry
	Checkpoint *TurnCheckpoint
}

func (t *SessionTranscript) Snapshot() TranscriptSnapshot {
	if t == nil {
		return TranscriptSnapshot{}
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	return TranscriptSnapshot{
		SessionID:  t.SessionID,
		AgentName:  t.AgentName,
		Status:     t.Status,
		Journal:    t.journalPath,
		Entries:    append([]TranscriptEntry(nil), t.Entries...),
		Checkpoint: CloneTurnCheckpoint(t.Checkpoint),
	}
}

func NewSessionTranscript(sessionID, agentName, outputDir string) *SessionTranscript {
	now := time.Now()
	baseName := fmt.Sprintf("transcript_%s_%s", SafeTranscriptFileID(sessionID), now.Format("20060102_150405.000000000"))
	transcript := &SessionTranscript{
		SessionID:      sessionID,
		AgentName:      agentName,
		StartTime:      now,
		Entries:        make([]TranscriptEntry, 0),
		Status:         "running",
		filePath:       filepath.Join(outputDir, baseName+".json"),
		journalPath:    filepath.Join(outputDir, baseName+".jsonl"),
		checkpointPath: filepath.Join(outputDir, baseName+".checkpoint.json"),
	}
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		transcript.journalErr = fmt.Errorf("create transcript directory: %w", err)
		return transcript
	}
	transcript.appendJournalEventLocked(SessionJournalEvent{Event: "session_start", Status: "running"})
	return transcript
}

func (t *SessionTranscript) recordEntry(entry TranscriptEntry) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.Entries = append(t.Entries, entry)
	journalEntry := entry
	t.appendJournalEventLocked(SessionJournalEvent{Event: entry.Type, Entry: &journalEntry})
}

// RecordUserMessage adds a user message to the transcript.
func (t *SessionTranscript) RecordUserMessage(content string) {
	t.recordEntry(TranscriptEntry{
		Timestamp: time.Now(),
		Type:      "user",
		Content:   content,
	})
}

// RecordAssistantMessage adds an assistant response.
func (t *SessionTranscript) RecordAssistantMessage(content string) {
	t.recordEntry(TranscriptEntry{
		Timestamp: time.Now(),
		Type:      "assistant",
		AgentName: t.AgentName,
		Content:   content,
	})
}

// RecordThinking adds a thinking/reasoning entry.
func (t *SessionTranscript) RecordThinking(content string) {
	t.recordEntry(TranscriptEntry{
		Timestamp: time.Now(),
		Type:      "thinking",
		AgentName: t.AgentName,
		Content:   content,
	})
}

// RecordToolCall adds a tool invocation.
func (t *SessionTranscript) RecordToolCall(toolName string, params map[string]interface{}, callID ...string) {
	params = CloneTranscriptParams(params)
	id := ""
	if len(callID) > 0 {
		id = callID[0]
	}
	t.recordEntry(TranscriptEntry{
		Timestamp:  time.Now(),
		Type:       "tool_call",
		AgentName:  t.AgentName,
		ToolName:   toolName,
		ToolCallID: id,
		ToolParams: params,
	})
}

// RecordToolResult adds a tool execution result.
func (t *SessionTranscript) RecordToolResult(toolName string, result string, success bool, durationMs int64, callID ...string) {
	id := ""
	if len(callID) > 0 {
		id = callID[0]
	}
	t.recordEntry(TranscriptEntry{
		Timestamp:   time.Now(),
		Type:        "tool_result",
		AgentName:   t.AgentName,
		ToolName:    toolName,
		ToolCallID:  id,
		ToolResult:  truncate(result, 2000),
		ToolSuccess: &success,
		DurationMs:  durationMs,
	})
}

// RecordSystemEvent adds a system event.
func (t *SessionTranscript) RecordSystemEvent(message string) {
	t.RecordLifecycleEvent("system", message)
}

// RecordLifecycleEvent records model/compaction/recovery state transitions.
func (t *SessionTranscript) RecordLifecycleEvent(eventType, message string) {
	eventType = strings.TrimSpace(eventType)
	if eventType == "" {
		eventType = "system"
	}
	t.recordEntry(TranscriptEntry{
		Timestamp: time.Now(),
		Type:      eventType,
		AgentName: t.AgentName,
		Content:   message,
	})
}

func (t *SessionTranscript) appendJournalEventLocked(event SessionJournalEvent) {
	if t == nil || strings.TrimSpace(t.journalPath) == "" {
		return
	}
	t.journalSeq++
	event.Version = sessionJournalVersion
	event.Sequence = t.journalSeq
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now()
	}
	event.SessionID = t.SessionID
	event.AgentName = t.AgentName

	data, err := json.Marshal(event)
	if err != nil {
		t.recordJournalErrorLocked(fmt.Errorf("marshal journal event: %w", err))
		return
	}
	data = append(data, '\n')
	file, err := os.OpenFile(t.journalPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		t.recordJournalErrorLocked(fmt.Errorf("open session journal: %w", err))
		return
	}
	if _, err := file.Write(data); err != nil {
		t.recordJournalErrorLocked(fmt.Errorf("append session journal: %w", err))
		_ = file.Close()
		return
	}
	// A completed write followed by close survives ordinary process crashes.
	// Per-event FlushFileBuffers is prohibitively slow on Windows, so reserve
	// power-loss-level durability for deployments that explicitly request it.
	if transcriptJournalFsyncEnabled() {
		if err := file.Sync(); err != nil {
			t.recordJournalErrorLocked(fmt.Errorf("sync session journal: %w", err))
		}
	}
	if err := file.Close(); err != nil {
		t.recordJournalErrorLocked(fmt.Errorf("close session journal: %w", err))
	}
}

func (t *SessionTranscript) recordJournalErrorLocked(err error) {
	if err == nil {
		return
	}
	if t.journalErr == nil {
		t.journalErr = err
	}
	log.Printf("[TRANSCRIPT] journal warning for session %q: %v", t.SessionID, err)
}

// CompactTranscript reduces transcript size by merging consecutive tool_results.
func (t *SessionTranscript) CompactTranscript() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.Entries) <= 100 {
		return 0
	}
	compacted := 0
	var merged []TranscriptEntry
	for i := 0; i < len(t.Entries); i++ {
		entry := t.Entries[i]
		if entry.Type == "tool_result" && i+1 < len(t.Entries) && t.Entries[i+1].Type == "tool_result" {
			compacted++
			continue
		}
		merged = append(merged, entry)
	}
	if len(merged) < len(t.Entries) {
		t.Entries = merged
	}
	return compacted
}

// Finalize completes the transcript and saves to disk.
func (t *SessionTranscript) Finalize(finalOutput string, status string) error {
	return t.FinalizeWithInternalRecap(finalOutput, status, "")
}

// FinalizeWithInternalRecap completes the transcript and keeps model recap/debug
// notes separate from the user-visible final output.
func (t *SessionTranscript) FinalizeWithInternalRecap(finalOutput string, status string, internalRecap string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.EndTime = time.Now()
	t.FinalOutput = finalOutput
	t.InternalRecap = strings.TrimSpace(internalRecap)
	t.Status = status
	t.appendJournalEventLocked(SessionJournalEvent{
		Event:         "session_end",
		Status:        status,
		FinalOutput:   finalOutput,
		InternalRecap: t.InternalRecap,
	})

	data, err := json.MarshalIndent(t, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal transcript: %w", err)
	}

	if err := fsutil.WriteAtomic(t.filePath, data, 0600); err != nil {
		return fmt.Errorf("write transcript: %w", err)
	}

	dur := t.EndTime.Sub(t.StartTime)
	log.Printf("[TRANSCRIPT] Saved %d entries to '%s' (%.1fs)", len(t.Entries), t.filePath, dur.Seconds())
	if t.journalErr != nil {
		return fmt.Errorf("transcript saved but journal was incomplete: %w", t.journalErr)
	}
	return nil
}

func truncate(value string, limit int) string {
	return truncateRunes(value, limit)
}

// FilePath returns the path where the transcript will be saved.
func (t *SessionTranscript) FilePath() string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.filePath
}

// JournalPath returns the append-only JSONL event log path.
func (t *SessionTranscript) JournalPath() string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.journalPath
}

// JournalError reports the first durability failure observed while recording.
func (t *SessionTranscript) JournalError() error {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.journalErr
}

// ReplaySummary returns a human-readable timeline of the session.
func (t *SessionTranscript) ReplaySummary() string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	var result string
	result += fmt.Sprintf("Session: %s | Agent: %s | Duration: %.1fs\n",
		t.SessionID, t.AgentName, t.EndTime.Sub(t.StartTime).Seconds())
	result += fmt.Sprintf("Status: %s | Entries: %d\n\n", t.Status, len(t.Entries))

	for i, entry := range t.Entries {
		ts := entry.Timestamp.Format("15:04:05")
		switch entry.Type {
		case "user":
			result += fmt.Sprintf("[%s] 👤 User: %s\n", ts, truncate(entry.Content, 100))
		case "assistant":
			result += fmt.Sprintf("[%s] 🤖 Assistant: %s\n", ts, truncate(entry.Content, 100))
		case "thinking":
			result += fmt.Sprintf("[%s] 💭 Thinking: %s\n", ts, truncate(entry.Content, 100))
		case "tool_call":
			paramsJSON, _ := json.Marshal(entry.ToolParams)
			result += fmt.Sprintf("[%s] 🔧 Tool: %s(%s)\n", ts, entry.ToolName, truncate(string(paramsJSON), 80))
		case "tool_result":
			icon := "✅"
			if entry.ToolSuccess != nil && !*entry.ToolSuccess {
				icon = "❌"
			}
			result += fmt.Sprintf("[%s] %s Result: %s (%dms)\n", ts, icon, truncate(entry.ToolResult, 80), entry.DurationMs)
		case "system":
			result += fmt.Sprintf("[%s] ⚙️ System: %s\n", ts, truncate(entry.Content, 100))
		}
		if i >= 50 {
			result += fmt.Sprintf("\n... (%d more entries) ...\n", len(t.Entries)-i-1)
			break
		}
	}
	return result
}

func CloneTranscriptParams(params map[string]interface{}) map[string]interface{} {
	if params == nil {
		return nil
	}
	data, err := json.Marshal(params)
	if err == nil {
		var clone map[string]interface{}
		if err := json.Unmarshal(data, &clone); err == nil {
			return clone
		}
	}
	clone := make(map[string]interface{}, len(params))
	for key, value := range params {
		clone[key] = value
	}
	return clone
}

func SafeTranscriptFileID(sessionID string) string {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		sessionID = "session"
	}
	base := safeFilename(sessionID)
	if len(base) > 48 {
		base = base[:48]
	}
	sum := sha256.Sum256([]byte(sessionID))
	return fmt.Sprintf("%s-%x", base, sum[:4])
}

// RecoverSessionTranscriptFromJournal reconstructs the last durable state. A
// partially written final line is ignored, which is the expected failure mode
// when a process is terminated during an append.
func RecoverSessionTranscriptFromJournal(journalPath string) (*SessionTranscript, error) {
	info, err := os.Stat(journalPath)
	if err != nil {
		return nil, fmt.Errorf("inspect session journal: %w", err)
	}
	if info.Size() > maxSessionJournalRecoveryBytes {
		return nil, fmt.Errorf("session journal is too large to recover safely: %d bytes", info.Size())
	}
	data, err := os.ReadFile(journalPath)
	if err != nil {
		return nil, fmt.Errorf("read session journal: %w", err)
	}
	lines := bytes.Split(data, []byte{'\n'})
	transcript := &SessionTranscript{
		Entries:        make([]TranscriptEntry, 0),
		Status:         "running",
		journalPath:    journalPath,
		filePath:       strings.TrimSuffix(journalPath, filepath.Ext(journalPath)) + ".json",
		checkpointPath: strings.TrimSuffix(journalPath, filepath.Ext(journalPath)) + ".checkpoint.json",
	}
	var seenEvent bool
	var lastSequence uint64
	for index, rawLine := range lines {
		line := bytes.TrimSpace(rawLine)
		if len(line) == 0 {
			continue
		}
		var event SessionJournalEvent
		if err := json.Unmarshal(line, &event); err != nil {
			if !hasNonEmptyJournalLine(lines[index+1:]) {
				break
			}
			return nil, fmt.Errorf("decode session journal line %d: %w", index+1, err)
		}
		if event.Version != sessionJournalVersion {
			return nil, fmt.Errorf("unsupported session journal version %d", event.Version)
		}
		if event.Sequence <= lastSequence {
			return nil, fmt.Errorf("non-monotonic session journal sequence %d after %d", event.Sequence, lastSequence)
		}
		lastSequence = event.Sequence
		if !seenEvent {
			transcript.SessionID = event.SessionID
			transcript.AgentName = event.AgentName
			transcript.StartTime = event.Timestamp
			seenEvent = true
		} else if transcript.SessionID != event.SessionID || transcript.AgentName != event.AgentName {
			return nil, fmt.Errorf("session journal identity changed at sequence %d", event.Sequence)
		}

		switch event.Event {
		case "session_start":
			transcript.StartTime = event.Timestamp
			if event.Status != "" {
				transcript.Status = event.Status
			}
		case "session_end":
			transcript.EndTime = event.Timestamp
			transcript.Status = event.Status
			transcript.FinalOutput = event.FinalOutput
			transcript.InternalRecap = event.InternalRecap
		case "turn_checkpoint":
			// The full, atomically replaced snapshot lives in the sidecar. The
			// journal event only makes checkpoint progress auditable.
		default:
			if event.Entry != nil {
				transcript.Entries = append(transcript.Entries, *event.Entry)
			}
		}
	}
	if !seenEvent {
		return nil, fmt.Errorf("session journal contains no complete events")
	}
	transcript.journalSeq = lastSequence
	if checkpoint, checkpointErr := LoadTurnCheckpoint(transcript.checkpointPath); checkpointErr == nil {
		if checkpoint.TaskID == transcript.SessionID {
			transcript.Checkpoint = checkpoint
		}
	} else if !os.IsNotExist(checkpointErr) {
		log.Printf("[TRANSCRIPT] turn checkpoint warning for %q: %v", transcript.SessionID, checkpointErr)
	}
	return transcript, nil
}

func hasNonEmptyJournalLine(lines [][]byte) bool {
	for _, line := range lines {
		if len(bytes.TrimSpace(line)) > 0 {
			return true
		}
	}
	return false
}

func transcriptJournalFsyncEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("OPENKITTY_TRANSCRIPT_FSYNC"))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func safeFilename(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "session"
	}
	name = regexp.MustCompile(`[^a-zA-Z0-9._-]+`).ReplaceAllString(name, "-")
	name = strings.Trim(name, ".-")
	if name == "" {
		return "session"
	}
	return name
}
