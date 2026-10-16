package pipeline

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"mac/internal/fsutil"
	"mac/internal/projectctx"
)

const CheckpointVersion = 1

const (
	CheckpointPhaseStepPending   = "step_pending"
	CheckpointPhaseStepRunning   = "step_running"
	CheckpointPhaseStepFinalized = "step_finalized"
	// Post-processing means the model turn finished but the host has not yet
	// committed the validated StepResult. A restart resumes the same model turn
	// from its terminal transcript instead of issuing another model request.
	CheckpointPhasePostProcessing  = "post_processing"
	CheckpointPhaseWaitingApproval = "waiting_approval"
	CheckpointPhaseCompleted       = "completed"
)

const pipelineCheckpointMaxTurns = 128

// PipelineTurnCheckpoint binds one deterministic executeStep invocation to
// the LLMAgent transcript/checkpoint files that own its model/tool cursor.
// Key includes the rendered prompt and retry ordinal, so repair turns and
// bounded retries cannot accidentally reuse one another's terminal output.
type PipelineTurnCheckpoint struct {
	Key            string    `json:"key"`
	TaskID         string    `json:"task_id"`
	ReceiptTaskID  string    `json:"receipt_task_id,omitempty"`
	StepIndex      int       `json:"step_index"`
	StepRevision   int       `json:"step_revision"`
	Attempt        int       `json:"attempt"`
	Phase          string    `json:"phase"` // running, success, failed
	CheckpointPath string    `json:"checkpoint_path,omitempty"`
	TranscriptPath string    `json:"transcript_path,omitempty"`
	Output         string    `json:"output,omitempty"`
	Error          string    `json:"error,omitempty"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// PipelineCheckpoint is the durable control-plane cursor for one execution.
// The nested LLMAgent owns model/tool continuation in TurnCheckpoint; this
// record binds that turn to a stable pipeline/step/attempt identity and keeps
// the outer workflow cursor durable across process restarts.
type PipelineCheckpoint struct {
	Version        int                                `json:"version"`
	Revision       uint64                             `json:"revision"`
	PipelineExecID string                             `json:"pipeline_exec_id"`
	ParentTaskID   string                             `json:"parent_task_id,omitempty"`
	StepIndex      int                                `json:"step_index"`
	StepName       string                             `json:"step_name,omitempty"`
	StepRevision   int                                `json:"step_revision,omitempty"`
	Attempt        int                                `json:"attempt,omitempty"`
	Phase          string                             `json:"phase"`
	TurnTaskID     string                             `json:"turn_task_id,omitempty"`
	ReceiptTaskID  string                             `json:"receipt_task_id,omitempty"`
	TurnCheckpoint string                             `json:"turn_checkpoint,omitempty"`
	TurnTranscript string                             `json:"turn_transcript,omitempty"`
	TurnOutput     string                             `json:"turn_output,omitempty"`
	TurnStatus     string                             `json:"turn_status,omitempty"`
	ActiveTurnKey  string                             `json:"active_turn_key,omitempty"`
	Turns          map[string]*PipelineTurnCheckpoint `json:"turns,omitempty"`
	RepeatCounts   map[string]int                     `json:"repeat_counts,omitempty"`
	RoutedHere     bool                               `json:"routed_here,omitempty"`
	RouteEntry     int                                `json:"route_entry,omitempty"`
	UpdatedAt      time.Time                          `json:"updated_at"`
}

func StepExecutionID(execID string, stepIndex, revision, attempt int, discriminator ...string) string {
	if revision <= 0 {
		revision = 1
	}
	if attempt <= 0 {
		attempt = 1
	}
	payload := fmt.Sprintf("%s\x00%d\x00%d\x00%d", strings.TrimSpace(execID), stepIndex, revision, attempt)
	if len(discriminator) > 0 {
		payload += "\x00" + strings.TrimSpace(discriminator[0])
	}
	sum := sha256.Sum256([]byte(payload))
	return fmt.Sprintf("pipeline-turn-%x", sum[:16])
}

type StepFingerprint struct {
	Name           string   `json:"name"`
	Model          string   `json:"model"`
	MaxIter        int      `json:"max_iter"`
	NoTools        bool     `json:"no_tools"`
	Tools          []string `json:"tools"`
	RequiredTools  []string `json:"required_tools"`
	RequiredSkills []string `json:"required_skills"`
	Prompt         string   `json:"prompt"`
}

func TurnCheckpointKey(execID string, stepIndex, revision, attempt int, fingerprint StepFingerprint) string {
	data, _ := json.Marshal(fingerprint)
	sum := sha256.Sum256(append([]byte(fmt.Sprintf("%s\x00%d\x00%d\x00%d\x00", strings.TrimSpace(execID), stepIndex, revision, attempt)), data...))
	return fmt.Sprintf("%x", sum[:])
}

func (checkpoint *PipelineCheckpoint) Normalize(execID string) error {
	if checkpoint == nil {
		return fmt.Errorf("pipeline checkpoint is nil")
	}
	checkpoint.Version = CheckpointVersion
	if checkpoint.Revision == 0 {
		checkpoint.Revision = 1
	}
	checkpoint.PipelineExecID = strings.TrimSpace(checkpoint.PipelineExecID)
	if checkpoint.PipelineExecID == "" {
		checkpoint.PipelineExecID = strings.TrimSpace(execID)
	}
	if checkpoint.PipelineExecID == "" {
		return fmt.Errorf("pipeline checkpoint execution id is empty")
	}
	checkpoint.ParentTaskID = strings.TrimSpace(checkpoint.ParentTaskID)
	checkpoint.StepName = strings.TrimSpace(checkpoint.StepName)
	checkpoint.TurnTaskID = strings.TrimSpace(checkpoint.TurnTaskID)
	checkpoint.ReceiptTaskID = strings.TrimSpace(checkpoint.ReceiptTaskID)
	checkpoint.TurnCheckpoint = strings.TrimSpace(checkpoint.TurnCheckpoint)
	checkpoint.TurnTranscript = strings.TrimSpace(checkpoint.TurnTranscript)
	checkpoint.TurnStatus = strings.TrimSpace(checkpoint.TurnStatus)
	checkpoint.ActiveTurnKey = strings.TrimSpace(checkpoint.ActiveTurnKey)
	if checkpoint.StepRevision <= 0 {
		checkpoint.StepRevision = 1
	}
	if checkpoint.Attempt <= 0 {
		checkpoint.Attempt = 1
	}
	if checkpoint.RouteEntry == 0 && !checkpoint.RoutedHere {
		checkpoint.RouteEntry = -1
	}
	switch checkpoint.Phase {
	case CheckpointPhaseStepPending,
		CheckpointPhaseStepRunning,
		CheckpointPhasePostProcessing,
		CheckpointPhaseStepFinalized,
		CheckpointPhaseWaitingApproval,
		CheckpointPhaseCompleted:
	default:
		return fmt.Errorf("unsupported pipeline checkpoint phase %q", checkpoint.Phase)
	}
	if checkpoint.UpdatedAt.IsZero() {
		checkpoint.UpdatedAt = time.Now()
	}
	if checkpoint.Turns == nil {
		checkpoint.Turns = make(map[string]*PipelineTurnCheckpoint)
	}
	for rawKey, turn := range checkpoint.Turns {
		key := strings.TrimSpace(rawKey)
		if key == "" || turn == nil {
			delete(checkpoint.Turns, rawKey)
			continue
		}
		if key != rawKey {
			delete(checkpoint.Turns, rawKey)
			checkpoint.Turns[key] = turn
		}
		turn.Key = key
		turn.TaskID = strings.TrimSpace(turn.TaskID)
		turn.ReceiptTaskID = strings.TrimSpace(turn.ReceiptTaskID)
		turn.CheckpointPath = strings.TrimSpace(turn.CheckpointPath)
		turn.TranscriptPath = strings.TrimSpace(turn.TranscriptPath)
		turn.Phase = strings.TrimSpace(turn.Phase)
		if turn.StepRevision <= 0 {
			turn.StepRevision = 1
		}
		if turn.Attempt <= 0 {
			turn.Attempt = 1
		}
		if turn.UpdatedAt.IsZero() {
			turn.UpdatedAt = checkpoint.UpdatedAt
		}
	}
	PruneTurnCheckpoints(checkpoint)
	return nil
}

func CloneCheckpoint(checkpoint *PipelineCheckpoint) *PipelineCheckpoint {
	if checkpoint == nil {
		return nil
	}
	clone := *checkpoint
	if checkpoint.RepeatCounts != nil {
		clone.RepeatCounts = make(map[string]int, len(checkpoint.RepeatCounts))
		for key, value := range checkpoint.RepeatCounts {
			clone.RepeatCounts[key] = value
		}
	}
	if checkpoint.Turns != nil {
		clone.Turns = make(map[string]*PipelineTurnCheckpoint, len(checkpoint.Turns))
		for key, turn := range checkpoint.Turns {
			if turn == nil {
				continue
			}
			turnClone := *turn
			clone.Turns[key] = &turnClone
		}
	}
	return &clone
}

func PruneTurnCheckpoints(checkpoint *PipelineCheckpoint) {
	if checkpoint == nil || len(checkpoint.Turns) <= pipelineCheckpointMaxTurns {
		return
	}
	type candidate struct {
		key string
		at  time.Time
	}
	completed := make([]candidate, 0, len(checkpoint.Turns))
	for key, turn := range checkpoint.Turns {
		if key == checkpoint.ActiveTurnKey || turn == nil || turn.Phase == "running" {
			continue
		}
		completed = append(completed, candidate{key: key, at: turn.UpdatedAt})
	}
	sort.Slice(completed, func(i, j int) bool { return completed[i].at.Before(completed[j].at) })
	remove := len(checkpoint.Turns) - pipelineCheckpointMaxTurns
	for _, item := range completed {
		if remove <= 0 {
			break
		}
		delete(checkpoint.Turns, item.key)
		remove--
	}
}

func RepeatCountsToStrings(source map[int]int) map[string]int {
	if len(source) == 0 {
		return nil
	}
	out := make(map[string]int, len(source))
	for key, value := range source {
		if value > 0 {
			out[strconv.Itoa(key)] = value
		}
	}
	return out
}

func RepeatCountsFromStrings(source map[string]int) map[int]int {
	out := make(map[int]int, len(source))
	for key, value := range source {
		index, err := strconv.Atoi(key)
		if err == nil && index >= 0 && value > 0 {
			out[index] = value
		}
	}
	return out
}

func CheckpointDirectory(workspace string) string {
	workspace = projectctx.NormalizeWorkspace(workspace)
	return filepath.Join(workspace, ".openkitty", "pipeline_checkpoints")
}

func CheckpointPath(workspace, execID string) string {
	return filepath.Join(CheckpointDirectory(workspace), safeCheckpointID(execID)+".json")
}

func WriteCheckpoint(path string, checkpoint *PipelineCheckpoint) error {
	checkpoint = CloneCheckpoint(checkpoint)
	if checkpoint == nil {
		return fmt.Errorf("pipeline checkpoint is nil")
	}
	if err := checkpoint.Normalize(""); err != nil {
		return err
	}
	data, err := json.MarshalIndent(checkpoint, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal pipeline checkpoint: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("create pipeline checkpoint directory: %w", err)
	}
	if err := fsutil.WriteAtomic(path, data, 0600); err != nil {
		return fmt.Errorf("write pipeline checkpoint: %w", err)
	}
	return nil
}

func LatestTurnArtifact(workspace, turnTaskID, suffix string) string {
	workspace = projectctx.NormalizeWorkspace(workspace)
	dir := filepath.Join(workspace, ".openkitty", "logs", "transcripts")
	pattern := filepath.Join(dir, "transcript_"+safeCheckpointID(turnTaskID)+"_*"+suffix)
	paths, err := filepath.Glob(pattern)
	if err != nil || len(paths) == 0 {
		return ""
	}
	sort.Slice(paths, func(i, j int) bool {
		left, leftErr := os.Stat(paths[i])
		right, rightErr := os.Stat(paths[j])
		if leftErr != nil {
			return false
		}
		if rightErr != nil {
			return true
		}
		return left.ModTime().After(right.ModTime())
	})
	for _, path := range paths {
		if suffix == ".json" && strings.HasSuffix(path, ".checkpoint.json") {
			continue
		}
		return path
	}
	return ""
}

func LoadLatestCompletedTurn(workspace, turnTaskID string) (string, string, string, bool) {
	path := LatestTurnArtifact(workspace, turnTaskID, ".json")
	if path == "" {
		return "", "", "", false
	}
	output, status, ok := LoadCompletedTurnTranscript(path, turnTaskID)
	return output, status, path, ok
}

func LoadCheckpoint(path string) (*PipelineCheckpoint, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var checkpoint PipelineCheckpoint
	if err := json.Unmarshal(data, &checkpoint); err != nil {
		return nil, fmt.Errorf("decode pipeline checkpoint: %w", err)
	}
	if checkpoint.Version != CheckpointVersion {
		return nil, fmt.Errorf("unsupported pipeline checkpoint version %d", checkpoint.Version)
	}
	if err := checkpoint.Normalize(""); err != nil {
		return nil, err
	}
	return &checkpoint, nil
}

func LoadCompletedTurnTranscript(path, turnTaskID string) (string, string, bool) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", "", false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", "", false
	}
	var transcript struct {
		SessionID   string `json:"session_id"`
		Status      string `json:"status"`
		FinalOutput string `json:"final_output"`
	}
	if err := json.Unmarshal(data, &transcript); err != nil {
		return "", "", false
	}
	if expected := strings.TrimSpace(turnTaskID); expected != "" && strings.TrimSpace(transcript.SessionID) != expected {
		return "", "", false
	}
	status := strings.TrimSpace(transcript.Status)
	if status != "success" && status != "failed" {
		return "", "", false
	}
	return strings.TrimSpace(transcript.FinalOutput), status, true
}

var checkpointFilenamePattern = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

func safeCheckpointID(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		value = "session"
	}
	base := checkpointFilenamePattern.ReplaceAllString(value, "-")
	base = strings.Trim(base, ".-")
	if base == "" {
		base = "skill"
	}
	if len(base) > 48 {
		base = base[:48]
	}
	sum := sha256.Sum256([]byte(value))
	return fmt.Sprintf("%s-%x", base, sum[:4])
}
