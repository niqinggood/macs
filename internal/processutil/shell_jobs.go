package processutil

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type ShellJobStatus string

const (
	ShellJobRunning ShellJobStatus = "running"
	ShellJobExited  ShellJobStatus = "exited"
	ShellJobStopped ShellJobStatus = "stopped"
	ShellJobFailed  ShellJobStatus = "failed"
)

type ShellJob struct {
	ID        string         `json:"id"`
	TaskID    string         `json:"task_id"`
	Command   string         `json:"command"`
	PID       int            `json:"pid"`
	LogPath   string         `json:"log_path"`
	WorkDir   string         `json:"work_dir"`
	Status    ShellJobStatus `json:"status"`
	StartedAt time.Time      `json:"started_at"`
	EndedAt   *time.Time     `json:"ended_at,omitempty"`

	owner   ShellResourceOwner
	process *os.Process
}

type ShellJobManager struct {
	mu           sync.RWMutex
	jobs         map[string]*ShellJob
	resolveOwner ShellOwnerResolver
	canAccess    ShellAccessChecker
}

func NewShellJobManager(resolveOwner ShellOwnerResolver, canAccess ShellAccessChecker) *ShellJobManager {
	return &ShellJobManager{
		jobs:         make(map[string]*ShellJob),
		resolveOwner: resolveOwner,
		canAccess:    canAccess,
	}
}

func (m *ShellJobManager) Register(taskID, command, logPath, workDir string, process *os.Process) ShellJob {
	return m.RegisterOwned(taskID, command, logPath, workDir, process, ShellResourceOwner{})
}

func (m *ShellJobManager) RegisterForContext(ctx context.Context, taskID, command, logPath, workDir string, process *os.Process) (ShellJob, error) {
	owner := ShellResourceOwner{}
	var err error
	if m.resolveOwner != nil {
		owner, err = m.resolveOwner(ctx)
	}
	if err != nil {
		return ShellJob{}, err
	}
	return m.RegisterOwned(taskID, command, logPath, workDir, process, owner), nil
}

// RegisterOwned records a process after the host has already resolved and
// validated its owner. This preserves the start/register transaction boundary.
func (m *ShellJobManager) RegisterOwned(taskID, command, logPath, workDir string, process *os.Process, owner ShellResourceOwner) ShellJob {
	if taskID == "" {
		taskID = "manual"
	}
	pid := 0
	if process != nil {
		pid = process.Pid
	}
	job := &ShellJob{
		ID:        fmt.Sprintf("%s_%d", sanitizeShellMarkerName(taskID), time.Now().UnixNano()),
		TaskID:    taskID,
		Command:   command,
		PID:       pid,
		LogPath:   filepath.ToSlash(logPath),
		WorkDir:   workDir,
		Status:    ShellJobRunning,
		StartedAt: time.Now(),
		owner:     owner,
		process:   process,
	}
	m.mu.Lock()
	m.jobs[job.ID] = job
	m.mu.Unlock()
	return *job
}

func (m *ShellJobManager) Finish(jobID string, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	job := m.jobs[jobID]
	if job == nil || job.Status == ShellJobStopped {
		return
	}
	now := time.Now()
	job.EndedAt = &now
	if err != nil {
		job.Status = ShellJobFailed
	} else {
		job.Status = ShellJobExited
	}
	job.process = nil
}

func (m *ShellJobManager) List() []ShellJob {
	return m.listAuthorized(context.Background())
}

func (m *ShellJobManager) ListForContext(ctx context.Context) []ShellJob {
	return m.listAuthorized(ctx)
}

func (m *ShellJobManager) listAuthorized(ctx context.Context) []ShellJob {
	m.mu.RLock()
	out := make([]ShellJob, 0, len(m.jobs))
	for _, job := range m.jobs {
		if !m.resourceAccessible(ctx, job.owner) {
			continue
		}
		copyJob := *job
		copyJob.process = nil
		out = append(out, copyJob)
	}
	m.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool {
		return out[i].StartedAt.After(out[j].StartedAt)
	})
	return out
}

func (m *ShellJobManager) Get(jobID string) (ShellJob, bool) {
	return m.getAuthorized(context.Background(), jobID)
}

func (m *ShellJobManager) GetForContext(ctx context.Context, jobID string) (ShellJob, bool) {
	return m.getAuthorized(ctx, jobID)
}

func (m *ShellJobManager) getAuthorized(ctx context.Context, jobID string) (ShellJob, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	job := m.jobs[jobID]
	if job == nil || !m.resourceAccessible(ctx, job.owner) {
		return ShellJob{}, false
	}
	copyJob := *job
	copyJob.process = nil
	return copyJob, true
}

func (m *ShellJobManager) Stop(jobID string) error {
	return m.stopAuthorized(context.Background(), jobID)
}

func (m *ShellJobManager) StopForContext(ctx context.Context, jobID string) error {
	return m.stopAuthorized(ctx, jobID)
}

func (m *ShellJobManager) stopAuthorized(ctx context.Context, jobID string) error {
	m.mu.Lock()
	job := m.jobs[jobID]
	if job == nil || !m.resourceAccessible(ctx, job.owner) {
		m.mu.Unlock()
		return fmt.Errorf("shell job %q not found", jobID)
	}
	process := job.process
	if job.Status != ShellJobRunning {
		m.mu.Unlock()
		return nil
	}
	job.Status = ShellJobStopped
	now := time.Now()
	job.EndedAt = &now
	job.process = nil
	m.mu.Unlock()

	if process == nil {
		return nil
	}
	return TerminateTree(process)
}

func (m *ShellJobManager) resourceAccessible(ctx context.Context, owner ShellResourceOwner) bool {
	if m.canAccess == nil {
		return !owner.Protected
	}
	return m.canAccess(ctx, owner)
}

func ReadShellJobLog(path string, maxLines int) (string, error) {
	if maxLines <= 0 {
		maxLines = 200
	}
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()

	var lines []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
		if len(lines) > maxLines {
			lines = lines[1:]
		}
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	return strings.Join(lines, "\n"), nil
}
