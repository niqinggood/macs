package aiemployee

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	pipelinecore "mac/internal/pipeline"
)

var (
	aiEmployeeExplicitUserSelectionRe = regexp.MustCompile(`(?i)(?:^|[\s,，。；;:：])(?:我|本人)(?:选择|选定|选了|决定选|确认选)|(?:^|\s)i\s+(?:choose|chose|select|selected)\b`)
	aiEmployeeAssumedUserSelectionRe  = regexp.MustCompile(`(?i)(?:你|您)(?:已选择|已经选择|已选|当前选择|当前选项|选定的是)|按(?:你|您)(?:已选|选择的)|\byou(?:'ve| have)?\s+(?:chosen|selected)\b`)
	aiEmployeeChineseUpstreamStepRe   = regexp.MustCompile(`(?i)上游(?:步骤)?\s*step\s*\d+(?:\s*(?:的)?\s*(?:推断|判断|产出|输出|结论))?`)
	aiEmployeeEnglishUpstreamStepRe   = regexp.MustCompile(`(?i)upstream\s+step\s*\d+(?:\s+(?:inference|deduction|output|result|conclusion))?`)
	aiEmployeeInternalStepNumberRe    = regexp.MustCompile(`(?i)\bstep\s*\d+\b`)
	aiEmployeeRejectsInteractionZHRe  = regexp.MustCompile(`(?is)(?:不要|别|无需|无须|不需要|不用|不必)[^，。；;\n]{0,20}(?:问(?:我)?|询问|提问|追问|确认|选择)`)
	aiEmployeeRejectsInteractionENRe  = regexp.MustCompile(`(?is)\b(?:do\s+not|don't|without|no\s+need\s+to)\b[^.!?;\n]{0,48}\b(?:ask|question|confirm|choose|select)\w*`)
)

var aiEmployeeInteractiveIntakeSignal = regexp.MustCompile(`(?is)(?:让我|供我|由我|请我).{0,24}(?:选择|确认|决定)|(?:请)?(?:先|首先).{0,20}(?:确认|询问|问我).{0,48}(?:再|然后|之后|并让我|并请我)|(?:let|ask)\s+me.{0,32}(?:choose|confirm|decide)|(?:present|show|give|list).{0,32}(?:options?|choices?).{0,48}(?:first|before|then)`)
var markdownOrderedListItemRe = regexp.MustCompile(`^(\s*)\d+([.)])(\s+)(.*)$`)
var markdownHeadingRe = regexp.MustCompile(`^(#{1,6})\s+\S`)

func containsAnyText(text string, needles []string) bool {
	lower := strings.ToLower(text)
	for _, needle := range needles {
		if strings.Contains(lower, strings.ToLower(needle)) {
			return true
		}
	}
	return false
}

func uniqueSortedStrings(values []string) []string {
	seen := make(map[string]bool, len(values))
	out := make([]string, 0, len(values))
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

func pipelineOutputWaitsForInput(output string) bool {
	if !strings.Contains(output, "INPUT_STATUS: MISSING") {
		return false
	}
	lines := strings.Split(strings.ReplaceAll(output, "\r\n", "\n"), "\n")
	for index := len(lines) - 1; index >= 0; index-- {
		line := strings.TrimSpace(lines[index])
		if line != "" {
			return line == "<!--PIPELINE_STOP-->"
		}
	}
	return false
}

func fundraisingTaskRequestsClarification(task string) bool {
	normalized := strings.ToLower(strings.TrimSpace(task))
	if aiEmployeeTaskRejectsInteractiveHandoff(task) {
		return false
	}
	if aiEmployeeInteractiveIntakeSignal.MatchString(strings.TrimSpace(task)) {
		return true
	}
	return containsAnyText(normalized, []string{
		"请先确认", "先确认", "先问我", "先询问", "问我几个", "询问我",
		"让我选择", "让我确认", "需要我确认", "结尾问", "结尾询问", "最后问",
		"只问", "必要问题", "待确认问题", "决定性问题", "关键追问", "列出问题",
		"ask me", "confirm with me", "questions at the end",
	})
}

func extractAIEmployeeExplicitCircledOptions(task string) []string {
	markers := []rune("①②③④⑤⑥⑦⑧⑨⑩")
	type occurrence struct {
		marker rune
		start  int
	}
	var found []occurrence
	for _, marker := range markers {
		if index := strings.IndexRune(task, marker); index >= 0 {
			found = append(found, occurrence{marker: marker, start: index})
		}
	}
	if len(found) < 2 {
		return nil
	}
	for index := 1; index < len(found); index++ {
		if found[index].start <= found[index-1].start {
			return nil
		}
	}
	options := make([]string, 0, len(found))
	for index, item := range found {
		end := len(task)
		if index+1 < len(found) {
			end = found[index+1].start
		}
		raw := strings.TrimSpace(task[item.start:end])
		if index == len(found)-1 {
			if boundary := strings.IndexAny(raw, "。\n\r"); boundary >= 0 {
				raw = strings.TrimSpace(raw[:boundary])
			}
		}
		raw = strings.Trim(strings.TrimSpace(raw), "，,；;。 ")
		if length := len([]rune(raw)); length < 2 || length > 240 {
			return nil
		}
		options = append(options, raw)
	}
	return options
}

func aiEmployeeOutputCoversExplicitOption(output, option string) bool {
	option = strings.TrimSpace(option)
	if option == "" || strings.Contains(output, option) {
		return true
	}
	marker, _ := utf8.DecodeRuneInString(option)
	for _, raw := range strings.Split(strings.ReplaceAll(output, "\r\n", "\n"), "\n") {
		line := strings.TrimLeft(strings.TrimSpace(raw), "-*+#> \t")
		if strings.HasPrefix(line, string(marker)) && len([]rune(strings.TrimSpace(strings.TrimPrefix(line, string(marker))))) >= 2 {
			return true
		}
	}
	return false
}

func detectPipelineTargetScope(input string) string {
	text := strings.TrimSpace(input)
	if text == "" {
		return ""
	}
	if match := regexp.MustCompile(`(?i)\b([a-z]):[\\/][^\s，。；;、]+`).FindString(text); match != "" {
		return normalizePipelineTargetPath(trimPipelineTargetSentenceSuffix(match))
	}
	match := regexp.MustCompile(`(?i)([a-z])盘\s*(?:下|里|的)?\s*([a-z0-9_.-]+(?:[\\/][a-z0-9_.-]+)*)?`).FindStringSubmatch(text)
	if len(match) == 0 {
		return ""
	}
	drive := strings.ToUpper(match[1]) + ":"
	relative := strings.TrimSpace(match[2])
	if relative == "" {
		return drive + `\`
	}
	return normalizePipelineTargetPath(drive + `\` + relative)
}

func trimPipelineTargetSentenceSuffix(path string) string {
	for _, marker := range []string{"是我的", "是我", "是一个", "是个", "你帮我", "请帮我", "帮我", "请帮", "用来", "用于"} {
		if index := strings.Index(path, marker); index > 2 {
			path = path[:index]
		}
	}
	return path
}

func normalizePipelineTargetPath(path string) string {
	path = strings.Trim(path, " \t\r\n，。；;、")
	return strings.ReplaceAll(path, "/", `\`)
}

func removeEmptyMarkdownSections(markdown string) string {
	lines := strings.Split(strings.ReplaceAll(markdown, "\r\n", "\n"), "\n")
	remove := make(map[int]bool)
	for index, line := range lines {
		match := markdownHeadingRe.FindStringSubmatch(strings.TrimSpace(line))
		if len(match) != 2 {
			continue
		}
		level := len(match[1])
		hasContent := false
		for next := index + 1; next < len(lines); next++ {
			trimmed := strings.TrimSpace(lines[next])
			if trimmed == "" || strings.Trim(trimmed, "-*_ ") == "" {
				continue
			}
			nextHeading := markdownHeadingRe.FindStringSubmatch(trimmed)
			if len(nextHeading) == 2 {
				if len(nextHeading[1]) <= level {
					break
				}
				hasContent = true
				break
			}
			hasContent = true
			break
		}
		if !hasContent {
			remove[index] = true
		}
	}
	kept := make([]string, 0, len(lines))
	for index, line := range lines {
		if !remove[index] {
			kept = append(kept, line)
		}
	}
	return strings.TrimSpace(strings.Join(kept, "\n"))
}

func collapseDuplicateMarkdownRules(markdown string) string {
	lines := strings.Split(strings.ReplaceAll(markdown, "\r\n", "\n"), "\n")
	kept := make([]string, 0, len(lines))
	lastContentWasRule := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		isRule := trimmed != "" && strings.Trim(trimmed, "-*_ ") == ""
		if isRule && lastContentWasRule {
			continue
		}
		kept = append(kept, line)
		if trimmed != "" {
			lastContentWasRule = isRule
		}
	}
	return strings.TrimSpace(strings.Join(kept, "\n"))
}

func aiEmployeeTaskRejectsInteractiveHandoff(task string) bool {
	task = strings.TrimSpace(task)
	return task != "" && (aiEmployeeRejectsInteractionZHRe.MatchString(task) ||
		aiEmployeeRejectsInteractionENRe.MatchString(task))
}

func aiEmployeeTaskRequestsInteractiveHandoff(employeeID, task string) bool {
	if aiEmployeeTaskRejectsInteractiveHandoff(task) {
		return false
	}
	if strings.EqualFold(strings.TrimSpace(employeeID), "fundraising-advisor") {
		return fundraisingTaskRequestsClarification(task)
	}
	return aiEmployeeInteractiveIntakeSignal.MatchString(strings.TrimSpace(task))
}

// aiEmployeeMinimumProgressIssues rejects a common cross-domain failure mode:
// an otherwise actionable task is replaced by a questionnaire and a promise to
// deliver after the next reply. This is intentionally employee-agnostic. It
// does not decide business facts or required fields; explicit pipeline
// missing-input outcomes remain authoritative.
func aiEmployeeMinimumProgressIssues(pipeline *pipelinecore.Pipeline, step *pipelinecore.PipelineStep, output string) []string {
	if pipeline == nil || step == nil || strings.TrimSpace(pipeline.AIEmployeeID) == "" {
		return nil
	}
	if step.AllowMissingInput || pipelineOutputWaitsForInput(output) {
		return nil
	}
	task := strings.TrimSpace(pipeline.AIEmployeeOriginalTask)
	normalized := strings.ToLower(strings.TrimSpace(output))
	if normalized == "" {
		return nil
	}
	issues := aiEmployeeUserFacingOutputIssues(task, output)
	if aiEmployeeTaskRequestsInteractiveHandoff(pipeline.AIEmployeeID, task) {
		issues = append(issues, aiEmployeeInteractiveHandoffOutputIssues(task, output)...)
	}
	if !hasInspectableAIEmployeeInput(task) {
		return uniqueSortedStrings(issues)
	}

	questionCount := countAIEmployeeClarificationQuestions(output)
	conditionalDeferral := containsConditionalDeliveryDeferral(normalized)
	requestedInteractionDelivered := aiEmployeeTaskRequestsInteractiveHandoff(pipeline.AIEmployeeID, task) &&
		aiEmployeeInteractiveResponseProvidesChoice(output)
	deliveryRefusal := containsAnyText(normalized, []string{
		"暂不写", "暂不生成", "暂不制作", "暂不交付",
		"现在不写", "现在不生成", "现在不制作", "现在不交付",
		"先不写", "先不生成", "先不制作", "先不交付",
		"不会提前写", "不会提前生成", "不会提前制作",
	})

	if conditionalDeferral && !requestedInteractionDelivered {
		issues = append(issues, "the answer postponed the requested deliverable until a future user reply")
	}
	if deliveryRefusal && !requestedInteractionDelivered {
		issues = append(issues, "the answer explicitly refused to produce a useful current-pass deliverable")
	}
	if questionCount > 3 {
		issues = append(issues, fmt.Sprintf("the answer asked %d clarification questions instead of limiting them to decision-changing gaps", questionCount))
	}
	return uniqueSortedStrings(issues)
}

func aiEmployeeInteractiveHandoffOutputIssues(task, output string) []string {
	if !aiEmployeeInteractiveIntakeSignal.MatchString(strings.TrimSpace(task)) {
		return nil
	}
	var issues []string
	for _, option := range extractAIEmployeeExplicitCircledOptions(task) {
		if !aiEmployeeOutputCoversExplicitOption(output, option) {
			marker, _ := utf8.DecodeRuneInString(option)
			issues = append(issues, fmt.Sprintf("the answer omitted the independently selectable %c option specified by the user", marker))
		}
	}
	if aiEmployeeAssumedUserSelectionRe.MatchString(output) &&
		!aiEmployeeExplicitUserSelectionRe.MatchString(task) {
		issues = append(issues, "the answer claimed the user had already made a choice that the current request only asked to present")
	}
	if utf8.RuneCountInString(strings.TrimSpace(output)) > 1500 &&
		countAIEmployeeMarkdownHeadings(output) >= 3 {
		issues = append(issues, "the interaction handoff expanded into a multi-section later-stage deliverable instead of staying at the earliest unresolved gate")
	}
	return issues
}

func countAIEmployeeMarkdownHeadings(output string) int {
	count := 0
	for _, raw := range strings.Split(strings.ReplaceAll(output, "\r\n", "\n"), "\n") {
		if _, _, ok := markdownHeadingLevelAndTitle(strings.TrimSpace(raw)); ok {
			count++
		}
	}
	return count
}

func aiEmployeeInteractiveResponseProvidesChoice(output string) bool {
	normalized := strings.ToLower(strings.TrimSpace(output))
	if normalized == "" {
		return false
	}
	if countAIEmployeeClarificationQuestions(output) > 0 {
		return true
	}
	if strings.Contains(normalized, "①") && strings.Contains(normalized, "②") {
		return true
	}
	return containsAnyText(normalized, []string{
		"请选择", "请确认", "选择一项", "选择其中", "回复选项", "回复序号",
		"可选路径", "信息起点", "options:", "choose one", "select one",
		"please choose", "please select", "please confirm",
	})
}

func aiEmployeeUserFacingOutputIssues(task, output string) []string {
	normalizedTask := strings.ToLower(strings.TrimSpace(task))
	normalizedOutput := strings.TrimSpace(output)
	var issues []string
	if detected, _ := aiEmployeeLeadingMetaPreamble(normalizedOutput); detected {
		issues = append(issues, "the final answer exposed a model-facing task-analysis preamble before the user-facing response")
	}
	if containsAnyText(normalizedOutput, []string{
		"## 意图理解",
		"## 约束账本",
		"## 执行边界",
	}) &&
		strings.Contains(normalizedOutput, "## 意图理解") &&
		strings.Contains(normalizedOutput, "## 约束账本") &&
		strings.Contains(normalizedOutput, "## 执行边界") {
		issues = append(issues, "the final answer exposed the internal intake ledger instead of a user-facing result")
	}

	taskDiscussesInternalMechanism := aiEmployeeTaskDiscussesInternalMechanism(normalizedTask)
	if !taskDiscussesInternalMechanism && containsAnyText(normalizedOutput, []string{
		"约束账本",
		"本步骤契约",
		"当前步骤契约",
		"上游步骤产出",
		"下游步骤继承",
		"下游约束",
		"任务受理槽位",
		"step 0",
		"step 1",
	}) {
		issues = append(issues, "the final answer exposed an internal ledger, contract, slot, or upstream/downstream handoff")
	}
	if !taskDiscussesInternalMechanism && containsAnyText(normalizedOutput, []string{
		"脚本只能在我手上",
		"按规范我必须",
		"根据内部规范",
		"技能要求我",
		"系统要求我",
		"模型要求我",
	}) {
		issues = append(issues, "the final answer exposed internal script, skill, model, or policy mechanics")
	}
	return issues
}

func stripAIEmployeeLeadingMetaPreamble(output string) string {
	normalized := strings.TrimSpace(strings.ReplaceAll(output, "\r\n", "\n"))
	detected, boundary := aiEmployeeLeadingMetaPreamble(normalized)
	if !detected || boundary < 0 {
		return normalized
	}
	lines := strings.Split(normalized, "\n")
	if boundary >= len(lines) {
		return normalized
	}
	remainder := strings.TrimSpace(strings.Join(lines[boundary:], "\n"))
	if utf8.RuneCountInString(remainder) < 20 {
		return normalized
	}
	return remainder
}

// aiEmployeeLeadingMetaPreamble identifies a cross-model failure mode where
// the model writes its private response plan before the actual answer. The
// detector is deliberately based on combined discourse signals and document
// structure, not employee names, domains, URLs, or business keywords.
func aiEmployeeLeadingMetaPreamble(output string) (bool, int) {
	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) < 2 {
		return false, -1
	}

	first := -1
	for i, raw := range lines {
		if strings.TrimSpace(raw) != "" {
			first = i
			break
		}
	}
	if first < 0 {
		return false, -1
	}

	limit := len(lines)
	if limit > first+40 {
		limit = first + 40
	}
	leading := strings.ToLower(strings.Join(lines[first:limit], "\n"))
	selfPlanningMarkers := []string{
		"i now have enough", "now i have enough", "now i have sufficient", "now i have verifiable",
		"i have enough", "i have sufficient", "i have verifiable",
		"i need to respond", "i should respond",
		"i must respond", "my response should", "before i respond",
		"我现在已经有足够", "我已有足够", "我需要回答", "我需要回应", "我应该回答", "我的回答应该",
	}
	thirdPersonRequestMarkers := []string{
		"the user's request", "the user wants", "the user asked", "the user provided",
		"based on the user's", "用户的请求", "用户希望", "用户要求", "用户提供了",
	}
	internalConstraintMarkers := []string{
		"per rules", "according to the rules", "must not invent", "cannot be inferred",
		"i must not", "critical decisions cannot", "按照规则", "根据规则", "不能编造", "不得编造", "无法推断",
	}
	evidenceInventoryMarkers := []string{
		"the provided url", "provided urls", "provided links", "the two provided",
		"both urls", "both links", "both pages",
		"the supplied material", "the supplied materials", "已提供的链接", "提供的两个链接", "现有材料表明",
	}

	hasSelfPlanning := containsAnyText(leading, selfPlanningMarkers)
	supportingSignals := 0
	for _, markers := range [][]string{
		thirdPersonRequestMarkers,
		internalConstraintMarkers,
		evidenceInventoryMarkers,
	} {
		if containsAnyText(leading, markers) {
			supportingSignals++
		}
	}
	if !hasSelfPlanning || supportingSignals == 0 {
		return false, -1
	}

	metaContinuationMarkers := make([]string, 0, len(selfPlanningMarkers)+len(thirdPersonRequestMarkers)+12)
	metaContinuationMarkers = append(metaContinuationMarkers, selfPlanningMarkers...)
	metaContinuationMarkers = append(metaContinuationMarkers, thirdPersonRequestMarkers...)
	metaContinuationMarkers = append(metaContinuationMarkers,
		"per rules", "according to the rules", "i must not", "critical decisions cannot",
		"the provided url", "provided urls", "provided links", "the two provided",
		"both urls", "both links", "both pages",
		"按照规则，我", "根据规则，我", "提供的两个链接",
	)
	lastMetaLine := first
	for i := first; i < limit; i++ {
		if containsAnyText(strings.ToLower(strings.TrimSpace(lines[i])), metaContinuationMarkers) {
			lastMetaLine = i
		}
	}

	if boundary := aiEmployeeUserFacingBoundary(lines, lastMetaLine); boundary >= 0 {
		return true, boundary
	}
	return true, -1
}

func aiEmployeeUserFacingBoundary(lines []string, after int) int {
	for i := after + 1; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		if _, title, ok := markdownHeadingLevelAndTitle(line); ok && !aiEmployeeInternalPlanningHeading(title) {
			return i
		}
		next := nextNonEmptyAIEmployeeLine(lines, i+1)
		if i > 0 &&
			strings.TrimSpace(lines[i-1]) == "" &&
			utf8.RuneCountInString(line) <= 160 &&
			!strings.HasSuffix(line, ":") &&
			!strings.HasSuffix(line, "：") &&
			aiEmployeeNumberedSectionLine(next) {
			return i
		}
	}

	sawBlank := false
	for i := after + 1; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			sawBlank = true
			continue
		}
		if sawBlank {
			return i
		}
	}
	return -1
}

func nextNonEmptyAIEmployeeLine(lines []string, start int) string {
	for i := start; i < len(lines); i++ {
		if line := strings.TrimSpace(lines[i]); line != "" {
			return line
		}
	}
	return ""
}

func aiEmployeeInternalPlanningHeading(title string) bool {
	switch strings.ToLower(strings.TrimSpace(title)) {
	case "analysis", "reasoning", "response plan", "task analysis", "internal reasoning",
		"分析", "推理", "回答计划", "任务分析", "内部推理":
		return true
	default:
		return false
	}
}

func aiEmployeeNumberedSectionLine(line string) bool {
	line = strings.TrimSpace(line)
	if line == "" {
		return false
	}
	for _, prefix := range []string{
		"一、", "二、", "三、", "四、", "五、", "六、", "七、", "八、", "九、", "十、",
	} {
		if strings.HasPrefix(line, prefix) {
			return true
		}
	}
	for i, r := range line {
		if r < '0' || r > '9' {
			return i > 0 && (r == '.' || r == ')' || r == '、')
		}
	}
	return false
}

func stripAIEmployeeInternalMechanicLines(output string, configuredSkillNames ...string) string {
	internalSkillNames := make([]string, 0, len(configuredSkillNames))
	for _, skillName := range configuredSkillNames {
		if normalized := strings.ToLower(strings.TrimSpace(skillName)); normalized != "" {
			internalSkillNames = append(internalSkillNames, normalized)
		}
	}
	lines := strings.Split(strings.ReplaceAll(output, "\r\n", "\n"), "\n")
	kept := make([]string, 0, len(lines))
	for _, line := range lines {
		lower := strings.ToLower(strings.TrimSpace(line))
		if isAIEmployeeInternalControlLine(lower) {
			continue
		}
		if isAIEmployeeInternalPipelineMetadataLine(lower) {
			continue
		}
		if containsAnyText(lower, []string{
			"use_skill", "skill_name", "pipe_direct_",
			"tool schema repair", "工具调用日志",
		}) {
			continue
		}
		if containsAnyText(lower, internalSkillNames) {
			continue
		}
		kept = append(kept, line)
	}
	return strings.TrimSpace(strings.Join(kept, "\n"))
}

func isAIEmployeeInternalPipelineMetadataLine(line string) bool {
	return containsAnyText(line, []string{
		"当前 pipeline 填充槽位",
		"current pipeline slot",
		"validated_upstream_slots",
		"slot_id:",
		"slot_id：",
	})
}

func isAIEmployeeInternalControlLine(line string) bool {
	if line == "<!--pipeline_stop-->" || line == "pipeline_stop" {
		return true
	}
	for _, prefix := range []string{
		"intake_status:",
		"input_status:",
		"slot_status:",
	} {
		if strings.HasPrefix(line, prefix) {
			return true
		}
	}
	return false
}

// sanitizeAIEmployeeTerminalOutput removes transport and orchestration details
// before user-facing quality gates run. This is deliberately domain-agnostic:
// it recognizes pipeline protocol shapes, not employee names or business terms.
func sanitizeAIEmployeeTerminalOutput(task, output string, configuredSkillNames ...string) string {
	cleaned := stripAIEmployeeLeadingMetaPreamble(output)
	cleaned = stripAIEmployeeInternalMechanicLines(cleaned, configuredSkillNames...)
	if !aiEmployeeTaskDiscussesInternalMechanism(task) {
		cleaned = aiEmployeeChineseUpstreamStepRe.ReplaceAllString(cleaned, "基于现有信息的推断")
		cleaned = aiEmployeeEnglishUpstreamStepRe.ReplaceAllString(cleaned, "inference from available information")
		// Standalone "Step N" references can also leak from pipeline context
		// into an otherwise complete table row. Rewrite the orchestration
		// label instead of failing and discarding the user's whole delivery.
		cleaned = aiEmployeeInternalStepNumberRe.ReplaceAllString(cleaned, "现有信息")
	}
	cleaned = removeEmptyMarkdownSections(cleaned)
	return collapseDuplicateMarkdownRules(cleaned)
}

func aiEmployeeTaskDiscussesInternalMechanism(task string) bool {
	return containsAnyText(strings.ToLower(strings.TrimSpace(task)), []string{
		"脚本", "script", "技能", "skill", "内部规范", "系统规范", "pipeline", "工作流规则",
		"step 0", "step 1", "上游步骤", "下游步骤",
	})
}

func hasInspectableAIEmployeeInput(task string) bool {
	normalized := strings.ToLower(strings.TrimSpace(task))
	if normalized == "" {
		return false
	}
	if strings.Contains(normalized, "http://") || strings.Contains(normalized, "https://") {
		return true
	}
	if detectPipelineTargetScope(task) != "" {
		return true
	}
	// A substantial current-turn brief is itself inspectable input even when it
	// has no attachment. Keep the threshold conservative so a one-line vague
	// request can still use the normal missing-input protocol.
	return len([]rune(normalized)) >= 240
}

func countAIEmployeeClarificationQuestions(output string) int {
	lines := strings.Split(strings.ReplaceAll(output, "\r\n", "\n"), "\n")
	sectionCount := 0
	questionSectionSeen := false
	activeQuestionSectionLevel := 0
	var activeSectionLines []string
	flushSection := func() {
		if len(activeSectionLines) == 0 {
			return
		}
		sectionCount += countAIEmployeeQuestionSectionItems(activeSectionLines)
		activeSectionLines = nil
	}
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if level, title, ok := markdownHeadingLevelAndTitle(line); ok {
			if activeQuestionSectionLevel > 0 && level <= activeQuestionSectionLevel {
				flushSection()
				activeQuestionSectionLevel = 0
			}
			if aiEmployeeClarificationSectionTitle(title) {
				questionSectionSeen = true
				activeQuestionSectionLevel = level
			}
			continue
		}
		if activeQuestionSectionLevel > 0 {
			activeSectionLines = append(activeSectionLines, raw)
		}
	}
	flushSection()
	if questionSectionSeen {
		return sectionCount
	}

	// A heading-free answer is a questionnaire only when question-shaped list
	// items dominate its visible content. This keeps a substantive report with
	// embedded rhetorical or unknown-field questions from being rejected.
	questionLines := 0
	visibleLines := 0
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" || strings.Trim(line, "-*_ ") == "" {
			continue
		}
		visibleLines++
		if aiEmployeeLineLooksLikeClarificationQuestion(line) &&
			(markdownOrderedListItemRe.MatchString(raw) ||
				strings.HasPrefix(line, "- ") ||
				strings.HasPrefix(line, "* ")) {
			questionLines++
		}
	}
	if questionLines > 3 && questionLines*2 >= visibleLines {
		return questionLines
	}
	return 0
}

// countAIEmployeeQuestionSectionItems counts the visible top-level questions,
// rather than every question-shaped subfield inside them. A numbered question
// may legitimately group several decision-changing fields on indented lines;
// treating each subfield as another question makes a compliant three-item
// intake fail after the model has already consolidated it.
func countAIEmployeeQuestionSectionItems(lines []string) int {
	type candidate struct {
		indent int
	}
	ordered := make([]candidate, 0)
	bullets := make([]candidate, 0)
	standalone := 0
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		indent := len(raw) - len(strings.TrimLeft(raw, " \t"))
		if match := markdownOrderedListItemRe.FindStringSubmatch(raw); len(match) == 5 {
			ordered = append(ordered, candidate{indent: len(match[1])})
			continue
		}
		if strings.HasPrefix(line, "- ") ||
			strings.HasPrefix(line, "* ") ||
			strings.HasPrefix(line, "+ ") {
			if aiEmployeeLineLooksLikeClarificationQuestion(line) {
				bullets = append(bullets, candidate{indent: indent})
			}
			continue
		}
		if aiEmployeeLineLooksLikeClarificationQuestion(line) {
			standalone++
		}
	}
	countAtMinimumIndent := func(items []candidate) int {
		if len(items) == 0 {
			return 0
		}
		minIndent := items[0].indent
		for _, item := range items[1:] {
			if item.indent < minIndent {
				minIndent = item.indent
			}
		}
		count := 0
		for _, item := range items {
			if item.indent == minIndent {
				count++
			}
		}
		return count
	}
	if len(ordered) > 0 {
		return countAtMinimumIndent(ordered)
	}
	if len(bullets) > 0 {
		return countAtMinimumIndent(bullets)
	}
	return standalone
}

func markdownHeadingLevelAndTitle(line string) (int, string, bool) {
	if !strings.HasPrefix(line, "#") {
		return 0, "", false
	}
	level := 0
	for level < len(line) && line[level] == '#' {
		level++
	}
	if level == 0 || level > 6 || level >= len(line) || line[level] != ' ' {
		return 0, "", false
	}
	return level, strings.TrimSpace(line[level+1:]), true
}

func aiEmployeeClarificationSectionTitle(title string) bool {
	lower := strings.ToLower(strings.TrimSpace(title))
	if strings.Contains(lower, "问题") &&
		containsAnyText(lower, []string{"决定", "改变结论", "待确认", "需确认", "补充", "澄清"}) {
		return true
	}
	return containsAnyText(lower, []string{
		"决定性问题", "改变结论的问题", "待确认", "需确认", "需要确认",
		"待补问题", "待补充问题", "需要补充", "请补充", "请回答",
		"澄清问题", "决定性缺口", "关键缺口",
		"decision-changing question", "questions to confirm", "clarifying questions",
	})
}

func aiEmployeeLineLooksLikeClarificationQuestion(line string) bool {
	line = strings.TrimSpace(line)
	if line == "" {
		return false
	}
	if strings.Contains(line, "？") || strings.Contains(line, "?") {
		return true
	}
	return containsAnyText(line, []string{
		"是否", "是什么", "请描述", "请选择", "请确认", "请提供",
		"三选一", "二选一", "四选一",
	})
}

func containsConditionalDeliveryDeferral(output string) bool {
	conditionMarkers := []string{
		"回复后", "回复之后", "你回复", "确认后", "你确认", "补充后",
		"提供后", "回完", "收到后", "信息齐全后", "材料到齐后",
	}
	deliveryMarkers := []string{
		"交付", "生成", "撰写", "制作", "开始", "继续", "推进",
		"给出", "输出", "完成",
	}
	currentDeliveryMarkers := []string{
		"已交付", "已经交付", "已完成", "已经完成", "现已完成",
		"当前交付", "本次交付", "无需回复", "不影响当前",
	}
	nextIterationMarkers := []string{
		"下一版", "下一稿", "v1", "迭代版", "更新版", "进一步优化",
	}
	lines := strings.Split(strings.ReplaceAll(output, "\r\n", "\n"), "\n")
	for i, raw := range lines {
		line := strings.ToLower(strings.TrimSpace(raw))
		if line == "" || !containsAnyText(line, conditionMarkers) {
			continue
		}
		if containsAnyText(line, deliveryMarkers) &&
			!containsAnyText(line, currentDeliveryMarkers) &&
			!containsAnyText(line, nextIterationMarkers) {
			return true
		}

		// Also catch a compact two-line promise such as “回复后：” followed by
		// “开始生成报告”, without coupling unrelated phrases across a long,
		// already-complete document.
		if !(strings.HasSuffix(line, "：") || strings.HasSuffix(line, ":")) {
			continue
		}
		next := strings.ToLower(nextNonEmptyAIEmployeeLine(lines, i+1))
		if next != "" &&
			containsAnyText(next, deliveryMarkers) &&
			!containsAnyText(next, currentDeliveryMarkers) &&
			!containsAnyText(next, nextIterationMarkers) {
			return true
		}
	}
	return false
}

func buildAIEmployeeMinimumProgressRepairPrompt(basePrompt string, issues []string, previous string) string {
	var b strings.Builder
	b.WriteString(basePrompt)
	b.WriteString("\n\n---\n\n[MINIMUM VIABLE DELIVERY REPAIR]\n")
	b.WriteString("Rewrite the entire answer as the final user-facing response now. Do not mention this repair, internal stages, model behavior, or quality checks. Do not call tools or introduce facts that are absent from the task, tool evidence, or prior draft.\n")
	b.WriteString("Start directly with the answer. Do not prepend a task-analysis note, a third-person recap of what the user wants, a response plan, or private reasoning about whether enough information is available.\n")
	b.WriteString("Use the current request and prior draft to produce the best complete v0 user-facing response that the available evidence supports. Preserve verified facts and useful analysis. Where decisive facts are unknown, use clearly labelled neutral assumptions, alternatives, or placeholders instead of postponing an otherwise useful deliverable.\n")
	b.WriteString("If the original request explicitly puts a user choice, confirmation, or necessary question before later work, the earliest unresolved interaction is the complete deliverable for this turn. Present every user-specified option and confirmation field concisely and in the requested order; do not replace them with different profiling questions, add unrelated analysis or recommendations, or execute stages placed after that interaction.\n")
	b.WriteString("Put no more than three genuinely decision-changing questions at the end. Do not condition the current deliverable on a future reply, promise automatic continuation, or claim that a file exists unless an actual tool receipt proves it.\n")
	if len(issues) > 0 {
		b.WriteString("Detected delivery issues:\n")
		for _, issue := range issues {
			fmt.Fprintf(&b, "- %s\n", issue)
		}
	}
	b.WriteString("\nPrevious draft to rewrite:\n")
	b.WriteString(previous)
	return b.String()
}

func buildAIEmployeeInteractiveHandoffCompactionPrompt(basePrompt, previous string) string {
	var b strings.Builder
	b.WriteString(basePrompt)
	b.WriteString("\n\n---\n\n[STRICT USER INTERACTION HANDOFF]\n")
	b.WriteString("Rewrite the draft as a concise user-facing interaction for the earliest unresolved choice or confirmation in the original request.\n")
	b.WriteString("- Copy every option and every confirmation field explicitly requested by the user. Preserve their meaning and order; do not omit, merge away, rename, recommend, or replace them.\n")
	b.WriteString("- Do not claim the user already selected or supplied anything that is still being asked. Do not infer a company, stage, target, profile, source path, or other answer.\n")
	b.WriteString("- Ask at most three grouped questions. A grouped question may contain all user-requested fields, so no requested field is lost.\n")
	b.WriteString("- Return only the options, necessary confirmation fields, and one compact reply template. Do not include analysis, a fact summary, an outline, a draft, future-stage content, internal mechanics, internal step numbers such as Step 0/Step 1, tools, skills, or policy boundaries.\n")
	b.WriteString("- Keep the complete answer under 1000 Chinese characters or roughly 700 English words. Start directly with natural user-facing language.\n")
	b.WriteString("\nDraft to compact:\n")
	b.WriteString(previous)
	return b.String()
}

func TaskRejectsInteractiveHandoff(task string) bool {
	return aiEmployeeTaskRejectsInteractiveHandoff(task)
}

func TaskRequestsInteractiveHandoff(employeeID, task string) bool {
	return aiEmployeeTaskRequestsInteractiveHandoff(employeeID, task)
}

func MinimumProgressIssues(pipeline *pipelinecore.Pipeline, step *pipelinecore.PipelineStep, output string) []string {
	return aiEmployeeMinimumProgressIssues(pipeline, step, output)
}

func InteractiveResponseProvidesChoice(output string) bool {
	return aiEmployeeInteractiveResponseProvidesChoice(output)
}

func UserFacingOutputIssues(task, output string) []string {
	return aiEmployeeUserFacingOutputIssues(task, output)
}

func SanitizeTerminalOutput(task, output string, configuredSkillNames ...string) string {
	return sanitizeAIEmployeeTerminalOutput(task, output, configuredSkillNames...)
}

func CountClarificationQuestions(output string) int {
	return countAIEmployeeClarificationQuestions(output)
}

func MarkdownHeadingLevelAndTitle(line string) (int, string, bool) {
	return markdownHeadingLevelAndTitle(line)
}

func BuildMinimumProgressRepairPrompt(basePrompt string, issues []string, previous string) string {
	return buildAIEmployeeMinimumProgressRepairPrompt(basePrompt, issues, previous)
}

func BuildInteractiveHandoffCompactionPrompt(basePrompt, previous string) string {
	return buildAIEmployeeInteractiveHandoffCompactionPrompt(basePrompt, previous)
}
