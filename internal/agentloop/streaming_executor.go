package agentloop

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"regexp"
	"strings"
	"sync"
	"time"
)

// ====================== Streaming Tool Executor ======================
// 借鉴 Claude Code StreamingToolExecutor: 在 SSE 流式响应中边收 token 边执行工具。
//
// 修复: 原实现所有工具无条件并发执行，写工具可能互相冲突。
// 新实现: 读工具并发(最多10路)，写工具串行(单信号量)，保证执行安全性。

const streamMaxConcurrent = 10 // max concurrent read tools

// PendingCall represents a tool call extracted from the stream.
type PendingCall struct {
	ID        string
	Name      string
	Arguments string // raw JSON
	Index     int
}

// ExecutedResult holds the result of a tool executed during streaming.
type ExecutedResult struct {
	CallID  string
	Name    string
	Content string
	Err     error
}

// StreamExecutor executes tool calls as they arrive from the SSE stream.
// Read tools run concurrently (up to streamMaxConcurrent), write tools run serially.
type StreamExecutor struct {
	mu sync.Mutex

	fullContent  string
	toolCalls    []PendingCall
	results      map[string]*ExecutedResult
	resultOrder  []string
	finished     bool
	finishReason string

	resultCh chan *ExecutedResult
	doneCh   chan struct{}
	ctx      context.Context
	cancel   context.CancelFunc

	isRead   func(string) bool
	executor func(ctx context.Context, name string, args map[string]interface{}) (string, error)
	logger   *log.Logger

	// Read/write partitioning semaphores
	readSem   chan struct{} // capacity = streamMaxConcurrent
	writeSem  chan struct{} // capacity = 1 (serial)
	pendingWg sync.WaitGroup
}

// NewStreamExecutor creates a new streaming executor.
func NewStreamExecutor(isRead func(string) bool, executor func(ctx context.Context, name string, args map[string]interface{}) (string, error)) *StreamExecutor {
	ctx, cancel := context.WithCancel(context.Background())
	return &StreamExecutor{
		results:  make(map[string]*ExecutedResult),
		isRead:   isRead,
		executor: executor,
		resultCh: make(chan *ExecutedResult, 20),
		doneCh:   make(chan struct{}),
		ctx:      ctx,
		cancel:   cancel,
		logger:   log.New(log.Writer(), "[StreamExec] ", log.LstdFlags),
		readSem:  make(chan struct{}, streamMaxConcurrent),
		writeSem: make(chan struct{}, 1),
	}
}

// ProcessStream reads SSE chunks from the response body.
// Content deltas are sent to onChunk. Tool calls are executed immediately with read/write partitioning.
func (se *StreamExecutor) ProcessStream(body io.Reader, onChunk func(delta string)) error {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var currentTool *PendingCall
	var contentBuilder strings.Builder

	for scanner.Scan() {
		line := scanner.Text()

		if line == "" || strings.HasPrefix(line, ":") {
			continue
		}
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			continue
		}

		var event map[string]interface{}
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			continue
		}

		choices, _ := event["choices"].([]interface{})
		if len(choices) == 0 {
			continue
		}
		choice := choices[0].(map[string]interface{})

		if fr, ok := choice["finish_reason"].(string); ok && fr != "" {
			se.mu.Lock()
			se.finishReason = fr
			se.mu.Unlock()
		}

		delta, hasDelta := choice["delta"].(map[string]interface{})
		if !hasDelta {
			continue
		}

		// Content delta
		if content, ok := delta["content"].(string); ok && content != "" {
			contentBuilder.WriteString(content)
			if onChunk != nil {
				onChunk(content)
			}
		}

		// Reasoning content
		if rc, ok := delta["reasoning_content"].(string); ok && rc != "" {
			if onChunk != nil {
				onChunk(rc)
			}
		}

		// Tool calls
		if tcDelta, ok := delta["tool_calls"].([]interface{}); ok {
			for _, tci := range tcDelta {
				tc, _ := tci.(map[string]interface{})
				if idx, ok := tc["index"].(float64); ok {
					blockIdx := int(idx)
					if id, ok := tc["id"].(string); ok && id != "" {
						if currentTool != nil && currentTool.ID != "" {
							se.enqueueCall(*currentTool)
						}
						currentTool = &PendingCall{ID: id, Index: blockIdx}
					}
					if currentTool != nil && currentTool.Index == blockIdx {
						if fn, ok := tc["function"].(map[string]interface{}); ok {
							if name, ok := fn["name"].(string); ok && name != "" {
								currentTool.Name = name
							}
							if args, ok := fn["arguments"].(string); ok {
								currentTool.Arguments += args
							}
						}
					}
				}
			}
		}
	}

	// Finalize last pending tool
	if currentTool != nil && currentTool.ID != "" {
		se.enqueueCall(*currentTool)
	}

	se.mu.Lock()
	se.fullContent = contentBuilder.String()
	se.finished = true
	se.mu.Unlock()

	close(se.doneCh)
	return scanner.Err()
}

func (se *StreamExecutor) enqueueCall(tc PendingCall) {
	se.mu.Lock()
	se.toolCalls = append(se.toolCalls, tc)
	se.mu.Unlock()

	se.pendingWg.Add(1)
	go se.dispatchCall(tc)
}

// dispatchCall routes a tool call to either concurrent (read) or serial (write) execution.
func (se *StreamExecutor) dispatchCall(tc PendingCall) {
	defer se.pendingWg.Done()

	isRead := se.isReadTool(tc.Name)

	if isRead {
		// Read tool: acquire read semaphore (up to streamMaxConcurrent)
		select {
		case se.readSem <- struct{}{}:
			defer func() { <-se.readSem }()
		case <-se.ctx.Done():
			return
		}
	} else {
		// Write tool: acquire write semaphore (capacity 1 = serial)
		select {
		case se.writeSem <- struct{}{}:
			defer func() { <-se.writeSem }()
		case <-se.ctx.Done():
			return
		}
	}

	se.executeCall(tc)
}

func (se *StreamExecutor) isReadTool(name string) bool {
	if se.isRead == nil {
		return false
	}
	return se.isRead(name)
}

func (se *StreamExecutor) executeCall(tc PendingCall) {
	var args map[string]interface{}
	if err := json.Unmarshal([]byte(tc.Arguments), &args); err != nil {
		cleaned := cleanJSONString(tc.Arguments)
		if err2 := json.Unmarshal([]byte(cleaned), &args); err2 != nil {
			se.storeResult(&ExecutedResult{
				CallID: tc.ID, Name: tc.Name,
				Content: fmt.Sprintf("参数解析失败: %v", err), Err: err,
			})
			return
		}
	}

	content, err := se.executor(se.ctx, tc.Name, args)
	se.storeResult(&ExecutedResult{
		CallID: tc.ID, Name: tc.Name,
		Content: content, Err: err,
	})
}

func (se *StreamExecutor) storeResult(r *ExecutedResult) {
	se.mu.Lock()
	defer se.mu.Unlock()
	if _, exists := se.results[r.CallID]; !exists {
		se.results[r.CallID] = r
		se.resultOrder = append(se.resultOrder, r.CallID)
	}
	select {
	case se.resultCh <- r:
	default:
	}
}

// WaitForAll waits for pending tool executions, up to timeout.
func (se *StreamExecutor) WaitForAll(timeout time.Duration) {
	done := make(chan struct{})
	go func() {
		se.pendingWg.Wait()
		close(done)
	}()
	deadline := time.After(timeout)
	for {
		se.mu.Lock()
		pending := len(se.toolCalls)
		finished := len(se.results)
		se.mu.Unlock()

		// WaitForAll is called after the caller has finished parsing the stream;
		// its contract is to wait for submitted executions, not for a separate
		// stream lifecycle flag. Requiring se.finished made the manual SSE parser
		// wait the full timeout even after every result had arrived (and for 0/0).
		if pending == finished {
			return
		}

		select {
		case <-se.resultCh:
			continue
		case <-done:
			// All goroutines completed, but double-check results
			time.Sleep(10 * time.Millisecond)
			continue
		case <-deadline:
			se.logger.Printf("[WARN] WaitForAll timeout after %v: %d/%d results collected",
				timeout, finished, pending)
			return
		case <-se.ctx.Done():
			return
		}
	}
}

// Results returns tool results in call order.
func (se *StreamExecutor) Results() []ExecutedResult {
	se.mu.Lock()
	defer se.mu.Unlock()

	out := make([]ExecutedResult, 0, len(se.results))
	seen := make(map[string]bool, len(se.results))
	for _, call := range se.toolCalls {
		if r, ok := se.results[call.ID]; ok {
			out = append(out, *r)
			seen[call.ID] = true
		}
	}

	// Include any stored result whose call was not retained in toolCalls.
	for _, callID := range se.resultOrder {
		if r, ok := se.results[callID]; ok && !seen[callID] {
			out = append(out, *r)
		}
	}
	return out
}

// Content returns accumulated text content.
func (se *StreamExecutor) Content() string {
	se.mu.Lock()
	defer se.mu.Unlock()
	return se.fullContent
}

// ToolCalls returns parsed tool calls.
func (se *StreamExecutor) ToolCalls() []PendingCall {
	se.mu.Lock()
	defer se.mu.Unlock()
	out := make([]PendingCall, len(se.toolCalls))
	copy(out, se.toolCalls)
	return out
}

// SubmitCall adds a tool call for immediate background execution.
func (se *StreamExecutor) SubmitCall(tc PendingCall) {
	se.mu.Lock()
	se.toolCalls = append(se.toolCalls, tc)
	se.mu.Unlock()

	se.pendingWg.Add(1)
	go se.dispatchCall(tc)
}

// Cancel stops pending executions.
func (se *StreamExecutor) Cancel() { se.cancel() }

func cleanJSONString(value string) string {
	value = strings.TrimSpace(value)
	if len(value) >= 2 && ((value[0] == '"' && value[len(value)-1] == '"') || (value[0] == '\'' && value[len(value)-1] == '\'')) {
		value = value[1 : len(value)-1]
	}
	var builder strings.Builder
	for _, r := range value {
		if r < 32 && r != '\n' && r != '\t' && r != '\r' {
			continue
		}
		builder.WriteRune(r)
	}
	value = builder.String()
	value = strings.ReplaceAll(value, `\'`, `'`)
	value = strings.ReplaceAll(value, `\"`, `"`)
	value = strings.ReplaceAll(value, `\\`, `\`)
	if !strings.HasPrefix(value, "{") {
		value = "{" + value
	}
	if !strings.HasSuffix(value, "}") {
		value += "}"
	}
	return regexp.MustCompile(`(\w+)\s*:`).ReplaceAllString(value, `"$1":`)
}
