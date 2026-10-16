package toolcontract

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"
)

type ToolCall = Call
type ToolResult = Result

// ExecutionRegistry is the narrow registry surface required by the batch
// orchestrator.
type ExecutionRegistry interface {
	ToolLookup
	Execute(context.Context, Call) (Result, error)
}

// ====================== Tool Orchestration — Concurrency Partitioning ======================
// Claude Code 借鉴: partitionToolCalls in services/tools/toolOrchestration.ts
//
// Partition tool calls into batches based on concurrency safety:
//   - Consecutive read-only tools run CONCURRENTLY (up to MaxConcurrentTools goroutines)
//   - Non-overlapping path-scoped tools may run CONCURRENTLY
//   - Unsafe, stateful, or overlapping tools run SERIALLY
//
// Uses registered ReadOnlyTool/PathScopedTool metadata plus static fallbacks.

const MaxConcurrentTools = 10

// ToolBatch represents a group of tools that can be executed together.
type ToolBatch struct {
	Blocks     []ToolCall
	IsReadOnly bool
	Concurrent bool
}

// PartitionToolCalls splits tool calls into execution batches.
// Concurrent-safe tools form batches when all calls in the batch can run together.
// Unsafe, stateful, or overlapping path-scoped tools form serial batches.
func PartitionToolCalls(registry ToolLookup, calls []ToolCall, readOnlyByName ReadOnlyNameClassifier) []ToolBatch {
	if len(calls) == 0 {
		return nil
	}

	var batches []ToolBatch
	var currentConcurrent []ToolCall
	flushConcurrent := func() {
		if len(currentConcurrent) > 0 {
			blocks := copyToolCalls(currentConcurrent)
			batches = append(batches, ToolBatch{
				Blocks:     blocks,
				IsReadOnly: allToolsReadOnly(registry, blocks, readOnlyByName),
				Concurrent: true,
			})
			currentConcurrent = currentConcurrent[:0]
		}
	}

	for _, call := range calls {
		if !canRunInConcurrentBatch(registry, call, readOnlyByName) {
			flushConcurrent()
			batches = append(batches, ToolBatch{Blocks: []ToolCall{call}})
			continue
		}

		if canAppendToConcurrentBatch(registry, currentConcurrent, call, readOnlyByName) {
			currentConcurrent = append(currentConcurrent, call)
		} else {
			flushConcurrent()
			currentConcurrent = append(currentConcurrent, call)
		}
	}
	flushConcurrent()

	return batches
}

// RunToolBatch executes a batch of tools — concurrently or serially depending on IsReadOnly.
func RunToolBatch(ctx context.Context, registry ExecutionRegistry, batch ToolBatch) ([]ToolResult, error) {
	if len(batch.Blocks) == 0 {
		return nil, nil
	}
	if registry == nil {
		return nil, fmt.Errorf("tool registry is nil")
	}
	if batch.Concurrent || batch.IsReadOnly {
		return runToolsConcurrently(ctx, registry, batch.Blocks)
	}
	return runToolsSerially(ctx, registry, batch.Blocks)
}

func runToolsConcurrently(ctx context.Context, registry ExecutionRegistry, calls []ToolCall) ([]ToolResult, error) {
	if len(calls) == 0 {
		return nil, nil
	}

	sem := make(chan struct{}, MaxConcurrentTools)
	type indexedToolResult struct {
		index  int
		result ToolResult
		err    error
	}
	resultCh := make(chan indexedToolResult, len(calls))
	results := make([]ToolResult, len(calls))
	completed := make([]bool, len(calls))
	var abortErr error

	for i, call := range calls {
		go func(idx int, tc ToolCall) {
			outcome := indexedToolResult{index: idx}
			defer func() {
				if recovered := recover(); recovered != nil {
					outcome.result = ToolResult{ToolCallID: tc.ID, Name: tc.Name, Content: fmt.Sprintf("tool panic: %v", recovered)}
					outcome.err = fmt.Errorf("tool %q panicked: %v", tc.Name, recovered)
				}
				resultCh <- outcome
			}()
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				outcome.result = ToolResult{ToolCallID: tc.ID, Name: tc.Name, Content: ctx.Err().Error()}
				outcome.err = fmt.Errorf("tool %q cancelled before execution: %w", tc.Name, ctx.Err())
				return
			}
			defer func() { <-sem }()

			t0 := time.Now()
			result, err := executeToolCallSafely(ctx, registry, tc)
			outcome.result = result
			outcome.err = toolExecutionFailure(tc.Name, result, err)
			if d := time.Since(t0); d > 2*time.Second {
				log.Printf("[ToolOrch] SLOW concurrent tool %s: %v", tc.Name, d)
			}
		}(i, call)
	}

	collected := 0
	for collected < len(calls) {
		select {
		case outcome := <-resultCh:
			results[outcome.index] = outcome.result
			completed[outcome.index] = true
			collected++
			if abortErr == nil && outcome.err != nil {
				abortErr = outcome.err
			}
		case <-ctx.Done():
			partial := make([]ToolResult, 0, collected)
			for idx, done := range completed {
				if done {
					partial = append(partial, results[idx])
				}
			}
			return partial, fmt.Errorf("concurrent tool batch cancelled after %d/%d results: %w", collected, len(calls), ctx.Err())
		}
	}

	if abortErr != nil {
		return results, abortErr
	}
	return results, nil
}

func runToolsSerially(ctx context.Context, registry ExecutionRegistry, calls []ToolCall) ([]ToolResult, error) {
	var results []ToolResult
	for _, call := range calls {
		if err := ctx.Err(); err != nil {
			return results, fmt.Errorf("serial tool batch cancelled before %q: %w", call.Name, err)
		}
		t0 := time.Now()
		result, err := executeToolCallSafely(ctx, registry, call)
		if err != nil {
			if result != (ToolResult{}) {
				results = append(results, result)
			}
			return results, fmt.Errorf("serial tool %q: %w", call.Name, err)
		}
		if failureErr := toolExecutionFailure(call.Name, result, nil); failureErr != nil {
			results = append(results, result)
			return results, failureErr
		}
		if d := time.Since(t0); d > 5*time.Second {
			log.Printf("[ToolOrch] SLOW serial tool %s: %v", call.Name, d)
		}
		results = append(results, result)
	}
	return results, nil
}

// ExecuteAllTools runs all tool batches in order, returning merged results.
// Concurrent batches execute in parallel, serial batches one at a time.
func ExecuteAllTools(ctx context.Context, registry ExecutionRegistry, calls []ToolCall, readOnlyByName ReadOnlyNameClassifier) ([]ToolResult, error) {
	if len(calls) == 0 {
		return nil, nil
	}
	if registry == nil {
		return nil, fmt.Errorf("tool registry is nil")
	}
	if len(calls) == 1 {
		result, err := executeToolCallSafely(ctx, registry, calls[0])
		if failureErr := toolExecutionFailure(calls[0].Name, result, err); failureErr != nil {
			if result == (ToolResult{}) {
				return nil, failureErr
			}
			return []ToolResult{result}, failureErr
		}
		return []ToolResult{result}, nil
	}

	batches := PartitionToolCalls(registry, calls, readOnlyByName)
	var allResults []ToolResult
	var firstErr error

	for _, batch := range batches {
		results, err := RunToolBatch(ctx, registry, batch)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			log.Printf("[ToolOrch] Batch execution error: %v (collected %d of %d results)",
				err, len(allResults)+len(results), len(calls))
		}
		allResults = append(allResults, results...)
	}

	return allResults, firstErr
}

// BatchSummary returns a human-readable summary of how tools will be executed.
func BatchSummary(calls []ToolCall, readOnlyByName ReadOnlyNameClassifier) string {
	if len(calls) <= 1 {
		return "single tool call"
	}
	batches := PartitionToolCalls(nil, calls, readOnlyByName)
	var parts []string
	for i, b := range batches {
		names := make([]string, len(b.Blocks))
		for j, c := range b.Blocks {
			names[j] = c.Name
		}
		sort.Strings(names)
		mode := "serial"
		if b.Concurrent || b.IsReadOnly {
			mode = "concurrent"
		}
		parts = append(parts, fmt.Sprintf("batch-%d[%s]: %v", i+1, mode, names))
	}
	return fmt.Sprintf("%d tools in %d batches: %v", len(calls), len(batches), parts)
}

func copyToolCalls(src []ToolCall) []ToolCall {
	dst := make([]ToolCall, len(src))
	copy(dst, src)
	return dst
}

func canRunInConcurrentBatch(registry ToolLookup, call ToolCall, readOnlyByName ReadOnlyNameClassifier) bool {
	if registry == nil {
		return readOnlyByName != nil && readOnlyByName(call.Name)
	}
	class := ClassifyToolParallel(call.Name, call.Arguments, registry, readOnlyByName)
	return class < MustSerial
}

func canAppendToConcurrentBatch(registry ToolLookup, batch []ToolCall, call ToolCall, readOnlyByName ReadOnlyNameClassifier) bool {
	if len(batch) == 0 {
		return true
	}
	for _, existing := range batch {
		if registry == nil {
			if readOnlyByName == nil || !readOnlyByName(existing.Name) || !readOnlyByName(call.Name) {
				return false
			}
			continue
		}
		if !CanRunConcurrently(existing, call, registry, readOnlyByName) {
			return false
		}
	}
	return true
}

func allToolsReadOnly(registry ToolLookup, calls []ToolCall, readOnlyByName ReadOnlyNameClassifier) bool {
	for _, call := range calls {
		if registry == nil {
			if readOnlyByName == nil || !readOnlyByName(call.Name) {
				return false
			}
			continue
		}
		if !isToolReadOnly(registry, call.Name, readOnlyByName) {
			return false
		}
	}
	return true
}

func isToolReadOnly(registry ToolLookup, name string, readOnlyByName ReadOnlyNameClassifier) bool {
	if registry != nil {
		if tool, ok := registry.Get(name); ok {
			if readOnly, ok := tool.(ReadOnlyTool); ok {
				return readOnly.IsReadOnly()
			}
		}
	}
	return readOnlyByName != nil && readOnlyByName(name)
}

func toolExecutionFailure(toolName string, result ToolResult, err error) error {
	if err != nil {
		return fmt.Errorf("tool %q failed: %w", toolName, err)
	}
	result.Name = toolName
	if ToolResultExecutionSucceeded(result, nil) {
		return nil
	}
	return fmt.Errorf("tool %q failed: %s", toolName, CompactToolFailure(result.Content))
}

func executeToolCallSafely(ctx context.Context, registry ExecutionRegistry, call ToolCall) (result ToolResult, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			result = ToolResult{
				ToolCallID: call.ID,
				Name:       call.Name,
				Content:    fmt.Sprintf("tool panic: %v", recovered),
			}
			err = fmt.Errorf("tool %q panicked: %v", call.Name, recovered)
		}
	}()
	return registry.Execute(ctx, call)
}

const ToolFailureSummaryLimit = 800

func CompactToolFailure(content string) string {
	content = strings.TrimSpace(strings.ReplaceAll(content, "\n", " "))
	if content == "" {
		return "empty failure result"
	}
	runes := []rune(content)
	if len(runes) > ToolFailureSummaryLimit {
		return string(runes[:ToolFailureSummaryLimit]) + "..."
	}
	return content
}
