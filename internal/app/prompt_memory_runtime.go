package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	memorycore "mac/internal/memory"
	"mac/internal/projectctx"
)

type promptMemoryCollectorKey struct{}
type promptMemoryHistoryKey struct{}
type promptMemoryWireHistoryKey struct{}

// Candidates are private sidecars. Neither locators nor source metadata are
// added to provider messages or accepted from a serialized task.
type promptMemoryCandidate struct {
	source         memorycore.PromptSource
	role           string
	block          string
	history        bool
	historyOrdinal int
}

type promptMemoryHistoryOrigin struct {
	// Retain the actual map for the collector lifetime. An address by itself
	// is not an identity: a removed message's allocation could be reused.
	message   map[string]interface{}
	candidate int
}

type promptMemoryCollector struct {
	mu             sync.Mutex
	service        *memorycore.Service
	owner          memorycore.Owner
	sessionID      string
	taskID         string
	agent          string
	callIndex      int
	candidates     []promptMemoryCandidate
	historyOrigins map[string]promptMemoryHistoryOrigin
}

func promptMemoryDigest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func promptMemoryCollectorFromContext(ctx context.Context) *promptMemoryCollector {
	if ctx == nil {
		return nil
	}
	collector, _ := ctx.Value(promptMemoryCollectorKey{}).(*promptMemoryCollector)
	return collector
}

// Only an accepted, host-sealed ordinary Web conversation can produce a
// personal input receipt. A client-provided channel alone is never authority.
func (a *LLMAgent) beginPromptMemoryInput(ctx context.Context, sessionID, taskID string) context.Context {
	if ctx != nil {
		// A child Generate must validate its own owner instead of borrowing a
		// caller's collector when its separate agent/session is not eligible.
		ctx = context.WithValue(ctx, promptMemoryCollectorKey{}, (*promptMemoryCollector)(nil))
		ctx = memorycore.WithPromptSourceObserver(ctx, nil)
	}
	if a == nil || a.memoryService == nil || ctx == nil || isInternalMemoryExecutionTurn(ctx) || isRestrictedTenantExecution(ctx) {
		return ctx
	}
	audience := webMemoryAudienceFromContext(ctx)
	channel, _ := ctx.Value("channel").(string)
	if audience == nil || audience.gateway == nil || channel != "web" || sessionID == "" || taskID == "" || sessionID != audience.sessionID ||
		!projectctx.SameWorkspace(a.workspace, audience.workspace) {
		return ctx
	}
	g := audience.gateway
	if g.db == nil || g.project.ProjectID != audience.projectID || !projectctx.SameWorkspace(g.currentProjectWorkspace(), audience.workspace) || !g.db.Migrator().HasTable(&ChatSession{}) {
		return ctx
	}
	r, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://memory.local/api/memory", nil)
	if err != nil {
		return ctx
	}
	var session ChatSession
	if err := g.personalMemorySessionQuery(r, a.name).Where("id = ?", sessionID).First(&session).Error; err != nil || !projectctx.SameWorkspace(session.Workspace, audience.workspace) {
		return ctx
	}
	owner, err := a.memoryOwner(ctx)
	if err != nil {
		return ctx
	}
	owner.SessionID = sessionID
	collector := &promptMemoryCollector{service: a.memoryService, owner: owner, sessionID: sessionID, taskID: taskID, agent: a.name, historyOrigins: map[string]promptMemoryHistoryOrigin{}}
	ctx = context.WithValue(ctx, promptMemoryCollectorKey{}, collector)
	if history, ok := ctx.Value(promptMemoryHistoryKey{}).([]promptMemoryCandidate); ok {
		for _, candidate := range history {
			registerPromptMemoryCandidate(ctx, candidate.source, candidate.role, candidate.block)
			collector.mu.Lock()
			for i := range collector.candidates {
				if collector.candidates[i].source.Locator == candidate.source.Locator {
					collector.candidates[i].history = true
					collector.candidates[i].historyOrdinal = candidate.historyOrdinal
				}
			}
			collector.mu.Unlock()
		}
	}
	collectorContext := ctx
	return memorycore.WithPromptSourceObserver(ctx, func(observation memorycore.PromptSourceObservation) {
		registerPromptMemoryCandidate(collectorContext, promptMemoryRecallSource(observation.Item), "tool", observation.Block)
	})
}

// Map identity stays entirely in this private sidecar. Provider messages and
// persistent checkpoints carry no hidden origin fields. A copied/reconstructed
// message loses attribution unless the transformation explicitly preserves it.
func bindPromptMemoryHistoryMessage(ctx context.Context, ordinal int, message map[string]interface{}) {
	collector := promptMemoryCollectorFromContext(ctx)
	if collector == nil {
		return
	}
	collector.mu.Lock()
	defer collector.mu.Unlock()
	for i, candidate := range collector.candidates {
		if candidate.history && candidate.historyOrdinal == ordinal {
			collector.historyOrigins[fmt.Sprintf("%p", message)] = promptMemoryHistoryOrigin{message: message, candidate: i + 1}
		}
	}
}

func promptMemoryHistoryOrigins(ctx context.Context, messages []map[string]interface{}) []int {
	collector := promptMemoryCollectorFromContext(ctx)
	if collector == nil {
		return nil
	}
	collector.mu.Lock()
	defer collector.mu.Unlock()
	origins := make([]int, len(messages))
	for i, message := range messages {
		origins[i] = collector.historyOrigins[fmt.Sprintf("%p", message)].candidate
	}
	return origins
}

func registerPromptMemoryCandidate(ctx context.Context, source memorycore.PromptSource, role, block string) {
	collector := promptMemoryCollectorFromContext(ctx)
	if collector == nil || strings.TrimSpace(block) == "" || source.Locator.Kind == "" || source.Locator.Digest == "" || len(block) > 128<<10 {
		return
	}
	if source.CapturedRevision == "" {
		source.CapturedRevision = source.Locator.Digest
	}
	if source.SourceSessionID == "" {
		source.SourceSessionID = source.Locator.SessionID
	}
	// Store only the excerpt that is actually represented in the outgoing
	// block, never the complete model request or an unselected candidate.
	if source.Excerpt == "" || !strings.Contains(block, source.Excerpt) {
		source.Excerpt = block
	}
	if runes := []rune(source.Excerpt); len(runes) > 1200 {
		source.Excerpt = string(runes[:1200])
	}
	collector.mu.Lock()
	defer collector.mu.Unlock()
	if len(collector.candidates) >= 256 {
		return
	}
	for _, existing := range collector.candidates {
		if existing.source.Locator == source.Locator && existing.role == role && existing.block == block {
			return
		}
	}
	collector.candidates = append(collector.candidates, promptMemoryCandidate{source: source, role: role, block: block})
}

func promptMemoryRecallSource(item memorycore.RecallItem) memorycore.PromptSource {
	locator := item.Locator
	label, category := "", "history"
	switch locator.Kind {
	case "entry":
		label = "memory_entries#" + locator.ID + ".value"
		parts := strings.Split(strings.TrimPrefix(item.Source, "long_term:"), ":")
		category = "long"
		if len(parts) == 2 {
			if parts[1] == "preference" {
				category = "preferences"
			} else if parts[0] == "session" {
				category = "short"
			} else if parts[0] == "project" || parts[0] == "global" {
				category = parts[0]
			}
		}
	case "context":
		label, category = ".openkitty/context/context.json#"+locator.Key+" (session)", "short"
	case "summary":
		label, category = "memory_summaries#"+locator.ID+".content", "short"
	case "chat_message":
		label = "chat_messages#" + locator.ID + ".content"
		if item.Source == "user_statement" {
			category = "preferences"
		}
	case "memory_event":
		label = "memory_events#" + locator.ID + "." + locator.Field
	case "knowledge":
		label, category = "knowledges#"+locator.ID+"."+locator.Field, "knowledge"
	case "knowledge_fact":
		label, category = ".openkitty/knowledge_graph.db:triples#"+locator.ID, "knowledge"
	}
	return memorycore.PromptSource{Kind: item.Source, Category: category, Title: firstNonEmpty(locator.Key, item.Source), SourceLabel: label,
		Excerpt: item.Content, CapturedRevision: locator.Digest, SourceSessionID: item.SourceSessionID, Locator: locator}
}

func registerPromptMemoryInstructions(ctx context.Context, instructions projectctx.ProjectInstructionSet, prompt string) {
	if promptMemoryCollectorFromContext(ctx) == nil {
		return
	}
	for _, candidate := range promptMemoryInstructionCandidates(instructions, prompt) {
		registerPromptMemoryCandidate(ctx, candidate.source, candidate.role, candidate.block)
	}
}

// The same resolved read representation is used by Generate and by the
// read-only source inventory. Constructing candidates never records a send.
func promptMemoryInstructionCandidates(instructions projectctx.ProjectInstructionSet, prompt string) []promptMemoryCandidate {
	var candidates []promptMemoryCandidate
	for _, source := range instructions.Sources {
		// Sources are server-resolved paths, never URLs or client locators.
		info, err := os.Stat(source.Path)
		if err != nil || !info.Mode().IsRegular() || info.Size() > 512<<10 {
			continue
		}
		raw, err := os.ReadFile(source.Path)
		if err != nil || source.Bytes > len(raw) || source.Bytes < 0 || promptMemoryDigest(string(raw[:source.Bytes])) != source.Hash {
			continue
		}
		block := ""
		if source.ImportedBy == "" && source.Content != "" {
			header := "\n### " + source.RelativePath + " [" + source.Layer
			if source.Scope != "" {
				header += "; scope=" + source.Scope
			}
			if len(source.Paths) > 0 {
				header += "; paths=" + strings.Join(source.Paths, ",")
			}
			block = header + "]\n" + source.Content
		} else if source.ImportedBy != "" {
			start := "<!-- imported from " + source.RelativePath + " -->\n"
			end := "<!-- end import " + source.RelativePath + " -->"
			if index := strings.Index(prompt, start); index >= 0 {
				if tail := strings.Index(prompt[index+len(start):], end); tail >= 0 {
					block = prompt[index : index+len(start)+tail+len(end)]
				}
			}
		}
		if block == "" || !strings.Contains(prompt, block) {
			continue
		}
		locator := memorycore.PromptLocator{Kind: "project_instruction", Path: source.Path, Field: "content", Digest: promptMemoryDigest(string(raw))}
		candidates = append(candidates, promptMemoryCandidate{role: "system", block: block, source: memorycore.PromptSource{Kind: "project_instruction", Category: "project", Title: source.RelativePath,
			SourceLabel: source.RelativePath + " (" + source.Layer + " instructions)", Excerpt: block, CapturedRevision: locator.Digest, Locator: locator}})
	}
	return candidates
}

type promptMemoryWireText struct {
	role  string
	text  string
	index int
}

// Inspect only model-input text fields. A matching string in a tool schema,
// model name, diagnostic or arbitrary request option is not input inclusion.
func promptMemoryWireTexts(body []byte) []promptMemoryWireText {
	var request map[string]json.RawMessage
	if json.Unmarshal(body, &request) != nil {
		return nil
	}
	var messages []struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	}
	if json.Unmarshal(request["messages"], &messages) != nil {
		if json.Unmarshal(request["input"], &messages) != nil {
			return nil
		}
	}
	var result []promptMemoryWireText
	for index, message := range messages {
		var text string
		if json.Unmarshal(message.Content, &text) == nil {
			result = append(result, promptMemoryWireText{role: message.Role, text: text, index: index})
			continue
		}
		var blocks []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if json.Unmarshal(message.Content, &blocks) == nil {
			for _, block := range blocks {
				if block.Type == "text" || block.Type == "input_text" || block.Type == "output_text" {
					result = append(result, promptMemoryWireText{role: message.Role, text: block.Text, index: index})
				}
			}
		}
	}
	return result
}

// Called after authorization and serialization immediately before HTTP Do.
// Attempted is a local send attempt, not provider acceptance or model adoption.
// A summarized, truncated or replaced block is deliberately not attributed to
// its old candidate: inclusion requires its complete registered representation.
type promptMemoryAttempt struct {
	collector *promptMemoryCollector
	id        string
}

func (a *LLMAgent) capturePromptMemoryInput(ctx context.Context, body []byte, messages ...[]map[string]interface{}) *promptMemoryAttempt {
	collector := promptMemoryCollectorFromContext(ctx)
	if collector == nil || a == nil || a.name != collector.agent || a.memoryService != collector.service || ctx.Err() != nil || isInternalMemoryExecutionTurn(ctx) {
		return nil
	}
	wire := promptMemoryWireTexts(body)
	if wire == nil {
		return nil
	}
	origins, _ := ctx.Value(promptMemoryWireHistoryKey{}).([]int)
	if len(messages) > 0 {
		origins = promptMemoryHistoryOrigins(ctx, messages[0])
	}
	collector.mu.Lock()
	collector.callIndex++
	input := memorycore.PromptInput{SessionID: collector.sessionID, TaskID: collector.taskID, Agent: collector.agent, CallIndex: collector.callIndex, Status: "attempted", Sources: []memorycore.PromptSource{}}
	seen := map[memorycore.PromptLocator]bool{}
	for candidateIndex, candidate := range collector.candidates {
		if seen[candidate.source.Locator] {
			continue
		}
		for _, text := range wire {
			if candidate.role != "" && candidate.role != text.role {
				continue
			}
			if candidate.history && (text.index >= len(origins) || origins[text.index] != candidateIndex+1 || text.text != candidate.block) {
				continue
			}
			if strings.Contains(text.text, candidate.block) {
				input.Sources = append(input.Sources, candidate.source)
				seen[candidate.source.Locator] = true
				break
			}
		}
		if len(input.Sources) >= 128 {
			break
		}
	}
	collector.mu.Unlock()
	writeCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	receipt, err := collector.service.SavePromptInput(writeCtx, collector.owner, input)
	if err != nil {
		// The receipt is observational; a failed local audit write cannot create
		// a second provider call or change the user's authorized model request.
		a.logMemoryError(fmt.Errorf("save model-input source receipt: %w", err))
		return nil
	}
	return &promptMemoryAttempt{collector: collector, id: receipt.ID}
}

func (a *LLMAgent) finishPromptMemoryInput(ctx context.Context, attempt *promptMemoryAttempt, response *http.Response, sendErr error) {
	if attempt == nil {
		return
	}
	status := "failed"
	if sendErr == nil && response != nil && response.StatusCode >= 200 && response.StatusCode < 300 {
		status = "succeeded"
	}
	// Cancellation of the model call must not leave a failed send presented as
	// an indefinitely attempted request. This small metadata write is bounded,
	// synchronous, and never retries or replays the provider request.
	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
	defer cancel()
	if err := attempt.collector.service.UpdatePromptInputStatus(writeCtx, attempt.collector.owner, attempt.id, status); err != nil {
		a.logMemoryError(fmt.Errorf("finish model-input source receipt: %w", err))
	}
}
