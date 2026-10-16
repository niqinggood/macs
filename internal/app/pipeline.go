package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"log"
	"mac/internal/modelruntime"
	"net/http"
	"net/url"
	"os"
	osexec "os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"mac/internal/agentresult"
	approvalcore "mac/internal/approval"
	"mac/internal/eventbus"
	middlewarecore "mac/internal/middleware"
	"mac/internal/permissionpolicy"
	pipelinecore "mac/internal/pipeline"
	"mac/internal/portablepath"
	"mac/internal/projectctx"
	sessioncore "mac/internal/session"
	skilldomain "mac/internal/skills"
	"mac/internal/textnormalize"
	"mac/internal/textutil"
	"mac/internal/toolcontract"
	"mac/internal/toolregistry"
	analysistools "mac/internal/tools/analysis"
	contenttools "mac/internal/tools/content"
	knowledgetools "mac/internal/tools/knowledge"

	"gorm.io/gorm"
)

// Pipeline blacklist: tools permanently blocked for all AI employees.
// These are destructive or dangerous — no pipeline step should run them unchecked.
var pipelineBlacklist = map[string]bool{
	"persistent_shell": true, // default-deny; allowed only when a step explicitly lists it
	"delete_file":      true, // destructive filesystem op
}

var pipelineAlwaysBlacklist = map[string]bool{
	"delete_file": true,
}

// ====================== Pipeline Config Types ======================

type PipelineConfig = pipelinecore.PipelineConfig
type PipelineTrigger = pipelinecore.PipelineTrigger
type Pipeline = pipelinecore.Pipeline
type PipelineStep = pipelinecore.PipelineStep

type PipelineOutputContract = pipelinecore.PipelineOutputContract
type PipelineOutputContractViolation = pipelinecore.PipelineOutputContractViolation

type PipelineSlotContract = pipelinecore.PipelineSlotContract
type PipelineSlotValidationCheck = pipelinecore.PipelineSlotValidationCheck
type PipelineSlotFillRecord = pipelinecore.PipelineSlotFillRecord

const (
	pipelineSlotPending = pipelinecore.SlotPending
	pipelineSlotFilling = pipelinecore.SlotFilling
	pipelineSlotFilled  = pipelinecore.SlotFilled
	pipelineSlotMissing = pipelinecore.SlotMissing
	pipelineSlotInvalid = pipelinecore.SlotInvalid

	pipelineSlotFilledMarker  = pipelinecore.SlotFilledMarker
	pipelineSlotMissingMarker = pipelinecore.SlotMissingMarker
)

func pipelineSlotStepContractInput(step PipelineStep) pipelinecore.SlotStepContractInput {
	input := pipelinecore.SlotStepContractInput{
		Name:              step.Name,
		Prompt:            step.Prompt,
		HasSourceContract: step.SourceContract != nil,
	}
	if step.OutputContract != nil {
		input.OutputMinChars = step.OutputContract.MinChars
		input.OutputRequiredSections = append([]string(nil), step.OutputContract.RequiredSections...)
	}
	if step.ArtifactContract != nil {
		input.ArtifactPaths = append([]string(nil), step.ArtifactContract.Paths...)
		input.RejectArtifactPlaceholders = step.ArtifactContract.RejectPlaceholders
	}
	return input
}

func ensurePipelineSlotContracts(pipeline Pipeline) Pipeline {
	steps := append([]PipelineStep(nil), pipeline.Steps...)
	previousSlot := ""
	for i := range steps {
		step := steps[i]
		if step.Slot != nil {
			step.Slot = pipelinecore.CloneSlotContract(step.Slot)
		} else {
			step.Slot = derivePipelineSlotContract(pipeline.Name, step, i, previousSlot)
		}
		if step.Slot != nil {
			if strings.TrimSpace(step.Slot.ID) == "" {
				step.Slot.ID = fmt.Sprintf("step_%02d", i)
			}
			previousSlot = step.Slot.ID
		}
		if !pipelineStepHasStopRoute(step) {
			step.Routing = append(append([]StepRoute(nil), step.Routing...), StepRoute{
				When: "contains '<!--PIPELINE_STOP-->'",
				Goto: "__stop__",
			})
		}
		steps[i] = step
	}
	pipeline.Steps = steps
	return pipeline
}

func pipelineStepHasStopRoute(step PipelineStep) bool {
	for _, route := range step.Routing {
		if strings.TrimSpace(route.Goto) == "__stop__" && strings.Contains(route.When, pipelineStopMarker) {
			return true
		}
	}
	return false
}

// pipelineSlotStepInput keeps business slots on the accepted task brief's
// source material. Host identity and obsolete clarification text stay out.
func pipelineSlotStepInput(pipeline Pipeline, fullInput string, stepIndex int) string {
	if stepIndex <= 0 || strings.TrimSpace(pipeline.AIEmployeeOriginalTask) == "" {
		return fullInput
	}
	input := strings.TrimSpace(pipeline.AIEmployeeOriginalTask)
	if path := strings.TrimSpace(pipeline.AIEmployeeContextPath); path != "" {
		input += "\n\n" + aiEmployeeContextPathMarker + " " + path
	}
	return input
}

func derivePipelineSlotContract(pipelineName string, step PipelineStep, index int, previousSlot string) *PipelineSlotContract {
	return pipelinecore.DeriveSlotContract(pipelineName, pipelineSlotStepContractInput(step), index, previousSlot, aiEmployeeIntakeStepName)
}

func appendPipelineSlotInstruction(prompt string, _ *PipelineExecution, step *PipelineStep, _ int) string {
	if step == nil {
		return prompt
	}
	return pipelinecore.AppendSlotInstruction(prompt, step.Slot, pipelineWaitingInputMarker, pipelineStopMarker)
}

type pipelineResumeSeedContextKey struct{}

type pipelineResumeSeed struct {
	SourceExecID string
	WaitingStep  int
	Steps        []StepResult
}

func withPipelineResumeSeed(ctx context.Context, source *PipelineExecution) context.Context {
	if ctx == nil || source == nil {
		return ctx
	}
	source.mu.Lock()
	seed := pipelineResumeSeed{SourceExecID: source.ID, WaitingStep: -1, Steps: append([]StepResult(nil), source.Steps...)}
	if source.CompletionReason != nil && source.CompletionReason.Code == aeWaitingInputStatus {
		seed.WaitingStep = source.CompletionReason.StepIndex
	}
	source.mu.Unlock()
	if seed.WaitingStep < 0 {
		return ctx
	}
	return context.WithValue(ctx, pipelineResumeSeedContextKey{}, seed)
}

func seedPipelineExecutionSlots(ctx context.Context, exec *PipelineExecution, pipeline Pipeline) {
	if ctx == nil || exec == nil {
		return
	}
	seed, ok := ctx.Value(pipelineResumeSeedContextKey{}).(pipelineResumeSeed)
	if !ok || seed.SourceExecID == "" || seed.WaitingStep < 0 {
		return
	}
	exec.ResumeFromExecID = seed.SourceExecID
	if seed.WaitingStep == 0 {
		return
	}
	oldBySlot := make(map[string]StepResult)
	for i, result := range seed.Steps {
		if i <= 0 || i >= seed.WaitingStep || (result.Status != "completed" && result.Status != "degraded") {
			continue
		}
		if strings.TrimSpace(result.SlotID) != "" {
			oldBySlot[result.SlotID] = result
		}
	}
	for i := 1; i < len(exec.Steps) && i < len(pipeline.Steps); i++ {
		slot := pipeline.Steps[i].Slot
		if slot == nil {
			continue
		}
		old, found := oldBySlot[slot.ID]
		if !found {
			continue
		}
		old.ReusedFromExec = seed.SourceExecID
		old.SlotStatus = pipelineSlotFilled
		old.SlotValidation = "reused_after_prior_validation"
		exec.Steps[i] = old
	}
}

func pipelineStepHasReusableSlot(exec *PipelineExecution, stepIndex int) bool {
	if exec == nil || stepIndex <= 0 {
		return false
	}
	exec.mu.Lock()
	defer exec.mu.Unlock()
	if stepIndex >= len(exec.Steps) {
		return false
	}
	result := exec.Steps[stepIndex]
	return result.ReusedFromExec != "" && result.SlotStatus == pipelineSlotFilled && (result.Status == "completed" || result.Status == "degraded")
}

func buildPipelineSlotFillRecord(step *PipelineStep, result StepResult) *PipelineSlotFillRecord {
	if step == nil || step.Slot == nil {
		return nil
	}
	clean := strings.TrimSpace(stripPipelineControlMarkers(result.Output))
	input := pipelinecore.SlotFillInput{
		Contract:            step.Slot,
		Status:              result.SlotStatus,
		CleanOutput:         clean,
		ProgressOnly:        isProgressOnlyFinalOutput(clean),
		ArtifactPaths:       pipelineSlotArtifactPaths(result),
		HasSourceContract:   step.SourceContract != nil,
		HasArtifactContract: step.ArtifactContract != nil,
		HasOutputContract:   step.OutputContract != nil,
		ResultAccepted:      result.Status == "completed" || result.Status == "degraded",
	}
	if result.SourceSnapshot != nil {
		input.SourceSnapshot = result.SourceSnapshot.Path
	}
	if step.SourceContract != nil {
		input.SourceContractRequired = step.SourceContract.Required
		input.SourceWhenReferenced = step.SourceContract.WhenReferenced
	}
	if step.ArtifactContract != nil {
		input.ArtifactContractPaths = append([]string(nil), step.ArtifactContract.Paths...)
	}
	return pipelinecore.BuildSlotFillRecord(input)
}

func pipelineSlotArtifactPaths(result StepResult) []string {
	calls := make([]pipelinecore.SlotToolCall, 0, len(result.ToolCalls))
	for _, call := range result.ToolCalls {
		converted := pipelinecore.SlotToolCall{
			Failed:    pipelineToolCallRecordFailed(call),
			Params:    call.Params,
			Result:    call.Result,
			Artifacts: make([]pipelinecore.SlotArtifact, 0, len(call.Artifacts)),
		}
		for _, artifact := range call.Artifacts {
			converted.Artifacts = append(converted.Artifacts, pipelinecore.SlotArtifact{Path: artifact.Path, Validated: artifact.Validated})
		}
		calls = append(calls, converted)
	}
	return pipelinecore.SlotArtifactPaths(calls)
}

func validatePipelineOutputContracts(pipeline Pipeline) error {
	for i := range pipeline.Steps {
		path := fmt.Sprintf("step %d (%q)", i, pipeline.Steps[i].Name)
		if err := validatePipelineStepOutputContractConfig(&pipeline.Steps[i], path); err != nil {
			return err
		}
	}
	return nil
}

func validatePipelineStepOutputContractConfig(step *PipelineStep, path string) error {
	if step == nil {
		return nil
	}
	if err := pipelinecore.ValidateOutputContractConfig(step.OutputContract, path); err != nil {
		return err
	}
	for i := range step.Parallel {
		childPath := fmt.Sprintf("%s parallel step %d (%q)", path, i, step.Parallel[i].Name)
		if err := validatePipelineStepOutputContractConfig(&step.Parallel[i], childPath); err != nil {
			return err
		}
	}
	return nil
}

func applyPipelineOutputContract(contract *PipelineOutputContract, output string) (string, bool, error) {
	return pipelinecore.ApplyOutputContract(contract, output)
}

func pipelineOutputContractDegradedReason(contract *PipelineOutputContract, output string) string {
	return pipelinecore.OutputContractDegradedReason(contract, output)
}

func pipelineTurnCheckpointKey(execID string, stepIndex, revision, attempt int, step *PipelineStep, prompt string) string {
	fingerprint := pipelinecore.StepFingerprint{Prompt: prompt}
	if step != nil {
		fingerprint.Name = step.Name
		fingerprint.Model = step.Model
		fingerprint.MaxIter = step.MaxIter
		fingerprint.NoTools = step.NoTools
		fingerprint.Tools = append([]string(nil), step.Tools...)
		fingerprint.RequiredTools = append([]string(nil), step.RequiredTools...)
		fingerprint.RequiredSkills = append([]string(nil), step.RequiredSkills...)
	}
	return pipelinecore.TurnCheckpointKey(execID, stepIndex, revision, attempt, fingerprint)
}

func loadLatestPipelineTurnCheckpoint(workspace, turnTaskID string) (*sessioncore.TurnCheckpoint, string, error) {
	path := pipelinecore.LatestTurnArtifact(workspace, turnTaskID, ".checkpoint.json")
	if path == "" {
		return nil, "", os.ErrNotExist
	}
	checkpoint, err := sessioncore.LoadTurnCheckpoint(path)
	if err != nil {
		return nil, path, err
	}
	if strings.TrimSpace(checkpoint.TaskID) != strings.TrimSpace(turnTaskID) {
		return nil, path, fmt.Errorf("turn checkpoint task identity mismatch")
	}
	return checkpoint, path, nil
}

const pipelineCheckpointVersion = pipelinecore.CheckpointVersion

const (
	pipelineCheckpointPhaseStepPending     = pipelinecore.CheckpointPhaseStepPending
	pipelineCheckpointPhaseStepRunning     = pipelinecore.CheckpointPhaseStepRunning
	pipelineCheckpointPhaseStepFinalized   = pipelinecore.CheckpointPhaseStepFinalized
	pipelineCheckpointPhasePostProcessing  = pipelinecore.CheckpointPhasePostProcessing
	pipelineCheckpointPhaseWaitingApproval = pipelinecore.CheckpointPhaseWaitingApproval
	pipelineCheckpointPhaseCompleted       = pipelinecore.CheckpointPhaseCompleted
)

type PipelineTurnCheckpoint = pipelinecore.PipelineTurnCheckpoint
type PipelineCheckpoint = pipelinecore.PipelineCheckpoint

func pipelineStepExecutionID(execID string, stepIndex, revision, attempt int, discriminator ...string) string {
	return pipelinecore.StepExecutionID(execID, stepIndex, revision, attempt, discriminator...)
}

func clonePipelineCheckpoint(checkpoint *PipelineCheckpoint) *PipelineCheckpoint {
	return pipelinecore.CloneCheckpoint(checkpoint)
}

func prunePipelineTurnCheckpoints(checkpoint *PipelineCheckpoint) {
	pipelinecore.PruneTurnCheckpoints(checkpoint)
}

func pipelineRepeatCountsToStrings(source map[int]int) map[string]int {
	return pipelinecore.RepeatCountsToStrings(source)
}

func pipelineRepeatCountsFromStrings(source map[string]int) map[int]int {
	return pipelinecore.RepeatCountsFromStrings(source)
}

func pipelineCheckpointPath(workspace, execID string) string {
	return pipelinecore.CheckpointPath(workspace, execID)
}

func writePipelineCheckpoint(path string, checkpoint *PipelineCheckpoint) error {
	return pipelinecore.WriteCheckpoint(path, checkpoint)
}

func loadLatestCompletedPipelineTurn(workspace, turnTaskID string) (string, string, string, bool) {
	return pipelinecore.LoadLatestCompletedTurn(workspace, turnTaskID)
}

func loadPipelineCheckpoint(path string) (*PipelineCheckpoint, error) {
	return pipelinecore.LoadCheckpoint(path)
}

func pipelineEvolutionSnapshot(exec *PipelineExecution) pipelinecore.ExecutionSnapshot {
	if exec == nil {
		return pipelinecore.ExecutionSnapshot{}
	}
	snapshot := pipelinecore.ExecutionSnapshot{
		PipelineName: exec.PipelineName,
		Status:       exec.Status,
		CreatedAt:    exec.CreatedAt,
		Steps:        make([]pipelinecore.StepSnapshot, len(exec.Steps)),
	}
	for index, step := range exec.Steps {
		snapshot.Steps[index] = pipelinecore.StepSnapshot{
			Name:   step.Name,
			Status: step.Status,
			Error:  step.Error,
		}
		for _, call := range step.ToolCalls {
			snapshot.Steps[index].ToolCalls = append(snapshot.Steps[index].ToolCalls, pipelinecore.ToolCallSnapshot{
				ToolName: call.ToolName, DurationMs: call.DurationMs,
			})
		}
	}
	return snapshot
}

// fallbackStepName returns the name of a fallback step if one is configured.
// Convention: if a step is named "xxx", its fallback is "xxx_fallback".
// Can be overridden by explicit EscalateTo configuration.
var pipelineDatabaseIDPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]{0,63}$`)
var pipelineDatabaseEnvPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func validatePipelineDatabaseConnections(p Pipeline) error {
	profiles := make(map[string]PipelineDatabaseConnection, len(p.DatabaseConnections))
	for _, profile := range p.DatabaseConnections {
		id := strings.TrimSpace(profile.ID)
		if !pipelineDatabaseIDPattern.MatchString(id) {
			return fmt.Errorf("database connection id %q must start with a letter and contain only letters, digits, _ or -", profile.ID)
		}
		if _, exists := profiles[id]; exists {
			return fmt.Errorf("duplicate database connection id %q", id)
		}
		if strings.TrimSpace(profile.Driver) == "" {
			return fmt.Errorf("database connection %q requires driver", id)
		}
		permission := strings.TrimSpace(profile.Permission)
		if permission != "" && permission != "readonly" {
			return fmt.Errorf("pipeline database connection %q must be readonly; use approved domain tools for writes", id)
		}
		if envName := strings.TrimSpace(profile.DSNEnv); envName != "" && !pipelineDatabaseEnvPattern.MatchString(envName) {
			return fmt.Errorf("database connection %q has invalid dsn_env %q", id, envName)
		}
		if strings.TrimSpace(profile.DSN) != "" && !strings.EqualFold(strings.TrimSpace(profile.Driver), "sqlite") {
			return fmt.Errorf("database connection %q must use dsn_env for non-SQLite credentials", id)
		}
		if profile.Enabled && strings.TrimSpace(profile.DSN) == "" && strings.TrimSpace(profile.DSNEnv) == "" {
			return fmt.Errorf("enabled database connection %q requires dsn or dsn_env", id)
		}
		profiles[id] = profile
	}

	var validateSteps func([]PipelineStep) error
	validateSteps = func(steps []PipelineStep) error {
		for _, step := range steps {
			connectionID := strings.TrimSpace(step.DatabaseConnection)
			if connectionID != "" {
				if _, ok := profiles[connectionID]; !ok {
					return fmt.Errorf("step %q references unknown database connection %q", step.Name, connectionID)
				}
			}
			if stepUsesTool(step, "db_query") && connectionID == "" && len(profiles) > 1 {
				return fmt.Errorf("step %q uses db_query and must choose one database_connection", step.Name)
			}
			if err := validateSteps(step.Parallel); err != nil {
				return err
			}
		}
		return nil
	}
	return validateSteps(p.Steps)
}

func stepUsesTool(step PipelineStep, name string) bool {
	for _, toolName := range step.Tools {
		if strings.TrimSpace(toolName) == name {
			return true
		}
	}
	return false
}

// StepExample is a single few-shot routing example for a pipeline step.
type StepExample = pipelinecore.StepExample

// StepRoute defines a conditional jump based on step output.
// ApprovalTemplate defines a structured approval form.
type ApprovalTemplate struct {
	Name   string          `json:"name"`
	Title  string          `json:"title"`
	Fields []ApprovalField `json:"fields"`
}

type ApprovalField struct {
	Key      string   `json:"key"`
	Label    string   `json:"label"`
	Type     string   `json:"type"` // string, number, date, select
	Required bool     `json:"required"`
	Options  []string `json:"options,omitempty"`
}

func buildStepExecutionPrompt(stepPrompt string) string {
	return fmt.Sprintf(`## 当前步骤唯一任务
%s

---

## 原始用户需求（仅作为整体背景）
${input}

---

## 阶段执行规则
- 只完成“当前步骤唯一任务”，不要提前执行后续步骤，也不要重做已经由上游步骤完成的工作。
- 当前步骤决定本阶段做什么，并继承受理槽位形成的约束账本。精确标的、项目路径、指定文件/来源等“任务身份坐标”不可替换；地区、行业、企业规模、时间和数量是筛选维度，不属于身份坐标。安全、权限、明确排除项，以及用户明确说“仅限/只要/必须/至少/至多/不超过/不要/禁止”的条件属于硬约束，不得偷偷违反。
- 地区、行业、企业规模、时间、数量、风格等普通描述默认是优先条件；“500 人以上”这类明确数值上下限默认属于硬约束，但用户同时明确说“没有就放宽/类似的也可以/不足时随便”等兜底授权时，应按用户指定顺序降为可放宽条件。先按全部条件严格执行；结果不足时只能逐项放宽获准的优先条件，并记录放宽了什么、为何放宽、候选满足哪些条件。
- 精确匹配与放宽后的近似候选必须分层展示，不能把近似候选冒充完全满足。若硬约束下没有结果，如实报告零个精确匹配，同时可提供清楚标为“不满足哪些硬条件”的扩展候选，供用户决定是否接受。
- 用户没有指定放宽先后顺序时，按对目标价值影响最小的条件先放宽并标明这是系统推断，不得声称该顺序由用户指定。
- 如果原始用户需求和当前步骤模板在交付规模上冲突，以用户明确限制为准；例如用户要求“小范围验收/只给计划/不需要完整报告”时，不得扩展成完整全量报告。
- 当前步骤完成后必须直接输出该步骤结果；不要输出“我需要/让我/我先/接下来/我将/let me/now let me”等过程话术。
- 如果资料不足，直接向用户说明缺少什么并请求补充，不要描述自己的思考过程。
- 如果本步骤生成文件，必须给出真实保存路径；如果本步骤不要求生成文件，不要声称已保存文件。`, strings.TrimSpace(stepPrompt))
}

func appendStepToolBoundaryInstruction(prompt string, step *PipelineStep) string {
	rule := stepToolBoundaryInstruction(step)
	if rule == "" {
		return prompt
	}
	return prompt + "\n\n---\n\n" + rule
}

func stepToolBoundaryInstruction(step *PipelineStep) string {
	if step == nil {
		return ""
	}
	if step.NoTools {
		return `## 本步骤工具边界
- 本步骤不允许调用工具，必须直接基于已有上下文输出结果。`
	}
	if len(step.Tools) == 0 {
		return ""
	}
	tools := uniqueSortedStrings(stepToolAllowlist(step))
	if len(tools) == 0 {
		return ""
	}
	return fmt.Sprintf(`## 本步骤工具边界
- 本步骤只允许调用这些工具：%s。
- 当前执行已绑定唯一的用户工作区。文件必须保存到当前任务工作区内，优先使用相对路径（例如 .openkitty/storage/...）；不要使用全局目录、其他用户目录或自行猜测的宿主机绝对路径。
- 不要调用未列出的工具；如果需要修正文件或脚本，只能使用上面列出的工具完成。
- 工具执行失败时，换用已列工具或直接说明失败原因；失败统计只用于定位，不代表工具能力不可用。`, strings.Join(tools, ", "))
}

func shouldRetryPipelineStepError(err error) bool {
	if err == nil {
		return false
	}
	text := strings.ToLower(strings.TrimSpace(err.Error()))
	if text == "" {
		return false
	}
	nonTransient := []string{
		"context canceled",
		"context cancelled",
		"iteration",
		"max iterations",
		"tool",
		"工具",
		"不存在",
		"not found",
		"not registered",
		"invalid_args",
		"guardrail",
	}
	if containsAnyText(text, nonTransient) {
		return false
	}
	transient := []string{
		"timeout",
		"deadline exceeded",
		"temporary",
		"connection reset",
		"connection refused",
		"eof",
		"429",
		"rate limit",
		"too many requests",
		"500",
		"502",
		"503",
		"504",
		"service unavailable",
		"bad gateway",
	}
	return containsAnyText(text, transient)
}

func pipelineTargetScopeInstruction(input string) string {
	target := detectPipelineTargetScope(input)
	if target == "" {
		return ""
	}
	return fmt.Sprintf(`## 本次任务目标范围
- 用户显式指定的目标项目/路径：%s
- 项目理解阶段必须优先读取或扫描该目标；如果目标不可访问，必须直接说明“目标路径不可访问/证据不足”，不要改用历史项目资料补位。
- 后续步骤只能把来自该目标、当前任务上游步骤、或本轮工具调用结果的内容当作本次项目证据。
- 如果上游材料、记忆、上下文或搜索结果出现其他项目名/路径，且不能证明来自本次目标，必须标为“证据冲突/待验证”，不得写成已验证事实。
- 不得引用历史 session/context/memory 中的项目画像作为本次已验证事实，除非其中明确包含本次目标路径和本轮读取证据。`, target)
}

func detectPipelineTargetScope(input string) string {
	text := strings.TrimSpace(input)
	if text == "" {
		return ""
	}
	if m := regexp.MustCompile(`(?i)\b([a-z]):[\\/][^\s，。；;、]+`).FindString(text); m != "" {
		return normalizePipelineTargetPath(trimPipelineTargetSentenceSuffix(m))
	}
	re := regexp.MustCompile(`(?i)([a-z])盘\s*(?:下|里|的)?\s*([a-z0-9_.-]+(?:[\\/][a-z0-9_.-]+)*)?`)
	m := re.FindStringSubmatch(text)
	if len(m) == 0 {
		return ""
	}
	drive := strings.ToUpper(m[1]) + ":"
	rel := strings.TrimSpace(m[2])
	if rel == "" {
		return drive + `\`
	}
	return normalizePipelineTargetPath(drive + `\` + rel)
}

func trimPipelineTargetSentenceSuffix(path string) string {
	// Chinese requests often place prose directly after a Windows path, e.g.
	// "E:\\DEV\\ai-node是我的一个项目". Windows permits CJK filenames, so keep
	// the rule deliberately limited to common sentence transitions.
	for _, marker := range []string{
		"是我的", "是我", "是一个", "是个", "你帮我", "请帮我", "帮我", "请帮", "用来", "用于",
	} {
		if index := strings.Index(path, marker); index > 2 {
			path = path[:index]
		}
	}
	return path
}

func normalizePipelineTargetPath(path string) string {
	path = strings.Trim(path, " \t\r\n，。；;、")
	path = strings.ReplaceAll(path, "/", `\`)
	return path
}

func appendPipelineTargetScopeInstruction(prompt, input string) string {
	var sections []string
	if scope := pipelineTargetScopeInstruction(input); scope != "" {
		sections = append(sections, scope)
	}
	if delivery := pipelineUserDeliveryConstraintInstruction(input); delivery != "" {
		sections = append(sections, delivery)
	}
	if len(sections) > 0 {
		return prompt + "\n\n---\n\n" + strings.Join(sections, "\n\n")
	}
	return prompt
}

func pipelineUserDeliveryConstraintInstruction(input string) string {
	text := strings.TrimSpace(input)
	if text == "" {
		return ""
	}
	markers := []string{
		"小范围", "只输出", "只要", "先输出", "不需要完整", "不要完整", "无需完整", "不用完整",
		"不需要全网", "不要全网", "不需要完整全网报告", "验证计划", "候选方向",
	}
	if !containsAnyText(text, markers) {
		return ""
	}
	return fmt.Sprintf(`## 用户显式交付约束（最高优先级）
- 用户原文约束：%s
- 必须按用户限定的范围、数量和交付类型输出；不得扩大成模板里的完整全量报告。
- 如果用户要求“小范围验收/验证计划/候选方向/不需要完整报告”，最终输出只能围绕这些内容组织。
- 不得额外补齐模板要求的全量章节、全量潜客表或完整全网报告，除非用户明确要求。`, truncate(text, 500))
}

func applyPipelineDeliveryConstraint(output, input string) string {
	if pipelineUserDeliveryConstraintInstruction(input) == "" {
		return output
	}
	if !containsAnyText(input, []string{"验证计划", "候选方向", "不需要完整", "不要完整", "小范围"}) {
		return output
	}
	candidates := extractCandidateDirections(output, 3)
	plan := extractPlanSection(output)
	if len(candidates) == 0 && plan == "" {
		return output
	}
	var sb strings.Builder
	sb.WriteString("## 小范围验收结果\n\n")
	sb.WriteString("本次按用户要求仅保留验证计划和 3 个候选方向，不展开完整全网报告。\n\n")
	sb.WriteString("## 3 个候选方向\n")
	if len(candidates) == 0 {
		sb.WriteString("1. 待补充：当前输出中未稳定提取到候选方向。\n")
	} else {
		for i, c := range candidates {
			fmt.Fprintf(&sb, "%d. %s\n", i+1, c)
		}
	}
	if plan != "" {
		sb.WriteString("\n## 验证计划\n")
		sb.WriteString(plan)
	}
	return strings.TrimSpace(sb.String())
}

func extractCandidateDirections(output string, limit int) []string {
	if limit <= 0 {
		return nil
	}
	lines := strings.Split(output, "\n")
	var out []string
	seen := map[string]bool{}
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "|") || strings.Contains(trimmed, "---") {
			continue
		}
		cells := splitMarkdownTableRow(trimmed)
		if len(cells) < 3 {
			continue
		}
		rowText := strings.Join(cells, " ")
		if strings.Contains(rowText, "优先级") || strings.Contains(rowText, "客户画像") || strings.Contains(rowText, "公司/机构") {
			continue
		}
		if !containsAnyText(rowText, []string{"P0", "P1", "高", "中", "待验证", "已验证"}) {
			continue
		}
		name := cleanMarkdownInline(cells[1])
		industry := ""
		if len(cells) > 2 {
			industry = cleanMarkdownInline(cells[2])
		}
		signal := ""
		if len(cells) > 3 {
			signal = cleanMarkdownInline(cells[3])
		}
		item := strings.TrimSpace(name)
		if industry != "" {
			item += " - " + industry
		}
		if signal != "" {
			item += "；" + signal
		}
		item = truncate(item, 260)
		key := strings.ToLower(item)
		if item == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, item)
		if len(out) >= limit {
			return out
		}
	}
	if len(out) >= limit {
		return out
	}
	headingRe := regexp.MustCompile(`^#{2,5}\s*(?:潜客|方向|候选)\s*\d*[:：]?\s*(.+)$`)
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		m := headingRe.FindStringSubmatch(trimmed)
		if len(m) != 2 {
			continue
		}
		item := truncate(cleanMarkdownInline(m[1]), 260)
		key := strings.ToLower(item)
		if item == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, item)
		if len(out) >= limit {
			return out
		}
	}
	return out
}

func splitMarkdownTableRow(row string) []string {
	row = strings.TrimSpace(row)
	row = strings.Trim(row, "|")
	parts := strings.Split(row, "|")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		out = append(out, strings.TrimSpace(p))
	}
	return out
}

func cleanMarkdownInline(text string) string {
	replacer := strings.NewReplacer("**", "", "`", "", "｜", "|", "\r", " ", "\n", " ")
	text = replacer.Replace(text)
	text = regexp.MustCompile(`\s+`).ReplaceAllString(text, " ")
	return strings.TrimSpace(text)
}

func extractPlanSection(output string) string {
	lines := strings.Split(output, "\n")
	start := -1
	for i, line := range lines {
		if containsAnyText(line, []string{"验证计划", "下一轮挖掘计划", "挖掘计划"}) {
			start = i
			break
		}
	}
	if start < 0 {
		return ""
	}
	var picked []string
	for i := start + 1; i < len(lines); i++ {
		line := strings.TrimRight(lines[i], " \t\r")
		if i > start+1 && strings.HasPrefix(strings.TrimSpace(line), "---") {
			break
		}
		if i > start+1 && strings.HasPrefix(strings.TrimSpace(line), "## ") && !containsAnyText(line, []string{"验证计划", "挖掘计划"}) {
			break
		}
		picked = append(picked, line)
		if len(strings.Join(picked, "\n")) > 1800 {
			break
		}
	}
	plan := strings.TrimSpace(strings.Join(picked, "\n"))
	return truncate(plan, 1800)
}

func renderStepPrompt(template string, stepIndex int, steps []StepResult, input string) string {
	result := strings.ReplaceAll(template, "${input}", input)
	stepRefRe := regexp.MustCompile(`\$\{step(\d+)(?:\.output)?\}`)
	result = stepRefRe.ReplaceAllStringFunc(result, func(match string) string {
		groups := stepRefRe.FindStringSubmatch(match)
		if len(groups) < 2 {
			return ""
		}
		refIdx, err := strconv.Atoi(groups[1])
		if err != nil || refIdx < 0 || refIdx >= stepIndex || refIdx >= len(steps) {
			return ""
		}
		return steps[refIdx].Output
	})
	return result
}

func buildPreviousStepContext(stepIndex int, steps []StepResult) string {
	if stepIndex <= 0 || len(steps) == 0 {
		return ""
	}
	var sb strings.Builder
	for i := 0; i < stepIndex && i < len(steps); i++ {
		s := steps[i]
		if s.Status != "completed" && s.Status != "degraded" {
			continue
		}
		contextOutput, _ := splitHostFallbackDecision(s.Output)
		output := cleanPipelineUserOutput(contextOutput)
		toolContext := buildPreviousToolCallContext(s.ToolCalls)
		if output == "" && toolContext == "" {
			continue
		}
		sb.WriteString(fmt.Sprintf("### Step %d: %s", i, s.Name))
		if s.Status == "degraded" {
			sb.WriteString(" [DEGRADED]")
		}
		sb.WriteString("\n")
		if s.Status == "degraded" {
			sb.WriteString("质量状态：DEGRADED（上游结果存在缺口，必须保留并披露，不得当作完整结果。）\n")
			if reason := strings.TrimSpace(s.Error); reason != "" {
				sb.WriteString("降级原因：")
				sb.WriteString(truncate(reason, 1000))
				sb.WriteString("\n\n")
			}
		}
		if output != "" {
			sb.WriteString(truncate(output, 6000))
			sb.WriteString("\n\n")
		}
		if toolContext != "" {
			sb.WriteString("#### 工具调用证据摘录\n")
			sb.WriteString(toolContext)
			sb.WriteString("\n\n")
		}
	}
	return strings.TrimSpace(sb.String())
}

func buildPreviousToolCallContext(calls []ToolCallRecord) string {
	if len(calls) == 0 {
		return ""
	}
	evidenceTools := map[string]bool{
		"web_fetch":       true,
		"web_search":      true,
		"platform_search": true,
		"lead_research":   true,
		"lead_save":       true,
		"lead_discover":   true,
		"lead_enrich":     true,
	}
	digest := buildToolCallLeadDigest(calls)
	const maxCalls = 20
	const maxResultChars = 1200
	var sb strings.Builder
	if digest != "" {
		sb.WriteString("##### 自动候选线索摘要\n")
		sb.WriteString(digest)
		sb.WriteString("\n\n##### 原始工具结果摘录\n")
	}
	included := 0
	for _, call := range calls {
		if !evidenceTools[call.ToolName] {
			continue
		}
		result := strings.TrimSpace(call.Result)
		params := strings.TrimSpace(call.Params)
		if result == "" && params == "" {
			continue
		}
		included++
		status := "success"
		if pipelineToolCallRecordFailed(call) {
			status = "error"
		}
		fmt.Fprintf(&sb, "- %s status=%s", call.ToolName, status)
		if params != "" {
			fmt.Fprintf(&sb, " params=%s", truncate(params, 300))
		}
		if status == "error" && strings.TrimSpace(call.Error) != "" {
			fmt.Fprintf(&sb, "\n  error: %s", truncate(strings.TrimSpace(call.Error), 500))
		}
		if result != "" {
			fmt.Fprintf(&sb, "\n  result: %s", truncate(cleanPipelineUserOutput(result), maxResultChars))
		}
		sb.WriteString("\n")
		if included >= maxCalls {
			break
		}
	}
	return strings.TrimSpace(sb.String())
}

func buildToolCallLeadDigest(calls []ToolCallRecord) string {
	type searchParams struct {
		Query string `json:"query"`
		URL   string `json:"url"`
	}
	interesting := []string{
		"招标", "采购", "中标", "合同", "项目", "建设", "平台", "质检", "缺陷", "风控",
		"数据中台", "AI中台", "人工智能", "机器学习", "MLOps", "AutoML", "智能制造",
	}
	noise := []string{"排行榜", "TOP", "厂商", "横评", "深度对比", "产品介绍", "工具集", "百科"}
	const maxLeads = 12
	var leads []string
	seen := map[string]bool{}
	for _, call := range calls {
		if call.ToolName != "web_fetch" && call.ToolName != "web_search" && call.ToolName != "platform_search" {
			continue
		}
		var params searchParams
		_ = json.Unmarshal([]byte(call.Params), &params)
		entries := extractSearchResultEntries(call.Result)
		for _, entry := range entries {
			title := strings.TrimSpace(entry.Title)
			if title == "" || !containsAnyText(title, interesting) {
				continue
			}
			rank := "候选线索"
			if containsAnyText(title, noise) {
				rank = "行业/竞品参考"
			}
			key := strings.ToLower(title + "|" + entry.URL)
			if seen[key] {
				continue
			}
			seen[key] = true
			query := strings.TrimSpace(params.Query)
			source := strings.TrimSpace(entry.URL)
			if source == "" {
				source = "搜索结果摘要"
			}
			leads = append(leads, fmt.Sprintf("- [%s] 标题：%s | 来源：%s | 触发查询：%s", rank, title, source, truncate(query, 160)))
			if len(leads) >= maxLeads {
				return strings.Join(leads, "\n")
			}
		}
	}
	return strings.Join(leads, "\n")
}

type searchResultEntry struct {
	Title string
	URL   string
}

func extractSearchResultEntries(result string) []searchResultEntry {
	lines := strings.Split(result, "\n")
	itemRe := regexp.MustCompile(`^\s*\d+\.\s+(.+?)\s*$`)
	urlRe := regexp.MustCompile(`^\s*URL:\s*(.+?)\s*$`)
	var entries []searchResultEntry
	var current *searchResultEntry
	for _, line := range lines {
		if m := itemRe.FindStringSubmatch(line); len(m) == 2 {
			if current != nil {
				entries = append(entries, *current)
			}
			current = &searchResultEntry{Title: strings.TrimSpace(m[1])}
			continue
		}
		if current != nil {
			if m := urlRe.FindStringSubmatch(line); len(m) == 2 {
				current.URL = strings.TrimSpace(m[1])
			}
		}
	}
	if current != nil {
		entries = append(entries, *current)
	}
	return entries
}

func containsAnyText(text string, needles []string) bool {
	lower := strings.ToLower(text)
	for _, needle := range needles {
		if strings.Contains(lower, strings.ToLower(needle)) {
			return true
		}
	}
	return false
}

func buildStepOutputPath(exec *PipelineExecution, stepIdx int, stepName string) string {
	safeStepName := sanitizePipelineFilename(stepName)
	execID := "execution"
	if exec != nil && strings.TrimSpace(exec.ID) != "" {
		execID = sanitizePipelineFilename(exec.ID)
	}
	return filepath.Join(".openkitty", "storage", fmt.Sprintf("%s_step%d_%s.txt", execID, stepIdx, safeStepName))
}

func buildPreviousStepFileList(exec *PipelineExecution, stepIndex int) string {
	if exec == nil || stepIndex <= 0 {
		return ""
	}
	var lines []string
	for i := 0; i < stepIndex && i < len(exec.Steps); i++ {
		if exec.Steps[i].Status != "completed" || strings.TrimSpace(exec.Steps[i].Output) == "" {
			continue
		}
		lines = append(lines, fmt.Sprintf("- Step %d %s: %s", i, exec.Steps[i].Name, buildStepOutputPath(exec, i, exec.Steps[i].Name)))
	}
	return strings.Join(lines, "\n")
}

func sanitizePipelineFilename(name string) string {
	name = strings.Map(func(r rune) rune {
		if strings.ContainsRune(`/\:*?"<>|`, r) {
			return '_'
		}
		return r
	}, name)
	name = strings.TrimSpace(name)
	if name == "" {
		return "step"
	}
	return name
}

const (
	pipelineStopMarker         = "<!--PIPELINE_STOP-->"
	pipelineWaitingInputMarker = "INPUT_STATUS: MISSING"
)

func stripPipelineControlMarkers(output string) string {
	output = strings.ReplaceAll(output, pipelineStopMarker, "")
	output = strings.ReplaceAll(output, pipelineWaitingInputMarker, "")
	output = strings.ReplaceAll(output, pipelineSlotFilledMarker, "")
	output = strings.ReplaceAll(output, pipelineSlotMissingMarker, "")
	return strings.TrimSpace(output)
}

func pipelineOutputWaitsForInput(output string) bool {
	return strings.Contains(output, pipelineWaitingInputMarker) && hasStandaloneFinalPipelineStopMarker(output)
}

func pipelineControlStopReason(output string) (string, string) {
	if pipelineOutputWaitsForInput(output) {
		return "waiting_input", "execution stopped for decisive missing input; a new explicit @ invocation must restart from step 1"
	}
	return "control_stop", "step output requested normal pipeline stop"
}

func hasStandaloneFinalPipelineStopMarker(output string) bool {
	lines := strings.Split(strings.ReplaceAll(output, "\r\n", "\n"), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		return line == pipelineStopMarker
	}
	return false
}

// ====================== Pipeline Templates ======================

// PipelineTemplate is a pre-built multi-step pipeline for common workflows.
type PipelineTemplate struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Steps       []PipelineStep `json:"steps"`
}

// GetPipelineTemplates returns all built-in pipeline templates.
func GetPipelineTemplates() map[string]PipelineTemplate {
	return map[string]PipelineTemplate{
		"ticket_resolution": {
			Name: "ticket_resolution", Description: "工单处理：问题分类→方案检索→升级审批",
			Steps: []PipelineStep{
				{Name: "classify", Prompt: "将客户工单分类为：技术问题/账户问题/账单问题/功能需求/投诉。判断紧急程度和优先级。", Tools: []string{"rag_search"}, MaxIter: 3},
				{Name: "resolve", Prompt: "根据问题类型，使用 rag_search 检索知识库找到解决方案。给出具体操作步骤和话术。", Tools: []string{"rag_search"}, MaxIter: 5},
				{Name: "escalate", Prompt: "判断是否需要人工介入。如需升级，说明升级原因和建议处理人。", NoTools: true, MaxIter: 2, RequireApproval: true},
			},
		},
		"contract_review": {
			Name: "contract_review", Description: "合同审查：条款提取→风险检查→报告生成",
			Steps: []PipelineStep{
				{Name: "extract", Prompt: "提取合同关键条款：签约方、金额、付款条件、违约责任、知识产权、保密条款、终止条件。", Tools: []string{"rag_search", "read_file"}, MaxIter: 5},
				{Name: "check", Prompt: "逐条审查：1)付款条件是否公平 2)违约责任是否对等 3)知识产权归属是否清晰 4)是否有隐藏陷阱。标注风险等级。", Tools: []string{"rag_search"}, MaxIter: 5},
				{Name: "report", Prompt: "生成审查报告：风险矩阵(高/中/低)、修改建议、谈判要点。保存到 storage/ 目录。", Tools: []string{"write_file"}, MaxIter: 3},
				{Name: "approve", Prompt: "最终审核：确认报告的准确性、完整性，通过/驳回/修改意见。", NoTools: true, MaxIter: 1, RequireApproval: true},
			},
		},
		"daily_report": {
			Name: "daily_report", Description: "日报生成：数据采集→分析统计→格式化输出",
			Steps: []PipelineStep{
				{Name: "collect", Prompt: "从 storage/ 目录和系统日志中采集当日数据。使用 persistent_shell 运行 Python 统计数据量和变化。", Tools: []string{"read_file", "persistent_shell"}, MaxIter: 5},
				{Name: "analyze", Prompt: "统计分析数据趋势，提取 Top-N 关键指标。使用 persistent_shell 运行 Python matplotlib 生成趋势图表到 storage/charts/。", Tools: []string{"read_file", "write_file", "persistent_shell"}, MaxIter: 8},
				{Name: "format", Prompt: "格式化为 Markdown 日报：1)摘要 2)关键指标 3)图表展示(引用存储路径) 4)异常标注 5)明日计划。保存到 storage/daily_report.md。", Tools: []string{"write_file"}, MaxIter: 3},
				{Name: "review", Prompt: "审核日报内容的准确性和完整性，确认后发布或返回修改。", NoTools: true, MaxIter: 1, RequireApproval: true},
			},
		},
		"incident_response": {
			Name: "incident_response", Description: "事件响应：分级→诊断→修复→验证",
			Steps: []PipelineStep{
				{Name: "triage", Prompt: "对事件进行分级：P0(全站宕机)/P1(核心功能异常)/P2(部分用户受影响)/P3(轻微问题)。使用 rag_search 检索历史类似事件。", Tools: []string{"rag_search"}, MaxIter: 5},
				{Name: "diagnose", Prompt: "根据事件级别进行根因分析。P0/P1 级别需立即排查。使用 persistent_shell 检查系统状态和日志。", Tools: []string{"rag_search", "persistent_shell"}, MaxIter: 8},
				{Name: "remediate", Prompt: "制定并执行修复方案。P0/P1 优先恢复服务再修根因。记录所有操作步骤。", Tools: []string{"persistent_shell"}, MaxIter: 5},
				{Name: "verify", Prompt: "验证修复效果：确认服务恢复正常，监控指标回到基线，无次生问题。", Tools: []string{"persistent_shell"}, MaxIter: 5},
			},
		},
		"purchase_approval": {
			Name: "purchase_approval", Description: "采购审批：申请录入→主管审批→财务审批",
			Steps: []PipelineStep{
				{Name: "intake", Prompt: "录入采购申请：物品名称、数量、单价、总金额、用途说明、供应商信息。金额<5000走快速通道，>=5000走完整审批。", NoTools: true, MaxIter: 3},
				{Name: "supervisor", Prompt: "主管审批：审核采购必要性、预算匹配、性价比。通过/驳回/要求补充信息。", NoTools: true, MaxIter: 1, RequireApproval: true, EscalationAfter: "24h", EscalateTo: "manager"},
				{Name: "finance", Prompt: "金额>=5000需财务复核：预算余额、采购合规性、供应商资质。通过/驳回。", NoTools: true, MaxIter: 1, RequireApproval: true, EscalationAfter: "48h", EscalateTo: "cfo"},
			},
		},
		"kb_maintenance": {
			Name: "kb_maintenance", Description: "知识库维护：过期扫描→内容调研→FAQ起草→人工审核",
			Steps: []PipelineStep{
				{Name: "scan", Prompt: "扫描最近7天未更新的知识条目。识别过时的 FAQ 和 chunk 内容。列出 Top-10 需要更新的条目。", Tools: []string{"rag_search"}, MaxIter: 3},
				{Name: "research", Prompt: "针对过时内容，使用 web_fetch 搜索最新信息。对比新旧内容，标注差异和更新要点。", Tools: []string{"web_fetch"}, MaxIter: 10},
				{Name: "draft", Prompt: "起草新的 FAQ 条目。每条格式：问题+答案。输出为 JSON 数组 [{question, answer}]。", NoTools: true, MaxIter: 5},
				{Name: "review", Prompt: "审核新起草的 FAQ 条目。检查准确性、完整性、可读性。通过/修改/驳回。", NoTools: true, MaxIter: 1, RequireApproval: true},
			},
		},
		"customer_followup": {
			Name: "customer_followup", Description: "客户跟进：历史回顾→跟进内容撰写→发送审批",
			Steps: []PipelineStep{
				{Name: "review_history", Prompt: "回顾客户历史互动记录：最近沟通内容、上次跟进时间、待解决问题、客户意向等级。", Tools: []string{"rag_search"}, MaxIter: 5},
				{Name: "compose", Prompt: "撰写跟进内容：问候语、上次问题进展、本次沟通要点、下一步行动建议。语气专业热情。", NoTools: true, MaxIter: 3},
				{Name: "send", Prompt: "审核跟进内容。确认后发送：邮件/短信/微信等方式。记录发送时间和内容到 CRM。", NoTools: true, MaxIter: 1, RequireApproval: true},
			},
		},
		"hipaa_compliance": {
			Name: "hipaa_compliance", Description: "HIPAA合规处理：PII脱敏→本地模型初筛→云端增强→结果回填",
			Steps: []PipelineStep{
				{Name: "redact", Prompt: "使用 redact_pii 工具对输入文本进行PII脱敏处理。识别并遮盖：身份证号、电话号码、邮箱、银行卡号、车牌号。输出脱敏后的文本。", Tools: []string{"redact_pii"}, MaxIter: 3},
				{Name: "local_screen", Prompt: "使用本地模型(Ollama)对脱敏文本进行初步分析。识别关键医疗实体：疾病名称、药品名称、检查项目、手术操作。输出结构化JSON。", Tools: []string{"read_file"}, MaxIter: 8},
				{Name: "cloud_enhance", Prompt: "将本地模型初筛结果发送云端模型进行深度分析。注意：此处输入已经过脱敏处理，不包含任何PII。生成完整分析报告。", Tools: []string{"web_fetch", "rag_search"}, MaxIter: 10},
				{Name: "restore", Prompt: "将云端分析结果回填原始PII信息。使用 redact_pii extract模式获取原始PII映射，恢复报告中的人名、联系方式等。输出最终报告到 storage/。", Tools: []string{"redact_pii", "write_file"}, MaxIter: 5},
				{Name: "approve", Prompt: "合规审核：确认PII处理是否完整、恢复是否正确、报告是否符合HIPAA标准。通过/驳回。", NoTools: true, MaxIter: 1, RequireApproval: true},
			},
		},
	}
}

// GetApprovalTemplates returns built-in approval templates.
func GetApprovalTemplates() map[string]ApprovalTemplate {
	return map[string]ApprovalTemplate{
		"leave_request": {
			Name: "leave_request", Title: "请假申请",
			Fields: []ApprovalField{
				{Key: "leave_type", Label: "请假类型", Type: "select", Required: true, Options: []string{"年假", "病假", "事假", "调休"}},
				{Key: "start_date", Label: "开始日期", Type: "date", Required: true},
				{Key: "end_date", Label: "结束日期", Type: "date", Required: true},
				{Key: "reason", Label: "请假原因", Type: "string", Required: true},
			},
		},
		"expense_report": {
			Name: "expense_report", Title: "报销申请",
			Fields: []ApprovalField{
				{Key: "amount", Label: "金额", Type: "number", Required: true},
				{Key: "category", Label: "类别", Type: "select", Required: true, Options: []string{"差旅", "办公用品", "餐饮", "其他"}},
				{Key: "description", Label: "说明", Type: "string", Required: true},
				{Key: "receipt", Label: "票据", Type: "string", Required: false},
			},
		},
		"purchase_order": {
			Name: "purchase_order", Title: "采购申请",
			Fields: []ApprovalField{
				{Key: "amount", Label: "金额", Type: "number", Required: true},
				{Key: "vendor", Label: "供应商", Type: "string", Required: true},
				{Key: "item", Label: "采购物品", Type: "string", Required: true},
				{Key: "urgency", Label: "紧急程度", Type: "select", Required: true, Options: []string{"低", "中", "高"}},
			},
		},
	}
}

type StepRoute = pipelinecore.StepRoute

// StepRepeat configures automatic retry/loop for a step.
type StepRepeat = pipelinecore.StepRepeat

// RunMode controls how the pipeline handles HITL steps.
type RunMode string

const (
	RunModeInteractive RunMode = "interactive" // Web UI: goroutine blocks waiting for user
	RunModeAPI         RunMode = "api"         // External trigger: persist & release on HITL
)

// ====================== Pipeline Execution State ======================

type PipelineExecution struct {
	ID                string                    `json:"id"`
	PipelineName      string                    `json:"pipeline_name"`
	ResolvedWorkspace string                    `json:"resolved_workspace,omitempty"`
	CurrentStep       int                       `json:"current_step"`
	Status            string                    `json:"status"` // running, pausing, paused, waiting_approval, completed, degraded, failed, stopped
	Steps             []StepResult              `json:"steps"`
	TaskID            string                    `json:"task_id,omitempty"`
	Mode              RunMode                   `json:"mode"`
	NotifyURL         string                    `json:"notify_url,omitempty"` // webhook for API mode
	Input             string                    `json:"input,omitempty"`
	SessionID         string                    `json:"session_id,omitempty"`
	AuditLog          []ApprovalRecord          `json:"audit_log,omitempty"` // 审批留痕
	CreatedAt         time.Time                 `json:"created_at"`
	CompletionReason  *PipelineCompletionReason `json:"completion_reason,omitempty"`
	ReworkCounts      map[string]int            `json:"rework_counts,omitempty"`
	PendingRework     *PipelineReworkRequest    `json:"pending_rework,omitempty"`
	ReworkHistory     []PipelineReworkRecord    `json:"rework_history,omitempty"`
	Principal         ExecutionPrincipal        `json:"principal,omitempty"`
	// RuntimePipeline persists per-run host additions such as the universal
	// AI-employee intake step, so HITL resume keeps identical step indices and
	// permissions even after a process restart.
	RuntimePipeline  *Pipeline           `json:"runtime_pipeline,omitempty"`
	ResumeFromExecID string              `json:"resume_from_exec_id,omitempty"`
	Checkpoint       *PipelineCheckpoint `json:"checkpoint,omitempty"`

	approveCh  chan string // interactive mode only
	cancelFunc context.CancelFunc
	mu         sync.Mutex
	persistMu  sync.Mutex // serializes sidecar + DB commits for this execution
	running    bool       // true while goroutine is active
	toolCache  map[string]string
	// parallelToolCalls keeps branch-local audit slices for contract validation.
	// The same calls are also aggregated into the parent StepResult for persisted
	// observability, but one branch must never satisfy another branch's contract.
	parallelToolCalls map[int][]ToolCallRecord
	// Pipeline-owned database profiles and lazy tools never escape this run.
	databaseConnections     map[string]PipelineDatabaseConnection
	stepDatabaseConnections map[int]string
	databaseTools           map[string]*PipelineDBQueryTool
	databaseMu              sync.Mutex
	// presentationStates preserves the verified production checkpoint across
	// bounded model/contract repair calls for the same pipeline step. It is
	// deliberately run-local and is never serialized or shared across tasks.
	presentationStates map[int]*presentationProductionState
}

func (exec *PipelineExecution) presentationStateForStep(ctx context.Context, stepIndex int, input string) *presentationProductionState {
	if exec == nil {
		return newPresentationProductionState(ctx, input)
	}
	exec.mu.Lock()
	if state := exec.presentationStates[stepIndex]; state != nil {
		exec.mu.Unlock()
		return state
	}
	strictPPTPolishVisual := exec.PipelineName == "ppt-polish" &&
		stepIndex >= 0 && stepIndex < len(exec.Steps) &&
		strings.TrimSpace(exec.Steps[stepIndex].Name) == "视觉润色"
	exec.mu.Unlock()

	candidate := newPresentationProductionState(ctx, input)
	if strictPPTPolishVisual {
		// The visual-production stage itself promises a real PPTX plus render and
		// per-slide visual QA. Enforce that contract even when a user's wording is
		// concise and does not happen to contain one of the strict-QA phrases.
		candidate.Expected = true
		candidate.IntakeRequired = false
		candidate.StrictQA = true
		candidate.PPTPolishContract = true
		candidate.ContractDeckPath = pptPolishCurrentRunOutputRelativePath(exec)
		candidate.ContractWorkDir = pptPolishCurrentRunWorkRelativePath(exec)
		candidate.ContractPreviewDir = pptPolishCurrentRunPreviewRelativePath(exec)
		if body, title := presentationRequestedAuditFontFloors(exec.Input); body > 0 || title > 0 {
			if body > 0 {
				candidate.RequiredBodyFontSize = body
			}
			if title > 0 {
				candidate.RequiredTitleFontSize = title
			}
		}
		candidate.PPTPolishExecution = exec
		candidate.PPTPolishRequest = exec.Input
	} else if exec.PipelineName == "ppt-polish" {
		// The visual-production step owns the strict deck checkpoint. Delivery
		// and content steps must consume that checkpoint through pipeline
		// contracts rather than opening a second independent presentation gate.
		candidate.StrictQA = false
	}
	exec.mu.Lock()
	defer exec.mu.Unlock()
	if exec.presentationStates == nil {
		exec.presentationStates = make(map[int]*presentationProductionState)
	}
	if state := exec.presentationStates[stepIndex]; state != nil {
		return state
	}
	exec.presentationStates[stepIndex] = candidate
	return candidate
}

// resetPresentationProductionEvidenceForStep preserves the selected Skill,
// scaffold and task-local builder while invalidating evidence tied to the
// previous render. Content repairs may rebuild once; a freshness-only repair
// preserves the exact deck bytes and permits only re-audit/render/vision.
func (exec *PipelineExecution) resetPresentationProductionEvidenceForStep(stepIndex int, reason string) {
	if exec == nil {
		return
	}
	exec.mu.Lock()
	state := exec.presentationStates[stepIndex]
	exec.mu.Unlock()
	if state == nil {
		return
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	mode := pptPolishReworkMode(reason)
	preserveDeck := mode == "freshness" && state.DeckCreated && strings.TrimSpace(state.DeckPath) != ""
	preservedDeckPath := state.DeckPath
	preservedSlideCount := state.DeckSlideCount
	state.BoundedRevision = true
	state.RevisionMode = mode
	state.RevisionBuildCount = 0
	state.RevisionMutationCount = 0
	state.RevisionBuildLimit = 1
	if mode == "freshness" {
		state.RevisionBuildLimit = 0
	}
	state.DeckCreated = preserveDeck
	state.DeckPath = ""
	state.DeckSlideCount = 0
	if preserveDeck {
		state.DeckPath = preservedDeckPath
		state.DeckSlideCount = preservedSlideCount
	}
	state.AuditPassed = false
	state.AuditEvidence = ""
	state.AuditFailed = false
	state.AuditFailureMutationCount = 0
	state.SourceDirty = false
	state.Rendered = false
	state.RenderEvidence = ""
	state.RenderTarget = ""
	state.RenderedSlideCount = 0
	state.VisualInspectedImages = make(map[string]bool)
	state.FinalVisualInspected = false
	state.VisualCoverageComplete = false
	state.VisualEvidence = ""
	state.VisualDefectsDetected = false
	state.VisualDefectEvidence = ""
	state.NativeQAAttempted = false
	state.SelfAuditFailed = false
	state.SelfAuditEvidence = ""
	state.CandidatePaths = make(map[string]struct{})
	if preserveDeck {
		state.CandidatePaths[preservedDeckPath] = struct{}{}
	}
}

func (exec *PipelineExecution) closeDatabaseTools() {
	if exec == nil {
		return
	}
	exec.databaseMu.Lock()
	tools := make([]*PipelineDBQueryTool, 0, len(exec.databaseTools))
	for _, tool := range exec.databaseTools {
		tools = append(tools, tool)
	}
	exec.databaseTools = nil
	exec.databaseMu.Unlock()
	for _, tool := range tools {
		_ = tool.Close()
	}
}

type PipelineCompletionReason struct {
	Code      string `json:"code"`
	StepIndex int    `json:"step_index,omitempty"`
	StepName  string `json:"step_name,omitempty"`
	Condition string `json:"condition,omitempty"`
	Target    string `json:"target,omitempty"`
	Message   string `json:"message,omitempty"`
}

// Parallel audit indices live in a negative namespace so they can never be
// mistaken for a real top-level step index (including when parentIdx == 0).
// The low bits retain the branch index for diagnostics; records are currently
// aggregated on the parent StepResult because that is the persisted audit unit.
const (
	parallelAuditBranchBits = 20
	parallelAuditBranchMask = (1 << parallelAuditBranchBits) - 1
)

func parallelAuditStepIndex(parentIdx, branchIdx int) int {
	return -(((parentIdx + 1) << parallelAuditBranchBits) | (branchIdx + 1))
}

func decodeParallelAuditStepIndex(stepIdx int) (parentIdx, branchIdx int, ok bool) {
	if stepIdx >= 0 {
		return 0, 0, false
	}
	payload := -stepIdx
	parentIdx = (payload >> parallelAuditBranchBits) - 1
	branchIdx = (payload & parallelAuditBranchMask) - 1
	if parentIdx < 0 || branchIdx < 0 || parallelAuditStepIndex(parentIdx, branchIdx) != stepIdx {
		return 0, 0, false
	}
	return parentIdx, branchIdx, true
}

// recordToolCall appends a tool call to the current step's audit trail. Every
// record is normalized here so host-side fast paths and model-issued tools use
// the same status and correlation contract.
func (exec *PipelineExecution) recordToolCall(stepIdx int, record ToolCallRecord) {
	exec.mu.Lock()
	defer exec.mu.Unlock()
	if stepIdx >= 0 && stepIdx < len(exec.Steps) {
		record = exec.normalizeToolCallRecordLocked(stepIdx, len(exec.Steps[stepIdx].ToolCalls)+1, record)
		exec.Steps[stepIdx].ToolCalls = append(exec.Steps[stepIdx].ToolCalls, record)
	} else if parentIdx, _, ok := decodeParallelAuditStepIndex(stepIdx); ok {
		if parentIdx >= 0 && parentIdx < len(exec.Steps) {
			record = exec.normalizeToolCallRecordLocked(parentIdx, len(exec.Steps[parentIdx].ToolCalls)+1, record)
			exec.Steps[parentIdx].ToolCalls = append(exec.Steps[parentIdx].ToolCalls, record)
			if exec.parallelToolCalls == nil {
				exec.parallelToolCalls = make(map[int][]ToolCallRecord)
			}
			exec.parallelToolCalls[stepIdx] = append(exec.parallelToolCalls[stepIdx], record)
		}
	}
}

func (exec *PipelineExecution) normalizeToolCallRecordLocked(stepIdx, sequence int, record ToolCallRecord) ToolCallRecord {
	if record.Timestamp.IsZero() {
		record.Timestamp = time.Now()
	}
	record.ExecutionID = exec.ID
	record.StepIndex = stepIdx
	record.Attempt = exec.Steps[stepIdx].Revision
	if record.Attempt <= 0 {
		record.Attempt = 1
	}
	if record.CallID == "" {
		record.CallID = fmt.Sprintf("%s:s%d:a%d:c%d", exec.ID, stepIdx, record.Attempt, sequence)
	}

	if pipelineToolCallRecordFailed(record) {
		record.Status = "error"
		if strings.TrimSpace(record.Error) == "" {
			record.Error = truncate(strings.TrimSpace(record.Result), 1000)
		} else {
			record.Error = truncate(strings.TrimSpace(record.Error), 1000)
		}
	} else {
		record.Status = "success"
		record.Error = ""
	}
	return record
}

func (exec *PipelineExecution) parallelContractExecutionView(auditIdx int) *PipelineExecution {
	if exec == nil {
		return nil
	}
	exec.mu.Lock()
	defer exec.mu.Unlock()

	view := &PipelineExecution{
		ID:           exec.ID,
		PipelineName: exec.PipelineName,
		CurrentStep:  exec.CurrentStep,
		Status:       exec.Status,
		Input:        exec.Input,
		SessionID:    exec.SessionID,
		CreatedAt:    exec.CreatedAt,
		Steps:        make([]StepResult, len(exec.Steps)),
	}
	for i := range exec.Steps {
		view.Steps[i] = exec.Steps[i]
		view.Steps[i].ToolCalls = nil
		if exec.Steps[i].SourceSnapshot != nil {
			copy := *exec.Steps[i].SourceSnapshot
			view.Steps[i].SourceSnapshot = &copy
		}
	}
	if parentIdx, _, ok := decodeParallelAuditStepIndex(auditIdx); ok && parentIdx >= 0 && parentIdx < len(view.Steps) {
		view.Steps[parentIdx].ToolCalls = append([]ToolCallRecord(nil), exec.parallelToolCalls[auditIdx]...)
	}
	return view
}

// recordApproval appends an approval event to the audit log.
func (exec *PipelineExecution) recordApproval(role, action, feedback string) {
	exec.mu.Lock()
	exec.AuditLog = append(exec.AuditLog, ApprovalRecord{
		Timestamp: time.Now(),
		Role:      role,
		Action:    action,
		Feedback:  feedback,
	})
	exec.mu.Unlock()
}

func (exec *PipelineExecution) getCachedToolResult(key string) (string, bool) {
	exec.mu.Lock()
	defer exec.mu.Unlock()
	if exec.toolCache == nil {
		return "", false
	}
	result, ok := exec.toolCache[key]
	return result, ok
}

func (exec *PipelineExecution) setCachedToolResult(key, result string) {
	exec.mu.Lock()
	if exec.toolCache == nil {
		exec.toolCache = make(map[string]string)
	}
	exec.toolCache[key] = result
	exec.mu.Unlock()
}

func shouldCacheWebFetchResult(result string) bool {
	if result == "" {
		return false
	}
	if ToolResultLooksFailedForTool("web_fetch", result) || ToolResultContainsAny(result,
		"error fetching",
		"access denied",
		"forbidden",
		"captcha",
		"安全验证",
	) {
		return false
	}
	return true
}

type StepResult struct {
	Name           string                  `json:"name"`
	SlotID         string                  `json:"slot_id,omitempty"`
	SlotStatus     string                  `json:"slot_status,omitempty"`
	SlotValidation string                  `json:"slot_validation,omitempty"`
	ReusedFromExec string                  `json:"reused_from_exec,omitempty"`
	SlotFill       *PipelineSlotFillRecord `json:"slot_fill,omitempty"`
	Output         string                  `json:"output,omitempty"`
	Status         string                  `json:"status"`
	DurationMs     int64                   `json:"duration_ms"` // pending, running, waiting_approval, completed, degraded, failed
	Error          string                  `json:"error,omitempty"`
	ToolCalls      []ToolCallRecord        `json:"tool_calls,omitempty"` // 审计轨迹：每次工具调用
	SourceSnapshot *PipelineSourceSnapshot `json:"source_snapshot,omitempty"`
	Revision       int                     `json:"revision,omitempty"`
	Revisions      []PipelineStepRevision  `json:"revisions,omitempty"`
}

func (exec *PipelineExecution) FinalOutput() string {
	output, _ := exec.FinalOutputWithStep()
	return output
}

func (exec *PipelineExecution) FinalOutputWithStep() (string, string) {
	if exec == nil {
		return "", ""
	}
	exec.mu.Lock()
	steps := append([]StepResult(nil), exec.Steps...)
	exec.mu.Unlock()
	return pipelineFinalOutputFromSteps(steps)
}

func pipelineFinalOutputFromSteps(steps []StepResult) (string, string) {
	for i := len(steps) - 1; i >= 0; i-- {
		s := steps[i]
		if s.Status != "completed" && s.Status != "degraded" {
			continue
		}
		output := cleanPipelineUserOutput(s.Output)
		if output != "" && !isProgressOnlyFinalOutput(output) {
			return output, s.Name
		}
	}
	return "", ""
}

func (exec *PipelineExecution) progressSnapshot() (string, []StepResult) {
	if exec == nil {
		return "", nil
	}
	exec.mu.Lock()
	defer exec.mu.Unlock()
	steps := append([]StepResult(nil), exec.Steps...)
	for index := range steps {
		steps[index].ToolCalls = append([]ToolCallRecord(nil), exec.Steps[index].ToolCalls...)
	}
	return exec.Status, steps
}

type PipelineExecutionView struct {
	*PipelineExecution
	EmployeeID    string             `json:"employee_id,omitempty"`
	EmployeeName  string             `json:"employee_name,omitempty"`
	EmployeeNote  string             `json:"employee_note,omitempty"`
	FinalOutput   string             `json:"final_output,omitempty"`
	FinalStepName string             `json:"final_step_name,omitempty"`
	Artifacts     []PipelineArtifact `json:"artifacts,omitempty"`
	Delivery      *PipelineDelivery  `json:"delivery,omitempty"`
}

type PipelineArtifact struct {
	Path        string `json:"path"`
	Size        int64  `json:"size,omitempty"`
	DownloadURL string `json:"download_url"`
	SourceStep  string `json:"source_step,omitempty"`
	Validated   bool   `json:"validated,omitempty"`
}

type PipelineDelivery struct {
	Status           string                    `json:"status"`
	FinalStepName    string                    `json:"final_step_name,omitempty"`
	FinalOutput      string                    `json:"final_output,omitempty"`
	Artifacts        []PipelineArtifact        `json:"artifacts,omitempty"`
	TotalSteps       int                       `json:"total_steps"`
	CompletedSteps   int                       `json:"completed_steps"`
	FailedSteps      int                       `json:"failed_steps"`
	RunningSteps     int                       `json:"running_steps"`
	WaitingSteps     int                       `json:"waiting_steps"`
	ToolCalls        int                       `json:"tool_calls"`
	CachedToolCalls  int                       `json:"cached_tool_calls"`
	EstimatedCredits int                       `json:"estimated_credits"`
	CreditBreakdown  []string                  `json:"credit_breakdown,omitempty"`
	Evidence         *PipelineEvidence         `json:"evidence,omitempty"`
	DurationMs       int64                     `json:"duration_ms"`
	RiskLevel        string                    `json:"risk_level"`
	Risks            []string                  `json:"risks,omitempty"`
	NextActions      []string                  `json:"next_actions,omitempty"`
	Diagnosis        *PipelineDiagnosis        `json:"diagnosis,omitempty"`
	CompletionReason *PipelineCompletionReason `json:"completion_reason,omitempty"`
}

type PipelineDiagnosis struct {
	Status           string                 `json:"status"`
	Summary          string                 `json:"summary"`
	FailedStep       string                 `json:"failed_step,omitempty"`
	Causes           []PipelineFailureCause `json:"causes,omitempty"`
	ArtifactIssues   []string               `json:"artifact_issues,omitempty"`
	Recommendations  []string               `json:"recommendations,omitempty"`
	IterationLimited bool                   `json:"iteration_limited,omitempty"`
}

type PipelineFailureCause struct {
	Code     string `json:"code"`
	Severity string `json:"severity"`
	Step     string `json:"step,omitempty"`
	Message  string `json:"message"`
	Evidence string `json:"evidence,omitempty"`
}

type PipelineEvidence struct {
	Level              string                   `json:"level"`
	SourceCount        int                      `json:"source_count"`
	PrimarySourceCount int                      `json:"primary_source_count"`
	SearchCalls        int                      `json:"search_calls"`
	FetchCalls         int                      `json:"fetch_calls"`
	VerifiedMentions   int                      `json:"verified_mentions"`
	InferredMentions   int                      `json:"inferred_mentions"`
	UnverifiedMentions int                      `json:"unverified_mentions"`
	Sources            []PipelineEvidenceSource `json:"sources,omitempty"`
	Warnings           []string                 `json:"warnings,omitempty"`
}

type PipelineEvidenceSource struct {
	URL      string `json:"url"`
	Title    string `json:"title,omitempty"`
	StepName string `json:"step_name,omitempty"`
	ToolName string `json:"tool_name,omitempty"`
	Type     string `json:"type,omitempty"`
}

func NewPipelineExecutionView(exec *PipelineExecution) *PipelineExecutionView {
	out, stepName := exec.FinalOutputWithStep()
	artifacts := extractPipelineArtifacts(exec)
	return &PipelineExecutionView{
		PipelineExecution: exec,
		FinalOutput:       out,
		FinalStepName:     stepName,
		Artifacts:         artifacts,
		Delivery:          buildPipelineDelivery(exec, out, stepName, artifacts),
	}
}

func buildPipelineDelivery(exec *PipelineExecution, finalOutput, finalStepName string, artifacts []PipelineArtifact) *PipelineDelivery {
	if exec == nil {
		return nil
	}
	exec.mu.Lock()
	status := exec.Status
	steps := append([]StepResult(nil), exec.Steps...)
	createdAt := exec.CreatedAt
	completionReason := exec.CompletionReason
	exec.mu.Unlock()

	d := &PipelineDelivery{
		Status:           status,
		FinalStepName:    finalStepName,
		FinalOutput:      finalOutput,
		Artifacts:        append([]PipelineArtifact(nil), artifacts...),
		TotalSteps:       len(steps),
		RiskLevel:        "low",
		CompletionReason: completionReason,
	}

	for _, step := range steps {
		switch step.Status {
		case "completed":
			d.CompletedSteps++
		case "degraded":
			d.CompletedSteps++
			d.Risks = append(d.Risks, fmt.Sprintf("%s completed with warnings: %s", step.Name, truncate(step.Error, 160)))
		case "failed", "failed_fallback":
			d.FailedSteps++
			if step.Error != "" {
				d.Risks = append(d.Risks, fmt.Sprintf("%s failed: %s", step.Name, truncate(step.Error, 160)))
			} else {
				d.Risks = append(d.Risks, fmt.Sprintf("%s failed", step.Name))
			}
		case "running":
			d.RunningSteps++
		case "waiting_approval":
			d.WaitingSteps++
		}
		d.DurationMs += step.DurationMs
		for _, tc := range step.ToolCalls {
			d.ToolCalls++
			if tc.Cached {
				d.CachedToolCalls++
			}
		}
		if strings.Contains(step.Error, "iteration_budget_degraded") {
			d.Risks = append(d.Risks, fmt.Sprintf("%s completed with degraded iteration budget", step.Name))
		}
	}
	d.EstimatedCredits, d.CreditBreakdown = estimatePipelineCredits(steps, artifacts)
	d.Evidence = buildPipelineEvidence(exec.PipelineName, steps, finalOutput)
	d.Diagnosis = buildPipelineDiagnosis(status, steps, finalOutput, artifacts, completionReason, exec.ResolvedWorkspace)
	if d.Evidence != nil && len(d.Evidence.Warnings) > 0 {
		d.Risks = append(d.Risks, d.Evidence.Warnings...)
	}
	if d.Diagnosis != nil && len(d.Diagnosis.Causes) > 0 {
		d.Risks = append(d.Risks, d.Diagnosis.Summary)
	}

	if d.DurationMs == 0 && !createdAt.IsZero() && status != "pending" {
		d.DurationMs = time.Since(createdAt).Milliseconds()
	}
	if status == "failed" || d.FailedSteps > 0 {
		d.RiskLevel = "high"
	} else if status == "degraded" || status == "waiting_approval" || d.WaitingSteps > 0 || len(d.Risks) > 0 {
		d.RiskLevel = "medium"
	}
	if status == "completed" && strings.TrimSpace(finalOutput) == "" {
		d.RiskLevel = "medium"
		d.Risks = append(d.Risks, "pipeline completed without a user-facing final output")
	}
	if d.Diagnosis != nil {
		for _, cause := range d.Diagnosis.Causes {
			if cause.Code == "incomplete_completed" {
				d.RiskLevel = "high"
				break
			}
		}
	}

	switch status {
	case "completed", "degraded":
		if len(artifacts) > 0 {
			d.NextActions = append(d.NextActions, "Review or download the generated artifacts.")
		}
		if strings.TrimSpace(finalOutput) != "" {
			d.NextActions = append(d.NextActions, "Review the final answer before sharing it externally.")
		}
	case "waiting_approval":
		d.NextActions = append(d.NextActions, "Approve the waiting step or send feedback to rerun it.")
	case "failed":
		d.NextActions = append(d.NextActions, "Open the failed step output and pipeline log, then rerun after fixing the blocker.")
	case "running":
		d.NextActions = append(d.NextActions, "Keep the execution open to monitor live progress.")
	case "stopped":
		d.NextActions = append(d.NextActions, "Resume or rerun the pipeline if the task still matters.")
	}
	if len(d.NextActions) == 0 {
		d.NextActions = append(d.NextActions, "Inspect the execution log for details.")
	}
	return d
}

func buildPipelineDiagnosis(status string, steps []StepResult, finalOutput string, artifacts []PipelineArtifact, completionReason *PipelineCompletionReason, workspace string) *PipelineDiagnosis {
	d := &PipelineDiagnosis{Status: "ok", Summary: "No execution blockers detected."}
	seenCodes := map[string]bool{}
	addCause := func(code, severity, stepName, message, evidence string) {
		if message == "" {
			return
		}
		key := code + "|" + stepName + "|" + message
		if seenCodes[key] {
			return
		}
		seenCodes[key] = true
		d.Causes = append(d.Causes, PipelineFailureCause{
			Code:     code,
			Severity: severity,
			Step:     stepName,
			Message:  message,
			Evidence: truncate(strings.TrimSpace(evidence), 260),
		})
	}

	pendingSteps := incompleteStepNames(steps)
	if status == "completed" && len(pendingSteps) > 0 && completionReason == nil {
		addCause(
			"incomplete_completed",
			"high",
			"",
			fmt.Sprintf("pipeline marked completed with %d incomplete step(s): %s", len(pendingSteps), strings.Join(pendingSteps, ", ")),
			strings.Join(pendingSteps, "\n"),
		)
	}

	for _, step := range steps {
		text := strings.TrimSpace(step.Error + "\n" + step.Output)
		if step.Status == "failed" || step.Status == "failed_fallback" || strings.TrimSpace(step.Error) != "" {
			code, message := classifyPipelineFailure(text)
			severity := "high"
			if step.Status == "degraded" {
				severity = "medium"
			}
			addCause(code, severity, step.Name, message, text)
			if d.FailedStep == "" {
				d.FailedStep = step.Name
			}
		}
		if strings.Contains(step.Error, "iteration_budget_degraded") || isIterationLimitFallbackOutput(step.Output) {
			d.IterationLimited = true
			addCause("iteration_limit", "medium", step.Name, "step reached iteration limit; output may be partial", text)
		}
		for callIndex, call := range step.ToolCalls {
			if ToolResultLooksFailedForTool(call.ToolName, call.Result) {
				recovered := false
				for laterIndex := callIndex + 1; laterIndex < len(step.ToolCalls); laterIndex++ {
					later := step.ToolCalls[laterIndex]
					if strings.EqualFold(strings.TrimSpace(later.ToolName), strings.TrimSpace(call.ToolName)) &&
						!ToolResultLooksFailedForTool(later.ToolName, later.Result) {
						recovered = true
						break
					}
				}
				if recovered {
					continue
				}
				code, message := classifyPipelineFailure(call.Result)
				addCause(code, "medium", step.Name, "tool "+call.ToolName+" failed: "+message, call.Result)
			}
		}
	}

	if strings.TrimSpace(finalOutput) == "" && (status == "completed" || status == "failed") {
		addCause("no_final_output", "medium", "", "execution has no user-facing final output", "")
	}
	for _, a := range artifacts {
		if strings.TrimSpace(a.Path) == "" {
			continue
		}
		if !artifactExistsInWorkspace(a.Path, workspace) {
			msg := "claimed artifact is missing on disk: " + a.Path
			d.ArtifactIssues = append(d.ArtifactIssues, msg)
			addCause("artifact_missing", "high", a.SourceStep, msg, a.Path)
		}
	}
	// A model's delivery claim is useful diagnostic evidence, but never enough
	// to create a download link. Keep missing claimed files visible as a risk so
	// operators can distinguish an honest no-file response from a false success.
	for _, step := range steps {
		if strings.TrimSpace(step.Name) == aiEmployeeIntakeStepName {
			continue
		}
		body := strings.TrimSpace(Clean(middlewarecore.StripToolCalls(step.Output)))
		if body == "" || outputDisclaimsArtifactDelivery(body) || !outputClaimsArtifactDelivery(body) {
			continue
		}
		for _, path := range extractArtifactPaths(body) {
			if artifactExistsInWorkspace(path, workspace) {
				continue
			}
			msg := "claimed artifact is missing on disk: " + path
			d.ArtifactIssues = append(d.ArtifactIssues, msg)
			addCause("artifact_missing", "medium", step.Name, msg, path)
		}
	}

	if len(d.Causes) > 0 {
		d.Status = "attention"
		d.Summary = summarizePipelineDiagnosis(d.Causes)
		d.Recommendations = pipelineDiagnosisRecommendations(d)
	}
	return d
}

func classifyPipelineFailure(text string) (string, string) {
	lower := strings.ToLower(text)
	switch {
	case (strings.Contains(lower, "tool") || mentionsPipelineToolName(lower)) && (strings.Contains(lower, "not found") || strings.Contains(lower, "not registered") || strings.Contains(lower, "missing") || strings.Contains(text, "不存在")):
		return "tool_missing", "configured tool is not registered"
	case strings.Contains(lower, "iteration") && strings.Contains(lower, "limit"), strings.Contains(lower, "max iterations"), strings.Contains(lower, "unable to generate summary"):
		return "iteration_limit", "model reached iteration limit before producing a complete answer"
	case strings.Contains(lower, "timeout") || strings.Contains(lower, "connection refused") || strings.Contains(lower, "failed to fetch") || strings.Contains(lower, "network"):
		return "network_failure", "network or fetch operation failed"
	case strings.Contains(lower, "401") || strings.Contains(lower, "403") || strings.Contains(lower, "api key") || strings.Contains(lower, "unauthorized") || strings.Contains(lower, "forbidden"):
		return "auth_or_permission", "authentication or permission failure"
	case strings.Contains(lower, "rate limit") || strings.Contains(lower, "429"):
		return "rate_limited", "provider or tool rate limit hit"
	case strings.Contains(lower, "model") || strings.Contains(lower, "llm") || strings.Contains(lower, "api error"):
		return "model_failure", "model/provider call failed"
	default:
		return "execution_error", "execution step failed"
	}
}

func incompleteStepNames(steps []StepResult) []string {
	var out []string
	for _, step := range steps {
		switch step.Status {
		case "", "pending", "running", "waiting_approval":
			name := strings.TrimSpace(step.Name)
			if name == "" {
				name = "unnamed step"
			}
			out = append(out, name)
		}
	}
	return out
}

func mentionsPipelineToolName(lower string) bool {
	for _, name := range []string{
		"read_file", "write_file", "edit_file", "list_dir", "grep", "glob",
		"web_search", "web_fetch", "persistent_shell", "cli",
	} {
		if strings.Contains(lower, name) {
			return true
		}
	}
	return false
}

func summarizePipelineDiagnosis(causes []PipelineFailureCause) string {
	if len(causes) == 0 {
		return "No execution blockers detected."
	}
	priority := []string{"incomplete_completed", "tool_missing", "artifact_missing", "network_failure", "auth_or_permission", "rate_limited", "iteration_limit", "model_failure", "no_final_output"}
	for _, code := range priority {
		for _, cause := range causes {
			if cause.Code == code {
				if cause.Step != "" {
					return cause.Step + ": " + cause.Message
				}
				return cause.Message
			}
		}
	}
	return causes[0].Message
}

func pipelineDiagnosisRecommendations(d *PipelineDiagnosis) []string {
	if d == nil {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	add := func(s string) {
		if s == "" || seen[s] {
			return
		}
		seen[s] = true
		out = append(out, s)
	}
	for _, cause := range d.Causes {
		switch cause.Code {
		case "tool_missing":
			add("Open tool self-check and register or remove the missing tool from this employee/pipeline.")
		case "artifact_missing":
			add("Rerun the task after fixing write_file/artifact persistence, then verify the download link.")
		case "network_failure":
			add("Retry after checking network connectivity and source accessibility.")
		case "auth_or_permission":
			add("Check API keys, provider credentials, or file access permissions.")
		case "rate_limited":
			add("Wait or switch the employee to a backup model/provider.")
		case "iteration_limit":
			add("Raise max_iter for this step or split the task into smaller steps.")
		case "no_final_output":
			add("Add a final no-tools report step or inspect the last completed step output.")
		case "incomplete_completed":
			add("Inspect routing and completion_reason; rerun after fixing the premature completion path.")
		}
	}
	return out
}

func artifactExistsInWorkspace(rel, workspace string) bool {
	rel = strings.TrimSpace(strings.ReplaceAll(rel, "\\", "/"))
	rel = strings.TrimLeft(rel, "/")
	if rel == "" {
		return false
	}
	path := filepath.FromSlash(rel)
	candidates := []string{path}
	if strings.TrimSpace(workspace) != "" && !filepath.IsAbs(path) {
		candidates = append([]string{filepath.Join(workspace, path)}, candidates...)
	}
	if strings.HasPrefix(rel, "storage/") {
		candidates = append(candidates, filepath.Join(".openkitty", filepath.FromSlash(rel)))
	}
	for _, p := range candidates {
		if info, err := os.Stat(p); err == nil && !info.IsDir() && info.Size() > 0 {
			return true
		}
	}
	return false
}

func estimatePipelineCredits(steps []StepResult, artifacts []PipelineArtifact) (int, []string) {
	startedSteps := 0
	toolCredits := 0
	cachedTools := 0
	toolCounts := map[string]int{}
	for _, step := range steps {
		if step.Status != "" && step.Status != "pending" {
			startedSteps++
		}
		for _, call := range step.ToolCalls {
			if call.Cached {
				cachedTools++
				continue
			}
			toolCounts[call.ToolName]++
			toolCredits += creditWeightForTool(call.ToolName)
		}
	}
	artifactCredits := len(artifacts)
	if artifactCredits > 5 {
		artifactCredits = 5
	}
	total := startedSteps + toolCredits + artifactCredits
	if total == 0 && (len(steps) > 0 || len(artifacts) > 0) {
		total = 1
	}

	breakdown := []string{}
	if startedSteps > 0 {
		breakdown = append(breakdown, fmt.Sprintf("%d started step(s) = %d credits", startedSteps, startedSteps))
	}
	if toolCredits > 0 {
		breakdown = append(breakdown, fmt.Sprintf("%d non-cached tool credit(s)", toolCredits))
		for _, name := range sortedToolNames(toolCounts) {
			breakdown = append(breakdown, fmt.Sprintf("%s x%d", name, toolCounts[name]))
		}
	}
	if cachedTools > 0 {
		breakdown = append(breakdown, fmt.Sprintf("%d cached tool call(s) = 0 credits", cachedTools))
	}
	if artifactCredits > 0 {
		breakdown = append(breakdown, fmt.Sprintf("%d artifact credit(s)", artifactCredits))
	}
	if len(breakdown) == 0 {
		breakdown = append(breakdown, "No billable work recorded yet.")
	}
	return total, breakdown
}

func creditWeightForTool(toolName string) int {
	name := strings.ToLower(strings.TrimSpace(toolName))
	switch name {
	case "external_agent", "sub_agent", "parallel_sub_agent", "swarm":
		return 12
	case "persistent_shell", "cli", "code_check", "test_runner":
		return 4
	case "web_search", "web_fetch", "platform_search", "rag_search":
		return 2
	case "read_file", "write_file", "edit_file", "json_patch", "multi_edit", "list_dir", "grep", "glob":
		return 1
	default:
		return 1
	}
}

func sortedToolNames(counts map[string]int) []string {
	names := make([]string, 0, len(counts))
	for name := range counts {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func buildPipelineEvidence(pipelineName string, steps []StepResult, finalOutput string) *PipelineEvidence {
	if !pipelineEvidenceRelevant(pipelineName, steps, finalOutput) {
		return nil
	}
	e := &PipelineEvidence{Level: "none"}
	seen := map[string]bool{}
	for _, step := range steps {
		for _, call := range step.ToolCalls {
			if !isEvidenceTool(call.ToolName) {
				continue
			}
			switch call.ToolName {
			case "web_search", "platform_search":
				e.SearchCalls++
			case "web_fetch", "rag_search", "lead_research":
				e.FetchCalls++
			}
			for _, source := range extractEvidenceSourcesFromToolCall(step.Name, call) {
				key := normalizeEvidenceURL(source.URL)
				if key == "" || seen[key] {
					continue
				}
				seen[key] = true
				source.URL = key
				source.Type = classifyEvidenceSourceType(key)
				if source.Type == "primary" {
					e.PrimarySourceCount++
				}
				if len(e.Sources) < 12 {
					e.Sources = append(e.Sources, source)
				}
				e.SourceCount++
			}
		}
		for _, source := range extractEvidenceSourcesFromText(step.Name, "step_output", step.Output) {
			key := normalizeEvidenceURL(source.URL)
			if key == "" || seen[key] {
				continue
			}
			seen[key] = true
			source.URL = key
			source.Type = classifyEvidenceSourceType(key)
			if source.Type == "primary" {
				e.PrimarySourceCount++
			}
			if len(e.Sources) < 12 {
				e.Sources = append(e.Sources, source)
			}
			e.SourceCount++
		}
	}
	for _, source := range extractEvidenceSourcesFromText("final output", "final_output", finalOutput) {
		key := normalizeEvidenceURL(source.URL)
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		source.URL = key
		source.Type = classifyEvidenceSourceType(key)
		if source.Type == "primary" {
			e.PrimarySourceCount++
		}
		if len(e.Sources) < 12 {
			e.Sources = append(e.Sources, source)
		}
		e.SourceCount++
	}

	allText := finalOutput
	for _, step := range steps {
		allText += "\n" + step.Output
	}
	e.VerifiedMentions = countAnyText(allText, []string{"已验证", "verified", "confirmed", "validated"})
	e.InferredMentions = countAnyText(allText, []string{"推断", "inferred", "likely", "可能"})
	e.UnverifiedMentions = countAnyText(allText, []string{"待验证", "unverified", "uncertain", "缺失", "missing evidence"})
	e.Level = evidenceLevel(e)
	e.Warnings = evidenceWarnings(e, finalOutput)
	return e
}

func pipelineEvidenceRelevant(pipelineName string, steps []StepResult, finalOutput string) bool {
	text := strings.ToLower(pipelineName + "\n" + finalOutput)
	if containsAnyText(text, []string{"research", "lead", "market", "investment", "risk", "wide-research", "evidence", "source"}) {
		return true
	}
	for _, step := range steps {
		stepText := strings.ToLower(step.Name + "\n" + step.Output)
		if containsAnyText(stepText, []string{"research", "evidence", "source", "market", "lead", "调研", "证据", "来源", "潜客", "市场", "投资", "风险"}) {
			return true
		}
		for _, call := range step.ToolCalls {
			if isEvidenceTool(call.ToolName) {
				return true
			}
		}
	}
	return false
}

func isEvidenceTool(toolName string) bool {
	switch strings.ToLower(strings.TrimSpace(toolName)) {
	case "web_fetch", "web_search", "platform_search", "rag_search", "lead_research", "lead_discover", "lead_enrich":
		return true
	default:
		return false
	}
}

func extractEvidenceSourcesFromToolCall(stepName string, call ToolCallRecord) []PipelineEvidenceSource {
	type params struct {
		URL   string `json:"url"`
		Query string `json:"query"`
	}
	var p params
	_ = json.Unmarshal([]byte(call.Params), &p)
	var out []PipelineEvidenceSource
	if p.URL != "" {
		out = append(out, PipelineEvidenceSource{URL: p.URL, StepName: stepName, ToolName: call.ToolName})
	}
	for _, entry := range extractSearchResultEntries(call.Result) {
		if entry.URL == "" {
			continue
		}
		out = append(out, PipelineEvidenceSource{
			URL:      entry.URL,
			Title:    entry.Title,
			StepName: stepName,
			ToolName: call.ToolName,
		})
	}
	out = append(out, extractEvidenceSourcesFromText(stepName, call.ToolName, call.Result)...)
	return out
}

func extractEvidenceSourcesFromText(stepName, toolName, text string) []PipelineEvidenceSource {
	if text == "" {
		return nil
	}
	re := regexp.MustCompile(`https?://[^\s<>"')\]}]+`)
	matches := re.FindAllString(text, -1)
	out := make([]PipelineEvidenceSource, 0, len(matches))
	for _, match := range matches {
		out = append(out, PipelineEvidenceSource{
			URL:      match,
			StepName: stepName,
			ToolName: toolName,
		})
	}
	return out
}

func normalizeEvidenceURL(raw string) string {
	raw = strings.TrimSpace(raw)
	raw = strings.TrimRight(raw, ".,;:!?，。；：！？")
	if raw == "" {
		return ""
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return ""
	}
	parsed.Fragment = ""
	return parsed.String()
}

func classifyEvidenceSourceType(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "secondary"
	}
	host := strings.ToLower(parsed.Hostname())
	switch {
	case strings.HasSuffix(host, ".gov") || strings.Contains(host, ".gov."):
		return "primary"
	case strings.HasSuffix(host, ".edu") || strings.Contains(host, ".edu."):
		return "primary"
	case strings.Contains(host, "sec.gov") || strings.Contains(host, "github.com") || strings.Contains(host, "arxiv.org"):
		return "primary"
	default:
		return "secondary"
	}
}

func evidenceLevel(e *PipelineEvidence) string {
	if e == nil || e.SourceCount == 0 {
		return "none"
	}
	if e.SourceCount >= 6 && (e.PrimarySourceCount > 0 || e.VerifiedMentions > 0) {
		return "strong"
	}
	if e.SourceCount >= 3 {
		return "moderate"
	}
	return "weak"
}

func evidenceWarnings(e *PipelineEvidence, finalOutput string) []string {
	if e == nil {
		return nil
	}
	var warnings []string
	if e.SourceCount == 0 {
		warnings = append(warnings, "research output has no explicit source URLs")
	}
	if e.SourceCount > 0 && e.SourceCount < 3 {
		warnings = append(warnings, "research output has fewer than 3 explicit sources")
	}
	if e.VerifiedMentions == 0 && e.InferredMentions+e.UnverifiedMentions > 0 {
		warnings = append(warnings, "output contains inferred or unverified claims without verified markers")
	}
	if e.SourceCount > 0 && !containsAnyText(finalOutput, []string{"来源", "证据", "source", "evidence", "引用"}) {
		warnings = append(warnings, "final output may not expose an evidence table to the user")
	}
	return warnings
}

func countAnyText(text string, markers []string) int {
	lower := strings.ToLower(text)
	total := 0
	for _, marker := range markers {
		total += strings.Count(lower, strings.ToLower(marker))
	}
	return total
}

func extractPipelineArtifacts(exec *PipelineExecution) []PipelineArtifact {
	if exec == nil {
		return nil
	}
	exec.mu.Lock()
	steps := append([]StepResult(nil), exec.Steps...)
	exec.mu.Unlock()

	seen := make(map[string]bool)
	var artifacts []PipelineArtifact
	for stepIndex, step := range steps {
		// Intake is a plan/decision step. It intentionally names future delivery
		// paths, but cannot create artifacts because it has no tool permission.
		if strings.TrimSpace(step.Name) == aiEmployeeIntakeStepName {
			continue
		}
		publicContractPaths, contractOwnsPublicArtifacts := pipelineStepPublicArtifactPaths(exec, stepIndex)
		isPublicArtifact := func(path string) bool {
			if !contractOwnsPublicArtifacts {
				return true
			}
			path = strings.TrimSpace(strings.ReplaceAll(path, "\\", "/"))
			return publicContractPaths[path]
		}
		for _, call := range step.ToolCalls {
			if !pipelineToolCallRecordFailed(call) {
				for _, recorded := range call.Artifacts {
					path, ok := normalizedValidatedToolArtifactPath(call.ToolName, recorded)
					if !ok || seen[path] || !isPublicArtifact(path) {
						continue
					}
					seen[path] = true
					downloadPath := "/" + path
					artifacts = append(artifacts, PipelineArtifact{
						Path:        path,
						Size:        recorded.Size,
						DownloadURL: pipelineArtifactDownloadURL(strings.TrimPrefix(downloadPath, "/")),
						SourceStep:  step.Name,
						Validated:   true,
					})
				}
			}
			if call.ToolName != "host_write_artifact" || ToolResultLooksFailedForTool(call.ToolName, call.Result) {
				continue
			}
			var params map[string]interface{}
			if json.Unmarshal([]byte(call.Params), &params) != nil {
				continue
			}
			path, _ := params["path"].(string)
			path = strings.TrimSpace(strings.ReplaceAll(path, "\\", "/"))
			if path == "" || seen[path] || !isPublicArtifact(path) {
				continue
			}
			validatedPath, size, ok := validatedPipelineContractArtifact(exec, path, 1)
			if !ok {
				continue
			}
			seen[validatedPath] = true
			artifacts = append(artifacts, PipelineArtifact{
				Path:        validatedPath,
				Size:        size,
				DownloadURL: pipelineArtifactDownloadURL(validatedPath),
				SourceStep:  step.Name,
				Validated:   true,
			})
		}
		for _, path := range extractArtifactPaths(step.Output) {
			if seen[path] || !isPublicArtifact(path) {
				continue
			}
			validatedPath, size, ok := validatedPipelineContractArtifact(exec, path, 1)
			if !ok || seen[validatedPath] {
				continue
			}
			seen[validatedPath] = true
			artifacts = append(artifacts, PipelineArtifact{
				Path:        validatedPath,
				Size:        size,
				DownloadURL: pipelineArtifactDownloadURL(validatedPath),
				SourceStep:  step.Name,
				Validated:   true,
			})
		}
	}
	// An artifact contract is host-validated proof of a current-run file even
	// when the file was produced by a renderer invoked through persistent_shell.
	// Surface those files just like write_file receipts so generic builders do
	// not have to repeat internal paths in their prose to make downloads work.
	if exec.RuntimePipeline != nil {
		for index, runtimeStep := range exec.RuntimePipeline.Steps {
			if runtimeStep.ArtifactContract == nil || index >= len(steps) {
				continue
			}
			if status := strings.ToLower(strings.TrimSpace(steps[index].Status)); status != "completed" && status != "degraded" {
				continue
			}
			for _, candidate := range pipelineArtifactContractPaths(exec, runtimeStep.ArtifactContract) {
				path, size, ok := validatedPipelineContractArtifact(exec, candidate, runtimeStep.ArtifactContract.MinBytes)
				if !ok || seen[path] {
					continue
				}
				seen[path] = true
				artifacts = append(artifacts, PipelineArtifact{
					Path: path, Size: size, DownloadURL: pipelineArtifactDownloadURL(path),
					SourceStep: steps[index].Name, Validated: true,
				})
			}
		}
	}
	return artifacts
}

func pipelineStepPublicArtifactPaths(exec *PipelineExecution, stepIndex int) (map[string]bool, bool) {
	if exec == nil || exec.RuntimePipeline == nil || stepIndex < 0 || stepIndex >= len(exec.RuntimePipeline.Steps) {
		return nil, false
	}
	contract := exec.RuntimePipeline.Steps[stepIndex].ArtifactContract
	if contract == nil || len(contract.Paths) == 0 {
		return nil, false
	}
	paths := make(map[string]bool, len(contract.Paths))
	for _, path := range pipelineArtifactContractPaths(exec, contract) {
		path = strings.TrimSpace(strings.ReplaceAll(path, "\\", "/"))
		if path != "" {
			paths[path] = true
		}
	}
	return paths, len(paths) > 0
}

func validatedPipelineContractArtifact(exec *PipelineExecution, candidate string, minBytes int) (string, int64, bool) {
	if exec == nil || strings.TrimSpace(exec.ResolvedWorkspace) == "" {
		return "", 0, false
	}
	path, err := normalizeAgentOSArtifactPath(strings.TrimSpace(strings.ReplaceAll(candidate, "\\", "/")))
	if err != nil {
		return "", 0, false
	}
	workspace, err := filepath.Abs(strings.TrimSpace(exec.ResolvedWorkspace))
	if err != nil {
		return "", 0, false
	}
	target, err := filepath.Abs(filepath.Join(workspace, filepath.FromSlash(path)))
	if err != nil || !projectctx.ContainsPath(workspace, target) || projectctx.EnsureResolvedPathInside(workspace, target) != nil {
		return "", 0, false
	}
	info, err := os.Stat(target)
	if err != nil || !info.Mode().IsRegular() {
		return "", 0, false
	}
	if minBytes <= 0 {
		minBytes = 1
	}
	if info.Size() < int64(minBytes) {
		return "", 0, false
	}
	return path, info.Size(), true
}

func pipelineArtifactDownloadURL(path string) string {
	path = strings.TrimSpace(strings.ReplaceAll(path, "\\", "/"))
	path = strings.TrimPrefix(path, "/")
	if normalized, err := normalizeAgentOSArtifactPath(path); err == nil {
		return agentOSArtifactDownloadURL(normalized)
	}
	return "/api/files/download?path=" + url.QueryEscape("/"+path)
}

func normalizedValidatedToolArtifactPath(toolName string, artifact ToolCallArtifactRecord) (string, bool) {
	if !artifact.Validated {
		return "", false
	}
	raw := strings.TrimSpace(strings.ReplaceAll(artifact.Path, "\\", "/"))
	if raw == "" || strings.HasPrefix(raw, "/") || filepath.IsAbs(filepath.FromSlash(raw)) || filepath.VolumeName(filepath.FromSlash(raw)) != "" {
		return "", false
	}
	clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(raw)))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", false
	}
	switch {
	case strings.EqualFold(strings.TrimSpace(toolName), "write_file"):
		if strings.HasPrefix(clean, ".openkitty/storage/") {
			return clean, true
		}
	case strings.HasPrefix(strings.TrimSpace(toolName), "professional_agent:"):
		if strings.HasPrefix(clean, ".openkitty/professional-agents/") {
			return clean, true
		}
	}
	return "", false
}

func extractArtifactPaths(text string) []string {
	if text == "" {
		return nil
	}
	patterns := []*regexp.Regexp{
		regexp.MustCompile("(?:^|[\\s`\"'(:：=|])((?:[A-Za-z]:)?(?:[/\\\\][^\\s`\"')<>]+)*[/\\\\]\\.openkitty[/\\\\]storage[/\\\\][^\\s`\"')<>]+)"),
		regexp.MustCompile("(?:^|[\\s`\"'(:：=|])((?:\\.[/\\\\])?(?:\\.openkitty[/\\\\]storage|storage)[/\\\\][^\\s`\"')<>]+)"),
	}
	seen := make(map[string]bool)
	var paths []string
	for _, re := range patterns {
		matches := re.FindAllStringSubmatch(text, -1)
		for _, match := range matches {
			if len(match) < 2 {
				continue
			}
			path := strings.ReplaceAll(match[1], "\\", "/")
			path = strings.TrimRight(path, ".,;:!?，。；：！？")
			if idx := strings.Index(strings.ToLower(path), ".openkitty/storage/"); idx >= 0 {
				path = path[idx:]
			}
			if strings.HasPrefix(path, "storage/") {
				path = ".openkitty/" + path
			}
			if path == "" || seen[path] {
				continue
			}
			seen[path] = true
			paths = append(paths, path)
		}
	}
	return paths
}

func cleanPipelineUserOutput(output string) string {
	output = strings.TrimSpace(stripPipelineControlMarkers(output))
	output = strings.TrimSpace(Clean(middlewarecore.StripToolCalls(output)))
	output = strings.TrimSpace(stripProcessNarrationSentences(output))
	return textnormalize.CalendarLabels(output)
}

func stripProcessNarrationSentences(output string) string {
	if output == "" {
		return ""
	}
	lines := strings.Split(strings.ReplaceAll(output, "\r\n", "\n"), "\n")
	preserveChoiceBullets := containsAnyText(strings.ToLower(output), []string{
		"选择", "选项", "回复序号",
		"choose one", "select one", "options:", "choices:",
	})
	for index, line := range lines {
		trimmed := strings.TrimSpace(line)
		if markdownOrderedListItemRe.MatchString(line) ||
			aiEmployeeLineStartsWithCircledOption(trimmed) ||
			(preserveChoiceBullets &&
				(strings.HasPrefix(trimmed, "- ") ||
					strings.HasPrefix(trimmed, "* ") ||
					strings.HasPrefix(trimmed, "+ "))) ||
			strings.HasPrefix(trimmed, "|") {
			continue
		}
		lines[index] = stripProcessNarrationFromPlainLine(line)
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func aiEmployeeLineStartsWithCircledOption(line string) bool {
	line = strings.TrimLeft(strings.TrimSpace(line), "-*+#> \t")
	for _, marker := range []string{"①", "②", "③", "④", "⑤", "⑥", "⑦", "⑧", "⑨", "⑩"} {
		if strings.HasPrefix(line, marker) {
			return true
		}
	}
	return false
}

// stripProcessNarrationFromPlainLine removes model narration from prose while
// preserving structured option, procedure, and table lines in the caller. A
// choice often describes what will happen after selection; deleting that as
// "process narration" leaves broken list markers and changes meaning.
func stripProcessNarrationFromPlainLine(line string) string {
	var b strings.Builder
	var sentence strings.Builder
	flush := func() {
		part := sentence.String()
		sentence.Reset()
		if strings.TrimSpace(part) == "" {
			b.WriteString(part)
			return
		}
		if isProcessNarrationSentence(part) {
			return
		}
		b.WriteString(part)
	}
	for _, r := range line {
		sentence.WriteRune(r)
		switch r {
		case '。', '！', '？', '.', '!', '?', '\n':
			flush()
		}
	}
	flush()
	return strings.TrimSpace(b.String())
}

func isProcessNarrationSentence(sentence string) bool {
	text := strings.TrimSpace(sentence)
	if text == "" {
		return false
	}
	plain := strings.TrimSpace(strings.TrimLeft(text, ">#*+-_` \t\"'“”‘’「」『』（()【["))
	lower := strings.ToLower(plain)
	englishMarkers := []string{
		"let me ",
		"now let me ",
		"i will ",
		"i'll ",
		"i need to ",
	}
	for _, marker := range englishMarkers {
		if !strings.HasPrefix(lower, marker) {
			continue
		}
		hasVerb := containsAnyText(lower, []string{
			"read", "inspect", "check", "search", "retrieve", "fetch", "organize", "analyse", "analyze", "summarize", "generate", "create", "write", "call", "use",
		})
		hasProcessObject := containsAnyText(lower, []string{
			"file", "document", "material", "tool", "web", "url", "link", "data", "source", "evidence", "report", "deliverable", "output", "result", "api", "directory", "path", "user",
		})
		if hasVerb && hasProcessObject {
			return true
		}
	}
	for _, marker := range []string{
		"直接给出最终交付",
		"现在给出最终交付",
		"转入最终交付",
		"检索已足够判断",
		"下面直接",
		"以下直接",
		"按用户限定，本轮只做",
		"未生成落盘文件",
		"未明确要求文件",
		"未明确要求生成文件",
		"故不写文件",
		"用户未给出的信息一律记为",
	} {
		if strings.Contains(text, marker) {
			return true
		}
	}
	if strings.Contains(text, "我已有足够") &&
		containsAnyText(text, []string{"信息", "证据", "材料", "框架", "上下文"}) {
		return true
	}

	processVerbs := []string{"确认", "读取", "检查", "搜索", "检索", "整理", "生成", "创建", "写入", "调用", "使用", "下载", "抓取", "解析", "核验", "尝试"}
	processObjects := []string{
		"用户", "文件", "材料", "工具", "网页", "网址", "链接", "数据", "来源", "证据", "报告", "交付", "文档", "表格", "幻灯片", "PPT", "输出", "结果", "接口", "API", "目录", "路径",
	}
	for _, marker := range []string{"我需要", "让我", "我先", "我将", "接下来", "正在", "准备"} {
		if !strings.HasPrefix(plain, marker) {
			continue
		}
		if containsAnyText(plain, processVerbs) && containsAnyText(plain, processObjects) {
			return true
		}
	}
	return false
}

func buildRecoverableStepFailureOutput(step *PipelineStep, err error) string {
	stepName := ""
	if step != nil {
		stepName = strings.TrimSpace(step.Name)
	}
	if stepName == "" {
		stepName = "current step"
	}
	errText := ""
	if err != nil {
		errText = err.Error()
	}
	return strings.TrimSpace(fmt.Sprintf(`## 阶段降级
步骤：%s
原因：%s

## 后续步骤使用规则
- 本阶段未形成完整结果，后续步骤只能使用已存在的上游材料和可验证事实。
- 缺失、冲突或未验证的信息必须明确标记，不得写成已验证事实。
- 不要要求用户重试；继续整理当前可交付结果，并明确说明本阶段缺口。`, stepName, errText))
}

func pipelineHasDegradedSteps(steps []StepResult) bool {
	for _, step := range steps {
		if step.Status == "degraded" || (strings.TrimSpace(step.Error) != "" && step.Status == "completed") {
			return true
		}
	}
	return false
}

func isIterationLimitFallbackOutput(output string) bool {
	text := strings.TrimSpace(output)
	if text == "" {
		return false
	}
	lower := strings.ToLower(text)
	return (strings.Contains(text, "任务达到迭代上限") && strings.Contains(text, "未能生成完整最终回答")) ||
		strings.Contains(lower, "task reached iteration limit") ||
		strings.Contains(lower, "unable to generate summary") ||
		strings.Contains(lower, "iteration budget is exhausted") ||
		strings.Contains(lower, "reached max iterations")
}

func sanitizeIterationLimitFallbackText(output string) string {
	text := strings.TrimSpace(middlewarecore.StripToolCalls(output))
	replacements := []string{
		"任务达到迭代上限，未能生成完整最终回答。", "本阶段触发执行预算保护，以下为已保留的阶段性材料。",
		"Task reached iteration limit. Unable to generate summary.", "本阶段触发执行预算保护，未能生成完整摘要。",
		"已获取到网页搜索结果，但模型未能在迭代预算内整理成报告。请重试或提高该步骤的迭代上限。", "已获取到网页搜索结果，后续步骤应基于现有材料继续整理，不要求用户重试。",
		"请重试或提高该步骤的迭代上限。", "后续步骤应基于现有材料继续整理。",
	}
	for i := 0; i+1 < len(replacements); i += 2 {
		text = strings.ReplaceAll(text, replacements[i], replacements[i+1])
	}
	return strings.TrimSpace(text)
}

func buildDegradedStepOutput(step *PipelineStep, rawOutput string) string {
	stepName := "current step"
	if step != nil && strings.TrimSpace(step.Name) != "" {
		stepName = strings.TrimSpace(step.Name)
	}
	raw := sanitizeIterationLimitFallbackText(rawOutput)
	var sb strings.Builder
	fmt.Fprintf(&sb, "## 阶段性降级交付：%s\n\n", stepName)
	sb.WriteString("本步骤已触发执行预算保护。系统已保留当前阶段能证明的材料，并允许后续步骤继续整理可交付结果。\n\n")
	sb.WriteString("## 后续步骤执行规则\n")
	sb.WriteString("- 不要要求用户重试。\n")
	sb.WriteString("- 不要重新做已经完成的搜索、读取或分析。\n")
	sb.WriteString("- 基于已有材料继续输出报告、表格、候选清单或缺口说明。\n")
	sb.WriteString("- 对证据不足的信息必须标注为待验证，不得编造成已验证事实。\n")
	if raw != "" {
		sb.WriteString("\n## 已保留阶段材料\n")
		sb.WriteString(raw)
	}
	return strings.TrimSpace(sb.String())
}

func evidenceBoundaryInstruction(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	employeeID, _ := ctx.Value("ae_employee_id").(string)
	if !requiresEvidenceBoundary(employeeID) {
		return ""
	}
	return `## 证据边界与防编造规则
- 输出具体事实、公司、法律风险、投资判断、数据结论时，必须标注证据等级：已验证 / 推断 / 待验证。
- “已验证”只能用于当前步骤、上游步骤或工具结果中出现过的文件、URL、搜索摘要、数据表或明确来源。
- 没有来源的公司、指标、法规、采购需求、财务结论不得写成事实；只能放入“推断”或“待验证”。
- 如果证据不足，仍然给出可交付结果，但必须写清证据缺口和下一步验证路径。
- 不要为了凑数量编造公司、链接、数据或引用。`
}

// ToolCallRecord captures every tool invocation for audit trail / compliance.
type ToolCallRecord struct {
	CallID      string                   `json:"call_id,omitempty"`
	ExecutionID string                   `json:"execution_id,omitempty"`
	StepIndex   int                      `json:"step_index"`
	Attempt     int                      `json:"attempt,omitempty"`
	Timestamp   time.Time                `json:"timestamp"`
	ToolName    string                   `json:"tool_name"`
	Params      string                   `json:"params"`           // JSON, truncated
	Result      string                   `json:"result"`           // truncated
	Status      string                   `json:"status,omitempty"` // success | error
	Error       string                   `json:"error,omitempty"`
	DurationMs  int64                    `json:"duration_ms"`
	Cached      bool                     `json:"cached,omitempty"`
	Artifacts   []ToolCallArtifactRecord `json:"artifacts,omitempty"`
}

// ToolCallArtifactRecord is host-authored audit metadata. Validated must only
// be set after the host has resolved symlinks, enforced its workspace/run
// boundary, and verified a non-empty regular file.
type ToolCallArtifactRecord struct {
	Path      string `json:"path"`
	Size      int64  `json:"size,omitempty"`
	Validated bool   `json:"validated,omitempty"`
}

func pipelineToolCallRecordFailed(record ToolCallRecord) bool {
	status := strings.ToLower(strings.TrimSpace(record.Status))
	if strings.TrimSpace(record.Error) != "" || status == "error" || status == "failed" {
		return true
	}
	if status == "success" || status == "completed" {
		return false
	}
	return ToolResultLooksFailedForTool(record.ToolName, record.Result)
}

// ApprovalRecord captures each HITL approval event.
type ApprovalRecord struct {
	Timestamp time.Time `json:"timestamp"`
	Role      string    `json:"role"`     // e.g. "部门总监", "DPO", "财务", "组长"
	Action    string    `json:"action"`   // "approved" | "rejected" | "feedback"
	Feedback  string    `json:"feedback"` // user feedback text, empty if "ok"
}

// ====================== DB Record for Persistence ======================

type PipelineExecutionRecord struct {
	ID        uint      `gorm:"primarykey" json:"id"`
	ExecID    string    `gorm:"uniqueIndex;size:128" json:"exec_id"`
	StateJSON string    `gorm:"type:text" json:"state_json"` // JSON serialized PipelineExecution
	Status    string    `gorm:"size:30" json:"status"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// ====================== Pipeline Executor ======================

type PipelineExecutor struct {
	agentFactory        *AgentFactory
	gateway             *GatewayService
	fileLogger          *log.Logger
	executions          map[string]*PipelineExecution // in-memory cache
	pipelines           map[string]Pipeline           // known pipelines (including dynamic from Direct mode)
	pipelinesMu         sync.RWMutex
	mu                  sync.RWMutex
	retrySleep          func(time.Duration)
	parallelWallTimeout time.Duration
	professionalAgents  *ProfessionalAgentRegistry
}

const defaultParallelStepWallTimeout = 60 * time.Second

func NewPipelineExecutor(factory *AgentFactory, gateway *GatewayService, fileLogger *log.Logger) *PipelineExecutor {
	retrySleep := time.Sleep
	if runningGoTestBinary() {
		retrySleep = func(time.Duration) {}
	}
	workspace := ""
	if gateway != nil && gateway.config != nil {
		workspace = gateway.config.Workspace
	}
	professionalAgents := NewProfessionalAgentRegistry(workspace, fileLogger)
	if gateway != nil {
		professionalAgents.credentialSource = func() (ProfessionalAgentsConfig, map[string]ModelConfig) {
			gateway.mutex.RLock()
			defer gateway.mutex.RUnlock()
			if gateway.config == nil {
				return ProfessionalAgentsConfig{}, nil
			}
			return gateway.config.ProfessionalAgents, gateway.config.ModelConfig
		}
	}
	return &PipelineExecutor{
		agentFactory:        factory,
		gateway:             gateway,
		fileLogger:          fileLogger,
		executions:          make(map[string]*PipelineExecution),
		pipelines:           make(map[string]Pipeline),
		retrySleep:          retrySleep,
		parallelWallTimeout: defaultParallelStepWallTimeout,
		professionalAgents:  professionalAgents,
	}
}

func (e *PipelineExecutor) parallelStepWallTimeout() time.Duration {
	if e != nil && e.parallelWallTimeout > 0 {
		return e.parallelWallTimeout
	}
	return defaultParallelStepWallTimeout
}

func runningGoTestBinary() bool {
	name := strings.ToLower(filepath.Base(os.Args[0]))
	return strings.HasSuffix(name, ".test") || strings.HasSuffix(name, ".test.exe")
}

func (e *PipelineExecutor) SetRetrySleep(fn func(time.Duration)) {
	if fn == nil {
		fn = time.Sleep
	}
	e.retrySleep = fn
}

func (e *PipelineExecutor) sleepBeforeRetry(delay time.Duration) {
	if delay <= 0 {
		return
	}
	if e != nil && e.retrySleep != nil {
		e.retrySleep(delay)
		return
	}
	time.Sleep(delay)
}

// Run starts a pipeline. mode=interactive blocks goroutine on HITL; mode=api persists & releases.
// ExecutePipeline convienience wrapper for scheduled/cron execution.
// RegisterDynamic adds a temporary pipeline (e.g. from Direct mode DE config).
// Overwrites any existing pipeline with the same name.
func (e *PipelineExecutor) RegisterDynamic(name string, pipeline Pipeline) error {
	if err := validatePipelineOutputContracts(pipeline); err != nil {
		return fmt.Errorf("pipeline %q has invalid output contract: %w", name, err)
	}
	e.pipelinesMu.Lock()
	e.pipelines[name] = pipeline
	e.pipelinesMu.Unlock()
	return nil
}

// IsProfessionalOnlyPipeline reports whether every executable stage is an
// isolated professional-agent invocation. Such a pipeline does not need a
// host LLM/model route merely to be dispatched.
func (e *PipelineExecutor) IsProfessionalOnlyPipeline(name string) bool {
	if e == nil || strings.TrimSpace(name) == "" {
		return false
	}
	var pipeline Pipeline
	var ok bool
	if e.gateway != nil && e.gateway.config != nil {
		pipeline, ok = e.gateway.config.Pipeline.Pipelines[name]
		if !ok {
			for _, candidate := range e.gateway.config.Pipeline.Pipelines {
				if candidate.Name == name {
					pipeline, ok = candidate, true
					break
				}
			}
		}
	}
	if !ok {
		e.pipelinesMu.RLock()
		pipeline, ok = e.pipelines[name]
		if !ok {
			for _, candidate := range e.pipelines {
				if candidate.Name == name {
					pipeline, ok = candidate, true
					break
				}
			}
		}
		e.pipelinesMu.RUnlock()
	}
	if !ok && e.gateway != nil {
		pipeline, ok = e.gateway.defaultPipelineSet()[name]
	}
	if !ok || len(pipeline.Steps) == 0 {
		return false
	}
	for i := range pipeline.Steps {
		if !pipelineStepUsesOnlyProfessionalAgents(&pipeline.Steps[i]) {
			return false
		}
	}
	return true
}

func pipelineStepUsesOnlyProfessionalAgents(step *PipelineStep) bool {
	if step == nil {
		return false
	}
	if len(step.Parallel) > 0 {
		for i := range step.Parallel {
			if !pipelineStepUsesOnlyProfessionalAgents(&step.Parallel[i]) {
				return false
			}
		}
		return true
	}
	return step.ProfessionalAgent != nil
}

func (e *PipelineExecutor) ExecutePipeline(pipelineName, input, sessionID string) (string, error) {
	ctx := context.Background()
	exec, err := e.Run(ctx, pipelineName, input, sessionID, "api", "")
	if err != nil {
		return "", err
	}
	if out := exec.FinalOutput(); out != "" {
		return out, nil
	}
	return fmt.Sprintf("Pipeline %s completed.", pipelineName), nil
}

func (e *PipelineExecutor) Run(ctx context.Context, pipelineName string, input string, sessionID string, mode RunMode, notifyURL string) (*PipelineExecution, error) {
	if isRetiredBuiltinPipeline(pipelineName) {
		return nil, fmt.Errorf("pipeline '%s' is retired", pipelineName)
	}
	// Check static config first (by key, then by Name), then dynamic pipelines
	var p Pipeline
	var exists bool
	e.mu.RLock()
	p, exists = e.gateway.config.Pipeline.Pipelines[pipelineName]
	// Also search by Name field (map keys are often English IDs but Name is Chinese)
	if !exists {
		for _, pp := range e.gateway.config.Pipeline.Pipelines {
			if pp.Name == pipelineName {
				p = pp
				exists = true
				break
			}
		}
	}
	e.mu.RUnlock()
	if !exists {
		e.pipelinesMu.RLock()
		p, exists = e.pipelines[pipelineName]
		// Also search dynamic by Name
		if !exists {
			for _, pp := range e.pipelines {
				if pp.Name == pipelineName {
					p = pp
					exists = true
					break
				}
			}
		}
		e.pipelinesMu.RUnlock()
	}
	if !exists {
		// Fallback: check built-in default templates
		for _, tmpl := range getDefaultPipelineExamples() {
			if tmpl.Name == pipelineName {
				p = tmpl
				exists = true
				break
			}
		}
	}
	if !exists {
		return nil, fmt.Errorf("pipeline '%s' not found", pipelineName)
	}
	if intakeSpec, ok := aiEmployeeIntakeSpecFromContext(ctx); ok {
		p = prependAIEmployeeIntakeStep(p, intakeSpec)
	}
	if err := validatePipelineReworkConfig(p); err != nil {
		return nil, fmt.Errorf("pipeline '%s' has invalid rework configuration: %w", pipelineName, err)
	}
	if err := validatePipelineOutputContracts(p); err != nil {
		return nil, fmt.Errorf("pipeline '%s' has invalid output contract: %w", pipelineName, err)
	}
	if err := validatePipelineDatabaseConnections(p); err != nil {
		return nil, fmt.Errorf("pipeline '%s' has invalid database configuration: %w", pipelineName, err)
	}

	execID := newPipelineExecutionID(pipelineName)
	resolvedWorkspace := strings.TrimSpace(projectctx.ResolvedWorkspace(ctx))
	if resolvedWorkspace == "" && e != nil && e.gateway != nil && e.gateway.config != nil {
		resolvedWorkspace = strings.TrimSpace(e.gateway.config.Workspace)
	}
	if resolvedWorkspace == "" {
		resolvedWorkspace = "."
	}
	resolvedWorkspace = projectctx.NormalizeWorkspace(resolvedWorkspace)
	parentTaskID := strings.TrimSpace(taskIDFromContext(ctx))
	if parentTaskID == "" {
		parentTaskID = fmt.Sprintf("pipe_%d", time.Now().UnixNano())
	}
	exec := &PipelineExecution{
		ID:                      execID,
		PipelineName:            pipelineName,
		ResolvedWorkspace:       resolvedWorkspace,
		CurrentStep:             0,
		Status:                  "running",
		TaskID:                  parentTaskID,
		Mode:                    mode,
		NotifyURL:               notifyURL,
		Input:                   input,
		SessionID:               sessionID,
		CreatedAt:               time.Now(),
		RuntimePipeline:         &p,
		approveCh:               make(chan string, 1),
		running:                 true,
		databaseConnections:     make(map[string]PipelineDatabaseConnection, len(p.DatabaseConnections)),
		stepDatabaseConnections: make(map[int]string),
		databaseTools:           make(map[string]*PipelineDBQueryTool),
	}
	for _, profile := range p.DatabaseConnections {
		exec.databaseConnections[strings.TrimSpace(profile.ID)] = profile
	}
	for i := range p.Steps {
		exec.stepDatabaseConnections[i] = strings.TrimSpace(p.Steps[i].DatabaseConnection)
		for branchIdx := range p.Steps[i].Parallel {
			exec.stepDatabaseConnections[parallelAuditStepIndex(i, branchIdx)] = strings.TrimSpace(p.Steps[i].Parallel[branchIdx].DatabaseConnection)
		}
	}
	if principal, ok := employeeExecutionPrincipal(ctx); ok {
		exec.Principal = principal
	}

	exec.Steps = make([]StepResult, len(p.Steps))
	for i, s := range p.Steps {
		slotID := ""
		if s.Slot != nil {
			slotID = strings.TrimSpace(s.Slot.ID)
		}
		exec.Steps[i] = StepResult{Name: s.Name, SlotID: slotID, SlotStatus: pipelineSlotPending, Status: "pending", Revision: 1}
	}
	seedPipelineExecutionSlots(ctx, exec, p)
	initialStepName := ""
	if len(p.Steps) > 0 {
		initialStepName = p.Steps[0].Name
	}
	exec.Checkpoint = &PipelineCheckpoint{
		Version:        pipelineCheckpointVersion,
		PipelineExecID: exec.ID,
		ParentTaskID:   exec.TaskID,
		StepIndex:      0,
		StepName:       initialStepName,
		StepRevision:   1,
		Attempt:        1,
		Phase:          pipelineCheckpointPhaseStepPending,
		RouteEntry:     -1,
		Turns:          make(map[string]*PipelineTurnCheckpoint),
		UpdatedAt:      time.Now(),
	}

	e.mu.Lock()
	e.executions[execID] = exec
	e.mu.Unlock()

	e.saveToDB(exec)

	// Detach request cancellation while preserving context values injected by AE
	// dispatch, such as RAG config and pipeline isolation flags.
	bgCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	exec.cancelFunc = cancel

	if mode == RunModeAPI {
		go func() {
			defer cancel()
			defer exec.closeDatabaseTools()
			e.runStepsAPI(bgCtx, exec, p, input, sessionID)
		}()
	} else {
		go func() {
			defer cancel()
			defer exec.closeDatabaseTools()
			e.runStepsInteractive(bgCtx, exec, p, input, sessionID)
		}()
	}

	return exec, nil
}

func (e *PipelineExecutor) recoveryContext(ctx context.Context, exec *PipelineExecution) (context.Context, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if exec == nil {
		return ctx, fmt.Errorf("pipeline execution is nil")
	}
	if e.gateway != nil {
		ctx = e.gateway.withAgentOSRuntime(ctx)
	}
	if exec.Principal.Kind == "employee" {
		if e.gateway == nil || e.gateway.aeManager == nil {
			return ctx, fmt.Errorf("execution '%s' cannot reauthorize AI employee %q", exec.ID, exec.Principal.EmployeeID)
		}
		cfg, ok := e.gateway.aeManager.GetEmployee(exec.Principal.EmployeeID)
		if !ok || cfg == nil {
			return ctx, fmt.Errorf("execution '%s' AI employee %q is no longer registered", exec.ID, exec.Principal.EmployeeID)
		}
		ctx = withEmployeeExecutionGrant(ctx, exec.Principal.EmployeeID, exec.Principal.TenantID)
		ctx = context.WithValue(ctx, "ae_employee_id", exec.Principal.EmployeeID)
		ctx = context.WithValue(ctx, "rag_tenant_id", exec.Principal.TenantID)
		ctx = context.WithValue(ctx, "ae_system_prompt", cfg.SystemMessage)
		ctx = context.WithValue(ctx, "ae_name", cfg.Name)
		ctx = context.WithValue(ctx, "ae_model_binding", cfg.ModelBinding)
		ctx = permissionpolicy.WithUserToolPolicyInput(ctx, firstNonEmpty(pipelineUserPolicyInput(ctx, exec), exec.Input))
		if !cfg.RAGMultiTurn {
			ctx = context.WithValue(ctx, "pipeline_isolated_context", true)
		}
	}
	ctx = context.WithValue(ctx, "task_id", exec.TaskID)
	ctx = context.WithValue(ctx, "session_id", exec.SessionID)
	if strings.TrimSpace(exec.ResolvedWorkspace) != "" {
		ctx = projectctx.WithResolvedWorkspace(ctx, exec.ResolvedWorkspace)
	}
	return ctx, nil
}

func (e *PipelineExecutor) executionPipeline(exec *PipelineExecution) (Pipeline, error) {
	if exec == nil {
		return Pipeline{}, fmt.Errorf("pipeline execution is nil")
	}
	if exec.RuntimePipeline != nil {
		return *exec.RuntimePipeline, nil
	}
	if e.gateway != nil && e.gateway.config != nil {
		if pipeline, ok := e.gateway.config.Pipeline.Pipelines[exec.PipelineName]; ok {
			return pipeline, nil
		}
	}
	e.pipelinesMu.RLock()
	pipeline, ok := e.pipelines[exec.PipelineName]
	e.pipelinesMu.RUnlock()
	if !ok {
		return Pipeline{}, fmt.Errorf("pipeline '%s' is unavailable for recovery", exec.PipelineName)
	}
	return pipeline, nil
}

func (e *PipelineExecutor) startPersistedExecution(ctx context.Context, exec *PipelineExecution, allowedStatus ...string) (*PipelineExecution, error) {
	if exec == nil {
		return nil, fmt.Errorf("pipeline execution is nil")
	}
	exec.mu.Lock()
	allowed := false
	for _, status := range allowedStatus {
		if exec.Status == status {
			allowed = true
			break
		}
	}
	if !allowed {
		status := exec.Status
		exec.mu.Unlock()
		return nil, fmt.Errorf("execution '%s' cannot resume from status %s", exec.ID, status)
	}
	if exec.running {
		exec.mu.Unlock()
		return exec, nil
	}
	if exec.Status == "recovering" || exec.Status == "paused" {
		exec.Status = "running"
	}
	if exec.approveCh == nil {
		exec.approveCh = make(chan string, 1)
	}
	exec.running = true
	exec.mu.Unlock()

	ctx, err := e.recoveryContext(ctx, exec)
	if err != nil {
		exec.mu.Lock()
		exec.running = false
		exec.mu.Unlock()
		return nil, err
	}
	pipeline, err := e.executionPipeline(exec)
	if err != nil {
		exec.mu.Lock()
		exec.running = false
		exec.mu.Unlock()
		return nil, err
	}
	if err := validatePipelineOutputContracts(pipeline); err != nil {
		exec.mu.Lock()
		exec.running = false
		exec.mu.Unlock()
		return nil, fmt.Errorf("pipeline '%s' has invalid output contract: %w", exec.PipelineName, err)
	}
	exec.mu.Lock()
	if exec.databaseConnections == nil {
		exec.databaseConnections = make(map[string]PipelineDatabaseConnection, len(pipeline.DatabaseConnections))
	}
	if exec.stepDatabaseConnections == nil {
		exec.stepDatabaseConnections = make(map[int]string)
	}
	if exec.databaseTools == nil {
		exec.databaseTools = make(map[string]*PipelineDBQueryTool)
	}
	if exec.parallelToolCalls == nil {
		exec.parallelToolCalls = make(map[int][]ToolCallRecord)
	}
	if exec.presentationStates == nil {
		exec.presentationStates = make(map[int]*presentationProductionState)
	}
	for _, profile := range pipeline.DatabaseConnections {
		exec.databaseConnections[strings.TrimSpace(profile.ID)] = profile
	}
	for index := range pipeline.Steps {
		exec.stepDatabaseConnections[index] = strings.TrimSpace(pipeline.Steps[index].DatabaseConnection)
		for branchIndex := range pipeline.Steps[index].Parallel {
			exec.stepDatabaseConnections[parallelAuditStepIndex(index, branchIndex)] = strings.TrimSpace(pipeline.Steps[index].Parallel[branchIndex].DatabaseConnection)
		}
	}
	exec.mu.Unlock()
	e.saveToDB(exec)

	execCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	exec.cancelFunc = cancel
	e.mu.Lock()
	e.executions[exec.ID] = exec
	e.mu.Unlock()

	go func() {
		defer cancel()
		defer exec.closeDatabaseTools()
		e.runStepsFrom(execCtx, exec, pipeline, exec.CurrentStep, exec.Input, exec.SessionID, exec.Mode)
	}()
	return exec, nil
}

// Resume continues a persisted pipeline from a manual pause or its last HITL
// checkpoint. HITL callers still deliver the approval through approveCh after
// the worker has re-entered waitForApproval.
func (e *PipelineExecutor) Resume(ctx context.Context, execID string) (*PipelineExecution, error) {
	exec, ok := e.GetExecution(execID)
	if !ok {
		// Try loading from DB
		loaded := e.loadFromDB(execID)
		if loaded == nil {
			return nil, fmt.Errorf("execution '%s' not found", execID)
		}
		exec = loaded
		exec.approveCh = make(chan string, 1)
		e.mu.Lock()
		e.executions[execID] = exec
		e.mu.Unlock()
	}

	if exec.Status == "paused" {
		return e.startPersistedExecution(ctx, exec, "paused")
	}
	if exec.Status != "waiting_approval" {
		return nil, fmt.Errorf("execution '%s' is not paused or waiting for approval (status: %s)", execID, exec.Status)
	}
	return e.startPersistedExecution(ctx, exec, "waiting_approval")
}

// ResumeInterrupted continues an execution that was running when the process
// exited. It re-enters the current step, whose executeStep calls reuse their
// stable TurnCheckpoint identities.
func (e *PipelineExecutor) ResumeInterrupted(ctx context.Context, execID string) (*PipelineExecution, error) {
	exec, ok := e.GetExecution(execID)
	if !ok {
		exec = e.loadFromDB(execID)
		if exec == nil {
			return nil, fmt.Errorf("execution '%s' not found", execID)
		}
	}
	return e.startPersistedExecution(ctx, exec, "running", "recovering")
}

// pausePipelineAtBoundary completes a cooperative pause only between steps.
// The current step is allowed to finish so an in-flight mutating tool is never
// abandoned and then replayed with an unknown outcome.
func (e *PipelineExecutor) pausePipelineAtBoundary(exec *PipelineExecution, stepIndex int) bool {
	if e == nil || exec == nil {
		return false
	}
	exec.mu.Lock()
	if exec.Status != "pausing" {
		exec.mu.Unlock()
		return false
	}
	exec.Status = "paused"
	exec.CurrentStep = stepIndex
	e.ensurePipelineCheckpointLocked(exec)
	exec.Checkpoint.StepIndex = stepIndex
	exec.Checkpoint.StepName = ""
	if stepIndex >= 0 && stepIndex < len(exec.Steps) {
		exec.Checkpoint.StepName = exec.Steps[stepIndex].Name
	}
	exec.Checkpoint.Phase = pipelineCheckpointPhaseStepPending
	exec.Checkpoint.ActiveTurnKey = ""
	exec.Checkpoint.UpdatedAt = time.Now()
	exec.mu.Unlock()
	e.saveToDB(exec)
	e.broadcastPipelineEvent(exec.ID, "paused", map[string]interface{}{"step_index": stepIndex})
	return true
}

// ====================== Interactive Mode (Web UI) ======================

func (e *PipelineExecutor) runStepsInteractive(ctx context.Context, exec *PipelineExecution, pipeline Pipeline, input string, sessionID string) {
	e.runStepsFrom(ctx, exec, pipeline, 0, input, sessionID, RunModeInteractive)
}

func (e *PipelineExecutor) runStepsAPI(ctx context.Context, exec *PipelineExecution, pipeline Pipeline, input string, sessionID string) {
	e.runStepsFrom(ctx, exec, pipeline, 0, input, sessionID, RunModeAPI)
}

func (e *PipelineExecutor) runStepsFrom(ctx context.Context, exec *PipelineExecution, pipeline Pipeline, startStep int, input string, sessionID string, mode RunMode) {
	defer func() {
		exec.mu.Lock()
		exec.running = false
		exec.mu.Unlock()
	}()
	if pipeline.Name == dataAnalysisLocalFastPipelineName && startStep == 0 &&
		(len(pipeline.Steps) == 0 || pipeline.Steps[0].Name != aiEmployeeIntakeStepName) {
		e.runNativeDataAnalysisFastPath(ctx, exec, pipeline, input)
		return
	}

	steps := pipeline.Steps
	i := startStep

	// Track per-step repeat counts
	repeatCount := make(map[int]int)
	exec.mu.Lock()
	if exec.Checkpoint != nil {
		for index, count := range pipelineRepeatCountsFromStrings(exec.Checkpoint.RepeatCounts) {
			repeatCount[index] = count
		}
	}
	exec.mu.Unlock()

	// Compute routing target indices for branch boundary detection.
	routingTargetIndices := make(map[int]bool)
	for si := range steps {
		for _, route := range steps[si].Routing {
			ti := findStepIndex(steps, route.Goto)
			if ti >= 0 {
				routingTargetIndices[ti] = true
			}
		}
	}
	maxStep := len(steps)
	routedHere := false
	routeEntryPoint := -1
	exec.mu.Lock()
	if exec.Checkpoint != nil {
		routedHere = exec.Checkpoint.RoutedHere
		routeEntryPoint = exec.Checkpoint.RouteEntry
	}
	exec.mu.Unlock()

runLoop:
	for i >= 0 && i < maxStep {
		if e.pausePipelineAtBoundary(exec, i) {
			return
		}
		step := &steps[i]
		exec.mu.Lock()
		revision := 1
		if i >= 0 && i < len(exec.Steps) && exec.Steps[i].Revision > 0 {
			revision = exec.Steps[i].Revision
		}
		e.ensurePipelineCheckpointLocked(exec)
		exec.Checkpoint.StepIndex = i
		exec.Checkpoint.StepName = step.Name
		exec.Checkpoint.StepRevision = revision
		exec.Checkpoint.Phase = pipelineCheckpointPhaseStepPending
		exec.Checkpoint.RepeatCounts = pipelineRepeatCountsToStrings(repeatCount)
		exec.Checkpoint.RoutedHere = routedHere
		exec.Checkpoint.RouteEntry = routeEntryPoint
		exec.Checkpoint.UpdatedAt = time.Now()
		exec.mu.Unlock()
		e.saveToDB(exec)
		stepInput := pipelineSlotStepInput(pipeline, input, i)
		if pipelineStepHasReusableSlot(exec, i) {
			e.fileLogger.Printf("[Pipeline] Step %d (%s) reused filled slot from prior execution", i, step.Name)
			e.broadcastPipelineEvent(exec.ID, "step_done", map[string]interface{}{
				"step_index": i, "step_name": step.Name, "slot_id": step.Slot.ID, "reused": true,
			})
			exec.mu.Lock()
			e.ensurePipelineCheckpointLocked(exec)
			exec.Checkpoint.Phase = pipelineCheckpointPhaseStepFinalized
			exec.Checkpoint.TurnStatus = exec.Steps[i].Status
			exec.Checkpoint.UpdatedAt = time.Now()
			exec.mu.Unlock()
			e.saveToDB(exec)
			i++
			continue
		}
		// Branch boundary: if we're running a routed branch and hit
		// Branch boundary: if routed and this step starts another branch, stop
		if routedHere && routingTargetIndices[i] && i > 0 && i != routeEntryPoint {
			e.fileLogger.Printf("[Pipeline] Step %d (%s): branch boundary, stopping", i, step.Name)
			exec.mu.Lock()
			exec.CompletionReason = &PipelineCompletionReason{
				Code:      "branch_boundary",
				StepIndex: i,
				StepName:  step.Name,
				Message:   "routed branch reached the next branch boundary",
			}
			exec.mu.Unlock()
			break
		}

		// Check cancellation
		select {
		case <-ctx.Done():
			exec.mu.Lock()
			exec.Status = "stopped"
			exec.mu.Unlock()
			e.saveToDB(exec)
			e.broadcastPipelineEvent(exec.ID, "stopped", nil)
			return
		default:
		}

		// A frozen source is an immutable cross-step boundary. For a normal
		// downstream step, validate and (when required by the snapshot) pre-read
		// it once on the host before spending a model call. Parallel branches do
		// their own pre-read below so each branch gets independent audit evidence.
		var sourceRuntimeContext string
		var prerequisiteErr error
		if len(step.Parallel) == 0 {
			sourceRuntimeContext, prerequisiteErr = e.preparePipelineSourceRuntimeContext(exec, i, i)
		} else {
			prerequisiteErr = e.verifyPipelinePrerequisites(exec, i)
		}
		if prerequisiteErr != nil {
			exec.mu.Lock()
			exec.CurrentStep = i
			exec.Steps[i].Status = "failed"
			exec.Steps[i].Error = "source contract: " + prerequisiteErr.Error()
			exec.Status = "failed"
			exec.mu.Unlock()
			e.saveToDB(exec)
			e.fileLogger.Printf("[Pipeline] Step %d (%s) blocked before execution: %v", i, step.Name, prerequisiteErr)
			e.broadcastPipelineEvent(exec.ID, "step_failed", map[string]interface{}{
				"step_index": i, "step_name": step.Name, "error": prerequisiteErr.Error(), "contract": "source",
			})
			return
		}

		// ========== Parallel step group ==========
		if len(step.Parallel) > 0 {
			e.executeParallelSteps(ctx, exec, step, i, stepInput, sessionID)
			exec.mu.Lock()
			if exec.Status == "failed" || exec.Status == "stopped" {
				exec.mu.Unlock()
				return
			}
			exec.mu.Unlock()
			i++
			continue
		}

		// ========== Normal step execution ==========
		exec.mu.Lock()
		exec.CurrentStep = i
		if exec.Steps[i].Status != "waiting_approval" {
			exec.Steps[i].Status = "running"
		}
		exec.Steps[i].SlotStatus = pipelineSlotFilling
		e.ensurePipelineCheckpointLocked(exec)
		exec.Checkpoint.Phase = pipelineCheckpointPhaseStepRunning
		exec.Checkpoint.UpdatedAt = time.Now()
		exec.mu.Unlock()
		e.saveToDB(exec)

		e.broadcastPipelineEvent(exec.ID, "step_start", map[string]interface{}{
			"step_index": i,
			"step_name":  step.Name,
		})

		_, hostSourceReady, hostSourceErr := e.hostAcquireDescribedSource(ctx, exec, step, i, stepInput)
		if hostSourceErr != nil {
			exec.mu.Lock()
			exec.Steps[i].Status = "failed"
			exec.Steps[i].Error = hostSourceErr.Error()
			exec.Status = "failed"
			exec.mu.Unlock()
			e.saveToDB(exec)
			e.broadcastPipelineEvent(exec.ID, "step_failed", map[string]interface{}{"step_index": i, "step_name": step.Name, "error": hostSourceErr.Error()})
			return
		}
		if hostSourceReady {
			output := fmt.Sprintf("SOURCE_STATUS: READY\nSOURCE_FILE: %s\nHost source acquisition completed; the downstream delivery step will use the frozen source.", pipelineFrozenSourcePath(exec, step.SourceContract))
			snapshot, contractErr := e.validatePipelineStepContracts(exec, step, i, stepInput, output)
			if contractErr != nil {
				exec.mu.Lock()
				exec.Steps[i].Status = "failed"
				exec.Steps[i].Error = contractErr.Error()
				exec.Status = "failed"
				exec.mu.Unlock()
				e.saveToDB(exec)
				e.broadcastPipelineEvent(exec.ID, "step_failed", map[string]interface{}{"step_index": i, "step_name": step.Name, "error": contractErr.Error()})
				return
			}
			exec.mu.Lock()
			exec.Steps[i].Output = output
			exec.Steps[i].Status = "completed"
			exec.Steps[i].SlotStatus = pipelineSlotFilled
			exec.Steps[i].SlotValidation = "passed"
			exec.Steps[i].SourceSnapshot = snapshot
			exec.Steps[i].SlotFill = buildPipelineSlotFillRecord(step, exec.Steps[i])
			exec.mu.Unlock()
			e.saveStepOutputToFile(exec, i, output)
			e.saveToDB(exec)
			e.broadcastPipelineEvent(exec.ID, "step_done", map[string]interface{}{"step_index": i, "step_name": step.Name, "host_source_acquire": true})
			i++
			continue
		}

		hostInvestmentOutput, hostInvestmentReady, hostInvestmentErr := e.hostCollectInvestmentEvidence(ctx, exec, step, i, stepInput)
		if hostInvestmentErr != nil {
			if step.ContinueOnError && i < len(steps)-1 && ctx.Err() == nil {
				recoverableOutput := buildRecoverableStepFailureOutput(step, hostInvestmentErr)
				exec.mu.Lock()
				exec.Steps[i].Output = recoverableOutput
				exec.Steps[i].Status = "degraded"
				exec.Steps[i].Error = hostInvestmentErr.Error()
				exec.mu.Unlock()
				e.saveStepOutputToFile(exec, i, recoverableOutput)
				e.saveToDB(exec)
				e.broadcastPipelineEvent(exec.ID, "step_done", map[string]interface{}{
					"step_index": i, "step_name": step.Name, "degraded": true,
					"reason": "host_structured_evidence_unavailable", "recoverable_error": hostInvestmentErr.Error(),
				})
				i++
				continue
			}
			exec.mu.Lock()
			exec.Steps[i].Status = "failed"
			exec.Steps[i].Error = hostInvestmentErr.Error()
			exec.Status = "failed"
			exec.mu.Unlock()
			e.saveToDB(exec)
			e.broadcastPipelineEvent(exec.ID, "step_failed", map[string]interface{}{"step_index": i, "step_name": step.Name, "error": hostInvestmentErr.Error()})
			return
		}
		if hostInvestmentReady {
			exec.mu.Lock()
			exec.Steps[i].Output = hostInvestmentOutput
			exec.Steps[i].Status = "completed"
			exec.Steps[i].SlotStatus = pipelineSlotFilled
			exec.Steps[i].SlotValidation = "passed"
			exec.Steps[i].SlotFill = buildPipelineSlotFillRecord(step, exec.Steps[i])
			exec.mu.Unlock()
			e.saveStepOutputToFile(exec, i, hostInvestmentOutput)
			e.saveToDB(exec)
			e.broadcastPipelineEvent(exec.ID, "step_done", map[string]interface{}{"step_index": i, "step_name": step.Name, "host_structured_evidence": true})
			i++
			continue
		}

		hostInvestmentReport, hostInvestmentReportReady, hostInvestmentReportErr := e.hostDeliverInvestmentReport(ctx, exec, step, i, stepInput)
		if hostInvestmentReportErr != nil {
			exec.mu.Lock()
			exec.Steps[i].Status = "failed"
			exec.Steps[i].Error = hostInvestmentReportErr.Error()
			exec.Status = "failed"
			exec.mu.Unlock()
			e.saveToDB(exec)
			e.broadcastPipelineEvent(exec.ID, "step_failed", map[string]interface{}{"step_index": i, "step_name": step.Name, "error": hostInvestmentReportErr.Error()})
			return
		}
		if hostInvestmentReportReady {
			snapshot, contractErr := e.validatePipelineStepContracts(exec, step, i, stepInput, hostInvestmentReport)
			if contractErr != nil {
				exec.mu.Lock()
				exec.Steps[i].Status = "failed"
				exec.Steps[i].Error = contractErr.Error()
				exec.Status = "failed"
				exec.mu.Unlock()
				e.saveToDB(exec)
				e.broadcastPipelineEvent(exec.ID, "step_failed", map[string]interface{}{"step_index": i, "step_name": step.Name, "error": contractErr.Error(), "contract": "artifact"})
				return
			}
			exec.mu.Lock()
			exec.Steps[i].Output = hostInvestmentReport
			exec.Steps[i].Status = "completed"
			exec.Steps[i].SlotStatus = pipelineSlotFilled
			exec.Steps[i].SlotValidation = "passed"
			exec.Steps[i].SourceSnapshot = snapshot
			exec.Steps[i].SlotFill = buildPipelineSlotFillRecord(step, exec.Steps[i])
			exec.mu.Unlock()
			e.saveStepOutputToFile(exec, i, hostInvestmentReport)
			e.saveToDB(exec)
			e.broadcastPipelineEvent(exec.ID, "step_done", map[string]interface{}{"step_index": i, "step_name": step.Name, "host_report_delivery": true})
			i++
			continue
		}

		hostPPTOutput, hostPPTReady, hostPPTErr := e.hostRunPPTPolishStep(ctx, exec, step, i, stepInput)
		if hostPPTErr != nil && exec.PipelineName == "ppt-polish" && strings.TrimSpace(step.Name) == "交付自查" && pptPolishHostFallbackEligible(hostPPTErr) {
			visualIndex := pipelineStepResultIndex(exec, "视觉润色")
			if visualIndex >= 0 && visualIndex < i && visualIndex < len(steps) {
				fallbackOutput, fallbackReady, fallbackErr := e.hostFallbackPPTPolishVisual(
					ctx,
					exec,
					&steps[visualIndex],
					visualIndex,
					hostPPTErr,
				)
				if fallbackReady && fallbackErr == nil {
					baseFallback := pptPolishOutputUsesHostBaseFallback(fallbackOutput)
					exec.mu.Lock()
					exec.Steps[visualIndex].Output = fallbackOutput
					exec.Steps[visualIndex].Status = "completed"
					exec.Steps[visualIndex].Error = ""
					exec.Steps[visualIndex].SlotStatus = pipelineSlotFilled
					exec.Steps[visualIndex].SlotValidation = "passed: existing model PPTX preserved"
					if baseFallback {
						exec.Steps[visualIndex].Status = "degraded"
						exec.Steps[visualIndex].Error = "model route did not produce a PPTX; host base-ppt fallback completed"
						exec.Steps[visualIndex].SlotValidation = "degraded: host PPT fallback"
					}
					exec.Steps[visualIndex].SlotFill = buildPipelineSlotFillRecord(&steps[visualIndex], exec.Steps[visualIndex])
					exec.mu.Unlock()
					e.saveStepOutputToFile(exec, visualIndex, fallbackOutput)
					e.saveToDB(exec)
					if e.fileLogger != nil {
						e.fileLogger.Printf("[Pipeline] Step %d (%s) recovered PPTX without overwriting model output (base_fallback=%t): %v", i, step.Name, baseFallback, hostPPTErr)
					}
					hostPPTOutput, hostPPTReady, hostPPTErr = e.hostRunPPTPolishStep(ctx, exec, step, i, stepInput)
				} else if fallbackReady && fallbackErr != nil && e.fileLogger != nil {
					e.fileLogger.Printf("[Pipeline] Step %d (%s) could not recover missing PPTX: %v", i, step.Name, fallbackErr)
				}
			}
		}
		if hostPPTErr == nil && hostPPTReady {
			hostRework, hostReworkErr := e.processPipelineReworkDecision(exec, steps, i, hostPPTOutput)
			if hostReworkErr != nil {
				hostPPTErr = hostReworkErr
				hostPPTReady = false
			} else {
				hostPPTOutput = hostRework.Output
				if hostRework.Requested {
					e.saveToDB(exec)
					if e.fileLogger != nil {
						e.fileLogger.Printf("[Pipeline] Host PPT step %d (%s) requested rework of step %d (%s), attempt %d/%d: %s",
							i, step.Name, hostRework.TargetIndex, steps[hostRework.TargetIndex].Name,
							hostRework.Attempt, hostRework.Max, hostRework.Reason)
					}
					e.broadcastPipelineEvent(exec.ID, "step_rework_requested", map[string]interface{}{
						"from_step_index": i, "from_step_name": step.Name,
						"target_step_index": hostRework.TargetIndex, "target_step_name": steps[hostRework.TargetIndex].Name,
						"attempt": hostRework.Attempt, "max": hostRework.Max, "reason": hostRework.Reason,
					})
					i = hostRework.TargetIndex
					continue
				}
			}
		}
		if hostPPTErr != nil {
			exec.mu.Lock()
			exec.Steps[i].Status = "failed"
			exec.Steps[i].Error = hostPPTErr.Error()
			exec.Steps[i].SlotStatus = pipelineSlotInvalid
			exec.Steps[i].SlotValidation = hostPPTErr.Error()
			exec.Status = "failed"
			exec.mu.Unlock()
			e.saveToDB(exec)
			e.broadcastPipelineEvent(exec.ID, "step_failed", map[string]interface{}{
				"step_index": i, "step_name": step.Name, "error": hostPPTErr.Error(), "host_ppt_polish": true,
			})
			return
		}
		if hostPPTReady {
			exec.mu.Lock()
			exec.Steps[i].Output = hostPPTOutput
			exec.Steps[i].Status = "completed"
			exec.Steps[i].SlotStatus = pipelineSlotFilled
			exec.Steps[i].SlotValidation = "passed"
			exec.Steps[i].SlotFill = buildPipelineSlotFillRecord(step, exec.Steps[i])
			exec.mu.Unlock()
			e.saveStepOutputToFile(exec, i, hostPPTOutput)
			e.saveToDB(exec)
			e.broadcastPipelineEvent(exec.ID, "step_done", map[string]interface{}{
				"step_index": i, "step_name": step.Name, "host_ppt_polish": true,
			})
			i++
			continue
		}
		prompt := renderStepPrompt(appendStepToolBoundaryInstruction(buildStepExecutionPrompt(step.Prompt), step), i, exec.Steps, stepInput)
		prompt = appendPipelineReworkReviewInstruction(prompt, step)
		if previousContext := buildPreviousStepContext(i, exec.Steps); previousContext != "" {
			prompt = fmt.Sprintf(`%s

---

## 上游步骤产出
你必须基于这些已完成结果继续工作，不要重新寻找已经读过的材料，不要把“继续读取/继续搜索”作为最终回答。

%s

## 上游产出文件
%s
`, prompt, previousContext, buildPreviousStepFileList(exec, i))
		}
		if reworkContext := pendingPipelineReworkContext(exec, i); reworkContext != "" {
			prompt += "\n\n---\n\n" + reworkContext
		}
		if evidenceRule := evidenceBoundaryInstruction(ctx); evidenceRule != "" {
			prompt = prompt + "\n\n---\n\n" + evidenceRule
		}
		targetScopeInput := stepInput
		if strings.TrimSpace(step.Name) == aiEmployeeIntakeStepName && strings.TrimSpace(pipeline.AIEmployeeOriginalTask) != "" {
			// Intake retains the labelled conversation for reasoning, but target
			// coordinates must come only from the current turn. Otherwise the first
			// stale path in history can override the user's corrected path.
			targetScopeInput = pipeline.AIEmployeeOriginalTask
		}
		prompt = appendPipelineTargetScopeInstruction(prompt, targetScopeInput)
		prompt = appendLeadResearchProductSourceContext(prompt, e.gateway, exec, step, stepInput)
		prompt = appendPipelineRuntimeContractInstructions(prompt, exec, steps, i, stepInput)
		prompt = appendPipelineSourceRuntimeContext(prompt, sourceRuntimeContext)
		prompt = appendPipelineSlotInstruction(prompt, exec, step, i)
		prompt = appendAIEmployeeClarificationProtocol(prompt, pipeline, step)
		// Inject step-level few-shot examples for routing steps
		if len(step.StepExamples) > 0 {
			var sb strings.Builder
			sb.WriteString("## Few-Shot 示例\n")
			for _, ex := range step.StepExamples {
				fmt.Fprintf(&sb, "用户: \"%s\" → %s\n", ex.UserMessage, ex.RouteTo)
			}
			sb.WriteString("\n")
			prompt = sb.String() + prompt
		}
		// Execute each agent step once. Model transport retries are already bounded
		// inside callLLM; retrying the whole agent loop here multiplies hangs and can
		// repeat completed tool side effects.
		var output string
		var err error
		maxStepRetries := 0
		if strings.TrimSpace(step.Name) == aiEmployeeIntakeStepName {
			output, _ = buildAIEmployeeDeterministicMissingInput(pipeline, input)
		}
		for retry := 0; strings.TrimSpace(output) == "" && retry <= maxStepRetries; retry++ {
			if retry > 0 {
				e.fileLogger.Printf("[Pipeline] Step %d (%s) retry %d/%d", i, step.Name, retry, maxStepRetries)
				e.sleepBeforeRetry(time.Duration(retry) * 2 * time.Second) // 2s, 4s backoff
			}
			output, err = e.executeStep(ctx, exec, step, i, prompt, sessionID)
			if err == nil {
				break
			}
			if !shouldRetryPipelineStepError(err) {
				e.fileLogger.Printf("[Pipeline] Step %d (%s) non-transient failure, no step retry: %v", i, step.Name, err)
				break
			}
			e.fileLogger.Printf("[Pipeline] Step %d (%s) attempt %d failed: %v", i, step.Name, retry+1, err)
		}
		if err != nil && strings.TrimSpace(step.Name) == aiEmployeeIntakeStepName {
			if e.fileLogger != nil {
				e.fileLogger.Printf("[Pipeline] Step %d (%s) using host intake fallback after model failure: %v", i, step.Name, err)
			}
			output = buildAIEmployeeIntakeFailureFallback(pipeline, input)
			err = nil
		}
		if err == nil && strings.TrimSpace(step.Name) == aiEmployeeIntakeStepName {
			if normalized, overridden := normalizeAIEmployeeIntakeDecision(pipeline, input, output); overridden {
				if e.fileLogger != nil {
					e.fileLogger.Printf("[Pipeline] Step %d (%s) normalized an intake-only stop into a downstream user-facing handoff", i, step.Name)
				}
				output = normalized
			}
		}
		if err == nil && i == len(steps)-1 &&
			aiEmployeeTaskRequestsInteractiveHandoff(
				pipeline.AIEmployeeID,
				pipeline.AIEmployeeOriginalTask,
			) {
			compactStep := *step
			compactStep.NoTools = true
			compactStep.Tools = nil
			compactStep.RequiredTools = nil
			compactStep.RequiredSkills = nil
			compactStep.MaxIter = 2
			compactStep.OutputTokenLimit = 600
			compactPrompt := buildAIEmployeeInteractiveHandoffCompactionPrompt(prompt, output)
			if compacted, compactErr := e.executeStep(ctx, exec, &compactStep, i, compactPrompt, sessionID); compactErr == nil &&
				strings.TrimSpace(compacted) != "" {
				if e.fileLogger != nil {
					e.fileLogger.Printf("[Pipeline] Step %d (%s) compacted an explicit user interaction handoff", i, step.Name)
				}
				output = compacted
			}
		}
		if err == nil && i == len(steps)-1 {
			output = sanitizeAIEmployeeTerminalOutput(
				pipeline.AIEmployeeOriginalTask,
				output,
				step.RequiredSkills...,
			)
			output = ensureAIEmployeeExplicitConfirmationCoverage(
				pipeline.AIEmployeeOriginalTask,
				output,
			)
			if issues := aiEmployeeMinimumProgressIssues(&pipeline, step, output); len(issues) > 0 {
				if e.fileLogger != nil {
					e.fileLogger.Printf("[Pipeline] Step %d (%s) repairing %d minimum-progress issue(s)", i, step.Name, len(issues))
				}
				repairStep := *step
				repairStep.NoTools = true
				repairStep.Tools = nil
				repairStep.RequiredTools = nil
				repairStep.RequiredSkills = nil
				repairStep.MaxIter = 2
				repairPrompt := buildAIEmployeeMinimumProgressRepairPrompt(prompt, issues, output)
				if aiEmployeeTaskRequestsInteractiveHandoff(
					pipeline.AIEmployeeID,
					pipeline.AIEmployeeOriginalTask,
				) {
					repairStep.OutputTokenLimit = 600
					repairPrompt = buildAIEmployeeInteractiveHandoffCompactionPrompt(prompt, output)
				}
				repaired, repairErr := e.executeStep(ctx, exec, &repairStep, i, repairPrompt, sessionID)
				if repairErr == nil && strings.TrimSpace(repaired) != "" {
					output = sanitizeAIEmployeeTerminalOutput(
						pipeline.AIEmployeeOriginalTask,
						repaired,
						step.RequiredSkills...,
					)
					output = ensureAIEmployeeExplicitConfirmationCoverage(
						pipeline.AIEmployeeOriginalTask,
						output,
					)
				} else if repairErr != nil {
					err = fmt.Errorf("minimum viable delivery repair failed: %w", repairErr)
				}
				if err == nil {
					if remaining := aiEmployeeMinimumProgressIssues(&pipeline, step, output); len(remaining) > 0 {
						// Keep one focused retry available after the full-context
						// repair. Repeating the original prompt can reinforce the
						// same questionnaire shape; the focused pass receives only
						// the remaining delivery defects and current draft.
						secondPrompt := buildAIEmployeeMinimumProgressRepairPrompt("", remaining, output)
						secondRepaired, secondErr := e.executeStep(ctx, exec, &repairStep, i, secondPrompt, sessionID)
						if secondErr == nil && strings.TrimSpace(secondRepaired) != "" {
							output = sanitizeAIEmployeeTerminalOutput(
								pipeline.AIEmployeeOriginalTask,
								secondRepaired,
								step.RequiredSkills...,
							)
							output = ensureAIEmployeeExplicitConfirmationCoverage(
								pipeline.AIEmployeeOriginalTask,
								output,
							)
						}
						if terminalIssues := aiEmployeeMinimumProgressIssues(&pipeline, step, output); len(terminalIssues) > 0 {
							err = fmt.Errorf("final answer still deferred the requested delivery after bounded repair: %s", strings.Join(terminalIssues, "; "))
						}
					}
				}
			}
		}
		isFundraisingContentStep := strings.TrimSpace(pipeline.AIEmployeeID) == "fundraising-advisor" &&
			strings.TrimSpace(step.Name) == "融资专业分析与交付"
		isFundraisingPresentationStep := strings.TrimSpace(pipeline.AIEmployeeID) == "fundraising-advisor" &&
			strings.TrimSpace(step.Name) == "融资BP演示文稿生成"
		if err == nil && (i == len(steps)-1 || isFundraisingContentStep) {
			if !isFundraisingPresentationStep {
				if issues := aiEmployeeDomainOutputIssues(&pipeline, output); len(issues) > 0 {
					if e.fileLogger != nil {
						e.fileLogger.Printf("[Pipeline] Step %d (%s) repairing %d domain-quality issue(s)", i, step.Name, len(issues))
					}
					repairStep := *step
					repairStep.NoTools = true
					repairStep.Tools = nil
					repairStep.RequiredTools = nil
					repairStep.RequiredSkills = nil
					repairStep.MaxIter = 2
					if strings.TrimSpace(pipeline.AIEmployeeID) == "market-analyst" {
						repairStep.NoTools = false
						repairStep.Tools = []string{"web_search", "web_fetch", "write_file"}
						repairStep.MaxIter = 8
					}
					repairPrompt := buildAIEmployeeDomainRepairPrompt(&pipeline, prompt, issues, output)
					repaired, repairErr := e.executeStep(ctx, exec, &repairStep, i, repairPrompt, sessionID)
					if repairErr == nil && strings.TrimSpace(repaired) != "" {
						output = repaired
					}
					if remaining := aiEmployeeDomainOutputIssues(&pipeline, output); len(remaining) > 0 {
						if strings.TrimSpace(pipeline.AIEmployeeID) == "fundraising-advisor" {
							output = stripUnsupportedFundraisingBenchmarkLines(output, pipeline.AIEmployeeOriginalTask)
							if e.fileLogger != nil {
								e.fileLogger.Printf("[Pipeline] Step %d (%s) removed %d unsupported benchmark line(s) after bounded repair", i, step.Name, len(remaining))
							}
							if coverageIssues := aiEmployeeDomainOutputIssues(&pipeline, output); len(coverageIssues) > 0 {
								// The first repair already received the complete step
								// context. Keep later passes focused on the remaining
								// semantic defects and the current draft.
								secondPrompt := buildAIEmployeeDomainRepairPrompt(&pipeline, "", coverageIssues, output)
								secondRepaired, secondErr := e.executeStep(ctx, exec, &repairStep, i, secondPrompt, sessionID)
								if secondErr == nil && strings.TrimSpace(secondRepaired) != "" {
									output = secondRepaired
								}
								if finalIssues := aiEmployeeDomainOutputIssues(&pipeline, output); len(finalIssues) > 0 {
									thirdPrompt := buildAIEmployeeDomainRepairPrompt(&pipeline, "", finalIssues, output)
									thirdRepaired, thirdErr := e.executeStep(ctx, exec, &repairStep, i, thirdPrompt, sessionID)
									if thirdErr == nil && strings.TrimSpace(thirdRepaired) != "" {
										output = thirdRepaired
									}
									if terminalIssues := aiEmployeeDomainOutputIssues(&pipeline, output); len(terminalIssues) > 0 {
										output = stripUnsupportedFundraisingBenchmarkLines(output, pipeline.AIEmployeeOriginalTask)
									}
								}
							}
						} else {
							// Evidence defects need another research pass, not a
							// financing-specific line stripper.
							secondPrompt := buildAIEmployeeDomainRepairPrompt(&pipeline, "", remaining, output)
							secondRepaired, secondErr := e.executeStep(ctx, exec, &repairStep, i, secondPrompt, sessionID)
							if secondErr == nil && strings.TrimSpace(secondRepaired) != "" {
								output = secondRepaired
							}
						}
					}
				}
			}
			if isFundraisingContentStep {
				output = ensureFundraisingTrailingDescriptionCoverage(
					pipeline.AIEmployeeOriginalTask,
					output,
				)
				output = ensureFundraisingRelationshipQuestionCoverage(
					pipeline.AIEmployeeOriginalTask,
					output,
				)
				output = stripFundraisingDirectDraftQuestions(
					pipeline.AIEmployeeOriginalTask,
					output,
				)
			}
			output = sanitizeAIEmployeeTerminalOutput(
				pipeline.AIEmployeeOriginalTask,
				output,
				step.RequiredSkills...,
			)
			if i == len(steps)-1 {
				output = ensureAIEmployeeExplicitConfirmationCoverage(pipeline.AIEmployeeOriginalTask, output)
			}
		}
		pptPolishFallbackAttempted := false
		pptPolishFallbackDegraded := false
		pptPolishBriefHostFinalized := false
		if reconciledOutput, reconciliationReady, reconciliationErr := e.hostMaterializePPTPolishBriefFromStepOutput(exec, step, i, output); reconciliationReady {
			if reconciliationErr == nil {
				if e.fileLogger != nil {
					e.fileLogger.Printf("[Pipeline] Step %d (%s) materialized a validated execution-scoped PPT content brief from structured model output", i, step.Name)
				}
				output = reconciledOutput
				err = nil
				pptPolishBriefHostFinalized = true
			} else if e.fileLogger != nil {
				e.fileLogger.Printf("[Pipeline] Step %d (%s) could not reconcile PPT content brief from model output: %v", i, step.Name, reconciliationErr)
			}
		}
		if err != nil {
			modelErr := err
			recoveredOutput, recoveryReady, recoveryErr := e.hostRecoverPPTPolishBriefAfterModelFailure(exec, step, i, modelErr)
			if recoveryReady && recoveryErr == nil {
				if e.fileLogger != nil {
					e.fileLogger.Printf("[Pipeline] Step %d (%s) recovered a validated PPT content brief after incomplete model finalization: %v", i, step.Name, modelErr)
				}
				output = recoveredOutput
				err = nil
				pptPolishBriefHostFinalized = true
			} else if recoveryReady && recoveryErr != nil && e.fileLogger != nil {
				e.fileLogger.Printf("[Pipeline] Step %d (%s) rejected PPT content brief recovery: %v", i, step.Name, recoveryErr)
			}
		}
		if err == nil && shouldRecoverPPTPolishVisualMissingOutput(exec, step, output, pipeline) {
			pptPolishFallbackAttempted = true
			modelOutputErr := errors.New("visual production incorrectly reported missing user input")
			fallbackOutput, fallbackReady, fallbackErr := e.hostFallbackPPTPolishVisual(
				ctx,
				exec,
				step,
				i,
				modelOutputErr,
			)
			if fallbackReady && fallbackErr == nil {
				if e.fileLogger != nil {
					e.fileLogger.Printf(
						"[Pipeline] Step %d (%s) replaced invalid missing-input response with host PPT fallback",
						i,
						step.Name,
					)
				}
				output = fallbackOutput
				pptPolishFallbackDegraded = pptPolishOutputUsesHostBaseFallback(fallbackOutput)
			} else if fallbackReady {
				err = fmt.Errorf("host PPT fallback failed after invalid missing-input response: %w", fallbackErr)
			}
		}
		var sourceSnapshot *PipelineSourceSnapshot
		if err == nil {
			var contractErr error
			sourceSnapshot, contractErr = e.validatePipelineStepContracts(exec, step, i, stepInput, output)
			if contractErr != nil {
				e.fileLogger.Printf("[Pipeline] Step %d (%s) contract repair: %v", i, step.Name, contractErr)
				if exec.PipelineName == "wechat-article" && strings.TrimSpace(step.Name) == "公众号交付物" {
					err = fmt.Errorf("pipeline contract failed: %w", contractErr)
				} else {
					repairPrompt := prompt + "\n\n---\n\n" + pipelineContractRepairInstruction(exec, steps, i, stepInput, contractErr)
					output, err = e.executeStep(ctx, exec, step, i, repairPrompt, sessionID)
					if err == nil {
						sourceSnapshot, contractErr = e.validatePipelineStepContracts(exec, step, i, stepInput, output)
					}
					if err == nil && contractErr != nil {
						err = fmt.Errorf("pipeline contract failed after repair: %w", contractErr)
					}
				}
			}
		}
		if err != nil && !pptPolishFallbackAttempted {
			modelErr := err
			fallbackOutput, fallbackReady, fallbackErr := e.hostFallbackPPTPolishVisual(ctx, exec, step, i, modelErr)
			pptPolishFallbackAttempted = fallbackReady
			if fallbackReady && fallbackErr == nil {
				if e.fileLogger != nil {
					e.fileLogger.Printf("[Pipeline] Step %d (%s) using host PPT fallback after model/contract failure: %v", i, step.Name, modelErr)
				}
				output = fallbackOutput
				err = nil
				pptPolishFallbackDegraded = pptPolishOutputUsesHostBaseFallback(fallbackOutput)
				var contractErr error
				sourceSnapshot, contractErr = e.validatePipelineStepContracts(exec, step, i, stepInput, output)
				if contractErr != nil {
					err = fmt.Errorf("host PPT fallback failed contract: %w", contractErr)
				}
			} else if fallbackReady && fallbackErr != nil && e.fileLogger != nil {
				e.fileLogger.Printf("[Pipeline] Step %d (%s) host PPT fallback failed: %v", i, step.Name, fallbackErr)
			}
		}
		if err != nil {
			modelErr := err
			fallbackOutput, fallbackReady, fallbackErr := e.hostFallbackWechatArticleDelivery(ctx, exec, step, i)
			if fallbackReady && fallbackErr == nil {
				if e.fileLogger != nil {
					e.fileLogger.Printf("[Pipeline] Step %d (%s) using host WeChat delivery fallback after model/contract failure: %v", i, step.Name, modelErr)
				}
				output = fallbackOutput
				err = nil
				var contractErr error
				sourceSnapshot, contractErr = e.validatePipelineStepContracts(exec, step, i, stepInput, output)
				if contractErr != nil {
					err = fmt.Errorf("host WeChat delivery fallback failed contract: %w", contractErr)
				}
			} else if fallbackReady && fallbackErr != nil && e.fileLogger != nil {
				e.fileLogger.Printf("[Pipeline] Step %d (%s) host WeChat delivery fallback failed: %v", i, step.Name, fallbackErr)
			}
		}

		reworkOutcome := pipelineReworkOutcome{Output: output}
		if err == nil {
			reworkOutcome, err = e.processPipelineReworkDecision(exec, steps, i, output)
			output = reworkOutcome.Output
			if err == nil && reworkOutcome.Requested {
				e.saveToDB(exec)
				e.fileLogger.Printf("[Pipeline] Step %d (%s) requested rework of step %d (%s), attempt %d/%d: %s",
					i, step.Name, reworkOutcome.TargetIndex, steps[reworkOutcome.TargetIndex].Name,
					reworkOutcome.Attempt, reworkOutcome.Max, reworkOutcome.Reason)
				e.broadcastPipelineEvent(exec.ID, "step_rework_requested", map[string]interface{}{
					"from_step_index": i, "from_step_name": step.Name,
					"target_step_index": reworkOutcome.TargetIndex, "target_step_name": steps[reworkOutcome.TargetIndex].Name,
					"attempt": reworkOutcome.Attempt, "max": reworkOutcome.Max, "reason": reworkOutcome.Reason,
				})
				i = reworkOutcome.TargetIndex
				continue
			}
		}

		outputContractDegraded := false
		outputContractReason := ""
		if err == nil {
			var contractErr error
			output, outputContractDegraded, contractErr = applyPipelineOutputContract(step.OutputContract, output)
			if contractErr != nil {
				err = contractErr
			} else if outputContractDegraded {
				outputContractReason = pipelineOutputContractDegradedReason(step.OutputContract, reworkOutcome.Output)
				if outputContractReason == "" {
					outputContractReason = "output contract violation"
				}
				if e.fileLogger != nil {
					e.fileLogger.Printf("[Pipeline] Step %d (%s) output contract degraded: %s", i, step.Name, outputContractReason)
				}
			}
		}
		unexpectedMissingInputSuppressed := false
		if err == nil {
			output, unexpectedMissingInputSuppressed = normalizeAIEmployeeStepWaitingInputOutput(output, pipeline, step)
			if i == len(steps)-1 {
				// Contract repair and rework can replace an answer after the
				// earlier quality pass. Re-apply user-authored confirmation
				// coverage at the last mutation point so a later rewrite
				// cannot silently drop one of the offered choices.
				output = ensureAIEmployeeExplicitConfirmationCoverage(
					pipeline.AIEmployeeOriginalTask,
					output,
				)
			}
		}
		if err == nil {
			exec.mu.Lock()
			e.ensurePipelineCheckpointLocked(exec)
			exec.Checkpoint.Phase = pipelineCheckpointPhasePostProcessing
			exec.Checkpoint.TurnOutput = output
			exec.Checkpoint.TurnStatus = "success"
			exec.Checkpoint.UpdatedAt = time.Now()
			exec.mu.Unlock()
			e.saveToDB(exec)
		}

		investmentQualityFallback := ""
		investmentQualityFallbackReady := false
		if err != nil {
			investmentQualityFallback, investmentQualityFallbackReady = e.hostFallbackInvestmentQualityGate(exec, step, stepInput)
		}

		exec.mu.Lock()
		if err != nil {
			var reportedProfessionalFailure *professionalAgentReportedFailure
			hardProfessionalFailure := errors.As(err, &reportedProfessionalFailure)
			failedDetail := ""
			if step.ProfessionalAgent != nil && strings.TrimSpace(output) != "" {
				failedDetail = boundProfessionalAgentText(
					stripProfessionalAgentStatusMarker(output),
					professionalAgentStepOutputMaxBytes,
				)
			}
			if investmentQualityFallbackReady && i < len(steps)-1 && ctx.Err() == nil {
				exec.Steps[i].Output = investmentQualityFallback
				exec.Steps[i].Status = "degraded"
				// This is an expected, fully handled degradation. Keeping Error empty
				// prevents an internal model diagnostic from leaking into the user-facing
				// AI employee summary while the degraded status remains observable.
				exec.Steps[i].Error = ""
				exec.mu.Unlock()
				e.saveStepOutputToFile(exec, i, investmentQualityFallback)
				e.saveToDB(exec)
				e.fileLogger.Printf("[Pipeline] Step %d (%s) used deterministic host fallback after synthesis failure: %v", i, step.Name, err)
				e.broadcastPipelineEvent(exec.ID, "step_done", map[string]interface{}{
					"step_index": i, "step_name": step.Name, "degraded": true,
					"reason": "quality_gate_host_fallback",
				})
				i++
				continue
			}
			if step.ContinueOnError && !hardProfessionalFailure && i < len(steps)-1 && ctx.Err() == nil {
				recoverableOutput := buildRecoverableStepFailureOutput(step, err)
				exec.Steps[i].Output = recoverableOutput
				exec.Steps[i].Status = "degraded"
				exec.Steps[i].Error = err.Error()
				exec.Steps[i].SlotStatus = pipelineSlotFilled
				exec.Steps[i].SlotValidation = "degraded: " + err.Error()
				exec.mu.Unlock()
				e.saveStepOutputToFile(exec, i, recoverableOutput)
				e.saveToDB(exec)
				e.fileLogger.Printf("[Pipeline] Step %d (%s) failed but continue_on_error=true: %v", i, step.Name, err)
				e.broadcastPipelineEvent(exec.ID, "step_done", map[string]interface{}{
					"step_index":        i,
					"step_name":         step.Name,
					"degraded":          true,
					"reason":            "continue_on_error",
					"recoverable_error": err.Error(),
				})
				i++
				continue
			}
			// Check for fallback step
			fallbackStep := step.FallbackStepName()
			unlockedForFallback := false

			if fallbackStep != "" && !hardProfessionalFailure {
				e.fileLogger.Printf("[Pipeline] Step %d (%s) failed, falling back to: %s", i, step.Name, fallbackStep)
				exec.Steps[i].Status = "failed_fallback"
				exec.Steps[i].Error = err.Error()
				exec.mu.Unlock()
				unlockedForFallback = true
				e.broadcastPipelineEvent(exec.ID, "step_failed", map[string]interface{}{
					"step_index": i, "step_name": step.Name, "error": err.Error(), "fallback": fallbackStep,
				})
				// Find and execute fallback step
				for j := range exec.Steps {
					if exec.Steps[j].Name == fallbackStep {
						// Execute fallback step directly
						fallbackOut, fallbackErr := e.executeStep(ctx, exec, step, j,
							"## 降级处理\n原步骤 "+step.Name+" 失败: "+err.Error()+"\n\n请执行降级处理方案。", sessionID)
						if fallbackErr == nil {
							exec.mu.Lock()
							exec.Steps[j].Output = fallbackOut
							exec.Steps[j].Status = "completed"
							exec.Status = "completed"
							exec.mu.Unlock()
							e.saveToDB(exec)
							return
						}
						e.fileLogger.Printf("[Pipeline] Fallback step also failed: %v", fallbackErr)
					}
				}
			}

			if unlockedForFallback {
				exec.mu.Lock()
			}
			exec.Steps[i].Status = "failed"
			exec.Steps[i].Error = err.Error()
			exec.Steps[i].SlotStatus = pipelineSlotInvalid
			exec.Steps[i].SlotValidation = err.Error()
			if failedDetail != "" {
				exec.Steps[i].Output = failedDetail
			}
			toolCallID := ""
			if calls := exec.Steps[i].ToolCalls; len(calls) > 0 {
				toolCallID = calls[len(calls)-1].CallID
			}
			if ctx.Err() != nil {
				exec.Status = "stopped"
			} else {
				exec.Status = "failed"
			}
			exec.Steps[i].SlotFill = buildPipelineSlotFillRecord(step, exec.Steps[i])
			exec.mu.Unlock()
			if failedDetail != "" {
				e.saveStepOutputToFile(exec, i, failedDetail)
			}
			e.saveToDB(exec)
			e.fileLogger.Printf("[Pipeline] Step %d (%s) failed after %d retries: %v", i, step.Name, maxStepRetries, err)
			failureEvent := map[string]interface{}{
				"step_index": i, "step_name": step.Name, "error": err.Error(),
			}
			if failedDetail != "" {
				failureEvent["detail"] = textutil.TruncateUTF8Bytes(failedDetail, professionalAgentProgressMaxBytes)
			}
			if toolCallID != "" {
				failureEvent["tool_call_id"] = toolCallID
			}
			e.broadcastPipelineEvent(exec.ID, "step_failed", failureEvent)
			// Notify supervisor if configured
			if step.EscalateTo != "" {
				cfg, _ := e.gateway.aeManager.GetEmployee(step.EscalateTo)
				if cfg != nil {
					go e.gateway.aeManager.notifySupervisor(cfg, fmt.Sprintf("Pipeline %s 步骤 %s 失败: %v", exec.PipelineName, step.Name, err))
				}
			}
			return
		}
		rawOutput := output
		iterationDegraded := isIterationLimitFallbackOutput(rawOutput)
		professionalStatus := professionalAgentOutputStatus(rawOutput)
		professionalDegraded := professionalStatus == "degraded" || professionalStatus == "unavailable"
		// A validated, execution-scoped brief is the actual contract consumed by
		// the visual stage. If the model wrote that brief but omitted its final
		// narration, host finalization is successful recovery, not a degraded
		// business result. Keep the recovery observable in logs and the step event
		// without poisoning the whole pipeline status.
		degraded := iterationDegraded || reworkOutcome.Degraded || professionalDegraded || outputContractDegraded || pptPolishFallbackDegraded || unexpectedMissingInputSuppressed
		degradedReason := reworkOutcome.DegradedReason
		if iterationDegraded {
			output = buildDegradedStepOutput(step, rawOutput)
			degradedReason = "iteration_budget_degraded"
			e.fileLogger.Printf("[Pipeline] Step %d (%s) reached iteration budget; continuing with degraded output", i, step.Name)
		} else {
			output = stripPipelineControlMarkers(rawOutput)
		}
		output = stripProfessionalAgentStatusMarker(output)
		if professionalDegraded && degradedReason == "" {
			degradedReason = "professional_agent_" + professionalStatus
		}
		if pptPolishFallbackDegraded && degradedReason == "" {
			degradedReason = "ppt_polish_base_ppt_fallback"
		}
		if unexpectedMissingInputSuppressed && degradedReason == "" {
			degradedReason = "unexpected_missing_input_stop_suppressed"
		}
		if outputContractDegraded {
			if degradedReason != "" {
				degradedReason += "; "
			}
			degradedReason += outputContractReason
		}
		if i == len(steps)-1 {
			output = applyPipelineDeliveryConstraint(output, stepInput)
			output = ensureAIEmployeeExplicitConfirmationCoverage(
				pipeline.AIEmployeeOriginalTask,
				output,
			)
		}
		exec.Steps[i].Output = output
		exec.Steps[i].Status = "completed"
		exec.Steps[i].SlotStatus = pipelineSlotFilled
		exec.Steps[i].SlotValidation = "passed"
		if sourceSnapshot != nil {
			exec.Steps[i].SourceSnapshot = sourceSnapshot
			e.fileLogger.Printf("[Pipeline] Step %d (%s) froze source: %s", i, step.Name, pipelineSourceSnapshotSummary(sourceSnapshot))
		}
		if degraded {
			exec.Steps[i].Status = "degraded"
			exec.Steps[i].Error = degradedReason
			exec.Steps[i].SlotValidation = "degraded: " + degradedReason
		}
		exec.Steps[i].SlotFill = buildPipelineSlotFillRecord(step, exec.Steps[i])
		e.ensurePipelineCheckpointLocked(exec)
		exec.Checkpoint.Phase = pipelineCheckpointPhaseStepFinalized
		exec.Checkpoint.StepIndex = i
		exec.Checkpoint.StepName = step.Name
		exec.Checkpoint.StepRevision = exec.Steps[i].Revision
		exec.Checkpoint.TurnOutput = output
		exec.Checkpoint.TurnStatus = exec.Steps[i].Status
		exec.Checkpoint.UpdatedAt = time.Now()
		exec.mu.Unlock()
		// Persist step output to file so subsequent steps can read_file it
		e.saveStepOutputToFile(exec, i, output)
		e.materializeClaimedArtifacts(exec, i, output)

		stepDonePayload := map[string]interface{}{
			"step_index": i, "step_name": step.Name,
		}
		if degraded {
			stepDonePayload["degraded"] = true
			stepDonePayload["reason"] = degradedReason
		}
		if pptPolishBriefHostFinalized {
			stepDonePayload["host_finalized"] = "validated_ppt_content_brief"
		}
		e.broadcastPipelineEvent(exec.ID, "step_done", stepDonePayload)
		if !step.RequireApproval && e.pausePipelineAtBoundary(exec, i+1) {
			return
		}

		// Missing-input is a terminal AI employee state, regardless of whether an
		// individual business step declared an explicit route. This prevents a
		// downstream step from running after a decisive gap and prevents the host
		// from treating the user's next ordinary message as an implicit resume.
		if aiEmployeeOutputRequiresFreshInvocation(rawOutput, pipeline) {
			completionCode, completionMessage := pipelineControlStopReason(rawOutput)
			exec.mu.Lock()
			exec.Steps[i].SlotStatus = pipelineSlotMissing
			exec.Steps[i].SlotValidation = "waiting_for_decisive_user_input"
			exec.Steps[i].SlotFill = buildPipelineSlotFillRecord(step, exec.Steps[i])
			exec.Status = "completed"
			exec.CompletionReason = &PipelineCompletionReason{
				Code:      completionCode,
				StepIndex: i,
				StepName:  step.Name,
				Condition: "AI employee decisive input missing",
				Target:    "__stop__",
				Message:   completionMessage,
			}
			exec.mu.Unlock()
			e.saveToDB(exec)
			break runLoop
		}

		// ========== HITL checkpoint ==========
		if step.RequireApproval {
			exec.mu.Lock()
			exec.Status = "waiting_approval"
			exec.Steps[i].Status = "waiting_approval"
			e.ensurePipelineCheckpointLocked(exec)
			exec.Checkpoint.Phase = pipelineCheckpointPhaseWaitingApproval
			exec.Checkpoint.UpdatedAt = time.Now()
			exec.mu.Unlock()

			e.saveToDB(exec)

			e.broadcastPipelineEvent(exec.ID, "waiting_approval", map[string]interface{}{
				"step_index": i, "step_name": step.Name, "output": truncate(output, 500),
			})

			e.fileLogger.Printf("[Pipeline] Step %d (%s) waiting for approval (mode=%s)", i, step.Name, mode)

			if mode == RunModeAPI {
				e.sendNotifyWebhook(exec, i, output)
				exec.mu.Lock()
				exec.running = false
				exec.mu.Unlock()
				return
			}

			feedback := e.waitForApproval(ctx, exec, step)
			action := "approved"
			if feedback == "" {
				action = "stopped"
				exec.mu.Lock()
				exec.Status = "stopped"
				exec.mu.Unlock()
				exec.recordApproval(step.Name, action, "timeout or cancelled")
				e.saveToDB(exec)
				return
			}
			if feedback != "ok" {
				action = "feedback"
			}
			exec.recordApproval(step.Name, action, feedback)

			exec.mu.Lock()
			exec.Steps[i].Status = "running"
			exec.Status = "running"
			output = exec.Steps[i].Output // restore output value after lock
			exec.mu.Unlock()

			if feedback != "ok" {
				prompt = prompt + "\n\n## User Feedback\n" + feedback + "\n\nPlease adjust your approach based on this feedback."
				output2, err2 := e.executeStep(ctx, exec, step, i, prompt, sessionID)
				exec.mu.Lock()
				if err2 != nil {
					exec.Steps[i].Status = "failed"
					exec.Steps[i].Error = err2.Error()
					exec.Status = "failed"
					exec.mu.Unlock()
					e.saveToDB(exec)
					return
				}
				exec.Steps[i].Output = output2
				exec.Steps[i].Status = "completed"
				output = output2
				exec.mu.Unlock()
			}

			e.broadcastPipelineEvent(exec.ID, "step_done", map[string]interface{}{
				"step_index": i, "step_name": step.Name, "rerun": feedback != "ok",
			})
		}

		// ========== Repeat/loop check ==========
		if step.Repeat != nil && matchCondition(output, step.Repeat.Condition) {
			repeatCount[i]++
			if repeatCount[i] < step.Repeat.Max {
				e.fileLogger.Printf("[Pipeline] Step %d (%s) loop %d/%d: output matches '%s'", i, step.Name, repeatCount[i], step.Repeat.Max, step.Repeat.Condition)
				e.broadcastPipelineEvent(exec.ID, "step_retry", map[string]interface{}{
					"step_index": i, "step_name": step.Name, "attempt": repeatCount[i] + 1, "max": step.Repeat.Max,
				})
				exec.mu.Lock()
				exec.Steps[i].Status = "running"
				exec.Steps[i].Revision++
				e.ensurePipelineCheckpointLocked(exec)
				exec.Checkpoint.StepRevision = exec.Steps[i].Revision
				exec.Checkpoint.Phase = pipelineCheckpointPhaseStepPending
				exec.Checkpoint.RepeatCounts = pipelineRepeatCountsToStrings(repeatCount)
				exec.Checkpoint.UpdatedAt = time.Now()
				exec.mu.Unlock()
				e.saveToDB(exec)
				continue // re-execute same step
			}
			// Max exceeded
			e.fileLogger.Printf("[Pipeline] Step %d (%s) max loops (%d) exceeded", i, step.Name, step.Repeat.Max)
			if step.Repeat.OnMax != "" {
				target := findStepIndex(steps, step.Repeat.OnMax)
				if target >= 0 {
					i = target
					continue
				}
			}
		}

		// ========== Conditional routing ==========
		if len(step.Routing) > 0 {
			nextI := -1
			stopPipeline := false
			stopCondition := ""
			for _, route := range step.Routing {
				if matchRouteCondition(exec, pipeline.Name, i, rawOutput, route) {
					if route.Goto == "__stop__" {
						stopPipeline = true
						stopCondition = route.When
						e.fileLogger.Printf("[Pipeline] Step %d (%s) requested normal stop: '%s'", i, step.Name, route.When)
						break
					}
					nextI = findStepIndex(steps, route.Goto)
					e.fileLogger.Printf("[Pipeline] Step %d (%s) routed: '%s' → goto '%s' (index=%d)", i, step.Name, route.When, route.Goto, nextI)
					break
				}
			}
			if stopPipeline {
				completionCode, completionMessage := pipelineControlStopReason(rawOutput)
				exec.mu.Lock()
				if completionCode == aeWaitingInputStatus {
					exec.Steps[i].SlotStatus = pipelineSlotMissing
					exec.Steps[i].SlotValidation = "waiting_for_decisive_user_input"
					exec.Steps[i].SlotFill = buildPipelineSlotFillRecord(step, exec.Steps[i])
				}
				exec.Status = "completed"
				exec.CompletionReason = &PipelineCompletionReason{
					Code:      completionCode,
					StepIndex: i,
					StepName:  step.Name,
					Condition: stopCondition,
					Target:    "__stop__",
					Message:   completionMessage,
				}
				exec.mu.Unlock()
				e.saveToDB(exec)
				break runLoop
			}
			if nextI >= 0 {
				i = nextI
				routedHere = true
				routeEntryPoint = nextI
			} else {
				i++ // no route matched, fall through
			}
		} else {
			i++
		}

		e.saveToDB(exec)
	}

	// Only mark completed if we naturally exited the loop (not stopped/failed)
	exec.mu.Lock()
	if exec.Status == "running" || exec.Status == "recovering" {
		if err := e.validateFastPipelineArtifacts(exec); err != nil {
			exec.Status = "failed"
			if len(exec.Steps) > 0 {
				last := len(exec.Steps) - 1
				exec.Steps[last].Status = "failed"
				exec.Steps[last].Error = err.Error()
			}
		} else if pipelineHasDegradedSteps(exec.Steps) {
			exec.Status = "degraded"
		} else {
			exec.Status = "completed"
		}
	}
	if exec.Status == "completed" || exec.Status == "degraded" || exec.Status == "failed" || exec.Status == "stopped" {
		e.ensurePipelineCheckpointLocked(exec)
		exec.Checkpoint.Phase = pipelineCheckpointPhaseCompleted
		exec.Checkpoint.ActiveTurnKey = ""
		exec.Checkpoint.UpdatedAt = time.Now()
	}
	exec.mu.Unlock()
	e.saveToDB(exec)

	e.fileLogger.Printf("[Pipeline] %s finished: status=%s", pipeline.Name, exec.Status)
	e.broadcastPipelineEvent(exec.ID, "completed", map[string]interface{}{
		"total_steps": len(steps), "status": exec.Status,
	})

	// Record for self-evolution
	if e.gateway != nil && e.gateway.evolver != nil {
		e.gateway.evolver.Record(pipelineEvolutionSnapshot(exec))
	}

	if mode == RunModeAPI && exec.NotifyURL != "" {
		e.sendCompletionWebhook(exec)
	}
}

func (e *PipelineExecutor) hostAcquireDescribedSource(ctx context.Context, exec *PipelineExecution, step *PipelineStep, stepIndex int, input string) (string, bool, error) {
	if e == nil || e.gateway == nil || e.gateway.config == nil || exec == nil || step == nil ||
		!pipelineSourceContractApplies(step, input) || extractChatroomSourceDescription(input) == "" {
		return "", false, nil
	}
	description := extractChatroomSourceDescription(input)
	frozenPath := pipelineFrozenSourcePath(exec, step.SourceContract)
	workspace := e.pipelineExecutionWorkspace(exec)
	params := map[string]interface{}{
		"description": description,
		"max_results": 1,
		"max_pages":   5,
		"output_path": frozenPath,
	}
	paramsJSON, _ := json.Marshal(params)
	started := time.Now()
	result, err := knowledgetools.NewSourceAcquireTool(workspace, knowledgetools.NewNovaChatroomSearchTool(workspace)).Execute(ctx, params)
	record := ToolCallRecord{
		Timestamp:  started,
		ToolName:   "source_acquire",
		Params:     string(paramsJSON),
		Result:     result,
		DurationMs: time.Since(started).Milliseconds(),
	}
	if err != nil {
		record.Error = err.Error()
	}
	exec.recordToolCall(stepIndex, record)
	if err != nil {
		return "", false, fmt.Errorf("host source acquisition failed: %w", err)
	}
	if ToolResultLooksFailedForTool("source_acquire", result) {
		return "", false, fmt.Errorf("host source acquisition failed: %s", truncate(result, 600))
	}
	fullPath := frozenPath
	if !filepath.IsAbs(fullPath) {
		fullPath = filepath.Join(workspace, filepath.FromSlash(fullPath))
	}
	data, err := os.ReadFile(fullPath)
	if err != nil {
		return "", false, fmt.Errorf("host source acquisition did not create frozen source: %w", err)
	}
	minBytes := step.SourceContract.MinBytes
	if minBytes <= 0 {
		minBytes = 256
	}
	if len(data) < minBytes {
		return "", false, fmt.Errorf("host frozen source too small: %d bytes", len(data))
	}
	return fmt.Sprintf("## Host-validated frozen source\nSOURCE_FILE: %s\nSOURCE_ACQUIRE_RECEIPT: %s\n\n%s", frozenPath, result, string(data)), true, nil
}

func (e *PipelineExecutor) hostReadPPTXMaterial(exec *PipelineExecution, step *PipelineStep, stepIndex int, input string) (string, bool, error) {
	if e == nil || e.gateway == nil || e.gateway.config == nil || exec == nil || step == nil ||
		exec.PipelineName != "ppt-polish" || step.Name != "素材读取" || stepIndex < 0 || stepIndex >= len(exec.Steps) {
		return "", false, nil
	}
	workspace := e.pipelineExecutionWorkspace(exec)
	var sourcePath string
	for _, mention := range extractLocalFileMentions(input) {
		if !strings.EqualFold(filepath.Ext(mention), ".pptx") {
			continue
		}
		candidate := mention
		if !filepath.IsAbs(candidate) {
			candidate = filepath.Join(workspace, filepath.FromSlash(candidate))
		}
		candidate = filepath.Clean(candidate)
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			sourcePath = candidate
			break
		}
	}
	if sourcePath == "" {
		return e.hostDescribePPTPolishRequestMaterial(exec, stepIndex, input)
	}

	started := time.Now()
	text, extractErr := contenttools.ExtractPPTX(sourcePath)
	structure, inspectErr := contenttools.InspectPPTXStructure(sourcePath)
	info, statErr := os.Stat(sourcePath)
	resultForAudit := text
	if extractErr != nil {
		resultForAudit = extractErr.Error()
	}
	paramsJSON, _ := json.Marshal(map[string]interface{}{"file": sourcePath, "mode": "host-pptx-material-read"})
	record := ToolCallRecord{
		Timestamp:  started,
		ToolName:   "extract",
		Params:     truncate(string(paramsJSON), 500),
		Result:     truncate(resultForAudit, 2000),
		DurationMs: time.Since(started).Milliseconds(),
	}
	if extractErr != nil {
		record.Error = extractErr.Error()
	}
	exec.recordToolCall(stepIndex, record)
	exec.mu.Lock()
	exec.Steps[stepIndex].DurationMs = record.DurationMs
	exec.mu.Unlock()
	if extractErr != nil {
		return "", false, fmt.Errorf("host PPTX extraction failed: %w", extractErr)
	}
	if inspectErr != nil {
		return "", false, fmt.Errorf("host PPTX inspection failed: %w", inspectErr)
	}
	if statErr != nil {
		return "", false, fmt.Errorf("host PPTX stat failed: %w", statErr)
	}

	format := "unknown"
	if structure.WidthEMU > 0 && structure.HeightEMU > 0 {
		ratio := float64(structure.WidthEMU) / float64(structure.HeightEMU)
		format = fmt.Sprintf("%.3f:1", ratio)
		if ratio > 1.76 && ratio < 1.79 {
			format += " (16:9)"
		}
	}
	output := fmt.Sprintf(`## PPTX 素材宿主解析

- 素材路径：%s
- 文件大小：%d bytes
- 现有页数：%d
- 页面比例：%s
- 可编辑结构摘要：文本形状 %d，图片 %d，表格 %d，图形框/图表容器 %d
- 主题与受众：以逐页文字和用户要求为准；未明确部分由内容规划阶段给出可覆盖假设，不额外编造产品或业务事实。
- 主要问题与润色方向：按用户要求优化信息层级、留白、配色一致性、表格与图表可读性；保留原稿事实、数字和决策口径。具体视觉问题在新稿生成与渲染验收时核对。

## 逐页可编辑文字

%s`, sourcePath, info.Size(), structure.SlideCount, format, structure.TextShapeCount, structure.PictureCount, structure.TableCount, structure.GraphicFrameCount, textutil.TruncateUTF8Bytes(strings.TrimSpace(text), 24000))
	return output, true, nil
}

func (e *PipelineExecutor) hostDescribePPTPolishRequestMaterial(
	exec *PipelineExecution,
	stepIndex int,
	input string,
) (string, bool, error) {
	if exec == nil || stepIndex < 0 || stepIndex >= len(exec.Steps) {
		return "", false, nil
	}
	request := strings.TrimSpace(exec.Input)
	if request == "" {
		request = strings.TrimSpace(input)
	}
	if len([]rune(request)) < 12 || pptPolishRequestNeedsExternalMaterial(request) {
		return "", false, nil
	}
	if requestedPPTXFilename(request) == "" &&
		requestedPresentationMinimumSlides(request) == 0 &&
		!presentationContainsAny(strings.ToLower(request), "ppt", "pptx", "presentation", "演示", "幻灯片") {
		return "", false, nil
	}

	slides := parsePPTPolishRequestedSlides(request)
	mode := "concept_only"
	if len(slides) >= 4 || requestedPresentationMinimumSlides(request) > 0 ||
		(pptPolishRequestField(request, "主题") != "" && pptPolishRequestField(request, "受众", "目标受众") != "") {
		mode = "detailed_brief"
	}
	theme := pptPolishRequestField(request, "主题", "标题")
	if theme == "" {
		theme = textutil.TruncateUTF8Bytes(strings.Join(strings.Fields(request), " "), 180)
	}
	audience := pptPolishRequestField(request, "受众", "目标受众")
	if audience == "" {
		audience = "普通业务读者（宿主中性默认，可由用户覆盖）"
	}
	pagePlan := "根据内容密度自适应"
	minimum := requestedPresentationMinimumSlides(request)
	maximum := requestedPresentationMaximumSlides(request)
	if minimum > 0 && maximum == minimum {
		pagePlan = fmt.Sprintf("固定 %d 页", minimum)
	} else if minimum > 0 && maximum > minimum {
		pagePlan = fmt.Sprintf("%d–%d 页", minimum, maximum)
	} else if len(slides) > 0 {
		pagePlan = fmt.Sprintf("按用户逐页要求制作 %d 页", len(slides))
	}
	output := fmt.Sprintf(`## PPT 用户需求宿主素材

- PPT_INPUT_MODE: %s
- 源素材真实路径：无外部文件；本次可信素材仅来自当前用户请求（execution_id=%s）
- 主题：%s
- 目标受众：%s
- 页数或内容结构：%s
- 结构问题：不是已有旧稿诊断；后续必须按用户给出的叙事顺序和逐页要求组织内容。
- 版式问题：不是已有旧稿诊断；后续必须落实用户明确的页面构图、字号和留白要求。
- 信息密度问题：以用户给出的每页事实为上限，不补造案例、市场数字或 ROI。
- 视觉一致性问题：不是已有旧稿诊断；后续统一配色、标题层级、图表样式和页脚系统。
- 润色方向：把当前请求转换为可编辑、可渲染、可逐页验收的演示文稿；不得把内部流程字段写到观众页面。
- 可信素材与来源：用户当前请求；未读取外部网页或其他任务文件。
- 合理默认假设：缺失的普通设计参数采用中性、清晰、专业基线；不得冒充用户明示。
- 事实/推断/未知边界：用户明确给出的文字和数字视为本次制作事实；其余业务事实保持未知。

## 用户当前请求（逐字保留）

%s`, mode, sanitizeFilename(exec.ID), theme, audience, pagePlan, request)
	return output, true, nil
}

func pptPolishRequestNeedsExternalMaterial(request string) bool {
	if regexp.MustCompile(`(?i)https?://`).MatchString(request) {
		return true
	}
	requestedOutput := strings.ToLower(filepath.Base(requestedPPTXFilename(request)))
	for _, mention := range extractLocalFileMentions(request) {
		ext := strings.ToLower(filepath.Ext(mention))
		if ext == "" {
			continue
		}
		if ext == ".pptx" && requestedOutput != "" && strings.EqualFold(filepath.Base(mention), requestedOutput) {
			continue
		}
		switch ext {
		case ".ppt", ".pptx", ".pdf", ".doc", ".docx", ".md", ".txt", ".rtf", ".csv", ".xls", ".xlsx", ".png", ".jpg", ".jpeg", ".webp":
			// Whether present or missing, a user-named source artifact must be
			// handled by the material-reading model/tool path. Treating its name
			// as prose would silently discard evidence or conceal a bad path.
			return true
		}
	}
	return false
}

func pptPolishRequestField(request string, labels ...string) string {
	for _, label := range labels {
		pattern := regexp.MustCompile(`(?m)^\s*` + regexp.QuoteMeta(label) + `\s*[:：]\s*([^\r\n]+)`)
		if match := pattern.FindStringSubmatch(request); len(match) == 2 {
			return strings.TrimSpace(match[1])
		}
	}
	return ""
}

type pptPolishRequestedSlide struct {
	Number int
	Title  string
	Body   string
}

func parsePPTPolishRequestedSlides(request string) []pptPolishRequestedSlide {
	pattern := regexp.MustCompile(`(?m)^\s*(\d{1,2})[.．、]\s*([^\r\n]+)`)
	matches := pattern.FindAllStringSubmatch(request, -1)
	best := []pptPolishRequestedSlide(nil)
	for start := range matches {
		if matches[start][1] != "1" {
			continue
		}
		candidate := make([]pptPolishRequestedSlide, 0, len(matches)-start)
		expected := 1
		for _, match := range matches[start:] {
			number, err := strconv.Atoi(match[1])
			if err != nil || number != expected {
				break
			}
			line := strings.TrimSpace(match[2])
			title := line
			body := ""
			if split := strings.IndexAny(line, ":："); split > 0 {
				title = strings.TrimSpace(line[:split])
				body = strings.TrimSpace(line[split+1:])
			}
			if title == "" {
				title = fmt.Sprintf("第 %d 页", number)
			}
			candidate = append(candidate, pptPolishRequestedSlide{Number: number, Title: title, Body: body})
			expected++
		}
		if len(candidate) > len(best) {
			best = candidate
		}
	}
	return best
}

func (e *PipelineExecutor) hostRunPPTPolishStep(ctx context.Context, exec *PipelineExecution, step *PipelineStep, stepIndex int, input string) (string, bool, error) {
	if exec == nil || step == nil || exec.PipelineName != "ppt-polish" {
		return "", false, nil
	}
	switch step.Name {
	case "素材读取":
		return e.hostReadPPTXMaterial(exec, step, stepIndex, input)
	case "内容重构":
		return e.hostWritePPTPolishBrief(exec, stepIndex, input)
	case "视觉润色":
		// Visual design belongs to the model-selected presentation Skill. The
		// host only prepares source evidence and validates the delivered deck.
		return "", false, nil
	case "交付自查":
		return e.hostValidatePPTPolish(ctx, exec, stepIndex, input)
	default:
		return "", false, nil
	}
}

func shouldRecoverPPTPolishVisualMissingOutput(
	exec *PipelineExecution,
	step *PipelineStep,
	output string,
	pipeline Pipeline,
) bool {
	return exec != nil &&
		step != nil &&
		exec.PipelineName == "ppt-polish" &&
		strings.TrimSpace(step.Name) == "视觉润色" &&
		pipeline.Name == "ppt-polish" &&
		strings.Contains(output, pipelineWaitingInputMarker) &&
		strings.Contains(output, pipelineStopMarker)
}

// hostMaterializePPTPolishBriefFromStepOutput reconciles a structured content
// plan returned in the model's final answer with the deterministic artifact
// path owned by this execution. This is intentionally narrow:
//   - only the ppt-polish content-planning step is eligible;
//   - only the current execution's exact path is written;
//   - the output must parse into at least four audience pages;
//   - no workspace scan or cross-run artifact reuse is permitted.
//
// This covers providers that return the full brief after a rejected absolute
// write path, while keeping the tenant filesystem boundary strict.
func (e *PipelineExecutor) hostMaterializePPTPolishBriefFromStepOutput(
	exec *PipelineExecution,
	step *PipelineStep,
	stepIndex int,
	output string,
) (string, bool, error) {
	if exec == nil ||
		step == nil ||
		exec.PipelineName != "ppt-polish" ||
		strings.TrimSpace(step.Name) != "内容重构" ||
		stepIndex < 0 ||
		stepIndex >= len(exec.Steps) {
		return "", false, nil
	}
	workspace := e.pipelineExecutionWorkspace(exec)
	briefPath := filepath.Clean(filepath.Join(
		workspace,
		".openkitty",
		"storage",
		"ppt_polish_brief_"+sanitizeFilename(exec.ID)+".md",
	))
	if info, statErr := os.Stat(briefPath); statErr == nil && info.Mode().IsRegular() && info.Size() >= 512 {
		if data, readErr := os.ReadFile(briefPath); readErr == nil {
			if _, validateErr := buildPPTPolishFallbackMarkdown(string(data)); validateErr == nil {
				return "", false, nil
			}
		}
	}

	body := strings.TrimSpace(Clean(middlewarecore.StripToolCalls(output)))
	if len([]byte(body)) < 512 || pptPolishAudiencePageCount(body) < 4 {
		return "", false, nil
	}
	if _, validateErr := buildPPTPolishFallbackMarkdown(body); validateErr != nil {
		return "", true, fmt.Errorf("structured PPT content brief validation failed: %w", validateErr)
	}
	if err := os.MkdirAll(filepath.Dir(briefPath), 0755); err != nil {
		return "", true, fmt.Errorf("create PPT content brief directory: %w", err)
	}
	started := time.Now()
	if err := os.WriteFile(briefPath, []byte(body+"\n"), 0644); err != nil {
		return "", true, fmt.Errorf("materialize PPT content brief: %w", err)
	}
	e.recordHostPipelineToolCall(exec, stepIndex, started, "write_file", map[string]interface{}{
		"path": briefPath, "reconciled_from": "validated_step_output",
	}, fmt.Sprintf("wrote %d bytes", len([]byte(body))+1))
	return fmt.Sprintf(
		"内容重构已完成。\n- Markdown 制作简报：%s\n- 规划页数：%d\n- 验证状态：宿主已将本步骤的结构化内容对账到本次执行的安全路径。",
		briefPath,
		pptPolishAudiencePageCount(body),
	), true, nil
}

// hostRecoverPPTPolishBriefAfterModelFailure turns a successfully written,
// execution-scoped content brief into a valid step result when the model spends
// its final iteration on verification or returns no usable closing sentence.
// Recovery is deliberately path-exact: it never scans the workspace or borrows
// another execution's artifact.
func (e *PipelineExecutor) hostRecoverPPTPolishBriefAfterModelFailure(
	exec *PipelineExecution,
	step *PipelineStep,
	stepIndex int,
	cause error,
) (string, bool, error) {
	if exec == nil ||
		step == nil ||
		exec.PipelineName != "ppt-polish" ||
		strings.TrimSpace(step.Name) != "内容重构" ||
		stepIndex < 0 ||
		stepIndex >= len(exec.Steps) ||
		cause == nil {
		return "", false, nil
	}
	workspace := e.pipelineExecutionWorkspace(exec)
	briefPath := filepath.Clean(filepath.Join(
		workspace,
		".openkitty",
		"storage",
		"ppt_polish_brief_"+sanitizeFilename(exec.ID)+".md",
	))
	info, err := os.Stat(briefPath)
	if err != nil {
		return "", true, fmt.Errorf("PPT content brief is missing at the execution-scoped path: %w", err)
	}
	if !info.Mode().IsRegular() || info.Size() < 512 {
		return "", true, fmt.Errorf("PPT content brief is not a meaningful regular file")
	}
	briefData, err := os.ReadFile(briefPath)
	if err != nil {
		return "", true, fmt.Errorf("read PPT content brief: %w", err)
	}
	if _, err := buildPPTPolishFallbackMarkdown(string(briefData)); err != nil {
		return "", true, fmt.Errorf("PPT content brief validation failed: %w", err)
	}
	pageCount := pptPolishAudiencePageCount(string(briefData))
	return fmt.Sprintf(
		"内容重构已完成。\n- Markdown 制作简报：%s\n- 规划页数：%d\n- 验证状态：宿主已确认文件存在、内容完整且可进入视觉制作。",
		briefPath,
		pageCount,
	), true, nil
}

const pptPolishHostBaseFallbackRoute = "host-base-ppt-fallback"

func pptPolishOutputUsesHostBaseFallback(output string) bool {
	return strings.Contains(output, "生产路线："+pptPolishHostBaseFallbackRoute)
}

// hostRecoverExistingPPTPolishVisual preserves a model-produced artifact at
// the exact execution-owned output path. A fallback must never overwrite such
// a file: doing so can silently replace a high-fidelity deck with a different
// page count and make earlier render/vision evidence refer to the wrong bytes.
func (e *PipelineExecutor) hostRecoverExistingPPTPolishVisual(
	ctx context.Context,
	exec *PipelineExecution,
	stepIndex int,
	target string,
	cause error,
) (string, bool, error) {
	info, statErr := os.Stat(target)
	if os.IsNotExist(statErr) {
		return "", false, nil
	}
	if statErr != nil {
		return "", true, fmt.Errorf("inspect existing model PPTX: %w", statErr)
	}
	if info.IsDir() || info.Size() < 10*1024 {
		return "", true, fmt.Errorf(
			"existing model PPTX is incomplete at %s (%d bytes); refusing to overwrite it with a different fallback deck",
			target,
			info.Size(),
		)
	}
	if err := contenttools.ValidateOfficeOpenXMLPackage(target); err != nil {
		return "", true, fmt.Errorf("existing model PPTX failed OOXML validation; refusing fallback overwrite: %w", err)
	}
	if err := contenttools.ValidatePPTXIntegerGeometry(target); err != nil {
		return "", true, fmt.Errorf("existing model PPTX failed geometry validation; refusing fallback overwrite: %w", err)
	}
	structure, inspectErr := contenttools.InspectPPTXStructure(target)
	if inspectErr != nil {
		return "", true, fmt.Errorf("existing model PPTX failed structure validation; refusing fallback overwrite: %w", inspectErr)
	}
	requestInput := ""
	if exec != nil {
		requestInput = exec.Input
	}
	if err := validatePPTPolishRequestedFilename(requestInput, target); err != nil {
		return "", true, fmt.Errorf("existing model PPTX cannot be substituted by fallback: %w", err)
	}
	if err := validatePPTPolishRequestedSlideCount(requestInput, structure.SlideCount); err != nil {
		return "", true, fmt.Errorf("existing model PPTX cannot be substituted by fallback: %w", err)
	}
	visualObjectCount := structure.TextShapeCount + structure.PictureCount + structure.GraphicFrameCount + structure.TableCount
	if structure.SlideCount < 4 || visualObjectCount == 0 {
		return "", true, fmt.Errorf(
			"existing model PPTX failed structure validation (slides=%d visual_objects=%d); refusing fallback overwrite",
			structure.SlideCount,
			visualObjectCount,
		)
	}
	audienceText, textErr := contenttools.ExtractPPTX(target)
	if textErr != nil {
		return "", true, fmt.Errorf("existing model PPTX failed audience-content extraction; refusing fallback overwrite: %w", textErr)
	}
	if violations := pptPolishAudienceContentViolations(audienceText); len(violations) > 0 {
		return "", true, fmt.Errorf(
			"existing model PPTX exposes production metadata (%s); refusing fallback overwrite",
			strings.Join(violations, ", "),
		)
	}
	if err := validatePPTPolishRequestedTheme(requestInput, target); err != nil {
		return "", true, fmt.Errorf("existing model PPTX failed requested-theme validation; refusing fallback overwrite: %w", err)
	}
	if visualErr := validatePPTPolishVisualEvidence(exec, stepIndex, structure.SlideCount); visualErr != nil {
		// A visual-production agent can spend its final turn on a targeted
		// correction and return no usable closing sentence even though it has
		// already produced a structurally valid deck. Do not lower the visual
		// gate and do not replace those bytes with the base fallback. When the
		// configured delivery reviewer owns bounded visual rework, preserve
		// the deck as a pending candidate so that reviewer can issue the normal
		// structured retry. The delivery step still has to reject the candidate
		// if the fresh run-scoped render and every-slide vision evidence are not
		// supplied on that revision.
		if pptPolishVisualCanDeferToDeliveryRework(exec, stepIndex) {
			started := time.Now()
			e.recordHostPipelineToolCall(exec, stepIndex, started, "pptx_preserve_pending_visual_rework", map[string]interface{}{
				"path": target, "reason": visualErr.Error(), "fallback_overwrite": false,
			}, fmt.Sprintf("preserved bytes=%d slides=%d pending_delivery_visual_rework=true", info.Size(), structure.SlideCount))
			if cause != nil && e.fileLogger != nil {
				e.fileLogger.Printf("[Pipeline] Preserved existing model PPTX for bounded delivery visual rework after incomplete model finalization: %v", cause)
			}
			return fmt.Sprintf(
				"PPT 视觉候选产物已保留，等待交付门禁完成有界复核。\n- PPTX真实路径：%s\n- 生产路线：model-produced-pptx-preserved-pending-visual-rework\n- 页面数：%d\n- 文件大小：%d bytes\n- 视觉证据状态：未放行；交付自查必须触发配置范围内的有界视觉返工。\n- 阻断原因：%s",
				target,
				structure.SlideCount,
				info.Size(),
				visualErr.Error(),
			), true, nil
		}
		return "", true, fmt.Errorf("existing model PPTX is preserved but its visual QA is incomplete: %w", visualErr)
	}
	if cause != nil && e.fileLogger != nil {
		e.fileLogger.Printf("[Pipeline] Preserved existing model PPTX after incomplete model finalization: %v", cause)
	}
	started := time.Now()
	e.recordHostPipelineToolCall(exec, stepIndex, started, "pptx_preserve", map[string]interface{}{
		"path": target, "fallback_overwrite": false,
	}, fmt.Sprintf("preserved bytes=%d slides=%d", info.Size(), structure.SlideCount))
	return fmt.Sprintf(
		"PPT 视觉制作已完成。\n- PPTX真实路径：%s\n- 生产路线：model-produced-pptx-preserved\n- 可编辑性：%t\n- 页面数：%d\n- 文件大小：%d bytes\n- 校验状态：模型产物已通过结构、整数 EMU、用户页数及逐页视觉证据校验；宿主未覆盖文件。",
		target,
		structure.TextShapeCount > 0,
		structure.SlideCount,
		info.Size(),
	), true, nil
}

// hostReconcilePPTPolishAlternateOutput handles one narrowly defined model
// mistake: the user requested an explicit filename, but the model wrote the
// documented current-run generic filename. It never scans storage. The exact
// alternate is validated first, then materialized without overwriting either
// file so the model bytes and diagnostic artifact remain recoverable.
func (e *PipelineExecutor) hostReconcilePPTPolishAlternateOutput(
	exec *PipelineExecution,
	stepIndex int,
	workspace string,
	target string,
) (bool, error) {
	if exec == nil || requestedPPTXFilename(exec.Input) == "" || strings.TrimSpace(exec.ID) == "" {
		return false, nil
	}
	alternate := filepath.Clean(filepath.Join(
		workspace,
		".openkitty",
		"storage",
		"ppt_polished_"+sanitizeFilename(exec.ID)+".pptx",
	))
	if sameLocalPath(alternate, target) {
		return false, nil
	}
	info, statErr := os.Stat(alternate)
	if os.IsNotExist(statErr) {
		return false, nil
	}
	if statErr != nil {
		return true, fmt.Errorf("inspect current-run alternate PPTX: %w", statErr)
	}
	if !info.Mode().IsRegular() || info.Size() < 10*1024 {
		return true, fmt.Errorf("current-run alternate PPTX is incomplete at %s", alternate)
	}
	if err := contenttools.ValidateOfficeOpenXMLPackage(alternate); err != nil {
		return true, fmt.Errorf("current-run alternate PPTX failed OOXML validation: %w", err)
	}
	if err := contenttools.ValidatePPTXIntegerGeometry(alternate); err != nil {
		return true, fmt.Errorf("current-run alternate PPTX failed geometry validation: %w", err)
	}
	structure, err := contenttools.InspectPPTXStructure(alternate)
	if err != nil {
		return true, fmt.Errorf("current-run alternate PPTX failed structure validation: %w", err)
	}
	if err := validatePPTPolishRequestedSlideCount(exec.Input, structure.SlideCount); err != nil {
		return true, fmt.Errorf("current-run alternate PPTX cannot satisfy the request: %w", err)
	}
	visualObjectCount := structure.TextShapeCount + structure.PictureCount + structure.GraphicFrameCount + structure.TableCount
	if structure.SlideCount < 4 || visualObjectCount == 0 {
		return true, fmt.Errorf(
			"current-run alternate PPTX failed structure validation (slides=%d visual_objects=%d)",
			structure.SlideCount,
			visualObjectCount,
		)
	}
	audienceText, err := contenttools.ExtractPPTX(alternate)
	if err != nil {
		return true, fmt.Errorf("current-run alternate PPTX failed audience-content extraction: %w", err)
	}
	if violations := pptPolishAudienceContentViolations(audienceText); len(violations) > 0 {
		return true, fmt.Errorf("current-run alternate PPTX exposes production metadata (%s)", strings.Join(violations, ", "))
	}
	if err := validatePPTPolishRequestedTheme(exec.Input, alternate); err != nil {
		return true, fmt.Errorf("current-run alternate PPTX failed requested-theme validation: %w", err)
	}
	if err := validatePPTPolishVisualEvidence(exec, stepIndex, structure.SlideCount); err != nil {
		return true, fmt.Errorf("current-run alternate PPTX visual QA is incomplete: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return true, err
	}
	if err := os.Link(alternate, target); err != nil {
		if copyErr := copyFileExclusive(alternate, target, info.Mode().Perm()); copyErr != nil {
			return true, fmt.Errorf("materialize explicitly requested PPTX filename: hardlink=%v copy=%w", err, copyErr)
		}
	}
	e.recordHostPipelineToolCall(exec, stepIndex, time.Now(), "pptx_reconcile_filename", map[string]interface{}{
		"source":    alternate,
		"target":    target,
		"overwrite": false,
	}, fmt.Sprintf("reconciled bytes=%d slides=%d", info.Size(), structure.SlideCount))
	return true, nil
}

func copyFileExclusive(source, target string, mode os.FileMode) (err error) {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	complete := false
	defer func() {
		closeErr := output.Close()
		if closeErr != nil {
			complete = false
			if err == nil {
				err = closeErr
			}
		}
		if !complete {
			_ = os.Remove(target)
		}
	}()
	if _, err = io.Copy(output, input); err != nil {
		return err
	}
	if err = output.Sync(); err != nil {
		return err
	}
	complete = true
	return nil
}

func (e *PipelineExecutor) hostFallbackPPTPolishVisual(ctx context.Context, exec *PipelineExecution, step *PipelineStep, stepIndex int, cause error) (string, bool, error) {
	if exec == nil || step == nil || exec.PipelineName != "ppt-polish" || strings.TrimSpace(step.Name) != "视觉润色" || stepIndex < 0 || stepIndex >= len(exec.Steps) {
		return "", false, nil
	}
	workspace := e.pptPolishWorkspace(ctx, exec)
	target := pptPolishDefaultOutputPath(exec, workspace)
	if recoveredOutput, found, recoveredErr := e.hostRecoverExistingPPTPolishVisual(ctx, exec, stepIndex, target, cause); found {
		return recoveredOutput, true, recoveredErr
	}
	if found, reconcileErr := e.hostReconcilePPTPolishAlternateOutput(exec, stepIndex, workspace, target); found {
		if reconcileErr != nil {
			return "", true, reconcileErr
		}
		return e.hostRecoverExistingPPTPolishVisual(ctx, exec, stepIndex, target, cause)
	}
	contentIndex := pipelineStepResultIndex(exec, "内容重构")
	if contentIndex < 0 || contentIndex >= stepIndex {
		return "", true, fmt.Errorf("PPT fallback cannot find the completed content brief step")
	}
	briefPath := firstExistingWorkspacePathWithExtension(exec.Steps[contentIndex].Output, ".md", workspace)
	if briefPath == "" {
		// The content contract defines one deterministic brief path for each
		// execution. Use only that exact path if the model created the file but
		// formatted or omitted its path in the final response. Never scan the
		// workspace or borrow another run's brief.
		expectedBrief := filepath.Join(
			workspace,
			".openkitty",
			"storage",
			"ppt_polish_brief_"+sanitizeFilename(exec.ID)+".md",
		)
		if info, statErr := os.Stat(expectedBrief); statErr == nil && !info.IsDir() {
			briefPath = filepath.Clean(expectedBrief)
		}
	}
	if briefPath == "" {
		return "", true, fmt.Errorf("PPT fallback cannot find the generated content brief for this execution")
	}
	briefData, err := os.ReadFile(briefPath)
	if err != nil {
		return "", true, fmt.Errorf("read PPT fallback brief: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
		return "", true, err
	}
	// The model-selected premium/native route failed. Convert only the
	// audience-facing Page sections of the production brief. Rendering the
	// entire brief would leak routing metadata, layout instructions, speaker
	// guidance and fallback rules into the final presentation.
	markdown, contentErr := buildPPTPolishFallbackMarkdown(string(briefData))
	if contentErr != nil {
		return "", true, contentErr
	}
	plannedPages := pptPolishAudiencePageCount(string(briefData))
	if plannedPages >= 8 || requestedPresentationMinimumSlides(exec.Input) >= 8 {
		return "", true, fmt.Errorf(
			"host base-ppt fallback cannot satisfy contact-sheet plus every-slide vision QA for an %d-page formal deck; no model artifact exists, so refusing a silent quality downgrade",
			plannedPages,
		)
	}
	theme := selectPPTPolishFallbackTheme(exec.Input, string(briefData))
	markdown = applyPPTPolishFallbackTheme(markdown, theme)
	started := time.Now()
	result, convertErr := NewConvertTool(workspace).Execute(ctx, map[string]interface{}{
		"markdown":  markdown,
		"output":    target,
		"generator": "base-ppt",
	})
	if convertErr != nil {
		result = convertErr.Error()
	}
	e.recordHostPipelineToolCall(exec, stepIndex, started, "convert", map[string]interface{}{
		"input": briefPath, "output": target, "generator": "base-ppt", "theme": theme, "fallback": true,
	}, result)
	if convertErr != nil {
		return "", true, fmt.Errorf("base-ppt fallback failed: %w", convertErr)
	}
	info, statErr := os.Stat(target)
	if statErr != nil || info.IsDir() || info.Size() < 10*1024 {
		return "", true, fmt.Errorf("base-ppt fallback did not create a valid PPTX: %s", target)
	}
	structure, inspectErr := contenttools.InspectPPTXStructure(target)
	if inspectErr != nil || structure.SlideCount < 4 {
		if inspectErr != nil {
			return "", true, inspectErr
		}
		return "", true, fmt.Errorf("base-ppt fallback created only %d slides", structure.SlideCount)
	}
	if cause != nil && e.fileLogger != nil {
		e.fileLogger.Printf("[Pipeline] PPT visual host fallback cause: %v", cause)
	}
	return fmt.Sprintf("PPT 视觉制作已完成。\n- PPTX真实路径：%s\n- 生产路线：%s\n- 风格：%s\n- 可编辑性：true\n- 页面数：%d\n- 文件大小：%d bytes\n- 校验状态：文件与PPTX结构已通过；下一步继续执行独立交付审计。", target, pptPolishHostBaseFallbackRoute, pptPolishFallbackThemeLabel(theme), structure.SlideCount, info.Size()), true, nil
}

func pptPolishDefaultOutputPath(exec *PipelineExecution, workspace string) string {
	return filepath.Clean(filepath.Join(workspace, filepath.FromSlash(pptPolishCurrentRunOutputRelativePath(exec))))
}

var requestedPPTXFilenamePattern = regexp.MustCompile(`(?i)(?:文件名(?:称)?(?:\s*(?:为|是))?|命名为|保存为|另存为|output\s+filename|file\s*name|filename|save\s+as)\s*[:：=]?\s*["'“”]?([^"'“”\r\n<>|?*\\/]+?\.pptx)\b`)

func requestedPPTXFilename(input string) string {
	match := requestedPPTXFilenamePattern.FindStringSubmatch(input)
	if len(match) != 2 {
		return ""
	}
	name := strings.Trim(strings.TrimSpace(match[1]), "`'\"“” ")
	if name == "" || !strings.EqualFold(filepath.Ext(name), ".pptx") || filepath.Base(name) != name {
		return ""
	}
	return name
}

func validatePPTPolishRequestedFilename(input, target string) error {
	requested := requestedPPTXFilename(input)
	if requested == "" {
		return nil
	}
	actual := filepath.Base(filepath.Clean(target))
	if !strings.EqualFold(actual, requested) {
		return fmt.Errorf("PPTX delivery filename mismatch: got %s, user explicitly requested %s", actual, requested)
	}
	return nil
}

func validatePPTPolishRequestedSlideCount(input string, slideCount int) error {
	minimum := requestedPresentationMinimumSlides(input)
	maximum := requestedPresentationMaximumSlides(input)
	if minimum > 0 && slideCount < minimum {
		return fmt.Errorf("PPTX delivery page count mismatch: got %d slides, user requested at least %d", slideCount, minimum)
	}
	if maximum > 0 && slideCount > maximum {
		return fmt.Errorf("PPTX delivery page count mismatch: got %d slides, user requested at most %d", slideCount, maximum)
	}
	return nil
}

var (
	pptPolishDirectRequiredQuotePattern = regexp.MustCompile(`(?:副标题|右侧结论|结论|核心观点|主标题|标题)\s*(?:为|是)?\s*[:：]?\s*“([^”\r\n]{2,120})”`)
	pptPolishSeriesRequiredQuotePattern = regexp.MustCompile(`(?:分别是|分别为)\s*((?:“[^”\r\n]{2,120}”[\s、，,；;]*){2,8})`)
	pptPolishAnySmartQuotePattern       = regexp.MustCompile(`“([^”\r\n]{2,120})”`)
	pptPolishArrowChainPattern          = regexp.MustCompile(`[:：]\s*([^。\r\n，,；;]{1,160}(?:→[^。\r\n，,；;]{1,80}){1,8})`)
	pptPolishContainsListPattern        = regexp.MustCompile(`(?:包含|包括|涵盖)\s*([^。；;\r\n]{2,240})`)
	pptPolishColumnsListPattern         = regexp.MustCompile(`(?:列为|列是|列名为)\s*([^。；;，,\r\n]{2,160})`)
	pptPolishNumericClaimPattern        = regexp.MustCompile(`(?i)([≤≥<>~≈]?\s*\d+(?:\.\d+)?(?:\s*[/／]\s*\d+(?:\.\d+)?){0,5})\s*(百分点|分钟|小时|人日|人天|人民币|万元|亿元|美元|minutes?|mins?|hours?|hrs?|days?|weeks?|months?|%|％|天|周|月|元)`)
)

func pptPolishRequiredAudiencePhrases(input string) []string {
	seen := make(map[string]bool)
	phrases := make([]string, 0, 24)
	appendPhrase := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			return
		}
		seen[value] = true
		phrases = append(phrases, value)
	}
	for _, match := range pptPolishDirectRequiredQuotePattern.FindAllStringSubmatch(input, -1) {
		if len(match) == 2 {
			appendPhrase(match[1])
		}
	}
	for _, series := range pptPolishSeriesRequiredQuotePattern.FindAllStringSubmatch(input, -1) {
		if len(series) != 2 {
			continue
		}
		for _, match := range pptPolishAnySmartQuotePattern.FindAllStringSubmatch(series[1], -1) {
			if len(match) == 2 {
				appendPhrase(match[1])
			}
		}
	}
	for _, chain := range pptPolishArrowChainPattern.FindAllStringSubmatch(input, -1) {
		if len(chain) != 2 {
			continue
		}
		for _, label := range strings.Split(chain[1], "→") {
			appendPhrase(strings.Trim(strings.TrimSpace(label), "'\"“”‘’（）()[]【】 "))
		}
	}
	appendDelimitedList := func(value string, separators *regexp.Regexp) {
		parts := separators.Split(value, -1)
		if len(parts) < 2 {
			return
		}
		for _, part := range parts {
			part = strings.Trim(strings.TrimSpace(part), "'\"“”‘’（）()[]【】 ")
			if runes := []rune(part); len(runes) >= 2 && len(runes) <= 48 {
				appendPhrase(part)
			}
		}
	}
	for _, match := range pptPolishContainsListPattern.FindAllStringSubmatch(input, -1) {
		if len(match) == 2 {
			appendDelimitedList(match[1], regexp.MustCompile(`[、，,]\s*`))
		}
	}
	for _, match := range pptPolishColumnsListPattern.FindAllStringSubmatch(input, -1) {
		if len(match) == 2 {
			appendDelimitedList(match[1], regexp.MustCompile(`[/／、，,]\s*`))
		}
	}
	return phrases
}

func normalizePPTPolishRequiredPhrase(value string) string {
	value = strings.Join(strings.Fields(value), "")
	return strings.NewReplacer(
		",", "，",
		":", "：",
		";", "；",
		"!", "！",
		"?", "？",
	).Replace(value)
}

func validatePPTPolishRequiredAudienceText(input, audienceText string) error {
	visible := normalizePPTPolishRequiredPhrase(audienceText)
	var missing []string
	for _, phrase := range pptPolishRequiredAudiencePhrases(input) {
		if !strings.Contains(visible, normalizePPTPolishRequiredPhrase(phrase)) {
			missing = append(missing, phrase)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return fmt.Errorf(
		"PPTX content fidelity failed: explicitly required audience text is missing or paraphrased (%s); render these phrases visibly and verbatim rather than leaving them only in notes or metadata",
		strings.Join(missing, "；"),
	)
}

func normalizePPTPolishNumericUnit(unit string) string {
	unit = strings.ToLower(strings.TrimSpace(unit))
	switch unit {
	case "％":
		return "%"
	case "minute", "minutes", "min", "mins":
		return "分钟"
	case "hour", "hours", "hr", "hrs":
		return "小时"
	case "day", "days":
		return "天"
	case "week", "weeks":
		return "周"
	case "month", "months":
		return "月"
	default:
		return unit
	}
}

func pptPolishNumericClaims(value string) []string {
	seen := make(map[string]bool)
	claims := make([]string, 0, 12)
	for _, match := range pptPolishNumericClaimPattern.FindAllStringSubmatch(value, -1) {
		if len(match) != 3 {
			continue
		}
		numberSeries := strings.Join(strings.Fields(match[1]), "")
		unit := normalizePPTPolishNumericUnit(match[2])
		prefix := ""
		for _, candidate := range []string{"≤", "≥", "<", ">", "~", "≈"} {
			if strings.HasPrefix(numberSeries, candidate) {
				prefix = candidate
				numberSeries = strings.TrimPrefix(numberSeries, candidate)
				break
			}
		}
		for _, number := range strings.FieldsFunc(numberSeries, func(r rune) bool { return r == '/' || r == '／' }) {
			claim := prefix + strings.TrimSpace(number) + unit
			if claim == unit || seen[claim] {
				continue
			}
			seen[claim] = true
			claims = append(claims, claim)
		}
	}
	return claims
}

func pptPolishUsesClosedDetailedBrief(exec *PipelineExecution) bool {
	materialIndex := pipelineStepResultIndex(exec, "素材读取")
	if exec == nil || materialIndex < 0 || materialIndex >= len(exec.Steps) {
		return false
	}
	material := exec.Steps[materialIndex].Output
	return strings.Contains(material, "PPT_INPUT_MODE: detailed_brief") &&
		strings.Contains(material, "本次可信素材仅来自当前用户请求") &&
		strings.Contains(material, "未读取外部网页")
}

func validatePPTPolishDetailedBriefNumericClaims(exec *PipelineExecution, requestInput, audienceText string) error {
	if !pptPolishUsesClosedDetailedBrief(exec) {
		return nil
	}
	allowed := make(map[string]bool)
	for _, claim := range pptPolishNumericClaims(requestInput) {
		allowed[claim] = true
	}
	var unsupported []string
	normalizedRequest := strings.Join(strings.Fields(requestInput), "")
	for _, claim := range pptPolishNumericClaims(audienceText) {
		if allowed[claim] {
			continue
		}
		// Phase labels such as 30天/60天/90天 are structural chronology, not
		// invented KPI values. The grouped request form can evade the numeric
		// tokenizer, so accept only an exact day token already present in input.
		if strings.HasSuffix(claim, "天") && strings.Contains(normalizedRequest, claim) {
			continue
		}
		unsupported = append(unsupported, claim)
	}
	if len(unsupported) == 0 {
		return nil
	}
	return fmt.Errorf(
		"PPTX numeric provenance failed: closed detailed brief contains unsupported visible numeric claims (%s); use only values present in the user request, otherwise display 未提供/待确认/未统计",
		strings.Join(unsupported, "；"),
	)
}

func validatePPTPolishRequiredNumericClaims(exec *PipelineExecution, requestInput, audienceText string) error {
	if !pptPolishUsesClosedDetailedBrief(exec) {
		return nil
	}
	visible := make(map[string]bool)
	for _, claim := range pptPolishNumericClaims(audienceText) {
		visible[claim] = true
	}
	var missing []string
	for _, claim := range pptPolishNumericClaims(requestInput) {
		if !visible[claim] {
			missing = append(missing, claim)
		}
	}
	missing = textutil.UniqueStrings(missing)
	if len(missing) == 0 {
		return nil
	}
	return fmt.Errorf(
		"PPTX numeric fidelity failed: user-supplied visible numeric claims are missing (%s); preserve supplied baselines, targets and units instead of deleting them while removing unsupported values",
		strings.Join(missing, "；"),
	)
}

var pptPolishBareTableNumberPattern = regexp.MustCompile(`^\s*([≤≥<>~≈]?\s*\d+(?:\.\d+)?)\s*$`)

func pptPolishSpecValueText(value interface{}) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	case json.Number:
		return typed.String()
	default:
		return strings.TrimSpace(fmt.Sprint(value))
	}
}

func pptPolishUnknownValue(value string) bool {
	return presentationContainsAny(strings.ToLower(strings.TrimSpace(value)),
		"未提供", "待确认", "未统计", "未知", "n/a", "na", "tbd",
	)
}

func pptPolishMetricImpliedUnit(metric string) string {
	lower := strings.ToLower(strings.Join(strings.Fields(metric), ""))
	switch {
	case presentationContainsAny(lower, "率", "比例", "占比", "差异", "percent", "percentage"):
		return "%"
	case presentationContainsAny(lower, "耗时", "时长", "分钟", "p95", "latency", "duration"):
		return "分钟"
	case presentationContainsAny(lower, "金额", "成本", "报价", "预算", "人民币", "元"):
		return "元"
	default:
		return ""
	}
}

func pptPolishRequestDirectlySuppliesMetricNumber(requestInput, metric, number string) bool {
	metric = strings.TrimSpace(metric)
	if metric == "" {
		return false
	}
	var phrase strings.Builder
	for _, r := range []rune(metric) {
		if unicode.IsSpace(r) {
			continue
		}
		if phrase.Len() > 0 {
			phrase.WriteString(`\s*`)
		}
		phrase.WriteString(regexp.QuoteMeta(string(r)))
	}
	pattern, err := regexp.Compile(phrase.String() + `[^，,；;。\r\n、]{0,24}([≤≥<>~≈]?\s*\d+(?:\.\d+)?)`)
	if err != nil {
		return false
	}
	normalizedNumber := strings.Join(strings.Fields(number), "")
	for _, match := range pattern.FindAllStringSubmatch(requestInput, -1) {
		if len(match) == 2 && strings.Join(strings.Fields(match[1]), "") == normalizedNumber {
			return true
		}
	}
	return false
}

func pptPolishRequestDirectlySuppliesResponsibility(requestInput, metric, owner string) bool {
	metric = normalizePPTPolishRequiredPhrase(metric)
	owner = normalizePPTPolishRequiredPhrase(owner)
	if metric == "" || owner == "" {
		return false
	}
	for _, clause := range strings.FieldsFunc(requestInput, func(r rune) bool {
		return r == '\n' || r == '\r' || r == '。' || r == '；' || r == ';'
	}) {
		clause = normalizePPTPolishRequiredPhrase(clause)
		if !strings.Contains(clause, metric) || !strings.Contains(clause, owner) {
			continue
		}
		if presentationContainsAny(strings.ToLower(clause), "责任", "负责", "owner", "由") {
			return true
		}
	}
	return false
}

func validatePPTPolishDeckSpecGrounding(exec *PipelineExecution, requestInput, workspace string) error {
	if !pptPolishUsesClosedDetailedBrief(exec) || exec == nil || strings.TrimSpace(workspace) == "" {
		return nil
	}
	specPath := filepath.Join(workspace, filepath.FromSlash(pptPolishCurrentRunWorkRelativePath(exec)), "deck_spec.json")
	data, err := os.ReadFile(specPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("PPTX deck-spec grounding could not read %s: %w", specPath, err)
	}
	var spec map[string]interface{}
	if err := json.Unmarshal(data, &spec); err != nil {
		return fmt.Errorf("PPTX deck-spec grounding could not parse %s: %w", specPath, err)
	}
	var issues []string
	if err := validatePPTPolishAudienceGrounding(exec, requestInput, string(data)); err != nil {
		issues = append(issues, err.Error())
	}
	slides, _ := spec["slides"].([]interface{})
	minimum := requestedPresentationMinimumSlides(requestInput)
	maximum := requestedPresentationMaximumSlides(requestInput)
	if minimum > 0 && len(slides) < minimum {
		issues = append(issues, fmt.Sprintf("deck specification contains %d slides, fewer than the user-required minimum %d; cover counts toward the total", len(slides), minimum))
	}
	if maximum > 0 && len(slides) > maximum {
		issues = append(issues, fmt.Sprintf("deck specification contains %d slides, more than the user-allowed maximum %d; cover counts toward the total", len(slides), maximum))
	}
	allowedClaims := make(map[string]bool)
	for _, claim := range pptPolishNumericClaims(requestInput) {
		allowedClaims[claim] = true
	}
	var unsupported []string
	var unsupportedOwners []string
	var missingDecisions []string
	checkImpliedClaim := func(metric, field, value string) {
		if value == "" || pptPolishUnknownValue(value) {
			return
		}
		match := pptPolishBareTableNumberPattern.FindStringSubmatch(value)
		if len(match) != 2 {
			return
		}
		number := strings.Join(strings.Fields(match[1]), "")
		unit := pptPolishMetricImpliedUnit(metric)
		if unit != "" {
			claims := pptPolishNumericClaims(number + unit)
			if len(claims) == 1 && allowedClaims[claims[0]] {
				return
			}
		} else if pptPolishRequestDirectlySuppliesMetricNumber(requestInput, metric, number) {
			return
		}
		unsupported = append(unsupported, fmt.Sprintf("%s/%s=%s", metric, field, value))
	}
	decisionLabels := []string{"优先级", "验收口径", "试点团队"}
	decisionRequired := strings.Contains(requestInput, "需确认") &&
		strings.Contains(requestInput, decisionLabels[0]) && strings.Contains(requestInput, decisionLabels[1]) && strings.Contains(requestInput, decisionLabels[2])
	decisionSeen := make(map[string]bool)
	for _, rawSlide := range slides {
		slide, _ := rawSlide.(map[string]interface{})
		kind := strings.ToLower(pptPolishSpecValueText(slide["kind"]))
		if kind == "table" {
			headers, _ := slide["headers"].([]interface{})
			if len(headers) == 0 {
				headers, _ = slide["columns"].([]interface{})
			}
			rows, _ := slide["rows"].([]interface{})
			for _, rawRow := range rows {
				row, _ := rawRow.([]interface{})
				if len(row) < 2 {
					continue
				}
				metric := pptPolishSpecValueText(row[0])
				for column := 1; column < len(row); column++ {
					header := ""
					if column < len(headers) {
						header = pptPolishSpecValueText(headers[column])
					}
					if presentationContainsAny(strings.ToLower(header), "责任", "owner") {
						value := pptPolishSpecValueText(row[column])
						if value != "" && !pptPolishUnknownValue(value) && !pptPolishRequestDirectlySuppliesResponsibility(requestInput, metric, value) {
							unsupportedOwners = append(unsupportedOwners, fmt.Sprintf("%s/%s=%s", metric, header, value))
						}
						continue
					}
					value := pptPolishSpecValueText(row[column])
					checkImpliedClaim(metric, header, value)
				}
			}
		}
		if kind == "metrics" {
			metricItems, _ := slide["items"].([]interface{})
			if len(metricItems) == 0 {
				metricItems, _ = slide["metrics"].([]interface{})
			}
			for offset, rawItem := range metricItems {
				item, _ := rawItem.(map[string]interface{})
				checkImpliedClaim(
					pptPolishSpecValueText(item["label"]),
					fmt.Sprintf("items[%d].value", offset+1),
					pptPolishSpecValueText(item["value"]),
				)
			}
		}
		if decisionRequired && kind == "closing" {
			items, _ := slide["items"].([]interface{})
			for _, rawItem := range items {
				item, _ := rawItem.(map[string]interface{})
				title := pptPolishSpecValueText(item["title"])
				body := pptPolishSpecValueText(item["body"])
				for _, label := range decisionLabels {
					if !strings.Contains(title, label) {
						continue
					}
					decisionSeen[label] = true
					if !presentationContainsAny(body, "待确认", "请管理层确认", "由管理层确认", "管理层需确认", "管理层确认后") {
						missingDecisions = append(missingDecisions, label)
					}
				}
			}
		}
	}
	var inspectCharts func(interface{})
	inspectCharts = func(value interface{}) {
		switch typed := value.(type) {
		case map[string]interface{}:
			categories, categoriesOK := typed["categories"].([]interface{})
			series, seriesOK := typed["series"].([]interface{})
			if categoriesOK && seriesOK {
				for seriesIndex, rawSeries := range series {
					seriesMap, _ := rawSeries.(map[string]interface{})
					values, _ := seriesMap["values"].([]interface{})
					labels, _ := seriesMap["labels"].([]interface{})
					for valueIndex, rawValue := range values {
						metric := ""
						if valueIndex < len(categories) {
							metric = pptPolishSpecValueText(categories[valueIndex])
						}
						if valueIndex < len(labels) && pptPolishSpecValueText(labels[valueIndex]) != "" {
							metric = pptPolishSpecValueText(labels[valueIndex])
						}
						checkImpliedClaim(metric, fmt.Sprintf("series[%d].values[%d]", seriesIndex+1, valueIndex+1), pptPolishSpecValueText(rawValue))
					}
				}
			}
			for _, nested := range typed {
				inspectCharts(nested)
			}
		case []interface{}:
			for _, nested := range typed {
				inspectCharts(nested)
			}
		}
	}
	inspectCharts(spec)
	if decisionRequired {
		for _, label := range decisionLabels {
			if !decisionSeen[label] {
				missingDecisions = append(missingDecisions, label)
			}
		}
	}
	if len(unsupported) > 0 {
		issues = append(issues, "unitless or implied-unit spec claims lack user provenance ("+strings.Join(unsupported, "；")+")")
	}
	if len(unsupportedOwners) > 0 {
		issues = append(issues, "responsibility/owner assignments lack explicit user provenance; use 待确认 unless the request assigns them ("+strings.Join(unsupportedOwners, "；")+")")
	}
	if len(missingDecisions) > 0 {
		issues = append(issues, "management decisions requested as unknown must remain visibly 待确认 ("+strings.Join(textutil.UniqueStrings(missingDecisions), "；")+")")
	}
	if len(issues) == 0 {
		return nil
	}
	return fmt.Errorf("PPTX deck-spec grounding failed: %s", strings.Join(issues, " | "))
}

func validatePPTPolishFinalGrounding(exec *PipelineExecution, requestInput, audienceText, workspace string) error {
	var issues []string
	if err := validatePPTPolishAudienceGrounding(exec, requestInput, audienceText); err != nil {
		issues = append(issues, err.Error())
	}
	if err := validatePPTPolishDeckSpecGrounding(exec, requestInput, workspace); err != nil {
		issues = append(issues, err.Error())
	}
	if len(issues) == 0 {
		return nil
	}
	return fmt.Errorf("PPTX audience grounding failed: %s", strings.Join(issues, " | "))
}

func validatePPTPolishAudienceGrounding(exec *PipelineExecution, requestInput, audienceText string) error {
	var issues []string
	if err := validatePPTPolishRequiredAudienceText(requestInput, audienceText); err != nil {
		issues = append(issues, err.Error())
	}
	if err := validatePPTPolishDetailedBriefNumericClaims(exec, requestInput, audienceText); err != nil {
		issues = append(issues, err.Error())
	}
	if err := validatePPTPolishRequiredNumericClaims(exec, requestInput, audienceText); err != nil {
		issues = append(issues, err.Error())
	}
	if len(issues) == 0 {
		return nil
	}
	return fmt.Errorf("PPTX audience grounding failed: %s", strings.Join(issues, " | "))
}

func (e *PipelineExecutor) pptPolishWorkspace(ctx context.Context, exec *PipelineExecution) string {
	if workspace := strings.TrimSpace(projectctx.ResolvedWorkspace(ctx)); workspace != "" {
		return workspace
	}
	return e.pipelineExecutionWorkspace(exec)
}

func (e *PipelineExecutor) pipelineExecutionWorkspace(exec *PipelineExecution) string {
	if exec != nil {
		if workspace := strings.TrimSpace(exec.ResolvedWorkspace); workspace != "" {
			return projectctx.NormalizeWorkspace(workspace)
		}
	}
	if e != nil && e.gateway != nil && e.gateway.config != nil {
		if workspace := strings.TrimSpace(e.gateway.config.Workspace); workspace != "" {
			return projectctx.NormalizeWorkspace(workspace)
		}
	}
	return projectctx.NormalizeWorkspace(".")
}

func (e *PipelineExecutor) hostWritePPTPolishBrief(exec *PipelineExecution, stepIndex int, input string) (string, bool, error) {
	materialIndex := pipelineStepResultIndex(exec, "素材读取")
	if exec == nil || stepIndex < 0 || stepIndex >= len(exec.Steps) || materialIndex < 0 || materialIndex >= stepIndex || strings.TrimSpace(exec.Steps[materialIndex].Output) == "" {
		return "", false, nil
	}
	materialOutput := strings.TrimSpace(exec.Steps[materialIndex].Output)
	existingDeckMaterial := strings.Contains(materialOutput, "## PPTX 素材宿主解析")
	detailedRequestMaterial := strings.Contains(materialOutput, "## PPT 用户需求宿主素材")
	if !existingDeckMaterial && !detailedRequestMaterial {
		return "", false, nil
	}
	workspace := e.pipelineExecutionWorkspace(exec)
	briefName := "ppt_polish_brief_" + sanitizeFilename(exec.ID) + ".md"
	briefPath := filepath.Join(workspace, ".openkitty", "storage", briefName)
	if err := os.MkdirAll(filepath.Dir(briefPath), 0755); err != nil {
		return "", false, err
	}
	brief := ""
	plannedPages := 0
	completionNote := ""
	if detailedRequestMaterial {
		request := strings.TrimSpace(exec.Input)
		if request == "" {
			request = strings.TrimSpace(input)
		}
		var ready bool
		brief, plannedPages, ready = buildPPTPolishDetailedRequestBrief(request)
		if !ready {
			return "", false, nil
		}
		completionNote = "宿主已把用户逐页要求逐字对账为本次执行的结构化制作简报；未使用外部来源或补造业务事实。"
	} else {
		material := materialOutput
		if marker := strings.Index(material, "## 逐页可编辑文字"); marker >= 0 {
			material = strings.TrimSpace(material[marker+len("## 逐页可编辑文字"):])
		}
		brief = buildPPTPolishPreserveBrief(material)
		plannedPages = pptPolishSourceSlideCount(exec)
		completionNote = "逐页保留源稿事实和数字，仅优化层级与视觉表达。"
	}
	started := time.Now()
	if err := os.WriteFile(briefPath, []byte(brief), 0644); err != nil {
		return "", false, err
	}
	e.recordHostPipelineToolCall(exec, stepIndex, started, "write_file", map[string]interface{}{"path": briefPath}, fmt.Sprintf("wrote %d bytes", len([]byte(brief))))
	return fmt.Sprintf("内容重构已由宿主保真完成。\n- Markdown 制作简报：%s\n- 规划页数：%d\n- 口径：%s", briefPath, plannedPages, completionNote), true, nil
}

func buildPPTPolishDetailedRequestBrief(request string) (string, int, bool) {
	slides := parsePPTPolishRequestedSlides(request)
	if len(slides) < 4 {
		return "", 0, false
	}
	minimum := requestedPresentationMinimumSlides(request)
	maximum := requestedPresentationMaximumSlides(request)
	if minimum > 0 && len(slides) < minimum {
		return "", 0, false
	}
	if maximum > 0 && len(slides) > maximum {
		return "", 0, false
	}
	theme := pptPolishRequestField(request, "主题", "标题")
	if theme == "" {
		theme = slides[0].Title
	}
	audience := pptPolishRequestField(request, "受众", "目标受众")
	if audience == "" {
		audience = "普通业务读者（宿主中性默认，可由用户覆盖）"
	}
	visual := pptPolishRequestField(request, "视觉", "风格")
	if visual == "" {
		visual = "清晰、克制、易读；使用原生可编辑元素"
	}

	var out strings.Builder
	out.WriteString("<!-- OPENKITTY_PPT_POLISH_REQUEST_BRIEF: current user facts only -->\n\n")
	out.WriteString("# " + theme + "\n\n")
	out.WriteString("- 输入模式：detailed_brief\n")
	out.WriteString("- 目标受众：" + audience + "\n")
	out.WriteString("- 视觉方向：" + visual + "\n")
	out.WriteString(fmt.Sprintf("- 页数：固定 %d 页；封面计入总页数\n", len(slides)))
	out.WriteString("- 事实来源：仅限用户当前请求；没有外部引用，不补造案例、市场数据或 ROI\n")
	out.WriteString("- 待确认项：用户未明确的普通设计参数使用中性专业默认值，不阻断制作\n\n")
	for _, slide := range slides {
		claim := strings.TrimSpace(slide.Body)
		if claim == "" {
			claim = slide.Title
		}
		visualSuggestion := "使用原生可编辑形状建立清晰层级，并按本页内容选择最简洁的构图。"
		if presentationContainsAny(strings.ToLower(claim), "图", "表", "流程", "路线", "架构", "对比", "矩阵", "timeline", "chart", "table") {
			visualSuggestion = "严格落实用户在本页原句中指定的图、表、流程或构图要求，所有元素保持原生可编辑。"
		}
		out.WriteString(fmt.Sprintf("### Slide %02d — %s\n\n", slide.Number, slide.Title))
		out.WriteString("| 字段 | 内容 |\n| --- | --- |\n")
		out.WriteString("| 唯一核心观点 | " + pptPolishMarkdownCell(claim) + " |\n")
		out.WriteString("| 支持内容 | " + pptPolishMarkdownCell(claim) + " |\n")
		out.WriteString("| 图表或配图建议 | " + pptPolishMarkdownCell(visualSuggestion) + " |\n")
		out.WriteString("| 演讲备注 | " + pptPolishMarkdownCell("围绕“"+slide.Title+"”讲清本页结论，不扩展用户未提供的业务事实。") + " |\n")
		out.WriteString("| 保留的原始数据与引用 | 用户当前请求原句；未使用外部来源 |\n\n")
	}
	return out.String(), len(slides), true
}

func pptPolishMarkdownCell(value string) string {
	value = strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
	value = strings.ReplaceAll(value, "|", `\|`)
	return value
}

func buildPPTPolishPreserveBrief(material string) string {
	slidePattern := regexp.MustCompile(`(?m)^# Slide (\d+)\s*$`)
	matches := slidePattern.FindAllStringSubmatchIndex(material, -1)
	if len(matches) == 0 {
		return "<!-- OPENKITTY_PPT_POLISH_PRESERVE -->\n\n# 演示文稿视觉润色\n\n## 原稿内容\n\n" + strings.TrimSpace(material) + "\n"
	}
	type preservedSlide struct {
		index int
		title string
		lines []string
		table string
	}
	slides := make([]preservedSlide, 0, len(matches))
	for matchIndex, match := range matches {
		start := match[1]
		end := len(material)
		if matchIndex+1 < len(matches) {
			end = matches[matchIndex+1][0]
		}
		index, _ := strconv.Atoi(material[match[2]:match[3]])
		body := strings.TrimSpace(material[start:end])
		table := ""
		if tableAt := strings.Index(body, "### Original editable table"); tableAt >= 0 {
			tablePart := body[tableAt:]
			if firstTableLine := strings.Index(tablePart, "\n|"); firstTableLine >= 0 {
				table = strings.TrimSpace(tablePart[firstTableLine+1:])
			}
			body = strings.TrimSpace(body[:tableAt])
		}
		var lines []string
		for _, raw := range strings.Split(body, "\n") {
			if clean := cleanPPTPolishParagraph(raw); clean != "" {
				lines = append(lines, clean)
			}
		}
		title := fmt.Sprintf("第%d页", index)
		if len(lines) > 0 {
			title = lines[0]
			lines = lines[1:]
		}
		title = localizedPPTPolishTitle(title)
		slides = append(slides, preservedSlide{index: index, title: title, lines: presentationLimitStrings(lines, 8), table: table})
	}
	deckTitle := "演示文稿视觉润色"
	if len(slides) > 0 && strings.TrimSpace(slides[0].title) != "" {
		deckTitle = slides[0].title
	}
	var out strings.Builder
	out.WriteString("<!-- OPENKITTY_PPT_POLISH_PRESERVE: source facts only; do not render production instructions -->\n\n")
	out.WriteString("# " + deckTitle + "\n\n")
	for _, slide := range slides {
		out.WriteString("## " + slide.title + "\n\n")
		for _, line := range slide.lines {
			out.WriteString("- " + line + "\n")
		}
		if slide.table != "" {
			out.WriteString("\n" + slide.table + "\n")
		}
		out.WriteString("\n")
	}
	return out.String()
}

func presentationLimitStrings(values []string, limit int) []string {
	seen := make(map[string]bool)
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out
}

func cleanPPTPolishParagraph(raw string) string {
	line := strings.Join(strings.Fields(strings.TrimSpace(raw)), " ")
	if line == "" {
		return ""
	}
	if cut := strings.Index(line, "基于上一轮证据制作 "); cut > 0 {
		line = strings.TrimSpace(line[:cut])
	}
	lower := strings.ToLower(line)
	for _, duplicatedSummaryPrefix := range []string{
		"users and entry points; trusted data and content inputs",
		"define the target user scenario and success criteria; prepare reliable input data",
		"user experience determines adoption quality; data and content governance determines",
	} {
		if strings.HasPrefix(lower, duplicatedSummaryPrefix) {
			return ""
		}
	}
	if regexp.MustCompile(`^\d{1,2}$|^\d{1,2}\s*/\s*\d{1,2}$`).MatchString(line) ||
		strings.EqualFold(line, "TECH SHARE") ||
		strings.HasPrefix(lower, "editable pptx generated from") ||
		strings.HasPrefix(lower, "technical narrative converted into") ||
		strings.HasPrefix(lower, "template:") || strings.HasPrefix(lower, "palette:") ||
		strings.HasPrefix(lower, "source file:") ||
		strings.Contains(lower, "generated by openkitty") {
		return ""
	}
	translations := map[string]string{
		"users and entry points":                                    "用户与接入点",
		"trusted data and content inputs":                           "可信数据与内容输入",
		"core service or reasoning layer":                           "核心服务与推理层",
		"architecture":                                              "方案架构",
		"layered architecture diagram.":                             "分层架构示意",
		"define the target user scenario and success criteria":      "明确目标用户场景与成功标准",
		"prepare reliable input data, content or system interfaces": "准备可靠输入数据、内容或系统接口",
		"run the core processing and orchestration layer":           "运行核心处理与编排层",
		"process":                  "执行流程",
		"horizontal process flow.": "横向流程示意",
		"user experience determines adoption quality":  "用户体验决定采用质量",
		"data and content governance determines trust": "数据与内容治理决定信任",
		"system orchestration determines reliability":  "系统编排决定可靠性",
		"matrix":                 "决策矩阵",
		"decision matrix cards.": "决策矩阵卡片",
		"the source comparison is preserved as an editable powerpoint table.": "",
	}
	if translated, ok := translations[lower]; ok {
		return translated
	}
	return strings.TrimSpace(line)
}

func localizedPPTPolishTitle(title string) string {
	translations := map[string]string{
		"why this matters":          "决策约束与调研口径",
		"reference architecture":    "方案能力架构",
		"end-to-end workflow":       "方案实施流程",
		"key design decisions":      "关键设计决策",
		"implementation priorities": "实施优先级",
		"rollout roadmap":           "试点推进路线",
		"discussion and next steps": "试点计划与下一步",
	}
	if translated, ok := translations[strings.ToLower(strings.TrimSpace(title))]; ok {
		return translated
	}
	return strings.TrimSpace(title)
}

func pptPolishDeliveryAuditCanRework(exec *PipelineExecution, stepIndex int) bool {
	if exec == nil || exec.RuntimePipeline == nil || stepIndex < 0 || stepIndex >= len(exec.RuntimePipeline.Steps) {
		return false
	}
	rework := exec.RuntimePipeline.Steps[stepIndex].Rework
	maxReworks := pipelineReworkMax(rework)
	return rework != nil && strings.TrimSpace(rework.Target) == "视觉润色" && maxReworks >= 1 && maxReworks <= 2
}

func pptPolishVisualCanDeferToDeliveryRework(exec *PipelineExecution, visualIndex int) bool {
	if exec == nil || visualIndex < 0 {
		return false
	}
	deliveryIndex := pipelineStepResultIndex(exec, "交付自查")
	return deliveryIndex > visualIndex && pptPolishDeliveryAuditCanRework(exec, deliveryIndex)
}

func pptPolishAuditReworkReason(auditResult string, auditErr error) string {
	var report struct {
		FatalErrors []string `json:"fatal_errors"`
		Errors      []string `json:"errors"`
	}
	if json.Unmarshal([]byte(strings.TrimSpace(auditResult)), &report) == nil {
		issues := report.FatalErrors
		if len(issues) == 0 {
			issues = report.Errors
		}
		if len(issues) > 0 {
			return "Bundled PPTX audit failed; correct exactly these blocking issues: " + truncate(strings.Join(issues, "; "), 900)
		}
	}
	if auditErr != nil {
		return "Bundled PPTX audit failed; correct the blocking issue: " + truncate(auditErr.Error(), 900)
	}
	return "Bundled PPTX audit failed; correct the reported blocking issue."
}

func buildPPTPolishAuditReworkOutput(reason string) string {
	reason = strings.TrimSpace(reason)
	switch pptPolishReworkMode(reason) {
	case "freshness":
		reason += " Freeze the current deck_spec.json, builder, and final PPTX bytes. Do not edit or rebuild. Audit that exact PPTX, render it into the exact contracted current-run preview, inspect contact_sheet plus every slide_NN with vision, and then stop."
	case "content":
		reason += " Change only the listed cells or visible text with edit_file/write_file (never shell or inline Python), then run the task builder as a direct command that preserves its real exit status. Only a real successful build consumes the one-build budget. Next audit once with the bundled audit_pptx.py, render once with the bundled render_pptx script into the exact contracted current-run preview, inspect contact_sheet.jpg plus every slide_NN.png with vision, and then stop without another edit."
	}
	record, _ := json.Marshal(pipelineReworkControl{Status: "retry_required", Reason: reason})
	return "PPTX delivery is paused for a bounded visual revision.\n" + pipelineReworkControlPrefix + string(record)
}

func pptPolishReworkMode(reason string) string {
	return pipelinecore.PresentationReworkMode(reason)
}

func (e *PipelineExecutor) hostValidatePPTPolish(ctx context.Context, exec *PipelineExecution, stepIndex int, input string) (string, bool, error) {
	visualIndex := pipelineStepResultIndex(exec, "视觉润色")
	materialIndex := pipelineStepResultIndex(exec, "素材读取")
	if exec == nil || stepIndex < 0 || stepIndex >= len(exec.Steps) || visualIndex < 0 || visualIndex >= stepIndex || strings.TrimSpace(exec.Steps[visualIndex].Output) == "" {
		return "", false, nil
	}
	workspace := e.pptPolishWorkspace(ctx, exec)
	// The visual step owns the delivered artifact. Looking in the original user
	// input first can accidentally validate the source deck and report a no-op as
	// a successful polish.
	target := firstExistingWorkspacePathWithExtension(exec.Steps[visualIndex].Output, ".pptx", workspace)
	if target == "" {
		target = pptPolishDefaultOutputPath(exec, workspace)
	}
	target = filepath.Clean(target)
	requestInput := strings.TrimSpace(exec.Input)
	if requestInput == "" {
		requestInput = input
	}
	if err := validatePPTPolishRequestedFilename(requestInput, target); err != nil {
		return "", false, err
	}
	source := ""
	if materialIndex >= 0 {
		source = firstExistingWorkspacePathWithExtension(exec.Steps[materialIndex].Output, ".pptx", workspace)
	}
	if source != "" && sameLocalPath(source, target) {
		return "", false, fmt.Errorf("PPTX delivery points to the unchanged source deck instead of a new polished file: %s", target)
	}
	started := time.Now()
	info, err := os.Stat(target)
	if err != nil || info.IsDir() {
		return "", false, fmt.Errorf("PPTX delivery missing: %s", target)
	}
	if info.Size() < 10*1024 {
		return "", false, fmt.Errorf("PPTX delivery too small: %s (%d bytes)", target, info.Size())
	}
	structure, err := contenttools.InspectPPTXStructure(target)
	if err != nil {
		return "", false, err
	}
	if err := validatePPTPolishRequestedSlideCount(requestInput, structure.SlideCount); err != nil {
		return "", false, err
	}
	visualObjectCount := structure.TextShapeCount + structure.PictureCount + structure.GraphicFrameCount + structure.TableCount
	if structure.SlideCount < 4 || visualObjectCount == 0 {
		return "", false, fmt.Errorf("PPTX delivery validation failed: slides=%d visual_objects=%d", structure.SlideCount, visualObjectCount)
	}
	audienceText, textErr := contenttools.ExtractPPTX(target)
	if textErr != nil {
		return "", false, fmt.Errorf("PPTX audience-content validation failed: %w", textErr)
	}
	if violations := pptPolishAudienceContentViolations(audienceText); len(violations) > 0 {
		return "", false, fmt.Errorf("PPTX audience-content validation failed: production metadata is visible (%s)", strings.Join(violations, ", "))
	}
	if err := validatePPTPolishFinalGrounding(exec, requestInput, audienceText, workspace); err != nil {
		if pptPolishDeliveryAuditCanRework(exec, stepIndex) {
			return buildPPTPolishAuditReworkOutput("Final PPTX content grounding is invalid; correct all listed blockers together: " + truncate(err.Error(), 1400)), true, nil
		}
		return "", false, err
	}
	if err := validatePPTPolishRequestedTheme(exec.Input, target); err != nil {
		return "", false, err
	}
	auditResult, auditAvailable, auditErr := auditPPTXWithBundledScript(ctx, target, pptxAuditArgumentsForRequest(exec.Input)...)
	if auditAvailable {
		e.recordHostPipelineToolCall(exec, stepIndex, started, "pptx_audit", map[string]interface{}{"path": target}, auditResult)
		if auditErr != nil {
			if pptPolishDeliveryAuditCanRework(exec, stepIndex) {
				return buildPPTPolishAuditReworkOutput(pptPolishAuditReworkReason(auditResult, auditErr)), true, nil
			}
			return "", false, auditErr
		}
	}
	if err := validatePPTPolishVisualEvidence(exec, visualIndex, structure.SlideCount); err != nil {
		if pptPolishDeliveryAuditCanRework(exec, stepIndex) {
			reason := "Final PPTX visual evidence is invalid or stale; correct exactly this blocker: " + truncate(err.Error(), 900)
			return buildPPTPolishAuditReworkOutput(reason), true, nil
		}
		return "", false, err
	}
	if structure.SlideCount >= 8 {
		e.recordHostPipelineToolCall(exec, stepIndex, started, "pptx_visual_evidence", map[string]interface{}{"path": target}, fmt.Sprintf("contact_sheet=1 slide_images=%d", structure.SlideCount))
	}
	editable := structure.TextShapeCount > 0
	result := fmt.Sprintf("validated path=%s bytes=%d slides=%d text_or_shape_objects=%d pictures=%d graphic_frames=%d editable_indicators=%t", target, info.Size(), structure.SlideCount, structure.TextShapeCount, structure.PictureCount, structure.GraphicFrameCount, editable)
	e.recordHostPipelineToolCall(exec, stepIndex, started, "pptx_validate", map[string]interface{}{"path": target}, result)
	auditSummary := "结构校验已通过；本机未提供独立PPTX审计脚本。"
	if auditAvailable {
		auditSummary = summarizePPTXAuditResult(auditResult)
	}
	displayPath := pptPolishUserVisiblePath(target, workspace)
	return fmt.Sprintf("PPT 已生成，共 %d 页，可编辑。\n\n- 文件：%s\n- 验收：%s", structure.SlideCount, displayPath, auditSummary), true, nil
}

func (e *PipelineExecutor) recordHostPipelineToolCall(exec *PipelineExecution, stepIndex int, started time.Time, toolName string, params map[string]interface{}, result string) {
	if exec == nil || stepIndex < 0 || stepIndex >= len(exec.Steps) {
		return
	}
	paramsJSON, _ := json.Marshal(params)
	record := ToolCallRecord{Timestamp: started, ToolName: toolName, Params: truncate(string(paramsJSON), 500), Result: truncate(result, 2000), DurationMs: time.Since(started).Milliseconds()}
	exec.recordToolCall(stepIndex, record)
	exec.mu.Lock()
	exec.Steps[stepIndex].DurationMs = record.DurationMs
	exec.mu.Unlock()
}

func pptPolishSourceSlideCount(exec *PipelineExecution) int {
	if materialIndex := pipelineStepResultIndex(exec, "素材读取"); materialIndex >= 0 {
		match := regexp.MustCompile(`现有页数：(\d+)`).FindStringSubmatch(exec.Steps[materialIndex].Output)
		if len(match) == 2 {
			if count, err := strconv.Atoi(match[1]); err == nil {
				return normalizePipelineSlideCount(count)
			}
		}
	}
	return normalizePipelineSlideCount(0)
}

func normalizePipelineSlideCount(slideCount int) int {
	if slideCount <= 0 {
		return 8
	}
	if slideCount > 60 {
		return 60
	}
	return slideCount
}

func firstExistingWorkspacePathWithExtension(text, extension, workspace string) string {
	absPattern := regexp.MustCompile(`(?i)[a-z]:[\\/][^\r\n<>|?*]*?` + regexp.QuoteMeta(extension))
	for _, match := range absPattern.FindAllString(text, -1) {
		match = cleanLocalFileMention(match)
		if info, err := os.Stat(match); err == nil && !info.IsDir() {
			return filepath.Clean(match)
		}
	}
	unixAbsPattern := regexp.MustCompile(`(?i)(?:^|[\s"'=:：(])(/[^\r\n<>"'|?*]*?` + regexp.QuoteMeta(extension) + `)(?:$|[\s"'<>|，。；;、])`)
	for _, groups := range unixAbsPattern.FindAllStringSubmatch(text, -1) {
		if len(groups) != 2 {
			continue
		}
		match := cleanLocalFileMention(groups[1])
		if info, err := os.Stat(match); err == nil && !info.IsDir() {
			return filepath.Clean(match)
		}
	}
	candidates := append(extractArtifactPaths(text), extractLocalFileMentions(text)...)
	for _, match := range candidates {
		if !strings.EqualFold(filepath.Ext(match), extension) {
			continue
		}
		candidate := match
		if !filepath.IsAbs(candidate) {
			candidate = filepath.Join(workspace, filepath.FromSlash(candidate))
		}
		candidate = filepath.Clean(candidate)
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate
		}
	}
	return ""
}

func sameLocalPath(left, right string) bool {
	leftAbs, leftErr := filepath.Abs(filepath.Clean(left))
	rightAbs, rightErr := filepath.Abs(filepath.Clean(right))
	if leftErr != nil || rightErr != nil {
		return strings.EqualFold(filepath.Clean(left), filepath.Clean(right))
	}
	return strings.EqualFold(leftAbs, rightAbs)
}

func auditPPTXWithBundledScript(ctx context.Context, target string, extraArgs ...string) (string, bool, error) {
	runtimeDir := ensureEmbeddedBuiltinRuntimeDirForContext(ctx, "", filepath.Join("presentation", "ppt-art-director"))
	script := filepath.Join(runtimeDir, "scripts", "audit_pptx.py")
	if runtimeDir == "" {
		return "", false, nil
	}
	if info, err := os.Stat(script); err != nil || info.IsDir() {
		return "", false, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	for _, python := range []string{"python", "python3"} {
		if _, err := osexec.LookPath(python); err != nil {
			continue
		}
		args := append([]string{script, target}, extraArgs...)
		output, err := osexec.CommandContext(ctx, python, args...).CombinedOutput()
		result := strings.TrimSpace(string(output))
		if err != nil {
			return result, true, fmt.Errorf("PPTX structural audit failed: %s", truncate(result, 1800))
		}
		return result, true, nil
	}
	return "", false, nil
}

func pptxAuditArgumentsForRequest(input string) []string {
	type fontFloor struct {
		labels string
		flag   string
	}
	var args []string
	for _, spec := range []fontFloor{
		{labels: `正文|body(?:\s+text)?`, flag: "--min-body-font-size"},
		{labels: `标题|titles?`, flag: "--min-title-font-size"},
	} {
		pattern := regexp.MustCompile(`(?i)(?:` + spec.labels + `)[^0-9\r\n]{0,24}(\d+(?:\.\d+)?)\s*(?:pt|磅)`)
		match := pattern.FindStringSubmatch(input)
		if len(match) != 2 {
			continue
		}
		value, err := strconv.ParseFloat(match[1], 64)
		if err != nil || value < 6 || value > 72 {
			continue
		}
		args = append(args, spec.flag, strconv.FormatFloat(value, 'f', -1, 64))
	}
	return args
}

func validatePPTPolishVisualEvidence(exec *PipelineExecution, visualIndex, slideCount int) error {
	if slideCount < 8 {
		return nil
	}
	if exec == nil || visualIndex < 0 || visualIndex >= len(exec.Steps) {
		return fmt.Errorf("PPTX visual QA evidence missing: visual-production step is unavailable")
	}
	contactSheet := false
	seenSlides := make(map[int]bool, slideCount)
	slidePattern := regexp.MustCompile(`(?i)(?:^|[/\\])slide[_-]?0*([1-9]\d*)\.(?:png|jpe?g)$`)
	expectedPreview := pptPolishCurrentRunPreviewRelativePath(exec)
	freshAfter := time.Time{}
	if buildAt, builtDeck := latestPPTPolishDeckBuild(exec.Steps[visualIndex].ToolCalls); !buildAt.IsZero() {
		renderAt := latestPPTPolishNativeRenderAfter(
			exec.Steps[visualIndex].ToolCalls,
			buildAt,
			builtDeck,
			expectedPreview,
		)
		if renderAt.IsZero() {
			filesystemRenderAt, filesystemErr := pptPolishFilesystemRenderFreshness(exec, slideCount)
			if filesystemErr != nil {
				return fmt.Errorf(
					"PPTX visual QA evidence is stale: the final deck bytes were rebuilt at %s after the last recognized native render, and the exact current-run preview could not prove a later complete render (%v); rerender the current PPTX into %s, then inspect contact_sheet and every slide_NN with vision(path=...)",
					buildAt.Format(time.RFC3339),
					filesystemErr,
					expectedPreview,
				)
			}
			renderAt = filesystemRenderAt
		}
		freshAfter = renderAt
	}
	for _, call := range exec.Steps[visualIndex].ToolCalls {
		if !strings.EqualFold(strings.TrimSpace(call.ToolName), "vision") || pipelineToolCallRecordFailed(call) {
			continue
		}
		if !freshAfter.IsZero() && !call.Timestamp.After(freshAfter) {
			continue
		}
		var params map[string]interface{}
		if json.Unmarshal([]byte(call.Params), &params) != nil {
			continue
		}
		path, _ := params["path"].(string)
		path = strings.TrimSpace(strings.ReplaceAll(path, "\\", "/"))
		if !pptPolishVisualPathUsesCurrentRunPreview(path, expectedPreview) {
			continue
		}
		lower := strings.ToLower(path)
		if strings.Contains(lower, "contact_sheet.") || strings.Contains(lower, "contact-sheet.") {
			contactSheet = true
		}
		match := slidePattern.FindStringSubmatch(path)
		if len(match) != 2 {
			continue
		}
		number, err := strconv.Atoi(match[1])
		if err == nil && number >= 1 && number <= slideCount {
			seenSlides[number] = true
		}
	}
	var missing []string
	for number := 1; number <= slideCount; number++ {
		if !seenSlides[number] {
			missing = append(missing, fmt.Sprintf("slide_%02d", number))
		}
	}
	if !contactSheet || len(missing) > 0 {
		return fmt.Errorf(
			"PPTX visual QA evidence missing in current-run preview %s: contact_sheet=%t inspected_slides=%d/%d missing=%s; shared or stale previews, rendering, and text-only audit cannot substitute for successful vision(path=...) calls",
			expectedPreview,
			contactSheet,
			len(seenSlides),
			slideCount,
			strings.Join(missing, ","),
		)
	}
	return nil
}

// pptPolishFilesystemRenderFreshness recognizes a complete native preview
// even when the model ran the builder/auditor/renderer through one task-local
// helper script. Tool-name parsing cannot see through an arbitrary helper, so
// the host verifies the exact run-scoped files instead: every required image
// must decode, must be no older than the final contracted PPTX, and later
// vision calls must still occur after the newest preview byte.
func pptPolishFilesystemRenderFreshness(exec *PipelineExecution, slideCount int) (time.Time, error) {
	if exec == nil || slideCount < 1 {
		return time.Time{}, fmt.Errorf("execution or slide count is unavailable")
	}
	workspace := strings.TrimSpace(exec.ResolvedWorkspace)
	if workspace == "" {
		return time.Time{}, fmt.Errorf("resolved workspace is unavailable")
	}
	workspaceAbs, err := filepath.Abs(projectctx.NormalizeWorkspace(workspace))
	if err != nil {
		return time.Time{}, fmt.Errorf("resolve workspace: %w", err)
	}
	resolvedWorkspace, err := projectctx.ResolvePathThroughExistingAncestor(workspaceAbs)
	if err != nil {
		return time.Time{}, fmt.Errorf("resolve workspace boundary: %w", err)
	}
	deckPath := pptPolishDefaultOutputPath(exec, workspaceAbs)
	previewPath := filepath.Join(workspaceAbs, filepath.FromSlash(pptPolishCurrentRunPreviewRelativePath(exec)))
	resolvedDeck, err := filepath.EvalSymlinks(deckPath)
	if err != nil {
		return time.Time{}, fmt.Errorf("final contracted PPTX is unavailable: %w", err)
	}
	resolvedPreview, err := filepath.EvalSymlinks(previewPath)
	if err != nil {
		return time.Time{}, fmt.Errorf("current-run preview directory is unavailable: %w", err)
	}
	if !projectctx.PathWithinEqualFold(resolvedWorkspace, resolvedDeck) || !projectctx.PathWithinEqualFold(resolvedWorkspace, resolvedPreview) {
		return time.Time{}, fmt.Errorf("final deck or preview escapes the resolved workspace")
	}
	deckInfo, err := os.Stat(resolvedDeck)
	if err != nil || deckInfo.IsDir() || deckInfo.Size() < 10*1024 {
		return time.Time{}, fmt.Errorf("final contracted PPTX is missing or invalid")
	}
	previewInfo, err := os.Stat(resolvedPreview)
	if err != nil || !previewInfo.IsDir() {
		return time.Time{}, fmt.Errorf("current-run preview is not a directory")
	}

	newest := time.Time{}
	verifyImage := func(label string, candidates ...string) error {
		var selected string
		for _, candidate := range candidates {
			path := filepath.Join(resolvedPreview, candidate)
			if info, statErr := os.Stat(path); statErr == nil && !info.IsDir() {
				selected = path
				break
			}
		}
		if selected == "" {
			return fmt.Errorf("%s is missing", label)
		}
		resolvedImage, resolveErr := filepath.EvalSymlinks(selected)
		if resolveErr != nil || !projectctx.PathWithinEqualFold(resolvedPreview, resolvedImage) {
			return fmt.Errorf("%s is outside the current-run preview", label)
		}
		file, openErr := os.Open(resolvedImage)
		if openErr != nil {
			return fmt.Errorf("open %s: %w", label, openErr)
		}
		config, _, decodeErr := image.DecodeConfig(file)
		closeErr := file.Close()
		if decodeErr != nil || config.Width < 1 || config.Height < 1 {
			return fmt.Errorf("%s is not a decodable image", label)
		}
		if closeErr != nil {
			return fmt.Errorf("close %s: %w", label, closeErr)
		}
		info, statErr := os.Stat(resolvedImage)
		if statErr != nil || info.IsDir() || info.Size() == 0 {
			return fmt.Errorf("%s is empty or unavailable", label)
		}
		if info.ModTime().Before(deckInfo.ModTime()) {
			return fmt.Errorf("%s predates the final PPTX", label)
		}
		if newest.IsZero() || info.ModTime().After(newest) {
			newest = info.ModTime()
		}
		return nil
	}
	if err := verifyImage("contact_sheet", "contact_sheet.jpg", "contact_sheet.jpeg", "contact_sheet.png", "contact-sheet.jpg", "contact-sheet.png"); err != nil {
		return time.Time{}, err
	}
	for number := 1; number <= slideCount; number++ {
		label := fmt.Sprintf("slide_%02d", number)
		if err := verifyImage(label, label+".png", label+".jpg", label+".jpeg"); err != nil {
			return time.Time{}, err
		}
	}
	return newest, nil
}

func pipelineToolCallFromRecord(record ToolCallRecord) (ToolCall, string, bool) {
	var arguments map[string]interface{}
	if json.Unmarshal([]byte(record.Params), &arguments) != nil {
		return ToolCall{}, "", false
	}
	toolCall := ToolCall{Name: record.ToolName, Arguments: arguments}
	return toolCall, presentationToolPayload(toolCall), true
}

// latestPPTPolishDeckBuild returns the last successful builder invocation that
// actually reported a PPTX path. Merely reading or grepping build_deck.py is
// not a byte mutation and therefore does not invalidate later QA evidence.
func latestPPTPolishDeckBuild(calls []ToolCallRecord) (time.Time, string) {
	latest := time.Time{}
	deckPath := ""
	for _, record := range calls {
		if record.Timestamp.IsZero() || pipelineToolCallRecordFailed(record) {
			continue
		}
		toolCall, payload, ok := pipelineToolCallFromRecord(record)
		if !ok || !isPresentationBuilderAction(toolCall, payload) || isPresentationBuilderSourceWrite(toolCall, payload) {
			continue
		}
		builtPath := presentationToolDeckPath(toolCall, payload, record.Result)
		if builtPath == "" || !pptPolishBuildRecordConfirmsCreation(record, builtPath) {
			continue
		}
		if latest.IsZero() || record.Timestamp.After(latest) {
			latest = record.Timestamp
			deckPath = builtPath
		}
	}
	return latest, deckPath
}

func pptPolishBuildRecordConfirmsCreation(record ToolCallRecord, builtPath string) bool {
	if presentationBuilderResultConfirmsCreation(record.Result, builtPath) {
		return true
	}
	// build_deck.py commonly prints only the output PPTX path. The shell
	// envelope and ToolCallRecord already carry the success state, so a matching
	// PPTX path in output result is sufficient to treat this as a byte mutation
	// for freshness purposes. Calls that only mention the path in argv remain
	// excluded because presentationToolOutputPayload strips the command section.
	reported := firstPresentationPath(presentationToolOutputPayload(record.Result))
	return reported != "" && samePresentationPath(reported, builtPath)
}

func latestPPTPolishNativeRenderAfter(
	calls []ToolCallRecord,
	buildAt time.Time,
	deckPath string,
	expectedPreview string,
) time.Time {
	latest := time.Time{}
	for _, record := range calls {
		if record.Timestamp.IsZero() || !record.Timestamp.After(buildAt) || pipelineToolCallRecordFailed(record) {
			continue
		}
		toolCall, payload, ok := pipelineToolCallFromRecord(record)
		if !ok || !hasPresentationRenderEvidenceForTool(toolCall, payload, record.Result) {
			continue
		}
		evidenceDeck := presentationToolDeckPath(toolCall, payload, record.Result)
		if deckPath != "" && evidenceDeck != "" && !samePresentationPath(deckPath, evidenceDeck) {
			continue
		}
		renderTarget := presentationRenderTarget(toolCall, payload, record.Result)
		if expectedPreview != "" && (renderTarget == "" || !pptPolishVisualPathUsesCurrentRunPreview(
			expectedPreview+"/slide_01.png",
			renderTarget,
		)) {
			continue
		}
		if latest.IsZero() || record.Timestamp.After(latest) {
			latest = record.Timestamp
		}
	}
	return latest
}

func pptPolishVisualPathUsesCurrentRunPreview(imagePath, expectedPreview string) bool {
	image := strings.ToLower(strings.TrimSuffix(portablepath.Normalize(imagePath), "/"))
	expected := strings.ToLower(strings.Trim(portablepath.Normalize(expectedPreview), "/"))
	if image == "" || expected == "" {
		return false
	}
	return strings.HasPrefix(image, expected+"/") || strings.Contains(image, "/"+expected+"/")
}

func pptPolishHostFallbackEligible(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	for _, marker := range []string{
		"pptx delivery missing",
		"pptx delivery too small",
		"pptx delivery validation failed",
		"cannot inspect pptx",
	} {
		if strings.Contains(message, marker) {
			return true
		}
	}
	return false
}

type pptxAuditReport struct {
	Status          string   `json:"status"`
	FatalErrors     []string `json:"fatal_errors"`
	QualityWarnings []string `json:"quality_warnings"`
	Errors          []string `json:"errors"`
	Warnings        []string `json:"warnings"`
}

func summarizePPTXAuditResult(result string) string {
	var report pptxAuditReport
	if err := json.Unmarshal([]byte(strings.TrimSpace(result)), &report); err != nil {
		return "独立PPTX结构审计已通过。"
	}
	fatalCount := len(report.FatalErrors)
	if fatalCount == 0 {
		fatalCount = len(report.Errors)
	}
	if fatalCount > 0 || strings.EqualFold(report.Status, "failed") {
		return "独立PPTX结构审计未通过。"
	}
	warningCount := len(report.QualityWarnings)
	if warningCount == 0 {
		warningCount = len(report.Warnings)
	}
	if warningCount > 0 {
		return fmt.Sprintf("PPTX结构审计已通过；发现%d项版式或可读性改进建议，已保留有效交付物。", warningCount)
	}
	return "PPTX结构与可编辑性审计已通过。"
}

func (e *PipelineExecutor) runNativeDataAnalysisFastPath(ctx context.Context, exec *PipelineExecution, pipeline Pipeline, input string) {
	if exec == nil || e == nil || e.gateway == nil || e.gateway.config == nil || len(exec.Steps) == 0 {
		return
	}
	started := time.Now()
	workspace := e.pipelineExecutionWorkspace(exec)
	var dataFile string
	for _, mention := range extractLocalFileMentions(input) {
		ext := strings.ToLower(filepath.Ext(mention))
		if ext != ".csv" && ext != ".tsv" && ext != ".xls" && ext != ".xlsx" && ext != ".json" {
			continue
		}
		candidate := mention
		if !filepath.IsAbs(candidate) {
			candidate = filepath.Join(workspace, filepath.FromSlash(candidate))
		}
		candidate = filepath.Clean(candidate)
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			dataFile = candidate
			break
		}
	}
	var reportPath, chartPath string
	for _, rel := range extractArtifactPaths(input) {
		switch strings.ToLower(filepath.Ext(rel)) {
		case ".md", ".markdown":
			if reportPath == "" {
				reportPath = rel
			}
		case ".png":
			if chartPath == "" {
				chartPath = rel
			}
		}
	}
	if reportPath == "" {
		reportPath = ".openkitty/storage/data_analysis_report.md"
	}
	if chartPath == "" {
		chartPath = ".openkitty/storage/charts/01_overview.png"
	}
	params := map[string]interface{}{
		"task":        "report",
		"data_file":   dataFile,
		"report_path": reportPath,
		"chart_path":  chartPath,
	}
	paramsJSON, _ := json.Marshal(params)
	var result string
	var runErr error
	if dataFile == "" {
		runErr = fmt.Errorf("native data analysis fast path: no readable structured data file found")
	} else {
		result, runErr = analysistools.NewDataScienceTool(workspace).Execute(ctx, params)
	}
	record := ToolCallRecord{
		Timestamp:  started,
		ToolName:   "data_science",
		Params:     truncate(string(paramsJSON), 500),
		Result:     truncate(result, 2000),
		DurationMs: time.Since(started).Milliseconds(),
	}
	if runErr != nil {
		record.Error = runErr.Error()
	}
	exec.recordToolCall(0, record)
	exec.mu.Lock()
	exec.CurrentStep = 0
	exec.Steps[0].DurationMs = record.DurationMs
	if runErr != nil {
		exec.Steps[0].Status = "failed"
		exec.Steps[0].Error = runErr.Error()
		exec.Status = "failed"
	} else {
		exec.Steps[0].Output = "数据分析已完成并通过文件校验。\n" + strings.TrimSpace(result)
		exec.Steps[0].Status = "completed"
		exec.Status = "running"
	}
	exec.mu.Unlock()
	if runErr == nil {
		if err := e.validateFastPipelineArtifacts(exec); err != nil {
			exec.mu.Lock()
			exec.Steps[0].Status = "failed"
			exec.Steps[0].Error = err.Error()
			exec.Status = "failed"
			exec.mu.Unlock()
		} else {
			exec.mu.Lock()
			exec.Status = "completed"
			exec.mu.Unlock()
			e.saveStepOutputToFile(exec, 0, exec.Steps[0].Output)
		}
	}
	e.saveToDB(exec)
	status := exec.Status
	if status == "completed" {
		e.broadcastPipelineEvent(exec.ID, "step_done", map[string]interface{}{"step_index": 0, "step_name": pipeline.Steps[0].Name, "native_fast_path": true})
	} else {
		e.broadcastPipelineEvent(exec.ID, "step_failed", map[string]interface{}{"step_index": 0, "step_name": pipeline.Steps[0].Name, "error": exec.Steps[0].Error})
	}
	e.fileLogger.Printf("[Pipeline] %s native fast path finished: status=%s duration=%s", pipeline.Name, status, time.Since(started))
	e.broadcastPipelineEvent(exec.ID, "completed", map[string]interface{}{"total_steps": len(exec.Steps), "status": status, "native_fast_path": true})
	if e.gateway.evolver != nil {
		e.gateway.evolver.Record(pipelineEvolutionSnapshot(exec))
	}
}

func (e *PipelineExecutor) validateFastPipelineArtifacts(exec *PipelineExecution) error {
	if exec == nil || exec.PipelineName != dataAnalysisLocalFastPipelineName {
		return nil
	}
	paths := extractArtifactPaths(exec.Input)
	var required []string
	for _, rel := range paths {
		switch strings.ToLower(filepath.Ext(rel)) {
		case ".md", ".markdown", ".png":
			required = append(required, rel)
		}
	}
	if len(required) == 0 {
		return fmt.Errorf("artifact validation failed: no requested Markdown or PNG path found")
	}
	for _, rel := range required {
		fullPath, ok := e.storageArtifactPathForExecution(exec, rel)
		if !ok {
			return fmt.Errorf("artifact validation failed: path outside storage: %s", rel)
		}
		info, err := os.Stat(fullPath)
		if err != nil || info.IsDir() {
			return fmt.Errorf("artifact validation failed: requested artifact missing: %s", fullPath)
		}
		minSize := int64(300)
		if strings.EqualFold(filepath.Ext(rel), ".png") {
			minSize = 1024
		}
		if info.Size() < minSize {
			return fmt.Errorf("artifact validation failed: requested artifact too small: %s (%d bytes)", fullPath, info.Size())
		}
		if !exec.CreatedAt.IsZero() && info.ModTime().Before(exec.CreatedAt.Add(-2*time.Second)) {
			return fmt.Errorf("artifact validation failed: requested artifact is stale: %s", fullPath)
		}
	}
	return nil
}

// ====================== Routing Helpers ======================

// matchCondition evaluates a simple DSL against step output.
// Supported patterns:
//
//	"contains 'X'" → output contains substring X
//	"status == 'X'" → output contains status:X (step-internal)
//	empty string → always matches (default route)
func matchCondition(output, condition string) bool {
	condition = strings.TrimSpace(condition)
	if condition == "" {
		return true // default route
	}
	if strings.HasPrefix(condition, "contains ") {
		sub, _ := containsConditionLiteral(condition)
		return strings.Contains(output, sub)
	}
	return false
}

func matchRouteCondition(exec *PipelineExecution, pipelineName string, stepIndex int, output string, route StepRoute) bool {
	sub, ok := containsConditionLiteral(route.When)
	if ok && sub == pipelineStopMarker {
		if !hasStandaloneFinalPipelineStopMarker(output) {
			return false
		}
		return pipelineAllowsControlStop(exec, pipelineName, stepIndex)
	}
	return matchCondition(output, route.When)
}

func containsConditionLiteral(condition string) (string, bool) {
	condition = strings.TrimSpace(condition)
	if !strings.HasPrefix(condition, "contains ") {
		return "", false
	}
	literal := strings.TrimSpace(condition[len("contains "):])
	if len(literal) >= 2 {
		first := literal[0]
		last := literal[len(literal)-1]
		if (first == '\'' || first == '"') && last == first {
			return literal[1 : len(literal)-1], true
		}
	}
	return strings.Trim(literal, "'\""), true
}

func pipelineAllowsControlStop(exec *PipelineExecution, pipelineName string, stepIndex int) bool {
	if pipelineName != "investment-advisory" {
		return true
	}
	if exec == nil {
		return true
	}
	exec.mu.Lock()
	isQualityGate := stepIndex >= 0 && stepIndex < len(exec.Steps) && exec.Steps[stepIndex].Name == "投研合成与质量门"
	exec.mu.Unlock()
	if !isQualityGate {
		return true
	}
	switch investmentAdvisoryTaskType(exec, stepIndex) {
	case "unclear", "market":
		return true
	case "sector", "single_stock":
		return false
	default:
		return false
	}
}

func investmentAdvisoryTaskType(exec *PipelineExecution, beforeStep int) string {
	if exec == nil {
		return ""
	}
	exec.mu.Lock()
	steps := append([]StepResult(nil), exec.Steps...)
	exec.mu.Unlock()
	if beforeStep > len(steps) {
		beforeStep = len(steps)
	}
	for i := beforeStep - 1; i >= 0; i-- {
		text := strings.TrimSpace(stripPipelineControlMarkers(steps[i].Output))
		if text == "" {
			continue
		}
		var payload struct {
			TaskType string `json:"task_type"`
		}
		if err := json.Unmarshal([]byte(text), &payload); err == nil && strings.TrimSpace(payload.TaskType) != "" {
			return strings.ToLower(strings.TrimSpace(payload.TaskType))
		}
		if m := regexp.MustCompile(`"task_type"\s*:\s*"([^"]+)"`).FindStringSubmatch(text); len(m) == 2 {
			return strings.ToLower(strings.TrimSpace(m[1]))
		}
	}
	return ""
}

// findStepIndex resolves a step name to its index in the steps slice.
func findStepIndex(steps []PipelineStep, name string) int {
	for i, s := range steps {
		if s.Name == name {
			return i
		}
	}
	return -1
}

// ====================== Parallel Execution ======================

func (e *PipelineExecutor) executeParallelSteps(ctx context.Context, exec *PipelineExecution, step *PipelineStep, parentIdx int, input string, sessionID string) {
	startedAt := time.Now()
	parallel := step.Parallel
	n := len(parallel)
	if e.fileLogger != nil {
		e.fileLogger.Printf("[Pipeline] Step %d (%s): fan-out to %d parallel sub-steps", parentIdx, step.Name, n)
	}

	finishWithError := func(status string, err error) {
		exec.mu.Lock()
		exec.Status = status
		if parentIdx >= 0 && parentIdx < len(exec.Steps) {
			exec.Steps[parentIdx].Status = "failed"
			exec.Steps[parentIdx].Error = err.Error()
			exec.Steps[parentIdx].DurationMs = time.Since(startedAt).Milliseconds()
		}
		exec.mu.Unlock()
		e.saveToDB(exec)
		eventType := "step_failed"
		if status == "stopped" {
			eventType = "stopped"
		}
		e.broadcastPipelineEvent(exec.ID, eventType, map[string]interface{}{
			"step_index": parentIdx, "step_name": step.Name, "error": err.Error(), "parallel": n,
		})
	}

	if n == 0 {
		finishWithError("failed", fmt.Errorf("parallel step %q has no sub-steps", step.Name))
		return
	}
	if pipelineSourceContractApplies(step, input) || step.ArtifactContract != nil {
		finishWithError("failed", fmt.Errorf("parallel parent step %q cannot own a runtime contract; attach the contract to the responsible sub-step", step.Name))
		return
	}
	activeSourceContracts := 0
	for j := range parallel {
		if pipelineSourceContractApplies(&parallel[j], input) {
			activeSourceContracts++
		}
	}
	if activeSourceContracts > 1 {
		finishWithError("failed", fmt.Errorf("parallel step %q has %d active source contracts; only one frozen source can be promoted to the parent step", step.Name, activeSourceContracts))
		return
	}

	exec.mu.Lock()
	exec.CurrentStep = parentIdx
	if parentIdx >= 0 && parentIdx < len(exec.Steps) {
		exec.Steps[parentIdx].Status = "running"
		exec.Steps[parentIdx].Error = ""
	}
	upstreamSteps := append([]StepResult(nil), exec.Steps...)
	exec.mu.Unlock()
	e.saveToDB(exec)
	e.broadcastPipelineEvent(exec.ID, "step_start", map[string]interface{}{
		"step_index": parentIdx, "step_name": step.Name, "parallel": n,
	})

	if ctx == nil {
		ctx = context.Background()
	}
	parallelCtx, cancel := context.WithTimeout(ctx, e.parallelStepWallTimeout())
	defer cancel()

	type parallelResult struct {
		idx            int
		output         string
		sourceSnapshot *PipelineSourceSnapshot
		err            error
	}

	results := make(chan parallelResult, n)

	for j := 0; j < n; j++ {
		go func(idx int, subStep PipelineStep) {
			result := parallelResult{idx: idx}
			defer func() {
				if recovered := recover(); recovered != nil {
					result.output = ""
					result.sourceSnapshot = nil
					result.err = fmt.Errorf("panic in parallel sub-step %q: %v", subStep.Name, recovered)
				}
				select {
				case results <- result:
				case <-parallelCtx.Done():
				}
			}()
			result.output, result.sourceSnapshot, result.err = e.executeParallelSubStep(
				parallelCtx, exec, subStep, parentIdx, idx, upstreamSteps, input, sessionID,
			)
		}(j, parallel[j])
	}

	// A worker may panic or ignore cancellation. Recovery turns panic into a
	// branch error; the wall deadline keeps the collector itself bounded.
	collected := make([]parallelResult, n)
	received := make([]bool, n)
	remaining := n
	parentCancelled := false
	acceptResult := func(r parallelResult) {
		if r.idx < 0 || r.idx >= n || received[r.idx] {
			return
		}
		collected[r.idx] = r
		received[r.idx] = true
		remaining--
	}
	for remaining > 0 {
		select {
		case r := <-results:
			acceptResult(r)
		case <-parallelCtx.Done():
			parentCancelled = ctx.Err() != nil
			// A result and the deadline can become ready together. Drain results
			// that already reached the buffered channel before classifying the
			// remaining branches as timed out; otherwise select randomness can
			// turn a completed required branch into a false hard failure.
			draining := true
			for draining && remaining > 0 {
				select {
				case r := <-results:
					acceptResult(r)
				default:
					draining = false
				}
			}
			deadlineErr := fmt.Errorf("parallel step wall deadline exceeded after %s", e.parallelStepWallTimeout())
			if parentCancelled {
				deadlineErr = ctx.Err()
			}
			for idx := range received {
				if received[idx] {
					continue
				}
				collected[idx] = parallelResult{idx: idx, err: deadlineErr}
				received[idx] = true
			}
			remaining = 0
		}
	}
	cancel()

	if parentCancelled {
		finishWithError("stopped", ctx.Err())
		return
	}

	merged := make([]string, n)
	allOk := true
	var promotedSource *PipelineSourceSnapshot
	var requiredFailures []string
	for j := range collected {
		r := collected[j]
		if r.err != nil {
			if parallel[r.idx].ContinueOnError {
				if e.fileLogger != nil {
					e.fileLogger.Printf("[Pipeline] Parallel sub-step %s[%d] failed but continue_on_error=true: %v", step.Name, r.idx, r.err)
				}
				merged[r.idx] = fmt.Sprintf("DATA_STATUS: fetch_failed\n支线：%s\n原因：%v", parallel[r.idx].Name, r.err)
				continue
			}
			if e.fileLogger != nil {
				e.fileLogger.Printf("[Pipeline] Parallel sub-step %s[%d] failed: %v", step.Name, r.idx, r.err)
			}
			requiredFailures = append(requiredFailures, fmt.Sprintf("%s: %v", parallel[r.idx].Name, r.err))
			allOk = false
		}
		merged[r.idx] = r.output
		if r.sourceSnapshot != nil {
			copy := *r.sourceSnapshot
			promotedSource = &copy
		}
	}

	if !allOk {
		finishWithError("failed", fmt.Errorf("parallel step %q failed: %s", step.Name, strings.Join(requiredFailures, "; ")))
		return
	}

	// Merge results into the parent step
	var mergedBuilder strings.Builder
	fmt.Fprintf(&mergedBuilder, "## 并行步骤结果: %s\n\n", step.Name)
	for j := 0; j < n; j++ {
		fmt.Fprintf(&mergedBuilder, "### %s\n%s\n\n", parallel[j].Name, merged[j])
	}
	mergedOutput := mergedBuilder.String()

	exec.mu.Lock()
	if parentIdx >= 0 && parentIdx < len(exec.Steps) {
		exec.Steps[parentIdx].Output = mergedOutput
		exec.Steps[parentIdx].Status = "completed"
		exec.Steps[parentIdx].DurationMs = time.Since(startedAt).Milliseconds()
		exec.Steps[parentIdx].SourceSnapshot = promotedSource
		e.ensurePipelineCheckpointLocked(exec)
		exec.Checkpoint.StepIndex = parentIdx
		exec.Checkpoint.StepName = step.Name
		exec.Checkpoint.StepRevision = exec.Steps[parentIdx].Revision
		exec.Checkpoint.Phase = pipelineCheckpointPhaseStepFinalized
		exec.Checkpoint.TurnOutput = mergedOutput
		exec.Checkpoint.TurnStatus = "completed"
		exec.Checkpoint.UpdatedAt = time.Now()
	}
	exec.mu.Unlock()
	if promotedSource != nil && e.fileLogger != nil {
		e.fileLogger.Printf("[Pipeline] Parallel step %d (%s) froze source: %s", parentIdx, step.Name, pipelineSourceSnapshotSummary(promotedSource))
	}

	// Parallel outputs can be much larger than the inline context budget. Persist
	// the complete merged result just like a normal step so downstream quality
	// gates can read every evidence branch instead of seeing a truncated prefix.
	if parentIdx >= 0 && parentIdx < len(exec.Steps) {
		e.saveStepOutputToFile(exec, parentIdx, mergedOutput)
		e.materializeClaimedArtifacts(exec, parentIdx, mergedOutput)
		e.saveToDB(exec)
	}

	e.broadcastPipelineEvent(exec.ID, "step_done", map[string]interface{}{
		"step_index": parentIdx, "step_name": step.Name, "parallel": n,
	})
}

func (e *PipelineExecutor) executeParallelSubStep(ctx context.Context, exec *PipelineExecution, subStep PipelineStep, parentIdx, branchIdx int, upstreamSteps []StepResult, input, sessionID string) (string, *PipelineSourceSnapshot, error) {
	auditIdx := parallelAuditStepIndex(parentIdx, branchIdx)
	sourceRuntimeContext, err := e.preparePipelineSourceRuntimeContext(exec, parentIdx, auditIdx)
	if err != nil {
		return "", nil, fmt.Errorf("source contract: %w", err)
	}
	prompt := renderStepPrompt(appendStepToolBoundaryInstruction(buildStepExecutionPrompt(subStep.Prompt), &subStep), parentIdx, upstreamSteps, input)
	if previousContext := buildPreviousStepContext(parentIdx, upstreamSteps); previousContext != "" {
		prompt = fmt.Sprintf("%s\n\n---\n\n## 上游步骤产出\n%s\n\n## 上游产出文件\n%s",
			prompt, previousContext, buildPreviousStepFileList(exec, parentIdx))
	}
	if evidenceRule := evidenceBoundaryInstruction(ctx); evidenceRule != "" {
		prompt = prompt + "\n\n---\n\n" + evidenceRule
	}
	prompt = appendPipelineTargetScopeInstruction(prompt, input)
	// Keep the sub-step at its real parent position in the synthetic step slice.
	// Runtime contract rendering uses that position to locate an upstream frozen
	// source snapshot, so collapsing every branch to index zero would silently
	// omit downstream source-read instructions.
	contractSteps := make([]PipelineStep, parentIdx+1)
	contractSteps[parentIdx] = subStep
	prompt = appendPipelineRuntimeContractInstructions(prompt, exec, contractSteps, parentIdx, input)
	prompt = appendPipelineSourceRuntimeContext(prompt, sourceRuntimeContext)
	if exec.RuntimePipeline != nil {
		prompt = appendAIEmployeeClarificationProtocol(prompt, *exec.RuntimePipeline, &subStep)
	}

	output, err := e.executeStep(ctx, exec, &subStep, auditIdx, prompt, sessionID)
	if err != nil {
		return output, nil, err
	}

	validationExec := exec.parallelContractExecutionView(auditIdx)
	sourceSnapshot, contractErr := e.validatePipelineStepContracts(validationExec, &subStep, parentIdx, input, output)
	if contractErr == nil {
		return output, sourceSnapshot, nil
	}
	if e.fileLogger != nil {
		e.fileLogger.Printf("[Pipeline] Parallel sub-step %d[%d] (%s) contract repair: %v", parentIdx, branchIdx, subStep.Name, contractErr)
	}
	repairPrompt := prompt + "\n\n---\n\n" + pipelineContractRepairInstruction(exec, contractSteps, parentIdx, input, contractErr)
	output, err = e.executeStep(ctx, exec, &subStep, auditIdx, repairPrompt, sessionID)
	if err != nil {
		return output, nil, err
	}
	validationExec = exec.parallelContractExecutionView(auditIdx)
	sourceSnapshot, contractErr = e.validatePipelineStepContracts(validationExec, &subStep, parentIdx, input, output)
	if contractErr != nil {
		return output, nil, fmt.Errorf("pipeline contract failed after repair: %w", contractErr)
	}
	return output, sourceSnapshot, nil
}

func (e *PipelineExecutor) waitForApproval(ctx context.Context, exec *PipelineExecution, step *PipelineStep) string {
	// Parse escalation timeout
	timeout := 30 * time.Minute // default
	if step.EscalationAfter != "" {
		if d, err := time.ParseDuration(step.EscalationAfter); err == nil {
			timeout = d
		}
	}

	// Half-time warning
	halfCh := time.After(timeout / 2)

	for {
		select {
		case <-ctx.Done():
			return ""
		case feedback := <-exec.approveCh:
			return feedback
		case <-halfCh:
			halfCh = nil // only fire once
			msg := fmt.Sprintf("⏰ 审批超时预警: %s 步骤 '%s' 已等待 %v，%v 后将自动升级到 %s",
				exec.PipelineName, step.Name, timeout/2, timeout/2, step.EscalateTo)
			eventbus.PushSystemMessage(msg, "warn")
			e.fileLogger.Printf("[Pipeline] %s", msg)
		case <-time.After(timeout):
			msg := fmt.Sprintf("🚨 审批超时: %s 步骤 '%s' 已超时(%v)，已自动升级到 %s",
				exec.PipelineName, step.Name, timeout, step.EscalateTo)
			eventbus.PushSystemMessage(msg, "error")
			e.fileLogger.Printf("[Pipeline] %s", msg)
			return "" // timeout → cancel + escalate
		}
	}
}

// ====================== Step Execution ======================

func (e *PipelineExecutor) executeStep(ctx context.Context, exec *PipelineExecution, step *PipelineStep, stepIdx int, prompt string, sessionID string) (string, error) {
	startTime := time.Now()
	defer func() {
		if exec != nil && stepIdx >= 0 && stepIdx < len(exec.Steps) {
			exec.Steps[stepIdx].DurationMs = time.Since(startTime).Milliseconds()
		}
	}()
	if step.ProfessionalAgent != nil {
		return e.executeProfessionalAgentStep(ctx, exec, step, stepIdx, sessionID)
	}
	if e.agentFactory == nil {
		return "", fmt.Errorf("agentFactory is nil — cannot execute step '%s'", step.Name)
	}

	stepRevision := 1
	if exec != nil {
		exec.mu.Lock()
		if stepIdx >= 0 && stepIdx < len(exec.Steps) && exec.Steps[stepIdx].Revision > 0 {
			stepRevision = exec.Steps[stepIdx].Revision
		} else if parentIdx, _, ok := decodeParallelAuditStepIndex(stepIdx); ok && parentIdx < len(exec.Steps) && exec.Steps[parentIdx].Revision > 0 {
			stepRevision = exec.Steps[parentIdx].Revision
		}
		exec.mu.Unlock()
	}

	maxIter := step.MaxIter
	if maxIter <= 0 {
		maxIter = 5
	}

	config := AgentConfig{
		Name:          fmt.Sprintf("pipe_%s_%s", exec.PipelineName, step.Name),
		MaxIterations: maxIter,
		Model:         step.Model,
	}
	if isolated, _ := ctx.Value("pipeline_isolated_context").(bool); isolated {
		hasMemory := false
		config.HasMemory = &hasMemory
	}

	// DE Pipeline: resolve model from Provider registry only when the step did
	// not explicitly request a model. Explicit step.model must win.
	cap := step.Capability
	if cap == "" {
		cap = "text-to-text"
	}
	if strings.TrimSpace(step.Model) != "" {
		if e.fileLogger != nil {
			e.fileLogger.Printf("[Pipeline] Step %s explicit model: %s (cap=%s)", step.Name, step.Model, cap)
		}
	} else if deID, ok := ctx.Value("ae_employee_id").(string); ok && deID != "" {
		if mc := ResolveModelForDE(deID, cap); mc != nil {
			config.Model = mc.ModelName
			if e.agentFactory != nil {
				e.agentFactory.UpsertRuntimeModelConfig(mc.ModelName, *mc)
			}
			if e.fileLogger != nil {
				e.fileLogger.Printf("[Pipeline] Step %s DE model: %s (cap=%s)", step.Name, mc.ModelName, cap)
			}
		}
	}

	// AE SystemPrompt → identity section at end of system prompt
	if sysPrompt, ok := ctx.Value("ae_system_prompt").(string); ok && sysPrompt != "" {
		config.SystemMessage = sysPrompt
		if n, ok := ctx.Value("ae_name").(string); ok && n != "" {
			config.AEName = n
		}
	}

	stepWorkspace := strings.TrimSpace(projectctx.ResolvedWorkspace(ctx))
	if stepWorkspace == "" && exec != nil {
		stepWorkspace = strings.TrimSpace(exec.ResolvedWorkspace)
	}
	policyInput := pipelineUserPolicyInput(ctx, exec)
	subAgent, err := e.agentFactory.GetOrCreateSubAgentInWorkspace(config, prompt, stepWorkspace)
	if err != nil {
		return "", fmt.Errorf("create sub-agent for step '%s': %w", step.Name, err)
	}

	allowedTools := pipelineToolsAllowedByUserPolicy(pipelineStepExecutionTools(exec, step, stepIdx), policyInput)
	if llmAgent, ok := subAgent.(*LLMAgent); ok {
		// The freshly-created registry is scoped to this execution workspace.
		// Reusing tools from a global resident agent here would move shell/file
		// operations and embedded Skill materialization outside the tenant.
		stepRegistry := e.buildStepRegistryFrom(llmAgent.toolRegistry, allowedTools, exec, stepIdx)
		if isolated, _ := ctx.Value("pipeline_isolated_context").(bool); isolated {
			llmAgent.hasMemory = false
			llmAgent.memory = nil
			llmAgent.skillsLoader = nil
		}
		if step.NoTools {
			llmAgent.toolRegistry = stepRegistry
			llmAgent.skillsLoader = nil
			e.fileLogger.Printf("[Pipeline] Step '%s' tools: none (maxIter=%d)", step.Name, maxIter)
		} else if len(step.Tools) > 0 {
			llmAgent.toolRegistry = stepRegistry
			e.fileLogger.Printf("[Pipeline] Step '%s' tools: configured %v (maxIter=%d)", step.Name, step.Tools, maxIter)
		} else {
			llmAgent.toolRegistry = stepRegistry
			e.fileLogger.Printf("[Pipeline] Step '%s' tools: all (blacklist) (maxIter=%d)", step.Name, maxIter)
		}
		prompt, err = e.activateRequiredPipelineSkills(ctx, step, stepRegistry, prompt)
		if err != nil {
			return "", fmt.Errorf("activate required skills for step '%s': %w", step.Name, err)
		}
		allowedTools = pipelineStepModelExecutionTools(step, allowedTools)
		if len(step.RequiredSkills) > 0 {
			// Required skills are activated by the host above. Removing the
			// activation tool from the model-visible registry prevents a stage
			// from loading unrelated skills and escaping its assigned role.
			stepRegistry.Unregister("use_skill")
		}
	} else if len(step.RequiredSkills) > 0 {
		return "", fmt.Errorf("step '%s' requires skills but its agent does not support tool-backed skill activation", step.Name)
	}
	if exec != nil && exec.RuntimePipeline != nil {
		prompt = appendAIEmployeeInteractiveHandoffInstruction(prompt, *exec.RuntimePipeline, step, stepIdx)
	}

	// Per-step KB slugs override DE-level KBs
	stepCtx := context.WithValue(ctx, "session_id", sessionID)
	// A pipeline sub-agent is responsible for one bounded stage, not for the
	// user's entire multi-stage request. Let the agent finalizer judge its
	// response against that stage boundary so a valid hand-off is not mistaken
	// for a progress-only answer and expanded into unrelated extra work.
	stepCtx = context.WithValue(stepCtx, "pipeline_step_execution", true)
	stepCtx = permissionpolicy.WithUserToolPolicyInput(stepCtx, policyInput)
	stepCtx = context.WithValue(
		stepCtx,
		presentationProductionStateContextKey{},
		exec.presentationStateForStep(stepCtx, stepIdx, policyInput),
	)
	if step.OutputTokenLimit > 0 {
		stepCtx = modelruntime.WithOutputTokenLimit(stepCtx, step.OutputTokenLimit)
	}
	if step.TimeoutSeconds > 0 {
		var cancel context.CancelFunc
		stepCtx, cancel = context.WithTimeout(stepCtx, time.Duration(step.TimeoutSeconds)*time.Second)
		defer cancel()
		if e.fileLogger != nil {
			e.fileLogger.Printf("[Pipeline] Step '%s' effective deadline: %ds", step.Name, step.TimeoutSeconds)
		}
	}
	if exec != nil && exec.PipelineName == "ppt-polish" && strings.TrimSpace(step.Name) == "视觉润色" {
		// PPT construction commonly needs several tool turns and a longer final
		// synthesis than an ordinary text step. Give each model call one bounded
		// five-minute attempt instead of repeating two full two-minute timeouts.
		stepCtx = modelruntime.WithCallTimeoutOverride(stepCtx, 5*time.Minute)
		stepCtx = modelruntime.WithCallMaxAttemptsOverride(stepCtx, 1)
	}
	// A pipeline allowlist is an explicit host authorization, not a discovery
	// hint. Expose full schemas for every assigned tool, including prompt-hidden
	// tools such as extract, while keeping the registry itself narrowly scoped.
	stepCtx = toolregistry.WithRequiredDefinitions(stepCtx, allowedTools...)
	stepCtx = toolregistry.WithRequiredObligations(stepCtx, step.RequiredTools...)
	if len(step.KBSlugs) > 0 && gatewayDB != nil {
		// Resolve step-level KB slugs using the DE tenant
		tenantID, _ := ctx.Value("rag_tenant_id").(string)
		stepKBIDs := resolveKBSlugs(step.KBSlugs, tenantID)
		if len(stepKBIDs) > 0 {
			stepCtx = context.WithValue(stepCtx, "rag_kb_ids", stepKBIDs)
		}
	}
	// Retry with exponential backoff if step has Repeat config. Each bounded
	// attempt has a distinct durable turn identity; a restart of that attempt
	// reuses its checkpoint, while the next retry cannot collide with it.
	maxAttempts := 1
	if step.Repeat != nil && step.Repeat.Max > 1 {
		maxAttempts = step.Repeat.Max
	}
	var output string
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		turnKey := pipelineTurnCheckpointKey(exec.ID, stepIdx, stepRevision, attempt, step, prompt)
		turnTaskID := pipelineStepExecutionID(exec.ID, stepIdx, stepRevision, attempt, turnKey)
		turnState := e.pipelineTurnState(exec, turnKey)
		if turnState != nil && strings.TrimSpace(turnState.TaskID) != "" {
			turnTaskID = strings.TrimSpace(turnState.TaskID)
		}

		if turnState != nil && (turnState.Phase == "success" || turnState.Phase == "failed") {
			output = turnState.Output
			if turnState.Phase == "failed" {
				err = agentresult.NewError(turnState.Error, output)
			} else {
				err = nil
			}
		} else if completedOutput, completedStatus, transcriptPath, ok := loadLatestCompletedPipelineTurn(stepWorkspace, turnTaskID); ok {
			output = completedOutput
			e.completePipelineTurn(exec, turnKey, turnTaskID, attempt, stepIdx, stepRevision, completedOutput, completedStatus, "", "", transcriptPath)
			output, err = agentresult.Terminal(completedOutput, completedStatus, "pipeline turn failed before restart")
		} else {
			resumeCheckpoint, checkpointPath, _ := loadLatestPipelineTurnCheckpoint(stepWorkspace, turnTaskID)
			receiptTaskID := firstNonEmpty(taskIDFromContext(stepCtx), turnTaskID)
			if resumeCheckpoint != nil {
				receiptTaskID = firstNonEmpty(resumeCheckpoint.ReceiptTaskID, resumeCheckpoint.TaskID, turnTaskID)
				stepCtx = sessioncore.WithTurnResumeState(stepCtx, resumeCheckpoint, receiptTaskID, "")
			}
			e.beginPipelineTurn(exec, turnKey, turnTaskID, receiptTaskID, attempt, stepIdx, stepRevision, checkpointPath)
			attemptCtx := sessioncore.WithTurnPersistenceID(stepCtx, turnTaskID)
			attemptCtx = sessioncore.WithTurnPersistencePathsObserver(attemptCtx, func(checkpointPath, transcriptPath string) {
				e.publishPipelineTurnPaths(exec, turnKey, checkpointPath, transcriptPath)
			})
			output, err = subAgent.Generate(attemptCtx, prompt)

			transcriptPath := ""
			if llmAgent, ok := subAgent.(*LLMAgent); ok && llmAgent.transcript != nil {
				checkpointPath = llmAgent.transcript.CheckpointPath()
				transcriptPath = llmAgent.transcript.FilePath()
			}
			status := "success"
			errText := ""
			if err != nil {
				status = "failed"
				errText = err.Error()
				if _, waiting := approvalcore.AsApprovalRequiredError(err); waiting {
					status = "running"
				}
			}
			e.completePipelineTurn(exec, turnKey, turnTaskID, attempt, stepIdx, stepRevision, output, status, errText, checkpointPath, transcriptPath)
		}
		if err == nil || attempt >= maxAttempts {
			break
		}
		backoff := time.Duration(attempt*attempt) * time.Second
		e.fileLogger.Printf("[Pipeline] Step '%s' attempt %d/%d failed: %v, retrying in %v", step.Name, attempt, maxAttempts, err, backoff)
		e.sleepBeforeRetry(backoff)
	}
	return output, err
}

func (e *PipelineExecutor) pipelineTurnState(exec *PipelineExecution, key string) *PipelineTurnCheckpoint {
	if e == nil || exec == nil || strings.TrimSpace(key) == "" {
		return nil
	}
	exec.mu.Lock()
	defer exec.mu.Unlock()
	checkpoint := e.ensurePipelineCheckpointLocked(exec)
	turn := checkpoint.Turns[key]
	if turn == nil {
		return nil
	}
	clone := *turn
	return &clone
}

func (e *PipelineExecutor) beginPipelineTurn(exec *PipelineExecution, key, turnTaskID, receiptTaskID string, attempt, stepIndex, stepRevision int, checkpointPath string) {
	if exec == nil {
		return
	}
	now := time.Now()
	cursorStepIndex := stepIndex
	if parentIndex, _, ok := decodeParallelAuditStepIndex(stepIndex); ok {
		cursorStepIndex = parentIndex
	}
	exec.mu.Lock()
	checkpoint := e.ensurePipelineCheckpointLocked(exec)
	checkpoint.StepIndex = cursorStepIndex
	checkpoint.StepRevision = stepRevision
	checkpoint.Attempt = attempt
	checkpoint.Phase = pipelineCheckpointPhaseStepRunning
	checkpoint.ActiveTurnKey = key
	checkpoint.TurnTaskID = turnTaskID
	checkpoint.ReceiptTaskID = receiptTaskID
	checkpoint.TurnCheckpoint = strings.TrimSpace(checkpointPath)
	checkpoint.TurnTranscript = ""
	checkpoint.TurnOutput = ""
	checkpoint.TurnStatus = "running"
	checkpoint.UpdatedAt = now
	checkpoint.Turns[key] = &PipelineTurnCheckpoint{
		Key:            key,
		TaskID:         turnTaskID,
		ReceiptTaskID:  receiptTaskID,
		StepIndex:      stepIndex,
		StepRevision:   stepRevision,
		Attempt:        attempt,
		Phase:          "running",
		CheckpointPath: strings.TrimSpace(checkpointPath),
		UpdatedAt:      now,
	}
	exec.mu.Unlock()
	e.saveToDB(exec)
}

func (e *PipelineExecutor) publishPipelineTurnPaths(exec *PipelineExecution, key, checkpointPath, transcriptPath string) {
	if e == nil || exec == nil || strings.TrimSpace(key) == "" {
		return
	}
	checkpointPath = strings.TrimSpace(checkpointPath)
	transcriptPath = strings.TrimSpace(transcriptPath)
	if checkpointPath == "" && transcriptPath == "" {
		return
	}
	now := time.Now()
	exec.mu.Lock()
	checkpoint := e.ensurePipelineCheckpointLocked(exec)
	turn := checkpoint.Turns[key]
	if turn == nil {
		turn = &PipelineTurnCheckpoint{Key: key, Phase: "running"}
		checkpoint.Turns[key] = turn
	}
	if checkpointPath != "" {
		turn.CheckpointPath = checkpointPath
		checkpoint.TurnCheckpoint = checkpointPath
	}
	if transcriptPath != "" {
		turn.TranscriptPath = transcriptPath
		checkpoint.TurnTranscript = transcriptPath
	}
	turn.UpdatedAt = now
	checkpoint.ActiveTurnKey = key
	checkpoint.UpdatedAt = now
	exec.mu.Unlock()
	e.saveToDB(exec)
}

func (e *PipelineExecutor) completePipelineTurn(exec *PipelineExecution, key, turnTaskID string, attempt, stepIndex, stepRevision int, output, status, errText, checkpointPath, transcriptPath string) {
	if exec == nil {
		return
	}
	status = strings.TrimSpace(status)
	if status == "" {
		status = "failed"
	}
	now := time.Now()
	cursorStepIndex := stepIndex
	if parentIndex, _, ok := decodeParallelAuditStepIndex(stepIndex); ok {
		cursorStepIndex = parentIndex
	}
	exec.mu.Lock()
	checkpoint := e.ensurePipelineCheckpointLocked(exec)
	turn := checkpoint.Turns[key]
	if turn == nil {
		turn = &PipelineTurnCheckpoint{Key: key}
		checkpoint.Turns[key] = turn
	}
	turn.TaskID = turnTaskID
	turn.StepIndex = stepIndex
	turn.StepRevision = stepRevision
	turn.Attempt = attempt
	turn.Phase = status
	turn.Output = output
	turn.Error = strings.TrimSpace(errText)
	if checkpointPath = strings.TrimSpace(checkpointPath); checkpointPath != "" {
		turn.CheckpointPath = checkpointPath
	}
	if transcriptPath = strings.TrimSpace(transcriptPath); transcriptPath != "" {
		turn.TranscriptPath = transcriptPath
	}
	turn.UpdatedAt = now
	checkpoint.StepIndex = cursorStepIndex
	checkpoint.StepRevision = stepRevision
	checkpoint.Attempt = attempt
	checkpoint.Phase = pipelineCheckpointPhasePostProcessing
	checkpoint.ActiveTurnKey = key
	checkpoint.TurnTaskID = turnTaskID
	checkpoint.ReceiptTaskID = firstNonEmpty(turn.ReceiptTaskID, turnTaskID)
	checkpoint.TurnCheckpoint = turn.CheckpointPath
	checkpoint.TurnTranscript = turn.TranscriptPath
	checkpoint.TurnOutput = output
	checkpoint.TurnStatus = status
	checkpoint.UpdatedAt = now
	prunePipelineTurnCheckpoints(checkpoint)
	exec.mu.Unlock()
	e.saveToDB(exec)
}

func (e *PipelineExecutor) activateRequiredPipelineSkills(ctx context.Context, step *PipelineStep, registry *toolregistry.Registry, prompt string) (string, error) {
	if step == nil || len(step.RequiredSkills) == 0 {
		return prompt, nil
	}
	if step.NoTools {
		return "", fmt.Errorf("required skills cannot run in a no-tools step")
	}
	if registry == nil {
		return "", fmt.Errorf("tool registry is unavailable")
	}
	tool, ok := registry.Get("use_skill")
	if !ok {
		return "", fmt.Errorf("use_skill is not available in the step tool allowlist")
	}

	seen := make(map[string]bool, len(step.RequiredSkills))
	var activated []string
	var sections []string
	var runtimeDirs []string
	for _, configuredName := range step.RequiredSkills {
		skillName := strings.TrimSpace(configuredName)
		key := strings.ToLower(skillName)
		if skillName == "" || seen[key] {
			continue
		}
		seen[key] = true
		params := map[string]interface{}{"skill_name": skillName}
		result, err := tool.Execute(ctx, params)
		if !ToolExecutionSucceededForTool("use_skill", result, err) {
			if err != nil {
				return "", fmt.Errorf("%s: %w", skillName, err)
			}
			return "", fmt.Errorf("%s: %s", skillName, truncate(strings.TrimSpace(result), 600))
		}
		activated = append(activated, skillName)
		sections = append(sections, fmt.Sprintf("### %s\n%s", skillName, strings.TrimSpace(result)))
		if runtimeDir := skilldomain.RuntimeDirectoryFromActivation(result); runtimeDir != "" {
			runtimeDirs = append(runtimeDirs, runtimeDir)
		}
	}
	if len(activated) == 0 {
		return "", fmt.Errorf("required_skills contains no valid skill name")
	}
	if e != nil && e.fileLogger != nil {
		e.fileLogger.Printf("[Pipeline] Host activated required skills for step '%s': %s", step.Name, strings.Join(activated, ", "))
	}
	// A pipeline stage often quotes the Skill's portable `<Runtime Directory>`
	// command before use_skill has run. Once the host has activated exactly one
	// required Skill, replace that portable token with the returned tenant-scoped
	// runtime path. Leaving the token literal makes the model probe directories
	// and retry shell commands even though the host already knows the path.
	if len(runtimeDirs) == 1 {
		prompt = strings.ReplaceAll(prompt, "<Runtime Directory>", runtimeDirs[0])
	}
	return strings.TrimSpace(prompt + "\n\n---\n\n## Host-activated required skills\n" +
		"The host already executed use_skill with the exact configured top-level skill_name for every skill below. Follow the returned instructions and do not call use_skill again merely to reload them.\n\n" +
		strings.Join(sections, "\n\n")), nil
}

func stepToolAllowlist(step *PipelineStep) []string {
	if step == nil {
		return nil
	}
	if step.NoTools {
		return []string{}
	}
	if len(step.Tools) > 0 {
		return expandWebResearchTools(step.Tools)
	}
	return nil
}

func pipelineToolsAllowedByUserPolicy(tools []string, input string) []string {
	policy := permissionpolicy.DeriveUserToolPolicy(input)
	if policy == (permissionpolicy.UserToolPolicy{}) || len(tools) == 0 {
		return tools
	}
	filtered := make([]string, 0, len(tools))
	for _, name := range tools {
		if blocked, _ := policy.Blocks(ToolCall{Name: name}, nil, toolcontract.IsReadOnlyToolName); blocked {
			continue
		}
		filtered = append(filtered, name)
	}
	return filtered
}

func pipelineUserPolicyInput(ctx context.Context, exec *PipelineExecution) string {
	if frozen := strings.TrimSpace(permissionpolicy.UserToolPolicyInput(ctx, "")); frozen != "" {
		return frozen
	}
	if spec, ok := aiEmployeeIntakeSpecFromContext(ctx); ok && strings.TrimSpace(spec.OriginalTask) != "" {
		return strings.TrimSpace(spec.OriginalTask)
	}
	if exec == nil {
		return ""
	}
	if exec.RuntimePipeline != nil && strings.TrimSpace(exec.RuntimePipeline.AIEmployeeOriginalTask) != "" {
		return strings.TrimSpace(exec.RuntimePipeline.AIEmployeeOriginalTask)
	}
	return strings.TrimSpace(exec.Input)
}

func pipelineStepModelExecutionTools(step *PipelineStep, allowedTools []string) []string {
	if step == nil || len(step.RequiredSkills) == 0 || len(allowedTools) == 0 {
		return allowedTools
	}
	filtered := make([]string, 0, len(allowedTools))
	for _, name := range allowedTools {
		if strings.EqualFold(strings.TrimSpace(name), "use_skill") {
			continue
		}
		filtered = append(filtered, name)
	}
	return filtered
}

func expandWebResearchTools(tools []string) []string {
	if len(tools) == 0 {
		return tools
	}
	seen := make(map[string]bool, len(tools)+4)
	out := make([]string, 0, len(tools)+4)
	hasWebResearch := false
	for _, tool := range tools {
		out = append(out, tool)
		seen[tool] = true
		if tool == "web_fetch" || tool == "web_search" {
			hasWebResearch = true
		}
	}
	if !hasWebResearch {
		return out
	}
	for _, tool := range []string{"web_targets", "web_navigate", "web_page", "web_close"} {
		if !seen[tool] {
			out = append(out, tool)
			seen[tool] = true
		}
	}
	return out
}

func (e *PipelineExecutor) buildStepRegistry(allowedNames []string, exec *PipelineExecution, stepIdx int) *toolregistry.Registry {
	return e.buildStepRegistryFrom(e.getFullToolRegistry(), allowedNames, exec, stepIdx)
}

func (e *PipelineExecutor) buildStepRegistryFrom(fullRegistry *toolregistry.Registry, allowedNames []string, exec *PipelineExecution, stepIdx int) *toolregistry.Registry {
	reg := newAppToolRegistry()
	if fullRegistry == nil && allowedNames == nil {
		return reg
	}
	isolatedWorkspace := e.pipelineUsesIsolatedWorkspace(exec)

	// Blacklist-first: nil means "use all tools"; empty slice means "allow none".
	var names []string
	if allowedNames == nil {
		names = fullRegistry.AllNames()
	} else {
		names = allowedNames
	}

	for _, name := range names {
		if pipelineAlwaysBlacklist[name] {
			continue
		}
		if pipelineBlacklist[name] && allowedNames == nil {
			continue
		}
		if name == "db_query" {
			if tool, ok := e.resolvePipelineDatabaseTool(exec, stepIdx); ok {
				reg.Register(&auditTool{inner: tool, exec: exec, stepIdx: stepIdx, fileLogger: e.fileLogger})
				continue
			}
		}
		tool, ok := resolvePipelineToolInRegistry(fullRegistry, name)
		if !ok && !isolatedWorkspace {
			tool, ok = e.resolvePipelineTool(fullRegistry, name)
		}
		if ok {
			tool = guardLeadResearchEvidenceTool(tool, exec, stepIdx, e.gateway)
			auditWrapper := &auditTool{
				inner:      tool,
				exec:       exec,
				stepIdx:    stepIdx,
				fileLogger: e.fileLogger,
			}
			reg.Register(auditWrapper)
		} else if allowedNames != nil && e.fileLogger != nil {
			e.fileLogger.Printf("[Pipeline] Step %d configured tool not registered: %s", stepIdx, name)
		}
	}
	return reg
}

func (e *PipelineExecutor) resolvePipelineDatabaseTool(exec *PipelineExecution, stepIdx int) (Tool, bool) {
	if exec == nil || len(exec.databaseConnections) == 0 {
		return nil, false
	}
	connectionID := strings.TrimSpace(exec.stepDatabaseConnections[stepIdx])
	if connectionID == "" && len(exec.databaseConnections) == 1 {
		for id := range exec.databaseConnections {
			connectionID = id
		}
	}
	profile, ok := exec.databaseConnections[connectionID]
	if !ok {
		if e.fileLogger != nil {
			e.fileLogger.Printf("[Pipeline] Step %d has no authorized database connection", stepIdx)
		}
		return nil, false
	}
	exec.databaseMu.Lock()
	defer exec.databaseMu.Unlock()
	if tool := exec.databaseTools[connectionID]; tool != nil {
		return tool, true
	}
	outputDir, _ := e.storageArtifactPathForExecution(exec, ".openkitty/storage/data")
	tool := NewPipelineDBQueryTool(profile, e.fileLogger, outputDir)
	exec.databaseTools[connectionID] = tool
	return tool, true
}

func resolvePipelineToolInRegistry(registry *toolregistry.Registry, name string) (Tool, bool) {
	if registry == nil {
		return nil, false
	}
	if tool, ok := registry.Get(name); ok {
		return tool, true
	}
	if name == "persistent_shell" {
		if tool, ok := registry.Get("cli"); ok {
			return &toolAlias{name: "persistent_shell", inner: tool}, true
		}
	}
	return nil, false
}

func (e *PipelineExecutor) pipelineUsesIsolatedWorkspace(exec *PipelineExecution) bool {
	if e == nil || e.agentFactory == nil || exec == nil || strings.TrimSpace(exec.ResolvedWorkspace) == "" {
		return false
	}
	return !projectctx.SameWorkspace(exec.ResolvedWorkspace, e.agentFactory.workspace)
}

func (e *PipelineExecutor) resolvePipelineTool(fullRegistry *toolregistry.Registry, name string) (Tool, bool) {
	if tool, ok := resolvePipelineToolInRegistry(fullRegistry, name); ok {
		return tool, true
	}
	if tool, ok := e.findPipelineToolInAgentRegistries(name); ok {
		return tool, true
	}
	if name == "persistent_shell" {
		if tool, ok := e.findPipelineToolInAgentRegistries("cli"); ok {
			return &toolAlias{name: "persistent_shell", inner: tool}, true
		}
	}
	return nil, false
}

func (e *PipelineExecutor) ToolAvailable(name string) bool {
	name = strings.TrimSpace(name)
	if e == nil || e.agentFactory == nil || name == "" || pipelineAlwaysBlacklist[name] {
		return false
	}
	fullRegistry := e.getFullToolRegistry()
	_, ok := e.resolvePipelineTool(fullRegistry, name)
	return ok
}

func (e *PipelineExecutor) MissingTools(names []string) []string {
	if e == nil || e.agentFactory == nil {
		return nil
	}
	seen := map[string]bool{}
	var missing []string
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		if !e.ToolAvailable(name) {
			missing = append(missing, name)
		}
	}
	sort.Strings(missing)
	return missing
}

func (e *PipelineExecutor) findPipelineToolInAgentRegistries(name string) (Tool, bool) {
	if e == nil || e.agentFactory == nil {
		return nil, false
	}
	e.agentFactory.mu.RLock()
	defer e.agentFactory.mu.RUnlock()
	for _, agent := range e.agentFactory.agents {
		llmAgent, ok := agent.(*LLMAgent)
		if !ok || llmAgent.toolRegistry == nil {
			continue
		}
		if tool, ok := llmAgent.toolRegistry.Get(name); ok {
			return tool, true
		}
	}
	return nil, false
}

// ====================== Audit Tool Wrapper ======================

type toolAlias struct {
	name  string
	inner Tool
}

func (t *toolAlias) Name() string                       { return t.name }
func (t *toolAlias) Description() string                { return t.inner.Description() }
func (t *toolAlias) Parameters() map[string]interface{} { return t.inner.Parameters() }
func (t *toolAlias) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	return t.inner.Execute(ctx, params)
}

func (t *toolAlias) MaxResultSize() int {
	if sl, ok := t.inner.(SizeLimitedTool); ok {
		return sl.MaxResultSize()
	}
	return 8000
}

// auditTool wraps any Tool with audit-trail recording. Every Execute call is
// logged as a ToolCallRecord in the parent PipelineExecution, giving full
// compliance visibility into what each pipeline step's agent did.
type auditTool struct {
	inner      Tool
	exec       *PipelineExecution
	stepIdx    int
	fileLogger *log.Logger
}

func (t *auditTool) Name() string                       { return t.inner.Name() }
func (t *auditTool) Description() string                { return t.inner.Description() }
func (t *auditTool) Parameters() map[string]interface{} { return t.inner.Parameters() }

// Preserve optional tool capabilities across the audit wrapper. Dropping these
// interfaces makes read_file fall back to the generic 8KB result limit and can
// force an agent into pointless save-then-read loops; it also changes parallel
// safety decisions for otherwise read-only or path-scoped tools.
func (t *auditTool) MaxResultSize() int {
	if limited, ok := t.inner.(SizeLimitedTool); ok {
		return limited.MaxResultSize()
	}
	return 8000
}

func (t *auditTool) IsReadOnly() bool {
	if readOnly, ok := t.inner.(ReadOnlyTool); ok {
		return readOnly.IsReadOnly()
	}
	return toolcontract.IsReadOnlyToolName(t.inner.Name())
}

func (t *auditTool) IsNeverParallel() bool {
	if serial, ok := t.inner.(NeverParallelTool); ok {
		return serial.IsNeverParallel()
	}
	return false
}

func (t *auditTool) ScopePath(args map[string]interface{}) string {
	if scoped, ok := t.inner.(PathScopedTool); ok {
		return scoped.ScopePath(args)
	}
	return ""
}

func (t *auditTool) ResultIsContentPayload() bool {
	payload, ok := t.inner.(ContentPayloadTool)
	return ok && payload.ResultIsContentPayload()
}

func (t *auditTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	start := time.Now()
	paramsJSON, _ := json.Marshal(params)
	toolName := t.inner.Name()
	cacheKey := ""

	if toolName == "web_fetch" && t.exec != nil {
		cacheKey = toolName + ":" + string(paramsJSON)
		if cached, ok := t.exec.getCachedToolResult(cacheKey); ok {
			record := ToolCallRecord{
				Timestamp:  start,
				ToolName:   toolName,
				Params:     truncate(string(paramsJSON), 500),
				Result:     truncate(cached, 1000),
				Status:     "success",
				DurationMs: 0,
				Cached:     true,
			}
			if t.exec != nil {
				t.exec.recordToolCall(t.stepIdx, record)
			}
			if t.fileLogger != nil {
				t.fileLogger.Printf("[Pipeline:audit] Step[%d] %s(%s) -> cache hit", t.stepIdx, toolName, record.Params)
			}
			return cached, nil
		}
	}

	result, err := t.inner.Execute(ctx, params)
	// Pipeline tool results are operational workflow data. Preserve their exact
	// contents when feeding the model and writing the execution audit.
	if err == nil && cacheKey != "" && t.exec != nil && shouldCacheWebFetchResult(result) {
		t.exec.setCachedToolResult(cacheKey, result)
	}

	resultAuditLimit := 1000
	if toolName == "web_search" {
		// Direct fetch/navigation URLs in lead research must be traceable to an
		// actual search result. Keep enough of the search receipt so a valid URL
		// later in the result list is not rejected merely because audit text was
		// clipped at the generic 1 KB limit.
		resultAuditLimit = 8 * 1024
	}
	if toolName == "nova_chatroom_search" || toolName == "source_acquire" {
		// The source contract verifies the complete host-issued JSON receipt.
		// Keep it intact in the audit trail instead of clipping a trailing hash.
		resultAuditLimit = 16 * 1024
	}
	record := ToolCallRecord{
		Timestamp:  start,
		ToolName:   toolName,
		Params:     truncate(string(paramsJSON), 500),
		Result:     truncate(result, resultAuditLimit),
		DurationMs: time.Since(start).Milliseconds(),
	}
	if err != nil {
		record.Error = err.Error()
	}
	if err == nil && toolName == "write_file" && !ToolResultLooksFailedForTool(toolName, result) && t.exec != nil {
		if scoped, ok := t.inner.(PathScopedTool); ok {
			if artifact, valid := validatedWriteFilePipelineArtifact(t.exec, scoped.ScopePath(params)); valid {
				record.Artifacts = []ToolCallArtifactRecord{artifact}
			}
		}
	}

	if t.exec != nil {
		t.exec.recordToolCall(t.stepIdx, record)
	}

	if t.fileLogger != nil {
		status := "success"
		detail := record.Result
		if pipelineToolCallRecordFailed(record) {
			status = "error"
			if strings.TrimSpace(record.Error) != "" {
				detail = record.Error
			}
		}
		t.fileLogger.Printf("[Pipeline:audit] Step[%d] %s(%s) -> status=%s detail=%s (%dms)", t.stepIdx, t.inner.Name(), record.Params, status, truncate(detail, 1000), record.DurationMs)
	}

	if err != nil {
		return "", err
	}
	return result, nil
}

func validatedWriteFilePipelineArtifact(exec *PipelineExecution, target string) (ToolCallArtifactRecord, bool) {
	if exec == nil || strings.TrimSpace(target) == "" || strings.TrimSpace(exec.ResolvedWorkspace) == "" {
		return ToolCallArtifactRecord{}, false
	}
	workspace, err := filepath.Abs(strings.TrimSpace(exec.ResolvedWorkspace))
	if err != nil {
		return ToolCallArtifactRecord{}, false
	}
	target, err = filepath.Abs(strings.TrimSpace(target))
	if err != nil || !projectctx.ContainsPath(workspace, target) || projectctx.EnsureResolvedPathInside(workspace, target) != nil {
		return ToolCallArtifactRecord{}, false
	}
	info, err := os.Stat(target)
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 {
		return ToolCallArtifactRecord{}, false
	}
	relative, err := filepath.Rel(workspace, target)
	if err != nil {
		return ToolCallArtifactRecord{}, false
	}
	relative = filepath.ToSlash(filepath.Clean(relative))
	if !strings.HasPrefix(relative, ".openkitty/storage/") {
		return ToolCallArtifactRecord{}, false
	}
	return ToolCallArtifactRecord{Path: relative, Size: info.Size(), Validated: true}, true
}

func (e *PipelineExecutor) getFullToolRegistry() *toolregistry.Registry {
	if e == nil || e.agentFactory == nil {
		return nil
	}
	e.agentFactory.mu.RLock()
	defer e.agentFactory.mu.RUnlock()
	for _, agent := range e.agentFactory.agents {
		if llmAgent, ok := agent.(*LLMAgent); ok && llmAgent.toolRegistry != nil {
			return llmAgent.toolRegistry
		}
	}
	return nil
}

// ====================== Approval / Stop ======================

// ApproveStep resumes a paused pipeline.
func (e *PipelineExecutor) ApproveStep(execID string, feedback string) error {
	e.mu.RLock()
	exec, ok := e.executions[execID]
	e.mu.RUnlock()

	if !ok {
		// Try loading from DB (API mode)
		exec = e.loadFromDB(execID)
		if exec == nil {
			return fmt.Errorf("execution '%s' not found", execID)
		}
		exec.approveCh = make(chan string, 1)
		e.mu.Lock()
		e.executions[execID] = exec
		e.mu.Unlock()
	}

	if exec.Status != "waiting_approval" {
		return fmt.Errorf("execution '%s' is not waiting for approval (status: %s)", execID, exec.Status)
	}

	if exec.running {
		// Interactive mode: goroutine is waiting on channel
		select {
		case exec.approveCh <- feedback:
		default:
		}
	} else {
		// API mode: goroutine exited, need to Resume
		_, err := e.Resume(context.Background(), execID)
		if err != nil {
			return err
		}
		// Now that the goroutine is running and waiting, send approval
		time.Sleep(50 * time.Millisecond) // let goroutine enter waitForApproval
		select {
		case exec.approveCh <- feedback:
		default:
		}
	}

	e.fileLogger.Printf("[Pipeline] Execution %s approved (feedback=%s)", execID, truncate(feedback, 100))
	return nil
}

// PauseExecution requests a durable pause at the next step boundary. It does
// not cancel the current step because doing so could leave a mutating tool with
// an unknown external outcome.
func (e *PipelineExecutor) PauseExecution(execID string) error {
	e.mu.RLock()
	exec, ok := e.executions[execID]
	e.mu.RUnlock()
	if !ok {
		exec = e.loadFromDB(execID)
		if exec == nil {
			return fmt.Errorf("execution '%s' not found", execID)
		}
		e.mu.Lock()
		e.executions[execID] = exec
		e.mu.Unlock()
	}

	pauseAtBoundary := false
	pauseStep := 0
	exec.mu.Lock()
	switch exec.Status {
	case "paused", "pausing":
		exec.mu.Unlock()
		return nil
	case "running", "recovering":
		if exec.running {
			exec.Status = "pausing"
		} else {
			// A durable record can be running while no local worker owns it
			// (for example immediately after recovery). Complete the pause
			// through the same boundary helper so its cursor is normalized.
			exec.Status = "pausing"
			pauseAtBoundary = true
			pauseStep = exec.CurrentStep
		}
	default:
		status := exec.Status
		exec.mu.Unlock()
		return fmt.Errorf("execution '%s' cannot pause from status %s", execID, status)
	}
	status := exec.Status
	exec.mu.Unlock()
	if pauseAtBoundary {
		e.pausePipelineAtBoundary(exec, pauseStep)
		return nil
	}

	e.saveToDB(exec)
	e.broadcastPipelineEvent(execID, status, nil)
	return nil
}

func (e *PipelineExecutor) StopExecution(execID string) error {
	e.mu.RLock()
	exec, ok := e.executions[execID]
	e.mu.RUnlock()
	if !ok {
		exec = e.loadFromDB(execID)
		if exec == nil {
			return fmt.Errorf("execution '%s' not found", execID)
		}
	}

	exec.mu.Lock()
	if exec.Status != "running" && exec.Status != "recovering" && exec.Status != "waiting_approval" &&
		exec.Status != "pausing" && exec.Status != "paused" {
		exec.mu.Unlock()
		return fmt.Errorf("execution '%s' is already %s", execID, exec.Status)
	}
	exec.Status = "stopped"
	exec.mu.Unlock()

	if exec.cancelFunc != nil {
		exec.cancelFunc()
	}
	if exec.running {
		select {
		case exec.approveCh <- "":
		default:
		}
	}

	e.saveToDB(exec)
	e.broadcastPipelineEvent(execID, "stopped", nil)
	return nil
}

func (e *PipelineExecutor) GetExecution(execID string) (*PipelineExecution, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	exec, ok := e.executions[execID]
	return exec, ok
}

// FindRecoverableExecutionByTaskID returns the newest non-terminal pipeline
// owned by one durable Gateway task. AgentOS uses this when an expired lease is
// reclaimed, so the repeated @ dispatch adopts the old execution instead of
// creating a duplicate pipeline.
func (e *PipelineExecutor) FindRecoverableExecutionByTaskID(taskID, pipelineName, employeeID string) (*PipelineExecution, bool) {
	taskID = strings.TrimSpace(taskID)
	if e == nil || taskID == "" {
		return nil, false
	}
	pipelineName = strings.TrimSpace(pipelineName)
	employeeID = strings.TrimSpace(employeeID)
	var candidates []*PipelineExecution
	e.mu.RLock()
	for _, exec := range e.executions {
		if exec != nil {
			candidates = append(candidates, exec)
		}
	}
	e.mu.RUnlock()
	if e.gateway != nil && e.gateway.db != nil {
		var records []PipelineExecutionRecord
		e.gateway.db.Where("status IN ?", []string{"running", "recovering", "waiting_approval"}).Order("updated_at desc").Find(&records)
		for _, record := range records {
			already := false
			for _, candidate := range candidates {
				if candidate.ID == record.ExecID {
					already = true
					break
				}
			}
			if !already {
				if loaded := e.loadFromDB(record.ExecID); loaded != nil {
					candidates = append(candidates, loaded)
				}
			}
		}
	}
	sort.SliceStable(candidates, func(i, j int) bool { return candidates[i].CreatedAt.After(candidates[j].CreatedAt) })
	for _, exec := range candidates {
		exec.mu.Lock()
		matches := strings.TrimSpace(exec.TaskID) == taskID &&
			(pipelineName == "" || strings.TrimSpace(exec.PipelineName) == pipelineName) &&
			(employeeID == "" || strings.TrimSpace(exec.Principal.EmployeeID) == employeeID) &&
			(exec.Status == "running" || exec.Status == "recovering" || exec.Status == "waiting_approval")
		exec.mu.Unlock()
		if matches {
			e.mu.Lock()
			e.executions[exec.ID] = exec
			e.mu.Unlock()
			return exec, true
		}
	}
	return nil, false
}

// ====================== Notification Webhooks ======================

func (e *PipelineExecutor) sendNotifyWebhook(exec *PipelineExecution, stepIndex int, output string) {
	if exec.NotifyURL == "" {
		return
	}
	go func() {
		payload := map[string]interface{}{
			"event":       "approval_required",
			"exec_id":     exec.ID,
			"pipeline":    exec.PipelineName,
			"step_index":  stepIndex,
			"step_name":   exec.Steps[stepIndex].Name,
			"output":      truncate(output, 1000),
			"approve_url": fmt.Sprintf("/api/pipeline/approve?exec_id=%s", exec.ID),
		}
		// fire-and-forget POST
		httpPost(exec.NotifyURL, payload)
	}()
}

func (e *PipelineExecutor) sendCompletionWebhook(exec *PipelineExecution) {
	go func() {
		payload := map[string]interface{}{
			"event":       "completed",
			"exec_id":     exec.ID,
			"pipeline":    exec.PipelineName,
			"status":      exec.Status,
			"total_steps": len(exec.Steps),
		}
		for i, s := range exec.Steps {
			payload[fmt.Sprintf("step_%d_output", i)] = truncate(s.Output, 1000)
		}
		httpPost(exec.NotifyURL, payload)
	}()
}

func httpPost(url string, payload map[string]interface{}) {
	body, _ := json.Marshal(payload)
	client := &http.Client{Timeout: 10 * time.Second}
	req, _ := http.NewRequest("POST", url, strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("[Pipeline] Webhook to %s failed: %v", url, err)
		return
	}
	resp.Body.Close()
}

// ====================== Step Output File Persistence ======================

// saveStepOutputToFile persists a step's output to .openkitty/storage/
func (e *PipelineExecutor) saveStepOutputToFile(exec *PipelineExecution, stepIdx int, output string) {
	if output == "" || e.gateway == nil {
		return
	}
	workspace := e.pipelineExecutionWorkspace(exec)
	dir := filepath.Join(workspace, ".openkitty", "storage")
	os.MkdirAll(dir, 0755)
	relPath := buildStepOutputPath(exec, stepIdx, exec.Steps[stepIdx].Name)
	fp := filepath.Join(workspace, relPath)
	if err := os.WriteFile(fp, []byte(output), 0644); err != nil {
		e.fileLogger.Printf("[Pipeline] Save step output error: %v", err)
	} else {
		e.fileLogger.Printf("[Pipeline] Step output saved: %s (%d bytes)", fp, len(output))
	}
}

func (e *PipelineExecutor) materializeClaimedArtifacts(exec *PipelineExecution, stepIdx int, output string) {
	paths := extractArtifactPaths(output)
	if len(paths) == 0 {
		return
	}
	body := strings.TrimSpace(Clean(middlewarecore.StripToolCalls(output)))
	if outputDisclaimsArtifactDelivery(body) {
		if e.fileLogger != nil {
			e.fileLogger.Printf("[Pipeline] Step %d refused to materialize disclaimed artifact", stepIdx)
		}
		return
	}
	// A path mentioned in a plan, hand-off, routing note, or user requirement is
	// not proof that an artifact was created.  Materialization is only a repair
	// for an explicit completed-delivery claim; otherwise an early pipeline step
	// can accidentally occupy the final report path with its own Markdown output.
	if !outputClaimsArtifactDelivery(body) {
		if e.fileLogger != nil {
			e.fileLogger.Printf("[Pipeline] Step %d ignored uncompleted artifact path mention", stepIdx)
		}
		return
	}
	if !looksLikeMarkdownArtifact(body) {
		return
	}
	for _, rel := range paths {
		ext := strings.ToLower(filepath.Ext(rel))
		if ext != ".md" && ext != ".markdown" && ext != ".txt" {
			continue
		}
		fullPath, ok := e.storageArtifactPathForExecution(exec, rel)
		if !ok {
			if e.fileLogger != nil {
				e.fileLogger.Printf("[Pipeline] Step %d refused claimed artifact path outside storage: %s", stepIdx, rel)
			}
			continue
		}
		if info, err := os.Stat(fullPath); err == nil && !info.IsDir() && info.Size() > 0 {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
			if e.fileLogger != nil {
				e.fileLogger.Printf("[Pipeline] Step %d create artifact dir error: %v", stepIdx, err)
			}
			continue
		}
		if err := os.WriteFile(fullPath, []byte(body), 0644); err != nil {
			if e.fileLogger != nil {
				e.fileLogger.Printf("[Pipeline] Step %d materialize claimed artifact error: %v", stepIdx, err)
			}
			continue
		}
		if e.fileLogger != nil {
			e.fileLogger.Printf("[Pipeline] Step %d materialized claimed artifact: %s (%d bytes)", stepIdx, fullPath, len(body))
		}
	}
}

func outputClaimsArtifactDelivery(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	if lower == "" {
		return false
	}
	for _, marker := range []string{
		"已保存", "已写入", "成功写入", "已经保存", "已经写入", "已生成并保存", "已创建",
		"saved to", "written to", "wrote to", "created at", "has been saved", "has been written",
	} {
		if strings.Contains(lower, strings.ToLower(marker)) {
			return true
		}
	}
	return false
}

func outputDisclaimsArtifactDelivery(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	if lower == "" {
		return false
	}
	for _, marker := range []string{
		"未实际落盘", "本轮未落盘", "未生成即不声称", "尚未生成", "没有生成", "未能生成",
		"计划落盘位置", "真实产物路径（计划", "待验证", "需要 1 次重试", "需要一次重试",
		"not generated", "not created", "was not generated", "was not created", "planned output path",
	} {
		if strings.Contains(lower, strings.ToLower(marker)) {
			return true
		}
	}
	return false
}

func looksLikeMarkdownArtifact(text string) bool {
	text = strings.TrimSpace(text)
	if text == "" {
		return false
	}
	if strings.HasPrefix(text, "#") || strings.Contains(text, "\n# ") || strings.Contains(text, "\n## ") {
		return true
	}
	if strings.Contains(text, "|") && strings.Contains(text, "---") {
		return true
	}
	return strings.Contains(text, "报告") || strings.Contains(strings.ToLower(text), "report")
}

func (e *PipelineExecutor) storageArtifactPath(rel string) (string, bool) {
	return e.storageArtifactPathForExecution(nil, rel)
}

func (e *PipelineExecutor) storageArtifactPathForExecution(exec *PipelineExecution, rel string) (string, bool) {
	rel = strings.TrimSpace(strings.ReplaceAll(rel, "\\", "/"))
	rel = strings.TrimLeft(rel, "/")
	if !strings.HasPrefix(rel, ".openkitty/storage/") {
		return "", false
	}
	base := e.pipelineExecutionWorkspace(exec)
	baseAbs, err := filepath.Abs(base)
	if err != nil {
		return "", false
	}
	storageAbs, err := filepath.Abs(filepath.Join(baseAbs, ".openkitty", "storage"))
	if err != nil {
		return "", false
	}
	fullAbs, err := filepath.Abs(filepath.Join(baseAbs, filepath.FromSlash(rel)))
	if err != nil {
		return "", false
	}
	if fullAbs != storageAbs && !strings.HasPrefix(fullAbs, storageAbs+string(os.PathSeparator)) {
		return "", false
	}
	return fullAbs, true
}

// ====================== DB Persistence ======================

func (e *PipelineExecutor) ensurePipelineCheckpointLocked(exec *PipelineExecution) *PipelineCheckpoint {
	if exec == nil {
		return nil
	}
	if exec.Checkpoint == nil {
		exec.Checkpoint = &PipelineCheckpoint{
			Version:        pipelineCheckpointVersion,
			PipelineExecID: exec.ID,
			ParentTaskID:   exec.TaskID,
			StepIndex:      exec.CurrentStep,
			StepRevision:   1,
			Attempt:        1,
			Phase:          pipelineCheckpointPhaseStepPending,
			RouteEntry:     -1,
			Turns:          make(map[string]*PipelineTurnCheckpoint),
			UpdatedAt:      time.Now(),
		}
	}
	if exec.Checkpoint.Turns == nil {
		exec.Checkpoint.Turns = make(map[string]*PipelineTurnCheckpoint)
	}
	return exec.Checkpoint
}

func (e *PipelineExecutor) persistPipelineCheckpoint(exec *PipelineExecution) {
	if exec == nil {
		return
	}
	exec.mu.Lock()
	current := e.ensurePipelineCheckpointLocked(exec)
	current.Revision++
	current.UpdatedAt = time.Now()
	checkpoint := clonePipelineCheckpoint(current)
	workspace := exec.ResolvedWorkspace
	exec.mu.Unlock()
	if checkpoint == nil {
		return
	}
	if err := writePipelineCheckpoint(pipelineCheckpointPath(workspace, exec.ID), checkpoint); err != nil && e.fileLogger != nil {
		e.fileLogger.Printf("[Pipeline] persist checkpoint %s failed: %v", exec.ID, err)
	}
}

func (e *PipelineExecutor) saveToDB(exec *PipelineExecution) {
	if exec == nil {
		return
	}
	exec.persistMu.Lock()
	defer exec.persistMu.Unlock()
	e.persistPipelineCheckpoint(exec)
	if e.gateway == nil || e.gateway.db == nil {
		return
	}
	exec.mu.Lock()
	stateJSON, marshalErr := json.Marshal(exec)
	status := exec.Status
	exec.mu.Unlock()
	if marshalErr != nil {
		if e.fileLogger != nil {
			e.fileLogger.Printf("[Pipeline] marshal execution %s failed: %v", exec.ID, marshalErr)
		}
		return
	}

	record := PipelineExecutionRecord{
		ExecID:    exec.ID,
		StateJSON: string(stateJSON),
		Status:    status,
	}

	db := e.gateway.db
	var existing PipelineExecutionRecord
	if err := db.Where("exec_id = ?", exec.ID).First(&existing).Error; err == nil {
		db.Model(&existing).Updates(map[string]interface{}{
			"state_json": record.StateJSON,
			"status":     record.Status,
			"updated_at": time.Now(),
		})
	} else {
		db.Create(&record)
	}
}

func (e *PipelineExecutor) loadFromDB(execID string) *PipelineExecution {
	if e.gateway == nil || e.gateway.db == nil {
		return nil
	}
	var record PipelineExecutionRecord
	if err := e.gateway.db.Where("exec_id = ?", execID).First(&record).Error; err != nil {
		return nil
	}
	var exec PipelineExecution
	if err := json.Unmarshal([]byte(record.StateJSON), &exec); err != nil {
		return nil
	}
	exec.approveCh = make(chan string, 1)
	exec.running = false
	return &exec
}

func (e *PipelineExecutor) parentTaskUsesAgentOS(exec *PipelineExecution) bool {
	if e == nil || e.gateway == nil || e.gateway.db == nil || exec == nil || strings.TrimSpace(exec.TaskID) == "" {
		return false
	}
	var task GatewayTaskRecord
	if err := e.gateway.db.Select("task_id", "queue_name").Where("task_id = ?", exec.TaskID).First(&task).Error; err != nil {
		return false
	}
	return task.QueueName == agentOSQueueName
}

// RecoverOrphanExecutions reloads all non-terminal executions. Ordinary
// pipelines resume immediately. AgentOS-owned pipelines wait for the durable
// parent queue lease to reclaim the @ task, then Dispatch adopts the same exec.
func (e *PipelineExecutor) RecoverOrphanExecutions() {
	if e.gateway == nil || e.gateway.db == nil {
		return
	}
	var records []PipelineExecutionRecord
	e.gateway.db.Where("status IN ?", []string{"running", "recovering", "pausing", "paused", "waiting_approval"}).Find(&records)
	if len(records) == 0 {
		return
	}

	recovered := 0
	autoResumed := 0
	deferred := 0
	for _, r := range records {
		exec := e.loadFromDB(r.ExecID)
		if exec == nil {
			continue
		}
		if sidecar, err := loadPipelineCheckpoint(pipelineCheckpointPath(exec.ResolvedWorkspace, exec.ID)); err == nil &&
			(exec.Checkpoint == nil || sidecar.Revision > exec.Checkpoint.Revision ||
				sidecar.Revision == exec.Checkpoint.Revision && sidecar.UpdatedAt.After(exec.Checkpoint.UpdatedAt)) {
			exec.Checkpoint = sidecar
			exec.CurrentStep = sidecar.StepIndex
		}
		exec.running = false
		e.mu.Lock()
		e.executions[exec.ID] = exec
		e.mu.Unlock()
		recovered++

		if r.Status == "waiting_approval" {
			e.fileLogger.Printf("[Pipeline] Recovered HITL execution: %s", r.ExecID)
			continue
		}
		if r.Status == "paused" || r.Status == "pausing" {
			exec.mu.Lock()
			exec.Status = "paused"
			exec.running = false
			exec.mu.Unlock()
			e.saveToDB(exec)
			e.fileLogger.Printf("[Pipeline] Recovered paused execution: %s", r.ExecID)
			continue
		}
		if e.parentTaskUsesAgentOS(exec) {
			exec.mu.Lock()
			// Keep the public/durable status vocabulary unchanged. The run is
			// recoverable because running=false; the parent queue owns the lease.
			exec.Status = "running"
			exec.running = false
			exec.mu.Unlock()
			e.saveToDB(exec)
			deferred++
			e.fileLogger.Printf("[Pipeline] Recovered execution %s; waiting for AgentOS task %s", r.ExecID, exec.TaskID)
			continue
		}
		if _, err := e.ResumeInterrupted(context.Background(), exec.ID); err != nil {
			e.fileLogger.Printf("[Pipeline] Recovered execution %s could not auto-resume: %v", r.ExecID, err)
		} else {
			autoResumed++
			e.fileLogger.Printf("[Pipeline] Auto-resumed execution %s from step %d", r.ExecID, exec.CurrentStep)
		}
	}
	if recovered > 0 {
		e.fileLogger.Printf("[Pipeline] Recovery complete: %d reloaded, %d auto-resumed, %d awaiting AgentOS",
			recovered, autoResumed, deferred)
	}
}

// ====================== Trigger Matching ======================

func (e *PipelineExecutor) MatchTrigger(message string, channel string) (string, bool) {
	if e.gateway == nil || len(e.gateway.config.Pipeline.Triggers) == 0 {
		return "", false
	}
	msgLower := strings.ToLower(message)
	for _, t := range e.gateway.config.Pipeline.Triggers {
		if strings.Contains(msgLower, strings.ToLower(t.When)) {
			if t.Channel != "" && channel != "" && channel != t.Channel {
				continue
			}
			return t.Pipeline, true
		}
	}
	return "", false
}

// Global reference for tools to access the pipeline executor.
var globalPipelineExec *PipelineExecutor

// SetGlobalPipelineExec is called by the gateway during initialization.
func SetGlobalPipelineExec(pe *PipelineExecutor) { globalPipelineExec = pe }

// GetGlobalPipelineExec returns the global pipeline executor.
func GetGlobalPipelineExec() *PipelineExecutor { return globalPipelineExec }

// ListPipelinesTool lets the agent list available pipelines (AI工作流).
type ListPipelinesTool struct{}

func NewListPipelinesTool() *ListPipelinesTool { return &ListPipelinesTool{} }
func (t *ListPipelinesTool) Name() string      { return "list_pipelines" }
func (t *ListPipelinesTool) Description() string {
	return "List all pipeline workflows (AI工作流) available in the system. Each pipeline defines a sequence of steps that AI employees execute."
}
func (t *ListPipelinesTool) Parameters() map[string]interface{} {
	return map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}
}
func (t *ListPipelinesTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	if globalPipelineExec == nil {
		return "系统未配置工作流引擎", nil
	}
	pipelines := globalPipelineExec.ListPipelines()
	if len(pipelines) == 0 {
		return "当前没有注册的工作流", nil
	}
	var sb strings.Builder
	sb.WriteString("## AI 工作流列表\n\n")
	sb.WriteString("| 名称 | 步骤数 | 描述 |\n")
	sb.WriteString("|------|--------|------|\n")
	for _, p := range pipelines {
		desc := p.Desc
		if desc == "" {
			desc = "-"
		} else if len(desc) > 60 {
			desc = desc[:60] + "..."
		}
		sb.WriteString(fmt.Sprintf("| %s | %d | %s |\n", p.Name, len(p.Steps), desc))
	}
	sb.WriteString(fmt.Sprintf("\n共 %d 个工作流\n", len(pipelines)))
	return sb.String(), nil
}

func (e *PipelineExecutor) ListPipelines() []Pipeline {
	if e.gateway == nil {
		return nil
	}
	pipelines := e.gateway.config.Pipeline.Pipelines
	out := make([]Pipeline, 0, len(pipelines))
	for _, p := range pipelines {
		if isRetiredBuiltinPipeline(p.Name) {
			continue
		}
		out = append(out, p)
	}
	return out
}

// ListExecutions returns all in-memory executions + recent ones from DB.
func (e *PipelineExecutor) ListExecutions(limit int) []*PipelineExecution {
	if limit <= 0 {
		limit = 50
	}

	out := make([]*PipelineExecution, 0, len(e.executions))
	e.mu.RLock()
	for _, exec := range e.executions {
		out = append(out, exec)
	}
	e.mu.RUnlock()

	// Also load from DB if gateway is available
	if e.gateway != nil && e.gateway.db != nil {
		var records []PipelineExecutionRecord
		e.gateway.db.Order("created_at desc").Limit(limit).Find(&records)
		for _, rec := range records {
			// Skip if already in memory
			found := false
			for _, mem := range out {
				if mem.ID == rec.ExecID {
					found = true
					break
				}
			}
			if !found {
				var exec PipelineExecution
				if err := json.Unmarshal([]byte(rec.StateJSON), &exec); err == nil {
					exec.approveCh = make(chan string, 1)
					out = append(out, &exec)
				}
			}
		}
	}

	sort.SliceStable(out, func(i, j int) bool {
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

// GetExecutionByID returns a specific execution by ID (memory first, then DB).
func (e *PipelineExecutor) GetExecutionByID(execID string) (*PipelineExecution, bool) {
	if exec, ok := e.GetExecution(execID); ok {
		return exec, true
	}
	// Try DB
	if e.gateway != nil && e.gateway.db != nil {
		exec := e.loadFromDB(execID)
		return exec, exec != nil
	}
	return nil, false
}

// ====================== SSE Broadcasting ======================

func (e *PipelineExecutor) broadcastPipelineEvent(execID string, eventType string, data interface{}) {
	if e.gateway == nil {
		return
	}
	event := ProgressEvent{
		TaskID:      execID,
		ExecutionID: execID,
		Type:        "pipeline_" + eventType,
		Message:     eventType,
		Status:      "running",
	}
	payload, _ := data.(map[string]interface{})
	exec, _ := e.GetExecution(execID)
	if payload != nil {
		if stepName, ok := payload["step_name"].(string); ok {
			event.StepName = strings.TrimSpace(stepName)
		}
		if stepIndex, ok := payload["step_index"].(int); ok && stepIndex >= 0 {
			event.StepIndex = stepIndex + 1
		}
		if attempt, ok := payload["attempt"].(int); ok && attempt > 0 {
			event.Attempt = attempt
		}
		if callID, ok := payload["tool_call_id"].(string); ok {
			event.ToolCallID = strings.TrimSpace(callID)
		}
	}
	if exec != nil {
		event.StepTotal = len(exec.Steps)
	}

	switch eventType {
	case "step_failed", "failed":
		event.Status = "error"
	case "step_done", "completed":
		event.Status = "success"
	case "pausing":
		event.Status = "pausing"
	case "paused":
		event.Status = "paused"
	case "stopped":
		event.Status = "stopped"
	}
	if payload != nil {
		if degraded, _ := payload["degraded"].(bool); degraded {
			event.Status = "warning"
		}
		messageParts := []string{eventType}
		if errorText, ok := payload["error"].(string); ok && strings.TrimSpace(errorText) != "" {
			messageParts = append(messageParts, strings.TrimSpace(errorText))
		}
		if detail, ok := payload["detail"].(string); ok && strings.TrimSpace(detail) != "" {
			messageParts = append(messageParts, "details: "+strings.TrimSpace(detail))
		}
		event.Message = strings.Join(messageParts, " | ")
	}
	event.Message = strings.ReplaceAll(event.Message, "\r", " ")
	event.Message = strings.ReplaceAll(event.Message, "\n", " ")
	// Leave a small transport reserve before publishing the progress event.
	progressMessageBudget := professionalAgentProgressMaxBytes - 256
	event.Message = textutil.TruncateUTF8Bytes(
		strings.TrimSpace(event.Message),
		progressMessageBudget,
	)
	e.gateway.BroadcastProgress(event)
	if publicEvent, ok := publicAgentOSPipelineProgressEvent(exec, eventType, event); ok &&
		e.gateway.isAgentOSTaskID(publicEvent.TaskID) {
		if publicEvent.AgentName == "" {
			publicEvent.AgentName = e.publicPipelineEmployeeName(exec)
		}
		if publicEvent.Type == "stage_start" || publicEvent.Type == "stage_retry" || publicEvent.Type == "stage_waiting" {
			if phase := agentOSBusinessStepPhase(publicEvent.StepName); phase != "" {
				e.gateway.ObserveTaskRuntime(publicEvent.TaskID, phase, "")
			}
		}
		e.gateway.BroadcastProgress(publicEvent)
	}
}

func (e *PipelineExecutor) publicPipelineEmployeeName(exec *PipelineExecution) string {
	if exec == nil || exec.RuntimePipeline == nil {
		return ""
	}
	for _, candidate := range []string{exec.RuntimePipeline.AIEmployeeName, exec.RuntimePipeline.AIEmployeeAtName} {
		if name := publicProgressStepName(candidate); name != "" {
			return name
		}
	}
	employeeID := strings.TrimSpace(exec.RuntimePipeline.AIEmployeeID)
	if employeeID == "" || e == nil || e.gateway == nil || e.gateway.aeManager == nil {
		return ""
	}
	manager := e.gateway.aeManager
	manager.mu.RLock()
	cfg := manager.employees[employeeID]
	manager.mu.RUnlock()
	if cfg == nil {
		return ""
	}
	for _, candidate := range []string{cfg.Name, cfg.AtName} {
		if name := publicProgressStepName(candidate); name != "" {
			return name
		}
	}
	return ""
}

func publicAgentOSPipelineProgressEvent(exec *PipelineExecution, eventType string, source ProgressEvent) (ProgressEvent, bool) {
	if exec == nil {
		return ProgressEvent{}, false
	}
	parentTaskID := strings.TrimSpace(exec.TaskID)
	if parentTaskID == "" || parentTaskID == strings.TrimSpace(exec.ID) {
		return ProgressEvent{}, false
	}
	stepName := publicProgressStepName(source.StepName)
	if stepName == "" && eventType != "completed" && eventType != "stopped" {
		return ProgressEvent{}, false
	}
	public := ProgressEvent{
		TaskID:      parentTaskID,
		ExecutionID: exec.ID,
		StepName:    stepName,
		StepIndex:   source.StepIndex,
		StepTotal:   source.StepTotal,
		Attempt:     source.Attempt,
		Status:      source.Status,
		Timestamp:   time.Now().UnixMilli(),
	}
	if exec.RuntimePipeline != nil {
		public.AgentName = publicProgressStepName(exec.RuntimePipeline.AIEmployeeName)
	}
	position := ""
	if public.StepIndex > 0 && public.StepTotal >= public.StepIndex {
		position = fmt.Sprintf("第 %d/%d 步 · ", public.StepIndex, public.StepTotal)
	}
	switch eventType {
	case "step_start":
		public.Type = "stage_start"
		public.Status = "running"
		public.Message = position + "正在执行：" + stepName
	case "step_done":
		public.Type = "stage_done"
		public.Message = position + stepName + "已完成"
	case "step_failed":
		public.Type = "stage_error"
		public.Status = "error"
		public.Message = position + stepName + "未完成，正在调整"
	case "step_retry", "step_rework_requested":
		public.Type = "stage_retry"
		public.Status = "running"
		public.Message = position + "正在重新处理：" + stepName
	case "waiting_approval":
		public.Type = "stage_waiting"
		public.Status = "waiting_approval"
		public.Message = position + stepName + "等待确认"
	default:
		return ProgressEvent{}, false
	}
	return public, true
}

// ====================== GORM Migration ======================

func migratePipeline(db *gorm.DB) error {
	return db.AutoMigrate(&PipelineExecutionRecord{})
}
