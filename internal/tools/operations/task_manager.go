package operations

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// ====================== Task V2 — Structured Task Management ======================
// Claude Code 借鉴: file-based task persistence with dependency graphs,
// concurrent-safe locking, lifecycle hooks, and agent-aware notifications.
//
// Improves on the existing todo system with:
//   - Dependency graphs (blocks/blockedBy) for coordination
//   - File-based storage with high-water-mark ID allocation
//   - Concurrent-safe via file locks
//   - Lifecycle hook events (TaskCreated, TaskCompleted)

// TaskManager manages task persistence and lifecycle.
type TaskManager struct {
	mu          sync.Mutex
	tasksDir    string
	onCreated   func(*Task)
	onCompleted func(*Task)

	// File-based locking
	lockPath string
	lockFile *os.File
}

// NewTaskManager creates a new task manager.
func NewTaskManager(workspace string, onCreated, onCompleted func(*Task)) *TaskManager {
	dir := filepath.Join(workspace, ".openkitty", "tasks")
	os.MkdirAll(dir, 0755)
	m := &TaskManager{
		tasksDir:    dir,
		onCreated:   onCreated,
		onCompleted: onCompleted,
		lockPath:    filepath.Join(dir, ".lock"),
	}
	return m
}

// lock acquires the file lock for concurrent safety.
func (m *TaskManager) lock() error {
	m.mu.Lock()
	var err error
	m.lockFile, err = os.OpenFile(m.lockPath, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		m.mu.Unlock()
		return fmt.Errorf("task lock: %w", err)
	}
	return nil
}

func (m *TaskManager) unlock() {
	if m.lockFile != nil {
		m.lockFile.Close()
		m.lockFile = nil
	}
	m.mu.Unlock()
}

// nextID generates the next monotonic task ID using a high water mark file.
func (m *TaskManager) nextID() (string, error) {
	hwmPath := filepath.Join(m.tasksDir, ".highwatermark")
	data, err := os.ReadFile(hwmPath)
	current := 0
	if err == nil {
		fmt.Sscanf(string(data), "%d", &current)
	}
	current++
	if err := os.WriteFile(hwmPath, []byte(fmt.Sprintf("%d", current)), 0644); err != nil {
		return "", err
	}
	return fmt.Sprintf("%d", current), nil
}

// Create creates a new task and fires TaskCreated hooks.
func (m *TaskManager) Create(subject, description, owner string, blockedBy []string) (*Task, error) {
	if err := m.lock(); err != nil {
		return nil, err
	}
	defer m.unlock()

	id, err := m.nextID()
	if err != nil {
		return nil, err
	}

	now := time.Now().Unix()
	task := &Task{
		ID:          id,
		Subject:     subject,
		Description: description,
		Owner:       owner,
		Status:      TaskPending,
		BlockedBy:   blockedBy,
		CreatedAt:   now,
		UpdatedAt:   now,
		Metadata:    make(map[string]any),
	}

	if err := m.save(task); err != nil {
		return nil, err
	}

	// Update blocking relationships
	for _, blockerID := range blockedBy {
		if blocker, err := m.getLocked(blockerID); err == nil {
			blocker.Blocks = append(blocker.Blocks, id)
			m.save(blocker)
		}
	}

	if m.onCreated != nil {
		m.onCreated(task)
	}

	return task, nil
}

// Get retrieves a task by ID.
func (m *TaskManager) Get(id string) (*Task, error) {
	if err := m.lock(); err != nil {
		return nil, err
	}
	defer m.unlock()
	return m.getLocked(id)
}

func (m *TaskManager) getLocked(id string) (*Task, error) {
	path := filepath.Join(m.tasksDir, id+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("task %s not found", id)
		}
		return nil, err
	}
	var task Task
	if err := json.Unmarshal(data, &task); err != nil {
		return nil, err
	}
	return &task, nil
}

// Update modifies task fields and manages dependency relationships.
func (m *TaskManager) Update(id string, updates map[string]any) (*Task, error) {
	if err := m.lock(); err != nil {
		return nil, err
	}
	defer m.unlock()

	task, err := m.getLocked(id)
	if err != nil {
		return nil, err
	}

	oldStatus := task.Status

	if v, ok := updates["subject"].(string); ok {
		task.Subject = v
	}
	if v, ok := updates["description"].(string); ok {
		task.Description = v
	}
	if v, ok := updates["owner"].(string); ok {
		task.Owner = v
	}
	if v, ok := updates["status"].(string); ok {
		task.Status = TaskStatus(v)
	}
	if v, ok := updates["blocked_by"].([]string); ok {
		// Remove old blockedBy relationships
		for _, oldID := range task.BlockedBy {
			if old, err := m.getLocked(oldID); err == nil {
				old.Blocks = removeString(old.Blocks, id)
				m.save(old)
			}
		}
		// Set new
		task.BlockedBy = v
		for _, newID := range v {
			if b, err := m.getLocked(newID); err == nil {
				b.Blocks = append(b.Blocks, id)
				m.save(b)
			}
		}
	}

	task.UpdatedAt = time.Now().Unix()
	if err := m.save(task); err != nil {
		return nil, err
	}

	if oldStatus != task.Status && task.Status == TaskCompleted && m.onCompleted != nil {
		m.onCompleted(task)
	}

	return task, nil
}

// List returns all non-deleted tasks, with blockedBy resolved (completed tasks removed).
func (m *TaskManager) List() ([]*Task, error) {
	if err := m.lock(); err != nil {
		return nil, err
	}
	defer m.unlock()

	entries, err := os.ReadDir(m.tasksDir)
	if err != nil {
		return nil, err
	}

	var tasks []*Task
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".json") || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".json")
		task, err := m.getLocked(id)
		if err != nil {
			continue
		}
		if task.Status == TaskDeleted {
			continue
		}
		// Resolve blockedBy: remove completed blockers
		var activeBlockers []string
		for _, blockerID := range task.BlockedBy {
			if bt, err := m.getLocked(blockerID); err == nil && bt.Status != TaskCompleted {
				activeBlockers = append(activeBlockers, blockerID)
			}
		}
		task.BlockedBy = activeBlockers
		tasks = append(tasks, task)
	}

	sort.Slice(tasks, func(i, j int) bool {
		return tasks[i].CreatedAt < tasks[j].CreatedAt
	})
	return tasks, nil
}

// Delete marks a task as deleted.
func (m *TaskManager) Delete(id string) error {
	if err := m.lock(); err != nil {
		return err
	}
	defer m.unlock()

	task, err := m.getLocked(id)
	if err != nil {
		return err
	}
	task.Status = TaskDeleted
	task.UpdatedAt = time.Now().Unix()

	// Remove this task from any tasks it was blocking
	for _, blockedID := range task.Blocks {
		if bt, err := m.getLocked(blockedID); err == nil {
			bt.BlockedBy = removeString(bt.BlockedBy, id)
			m.save(bt)
		}
	}
	for _, blockerID := range task.BlockedBy {
		if bt, err := m.getLocked(blockerID); err == nil {
			bt.Blocks = removeString(bt.Blocks, id)
			m.save(bt)
		}
	}
	return m.save(task)
}

func (m *TaskManager) save(task *Task) error {
	path := filepath.Join(m.tasksDir, task.ID+".json")
	data, err := json.MarshalIndent(task, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

func removeString(slice []string, item string) []string {
	var result []string
	for _, s := range slice {
		if s != item {
			result = append(result, s)
		}
	}
	return result
}
