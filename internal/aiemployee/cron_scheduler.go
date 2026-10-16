package aiemployee

import (
	"context"
	"fmt"
	"log"
	"math/rand"
	"strings"
	"sync"
	"time"

	"github.com/robfig/cron/v3"
)

// CronScheduler manages scheduled tasks for digital employees.
// Claude Code borrowed: ScheduleCronTool + proactive task scheduling
type CronScheduler struct {
	cron          *cron.Cron
	entries       map[string]cron.EntryID // task name -> entry ID
	mu            sync.RWMutex
	dispatch      func(context.Context, string, string, string) (string, error)
	logger        *log.Logger
	started       bool
	jitterMax     time.Duration // max jitter offset (default 5s)
	jitterEnabled bool
}

type ScheduledTask struct {
	Name     string    `json:"name"`
	CronExpr string    `json:"cron_expr"`
	DEID     string    `json:"ae_id"`
	Prompt   string    `json:"prompt"`
	NextRun  time.Time `json:"next_run,omitempty"`
	LastRun  time.Time `json:"last_run,omitempty"`
	LastErr  string    `json:"last_error,omitempty"`
	Enabled  bool      `json:"enabled"`
}

func NewCronScheduler(dispatch func(context.Context, string, string, string) (string, error), logger *log.Logger) *CronScheduler {
	return &CronScheduler{
		cron:          cron.New(cron.WithSeconds()),
		entries:       make(map[string]cron.EntryID),
		dispatch:      dispatch,
		logger:        logger,
		jitterMax:     5 * time.Second,
		jitterEnabled: true,
	}
}

func (cs *CronScheduler) Start() {
	if cs.started {
		return
	}
	cs.cron.Start()
	cs.started = true
	if cs.logger != nil {
		cs.logger.Printf("[CRON] Scheduler started")
	}
}

func (cs *CronScheduler) Stop() {
	if !cs.started {
		return
	}
	cs.cron.Stop()
	cs.started = false
	if cs.logger != nil {
		cs.logger.Printf("[CRON] Scheduler stopped")
	}
}

// AddTask registers a new scheduled task with optional jitter.
func (cs *CronScheduler) AddTask(task ScheduledTask) error {
	cs.mu.Lock()
	defer cs.mu.Unlock()

	// Apply jitter: add random offset to avoid thundering herd
	expr := task.CronExpr
	if cs.jitterEnabled && cs.jitterMax > 0 {
		expr = applyJitter(task.CronExpr, cs.jitterMax)
	}

	// Remove existing entry with same name
	if oldID, ok := cs.entries[task.Name]; ok {
		cs.cron.Remove(oldID)
	}

	entryID, err := cs.cron.AddFunc(expr, func() {
		cs.executeTask(task)
	})
	if err != nil {
		return fmt.Errorf("invalid cron expression '%s': %w", task.CronExpr, err)
	}

	cs.entries[task.Name] = entryID
	if cs.logger != nil {
		cs.logger.Printf("[CRON] Added task '%s': %s (DE=%s)", task.Name, task.CronExpr, task.DEID)
	}
	return nil
}

// RemoveTask removes a scheduled task.
func (cs *CronScheduler) RemoveTask(name string) error {
	cs.mu.Lock()
	defer cs.mu.Unlock()

	entryID, ok := cs.entries[name]
	if !ok {
		return fmt.Errorf("task '%s' not found", name)
	}

	cs.cron.Remove(entryID)
	delete(cs.entries, name)
	if cs.logger != nil {
		cs.logger.Printf("[CRON] Removed task '%s'", name)
	}
	return nil
}

// ListTasks returns all registered tasks with their next run times.
func (cs *CronScheduler) ListTasks() []ScheduledTask {
	cs.mu.RLock()
	defer cs.mu.RUnlock()

	var tasks []ScheduledTask
	for _, entry := range cs.cron.Entries() {
		for name, id := range cs.entries {
			if id == entry.ID {
				tasks = append(tasks, ScheduledTask{
					Name:    name,
					NextRun: entry.Next,
					Enabled: true,
				})
			}
		}
	}
	return tasks
}

// Load replaces the scheduled task set from employee configuration.
func (cs *CronScheduler) Load(tasks []ScheduledTask) int {
	count := 0
	for _, task := range tasks {
		if task.CronExpr == "" || !task.Enabled {
			continue
		}
		if err := cs.AddTask(task); err != nil {
			if cs.logger != nil {
				cs.logger.Printf("[CRON] Failed to load task for employee '%s': %v", task.DEID, err)
			}
			continue
		}
		count++
	}
	if cs.logger != nil {
		cs.logger.Printf("[CRON] Loaded %d tasks from DE configs", count)
	}
	return count
}

func (cs *CronScheduler) executeTask(task ScheduledTask) {
	if cs.logger != nil {
		cs.logger.Printf("[CRON] Executing task '%s' for DE=%s", task.Name, task.DEID)
	}

	if cs.dispatch == nil {
		if cs.logger != nil {
			cs.logger.Printf("[CRON] No dispatcher available for task '%s'", task.Name)
		}
		return
	}

	prompt := task.Prompt
	if prompt == "" {
		prompt = fmt.Sprintf("定时任务: %s", task.Name)
	}

	// Dispatch through gateway (non-blocking, fire-and-forget)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				if cs.logger != nil {
					cs.logger.Printf("[CRON] Panic in task '%s': %v", task.Name, r)
				}
			}
		}()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()

		result, err := cs.dispatch(ctx, task.DEID, prompt, "cron_"+task.Name)
		if err != nil {
			if cs.logger != nil {
				cs.logger.Printf("[CRON] Task '%s' failed: %v", task.Name, err)
			}
			return
		}
		if cs.logger != nil {
			cs.logger.Printf("[CRON] Task '%s' completed: %s", task.Name, truncateResult(result, 200))
		}
	}()
}

func truncateResult(value string, maxRunes int) string {
	runes := []rune(value)
	if len(runes) <= maxRunes {
		return value
	}
	return string(runes[:maxRunes]) + "..."
}

// applyJitter adds a random second offset to a cron expression's second field.
// Claude Code borrowed: CronJitterConfig — prevents fleet-wide :00 spikes
func applyJitter(expr string, maxJitter time.Duration) string {
	maxSec := int(maxJitter.Seconds())
	if maxSec <= 0 {
		return expr
	}
	jitterSec := rand.Intn(maxSec)
	fields := strings.Fields(expr)
	if len(fields) >= 1 {
		// Replace the first field (seconds) with jittered value
		fields[0] = fmt.Sprintf("%d", jitterSec)
		return strings.Join(fields, " ")
	}
	return expr
}
