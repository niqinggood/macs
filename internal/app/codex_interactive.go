package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"mac/internal/processutil"
)

const codexInteractiveIdleTTL = 2 * time.Hour

type CodexInteractiveManager struct {
	gateway *GatewayService
	logger  *log.Logger
	mu      sync.Mutex
	active  map[string]*CodexInteractiveSession
}

type CodexInteractiveRuntimeState struct {
	SessionAlive     bool `json:"session_alive"`
	ActiveSessions   int  `json:"active_sessions"`
	ActiveTurns      int  `json:"active_turns"`
	WaitingApprovals int  `json:"waiting_approvals"`
}

type CodexInteractiveSession struct {
	ID        string
	SessionID string
	TaskID    string
	WorkDir   string
	Channel   string
	UserID    string
	Recipient string

	proc      processutil.PTYProcess
	appClient *codexAppServerClient
	appQueue  chan string
	cancel    context.CancelFunc
	startedAt time.Time
	lastUsed  time.Time
	lastReply time.Time
	done      chan struct{}

	mu                  sync.Mutex
	buf                 strings.Builder
	closed              bool
	exitErr             error
	threadID            string
	activeTurnID        string
	exitOnce            sync.Once
	trustPromptProbe    string
	trustPromptAnswered bool
}

func NewCodexInteractiveManager(gateway *GatewayService, logger *log.Logger) *CodexInteractiveManager {
	if logger == nil {
		logger = log.New(os.Stdout, "[CodexInteractive] ", log.LstdFlags)
	}
	m := &CodexInteractiveManager{
		gateway: gateway,
		logger:  logger,
		active:  make(map[string]*CodexInteractiveSession),
	}
	go m.cleanupLoop()
	return m
}

func (m *CodexInteractiveManager) Has(sessionID string) bool {
	if m == nil || strings.TrimSpace(sessionID) == "" {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.active[sessionID]
	if s == nil || !s.Alive() {
		if s != nil {
			delete(m.active, sessionID)
		}
		return false
	}
	return true
}

func (m *CodexInteractiveManager) RuntimeState() CodexInteractiveRuntimeState {
	state := CodexInteractiveRuntimeState{}
	if m == nil {
		return state
	}
	m.mu.Lock()
	sessions := make([]*CodexInteractiveSession, 0, len(m.active))
	for sessionID, s := range m.active {
		if s == nil || !s.Alive() {
			delete(m.active, sessionID)
			continue
		}
		sessions = append(sessions, s)
	}
	m.mu.Unlock()

	state.ActiveSessions = len(sessions)
	state.SessionAlive = state.ActiveSessions > 0
	for _, s := range sessions {
		s.mu.Lock()
		activeTurn := strings.TrimSpace(s.activeTurnID) != ""
		s.mu.Unlock()
		if activeTurn {
			state.ActiveTurns++
		}
	}
	return state
}

func (m *CodexInteractiveManager) StartOrSend(ctx context.Context, sessionID, channel, userID, recipient, message, workDir string) (string, string, error) {
	if m == nil {
		return "", "", fmt.Errorf("codex interactive manager unavailable")
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return "", "", fmt.Errorf("session_id is required")
	}
	message = strings.TrimSpace(message)
	if message == "" {
		return "", "", fmt.Errorf("message is empty")
	}
	if isCodexInteractiveStopCommand(message) {
		taskID := aeChatTaskID(sessionID + "-codex")
		stopped := m.Stop(sessionID)
		if stopped {
			return taskID, "Codex interactive session stopped.", nil
		}
		return taskID, "No active Codex interactive session.", nil
	}

	s, created, err := m.getOrCreate(ctx, sessionID, channel, userID, recipient, workDir, message)
	if err != nil {
		return "", "", err
	}
	m.markCodexInteractiveTurnRunning(s, message)
	if !created {
		if err := s.Send(message); err != nil {
			m.remove(sessionID, s)
			return "", "", err
		}
	}
	if created {
		return s.TaskID, fmt.Sprintf("Codex interactive session started in %s.", s.WorkDir), nil
	}
	return s.TaskID, "Message forwarded to active Codex session.", nil
}

func (m *CodexInteractiveManager) Stop(sessionID string) bool {
	if m == nil || strings.TrimSpace(sessionID) == "" {
		return false
	}
	m.mu.Lock()
	s := m.active[sessionID]
	delete(m.active, sessionID)
	m.mu.Unlock()
	if s == nil {
		return false
	}
	s.Close()
	return true
}

func (m *CodexInteractiveManager) getOrCreate(ctx context.Context, sessionID, channel, userID, recipient, workDir, initialMessage string) (*CodexInteractiveSession, bool, error) {
	m.mu.Lock()
	if s := m.active[sessionID]; s != nil && s.Alive() {
		s.updateRoute(channel, userID, recipient)
		s.touch()
		m.mu.Unlock()
		return s, false, nil
	}
	delete(m.active, sessionID)
	m.mu.Unlock()

	s, err := m.start(ctx, sessionID, channel, userID, recipient, workDir, initialMessage)
	if err != nil {
		return nil, false, err
	}

	m.mu.Lock()
	if existing := m.active[sessionID]; existing != nil && existing.Alive() {
		m.mu.Unlock()
		s.Close()
		existing.updateRoute(channel, userID, recipient)
		existing.touch()
		return existing, false, nil
	}
	m.active[sessionID] = s
	m.mu.Unlock()
	return s, true, nil
}

func (m *CodexInteractiveManager) start(ctx context.Context, sessionID, channel, userID, recipient, workDir, initialMessage string) (*CodexInteractiveSession, error) {
	workDir = resolveCodexInteractiveWorkDir(workDir, m.defaultWorkDir())
	if _, err := os.Stat(workDir); err != nil {
		return nil, fmt.Errorf("codex work_dir unavailable %q: %w", workDir, err)
	}
	if s, err := m.startAppServer(ctx, sessionID, channel, userID, recipient, workDir, initialMessage); err == nil {
		return s, nil
	} else if m.logger != nil {
		m.logger.Printf("codex app-server interactive unavailable, falling back to PTY: %v", err)
	}
	return m.startPTY(ctx, sessionID, channel, userID, recipient, workDir, initialMessage)
}

func (m *CodexInteractiveManager) startAppServer(ctx context.Context, sessionID, channel, userID, recipient, workDir, initialMessage string) (*CodexInteractiveSession, error) {
	if ok, reason := codexAppServerSupported(ctx); !ok {
		if strings.TrimSpace(reason) == "" {
			reason = "codex app-server is unavailable"
		}
		return nil, fmt.Errorf("%s", reason)
	}

	runCtx, cancel := context.WithCancel(context.Background())
	client, err := newCodexAppServerClient(runCtx, workDir)
	if err != nil {
		cancel()
		return nil, err
	}
	setupCtx, setupCancel := context.WithTimeout(ctx, 30*time.Second)
	defer setupCancel()
	if err := client.initialize(setupCtx); err != nil {
		client.close()
		cancel()
		return nil, codexAppServerWrapError(client, "codex app-server initialize failed", err)
	}
	threadRes, err := client.request(setupCtx, "thread/start", map[string]interface{}{"cwd": workDir}, 30*time.Second)
	if err != nil {
		client.close()
		cancel()
		return nil, codexAppServerWrapError(client, "codex thread/start failed", err)
	}
	threadID := extractCodexThreadID(threadRes)
	if threadID == "" {
		client.close()
		cancel()
		return nil, fmt.Errorf("codex thread/start returned no thread id")
	}

	taskID := aeChatTaskID(sessionID + "-codex")
	s := &CodexInteractiveSession{
		ID:        fmt.Sprintf("codex_interactive_%d", time.Now().UnixNano()),
		SessionID: sessionID,
		TaskID:    taskID,
		WorkDir:   workDir,
		Channel:   channel,
		UserID:    userID,
		Recipient: recipient,
		appClient: client,
		appQueue:  make(chan string, 32),
		cancel:    cancel,
		startedAt: time.Now(),
		lastUsed:  time.Now(),
		done:      make(chan struct{}),
		threadID:  threadID,
	}

	go s.appServerLoop(runCtx, m)
	go func() {
		<-client.done
		err := error(nil)
		if !errors.Is(runCtx.Err(), context.Canceled) {
			err = fmt.Errorf("codex app-server exited")
		}
		s.markExited(err)
		if m.gateway != nil {
			msg := "Codex app-server interactive session exited."
			status := "completed"
			if errors.Is(runCtx.Err(), context.Canceled) {
				msg = "Codex app-server interactive session stopped."
				status = "stopped"
			} else if err != nil {
				msg = "Codex app-server interactive session exited: " + err.Error()
				status = "error"
			}
			m.gateway.BroadcastProgress(ProgressEvent{Type: "codex_exit", TaskID: taskID, Message: msg, AgentName: "codex", Status: status})
			m.finishGatewayTask(taskID, status, msg)
		}
		m.remove(sessionID, s)
	}()

	m.registerGatewayTask(s, "Codex app-server interactive session started: "+workDir)
	if err := s.enqueueAppMessage(initialMessage); err != nil {
		s.Close()
		return nil, err
	}
	return s, nil
}

func (m *CodexInteractiveManager) startPTY(ctx context.Context, sessionID, channel, userID, recipient, workDir, initialMessage string) (*CodexInteractiveSession, error) {
	exe, err := findExternalCodingExecutable("codex")
	if err != nil {
		return nil, err
	}

	runCtx, cancel := context.WithCancel(context.Background())
	args := codexInteractiveArgs(workDir, initialMessage)
	env := codexInteractiveEnv()
	proc, err := processutil.StartPTY(runCtx, exe, args, workDir, env)
	if err != nil {
		cancel()
		return nil, err
	}

	taskID := aeChatTaskID(sessionID + "-codex")
	s := &CodexInteractiveSession{
		ID:        fmt.Sprintf("codex_interactive_%d", time.Now().UnixNano()),
		SessionID: sessionID,
		TaskID:    taskID,
		WorkDir:   workDir,
		Channel:   channel,
		UserID:    userID,
		Recipient: recipient,
		proc:      proc,
		cancel:    cancel,
		startedAt: time.Now(),
		lastUsed:  time.Now(),
		done:      make(chan struct{}),
	}

	go s.readLoop(proc, false, m)
	go func() {
		err := proc.Wait(runCtx)
		s.markExited(err)
		if m.gateway != nil {
			msg := "Codex interactive session exited."
			status := "completed"
			if errors.Is(runCtx.Err(), context.Canceled) {
				msg = "Codex interactive session stopped."
				status = "stopped"
			} else if err != nil {
				msg = "Codex interactive session exited: " + err.Error()
				status = "error"
			}
			m.gateway.BroadcastProgress(ProgressEvent{Type: "codex_exit", TaskID: taskID, Message: msg, AgentName: "codex", Status: status})
			m.finishGatewayTask(taskID, status, msg)
		}
		m.remove(sessionID, s)
	}()

	m.registerGatewayTask(s, "Codex interactive session started: "+workDir)
	return s, nil
}

func (m *CodexInteractiveManager) registerGatewayTask(s *CodexInteractiveSession, startMessage string) {
	if m == nil || m.gateway == nil || s == nil {
		return
	}
	task := &GatewayTask{
		ID:        s.TaskID,
		Request:   &MessageRequest{TaskID: s.TaskID, TaskType: "codex_interactive", SessionId: s.SessionID, AgentName: "codex", Channel: s.Channel, UserId: s.UserID, CreatedAt: time.Now()},
		Status:    "running",
		CreatedAt: time.Now(),
	}
	m.gateway.tasksMu.Lock()
	if m.gateway.tasks != nil {
		m.gateway.tasks[s.TaskID] = task
	}
	m.gateway.tasksMu.Unlock()
	_ = SaveGatewayTaskToDB(m.gateway.db, task)
	m.gateway.BroadcastProgress(ProgressEvent{Type: "codex_start", TaskID: s.TaskID, Message: startMessage, AgentName: "codex", Status: "running"})
}

func codexInteractiveArgs(workDir, initialMessage string) []string {
	args := []string{"--dangerously-bypass-approvals-and-sandbox", "--no-alt-screen"}
	if strings.TrimSpace(workDir) != "" {
		args = append(args, "--cd", workDir)
	}
	if prompt := codexInteractiveInput(codexInteractivePrepareTurnMessage(initialMessage)); prompt != "" {
		args = append(args, prompt)
	}
	return args
}

func codexInteractiveEnv() []string {
	env := os.Environ()
	env = setEnvValue(env, "TERM", "xterm-256color")
	env = setEnvValue(env, "COLORTERM", "truecolor")
	env = setEnvValue(env, "NO_COLOR", "1")
	return env
}

func setEnvValue(env []string, key, value string) []string {
	prefix := key + "="
	for i, item := range env {
		if strings.HasPrefix(item, prefix) {
			env[i] = prefix + value
			return env
		}
	}
	return append(env, prefix+value)
}

func (m *CodexInteractiveManager) defaultWorkDir() string {
	if m != nil && m.gateway != nil && m.gateway.config != nil && strings.TrimSpace(m.gateway.config.Workspace) != "" {
		return m.gateway.config.Workspace
	}
	wd, _ := os.Getwd()
	return wd
}

func (m *CodexInteractiveManager) remove(sessionID string, s *CodexInteractiveSession) {
	if m == nil {
		return
	}
	m.mu.Lock()
	if m.active[sessionID] == s {
		delete(m.active, sessionID)
	}
	m.mu.Unlock()
}

func (m *CodexInteractiveManager) finishGatewayTask(taskID, status, result string) {
	if m == nil || m.gateway == nil || strings.TrimSpace(taskID) == "" {
		return
	}
	g := m.gateway
	var task *GatewayTask
	g.tasksMu.Lock()
	if existing, ok := g.tasks[taskID]; ok {
		task = existing
		task.Status = status
		task.Result = result
		if status == "error" {
			task.Error = fmt.Errorf("codex interactive error")
		} else {
			task.Error = nil
		}
		now := time.Now()
		task.CompletedAt = &now
	}
	g.tasksMu.Unlock()
	if task != nil {
		_ = SaveGatewayTaskToDB(g.db, task)
	}
	if g.aeManager != nil {
		workStatus := status
		if workStatus == "error" {
			workStatus = "failed"
		}
		g.aeManager.updateRunningWorkRecord(strings.TrimSuffix(taskID, "-de"), workStatus, result)
	}
	var taskErr error
	if status == "error" {
		taskErr = errors.New(result)
	}
	g.broadcastTaskUpdate(taskID, status, result, taskErr)
	g.taskStreamsMu.Lock()
	if ch, ok := g.taskStreams[taskID]; ok {
		delete(g.taskStreams, taskID)
		close(ch)
	}
	g.taskStreamsMu.Unlock()
}

func (m *CodexInteractiveManager) markCodexInteractiveTurnRunning(s *CodexInteractiveSession, message string) {
	if m == nil || m.gateway == nil || s == nil || strings.TrimSpace(s.TaskID) == "" {
		return
	}
	g := m.gateway
	var task *GatewayTask
	g.tasksMu.Lock()
	if existing, ok := g.tasks[s.TaskID]; ok {
		task = existing
		task.Status = "running"
		task.Result = nil
		task.Error = nil
		task.CompletedAt = nil
		if task.Request != nil {
			task.Request.Message = message
			task.Request.TaskType = "codex_interactive"
			task.Request.SessionId = s.SessionID
			task.Request.AgentName = "codex"
			task.Request.Channel = s.Channel
			task.Request.UserId = s.UserID
		}
	}
	g.tasksMu.Unlock()
	if task != nil {
		_ = SaveGatewayTaskToDB(g.db, task)
	}
	g.broadcastTaskUpdate(s.TaskID, "running", nil, nil)
}

func (m *CodexInteractiveManager) completeCodexInteractiveTurn(s *CodexInteractiveSession, result string, isErr bool) {
	if m == nil || m.gateway == nil || s == nil || strings.TrimSpace(s.TaskID) == "" {
		return
	}
	status := "completed"
	var taskErr error
	if isErr {
		status = "failed"
		taskErr = errors.New(result)
	}
	g := m.gateway
	now := time.Now()
	var task *GatewayTask
	g.tasksMu.Lock()
	if existing, ok := g.tasks[s.TaskID]; ok {
		task = existing
		task.Status = status
		task.Result = result
		task.Error = taskErr
		task.CompletedAt = &now
	}
	g.tasksMu.Unlock()
	if task != nil {
		_ = SaveGatewayTaskToDB(g.db, task)
	}
	if g.db != nil {
		updates := map[string]interface{}{"status": status}
		_ = g.db.Model(&ChatMessage{}).
			Where("session_id = ? AND task_id = ? AND role = ?", s.SessionID, s.TaskID, "user").
			Updates(updates).Error
	}
	if g.aeManager != nil {
		g.aeManager.updateWorkRecord(strings.TrimSuffix(s.TaskID, "-de"), status, result)
	}
	g.broadcastTaskUpdate(s.TaskID, status, result, taskErr)
}

func (m *CodexInteractiveManager) cleanupLoop() {
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		now := time.Now()
		var stale []*CodexInteractiveSession
		m.mu.Lock()
		for sessionID, s := range m.active {
			if s == nil || !s.Alive() || now.Sub(s.LastUsed()) > codexInteractiveIdleTTL {
				delete(m.active, sessionID)
				stale = append(stale, s)
			}
		}
		m.mu.Unlock()
		for _, s := range stale {
			if s != nil {
				s.Close()
			}
		}
	}
}

func (s *CodexInteractiveSession) Alive() bool {
	if s == nil {
		return false
	}
	select {
	case <-s.done:
		return false
	default:
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return !s.closed
}

func (s *CodexInteractiveSession) LastUsed() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastUsed
}

func (s *CodexInteractiveSession) touch() {
	s.mu.Lock()
	s.lastUsed = time.Now()
	s.mu.Unlock()
}

func (s *CodexInteractiveSession) updateRoute(channel, userID, recipient string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	if strings.TrimSpace(channel) != "" {
		s.Channel = channel
	}
	if strings.TrimSpace(userID) != "" {
		s.UserID = userID
	}
	if strings.TrimSpace(recipient) != "" {
		s.Recipient = recipient
	}
	s.mu.Unlock()
}

func (s *CodexInteractiveSession) Send(message string) error {
	if s != nil && s.appClient != nil {
		return s.enqueueAppMessage(message)
	}
	if s == nil || s.proc == nil {
		return fmt.Errorf("codex session is not writable")
	}
	s.touch()
	message = codexInteractiveInput(codexInteractivePrepareTurnMessage(message))
	if !strings.HasSuffix(message, "\r") {
		message += "\r"
	}
	return s.writeRaw(message)
}

func (s *CodexInteractiveSession) writeRaw(message string) error {
	if s == nil || s.proc == nil {
		return fmt.Errorf("codex session is not writable")
	}
	s.touch()
	_, err := io.WriteString(s.proc, message)
	return err
}

func codexInteractiveInput(message string) string {
	message = strings.ReplaceAll(message, "\r", " ")
	message = strings.ReplaceAll(message, "\n", " ")
	return strings.TrimSpace(message)
}

func codexInteractivePrepareTurnMessage(message string) string {
	message = strings.TrimSpace(message)
	var instructions []string
	if codexInteractiveWantsVisualArtifact(message) {
		instructions = append(instructions, codexInteractiveImageInstruction())
	}
	if codexInteractiveWantsPresentationArtifact(message) || codexInteractiveWantsMaterialArtifact(message) {
		instructions = append(instructions, codexInteractivePresentationInstruction())
	}
	if len(instructions) == 0 {
		return message
	}
	return message + "\n\n" + strings.Join(instructions, "\n\n")
}

func codexInteractiveImageInstruction() string {
	return "[OpenKitty image output instruction]\nThe user is asking for a generated visual. If you generate or edit a bitmap image, use Codex's built-in image generation capability when available (for example $imagegen), save the result to a local file if the tool provides a path, and include the saved path or image result in the final answer. OpenKitty will render image artifacts returned by Codex."
}

func codexInteractivePresentationInstruction() string {
	return "[OpenKitty PPT/material reference instruction]\nThe user is asking for PPT, slides, posters, generated images, or other presentation materials. Start with `builtin/presentation/ppt-art-director/SKILL.md`; Runtime assets are bundled and must not depend on a workspace reference directory. Level 3 premium custom is the beauty-first and higher-end commercial route: for explicit premium, design-led, executive, board, investor, client, launch, sales, brand, or showcase work, activate `builtin/presentation/ppt-master/SKILL.md` and run its complete Strategist/Executor workflow before any conservative scaffold. Level 2 regular custom is the default for substantive audience-facing PPTs: use the bundled task-specific python-pptx/PptxGenJS builder plus source-bound deck_spec.json and executable visual_plan.json. The selected palette, typography, composition rhythm, and slide-by-slide asset manifest must drive the real output. Level 1 fallback structured uses `builtin/presentation/base-ppt/SKILL.md` only for explicit fast/internal/recurring/fixed-schema work or disclosed disaster recovery after applicable quality routes and one targeted correction fail. Use `builtin/presentation/paper-operators-visual/SKILL.md` when a small number of decisive visual assets materially improve the story. better-ppt is a legacy compatibility alias. Phrases such as \"简单把做的事做成 PPT\" request concise content, not lower visual quality. Never call convert for PPTX with an empty generator or substitute a giant one-shot inline script. Preserve facts and never invent numeric charts. Save deliverables under .openkitty/storage. For every substantive deck, perform bounded render-and-visual-review on actual rendered pixels: audit at 16pt body/35pt title, native-render it, inspect the contact sheet and every slide separately, and validate a vision-authored aesthetic_qa.json before delivery. Report the route, Editable true/false, PPTX path, slide count, QA result, and any fallback reason."
}

func codexInteractiveWantsVisualArtifact(message string) bool {
	lower := strings.ToLower(strings.TrimSpace(message))
	if lower == "" {
		return false
	}
	if codexInteractiveHasGenerateIntent(lower) {
		for _, word := range codexVisualArtifactWords {
			if strings.Contains(lower, strings.ToLower(word)) {
				return true
			}
		}
	}
	generateWords := []string{"\u751f\u6210", "\u753b", "\u7ed8\u5236", "\u505a", "\u5236\u4f5c", "create", "generate", "draw", "make"}
	visualWords := []string{"\u56fe", "\u56fe\u7247", "\u6d77\u62a5", "\u914d\u56fe", "\u751f\u56fe", "\u4fe1\u606f\u56fe", "\u6d41\u7a0b\u56fe", "\u67b6\u6784\u56fe", "\u793a\u610f\u56fe", "\u77e5\u8bc6\u6d77\u62a5", "diagram", "infographic", "svg", "mermaid"}
	hasGenerate := false
	for _, word := range generateWords {
		if strings.Contains(lower, word) {
			hasGenerate = true
			break
		}
	}
	if !hasGenerate {
		return false
	}
	for _, word := range visualWords {
		if strings.Contains(lower, strings.ToLower(word)) {
			return true
		}
	}
	return false
}

func codexInteractiveWantsPresentationArtifact(message string) bool {
	lower := strings.ToLower(strings.TrimSpace(message))
	if lower == "" || !codexInteractiveHasGenerateIntent(lower) {
		return false
	}
	for _, word := range codexPresentationArtifactWords {
		if strings.Contains(lower, strings.ToLower(word)) {
			return true
		}
	}
	return false
}

func codexInteractiveWantsMaterialArtifact(message string) bool {
	lower := strings.ToLower(strings.TrimSpace(message))
	if lower == "" || !codexInteractiveHasGenerateIntent(lower) {
		return false
	}
	for _, word := range codexMaterialArtifactWords {
		if strings.Contains(lower, strings.ToLower(word)) {
			return true
		}
	}
	return false
}

func codexInteractiveHasGenerateIntent(lower string) bool {
	for _, word := range codexGenerateIntentWords {
		if strings.Contains(lower, strings.ToLower(word)) {
			return true
		}
	}
	return false
}

var codexGenerateIntentWords = []string{
	"\u751f\u6210", "\u521b\u5efa", "\u5236\u4f5c", "\u7ed8\u5236", "\u753b", "\u505a", "\u8bbe\u8ba1", "\u8f6c\u6362", "\u6e32\u67d3", "\u4ea7\u51fa", "\u4ea4\u4ed8", "\u751f\u6210\u4e00\u4e2a",
	"create", "generate", "draw", "make", "build", "produce", "design", "convert", "render",
}

var codexVisualArtifactWords = []string{
	"\u56fe", "\u56fe\u7247", "\u914d\u56fe", "\u6d77\u62a5", "\u751f\u56fe", "\u4fe1\u606f\u56fe", "\u6d41\u7a0b\u56fe", "\u67b6\u6784\u56fe", "\u793a\u610f\u56fe", "\u77e5\u8bc6\u6d77\u62a5",
	"\u6a2a\u5e45", "\u5361\u7247", "\u5c01\u9762\u56fe", "\u5934\u56fe", "\u957f\u56fe", "\u63d2\u56fe", "\u89c6\u89c9\u7269\u6599", "\u7d20\u6750", "\u7269\u6599",
	"diagram", "infographic", "svg", "mermaid", "poster", "hero image", "visual asset", "illustration", "image",
}

var codexPresentationArtifactWords = []string{
	"ppt", "pptx", "powerpoint", "slides", "slide deck", "deck", "presentation", "keynote",
	"\u5e7b\u706f\u7247", "\u6f14\u793a\u6587\u7a3f", "\u6f14\u793a", "\u6c47\u62a5", "\u8def\u6f14", "\u8bb2\u7a3f", "\u6c47\u62a5\u6750\u6599", "\u7b80\u62a5",
}

var codexMaterialArtifactWords = []string{
	"\u6a2a\u5e45", "\u5361\u7247", "\u5c0f\u7ea2\u4e66\u56fe", "\u516c\u4f17\u53f7\u56fe", "\u516c\u4f17\u53f7\u5934\u56fe", "\u5c01\u9762", "\u957f\u56fe", "\u89c6\u89c9\u7269\u6599",
	"\u7d20\u6750", "\u7269\u6599", "\u8425\u9500\u7269\u6599", "\u5934\u56fe", "\u5c01\u9762\u56fe", "\u77e5\u8bc6\u6d77\u62a5", "poster", "one-pager", "social card", "visual material",
}

func (s *CodexInteractiveSession) Close() {
	if s == nil {
		return
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	proc := s.proc
	client := s.appClient
	threadID := s.threadID
	turnID := s.activeTurnID
	cancel := s.cancel
	s.mu.Unlock()
	if client != nil && threadID != "" && turnID != "" && !client.exited() {
		interruptCtx, interruptCancel := context.WithTimeout(context.Background(), 2*time.Second)
		_, _ = client.request(interruptCtx, "turn/interrupt", map[string]interface{}{"threadId": threadID, "turnId": turnID}, 2*time.Second)
		interruptCancel()
	}
	if cancel != nil {
		cancel()
	}
	if proc != nil {
		_ = proc.Close()
	}
	if client != nil {
		client.close()
	}
	s.markExited(context.Canceled)
}

func (s *CodexInteractiveSession) enqueueAppMessage(message string) error {
	if s == nil || s.appClient == nil || s.appQueue == nil {
		return fmt.Errorf("codex app-server session is not writable")
	}
	message = strings.TrimSpace(message)
	if message == "" {
		return fmt.Errorf("message is empty")
	}
	s.mu.Lock()
	closed := s.closed
	q := s.appQueue
	s.lastUsed = time.Now()
	s.mu.Unlock()
	if closed {
		return fmt.Errorf("codex app-server session is closed")
	}
	select {
	case q <- message:
		return nil
	default:
		return fmt.Errorf("codex app-server message queue is full")
	}
}

func (s *CodexInteractiveSession) appServerLoop(ctx context.Context, m *CodexInteractiveManager) {
	for {
		select {
		case <-ctx.Done():
			return
		case message := <-s.appQueue:
			if err := s.runAppServerTurn(ctx, m, message); err != nil && !errors.Is(err, context.Canceled) {
				if m != nil {
					m.deliverOutput(s, "Codex app-server turn failed: "+truncate(err.Error(), 1200), true)
				}
			}
		}
	}
}

func (s *CodexInteractiveSession) runAppServerTurn(parent context.Context, m *CodexInteractiveManager, message string) error {
	if s == nil || s.appClient == nil || s.threadID == "" {
		return fmt.Errorf("codex app-server session is not ready")
	}
	message = codexInteractivePrepareTurnMessage(message)
	client := s.appClient
	turnCtx, cancel := context.WithTimeout(parent, 45*time.Minute)
	defer cancel()

	turnRes, err := client.request(turnCtx, "turn/start", codexInteractiveTurnStartParams(s.threadID, s.WorkDir, message, true), 30*time.Second)
	if err != nil {
		if m != nil {
			m.broadcastCodexAppServerProgress(s, codexAppServerProgress{Type: "warning", Message: "Codex app-server permissive turn failed, retrying with default policy: " + err.Error(), Status: "warn"})
		}
		turnRes, err = client.request(turnCtx, "turn/start", codexInteractiveTurnStartParams(s.threadID, s.WorkDir, message, false), 30*time.Second)
	}
	if err != nil {
		return codexAppServerWrapError(client, "codex turn/start failed", err)
	}
	turnID := extractCodexTurnID(turnRes)
	s.setActiveTurnID(turnID)
	defer s.setActiveTurnID("")

	fileChangeSummaries := make(map[string]string)
	var finalText string
	var imageArtifacts []string
	seenImageArtifacts := make(map[string]bool)
	for {
		if req, ok := client.takeServerRequest(); ok {
			progress, err := handleCodexInteractiveServerRequest(client, req, fileChangeSummaries)
			if m != nil && strings.TrimSpace(progress.Message) != "" {
				m.broadcastCodexAppServerProgress(s, progress)
			}
			if err != nil && m != nil {
				m.broadcastCodexAppServerProgress(s, codexAppServerProgress{Type: "warning", Message: "Codex app-server request handling failed: " + err.Error(), Status: "warn"})
			}
			continue
		}

		select {
		case <-turnCtx.Done():
			if turnID != "" && !client.exited() {
				_, _ = client.request(context.Background(), "turn/interrupt", map[string]interface{}{"threadId": s.threadID, "turnId": turnID}, 5*time.Second)
			}
			return turnCtx.Err()
		default:
		}
		if client.exited() {
			return codexAppServerWrapError(client, "codex app-server process exited", nil)
		}

		note, ok := client.takeNotification(250 * time.Millisecond)
		if !ok {
			continue
		}
		progress, extractedFinal := projectCodexNotification(note, fileChangeSummaries)
		if m != nil && strings.TrimSpace(progress.Message) != "" {
			m.broadcastCodexAppServerProgress(s, progress)
		}
		if m != nil {
			if imageMarkdown, key := m.codexImageArtifactMarkdown(s, note); strings.TrimSpace(imageMarkdown) != "" {
				if key == "" {
					key = imageMarkdown
				}
				if !seenImageArtifacts[key] {
					seenImageArtifacts[key] = true
					imageArtifacts = append(imageArtifacts, strings.TrimSpace(imageMarkdown))
				}
			}
		}
		if strings.TrimSpace(extractedFinal) != "" {
			finalText = strings.TrimSpace(extractedFinal)
		}
		if note["method"] == "turn/completed" {
			final := strings.TrimSpace(finalText)
			if len(imageArtifacts) > 0 {
				images := imageArtifacts[len(imageArtifacts)-1]
				if final != "" {
					final = images + "\n\n" + final
				} else {
					final = images
				}
			}
			if strings.TrimSpace(final) != "" && m != nil {
				s.appendOutput(final)
				m.deliverOutput(s, final, false)
			}
			status, turnErr := codexTurnCompletionStatus(note)
			if status != "" && status != "completed" && status != "interrupted" {
				if turnErr == "" {
					turnErr = status
				}
				if codexTurnFailureCanUseDeliveredFinal(final, status, turnErr) {
					if m != nil {
						m.broadcastCodexAppServerProgress(s, codexAppServerProgress{Type: "warning", Message: "Codex reported a non-fatal turn status after delivering the final output: " + status, Status: "warn"})
					}
					return nil
				}
				return fmt.Errorf("codex turn ended status=%s: %s", status, turnErr)
			}
			return nil
		}
	}
}

func (m *CodexInteractiveManager) codexImageArtifactMarkdown(s *CodexInteractiveSession, note map[string]interface{}) (string, string) {
	method := codexMapString(note, "method")
	if method != "item/completed" && method != "item/updated" {
		return "", ""
	}
	params, _ := note["params"].(map[string]interface{})
	item, _ := params["item"].(map[string]interface{})
	if len(item) == 0 {
		return "", ""
	}
	itemType := codexMapString(item, "type")
	itemID := codexMapString(item, "id")
	switch itemType {
	case "imageGeneration":
		status := strings.ToLower(strings.TrimSpace(codexMapString(item, "status")))
		if method != "item/completed" && !strings.Contains(status, "complete") && !strings.Contains(status, "success") {
			return "", ""
		}
		savedPath := strings.TrimSpace(codexMapString(item, "savedPath"))
		result := strings.TrimSpace(codexMapString(item, "result"))
		markdown, key := m.codexImageMarkdownFromPathOrResult(s, savedPath, result, "Codex 閻㈢喐鍨氶崶鍓у")
		if key == "" {
			key = itemID + ":" + savedPath + ":" + result
		}
		return markdown, key
	case "imageView":
		imagePath := strings.TrimSpace(codexMapString(item, "path"))
		markdown, key := m.codexImageMarkdownFromPathOrResult(s, imagePath, "", "Codex image")
		if key == "" {
			key = itemID + ":" + imagePath
		}
		return markdown, key
	default:
		return "", ""
	}
}

func (m *CodexInteractiveManager) codexImageMarkdownFromPathOrResult(s *CodexInteractiveSession, imagePath, result, alt string) (string, string) {
	if direct := codexDirectImageURL(result); direct != "" {
		return fmt.Sprintf("![%s](%s)", alt, direct), direct
	}
	candidates := []string{imagePath, result}
	for _, candidate := range candidates {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" || codexDirectImageURL(candidate) != "" {
			continue
		}
		resolved := codexResolveArtifactPath(s, candidate)
		if resolved == "" || !codexIsSupportedImagePath(resolved) {
			continue
		}
		if info, err := os.Stat(resolved); err == nil && !info.IsDir() {
			if md, key, err := m.storeCodexImageArtifact(s, resolved, alt); err == nil {
				return md, key
			}
		}
	}
	return "", ""
}

func codexDirectImageURL(value string) string {
	lower := strings.ToLower(strings.TrimSpace(value))
	if strings.HasPrefix(lower, "data:image/") || strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") {
		return strings.TrimSpace(value)
	}
	return ""
}

func codexResolveArtifactPath(s *CodexInteractiveSession, candidate string) string {
	candidate = strings.TrimSpace(strings.Trim(candidate, `"'`))
	if candidate == "" {
		return ""
	}
	if filepath.IsAbs(candidate) {
		return filepath.Clean(candidate)
	}
	base := ""
	if s != nil {
		base = strings.TrimSpace(s.WorkDir)
	}
	if base == "" {
		base, _ = os.Getwd()
	}
	return filepath.Clean(filepath.Join(base, candidate))
}

func codexIsSupportedImagePath(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".png", ".jpg", ".jpeg", ".webp", ".gif", ".bmp", ".svg":
		return true
	default:
		return false
	}
}

func (m *CodexInteractiveManager) storeCodexImageArtifact(s *CodexInteractiveSession, sourcePath, alt string) (string, string, error) {
	if m == nil || m.gateway == nil {
		return "", "", fmt.Errorf("gateway unavailable")
	}
	workspace := ""
	if m.gateway.config != nil {
		workspace = strings.TrimSpace(m.gateway.config.Workspace)
	}
	if workspace == "" && s != nil {
		workspace = strings.TrimSpace(s.WorkDir)
	}
	if workspace == "" {
		workspace, _ = os.Getwd()
	}
	sessionID := "codex"
	if s != nil && strings.TrimSpace(s.SessionID) != "" {
		sessionID = s.SessionID
	}
	ext := strings.ToLower(filepath.Ext(sourcePath))
	if ext == "" {
		ext = ".png"
	}
	dirRel := filepath.Join(".openkitty", "storage", "codex-images", codexArtifactSafeName(sessionID))
	dirAbs := filepath.Join(workspace, dirRel)
	if err := os.MkdirAll(dirAbs, 0755); err != nil {
		return "", "", err
	}
	base := strings.TrimSuffix(filepath.Base(sourcePath), filepath.Ext(sourcePath))
	base = codexArtifactSafeName(base)
	if base == "" {
		base = "image"
	}
	destName := fmt.Sprintf("%s-%d-%s%s", time.Now().Format("20060102-150405"), time.Now().UnixNano()%1000000, base, ext)
	destRel := filepath.Join(dirRel, destName)
	destAbs := filepath.Join(workspace, destRel)
	if err := copyCodexArtifactFile(sourcePath, destAbs); err != nil {
		return "", "", err
	}
	apiPath := "/" + filepath.ToSlash(destRel)
	downloadURL := "/api/files/download?path=" + url.QueryEscape(apiPath)
	markdown := fmt.Sprintf("![%s](%s)\n\nSaved: %s", alt, downloadURL, filepath.ToSlash(destRel))
	return markdown, destAbs, nil
}

func copyCodexArtifactFile(sourcePath, destPath string) error {
	src, err := os.Open(sourcePath)
	if err != nil {
		return err
	}
	defer src.Close()
	dst, err := os.Create(destPath)
	if err != nil {
		return err
	}
	defer dst.Close()
	_, err = io.Copy(dst, src)
	return err
}

func codexArtifactSafeName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "artifact"
	}
	re := regexp.MustCompile(`[^a-zA-Z0-9._-]+`)
	name = re.ReplaceAllString(name, "-")
	name = strings.Trim(name, ".-")
	if name == "" {
		return "artifact"
	}
	if len(name) > 80 {
		name = name[:80]
	}
	return name
}

func (s *CodexInteractiveSession) setActiveTurnID(turnID string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.activeTurnID = turnID
	s.mu.Unlock()
}

func codexInteractiveTurnStartParams(threadID, workDir, message string, permissive bool) map[string]interface{} {
	params := map[string]interface{}{
		"threadId": threadID,
		"input": []map[string]interface{}{{
			"type": "text",
			"text": message,
		}},
	}
	if strings.TrimSpace(workDir) != "" {
		params["cwd"] = workDir
	}
	if permissive {
		params["approvalPolicy"] = "never"
		params["sandboxPolicy"] = map[string]interface{}{"type": "dangerFullAccess"}
	}
	return params
}

func (m *CodexInteractiveManager) broadcastCodexAppServerProgress(s *CodexInteractiveSession, evt codexAppServerProgress) {
	if m == nil || m.gateway == nil || s == nil || strings.TrimSpace(evt.Message) == "" {
		return
	}
	status := evt.Status
	if status == "" {
		status = "running"
	}
	eventType := evt.Type
	if eventType == "" {
		eventType = "codex_progress"
	}
	m.gateway.BroadcastProgress(ProgressEvent{
		Type:      eventType,
		TaskID:    s.TaskID,
		Message:   evt.Message,
		ToolName:  evt.ToolName,
		AgentName: "codex",
		Status:    status,
	})
}

func handleCodexInteractiveServerRequest(client *codexAppServerClient, req map[string]interface{}, fileChangeSummaries map[string]string) (codexAppServerProgress, error) {
	if client == nil {
		return codexAppServerProgress{}, fmt.Errorf("codex app-server client is nil")
	}
	method := codexMapString(req, "method")
	id := req["id"]
	params, _ := req["params"].(map[string]interface{})
	switch method {
	case "item/commandExecution/requestApproval":
		summary := summarizeCodexCommandApproval(params)
		decision, reason := codexAutomaticCommandDecision(params)
		if decision == "decline" {
			return codexAppServerProgress{Type: "permission_denied", Message: reason, ToolName: "exec", Status: "error"}, client.respond(id, map[string]interface{}{"decision": decision})
		}
		progress := codexAppServerProgress{Type: "permission_auto_approved", Message: "Auto-approved command execution: " + strings.ReplaceAll(summary, "\n", " "), ToolName: "exec", Status: "running"}
		return progress, client.respond(id, map[string]interface{}{"decision": decision})
	case "item/fileChange/requestApproval":
		summary := summarizeCodexFileApproval(params, fileChangeSummaries)
		if codexFileChangeTargetsProtectedSystemPath(params, fileChangeSummaries) {
			progress := codexAppServerProgress{Type: "permission_denied", Message: "Blocked Codex file change targeting a protected system path: " + summary, ToolName: "apply_patch", Status: "error"}
			return progress, client.respond(id, map[string]interface{}{"decision": "decline"})
		}
		progress := codexAppServerProgress{Type: "permission_auto_approved", Message: "Auto-approved file changes: " + strings.ReplaceAll(summary, "\n", " "), ToolName: "apply_patch", Status: "running"}
		return progress, client.respond(id, map[string]interface{}{"decision": "acceptForSession"})
	case "item/permissions/requestApproval":
		progress := codexAppServerProgress{Type: "permission_auto_approved", Message: "Auto-approved Codex permission expansion for this session", Status: "running"}
		return progress, client.respond(id, codexInteractivePermissionGrantResponse())
	case "mcpServer/elicitation/request":
		return codexAppServerProgress{Type: "permission_denied", Message: "Declined Codex MCP elicitation request", Status: "warn"}, client.respond(id, map[string]interface{}{"action": "decline", "content": nil, "_meta": nil})
	default:
		return codexAppServerProgress{Type: "warning", Message: "Unsupported Codex app-server request: " + method, Status: "warn"}, client.respondError(id, -32601, "Unsupported method: "+method)
	}
}

func codexInteractivePermissionGrantResponse() map[string]interface{} {
	return map[string]interface{}{
		"permissions": map[string]interface{}{
			"fileSystem": map[string]interface{}{
				"entries": []map[string]interface{}{{
					"access": "write",
					"path": map[string]interface{}{
						"type":  "special",
						"value": map[string]interface{}{"kind": "root"},
					},
				}},
			},
			"network": map[string]interface{}{"enabled": true},
		},
		"scope":            "session",
		"strictAutoReview": false,
	}
}

func (s *CodexInteractiveSession) readLoop(r io.Reader, isErr bool, m *CodexInteractiveManager) {
	buf := make([]byte, 4096)
	pending := ""
	for {
		n, err := r.Read(buf)
		if n > 0 {
			pending = s.deliverCodexInteractiveChunk(pending+string(buf[:n]), isErr, m)
		}
		if err != nil {
			if strings.TrimSpace(pending) != "" {
				line := cleanCodexInteractiveLine(pending)
				s.emitCodexInteractiveLine(line, isErr, m)
			}
			return
		}
	}
}

func (s *CodexInteractiveSession) deliverCodexInteractiveChunk(text string, isErr bool, m *CodexInteractiveManager) string {
	s.maybeAnswerCodexTrustPrompt(text)
	text = strings.ReplaceAll(text, "\r", "\n")
	parts := strings.Split(text, "\n")
	for _, part := range parts[:len(parts)-1] {
		line := cleanCodexInteractiveLine(part)
		s.emitCodexInteractiveLine(line, isErr, m)
	}
	return parts[len(parts)-1]
}

func (s *CodexInteractiveSession) emitCodexInteractiveLine(line string, isErr bool, m *CodexInteractiveManager) {
	if line == "" || shouldDropCodexInteractiveLine(line) {
		return
	}
	s.appendOutput(line)
	if m != nil {
		m.deliverOutput(s, line, isErr)
	}
}

var ansiControlRe = regexp.MustCompile("\x1b\\[[0-?]*[ -/]*[@-~]")
var oscControlRe = regexp.MustCompile("\x1b\\][^\x07]*(?:\x07|\x1b\\\\)")

func cleanCodexInteractiveLine(line string) string {
	line = strings.TrimRight(line, "\r\n")
	line = oscControlRe.ReplaceAllString(line, "")
	line = ansiControlRe.ReplaceAllString(line, "")
	line = strings.ReplaceAll(line, "\r", "")
	line = trimCodexTUIBorder(line)
	return strings.TrimSpace(line)
}

func trimCodexTUIBorder(line string) string {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, "\u2502") {
		return line
	}
	trimmed = strings.TrimSpace(strings.TrimPrefix(trimmed, "\u2502"))
	trimmed = strings.TrimSpace(strings.TrimSuffix(trimmed, "\u2502"))
	return trimmed
}

func cleanCodexInteractiveScreenText(text string) string {
	text = oscControlRe.ReplaceAllString(text, "")
	text = ansiControlRe.ReplaceAllString(text, "")
	text = strings.ReplaceAll(text, "\r", " ")
	text = strings.ReplaceAll(text, "\n", " ")
	return strings.TrimSpace(text)
}

func shouldDropCodexInteractiveLine(line string) bool {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return true
	}
	lower := strings.ToLower(trimmed)
	if codexTrustPromptDetected(lower) {
		return true
	}
	if codexTrustMenuLine(lower) {
		return true
	}
	if strings.HasPrefix(trimmed, "\u256d") || strings.HasPrefix(trimmed, "\u2570") {
		return true
	}
	if strings.HasPrefix(trimmed, "\u2502") && codexTUIChromeLine(trimmed) {
		return true
	}
	if strings.HasPrefix(trimmed, "\u203a") {
		return true
	}
	if strings.Contains(lower, "gpt-") && strings.Contains(trimmed, "\u00b7") {
		return true
	}
	if strings.Contains(lower, "mcp server") || strings.Contains(lower, "starting mcp") {
		return true
	}
	if isCodexSpinnerLine(trimmed) {
		return true
	}
	return false
}

func codexTrustMenuLine(lower string) bool {
	return (strings.Contains(lower, "yes, continue") && strings.Contains(lower, "no, quit")) ||
		strings.Contains(lower, "press enter to continue")
}

func codexTUIChromeLine(line string) bool {
	trimmed := strings.TrimSpace(strings.TrimLeft(line, "\u2502 "))
	if trimmed == "" {
		return true
	}
	lower := strings.ToLower(trimmed)
	if codexTrustPromptDetected(lower) || codexTrustMenuLine(lower) {
		return true
	}
	if strings.Contains(lower, "model:") || strings.Contains(lower, "directory:") || strings.Contains(lower, "permissions:") {
		return true
	}
	return false
}

func isCodexSpinnerLine(line string) bool {
	runes := []rune(strings.TrimSpace(line))
	if len(runes) == 0 || len(runes) > 3 {
		return false
	}
	for _, r := range runes {
		switch r {
		case '\u280b', '\u2819', '\u2839', '\u2838', '\u283c', '\u2834', '\u2826', '\u2827', '\u2807', '\u280f':
			return true
		}
	}
	return false
}

func (s *CodexInteractiveSession) maybeAnswerCodexTrustPrompt(text string) {
	if s == nil || text == "" {
		return
	}
	cleaned := strings.ToLower(cleanCodexInteractiveScreenText(text))
	if cleaned == "" {
		return
	}
	s.mu.Lock()
	if s.trustPromptAnswered {
		s.mu.Unlock()
		return
	}
	s.trustPromptProbe += " " + cleaned
	if len(s.trustPromptProbe) > 4096 {
		runes := []rune(s.trustPromptProbe)
		s.trustPromptProbe = string(runes[len(runes)-4096:])
	}
	shouldAnswer := codexTrustPromptDetected(s.trustPromptProbe)
	if shouldAnswer {
		s.trustPromptAnswered = true
	}
	s.mu.Unlock()
	if shouldAnswer {
		go func() {
			_ = s.writeRaw("1\r")
		}()
	}
}

func codexTrustPromptDetected(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	if lower == "" {
		return false
	}
	if strings.Contains(lower, "do you trust the contents of this directory") {
		return true
	}
	return strings.Contains(lower, "trust the contents") &&
		strings.Contains(lower, "yes, continue") &&
		strings.Contains(lower, "no, quit")
}

func (s *CodexInteractiveSession) appendOutput(line string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.buf.Len() > 0 {
		s.buf.WriteByte('\n')
	}
	s.buf.WriteString(line)
	s.lastReply = time.Now()
	if s.buf.Len() > 24000 {
		text := s.buf.String()
		runes := []rune(text)
		if len(runes) > 12000 {
			s.buf.Reset()
			s.buf.WriteString(string(runes[len(runes)-12000:]))
		}
	}
}

func (s *CodexInteractiveSession) markExited(err error) {
	s.exitOnce.Do(func() {
		s.mu.Lock()
		s.closed = true
		s.exitErr = err
		s.mu.Unlock()
		close(s.done)
	})
}

func (m *CodexInteractiveManager) deliverOutput(s *CodexInteractiveSession, line string, isErr bool) {
	if m == nil || s == nil || m.gateway == nil {
		return
	}
	status := "running"
	eventType := "codex_output"
	if isErr {
		status = "warn"
		eventType = "codex_stderr"
	}
	m.gateway.BroadcastProgress(ProgressEvent{Type: eventType, TaskID: s.TaskID, Message: line, AgentName: "codex", Status: status})

	if s.Channel != "" && s.Channel != "web" && m.gateway.channelManager != nil {
		if ch, ok := m.gateway.channelManager.GetChannel(s.Channel); ok {
			recipient := s.Recipient
			if strings.TrimSpace(recipient) == "" {
				recipient = s.UserID
			}
			_ = sendChannelContent(m.gateway, ch, recipient, line, m.logger)
		}
	}
	if m.gateway.db != nil {
		chatStatus := "completed"
		if isErr {
			chatStatus = "failed"
		}
		_, _ = m.gateway.saveChatMessage(s.SessionID, "assistant", "codex", line, chatStatus, s.TaskID, s.Channel, s.UserID)
	}
	if s.appClient != nil {
		m.completeCodexInteractiveTurn(s, line, isErr)
	}
}

func isCodexInteractiveStopCommand(message string) bool {
	msg := strings.TrimSpace(strings.ToLower(message))
	return msg == "/codex stop" || msg == "/codex exit" || msg == "/codex quit" || msg == "\u505c\u6b62codex" || msg == "\u9000\u51facodex" || msg == "闁稿绮嶉娌渙dex" || msg == "闂侇偀鍋撻柛鎴狀嚝odex"
}

func shouldRouteToCodexInteractive(message string, hasActiveSession bool) bool {
	stop := isCodexInteractiveStopCommand(message)
	return (hasActiveSession || stop) && (stop || !strings.Contains(message, "@"))
}

func resolveCodexInteractiveWorkDir(requested, fallback string) string {
	requested = strings.TrimSpace(requested)
	if requested == "" {
		requested = fallback
	}
	if requested == "" {
		requested, _ = os.Getwd()
	}
	if filepath.IsAbs(requested) {
		return filepath.Clean(requested)
	}
	if filepath.IsAbs(fallback) {
		return filepath.Clean(filepath.Join(fallback, requested))
	}
	wd, _ := os.Getwd()
	return filepath.Clean(filepath.Join(wd, requested))
}

var windowsDrivePathRe = regexp.MustCompile(`(?i)([a-z]:\\[^\s'"<>|]+)`)

func inferCodexWorkDirFromTask(task, fallback string) string {
	if match := windowsDrivePathRe.FindString(strings.TrimSpace(task)); match != "" {
		path := strings.TrimRight(match, `.,;:!?)]}"'`)
		if info, err := os.Stat(path); err == nil && info.IsDir() {
			return path
		}
		if dir := filepath.Dir(path); dir != "." && dir != "" {
			if info, err := os.Stat(dir); err == nil && info.IsDir() {
				return dir
			}
		}
	}
	return fallback
}
