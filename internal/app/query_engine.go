package app

import (
	"context"
	"crypto/md5"
	"encoding/json"
	"fmt"
	"log"
	"mac/internal/projectctx"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"mac/internal/agentloop"
	"mac/internal/agentos"
	"mac/internal/agentresult"
	approvalcore "mac/internal/approval"
	codingcore "mac/internal/coding"
	middlewarecore "mac/internal/middleware"
	"mac/internal/permissionpolicy"
	ptccore "mac/internal/ptc"
	researchcore "mac/internal/research"
	"mac/internal/runtimeutil"
	securitycore "mac/internal/security"
	skilldomain "mac/internal/skills"
	"mac/internal/textutil"
	"mac/internal/toolcontract"
	workspacetools "mac/internal/tools/workspace"
)

type ToolRecoveryTracker = agentloop.ToolRecoveryTracker
type TurnState = agentloop.TurnState

var NewToolRecoveryTracker = agentloop.NewToolRecoveryTracker
var NewTurnState = agentloop.NewTurnState

// QueryEngine is the lifecycle boundary for a single agent conversation.
//
// The current LLMAgent.Generate loop is still the production driver. This
// struct gives the loop stable seams for turn state, message storage, and tool
// execution so the large loop can be migrated incrementally without changing
// behavior in one risky edit.
type QueryEngine struct {
	agent           *LLMAgent
	messageStore    *MessageStore
	toolExecutor    *AgentToolExecutor
	toolFinalizer   *ToolTurnFinalizer
	resultProcessor *ToolResultProcessor
	stepRecorder    *ToolStepRecorder
	loopGuard       *ToolCallLoopGuard
	reporter        *ToolCallReporter
}

func NewQueryEngine(agent *LLMAgent, hotMessages, coldMessages []map[string]interface{}, taskID ...string) *QueryEngine {
	id := ""
	if len(taskID) > 0 {
		id = taskID[0]
	}
	return &QueryEngine{
		agent:           agent,
		messageStore:    NewMessageStore(hotMessages, coldMessages),
		toolExecutor:    NewAgentToolExecutor(agent),
		toolFinalizer:   NewToolTurnFinalizer(agent),
		resultProcessor: NewToolResultProcessor(),
		stepRecorder:    NewToolStepRecorder(),
		loopGuard:       NewToolCallLoopGuard(2),
		reporter:        NewToolCallReporter(agent, id),
	}
}

func (e *QueryEngine) Messages() *MessageStore {
	if e == nil {
		return nil
	}
	return e.messageStore
}

func (e *QueryEngine) BindMessages(hot, cold *[]map[string]interface{}) *MessagePairAppender {
	return &MessagePairAppender{hot: hot, cold: cold}
}

func (e *QueryEngine) ToolExecutor() *AgentToolExecutor {
	if e == nil {
		return nil
	}
	return e.toolExecutor
}

func (e *QueryEngine) ToolFinalizer() *ToolTurnFinalizer {
	if e == nil {
		return nil
	}
	return e.toolFinalizer
}

func (e *QueryEngine) ResultProcessor() *ToolResultProcessor {
	if e == nil {
		return nil
	}
	return e.resultProcessor
}

func (e *QueryEngine) StepRecorder() *ToolStepRecorder {
	if e == nil {
		return nil
	}
	return e.stepRecorder
}

func (e *QueryEngine) LoopGuard() *ToolCallLoopGuard {
	if e == nil {
		return nil
	}
	return e.loopGuard
}

func (e *QueryEngine) Reporter() *ToolCallReporter {
	if e == nil {
		return nil
	}
	return e.reporter
}

func (e *QueryEngine) NewTurn(input, taskID string, maxIterations int) *TurnState {
	return NewTurnState(input, taskID, maxIterations)
}

var (
	searchItemCountAfterNumber = regexp.MustCompile(`(?i)([0-9]{1,2}|[一二两三四五六七八九十]{1,3})\s*(?:条|项|个|家|款|篇|则|组|份|点|例|案例|候选(?:项)?|结果|items?|examples?|options?|entries?|results?|candidates?)`)
	searchTopCount             = regexp.MustCompile(`(?i)(?:top|前)\s*([0-9]{1,2}|[一二两三四五六七八九十]{1,3})`)
)

func maxRequestedCount(input string, patterns ...*regexp.Regexp) int {
	best := 0
	for _, pattern := range patterns {
		for _, match := range pattern.FindAllStringSubmatch(input, -1) {
			if len(match) < 2 {
				continue
			}
			if value := parseRequestedCount(match[1]); value > best {
				best = value
			}
		}
	}
	return best
}

func parseRequestedCount(raw string) int {
	raw = strings.TrimSpace(raw)
	if value, err := strconv.Atoi(raw); err == nil {
		return value
	}
	digits := map[rune]int{'一': 1, '二': 2, '两': 2, '三': 3, '四': 4, '五': 5, '六': 6, '七': 7, '八': 8, '九': 9}
	runes := []rune(raw)
	if len(runes) == 1 {
		return digits[runes[0]]
	}
	for i, r := range runes {
		if r != '十' {
			continue
		}
		tens := 1
		if i > 0 {
			tens = digits[runes[i-1]]
		}
		ones := 0
		if i+1 < len(runes) {
			ones = digits[runes[i+1]]
		}
		return tens*10 + ones
	}
	return 0
}

func deduplicateHousekeepingToolCalls(calls []ToolCall) ([]ToolCall, int) {
	if len(calls) < 2 {
		return calls, 0
	}
	seen := make(map[string]bool)
	filtered := make([]ToolCall, 0, len(calls))
	dropped := 0
	for _, call := range calls {
		if !strings.EqualFold(strings.TrimSpace(call.Name), "update_plan_step") {
			filtered = append(filtered, call)
			continue
		}
		stepID, _ := call.Arguments["step_id"].(string)
		status, _ := call.Arguments["status"].(string)
		if strings.TrimSpace(stepID) == "" || strings.TrimSpace(status) == "" {
			filtered = append(filtered, call)
			continue
		}
		key := strings.ToLower(strings.TrimSpace(call.Name)) + "|" + strings.TrimSpace(stepID) + "|" + strings.ToLower(strings.TrimSpace(status))
		if seen[key] {
			dropped++
			continue
		}
		seen[key] = true
		filtered = append(filtered, call)
	}
	if dropped == 0 {
		return calls, 0
	}
	return filtered, dropped
}

func turnToolExecutionState(t *TurnState,
	messages, coldMessages *[]map[string]interface{},
	toolCallSummary *[]string,
	callChain *[]map[string]interface{},
	totalDurationMs *int64,
	allToolResults *[]string,
	toolExecCount *int,
) *ToolExecutionState {
	if t == nil {
		return &ToolExecutionState{}
	}
	return &ToolExecutionState{
		Input:                  t.Input,
		Messages:               messages,
		ColdMessages:           coldMessages,
		TaskID:                 t.TaskID,
		ToolCallSummary:        toolCallSummary,
		CallChain:              callChain,
		TotalDurationMs:        totalDurationMs,
		AllToolResults:         allToolResults,
		ToolExecCount:          toolExecCount,
		FailTracker:            t.FailTracker,
		BrokenTools:            t.BrokenTools,
		FailDiagnosticInjected: t.FailDiagnosticInjected,
		RecoveryTracker:        t.RecoveryTracker,
	}
}

func turnToolTurnFinalizeState(t *TurnState,
	messages, coldMessages *[]map[string]interface{},
	pendingDiagnostics []map[string]interface{},
	toolExecCount int,
	toolReviewInterval int,
	toolCallCount int,
) *ToolTurnFinalizeState {
	if t == nil {
		return &ToolTurnFinalizeState{}
	}
	return &ToolTurnFinalizeState{
		Messages:           messages,
		ColdMessages:       coldMessages,
		PendingDiagnostics: pendingDiagnostics,
		ToolExecCount:      toolExecCount,
		ToolReviewInterval: toolReviewInterval,
		FailTracker:        t.FailTracker,
		BrokenTools:        t.BrokenTools,
		TaskID:             t.TaskID,
		ToolCallCount:      toolCallCount,
	}
}

// MessageStore keeps hot context and cold transcript in one place.
type MessageStore struct {
	hot  []map[string]interface{}
	cold []map[string]interface{}
}

func NewMessageStore(hot, cold []map[string]interface{}) *MessageStore {
	return &MessageStore{
		hot:  cloneAPIMessages(hot),
		cold: cloneAPIMessages(cold),
	}
}

func (s *MessageStore) Hot() []map[string]interface{} {
	if s == nil {
		return nil
	}
	return cloneAPIMessages(s.hot)
}

func (s *MessageStore) Cold() []map[string]interface{} {
	if s == nil {
		return nil
	}
	return cloneAPIMessages(s.cold)
}

func (s *MessageStore) SetHot(messages []map[string]interface{}) {
	if s == nil {
		return
	}
	s.hot = cloneAPIMessages(messages)
}

func (s *MessageStore) SetCold(messages []map[string]interface{}) {
	if s == nil {
		return
	}
	s.cold = cloneAPIMessages(messages)
}

func (s *MessageStore) AppendHot(msg map[string]interface{}) {
	if s == nil {
		return
	}
	s.hot = append(s.hot, cloneAPIMessage(msg))
}

func (s *MessageStore) AppendCold(msg map[string]interface{}) {
	if s == nil {
		return
	}
	s.cold = append(s.cold, cloneAPIMessage(msg))
}

func (s *MessageStore) AppendBoth(msg map[string]interface{}) {
	if s == nil {
		return
	}
	copied := cloneAPIMessage(msg)
	s.hot = append(s.hot, copied)
	s.cold = append(s.cold, cloneAPIMessage(copied))
}

func (s *MessageStore) AppendUser(content string) {
	s.AppendBoth(map[string]interface{}{"role": "user", "content": content})
}

func (s *MessageStore) AppendSystem(content string) {
	s.AppendBoth(map[string]interface{}{"role": "system", "content": content})
}

func (s *MessageStore) AppendAssistant(content string, toolCalls []ToolCall, reasoningContent ...string) {
	s.AppendBoth(BuildAssistantAPIMessage(content, toolCalls, reasoningContent...))
}

func (s *MessageStore) AppendToolResult(result ToolResult, success bool) {
	s.AppendBoth(BuildToolResultAPIMessage(result, success))
}

func (s *MessageStore) ExportTo(hot, cold *[]map[string]interface{}) {
	if s == nil {
		return
	}
	if hot != nil {
		*hot = s.Hot()
	}
	if cold != nil {
		*cold = s.Cold()
	}
}

type MessagePairAppender struct {
	hot  *[]map[string]interface{}
	cold *[]map[string]interface{}
}

func (a *MessagePairAppender) AppendHot(msg map[string]interface{}) {
	if a == nil || a.hot == nil {
		return
	}
	*a.hot = append(*a.hot, cloneAPIMessage(msg))
}

func (a *MessagePairAppender) AppendCold(msg map[string]interface{}) {
	if a == nil || a.cold == nil {
		return
	}
	*a.cold = append(*a.cold, cloneAPIMessage(msg))
}

func (a *MessagePairAppender) AppendBoth(msg map[string]interface{}) {
	if a == nil {
		return
	}
	appendBothMessages(a.hot, a.cold, msg)
}

func (a *MessagePairAppender) AppendUser(content string) {
	a.AppendBoth(map[string]interface{}{"role": "user", "content": content})
}

func (a *MessagePairAppender) AppendSystem(content string) {
	a.AppendBoth(map[string]interface{}{"role": "system", "content": content})
}

func (a *MessagePairAppender) AppendAssistant(content string, toolCalls []ToolCall, reasoningContent ...string) {
	a.AppendBoth(BuildAssistantAPIMessage(content, toolCalls, reasoningContent...))
}

func (a *MessagePairAppender) AppendToolResult(result ToolResult, success bool) {
	a.AppendBoth(BuildToolResultAPIMessage(result, success))
}

// BuildAssistantAPIMessage converts a provider response into the assistant
// message that is replayed on the next tool turn. Some OpenAI-compatible
// thinking endpoints (including DeepSeek) require their returned
// reasoning_content to be passed back with the assistant tool_calls message.
// Keep the argument optional so non-thinking providers retain the exact legacy
// wire shape and never receive an invented field.
func BuildAssistantAPIMessage(content string, toolCalls []ToolCall, reasoningContent ...string) map[string]interface{} {
	msg := map[string]interface{}{
		"role":    "assistant",
		"content": content,
	}
	if len(reasoningContent) > 0 && reasoningContent[0] != "" {
		msg["reasoning_content"] = reasoningContent[0]
	}
	if len(toolCalls) == 0 {
		return msg
	}

	data := make([]map[string]interface{}, len(toolCalls))
	for i, tc := range toolCalls {
		argsJSON, _ := json.Marshal(tc.Arguments)
		data[i] = map[string]interface{}{
			"id":   tc.ID,
			"type": "function",
			"function": map[string]interface{}{
				"name":      tc.Name,
				"arguments": string(argsJSON),
			},
		}
	}
	msg["tool_calls"] = data
	return msg
}

func ensureToolCallIDs(calls []ToolCall, taskID string, iteration int) []ToolCall {
	if len(calls) == 0 {
		return calls
	}
	for i := range calls {
		args, _ := json.Marshal(calls[i].Arguments)
		seed := fmt.Sprintf("%s|%d|%d|%s|%s", taskID, iteration, i, calls[i].Name, args)
		sum := md5.Sum([]byte(seed))
		// Always replace provider IDs at the host boundary. Some OpenAI-compatible
		// models emit an empty ID for every batch; others reuse IDs across turns.
		// Host-generated IDs keep assistant calls and tool results paired while
		// remaining deterministic for logs/retries.
		calls[i].ID = fmt.Sprintf("call_%x", sum[:12])
	}
	return calls
}

func BuildToolResultAPIMessage(result ToolResult, success bool) map[string]interface{} {
	externalEvidence := securitycore.IsExternalEvidenceToolName(result.Name) &&
		!strings.EqualFold(strings.TrimSpace(result.Name), "agent_reach")
	return buildToolResultAPIMessage(result, success, externalEvidence)
}

// buildToolResultAPIMessageForCall keeps action-aware tools honest. A tool can
// expose both retrieval actions and diagnostics; only the former should tell
// the model that its output is external evidence.
func buildToolResultAPIMessageForCall(call ToolCall, result ToolResult, success bool) map[string]interface{} {
	return buildToolResultAPIMessage(result, success, securitycore.IsExternalEvidenceToolCall(call))
}

func buildToolResultAPIMessage(result ToolResult, success, externalEvidence bool) map[string]interface{} {
	content := result.Content
	if success && externalEvidence {
		content = securitycore.AppendExternalEvidenceBoundary(content)
	}
	return map[string]interface{}{
		"role":         "tool",
		"tool_call_id": result.ToolCallID,
		"name":         result.Name,
		"content":      content,
		"status":       success,
	}
}

type ToolExecutionState struct {
	Input                   string
	Messages                *[]map[string]interface{}
	ColdMessages            *[]map[string]interface{}
	TaskID                  string
	ToolCallSummary         *[]string
	CallChain               *[]map[string]interface{}
	TotalDurationMs         *int64
	AllToolResults          *[]string
	ToolExecCount           *int
	FailTracker             map[string]int
	BrokenTools             map[string]bool
	FailDiagnosticInjected  map[string]bool
	RecoveryTracker         *ToolRecoveryTracker
	MutatingToolSucceeded   bool
	MutatingToolTrigger     string
	TerminalFailure         *agentresult.TerminalToolFailure
	LoopGuard               *ToolCallLoopGuard
	ToolSignatures          map[string]int
	ToolConsecutiveFailures map[string]int
	PreventedToolCalls      int
	ExecutedToolCalls       int
}

type AgentToolRunResult struct {
	Index                  int
	ToolCall               ToolCall
	Result                 ToolResult
	Err                    error
	DurationMs             int64
	TranscriptCallRecorded bool
	ExecutionPrevented     bool
	LoopDecision           ToolCallLoopDecision
}

type ProcessedToolResult struct {
	Content   string
	Succeeded bool
	Message   map[string]interface{}
	FailKey   string
}

type ToolStepRecord struct {
	Content      string
	Succeeded    bool
	Step         map[string]interface{}
	Summary      string
	ErrorSummary string
	DurationMs   int64
}

type ToolStepRecorder struct{}

func NewToolStepRecorder() *ToolStepRecorder {
	return &ToolStepRecorder{}
}

func (r *ToolStepRecorder) Record(call ToolCall, result ToolResult, content string, err error, durationMs int64, succeeded bool) ToolStepRecord {
	step := map[string]interface{}{
		"type": "tool",
		"name": call.Name,
	}
	record := ToolStepRecord{
		Content:    content,
		Succeeded:  succeeded,
		Step:       step,
		Summary:    formatToolCall(call.Name, call.Arguments, succeeded),
		DurationMs: durationMs,
	}

	if !succeeded {
		step["success"] = false
		if err != nil {
			record.ErrorSummary = err.Error()
			record.Content = fmt.Sprintf("工具执行错误: %v", err)
		} else {
			record.ErrorSummary = toolcontract.CompactToolFailure(content)
		}
		step["error"] = record.ErrorSummary
		step["output_summary"] = "工具执行失败"
	} else {
		step["success"] = true
		step["output_summary"] = result.Content
		step["duration_ms"] = durationMs
	}

	step["input_summary"] = record.Summary
	return record
}

func (r *ToolStepRecorder) Append(record ToolStepRecord, toolCallSummary *[]string, callChain *[]map[string]interface{}, totalDurationMs *int64) {
	if toolCallSummary != nil {
		*toolCallSummary = append(*toolCallSummary, record.Summary)
	}
	if callChain != nil {
		*callChain = append(*callChain, cloneAPIMessage(record.Step))
	}
	if totalDurationMs != nil {
		*totalDurationMs += record.DurationMs
	}
}

type ToolFailureUpdate struct {
	ErrorSignature string
	FailKey        string
	FailCount      int
}

type ToolCallLoopDecision struct {
	Signature string
	Blocked   bool
	Attempt   int
	Result    ToolResult
	Err       error
	Message   string
}

type ToolCallLoopGuard struct {
	blockAfter int
}

// Keep the broad per-tool recovery budget aligned with PTC's bounded 80-call
// budget. Exact duplicate calls are still stopped by ToolCallLoopGuard's much
// tighter signature limit, while varied diagnostic/build commands can recover
// from several independent errors without permanently disabling the tool.
const toolConsecutiveFailureBlockAfter = ptccore.CallLimitPerRun

func NewToolCallLoopGuard(blockAfter int) *ToolCallLoopGuard {
	if blockAfter <= 0 {
		blockAfter = 2
	}
	return &ToolCallLoopGuard{blockAfter: blockAfter}
}

func (g *ToolCallLoopGuard) Signature(call ToolCall) string {
	argsJSON, _ := json.Marshal(call.Arguments)
	return call.Name + ":" + string(argsJSON)
}

func (g *ToolCallLoopGuard) Check(call ToolCall, signatures map[string]int) ToolCallLoopDecision {
	signature := g.Signature(call)
	count := 0
	if signatures != nil {
		count = signatures[signature]
	}
	decision := ToolCallLoopDecision{
		Signature: signature,
		Attempt:   count + 1,
	}
	if count < g.blockAfter {
		return decision
	}
	message := fmt.Sprintf(
		"Exact same call to %s already failed %d times. The call was not executed again; use a different approach or finish with the evidence already collected.",
		call.Name,
		count,
	)
	executionSucceeded := false
	decision.Blocked = true
	decision.Message = "[LOOP BLOCKED] " + message
	decision.Result = ToolResult{
		ToolCallID:         call.ID,
		Name:               call.Name,
		Content:            "Tool Result\nstatus: failed\nerror_code: duplicate_failed_call_blocked\nsummary: " + message,
		ExecutionSucceeded: &executionSucceeded,
		ErrorCode:          "duplicate_failed_call_blocked",
	}
	decision.Err = middlewarecore.NewGuardrailBlockError(call.Name, message, "duplicate_failed_call_blocked")
	return decision
}

func (g *ToolCallLoopGuard) CheckWithFailureCircuit(call ToolCall, signatures, consecutiveFailures map[string]int) ToolCallLoopDecision {
	decision := g.Check(call, signatures)
	if decision.Blocked {
		return decision
	}

	failures := 0
	if consecutiveFailures != nil {
		failures = consecutiveFailures[call.Name]
	}
	if failures < toolConsecutiveFailureBlockAfter {
		return decision
	}

	message := fmt.Sprintf(
		"%s failed %d consecutive times during this turn. The call was not executed again; use another available route or finish from the evidence already collected.",
		call.Name,
		failures,
	)
	executionSucceeded := false
	decision.Blocked = true
	decision.Attempt = failures + 1
	decision.Message = "[FAILURE CIRCUIT OPEN] " + message
	decision.Result = ToolResult{
		ToolCallID:         call.ID,
		Name:               call.Name,
		Content:            "Tool Result\nstatus: failed\nerror_code: repeated_tool_failure_circuit_open\nsummary: " + message,
		ExecutionSucceeded: &executionSucceeded,
		ErrorCode:          "repeated_tool_failure_circuit_open",
	}
	decision.Err = middlewarecore.NewGuardrailBlockError(call.Name, message, "repeated_tool_failure_circuit_open")
	return decision
}

type ToolCallReporter struct {
	agent  *LLMAgent
	taskID string
}

func NewToolCallReporter(agent *LLMAgent, taskID ...string) *ToolCallReporter {
	id := ""
	if len(taskID) > 0 {
		id = taskID[0]
	}
	return &ToolCallReporter{agent: agent, taskID: id}
}

func (r *ToolCallReporter) RecordAssistantToolCall(taskID string, call ToolCall) {
	if r == nil || r.agent == nil || r.agent.memory == nil {
		return
	}
	argsJSON, _ := json.Marshal(call.Arguments)
	r.agent.memory.SaveTaskLog(taskID, "TOOL_CALL", fmt.Sprintf("Tool: %s\nArgs: %s", call.Name, truncate(string(argsJSON), 1000)))
}

func (r *ToolCallReporter) ReportToolStart(call ToolCall, logVerb string) {
	if r == nil || r.agent == nil {
		return
	}
	agent := r.agent
	if agent.callback != nil {
		argsJSON, _ := json.Marshal(call.Arguments)
		emitAgentThinking(agent.callback, r.taskID, agent.name, fmt.Sprintf("will use %s: %s", call.Name, string(argsJSON)), "use_tool")
	}
	if agent.fileLogger == nil {
		return
	}
	if logVerb == "" {
		logVerb = "tool"
	}
	if call.Name != "use_skill" {
		agent.fileLogger.Printf("[INFO] %s: %s, ID: %s", logVerb, call.Name, call.ID)
		return
	}
	skillName, _ := call.Arguments["skill_name"].(string)
	agent.fileLogger.Printf("[INFO] %s: %s, ID: %s, skill:%s", logVerb, call.Name, call.ID, skillName)
}

func (r *ToolCallReporter) SaveToolResult(taskID string, call ToolCall, content string) {
	if r == nil || r.agent == nil || r.agent.memory == nil {
		return
	}
	r.agent.memory.SaveTaskLog(taskID, "TOOL_RESULT", fmt.Sprintf("Tool: %s\nResult:\n%s", call.Name, truncate(content, 300)))
}

func (r *ToolCallReporter) ReportToolFailure(call ToolCall, errorText string) {
	if r == nil || r.agent == nil {
		return
	}
	agent := r.agent
	if agent.callback != nil {
		emitAgentThinking(agent.callback, r.taskID, agent.name,
			fmt.Sprintf("%s failed: %s", call.Name, textutil.TruncateRunes(errorText, toolcontract.ToolFailureSummaryLimit)), "tool_error")
	}
	if agent.fileLogger != nil {
		agent.fileLogger.Printf("[ERROR] 工具执行失败: %s, 错误: %s", call.Name, errorText)
	}
}

func (r *ToolCallReporter) ReportToolRecoverable(call ToolCall, code, detail string) {
	if r == nil || r.agent == nil {
		return
	}
	agent := r.agent
	if agent.callback != nil {
		emitAgentThinking(agent.callback, r.taskID, agent.name,
			fmt.Sprintf("%s needs a refreshed edit context (%s); recovery is continuing", call.Name, code), "tool_recovery")
	}
	if agent.fileLogger != nil {
		agent.fileLogger.Printf("[RECOVERABLE_TOOL_ERROR] tool=%s code=%s detail=%s", call.Name, code, textutil.TruncateRunes(detail, toolcontract.ToolFailureSummaryLimit))
	}
}

func (r *ToolCallReporter) ReportToolSuccess(call ToolCall, content string, tokenCount int) {
	if r == nil || r.agent == nil {
		return
	}
	agent := r.agent
	if agent.callback != nil {
		if provenance := skillProgressProvenance(call, content); provenance != "" {
			emitAgentThinking(agent.callback, r.taskID, agent.name, fmt.Sprintf("%s completed (%s)", call.Name, provenance), "rec")
		} else if tokenCount < 300 {
			emitAgentThinking(agent.callback, r.taskID, agent.name, fmt.Sprintf("%s completed (%d chars)", call.Name, len(content)), "rec")
		} else if strings.Contains(content, "完整内容已保存至") {
			emitAgentThinking(agent.callback, r.taskID, agent.name, fmt.Sprintf("%s completed (%d tokens, overflow saved to file)", call.Name, tokenCount), "rec")
		} else {
			emitAgentThinking(agent.callback, r.taskID, agent.name, fmt.Sprintf("%s completed (%d tokens)", call.Name, tokenCount), "rec")
		}
	}
	if agent.fileLogger != nil {
		agent.fileLogger.Printf("[INFO] 工具执行成功: %s, 结果长度: %d,执行结果:%s", call.Name, tokenCount, truncate(content, 1000))
	}
}

// skillProgressProvenance exposes the server-authored provenance receipt in
// progress events without copying arbitrary Skill instructions or host paths.
// This makes Skill routing independently auditable by UI and black-box clients.
func skillProgressProvenance(call ToolCall, content string) string {
	if toolcontract.NormalizeToolName(call.Name) != "use_skill" {
		return ""
	}
	for _, line := range strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "来源:") {
			continue
		}
		lower := strings.ToLower(line)
		switch {
		case strings.Contains(lower, "user") || strings.Contains(line, "用户目录/磁盘"):
			return "来源: user（用户目录/磁盘）"
		case strings.Contains(lower, "builtin"):
			return "来源: builtin（内存）"
		}
	}
	return ""
}

func (r *ToolCallReporter) ReportNoProgress(toolName string, repeated int) {
	if r == nil || r.agent == nil {
		return
	}
	if r.agent.fileLogger != nil {
		r.agent.fileLogger.Printf("[NO_PROGRESS] %s returns same result %d times", toolName, repeated)
	}
	if r.agent.callback != nil {
		emitAgentThinking(r.agent.callback, r.taskID, r.agent.name, fmt.Sprintf("No progress: %s returned same result %d times", toolName, repeated), "system")
	}
}

func (r *ToolCallReporter) ReportFailureDiagnostic(toolName string, failCount int) {
	if r == nil || r.agent == nil || r.agent.callback == nil {
		return
	}
	emitAgentThinking(r.agent.callback, r.taskID, r.agent.name, fmt.Sprintf("故障诊断: %s 已失败%d次，提供修复建议", toolName, failCount), "system")
}

func (r *ToolCallReporter) ReportLoopBlocked(call ToolCall, signature string, attempt int) {
	if r == nil || r.agent == nil {
		return
	}
	if r.agent.fileLogger != nil {
		r.agent.fileLogger.Printf("[LOOP_BLOCK] retry circuit blocked call: %s (attempt %d)", signature, attempt)
	}
	if r.agent.callback != nil {
		emitAgentThinking(r.agent.callback, r.taskID, r.agent.name, fmt.Sprintf("Retry circuit open: %s has repeatedly failed; switching approach", call.Name), "system")
	}
}

func (r *ToolCallReporter) ReportDeduplicated(toolName string, duplicateID int) {
	if r == nil || r.agent == nil || r.agent.fileLogger == nil {
		return
	}
	r.agent.fileLogger.Printf("[DEDUP] %s result matches call #%d, skipped", toolName, duplicateID)
}

func (r *ToolCallReporter) ReportErrorTrack(toolName, errorSignature string, failCount int, logical bool) {
	if r == nil || r.agent == nil || r.agent.fileLogger == nil {
		return
	}
	r.agent.fileLogger.Printf("[ERROR_TRACK] tool=%s, sig=%s, count=%d, logical=%v", toolName, errorSignature, failCount, logical)
}

func (r *ToolCallReporter) ReportGuardrailCircuitBreaker(message string) {
	if r == nil || r.agent == nil || r.agent.fileLogger == nil {
		return
	}
	r.agent.fileLogger.Printf("[GUARDRAIL] Circuit breaker: %s", message)
}

func (r *ToolCallReporter) ReportToolMessageAppended(call ToolCall) {
	if r == nil || r.agent == nil {
		return
	}
	if r.agent.fileLogger != nil {
		r.agent.fileLogger.Printf("[INFO] 添加tool消息，tool_call_id: %s", call.ID)
	}
	log.Printf("[INFO] 添加tool消息，tool_call_id: %s", call.ID)
}

type ToolGuardrailAfterResult struct {
	Content  string
	Decision *toolcontract.GuardrailDecision
	Halt     bool
	Err      error
}

type ToolResultProcessor struct{}

func NewToolResultProcessor() *ToolResultProcessor {
	return &ToolResultProcessor{}
}

func (p *ToolResultProcessor) Process(run AgentToolRunResult, failTracker map[string]int, _ map[string]bool) ProcessedToolResult {
	content := run.Result.Content
	if run.Err != nil && strings.TrimSpace(content) == "" {
		content = fmt.Sprintf("tool error: %v", run.Err)
	}
	resultForStatus := run.Result
	resultForStatus.Name = run.ToolCall.Name
	resultForStatus.Content = content
	succeeded := ToolResultExecutionSucceeded(resultForStatus, run.Err)
	processed := ProcessedToolResult{
		Content:   content,
		Succeeded: succeeded,
		Message: buildToolResultAPIMessageForCall(run.ToolCall, ToolResult{
			ToolCallID: run.ToolCall.ID,
			Name:       run.ToolCall.Name,
			Content:    content,
		}, succeeded),
	}
	if succeeded {
		return processed
	}

	errSig := extractErrorSignature(content)
	processed.FailKey = run.ToolCall.Name + ":" + errSig
	if failTracker != nil {
		failTracker[processed.FailKey]++
	}
	return processed
}

func (p *ToolResultProcessor) DeduplicateLongResult(toolName, content string, resultHashes map[string]int) (string, bool, int) {
	if len(content) <= 200 || resultHashes == nil {
		return content, false, 0
	}
	hash := fmt.Sprintf("%x", md5.Sum([]byte(content[:min(len(content), 2000)])))
	signature := toolName + ":" + hash
	if duplicateID, ok := resultHashes[signature]; ok {
		return fmt.Sprintf("[Duplicate result only - %s executed successfully; output matches call #%d. Do not rerun.]", toolName, duplicateID), true, duplicateID
	}
	resultHashes[signature] = len(resultHashes) + 1
	return content, false, 0
}

func (p *ToolResultProcessor) TrackNoProgress(toolName, content string, resultHashes map[string]int) int {
	if resultHashes == nil || !toolcontract.IsReadOnlyToolName(toolName) || len(content) >= 4000 {
		return 0
	}
	hash := fmt.Sprintf("%x", md5.Sum([]byte(content)))
	resultHashes[hash]++
	return resultHashes[hash]
}

func (p *ToolResultProcessor) TrackFailure(
	toolName string,
	content string,
	callSignature string,
	failTracker map[string]int,
	failDiagnosticInjected map[string]bool,
	toolSignatures map[string]int,
) ToolFailureUpdate {
	errSig := extractErrorSignature(content)
	failKey := toolName + ":" + errSig
	update := ToolFailureUpdate{
		ErrorSignature: errSig,
		FailKey:        failKey,
	}
	if failTracker != nil {
		failTracker[failKey]++
		update.FailCount = failTracker[failKey]
	}
	if callSignature != "" && toolSignatures != nil {
		toolSignatures[callSignature]++
	}
	if update.FailCount >= 2 && failDiagnosticInjected != nil && !failDiagnosticInjected[failKey] {
		failDiagnosticInjected[failKey] = true
	}
	return update
}

func (p *ToolResultProcessor) ApplyGuardrailAfter(controller *toolcontract.ToolGuardrailController, run AgentToolRunResult, content string) ToolGuardrailAfterResult {
	result := ToolGuardrailAfterResult{Content: content}
	if controller == nil {
		return result
	}

	modified, decision := controller.RecordAfter(
		run.ToolCall.Name,
		run.ToolCall.Arguments,
		content,
		run.Err,
		run.Result.ExecutionSucceeded,
	)
	result.Content = modified
	result.Decision = decision
	if decision != nil && decision.ShouldHaltTurn() {
		result.Halt = true
		result.Err = fmt.Errorf("circuit breaker: %s", decision.Message)
	}
	return result
}

type RunSingleToolOptions struct {
	Index            int
	RecordTranscript bool
	SkipHooks        bool
	SkipSafety       bool
	SkipSnapshot     bool
}

type ToolTurnFinalizeState struct {
	Messages           *[]map[string]interface{}
	ColdMessages       *[]map[string]interface{}
	PendingDiagnostics []map[string]interface{}
	ToolExecCount      int
	ToolReviewInterval int
	FailTracker        map[string]int
	BrokenTools        map[string]bool
	TaskID             string
	ToolCallCount      int
}

func (s *ToolTurnFinalizeState) Validate() error {
	if s == nil {
		return fmt.Errorf("tool turn finalize state is nil")
	}
	if s.Messages == nil || s.ColdMessages == nil {
		return fmt.Errorf("tool turn finalize messages are nil")
	}
	return nil
}

type ToolTurnFinalizer struct {
	agent *LLMAgent
}

func NewToolTurnFinalizer(agent *LLMAgent) *ToolTurnFinalizer {
	return &ToolTurnFinalizer{agent: agent}
}

func (f *ToolTurnFinalizer) Finalize(state *ToolTurnFinalizeState) error {
	if err := state.Validate(); err != nil {
		return err
	}

	for _, diag := range state.PendingDiagnostics {
		if kind := agentloop.InternalControlKind(diag); kind != "" {
			agentloop.ReplaceInternalControlBoth(state.Messages, state.ColdMessages, kind, diag)
		} else {
			appendBothMessages(state.Messages, state.ColdMessages, diag)
		}
		if f != nil && f.agent != nil && f.agent.fileLogger != nil {
			f.agent.fileLogger.Printf("[ERROR_RECOVERY] 延迟注入故障诊断到消息队列")
		}
	}

	if f == nil || f.agent == nil {
		return nil
	}

	if state.ToolReviewInterval > 0 && state.ToolExecCount > 0 && state.ToolExecCount%state.ToolReviewInterval == 0 {
		if f.agent.fileLogger != nil {
			f.agent.fileLogger.Printf("[REVIEW] 触发工具审查 #%d", state.ToolExecCount/state.ToolReviewInterval)
		}
		// buildToolReview writes a debug report for operators. Keep it out of
		// the model conversation so internal guardrail/tool diagnostics cannot
		// become the assistant's final answer or steer the next turn off task.
		_ = f.agent.buildToolReview(state.FailTracker, state.BrokenTools, state.ToolExecCount, state.TaskID)
	}

	if plan := f.agent.GetActivePlan(); plan != nil {
		progress := plan.FormatProgress()
		if progress != "" {
			agentloop.ReplaceSystemMessageByPrefixBoth(state.Messages, state.ColdMessages, "[Plan:", map[string]interface{}{
				"role":    "system",
				"content": progress,
			})
			if f.agent.fileLogger != nil {
				f.agent.fileLogger.Printf("[PLAN] Progress for %s", plan.PlanID)
			}
		}
	}

	// Capture session memory after the completed response in Generate, with
	// the request context and actual session ID. Intermediate tool rounds are
	// not authoritative outcomes and do not launch an extra background writer.
	return nil
}

func (s *ToolExecutionState) Validate() error {
	if s == nil {
		return fmt.Errorf("tool execution state is nil")
	}
	if s.Messages == nil || s.ColdMessages == nil {
		return fmt.Errorf("tool execution messages are nil")
	}
	if s.ToolCallSummary == nil || s.CallChain == nil || s.TotalDurationMs == nil ||
		s.AllToolResults == nil || s.ToolExecCount == nil {
		return fmt.Errorf("tool execution counters are nil")
	}
	if s.FailTracker == nil {
		s.FailTracker = make(map[string]int)
	}
	if s.BrokenTools == nil {
		s.BrokenTools = make(map[string]bool)
	}
	if s.FailDiagnosticInjected == nil {
		s.FailDiagnosticInjected = make(map[string]bool)
	}
	if s.LoopGuard == nil {
		s.LoopGuard = NewToolCallLoopGuard(2)
	}
	if s.ToolSignatures == nil {
		s.ToolSignatures = make(map[string]int)
	}
	if s.ToolConsecutiveFailures == nil {
		s.ToolConsecutiveFailures = make(map[string]int)
	}
	return nil
}

func (s *ToolExecutionState) AppendRunResult(run AgentToolRunResult, processor *ToolResultProcessor) (ProcessedToolResult, error) {
	if err := s.Validate(); err != nil {
		return ProcessedToolResult{}, err
	}
	if processor == nil {
		processor = NewToolResultProcessor()
	}
	run.Result.Content, _ = researchcore.FilterCurrentNewsToolCallEvidence(s.Input, run.ToolCall, run.Result.Content)
	processed := processor.Process(run, s.FailTracker, s.BrokenTools)
	if run.ExecutionPrevented {
		s.PreventedToolCalls++
	} else {
		s.ExecutedToolCalls++
		if processed.Succeeded {
			delete(s.ToolConsecutiveFailures, run.ToolCall.Name)
		} else {
			s.ToolSignatures[s.LoopGuard.Signature(run.ToolCall)]++
			s.ToolConsecutiveFailures[run.ToolCall.Name]++
		}
	}
	if s.RecoveryTracker != nil {
		s.RecoveryTracker.Observe(run.ToolCall, run.Result, processed.Succeeded)
	}
	if !processed.Succeeded && s.TerminalFailure == nil {
		if boundary, ok := agentresult.DetectTerminalToolFailure(run.ToolCall, run.Result, run.Err, securitycore.IsExternalEvidenceToolName); ok {
			s.TerminalFailure = &boundary
		}
	}
	if content, ok := processed.Message["content"].(string); ok {
		processed.Message["content"] = content
	}
	*s.ToolExecCount++
	*s.TotalDurationMs += run.DurationMs
	*s.ToolCallSummary = append(*s.ToolCallSummary, formatToolCall(run.ToolCall.Name, run.ToolCall.Arguments, processed.Succeeded))
	*s.AllToolResults = append(*s.AllToolResults, toolResultForFinalization(run.ToolCall, run.Result.Content, processed.Succeeded))
	step := map[string]interface{}{
		"type":          "tool",
		"name":          run.ToolCall.Name,
		"success":       processed.Succeeded,
		"duration_ms":   run.DurationMs,
		"input_summary": formatToolCall(run.ToolCall.Name, run.ToolCall.Arguments, processed.Succeeded),
	}
	if processed.Succeeded {
		step["output_summary"] = truncate(processed.Content, 1000)
	} else {
		step["error"] = toolcontract.CompactToolFailure(processed.Content)
		step["output_summary"] = "工具执行失败"
	}
	*s.CallChain = append(*s.CallChain, step)
	if processed.Succeeded && codingcore.ToolCallRequiresAutoVerification(run.ToolCall) {
		s.MutatingToolSucceeded = true
		trigger := codingcore.FormatToolCallBrief(run.ToolCall)
		if trigger != "" && !strings.Contains(s.MutatingToolTrigger, trigger) {
			if s.MutatingToolTrigger != "" {
				s.MutatingToolTrigger += ", "
			}
			s.MutatingToolTrigger += trigger
		}
	}
	appendBothMessages(s.Messages, s.ColdMessages, processed.Message)
	return processed, nil
}

func (s *ToolExecutionState) blockedRun(toolCall ToolCall, opts RunSingleToolOptions) (AgentToolRunResult, bool) {
	decision := s.LoopGuard.CheckWithFailureCircuit(toolCall, s.ToolSignatures, s.ToolConsecutiveFailures)
	if decision.Blocked {
		return AgentToolRunResult{
			Index:              opts.Index,
			ToolCall:           toolCall,
			Result:             decision.Result,
			Err:                decision.Err,
			ExecutionPrevented: true,
			LoopDecision:       decision,
		}, true
	}
	return AgentToolRunResult{}, false
}

type AgentToolExecutor struct {
	agent *LLMAgent
}

func NewAgentToolExecutor(agent *LLMAgent) *AgentToolExecutor {
	return &AgentToolExecutor{agent: agent}
}

func (e *AgentToolExecutor) RunSingleTool(ctx context.Context, toolCall ToolCall, opts RunSingleToolOptions) (run AgentToolRunResult) {
	startedAt := time.Now()
	run = AgentToolRunResult{
		Index:    opts.Index,
		ToolCall: toolCall,
	}
	taskID := taskIDFromContext(ctx)
	receiptTaskID := agentos.ToolExecutionReceiptTaskIDFromContext(ctx, taskID)
	observer := agentos.TaskRuntimeObserverFromContext(ctx)
	var ledger agentos.ToolExecutionLedger
	receiptStarted := false
	defer func() {
		if receiptStarted && ledger != nil {
			finishCtx := context.WithoutCancel(ctx)
			if err := ledger.Finish(finishCtx, receiptTaskID, toolCall, run.Result, run.Err, run.DurationMs); err != nil && e != nil && e.agent != nil && e.agent.fileLogger != nil {
				e.agent.fileLogger.Printf("[TOOL-RECEIPT] finish failed: task=%s call=%s tool=%s err=%v", receiptTaskID, toolCall.ID, toolCall.Name, err)
			}
		}
		if observer != nil {
			observer.ObserveTaskRuntime(taskID, "reasoning", toolCall.ID)
		}
	}()
	defer func() {
		if recovered := recover(); recovered != nil {
			run.Err = fmt.Errorf("tool %q panicked: %v", toolCall.Name, recovered)
			run.Result = ToolResult{ToolCallID: toolCall.ID, Name: toolCall.Name, Content: fmt.Sprintf("tool panic: %v", recovered)}
			run.DurationMs = time.Since(startedAt).Milliseconds()
		}
	}()
	if e == nil || e.agent == nil {
		run.Err = fmt.Errorf("agent tool executor is not initialized")
		run.Result = ToolResult{ToolCallID: toolCall.ID, Name: toolCall.Name, Content: run.Err.Error()}
		return run
	}
	agent := e.agent
	if opts.RecordTranscript && agent.transcript != nil {
		agent.transcript.RecordToolCall(toolCall.Name, toolCall.Arguments, toolCall.ID)
		run.TranscriptCallRecorded = true
	}

	if !opts.SkipHooks && agent.hookManager != nil {
		if hookResult, hookErr := agent.hookManager.RunPreToolUse(agent.name, "", toolCall.Name, toolCall.Arguments); hookErr == nil && !hookResult.Allowed {
			run.Err = fmt.Errorf("blocked by hook")
			run.Result = ToolResult{ToolCallID: toolCall.ID, Name: toolCall.Name, Content: fmt.Sprintf("[BLOCKED by hook] %s", hookResult.Message)}
			return run
		}
	}

	start := time.Now()
	if !opts.SkipSafety {
		if result, err, blocked := agent.checkToolSafety(ctx, toolCall); blocked {
			run.Result = result
			run.Err = err
			run.DurationMs = time.Since(start).Milliseconds()
			return run
		}
	}

	if agent.toolRegistry == nil {
		run.Err = fmt.Errorf("tool registry is nil")
		run.Result = ToolResult{ToolCallID: toolCall.ID, Name: toolCall.Name, Content: run.Err.Error()}
		run.DurationMs = time.Since(start).Milliseconds()
		return run
	}

	if observer != nil {
		observer.ObserveTaskRuntime(taskID, "tool:"+toolCall.Name, toolCall.ID)
	}
	if registeredTool, ok := agent.toolRegistry.Get(toolCall.Name); ok {
		readOnly := false
		if declared, ok := registeredTool.(CallReadOnlyTool); ok {
			readOnly = declared.IsReadOnlyForCall(toolCall.Arguments)
		} else if declared, ok := registeredTool.(ReadOnlyTool); ok {
			readOnly = declared.IsReadOnly()
		}
		ledger = agentos.ToolExecutionLedgerFromContext(ctx)
		if ledger != nil {
			decision, err := ledger.Begin(ctx, receiptTaskID, toolCall, readOnly)
			if err != nil {
				run.Err = fmt.Errorf("tool execution receipt rejected call: %w", err)
				run.Result = ToolResult{ToolCallID: toolCall.ID, Name: toolCall.Name, Content: run.Err.Error()}
				run.DurationMs = time.Since(start).Milliseconds()
				return run
			}
			if decision.Cached != nil {
				run.Result = *decision.Cached
				run.DurationMs = time.Since(start).Milliseconds()
				approvalcore.ConsumeToolApproval(ctx, toolCall)
				return run
			}
			receiptStarted = decision.Execute
		}
	}

	var pendingSnapshots []*workspacetools.FileSnapshot
	if !opts.SkipSnapshot {
		pendingSnapshots = agent.savePreMutationSnapshot(toolCall, ctx)
		if len(pendingSnapshots) > 0 {
			defer agent.snapshotManager.FinalizeSnapshots(pendingSnapshots)
		}
	}

	stopToolHeartbeat := runtimeutil.StartProgressHeartbeat(ctx, runtimeutil.DefaultProgressHeartbeatInterval, func(elapsed time.Duration) {
		emitAgentThinking(agent.callback, taskID, agent.name,
			fmt.Sprintf("%s 仍在执行，已等待 %d 秒", toolCall.Name, int(elapsed.Round(time.Second).Seconds())), "heartbeat")
	})
	defer stopToolHeartbeat()
	defer approvalcore.ConsumeToolApproval(ctx, toolCall)
	result, err := agent.toolRegistry.Execute(ctx, toolCall)
	run.Result = result
	run.Err = err
	run.DurationMs = time.Since(start).Milliseconds()
	recordPresentationToolResult(ctx, toolCall, result, err)
	skilldomain.RecordExplicitSkillToolResult(ctx, toolCall, result, err)
	if receipt := presentationSkillActivationReceipt(ctx, toolCall, result, err); receipt != "" {
		result.Content = strings.TrimSpace(result.Content) + "\n\n" + receipt
		run.Result = result
	}
	return run
}

func (e *AgentToolExecutor) Execute(ctx context.Context, toolCalls []ToolCall, state *ToolExecutionState) error {
	if len(toolCalls) == 0 {
		return nil
	}
	if e == nil || e.agent == nil {
		return fmt.Errorf("agent tool executor is not initialized")
	}
	if e.agent.toolRegistry == nil {
		return fmt.Errorf("tool registry is nil")
	}
	if err := state.Validate(); err != nil {
		return err
	}
	if err := preflightToolApprovals(
		ctx,
		e.agent.permissionEngine,
		e.agent.toolRegistry,
		e.agent.sandbox,
		toolCalls,
	); err != nil {
		return err
	}
	// A one-shot approval must never authorize two calls racing against the
	// same grant. Keep approval-governed batches serial even when each tool
	// otherwise advertises concurrent safety.
	if approvalcore.ToolCallsUseApprovalPolicy(e.agent.permissionEngine, toolCalls) {
		return e.executeBatchSerial(ctx, toolCalls, state)
	}

	for _, batch := range partitionToolCalls(toolCalls, e.agent.toolRegistry) {
		if state.TerminalFailure != nil {
			break
		}
		if batch.concurrent {
			if e.agent.fileLogger != nil {
				e.agent.fileLogger.Printf("[INFO] concurrent %d tools", len(batch.calls))
			}
			if err := e.executeBatchConcurrent(ctx, batch.calls, state); err != nil {
				return err
			}
			if state.TerminalFailure != nil {
				break
			}
			continue
		}
		if err := e.executeBatchSerial(ctx, batch.calls, state); err != nil {
			return err
		}
		if state.TerminalFailure != nil {
			break
		}
	}
	return nil
}

func (e *AgentToolExecutor) executeBatchConcurrent(ctx context.Context, toolCalls []ToolCall, state *ToolExecutionState) error {
	if err := state.Validate(); err != nil {
		return err
	}
	slowTools := map[string]bool{"web_fetch": true, "web_search": true}
	slowSem := make(chan struct{}, 3)
	results := make(chan AgentToolRunResult, len(toolCalls))
	reporter := NewToolCallReporter(e.agent, taskIDFromContext(ctx))
	for idx, tc := range toolCalls {
		reporter.ReportToolStart(tc, "parallel tool")
		if e.agent.transcript != nil {
			e.agent.transcript.RecordToolCall(tc.Name, tc.Arguments, tc.ID)
		}
		go func(i int, toolCall ToolCall) {
			if run, blocked := state.blockedRun(toolCall, RunSingleToolOptions{Index: i}); blocked {
				results <- run
				return
			}
			if slowTools[toolCall.Name] {
				select {
				case slowSem <- struct{}{}:
				case <-ctx.Done():
					results <- AgentToolRunResult{
						Index: i, ToolCall: toolCall,
						Result: ToolResult{ToolCallID: toolCall.ID, Name: toolCall.Name, Content: ctx.Err().Error()},
						Err:    ctx.Err(),
					}
					return
				}
				defer func() { <-slowSem }()
			}
			results <- e.RunSingleTool(ctx, toolCall, RunSingleToolOptions{Index: i})
		}(idx, tc)
	}

	collected := make([]AgentToolRunResult, len(toolCalls))
	completed := make([]bool, len(toolCalls))
	received := 0
	var batchErr error
collectLoop:
	for received < len(toolCalls) {
		select {
		case run := <-results:
			collected[run.Index] = run
			completed[run.Index] = true
			received++
		case <-ctx.Done():
			batchErr = fmt.Errorf("concurrent tool batch cancelled after %d/%d results: %w", received, len(toolCalls), ctx.Err())
			break collectLoop
		}
	}
	processor := NewToolResultProcessor()
	for idx, run := range collected {
		if !completed[idx] {
			continue
		}
		processed, err := state.AppendRunResult(run, processor)
		if err != nil {
			return err
		}
		if run.ExecutionPrevented {
			reporter.ReportLoopBlocked(run.ToolCall, run.LoopDecision.Signature, run.LoopDecision.Attempt)
		}
		if processed.Succeeded {
			reporter.ReportToolSuccess(run.ToolCall, processed.Content, e.agent.countTokens(processed.Content))
		} else if run.Result.Recovery != nil {
			reporter.ReportToolRecoverable(run.ToolCall, run.Result.ErrorCode, processed.Content)
		} else {
			reporter.ReportToolFailure(run.ToolCall, processed.Content)
		}
		reporter.SaveToolResult(state.TaskID, run.ToolCall, processed.Content)
		reporter.ReportToolMessageAppended(run.ToolCall)
		if e.agent.transcript != nil {
			e.agent.transcript.RecordToolResult(run.ToolCall.Name, processed.Content, processed.Succeeded, run.DurationMs, run.ToolCall.ID)
		}
	}
	return batchErr
}

func (e *AgentToolExecutor) executeBatchSerial(ctx context.Context, toolCalls []ToolCall, state *ToolExecutionState) error {
	if err := state.Validate(); err != nil {
		return err
	}
	reporter := NewToolCallReporter(e.agent, taskIDFromContext(ctx))
	processor := NewToolResultProcessor()
	for _, toolCall := range toolCalls {
		log.Printf("[DEBUG] serial tool: %s", toolCall.Name)
		reporter.ReportToolStart(toolCall, "tool")
		run, blocked := state.blockedRun(toolCall, RunSingleToolOptions{RecordTranscript: true})
		if !blocked {
			run = e.RunSingleTool(ctx, toolCall, RunSingleToolOptions{RecordTranscript: true})
		}
		if run.ExecutionPrevented {
			reporter.ReportLoopBlocked(run.ToolCall, run.LoopDecision.Signature, run.LoopDecision.Attempt)
		}
		if run.Err != nil && strings.Contains(run.Result.Content, "[BLOCKED by hook]") && e.agent != nil && e.agent.fileLogger != nil {
			e.agent.fileLogger.Printf("[HOOK] pre_tool_use blocked '%s': %s", toolCall.Name, strings.TrimPrefix(run.Result.Content, "[BLOCKED by hook] "))
		}
		processed, err := state.AppendRunResult(run, processor)
		if err != nil {
			return err
		}
		if processed.Succeeded {
			reporter.ReportToolSuccess(run.ToolCall, processed.Content, e.agent.countTokens(processed.Content))
		} else if run.Result.Recovery != nil {
			reporter.ReportToolRecoverable(run.ToolCall, run.Result.ErrorCode, processed.Content)
		} else {
			reporter.ReportToolFailure(run.ToolCall, processed.Content)
		}
		reporter.SaveToolResult(state.TaskID, run.ToolCall, processed.Content)
		reporter.ReportToolMessageAppended(run.ToolCall)
		if e.agent.transcript != nil {
			e.agent.transcript.RecordToolResult(run.ToolCall.Name, processed.Content, processed.Succeeded, run.DurationMs, run.ToolCall.ID)
		}
		if state.TerminalFailure != nil {
			break
		}
	}
	return nil
}

func (a *LLMAgent) checkToolSafety(ctx context.Context, toolCall ToolCall) (ToolResult, error, bool) {
	if a == nil {
		return ToolResult{}, nil, false
	}
	if blocked, reason := skilldomain.ExplicitSkillPolicyBlocksTool(ctx, toolCall); blocked {
		err := fmt.Errorf("tool %q is blocked: %s", toolCall.Name, reason)
		return ToolResult{ToolCallID: toolCall.ID, Name: toolCall.Name, Content: "[BLOCKED: EXPLICIT SKILL POLICY] " + err.Error()}, err, true
	}
	if blocked, reason := permissionpolicy.UserToolPolicyBlocksTool(ctx, toolCall, a.toolRegistry, toolcontract.IsReadOnlyToolName); blocked {
		err := fmt.Errorf("tool %q is blocked: %s", toolCall.Name, reason)
		return ToolResult{
			ToolCallID:         toolCall.ID,
			Name:               toolCall.Name,
			Content:            "[BLOCKED: USER REQUEST POLICY] " + err.Error(),
			ExecutionSucceeded: toolExecutionOutcome(false),
			ErrorCode:          agentresult.UserRequestPolicyConflictErrorCode,
		}, err, true
	}
	if result, err, blocked := checkPresentationToolGate(ctx, toolCall); blocked {
		return result, err, true
	}
	if a.safetyGuard == nil {
		if a.permissionEngine == nil {
			return ToolResult{}, nil, false
		}
	}
	if a.permissionEngine != nil {
		allowed, approvalRequired, reason := a.permissionEngine.Check(toolCall.Name, toolCall.Arguments)
		if !allowed {
			code := "permission_denied"
			prefix := "[BLOCKED: PERMISSION POLICY] "
			if approvalRequired {
				code = "approval_required"
				prefix = "[APPROVAL REQUIRED] "
				if approvalErr := approvalcore.AuthorizeToolApproval(ctx, toolCall, reason); approvalErr == nil {
					return ToolResult{}, nil, false
				} else {
					if coded, ok := approvalErr.(interface{ MiddlewareErrorCode() string }); ok {
						code = coded.MiddlewareErrorCode()
					}
					if code != "approval_required" {
						prefix = "[BLOCKED: APPROVAL POLICY] "
					}
					reason = approvalErr.Error()
					return ToolResult{
						ToolCallID:         toolCall.ID,
						Name:               toolCall.Name,
						Content:            prefix + reason,
						ExecutionSucceeded: toolExecutionOutcome(false),
						ErrorCode:          code,
					}, approvalErr, true
				}
			}
			if strings.TrimSpace(reason) == "" {
				reason = fmt.Sprintf("tool %q is not allowed by the active permission policy", toolCall.Name)
			}
			err := fmt.Errorf("%s", reason)
			return ToolResult{
				ToolCallID:         toolCall.ID,
				Name:               toolCall.Name,
				Content:            prefix + reason,
				ExecutionSucceeded: toolExecutionOutcome(false),
				ErrorCode:          code,
			}, err, true
		}
		return ToolResult{}, nil, false
	}
	_, blockErr := a.safetyGuard.CheckTool(toolCall.Name, toolCall.Arguments)
	if blockErr != nil {
		return ToolResult{ToolCallID: toolCall.ID, Name: toolCall.Name, Content: blockErr.Error()}, blockErr, true
	}
	return ToolResult{}, nil, false
}

func (a *LLMAgent) savePreMutationSnapshot(toolCall ToolCall, runContexts ...context.Context) []*workspacetools.FileSnapshot {
	if a == nil || a.snapshotManager == nil {
		return nil
	}
	var runContext context.Context
	if len(runContexts) == 1 {
		runContext = runContexts[0]
	}
	var paths []string
	if a.toolRegistry != nil {
		if registered, ok := a.toolRegistry.Get(toolCall.Name); ok {
			if snapshotter, ok := registered.(SnapshotPathsTool); ok {
				paths = snapshotter.SnapshotPaths(toolCall.Arguments)
			}
		}
	}
	if len(paths) == 0 {
		switch toolCall.Name {
		case "write_file", "edit_file", "json_patch":
			if path := firstStringArg(toolCall.Arguments, "path", "file_path", "file"); path != "" {
				paths = append(paths, path)
			}
		}
	}

	seen := make(map[string]bool)
	snapshots := make([]*workspacetools.FileSnapshot, 0, len(paths))
	for _, path := range paths {
		if err := enforcePersonalWorkspacePath(runContext, a.workspace, path); err != nil {
			if a.fileLogger != nil {
				a.fileLogger.Printf("[SNAPSHOT] rejected path for %s: %v", toolCall.Name, err)
			}
			continue
		}
		if !filepath.IsAbs(path) && a.workspace != "" {
			path = filepath.Join(a.workspace, path)
		}
		path = filepath.Clean(path)
		key := projectctx.IdentityKey(path)
		if path == "" || seen[key] {
			continue
		}
		seen[key] = true
		snapshot, err := a.snapshotManager.SaveSnapshot(path, toolCall.Name)
		if err != nil {
			if a.fileLogger != nil {
				a.fileLogger.Printf("[SNAPSHOT] failed to snapshot %s for %s: %v", path, toolCall.Name, err)
			}
			continue
		}
		if snapshot != nil {
			snapshots = append(snapshots, snapshot)
		}
	}
	return snapshots
}

func firstStringArg(args map[string]interface{}, keys ...string) string {
	for _, key := range keys {
		if value, ok := args[key].(string); ok && value != "" {
			return value
		}
	}
	return ""
}

func cloneAPIMessages(messages []map[string]interface{}) []map[string]interface{} {
	if len(messages) == 0 {
		return nil
	}
	out := make([]map[string]interface{}, len(messages))
	for i, msg := range messages {
		out[i] = cloneAPIMessage(msg)
	}
	return out
}

func appendBothMessages(hot, cold *[]map[string]interface{}, msg map[string]interface{}) {
	if hot != nil {
		*hot = append(*hot, cloneAPIMessage(msg))
	}
	if cold != nil {
		*cold = append(*cold, cloneAPIMessage(msg))
	}
}

func cloneAPIMessage(msg map[string]interface{}) map[string]interface{} {
	if msg == nil {
		return nil
	}
	out := make(map[string]interface{}, len(msg))
	for k, v := range msg {
		out[k] = cloneAPIValue(v)
	}
	return out
}

func cloneAPIValue(v interface{}) interface{} {
	switch typed := v.(type) {
	case map[string]interface{}:
		return cloneAPIMessage(typed)
	case []map[string]interface{}:
		out := make([]map[string]interface{}, len(typed))
		for i, item := range typed {
			out[i] = cloneAPIMessage(item)
		}
		return out
	case []interface{}:
		out := make([]interface{}, len(typed))
		for i, item := range typed {
			out[i] = cloneAPIValue(item)
		}
		return out
	default:
		return typed
	}
}
