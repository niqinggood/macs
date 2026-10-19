package app

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/chromedp/chromedp"
	"github.com/fsnotify/fsnotify"
	"github.com/gorilla/websocket"
	"github.com/robfig/cron/v3"
	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/encoding/japanese"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/encoding/traditionalchinese"
	"golang.org/x/text/transform"
	"gorm.io/datatypes"
	"gorm.io/gorm"
	"html"
	"io"
	"io/fs"
	"log"
	rootassets "mac"
	"mac/internal/agentloop"
	"mac/internal/agentos"
	"mac/internal/agentresult"
	"mac/internal/aiemployee"
	approvalcore "mac/internal/approval"
	attachmentcore "mac/internal/attachments"
	authcore "mac/internal/auth"
	"mac/internal/browserruntime"
	"mac/internal/channelhost"
	channelcore "mac/internal/channels"
	communitycore "mac/internal/community"
	"mac/internal/configvalue"
	"mac/internal/conversation"
	customerservice "mac/internal/customerservice"
	"mac/internal/eventbus"
	"mac/internal/eventstream"
	documentapi "mac/internal/gateway/document"
	gatewayhttp "mac/internal/gateway/httpx"
	gatewaymigrations "mac/internal/gateway/migrations"
	gatewayrisk "mac/internal/gateway/risk"
	"mac/internal/gitutil"
	"mac/internal/globalstate"
	hookscore "mac/internal/hooks"
	mcpcore "mac/internal/mcp"
	memorycore "mac/internal/memory"
	middlewarecore "mac/internal/middleware"
	"mac/internal/modelruntime"
	pipelinecore "mac/internal/pipeline"
	"mac/internal/processutil"
	"mac/internal/projectctx"
	ragcore "mac/internal/rag"
	"mac/internal/ragmodel"
	"mac/internal/runevents"
	"mac/internal/runtimeutil"
	"mac/internal/security"
	sessioncore "mac/internal/session"
	"mac/internal/skillpath"
	skilldomain "mac/internal/skills"
	tenantcore "mac/internal/tenant"
	"mac/internal/textnormalize"
	"mac/internal/textutil"
	todocore "mac/internal/todo"
	"mac/internal/toolcontract"
	"mac/internal/toolmetrics"
	"mac/internal/toolregistry"
	businesstools "mac/internal/tools/business"
	desktoptools "mac/internal/tools/desktop"
	knowledgetools "mac/internal/tools/knowledge"
	networktools "mac/internal/tools/network"
	operationstools "mac/internal/tools/operations"
	voicecore "mac/internal/voice"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

var wsUpgrader = websocket.Upgrader{
	CheckOrigin: gatewayhttp.RequestHasNoOriginOrSameHost,
}

var apiRateLimiter = gatewayhttp.NewRateLimiter(60, 10)

var globalBrowserTaskManager = browserruntime.NewBrowserTaskManager()
var globalDesktopTaskManager = desktoptools.NewDesktopTaskManager()

// gatewayDB is the application database shared by gateway-bound services.
var gatewayDB *gorm.DB

var todoManagerGlobal *todocore.Manager

func newTodoManager(db *gorm.DB, engine *MultiAgentEngine) *todocore.Manager {
	return todocore.NewManager(
		db,
		engine.GetDefaultAgentName,
		func(ctx context.Context, agentName, prompt string) (string, error) {
			agent, err := engine.getAgent(agentName)
			if err != nil {
				return "", err
			}
			return agent.Generate(ctx, prompt)
		},
	)
}

func (g *GatewayService) aguiSSEHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	flusher, _ := w.(http.Flusher)
	clientID := fmt.Sprintf("agui_%d", time.Now().UnixNano())
	eventChannel := make(chan Event, 20)
	g.sseMutex.Lock()
	if g.sseClients["*"] == nil {
		g.sseClients["*"] = make(map[string]chan Event)
	}
	g.sseClients["*"][clientID] = eventChannel
	g.sseMutex.Unlock()
	defer func() {
		g.sseMutex.Lock()
		delete(g.sseClients["*"], clientID)
		g.sseMutex.Unlock()
	}()

	initial := eventbus.AGUIEvent{Type: eventbus.AGUIStateSnapshot, Patch: globalSharedState.Snapshot()}
	data, _ := json.Marshal(initial)
	fmt.Fprintf(w, "event: agui\ndata: %s\n\n", string(data))
	flusher.Flush()
	for {
		select {
		case event := <-eventChannel:
			mapped := eventbus.MapToAGUI(eventbus.SourceEvent{Type: event.Type, Data: event.Data, TaskID: event.TaskID})
			if mapped != nil {
				data, _ = json.Marshal(mapped)
				fmt.Fprintf(w, "event: agui\ndata: %s\n\n", string(data))
				flusher.Flush()
			}
		case <-r.Context().Done():
			return
		}
	}
}

func (g *GatewayService) handleMessages(w http.ResponseWriter, r *http.Request) {
	gatewayhttp.HandleMessages(w, eventbus.DefaultNotificationCenter)
}

func (g *GatewayService) handleConfirmAction(w http.ResponseWriter, r *http.Request) {
	gatewayhttp.HandleConfirmAction(w, r, eventbus.DefaultNotificationCenter)
}

func (g *GatewayService) handleShutdown(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"ok": true, "msg": "shutting down"})
	go func() {
		time.Sleep(500 * time.Millisecond)
		os.Exit(0)
	}()
}

func (g *GatewayService) handleDesktopTasks(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	limit := 50
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		if value, err := strconv.Atoi(raw); err == nil && value > 0 {
			limit = value
		}
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"tasks": globalDesktopTaskManager.List(limit)})
}

func (g *GatewayService) handleDesktopTaskDetail(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	if id == "" {
		http.Error(w, `{"error":"id required"}`, http.StatusBadRequest)
		return
	}
	record, ok := globalDesktopTaskManager.Get(id)
	if !ok {
		http.Error(w, `{"error":"desktop task not found"}`, http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"task": record})
}

func (g *GatewayService) assessAndLogGatewayUserContent(source, text string) gatewayrisk.Decision {
	if g == nil || g.logger == nil {
		return gatewayrisk.AssessAndLogUserContent(nil, source, text)
	}
	return gatewayrisk.AssessAndLogUserContent(g.logger, source, text)
}

func (g *GatewayService) handleSharedState(w http.ResponseWriter, r *http.Request) {
	var broadcast func(map[string]interface{})
	if g != nil && g.sseClients != nil {
		broadcast = g.broadcastStateChange
	}
	gatewayhttp.HandleSharedState(w, r, globalSharedState, broadcast)
}

func (g *GatewayService) broadcastStateChange(updates map[string]interface{}) {
	g.sseMutex.RLock()
	defer g.sseMutex.RUnlock()

	event := Event{Type: "state_change", Data: updates}
	for _, clients := range g.sseClients {
		for _, channel := range clients {
			select {
			case channel <- event:
			default:
			}
		}
	}
}

func (g *GatewayService) handleBrowserTasks(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	limit := 50
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		if value, err := strconv.Atoi(raw); err == nil && value > 0 {
			limit = value
		}
	}
	tasks := globalBrowserTaskManager.List(limit)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"tasks": tasks})
}

func (g *GatewayService) handleBrowserTaskDetail(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	if id == "" {
		http.Error(w, `{"error":"id required"}`, http.StatusBadRequest)
		return
	}
	record, ok := globalBrowserTaskManager.Get(id)
	if !ok {
		http.Error(w, `{"error":"browser task not found"}`, http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"task": record})
}

// EnsureToken materializes a gateway token when authentication is enabled.
// Only a short fingerprint is logged; the token itself stays in local config.
func (g *GatewayService) EnsureToken() {
	if g.config.Gateway.AuthEnabled && g.config.Gateway.AuthToken == "" {
		g.config.Gateway.AuthToken = security.GenerateToken(48)
		fingerprint := sha256.Sum256([]byte(g.config.Gateway.AuthToken))
		g.logger.Printf("============================================")
		g.logger.Printf("AUTH TOKEN generated (fingerprint: %x)", fingerprint[:6])
		g.logger.Printf("The full token was saved to the configured local config file; it is not printed to logs.")
		g.logger.Printf("============================================")
		if g.configPath != "" {
			go SaveConfig(g.configPath, g.config)
		}
	}
}

var embeddedWebFS = rootassets.WebFS
var embeddedPdfExtractPy = rootassets.PDFExtractScript
var embeddedUnstructuredExtractPy = rootassets.UnstructuredExtractScript

var interruptChannels sync.Map // sessionID -> interruptRegistration

type interruptRegistration struct {
	TaskID string
	Ch     chan string
}

func storeInterruptChannel(sessionID, taskID string, ch chan string) {
	if strings.TrimSpace(sessionID) == "" || ch == nil {
		return
	}
	interruptChannels.Store(sessionID, interruptRegistration{TaskID: taskID, Ch: ch})
}

func loadInterruptChannel(sessionID string) (interruptRegistration, bool) {
	raw, ok := interruptChannels.Load(sessionID)
	if !ok {
		return interruptRegistration{}, false
	}
	switch v := raw.(type) {
	case interruptRegistration:
		return v, v.Ch != nil
	case *interruptRegistration:
		if v == nil {
			return interruptRegistration{}, false
		}
		return *v, v.Ch != nil
	case chan string:
		return interruptRegistration{Ch: v}, true
	default:
		return interruptRegistration{}, false
	}
}

func deleteInterruptChannel(sessionID, taskID string) {
	if strings.TrimSpace(sessionID) == "" {
		return
	}
	reg, ok := loadInterruptChannel(sessionID)
	if !ok {
		return
	}
	if taskID != "" && reg.TaskID != "" && reg.TaskID != taskID {
		return
	}
	interruptChannels.Delete(sessionID)
}

func trySendTaskProgress(ch chan map[string]interface{}, event map[string]interface{}) bool {
	if ch == nil {
		return false
	}
	select {
	case ch <- event:
		return true
	default:
		return false
	}
}

type MessageRequest struct {
	TaskID            string                                `json:"task_id,omitempty"`
	TaskType          string                                `json:"task_type"` // chat (Agent), qa (no tools), task, command
	SessionId         string                                `json:"session_id"`
	TenantID          string                                `json:"tenant_id,omitempty"`
	SubjectID         string                                `json:"subject_id,omitempty"`
	RoleID            string                                `json:"role_id,omitempty"`
	Message           string                                `json:"message"` // 用户消息
	Attachments       []attachmentcore.File                 `json:"attachments,omitempty"`
	AgentName         string                                `json:"agent_name,omitempty"`         // 指定Agent
	ModelConfig       string                                `json:"model_config,omitempty"`       // 本机 Web 本次任务的模型配置 ID
	CollaborationType string                                `json:"collaboration_type,omitempty"` // 协作类型
	Collaboration     *CollaborationConfig                  `json:"collaboration,omitempty"`
	SkillRun          *SkillRunRequest                      `json:"skill_run,omitempty"`
	Parameters        interface{}                           `gorm:"type:json" json:"parameters"`
	InitialMessages   []gatewayhttp.AgentOSInitialMessage   `json:"initial_messages,omitempty"`
	AssistantIdentity *gatewayhttp.AgentOSAssistantIdentity `json:"assistant_identity,omitempty"`
	Stream            bool                                  `json:"stream,omitempty"` // 是否流式响应
	Channel           string                                `json:"channel,omitempty"`
	UserId            string                                `gorm:"index;size:128" json:"user_id"` // 用户标识
	CreatedAt         time.Time                             `json:"created_at"`
	ResolvedWorkspace string                                `json:"-"`
	RequestID         string                                `json:"request_id,omitempty"`
	Traceparent       string                                `json:"traceparent,omitempty"`
}

// GatewayResponse Gateway响应
type GatewayResponse struct {
	TaskID    string                               `json:"task_id"`
	Status    string                               `json:"status"` // pending, running, completed, failed
	Result    interface{}                          `json:"result,omitempty"`
	Error     string                               `json:"error,omitempty"`
	Recovery  *sessioncore.InterruptedTaskRecovery `json:"recovery,omitempty"`
	SkillRun  *SkillRunReceipt                     `json:"skill_run,omitempty"`
	Timestamp time.Time                            `json:"timestamp"`
}

type GatewayService struct {
	config *Config
	engine *MultiAgentEngine

	skillInstallDir     string
	remoteSkillRegistry *skilldomain.RemoteSkillRegistry

	httpServer          *http.Server
	accountHandler      *authcore.AccountHandler
	agentOSHTTPServer   *http.Server
	backgroundMu        sync.Mutex
	backgroundCancel    context.CancelFunc
	backgroundWG        sync.WaitGroup
	agentOSBoundary     gatewayhttp.AgentOSLegacyBoundary
	tasks               map[string]*GatewayTask
	logger              *log.Logger
	db                  *gorm.DB
	promptSourceViewsMu sync.Mutex
	promptSourceViews   map[string]personalPromptSourceView
	configpath          string
	project             projectctx.ProjectContext
	port                int
	channelManager      *channelhost.Manager

	todoManager *todocore.Manager

	sseClients map[string]map[string]chan Event // task_id -> client_id -> chan
	sseMutex   sync.RWMutex
	// agentOSEvents retains a small reconnect window only. Durable task state
	// remains authoritative across process restarts and multiple Pods.
	agentOSEvents *eventstream.Journal

	agentOSSSEConnectionsMu sync.Mutex
	agentOSSSEConnections   map[string]int
	mutex                   sync.RWMutex  // ✅ 添加互斥锁
	configWriteMu           sync.Mutex    // serializes settings/team read-modify-save-reload
	eventBus                *eventbus.Bus // 事件总线 (Arbor)
	tasksMu                 sync.RWMutex  // protects g.tasks map

	taskStreams         map[string]chan map[string]interface{} // taskID -> progress channel for POST streaming
	taskStreamsMu       sync.RWMutex
	taskProgressLogs    map[string][]string
	taskProgressLogsMu  sync.RWMutex
	taskIDMu            sync.Mutex
	sessionTaskCounters map[string]int64

	wakeService      *voicecore.WakeService // Siri-like voice wake
	updateChecker    *operationstools.UpdateChecker
	lastUpdateInfo   *operationstools.UpdateInfo
	updateInfoMu     sync.RWMutex
	updateProgress   map[string]*operationstools.UpdateProgress
	updateProgMu     sync.Mutex
	pipelineExec     *PipelineExecutor
	mcpManager       *mcpcore.MCPManager
	aeManager        *AIEmployeeManager
	codexInteractive *CodexInteractiveManager
	supervisorRouter *aiemployee.SupervisorRouter
	secHooks         *security.SecurityHookChain
	tenantMgr        *tenantcore.TenantManager
	ssoManager       *authcore.SSOManager
	configPath       string
	evolver          *pipelinecore.PipelineEvolver
	licenseCache     map[string]*security.License
	agentOSQueueMu   sync.Mutex
	agentOSQueue     *agentOSQueueRuntime
}

type Event struct {
	ID     string      `json:"id,omitempty"`
	Type   string      `json:"type"`
	Data   interface{} `json:"data"`
	TaskID string      `json:"task_id,omitempty"`
}

func (g *GatewayService) tenantIsLicensed(tenantID string) bool {
	if g.licenseCache == nil {
		g.licenseCache = make(map[string]*security.License)
	}
	if _, ok := g.licenseCache[tenantID]; ok && g.licenseCache[tenantID] == nil {
		return false
	}

	lic := security.CheckLicense(tenantID, "licenses")
	if lic == nil {
		g.licenseCache[tenantID] = nil
		return false
	}
	g.licenseCache[tenantID] = lic

	if lic.MaxUsers > 0 && g.db != nil {
		var userCount int64
		g.db.Model(&ChatMessage{}).
			Where("user_id != ''").
			Distinct("user_id").
			Count(&userCount)
		if userCount > int64(lic.MaxUsers) {
			log.Printf("[License] tenant=%s user_count=%d exceeds max_users=%d",
				tenantID, userCount, lic.MaxUsers)
			return false
		}
	}
	return true
}

func (g *GatewayService) initSSE() {
	g.sseClients = make(map[string]map[string]chan Event)
	g.agentOSEvents = eventstream.NewJournal(64, 256)
	g.taskStreams = make(map[string]chan map[string]interface{})
	g.taskProgressLogs = make(map[string][]string)
	g.sessionTaskCounters = make(map[string]int64)
	g.updateProgress = make(map[string]*operationstools.UpdateProgress)
}

func (g *GatewayService) recordAgentOSEvent(event Event) Event {
	if g == nil || g.agentOSEvents == nil || strings.TrimSpace(event.TaskID) == "" {
		return event
	}
	data, err := json.Marshal(event.Data)
	if err != nil {
		return event
	}
	record := g.agentOSEvents.Append(event.TaskID, event.Type, data)
	event.ID = record.ID
	return event
}

func (g *GatewayService) handleSSE(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	clientID := r.URL.Query().Get("client_id")
	if clientID == "" {
		clientID = fmt.Sprintf("client_%d", time.Now().UnixNano())
	}
	taskID := strings.TrimSpace(r.URL.Query().Get("task_id")) // 按任务订阅，空则全局
	if gatewayhttp.IsReservedAgentOSSSEKey(taskID) || taskID != "" && g.isAgentOSTaskID(taskID) {
		http.NotFound(w, r)
		return
	}

	// 创建客户端通道
	eventChan := make(chan Event, 10)
	g.sseMutex.Lock()
	key := taskID
	if key == "" {
		key = "*" // 全局频道
	}
	if g.sseClients[key] == nil {
		g.sseClients[key] = make(map[string]chan Event)
	}
	g.sseClients[key][clientID] = eventChan
	g.sseMutex.Unlock()

	// 清理函数
	defer func() {
		g.sseMutex.Lock()
		if clients, ok := g.sseClients[key]; ok {
			delete(clients, clientID)
			if len(clients) == 0 {
				delete(g.sseClients, key)
			}
		}
		g.sseMutex.Unlock()
		close(eventChan)
	}()

	// 发送初始连接成功消息
	fmt.Fprintf(w, "event: connected\ndata: {\"client_id\":\"%s\"}\n\n", clientID)
	w.(http.Flusher).Flush()

	// 监听事件
	for {
		select {
		case event := <-eventChan:
			data, _ := json.Marshal(event.Data)
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event.Type, string(data))
			w.(http.Flusher).Flush()
		case <-r.Context().Done():
			return
		}
	}
}

// 广播任务状态更新
func (g *GatewayService) broadcastTaskUpdate(taskID string, status string, result interface{}, err error) {
	agentOSTask := g.isAgentOSTaskID(taskID)
	if agentOSTask {
		result = agentos.PublicResponseValue(result)
	}
	data := map[string]interface{}{
		"task_id":   taskID,
		"status":    status,
		"result":    result,
		"error":     nil,
		"timestamp": time.Now(),
	}
	// 附带耗时信息
	g.tasksMu.RLock()
	t, ok := g.tasks[taskID]
	var sessionID string
	var createdAt time.Time
	var completedAt *time.Time
	if ok {
		if t.Request != nil {
			sessionID = t.Request.SessionId
		}
		if t.CompletedAt != nil {
			completedCopy := *t.CompletedAt
			completedAt = &completedCopy
		}
		createdAt = t.CreatedAt
	}
	g.tasksMu.RUnlock()
	if ok {
		if sessionID != "" {
			data["session_id"] = sessionID
		}
		if completedAt != nil {
			data["duration_sec"] = completedAt.Sub(createdAt).Seconds()
		} else {
			data["duration_sec"] = time.Since(createdAt).Seconds()
		}
	}
	event := Event{
		Type:   "task_update",
		TaskID: taskID,
		Data:   data,
	}
	if err != nil {
		errorMessage := err.Error()
		if agentOSTask {
		}
		event.Data.(map[string]interface{})["error"] = errorMessage
	}
	if agentOSTask {
		event = g.recordAgentOSEvent(event)
	}

	g.sseMutex.RLock()
	defer g.sseMutex.RUnlock()

	// AgentOS task events use a separate namespace and never reach the legacy
	// global Web stream, which has no tenant/service identity.
	for _, key := range g.sseTargetsForTask(taskID) {
		if taskClients, ok := g.sseClients[key]; ok {
			for _, ch := range taskClients {
				select {
				case ch <- event:
				default:
				}
			}
		}
	}
	if isTerminalGatewayTaskStatus(status) {
		g.unmarkAgentOSTaskID(taskID)
	}
}

// broadcastSessionListChanged 通知所有客户端刷新会话列表
func (g *GatewayService) broadcastSessionListChanged() {
	g.sseMutex.RLock()
	defer g.sseMutex.RUnlock()
	if clients, ok := g.sseClients["*"]; ok {
		for _, ch := range clients {
			select {
			case ch <- Event{Type: "session_list_changed", Data: map[string]interface{}{}}:
			default:
			}
		}
	}
}

// BroadcastProgress sends a real-time progress event to all SSE clients
func (g *GatewayService) BroadcastProgress(evt ProgressEvent) {
	displayEvt := evt
	displayEvt.StepName = publicProgressStepName(displayEvt.StepName)
	if displayEvt.StepName == "" && strings.HasPrefix(strings.ToLower(strings.TrimSpace(displayEvt.AgentName)), "pipe_") {
		displayEvt.StepName = g.currentAgentOSBusinessStep(displayEvt.TaskID)
	}
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(displayEvt.AgentName)), "pipe_") {
		// Pipeline implementation names are internal routing identifiers. The
		// public step name above carries the useful business context.
		displayEvt.AgentName = ""
	}
	if displayEvt.StepIndex < 0 || displayEvt.StepTotal < 0 ||
		(displayEvt.StepTotal > 0 && displayEvt.StepIndex > displayEvt.StepTotal) {
		displayEvt.StepIndex = 0
		displayEvt.StepTotal = 0
	}
	progressLogLine := sanitizeProgressLogMessage(displayEvt)
	if progressLogLine != "" {
		displayEvt.Message = progressLogLine
	} else if strings.TrimSpace(evt.Message) != "" {
		displayEvt.Message = ""
	}
	sseEvent := Event{
		Type:   "progress",
		TaskID: displayEvt.TaskID,
		Data:   displayEvt,
	}
	if g.isAgentOSTaskID(displayEvt.TaskID) {
		sseEvent = g.recordAgentOSEvent(sseEvent)
	}
	if evt.Type == "heartbeat" && evt.TaskID != "" {
		g.touchRunningTaskHeartbeat(evt.TaskID)
	}

	g.sseMutex.RLock()
	defer g.sseMutex.RUnlock()

	// AgentOS progress is visible only through its authenticated v1 task SSE.
	for _, clients := range g.sseTargetsForTask(displayEvt.TaskID) {
		if taskClients, ok := g.sseClients[clients]; ok {
			for _, ch := range taskClients {
				select {
				case ch <- sseEvent:
				default:
				}
			}
		}
	}

	// Also push to task-specific streaming channel (for POST stream:true clients)
	g.taskStreamsMu.RLock()
	if taskCh, ok := g.taskStreams[displayEvt.TaskID]; ok {
		g.taskStreamsMu.RUnlock()
		select {
		case taskCh <- map[string]interface{}{
			"type":       displayEvt.Type,
			"message":    displayEvt.Message,
			"tool_name":  displayEvt.ToolName,
			"agent_name": displayEvt.AgentName,
			"status":     displayEvt.Status,
		}:
		default:
		}
	} else {
		g.taskStreamsMu.RUnlock()
	}
	// Heartbeats keep liveness fresh but are intentionally transient. Persisting
	// every "still working" tick creates noisy, repetitive user timelines.
	if evt.TaskID != "" && progressLogLine != "" && evt.Type != "heartbeat" {
		g.taskProgressLogsMu.Lock()
		if g.taskProgressLogs == nil {
			g.taskProgressLogs = make(map[string][]string)
		}
		g.taskProgressLogs[evt.TaskID] = append(g.taskProgressLogs[evt.TaskID], progressLogLine)
		g.taskProgressLogsMu.Unlock()
		g.appendTaskProgressLogLine(evt.TaskID, progressLogLine)
	}
}

const agentOSBusinessStepPhasePrefix = "business_step:"

func publicProgressStepName(name string) string {
	cleaned := strings.TrimSpace(textutil.Clean(middlewarecore.StripToolCalls(name)))
	cleaned = strings.Join(strings.Fields(cleaned), " ")
	if cleaned == "" {
		return ""
	}
	lower := strings.ToLower(cleaned)
	for _, marker := range []string{
		".openkitty", "skill.md", "use_skill", "code_check", "tool:",
		"runtime/", `runtime\`, "scripts/", `scripts\`,
	} {
		if strings.Contains(lower, marker) {
			return ""
		}
	}
	if len(sandboxTextPaths(cleaned)) > 0 {
		return ""
	}
	return textutil.TruncateUTF8Bytes(cleaned, 96)
}

func agentOSBusinessStepPhase(stepName string) string {
	stepName = publicProgressStepName(stepName)
	if stepName == "" {
		return ""
	}
	return agentOSBusinessStepPhasePrefix + textutil.TruncateUTF8Bytes(stepName, 48)
}

func agentOSBusinessStepFromPhase(phase string) string {
	phase = strings.TrimSpace(phase)
	if !strings.HasPrefix(phase, agentOSBusinessStepPhasePrefix) {
		return ""
	}
	return publicProgressStepName(strings.TrimPrefix(phase, agentOSBusinessStepPhasePrefix))
}

func (g *GatewayService) currentAgentOSBusinessStep(taskID string) string {
	if g == nil || strings.TrimSpace(taskID) == "" {
		return ""
	}
	g.tasksMu.RLock()
	task := g.tasks[taskID]
	phase := ""
	if task != nil {
		phase = task.CurrentPhase
	}
	g.tasksMu.RUnlock()
	return agentOSBusinessStepFromPhase(phase)
}

func (g *GatewayService) touchRunningTaskHeartbeat(taskID string) {
	g.ObserveTaskRuntime(taskID, "", "")
}

// ObserveTaskRuntime is the host-owned liveness boundary used by model/tool
// execution. It updates memory synchronously and durable state asynchronously
// so task execution never waits on observability I/O.
func (g *GatewayService) ObserveTaskRuntime(taskID, phase, toolCallID string) {
	if g == nil || strings.TrimSpace(taskID) == "" {
		return
	}
	now := time.Now()
	var leaseToken string
	var leaseUntil *time.Time
	g.tasksMu.Lock()
	if task := g.tasks[taskID]; task != nil && task.Status == "running" {
		task.HeartbeatAt = &now
		if task.QueueName == agentOSQueueName && strings.TrimSpace(task.LeaseToken) != "" {
			leaseToken = task.LeaseToken
			renewed := now.Add(agentOSTaskLeaseDuration(g))
			task.LeaseUntil = &renewed
			leaseUntil = &renewed
		}
		if strings.TrimSpace(phase) != "" {
			task.CurrentPhase = strings.TrimSpace(phase)
		}
		if strings.TrimSpace(toolCallID) != "" {
			task.LastToolCallID = strings.TrimSpace(toolCallID)
		}
	}
	g.tasksMu.Unlock()
	if g.db == nil {
		return
	}
	db := g.db
	go func() {
		// Liveness bookkeeping must never delay SSE or the task itself.
		updates := map[string]interface{}{"heartbeat_at": now, "updated_at": now}
		if strings.TrimSpace(phase) != "" {
			updates["current_phase"] = strings.TrimSpace(phase)
		}
		if strings.TrimSpace(toolCallID) != "" {
			updates["last_tool_call_id"] = strings.TrimSpace(toolCallID)
		}
		query := db.Model(&GatewayTaskRecord{}).Where("task_id = ? AND status = ?", taskID, "running")
		if leaseToken != "" && leaseUntil != nil {
			query = query.Where("lease_token = ?", leaseToken)
			updates["lease_until"] = *leaseUntil
		}
		_ = query.
			Updates(updates).Error
	}()
}

func sanitizeProgressLogMessage(evt ProgressEvent) string {
	msg := strings.TrimSpace(evt.Message)
	if msg == "" {
		return ""
	}
	cleaned := strings.TrimSpace(textutil.Clean(middlewarecore.StripToolCalls(msg)))
	if cleaned == "" {
		return ""
	}
	lower := strings.ToLower(cleaned)
	if strings.HasPrefix(lower, "llm:") {
		if evt.Type == "llm_call" {
			return "LLM call complete"
		}
		return ""
	}
	internalMarkers := []string{
		"cold data:",
		"\"messages\"",
		"请求体",
		"[debug]",
		"systemprompt",
		"prompt:",
		"<think",
		"</think",
	}
	for _, marker := range internalMarkers {
		if strings.Contains(lower, marker) {
			return ""
		}
	}
	return cleaned
}

func (g *GatewayService) appendTaskProgressLogLine(taskID, line string) {
	if !isSafeTaskLogID(taskID) || strings.TrimSpace(line) == "" {
		return
	}
	dir := filepath.Join(g.progressLogDir(), "progress")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return
	}
	path := filepath.Join(dir, taskID+".log")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.WriteString(line + "\n")
}

// BroadcastSSEEvent sends a typed SSE event using the new event system (Claude Code pattern).
// Complements the legacy BroadcastProgress with structured event types.
func (g *GatewayService) BroadcastSSEEvent(evt eventbus.SSEEvent) {
	sseEvent := Event{
		Type:   string(evt.Type),
		TaskID: evt.TaskID,
		Data:   evt,
	}
	if g.isAgentOSTaskID(evt.TaskID) {
		sseEvent = g.recordAgentOSEvent(sseEvent)
	}

	g.sseMutex.RLock()
	defer g.sseMutex.RUnlock()

	for _, key := range g.sseTargetsForTask(evt.TaskID) {
		if taskClients, ok := g.sseClients[key]; ok {
			for _, ch := range taskClients {
				select {
				case ch <- sseEvent:
				default:
				}
			}
		}
	}
}

func (g *GatewayService) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	if g.rejectLegacyAgentOSSession(w, r, r.URL.Query().Get("session_id")) {
		return
	}
	conn, err := wsUpgrader.Upgrade(w, r, nil)
	if err != nil {
		g.logger.Printf("WebSocket 升级失败: %v", err)
		return
	}
	defer conn.Close()

	// 注册到全局广播列表
	clientID := fmt.Sprintf("ws_%d", time.Now().UnixNano())
	eventChan := make(chan Event, 20)
	g.sseMutex.Lock()
	if g.sseClients["*"] == nil {
		g.sseClients["*"] = make(map[string]chan Event)
	}
	g.sseClients["*"][clientID] = eventChan
	g.sseMutex.Unlock()

	defer func() {
		g.sseMutex.Lock()
		if clients, ok := g.sseClients["*"]; ok {
			delete(clients, clientID)
		}
		g.sseMutex.Unlock()
		close(eventChan)
	}()

	// 发送连接确认
	conn.WriteJSON(map[string]interface{}{
		"type":      "connected",
		"client_id": clientID,
		"message":   "WebSocket 已连接",
	})

	// 启动广播转发协程
	go func() {
		for evt := range eventChan {
			if err := conn.WriteJSON(evt); err != nil {
				return
			}
		}
	}()

	// 读取客户端消息
	for {
		var msg map[string]interface{}
		if err := conn.ReadJSON(&msg); err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
				g.logger.Printf("WebSocket 异常关闭: %v", err)
			}
			break
		}

		msgType, _ := msg["type"].(string)
		switch msgType {
		case "chat":
			body, _ := msg["message"].(string)
			agentName, _ := msg["agent_name"].(string)
			sessionID, _ := msg["session_id"].(string)
			userID, _ := msg["user_id"].(string)
			channel, _ := msg["channel"].(string)
			if channel == "" {
				channel = "ws"
			}

			if body == "" {
				conn.WriteJSON(map[string]interface{}{
					"type":  "error",
					"error": "message 不能为空",
				})
				continue
			}
			g.assessAndLogGatewayUserContent("websocket", body)

			// 构建请求（复用 /api/chat 的逻辑）
			req := &MessageRequest{
				TaskType:  "chat",
				SessionId: sessionID,
				Message:   body,
				AgentName: agentName,
				Channel:   channel,
				UserId:    userID,
				Stream:    true,
			}
			matchedSkill := false
			if g.aeManager == nil {
				if matched, bindErr := g.bindSkillMentionRequest(req); bindErr != nil {
					_ = conn.WriteJSON(map[string]interface{}{"type": "error", "error": bindErr.Error()})
					continue
				} else {
					matchedSkill = matched
				}
			} else if employee, _ := g.aeManager.MatchAtName(body); employee == nil {
				if matched, bindErr := g.bindSkillMentionRequest(req); bindErr != nil {
					_ = conn.WriteJSON(map[string]interface{}{"type": "error", "error": bindErr.Error()})
					continue
				} else {
					matchedSkill = matched
				}
			}
			if matchedSkill {
				req.Message = skilldomain.PrependMentionToTask(body, req.Message)
			}

			if req.SessionId == "" {
				req.SessionId = fmt.Sprintf("ws_%d", time.Now().UnixNano())
			}
			if g.isAgentOSSessionID(req.SessionId) {
				_ = conn.WriteJSON(map[string]interface{}{
					"type":  "error",
					"code":  "session_not_found",
					"error": "session not found",
				})
				continue
			}
			resolvedWorkspace := ""
			if req.SkillRun != nil {
				if g.db == nil {
					_ = conn.WriteJSON(map[string]interface{}{"type": "error", "error": "Skill Run requires a durable database"})
					continue
				}
				var workspaceErr error
				resolvedWorkspace, workspaceErr = g.bindSessionWorkspace(req.SessionId, req.AgentName, req.UserId, true)
				if workspaceErr != nil {
					_ = conn.WriteJSON(map[string]interface{}{"type": "error", "error": workspaceErr.Error()})
					continue
				}
				req.ResolvedWorkspace = resolvedWorkspace
			}

			taskID := fmt.Sprintf("ws_%d", time.Now().UnixNano())
			req.TaskID = taskID

			// 创建任务并异步执行
			task := &GatewayTask{
				ID:                taskID,
				Request:           req,
				Status:            "pending",
				CreatedAt:         time.Now(),
				ResolvedWorkspace: resolvedWorkspace,
				ResultCh:          make(chan interface{}, 1),
			}
			if req.SkillRun != nil {
				if persistErr := SaveGatewayTaskToDB(g.db, task); persistErr != nil {
					_ = conn.WriteJSON(map[string]interface{}{"type": "error", "error": "persist Skill task: " + persistErr.Error()})
					continue
				}
			}

			// 保存用户消息
			g.saveChatMessage(req.SessionId, "user", req.AgentName, body, "pending", taskID, req.Channel, req.UserId)

			g.tasksMu.Lock()
			g.tasks[taskID] = task
			g.tasksMu.Unlock()

			// 通知任务已创建
			conn.WriteJSON(map[string]interface{}{
				"type":    "task_created",
				"task_id": taskID,
			})

			// 异步执行任务
			go func() {
				g.processTask(task)
				// 任务完成后发送结果
				g.tasksMu.RLock()
				t := g.tasks[taskID]
				g.tasksMu.RUnlock()
				if t != nil {
					result := map[string]interface{}{
						"type":    "task_update",
						"task_id": taskID,
						"data": map[string]interface{}{
							"status": string(t.Status),
							"result": t.Result,
							"error":  t.Error,
						},
					}
					eventChan <- Event{
						Type:   "task_update",
						TaskID: taskID,
						Data: map[string]interface{}{
							"status": string(t.Status),
							"result": t.Result,
							"error":  t.Error,
						},
					}
					_ = result
				}
			}()

		case "ping":
			conn.WriteJSON(map[string]interface{}{"type": "pong"})

		default:
			conn.WriteJSON(map[string]interface{}{
				"type":  "error",
				"error": fmt.Sprintf("未知消息类型: %s", msgType),
			})
		}
	}
}

// qqWebhookPort  int    `json:"qq_webhook_port" yaml:"qq_webhook_port"` // QQ Webhook 专用端口
// qqWebhookPath  string `json:"qq_webhook_path" yaml:"qq_webhook_path"`

type GatewayTask struct {
	memoryReview         *memoryReviewCapture
	platformAccount      *authcore.PlatformAccountSession
	webModelSelection    *webModelSelection
	webMemoryAudience    *webMemoryAudience
	ID                   string
	Request              *MessageRequest
	TenantID             string
	SubjectID            string
	TenantScoped         bool
	RoleID               string
	RoleRevision         string
	RoleSnapshot         string
	Status               string
	Result               interface{}
	Error                error
	CreatedAt            time.Time
	CompletedAt          *time.Time
	HeartbeatAt          *time.Time
	CurrentPhase         string
	RunAttempt           int
	LastToolCallID       string
	CallerID             string
	ResolvedWorkspace    string
	ResumedFromTaskID    string
	ResumeCheckpoint     *sessioncore.TurnCheckpoint
	ResumeSupplement     string
	RecoveryState        string
	RecoveryJournal      string
	RecoveryPreview      string
	RecoveryEntryCount   int
	RecoveryLastActivity *time.Time
	ResumedByTaskID      string
	QueueName            string
	QueueOwner           string
	LeaseToken           string
	LeaseUntil           *time.Time
	AvailableAt          *time.Time
	ClaimedAt            *time.Time
	ResultCh             chan interface{}
	ProgressCh           chan map[string]interface{} // task-specific streaming progress
	InterruptCh          chan string                 // user messages to inject mid-execution
	FollowupCh           chan *agentos.TaskFollowupRecord
	Cancel               context.CancelFunc // cancel the task's context
}

type gatewayTaskInterruption struct {
	Message  string
	Followup *agentos.TaskFollowupRecord
}

func cloneGatewayTask(task *GatewayTask) *GatewayTask {
	if task == nil {
		return nil
	}
	copyTask := *task
	if task.Request != nil {
		copyRequest := *task.Request
		copyRequest.SkillRun = cloneSkillRunRequest(task.Request.SkillRun)
		copyRequest.InitialMessages = append([]gatewayhttp.AgentOSInitialMessage(nil), task.Request.InitialMessages...)
		if task.Request.AssistantIdentity != nil {
			copyIdentity := *task.Request.AssistantIdentity
			copyRequest.AssistantIdentity = &copyIdentity
		}
		copyTask.Request = &copyRequest
	}
	copyTask.ResumeCheckpoint = sessioncore.CloneTurnCheckpoint(task.ResumeCheckpoint)
	if task.CompletedAt != nil {
		completedAt := *task.CompletedAt
		copyTask.CompletedAt = &completedAt
	}
	if task.HeartbeatAt != nil {
		heartbeatAt := *task.HeartbeatAt
		copyTask.HeartbeatAt = &heartbeatAt
	}
	if task.LeaseUntil != nil {
		leaseUntil := *task.LeaseUntil
		copyTask.LeaseUntil = &leaseUntil
	}
	if task.AvailableAt != nil {
		availableAt := *task.AvailableAt
		copyTask.AvailableAt = &availableAt
	}
	if task.ClaimedAt != nil {
		claimedAt := *task.ClaimedAt
		copyTask.ClaimedAt = &claimedAt
	}
	return &copyTask
}

func isTerminalGatewayTaskStatus(status string) bool {
	switch status {
	case "completed", "degraded", "failed", "stopped":
		return true
	default:
		return false
	}
}

func (g *GatewayService) gatewayTaskSnapshot(taskID string) (*GatewayTask, bool) {
	if g == nil {
		return nil, false
	}
	g.tasksMu.RLock()
	task, exists := g.tasks[taskID]
	snapshot := cloneGatewayTask(task)
	g.tasksMu.RUnlock()
	return snapshot, exists
}

// gatewayTaskSnapshotWithDB keeps polling/detail endpoints coherent after a
// process restart. Live in-memory state always wins; durable terminal history
// is used only when the current Gateway instance does not own the task.
func (g *GatewayService) gatewayTaskSnapshotWithDB(taskID string) (*GatewayTask, bool) {
	if snapshot, ok := g.gatewayTaskSnapshot(taskID); ok {
		// Queue tasks can be canceled or reclaimed by another instance. Prefer a
		// durable terminal state or a different lease token over stale local state.
		if snapshot.QueueName == agentOSQueueName && g != nil && g.db != nil {
			var current GatewayTaskRecord
			if err := g.db.Where("task_id = ?", taskID).First(&current).Error; err == nil {
				if isTerminalGatewayTaskStatus(current.Status) ||
					current.Status == "waiting_approval" ||
					(current.LeaseToken != "" && current.LeaseToken != snapshot.LeaseToken) {
					return gatewayTaskFromRecord(current), true
				}
			}
		}
		return snapshot, true
	}
	if g == nil || g.db == nil || strings.TrimSpace(taskID) == "" {
		return nil, false
	}
	var record GatewayTaskRecord
	if err := g.db.Where("task_id = ?", taskID).First(&record).Error; err != nil {
		return nil, false
	}
	return gatewayTaskFromRecord(record), true
}

func gatewayTaskFromRecord(record GatewayTaskRecord) *GatewayTask {
	request := &MessageRequest{
		TaskID: record.TaskID, TaskType: record.TaskType, Message: record.Message,
		AgentName: record.AgentName, SessionId: record.SessionID, UserId: record.UserID,
		TenantID: record.TenantID, SubjectID: record.SubjectID, RoleID: record.RoleID,
		Channel: record.Channel, ResolvedWorkspace: record.ResolvedWorkspace,
	}
	if payload := strings.TrimSpace(record.RequestPayload); payload != "" {
		var persisted MessageRequest
		if err := json.Unmarshal([]byte(payload), &persisted); err == nil {
			request = &persisted
			// Durable columns are the canonical routing and ownership
			// contract. RequestPayload carries extensible input only and must
			// never override them after a restart.
			request.TaskID = record.TaskID
			request.TaskType = record.TaskType
			request.Message = record.Message
			request.AgentName = record.AgentName
			request.SessionId = record.SessionID
			request.UserId = record.UserID
			request.TenantID = record.TenantID
			request.SubjectID = record.SubjectID
			request.RoleID = record.RoleID
			request.Channel = record.Channel
			request.ResolvedWorkspace = record.ResolvedWorkspace
		}
	}
	task := &GatewayTask{
		ID: record.TaskID, Request: request, Status: record.Status,
		TenantID: record.TenantID, SubjectID: record.SubjectID,
		TenantScoped: record.TenantScoped || record.QueueName == agentOSQueueName,
		RoleID:       record.RoleID,
		RoleRevision: record.RoleRevision, RoleSnapshot: record.RoleSnapshot,
		CreatedAt: record.CreatedAt, CompletedAt: record.CompletedAt,
		HeartbeatAt: record.HeartbeatAt, CurrentPhase: record.CurrentPhase,
		RunAttempt: record.RunAttempt, LastToolCallID: record.LastToolCallID,
		CallerID: record.CallerID, ResolvedWorkspace: record.ResolvedWorkspace,
		ResumedFromTaskID: record.ResumedFromTaskID, RecoveryState: record.RecoveryState,
		RecoveryJournal: record.RecoveryJournal, RecoveryPreview: record.RecoveryPreview,
		RecoveryEntryCount: record.RecoveryEntryCount, RecoveryLastActivity: record.RecoveryLastActivity,
		ResumedByTaskID: record.ResumedByTaskID, QueueName: record.QueueName,
		QueueOwner: record.QueueOwner, LeaseToken: record.LeaseToken,
		LeaseUntil: record.LeaseUntil, AvailableAt: record.AvailableAt, ClaimedAt: record.ClaimedAt,
	}
	if raw := strings.TrimSpace(record.Result); raw != "" {
		var decoded interface{}
		if err := json.Unmarshal([]byte(raw), &decoded); err == nil {
			task.Result = decoded
		} else {
			task.Result = record.Result
		}
	}
	if message := strings.TrimSpace(record.ErrorMsg); message != "" {
		task.Error = fmt.Errorf("%s", message)
	}
	return task
}

func (g *GatewayService) markGatewayTaskRunning(task *GatewayTask, cancel context.CancelFunc) (*GatewayTask, bool) {
	if g == nil || task == nil {
		return nil, false
	}
	g.tasksMu.Lock()
	if isTerminalGatewayTaskStatus(task.Status) || task.Status == "paused" || task.Status == "pausing" {
		snapshot := cloneGatewayTask(task)
		g.tasksMu.Unlock()
		return snapshot, false
	}
	task.Status = "running"
	task.Cancel = cancel
	now := time.Now()
	task.HeartbeatAt = &now
	task.CurrentPhase = "starting"
	if task.QueueName != agentOSQueueName {
		task.RunAttempt++
	}
	snapshot := cloneGatewayTask(task)
	g.tasksMu.Unlock()
	return snapshot, true
}

func (g *GatewayService) finishGatewayTask(task *GatewayTask, status string, result interface{}, err error) (*GatewayTask, bool) {
	if g == nil || task == nil {
		return nil, false
	}
	if status == "completed" && err == nil && task.Request != nil && task.Request.SkillRun != nil &&
		strings.EqualFold(strings.TrimSpace(task.Request.SkillRun.SkillName), "tcm-expert") {
		if guarded, replaced := security.GuardTCMExpertCompletion(task.Request.Message, gatewayResultText(result)); replaced {
			result = guarded
			if g.logger != nil {
				g.logger.Printf("[tcm-expert] safety completion replaced for task %s", task.ID)
			}
		}
	}
	g.tasksMu.Lock()
	if task.QueueName == agentOSQueueName {
		current := g.tasks[task.ID]
		if current == nil ||
			current != task ||
			strings.TrimSpace(task.LeaseToken) == "" ||
			current.LeaseToken != task.LeaseToken {
			snapshot := cloneGatewayTask(current)
			g.tasksMu.Unlock()
			if snapshot != nil {
				return snapshot, false
			}
			if durable, ok := g.gatewayTaskSnapshotWithDB(task.ID); ok {
				return durable, false
			}
			return nil, false
		}
	}
	if isTerminalGatewayTaskStatus(task.Status) {
		snapshot := cloneGatewayTask(task)
		g.tasksMu.Unlock()
		return snapshot, false
	}
	terminal := cloneGatewayTask(task)
	terminal.Status = status
	terminal.Result = result
	terminal.Error = err
	completedAt := time.Now()
	terminal.CompletedAt = &completedAt
	terminal.HeartbeatAt = &completedAt
	terminal.CurrentPhase = status
	// Keep the task lock until the terminal snapshot is durable. Otherwise a
	// status caller can observe "completed" and immediately restart the service
	// while the database still says "running". Holding this short critical
	// section also preserves the existing completion-vs-cancel winner semantics.
	if g.db != nil {
		// A task terminal state and its Skill Run receipt are one observable
		// transition. Persisting both in one transaction prevents recovery of a
		// completed task with a still-running Skill checkpoint.
		followupRequeued := false
		saveErr := g.db.Transaction(func(tx *gorm.DB) error {
			if task.QueueName == agentOSQueueName {
				if txErr := agentos.EnsureTaskFollowupState(tx, task.ID, true, completedAt); txErr != nil {
					return txErr
				}
				decision, txErr := agentos.TryCloseTaskFollowups(tx, task.ID, completedAt)
				if txErr != nil {
					return txErr
				}
				if decision == agentos.TaskFollowupClosePending {
					requeued := tx.Model(&GatewayTaskRecord{}).
						Where(
							"task_id = ? AND queue_name = ? AND status = ? AND lease_token = ?",
							task.ID, agentOSQueueName, "running", task.LeaseToken,
						).
						Updates(map[string]interface{}{
							"status": "pending", "current_phase": "followup_queued",
							"queue_owner": "", "lease_token": "", "lease_until": nil,
							"available_at": completedAt, "claimed_at": nil,
							"completed_at": nil, "error_msg": "", "result": "",
							"heartbeat_at": completedAt, "updated_at": completedAt,
						})
					if requeued.Error != nil {
						return requeued.Error
					}
					if requeued.RowsAffected != 1 {
						return agentos.ErrLeaseLost
					}
					followupRequeued = true
					return nil
				}
				if txErr := persistAgentOSAssistantCompletionWithDB(tx, terminal); txErr != nil {
					return txErr
				}
			}
			if txErr := SaveGatewayTaskToDB(tx, terminal); txErr != nil {
				return txErr
			}
			if txErr := finishSkillRunWithDB(tx, terminal, terminal.Result, terminal.Error); txErr != nil {
				return txErr
			}
			return task.memoryReview.enqueue(tx, terminal)
		})
		if saveErr == nil && followupRequeued {
			requeued := cloneGatewayTask(task)
			requeued.Status = "pending"
			requeued.Result = nil
			requeued.Error = nil
			requeued.CompletedAt = nil
			requeued.CurrentPhase = "followup_queued"
			requeued.QueueOwner = ""
			requeued.LeaseToken = ""
			requeued.LeaseUntil = nil
			requeued.AvailableAt = &completedAt
			requeued.ClaimedAt = nil
			if current := g.tasks[task.ID]; current == task {
				delete(g.tasks, task.ID)
			}
			g.tasksMu.Unlock()
			g.broadcastTaskUpdate(task.ID, "pending", nil, nil)
			g.ensureAgentOSQueue().Signal()
			return requeued, false
		}
		if saveErr != nil {
			if g.logger != nil {
				g.logger.Printf("任务 %s 终态预落库失败: %v", task.ID, saveErr)
			}
			if task.memoryReview != nil || task.QueueName == agentOSQueueName ||
				(task.Request != nil && task.Request.SkillRun != nil) {
				snapshot := cloneGatewayTask(task)
				g.tasksMu.Unlock()
				// A conditional AgentOS write can fail because another worker
				// owns the durable lease. A Skill Run can also fail while its
				// task and receipt are being committed together. Return the
				// durable state in either case so an uncommitted completion is
				// never published and later reversed by a restart.
				if durable, ok := g.gatewayTaskSnapshotWithDB(task.ID); ok {
					return durable, false
				}
				return snapshot, false
			}
		}
	}
	task.Status = terminal.Status
	task.Result = terminal.Result
	task.Error = terminal.Error
	task.CompletedAt = terminal.CompletedAt
	task.HeartbeatAt = terminal.HeartbeatAt
	task.CurrentPhase = terminal.CurrentPhase
	snapshot := cloneGatewayTask(task)
	g.tasksMu.Unlock()
	return snapshot, true
}

// stopGatewayTask owns the single atomic stopped transition used by every
// stop endpoint. The process goroutine remains the sole ResultCh producer and
// closer, avoiding a full-channel deadlock when cancellation returns.
func (g *GatewayService) stopGatewayTask(taskID string) (*GatewayTask, bool, bool) {
	if g == nil || taskID == "" {
		return nil, false, false
	}
	if g.db != nil {
		var record GatewayTaskRecord
		if err := g.db.Select("task_id", "queue_name").Where("task_id = ?", taskID).First(&record).Error; err == nil && record.QueueName == agentOSQueueName {
			return g.stopAgentOSQueueTask(taskID)
		}
	}
	g.tasksMu.Lock()
	task, exists := g.tasks[taskID]
	if !exists {
		g.tasksMu.Unlock()
		return nil, false, false
	}
	if task.Status != "running" && task.Status != "pending" && task.Status != "waiting_approval" &&
		task.Status != "pausing" && task.Status != "paused" {
		snapshot := cloneGatewayTask(task)
		g.tasksMu.Unlock()
		return snapshot, true, false
	}
	completedAt := time.Now()
	task.Status = "stopped"
	task.CompletedAt = &completedAt
	task.HeartbeatAt = &completedAt
	task.CurrentPhase = "stopped"
	task.Error = fmt.Errorf("task stopped by user")
	cancel := task.Cancel
	snapshot := cloneGatewayTask(task)
	if cancel != nil {
		cancel()
	}
	if g.db != nil {
		persistErr := g.db.Transaction(func(tx *gorm.DB) error {
			if err := SaveGatewayTaskToDB(tx, snapshot); err != nil {
				return err
			}
			return updateBoundSkillRunTerminalWithDB(
				tx, snapshot, skillRunPhaseStopped, "task stopped by user", completedAt,
			)
		})
		if persistErr != nil && g.logger != nil {
			g.logger.Printf("保存停止任务状态失败: %v", persistErr)
		}
	}
	// As with normal completion, do not expose a terminal in-memory state until
	// its durable task record has been written.
	g.tasksMu.Unlock()

	if g.db != nil {
		if err := g.db.Model(&approvalcore.ApprovalRequestRecord{}).
			Where("task_id = ? AND status IN ?", taskID, []string{approvalcore.StatusPending, approvalcore.StatusApproved}).
			Updates(map[string]interface{}{
				"status": approvalcore.StatusCancelled, "updated_at": completedAt,
			}).Error; err != nil && g.logger != nil {
			g.logger.Printf("cancel task approvals failed: %v", err)
		}
		if err := g.db.Model(&ChatMessage{}).
			Where("task_id = ? AND status IN ?", taskID, []string{"pending", "running", "waiting_approval", "pausing", "paused"}).
			Update("status", "stopped").Error; err != nil && g.logger != nil {
			g.logger.Printf("保存停止任务消息状态失败: %v", err)
		}
	}
	g.broadcastTaskUpdate(taskID, "stopped", snapshot.Result, snapshot.Error)
	return snapshot, true, true
}

func trySendGatewayTaskResult(ch chan interface{}, response GatewayResponse) {
	if ch == nil {
		return
	}
	select {
	case ch <- response:
	default:
	}
}

type MessageRequestRecord struct {
	ID        uint   `gorm:"column:id;primarykey" json:"id"`
	TaskID    string `gorm:"column:task_id;index;size:64" json:"task_id"`
	TaskType  string `gorm:"column:task_type;size:50" json:"task_type"`
	Message   string `gorm:"column:message;type:text" json:"message"`
	AgentName string `gorm:"column:agent_name;size:100" json:"agent_name"`
	Channel   string `gorm:"column:channel;size:50" json:"channel"`
	Stream    bool   `gorm:"column:stream" json:"stream"`
	// Parameters 用 JSON 类型存储
	Parameters string    `gorm:"column:parameters;type:json" json:"parameters"` // 存储为 JSON 字符串
	CreatedAt  time.Time `gorm:"column:created_at" json:"created_at"`
}

func (MessageRequestRecord) TableName() string { return "message_request_records" }

type GatewayTaskRecord struct {
	ID                   uint       `gorm:"primarykey"`
	TaskID               string     `gorm:"uniqueIndex;size:64" json:"task_id"`
	TenantID             string     `gorm:"index;size:64;default:default" json:"tenant_id"`
	SubjectID            string     `gorm:"index;size:128" json:"subject_id,omitempty"`
	TenantScoped         bool       `json:"tenant_scoped,omitempty"`
	RoleID               string     `gorm:"index;size:128" json:"role_id,omitempty"`
	RoleRevision         string     `gorm:"index;size:80" json:"role_revision,omitempty"`
	RoleSnapshot         string     `gorm:"type:text" json:"-"`
	SessionID            string     `gorm:"index;size:64" json:"session_id"`
	UserID               string     `gorm:"index;size:128" json:"user_id"`
	TaskType             string     `gorm:"size:50" json:"task_type"`
	Status               string     `gorm:"size:20;default:'pending'" json:"status"` // pending, running, pausing, paused, waiting_approval, completed, failed, stopped
	Message              string     `gorm:"type:text" json:"message"`
	AgentName            string     `gorm:"size:100" json:"agent_name"`
	Channel              string     `gorm:"size:50" json:"channel"`
	Result               string     `gorm:"type:text" json:"result"` // 存储 JSON 字符串
	ErrorMsg             string     `gorm:"type:text" json:"error_msg"`
	HeartbeatAt          *time.Time `gorm:"index" json:"heartbeat_at,omitempty"`
	CurrentPhase         string     `gorm:"index;size:64" json:"current_phase,omitempty"`
	RunAttempt           int        `json:"run_attempt"`
	LastToolCallID       string     `gorm:"index;size:128" json:"last_tool_call_id,omitempty"`
	CallerID             string     `gorm:"index;size:128" json:"caller_id,omitempty"`
	ResolvedWorkspace    string     `gorm:"size:1024" json:"resolved_workspace"`
	RequestPayload       string     `gorm:"type:text" json:"-"`
	QueueName            string     `gorm:"index;size:32" json:"queue_name,omitempty"`
	QueueOwner           string     `gorm:"index;size:160" json:"queue_owner,omitempty"`
	LeaseToken           string     `gorm:"index;size:64" json:"-"`
	LeaseUntil           *time.Time `gorm:"index" json:"lease_until,omitempty"`
	AvailableAt          *time.Time `gorm:"index" json:"available_at,omitempty"`
	ClaimedAt            *time.Time `json:"claimed_at,omitempty"`
	RecoveryState        string     `gorm:"index;size:24" json:"recovery_state,omitempty"`
	RecoveryJournal      string     `gorm:"type:text" json:"-"`
	RecoveryPreview      string     `gorm:"type:text" json:"recovery_preview,omitempty"`
	RecoveryEntryCount   int        `json:"recovery_entry_count,omitempty"`
	RecoveryLastActivity *time.Time `json:"recovery_last_activity,omitempty"`
	ResumedByTaskID      string     `gorm:"index;size:64" json:"resumed_by_task_id,omitempty"`
	ResumedFromTaskID    string     `gorm:"index;size:64" json:"resumed_from_task_id,omitempty"`
	CreatedAt            time.Time  `json:"created_at"`
	CompletedAt          *time.Time `json:"completed_at"`
	UpdatedAt            time.Time  `json:"updated_at"`
}

const orphanGatewayTaskError = "task interrupted because the previous MACS process exited before completion"

// recoverOrphanGatewayTasks reconciles durable task state with the fact that
// the in-memory queue and goroutines do not survive a process restart. It must
// run once during process-level database initialization, before a new Gateway
// service starts accepting work. Pending tasks are included because there is no
// persisted queue from which they could resume.
func recoverOrphanGatewayTasks(db *gorm.DB, recoveredAt time.Time) (int64, error) {
	if db == nil {
		return 0, fmt.Errorf("database connection is nil")
	}
	recoveredAt = recoveredAt.Round(0)
	var recovered int64
	err := db.Transaction(func(tx *gorm.DB) error {
		var taskIDs []string
		if err := tx.Model(&GatewayTaskRecord{}).
			Where("status IN ? AND (queue_name = '' OR queue_name IS NULL)", []string{"pending", "running", "pausing"}).
			Pluck("task_id", &taskIDs).Error; err != nil {
			return fmt.Errorf("find orphan gateway tasks: %w", err)
		}
		if len(taskIDs) > 0 {
			result := tx.Model(&GatewayTaskRecord{}).
				Where("task_id IN ? AND status IN ? AND (queue_name = '' OR queue_name IS NULL)", taskIDs, []string{"pending", "running", "pausing"}).
				Updates(map[string]interface{}{
					"status":         "failed",
					"error_msg":      orphanGatewayTaskError,
					"recovery_state": sessioncore.RecoveryStatePendingScan,
					"current_phase":  "interrupted",
					"heartbeat_at":   recoveredAt,
					"completed_at":   recoveredAt,
					"updated_at":     recoveredAt,
				})
			if result.Error != nil {
				return fmt.Errorf("fail orphan gateway tasks: %w", result.Error)
			}
			recovered = result.RowsAffected
		}

		// Also reconcile messages left non-terminal by an older process that
		// managed to persist the task terminal state first (for example stop).
		for _, status := range []string{"completed", "degraded", "failed", "stopped", "timeout", "cancel", "canceled", "cancelled"} {
			var terminalTaskIDs []string
			if err := tx.Model(&GatewayTaskRecord{}).Where("status = ?", status).Pluck("task_id", &terminalTaskIDs).Error; err != nil {
				return fmt.Errorf("find %s gateway tasks for message reconciliation: %w", status, err)
			}
			if len(terminalTaskIDs) == 0 {
				continue
			}
			if err := tx.Model(&ChatMessage{}).
				Where("task_id IN ? AND status IN ?", terminalTaskIDs, []string{"pending", "running"}).
				Update("status", status).Error; err != nil {
				return fmt.Errorf("reconcile messages for %s gateway tasks: %w", status, err)
			}
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return recovered, nil
}

func SaveGatewayTaskToDB(db *gorm.DB, task *GatewayTask) error {
	if db == nil {
		return fmt.Errorf("database connection is nil")
	}

	if task == nil {
		return fmt.Errorf("task is nil")
	}

	record := &GatewayTaskRecord{
		TaskID:               task.ID,
		TenantID:             task.TenantID,
		SubjectID:            strings.TrimSpace(task.SubjectID),
		TenantScoped:         task.TenantScoped || task.QueueName == agentOSQueueName,
		RoleID:               task.RoleID,
		RoleRevision:         task.RoleRevision,
		RoleSnapshot:         task.RoleSnapshot,
		Status:               task.Status,
		RecoveryState:        task.RecoveryState,
		RecoveryJournal:      task.RecoveryJournal,
		RecoveryPreview:      task.RecoveryPreview,
		RecoveryEntryCount:   task.RecoveryEntryCount,
		RecoveryLastActivity: task.RecoveryLastActivity,
		ResumedByTaskID:      task.ResumedByTaskID,
		ResumedFromTaskID:    task.ResumedFromTaskID,
		ResolvedWorkspace:    strings.TrimSpace(task.ResolvedWorkspace),
		HeartbeatAt:          task.HeartbeatAt,
		CurrentPhase:         task.CurrentPhase,
		RunAttempt:           task.RunAttempt,
		LastToolCallID:       task.LastToolCallID,
		CallerID:             task.CallerID,
		QueueName:            task.QueueName,
		QueueOwner:           task.QueueOwner,
		LeaseToken:           task.LeaseToken,
		LeaseUntil:           task.LeaseUntil,
		AvailableAt:          task.AvailableAt,
		ClaimedAt:            task.ClaimedAt,
		CreatedAt:            task.CreatedAt,
		UpdatedAt:            time.Now(),
	}

	if task.CompletedAt != nil {
		record.CompletedAt = task.CompletedAt
	}

	if task.Request != nil {
		taskTenantID := strings.TrimSpace(record.TenantID)
		requestTenantID := strings.TrimSpace(task.Request.TenantID)
		if taskTenantID != "" && requestTenantID != "" &&
			normalizePersonalOwnerTenantID(taskTenantID) != normalizePersonalOwnerTenantID(requestTenantID) {
			return fmt.Errorf("%w: task and request tenants differ", ErrTaskOwnerConflict)
		}
		if strings.TrimSpace(record.TenantID) == "" {
			record.TenantID = task.Request.TenantID
		}
		requestSubjectID := strings.TrimSpace(task.Request.SubjectID)
		if record.SubjectID != "" && requestSubjectID != "" && record.SubjectID != requestSubjectID {
			return fmt.Errorf("%w: task and request subjects differ", ErrTaskOwnerConflict)
		}
		if record.SubjectID == "" && record.TenantScoped {
			record.SubjectID = requestSubjectID
		}
		if strings.TrimSpace(record.RoleID) == "" {
			record.RoleID = task.Request.RoleID
		}
		record.SessionID = task.Request.SessionId
		record.UserID = task.Request.UserId
		if record.SubjectID == "" && record.TenantScoped {
			record.SubjectID = fallbackAgentOSTaskSubject(
				record.TenantID,
				record.CallerID,
				record.UserID,
			)
		}
		record.TaskType = task.Request.TaskType
		record.Message = task.Request.Message
		record.AgentName = task.Request.AgentName
		record.Channel = task.Request.Channel
		persistedRequest := *task.Request
		persistedRequest.SkillRun = cloneSkillRunRequest(task.Request.SkillRun)
		persistedRequest.TenantID = record.TenantID
		persistedRequest.SubjectID = record.SubjectID
		if requestBytes, err := json.Marshal(&persistedRequest); err == nil {
			record.RequestPayload = string(requestBytes)
		}
		if strings.TrimSpace(record.ResolvedWorkspace) == "" && strings.TrimSpace(task.Request.ResolvedWorkspace) != "" {
			record.ResolvedWorkspace = projectctx.NormalizeWorkspace(task.Request.ResolvedWorkspace)
		}
	}
	if strings.TrimSpace(record.TenantID) == "" {
		record.TenantID = defaultExecutionTenantID
	}
	if record.TenantScoped && strings.TrimSpace(record.SubjectID) == "" {
		record.SubjectID = fallbackAgentOSTaskSubject(
			record.TenantID,
			record.CallerID,
			record.UserID,
		)
	}
	if strings.TrimSpace(record.RoleID) == "" {
		record.RoleID = record.AgentName
	}

	if task.Error != nil {
		record.ErrorMsg = task.Error.Error()
	}

	if task.Result != nil {
		resultBytes, err := json.Marshal(task.Result)
		if err != nil {
			// 如果 JSON 序列化失败，尝试转换为字符串
			record.Result = fmt.Sprintf("%v", task.Result)
		} else {
			record.Result = string(resultBytes)
		}
	}

	// UPSERT with bounded retry. SQLite can briefly report BUSY while chat,
	// progress, and task records finish at the same time; dropping the terminal
	// payload in that window makes a completed session look unanswered.
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		var existing GatewayTaskRecord
		lookup := db.Where("task_id = ?", task.ID).First(&existing)
		switch lookup.Error {
		case nil:
			if !samePersistedPersonalOwner(
				existing.TenantID, existing.SubjectID,
				record.TenantID, record.SubjectID,
			) {
				return fmt.Errorf("%w: task %q owner is immutable", ErrTaskOwnerConflict, task.ID)
			}
			record.ID = existing.ID
			if task.QueueName == agentOSQueueName && strings.TrimSpace(task.LeaseToken) != "" {
				updated := db.Model(&GatewayTaskRecord{}).
					Where("task_id = ? AND lease_token = ?", task.ID, task.LeaseToken).
					Select("*").Updates(record)
				if updated.Error != nil {
					lastErr = updated.Error
				} else if updated.RowsAffected == 0 {
					lastErr = agentos.ErrLeaseLost
				} else {
					lastErr = nil
				}
			} else {
				lastErr = db.Save(record).Error
			}
		case gorm.ErrRecordNotFound:
			record.ID = 0
			lastErr = db.Create(record).Error
		default:
			lastErr = lookup.Error
		}
		if lastErr == nil {
			syncGatewayTaskPersistedOwner(task, record)
			return nil
		}
		if !isTransientDatabaseWriteError(lastErr) || attempt == 2 {
			return lastErr
		}
		time.Sleep(time.Duration(attempt+1) * 25 * time.Millisecond)
	}
	return lastErr
}

func syncGatewayTaskPersistedOwner(task *GatewayTask, record *GatewayTaskRecord) {
	if task == nil || record == nil {
		return
	}
	tenantID := normalizePersonalOwnerTenantID(record.TenantID)
	subjectID := strings.TrimSpace(record.SubjectID)
	if strings.TrimSpace(task.TenantID) == "" {
		task.TenantID = tenantID
	}
	if strings.TrimSpace(task.SubjectID) == "" && subjectID != "" {
		task.SubjectID = subjectID
	}
	if !task.TenantScoped && record.TenantScoped {
		task.TenantScoped = true
	}
	if task.Request == nil {
		return
	}
	if strings.TrimSpace(task.Request.TenantID) == "" {
		task.Request.TenantID = tenantID
	}
	if strings.TrimSpace(task.Request.SubjectID) == "" && subjectID != "" {
		task.Request.SubjectID = subjectID
	}
}

func fallbackAgentOSTaskSubject(tenantID, callerID, userID string) string {
	if subjectID, err := security.ValidatePersonalSubjectID(userID); err == nil && subjectID != "" {
		return subjectID
	}
	principal := strings.TrimSpace(callerID)
	if principal == "" {
		principal = strings.TrimSpace(userID)
	}
	if principal == "" {
		principal = "anonymous"
	}
	return "actor_" + security.PersonalPartitionID(tenantID, principal, defaultExecutionTenantID)
}

func isTransientDatabaseWriteError(err error) bool {
	if err == nil {
		return false
	}
	lower := strings.ToLower(err.Error())
	for _, marker := range []string{"database is locked", "database table is locked", "database is busy", "deadlock", "lock wait timeout"} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func LoadTaskFromDB(db *gorm.DB, taskID string) (*GatewayTaskRecord, error) {
	if db == nil {
		return nil, fmt.Errorf("database connection is nil")
	}

	var record GatewayTaskRecord
	err := db.Where("task_id = ?", taskID).First(&record).Error
	if err != nil {
		return nil, err
	}
	return &record, nil
}

// UpdateTaskStatusInDB 更新任务状态（独立函数）
func UpdateTaskStatusInDB(db *gorm.DB, taskID string, status string, errMsg string) error {
	if db == nil {
		return fmt.Errorf("database connection is nil")
	}

	updates := map[string]interface{}{
		"status":     status,
		"updated_at": time.Now(),
	}

	if errMsg != "" {
		updates["error_msg"] = errMsg
	}

	if status == "completed" || status == "degraded" || status == "failed" || status == "stopped" {
		now := time.Now()
		updates["completed_at"] = now
	}

	return db.Model(&GatewayTaskRecord{}).Where("task_id = ?", taskID).Updates(updates).Error
}

// ShouldStopTask 检查任务是否应该停止（独立函数）
func ShouldStopTask(db *gorm.DB, taskID string) bool {
	if db == nil {
		return false
	}

	var record GatewayTaskRecord
	err := db.Where("task_id = ?", taskID).Select("status").First(&record).Error
	if err != nil {
		return false
	}
	return record.Status == "stopped"
}

// DeleteTaskFromDB 从数据库删除任务（独立函数）
func DeleteTaskFromDB(db *gorm.DB, taskID string) error {
	if db == nil {
		return fmt.Errorf("database connection is nil")
	}

	return db.Where("task_id = ?", taskID).Delete(&GatewayTaskRecord{}).Error
}

// ListTasksFromDB 从数据库列出任务（独立函数）
func ListTasksFromDB(db *gorm.DB, status string, limit int) ([]GatewayTaskRecord, error) {
	if db == nil {
		return nil, fmt.Errorf("database connection is nil")
	}

	var records []GatewayTaskRecord
	query := db.Order("created_at DESC")

	if status != "" && status != "all" {
		query = query.Where("status = ?", status)
	}

	if limit > 0 {
		query = query.Limit(limit)
	}

	err := query.Find(&records).Error
	return records, err
}

// ListLegacyTasksFromDB backs the unauthenticated Web compatibility surface.
// Tenant-scoped AgentOS records must never be returned through that surface.
func ListLegacyTasksFromDB(db *gorm.DB, status string, limit int) ([]GatewayTaskRecord, error) {
	if db == nil {
		return nil, fmt.Errorf("database connection is nil")
	}
	var records []GatewayTaskRecord
	query := db.
		Where("queue_name IS NULL OR queue_name = ''").
		Order("created_at DESC")
	if status != "" && status != "all" {
		query = query.Where("status = ?", status)
	}
	if limit > 0 {
		query = query.Limit(limit)
	}
	err := query.Find(&records).Error
	return records, err
}

// AEConfigRecord stores web-managed digital employee configs in DB.
type AEConfigRecord struct {
	ID              uint   `gorm:"primaryKey"`
	AeID            string `gorm:"uniqueIndex:idx_ae_config_tenant;size:128"`
	TenantID        string `gorm:"uniqueIndex:idx_ae_config_tenant;size:100;default:default"`
	Name            string `gorm:"size:256"`
	AtName          string `gorm:"size:256"`
	Note            string `gorm:"size:256"`
	Role            string `gorm:"size:256"`
	Pipeline        string `gorm:"size:256"`
	Supervisor      string `gorm:"size:256"`
	Channel         string `gorm:"size:64"`
	Tier            string `gorm:"size:32;default:community"`
	ModelBinding    string `gorm:"size:1024"`
	SystemPrompt    string `gorm:"type:text"`     // DE-level personality prompt
	MentionAliases  string `gorm:"type:text"`     // JSON array of extra @mention aliases
	RoutingExamples string `gorm:"type:text"`     // JSON array of few-shot example messages
	InternalOnly    bool   `gorm:"default:false"` // retained for compatibility/diagnostics; never exposed as a user-routable employee
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

func (r *AEConfigRecord) ToConfig() AIEmployeeConfig {
	var examples []string
	if r.RoutingExamples != "" {
		json.Unmarshal([]byte(r.RoutingExamples), &examples)
	}
	var mentionAliases []string
	if r.MentionAliases != "" {
		json.Unmarshal([]byte(r.MentionAliases), &mentionAliases)
	}
	return AIEmployeeConfig{
		ID: r.AeID, Name: r.Name, AtName: r.AtName,
		Note: r.Note, Role: r.Role, SystemMessage: r.SystemPrompt,
		Pipeline:   r.Pipeline,
		Supervisor: r.Supervisor, Channel: r.Channel,
		Tier: r.Tier, ModelBinding: r.ModelBinding,
		MentionAliases:  mentionAliases,
		RoutingExamples: examples,
		InternalOnly:    r.InternalOnly,
	}
}

var supportedAEModelCapabilities = map[string]bool{
	"text-to-text":  true,
	"multi-modal":   true,
	"text-to-image": true,
	"text-to-video": true,
	"audio":         true,
	"default":       true,
}

func normalizeAEModelBinding(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	var binding map[string]string
	if err := json.Unmarshal([]byte(raw), &binding); err != nil {
		return "", fmt.Errorf("model_binding must be a JSON object: %w", err)
	}
	cleaned := make(map[string]string)
	for capability, model := range binding {
		capability = strings.TrimSpace(capability)
		model = strings.TrimSpace(model)
		if capability == "" || model == "" {
			continue
		}
		if !supportedAEModelCapabilities[capability] {
			return "", fmt.Errorf("unsupported model capability %q", capability)
		}
		cleaned[capability] = model
	}
	if len(cleaned) == 0 {
		return "", nil
	}
	data, _ := json.Marshal(cleaned)
	return string(data), nil
}

func decodeAIEmployeeConfigPayload(r io.Reader) (AIEmployeeConfig, string, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return AIEmployeeConfig{}, "", err
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return AIEmployeeConfig{}, "", err
	}
	if mb, ok := raw["model_binding"]; ok {
		trimmed := bytes.TrimSpace(mb)
		if len(trimmed) > 0 && trimmed[0] == '{' {
			compact := new(bytes.Buffer)
			if err := json.Compact(compact, trimmed); err != nil {
				return AIEmployeeConfig{}, "", fmt.Errorf("invalid model_binding: %w", err)
			}
			encoded, _ := json.Marshal(compact.String())
			raw["model_binding"] = encoded
		}
	}
	normalized, _ := json.Marshal(raw)
	var cfg AIEmployeeConfig
	if err := json.Unmarshal(normalized, &cfg); err != nil {
		return AIEmployeeConfig{}, "", err
	}
	provider := ""
	if providerRaw, ok := raw["provider"]; ok {
		_ = json.Unmarshal(providerRaw, &provider)
	}
	return cfg, provider, nil
}

// Replace JSON file storage with DB-based storage
func (g *GatewayService) loadAEsFromDB(tenantID string) []AIEmployeeConfig {
	if g.db == nil {
		return nil
	}
	var records []AEConfigRecord
	q := g.db.Model(&AEConfigRecord{})
	if tenantID != "" {
		q = q.Where("tenant_id = ?", tenantID)
	}
	q.Find(&records)
	// Build hardcoded defaults lookup for merging Direct + SystemMessage
	defaults := make(map[string]*AIEmployeeConfig)
	defaultList := defaultAIEmployees()
	for i := range defaultList {
		d := &defaultList[i]
		defaults[d.ID] = d
	}
	var configs []AIEmployeeConfig
	for _, r := range records {
		// relationship-mentor and hardship-support were briefly shipped as
		// product-owned AI employees even though their intended extension point is
		// a builtin Skill. Ignore the exact legacy seed records if a read-only DB or
		// a failed migration prevents their removal. Any user-customized record has
		// a different fingerprint and remains available.
		if isLegacySkillOnlyDefaultAE(r, legacySkillOnlyDefaultAEFingerprints) {
			continue
		}
		if r.AeID == aiEmployeeDefaultModelBindingID {
			setAIEmployeeDefaultModelBinding(r.ModelBinding)
			continue
		}
		cfg := r.ToConfig()
		if def, ok := defaults[cfg.ID]; ok {
			applyBuiltInAEAliases(&cfg)
			cfg.Hidden = def.Hidden
			cfg.InternalOnly = def.InternalOnly
			// Merge Direct config from hardcoded defaults (not stored in DB)
			if cfg.Direct == nil && def.Direct != nil {
				cfg.Direct = def.Direct
			}
			// Merge Pipeline from hardcoded defaults if DB record is empty.
			if cfg.Pipeline == "" && def.Pipeline != "" {
				cfg.Pipeline = def.Pipeline
			}
			// Merge SystemMessage from hardcoded defaults if DB record is empty
			if cfg.SystemMessage == "" && def.SystemMessage != "" {
				cfg.SystemMessage = def.SystemMessage
			}
			if len(cfg.MentionAliases) == 0 && len(def.MentionAliases) > 0 {
				cfg.MentionAliases = append([]string(nil), def.MentionAliases...)
			}
			if len(cfg.RoutingExamples) == 0 && len(def.RoutingExamples) > 0 {
				cfg.RoutingExamples = append([]string(nil), def.RoutingExamples...)
			}
			if cfg.ModelBinding == "" && def.ModelBinding != "" {
				cfg.ModelBinding = def.ModelBinding
			}
			if strings.TrimSpace(cfg.Note) == "" && strings.TrimSpace(def.Note) != "" {
				cfg.Note = def.Note
			}
			applyBuiltInAEPolicy(&cfg, def)
		}
		// Generic fallback: name + role
		if cfg.SystemMessage == "" && cfg.Name != "" {
			cfg.SystemMessage = "你是" + cfg.Name + "。" + cfg.Role + "。"
		}
		configs = append(configs, cfg)
	}
	return configs
}

// legacySkillOnlyDefaultAEFingerprints identifies only the exact records that
// OpenKitty itself seeded while relationship-mentor and hardship-support were
// temporarily represented as AI employees. Timestamps and the numeric DB ID
// are intentionally excluded. If a user changes any persisted employee field,
// the fingerprint changes and the record is preserved as a custom employee.
var legacySkillOnlyDefaultAEFingerprints = map[string]string{
	"hardship-support":    "81e69f660f8e8b9164c90c4dd089dd32cd903e84b0b8d3b42912bec8cad076b2",
	"relationship-mentor": "01a7e303ea199383ccd0b3df48283f59c4aca9726ec13197146566e68775bef6",
}

func aeConfigRecordFingerprint(record AEConfigRecord) string {
	internalOnly := ""
	if record.InternalOnly {
		internalOnly = "1"
	}
	canonical := strings.Join([]string{
		record.TenantID,
		record.Name,
		record.AtName,
		record.Note,
		record.Role,
		record.Pipeline,
		record.Supervisor,
		record.Channel,
		record.Tier,
		record.ModelBinding,
		record.SystemPrompt,
		record.MentionAliases,
		record.RoutingExamples,
		internalOnly,
	}, "\x00")
	sum := sha256.Sum256([]byte(canonical))
	return hex.EncodeToString(sum[:])
}

func isLegacySkillOnlyDefaultAE(record AEConfigRecord, fingerprints map[string]string) bool {
	expected, ok := fingerprints[strings.TrimSpace(record.AeID)]
	return ok && expected != "" && aeConfigRecordFingerprint(record) == expected
}

func (g *GatewayService) retireLegacySkillOnlyDefaultAEs() {
	g.retireLegacySkillOnlyDefaultAEsWithFingerprints(legacySkillOnlyDefaultAEFingerprints)
}

func (g *GatewayService) retireLegacySkillOnlyDefaultAEsWithFingerprints(fingerprints map[string]string) {
	if g == nil || g.db == nil || len(fingerprints) == 0 {
		return
	}
	ids := make([]string, 0, len(fingerprints))
	for id := range fingerprints {
		ids = append(ids, id)
	}
	var records []AEConfigRecord
	if err := g.db.Where("ae_id IN ?", ids).Find(&records).Error; err != nil {
		if g.logger != nil {
			g.logger.Printf("[Seed] Failed to inspect legacy Skill-only AI employees: %v", err)
		}
		return
	}
	for _, record := range records {
		if !isLegacySkillOnlyDefaultAE(record, fingerprints) {
			continue
		}
		if err := g.db.Delete(&record).Error; err != nil {
			if g.logger != nil {
				g.logger.Printf("[Seed] Failed to retire legacy Skill-only AI employee %s: %v", record.AeID, err)
			}
			continue
		}
		if g.logger != nil {
			g.logger.Printf("[Seed] Retired legacy AI employee %s; builtin Skill remains available", record.AeID)
		}
	}
}

// backfillAESystemMessages fills empty system_message fields from hardcoded defaults.
func backfillAESystemMessages(db *gorm.DB) {
	var records []AEConfigRecord
	db.Find(&records)
	defaults := defaultAIEmployees()
	updated := 0
	for i := range records {
		if records[i].SystemPrompt != "" {
			continue
		}
		found := false
		for _, d := range defaults {
			if d.ID == records[i].AeID && d.SystemMessage != "" {
				records[i].SystemPrompt = d.SystemMessage
				db.Model(&records[i]).Update("system_prompt", d.SystemMessage)
				updated++
				found = true
				log.Printf("[Backfill] AE %s: system_message populated from default", records[i].AeID)
				break
			}
		}
		// Generic fallback: name + role
		if !found && records[i].Name != "" {
			fallback := "你是" + records[i].Name + "。" + records[i].Role + "。"
			records[i].SystemPrompt = fallback
			db.Model(&records[i]).Update("system_prompt", fallback)
			updated++
			log.Printf("[Backfill] AE %s: system_message populated (generic)", records[i].AeID)
		}
	}
	if updated > 0 {
		log.Printf("[Backfill] %d AE(s) system_message backfilled", updated)
	}
}

func (g *GatewayService) saveAEToDB(cfg *AIEmployeeConfig) error {
	if g.db == nil {
		return fmt.Errorf("database not available")
	}
	if def, ok := policyManagedBuiltInAIEmployeeDefault(cfg.ID); ok {
		applyBuiltInAEPolicy(cfg, &def)
	}
	applyBuiltInAEAliases(cfg)
	tenantID := cfg.TenantID
	if tenantID == "" {
		tenantID = "default"
	}
	var record AEConfigRecord
	lookup := g.db.Where("ae_id = ? AND tenant_id = ?", cfg.ID, tenantID).Limit(1).Find(&record)
	if lookup.Error != nil {
		return lookup.Error
	}
	found := lookup.RowsAffected > 0
	record.AeID = cfg.ID
	record.TenantID = tenantID
	record.Name = cfg.Name
	record.AtName = cfg.AtName
	record.Note = cfg.Note
	record.Role = cfg.Role
	record.SystemPrompt = cfg.SystemMessage
	record.Pipeline = cfg.Pipeline
	record.Supervisor = cfg.Supervisor
	record.Channel = cfg.Channel
	record.Tier = cfg.Tier
	record.InternalOnly = cfg.InternalOnly
	modelBinding, err := normalizeAEModelBinding(cfg.ModelBinding)
	if err != nil {
		return err
	}
	cfg.ModelBinding = modelBinding
	record.ModelBinding = cfg.ModelBinding
	if len(cfg.MentionAliases) > 0 {
		data, _ := json.Marshal(cfg.MentionAliases)
		record.MentionAliases = string(data)
	} else {
		record.MentionAliases = ""
	}
	if len(cfg.RoutingExamples) > 0 {
		data, _ := json.Marshal(cfg.RoutingExamples)
		record.RoutingExamples = string(data)
	} else {
		record.RoutingExamples = ""
	}
	if !found {
		return g.db.Create(&record).Error
	}
	return g.db.Save(&record).Error
}

func applyBuiltInAEAliases(cfg *AIEmployeeConfig) {
	if cfg == nil {
		return
	}
	if cfg.ID == "meeting-secretary" {
		if cfg.Name == "" || cfg.Name == "会议秘书" {
			cfg.Name = "秘书"
		}
		if cfg.AtName == "" || cfg.AtName == "会议秘书" {
			cfg.AtName = "秘书"
		}
	}
	if cfg.ID == "customer-service" && (cfg.Name == "" || cfg.Name == "智能客服" || cfg.Name == "智能客服（转化客户）") {
		cfg.Name = "智能客服"
		if cfg.Note == "转化客户" {
			cfg.Note = ""
		}
	}
	if cfg.ID == "lead-acquisition" && (cfg.Name == "" || cfg.Name == "获客专员" || cfg.Name == "获客专员（转化客户）") {
		cfg.Name = "获客专员"
		if strings.TrimSpace(cfg.Note) == "" {
			cfg.Note = "转化客户"
		}
	}
	if cfg.ID == "lead-researcher" && (cfg.Name == "" || cfg.Name == "潜客研究员" || cfg.Name == "潜客研究员（发现客户）") {
		cfg.Name = "潜客研究员"
		if strings.TrimSpace(cfg.Note) == "" {
			cfg.Note = "发现客户"
		}
	}
}

func applyBuiltInAEPolicy(cfg *AIEmployeeConfig, def *AIEmployeeConfig) {
	if cfg == nil || def == nil {
		return
	}
	switch {
	case isPolicyManagedBuiltInAIEmployee(cfg.ID):
		// Keep stale DB rows from restoring an obsolete user-facing identity,
		// aliases or policy after a product-owned employee is upgraded.
		cfg.Name = def.Name
		cfg.AtName = def.AtName
		cfg.Role = def.Role
		cfg.Pipeline = def.Pipeline
		cfg.SystemMessage = def.SystemMessage
		if def.Direct == nil {
			cfg.Direct = nil
		} else {
			direct := *def.Direct
			direct.Tools = append([]string(nil), def.Direct.Tools...)
			direct.RequiredSkills = append([]string(nil), def.Direct.RequiredSkills...)
			cfg.Direct = &direct
		}
		cfg.MentionAliases = append([]string(nil), def.MentionAliases...)
		cfg.RoutingExamples = append([]string(nil), def.RoutingExamples...)
		cfg.Hidden = def.Hidden
		cfg.InternalOnly = def.InternalOnly
		return
	case cfg.ID == "data-analyst":
		// Continue below to preserve the data-analyst delivery-policy merge.
	default:
		return
	}
	defaultMsg := strings.TrimSpace(def.SystemMessage)
	if defaultMsg == "" {
		return
	}
	if strings.TrimSpace(cfg.SystemMessage) == "" {
		cfg.SystemMessage = defaultMsg
		if !dataAnalystSystemMessageHasPresentationPolicy(cfg.SystemMessage) {
			cfg.SystemMessage += "\n" + dataAnalystPresentationPolicyText()
		}
		return
	}
	var missing []string
	if !strings.Contains(cfg.SystemMessage, "产出文件清单") {
		missing = append(missing, defaultMsg)
	}
	if !dataAnalystSystemMessageHasPresentationPolicy(cfg.SystemMessage) && !dataAnalystSystemMessageHasPresentationPolicy(defaultMsg) {
		missing = append(missing, dataAnalystPresentationPolicyText())
	}
	if len(missing) == 0 {
		return
	}
	cfg.SystemMessage = strings.TrimSpace(cfg.SystemMessage) + "\n\n内置交付规则：" + strings.Join(missing, "\n")
}

func isBuiltInFinancialAIEmployee(id string) bool {
	switch strings.TrimSpace(id) {
	case "quant-trader", "investment-research-manager", "financial-intelligence-analyst":
		return true
	default:
		return false
	}
}

// isPolicyManagedBuiltInAIEmployee identifies product-owned employees whose
// identity, routing aliases and safety policy must migrate with the binary.
// It is deliberately separate from isBuiltInFinancialAIEmployee: the latter
// also controls historical work-record filtering for legacy financial facades.
func isPolicyManagedBuiltInAIEmployee(id string) bool {
	switch strings.TrimSpace(id) {
	case "fundraising-advisor", "ppt-polish-specialist", "product-manager", "bid-specialist":
		return true
	default:
		return isBuiltInFinancialAIEmployee(id)
	}
}

func isBuiltInFinancialAIPipeline(name string) bool {
	switch strings.TrimSpace(name) {
	case "ai-trader", "quant-trader", "investment-research-manager", "financial-intelligence-analyst":
		return true
	default:
		return false
	}
}

func policyManagedBuiltInAIEmployeeDefault(id string) (AIEmployeeConfig, bool) {
	if !isPolicyManagedBuiltInAIEmployee(id) {
		return AIEmployeeConfig{}, false
	}
	for _, employee := range defaultAIEmployees() {
		if employee.ID == id {
			return employee, true
		}
	}
	return AIEmployeeConfig{}, false
}

func dataAnalystSystemMessageHasPresentationPolicy(text string) bool {
	lower := strings.ToLower(text)
	return strings.Contains(lower, "ppt") ||
		strings.Contains(lower, "pptx") ||
		strings.Contains(lower, "powerpoint") ||
		strings.Contains(text, "幻灯片") ||
		strings.Contains(text, "演示文稿") ||
		strings.Contains(text, ".pptx")
}

func dataAnalystPresentationPolicyText() string {
	return "如果用户明确要求 PPT/PPTX/PowerPoint/幻灯片/演示文稿或汇报材料，必须在完整 Markdown 报告和图表之后生成真实 .pptx 文件并在最终回复给出路径。"
}

func (g *GatewayService) deleteDEFromDB(id string) error {
	if g.db == nil {
		return fmt.Errorf("database not available")
	}
	return g.db.Where("ae_id = ?", id).Delete(&AEConfigRecord{}).Error
}

// ====================== Pipeline DB Records ======================

type PipelineDBRecord struct {
	ID                      uint   `gorm:"primaryKey"`
	Name                    string `gorm:"uniqueIndex;size:256"`
	DisplayName             string `gorm:"size:256"`
	Description             string `gorm:"size:1024"`
	StepsJSON               string `gorm:"column:steps_json;type:text"`
	DatabaseConnectionsJSON string `gorm:"column:database_connections_json;type:text"`
	CreatedAt               time.Time
	UpdatedAt               time.Time
}

func (PipelineDBRecord) TableName() string { return "pipeline_definitions" }

func (r *PipelineDBRecord) ToPipeline() Pipeline {
	var steps []PipelineStep
	json.Unmarshal([]byte(r.StepsJSON), &steps)
	var databaseConnections []PipelineDatabaseConnection
	if strings.TrimSpace(r.DatabaseConnectionsJSON) != "" {
		json.Unmarshal([]byte(r.DatabaseConnectionsJSON), &databaseConnections)
	}
	return Pipeline{Name: r.Name, Desc: r.Description, DatabaseConnections: databaseConnections, Steps: steps}
}

type PipelineTriggerDBRecord struct {
	ID           uint   `gorm:"column:id;primaryKey"`
	Keyword      string `gorm:"column:keyword;size:256"`
	PipelineName string `gorm:"column:pipeline_name;size:256"`
	Channel      string `gorm:"column:channel;size:64"`
}

func (PipelineTriggerDBRecord) TableName() string { return "pipeline_triggers" }

func (g *GatewayService) loadPipelinesFromDB() {
	if g.db == nil {
		return
	}
	var records []PipelineDBRecord
	g.db.Find(&records)
	if len(records) == 0 {
		// Auto-seed from code templates on fresh DB
		g.seedDefaultPipelines()
		g.db.Find(&records)
	}
	g.syncBuiltinPipelines()
	g.db.Find(&records)
	if g.config.Pipeline.Pipelines == nil {
		g.config.Pipeline.Pipelines = make(map[string]Pipeline)
	}
	for name := range retiredBuiltinPipelines() {
		delete(g.config.Pipeline.Pipelines, name)
	}
	for _, r := range records {
		g.config.Pipeline.Pipelines[r.Name] = r.ToPipeline()
	}
	g.logger.Printf("[Pipeline] Loaded %d pipelines from DB", len(records))

	// Load triggers
	var triggers []PipelineTriggerDBRecord
	g.db.Find(&triggers)
	g.config.Pipeline.Triggers = nil
	for _, t := range triggers {
		g.config.Pipeline.Triggers = append(g.config.Pipeline.Triggers, PipelineTrigger{
			When: t.Keyword, Pipeline: t.PipelineName, Channel: t.Channel,
		})
	}
	if len(triggers) > 0 {
		g.logger.Printf("[Pipeline] Loaded %d triggers from DB", len(triggers))
	}
}

func (g *GatewayService) syncBuiltinPipelines() {
	if g.db == nil {
		return
	}
	g.retireBuiltinPipelines()
	builtins := g.defaultPipelineSet()
	for _, name := range []string{"lead-acquisition", "lead-research", "wide-research", "cs-router", "data-analysis-pipeline", "sales-support", "product-management", "wechat-article", "ppt-polish", "bid-proposal", "investment-advisory", "ai-trader", "quant-trader", "investment-research-manager", "financial-intelligence-analyst"} {
		p, ok := builtins[name]
		if !ok {
			continue
		}
		if err := g.savePipelineToDB(name, p); err != nil {
			g.logger.Printf("[Pipeline] Sync builtin %s failed: %v", name, err)
		}
	}
}

func retiredBuiltinPipelines() map[string]bool {
	return map[string]bool{}
}

func isRetiredBuiltinPipeline(name string) bool {
	return retiredBuiltinPipelines()[strings.TrimSpace(name)]
}

func (g *GatewayService) retireBuiltinPipelines() {
	if g.db == nil {
		return
	}
	for name := range retiredBuiltinPipelines() {
		if err := g.deletePipelineFromDB(name); err != nil {
			g.logger.Printf("[Pipeline] Retire builtin %s failed: %v", name, err)
		} else {
			g.logger.Printf("[Pipeline] Retired builtin pipeline: %s", name)
		}
	}
}

func (g *GatewayService) savePipelineToDB(name string, p Pipeline) error {
	if g.db == nil {
		return fmt.Errorf("database not available")
	}
	stepsJSON, _ := json.Marshal(p.Steps)
	databaseConnectionsJSON, _ := json.Marshal(p.DatabaseConnections)
	var record PipelineDBRecord
	lookup := g.db.Where("name = ?", name).Limit(1).Find(&record)
	if lookup.Error != nil {
		return lookup.Error
	}
	found := lookup.RowsAffected > 0
	record.Name = name
	record.DisplayName = p.Desc
	record.Description = p.Desc
	record.StepsJSON = string(stepsJSON)
	record.DatabaseConnectionsJSON = string(databaseConnectionsJSON)
	if !found {
		return g.db.Create(&record).Error
	}
	return g.db.Save(&record).Error
}

func (g *GatewayService) deletePipelineFromDB(name string) error {
	if g.db == nil {
		return fmt.Errorf("database not available")
	}
	g.db.Where("pipeline_name = ?", name).Delete(&PipelineTriggerDBRecord{})
	return g.db.Where("name = ?", name).Delete(&PipelineDBRecord{}).Error
}

// seedDefaultPipelines writes built-in pipeline templates to DB on first run.
func (g *GatewayService) seedDefaultPipelines() {
	if g.db == nil {
		return
	}
	g.logger.Printf("[Seed] Seeding default pipelines...")
	count := 0
	for _, tmpl := range GetPipelineTemplates() {
		var existing PipelineDBRecord
		lookup := g.db.Where("name = ?", tmpl.Name).Limit(1).Find(&existing)
		if lookup.Error != nil {
			g.logger.Printf("[Seed] Failed to inspect pipeline %s: %v", tmpl.Name, lookup.Error)
			continue
		}
		if lookup.RowsAffected > 0 {
			continue // already exists
		}
		stepsJSON, _ := json.Marshal(tmpl.Steps)
		g.db.Create(&PipelineDBRecord{
			Name: tmpl.Name, DisplayName: tmpl.Description,
			Description: tmpl.Description, StepsJSON: string(stepsJSON),
		})
		count++
	}
	// Also seed the dynamic pipelines that were created via web UI
	seedExtra := g.defaultPipelineSet()
	retired := retiredBuiltinPipelines()
	for name, p := range seedExtra {
		if retired[name] {
			continue
		}
		var existing PipelineDBRecord
		lookup := g.db.Where("name = ?", name).Limit(1).Find(&existing)
		if lookup.Error != nil {
			g.logger.Printf("[Seed] Failed to inspect pipeline %s: %v", name, lookup.Error)
			continue
		}
		if lookup.RowsAffected > 0 {
			continue
		}
		stepsJSON, _ := json.Marshal(p.Steps)
		databaseConnectionsJSON, _ := json.Marshal(p.DatabaseConnections)
		g.db.Create(&PipelineDBRecord{
			Name: name, DisplayName: p.Desc,
			Description: p.Desc, StepsJSON: string(stepsJSON), DatabaseConnectionsJSON: string(databaseConnectionsJSON),
		})
		count++
	}
	g.logger.Printf("[Seed] Seeded %d pipelines", count)
}

// findSessionDE returns the DE ID that last handled this session, if any.
// This enables conversation continuity: same session → same DE, no @mention needed.
func (g *GatewayService) findSessionDE(sessionID string) string {
	if g.db == nil {
		return ""
	}
	var msgs []struct {
		AgentName string `gorm:"size:128"`
		Role      string `gorm:"size:32"`
	}
	g.db.Model(&ChatMessage{}).Where("session_id = ? AND role = ?", sessionID, "assistant").
		Order("created_at desc").Limit(3).Find(&msgs)
	for _, m := range msgs {
		if m.AgentName != "" {
			// Check if it's a valid DE
			if g.aeManager != nil {
				if _, ok := g.aeManager.GetEmployee(m.AgentName); ok {
					return m.AgentName
				}
			}
		}
	}
	return ""
}

// firstModelConfig returns the best available model for classification tasks.
// Uses the same cost-aware preference order as fallback chains.
func (g *GatewayService) firstModelConfig() *ModelConfig {
	for _, name := range preferredModelConfigKeys(g.config.ModelConfig) {
		cfg := g.config.ModelConfig[name]
		g.logger.Printf("[Supervisor] Using model %s for classification", name)
		mc := cfg
		return &mc
	}
	return nil
}

// seedDefaultAEs writes default digital employee configs to DB on first run.
func (g *GatewayService) seedDefaultAEs() {
	if g.db == nil {
		return
	}
	g.retireLegacySkillOnlyDefaultAEs()
	g.logger.Printf("[Seed] Seeding default DEs...")
	created := 0
	migrated := 0
	skipped := 0
	for _, cfg := range defaultAIEmployees() {
		var existing AEConfigRecord
		lookup := g.db.Where("ae_id = ?", cfg.ID).Limit(1).Find(&existing)
		if lookup.Error != nil {
			g.logger.Printf("[Seed] Failed to inspect DE %s: %v", cfg.ID, lookup.Error)
			continue
		}
		if lookup.RowsAffected > 0 {
			if isPolicyManagedBuiltInAIEmployee(cfg.ID) {
				aliases, _ := json.Marshal(cfg.MentionAliases)
				routingExamples, _ := json.Marshal(cfg.RoutingExamples)
				updates := map[string]interface{}{
					"name":             cfg.Name,
					"at_name":          cfg.AtName,
					"role":             cfg.Role,
					"pipeline":         cfg.Pipeline,
					"system_prompt":    cfg.SystemMessage,
					"mention_aliases":  string(aliases),
					"routing_examples": string(routingExamples),
					"internal_only":    cfg.InternalOnly,
				}
				if err := g.db.Model(&existing).Updates(updates).Error; err != nil {
					g.logger.Printf("[Seed] Failed to migrate built-in financial DE %s: %v", cfg.ID, err)
				} else {
					migrated++
				}
			}
			skipped++
			continue
		}
		err := g.db.Create(&AEConfigRecord{
			AeID: cfg.ID, TenantID: "default", Name: cfg.Name, AtName: cfg.AtName,
			Note: cfg.Note, Role: cfg.Role, Pipeline: cfg.Pipeline,
			Supervisor: cfg.Supervisor, Channel: cfg.Channel,
			SystemPrompt: cfg.SystemMessage,
			ModelBinding: cfg.ModelBinding,
			InternalOnly: cfg.InternalOnly,
			MentionAliases: func() string {
				if len(cfg.MentionAliases) == 0 {
					return ""
				}
				data, _ := json.Marshal(cfg.MentionAliases)
				return string(data)
			}(),
			RoutingExamples: func() string {
				if len(cfg.RoutingExamples) == 0 {
					return ""
				}
				data, _ := json.Marshal(cfg.RoutingExamples)
				return string(data)
			}(),
		}).Error
		if err != nil {
			g.logger.Printf("[Seed] Failed to seed DE %s: %v", cfg.ID, err)
			continue
		}
		created++
	}
	g.logger.Printf("[Seed] Default DE sync complete: created=%d migrated=%d skipped=%d", created, migrated, skipped)
}

// defaultAIEmployees returns the built-in AI employee roster.
func defaultAIEmployees() []AIEmployeeConfig {
	employees := []AIEmployeeConfig{
		// ====== 📋 综合办公 ======
		{ID: "project-assistant", Name: "项目助理", AtName: "项目助理", Role: "项目周报/站会纪要/PR审查/发版管理/新人引导", Pipeline: "project-assistant-router"},
		{ID: "meeting-secretary", Name: "秘书", AtName: "秘书", Role: "会议纪要/日程安排/材料整理/待办跟进/摘要归档",
			SystemMessage: "你是综合秘书。用户消息和上传的参考材料都可以作为工作输入：支持文字、Markdown、PDF、音频（mp3/wav/m4a/ogg/flac）以及视频（mp4/mov/avi/webm，先提取音频）。根据用户需求完成会议纪要、日程安排、材料整理、待办提取、摘要归档和跟进提醒。不要搜索本地无关文件，用户提供的内容就是唯一材料；缺少关键信息时再向用户确认。",
			Direct:        &DirectConfig{Prompt: "你是综合秘书。先判断用户要做的是会议纪要、日程安排、材料整理、待办跟进还是摘要归档。优先读取用户提供的参考材料：Markdown、PDF、音频和视频文件；如是视频，先抽取音频再转写。按任务类型输出结构化结果：会议类包含参会人/议题/决议/待办/时间节点；日程类包含时间/地点/参与人/事项/提醒；材料类输出摘要/要点/待办。用 write_file 输出到 .openkitty/storage/secretary-{日期}.md。无足够材料时请用户补充。", Tools: []string{"extract", "read_file", "write_file"}}},
		{ID: "admin-assistant", Name: "行政助理", AtName: "行政助理", Role: "会议安排协调、文档归档管理、办公用品采购比价", Pipeline: "admin-support", Tier: "commercial"},

		// ====== 💰 投资与财务 ======
		{ID: "investment-advisor", Name: "投资顾问", AtName: "投资顾问", Role: "金融信息核验、基本面与估值研究、短线交易情报、风险预算和条件式投资建议", Pipeline: "investment-advisory",
			SystemMessage: "你是专业投资顾问。先区分信息查询、客观分析、一般策略、结合本人情况的个性化操作和真实执行五个决策层级，只处理当前问题需要的维度。行情、大盘、指数、行业、基金、事件或知识解释属于信息与研究任务：直接核验必要数据并回答，不询问持仓、成本、资产负债、投资经验、风险偏好或账户规模，也不要求用户批准只读行情查询。一般性的“能买吗、怎么看、是否值得关注”给条件场景、观察指标和失效条件，不因缺少个人资料而停止。只有用户明确要求结合本人持仓制定加减仓、止盈止损或精确仓位方案时，才检查会实质改变方案的最少参数；账户规模只在计算具体金额或数量时需要。严格区分可核验事实、透明推断、来源冲突和未知项；不得编造实时数据、概率或收益，不得执行或声称执行真实下单。默认用自然语言直接回答，只有深度研究或用户明确要求保存时才生成报告文件。"},
		{ID: "fundraising-advisor", Name: "融资专家", AtName: "融资专家", Role: "创业融资准备度、BP与财务模型审阅、投资人画像、TS条款、尽调、谈判和一级市场项目评估",
			MentionAliases: []string{
				"焕智FA", "融资专家", "投融资专家", "融资顾问", "投融资顾问", "创业融资顾问",
				"融资助手", "融资规划师", "创业融资", "融资专员", "创业融资专员", "投融资专员",
				"FA", "FA顾问", "融资FA", "BP顾问", "BP专家",
			},
			RoutingExamples: []string{"评估一下我们是否适合启动A轮融资", "审查这份TS的清算优先权和反稀释风险", "从投资方视角评估这个一级市场项目并形成投资备忘录"},
			SystemMessage: `你是融资专家，服务创业融资与一级市场投资，不处理股票行情和个人证券仓位。

先判断任务是 BP 制作、材料审阅、融资诊断、条款分析、投资方研究还是概念咨询，再只做需要的深度。具体项目先读取用户指定材料并补充必要对比；简单问题直接回答。

材料角色由用户语义决定，不由文件形式决定。用户称为“我的/我们的/本公司/本项目/我产品的资料、参考资料或介绍材料”的 URL、附件和正文，是项目方提供的标的材料；内容可作为初稿依据，标为项目方材料陈述并说明是否独立核验。只有用户明确称为竞品、对标、参考产品或行业案例时，才按外部对象处理。表述有歧义时结合最近修饰对象和整体任务作最有用的合理判断，不要因为材料是公开 URL 就抹掉唯一可识别的融资标的。

用户要求制作或起草 BP 时，本轮直接交付完整 v0 和真实可编辑 PPTX；明确只要聊天或不要文件时除外。BP 初稿不以团队、收入、轮次、金额等字段齐全为前提。利用现有材料写实定位、问题和产品，并主动补出有依据的目标客户、商业模式、获客路径、竞争策略、阶段目标、资金用途类别和风险对策。制作需要数字时自行给出“规划估算（非项目事实）”，列明关键假设与依据；用户未提供融资金额时，也要基于 runway、团队、算力与市场投入给出一套可编辑的建议金额或区间及用途比例，不得只写金额未知。无法核实的真实经营数据集中列成“项目方后续替换清单”，说明用户应补什么；不要先发问卷，不要让每章都只剩未知或占位符。只有标的完全无法识别且任何有用初稿都无法形成时，才问一个最小问题。

严格区分已核验事实、项目方陈述、规划估算、分析推断和未知，不把估算冒充真实收入、客户、合同、团队履历或融资历史，不编造案例、融资概率、机构偏好或来源。宣传性主张降级标注，局部指标只支持局部判断。涉及当前市场、机构、法规、交易或条款惯例时给出具体来源和截至时间。重大法律、财务、税务和交易文件提示专业人士复核。`,
			Direct: &DirectConfig{Prompt: `完成用户当前的投融资任务。上游已经读取指定材料并建立事实底稿；本步骤使用融资专业 skill，把事实转化成用户真正要的 BP、审阅意见、融资诊断、条款分析或投资方研究，不展示内部流程。

材料角色以用户原话为准：自有项目的资料可以支撑 BP，并标注为项目方材料陈述；明确的竞品或对标才作为外部参考。来源方宣传结论不得冒充独立事实。

制作 BP 时直接写完整 v0，不附准备度评分或前置问卷。优先写实已有材料能够支持的定位、痛点、产品和差异，并主动补出有依据的目标客户、商业模式、获客路径、竞争策略、阶段目标、资金用途类别和风险对策。需要数字时自行给出明确标注的规划估算、关键假设和推导依据；用户未给融资金额时，必须基于 runway、团队、算力与市场投入给出至少一套建议金额或区间和用途比例，不得只写金额未知；估算不能冒充项目现状。把确实无法核实的主体、团队履历、真实客户、经营数据和融资历史集中列成“项目方后续替换清单”，说明需要用户替换的字段；不要在正文反复写未知。除非用户明确要求互动，否则不要求确认、不以问题结尾，也不把文件交付推给下一轮。

只在用户明确要求诊断或评分时使用完整准备度框架。解释 TS 时遵循 skill 的概念基准。区分事实、项目方陈述、推断、假设和未知；不保证融资结果，不代替律师、会计师、税务师或持牌机构。`,
				Tools: []string{"list_skills", "use_skill", "read_file", "extract", "vision", "web_search", "web_fetch", "persistent_shell", "write_file"}, RequiredSkills: []string{"huanzhi-fa-fundraising"}, MaxIter: 24, ArtifactPolicy: aiEmployeeArtifactPolicyExplicitRequest}},
		{ID: "quant-trader", Name: "AI交易员", AtName: "交易员", Role: "基于 TradingAgents 研判市场状态、技术信号、交易时机与模拟交易计划", Pipeline: "ai-trader",
			MentionAliases: []string{"量化交易员", "AI量化交易员"},
			InternalOnly:   true,
			SystemMessage:  "你是独立运行的 AI 交易员，专注市场状态、技术结构、交易信号、入场与退出条件以及模拟交易计划。你只提供研究和模拟决策；不得承诺收益，不得执行真实下单，也不得声称已经下单。必须标注数据截至时间、信号依据、失效条件、不确定性和风险。"},
		{ID: "investment-research-manager", Name: "AI投研经理", AtName: "投研经理", Role: "基于 AI Hedge Fund 统筹基本面、估值、技术面与情绪研究，裁决分歧并形成风险预算和模拟仓位建议", Pipeline: "investment-research-manager",
			InternalOnly:  true,
			SystemMessage: "你是独立运行的 AI 投研经理，负责汇总基本面、估值、技术面和市场情绪，审查证据质量，处理研究观点冲突，并形成带置信度的风险预算与模拟仓位建议。不得把默认兜底决定冒充有效判断，不得执行真实下单；必须标注数据截至时间、关键分歧、未知项和决策失效条件。"},
		{ID: "financial-intelligence-analyst", Name: "AI金信分析师", AtName: "金信分析师", Role: "基于 FinGPT 分析金融新闻、公告、研报与舆情，提取事件、实体、情绪及其证据", Pipeline: "financial-intelligence-analyst",
			InternalOnly:  true,
			SystemMessage: "你是独立运行的 AI 金信分析师，负责分析金融新闻、公告、研报和舆情，提取事件、实体、情绪、时间与来源证据。必须区分事实、模型推断和未知项，标注材料及数据截至时间；不得仅凭情绪给出确定性买卖指令，不得执行真实下单。"},
		{ID: "risk-manager", Name: "风控专员", AtName: "风控专员", Role: "业务风险评估、合规检查清单、风险预警报告", Pipeline: "risk-management", Tier: "commercial"},
		{ID: "finance-specialist", Name: "财务专员", AtName: "财务专员", Role: "发票识别整理、费用报销审核、财务报表生成", Pipeline: "finance-review", Tier: "commercial"},

		// ====== ⚖️ 法务 ======
		{ID: "legal-review", Name: "法务顾问", AtName: "法务顾问", Role: "合同审查与法律风险合规", Pipeline: "contract-review"},
		{ID: "bid-specialist", Name: "投竞标专员", AtName: "投竞标", Role: "招标文件解读、响应矩阵、技术/商务标书、投标风险清单", Pipeline: "bid-proposal",
			MentionAliases: []string{"投标", "竞标", "标书", "投竞标专员"}},

		// ====== 📝 内容与市场 ======
		{ID: "content-editor", Name: "微信公众号编辑", AtName: "微信公众号编辑", Role: "公众号选题、成稿、微信兼容HTML排版、封面摘要与发布前自查", Pipeline: "wechat-article",
			MentionAliases: []string{"公众号助手", "公众号编辑", "微信编辑"}},
		{ID: "ppt-polish-specialist", Name: "PPT润色专员", AtName: "PPT润色", Note: "PPT与文稿设计", Role: "PPT与文稿类设计：结构重构、内容润色、版式与视觉设计、图表重绘和真实可编辑文件交付", Pipeline: "ppt-polish",
			MentionAliases:  []string{"PPT润色", "ppt润色", "ppt润色专员", "PPT美化", "PPT润色专员", "幻灯片润色", "演示文稿设计师", "文稿设计师"},
			RoutingExamples: []string{"@PPT润色 润色这份PPT并生成可编辑的新文件", "@PPT润色 根据这份材料制作一份客户汇报PPT"}},
		{ID: "market-analyst", Name: "市场分析师", AtName: "市场分析师", Role: "市场趋势、行业研究、投资主题分析",
			SystemMessage: "你是市场分析师。用户消息就是研究课题，直接分析其关注的市场趋势、行业动态和投资主题，不做无关的本地文件扫描或意图复述。优先打开官方或一手来源核验关键结论与精确数字，并给出具体 URL、来源日期和分析截至时间。若具体页面暂时无法打开，可保留搜索结果实际返回的标题、发布方、日期和原始 URL，明确标为‘检索索引线索/待复核’，但不能据此给出精确数字或伪装成已核验事实；不要编造 URL。",
			Direct: &DirectConfig{Prompt: `完成用户当前的市场研究任务。先用 web_search 发现候选资料，再用 web_fetch 打开具体来源页核验。优先使用官方机构、公司公告、研究机构原文等一手来源。每个关键结论或精确数字旁给出可点击的具体 URL，并标明来源日期或数据期与本次分析截至时间。若具体页面经过一次有效抓取仍不可访问，可使用搜索结果实际返回的标题、发布方、日期与原始 URL 作为“检索索引线索/待复核”；这种线索只能支持定性方向，不能支持精确数字。证据不足时删去精确数字，不用看似精确的数字填空，也不要猜测或拼接 URL。

				直接开始研究，无需确认。用户要求报告或任务明显需要结构化研究交付时，用 write_file 保存到 .openkitty/storage/，且报告正文与聊天摘要都必须保留具体来源链接。`, Tools: []string{"web_fetch", "web_search", "write_file"}, MaxIter: 12, ArtifactPolicy: aiEmployeeArtifactPolicyExplicitRequest}, Tier: "commercial"},
		{ID: "sales-assistant", Name: "销售助理", AtName: "销售助理", Role: "销售线索跟进记录、报价单生成、客户回访提醒", Pipeline: "sales-support", Tier: "commercial"},
		{ID: "customer-service", Name: "智能客服", AtName: "客服", Role: "售前咨询/售中协助/售后服务/投诉受理/智能路由", Pipeline: "cs-router"},
		{ID: "cs-pre-sales", Name: "售前客服", AtName: "售前", Role: "商品咨询、价格查询、库存确认、促销推荐、商品对比", Pipeline: "cs-pre-sales"},
		{ID: "cs-after-sales", Name: "售后客服", AtName: "售后", Role: "物流追踪、退款处理、退货指导、投诉受理、工单跟进", Pipeline: "cs-after-sales"},
		{ID: "social-media", Name: "社交媒体运营", AtName: "社交媒体运营", Role: "内容策划排期、多平台发布、互动数据分析", Pipeline: "social-media-ops", Tier: "commercial",
			SystemMessage: "你是社交媒体运营专家。\n\n" +
				"## 核心能力\n" +
				"策划多平台内容选题、撰写适配各平台的文案、分析运营数据。\n\n" +
				"## 关键规则\n" +
				"- 用户询问\"热点/热搜/热门话题/有什么新闻\"时，先用 web_fetch 搜索相关平台的热榜/热搜，整理成清晰的热点列表回复用户。不要生成营销内容。\n" +
				"- 用户明确要求\"写文案/做策划/排期/制作内容\"时，才进入内容制作流程。\n" +
				"- 不确定用户意图时，先问清楚再行动。\n\n" +
				"## 回复风格\n" +
				"简洁明快，善于用表格呈现信息。"},
		{ID: "translator", Name: "翻译专员", AtName: "翻译专员", Role: "中英双向翻译、多语种文档本地化、术语一致性检查", Pipeline: "translation-service", Tier: "commercial"},

		// ====== 📊 数据与技术 ======
		{ID: "data-analyst", Name: "数据分析师", AtName: "数据分析", Role: "数据加载、统计分析、可视化和完整报告生成", Pipeline: "data-analysis-pipeline",
			SystemMessage: "你是数据分析师。用户给出数据文件、表格、业务数据或分析主题时，必须完成数据加载、统计分析、可视化和完整报告整理。最终交付优先保存为 .openkitty/storage/ 下的 Markdown 报告，并附真实图表/数据产物路径。报告必须包含摘要、数据概览、数据质量、核心发现、图表解读、建议和产出文件清单；不要只给零散结论。如果用户明确要求 PPT/PPTX/PowerPoint/幻灯片/演示文稿或汇报材料，必须在完整 Markdown 报告和图表之后生成真实 .pptx 文件并在最终回复给出路径。"},
		{ID: "it-ops", Name: "IT运维", AtName: "IT运维", Role: "服务器监控/日志分析/自动修复/部署回滚决策", Pipeline: "it-operations", Tier: "commercial"},
		{ID: "product-manager", Name: "产品经理", AtName: "产品经理", Note: "PRD、估算与报价", Role: "PRD需求管理、WBS项目拆分、工期与人力估算、成本测算、对外报价及标书方案整理", Pipeline: "product-management", Tier: "commercial",
			MentionAliases:  []string{"产品经理", "PRD专员", "需求专员", "项目拆分", "项目估算", "成本估算", "报价专员", "项目报价", "标书专员"},
			RoutingExamples: []string{"@产品经理 把需求拆成PRD和WBS并估算工期、人力、成本与报价", "@产品经理 根据招标材料整理响应矩阵和标书初稿"}},
		{ID: "mentor-buddy", Name: "新人导师", AtName: "新人导师", Role: "项目架构讲解、代码流程追踪、开发上手引导", Pipeline: "onboarding-walkthrough", Tier: "commercial"},
		{ID: "developer-engineer", Name: "开发工程师", AtName: "开发工程师", Role: "需求编码/调试修复/测试生成/代码重构/代码解释/文档生成", Pipeline: "developer-router"},
		{ID: "codex-specialist", Name: "codex专员", AtName: "codex专员", Role: "通过 Codex CLI 执行代码修改、测试、重构、代码解释，以及 PPT、海报、生图、视觉物料生成，并把执行报告、产物路径和变更文件回传给用户",
			MentionAliases: []string{"codex", "codeX", "Codex专员"},
			Direct: &DirectConfig{Prompt: `你是 Codex 专员，负责把用户的代码任务、PPT/海报/生图/视觉物料生成任务交给 Codex CLI 在当前 MACS 工作区内完成。

必须调用 external_agent 工具：
- agent: codex
- prompt: 使用用户的真实需求，不要改写成泛泛任务。

执行要求：
1. 先明确用户要改什么、验收标准是什么；信息足够时直接执行，不要空泛确认。
2. 如果用户要求生成 PPT、幻灯片、deck、演示文稿、海报、生图、配图或物料，必须在传给 Codex 的 prompt 中保留原始需求，并提示它使用 embedded builtin runtime、先用 builtin/presentation/ppt-art-director/SKILL.md 做艺术指导，并严格执行三级生产：Level 3 高端定制使用 builtin/presentation/ppt-master/SKILL.md 完整上游 Strategist/Executor；Level 2 常规使用内置脚手架创建任务专用 python-pptx/PptxGenJS 逐页脚本；Level 1 保底才使用 builtin/presentation/base-ppt/SKILL.md，只限明确快速、内部、周期性、固定结构、兼容占位符，或 Level 2 一次构建加一次修正后仍失败的公开降级。禁止空 generator 直接 convert 成 PPTX，禁止用一次性内联大脚本冒充 Level 2。图像型视觉保真可接受时可用 HTML/canvas；只有少量关键视觉资产确实有帮助时使用 builtin/presentation/paper-operators-visual/SKILL.md。better-ppt 只是兼容别名，不作为第二个对外路线。不能要求用户 workspace 存在 reference/。结果里必须写明实际路线、Editable true/false、PPTX 路径、页数、一次有界视觉检查结果和回退原因。
   明确要求漂亮、精美、高级、设计感，或面向高管/董事会/投资人/客户/发布/品牌展示时，Level 3 是首选路线，不得先做一版保守 Level 2。其他正式 Level 2 必须完成可执行 visual_plan.json，让配色、字体和逐页素材真正进入页面；最终按正文 16pt/标题 35pt 审计，逐页看渲染图，并让基于真实视觉观察的 aesthetic_qa.json 通过内置验证器。
3. external_agent 返回后，必须把 Report 路径、Changed files、Codex 输出摘要、测试结果、产物路径或失败原因回传给用户。
4. 如果 Codex 失败，给出可执行的下一步，不要只说失败。
5. 不要伪造文件路径、产物路径或测试结果。`, Tools: []string{"external_agent"}}},
		{ID: "cc-specialist", Name: "cc专员", AtName: "cc专员", Role: "通过 Claude Code 执行代码修改、测试、重构、代码解释，并把执行报告和变更文件回传给用户",
			MentionAliases: []string{"cc", "CC", "CC专员", "claude", "claude code", "claude code专员", "Claude Code专员"},
			Direct: &DirectConfig{Prompt: `你是 CC 专员，负责把用户的代码任务交给 Claude Code 在当前 MACS 工作区内完成。

必须调用 external_agent 工具：
- agent: claude
- prompt: 使用用户的真实需求，不要改写成泛泛任务。

执行要求：
1. 先明确用户要改什么、验收标准是什么；信息足够时直接执行，不要空泛确认。
2. external_agent 返回后，必须把 Report 路径、Changed files、Claude Code 输出摘要、测试结果或失败原因回传给用户。
3. 如果 Claude Code 失败，给出可执行的下一步，不要只说失败。
4. 不要伪造文件路径或测试结果。`, Tools: []string{"external_agent"}}},
		{ID: "code-review-bot", Name: "Code Review 机器人", AtName: "CR机器人", Role: "自动PR代码审查：安全检查/风格审查/逻辑审查", Pipeline: "pr-review", Tier: "commercial"},

		// ====== 👥 人事 ======
		{ID: "hr-recruiter", Name: "HR招聘专员", AtName: "HR招聘", Role: "简历筛选评估、岗位JD撰写、面试题库生成", Pipeline: "hr-recruitment", Tier: "commercial"},
		{ID: "trainer", Name: "培训讲师", AtName: "培训讲师", Role: "课程大纲设计、培训材料制作、学员知识测评出题", Pipeline: "training-design", Tier: "commercial"},

		// ====== 🏭 运营与供应链 ======
		{ID: "supply-chain", Name: "供应链管理", AtName: "供应链管理", Role: "供应商评估对比、采购成本优化、库存周转分析", Pipeline: "supply-chain-mgmt", Tier: "commercial"},
		{ID: "qa-specialist", Name: "质检专员", AtName: "质检专员", Role: "产品质量检验标准制定、缺陷追踪分析、质量报告", Pipeline: "quality-inspection", Tier: "commercial"},
		{ID: "event-planner", Name: "活动策划", AtName: "活动策划", Role: "活动方案设计、预算编制、执行清单与复盘报告", Pipeline: "event-planning", Tier: "commercial"},

		// ====== 📈 获客与销售 ======
		{ID: "lead-acquisition", Name: "获客专员", AtName: "获客", Note: "转化客户", Role: "潜客发现/画像分析/意向评级/触达话术/跟进计划/转化漏斗", Pipeline: "lead-acquisition"},
		{ID: "lead-researcher", Name: "潜客研究员", AtName: "潜客研究", Note: "发现客户", Role: "行业搜索/公司调研/采购需求信号识别/线索建档", Pipeline: "lead-research"},
	}
	for i := range employees {
		employees[i].Hidden = !defaultAIEmployeeVisible(employees[i].ID)
	}
	return employees
}

func defaultAIEmployeeVisible(id string) bool {
	switch id {
	case "investment-advisor",
		"fundraising-advisor",
		"meeting-secretary",
		"content-editor",
		"lead-researcher",
		"lead-acquisition",
		"customer-service",
		"ppt-polish-specialist",
		"product-manager",
		"data-analyst",
		"codex-specialist":
		return true
	default:
		return false
	}
}

// defaultPipelineSet returns the full pipeline catalog to seed on fresh DB.
// defaultPipelineSet returns the full pipeline catalog to seed on fresh DB.
func initGateway(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("数据库连接未初始化")
	}
	if err := gatewaymigrations.PrepareSQLite(db, (agentos.ToolExecutionReceipt{}).TableName(), DENotificationDB{}, struct {
		CID        int    `gorm:"column:cid"`
		Name       string `gorm:"column:name"`
		Type       string `gorm:"column:type"`
		PrimaryKey int    `gorm:"column:pk"`
	}{}); err != nil {
		return fmt.Errorf("迁移前兼容处理失败: %w", err)
	}
	// ✅ 移除 MessageRequest，它不应该作为数据库表
	err := db.AutoMigrate(
		&ConfigHistory{},
		&ChatMessage{},
		&ChatSession{}, // 添加 ChatSession 表
		&MessageRequestRecord{},
		&GatewayTaskRecord{},
		&agentos.ToolExecutionReceipt{},
		&SkillRunRecord{},
		&agentos.AgentTaskRequestRecord{},
		&agentos.TaskFollowupState{},
		&agentos.TaskFollowupRecord{},
		&approvalcore.ApprovalRequestRecord{},
		&PipelineExecutionRecord{},
		&RAGKb{},
		&RAGChunk{},
		&RAGFAQ{},
		&RAGSearchLog{},
		&projectctx.Customer{},
		&AEConfigRecord{},
		&AEWorkRecordDB{},
		&DENotificationDB{},
		&PipelineDBRecord{},
		&PipelineTriggerDBRecord{},
		&ModelProviderRecord{},
		&customerservice.HandoffRecord{},
		&gatewayhttp.UISetting{},
	)
	if err != nil {
		log.Printf("表结构迁移失败: %v", err)
		return fmt.Errorf("表结构迁移失败: %w", err)
	}
	backfilledFollowups, err := agentos.BackfillTaskFollowupStates(db, time.Now())
	if err != nil {
		return fmt.Errorf("初始化 AgentOS 追加指令状态失败: %w", err)
	}
	if backfilledFollowups > 0 {
		log.Printf("[Gateway] initialized follow-up state for %d existing AgentOS task(s)", backfilledFollowups)
	}
	recovered, err := recoverOrphanGatewayTasks(db, time.Now())
	if err != nil {
		return fmt.Errorf("恢复中断 Gateway 任务失败: %w", err)
	}
	if recovered > 0 {
		log.Printf("[Gateway] recovered %d orphan pending/running task(s) from the previous process", recovered)
	}
	recoveredReceipts, err := agentos.RecoverOrphanToolReceipts(db, time.Now())
	if err != nil {
		return fmt.Errorf("恢复中断工具回执失败: %w", err)
	}
	if recoveredReceipts > 0 {
		log.Printf("[Gateway] recovered %d orphan running tool receipt(s) from the previous process", recoveredReceipts)
	}
	recoveredRequests, err := agentos.RecoverOrphanTaskRequests(db)
	if err != nil {
		return fmt.Errorf("恢复孤立 AgentOS 幂等请求失败: %w", err)
	}
	if recoveredRequests > 0 {
		log.Printf("[Gateway] removed %d orphan AgentOS idempotency reservation(s)", recoveredRequests)
	}
	log.Println("Gateway table init succ")
	migrateFTS5(db)
	gatewayDB = db
	ragcore.SetDatabase(db)
	backfillAESystemMessages(db)
	knowledgetools.LoadAllVectorIndexes()
	ragmodel.InitSessionCleanup()
	return nil
}

func migrateFTS5(db *gorm.DB) {
	db.Exec("CREATE VIRTUAL TABLE IF NOT EXISTS chat_messages_fts USING fts5(session_id, content, tokenize='unicode61')")
	db.Exec("INSERT OR IGNORE INTO chat_messages_fts(rowid, session_id, content) SELECT id, session_id, content FROM chat_messages")
	db.Exec("DROP TRIGGER IF EXISTS chat_fts_insert")
	db.Exec("DROP TRIGGER IF EXISTS chat_fts_delete")
	db.Exec("DROP TRIGGER IF EXISTS chat_fts_update")
	db.Exec("CREATE TRIGGER IF NOT EXISTS chat_fts_insert AFTER INSERT ON chat_messages BEGIN INSERT INTO chat_messages_fts(rowid, session_id, content) VALUES (new.id, new.session_id, new.content); END")
	db.Exec("CREATE TRIGGER IF NOT EXISTS chat_fts_delete AFTER DELETE ON chat_messages BEGIN DELETE FROM chat_messages_fts WHERE rowid = old.id; END")
	db.Exec("CREATE TRIGGER IF NOT EXISTS chat_fts_update AFTER UPDATE ON chat_messages BEGIN DELETE FROM chat_messages_fts WHERE rowid = old.id; INSERT INTO chat_messages_fts(rowid, session_id, content) VALUES (new.id, new.session_id, new.content); END")

	// RAG chunks FTS5 index for fast BM25 retrieval
	db.Exec("CREATE VIRTUAL TABLE IF NOT EXISTS rag_chunks_fts USING fts5(kb_id, source_doc, content, tokenize='unicode61')")
	// Sync existing data (if any - typically empty on first run)
	db.Exec("INSERT OR IGNORE INTO rag_chunks_fts(rowid, kb_id, source_doc, content) SELECT id, kb_id, source_doc, content FROM rag_chunks")

	authcore.InitTenantUsers(db)
	log.Println("FTS5 search indexes ready")
}
func NewGatewayService(config *Config, engine *MultiAgentEngine, configPath string) *GatewayService {

	tm := newTodoManager(db, engine)
	todoManagerGlobal = tm

	g := &GatewayService{
		config:              config,
		engine:              engine,
		skillInstallDir:     skillpath.ExecutableSkillsDir(),
		remoteSkillRegistry: skilldomain.NewRemoteSkillRegistry(),

		db:          db,
		configpath:  configPath,
		project:     projectctx.New(config.Workspace),
		tasks:       make(map[string]*GatewayTask),
		logger:      log.New(os.Stdout, "[Gateway] ", log.LstdFlags),
		todoManager: tm,
	}

	g.channelManager = channelhost.NewManager(newChannelRuntimeAdapter(g))
	g.loadChannelsFromConfig()
	g.initSSE()
	g.eventBus = eventbus.New(g.logger)

	// EventBus subscriptions: forward events to SSE broadcast
	g.eventBus.Subscribe(eventbus.SessionCreated, func(evt eventbus.Event) { g.broadcastSessionListChanged() })
	g.eventBus.Subscribe(eventbus.SessionUpdated, func(evt eventbus.Event) { g.broadcastSessionListChanged() })
	g.eventBus.Subscribe(eventbus.TaskStarted, func(evt eventbus.Event) {
		g.BroadcastProgress(ProgressEvent{Type: "task_start", TaskID: evt.TaskID, Message: "Task started", Status: "running"})
	})
	g.eventBus.Subscribe(eventbus.TaskCompleted, func(evt eventbus.Event) {
		g.BroadcastProgress(ProgressEvent{Type: "task_update", TaskID: evt.TaskID, Message: "Task completed", Status: "completed"})
	})

	g.wakeService = voicecore.NewWakeService(g.submitVoiceTask)
	fmt.Println("🔧 [Gateway] 初始化 Pipeline + MCP...")
	knowledgetools.InitEmbedder(config.Embedding, func(modelPath string) knowledgetools.LocalEmbeddingProvider {
		provider := modelruntime.GetLocalLLM(modelPath, modelruntime.LocalLLMHooks{
			Confirm: func(message, actionID, actionLabel string) <-chan struct{} {
				return eventbus.PushSystemMessageAction(message, "action", actionID, actionLabel)
			},
			Notify:   eventbus.PushSystemMessage,
			Progress: eventbus.UpdateMessageProgress,
		})
		if provider == nil {
			return nil
		}
		return provider
	})
	memoryEmbedMode := strings.TrimSpace(config.MemoryConfig.EmbedMode)
	if memoryEmbedMode == "" && config.Embedding.IsEnabled() {
		memoryEmbedMode = string(config.Embedding.Mode)
	}
	memorycore.SetMemoryEmbed(memoryEmbedMode)

	// Configure LLM reranker + HyDE (use the preferred configured model)
	for _, name := range preferredModelConfigKeys(config.ModelConfig) {
		mc := config.ModelConfig[name]
		ragcore.ConfigureSearchEnhancer(knowledgetools.SearchModelConfig{
			APIKey: mc.APIKey, Model: mc.ModelName, BaseURL: mc.BaseURL,
		})
		g.logger.Printf("[Reranker+HyDE] Using model: %s", name)
		break
	}
	// Start cron scheduler for scheduled pipeline tasks
	g.startCronScheduler()
	g.startConfigWatcher()
	g.startLogRotation()
	g.pipelineExec = NewPipelineExecutor(engine.agentFactory, g, log.New(os.Stdout, "[Pipeline] ", log.LstdFlags))
	SetGlobalPipelineExec(g.pipelineExec)
	g.loadPipelinesFromDB()                // Overwrite YAML-loaded pipelines with DB version
	tm.SetPipelineExecutor(g.pipelineExec) // 让TODO能触发Pipeline
	g.mcpManager = mcpcore.NewMCPManager(log.New(os.Stdout, "[MCP] ", log.LstdFlags), CurrentVersion)
	g.aeManager = NewAIEmployeeManager(g, log.New(os.Stdout, "[AE] ", log.LstdFlags))
	g.codexInteractive = NewCodexInteractiveManager(g, log.New(os.Stdout, "[CodexInteractive] ", log.LstdFlags))
	SetGlobalAEManager(g.aeManager)
	var supervisorModel *aiemployee.SupervisorModelConfig
	if model := g.firstModelConfig(); model != nil {
		supervisorModel = &aiemployee.SupervisorModelConfig{
			APIKey: model.APIKey, ModelName: model.ModelName, BaseURL: model.BaseURL,
		}
	}
	g.supervisorRouter = aiemployee.NewSupervisorRouter(config.Supervisor, g.aeManager, supervisorModel, log.New(os.Stdout, "[Supervisor] ", log.LstdFlags))

	// Init customer service subsystem
	customerservice.InitCSMiddleware(log.New(os.Stdout, "[CS] ", log.LstdFlags), db)
	csBackend := businesstools.GetUniversalBackend()
	globalIdentityInjector = businesstools.NewIdentityInjector(csBackend, log.New(os.Stdout, "[Identity] ", log.LstdFlags))
	SetGlobalIdentityInjector(globalIdentityInjector)
	csBackend = businesstools.GetCSBackend()
	csTools := businesstools.RegisterAllCSTools(csBackend)
	engine.agentFactory.mu.RLock()
	for _, agent := range engine.agentFactory.agents {
		if llmAgent, ok := agent.(*LLMAgent); ok && llmAgent.toolRegistry != nil {
			for _, tool := range csTools {
				if _, exists := llmAgent.toolRegistry.Get(tool.Name()); !exists {
					llmAgent.toolRegistry.Register(tool)
				}
			}
			break
		}
	}
	engine.agentFactory.mu.RUnlock()
	g.logger.Printf("[CS] Registered %d customer service tools", len(csTools))

	// Init lead acquisition subsystem
	leadBackend := businesstools.GetLeadBackend()
	leadTools := businesstools.RegisterAllLeadTools(leadBackend)
	engine.agentFactory.mu.RLock()
	for _, agent := range engine.agentFactory.agents {
		if llmAgent, ok := agent.(*LLMAgent); ok && llmAgent.toolRegistry != nil {
			for _, tool := range leadTools {
				if _, exists := llmAgent.toolRegistry.Get(tool.Name()); !exists {
					llmAgent.toolRegistry.Register(tool)
				}
			}
			break
		}
	}
	engine.agentFactory.mu.RUnlock()
	// Register platform search tool for lead acquisition
	platformTool := networktools.NewPlatformSearchTool()
	engine.agentFactory.mu.RLock()
	for _, agent := range engine.agentFactory.agents {
		if llmAgent, ok := agent.(*LLMAgent); ok && llmAgent.toolRegistry != nil {
			if _, exists := llmAgent.toolRegistry.Get(platformTool.Name()); !exists {
				llmAgent.toolRegistry.Register(platformTool)
			}
			break
		}
	}
	engine.agentFactory.mu.RUnlock()

	g.logger.Printf("[Lead] Registered %d lead acquisition tools", len(leadTools))

	// Register business schema tools (universal query_* + do_* tools)
	bizProvider := businesstools.NewMockBusinessProvider(businesstools.EcommerceSchema())
	bizTools := businesstools.InitBusinessSystem(bizProvider)
	engine.agentFactory.mu.RLock()
	for _, agent := range engine.agentFactory.agents {
		if llmAgent, ok := agent.(*LLMAgent); ok && llmAgent.toolRegistry != nil {
			for _, tool := range bizTools {
				if _, exists := llmAgent.toolRegistry.Get(tool.Name()); !exists {
					llmAgent.toolRegistry.Register(tool)
				}
			}
			break
		}
	}
	engine.agentFactory.mu.RUnlock()
	g.logger.Printf("[Business] Registered %d schema-driven tools (%s)", len(bizTools), bizProvider.GetSchema().Name)

	// Init tool execution protection (circuit breaker + retry)
	InitToolProtector(log.New(os.Stdout, "[Protect] ", log.LstdFlags))
	g.logger.Printf("[Protect] Tool protection enabled (circuit breaker + retry)")

	// Register DE collaboration tools (delegate_to + list_colleagues)
	collabTools := RegisterCollaborationTools(g.aeManager, log.New(os.Stdout, "[Collab] ", log.LstdFlags))
	engine.agentFactory.mu.RLock()
	for _, agent := range engine.agentFactory.agents {
		if llmAgent, ok := agent.(*LLMAgent); ok && llmAgent.toolRegistry != nil {
			for _, tool := range collabTools {
				if _, exists := llmAgent.toolRegistry.Get(tool.Name()); !exists {
					llmAgent.toolRegistry.Register(tool)
				}
			}
			break
		}
	}
	engine.agentFactory.mu.RUnlock()
	g.logger.Printf("[Collab] Registered %d DE collaboration tools", len(collabTools))

	// Init model fallback (auto-degrade to backup models on failure)
	startConfiguredModelHealthCheck(config.ModelConfig, log.New(os.Stdout, "[Fallback] ", log.LstdFlags))
	g.logger.Printf("[Fallback] Model fallback + health check enabled")

	globalAutoWorker = nil
	g.logger.Printf("[AutoWorker] Disabled; scheduled AI employee work is handled by TodoManager")

	if engine.agentFactory != nil {
		vaultTool := knowledgetools.NewObsidianVaultTool()
		engine.agentFactory.mu.RLock()
		for _, agent := range engine.agentFactory.agents {
			if llmAgent, ok := agent.(*LLMAgent); ok && llmAgent.toolRegistry != nil {
				llmAgent.toolRegistry.Register(vaultTool)
			}
		}
		engine.agentFactory.mu.RUnlock()
	}
	RegisterPIITool(engine.agentFactory)
	g.secHooks = security.NewSecurityHookChain(log.New(os.Stdout, "[Security] ", log.LstdFlags))
	g.configPath = configPath
	g.EnsureToken()
	// Init tenant manager
	g.tenantMgr = tenantcore.NewTenantManager(filepath.Join(config.Workspace, ".openkitty", "tenants"), log.New(os.Stdout, "[Tenant] ", log.LstdFlags))
	g.evolver = pipelinecore.NewPipelineEvolver(filepath.Join(config.Workspace, ".openkitty", "pipeline_metrics.json"), false, log.New(os.Stdout, "[Evolve] ", log.LstdFlags))
	if len(config.Tenants) > 0 || len(config.ServiceClients) > 0 || config.Gateway.AuthEnabled {
		if err := g.tenantMgr.Init(config.Tenants, config.Gateway.AuthToken); err != nil {
			g.logger.Printf("[Tenant] initialization failed: %v", err)
		}
	}
	// Helper: extract pure port from ":8090" or "0.0.0.0:8090"
	gatewayPort := func() string {
		parts := strings.Split(config.Gateway.ListenAddr, ":")
		if len(parts) >= 2 {
			return parts[len(parts)-1]
		}
		return "8090"
	}()

	// SSO init
	g.ssoManager = authcore.NewSSOManager(log.New(os.Stdout, "[SSO] ", log.LstdFlags))
	if config.Channels != nil {
		for _, ch := range config.Channels {
			if ch.Type == "feishu" && ch.Enabled {
				if appID, _ := ch.Config["app_id"].(string); appID != "" {
					if secret, _ := ch.Config["app_secret"].(string); secret != "" {
						redirect := fmt.Sprintf("http://localhost:%s/api/auth/sso/callback?provider=feishu", gatewayPort)
						g.ssoManager.ConfigureFeishu(appID, secret, redirect)
					}
				}
			}
			if ch.Type == "dingtalk" && ch.Enabled {
				if appID, _ := ch.Config["app_id"].(string); appID != "" {
					if secret, _ := ch.Config["app_secret"].(string); secret != "" {
						redirect := fmt.Sprintf("http://localhost:%s/api/auth/sso/callback?provider=dingtalk", gatewayPort)
						g.ssoManager.ConfigureDingTalk(appID, secret, redirect)
					}
				}
			}
			if ch.Type == "wechatwork" && ch.Enabled {
				if corpID, _ := ch.Config["corp_id"].(string); corpID != "" {
					if secret, _ := ch.Config["corp_secret"].(string); secret != "" {
						redirect := fmt.Sprintf("http://localhost:%s/api/auth/sso/callback?provider=wechatwork", gatewayPort)
						g.ssoManager.ConfigureWeChatWork(corpID, secret, redirect)
					}
				}
			}
		}
	}
	if len(config.AIEmployees) > 0 {
		g.aeManager.Register(config.AIEmployees)
	}
	// Load web-managed DEs from DB; keep built-in DEs in sync for upgraded DBs.
	g.seedDefaultAEs()
	webDEs := g.loadAEsFromDB("")
	if len(webDEs) > 0 {
		g.aeManager.Register(webDEs)
		log.Printf("[AE] Loaded %d AI employees from DB", len(webDEs))
	}
	g.aeManager.loadWorkRecordsFromDB()
	g.initModelProviders(config)
	// Employee definitions, work records and model providers must exist before
	// pipeline recovery rebuilds execution identity. AgentOS-owned runs are only
	// loaded here; their durable queue lease will reclaim them exactly once.
	g.pipelineExec.RecoverOrphanExecutions()
	if len(config.MCPServers) > 0 {
		go func() {
			g.mcpManager.ConnectAll(context.Background(), config.MCPServers)
			engine.registerMCPTools(g.mcpManager)
		}()
	}

	// OTLP exporter
	if config.Gateway.OTLPEndpoint != "" {
		runevents.SetGlobalExporter(runevents.NewOTLPExporter(config.Gateway.OTLPEndpoint, "openkitty"))
		g.logger.Printf("OTLP exporter enabled: %s", config.Gateway.OTLPEndpoint)
	}

	// Knowledge Graph + Memory Deriver
	if config.MemoryConfig.Enabled {
		kgDir := filepath.Join(config.Workspace, ".openkitty")
		if kg, err := memorycore.NewKnowledgeGraph(kgDir); err == nil {
			globalKG = kg
			g.logger.Printf("Knowledge Graph initialized: %s", kgDir)
		} else {
			g.logger.Printf("[WARN] Knowledge Graph init failed: %v", err)
		}
	}

	// Auto-update checker
	if config.Gateway.UpdateURL != "" {
		uc := operationstools.NewUpdateChecker("", "", CurrentVersion)
		uc.SetCustomURL(config.Gateway.UpdateURL)
		uc.StartBackgroundChecker(6 * time.Hour)
		g.logger.Printf("自动更新已启用 (URL: %s)", config.Gateway.UpdateURL)
	}
	if indexed, err := indexInterruptedSessionJournals(g.db, config.Workspace, g.logger); err != nil {
		g.logger.Printf("[Recovery] 中断任务日志索引失败: %v", err)
	} else if indexed > 0 {
		g.logger.Printf("[Recovery] 已发现或还原 %d 个中断任务", indexed)
	}

	return g

}

func startConfiguredModelHealthCheck(configs map[string]ModelConfig, logger *log.Logger) chan struct{} {
	models := make([]ModelConfig, 0, len(configs))
	for _, name := range preferredModelConfigKeys(configs) {
		if model := configs[name]; model.APIKey != "" {
			models = append(models, model)
		}
	}
	if logger != nil && len(models) > 1 {
		logger.Printf("[Fallback] Default chain: %d models (%s -> %s)", len(models), models[0].ModelName, models[len(models)-1].ModelName)
	}
	return modelruntime.StartHealthCheck(5*time.Minute, func() {
		for _, model := range models {
			err := modelruntime.ProbeChatCompletion(context.Background(), &http.Client{Timeout: 5 * time.Second}, modelruntime.ChatEndpoint{
				APIKey: model.APIKey, ModelName: model.ModelName, BaseURL: model.BaseURL,
			})
			if err != nil && logger != nil {
				logger.Printf("[Health] %s: FAILED - %v", model.ModelName, err)
			}
		}
	})
}

func (g *GatewayService) newGatewayTaskContext() (context.Context, context.CancelFunc) {
	// Agent tasks have their own bounded LLM calls, tool timeouts and iteration
	// budgets. A gateway-wide deadline can expire between those operations and
	// turn a healthy long-running task into a misleading per-call timeout. Keep
	// only explicit lifecycle cancellation here (stop/restart/shutdown).
	return context.WithCancel(context.Background())
}

func (g *GatewayService) UpdateConfig(cfg *GatewayConfig) error {
	if cfg == nil {
		return fmt.Errorf("cfg is nil")
	}

	// 更新配置
	g.config.Gateway = *cfg
	g.logger.Printf("Gateway 配置已更新: %+v", cfg)
	return nil
}

// startCronScheduler starts background pipeline scheduling via cron expressions.

// registerAPICallTool adds the api_call tool to the global registry with DE-specific tokens.
func (g *GatewayService) registerAPICallTool(tokens map[string]string) {
	// Tool is registered globally, tokens are per-DE instance
	// The tool reads tokens from the tool instance at execution time
	if g.engine != nil && g.engine.agentFactory != nil {
		tool := NewAPICallTool(tokens)
		// Register through the first LLMAgent's toolRegistry
		g.engine.agentFactory.mu.RLock()
		for _, agent := range g.engine.agentFactory.agents {
			if llmAgent, ok := agent.(*LLMAgent); ok && llmAgent.toolRegistry != nil {
				if _, exists := llmAgent.toolRegistry.Get("api_call"); !exists {
					llmAgent.toolRegistry.Register(tool)
					g.logger.Printf("[API] Registered api_call tool with %d tokens", len(tokens))
				}
				break
			}
		}
		g.engine.agentFactory.mu.RUnlock()
	}
}

// handleWebhook routes external webhook events to DE dispatch.
// POST /api/webhook/{ae_id} with JSON body -> dispatched as task to the DE.
func (g *GatewayService) handleWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, `{"error":"POST only"}`, 405)
		return
	}
	deID := strings.TrimPrefix(r.URL.Path, "/api/webhook/")
	if deID == "" {
		http.Error(w, `{"error":"ae_id required in path"}`, 400)
		return
	}
	if g.aeManager == nil {
		http.Error(w, `{"error":"AE manager not available"}`, 500)
		return
	}
	if cfg, ok := g.aeManager.GetEmployee(deID); ok && cfg.InternalOnly {
		http.Error(w, `{"error":"employee capability has been integrated into @投资顾问"}`, http.StatusForbidden)
		return
	}

	var body map[string]interface{}
	json.NewDecoder(r.Body).Decode(&body)

	// Build task description from webhook payload
	task := fmt.Sprintf("Webhook event")
	if event, ok := body["event"].(string); ok {
		task = event
	}
	if data, ok := body["data"].(string); ok {
		task += ": " + data
	}
	g.assessAndLogGatewayUserContent("webhook", task)

	sessionID := fmt.Sprintf("wh_%s_%d", deID, time.Now().Unix())
	record, err := g.aeManager.Dispatch(context.Background(), deID, task, sessionID)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), 500)
		return
	}

	// Extract customer info from webhook if available
	if tenant, ok := body["tenant"].(string); ok {
		if channel, ok := body["channel"].(string); ok {
			if extID, ok := body["customer_id"].(string); ok {
				name, _ := body["customer_name"].(string)
				refs, _ := json.Marshal(body["external_refs"])
				projectctx.FindOrCreateCustomer(gatewayDB, tenant, channel, extID, name, string(refs))
			}
		}
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"ok": true, "exec_id": record.ExecID, "de": deID,
	})
}

func (g *GatewayService) startCronScheduler() {
	if schedulersDisabledByEnv() {
		if g.logger != nil {
			g.logger.Printf("[Cron] scheduler disabled by OPENKITTY_DISABLE_SCHEDULERS")
		}
		return
	}
	c := cron.New()
	// Register DE cron tasks
	if g.aeManager != nil {
		for id, cfg := range g.aeManager.employees {
			if cfg.InternalOnly {
				continue
			}
			if cfg.CronExpr != "" {
				employeeID := id
				config := cfg
				_, err := c.AddFunc(cfg.CronExpr, func() {
					g.aeManager.Dispatch(context.Background(), employeeID, "执行定时任务", "cron_"+employeeID)
				})
				if err != nil {
					g.logger.Printf("[Cron] Failed to register %s (expr=%s): %v", config.Name, cfg.CronExpr, err)
				} else {
					g.logger.Printf("[Cron] Scheduled %s every %s", config.Name, cfg.CronExpr)
				}
			}
		}
	}
	// Weekly health report (Monday 9:00 AM)
	c.AddFunc("0 9 * * 1", func() {
		g.SendWeeklyHealthReport()
	})
	c.Start()
}

func schedulersDisabledByEnv() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("OPENKITTY_DISABLE_SCHEDULERS"))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

// startConfigWatcher monitors config.yaml for changes and auto-reloads.

// startLogRotation cleans up log files older than 7 days.
func (g *GatewayService) startLogRotation() {
	go func() {
		for {
			time.Sleep(24 * time.Hour)
			cutoff := time.Now().Add(-7 * 24 * time.Hour)
			entries, _ := os.ReadDir("logs")
			cleaned := 0
			for _, e := range entries {
				if e.IsDir() {
					continue
				}
				info, err := e.Info()
				if err != nil {
					continue
				}
				if info.ModTime().Before(cutoff) {
					os.Remove(filepath.Join("logs", e.Name()))
					cleaned++
				}
			}
			if cleaned > 0 {
				g.logger.Printf("[LogRotation] Cleaned %d old log files", cleaned)
			}
		}
	}()
}

// startStorageCleanup removes AI-generated temp files older than 7 days.
func (g *GatewayService) startStorageCleanup() {
	go func() {
		for {
			time.Sleep(6 * time.Hour)
			cutoff := time.Now().Add(-7 * 24 * time.Hour)
			dirs := []string{"storage/charts", "storage/.system"}
			cleaned := 0
			for _, dir := range dirs {
				entries, err := os.ReadDir(dir)
				if err != nil {
					continue
				}
				for _, e := range entries {
					if e.IsDir() {
						continue
					}
					info, err := e.Info()
					if err != nil {
						continue
					}
					if info.ModTime().Before(cutoff) {
						os.Remove(filepath.Join(dir, e.Name()))
						cleaned++
					}
				}
			}
			if cleaned > 0 {
				g.logger.Printf("[Cleanup] Removed %d stale files from storage", cleaned)
			}
		}
	}()
}

// startDBBackup performs daily SQLite VACUUM + backup.
func (g *GatewayService) startDBBackup() {
	go func() {
		for {
			time.Sleep(24 * time.Hour)
			if g.db == nil {
				continue
			}
			// VACUUM to reclaim space
			g.db.Exec("VACUUM")
			// Copy to backup dir
			backupDir := "storage/.backup"
			os.MkdirAll(backupDir, 0755)
			backupPath := filepath.Join(backupDir, fmt.Sprintf("kclaw_%s.db", time.Now().Format("20060102")))
			src, err := os.Open("kclaw.db")
			if err != nil {
				g.logger.Printf("[Backup] Failed to open DB: %v", err)
				continue
			}
			dst, err := os.Create(backupPath)
			if err != nil {
				src.Close()
				g.logger.Printf("[Backup] Failed to create backup: %v", err)
				continue
			}
			io.Copy(dst, src)
			src.Close()
			dst.Close()
			g.logger.Printf("[Backup] DB backed up to %s", backupPath)
			// Keep only last 7 backups
			entries, _ := os.ReadDir(backupDir)
			var backups []string
			for _, e := range entries {
				if strings.HasPrefix(e.Name(), "kclaw_") {
					backups = append(backups, e.Name())
				}
			}
			if len(backups) > 7 {
				sort.Strings(backups)
				for _, old := range backups[:len(backups)-7] {
					os.Remove(filepath.Join(backupDir, old))
				}
			}
		}
	}()
}

func (g *GatewayService) startConfigWatcher() {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		g.logger.Printf("[ConfigWatcher] Failed to create: %v", err)
		return
	}
	watcher.Add(g.configpath)
	g.logger.Printf("[ConfigWatcher] Watching %s", g.configpath)

	go func() {
		defer watcher.Close()
		var debounce *time.Timer
		for {
			select {
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}
				if event.Op&fsnotify.Write == fsnotify.Write {
					if debounce != nil {
						debounce.Stop()
					}
					debounce = time.AfterFunc(1*time.Second, func() {
						g.logger.Printf("[ConfigWatcher] Config changed, reloading...")
						if err := g.ReloadConfig(); err != nil {
							g.logger.Printf("[ConfigWatcher] Reload failed: %v", err)
							eventbus.PushSystemMessage("Config reload failed: "+err.Error(), "error")
						} else {
							eventbus.PushSystemMessage("Config auto-reloaded", "success")
						}
					})
				}
			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}
				g.logger.Printf("[ConfigWatcher] Error: %v", err)
			}
		}
	}()
}

func (g *GatewayService) loadChannelsFromConfig() {
	if channelsDisabledByEnv() {
		g.logger.Printf("渠道加载已被 %s 禁用", envDisableChannels)
		return
	}
	if g.config.Channels == nil {
		return
	}

	for _, cfg := range g.config.Channels {
		if !cfg.Enabled {
			g.logger.Printf("渠道 %s 已禁用", cfg.Name)
			continue
		}

		var channel Channel
		var err error

		switch cfg.Type {
		case "feishu":
			appID := getString(cfg.Config, "app_id", "")
			appSecret := getString(cfg.Config, "app_secret", "")

			if err = validateRequiredFields(cfg.Name, map[string]string{
				"app_id":     appID,
				"app_secret": appSecret,
			}); err != nil {
				g.logger.Printf("渠道 %s 配置验证失败: %v", cfg.Name, err)
				continue
			}

			feishuConfig := channelhost.FeishuChannelConfig{
				AppID:     appID,
				AppSecret: appSecret,
			}
			channel = channelhost.NewFeishuChannel(cfg.Name, feishuConfig, g.config.Workspace)

		case "dingtalk":
			clientID := getString(cfg.Config, "client_id", "")
			clientSecret := getString(cfg.Config, "client_secret", "")
			robotCode := getString(cfg.Config, "robot_code", "")

			if err = validateRequiredFields(cfg.Name, map[string]string{
				"client_id":     clientID,
				"client_secret": clientSecret,
				"robot_code":    robotCode,
			}); err != nil {
				g.logger.Printf("渠道 %s 配置验证失败: %v", cfg.Name, err)
				continue
			}

			dingtalkConfig := channelhost.DingTalkChannelConfig{
				ClientID:     clientID,
				ClientSecret: clientSecret,
				RobotCode:    robotCode,
			}
			channel = channelhost.NewDingTalkChannel(cfg.Name, dingtalkConfig, g.config.Workspace)

		case "qq":
			appID := getString(cfg.Config, "app_id", "")
			appSecret := getString(cfg.Config, "app_secret", "")

			if err = validateRequiredFields(cfg.Name, map[string]string{
				"app_id":     appID,
				"app_secret": appSecret,
			}); err != nil {
				g.logger.Printf("渠道 %s 配置验证失败: %v", cfg.Name, err)
				continue
			}

			qqConfig := channelhost.QQChannelConfig{
				AppID:     appID,
				AppSecret: appSecret,
			}
			channel = channelhost.NewQQChannel(cfg.Name, qqConfig)

		case "wework":
			botID := getString(cfg.Config, "bot_id", "")
			secret := getString(cfg.Config, "secret", "")

			if err = validateRequiredFields(cfg.Name, map[string]string{
				"bot_id": botID,
				"secret": secret,
			}); err != nil {
				g.logger.Printf("渠道 %s 配置验证失败: %v", cfg.Name, err)
				continue
			}

			weworkConfig := channelhost.WeWorkChannelConfig{
				BotID:  botID,
				Secret: secret,
			}
			channel = channelhost.NewWeWorkChannel(cfg.Name, weworkConfig, g.config.Workspace)
		case "email": // 新增邮箱渠道
			smtpHost := getString(cfg.Config, "smtp_host", "")
			smtpPort := getInt(cfg.Config, "smtp_port", 465)
			imapHost := getString(cfg.Config, "imap_host", "")
			imapPort := getInt(cfg.Config, "imap_port", 993)
			username := getString(cfg.Config, "username", "")
			password := getString(cfg.Config, "password", "")
			fromAddress := getString(cfg.Config, "from_address", "")
			fromName := getString(cfg.Config, "from_name", "AI助手")
			useTLS := getBool(cfg.Config, "use_tls", true)
			checkInterval := getInt(cfg.Config, "check_interval", 30)
			monitorFolder := getString(cfg.Config, "monitor_folder", "INBOX")

			if err = validateRequiredFields(cfg.Name, map[string]string{
				"smtp_host":    smtpHost,
				"imap_host":    imapHost,
				"username":     username,
				"password":     password,
				"from_address": fromAddress,
			}); err != nil {
				g.logger.Printf("渠道 %s 配置验证失败: %v", cfg.Name, err)
				continue
			}

			emailConfig := channelhost.EmailChannelConfig{
				Enabled:       true,
				SMTPHost:      smtpHost,
				SMTPPort:      smtpPort,
				IMAPHost:      imapHost,
				IMAPPort:      imapPort,
				Username:      username,
				Password:      password,
				FromAddress:   fromAddress,
				FromName:      fromName,
				UseTLS:        useTLS,
				CheckInterval: checkInterval,
				MonitorFolder: monitorFolder,
			}
			channel = channelhost.NewEmailChannel(cfg.Name, emailConfig)

		case "telegram":
			botToken := getString(cfg.Config, "bot_token", "")
			if err = validateRequiredFields(cfg.Name, map[string]string{
				"bot_token": botToken,
			}); err != nil {
				g.logger.Printf("渠道 %s 配置验证失败: %v", cfg.Name, err)
				continue
			}
			tgConfig := channelcore.TelegramChannelConfig{BotToken: botToken}
			channel = channelcore.NewTelegramChannel(cfg.Name, tgConfig, g.config.Workspace)

		case "weixin", "openclaw-weixin":
			channel = channelhost.NewWeixinChannelFromMap(cfg.Name, cfg.Config, g.config.Workspace)

		default:
			g.logger.Printf("不支持的渠道类型: %s", cfg.Type)
			continue
		}

		if channel != nil {
			if err := g.channelManager.RegisterChannel(channel); err != nil {
				g.logger.Printf("注册渠道 %s 失败: %v", cfg.Name, err)
			} else {
				g.logger.Printf("渠道 %s 注册成功", cfg.Name)
			}
		}
	}
}

// validateRequiredFields 验证必需字段不为空
const envDisableChannels = "OPENKITTY_DISABLE_CHANNELS"

func channelsDisabledByEnv() bool {
	v := strings.TrimSpace(os.Getenv(envDisableChannels))
	if v == "" {
		return false
	}
	switch strings.ToLower(v) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func validateRequiredFields(channelName string, fields map[string]string) error {
	var emptyFields []string
	for fieldName, fieldValue := range fields {
		if fieldValue == "" {
			emptyFields = append(emptyFields, fieldName)
		}
	}

	if len(emptyFields) > 0 {
		return fmt.Errorf("渠道 %s 缺少必需配置: %v", channelName, emptyFields)
	}
	return nil
}

func (g *GatewayService) findAvailablePort(startPort int) int {
	return g.findAvailablePortOnHost("", startPort)
}

func (g *GatewayService) findAvailablePortOnHost(host string, startPort int) int {
	for port := startPort; port < startPort+100; port++ {
		addr := net.JoinHostPort(host, strconv.Itoa(port))
		listener, err := net.Listen("tcp", addr)
		if err == nil {
			listener.Close()
			if port != startPort {
				fmt.Printf("🚨 端口 %d 已被占用，自动切换到: %d\n", startPort, port)
			}
			return port
		}
	}
	return 0
}

func gatewayListenHostAndPort(listenAddr string, fallbackPort int) (string, int) {
	host := "127.0.0.1"
	port := fallbackPort
	listenAddr = strings.TrimSpace(listenAddr)
	if listenAddr == "" {
		return host, port
	}
	if parsedHost, parsedPort, err := net.SplitHostPort(listenAddr); err == nil {
		if parsedHost != "" {
			host = parsedHost
		}
		if parsed, err := strconv.Atoi(parsedPort); err == nil && parsed > 0 && parsed <= 65535 {
			port = parsed
		}
		return host, port
	}
	if parsed, err := strconv.Atoi(listenAddr); err == nil && parsed > 0 && parsed <= 65535 {
		port = parsed
	}
	return host, port
}

// openBrowser 优先尝试打开 Chrome，失败则回退到系统默认浏览器
func openBrowser(url string) error {
	// 定义各系统优先的 Chrome 命令
	var chromeCommands [][]string
	// 定义系统默认浏览器命令
	var fallbackCommands map[string][]string

	switch runtime.GOOS {
	case "linux":
		chromeCommands = [][]string{
			{"xdg-open", url}, // 系统默认，复用已有窗口
		}
		fallbackCommands = map[string][]string{
			"google-chrome":        {url},
			"google-chrome-stable": {url},
			"chromium":             {url},
			"x-www-browser":        {url},
		}
	case "windows":
		chromeCommands = [][]string{
			{"cmd", "/c", "start", "", url}, // 系统处理，复用已有标签页
		}
		fallbackCommands = map[string][]string{
			"rundll32": {"url.dll,FileProtocolHandler", url},
		}
	case "darwin":
		chromeCommands = [][]string{
			{"open", url}, // 系统默认，复用已有窗口
		}
		fallbackCommands = map[string][]string{
			"chrome": {"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome", url},
		}
	default:
		return fmt.Errorf("unsupported platform: %s", runtime.GOOS)
	}

	// 第一步：优先尝试打开 Chrome
	for _, cmdArgs := range chromeCommands {
		if len(cmdArgs) < 1 {
			continue
		}
		cmd := exec.Command(cmdArgs[0], cmdArgs[1:]...)
		if err := cmd.Start(); err == nil {
			return nil // Chrome 打开成功，直接返回
		}
		// Chrome 命令执行失败，继续尝试下一个 Chrome 路径/命令
	}

	// 第二步：Chrome 打开失败，回退到系统默认浏览器
	for cmd, args := range fallbackCommands {
		err := exec.Command(cmd, args...).Start()
		if err == nil {
			return nil // 系统默认浏览器打开成功
		}
	}

	// 所有命令都失败
	return fmt.Errorf("no compatible browser command found (Chrome and default browsers all failed)")
}

func getLocalIP() (string, error) {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return "", err
	}

	for _, addr := range addrs {
		if ipnet, ok := addr.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
			// 只处理IPv4地址
			if ipnet.IP.To4() != nil {
				ip := ipnet.IP.String()

				// 排除APIPA地址 (169.254.x.x)
				if ipnet.IP.IsLinkLocalUnicast() {
					continue
				}

				// 排除虚拟机网络（可选）
				if strings.HasPrefix(ip, "192.168.98.") ||
					strings.HasPrefix(ip, "192.168.182.") {
					continue
				}

				// 优先选择无线网络适配器的地址
				if strings.HasPrefix(ip, "192.168.1.") {
					return ip, nil
				}

				return ip, nil
			}
		}
	}

	return "", fmt.Errorf("no valid local IP found")
}

func gatewayWebUIURL(listenHost string, port int) string {
	host := strings.TrimSpace(listenHost)
	if host == "" {
		host = "127.0.0.1"
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsUnspecified() {
		if localIP, err := getLocalIP(); err == nil && strings.TrimSpace(localIP) != "" {
			host = localIP
		} else {
			host = "127.0.0.1"
		}
	}
	return "http://" + net.JoinHostPort(host, strconv.Itoa(port)) + "/web"
}

func shouldAutoOpenGatewayBrowser(config *Config) bool {
	return config != nil && !config.Headless
}

type ChannelMessage struct {
	Channel   string
	UserID    string
	Recipient string
	MessageID string
	Content   string
	Metadata  map[string]interface{}
}

func (g *GatewayService) handleChannelMessage(msg *ChannelMessage) {
	if msg == nil {
		return
	}
	g.assessAndLogGatewayUserContent("channel:"+msg.Channel, msg.Content)
	g.logger.Printf("收到渠道消息: channel=%s, user=%s, content_length=%d",
		msg.Channel, msg.UserID, len([]rune(msg.Content)))

	// 获取或创建会话
	sessionID := fmt.Sprintf("%s_%s", msg.Channel, msg.UserID)
	taskID := g.nextSessionTaskID(sessionID)

	// 构建任务请求
	req := MessageRequest{
		TaskID:    taskID,
		TaskType:  "chat",
		SessionId: sessionID,
		Message:   msg.Content,
		AgentName: "",
		Channel:   msg.Channel,
		UserId:    msg.UserID,
		CreatedAt: time.Now(),
	}

	// 记录用户消息
	g.saveChatMessage(sessionID, "user", "", msg.Content, "pending", taskID, msg.Channel, msg.UserID)

	// 创建任务
	task := &GatewayTask{
		ID:        taskID,
		Request:   &req,
		Status:    "pending",
		CreatedAt: time.Now(),
		ResultCh:  make(chan interface{}, 1),
	}

	g.tasksMu.Lock()
	if g.tasks == nil {
		g.tasks = make(map[string]*GatewayTask)
	}
	g.tasks[taskID] = task
	g.tasksMu.Unlock()

	// 异步处理
	go g.processChannelMessage(task, msg)
}

func (g *GatewayService) processChannelMessage(task *GatewayTask, msg *ChannelMessage) {
	if task == nil {
		return
	}
	if task.ResultCh != nil {
		defer close(task.ResultCh)
	}

	sendResult := func(status string, result interface{}, err error) {
		task.Status = status
		task.Result = result
		task.Error = err
		now := time.Now()
		task.CompletedAt = &now
		if g.db != nil {
			if saveErr := SaveGatewayTaskToDB(g.db, task); saveErr != nil && g.logger != nil {
				g.logger.Printf("保存渠道任务状态失败: %v", saveErr)
			}
		}
		g.broadcastTaskUpdate(task.ID, status, result, err)
		if task.ResultCh == nil {
			return
		}
		response := GatewayResponse{
			TaskID:    task.ID,
			Status:    status,
			Result:    result,
			Timestamp: time.Now(),
		}
		if err != nil {
			response.Error = err.Error()
		}
		select {
		case task.ResultCh <- response:
		default:
		}
	}

	if task.Request == nil {
		err := fmt.Errorf("missing task request")
		sendResult("failed", nil, err)
		g.sendChannelError(msg, fmt.Sprintf("处理失败: %v", err))
		return
	}

	task.Status = "running"
	if g.db != nil {
		if err := SaveGatewayTaskToDB(g.db, task); err != nil && g.logger != nil {
			g.logger.Printf("保存渠道任务运行状态失败: %v", err)
		}
	}
	g.broadcastTaskUpdate(task.ID, "running", nil, nil)

	// 获取 Agent
	var agent Agent
	var agentName string

	if g.engine == nil || g.engine.agentFactory == nil {
		err := fmt.Errorf("没有可用的 Agent")
		sendResult("failed", nil, err)
		g.sendChannelError(msg, err.Error())
		return
	}

	var ok bool
	agentName, agent, ok = g.engine.DefaultAgent()
	if !ok {
		err := fmt.Errorf("没有可用的 Agent")
		sendResult("failed", nil, err)
		g.sendChannelError(msg, err.Error())
		return
	}

	// 生成回复
	ctx := context.WithValue(context.Background(), "task_id", task.ID)
	ctx = context.WithValue(ctx, "session_id", task.Request.SessionId)
	ctx = context.WithValue(ctx, "channel", task.Request.Channel)
	ctx = context.WithValue(ctx, "user_id", task.Request.UserId)

	g.ObserveTaskRuntime(task.ID, "model", "")
	result, err := agent.Generate(ctx, task.Request.Message)

	if err != nil {
		failureOutput := agentresult.FailureUserMessage(result, err)
		channel := task.Request.Channel
		userID := task.Request.UserId
		if msg != nil {
			channel = msg.Channel
			userID = msg.UserID
		}
		sendResult("failed", failureOutput, err)
		g.saveChatMessage(task.Request.SessionId, "assistant", agentName,
			failureOutput, "failed", task.ID, channel, userID)
		g.sendChannelError(msg, failureOutput)
		return
	}

	// 保存 AI 响应
	channel := task.Request.Channel
	userID := task.Request.UserId
	if msg != nil {
		channel = msg.Channel
		userID = msg.UserID
	}
	g.saveChatMessage(task.Request.SessionId, "assistant", agentName,
		fmt.Sprintf("%v", result), "completed", task.ID, channel, userID)
	sendResult("completed", result, nil)

	// 发送回复
	g.sendChannelReply(msg, fmt.Sprintf("%v", result))
}

func (g *GatewayService) sendChannelReply(msg *ChannelMessage, content string) {
	if msg == nil {
		return
	}
	if g.channelManager == nil {
		if g.logger != nil {
			g.logger.Printf("渠道管理器未初始化，无法发送渠道消息: channel=%s", msg.Channel)
		}
		return
	}
	// 通过渠道发送消息
	channel, ok := g.channelManager.GetChannel(msg.Channel)
	if !ok {
		if g.logger != nil {
			g.logger.Printf("渠道 %s 不存在", msg.Channel)
		}
		return
	}

	// 使用统一渠道投递，支持文本与图片 artifact 分流
	if err := sendChannelContent(g, channel, msg.Recipient, content, g.logger); err != nil {
		if g.logger != nil {
			g.logger.Printf("发送消息失败: %v", err)
		}
	}
}

func (g *GatewayService) sendChannelError(msg *ChannelMessage, errMsg string) {
	g.sendChannelReply(msg, errMsg)
}

func (g *GatewayService) beginGatewayBackground() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	g.backgroundMu.Lock()
	previous := g.backgroundCancel
	g.backgroundWG.Add(3)
	g.backgroundCancel = cancel
	g.backgroundMu.Unlock()
	if previous != nil {
		previous()
	}
	return ctx
}

func (g *GatewayService) startGatewayBackground(ctx context.Context) {
	go func() {
		defer g.backgroundWG.Done()
		g.runMemoryReviews(ctx)
	}()
	go func() {
		defer g.backgroundWG.Done()
		g.runBackgroundUpdateCheck(ctx, 6*time.Hour)
	}()
	go func() {
		defer g.backgroundWG.Done()
		g.cleanupTasks(ctx)
	}()
}

func (g *GatewayService) stopGatewayBackground(ctx context.Context) {
	g.backgroundMu.Lock()
	cancel := g.backgroundCancel
	g.backgroundCancel = nil
	g.backgroundMu.Unlock()
	if cancel == nil {
		return
	}
	cancel()
	done := make(chan struct{})
	go func() {
		g.backgroundWG.Wait()
		close(done)
	}()
	if ctx == nil {
		<-done
		return
	}
	select {
	case <-done:
	case <-ctx.Done():
	}
}

func resetAgentOSQueueAfterFailedGatewayStart(queue *agentOSQueueRuntime) {
	if queue == nil {
		return
	}
	queue.mu.Lock()
	queue.started = false
	queue.ctx = nil
	queue.cancel = nil
	queue.mu.Unlock()
	queue.draining.Store(false)
	queue.accepting.Store(false)
	for {
		select {
		case <-queue.wake:
		default:
			return
		}
	}
}

// rollbackFailedGatewayStart unwinds only resources acquired by Start. It is
// intentionally separate from Stop: a normal Stop retains its established
// lifecycle semantics, while a failed Start leaves the service retryable.
func (g *GatewayService) rollbackFailedGatewayStart(queueStarted, channelsStarted bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	queue := g.ensureAgentOSQueue()
	if queueStarted {
		queue.BeginDrain()
	}
	g.stopGatewayBackground(ctx)
	if channelsStarted && g.channelManager != nil {
		_ = g.channelManager.StopAll()
	}
	if g.agentOSHTTPServer != nil {
		if err := g.agentOSHTTPServer.Shutdown(ctx); err != nil {
			_ = g.agentOSHTTPServer.Close()
		}
		g.agentOSHTTPServer = nil
	}
	if g.httpServer != nil {
		if err := g.httpServer.Shutdown(ctx); err != nil {
			_ = g.httpServer.Close()
		}
		g.httpServer = nil
	}
	if queueStarted {
		queue.Stop(ctx)
		resetAgentOSQueueAfterFailedGatewayStart(queue)
	}
}

// Start 启动Gateway服务
func (g *GatewayService) Start() error {
	queueStarted := false
	channelsStarted := false
	rollbackRequired := true
	defer func() {
		if rollbackRequired {
			g.rollbackFailedGatewayStart(queueStarted, channelsStarted)
		}
	}()

	// 初始化更新检查
	updateURL := ""
	if g.config.Gateway.UpdateURL != "" {
		updateURL = g.config.Gateway.UpdateURL
	} else {
		// Auto-detect update URL from Sicurrent based on platform
		platform := runtime.GOOS
		// Map Go arch names to product config names
		archName := runtime.GOARCH
		switch archName {
		case "amd64":
			archName = "x64"
		case "386":
			archName = "x86"
		}
		updateURL = fmt.Sprintf("https://www.sicurrent.com/nova/products/openkitty?platform=%s&arch=%s&latest=true", platform, archName)
	}
	g.updateChecker = operationstools.NewUpdateChecker("openkitty-ai", "openkitty", CurrentVersion)
	if updateURL != "" {
		g.updateChecker.SetCustomURL(updateURL)
	}

	// Desktop mode may find a nearby free port. Containers and Kubernetes must
	// fail fast instead: a silent port change would make Service probes target
	// the wrong socket while the process appeared healthy in its own logs.
	listenHost, defaultPort := gatewayListenHostAndPort(g.config.Gateway.ListenAddr, 8080)
	if fixedGatewayPort() {
		g.port = defaultPort
	} else {
		g.port = g.findAvailablePortOnHost(listenHost, defaultPort)
	}
	if g.port == 0 {
		return fmt.Errorf("无法找到可用端口")
	}

	addr := net.JoinHostPort(listenHost, strconv.Itoa(g.port))
	fmt.Printf("🌐 [Gateway] 启动 Web 服务，监听地址: %s\n", addr)
	g.logger.Printf("使用监听地址: %s", addr)

	mux := http.NewServeMux()

	// CORS 包装器
	corsHandler := func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			origin := strings.TrimSpace(r.Header.Get("Origin"))
			if origin != "" {
				if !gatewayhttp.RequestHasNoOriginOrSameHost(r) {
					http.Error(w, "Forbidden origin", http.StatusForbidden)
					return
				}
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Vary", "Origin")
			}
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-API-Key, X-Auth-Token, X-Caller-ID, X-OpenKitty-Agent, Idempotency-Key")
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusOK)
				return
			}
			next(w, r)
		}
	}

	// API路由
	mux.HandleFunc("/api/chat", corsHandler(gatewayhttp.RateLimitMiddleware(apiRateLimiter, g.handleChat)))
	agentOSDataHandler := g.agentOSDataHandler()
	if strings.TrimSpace(g.config.Gateway.AgentOSListenAddr) == "" {
		mountAgentOSDataPlaneOnMainMux(mux, agentOSDataHandler)
	}
	mux.HandleFunc("/api/task", corsHandler(g.handleTask))
	mux.HandleFunc("/api/task/", corsHandler(g.handleTaskStatus))
	mux.HandleFunc("/api/tasks", corsHandler(g.handleListTasks))
	mux.HandleFunc("/api/task/stop", corsHandler(g.handleStopTask))
	mux.HandleFunc("/api/task/resume", corsHandler(g.handleResumeTask))
	mux.HandleFunc("/api/interrupt", corsHandler(g.handleInterrupt))
	mux.HandleFunc("/api/agents", corsHandler(g.handleListAgents))
	mux.HandleFunc("/api/models", corsHandler(g.handleComposerModels))
	mux.HandleFunc("/api/health", corsHandler(g.handleHealth))
	mux.HandleFunc("/livez", g.handleLivez)
	mux.HandleFunc("/readyz", g.handleReadyz)
	mux.HandleFunc("/metrics", g.handleAgentOSMetrics)
	mux.HandleFunc("/api/status", corsHandler(g.handleAPIStatus))
	mux.HandleFunc("/api/collaboration", corsHandler(g.handleCollaboration))
	mux.HandleFunc("/api/sessions", corsHandler(g.handleListSessions))
	mux.HandleFunc("/api/recoveries", corsHandler(g.handleListRecoveries))
	mux.HandleFunc("/api/session/", corsHandler(g.handleSessionMessages))
	mux.HandleFunc("/api/session/export/", corsHandler(g.handleSessionExport))
	mux.HandleFunc("/api/config", corsHandler(g.handleConfig))
	mux.HandleFunc("/api/config/reload", corsHandler(g.handleConfigReload))
	mux.HandleFunc("/api/ui/language", corsHandler(func(w http.ResponseWriter, r *http.Request) {
		gatewayhttp.HandleUILanguage(w, r, g.db)
	}))
	mux.HandleFunc("/api/files/list", corsHandler(g.handleListFiles))
	mux.HandleFunc("/api/files/download", corsHandler(g.handleDownloadFile))
	mux.HandleFunc("/api/artifacts", corsHandler(g.handleArtifacts))
	mux.HandleFunc("/api/upload", corsHandler(g.handleUpload))

	mux.HandleFunc("/api/todo/create", corsHandler(g.handleCreateTodo))
	mux.HandleFunc("/api/todo/list", corsHandler(g.handleListTodos))
	mux.HandleFunc("/api/todo/", corsHandler(g.handleTodoByID))
	mux.HandleFunc("/api/agents/names", corsHandler(g.handleGetAllAgentsNames))

	mux.HandleFunc("/api/task/get/", corsHandler(g.handleGetTask))
	mux.HandleFunc("/api/task/stop/", corsHandler(g.handleStopTaskByID))
	mux.HandleFunc("/api/task/pause/", corsHandler(g.handlePauseTaskByID))
	mux.HandleFunc("/api/task/progress-log/", corsHandler(g.handleProgressLogDownload))
	mux.HandleFunc("/api/tasks/stop/batch", corsHandler(g.handleBatchStopTasks))
	mux.HandleFunc("/api/tasks/running", corsHandler(g.handleListRunningTasks))
	mux.HandleFunc("/api/events", corsHandler(g.handleSSE))

	mux.HandleFunc("/api/logs", corsHandler(g.handleGetLogs))
	mux.HandleFunc("/api/shell/jobs", corsHandler(g.handleShellJobs))
	mux.HandleFunc("/api/shell/jobs/log", corsHandler(g.handleShellJobLog))
	mux.HandleFunc("/api/shell/jobs/stop", corsHandler(g.handleShellJobStop))
	mux.HandleFunc("/api/shell/jobs/download", corsHandler(g.handleShellJobDownload))
	mux.HandleFunc("/api/config/health", corsHandler(g.handleConfigHealth))
	mux.HandleFunc("/api/config/sections", corsHandler(g.handleConfigSections))
	mux.HandleFunc("/api/team-templates", corsHandler(g.handleTeamTemplates))
	mux.HandleFunc("/api/config/model-test", corsHandler(g.handleSettingsModelTest))
	mux.HandleFunc("/api/config/model-catalog", corsHandler(g.handleSettingsModelCatalog))
	mux.HandleFunc("/api/config/hotreload", corsHandler(g.handleConfigReload)) // 别名
	mux.HandleFunc("/api/weixin/login/start", corsHandler(g.handleWeixinLoginStart))
	mux.HandleFunc("/api/weixin/login/wait", corsHandler(g.handleWeixinLoginWait))
	mux.HandleFunc("/api/weixin/status", corsHandler(g.handleWeixinStatus))
	mux.HandleFunc("/api/ws", g.handleWebSocket) // WebSocket

	// Pipeline endpoints
	mux.HandleFunc("/api/pipeline/run", corsHandler(g.handlePipelineRun))
	mux.HandleFunc("/api/pipeline/approve", corsHandler(g.handlePipelineApprove))
	mux.HandleFunc("/api/pipeline/pause", corsHandler(g.handlePipelinePause))
	mux.HandleFunc("/api/pipeline/resume", corsHandler(g.handlePipelineResume))
	mux.HandleFunc("/api/pipeline/status/", corsHandler(g.handlePipelineStatus))
	mux.HandleFunc("/api/pipeline/list", corsHandler(g.handlePipelineList))
	mux.HandleFunc("/api/pipeline/save", corsHandler(g.handlePipelineSave))
	mux.HandleFunc("/api/pipeline/executions", corsHandler(g.handlePipelineExecutions))
	mux.HandleFunc("/api/pipeline/log/download", corsHandler(g.handlePipelineLogDownload))
	mux.HandleFunc("/api/knowledge/list", corsHandler(g.handleKnowledgeList))
	mux.HandleFunc("/api/knowledge/save", corsHandler(g.handleKnowledgeSave))
	mux.HandleFunc("/api/knowledge/upload", corsHandler(g.handleKnowledgeUpload))
	mux.HandleFunc("/api/knowledge/", corsHandler(g.handleKnowledgeDelete))
	mux.HandleFunc("/api/ae/export", corsHandler(authcore.TenantSessionMiddleware(g.handleAEExport)))
	mux.HandleFunc("/api/tenant/stats", corsHandler(authcore.TenantSessionMiddleware(g.handleTenantStats)))
	mux.HandleFunc("/api/messages", corsHandler(g.handleMessages))
	mux.HandleFunc("/api/system/shutdown", corsHandler(g.handleShutdown))
	mux.HandleFunc("/api/state", corsHandler(g.handleSharedState))
	mux.HandleFunc("/api/state/", corsHandler(g.handleSharedState))
	mux.HandleFunc("/api/agui", corsHandler(g.aguiSSEHandler))
	mux.HandleFunc("/api/system/confirm", corsHandler(g.handleConfirmAction))
	documentHandler := documentapi.NewHandler(documentapi.Dependencies{
		ExtractPDF:  ragExtractPDF,
		ExtractDOCX: ragExtractDocxGo,
		ExtractXLSX: ragExtractXLSX,
	})
	mux.HandleFunc("/api/document/analyze", corsHandler(documentHandler.HandleAnalyze))
	mux.HandleFunc("/api/document/classify", corsHandler(documentHandler.HandleClassify))
	mux.HandleFunc("/api/document-automation/inspect", corsHandler(g.handleDocumentAutomationInspect))
	mux.HandleFunc("/api/document-automation/generate", corsHandler(g.handleDocumentAutomationGenerate))
	mux.HandleFunc("/api/document-automation/profiles", corsHandler(g.handleDocumentAutomationProfiles))
	mux.HandleFunc("/api/memory", corsHandler(g.handlePersonalMemory))
	mux.HandleFunc("/api/memory/export", corsHandler(g.handlePersonalMemory))
	mux.HandleFunc("/api/memory/recalls", corsHandler(g.handlePersonalMemory))
	mux.HandleFunc("/api/memory/catalog", corsHandler(g.handlePersonalMemory))
	mux.HandleFunc("/api/memory/inputs", corsHandler(g.handlePersonalMemoryInputs))
	mux.HandleFunc("/api/memory/prompt-sources", corsHandler(g.handlePersonalPromptSources))
	mux.HandleFunc("/api/webhook/", corsHandler(g.handleWebhook))
	mux.HandleFunc("/api/ae/list", corsHandler(authcore.TenantSessionMiddleware(g.handleAEList)))
	mux.HandleFunc("/api/ae/readiness", corsHandler(authcore.TenantSessionMiddleware(g.handleAEReadiness)))
	mux.HandleFunc("/api/ae/detail", corsHandler(authcore.TenantSessionMiddleware(g.handleAEDetail)))

	mux.HandleFunc("/api/pipeline/status", corsHandler(authcore.TenantSessionMiddleware(g.handlePipelineStatus)))
	mux.HandleFunc("/api/rag/health", corsHandler(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		var kbID uint
		if idStr := r.URL.Query().Get("kb_id"); idStr != "" {
			if id, err := strconv.Atoi(idStr); err == nil {
				kbID = uint(id)
			}
		}
		json.NewEncoder(w).Encode(ragcore.RAGHealth(kbID))
	}))
	mux.HandleFunc("/api/rag/", corsHandler(authcore.TenantSessionMiddleware(g.handleRAG)))
	mux.HandleFunc("/api/admin/login", corsHandler(g.handleAdminLogin))
	mux.HandleFunc("/api/admin/verify", corsHandler(g.handleAdminVerify))
	tenantHandler := authcore.NewTenantHandler(g.db, g.tenantIsLicensed)
	mux.HandleFunc("/api/auth/login", corsHandler(tenantHandler.HandleLogin))
	mux.HandleFunc("/api/auth/logout", corsHandler(tenantHandler.HandleLogout))
	mux.HandleFunc("/api/auth/me", corsHandler(tenantHandler.HandleMe))
	mux.HandleFunc("/api/auth/users", corsHandler(tenantHandler.HandleUsers))
	mux.HandleFunc("/api/auth/tenants", corsHandler(g.handleTenantList))
	mux.HandleFunc("/api/auth/sso/providers", corsHandler(g.ssoManager.HandleProviders))
	mux.HandleFunc("/api/auth/sso/login", corsHandler(g.ssoManager.HandleLogin))
	mux.HandleFunc("/api/auth/sso/callback", corsHandler(g.ssoManager.HandleCallback))
	mux.HandleFunc("/api/auth/sso/verify", corsHandler(g.ssoManager.HandleVerify))
	accountConfig := authcore.AccountConfig{}
	if g.config != nil {
		accountConfig = authcore.AccountConfig{
			ServiceURL:  g.config.Gateway.AccountServiceURL,
			RegisterURL: g.config.Gateway.AccountRegisterURL,
			CenterURL:   g.config.Gateway.AccountCenterURL,
		}
	}
	accountHandler := authcore.NewAccountHandler(accountConfig)
	g.accountHandler = accountHandler
	mux.HandleFunc("/api/account/config", corsHandler(accountHandler.HandleConfig))
	mux.HandleFunc("/api/account/login", corsHandler(accountHandler.HandleLogin))
	mux.HandleFunc("/api/account/me", corsHandler(accountHandler.HandleMe))
	mux.HandleFunc("/api/account/summary", corsHandler(accountHandler.HandleSummary))
	mux.HandleFunc("/api/account/logout", corsHandler(accountHandler.HandleLogout))
	mux.HandleFunc("/api/community/feed", corsHandler(communitycore.Handler))
	mux.HandleFunc("/api/community/post", corsHandler(communitycore.PostHandler))
	mux.HandleFunc("/api/platform/models", corsHandler(g.handlePlatformModelCatalog))
	mux.HandleFunc("/api/platform/media/", corsHandler(accountHandler.HandlePlatformMedia))
	mux.HandleFunc("/api/conversation/media", corsHandler(g.handleConversationMedia))
	mux.HandleFunc("/api/conversation/media/", corsHandler(g.handleConversationMedia))
	mux.HandleFunc("/api/robot/status", corsHandler(g.handleRobotStatus))
	mux.HandleFunc("/api/robot/command", corsHandler(g.handleRobotCommand))
	mux.HandleFunc("/api/robot/stop", corsHandler(g.handleRobotStop))
	mux.HandleFunc("/api/pipeline/stop", corsHandler(g.handlePipelineStop))
	mux.HandleFunc("/api/sessions/search", corsHandler(g.handleSessionSearch))
	mux.HandleFunc("/api/pipeline/evolve", corsHandler(g.handlePipelineEvolve))
	mux.HandleFunc("/api/ae/worklog", corsHandler(g.handleAEWorkLog))
	mux.HandleFunc("/api/deliveries", corsHandler(g.handleDeliveries))
	mux.HandleFunc("/api/delivery/summary", corsHandler(g.handleDeliverySummary))
	mux.HandleFunc("/api/browser/tasks", corsHandler(g.handleBrowserTasks))
	mux.HandleFunc("/api/browser/task", corsHandler(g.handleBrowserTaskDetail))
	mux.HandleFunc("/api/desktop/tasks", corsHandler(g.handleDesktopTasks))
	mux.HandleFunc("/api/desktop/task", corsHandler(g.handleDesktopTaskDetail))
	mux.HandleFunc("/api/ae/save", corsHandler(g.handleAESave))
	mux.HandleFunc("/api/handoff/list", corsHandler(g.handleHandoffList))
	mux.HandleFunc("/api/handoff/claim", corsHandler(g.handleHandoffClaim))
	mux.HandleFunc("/api/handoff/complete", corsHandler(g.handleHandoffComplete))
	mux.HandleFunc("/api/handoff/close", corsHandler(g.handleHandoffClose))
	mux.HandleFunc("/api/ae/delete", corsHandler(g.handleAEDelete))

	// Siri-like voice wake endpoints
	mux.HandleFunc("/api/wake/start", g.handleWakeStart)
	mux.HandleFunc("/api/wake/stop", g.handleWakeStop)
	mux.HandleFunc("/api/wake/status", g.handleWakeStatus)

	// Cache monitoring endpoints
	mux.HandleFunc("/api/monitor/cache", corsHandler(g.handleMonitorCache))
	mux.HandleFunc("/api/monitor/cache/history", corsHandler(g.handleMonitorCacheHistory))
	mux.HandleFunc("/api/monitor/cache/prompt", corsHandler(g.handleMonitorCachePrompt))
	mux.HandleFunc("/api/monitor/cache/config", corsHandler(g.handleCacheConfig))
	mux.HandleFunc("/api/trace/", corsHandler(g.handleTaskTrace))

	// Skill management endpoints
	mux.HandleFunc("/api/skills", g.handleSkillList)
	mux.HandleFunc("/api/skills/sources", corsHandler(g.handleRemoteSkillSources))
	mux.HandleFunc("/api/skills/search", corsHandler(g.handleRemoteSkillSearch))
	mux.HandleFunc("/api/skills/install", corsHandler(g.handleRemoteSkillInstall))
	mux.HandleFunc("/api/skill/install", g.handleSkillInstall)
	mux.HandleFunc("/api/skill/install-local", g.handleSkillInstallLocal)
	mux.HandleFunc("/api/skill/merge", g.handleSkillMerge)
	mux.HandleFunc("/api/skill/reload", g.handleSkillReload)
	mux.HandleFunc("/api/skill/detail", g.handleSkillDetail)
	mux.HandleFunc("/api/skill/save", g.handleSkillSave)
	mux.HandleFunc("/api/skill/delete", g.handleSkillDelete)

	// Model provider endpoints
	mux.HandleFunc("/api/providers", g.handleProviderList)
	mux.HandleFunc("/api/model/routes", g.handleModelRoutes)
	mux.HandleFunc("/api/provider/save", g.handleProviderSave)
	mux.HandleFunc("/api/provider/delete", g.handleProviderDelete)
	mux.HandleFunc("/api/ae/model-defaults", g.handleAEModelDefaults)
	mux.HandleFunc("/api/ae/config", g.handleAEConfigSave)

	// Update check endpoint
	mux.HandleFunc("/api/update/check", g.handleUpdateCheck)
	mux.HandleFunc("/api/update/apply", g.handleUpdateApply)
	mux.HandleFunc("/api/update/progress", g.handleUpdateProgress)

	// Cache break detection health
	mux.HandleFunc("/api/cache/health", g.handleCacheHealth)
	mux.HandleFunc("/api/voice", g.handleVoiceUpload)

	// MCP Server (Model Context Protocol) — expose tools to other AI agents
	mcpServer := NewMCPServer(g)
	mux.HandleFunc("/mcp", mcpServer.ServeHTTP)
	g.logger.Printf("[MCP] Server ready at http://localhost:%d/mcp", g.port)

	// Storage 文件直链服务（一点即下载/查看）
	os.MkdirAll(filepath.Join(g.config.Workspace, ".openkitty", "storage"), 0755)
	mux.Handle("/storage/", http.StripPrefix("/storage/", http.FileServer(http.Dir("storage"))))
	mux.HandleFunc("/favicon.ico", func(w http.ResponseWriter, r *http.Request) {
		icon, err := embeddedWebFS.ReadFile("web/openkitty-avatar.png")
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Cache-Control", "public, max-age=86400")
		_, _ = w.Write(icon)
	})

	// Web UI (从内嵌文件系统提供服务)
	webSub, err := fs.Sub(embeddedWebFS, "web")
	if err != nil {
		g.logger.Printf("内嵌 Web FS 初始化失败: %v", err)
	} else {
		webHandler := gatewayhttp.WithWebAssetHeaders(http.StripPrefix("/web/", http.FileServer(http.FS(webSub))))
		mux.Handle("/web/", webHandler)
		mux.HandleFunc("/web", func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "/web/", http.StatusMovedPermanently)
		})
		workbenchV2Redirect := func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "/web/workbench-v2.html", http.StatusTemporaryRedirect)
		}
		mux.HandleFunc("/web-v2", workbenchV2Redirect)
		mux.HandleFunc("/web-v2/", workbenchV2Redirect)
		g.logger.Printf("Web UI 已就绪 (内嵌)")
	}

	// 根路径重定向（如果启用 Web UI）
	if g.config.Gateway.EnableWebUI {
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/" {
				http.Redirect(w, r, "/web/", http.StatusMovedPermanently)
				return
			}
			http.NotFound(w, r)
		})
	}

	g.httpServer = &http.Server{
		Addr:              addr,
		Handler:           mux, //g.authMiddleware(mux),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
		MaxHeaderBytes:    1 << 20,
	}
	mainListener, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen on Gateway address %s: %w", addr, err)
	}
	if err := g.startDedicatedAgentOSDataPlane(agentOSDataHandler); err != nil {
		_ = mainListener.Close()
		return err
	}

	// Bind every externally visible socket before starting workers or channels.
	// A port conflict must never allow this process to claim durable AgentOS
	// work and then race a retrying instance while failed-start cleanup runs.
	g.ensureAgentOSQueue().Start()
	queueStarted = true
	if g.channelManager != nil && !channelsDisabledByEnv() {
		channelsStarted = true
		if err := g.channelManager.StartAll(); err != nil {
			g.logger.Printf("启动渠道失败: %v", err)
		}
	} else if channelsDisabledByEnv() {
		g.logger.Printf("渠道启动已被 %s 禁用", envDisableChannels)
	}

	backgroundCtx := g.beginGatewayBackground()
	g.startGatewayBackground(backgroundCtx)

	webUIURL := gatewayWebUIURL(listenHost, g.port)
	g.logger.Printf("Gateway 服务启动在 %s", webUIURL)
	g.logger.Printf("Web 页面: %s", webUIURL)
	if shouldAutoOpenGatewayBrowser(g.config) {
		go func(ctx context.Context, url string) {
			timer := time.NewTimer(1 * time.Second)
			defer timer.Stop()
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
			}
			log.Printf("Web UI: %s", url)
			if err := openBrowser(url); err != nil {
				log.Printf("Failed to open browser: %v", err)
			}
		}(backgroundCtx, webUIURL)
	} else {
		g.logger.Printf("Headless 模式：跳过自动打开浏览器")
	}

	// HTTPS with auto-generated self-signed cert (enables microphone/WebRTC).
	// Kubernetes normally terminates TLS at Ingress, so service deployments can
	// disable this extra listener explicitly.
	if !httpsServerDisabledByEnv() {
		go func() {
			tlsPort := g.port + 443
			if tlsPort > 65535 {
				tlsPort = g.port + 1
			}
			tlsAddr := net.JoinHostPort(listenHost, strconv.Itoa(tlsPort))
			g.logger.Printf("HTTPS: https://localhost:%d/web", tlsPort)
			security.StartHTTPSServer(g.config.Workspace, tlsAddr, mux)
		}()
	}

	serveErr := g.httpServer.Serve(mainListener)
	if errors.Is(serveErr, http.ErrServerClosed) {
		// Stop owns the normal shutdown path. Avoid repeating channel and TODO
		// shutdown side effects from the failed-start rollback.
		rollbackRequired = false
	}
	return serveErr
}

func fixedGatewayPort() bool {
	if strings.TrimSpace(os.Getenv("KUBERNETES_SERVICE_HOST")) != "" {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(os.Getenv("OPENKITTY_FIXED_PORT"))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func httpsServerDisabledByEnv() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("OPENKITTY_DISABLE_HTTPS"))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func (g *GatewayService) handleConfigReload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")

	g.logger.Println("收到配置重载请求")

	if err := g.ReloadConfig(); err != nil {
		g.logger.Printf("配置重载失败: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status":  "error",
			"message": err.Error(),
		})
		return
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":    "success",
		"message":   "配置已重新加载",
		"timestamp": time.Now(),
	})
}

func (g *GatewayService) ReloadConfig() error {
	g.mutex.Lock()
	defer g.mutex.Unlock()

	g.logger.Println("========== Gateway 开始重载配置 ==========")

	// 1. 加载新配置
	newConfig, err := LoadConfig(g.configpath)
	if err != nil {
		return fmt.Errorf("加载配置文件失败: %w", err)
	}
	preserveRuntimeOnlyConfig(newConfig, g.config)

	// 2. 验证新配置
	if err := g.validateConfig(newConfig); err != nil {
		return fmt.Errorf("配置验证失败: %w", err)
	}
	newHookSettings, hookSources, err := runtimeHookSettings(newConfig)
	if err != nil {
		return fmt.Errorf("Hook V2 配置验证失败（继续使用当前运行配置）: %w", err)
	}

	// 3. 检测 startup-only 字段变更
	warnings := configvalue.DetectStartupFieldChanges(g.config, newConfig)
	configvalue.LogStartupFieldWarnings(warnings, func(format string, args ...interface{}) {
		g.logger.Printf(format, args...)
	})
	for _, w := range warnings {
		eventbus.PushSystemMessage(w, "warning")
	}

	// 4. 更新引擎配置
	if err := g.updateEngineConfig(newConfig); err != nil {
		g.logger.Printf("更新引擎配置失败: %v", err)
		if g.config == nil || !reflect.DeepEqual(g.config.TeamTemplates, newConfig.TeamTemplates) || memorySettingsChanged(g.config, newConfig) {
			return fmt.Errorf("配置已保存，但引擎更新失败: %w", err)
		}
		// 不返回错误，继续更新其他部分
	}

	// 5. 更新渠道配置
	if err := g.updateChannelsConfig(newConfig.Channels); err != nil {
		g.logger.Printf("更新渠道配置失败: %v", err)
	}

	// 6. 更新 Gateway 自身配置
	g.updateGatewayConfig(newConfig.Gateway)

	// 7. 更新当前配置
	g.config = newConfig
	loadFeatureFlagsFromConfig(newConfig)
	installGlobalHookSettings(newHookSettings, newConfig, hookSources, g.logger)
	g.loadPipelinesFromDB() // Reload pipelines from DB (YAML no longer carries them)

	// 8. 保存配置历史
	g.saveConfigHistory(newConfig)

	g.logger.Println("========== Gateway 配置重载完成 ==========")
	return nil
}

func (g *GatewayService) validateConfig(cfg *Config) error {
	if err := validateConfig(cfg); err != nil {
		return err
	}

	for name, model := range cfg.ModelConfig {
		if g.logger == nil {
			continue
		}
		if modelConfigUsesAccount(model) {
			g.logger.Printf("模型 %s 使用提交者的登录账号，调用时校验登录并按账号计费", name)
			continue
		}
		if !modelConfigRequiresAPIKey(model) {
			g.logger.Printf("模型 %s 是本地模型 (type=%s)，无需 API Key", name, model.Type)
			continue
		}
		if isModelConfigAvailable(model) {
			g.logger.Printf("模型 %s 已配置 API Key (type=%s)", name, model.Type)
		} else {
			g.logger.Printf("警告: 模型 %s 当前不可用 (type=%s)，将被跳过", name, model.Type)
		}
	}
	return nil
}

// updateEngineConfig 更新引擎配置
func (g *GatewayService) updateEngineConfig(newConfig *Config) error {
	if g.engine == nil {
		return fmt.Errorf("引擎未初始化")
	}

	// 检查是否需要更新
	if !g.needUpdateEngine(g.config, newConfig) {
		g.logger.Println("引擎配置无变化，跳过更新")
		return nil
	}

	g.logger.Println("检测到引擎配置变更，开始更新...")

	// 更新引擎的配置
	if err := g.engine.UpdateConfig(newConfig); err != nil {
		return fmt.Errorf("更新引擎配置失败: %w", err)
	}

	g.logger.Println("引擎配置更新成功")
	return nil
}

// needUpdateEngine 判断是否需要更新引擎
func (g *GatewayService) needUpdateEngine(old, new *Config) bool {
	if old == nil || new == nil {
		return true
	}
	if memorySettingsChanged(old, new) {
		return true
	}
	if debugToolGateChanged(old, new) ||
		modbusToolGateChanged(old, new) ||
		uartToolGateChanged(old, new) ||
		ros2ToolGateChanged(old, new) ||
		robotToolGateChanged(old, new) {
		return true
	}
	if !strings.EqualFold(strings.TrimSpace(old.DefaultAgent), strings.TrimSpace(new.DefaultAgent)) {
		return true
	}
	if strings.TrimSpace(old.DefaultModel) != strings.TrimSpace(new.DefaultModel) {
		return true
	}
	if !reflect.DeepEqual(old.TeamTemplates, new.TeamTemplates) {
		return true
	}

	// 比较 Agent 配置
	if len(old.Agents) != len(new.Agents) {
		return true
	}

	for i := range old.Agents {
		if old.Agents[i].Name != new.Agents[i].Name ||
			old.Agents[i].Type != new.Agents[i].Type ||
			old.Agents[i].Model != new.Agents[i].Model {
			return true
		}
	}

	// 比较模型配置
	if len(old.ModelConfig) != len(new.ModelConfig) {
		return true
	}

	for k, v := range old.ModelConfig {
		newV, ok := new.ModelConfig[k]
		if !ok {
			return true
		}
		// Compare all provider settings, including model name, sampling and
		// capability flags. Secret-resolution bookkeeping is not a setting.
		v.apiKeyResolved = false
		newV.apiKeyResolved = false
		if v != newV {
			return true
		}
	}

	// Check collaboration config changes
	if old.Collaboration.Type != new.Collaboration.Type ||
		old.Collaboration.MaxRound != new.Collaboration.MaxRound ||
		len(old.Collaboration.Tasks) != len(new.Collaboration.Tasks) {
		return true
	}
	return false
}

// updateGatewayConfig 更新 Gateway 自身配置
func (g *GatewayService) updateGatewayConfig(new GatewayConfig) {
	g.logger.Printf("Gateway 配置更新: ListenAddr=%s, MaxTasks=%d",
		new.ListenAddr, new.MaxTasks)

	// 注意：有些配置需要重启才能生效，这里只更新内存中的配置
	g.config.Gateway = new
}

// updateChannelsConfig 更新渠道配置
func (g *GatewayService) updateChannelsConfig(newChannels []ChannelConfig) error {
	if g.channelManager == nil {
		return fmt.Errorf("渠道管理器未初始化")
	}

	// 检查是否需要更新
	if !g.needUpdateChannels(g.config.Channels, newChannels) {
		g.logger.Println("渠道配置无变化，跳过更新")
		return nil
	}

	g.logger.Println("检测到渠道配置变更，开始更新...")

	// 停止现有渠道
	g.channelManager.StopAll()
	g.channelManager = channelhost.NewManager(newChannelRuntimeAdapter(g))

	// 重新加载渠道
	g.config.Channels = newChannels
	g.loadChannelsFromConfig()

	// 启动新渠道
	if !channelsDisabledByEnv() {
		if err := g.channelManager.StartAll(); err != nil {
			return fmt.Errorf("启动渠道失败: %w", err)
		}
	}

	g.logger.Println("渠道配置更新成功")
	return nil
}

// needUpdateChannels 判断是否需要更新渠道
func (g *GatewayService) needUpdateChannels(old, new []ChannelConfig) bool {
	if len(old) != len(new) {
		return true
	}

	for i := range old {
		if !channelConfigEqualForReload(old[i], new[i]) {
			return true
		}
	}

	return false
}

// saveConfigHistory 保存配置历史
func (g *GatewayService) saveConfigHistory(cfg *Config) {
	if g.db == nil {
		return
	}

	persistedConfig, err := configForPersistence(cfg)
	if err != nil {
		g.logger.Printf("配置历史未保存：模型 API Key 环境变量配置无效: %v", err)
		return
	}
	configJSON, err := json.MarshalIndent(persistedConfig, "", "  ")
	if err != nil {
		g.logger.Printf("序列化配置失败: %v", err)
		return
	}

	// 获取下一个版本号
	var maxVersion int
	g.db.Model(&ConfigHistory{}).Select("COALESCE(MAX(version), 0)").Scan(&maxVersion)
	nextVersion := maxVersion + 1

	history := ConfigHistory{
		Version:   nextVersion,
		Config:    string(configJSON),
		CreatedAt: time.Now(),
	}

	if err := g.db.Create(&history).Error; err != nil {
		g.logger.Printf("保存配置历史失败: %v", err)
	} else {
		g.logger.Printf("配置历史已保存，版本: %d", history.Version)
	}
}

func configFileRevision(configPath string) (string, error) {
	data, err := os.ReadFile(configvalue.ResolvePath(configPath))
	if err != nil {
		return "", fmt.Errorf("读取配置版本失败: %w", err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func (g *GatewayService) handleConfigSections(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		config, err := LoadConfig(g.configpath)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		revision, err := configFileRevision(g.configpath)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		// 返回与 Config 结构匹配的数据
		result := map[string]interface{}{
			"config_revision": revision,
			"team_name":       config.TeamName,
			"default_agent":   config.DefaultAgent,
			"default_model":   selectedModelKey(config),
			"workspace":       config.Workspace,
			"task":            config.Task,
			"agents":          config.Agents,
			"collaboration":   config.Collaboration,
			"output":          config.Output,
			"delivery":        config.Delivery,
			"runtime":         config.Runtime,
			"model_config":    config.ModelConfig,
			"memory_config":   config.MemoryConfig,
			"skills_config":   config.SkillsConfig,
			"headless":        config.Headless,
			"tools_config":    config.ToolsConfig,
			"git_tools":       config.GitTools,
			"gateway":         config.Gateway,
			"email_config":    config.EmailConfig,
			"channels":        config.Channels,
			"heartbeat":       config.Heartbeat,
			"hooks":           config.Hooks,
			"config_status":   buildSettingsConfigStatus(config),
		}

		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		json.NewEncoder(w).Encode(redactedConfigAPIValue(result))

	case http.MethodPut:
		g.configWriteMu.Lock()
		defer g.configWriteMu.Unlock()
		var updates map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&updates); err != nil {
			http.Error(w, "Invalid request body", http.StatusBadRequest)
			return
		}
		if expectedRevision, _ := updates["config_revision"].(string); strings.TrimSpace(expectedRevision) != "" {
			currentRevision, revisionErr := configFileRevision(g.configpath)
			if revisionErr != nil {
				http.Error(w, revisionErr.Error(), http.StatusInternalServerError)
				return
			}
			if currentRevision != strings.TrimSpace(expectedRevision) {
				w.Header().Set("Content-Type", "application/json; charset=utf-8")
				w.WriteHeader(http.StatusConflict)
				json.NewEncoder(w).Encode(map[string]interface{}{
					"status":  "conflict",
					"message": "配置文件已被其他页面或进程修改，请重新加载后再保存",
				})
				return
			}
		}

		// 读取现有配置
		config, err := LoadConfig(g.configpath)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		// 更新配置
		if err := mergeFullConfig(config, updates); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if err := validateSettingsConfigForSave(config); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		// 保存配置到文件
		if err := SaveConfig(g.configpath, config); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		// 更新当前运行的配置并热加载到引擎
		if g.engine != nil &&
			(debugToolGateChanged(g.config, config) ||
				modbusToolGateChanged(g.config, config) ||
				uartToolGateChanged(g.config, config) ||
				ros2ToolGateChanged(g.config, config) ||
				robotToolGateChanged(g.config, config)) {
			// Apply registry gates while the old and new configurations are still
			// distinguishable. The full reload below owns replacing g.config.
			g.engine.syncDebugToolGate(config)
			g.engine.syncModbusToolGate(config)
			g.engine.syncUARTToolGate(config)
			g.engine.syncROS2ToolGate(config)
			g.engine.syncRobotToolGate(config)
		}
		if err := g.ReloadConfig(); err != nil {
			g.logger.Printf("配置已保存，但热加载失败: %v", err)
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"status":        "saved_reload_failed",
				"message":       "配置已写入文件，但热加载失败：" + err.Error(),
				"config_status": buildSettingsConfigStatus(config),
			})
			return
		}
		savedRevision, _ := configFileRevision(g.configpath)

		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status":          "success",
			"message":         "配置已保存并热加载",
			"config_revision": savedRevision,
			"config_status":   buildSettingsConfigStatus(config),
		})

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleConfigHealth exposes the same validation used by the settings page in
// a small, stable endpoint. Startup-recovery probes can therefore distinguish
// "the Web server stayed up" from "the repaired file is ready to edit" without
// scraping the full settings payload.
func (g *GatewayService) handleConfigHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	config := g.config
	loadError := ""
	if strings.TrimSpace(g.configpath) != "" {
		loaded, err := LoadConfig(g.configpath)
		if err != nil {
			loadError = err.Error()
		} else {
			config = loaded
		}
	}
	status := buildSettingsConfigStatus(config)
	errorsList, _ := status["errors"].([]string)
	if loadError != "" {
		errorsList = append([]string{loadError}, errorsList...)
		status["errors"] = errorsList
		status["ok"] = false
	}

	path := strings.TrimSpace(g.configpath)
	if path != "" {
		path = configvalue.ResolvePath(path)
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"ok":            status["ok"],
		"error":         strings.Join(errorsList, "\n"),
		"path":          path,
		"config_status": status,
	})
}

func mergeToolsConfig(config *Config, updates map[string]interface{}) {
	if config == nil || updates == nil {
		return
	}
	raw, ok := updates["tools_config"]
	if !ok {
		return
	}
	tools, ok := raw.(map[string]interface{})
	if !ok {
		return
	}
	if enabled, ok := tools["enabled"].(bool); ok {
		config.ToolsConfig.Enabled = enabled
	}
	if protected, ok := tools["private_network_protection"].(bool); ok {
		config.ToolsConfig.PrivateNetworkProtection = protected
	}
	if rawModbus, ok := tools["modbus"].(map[string]interface{}); ok {
		if enabled, ok := rawModbus["enabled"].(bool); ok {
			config.ToolsConfig.Modbus.Enabled = enabled
		}
		if allowWrite, ok := rawModbus["allow_write"].(bool); ok {
			config.ToolsConfig.Modbus.AllowWrite = allowWrite
		}
		if timeout, err := operationstools.ModbusIntegerValue(rawModbus["timeout_ms"], 100, 30000); err == nil {
			config.ToolsConfig.Modbus.TimeoutMS = timeout
		}
		if rawTargets, exists := rawModbus["targets"]; exists {
			if targetMap, ok := rawTargets.(map[string]interface{}); ok {
				targets := make(map[string]operationstools.ModbusTargetConfig, len(targetMap))
				for name, rawTarget := range targetMap {
					targetValues, ok := rawTarget.(map[string]interface{})
					if !ok || strings.TrimSpace(name) == "" {
						continue
					}
					target := operationstools.ModbusTargetConfig{}
					if transport, ok := targetValues["transport"].(string); ok {
						target.Transport = strings.TrimSpace(transport)
					}
					if address, ok := targetValues["address"].(string); ok {
						target.Address = strings.TrimSpace(address)
					}
					if unitID, err := operationstools.ModbusIntegerValue(targetValues["unit_id"], 1, 247); err == nil {
						target.UnitID = unitID
					}
					if baudRate, err := operationstools.ModbusIntegerValue(targetValues["baud_rate"], 1, 4_000_000); err == nil {
						target.BaudRate = baudRate
					}
					if dataBits, err := operationstools.ModbusIntegerValue(targetValues["data_bits"], 5, 8); err == nil {
						target.DataBits = dataBits
					}
					if parity, ok := targetValues["parity"].(string); ok {
						target.Parity = strings.TrimSpace(parity)
					}
					if stopBits, err := operationstools.ModbusIntegerValue(targetValues["stop_bits"], 1, 2); err == nil {
						target.StopBits = stopBits
					}
					if allowWrite, ok := targetValues["allow_write"].(bool); ok {
						target.AllowWrite = allowWrite
					}
					targets[name] = target
				}
				config.ToolsConfig.Modbus.Targets = targets
			}
		}
	}
	if rawUART, ok := tools["uart"].(map[string]interface{}); ok {
		if enabled, ok := rawUART["enabled"].(bool); ok {
			config.ToolsConfig.UART.Enabled = enabled
		}
		if allowWrite, ok := rawUART["allow_write"].(bool); ok {
			config.ToolsConfig.UART.AllowWrite = allowWrite
		}
		if timeout, err := operationstools.UARTIntegerValue(rawUART["timeout_ms"], 100, 60000); err == nil {
			config.ToolsConfig.UART.TimeoutMS = timeout
		}
		if maximum, err := operationstools.UARTIntegerValue(rawUART["max_read_bytes"], 1, operationstools.MaxUARTTransferBytes); err == nil {
			config.ToolsConfig.UART.MaxReadBytes = maximum
		}
		if maximum, err := operationstools.UARTIntegerValue(rawUART["max_write_bytes"], 1, operationstools.MaxUARTTransferBytes); err == nil {
			config.ToolsConfig.UART.MaxWriteBytes = maximum
		}
		if rawTargets, exists := rawUART["targets"]; exists {
			if targetMap, ok := rawTargets.(map[string]interface{}); ok {
				targets := make(map[string]operationstools.UARTTargetConfig, len(targetMap))
				for name, rawTarget := range targetMap {
					targetValues, ok := rawTarget.(map[string]interface{})
					if !ok || strings.TrimSpace(name) == "" {
						continue
					}
					target := operationstools.UARTTargetConfig{}
					if port, ok := targetValues["port"].(string); ok {
						target.Port = strings.TrimSpace(port)
					}
					if baudRate, err := operationstools.UARTIntegerValue(targetValues["baud_rate"], 1, 4_000_000); err == nil {
						target.BaudRate = baudRate
					}
					if dataBits, err := operationstools.UARTIntegerValue(targetValues["data_bits"], 5, 8); err == nil {
						target.DataBits = dataBits
					}
					if parity, ok := targetValues["parity"].(string); ok {
						target.Parity = strings.TrimSpace(parity)
					}
					if stopBits, err := operationstools.UARTIntegerValue(targetValues["stop_bits"], 1, 2); err == nil {
						target.StopBits = stopBits
					}
					if timeout, err := operationstools.UARTIntegerValue(targetValues["timeout_ms"], 100, 60000); err == nil {
						target.TimeoutMS = timeout
					}
					if allowWrite, ok := targetValues["allow_write"].(bool); ok {
						target.AllowWrite = allowWrite
					}
					targets[strings.TrimSpace(name)] = target
				}
				config.ToolsConfig.UART.Targets = targets
			}
		}
	}
	if rawROS2, ok := tools["ros2"].(map[string]interface{}); ok {
		if enabled, ok := rawROS2["enabled"].(bool); ok {
			config.ToolsConfig.ROS2.Enabled = enabled
		}
		if allowed, ok := rawROS2["allow_publish"].(bool); ok {
			config.ToolsConfig.ROS2.AllowPublish = allowed
		}
		if allowed, ok := rawROS2["allow_service_call"].(bool); ok {
			config.ToolsConfig.ROS2.AllowServiceCall = allowed
		}
		if allowed, ok := rawROS2["allow_action"].(bool); ok {
			config.ToolsConfig.ROS2.AllowAction = allowed
		}
		if timeout, err := operationstools.UARTIntegerValue(rawROS2["timeout_ms"], 100, 300000); err == nil {
			config.ToolsConfig.ROS2.TimeoutMS = timeout
		}
		if maximum, err := operationstools.UARTIntegerValue(rawROS2["max_output_bytes"], 1024, operationstools.MaxROS2OutputBytes); err == nil {
			config.ToolsConfig.ROS2.MaxOutputBytes = maximum
		}
		if command, ok := rawROS2["command"].(string); ok {
			config.ToolsConfig.ROS2.Command = strings.TrimSpace(command)
		}
		if domainID, err := operationstools.UARTIntegerValue(rawROS2["domain_id"], 0, 232); err == nil {
			config.ToolsConfig.ROS2.DomainID = domainID
		}
		if values, ok := configStringSlice(rawROS2["topic_allowlist"]); ok {
			config.ToolsConfig.ROS2.TopicAllowlist = values
		}
		if values, ok := configStringSlice(rawROS2["service_allowlist"]); ok {
			config.ToolsConfig.ROS2.ServiceAllowlist = values
		}
		if values, ok := configStringSlice(rawROS2["action_allowlist"]); ok {
			config.ToolsConfig.ROS2.ActionAllowlist = values
		}
	}
	if rawRobot, ok := tools["robot"].(map[string]interface{}); ok {
		if enabled, ok := rawRobot["enabled"].(bool); ok {
			config.ToolsConfig.Robot.Enabled = enabled
		}
		if allowMotion, ok := rawRobot["allow_motion"].(bool); ok {
			config.ToolsConfig.Robot.AllowMotion = allowMotion
		}
		if defaultDevice, ok := rawRobot["default_device"].(string); ok {
			config.ToolsConfig.Robot.DefaultDevice = strings.TrimSpace(defaultDevice)
		}
		if rawDevices, exists := rawRobot["devices"]; exists {
			if deviceMap, ok := rawDevices.(map[string]interface{}); ok {
				devices := make(map[string]operationstools.RobotDeviceConfig, len(deviceMap))
				for name, rawDevice := range deviceMap {
					deviceValues, ok := rawDevice.(map[string]interface{})
					if !ok || strings.TrimSpace(name) == "" {
						continue
					}
					device := operationstools.RobotDeviceConfig{}
					if enabled, ok := deviceValues["enabled"].(bool); ok {
						device.Enabled = enabled
					}
					if driver, ok := deviceValues["driver"].(string); ok {
						device.Driver = strings.TrimSpace(driver)
					}
					if allowMotion, ok := deviceValues["allow_motion"].(bool); ok {
						device.AllowMotion = allowMotion
					}
					if value, err := operationstools.RobotIntegerParam(deviceValues["watchdog_ms"], 0, 60000); err == nil {
						device.WatchdogMS = value
					}
					if value, err := operationstools.RobotIntegerParam(deviceValues["max_command_ms"], 0, 60000); err == nil {
						device.MaxCommandMS = value
					}
					if value, exists, err := operationstools.RobotNumberParam(deviceValues, "max_linear_mps"); err == nil && exists {
						device.MaxLinearMPS = value
					}
					if value, exists, err := operationstools.RobotNumberParam(deviceValues, "max_angular_rps"); err == nil && exists {
						device.MaxAngularRPS = value
					}
					if value, exists, err := operationstools.RobotNumberParam(deviceValues, "max_distance_m"); err == nil && exists {
						device.MaxDistanceM = value
					}
					if value, exists, err := operationstools.RobotNumberParam(deviceValues, "max_turn_degrees"); err == nil && exists {
						device.MaxTurnDegrees = value
					}
					devices[strings.TrimSpace(name)] = device
				}
				config.ToolsConfig.Robot.Devices = devices
			}
		}
	}
	if rawDisabled, exists := tools["disabled_tools"]; exists {
		switch disabled := rawDisabled.(type) {
		case []string:
			config.ToolsConfig.DisabledTools = append([]string(nil), disabled...)
		case []interface{}:
			values := make([]string, 0, len(disabled))
			for _, item := range disabled {
				if name, ok := item.(string); ok && strings.TrimSpace(name) != "" {
					values = append(values, name)
				}
			}
			config.ToolsConfig.DisabledTools = values
		}
	}
}

func configStringSlice(value interface{}) ([]string, bool) {
	switch values := value.(type) {
	case []string:
		return append([]string(nil), values...), true
	case []interface{}:
		result := make([]string, 0, len(values))
		for _, item := range values {
			text, ok := item.(string)
			if !ok {
				continue
			}
			if text = strings.TrimSpace(text); text != "" {
				result = append(result, text)
			}
		}
		return result, true
	default:
		return nil, false
	}
}

func decodeAgentConfigUpdates(value interface{}) ([]AgentConfig, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("Agent 配置编码失败: %w", err)
	}
	var agents []AgentConfig
	if err := json.Unmarshal(data, &agents); err != nil {
		return nil, fmt.Errorf("Agent 配置格式错误: %w", err)
	}
	return agents, nil
}

// preserveRedactedConfigUpdates treats the public API's [REDACTED] value as a
// keep-existing sentinel. Without this pass, opening Settings and pressing
// Save replaces model keys, mail passwords, and channel tokens with the literal
// placeholder returned by the corresponding GET endpoint.
func preserveRedactedConfigUpdates(config *Config, updates map[string]interface{}) {
	if config == nil || len(updates) == 0 {
		return
	}
	data, err := json.Marshal(config)
	if err != nil {
		return
	}
	var current map[string]interface{}
	if err := json.Unmarshal(data, &current); err != nil {
		return
	}
	// Config's persisted field is model_cfg while both settings update APIs use
	// model_config. Channels historically has no json tag and therefore encodes
	// as "Channels"; expose both API spellings to the recursive matcher.
	if models, ok := current["model_cfg"]; ok {
		current["model_config"] = models
	}
	if channels, ok := current["Channels"]; ok {
		current["channels"] = channels
	}
	restoreRedactedConfigValues(updates, current)
}

func restoreRedactedConfigValues(update, current interface{}) {
	switch typed := update.(type) {
	case map[string]interface{}:
		currentMap, _ := current.(map[string]interface{})
		for key, value := range typed {
			currentValue := currentMap[key]
			if text, ok := value.(string); ok && text == "[REDACTED]" && isSensitiveConfigField(key) {
				if existing, ok := currentValue.(string); ok {
					typed[key] = existing
				}
				continue
			}
			restoreRedactedConfigValues(value, currentValue)
		}
	case []interface{}:
		currentList, _ := current.([]interface{})
		for index, value := range typed {
			currentValue := matchingConfigListValue(value, currentList, index)
			restoreRedactedConfigValues(value, currentValue)
		}
	}
}

func matchingConfigListValue(update interface{}, current []interface{}, index int) interface{} {
	if updateMap, ok := update.(map[string]interface{}); ok {
		if name, ok := updateMap["name"].(string); ok && strings.TrimSpace(name) != "" {
			for _, candidate := range current {
				candidateMap, _ := candidate.(map[string]interface{})
				if candidateName, _ := candidateMap["name"].(string); candidateName == name {
					return candidate
				}
			}
		}
	}
	if index >= 0 && index < len(current) {
		return current[index]
	}
	return nil
}

func mergeFullConfig(config *Config, updates map[string]interface{}) error {
	preserveRedactedConfigUpdates(config, updates)
	mergeToolsConfig(config, updates)
	if rawHooks, ok := updates["hooks"]; ok {
		encoded, err := json.Marshal(rawHooks)
		if err != nil {
			return fmt.Errorf("hooks 配置编码失败: %w", err)
		}
		var hooks hookscore.V2HookSettings
		if err := json.Unmarshal(encoded, &hooks); err != nil {
			return fmt.Errorf("hooks 配置格式错误: %w", err)
		}
		if err := hookscore.ValidateHookSettings(&hooks); err != nil {
			return fmt.Errorf("hooks 配置无效: %w", err)
		}
		config.Hooks = *hookscore.CloneSettings(&hooks)
	}

	// 基础配置
	if val, ok := updates["team_name"]; ok {
		if str, ok := val.(string); ok {
			config.TeamName = str
		}
	}
	if val, ok := updates["default_agent"]; ok {
		if str, ok := val.(string); ok {
			config.DefaultAgent = strings.TrimSpace(str)
		}
	}
	if val, ok := updates["default_model"].(string); ok {
		config.DefaultModel = strings.TrimSpace(val)
	}
	if val, ok := updates["workspace"]; ok {
		if str, ok := val.(string); ok {
			config.Workspace = str
		}
	}
	if val, ok := updates["headless"]; ok {
		if b, ok := val.(bool); ok {
			config.Headless = b
		}
	}

	if val, ok := updates["output"]; ok {
		if outputMap, ok := val.(map[string]interface{}); ok {
			if format, ok := outputMap["format"]; ok {
				if str, ok := format.(string); ok {
					config.Output.Format = OutputFormat(str) // ✅ 添加类型转换
				}
			}
			if dir, ok := outputMap["dir"]; ok {
				if str, ok := dir.(string); ok {
					config.Output.Dir = str
				}
			}
		}
	}

	// Memory master switch. Preserve window, storage and embedding settings
	// when the compact Web settings form only changes enabled.
	if memory, ok := updates["memory_config"].(map[string]interface{}); ok {
		if enabled, ok := memory["enabled"].(bool); ok {
			config.MemoryConfig.Enabled = enabled
		}
	}

	// Runtime 配置
	if val, ok := updates["runtime"]; ok {
		if runtimeMap, ok := val.(map[string]interface{}); ok {
			if limit, ok := runtimeMap["semaphore_limit"]; ok {
				if num, ok := limit.(float64); ok {
					config.Runtime.SemaphoreLimit = int(num)
				}
			}
		}
	}

	// Gateway 配置
	if val, ok := updates["gateway"]; ok {
		if gatewayMap, ok := val.(map[string]interface{}); ok {
			if enabled, ok := gatewayMap["enabled"]; ok {
				if b, ok := enabled.(bool); ok {
					config.Gateway.Enabled = b
				}
			}
			if listenAddr, ok := gatewayMap["listen_addr"]; ok {
				if str, ok := listenAddr.(string); ok {
					config.Gateway.ListenAddr = str
				}
			}
			if maxTasks, ok := gatewayMap["max_tasks"]; ok {
				if num, ok := maxTasks.(float64); ok {
					config.Gateway.MaxTasks = int(num)
				}
			}
			if maxQueued, ok := gatewayMap["max_queued_tasks"].(float64); ok {
				config.Gateway.MaxQueuedTasks = int(maxQueued)
			}
			if mode, ok := gatewayMap["agent_os_mode"].(string); ok {
				config.Gateway.AgentOSMode = agentos.NormalizeMode(mode)
			}
			if leaseSeconds, ok := gatewayMap["task_lease_seconds"].(float64); ok {
				config.Gateway.TaskLeaseSeconds = int(leaseSeconds)
			}
			if pollMS, ok := gatewayMap["task_poll_interval_ms"].(float64); ok {
				config.Gateway.TaskPollIntervalMS = int(pollMS)
			}
			if taskTimeout, ok := gatewayMap["task_timeout"]; ok {
				if num, ok := taskTimeout.(float64); ok {
					config.Gateway.TaskTimeout = int(num)
				}
			}
			if enableWebUI, ok := gatewayMap["enable_webui"]; ok {
				if b, ok := enableWebUI.(bool); ok {
					config.Gateway.EnableWebUI = b
				}
			}
			if webUIPath, ok := gatewayMap["webui_path"]; ok {
				if str, ok := webUIPath.(string); ok {
					config.Gateway.WebUIPath = str
				}
			}
		}
	}

	// Email 配置
	if val, ok := updates["email_config"]; ok {
		if emailMap, ok := val.(map[string]interface{}); ok {
			if enabled, ok := emailMap["enabled"]; ok {
				if b, ok := enabled.(bool); ok {
					config.EmailConfig.Enabled = b
				}
			}
			if smtpHost, ok := emailMap["smtp_host"]; ok {
				if str, ok := smtpHost.(string); ok {
					config.EmailConfig.SMTPHost = str
				}
			}
			if smtpPort, ok := emailMap["smtp_port"]; ok {
				if num, ok := smtpPort.(float64); ok {
					config.EmailConfig.SMTPPort = int(num)
				}
			}
			if imapHost, ok := emailMap["imap_host"]; ok {
				if str, ok := imapHost.(string); ok {
					config.EmailConfig.IMAPHost = str
				}
			}
			if imapPort, ok := emailMap["imap_port"]; ok {
				if num, ok := imapPort.(float64); ok {
					config.EmailConfig.IMAPPort = int(num)
				}
			}
			if username, ok := emailMap["username"]; ok {
				if str, ok := username.(string); ok {
					config.EmailConfig.Username = str
				}
			}
			if password, ok := emailMap["password"]; ok {
				if str, ok := password.(string); ok {
					config.EmailConfig.Password = str
				}
			}
			if fromAddress, ok := emailMap["from_address"]; ok {
				if str, ok := fromAddress.(string); ok {
					config.EmailConfig.FromAddress = str
				}
			}
			if useTLS, ok := emailMap["use_tls"]; ok {
				if b, ok := useTLS.(bool); ok {
					config.EmailConfig.UseTLS = b
				}
			}
		}
	}

	// Agents 配置
	if val, ok := updates["agents"]; ok {
		newAgents, err := decodeAgentConfigUpdates(val)
		if err != nil {
			return err
		}
		config.Agents = newAgents
	}

	// model_config is a complete Settings snapshot. Build a replacement map so
	// omitted entries are genuinely deleted. Existing entries remain the merge
	// base to preserve advanced fields that the compact editor does not expose.
	if val, ok := updates["model_config"]; ok {
		modelsMap, valid := val.(map[string]interface{})
		if !valid {
			return fmt.Errorf("model_config 配置格式错误")
		}
		replacement := make(map[string]ModelConfig, len(modelsMap))
		for name, modelData := range modelsMap {
			modelMap, valid := modelData.(map[string]interface{})
			if !valid {
				return fmt.Errorf("模型 %s 配置格式错误", name)
			}
			sourceName := name
			if previousName, ok := modelMap["__previous_name"].(string); ok && strings.TrimSpace(previousName) != "" {
				sourceName = strings.TrimSpace(previousName)
			}
			modelConfig := config.ModelConfig[sourceName]
			// A renamed model cannot match the redacted placeholder by map key.
			// Keeping the old model as the merge base preserves its real secret.
			if sourceName != name {
				if key, ok := modelMap["api_key"].(string); ok && key == "[REDACTED]" {
					delete(modelMap, "api_key")
				}
			}
			if t, ok := modelMap["type"]; ok {
				if str, ok := t.(string); ok {
					modelConfig.Type = str
				}
			}
			mergeModelConfigAPIKeyFields(&modelConfig, modelMap)
			if baseURL, ok := modelMap["base_url"]; ok {
				if str, ok := baseURL.(string); ok {
					modelConfig.BaseURL = str
				}
			}
			if modelName, ok := modelMap["model_name"]; ok {
				if str, ok := modelName.(string); ok {
					modelConfig.ModelName = str
				}
			}
			if temp, ok := modelMap["temperature"]; ok {
				if num, ok := temp.(float64); ok {
					modelConfig.Temperature = num
				}
			}
			if maxTokens, ok := modelMap["max_tokens"]; ok {
				if num, ok := maxTokens.(float64); ok {
					modelConfig.MaxTokens = int(num)
				}
			}
			if thinkingMode, ok := modelMap["thinking_mode"].(string); ok {
				modelConfig.ThinkingMode = thinkingMode
			}
			if supportsVision, ok := modelMap["supports_vision"].(bool); ok {
				modelConfig.SupportsVision = supportsVision
			}
			replacement[name] = modelConfig
		}
		config.ModelConfig = replacement
	}

	// Channels 配置
	if val, ok := updates["channels"]; ok {
		if channelsArray, ok := val.([]interface{}); ok {
			var newChannels []ChannelConfig
			for _, item := range channelsArray {
				if channelMap, ok := item.(map[string]interface{}); ok {
					channel := ChannelConfig{}
					if name, ok := channelMap["name"]; ok {
						if str, ok := name.(string); ok {
							channel.Name = str
						}
					}
					if t, ok := channelMap["type"]; ok {
						if str, ok := t.(string); ok {
							channel.Type = str
						}
					}
					if enabled, ok := channelMap["enabled"]; ok {
						if b, ok := enabled.(bool); ok {
							channel.Enabled = b
						}
					}
					if configMap, ok := channelMap["config"]; ok {
						if cfg, ok := configMap.(map[string]interface{}); ok {
							channel.Config = cfg
						}
					}
					newChannels = append(newChannels, channel)
				}
			}
			config.Channels = newChannels
		}
	}

	return resolveModelConfigEnvironment(config)
}

// 合并配置的辅助函数
func mergeConfig(config *Config, updates map[string]interface{}) error {
	mergeToolsConfig(config, updates)

	// 这里需要根据你的 Config 结构体实现具体的合并逻辑
	// 示例实现：
	if val, ok := updates["team_name"]; ok {
		if str, ok := val.(string); ok {
			config.TeamName = str
		}
	}
	if val, ok := updates["workspace"]; ok {
		if str, ok := val.(string); ok {
			config.Workspace = str
		}
	}
	if val, ok := updates["gateway"]; ok {
		if gateway, ok := val.(map[string]interface{}); ok {
			if listenAddr, ok := gateway["listen_addr"]; ok {
				if str, ok := listenAddr.(string); ok {
					config.Gateway.ListenAddr = str
				}
			}
			if maxTasks, ok := gateway["max_tasks"]; ok {
				if num, ok := maxTasks.(float64); ok {
					config.Gateway.MaxTasks = int(num)
				}
			}
			if maxQueued, ok := gateway["max_queued_tasks"].(float64); ok {
				config.Gateway.MaxQueuedTasks = int(maxQueued)
			}
			if mode, ok := gateway["agent_os_mode"].(string); ok {
				config.Gateway.AgentOSMode = agentos.NormalizeMode(mode)
			}
			if leaseSeconds, ok := gateway["task_lease_seconds"].(float64); ok {
				config.Gateway.TaskLeaseSeconds = int(leaseSeconds)
			}
			if pollMS, ok := gateway["task_poll_interval_ms"].(float64); ok {
				config.Gateway.TaskPollIntervalMS = int(pollMS)
			}
			if taskTimeout, ok := gateway["task_timeout"]; ok {
				if num, ok := taskTimeout.(float64); ok {
					config.Gateway.TaskTimeout = int(num)
				}
			}
			if enableWebUI, ok := gateway["enable_webui"]; ok {
				if boolVal, ok := enableWebUI.(bool); ok {
					config.Gateway.EnableWebUI = boolVal
				}
			}
		}
	}
	if val, ok := updates["email_config"]; ok {
		if email, ok := val.(map[string]interface{}); ok {
			if enabled, ok := email["enabled"]; ok {
				if boolVal, ok := enabled.(bool); ok {
					config.EmailConfig.Enabled = boolVal
				}
			}
			if smtpHost, ok := email["smtp_host"]; ok {
				if str, ok := smtpHost.(string); ok {
					config.EmailConfig.SMTPHost = str
				}
			}
			if smtpPort, ok := email["smtp_port"]; ok {
				if num, ok := smtpPort.(float64); ok {
					config.EmailConfig.SMTPPort = int(num)
				}
			}
			if username, ok := email["username"]; ok {
				if str, ok := username.(string); ok {
					config.EmailConfig.Username = str
				}
			}
			if password, ok := email["password"]; ok {
				if str, ok := password.(string); ok {
					config.EmailConfig.Password = str
				}
			}
		}
	}
	if val, ok := updates["model_config"]; ok {
		if models, ok := val.(map[string]interface{}); ok {
			for name, modelData := range models {
				if modelMap, ok := modelData.(map[string]interface{}); ok {
					modelConfig := ModelConfig{}
					if t, ok := modelMap["type"]; ok {
						if str, ok := t.(string); ok {
							modelConfig.Type = str
						}
					}
					mergeModelConfigAPIKeyFields(&modelConfig, modelMap)
					if baseURL, ok := modelMap["base_url"]; ok {
						if str, ok := baseURL.(string); ok {
							modelConfig.BaseURL = str
						}
					}
					if modelName, ok := modelMap["model_name"]; ok {
						if str, ok := modelName.(string); ok {
							modelConfig.ModelName = str
						}
					}
					if temp, ok := modelMap["temperature"]; ok {
						if num, ok := temp.(float64); ok {
							modelConfig.Temperature = num
						}
					}
					if maxTokens, ok := modelMap["max_tokens"]; ok {
						if num, ok := maxTokens.(float64); ok {
							modelConfig.MaxTokens = int(num)
						}
					}
					config.ModelConfig[name] = modelConfig
				}
			}
		}
	}

	return resolveModelConfigEnvironment(config)
}

func buildSettingsConfigStatus(config *Config) map[string]interface{} {
	status := map[string]interface{}{
		"ok":               true,
		"errors":           []string{},
		"warnings":         []string{},
		"available_models": []string{},
		"invalid_models":   map[string]string{},
		"agent_errors":     []string{},
		"channel_warnings": []string{},
	}
	if config == nil {
		status["ok"] = false
		status["errors"] = []string{"config is nil"}
		return status
	}

	errors := []string{}
	warnings := []string{}
	agentErrors := []string{}
	channelWarnings := []string{}
	invalidModels := map[string]string{}
	availableModels := preferredModelConfigKeys(config.ModelConfig)
	availableSet := map[string]bool{}
	for _, name := range availableModels {
		availableSet[name] = true
	}
	for name, cfg := range config.ModelConfig {
		if !isModelConfigAvailable(cfg) {
			invalidModels[name] = modelConfigUnavailableReason(cfg)
		}
	}
	if len(availableModels) == 0 {
		errors = append(errors, "没有可用模型：请在「模型配置」中配置至少一个真实可用的模型")
	}
	if name := strings.TrimSpace(config.DefaultModel); name != "" && !availableSet[name] {
		errors = append(errors, fmt.Sprintf("当前选择的模型 %s 不可用", name))
	}
	for _, agent := range config.Agents {
		modelName := effectiveAgentModelKey(config, agent)
		if modelName == "" {
			agentErrors = append(agentErrors, fmt.Sprintf("Agent %s 没有可继承的当前模型", agent.Name))
			continue
		}
		if _, ok := config.ModelConfig[modelName]; !ok {
			agentErrors = append(agentErrors, fmt.Sprintf("Agent %s 选择的模型 %s 不存在", agent.Name, modelName))
			continue
		}
		if !availableSet[modelName] {
			agentErrors = append(agentErrors, fmt.Sprintf("Agent %s 选择的模型 %s 不可用：%s", agent.Name, modelName, modelConfigUnavailableReason(config.ModelConfig[modelName])))
		}
	}
	if len(agentErrors) > 0 {
		errors = append(errors, agentErrors...)
	}
	if err := hookscore.ValidateHookSettings(&config.Hooks); err != nil {
		errors = append(errors, "Hooks 配置无效："+err.Error())
	}
	if config.ToolsConfig.UART.Enabled {
		if _, err := operationstools.NormalizeUARTToolConfig(config.ToolsConfig.UART); err != nil {
			errors = append(errors, "UART 配置无效："+err.Error())
		} else if !config.ToolsConfig.UART.AllowWrite {
			warnings = append(warnings, "UART 工具已启用，但 allow_write=false；当前仅允许探测和读取")
		}
	}
	if config.ToolsConfig.ROS2.Enabled {
		if _, err := operationstools.NormalizeROS2ToolConfig(config.ToolsConfig.ROS2); err != nil {
			errors = append(errors, "ROS2 配置无效："+err.Error())
		} else if !config.ToolsConfig.ROS2.AllowPublish &&
			!config.ToolsConfig.ROS2.AllowServiceCall &&
			!config.ToolsConfig.ROS2.AllowAction {
			warnings = append(warnings, "ROS2 工具已启用，但发布、Service 调用和 Action 调用均关闭；当前仅允许读取 ROS 图")
		}
	}
	if config.ToolsConfig.Robot.Enabled {
		if _, err := operationstools.NormalizeRobotToolConfig(config.ToolsConfig.Robot); err != nil {
			errors = append(errors, "机器人配置无效："+err.Error())
		} else if !config.ToolsConfig.Robot.AllowMotion {
			warnings = append(warnings, "机器人运行时已启用，但全局 allow_motion=false；当前仅允许状态读取和停止")
		}
	}
	for _, ch := range config.Channels {
		if ch.Enabled {
			if missing := missingChannelFields(ch); len(missing) > 0 {
				channelWarnings = append(channelWarnings, fmt.Sprintf("渠道 %s(%s) 已启用但缺少参数：%s", ch.Name, ch.Type, strings.Join(missing, ", ")))
			}
		}
	}
	warnings = append(warnings, channelWarnings...)

	status["ok"] = len(errors) == 0
	status["errors"] = errors
	status["warnings"] = warnings
	status["available_models"] = availableModels
	status["invalid_models"] = invalidModels
	status["agent_errors"] = agentErrors
	status["channel_warnings"] = channelWarnings
	return status
}

func validateSettingsConfigForSave(config *Config) error {
	status := buildSettingsConfigStatus(config)
	if ok, _ := status["ok"].(bool); ok {
		return nil
	}
	if errors, ok := status["errors"].([]string); ok && len(errors) > 0 {
		return fmt.Errorf("%s", strings.Join(errors, "\n"))
	}
	return fmt.Errorf("配置不可用")
}

func modelConfigUnavailableReason(cfg ModelConfig) string {
	if strings.TrimSpace(cfg.ModelName) == "" {
		return "model_name missing"
	}
	if modelConfigRequiresAPIKey(cfg) && isAPIKeySuspicious(cfg.APIKey) {
		return "api_key missing or placeholder"
	}
	return "unavailable"
}

func missingChannelFields(ch ChannelConfig) []string {
	required := map[string][]string{
		"feishu":   {"app_id", "app_secret"},
		"dingtalk": {"client_id", "client_secret"},
		"qq":       {"app_id", "app_secret"},
		"wework":   {"bot_id", "secret"},
	}
	fields := required[strings.ToLower(strings.TrimSpace(ch.Type))]
	if len(fields) == 0 {
		return nil
	}
	missing := []string{}
	for _, field := range fields {
		value, ok := ch.Config[field]
		if !ok || strings.TrimSpace(fmt.Sprint(value)) == "" {
			missing = append(missing, field)
		}
	}
	return missing
}

func copyFile(src, dst string) error {
	source, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, source, 0644)
}

func (g *GatewayService) handleGetLogs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// 获取参数
	lines := 100 // 默认返回100行
	if l := r.URL.Query().Get("lines"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 && n <= 1000 {
			lines = n
		}
	}

	level := r.URL.Query().Get("level") // info, error, warning, all
	search := r.URL.Query().Get("search")

	// 获取最新的日志文件
	logDir := "./logs" // 或者从配置读取
	logFile := filepath.Join(logDir, "app.log")

	// 如果今天的日志文件不存在，尝试找最近的
	if _, err := os.Stat(logFile); os.IsNotExist(err) {
		// 查找最新的日志文件
		files, err := filepath.Glob(filepath.Join(logDir, "*.log"))
		if err != nil || len(files) == 0 {
			sendJSONError(w, "No log files found", http.StatusNotFound)
			return
		}
		// 按修改时间排序，取最新的
		var latest string
		var latestTime time.Time
		for _, f := range files {
			info, _ := os.Stat(f)
			if info.ModTime().After(latestTime) {
				latestTime = info.ModTime()
				latest = f
			}
		}
		if latest != "" {
			logFile = latest
		}
	}

	// 读取日志文件
	content, err := os.ReadFile(logFile)
	if err != nil {
		sendJSONError(w, "Failed to read log file", http.StatusInternalServerError)
		return
	}

	// 解析日志
	logs := parseLogLines(string(content), lines, level, search)

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"logs":      logs,
		"total":     len(logs),
		"log_file":  filepath.Base(logFile),
		"timestamp": time.Now(),
	})
}

func (g *GatewayService) handleShellJobs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"sessions": globalstate.TerminalManager(shellResourceOwnerFromContext, shellResourceAccessible).ListSessions(),
		"jobs":     globalstate.ShellJobs(shellResourceOwnerFromContext, shellResourceAccessible).List(),
	})
}

func (g *GatewayService) handleShellJobLog(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	jobID := shellJobIDFromRequest(r)
	if jobID == "" {
		sendJSONError(w, "job id required", http.StatusBadRequest)
		return
	}
	job, ok := globalstate.ShellJobs(shellResourceOwnerFromContext, shellResourceAccessible).Get(jobID)
	if !ok {
		sendJSONError(w, "shell job not found", http.StatusNotFound)
		return
	}

	logPath, ok := g.shellJobLogAbsPath(job)
	if !ok {
		sendJSONError(w, "shell job log path is outside runtime directory", http.StatusForbidden)
		return
	}
	maxLines := 200
	if s := r.URL.Query().Get("lines"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 && n <= 5000 {
			maxLines = n
		}
	}
	content, err := processutil.ReadShellJobLog(logPath, maxLines)
	if err != nil {
		sendJSONError(w, "failed to read shell job log", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"job":     job,
		"content": content,
		"lines":   maxLines,
	})
}

func (g *GatewayService) handleShellJobStop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var body struct {
		ID    string `json:"id"`
		JobID string `json:"job_id"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	jobID := strings.TrimSpace(body.ID)
	if jobID == "" {
		jobID = strings.TrimSpace(body.JobID)
	}
	if jobID == "" {
		jobID = shellJobIDFromRequest(r)
	}
	if jobID == "" {
		sendJSONError(w, "job id required", http.StatusBadRequest)
		return
	}
	if err := globalstate.ShellJobs(shellResourceOwnerFromContext, shellResourceAccessible).Stop(jobID); err != nil {
		sendJSONError(w, err.Error(), http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"ok":     true,
		"job_id": jobID,
	})
}

func (g *GatewayService) handleShellJobDownload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	jobID := shellJobIDFromRequest(r)
	if jobID == "" {
		http.Error(w, "job id required", http.StatusBadRequest)
		return
	}
	job, ok := globalstate.ShellJobs(shellResourceOwnerFromContext, shellResourceAccessible).Get(jobID)
	if !ok {
		http.Error(w, "shell job not found", http.StatusNotFound)
		return
	}
	logPath, ok := g.shellJobLogAbsPath(job)
	if !ok {
		http.Error(w, "shell job log path is outside runtime directory", http.StatusForbidden)
		return
	}
	if _, err := os.Stat(logPath); err != nil {
		http.Error(w, "log not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filepath.Base(logPath)))
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	http.ServeFile(w, r, logPath)
}

func shellJobIDFromRequest(r *http.Request) string {
	jobID := strings.TrimSpace(r.URL.Query().Get("id"))
	if jobID == "" {
		jobID = strings.TrimSpace(r.URL.Query().Get("job_id"))
	}
	return jobID
}

func (g *GatewayService) shellJobLogAbsPath(job processutil.ShellJob) (string, bool) {
	baseDir := "."
	if g != nil && g.config != nil && strings.TrimSpace(g.config.Workspace) != "" {
		baseDir = g.config.Workspace
	}

	logPath := filepath.Clean(job.LogPath)
	if !filepath.IsAbs(logPath) {
		logPath = filepath.Join(baseDir, logPath)
	}
	absLog, err := filepath.Abs(logPath)
	if err != nil {
		return "", false
	}
	absRuntime, err := filepath.Abs(filepath.Join(baseDir, ".openkitty", "runtime", "shell"))
	if err != nil {
		return "", false
	}
	rel, err := filepath.Rel(absRuntime, absLog)
	if err != nil || rel == "." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || rel == ".." || filepath.IsAbs(rel) {
		return "", false
	}
	return absLog, true
}

// 解析日志行
func parseLogLines(content string, limit int, levelFilter, searchFilter string) []map[string]interface{} {
	lines := strings.Split(content, "\n")
	logs := make([]map[string]interface{}, 0)

	// 从后往前读取（最新的在前）
	for i := len(lines) - 1; i >= 0 && len(logs) < limit; i-- {
		line := lines[i]
		if line == "" {
			continue
		}

		// 解析日志格式，假设标准格式: [2024-01-15 10:30:45] [INFO] message
		logEntry := parseLogLine(line)

		// 应用过滤
		if levelFilter != "" && levelFilter != "all" {
			if logLevel, ok := logEntry["level"].(string); !ok || !strings.EqualFold(logLevel, levelFilter) {
				continue
			}
		}

		if searchFilter != "" && !strings.Contains(strings.ToLower(line), strings.ToLower(searchFilter)) {
			continue
		}

		logs = append(logs, logEntry)
	}

	return logs
}

func parseLogLine(line string) map[string]interface{} {
	entry := make(map[string]interface{})
	entry["raw"] = line

	// 尝试解析时间戳
	// 匹配格式: [2024-01-15 10:30:45]
	re := regexp.MustCompile(`\[(\d{4}-\d{2}-\d{2}\s+\d{2}:\d{2}:\d{2})\]`)
	if match := re.FindStringSubmatch(line); len(match) > 1 {
		entry["timestamp"] = match[1]
	}

	// 解析日志级别
	if strings.Contains(line, "[INFO]") || strings.Contains(line, " INFO ") {
		entry["level"] = "info"
	} else if strings.Contains(line, "[ERROR]") || strings.Contains(line, " ERROR ") {
		entry["level"] = "error"
	} else if strings.Contains(line, "[WARN]") || strings.Contains(line, " WARN ") {
		entry["level"] = "warning"
	} else if strings.Contains(line, "[DEBUG]") || strings.Contains(line, " DEBUG ") {
		entry["level"] = "debug"
	} else {
		entry["level"] = "info"
	}

	// 提取消息内容
	// 移除时间戳和级别标记
	msg := line
	if idx := strings.Index(msg, "]"); idx != -1 {
		msg = strings.TrimSpace(msg[idx+1:])
	}
	// 移除级别标记
	for _, levelTag := range []string{"[INFO]", "[ERROR]", "[WARN]", "[DEBUG]", "INFO", "ERROR", "WARN", "DEBUG"} {
		msg = strings.ReplaceAll(msg, levelTag, "")
	}
	entry["message"] = strings.TrimSpace(msg)

	return entry
}

func (g *GatewayService) handleListRunningTasks(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	sessionFilter := strings.TrimSpace(r.URL.Query().Get("session_id"))
	includeHistory := parseTruthyQueryValue(r.URL.Query().Get("include_history"))
	includeResult := includeHistory && parseTruthyQueryValue(r.URL.Query().Get("include_result"))
	limit := 50
	if parsed, err := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get("limit"))); err == nil && parsed > 0 {
		limit = min(parsed, 100)
	}
	type taskSnapshot struct {
		id              string
		status          string
		request         *MessageRequest
		recoveryState   string
		resumedByTaskID string
		createdAt       time.Time
		completedAt     *time.Time
		errorText       string
		result          interface{}
	}
	snapshots := make([]taskSnapshot, 0)
	totalTasks := 0
	g.tasksMu.RLock()
	for id, task := range g.tasks {
		if task == nil || task.QueueName == agentOSQueueName {
			continue
		}
		totalTasks++
		status := task.Status
		if !includeHistory && status != "running" && status != "pending" && status != "pausing" && status != "paused" {
			continue
		}
		if sessionFilter != "" && (task.Request == nil || task.Request.SessionId != sessionFilter) {
			continue
		}
		snapshot := taskSnapshot{
			id:              id,
			status:          status,
			request:         task.Request,
			recoveryState:   task.RecoveryState,
			resumedByTaskID: task.ResumedByTaskID,
			createdAt:       task.CreatedAt,
		}
		if task.CompletedAt != nil {
			completedAt := *task.CompletedAt
			snapshot.completedAt = &completedAt
		}
		if task.Error != nil {
			snapshot.errorText = task.Error.Error()
		}
		if includeResult && task.Result != nil {
			snapshot.result = task.Result
		}
		snapshots = append(snapshots, snapshot)
	}
	g.tasksMu.RUnlock()

	runningTasks := make([]map[string]interface{}, 0)
	pendingTasks := make([]map[string]interface{}, 0)
	completedTasks := make([]map[string]interface{}, 0)
	stoppedTasks := make([]map[string]interface{}, 0)
	failedTasks := make([]map[string]interface{}, 0)
	for _, task := range snapshots {
		taskInfo := map[string]interface{}{
			"id":                 task.id,
			"status":             task.status,
			"type":               "",
			"message":            "",
			"agent_name":         "",
			"created_at":         task.createdAt,
			"duration":           "",
			"recovery_state":     task.recoveryState,
			"resumed_by_task_id": task.resumedByTaskID,
			"can_resume":         task.status == "paused" && task.recoveryState == sessioncore.RecoveryStateAvailable,
		}
		if task.request != nil {
			taskInfo["type"] = task.request.TaskType
			taskInfo["message"] = truncate(task.request.Message, 100)
			taskInfo["agent_name"] = task.request.AgentName
			taskInfo["session_id"] = task.request.SessionId
			taskInfo["user_id"] = task.request.UserId
			taskInfo["channel"] = task.request.Channel
		}
		if task.completedAt != nil {
			duration := task.completedAt.Sub(task.createdAt)
			taskInfo["duration"] = duration.String()
			taskInfo["completed_at"] = task.completedAt
		} else {
			duration := time.Since(task.createdAt)
			taskInfo["duration"] = duration.String()
		}
		if task.errorText != "" {
			taskInfo["error"] = task.errorText
		}
		if task.result != nil {
			taskInfo["result"] = task.result
		}
		switch task.status {
		case "running":
			runningTasks = append(runningTasks, taskInfo)
		case "pending":
			pendingTasks = append(pendingTasks, taskInfo)
		case "pausing":
			runningTasks = append(runningTasks, taskInfo)
		case "paused":
			pendingTasks = append(pendingTasks, taskInfo)
		case "completed":
			completedTasks = append(completedTasks, taskInfo)
		case "stopped":
			stoppedTasks = append(stoppedTasks, taskInfo)
		case "failed":
			failedTasks = append(failedTasks, taskInfo)
		}
	}
	sortTasksByCreatedTime := func(tasks []map[string]interface{}) []map[string]interface{} {
		sort.Slice(tasks, func(i, j int) bool {
			t1, ok1 := tasks[i]["created_at"].(time.Time)
			t2, ok2 := tasks[j]["created_at"].(time.Time)
			return ok1 && ok2 && t1.After(t2)
		})
		if len(tasks) > limit {
			tasks = tasks[:limit]
		}
		return tasks
	}
	runningTasks = sortTasksByCreatedTime(runningTasks)
	pendingTasks = sortTasksByCreatedTime(pendingTasks)
	completedTasks = sortTasksByCreatedTime(completedTasks)
	stoppedTasks = sortTasksByCreatedTime(stoppedTasks)
	failedTasks = sortTasksByCreatedTime(failedTasks)

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"running":   runningTasks,
		"pending":   pendingTasks,
		"completed": completedTasks,
		"stopped":   stoppedTasks,
		"failed":    failedTasks,
		"total":     totalTasks,
	})
}

func parseTruthyQueryValue(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func (g *GatewayService) handleGetTask(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// 从 URL 路径提取 taskID
	taskID := strings.TrimPrefix(r.URL.Path, "/api/task/get/")
	if taskID == "" {
		taskID = r.URL.Query().Get("id")
	}

	if taskID == "" {
		http.Error(w, "Task ID required", http.StatusBadRequest)
		return
	}

	// Copy a coherent snapshot while status/result/error share the same lock.
	task, exists := g.gatewayTaskSnapshotWithDB(taskID)
	if !exists || task.QueueName == agentOSQueueName {
		http.Error(w, "Task not found", http.StatusNotFound)
		return
	}

	// 构建响应
	response := map[string]interface{}{
		"id":                 task.ID,
		"status":             task.Status,
		"created_at":         task.CreatedAt,
		"completed_at":       task.CompletedAt,
		"recovery_state":     task.RecoveryState,
		"resumed_by_task_id": task.ResumedByTaskID,
		"can_resume":         task.Status == "paused" && task.RecoveryState == sessioncore.RecoveryStateAvailable,
	}

	// 添加请求信息
	if task.Request != nil {
		response["type"] = task.Request.TaskType
		response["message"] = task.Request.Message
		response["agent_name"] = task.Request.AgentName
		response["session_id"] = task.Request.SessionId
		response["user_id"] = task.Request.UserId
		response["channel"] = task.Request.Channel
	}

	// Preserve a usable partial answer even when the agent truthfully marks the
	// run failed. Polling clients should not turn an evidence-gap failure into
	// an empty chat bubble.
	if task.Result != nil {
		response["result"] = task.Result
	}
	if task.Status == "failed" && task.Error != nil {
		response["error"] = task.Error.Error()
	} else if task.Status == "stopped" && task.Error != nil {
		response["error"] = task.Error.Error()
	}

	// 计算耗时
	if task.CompletedAt != nil {
		duration := task.CompletedAt.Sub(task.CreatedAt)
		response["duration"] = duration.String()
	} else {
		duration := time.Since(task.CreatedAt)
		response["duration"] = duration.String()
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(response)
}

func (g *GatewayService) handleStopTaskByID(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost && r.Method != http.MethodDelete {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// 从 URL 路径提取 taskID
	path := r.URL.Path
	taskID := strings.TrimPrefix(path, "/api/task/stop/")

	// 如果还有斜杠，取最后一部分
	if strings.Contains(taskID, "/") {
		parts := strings.Split(taskID, "/")
		taskID = parts[len(parts)-1]
	}

	if taskID == "" {
		// 尝试从查询参数获取
		taskID = r.URL.Query().Get("id")
	}

	if taskID == "" {
		// 尝试从请求体获取
		var req struct {
			TaskID string `json:"task_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err == nil && req.TaskID != "" {
			taskID = req.TaskID
		}
	}

	if taskID == "" {
		http.Error(w, `{"error": "Task ID required"}`, http.StatusBadRequest)
		return
	}

	g.logger.Printf("尝试停止任务: %s", taskID)

	if g.isAgentOSTaskID(taskID) {
		http.NotFound(w, r)
		return
	}
	task, exists, stopped := g.stopGatewayTask(taskID)
	if !exists {
		http.Error(w, fmt.Sprintf(`{"error": "Task %s not found"}`, taskID), http.StatusNotFound)
		return
	}
	if !stopped {
		http.Error(w, fmt.Sprintf(`{"error": "Task %s cannot be stopped, current status: %s"}`, taskID, task.Status), http.StatusBadRequest)
		return
	}

	g.logger.Printf("任务 %s 已停止", taskID)

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":    "stopped",
		"task_id":   taskID,
		"timestamp": time.Now(),
	})
}

func (g *GatewayService) handleBatchStopTasks(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		TaskIDs []string `json:"task_ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if len(req.TaskIDs) == 0 {
		http.Error(w, "No task IDs provided", http.StatusBadRequest)
		return
	}

	results := make([]map[string]interface{}, 0)
	for _, taskID := range req.TaskIDs {
		result := map[string]interface{}{
			"task_id": taskID,
		}
		if g.isAgentOSTaskID(taskID) {
			result["status"] = "not_found"
			result["error"] = "task not found"
			results = append(results, result)
			continue
		}

		task, exists, stopped := g.stopGatewayTask(taskID)
		if !exists {
			result["status"] = "not_found"
			result["error"] = "task not found"
			results = append(results, result)
			continue
		}

		if !stopped {
			result["status"] = "failed"
			result["error"] = fmt.Sprintf("task cannot be stopped, current status: %s", task.Status)
			results = append(results, result)
			continue
		}

		result["status"] = "stopped"
		g.logger.Printf("任务 %s 已停止", taskID)
		results = append(results, result)
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"results": results,
		"count":   len(results),
	})
}

// handleProgressLogDownload serves the accumulated progress log
func (g *GatewayService) handleProgressLogDownload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	taskID := strings.TrimPrefix(r.URL.Path, "/api/task/progress-log/")
	if taskID == "" {
		http.Error(w, "task ID required", http.StatusBadRequest)
		return
	}
	if g.isAgentOSTaskID(taskID) {
		http.NotFound(w, r)
		return
	}
	g.taskProgressLogsMu.RLock()
	logs := g.taskProgressLogs[taskID]
	// Debug: show all available keys
	if len(logs) == 0 {
		var keys []string
		for k := range g.taskProgressLogs {
			keys = append(keys, k)
		}
		log.Printf("[PROGRESS_DL] task=%s not found, available keys: %v", taskID, keys)
	}
	g.taskProgressLogsMu.RUnlock()
	content := ""
	filename := fmt.Sprintf("progress_%s.log", time.Now().Format("20060102_150405"))
	if len(logs) > 0 {
		content = strings.Join(logs, "\n")
	} else if diskContent, diskFilename, ok := loadTaskProgressLogFromDiskInDir(taskID, g.progressLogDir()); ok {
		content = diskContent
		filename = diskFilename
	} else {
		content = "(log not available - server restarted)"
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
	w.Write([]byte(content))
}

func loadTaskProgressLogFromDisk(taskID string) (string, string, bool) {
	return loadTaskProgressLogFromDiskInDir(taskID, filepath.Join(".openkitty", "logs"))
}

func (g *GatewayService) progressLogDir() string {
	workspace := "."
	runtimeDir := ""
	if g != nil && g.config != nil {
		if trimmed := strings.TrimSpace(g.config.Workspace); trimmed != "" {
			workspace = trimmed
		}
		runtimeDir = strings.TrimSpace(g.config.Runtime.RuntimeDir)
	}
	return kittyRuntimeDir(workspace, runtimeDir, filepath.Join(".openkitty", "logs"))
}

func loadTaskProgressLogFromDiskInDir(taskID string, logDir string) (string, string, bool) {
	if !isSafeTaskLogID(taskID) {
		return "", "", false
	}
	if strings.TrimSpace(logDir) == "" {
		logDir = filepath.Join(".openkitty", "logs")
	}
	candidates := []string{
		filepath.Join(logDir, "progress", taskID+".log"),
		filepath.Join(logDir, "progress_"+taskID+".log"),
	}
	for _, path := range candidates {
		data, err := os.ReadFile(path)
		if err != nil || len(data) == 0 {
			continue
		}
		return string(data), fmt.Sprintf("progress_%s.log", taskID), true
	}
	return "", "", false
}

func isSafeTaskLogID(taskID string) bool {
	if taskID == "" {
		return false
	}
	for _, r := range taskID {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' || r == '.' {
			continue
		}
		return false
	}
	return true
}

func splitSessionTaskLogID(taskID string) (string, string, bool) {
	idx := strings.LastIndex(taskID, "-")
	if idx <= 0 || idx >= len(taskID)-1 {
		return "", "", false
	}
	return taskID[:idx], taskID[idx+1:], true
}

func (g *GatewayService) nextSessionTaskID(sessionID string) string {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		sessionID = fmt.Sprintf("loc_%d", time.Now().UnixNano())
	}

	g.taskIDMu.Lock()
	defer g.taskIDMu.Unlock()

	if g.sessionTaskCounters == nil {
		g.sessionTaskCounters = make(map[string]int64)
	}

	maxSeq := g.sessionTaskCounters[sessionID]
	if g.db != nil {
		var taskIDs []string
		if err := g.db.Model(&ChatMessage{}).
			Where("session_id = ? AND task_id <> ''", sessionID).
			Distinct("task_id").
			Pluck("task_id", &taskIDs).Error; err == nil {
			for _, id := range taskIDs {
				if seq, ok := sessionTaskSequence(id, sessionID); ok && seq > maxSeq {
					maxSeq = seq
				}
			}
		}
	}

	nextSeq := maxSeq + 1
	g.sessionTaskCounters[sessionID] = nextSeq
	return fmt.Sprintf("%s-t%d", sessionID, nextSeq)
}

func sessionTaskSequence(taskID string, sessionID string) (int64, bool) {
	idSession, taskNum, ok := splitSessionTaskLogID(taskID)
	if !ok || idSession != sessionID || !strings.HasPrefix(taskNum, "t") || len(taskNum) <= 1 {
		return 0, false
	}
	seq, err := strconv.ParseInt(taskNum[1:], 10, 64)
	if err != nil || seq <= 0 {
		return 0, false
	}
	return seq, true
}

func (g *GatewayService) handleCreateTodo(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Title        string `json:"title"`
		Description  string `json:"description"`
		Priority     string `json:"priority"`
		AgentName    string `json:"agent_name"`
		ScheduleType string `json:"schedule_type"`
		RunAt        string `json:"run_at"`
		CronExpr     string `json:"cron_expr"`
		IntervalSec  int    `json:"interval_sec"`
		InputData    string `json:"input_data"`
		MaxRetries   int    `json:"max_retries"`
		PipelineName string `json:"pipeline_name"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}

	// 获取当前用户信息
	userID := r.Header.Get("X-User-ID")
	if userID == "" {
		userID = "anonymous"
	}

	todo := &todocore.Todo{
		Title:        req.Title,
		Description:  req.Description,
		Priority:     todocore.TodoPriority(req.Priority),
		AgentName:    req.AgentName,
		ScheduleType: req.ScheduleType,
		InputData:    req.InputData,
		MaxRetries:   req.MaxRetries,
		PipelineName: req.PipelineName,
		CreatedBy:    userID,
		Channel:      "api",
	}

	if todo.ScheduleType == "" {
		if req.CronExpr != "" {
			todo.ScheduleType = "cron"
		} else {
			todo.ScheduleType = "once"
		}
	}

	if todo.Priority == "" {
		todo.Priority = todocore.PriorityNormal
	}

	if todo.MaxRetries <= 0 {
		todo.MaxRetries = 3
	}

	if req.RunAt != "" {
		runAt, err := todocore.ParseTime(req.RunAt)
		if err != nil {
			runAt = time.Now()
		}
		todo.RunAt = &runAt
	}

	if req.CronExpr != "" {
		todo.CronExpr = req.CronExpr
	}

	if req.IntervalSec > 0 {
		todo.IntervalSec = req.IntervalSec
	}

	created, err := g.todoManager.CreateTodo(todo)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(created)
}

// handleListTodos 列出TODO
func (g *GatewayService) handleListTodos(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	agentName := r.URL.Query().Get("agent")
	teamName := r.URL.Query().Get("team")
	status := r.URL.Query().Get("status")
	limit := 20
	if l, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && l > 0 {
		limit = l
	}

	todos, err := g.todoManager.ListTodos(agentName, teamName, todocore.TodoStatus(status), limit)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"todos": todos,
		"count": len(todos),
	})
}

func (g *GatewayService) handleTodoByID(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/todo/")
	if path == "" {
		http.Error(w, "Todo ID required", http.StatusBadRequest)
		return
	}

	// Check if requesting logs: /api/todo/{id}/logs
	if strings.HasSuffix(path, "/logs") {
		todoID := strings.TrimSuffix(path, "/logs")
		if r.Method != http.MethodGet {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		logs, err := g.todoManager.GetExecutionLogs(todoID, 50)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"todo_id": todoID,
			"logs":    logs,
			"count":   len(logs),
		})
		return
	}

	todoID := path

	switch r.Method {
	case http.MethodGet:
		todo, err := g.todoManager.GetTodo(todoID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		json.NewEncoder(w).Encode(todo)

	case http.MethodPut:
		var updates map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&updates); err != nil {
			http.Error(w, "Invalid request body", http.StatusBadRequest)
			return
		}
		todo, err := g.todoManager.UpdateTodo(todoID, updates)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		json.NewEncoder(w).Encode(todo)

	case http.MethodDelete:
		// 方案1: 物理删除（直接从数据库删除记录）
		err := g.todoManager.DeleteTodo(todoID) // 需要实现这个方法
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status":  "deleted",
			"todo_id": todoID,
		})

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func (g *GatewayService) handleListFiles(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	requestedPath := r.URL.Query().Get("path")
	if strings.TrimSpace(requestedPath) == "" {
		requestedPath = "/"
	}
	fullPath, err := projectctx.ResolveLegacyWorkspacePath(g.attachmentWorkspaceRoot(), requestedPath)
	if err != nil {
		if g.logger != nil {
			g.logger.Printf("拒绝列出路径 %q: %v", requestedPath, err)
		}
		sendJSONError(w, "Access denied", http.StatusForbidden)
		return
	}

	// 目录不存在时返回空列表
	info, err := os.Stat(fullPath)
	if os.IsNotExist(err) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"files": []interface{}{},
			"path":  requestedPath,
		})
		return
	}
	if err != nil {
		sendJSONError(w, "Failed to inspect directory", http.StatusInternalServerError)
		return
	}

	if !info.IsDir() {
		sendJSONError(w, "Path is not a directory", http.StatusBadRequest)
		return
	}

	// 读取目录内容
	entries, err := os.ReadDir(fullPath)
	if err != nil {
		g.logger.Printf("读取目录失败 %s: %v", fullPath, err)
		sendJSONError(w, "Failed to read directory", http.StatusInternalServerError)
		return
	}

	// 构建文件列表
	type FileInfo struct {
		Name    string    `json:"name"`
		IsDir   bool      `json:"is_dir"`
		Size    int64     `json:"size"`
		ModTime time.Time `json:"mod_time"`
	}

	var files []FileInfo
	for _, entry := range entries {
		// Do not even disclose the reserved subtree or a symlink alias to it.
		// Any unexpected resolution error fails the whole listing closed.
		if err := projectctx.RejectReservedTenantWorkspacePath(
			g.attachmentWorkspaceRoot(),
			filepath.Join(fullPath, entry.Name()),
		); err != nil {
			if errors.Is(err, projectctx.ErrReservedTenantWorkspacePath) {
				continue
			}
			if g.logger != nil {
				g.logger.Printf("拒绝列出目录项 %q: %v", entry.Name(), err)
			}
			sendJSONError(w, "Access denied", http.StatusForbidden)
			return
		}
		fileInfo, err := entry.Info()
		if err != nil {
			g.logger.Printf("获取文件信息失败 %s: %v", entry.Name(), err)
			continue
		}

		files = append(files, FileInfo{
			Name:    entry.Name(),
			IsDir:   entry.IsDir(),
			Size:    fileInfo.Size(),
			ModTime: fileInfo.ModTime(),
		})
	}

	// 排序：目录优先，然后按修改时间降序（最新在前）
	sort.Slice(files, func(i, j int) bool {
		if files[i].IsDir != files[j].IsDir {
			return files[i].IsDir
		}
		return files[i].ModTime.After(files[j].ModTime)
	})

	// 返回JSON响应
	response := map[string]interface{}{
		"path":  requestedPath,
		"files": files,
	}

	if err := json.NewEncoder(w).Encode(response); err != nil {
		g.logger.Printf("编码响应失败: %v", err)
	}

	g.logger.Printf("列出目录 %s: %d 个项目", fullPath, len(files))
}

func (g *GatewayService) handleDownloadFile(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// 获取文件路径
	filePath := r.URL.Query().Get("path")
	if filePath == "" {
		http.Error(w, "File path required", http.StatusBadRequest)
		return
	}
	fullPath, err := projectctx.ResolveLegacyWorkspacePath(g.attachmentWorkspaceRoot(), filePath)
	if err != nil {
		if g.logger != nil {
			g.logger.Printf("拒绝下载 %q: %v", filePath, err)
		}
		http.Error(w, "Access denied", http.StatusForbidden)
		return
	}

	// 检查文件是否存在
	info, err := os.Stat(fullPath)
	if os.IsNotExist(err) {
		g.logger.Printf("文件不存在: %s", fullPath)
		http.Error(w, "File not found", http.StatusNotFound)
		return
	}
	if err != nil {
		g.logger.Printf("检查文件失败 %s: %v", fullPath, err)
		http.Error(w, "Failed to inspect file", http.StatusInternalServerError)
		return
	}

	// 如果是目录，不允许下载
	if info.IsDir() {
		g.logger.Printf("尝试下载目录: %s", fullPath)
		http.Error(w, "Cannot download directory", http.StatusBadRequest)
		return
	}

	// 打开文件
	file, err := os.Open(fullPath)
	if err != nil {
		g.logger.Printf("打开文件失败 %s: %v", fullPath, err)
		http.Error(w, "Failed to open file", http.StatusInternalServerError)
		return
	}
	defer file.Close()

	// 设置响应头
	fileName := filepath.Base(fullPath)
	// RFC 5987: support Chinese/non-ASCII filenames
	encodedName := strings.ReplaceAll(url.QueryEscape(fileName), "+", "%20")
	contentType := mime.TypeByExtension(strings.ToLower(filepath.Ext(fileName)))
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	disposition := "attachment"
	if r.URL.Query().Get("inline") == "1" && isInlineSafeImageMIME(contentType) {
		disposition = "inline"
	}
	w.Header().Set("Content-Disposition", fmt.Sprintf(`%s; filename="%s"; filename*=UTF-8''%s`, disposition, fileName, encodedName))
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Length", fmt.Sprintf("%d", info.Size()))

	// 传输文件
	_, err = io.Copy(w, file)
	if err != nil {
		g.logger.Printf("传输文件失败 %s: %v", fullPath, err)
		// 注意：这里已经发送了部分数据，无法再返回错误
	}

	g.logger.Printf("文件下载完成: %s (%d bytes)", fullPath, info.Size())
}

// handleUpload handles file uploads to the configured workspace storage directory.
func (g *GatewayService) handleUpload(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}

	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Limit to 50MB
	r.Body = http.MaxBytesReader(w, r.Body, 50<<20)

	if err := r.ParseMultipartForm(50 << 20); err != nil {
		http.Error(w, "File too large (max 50MB)", http.StatusBadRequest)
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "No file provided", http.StatusBadRequest)
		return
	}
	defer file.Close()

	attachment, err := attachmentcore.SaveWorkspaceUpload(g.attachmentWorkspaceRoot(), header.Filename, header.Header.Get("Content-Type"), file)
	if err != nil {
		g.logger.Printf("保存上传文件失败: %v", err)
		sendJSONError(w, "Failed to save file", http.StatusInternalServerError)
		return
	}

	g.logger.Printf("文件上传成功: %s (%d bytes, %s)", attachment.Path, attachment.Size, header.Filename)

	// Return file info as JSON
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":  true,
		"filename": attachment.Name,
		"path":     attachment.Path,
		"size":     attachment.Size,
		"type":     attachment.MIMEType,
		"kind":     attachment.Kind,
	})
}

func sendJSONError(w http.ResponseWriter, message string, statusCode int) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(map[string]string{"error": message})
}

func (g *GatewayService) currentProjectWorkspace() string {
	if g == nil {
		return projectctx.NormalizeWorkspace(".")
	}
	if strings.TrimSpace(g.project.Workspace) != "" {
		return projectctx.NormalizeWorkspace(g.project.Workspace)
	}
	if g.config != nil && strings.TrimSpace(g.config.Workspace) != "" {
		return projectctx.NormalizeWorkspace(g.config.Workspace)
	}
	return projectctx.NormalizeWorkspace(".")
}

func newServerWebSessionID(now time.Time) string {
	var suffix [3]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return fmt.Sprintf("web_%s_%x", now.Format("060102150405"), now.UnixNano()&0xffffff)
	}
	return "web_" + now.Format("060102150405") + "_" + hex.EncodeToString(suffix[:])
}

func (g *GatewayService) bindSessionWorkspace(sessionID, agentName, userID string, allowCreate bool) (string, error) {
	return g.bindSessionWorkspaceAt(sessionID, agentName, userID, g.currentProjectWorkspace(), allowCreate)
}

func (g *GatewayService) bindSessionWorkspaceAt(sessionID, agentName, userID, requestedWorkspace string, allowCreate bool) (string, error) {
	return g.bindSessionWorkspaceOwnedWithUserAt(
		sessionID,
		agentName,
		defaultExecutionTenantID,
		"",
		userID,
		requestedWorkspace,
		allowCreate,
	)
}

func (g *GatewayService) bindSessionWorkspaceOwnedAt(
	sessionID, agentName, tenantID, subjectID, requestedWorkspace string,
	allowCreate bool,
) (string, error) {
	return g.bindSessionWorkspaceOwnedWithUserAt(
		sessionID,
		agentName,
		tenantID,
		subjectID,
		subjectID,
		requestedWorkspace,
		allowCreate,
	)
}

func (g *GatewayService) bindSessionWorkspaceOwnedWithUserAt(
	sessionID, agentName, tenantID, subjectID, userID, requestedWorkspace string,
	allowCreate bool,
) (string, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return "", fmt.Errorf("session_id is required")
	}
	tenantID = normalizePersonalOwnerTenantID(tenantID)
	subjectID = strings.TrimSpace(subjectID)
	workspace := projectctx.NormalizeWorkspace(requestedWorkspace)
	project := projectctx.New(workspace)
	if g.db == nil {
		if subjectID != "" {
			return "", fmt.Errorf("personal session ownership requires a database")
		}
		return workspace, nil
	}

	var session ChatSession
	result := g.db.Where("id = ?", sessionID).Limit(1).Find(&session)
	if result.Error != nil {
		return "", result.Error
	}
	if result.RowsAffected == 0 {
		if !allowCreate {
			return "", fmt.Errorf("unknown web session %q; create it with POST /api/sessions", sessionID)
		}
		session = ChatSession{
			ID: sessionID, Name: sessionID, AgentName: agentName, UserId: userID,
			TenantID: tenantID, SubjectID: subjectID,
			Workspace: workspace, ProjectID: project.ProjectID,
			CreatedAt: time.Now(), UpdatedAt: time.Now(),
		}
		if err := g.db.Create(&session).Error; err != nil {
			return "", err
		}
		g.broadcastSessionListChanged()
		return workspace, nil
	}
	if !samePersistedPersonalOwner(session.TenantID, session.SubjectID, tenantID, subjectID) {
		return "", fmt.Errorf("%w: session %q owner is immutable", ErrSessionOwnerConflict, sessionID)
	}

	bound := strings.TrimSpace(session.Workspace)
	if bound == "" {
		updates := map[string]interface{}{"workspace": workspace, "project_id": project.ProjectID}
		if err := g.db.Model(&session).Updates(updates).Error; err != nil {
			return "", err
		}
		return workspace, nil
	}
	bound = projectctx.NormalizeWorkspace(bound)
	if !projectctx.SameWorkspace(bound, workspace) {
		return "", fmt.Errorf("session %q is bound to workspace %q and cannot run in %q", sessionID, bound, workspace)
	}
	return bound, nil
}

func (g *GatewayService) handleCreateSession(w http.ResponseWriter, r *http.Request) {
	if g.db == nil {
		http.Error(w, "数据库未初始化", http.StatusInternalServerError)
		return
	}
	var req struct {
		AgentName string `json:"agent_name"`
		UserID    string `json:"user_id"`
	}
	if r.Body != nil {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && err != io.EOF {
			sendJSONError(w, "Invalid request body", http.StatusBadRequest)
			return
		}
	}
	workspace := g.currentProjectWorkspace()
	var session ChatSession
	for attempt := 0; attempt < 4; attempt++ {
		session = ChatSession{
			ID: newServerWebSessionID(time.Now()), AgentName: strings.TrimSpace(req.AgentName),
			UserId: strings.TrimSpace(req.UserID), Workspace: workspace, ProjectID: g.project.ProjectID,
			CreatedAt: time.Now(), UpdatedAt: time.Now(),
		}
		session.Name = session.ID
		if err := g.db.Create(&session).Error; err == nil {
			g.broadcastSessionListChanged()
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(map[string]interface{}{"session": session, "project": g.project})
			return
		}
	}
	sendJSONError(w, "failed to create session", http.StatusInternalServerError)
}

func (g *GatewayService) handleListSessions(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		g.handleCreateSession(w, r)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if g.db == nil {
		http.Error(w, "数据库未初始化", http.StatusInternalServerError)
		return
	}

	limit := 50
	if l, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && l > 0 && l <= 200 {
		limit = l
	}

	var total int64
	agentOSSessions := g.db.Model(&GatewayTaskRecord{}).
		Select("session_id").
		Where("queue_name = ? AND session_id IS NOT NULL AND session_id <> ''", agentOSQueueName)
	sessionsQuery := scopeRowsToPersistedOwner(
		g.db.Model(&ChatSession{}),
		defaultExecutionTenantID,
		"",
	).
		Where("project_id = ?", g.project.ProjectID).
		Where("media_owner = '' OR media_owner IS NULL OR media_owner = ?", g.mediaOwner(r)).
		Where("id NOT IN (?)", agentOSSessions)
	sessionsQuery.Count(&total)

	var sessions []ChatSession
	sessionsQuery.
		Select(`chat_sessions.*,
			COALESCE((
				SELECT SUBSTR(chat_messages.content, 1, 240)
				FROM chat_messages
				WHERE chat_messages.session_id = chat_sessions.id
					AND chat_messages.role = ?
					AND chat_messages.tenant_id = 'default'
					AND (chat_messages.subject_id IS NULL OR chat_messages.subject_id = '')
				ORDER BY chat_messages.created_at ASC, chat_messages.id ASC
				LIMIT 1
			), '') AS first_question`, "user").
		Order("updated_at desc").
		Limit(limit).
		Find(&sessions)

	g.logger.Printf("返回 %d/%d 个会话", len(sessions), total)

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"sessions": sessions,
		"count":    len(sessions),
		"total":    total,
		"has_more": total > int64(limit),
	})
}

func (g *GatewayService) handleSessionMessages(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	sessionID := strings.TrimPrefix(r.URL.Path, "/api/session/")
	if sessionID == "" {
		http.Error(w, "Session ID required", http.StatusBadRequest)
		return
	}

	if g.db == nil {
		http.Error(w, "数据库未初始化", http.StatusInternalServerError)
		return
	}
	if g.isAgentOSSessionID(sessionID) {
		http.NotFound(w, r)
		return
	}

	// Pagination: default 200, max 2000
	limit := 200
	if l, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && l > 0 {
		if l > 2000 {
			l = 2000
		}
		limit = l
	}

	// Get total count first
	var total int64
	messageQuery := scopeRowsToPersistedOwner(
		g.db.Model(&ChatMessage{}).Where("media_owner = '' OR media_owner IS NULL OR media_owner = ?", g.mediaOwner(r)),
		defaultExecutionTenantID,
		"",
	)
	messageQuery.
		Where("session_id = ? AND NOT (role = ? AND content LIKE ?)", sessionID, "assistant", "%已接收任务。第一步将先理解意图%").
		Count(&total)

	// Fetch last N messages. A request can persist user and assistant messages
	// at the same timestamp; ID preserves their submission order before reversal.
	var messages []ChatMessage
	scopeRowsToPersistedOwner(
		g.db.Where("session_id = ? AND NOT (role = ? AND content LIKE ?)", sessionID, "assistant", "%已接收任务。第一步将先理解意图%").Where("media_owner = '' OR media_owner IS NULL OR media_owner = ?", g.mediaOwner(r)),
		defaultExecutionTenantID,
		"",
	).
		Order("created_at desc, id desc").Limit(limit).
		Find(&messages)

	// Reverse to chronological order
	for i, j := 0, len(messages)-1; i < j; i, j = i+1, j-1 {
		messages[i], messages[j] = messages[j], messages[i]
	}
	for i := range messages {
		if messages[i].Role == "assistant" {
			messages[i].Content = stripLegacyAEPipelineDiagnosticPrefix(messages[i].Content)
		}
	}

	// 计算会话耗时
	var durationSec float64
	if total >= 2 {
		var first, last ChatMessage
		scopeRowsToPersistedOwner(
			g.db.Where("session_id = ?", sessionID).Where("media_owner = '' OR media_owner IS NULL OR media_owner = ?", g.mediaOwner(r)),
			defaultExecutionTenantID,
			"",
		).Order("created_at asc, id asc").Limit(1).Find(&first)
		scopeRowsToPersistedOwner(
			g.db.Where("session_id = ?", sessionID).Where("media_owner = '' OR media_owner IS NULL OR media_owner = ?", g.mediaOwner(r)),
			defaultExecutionTenantID,
			"",
		).Order("created_at desc, id desc").Limit(1).Find(&last)
		durationSec = last.CreatedAt.Sub(first.CreatedAt).Seconds()
	}
	recoveries, recoveryErr := g.listInterruptedTaskRecoveries(sessionID, 20)
	if recoveryErr != nil && g.logger != nil {
		g.logger.Printf("查询会话恢复点失败: %v", recoveryErr)
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"session_id":   sessionID,
		"messages":     messages,
		"count":        len(messages),
		"total":        total,
		"has_more":     total > int64(limit),
		"duration_sec": durationSec,
		"recoveries":   recoveries,
	})
}

func stripLegacyAEPipelineDiagnosticPrefix(content string) string {
	trimmed := strings.TrimSpace(content)
	if !strings.HasPrefix(trimmed, "AI员工任务已产生阶段性结果，但存在降级/异常：") {
		return content
	}
	// Older builds persisted raw step/provider diagnostics before the actual
	// Markdown delivery. Keep those diagnostics in execution audit data, but
	// make historical chat sessions delivery-first as well.
	if index := strings.Index(trimmed, "\n\n# "); index >= 0 {
		return strings.TrimSpace(trimmed[index+2:])
	}
	return content
}

func buildExportHTML(sessionID string, messages []ChatMessage) string {
	var sb strings.Builder
	exportSessionID := redactAccountInfoTextForExport(sessionID)
	fmt.Fprintf(&sb, `<!DOCTYPE html><html><head><meta charset="UTF-8"><title>%s</title><style>
*{margin:0;padding:0;box-sizing:border-box}
body{font-family:-apple-system,BlinkMacSystemFont,"Segoe UI","Microsoft YaHei",sans-serif;font-size:14px;color:#1e293b;line-height:1.6;max-width:900px;margin:0 auto;padding:20px;background:#fafaf9}
h1{font-size:18px;color:#6366f1;margin-bottom:4px;padding-bottom:8px;border-bottom:2px solid #e2e8f0}
h2{font-size:13px;color:#94a3b8;font-weight:400;margin-bottom:20px}
.msg{margin-bottom:16px;padding:14px 18px;border-radius:12px;break-inside:avoid;max-width:80%%}
.msg.user{background:#eef2ff;border:1px solid #c7d2fe;margin-left:auto;text-align:right;border-bottom-right-radius:4px}
.msg.assistant{background:#fff;border:1px solid #e2e8f0;box-shadow:0 1px 3px rgba(0,0,0,0.04);border-bottom-left-radius:4px}
.role{font-size:12px;font-weight:600;margin-bottom:6px}
.user .role{color:#4f46e5}
.assistant .role{color:#0f172a}
.time{font-size:10px;color:#94a3b8;margin-left:8px;font-weight:400}
.content{word-wrap:break-word;overflow-wrap:break-word}
.content pre{background:#1e293b;color:#cbd5e1;padding:12px;border-radius:6px;overflow-x:auto;font-size:12px;margin:6px 0;white-space:pre-wrap}
.content code{background:#f1f5f9;padding:1px 4px;border-radius:3px;font-size:0.9em}
.content table{border-collapse:collapse;width:100%%;margin:8px 0;font-size:12px}
.content th,.content td{border:1px solid #d1d5db;padding:6px 10px;text-align:left}
.content th{background:#f1f5f9}
.content blockquote{border-left:3px solid #94a3b8;padding:4px 12px;margin:6px 0;color:#475569}
.content img{max-width:100%%}
.content hr{border:none;border-top:1px solid #e2e8f0;margin:8px 0}
.footer{text-align:center;color:#94a3b8;font-size:11px;margin-top:30px;padding-top:12px;border-top:1px solid #e2e8f0}
</style></head><body>
<h1>macs Conversation</h1>
<h2>Session: %s | %d messages | %s</h2>
`, html.EscapeString(exportSessionID), html.EscapeString(exportSessionID), len(messages), time.Now().Format("2006-01-02 15:04"))

	for _, msg := range messages {
		roleLabel := "You"
		roleClass := "user"
		if msg.Role == "assistant" {
			roleLabel = msg.AgentName
			if roleLabel == "" {
				roleLabel = "AI"
			}
			roleClass = "assistant"
		}
		roleLabel = redactAccountInfoTextForExport(roleLabel)
		content := redactAccountInfoTextForExport(msg.Content)
		if len(content) > 8000 {
			content = content[:8000] + "\n\n... (truncated)"
		}
		generation := msg.Generation
		if generation != nil {
			snapshot := *generation
			snapshot.ModelName = redactAccountInfoTextForExport(snapshot.ModelName)
			snapshot.Task.Title = redactAccountInfoTextForExport(snapshot.Task.Title)
			snapshot.Task.Lyrics = redactAccountInfoTextForExport(snapshot.Task.Lyrics)
			generation = &snapshot
		}
		content = strings.ReplaceAll(html.EscapeString(content), "\n", "<br>") + conversation.ExportGenerationHTML(generation)
		fmt.Fprintf(&sb, `<div class="msg %s"><div class="role">%s<span class="time">%s</span></div><div class="content">%s</div></div>`,
			roleClass, html.EscapeString(roleLabel), msg.CreatedAt.Format("01-02 15:04"), content)
	}

	fmt.Fprintf(&sb, `<div class="footer">Generated by macs</div></body></html>`)
	return sb.String()
}

func (g *GatewayService) handleSessionExport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/session/export/")
	sessionID := path
	asImage := strings.HasSuffix(path, "/image")
	if asImage {
		sessionID = strings.TrimSuffix(path, "/image")
	}

	if sessionID == "" || g.db == nil {
		http.Error(w, "Session ID required", http.StatusBadRequest)
		return
	}
	if g.isAgentOSSessionID(sessionID) {
		http.NotFound(w, r)
		return
	}

	var messages []ChatMessage
	scopeRowsToPersistedOwner(
		g.db.Where("session_id = ? AND role IN ('user', 'assistant')", sessionID).Where("media_owner = '' OR media_owner IS NULL OR media_owner = ?", g.mediaOwner(r)),
		defaultExecutionTenantID,
		"",
	).
		Order("created_at asc, id asc").Limit(5000).
		Find(&messages)

	if len(messages) == 0 {
		http.Error(w, "No messages", http.StatusNotFound)
		return
	}

	for i := range messages {
		generation := messages[i].Generation
		if generation == nil || generation.Capability != "image" || generation.Task.Status != "completed" || generation.Task.ResultURL == "" {
			continue
		}
		if g.accountHandler == nil {
			http.Error(w, "Image unavailable", http.StatusBadGateway)
			return
		}
		data, mime, code := g.accountHandler.ReadPlatformImage(r.Context(), generation.Task.ResultURL)
		if code != "" {
			http.Error(w, "Image unavailable; please retry", http.StatusBadGateway)
			return
		}
		snapshot := *generation
		snapshot.Task.ResultURL = "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data)
		messages[i].Generation = &snapshot
	}
	html := buildExportHTML(sessionID, messages)

	if asImage {
		// Save HTML to temp file, use chromedp to screenshot
		file, err := os.CreateTemp("", "macs-export-*.html")
		if err != nil {
			http.Error(w, "Export unavailable", http.StatusInternalServerError)
			return
		}
		tmpFile := file.Name()
		defer os.Remove(tmpFile)
		if _, err := file.WriteString(html); err != nil {
			file.Close()
			http.Error(w, "Export unavailable", http.StatusInternalServerError)
			return
		}
		if err := file.Close(); err != nil {
			http.Error(w, "Export unavailable", http.StatusInternalServerError)
			return
		}

		exportCtx, cancelExport := context.WithTimeout(r.Context(), 45*time.Second)
		defer cancelExport()
		tabCtx, cancel := chromedp.NewContext(exportCtx)
		defer cancel()

		var buf []byte
		if err := chromedp.Run(tabCtx,
			chromedp.Navigate("file:///"+strings.ReplaceAll(tmpFile, "\\", "/")),
			chromedp.WaitReady("body"),
			chromedp.Poll(`Array.from(document.images).every(image => image.complete && image.naturalWidth > 0)`, nil, chromedp.WithPollingTimeout(20*time.Second)),
			chromedp.FullScreenshot(&buf, 90),
		); err != nil {
			// Fallback: return HTML
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Write([]byte(html))
			return
		}

		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=openkitty_%s.png", sessionID))
		w.Write(buf)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=openkitty_%s.html", sessionID))
	w.Write([]byte(html))
}

func (g *GatewayService) handleConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		// Auto-recover if config was deleted: try FindConfig, fallback to generate new
		if _, err := os.Stat(g.configpath); os.IsNotExist(err) {
			if found, ferr := FindConfig(""); ferr == nil {
				g.configpath = found
			} else {
				homeCfg := filepath.Join(OpenKittyHome(), "openkitty-config.yaml")
				os.MkdirAll(OpenKittyHome(), 0755)
				os.WriteFile(homeCfg, defaultConfigYAML, 0644)
				g.configpath = homeCfg
			}
		}
		config, err := LoadConfig(g.configpath)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		json.NewEncoder(w).Encode(redactedConfigAPIValue(config))

	case http.MethodPut, http.MethodPost:
		var updates map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&updates); err != nil {
			http.Error(w, "Invalid request body", http.StatusBadRequest)
			return
		}

		// 调用完整的配置更新方法
		if err := g.UpdateFullConfig(updates); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status":    "success",
			"message":   "配置已更新",
			"timestamp": time.Now(),
		})

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func redactedConfigAPIValue(value interface{}) interface{} {
	data, err := json.Marshal(value)
	if err != nil {
		return map[string]interface{}{}
	}
	var decoded interface{}
	if err := json.Unmarshal(data, &decoded); err != nil {
		return map[string]interface{}{}
	}
	redactSensitiveConfigFields(decoded)
	data, err = json.Marshal(decoded)
	if err != nil {
		return decoded
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		return map[string]interface{}{}
	}
	return decoded
}

func redactSensitiveConfigFields(value interface{}) {
	switch typed := value.(type) {
	case map[string]interface{}:
		for key, item := range typed {
			if isSensitiveConfigField(key) {
				if text, ok := item.(string); ok && text != "" {
					typed[key] = "[REDACTED]"
				}
				continue
			}
			redactSensitiveConfigFields(item)
		}
	case []interface{}:
		for _, item := range typed {
			redactSensitiveConfigFields(item)
		}
	}
}

func isSensitiveConfigField(key string) bool {
	normalized := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(key), "-", "_"))
	switch normalized {
	case "api_key", "apikey", "api_secret", "secret_key", "app_secret", "client_secret",
		"access_token", "refresh_token", "auth_token", "token", "password", "passwd",
		"cookie", "private_key", "x_api_key":
		return true
	}
	return strings.HasSuffix(normalized, "_secret") ||
		strings.HasSuffix(normalized, "_password") ||
		strings.HasSuffix(normalized, "_token")
}

func (g *GatewayService) UpdateFullConfig(updates map[string]interface{}) error {
	g.mutex.Lock()
	defer g.mutex.Unlock()

	g.logger.Println("开始更新配置...")

	// 1. 读取当前配置
	currentConfig, err := LoadConfig(g.configpath)
	if err != nil {
		return fmt.Errorf("读取配置失败: %w", err)
	}
	preserveRuntimeOnlyConfig(currentConfig, g.config)

	// 2. 合并更新
	if err := g.mergeConfig(currentConfig, updates); err != nil {
		return fmt.Errorf("合并配置失败: %w", err)
	}

	// 3. 验证配置
	if err := g.validateConfig(currentConfig); err != nil {
		return fmt.Errorf("配置验证失败: %w", err)
	}

	// 4. 保存到文件
	if err := SaveConfig(g.configpath, currentConfig); err != nil {
		return fmt.Errorf("保存配置失败: %w", err)
	}

	// 5. 热更新运行中的组件
	if err := g.applyConfigChanges(currentConfig); err != nil {
		g.logger.Printf("应用配置变更失败: %v", err)
		// 不返回错误，配置已保存，只是热更新失败
	}

	// 6. 更新内存中的配置
	g.config = currentConfig

	g.logger.Println("配置更新完成")
	return nil
}

func (g *GatewayService) mergeConfig(config *Config, updates map[string]interface{}) error {
	preserveRedactedConfigUpdates(config, updates)
	mergeToolsConfig(config, updates)
	if value, ok := updates["default_agent"].(string); ok {
		config.DefaultAgent = strings.TrimSpace(value)
	}
	if value, ok := updates["default_model"].(string); ok {
		config.DefaultModel = strings.TrimSpace(value)
	}

	// 更新 Gateway 配置
	if gatewayUpdates, ok := updates["gateway"].(map[string]interface{}); ok {
		if listenAddr, ok := gatewayUpdates["listen_addr"].(string); ok {
			config.Gateway.ListenAddr = listenAddr
		}
		if maxTasks, ok := gatewayUpdates["max_tasks"].(float64); ok {
			config.Gateway.MaxTasks = int(maxTasks)
		}
		if maxQueued, ok := gatewayUpdates["max_queued_tasks"].(float64); ok {
			config.Gateway.MaxQueuedTasks = int(maxQueued)
		}
		if mode, ok := gatewayUpdates["agent_os_mode"].(string); ok {
			config.Gateway.AgentOSMode = agentos.NormalizeMode(mode)
		}
		if leaseSeconds, ok := gatewayUpdates["task_lease_seconds"].(float64); ok {
			config.Gateway.TaskLeaseSeconds = int(leaseSeconds)
		}
		if pollMS, ok := gatewayUpdates["task_poll_interval_ms"].(float64); ok {
			config.Gateway.TaskPollIntervalMS = int(pollMS)
		}
		if taskTimeout, ok := gatewayUpdates["task_timeout"].(float64); ok {
			config.Gateway.TaskTimeout = int(taskTimeout)
		}
		if enableWebUI, ok := gatewayUpdates["enable_webui"].(bool); ok {
			config.Gateway.EnableWebUI = enableWebUI
		}
		if webUIPath, ok := gatewayUpdates["webui_path"].(string); ok {
			config.Gateway.WebUIPath = webUIPath
		}
		if apiKey, ok := gatewayUpdates["api_key"].(string); ok {
			config.Gateway.APIKey = apiKey
		}
	}

	// 更新模型配置
	if modelUpdates, ok := updates["model_config"].(map[string]interface{}); ok {
		replacement := make(map[string]ModelConfig, len(modelUpdates))
		for name, modelData := range modelUpdates {
			if modelMap, ok := modelData.(map[string]interface{}); ok {
				sourceName := name
				if previousName, ok := modelMap["__previous_name"].(string); ok && strings.TrimSpace(previousName) != "" {
					sourceName = strings.TrimSpace(previousName)
				}
				modelConfig := config.ModelConfig[sourceName]
				if sourceName != name {
					if key, ok := modelMap["api_key"].(string); ok && key == "[REDACTED]" {
						delete(modelMap, "api_key")
					}
				}
				if t, ok := modelMap["type"].(string); ok {
					modelConfig.Type = t
				}
				mergeModelConfigAPIKeyFields(&modelConfig, modelMap)
				if baseURL, ok := modelMap["base_url"].(string); ok {
					modelConfig.BaseURL = baseURL
				}
				if modelName, ok := modelMap["model_name"].(string); ok {
					modelConfig.ModelName = modelName
				}
				if temp, ok := modelMap["temperature"].(float64); ok {
					modelConfig.Temperature = temp
				}
				if maxTokens, ok := modelMap["max_tokens"].(float64); ok {
					modelConfig.MaxTokens = int(maxTokens)
				}
				if thinkingMode, ok := modelMap["thinking_mode"].(string); ok {
					modelConfig.ThinkingMode = thinkingMode
				}
				if supportsVision, ok := modelMap["supports_vision"].(bool); ok {
					modelConfig.SupportsVision = supportsVision
				}
				replacement[name] = modelConfig
			}
		}
		config.ModelConfig = replacement
	}

	// 更新 Agent 配置
	if agentUpdates, ok := updates["agents"]; ok {
		newAgents, err := decodeAgentConfigUpdates(agentUpdates)
		if err != nil {
			return err
		}
		config.Agents = newAgents
	}

	// 更新渠道配置
	if channelUpdates, ok := updates["channels"].([]interface{}); ok {
		var newChannels []ChannelConfig
		for _, item := range channelUpdates {
			if channelMap, ok := item.(map[string]interface{}); ok {
				channel := ChannelConfig{}
				if name, ok := channelMap["name"].(string); ok {
					channel.Name = name
				}
				if t, ok := channelMap["type"].(string); ok {
					channel.Type = t
				}
				if enabled, ok := channelMap["enabled"].(bool); ok {
					channel.Enabled = enabled
				}
				if cfg, ok := channelMap["config"].(map[string]interface{}); ok {
					channel.Config = cfg
				}
				newChannels = append(newChannels, channel)
			}
		}
		config.Channels = newChannels
	}

	// 更新邮件配置
	if emailUpdates, ok := updates["email_config"].(map[string]interface{}); ok {
		if enabled, ok := emailUpdates["enabled"].(bool); ok {
			config.EmailConfig.Enabled = enabled
		}
		if smtpHost, ok := emailUpdates["smtp_host"].(string); ok {
			config.EmailConfig.SMTPHost = smtpHost
		}
		if smtpPort, ok := emailUpdates["smtp_port"].(float64); ok {
			config.EmailConfig.SMTPPort = int(smtpPort)
		}
		if username, ok := emailUpdates["username"].(string); ok {
			config.EmailConfig.Username = username
		}
		if password, ok := emailUpdates["password"].(string); ok {
			config.EmailConfig.Password = password
		}
	}

	return resolveModelConfigEnvironment(config)
}

func (g *GatewayService) applyConfigChanges(newConfig *Config) error {
	g.logger.Println("应用配置变更到运行组件...")

	// 更新引擎配置
	if g.engine != nil {
		if err := g.engine.UpdateConfig(newConfig); err != nil {
			g.logger.Printf("更新引擎配置失败: %v", err)
		}
	}

	// 更新渠道配置
	if g.channelManager != nil {
		// 检查渠道是否有变化
		if g.needUpdateChannels(g.config.Channels, newConfig.Channels) {
			g.logger.Println("渠道配置已变更，重启渠道管理器...")
			g.channelManager.StopAll()
			g.channelManager = channelhost.NewManager(newChannelRuntimeAdapter(g))
			g.config.Channels = newConfig.Channels
			g.loadChannelsFromConfig()
			if !channelsDisabledByEnv() {
				g.channelManager.StartAll()
			}
		}
	}

	return nil
}

func (g *GatewayService) attachmentWorkspaceRoot() string {
	if g != nil && g.config != nil && strings.TrimSpace(g.config.Workspace) != "" {
		return g.config.Workspace
	}
	return "."
}

func isInlineSafeImageMIME(mimeType string) bool {
	switch strings.ToLower(strings.TrimSpace(strings.Split(mimeType, ";")[0])) {
	case "image/png", "image/jpeg", "image/gif", "image/webp", "image/bmp", "image/avif", "image/x-icon", "image/vnd.microsoft.icon":
		return true
	default:
		return false
	}
}

func (g *GatewayService) saveChatMessage(sessionID, role, agentName, content, status, taskID, channel, userID string) (*ChatMessage, error) {
	return g.saveChatMessageWithAttachments(sessionID, role, agentName, content, status, taskID, channel, userID, nil)
}

type expectedChatMessageOwner struct {
	TenantID  string
	SubjectID string
}

func (g *GatewayService) saveChatMessageForTask(
	task *GatewayTask,
	sessionID, role, agentName, content, status, taskID, channel, userID string,
) (*ChatMessage, error) {
	tenantID, subjectID, scoped := gatewayTaskPersonalOwner(task)
	if !scoped {
		return g.saveChatMessage(
			sessionID, role, agentName, content, status, taskID, channel, userID,
		)
	}
	if strings.TrimSpace(subjectID) == "" {
		return nil, fmt.Errorf("personal task has no subject owner")
	}
	if task.Request != nil {
		expectedSessionID := strings.TrimSpace(task.Request.SessionId)
		if expectedSessionID != "" && expectedSessionID != strings.TrimSpace(sessionID) {
			return nil, fmt.Errorf("%w: task session is immutable", ErrSessionOwnerConflict)
		}
	}
	if expectedTaskID := strings.TrimSpace(task.ID); expectedTaskID != "" &&
		expectedTaskID != strings.TrimSpace(taskID) {
		return nil, fmt.Errorf("%w: message task id does not match its owner", ErrTaskOwnerConflict)
	}
	return g.saveChatMessageWithAttachments(
		sessionID, role, agentName, content, status, taskID, channel, userID, nil,
		expectedChatMessageOwner{TenantID: tenantID, SubjectID: subjectID},
	)
}

func (g *GatewayService) saveChatMessageWithAttachments(
	sessionID, role, agentName, content, status, taskID, channel, userID string,
	attachments []attachmentcore.File,
	expectedOwners ...expectedChatMessageOwner,
) (*ChatMessage, error) {
	if g.db == nil {
		g.logger.Printf("数据库未初始化，跳过保存消息（%d 字符）", len([]rune(content)))
		return nil, fmt.Errorf("database not initialized")
	}
	var expectedOwner *expectedChatMessageOwner
	if len(expectedOwners) == 1 {
		normalized := expectedChatMessageOwner{
			TenantID:  normalizePersonalOwnerTenantID(expectedOwners[0].TenantID),
			SubjectID: strings.TrimSpace(expectedOwners[0].SubjectID),
		}
		expectedOwner = &normalized
	}

	var msg ChatMessage
	createdSession := false
	err := g.db.Transaction(func(tx *gorm.DB) error {
		// The session row is the authoritative owner binding. Message callers
		// cannot supply or override TenantID/SubjectID through legacy user
		// fields, so assistant messages and resumed work inherit the same owner.
		var session ChatSession
		sessionQuery := tx.Where("id = ?", sessionID)
		if expectedOwner != nil {
			sessionQuery = scopeRowsToPersistedOwner(
				sessionQuery,
				expectedOwner.TenantID,
				expectedOwner.SubjectID,
			)
		}
		result := sessionQuery.First(&session)
		switch result.Error {
		case nil:
		case gorm.ErrRecordNotFound:
			if expectedOwner != nil {
				return fmt.Errorf("%w: personal session is missing or belongs to another subject", ErrSessionOwnerConflict)
			}
			now := time.Now()
			session = ChatSession{
				ID: sessionID, Name: sessionID, AgentName: agentName,
				TenantID: defaultExecutionTenantID, UserId: userID,
				MessageCount: 0, CreatedAt: now, UpdatedAt: now,
			}
			if err := tx.Create(&session).Error; err != nil {
				return fmt.Errorf("create chat session: %w", err)
			}
			createdSession = true
		default:
			return fmt.Errorf("load chat session owner: %w", result.Error)
		}

		msg = ChatMessage{
			SessionID: sessionID,
			TenantID:  normalizePersonalOwnerTenantID(session.TenantID),
			SubjectID: strings.TrimSpace(session.SubjectID),
			Role:      role,
			AgentName: agentName,
			Content:   content,
			Attachments: append(
				[]attachmentcore.File(nil),
				attachments...,
			),
			Status:    status,
			TaskID:    taskID,
			Channel:   channel,
			UserId:    userID,
			CreatedAt: time.Now(),
		}
		if err := tx.Create(&msg).Error; err != nil {
			return fmt.Errorf("create chat message: %w", err)
		}

		updates := map[string]interface{}{
			"message_count": gorm.Expr("message_count + 1"),
			"updated_at":    time.Now(),
		}
		if agentName != "" {
			updates["agent_name"] = agentName
		}
		if userID != "" {
			updates["user_id"] = userID
		}
		if err := tx.Model(&session).Updates(updates).Error; err != nil {
			return fmt.Errorf("update chat session statistics: %w", err)
		}
		return nil
	})
	if err != nil {
		g.logger.Printf("保存消息失败: %v", err)
		return nil, err
	}
	g.logger.Printf("保存消息成功 - Session: %s, Role: %s, Channel: %s", sessionID, role, channel)
	if createdSession {
		g.broadcastSessionListChanged()
	}
	return &msg, nil
}

func (g *GatewayService) updateAEUserMessageTask(userMsg *ChatMessage, taskID, status string) {
	if g == nil || g.db == nil || userMsg == nil {
		return
	}
	updates := map[string]interface{}{"task_id": taskID}
	if status != "" {
		updates["status"] = status
	}
	if err := g.db.Model(&ChatMessage{}).Where("id = ?", userMsg.ID).Updates(updates).Error; err != nil && g.logger != nil {
		g.logger.Printf("[AE] update @mention user message task failed: %v", err)
	}
}

func (g *GatewayService) handleCollaboration(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Read and detect encoding (same as handleChat)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Failed to read request body", http.StatusBadRequest)
		return
	}
	convertedBody, err := detectAndConvertToUTF8(body)
	if err != nil {
		convertedBody = body
	}

	var req struct {
		TaskType      string                 `json:"task_type"`
		Message       string                 `json:"message"`
		SessionID     string                 `json:"session_id"`
		Channel       string                 `json:"channel"`
		UserID        string                 `json:"user_id"`
		Collaboration CollaborationConfig    `json:"collaboration"`
		Parameters    map[string]interface{} `json:"parameters"`
	}

	if err := json.Unmarshal(convertedBody, &req); err != nil {
		// Fallback: try original body
		if err2 := json.Unmarshal(body, &req); err2 != nil {
			http.Error(w, "Invalid request body", http.StatusBadRequest)
			return
		}
	}
	g.assessAndLogGatewayUserContent("collaboration", req.Message)

	sessionID := req.SessionID
	if sessionID == "" {
		sessionID = fmt.Sprintf("session_%d", time.Now().UnixNano())
	}
	if g.rejectLegacyAgentOSSession(w, r, sessionID) {
		return
	}
	channel := strings.TrimSpace(req.Channel)
	if channel == "" {
		channel = "web"
	}
	userID := strings.TrimSpace(req.UserID)

	// 生成任务ID
	taskID := fmt.Sprintf("collab_%s", time.Now().Format("060102150405"))

	// 保存用户消息到数据库
	g.saveChatMessage(sessionID, "user", "", req.Message, "pending", taskID, channel, userID)

	// 加载历史上下文（与 chat 模式一致，让协作任务也能记住之前的对话）
	var historyContext string
	if g.db != nil && sessionID != "" {
		var messages []ChatMessage
		query := g.db.Where("session_id = ? AND role IN ('user', 'assistant') AND task_id != ?", sessionID, taskID)
		query = scopeChatHistoryForTask(query, nil)
		if userID != "" {
			query = query.Where("user_id = ? OR user_id = ''", userID)
		}
		query.Order("created_at desc").Limit(10).Find(&messages)
		for i, j := 0, len(messages)-1; i < j; i, j = i+1, j-1 {
			messages[i], messages[j] = messages[j], messages[i]
		}
		if history := buildGatewaySessionHistory(messages); len(history.Messages) > 0 {
			historyContext = history.Context
			g.logger.Printf("Collaboration: loaded %d clean history messages (SessionId: %s)", len(history.Messages), sessionID)
		}
	}

	fullMessage := req.Message
	if historyContext != "" {
		fullMessage = historyContext + req.Message
	}

	// 创建任务
	task := &GatewayTask{
		ID: taskID,
		Request: &MessageRequest{
			TaskID:    taskID,
			TaskType:  "collaboration",
			Message:   req.Message,
			SessionId: sessionID,
			Channel:   channel,
			UserId:    userID,
		},
		Status:    "pending",
		CreatedAt: time.Now(),
		ResultCh:  make(chan interface{}, 1),
	}
	task.ProgressCh = make(chan map[string]interface{}, 20)
	task.InterruptCh = make(chan string, 5)
	g.tasksMu.Lock()
	g.tasks[taskID] = task
	g.tasksMu.Unlock()

	// Register interrupt channel
	storeInterruptChannel(sessionID, taskID, task.InterruptCh)

	// Register progress channel
	g.taskStreamsMu.Lock()
	g.taskStreams[taskID] = task.ProgressCh
	g.taskStreamsMu.Unlock()

	// Broadcast task created
	g.broadcastTaskUpdate(taskID, "pending", nil, nil)

	// 异步处理
	go func() {
		ctx, cancel := g.newGatewayTaskContext()
		defer cancel()
		ctx = g.withAgentOSRuntime(ctx)
		ctx = context.WithValue(ctx, "task_id", taskID)
		ctx = context.WithValue(ctx, "session_id", sessionID)
		ctx = context.WithValue(ctx, "channel", channel)
		ctx = context.WithValue(ctx, "user_id", userID)
		defer func() {
			g.taskStreamsMu.Lock()
			delete(g.taskStreams, taskID)
			g.taskStreamsMu.Unlock()
			deleteInterruptChannel(sessionID, taskID)
			close(task.InterruptCh)
			close(task.ProgressCh)
			close(task.ResultCh)
		}()

		runningSnapshot, started := g.markGatewayTaskRunning(task, cancel)
		if !started {
			response := GatewayResponse{TaskID: taskID, Status: "stopped", Timestamp: time.Now()}
			if runningSnapshot != nil {
				response.Status = runningSnapshot.Status
				response.Result = runningSnapshot.Result
				if runningSnapshot.Error != nil {
					response.Error = runningSnapshot.Error.Error()
				}
			}
			trySendGatewayTaskResult(task.ResultCh, response)
			return
		}
		g.broadcastTaskUpdate(taskID, "running", nil, nil)

		g.engine.runMu.Lock()
		defer g.engine.runMu.Unlock()

		// Apply all per-request collaboration settings to both the public
		// config and the engine's cached runtime fields. Restore them after
		// this serialized run so one chat request cannot leak into the next.
		originalTeamName := g.engine.config.TeamName
		restoreCollaboration := g.updateEngineCollaboration(req.Collaboration, fullMessage)
		defer func() {
			restoreCollaboration()
			g.engine.config.TeamName = originalTeamName
		}()

		g.engine.config.Task.Description = req.Message
		if name, ok := req.Parameters["team_name"].(string); ok {
			g.engine.config.TeamName = name
		}

		// Broadcast task start so frontend shows progress timeline immediately
		g.BroadcastProgress(ProgressEvent{
			Type: "task_start", TaskID: taskID,
			Message: "", Status: "running",
		})

		// Handle interrupts
		go func() {
			for msg := range task.InterruptCh {
				g.logger.Printf("Collaboration task %s interrupted with message: %s", taskID, msg)
				// Inject the interrupt message as additional input
				g.engine.config.Task.Input = g.engine.config.Task.Input + "\n\n[用户补充]: " + msg
				// 持久化中断消息
				if g.db != nil {
					if _, saveErr := g.saveChatMessageForTask(
						task, sessionID, "user", "", msg, "pending", taskID, channel, userID,
					); saveErr != nil && g.logger != nil {
						g.logger.Printf("保存任务 %s 的中断消息失败: %v", taskID, saveErr)
					}
				}
			}
		}()

		startTime := time.Now()

		// 使用引擎执行
		g.ObserveTaskRuntime(taskID, "collaboration", "")
		result, err := g.engine.run(ctx)
		if err == nil {
			err = collaborationResultError(result)
		}
		var terminalResult interface{} = result

		duration := time.Since(startTime)
		status := "completed"
		if err != nil {
			status = "failed"
			failureOutput := agentresult.FailureUserMessage(gatewayResultText(terminalResult), err)
			if strings.TrimSpace(gatewayResultText(terminalResult)) == "" && failureOutput != "" {
				terminalResult = failureOutput
			}
		}
		terminalTask, applied := g.finishGatewayTask(task, status, terminalResult, err)
		response := GatewayResponse{TaskID: taskID, Status: status, Result: terminalResult, Timestamp: time.Now()}
		if terminalTask != nil {
			response.Status = terminalTask.Status
			response.Result = terminalTask.Result
			if terminalTask.Error != nil {
				response.Error = terminalTask.Error.Error()
			}
		}
		trySendGatewayTaskResult(task.ResultCh, response)
		if !applied {
			g.logger.Printf("Collaboration task %s already terminal as %s; ignored late %s result", taskID, response.Status, status)
			return
		}

		if err != nil {
			g.saveChatMessage(sessionID, "assistant", "", gatewayResultText(terminalResult), "failed", taskID, channel, userID)
			g.logger.Printf("Collaboration task %s failed: %v (duration: %.2fs)", taskID, err, duration.Seconds())
		} else {
			var resultStr string
			if resultBytes, marshalErr := json.Marshal(terminalResult); marshalErr == nil {
				resultStr = string(resultBytes)
			} else {
				resultStr = fmt.Sprintf("%v", terminalResult)
			}
			g.saveChatMessage(sessionID, "assistant", "", resultStr, "completed", taskID, channel, userID)
			g.logger.Printf("Collaboration task %s completed (duration: %.2fs)", taskID, duration.Seconds())
		}
		if g.db != nil {
			if saveErr := SaveGatewayTaskToDB(g.db, terminalTask); saveErr != nil && g.logger != nil {
				g.logger.Printf("保存协同任务最终状态失败: %v", saveErr)
			}
		}
		g.broadcastTaskUpdate(taskID, terminalTask.Status, terminalTask.Result, terminalTask.Error)
	}()

	// 立即返回
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(GatewayResponse{
		TaskID:    taskID,
		Status:    "pending",
		Timestamp: time.Now(),
	})
}

func (g *GatewayService) updateEngineCollaboration(collabConfig CollaborationConfig, message string) func() {
	// 保存原始配置
	originalTask := g.engine.config.Task
	originalCollaboration := g.engine.config.Collaboration
	originalMaxRound := g.engine.maxRound
	originalFailureStrategy := g.engine.failureStrategy

	// 更新配置
	g.engine.config.Task.Input = message
	g.engine.config.Collaboration = collabConfig
	g.engine.maxRound = collabConfig.MaxRound
	if g.engine.maxRound <= 0 {
		g.engine.maxRound = 3
	}
	g.engine.failureStrategy = collabConfig.FailureStrategy
	if g.engine.failureStrategy == "" {
		g.engine.failureStrategy = Continue
	}

	// 返回恢复函数
	return func() {
		g.engine.config.Task = originalTask
		g.engine.config.Collaboration = originalCollaboration
		g.engine.maxRound = originalMaxRound
		g.engine.failureStrategy = originalFailureStrategy
	}
}

// 新增：停止任务
func (g *GatewayService) handleInterrupt(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	var req struct {
		SessionID string `json:"session_id"`
		TaskID    string `json:"task_id,omitempty"`
		Message   string `json:"message"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"status":"bad_request"}`, http.StatusBadRequest)
		return
	}
	if req.SessionID == "" {
		http.Error(w, "session_id required", http.StatusBadRequest)
		return
	}
	g.assessAndLogGatewayUserContent("interrupt", req.Message)
	if reg, ok := loadInterruptChannel(req.SessionID); ok {
		if req.TaskID != "" && reg.TaskID != "" && req.TaskID != reg.TaskID {
			json.NewEncoder(w).Encode(map[string]string{"status": "no_active_task"})
			return
		}
		taskID := reg.TaskID
		if taskID == "" {
			taskID = req.TaskID
		}
		if taskID != "" {
			task, exists := g.gatewayTaskSnapshot(taskID)
			if !exists || task.QueueName == agentOSQueueName ||
				!(task.Status == "pending" || task.Status == "running") {
				deleteInterruptChannel(req.SessionID, taskID)
				json.NewEncoder(w).Encode(map[string]string{"status": "no_active_task"})
				return
			}
		}
		select {
		case reg.Ch <- req.Message:
			json.NewEncoder(w).Encode(map[string]string{"status": "injected", "task_id": taskID})
			return
		default:
		}
	}
	json.NewEncoder(w).Encode(map[string]string{"status": "no_active_task"})
}

func (g *GatewayService) handleStopTask(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")

	// 从请求体中解析任务ID
	var req struct {
		TaskID string `json:"task_id"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		// 也可以从 URL 参数获取
		req.TaskID = r.URL.Query().Get("id")
	}

	if req.TaskID == "" {
		http.Error(w, `{"error": "Task ID required"}`, http.StatusBadRequest)
		return
	}

	g.logger.Printf("尝试停止任务: %s", req.TaskID)

	if g.isAgentOSTaskID(req.TaskID) {
		http.NotFound(w, r)
		return
	}
	task, exists, stopped := g.stopGatewayTask(req.TaskID)
	if exists && stopped {
		g.logger.Printf("任务 %s 已停止", req.TaskID)

		json.NewEncoder(w).Encode(map[string]interface{}{
			"status":    "stopped",
			"task_id":   req.TaskID,
			"timestamp": time.Now(),
		})
	} else if !exists {
		g.logger.Printf("任务 %s 不存在", req.TaskID)
		http.Error(w, fmt.Sprintf(`{"error": "Task %s not found"}`, req.TaskID), http.StatusNotFound)
	} else {
		http.Error(w, fmt.Sprintf(`{"error": "Task %s cannot be stopped, current status: %s"}`, req.TaskID, task.Status), http.StatusBadRequest)
	}
}

func (g *GatewayService) handleListTasks(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	limit := 50
	if l, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && l > 0 {
		limit = l
	}
	if limit > 200 {
		limit = 200
	}
	statusFilter := strings.TrimSpace(r.URL.Query().Get("status"))

	g.tasksMu.RLock()
	tasks := make([]map[string]interface{}, 0, len(g.tasks))
	for id, task := range g.tasks {
		if task == nil || task.QueueName == agentOSQueueName {
			continue
		}
		if statusFilter != "" && statusFilter != "all" && task.Status != statusFilter {
			continue
		}
		// 修复：添加 nil 检查，防止访问 nil 的 Request
		taskType := ""
		if task.Request != nil {
			taskType = task.Request.TaskType
		}

		message := ""
		if task.Request != nil {
			message = task.Request.Message
		}
		tasks = append(tasks, g.taskListItem(id, task.Status, taskType, message, task.CreatedAt, task.CompletedAt))
	}
	g.tasksMu.RUnlock()
	sort.Slice(tasks, func(i, j int) bool {
		ti, _ := tasks[i]["created_at"].(time.Time)
		tj, _ := tasks[j]["created_at"].(time.Time)
		return ti.After(tj)
	})
	if len(tasks) > limit {
		tasks = tasks[:limit]
	}

	// 内存不足时回退 DB（保留历史任务）
	if len(tasks) == 0 && g.db != nil {
		dbTasks, err := ListLegacyTasksFromDB(g.db, statusFilter, limit)
		if err == nil {
			for _, t := range dbTasks {
				msg := t.Message
				if len(msg) > 60 {
					msg = msg[:60]
				}
				item := g.taskListItem(t.TaskID, t.Status, t.TaskType, msg, t.CreatedAt, t.CompletedAt)
				item["agent_name"] = t.AgentName
				tasks = append(tasks, item)
			}
		}
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"tasks": tasks,
		"count": len(tasks),
	})
}

func (g *GatewayService) taskListItem(id, status, taskType, message string, createdAt time.Time, completedAt *time.Time) map[string]interface{} {
	item := map[string]interface{}{
		"id":           id,
		"status":       status,
		"type":         taskType,
		"message":      message,
		"created_at":   createdAt,
		"completed_at": completedAt,
	}
	if progress := g.pipelineProgressForTask(id); progress != nil {
		item["pipeline_progress"] = progress
	}
	return item
}

func (g *GatewayService) pipelineProgressForTask(taskID string) map[string]interface{} {
	if g == nil || g.pipelineExec == nil || taskID == "" {
		return nil
	}
	execID := strings.TrimSuffix(taskID, "-de")
	exec, ok := g.pipelineExec.GetExecutionByID(execID)
	if !ok || exec == nil {
		return nil
	}
	exec.mu.Lock()
	defer exec.mu.Unlock()
	steps := make([]map[string]interface{}, 0, len(exec.Steps))
	for _, step := range exec.Steps {
		steps = append(steps, map[string]interface{}{
			"name":        step.Name,
			"status":      step.Status,
			"duration_ms": step.DurationMs,
			"error":       step.Error,
		})
	}
	return map[string]interface{}{
		"exec_id":       exec.ID,
		"pipeline_name": exec.PipelineName,
		"status":        exec.Status,
		"current_step":  exec.CurrentStep,
		"total_steps":   len(exec.Steps),
		"steps":         steps,
	}
}

// Stop 停止Gateway服务
func (g *GatewayService) Stop(ctx context.Context) error {
	queue := g.ensureAgentOSQueue()
	queue.BeginDrain()
	var shutdownErr error

	if g.channelManager != nil {
		g.channelManager.StopAll()
	}
	g.stopGatewayBackground(ctx)

	if g.agentOSHTTPServer != nil {
		if err := g.agentOSHTTPServer.Shutdown(ctx); err != nil {
			shutdownErr = err
		}
	}
	if g.httpServer != nil {
		if err := g.httpServer.Shutdown(ctx); err != nil && shutdownErr == nil {
			shutdownErr = err
		}
	}

	if g.todoManager != nil {
		g.todoManager.Stop()
	}
	queue.Stop(ctx)
	return shutdownErr
}

// 认证中间件
func (g *GatewayService) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 健康检查不需要认证
		if r.URL.Path == "/api/health" || r.URL.Path == "/livez" || r.URL.Path == "/readyz" || r.URL.Path == "/metrics" {
			next.ServeHTTP(w, r)
			return
		}

		// API Key 认证
		if g.config.Gateway.APIKey != "" {
			apiKey := r.Header.Get("X-API-Key")
			if apiKey != g.config.Gateway.APIKey {
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}
		}

		next.ServeHTTP(w, r)
	})
}

// handleChatWithFile handles multipart file upload for /api/chat.
func (g *GatewayService) handleChatWithFile(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(50 << 20); err != nil {
		http.Error(w, `{"error":"file too large (max 50MB)"}`, http.StatusBadRequest)
		return
	}
	message := r.FormValue("message")
	sessionID := r.FormValue("session_id")
	agentName := r.FormValue("agent_name")
	g.assessAndLogGatewayUserContent("chat_multipart", message)
	if agentName == "" {
		agentName = "Researcher"
	}
	if sessionID == "" {
		sessionID = fmt.Sprintf("s%s_file", time.Now().Format("0601021504"))
	}
	sessionID = strings.ReplaceAll(sessionID, "..", "")
	sessionID = strings.ReplaceAll(sessionID, "/", "_")
	sessionID = strings.ReplaceAll(sessionID, "\\", "_")
	if g.rejectLegacyAgentOSSession(w, r, sessionID) {
		return
	}
	files := r.MultipartForm.File["file"]
	attachments := make([]attachmentcore.File, 0, len(files))
	for _, fh := range files {
		src, err := fh.Open()
		if err != nil {
			sendJSONError(w, "failed to read attachment", http.StatusBadRequest)
			return
		}
		attachment, saveErr := attachmentcore.SaveWorkspaceUpload(g.attachmentWorkspaceRoot(), fh.Filename, fh.Header.Get("Content-Type"), src)
		_ = src.Close()
		if saveErr != nil {
			sendJSONError(w, "failed to save attachment", http.StatusInternalServerError)
			return
		}
		attachments = append(attachments, attachment)
	}
	if strings.TrimSpace(message) == "" && len(attachments) == 0 {
		http.Error(w, `{"error":"message cannot be empty"}`, http.StatusBadRequest)
		return
	}
	req := MessageRequest{
		Message:     message,
		Attachments: attachments,
		SessionId:   sessionID,
		AgentName:   agentName,
		Channel:     "api",
		UserId:      r.FormValue("user_id"),
		Stream:      r.FormValue("stream") == "true",
	}
	body, _ := json.Marshal(req)
	r.Body = io.NopCloser(bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	g.handleChat(w, r)
}

func (g *GatewayService) handleChat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")

	// Support multipart file upload: POST /api/chat with file(s) + message
	contentType := r.Header.Get("Content-Type")
	if strings.HasPrefix(contentType, "multipart/form-data") {
		g.handleChatWithFile(w, r)
		return
	}

	// 读取原始请求体
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Failed to read request body", http.StatusBadRequest)
		return
	}

	g.logger.Printf("原始请求体长度: %d 字节", len(body))

	// 尝试检测并转换编码
	convertedBody, err := detectAndConvertToUTF8(body)
	if err != nil {
		g.logger.Printf("编码转换失败: %v", err)
		// 如果转换失败，尝试直接解析
		convertedBody = body
	}

	g.logger.Printf("请求体编码转换完成: %d 字节", len(convertedBody))

	// 解析JSON
	var req MessageRequest
	if err := json.Unmarshal(convertedBody, &req); err != nil {
		g.logger.Printf("JSON解析失败: %v", err)

		// 尝试强制转换
		// 如果是GBK，尝试手动解码
		if decoded, err := gbkToUTF8(body); err == nil {
			g.logger.Printf("GBK解码成功: %d 字节", len(decoded))
			if decodeErr := json.Unmarshal(decoded, &req); decodeErr != nil {
				http.Error(w, "Invalid JSON: "+decodeErr.Error(), http.StatusBadRequest)
				return
			}
			g.logger.Printf("使用GBK解码后解析成功")
		} else {
			http.Error(w, "Invalid JSON: "+err.Error(), http.StatusBadRequest)
			return
		}
	}
	simpleQA := req.TaskType == "qa"
	if simpleQA {
		if req.SkillRun != nil || len(req.Attachments) != 0 {
			sendJSONError(w, "简单问答仅接收文字；使用 Skill 或附件请切换到 Agent 模式", http.StatusBadRequest)
			return
		}
		req.AgentName = ""
	}
	normalizedSkillRun, skillRunErr := normalizeSkillRunRequest(req.SkillRun)
	if skillRunErr != nil {
		sendJSONError(w, skillRunErr.Error(), http.StatusBadRequest)
		return
	}
	req.SkillRun = normalizedSkillRun
	webSelection, selectionStatus, selectionErr := g.captureWebModelSelection(r, &req)
	if selectionErr != nil {
		sendJSONError(w, selectionErr.Error(), selectionStatus)
		return
	}
	platformAccount, accountErr := g.captureWebPlatformAccount(r, &req, webSelection)
	if accountErr != nil {
		sendJSONError(w, accountErr.Error(), http.StatusUnauthorized)
		return
	}
	r = r.WithContext(platformAccount.Context(r.Context()))
	requestedSkillTask := strings.EqualFold(strings.TrimSpace(req.TaskType), "skill")
	if req.SkillRun != nil {
		req.TaskType = "skill"
	}

	// 如果消息还是乱码，尝试修复
	if strings.ContainsAny(req.Message, "���") || hasReplacementChar(req.Message) {
		g.logger.Printf("检测到乱码，尝试修复消息（%d 字符）", len([]rune(req.Message)))
		if fixed, err := fixGarbledText(req.Message); err == nil {
			req.Message = fixed
			g.logger.Printf("乱码修复完成（%d 字符）", len([]rune(req.Message)))
		}
	}
	visibleMessage := strings.TrimSpace(req.Message)
	g.assessAndLogGatewayUserContent("chat", visibleMessage)

	if req.Channel == "" {
		req.Channel = "web"
	}
	clientProvidedSession := strings.TrimSpace(req.SessionId) != ""
	if req.SessionId == "" {
		if req.Channel == "web" {
			req.SessionId = newServerWebSessionID(time.Now())
		} else if req.Channel != "" && req.UserId != "" {
			req.SessionId = businesstools.PersistentSessionID(req.Channel, req.UserId, "")
		} else {
			req.SessionId = fmt.Sprintf("s%s_%s", time.Now().Format("0601021504"), req.AgentName)
		}
		g.logger.Printf("Session: %s (channel=%s user=%s)", req.SessionId, req.Channel, req.UserId)
	}
	req.SessionId = strings.ReplaceAll(req.SessionId, "..", "")
	req.SessionId = strings.ReplaceAll(req.SessionId, "/", "_")
	req.SessionId = strings.ReplaceAll(req.SessionId, "\\", "_")
	if g.rejectLegacyAgentOSSession(w, r, req.SessionId) {
		return
	}
	resolvedWorkspace, workspaceErr := g.bindSessionWorkspace(req.SessionId, req.AgentName, req.UserId, req.Channel != "web" || !clientProvidedSession)
	if workspaceErr != nil {
		sendJSONError(w, workspaceErr.Error(), http.StatusConflict)
		return
	}
	req.ResolvedWorkspace = resolvedWorkspace
	normalizedAttachments, attachmentErr := attachmentcore.Normalize(req.Attachments, g.attachmentWorkspaceRoot(), false)
	if attachmentErr != nil {
		g.logger.Printf("附件校验失败: %v", attachmentErr)
		sendJSONError(w, attachmentErr.Error(), http.StatusBadRequest)
		return
	}
	req.Attachments = normalizedAttachments
	req.Message = attachmentcore.AgentMessage(visibleMessage, normalizedAttachments)

	g.logger.Printf("最终请求 - Agent: %q, 消息长度: %d", req.AgentName, len([]rune(req.Message)))

	if visibleMessage == "" && len(req.Attachments) == 0 {
		http.Error(w, "Message cannot be empty", http.StatusBadRequest)
		return
	}
	// Preserve the existing Codex and AI-employee routing precedence. Only a
	// complete @name/@alias that neither router owns may bind an installed
	// Skill. Unknown mentions continue through ordinary conversation unchanged.
	reservedForCodex := !simpleQA && req.SkillRun == nil && g.codexInteractive != nil &&
		shouldRouteToCodexInteractive(req.Message, g.codexInteractive.Has(req.SessionId))
	reservedForEmployee := false
	if !simpleQA && req.SkillRun == nil && g.aeManager != nil {
		cfg, _ := g.aeManager.MatchAtName(req.Message)
		reservedForEmployee = cfg != nil
	}
	if webSelection != nil && (reservedForCodex || reservedForEmployee) {
		sendJSONError(w, "当前模型选择仅用于对话 Agent、简单问答和 Skill；请通过对应入口启动外部 Agent 或 AI 员工", http.StatusBadRequest)
		return
	}
	if !simpleQA && req.SkillRun == nil && !reservedForCodex && !reservedForEmployee {
		mentionRun, skillTask, matched, mentionErr := g.resolveSkillMention(visibleMessage, req.AgentName)
		if mentionErr != nil {
			sendJSONError(w, mentionErr.Error(), http.StatusBadRequest)
			return
		}
		if matched {
			normalizedMentionRun, normalizeErr := normalizeSkillRunRequest(mentionRun)
			if normalizeErr != nil {
				sendJSONError(w, normalizeErr.Error(), http.StatusBadRequest)
				return
			}
			req.SkillRun = normalizedMentionRun
			req.TaskType = "skill"
			req.Message = attachmentcore.AgentMessage(
				skilldomain.PrependMentionToTask(visibleMessage, skillTask), normalizedAttachments,
			)
			g.logger.Printf("[SkillRun] @mention matched: %s -> task=%s", req.SkillRun.SkillName, textutil.TruncateRunes(skillTask, 160))
		}
	}
	if requestedSkillTask && req.SkillRun == nil {
		sendJSONError(w, "skill_run or a valid @Skill mention is required when task_type is skill", http.StatusBadRequest)
		return
	}
	if !simpleQA && req.SkillRun == nil && g.codexInteractive != nil &&
		shouldRouteToCodexInteractive(req.Message, g.codexInteractive.Has(req.SessionId)) {
		taskID, result, err := g.codexInteractive.StartOrSend(r.Context(), req.SessionId, req.Channel, req.UserId, "", req.Message, "")
		if err != nil {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"type": "error", "message": fmt.Sprintf("Codex interactive failed: %v", err),
			})
			return
		}
		if taskID == "" {
			taskID = aeChatTaskID(req.SessionId + "-codex")
		}
		g.saveChatMessageWithAttachments(req.SessionId, "user", req.AgentName, visibleMessage, "completed", taskID, req.Channel, req.UserId, req.Attachments)
		json.NewEncoder(w).Encode(GatewayResponse{
			TaskID:    taskID,
			Status:    "running",
			Result:    result,
			Timestamp: time.Now(),
		})
		return
	}
	// AI员工 @mention 匹配：如果消息中 @了某个AI员工，自动分派任务
	if !simpleQA && req.SkillRun == nil && g.aeManager != nil {
		if cfg, task := g.aeManager.MatchAtName(req.Message); cfg != nil {
			g.logger.Printf("[AE] @mention matched: %s → task=%s", cfg.Name, task)
			provisionalTaskID := fmt.Sprintf("ae_pending_%d", time.Now().UnixNano())
			userMsg, saveErr := g.saveChatMessageWithAttachments(req.SessionId, "user", req.AgentName, visibleMessage, "pending", provisionalTaskID, req.Channel, req.UserId, req.Attachments)
			if saveErr != nil {
				g.logger.Printf("[AE] save @mention user message failed: %v", saveErr)
			}
			record, err := g.aeManager.DispatchExplicitMention(r.Context(), cfg.ID, task, req.SessionId)
			if err != nil {
				g.logger.Printf("[AE] Dispatch error: %v", err)
				if userMsg != nil {
					g.db.Model(&ChatMessage{}).Where("id = ?", userMsg.ID).Update("status", "failed")
				}
				json.NewEncoder(w).Encode(map[string]interface{}{
					"type": "error", "message": fmt.Sprintf("AI员工 %s 启动失败: %v", cfg.Name, err),
				})
				return
			}
			taskID := aeChatTaskID(record.ExecID)
			req.TaskID = taskID
			req.TaskType = "ae"
			g.updateAEUserMessageTask(userMsg, taskID, "running")
			// Create a GatewayTask so frontend can subscribe to SSE progress
			deTask := &GatewayTask{
				ID:        taskID,
				Request:   &req,
				Status:    "running",
				CreatedAt: time.Now(),
				ResultCh:  make(chan interface{}, 1),
			}
			deTask.ProgressCh = make(chan map[string]interface{}, 50)
			g.tasksMu.Lock()
			g.tasks[taskID] = deTask
			g.tasksMu.Unlock()
			if record.Pipeline == "external_agent" {
				g.aeManager.bindExternalCodingGatewayTask(taskID, deTask)
			}
			if err := SaveGatewayTaskToDB(g.db, deTask); err != nil {
				g.logger.Printf("[AE] save gateway task failed: %v", err)
			}
			g.broadcastTaskUpdate(taskID, "running", nil, nil)
			g.taskStreamsMu.Lock()
			g.taskStreams[taskID] = deTask.ProgressCh
			g.taskStreamsMu.Unlock()
			g.aeManager.publishAIEmployeeWorkflowPlan(record)
			// Pipeline DEs are monitored through PipelineExecutor. External coding
			// DEs push their own Codex/Claude progress and close the stream on finish.
			if record.Pipeline != "external_agent" && record.Pipeline != "codex_interactive" {
				go g.streamDEProgress(taskID, cfg.Name, record.ExecID, req.SessionId, deTask.ProgressCh)
			} else if record.Status != "running" {
				g.aeManager.finishExternalCodingGatewayTask(record, record.Status, record.Result)
			}
			json.NewEncoder(w).Encode(GatewayResponse{
				TaskID:    taskID,
				Status:    "running",
				Timestamp: time.Now(),
			})
			return
		}
	}

	// Session延续已禁用：必须显式@mention才能触发AI员工
	/* 		// Supervisor智能路由：无@mention时，根据系统身份自动判断模式
	   		if g.supervisorRouter != nil {
	   			result := g.supervisorRouter.Route(r.Context(), req.Message)
	   			switch result.Mode {
	   			case aiemployee.ModeAE:
	   				if result.TargetAE != "" && result.Confidence > 0 {
	   					// 匹配到DE，直接派发
	   					g.logger.Printf("[Supervisor] Routing to DE: %s (conf=%.2f, reason=%s)", result.TargetAE, result.Confidence, result.Reasoning)
	   					record, err := g.aeManager.Dispatch(r.Context(), result.TargetAE, req.Message, req.SessionId)
	   					if err != nil {
	   						g.logger.Printf("[Supervisor] DE dispatch error: %v", err)
	   						json.NewEncoder(w).Encode(map[string]interface{}{
	   							"type": "error", "message": fmt.Sprintf("AI员工启动失败: %v", err),
	   						})
	   						return
	   					}
	   					g.saveChatMessage(req.SessionId, "user", req.AgentName, req.Message, "pending", fmt.Sprintf("%s-de", record.ExecID), req.Channel, req.UserId)
	   					taskID := fmt.Sprintf("%s-de", record.ExecID)
	   					deTask := &GatewayTask{
	   						ID:        taskID,
	   						Request:   &req,
	   						Status:    "running",
	   						CreatedAt: time.Now(),
	   						ResultCh:  make(chan interface{}, 1),
	   					}
	   					deTask.ProgressCh = make(chan map[string]interface{}, 50)
	   					g.taskStreamsMu.Lock()
	   					g.taskStreams[taskID] = deTask.ProgressCh
	   					g.taskStreamsMu.Unlock()
	   					go g.streamDEProgress(taskID, "", record.ExecID, req.SessionId, deTask.ProgressCh)
	   					json.NewEncoder(w).Encode(GatewayResponse{
	   						TaskID:    taskID,
	   						Status:    "running",
	   						Timestamp: time.Now(),
	   					})
	   					return
	   				}
	   				// Enterprise mode: no match, return clarification
	   				if g.supervisorRouter.Identity() == "enterprise" {
	   					g.logger.Printf("[Supervisor] Enterprise mode: no DE match, asking clarification")
	   					json.NewEncoder(w).Encode(map[string]interface{}{
	   						"type":    "clarification",
	   						"message": result.Reasoning,
	   					})
	   					return
	   				}
	   				// Assistant mode: no match, fall through to normal agent
	   				g.logger.Printf("[Supervisor] Assistant mode: no DE match, falling through to agent")
	   			case aiemployee.ModeExplore:
	   				g.logger.Printf("[Supervisor] Explore mode: %s", result.Reasoning)
	   				// Fall through to normal agent handling
	   			case aiemployee.ModeCoordinator:
	   				g.logger.Printf("[Supervisor] Coordinator mode: %s", result.Reasoning)
	   				// Inject coordinator prompt + enable sub_agent parallelism
	   				// The agent factory will inject coordinatorSystemPrompt when coordinator flag is set
	   				req.AgentName = "coordinator"
	   			case aiemployee.ModeAuto:
	   				g.logger.Printf("[Supervisor] Auto mode: %s", result.Reasoning)
	   				// Context flag for auto-approval of safe tool calls
	   				ctx2 := context.WithValue(r.Context(), "auto_mode", true)
	   				r = r.WithContext(ctx2)
	   			default:
	   				g.logger.Printf("[Supervisor] Mode %s: falling through to agent", result.Mode)
	   			}
	   		} */

	// 验证 agent 名称：拒绝路径穿越和无效字符
	if strings.Contains(req.AgentName, "/") || strings.Contains(req.AgentName, "\\") ||
		strings.Contains(req.AgentName, "..") {
		http.Error(w, "Invalid agent name", http.StatusBadRequest)
		return
	}

	// Pipeline command: /pipeline <name> or /run <name>
	msg := strings.TrimSpace(req.Message)
	if !simpleQA && req.SkillRun == nil && (strings.HasPrefix(msg, "/pipeline ") || strings.HasPrefix(msg, "/run ")) {
		if webSelection != nil {
			sendJSONError(w, "工作流使用各步骤的模型配置；请通过工作流入口启动", http.StatusBadRequest)
			return
		}
		parts := strings.Fields(msg)
		if len(parts) >= 2 {
			pipelineName := parts[1]
			input := ""
			if len(parts) >= 3 {
				input = strings.Join(parts[2:], " ")
			} else {
				input = pipelineName
			}
			if g.pipelineExec != nil {
				// HTTP workflows do not have the ordinary task's frozen audience;
				// keep independent Entry recall, but exclude legacy mixed sources.
				exec, err := g.pipelineExec.Run(context.WithValue(r.Context(), "channel", "web"), pipelineName, input, req.SessionId, RunModeInteractive, "")
				if err != nil {
					g.logger.Printf("Pipeline error: %v", err)
					if req.Stream {
						w.Header().Set("Content-Type", "text/event-stream")
						fmt.Fprintf(w, "data: {\"error\":\"%s\"}\n\n", err.Error())
						return
					}
					http.Error(w, err.Error(), http.StatusBadRequest)
					return
				}
				if req.Stream {
					w.Header().Set("Content-Type", "text/event-stream")
					fmt.Fprintf(w, "data: {\"type\":\"pipeline_started\",\"exec_id\":\"%s\",\"pipeline\":\"%s\"}\n\n", exec.ID, pipelineName)
					// Stream status updates via SSE
					for {
						time.Sleep(500 * time.Millisecond)
						current, ok := g.pipelineExec.GetExecution(exec.ID)
						if !ok {
							break
						}
						if isTerminalGatewayTaskStatus(current.Status) {
							data, _ := json.Marshal(current)
							fmt.Fprintf(w, "data: {\"type\":\"pipeline_done\",\"status\":\"%s\",\"execution\":%s}\n\n", current.Status, string(data))
							break
						}
						if current.Status == "waiting_approval" {
							data, _ := json.Marshal(current)
							fmt.Fprintf(w, "data: {\"type\":\"pipeline_waiting\",\"execution\":%s}\n\n", string(data))
							break
						}
						// heartbeat
						fmt.Fprintf(w, "data: {\"type\":\"pipeline_running\",\"current_step\":%d}\n\n", current.CurrentStep)
					}
					return
				}
				w.Header().Set("Content-Type", "application/json; charset=utf-8")
				json.NewEncoder(w).Encode(map[string]interface{}{
					"status": "started", "exec_id": exec.ID, "pipeline": pipelineName,
				})
				return
			}
		}
	}

	// 生成任务ID（按 session 递增）
	if strings.TrimSpace(req.SessionId) == "" {
		req.SessionId = fmt.Sprintf("loc_%d", time.Now().UnixNano())
	}
	taskID := g.nextSessionTaskID(req.SessionId)
	req.TaskID = taskID

	sessionID := req.SessionId

	// 记录用户消息到数据库
	g.saveChatMessageWithAttachments(req.SessionId, "user", req.AgentName, visibleMessage, "pending", taskID, req.Channel, req.UserId, req.Attachments)

	// 创建任务
	task := &GatewayTask{
		ID:                taskID,
		Request:           &req,
		platformAccount:   platformAccount,
		webModelSelection: webSelection,
		webMemoryAudience: g.captureWebMemoryAudience(r, &req, resolvedWorkspace),
		Status:            "pending",
		CreatedAt:         time.Now(),
		ResolvedWorkspace: resolvedWorkspace,
		ResultCh:          make(chan interface{}, 1),
	}

	g.tasksMu.Lock()
	g.tasks[taskID] = task
	g.tasksMu.Unlock()

	// Create progress channel BEFORE starting goroutine (streamResponse reads it)
	task.ProgressCh = make(chan map[string]interface{}, 20)
	task.InterruptCh = make(chan string, 5)
	if sessionID != "" {
		storeInterruptChannel(sessionID, taskID, task.InterruptCh)
	}
	if req.Stream {
		g.taskStreamsMu.Lock()
		g.taskStreams[taskID] = task.ProgressCh
		g.taskStreamsMu.Unlock()
	}

	// 异步处理任务，传递 sessionID
	go g.processTaskWithSession(task, req.SessionId)

	if req.Stream {
		g.streamResponse(w, task)
		return
	}

	json.NewEncoder(w).Encode(GatewayResponse{
		TaskID:    taskID,
		Status:    "pending",
		Timestamp: time.Now(),
	})

}

func IsTaskCancelled(db *gorm.DB, taskID string) bool {
	if db == nil || taskID == "" {
		return false
	}

	var task GatewayTaskRecord
	result := db.Where("task_id = ?", taskID).First(&task)
	if result.Error != nil {
		return false
	}

	return task.Status == "stopped" || task.Status == "cancel"
}

func (g *GatewayService) processTaskWithSession(task *GatewayTask, sessionID string) {
	// Create cancellable context for task lifecycle
	ctx, cancel := g.newGatewayTaskContext()
	defer cancel()
	ctx = task.platformAccount.Context(ctx)
	if task.QueueName != agentOSQueueName {
		ctx = withWebModelSelection(ctx, task.webModelSelection)
		if task.webMemoryAudience != nil {
			ctx = context.WithValue(ctx, webMemoryAudienceKey{}, task.webMemoryAudience)
		}
	}
	ctx = g.withAgentOSRuntime(ctx)
	if task.QueueName == agentOSQueueName {
		ctx = g.withAgentOSApprovalRuntime(ctx)
	}
	ctx = agentos.WithLeaseToken(ctx, task.LeaseToken)
	ctx = projectctx.WithResolvedWorkspace(ctx, task.ResolvedWorkspace)
	ctx = context.WithValue(ctx, "task_id", task.ID)
	if task.ResumeCheckpoint != nil {
		ctx = sessioncore.WithTurnResumeState(ctx, task.ResumeCheckpoint, task.ResumedFromTaskID, task.ResumeSupplement)
	}
	if task.Request != nil {
		ctx = agentos.WithHTTPTrace(ctx, task.Request.RequestID, task.Request.Traceparent)
		ctx = context.WithValue(ctx, "session_id", task.Request.SessionId)
		ctx = context.WithValue(ctx, "channel", task.Request.Channel)
		ctx = context.WithValue(ctx, "user_id", task.Request.UserId)
		tenantID := strings.TrimSpace(task.TenantID)
		if tenantID == "" {
			tenantID = strings.TrimSpace(task.Request.TenantID)
		}
		_, subjectID, personallyScoped := gatewayTaskPersonalOwner(task)
		roleID := strings.TrimSpace(task.RoleID)
		if roleID == "" {
			roleID = strings.TrimSpace(task.Request.RoleID)
		}
		if roleID == "" {
			roleID = strings.TrimSpace(task.Request.AgentName)
		}
		ctx = withExecutionIdentity(ctx, ExecutionIdentity{
			TenantID:     tenantID,
			UserID:       task.Request.UserId,
			SubjectID:    subjectID,
			RoleID:       roleID,
			AgentName:    task.Request.AgentName,
			Source:       "agent_os",
			ServiceID:    task.CallerID,
			Service:      true,
			TenantScoped: personallyScoped,
		})
		if task.QueueName == agentOSQueueName {
			ctx = gatewayrisk.WithAgentOSRequestSecurity(
				ctx,
				attachmentcore.VisibleMessage(task.Request.Message, task.Request.Attachments),
				agentOSRequestSecurityOptions(),
			)
			ctx = gatewayhttp.WithAgentOSAssistantIdentity(ctx, task.Request.AssistantIdentity)
		}
	}

	// Cleanup progress channel when task completes
	defer func() {
		g.taskStreamsMu.Lock()
		delete(g.taskStreams, task.ID)
		g.taskStreamsMu.Unlock()
		deleteInterruptChannel(sessionID, task.ID)
		if task.ProgressCh != nil {
			close(task.ProgressCh)
		}
		if task.ResultCh != nil {
			close(task.ResultCh)
		}
	}()

	runningSnapshot, started := g.markGatewayTaskRunning(task, cancel)
	if !started {
		response := GatewayResponse{TaskID: task.ID, Status: "stopped", Timestamp: time.Now()}
		if runningSnapshot != nil {
			response.Status = runningSnapshot.Status
			response.Result = runningSnapshot.Result
			if runningSnapshot.Error != nil {
				response.Error = runningSnapshot.Error.Error()
			}
		}
		trySendGatewayTaskResult(task.ResultCh, response)
		return
	}
	g.broadcastTaskUpdate(task.ID, "running", nil, nil)

	// Push initial progress event
	trySendTaskProgress(task.ProgressCh, map[string]interface{}{
		"type":    "progress",
		"message": "Task started, analyzing request...",
		"status":  "running",
	})

	// 检查是否应该停止
	if ShouldStopTask(g.db, task.ID) {
		stoppedTask, _, _ := g.stopGatewayTask(task.ID)
		if g.db != nil {
			scopeChatHistoryForTask(
				g.db.Model(&ChatMessage{}).
					Where("task_id = ? AND role = ? AND status IN ?", task.ID, "user", []string{"pending", "running"}),
				task,
			).
				Update("status", "stopped")
		}
		response := GatewayResponse{
			TaskID:    task.ID,
			Status:    "stopped",
			Error:     "task stopped by user",
			Timestamp: time.Now(),
		}
		if stoppedTask != nil {
			response.Result = stoppedTask.Result
		}
		trySendGatewayTaskResult(task.ResultCh, response)
		return
	}

	if err := SaveGatewayTaskToDB(g.db, runningSnapshot); err != nil {
		g.logger.Printf("保存任务到数据库失败: %v", err)
	}

	message := task.Request.Message
	var appliedFollowupMessages []string
	var appliedFollowupLoadErr error
	if task.QueueName == agentOSQueueName {
		message = agentos.ExecutionMessage(message, task.Request.Parameters)
		var applied []agentos.TaskFollowupRecord
		for attempt := 0; attempt < 3; attempt++ {
			applied, appliedFollowupLoadErr = agentos.ListAppliedTaskFollowups(ctx, g.db, task.ID)
			if appliedFollowupLoadErr == nil {
				break
			}
			time.Sleep(time.Duration(attempt+1) * 25 * time.Millisecond)
		}
		if appliedFollowupLoadErr == nil {
			for _, followup := range applied {
				text := strings.TrimSpace(followup.Message)
				if text == "" {
					continue
				}
				appliedFollowupMessages = append(appliedFollowupMessages, text)
				message += "\n\n[用户补充]: " + text
			}
		}
	}
	agentName := task.Request.AgentName
	channel := task.Request.Channel
	userID := task.Request.UserId

	g.logger.Printf("开始处理任务 %s: SessionId=%s, UserId=%s, 消息长度=%d", task.ID, sessionID, userID, len([]rune(message)))
	startTime := time.Now()

	var result interface{}
	err := validateWebModelTaskSnapshot(task)
	if appliedFollowupLoadErr != nil {
		err = fmt.Errorf("load durable task follow-ups: %w", appliedFollowupLoadErr)
	}

	var historyMessages []map[string]interface{}
	if g.db != nil && sessionID != "" {
		var messages []ChatMessage
		query := g.db.Where("session_id = ? AND role IN ('user', 'assistant') AND task_id != ?", sessionID, task.ID)
		query = scopeChatHistoryForTask(query, task)
		if _, _, personallyScoped := gatewayTaskPersonalOwner(task); !personallyScoped && userID != "" {
			query = query.Where("user_id = ? OR user_id = ''", userID)
		}

		query.Order("created_at desc").
			Limit(50).
			Find(&messages)

		// 反转顺序，让历史消息按时间正序
		for i, j := 0, len(messages)-1; i < j; i, j = i+1, j-1 {
			messages[i], messages[j] = messages[j], messages[i]
		}

		if history := buildGatewaySessionHistory(messages); len(history.Messages) > 0 {
			historyMessages = history.Messages
			ctx = context.WithValue(ctx, promptMemoryHistoryKey{}, history.Sources)
			g.logger.Printf("加载了 %d 条已清理历史消息作为多轮对话上下文 (SessionId: %s)", len(historyMessages), sessionID)
		}
	} else {
		g.logger.Printf("db is nil")
	}

	taskType := task.Request.TaskType
	if taskType == "" {
		taskType = "chat"
	}
	if err == nil {
		switch taskType {
		case "qa":
			result, err = g.simpleChat(ctx, message, historyMessages)
		case "chat", "skill":
			var agent Agent
			var exists bool
			var cleanupAgent func()
			defer func() {
				if cleanupAgent != nil {
					cleanupAgent()
				}
			}()

			if task.Request.SkillRun == nil && task.QueueName == agentOSQueueName && g.aeManager != nil {
				if cfg, employeeTask, _ := matchAgentOSExplicitAIEmployee(g.aeManager, message); cfg != nil {
					g.logger.Printf("[AgentOS] explicit @mention routed to AI employee: %s", cfg.Name)
					agentName = cfg.Name
					employeeCtx := ctx
					if len(historyMessages) > 0 {
						employeeCtx = context.WithValue(employeeCtx, "history_messages", historyMessages)
					}
					result, err = g.runAgentOSExplicitAIEmployee(employeeCtx, task, cfg, employeeTask)
					break
				}
			}

			if agentName == "" {
				// 默认使用配置中排第一位的 Agent（确定性顺序，保证同一 session 内 agent 一致）
				agentName = g.engine.GetDefaultAgentName()
				if agentName == "" {
					err = fmt.Errorf("没有可用的Agent")
					break
				}
				foundName, foundAgent, found, cleanup, createErr := g.resolveChatAgentForGatewayTask(task, agentName, task.ResolvedWorkspace)
				if createErr != nil {
					err = fmt.Errorf("创建Agent %s 失败: %w", agentName, createErr)
					break
				}
				cleanupAgent = cleanup
				agentName = foundName
				agent = foundAgent
				exists = true
				if !found {
					err = fmt.Errorf("默认Agent %s 未找到", agentName)
					break
				}
			} else {
				foundName, foundAgent, found, cleanup, createErr := g.resolveChatAgentForGatewayTask(task, agentName, task.ResolvedWorkspace)
				if createErr != nil {
					err = fmt.Errorf("创建Agent %s 失败: %w", agentName, createErr)
					break
				}
				cleanupAgent = cleanup
				if found {
					agentName = foundName
					agent = foundAgent
					exists = true
				} else {
					availableAgents := g.engine.GetAgentNames()
					err = fmt.Errorf("agent %s not found (available: %v)", agentName, availableAgents)
				}
			}

			if !exists || agent == nil {
				err = fmt.Errorf("无法获取Agent: %s", agentName)
				break
			}
			agentCtx := ctx
			agentCtx = g.beginTaskMemoryReview(agentCtx, task, agentName)
			if task.Request.SkillRun != nil {
				agentCtx, err = g.beginSkillRun(agentCtx, task, agent)
				if err != nil {
					break
				}
				trySendTaskProgress(task.ProgressCh, map[string]interface{}{
					"type": "skill_run", "status": "running",
					"skill": task.Request.SkillRun.SkillName,
				})
			}
			// 支持中断注入：在 agent 执行期间，用户可通过 /api/interrupt 发送新消息
			// 新消息会取消当前执行，追加到对话中，重新启动 agent
			currentMessage := message
			currentUserInput := attachmentcore.VisibleMessage(
				task.Request.Message,
				task.Request.Attachments,
			)
			for _, followup := range appliedFollowupMessages {
				currentUserInput += "\n\n" + followup
			}
			for {
				stepCtx, stepCancel := context.WithTimeout(agentCtx, 180*time.Minute)
				stepCtx = sessioncore.WithTurnPersistencePathsObserver(stepCtx, func(_, transcriptPath string) {
					g.rememberGatewayTaskJournal(task, transcriptPath)
				})
				stepCtx = projectctx.WithResolvedWorkspace(stepCtx, task.ResolvedWorkspace)
				stepCtx = context.WithValue(stepCtx, "task_id", task.ID)
				stepCtx = context.WithValue(stepCtx, "session_id", sessionID)
				stepCtx = context.WithValue(stepCtx, "channel", channel)
				stepCtx = withTenantRequestEvidence(
					stepCtx,
					currentUserInput,
					task.Request.Attachments,
				)
				if task.QueueName == agentOSQueueName {
					stepCtx = gatewayrisk.WithAgentOSRequestSecurity(stepCtx, currentUserInput, agentOSRequestSecurityOptions())
				}
				if userID == "" {
					stepCtx = context.WithValue(stepCtx, "user_name", "local")
				}
				// 注入多轮对话历史到上下文
				if len(historyMessages) > 0 {
					stepCtx = context.WithValue(stepCtx, "history_messages", historyMessages)
					g.logger.Printf("注入 %d 条历史消息到上下文 (SessionId: %s)", len(historyMessages), sessionID)
				}
				// Broadcast task start so frontend shows progress timeline immediately
				g.BroadcastProgress(ProgressEvent{
					Type: "task_start", TaskID: task.ID,
					Message: "", Status: "running",
				})

				// 监听中断消息 - 收到后立即取消当前 generation
				interruptCh := make(chan gatewayTaskInterruption, 1)
				go func() {
					select {
					case msg, ok := <-task.InterruptCh:
						if !ok {
							return
						}
						interruptCh <- gatewayTaskInterruption{Message: msg}
						stepCancel()
					case followup, ok := <-task.FollowupCh:
						if !ok || followup == nil {
							return
						}
						interruptCh <- gatewayTaskInterruption{
							Message: followup.Message, Followup: followup,
						}
						stepCancel()
					case <-stepCtx.Done():
						return
					}
				}()

				g.ObserveTaskRuntime(task.ID, "model", "")
				result, err = agent.Generate(stepCtx, currentMessage)
				stepCancel()

				// 优先检查中断消息（中断取消导致的 context.Canceled 不视为任务失败）
				select {
				case interruption := <-interruptCh:
					interruptMsg := strings.TrimSpace(interruption.Message)
					if interruption.Followup != nil {
						applyErr := g.applyAgentOSTaskFollowup(ctx, task, interruption.Followup)
						if applyErr != nil {
							releaseCtx, releaseCancel := context.WithTimeout(context.Background(), 2*time.Second)
							_ = agentos.ReleaseTaskFollowupClaim(
								releaseCtx,
								g.db,
								interruption.Followup.FollowupID,
								task.LeaseToken,
								time.Now(),
							)
							releaseCancel()
							err = fmt.Errorf("apply durable task follow-up: %w", applyErr)
							result = nil
							break
						}
					}
					g.logger.Printf("任务 %s 收到中断消息，立即取消当前生成: %s", task.ID, textutil.TruncateRunes(interruptMsg, 100))
					currentMessage = currentMessage + "\n\n[用户补充]: " + interruptMsg
					currentUserInput = currentUserInput + "\n\n" + interruptMsg
					// 将中断消息加入历史，重启时 agent 能看到完整上下文
					historyMessages = append(historyMessages, map[string]interface{}{
						"role":    "user",
						"content": interruptMsg,
					})
					// 同时持久化到 DB 和 cold storage，防止丢失
					if g.db != nil && interruption.Followup == nil {
						if _, saveErr := g.saveChatMessageForTask(
							task, sessionID, "user", "", interruptMsg, "pending", task.ID, channel, userID,
						); saveErr != nil && g.logger != nil {
							g.logger.Printf("保存任务 %s 的中断消息失败: %v", task.ID, saveErr)
						}
					}
					g.BroadcastProgress(ProgressEvent{
						Type:      "progress",
						TaskID:    task.ID,
						AgentName: agentName,
						Message:   "收到新消息，重新处理: " + textutil.TruncateRunes(interruptMsg, 80),
						Status:    "running",
					})
					err = nil // 清除中断导致的 context.Canceled 错误
					continue
				default:
				}
				break
			}
		case "task":
			result, err = g.engine.Run(ctx)

		case "collaboration":
			// 处理协同任务
			result, err = g.handleCollaborationTask(ctx, task.Request)

		default:
			err = fmt.Errorf("unknown task type: %s", task.Request.TaskType)
		}
	}
	if pausedTask, paused := g.finalizeGatewayTaskPause(task); paused {
		response := GatewayResponse{TaskID: task.ID, Status: "paused", Timestamp: time.Now()}
		if pausedTask != nil {
			response.Status = pausedTask.Status
			response.Result = pausedTask.Result
		}
		trySendGatewayTaskResult(task.ResultCh, response)
		return
	}
	if required, ok := approvalcore.AsApprovalRequiredError(err); ok {
		waitingTask, suspendErr := g.suspendGatewayTaskForApproval(task, required)
		if suspendErr != nil {
			err = fmt.Errorf("pause task for approval: %w", suspendErr)
		} else {
			response := GatewayResponse{
				TaskID: task.ID, Status: "waiting_approval",
				Result: map[string]interface{}{
					"approval_id": required.Request.ApprovalID,
					"tool_name":   required.Request.ToolName,
				},
				Timestamp: time.Now(),
			}
			if waitingTask != nil {
				response.Status = waitingTask.Status
			}
			trySendGatewayTaskResult(task.ResultCh, response)
			if g.logger != nil {
				g.logger.Printf(
					"task %s paused for approval %s before tool %s",
					task.ID, required.Request.ApprovalID, required.Request.ToolName,
				)
			}
			return
		}
	}
	if err == nil && task.QueueName == agentOSQueueName {
		var schemaErr error
		result, schemaErr = normalizeAgentOSRoleOutput(task, result)
		if schemaErr != nil {
			err = schemaErr
		}
	}

	duration := time.Since(startTime)
	response := GatewayResponse{
		TaskID:    task.ID,
		Timestamp: time.Now(),
	}
	status := "completed"
	resultStr := gatewayResultText(result)
	userMessage := ""
	if err != nil {
		status = "failed"
		userMessage = strings.TrimSpace(resultStr)
		if executionErr, ok := agentresult.AsExecutionError(err); ok && executionErr.UserMessage() != "" {
			userMessage = executionErr.UserMessage()
		}
		if userMessage == "" {
			userMessage = err.Error()
		}
		if strings.TrimSpace(gatewayResultText(result)) == "" {
			result = userMessage
		}
	} else {
		if strings.TrimSpace(resultStr) == "" {
			resultStr = emptyGatewayCompletionMessage
			result = resultStr
			if g.logger != nil {
				g.logger.Printf("任务 %s completed with empty result; persisted diagnostic completion", task.ID)
			}
		}
	}

	terminalTask, applied := g.finishGatewayTask(task, status, result, err)
	if !applied {
		response.SkillRun = g.skillRunReceiptForTask(task.ID)
		response.Status = "stopped"
		if terminalTask != nil {
			response.Status = terminalTask.Status
			response.Result = terminalTask.Result
			if terminalTask.Error != nil {
				response.Error = terminalTask.Error.Error()
			}
		}
		trySendGatewayTaskResult(task.ResultCh, response)
		if g.logger != nil {
			g.logger.Printf("任务 %s 已进入终态 %s，忽略迟到的 %s 结果", task.ID, response.Status, status)
		}
		return
	}
	response.Status = status
	response.Result = terminalTask.Result
	response.SkillRun = g.skillRunReceiptForTask(task.ID)
	if terminalTask.Error != nil {
		response.Error = terminalTask.Error.Error()
	}

	if err != nil {
		g.saveChatMessageForTask(task, sessionID, "assistant", agentName, userMessage, "failed", task.ID, channel, userID)
		g.logger.Printf("任务 %s 失败: %v (耗时: %.2fs)", task.ID, err, duration.Seconds())
	} else {
		if g.db != nil {
			var existingMsg ChatMessage
			result2 := scopeRowsToPersistedOwner(
				g.db.Where("task_id = ? AND role = ?", task.ID, "assistant"),
				task.TenantID,
				task.SubjectID,
			).First(&existingMsg)

			if result2.Error == gorm.ErrRecordNotFound {
				taskType := ""
				if task.Request != nil {
					taskType = task.Request.TaskType
				}
				if shouldPersistAssistantCompletion(taskType, task.ID) {
					if _, saveErr := g.saveChatMessageForTask(task, sessionID, "assistant", agentName, resultStr, "completed", task.ID, channel, userID); saveErr != nil {
						if g.logger != nil {
							g.logger.Printf("任务 %s completed but failed to save assistant chat message: %v", task.ID, saveErr)
						}
						if task.ProgressCh != nil {
							select {
							case task.ProgressCh <- map[string]interface{}{
								"type":    "warning",
								"message": "任务已完成，但保存聊天回复失败: " + saveErr.Error(),
								"status":  "completed",
							}:
							default:
							}
						}
					}
				}
			} else if result2.Error == nil {
				g.logger.Printf("消息已存在，跳过保存: TaskID=%s", task.ID)
			} else if g.logger != nil {
				g.logger.Printf("任务 %s 查询已有回复失败: %v", task.ID, result2.Error)
			}
		}
		g.logger.Printf("任务 %s 完成 (耗时: %.2fs)", task.ID, duration.Seconds())
	}

	// Persist the terminal payload before publishing completion. Updating only
	// the status leaves gateway_task_records.result empty even when the agent
	// produced a valid final answer, so polling clients cannot recover it.
	if g.db != nil {
		if updateErr := scopeRowsToPersistedOwner(
			g.db.Model(&ChatMessage{}).
				Where("task_id = ? AND role = ? AND status IN ?", task.ID, "user", []string{"pending", "running"}),
			task.TenantID,
			task.SubjectID,
		).
			Update("status", terminalTask.Status).Error; updateErr != nil && g.logger != nil {
			g.logger.Printf("任务 %s 用户消息状态更新失败: %v", task.ID, updateErr)
		}
		if saveErr := SaveGatewayTaskToDB(g.db, terminalTask); saveErr != nil && g.logger != nil {
			g.logger.Printf("任务 %s 最终结果落库失败: %v", task.ID, saveErr)
		}
	}
	g.broadcastTaskUpdate(task.ID, terminalTask.Status, terminalTask.Result, terminalTask.Error)
	trySendGatewayTaskResult(task.ResultCh, response)
}

func (g *GatewayService) resolveChatAgentForTask(agentName, workspace string) (string, Agent, bool, func(), error) {
	if g == nil || g.engine == nil {
		return "", nil, false, nil, fmt.Errorf("engine is not initialized")
	}
	foundName, agent, found, err := g.engine.NewAgentForTaskInWorkspace(agentName, workspace)
	if err != nil {
		return foundName, nil, found, nil, err
	}
	if found {
		cleanup := newAgentRuntimeCleanup(agent)
		return foundName, agent, true, cleanup, nil
	}
	foundName, agent, found = g.engine.FindAgent(agentName)
	return foundName, agent, found, nil, nil
}

func (g *GatewayService) handleCollaborationTask(parent context.Context, req *MessageRequest) (interface{}, error) {
	if req == nil {
		return nil, fmt.Errorf("missing collaboration request")
	}
	// Broadcast task start so frontend shows progress timeline immediately
	g.BroadcastProgress(ProgressEvent{
		Type: "task_start", TaskID: req.TaskID,
		Message: "", Status: "running",
	})
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	ctx = context.WithValue(ctx, "task_id", req.TaskID)
	ctx = context.WithValue(ctx, "session_id", req.SessionId)
	ctx = context.WithValue(ctx, "channel", req.Channel)
	ctx = context.WithValue(ctx, "user_id", req.UserId)
	ctx = projectctx.WithResolvedWorkspace(ctx, req.ResolvedWorkspace)
	ctx = withTenantRequestEvidence(ctx, req.Message, req.Attachments)
	if req.Collaboration != nil {
		participants, cleanup, err := g.loadScopedCollaborationParticipants(
			*req.Collaboration,
			req.ResolvedWorkspace,
		)
		if err != nil {
			return nil, err
		}
		defer cleanup()
		return g.runScopedCollaboration(ctx, req, participants)
	}
	result, err := g.engine.Run(ctx)
	if err == nil {
		err = collaborationResultError(result)
	}
	return result, err
}

func collaborationResultError(result map[string]interface{}) error {
	if result == nil || strings.TrimSpace(fmt.Sprint(result["status"])) != "partial_success" {
		return nil
	}
	return fmt.Errorf("team collaboration incomplete: %v successful task(s), %v failed or skipped task(s)", result["successful_tasks"], result["failed_tasks"])
}

func detectAndConvertToUTF8(data []byte) ([]byte, error) {
	// 检查是否是有效的UTF-8
	if utf8.Valid(data) {
		return data, nil
	}

	encoders := []encoding.Encoding{
		simplifiedchinese.GBK,
		simplifiedchinese.HZGB2312,
		traditionalchinese.Big5,
		japanese.ShiftJIS,
		japanese.EUCJP,
		charmap.Windows1252,
	}

	var best []byte
	bestScore := -1
	for _, enc := range encoders {
		converted, err := decodeBytes(data, enc)
		if err != nil || !utf8.Valid(converted) {
			continue
		}
		score := textDecodeScore(string(converted))
		if score > bestScore {
			best = converted
			bestScore = score
		}
	}
	if best != nil {
		return best, nil
	}

	return nil, fmt.Errorf("unsupported text encoding")
}

// 检查是否包含Unicode替换字符
func hasReplacementChar(s string) bool {
	return strings.ContainsRune(s, '\uFFFD')
}

// 修复乱码文本
func fixGarbledText(garbled string) (string, error) {
	// 尝试将乱码当作GBK解码
	gbBytes := []byte(garbled)
	utf8Bytes, err := gbkToUTF8(gbBytes)
	if err == nil {
		return string(utf8Bytes), nil
	}

	// 尝试反转（如果是UTF-8被当作GBK显示）
	// 这种情况比较复杂，需要具体分析

	return garbled, fmt.Errorf("无法修复乱码")
}

func latin1ToUTF8(data []byte) ([]byte, error) {
	// Latin-1 可以直接映射到 Unicode 的前 256 个码点
	result := make([]rune, 0, len(data))
	for _, b := range data {
		result = append(result, rune(b))
	}
	return []byte(string(result)), nil
}
func toUTF8Bytes(data []byte) ([]byte, error) {
	// 检查是否已经是有效的 UTF-8
	if utf8.Valid(data) {
		return data, nil
	}

	return detectAndConvertToUTF8(data)
}
func decodeBig5(data []byte) string {
	decoded, err := decodeBytes(data, traditionalchinese.Big5)
	if err != nil {
		return string(data)
	}
	return string(decoded)
}

// 添加日文解码函数
func decodeJapanese(data []byte) string {
	decoded, err := decodeBytes(data, japanese.ShiftJIS)
	if err != nil {
		return string(data)
	}
	return string(decoded)
}

// 检查字符串是否包含中文字符
func containsChinese(s string) bool {
	for _, r := range s {
		if unicode.Is(unicode.Han, r) {
			return true
		}
	}
	return false
}

// 改进的 GBK 解码函数
func decodeGBK(data []byte) string {
	decoded, err := decodeBytes(data, simplifiedchinese.GBK)
	if err != nil {
		return string(data)
	}
	return string(decoded)
}

func decodeBytes(data []byte, enc encoding.Encoding) ([]byte, error) {
	reader := transform.NewReader(bytes.NewReader(data), enc.NewDecoder())
	return io.ReadAll(reader)
}

func textDecodeScore(s string) int {
	score := 0
	for _, r := range s {
		switch {
		case unicode.Is(unicode.Han, r):
			score += 10
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			score += 2
		case unicode.IsSpace(r) || strings.ContainsRune("{}[],:._-\"'", r):
			score++
		case r == '\uFFFD' || r == '?':
			score -= 8
		}
	}
	return score
}

func toUTF8(s string) string {
	// 尝试将字符串转换为 UTF-8
	if utf8.ValidString(s) {
		return s
	}

	// 如果不是有效的 UTF-8，尝试从其他编码转换
	// 这里简单返回原字符串，实际可能需要更复杂的转换
	return s
}

// 处理任务请求
func (g *GatewayService) handleTask(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req MessageRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	g.assessAndLogGatewayUserContent("task", req.Message)

	if strings.TrimSpace(req.SessionId) == "" {
		req.SessionId = fmt.Sprintf("loc_%d", time.Now().UnixNano())
	}
	if g.rejectLegacyAgentOSSession(w, r, req.SessionId) {
		return
	}
	taskID := g.nextSessionTaskID(req.SessionId)
	req.TaskID = taskID

	task := &GatewayTask{
		ID:         taskID,
		Request:    &req,
		Status:     "pending",
		CreatedAt:  time.Now(),
		ResultCh:   make(chan interface{}, 1),
		ProgressCh: make(chan map[string]interface{}, 20),
	}

	g.tasksMu.Lock()
	g.tasks[taskID] = task
	g.tasksMu.Unlock()

	go g.processTask(task)

	// 等待结果（带超时）
	select {
	case result := <-task.ResultCh:
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		json.NewEncoder(w).Encode(result)
	case <-time.After(30 * time.Second):
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusAccepted)
		json.NewEncoder(w).Encode(GatewayResponse{
			TaskID:    taskID,
			Status:    "processing",
			Timestamp: time.Now(),
		})
	}
}

// 查询任务状态
func (g *GatewayService) handleTaskStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	taskID := strings.TrimPrefix(r.URL.Path, "/api/task/")

	task, exists := g.gatewayTaskSnapshotWithDB(taskID)

	if !exists || task.QueueName == agentOSQueueName {
		http.Error(w, "Task not found", http.StatusNotFound)
		return
	}

	response := GatewayResponse{
		TaskID:    task.ID,
		Status:    task.Status,
		Recovery:  recoveryInfoFromTask(task),
		SkillRun:  g.skillRunReceiptForTask(task.ID),
		Timestamp: time.Now(),
	}

	if task.Result != nil {
		response.Result = task.Result
	}
	if task.Status == "failed" && task.Error != nil {
		response.Error = task.Error.Error()
	} else if task.Status == "stopped" && task.Error != nil {
		response.Error = task.Error.Error()
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(response)
}

// 列出可用Agent
func (g *GatewayService) handleListAgents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// 从引擎获取所有Agent
	agents := g.engine.GetAllAgents()

	agentList := make([]map[string]interface{}, 0, len(agents))
	for _, name := range g.engine.GetAgentNames() {
		agent := agents[name]
		if agent == nil {
			continue
		}
		info := map[string]interface{}{
			"name": name,
			"type": agent.GetType(),
		}
		for _, configured := range g.engine.config.Agents {
			if configured.Name == name {
				info["model_override"] = strings.TrimSpace(configured.Model)
				break
			}
		}
		if a, ok := agent.(*LLMAgent); ok {
			info["model_config"] = a.modelKey
			info["model"] = a.modelConfig.ModelName
			info["model_type"] = a.modelConfig.Type
			info["base_url"] = a.modelConfig.BaseURL
			info["max_tokens"] = a.modelConfig.MaxTokens
		}
		agentList = append(agentList, info)
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"agents":        agentList,
		"count":         len(agentList),
		"default_agent": g.engine.GetDefaultAgentName(),
	})
}

// 健康检查
var serverStartTime = time.Now()

func (g *GatewayService) handleAPIStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	status := map[string]interface{}{
		"version":      "1.1",
		"tools":        0,
		"hooks":        0,
		"agents":       0,
		"plugins":      0,
		"mode":         "default",
		"plan":         false,
		"cron_tasks":   0,
		"snapshots":    0,
		"worktrees":    "none",
		"git_branch":   "",
		"tool_metrics": "",
		"project":      g.project,
	}
	if g.aeManager != nil {
		status["agents"] = len(g.aeManager.employees)
	}
	if g.config != nil {
		if g.config.PermissionMode != "" {
			status["mode"] = g.config.PermissionMode
		}
	}
	// Git branch
	if branch := strings.TrimSpace(gitutil.Output(".", "rev-parse", "--abbrev-ref", "HEAD")); branch != "" {
		status["git_branch"] = branch
	}
	// Tool metrics
	status["tool_metrics"] = toolmetrics.Report()
	json.NewEncoder(w).Encode(status)
}

func (g *GatewayService) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	dbOK := true
	if g.db != nil {
		if err := g.db.Exec("SELECT 1").Error; err != nil {
			dbOK = false
		}
	}
	agentCount := 0
	if g.engine != nil {
		agents := g.engine.getAllAgents()
		agentCount = len(agents)
	}
	aeCount := 0
	if g.aeManager != nil {
		g.aeManager.mu.RLock()
		aeCount = len(g.aeManager.employees)
		g.aeManager.mu.RUnlock()
	}
	g.tasksMu.RLock()
	taskCount := len(g.tasks)
	runningTasks := 0
	for _, task := range g.tasks {
		if task != nil && task.Status == "running" {
			runningTasks++
		}
	}
	g.tasksMu.RUnlock()
	queueStats, queueErr := g.ensureAgentOSQueue().stats(r.Context())
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":         "healthy",
		"version":        CurrentVersion,
		"uptime_sec":     int(time.Since(serverStartTime).Seconds()),
		"time":           time.Now().Format(time.RFC3339),
		"tasks":          taskCount,
		"running_tasks":  runningTasks,
		"db":             dbOK,
		"agents":         agentCount,
		"employees":      aeCount,
		"agent_os_queue": queueStats,
		"agent_os_queue_error": func() string {
			if queueErr != nil {
				return queueErr.Error()
			}
			return ""
		}(),
	})
}

func (g *GatewayService) processTask(task *GatewayTask) {
	ctx, cancel := g.newGatewayTaskContext()
	defer cancel()
	ctx = g.withAgentOSRuntime(ctx)
	ctx = projectctx.WithResolvedWorkspace(ctx, task.ResolvedWorkspace)
	defer func() {
		if task.ProgressCh != nil {
			close(task.ProgressCh)
		}
		if task.ResultCh != nil {
			close(task.ResultCh)
		}
	}()
	runningSnapshot, started := g.markGatewayTaskRunning(task, cancel)
	if !started {
		response := GatewayResponse{TaskID: task.ID, Status: "stopped", Timestamp: time.Now()}
		if runningSnapshot != nil {
			response.Status = runningSnapshot.Status
			response.Result = runningSnapshot.Result
			if runningSnapshot.Error != nil {
				response.Error = runningSnapshot.Error.Error()
			}
		}
		trySendGatewayTaskResult(task.ResultCh, response)
		return
	}
	ctx = context.WithValue(ctx, "task_id", task.ID)
	if task.Request != nil {
		ctx = context.WithValue(ctx, "session_id", task.Request.SessionId)
		ctx = context.WithValue(ctx, "channel", task.Request.Channel)
		ctx = context.WithValue(ctx, "user_id", task.Request.UserId)
	}

	// 获取原始请求数据
	message := task.Request.Message
	agentName := task.Request.AgentName

	g.logger.Printf("开始处理任务 %s: 消息长度=%d", task.ID, len([]rune(message)))
	g.logger.Printf("请求的 Agent: %s", agentName)

	startTime := time.Now()

	var result interface{}
	var err error
	if task.Request.SkillRun != nil {
		if g.db == nil {
			err = fmt.Errorf("Skill Run requires a durable database")
		} else if saveErr := SaveGatewayTaskToDB(g.db, runningSnapshot); saveErr != nil {
			err = fmt.Errorf("persist running Skill task: %w", saveErr)
		}
	}

	taskType := task.Request.TaskType
	if taskType == "" {
		taskType = "chat"
	}
	if err == nil {
		switch taskType {
		case "chat", "skill":
			// 单Agent聊天
			var agent Agent
			var exists bool
			var cleanupAgent func()
			defer func() {
				if cleanupAgent != nil {
					cleanupAgent()
				}
			}()

			if agentName == "" {
				// Reuse the engine's deterministic default resolution. It first
				// honors configured preference and then falls back to the sorted
				// runtime catalog, matching every other chat entrypoint.
				foundName, foundAgent, found := g.engine.DefaultAgent()
				if !found || foundAgent == nil {
					err = fmt.Errorf("没有可用的Agent")
					break
				}
				agentName = foundName
				agent = foundAgent
				exists = true
			} else {
				// 尝试查找指定名称的Agent
				foundName, foundAgent, found := g.engine.FindAgent(agentName)
				if found {
					agentName = foundName
					agent = foundAgent
					exists = true
					g.logger.Printf("找到匹配的Agent: %s", agentName)
				} else {
					availableAgents := g.engine.GetAgentNames()
					err = fmt.Errorf("agent %s not found (available: %v)", agentName, availableAgents)
					g.logger.Printf("错误: %v", err)
					break
				}
			}
			if exists && task.Request.SkillRun != nil && strings.TrimSpace(task.ResolvedWorkspace) != "" {
				foundName, scopedAgent, found, cleanup, createErr := g.resolveChatAgentForGatewayTask(
					task, agentName, task.ResolvedWorkspace,
				)
				if createErr != nil {
					err = fmt.Errorf("创建Agent %s 失败: %w", agentName, createErr)
					break
				}
				if found && scopedAgent != nil {
					agentName = foundName
					agent = scopedAgent
					exists = true
					cleanupAgent = cleanup
				}
			}

			if !exists || agent == nil {
				err = fmt.Errorf("无法获取Agent: %s", agentName)
				break
			}

			// 记录原始消息
			g.logger.Printf("使用 Agent: %s, 消息长度: %d", agentName, len([]rune(message)))

			// 加载历史消息构建多轮对话上下文
			stepCtx := ctx
			sid := task.Request.SessionId
			if g.db != nil && sid != "" {
				var dbMessages []ChatMessage
				query := g.db.Where("session_id = ? AND role IN ('user', 'assistant') AND task_id != ?", sid, task.ID)
				scopeChatHistoryForTask(query, task).
					Order("created_at desc").Limit(50).Find(&dbMessages)
				if len(dbMessages) > 0 {
					// 反转顺序
					for i, j := 0, len(dbMessages)-1; i < j; i, j = i+1, j-1 {
						dbMessages[i], dbMessages[j] = dbMessages[j], dbMessages[i]
					}
					if history := buildGatewaySessionHistory(dbMessages); len(history.Messages) > 0 {
						stepCtx = context.WithValue(stepCtx, "history_messages", history.Messages)
						g.logger.Printf("注入 %d 条已清理历史消息到上下文 (SessionId: %s)", len(history.Messages), sid)
					}
				}
			}
			if task.Request.SkillRun != nil {
				stepCtx, err = g.beginSkillRun(stepCtx, task, agent)
				if err != nil {
					break
				}
			}

			// 调用 Agent
			g.ObserveTaskRuntime(task.ID, "model", "")
			result, err = agent.Generate(stepCtx, message)

		case "task":
			result, err = g.engine.Run(ctx)

		default:
			err = fmt.Errorf("unknown task type: %s", taskType)
		}
	}

	duration := time.Since(startTime)
	response := GatewayResponse{
		TaskID:    task.ID,
		Timestamp: time.Now(),
	}
	status := "completed"
	if err != nil {
		status = "failed"
		failureOutput := agentresult.FailureUserMessage(gatewayResultText(result), err)
		if strings.TrimSpace(gatewayResultText(result)) == "" && failureOutput != "" {
			result = failureOutput
		}
	}
	terminalTask, applied := g.finishGatewayTask(task, status, result, err)
	if terminalTask != nil {
		response.Status = terminalTask.Status
		response.Result = terminalTask.Result
		if terminalTask.Error != nil {
			response.Error = terminalTask.Error.Error()
		}
	}
	response.SkillRun = g.skillRunReceiptForTask(task.ID)
	trySendGatewayTaskResult(task.ResultCh, response)
	if !applied {
		if g.logger != nil {
			g.logger.Printf("任务 %s 已进入终态 %s，忽略迟到的 %s 结果", task.ID, response.Status, status)
		}
		return
	}
	g.broadcastTaskUpdate(task.ID, terminalTask.Status, terminalTask.Result, terminalTask.Error)
	if err != nil {
		g.logger.Printf("任务 %s 失败: %v (耗时: %.2fs)", task.ID, err, duration.Seconds())
	} else {
		g.logger.Printf("任务 %s 完成 (耗时: %.2fs)", task.ID, duration.Seconds())
	}
}

func detectEncoding(data []byte) string {
	if utf8.Valid(data) {
		return "UTF-8"
	}

	// 检查是否为 GBK 或 GB2312
	// 中文字符在 GBK/GB2312 中通常是两个字节，且每个字节的高位为1
	i := 0
	gbkValid := true
	for i < len(data) {
		if data[i] <= 127 {
			i++
			continue
		}
		// 如果是双字节字符
		if i+1 < len(data) && data[i] > 127 && data[i+1] > 127 {
			i += 2
			continue
		}
		gbkValid = false
		break
	}

	if gbkValid {
		// 进一步区分是 GBK 还是 GB2312
		// 这里简化处理，返回 "GBK/GB2312"
		return "GBK/GB2312"
	}

	return "Unknown"
}

func utf8ToGBK(data []byte) ([]byte, error) {
	if len(data) == 0 {
		return data, nil
	}

	encoder := simplifiedchinese.GBK.NewEncoder()
	reader := transform.NewReader(bytes.NewReader(data), encoder)
	gbkData, err := io.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("failed to convert from UTF-8 to GBK: %v", err)
	}
	return gbkData, nil
}

// 添加一个简单的测试页面来验证中文编码

const gatewaySessionHistoryMaxRunes = 1200

type gatewaySessionHistory struct {
	Messages []map[string]interface{}
	Context  string
	Sources  []promptMemoryCandidate
}

// buildGatewaySessionHistory is the single conversion boundary between raw
// persisted ChatMessage rows and model-visible history. Callers may use the
// role-aware message list (chat paths) or the equivalent textual context
// (collaboration path). It never mutates the rows supplied by the database.
func buildGatewaySessionHistory(messages []ChatMessage) gatewaySessionHistory {
	history := gatewaySessionHistory{
		Messages: make([]map[string]interface{}, 0, len(messages)),
	}
	for _, msg := range messages {
		role := strings.ToLower(strings.TrimSpace(msg.Role))
		if role != "user" && role != "assistant" {
			continue
		}
		content := sessioncore.CleanCarryoverContent(role, msg.Content, gatewaySessionHistoryMaxRunes)
		if content == "" {
			continue
		}
		history.Messages = append(history.Messages, map[string]interface{}{
			"role":    role,
			"content": content,
		})
		if msg.ID != 0 {
			id := strconv.FormatUint(uint64(msg.ID), 10)
			locator := memorycore.PromptLocator{Kind: "chat_message", ID: id, SessionID: msg.SessionID, Field: "content", Digest: promptMemoryDigest(msg.Content)}
			history.Sources = append(history.Sources, promptMemoryCandidate{role: role, block: content, history: true, historyOrdinal: len(history.Messages) - 1, source: memorycore.PromptSource{Kind: "chat_message", Category: "history",
				Title: role + " message", SourceLabel: "chat_messages#" + id + ".content", Excerpt: content, CapturedRevision: locator.Digest, SourceSessionID: msg.SessionID, Locator: locator}})
		}
	}
	if len(history.Messages) == 0 {
		return history
	}

	var contextBuilder strings.Builder
	contextBuilder.WriteString("以下是历史对话记录，请基于这些历史对话回答用户的问题：\n\n")
	for _, msg := range history.Messages {
		role, _ := msg["role"].(string)
		content, _ := msg["content"].(string)
		roleName := "用户"
		if role == "assistant" {
			roleName = "助手"
		}
		contextBuilder.WriteString(fmt.Sprintf("%s: %s\n", roleName, content))
	}
	contextBuilder.WriteString("\n当前问题:\n")
	history.Context = contextBuilder.String()
	return history
}

type ChatMessage struct {
	ID               uint                     `gorm:"primarykey" json:"id"`
	SessionID        string                   `gorm:"index;size:64" json:"session_id"`
	TenantID         string                   `gorm:"index:idx_chat_messages_owner;size:64;not null;default:default" json:"tenant_id,omitempty"`
	SubjectID        string                   `gorm:"index:idx_chat_messages_owner;size:128;not null;default:''" json:"subject_id,omitempty"`
	Role             string                   `gorm:"size:20" json:"role"` // user, assistant, system
	AgentName        string                   `gorm:"size:100" json:"agent_name"`
	UserId           string                   `gorm:"size:128" json:"user_id"`
	Content          string                   `gorm:"type:text" json:"content"`
	Attachments      []attachmentcore.File    `gorm:"serializer:json;type:text" json:"attachments,omitempty"`
	Generation       *conversation.Generation `gorm:"serializer:json;type:text" json:"generation,omitempty"`
	MediaOwner       string                   `gorm:"index;size:64;default:''" json:"-"`
	MediaRequestID   *string                  `gorm:"uniqueIndex;size:32" json:"-"`
	MediaPayloadHash string                   `gorm:"size:64" json:"-"`
	Status           string                   `gorm:"size:20;default:'completed'" json:"status"`
	TaskID           string                   `gorm:"index;size:64" json:"task_id"`
	Channel          string                   `gorm:"size:50" json:"channel"` // 新增：消息来源渠道
	CreatedAt        time.Time                `json:"created_at"`
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// ChatSession 聊天会话模型
type ChatSession struct {
	ID            string    `gorm:"primaryKey;size:64" json:"id"`
	Name          string    `gorm:"size:200" json:"name"`
	AgentName     string    `gorm:"size:100" json:"agent_name"`
	TenantID      string    `gorm:"index:idx_chat_sessions_owner;size:64;not null;default:default" json:"tenant_id,omitempty"`
	SubjectID     string    `gorm:"index:idx_chat_sessions_owner;size:128;not null;default:''" json:"subject_id,omitempty"`
	UserId        string    `gorm:"index;size:128" json:"user_id"` // 所属用户
	MediaOwner    string    `gorm:"size:64;default:''" json:"-"`
	MessageCount  int       `gorm:"default:0" json:"message_count"`
	Workspace     string    `gorm:"size:1024" json:"workspace"`
	ProjectID     string    `gorm:"size:64;index" json:"project_id"`
	FirstQuestion string    `gorm:"column:first_question;->;-:migration" json:"first_question,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// ConfigHistory 配置历史记录
type ConfigHistory struct {
	ID        uint      `gorm:"primarykey" json:"id"`
	Version   int       `json:"version"`
	Config    string    `gorm:"type:text" json:"config"`
	CreatedAt time.Time `json:"created_at"`
}

// unescapeJSON converts \uXXXX escape sequences to actual Unicode characters.
func unescapeJSON(s string) string {
	var buf strings.Builder
	i := 0
	for i < len(s) {
		if i+5 < len(s) && s[i] == '\\' && s[i+1] == 'u' {
			var h [4]byte
			copy(h[:], s[i+2:i+6])
			var v rune
			for _, c := range h {
				v = v*16 + rune(func(b byte) byte {
					if b >= '0' && b <= '9' {
						return b - '0'
					}
					if b >= 'a' && b <= 'f' {
						return b - 'a' + 10
					}
					if b >= 'A' && b <= 'F' {
						return b - 'A' + 10
					}
					return 0
				}(c))
			}
			buf.WriteRune(v)
			i += 6
		} else {
			buf.WriteByte(s[i])
			i++
		}
	}
	return buf.String()
}

// streamDEProgress polls a DE pipeline execution and broadcasts progress via SSE.
func (g *GatewayService) streamDEProgress(taskID, deName, execID, sessionID string, progressCh chan map[string]interface{}) {
	defer close(progressCh)

	g.BroadcastProgress(ProgressEvent{
		Type: "task_start", TaskID: taskID,
		Message: fmt.Sprintf("AI员工「%s」启动", deName),
		Status:  "running",
	})

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	// Track which tool calls we've already broadcast, per step
	seenCalls := make(map[string]int) // "stepName" -> count of tool calls already broadcast
	startedSteps := make(map[string]bool)
	completedSteps := make(map[string]bool)
	timeout := time.After(15 * time.Minute)

	for {
		select {
		case <-ticker.C:
			if g.pipelineExec == nil {
				continue
			}
			exec, ok := g.pipelineExec.GetExecution(execID)
			if !ok {
				continue
			}
			execStatus, stepSnapshot := exec.progressSnapshot()

			// Broadcast per-step tool call progress
			for idx, s := range stepSnapshot {
				key := pipelineProgressStepKey(idx, s)
				prev := seenCalls[key]

				// Notify when a step starts running for the first time
				if s.Status == "running" && !startedSteps[key] {
					startedSteps[key] = true
					statusMsg := fmt.Sprintf("▶ 开始 %s", s.Name)
					g.BroadcastProgress(ProgressEvent{
						Type: "task_start", TaskID: taskID,
						Message: statusMsg, Status: "running",
					})
				}

				// Broadcast new tool calls since last poll
				for i := prev; i < len(s.ToolCalls); i++ {
					tc := s.ToolCalls[i]
					params := textutil.TruncateRunes(unescapeJSON(tc.Params), eventbus.ProgressToolCommandDetailLimit)
					msg := fmt.Sprintf("%s %s", tc.ToolName, params)
					g.BroadcastProgress(ProgressEvent{
						Type:        "tool_start",
						TaskID:      taskID,
						ExecutionID: execID,
						StepName:    s.Name,
						Attempt:     max(s.Revision, 1),
						Message:     textutil.TruncateRunes(msg, toolcontract.ToolFailureSummaryLimit),
						ToolName:    tc.ToolName,
						ToolCallID:  tc.CallID,
						Status:      "running",
					})
					endStatus, endMessage := pipelineToolCallProgressOutcome(tc)
					g.BroadcastProgress(ProgressEvent{
						Type:        "tool_end",
						TaskID:      taskID,
						ExecutionID: execID,
						StepName:    s.Name,
						Attempt:     max(s.Revision, 1),
						Message:     endMessage,
						ToolName:    tc.ToolName,
						ToolCallID:  tc.CallID,
						Status:      endStatus,
					})
				}
				seenCalls[key] = len(s.ToolCalls)

				// Notify when a step reaches a successful or degraded terminal state.
				if (s.Status == "completed" || s.Status == "degraded") && !completedSteps[key] {
					completedSteps[key] = true
					completeMsg := fmt.Sprintf("✅ %s 完成 (%dms, %d次工具调用)", s.Name, s.DurationMs, len(s.ToolCalls))
					completeStatus := "success"
					if s.Status == "degraded" {
						completeMsg = fmt.Sprintf("⚠️ %s 已完成，部分资料暂未取得 (%dms, %d次工具调用)", s.Name, s.DurationMs, len(s.ToolCalls))
						completeStatus = "warning"
					}
					g.BroadcastProgress(ProgressEvent{
						Type: "complete", TaskID: taskID,
						Message: completeMsg, Status: completeStatus,
					})
				}
			}

			switch execStatus {
			case "completed", "degraded":
				finalOutput, _ := pipelineFinalOutputFromSteps(stepSnapshot)
				if finalOutput == "" {
					finalOutput = fmt.Sprintf("AI employee %q task completed", deName)
				}
				gatewayStatus := aeUserFacingTaskStatus(execStatus, finalOutput)
				if pipelineExecutionWaitsForInput(exec) {
					gatewayStatus = aeWaitingInputStatus
				}
				g.tasksMu.Lock()
				if task, ok := g.tasks[taskID]; ok {
					task.Status = gatewayStatus
					task.Result = finalOutput
					now := time.Now()
					task.CompletedAt = &now
					if err := SaveGatewayTaskToDB(g.db, task); err != nil {
						g.logger.Printf("[AE] update completed task failed: %v", err)
					}
				}
				g.tasksMu.Unlock()
				completionMessage := fmt.Sprintf("AI员工「%s」任务已完成", deName)
				completionEventStatus := "success"
				if execStatus == "degraded" && gatewayStatus == "completed" {
					completionMessage = fmt.Sprintf("AI员工「%s」任务已完成；部分资料暂未取得，原因已在报告中说明", deName)
					completionEventStatus = "warning"
				} else if gatewayStatus == aeWaitingInputStatus {
					completionMessage = fmt.Sprintf("AI员工「%s」本次执行已停止；请重新 @%s，并将原需求与缺失信息合并成一条完整新请求后从头发起", deName, strings.TrimPrefix(strings.TrimSpace(deName), "@"))
					completionEventStatus = "warning"
				}
				g.BroadcastProgress(ProgressEvent{
					Type: "complete", TaskID: taskID,
					Message: completionMessage, Status: completionEventStatus,
				})
				g.broadcastTaskUpdate(taskID, gatewayStatus, finalOutput, nil)
				return
			case "failed", "stopped":
				g.tasksMu.Lock()
				if task, ok := g.tasks[taskID]; ok {
					task.Status = execStatus
					task.Error = fmt.Errorf("pipeline %s", execStatus)
					now := time.Now()
					task.CompletedAt = &now
					if err := SaveGatewayTaskToDB(g.db, task); err != nil {
						g.logger.Printf("[AE] update failed task failed: %v", err)
					}
				}
				g.tasksMu.Unlock()
				g.BroadcastProgress(ProgressEvent{
					Type: "error", TaskID: taskID,
					Message: fmt.Sprintf("AI employee %q task %s", deName, execStatus), Status: "error",
				})
				g.broadcastTaskUpdate(taskID, execStatus, nil, fmt.Errorf("pipeline %s", execStatus))
				return
			}

		case <-timeout:
			g.tasksMu.Lock()
			if task, ok := g.tasks[taskID]; ok {
				task.Status = "timeout"
				task.Error = fmt.Errorf("timeout")
				now := time.Now()
				task.CompletedAt = &now
				if err := SaveGatewayTaskToDB(g.db, task); err != nil {
					g.logger.Printf("[AE] update timeout task failed: %v", err)
				}
			}
			g.tasksMu.Unlock()
			g.BroadcastProgress(ProgressEvent{
				Type: "error", TaskID: taskID,
				Message: fmt.Sprintf("AI员工「%s」任务超时", deName), Status: "error",
			})
			g.broadcastTaskUpdate(taskID, "timeout", nil, fmt.Errorf("timeout"))
			return
		}
	}
}

func pipelineToolCallProgressOutcome(tc ToolCallRecord) (string, string) {
	if pipelineToolCallRecordFailed(tc) {
		detail := strings.TrimSpace(tc.Error)
		if detail == "" {
			detail = strings.TrimSpace(tc.Result)
		}
		return "error", fmt.Sprintf("%s failed: %s", tc.ToolName, textutil.TruncateRunes(detail, toolcontract.ToolFailureSummaryLimit))
	}

	var params map[string]interface{}
	_ = json.Unmarshal([]byte(tc.Params), &params)
	suffix := ""
	if url, ok := params["url"].(string); ok && url != "" {
		suffix = " ← " + textutil.TruncateRunes(url, 60)
	} else if query, ok := params["query"].(string); ok && query != "" {
		suffix = " ← q=" + textutil.TruncateRunes(query, 40)
	}
	return "success", fmt.Sprintf("%s completed%s", tc.ToolName, suffix)
}

func pipelineProgressStepKey(stepIndex int, step StepResult) string {
	revision := step.Revision
	if revision <= 0 {
		revision = 1
	}
	return fmt.Sprintf("%d:%s:r%d", stepIndex, step.Name, revision)
}

// 流式响应
func (g *GatewayService) streamResponse(w http.ResponseWriter, task *GatewayTask) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
		return
	}

	// Send initial connected event
	fmt.Fprintf(w, "event: connected\ndata: {\"task_id\":\"%s\"}\n\n", task.ID)
	flusher.Flush()

	progressCh := task.ProgressCh
	noResult := task.ResultCh == nil

	// Heartbeat ticker: 每15秒发送keepalive防止代理断开
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()

	for {
		select {
		case evt, ok := <-progressCh:
			if !ok {
				// channel已关闭, 等待最终结果
				progressCh = nil
				if noResult {
					return // 无ResultCh, 直接结束
				}
				continue
			}
			data, _ := json.Marshal(evt)
			fmt.Fprintf(w, "event: progress\ndata: %s\n\n", string(data))
			flusher.Flush()

		case result, ok := <-task.ResultCh:
			if !ok {
				return // ResultCh closed, done
			}
			fmt.Fprintf(w, "event: result\ndata: %s\n\n", mustJSON(result))
			flusher.Flush()
			return

		case <-heartbeat.C:
			// 心跳保活: 防止NAT/代理断开空闲连接
			fmt.Fprintf(w, ": heartbeat\n\n")
			flusher.Flush()
		}
	}
}

// 清理过期任务
func (g *GatewayService) cleanupTasks(ctx context.Context) {
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			g.tasksMu.Lock()
			for id, task := range g.tasks {
				if task.CompletedAt != nil && time.Since(*task.CompletedAt) > 24*time.Hour {
					delete(g.tasks, id)
					g.logger.Printf("清理过期任务: %s", id)
				}
			}
			g.tasksMu.Unlock()
		}
	}
}

func gbkToUTF8(data []byte) ([]byte, error) {
	if len(data) == 0 {
		return data, nil
	}
	// 首先尝试判断是否是 GBK 编码
	// GBK/GB2312 的特征：中文字符通常以两个字节表示，且每个字节的高位为1（即 > 127）
	hasHighBit := false
	for _, b := range data {
		if b > 127 {
			hasHighBit = true
			break
		}
	}

	// 如果没有高位为1的字节，说明是纯 ASCII，直接返回
	if !hasHighBit {
		return data, nil
	}

	// 尝试作为 GBK 解码
	decoder := simplifiedchinese.GBK.NewDecoder()

	// 使用 transform 进行转换
	reader := transform.NewReader(bytes.NewReader(data), decoder)
	utf8Data, err := io.ReadAll(reader)
	if err == nil && len(utf8Data) > 0 {
		// 验证转换后的数据是否是有效的 UTF-8
		if utf8.Valid(utf8Data) {
			return utf8Data, nil
		}
	}
	decoder = simplifiedchinese.GBK.NewDecoder()
	reader = transform.NewReader(bytes.NewReader(data), decoder)
	utf8Data, err = io.ReadAll(reader)
	if err == nil && len(utf8Data) > 0 {
		if utf8.Valid(utf8Data) {
			return utf8Data, nil
		}
	}

	// 如果都失败，返回原始数据并说明情况
	return data, fmt.Errorf("failed to convert from GBK/GB2312 to UTF-8, data may be in another encoding")
}

type HeartbeatService struct {
	config map[string]interface{}
	engine *MultiAgentEngine
	tasks  []HeartbeatTask
	stopCh chan struct{}
	logger *log.Logger
}

func (h *HeartbeatService) UpdateConfig(config map[string]interface{}) {

	h.config = config
	h.reloadTasks()
	h.logger.Println("心跳服务配置已更新")
}

func (h *HeartbeatService) reloadTasks() {
	// 重新加载任务配置
	h.tasks = make([]HeartbeatTask, 0)
	h.loadTasks()
	h.logger.Printf("重新加载了 %d 个定时任务", len(h.tasks))
}

type HeartbeatTask struct {
	Name       string        `json:"name"`
	Cron       string        `json:"cron"`      // CRON表达式
	Interval   time.Duration `json:"interval"`  // 或固定间隔
	TaskType   string        `json:"task_type"` // chat, task, command
	AgentName  string        `json:"agent_name"`
	Message    string        `json:"message"`
	Enabled    bool          `json:"enabled"`
	LastRun    *time.Time    `json:"last_run"`
	LastResult interface{}   `json:"last_result"`
}

func NewHeartbeatService(config map[string]interface{}, engine *MultiAgentEngine) *HeartbeatService {
	return &HeartbeatService{
		config: config,

		engine: engine,
		tasks:  make([]HeartbeatTask, 0),
		stopCh: make(chan struct{}),
		logger: log.New(os.Stdout, "[Heartbeat] ", log.LstdFlags),
	}
}

// Start 启动心跳服务
func (h *HeartbeatService) Start() {
	h.logger.Println("心跳服务启动")

	// 从配置加载定时任务
	h.loadTasks()

	for _, task := range h.tasks {
		if task.Enabled {
			go h.runTask(task)
		}
	}
}

// Stop 停止心跳服务
func (h *HeartbeatService) Stop() {
	close(h.stopCh)
	h.logger.Println("心跳服务停止")
}

func (h *HeartbeatService) loadTasks() {
	// 从配置加载定时任务
	// 这里简单添加几个示例任务
	h.tasks = append(h.tasks, HeartbeatTask{
		Name:     "daily_summary",
		Interval: 24 * time.Hour,
		TaskType: "chat",
		Message:  "请总结过去24小时的重要事件",
		Enabled:  true,
	})
}

func (h *HeartbeatService) runTask(task HeartbeatTask) {
	ticker := time.NewTicker(task.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-h.stopCh:
			return
		case t := <-ticker.C:
			h.logger.Printf("执行定时任务: %s at %s", task.Name, t.Format("2006-01-02 15:04:05"))

			// 执行任务
			result, err := h.executeTask(task)

			// 记录结果
			now := time.Now()
			task.LastRun = &now
			task.LastResult = result

			// 发送消息到总线
			if err != nil {
				log.Printf("err:%v", err)
			} else {
				log.Printf("err:%v", err)
			}
		}
	}
}

func heartbeatTaskContext(task HeartbeatTask) context.Context {
	sessionID := "heartbeat"
	if name := strings.TrimSpace(task.Name); name != "" {
		sessionID = "heartbeat_" + strings.ReplaceAll(name, " ", "_")
	}
	taskID := fmt.Sprintf("%s-t%d", sessionID, time.Now().UnixNano())
	ctx := context.WithValue(context.Background(), "task_id", taskID)
	ctx = context.WithValue(ctx, "session_id", sessionID)
	ctx = context.WithValue(ctx, "channel", "heartbeat")
	ctx = context.WithValue(ctx, "user_id", "heartbeat")
	return ctx
}

func (h *HeartbeatService) executeTask(task HeartbeatTask) (interface{}, error) {
	if h.engine == nil {
		return nil, fmt.Errorf("engine not available")
	}
	ctx := heartbeatTaskContext(task)
	switch task.TaskType {
	case "chat":
		agent, exists := h.engine.getAgent(task.AgentName)
		if exists != nil {
			return nil, fmt.Errorf("agent %s not found", task.AgentName)
		}
		return agent.Generate(ctx, task.Message)
	case "task":
		return h.engine.Run(ctx)
	default:
		return nil, fmt.Errorf("unknown task type: %s", task.TaskType)
	}
}

// SaveConfig 保存配置
func YamlSaveConfig(config *Config, configPath string) error {
	persistedConfig, err := configForPersistence(config)
	if err != nil {
		return fmt.Errorf("模型 API Key 环境变量配置无效: %w", err)
	}

	// 保存配置历史
	configJSON, err := json.Marshal(persistedConfig)
	if err != nil {
		return err
	}
	var lastVersion ConfigHistory
	db.Order("version desc").First(&lastVersion)

	history := ConfigHistory{
		Version:   lastVersion.Version + 1,
		Config:    string(configJSON),
		CreatedAt: time.Now(),
	}
	db.Create(&history)

	// 写入文件
	data, err := json.MarshalIndent(persistedConfig, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(configPath, data, 0644)
}

// 在 GatewayService 中添加获取所有Agent名称的方法
func (g *GatewayService) handleGetAllAgentsNames(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	agentNames := g.engine.GetAgentNames()

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"agents": agentNames,
	})
}

// submitVoiceTask binds the reusable voice listener to the application task runtime.
func (g *GatewayService) submitVoiceTask(text string) {
	if g == nil || g.engine == nil {
		return
	}
	agentName, agent, ok := g.engine.DefaultAgent()
	if !ok {
		return
	}

	g.logger.Printf("📝 提交语音任务到 %s: %s", agentName, text)
	taskID := fmt.Sprintf("voice_%s", time.Now().Format("20060102_150405"))
	task := &GatewayTask{
		ID: taskID,
		Request: &MessageRequest{
			TaskType:  "chat",
			Message:   fmt.Sprintf("[用户通过语音说]: %s\n\n请处理这个语音指令，并用 speak 工具朗读回复。", text),
			AgentName: agentName,
			Channel:   "voice",
			UserId:    "voice",
		},
		Status:    "pending",
		CreatedAt: time.Now(),
		ResultCh:  make(chan interface{}, 1),
	}

	g.mutex.Lock()
	g.tasks[taskID] = task
	g.mutex.Unlock()

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 180*time.Minute)
		defer cancel()
		ctx = context.WithValue(ctx, "task_id", taskID)
		ctx = context.WithValue(ctx, "session_id", "voice_"+taskID)
		ctx = context.WithValue(ctx, "channel", "voice")
		ctx = context.WithValue(ctx, "user_id", "voice")

		result, err := agent.Generate(ctx, task.Request.Message)
		task.Status = "completed"
		if err != nil {
			task.Status = "failed"
			task.Error = err
		}
		task.Result = result
		completedAt := time.Now()
		task.CompletedAt = &completedAt
		task.ResultCh <- result
		g.logger.Printf("✅ 语音任务完成: %s", taskID)
	}()
}

// handleWakeStart starts Siri-like voice wake service.
func (g *GatewayService) handleWakeStart(w http.ResponseWriter, r *http.Request) {
	if g.wakeService == nil {
		json.NewEncoder(w).Encode(map[string]interface{}{"ok": false, "error": "wake service not initialized"})
		return
	}
	g.wakeService.Start()
	json.NewEncoder(w).Encode(map[string]interface{}{"ok": true, "status": "listening"})
}

// handleWakeStop stops the voice wake service.
func (g *GatewayService) handleWakeStop(w http.ResponseWriter, r *http.Request) {
	if g.wakeService == nil {
		json.NewEncoder(w).Encode(map[string]interface{}{"ok": false})
		return
	}
	g.wakeService.Stop()
	json.NewEncoder(w).Encode(map[string]interface{}{"ok": true, "status": "stopped"})
}

// handleWakeStatus returns current wake service status.
func (g *GatewayService) handleWakeStatus(w http.ResponseWriter, r *http.Request) {
	if g.wakeService == nil {
		json.NewEncoder(w).Encode(map[string]interface{}{"listening": false})
		return
	}
	json.NewEncoder(w).Encode(map[string]interface{}{
		"listening": g.wakeService.IsListening(),
	})
}

// runBackgroundUpdateCheck periodically checks for updates (silent, no push).
func (g *GatewayService) runBackgroundUpdateCheck(ctx context.Context, interval time.Duration) {
	g.logger.Printf("后台更新检查已启动 (间隔: %v)", interval)
	initialDelay := time.NewTimer(30 * time.Second)
	defer initialDelay.Stop()
	select {
	case <-ctx.Done():
		return
	case <-initialDelay.C:
	}

	for {
		info, err := g.updateChecker.CheckForUpdate()
		g.updateInfoMu.Lock()
		if err != nil {
			g.logger.Printf("更新检查失败: %v", err)
		} else if info != nil {
			g.lastUpdateInfo = info
			g.logger.Printf("📦 新版本可用: %s → %s", CurrentVersion, info.Version)
		}
		g.updateInfoMu.Unlock()
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

// handleMonitorCache returns current cache metrics snapshot.
func (g *GatewayService) handleMonitorCachePrompt(w http.ResponseWriter, r *http.Request) {
	if !g.allowSensitiveDebugRequest(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	idStr := r.URL.Query().Get("id")
	id, err := strconv.ParseUint(idStr, 10, 64)
	if err != nil || idStr == "" || strconv.FormatUint(id, 10) != idStr {
		http.Error(w, "invalid prompt id", http.StatusBadRequest)
		return
	}
	filePath := filepath.Join(".openkitty", "prompt_dump", strconv.FormatUint(id, 10)+".json")
	data, err := os.ReadFile(filePath)
	if err != nil {
		http.Error(w, "prompt not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Disposition", "attachment; filename=prompt_"+idStr+".json")
	w.Write(data)
}

func (g *GatewayService) allowSensitiveDebugRequest(r *http.Request) bool {
	configuredKey := ""
	if g != nil && g.config != nil {
		configuredKey = strings.TrimSpace(g.config.Gateway.APIKey)
	}
	if configuredKey != "" {
		return r.Header.Get("X-API-Key") == configuredKey
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	if ip == nil || !ip.IsLoopback() {
		return false
	}
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		return true
	}
	parsed, err := url.Parse(origin)
	return err == nil && strings.EqualFold(parsed.Host, r.Host)
}

func (g *GatewayService) handleMonitorCache(w http.ResponseWriter, r *http.Request) {
	snapshot := globalCacheMetrics.Snapshot()
	health := globalstate.CacheBreakDetector().Health()

	// Build response with backward-compatible flat fields (web UI depends on these)
	resp := map[string]interface{}{
		"cache_hit_rate":          snapshot.CacheHitRate,
		"total_prompt_tokens":     snapshot.TotalPromptTokens,
		"total_completion_tokens": snapshot.TotalCompletionTokens,
		"total_cache_hit_tokens":  snapshot.TotalCacheHitTokens,
		"total_cache_miss_tokens": snapshot.TotalCacheMissTokens,
		"avg_latency_ms":          snapshot.AvgLatencyMs,
		"total_calls":             snapshot.TotalCalls,
		"recent":                  snapshot.Recent,
		// New: cache break detection (Claude Code style)
		"breaks": map[string]interface{}{
			"total":       health.TotalBreaks,
			"by_category": health.BreaksByCategory,
			"last_break":  health.LastBreak,
			"recent":      health.RecentEvents,
		},
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// handleMonitorCacheHistory returns recent cache records for charting.
func (g *GatewayService) handleMonitorCacheHistory(w http.ResponseWriter, r *http.Request) {
	nStr := r.URL.Query().Get("n")
	n := 50
	if nStr != "" {
		if parsed, err := strconv.Atoi(nStr); err == nil && parsed > 0 && parsed <= 500 {
			n = parsed
		}
	}
	history := globalCacheMetrics.History(n)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(history)
}

// handleCacheConfig returns/sets semaphore_limit and other cache-affecting runtime config.
func (g *GatewayService) handleCacheConfig(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method == "POST" {
		var body struct {
			SemaphoreLimit int `json:"semaphore_limit"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, `{"error":"invalid json"}`, 400)
			return
		}
		if body.SemaphoreLimit < 1 || body.SemaphoreLimit > 10 {
			http.Error(w, `{"error":"semaphore_limit must be 1-10"}`, 400)
			return
		}
		g.config.Runtime.SemaphoreLimit = body.SemaphoreLimit
		// Also update the engine's live config if available
		if g.engine != nil {
			g.engine.UpdateRuntimeSemaphore(body.SemaphoreLimit)
		}
		json.NewEncoder(w).Encode(map[string]interface{}{
			"ok": true, "semaphore_limit": body.SemaphoreLimit,
			"message": "已生效，新任务将使用此并发数",
		})
		return
	}
	limit := g.config.Runtime.SemaphoreLimit
	if limit <= 0 {
		limit = 4
	}
	agents := []map[string]interface{}{}
	if g.engine != nil {
		for _, a := range g.engine.GetAgents() {
			agents = append(agents, map[string]interface{}{
				"name": a.Name, "model": a.Model,
			})
		}
	}
	json.NewEncoder(w).Encode(map[string]interface{}{
		"semaphore_limit": limit,
		"agents":          agents,
		"note":            "并发越高吞吐越好，但多个对话前缀会竞争 DeepSeek 缓存槽位，降低缓存命中率。设为 1 可最大化缓存命中、节省 token。",
	})
}

// handleTaskTrace returns the full trace of a task (LLM calls + tool executions).
func (g *GatewayService) handleTaskTrace(w http.ResponseWriter, r *http.Request) {
	taskID := strings.TrimPrefix(r.URL.Path, "/api/trace/")
	if taskID == "" {
		http.Error(w, `{"error":"missing task_id"}`, 400)
		return
	}
	if g.isAgentOSTaskID(taskID) {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/json")

	// Collect trace data
	type traceStep struct {
		Time     string `json:"time"`
		Type     string `json:"type"` // "llm" or "tool"
		Detail   string `json:"detail"`
		Tokens   int    `json:"tokens,omitempty"`
		CacheHit int    `json:"cache_hit,omitempty"`
		Duration int64  `json:"duration_ms,omitempty"`
	}
	var steps []traceStep

	// 1. Cache records for this task
	for _, rec := range globalCacheMetrics.History(500) {
		if rec.TaskID == taskID {
			steps = append(steps, traceStep{
				Time:     rec.Time.Format("15:04:05"),
				Type:     "llm",
				Detail:   fmt.Sprintf("%s / %s", rec.Model, rec.AgentName),
				Tokens:   rec.PromptTokens,
				CacheHit: rec.CacheHitTokens,
				Duration: rec.LatencyMs,
			})
		}
	}

	// 2. DB: Gateway task record
	var taskRec GatewayTaskRecord
	if g.db != nil {
		if err := g.db.Where("task_id = ?", taskID).First(&taskRec).Error; err == nil {
			dur := ""
			if taskRec.CompletedAt != nil {
				dur = taskRec.CompletedAt.Sub(taskRec.CreatedAt).Round(time.Second).String()
			}
			steps = append(steps, traceStep{
				Time:   taskRec.CreatedAt.Format("15:04:05"),
				Type:   "task",
				Detail: fmt.Sprintf("创建: %s | Agent: %s | 状态: %s | 耗时: %s", taskRec.Message, taskRec.AgentName, taskRec.Status, dur),
			})
		}
	}

	// 3. DB: Memory events for this task
	if g.db != nil {
		var events []memorycore.MemoryEvent
		g.db.Where("session_id LIKE ? OR user_question LIKE ?",
			"%"+taskID+"%", "%"+taskID+"%").
			Order("created_at ASC").Find(&events)
		for _, ev := range events {
			steps = append(steps, traceStep{
				Time:   ev.CreatedAt.Format("15:04:05"),
				Type:   "task",
				Detail: fmt.Sprintf("Q: %s → Status: %s", truncate(ev.UserQuestion, 80), ev.Status),
			})
		}
	}

	// 4. Engine logs: parse cache data from logs/engine_*.log for this task
	logDir := filepath.Join(".", "logs")
	if entries, err := os.ReadDir(logDir); err == nil {
		for _, entry := range entries {
			if !strings.HasPrefix(entry.Name(), "engine_") || !strings.HasSuffix(entry.Name(), ".log") {
				continue
			}
			data, err := os.ReadFile(filepath.Join(logDir, entry.Name()))
			if err != nil {
				continue
			}
			for _, line := range strings.Split(string(data), "\n") {
				if !strings.Contains(line, taskID) || !strings.Contains(line, "prompt_cache_hit_tokens") {
					continue
				}
				// Parse cache line
				timeStr := ""
				if idx := strings.Index(line, "2026/"); idx >= 0 {
					timeStr = line[idx+11 : idx+19] // "HH:MM:SS"
				}
				tokens := 0
				cacheHit := 0
				model := "?"
				fmt.Sscanf(line, "%*s %*s %*s %*s %*s %*s %*s %*s %*s %*s \"model\":\"%s\"", &model)
				// Extract numbers
				if idx := strings.Index(line, "\"prompt_tokens\":"); idx >= 0 {
					fmt.Sscanf(line[idx:], "\"prompt_tokens\":%d", &tokens)
				}
				if idx := strings.Index(line, "\"prompt_cache_hit_tokens\":"); idx >= 0 {
					fmt.Sscanf(line[idx:], "\"prompt_cache_hit_tokens\":%d", &cacheHit)
				}
				if tokens > 0 {
					dur := int64(0)
					if idx := strings.Index(line, "\"latency_ms\":"); idx < 0 {
						// try to parse latency
					}
					steps = append(steps, traceStep{
						Time:     timeStr,
						Type:     "llm",
						Detail:   model,
						Tokens:   tokens,
						CacheHit: cacheHit,
						Duration: dur,
					})
				}
			}
		}
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"task_id": taskID,
		"steps":   steps,
	})
}

// handleCacheHealth returns cache break detection health summary.
func (g *GatewayService) handleCacheHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	health := globalstate.CacheBreakDetector().Health()
	json.NewEncoder(w).Encode(health)
}

// handleVoiceUpload accepts audio file uploads and returns transcribed text.
// POST /api/voice with multipart form: file=audio.wav
func (g *GatewayService) handleVoiceUpload(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"POST required"}`, http.StatusMethodNotAllowed)
		return
	}

	r.ParseMultipartForm(10 << 20) // 10MB
	file, header, err := r.FormFile("file")
	if err != nil {
		json.NewEncoder(w).Encode(map[string]interface{}{"ok": false, "error": "file required"})
		return
	}
	defer file.Close()

	// Save to temp
	tmpDir := filepath.Join(g.config.Workspace, ".openkitty", "voice")
	os.MkdirAll(tmpDir, 0755)
	tmpPath := filepath.Join(tmpDir, fmt.Sprintf("voice_%d_%s", time.Now().Unix(), header.Filename))
	dst, err := os.Create(tmpPath)
	if err != nil {
		json.NewEncoder(w).Encode(map[string]interface{}{"ok": false, "error": "save failed"})
		return
	}
	io.Copy(dst, file)
	dst.Close()

	// Transcribe using existing pipeline
	voiceHandler := channelcore.NewChannelVoiceHandler(g.config.Workspace, g.logger, processutil.ResolveFFmpegBinary)
	lang := r.FormValue("lang")
	if lang == "" {
		lang = "zh"
	}
	text := voiceHandler.TranscribeAudio(tmpPath, lang)

	g.logger.Printf("[Voice] Transcribed %s (%d bytes) → %d chars", header.Filename, header.Size, len(text))

	json.NewEncoder(w).Encode(map[string]interface{}{
		"ok":   true,
		"text": text,
	})
}

// handleUpdateCheck checks for available updates.
func (g *GatewayService) handleUpdateCheck(w http.ResponseWriter, r *http.Request) {
	// Return cached result if available (fast response for UI polling)
	g.updateInfoMu.RLock()
	cached := g.lastUpdateInfo
	g.updateInfoMu.RUnlock()

	if cached != nil {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"ok": true, "current": CurrentVersion, "update_available": true,
			"version": cached.Version, "changelog": cached.ChangeLog,
			"download_url": cached.DownloadURL, "size": cached.Size,
		})
		return
	}

	// Use cached result if available; otherwise trigger fresh check
	g.updateInfoMu.RLock()
	info := g.lastUpdateInfo
	g.updateInfoMu.RUnlock()

	if info == nil {
		var err error
		info, err = g.updateChecker.CheckForUpdate()
		if err != nil {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"ok": false, "error": err.Error(), "current": CurrentVersion,
			})
			return
		}
	}

	if info == nil {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"ok": true, "current": CurrentVersion, "update_available": false,
		})
		return
	}
	json.NewEncoder(w).Encode(map[string]interface{}{
		"ok": true, "current": CurrentVersion, "update_available": true,
		"version": info.Version, "changelog": info.ChangeLog,
		"download_url": info.DownloadURL, "size": info.Size,
	})
}

// handleUpdateApply downloads and applies the latest update automatically.
func (g *GatewayService) handleUpdateApply(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")

	if g.updateChecker == nil {
		json.NewEncoder(w).Encode(map[string]interface{}{"ok": false, "error": "更新检查器未初始化"})
		return
	}

	info, err := g.updateChecker.CheckForUpdate()
	if err != nil {
		json.NewEncoder(w).Encode(map[string]interface{}{"ok": false, "error": err.Error()})
		return
	}
	if info == nil {
		json.NewEncoder(w).Encode(map[string]interface{}{"ok": true, "updated": false, "message": "已是最新版本"})
		return
	}

	eventbus.PushSystemMessage(fmt.Sprintf("⬇ 正在下载 v%s ...", info.Version), "info")
	g.logger.Printf("[Update] Downloading v%s from %s", info.Version, info.DownloadURL)

	newBinary, err := g.updateChecker.DownloadUpdate(info, nil)
	if err != nil {
		msg := fmt.Sprintf("下载失败: %v", err)
		eventbus.PushSystemMessage(msg, "error")
		json.NewEncoder(w).Encode(map[string]interface{}{"ok": false, "error": msg})
		return
	}

	eventbus.PushSystemMessage(fmt.Sprintf("🔄 正在安装 v%s，即将重启...", info.Version), "warn")

	// Apply asynchronously — give HTTP response time to flush
	go func() {
		time.Sleep(500 * time.Millisecond)
		if err := g.updateChecker.ApplyUpdate(newBinary); err != nil {
			g.logger.Printf("[Update] Apply failed: %v", err)
			eventbus.PushSystemMessage(fmt.Sprintf("安装失败: %v", err), "error")
		}
	}()

	json.NewEncoder(w).Encode(map[string]interface{}{
		"ok": true, "updated": true, "version": info.Version,
		"message": "更新已下载，即将重启应用",
	})
}

// handleUpdateProgress returns download progress for the UI to poll.
func (g *GatewayService) handleUpdateProgress(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	taskID := r.URL.Query().Get("task")
	g.updateProgMu.Lock()
	prog, ok := g.updateProgress[taskID]
	g.updateProgMu.Unlock()
	if !ok {
		json.NewEncoder(w).Encode(map[string]interface{}{"ok": false, "error": "task not found"})
		return
	}
	json.NewEncoder(w).Encode(map[string]interface{}{
		"ok": true, "task_id": prog.TaskID,
		"total": prog.Total, "downloaded": prog.Downloaded,
		"percent": prog.Percent, "done": prog.Done, "error": prog.Error,
	})
}

// ====================== Pipeline Handlers ======================

func (g *GatewayService) handlePipelineRun(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Pipeline  string `json:"pipeline"`
		Input     string `json:"input"`
		SessionID string `json:"session_id,omitempty"`
		Mode      string `json:"mode,omitempty"`
		NotifyURL string `json:"notify_url,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request"}`, http.StatusBadRequest)
		return
	}
	if req.Pipeline == "" || req.Input == "" {
		http.Error(w, `{"error":"pipeline and input required"}`, http.StatusBadRequest)
		return
	}
	g.assessAndLogGatewayUserContent("pipeline", req.Input)
	if isBuiltInFinancialAIPipeline(req.Pipeline) {
		http.Error(w, `{"error":"professional role is internal; use @投资顾问"}`, http.StatusForbidden)
		return
	}
	if req.SessionID == "" {
		req.SessionID = fmt.Sprintf("pipe_sess_%d", time.Now().UnixNano())
	}

	mode := RunModeInteractive
	if req.Mode == "api" {
		mode = RunModeAPI
	}

	exec, err := g.pipelineExec.Run(context.WithValue(r.Context(), "channel", "web"), req.Pipeline, req.Input, req.SessionID, mode, req.NotifyURL)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":   "started",
		"exec_id":  exec.ID,
		"pipeline": exec.PipelineName,
		"task_id":  exec.TaskID,
	})
}

func (g *GatewayService) handlePipelineApprove(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		ExecID   string `json:"exec_id"`
		Feedback string `json:"feedback"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request"}`, http.StatusBadRequest)
		return
	}
	if req.ExecID == "" {
		http.Error(w, `{"error":"exec_id required"}`, http.StatusBadRequest)
		return
	}
	if req.Feedback == "" {
		req.Feedback = "ok"
	}

	if err := g.pipelineExec.ApproveStep(req.ExecID, req.Feedback); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  "approved",
		"exec_id": req.ExecID,
	})
}

func (g *GatewayService) handlePipelineStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	execID := strings.TrimPrefix(r.URL.Path, "/api/pipeline/status/")
	if execID == "" {
		execID = r.URL.Query().Get("exec_id")
	}
	if execID == "" {
		http.Error(w, `{"error":"exec_id required"}`, http.StatusBadRequest)
		return
	}

	exec, ok := g.pipelineExec.GetExecution(execID)
	if !ok {
		// Fall back to DB for completed/cleaned-up executions
		if g.db != nil {
			var rec PipelineExecutionRecord
			if err := g.db.Where("exec_id = ?", execID).First(&rec).Error; err == nil && rec.StateJSON != "" {
				var saved PipelineExecution
				if json.Unmarshal([]byte(rec.StateJSON), &saved) == nil {
					view := NewPipelineExecutionView(&saved)
					g.enrichPipelineExecutionViewWithAE(view)
					w.Header().Set("Content-Type", "application/json; charset=utf-8")
					json.NewEncoder(w).Encode(view)
					return
				}
			}
		}
		http.Error(w, `{"error":"execution not found"}`, http.StatusNotFound)
		return
	}

	view := NewPipelineExecutionView(exec)
	g.enrichPipelineExecutionViewWithAE(view)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(view)
}

func (g *GatewayService) handlePipelineLogDownload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	execID := strings.TrimSpace(r.URL.Query().Get("id"))
	if execID == "" {
		execID = strings.TrimSpace(r.URL.Query().Get("exec_id"))
	}
	if execID == "" {
		http.Error(w, "exec id required", http.StatusBadRequest)
		return
	}
	if g == nil || g.pipelineExec == nil {
		http.Error(w, "pipeline executor not available", http.StatusServiceUnavailable)
		return
	}
	exec, ok := g.pipelineExec.GetExecutionByID(execID)
	if !ok || exec == nil {
		http.Error(w, "execution not found", http.StatusNotFound)
		return
	}
	content := formatPipelineExecutionLog(exec)
	filename := fmt.Sprintf("pipeline_%s.log", processutil.SanitizeShellLogName(execID))
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte(content))
}

func formatPipelineExecutionLog(exec *PipelineExecution) string {
	if exec == nil {
		return ""
	}
	exec.mu.Lock()
	steps := append([]StepResult(nil), exec.Steps...)
	for index := range steps {
		steps[index].ToolCalls = append([]ToolCallRecord(nil), exec.Steps[index].ToolCalls...)
		steps[index].Revisions = append([]PipelineStepRevision(nil), exec.Steps[index].Revisions...)
		for revisionIndex := range steps[index].Revisions {
			steps[index].Revisions[revisionIndex].ToolCalls = append(
				[]ToolCallRecord(nil),
				exec.Steps[index].Revisions[revisionIndex].ToolCalls...,
			)
		}
	}
	audit := append([]ApprovalRecord(nil), exec.AuditLog...)
	execID := exec.ID
	pipelineName := exec.PipelineName
	status := exec.Status
	taskID := exec.TaskID
	sessionID := exec.SessionID
	createdAt := exec.CreatedAt
	input := exec.Input
	exec.mu.Unlock()

	var b strings.Builder
	b.WriteString("MACS Pipeline Execution Log\n")
	b.WriteString("================================\n\n")
	b.WriteString(fmt.Sprintf("Execution ID: %s\n", execID))
	b.WriteString(fmt.Sprintf("Pipeline: %s\n", pipelineName))
	b.WriteString(fmt.Sprintf("Status: %s\n", status))
	b.WriteString(fmt.Sprintf("Task ID: %s\n", taskID))
	b.WriteString(fmt.Sprintf("Session ID: %s\n", sessionID))
	if !createdAt.IsZero() {
		b.WriteString(fmt.Sprintf("Created At: %s\n", createdAt.Format(time.RFC3339)))
	}
	if strings.TrimSpace(input) != "" {
		b.WriteString("\nInput\n-----\n")
		b.WriteString(input)
		b.WriteString("\n")
	}
	b.WriteString("\nSteps\n-----\n")
	for i, step := range steps {
		b.WriteString(fmt.Sprintf("\n[%d] %s\n", i+1, step.Name))
		b.WriteString(fmt.Sprintf("Status: %s\n", step.Status))
		if step.Revision > 0 {
			b.WriteString(fmt.Sprintf("Attempt: %d\n", step.Revision))
		}
		if step.DurationMs > 0 {
			b.WriteString(fmt.Sprintf("Duration: %dms\n", step.DurationMs))
		}
		if strings.TrimSpace(step.Error) != "" {
			b.WriteString("Error:\n")
			b.WriteString(step.Error)
			b.WriteString("\n")
		}
		if strings.TrimSpace(step.Output) != "" {
			b.WriteString("Output:\n")
			b.WriteString(step.Output)
			b.WriteString("\n")
		}
		if len(step.ToolCalls) > 0 {
			b.WriteString("Tool Calls:\n")
			writePipelineToolCallsLog(&b, step.ToolCalls, step.Revision, "")
		}
		if len(step.Revisions) > 0 {
			b.WriteString("Archived Attempts:\n")
			for _, revision := range step.Revisions {
				b.WriteString(fmt.Sprintf("- Attempt %d status=%s duration=%dms archived_at=%s\n", revision.Revision, revision.Status, revision.DurationMs, revision.ArchivedAt.Format(time.RFC3339)))
				if strings.TrimSpace(revision.Error) != "" {
					b.WriteString(fmt.Sprintf("  error: %s\n", revision.Error))
				}
				if strings.TrimSpace(revision.Output) != "" {
					b.WriteString("  output:\n")
					b.WriteString(revision.Output)
					b.WriteString("\n")
				}
				if len(revision.ToolCalls) > 0 {
					b.WriteString("  tool calls:\n")
					writePipelineToolCallsLog(&b, revision.ToolCalls, revision.Revision, "  ")
				}
			}
		}
	}
	if len(audit) > 0 {
		b.WriteString("\nAudit\n-----\n")
		for _, item := range audit {
			b.WriteString(fmt.Sprintf("- %s %s %s %s\n", item.Timestamp.Format(time.RFC3339), item.Role, item.Action, item.Feedback))
		}
	}
	if final, _ := pipelineFinalOutputFromSteps(steps); strings.TrimSpace(final) != "" {
		b.WriteString("\nFinal Output\n------------\n")
		b.WriteString(final)
		b.WriteString("\n")
	}
	return b.String()
}

func writePipelineToolCallsLog(b *strings.Builder, calls []ToolCallRecord, fallbackAttempt int, indent string) {
	if b == nil {
		return
	}
	for _, call := range calls {
		ts := ""
		if !call.Timestamp.IsZero() {
			ts = call.Timestamp.Format(time.RFC3339) + " "
		}
		cached := ""
		if call.Cached {
			cached = " cached=true"
		}
		callStatus := strings.ToLower(strings.TrimSpace(call.Status))
		if callStatus == "" {
			callStatus = "success"
			if pipelineToolCallRecordFailed(call) {
				callStatus = "error"
			}
		}
		correlation := ""
		if call.CallID != "" {
			correlation = " call_id=" + call.CallID
		}
		attempt := call.Attempt
		if attempt <= 0 {
			attempt = fallbackAttempt
		}
		b.WriteString(fmt.Sprintf("%s- %s%s duration=%dms%s status=%s attempt=%d%s\n", indent, ts, call.ToolName, call.DurationMs, cached, callStatus, attempt, correlation))
		if call.Params != "" {
			b.WriteString(fmt.Sprintf("%s  params: %s\n", indent, call.Params))
		}
		if call.Result != "" {
			b.WriteString(fmt.Sprintf("%s  result: %s\n", indent, call.Result))
		}
		if call.Error != "" {
			b.WriteString(fmt.Sprintf("%s  error: %s\n", indent, call.Error))
		}
	}
}

func (g *GatewayService) handlePipelineList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	pipelines := g.pipelineExec.ListPipelines()
	// 首次使用：提供内置案例模板
	if len(pipelines) == 0 {
		pipelines = getDefaultPipelineExamples()
	}
	if !internalAEDiagnosticsRequested(r) {
		visible := make([]Pipeline, 0, len(pipelines))
		for _, pipeline := range pipelines {
			if isBuiltInFinancialAIPipeline(pipeline.Name) {
				continue
			}
			visible = append(visible, pipeline)
		}
		pipelines = visible
	}
	// Never expose persisted credentials through the Pipeline list API. The UI
	// edits dsn_env references; plaintext DSNs remain write-only compatibility.
	for i := range pipelines {
		connections := append([]PipelineDatabaseConnection(nil), pipelines[i].DatabaseConnections...)
		for j := range connections {
			connections[j].DSN = ""
		}
		pipelines[i].DatabaseConnections = connections
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"pipelines": pipelines,
	})
}

// getDefaultPipelineExamples 内置企业级Pipeline案例模板
func getDefaultPipelineExamples() []Pipeline {
	return []Pipeline{
		{
			Name: "日报自动生成",
			Desc: "每早8:30自动抓取财经/科技/微博热点 → AI分析 → 生成Markdown日报 → 邮件发送",
			Steps: []PipelineStep{
				{Name: "信息采集", Prompt: "搜索今天的财经、科技、微博热点新闻，每个类目取前5条，整理成结构化数据", Tools: []string{"web_fetch", "web_search"}},
				{Name: "分析撰写", Prompt: "基于采集的信息，写一份简洁的日报，包含：今日头条(3条)、行业动态(5条)、值得关注(3条)。Markdown格式。", RequireApproval: true},
				{Name: "邮件发送", Prompt: "将日报通过邮件发送给团队成员", Tools: []string{"email"}},
			},
		},
		{
			Name: "竞品监控周报",
			Desc: "每周一自动扫描主要竞品动态 → 五维分析 → 生成策略建议 → 飞书通知",
			Steps: []PipelineStep{
				{Name: "竞品扫描", Prompt: "扫描主要竞品本周动态：产品更新、融资消息、市场活动、人才变动。", Tools: []string{"web_fetch", "web_search"}},
				{Name: "深度分析", Prompt: "对竞品进行五维分析(产品/技术/市场/组织/财务)，标注优势、劣势和威胁级别", Tools: []string{"sub_agent"}},
				{Name: "策略建议", Prompt: "基于分析结果生成本周策略建议：攻防策略、优先级排序、资源调配建议。", RequireApproval: true},
			},
		},
		{
			Name: "客户邮件分拣",
			Desc: "自动检查收件箱 → AI分拣(咨询/投诉/合作/垃圾) → 重要邮件自动回复或转发",
			Steps: []PipelineStep{
				{Name: "收件检查", Prompt: "检查收件箱最新20封未读邮件，提取发件人、主题、正文摘要", Tools: []string{"email"}},
				{Name: "智能分拣", Prompt: "将邮件分为4类：咨询(产品/价格)、投诉(质量问题)、合作(商务/B2B)、垃圾。标注优先级。", RequireApproval: true},
				{Name: "自动回复", Prompt: "对咨询类邮件自动回复产品信息和联系方式，对投诉类转发客服，合作类标记待人工处理", Tools: []string{"email"}},
			},
		},
		{
			Name: "财报分析报告",
			Desc: "输入公司名 → 抓取最新财报 → AI分析盈利能力/成长性/风险 → 生成PDF报告",
			Steps: []PipelineStep{
				{Name: "数据采集", Prompt: "搜索公司最新季报/年报数据：营收、利润、现金流、资产负债。标注数据来源。", Tools: []string{"web_fetch", "web_search"}},
				{Name: "财务分析", Prompt: "分析：1)盈利能力(毛利率/净利率趋势) 2)成长性(营收/利润增速) 3)风险(负债率/现金流) 4)估值(PE/PB/PS)", RequireApproval: true},
				{Name: "报告生成", Prompt: "生成结构化PDF报告，包含：封面、目录、核心指标、深度分析、风险提示、投资建议。", Tools: []string{"write_file"}},
			},
		},
		{
			Name: "合同审查工坊",
			Desc: "上传合同 → 结构扫描 → 条款风险分析(红黄绿) → 合规审核 → 输出修改建议",
			Steps: []PipelineStep{
				{Name: "结构扫描", Prompt: "识别合同类型、主体、管辖法律、关键条款位置。输出结构化概览。", Tools: []string{"read_file"}},
				{Name: "条款分析", Prompt: "逐条分析风险点：无限责任、单方解约权、模糊表述、赔偿上限。红黄绿标注。", RequireApproval: true},
				{Name: "合规审核", Prompt: "综合条款分析 → 风险矩阵 → 权利义务清单 → 修改建议(具体条款修改措辞)", Tools: []string{"sub_agent"}},
			},
		},
	}
}

// handlePipelineSave creates or deletes a pipeline config at runtime.
func (g *GatewayService) handlePipelineSave(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if r.Method == "DELETE" {
		var body struct {
			Name string `json:"name"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		if body.Name == "" {
			http.Error(w, `{"error":"name required"}`, 400)
			return
		}
		if isBuiltInFinancialAIPipeline(body.Name) {
			http.Error(w, `{"error":"isolated professional pipeline is managed by the host"}`, http.StatusConflict)
			return
		}
		delete(g.config.Pipeline.Pipelines, body.Name)
		json.NewEncoder(w).Encode(map[string]interface{}{"ok": true, "deleted": body.Name})
		return
	}
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		Name                string                       `json:"name"`
		Description         string                       `json:"description"`
		DatabaseConnections []PipelineDatabaseConnection `json:"database_connections"`
		Steps               []PipelineStep               `json:"steps"`
		Schedule            string                       `json:"schedule"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, `{"error":"invalid json"}`, 400)
		return
	}
	if body.Name == "" || len(body.Steps) == 0 {
		http.Error(w, `{"error":"name and at least one step required"}`, 400)
		return
	}
	if isBuiltInFinancialAIPipeline(body.Name) {
		http.Error(w, `{"error":"isolated professional pipeline is managed by the host"}`, http.StatusConflict)
		return
	}
	if isRetiredBuiltinPipeline(body.Name) {
		g.deletePipelineFromDB(body.Name)
		delete(g.config.Pipeline.Pipelines, body.Name)
		http.Error(w, `{"error":"pipeline retired"}`, 410)
		return
	}
	pipeline := Pipeline{Name: body.Name, Desc: body.Description, DatabaseConnections: body.DatabaseConnections, Steps: body.Steps}
	if err := validatePipelineDatabaseConnections(pipeline); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"invalid database configuration: %s"}`, strings.ReplaceAll(err.Error(), `"`, `'`)), http.StatusBadRequest)
		return
	}
	if g.config.Pipeline.Pipelines == nil {
		g.config.Pipeline.Pipelines = make(map[string]Pipeline)
	}
	g.config.Pipeline.Pipelines[body.Name] = pipeline
	if err := g.savePipelineToDB(body.Name, pipeline); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"save failed: %s"}`, strings.ReplaceAll(err.Error(), `"`, `'`)), http.StatusInternalServerError)
		return
	}

	// 定时调度：自动创建 TODO
	if body.Schedule != "" && g.todoManager != nil {
		todoID := fmt.Sprintf("pipe_%s_%d", body.Name, time.Now().Unix())
		todo := &todocore.Todo{
			TodoID:       todoID,
			Title:        "Pipeline: " + body.Name,
			Description:  body.Description,
			PipelineName: body.Name,
			ScheduleType: "cron",
			CronExpr:     body.Schedule,
			Priority:     "normal",
			Status:       "pending",
		}
		if _, err := g.todoManager.CreateTodo(todo); err == nil {
			log.Printf("[PIPE] Pipeline '%s' 定时调度已创建: %s", body.Name, body.Schedule)
		}
	}

	// 写回 config
	raw, _ := json.MarshalIndent(g.config, "", "  ")
	os.WriteFile("config.json", raw, 0644)
	log.Printf("[PIPE] Pipeline '%s' saved (%d steps)", body.Name, len(body.Steps))
	json.NewEncoder(w).Encode(map[string]interface{}{"ok": true, "name": body.Name, "steps": len(body.Steps), "schedule": body.Schedule})
}

func (g *GatewayService) handlePipelineExecutions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	execs := g.pipelineExec.ListExecutions(100)
	views := make([]*PipelineExecutionView, 0, len(execs))
	for _, exec := range execs {
		view := NewPipelineExecutionView(exec)
		g.enrichPipelineExecutionViewWithAE(view)
		views = append(views, view)
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"executions": views,
	})
}

func (g *GatewayService) enrichPipelineExecutionViewWithAE(view *PipelineExecutionView) {
	if g == nil || g.aeManager == nil || view == nil || view.PipelineExecution == nil {
		return
	}
	pipelineName := strings.TrimSpace(view.PipelineName)
	if pipelineName == "" {
		return
	}
	g.aeManager.mu.RLock()
	defer g.aeManager.mu.RUnlock()
	for id, cfg := range g.aeManager.employees {
		if cfg == nil {
			continue
		}
		if strings.TrimSpace(cfg.Pipeline) == pipelineName || strings.TrimSpace(aeDisplayPipeline(cfg)) == pipelineName ||
			(id == "data-analyst" && pipelineName == dataAnalysisPresentationPipelineName) {
			view.EmployeeID = id
			view.EmployeeName = cfg.Name
			view.EmployeeNote = cfg.Note
			return
		}
	}
}

// handleSessionSearch searches chat messages via FTS5.

// handleAEWorkLog returns work records for digital employees.
func (g *GatewayService) handleAEWorkLog(w http.ResponseWriter, r *http.Request) {
	if g.aeManager == nil {
		http.Error(w, `{"employees":[]}`, http.StatusOK)
		return
	}
	employees := g.aeManager.ListEmployees()
	includeHidden := truthyQuery(r, "include_hidden")
	includeInternal := internalAEDiagnosticsRequested(r)
	type workSummary struct {
		EmployeeID    string          `json:"employee_id"`
		Name          string          `json:"name"`
		AtName        string          `json:"at_name"`
		Note          string          `json:"note,omitempty"`
		Role          string          `json:"role"`
		Supervisor    string          `json:"supervisor"`
		Channel       string          `json:"channel"`
		Pipeline      string          `json:"pipeline"`
		TotalTasks    int             `json:"total_tasks"`
		Running       int             `json:"running"`
		Completed     int             `json:"completed"`
		Degraded      int             `json:"degraded"`
		Failed        int             `json:"failed"`
		LastActive    string          `json:"last_active"`
		LatestSuccess *AEWorkRecord   `json:"latest_success,omitempty"`
		RecentWork    []*AEWorkRecord `json:"recent_work"`
		Hidden        bool            `json:"hidden,omitempty"`
		InternalOnly  bool            `json:"internal_only,omitempty"`
	}
	var summaries []workSummary
	for _, emp := range employees {
		if emp.InternalOnly && !includeInternal {
			continue
		}
		if emp.Hidden && !includeHidden {
			continue
		}
		ws := workSummary{EmployeeID: emp.ID, Name: emp.Name, AtName: emp.AtName, Note: emp.Note, Role: emp.Role, Supervisor: emp.Supervisor, Channel: emp.Channel, Pipeline: emp.Pipeline, Hidden: emp.Hidden, InternalOnly: emp.InternalOnly}
		allRecs := currentAEWorkRecords(*emp, g.aeManager.GetWorkLog(emp.ID))
		// Always return the newest records first. Startup hydration loads records
		// newest-first while live runs append to the same slice, so the in-memory
		// order is intentionally not relied upon here.
		ws.RecentWork = newestAEWorkRecords(allRecs, 5)
		var lastActive time.Time
		for _, rec := range allRecs {
			if rec == nil {
				continue
			}
			ws.TotalTasks++
			switch rec.Status {
			case "running":
				ws.Running++
			case "completed":
				ws.Completed++
				if ws.LatestSuccess == nil || aeWorkRecordActiveAt(rec).After(aeWorkRecordActiveAt(ws.LatestSuccess)) {
					ws.LatestSuccess = rec
				}
			case "degraded", "unavailable":
				ws.Degraded++
			case "failed":
				ws.Failed++
			}
			activeAt := rec.UpdatedAt
			if activeAt.IsZero() || rec.CreatedAt.After(activeAt) {
				activeAt = rec.CreatedAt
			}
			if activeAt.After(lastActive) {
				lastActive = activeAt
			}
		}
		if !lastActive.IsZero() {
			ws.LastActive = lastActive.Format("2006-01-02 15:04")
		}
		summaries = append(summaries, ws)
	}
	order := deCanonicalOrder()
	sort.SliceStable(summaries, func(i, j int) bool {
		left, leftOK := order[summaries[i].EmployeeID]
		right, rightOK := order[summaries[j].EmployeeID]
		if leftOK != rightOK {
			return leftOK
		}
		if leftOK && left != right {
			return left < right
		}
		return summaries[i].EmployeeID < summaries[j].EmployeeID
	})
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(map[string]interface{}{"employees": summaries})
}

func currentAEWorkRecords(employee AIEmployeeConfig, records []*AEWorkRecord) []*AEWorkRecord {
	if !isBuiltInFinancialAIEmployee(employee.ID) {
		return records
	}
	pipeline := strings.TrimSpace(employee.Pipeline)
	filtered := make([]*AEWorkRecord, 0, len(records))
	for _, record := range records {
		if record != nil && strings.TrimSpace(record.Pipeline) == pipeline {
			filtered = append(filtered, record)
		}
	}
	return filtered
}

func newestAEWorkRecords(records []*AEWorkRecord, limit int) []*AEWorkRecord {
	ordered := append([]*AEWorkRecord(nil), records...)
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i] == nil {
			return false
		}
		if ordered[j] == nil {
			return true
		}
		left := aeWorkRecordActiveAt(ordered[i])
		right := aeWorkRecordActiveAt(ordered[j])
		return left.After(right)
	})
	if limit > 0 && len(ordered) > limit {
		ordered = ordered[:limit]
	}
	return ordered
}

func aeWorkRecordActiveAt(record *AEWorkRecord) time.Time {
	if record == nil {
		return time.Time{}
	}
	activeAt := record.UpdatedAt
	if activeAt.IsZero() || record.CreatedAt.After(activeAt) {
		activeAt = record.CreatedAt
	}
	return activeAt
}

// handleAESave creates or updates a digital employee via web UI.
func (g *GatewayService) handleAESave(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, 405)
		return
	}
	cfg, _, err := decodeAIEmployeeConfigPayload(r.Body)
	if err != nil {
		http.Error(w, `{"error":"invalid json"}`, 400)
		return
	}
	if cfg.ID == "" || cfg.Name == "" {
		http.Error(w, `{"error":"id and name required"}`, 400)
		return
	}
	if err := g.saveAEToDB(&cfg); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"save failed: %v"}`, err), 500)
		return
	}
	if g.aeManager != nil {
		g.aeManager.Register([]AIEmployeeConfig{cfg})
	}
	json.NewEncoder(w).Encode(map[string]interface{}{"ok": true, "id": cfg.ID})
}

// handleAEDelete removes a digital employee.
func (g *GatewayService) handleAEDelete(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if r.Method != http.MethodPost && r.Method != http.MethodDelete {
		http.Error(w, `{"error":"method not allowed"}`, 405)
		return
	}
	id := r.URL.Query().Get("id")
	if id == "" {
		http.Error(w, `{"error":"id required"}`, 400)
		return
	}
	if isPolicyManagedBuiltInAIEmployee(id) {
		http.Error(w, `{"error":"built-in professional employee cannot be deleted"}`, http.StatusConflict)
		return
	}
	if err := g.deleteDEFromDB(id); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"delete failed: %v"}`, err), 500)
		return
	}
	if g.aeManager != nil {
		g.aeManager.Remove(id)
	}
	json.NewEncoder(w).Encode(map[string]interface{}{"ok": true, "deleted": id})
}

// ====================== Handoff API Handlers ======================

func (g *GatewayService) handleHandoffList(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if g.aeManager == nil {
		json.NewEncoder(w).Encode(map[string]interface{}{"ok": true, "handoffs": []interface{}{}})
		return
	}
	records := customerservice.GetCSHandoffManager().GetActiveHandoffs()
	if records == nil {
		records = []customerservice.HandoffRecord{}
	}
	json.NewEncoder(w).Encode(map[string]interface{}{"ok": true, "handoffs": records})
}

func (g *GatewayService) handleHandoffClaim(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"POST only"}`, 405)
		return
	}
	var body struct {
		TicketID string `json:"ticket_id"`
		Operator string `json:"operator"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, `{"error":"invalid json"}`, 400)
		return
	}
	if body.TicketID == "" || body.Operator == "" {
		http.Error(w, `{"error":"ticket_id and operator required"}`, 400)
		return
	}
	record, err := customerservice.GetCSHandoffManager().ClaimHandoff(body.TicketID, body.Operator)
	if err != nil {
		json.NewEncoder(w).Encode(map[string]interface{}{"ok": false, "error": err.Error()})
		return
	}
	json.NewEncoder(w).Encode(map[string]interface{}{"ok": true, "handoff": record})
}

func (g *GatewayService) handleHandoffComplete(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"POST only"}`, 405)
		return
	}
	var body struct {
		TicketID     string `json:"ticket_id"`
		OperatorNote string `json:"operator_note"`
		AISummary    string `json:"ai_summary"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, `{"error":"invalid json"}`, 400)
		return
	}
	if body.TicketID == "" {
		http.Error(w, `{"error":"ticket_id required"}`, 400)
		return
	}
	record, err := customerservice.GetCSHandoffManager().CompleteHandoff(body.TicketID, body.OperatorNote, body.AISummary)
	if err != nil {
		json.NewEncoder(w).Encode(map[string]interface{}{"ok": false, "error": err.Error()})
		return
	}
	json.NewEncoder(w).Encode(map[string]interface{}{"ok": true, "handoff": record})
}

func (g *GatewayService) handleHandoffClose(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"POST only"}`, 405)
		return
	}
	var body struct {
		TicketID string `json:"ticket_id"`
		Reason   string `json:"reason"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, `{"error":"invalid json"}`, 400)
		return
	}
	if body.TicketID == "" {
		http.Error(w, `{"error":"ticket_id required"}`, 400)
		return
	}
	record, err := customerservice.GetCSHandoffManager().CloseHandoff(body.TicketID, body.Reason)
	if err != nil {
		json.NewEncoder(w).Encode(map[string]interface{}{"ok": false, "error": err.Error()})
		return
	}
	json.NewEncoder(w).Encode(map[string]interface{}{"ok": true, "handoff": record})
}

// handlePipelineEvolve returns evolution suggestions for pipelines.
func (g *GatewayService) handlePipelineEvolve(w http.ResponseWriter, r *http.Request) {
	pipeline := r.URL.Query().Get("pipeline")
	if pipeline == "" {
		all := g.evolver.AllMetrics()
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		json.NewEncoder(w).Encode(all)
		return
	}
	suggestions := g.evolver.GetSuggestions(pipeline)
	metrics := g.evolver.GetMetrics(pipeline)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"pipeline":    pipeline,
		"suggestions": suggestions,
		"metrics":     metrics,
	})
}

func (g *GatewayService) handleSessionSearch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	q := r.URL.Query().Get("q")
	if q == "" {
		http.Error(w, `{"sessions":[]}`, http.StatusOK)
		return
	}
	limit := 20
	var results []struct {
		SessionID     string `json:"session_id"`
		Snippet       string `json:"snippet"`
		FirstQuestion string `json:"first_question"`
		UpdatedAt     string `json:"updated_at"`
	}
	g.db.Raw(`
		SELECT
			cm.session_id,
			SUBSTR(cm.content, 1, 200) AS snippet,
			COALESCE((
				SELECT SUBSTR(first_user.content, 1, 240)
				FROM chat_messages first_user
				WHERE first_user.session_id = cm.session_id
					AND first_user.role = ?
					AND first_user.tenant_id = ?
					AND (first_user.subject_id IS NULL OR first_user.subject_id = '')
					AND (first_user.media_owner = '' OR first_user.media_owner IS NULL OR first_user.media_owner = ?)
				ORDER BY first_user.created_at ASC, first_user.id ASC
				LIMIT 1
			), '') AS first_question,
			MAX(cm.created_at) AS updated_at
		FROM chat_messages cm
		INNER JOIN chat_messages_fts fts ON cm.id = fts.rowid
		WHERE chat_messages_fts MATCH ?
			AND cm.tenant_id = ?
			AND (cm.subject_id IS NULL OR cm.subject_id = '')
			AND (cm.media_owner = '' OR cm.media_owner IS NULL OR cm.media_owner = ?)
			AND NOT EXISTS (
				SELECT 1
				FROM gateway_task_records gtr
				WHERE gtr.session_id = cm.session_id
					AND gtr.queue_name = ?
			)
		GROUP BY cm.session_id
		ORDER BY updated_at DESC
		LIMIT ?
	`, "user", defaultExecutionTenantID, g.mediaOwner(r), q, defaultExecutionTenantID, g.mediaOwner(r), agentOSQueueName, limit).Scan(&results)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(map[string]interface{}{"query": q, "sessions": results})
}

func decodePipelineControlRequest(w http.ResponseWriter, r *http.Request) (string, bool) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return "", false
	}
	var req struct {
		ExecID string `json:"exec_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request"}`, http.StatusBadRequest)
		return "", false
	}
	req.ExecID = strings.TrimSpace(req.ExecID)
	if req.ExecID == "" {
		http.Error(w, `{"error":"exec_id required"}`, http.StatusBadRequest)
		return "", false
	}
	return req.ExecID, true
}

func (g *GatewayService) handlePipelinePause(w http.ResponseWriter, r *http.Request) {
	execID, ok := decodePipelineControlRequest(w, r)
	if !ok {
		return
	}
	if g.pipelineExec == nil {
		http.Error(w, `{"error":"pipeline executor is unavailable"}`, http.StatusServiceUnavailable)
		return
	}
	if err := g.pipelineExec.PauseExecution(execID); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), http.StatusConflict)
		return
	}
	exec, _ := g.pipelineExec.GetExecutionByID(execID)
	status := "pausing"
	if exec != nil {
		exec.mu.Lock()
		status = exec.Status
		exec.mu.Unlock()
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"status": status, "exec_id": execID})
}

func (g *GatewayService) handlePipelineResume(w http.ResponseWriter, r *http.Request) {
	execID, ok := decodePipelineControlRequest(w, r)
	if !ok {
		return
	}
	if g.pipelineExec == nil {
		http.Error(w, `{"error":"pipeline executor is unavailable"}`, http.StatusServiceUnavailable)
		return
	}
	exec, err := g.pipelineExec.Resume(r.Context(), execID)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), http.StatusConflict)
		return
	}
	exec.mu.Lock()
	status := exec.Status
	exec.mu.Unlock()
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"status": status, "exec_id": execID})
}

func (g *GatewayService) handlePipelineStop(w http.ResponseWriter, r *http.Request) {
	execID, ok := decodePipelineControlRequest(w, r)
	if !ok {
		return
	}
	if g.pipelineExec == nil {
		http.Error(w, `{"error":"pipeline executor is unavailable"}`, http.StatusServiceUnavailable)
		return
	}

	if err := g.pipelineExec.StopExecution(execID); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  "stopped",
		"exec_id": execID,
	})
}

const emptyGatewayCompletionMessage = "任务已完成，但未生成可用最终回答。请查看任务日志或已生成的存储文件。"

func gatewayResultText(result interface{}) string {
	switch value := result.(type) {
	case nil:
		return ""
	case string:
		return value
	case []byte:
		return string(value)
	default:
		resultBytes, err := json.Marshal(result)
		if err == nil {
			if string(resultBytes) == "null" {
				return ""
			}
			return string(resultBytes)
		}
		return fmt.Sprintf("%v", result)
	}
}

func shouldPersistAssistantCompletion(taskType, taskID string) bool {
	if taskType == "ae" {
		return false
	}
	if strings.HasSuffix(taskID, "-de") {
		return false
	}
	return true
}

// ===== Admin API =====

func (g *GatewayService) handleAdminLogin(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	var body struct {
		Password string `json:"password"`
	}
	json.NewDecoder(r.Body).Decode(&body)
	adminPass := os.Getenv("ADMIN_PASSWORD")
	if adminPass == "" {
		adminPass = "openkitty"
	}
	if body.Password != adminPass {
		http.Error(w, `{"ok":false,"error":"wrong password"}`, 401)
		return
	}
	token := fmt.Sprintf("admin_%d", time.Now().UnixNano())
	validTokensMu.Lock()
	validTokens[token] = time.Now().Add(12 * time.Hour)
	validTokensMu.Unlock()
	json.NewEncoder(w).Encode(map[string]interface{}{"ok": true, "token": token})
}

var validTokens = map[string]time.Time{}
var validTokensMu sync.Mutex

func (g *GatewayService) handleAdminVerify(w http.ResponseWriter, r *http.Request) {
	token := r.Header.Get("X-Admin-Token")
	validTokensMu.Lock()
	exp, ok := validTokens[token]
	validTokensMu.Unlock()
	if !ok || time.Now().After(exp) {
		http.Error(w, `{"ok":false}`, 401)
		return
	}
	json.NewEncoder(w).Encode(map[string]interface{}{"ok": true})
}

func (g *GatewayService) handleTenantList(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	tenants := authcore.ListTenants()
	json.NewEncoder(w).Encode(map[string]interface{}{"tenants": tenants})
}

// Channel 通用渠道接口
type Channel = channelhost.Channel

func sendChannelContent(gateway *GatewayService, channel Channel, recipient, content string, logger *log.Logger) error {
	return channelcore.SendContent(channelArtifactWorkspace(gateway), channel, recipient, content, logger)
}

func channelArtifactWorkspace(gateway *GatewayService) string {
	workspace := ""
	if gateway != nil && gateway.config != nil {
		workspace = strings.TrimSpace(gateway.config.Workspace)
	}
	if workspace == "" {
		workspace, _ = os.Getwd()
	}
	absolute, err := filepath.Abs(workspace)
	if err != nil {
		return filepath.Clean(workspace)
	}
	return filepath.Clean(absolute)
}

type ApprovalCardSender interface {
	SendApprovalCard(recipient string, req *CodexApprovalRequest) error
}

func registerSessionInterrupt(sessionID string) chan string {
	ch := make(chan string, 5)
	if strings.TrimSpace(sessionID) != "" {
		interruptChannels.Store(sessionID, ch)
	}
	return ch
}

func unregisterSessionInterrupt(sessionID string, ch chan string) {
	if strings.TrimSpace(sessionID) == "" || ch == nil {
		return
	}
	if current, ok := interruptChannels.Load(sessionID); ok && current == ch {
		interruptChannels.Delete(sessionID)
	}
}

func tryInjectChannelInterrupt(g *GatewayService, sessionID, channelName, userID, message string) bool {
	if g == nil || strings.TrimSpace(sessionID) == "" {
		return false
	}
	chAny, ok := interruptChannels.Load(sessionID)
	if !ok {
		return false
	}
	ch, ok := chAny.(chan string)
	if !ok {
		return false
	}
	select {
	case ch <- message:
		if g.db != nil {
			g.saveChatMessage(sessionID, "user", "", message, "pending", "", channelName, userID)
		}
		return true
	default:
		return false
	}
}

// BaseChannel 渠道基类，提供通用功能
type BaseChannel struct {
	name        string
	channelType string
	gateway     *GatewayService
	logger      *log.Logger
	sessions    map[string]string // userID -> sessionID
	mu          sync.RWMutex
	stopCh      chan struct{}
}

func NewBaseChannel(name, channelType string) *BaseChannel {
	return &BaseChannel{
		name:        name,
		channelType: channelType,
		logger:      log.New(os.Stdout, fmt.Sprintf("[%s:%s] ", channelType, name), log.LstdFlags),
		sessions:    make(map[string]string),
		stopCh:      make(chan struct{}),
	}
}

func (c *BaseChannel) Name() string                       { return c.name }
func (c *BaseChannel) Type() string                       { return c.channelType }
func (c *BaseChannel) SetGateway(gateway *GatewayService) { c.gateway = gateway }

// GetOrCreateSession 获取或创建用户会话
func (c *BaseChannel) GetOrCreateSession(userID string) string {
	c.mu.RLock()
	sessionID, exists := c.sessions[userID]
	c.mu.RUnlock()

	if exists {
		return sessionID
	}

	// 创建新会话
	sessionID = fmt.Sprintf("%s_%s_%d", c.channelType, userID, time.Now().UnixNano())

	c.mu.Lock()
	c.sessions[userID] = sessionID
	c.mu.Unlock()

	return sessionID
}

// LoadHistory 加载历史消息
func (c *BaseChannel) LoadHistory(sessionID, userID, currentTaskID string) string {
	if c.gateway == nil || c.gateway.db == nil || sessionID == "" {
		return ""
	}

	var messages []ChatMessage
	query := c.gateway.db.Where("session_id = ? AND role IN ('user', 'assistant') AND task_id != ?",
		sessionID, currentTaskID)

	if userID != "" {
		query = query.Where("user_id = ? OR user_id = ''", userID)
	}

	query.Order("created_at desc").
		Limit(10).
		Find(&messages)

	// 反转顺序
	for i, j := 0, len(messages)-1; i < j; i, j = i+1, j-1 {
		messages[i], messages[j] = messages[j], messages[i]
	}

	if len(messages) == 0 {
		return ""
	}

	var builder strings.Builder
	builder.WriteString("以下是历史对话记录：\n\n")
	for _, msg := range messages {
		roleName := "用户"
		if msg.Role == "assistant" {
			roleName = "助手"
		}
		builder.WriteString(fmt.Sprintf("%s: %s\n", roleName, msg.Content))
	}
	builder.WriteString("\n当前问题:\n")

	return builder.String()
}

// ProcessUserMessage 处理用户消息的通用逻辑
func (c *BaseChannel) ProcessUserMessage(userID, recipient, messageID, message string, metadata map[string]interface{}) {
	c.logger.Printf("处理用户消息: user=%s, message=%s", userID, truncate(message, 50))

	// 获取或创建会话
	sessionID := c.GetOrCreateSession(userID)

	if c.gateway != nil && c.gateway.codexInteractive != nil &&
		shouldRouteToCodexInteractive(message, c.gateway.codexInteractive.Has(sessionID)) {
		taskID, reply, err := c.gateway.codexInteractive.StartOrSend(context.Background(), sessionID, c.name, userID, recipient, message, "")
		if err != nil {
			c.sendError(recipient, messageID, fmt.Sprintf("Codex interactive failed: %v", err))
			return
		}
		if taskID == "" {
			taskID = aeChatTaskID(sessionID + "-codex")
		}
		if c.gateway.db != nil {
			c.gateway.saveChatMessage(sessionID, "user", "", message, "completed", taskID, c.name, userID)
		}
		if strings.TrimSpace(reply) != "" {
			c.sendReply(recipient, messageID, reply, nil)
		}
		return
	}

	// Resolve a valid @Skill before the legacy interrupt shortcut. A bound
	// Skill is an independent durable task and must never be appended as plain
	// text to the model call that happens to be running for this session.
	reservedForEmployee := false
	if c.gateway != nil && c.gateway.aeManager != nil {
		cfg, _ := c.gateway.aeManager.MatchAtName(message)
		reservedForEmployee = cfg != nil
	}
	inlineSkillMention := false
	if c.gateway != nil && !reservedForEmployee {
		_, _, matched, err := c.gateway.resolveSkillMention(message, "")
		if err != nil {
			c.sendError(recipient, messageID, err.Error())
			return
		}
		inlineSkillMention = matched
	}

	// 如果该 session 已有运行中的任务，注入消息而非创建新任务
	if ch, ok := interruptChannels.Load(sessionID); ok {
		if inlineSkillMention {
			c.sendError(recipient, messageID, "当前任务仍在运行；@Skill 会启动独立任务，请等待当前任务结束后再发送")
			return
		}
		select {
		case ch.(chan string) <- message:
			c.logger.Printf("消息已注入到运行中任务: session=%s", sessionID)
			c.gateway.saveChatMessage(sessionID, "user", "", message, "pending", "", c.name, userID)
			// 回复确认（通过 gateway 的 channelManager）
			if c.gateway != nil && c.gateway.channelManager != nil {
				c.gateway.channelManager.SendToChannel(c.name, recipient, "收到，正在处理中，消息已追加...")
			}
			return
		default:
			c.logger.Printf("注入队列已满，创建新任务: session=%s", sessionID)
		}
	}

	// 构建请求
	if c.gateway != nil && c.gateway.aeManager != nil {
		if cfg, task := c.gateway.aeManager.MatchAtName(message); cfg != nil {
			c.logger.Printf("[AE] @mention matched in channel %s: %s -> task=%s", c.name, cfg.Name, task)
			record, err := c.gateway.aeManager.DispatchExplicitMentionFromChannel(context.Background(), cfg.ID, task, sessionID, c.name, recipient, userID)
			if err != nil {
				if c.gateway.channelManager != nil {
					c.gateway.channelManager.SendToChannel(c.name, recipient, fmt.Sprintf("AI员工 %s 启动失败: %v", cfg.Name, err))
				}
				return
			}
			if c.gateway.db != nil {
				c.gateway.saveChatMessage(sessionID, "user", "", message, "pending", aeChatTaskID(record.ExecID), c.name, userID)
			}
			if c.gateway.channelManager != nil {
				c.gateway.channelManager.SendToChannel(c.name, recipient, fmt.Sprintf("%s 已收到任务，开始处理...", cfg.Name))
			}
			return
		}
	}

	req := MessageRequest{
		TaskID:    fmt.Sprintf("%s_%d", c.channelType, time.Now().UnixNano()),
		TaskType:  "chat",
		SessionId: sessionID,
		Message:   channelcore.PrepareMessageForAgent(message),
		AgentName: "", // 使用默认 Agent
		Channel:   c.name,
		UserId:    userID,
		CreatedAt: time.Now(),
	}
	if matched, err := c.gateway.bindSkillMentionRequestFrom(&req, message); err != nil {
		c.sendError(recipient, messageID, err.Error())
		return
	} else if matched {
		req.Message = skilldomain.PrependMentionToTask(message, req.Message)
		if c.gateway.db == nil {
			c.sendError(recipient, messageID, "Skill Run 需要可用的持久化数据库")
			return
		}
		workspace, workspaceErr := c.gateway.bindSessionWorkspace(sessionID, req.AgentName, userID, true)
		if workspaceErr != nil {
			c.sendError(recipient, messageID, workspaceErr.Error())
			return
		}
		req.ResolvedWorkspace = workspace
	}

	taskID := req.TaskID

	// 创建任务
	task := &GatewayTask{
		ID:                taskID,
		Request:           &req,
		Status:            "pending",
		CreatedAt:         time.Now(),
		ResolvedWorkspace: req.ResolvedWorkspace,
		ResultCh:          make(chan interface{}, 1),
		InterruptCh:       make(chan string, 5),
	}
	if req.SkillRun != nil {
		if err := SaveGatewayTaskToDB(c.gateway.db, task); err != nil {
			c.sendError(recipient, messageID, fmt.Sprintf("持久化 Skill 任务失败: %v", err))
			return
		}
	}

	// 记录用户消息
	c.gateway.saveChatMessage(sessionID, "user", "", message, "pending", taskID, c.name, userID)
	interruptChannels.Store(sessionID, task.InterruptCh)

	c.gateway.tasksMu.Lock()
	c.gateway.tasks[taskID] = task
	c.gateway.tasksMu.Unlock()

	// 异步处理
	go c.processTaskAndReply(task, userID, recipient, messageID, sessionID, metadata)
}

func (c *BaseChannel) processTaskAndReply(task *GatewayTask, userID, recipient, messageID, sessionID string, metadata map[string]interface{}) {
	defer unregisterSessionInterrupt(sessionID, task.InterruptCh)
	defer close(task.ResultCh)

	task.Status = "running"

	// 获取 Agent
	var agent Agent
	var agentName string
	var exists bool
	var cleanupAgent func()
	defer func() {
		if cleanupAgent != nil {
			cleanupAgent()
		}
	}()

	if task.Request.AgentName == "" {
		foundName, foundAgent, found := c.gateway.engine.DefaultAgent()
		if found {
			agentName = foundName
			agent = foundAgent
			exists = true
		}
	} else {
		foundName, foundAgent, found := c.gateway.engine.FindAgent(task.Request.AgentName)
		if found {
			agentName = foundName
			agent = foundAgent
			exists = true
		}
	}

	if !exists || agent == nil {
		taskErr := fmt.Errorf("没有可用的 Agent")
		if strings.TrimSpace(task.Request.AgentName) != "" {
			taskErr = fmt.Errorf("agent %s not found", task.Request.AgentName)
		}
		c.sendError(recipient, messageID, taskErr.Error())
		if task.Request.SkillRun != nil {
			c.gateway.finishGatewayTask(task, "failed", taskErr.Error(), taskErr)
		} else {
			task.Status = "failed"
			task.Error = taskErr
		}
		task.ResultCh <- GatewayResponse{
			TaskID:    task.ID,
			Status:    "failed",
			Error:     taskErr.Error(),
			Timestamp: time.Now(),
		}
		return
	}
	if task.Request.SkillRun != nil && strings.TrimSpace(task.ResolvedWorkspace) != "" {
		foundName, scopedAgent, found, cleanup, createErr := c.gateway.resolveChatAgentForGatewayTask(
			task, agentName, task.ResolvedWorkspace,
		)
		if createErr != nil || !found || scopedAgent == nil {
			taskErr := createErr
			if taskErr == nil {
				taskErr = fmt.Errorf("无法为 workspace 创建 Agent: %s", agentName)
			}
			c.gateway.finishGatewayTask(task, "failed", taskErr.Error(), taskErr)
			c.sendError(recipient, messageID, taskErr.Error())
			task.ResultCh <- GatewayResponse{TaskID: task.ID, Status: "failed", Result: taskErr.Error(), Error: taskErr.Error(), Timestamp: time.Now()}
			return
		}
		agentName = foundName
		agent = scopedAgent
		cleanupAgent = cleanup
	}

	// 加载历史消息
	historyContext := c.LoadHistory(sessionID, userID, task.ID)

	// 生成回复
	finalMessage := task.Request.Message
	if historyContext != "" {
		finalMessage = historyContext + task.Request.Message
	}

	// 调用 Agent，支持运行中接受中断消息
	currentMsg := finalMessage
	var resultStr string
	var err error
	baseCtx := channelcore.AgentContext(context.Background(), task.ID, sessionID, c.name, userID)
	if task.Request.SkillRun != nil {
		baseCtx = c.gateway.withAgentOSRuntime(baseCtx)
		baseCtx = projectctx.WithResolvedWorkspace(baseCtx, task.ResolvedWorkspace)
		now := time.Now()
		task.HeartbeatAt = &now
		task.CurrentPhase = "starting"
		if saveErr := SaveGatewayTaskToDB(c.gateway.db, task); saveErr != nil {
			err = fmt.Errorf("persist running Skill task: %w", saveErr)
			c.gateway.finishGatewayTask(task, "failed", err.Error(), err)
			c.gateway.saveChatMessage(sessionID, "assistant", agentName, err.Error(), "failed", task.ID, c.name, userID)
			c.sendError(recipient, messageID, err.Error())
			task.ResultCh <- GatewayResponse{TaskID: task.ID, Status: "failed", Result: err.Error(), Error: err.Error(), Timestamp: time.Now()}
			return
		}
		baseCtx, err = c.gateway.beginSkillRun(baseCtx, task, agent)
		if err != nil {
			c.gateway.finishGatewayTask(task, "failed", err.Error(), err)
			c.gateway.saveChatMessage(sessionID, "assistant", agentName, err.Error(), "failed", task.ID, c.name, userID)
			c.sendError(recipient, messageID, err.Error())
			task.ResultCh <- GatewayResponse{TaskID: task.ID, Status: "failed", Result: err.Error(), Error: err.Error(), Timestamp: time.Now()}
			return
		}
	}
agentLoop:
	for {
		ctx, cancel := context.WithCancel(baseCtx)
		resultCh := make(chan struct {
			result string
			err    error
		}, 1)
		go func() {
			r, e := agent.Generate(ctx, currentMsg)
			resultCh <- struct {
				result string
				err    error
			}{r, e}
		}()

		select {
		case res := <-resultCh:
			resultStr, err = res.result, res.err
			cancel()
			break agentLoop
		case interruptMsg := <-task.InterruptCh:
			cancel()
			c.logger.Printf("任务 %s 收到中断消息: %s", task.ID, textutil.TruncateRunes(interruptMsg, 100))
			currentMsg = currentMsg + "\n\n[用户补充]: " + interruptMsg
			c.gateway.saveChatMessage(sessionID, "user", "", interruptMsg, "pending", task.ID, c.name, userID)
			// 继续循环，用新消息重启 agent
		}
	}

	completedAt := time.Now()
	task.CompletedAt = &completedAt

	if err != nil {
		failureOutput := agentresult.FailureUserMessage(resultStr, err)
		if task.Request.SkillRun != nil {
			terminal, applied := c.gateway.finishGatewayTask(task, "failed", failureOutput, err)
			if !applied && terminal != nil && isTerminalGatewayTaskStatus(terminal.Status) {
				failureOutput = firstNonEmpty(gatewayResultText(terminal.Result), terminal.Status)
			}
		} else {
			task.Status = "failed"
			task.Error = err
			task.Result = failureOutput
		}
		c.gateway.saveChatMessage(sessionID, "assistant", agentName, failureOutput, "failed", task.ID, c.name, userID)
		c.sendError(recipient, messageID, failureOutput)
		task.ResultCh <- GatewayResponse{
			TaskID:    task.ID,
			Status:    "failed",
			Result:    failureOutput,
			Error:     err.Error(),
			Timestamp: time.Now(),
		}
		return
	}

	if task.Request.SkillRun != nil {
		terminal, applied := c.gateway.finishGatewayTask(task, "completed", resultStr, nil)
		if !applied || terminal == nil || terminal.Status != "completed" {
			status := "failed"
			message := "Skill 任务结果已生成，但完成回执未能持久化；未向用户发布假完成状态"
			if terminal != nil && isTerminalGatewayTaskStatus(terminal.Status) {
				status = terminal.Status
				message = firstNonEmpty(gatewayResultText(terminal.Result), terminal.Status)
			}
			c.gateway.saveChatMessage(sessionID, "assistant", agentName, message, status, task.ID, c.name, userID)
			c.sendError(recipient, messageID, message)
			task.ResultCh <- GatewayResponse{TaskID: task.ID, Status: status, Result: message, Error: message, Timestamp: time.Now()}
			return
		}
	} else {
		task.Status = "completed"
		task.Result = resultStr
	}

	// 保存 AI 响应
	c.gateway.saveChatMessage(sessionID, "assistant", agentName, resultStr, "completed", task.ID, c.name, userID)

	// 发送回复（由具体渠道实现）
	c.sendReply(recipient, messageID, channelcore.FormatReplyForTransport(resultStr), metadata)

	task.ResultCh <- GatewayResponse{
		TaskID:    task.ID,
		Status:    "completed",
		Result:    resultStr,
		Timestamp: time.Now(),
	}
}

func (c *BaseChannel) sendReply(recipient, messageID, content string, metadata map[string]interface{}) {
	if c == nil || c.gateway == nil || c.gateway.channelManager == nil {
		return
	}
	if err := c.gateway.channelManager.SendToChannel(c.name, recipient, content); err != nil {
		c.logger.Printf("send reply failed: %v", err)
	}
}

func (c *BaseChannel) sendError(recipient, messageID, errMsg string) {
	c.sendReply(recipient, messageID, errMsg, nil)
}

// channelRuntimeAdapter is the application-owned implementation of the
// dependency-neutral channelhost runtime port.
type channelRuntimeAdapter struct {
	gateway *GatewayService
	bases   sync.Map
}

func newChannelRuntimeAdapter(gateway *GatewayService) *channelRuntimeAdapter {
	return &channelRuntimeAdapter{gateway: gateway}
}

func (adapter *channelRuntimeAdapter) DefaultAgent() (string, channelcore.Agent, bool) {
	if adapter == nil || adapter.gateway == nil || adapter.gateway.engine == nil {
		return "", nil, false
	}
	name, agent, ok := adapter.gateway.engine.DefaultAgent()
	return name, agent, ok
}

func (adapter *channelRuntimeAdapter) SaveChatMessage(sessionID, role, agentName, content, status, taskID, channelName, userID string) {
	if adapter == nil || adapter.gateway == nil || adapter.gateway.db == nil {
		return
	}
	adapter.gateway.saveChatMessage(sessionID, role, agentName, content, status, taskID, channelName, userID)
}

func (adapter *channelRuntimeAdapter) TryInjectInterrupt(sessionID, channelName, userID, message string) bool {
	return adapter != nil && tryInjectChannelInterrupt(adapter.gateway, sessionID, channelName, userID, message)
}

func (adapter *channelRuntimeAdapter) RegisterInterrupt(sessionID string) chan string {
	return registerSessionInterrupt(sessionID)
}

func (adapter *channelRuntimeAdapter) UnregisterInterrupt(sessionID string, interruptCh chan string) {
	unregisterSessionInterrupt(sessionID, interruptCh)
}

func (adapter *channelRuntimeAdapter) Available() bool {
	return adapter != nil && adapter.gateway != nil && adapter.gateway.db != nil
}

func (adapter *channelRuntimeAdapter) ArtifactWorkspace() string {
	if adapter == nil {
		return ""
	}
	return channelArtifactWorkspace(adapter.gateway)
}

func (adapter *channelRuntimeAdapter) HistoryContext(sessionID, userID, currentTaskID string) string {
	if !adapter.Available() || strings.TrimSpace(sessionID) == "" {
		return ""
	}
	var messages []ChatMessage
	query := adapter.gateway.db.Where(
		"session_id = ? AND role IN ('user', 'assistant') AND task_id != ?", sessionID, currentTaskID,
	)
	if strings.TrimSpace(userID) != "" {
		query = query.Where("user_id = ? OR user_id = ''", userID)
	}
	query.Order("created_at desc").Limit(10).Find(&messages)
	for i, j := 0, len(messages)-1; i < j; i, j = i+1, j-1 {
		messages[i], messages[j] = messages[j], messages[i]
	}
	if len(messages) == 0 {
		return ""
	}
	var builder strings.Builder
	builder.WriteString("以下是历史对话记录：\n\n")
	for _, message := range messages {
		roleName := "用户"
		if message.Role == "assistant" {
			roleName = "助手"
		}
		builder.WriteString(fmt.Sprintf("%s: %s\n", roleName, message.Content))
	}
	builder.WriteString("\n当前问题:\n")
	return builder.String()
}

func (adapter *channelRuntimeAdapter) ProcessMessage(
	channelName, channelType, userID, recipient, messageID, message string,
	metadata map[string]interface{},
) {
	if adapter == nil || adapter.gateway == nil {
		return
	}
	key := channelType + "\x00" + channelName
	value, _ := adapter.bases.LoadOrStore(key, NewBaseChannel(channelName, channelType))
	base, _ := value.(*BaseChannel)
	if base == nil {
		return
	}
	base.SetGateway(adapter.gateway)
	base.ProcessUserMessage(userID, recipient, messageID, message, metadata)
}

func (adapter *channelRuntimeAdapter) TryCodex(
	ctx context.Context,
	sessionID, channelName, userID, recipient, message string,
) (bool, string, string, error) {
	if adapter == nil || adapter.gateway == nil || adapter.gateway.codexInteractive == nil {
		return false, "", "", nil
	}
	if !shouldRouteToCodexInteractive(message, adapter.gateway.codexInteractive.Has(sessionID)) {
		return false, "", "", nil
	}
	taskID, reply, err := adapter.gateway.codexInteractive.StartOrSend(
		ctx, sessionID, channelName, userID, recipient, message, "",
	)
	if taskID == "" {
		taskID = aeChatTaskID(sessionID + "-codex")
	}
	return true, taskID, reply, err
}

func (adapter *channelRuntimeAdapter) TryEmployee(
	ctx context.Context,
	message, sessionID, channelName, recipient, userID string,
) (bool, string, string, error) {
	if adapter == nil || adapter.gateway == nil || adapter.gateway.aeManager == nil {
		return false, "", "", nil
	}
	config, task := adapter.gateway.aeManager.MatchAtName(message)
	if config == nil {
		return false, "", "", nil
	}
	record, err := adapter.gateway.aeManager.DispatchExplicitMentionFromChannel(
		ctx, config.ID, task, sessionID, channelName, recipient, userID,
	)
	if err != nil {
		return true, config.Name, "", err
	}
	taskID := ""
	if record != nil {
		taskID = aeChatTaskID(record.ExecID)
	}
	return true, config.Name, taskID, nil
}

func (adapter *channelRuntimeAdapter) ResolveApproval(id, decision string) (*channelcore.ApprovalRequest, bool) {
	request, ok := globalCodexApprovalManager.Resolve(id, decision)
	if !ok || request == nil {
		return nil, false
	}
	converted := &channelcore.ApprovalRequest{
		ID: request.ID, SessionID: request.SessionID, Recipient: request.Recipient,
		Kind: request.Kind, Summary: request.Summary, Command: request.Command, CWD: request.CWD,
	}
	if request.ShellAnalysis != nil {
		converted.ShellAnalysis = &channelcore.ApprovalShellAnalysis{
			Class: string(request.ShellAnalysis.Class), Risk: request.ShellAnalysis.Risk,
			Blocked: request.ShellAnalysis.Blocked, BlockReason: request.ShellAnalysis.BlockReason,
			SuggestedRule: request.ShellAnalysis.SuggestedRule,
		}
	}
	return converted, true
}

// StreamCallback is called for each chunk of streaming response.
type StreamCallback func(chunk StreamChunk)

// StreamChunk represents a single streaming response chunk.
type StreamChunk struct {
	Content          string `json:"content,omitempty"`
	ReasoningContent string `json:"reasoning_content,omitempty"`
	FinishReason     string `json:"finish_reason,omitempty"`
	ToolCallName     string `json:"tool_call_name,omitempty"`
	ToolCallArgs     string `json:"tool_call_args,omitempty"`
	IsFinal          bool   `json:"is_final"`
	toolCallID       string
}

// sanitizedStreamEmitter keeps terminal-parser state between provider chunks.
// The final response is sanitized again after assembly, but callbacks happen
// before that point and must never observe a split ANSI/OSC/C0 fragment.
type sanitizedStreamEmitter struct {
	callback  StreamCallback
	content   textnormalize.TerminalControlStreamSanitizer
	reasoning textnormalize.TerminalControlStreamSanitizer
	toolNames map[string]*textnormalize.TerminalControlStreamSanitizer
	toolArgs  map[string]*textnormalize.TerminalControlStreamSanitizer
}

func newSanitizedStreamEmitter(callback StreamCallback) *sanitizedStreamEmitter {
	return &sanitizedStreamEmitter{
		callback:  callback,
		toolNames: make(map[string]*textnormalize.TerminalControlStreamSanitizer),
		toolArgs:  make(map[string]*textnormalize.TerminalControlStreamSanitizer),
	}
}

func (e *sanitizedStreamEmitter) Emit(chunk StreamChunk) {
	if e == nil || e.callback == nil {
		return
	}

	chunk.Content = e.content.SanitizeChunk(chunk.Content)
	chunk.ReasoningContent = e.reasoning.SanitizeChunk(chunk.ReasoningContent)
	toolID := chunk.toolCallID
	if toolID == "" {
		toolID = "_default"
	}
	if chunk.ToolCallName != "" {
		chunk.ToolCallName = streamFieldSanitizer(e.toolNames, toolID).SanitizeChunk(chunk.ToolCallName)
	}
	if chunk.ToolCallArgs != "" {
		chunk.ToolCallArgs = streamFieldSanitizer(e.toolArgs, toolID).SanitizeChunk(chunk.ToolCallArgs)
	}
	chunk.FinishReason = textnormalize.StripTerminalControlSequences(chunk.FinishReason)

	if chunk.Content != "" || chunk.ReasoningContent != "" || chunk.ToolCallName != "" ||
		chunk.ToolCallArgs != "" || chunk.IsFinal {
		e.callback(chunk)
	}
}

func streamFieldSanitizer(sanitizers map[string]*textnormalize.TerminalControlStreamSanitizer, id string) *textnormalize.TerminalControlStreamSanitizer {
	if sanitizer := sanitizers[id]; sanitizer != nil {
		return sanitizer
	}
	sanitizer := &textnormalize.TerminalControlStreamSanitizer{}
	sanitizers[id] = sanitizer
	return sanitizer
}

func (e *sanitizedStreamEmitter) Finish() {
	if e == nil {
		return
	}
	e.content.Finish()
	e.reasoning.Finish()
	for _, sanitizers := range []map[string]*textnormalize.TerminalControlStreamSanitizer{e.toolNames, e.toolArgs} {
		for _, sanitizer := range sanitizers {
			sanitizer.Finish()
		}
	}
}

// callLLMStream makes a streaming LLM API call with SSE parsing.
// Tool calls are collected here and executed later by the agent's unified tool
// execution path. The SSE parser must never execute tools itself: doing so
// bypasses safety/hooks/snapshots and can execute a call twice when the main
// agent loop subsequently processes AgentResponse.ToolCalls.
func (a *LLMAgent) callLLMStream(ctx context.Context, messages []map[string]interface{}, tools []map[string]interface{}, callback StreamCallback) (*AgentResponse, error) {
	return a.callLLMStreamAdaptive(ctx, messages, tools, callback, true)
}

func (a *LLMAgent) callLLMStreamAdaptive(ctx context.Context, messages []map[string]interface{}, tools []map[string]interface{}, callback StreamCallback, allowProtocolRecovery bool) (result *AgentResponse, resultErr error) {
	ctx = context.WithValue(ctx, promptMemoryWireHistoryKey{}, promptMemoryHistoryOrigins(ctx, messages))
	ctx = modelruntime.WithPlatformCallIdentity(ctx, a.modelConfig.BaseURL)
	circuitCtx, circuitOutcome, ownsCircuitOutcome, err := runtimeutil.BeginLLMCircuitOutcome(ctx, a.circuitBreaker)
	if err != nil {
		return nil, fmt.Errorf("circuit breaker: %w", err)
	}
	ctx = circuitCtx
	if ownsCircuitOutcome {
		defer func() {
			circuitOutcome.Finish(resultErr == nil && result != nil)
		}()
	}
	policy := a.resolvedLLMCallPolicyForContext(ctx)
	requestBody := map[string]interface{}{
		"model":       a.modelConfig.ModelName,
		"messages":    messages,
		"max_tokens":  modelruntime.EffectiveMaxTokens(ctx, a.modelConfig.MaxTokens),
		"temperature": modelruntime.EffectiveTemperature(ctx, a.modelConfig.Temperature),
		"stream":      true,
	}
	sentThinkingMode := modelruntime.ApplyThinkingPolicy(ctx, a.modelConfig.thinkingPolicyModel(), messages, requestBody)
	if len(tools) > 0 {
		requestBody["tools"] = tools
	}

	bodyJSON, err := json.Marshal(requestBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	baseURL := strings.TrimRight(a.modelConfig.BaseURL, "/")
	if baseURL == "" {
		baseURL = "https://api.deepseek.com/v1"
	}
	endpoint := baseURL + "/chat/completions"
	var lastErr error
	nextRetryDelay := time.Duration(0)
	hasNextRetryDelay := false
	for attempt := 0; attempt < policy.MaxAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if attempt > 0 {
			delay := nextRetryDelay
			if !hasNextRetryDelay {
				delay = policy.RetryBackoff(attempt)
			}
			nextRetryDelay = 0
			hasNextRetryDelay = false
			if a.fileLogger != nil {
				a.fileLogger.Printf("[LLM-STREAM] waiting %v before same-model retry %d/%d", delay, attempt+1, policy.MaxAttempts)
			}
			if err := modelruntime.WaitForRetry(ctx, delay); err != nil {
				return nil, err
			}
		}

		emitted := false
		trackedCallback := func(chunk StreamChunk) {
			emitted = true
			if callback != nil {
				callback(chunk)
			}
		}
		response, err := a.callLLMStreamAttempt(ctx, endpoint, bodyJSON, policy.AttemptTimeout, attempt, trackedCallback)
		if err == nil {
			return response, nil
		}
		lastErr = err
		status, headers, hasStatus := modelruntime.HTTPErrorMetadata(err)
		runtimeutil.NoteLLMCircuitFailure(ctx, status)

		if modelruntime.IsReasoningContextProtocolError(err.Error()) {
			learned := modelruntime.MarkAdaptiveThinkingDisabled(a.modelConfig.thinkingPolicyModel())
			if allowProtocolRecovery && sentThinkingMode != modelruntime.ThinkingModeDisabled && modelruntime.IsAdaptiveThinkingDisabled(a.modelConfig.thinkingPolicyModel()) {
				if a.fileLogger != nil {
					a.fileLogger.Printf("[PROTOCOL] streaming reasoning replay rejected; learned thinking=disabled and retrying once: model=%s newly_learned=%t", a.modelConfig.ModelName, learned)
				}
				return a.callLLMStreamAdaptive(ctx, messages, tools, callback, false)
			}
		}

		// Once any bytes were exposed to the caller, replaying the request would
		// duplicate visible output. Only pre-stream failures are safe to retry.
		retryable := !emitted && ctx.Err() == nil
		if hasStatus {
			retryable = retryable && modelruntime.IsRetryableHTTPStatus(status, headers)
		}
		if !retryable || attempt+1 >= policy.MaxAttempts {
			return nil, err
		}
		if hasStatus {
			nextRetryDelay = policy.RetryDelay(attempt+1, headers.Get("Retry-After"), time.Now())
			hasNextRetryDelay = true
		}
	}
	return nil, lastErr
}

func (a *LLMAgent) callLLMStreamAttempt(ctx context.Context, endpoint string, bodyJSON []byte, timeout time.Duration, attempt int, callback StreamCallback) (*AgentResponse, error) {
	callCtx, cancel := modelruntime.AttemptContext(ctx, timeout)
	defer cancel()
	httpReq, err := http.NewRequestWithContext(callCtx, "POST", endpoint, strings.NewReader(string(bodyJSON)))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if err := modelruntime.AuthorizeModelRequest(httpReq, a.modelConfig.APIKey, modelConfigUsesAccount(*a.modelConfig)); err != nil {
		return nil, err
	}
	httpReq.Header.Set("Accept", "text/event-stream")
	httpReq.Header.Set("X-Retry-Count", fmt.Sprintf("%d", attempt))
	modelruntime.SetPlatformCallIdentity(httpReq, bodyJSON)

	runtimeutil.MarkLLMProviderRequest(callCtx)
	promptAttempt := a.capturePromptMemoryInput(callCtx, bodyJSON)
	resp, err := modelruntime.ModelHTTPClient(a.httpClient, modelConfigUsesAccount(*a.modelConfig)).Do(httpReq)
	a.finishPromptMemoryInput(callCtx, promptAttempt, resp, err)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, &modelruntime.HTTPStatusError{
			StatusCode: resp.StatusCode,
			Header:     resp.Header.Clone(),
			Message:    fmt.Sprintf("API error %d: %s", resp.StatusCode, string(body)),
		}
	}

	// Parse only. Tool execution belongs to AgentToolExecutor, which applies the
	// same lifecycle regardless of whether the model response was streamed.
	response, err := parseSSEStream(resp.Body, callback)
	if err != nil {
		return nil, fmt.Errorf("parse stream: %w", err)
	}
	return response, nil
}

// parseSSEStream parses Server-Sent Events from the response body.
func parseSSEStream(body io.Reader, callback StreamCallback) (*AgentResponse, error) {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	emitter := newSanitizedStreamEmitter(callback)
	defer emitter.Finish()

	var fullContent strings.Builder
	var reasoningContent strings.Builder
	var toolCalls []ToolCall
	var finishReason string
	var usage struct {
		PromptTokens     int
		CompletionTokens int
	}
	currentToolCall := make(map[string]*ToolCall)
	currentArgs := make(map[string]*strings.Builder)

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
			break
		}

		var chunk map[string]interface{}
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}

		// Process usage info
		if u, ok := chunk["usage"].(map[string]interface{}); ok {
			if pt, ok := u["prompt_tokens"].(float64); ok {
				usage.PromptTokens = int(pt)
			}
			if ct, ok := u["completion_tokens"].(float64); ok {
				usage.CompletionTokens = int(ct)
			}
		}

		choices, ok := chunk["choices"].([]interface{})
		if !ok || len(choices) == 0 {
			continue
		}

		choice := choices[0].(map[string]interface{})
		delta, ok := choice["delta"].(map[string]interface{})
		if !ok {
			continue
		}

		streamChunk := StreamChunk{}

		// Content delta
		if content, ok := delta["content"].(string); ok && content != "" {
			fullContent.WriteString(content)
			streamChunk.Content = content
		}

		// Reasoning content
		if reasoning, ok := delta["reasoning_content"].(string); ok && reasoning != "" {
			reasoningContent.WriteString(reasoning)
			streamChunk.ReasoningContent = reasoning
		}

		// Tool calls
		if tcs, ok := delta["tool_calls"].([]interface{}); ok {
			for _, tcRaw := range tcs {
				tcMap, ok := tcRaw.(map[string]interface{})
				if !ok {
					continue
				}
				tcID, _ := tcMap["id"].(string)
				if tcID == "" {
					tcID = fmt.Sprintf("tc_%d", len(toolCalls))
				}

				fn, ok := tcMap["function"].(map[string]interface{})
				if !ok {
					continue
				}

				if name, ok := fn["name"].(string); ok && name != "" {
					tc := ToolCall{ID: tcID, Name: name}
					currentToolCall[tcID] = &tc
					currentArgs[tcID] = &strings.Builder{}
					streamChunk.ToolCallName = name
					streamChunk.toolCallID = tcID
				}

				if args, ok := fn["arguments"].(string); ok && args != "" {
					if sb, exists := currentArgs[tcID]; exists {
						sb.WriteString(args)
						streamChunk.ToolCallArgs = args
						streamChunk.toolCallID = tcID
					}
				}
			}
		}

		// Finish reason
		if fr, ok := choice["finish_reason"].(string); ok && fr != "" {
			finishReason = fr
			streamChunk.FinishReason = fr
			if fr == "stop" || fr == "tool_calls" {
				streamChunk.IsFinal = true
			}
		}

		emitter.Emit(streamChunk)
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scanner error: %w", err)
	}

	// Finalize tool calls
	for id, tc := range currentToolCall {
		if sb, ok := currentArgs[id]; ok {
			var args map[string]interface{}
			json.Unmarshal([]byte(sb.String()), &args)
			tc.Arguments = args
		}
		toolCalls = append(toolCalls, *tc)
	}
	if isIncompleteLLMFinishReason(finishReason) {
		// Partial tool arguments are never executable. The Generate-level bounded
		// completion gate will request one compact rewrite of the whole response.
		toolCalls = nil
	}

	rawReasoning := reasoningContent.String()
	return &AgentResponse{
		Content:                textnormalize.ModelText(fullContent.String()),
		ReasoningContent:       textnormalize.StripTerminalControlSequences(rawReasoning),
		ReasoningReplayContent: rawReasoning,
		ToolCalls:              toolCalls,
		FinishReason:           finishReason,
		PromptTokens:           usage.PromptTokens,
		CompletionTokens:       usage.CompletionTokens,
	}, nil
}

// parseSSEStreamWithExec parses SSE stream and executes tools DURING streaming.
// Claude Code pattern: tools start executing as soon as their arguments are complete.
func (a *LLMAgent) parseSSEStreamWithExec(body io.Reader, callback StreamCallback) (*AgentResponse, error) {
	executor := agentloop.NewStreamExecutor(func(name string) bool {
		if tool, ok := a.toolRegistry.Get(name); ok {
			if _, neverParallel := tool.(NeverParallelTool); neverParallel {
				return false
			}
			if readOnly, declared := tool.(ReadOnlyTool); declared {
				return readOnly.IsReadOnly()
			}
		}
		return toolcontract.IsReadOnlyToolName(name)
	}, func(ctx context.Context, name string, args map[string]interface{}) (string, error) {
		if tool, ok := a.toolRegistry.Get(name); ok {
			return tool.Execute(ctx, args)
		}
		return "", fmt.Errorf("tool not found: %s", name)
	})

	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	emitter := newSanitizedStreamEmitter(callback)
	defer emitter.Finish()

	var fullContent, reasoningContent strings.Builder
	var toolCalls []ToolCall
	var usagePrompt, usageCompletion int
	curTC := make(map[string]*ToolCall)
	curArgs := make(map[string]strings.Builder)
	tcOrder := make([]string, 0)
	finishReason := ""

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" || strings.HasPrefix(line, ":") || !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			break
		}

		var chunk map[string]interface{}
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}

		if u, ok := chunk["usage"].(map[string]interface{}); ok {
			if pt, ok := u["prompt_tokens"].(float64); ok {
				usagePrompt = int(pt)
			}
			if ct, ok := u["completion_tokens"].(float64); ok {
				usageCompletion = int(ct)
			}
		}

		choices, _ := chunk["choices"].([]interface{})
		if len(choices) == 0 {
			continue
		}
		choice := choices[0].(map[string]interface{})

		// 🔥 KEY: when finish_reason arrives, tool args are complete — start executing NOW
		chunkFinishReason := ""
		chunkIsFinal := false
		if fr, ok := choice["finish_reason"].(string); ok && fr != "" {
			finishReason = fr
			chunkFinishReason = fr
			chunkIsFinal = fr == "tool_calls" || fr == "stop"
			if fr == "tool_calls" || fr == "stop" {
				for _, id := range tcOrder {
					tc, ok1 := curTC[id]
					sb, ok2 := curArgs[id]
					if ok1 && ok2 {
						argStr := sb.String()
						if argStr != "" {
							json.Unmarshal([]byte(argStr), &tc.Arguments)
						}
						// Start executing immediately while stream continues
						executor.SubmitCall(agentloop.PendingCall{ID: tc.ID, Name: tc.Name, Arguments: argStr})
					}
				}
				// Save tool calls for response
				for _, id := range tcOrder {
					if tc := curTC[id]; tc != nil {
						toolCalls = append(toolCalls, *tc)
					}
				}
				curTC = make(map[string]*ToolCall)
				curArgs = make(map[string]strings.Builder)
			}
		}

		delta, _ := choice["delta"].(map[string]interface{})
		if delta == nil {
			emitter.Emit(StreamChunk{FinishReason: chunkFinishReason, IsFinal: chunkIsFinal})
			continue
		}

		sc := StreamChunk{FinishReason: chunkFinishReason, IsFinal: chunkIsFinal}
		if content, ok := delta["content"].(string); ok && content != "" {
			fullContent.WriteString(content)
			sc.Content = content
		}
		if reasoning, ok := delta["reasoning_content"].(string); ok && reasoning != "" {
			reasoningContent.WriteString(reasoning)
			sc.ReasoningContent = reasoning
		}

		if tcs, ok := delta["tool_calls"].([]interface{}); ok {
			for _, tcRaw := range tcs {
				tcMap, _ := tcRaw.(map[string]interface{})
				if tcMap == nil {
					continue
				}
				tcID, _ := tcMap["id"].(string)
				if tcID == "" {
					tcID = fmt.Sprintf("tc_%d", len(tcOrder))
				}
				fn, _ := tcMap["function"].(map[string]interface{})
				if fn == nil {
					continue
				}
				if name, ok := fn["name"].(string); ok && name != "" {
					if _, exists := curTC[tcID]; !exists {
						tcOrder = append(tcOrder, tcID)
						curArgs[tcID] = strings.Builder{}
					}
					curTC[tcID] = &ToolCall{ID: tcID, Name: name}
					sc.ToolCallName = name
					sc.toolCallID = tcID
				}
				if args, ok := fn["arguments"].(string); ok && args != "" {
					if sb, exists := curArgs[tcID]; exists {
						sb.WriteString(args)
					}
					sc.ToolCallArgs = args
					sc.toolCallID = tcID
				}
			}
		}

		emitter.Emit(sc)
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scanner: %w", err)
	}

	if isIncompleteLLMFinishReason(finishReason) {
		// finish_reason=length/abort/incomplete never authorizes dispatch of
		// partially streamed tool arguments or acceptance as a final response.
		toolCalls = nil
	} else {
		// Wait for in-flight tool executions (with timeout) only for a complete
		// provider response. Incomplete streams cannot have authorized calls and
		// must not add a pointless 30-second collector wait before repair.
		executor.WaitForAll(30 * time.Second)
	}

	rawReasoning := reasoningContent.String()
	return &AgentResponse{
		Content:                textnormalize.ModelText(fullContent.String()),
		ReasoningContent:       textnormalize.StripTerminalControlSequences(rawReasoning),
		ReasoningReplayContent: rawReasoning,
		ToolCalls:              toolCalls,
		FinishReason:           finishReason,
		PromptTokens:           usagePrompt,
		CompletionTokens:       usageCompletion,
		PreExecResults:         executor.Results(),
	}, nil
}

// NotifySupervisor sends a notification about a DE task failure.

// SendWeeklyHealthReport emails a KB health summary to the admin.
func (g *GatewayService) SendWeeklyHealthReport() {
	if g.config.EmailConfig.SMTPHost == "" {
		return
	}
	adminEmail := g.config.EmailConfig.FromAddress
	if adminEmail == "" {
		return
	}

	health := ragcore.RAGHealth(0)
	stats := ""

	if dbCount, ok := health["db_count"].(int64); ok {
		stats += fmt.Sprintf("DB chunks: %d\n", dbCount)
	}
	if bleveCount, ok := health["bleve_count"].(uint64); ok {
		stats += fmt.Sprintf("Bleve docs: %d\n", bleveCount)
	}
	if consistent, ok := health["consistent"].(bool); ok {
		stats += fmt.Sprintf("Consistent: %v\n", consistent)
	}
	if engine, ok := health["engine"].(string); ok {
		stats += fmt.Sprintf("Engine: %s\n", engine)
	}

	// DE stats
	if g.aeManager != nil {
		g.aeManager.mu.RLock()
		total := len(g.aeManager.employees)
		completed, failed := 0, 0
		for id := range g.aeManager.employees {
			if wl, ok := g.aeManager.workLog[id]; ok {
				for _, w := range wl {
					if w.Status == "completed" {
						completed++
					}
					if w.Status == "failed" {
						failed++
					}
				}
			}
		}
		g.aeManager.mu.RUnlock()
		stats += fmt.Sprintf("\nDEs: %d total, %d completed, %d failed\n", total, completed, failed)
	}

	body := operationstools.WeeklyHealthReportBody(stats, time.Now())
	config := notificationSMTPConfig(g.config.EmailConfig)

	go func() {
		if err := operationstools.SendNotificationEmail(config, adminEmail, "[MACS] Weekly Health Report", body); err != nil {
			log.Printf("[HealthReport] Email failed: %v", err)
		} else {
			log.Printf("[HealthReport] Weekly report sent to %s", adminEmail)
		}
	}()
}

func (g *GatewayService) NotifySupervisor(deName, supervisor, task, errorMsg string) {
	if g.config.EmailConfig.SMTPHost == "" || supervisor == "" {
		return
	}
	config := notificationSMTPConfig(g.config.EmailConfig)
	subject, body := operationstools.TaskFailureNotification(deName, task, errorMsg, time.Now())
	go func() {
		if err := operationstools.SendNotificationEmail(config, supervisor, subject, body); err != nil {
			log.Printf("[Notify] Email to %s failed: %v", supervisor, err)
		}
	}()
}

func notificationSMTPConfig(config EmailConfig) operationstools.SMTPNotificationConfig {
	return operationstools.SMTPNotificationConfig{
		Host:        config.SMTPHost,
		Port:        config.SMTPPort,
		Username:    config.Username,
		Password:    config.Password,
		FromAddress: config.FromAddress,
	}
}

func (g *GatewayService) handleWeixinLoginStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		ChannelName string `json:"channel"`
		Force       bool   `json:"force"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	localTokens := g.weixinLocalTokens(req.ChannelName)
	result, err := channelcore.StartWeixinQRLogin(r.Context(), channelcore.WeixinQRStartOptions{
		ChannelName: req.ChannelName,
		LocalTokens: localTokens,
		Force:       req.Force,
	})
	if err != nil {
		writeWeixinJSON(w, http.StatusBadGateway, map[string]interface{}{"ok": false, "error": err.Error()})
		return
	}
	writeWeixinJSON(w, http.StatusOK, result)
}

func (g *GatewayService) handleWeixinLoginWait(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		SessionKey string `json:"session_key"`
		Channel    string `json:"channel"`
		VerifyCode string `json:"verify_code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	result, err := channelcore.WaitWeixinQRLoginOnce(r.Context(), req.SessionKey, req.VerifyCode)
	if err != nil {
		writeWeixinJSON(w, http.StatusBadGateway, map[string]interface{}{"ok": false, "error": err.Error()})
		return
	}
	if result.Connected {
		if result.AccountID == "" || result.Token == "" {
			writeWeixinJSON(w, http.StatusBadGateway, map[string]interface{}{"ok": false, "error": "confirmed login is missing account_id or token"})
			return
		}
		channelName, err := g.persistWeixinLogin(req.Channel, result)
		if err != nil {
			writeWeixinJSON(w, http.StatusInternalServerError, map[string]interface{}{"ok": false, "error": err.Error()})
			return
		}
		result.Message = "Weixin login saved to channel " + channelName + "."
	}
	writeWeixinJSON(w, http.StatusOK, result)
}

func (g *GatewayService) handleWeixinStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	items := make([]map[string]interface{}, 0)
	if g.config != nil {
		for _, cfg := range g.config.Channels {
			if !channelcore.IsWeixinChannelType(cfg.Type) {
				continue
			}
			accountID := getString(cfg.Config, "account_id", "")
			token := getString(cfg.Config, "token", "")
			running := false
			if g.channelManager != nil {
				if ch, ok := g.channelManager.GetChannel(cfg.Name); ok {
					if wx, ok := ch.(*channelhost.WeixinChannel); ok {
						running = wx.IsRunning()
					}
				}
			}
			items = append(items, map[string]interface{}{
				"name":       cfg.Name,
				"type":       cfg.Type,
				"enabled":    cfg.Enabled,
				"configured": accountID != "" && token != "",
				"account_id": accountID,
				"token":      channelcore.MaskWeixinToken(token),
				"base_url":   firstNonEmptyString(getString(cfg.Config, "base_url", ""), channelcore.WeixinDefaultBaseURL),
				"running":    running,
			})
		}
	}
	writeWeixinJSON(w, http.StatusOK, map[string]interface{}{"ok": true, "channels": items})
}

func (g *GatewayService) weixinLocalTokens(channelName string) []string {
	var tokens []string
	if g.config == nil {
		return tokens
	}
	for _, cfg := range g.config.Channels {
		if !channelcore.IsWeixinChannelType(cfg.Type) {
			continue
		}
		if channelName != "" && cfg.Name != channelName {
			continue
		}
		if token := strings.TrimSpace(getString(cfg.Config, "token", "")); token != "" {
			tokens = append(tokens, token)
		}
	}
	return tokens
}

func (g *GatewayService) persistWeixinLogin(channelName string, result *channelcore.WeixinQRWaitResult) (string, error) {
	g.mutex.Lock()
	defer g.mutex.Unlock()

	configPath := firstNonEmptyString(g.configpath, g.configPath)
	if configPath == "" {
		return "", fmt.Errorf("config path is empty")
	}
	cfg, err := LoadConfig(configPath)
	if err != nil {
		return "", err
	}
	channelName = strings.TrimSpace(channelName)
	if channelName == "" {
		channelName = firstWeixinChannelName(cfg.Channels)
	}
	if channelName == "" {
		channelName = "weixin"
	}

	updatedName := upsertWeixinLoginConfig(cfg, channelName, result)
	if err := SaveConfig(configPath, cfg); err != nil {
		return "", err
	}
	if g.channelManager != nil {
		if err := g.updateChannelsConfig(cfg.Channels); err != nil {
			return "", err
		}
	}
	g.config = cfg
	return updatedName, nil
}

func upsertWeixinLoginConfig(cfg *Config, channelName string, result *channelcore.WeixinQRWaitResult) string {
	if cfg == nil {
		return channelName
	}
	for i := range cfg.Channels {
		if cfg.Channels[i].Name == channelName || (channelName == "" && channelcore.IsWeixinChannelType(cfg.Channels[i].Type)) {
			mergeWeixinLoginConfig(&cfg.Channels[i], result)
			return cfg.Channels[i].Name
		}
	}
	newCfg := ChannelConfig{
		Name:    channelName,
		Type:    "weixin",
		Enabled: true,
		Config:  map[string]interface{}{},
	}
	mergeWeixinLoginConfig(&newCfg, result)
	cfg.Channels = append(cfg.Channels, newCfg)
	return newCfg.Name
}

func mergeWeixinLoginConfig(cfg *ChannelConfig, result *channelcore.WeixinQRWaitResult) {
	if cfg.Config == nil {
		cfg.Config = map[string]interface{}{}
	}
	if strings.TrimSpace(cfg.Type) == "" {
		cfg.Type = "weixin"
	}
	cfg.Enabled = true
	cfg.Config["account_id"] = result.AccountID
	cfg.Config["token"] = result.Token
	cfg.Config["base_url"] = firstNonEmptyString(result.BaseURL, channelcore.WeixinDefaultBaseURL)
	if strings.TrimSpace(getString(cfg.Config, "bot_agent", "")) == "" {
		cfg.Config["bot_agent"] = "MACS/1.0 OpenClawCompatible/2.4.6"
	}
	if result.UserID != "" {
		existing := firstNonEmptyStringList(getStringSlice(cfg.Config, "allowed_users", nil), channelcore.SplitWeixinCSV(getString(cfg.Config, "allowed_users", "")))
		if len(existing) == 0 {
			cfg.Config["allowed_users"] = []string{result.UserID}
		}
	}
}

func firstWeixinChannelName(channels []ChannelConfig) string {
	for _, cfg := range channels {
		if channelcore.IsWeixinChannelType(cfg.Type) {
			return cfg.Name
		}
	}
	return ""
}

func channelConfigEqualForReload(a, b ChannelConfig) bool {
	return a.Name == b.Name &&
		a.Type == b.Type &&
		a.Enabled == b.Enabled &&
		reflect.DeepEqual(a.Config, b.Config)
}

func writeWeixinJSON(w http.ResponseWriter, status int, value interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func firstNonEmptyString(values ...string) string {
	return channelcore.FirstNonEmptyString(values...)
}

func firstNonEmptyStringList(values ...[]string) []string {
	return channelcore.FirstNonEmptyStringList(values...)
}

type appMCPServerHost struct{ gateway *GatewayService }

func NewMCPServer(gateway *GatewayService) *mcpcore.Server {
	return mcpcore.NewServer(&appMCPServerHost{gateway: gateway}, CurrentVersion)
}

func (h *appMCPServerHost) AuthenticateRequest(request *http.Request) (context.Context, error) {
	if h == nil || h.gateway == nil {
		return nil, fmt.Errorf("gateway is unavailable")
	}
	return h.gateway.authenticateExecutionRequest(request, "mcp")
}

func (h *appMCPServerHost) WithRequestedRole(ctx context.Context, role string) context.Context {
	if identity, ok := executionIdentityFromContext(ctx); ok {
		identity.RoleID = strings.TrimSpace(role)
		return withExecutionIdentity(ctx, identity)
	}
	return ctx
}

func (h *appMCPServerHost) CanInspectResources(ctx context.Context) bool {
	identity, ok := executionIdentityFromContext(ctx)
	return !ok || !identity.TenantScoped || identity.ControlPlane
}

func (h *appMCPServerHost) ListTools(ctx context.Context) ([]mcpcore.ToolDefinition, error) {
	identity, _ := executionIdentityFromContext(ctx)
	registry, _, err := h.resolveRegistry(ctx, identity.RoleID)
	if err != nil {
		return nil, err
	}
	tools := make([]mcpcore.ToolDefinition, 0, registry.Count())
	for _, name := range registry.ListNames() {
		tool, ok := registry.Get(name)
		if !ok {
			continue
		}
		tools = append(tools, mcpcore.ToolDefinition{
			Name: name, Description: tool.Description(), InputSchema: tool.Parameters(),
		})
	}
	return tools, nil
}

func (h *appMCPServerHost) ExecuteTool(ctx context.Context, name string, arguments map[string]interface{}, callID string) (mcpcore.ExecutionResult, error) {
	identity, _ := executionIdentityFromContext(ctx)
	registry, resolvedIdentity, err := h.resolveRegistry(ctx, identity.RoleID)
	if err != nil {
		return mcpcore.ExecutionResult{}, err
	}
	if _, ok := registry.Get(name); !ok {
		return mcpcore.ExecutionResult{}, fmt.Errorf("tool not found for role %q: %s", resolvedIdentity.RoleID, name)
	}
	result, err := registry.Execute(withExecutionIdentity(ctx, resolvedIdentity), ToolCall{
		ID: callID, Name: name, Arguments: arguments,
	})
	return mcpcore.ExecutionResult{
		Content: result.Content, ExecutionSucceeded: result.ExecutionSucceeded,
	}, err
}

func (h *appMCPServerHost) ReadResource(_ context.Context, uri string) (string, error) {
	if h == nil || h.gateway == nil {
		return "", fmt.Errorf("gateway is unavailable")
	}
	switch uri {
	case "openkitty://sessions":
		sessionCount := 0
		if h.gateway.engine != nil && h.gateway.engine.sessionManager != nil {
			sessionCount = len(h.gateway.engine.sessionManager.sessions)
		}
		return fmt.Sprintf(`{"sessions_count": %d}`, sessionCount), nil
	case "openkitty://tasks":
		return `{"tasks": "see /api/tasks for full list"}`, nil
	case "openkitty://digital-employees":
		return fmt.Sprintf(`{"employees_count": %d}`, len(h.gateway.config.AIEmployees)), nil
	case "openkitty://skills":
		if h.gateway.engine != nil {
			for _, agent := range h.gateway.engine.getAllAgents() {
				if llmAgent, ok := agent.(*LLMAgent); ok && llmAgent.skillsLoader != nil {
					return llmAgent.skillsLoader.BuildSkillsSummary(), nil
				}
			}
		}
		return "{}", nil
	default:
		return "", fmt.Errorf("Unknown resource: %s", uri)
	}
}

func (h *appMCPServerHost) StdioContext() context.Context {
	return withExecutionIdentity(context.Background(), ExecutionIdentity{
		TenantID: defaultExecutionTenantID,
		Source:   "mcp_stdio",
		Service:  true,
	})
}

func (h *appMCPServerHost) Logf(format string, args ...interface{}) {
	if h != nil && h.gateway != nil && h.gateway.logger != nil {
		h.gateway.logger.Printf(format, args...)
	}
}

func (h *appMCPServerHost) resolveRegistry(parent context.Context, requestedRole string) (*toolregistry.Registry, ExecutionIdentity, error) {
	if h == nil || h.gateway == nil || h.gateway.engine == nil {
		return nil, ExecutionIdentity{}, fmt.Errorf("agent engine is unavailable")
	}
	if parent == nil {
		parent = context.Background()
	}
	identity, ok := executionIdentityFromContext(parent)
	if !ok {
		identity = ExecutionIdentity{
			TenantID: defaultExecutionTenantID,
			Source:   "mcp_stdio",
			Service:  true,
		}
	}
	callerID := agentos.NormalizeCallerID(identity.ServiceID)
	resolvedRole, agentName, err := h.gateway.resolveAgentOSRole(requestedRole, callerID, identity)
	if err != nil {
		return nil, ExecutionIdentity{}, err
	}
	agent, ok := h.gateway.engine.getAllAgents()[agentName]
	if !ok {
		return nil, ExecutionIdentity{}, fmt.Errorf("agent role %q is unavailable", resolvedRole)
	}
	llmAgent, ok := agent.(*LLMAgent)
	if !ok || llmAgent.toolRegistry == nil {
		return nil, ExecutionIdentity{}, fmt.Errorf("agent role %q has no tool registry", resolvedRole)
	}
	identity.RoleID = resolvedRole
	identity.AgentName = agentName
	return llmAgent.toolRegistry, identity, nil
}

// RunMCPStdio starts the MCP server in newline-delimited stdio mode.
func RunMCPStdio(gateway *GatewayService) {
	log.SetOutput(io.Discard)
	mcpcore.RunStdio(NewMCPServer(gateway))
}

const maxRobotAPIRequestBytes = 4096

type robotAPICommandRequest struct {
	Text      string `json:"text"`
	Device    string `json:"device,omitempty"`
	CommandID string `json:"command_id,omitempty"`
}

type robotAPIStopRequest struct {
	Device    string `json:"device,omitempty"`
	CommandID string `json:"command_id,omitempty"`
	Reason    string `json:"reason,omitempty"`
}

func (g *GatewayService) configuredRobotRuntime() (*operationstools.RobotRuntimeHub, error) {
	if g == nil || g.config == nil {
		return nil, fmt.Errorf("robot runtime is unavailable")
	}
	if !g.config.ToolsConfig.Enabled || !g.config.ToolsConfig.Robot.Enabled {
		return nil, fmt.Errorf("robot runtime is disabled")
	}
	return operationstools.RobotRuntimeHubFor(g.config.Workspace, g.config.ToolsConfig.Robot), nil
}

func (g *GatewayService) allowRobotAPIRequest(r *http.Request) bool {
	// Robot control is a sensitive local capability. Reuse the stricter debug
	// boundary instead of the gateway's legacy broad API behavior.
	return g != nil && g.allowSensitiveDebugRequest(r)
}

func writeRobotAPIJSON(w http.ResponseWriter, status int, value interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeRobotAPIError(w http.ResponseWriter, status int, err error) {
	message := "robot request failed"
	if err != nil {
		message = err.Error()
	}
	writeRobotAPIJSON(w, status, map[string]interface{}{"ok": false, "error": message})
}

func decodeRobotAPIJSON(w http.ResponseWriter, r *http.Request, target interface{}) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxRobotAPIRequestBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("invalid JSON body: %w", err)
	}
	var trailing interface{}
	if err := decoder.Decode(&trailing); err != io.EOF {
		return fmt.Errorf("request body must contain exactly one JSON object")
	}
	return nil
}

func robotAPIExecutionStatus(err error) int {
	if err == nil {
		return http.StatusOK
	}
	message := strings.ToLower(err.Error())
	switch {
	case strings.Contains(message, "allow_motion") || strings.Contains(message, "motion is disabled"):
		return http.StatusForbidden
	case strings.Contains(message, "busy with command"):
		return http.StatusConflict
	case strings.Contains(message, "audit record could not be persisted") || strings.Contains(message, "driver"):
		return http.StatusServiceUnavailable
	default:
		return http.StatusBadRequest
	}
}

func (g *GatewayService) handleRobotStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeRobotAPIError(w, http.StatusMethodNotAllowed, fmt.Errorf("GET required"))
		return
	}
	if !g.allowRobotAPIRequest(r) {
		writeRobotAPIError(w, http.StatusForbidden, fmt.Errorf("robot API requires loopback same-origin access or a valid X-API-Key"))
		return
	}
	hub, err := g.configuredRobotRuntime()
	if err != nil {
		writeRobotAPIError(w, http.StatusServiceUnavailable, err)
		return
	}
	status, err := hub.Status(r.Context(), r.URL.Query().Get("device"))
	if err != nil {
		writeRobotAPIError(w, robotAPIExecutionStatus(err), err)
		return
	}
	writeRobotAPIJSON(w, http.StatusOK, status)
}

func (g *GatewayService) handleRobotCommand(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeRobotAPIError(w, http.StatusMethodNotAllowed, fmt.Errorf("POST required"))
		return
	}
	if !g.allowRobotAPIRequest(r) {
		writeRobotAPIError(w, http.StatusForbidden, fmt.Errorf("robot API requires loopback same-origin access or a valid X-API-Key"))
		return
	}
	hub, err := g.configuredRobotRuntime()
	if err != nil {
		writeRobotAPIError(w, http.StatusServiceUnavailable, err)
		return
	}
	var request robotAPICommandRequest
	if err := decodeRobotAPIJSON(w, r, &request); err != nil {
		writeRobotAPIError(w, http.StatusBadRequest, err)
		return
	}
	result, err := operationstools.ExecuteRobotQuickCommand(r.Context(), hub, request.Text, request.Device, request.CommandID)
	if err != nil {
		writeRobotAPIError(w, robotAPIExecutionStatus(err), err)
		return
	}
	writeRobotAPIJSON(w, http.StatusAccepted, result)
}

func (g *GatewayService) handleRobotStop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeRobotAPIError(w, http.StatusMethodNotAllowed, fmt.Errorf("POST required"))
		return
	}
	if !g.allowRobotAPIRequest(r) {
		writeRobotAPIError(w, http.StatusForbidden, fmt.Errorf("robot API requires loopback same-origin access or a valid X-API-Key"))
		return
	}
	hub, err := g.configuredRobotRuntime()
	if err != nil {
		writeRobotAPIError(w, http.StatusServiceUnavailable, err)
		return
	}
	var request robotAPIStopRequest
	if err := decodeRobotAPIJSON(w, r, &request); err != nil {
		writeRobotAPIError(w, http.StatusBadRequest, err)
		return
	}
	receipt, err := hub.Stop(r.Context(), map[string]interface{}{
		"device": request.Device, "command_id": request.CommandID, "reason": request.Reason,
	})
	if err != nil {
		writeRobotAPIError(w, robotAPIExecutionStatus(err), err)
		return
	}
	writeRobotAPIJSON(w, http.StatusOK, receipt)
}

func searchKnowledgeFTSForTenant(db *gorm.DB, tenantID, query string, limit int) []knowledgetools.KnowledgeResult {
	if db == nil {
		return nil
	}
	escapedQuery := strings.ReplaceAll(query, "\"", "\"\"")
	var results []knowledgetools.KnowledgeResult
	db.Raw(`SELECT k.title, k.summary as content, bm25(knowledge_fts, 0.0, 1.0, 0.5) as score
		FROM knowledges k
		JOIN knowledge_fts ON k.id = knowledge_fts.rowid
		WHERE knowledge_fts MATCH ? AND k.tenant_id = ?
		ORDER BY score LIMIT ?`, escapedQuery, memoryTenantID(tenantID), limit).Scan(&results)
	return results
}

// handleKnowledgeList returns all knowledge entries for a tenant
func (g *GatewayService) handleKnowledgeList(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if g.db == nil {
		json.NewEncoder(w).Encode(map[string]interface{}{"entries": []interface{}{}})
		return
	}
	var entries []memorycore.Knowledge
	g.db.Order("updated_at DESC").Limit(200).Find(&entries)
	json.NewEncoder(w).Encode(map[string]interface{}{"entries": entries})
}

// handleKnowledgeSave creates or updates a knowledge entry
func (g *GatewayService) handleKnowledgeSave(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if g.db == nil {
		http.Error(w, `{"error":"database not available"}`, 500)
		return
	}
	var body struct {
		ID       uint     `json:"id"`
		Title    string   `json:"title"`
		Content  string   `json:"content"`
		Category string   `json:"category"`
		Tags     []string `json:"tags"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, `{"error":"invalid json"}`, 400)
		return
	}
	if body.Title == "" || body.Content == "" {
		http.Error(w, `{"error":"title and content required"}`, 400)
		return
	}

	if body.ID > 0 {
		// Update
		var entry memorycore.Knowledge
		if err := g.db.First(&entry, body.ID).Error; err == nil {
			tagsJSON, _ := json.Marshal(body.Tags)
			g.db.Model(&entry).Updates(map[string]interface{}{
				"title": body.Title, "summary": body.Content,
				"category": body.Category, "tags": datatypes.JSON(tagsJSON),
				"updated_at": time.Now(),
			})
			json.NewEncoder(w).Encode(map[string]interface{}{"ok": true, "id": entry.ID})
			return
		}
	}

	// Create
	tagsJSON, _ := json.Marshal(body.Tags)
	entry := &memorycore.Knowledge{
		Title:      body.Title,
		Summary:    body.Content,
		Category:   body.Category,
		Tags:       datatypes.JSON(tagsJSON),
		Importance: 5,
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}
	g.db.Create(entry)
	json.NewEncoder(w).Encode(map[string]interface{}{"ok": true, "id": entry.ID})
}

// handleKnowledgeDelete deletes a knowledge entry
func (g *GatewayService) handleKnowledgeDelete(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if g.db == nil {
		http.Error(w, `{"error":"db not available"}`, 500)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/api/knowledge/")
	if r.Method == "DELETE" {
		g.db.Delete(&memorycore.Knowledge{}, id)
		json.NewEncoder(w).Encode(map[string]interface{}{"ok": true})
		return
	}
	http.Error(w, `{"error":"method not allowed"}`, 405)
}

// handleKnowledgeUpload handles file upload for knowledge base
func (g *GatewayService) handleKnowledgeUpload(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if r.Method != "POST" {
		http.Error(w, `{"error":"POST only"}`, 405)
		return
	}

	r.ParseMultipartForm(32 << 20) // 32MB
	file, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, `{"error":"no file uploaded"}`, 400)
		return
	}
	defer file.Close()

	data, err := io.ReadAll(file)
	if err != nil {
		http.Error(w, `{"error":"read failed"}`, 500)
		return
	}

	title := r.FormValue("title")
	if title == "" {
		title = strings.TrimSuffix(header.Filename, filepath.Ext(header.Filename))
	}
	category := r.FormValue("category")

	// Save to local knowledge dir
	knowledgeDir := "knowledge"
	os.MkdirAll(knowledgeDir, 0755)
	filename := fmt.Sprintf("%s_%d%s", sanitizeFilename(title), time.Now().Unix(), filepath.Ext(header.Filename))
	filePath := filepath.Join(knowledgeDir, filename)
	os.WriteFile(filePath, data, 0644)

	// Save to DB
	if g.db != nil {
		content := string(data)
		if len(content) > 10000 {
			content = content[:10000]
		}
		entry := &memorycore.Knowledge{
			Title:      title,
			Summary:    content,
			Category:   category,
			Tags:       datatypes.JSON(fmt.Sprintf(`["file","%s"]`, filepath.Ext(header.Filename))),
			Importance: 5,
			CreatedAt:  time.Now(),
			UpdatedAt:  time.Now(),
		}
		g.db.Create(entry)
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"ok": true, "title": title, "file": filePath,
	})
}

func sanitizeFilename(name string) string {
	name = strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			return r
		}
		return '_'
	}, name)
	if len(name) > 50 {
		name = name[:50]
	}
	return name
}
