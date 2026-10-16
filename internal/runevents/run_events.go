package runevents

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"mac/internal/middleware"
	"mac/internal/permissionpolicy"
)

var globalOTLP *OTLPExporter

func SetGlobalExporter(exporter *OTLPExporter) {
	globalOTLP = exporter
}

// RunEventType identifies the kind of event.
type RunEventType string

const (
	EventRunStart       RunEventType = "run_start"
	EventRunEnd         RunEventType = "run_end"
	EventModelRequest   RunEventType = "model_request"
	EventModelResponse  RunEventType = "model_response"
	EventToolCallStart  RunEventType = "tool_call_start"
	EventToolCallResult RunEventType = "tool_call_result"
	EventToolCallError  RunEventType = "tool_call_error"
	EventMiddlewareHook RunEventType = "middleware_hook"
	EventUserMessage    RunEventType = "user_message"
	EventCompaction     RunEventType = "compaction"
)

// RunEvent is a single event in the run's lifecycle.
type RunEvent struct {
	Type      RunEventType           `json:"type"`
	RunID     string                 `json:"run_id"`
	ThreadID  string                 `json:"thread_id,omitempty"`
	AgentName string                 `json:"agent_name,omitempty"`
	Seq       int                    `json:"seq"`
	Timestamp int64                  `json:"timestamp"`
	Data      map[string]interface{} `json:"data,omitempty"`
	Duration  int64                  `json:"duration_ms,omitempty"`
}

// RunEventStore persists events per run to JSONL files.
// Each run gets its own file: storage/run_events/{YYYY-MM-DD}/{run_id}.jsonl
type RunEventStore struct {
	mu      sync.Mutex
	baseDir string
	writers map[string]*os.File // runID → open file handle
	seqs    map[string]int      // runID → current sequence number
}

func NewRunEventStore(baseDir string) *RunEventStore {
	dir := baseDir
	if dir == "" {
		dir = "storage/run_events"
	}
	os.MkdirAll(dir, 0755)
	return &RunEventStore{
		baseDir: dir,
		writers: make(map[string]*os.File),
		seqs:    make(map[string]int),
	}
}

// Emit writes an event to the run's JSONL file.
func (s *RunEventStore) Emit(event RunEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Auto-increment sequence
	s.seqs[event.RunID]++
	event.Seq = s.seqs[event.RunID]
	event.Timestamp = time.Now().UnixMilli()

	// Get or create the day directory
	dayDir := filepath.Join(s.baseDir, time.Now().Format("2006-01-02"))
	os.MkdirAll(dayDir, 0755)

	// Get or open the file handle
	writer, ok := s.writers[event.RunID]
	if !ok {
		path := filepath.Join(dayDir, event.RunID+".jsonl")
		f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err != nil {
			return fmt.Errorf("open run events file: %w", err)
		}
		writer = f
		s.writers[event.RunID] = writer
	}

	data, err := json.Marshal(event)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	_, err = writer.Write(data)

	// Export to OTLP collector if configured
	if globalOTLP != nil {
		go globalOTLP.Export([]RunEvent{event})
	}

	return err
}

// Close closes all open file handles (call on shutdown).
func (s *RunEventStore) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, f := range s.writers {
		f.Close()
		delete(s.writers, id)
	}
}

// CloseRun closes the file handle for a specific run.
func (s *RunEventStore) CloseRun(runID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if f, ok := s.writers[runID]; ok {
		f.Close()
		delete(s.writers, runID)
	}
}

// Query returns events for a run by reading its JSONL file.
func (s *RunEventStore) Query(runID string) ([]RunEvent, error) {
	// Search in recent day directories
	entries, err := os.ReadDir(s.baseDir)
	if err != nil {
		return nil, err
	}

	for i := len(entries) - 1; i >= 0; i-- {
		e := entries[i]
		if !e.IsDir() {
			continue
		}
		path := filepath.Join(s.baseDir, e.Name(), runID+".jsonl")
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		return parseJSONLEvents(data)
	}

	return nil, fmt.Errorf("run %s not found", runID)
}

func parseJSONLEvents(data []byte) ([]RunEvent, error) {
	var events []RunEvent
	lines := splitJSONLines(string(data))
	for _, line := range lines {
		if line == "" {
			continue
		}
		var event RunEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			continue // skip corrupted lines
		}
		events = append(events, event)
	}
	return events, nil
}

func splitJSONLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}

// RunEventsMiddleware emits tool lifecycle events to the RunEventStore.
type RunEventsMiddleware struct {
	middleware.Base
	store *RunEventStore
}

func NewRunEventsMiddleware(store *RunEventStore) *RunEventsMiddleware {
	if store == nil {
		return nil
	}
	return &RunEventsMiddleware{
		Base:  middleware.NewBase("run_events"),
		store: store,
	}
}

func (m *RunEventsMiddleware) BeforeTool(ctx *middleware.Context) error {
	if m == nil || m.store == nil {
		return nil
	}

	return m.store.Emit(RunEvent{
		Type:      EventToolCallStart,
		RunID:     ctx.TaskID,
		AgentName: ctx.AgentName,
		Data: map[string]interface{}{
			"tool_name": ctx.ToolCall.Name,
			"args":      ctx.ToolCall.Arguments,
		},
	})
}

func (m *RunEventsMiddleware) AfterTool(ctx *middleware.Context) error {
	if m == nil || m.store == nil {
		return nil
	}

	eventType := EventToolCallResult
	if ctx.ToolErr != nil {
		eventType = EventToolCallError
	}

	data := map[string]interface{}{
		"tool_name": ctx.ToolCall.Name,
	}
	if ctx.ToolResult != nil {
		// Truncate content for event storage (max 2000 chars)
		content := ctx.ToolResult.Content
		if len(content) > 2000 {
			content = content[:2000] + "..."
		}
		data["content"] = content
	}
	if ctx.ToolErr != nil {
		data["error"] = ctx.ToolErr.Error()
	}

	return m.store.Emit(RunEvent{
		Type:      eventType,
		RunID:     ctx.TaskID,
		AgentName: ctx.AgentName,
		Data:      data,
	})
}

// ====================== OTLP Exporter ======================

// OTLPExporter sends run events to an OpenTelemetry collector via HTTP.
type OTLPExporter struct {
	endpoint   string
	client     *http.Client
	sourceName string
}

func NewOTLPExporter(endpoint, sourceName string) *OTLPExporter {
	if endpoint == "" {
		return nil
	}
	return &OTLPExporter{
		endpoint:   endpoint,
		client:     &http.Client{Timeout: 5 * time.Second},
		sourceName: sourceName,
	}
}

// Export sends a batch of events as OTLP traces.
func (e *OTLPExporter) Export(events []RunEvent) error {
	if e == nil || len(events) == 0 {
		return nil
	}

	spans := make([]map[string]interface{}, 0, len(events))
	for _, ev := range events {
		span := map[string]interface{}{
			"traceId":           fmt.Sprintf("%032x", permissionpolicy.HashString(ev.RunID)),
			"spanId":            fmt.Sprintf("%016x", permissionpolicy.HashString(ev.RunID+fmt.Sprintf("%d", ev.Seq))),
			"name":              string(ev.Type),
			"startTimeUnixNano": fmt.Sprintf("%d", ev.Timestamp*1000000),
			"endTimeUnixNano":   fmt.Sprintf("%d", (ev.Timestamp+ev.Duration)*1000000),
			"attributes": []map[string]interface{}{
				{"key": "run_id", "value": map[string]interface{}{"stringValue": ev.RunID}},
				{"key": "agent", "value": map[string]interface{}{"stringValue": ev.AgentName}},
				{"key": "seq", "value": map[string]interface{}{"intValue": fmt.Sprintf("%d", ev.Seq)}},
			},
		}
		if ev.Data != nil {
			for k, v := range ev.Data {
				span["attributes"] = append(span["attributes"].([]map[string]interface{}), map[string]interface{}{
					"key": k, "value": map[string]interface{}{"stringValue": fmt.Sprintf("%v", v)},
				})
			}
		}
		spans = append(spans, span)
	}

	payload := map[string]interface{}{
		"resourceSpans": []map[string]interface{}{{
			"resource": map[string]interface{}{
				"attributes": []map[string]interface{}{
					{"key": "service.name", "value": map[string]interface{}{"stringValue": e.sourceName}},
				},
			},
			"scopeSpans": []map[string]interface{}{{
				"spans": spans,
			}},
		}},
	}

	body, _ := json.Marshal(payload)
	req, _ := http.NewRequest("POST", e.endpoint+"/v1/traces", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := e.client.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}
