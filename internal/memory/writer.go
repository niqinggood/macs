package memory

import (
	"log"
	"sync"
	"time"
)

// ====================== Async Memory Writer ======================
// 借鉴 DeerFlow MemoryMiddleware + MemoryUpdateQueue:
//   - 记忆写入不在主循环同步执行，改为异步队列
//   - Debounce: 同一 session 30s 内多次写入合并
//   - 独立 goroutine worker，不阻塞 agent 响应
//   - 优雅关闭: Stop() 等待队列排空

// WriteJob represents a pending memory write operation.
type WriteJob struct {
	Type      string                 // "task_log", "memory_event", "post_session"
	TaskID    string                 // task identifier
	SessionID string                 // for debounce key
	AgentName string                 // agent name
	Data      map[string]interface{} // write payload
	CreatedAt time.Time
}

type WriteDependencies struct {
	SaveTaskLog     func(taskID, phase, content string) error
	SaveMemoryEvent func(event interface{}) error
}

// WriteQueue is a debounced, async memory write queue.
// Each (sessionID, agentName) key gets one pending slot — new writes
// to the same key replace the previous one within the debounce window.
type WriteQueue struct {
	mu       sync.Mutex
	pending  map[string]*WriteJob // key: "sessionID:agentName"
	jobCh    chan *WriteJob       // channel to worker
	writer   WriteDependencies
	logger   *log.Logger
	debounce time.Duration // how long to wait before flushing
	stopCh   chan struct{}
	wg       sync.WaitGroup
	running  bool
}

// NewWriteQueue creates a new async memory write queue.
func NewWriteQueue(writer WriteDependencies, logger *log.Logger) *WriteQueue {
	if logger == nil {
		logger = log.Default()
	}
	return &WriteQueue{
		pending:  make(map[string]*WriteJob),
		jobCh:    make(chan *WriteJob, 100),
		writer:   writer,
		logger:   logger,
		debounce: 30 * time.Second, // DeerFlow default
		stopCh:   make(chan struct{}),
	}
}

// Start launches the background writer goroutine.
func (q *WriteQueue) Start() {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.running {
		return
	}
	q.stopCh = make(chan struct{})
	q.running = true
	q.wg.Add(1)
	go q.worker()
	q.logger.Printf("[MemoryQueue] started (debounce=%v)", q.debounce)
}

// Stop gracefully shuts down the writer, flushing pending jobs.
func (q *WriteQueue) Stop() {
	q.mu.Lock()
	if !q.running {
		q.mu.Unlock()
		q.flushAll()
		return
	}
	q.running = false
	stopCh := q.stopCh
	q.mu.Unlock()

	// Flush all pending jobs
	q.flushAll()

	close(stopCh)
	q.wg.Wait()
	q.logger.Printf("[MemoryQueue] stopped")
}

// Enqueue adds a memory write job. Non-blocking — always returns immediately.
// If a job with the same key already exists in the pending queue, it is replaced
// (debounce — only the latest write for a session survives).
func (q *WriteQueue) Enqueue(job *WriteJob) {
	if q == nil || job == nil {
		return
	}
	job.CreatedAt = time.Now()
	key := job.SessionID + ":" + job.AgentName

	q.mu.Lock()
	q.pending[key] = job // replace previous pending job for same key
	q.mu.Unlock()

	// Non-blocking send to worker
	select {
	case q.jobCh <- job:
	default:
		// Channel full — worker will pick up pending jobs on next tick anyway
		q.logger.Printf("[MemoryQueue] channel full, job queued in pending map")
	}
}

// EnqueueTaskLog is a convenience method for SaveTaskLog calls.
func (q *WriteQueue) EnqueueTaskLog(taskID, phase, content string) {
	q.Enqueue(&WriteJob{
		Type:      "task_log",
		TaskID:    taskID,
		SessionID: taskID, // task logs are keyed by taskID
		Data: map[string]interface{}{
			"phase":   phase,
			"content": content,
		},
	})
}

// EnqueueMemoryEvent is a convenience method for SaveMemoryEvent.
func (q *WriteQueue) EnqueueMemoryEvent(sessionID, agentName string, event interface{}) {
	q.Enqueue(&WriteJob{
		Type:      "memory_event",
		SessionID: sessionID,
		AgentName: agentName,
		Data: map[string]interface{}{
			"event": event,
		},
	})
}

// ====================== Internal ======================

func (q *WriteQueue) worker() {
	defer q.wg.Done()

	ticker := time.NewTicker(q.debounce)
	defer ticker.Stop()

	for {
		select {
		case <-q.stopCh:
			return
		case job := <-q.jobCh:
			// Process immediately? No — debounce: add to pending and wait
			// The actual flush happens on ticker
			_ = job
		case <-ticker.C:
			q.flushPending()
		}
	}
}

// flushPending writes all debounced pending jobs to the memory store.
func (q *WriteQueue) flushPending() {
	q.mu.Lock()
	if len(q.pending) == 0 {
		q.mu.Unlock()
		return
	}

	// Take a snapshot and clear pending
	jobs := make([]*WriteJob, 0, len(q.pending))
	for key, job := range q.pending {
		jobs = append(jobs, job)
		delete(q.pending, key)
	}
	q.mu.Unlock()

	if q.writer.SaveTaskLog == nil && q.writer.SaveMemoryEvent == nil {
		q.logger.Printf("[MemoryQueue] store is nil, skipping %d jobs", len(jobs))
		return
	}

	// Write all jobs
	for _, job := range jobs {
		q.executeJob(job)
	}
	q.logger.Printf("[MemoryQueue] flushed %d jobs", len(jobs))
}

// flushAll immediately writes all pending jobs (for Stop()).
func (q *WriteQueue) flushAll() {
	q.mu.Lock()
	if len(q.pending) == 0 {
		q.mu.Unlock()
		return
	}
	jobs := make([]*WriteJob, 0, len(q.pending))
	for key, job := range q.pending {
		jobs = append(jobs, job)
		delete(q.pending, key)
	}
	q.mu.Unlock()

	if q.writer.SaveTaskLog == nil && q.writer.SaveMemoryEvent == nil {
		return
	}
	for _, job := range jobs {
		q.executeJob(job)
	}
	q.logger.Printf("[MemoryQueue] final flush: %d jobs", len(jobs))
}

func (q *WriteQueue) executeJob(job *WriteJob) {
	switch job.Type {
	case "task_log":
		phase, _ := job.Data["phase"].(string)
		content, _ := job.Data["content"].(string)
		if phase != "" && content != "" && q.writer.SaveTaskLog != nil {
			if err := q.writer.SaveTaskLog(job.TaskID, phase, content); err != nil {
				q.logger.Printf("[MemoryQueue] SaveTaskLog error: %v", err)
			}
		}
	case "memory_event":
		if event, ok := job.Data["event"]; ok && q.writer.SaveMemoryEvent != nil {
			if err := q.writer.SaveMemoryEvent(event); err != nil {
				q.logger.Printf("[MemoryQueue] SaveMemoryEvent error: %v", err)
			}
		}
	default:
		q.logger.Printf("[MemoryQueue] unknown job type: %s", job.Type)
	}
}

// ====================== Global Instance ======================

var (
	globalWriteQueueMu sync.Mutex
	globalWriteQueue   *WriteQueue
)

// InitGlobalWriteQueue initializes the process async memory queue.
func InitGlobalWriteQueue(writer WriteDependencies, logger *log.Logger) {
	globalWriteQueueMu.Lock()
	defer globalWriteQueueMu.Unlock()
	if globalWriteQueue != nil {
		globalWriteQueue.Stop()
	}
	globalWriteQueue = NewWriteQueue(writer, logger)
	globalWriteQueue.Start()
}

// ShutdownGlobalWriteQueue stops the process queue gracefully.
func ShutdownGlobalWriteQueue() {
	globalWriteQueueMu.Lock()
	defer globalWriteQueueMu.Unlock()
	if globalWriteQueue != nil {
		globalWriteQueue.Stop()
		globalWriteQueue = nil
	}
}
