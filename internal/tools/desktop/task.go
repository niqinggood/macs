package desktop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"mac/internal/configvalue"
	"mac/internal/textutil"
)

type DesktopTaskStatus string

const (
	DesktopTaskRunning   DesktopTaskStatus = "running"
	DesktopTaskCompleted DesktopTaskStatus = "completed"
	DesktopTaskFailed    DesktopTaskStatus = "failed"
)

type DesktopTaskRecord struct {
	ID              string               `json:"id"`
	Objective       string               `json:"objective,omitempty"`
	Action          string               `json:"action,omitempty"`
	TargetPath      string               `json:"target_path,omitempty"`
	Status          DesktopTaskStatus    `json:"status"`
	StartedAt       time.Time            `json:"started_at"`
	UpdatedAt       time.Time            `json:"updated_at"`
	DurationMs      int64                `json:"duration_ms,omitempty"`
	System          DesktopSystemInfo    `json:"system"`
	Steps           []DesktopTaskStep    `json:"steps"`
	Files           []DesktopFileInfo    `json:"files,omitempty"`
	Interactions    []DesktopInteraction `json:"interactions,omitempty"`
	ActiveWindow    string               `json:"active_window,omitempty"`
	ScreenshotPath  string               `json:"screenshot_path,omitempty"`
	ScreenshotURL   string               `json:"screenshot_url,omitempty"`
	Error           string               `json:"error,omitempty"`
	Risks           []string             `json:"risks,omitempty"`
	NextActions     []string             `json:"next_actions,omitempty"`
	EstimatedCredit int                  `json:"estimated_credits,omitempty"`
	Markdown        string               `json:"markdown,omitempty"`
}

type DesktopTaskStep struct {
	Name       string `json:"name"`
	Status     string `json:"status"`
	DurationMs int64  `json:"duration_ms,omitempty"`
	Output     string `json:"output,omitempty"`
	Error      string `json:"error,omitempty"`
}

type DesktopSystemInfo struct {
	OS        string `json:"os"`
	Arch      string `json:"arch"`
	Hostname  string `json:"hostname,omitempty"`
	Workspace string `json:"workspace,omitempty"`
	Home      string `json:"home,omitempty"`
}

type DesktopCapabilities struct {
	OS             string   `json:"os"`
	DisplayServer  string   `json:"display_server,omitempty"`
	SessionType    string   `json:"session_type,omitempty"`
	Screenshot     string   `json:"screenshot,omitempty"`
	ActiveWindow   string   `json:"active_window,omitempty"`
	Pointer        string   `json:"pointer,omitempty"`
	Keyboard       string   `json:"keyboard,omitempty"`
	SupportsClick  bool     `json:"supports_click"`
	SupportsType   bool     `json:"supports_type"`
	SupportsScroll bool     `json:"supports_scroll"`
	SupportsDrag   bool     `json:"supports_drag"`
	Notes          []string `json:"notes,omitempty"`
}

type DesktopFileInfo struct {
	Path    string    `json:"path"`
	Name    string    `json:"name"`
	IsDir   bool      `json:"is_dir"`
	Size    int64     `json:"size,omitempty"`
	ModTime time.Time `json:"mod_time,omitempty"`
}

type DesktopInteraction struct {
	Action      string    `json:"action"`
	X           int       `json:"x,omitempty"`
	Y           int       `json:"y,omitempty"`
	EndX        int       `json:"end_x,omitempty"`
	EndY        int       `json:"end_y,omitempty"`
	DeltaY      int       `json:"delta_y,omitempty"`
	Text        string    `json:"text,omitempty"`
	Key         string    `json:"key,omitempty"`
	WindowTitle string    `json:"window_title,omitempty"`
	DryRun      bool      `json:"dry_run,omitempty"`
	At          time.Time `json:"at"`
}

type DesktopTaskManager struct {
	mu    sync.RWMutex
	tasks map[string]*DesktopTaskRecord
	seq   int64
}

var globalDesktopTaskManager = NewDesktopTaskManager()

func NewDesktopTaskManager() *DesktopTaskManager {
	return &DesktopTaskManager{tasks: make(map[string]*DesktopTaskRecord)}
}

func (m *DesktopTaskManager) Start(objective, action, targetPath, workspace string) *DesktopTaskRecord {
	if m == nil {
		return nil
	}
	now := time.Now()
	m.mu.Lock()
	m.seq++
	seq := m.seq
	rec := &DesktopTaskRecord{
		ID:         fmt.Sprintf("desktop_%s_%06d", now.Format("060102150405"), seq),
		Objective:  strings.TrimSpace(objective),
		Action:     strings.TrimSpace(action),
		TargetPath: strings.TrimSpace(targetPath),
		Status:     DesktopTaskRunning,
		StartedAt:  now,
		UpdatedAt:  now,
		System:     desktopSystemInfo(workspace),
	}
	m.tasks[rec.ID] = rec
	m.mu.Unlock()
	return rec
}

func (m *DesktopTaskManager) AddStep(taskID, name, status, output string, durationMs int64, err error) {
	if m == nil || taskID == "" {
		return
	}
	step := DesktopTaskStep{
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

func (m *DesktopTaskManager) Update(taskID string, update func(*DesktopTaskRecord)) {
	if m == nil || taskID == "" || update == nil {
		return
	}
	m.mu.Lock()
	if rec := m.tasks[taskID]; rec != nil {
		update(rec)
		rec.UpdatedAt = time.Now()
		rec.DurationMs = rec.UpdatedAt.Sub(rec.StartedAt).Milliseconds()
		rec.EstimatedCredit = estimateDesktopTaskCredits(rec)
		rec.NextActions = desktopTaskNextActions(rec)
		rec.Markdown = formatDesktopTaskMarkdown(rec)
	}
	m.mu.Unlock()
}

func (m *DesktopTaskManager) Get(taskID string) (*DesktopTaskRecord, bool) {
	if m == nil || taskID == "" {
		return nil, false
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	rec, ok := m.tasks[taskID]
	if !ok || rec == nil {
		return nil, false
	}
	return cloneDesktopTaskRecord(rec), true
}

func (m *DesktopTaskManager) List(limit int) []*DesktopTaskRecord {
	if m == nil {
		return nil
	}
	m.mu.RLock()
	out := make([]*DesktopTaskRecord, 0, len(m.tasks))
	for _, rec := range m.tasks {
		out = append(out, cloneDesktopTaskRecord(rec))
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

func cloneDesktopTaskRecord(rec *DesktopTaskRecord) *DesktopTaskRecord {
	if rec == nil {
		return nil
	}
	cp := *rec
	cp.Steps = append([]DesktopTaskStep(nil), rec.Steps...)
	cp.Files = append([]DesktopFileInfo(nil), rec.Files...)
	cp.Interactions = append([]DesktopInteraction(nil), rec.Interactions...)
	cp.Risks = append([]string(nil), rec.Risks...)
	cp.NextActions = append([]string(nil), rec.NextActions...)
	return &cp
}

type DesktopTaskTool struct {
	workspace string
	manager   *DesktopTaskManager
}

func NewDesktopTaskTool(workspace string, manager *DesktopTaskManager) *DesktopTaskTool {
	if manager == nil {
		manager = globalDesktopTaskManager
	}
	return &DesktopTaskTool{workspace: workspace, manager: manager}
}

func (t *DesktopTaskTool) Name() string { return "desktop_task" }

func (t *DesktopTaskTool) Description() string {
	return "Create an auditable local desktop/My Computer task package. Inspects local files, opens local apps/files, captures screenshots, reads active window, and can click/type/send keys with step-by-step user-visible audit output."
}

func (t *DesktopTaskTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"action": map[string]interface{}{
				"type":        "string",
				"enum":        []string{"capabilities", "inspect", "list", "open", "screenshot", "active_window", "click", "type", "key", "scroll", "drag"},
				"description": "capabilities: report the usable platform backends. inspect/list/open: local targets. screenshot/active_window observe the GUI. click/type/key/scroll/drag operate the active desktop session.",
			},
			"objective": map[string]interface{}{
				"type":        "string",
				"description": "User-facing desktop task objective",
			},
			"target_path": map[string]interface{}{
				"type":        "string",
				"description": "Local file/folder/app path. Relative paths resolve under workspace.",
			},
			"open": map[string]interface{}{
				"type":        "boolean",
				"description": "When true, open target_path with the OS default app. Default false except action=open.",
			},
			"capture_screen": map[string]interface{}{
				"type":        "boolean",
				"description": "Capture screen into the task package. Default true.",
			},
			"max_files": map[string]interface{}{
				"type":        "integer",
				"description": "Max directory entries for list/inspect. Default 30.",
			},
			"x": map[string]interface{}{
				"type":        "integer",
				"description": "Screen X coordinate for action=click.",
			},
			"y": map[string]interface{}{
				"type":        "integer",
				"description": "Screen Y coordinate for action=click.",
			},
			"end_x": map[string]interface{}{
				"type":        "integer",
				"description": "Ending screen X coordinate for action=drag.",
			},
			"end_y": map[string]interface{}{
				"type":        "integer",
				"description": "Ending screen Y coordinate for action=drag.",
			},
			"delta_y": map[string]interface{}{
				"type":        "integer",
				"description": "Vertical scroll amount. Positive scrolls down; negative scrolls up.",
			},
			"text": map[string]interface{}{
				"type":        "string",
				"description": "Text to type for action=type. Returned logs are truncated to avoid leaking long content.",
			},
			"key": map[string]interface{}{
				"type":        "string",
				"description": "Key for action=key, e.g. enter, tab, esc, ctrl+s, alt+f4, win+r.",
			},
			"dry_run": map[string]interface{}{
				"type":        "boolean",
				"description": "When true, record the intended desktop action without executing it.",
			},
			"delay_ms": map[string]interface{}{
				"type":        "integer",
				"description": "Delay after click/type/key before screenshot. Default 300.",
			},
			"duration_ms": map[string]interface{}{
				"type":        "integer",
				"description": "Drag duration in milliseconds. Default 300, maximum 5000.",
			},
			"screenshot_file": map[string]interface{}{
				"type":        "string",
				"description": "Optional screenshot output path. Relative paths resolve under the workspace.",
			},
		},
	}
}

func (t *DesktopTaskTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	action, _ := params["action"].(string)
	if action == "" {
		action = "inspect"
	}
	objective, _ := params["objective"].(string)
	targetPath, _ := params["target_path"].(string)
	targetPath = t.resolveTargetPath(targetPath)
	openTarget, _ := params["open"].(bool)
	if action == "open" {
		openTarget = true
	}
	captureScreen := true
	if v, ok := params["capture_screen"].(bool); ok {
		captureScreen = v
	}
	if action == "capabilities" {
		captureScreen = false
	}
	maxFiles := configvalue.Int(params, "max_files", 30)
	if maxFiles <= 0 {
		maxFiles = 30
	}
	if maxFiles > 200 {
		maxFiles = 200
	}
	x := configvalue.Int(params, "x", 0)
	y := configvalue.Int(params, "y", 0)
	endX := configvalue.Int(params, "end_x", 0)
	endY := configvalue.Int(params, "end_y", 0)
	deltaY := configvalue.Int(params, "delta_y", 0)
	text, _ := params["text"].(string)
	key, _ := params["key"].(string)
	dryRun, _ := params["dry_run"].(bool)
	delayMs := configvalue.Int(params, "delay_ms", 300)
	if delayMs < 0 {
		delayMs = 0
	}
	if delayMs > 5000 {
		delayMs = 5000
	}
	durationMs := configvalue.Int(params, "duration_ms", 300)
	if durationMs < 0 {
		durationMs = 0
	}
	if durationMs > 5000 {
		durationMs = 5000
	}
	if action == "capabilities" {
		capabilities := detectDesktopCapabilities()
		data, err := json.MarshalIndent(capabilities, "", "  ")
		if err != nil {
			return "", err
		}
		return string(data), nil
	}

	rec := t.manager.Start(objective, action, targetPath, t.workspaceOrDot())
	taskDir := filepath.Join(t.workspaceOrDot(), ".openkitty", "storage", "desktop_tasks", rec.ID)
	if err := os.MkdirAll(taskDir, 0755); err != nil {
		return "", err
	}

	if action != "screenshot" && action != "active_window" && action != "click" && action != "type" && action != "key" && action != "scroll" && action != "drag" {
		start := time.Now()
		files, out, err := inspectDesktopTarget(targetPath, maxFiles)
		t.manager.AddStep(rec.ID, "inspect", desktopTaskStatusFromErr(err), out, time.Since(start).Milliseconds(), err)
		if err == nil {
			t.manager.Update(rec.ID, func(r *DesktopTaskRecord) { r.Files = files })
		} else {
			t.manager.Update(rec.ID, func(r *DesktopTaskRecord) { r.Risks = append(r.Risks, err.Error()) })
		}
	}

	if openTarget {
		start := time.Now()
		out, err := openDesktopTarget(ctx, targetPath)
		t.manager.AddStep(rec.ID, "open", desktopTaskStatusFromErr(err), out, time.Since(start).Milliseconds(), err)
		if err != nil {
			t.manager.Update(rec.ID, func(r *DesktopTaskRecord) { r.Risks = append(r.Risks, err.Error()) })
		}
	}

	if action == "active_window" || (!dryRun && (action == "click" || action == "type" || action == "key" || action == "scroll" || action == "drag")) {
		start := time.Now()
		title, err := desktopActiveWindow(ctx)
		t.manager.AddStep(rec.ID, "active_window", desktopTaskStatusFromErr(err), title, time.Since(start).Milliseconds(), err)
		if title != "" {
			t.manager.Update(rec.ID, func(r *DesktopTaskRecord) { r.ActiveWindow = title })
		}
		if err != nil {
			t.manager.Update(rec.ID, func(r *DesktopTaskRecord) { r.Risks = append(r.Risks, err.Error()) })
		}
	}

	switch action {
	case "click":
		start := time.Now()
		out, err := desktopClick(ctx, x, y, dryRun)
		t.manager.AddStep(rec.ID, "click", desktopTaskStatusFromErr(err), out, time.Since(start).Milliseconds(), err)
		t.recordInteraction(rec.ID, DesktopInteraction{Action: "click", X: x, Y: y, DryRun: dryRun})
		if err != nil {
			t.manager.Update(rec.ID, func(r *DesktopTaskRecord) { r.Risks = append(r.Risks, err.Error()) })
		}
	case "type":
		start := time.Now()
		out, err := desktopTypeText(ctx, text, dryRun)
		t.manager.AddStep(rec.ID, "type", desktopTaskStatusFromErr(err), out, time.Since(start).Milliseconds(), err)
		t.recordInteraction(rec.ID, DesktopInteraction{Action: "type", Text: textutil.Truncate(text, 200), DryRun: dryRun})
		if err != nil {
			t.manager.Update(rec.ID, func(r *DesktopTaskRecord) { r.Risks = append(r.Risks, err.Error()) })
		}
	case "key":
		start := time.Now()
		out, err := desktopSendKey(ctx, key, dryRun)
		t.manager.AddStep(rec.ID, "key", desktopTaskStatusFromErr(err), out, time.Since(start).Milliseconds(), err)
		t.recordInteraction(rec.ID, DesktopInteraction{Action: "key", Key: strings.TrimSpace(key), DryRun: dryRun})
		if err != nil {
			t.manager.Update(rec.ID, func(r *DesktopTaskRecord) { r.Risks = append(r.Risks, err.Error()) })
		}
	case "scroll":
		start := time.Now()
		out, err := desktopScroll(ctx, x, y, deltaY, dryRun)
		t.manager.AddStep(rec.ID, "scroll", desktopTaskStatusFromErr(err), out, time.Since(start).Milliseconds(), err)
		t.recordInteraction(rec.ID, DesktopInteraction{Action: "scroll", X: x, Y: y, DeltaY: deltaY, DryRun: dryRun})
		if err != nil {
			t.manager.Update(rec.ID, func(r *DesktopTaskRecord) { r.Risks = append(r.Risks, err.Error()) })
		}
	case "drag":
		start := time.Now()
		out, err := desktopDrag(ctx, x, y, endX, endY, durationMs, dryRun)
		t.manager.AddStep(rec.ID, "drag", desktopTaskStatusFromErr(err), out, time.Since(start).Milliseconds(), err)
		t.recordInteraction(rec.ID, DesktopInteraction{Action: "drag", X: x, Y: y, EndX: endX, EndY: endY, DryRun: dryRun})
		if err != nil {
			t.manager.Update(rec.ID, func(r *DesktopTaskRecord) { r.Risks = append(r.Risks, err.Error()) })
		}
	}
	if (action == "click" || action == "type" || action == "key" || action == "scroll" || action == "drag") && delayMs > 0 {
		timer := time.NewTimer(time.Duration(delayMs) * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
		case <-timer.C:
		}
	}

	if captureScreen {
		screenAbs, screenRel := t.screenshotPaths(rec.ID, stringParam(params, "screenshot_file"))
		start := time.Now()
		err := captureDesktopScreenshot(ctx, screenAbs)
		t.manager.AddStep(rec.ID, "screenshot", desktopTaskStatusFromErr(err), screenRel, time.Since(start).Milliseconds(), err)
		if err == nil {
			t.manager.Update(rec.ID, func(r *DesktopTaskRecord) {
				r.ScreenshotPath = screenRel
				r.ScreenshotURL = "/api/files/download?path=" + url.QueryEscape("/"+screenRel)
			})
		} else {
			t.manager.Update(rec.ID, func(r *DesktopTaskRecord) { r.Risks = append(r.Risks, "screen capture failed: "+err.Error()) })
		}
	}

	t.manager.Update(rec.ID, func(r *DesktopTaskRecord) {
		if captureScreen && r.ScreenshotPath == "" {
			r.Status = DesktopTaskFailed
			r.Error = "screen capture failed"
			return
		}
		if action == "click" || action == "type" || action == "key" || action == "scroll" || action == "drag" {
			for _, step := range r.Steps {
				if step.Name == action && step.Status == "failed" {
					r.Status = DesktopTaskFailed
					r.Error = step.Error
					return
				}
			}
		}
		if r.Status != DesktopTaskFailed {
			r.Status = DesktopTaskCompleted
		}
	})
	return formatDesktopTaskMarkdown(mustGetDesktopTask(t.manager, rec.ID)), nil
}

func (t *DesktopTaskTool) screenshotPaths(taskID, requested string) (string, string) {
	workspace, err := filepath.Abs(t.workspaceOrDot())
	if err != nil {
		workspace = filepath.Clean(t.workspaceOrDot())
	}
	requested = strings.TrimSpace(requested)
	if requested == "" {
		rel := filepath.ToSlash(filepath.Join(".openkitty", "storage", "desktop_tasks", taskID, "screen.png"))
		return filepath.Join(workspace, filepath.FromSlash(rel)), rel
	}
	abs := requested
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(workspace, abs)
	}
	abs = filepath.Clean(abs)
	if rel, relErr := filepath.Rel(workspace, abs); relErr == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return abs, filepath.ToSlash(rel)
	}
	return abs, filepath.ToSlash(abs)
}

func (t *DesktopTaskTool) recordInteraction(taskID string, interaction DesktopInteraction) {
	if interaction.At.IsZero() {
		interaction.At = time.Now()
	}
	t.manager.Update(taskID, func(r *DesktopTaskRecord) {
		interaction.WindowTitle = r.ActiveWindow
		r.Interactions = append(r.Interactions, interaction)
	})
}

func (t *DesktopTaskTool) workspaceOrDot() string {
	if strings.TrimSpace(t.workspace) == "" {
		return "."
	}
	return t.workspace
}

func (t *DesktopTaskTool) resolveTargetPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return t.workspaceOrDot()
	}
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Join(t.workspaceOrDot(), path)
}

func desktopSystemInfo(workspace string) DesktopSystemInfo {
	host, _ := os.Hostname()
	home, _ := os.UserHomeDir()
	return DesktopSystemInfo{
		OS:        runtime.GOOS,
		Arch:      runtime.GOARCH,
		Hostname:  host,
		Workspace: workspace,
		Home:      home,
	}
}

func inspectDesktopTarget(path string, maxFiles int) ([]DesktopFileInfo, string, error) {
	if strings.TrimSpace(path) == "" {
		path = "."
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, "", err
	}
	files := []DesktopFileInfo{desktopFileInfo(path, info)}
	var b strings.Builder
	b.WriteString(fmt.Sprintf("Target: %s\n", path))
	b.WriteString(fmt.Sprintf("Type: %s\n", map[bool]string{true: "directory", false: "file"}[info.IsDir()]))
	if !info.IsDir() {
		b.WriteString(fmt.Sprintf("Size: %d bytes\n", info.Size()))
		return files, b.String(), nil
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return files, b.String(), err
	}
	count := 0
	for _, entry := range entries {
		if count >= maxFiles {
			break
		}
		childPath := filepath.Join(path, entry.Name())
		childInfo, err := entry.Info()
		if err != nil {
			continue
		}
		files = append(files, desktopFileInfo(childPath, childInfo))
		count++
	}
	b.WriteString(fmt.Sprintf("Entries listed: %d/%d\n", count, len(entries)))
	for _, f := range files[1:] {
		kind := "file"
		if f.IsDir {
			kind = "dir"
		}
		b.WriteString(fmt.Sprintf("- [%s] %s (%d bytes)\n", kind, f.Name, f.Size))
	}
	return files, b.String(), nil
}

func desktopFileInfo(path string, info os.FileInfo) DesktopFileInfo {
	return DesktopFileInfo{
		Path:    filepath.Clean(path),
		Name:    info.Name(),
		IsDir:   info.IsDir(),
		Size:    info.Size(),
		ModTime: info.ModTime(),
	}
}

func openDesktopTarget(ctx context.Context, path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("target_path is required for open")
	}
	if _, err := os.Stat(path); err != nil {
		return "", err
	}
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.CommandContext(ctx, "rundll32", "url.dll,FileProtocolHandler", path)
	case "darwin":
		cmd = exec.CommandContext(ctx, "open", path)
	default:
		cmd = exec.CommandContext(ctx, "xdg-open", path)
	}
	if err := cmd.Start(); err != nil {
		return "", err
	}
	return "opened: " + path, nil
}

func captureDesktopScreenshot(ctx context.Context, output string) error {
	if err := os.MkdirAll(filepath.Dir(output), 0755); err != nil {
		return err
	}
	if runtime.GOOS == "linux" {
		commands := [][]string{}
		if strings.TrimSpace(os.Getenv("WAYLAND_DISPLAY")) != "" {
			commands = append(commands,
				[]string{"grim", output},
				[]string{"gnome-screenshot", "-f", output},
				[]string{"spectacle", "-b", "-n", "-o", output},
			)
		}
		commands = append(commands,
			[]string{"gnome-screenshot", "-f", output},
			[]string{"scrot", output},
			[]string{"maim", output},
			[]string{"import", "-window", "root", output},
		)
		for _, candidate := range commands {
			if _, err := exec.LookPath(candidate[0]); err != nil {
				continue
			}
			if err := exec.CommandContext(ctx, candidate[0], candidate[1:]...).Run(); err == nil {
				return nil
			}
		}
	}
	if runtime.GOOS == "darwin" {
		if _, err := exec.LookPath("screencapture"); err == nil {
			if err := exec.CommandContext(ctx, "screencapture", "-x", output).Run(); err == nil {
				return nil
			}
		}
	}
	if python := firstPythonWithModules("PIL.ImageGrab"); python != "" {
		script := fmt.Sprintf(`
from PIL import ImageGrab
img = ImageGrab.grab()
img.save(r'%s')
`, strings.ReplaceAll(output, "\\", "\\\\"))
		if err := exec.CommandContext(ctx, python, "-c", script).Run(); err == nil {
			return nil
		}
	}
	if _, err := exec.LookPath("ffmpeg"); err == nil {
		if runtime.GOOS == "windows" {
			return exec.CommandContext(ctx, "ffmpeg", "-f", "gdigrab", "-i", "desktop", "-vframes", "1", "-y", output).Run()
		}
	}
	return fmt.Errorf("no desktop screenshot backend available (Windows: Pillow/ffmpeg; Linux: grim, gnome-screenshot, spectacle, scrot, maim, ImageMagick, or Pillow)")
}

func desktopActiveWindow(ctx context.Context) (string, error) {
	switch runtime.GOOS {
	case "windows":
		script := `
Add-Type @"
using System;
using System.Text;
using System.Runtime.InteropServices;
public class Win32Window {
  [DllImport("user32.dll")] public static extern IntPtr GetForegroundWindow();
  [DllImport("user32.dll", CharSet = CharSet.Unicode)] public static extern int GetWindowText(IntPtr hWnd, StringBuilder text, int count);
}
"@
$h = [Win32Window]::GetForegroundWindow()
$b = New-Object System.Text.StringBuilder 512
[void][Win32Window]::GetWindowText($h, $b, $b.Capacity)
$b.ToString()
`
		out, err := exec.CommandContext(ctx, "powershell", "-NoProfile", "-ExecutionPolicy", "Bypass", "-Command", script).CombinedOutput()
		if err != nil {
			return "", fmt.Errorf("active window failed: %w: %s", err, strings.TrimSpace(string(out)))
		}
		return strings.TrimSpace(string(out)), nil
	case "linux":
		if _, err := exec.LookPath("xdotool"); err == nil {
			out, runErr := exec.CommandContext(ctx, "xdotool", "getactivewindow", "getwindowname").CombinedOutput()
			if runErr == nil {
				return strings.TrimSpace(string(out)), nil
			}
		}
		return linuxActiveWindowWithXProp(ctx)
	case "darwin":
		out, err := exec.CommandContext(ctx, "osascript", "-e", `tell application "System Events" to get name of first application process whose frontmost is true`).CombinedOutput()
		if err != nil {
			return "", fmt.Errorf("active window failed: %w: %s", err, strings.TrimSpace(string(out)))
		}
		return strings.TrimSpace(string(out)), nil
	default:
		return "", fmt.Errorf("active window inspection is not implemented on %s", runtime.GOOS)
	}
}

func desktopClick(ctx context.Context, x, y int, dryRun bool) (string, error) {
	if dryRun {
		return fmt.Sprintf("dry-run click at (%d,%d)", x, y), nil
	}
	switch runtime.GOOS {
	case "windows":
		script := `
param([int]$x, [int]$y)
Add-Type @"
using System;
using System.Runtime.InteropServices;
public class MouseOps {
  [DllImport("user32.dll")] public static extern bool SetCursorPos(int X, int Y);
  [DllImport("user32.dll")] public static extern void mouse_event(uint dwFlags, uint dx, uint dy, uint dwData, UIntPtr dwExtraInfo);
}
"@
[void][MouseOps]::SetCursorPos($x, $y)
Start-Sleep -Milliseconds 60
[MouseOps]::mouse_event(0x0002, 0, 0, 0, [UIntPtr]::Zero)
Start-Sleep -Milliseconds 40
[MouseOps]::mouse_event(0x0004, 0, 0, 0, [UIntPtr]::Zero)
"clicked: $x,$y"
`
		out, err := exec.CommandContext(ctx, "powershell", "-NoProfile", "-ExecutionPolicy", "Bypass", "-Command", script, strconv.Itoa(x), strconv.Itoa(y)).CombinedOutput()
		if err != nil {
			return "", fmt.Errorf("desktop click failed: %w: %s", err, strings.TrimSpace(string(out)))
		}
		return strings.TrimSpace(string(out)), nil
	case "linux":
		if _, err := exec.LookPath("xdotool"); err == nil {
			out, runErr := exec.CommandContext(ctx, "xdotool", "mousemove", "--sync", strconv.Itoa(x), strconv.Itoa(y), "click", "1").CombinedOutput()
			if runErr == nil {
				return fmt.Sprintf("clicked: %d,%d via xdotool", x, y), nil
			}
			return "", fmt.Errorf("desktop click with xdotool failed: %w: %s", runErr, strings.TrimSpace(string(out)))
		}
		if _, err := exec.LookPath("ydotool"); err == nil {
			if out, runErr := exec.CommandContext(ctx, "ydotool", "mousemove", "--absolute", "-x", strconv.Itoa(x), "-y", strconv.Itoa(y)).CombinedOutput(); runErr != nil {
				return "", fmt.Errorf("desktop pointer move with ydotool failed: %w: %s", runErr, strings.TrimSpace(string(out)))
			}
			if out, runErr := exec.CommandContext(ctx, "ydotool", "click", "0xC0").CombinedOutput(); runErr != nil {
				return "", fmt.Errorf("desktop click with ydotool failed: %w: %s", runErr, strings.TrimSpace(string(out)))
			}
			return fmt.Sprintf("clicked: %d,%d via ydotool", x, y), nil
		}
		return runPyAutoGUI(ctx, `pyautogui.click(int(sys.argv[1]), int(sys.argv[2]))`, strconv.Itoa(x), strconv.Itoa(y))
	case "darwin":
		return runPyAutoGUI(ctx, `pyautogui.click(int(sys.argv[1]), int(sys.argv[2]))`, strconv.Itoa(x), strconv.Itoa(y))
	default:
		return "", fmt.Errorf("desktop click is not implemented on %s", runtime.GOOS)
	}
}

func desktopTypeText(ctx context.Context, text string, dryRun bool) (string, error) {
	text = strings.TrimRight(text, "\r\n")
	if text == "" {
		return "", fmt.Errorf("text is required for desktop type")
	}
	if dryRun {
		return fmt.Sprintf("dry-run type %d character(s)", len([]rune(text))), nil
	}
	switch runtime.GOOS {
	case "windows":
		script := `
param([string]$text)
Add-Type -AssemblyName System.Windows.Forms
$hadClipboard = $true
$old = ""
try { $old = Get-Clipboard -Raw -ErrorAction Stop } catch { $hadClipboard = $false }
Set-Clipboard -Value $text
Start-Sleep -Milliseconds 80
[System.Windows.Forms.SendKeys]::SendWait("^v")
Start-Sleep -Milliseconds 80
if ($hadClipboard) { Set-Clipboard -Value $old }
"typed: $($text.Length) chars"
`
		out, err := exec.CommandContext(ctx, "powershell", "-NoProfile", "-STA", "-ExecutionPolicy", "Bypass", "-Command", script, text).CombinedOutput()
		if err != nil {
			return "", fmt.Errorf("desktop type failed: %w: %s", err, strings.TrimSpace(string(out)))
		}
		return strings.TrimSpace(string(out)), nil
	case "linux":
		return linuxTypeText(ctx, text)
	case "darwin":
		return runPyAutoGUI(ctx, `pyperclip.copy(sys.argv[1]); pyautogui.hotkey('command', 'v')`, text)
	default:
		return "", fmt.Errorf("desktop type is not implemented on %s", runtime.GOOS)
	}
}

func desktopSendKey(ctx context.Context, key string, dryRun bool) (string, error) {
	key = strings.TrimSpace(strings.ToLower(key))
	if key == "" {
		return "", fmt.Errorf("key is required for desktop key")
	}
	if dryRun {
		return "dry-run key: " + key, nil
	}
	switch runtime.GOOS {
	case "windows":
		sendKey, ok := windowsSendKeysToken(key)
		if !ok {
			return "", fmt.Errorf("unsupported key %q", key)
		}
		script := `
param([string]$key)
Add-Type -AssemblyName System.Windows.Forms
[System.Windows.Forms.SendKeys]::SendWait($key)
"sent key: $key"
`
		out, err := exec.CommandContext(ctx, "powershell", "-NoProfile", "-STA", "-ExecutionPolicy", "Bypass", "-Command", script, sendKey).CombinedOutput()
		if err != nil {
			return "", fmt.Errorf("desktop key failed: %w: %s", err, strings.TrimSpace(string(out)))
		}
		return strings.TrimSpace(string(out)), nil
	case "linux":
		return linuxSendKey(ctx, key)
	case "darwin":
		return runPyAutoGUI(ctx, `pyautogui.hotkey(*sys.argv[1].split('+'))`, key)
	default:
		return "", fmt.Errorf("desktop key is not implemented on %s", runtime.GOOS)
	}
}

func desktopScroll(ctx context.Context, x, y, deltaY int, dryRun bool) (string, error) {
	if deltaY == 0 {
		deltaY = 500
	}
	if dryRun {
		return fmt.Sprintf("dry-run scroll %d at (%d,%d)", deltaY, x, y), nil
	}
	switch runtime.GOOS {
	case "windows":
		script := `
param([int]$x, [int]$y, [int]$delta)
Add-Type @"
using System;
using System.Runtime.InteropServices;
public class MouseWheelOps {
  [DllImport("user32.dll")] public static extern bool SetCursorPos(int X, int Y);
  [DllImport("user32.dll")] public static extern void mouse_event(uint flags, uint dx, uint dy, int data, UIntPtr extra);
}
"@
if ($x -ne 0 -or $y -ne 0) { [void][MouseWheelOps]::SetCursorPos($x, $y) }
[MouseWheelOps]::mouse_event(0x0800, 0, 0, -$delta, [UIntPtr]::Zero)
"scrolled: $delta"
`
		out, err := exec.CommandContext(ctx, "powershell", "-NoProfile", "-ExecutionPolicy", "Bypass", "-Command", script, strconv.Itoa(x), strconv.Itoa(y), strconv.Itoa(deltaY)).CombinedOutput()
		if err != nil {
			return "", fmt.Errorf("desktop scroll failed: %w: %s", err, strings.TrimSpace(string(out)))
		}
		return strings.TrimSpace(string(out)), nil
	case "linux":
		if _, err := exec.LookPath("xdotool"); err == nil {
			if x != 0 || y != 0 {
				if out, moveErr := exec.CommandContext(ctx, "xdotool", "mousemove", "--sync", strconv.Itoa(x), strconv.Itoa(y)).CombinedOutput(); moveErr != nil {
					return "", fmt.Errorf("desktop scroll pointer move failed: %w: %s", moveErr, strings.TrimSpace(string(out)))
				}
			}
			button := "5"
			if deltaY < 0 {
				button = "4"
			}
			clicks := absInt(deltaY) / 120
			if clicks < 1 {
				clicks = 1
			}
			if clicks > 20 {
				clicks = 20
			}
			for i := 0; i < clicks; i++ {
				if out, clickErr := exec.CommandContext(ctx, "xdotool", "click", button).CombinedOutput(); clickErr != nil {
					return "", fmt.Errorf("desktop scroll failed: %w: %s", clickErr, strings.TrimSpace(string(out)))
				}
			}
			return fmt.Sprintf("scrolled: %d via xdotool", deltaY), nil
		}
		return runPyAutoGUI(ctx, `pyautogui.moveTo(int(sys.argv[1]), int(sys.argv[2])) if int(sys.argv[1]) or int(sys.argv[2]) else None; pyautogui.scroll(-int(sys.argv[3]))`, strconv.Itoa(x), strconv.Itoa(y), strconv.Itoa(deltaY/120))
	case "darwin":
		return runPyAutoGUI(ctx, `pyautogui.moveTo(int(sys.argv[1]), int(sys.argv[2])) if int(sys.argv[1]) or int(sys.argv[2]) else None; pyautogui.scroll(-int(sys.argv[3]))`, strconv.Itoa(x), strconv.Itoa(y), strconv.Itoa(deltaY/120))
	default:
		return "", fmt.Errorf("desktop scroll is not implemented on %s", runtime.GOOS)
	}
}

func desktopDrag(ctx context.Context, x, y, endX, endY, durationMS int, dryRun bool) (string, error) {
	if dryRun {
		return fmt.Sprintf("dry-run drag (%d,%d) -> (%d,%d)", x, y, endX, endY), nil
	}
	switch runtime.GOOS {
	case "windows":
		script := `
param([int]$x, [int]$y, [int]$endX, [int]$endY, [int]$duration)
Add-Type @"
using System;
using System.Runtime.InteropServices;
public class MouseDragOps {
  [DllImport("user32.dll")] public static extern bool SetCursorPos(int X, int Y);
  [DllImport("user32.dll")] public static extern void mouse_event(uint flags, uint dx, uint dy, uint data, UIntPtr extra);
}
"@
[void][MouseDragOps]::SetCursorPos($x, $y)
[MouseDragOps]::mouse_event(0x0002, 0, 0, 0, [UIntPtr]::Zero)
$steps = 10
for ($i = 1; $i -le $steps; $i++) {
  $nx = [int]($x + (($endX - $x) * $i / $steps))
  $ny = [int]($y + (($endY - $y) * $i / $steps))
  [void][MouseDragOps]::SetCursorPos($nx, $ny)
  if ($duration -gt 0) { Start-Sleep -Milliseconds ([Math]::Max(1, [int]($duration / $steps))) }
}
[MouseDragOps]::mouse_event(0x0004, 0, 0, 0, [UIntPtr]::Zero)
"dragged: $x,$y -> $endX,$endY"
`
		out, err := exec.CommandContext(ctx, "powershell", "-NoProfile", "-ExecutionPolicy", "Bypass", "-Command", script, strconv.Itoa(x), strconv.Itoa(y), strconv.Itoa(endX), strconv.Itoa(endY), strconv.Itoa(durationMS)).CombinedOutput()
		if err != nil {
			return "", fmt.Errorf("desktop drag failed: %w: %s", err, strings.TrimSpace(string(out)))
		}
		return strings.TrimSpace(string(out)), nil
	case "linux":
		if _, err := exec.LookPath("xdotool"); err == nil {
			commands := [][]string{
				{"mousemove", "--sync", strconv.Itoa(x), strconv.Itoa(y)},
				{"mousedown", "1"},
				{"mousemove", "--sync", strconv.Itoa(endX), strconv.Itoa(endY)},
				{"mouseup", "1"},
			}
			for _, args := range commands {
				if out, runErr := exec.CommandContext(ctx, "xdotool", args...).CombinedOutput(); runErr != nil {
					return "", fmt.Errorf("desktop drag failed: %w: %s", runErr, strings.TrimSpace(string(out)))
				}
			}
			return fmt.Sprintf("dragged: %d,%d -> %d,%d via xdotool", x, y, endX, endY), nil
		}
		return runPyAutoGUI(ctx, `pyautogui.moveTo(int(sys.argv[1]), int(sys.argv[2])); pyautogui.dragTo(int(sys.argv[3]), int(sys.argv[4]), duration=float(sys.argv[5])/1000.0, button='left')`, strconv.Itoa(x), strconv.Itoa(y), strconv.Itoa(endX), strconv.Itoa(endY), strconv.Itoa(durationMS))
	case "darwin":
		return runPyAutoGUI(ctx, `pyautogui.moveTo(int(sys.argv[1]), int(sys.argv[2])); pyautogui.dragTo(int(sys.argv[3]), int(sys.argv[4]), duration=float(sys.argv[5])/1000.0, button='left')`, strconv.Itoa(x), strconv.Itoa(y), strconv.Itoa(endX), strconv.Itoa(endY), strconv.Itoa(durationMS))
	default:
		return "", fmt.Errorf("desktop drag is not implemented on %s", runtime.GOOS)
	}
}

func detectDesktopCapabilities() DesktopCapabilities {
	capabilities := DesktopCapabilities{OS: runtime.GOOS}
	switch runtime.GOOS {
	case "windows":
		capabilities.Screenshot = "Pillow ImageGrab or ffmpeg gdigrab"
		capabilities.ActiveWindow = "Win32"
		capabilities.Pointer = "Win32"
		capabilities.Keyboard = "Windows SendKeys"
		capabilities.SupportsClick = true
		capabilities.SupportsType = true
		capabilities.SupportsScroll = true
		capabilities.SupportsDrag = true
	case "linux":
		capabilities.DisplayServer = linuxDisplayServer()
		capabilities.SessionType = strings.TrimSpace(os.Getenv("XDG_SESSION_TYPE"))
		capabilities.Screenshot = firstExecutable("grim", "gnome-screenshot", "spectacle", "scrot", "maim", "import")
		if capabilities.Screenshot == "" {
			capabilities.Screenshot = firstPythonWithModules("PIL.ImageGrab")
		}
		capabilities.ActiveWindow = firstExecutable("xdotool", "xprop")
		pythonInput := firstPythonWithModules("pyautogui", "pyperclip")
		capabilities.Pointer = firstExecutable("xdotool", "ydotool")
		if capabilities.Pointer == "" {
			capabilities.Pointer = pythonInput
		}
		capabilities.Keyboard = firstExecutable("xdotool", "wtype")
		if capabilities.Keyboard == "" {
			capabilities.Keyboard = pythonInput
		}
		capabilities.SupportsClick = capabilities.Pointer != ""
		capabilities.SupportsType = capabilities.Keyboard != ""
		capabilities.SupportsScroll = capabilities.Pointer == "xdotool" || (pythonInput != "" && capabilities.Pointer == pythonInput)
		capabilities.SupportsDrag = capabilities.Pointer == "xdotool" || (pythonInput != "" && capabilities.Pointer == pythonInput)
		if capabilities.DisplayServer == "wayland" {
			capabilities.Notes = append(capabilities.Notes, "Wayland desktop control depends on compositor-supported tools such as grim/wtype/ydotool; Chrome CDP browser control remains fully available.")
		}
		if capabilities.Pointer == "" || capabilities.Keyboard == "" {
			capabilities.Notes = append(capabilities.Notes, "Install xdotool on X11, or compositor-compatible Wayland tools, to enable desktop input.")
		}
	case "darwin":
		capabilities.Screenshot = firstExecutable("screencapture")
		if capabilities.Screenshot == "" {
			capabilities.Screenshot = firstPythonWithModules("PIL.ImageGrab")
		}
		capabilities.ActiveWindow = "osascript"
		pythonInput := firstPythonWithModules("pyautogui", "pyperclip")
		capabilities.Pointer = pythonInput
		capabilities.Keyboard = pythonInput
		capabilities.SupportsClick = capabilities.Pointer != ""
		capabilities.SupportsType = capabilities.Keyboard != ""
		capabilities.SupportsScroll = capabilities.Pointer != ""
		capabilities.SupportsDrag = capabilities.Pointer != ""
	}
	return capabilities
}

func linuxDisplayServer() string {
	if strings.TrimSpace(os.Getenv("WAYLAND_DISPLAY")) != "" || strings.EqualFold(strings.TrimSpace(os.Getenv("XDG_SESSION_TYPE")), "wayland") {
		return "wayland"
	}
	if strings.TrimSpace(os.Getenv("DISPLAY")) != "" || strings.EqualFold(strings.TrimSpace(os.Getenv("XDG_SESSION_TYPE")), "x11") {
		return "x11"
	}
	return "headless"
}

func firstExecutable(names ...string) string {
	for _, name := range names {
		if _, err := exec.LookPath(name); err == nil {
			return name
		}
	}
	return ""
}

func firstPythonWithModules(modules ...string) string {
	imports := make([]string, 0, len(modules))
	for _, module := range modules {
		module = strings.TrimSpace(module)
		if module != "" {
			imports = append(imports, "import "+module)
		}
	}
	for _, python := range []string{"python3", "python"} {
		if _, err := exec.LookPath(python); err != nil {
			continue
		}
		if err := exec.Command(python, "-c", strings.Join(imports, "\n")).Run(); err == nil {
			return python
		}
	}
	return ""
}

func linuxActiveWindowWithXProp(ctx context.Context) (string, error) {
	if _, err := exec.LookPath("xprop"); err != nil {
		return "", fmt.Errorf("no Linux active-window backend available (install xdotool on X11; Wayland compositors may restrict global active-window inspection)")
	}
	root, err := exec.CommandContext(ctx, "xprop", "-root", "_NET_ACTIVE_WINDOW").CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("read Linux active window ID: %w: %s", err, strings.TrimSpace(string(root)))
	}
	fields := strings.Fields(string(root))
	if len(fields) == 0 {
		return "", errors.New("Linux active window ID was empty")
	}
	id := strings.TrimSpace(fields[len(fields)-1])
	out, err := exec.CommandContext(ctx, "xprop", "-id", id, "_NET_WM_NAME").CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("read Linux active window title: %w: %s", err, strings.TrimSpace(string(out)))
	}
	title := strings.TrimSpace(string(out))
	if index := strings.Index(title, "="); index >= 0 {
		title = strings.TrimSpace(title[index+1:])
	}
	return strings.Trim(title, `"`), nil
}

func linuxTypeText(ctx context.Context, text string) (string, error) {
	if _, copyErr := exec.LookPath("wl-copy"); copyErr == nil {
		if _, typeErr := exec.LookPath("wtype"); typeErr == nil {
			old, _ := exec.CommandContext(ctx, "wl-paste", "-n").Output()
			if err := runCommandWithInput(ctx, text, "wl-copy"); err != nil {
				return "", err
			}
			_, keyErr := linuxSendKey(ctx, "ctrl+v")
			if len(old) > 0 {
				_ = runCommandWithInput(ctx, string(old), "wl-copy")
			}
			if keyErr != nil {
				return "", keyErr
			}
			return fmt.Sprintf("typed: %d chars via Wayland clipboard", len([]rune(text))), nil
		}
	}
	if _, copyErr := exec.LookPath("xclip"); copyErr == nil {
		if _, typeErr := exec.LookPath("xdotool"); typeErr == nil {
			old, _ := exec.CommandContext(ctx, "xclip", "-selection", "clipboard", "-o").Output()
			if err := runCommandWithInput(ctx, text, "xclip", "-selection", "clipboard"); err != nil {
				return "", err
			}
			_, keyErr := linuxSendKey(ctx, "ctrl+v")
			if len(old) > 0 {
				_ = runCommandWithInput(ctx, string(old), "xclip", "-selection", "clipboard")
			}
			if keyErr != nil {
				return "", keyErr
			}
			return fmt.Sprintf("typed: %d chars via X11 clipboard", len([]rune(text))), nil
		}
	}
	if _, err := exec.LookPath("xdotool"); err == nil {
		out, runErr := exec.CommandContext(ctx, "xdotool", "type", "--clearmodifiers", "--delay", "1", "--", text).CombinedOutput()
		if runErr != nil {
			return "", fmt.Errorf("desktop type with xdotool failed: %w: %s", runErr, strings.TrimSpace(string(out)))
		}
		return fmt.Sprintf("typed: %d chars via xdotool", len([]rune(text))), nil
	}
	if _, err := exec.LookPath("wtype"); err == nil {
		out, runErr := exec.CommandContext(ctx, "wtype", "--", text).CombinedOutput()
		if runErr != nil {
			return "", fmt.Errorf("desktop type with wtype failed: %w: %s", runErr, strings.TrimSpace(string(out)))
		}
		return fmt.Sprintf("typed: %d chars via wtype", len([]rune(text))), nil
	}
	return runPyAutoGUI(ctx, `pyautogui.write(sys.argv[1], interval=0.001)`, text)
}

func linuxSendKey(ctx context.Context, key string) (string, error) {
	if _, err := exec.LookPath("xdotool"); err == nil {
		translated, ok := linuxXDoToolKey(key)
		if !ok {
			return "", fmt.Errorf("unsupported Linux key %q", key)
		}
		out, runErr := exec.CommandContext(ctx, "xdotool", "key", "--clearmodifiers", translated).CombinedOutput()
		if runErr != nil {
			return "", fmt.Errorf("desktop key with xdotool failed: %w: %s", runErr, strings.TrimSpace(string(out)))
		}
		return "sent key: " + key + " via xdotool", nil
	}
	if _, err := exec.LookPath("wtype"); err == nil {
		args, ok := linuxWtypeKeyArgs(key)
		if !ok {
			return "", fmt.Errorf("unsupported Wayland key %q", key)
		}
		out, runErr := exec.CommandContext(ctx, "wtype", args...).CombinedOutput()
		if runErr != nil {
			return "", fmt.Errorf("desktop key with wtype failed: %w: %s", runErr, strings.TrimSpace(string(out)))
		}
		return "sent key: " + key + " via wtype", nil
	}
	return runPyAutoGUI(ctx, `pyautogui.hotkey(*sys.argv[1].split('+'))`, key)
}

func linuxXDoToolKey(key string) (string, bool) {
	parts := strings.Split(strings.ToLower(strings.TrimSpace(key)), "+")
	if len(parts) == 0 {
		return "", false
	}
	keyNames := map[string]string{
		"enter": "Return", "return": "Return", "tab": "Tab", "esc": "Escape", "escape": "Escape",
		"backspace": "BackSpace", "delete": "Delete", "del": "Delete", "space": "space",
		"up": "Up", "down": "Down", "left": "Left", "right": "Right", "home": "Home", "end": "End",
		"pageup": "Prior", "pgup": "Prior", "pagedown": "Next", "pgdn": "Next",
	}
	modifiers := map[string]string{"ctrl": "ctrl", "control": "ctrl", "alt": "alt", "shift": "shift", "win": "super", "super": "super", "meta": "super"}
	translated := make([]string, 0, len(parts))
	for index, part := range parts {
		part = strings.TrimSpace(part)
		if index < len(parts)-1 {
			modifier, ok := modifiers[part]
			if !ok {
				return "", false
			}
			translated = append(translated, modifier)
			continue
		}
		if named, ok := keyNames[part]; ok {
			translated = append(translated, named)
		} else if len([]rune(part)) == 1 || isFunctionKey(part) {
			translated = append(translated, part)
		} else {
			return "", false
		}
	}
	return strings.Join(translated, "+"), true
}

func linuxWtypeKeyArgs(key string) ([]string, bool) {
	parts := strings.Split(strings.ToLower(strings.TrimSpace(key)), "+")
	if len(parts) == 0 {
		return nil, false
	}
	modifiers := map[string]string{"ctrl": "ctrl", "control": "ctrl", "alt": "alt", "shift": "shift", "win": "logo", "super": "logo", "meta": "logo"}
	pressed := []string{}
	args := []string{}
	for _, part := range parts[:len(parts)-1] {
		modifier, ok := modifiers[strings.TrimSpace(part)]
		if !ok {
			return nil, false
		}
		args = append(args, "-M", modifier)
		pressed = append(pressed, modifier)
	}
	translated, ok := linuxXDoToolKey(parts[len(parts)-1])
	if !ok || strings.Contains(translated, "+") {
		return nil, false
	}
	args = append(args, "-k", translated)
	for index := len(pressed) - 1; index >= 0; index-- {
		args = append(args, "-m", pressed[index])
	}
	return args, true
}

func runCommandWithInput(ctx context.Context, input, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = strings.NewReader(input)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%s failed: %w: %s", name, err, strings.TrimSpace(string(out)))
	}
	return nil
}

func runPyAutoGUI(ctx context.Context, statement string, args ...string) (string, error) {
	python := firstExecutable("python3", "python")
	if python == "" {
		return "", errors.New("no desktop input backend available; install xdotool on X11, compositor-compatible Wayland tools, or Python pyautogui")
	}
	script := "import sys\ntry:\n import pyautogui\n import pyperclip\nexcept Exception as exc:\n raise SystemExit('pyautogui backend unavailable: %s' % exc)\n" + statement
	cmdArgs := append([]string{"-c", script}, args...)
	out, err := exec.CommandContext(ctx, python, cmdArgs...).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("desktop pyautogui backend failed: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return "desktop action completed via pyautogui", nil
}

func isFunctionKey(key string) bool {
	if !strings.HasPrefix(key, "f") {
		return false
	}
	n, err := strconv.Atoi(strings.TrimPrefix(key, "f"))
	return err == nil && n >= 1 && n <= 12
}

func absInt(value int) int {
	if value < 0 {
		return -value
	}
	return value
}

func stringParam(params map[string]interface{}, name string) string {
	value, _ := params[name].(string)
	return value
}

func windowsSendKeysToken(key string) (string, bool) {
	switch key {
	case "enter", "return":
		return "{ENTER}", true
	case "tab":
		return "{TAB}", true
	case "esc", "escape":
		return "{ESC}", true
	case "backspace":
		return "{BACKSPACE}", true
	case "delete", "del":
		return "{DELETE}", true
	case "space":
		return " ", true
	case "up":
		return "{UP}", true
	case "down":
		return "{DOWN}", true
	case "left":
		return "{LEFT}", true
	case "right":
		return "{RIGHT}", true
	case "home":
		return "{HOME}", true
	case "end":
		return "{END}", true
	case "pageup", "pgup":
		return "{PGUP}", true
	case "pagedown", "pgdn":
		return "{PGDN}", true
	case "ctrl+s":
		return "^s", true
	case "ctrl+a":
		return "^a", true
	case "ctrl+c":
		return "^c", true
	case "ctrl+v":
		return "^v", true
	case "ctrl+x":
		return "^x", true
	case "ctrl+z":
		return "^z", true
	case "ctrl+y":
		return "^y", true
	case "alt+f4":
		return "%{F4}", true
	case "win+r":
		return "^{ESC}r", true
	default:
		if strings.HasPrefix(key, "f") {
			n, err := strconv.Atoi(strings.TrimPrefix(key, "f"))
			if err == nil && n >= 1 && n <= 12 {
				return fmt.Sprintf("{F%d}", n), true
			}
		}
		if len([]rune(key)) == 1 {
			return key, true
		}
	}
	return "", false
}

func mustGetDesktopTask(manager *DesktopTaskManager, taskID string) *DesktopTaskRecord {
	rec, ok := manager.Get(taskID)
	if !ok {
		return &DesktopTaskRecord{ID: taskID, Status: DesktopTaskFailed, Error: "desktop task record missing"}
	}
	return rec
}

func estimateDesktopTaskCredits(rec *DesktopTaskRecord) int {
	if rec == nil {
		return 0
	}
	credits := 1
	for _, step := range rec.Steps {
		if step.Status != "" {
			credits++
		}
	}
	return credits
}

func desktopTaskNextActions(rec *DesktopTaskRecord) []string {
	if rec == nil {
		return nil
	}
	if rec.Status == DesktopTaskFailed {
		return []string{"Review the failed desktop task step and retry after fixing the local blocker."}
	}
	actions := []string{}
	if rec.ScreenshotURL != "" {
		actions = append(actions, "Review the desktop screenshot before taking further local actions.")
	}
	if len(rec.Interactions) > 0 {
		actions = append(actions, "Review the recorded desktop interaction log and continue from the latest screen state.")
	}
	if rec.TargetPath != "" {
		actions = append(actions, "Use read_file/list_dir or desktop_task with open=true for the target path when appropriate.")
	}
	if len(actions) == 0 {
		actions = append(actions, "Inspect the desktop task steps for next actions.")
	}
	return actions
}

func formatDesktopTaskMarkdown(rec *DesktopTaskRecord) string {
	if rec == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString("## Desktop task\n\n")
	b.WriteString("- Task ID: " + rec.ID + "\n")
	b.WriteString("- Status: " + string(rec.Status) + "\n")
	b.WriteString("- Action: " + displayValue(rec.Action) + "\n")
	b.WriteString("- Target: " + displayValue(rec.TargetPath) + "\n")
	b.WriteString("- System: " + rec.System.OS + "/" + rec.System.Arch + "\n")
	b.WriteString("- Estimated credits: " + strconv.Itoa(rec.EstimatedCredit) + "\n")
	if rec.Objective != "" {
		b.WriteString("- Objective: " + rec.Objective + "\n")
	}
	if rec.Error != "" {
		b.WriteString("- Error: " + rec.Error + "\n")
	}
	if rec.ScreenshotURL != "" {
		b.WriteString("- Screenshot: [" + filepath.Base(rec.ScreenshotPath) + "](" + rec.ScreenshotURL + ")\n")
	}
	if rec.ActiveWindow != "" {
		b.WriteString("- Active window: " + rec.ActiveWindow + "\n")
	}
	if len(rec.Files) > 0 {
		b.WriteString("\n### Files\n\n")
		limit := len(rec.Files)
		if limit > 20 {
			limit = 20
		}
		for _, f := range rec.Files[:limit] {
			kind := "file"
			if f.IsDir {
				kind = "dir"
			}
			b.WriteString("- " + kind + ": " + f.Path + "\n")
		}
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
	if len(rec.Interactions) > 0 {
		b.WriteString("\n### Interactions\n\n")
		for _, interaction := range rec.Interactions {
			line := "- " + interaction.Action
			switch interaction.Action {
			case "click":
				line += " at " + strconv.Itoa(interaction.X) + "," + strconv.Itoa(interaction.Y)
			case "type":
				line += " " + strconv.Itoa(len([]rune(interaction.Text))) + " char(s)"
			case "key":
				line += " " + interaction.Key
			}
			if interaction.WindowTitle != "" {
				line += " in " + interaction.WindowTitle
			}
			if interaction.DryRun {
				line += " (dry-run)"
			}
			b.WriteString(line + "\n")
		}
	}
	if len(rec.Risks) > 0 {
		b.WriteString("\n### Risks\n\n")
		for _, risk := range rec.Risks {
			b.WriteString("- " + risk + "\n")
		}
	}
	if len(rec.NextActions) > 0 {
		b.WriteString("\n### Next actions\n\n")
		for _, action := range rec.NextActions {
			b.WriteString("- " + action + "\n")
		}
	}
	return strings.TrimSpace(b.String())
}

func desktopTaskStatusFromErr(err error) string {
	if err != nil {
		return "failed"
	}
	return "completed"
}

// InspectTarget exposes desktop target inspection to application adapters.
func InspectTarget(path string, maxFiles int) ([]DesktopFileInfo, string, error) {
	return inspectDesktopTarget(path, maxFiles)
}

// WindowsSendKeysToken translates a supported key name to SendKeys syntax.
func WindowsSendKeysToken(key string) (string, bool) {
	return windowsSendKeysToken(key)
}

// NextActions derives suggested follow-up actions for a desktop task record.
func NextActions(rec *DesktopTaskRecord) []string {
	return desktopTaskNextActions(rec)
}

// FormatMarkdown renders a desktop task record for artifact delivery.
func FormatMarkdown(rec *DesktopTaskRecord) string {
	return formatDesktopTaskMarkdown(rec)
}

func displayValue(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "-"
	}
	return value
}
