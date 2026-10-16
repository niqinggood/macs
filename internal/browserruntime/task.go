package browserruntime

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"mac/internal/configvalue"
	"mac/internal/textutil"
)

type BrowserTaskStatus string

const (
	BrowserTaskRunning   BrowserTaskStatus = "running"
	BrowserTaskCompleted BrowserTaskStatus = "completed"
	BrowserTaskFailed    BrowserTaskStatus = "failed"
)

type BrowserTaskRecord struct {
	ID              string            `json:"id"`
	Objective       string            `json:"objective,omitempty"`
	URL             string            `json:"url,omitempty"`
	TargetID        string            `json:"target_id,omitempty"`
	Status          BrowserTaskStatus `json:"status"`
	StartedAt       time.Time         `json:"started_at"`
	UpdatedAt       time.Time         `json:"updated_at"`
	DurationMs      int64             `json:"duration_ms,omitempty"`
	Steps           []BrowserTaskStep `json:"steps"`
	ScreenshotPath  string            `json:"screenshot_path,omitempty"`
	ScreenshotURL   string            `json:"screenshot_url,omitempty"`
	ReplayPath      string            `json:"replay_path,omitempty"`
	ReplayURL       string            `json:"replay_url,omitempty"`
	ExtractExcerpt  string            `json:"extract_excerpt,omitempty"`
	Error           string            `json:"error,omitempty"`
	NextActions     []string          `json:"next_actions,omitempty"`
	EstimatedCredit int               `json:"estimated_credits,omitempty"`
	Markdown        string            `json:"markdown,omitempty"`
}

type BrowserTaskStep struct {
	Name       string `json:"name"`
	Status     string `json:"status"`
	DurationMs int64  `json:"duration_ms,omitempty"`
	Output     string `json:"output,omitempty"`
	Error      string `json:"error,omitempty"`
}

type BrowserTaskManager struct {
	mu    sync.RWMutex
	tasks map[string]*BrowserTaskRecord
	seq   int64
}

func NewBrowserTaskManager() *BrowserTaskManager {
	return &BrowserTaskManager{tasks: make(map[string]*BrowserTaskRecord)}
}

func (m *BrowserTaskManager) Start(objective, rawURL string) *BrowserTaskRecord {
	if m == nil {
		return nil
	}
	now := time.Now()
	m.mu.Lock()
	m.seq++
	seq := m.seq
	rec := &BrowserTaskRecord{
		ID:        fmt.Sprintf("browser_%s_%06d", now.Format("060102150405"), seq),
		Objective: strings.TrimSpace(objective),
		URL:       strings.TrimSpace(rawURL),
		Status:    BrowserTaskRunning,
		StartedAt: now,
		UpdatedAt: now,
	}
	m.tasks[rec.ID] = rec
	m.mu.Unlock()
	return rec
}

func (m *BrowserTaskManager) AddStep(taskID, name, status, output string, durationMs int64, err error) {
	if m == nil || taskID == "" {
		return
	}
	step := BrowserTaskStep{
		Name:       name,
		Status:     status,
		DurationMs: durationMs,
		Output:     textutil.Truncate(strings.TrimSpace(output), 1200),
	}
	if err != nil {
		step.Error = textutil.Truncate(err.Error(), 500)
	}
	m.mu.Lock()
	if rec := m.tasks[taskID]; rec != nil {
		rec.Steps = append(rec.Steps, step)
		rec.UpdatedAt = time.Now()
	}
	m.mu.Unlock()
}

func (m *BrowserTaskManager) Update(taskID string, update func(*BrowserTaskRecord)) {
	if m == nil || taskID == "" || update == nil {
		return
	}
	m.mu.Lock()
	if rec := m.tasks[taskID]; rec != nil {
		update(rec)
		rec.UpdatedAt = time.Now()
		rec.DurationMs = rec.UpdatedAt.Sub(rec.StartedAt).Milliseconds()
		rec.EstimatedCredit = estimateBrowserTaskCredits(rec)
		rec.NextActions = BrowserTaskNextActions(rec)
		rec.Markdown = FormatBrowserTaskMarkdown(rec)
	}
	m.mu.Unlock()
}

func (m *BrowserTaskManager) Get(taskID string) (*BrowserTaskRecord, bool) {
	if m == nil || taskID == "" {
		return nil, false
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	rec, ok := m.tasks[taskID]
	if !ok || rec == nil {
		return nil, false
	}
	return cloneBrowserTaskRecord(rec), true
}

func (m *BrowserTaskManager) List(limit int) []*BrowserTaskRecord {
	if m == nil {
		return nil
	}
	m.mu.RLock()
	out := make([]*BrowserTaskRecord, 0, len(m.tasks))
	for _, rec := range m.tasks {
		out = append(out, cloneBrowserTaskRecord(rec))
	}
	m.mu.RUnlock()
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].StartedAt.Equal(out[j].StartedAt) {
			return out[i].ID > out[j].ID
		}
		return out[i].StartedAt.After(out[j].StartedAt)
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

func cloneBrowserTaskRecord(rec *BrowserTaskRecord) *BrowserTaskRecord {
	if rec == nil {
		return nil
	}
	cp := *rec
	cp.Steps = append([]BrowserTaskStep(nil), rec.Steps...)
	cp.NextActions = append([]string(nil), rec.NextActions...)
	return &cp
}

type BrowserTaskTool struct {
	workspace  string
	manager    *BrowserTaskManager
	operations BrowserTaskOperations
}

type BrowserTaskExecutor func(context.Context, map[string]interface{}) (string, error)

type BrowserTaskOperations struct {
	Navigate       BrowserTaskExecutor
	Page           BrowserTaskExecutor
	Close          BrowserTaskExecutor
	StartRecording func(string)
	ExportReplay   func() string
}

func NewBrowserTaskTool(workspace string, manager *BrowserTaskManager, operations BrowserTaskOperations) *BrowserTaskTool {
	if manager == nil {
		manager = NewBrowserTaskManager()
	}
	return &BrowserTaskTool{workspace: workspace, manager: manager, operations: operations}
}

func (t *BrowserTaskTool) Name() string { return "browser_task" }

func (t *BrowserTaskTool) Description() string {
	return "Create an auditable browser operation package. Opens a page, extracts interactive elements, captures a screenshot, exports replay script, and returns task status/links. Use before deeper web_page actions when a user asks the agent to operate a website."
}

func (t *BrowserTaskTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"url": map[string]interface{}{
				"type":        "string",
				"description": "URL to open in the browser",
			},
			"objective": map[string]interface{}{
				"type":        "string",
				"description": "User-facing browser task objective",
			},
			"timeout": map[string]interface{}{
				"type":        "integer",
				"description": "Navigation timeout seconds, default 20",
			},
			"close_tab": map[string]interface{}{
				"type":        "boolean",
				"description": "Close the created tab after inspection. Default false so the user/agent can continue from the target_id.",
			},
		},
		"required": []string{"url"},
	}
}

func (t *BrowserTaskTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	rawURL, _ := params["url"].(string)
	if strings.TrimSpace(rawURL) == "" {
		return "", fmt.Errorf("url is required")
	}
	objective, _ := params["objective"].(string)
	timeout := configvalue.Int(params, "timeout", 20)
	if timeout <= 0 {
		timeout = 20
	}
	closeTab, _ := params["close_tab"].(bool)

	rec := t.manager.Start(objective, rawURL)
	if t.operations.Navigate == nil || t.operations.Page == nil {
		return "", fmt.Errorf("browser task operations are not configured")
	}
	if t.operations.StartRecording != nil {
		t.operations.StartRecording(rec.ID)
	}
	taskDir := filepath.Join(t.workspaceOrDot(), ".openkitty", "storage", "browser_tasks", rec.ID)
	if err := os.MkdirAll(taskDir, 0755); err != nil {
		return "", err
	}

	targetID := ""
	start := time.Now()
	navOut, err := t.operations.Navigate(ctx, map[string]interface{}{"url": rawURL, "timeout": float64(timeout)})
	t.manager.AddStep(rec.ID, "navigate", statusFromErr(err), navOut, time.Since(start).Milliseconds(), err)
	if err != nil {
		t.fail(rec.ID, err)
		return FormatBrowserTaskMarkdown(mustGetBrowserTask(t.manager, rec.ID)), err
	}
	targetID = ParseBrowserTaskTargetID(navOut)
	t.manager.Update(rec.ID, func(r *BrowserTaskRecord) { r.TargetID = targetID })

	start = time.Now()
	extractOut, err := t.operations.Page(ctx, map[string]interface{}{"action": "extract", "target_id": targetID})
	t.manager.AddStep(rec.ID, "extract", statusFromErr(err), extractOut, time.Since(start).Milliseconds(), err)
	if err != nil {
		t.fail(rec.ID, err)
		return FormatBrowserTaskMarkdown(mustGetBrowserTask(t.manager, rec.ID)), err
	}
	t.manager.Update(rec.ID, func(r *BrowserTaskRecord) { r.ExtractExcerpt = textutil.Truncate(extractOut, 2000) })

	screenshotRel := filepath.ToSlash(filepath.Join(".openkitty", "storage", "browser_tasks", rec.ID, "screenshot.png"))
	screenshotAbs := filepath.Join(t.workspaceOrDot(), filepath.FromSlash(screenshotRel))
	start = time.Now()
	screenshotTimeout := min(timeout, 20)
	shotOut, err := t.operations.Page(ctx, map[string]interface{}{
		"action":     "screenshot",
		"target_id":  targetID,
		"file":       screenshotAbs,
		"timeout_ms": float64(screenshotTimeout * 1000),
	})
	t.manager.AddStep(rec.ID, "screenshot", statusFromErr(err), shotOut, time.Since(start).Milliseconds(), err)
	if err == nil {
		t.manager.Update(rec.ID, func(r *BrowserTaskRecord) {
			r.ScreenshotPath = screenshotRel
			r.ScreenshotURL = "/api/files/download?path=" + url.QueryEscape("/"+screenshotRel)
		})
	}

	replay := ""
	if t.operations.ExportReplay != nil {
		replay = t.operations.ExportReplay()
	}
	if strings.TrimSpace(replay) != "" {
		replayRel := filepath.ToSlash(filepath.Join(".openkitty", "storage", "browser_tasks", rec.ID, "replay.js"))
		replayAbs := filepath.Join(t.workspaceOrDot(), filepath.FromSlash(replayRel))
		if writeErr := os.WriteFile(replayAbs, []byte(replay), 0644); writeErr == nil {
			t.manager.Update(rec.ID, func(r *BrowserTaskRecord) {
				r.ReplayPath = replayRel
				r.ReplayURL = "/api/files/download?path=" + url.QueryEscape("/"+replayRel)
			})
		} else {
			t.manager.AddStep(rec.ID, "replay", "failed", "", 0, writeErr)
		}
	}

	if closeTab && targetID != "" && t.operations.Close != nil {
		start = time.Now()
		closeOut, closeErr := t.operations.Close(ctx, map[string]interface{}{"target_id": targetID})
		t.manager.AddStep(rec.ID, "close", statusFromErr(closeErr), closeOut, time.Since(start).Milliseconds(), closeErr)
	}

	t.manager.Update(rec.ID, func(r *BrowserTaskRecord) {
		if r.Status != BrowserTaskFailed {
			r.Status = BrowserTaskCompleted
		}
	})
	return FormatBrowserTaskMarkdown(mustGetBrowserTask(t.manager, rec.ID)), nil
}

func (t *BrowserTaskTool) workspaceOrDot() string {
	if strings.TrimSpace(t.workspace) == "" {
		return "."
	}
	return t.workspace
}

func (t *BrowserTaskTool) fail(taskID string, err error) {
	t.manager.Update(taskID, func(r *BrowserTaskRecord) {
		r.Status = BrowserTaskFailed
		if err != nil {
			r.Error = err.Error()
		}
	})
}

func statusFromErr(err error) string {
	if err != nil {
		return "failed"
	}
	return "completed"
}

func mustGetBrowserTask(manager *BrowserTaskManager, taskID string) *BrowserTaskRecord {
	rec, ok := manager.Get(taskID)
	if !ok {
		return &BrowserTaskRecord{ID: taskID, Status: BrowserTaskFailed, Error: "browser task record missing"}
	}
	return rec
}

func ParseBrowserTaskTargetID(output string) string {
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Opened tab ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "Opened tab "))
		}
	}
	return ""
}

func estimateBrowserTaskCredits(rec *BrowserTaskRecord) int {
	if rec == nil {
		return 0
	}
	credits := 1
	for _, step := range rec.Steps {
		switch step.Name {
		case "navigate", "extract", "screenshot", "replay":
			credits++
		default:
			if step.Status != "" {
				credits++
			}
		}
	}
	return credits
}

func BrowserTaskNextActions(rec *BrowserTaskRecord) []string {
	if rec == nil {
		return nil
	}
	if rec.Status == BrowserTaskFailed {
		return []string{"Open the browser task detail and retry after fixing the reported blocker."}
	}
	actions := []string{}
	if rec.TargetID != "" {
		actions = append(actions, "Continue with web_page using target_id "+rec.TargetID+" for click/input actions.")
	}
	if rec.ScreenshotURL != "" {
		actions = append(actions, "Review the screenshot before taking state-changing actions.")
	}
	if rec.ReplayURL != "" {
		actions = append(actions, "Use the replay script for audit or regression testing.")
	}
	if len(actions) == 0 {
		actions = append(actions, "Inspect the browser task steps for next actions.")
	}
	return actions
}

func FormatBrowserTaskMarkdown(rec *BrowserTaskRecord) string {
	if rec == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString("## Browser task\n\n")
	b.WriteString("- Task ID: " + rec.ID + "\n")
	b.WriteString("- Status: " + string(rec.Status) + "\n")
	b.WriteString("- URL: " + emptyValueDash(rec.URL) + "\n")
	if rec.Objective != "" {
		b.WriteString("- Objective: " + rec.Objective + "\n")
	}
	if rec.TargetID != "" {
		b.WriteString("- Target ID: " + rec.TargetID + "\n")
	}
	b.WriteString("- Estimated credits: " + strconv.Itoa(rec.EstimatedCredit) + "\n")
	if rec.Error != "" {
		b.WriteString("- Error: " + rec.Error + "\n")
	}
	if rec.ScreenshotURL != "" {
		b.WriteString("- Screenshot: [" + filepath.Base(rec.ScreenshotPath) + "](" + rec.ScreenshotURL + ")\n")
	}
	if rec.ReplayURL != "" {
		b.WriteString("- Replay: [" + filepath.Base(rec.ReplayPath) + "](" + rec.ReplayURL + ")\n")
	}
	if len(rec.Steps) > 0 {
		b.WriteString("\n### Steps\n\n")
		for _, step := range rec.Steps {
			b.WriteString("- " + step.Name + ": " + step.Status)
			if step.Error != "" {
				b.WriteString(" - " + step.Error)
			}
			b.WriteString("\n")
		}
	}
	if rec.ExtractExcerpt != "" {
		b.WriteString("\n### Page extract\n\n```text\n")
		b.WriteString(textutil.Truncate(rec.ExtractExcerpt, 1800))
		b.WriteString("\n```\n")
	}
	if len(rec.NextActions) > 0 {
		b.WriteString("\n### Next actions\n\n")
		for _, action := range rec.NextActions {
			b.WriteString("- " + action + "\n")
		}
	}
	return strings.TrimSpace(b.String())
}

func emptyValueDash(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "-"
	}
	return value
}
