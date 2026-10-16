// Package computer provides a model-facing, stateful computer interaction
// protocol over browser and desktop automation backends.
package computer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	"mac/internal/configvalue"
	"mac/internal/textutil"
	"mac/internal/toolcontract"
)

const (
	maxRunSteps       = 16
	maxSessionHistory = 80
)

var browserTargetPattern = regexp.MustCompile(`(?m)^Opened tab\s+([^\s]+)`)

// ExecuteFunc is the small adapter used to connect existing browser and
// desktop tools without coupling this package to their implementations.
type ExecuteFunc func(context.Context, map[string]interface{}) (string, error)

// Operations contains the existing low-level automation backends.
type Operations struct {
	BrowserTargets  ExecuteFunc
	BrowserNavigate ExecuteFunc
	BrowserPage     ExecuteFunc
	BrowserClose    ExecuteFunc
	Desktop         ExecuteFunc
}

// SessionStatus describes whether a session can continue receiving actions.
type SessionStatus string

const (
	SessionActive SessionStatus = "active"
	SessionFailed SessionStatus = "failed"
	SessionClosed SessionStatus = "closed"
)

// SessionStep is a compact, value-safe record of one computer operation.
type SessionStep struct {
	Number         int                    `json:"number"`
	Operation      string                 `json:"operation"`
	Status         string                 `json:"status"`
	StartedAt      time.Time              `json:"started_at"`
	DurationMS     int64                  `json:"duration_ms,omitempty"`
	Arguments      map[string]interface{} `json:"arguments,omitempty"`
	Output         string                 `json:"output,omitempty"`
	Error          string                 `json:"error,omitempty"`
	Verification   string                 `json:"verification,omitempty"`
	ScreenshotPath string                 `json:"screenshot_path,omitempty"`
	ScreenshotURL  string                 `json:"screenshot_url,omitempty"`
}

// SessionAssertion records an explicit success condition separately from the
// action list, so learned workflows can retain completion evidence without
// turning verification into a replayable UI action.
type SessionAssertion struct {
	Kind     string `json:"kind"`
	Expected string `json:"expected"`
	Status   string `json:"status"`
	Error    string `json:"error,omitempty"`
}

// Session retains the target and latest observation across model calls.
type Session struct {
	ID                 string             `json:"id"`
	Surface            string             `json:"surface"`
	Objective          string             `json:"objective,omitempty"`
	Status             SessionStatus      `json:"status"`
	TargetID           string             `json:"target_id,omitempty"`
	URL                string             `json:"url,omitempty"`
	StartedAt          time.Time          `json:"started_at"`
	UpdatedAt          time.Time          `json:"updated_at"`
	LastObservation    string             `json:"last_observation,omitempty"`
	ObservationID      string             `json:"observation_id,omitempty"`
	ObservationVersion uint64             `json:"observation_version"`
	ObservationValid   bool               `json:"observation_valid"`
	LastScreenshotPath string             `json:"last_screenshot_path,omitempty"`
	LastScreenshotURL  string             `json:"last_screenshot_url,omitempty"`
	LastError          string             `json:"last_error,omitempty"`
	StepCount          int                `json:"step_count"`
	Steps              []SessionStep      `json:"steps,omitempty"`
	Assertions         []SessionAssertion `json:"assertions,omitempty"`
}

// Manager owns in-memory computer sessions for one tool registration.
type Manager struct {
	mu       sync.RWMutex
	sessions map[string]*Session
	seq      uint64
}

func NewManager() *Manager {
	return &Manager{sessions: make(map[string]*Session)}
}

func (m *Manager) start(surface, objective string) *Session {
	now := time.Now()
	m.mu.Lock()
	m.seq++
	session := &Session{
		ID:        fmt.Sprintf("computer_%s_%06d", now.Format("060102150405"), m.seq),
		Surface:   surface,
		Objective: strings.TrimSpace(objective),
		Status:    SessionActive,
		StartedAt: now,
		UpdatedAt: now,
	}
	m.sessions[session.ID] = session
	m.mu.Unlock()
	return cloneSession(session)
}

func (m *Manager) get(id string) (*Session, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	session, ok := m.sessions[id]
	if !ok || session == nil {
		return nil, false
	}
	return cloneSession(session), true
}

func (m *Manager) update(id string, update func(*Session)) (*Session, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	session, ok := m.sessions[id]
	if !ok || session == nil {
		return nil, false
	}
	if update != nil {
		update(session)
	}
	session.UpdatedAt = time.Now()
	if len(session.Steps) > maxSessionHistory {
		session.Steps = append([]SessionStep(nil), session.Steps[len(session.Steps)-maxSessionHistory:]...)
	}
	return cloneSession(session), true
}

func cloneSession(session *Session) *Session {
	if session == nil {
		return nil
	}
	copy := *session
	copy.Steps = append([]SessionStep(nil), session.Steps...)
	for index := range copy.Steps {
		copy.Steps[index].Arguments = copyStepParameters(session.Steps[index].Arguments)
	}
	copy.Assertions = append([]SessionAssertion(nil), session.Assertions...)
	return &copy
}

// Export returns a stable JSON snapshot for workflow learning and diagnostics.
func (m *Manager) Export(id string) (string, error) {
	session, ok := m.get(strings.TrimSpace(id))
	if !ok {
		return "", fmt.Errorf("computer session %q was not found", id)
	}
	data, err := json.Marshal(session)
	if err != nil {
		return "", fmt.Errorf("encode computer session: %w", err)
	}
	return string(data), nil
}

// Tool exposes a compact observe-act-verify loop to the model.
type Tool struct {
	workspace string
	manager   *Manager
	ops       Operations
	gate      chan struct{}
}

func NewTool(workspace string, manager *Manager, ops Operations) *Tool {
	if manager == nil {
		manager = NewManager()
	}
	return &Tool{workspace: workspace, manager: manager, ops: ops, gate: make(chan struct{}, 1)}
}

func (t *Tool) Name() string { return "computer" }

func (t *Tool) Description() string {
	return "Operate browser or desktop interfaces through a persistent observe-act-verify session. " +
		"Use observe before acting, run for a short ordered action group, and inspect the returned screenshot and state before continuing. " +
		"Browser sessions use stable DOM controls; desktop sessions use the active graphical session on Windows or Linux."
}

func (t *Tool) ToolSearchMetadata() toolcontract.ToolSearchMetadata {
	return toolcontract.ToolSearchMetadata{
		Aliases: []string{"use_computer", "computer_use", "computer-use", "desktop automation", "GUI automation", "电脑操作", "桌面操作", "界面操作"},
		Tags:    []string{"browser", "desktop", "playwright", "CDP", "X11", "Wayland", "LIMS", "form filling", "UI testing"},
	}
}

func (t *Tool) IsNeverParallel() bool { return true }

func (t *Tool) SandboxCapabilitiesForCall(params map[string]interface{}) toolcontract.SandboxCapabilities {
	surface := strings.ToLower(strings.TrimSpace(stringParam(params, "surface")))
	if surface == "" || surface == "auto" {
		if stringParam(params, "url") != "" || stringParam(params, "target_id") != "" {
			surface = "browser"
		} else if id := strings.TrimSpace(stringParam(params, "session_id")); id != "" {
			if session, ok := t.manager.get(id); ok {
				surface = session.Surface
			}
		}
	}
	return toolcontract.SandboxCapabilities{
		Process: surface == "desktop",
		Network: surface == "browser",
	}
}

func (t *Tool) Parameters() map[string]interface{} {
	stepProperties := computerStepProperties()
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"action": map[string]interface{}{
				"type":        "string",
				"enum":        []string{"status", "observe", "act", "run", "history", "close"},
				"description": "Session action. observe reads the current UI; act performs one operation; run performs up to 16 ordered operations.",
			},
			"session_id": map[string]interface{}{
				"type":        "string",
				"description": "Existing computer session ID. Omit on the first observe/act/run call.",
			},
			"surface": map[string]interface{}{
				"type":        "string",
				"enum":        []string{"auto", "browser", "desktop"},
				"description": "Target surface. auto chooses browser when url or target_id is supplied, otherwise desktop.",
			},
			"objective": map[string]interface{}{
				"type":        "string",
				"description": "Short task objective retained with the session.",
			},
			"url": map[string]interface{}{
				"type":        "string",
				"description": "URL to open for a new browser session.",
			},
			"target_id": map[string]interface{}{
				"type":        "string",
				"description": "Existing browser tab target ID.",
			},
			"observation_id": map[string]interface{}{
				"type":        "string",
				"description": "For act/run, echo the latest session observation_id to reject actions based on an older observation. This is a session revision check, not a live DOM fingerprint.",
			},
			"operation": map[string]interface{}{
				"type":        "string",
				"description": "Single operation for action=act. Browser: navigate/extract/click/input/select/check/hover/press/upload/scroll/back/forward/reload/wait/eval/dialog/screenshot. Desktop: open/screenshot/active_window/click/type/key/scroll/drag/capabilities.",
			},
			"steps": map[string]interface{}{
				"type":        "array",
				"description": "Ordered operations for action=run. Keep groups short and verify after each meaningful UI transition.",
				"maxItems":    maxRunSteps,
				"items": map[string]interface{}{
					"type":                 "object",
					"properties":           stepProperties,
					"required":             []string{"operation"},
					"additionalProperties": false,
				},
			},
			"observe_after": map[string]interface{}{
				"type":        "boolean",
				"description": "Capture a fresh observation after act/run. Default true.",
			},
			"stop_on_error": map[string]interface{}{
				"type":        "boolean",
				"description": "Stop a run at the first failed operation. Default true.",
			},
			"expect_text": map[string]interface{}{
				"type":        "string",
				"description": "Browser text that must appear after the action group.",
			},
			"expect_window": map[string]interface{}{
				"type":        "string",
				"description": "Desktop active-window title fragment that must appear after the action group.",
			},
		},
		"required":             []string{"action"},
		"additionalProperties": true,
	}
}

func computerStepProperties() map[string]interface{} {
	return map[string]interface{}{
		"operation":     map[string]interface{}{"type": "string"},
		"expect_text":   map[string]interface{}{"type": "string", "description": "Browser text to verify immediately after this step; failure always stops the group."},
		"expect_window": map[string]interface{}{"type": "string", "description": "Desktop window fragment to verify immediately after this step; failure always stops the group."},
		"url":           map[string]interface{}{"type": "string"},
		"target_path":   map[string]interface{}{"type": "string"},
		"index":         map[string]interface{}{"type": "integer"},
		"selector":      map[string]interface{}{"type": "string"},
		"text":          map[string]interface{}{"type": "string"},
		"value":         map[string]interface{}{"type": "string"},
		"checked":       map[string]interface{}{"type": "boolean"},
		"accept":        map[string]interface{}{"type": "boolean"},
		"prompt_text":   map[string]interface{}{"type": "string"},
		"key":           map[string]interface{}{"type": "string"},
		"files": map[string]interface{}{
			"type":  "array",
			"items": map[string]interface{}{"type": "string"},
		},
		"code":           map[string]interface{}{"type": "string"},
		"x":              map[string]interface{}{"type": "integer"},
		"y":              map[string]interface{}{"type": "integer"},
		"end_x":          map[string]interface{}{"type": "integer"},
		"end_y":          map[string]interface{}{"type": "integer"},
		"delta_y":        map[string]interface{}{"type": "integer"},
		"direction":      map[string]interface{}{"type": "string"},
		"timeout_ms":     map[string]interface{}{"type": "integer"},
		"delay_ms":       map[string]interface{}{"type": "integer"},
		"duration_ms":    map[string]interface{}{"type": "integer"},
		"capture_screen": map[string]interface{}{"type": "boolean"},
		"dry_run":        map[string]interface{}{"type": "boolean"},
	}
}

func (t *Tool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	// The registry's never-parallel flag does not cover direct workflow replay
	// and concurrent callers. Serialize actual backend use, cancellably.
	select {
	case t.gate <- struct{}{}:
		defer func() { <-t.gate }()
	case <-ctx.Done():
		return "", ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	action := strings.ToLower(strings.TrimSpace(stringParam(params, "action")))
	switch action {
	case "status":
		return t.status(ctx, params)
	case "history":
		return t.history(params)
	case "close":
		return t.close(ctx, params)
	case "observe", "act", "run":
		return t.executeSessionAction(ctx, action, params)
	default:
		return "", fmt.Errorf("unknown computer action %q", action)
	}
}

func (t *Tool) status(ctx context.Context, params map[string]interface{}) (string, error) {
	if id := strings.TrimSpace(stringParam(params, "session_id")); id != "" {
		session, ok := t.manager.get(id)
		if !ok {
			return "", fmt.Errorf("computer session %q was not found", id)
		}
		return encodeResult(map[string]interface{}{"ok": true, "session": session})
	}
	result := map[string]interface{}{
		"ok":       true,
		"platform": runtime.GOOS,
		"browser": map[string]interface{}{
			"available": t.ops.BrowserPage != nil && t.ops.BrowserNavigate != nil,
			"backend":   "Chrome CDP",
		},
		"desktop": map[string]interface{}{"available": t.ops.Desktop != nil},
	}
	if t.ops.Desktop != nil {
		out, err := t.ops.Desktop(ctx, map[string]interface{}{
			"action":         "capabilities",
			"capture_screen": false,
			"objective":      "Inspect desktop automation capabilities",
		})
		if err != nil {
			result["desktop"] = map[string]interface{}{"available": false, "error": err.Error()}
		} else {
			result["desktop"] = map[string]interface{}{"available": true, "capabilities": out}
		}
	}
	return encodeResult(result)
}

func (t *Tool) history(params map[string]interface{}) (string, error) {
	id := strings.TrimSpace(stringParam(params, "session_id"))
	if id == "" {
		return "", errors.New("session_id is required for computer history")
	}
	session, ok := t.manager.get(id)
	if !ok {
		return "", fmt.Errorf("computer session %q was not found", id)
	}
	return encodeResult(map[string]interface{}{"ok": true, "session": session})
}

func (t *Tool) close(ctx context.Context, params map[string]interface{}) (string, error) {
	id := strings.TrimSpace(stringParam(params, "session_id"))
	if id == "" {
		return "", errors.New("session_id is required for computer close")
	}
	session, ok := t.manager.get(id)
	if !ok {
		return "", fmt.Errorf("computer session %q was not found", id)
	}
	var closeErr error
	if session.Surface == "browser" && session.TargetID != "" && t.ops.BrowserClose != nil {
		_, closeErr = t.ops.BrowserClose(ctx, map[string]interface{}{"target_id": session.TargetID})
	}
	session, _ = t.manager.update(id, func(current *Session) {
		current.Status = SessionClosed
		if closeErr != nil {
			current.LastError = closeErr.Error()
		}
	})
	if closeErr != nil {
		return encodeResult(map[string]interface{}{"ok": false, "session": session, "error": closeErr.Error()})
	}
	return encodeResult(map[string]interface{}{"ok": true, "session": session})
}

func (t *Tool) executeSessionAction(ctx context.Context, action string, params map[string]interface{}) (string, error) {
	session, err := t.resolveSession(params)
	if err != nil {
		return "", err
	}
	if session.Status == SessionClosed {
		return "", fmt.Errorf("computer session %q is closed", session.ID)
	}
	if action == "act" || action == "run" {
		if expected := strings.TrimSpace(stringParam(params, "observation_id")); expected != "" &&
			(!session.ObservationValid || expected != session.ObservationID) {
			return encodeResult(map[string]interface{}{"ok": false, "session": session, "error": "stale_observation", "next_action": "observe", "automatic_replay_allowed": false})
		}
	}

	stopOnError := boolParam(params, "stop_on_error", true)
	observeAfter := boolParam(params, "observe_after", true)
	var steps []map[string]interface{}
	switch action {
	case "observe":
		observeAfter = false
	case "act":
		operation := strings.TrimSpace(stringParam(params, "operation"))
		if operation == "" {
			return "", errors.New("operation is required for computer action=act")
		}
		step := copyStepParameters(params)
		// A single act keeps the existing group assertion receipt; per-step
		// assertions are for run and must not be checked twice.
		delete(step, "expect_text")
		delete(step, "expect_window")
		step["operation"] = operation
		steps = []map[string]interface{}{step}
	case "run":
		steps, err = decodeSteps(params["steps"])
		if err != nil {
			return "", err
		}
	}
	// Reject unusable contracts before opening a tab or executing an earlier
	// step. A wrong-surface assertion cannot be verified after a real action.
	if err := validateAssertionSurface(session.Surface, params); err != nil {
		return "", err
	}
	for _, step := range steps {
		if err := validateAssertionSurface(session.Surface, step); err != nil {
			return "", err
		}
	}
	if session.Surface == "browser" {
		if err := t.ensureBrowserTarget(ctx, session, params); err != nil {
			return t.failSession(session.ID, err)
		}
		session, _ = t.manager.get(session.ID)
	}

	results := make([]SessionStep, 0, len(steps)+2)
	for _, rawStep := range steps {
		if err := ctx.Err(); err != nil {
			return t.failSession(session.ID, err)
		}
		current, _ := t.manager.get(session.ID)
		step := t.executeStep(ctx, current, rawStep)
		results = append(results, step)
		t.appendStep(session.ID, step)
		if step.Status == "failed" && (stopOnError || step.Verification == "failed") {
			break
		}
	}

	failed := firstFailedStep(results)
	if failed == nil && action != "observe" {
		if err := t.verify(ctx, session.ID, params); err != nil {
			t.recordAssertions(session.ID, params, err)
			step := SessionStep{Operation: "verify", Status: "failed", StartedAt: time.Now(), Error: err.Error()}
			results = append(results, step)
			t.appendStep(session.ID, step)
			failed = &step
		} else {
			t.recordAssertions(session.ID, params, nil)
		}
	}

	if action == "observe" || observeAfter {
		current, _ := t.manager.get(session.ID)
		step := t.observe(ctx, current)
		results = append(results, step)
		t.appendStep(session.ID, step)
		if step.Status == "failed" && failed == nil {
			failed = &step
		}
	}

	updated, _ := t.manager.update(session.ID, func(current *Session) {
		if failed != nil {
			current.Status = SessionFailed
			current.LastError = failed.Error
		} else {
			current.Status = SessionActive
			current.LastError = ""
		}
	})
	nextAction := "continue"
	if failed != nil {
		nextAction = "observe_and_replan"
	}
	return encodeResult(map[string]interface{}{
		"ok":                       failed == nil,
		"session":                  updated,
		"results":                  results,
		"next_action":              nextAction,
		"automatic_replay_allowed": false,
	})
}

func (t *Tool) resolveSession(params map[string]interface{}) (*Session, error) {
	id := strings.TrimSpace(stringParam(params, "session_id"))
	if id != "" {
		session, ok := t.manager.get(id)
		if !ok {
			return nil, fmt.Errorf("computer session %q was not found", id)
		}
		if targetID := strings.TrimSpace(stringParam(params, "target_id")); targetID != "" && session.TargetID == "" {
			session, _ = t.manager.update(id, func(current *Session) { current.TargetID = targetID })
		}
		return session, nil
	}
	surface := strings.ToLower(strings.TrimSpace(stringParam(params, "surface")))
	if surface == "" || surface == "auto" {
		if stringParam(params, "url") != "" || stringParam(params, "target_id") != "" {
			surface = "browser"
		} else {
			surface = "desktop"
		}
	}
	if surface != "browser" && surface != "desktop" {
		return nil, fmt.Errorf("unsupported computer surface %q", surface)
	}
	session := t.manager.start(surface, stringParam(params, "objective"))
	targetID := strings.TrimSpace(stringParam(params, "target_id"))
	urlValue := strings.TrimSpace(stringParam(params, "url"))
	if targetID != "" || urlValue != "" {
		session, _ = t.manager.update(session.ID, func(current *Session) {
			current.TargetID = targetID
			current.URL = urlValue
		})
	}
	return session, nil
}

func (t *Tool) ensureBrowserTarget(ctx context.Context, session *Session, params map[string]interface{}) error {
	if t.ops.BrowserPage == nil || t.ops.BrowserNavigate == nil {
		return errors.New("browser computer backend is unavailable")
	}
	if session.TargetID != "" {
		return nil
	}
	urlValue := strings.TrimSpace(stringParam(params, "url"))
	if urlValue == "" {
		urlValue = session.URL
	}
	if urlValue == "" {
		// web_page can resolve the most recent tab itself. Leave TargetID empty
		// so an already-open interactive browser remains usable.
		return nil
	}
	out, err := t.ops.BrowserNavigate(ctx, map[string]interface{}{"url": urlValue})
	if err != nil {
		return err
	}
	match := browserTargetPattern.FindStringSubmatch(out)
	if len(match) != 2 {
		return fmt.Errorf("browser navigation did not return a target ID: %s", textutil.Truncate(out, 500))
	}
	_, _ = t.manager.update(session.ID, func(current *Session) {
		current.TargetID = strings.TrimSpace(match[1])
		current.URL = urlValue
	})
	return nil
}

func (t *Tool) executeStep(ctx context.Context, session *Session, raw map[string]interface{}) SessionStep {
	operation := strings.ToLower(strings.TrimSpace(stringParam(raw, "operation")))
	arguments := copyStepParameters(raw)
	delete(arguments, "operation")
	step := SessionStep{
		Operation: operation,
		Status:    "completed",
		StartedAt: time.Now(),
		Arguments: arguments,
	}
	if operation == "" {
		step.Status = "failed"
		step.Error = "operation is required"
		return step
	}
	started := time.Now()
	// An action can change the UI even when its receipt is lost. Invalidate the
	// old observation before calling the backend, not only after success.
	if operation != "extract" && operation != "screenshot" && operation != "active_window" && operation != "capabilities" && operation != "wait" {
		_, _ = t.manager.update(session.ID, func(current *Session) { current.ObservationValid = false })
	}
	var output string
	var err error
	if session.Surface == "browser" {
		output, err = t.executeBrowserStep(ctx, session, raw)
	} else {
		output, err = t.executeDesktopStep(ctx, session, raw)
	}
	if err == nil && (stringParam(raw, "expect_text") != "" || stringParam(raw, "expect_window") != "") {
		err = t.verify(ctx, session.ID, raw)
		step.Verification = "passed"
		if err != nil {
			step.Verification = "failed"
		}
	}
	step.DurationMS = time.Since(started).Milliseconds()
	step.Output = textutil.Truncate(strings.TrimSpace(output), 3000)
	if err != nil {
		step.Status = "failed"
		step.Error = textutil.Truncate(err.Error(), 1000)
	}
	return step
}

func (t *Tool) recordAssertions(sessionID string, params map[string]interface{}, verifyErr error) {
	records := make([]SessionAssertion, 0, 2)
	if expected := strings.TrimSpace(stringParam(params, "expect_text")); expected != "" {
		records = append(records, SessionAssertion{Kind: "visible_text", Expected: expected, Status: "completed"})
	}
	if expected := strings.TrimSpace(stringParam(params, "expect_window")); expected != "" {
		records = append(records, SessionAssertion{Kind: "active_window", Expected: expected, Status: "completed"})
	}
	if len(records) == 0 {
		return
	}
	if verifyErr != nil {
		for index := range records {
			records[index].Status = "failed"
			records[index].Error = textutil.Truncate(verifyErr.Error(), 1000)
		}
	}
	_, _ = t.manager.update(sessionID, func(session *Session) {
		for _, record := range records {
			replaced := false
			for index := range session.Assertions {
				if session.Assertions[index].Kind == record.Kind && session.Assertions[index].Expected == record.Expected {
					session.Assertions[index] = record
					replaced = true
					break
				}
			}
			if !replaced {
				session.Assertions = append(session.Assertions, record)
			}
		}
	})
}

func (t *Tool) executeBrowserStep(ctx context.Context, session *Session, raw map[string]interface{}) (string, error) {
	operation := strings.ToLower(strings.TrimSpace(stringParam(raw, "operation")))
	if operation == "navigate" {
		urlValue := strings.TrimSpace(stringParam(raw, "url"))
		if urlValue == "" {
			return "", errors.New("url is required for browser navigate")
		}
		out, err := t.ops.BrowserNavigate(ctx, map[string]interface{}{"url": urlValue})
		if err != nil {
			return out, err
		}
		match := browserTargetPattern.FindStringSubmatch(out)
		if len(match) != 2 {
			return out, errors.New("browser navigate did not return a target ID")
		}
		_, _ = t.manager.update(session.ID, func(current *Session) {
			current.TargetID = strings.TrimSpace(match[1])
			current.URL = urlValue
		})
		return out, nil
	}
	allowed := map[string]bool{
		"extract": true, "click": true, "input": true, "select": true, "check": true,
		"hover": true, "press": true, "upload": true, "scroll": true, "back": true,
		"forward": true, "reload": true, "wait": true, "eval": true, "dialog": true,
		"screenshot": true,
	}
	if !allowed[operation] {
		return "", fmt.Errorf("unsupported browser computer operation %q", operation)
	}
	params := copyStepParameters(raw)
	params["action"] = operation
	if _, ok := params["target_id"]; !ok && session.TargetID != "" {
		params["target_id"] = session.TargetID
	}
	if operation == "screenshot" {
		path, _ := t.nextScreenshotPath(session.ID)
		params["file"] = path
	}
	return t.ops.BrowserPage(ctx, params)
}

func (t *Tool) executeDesktopStep(ctx context.Context, session *Session, raw map[string]interface{}) (string, error) {
	if t.ops.Desktop == nil {
		return "", errors.New("desktop computer backend is unavailable")
	}
	operation := strings.ToLower(strings.TrimSpace(stringParam(raw, "operation")))
	allowed := map[string]bool{
		"open": true, "screenshot": true, "active_window": true, "click": true,
		"type": true, "key": true, "scroll": true, "drag": true, "capabilities": true,
	}
	if !allowed[operation] {
		return "", fmt.Errorf("unsupported desktop computer operation %q", operation)
	}
	params := copyStepParameters(raw)
	params["action"] = operation
	params["objective"] = session.Objective
	if _, ok := params["capture_screen"]; !ok {
		params["capture_screen"] = false
	}
	output, err := t.ops.Desktop(ctx, params)
	if err != nil {
		return output, err
	}
	if failure := desktopTaskFailure(output); failure != "" {
		return output, errors.New(failure)
	}
	return output, nil
}

func desktopTaskFailure(output string) string {
	failed := false
	message := ""
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "-"))
		if strings.EqualFold(line, "status: failed") {
			failed = true
		}
		if strings.HasPrefix(strings.ToLower(line), "error:") {
			message = strings.TrimSpace(line[len("error:"):])
		}
	}
	if !failed {
		return ""
	}
	if message == "" {
		message = "desktop action failed"
	}
	return message
}

func (t *Tool) observe(ctx context.Context, session *Session) SessionStep {
	step := SessionStep{Operation: "observe", Status: "completed", StartedAt: time.Now()}
	started := time.Now()
	screenshotPath, screenshotRel := t.nextScreenshotPath(session.ID)
	var output string
	var err error
	if session.Surface == "browser" {
		params := map[string]interface{}{"action": "extract"}
		if session.TargetID != "" {
			params["target_id"] = session.TargetID
		}
		output, err = t.ops.BrowserPage(ctx, params)
		if err == nil {
			shotParams := map[string]interface{}{"action": "screenshot", "file": screenshotPath}
			if session.TargetID != "" {
				shotParams["target_id"] = session.TargetID
			}
			var shotOut string
			shotOut, err = t.ops.BrowserPage(ctx, shotParams)
			if shotOut != "" {
				output += "\n" + shotOut
			}
		}
	} else if t.ops.Desktop == nil {
		err = errors.New("desktop computer backend is unavailable")
	} else {
		output, err = t.ops.Desktop(ctx, map[string]interface{}{
			"action":          "active_window",
			"objective":       session.Objective,
			"capture_screen":  true,
			"screenshot_file": screenshotPath,
		})
		if err == nil {
			if failure := desktopTaskFailure(output); failure != "" {
				err = errors.New(failure)
			}
		}
	}
	step.DurationMS = time.Since(started).Milliseconds()
	step.Output = textutil.Truncate(strings.TrimSpace(output), 6000)
	if err != nil {
		_, _ = t.manager.update(session.ID, func(current *Session) { current.ObservationValid = false })
		step.Status = "failed"
		step.Error = textutil.Truncate(err.Error(), 1000)
		return step
	}
	if info, statErr := os.Stat(screenshotPath); statErr == nil && info.Size() > 0 {
		step.ScreenshotPath = screenshotRel
		step.ScreenshotURL = downloadURL(screenshotRel)
	}
	_, _ = t.manager.update(session.ID, func(current *Session) {
		current.ObservationVersion++
		current.ObservationID = fmt.Sprintf("%s.o%d", current.ID, current.ObservationVersion)
		current.ObservationValid = true
		current.LastObservation = step.Output
		current.LastScreenshotPath = step.ScreenshotPath
		current.LastScreenshotURL = step.ScreenshotURL
	})
	return step
}

func validateAssertionSurface(surface string, params map[string]interface{}) error {
	for key, requiredSurface := range map[string]string{"expect_text": "browser", "expect_window": "desktop"} {
		if value, exists := params[key]; exists {
			text, ok := value.(string)
			if !ok {
				return fmt.Errorf("%s must be a string", key)
			}
			if strings.TrimSpace(text) != "" && surface != requiredSurface {
				return fmt.Errorf("%s is only valid for %s sessions", key, requiredSurface)
			}
		}
	}
	return nil
}

func (t *Tool) verify(ctx context.Context, sessionID string, params map[string]interface{}) error {
	session, ok := t.manager.get(sessionID)
	if !ok {
		return errors.New("computer session disappeared before verification")
	}
	if expected := strings.TrimSpace(stringParam(params, "expect_text")); expected != "" {
		if session.Surface != "browser" {
			return errors.New("expect_text currently verifies browser DOM text; inspect the desktop screenshot for visual text")
		}
		waitParams := map[string]interface{}{
			"action":     "wait",
			"text":       expected,
			"timeout_ms": configvalue.Int(params, "timeout_ms", 5000),
		}
		if session.TargetID != "" {
			waitParams["target_id"] = session.TargetID
		}
		if _, err := t.ops.BrowserPage(ctx, waitParams); err != nil {
			return fmt.Errorf("expected browser text %q was not observed: %w", expected, err)
		}
	}
	if expected := strings.TrimSpace(stringParam(params, "expect_window")); expected != "" {
		if session.Surface != "desktop" {
			return errors.New("expect_window is only valid for desktop sessions")
		}
		out, err := t.ops.Desktop(ctx, map[string]interface{}{
			"action":         "active_window",
			"capture_screen": false,
			"objective":      session.Objective,
		})
		if err != nil {
			return err
		}
		if failure := desktopTaskFailure(out); failure != "" {
			return errors.New(failure)
		}
		if !strings.Contains(strings.ToLower(out), strings.ToLower(expected)) {
			return fmt.Errorf("active window does not contain %q", expected)
		}
	}
	return nil
}

func (t *Tool) appendStep(sessionID string, step SessionStep) {
	_, _ = t.manager.update(sessionID, func(session *Session) {
		session.StepCount++
		step.Number = session.StepCount
		session.Steps = append(session.Steps, step)
		if step.ScreenshotPath != "" {
			session.LastScreenshotPath = step.ScreenshotPath
			session.LastScreenshotURL = step.ScreenshotURL
		}
	})
}

func (t *Tool) failSession(sessionID string, failure error) (string, error) {
	session, _ := t.manager.update(sessionID, func(current *Session) {
		current.Status = SessionFailed
		current.LastError = failure.Error()
	})
	result, _ := encodeResult(map[string]interface{}{"ok": false, "session": session, "error": failure.Error()})
	return result, failure
}

func (t *Tool) nextScreenshotPath(sessionID string) (string, string) {
	workspace := strings.TrimSpace(t.workspace)
	if workspace == "" {
		workspace = "."
	}
	sequence := 1
	if session, ok := t.manager.get(sessionID); ok {
		sequence = session.StepCount + 1
	}
	rel := filepath.ToSlash(filepath.Join(".openkitty", "storage", "computer", sessionID, fmt.Sprintf("screen-%04d.png", sequence)))
	abs := filepath.Join(workspace, filepath.FromSlash(rel))
	_ = os.MkdirAll(filepath.Dir(abs), 0755)
	return abs, rel
}

func downloadURL(path string) string {
	return "/api/files/download?path=" + url.QueryEscape("/"+filepath.ToSlash(path))
}

func decodeSteps(value interface{}) ([]map[string]interface{}, error) {
	items, ok := value.([]interface{})
	if !ok || len(items) == 0 {
		return nil, errors.New("steps must contain at least one operation for computer action=run")
	}
	if len(items) > maxRunSteps {
		return nil, fmt.Errorf("computer run accepts at most %d steps", maxRunSteps)
	}
	steps := make([]map[string]interface{}, 0, len(items))
	for index, item := range items {
		step, ok := item.(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("steps[%d] must be an object", index)
		}
		if strings.TrimSpace(stringParam(step, "operation")) == "" {
			return nil, fmt.Errorf("steps[%d].operation is required", index)
		}
		steps = append(steps, step)
	}
	return steps, nil
}

func copyStepParameters(source map[string]interface{}) map[string]interface{} {
	allowed := computerStepProperties()
	copy := make(map[string]interface{}, len(allowed))
	for key, value := range source {
		if _, ok := allowed[key]; ok {
			copy[key] = value
		}
	}
	return copy
}

func firstFailedStep(steps []SessionStep) *SessionStep {
	for index := range steps {
		if steps[index].Status == "failed" {
			return &steps[index]
		}
	}
	return nil
}

func stringParam(params map[string]interface{}, name string) string {
	value, _ := params[name].(string)
	return value
}

func boolParam(params map[string]interface{}, name string, fallback bool) bool {
	value, ok := params[name].(bool)
	if !ok {
		return fallback
	}
	return value
}

func encodeResult(value interface{}) (string, error) {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return "", err
	}
	return string(data), nil
}
