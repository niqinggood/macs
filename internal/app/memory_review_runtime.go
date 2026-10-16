package app

import (
	"context"
	"encoding/json"
	"os"
	"sync"
	"time"

	memorycore "mac/internal/memory"
	"mac/internal/modelruntime"
	"mac/internal/projectctx"

	"gorm.io/gorm"
)

type memoryReviewCaptureKey struct{}

type memoryReviewCapture struct {
	mu      sync.Mutex
	service *memorycore.Service
	owner   memorycore.Owner
	turn    memorycore.ReviewTurn
	agent   string
	ctx     context.Context
}

// Only a host-sealed ordinary Web Agent task creates this sidecar. Generate
// records its original turn and receipts, but cannot authorize a terminal write.
func (g *GatewayService) beginTaskMemoryReview(ctx context.Context, task *GatewayTask, agent string) context.Context {
	if task == nil || task.Request == nil || task.QueueName != "" || task.Request.Channel != "web" || task.webMemoryAudience == nil ||
		task.Request.TaskType != "" && task.Request.TaskType != "chat" && task.Request.TaskType != "skill" {
		return ctx
	}
	capture := &memoryReviewCapture{agent: agent, ctx: ctx}
	g.tasksMu.Lock()
	task.memoryReview = capture
	g.tasksMu.Unlock()
	return context.WithValue(ctx, memoryReviewCaptureKey{}, capture)
}

func (a *LLMAgent) captureTaskMemoryReview(ctx context.Context, event *memorycore.MemoryEvent) {
	if a == nil || !a.hasMemory || a.memoryService == nil || event == nil || isInternalMemoryExecutionTurn(ctx) || isRestrictedTenantExecution(ctx) {
		return
	}
	capture, _ := ctx.Value(memoryReviewCaptureKey{}).(*memoryReviewCapture)
	if capture == nil || capture.agent != a.name || webMemoryAudienceFromContext(ctx) == nil {
		return
	}
	evidence, _ := ctx.Value(explicitMemoryTurnKey{}).(explicitMemoryTurnEvidence)
	if evidence.input == "" {
		return // Nested Generate and augmented prompts cannot supply evidence.
	}
	owner, err := a.memoryOwner(ctx)
	if err != nil {
		return
	}
	owner.ImportLegacy = false
	turn := memorycore.ReviewTurn{TaskID: taskIDFromContext(ctx), SessionID: owner.SessionID, Mode: "agent", Input: evidence.input}
	if selected := webModelSelectionFromContext(ctx); selected != nil {
		turn.ModelAlias = selected.alias
	}
	var calls []struct {
		Type    string `json:"type"`
		Name    string `json:"name"`
		Success *bool  `json:"success"`
		Output  string `json:"output_summary"`
		Error   string `json:"error"`
	}
	if json.Unmarshal(event.CallChain, &calls) == nil {
		for _, call := range calls {
			if call.Type != "tool" || call.Success == nil || len(turn.Receipts) >= 20 {
				continue
			}
			result := call.Output
			if !*call.Success {
				result = call.Error
			}
			turn.Receipts = append(turn.Receipts, memorycore.ReviewReceipt{Name: call.Name, Success: *call.Success, Result: result})
		}
	}
	capture.mu.Lock()
	capture.service, capture.owner, capture.turn = a.memoryService, owner, turn
	capture.mu.Unlock()
}

func (capture *memoryReviewCapture) enqueue(tx *gorm.DB, terminal *GatewayTask) error {
	if capture == nil || terminal == nil {
		return nil
	}
	capture.mu.Lock()
	service, owner, turn := capture.service, capture.owner, capture.turn
	capture.mu.Unlock()
	if service == nil || turn.TaskID != terminal.ID {
		return nil
	}
	turn.Outcome, turn.Output = terminal.Status, gatewayResultText(terminal.Result)
	prepared, err := service.PrepareReview(capture.ctx, owner, turn)
	if err != nil {
		// Unsupported/oversized turns remain ordinary task history.
		return nil
	}
	return prepared.Enqueue(capture.ctx, tx)
}

func (g *GatewayService) runMemoryReviews(ctx context.Context) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		if g.engine != nil && g.engine.agentFactory != nil && g.db != nil {
			factory := g.engine.agentFactory
			factory.mu.RLock()
			var agents []*LLMAgent
			for _, agent := range factory.agents {
				if runtime, ok := agent.(*LLMAgent); ok && runtime.hasMemory && runtime.modelConfig != nil {
					agents = append(agents, runtime)
				}
			}
			factory.mu.RUnlock()
			for _, agent := range agents {
				if ctx.Err() != nil {
					return
				}
				workspace := g.currentProjectWorkspace()
				service := memorycore.NewService(g.db, workspace, agent.name, memorycore.WithReviewAllowed(func() bool {
					factory.mu.RLock()
					current := factory.agents[agent.name] == agent
					factory.mu.RUnlock()
					return current && agent.hasMemory && projectctx.SameWorkspace(workspace, g.currentProjectWorkspace())
				}))
				owner := memorycore.Owner{TenantID: defaultExecutionTenantID, PartitionID: defaultExecutionTenantID}
				err := service.ProcessReviews(ctx, owner, func(callCtx context.Context, messages []map[string]interface{}, limit int) (string, error) {
					if !agent.hasMemory || !projectctx.SameWorkspace(workspace, g.currentProjectWorkspace()) {
						return "", context.Canceled
					}
					route := memorycore.ReviewRouteFromContext(callCtx)
					g.tasksMu.RLock()
					task := cloneGatewayTask(g.tasks[route.TaskID])
					g.tasksMu.RUnlock()
					model := *agent.modelConfig
					if task != nil && task.Request != nil && task.Request.Channel == "web" && task.QueueName == "" &&
						projectctx.SameWorkspace(task.ResolvedWorkspace, workspace) && (task.Request.AgentName == "" || task.Request.AgentName == agent.name) {
						callCtx = task.platformAccount.Context(callCtx)
						if task.webModelSelection != nil {
							model = task.webModelSelection.model
						}
					} else if route.ModelAlias != "" {
						config, err := LoadConfig(g.configpath)
						if err != nil {
							return "", err
						}
						configured, exists := config.ModelConfig[route.ModelAlias]
						if !exists || !isModelConfigAvailable(configured) {
							return "", modelruntime.ErrPlatformLoginRequired
						}
						model = configured
					}
					// AccountAuth checks the ORIGINAL captured login. A recovered
					// task has no login sidecar and cannot use another user's account.
					if model.APIKey == "" && model.APIKeyEnv != "" {
						model.APIKey = os.Getenv(model.APIKeyEnv)
					}
					policy := agent.resolvedLLMCallPolicyForContext(callCtx)
					policy.MaxAttempts = 1 // Durable queue owns retries and metering.
					return modelruntime.CallMessages(callCtx, agent.httpClient, completionConfig(&model), messages, limit, 0.2, policy)
				})
				if err != nil && ctx.Err() == nil && g.logger != nil {
					g.logger.Printf("[Memory] Agent review deferred: %v", err)
				}
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
