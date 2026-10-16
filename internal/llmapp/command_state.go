package llmapp

import (
	"bufio"
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

var ErrLLMAppCommandOutcomeUnknown = errors.New("llm_app command outcome is unknown")

type llmAppCommandState struct {
	Command       LLMAppCommand         `json:"command"`
	PayloadDigest string                `json:"payload_digest"`
	Status        string                `json:"status"`
	Receipt       *LLMAppCommandReceipt `json:"receipt,omitempty"`
	Error         string                `json:"error,omitempty"`
	CreatedAt     time.Time             `json:"created_at"`
	UpdatedAt     time.Time             `json:"updated_at"`
}

func digestLLMAppCommand(command LLMAppCommand) (string, error) {
	data, err := json.Marshal(command)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func (m *LLMAppManager) commandStatePath(runID, commandID string) string {
	sum := sha256.Sum256([]byte(commandID))
	return filepath.Join(m.runDir(runID), "commands", hex.EncodeToString(sum[:])+".json")
}

func (m *LLMAppManager) readCommandState(runID, commandID string) (llmAppCommandState, error) {
	var state llmAppCommandState
	err := readLLMAppJSON(m.commandStatePath(runID, commandID), &state)
	return state, err
}

func (m *LLMAppManager) writeCommandState(run *llmAppManagedRun, state llmAppCommandState) error {
	if run == nil {
		return errors.New("llm_app run is unavailable")
	}
	state.UpdatedAt = time.Now()
	if state.CreatedAt.IsZero() {
		state.CreatedAt = state.UpdatedAt
	}
	runID := run.snapshot().RunID
	return m.writeRunJSON(run, m.commandStatePath(runID, state.Command.CommandID), state)
}

func (m *LLMAppManager) loadCommandReceipts(run *llmAppManagedRun) error {
	if run == nil {
		return nil
	}
	record := run.snapshot()
	receipts := make(map[string]LLMAppCommandReceipt)
	dir := filepath.Join(m.runDir(record.RunID), "commands")
	entries, err := os.ReadDir(dir)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(strings.ToLower(entry.Name()), ".json") {
			continue
		}
		var state llmAppCommandState
		if err := readLLMAppJSON(filepath.Join(dir, entry.Name()), &state); err != nil {
			return err
		}
		if state.Status == "applied" && state.Receipt != nil {
			receipts[state.Receipt.CommandID] = *state.Receipt
		}
	}
	if record.CommandPath != "" {
		file, openErr := os.Open(record.CommandPath)
		if openErr == nil {
			scanner := bufio.NewScanner(file)
			scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
			for scanner.Scan() {
				var receipt LLMAppCommandReceipt
				if json.Unmarshal(scanner.Bytes(), &receipt) == nil && receipt.CommandID != "" {
					if _, exists := receipts[receipt.CommandID]; !exists {
						receipts[receipt.CommandID] = receipt
					}
				}
			}
			scanErr := scanner.Err()
			_ = file.Close()
			if scanErr != nil {
				return fmt.Errorf("scan command receipts: %w", scanErr)
			}
		} else if !os.IsNotExist(openErr) {
			return openErr
		}
	}
	run.mu.Lock()
	run.commandReceipts = receipts
	run.mu.Unlock()
	return nil
}
