package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	sessioncore "mac/internal/session"
)

var (
	errGatewayTaskNotFound      = errors.New("task not found")
	errGatewayTaskNotPausable   = errors.New("task cannot be paused")
	errGatewayTaskPauseNotLocal = errors.New("AgentOS tasks must use the AgentOS pause endpoint")
)

// pauseGatewayTask requests a cooperative pause. Running model work is
// cancelled, but the task is not made terminal: processTaskWithSession owns
// the final pausing -> paused transition after the current Turn checkpoint has
// had a chance to reach durable storage.
func (g *GatewayService) pauseGatewayTask(taskID string) (*GatewayTask, error) {
	if g == nil {
		return nil, errGatewayTaskNotFound
	}
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return nil, fmt.Errorf("task_id is required")
	}

	var cancel context.CancelFunc
	g.tasksMu.Lock()
	task := g.tasks[taskID]
	if task == nil {
		g.tasksMu.Unlock()
		return nil, errGatewayTaskNotFound
	}
	if task.QueueName == agentOSQueueName {
		g.tasksMu.Unlock()
		return nil, errGatewayTaskPauseNotLocal
	}
	taskType := "chat"
	if task.Request != nil && strings.TrimSpace(task.Request.TaskType) != "" {
		taskType = strings.ToLower(strings.TrimSpace(task.Request.TaskType))
	}
	if taskType != "chat" && taskType != "skill" {
		g.tasksMu.Unlock()
		return nil, fmt.Errorf("%w for task type %s", errGatewayTaskNotPausable, taskType)
	}
	previousStatus := task.Status
	previousPhase := task.CurrentPhase
	previousRecoveryState := task.RecoveryState
	previousHeartbeat := task.HeartbeatAt
	switch task.Status {
	case "paused", "pausing":
		snapshot := cloneGatewayTask(task)
		g.tasksMu.Unlock()
		return snapshot, nil
	case "pending":
		task.Status = "paused"
		task.CurrentPhase = "paused"
		task.RecoveryState = sessioncore.RecoveryStateAvailable
		now := time.Now().Round(0)
		task.HeartbeatAt = &now
	case "running":
		task.Status = "pausing"
		task.CurrentPhase = "pausing"
		now := time.Now().Round(0)
		task.HeartbeatAt = &now
		cancel = task.Cancel
	default:
		status := task.Status
		g.tasksMu.Unlock()
		return nil, fmt.Errorf("%w from status %s", errGatewayTaskNotPausable, status)
	}
	snapshot := cloneGatewayTask(task)
	g.tasksMu.Unlock()

	if g.db != nil {
		if err := SaveGatewayTaskToDB(g.db, snapshot); err != nil {
			// The pause is only accepted after its lifecycle state is durable.
			// Restore the in-memory state when persistence fails so the worker is
			// not left stranded in a pause state that cannot survive a restart.
			g.tasksMu.Lock()
			if current := g.tasks[taskID]; current == task && current.Status == snapshot.Status {
				current.Status = previousStatus
				current.CurrentPhase = previousPhase
				current.RecoveryState = previousRecoveryState
				current.HeartbeatAt = previousHeartbeat
			}
			g.tasksMu.Unlock()
			return nil, fmt.Errorf("persist paused task: %w", err)
		}
		messageStatus := snapshot.Status
		if err := scopeRowsToPersistedOwner(
			g.db.Model(&ChatMessage{}).
				Where("task_id = ? AND status IN ?", taskID, []string{"pending", "running", "pausing"}),
			snapshot.TenantID,
			snapshot.SubjectID,
		).Update("status", messageStatus).Error; err != nil && g.logger != nil {
			g.logger.Printf("persist paused task message %s: %v", taskID, err)
		}
	}
	g.broadcastTaskUpdate(taskID, snapshot.Status, nil, nil)
	if cancel != nil {
		cancel()
	}
	return snapshot, nil
}

// rememberGatewayTaskJournal records the transcript path published by the
// agent runtime. The journal itself remains the source of truth; the task row
// only stores a validated pointer used by explicit user-controlled resume.
func (g *GatewayService) rememberGatewayTaskJournal(task *GatewayTask, transcriptPath string) {
	if g == nil || task == nil {
		return
	}
	transcriptPath = strings.TrimSpace(transcriptPath)
	if transcriptPath == "" {
		return
	}
	g.tasksMu.Lock()
	if current := g.tasks[task.ID]; current == task {
		current.RecoveryJournal = transcriptPath
	}
	g.tasksMu.Unlock()
}

// finalizeGatewayTaskPause publishes a stable paused snapshot after the model
// call has returned. It deliberately does not set CompletedAt: paused is a
// resumable lifecycle state, not a terminal result.
func (g *GatewayService) finalizeGatewayTaskPause(task *GatewayTask) (*GatewayTask, bool) {
	if g == nil || task == nil {
		return nil, false
	}

	g.tasksMu.Lock()
	current := g.tasks[task.ID]
	if current != task || (task.Status != "pausing" && task.Status != "paused") {
		snapshot := cloneGatewayTask(current)
		g.tasksMu.Unlock()
		return snapshot, false
	}
	journalPath := strings.TrimSpace(task.RecoveryJournal)
	g.tasksMu.Unlock()

	var preview string
	var entryCount int
	var lastActivity *time.Time
	if journalPath != "" {
		if transcript, err := sessioncore.RecoverSessionTranscriptFromJournal(journalPath); err == nil {
			preview = sessioncore.RecoveryPreview(transcript)
			entryCount = len(transcript.Entries)
			activity := sessioncore.LastActivity(transcript)
			if !activity.IsZero() {
				lastActivity = &activity
			}
		} else if g.logger != nil {
			g.logger.Printf("pause journal for task %s is not readable yet: %v", task.ID, err)
		}
	}

	now := time.Now().Round(0)
	g.tasksMu.Lock()
	current = g.tasks[task.ID]
	if current != task || (task.Status != "pausing" && task.Status != "paused") {
		snapshot := cloneGatewayTask(current)
		g.tasksMu.Unlock()
		return snapshot, false
	}
	task.Status = "paused"
	task.CurrentPhase = "paused"
	task.Cancel = nil
	task.Error = nil
	task.CompletedAt = nil
	task.HeartbeatAt = &now
	task.RecoveryState = sessioncore.RecoveryStateAvailable
	task.RecoveryPreview = preview
	task.RecoveryEntryCount = entryCount
	task.RecoveryLastActivity = lastActivity
	snapshot := cloneGatewayTask(task)
	g.tasksMu.Unlock()

	if g.db != nil {
		if err := SaveGatewayTaskToDB(g.db, snapshot); err != nil {
			if g.logger != nil {
				g.logger.Printf("persist paused task %s: %v", task.ID, err)
			}
			return snapshot, true
		}
		if err := scopeRowsToPersistedOwner(
			g.db.Model(&ChatMessage{}).
				Where("task_id = ? AND status IN ?", task.ID, []string{"pending", "running", "pausing"}),
			snapshot.TenantID,
			snapshot.SubjectID,
		).Update("status", "paused").Error; err != nil && g.logger != nil {
			g.logger.Printf("persist paused task message %s: %v", task.ID, err)
		}
	}
	g.broadcastTaskUpdate(task.ID, "paused", map[string]interface{}{
		"recovery_state": snapshot.RecoveryState,
		"can_resume":     true,
	}, nil)
	return snapshot, true
}

func (g *GatewayService) handlePauseTaskByID(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	taskID := strings.TrimSpace(strings.TrimPrefix(r.URL.Path, "/api/task/pause/"))
	if taskID == "" {
		sendJSONError(w, "Task ID required", http.StatusBadRequest)
		return
	}
	task, err := g.pauseGatewayTask(taskID)
	if err != nil {
		status := http.StatusConflict
		if errors.Is(err, errGatewayTaskNotFound) {
			status = http.StatusNotFound
		}
		sendJSONError(w, err.Error(), status)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"task_id": task.ID,
		"status":  task.Status,
	})
}
