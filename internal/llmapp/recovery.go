package llmapp

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func (m *LLMAppManager) configureRuntime(runtime Runtime) {
	if m == nil {
		return
	}
	runtime = normalizeRuntime(runtime)
	m.runtimeMu.Lock()
	shouldScan := false
	if runtime.ModelCaller != nil {
		previousDefault := m.runtime
		m.runtime = runtime
		if m.runtimes == nil {
			m.runtimes = make(map[string]Runtime)
		}
		if runtime.BindingID != "" {
			previous, existed := m.runtimes[runtime.BindingID]
			m.runtimes[runtime.BindingID] = runtime
			shouldScan = !existed || (previous.DisableAutoResume && !runtime.DisableAutoResume)
		} else {
			shouldScan = previousDefault.ModelCaller == nil ||
				(previousDefault.DisableAutoResume && !runtime.DisableAutoResume)
		}
	} else if m.runtime.pdfExtractor == nil {
		m.runtime.pdfExtractor = runtime.pdfExtractor
	}
	disabled := runtime.DisableAutoResume
	m.runtimeMu.Unlock()
	if runtime.ModelCaller != nil && shouldScan && !disabled {
		// Scan when each stable binding first becomes available. Startup creates
		// Agents sequentially, so a single first-runtime scan can otherwise miss
		// unfinished runs owned by Agents registered later.
		go m.recoverUnfinishedRuns()
	}
}

func (m *LLMAppManager) currentRuntime(bindingID string) (Runtime, bool) {
	m.runtimeMu.RLock()
	defer m.runtimeMu.RUnlock()
	bindingID = strings.ToLower(strings.TrimSpace(bindingID))
	if bindingID != "" {
		runtime, ok := m.runtimes[bindingID]
		return normalizeRuntime(runtime), ok && runtime.ModelCaller != nil
	}
	return normalizeRuntime(m.runtime), m.runtime.ModelCaller != nil
}

func (m *LLMAppManager) recoverUnfinishedRuns() {
	// Serialize the one-time startup scan with new run creation. Otherwise the
	// scanner can observe run.json in the narrow window before createRun has
	// installed and leased its in-memory worker.
	m.startMu.Lock()
	defer m.startMu.Unlock()
	dir := filepath.Join(m.root, "runs")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if !os.IsNotExist(err) && m.logger != nil {
			m.logger.Printf("scan unfinished llm_app runs: %v", err)
		}
		return
	}
	for _, entry := range entries {
		if !entry.IsDir() || !llmAppRunIDPattern.MatchString(strings.ToLower(entry.Name())) {
			continue
		}
		var record LLMAppRunRecord
		if err := readLLMAppJSON(filepath.Join(dir, entry.Name(), "run.json"), &record); err != nil || isTerminalLLMAppStatus(record.Status) {
			continue
		}
		if _, err := m.getRun(entry.Name()); err != nil && m.logger != nil {
			m.logger.Printf("recover llm_app run %s: %v", entry.Name(), err)
		}
	}
}

func (m *LLMAppManager) ensureRunWorker(run *llmAppManagedRun) error {
	if run == nil {
		return errors.New("llm_app run is unavailable")
	}
	if err := m.refreshIdleRunState(run); err != nil {
		return err
	}
	run.mu.Lock()
	bindingID := run.record.ExecutorBindingID
	run.mu.Unlock()
	runtime, available := m.currentRuntime(bindingID)
	if !available || runtime.DisableAutoResume {
		return nil
	}
	run.mu.Lock()
	if isTerminalLLMAppStatus(run.record.Status) || run.workerActive || run.workerStarting {
		run.mu.Unlock()
		return nil
	}
	run.workerStarting = true
	runID := run.record.RunID
	run.mu.Unlock()

	clearStarting := func() {
		run.mu.Lock()
		run.workerStarting = false
		run.mu.Unlock()
	}
	var definition LLMAppDefinition
	if err := readLLMAppJSON(filepath.Join(m.runDir(runID), "definition.json"), &definition); err != nil {
		clearStarting()
		return fmt.Errorf("load frozen llm_app definition: %w", err)
	}
	if err := normalizeLLMAppDefinition(&definition); err != nil {
		clearStarting()
		return err
	}
	var request llmAppRunRequest
	if err := readLLMAppJSON(filepath.Join(m.runDir(runID), "request.json"), &request); err != nil {
		clearStarting()
		return fmt.Errorf("load llm_app request: %w", err)
	}
	acquired, err := m.acquireRunLease(run, llmAppLeaseDuration(definition.Health.HeartbeatSeconds))
	if err != nil {
		clearStarting()
		return err
	}
	if !acquired {
		clearStarting()
		_ = m.refreshIdleRunState(run)
		m.scheduleRunRecovery(run)
		return nil
	}

	base := context.Background()
	if runtime.RestoreContext != nil {
		base = runtime.RestoreContext(base, run.snapshot())
		if base == nil {
			base = context.Background()
		}
	}
	runCtx, cancel := newLLMAppExecutionContext(base, llmAppLifecycleDetached, definition.Budget, run.snapshot().CreatedAt)
	run.mu.Lock()
	if run.record.Lifecycle == "" {
		// Old releases detached workers from the invoking context.
		run.record.Lifecycle = llmAppLifecycleDetached
	}
	run.budget = definition.Budget
	run.workflow = definition.Workflow
	run.maxItems = definition.MaxItems
	run.maxItemChars = definition.MaxItemChars
	run.cancel = cancel
	run.done = make(chan struct{})
	run.workerStarting = false
	run.workerActive = true
	stopping := run.record.Status == llmAppStatusStopping
	run.pauseRequested = run.record.Status == llmAppStatusPaused || run.record.Status == llmAppStatusPausing
	if run.pauseRequested && run.resumeCh == nil {
		run.resumeCh = make(chan struct{})
	}
	run.mu.Unlock()
	if stopping {
		cancel()
	}
	if err := m.loadCommandReceipts(run); err != nil && m.logger != nil {
		m.logger.Printf("reload llm_app command receipts %s: %v", runID, err)
	}
	go m.executeRun(runCtx, run, definition, request, runtime, true)
	return nil
}

// refreshIdleRunState makes status/event readers on a non-owner instance use
// the durable snapshot written by the current lease owner. It also prevents a
// delayed recovery timer from resurrecting a run that another instance has
// already completed.
func (m *LLMAppManager) refreshIdleRunState(run *llmAppManagedRun) error {
	if run == nil {
		return errors.New("llm_app run is unavailable")
	}
	run.mu.Lock()
	if run.workerActive || run.workerStarting {
		run.mu.Unlock()
		return nil
	}
	runID := run.record.RunID
	run.mu.Unlock()

	var record LLMAppRunRecord
	if err := readLLMAppJSON(filepath.Join(m.runDir(runID), "run.json"), &record); err != nil {
		return err
	}
	if record.RunID != runID {
		return errors.New("durable llm_app run id does not match its directory")
	}
	var plan LLMAppRunPlan
	planErr := readLLMAppJSON(filepath.Join(m.runDir(runID), "plan.json"), &plan)

	run.mu.Lock()
	defer run.mu.Unlock()
	if run.workerActive || run.workerStarting {
		return nil
	}
	if record.Lifecycle == "" {
		record.Lifecycle = llmAppLifecycleDetached
	}
	run.record = record
	if planErr == nil {
		run.plan = plan
	}
	return nil
}

func (m *LLMAppManager) scheduleRunRecovery(run *llmAppManagedRun) {
	if run == nil {
		return
	}
	run.mu.Lock()
	if run.recoveryRetryPending || run.workerActive || isTerminalLLMAppStatus(run.record.Status) {
		run.mu.Unlock()
		return
	}
	run.recoveryRetryPending = true
	runID := run.record.RunID
	run.mu.Unlock()
	delay := time.Second
	var lease llmAppLease
	if readLLMAppJSON(m.leasePath(runID), &lease) == nil {
		if remaining := time.Until(lease.ExpiresAt) + 100*time.Millisecond; remaining > delay {
			delay = remaining
		}
	}
	if delay > 2*time.Minute {
		delay = 2 * time.Minute
	}
	go func() {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		<-timer.C
		run.mu.Lock()
		run.recoveryRetryPending = false
		run.mu.Unlock()
		if err := m.ensureRunWorker(run); err != nil && m.logger != nil {
			m.logger.Printf("retry llm_app recovery %s: %v", runID, err)
			m.scheduleRunRecovery(run)
		}
	}()
}
