package agents

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"
)

// ProactiveConfig controls autonomous agent behavior.
type ProactiveConfig struct {
	Enabled         bool          `json:"enabled"`
	CheckInterval   time.Duration `json:"check_interval"` // how often to check for work
	MaxTasksPerRun  int           `json:"max_tasks_per_run"`
	RequireApproval bool          `json:"require_approval"` // ask before executing
}

// ProactiveEngine manages autonomous background task execution.
// Inspired by Claude Code's proactive mode.
type ProactiveEngine struct {
	config ProactiveConfig
	agent  ProactiveAgent
	tasks  chan ProactiveTask
	deps   ProactiveDependencies

	lifecycleMu sync.Mutex
	started     bool
	stopped     bool
	runCtx      context.Context
	cancel      context.CancelFunc
	done        chan struct{}
}

type ProactiveAgent interface {
	Generate(context.Context, string) (string, error)
}

type ProactiveDependencies struct {
	DiscoverTasks   func() []ProactiveTask
	RequestApproval func(ProactiveTask)
}

// ProactiveTask is a self-initiated task for the agent to work on.
type ProactiveTask struct {
	ID          string    `json:"id"`
	Description string    `json:"description"`
	Priority    int       `json:"priority"` // 1=urgent, 5=low
	CreatedAt   time.Time `json:"created_at"`
	Source      string    `json:"source"` // "memory", "system", "schedule"
}

func NewProactiveEngine(agent ProactiveAgent, cfg ProactiveConfig, dependencies ...ProactiveDependencies) *ProactiveEngine {
	if !cfg.Enabled || agent == nil {
		return nil
	}
	if cfg.CheckInterval == 0 {
		cfg.CheckInterval = 5 * time.Minute
	}
	if cfg.MaxTasksPerRun == 0 {
		cfg.MaxTasksPerRun = 3
	}
	runCtx, cancel := context.WithCancel(context.Background())
	engine := &ProactiveEngine{
		config: cfg,
		agent:  agent,
		tasks:  make(chan ProactiveTask, 10),
		runCtx: runCtx,
		cancel: cancel,
		done:   make(chan struct{}),
	}
	if len(dependencies) > 0 {
		engine.deps = dependencies[0]
	}
	return engine
}

// Start begins autonomous background checking.
func (pe *ProactiveEngine) Start() {
	if pe == nil {
		return
	}
	pe.lifecycleMu.Lock()
	if pe.started || pe.stopped {
		pe.lifecycleMu.Unlock()
		return
	}
	pe.started = true
	pe.lifecycleMu.Unlock()

	go func() {
		defer close(pe.done)
		log.Printf("[PROACTIVE] started (interval: %v)", pe.config.CheckInterval)
		initialDelay := time.NewTimer(30 * time.Second)
		select {
		case <-pe.runCtx.Done():
			if !initialDelay.Stop() {
				select {
				case <-initialDelay.C:
				default:
				}
			}
			log.Printf("[PROACTIVE] stopped")
			return
		case <-initialDelay.C:
		}

		ticker := time.NewTicker(pe.config.CheckInterval)
		defer ticker.Stop()

		for {
			select {
			case <-pe.runCtx.Done():
				log.Printf("[PROACTIVE] stopped")
				return
			case <-ticker.C:
				pe.checkForWork()
			case task := <-pe.tasks:
				pe.executeTask(pe.runCtx, task)
			}
		}
	}()
}

func (pe *ProactiveEngine) Stop() {
	if pe == nil {
		return
	}
	pe.lifecycleMu.Lock()
	if !pe.stopped {
		pe.stopped = true
		pe.cancel()
	}
	started := pe.started
	done := pe.done
	pe.lifecycleMu.Unlock()

	// Wait for the worker to release its agent reference. This is bounded by
	// cancellation because executeTask derives its context from runCtx.
	if started {
		<-done
	}
}

// checkForWork scans memory and system state for pending tasks.
func (pe *ProactiveEngine) checkForWork() {
	// 1. Check for scheduled tasks (cron)
	// 2. Check for stale checkpoints in pipeline
	// 3. Check memory for unresolved items
	tasks := pe.discoverTasks()
	if len(tasks) > 0 {
		log.Printf("[PROACTIVE] discovered %d tasks", len(tasks))
		for _, t := range tasks {
			select {
			case pe.tasks <- t:
			case <-pe.runCtx.Done():
				return
			}
		}
	}
}

// discoverTasks finds pending work from various sources.
func (pe *ProactiveEngine) discoverTasks() []ProactiveTask {
	if pe == nil || pe.deps.DiscoverTasks == nil {
		return nil
	}
	return pe.deps.DiscoverTasks()
}

// executeTask runs a single task using the agent.
func (pe *ProactiveEngine) executeTask(parent context.Context, task ProactiveTask) {
	log.Printf("[PROACTIVE] executing: %s", task.Description)

	if pe.config.RequireApproval {
		if pe.deps.RequestApproval != nil {
			pe.deps.RequestApproval(task)
		}
		log.Printf("[PROACTIVE] task pending approval: %s", task.Description)
		return
	}

	ctx, cancel := context.WithTimeout(parent, 5*time.Minute)
	defer cancel()

	result, err := pe.agent.Generate(ctx, task.Description)
	if err != nil {
		log.Printf("[PROACTIVE] task failed: %v", err)
		return
	}
	log.Printf("[PROACTIVE] task completed: %s", truncateProactiveResult(result, 100))
}

func truncateProactiveResult(value string, maxRunes int) string {
	runes := []rune(value)
	if len(runes) <= maxRunes {
		return value
	}
	return string(runes[:maxRunes]) + "..."
}

// ScheduleTask adds a task to the proactive queue.
func (pe *ProactiveEngine) ScheduleTask(desc string, priority int) {
	if pe == nil {
		return
	}
	task := ProactiveTask{
		ID:          fmt.Sprintf("sched_%d", time.Now().Unix()),
		Description: desc,
		Priority:    priority,
		CreatedAt:   time.Now(),
		Source:      "schedule",
	}
	select {
	case pe.tasks <- task:
	case <-pe.runCtx.Done():
	}
}

func (pe *ProactiveEngine) QueueLength() int {
	if pe == nil {
		return 0
	}
	return len(pe.tasks)
}

func (pe *ProactiveEngine) SetEnabled(enabled bool) {
	if pe != nil {
		pe.config.Enabled = enabled
	}
}
