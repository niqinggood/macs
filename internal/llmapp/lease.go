package llmapp

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

var ErrLLMAppLeaseLost = errors.New("llm_app worker lease lost")
var ErrLLMAppWorkerNotLocal = errors.New("llm_app run is owned by another worker instance")

type llmAppLease struct {
	RunID      string    `json:"run_id"`
	WorkerID   string    `json:"worker_id"`
	Token      string    `json:"token"`
	AcquiredAt time.Time `json:"acquired_at"`
	ExpiresAt  time.Time `json:"expires_at"`
}

func newLLMAppWorkerID() string {
	value, err := newLLMAppOpaqueID("worker_", 8)
	if err == nil {
		return value
	}
	return fmt.Sprintf("worker_%d_%d", os.Getpid(), time.Now().UnixNano())
}

func newLLMAppOpaqueID(prefix string, size int) (string, error) {
	random := make([]byte, size)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	return prefix + hex.EncodeToString(random), nil
}

func llmAppLeaseDuration(heartbeatSeconds int) time.Duration {
	if heartbeatSeconds < 5 {
		heartbeatSeconds = 5
	}
	duration := time.Duration(heartbeatSeconds*4) * time.Second
	if duration < 30*time.Second {
		duration = 30 * time.Second
	}
	return duration
}

func (m *LLMAppManager) leasePath(runID string) string {
	return filepath.Join(m.runDir(runID), "lease.json")
}

func (m *LLMAppManager) leaseGuardPath(runID string) string {
	return filepath.Join(m.runDir(runID), ".lease-guard")
}

func (m *LLMAppManager) withLeaseGuard(runID string, operation func() error) error {
	guard := m.leaseGuardPath(runID)
	if err := os.MkdirAll(filepath.Dir(guard), 0o700); err != nil {
		return err
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		err := os.Mkdir(guard, 0o700)
		if err == nil {
			defer os.Remove(guard)
			return operation()
		}
		// Windows can transiently report ERROR_ACCESS_DENIED while another
		// goroutine removes the just-released guard directory. Treat it as
		// lock contention; the bounded deadline still prevents an infinite wait.
		if !os.IsExist(err) && !os.IsPermission(err) {
			return err
		}
		if info, statErr := os.Stat(guard); statErr == nil && time.Since(info.ModTime()) > 30*time.Second {
			_ = os.Remove(guard)
			continue
		}
		if !time.Now().Before(deadline) {
			return errors.New("timed out acquiring llm_app lease guard")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (m *LLMAppManager) acquireRunLease(run *llmAppManagedRun, duration time.Duration) (bool, error) {
	if run == nil {
		return false, errors.New("llm_app run is unavailable")
	}
	record := run.snapshot()
	token, err := newLLMAppOpaqueID("lease_", 16)
	if err != nil {
		return false, err
	}
	now := time.Now()
	lease := llmAppLease{
		RunID: record.RunID, WorkerID: m.workerID, Token: token,
		AcquiredAt: now, ExpiresAt: now.Add(duration),
	}
	acquired := false
	err = m.withLeaseGuard(record.RunID, func() error {
		var current llmAppLease
		readErr := readLLMAppJSON(m.leasePath(record.RunID), &current)
		if readErr == nil && current.Token != "" && current.ExpiresAt.After(now) {
			return nil
		}
		if readErr != nil && !os.IsNotExist(readErr) {
			return readErr
		}
		if err := writeLLMAppJSON(m.leasePath(record.RunID), lease); err != nil {
			return err
		}
		acquired = true
		return nil
	})
	if err != nil || !acquired {
		return acquired, err
	}
	run.mu.Lock()
	run.leaseToken = token
	run.record.WorkerID = m.workerID
	run.record.LeaseExpiresAt = &lease.ExpiresAt
	run.mu.Unlock()
	if err := m.persistRunRecordUnfenced(run); err != nil {
		_ = m.releaseRunLease(run)
		return false, err
	}
	return true, nil
}

func (m *LLMAppManager) renewRunLease(run *llmAppManagedRun, duration time.Duration) error {
	if run == nil {
		return ErrLLMAppLeaseLost
	}
	run.mu.Lock()
	runID := run.record.RunID
	token := run.leaseToken
	run.mu.Unlock()
	if strings.TrimSpace(token) == "" {
		return ErrLLMAppLeaseLost
	}
	var renewed llmAppLease
	err := m.withLeaseGuard(runID, func() error {
		var current llmAppLease
		if err := readLLMAppJSON(m.leasePath(runID), &current); err != nil {
			return fmt.Errorf("%w: %v", ErrLLMAppLeaseLost, err)
		}
		if current.Token != token || current.WorkerID != m.workerID {
			return ErrLLMAppLeaseLost
		}
		current.ExpiresAt = time.Now().Add(duration)
		if err := writeLLMAppJSON(m.leasePath(runID), current); err != nil {
			return err
		}
		renewed = current
		return nil
	})
	if err != nil {
		return err
	}
	run.mu.Lock()
	run.record.LeaseExpiresAt = &renewed.ExpiresAt
	run.mu.Unlock()
	return nil
}

func (m *LLMAppManager) assertRunLease(run *llmAppManagedRun) error {
	if run == nil {
		return errors.New("llm_app run is unavailable")
	}
	run.mu.Lock()
	runID := run.record.RunID
	token := run.leaseToken
	run.mu.Unlock()
	return m.withRunLeaseToken(runID, token, nil)
}

// withRunLease keeps the fencing check and the protected write in the same
// lease-guard critical section. Checking first and writing later would leave a
// TOCTOU window where an expired worker could overwrite its successor.
func (m *LLMAppManager) withRunLease(run *llmAppManagedRun, operation func() error) error {
	if run == nil {
		return errors.New("llm_app run is unavailable")
	}
	run.mu.Lock()
	runID := run.record.RunID
	token := run.leaseToken
	run.mu.Unlock()
	return m.withRunLeaseToken(runID, token, operation)
}

func (m *LLMAppManager) withRunLeaseToken(runID, token string, operation func() error) error {
	if strings.TrimSpace(runID) == "" || strings.TrimSpace(token) == "" {
		return ErrLLMAppLeaseLost
	}
	return m.withLeaseGuard(runID, func() error {
		var current llmAppLease
		if err := readLLMAppJSON(m.leasePath(runID), &current); err != nil {
			return fmt.Errorf("%w: %v", ErrLLMAppLeaseLost, err)
		}
		if current.Token != token || current.WorkerID != m.workerID || !current.ExpiresAt.After(time.Now()) {
			return ErrLLMAppLeaseLost
		}
		if operation != nil {
			return operation()
		}
		return nil
	})
}

func (m *LLMAppManager) assertLocalRunControl(run *llmAppManagedRun) error {
	if run == nil {
		return errors.New("llm_app run is unavailable")
	}
	run.mu.Lock()
	terminal := isTerminalLLMAppStatus(run.record.Status)
	local := run.workerActive && run.leaseToken != ""
	run.mu.Unlock()
	if terminal || local {
		if terminal {
			return nil
		}
		return m.assertRunLease(run)
	}
	return fmt.Errorf("%w; retry against the owning MACS instance or wait for lease recovery", ErrLLMAppWorkerNotLocal)
}

func (m *LLMAppManager) releaseRunLease(run *llmAppManagedRun) error {
	if run == nil {
		return nil
	}
	run.mu.Lock()
	runID := run.record.RunID
	token := run.leaseToken
	run.mu.Unlock()
	if token == "" {
		return nil
	}
	run.persistMu.Lock()
	defer run.persistMu.Unlock()
	err := m.withLeaseGuard(runID, func() error {
		var current llmAppLease
		if err := readLLMAppJSON(m.leasePath(runID), &current); err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if current.Token != token {
			return ErrLLMAppLeaseLost
		}
		// Publish the local worker cleanup before removing the lease, while the
		// guard still prevents a successor from acquiring it. Writing after lease
		// removal could otherwise overwrite the successor's new owner record.
		run.mu.Lock()
		run.leaseToken = ""
		run.record.LeaseExpiresAt = nil
		run.workerActive = false
		run.workerStarting = false
		run.cancel = nil
		record := run.record
		run.mu.Unlock()
		if err := writeLLMAppJSON(filepath.Join(m.runDir(runID), "run.json"), record); err != nil {
			return err
		}
		if err := os.Remove(m.leasePath(runID)); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		// Clear only ephemeral ownership state. On a token mismatch the durable
		// record belongs to the successor and must not be rewritten by this run.
		run.mu.Lock()
		run.leaseToken = ""
		run.workerActive = false
		run.workerStarting = false
		run.cancel = nil
		run.mu.Unlock()
	}
	return err
}
