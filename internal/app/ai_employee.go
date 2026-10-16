package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"mac/internal/modelruntime"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"mac/internal/aiemployee"
	middlewarecore "mac/internal/middleware"
	"mac/internal/permissionpolicy"
	pipelinecatalog "mac/internal/pipelines/catalog"
	"mac/internal/portablepath"
	"mac/internal/projectctx"
	sessioncore "mac/internal/session"
	"mac/internal/textutil"
	"mac/internal/toolregistry"
	agenttools "mac/internal/tools/agents"
	businesstools "mac/internal/tools/business"
)

// ====================== Digital Employee Config ======================

type AIEmployeeConfig = aiemployee.Config
type DirectConfig = aiemployee.DirectConfig
type SupervisorConfig = aiemployee.SupervisorConfig
type AEWorkRecord = aiemployee.WorkRecord
type AEWorkRecordDB = aiemployee.WorkRecordDB
type DENotificationDB = aiemployee.NotificationDB

const aiEmployeeArtifactPolicyExplicitRequest = aiemployee.ArtifactPolicyExplicitRequest

// loadSessionContext reads recent chat messages for a session and builds context.
// This enables multi-turn dialogue: the DE sees previous exchanges and can continue naturally.
func loadSessionContext(sessionID string, maxMsgs int) string {
	if gatewayDB == nil {
		return ""
	}
	var messages []sessioncore.SessionContextChatMessage
	gatewayDB.Model(&ChatMessage{}).Where("session_id = ?", sessionID).
		Order("created_at desc").Limit(maxMsgs).Find(&messages)
	return sessioncore.BuildSessionContextPromptFromMessages(messages)
}

// ====================== Work Record ======================

// ====================== Digital Employee Manager ======================

// deQueueItem 任务队列项
type deQueueItem struct {
	Task      string
	SessionID string
	Ctx       context.Context
}

// aeConversation 多轮对话记忆
type aeConversation struct {
	History  []string // 最近对话摘要
	LastTalk time.Time
}

const aeWaitingInputStatus = "waiting_input"

const aePendingInputTTL = 24 * time.Hour

func pipelineExecutionWaitsForInput(exec *PipelineExecution) bool {
	if exec == nil {
		return false
	}
	exec.mu.Lock()
	defer exec.mu.Unlock()
	return exec.CompletionReason != nil && exec.CompletionReason.Code == aeWaitingInputStatus
}

type explicitAEMentionGrant struct {
	EmployeeID string
	seal       *struct{}
}

type explicitAEMentionGrantContextKey struct{}

var explicitAEMentionGrantSeal = &struct{}{}

func withExplicitAEMentionGrant(ctx context.Context, employeeID string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, explicitAEMentionGrantContextKey{}, explicitAEMentionGrant{
		EmployeeID: strings.TrimSpace(employeeID),
		seal:       explicitAEMentionGrantSeal,
	})
}

func hasExplicitAEMentionGrant(ctx context.Context, employeeID string) bool {
	if ctx == nil {
		return false
	}
	grant, ok := ctx.Value(explicitAEMentionGrantContextKey{}).(explicitAEMentionGrant)
	return ok && grant.seal == explicitAEMentionGrantSeal &&
		strings.EqualFold(strings.TrimSpace(grant.EmployeeID), strings.TrimSpace(employeeID))
}

type AIEmployeeManager struct {
	employees       map[string]*AIEmployeeConfig  // id → config
	byAtName        map[string]*AIEmployeeConfig  // at_name → config
	workLog         map[string][]*AEWorkRecord    // employee_id → records
	taskQueue       map[string]chan deQueueItem   // employee_id → task queue
	busy            map[string]bool               // employee_id → is busy
	conversations   map[string]*aeConversation    // employee_id → conversation memory
	channelChatIDs  map[string]string             // employee_id → "channelName:chatID" for dynamic channel replies
	externalCancels map[string]context.CancelFunc // gateway task_id → external coding runtime cancel
	gateway         *GatewayService
	logger          *log.Logger
	mu              sync.RWMutex
}

func NewAIEmployeeManager(gw *GatewayService, logger *log.Logger) *AIEmployeeManager {
	return &AIEmployeeManager{
		employees:       make(map[string]*AIEmployeeConfig),
		byAtName:        make(map[string]*AIEmployeeConfig),
		workLog:         make(map[string][]*AEWorkRecord),
		taskQueue:       make(map[string]chan deQueueItem),
		busy:            make(map[string]bool),
		conversations:   make(map[string]*aeConversation),
		channelChatIDs:  make(map[string]string),
		externalCancels: make(map[string]context.CancelFunc),
		gateway:         gw,
		logger:          logger,
	}
}

// Register loads digital employees from config.
func (m *AIEmployeeManager) Register(configs []AIEmployeeConfig) {
	for i := range configs {
		if configs[i].Template != "" {
			if err := aiemployee.ApplyTemplate(configs[i].Template, &configs[i]); err != nil {
				m.logger.Printf("[AE] Template warning for %s: %v", configs[i].Name, err)
			}
		}
		if configs[i].PipelineTemplate != "" {
			if tmpl, ok := GetPipelineTemplates()[configs[i].PipelineTemplate]; ok {
				configs[i].Pipeline = configs[i].PipelineTemplate
				m.gateway.pipelineExec.RegisterDynamic(configs[i].Pipeline, Pipeline{Name: configs[i].PipelineTemplate, Steps: tmpl.Steps})
				m.logger.Printf("[AE] %s uses pipeline template: %s (%d steps)", configs[i].Name, configs[i].PipelineTemplate, len(tmpl.Steps))
			} else {
				m.logger.Printf("[AE] Pipeline template not found: %s", configs[i].PipelineTemplate)
			}
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range configs {
		cfg := &configs[i]
		// Re-registration is also used to apply configuration changes at runtime.
		// Remove every prior alias for this employee before installing the new
		// config so switching to internal_only cannot leave a stale @ entry.
		for alias, registered := range m.byAtName {
			if registered != nil && registered.ID == cfg.ID {
				delete(m.byAtName, alias)
			}
		}
		m.employees[cfg.ID] = cfg
		if !cfg.InternalOnly {
			for _, alias := range aiemployee.MentionAliases(cfg) {
				m.byAtName[alias] = cfg
			}
		}
		m.logger.Printf("[AE] Registered: %s (%s) → pipeline=%s", cfg.Name, cfg.AtName, cfg.Pipeline)
	}
}

// SetChannelChatID stores a dynamic chat ID and channel name for channel-based DE replies.
func (m *AIEmployeeManager) SetChannelChatID(employeeID, channelName, chatID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.channelChatIDs[employeeID] = channelName + ":" + chatID
}

func (m *AIEmployeeManager) putExternalCodingCancel(taskID string, cancel context.CancelFunc) {
	if m == nil || taskID == "" || cancel == nil {
		return
	}
	m.mu.Lock()
	if m.externalCancels == nil {
		m.externalCancels = make(map[string]context.CancelFunc)
	}
	m.externalCancels[taskID] = cancel
	m.mu.Unlock()

	if m.gateway != nil {
		m.gateway.tasksMu.Lock()
		if task, ok := m.gateway.tasks[taskID]; ok {
			task.Cancel = cancel
		}
		m.gateway.tasksMu.Unlock()
	}
}

func (m *AIEmployeeManager) bindExternalCodingGatewayTask(taskID string, task *GatewayTask) bool {
	if m == nil || taskID == "" || task == nil {
		return false
	}
	m.mu.RLock()
	cancel := m.externalCancels[taskID]
	m.mu.RUnlock()
	if cancel == nil {
		return false
	}
	task.Cancel = cancel
	return true
}

func (m *AIEmployeeManager) clearExternalCodingCancel(taskID string) {
	if m == nil || taskID == "" {
		return
	}
	m.mu.Lock()
	delete(m.externalCancels, taskID)
	m.mu.Unlock()
}

// MatchAtName checks if a message @mentions a known digital employee.
func (m *AIEmployeeManager) MatchAtName(message string) (*AIEmployeeConfig, string) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// Route the first explicit, complete @mention in the message. A global
	// "longest alias wins" scan can route a later employee instead of the one
	// the user addressed first, while a raw substring match makes
	// "@交易员助理" accidentally activate "@交易员".
	bestIndex := -1
	bestAlias := ""
	bestMention := ""
	var bestConfig *AIEmployeeConfig
	for atName, cfg := range m.byAtName {
		// Defensive check for managers populated directly by tests/legacy code or
		// an alias map created before internal_only was applied.
		if cfg == nil || cfg.InternalOnly {
			continue
		}
		idx, mention, ok := aiemployee.FindExplicitMention(message, atName)
		if !ok {
			continue
		}
		if bestIndex < 0 || idx < bestIndex ||
			(idx == bestIndex && (len(mention) > len(bestMention) ||
				(len(mention) == len(bestMention) && atName < bestAlias))) {
			bestIndex = idx
			bestAlias = atName
			bestMention = mention
			bestConfig = cfg
		}
	}
	if bestConfig != nil {
		return bestConfig, aiemployee.CleanMatchedTask(message, bestMention)
	}

	return nil, ""
}

func truthyQuery(r *http.Request, key string) bool {
	if r == nil {
		return false
	}
	v := strings.TrimSpace(r.URL.Query().Get(key))
	switch strings.ToLower(v) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

// internalAEDiagnosticsRequested requires an explicit double opt-in so a
// generic "show hidden" UI action never reveals compatibility-only employees.
func internalAEDiagnosticsRequested(r *http.Request) bool {
	return truthyQuery(r, "include_hidden") && truthyQuery(r, "include_internal")
}

type aeDispatchContextKey string

const (
	aeDispatchChannelKey    aeDispatchContextKey = "ae_dispatch_channel"
	aeDispatchRecipientKey  aeDispatchContextKey = "ae_dispatch_recipient"
	aeDispatchUserIDKey     aeDispatchContextKey = "ae_dispatch_user_id"
	aeSuppressResultChatKey aeDispatchContextKey = "ae_suppress_result_chat"
)

func withAESuppressResultChatPersistence(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, aeSuppressResultChatKey, true)
}

func aeSuppressesResultChatPersistence(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	value, _ := ctx.Value(aeSuppressResultChatKey).(bool)
	return value
}

func withAEChannelContext(ctx context.Context, channel, recipient, userID string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if strings.TrimSpace(channel) != "" {
		ctx = context.WithValue(ctx, aeDispatchChannelKey, channel)
	}
	if strings.TrimSpace(recipient) != "" {
		ctx = context.WithValue(ctx, aeDispatchRecipientKey, recipient)
	}
	if strings.TrimSpace(userID) != "" {
		ctx = context.WithValue(ctx, aeDispatchUserIDKey, userID)
	}
	return ctx
}

func aeChannelContext(ctx context.Context) (channel, recipient, userID string) {
	if ctx == nil {
		return "", "", ""
	}
	if v, ok := ctx.Value(aeDispatchChannelKey).(string); ok {
		channel = strings.TrimSpace(v)
	}
	if v, ok := ctx.Value(aeDispatchRecipientKey).(string); ok {
		recipient = strings.TrimSpace(v)
	}
	if v, ok := ctx.Value(aeDispatchUserIDKey).(string); ok {
		userID = strings.TrimSpace(v)
	}
	return channel, recipient, userID
}

func (m *AIEmployeeManager) DispatchFromChannel(ctx context.Context, employeeID, task, sessionID, channel, recipient, userID string) (*AEWorkRecord, error) {
	if strings.TrimSpace(channel) != "" && strings.TrimSpace(recipient) != "" {
		m.SetChannelChatID(employeeID, channel, recipient)
	}
	return m.Dispatch(withAEChannelContext(ctx, channel, recipient, userID), employeeID, task, sessionID)
}

// DispatchExplicitMention is the only entrypoint that certifies a task as
// originating from a successful MatchAtName call. Professional-only employee
// pipelines reject ordinary Dispatch calls so webhook, cron, autonomous, and
// delegate paths cannot silently bypass the user's explicit @ requirement.
func (m *AIEmployeeManager) DispatchExplicitMention(ctx context.Context, employeeID, task, sessionID string) (*AEWorkRecord, error) {
	return m.Dispatch(withExplicitAEMentionGrant(ctx, employeeID), employeeID, task, sessionID)
}

func (m *AIEmployeeManager) DispatchExplicitMentionFromChannel(ctx context.Context, employeeID, task, sessionID, channel, recipient, userID string) (*AEWorkRecord, error) {
	if strings.TrimSpace(channel) != "" && strings.TrimSpace(recipient) != "" {
		m.SetChannelChatID(employeeID, channel, recipient)
	}
	ctx = withAEChannelContext(ctx, channel, recipient, userID)
	return m.DispatchExplicitMention(ctx, employeeID, task, sessionID)
}

type aePreflightResolvedFile struct {
	Mention string
	Path    string
	Size    int64
	IsDir   bool
}

func (m *AIEmployeeManager) buildAIEmployeePreflightContext(cfg *AIEmployeeConfig, userTask string) string {
	if cfg == nil || !aiEmployeePreflightEnabled(cfg.ID) {
		return ""
	}
	workspace := m.aiEmployeeWorkspace()
	mentions := extractLocalFileMentions(userTask)
	if workspace == "" || len(mentions) == 0 {
		return ""
	}

	var resolved []aePreflightResolvedFile
	var unresolved []string
	seenPath := map[string]bool{}
	for _, mention := range mentions {
		matches := resolveLocalFileMention(workspace, mention, 3)
		if len(matches) == 0 {
			unresolved = append(unresolved, mention)
			continue
		}
		for _, match := range matches {
			key := strings.ToLower(filepath.Clean(match.Path))
			if seenPath[key] {
				continue
			}
			seenPath[key] = true
			resolved = append(resolved, match)
		}
	}
	if len(resolved) == 0 && len(unresolved) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("## AI员工任务预解析\n")
	b.WriteString("系统已解析用户任务中的本地资源。后续步骤应优先使用这里的 resolved 文件或目录；`.openkitty/logs` / Cold data 是内部会话日志，不是业务数据源，除非用户明确要求读取日志。\n")
	if len(resolved) > 0 {
		b.WriteString("\n### Resolved local resources\n")
		for _, f := range resolved {
			fmt.Fprintf(&b, "- `%s` -> `%s`", f.Mention, f.Path)
			if f.IsDir {
				b.WriteString(" (directory)")
			} else if f.Size > 0 {
				fmt.Fprintf(&b, " (%d bytes)", f.Size)
			}
			b.WriteString("\n")
		}
	}
	if len(unresolved) > 0 {
		b.WriteString("\n### Unresolved local file mentions\n")
		for _, mention := range uniqueSortedStrings(unresolved) {
			fmt.Fprintf(&b, "- `%s`\n", mention)
		}
	}
	return b.String()
}

func (m *AIEmployeeManager) aiEmployeeWorkspace() string {
	if m != nil && m.gateway != nil && m.gateway.config != nil && strings.TrimSpace(m.gateway.config.Workspace) != "" {
		return strings.TrimSpace(m.gateway.config.Workspace)
	}
	wd, _ := os.Getwd()
	return wd
}

func aiEmployeePreflightEnabled(employeeID string) bool {
	switch strings.TrimSpace(employeeID) {
	case "data-analyst",
		"meeting-secretary",
		"customer-service",
		"hr-recruiter",
		"lead-researcher",
		"lead-acquisition":
		return true
	default:
		return false
	}
}

var localFileMentionPattern = regexp.MustCompile(`(?i)(?:[a-z]:[\\/][^\s"'<>，。；;、]+|(?:\.{1,2}[\\/])?[^\s"'<>，。；;、]+?\.(?:csv|tsv|xlsx?|json|txt|md|docx?|pdf|pptx?|png|jpe?g|webp|gif|mp3|wav|m4a))`)

func extractLocalFileMentions(text string) []string {
	matches := localFileMentionPattern.FindAllString(text, -1)
	if len(matches) == 0 {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, match := range matches {
		match = cleanLocalFileMention(match)
		if match == "" {
			continue
		}
		key := strings.ToLower(match)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, match)
	}
	return out
}

func cleanLocalFileMention(path string) string {
	path = strings.TrimSpace(path)
	path = strings.Trim(path, "`'\"“”‘’()[]{}<>，。；;、")
	if regexp.MustCompile(`(?i)^[a-z]:[\\/]`).MatchString(path) {
		path = trimPipelineTargetSentenceSuffix(path)
	}
	for strings.HasSuffix(path, ".") || strings.HasSuffix(path, ",") || strings.HasSuffix(path, ":") {
		path = strings.TrimSuffix(path, path[len(path)-1:])
	}
	return strings.TrimSpace(path)
}

func resolveLocalFileMention(workspace, mention string, limit int) []aePreflightResolvedFile {
	workspace = strings.TrimSpace(workspace)
	mention = cleanLocalFileMention(mention)
	if workspace == "" || mention == "" {
		return nil
	}
	if limit <= 0 {
		limit = 3
	}

	var candidates []string
	add := func(path string) {
		path = strings.TrimSpace(path)
		if path == "" {
			return
		}
		candidates = append(candidates, path)
	}

	fromSlash := filepath.FromSlash(strings.ReplaceAll(mention, "\\", "/"))
	if filepath.IsAbs(fromSlash) {
		add(fromSlash)
	} else if strings.ContainsAny(mention, `/\`) {
		add(filepath.Join(workspace, fromSlash))
	} else {
		for _, base := range []string{
			workspace,
			filepath.Join(workspace, ".openkitty", "storage"),
			filepath.Join(workspace, "storage"),
			filepath.Join(workspace, "output"),
		} {
			add(filepath.Join(base, mention))
		}
		for _, found := range findLocalFileByBaseName(workspace, mention, limit) {
			add(found)
		}
	}

	seen := map[string]bool{}
	var out []aePreflightResolvedFile
	for _, candidate := range candidates {
		abs, err := filepath.Abs(candidate)
		if err != nil {
			continue
		}
		key := strings.ToLower(filepath.Clean(abs))
		if seen[key] {
			continue
		}
		seen[key] = true
		info, err := os.Stat(abs)
		if err != nil {
			continue
		}
		out = append(out, aePreflightResolvedFile{Mention: mention, Path: abs, Size: info.Size(), IsDir: info.IsDir()})
		if len(out) >= limit {
			break
		}
	}
	return out
}

func findLocalFileByBaseName(workspace, baseName string, limit int) []string {
	if limit <= 0 {
		limit = 3
	}
	root, err := filepath.Abs(workspace)
	if err != nil {
		return nil
	}
	target := strings.ToLower(baseName)
	var found []string
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d == nil {
			return nil
		}
		if d.IsDir() {
			if shouldSkipAEPreflightSearchDir(root, path, d.Name()) {
				return filepath.SkipDir
			}
			if aePreflightPathDepth(root, path) > 4 {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.EqualFold(d.Name(), target) {
			found = append(found, path)
			if len(found) >= limit {
				return filepath.SkipAll
			}
		}
		return nil
	})
	return found
}

func shouldSkipAEPreflightSearchDir(root, path, name string) bool {
	lowerName := strings.ToLower(name)
	switch lowerName {
	case ".git", "node_modules", "all", "skills", "toadd_skills", ".claude", ".codex", ".codex-tmp", "__pycache__":
		return true
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	rel = strings.ToLower(filepath.ToSlash(rel))
	return strings.HasPrefix(rel, ".openkitty/logs") || strings.HasPrefix(rel, ".openkitty/runtime")
}

func aePreflightPathDepth(root, path string) int {
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == "." {
		return 0
	}
	rel = filepath.ToSlash(rel)
	return strings.Count(rel, "/") + 1
}

// Dispatch starts a pipeline for a digital employee task.
func (m *AIEmployeeManager) Dispatch(ctx context.Context, employeeID, task, sessionID string) (*AEWorkRecord, error) {
	originalTask := task
	displayTask := task
	m.mu.RLock()
	cfg, ok := m.employees[employeeID]
	m.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("employee '%s' not found", employeeID)
	}
	// Internal-only employees have no ordinary user/automation entrypoint. The
	// sealed explicit-dispatch grant remains available to trusted programmatic
	// diagnostics and compatibility tests; users cannot obtain it because no
	// @ aliases are registered for internal-only configs.
	if cfg.InternalOnly && !hasExplicitAEMentionGrant(ctx, employeeID) {
		return nil, fmt.Errorf("employee '%s' is internal-only and cannot be dispatched", employeeID)
	}

	if cfg.Tier == "commercial" {
		tenantID := cfg.TenantID
		if tenantID == "" {
			tenantID = "default"
		}
		if m.gateway == nil || !m.gateway.tenantIsLicensed(tenantID) {
			return nil, fmt.Errorf("%s 需要商业版授权才能使用", cfg.Name)
		}
	}

	if m.gateway == nil {
		return nil, fmt.Errorf("gateway not available")
	}
	if agentType, ok := externalCodingEmployeeAgent(employeeID); ok {
		return m.DispatchExternalCodingEmployee(ctx, cfg, agentType, displayTask, task, sessionID)
	}
	if m.gateway.pipelineExec == nil {
		return nil, fmt.Errorf("pipeline executor not available")
	}
	professionalOnlyPipeline := m.gateway.pipelineExec.IsProfessionalOnlyPipeline(cfg.Pipeline)
	if professionalOnlyPipeline && !hasExplicitAEMentionGrant(ctx, employeeID) {
		return nil, fmt.Errorf("%s only starts from an explicit @%s mention", cfg.Name, cfg.AtName)
	}
	if !professionalOnlyPipeline {
		if err := m.validateDispatchModelReady(cfg); err != nil {
			return nil, err
		}
	}

	// Waiting-input executions are terminal. A new explicit @ invocation always
	// starts from the first step with only its own request; ordinary messages
	// must never be merged into a prior AI employee task.
	m.supersedeWaitingInputForExplicitInvocation(ctx, employeeID, sessionID)

	// 任务队列：如果正在忙，排队等待
	m.mu.Lock()
	if m.busy[employeeID] {
		// Create queue channel if not exists
		if m.taskQueue[employeeID] == nil {
			m.taskQueue[employeeID] = make(chan deQueueItem, 20)
		}
		queue := m.taskQueue[employeeID]
		m.mu.Unlock()
		m.logger.Printf("[AE] %s is busy, queuing task: '%s'", cfg.Name, task)
		select {
		case queue <- deQueueItem{Task: task, SessionID: sessionID, Ctx: ctx}:
			return &AEWorkRecord{ID: "queued", EmployeeID: employeeID, Task: task, Status: "queued", CreatedAt: time.Now()}, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	m.busy[employeeID] = true
	m.mu.Unlock()
	defer func() { m.mu.Lock(); m.busy[employeeID] = false; m.mu.Unlock() }()

	// 多轮记忆：把最近对话摘要注入任务上下文
	useMultiTurnContext := cfg.RAGMultiTurn
	if conv, ok := m.conversations[employeeID]; useMultiTurnContext && ok && len(conv.History) > 0 {
		memoryCtx := "## 最近对话记忆\n"
		for _, h := range conv.History {
			if cleaned := sessioncore.CleanCarryoverContent("user", h, 200); cleaned != "" {
				memoryCtx += "- " + cleaned + "\n"
			}
		}
		if strings.TrimSpace(memoryCtx) != "## 最近对话记忆" {
			task = memoryCtx + "\n## 当前任务\n" + task
		}
	}
	// Record this interaction
	m.mu.Lock()
	if m.conversations[employeeID] == nil {
		m.conversations[employeeID] = &aeConversation{}
	}
	conv := m.conversations[employeeID]
	if historyTask := sessioncore.CleanCarryoverContent("user", originalTask, 100); historyTask != "" {
		conv.History = append(conv.History, fmt.Sprintf("[%s] %s", time.Now().Format("15:04"), historyTask))
	}
	if len(conv.History) > 5 {
		conv.History = conv.History[1:]
	}
	conv.LastTalk = time.Now()
	m.mu.Unlock()

	// Business hours check
	if cfg.WorkingHours != "" {
		if !aiemployee.IsWithinWorkingHours(cfg.WorkingHours) {
			task = "[非工作时间 - 当前 " + time.Now().Format("15:04") + "，工作时间 " + cfg.WorkingHours + "]\n" + task
		}
	}

	// Customer tracking
	if cfg.Channel != "" && sessionID != "" && gatewayDB != nil {
		// Extract external user ID from session or channel data
		extID := sessionID
		if cust, err := projectctx.FindOrCreateCustomer(gatewayDB, cfg.TenantID, cfg.Channel, extID, "", ""); err == nil {
			ctx = projectctx.InjectCustomerIntoContext(ctx, cust)
		}
	}

	// Inject RAG config into context for tools
	kbIDs := resolveKBSlugs(cfg.RAGKBSlugs, cfg.TenantID)
	if len(cfg.RAGKBIDs) > 0 {
		kbIDs = append(kbIDs, cfg.RAGKBIDs...)
	}
	ctx = context.WithValue(ctx, "rag_kb_ids", kbIDs)
	ctx = context.WithValue(ctx, "rag_multi_turn", cfg.RAGMultiTurn)
	ctx = context.WithValue(ctx, "rag_tenant_id", cfg.TenantID)
	ctx = context.WithValue(ctx, "ae_system_prompt", cfg.SystemMessage)
	ctx = context.WithValue(ctx, "ae_name", cfg.Name)
	ctx = context.WithValue(ctx, "ae_model_binding", cfg.ModelBinding)
	ctx = context.WithValue(ctx, "ae_employee_id", employeeID)
	ctx = withEmployeeExecutionGrant(ctx, employeeID, cfg.TenantID)
	ctx = permissionpolicy.WithUserToolPolicyInput(ctx, originalTask)
	if !useMultiTurnContext {
		ctx = context.WithValue(ctx, "pipeline_isolated_context", true)
	}

	// Assemble task prefix: conversation history + DE identity + customer identity
	var taskPrefix []string

	// Layer 0: Recent conversation history — enables multi-turn dialogue
	if useMultiTurnContext && sessionID != "" && gatewayDB != nil {
		chatCtx := loadSessionContext(sessionID, 6)
		if chatCtx != "" {
			taskPrefix = append(taskPrefix, chatCtx)
		}
		// Also inject as structured history_messages for the LLM
		var messages []ChatMessage
		historyQuery := gatewayDB.Where("session_id = ? AND role IN ('user', 'assistant')", sessionID)
		if currentTaskID := strings.TrimSpace(taskIDFromContext(ctx)); currentTaskID != "" {
			historyQuery = historyQuery.Where("task_id != ?", currentTaskID)
		}
		historyQuery.
			Order("created_at desc").
			Limit(50).
			Find(&messages)
		if len(messages) > 0 {
			// Reverse to chronological order
			for i, j := 0, len(messages)-1; i < j; i, j = i+1, j-1 {
				messages[i], messages[j] = messages[j], messages[i]
			}
			var historyMsgs []map[string]interface{}
			for _, msg := range messages {
				role := "user"
				if msg.Role == "assistant" {
					role = "assistant"
				}
				limit := 2000
				if role == "assistant" {
					limit = 1200
				}
				content := sessioncore.CleanCarryoverContent(role, msg.Content, limit)
				if content == "" {
					continue
				}
				historyMsgs = append(historyMsgs, map[string]interface{}{
					"role":    role,
					"content": content,
				})
			}
			if len(historyMsgs) > 0 {
				ctx = context.WithValue(ctx, "history_messages", historyMsgs)
				m.logger.Printf("[AE] Injected %d history messages for session %s", len(historyMsgs), sessionID)
			}
		}
	}
	// Layer 1: DE system prompt — defines who this employee IS and what boundaries they have
	if cfg.SystemMessage != "" {
		taskPrefix = append(taskPrefix, cfg.SystemMessage)
	}
	// Layer 2: Customer identity — who they're talking to
	if globalIdentityInjector != nil && cfg.Channel != "" && sessionID != "" {
		if prefix, _, err := globalIdentityInjector.Inject(cfg.Channel, sessionID); err == nil && prefix != "" {
			taskPrefix = append(taskPrefix, prefix)
		}
	}
	if len(taskPrefix) > 0 {
		task = strings.Join(taskPrefix, "\n\n") + "\n\n---\n\n" + task
	}
	if preflight := m.buildAIEmployeePreflightContext(cfg, originalTask); preflight != "" {
		task = preflight + "\n\n---\n\n" + task
	}

	pipelineName := cfg.Pipeline
	if employeeID == "investment-advisor" {
		pipelineName = "investment-advisory"
	} else if cfg.Direct != nil {
		pipelineName = "direct_" + employeeID
		maxIter := cfg.Direct.MaxIter
		if maxIter <= 0 {
			maxIter = 8
		}
		directTools := aiemployee.DirectToolsForRequest(cfg.Direct, originalTask)
		// Register a dynamic pipeline for this DE. Domain employees can expand
		// the direct response into evidence-first stages without changing the
		// persisted DirectConfig schema.
		dynamicPipeline := buildAIEmployeeDirectPipeline(
			employeeID,
			pipelineName,
			cfg.Direct,
			directTools,
			maxIter,
			originalTask,
		)
		m.gateway.pipelineExec.RegisterDynamic(pipelineName, dynamicPipeline)
		// Register api_call tool if API tokens configured
		if len(cfg.APITokens) > 0 {
			m.gateway.registerAPICallTool(cfg.APITokens)
		}
	}
	if cfg.Direct == nil && employeeID == "content-editor" {
		if customPipeline, ok := m.wechatArticleCustomOutputPipeline(pipelineName, originalTask); ok {
			pipelineName = customPipeline.Name
			m.gateway.pipelineExec.RegisterDynamic(pipelineName, customPipeline)
		}
	}
	if cfg.Direct == nil && employeeID == "data-analyst" {
		if dataAnalystWantsPresentationArtifact(originalTask) {
			if pptPipeline, ok := m.dataAnalystPresentationPipeline(pipelineName, originalTask); ok {
				pipelineName = pptPipeline.Name
				m.gateway.pipelineExec.RegisterDynamic(pipelineName, pptPipeline)
			}
		} else if dataAnalystCanUseLocalFastPath(originalTask) {
			fastPipeline := dataAnalystLocalFastPipeline()
			pipelineName = fastPipeline.Name
			m.gateway.pipelineExec.RegisterDynamic(pipelineName, fastPipeline)
		}
	}

	// Every user-facing pipeline employee receives the same bounded intake
	// contract. Session context is host-frozen and explicitly labelled as a
	// candidate, so the first step can reuse a just-completed result without
	// allowing unrelated chat history to silently become task facts.
	if !professionalOnlyPipeline {
		contextPath, contextBlock := m.materializeAESessionContext(sessionID)
		if contextBlock != "" {
			task = task + "\n\n---\n\n" + contextBlock
		}
		if _, ok := m.gateway.resolveAEPipeline(pipelineName); ok {
			ctx = aiemployee.WithIntakeSpec(ctx, aiemployee.IntakeSpec{
				EmployeeID:     employeeID,
				EmployeeName:   cfg.Name,
				EmployeeAtName: cfg.AtName,
				Role:           cfg.Role,
				PipelineName:   pipelineName,
				ContextPath:    contextPath,
				OriginalTask:   originalTask,
			})
		}
	}

	// A professional-only pipeline is an isolated protocol boundary, not an
	// LLM prompt. Forward the user's original payload byte-for-byte (apart from
	// the caller's outer whitespace) so employee personality, history,
	// preflight text, working-hours notices, and identity context cannot corrupt
	// JSON envelopes or become executable input for the child agent.
	if professionalOnlyPipeline && m.gateway.pipelineExec.IsProfessionalOnlyPipeline(pipelineName) {
		task = strings.TrimSpace(originalTask)
	}
	var exec *PipelineExecution
	var record *AEWorkRecord
	parentTaskID := strings.TrimSpace(taskIDFromContext(ctx))
	if recoveredExec, found := m.gateway.pipelineExec.FindRecoverableExecutionByTaskID(parentTaskID, "", employeeID); found {
		exec = recoveredExec
		if exec.Status != "waiting_approval" {
			var resumeErr error
			exec, resumeErr = m.gateway.pipelineExec.ResumeInterrupted(ctx, exec.ID)
			if resumeErr != nil {
				return nil, fmt.Errorf("resume pipeline '%s': %w", pipelineName, resumeErr)
			}
		}
		record = m.findWorkRecordByExecID(exec.ID)
		m.logger.Printf("[AE] %s reclaimed pipeline execution %s for parent task %s", cfg.Name, exec.ID, parentTaskID)
	}
	var err error
	if exec == nil {
		exec, err = m.gateway.pipelineExec.Run(ctx, pipelineName, task, sessionID, RunModeInteractive, "")
	}
	if err != nil {
		return nil, fmt.Errorf("start pipeline '%s': %w", pipelineName, err)
	}

	if record == nil {
		record = &AEWorkRecord{
			ID:          fmt.Sprintf("dewr_%s_%d", employeeID, time.Now().UnixNano()),
			EmployeeID:  employeeID,
			Task:        displayTask,
			Pipeline:    pipelineName,
			ExecID:      exec.ID,
			Status:      "running",
			SessionID:   sessionID,
			ChannelUser: sessionID, // For channel @reply: extract user name/ID from session
			CreatedAt:   time.Now(),
			UpdatedAt:   time.Now(),
		}
		m.mu.Lock()
		m.workLog[employeeID] = append(m.workLog[employeeID], record)
		m.mu.Unlock()
		m.saveWorkRecordToDB(record)
	} else {
		m.mu.Lock()
		record.Status = "running"
		record.UpdatedAt = time.Now()
		m.mu.Unlock()
		m.saveWorkRecordToDB(record)
	}

	m.logger.Printf("[AE] %s dispatched: task='%s' pipeline=%s exec=%s", cfg.Name, displayTask, pipelineName, exec.ID)

	// Notify supervisor that task started (async)
	startMessage := fmt.Sprintf("任务已启动\n任务: %s\nPipeline: %s\n执行ID: %s", displayTask, pipelineName, exec.ID)
	if planMessage := m.aeWorkflowPlanMessage(record); planMessage != "" {
		startMessage += "\n\n" + planMessage
	}
	go m.notifySupervisor(cfg, startMessage)

	// Watch pipeline for HITL and completion notifications
	// Use background context so monitoring survives HTTP response
	watchCtx := context.Background()
	if aeSuppressesResultChatPersistence(ctx) {
		watchCtx = withAESuppressResultChatPersistence(watchCtx)
	}
	go m.watchPipeline(watchCtx, cfg, record)

	return record, nil
}

func (m *AIEmployeeManager) wechatArticleCustomOutputPipeline(baseName, task string) (Pipeline, bool) {
	if m == nil || m.gateway == nil || m.gateway.config == nil {
		return Pipeline{}, false
	}
	workspace := strings.TrimSpace(m.gateway.config.Workspace)
	var mdPath, htmlPath string
	for _, mention := range extractLocalFileMentions(task) {
		rel, ok := wechatStorageRelativePath(workspace, mention)
		if !ok {
			continue
		}
		switch strings.ToLower(filepath.Ext(rel)) {
		case ".md", ".markdown":
			if mdPath == "" {
				mdPath = rel
			}
		case ".html", ".htm":
			if htmlPath == "" {
				htmlPath = rel
			}
		}
	}
	if mdPath == "" || htmlPath == "" {
		return Pipeline{}, false
	}
	base, ok := m.gateway.resolveAEPipeline(baseName)
	if !ok || len(base.Steps) < 2 {
		return Pipeline{}, false
	}
	steps := append([]PipelineStep(nil), base.Steps...)
	delivery := &steps[len(steps)-1]
	contract := PipelineArtifactContract{Paths: []string{mdPath, htmlPath}, MinBytes: 500, RequireRead: false, TextProvenance: true, RejectPlaceholders: true}
	if delivery.ArtifactContract != nil {
		contract = *delivery.ArtifactContract
		contract.Paths = []string{mdPath, htmlPath}
	}
	delivery.ArtifactContract = &contract
	delivery.Prompt += fmt.Sprintf(`

User-requested output override (mandatory):
- wechat_article_bundle md_path must be exactly %s
- wechat_article_bundle html_path must be exactly %s
- These exact user paths override the template's run-scoped default names.
- Do not place the full OPENKITTY source SHA-256 or execution ID in visible Markdown body text. The bundle tool adds full invisible provenance and returns the full hash in its receipt/final answer. If the article footer needs a reader-visible fingerprint, use at most the first 12 hexadecimal characters and label it 来源内容指纹.`, mdPath, htmlPath)
	name := fmt.Sprintf("wechat-article-custom-%d", time.Now().UnixNano())
	return Pipeline{Name: name, Desc: base.Desc, Steps: steps}, true
}

func wechatStorageRelativePath(workspace, path string) (string, bool) {
	path = portablepath.Normalize(path)
	if path == "" {
		return "", false
	}
	if portablepath.IsAbs(path) {
		if strings.TrimSpace(workspace) == "" {
			return "", false
		}
		rel, ok := portablepath.RelativeTo(workspace, path)
		if !ok {
			return "", false
		}
		path = rel
	}
	path = portablepath.Normalize(path)
	if path == ".." || strings.HasPrefix(path, "../") {
		return "", false
	}
	if !strings.HasPrefix(strings.ToLower(path), ".openkitty/storage/") {
		return "", false
	}
	return path, true
}

func aiEmployeeDirectPrompt(employeeID, base string) string {
	base = strings.TrimSpace(base)
	if employeeID != "meeting-secretary" {
		return base
	}
	boundary := `

Grounding boundary for meeting and office records:
- Treat the user's text and referenced materials as a closed, complete factual source. Every factual table cell and sentence in the official record must be directly traceable to explicit source wording.
- Never invent attendees, owners, dates, times, percentages, thresholds, rollout stages, budgets, decisions, reminder cadence, dependencies, causal impacts, status, or default responsibility. Dates appearing in sequence do not establish a dependency or a number of working days. A named responsible person is not automatically a meeting attendee.
- Preserve the source verb: "负责" does not mean "已完成"; a deadline does not prove a reminder, review, or intermediate milestone.
- Preserve partial dates exactly as supplied. Never add a year, weekday, time, timezone, or date precision from the current clock or surrounding context.
- Risks may only quote or closely paraphrase an explicit source blocker, unresolved decision, or if-then consequence. Never append phrases such as "may affect", "may cause", "可能影响", "可能导致", or "威胁" unless that consequence is explicit in the source. Do not derive additional schedule, quality, procurement, analytics, resource, or business impact.
- A "pending confirmation" section may include only questions explicitly unresolved by the source. Do not enumerate other fields that a typical meeting might contain. Never prefill examples, sample values, named owners, proposed schedules, or suggested answers.
- When a user-requested field has no source value, write only "原文未提供". Do not fill the gap by convention or recommendation.
- Do not add standard-template fields or sections (for example meeting location, attendees, host, recorder, reminder plan, or dependency table) unless the user requested that field or the source explicitly contains it.
- Keep all recommendations and inferences out of the official minutes unless the user explicitly asks for them; if asked, place them in a separately labelled non-factual appendix.

会议纪要事实边界（必须遵守）：逐字段封闭抽取；日期不得补年份或星期；不得按办公惯例补写参会人、地点、依赖关系、默认责任人、工作日间隔、影响或提醒。风险只复述原文明确的风险/条件后果，不得自行接“可能影响/可能导致”；待确认项只保留原文明说尚未决定的事项。`
	return strings.TrimSpace(base + boundary)
}

func buildAIEmployeeDirectPipeline(employeeID, pipelineName string, direct *DirectConfig, directTools []string, maxIter int, originalTask string) Pipeline {
	responseStep := PipelineStep{
		Name:           "respond",
		Prompt:         aiEmployeeDirectPrompt(employeeID, direct.Prompt),
		Tools:          append([]string(nil), directTools...),
		RequiredSkills: append([]string(nil), direct.RequiredSkills...),
		MaxIter:        maxIter,
	}
	if strings.TrimSpace(employeeID) == "market-analyst" &&
		aiemployee.ExplicitArtifactRequest(permissionpolicy.TrustedDirectiveText(originalTask)) {
		return buildMarketAnalystArtifactPipeline(pipelineName, responseStep)
	}
	if strings.TrimSpace(employeeID) != "fundraising-advisor" {
		return Pipeline{Name: pipelineName, Steps: []PipelineStep{responseStep}}
	}
	if aiemployee.TaskRequestsInteractiveHandoff(employeeID, originalTask) {
		// A user-authored choice or confirmation is the complete deliverable for
		// this turn. Keep that explicit interaction ahead of both research and
		// professional analysis; the next fully specified invocation will use
		// the normal evidence-first fundraising pipeline.
		responseStep.Name = "融资需求选择与确认"
		responseStep.NoTools = true
		responseStep.Tools = nil
		responseStep.RequiredSkills = nil
		responseStep.MaxIter = 2
		return Pipeline{Name: pipelineName, Steps: []PipelineStep{responseStep}}
	}

	createBPDeck := fundraisingBPDeckRequest(originalTask)
	researchTools := make([]string, 0, len(directTools))
	for _, tool := range directTools {
		switch strings.TrimSpace(tool) {
		case "read_file", "extract", "vision", "web_search", "web_fetch":
			researchTools = append(researchTools, tool)
		}
	}
	deliveryTools := make([]string, 0, len(responseStep.Tools))
	for _, tool := range responseStep.Tools {
		if createBPDeck {
			switch strings.TrimSpace(tool) {
			case "use_skill", "read_file", "extract", "vision":
				// Research owns evidence acquisition and the next stage owns
				// file production. Keep only the host skill-activation channel
				// plus narrowly scoped source inspection in the BP-writing
				// stage, so an explicit request for both BP and PPT cannot pull
				// the finance writer into presentation tooling or web searches.
			default:
				continue
			}
		}
		switch strings.TrimSpace(tool) {
		case "list_skills":
			// RequiredSkills identifies the skill exactly, so discovery is
			// unnecessary. Keep use_skill in the allowlist because the host
			// uses that channel to activate RequiredSkills before model work.
			continue
		case "persistent_shell":
			if aiemployee.FundraisingBPOnlyRequest(originalTask) {
				// A chat-first BP draft needs evidence and writing, not an
				// internal diagnostic script or arbitrary shell exploration.
				continue
			}
			if createBPDeck {
				// The dedicated presentation step owns all renderer commands.
				continue
			}
		case "write_file":
			if createBPDeck {
				// Keep the finance step content-only. The presentation step has
				// a run-scoped PPTX artifact contract and is the only writer.
				continue
			}
		}
		deliveryTools = append(deliveryTools, tool)
	}
	responseStep.Tools = deliveryTools
	responseStep.Name = "融资专业分析与交付"
	responseStep.Prompt = strings.TrimSpace(responseStep.Prompt + `

上游已经读取指定材料并完成必要的公司、产品与对比研究。以它为事实起点，使用融资专业 skill 完成用户真正要求的交付；不要展示研究底稿、内部步骤、skill 名或工具日志。

材料角色以用户语义为准。用户称为自有项目资料的 URL、附件或正文可以支撑 BP，标为项目方材料陈述；明确的竞品、对标或参考产品才保持为外部对象。若上游因 URL 形式误判了材料角色，以原始用户表达纠正。

制作 BP 时直接完成当前版本，不附融资准备度问卷或评分。把材料中已有的定位、问题、产品、能力与差异写充分，并主动补出有依据的目标客户、商业模式、获客路径、竞争策略、阶段目标、资金用途类别与风险对策。需要数字时自行给出明确标注的“规划估算（非项目事实）”、关键假设与推导依据；用户未给融资金额时，必须基于 runway、团队、算力与市场投入给出至少一套建议金额或区间和用途比例，不得只写金额未知，且不能冒充项目现状。把确实无法核实的主体、团队履历、真实客户、经营数据和融资历史集中列成“项目方后续替换清单”，说明用户应补什么；不要在每章反复输出“未知、待补、来源等级”。宣传性主张不能写成独立事实。除非用户明确要求互动，否则不要求确认、不以问题结尾。

其他任务按用户所问选择必要方法：只有明确要求准备度诊断或评分时才评分；条款解释遵循专业概念基准。`)
	if createBPDeck {
		responseStep.Prompt = strings.TrimSpace(responseStep.Prompt + `

本步骤只负责形成完整、可供下游直接排版的 BP 正文，不负责制作、保存、搜索制作方法或声称已经生成 PPTX。不得调用 write_file、persistent_shell 或其他文件制作工具，也不得再次调用 use_skill；宿主已经激活融资专业 skill。下一步“融资BP演示文稿生成”会独立读取本步骤正文并生成真实 PPTX，因此本步骤结尾不得写“本轮未要求 PPTX”“如需 PPTX 请明示”或把文件交付推回用户。`)
	}

	steps := []PipelineStep{
		{
			Name: "公司、产品与对比研究",
			Prompt: `建立当前投融资任务需要的事实底稿，不写 BP 正文，也不调用融资专业 skill。

先判断材料角色，判断依据是用户语义而非 URL 或文件形式：用户称为“我的/我们的/本公司/本项目/我产品的资料、参考资料或介绍材料”的内容属于项目方提供的标的材料；明确称为竞品、对标、参考产品或行业案例的内容才是外部参考。表述有歧义时结合最近修饰对象和整体任务作最有用的合理判断，不能把唯一可识别的融资标的降级成外部参考。

实际读取用户指定的 URL、附件、正文或文件，提取产品定位、目标客户、核心能力、使用场景、商业模式主张与公开进展；再按任务需要补充官网、官方文档和可靠独立来源，并从直接竞品、替代方案、人工流程或维持现状中选择真正有用的对比。一次抓取成功或明确失败后不重复同一路径，必要时换一个替代来源。

区分独立核验事实、项目方材料陈述、分析推断和未取得的真实数据。研究重点是给下游足够的实质内容，不是罗列空字段；无法取得的主体、团队履历、真实客户、经营数据与融资历史合并成一个简短缺口清单，不要重复输出“未知/来源等级”。不补造来源和已经发生的事实。

输出紧凑底稿：材料角色与标的、产品事实、对比结论、可用于 BP 的建议性推演、来源局限、项目方后续需要替换的真实数据。概念咨询只核验回答问题所需规则。`,
			Tools:           researchTools,
			MaxIter:         14,
			ContinueOnError: true,
		},
		responseStep,
	}
	if createBPDeck {
		steps = append(steps, fundraisingBPPresentationStep())
	}
	return Pipeline{Name: pipelineName, Steps: steps}
}

func buildMarketAnalystArtifactPipeline(pipelineName string, responseStep PipelineStep) Pipeline {
	research := responseStep
	research.Name = "市场证据研究"
	research.Tools = filterStrings(research.Tools, func(tool string) bool {
		return !strings.EqualFold(strings.TrimSpace(tool), "write_file")
	})
	if research.MaxIter < 12 {
		research.MaxIter = 12
	}
	research.Prompt += `

本步骤只完成证据研究和报告草稿，不声称文件已生成。为文件交付预留独立的下一步骤：达到足以回答用户问题的来源覆盖后停止继续搜索，输出完整草稿、具体 URL、来源日期、分析截至时间及证据局限，供下一步骤原样落盘。优先抓取原文；若某个具体页面经过一次有效抓取仍不可访问，可保留 web_search 实际返回的标题、发布方、日期和原始 URL，明确标为“检索索引线索/待复核”，只用于定性方向，不用它支撑精确数字。不得猜测或拼接 URL。`

	delivery := PipelineStep{
		Name:    "市场报告交付",
		Prompt:  `基于上一步已经核验或明确降级的证据，整理成最终市场研究报告；不得新增上一步未出现的事实或来源。报告正文必须保留每个关键结论对应的具体外部 URL、来源日期或数据期、分析截至时间，以及无法核验内容的降级说明。检索索引线索必须继续标记为待复核，不能升级为事实或精确数字。把完整正文写入当前产物契约指定的精确 Markdown 路径；写入后直接返回同一份完整报告和真实文件路径，不再搜索。如果 write_file 未能完成，也要输出完整报告正文，由宿主按产物契约安全落盘。`,
		Tools:   []string{"write_file"},
		MaxIter: 3,
		ArtifactContract: &PipelineArtifactContract{
			Paths:              []string{".openkitty/storage/market_analysis_${execution_id}.md"},
			MinBytes:           800,
			TextProvenance:     true,
			RejectPlaceholders: true,
			MaterializeOutput:  true,
		},
	}
	return Pipeline{Name: pipelineName, Steps: []PipelineStep{research, delivery}}
}

func filterStrings(values []string, keep func(string) bool) []string {
	filtered := make([]string, 0, len(values))
	for _, value := range values {
		if keep(value) {
			filtered = append(filtered, value)
		}
	}
	return filtered
}

func fundraisingBPDeckRequest(task string) bool {
	normalized := strings.ToLower(strings.TrimSpace(task))
	if normalized == "" || fundraisingTaskRejectsPresentationArtifact(normalized) {
		return false
	}
	explicitPresentation := containsAnyText(normalized, []string{
		"ppt", "pptx", "powerpoint", "pitch deck", "slide", "slides",
		"presentation", "deck", "幻灯片", "演示文稿", "演示稿", "路演稿",
	})
	hasBP := containsAnyText(normalized, []string{
		"bp", "商业计划书", "融资计划书", "business plan", "pitch deck",
	})
	if !hasBP {
		return false
	}
	if explicitPresentation {
		return true
	}
	return containsAnyText(normalized, []string{
		"帮我做", "给我做", "替我做", "做一个", "做一份",
		"制作", "生成", "创建", "撰写", "起草", "写一份", "写一个",
		"create", "make", "draft", "prepare", "build", "write a", "write an",
	})
}

func fundraisingTaskRejectsPresentationArtifact(task string) bool {
	normalized := strings.ToLower(strings.TrimSpace(task))
	return containsAnyText(normalized, []string{
		"不要保存", "别保存", "无需保存", "不需要保存", "不用保存", "不必保存",
		"不要下载", "别下载", "无需下载", "不需要下载", "不用下载",
		"不要导出", "别导出", "无需导出", "不需要导出", "不用导出",
		"不要文件", "不需要文件", "无需文件", "不用文件",
		"不要ppt", "不要 ppt", "不需要ppt", "不需要 ppt", "无需ppt", "无需 ppt", "不用ppt", "不用 ppt",
		"不要pptx", "不需要pptx", "无需pptx", "不用pptx",
		"不要幻灯片", "不需要幻灯片", "无需幻灯片", "不用幻灯片",
		"不要演示文稿", "不需要演示文稿", "无需演示文稿", "不用演示文稿",
		"只在聊天", "仅在聊天", "直接在聊天", "只在对话", "仅在对话",
		"do not save", "don't save", "without saving", "do not download",
		"do not export", "no file", "no ppt", "no pptx", "chat only",
	})
}

func fundraisingTaskRequiresChatOnly(task string) bool {
	normalized := strings.ToLower(strings.TrimSpace(task))
	return containsAnyText(normalized, []string{
		"不要保存", "别保存", "无需保存", "不需要保存", "不用保存", "不必保存",
		"不要下载", "别下载", "无需下载", "不需要下载", "不用下载",
		"不要导出", "别导出", "无需导出", "不需要导出", "不用导出",
		"不要文件", "不需要文件", "无需文件", "不用文件",
		"只在聊天", "仅在聊天", "直接在聊天", "只在对话", "仅在对话",
		"do not save", "don't save", "without saving", "do not download",
		"do not export", "no file", "chat only",
	})
}

func fundraisingBPPresentationStep() PipelineStep {
	return PipelineStep{
		Name: "融资BP演示文稿生成",
		Prompt: `把上游 BP 内容整理为 10–14 页投资人演示稿并交付真实可编辑 PPTX。宿主已经激活通用 ppt-art-director skill；不要再次调用同一技能或探测目录。生成必须使用纯 python-pptx/PptxGenJS 的跨平台方式；PowerPoint 或 LibreOffice 只用于成品渲染质检，不能成为生成依赖。

生产路线是硬规则：这是 8 页以上的正式投资人材料，默认使用脚手架生成本次任务独有的逐页 builder，不得把整套稿件压进通用紧凑 JSON，也不得调用 build_task_pptx.py。Windows 直接使用 python，macOS/Linux 直接使用 python3，首次写入或执行幻灯片生成代码的动作必须运行 "<Runtime Directory>/scripts/scaffold_task_pptx.py"，builder 固定为 ".openkitty/storage/fundraising_bp_work_${execution_id}/build_deck.py"，output 固定为 ".openkitty/storage/fundraising_bp_${execution_id}.pptx"。不要先尝试错误解释器，不要用 python -c、ptc_execute、shell heredoc 或一次性内联大脚本替代脚手架。base-ppt 不得参与正常选路；只有本任务 builder 的真实初次构建和一次针对性修正均失败，并且完整 PPTMaster 路线也客观不可用或修正后仍失败时，才允许公开说明原因后灾备降级。

脚手架创建后只完整读取一次本次任务的 build_deck.py。先在推理中冻结逐页架构：slide、叙事任务、结论、证据形式、构图家族、视觉焦点；不要另建规划文件。随后用有锚点的 edit_file 分段修改现有 builder，不要用 write_file 覆盖整份脚手架；必须在 TASK PAGE COMPOSITIONS 中为 10–14 页的每一页登记独立 renderer。每个代码修改保持有界并围绕若干连续页面，优先复用脚手架里的排版、图表、表格、时间线、矩阵和边界检查能力；这些只作底层积木，不能重新成为整套稿件的通用页型引擎。

叙事主线为：机会变化→客户痛点→产品闭环→竞争/壁垒→商业与增长→里程碑→融资用途→风险与决策。每页只表达一个投资判断；至少使用 5 种真正不同的构图家族，例如主张页、机制/架构图、证据图表、对比、矩阵、时间线、资金配置和决策收束。相邻正文页不得复用同一骨架，cards 最多 1 页；不能把 10–14 页做成一串圆角卡片、分栏文字或仪表盘。使用深墨蓝、暖金、白色与克制米色的咨询式视觉基线，但页面个性来自信息层级、比例、留白和逐页构图，不靠霓虹、胶囊、装饰圆点或堆叠颜色。

图表只能使用上游事实或明确标注的规划估算，不得为装饰编数字。正文保持演示可读密度，普通正文不少于 16pt。风险页写风险情景、触发信号和缓释措施；里程碑页使用相对月份、验收标准和建议责任角色。融资页必须给出人民币建议融资总额或区间，并同时给出资金用途百分比、runway、团队、算力和市场投入假设；金额与比例标注“规划估算（非项目事实）”。真实历史数据无法取得时，只允许一页集中列出项目方后续应替换的真实字段，不得把“未知、待补、来源等级”扩散成多张空页，也不得出现模板占位词。

编辑完成后直接运行本次 builder 生成 PPTX，再运行 audit_pptx.py。Windows 用 render_pptx.ps1，macOS/Linux 用 render_pptx.py，预览目录固定为 ".openkitty/storage/fundraising_bp_preview_${execution_id}"；渲染成功后用 vision 检查 contact_sheet.jpg 的每一页。若发现重复骨架、小字、溢出、遮挡、失衡或视觉焦点缺失，只做一次有针对性的 builder 修改，然后重新构建、审计、渲染并复查。不能把结构警告解释成“正常模板复用”。最终只报告真实路径、页数、大小、Editable: true、实际生产路线和实际校验情况。`,
		Tools:            []string{"use_skill", "read_file", "write_file", "edit_file", "persistent_shell", "vision"},
		RequiredSkills:   []string{"ppt-art-director"},
		MaxIter:          80,
		TimeoutSeconds:   900,
		OutputTokenLimit: 6500,
		ArtifactContract: &PipelineArtifactContract{
			Paths:                  []string{".openkitty/storage/fundraising_bp_${execution_id}.pptx"},
			MinBytes:               10 * 1024,
			MinSlides:              10,
			MinVisibleChars:        1500,
			RequireCurrencyAmount:  true,
			RequirePercentage:      true,
			MaxPlaceholderMentions: 6,
		},
	}
}

const dataAnalysisPresentationPipelineName = "data-analysis-ppt-pipeline"
const dataAnalysisLocalFastPipelineName = "data-analysis-local-fast-pipeline"

func dataAnalystCanUseLocalFastPath(task string) bool {
	for _, mention := range extractLocalFileMentions(task) {
		ext := strings.ToLower(filepath.Ext(mention))
		switch ext {
		case ".csv", ".tsv", ".xls", ".xlsx", ".json":
			return true
		}
	}
	return false
}

func dataAnalystLocalFastPipeline() Pipeline {
	return Pipeline{
		Name: dataAnalysisLocalFastPipelineName,
		Desc: "本地结构化数据快速分析：一次计算完成质量检查、指标、图表和报告",
		Steps: []PipelineStep{{
			Name:    "分析并交付",
			Prompt:  `本步骤独立完成本地 CSV/TSV/Excel/JSON 数据分析，不要拆分计划，也不要联网。立即且只调用一次 data_science：task="report"，data_file 使用用户指定的原始数据路径，report_path 和 chart_path 必须使用用户指定的确切 Markdown/PNG 路径；只有用户未指定时才使用默认路径。不要调用 shell，不要先预览数据，不要自己编写 Python。data_science 会原子完成数据质量检查、逻辑异常识别、指标公式、异常隔离口径、Markdown、PNG 和文件验证。工具成功后只回复 3-6 条带数字的核心结论、异常处理口径、真实报告与图表路径；工具失败则如实返回具体错误，不得输出计划路径冒充交付。`,
			Tools:   []string{"data_science"},
			MaxIter: 4,
		}},
	}
}

func dataAnalystWantsPresentationArtifact(task string) bool {
	text := strings.ToLower(strings.TrimSpace(task))
	if text == "" {
		return false
	}
	for _, neg := range []string{
		"不要ppt", "不要 ppt", "不需要ppt", "不需要 ppt", "无需ppt", "无需 ppt", "不用ppt", "不用 ppt",
		"不要pptx", "不需要pptx", "无需pptx", "不用pptx",
		"不要幻灯片", "不需要幻灯片", "无需幻灯片", "不用幻灯片",
		"不要演示文稿", "不需要演示文稿", "无需演示文稿", "不用演示文稿",
	} {
		if strings.Contains(text, neg) {
			return false
		}
	}
	for _, marker := range []string{
		"ppt", "pptx", "powerpoint", "slide", "slides", "presentation", "deck",
		"幻灯片", "演示文稿", "演示稿", "汇报材料",
	} {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}

func (m *AIEmployeeManager) dataAnalystPresentationPipeline(baseName, task string) (Pipeline, bool) {
	if m == nil || m.gateway == nil {
		return Pipeline{}, false
	}
	base, ok := m.gateway.resolveAEPipeline(baseName)
	if !ok || len(base.Steps) == 0 {
		return Pipeline{}, false
	}
	for _, step := range base.Steps {
		if strings.TrimSpace(step.Name) == "PPT报告" {
			return base, true
		}
	}
	steps := append([]PipelineStep(nil), base.Steps...)
	steps = append(steps, dataAnalysisPresentationStep())
	desc := strings.TrimSpace(base.Desc)
	if desc == "" {
		desc = "数据分析"
	}
	return Pipeline{Name: dataAnalysisPresentationPipelineName, Desc: desc + "→PPT报告", Steps: steps}, true
}

func dataAnalysisPresentationStep() PipelineStep {
	step := PipelineStep{
		Name: "PPT报告",
		Prompt: `阶段边界：本步骤只在用户明确要求 PPT、PPTX、PowerPoint、幻灯片、演示文稿、deck 或汇报材料时执行。不要停留在 Markdown 报告，必须生成真实 PowerPoint .pptx 文件。

输入与素材：
1. 优先读取 .openkitty/storage/analysis_data.json 和 .openkitty/storage/data_analysis_report.md。
2. 读取 .openkitty/storage/charts/ 下真实存在的本次分析 PNG 图表；只能引用存在的图表，不得伪造路径。
3. 如果报告或图表缺失，基于上游数据分析结果生成降级版 PPT，并在最后一页说明缺失项。

产出要求：
1. 先调用 use_skill ppt-art-director，并严格按三级生产：Level 3 高端定制=完整上游 PPT Master；Level 2 常规=从内置脚手架创建任务专用 python-pptx/PptxGenJS 逐页脚本；Level 1 灾备=base-ppt。base-ppt 直接选路仅用于用户明确要求快速、内部、周期性、固定结构或兼容占位符；正式精美稿必须先完成所选精美路线的真实构建和一次针对性修正，仍失败再尝试另一条适用精美路线，只有两条路线均客观不可用或修正后仍失败时才允许公开降级。所有正式数据汇报无论页数都默认使用脚手架生成本次任务独有的逐页 builder：deck_spec.json 只负责事实和叙事契约，读取 builder 一次后必须为每页登记独立 renderer；紧凑 JSON 只用于用户明确要求简单/快速/内部输出，或真正固定结构、周期性的稿件。禁止空 generator 直接 convert 成 PPTX，禁止用一次性内联大脚本代替 Level 2 脚手架。不得按题材关键词自动选择配色或模板。
2. 不要分段读取整份 JSON/Markdown；优先用脚本一次性读取 analysis_data.json、报告标题摘要和 charts 目录清单。
3. PPT 保存到 .openkitty/storage/data_analysis_report.pptx；如果已有同名文件，可用 data_analysis_report_{日期或时间}.pptx。
4. 建议 6-10 页：封面、结论摘要、数据概览、数据质量、核心发现、关键图表解读、建议/下一步、产出清单。
5. PPT 内容必须来自真实数据分析和真实图表。没有销售字段时不得写销售漏斗、营收或转化率结论。
6. 生成后必须用 Python 重新打开 PPTX 校验页数和文件大小，文件必须大于 10KB 且至少 4 页；可用 PowerPoint 或 LibreOffice 渲染时优先渲染检查。
7. 最终回答必须包含 PPTX 真实路径、页数、使用的数据/报告/图表路径和 3-6 条结论摘要；禁止输出“我将生成/下一步生成/let me generate”。`,
		Tools:            []string{"read_file", "write_file", "edit_file", "persistent_shell", "list_skills", "use_skill", "convert", "vision"},
		MaxIter:          80,
		TimeoutSeconds:   900,
		OutputTokenLimit: 5000,
	}
	step.Prompt += presentationDataAnalysisPrompt()
	return step
}

func presentationDataAnalysisPrompt() string {
	return `

OpenKitty presentation production route:
1. Call use_skill ppt-art-director first. Choose the renderer from the actual job; do not force a generic host-side generator.
2. Enforce three levels. Route explicit beauty, premium, design-led, executive, board, investor, client, launch, sales, brand, or showcase work to Level 3 PPTMaster first and run the complete Strategist/Executor workflow. Other substantive audience-facing work uses Level 2: the bundled task-specific builder, source-bound deck_spec.json, executable visual_plan.json, and one page-owned renderer per slide. Level 1 base-ppt is only for explicit simple/fast/internal/recurring/fixed-schema output or disclosed disaster recovery after applicable quality routes and one focused correction fail. Never call convert for PPTX with an empty generator and never replace Level 2 with a one-shot inline Python program.
3. When PPTMaster is selected, complete its bundled upstream Strategist/Executor, SVG quality, finalize, export, and visual-QA stages.
4. Preserve .openkitty/storage/data_analysis_report.md, analysis_data.json, and only the charts actually produced by this task. Never invent facts or silently reuse a public chart directory.
5. For every substantive deck, audit at 16pt body/35pt title, native-render it, inspect the contact sheet and every slide separately, validate a vision-authored aesthetic_qa.json, fix detected quality problems, and state the actual route, Editable true/false, real PPTX path, and slide count.
`
}

func (m *AIEmployeeManager) aeWorkflowPlanMessage(record *AEWorkRecord) string {
	if m == nil || m.gateway == nil || record == nil {
		return ""
	}
	pipelineName := strings.TrimSpace(record.Pipeline)
	if pipelineName == "" {
		return ""
	}
	pipeline, ok := m.gateway.resolveAEPipeline(pipelineName)
	if m.gateway.pipelineExec != nil {
		if exec, found := m.gateway.pipelineExec.GetExecution(record.ExecID); found && exec.RuntimePipeline != nil {
			pipeline = *exec.RuntimePipeline
			ok = true
		}
	}
	if !ok || len(pipeline.Steps) == 0 {
		return ""
	}
	agentName := strings.TrimSpace(record.EmployeeID)
	m.mu.RLock()
	if cfg, ok := m.employees[record.EmployeeID]; ok && strings.TrimSpace(cfg.Name) != "" {
		agentName = strings.TrimSpace(cfg.Name)
	}
	m.mu.RUnlock()
	message := buildAEWorkflowPlanMessage(agentName, pipelineName, pipeline.Steps)
	if task := strings.TrimSpace(record.Task); task != "" {
		message = "当前请求：" + textutil.TruncateRunes(task, 500) + "\n" + message
	}
	return message
}

func buildAEWorkflowPlanMessage(agentName, pipelineName string, steps []PipelineStep) string {
	if len(steps) == 0 {
		return ""
	}
	if strings.TrimSpace(pipelineName) == "investment-advisory" {
		if strings.TrimSpace(agentName) == "" {
			agentName = "投资顾问"
		}
		return "已接收任务。\nAI员工：" + agentName + "\n处理方式：先识别问题类型和所需决策深度，只核验本题必要的市场信息并直接回答；只有深度研究或明确要求保存时才生成报告。"
	}
	stepNames := make([]string, 0, len(steps))
	hasPPT := false
	for _, step := range steps {
		name := strings.TrimSpace(step.Name)
		if name == "" {
			continue
		}
		stepNames = append(stepNames, name)
		if strings.Contains(strings.ToLower(name), "ppt") || strings.Contains(name, "幻灯片") || strings.Contains(name, "演示文稿") {
			hasPPT = true
		}
	}
	if len(stepNames) == 0 {
		return ""
	}
	if strings.TrimSpace(agentName) == "" {
		agentName = "AI员工"
	}
	var b strings.Builder
	b.WriteString("已接收任务。第一步将先理解意图、核对可用上下文和输入完整性，再决定是否启动后续业务步骤。\n")
	b.WriteString("AI员工：")
	b.WriteString(agentName)
	b.WriteString("\nPipeline：")
	b.WriteString(strings.TrimSpace(pipelineName))
	b.WriteString("\n执行流程：")
	for i, name := range stepNames {
		if i > 0 {
			b.WriteString(" → ")
		}
		b.WriteString(name)
	}
	b.WriteString("\n填充槽位：")
	for _, step := range steps {
		if step.Slot == nil {
			continue
		}
		b.WriteString("\n- ")
		b.WriteString(step.Slot.ID)
		b.WriteString("：")
		b.WriteString(strings.TrimSpace(step.Name))
		if len(step.Slot.RequiredFields) > 0 {
			b.WriteString("；必填 ")
			b.WriteString(textutil.TruncateRunes(strings.Join(step.Slot.RequiredFields, "、"), 240))
		}
	}
	permissionLines := buildAEWorkflowPermissionLines(steps)
	if permissionLines != "" {
		b.WriteString("\n权限边界：")
		b.WriteString(permissionLines)
	}
	b.WriteString("\n交付标准：")
	b.WriteString(strings.TrimPrefix(aiemployee.BuildPipelineDeliveryStandards(steps), "- "))
	if strings.TrimSpace(pipelineName) == dataAnalysisPresentationPipelineName || hasPPT {
		b.WriteString("\n交付目标补充：Markdown 数据分析报告 + 图表 + PPTX。")
	} else if strings.TrimSpace(pipelineName) == "data-analysis-pipeline" {
		b.WriteString("\n交付目标补充：Markdown 数据分析报告 + 图表。")
	}
	return b.String()
}

func buildAEWorkflowPermissionLines(steps []PipelineStep) string {
	var lines []string
	for _, step := range steps {
		name := strings.TrimSpace(step.Name)
		if name == "" {
			continue
		}
		permission := "遵循 Pipeline 默认工具边界"
		if step.NoTools {
			permission = "仅分析已有输入（不调用工具）"
		} else if len(step.Tools) > 0 {
			permission = strings.Join(uniqueSortedStrings(step.Tools), "、")
		}
		lines = append(lines, fmt.Sprintf("\n- %s：%s", name, permission))
	}
	return strings.Join(lines, "")
}

func (m *AIEmployeeManager) publishAIEmployeeWorkflowPlan(record *AEWorkRecord) string {
	if m == nil || m.gateway == nil || record == nil {
		return ""
	}
	message := strings.TrimSpace(m.aeWorkflowPlanMessage(record))
	if message == "" {
		return ""
	}
	taskID := aeChatTaskID(record.ExecID)
	agentName := strings.TrimSpace(record.EmployeeID)
	pipelineName := strings.TrimSpace(record.Pipeline)
	totalSlots := 0
	m.mu.RLock()
	if cfg, ok := m.employees[record.EmployeeID]; ok {
		if strings.TrimSpace(cfg.Name) != "" {
			agentName = strings.TrimSpace(cfg.Name)
		}
	}
	m.mu.RUnlock()
	if m.gateway.pipelineExec != nil {
		if exec, found := m.gateway.pipelineExec.GetExecution(record.ExecID); found && exec.RuntimePipeline != nil {
			totalSlots = len(exec.RuntimePipeline.Steps)
		}
	}
	progressMessage := fmt.Sprintf("%s 已受理，正在执行 %s", agentName, pipelineName)
	if totalSlots > 0 {
		progressMessage += fmt.Sprintf("（%d 个槽位）", totalSlots)
	}
	progressMessage += "；详细步骤请在任务进度中查看。"
	m.gateway.BroadcastProgress(ProgressEvent{
		Type:      "workflow_plan",
		TaskID:    taskID,
		AgentName: agentName,
		Message:   progressMessage,
		Status:    "running",
		Timestamp: time.Now().Unix(),
	})
	return message
}

func externalCodingEmployeeAgent(employeeID string) (string, bool) {
	switch strings.TrimSpace(employeeID) {
	case "codex-specialist":
		return "codex", true
	case "cc-specialist":
		return "claude", true
	default:
		return "", false
	}
}

func externalCodingRunPrompt(task, workDir string) string {
	task = strings.TrimSpace(task)
	var hints []string
	if wd := strings.TrimSpace(workDir); wd != "" {
		hints = append(hints, "OpenKitty workspace: "+wd)
	}
	hints = append(hints, "Host path rule: this process runs on Windows. Keep absolute drive paths like E:\\project as Windows paths. Do not rewrite them to /E/project.")
	hints = append(hints, "Coding task contract: inspect repository instructions and the current working tree before editing; preserve unrelated user changes; make the smallest coherent change; run the narrowest meaningful check first and broaden only when shared code is affected; report exact files, checks, and any remaining failure. For diagnosis-only requests, explain the cause without silently modifying code.")
	if strings.Contains(strings.ToLower(task), "ai-movie") {
		if _, err := os.Stat(`E:\ai-moive`); err == nil {
			hints = append(hints, `Known local path: E:\ai-moive exists. If E:\ai-movie is missing, inspect E:\ai-moive; the local folder name is spelled "ai-moive".`)
		}
	}
	if codexInteractiveWantsPresentationArtifact(task) || codexInteractiveWantsMaterialArtifact(task) {
		hints = append(hints, "PPT/material reference rule: for PPT, slides, deck, poster, generated-image, or visual-material requests, start with builtin/presentation/ppt-art-director/SKILL.md. Enforce three levels: Level 3 premium custom uses the complete upstream builtin/presentation/ppt-master/SKILL.md for suitable higher-end commercial, executive, client, investor, launch, or showcase work; Level 2 regular custom defaults every substantive audience-facing PPT, regardless of page count, to a task-specific python-pptx/PptxGenJS per-deck renderer created from the bundled scaffold. Per-slide customization is fundamental: deck_spec.json is the truth/narrative contract and the task builder must register one renderer for every slide; stable kinds are reusable mechanics, not the final whole-deck layout engine. Level 1 disaster fallback uses canonical builtin/presentation/base-ppt/SKILL.md directly only when the user clearly prioritizes fast turnaround, internal recurring output, a fixed schema, or compatible native placeholders. For polished work, repair the selected quality route once, then try the other applicable quality route; use base-ppt only after both are objectively unavailable or still fail after targeted correction, and disclose the exact reason. Phrases such as 简单整理一下、简单介绍、简单把做的事做成PPT、先看看效果 describe concise content or a first usable version; they do not by themselves authorize lower visual quality or base-ppt. Never call convert for PPTX with an empty generator and never replace Level 2 with a giant one-shot inline Python program. better-ppt is a legacy compatibility alias, not a second public route. Runtime assets are embedded and must not require a workspace reference/ directory. Save generated deliverables under .openkitty/storage, perform one bounded render-and-visual-review pass on actual rendered pixels, and report the actual route, Editable true/false, concrete PPTX path, slide count, and any fallback reason.")
		hints = append(hints, "PPT visual-quality override: explicitly beautiful/premium/design-led or executive/board/investor/client/launch/brand/showcase requests start on Level 3 PPTMaster; do not scaffold Level 2 first. Other substantive Level-2 decks must complete executable visual_plan.json, use its palette/type/assets in the actual slide objects, audit with 16pt body and 35pt titles, inspect every rendered slide separately, and pass the built-in validator for a vision-authored aesthetic_qa.json.")
	}
	if codexInteractiveWantsVisualArtifact(task) {
		hints = append(hints, "Generated image output rule: use Codex image generation when available, save image assets locally when possible, and include the saved path or image result in the final answer.")
	}
	if len(hints) == 0 {
		return task
	}
	return "## Host context\n- " + strings.Join(hints, "\n- ") + "\n\n## User task\n" + task
}

func (m *AIEmployeeManager) DispatchExternalCodingEmployee(ctx context.Context, cfg *AIEmployeeConfig, agentType, displayTask, task, sessionID string) (*AEWorkRecord, error) {
	if cfg == nil {
		return nil, fmt.Errorf("AI employee config missing")
	}
	workDir := ""
	if m.gateway != nil && m.gateway.config != nil {
		workDir = strings.TrimSpace(m.gateway.config.Workspace)
	}
	if workDir == "" {
		workDir, _ = os.Getwd()
	}
	if err := checkExternalCodingAgentReady(ctx, agentType); err != nil {
		return nil, err
	}

	execID := fmt.Sprintf("external_%s_%d", agentType, time.Now().UnixNano())
	if agentType == "codex" && m.gateway != nil && m.gateway.codexInteractive != nil && sessionID != "" {
		execID = sessionID + "-codex"
	}
	record := &AEWorkRecord{
		ID:          fmt.Sprintf("dewr_%s_%d", cfg.ID, time.Now().UnixNano()),
		EmployeeID:  cfg.ID,
		Task:        displayTask,
		Pipeline:    "external_agent",
		ExecID:      execID,
		Status:      "running",
		SessionID:   sessionID,
		ChannelUser: sessionID,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}

	m.mu.Lock()
	m.workLog[cfg.ID] = append(m.workLog[cfg.ID], record)
	m.mu.Unlock()
	m.saveWorkRecordToDB(record)

	m.logger.Printf("[AE] %s direct external agent dispatched: agent=%s exec=%s", cfg.Name, agentType, execID)
	go m.notifySupervisor(cfg, fmt.Sprintf("%s 已启动\n任务: %s\n工作目录: %s\n执行ID: %s", strings.Title(agentType), displayTask, workDir, execID))

	if agentType == "codex" && m.gateway != nil && m.gateway.codexInteractive != nil {
		codexWorkDir := inferCodexWorkDirFromTask(task, workDir)
		codexChannel, codexRecipient, codexUserID := aeChannelContext(ctx)
		if codexChannel == "" {
			codexChannel = "web"
		}
		if codexUserID == "" {
			codexUserID = "web"
			if codexChannel != "web" && sessionID != "" {
				codexUserID = sessionID
			}
		}
		taskID, result, err := m.gateway.codexInteractive.StartOrSend(ctx, sessionID, codexChannel, codexUserID, codexRecipient, task, codexWorkDir)
		if taskID != "" {
			record.ExecID = strings.TrimSuffix(taskID, "-de")
			record.Pipeline = "codex_interactive"
		}
		if err != nil {
			record.Status = "failed"
			record.Result = err.Error()
			record.UpdatedAt = time.Now()
			m.updateWorkRecord(record.ExecID, "failed", record.Result)
			m.savePipelineResultToChat(record, record.Result)
			return record, nil
		}
		record.Result = result
		record.UpdatedAt = time.Now()
		m.updateWorkRecord(record.ExecID, "running", result)
		return record, nil
	}

	runPrompt := task
	if sessionID != "" {
		if history := strings.TrimSpace(loadSessionContext(sessionID, 6)); history != "" {
			runPrompt = history + "\n\n---\n\n当前任务:\n" + task
		}
	}

	runPrompt = externalCodingRunPrompt(runPrompt, workDir)

	runCtx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	m.putExternalCodingCancel(aeChatTaskID(execID), cancel)
	go m.runExternalCodingEmployee(runCtx, cancel, cfg, record, agentType, workDir, runPrompt)
	return record, nil
}

func (m *AIEmployeeManager) runExternalCodingEmployee(runCtx context.Context, cancel context.CancelFunc, cfg *AIEmployeeConfig, record *AEWorkRecord, agentType, workDir, prompt string) {
	if cancel != nil {
		defer cancel()
	}
	if record != nil {
		defer m.clearExternalCodingCancel(aeChatTaskID(record.ExecID))
	}
	start := time.Now()
	done := make(chan struct {
		result string
		err    error
	}, 1)
	go func() {
		var result string
		var err error
		if agentType == "codex" {
			result, err = m.runCodexAppServerCodingAgent(runCtx, cfg, record, workDir, prompt)
		} else {
			result, err = runExternalCodingAgent(runCtx, agentType, workDir, prompt)
		}
		done <- struct {
			result string
			err    error
		}{result: result, err: err}
	}()

	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			m.notifySupervisor(cfg, fmt.Sprintf("%s 正在执行中，已运行 %s\n任务: %s", strings.Title(agentType), time.Since(start).Round(time.Second), truncate(record.Task, 120)))
		case res := <-done:
			status := "completed"
			result := strings.TrimSpace(res.result)
			if res.err != nil {
				status = "failed"
				if result != "" {
					result += "\n\n"
				}
				result += "执行失败: " + res.err.Error()
			}
			if result == "" {
				result = "外部编码代理未返回内容。"
			}
			status, result = externalCodingRunStatus(res.result, res.err)
			record.Status = status
			record.Result = result
			record.UpdatedAt = time.Now()
			m.updateWorkRecord(record.ExecID, status, result)
			m.savePipelineResultToChat(record, result)
			m.finishExternalCodingGatewayTask(record, status, result)
			statusText := "完成"
			if status == "failed" {
				statusText = "失败"
			}
			if status == "stopped" {
				statusText = "stopped"
			}
			m.notifySupervisor(cfg, fmt.Sprintf("%s 任务%s\n任务: %s\n结果: %s", strings.Title(agentType), statusText, record.Task, truncate(result, 1200)))
			return
		}
	}
}

func externalCodingRunStatus(rawResult string, err error) (string, string) {
	result := cleanExternalCodingResult(rawResult)
	if err == nil {
		if result == "" {
			result = "External coding runtime completed without final output."
		}
		return "completed", result
	}

	status := "failed"
	message := "External coding runtime failed: " + err.Error()
	if errors.Is(err, context.Canceled) {
		status = "stopped"
		message = "External coding runtime stopped by user."
	} else if errors.Is(err, context.DeadlineExceeded) {
		message = "External coding runtime timed out: " + err.Error()
	}
	if result != "" {
		result += "\n\n"
	}
	return status, result + message
}

func cleanExternalCodingResult(rawResult string) string {
	result := strings.TrimSpace(rawResult)
	if result == "" {
		return ""
	}
	if looksLikeCodexProgressTranscript(result) {
		return ""
	}
	return result
}

func looksLikeCodexProgressTranscript(output string) bool {
	lines := strings.Split(strings.TrimSpace(output), "\n")
	nonEmpty := 0
	progressLines := 0
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		nonEmpty++
		lower := strings.ToLower(line)
		for _, prefix := range []string{
			"codex started ",
			"codex completed ",
			"codex generated a reply",
			"codex turn completed",
			"codex task cancelled",
			"run command:",
			"command completed:",
			"preparing file changes:",
			"file changes completed:",
			"calling tool:",
			"tool completed:",
			"approval error:",
		} {
			if strings.HasPrefix(lower, prefix) {
				progressLines++
				break
			}
		}
	}
	if nonEmpty == 0 {
		return false
	}
	return progressLines >= 2 && progressLines*3 >= nonEmpty*2
}

func (m *AIEmployeeManager) runCodexAppServerCodingAgent(ctx context.Context, cfg *AIEmployeeConfig, record *AEWorkRecord, workDir, prompt string) (string, error) {
	ok, reason := codexAppServerSupported(ctx)
	if !ok {
		if strings.TrimSpace(reason) == "" {
			reason = "codex app-server is unavailable"
		}
		return "", fmt.Errorf("codex runtime unavailable: %s", reason)
	}
	taskID := aeChatTaskID(record.ExecID)
	channelName, chatID := m.resolveSupervisorChannelTarget(cfg)
	onProgress := func(evt codexAppServerProgress) {
		if m.gateway != nil {
			m.gateway.BroadcastProgress(ProgressEvent{
				Type:      evt.Type,
				TaskID:    taskID,
				Message:   evt.Message,
				ToolName:  evt.ToolName,
				AgentName: cfg.Name,
				Status:    evt.Status,
			})
		}
		if shouldNotifyCodexChannelProgress(evt) {
			m.notifySupervisor(cfg, fmt.Sprintf("Codex 进度\n任务: %s\n%s", truncate(record.Task, 120), evt.Message))
		}
	}
	runtime := NewCodexRuntime(globalCodexApprovalManager)
	return runtime.Run(ctx, codexAppServerRunOptions{
		WorkDir:         resolveCodexWorkDir(workDir),
		Prompt:          prompt,
		TaskID:          taskID,
		SessionID:       record.SessionID,
		Channel:         channelName,
		Recipient:       chatID,
		ApprovalManager: globalCodexApprovalManager,
		OnProgress:      onProgress,
	})
}

func shouldNotifyCodexChannelProgress(evt codexAppServerProgress) bool {
	// Codex progress belongs in the Web/SSE progress bubbles and downloadable
	// log. External channels should only receive approval cards and the final
	// task result, otherwise command/tool log bubbles get pushed to WeChat.
	return false
}

func (m *AIEmployeeManager) finishExternalCodingGatewayTask(record *AEWorkRecord, status, result string) {
	if m == nil || m.gateway == nil || record == nil || record.ExecID == "" {
		return
	}
	taskID := aeChatTaskID(record.ExecID)
	if taskID == "" {
		return
	}
	g := m.gateway
	var task *GatewayTask
	finalStatus := status
	finalResult := result
	g.tasksMu.Lock()
	if existing, ok := g.tasks[taskID]; ok {
		task = existing
		if existing.Status == "stopped" && status != "stopped" {
			finalStatus = "stopped"
			if strings.TrimSpace(finalResult) == "" {
				finalResult = "External coding runtime stopped by user."
			}
		}
		task.Status = finalStatus
		task.Result = finalResult
		if finalStatus != "completed" {
			task.Error = fmt.Errorf("external agent %s", finalStatus)
		} else {
			task.Error = nil
		}
		now := time.Now()
		task.CompletedAt = &now
	}
	g.tasksMu.Unlock()
	if task != nil {
		if err := SaveGatewayTaskToDB(g.db, task); err != nil {
			m.logger.Printf("[AE] update external gateway task failed: %v", err)
		}
	}
	progressStatus := "success"
	progressType := "complete"
	message := "Codex 员工任务完成"
	var taskErr error
	if finalStatus != "completed" {
		progressStatus = "error"
		progressType = "error"
		message = "Codex 员工任务失败"
		taskErr = fmt.Errorf("external agent %s", finalStatus)
	}
	if finalStatus == "stopped" {
		progressStatus = "stopped"
		progressType = "cancelled"
		message = "Codex task stopped"
	}
	g.BroadcastProgress(ProgressEvent{
		Type:    progressType,
		TaskID:  taskID,
		Message: message,
		Status:  progressStatus,
	})
	g.broadcastTaskUpdate(taskID, finalStatus, finalResult, taskErr)
	g.taskStreamsMu.Lock()
	if ch, ok := g.taskStreams[taskID]; ok {
		delete(g.taskStreams, taskID)
		close(ch)
	}
	g.taskStreamsMu.Unlock()
}

func checkExternalCodingAgentReady(ctx context.Context, agentType string) error {
	exe, err := findExternalCodingExecutable(agentType)
	if err != nil {
		switch agentType {
		case "codex":
			return fmt.Errorf("Codex CLI 未安装或不在 PATH 中。请在后台机器安装：npm i -g @openai/codex")
		case "claude":
			return fmt.Errorf("Claude Code CLI 未安装或不在 PATH 中")
		default:
			return err
		}
	}
	probeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if out, err := exec.CommandContext(probeCtx, exe, "--version").CombinedOutput(); err != nil {
		return fmt.Errorf("%s CLI 无法启动：%v\n%s", strings.Title(agentType), err, strings.TrimSpace(string(out)))
	}
	if agentType == "codex" {
		loginCtx, cancelLogin := context.WithTimeout(ctx, 10*time.Second)
		defer cancelLogin()
		out, err := exec.CommandContext(loginCtx, exe, "login", "status").CombinedOutput()
		text := strings.TrimSpace(string(out))
		if err != nil || strings.Contains(strings.ToLower(text), "not logged") {
			return fmt.Errorf("Codex CLI 未登录或凭据不可用。请在后台机器运行 `codex login` 后重试。\n%s", text)
		}
	}
	return nil
}

func runExternalCodingAgent(ctx context.Context, agentType, workDir, prompt string) (string, error) {
	exe, err := findExternalCodingExecutable(agentType)
	if err != nil {
		return "", err
	}
	var args []string
	switch agentType {
	case "codex":
		args = []string{"exec", "--color", "never", "--skip-git-repo-check", "-C", workDir, "--dangerously-bypass-approvals-and-sandbox", "-"}
	case "claude":
		args = []string{"-p", prompt, "--output-format", "text", "--dangerously-skip-permissions", "--no-show-cost"}
	default:
		return "", fmt.Errorf("unsupported external coding agent: %s", agentType)
	}

	cmd := exec.CommandContext(ctx, exe, args...)
	cmd.Dir = workDir
	cmd.Env = append(os.Environ(),
		"CLAUDE_CODE_PERMISSION_MODE=bypassPermissions",
		"CLAUDE_PERMISSION_MODE=acceptEdits",
		"DISABLE_PERMISSIONS=1",
		"NO_COLOR=1",
		"TERM=dumb",
	)
	if agentType == "codex" {
		cmd.Stdin = strings.NewReader(prompt)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	start := time.Now()
	runErr := cmd.Run()
	elapsed := time.Since(start)

	output := stdout.String()
	if stderr.Len() > 0 {
		output += "\n[stderr]\n" + stderr.String()
	}
	status := "success"
	if runErr != nil {
		if ctx.Err() == context.DeadlineExceeded {
			status = "timeout after 600s"
		} else {
			status = "error: " + runErr.Error()
		}
	}
	changedFiles := agenttools.CollectExternalAgentChangedFiles(workDir)
	tool := agenttools.NewExternalAgentTool(workDir, projectctx.ExternalAgentProjectContext)
	reportPath, reportErr := tool.WriteExecutionReport(workDir, agentType, fmt.Sprintf("ext_%d", time.Now().UnixNano()), status, elapsed, prompt, output, changedFiles)
	var b strings.Builder
	b.WriteString(fmt.Sprintf("[External Agent: %s] [Status: %s] [Duration: %.1fs]\n", agentType, status, elapsed.Seconds()))
	if reportErr == nil && reportPath != "" {
		b.WriteString("[Report: ")
		b.WriteString(reportPath)
		b.WriteString("]\n")
	} else if reportErr != nil {
		b.WriteString("[Report error: ")
		b.WriteString(reportErr.Error())
		b.WriteString("]\n")
	}
	if len(changedFiles) > 0 {
		b.WriteString("[Changed files]\n")
		b.WriteString(strings.Join(agenttools.PrefixLines(changedFiles, "- "), "\n"))
		b.WriteString("\n")
	}
	b.WriteString("\n")
	b.WriteString(output)
	return b.String(), runErr
}

func findExternalCodingExecutable(agentType string) (string, error) {
	var names []string
	switch agentType {
	case "codex":
		names = []string{"codex.cmd", "codex.exe", "codex"}
	case "claude":
		names = []string{"claude.cmd", "claude.exe", "claude"}
	default:
		names = []string{agentType}
	}
	for _, name := range names {
		if path, err := exec.LookPath(name); err == nil {
			return path, nil
		}
	}
	return "", fmt.Errorf("%s executable not found", agentType)
}

func (m *AIEmployeeManager) validateDispatchModelReady(cfg *AIEmployeeConfig) error {
	if cfg == nil {
		return fmt.Errorf("AI employee config missing")
	}
	route := aeModelRouteInfo(cfg, "text-to-text")
	if route.Available {
		return nil
	}
	reason := strings.TrimSpace(route.Reason)
	if reason == "" {
		reason = "no available model route"
	}
	name := strings.TrimSpace(cfg.Name)
	if name == "" {
		name = strings.TrimSpace(cfg.ID)
	}
	if name == "" {
		name = "AI employee"
	}
	if isModelAPIKeyConfigError(reason) {
		return fmt.Errorf("AI员工 %s 的模型配置不可用：API Key 未配置或仍是占位符。请在设置的模型配置中填写真实 API Key 后重试", name)
	}
	return fmt.Errorf("AI员工 %s 的模型配置不可用：%s", name, reason)
}

// watchPipeline monitors a running pipeline for HITL and completion events.
func (m *AIEmployeeManager) watchPipeline(ctx context.Context, cfg *AIEmployeeConfig, record *AEWorkRecord) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	lastStatus := ""

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		if m.gateway == nil || m.gateway.pipelineExec == nil {
			continue
		}

		exec, ok := m.gateway.pipelineExec.GetExecution(record.ExecID)
		if !ok {
			continue
		}

		// Check for HITL — send approval request to supervisor
		if exec.Status == "waiting_approval" && lastStatus != "waiting_approval" {
			stepIdx := exec.CurrentStep
			if stepIdx >= 0 && stepIdx < len(exec.Steps) {
				stepOutput := exec.Steps[stepIdx].Output
				m.notifySupervisor(cfg, fmt.Sprintf("需要审批\n步骤: %s\n\n摘要: %s",
					exec.Steps[stepIdx].Name, truncate(stepOutput, 500)))
			}
		}

		// Check for completion
		if exec.Status == "completed" || exec.Status == "degraded" || exec.Status == "failed" || exec.Status == "stopped" {
			if lastStatus != exec.Status {
				recordStatus := exec.Status
				if pipelineExecutionWaitsForInput(exec) {
					recordStatus = aeWaitingInputStatus
				}
				resultSummary := summarizeAEPipelineResult(exec)
				// A clarification stop has no deliverables. In particular, intake
				// descriptions of future contract paths must never become clickable
				// claims that those files already exist.
				if recordStatus != aeWaitingInputStatus {
					resultSummary = pipelineResultWithArtifacts(exec, resultSummary)
				}
				m.logger.Printf("[AE] watchPipeline: final result=%d chars", len(resultSummary))

				statusText := map[string]string{"completed": "完成", "degraded": "已完成（部分资料暂未取得）", "failed": "失败", "stopped": "已停止", aeWaitingInputStatus: "已停止（需重新@发起）"}[recordStatus]
				// Prepend @reply for channel-based conversations (feishu/qq/dingtalk)
				replyPrefix := ""
				if cfg.Channel != "" && record.ChannelUser != "" {
					replyPrefix = fmt.Sprintf("@%s ", record.ChannelUser)
				}
				msg := fmt.Sprintf("%s%s 任务%s\n任务: %s\n结果: %s",
					replyPrefix, cfg.Name, statusText, record.Task, truncate(resultSummary, 1000))
				m.notifySupervisor(cfg, msg)
				m.updateWorkRecord(record.ExecID, recordStatus, resultSummary)
				record.Status = recordStatus
				record.Result = resultSummary
				record.UpdatedAt = time.Now()

				// Save the AI response as a chat message so Web UI and session history show it
				if !aeSuppressesResultChatPersistence(ctx) {
					m.savePipelineResultToChat(record, resultSummary)
				}
				// 处理队列中的下一个任务
				m.mu.RLock()
				queue := m.taskQueue[record.EmployeeID]
				m.mu.RUnlock()
				if queue != nil {
					select {
					case next := <-queue:
						m.logger.Printf("[AE] %s dequeueing next task: '%s'", cfg.Name, next.Task)
						go m.Dispatch(next.Ctx, record.EmployeeID, next.Task, next.SessionID)
					default:
					}
				}
				return
			}
		}

		lastStatus = exec.Status
	}
}

func summarizeAEPipelineResult(exec *PipelineExecution) string {
	if exec == nil {
		return "AI员工任务失败：未找到执行记录。"
	}
	status := strings.TrimSpace(exec.Status)
	if exec.PipelineName == "ppt-polish" && (status == "failed" || status == "stopped") {
		return summarizePPTPolishFailure(status)
	}
	resultSummary := strings.TrimSpace(exec.FinalOutput())
	if resultSummary == "" {
		for _, output := range readStepOutputFiles(exec) {
			output = strings.TrimSpace(output)
			if output != "" && !isProgressOnlyFinalOutput(output) {
				resultSummary = output
			}
		}
	}
	if status == "failed" || status == "stopped" {
		resultSummary = userFacingAEPipelinePartialResult(exec)
	}

	issues := aePipelineIssues(exec)
	if status == "failed" || status == "stopped" {
		var b strings.Builder
		if status == "stopped" {
			b.WriteString("AI员工任务已停止，未形成完整交付。")
		} else {
			b.WriteString("AI员工任务失败，未形成完整交付。")
		}
		if len(issues) > 0 {
			b.WriteString("\n\n失败/异常原因：")
			for _, issue := range issues {
				b.WriteString("\n- ")
				b.WriteString(issue)
			}
		}
		if resultSummary != "" {
			b.WriteString("\n\n已保留的阶段性结果：\n")
			b.WriteString(resultSummary)
		}
		return strings.TrimSpace(b.String())
	}

	if resultSummary == "" {
		if len(issues) > 0 {
			return "AI员工任务状态异常：流程标记为完成，但没有生成可用最终结果。\n\n异常原因：\n- " + strings.Join(issues, "\n- ")
		}
		return "AI员工任务状态异常：流程标记为完成，但没有生成可用最终结果。"
	}

	if len(issues) > 0 {
		// The full provider/tool diagnostics remain available in the execution
		// detail and status badge. When a substantive host fallback was
		// successfully produced, the chat bubble itself is the deliverable—not
		// a receipt, warning, transport error, request ID, or safety response.
		return resultSummary
	}
	return resultSummary
}

func userFacingAEPipelinePartialResult(exec *PipelineExecution) string {
	if exec == nil {
		return ""
	}
	exec.mu.Lock()
	steps := append([]StepResult(nil), exec.Steps...)
	exec.mu.Unlock()
	task := ""
	if exec.RuntimePipeline != nil {
		task = strings.TrimSpace(exec.RuntimePipeline.AIEmployeeOriginalTask)
	}
	for index := len(steps) - 1; index >= 0; index-- {
		step := steps[index]
		if strings.TrimSpace(step.Name) == aiEmployeeIntakeStepName ||
			(step.Status != "completed" && step.Status != "degraded") {
			continue
		}
		output := strings.TrimSpace(cleanPipelineUserOutput(step.Output))
		if output == "" || isProgressOnlyFinalOutput(output) ||
			len(aiemployee.UserFacingOutputIssues(task, output)) > 0 {
			continue
		}
		return output
	}
	return ""
}

func summarizePPTPolishFailure(status string) string {
	if strings.TrimSpace(status) == "stopped" {
		return "PPT 制作已停止，本次没有生成可下载的成品。"
	}
	return "这次没有成功生成可下载的 PPT。\n\n系统已确认最终 PPTX 未通过文件与结构校验，因此没有把构建脚本、临时路径或未验证文件当作成品。此次失败属于内部生成问题，不代表你的主题或材料不完整。"
}

func aePipelineIssues(exec *PipelineExecution) []string {
	if exec == nil {
		return nil
	}
	exec.mu.Lock()
	steps := append([]StepResult(nil), exec.Steps...)
	exec.mu.Unlock()
	issues := make([]string, 0)
	for _, step := range steps {
		status := strings.TrimSpace(step.Status)
		errText := strings.TrimSpace(step.Error)
		if status == "completed" && errText == "" {
			continue
		}
		if status == "failed" || status == "stopped" || status == "failed_fallback" || errText != "" {
			name := strings.TrimSpace(step.Name)
			if name == "" {
				name = "unnamed step"
			}
			if errText != "" {
				issues = append(issues, fmt.Sprintf("%s [%s]: %s", name, status, truncate(errText, 180)))
			} else {
				issues = append(issues, fmt.Sprintf("%s [%s]", name, status))
			}
		}
	}
	return issues
}

func pipelineResultWithArtifacts(exec *PipelineExecution, resultSummary string) string {
	artifacts := extractPipelineArtifacts(exec)
	deliveryURL := ""
	if exec != nil && strings.TrimSpace(exec.ID) != "" {
		deliveryURL = "/api/delivery/summary?exec_id=" + url.QueryEscape(exec.ID)
	}
	if len(artifacts) == 0 {
		if deliveryURL != "" {
			resultSummary = strings.TrimSpace(resultSummary)
			if resultSummary != "" {
				return resultSummary + "\n\n交付摘要：[" + exec.ID + "](" + deliveryURL + ")"
			}
			return "交付摘要：[" + exec.ID + "](" + deliveryURL + ")"
		}
		return resultSummary
	}
	resultSummary = strings.TrimSpace(resultSummary)
	var b strings.Builder
	if resultSummary != "" {
		b.WriteString(resultSummary)
		b.WriteString("\n\n")
	}
	b.WriteString("交付物：\n")
	for _, artifact := range artifacts {
		name := filepath.Base(artifact.Path)
		if name == "." || name == "/" || name == "" {
			name = artifact.Path
		}
		b.WriteString("- [")
		b.WriteString(name)
		b.WriteString("](")
		b.WriteString(artifact.DownloadURL)
		b.WriteString(")")
		if artifact.SourceStep != "" {
			b.WriteString(" - ")
			b.WriteString(artifact.SourceStep)
		}
		b.WriteString("\n")
	}
	if deliveryURL != "" {
		b.WriteString("\n交付摘要：[")
		b.WriteString(exec.ID)
		b.WriteString("](")
		b.WriteString(deliveryURL)
		b.WriteString(")")
	}
	return strings.TrimSpace(b.String())
}

// savePipelineResultToChat persists the pipeline result as an assistant chat message
// so the Web UI and session history can display it.
func (m *AIEmployeeManager) savePipelineResultToChat(record *AEWorkRecord, resultSummary string) {
	if m.gateway == nil || m.gateway.db == nil || record.SessionID == "" {
		return
	}
	chatTaskID := aeChatTaskID(record.ExecID)
	chatStatus := aeUserFacingTaskStatus(record.Status, resultSummary)
	if chatTaskID != "" {
		if err := m.gateway.db.Model(&ChatMessage{}).
			Where("session_id = ? AND task_id = ? AND role = ?", record.SessionID, chatTaskID, "user").
			Update("status", chatStatus).Error; err != nil {
			m.logger.Printf("[AE] update user chat message status failed: %v", err)
		}
		if err := m.gateway.db.Model(&ChatMessage{}).
			Where("session_id = ? AND task_id = ? AND role = ? AND content LIKE ?",
				record.SessionID, chatTaskID, "assistant", "%已接收任务。第一步将先理解意图%").
			Delete(&ChatMessage{}).Error; err != nil {
			m.logger.Printf("[AE] delete legacy workflow plan chat message failed: %v", err)
		}
	}
	agentName := record.EmployeeID
	m.mu.RLock()
	if cfg, ok := m.employees[record.EmployeeID]; ok && strings.TrimSpace(cfg.Name) != "" {
		agentName = cfg.Name
	}
	m.mu.RUnlock()
	m.gateway.saveChatMessage(record.SessionID, "assistant", agentName,
		middlewarecore.StripToolCalls(resultSummary), chatStatus, chatTaskID, "", "")
	m.logger.Printf("[AE] Chat message saved: session=%s status=%s internal_status=%s", record.SessionID, chatStatus, record.Status)
}

func aeUserFacingTaskStatus(internalStatus, result string) string {
	if strings.TrimSpace(internalStatus) == "degraded" && strings.TrimSpace(result) != "" {
		return "completed"
	}
	return internalStatus
}

func aeChatTaskID(execID string) string {
	execID = strings.TrimSpace(execID)
	if execID == "" {
		return ""
	}
	if strings.HasSuffix(execID, "-de") {
		return execID
	}
	return execID + "-de"
}

func (m *AIEmployeeManager) resolveSupervisorChannelTarget(cfg *AIEmployeeConfig) (string, string) {
	if m == nil || cfg == nil {
		return "", ""
	}
	m.mu.RLock()
	if dynamicVal, ok := m.channelChatIDs[cfg.ID]; ok {
		parts := strings.SplitN(dynamicVal, ":", 2)
		if len(parts) == 2 && strings.TrimSpace(parts[0]) != "" && strings.TrimSpace(parts[1]) != "" {
			m.mu.RUnlock()
			return parts[0], parts[1]
		}
	}
	m.mu.RUnlock()
	channelName := cfg.Channel
	chatID := cfg.ChatID
	return channelName, chatID
}

func (m *AIEmployeeManager) notifyCodexApprovalCard(cfg *AIEmployeeConfig, evt codexAppServerProgress) bool {
	if m == nil || m.gateway == nil || m.gateway.channelManager == nil {
		return false
	}
	approvalID := codexProgressMetadataString(evt.Metadata, "approval_id")
	if approvalID == "" {
		return false
	}
	req, ok := globalCodexApprovalManager.Get(approvalID)
	if !ok || req == nil {
		return false
	}
	channelName := req.Channel
	chatID := req.Recipient
	if channelName == "" || chatID == "" {
		channelName, chatID = m.resolveSupervisorChannelTarget(cfg)
		req.Channel = channelName
		req.Recipient = chatID
	}
	if channelName == "" || chatID == "" {
		return false
	}
	ch, ok := m.gateway.channelManager.GetChannel(channelName)
	if !ok {
		return false
	}
	sender, ok := ch.(ApprovalCardSender)
	if !ok {
		return false
	}
	if err := sender.SendApprovalCard(chatID, req); err != nil {
		m.logger.Printf("[AE] Codex approval card send failed (%s): %v", channelName, err)
		return false
	}
	return true
}

func codexProgressMetadataString(metadata map[string]interface{}, key string) string {
	if metadata == nil {
		return ""
	}
	v, ok := metadata[key]
	if !ok || v == nil {
		return ""
	}
	switch t := v.(type) {
	case string:
		return strings.TrimSpace(t)
	case fmt.Stringer:
		return strings.TrimSpace(t.String())
	default:
		return strings.TrimSpace(fmt.Sprintf("%v", t))
	}
}

// notifySupervisor sends notification via the registered channel.
func (m *AIEmployeeManager) notifySupervisor(cfg *AIEmployeeConfig, message string) {
	if m == nil || cfg == nil {
		return
	}
	msg := fmt.Sprintf("[%s] %s\n%s", cfg.Name, cfg.Role, message)
	m.logger.Printf("[AE] NOTIFY %s: %s", cfg.Supervisor, truncate(msg, 200))

	// Queue to DB for audit
	if m.gateway != nil && m.gateway.db != nil {
		m.gateway.db.Exec("INSERT INTO de_notifications (employee_id, supervisor, channel, message, created_at) VALUES (?, ?, ?, ?, ?)",
			cfg.ID, cfg.Supervisor, cfg.Channel, msg, time.Now())
	}

	// Send via channel if available — channel-agnostic, uses Channel.SendMessage interface
	channelName, chatID := m.resolveSupervisorChannelTarget(cfg)
	if m.gateway != nil && m.gateway.channelManager != nil && channelName != "" && chatID != "" {
		if ch, ok := m.gateway.channelManager.GetChannel(channelName); ok {
			if err := ch.SendMessage(chatID, msg); err != nil {
				m.logger.Printf("[AE] Channel send failed (%s): %v", channelName, err)
			}
		}
	}
}

func (m *AIEmployeeManager) latestWaitingInputRecord(employeeID, sessionID string) *AEWorkRecord {
	employeeID = strings.TrimSpace(employeeID)
	sessionID = strings.TrimSpace(sessionID)
	if employeeID == "" || sessionID == "" {
		return nil
	}
	cutoff := time.Now().Add(-aePendingInputTTL)

	var latest *AEWorkRecord
	m.mu.RLock()
	for _, record := range m.workLog[employeeID] {
		if record == nil || record.Status != aeWaitingInputStatus || record.SessionID != sessionID || record.UpdatedAt.Before(cutoff) {
			continue
		}
		if latest == nil || record.UpdatedAt.After(latest.UpdatedAt) {
			copyRecord := *record
			latest = &copyRecord
		}
	}
	m.mu.RUnlock()
	if latest != nil {
		return latest
	}

	if m.gateway == nil || m.gateway.db == nil {
		return nil
	}
	var dbRecord AEWorkRecordDB
	if err := m.gateway.db.
		Where("employee_id = ? AND session_id = ? AND status = ? AND updated_at >= ?", employeeID, sessionID, aeWaitingInputStatus, cutoff).
		Order("updated_at desc").First(&dbRecord).Error; err != nil {
		return nil
	}
	return dbRecord.ToMemory()
}

func (m *AIEmployeeManager) supersedeWaitingInputForExplicitInvocation(ctx context.Context, employeeID, sessionID string) {
	if !hasExplicitAEMentionGrant(ctx, employeeID) {
		return
	}
	if pending := m.latestWaitingInputRecord(employeeID, sessionID); pending != nil {
		m.markPendingInputRecord(pending.ID, "cancelled", "superseded by a new explicit @ invocation; restarted from step 1")
	}
}

func (m *AIEmployeeManager) markPendingInputRecord(recordID, status, result string) {
	recordID = strings.TrimSpace(recordID)
	if recordID == "" {
		return
	}
	now := time.Now()
	m.mu.Lock()
	for employeeID, records := range m.workLog {
		for i, record := range records {
			if record == nil || record.ID != recordID {
				continue
			}
			m.workLog[employeeID][i].Status = status
			m.workLog[employeeID][i].Result = result
			m.workLog[employeeID][i].UpdatedAt = now
		}
	}
	m.mu.Unlock()
	if m.gateway != nil && m.gateway.db != nil {
		m.gateway.db.Model(&AEWorkRecordDB{}).Where("record_id = ?", recordID).Updates(map[string]interface{}{
			"status": status, "result": result, "updated_at": now,
		})
	}
}

func (m *AIEmployeeManager) saveWorkRecordToDB(record *AEWorkRecord) {
	if m.gateway == nil || m.gateway.db == nil {
		return
	}
	var dbRecord AEWorkRecordDB
	dbRecord.FromMemory(record)
	// Upsert
	var existing AEWorkRecordDB
	if err := m.gateway.db.Where("record_id = ?", record.ID).First(&existing).Error; err == nil {
		m.gateway.db.Model(&existing).Updates(map[string]interface{}{
			"pipeline": dbRecord.Pipeline, "exec_id": dbRecord.ExecID,
			"status": dbRecord.Status, "result": dbRecord.Result,
			"tool_calls": dbRecord.ToolCalls, "updated_at": time.Now(),
		})
	} else {
		m.gateway.db.Create(&dbRecord)
	}
}

func (m *AIEmployeeManager) loadWorkRecordsFromDB() {
	if m.gateway == nil || m.gateway.db == nil {
		return
	}
	var records []AEWorkRecordDB
	m.gateway.db.Order("created_at desc").Limit(500).Find(&records)
	m.mu.Lock()
	for i := range records {
		r := records[i].ToMemory()
		m.workLog[r.EmployeeID] = append(m.workLog[r.EmployeeID], r)
	}
	reconciled := m.reconcileStaleCodexRunningWorkRecordsLocked()
	m.mu.Unlock()
	for _, record := range reconciled {
		m.saveWorkRecordToDB(record)
	}
	m.logger.Printf("[AE] Loaded %d work records from DB", len(records))
}

func (m *AIEmployeeManager) updateWorkRecord(execID, status, result string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for empID, records := range m.workLog {
		for i, r := range records {
			if sameAEExecID(r.ExecID, execID) {
				m.workLog[empID][i].Status = status
				m.workLog[empID][i].Result = result
				m.workLog[empID][i].UpdatedAt = time.Now()
				m.logger.Printf("[AE] Updated work record %s → %s", r.ID, status)
				// Persist status update to DB (async to avoid deadlock)
				go m.saveWorkRecordToDB(m.workLog[empID][i])
				return
			}
		}
	}
	m.logger.Printf("[AE] Work record not found for exec_id=%s", execID)
}

func (m *AIEmployeeManager) findWorkRecordByExecID(execID string) *AEWorkRecord {
	if m == nil || strings.TrimSpace(execID) == "" {
		return nil
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, records := range m.workLog {
		for _, record := range records {
			if record != nil && sameAEExecID(record.ExecID, execID) {
				return record
			}
		}
	}
	return nil
}

func (m *AIEmployeeManager) updateRunningWorkRecord(execID, status, result string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for empID, records := range m.workLog {
		for i, r := range records {
			if !sameAEExecID(r.ExecID, execID) || r.Status != "running" {
				continue
			}
			m.workLog[empID][i].Status = status
			m.workLog[empID][i].Result = result
			m.workLog[empID][i].UpdatedAt = time.Now()
			go m.saveWorkRecordToDB(m.workLog[empID][i])
			return true
		}
	}
	return false
}

func sameAEExecID(left, right string) bool {
	left = strings.TrimSuffix(strings.TrimSpace(left), "-de")
	right = strings.TrimSuffix(strings.TrimSpace(right), "-de")
	return left != "" && left == right
}

func (m *AIEmployeeManager) reconcileStaleCodexRunningWorkRecordsLocked() []*AEWorkRecord {
	if m == nil || m.gateway == nil || m.gateway.db == nil {
		return nil
	}
	records := m.workLog["codex-specialist"]
	if len(records) == 0 {
		return nil
	}
	var reconciled []*AEWorkRecord
	for _, record := range records {
		if record == nil || record.Status != "running" {
			continue
		}
		status, result := m.codexStaleWorkRecordFinalStatus(record)
		record.Status = status
		record.Result = result
		record.UpdatedAt = time.Now()
		cp := *record
		reconciled = append(reconciled, &cp)
	}
	return reconciled
}

func (m *AIEmployeeManager) codexStaleWorkRecordFinalStatus(record *AEWorkRecord) (string, string) {
	staleMessage := "Marked stopped during startup: no active Codex runtime is attached to this work record."
	if m == nil || m.gateway == nil || m.gateway.db == nil || record == nil {
		return "stopped", staleMessage
	}
	taskID := aeChatTaskID(record.ExecID)
	if taskID == "" {
		return "stopped", staleMessage
	}
	var task GatewayTaskRecord
	if err := m.gateway.db.Where("task_id = ?", taskID).First(&task).Error; err != nil {
		return "stopped", staleMessage
	}
	switch task.Status {
	case "completed":
		if strings.TrimSpace(task.Result) != "" {
			return "completed", task.Result
		}
		return "completed", "Codex task completed."
	case "failed":
		if strings.TrimSpace(task.ErrorMsg) != "" {
			return "failed", task.ErrorMsg
		}
		if strings.TrimSpace(task.Result) != "" {
			return "failed", task.Result
		}
		return "failed", "Codex task failed."
	case "stopped":
		if strings.TrimSpace(task.Result) != "" {
			return "stopped", task.Result
		}
		return "stopped", "Codex task stopped."
	default:
		return "stopped", staleMessage
	}
}

// readStepOutputFiles reads persisted step output files as fallback.
func readStepOutputFiles(exec *PipelineExecution) []string {
	dir := filepath.Join(".openkitty", "storage")
	var outputs []string
	for i, s := range exec.Steps {
		safeName := strings.Map(func(r rune) rune {
			if strings.ContainsRune(`/\:*?"<>|`, r) {
				return '_'
			}
			return r
		}, s.Name)
		filename := fmt.Sprintf("pipe_%s_step%d_%s.txt", exec.PipelineName, i, safeName)
		fp := filepath.Join(dir, filename)
		data, err := os.ReadFile(fp)
		if err == nil && len(data) > 0 {
			outputs = append(outputs, fmt.Sprintf("## %s\n%s", s.Name, string(data)))
		}
	}
	return outputs
}

// GetWorkLog returns the work history for an employee.
func (m *AIEmployeeManager) GetWorkLog(employeeID string) []*AEWorkRecord {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.workLog[employeeID]
}

// Remove unregisters a digital employee at runtime.
func (m *AIEmployeeManager) Remove(employeeID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if cfg, ok := m.employees[employeeID]; ok {
		delete(m.employees, employeeID)
		for alias, registered := range m.byAtName {
			if registered == cfg || (registered != nil && registered.ID == employeeID) {
				delete(m.byAtName, alias)
			}
		}
		m.logger.Printf("[AE] Removed: %s (%s)", cfg.Name, employeeID)
	}
}

// GetEmployee returns the config for an employee.
func (m *AIEmployeeManager) GetEmployee(employeeID string) (*AIEmployeeConfig, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	cfg, ok := m.employees[employeeID]
	return cfg, ok
}

// Global reference for tools to access the AE manager.
var globalAEManager *AIEmployeeManager

// SetGlobalAEManager is called by the gateway during initialization.
func SetGlobalAEManager(m *AIEmployeeManager) {
	globalAEManager = m
}

// Global identity injector for customer context assembly.
var globalIdentityInjector *businesstools.IdentityInjector

// SetGlobalIdentityInjector is called by the gateway during initialization.
func SetGlobalIdentityInjector(injector *businesstools.IdentityInjector) {
	globalIdentityInjector = injector
}

// ListEmployeesTool lets the default agent list AI employees.
type ListEmployeesTool struct{}

func NewListEmployeesTool() *ListEmployeesTool { return &ListEmployeesTool{} }
func (t *ListEmployeesTool) Name() string      { return "list_employees" }
func (t *ListEmployeesTool) Description() string {
	return "List visible AI digital employees (AI员工). Returns name, role, @mention name, status, pipeline, and recent task history for each."
}
func (t *ListEmployeesTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"include_hidden": map[string]interface{}{"type": "boolean", "description": "Include temporarily hidden employees."},
		},
	}
}
func (t *ListEmployeesTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	if globalAEManager == nil {
		return "系统未配置数字员工管理模块", nil
	}
	globalAEManager.mu.RLock()
	defer globalAEManager.mu.RUnlock()
	if len(globalAEManager.employees) == 0 {
		return "当前没有注册的AI员工", nil
	}
	var sb strings.Builder
	sb.WriteString("## AI 员工列表\n\n")
	sb.WriteString("| 名字 | 备注 | 角色 | @提及名 | 状态 | 流程 | 任务数 |\n")
	sb.WriteString("|------|------|------|---------|------|------|--------|\n")
	includeHidden, _ := params["include_hidden"].(bool)
	visibleCount := 0
	for id, cfg := range globalAEManager.employees {
		// Internal-only employees are implementation details. Unlike Hidden,
		// include_hidden is never allowed to disclose or advertise them.
		if cfg.InternalOnly {
			continue
		}
		if cfg.Hidden && !includeHidden {
			continue
		}
		status := "空闲"
		if globalAEManager.busy[id] {
			status = "忙碌"
		}
		pipeline := cfg.Pipeline
		if cfg.Direct != nil {
			pipeline = "direct（单步任务）"
		}
		role := cfg.Role
		if role == "" {
			role = "-"
		}
		atName := cfg.AtName
		if atName == "" {
			atName = "-"
		}
		taskCount := 0
		if wl, ok := globalAEManager.workLog[id]; ok {
			taskCount = len(wl)
		}
		note := strings.TrimSpace(cfg.Note)
		if note == "" {
			note = "-"
		}
		sb.WriteString(fmt.Sprintf("| %s | %s | %s | @%s | %s | %s | %d |\n",
			cfg.Name, note, role, atName, status, pipeline, taskCount))
		visibleCount++
	}
	sb.WriteString(fmt.Sprintf("\n共 %d 名AI员工\n", visibleCount))
	return sb.String(), nil
}

// ListEmployees returns all registered digital employees.
func (m *AIEmployeeManager) ListEmployees() []*AIEmployeeConfig {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]*AIEmployeeConfig, 0, len(m.employees))
	for _, cfg := range m.employees {
		out = append(out, cfg)
	}
	return out
}

// ToJSON exports work records as JSON.
func (m *AIEmployeeManager) ToJSON() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	type export struct {
		Employees []*AIEmployeeConfig        `json:"employees"`
		WorkLogs  map[string][]*AEWorkRecord `json:"work_logs"`
	}
	e := export{
		Employees: make([]*AIEmployeeConfig, 0, len(m.employees)),
		WorkLogs:  m.workLog,
	}
	for _, cfg := range m.employees {
		e.Employees = append(e.Employees, cfg)
	}
	data, _ := json.MarshalIndent(e, "", "  ")
	return string(data)
}

// GetDailyReport 生成数字员工每日工作报告
func (m *AIEmployeeManager) GetDailyReport(employeeID string) string {
	m.mu.RLock()
	records := append([]*AEWorkRecord(nil), m.workLog[employeeID]...)
	cfg := m.employees[employeeID]
	conv := m.conversations[employeeID]
	m.mu.RUnlock()

	if len(records) == 0 {
		return "今天暂无工作任务。"
	}

	todayStart := time.Now().Truncate(24 * time.Hour)
	var todayRecords []*AEWorkRecord
	for _, r := range records {
		if r.CreatedAt.After(todayStart) {
			todayRecords = append(todayRecords, r)
		}
	}
	if len(todayRecords) == 0 {
		return "今天暂无工作任务。"
	}

	name := employeeID
	if cfg != nil && cfg.Name != "" {
		name = cfg.Name
	}

	taskText := "未记录"
	deliveryText := "未记录明确交付物"
	resultText := "未记录完成结果"
	productionText := "未记录产出过程"

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("# %s 今日工作报告\n\n", name))
	sb.WriteString("## 本日概览\n")
	sb.WriteString(fmt.Sprintf("- 任务数：%d\n", len(todayRecords)))

	completed, failed, queued := 0, 0, 0
	for _, r := range todayRecords {
		switch r.Status {
		case "completed", "success", "done":
			completed++
		case "failed", "error":
			failed++
		default:
			queued++
		}
	}
	sb.WriteString(fmt.Sprintf("- 完成：%d\n- 失败：%d\n- 进行中/排队：%d\n\n", completed, failed, queued))

	if len(todayRecords) > 0 {
		last := todayRecords[len(todayRecords)-1]
		if strings.TrimSpace(last.Task) != "" {
			taskText = last.Task
		}
		if strings.TrimSpace(last.Result) != "" {
			resultText = textutil.TruncateRunes(strings.TrimSpace(last.Result), 1200)
			lines := strings.Split(resultText, "\n")
			deliveryText = strings.TrimSpace(lines[0])
			if deliveryText == "" {
				deliveryText = "已输出对应结果"
			}
			productionText = fmt.Sprintf("通过 %s 执行，创建于 %s，状态 %s。", last.Pipeline, last.CreatedAt.Format("15:04"), last.Status)
			if strings.TrimSpace(last.ToolCalls) != "" {
				productionText += " 工具调用：" + textutil.TruncateRunes(strings.TrimSpace(last.ToolCalls), 300)
			}
		} else {
			if strings.TrimSpace(last.Pipeline) != "" {
				productionText = fmt.Sprintf("通过 %s 执行，创建于 %s，状态 %s。", last.Pipeline, last.CreatedAt.Format("15:04"), last.Status)
			}
		}
	}

	sb.WriteString("## 接了什么任务\n")
	sb.WriteString("- " + taskText + "\n\n")
	sb.WriteString("## 交付什么\n")
	sb.WriteString("- " + deliveryText + "\n\n")
	sb.WriteString("## 结果如何\n")
	sb.WriteString("- " + resultText + "\n\n")
	sb.WriteString("## 怎么产出的\n")
	sb.WriteString("- " + productionText + "\n")

	if conv != nil && len(conv.History) > 0 {
		sb.WriteString("\n## 最近对话\n")
		for _, h := range conv.History {
			sb.WriteString("- " + textutil.TruncateRunes(h, 180) + "\n")
		}
	}

	reportBody := sb.String()
	reportRelPath := filepath.ToSlash(filepath.Join(".openkitty", "storage", "ae-reports",
		fmt.Sprintf("%s-%s.md", sanitizeReportFileName(name), time.Now().Format("2006-01-02"))))
	baseDir := "."
	if m.gateway != nil && m.gateway.config != nil && m.gateway.config.Workspace != "" {
		baseDir = m.gateway.config.Workspace
	}
	reportPath := filepath.Join(baseDir, filepath.FromSlash(reportRelPath))
	if err := os.MkdirAll(filepath.Dir(reportPath), 0755); err == nil {
		if err := os.WriteFile(reportPath, []byte(reportBody), 0644); err != nil && m.logger != nil {
			m.logger.Printf("[AE] write daily report failed: %v", err)
		}
	} else if m.logger != nil {
		m.logger.Printf("[AE] create daily report dir failed: %v", err)
	}

	return fmt.Sprintf("已生成日报：[%s](%s)\n\n%s", filepath.Base(reportPath), "/api/files/download?path=/"+reportRelPath, reportBody)
}

func sanitizeReportFileName(name string) string {
	var sb strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			sb.WriteRune(r)
		case r == '-' || r == '_':
			sb.WriteRune(r)
		default:
			sb.WriteRune('_')
		}
	}
	result := strings.Trim(sb.String(), "_")
	if result == "" {
		return "employee"
	}
	return result
}

// GetQueuedTasks 返回排队中的任务数

// handleAEExport exports DE conversation history.
func (g *GatewayService) handleAEExport(w http.ResponseWriter, r *http.Request) {
	employeeID := r.URL.Query().Get("id")
	format := r.URL.Query().Get("format")
	if format == "" {
		format = "md"
	}
	if employeeID == "" {
		http.Error(w, `{"error":"id required"}`, 400)
		return
	}
	if g.aeManager == nil {
		http.Error(w, `{"error":"AE manager not available"}`, 500)
		return
	}
	g.aeManager.mu.RLock()
	cfg, ok := g.aeManager.employees[employeeID]
	g.aeManager.mu.RUnlock()
	if !ok || (cfg.InternalOnly && !internalAEDiagnosticsRequested(r)) {
		http.Error(w, `{"error":"employee not found"}`, 404)
		return
	}

	var sb strings.Builder
	sb.WriteString("# " + cfg.Name + " 工作记录\n\n")
	sb.WriteString(fmt.Sprintf("导出时间: %s\n\n", time.Now().Format("2006-01-02 15:04:05")))

	if conv, ok := g.aeManager.conversations[employeeID]; ok && len(conv.History) > 0 {
		for _, h := range conv.History {
			sb.WriteString("- " + h + "\n")
		}
		sb.WriteString("\n")
	}

	if wl, ok := g.aeManager.workLog[employeeID]; ok {
		sb.WriteString("## 执行记录\n\n")
		for _, w := range wl {
			sb.WriteString(fmt.Sprintf("- [%s] %s (%s)\n", w.CreatedAt.Format("01-02 15:04"), textutil.TruncateRunes(w.Task, 80), w.Status))
		}
	}

	if format == "json" {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		type exportData struct {
			Name    string   `json:"name"`
			History []string `json:"history"`
		}
		ed := exportData{Name: cfg.Name}
		if conv, ok := g.aeManager.conversations[employeeID]; ok {
			ed.History = conv.History
		}
		json.NewEncoder(w).Encode(map[string]interface{}{"ok": true, "data": ed})
		return
	}

	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%s_conversation.md", employeeID))
	w.Write([]byte(sb.String()))
}

// handleTenantStats returns per-tenant usage statistics.
func (g *GatewayService) handleTenantStats(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if g.db == nil {
		json.NewEncoder(w).Encode(map[string]interface{}{"ok": false, "error": "db unavailable"})
		return
	}
	type tenantInfo struct {
		TenantID     string `json:"tenant_id"`
		TotalQueries int64  `json:"total_queries"`
		ZeroResults  int64  `json:"zero_results"`
		UpFeedback   int64  `json:"up_feedback"`
		DETasks      int64  `json:"de_tasks"`
	}
	var tenants []tenantInfo
	g.db.Model(&RAGSearchLog{}).Select("tenant_id, COUNT(*) as total_queries, SUM(CASE WHEN hit_count=0 THEN 1 ELSE 0 END) as zero_results, SUM(CASE WHEN feedback='up' THEN 1 ELSE 0 END) as up_feedback").Group("tenant_id").Scan(&tenants)

	if tenants == nil {
		tenants = []tenantInfo{}
	}
	json.NewEncoder(w).Encode(map[string]interface{}{"ok": true, "tenants": tenants})
}
func (m *AIEmployeeManager) GetQueuedTasks(employeeID string) int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if q := m.taskQueue[employeeID]; q != nil {
		return len(q)
	}
	return 0
}

type aeRuntimeState struct {
	Status           string
	Busy             bool
	SessionAlive     bool
	ActiveSessions   int
	ActiveTurns      int
	WaitingApprovals int
}

func (g *GatewayService) employeeRuntimeState(employeeID string, memoryBusy bool) aeRuntimeState {
	state := aeRuntimeState{Status: "idle", Busy: memoryBusy}
	if memoryBusy {
		state.Status = "busy"
	}
	if employeeID != "codex-specialist" || g == nil || g.codexInteractive == nil {
		return state
	}
	codexState := g.codexInteractive.RuntimeState()
	state.SessionAlive = codexState.SessionAlive
	state.ActiveSessions = codexState.ActiveSessions
	state.ActiveTurns = codexState.ActiveTurns
	state.WaitingApprovals = codexState.WaitingApprovals
	state.Busy = codexState.ActiveTurns > 0 || codexState.WaitingApprovals > 0
	switch {
	case codexState.WaitingApprovals > 0:
		state.Status = "waiting_approval"
	case codexState.ActiveTurns > 0:
		state.Status = "busy"
	default:
		state.Status = "idle"
	}
	return state
}

func aeDisplayPipeline(cfg *AIEmployeeConfig) string {
	if cfg == nil {
		return ""
	}
	if cfg.ID == "investment-advisor" {
		return "investment-advisory"
	}
	if cfg.Direct != nil {
		return "direct"
	}
	return cfg.Pipeline
}

func aeDisplayModel(cfg *AIEmployeeConfig) (model string, source string) {
	route := aeModelRouteInfo(cfg, "text-to-text")
	return route.Model, route.Source
}

func aeModelRouteInfo(cfg *AIEmployeeConfig, capability string) ModelRouteInfo {
	capability = normalizeModelCapability(capability)
	if cfg == nil {
		return ModelRouteInfo{Capability: capability, Source: "none", Available: false, Reason: "missing employee config"}
	}
	if globalProviderRegistry != nil {
		return globalProviderRegistry.ExplainResolveForAEBinding(cfg.ID, cfg.ModelBinding, capability)
	}
	bindingText := strings.TrimSpace(cfg.ModelBinding)
	if bindingText != "" {
		var binding map[string]string
		if json.Unmarshal([]byte(bindingText), &binding) == nil {
			if model := strings.TrimSpace(binding[capability]); model != "" {
				return ModelRouteInfo{EmployeeID: cfg.ID, Capability: capability, Model: model, Source: "model_binding", Available: false, Reason: "provider registry not ready"}
			}
			if model := strings.TrimSpace(binding["default"]); model != "" && isTextModelCapability(capability) {
				return ModelRouteInfo{EmployeeID: cfg.ID, Capability: capability, Model: model, Source: "model_binding_default", Available: false, Reason: "provider registry not ready"}
			}
		}
	}
	if isTextModelCapability(capability) {
		return ModelRouteInfo{EmployeeID: cfg.ID, Capability: capability, Model: "minimax-m3", Source: "default_minimax", Available: false, Reason: "provider registry not ready"}
	}
	return ModelRouteInfo{EmployeeID: cfg.ID, Capability: capability, Source: "none", Available: false, Reason: "provider registry not ready"}
}

func aeModelRoutes(cfg *AIEmployeeConfig) []ModelRouteInfo {
	capabilities := aeRequiredModelCapabilities(cfg)
	routes := make([]ModelRouteInfo, 0, len(capabilities))
	for _, capability := range capabilities {
		routes = append(routes, aeModelRouteInfo(cfg, capability))
	}
	return routes
}

func aeRequiredModelCapabilities(cfg *AIEmployeeConfig) []string {
	capabilities := []string{"text-to-text"}
	if cfg == nil {
		return capabilities
	}
	if strings.TrimSpace(cfg.ModelBinding) != "" {
		var binding map[string]string
		if json.Unmarshal([]byte(cfg.ModelBinding), &binding) == nil {
			for capability := range binding {
				capability = normalizeModelCapability(capability)
				if capability == "default" {
					continue
				}
				capabilities = append(capabilities, capability)
			}
		}
	}
	return uniqueSortedStrings(capabilities)
}

type AEReadiness struct {
	Level            string   `json:"level"`
	Score            int      `json:"score"`
	Reasons          []string `json:"reasons"`
	RequiredTools    []string `json:"required_tools,omitempty"`
	MissingTools     []string `json:"missing_tools,omitempty"`
	SampleTask       string   `json:"sample_task,omitempty"`
	OfficeReady      bool     `json:"office_ready"`
	EvidenceRequired bool     `json:"evidence_required"`
}

func (g *GatewayService) assessAEReadiness(cfg *AIEmployeeConfig) AEReadiness {
	r := AEReadiness{Level: "placeholder", Reasons: []string{}}
	if cfg == nil {
		r.Reasons = append(r.Reasons, "missing employee config")
		return r
	}

	score := 0
	if strings.TrimSpace(cfg.ID) != "" && strings.TrimSpace(cfg.Name) != "" && strings.TrimSpace(cfg.AtName) != "" {
		score += 15
	} else {
		r.Reasons = append(r.Reasons, "missing id/name/at_name")
	}
	if strings.TrimSpace(cfg.Role) != "" {
		score += 10
	} else {
		r.Reasons = append(r.Reasons, "missing role")
	}

	hasExecutable := false
	professionalOnlyPipeline := false
	var requiredTools []string
	pipelineName := strings.TrimSpace(cfg.Pipeline)
	if cfg.ID == "investment-advisor" {
		pipelineName = "investment-advisory"
	}
	if cfg.Direct != nil && cfg.ID != "investment-advisor" {
		hasExecutable = true
		score += 35
		if singleStepPipelineIsExpected(cfg.ID) {
			score += 10
		}
		if strings.TrimSpace(cfg.Direct.Prompt) == "" {
			score -= 20
			r.Reasons = append(r.Reasons, "direct mode has empty prompt")
		}
		if len(cfg.Direct.Tools) > 0 {
			score += 5
			requiredTools = append(requiredTools, cfg.Direct.Tools...)
		}
	} else if pipelineName != "" {
		if p, ok := g.resolveAEPipeline(pipelineName); ok && len(p.Steps) > 0 {
			hasExecutable = true
			professionalOnlyPipeline = pipelineStepsUseOnlyProfessionalAgents(p.Steps)
			score += 35
			stepScore, stepReasons := scorePipelineStepsForReadiness(cfg.ID, p.Steps)
			score += stepScore
			r.Reasons = append(r.Reasons, stepReasons...)
			requiredTools = append(requiredTools, requiredToolsForPipelineSteps(p.Steps)...)
			if len(p.Steps) == 1 && !singleStepPipelineIsExpected(cfg.ID) && !professionalOnlyPipeline {
				score -= 1
				r.Reasons = append(r.Reasons, "unexpected single-step pipeline needs review")
			}
		} else {
			r.Reasons = append(r.Reasons, "pipeline not found or has no steps: "+pipelineName)
		}
	} else {
		r.Reasons = append(r.Reasons, "no direct mode or pipeline")
	}

	if hasExecutable {
		r.Reasons = append(r.Reasons, "has executable entry")
	}
	r.RequiredTools = uniqueSortedStrings(requiredTools)
	if g != nil && g.pipelineExec != nil && len(r.RequiredTools) > 0 {
		r.MissingTools = g.pipelineExec.MissingTools(r.RequiredTools)
		if len(r.MissingTools) > 0 {
			score -= min(25, len(r.MissingTools)*8)
			r.Reasons = append(r.Reasons, "missing tools: "+strings.Join(r.MissingTools, ", "))
		} else {
			r.Reasons = append(r.Reasons, "all required tools registered")
		}
	}
	if strings.TrimSpace(aeSystemMessage(cfg)) != "" {
		score += 5
	}
	modelRoutes := aeModelRoutes(cfg)
	modelReady := professionalOnlyPipeline || len(modelRoutes) > 0
	modelPending := false
	modelBlocked := false
	var unavailableModelRoutes []string
	if professionalOnlyPipeline {
		score += 5
		r.Reasons = append(r.Reasons, "isolated professional-agent pipeline configured; host model route not required; backend health is checked at execution")
	} else {
		for _, route := range modelRoutes {
			if strings.TrimSpace(route.Model) != "" && route.Available {
				continue
			}
			if strings.TrimSpace(route.Reason) == "provider registry not ready" {
				modelPending = true
				continue
			}
			modelReady = false
			reason := route.Reason
			if reason == "" {
				reason = "missing model route"
			}
			modelBlocked = true
			unavailableModelRoutes = append(unavailableModelRoutes, route.Capability+": "+reason)
		}
		if modelReady && !modelPending {
			score += 5
			r.Reasons = append(r.Reasons, "model routes ready")
		} else if modelPending && !modelBlocked {
			r.Reasons = append(r.Reasons, "model route pending: provider registry not ready")
		} else {
			score -= 15
			r.Reasons = append(r.Reasons, "model route unavailable: "+strings.Join(unavailableModelRoutes, "; "))
		}
	}
	if isOfficePriorityAE(cfg.ID) {
		score += 10
		r.OfficeReady = hasExecutable && score >= 70
		r.SampleTask = sampleTaskForAE(cfg.ID)
	}
	if requiresEvidenceBoundary(cfg.ID) {
		r.EvidenceRequired = true
	}
	if score > 100 {
		score = 100
	}
	if score < 0 {
		score = 0
	}
	r.Score = score
	switch {
	case hasExecutable && (modelReady || (modelPending && !modelBlocked)) && score >= 75:
		r.Level = "ready"
	case hasExecutable && modelReady && score >= 45:
		r.Level = "needs_review"
	default:
		r.Level = "placeholder"
	}
	if r.OfficeReady && r.Level == "placeholder" {
		r.OfficeReady = false
	}
	return r
}

func requiredToolsForPipelineSteps(steps []PipelineStep) []string {
	var tools []string
	for _, step := range steps {
		if step.NoTools {
			continue
		}
		tools = append(tools, step.Tools...)
	}
	return tools
}

func uniqueSortedStrings(values []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func (g *GatewayService) resolveAEPipeline(name string) (Pipeline, bool) {
	name = strings.TrimSpace(name)
	if name == "" {
		return Pipeline{}, false
	}
	if g != nil && g.pipelineExec != nil {
		g.pipelineExec.pipelinesMu.RLock()
		if p, ok := g.pipelineExec.pipelines[name]; ok {
			g.pipelineExec.pipelinesMu.RUnlock()
			return p, true
		}
		g.pipelineExec.pipelinesMu.RUnlock()
	}
	if g != nil {
		if p, ok := g.defaultPipelineSet()[name]; ok {
			return p, true
		}
	}
	if tmpl, ok := GetPipelineTemplates()[name]; ok {
		return Pipeline{Name: tmpl.Name, Desc: tmpl.Description, Steps: tmpl.Steps}, true
	}
	if g != nil && g.db != nil {
		var rec PipelineDBRecord
		if err := g.db.Where("name = ?", name).First(&rec).Error; err == nil && strings.TrimSpace(rec.StepsJSON) != "" {
			var steps []PipelineStep
			if json.Unmarshal([]byte(rec.StepsJSON), &steps) == nil && len(steps) > 0 {
				return Pipeline{Name: rec.Name, Desc: rec.Description, Steps: steps}, true
			}
		}
	}
	return Pipeline{}, false
}

func scorePipelineStepsForReadiness(employeeID string, steps []PipelineStep) (int, []string) {
	score := 0
	reasons := []string{}
	hasFinalNoTools := false
	for i, step := range steps {
		if strings.TrimSpace(step.Prompt) == "" {
			reasons = append(reasons, fmt.Sprintf("step %d has empty prompt", i))
			continue
		}
		score += 3
		if step.MaxIter > 0 {
			score += 1
		}
		if step.NoTools || len(step.Tools) > 0 {
			score += 1
		}
		if i == len(steps)-1 && step.NoTools {
			hasFinalNoTools = true
		}
	}
	if len(steps) >= 2 {
		score += 5
	}
	if len(steps) == 1 && singleStepPipelineIsExpected(employeeID) {
		score += 10
	}
	if hasFinalNoTools {
		score += 5
	}
	if score > 25 {
		score = 25
	}
	return score, reasons
}

func singleStepPipelineIsExpected(employeeID string) bool {
	switch employeeID {
	case "market-analyst", "cs-pre-sales", "cs-after-sales":
		return true
	default:
		return false
	}
}

func pipelineStepsUseOnlyProfessionalAgents(steps []PipelineStep) bool {
	if len(steps) == 0 {
		return false
	}
	for i := range steps {
		if !pipelineStepUsesOnlyProfessionalAgents(&steps[i]) {
			return false
		}
	}
	return true
}

func isOfficePriorityAE(id string) bool {
	switch id {
	case "meeting-secretary", "sales-assistant", "product-manager", "data-analyst", "hr-recruiter", "translator", "lead-researcher", "ppt-polish-specialist", "bid-specialist":
		return true
	default:
		return false
	}
}

func requiresEvidenceBoundary(id string) bool {
	switch id {
	case "lead-researcher", "investment-advisor", "legal-review", "data-analyst", "risk-manager", "bid-specialist":
		return true
	default:
		return false
	}
}

func sampleTaskForAE(id string) string {
	switch id {
	case "meeting-secretary":
		return "@秘书 请整理会议纪要，提取决议、待办、负责人和截止日期"
	case "sales-assistant":
		return "@销售助理 请读取销售线索表，整理今日跟进清单、报价建议和回访提醒"
	case "product-manager":
		return "@产品经理 请基于需求材料撰写 PRD、拆分 WBS，并给出工期、人力、成本和对外报价估算"
	case "data-analyst":
		return "@数据分析 请读取 CSV，分析趋势、转化率并生成图表报告"
	case "hr-recruiter":
		return "@HR招聘 请评估简历和岗位匹配度，并生成面试题"
	case "translator":
		return "@翻译专员 请把这份文档翻译成专业英文并保持 Markdown 结构"
	case "lead-researcher":
		return "@潜客研究员 帮我寻找E盘ai-node目录下项目，AI平台的潜在客户"
	case "ppt-polish-specialist":
		return "@PPT润色 请润色这份PPT，优化结构、版式和视觉风格，并生成新的可编辑PPTX"
	case "bid-specialist":
		return "@投竞标 请解读这份招标文件，整理响应矩阵、标书初稿和风险清单"
	default:
		return ""
	}
}

// ====================== Web API Handlers ======================

// handleAEList returns status of all digital employees.
func (g *GatewayService) handleAEList(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if g.aeManager == nil {
		json.NewEncoder(w).Encode(map[string]interface{}{"employees": []interface{}{}, "ok": true})
		return
	}
	type deInfo struct {
		ID              string           `json:"id"`
		Name            string           `json:"name"`
		AtName          string           `json:"at_name"`
		Note            string           `json:"note,omitempty"`
		Role            string           `json:"role"`
		Kind            string           `json:"kind"`   // "single" or "router"
		Status          string           `json:"status"` // idle, busy
		SessionAlive    bool             `json:"session_alive,omitempty"`
		ActiveSessions  int              `json:"active_sessions,omitempty"`
		ActiveTurns     int              `json:"active_turns,omitempty"`
		WaitingApproval int              `json:"waiting_approvals,omitempty"`
		Pipeline        string           `json:"pipeline"`
		QueuedTasks     int              `json:"queued_tasks"`
		TotalTasks      int              `json:"total_tasks"`
		LastTask        string           `json:"last_task"`
		LastError       string           `json:"last_error"`
		LastTalkTime    string           `json:"last_talk_time"`
		Tier            string           `json:"tier,omitempty"`
		Model           string           `json:"model,omitempty"`
		ModelSource     string           `json:"model_source,omitempty"`
		ModelProvider   string           `json:"model_provider,omitempty"`
		ModelAvailable  bool             `json:"model_available"`
		ModelReason     string           `json:"model_reason,omitempty"`
		ModelRoutes     []ModelRouteInfo `json:"model_routes,omitempty"`
		Readiness       AEReadiness      `json:"readiness"`
		Hidden          bool             `json:"hidden,omitempty"`
		InternalOnly    bool             `json:"internal_only,omitempty"`
	}
	var list []deInfo
	includeHidden := truthyQuery(r, "include_hidden")
	includeInternal := internalAEDiagnosticsRequested(r)
	g.aeManager.mu.RLock()
	for id, cfg := range g.aeManager.employees {
		if cfg.InternalOnly && !includeInternal {
			continue
		}
		if cfg.Hidden && !includeHidden {
			continue
		}
		runtimeState := g.employeeRuntimeState(id, g.aeManager.busy[id])
		route := aeModelRouteInfo(cfg, "text-to-text")
		info := deInfo{
			ID:              id,
			Name:            cfg.Name,
			AtName:          cfg.AtName,
			Note:            cfg.Note,
			Role:            cfg.Role,
			Kind:            cfg.AEKind(),
			Status:          runtimeState.Status,
			SessionAlive:    runtimeState.SessionAlive,
			ActiveSessions:  runtimeState.ActiveSessions,
			ActiveTurns:     runtimeState.ActiveTurns,
			WaitingApproval: runtimeState.WaitingApprovals,
			Pipeline:        aeDisplayPipeline(cfg),
			Tier:            cfg.Tier,
			Model:           route.Model,
			ModelSource:     route.Source,
			ModelProvider:   route.Provider,
			ModelAvailable:  route.Available,
			ModelReason:     route.Reason,
			ModelRoutes:     aeModelRoutes(cfg),
			Readiness:       g.assessAEReadiness(cfg),
			Hidden:          cfg.Hidden,
			InternalOnly:    cfg.InternalOnly,
		}
		info.QueuedTasks = g.aeManager.GetQueuedTasks(id)
		if wl, ok := g.aeManager.workLog[id]; ok {
			wl = currentAEWorkRecords(*cfg, wl)
			info.TotalTasks = len(wl)
			for i := len(wl) - 1; i >= 0; i-- {
				if wl[i].Status == "failed" {
					info.LastError = wl[i].Task
					break
				}
			}
			if len(wl) > 0 {
				info.LastTask = wl[len(wl)-1].Task
			}
		}
		if conv, ok := g.aeManager.conversations[id]; ok {
			info.LastTalkTime = conv.LastTalk.Format("15:04")
		}
		list = append(list, info)
	}
	g.aeManager.mu.RUnlock()
	if list == nil {
		list = []deInfo{}
	}
	// Sort by canonical department order
	order := deCanonicalOrder()
	sort.SliceStable(list, func(i, j int) bool {
		oi, oj := order[list[i].ID], order[list[j].ID]
		if oi == 0 {
			oi = 999
		}
		if oj == 0 {
			oj = 999
		}
		return oi < oj
	})
	json.NewEncoder(w).Encode(map[string]interface{}{"employees": list, "ok": true})
}

func (g *GatewayService) handleAEReadiness(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if g.aeManager == nil {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"ok": true, "summary": map[string]int{}, "employees": []interface{}{},
		})
		return
	}
	type readinessRow struct {
		ID           string      `json:"id"`
		Name         string      `json:"name"`
		AtName       string      `json:"at_name"`
		Pipeline     string      `json:"pipeline"`
		Readiness    AEReadiness `json:"readiness"`
		InternalOnly bool        `json:"internal_only,omitempty"`
	}
	rows := []readinessRow{}
	summary := map[string]int{"ready": 0, "needs_review": 0, "placeholder": 0}
	officeReady := 0

	g.aeManager.mu.RLock()
	for id, cfg := range g.aeManager.employees {
		if cfg.InternalOnly && !internalAEDiagnosticsRequested(r) {
			continue
		}
		readiness := g.assessAEReadiness(cfg)
		summary[readiness.Level]++
		if readiness.OfficeReady {
			officeReady++
		}
		rows = append(rows, readinessRow{
			ID: id, Name: cfg.Name, AtName: cfg.AtName, Pipeline: aeDisplayPipeline(cfg), Readiness: readiness, InternalOnly: cfg.InternalOnly,
		})
	}
	g.aeManager.mu.RUnlock()

	order := deCanonicalOrder()
	sort.SliceStable(rows, func(i, j int) bool {
		oi, oj := order[rows[i].ID], order[rows[j].ID]
		if oi == 0 {
			oi = 999
		}
		if oj == 0 {
			oj = 999
		}
		return oi < oj
	})

	json.NewEncoder(w).Encode(map[string]interface{}{
		"ok":           true,
		"summary":      summary,
		"office_ready": officeReady,
		"employees":    rows,
	})
}

// aeSystemMessage returns the AE's system_message, falling back to hardcoded default, then generic.
func aeSystemMessage(cfg *AIEmployeeConfig) string {
	if cfg.SystemMessage != "" {
		return cfg.SystemMessage
	}
	for _, d := range defaultAIEmployees() {
		if d.ID == cfg.ID && d.SystemMessage != "" {
			return d.SystemMessage
		}
	}
	if cfg.Name != "" {
		return "你是" + cfg.Name + "。" + cfg.Role + "。"
	}
	return ""
}

// handleAEDetail returns detailed info for a single digital employee.
func (g *GatewayService) handleAEDetail(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	employeeID := r.URL.Query().Get("id")
	if employeeID == "" {
		http.Error(w, `{"error":"id required"}`, 400)
		return
	}
	if g.aeManager == nil {
		http.Error(w, `{"error":"AE manager not available"}`, 500)
		return
	}
	g.aeManager.mu.RLock()
	cfg, ok := g.aeManager.employees[employeeID]
	g.aeManager.mu.RUnlock()
	if !ok || (cfg.InternalOnly && !internalAEDiagnosticsRequested(r)) {
		http.Error(w, `{"error":"employee not found"}`, 404)
		return
	}

	type workSummary struct {
		ID        string `json:"id"`
		Task      string `json:"task"`
		Status    string `json:"status"`
		ExecID    string `json:"exec_id,omitempty"`
		CreatedAt string `json:"created_at"`
	}
	var workLog []workSummary
	if wl, ok := g.aeManager.workLog[employeeID]; ok {
		wl = currentAEWorkRecords(*cfg, wl)
		for _, w := range wl {
			workLog = append(workLog, workSummary{
				ID: w.ID, Task: w.Task, Status: w.Status, ExecID: w.ExecID,
				CreatedAt: w.CreatedAt.Format("01-02 15:04"),
			})
		}
	}
	if workLog == nil {
		workLog = []workSummary{}
	}

	var history []string
	if conv, ok := g.aeManager.conversations[employeeID]; ok {
		history = conv.History
	}
	if history == nil {
		history = []string{}
	}

	// Include active pipeline execution progress
	type stepInfo struct {
		Name     string `json:"name"`
		Status   string `json:"status"`
		Duration int64  `json:"duration_ms"`
	}
	type pipeProgress struct {
		ExecID       string     `json:"exec_id"`
		PipelineName string     `json:"pipeline_name"`
		Status       string     `json:"status"`
		CurrentStep  int        `json:"current_step"`
		TotalSteps   int        `json:"total_steps"`
		Steps        []stepInfo `json:"steps"`
	}
	var progress *pipeProgress
	if wl, ok := g.aeManager.workLog[employeeID]; ok && g.pipelineExec != nil {
		wl = currentAEWorkRecords(*cfg, wl)
		for i := len(wl) - 1; i >= 0; i-- {
			if wl[i].ExecID == "" {
				continue
			}
			if exec, ok := g.pipelineExec.GetExecution(wl[i].ExecID); ok {
				if exec.Status == "running" || exec.Status == "waiting_approval" {
					pp := &pipeProgress{
						ExecID: exec.ID, PipelineName: exec.PipelineName,
						Status: exec.Status, CurrentStep: exec.CurrentStep,
						TotalSteps: len(exec.Steps),
					}
					for _, s := range exec.Steps {
						pp.Steps = append(pp.Steps, stepInfo{
							Name: s.Name, Status: s.Status, Duration: s.DurationMs,
						})
					}
					progress = pp
					break
				}
			}
		}
	}
	route := aeModelRouteInfo(cfg, "text-to-text")
	runtimeState := g.employeeRuntimeState(employeeID, g.aeManager.busy[employeeID])

	json.NewEncoder(w).Encode(map[string]interface{}{
		"ok":                true,
		"name":              cfg.Name,
		"at_name":           cfg.AtName,
		"note":              cfg.Note,
		"kind":              cfg.AEKind(),
		"pipeline":          aeDisplayPipeline(cfg),
		"pipeline_role":     cfg.Role,
		"model":             route.Model,
		"model_source":      route.Source,
		"model_provider":    route.Provider,
		"model_available":   route.Available,
		"model_reason":      route.Reason,
		"model_route":       route,
		"model_routes":      aeModelRoutes(cfg),
		"model_binding":     cfg.ModelBinding,
		"readiness":         g.assessAEReadiness(cfg),
		"system_message":    aeSystemMessage(cfg),
		"routing_examples":  cfg.GetRoutingExamples(),
		"tenant":            cfg.TenantID,
		"kb_slugs":          cfg.RAGKBSlugs,
		"status":            runtimeState.Status,
		"busy":              runtimeState.Busy,
		"session_alive":     runtimeState.SessionAlive,
		"active_sessions":   runtimeState.ActiveSessions,
		"active_turns":      runtimeState.ActiveTurns,
		"waiting_approvals": runtimeState.WaitingApprovals,
		"work_log":          workLog,
		"history":           history,
		"pipeline_progress": progress,
	})
}

// deCanonicalOrder returns the display order for DE IDs (matching defaultAIEmployees).
func deCanonicalOrder() map[string]int {
	return map[string]int{
		// 📋 综合办公
		"project-assistant": 1,
		"meeting-secretary": 2,
		"admin-assistant":   3,
		// 💰 投资与财务
		"investment-advisor":             10,
		"quant-trader":                   11,
		"investment-research-manager":    12,
		"financial-intelligence-analyst": 13,
		"risk-manager":                   14,
		"finance-specialist":             15,
		// ⚖️ 法务
		"legal-review":   20,
		"bid-specialist": 21,
		// 📝 内容与市场
		"content-editor":        30,
		"ppt-polish-specialist": 31,
		"market-analyst":        32,
		"sales-assistant":       33,
		"customer-service":      34,
		"social-media":          35,
		"translator":            36,
		// 📊 数据与技术
		"data-analyst":    40,
		"it-ops":          41,
		"product-manager": 42,
		"mentor-buddy":    43,
		"code-review-bot": 44,
		// 👥 人事
		"hr-recruiter": 50,
		"trainer":      51,
		// 🏭 运营与供应链
		"supply-chain":  60,
		"qa-specialist": 61,
		"event-planner": 62,
		// 📈 获客与销售
		"lead-researcher":  70,
		"lead-acquisition": 71,
	}
}

func (m *AIEmployeeManager) materializeAESessionContext(sessionID string) (string, string) {
	if m == nil || m.gateway == nil || m.gateway.db == nil || strings.TrimSpace(sessionID) == "" {
		return "", ""
	}
	var rows []ChatMessage
	m.gateway.db.Where("session_id = ? AND role IN ? AND status IN ?", sessionID,
		[]string{"user", "assistant"}, []string{"completed", "degraded"}).
		Order("created_at desc").Limit(12).Find(&rows)
	for left, right := 0, len(rows)-1; left < right; left, right = left+1, right-1 {
		rows[left], rows[right] = rows[right], rows[left]
	}
	messages := make([]aiemployee.SessionContextMessage, 0, len(rows))
	for _, row := range rows {
		limit := 1200
		if row.Role == "assistant" {
			limit = 5000
		}
		content := sessioncore.CleanCarryoverContent(row.Role, row.Content, limit)
		if content == "" {
			continue
		}
		messages = append(messages, aiemployee.SessionContextMessage{ID: row.ID, Role: row.Role,
			AgentName: row.AgentName, CreatedAt: row.CreatedAt.Format(time.RFC3339),
			SourceLocator: fmt.Sprintf("session://%s/messages/%d", sessionID, row.ID), Content: content})
	}
	return aiemployee.MaterializeSessionContext(sessionID, m.aiEmployeeWorkspace(), messages, time.Now())
}

func buildAIEmployeeFinalRecoveryMessages(originalRequest string, results []string) []map[string]interface{} {
	return aiemployee.BuildFinalRecoveryMessages(originalRequest, results, func(value string) (aiemployee.ToolRecord, bool) {
		record, ok := parseOpenKittyToolRecord(value)
		return aiemployee.ToolRecord{Tool: record.Tool, Succeeded: record.Succeeded}, ok
	})
}

func (a *LLMAgent) recoverAIEmployeeFinalAnswer(ctx context.Context, originalRequest string, results []string) (string, error) {
	if a == nil {
		return "", fmt.Errorf("AI employee final recovery has no agent")
	}
	recoveryCtx := toolregistry.WithSuppressedDefinitions(ctx)
	recoveryCtx = modelruntime.WithTemperatureOverride(recoveryCtx, 0.1)
	recoveryCtx = modelruntime.WithOutputTokenLimit(recoveryCtx, aiemployee.FinalRecoveryOutputTokens)
	recoveryCtx = modelruntime.WithCallTimeoutOverride(recoveryCtx, aiemployee.FinalRecoveryTimeout)
	recoveryCtx = modelruntime.WithCallMaxAttemptsOverride(recoveryCtx, 1)
	response, err := a.callLLMStream(recoveryCtx, buildAIEmployeeFinalRecoveryMessages(originalRequest, results),
		a.toolDefinitionsForContext(recoveryCtx), func(StreamChunk) {})
	if err != nil {
		return "", err
	}
	if response == nil || len(response.ToolCalls) > 0 || isIncompleteLLMFinishReason(response.FinishReason) {
		return "", fmt.Errorf("AI employee final recovery returned an invalid response")
	}
	output := cleanUserFacingFinalAnswer(response.Content)
	if strings.TrimSpace(output) == "" {
		return "", fmt.Errorf("AI employee final recovery returned empty output")
	}
	return output, nil
}

func (g *GatewayService) defaultPipelineSet() map[string]Pipeline {
	return pipelinecatalog.Builtins()
}
