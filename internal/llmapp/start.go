package llmapp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const llmAppMaximumIdempotencyKeyLength = 256

type llmAppStartOptions struct {
	IdempotencyKey string
	Lifecycle      string
}

type llmAppStartReceipt struct {
	KeyDigest     string     `json:"key_digest"`
	PayloadDigest string     `json:"payload_digest"`
	RunID         string     `json:"run_id"`
	Status        string     `json:"status"`
	CreatedAt     time.Time  `json:"created_at"`
	ReadyAt       *time.Time `json:"ready_at,omitempty"`
}

func decodeLLMAppStartOptions(params map[string]interface{}) (llmAppStartOptions, error) {
	options := llmAppStartOptions{
		IdempotencyKey: strings.TrimSpace(llmAppStringParam(params, "idempotency_key")),
		Lifecycle:      strings.ToLower(strings.TrimSpace(llmAppStringParam(params, "lifecycle"))),
	}
	if len(options.IdempotencyKey) > llmAppMaximumIdempotencyKeyLength {
		return llmAppStartOptions{}, fmt.Errorf("idempotency_key exceeds %d characters", llmAppMaximumIdempotencyKeyLength)
	}
	if options.Lifecycle == "" {
		// Preserve the historical background-run behavior. Callers that want
		// strict parent/child cancellation opt into attached explicitly.
		options.Lifecycle = llmAppLifecycleDetached
	}
	switch options.Lifecycle {
	case llmAppLifecycleAttached, llmAppLifecycleDetached:
	default:
		return llmAppStartOptions{}, fmt.Errorf("unsupported lifecycle %q", options.Lifecycle)
	}
	return options, nil
}

func (m *LLMAppManager) startRun(
	ctx context.Context,
	definition LLMAppDefinition,
	request llmAppRunRequest,
	runtime Runtime,
	options llmAppStartOptions,
) (LLMAppRunRecord, bool, error) {
	if runtime.ModelCaller == nil {
		return LLMAppRunRecord{}, false, errors.New("model caller is required")
	}
	runtime = normalizeRuntime(runtime)
	m.configureRuntime(runtime)
	m.startMu.Lock()
	defer m.startMu.Unlock()

	runID, err := newLLMAppRunID()
	if err != nil {
		return LLMAppRunRecord{}, false, err
	}
	idempotencyDigest := ""
	payloadDigest := ""
	receiptPath := ""
	if options.IdempotencyKey != "" {
		idempotencyDigest, payloadDigest, err = m.startDigests(
			ctx, options.IdempotencyKey, definition, request, options.Lifecycle, runtime.BindingID,
		)
		if err != nil {
			return LLMAppRunRecord{}, false, err
		}
		receiptPath = filepath.Join(m.root, "idempotency", idempotencyDigest+".json")
		receipt := llmAppStartReceipt{
			KeyDigest: idempotencyDigest, PayloadDigest: payloadDigest, RunID: runID,
			Status: "creating", CreatedAt: time.Now(),
		}
		claimed, existing, claimErr := writeExclusiveLLMAppJSON(receiptPath, receipt)
		if claimErr != nil {
			return LLMAppRunRecord{}, false, claimErr
		}
		if !claimed {
			if existing.PayloadDigest != payloadDigest {
				return LLMAppRunRecord{}, false, errors.New("idempotency_key was already used with a different llm_app start payload")
			}
			runID = existing.RunID
			for attempt := 0; attempt < 20; attempt++ {
				run, loadErr := m.getRun(runID)
				if loadErr == nil {
					return run.snapshot(), true, nil
				}
				if !strings.Contains(loadErr.Error(), "not found") {
					return LLMAppRunRecord{}, false, loadErr
				}
				if time.Since(existing.CreatedAt) >= 2*time.Second {
					break
				}
				time.Sleep(25 * time.Millisecond)
			}
			if time.Since(existing.CreatedAt) < 2*time.Second {
				return LLMAppRunRecord{}, false, errors.New("idempotent llm_app start is still being created; retry with the same key")
			}
		}
	}

	record, err := m.createRun(ctx, runID, idempotencyDigest, definition, request, runtime, options)
	if err != nil {
		return LLMAppRunRecord{}, false, err
	}
	if receiptPath != "" {
		readyAt := time.Now()
		_ = writeLLMAppJSON(receiptPath, llmAppStartReceipt{
			KeyDigest: idempotencyDigest, PayloadDigest: payloadDigest, RunID: record.RunID,
			Status: "ready", CreatedAt: record.CreatedAt, ReadyAt: &readyAt,
		})
	}
	return record, false, nil
}

func (m *LLMAppManager) startDigests(
	ctx context.Context,
	key string,
	definition LLMAppDefinition,
	request llmAppRunRequest,
	lifecycle string,
	executorBindingID string,
) (string, string, error) {
	identity, _ := m.resolveIdentity(ctx)
	scope := identity.TenantID + "\x00" + identity.SubjectID + "\x00" + key
	keySum := sha256.Sum256([]byte(scope))
	payload, err := json.Marshal(struct {
		AppName   string           `json:"app_name"`
		AppDigest string           `json:"app_digest"`
		Request   llmAppRunRequest `json:"request"`
		Lifecycle string           `json:"lifecycle"`
		Executor  string           `json:"executor_binding_id,omitempty"`
	}{definition.Name, definition.Digest, request, lifecycle, executorBindingID})
	if err != nil {
		return "", "", err
	}
	payloadSum := sha256.Sum256(payload)
	return hex.EncodeToString(keySum[:]), hex.EncodeToString(payloadSum[:]), nil
}

func writeExclusiveLLMAppJSON(path string, value llmAppStartReceipt) (bool, llmAppStartReceipt, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return false, llmAppStartReceipt{}, err
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return false, llmAppStartReceipt{}, err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if !os.IsExist(err) {
			return false, llmAppStartReceipt{}, err
		}
		var existing llmAppStartReceipt
		if readErr := readLLMAppJSON(path, &existing); readErr != nil {
			return false, llmAppStartReceipt{}, fmt.Errorf("read existing llm_app idempotency receipt: %w", readErr)
		}
		return false, existing, nil
	}
	written := false
	defer func() {
		_ = file.Close()
		if !written {
			_ = os.Remove(path)
		}
	}()
	if _, err := file.Write(append(data, '\n')); err != nil {
		return false, llmAppStartReceipt{}, err
	}
	if err := file.Sync(); err != nil {
		return false, llmAppStartReceipt{}, err
	}
	if err := file.Close(); err != nil {
		return false, llmAppStartReceipt{}, err
	}
	written = true
	return true, llmAppStartReceipt{}, nil
}

func newLLMAppExecutionContext(
	parent context.Context,
	lifecycle string,
	policy LLMAppBudgetPolicy,
	createdAt time.Time,
) (context.Context, context.CancelFunc) {
	if parent == nil {
		parent = context.Background()
	}
	if lifecycle == llmAppLifecycleDetached {
		parent = context.WithoutCancel(parent)
	}
	if policy.MaxDurationSeconds > 0 {
		return context.WithDeadline(parent, createdAt.Add(time.Duration(policy.MaxDurationSeconds)*time.Second))
	}
	return context.WithCancel(parent)
}
