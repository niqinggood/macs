package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	sessioncore "mac/internal/session"

	"gorm.io/gorm"
)

var errInterruptedTaskNotResumable = errors.New("interrupted task is not available for resume")

type recoveredJournalCandidate struct {
	path       string
	transcript *sessioncore.SessionTranscript
	activity   time.Time
}

func preferRecoveredJournalCandidate(current, candidate recoveredJournalCandidate) bool {
	if current.transcript == nil {
		return true
	}
	if candidate.transcript == nil {
		return false
	}
	candidateTerminal := sessioncore.Succeeded(candidate.transcript) && strings.TrimSpace(candidate.transcript.FinalOutput) != ""
	currentTerminal := sessioncore.Succeeded(current.transcript) && strings.TrimSpace(current.transcript.FinalOutput) != ""
	if candidateTerminal != currentTerminal {
		return candidateTerminal
	}
	if candidateTerminal {
		return candidate.activity.After(current.activity)
	}
	// A bootstrap checkpoint is more useful than a slightly newer journal that
	// only reached session_start/user logging before the process died.
	if current.transcript.Checkpoint != nil && candidate.transcript.Checkpoint == nil {
		return false
	}
	if current.transcript.Checkpoint == nil && candidate.transcript.Checkpoint != nil {
		return true
	}
	return candidate.activity.After(current.activity)
}

// indexInterruptedSessionJournals links durable agent journals to Gateway
// tasks that were marked interrupted at process startup. It never re-executes
// tools. A terminal successful journal is reconciled directly; every other
// usable journal becomes an explicit user-controlled resume point.
func indexInterruptedSessionJournals(db *gorm.DB, workspace string, logger *log.Logger) (int, error) {
	if db == nil {
		return 0, fmt.Errorf("database connection is nil")
	}
	transcriptDir := sessioncore.TranscriptDirectory(workspace)
	if _, err := os.Stat(transcriptDir); err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("inspect transcript directory: %w", err)
	}

	var records []GatewayTaskRecord
	if err := db.Where(
		"status = ? AND error_msg = ? AND (recovery_state IS NULL OR recovery_state NOT IN ?)",
		"failed", orphanGatewayTaskError,
		[]string{sessioncore.RecoveryStateResuming, sessioncore.RecoveryStateResumed, sessioncore.RecoveryStateRecoveredComplete},
	).Find(&records).Error; err != nil {
		return 0, fmt.Errorf("load interrupted gateway tasks: %w", err)
	}
	targetTaskIDs := make(map[string]struct{}, len(records))
	for index := range records {
		targetTaskIDs[records[index].TaskID] = struct{}{}
	}

	// Cover the narrow crash window after the user message and journal were
	// persisted but before the first GatewayTaskRecord write.
	var pendingMessages []ChatMessage
	if err := db.Where("role = ? AND task_id <> '' AND status IN ?", "user", []string{"pending", "running"}).
		Order("id asc").Find(&pendingMessages).Error; err != nil {
		return 0, fmt.Errorf("load pending chat messages for recovery: %w", err)
	}
	pendingByTask := make(map[string]ChatMessage)
	for _, message := range pendingMessages {
		if _, exists := pendingByTask[message.TaskID]; exists {
			continue
		}
		var taskCount int64
		if err := db.Model(&GatewayTaskRecord{}).Where("task_id = ?", message.TaskID).Count(&taskCount).Error; err != nil || taskCount > 0 {
			continue
		}
		pendingByTask[message.TaskID] = message
		targetTaskIDs[message.TaskID] = struct{}{}
	}

	// Journal filenames contain a stable hash of task_id. Inspect only likely
	// interrupted tasks instead of re-reading every historical log at startup.
	candidates := make(map[string]recoveredJournalCandidate, len(targetTaskIDs))
	for taskID := range targetTaskIDs {
		pattern := filepath.Join(transcriptDir, "transcript_"+sessioncore.SafeTranscriptFileID(taskID)+"_*.jsonl")
		matches, globErr := filepath.Glob(pattern)
		if globErr != nil {
			return 0, fmt.Errorf("find recovery journals for %s: %w", taskID, globErr)
		}
		for _, journalPath := range matches {
			transcript, recoverErr := sessioncore.RecoverSessionTranscriptFromJournal(journalPath)
			if recoverErr != nil {
				if logger != nil {
					logger.Printf("[Recovery] skip unreadable journal %s: %v", filepath.Base(journalPath), recoverErr)
				}
				continue
			}
			if transcript.SessionID != taskID {
				continue
			}
			activity := sessioncore.LastActivity(transcript)
			candidate := recoveredJournalCandidate{path: journalPath, transcript: transcript, activity: activity}
			if current, ok := candidates[taskID]; ok && !preferRecoveredJournalCandidate(current, candidate) {
				continue
			}
			candidates[taskID] = candidate
		}
	}

	// A resumed task persists the source journal path before its goroutine starts.
	// If the process dies in that narrow window, there is no new task-named
	// journal yet; reuse the source checkpoint instead of losing resumability.
	for index := range records {
		record := &records[index]
		if _, exists := candidates[record.TaskID]; exists {
			continue
		}
		journalPath := strings.TrimSpace(record.RecoveryJournal)
		if journalPath == "" || !pathWithin(transcriptDir, journalPath) {
			continue
		}
		transcript, recoverErr := sessioncore.RecoverSessionTranscriptFromJournal(journalPath)
		if recoverErr != nil {
			if logger != nil {
				logger.Printf("[Recovery] skip inherited journal %s: %v", filepath.Base(journalPath), recoverErr)
			}
			continue
		}
		if transcript.SessionID != record.TaskID && transcript.SessionID != strings.TrimSpace(record.ResumedFromTaskID) {
			continue
		}
		candidates[record.TaskID] = recoveredJournalCandidate{
			path: journalPath, transcript: transcript, activity: sessioncore.LastActivity(transcript),
		}
	}

	for taskID, userMessage := range pendingByTask {
		candidate, exists := candidates[taskID]
		if !exists {
			continue
		}
		record := GatewayTaskRecord{
			TaskID:        taskID,
			SessionID:     userMessage.SessionID,
			TenantID:      normalizePersonalOwnerTenantID(userMessage.TenantID),
			SubjectID:     strings.TrimSpace(userMessage.SubjectID),
			TenantScoped:  strings.TrimSpace(userMessage.SubjectID) != "",
			UserID:        userMessage.UserId,
			TaskType:      "chat",
			Status:        "failed",
			Message:       userMessage.Content,
			AgentName:     candidate.transcript.AgentName,
			Channel:       userMessage.Channel,
			ErrorMsg:      orphanGatewayTaskError,
			RecoveryState: sessioncore.RecoveryStatePendingScan,
			CreatedAt:     userMessage.CreatedAt,
			UpdatedAt:     time.Now(),
		}
		if err := db.Create(&record).Error; err != nil {
			if logger != nil {
				logger.Printf("[Recovery] cannot reconstruct task record %s: %v", taskID, err)
			}
			continue
		}
		scopeRowsToPersistedOwner(
			db.Model(&ChatMessage{}).
				Where("task_id = ? AND status IN ?", taskID, []string{"pending", "running"}),
			record.TenantID,
			record.SubjectID,
		).
			Update("status", "failed")
		records = append(records, record)
	}

	indexed := 0
	for index := range records {
		record := &records[index]
		candidate, ok := candidates[record.TaskID]
		if !ok {
			if record.RecoveryState == "" || record.RecoveryState == sessioncore.RecoveryStatePendingScan {
				db.Model(&GatewayTaskRecord{}).Where("task_id = ?", record.TaskID).
					Updates(map[string]interface{}{"recovery_state": sessioncore.RecoveryStateUnavailable, "updated_at": time.Now()})
			}
			continue
		}

		transcript := candidate.transcript
		preview := sessioncore.RecoveryPreview(transcript)
		activity := candidate.activity
		if sessioncore.Succeeded(transcript) && strings.TrimSpace(transcript.FinalOutput) != "" {
			if err := reconcileRecoveredCompletedTask(db, record, transcript, candidate.path, preview, activity); err != nil {
				return indexed, err
			}
			indexed++
			continue
		}

		updates := map[string]interface{}{
			"recovery_state":         sessioncore.RecoveryStateAvailable,
			"recovery_journal":       candidate.path,
			"recovery_preview":       preview,
			"recovery_entry_count":   len(transcript.Entries),
			"recovery_last_activity": activity,
			"updated_at":             time.Now(),
		}
		if err := db.Model(&GatewayTaskRecord{}).Where("task_id = ?", record.TaskID).Updates(updates).Error; err != nil {
			return indexed, fmt.Errorf("index recovery journal for %s: %w", record.TaskID, err)
		}
		indexed++
	}
	return indexed, nil
}

func reconcileRecoveredCompletedTask(db *gorm.DB, record *GatewayTaskRecord, transcript *sessioncore.SessionTranscript, journalPath, preview string, activity time.Time) error {
	output := strings.TrimSpace(transcript.FinalOutput)
	encoded, err := json.Marshal(output)
	if err != nil {
		return fmt.Errorf("encode recovered result for %s: %w", record.TaskID, err)
	}
	completedAt := transcript.EndTime
	if completedAt.IsZero() {
		completedAt = activity
	}

	return db.Transaction(func(tx *gorm.DB) error {
		updates := map[string]interface{}{
			"status":                 "completed",
			"result":                 string(encoded),
			"error_msg":              "",
			"completed_at":           completedAt,
			"recovery_state":         sessioncore.RecoveryStateRecoveredComplete,
			"recovery_journal":       journalPath,
			"recovery_preview":       preview,
			"recovery_entry_count":   len(transcript.Entries),
			"recovery_last_activity": activity,
			"updated_at":             time.Now(),
		}
		if err := tx.Model(&GatewayTaskRecord{}).Where("task_id = ?", record.TaskID).Updates(updates).Error; err != nil {
			return fmt.Errorf("reconcile completed task %s: %w", record.TaskID, err)
		}
		if err := scopeRowsToPersistedOwner(
			tx.Model(&ChatMessage{}).
				Where("task_id = ? AND status IN ?", record.TaskID, []string{"pending", "running", "failed"}),
			record.TenantID,
			record.SubjectID,
		).
			Update("status", "completed").Error; err != nil {
			return fmt.Errorf("reconcile completed messages %s: %w", record.TaskID, err)
		}

		var assistantCount int64
		if err := scopeRowsToPersistedOwner(
			tx.Model(&ChatMessage{}).Where("task_id = ? AND role = ?", record.TaskID, "assistant"),
			record.TenantID,
			record.SubjectID,
		).Count(&assistantCount).Error; err != nil {
			return err
		}
		if assistantCount == 0 {
			message := ChatMessage{
				SessionID: record.SessionID,
				TenantID:  normalizePersonalOwnerTenantID(record.TenantID),
				SubjectID: strings.TrimSpace(record.SubjectID),
				Role:      "assistant",
				AgentName: firstNonEmpty(transcript.AgentName, record.AgentName),
				UserId:    record.UserID,
				Content:   output,
				Status:    "completed",
				TaskID:    record.TaskID,
				Channel:   record.Channel,
				CreatedAt: completedAt,
			}
			if err := tx.Create(&message).Error; err != nil {
				return fmt.Errorf("restore completed assistant message %s: %w", record.TaskID, err)
			}
		}
		return refreshRecoveredChatSession(tx, record)
	})
}

func refreshRecoveredChatSession(tx *gorm.DB, record *GatewayTaskRecord) error {
	if strings.TrimSpace(record.SessionID) == "" {
		return nil
	}
	var messageCount int64
	if err := scopeRowsToPersistedOwner(
		tx.Model(&ChatMessage{}).Where("session_id = ?", record.SessionID),
		record.TenantID,
		record.SubjectID,
	).Count(&messageCount).Error; err != nil {
		return err
	}
	updates := map[string]interface{}{"message_count": messageCount, "updated_at": time.Now()}
	result := scopeRowsToPersistedOwner(
		tx.Model(&ChatSession{}).Where("id = ?", record.SessionID),
		record.TenantID,
		record.SubjectID,
	).Updates(updates)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return tx.Create(&ChatSession{
			ID:           record.SessionID,
			Name:         record.SessionID,
			AgentName:    record.AgentName,
			TenantID:     normalizePersonalOwnerTenantID(record.TenantID),
			SubjectID:    strings.TrimSpace(record.SubjectID),
			UserId:       record.UserID,
			MessageCount: int(messageCount),
			CreatedAt:    record.CreatedAt,
			UpdatedAt:    time.Now(),
		}).Error
	}
	return nil
}

func recoveryInfoFromRecord(record GatewayTaskRecord) sessioncore.InterruptedTaskRecovery {
	return sessioncore.InterruptedTaskRecovery{
		TaskID:                 record.TaskID,
		SessionID:              record.SessionID,
		AgentName:              record.AgentName,
		State:                  record.RecoveryState,
		CanResume:              record.RecoveryState == sessioncore.RecoveryStateAvailable,
		OriginalRequestPreview: sessioncore.TruncateRecoveryText(record.Message, 300),
		ProgressPreview:        record.RecoveryPreview,
		EntryCount:             record.RecoveryEntryCount,
		LastActivity:           record.RecoveryLastActivity,
		ResumedByTaskID:        record.ResumedByTaskID,
	}
}

func recoveryInfoFromTask(task *GatewayTask) *sessioncore.InterruptedTaskRecovery {
	if task == nil || strings.TrimSpace(task.RecoveryState) == "" {
		return nil
	}
	request := task.Request
	record := GatewayTaskRecord{
		TaskID:               task.ID,
		RecoveryState:        task.RecoveryState,
		RecoveryPreview:      task.RecoveryPreview,
		RecoveryEntryCount:   task.RecoveryEntryCount,
		RecoveryLastActivity: task.RecoveryLastActivity,
		ResumedByTaskID:      task.ResumedByTaskID,
	}
	if request != nil {
		record.SessionID = request.SessionId
		record.AgentName = request.AgentName
		record.Message = request.Message
	}
	info := recoveryInfoFromRecord(record)
	return &info
}

func (g *GatewayService) listInterruptedTaskRecoveries(sessionID string, limit int) ([]sessioncore.InterruptedTaskRecovery, error) {
	if g == nil || g.db == nil {
		return nil, fmt.Errorf("database connection is nil")
	}
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	query := g.db.
		Where("recovery_state = ?", sessioncore.RecoveryStateAvailable).
		Where("queue_name IS NULL OR queue_name <> ?", agentOSQueueName)
	query = scopeRowsToPersistedOwner(query, defaultExecutionTenantID, "")
	if strings.TrimSpace(sessionID) != "" {
		query = query.Where("session_id = ?", sessionID)
	}
	var records []GatewayTaskRecord
	if err := query.Order("recovery_last_activity desc, updated_at desc").Limit(limit).Find(&records).Error; err != nil {
		return nil, err
	}
	recoveries := make([]sessioncore.InterruptedTaskRecovery, 0, len(records))
	for _, record := range records {
		recoveries = append(recoveries, recoveryInfoFromRecord(record))
	}
	return recoveries, nil
}

func (g *GatewayService) resumeInterruptedTask(taskID, supplement string) (*GatewayTask, error) {
	if g == nil || g.db == nil || g.engine == nil {
		return nil, fmt.Errorf("gateway is not ready")
	}
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return nil, fmt.Errorf("task_id is required")
	}
	var record GatewayTaskRecord
	query := scopeRowsToPersistedOwner(
		g.db.Where("task_id = ? AND recovery_state = ?", taskID, sessioncore.RecoveryStateAvailable),
		defaultExecutionTenantID,
		"",
	)
	if err := query.First(&record).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errInterruptedTaskNotResumable
		}
		return nil, err
	}
	if record.QueueName == agentOSQueueName {
		return nil, errInterruptedTaskNotResumable
	}

	transcriptDir := sessioncore.TranscriptDirectory(g.config.Workspace)
	var transcript *sessioncore.SessionTranscript
	if journalPath := strings.TrimSpace(record.RecoveryJournal); journalPath != "" {
		if !pathWithin(transcriptDir, journalPath) {
			return nil, fmt.Errorf("recovery journal is outside the transcript directory")
		}
		var err error
		transcript, err = sessioncore.RecoverSessionTranscriptFromJournal(journalPath)
		if err != nil {
			return nil, fmt.Errorf("recover session journal: %w", err)
		}
		if transcript.SessionID != record.TaskID && transcript.SessionID != strings.TrimSpace(record.ResumedFromTaskID) {
			return nil, fmt.Errorf("recovery journal task identity mismatch")
		}
	} else if record.Status != "paused" {
		return nil, fmt.Errorf("recovery journal is unavailable")
	}

	newTaskID := g.nextSessionTaskID(record.SessionID)
	claim := g.db.Model(&GatewayTaskRecord{}).
		Where("task_id = ? AND recovery_state = ?", taskID, sessioncore.RecoveryStateAvailable).
		Updates(map[string]interface{}{
			"recovery_state":     sessioncore.RecoveryStateResuming,
			"resumed_by_task_id": newTaskID,
			"updated_at":         time.Now(),
		})
	if claim.Error != nil {
		return nil, claim.Error
	}
	if claim.RowsAffected != 1 {
		return nil, errInterruptedTaskNotResumable
	}

	message := record.Message
	if transcript != nil {
		message = sessioncore.BuildRecoveryPrompt(record.Message, transcript, supplement)
	} else if strings.TrimSpace(supplement) != "" {
		message += "\n\n[用户补充]: " + strings.TrimSpace(supplement)
	}
	channel := firstNonEmpty(record.Channel, "web")
	agentName := record.AgentName
	if transcript != nil {
		agentName = firstNonEmpty(agentName, transcript.AgentName)
	}
	var resumeCheckpoint *sessioncore.TurnCheckpoint
	if transcript != nil {
		resumeCheckpoint = sessioncore.CloneTurnCheckpoint(transcript.Checkpoint)
	}
	resumeJournal := record.RecoveryJournal
	if resumeCheckpoint != nil {
		// Persist a bootstrap checkpoint under the new task ID before the
		// goroutine starts. This closes the crash window between claiming the
		// source recovery and LLMAgent creating its own transcript.
		resumeCheckpoint.TaskID = newTaskID
		resumeCheckpoint.ReceiptTaskID = firstNonEmpty(
			resumeCheckpoint.ReceiptTaskID,
			transcript.SessionID,
			taskID,
		)
		bootstrap := sessioncore.NewSessionTranscript(newTaskID, agentName, transcriptDir)
		bootstrap.RecordLifecycleEvent("recovery", fmt.Sprintf(
			"bootstrap source_task=%s source_checkpoint_revision=%d",
			taskID,
			resumeCheckpoint.Revision,
		))
		if checkpointErr := bootstrap.RecordTurnCheckpoint(resumeCheckpoint); checkpointErr != nil || bootstrap.JournalError() != nil {
			g.db.Model(&GatewayTaskRecord{}).Where("task_id = ? AND recovery_state = ?", taskID, sessioncore.RecoveryStateResuming).
				Updates(map[string]interface{}{"recovery_state": sessioncore.RecoveryStateAvailable, "resumed_by_task_id": ""})
			if checkpointErr != nil {
				return nil, fmt.Errorf("persist resumed turn checkpoint: %w", checkpointErr)
			}
			return nil, fmt.Errorf("persist resumed turn journal: %w", bootstrap.JournalError())
		}
		resumeJournal = bootstrap.JournalPath()
	}
	request := gatewayTaskFromRecord(record).Request
	if request == nil {
		request = &MessageRequest{}
	}
	request.TaskID = newTaskID
	request.TaskType = firstNonEmpty(record.TaskType, "chat")
	request.SessionId = record.SessionID
	request.TenantID = normalizePersonalOwnerTenantID(record.TenantID)
	request.SubjectID = strings.TrimSpace(record.SubjectID)
	request.Message = message
	request.AgentName = agentName
	request.Channel = channel
	request.UserId = record.UserID
	request.ResolvedWorkspace = record.ResolvedWorkspace
	request.CreatedAt = time.Now()
	task := &GatewayTask{
		ID:                newTaskID,
		Request:           request,
		TenantID:          normalizePersonalOwnerTenantID(record.TenantID),
		SubjectID:         strings.TrimSpace(record.SubjectID),
		TenantScoped:      record.TenantScoped,
		Status:            "pending",
		CreatedAt:         time.Now(),
		ResolvedWorkspace: record.ResolvedWorkspace,
		ResumedFromTaskID: taskID,
		RecoveryJournal:   resumeJournal,
		ResumeCheckpoint:  resumeCheckpoint,
		ResumeSupplement:  strings.TrimSpace(supplement),
		ResultCh:          make(chan interface{}, 1),
		ProgressCh:        make(chan map[string]interface{}, 20),
		InterruptCh:       make(chan string, 5),
	}
	if err := SaveGatewayTaskToDB(g.db, task); err != nil {
		g.db.Model(&GatewayTaskRecord{}).Where("task_id = ? AND recovery_state = ?", taskID, sessioncore.RecoveryStateResuming).
			Updates(map[string]interface{}{"recovery_state": sessioncore.RecoveryStateAvailable, "resumed_by_task_id": ""})
		return nil, fmt.Errorf("persist resumed task: %w", err)
	}

	visibleMessage := "继续已暂停的任务"
	if record.Status != "paused" {
		visibleMessage = "继续上次因进程退出而中断的任务"
	}
	if strings.TrimSpace(supplement) != "" {
		visibleMessage += "：" + strings.TrimSpace(supplement)
	}
	if _, saveErr := g.saveChatMessage(record.SessionID, "user", agentName, visibleMessage, "pending", newTaskID, channel, record.UserID); saveErr != nil && g.logger != nil {
		g.logger.Printf("[Recovery] resumed task %s chat message was not saved: %v", newTaskID, saveErr)
	}

	g.tasksMu.Lock()
	if g.tasks == nil {
		g.tasks = make(map[string]*GatewayTask)
	}
	g.tasks[newTaskID] = task
	g.tasksMu.Unlock()
	if record.SessionID != "" {
		storeInterruptChannel(record.SessionID, newTaskID, task.InterruptCh)
	}
	g.db.Model(&GatewayTaskRecord{}).Where("task_id = ?", taskID).
		Updates(map[string]interface{}{"recovery_state": sessioncore.RecoveryStateResumed, "updated_at": time.Now()})
	g.tasksMu.Lock()
	if original := g.tasks[taskID]; original != nil {
		original.RecoveryState = sessioncore.RecoveryStateResumed
		original.ResumedByTaskID = newTaskID
	}
	g.tasksMu.Unlock()
	go g.processTaskWithSession(task, record.SessionID)
	return task, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func (g *GatewayService) handleListRecoveries(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	recoveries, err := g.listInterruptedTaskRecoveries(r.URL.Query().Get("session_id"), 100)
	if err != nil {
		sendJSONError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(map[string]interface{}{"recoveries": recoveries, "count": len(recoveries)})
}

func (g *GatewayService) handleResumeTask(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var request struct {
		TaskID  string `json:"task_id"`
		Message string `json:"message,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		sendJSONError(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	task, err := g.resumeInterruptedTask(request.TaskID, request.Message)
	if err != nil {
		status := http.StatusConflict
		if strings.TrimSpace(request.TaskID) == "" {
			status = http.StatusBadRequest
		} else if !errors.Is(err, errInterruptedTaskNotResumable) {
			status = http.StatusInternalServerError
		}
		sendJSONError(w, err.Error(), status)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"task_id":      task.ID,
		"status":       task.Status,
		"resumed_from": task.ResumedFromTaskID,
		"timestamp":    time.Now(),
	})
}
