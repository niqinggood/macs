package app

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"

	"mac/internal/globalstate"
	memorycore "mac/internal/memory"
	"mac/internal/security"
)

// Memory settings affect constructed runtime objects, not just model options.
func memorySettingsChanged(old, next *Config) bool {
	if old == nil || next == nil || !reflect.DeepEqual(old.MemoryConfig, next.MemoryConfig) {
		return true
	}
	for _, agent := range old.Agents {
		if agentMemorySettingsChanged(old, next, agent.Name) {
			return true
		}
	}
	return false
}

func agentMemorySettingsChanged(old, next *Config, name string) bool {
	if old == nil || next == nil || !reflect.DeepEqual(old.MemoryConfig, next.MemoryConfig) {
		return true
	}
	var previous, updated *AgentConfig
	for i := range old.Agents {
		if old.Agents[i].Name == name {
			previous = &old.Agents[i]
			break
		}
	}
	for i := range next.Agents {
		if next.Agents[i].Name == name {
			updated = &next.Agents[i]
			break
		}
	}
	return previous == nil || updated == nil ||
		!reflect.DeepEqual(previous.HasMemory, updated.HasMemory) ||
		!reflect.DeepEqual(previous.MemoryWindow, updated.MemoryWindow)
}

// Removal is allowed with memory disabled, but isolated internal executions
// cannot inspect or mutate the user's persisted memory through tools.
func (a *LLMAgent) allowMemoryTool(ctx context.Context, removal bool) bool {
	return a != nil && !isInternalMemoryExecutionTurn(ctx) && (a.hasMemory || removal)
}

func (a *LLMAgent) buildKnowledgeMemoryPrompt(ctx context.Context, sessionID string) string {
	if a == nil || !a.hasMemory || isInternalMemoryExecutionTurn(ctx) || isRestrictedTenantExecution(ctx) || globalKG == nil || !globalKG.BelongsToWorkspace(a.workspace) {
		return ""
	}
	prompt := globalKG.ProactiveMemoryForSession(sessionID) + globalKG.BuildContextPromptForSession(sessionID, 50)
	if prompt == "" {
		return ""
	}
	cleaned, ok := security.ScanMemoryRecall(globalstate.PromptInjectionDetector(), "knowledge_memory:"+sessionID, prompt)
	if !ok {
		return ""
	}
	return cleaned
}

func (a *LLMAgent) memoryOwner(ctx context.Context) (memorycore.Owner, error) {
	if a == nil || isInternalMemoryExecutionTurn(ctx) {
		return memorycore.Owner{}, fmt.Errorf("memory unavailable for isolated execution")
	}
	owner := memorycore.Owner{TenantID: defaultExecutionTenantID, PartitionID: memoryTenantIDFromContext(ctx), ImportLegacy: true}
	if ctx != nil {
		owner.SessionID, _ = ctx.Value("session_id").(string)
	}
	if identity, ok := executionIdentityFromContext(ctx); ok && identity.TenantScoped {
		if strings.TrimSpace(identity.SubjectID) == "" {
			return owner, fmt.Errorf("personal memory requires an authenticated subject")
		}
		if err := enforcePersonalWorkspacePath(ctx, a.workspace, "."); err != nil {
			return owner, err
		}
		// A matching run root alone is insufficient: a host accidentally reusing
		// Alice's agent for Bob must not import Alice's JSON or SCM state.
		expected := "usr_" + security.PersonalPartitionID(identity.TenantID, identity.SubjectID, defaultExecutionTenantID)
		root := filepath.Clean(a.workspace)
		if filepath.Base(root) != expected || filepath.Base(filepath.Dir(root)) != "users" || filepath.Base(filepath.Dir(filepath.Dir(root))) != ".openkitty" {
			return owner, fmt.Errorf("memory workspace does not belong to the authenticated subject")
		}
		owner.TenantID = identity.TenantID
		owner.SubjectID = identity.SubjectID
	}
	return owner, nil
}

func (a *LLMAgent) buildMemoryPrompt(ctx context.Context, userName, sessionID, question string) string {
	if a == nil || !a.hasMemory || a.memoryService == nil || isInternalMemoryExecutionTurn(ctx) {
		return ""
	}
	owner, err := a.memoryOwner(ctx)
	if err != nil {
		a.logMemoryError(err)
		return ""
	}
	owner.SessionID = sessionID
	var supplemental []memorycore.RecallItem
	if webMemoryAudienceFromContext(ctx) != nil {
		var sourceErr error
		supplemental, sourceErr = a.webMemorySources(ctx, sessionID)
		if sourceErr != nil {
			a.logMemoryError(sourceErr)
		}
	} else if channel, _ := ctx.Value("channel").(string); channel != "web" {
		if a.memory != nil {
			supplemental = append(supplemental, memorycore.RecallItem{ID: "history-profile", Source: "history_and_profile", Rank: 60,
				Content: a.memory.GetMemoryContextForTenant(owner.PartitionID, userName, sessionID, question)})
		}
		supplemental = append(supplemental,
			memorycore.RecallItem{ID: "graph", Source: "knowledge_graph", Rank: 20, Content: a.buildKnowledgeMemoryPrompt(ctx, sessionID)})
	}
	result, err := a.memoryService.Recall(ctx, owner, question, memorycore.DefaultRecallTokens, supplemental)
	if err != nil {
		a.logMemoryError(err)
		return ""
	}
	status := "ready"
	cleaned := result.Prompt
	if result.Prompt == "" {
		status = "empty"
	} else {
		var ok bool
		cleaned, ok = security.ScanMemoryRecall(globalstate.PromptInjectionDetector(), "unified_memory:"+sessionID, result.Prompt)
		if !ok {
			status, cleaned = "blocked", ""
		} else if cleaned != result.Prompt {
			status = "redacted"
		}
	}
	turnRef, _ := ctx.Value("task_id").(string)
	if err := a.memoryService.SaveRecallTrace(ctx, owner, turnRef, status, result); err != nil {
		a.logMemoryError(err)
	}
	if status == "ready" || status == "redacted" {
		for _, item := range result.Items {
			block := fmt.Sprintf("\n[%s; id=%s; version=%d; source_session=%s]\n%s\n", item.Source, item.ID, item.Version, item.SourceSessionID, item.Content)
			// The scanner may remove one source without removing the whole
			// envelope. Register only representations that survived unchanged.
			if !strings.Contains(cleaned, block) {
				continue
			}
			registerPromptMemoryCandidate(ctx, promptMemoryRecallSource(item), "user", block)
		}
	}
	return cleaned
}

func (a *LLMAgent) logMemoryError(err error) {
	if err != nil && a != nil && a.fileLogger != nil {
		a.fileLogger.Printf("[Memory] %v", err)
	}
}

// Use only the host-frozen original user turn, before composing the memory
// envelope. Model output and isolated workflow inputs cannot become explicit
// personal instructions through this path.
func (a *LLMAgent) recordExplicitUserMemory(ctx context.Context) {
	if a == nil || !a.hasMemory || a.memoryService == nil || isInternalMemoryExecutionTurn(ctx) {
		return
	}
	owner, err := a.memoryOwner(ctx)
	if err == nil {
		owner.ImportLegacy = false
		_, err = a.memoryService.RecordExplicitUserTurn(explicitMemoryWriteContext(ctx), owner)
	}
	a.logMemoryError(err)
}

type memoryProcessingPayload struct {
	Messages []map[string]interface{} `json:"messages"`
	Input    string                   `json:"input,omitempty"`
	Output   string                   `json:"output,omitempty"`
}

func (a *LLMAgent) enqueueMemoryJob(ctx context.Context, sessionID, kind string, payload memoryProcessingPayload) error {
	owner, err := a.memoryOwner(ctx)
	if err != nil {
		return err
	}
	owner.SessionID = sessionID
	return a.memoryService.Enqueue(ctx, owner, kind, payload)
}

func (a *LLMAgent) processMemoryJobs(ctx context.Context) {
	if a == nil || !a.hasMemory || a.memoryService == nil || a.scm == nil || isInternalMemoryExecutionTurn(ctx) {
		return
	}
	if webMemoryAudienceFromContext(ctx) != nil {
		// Legacy snapshots do not carry reliable per-message Agent/QA mode.
		// The Web host now dispatches only its sealed terminal review outbox.
		return
	}
	owner, err := a.memoryOwner(ctx)
	if err != nil {
		a.logMemoryError(err)
		return
	}
	sem := a.scm.DerivationSemaphore()
	select {
	case sem <- struct{}{}:
	default:
		return
	}
	go func() {
		defer func() { <-sem }()
		err := a.memoryService.ProcessPending(ctx, owner, func(workCtx context.Context, job memorycore.ProcessingJob) error {
			if !a.hasMemory || isInternalMemoryExecutionTurn(workCtx) {
				return fmt.Errorf("memory disabled")
			}
			var payload memoryProcessingPayload
			if err := json.Unmarshal([]byte(job.Payload), &payload); err != nil {
				return memorycore.PermanentProcessingError(err)
			}
			workCtx = context.WithValue(workCtx, "session_id", job.SessionID)
			if job.Kind == "summary" {
				if a.sessionMemory == nil {
					return fmt.Errorf("summary extractor unavailable")
				}
				extractor := a.sessionMemory.ForSession(job.SessionID)
				jobOwner := owner
				jobOwner.SessionID = job.SessionID
				return extractor.ExtractContextWithStore(workCtx, payload.Messages, a.name,
					func(ctx context.Context) (string, error) { return a.memoryService.LoadSummary(ctx, jobOwner) },
					func(ctx context.Context, content string) error {
						return a.memoryService.SaveSummary(ctx, jobOwner, content)
					})
			}
			if isRestrictedTenantExecution(workCtx) || globalKG == nil || !globalKG.BelongsToWorkspace(a.workspace) {
				return fmt.Errorf("knowledge graph unavailable for this owner")
			}
			deriver, _ := a.getMemoryDeriver(globalKG)
			return deriver.DeriveContextResult(workCtx, job.SessionID, payload.Input, payload.Output, payload.Messages)
		})
		a.logMemoryError(err)
	}()
}
