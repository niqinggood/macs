package llmapp

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"mac/internal/fsutil"
	"mac/internal/projectctx"
	"mac/internal/toolcontract"
	contenttools "mac/internal/tools/content"
)

const (
	llmAppEngineNative       = "native"
	llmAppWorkflowPrompt     = "prompt"
	llmAppWorkflowBatch      = "batch"
	llmAppWorkflowPDFPages   = "pdf_pages"
	llmAppWorkflowGraph      = "graph"
	llmAppLifecycleAttached  = "attached"
	llmAppLifecycleDetached  = "detached"
	llmAppStatusQueued       = "queued"
	llmAppStatusRunning      = "running"
	llmAppStatusPausing      = "pausing"
	llmAppStatusPaused       = "paused"
	llmAppStatusStopping     = "stopping"
	llmAppStatusStopped      = "stopped"
	llmAppStatusCompleted    = "completed"
	llmAppStatusDegraded     = "degraded"
	llmAppStatusFailed       = "failed"
	llmAppHealthHealthy      = "healthy"
	llmAppHealthDegraded     = "degraded"
	llmAppHealthStalled      = "stalled"
	llmAppHealthBlocked      = "blocked"
	llmAppHealthUnhealthy    = "unhealthy"
	llmAppDefaultMaxItems    = 1000
	llmAppMaximumMaxItems    = 10000
	llmAppDefaultItemChars   = 40000
	llmAppMaximumItemChars   = 200000
	llmAppMaximumPromptChars = 200000
	llmAppMaximumConcurrency = 16
)

var (
	llmAppNamePattern  = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)
	llmAppRunIDPattern = regexp.MustCompile(`^llma_[a-z0-9]{20}$`)
)

// LLMAppHealthPolicy controls host-observable health without asking the child
// model to manage its own lifecycle. API retries remain owned by the model
// caller; validation retries and item failure limits are application concerns.
type LLMAppHealthPolicy struct {
	HeartbeatSeconds  int    `json:"heartbeat_seconds,omitempty"`
	StallSeconds      int    `json:"stall_seconds,omitempty"`
	MaxItemErrors     int    `json:"max_item_errors,omitempty"`
	OnUnhealthy       string `json:"on_unhealthy,omitempty"`
	ValidationRetries int    `json:"validation_retries,omitempty"`
}

// LLMAppDefinition is a declarative, prompt-small model application. Native
// applications are executed by MACS directly; future engines may adapt
// the same contract to LangGraph without changing control-plane semantics.
type LLMAppDefinition struct {
	Name            string                 `json:"name"`
	Description     string                 `json:"description,omitempty"`
	Engine          string                 `json:"engine"`
	Workflow        string                 `json:"workflow"`
	SystemPrompt    string                 `json:"system_prompt,omitempty"`
	PromptTemplate  string                 `json:"prompt_template"`
	PromptVariables map[string]string      `json:"prompt_variables,omitempty"`
	Model           string                 `json:"model,omitempty"`
	MaxTokens       int                    `json:"max_tokens,omitempty"`
	Temperature     float64                `json:"temperature,omitempty"`
	Concurrency     int                    `json:"concurrency,omitempty"`
	MaxItems        int                    `json:"max_items,omitempty"`
	MaxItemChars    int                    `json:"max_item_chars,omitempty"`
	OutputSchema    map[string]interface{} `json:"output_schema,omitempty"`
	Graph           *LLMAppGraphDefinition `json:"graph,omitempty"`
	Health          LLMAppHealthPolicy     `json:"health,omitempty"`
	Budget          LLMAppBudgetPolicy     `json:"budget,omitempty"`
	Revision        int                    `json:"revision"`
	Digest          string                 `json:"digest"`
	CreatedAt       time.Time              `json:"created_at"`
	UpdatedAt       time.Time              `json:"updated_at"`
}

type LLMAppProgress struct {
	Completed int     `json:"completed"`
	Total     int     `json:"total"`
	Percent   float64 `json:"percent"`
}

type LLMAppHealth struct {
	Status            string    `json:"status"`
	Reason            string    `json:"reason,omitempty"`
	RecommendedAction string    `json:"recommended_action,omitempty"`
	Evidence          []string  `json:"evidence,omitempty"`
	EvaluatedAt       time.Time `json:"evaluated_at"`
}

type LLMAppUsage struct {
	ModelCalls            int     `json:"model_calls"`
	InputTokens           int     `json:"input_tokens,omitempty"`
	OutputTokens          int     `json:"output_tokens,omitempty"`
	EstimatedInputTokens  int     `json:"estimated_input_tokens"`
	EstimatedOutputTokens int     `json:"estimated_output_tokens"`
	UnknownModelCalls     int     `json:"unknown_model_calls,omitempty"`
	CostUSD               float64 `json:"cost_usd,omitempty"`
	LastProviderRequestID string  `json:"last_provider_request_id,omitempty"`
}

// LLMAppRunRecord is the public, durable task status. Raw input and full item
// outputs live in private files beside the record and are not duplicated here.
type LLMAppRunRecord struct {
	RunID             string               `json:"run_id"`
	AppName           string               `json:"app_name"`
	AppRevision       int                  `json:"app_revision"`
	AppDigest         string               `json:"app_digest"`
	PlanRevision      int                  `json:"plan_revision"`
	PlanDigest        string               `json:"plan_digest"`
	ParentTaskID      string               `json:"parent_task_id,omitempty"`
	Lifecycle         string               `json:"lifecycle"`
	IdempotencyDigest string               `json:"idempotency_digest,omitempty"`
	OwnerTenantID     string               `json:"owner_tenant_id,omitempty"`
	OwnerSubjectID    string               `json:"owner_subject_id,omitempty"`
	ExecutorBindingID string               `json:"executor_binding_id,omitempty"`
	Status            string               `json:"status"`
	Phase             string               `json:"phase,omitempty"`
	Progress          LLMAppProgress       `json:"progress"`
	Health            LLMAppHealth         `json:"health"`
	CurrentItem       string               `json:"current_item,omitempty"`
	Checkpoint        string               `json:"checkpoint,omitempty"`
	ItemErrors        int                  `json:"item_errors"`
	Usage             LLMAppUsage          `json:"usage"`
	Budget            LLMAppBudgetSnapshot `json:"budget"`
	ResultPath        string               `json:"result_path,omitempty"`
	PartialResultPath string               `json:"partial_result_path,omitempty"`
	EventPath         string               `json:"event_path,omitempty"`
	CommandPath       string               `json:"command_path,omitempty"`
	EventSequence     int64                `json:"event_sequence"`
	Error             string               `json:"error,omitempty"`
	CreatedAt         time.Time            `json:"created_at"`
	StartedAt         *time.Time           `json:"started_at,omitempty"`
	CompletedAt       *time.Time           `json:"completed_at,omitempty"`
	LastHeartbeatAt   time.Time            `json:"last_heartbeat_at"`
	LastProgressAt    time.Time            `json:"last_progress_at"`
	WorkerID          string               `json:"worker_id,omitempty"`
	LeaseExpiresAt    *time.Time           `json:"lease_expires_at,omitempty"`
}

type llmAppRunRequest struct {
	Input string        `json:"input,omitempty"`
	Items []interface{} `json:"items,omitempty"`
	File  string        `json:"file,omitempty"`
}

type llmAppWorkItem struct {
	Index    int               `json:"index"`
	ID       string            `json:"id"`
	Text     string            `json:"text"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

type LLMAppItemResult struct {
	Index              int               `json:"index"`
	ID                 string            `json:"id"`
	Output             string            `json:"output,omitempty"`
	Value              interface{}       `json:"value,omitempty"`
	Error              string            `json:"error,omitempty"`
	ModelCalls         int               `json:"model_calls"`
	InputTokens        int               `json:"input_tokens,omitempty"`
	OutputTokens       int               `json:"output_tokens,omitempty"`
	ProviderRequestIDs []string          `json:"provider_request_ids,omitempty"`
	PlanRevision       int               `json:"plan_revision"`
	Metadata           map[string]string `json:"metadata,omitempty"`
}

type llmAppRunResult struct {
	RunID       string             `json:"run_id"`
	AppName     string             `json:"app_name"`
	Status      string             `json:"status"`
	Completed   int                `json:"completed"`
	Total       int                `json:"total"`
	ItemErrors  int                `json:"item_errors"`
	Results     []LLMAppItemResult `json:"results"`
	Output      interface{}        `json:"output,omitempty"`
	CompletedAt time.Time          `json:"completed_at"`
}

type ModelCaller func(
	ctx context.Context,
	model string,
	messages []map[string]interface{},
	maxTokens int,
	temperature float64,
) (string, error)

type llmAppModelCaller = ModelCaller

// Identity contains only the host-authenticated fields needed to isolate
// durable llm_app runs. The host owns how this identity is stored in context.
type Identity struct {
	TenantID     string
	SubjectID    string
	ControlPlane bool
}

type IdentityResolver func(context.Context) (Identity, bool)
type PathGuard func(context.Context, string, string) error

type llmAppPDFExtractor func(
	ctx context.Context,
	path string,
	progress func(current, total int),
) ([]llmAppWorkItem, error)

type llmAppManagedRun struct {
	mu                   sync.Mutex
	persistMu            sync.Mutex
	commandMu            sync.Mutex
	record               LLMAppRunRecord
	plan                 LLMAppRunPlan
	cancel               context.CancelFunc
	done                 chan struct{}
	pauseRequested       bool
	resumePhase          string
	resumeCh             chan struct{}
	workChangedCh        chan struct{}
	eventChangedCh       chan struct{}
	pendingItems         []llmAppWorkItem
	itemPlanRevisions    map[string]int
	inFlightItems        int
	queueOpen            bool
	workflow             string
	maxItems             int
	maxItemChars         int
	commandReceipts      map[string]LLMAppCommandReceipt
	budget               LLMAppBudgetPolicy
	reservedOutputTokens int
	reservedCostUSD      float64
	leaseToken           string
	workerStarting       bool
	workerActive         bool
	recoveryRetryPending bool
}

type LLMAppManager struct {
	workspace        string
	root             string
	logger           *log.Logger
	identityResolver IdentityResolver
	startMu          sync.Mutex
	runtimeMu        sync.RWMutex
	runtime          Runtime
	runtimes         map[string]Runtime
	workerID         string
	mu               sync.RWMutex
	runs             map[string]*llmAppManagedRun
}

var llmAppManagers = struct {
	sync.Mutex
	items map[string]*LLMAppManager
}{items: make(map[string]*LLMAppManager)}

func llmAppManagerForWorkspace(workspace string, logger *log.Logger, identityResolver IdentityResolver) *LLMAppManager {
	workspace = projectctx.NormalizeWorkspace(workspace)
	key := projectctx.IdentityKey(workspace)
	llmAppManagers.Lock()
	defer llmAppManagers.Unlock()
	if manager := llmAppManagers.items[key]; manager != nil {
		return manager
	}
	manager := &LLMAppManager{
		workspace:        workspace,
		root:             filepath.Join(workspace, ".openkitty", "llm_apps"),
		logger:           logger,
		identityResolver: identityResolver,
		workerID:         newLLMAppWorkerID(),
		runtimes:         make(map[string]Runtime),
		runs:             make(map[string]*llmAppManagedRun),
	}
	llmAppManagers.items[key] = manager
	return manager
}

type LLMAppTool struct {
	workspace        string
	logger           *log.Logger
	modelCaller      llmAppModelCaller
	runtime          Runtime
	pdfExtractor     llmAppPDFExtractor
	pathGuard        PathGuard
	identityResolver IdentityResolver
}

func NewTool(
	workspace string,
	logger *log.Logger,
	caller ModelCaller,
	pathGuard PathGuard,
	identityResolver IdentityResolver,
) *LLMAppTool {
	return NewToolWithRuntime(
		workspace,
		logger,
		runtimeFromLegacyModelCaller(caller),
		pathGuard,
		identityResolver,
	)
}

// NewToolWithRuntime constructs llm_app with usage-aware model calls and the
// host's existing tool registry bridge. New execution features use Runtime;
// NewTool remains source-compatible for existing integrations and tests.
func NewToolWithRuntime(
	workspace string,
	logger *log.Logger,
	runtime Runtime,
	pathGuard PathGuard,
	identityResolver IdentityResolver,
) *LLMAppTool {
	runtime = normalizeRuntime(runtime)
	legacyCaller := llmAppModelCaller(nil)
	if runtime.ModelCaller != nil {
		legacyCaller = func(
			ctx context.Context,
			model string,
			messages []map[string]interface{},
			maxTokens int,
			temperature float64,
		) (string, error) {
			response, err := runtime.ModelCaller(ctx, model, messages, maxTokens, temperature)
			return response.Content, err
		}
	}
	tool := &LLMAppTool{
		workspace:        workspace,
		logger:           logger,
		modelCaller:      legacyCaller,
		runtime:          runtime,
		pdfExtractor:     runtime.pdfExtractor,
		pathGuard:        pathGuard,
		identityResolver: identityResolver,
	}
	manager := llmAppManagerForWorkspace(workspace, logger, identityResolver)
	manager.configureRuntime(runtime)
	return tool
}

func newLLMAppToolForTest(
	workspace string,
	caller llmAppModelCaller,
	extractor llmAppPDFExtractor,
) *LLMAppTool {
	if extractor == nil {
		extractor = extractLLMAppPDFPages
	}
	runtime := Runtime{pdfExtractor: extractor}
	if caller != nil {
		runtime.ModelCaller = func(
			ctx context.Context,
			model string,
			messages []map[string]interface{},
			maxTokens int,
			temperature float64,
		) (ModelResponse, error) {
			content, err := caller(ctx, model, messages, maxTokens, temperature)
			return ModelResponse{Content: content}, err
		}
	}
	tool := &LLMAppTool{
		workspace: workspace, modelCaller: caller, pdfExtractor: extractor,
		runtime: runtime,
		logger:  log.New(os.Stderr, "[llm_app_test] ", 0),
	}
	llmAppManagerForWorkspace(workspace, tool.logger, nil).configureRuntime(runtime)
	return tool
}

func (t *LLMAppTool) Name() string { return "llm_app" }

func (t *LLMAppTool) Description() string {
	return "Create and manage prompt-small AI applications that run independently of the general agent prompt. " +
		"Supports one-shot, batch/row, PDF page, and source/model/tool/validate/reduce/sink graph workflows with " +
		"durable idempotency, budgets, full JSON Schema outputs, crash recovery, progress, versioned replanning, " +
		"events, pause/resume, stop, and durable results. Tool graph nodes reuse the existing MACS Tool Registry."
}

func (t *LLMAppTool) MaxResultSize() int { return 50000 }

func (t *LLMAppTool) IsReadOnlyForCall(args map[string]interface{}) bool {
	action := strings.ToLower(strings.TrimSpace(llmAppStringParam(args, "action")))
	if action == "health" {
		_, hasDecision := args["decision"]
		return !hasDecision
	}
	switch action {
	case "list", "inspect", "status", "events", "result":
		return true
	default:
		return false
	}
}

func (t *LLMAppTool) SandboxCapabilitiesForCall(args map[string]interface{}) toolcontract.SandboxCapabilities {
	action := strings.ToLower(strings.TrimSpace(llmAppStringParam(args, "action")))
	return toolcontract.SandboxCapabilities{Network: action == "start" || action == "resume" || action == "command"}
}

func (t *LLMAppTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"action": map[string]interface{}{
				"type": "string",
				"enum": []string{"create", "list", "inspect", "start", "command", "events", "status", "health", "pause", "resume", "stop", "result"},
			},
			"name": map[string]interface{}{
				"type": "string", "description": "Application name for create, inspect, or start",
			},
			"definition": map[string]interface{}{
				"type": "object", "description": "For create: app definition including name/workflow and prompt or graph, plus optional model/limits/output_schema/health/budget",
			},
			"run_id": map[string]interface{}{
				"type": "string", "description": "Managed run ID for status, health, pause, resume, stop, or result",
			},
			"input": map[string]interface{}{
				"type": "string", "description": "Input for a prompt workflow, or shared context for batch/PDF items",
			},
			"items": map[string]interface{}{
				"type": "array", "description": "Items for a batch workflow; strings and JSON values are accepted",
			},
			"file": map[string]interface{}{
				"type": "string", "description": "PDF path for a pdf_pages workflow",
			},
			"idempotency_key": map[string]interface{}{
				"type": "string", "description": "Optional durable start idempotency key; the same key and payload return the original run",
			},
			"lifecycle": map[string]interface{}{
				"type": "string", "enum": []string{llmAppLifecycleAttached, llmAppLifecycleDetached},
				"description": "detached (compatibility default) continues independently; attached follows parent cancellation",
			},
			"decision": map[string]interface{}{
				"type": "object", "description": "Optional health judgment: status, reason, recommended_action, and evidence",
			},
			"command": map[string]interface{}{
				"type": "object", "description": "For command: a versioned runtime instruction",
				"properties": map[string]interface{}{
					"command_id": map[string]interface{}{
						"type": "string", "description": "Optional idempotency key; reuse the same key when retrying one command",
					},
					"type": map[string]interface{}{
						"type": "string", "enum": []string{"replan", "enqueue_items", "pause", "resume", "stop"},
					},
					"expected_plan_revision": map[string]interface{}{
						"type": "integer", "description": "Optional optimistic concurrency guard for the current run plan",
					},
					"reason": map[string]interface{}{
						"type": "string", "description": "Auditable reason for the command",
					},
					"changes": map[string]interface{}{
						"type": "object", "description": "For replan: fields applied only to work items not yet claimed",
						"properties": map[string]interface{}{
							"system_prompt":   map[string]interface{}{"type": "string"},
							"prompt_template": map[string]interface{}{"type": "string"},
							"prompt_variables": map[string]interface{}{
								"type": "object", "additionalProperties": map[string]interface{}{"type": "string"},
							},
							"model":         map[string]interface{}{"type": "string"},
							"max_tokens":    map[string]interface{}{"type": "integer"},
							"temperature":   map[string]interface{}{"type": "number"},
							"concurrency":   map[string]interface{}{"type": "integer"},
							"output_schema": map[string]interface{}{"type": "object"},
						},
					},
					"items": map[string]interface{}{
						"type": "array", "description": "For enqueue_items: new batch values",
					},
				},
				"required": []string{"type"},
			},
			"after_seq": map[string]interface{}{
				"type": "integer", "minimum": 0, "description": "For events: return events after this sequence number",
			},
			"limit": map[string]interface{}{
				"type": "integer", "minimum": 1, "maximum": 100, "description": "For events: maximum events to return (default 50)",
			},
			"wait_seconds": map[string]interface{}{
				"type": "integer", "minimum": 0, "maximum": 30, "description": "For events: long-poll for a new event, up to 30 seconds",
			},
			"event_types": map[string]interface{}{
				"type": "array", "items": map[string]interface{}{"type": "string"}, "description": "For events: optional event type filter",
			},
			"include_outputs": map[string]interface{}{
				"type": "boolean", "description": "For result: include item outputs in the response; defaults to false",
			},
		},
		"required": []string{"action"},
	}
}

func (t *LLMAppTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	action := strings.ToLower(strings.TrimSpace(llmAppStringParam(params, "action")))
	if action == "" {
		return "", errors.New("action is required")
	}
	workspace := strings.TrimSpace(projectctx.ResolvedWorkspace(ctx))
	if workspace == "" {
		workspace = t.workspace
	}
	workspace = projectctx.NormalizeWorkspace(workspace)
	manager := llmAppManagerForWorkspace(workspace, t.logger, t.identityResolver)
	manager.configureRuntime(t.runtime)

	switch action {
	case "create":
		definition, err := decodeLLMAppDefinition(params)
		if err != nil {
			return "", err
		}
		created, err := manager.createDefinition(definition)
		if err != nil {
			return "", err
		}
		return marshalLLMAppJSON(map[string]interface{}{
			"status": "created", "application": created,
		})
	case "list":
		definitions, err := manager.listDefinitions()
		if err != nil {
			return "", err
		}
		return marshalLLMAppJSON(map[string]interface{}{
			"count": len(definitions), "applications": definitions,
		})
	case "inspect":
		definition, err := manager.loadDefinition(llmAppStringParam(params, "name"))
		if err != nil {
			return "", err
		}
		return marshalLLMAppJSON(definition)
	case "start":
		if t.runtime.ModelCaller == nil {
			return "", errors.New("llm_app model caller is unavailable")
		}
		definition, err := manager.loadDefinition(llmAppStringParam(params, "name"))
		if err != nil {
			return "", err
		}
		request, err := t.decodeRunRequest(ctx, workspace, definition, params)
		if err != nil {
			return "", err
		}
		options, err := decodeLLMAppStartOptions(params)
		if err != nil {
			return "", err
		}
		record, replay, err := manager.startRun(ctx, definition, request, t.runtime, options)
		if err != nil {
			return "", err
		}
		return marshalLLMAppJSON(map[string]interface{}{
			"run_id": record.RunID, "status": record.Status, "application": record.AppName,
			"plan_revision": record.PlanRevision, "event_sequence": record.EventSequence,
			"lifecycle": record.Lifecycle, "idempotent_replay": replay,
			"status_hint": "Use action=events with after_seq for event-driven monitoring; use status/health for snapshots and command for controlled replanning.",
		})
	case "status":
		record, run, err := manager.authorizedRun(ctx, llmAppStringParam(params, "run_id"))
		if err != nil {
			return "", err
		}
		if run != nil {
			record = run.snapshot()
		}
		return marshalLLMAppJSON(record)
	case "events":
		record, run, err := manager.authorizedRun(ctx, llmAppStringParam(params, "run_id"))
		if err != nil {
			return "", err
		}
		return manager.readEvents(ctx, run, record, params)
	case "command":
		_, run, err := manager.authorizedRun(ctx, llmAppStringParam(params, "run_id"))
		if err != nil {
			return "", err
		}
		command, err := decodeLLMAppCommand(params["command"])
		if err != nil {
			return "", err
		}
		receipt, err := manager.applyCommand(run, command)
		if err != nil {
			return "", err
		}
		return marshalLLMAppJSON(map[string]interface{}{
			"command": receipt, "run": run.snapshot(),
		})
	case "health":
		runID := llmAppStringParam(params, "run_id")
		_, run, err := manager.authorizedRun(ctx, runID)
		if err != nil {
			return "", err
		}
		if raw, ok := params["decision"]; ok && raw != nil {
			decision, err := decodeLLMAppHealth(raw)
			if err != nil {
				return "", err
			}
			if err := manager.applyHealthDecision(run, decision); err != nil {
				return "", err
			}
		}
		record := manager.evaluateHealth(run)
		return marshalLLMAppJSON(map[string]interface{}{
			"run_id": record.RunID, "status": record.Status, "phase": record.Phase,
			"progress": record.Progress, "health": record.Health,
			"last_heartbeat_at": record.LastHeartbeatAt,
		})
	case "pause":
		_, run, err := manager.authorizedRun(ctx, llmAppStringParam(params, "run_id"))
		if err != nil {
			return "", err
		}
		if err := manager.pauseRun(run); err != nil {
			return "", err
		}
		return marshalLLMAppJSON(run.snapshot())
	case "resume":
		_, run, err := manager.authorizedRun(ctx, llmAppStringParam(params, "run_id"))
		if err != nil {
			return "", err
		}
		if err := manager.resumeRun(run); err != nil {
			return "", err
		}
		return marshalLLMAppJSON(run.snapshot())
	case "stop":
		_, run, err := manager.authorizedRun(ctx, llmAppStringParam(params, "run_id"))
		if err != nil {
			return "", err
		}
		record, err := manager.stopRun(run, "stopped by MACS")
		if err != nil {
			return "", err
		}
		return marshalLLMAppJSON(record)
	case "result":
		record, run, err := manager.authorizedRun(ctx, llmAppStringParam(params, "run_id"))
		if err != nil {
			return "", err
		}
		if run != nil {
			record = run.snapshot()
		}
		return manager.readResult(record, llmAppBoolParam(params, "include_outputs"))
	default:
		return "", fmt.Errorf("unsupported llm_app action %q", action)
	}
}

func decodeLLMAppDefinition(params map[string]interface{}) (LLMAppDefinition, error) {
	raw := params["definition"]
	if raw == nil {
		raw = params
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return LLMAppDefinition{}, fmt.Errorf("encode llm_app definition: %w", err)
	}
	var definition LLMAppDefinition
	if err := json.Unmarshal(data, &definition); err != nil {
		return LLMAppDefinition{}, fmt.Errorf("decode llm_app definition: %w", err)
	}
	if definition.Name == "" {
		definition.Name = llmAppStringParam(params, "name")
	}
	if err := normalizeLLMAppDefinition(&definition); err != nil {
		return LLMAppDefinition{}, err
	}
	return definition, nil
}

func normalizeLLMAppDefinition(definition *LLMAppDefinition) error {
	if definition == nil {
		return errors.New("application definition is required")
	}
	definition.Name = strings.ToLower(strings.TrimSpace(definition.Name))
	if !llmAppNamePattern.MatchString(definition.Name) {
		return errors.New("application name must match ^[a-z][a-z0-9_-]{0,63}$")
	}
	definition.Description = strings.TrimSpace(definition.Description)
	definition.Engine = strings.ToLower(strings.TrimSpace(definition.Engine))
	if definition.Engine == "" {
		definition.Engine = llmAppEngineNative
	}
	if definition.Engine != llmAppEngineNative {
		return fmt.Errorf("engine %q is not executable yet; use native", definition.Engine)
	}
	definition.Workflow = strings.ToLower(strings.TrimSpace(definition.Workflow))
	if definition.Workflow == "" {
		definition.Workflow = llmAppWorkflowPrompt
	}
	switch definition.Workflow {
	case llmAppWorkflowPrompt, llmAppWorkflowBatch, llmAppWorkflowPDFPages, llmAppWorkflowGraph:
	default:
		return fmt.Errorf("unsupported workflow %q", definition.Workflow)
	}
	definition.SystemPrompt = strings.TrimSpace(definition.SystemPrompt)
	definition.PromptTemplate = strings.TrimSpace(definition.PromptTemplate)
	if definition.PromptTemplate == "" {
		if definition.Workflow == llmAppWorkflowGraph {
			// The mutable run plan remains structurally valid even though graph
			// model nodes own their individual prompts.
			definition.PromptTemplate = "{{item}}"
		} else {
			return errors.New("prompt_template is required")
		}
	}
	if len(definition.SystemPrompt)+len(definition.PromptTemplate) > llmAppMaximumPromptChars {
		return fmt.Errorf("combined application prompt exceeds %d characters", llmAppMaximumPromptChars)
	}
	promptVariables, err := normalizeLLMAppPromptVariables(definition.PromptVariables)
	if err != nil {
		return err
	}
	definition.PromptVariables = promptVariables
	definition.Model = strings.TrimSpace(definition.Model)
	if definition.MaxTokens <= 0 {
		definition.MaxTokens = 1024
	}
	if definition.MaxTokens > 32768 {
		definition.MaxTokens = 32768
	}
	if definition.Temperature < 0 {
		definition.Temperature = 0
	}
	if definition.Temperature > 2 {
		definition.Temperature = 2
	}
	if definition.Concurrency <= 0 {
		definition.Concurrency = 1
	}
	if definition.Concurrency > llmAppMaximumConcurrency {
		definition.Concurrency = llmAppMaximumConcurrency
	}
	if definition.MaxItems <= 0 {
		definition.MaxItems = llmAppDefaultMaxItems
	}
	if definition.MaxItems > llmAppMaximumMaxItems {
		definition.MaxItems = llmAppMaximumMaxItems
	}
	if definition.MaxItemChars <= 0 {
		definition.MaxItemChars = llmAppDefaultItemChars
	}
	if definition.MaxItemChars > llmAppMaximumItemChars {
		definition.MaxItemChars = llmAppMaximumItemChars
	}
	if definition.Health.HeartbeatSeconds <= 0 {
		definition.Health.HeartbeatSeconds = 15
	}
	if definition.Health.HeartbeatSeconds < 5 {
		definition.Health.HeartbeatSeconds = 5
	}
	if definition.Health.StallSeconds <= 0 {
		definition.Health.StallSeconds = 360
	}
	if definition.Health.MaxItemErrors <= 0 {
		definition.Health.MaxItemErrors = 3
	}
	definition.Health.OnUnhealthy = strings.ToLower(strings.TrimSpace(definition.Health.OnUnhealthy))
	if definition.Health.OnUnhealthy == "" {
		definition.Health.OnUnhealthy = "fail"
	}
	switch definition.Health.OnUnhealthy {
	case "fail", "pause", "continue":
	default:
		return fmt.Errorf("unsupported health.on_unhealthy %q", definition.Health.OnUnhealthy)
	}
	if definition.Health.ValidationRetries < 0 {
		definition.Health.ValidationRetries = 0
	}
	if definition.Health.ValidationRetries > 2 {
		definition.Health.ValidationRetries = 2
	}
	if err := validateLLMAppJSONSchema(definition.OutputSchema); err != nil {
		return err
	}
	if definition.Workflow == llmAppWorkflowGraph {
		if err := normalizeLLMAppGraph(definition); err != nil {
			return err
		}
	}
	budgetUnits := definition.MaxItems
	if definition.Workflow == llmAppWorkflowPrompt {
		budgetUnits = 1
	} else if definition.Workflow == llmAppWorkflowGraph && definition.Graph != nil {
		budgetUnits = 0
		for _, node := range definition.Graph.Nodes {
			if node.Type == llmAppGraphModel {
				budgetUnits++
			}
		}
		if budgetUnits == 0 {
			budgetUnits = 1
		}
	}
	if err := normalizeLLMAppBudget(&definition.Budget, budgetUnits, definition.Health.ValidationRetries); err != nil {
		return err
	}
	return nil
}

func (m *LLMAppManager) createDefinition(definition LLMAppDefinition) (LLMAppDefinition, error) {
	if err := normalizeLLMAppDefinition(&definition); err != nil {
		return LLMAppDefinition{}, err
	}
	now := time.Now()
	if existing, err := m.loadDefinition(definition.Name); err == nil {
		definition.Revision = existing.Revision + 1
		definition.CreatedAt = existing.CreatedAt
	} else {
		definition.Revision = 1
		definition.CreatedAt = now
	}
	definition.UpdatedAt = now
	definition.Digest = ""
	digestData, err := json.Marshal(definition)
	if err != nil {
		return LLMAppDefinition{}, err
	}
	digest := sha256.Sum256(digestData)
	definition.Digest = hex.EncodeToString(digest[:])
	if err := writeLLMAppJSON(m.definitionPath(definition.Name), definition); err != nil {
		return LLMAppDefinition{}, err
	}
	return definition, nil
}

func (m *LLMAppManager) definitionPath(name string) string {
	return filepath.Join(m.root, "definitions", name+".json")
}

func (m *LLMAppManager) runDir(runID string) string {
	return filepath.Join(m.root, "runs", runID)
}

func (m *LLMAppManager) loadDefinition(name string) (LLMAppDefinition, error) {
	name = strings.ToLower(strings.TrimSpace(name))
	if !llmAppNamePattern.MatchString(name) {
		return LLMAppDefinition{}, errors.New("valid application name is required")
	}
	var definition LLMAppDefinition
	if err := readLLMAppJSON(m.definitionPath(name), &definition); err != nil {
		if os.IsNotExist(err) {
			return LLMAppDefinition{}, fmt.Errorf("llm_app %q not found", name)
		}
		return LLMAppDefinition{}, err
	}
	if err := normalizeLLMAppDefinition(&definition); err != nil {
		return LLMAppDefinition{}, fmt.Errorf("invalid stored llm_app %q: %w", name, err)
	}
	return definition, nil
}

func (m *LLMAppManager) listDefinitions() ([]LLMAppDefinition, error) {
	dir := filepath.Join(m.root, "definitions")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return []LLMAppDefinition{}, nil
		}
		return nil, err
	}
	definitions := make([]LLMAppDefinition, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(strings.ToLower(entry.Name()), ".json") {
			continue
		}
		name := strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name()))
		definition, err := m.loadDefinition(name)
		if err == nil {
			definitions = append(definitions, definition)
		}
	}
	sort.Slice(definitions, func(i, j int) bool { return definitions[i].Name < definitions[j].Name })
	return definitions, nil
}

func (t *LLMAppTool) decodeRunRequest(
	ctx context.Context,
	workspace string,
	definition LLMAppDefinition,
	params map[string]interface{},
) (llmAppRunRequest, error) {
	request := llmAppRunRequest{Input: llmAppStringParam(params, "input")}
	if rawItems, ok := params["items"].([]interface{}); ok {
		request.Items = rawItems
	}
	switch definition.Workflow {
	case llmAppWorkflowPrompt:
		if strings.TrimSpace(request.Input) == "" {
			return request, errors.New("input is required for prompt workflow")
		}
	case llmAppWorkflowBatch:
		if len(request.Items) == 0 {
			return request, errors.New("items are required for batch workflow")
		}
	case llmAppWorkflowPDFPages:
		file := strings.TrimSpace(llmAppStringParam(params, "file"))
		if file == "" {
			return request, errors.New("file is required for pdf_pages workflow")
		}
		if t.pathGuard != nil {
			if err := t.pathGuard(ctx, workspace, file); err != nil {
				return request, err
			}
		}
		if !filepath.IsAbs(file) {
			file = filepath.Join(workspace, file)
		}
		file = filepath.Clean(file)
		if !strings.EqualFold(filepath.Ext(file), ".pdf") {
			return request, errors.New("pdf_pages workflow requires a .pdf file")
		}
		info, err := os.Stat(file)
		if err != nil {
			return request, fmt.Errorf("inspect PDF: %w", err)
		}
		if !info.Mode().IsRegular() || info.Size() == 0 {
			return request, errors.New("PDF must be a non-empty regular file")
		}
		request.File = file
	case llmAppWorkflowGraph:
		requiresInput, requiresItems, requiresFile := false, false, false
		if definition.Graph != nil {
			for _, node := range definition.Graph.Nodes {
				if node.Type != llmAppGraphSource {
					continue
				}
				switch node.Source {
				case "input":
					requiresInput = true
				case "items":
					requiresItems = true
				case "file":
					requiresFile = true
				}
			}
		}
		if requiresInput && strings.TrimSpace(request.Input) == "" {
			return request, errors.New("input is required by the graph source")
		}
		if requiresItems && len(request.Items) == 0 {
			return request, errors.New("items are required by the graph source")
		}
		if requiresFile {
			file := strings.TrimSpace(llmAppStringParam(params, "file"))
			if file == "" {
				return request, errors.New("file is required by the graph source")
			}
			if t.pathGuard != nil {
				if err := t.pathGuard(ctx, workspace, file); err != nil {
					return request, err
				}
			}
			if !filepath.IsAbs(file) {
				file = filepath.Join(workspace, file)
			}
			file = filepath.Clean(file)
			info, err := os.Stat(file)
			if err != nil {
				return request, fmt.Errorf("inspect graph source file: %w", err)
			}
			if !info.Mode().IsRegular() || info.Size() == 0 {
				return request, errors.New("graph source file must be a non-empty regular file")
			}
			if info.Size() > 16*1024*1024 {
				return request, errors.New("graph source file exceeds the 16 MiB runtime limit")
			}
			request.File = file
		}
	}
	if len(request.Items) > definition.MaxItems {
		return request, fmt.Errorf("input has %d items; application limit is %d", len(request.Items), definition.MaxItems)
	}
	return request, nil
}

func (m *LLMAppManager) createRun(
	ctx context.Context,
	runID string,
	idempotencyDigest string,
	definition LLMAppDefinition,
	request llmAppRunRequest,
	runtime Runtime,
	options llmAppStartOptions,
) (LLMAppRunRecord, error) {
	if runtime.ModelCaller == nil {
		return LLMAppRunRecord{}, errors.New("model caller is required")
	}
	runtime = normalizeRuntime(runtime)
	now := time.Now()
	plan, err := newLLMAppRunPlan(definition, now)
	if err != nil {
		return LLMAppRunRecord{}, err
	}
	eventPath := filepath.Join(m.runDir(runID), "events.jsonl")
	commandPath := filepath.Join(m.runDir(runID), "commands.jsonl")
	record := LLMAppRunRecord{
		RunID: runID, AppName: definition.Name, AppRevision: definition.Revision,
		AppDigest: definition.Digest, PlanRevision: plan.Revision, PlanDigest: plan.Digest,
		ParentTaskID: taskIDFromContext(ctx), Lifecycle: options.Lifecycle,
		IdempotencyDigest: idempotencyDigest,
		ExecutorBindingID: runtime.BindingID,
		Status:            llmAppStatusQueued, Phase: "queued",
		Health:    LLMAppHealth{Status: llmAppHealthHealthy, EvaluatedAt: now},
		EventPath: eventPath, CommandPath: commandPath,
		CreatedAt: now, LastHeartbeatAt: now, LastProgressAt: now,
	}
	record.Budget = buildLLMAppBudgetSnapshot(definition.Budget, record.Usage, 0, 0, "")
	if identity, ok := m.resolveIdentity(ctx); ok {
		record.OwnerTenantID = identity.TenantID
		record.OwnerSubjectID = identity.SubjectID
	}
	runDir := m.runDir(runID)
	if err := os.MkdirAll(runDir, 0o700); err != nil {
		return LLMAppRunRecord{}, fmt.Errorf("create llm_app run directory: %w", err)
	}
	if err := writeLLMAppJSON(filepath.Join(runDir, "definition.json"), definition); err != nil {
		return LLMAppRunRecord{}, err
	}
	if err := writeLLMAppJSON(filepath.Join(runDir, "plan.json"), plan); err != nil {
		return LLMAppRunRecord{}, err
	}
	if err := writeLLMAppJSON(llmAppPlanRevisionPath(runDir, plan.Revision), plan); err != nil {
		return LLMAppRunRecord{}, err
	}
	if err := writeLLMAppJSON(filepath.Join(runDir, "request.json"), request); err != nil {
		return LLMAppRunRecord{}, err
	}
	if err := writeLLMAppJSON(filepath.Join(runDir, "run.json"), record); err != nil {
		return LLMAppRunRecord{}, err
	}

	runCtx, cancel := newLLMAppExecutionContext(ctx, options.Lifecycle, definition.Budget, now)
	run := &llmAppManagedRun{
		record: record, plan: plan, cancel: cancel, done: make(chan struct{}),
		workChangedCh: make(chan struct{}), eventChangedCh: make(chan struct{}), workflow: definition.Workflow,
		maxItems: definition.MaxItems, maxItemChars: definition.MaxItemChars,
		commandReceipts: make(map[string]LLMAppCommandReceipt), itemPlanRevisions: make(map[string]int),
		budget: definition.Budget, workerStarting: true,
	}
	m.mu.Lock()
	m.runs[runID] = run
	m.mu.Unlock()
	acquired, err := m.acquireRunLease(run, llmAppLeaseDuration(definition.Health.HeartbeatSeconds))
	if err != nil {
		cancel()
		run.mu.Lock()
		run.workerStarting = false
		run.mu.Unlock()
		m.mu.Lock()
		if m.runs[runID] == run {
			delete(m.runs, runID)
		}
		m.mu.Unlock()
		return LLMAppRunRecord{}, fmt.Errorf("acquire llm_app worker lease: %w", err)
	}
	if !acquired {
		cancel()
		run.mu.Lock()
		run.workerStarting = false
		run.mu.Unlock()
		m.mu.Lock()
		if m.runs[runID] == run {
			delete(m.runs, runID)
		}
		m.mu.Unlock()
		return LLMAppRunRecord{}, errors.New("llm_app run is already owned by another worker")
	}
	run.mu.Lock()
	run.workerStarting = false
	run.workerActive = true
	run.mu.Unlock()
	m.emitRunEvent(run, llmAppEventRunCreated, "worker", "llm_app run created", map[string]interface{}{
		"application": definition.Name, "app_revision": definition.Revision,
		"plan_revision": plan.Revision, "workflow": definition.Workflow,
	})
	go m.executeRun(runCtx, run, definition, request, runtime, false)
	return run.snapshot(), nil
}

func (m *LLMAppManager) executeRun(
	ctx context.Context,
	run *llmAppManagedRun,
	definition LLMAppDefinition,
	request llmAppRunRequest,
	runtime Runtime,
	recovered bool,
) {
	defer func() {
		_ = m.releaseRunLease(run)
		run.mu.Lock()
		done := run.done
		run.done = nil
		run.mu.Unlock()
		if done != nil {
			close(done)
		}
	}()
	now := time.Now()
	m.updateRun(run, func(record *LLMAppRunRecord) {
		if !run.pauseRequested && record.Status != llmAppStatusStopping {
			record.Status = llmAppStatusRunning
			record.Phase = "preparing"
		}
		if record.StartedAt == nil {
			record.StartedAt = &now
		}
		record.LastHeartbeatAt = now
		record.LastProgressAt = now
	})
	startedEvent := llmAppEventRunStarted
	startedMessage := "llm_app worker started"
	if recovered {
		startedEvent = llmAppEventRunRecovered
		startedMessage = "llm_app worker resumed from durable checkpoints"
	}
	m.emitRunEvent(run, startedEvent, "worker", startedMessage, map[string]interface{}{
		"workflow": definition.Workflow, "plan_revision": run.snapshot().PlanRevision,
	})
	heartbeatDone := make(chan struct{})
	defer close(heartbeatDone)
	go m.runHeartbeat(ctx, run, definition.Health.HeartbeatSeconds, heartbeatDone)

	var err error
	var graphOutput interface{}
	results := []LLMAppItemResult{}
	if definition.Workflow == llmAppWorkflowGraph {
		m.updateRun(run, func(record *LLMAppRunRecord) {
			record.Phase = "graph"
			record.CurrentItem = ""
			record.LastProgressAt = time.Now()
		})
		m.emitRunEvent(run, llmAppEventPhaseChanged, "worker", "application graph started", map[string]interface{}{
			"phase": "graph", "nodes": len(definition.Graph.Nodes),
		})
		graphOutput, err = m.executeGraph(ctx, run, definition, request, runtime)
	} else {
		var items []llmAppWorkItem
		switch definition.Workflow {
		case llmAppWorkflowPrompt:
			items = []llmAppWorkItem{{Index: 0, ID: "input", Text: request.Input}}
		case llmAppWorkflowBatch:
			items, err = llmAppItemsFromValues(request.Items)
			if err == nil {
				var appended []llmAppWorkItem
				appended, err = readLLMAppWorkItems(filepath.Join(m.runDir(run.snapshot().RunID), "inputs.jsonl"))
				items = append(items, appended...)
			}
		case llmAppWorkflowPDFPages:
			if runtime.pdfExtractor == nil {
				err = errors.New("PDF extractor is unavailable")
				break
			}
			m.updateRun(run, func(record *LLMAppRunRecord) { record.Phase = "extract_pdf" })
			m.emitRunEvent(run, llmAppEventPhaseChanged, "worker", "extracting PDF pages", map[string]interface{}{
				"phase": "extract_pdf",
			})
			items, err = runtime.pdfExtractor(ctx, request.File, func(current, total int) {
				if waitErr := m.waitUntilRunnable(ctx, run); waitErr != nil {
					return
				}
				m.updateRun(run, func(record *LLMAppRunRecord) {
					record.Phase = "extract_pdf"
					record.CurrentItem = fmt.Sprintf("page_%d", current)
					record.Progress.Total = total
					record.LastProgressAt = time.Now()
				})
			})
		}
		if err == nil {
			items = splitLLMAppItems(items, definition.MaxItemChars)
			if len(items) == 0 {
				err = errors.New("workflow produced no processable items")
			} else if len(items) > definition.MaxItems {
				err = fmt.Errorf("workflow produced %d items; application limit is %d", len(items), definition.MaxItems)
			}
		}
		if err == nil {
			m.updateRun(run, func(record *LLMAppRunRecord) {
				record.Phase = "model_loop"
				record.CurrentItem = ""
				record.LastProgressAt = time.Now()
			})
			m.emitRunEvent(run, llmAppEventPhaseChanged, "worker", "model loop started", map[string]interface{}{
				"phase": "model_loop", "initial_items": len(items),
			})
			results, err = m.executeItems(ctx, run, definition, request.Input, items, runtime.ModelCaller)
		}
		m.closeItemQueue(run)
	}

	completedAt := time.Now()
	finalStatus := llmAppStatusCompleted
	finalError := ""
	current := run.snapshot()
	if leaseErr := m.assertRunLease(run); leaseErr != nil {
		return
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		finalStatus = llmAppStatusFailed
		finalError = fmt.Sprintf("%v: maximum run duration reached", ErrLLMAppBudgetExceeded)
	} else if ctx.Err() != nil || current.Status == llmAppStatusStopping {
		finalStatus = llmAppStatusStopped
		if current.Status == llmAppStatusStopping && current.Health.Reason != "" {
			finalError = current.Health.Reason
		} else if current.Lifecycle == llmAppLifecycleAttached {
			finalError = "parent execution was canceled"
		} else {
			finalError = "stopped by MACS"
		}
	} else if err != nil {
		finalStatus = llmAppStatusFailed
		finalError = err.Error()
	} else if current.ItemErrors > 0 {
		finalStatus = llmAppStatusDegraded
	}
	if definition.Workflow != llmAppWorkflowGraph {
		checkpointResults, checkpointErr := m.collectSettledItemResults(current.RunID)
		if checkpointErr != nil {
			if finalError == "" {
				finalStatus = llmAppStatusFailed
				finalError = checkpointErr.Error()
			}
		} else {
			results = checkpointResults
			current.Progress.Completed = len(results)
			if current.Progress.Total < len(results) {
				current.Progress.Total = len(results)
			}
			if current.Progress.Total > 0 {
				current.Progress.Percent = float64(current.Progress.Completed) * 100 / float64(current.Progress.Total)
			}
			current.ItemErrors = 0
			for _, itemResult := range results {
				if itemResult.Error != "" {
					current.ItemErrors++
				}
			}
		}
	}
	if finalStatus == llmAppStatusCompleted && current.ItemErrors > 0 {
		finalStatus = llmAppStatusDegraded
	}
	resultPath := filepath.Join(m.runDir(current.RunID), "result.json")
	result := llmAppRunResult{
		RunID: current.RunID, AppName: current.AppName, Status: finalStatus,
		Completed: current.Progress.Completed, Total: current.Progress.Total,
		ItemErrors: current.ItemErrors, Results: results, Output: graphOutput, CompletedAt: completedAt,
	}
	if writeErr := m.writeRunJSON(run, resultPath, result); writeErr != nil && finalError == "" {
		finalStatus = llmAppStatusFailed
		finalError = writeErr.Error()
	}
	finalEvent := llmAppEventRunCompleted
	switch finalStatus {
	case llmAppStatusDegraded:
		finalEvent = llmAppEventRunDegraded
	case llmAppStatusStopped:
		finalEvent = llmAppEventRunStopped
	case llmAppStatusFailed:
		finalEvent = llmAppEventRunFailed
	}
	m.finalizeRunRecord(run, finalEvent, "llm_app run reached a terminal state", map[string]interface{}{
		"status": finalStatus, "completed": current.Progress.Completed,
		"total": current.Progress.Total, "item_errors": current.ItemErrors,
	}, func(record *LLMAppRunRecord) {
		record.Status = finalStatus
		record.Phase = finalStatus
		record.Progress = current.Progress
		record.ItemErrors = current.ItemErrors
		record.ResultPath = resultPath
		record.Error = finalError
		record.CompletedAt = &completedAt
		record.LastHeartbeatAt = completedAt
		record.CurrentItem = ""
		switch finalStatus {
		case llmAppStatusCompleted:
			record.Health = LLMAppHealth{Status: llmAppHealthHealthy, EvaluatedAt: completedAt}
		case llmAppStatusDegraded:
			record.Health = LLMAppHealth{Status: llmAppHealthDegraded, Reason: "one or more items failed", RecommendedAction: "inspect_result", EvaluatedAt: completedAt}
		case llmAppStatusStopped:
			record.Health = LLMAppHealth{Status: llmAppHealthBlocked, Reason: finalError, EvaluatedAt: completedAt}
		default:
			record.Health = LLMAppHealth{Status: llmAppHealthUnhealthy, Reason: finalError, RecommendedAction: "inspect_error", EvaluatedAt: completedAt}
		}
	})
}

func (m *LLMAppManager) runHeartbeat(
	ctx context.Context,
	run *llmAppManagedRun,
	seconds int,
	done <-chan struct{},
) {
	if seconds <= 0 {
		seconds = 15
	}
	ticker := time.NewTicker(time.Duration(seconds) * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-done:
			return
		case now := <-ticker.C:
			if err := m.renewRunLease(run, llmAppLeaseDuration(seconds)); err != nil {
				run.mu.Lock()
				cancel := run.cancel
				run.mu.Unlock()
				if cancel != nil {
					cancel()
				}
				return
			}
			m.updateRun(run, func(record *LLMAppRunRecord) {
				if !isTerminalLLMAppStatus(record.Status) {
					record.LastHeartbeatAt = now
				}
			})
		}
	}
}

func (m *LLMAppManager) executeItems(
	ctx context.Context,
	run *llmAppManagedRun,
	definition LLMAppDefinition,
	sharedInput string,
	items []llmAppWorkItem,
	caller DetailedModelCaller,
) ([]LLMAppItemResult, error) {
	workerCtx, cancelWorkers := context.WithCancel(ctx)
	defer cancelWorkers()
	settledResults, err := m.prepareItemQueue(run, items)
	if err != nil {
		return nil, err
	}
	type outcome struct {
		result LLMAppItemResult
		err    error
	}
	outcomes := make(chan outcome)

	var workerWG sync.WaitGroup
	for workerID := 0; workerID < llmAppMaximumConcurrency; workerID++ {
		workerWG.Add(1)
		go func(workerID int) {
			defer workerWG.Done()
			for {
				item, plan, claimed, err := m.claimRunItem(workerCtx, run, workerID)
				if err != nil || !claimed {
					return
				}
				m.emitRunEvent(run, llmAppEventItemStarted, "worker", "work item claimed", map[string]interface{}{
					"item_id": item.ID, "index": item.Index, "plan_revision": plan.Revision,
				})
				// Pause is checked again after the claim. The claimed item keeps its
				// plan snapshot, while no model request starts until MACS resumes.
				if err := m.waitUntilRunnable(workerCtx, run); err != nil {
					m.finishClaimedRunItem(run)
					return
				}
				itemDefinition := plan.applyToDefinition(definition)
				result, processErr := m.processManagedLLMAppItem(workerCtx, run, itemDefinition, sharedInput, item, plan, caller)
				result.PlanRevision = plan.Revision
				m.finishClaimedRunItem(run)
				select {
				case outcomes <- outcome{result: result, err: processErr}:
				case <-workerCtx.Done():
					return
				}
				if processErr != nil {
					return
				}
			}
		}(workerID)
	}
	go func() {
		workerWG.Wait()
		close(outcomes)
	}()

	partialPath := filepath.Join(m.runDir(run.snapshot().RunID), "results.jsonl")
	results := append([]LLMAppItemResult(nil), settledResults...)
	var limitErr error
	for itemOutcome := range outcomes {
		if itemOutcome.err != nil {
			if limitErr == nil {
				limitErr = itemOutcome.err
			}
			cancelWorkers()
			continue
		}
		result := itemOutcome.result
		results = append(results, result)
		_ = m.appendRunJSONLine(run, partialPath, result)
		pauseForHealth := false
		m.updateRun(run, func(record *LLMAppRunRecord) {
			record.PartialResultPath = partialPath
			record.Progress.Completed++
			if record.Progress.Total > 0 {
				record.Progress.Percent = float64(record.Progress.Completed) * 100 / float64(record.Progress.Total)
			}
			record.CurrentItem = result.ID
			record.Checkpoint = fmt.Sprintf("%d/%d", record.Progress.Completed, record.Progress.Total)
			record.LastProgressAt = time.Now()
			record.LastHeartbeatAt = record.LastProgressAt
			if result.Error != "" {
				record.ItemErrors++
				record.Health = LLMAppHealth{
					Status: llmAppHealthDegraded, Reason: result.Error,
					RecommendedAction: "continue", Evidence: []string{"item_error"}, EvaluatedAt: time.Now(),
				}
				if record.ItemErrors >= definition.Health.MaxItemErrors && definition.Health.OnUnhealthy != "continue" {
					reason := fmt.Sprintf("item error limit reached: %d", record.ItemErrors)
					if definition.Health.OnUnhealthy == "fail" {
						limitErr = errors.New(reason)
					} else if definition.Health.OnUnhealthy == "pause" {
						pauseForHealth = true
					}
					record.Health = LLMAppHealth{
						Status: llmAppHealthUnhealthy, Reason: reason,
						RecommendedAction: definition.Health.OnUnhealthy, Evidence: []string{"item_error_limit"}, EvaluatedAt: time.Now(),
					}
				}
			}
		})
		if pauseForHealth {
			_ = m.pauseRun(run)
			m.emitRunEvent(run, llmAppEventDecisionRequired, "worker", "item error limit requires a MACS decision", map[string]interface{}{
				"item_id": result.ID, "recommended_action": "pause",
			})
		}
		eventType := llmAppEventItemCompleted
		message := "work item completed"
		if result.Error != "" {
			eventType = llmAppEventItemFailed
			message = "work item failed"
		}
		snapshot := run.snapshot()
		m.emitRunEvent(run, eventType, "worker", message, map[string]interface{}{
			"item_id": result.ID, "index": result.Index, "plan_revision": result.PlanRevision,
			"completed": snapshot.Progress.Completed, "total": snapshot.Progress.Total,
			"error": result.Error,
		})
		if limitErr != nil {
			cancelWorkers()
		}
	}
	if limitErr != nil {
		return results, limitErr
	}
	if ctx.Err() != nil {
		return results, ctx.Err()
	}
	sort.Slice(results, func(i, j int) bool { return results[i].Index < results[j].Index })
	return results, nil
}

func processLLMAppItem(
	ctx context.Context,
	definition LLMAppDefinition,
	sharedInput string,
	item llmAppWorkItem,
	caller llmAppModelCaller,
) (LLMAppItemResult, int, int) {
	result := LLMAppItemResult{Index: item.Index, ID: item.ID, Metadata: item.Metadata}
	if strings.TrimSpace(item.Text) == "" {
		result.Error = "item has no extractable text; OCR or another extractor may be required"
		return result, 0, 0
	}
	userPrompt := renderLLMAppPrompt(definition.PromptTemplate, definition.PromptVariables, sharedInput, item)
	messages := make([]map[string]interface{}, 0, 4)
	if definition.SystemPrompt != "" {
		messages = append(messages, map[string]interface{}{"role": "system", "content": definition.SystemPrompt})
	}
	messages = append(messages, map[string]interface{}{"role": "user", "content": userPrompt})
	inputChars := len(definition.SystemPrompt) + len(userPrompt)
	for validationAttempt := 0; validationAttempt <= definition.Health.ValidationRetries; validationAttempt++ {
		output, err := caller(ctx, definition.Model, messages, definition.MaxTokens, definition.Temperature)
		result.ModelCalls++
		if err != nil {
			result.Error = err.Error()
			return result, inputChars, 0
		}
		if err := validateLLMAppOutput(output, definition.OutputSchema); err == nil {
			result.Output = output
			return result, inputChars, len(output)
		} else if validationAttempt == definition.Health.ValidationRetries {
			result.Error = "output validation failed: " + err.Error()
			return result, inputChars, len(output)
		} else {
			messages = append(messages,
				map[string]interface{}{"role": "assistant", "content": output},
				map[string]interface{}{"role": "user", "content": "The previous output did not satisfy the required JSON schema. Return only one valid JSON value that satisfies it."},
			)
			inputChars += len(output) + 110
		}
	}
	return result, inputChars, 0
}

func renderLLMAppPrompt(template string, variables map[string]string, sharedInput string, item llmAppWorkItem) string {
	values := map[string]string{
		"{{input}}": sharedInput,
		"{{item}}":  item.Text,
		"{{index}}": strconv.Itoa(item.Index + 1),
		"{{id}}":    item.ID,
		"{{page}}":  item.Metadata["page"],
	}
	rendered := template
	usedItem := strings.Contains(rendered, "{{item}}")
	for marker, value := range values {
		rendered = strings.ReplaceAll(rendered, marker, value)
	}
	for key, value := range variables {
		rendered = strings.ReplaceAll(rendered, "{{var:"+key+"}}", value)
	}
	if !usedItem {
		rendered += "\n\nInput:\n" + item.Text
	}
	return rendered
}

func validateLLMAppOutput(output string, schema map[string]interface{}) error {
	if len(schema) == 0 {
		return nil
	}
	value, err := decodeLLMAppStructuredOutput(output)
	if err != nil {
		return err
	}
	return validateLLMAppJSONValue(schema, value)
}

func llmAppItemsFromValues(values []interface{}) ([]llmAppWorkItem, error) {
	items := make([]llmAppWorkItem, 0, len(values))
	for index, value := range values {
		var text string
		switch typed := value.(type) {
		case string:
			text = typed
		default:
			data, err := json.Marshal(typed)
			if err != nil {
				return nil, fmt.Errorf("encode item %d: %w", index+1, err)
			}
			text = string(data)
		}
		items = append(items, llmAppWorkItem{Index: index, ID: fmt.Sprintf("item_%06d", index+1), Text: text})
	}
	return items, nil
}

func splitLLMAppItems(items []llmAppWorkItem, maxChars int) []llmAppWorkItem {
	if maxChars <= 0 {
		maxChars = llmAppDefaultItemChars
	}
	result := make([]llmAppWorkItem, 0, len(items))
	for _, item := range items {
		runes := []rune(item.Text)
		if len(runes) <= maxChars {
			item.Index = len(result)
			result = append(result, item)
			continue
		}
		parts := (len(runes) + maxChars - 1) / maxChars
		for part := 0; part < parts; part++ {
			start := part * maxChars
			end := start + maxChars
			if end > len(runes) {
				end = len(runes)
			}
			metadata := make(map[string]string, len(item.Metadata)+2)
			for key, value := range item.Metadata {
				metadata[key] = value
			}
			metadata["part"] = strconv.Itoa(part + 1)
			metadata["parts"] = strconv.Itoa(parts)
			result = append(result, llmAppWorkItem{
				Index: len(result), ID: fmt.Sprintf("%s_part_%03d", item.ID, part+1),
				Text: string(runes[start:end]), Metadata: metadata,
			})
		}
	}
	return result
}

func extractLLMAppPDFPages(
	ctx context.Context,
	path string,
	progress func(current, total int),
) ([]llmAppWorkItem, error) {
	doc, err := contenttools.OpenPDF(path)
	if err != nil {
		return nil, fmt.Errorf("open PDF: %w", err)
	}
	defer doc.Close()
	total := doc.NumPages()
	if total <= 0 {
		return nil, errors.New("PDF has no pages")
	}
	items := make([]llmAppWorkItem, 0, total)
	for pageNumber := 1; pageNumber <= total; pageNumber++ {
		if err := ctx.Err(); err != nil {
			return items, err
		}
		page := doc.Page(pageNumber)
		var text strings.Builder
		pageWidth, pageHeight := page.PageSize()
		text.WriteString(page.ExtractTextLayout(pageWidth, pageHeight))
		for _, table := range page.ExtractTables() {
			if rendered := contenttools.TableToMarkdown(table); rendered != "" {
				text.WriteString("\n\n")
				text.WriteString(rendered)
			}
		}
		items = append(items, llmAppWorkItem{
			Index: pageNumber - 1, ID: fmt.Sprintf("page_%06d", pageNumber),
			Text: strings.TrimSpace(text.String()), Metadata: map[string]string{"page": strconv.Itoa(pageNumber)},
		})
		if progress != nil {
			progress(pageNumber, total)
		}
	}
	return items, nil
}

func (m *LLMAppManager) updateRun(run *llmAppManagedRun, update func(*LLMAppRunRecord)) {
	if run == nil {
		return
	}
	run.mu.Lock()
	update(&run.record)
	run.mu.Unlock()
	if err := m.persistRunRecord(run); err != nil && m.logger != nil {
		m.logger.Printf("persist llm_app run %s: %v", run.snapshot().RunID, err)
	}
}

func (m *LLMAppManager) persistRunRecord(run *llmAppManagedRun) error {
	if run == nil {
		return errors.New("run not found")
	}
	run.persistMu.Lock()
	defer run.persistMu.Unlock()
	return m.withRunLease(run, func() error {
		record := run.snapshot()
		return writeLLMAppJSON(filepath.Join(m.runDir(record.RunID), "run.json"), record)
	})
}

func (m *LLMAppManager) persistRunRecordUnfenced(run *llmAppManagedRun) error {
	if run == nil {
		return errors.New("run not found")
	}
	run.persistMu.Lock()
	defer run.persistMu.Unlock()
	record := run.snapshot()
	return writeLLMAppJSON(filepath.Join(m.runDir(record.RunID), "run.json"), record)
}

func (m *LLMAppManager) writeRunJSON(run *llmAppManagedRun, path string, value interface{}) error {
	return m.withRunLease(run, func() error { return writeLLMAppJSON(path, value) })
}

func (m *LLMAppManager) appendRunJSONLine(run *llmAppManagedRun, path string, value interface{}) error {
	return m.withRunLease(run, func() error { return appendLLMAppJSONLine(path, value) })
}

func (run *llmAppManagedRun) snapshot() LLMAppRunRecord {
	if run == nil {
		return LLMAppRunRecord{}
	}
	run.mu.Lock()
	defer run.mu.Unlock()
	record := run.record
	record.Health.Evidence = append([]string(nil), run.record.Health.Evidence...)
	return record
}

func (m *LLMAppManager) waitUntilRunnable(ctx context.Context, run *llmAppManagedRun) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		run.mu.Lock()
		paused := run.pauseRequested
		resumeCh := run.resumeCh
		if paused {
			run.record.Status = llmAppStatusPaused
			run.record.Phase = llmAppStatusPaused
			run.record.LastHeartbeatAt = time.Now()
		}
		run.mu.Unlock()
		if !paused {
			return nil
		}
		_ = m.persistRunRecord(run)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-resumeCh:
		}
	}
}

func (m *LLMAppManager) pauseRun(run *llmAppManagedRun) error {
	if run == nil {
		return errors.New("run not found")
	}
	if err := m.assertLocalRunControl(run); err != nil {
		return err
	}
	run.mu.Lock()
	if isTerminalLLMAppStatus(run.record.Status) {
		status := run.record.Status
		run.mu.Unlock()
		return fmt.Errorf("cannot pause run in %s status", status)
	}
	if run.pauseRequested {
		run.mu.Unlock()
		return nil
	}
	if phase := strings.TrimSpace(run.record.Phase); phase != "" && phase != llmAppStatusPaused && phase != llmAppStatusPausing {
		run.resumePhase = phase
	}
	run.pauseRequested = true
	run.resumeCh = make(chan struct{})
	run.record.Status = llmAppStatusPausing
	run.record.Phase = llmAppStatusPausing
	m.signalWorkChangedLocked(run)
	run.mu.Unlock()
	if err := m.persistRunRecord(run); err != nil {
		return err
	}
	m.emitRunEvent(run, llmAppEventPauseRequested, "openkitty", "pause requested", nil)
	return nil
}

func (m *LLMAppManager) resumeRun(run *llmAppManagedRun) error {
	if run == nil {
		return errors.New("run not found")
	}
	if err := m.assertLocalRunControl(run); err != nil {
		return err
	}
	run.mu.Lock()
	if isTerminalLLMAppStatus(run.record.Status) {
		status := run.record.Status
		run.mu.Unlock()
		return fmt.Errorf("cannot resume run in %s status", status)
	}
	if !run.pauseRequested {
		run.mu.Unlock()
		return errors.New("run is not paused")
	}
	run.pauseRequested = false
	resumeCh := run.resumeCh
	run.resumeCh = nil
	run.record.Status = llmAppStatusRunning
	resumePhase := strings.TrimSpace(run.resumePhase)
	if resumePhase == "" || resumePhase == "queued" || resumePhase == llmAppStatusPausing || resumePhase == llmAppStatusPaused {
		resumePhase = "model_loop"
	}
	run.record.Phase = resumePhase
	run.resumePhase = ""
	run.record.LastHeartbeatAt = time.Now()
	m.signalWorkChangedLocked(run)
	run.mu.Unlock()
	if resumeCh != nil {
		close(resumeCh)
	}
	if err := m.persistRunRecord(run); err != nil {
		return err
	}
	m.emitRunEvent(run, llmAppEventResumed, "openkitty", "run resumed", nil)
	return nil
}

func (m *LLMAppManager) stopRun(run *llmAppManagedRun, reason string) (LLMAppRunRecord, error) {
	cancel, done, err := m.requestRunStop(run, reason)
	if cancel != nil {
		cancel()
	}
	if done != nil {
		select {
		case <-done:
		case <-time.After(2 * time.Second):
		}
	}
	return run.snapshot(), err
}

// requestRunStop persists the stop request but deliberately leaves the worker
// alive until the caller invokes cancel. Durable command handling uses this
// gap to settle its receipt before the worker can commit terminal state and
// release the lease.
func (m *LLMAppManager) requestRunStop(
	run *llmAppManagedRun,
	reason string,
) (context.CancelFunc, <-chan struct{}, error) {
	if run == nil {
		return nil, nil, errors.New("run not found")
	}
	if err := m.assertLocalRunControl(run); err != nil {
		return nil, nil, err
	}
	run.mu.Lock()
	if isTerminalLLMAppStatus(run.record.Status) {
		run.mu.Unlock()
		return nil, nil, nil
	}
	run.record.Status = llmAppStatusStopping
	run.record.Phase = llmAppStatusStopping
	run.record.Health = LLMAppHealth{
		Status: llmAppHealthBlocked, Reason: reason, RecommendedAction: "stop", EvaluatedAt: time.Now(),
	}
	cancel := run.cancel
	done := run.done
	m.signalWorkChangedLocked(run)
	run.mu.Unlock()
	if err := m.persistRunRecord(run); err != nil {
		return cancel, done, err
	}
	m.emitRunEvent(run, llmAppEventStopRequested, "openkitty", "stop requested", map[string]interface{}{
		"reason": reason,
	})
	return cancel, done, nil
}

func (m *LLMAppManager) applyHealthDecision(run *llmAppManagedRun, health LLMAppHealth) error {
	if run == nil {
		return errors.New("run not found")
	}
	if err := m.assertLocalRunControl(run); err != nil {
		return err
	}
	if err := normalizeLLMAppHealth(&health); err != nil {
		return err
	}
	m.updateRun(run, func(record *LLMAppRunRecord) { record.Health = health })
	m.emitRunEvent(run, llmAppEventHealthChanged, "openkitty", "health decision applied", map[string]interface{}{
		"status": health.Status, "recommended_action": health.RecommendedAction,
		"reason": health.Reason, "evidence": health.Evidence,
	})
	switch health.RecommendedAction {
	case "stop":
		_, err := m.stopRun(run, health.Reason)
		return err
	case "pause":
		return m.pauseRun(run)
	case "continue":
		run.mu.Lock()
		paused := run.pauseRequested
		run.mu.Unlock()
		if paused {
			return m.resumeRun(run)
		}
	}
	return nil
}

func decodeLLMAppHealth(raw interface{}) (LLMAppHealth, error) {
	data, err := json.Marshal(raw)
	if err != nil {
		return LLMAppHealth{}, err
	}
	var health LLMAppHealth
	if err := json.Unmarshal(data, &health); err != nil {
		return LLMAppHealth{}, err
	}
	if err := normalizeLLMAppHealth(&health); err != nil {
		return LLMAppHealth{}, err
	}
	return health, nil
}

func normalizeLLMAppHealth(health *LLMAppHealth) error {
	if health == nil {
		return errors.New("health decision is required")
	}
	health.Status = strings.ToLower(strings.TrimSpace(health.Status))
	switch health.Status {
	case llmAppHealthHealthy, llmAppHealthDegraded, llmAppHealthStalled, llmAppHealthBlocked, llmAppHealthUnhealthy:
	default:
		return fmt.Errorf("unsupported health status %q", health.Status)
	}
	health.Reason = strings.TrimSpace(health.Reason)
	health.RecommendedAction = strings.ToLower(strings.TrimSpace(health.RecommendedAction))
	switch health.RecommendedAction {
	case "", "continue", "pause", "stop", "retry_current", "reduce_concurrency", "inspect_result", "inspect_error":
	default:
		return fmt.Errorf("unsupported health action %q", health.RecommendedAction)
	}
	health.EvaluatedAt = time.Now()
	return nil
}

func (m *LLMAppManager) evaluateHealth(run *llmAppManagedRun) LLMAppRunRecord {
	record := run.snapshot()
	if isTerminalLLMAppStatus(record.Status) {
		return record
	}
	run.mu.Lock()
	localWorker := run.workerActive && run.leaseToken != ""
	run.mu.Unlock()
	if !localWorker {
		return record
	}
	now := time.Now()
	var definition LLMAppDefinition
	err := readLLMAppJSON(filepath.Join(m.runDir(record.RunID), "definition.json"), &definition)
	if err == nil {
		err = normalizeLLMAppDefinition(&definition)
	}
	if err != nil {
		return record
	}
	health := record.Health
	if now.Sub(record.LastHeartbeatAt) > time.Duration(definition.Health.HeartbeatSeconds*3)*time.Second {
		health = LLMAppHealth{
			Status: llmAppHealthUnhealthy, Reason: "worker heartbeat is stale",
			RecommendedAction: "stop", Evidence: []string{"heartbeat_stale"}, EvaluatedAt: now,
		}
	} else if record.Status == llmAppStatusRunning && now.Sub(record.LastProgressAt) > time.Duration(definition.Health.StallSeconds)*time.Second {
		health = LLMAppHealth{
			Status: llmAppHealthStalled, Reason: "workflow progress is stale",
			RecommendedAction: "pause", Evidence: []string{"progress_stalled"}, EvaluatedAt: now,
		}
	} else if health.Status == "" {
		health = LLMAppHealth{Status: llmAppHealthHealthy, EvaluatedAt: now}
	}
	if health.Status != record.Health.Status ||
		health.Reason != record.Health.Reason ||
		health.RecommendedAction != record.Health.RecommendedAction ||
		strings.Join(health.Evidence, "\x00") != strings.Join(record.Health.Evidence, "\x00") {
		m.updateRun(run, func(target *LLMAppRunRecord) { target.Health = health })
		record.Health = health
		m.emitRunEvent(run, llmAppEventHealthChanged, "supervisor", "health evaluation changed", map[string]interface{}{
			"status": health.Status, "recommended_action": health.RecommendedAction,
			"reason": health.Reason, "evidence": health.Evidence,
		})
		record = run.snapshot()
	}
	return record
}

func (m *LLMAppManager) authorizedRun(
	ctx context.Context,
	runID string,
) (LLMAppRunRecord, *llmAppManagedRun, error) {
	run, err := m.getRun(runID)
	if err != nil {
		return LLMAppRunRecord{}, nil, err
	}
	record := run.snapshot()
	if !m.canAccessRun(ctx, record) {
		return LLMAppRunRecord{}, nil, errors.New("llm_app run not found")
	}
	if isTerminalLLMAppStatus(record.Status) {
		run.mu.Lock()
		done := run.done
		workerActive := run.workerActive
		run.mu.Unlock()
		if workerActive && done != nil {
			select {
			case <-done:
				record = run.snapshot()
			case <-ctx.Done():
				return LLMAppRunRecord{}, nil, ctx.Err()
			}
		}
	}
	return record, run, nil
}

func (m *LLMAppManager) getRun(runID string) (*llmAppManagedRun, error) {
	runID = strings.ToLower(strings.TrimSpace(runID))
	if !llmAppRunIDPattern.MatchString(runID) {
		return nil, errors.New("valid run_id is required")
	}
	m.mu.RLock()
	run := m.runs[runID]
	m.mu.RUnlock()
	if run != nil {
		if err := m.ensureRunWorker(run); err != nil && m.logger != nil {
			m.logger.Printf("resume llm_app run %s: %v", runID, err)
		}
		return run, nil
	}
	var record LLMAppRunRecord
	if err := readLLMAppJSON(filepath.Join(m.runDir(runID), "run.json"), &record); err != nil {
		if os.IsNotExist(err) {
			return nil, errors.New("llm_app run not found")
		}
		return nil, err
	}
	var definition LLMAppDefinition
	_ = readLLMAppJSON(filepath.Join(m.runDir(runID), "definition.json"), &definition)
	_ = normalizeLLMAppDefinition(&definition)
	var plan LLMAppRunPlan
	if err := readLLMAppJSON(filepath.Join(m.runDir(runID), "plan.json"), &plan); err != nil {
		plan, _ = newLLMAppRunPlan(definition, record.CreatedAt)
	}
	if record.PlanRevision == 0 || plan.Revision > record.PlanRevision {
		record.PlanRevision = plan.Revision
		record.PlanDigest = plan.Digest
	}
	if record.EventPath == "" {
		record.EventPath = filepath.Join(m.runDir(runID), "events.jsonl")
	}
	if sequence, err := lastLLMAppEventSequence(record.EventPath); err == nil && sequence > record.EventSequence {
		record.EventSequence = sequence
	}
	if record.CommandPath == "" {
		record.CommandPath = filepath.Join(m.runDir(runID), "commands.jsonl")
	}
	if record.Lifecycle == "" {
		record.Lifecycle = llmAppLifecycleDetached
	}
	if record.Budget.Policy.MaxModelCalls == 0 {
		record.Budget = buildLLMAppBudgetSnapshot(definition.Budget, record.Usage, 0, 0, record.Budget.Reason)
	}
	run = &llmAppManagedRun{
		record: record, plan: plan, workflow: definition.Workflow,
		maxItems: definition.MaxItems, maxItemChars: definition.MaxItemChars,
		workChangedCh: make(chan struct{}), eventChangedCh: make(chan struct{}),
		commandReceipts: make(map[string]LLMAppCommandReceipt), itemPlanRevisions: make(map[string]int),
		budget: definition.Budget,
	}
	if record.Status == llmAppStatusPaused || record.Status == llmAppStatusPausing {
		run.pauseRequested = true
		run.resumeCh = make(chan struct{})
	}
	m.mu.Lock()
	inserted := false
	if existing := m.runs[runID]; existing != nil {
		run = existing
	} else {
		m.runs[runID] = run
		inserted = true
	}
	m.mu.Unlock()
	if inserted {
		if err := m.loadCommandReceipts(run); err != nil && m.logger != nil {
			m.logger.Printf("load llm_app command receipts %s: %v", runID, err)
		}
	}
	if err := m.ensureRunWorker(run); err != nil && m.logger != nil {
		m.logger.Printf("resume llm_app run %s: %v", runID, err)
	}
	return run, nil
}

func (m *LLMAppManager) resolveIdentity(ctx context.Context) (Identity, bool) {
	if m == nil || m.identityResolver == nil {
		return Identity{}, false
	}
	return m.identityResolver(ctx)
}

func (m *LLMAppManager) canAccessRun(ctx context.Context, record LLMAppRunRecord) bool {
	if record.OwnerTenantID == "" && record.OwnerSubjectID == "" {
		return true
	}
	identity, ok := m.resolveIdentity(ctx)
	if !ok {
		return false
	}
	if identity.ControlPlane {
		return true
	}
	if record.OwnerTenantID != "" && identity.TenantID != record.OwnerTenantID {
		return false
	}
	if record.OwnerSubjectID != "" && identity.SubjectID != record.OwnerSubjectID {
		return false
	}
	return true
}

func (m *LLMAppManager) readResult(record LLMAppRunRecord, includeOutputs bool) (string, error) {
	response := map[string]interface{}{
		"run_id": record.RunID, "application": record.AppName, "status": record.Status,
		"progress": record.Progress, "health": record.Health, "result_path": record.ResultPath,
		"partial_result_path": record.PartialResultPath, "error": record.Error,
		"usage": record.Usage, "budget": record.Budget, "lifecycle": record.Lifecycle,
	}
	if !includeOutputs || record.ResultPath == "" {
		return marshalLLMAppJSON(response)
	}
	var result llmAppRunResult
	if err := readLLMAppJSON(record.ResultPath, &result); err != nil {
		return "", err
	}
	response["result"] = result
	return marshalLLMAppJSON(response)
}

func isTerminalLLMAppStatus(status string) bool {
	switch status {
	case llmAppStatusStopped, llmAppStatusCompleted, llmAppStatusDegraded, llmAppStatusFailed:
		return true
	default:
		return false
	}
}

func newLLMAppRunID() (string, error) {
	random := make([]byte, 10)
	if _, err := rand.Read(random); err != nil {
		return "", fmt.Errorf("generate llm_app run id: %w", err)
	}
	return "llma_" + hex.EncodeToString(random), nil
}

func writeLLMAppJSON(path string, value interface{}) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return fsutil.WriteAtomic(path, append(data, '\n'), 0o600)
}

func readLLMAppJSON(path string, target interface{}) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, target); err != nil {
		return fmt.Errorf("decode %s: %w", path, err)
	}
	return nil
}

func appendLLMAppJSONLine(path string, value interface{}) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	writer := bufio.NewWriter(file)
	if _, err := writer.Write(append(data, '\n')); err != nil {
		return err
	}
	return writer.Flush()
}

func marshalLLMAppJSON(value interface{}) (string, error) {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func llmAppStringParam(params map[string]interface{}, key string) string {
	if params == nil {
		return ""
	}
	value, _ := params[key].(string)
	return value
}

func llmAppBoolParam(params map[string]interface{}, key string) bool {
	if params == nil {
		return false
	}
	value, _ := params[key].(bool)
	return value
}

func estimateLLMAppTokens(characters int) int {
	if characters <= 0 {
		return 0
	}
	return (characters + 3) / 4
}

func taskIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	taskID, _ := ctx.Value("task_id").(string)
	return taskID
}
