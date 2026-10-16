package app

import (
	"context"
	"errors"
	"io"
	"log"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

func openAgentOSScenarioDB(t *testing.T) *gorm.DB {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "agent-os-scenarios.db")
	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{
		Logger: gormlogger.Default.LogMode(gormlogger.Silent),
	})
	if err != nil {
		t.Fatalf("open scenario database: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get scenario sql database: %v", err)
	}
	// A single connection makes SQLite tests deterministic while still allowing
	// goroutines to exercise the host's concurrency boundaries.
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() {
		if err := sqlDB.Close(); err != nil {
			t.Errorf("close scenario database: %v", err)
		}
	})
	if err := db.AutoMigrate(
		&GatewayTaskRecord{}, &ChatMessage{}, &ChatSession{},
		&ToolExecutionReceipt{}, &AgentTaskRequestRecord{}, &ApprovalRequestRecord{},
		&SkillRunRecord{},
	); err != nil {
		t.Fatalf("migrate scenario database: %v", err)
	}
	return db
}

func TestAgentOSDurableSchemaUsesStableSQLNames(t *testing.T) {
	db := openAgentOSScenarioDB(t)
	if err := db.AutoMigrate(&MessageRequestRecord{}); err != nil {
		t.Fatalf("migrate message request records: %v", err)
	}

	cases := []struct {
		name    string
		model   interface{}
		columns []string
	}{
		{
			name:  "tool_execution_receipts",
			model: &ToolExecutionReceipt{},
			columns: []string{
				"id", "task_id", "tool_call_id", "tool_name", "arguments_hash",
				"lease_token", "read_only", "status", "result_content",
				"result_truncated", "error_msg", "duration_ms", "started_at",
				"completed_at", "updated_at",
			},
		},
		{
			name:  "agent_task_request_records",
			model: &AgentTaskRequestRecord{},
			columns: []string{
				"id", "key_hash", "request_hash", "tenant_id", "role_id",
				"caller_id", "subject_id", "task_id", "created_at",
			},
		},
		{
			name:  "skill_run_records",
			model: &SkillRunRecord{},
			columns: []string{
				"id", "run_id", "task_id", "tenant_id", "subject_id",
				"skill_name", "skill_version", "content_sha256", "runtime_digest",
				"skill_snapshot", "parameters_json", "activated_output",
				"frozen_runtime_dir", "activation_id", "activation_count", "phase",
				"artifacts_json", "verification_json", "client_reference", "error_msg",
				"started_at", "completed_at", "created_at", "updated_at",
			},
		},
		{
			name:  "message_request_records",
			model: &MessageRequestRecord{},
			columns: []string{
				"id", "task_id", "task_type", "message", "agent_name",
				"channel", "stream", "parameters", "created_at",
			},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if !db.Migrator().HasTable(testCase.name) {
				t.Fatalf("stable table %q was not created", testCase.name)
			}
			for _, column := range testCase.columns {
				if !db.Migrator().HasColumn(testCase.model, column) {
					t.Errorf("stable column %q.%s was not created", testCase.name, column)
				}
			}
		})
	}
}

func TestPrepareGatewaySQLiteMigrationsRemovesLegacyNamedIndexCollision(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "legacy-index-collision.db")
	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{
		Logger: gormlogger.Default.LogMode(gormlogger.Silent),
	})
	if err != nil {
		t.Fatalf("open legacy database: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get legacy sql database: %v", err)
	}
	t.Cleanup(func() {
		if err := sqlDB.Close(); err != nil {
			t.Errorf("close legacy database: %v", err)
		}
	})
	if err := db.Exec(
		"CREATE TABLE `d1r_cbts` (`id` integer PRIMARY KEY AUTOINCREMENT, `bsk_f96_i7l` text, `mg9_hj_jr_nl_q` text)",
	).Error; err != nil {
		t.Fatalf("create legacy receipt table: %v", err)
	}
	if err := db.Exec(
		"CREATE UNIQUE INDEX `idx_tool_receipt_call` ON `d1r_cbts`(`bsk_f96_i7l`,`mg9_hj_jr_nl_q`)",
	).Error; err != nil {
		t.Fatalf("create legacy receipt index: %v", err)
	}

	if err := prepareGatewaySQLiteMigrations(db); err != nil {
		t.Fatalf("prepare legacy migration: %v", err)
	}
	if err := db.AutoMigrate(&ToolExecutionReceipt{}); err != nil {
		t.Fatalf("migrate stable receipt table: %v", err)
	}

	var owner sqliteIndexOwner
	if err := db.Raw(
		"SELECT tbl_name FROM sqlite_master WHERE type = 'index' AND name = ? LIMIT 1",
		toolReceiptUniqueIndexName,
	).Scan(&owner).Error; err != nil {
		t.Fatalf("read migrated index owner: %v", err)
	}
	if owner.TableName != (ToolExecutionReceipt{}).TableName() {
		t.Fatalf(
			"index %s owner = %q, want %q",
			toolReceiptUniqueIndexName,
			owner.TableName,
			(ToolExecutionReceipt{}).TableName(),
		)
	}
}

type agentOSScenarioTool struct {
	name     string
	readOnly bool
	maxBytes int
	calls    atomic.Int32
	execute  func(context.Context, map[string]interface{}, int32) (string, error)
}

func (t *agentOSScenarioTool) Name() string        { return t.name }
func (t *agentOSScenarioTool) Description() string { return "AgentOS scenario tool" }
func (t *agentOSScenarioTool) Parameters() map[string]interface{} {
	return nil
}
func (t *agentOSScenarioTool) IsReadOnly() bool { return t.readOnly }
func (t *agentOSScenarioTool) MaxResultSize() int {
	if t.maxBytes > 0 {
		return t.maxBytes
	}
	return 8000
}
func (t *agentOSScenarioTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	call := t.calls.Add(1)
	if t.execute != nil {
		return t.execute(ctx, params, call)
	}
	return "ok", nil
}

func agentOSScenarioExecutor(tool Tool) *AgentToolExecutor {
	registry := NewToolRegistry()
	registry.Register(tool)
	agent := &LLMAgent{
		name:         "AgentOSScenario",
		toolRegistry: registry,
		fileLogger:   log.New(io.Discard, "", 0),
	}
	return NewAgentToolExecutor(agent)
}

func agentOSToolContext(db *gorm.DB, taskID string) context.Context {
	ctx := context.WithValue(context.Background(), "task_id", taskID)
	return withToolExecutionLedger(ctx, newGormToolExecutionLedger(db))
}

func runAgentOSScenarioTool(executor *AgentToolExecutor, ctx context.Context, call ToolCall) AgentToolRunResult {
	return executor.RunSingleTool(ctx, call, RunSingleToolOptions{
		SkipHooks: true, SkipSafety: true, SkipSnapshot: true,
	})
}

func TestToolApprovalGrantIsExactAndOneShot(t *testing.T) {
	db := openAgentOSScenarioDB(t)
	now := time.Now().Round(0)
	if err := db.Create(&GatewayTaskRecord{
		TaskID: "task-approval-exact", TenantID: "tenant-a", RoleID: "publisher",
		Status: "running", CurrentPhase: "tool", CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	permissions := NewPermissionEngine(PermissionDefault)
	permissions.AddRule(PermissionRule{
		Source: SourcePolicy, Behavior: BehaviorAsk, ToolName: "publish",
	})
	tool := &agentOSScenarioTool{name: "publish", readOnly: false}
	registry := NewToolRegistry()
	registry.Register(tool)
	agent := &LLMAgent{
		name: "ApprovalScenario", toolRegistry: registry,
		permissionEngine: permissions, fileLogger: log.New(io.Discard, "", 0),
	}
	executor := NewAgentToolExecutor(agent)
	newContext := func() context.Context {
		ctx := context.WithValue(context.Background(), "task_id", "task-approval-exact")
		return withToolApprovalBroker(ctx, newGormToolApprovalBroker(db))
	}
	call := ToolCall{
		ID: "publish-1", Name: "publish",
		Arguments: map[string]interface{}{"target": "production"},
	}

	first := executor.RunSingleTool(newContext(), call, RunSingleToolOptions{
		SkipHooks: true, SkipSnapshot: true,
	})
	if _, ok := asApprovalRequiredError(first.Err); !ok || tool.calls.Load() != 0 {
		t.Fatalf("unapproved call=%+v executions=%d", first, tool.calls.Load())
	}
	var approval ApprovalRequestRecord
	if err := db.Where("task_id = ?", "task-approval-exact").First(&approval).Error; err != nil {
		t.Fatal(err)
	}
	if approval.Status != approvalStatusPending {
		t.Fatalf("approval status=%q, want pending", approval.Status)
	}
	if err := db.Model(&ApprovalRequestRecord{}).
		Where("approval_id = ?", approval.ApprovalID).
		Updates(map[string]interface{}{
			"status": approvalStatusApproved, "decided_at": now, "decided_by": "operator",
		}).Error; err != nil {
		t.Fatal(err)
	}

	second := executor.RunSingleTool(newContext(), call, RunSingleToolOptions{
		SkipHooks: true, SkipSnapshot: true,
	})
	if !ToolResultExecutionSucceeded(second.Result, second.Err) || tool.calls.Load() != 1 {
		t.Fatalf("approved call=%+v executions=%d", second, tool.calls.Load())
	}
	if err := db.Where("approval_id = ?", approval.ApprovalID).First(&approval).Error; err != nil {
		t.Fatal(err)
	}
	if approval.Status != approvalStatusConsumed {
		t.Fatalf("approval status=%q, want consumed", approval.Status)
	}

	replay := call
	replay.ID = "publish-2"
	third := executor.RunSingleTool(newContext(), replay, RunSingleToolOptions{
		SkipHooks: true, SkipSnapshot: true,
	})
	if _, ok := asApprovalRequiredError(third.Err); !ok || tool.calls.Load() != 1 {
		t.Fatalf("second side effect did not require a fresh approval: %+v executions=%d", third, tool.calls.Load())
	}

	changed := call
	changed.ID = "publish-3"
	changed.Arguments = map[string]interface{}{"target": "staging"}
	fourth := executor.RunSingleTool(newContext(), changed, RunSingleToolOptions{
		SkipHooks: true, SkipSnapshot: true,
	})
	if _, ok := asApprovalRequiredError(fourth.Err); !ok {
		t.Fatalf("changed arguments should require a distinct approval: %+v", fourth)
	}
	var count int64
	if err := db.Model(&ApprovalRequestRecord{}).
		Where("task_id = ?", "task-approval-exact").Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 3 {
		t.Fatalf("approval records=%d, want 3", count)
	}
}

func TestApprovalPreflightWithoutAgentOSBrokerDoesNotSuspendTask(t *testing.T) {
	permissions := NewPermissionEngine(PermissionDefault)
	permissions.AddRule(PermissionRule{
		Source: SourcePolicy, Behavior: BehaviorAsk, ToolName: "publish",
	})
	tool := &agentOSScenarioTool{name: "publish"}
	registry := NewToolRegistry()
	registry.Register(tool)
	call := ToolCall{
		ID: "interactive-publish", Name: "publish",
		Arguments: map[string]interface{}{"target": "production"},
	}
	if err := preflightToolApprovals(
		context.WithValue(context.Background(), "task_id", "interactive-task"),
		permissions,
		registry,
		NewWorkspaceSandbox(t.TempDir(), SandboxConfig{Mode: SandboxStrict}),
		[]ToolCall{call},
	); err != nil {
		t.Fatalf("interactive preflight should fall through to a tool observation: %v", err)
	}

	registry.SetChain(NewMiddlewareChain(NewExecutionPolicyMiddleware(permissions, nil, nil)))
	result, err := registry.Execute(
		context.WithValue(context.Background(), "task_id", "interactive-task"),
		call,
	)
	if err != nil || result.ErrorCode != "approval_required" || tool.calls.Load() != 0 {
		t.Fatalf("result=%+v err=%v calls=%d", result, err, tool.calls.Load())
	}
}

func TestExpiredApprovalFailsWaitingTask(t *testing.T) {
	db := openAgentOSScenarioDB(t)
	now := time.Now().Round(0)
	expiredAt := now.Add(-time.Minute)
	task := GatewayTaskRecord{
		TaskID: "task-expired-approval", TenantID: "tenant-a",
		Status: "waiting_approval", CurrentPhase: "waiting_approval",
		QueueName: agentOSQueueName, CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Hour),
	}
	if err := db.Create(&task).Error; err != nil {
		t.Fatal(err)
	}
	request := ApprovalRequestRecord{
		ApprovalID: "apr_expired", ApprovalKey: "expired-key",
		Generation: 1,
		TaskID:     task.TaskID, TenantID: task.TenantID,
		ToolName: "publish", ArgumentsHash: strings.Repeat("a", 64),
		Status: approvalStatusPending, RequestedAt: now.Add(-time.Hour),
		ExpiresAt: &expiredAt, CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Hour),
	}
	if err := db.Create(&request).Error; err != nil {
		t.Fatal(err)
	}
	expired, err := expireAgentOSApprovals(db, now)
	if err != nil || expired != 1 {
		t.Fatalf("expired=%d err=%v", expired, err)
	}
	if err := db.Where("approval_id = ?", request.ApprovalID).First(&request).Error; err != nil {
		t.Fatal(err)
	}
	if request.Status != approvalStatusExpired {
		t.Fatalf("approval status=%q", request.Status)
	}
	if err := db.Where("task_id = ?", task.TaskID).First(&task).Error; err != nil {
		t.Fatal(err)
	}
	if task.Status != "failed" || task.CurrentPhase != "approval_expired" ||
		task.CompletedAt == nil || !strings.Contains(task.ErrorMsg, "expired") {
		t.Fatalf("expired task=%+v", task)
	}
}

func loadAgentOSToolReceipt(t *testing.T, db *gorm.DB, taskID, callID string) ToolExecutionReceipt {
	t.Helper()
	var receipt ToolExecutionReceipt
	if err := db.Where("task_id = ? AND tool_call_id = ?", taskID, callID).First(&receipt).Error; err != nil {
		t.Fatalf("load receipt %s/%s: %v", taskID, callID, err)
	}
	return receipt
}

func TestAgentOSToolReceiptScenarioMatrix(t *testing.T) {
	t.Run("successful mutating replay executes external action once", func(t *testing.T) {
		db := openAgentOSScenarioDB(t)
		tool := &agentOSScenarioTool{name: "publish", readOnly: false, execute: func(context.Context, map[string]interface{}, int32) (string, error) {
			return "published", nil
		}}
		executor := agentOSScenarioExecutor(tool)
		ctx := agentOSToolContext(db, "task-success-replay")
		call := ToolCall{ID: "call-publish", Name: "publish", Arguments: map[string]interface{}{"target": "prod"}}

		first := runAgentOSScenarioTool(executor, ctx, call)
		second := runAgentOSScenarioTool(executor, ctx, call)
		if !ToolResultExecutionSucceeded(first.Result, first.Err) || !ToolResultExecutionSucceeded(second.Result, second.Err) {
			t.Fatalf("expected both calls to report success: first=%+v second=%+v", first, second)
		}
		if got := tool.calls.Load(); got != 1 {
			t.Fatalf("external executions = %d, want 1", got)
		}
		if !strings.Contains(second.Result.Content, "published") {
			t.Fatalf("cached result missing original content: %q", second.Result.Content)
		}
		if receipt := loadAgentOSToolReceipt(t, db, "task-success-replay", call.ID); receipt.Status != "succeeded" {
			t.Fatalf("receipt status = %q, want succeeded", receipt.Status)
		}
	})

	t.Run("same call id with changed arguments is rejected", func(t *testing.T) {
		db := openAgentOSScenarioDB(t)
		tool := &agentOSScenarioTool{name: "write", readOnly: false}
		executor := agentOSScenarioExecutor(tool)
		ctx := agentOSToolContext(db, "task-arg-drift")
		first := ToolCall{ID: "stable-id", Name: "write", Arguments: map[string]interface{}{"path": "a.txt"}}
		changed := ToolCall{ID: "stable-id", Name: "write", Arguments: map[string]interface{}{"path": "b.txt"}}

		_ = runAgentOSScenarioTool(executor, ctx, first)
		run := runAgentOSScenarioTool(executor, ctx, changed)
		if run.Err == nil || !strings.Contains(run.Err.Error(), "different arguments") {
			t.Fatalf("changed arguments were not rejected: %+v", run)
		}
		if got := tool.calls.Load(); got != 1 {
			t.Fatalf("external executions = %d, want 1", got)
		}
	})

	t.Run("same call id is isolated across tasks", func(t *testing.T) {
		db := openAgentOSScenarioDB(t)
		tool := &agentOSScenarioTool{name: "write", readOnly: false}
		executor := agentOSScenarioExecutor(tool)
		call := ToolCall{ID: "shared-call", Name: "write", Arguments: map[string]interface{}{"path": "same.txt"}}

		_ = runAgentOSScenarioTool(executor, agentOSToolContext(db, "task-one"), call)
		_ = runAgentOSScenarioTool(executor, agentOSToolContext(db, "task-two"), call)
		if got := tool.calls.Load(); got != 2 {
			t.Fatalf("external executions = %d, want 2", got)
		}
	})

	t.Run("different call ids in one task remain independent", func(t *testing.T) {
		db := openAgentOSScenarioDB(t)
		tool := &agentOSScenarioTool{name: "write", readOnly: false}
		executor := agentOSScenarioExecutor(tool)
		ctx := agentOSToolContext(db, "task-independent-calls")

		_ = runAgentOSScenarioTool(executor, ctx, ToolCall{ID: "call-a", Name: "write"})
		_ = runAgentOSScenarioTool(executor, ctx, ToolCall{ID: "call-b", Name: "write"})
		if got := tool.calls.Load(); got != 2 {
			t.Fatalf("external executions = %d, want 2", got)
		}
	})

	t.Run("orphan mutating call is interrupted and blocked", func(t *testing.T) {
		db := openAgentOSScenarioDB(t)
		ledger := newGormToolExecutionLedger(db)
		call := ToolCall{ID: "orphan-write", Name: "write", Arguments: map[string]interface{}{"value": 7}}
		decision, err := ledger.Begin(context.Background(), "task-orphan-write", call, false)
		if err != nil || !decision.Execute {
			t.Fatalf("seed running receipt: decision=%+v err=%v", decision, err)
		}
		if recovered, err := recoverOrphanToolReceipts(db, time.Now()); err != nil || recovered != 1 {
			t.Fatalf("recover receipts = %d, %v; want 1, nil", recovered, err)
		}

		tool := &agentOSScenarioTool{name: "write", readOnly: false}
		run := runAgentOSScenarioTool(agentOSScenarioExecutor(tool), agentOSToolContext(db, "task-orphan-write"), call)
		if run.Err == nil || !strings.Contains(run.Err.Error(), "interrupted") {
			t.Fatalf("orphan mutating call was not blocked: %+v", run)
		}
		if got := tool.calls.Load(); got != 0 {
			t.Fatalf("external executions = %d, want 0", got)
		}
	})

	t.Run("orphan read-only call can recover by retry", func(t *testing.T) {
		db := openAgentOSScenarioDB(t)
		ledger := newGormToolExecutionLedger(db)
		call := ToolCall{ID: "orphan-read", Name: "read", Arguments: map[string]interface{}{"key": "x"}}
		if _, err := ledger.Begin(context.Background(), "task-orphan-read", call, true); err != nil {
			t.Fatal(err)
		}
		if _, err := recoverOrphanToolReceipts(db, time.Now()); err != nil {
			t.Fatal(err)
		}

		tool := &agentOSScenarioTool{name: "read", readOnly: true}
		run := runAgentOSScenarioTool(agentOSScenarioExecutor(tool), agentOSToolContext(db, "task-orphan-read"), call)
		if !ToolResultExecutionSucceeded(run.Result, run.Err) || tool.calls.Load() != 1 {
			t.Fatalf("read-only recovery failed: run=%+v calls=%d", run, tool.calls.Load())
		}
		if receipt := loadAgentOSToolReceipt(t, db, "task-orphan-read", call.ID); receipt.Status != "succeeded" {
			t.Fatalf("receipt status = %q, want succeeded", receipt.Status)
		}
	})

	t.Run("failed mutating call is not guessed safe to replay", func(t *testing.T) {
		db := openAgentOSScenarioDB(t)
		tool := &agentOSScenarioTool{name: "charge", readOnly: false, execute: func(context.Context, map[string]interface{}, int32) (string, error) {
			return "provider may have accepted charge", errors.New("connection dropped after send")
		}}
		executor := agentOSScenarioExecutor(tool)
		ctx := agentOSToolContext(db, "task-failed-write")
		call := ToolCall{ID: "charge-1", Name: "charge"}

		first := runAgentOSScenarioTool(executor, ctx, call)
		if ToolResultExecutionSucceeded(first.Result, first.Err) {
			t.Fatalf("first uncertain call unexpectedly succeeded: %+v", first)
		}
		second := runAgentOSScenarioTool(executor, ctx, call)
		if second.Err == nil || !strings.Contains(second.Err.Error(), "automatic replay is blocked") {
			t.Fatalf("uncertain mutating replay was not blocked: %+v", second)
		}
		if got := tool.calls.Load(); got != 1 {
			t.Fatalf("external executions = %d, want 1", got)
		}
	})

	t.Run("failed read-only call retries and succeeds", func(t *testing.T) {
		db := openAgentOSScenarioDB(t)
		tool := &agentOSScenarioTool{name: "lookup", readOnly: true, execute: func(_ context.Context, _ map[string]interface{}, call int32) (string, error) {
			if call == 1 {
				return "", errors.New("temporary read failure")
			}
			return "fresh value", nil
		}}
		executor := agentOSScenarioExecutor(tool)
		ctx := agentOSToolContext(db, "task-read-retry")
		call := ToolCall{ID: "lookup-1", Name: "lookup"}

		first := runAgentOSScenarioTool(executor, ctx, call)
		second := runAgentOSScenarioTool(executor, ctx, call)
		if ToolResultExecutionSucceeded(first.Result, first.Err) || !ToolResultExecutionSucceeded(second.Result, second.Err) {
			t.Fatalf("unexpected retry outcomes: first=%+v second=%+v", first, second)
		}
		if got := tool.calls.Load(); got != 2 {
			t.Fatalf("external executions = %d, want 2", got)
		}
	})

	t.Run("read-only classification drift blocks recovery", func(t *testing.T) {
		db := openAgentOSScenarioDB(t)
		ledger := newGormToolExecutionLedger(db)
		call := ToolCall{ID: "classification-drift", Name: "maybe_read"}
		if _, err := ledger.Begin(context.Background(), "task-classification", call, true); err != nil {
			t.Fatal(err)
		}
		if _, err := recoverOrphanToolReceipts(db, time.Now()); err != nil {
			t.Fatal(err)
		}

		changed := &agentOSScenarioTool{name: "maybe_read", readOnly: false}
		run := runAgentOSScenarioTool(agentOSScenarioExecutor(changed), agentOSToolContext(db, "task-classification"), call)
		if run.Err == nil || !strings.Contains(run.Err.Error(), "changed read-only classification") {
			t.Fatalf("classification drift was not blocked: %+v", run)
		}
		if changed.calls.Load() != 0 {
			t.Fatal("classification-drift call reached external tool")
		}
	})

	t.Run("tool panic becomes failed receipt and cannot repeat side effect", func(t *testing.T) {
		db := openAgentOSScenarioDB(t)
		tool := &agentOSScenarioTool{name: "panic_write", readOnly: false, execute: func(context.Context, map[string]interface{}, int32) (string, error) {
			panic("simulated driver panic")
		}}
		executor := agentOSScenarioExecutor(tool)
		ctx := agentOSToolContext(db, "task-panic")
		call := ToolCall{ID: "panic-call", Name: "panic_write"}

		first := runAgentOSScenarioTool(executor, ctx, call)
		if first.Err == nil || !strings.Contains(first.Err.Error(), "panicked") {
			t.Fatalf("panic was not isolated: %+v", first)
		}
		if receipt := loadAgentOSToolReceipt(t, db, "task-panic", call.ID); receipt.Status != "failed" {
			t.Fatalf("panic receipt status = %q, want failed", receipt.Status)
		}
		second := runAgentOSScenarioTool(executor, ctx, call)
		if second.Err == nil || tool.calls.Load() != 1 {
			t.Fatalf("panic replay not blocked: run=%+v calls=%d", second, tool.calls.Load())
		}
	})

	t.Run("canceled read-only call records failure then retries", func(t *testing.T) {
		db := openAgentOSScenarioDB(t)
		started := make(chan struct{})
		tool := &agentOSScenarioTool{name: "cancelable_read", readOnly: true, execute: func(ctx context.Context, _ map[string]interface{}, call int32) (string, error) {
			if call == 1 {
				close(started)
				<-ctx.Done()
				return "", ctx.Err()
			}
			return "recovered", nil
		}}
		executor := agentOSScenarioExecutor(tool)
		base := agentOSToolContext(db, "task-cancel-read")
		canceled, cancel := context.WithCancel(base)
		call := ToolCall{ID: "cancel-read", Name: "cancelable_read"}

		firstDone := make(chan AgentToolRunResult, 1)
		go func() { firstDone <- runAgentOSScenarioTool(executor, canceled, call) }()
		select {
		case <-started:
			cancel()
		case <-time.After(3 * time.Second):
			cancel()
			t.Fatal("cancelable read did not start")
		}
		var first AgentToolRunResult
		select {
		case first = <-firstDone:
		case <-time.After(3 * time.Second):
			t.Fatal("canceled read did not return")
		}
		if ToolResultExecutionSucceeded(first.Result, first.Err) {
			t.Fatalf("canceled call unexpectedly succeeded: %+v", first)
		}
		second := runAgentOSScenarioTool(executor, base, call)
		if !ToolResultExecutionSucceeded(second.Result, second.Err) || tool.calls.Load() != 2 {
			t.Fatalf("canceled read recovery failed: run=%+v calls=%d", second, tool.calls.Load())
		}
	})

	t.Run("large multibyte result is cached as valid UTF-8", func(t *testing.T) {
		db := openAgentOSScenarioDB(t)
		large := strings.Repeat("界", 30000)
		ledger := newGormToolExecutionLedger(db)
		call := ToolCall{ID: "large-call", Name: "large_read"}
		if decision, err := ledger.Begin(context.Background(), "task-large-result", call, true); err != nil || !decision.Execute {
			t.Fatalf("begin large result receipt: decision=%+v err=%v", decision, err)
		}
		succeeded := true
		if err := ledger.Finish(context.Background(), "task-large-result", call, ToolResult{
			ToolCallID: call.ID, Name: call.Name, Content: large, ExecutionSucceeded: &succeeded,
		}, nil, 15); err != nil {
			t.Fatal(err)
		}
		replay, err := ledger.Begin(context.Background(), "task-large-result", call, true)
		if err != nil || replay.Cached == nil {
			t.Fatalf("load cached large result: decision=%+v err=%v", replay, err)
		}
		receipt := loadAgentOSToolReceipt(t, db, "task-large-result", call.ID)
		if !receipt.ResultTruncated || len(receipt.ResultContent) > maxStoredToolReceiptResult || !utf8.ValidString(receipt.ResultContent) {
			t.Fatalf("invalid truncated receipt: truncated=%v bytes=%d utf8=%v", receipt.ResultTruncated, len(receipt.ResultContent), utf8.ValidString(receipt.ResultContent))
		}
		if !utf8.ValidString(replay.Cached.Content) || !strings.Contains(replay.Cached.Content, "cached tool result was truncated") {
			t.Fatalf("cached result lacks valid truncation marker: utf8=%v tail=%q", utf8.ValidString(replay.Cached.Content), truncateUTF8Bytes(replay.Cached.Content, 120))
		}
	})

	t.Run("receipt persistence preserves tool account information", func(t *testing.T) {
		db := openAgentOSScenarioDB(t)
		secret := "sk-openkitty-agent-os-synthetic-0123456789abcdef"
		tool := &agentOSScenarioTool{name: "secret_read", readOnly: true, execute: func(context.Context, map[string]interface{}, int32) (string, error) {
			return "credential=" + secret, nil
		}}
		call := ToolCall{ID: "secret-call", Name: "secret_read"}
		_ = runAgentOSScenarioTool(agentOSScenarioExecutor(tool), agentOSToolContext(db, "task-secret"), call)

		receipt := loadAgentOSToolReceipt(t, db, "task-secret", call.ID)
		if !strings.Contains(receipt.ResultContent, secret) {
			t.Fatalf("workflow receipt lost the original tool result: %q", receipt.ResultContent)
		}
		if strings.Contains(receipt.ResultContent, "[REDACTED]") || strings.Contains(receipt.ResultContent, "[SCRUBBED]") {
			t.Fatalf("workflow receipt was forcibly redacted: %q", receipt.ResultContent)
		}
	})

	t.Run("concurrent duplicate mutating call cannot run twice", func(t *testing.T) {
		db := openAgentOSScenarioDB(t)
		started := make(chan struct{})
		release := make(chan struct{})
		var once sync.Once
		tool := &agentOSScenarioTool{name: "slow_publish", readOnly: false, execute: func(context.Context, map[string]interface{}, int32) (string, error) {
			once.Do(func() { close(started) })
			<-release
			return "published once", nil
		}}
		executor := agentOSScenarioExecutor(tool)
		ctx := agentOSToolContext(db, "task-concurrent-replay")
		call := ToolCall{ID: "same-call", Name: "slow_publish"}
		firstDone := make(chan AgentToolRunResult, 1)
		go func() { firstDone <- runAgentOSScenarioTool(executor, ctx, call) }()
		select {
		case <-started:
		case <-time.After(3 * time.Second):
			t.Fatal("first mutating call did not start")
		}

		duplicate := runAgentOSScenarioTool(executor, ctx, call)
		if duplicate.Err == nil || !strings.Contains(duplicate.Err.Error(), "previous running receipt") {
			t.Fatalf("concurrent duplicate was not blocked: %+v", duplicate)
		}
		close(release)
		select {
		case first := <-firstDone:
			if !ToolResultExecutionSucceeded(first.Result, first.Err) {
				t.Fatalf("original call failed: %+v", first)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("original call did not finish")
		}
		if got := tool.calls.Load(); got != 1 {
			t.Fatalf("external executions = %d, want 1", got)
		}
	})

	t.Run("orphan recovery is idempotent", func(t *testing.T) {
		db := openAgentOSScenarioDB(t)
		ledger := newGormToolExecutionLedger(db)
		if _, err := ledger.Begin(context.Background(), "task-idempotent-recovery", ToolCall{ID: "call", Name: "read"}, true); err != nil {
			t.Fatal(err)
		}
		first, err := recoverOrphanToolReceipts(db, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		second, err := recoverOrphanToolReceipts(db, time.Now().Add(time.Minute))
		if err != nil || first != 1 || second != 0 {
			t.Fatalf("recovery counts = %d then %d, err=%v; want 1 then 0", first, second, err)
		}
	})
}

func TestAgentOSCombinedRestartAndSideEffectRecoveryScenario(t *testing.T) {
	db := openAgentOSScenarioDB(t)
	now := time.Now().Add(-time.Minute)
	task := &GatewayTask{
		ID: "task-combined-restart", Status: "running", CurrentPhase: "tool:publish", RunAttempt: 1,
		CreatedAt: now, HeartbeatAt: &now, CallerID: "scheduler",
		Request: &MessageRequest{TaskID: "task-combined-restart", TaskType: "chat", SessionId: "session-combined", AgentName: "Researcher"},
	}
	if err := SaveGatewayTaskToDB(db, task); err != nil {
		t.Fatal(err)
	}
	ledger := newGormToolExecutionLedger(db)
	readCall := ToolCall{ID: "read-before-crash", Name: "lookup"}
	writeCall := ToolCall{ID: "write-before-crash", Name: "publish"}
	if _, err := ledger.Begin(context.Background(), task.ID, readCall, true); err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.Begin(context.Background(), task.ID, writeCall, false); err != nil {
		t.Fatal(err)
	}
	recoveredAt := time.Now().Round(0)
	if recovered, err := recoverOrphanGatewayTasks(db, recoveredAt); err != nil || recovered != 1 {
		t.Fatalf("recover task = %d, %v", recovered, err)
	}
	if recovered, err := recoverOrphanToolReceipts(db, recoveredAt); err != nil || recovered != 2 {
		t.Fatalf("recover receipts = %d, %v", recovered, err)
	}

	readTool := &agentOSScenarioTool{name: "lookup", readOnly: true}
	readRun := runAgentOSScenarioTool(agentOSScenarioExecutor(readTool), agentOSToolContext(db, task.ID), readCall)
	if !ToolResultExecutionSucceeded(readRun.Result, readRun.Err) || readTool.calls.Load() != 1 {
		t.Fatalf("read-only restart recovery failed: run=%+v calls=%d", readRun, readTool.calls.Load())
	}
	writeTool := &agentOSScenarioTool{name: "publish", readOnly: false}
	writeRun := runAgentOSScenarioTool(agentOSScenarioExecutor(writeTool), agentOSToolContext(db, task.ID), writeCall)
	if writeRun.Err == nil || writeTool.calls.Load() != 0 {
		t.Fatalf("uncertain write crossed restart boundary: run=%+v calls=%d", writeRun, writeTool.calls.Load())
	}

	var record GatewayTaskRecord
	if err := db.Where("task_id = ?", task.ID).First(&record).Error; err != nil {
		t.Fatal(err)
	}
	if record.Status != "failed" || record.CurrentPhase != "interrupted" || record.HeartbeatAt == nil || !record.HeartbeatAt.Equal(recoveredAt) {
		t.Fatalf("unexpected recovered task: status=%q phase=%q heartbeat=%v", record.Status, record.CurrentPhase, record.HeartbeatAt)
	}
	if record.ErrorMsg != orphanGatewayTaskError {
		t.Fatalf("recovery error = %q", record.ErrorMsg)
	}
}

func TestAgentTaskIdempotencyHashPartitionsSubjectsAndPreservesLegacyForm(t *testing.T) {
	legacy := agentTaskIdempotencyHashForTenant("tenant-a", "crm", "shared-key")
	if legacy != agentTaskIdempotencyHashForTenant("tenant-a", "crm", "", "shared-key") {
		t.Fatal("empty subject changed the legacy idempotency namespace")
	}
	alice := agentTaskIdempotencyHashForTenant("tenant-a", "crm", "alice", "shared-key")
	bob := agentTaskIdempotencyHashForTenant("tenant-a", "crm", "bob", "shared-key")
	if alice == bob || alice == legacy || bob == legacy {
		t.Fatalf("subject idempotency namespaces collided: legacy=%s alice=%s bob=%s", legacy, alice, bob)
	}
	if alice != agentTaskIdempotencyHashForTenant("tenant-a", "crm", "alice", "shared-key") {
		t.Fatal("subject idempotency hash is not deterministic")
	}
}
