package operations

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// ====================== Telemetry System ======================
// Structured, low-overhead event emission for observability.
// Events are buffered and written asynchronously to JSONL + optional OTLP.

// TelemetryEventType categorizes emitted events.
type TelemetryEventType string

const (
	TelLLMCall        TelemetryEventType = "llm.call"
	TelLLMError       TelemetryEventType = "llm.error"
	TelToolCall       TelemetryEventType = "tool.call"
	TelToolError      TelemetryEventType = "tool.error"
	TelSessionStart   TelemetryEventType = "session.start"
	TelSessionEnd     TelemetryEventType = "session.end"
	TelCompaction     TelemetryEventType = "compaction"
	TelCacheHit       TelemetryEventType = "cache.hit"
	TelCacheMiss      TelemetryEventType = "cache.miss"
	TelSubAgentSpawn  TelemetryEventType = "sub_agent.spawn"
	TelSubAgentResult TelemetryEventType = "sub_agent.result"
	TelHookFire       TelemetryEventType = "hook.fire"
	TelRateLimit      TelemetryEventType = "rate_limit"
	TelSystem         TelemetryEventType = "system"
)

// TelemetryEvent is a single structured event.
type TelemetryEvent struct {
	Type      TelemetryEventType `json:"type"`
	Timestamp int64              `json:"ts"`
	SessionID string             `json:"session,omitempty"`
	AgentName string             `json:"agent,omitempty"`
	TaskID    string             `json:"task,omitempty"`

	// LLM
	Model     string `json:"model,omitempty"`
	TokensIn  int    `json:"tokens_in,omitempty"`
	TokensOut int    `json:"tokens_out,omitempty"`
	LatencyMs int64  `json:"latency_ms,omitempty"`
	CacheHit  bool   `json:"cache_hit,omitempty"`

	// Tool
	ToolName    string `json:"tool,omitempty"`
	ToolSuccess bool   `json:"tool_success,omitempty"`
	ToolError   string `json:"tool_error,omitempty"`

	// General
	Message string                 `json:"msg,omitempty"`
	Extra   map[string]interface{} `json:"extra,omitempty"`
}

// TelemetryEmitter buffers and writes events asynchronously.
type TelemetryEmitter struct {
	mu      sync.Mutex
	ch      chan *TelemetryEvent
	file    *os.File
	logger  *log.Logger
	otlpURL string
	stopCh  chan struct{}
	wg      sync.WaitGroup
}

// NewTelemetryEmitter creates a new emitter writing to workspace/.openkitty/telemetry/.
func NewTelemetryEmitter(workspace, otlpURL string, logger *log.Logger) *TelemetryEmitter {
	dir := filepath.Join(workspace, ".openkitty", "telemetry")
	os.MkdirAll(dir, 0755)

	filename := filepath.Join(dir, fmt.Sprintf("events_%s.jsonl", time.Now().Format("2006-01-02")))
	f, err := os.OpenFile(filename, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		logger.Printf("[Telemetry] Failed to open file: %v", err)
	}

	t := &TelemetryEmitter{
		ch:      make(chan *TelemetryEvent, 4096),
		file:    f,
		logger:  logger,
		otlpURL: otlpURL,
		stopCh:  make(chan struct{}),
	}
	t.wg.Add(1)
	go t.drain()
	return t
}

func (t *TelemetryEmitter) drain() {
	defer t.wg.Done()
	for {
		select {
		case evt := <-t.ch:
			t.writeEvent(evt)
		case <-t.stopCh:
			// Drain remaining
			for {
				select {
				case evt := <-t.ch:
					t.writeEvent(evt)
				default:
					return
				}
			}
		}
	}
}

func (t *TelemetryEmitter) writeEvent(evt *TelemetryEvent) {
	if t.file != nil {
		data, _ := json.Marshal(evt)
		t.file.Write(append(data, '\n'))
	}
	// OTLP export (fire-and-forget, best effort)
	if t.otlpURL != "" {
		go t.exportOTLP(evt)
	}
}

func (t *TelemetryEmitter) exportOTLP(evt *TelemetryEvent) {
	// Minimal OTLP JSON export — best-effort POST
	data, _ := json.Marshal(map[string]interface{}{
		"resourceLogs": []map[string]interface{}{
			{
				"resource": map[string]interface{}{
					"attributes": []map[string]interface{}{
						{"key": "service.name", "value": map[string]interface{}{"stringValue": "openkitty"}},
					},
				},
				"scopeLogs": []map[string]interface{}{
					{
						"scope": map[string]interface{}{"name": string(evt.Type)},
						"logRecords": []map[string]interface{}{
							{
								"timeUnixNano": fmt.Sprintf("%d", evt.Timestamp*1e6),
								"severityText": "INFO",
								"body":         map[string]interface{}{"stringValue": evt.Message},
								"attributes":   eventToAttributes(evt),
							},
						},
					},
				},
			},
		},
	})
	// Best-effort: we don't want telemetry failure to affect the main path
	httpClient := &http.Client{Timeout: 5 * time.Second}
	req, _ := http.NewRequest("POST", t.otlpURL+"/v1/logs", bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")
	resp, _ := httpClient.Do(req)
	if resp != nil {
		resp.Body.Close()
	}
}

func eventToAttributes(evt *TelemetryEvent) []map[string]interface{} {
	var attrs []map[string]interface{}
	add := func(k, v string) {
		attrs = append(attrs, map[string]interface{}{"key": k, "value": map[string]interface{}{"stringValue": v}})
	}
	if evt.SessionID != "" {
		add("session_id", evt.SessionID)
	}
	if evt.AgentName != "" {
		add("agent_name", evt.AgentName)
	}
	if evt.TaskID != "" {
		add("task_id", evt.TaskID)
	}
	if evt.Model != "" {
		add("model", evt.Model)
	}
	if evt.ToolName != "" {
		add("tool_name", evt.ToolName)
	}
	return attrs
}

// Emit sends an event asynchronously. Never blocks — drops on full buffer.
func (t *TelemetryEmitter) Emit(evt *TelemetryEvent) {
	if t == nil {
		return
	}
	evt.Timestamp = time.Now().UnixMilli()
	select {
	case t.ch <- evt:
	default:
		t.logger.Printf("[Telemetry] Buffer full, dropping event: %s", evt.Type)
	}
}

// Convenience emitters

func (t *TelemetryEmitter) EmitLLMCall(sessionID, agentName, model string, tokensIn, tokensOut int, latencyMs int64, cacheHit bool) {
	t.Emit(&TelemetryEvent{
		Type: TelLLMCall, SessionID: sessionID, AgentName: agentName, Model: model,
		TokensIn: tokensIn, TokensOut: tokensOut, LatencyMs: latencyMs, CacheHit: cacheHit,
	})
}

func (t *TelemetryEmitter) EmitToolCall(sessionID, agentName, toolName string, success bool, latencyMs int64) {
	t.Emit(&TelemetryEvent{
		Type: TelToolCall, SessionID: sessionID, AgentName: agentName, ToolName: toolName,
		ToolSuccess: success, LatencyMs: latencyMs,
	})
}

func (t *TelemetryEmitter) EmitToolError(sessionID, agentName, toolName, errMsg string) {
	t.Emit(&TelemetryEvent{
		Type: TelToolError, SessionID: sessionID, AgentName: agentName, ToolName: toolName,
		ToolError: errMsg,
	})
}

func (t *TelemetryEmitter) EmitSessionStart(sessionID, agentName string) {
	t.Emit(&TelemetryEvent{Type: TelSessionStart, SessionID: sessionID, AgentName: agentName})
}

func (t *TelemetryEmitter) EmitSessionEnd(sessionID, agentName string) {
	t.Emit(&TelemetryEvent{Type: TelSessionEnd, SessionID: sessionID, AgentName: agentName})
}

func (t *TelemetryEmitter) EmitCompaction(sessionID, agentName string, preTokens, postTokens int) {
	t.Emit(&TelemetryEvent{
		Type: TelCompaction, SessionID: sessionID, AgentName: agentName,
		TokensIn: preTokens, TokensOut: postTokens,
		Message: fmt.Sprintf("compaction: %d → %d tokens", preTokens, postTokens),
	})
}

func (t *TelemetryEmitter) EmitSubAgentSpawn(sessionID, parentName, subName, task string) {
	t.Emit(&TelemetryEvent{
		Type: TelSubAgentSpawn, SessionID: sessionID, AgentName: parentName,
		ToolName: subName, Message: task,
	})
}

func (t *TelemetryEmitter) EmitSubAgentResult(sessionID, parentName, subName string, success bool, latencyMs int64) {
	t.Emit(&TelemetryEvent{
		Type: TelSubAgentResult, SessionID: sessionID, AgentName: parentName,
		ToolName: subName, ToolSuccess: success, LatencyMs: latencyMs,
	})
}

func (t *TelemetryEmitter) Shutdown() {
	if t == nil {
		return
	}
	close(t.stopCh)
	t.wg.Wait()
	if t.file != nil {
		t.file.Close()
	}
}

// ====================== Global Telemetry Instance ======================

var globalTelemetry *TelemetryEmitter

func InitGlobalTelemetry(workspace, otlpURL string, logger *log.Logger) {
	globalTelemetry = NewTelemetryEmitter(workspace, otlpURL, logger)
}

func GetTelemetry() *TelemetryEmitter { return globalTelemetry }
func ShutdownTelemetry() {
	if globalTelemetry != nil {
		globalTelemetry.Shutdown()
	}
}
