package aiemployee

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode"

	pipelinecore "mac/internal/pipeline"
	investmentadvisory "mac/internal/pipelines/investmentadvisory"
)

type PipelineStep = pipelinecore.PipelineStep
type StepRoute = pipelinecore.StepRoute

const (
	aiEmployeeIntakeStepName    = "任务受理与意图理解"
	aiEmployeeContextPathMarker = "OPENKITTY_AE_CONTEXT_PATH:"
	aiEmployeeContextHeader     = "## 同会话候选上下文（宿主已冻结）"
	aiEmployeeIntakeContextKey  = "ae_intake_spec"
	pipelineStopMarker          = "<!--PIPELINE_STOP-->"
	pipelineWaitingInputMarker  = "INPUT_STATUS: MISSING"
)

var (
	aiEmployeeExplicitLabelRequirement = regexp.MustCompile(`(?:标为|标注为|标记为)\s*[“"'‘]?([^，,。；;\n”"'’]{1,32})`)
)

type IntakeSpec struct {
	EmployeeID     string
	EmployeeName   string
	EmployeeAtName string
	Role           string
	PipelineName   string
	ContextPath    string
	OriginalTask   string
}

type aiEmployeeIntakeSpec = IntakeSpec

type SessionContextMessage struct {
	ID            uint   `json:"id"`
	Role          string `json:"role"`
	AgentName     string `json:"agent_name,omitempty"`
	CreatedAt     string `json:"created_at"`
	SourceLocator string `json:"source_locator"`
	Content       string `json:"content"`
}

type aiEmployeeSessionContextMessage = SessionContextMessage

type aiEmployeeSessionContextBundle struct {
	SessionID  string                            `json:"session_id"`
	CapturedAt string                            `json:"captured_at"`
	Usage      string                            `json:"usage"`
	Messages   []aiEmployeeSessionContextMessage `json:"messages"`
}

func withAIEmployeeIntakeSpec(ctx context.Context, spec aiEmployeeIntakeSpec) context.Context {
	return context.WithValue(ctx, aiEmployeeIntakeContextKey, spec)
}

func aiEmployeeIntakeSpecFromContext(ctx context.Context) (aiEmployeeIntakeSpec, bool) {
	if ctx == nil {
		return aiEmployeeIntakeSpec{}, false
	}
	spec, ok := ctx.Value(aiEmployeeIntakeContextKey).(aiEmployeeIntakeSpec)
	return spec, ok && strings.TrimSpace(spec.EmployeeID) != ""
}

// MaterializeSessionContext freezes an already-authorized, bounded chat slice
// so the intake step can evaluate it. Querying and content redaction stay with
// the application host; this package only serializes the supplied messages.
func MaterializeSessionContext(sessionID, workspace string, messages []SessionContextMessage, now time.Time) (string, string) {
	if strings.TrimSpace(sessionID) == "" || strings.TrimSpace(workspace) == "" || len(messages) == 0 {
		return "", ""
	}
	if now.IsZero() {
		now = time.Now()
	}

	bundle := aiEmployeeSessionContextBundle{
		SessionID:  sessionID,
		CapturedAt: now.Format(time.RFC3339Nano),
		Usage:      "Candidate same-session context. The intake step must decide relevance; current explicit user instructions always win.",
		Messages:   messages,
	}
	data, err := json.MarshalIndent(bundle, "", "  ")
	if err != nil {
		return "", ""
	}
	relPath := filepath.Join(
		".openkitty", "storage", ".system", "ae_context",
		fmt.Sprintf("%s_%d.json", sanitizeIntakeFilename(sessionID), now.UnixNano()),
	)
	fullPath := filepath.Join(workspace, relPath)
	if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
		return "", ""
	}
	if err := os.WriteFile(fullPath, data, 0644); err != nil {
		return "", ""
	}

	// Keep the inline preview focused on the latest useful exchange. The exact
	// file remains available to a later source-acquisition step through read_file.
	previewStart := len(messages) - 4
	if previewStart < 0 {
		previewStart = 0
	}
	var preview strings.Builder
	for _, message := range messages[previewStart:] {
		label := "用户"
		if message.Role == "assistant" {
			label = "助手"
			if strings.TrimSpace(message.AgentName) != "" {
				label = message.AgentName
			}
		}
		fmt.Fprintf(&preview, "- [%s · %s] %s\n", label, message.SourceLocator, truncateIntakeRunes(message.Content, 1800))
	}
	relDisplay := filepath.ToSlash(relPath)
	contextBlock := fmt.Sprintf(`%s
%s %s

这是当前会话中已经完成的近期对话，不是自动成立的任务事实。第一步必须先判断相关性：
- 当前用户本轮的明确要求优先级最高；
- 若近期结果与当前任务直接相关且信息足够，应直接复用，不得机械要求用户重新提供链接、文件或主题；
- 若不相关，忽略它并按当前任务自主执行；
- 业务步骤若要把它作为正式素材，先用 read_file 读取上述文件，并保留其中的 session:// 来源定位。

近期内容预览：
%s`, aiEmployeeContextHeader, aiEmployeeContextPathMarker, relDisplay, strings.TrimSpace(preview.String()))
	return relDisplay, strings.TrimSpace(contextBlock)
}

func sanitizeIntakeFilename(name string) string {
	name = strings.Map(func(r rune) rune {
		if strings.ContainsRune(`/\:*?"<>|`, r) {
			return '_'
		}
		return r
	}, name)
	name = strings.TrimSpace(name)
	if name == "" {
		return "context"
	}
	return name
}

func truncateIntakeRunes(value string, maximum int) string {
	if maximum <= 0 {
		return value
	}
	runes := []rune(value)
	if len(runes) <= maximum {
		return value
	}
	return string(runes[:maximum]) + "..."
}

func truncateRunes(value string, maximum int) string {
	return truncateIntakeRunes(value, maximum)
}

func stripPipelineControlMarkers(output string) string {
	output = strings.ReplaceAll(output, pipelineStopMarker, "")
	output = strings.ReplaceAll(output, pipelineWaitingInputMarker, "")
	output = strings.ReplaceAll(output, pipelinecore.SlotFilledMarker, "")
	output = strings.ReplaceAll(output, pipelinecore.SlotMissingMarker, "")
	return strings.TrimSpace(output)
}

func hasStandaloneFinalPipelineStopMarker(output string) bool {
	lines := strings.Split(strings.ReplaceAll(output, "\r\n", "\n"), "\n")
	for index := len(lines) - 1; index >= 0; index-- {
		line := strings.TrimSpace(lines[index])
		if line != "" {
			return line == pipelineStopMarker
		}
	}
	return false
}

func extractAIEmployeeContextPath(input string) string {
	return pipelinecore.ExtractContextPath(input, aiEmployeeContextPathMarker)
}

func stripAIEmployeeContextBlock(input string) string {
	return pipelinecore.StripContextBlock(input, aiEmployeeContextHeader, aiEmployeeContextPathMarker)
}

func prependAIEmployeeIntakeStep(pipeline Pipeline, spec aiEmployeeIntakeSpec) Pipeline {
	pipeline.AIEmployeeID = strings.TrimSpace(spec.EmployeeID)
	pipeline.AIEmployeeName = strings.TrimSpace(spec.EmployeeName)
	pipeline.AIEmployeeAtName = strings.TrimSpace(spec.EmployeeAtName)
	if len(pipeline.Steps) > 0 && strings.TrimSpace(pipeline.Steps[0].Name) == aiEmployeeIntakeStepName {
		pipeline.AIEmployeeOriginalTask = strings.TrimSpace(spec.OriginalTask)
		pipeline.AIEmployeeContextPath = strings.TrimSpace(spec.ContextPath)
		return pipeline
	}
	// Work on a run-local step slice. Dynamic/static pipeline definitions may
	// share backing arrays, and intake must never mutate the global definition.
	businessSteps := append([]PipelineStep(nil), pipeline.Steps...)
	if len(businessSteps) > 0 && strings.TrimSpace(spec.ContextPath) != "" && !strings.EqualFold(strings.TrimSpace(pipeline.Name), "lead-research") {
		first := businessSteps[0]
		first.Tools = append([]string(nil), first.Tools...)
		if !first.NoTools && first.ProfessionalAgent == nil && len(first.Tools) > 0 && !aiEmployeeToolListed(first.Tools, "read_file") {
			first.Tools = append(first.Tools, "read_file")
		}
		first.Prompt = fmt.Sprintf(`## 同会话上下文接续规则
受理步骤会判断宿主候选上下文是否与当前请求直接相关。若受理结果决定复用，先用 read_file 读取 %s，再开始新的搜索或要求用户补资料；若受理结果决定忽略，则按本轮请求执行。当前用户本轮明确要求始终优先。

%s`, spec.ContextPath, first.Prompt)
		businessSteps[0] = first
	}
	pipeline.Steps = businessSteps
	pipeline.AIEmployeeOriginalTask = strings.TrimSpace(spec.OriginalTask)
	pipeline.AIEmployeeContextPath = strings.TrimSpace(spec.ContextPath)
	intake := PipelineStep{
		Name:             aiEmployeeIntakeStepName,
		Prompt:           buildAIEmployeeIntakePrompt(pipeline, spec),
		NoTools:          true,
		MaxIter:          2,
		OutputTokenLimit: 1800,
		Routing: []StepRoute{{
			When: "contains '<!--PIPELINE_STOP-->'",
			Goto: "__stop__",
		}},
	}
	steps := make([]PipelineStep, 0, len(businessSteps)+1)
	steps = append(steps, intake)
	steps = append(steps, businessSteps...)
	pipeline.Steps = steps
	return pipeline
}

func aiEmployeeToolListed(tools []string, want string) bool {
	for _, tool := range tools {
		if strings.EqualFold(strings.TrimSpace(tool), strings.TrimSpace(want)) {
			return true
		}
	}
	return false
}

func buildAIEmployeeIntakePrompt(pipeline Pipeline, spec aiEmployeeIntakeSpec) string {
	var workflow []string
	for _, step := range pipeline.Steps {
		name := strings.TrimSpace(step.Name)
		if name == "" {
			continue
		}
		permissions := "遵循 Pipeline 默认工具边界"
		if step.NoTools {
			permissions = "无工具权限（仅分析已有上下文）"
		} else if len(step.Tools) > 0 {
			permissions = strings.Join(uniqueSortedStrings(step.Tools), "、")
		}
		workflow = append(workflow, fmt.Sprintf("- %s：允许 %s", name, permissions))
	}
	delivery := buildAEPipelineDeliveryStandards(pipeline.Steps)
	contextRule := "当前没有宿主冻结的同会话候选上下文；根据本轮请求判断。"
	if strings.TrimSpace(spec.ContextPath) != "" {
		contextRule = fmt.Sprintf("宿主提供了候选上下文 %s；先判断相关性。若足以支撑任务，后续业务步骤必须优先读取并复用，不得要求用户重复提供。", spec.ContextPath)
	}
	targetRule := ""
	if target := detectPipelineTargetScope(spec.OriginalTask); target != "" && aiEmployeePipelineCanInspectExplicitTarget(pipeline) {
		targetRule = fmt.Sprintf(`当前请求已明确给出本地目标路径 %s：
- 该路径本身已经足以启动后续“读取/扫描目标”的业务步骤，受理步骤必须判定 READY。
- 本步骤没有工具权限，不得根据旧对话中对其他路径的扫描结果，声称当前路径不存在、不可访问或尚未探测。
- 当前路径覆盖历史对话中的旧路径或旧扫描结论；实际可访问性和项目内容由首个获准只读工具的业务步骤验证。`, target)
	}
	domainRule := buildAIEmployeeDomainIntakeRule(pipeline)
	return fmt.Sprintf(`你是所有 AI员工共用的“任务受理与意图理解”步骤。本步骤只形成执行简报，不做业务研究、不调用工具、不生成最终交付物。

AI员工：%s（%s）
职责：%s
Pipeline：%s

你必须结合“当前用户请求”和输入中宿主提供的同会话候选上下文，先理解用户真正想得到的结果，再决定是否可以启动后续流程。

判断优先级：
1. 当前用户明确写出的目标、范围、格式和限制；
2. 当前用户给出的文件、URL、正文或数据；
3. 同会话刚完成且与本任务直接相关的结果；
4. 后续步骤获准执行的自主只读检索或分析。

约束分级规则（所有 AI员工通用）：
- 任务身份坐标：精确标的、项目路径、指定文件/来源不可替换；地区、行业、企业规模、时间和数量是筛选维度，不得同时列为不可替换身份。
- 硬约束：安全、权限、明确排除项，以及“仅限/只要/必须/至少/至多/不超过/不要/禁止”和明确数值上下限；不得静默违反。若用户同时明确授权“没有就放宽/类似的也可以/不足时随便”等兜底，则相应业务条件按用户指定顺序降为可放宽条件。
- 优先条件：地区、行业、企业类型/规模、时间、数量、风格等普通描述。先严格满足；结果不足时按对目标影响从小到大逐项放宽，不要因为偏好暂时无结果就停止整个任务。
- 放宽后的结果必须与完全匹配结果分层，逐项标注满足/未满足/待核验；硬约束无结果时报告零个精确匹配，可另列明确不合格原因的扩展候选供用户选择。
- 自动放宽没有用户指定先后顺序时，按对目标价值影响最小的条件先放宽，并把顺序标为“系统推断”，不得声称是用户指定。

%s

%s

%s

后续步骤与权限边界：
%s
- 每一步只能使用上面列出的工具；未列出的权限默认没有。
- 只有明确列出发布、发送、部署或其他外部写操作的步骤才能产生相应外部副作用；否则只能本地交付。

交付标准：
%s

输出固定包含以下部分：
## 意图理解
- 目标结果：用一句话说明用户最终要拿到什么。
- 已有输入：列出本轮输入，以及决定复用或忽略的同会话信息。
- 合理推断：说明受众、范围、风格等可安全推断项；不要把可推断项反问给用户。

## 约束账本
- 任务身份坐标：只列不可替换的标的、路径、指定素材或数据源；不得把地区、行业、企业规模、时间和数量等筛选维度列入此项。
- 硬约束：列出不能静默放宽的权限、安全、排除项和明确上下限；没有则写“无额外硬约束”。
- 优先条件：列出地区、行业、企业规模、时间、数量、风格等排序偏好。
- 自动放宽顺序：说明结果不足时先放宽什么、后放宽什么，以及如何标记近似候选；任务身份坐标和硬约束不得列入自动放宽。

## 执行边界
- 能做什么：基于上述步骤和权限说明。
- 不能做什么：列出缺失权限、禁止编造和外部副作用边界。

## 执行流程与交付标准
- 按顺序列出将启动的业务步骤。
- 明确最终文件/正文/报告/图表等交付物和验收要求。
- 只能承诺后续步骤权限和 Artifact Contract 真正支持的交付形式；如果没有 write_file/专用产物工具或文件契约，只能承诺聊天正文，不得虚构落盘路径。

## 输入判定
- 信息足够时输出“INTAKE_STATUS: READY”，随后 Pipeline 自动进入业务步骤。
- 只有缺少“无法通过同会话上下文或获准只读能力取得、且会实质改变交付结果”的决定性输入时，才简洁说明需要补什么，并另起一行输出“INPUT_STATUS: MISSING”，最后一行精确输出“<!--PIPELINE_STOP-->”。
- 如果用户明确要求你先呈现选项、确认范围、询问必要信息或等待用户确认，那么“面向用户给出选项/最少问题”本身就是当前可交付结果。此时必须判定 READY，让后续面向用户的业务步骤完成该交互；不得仅因用户尚未回答你被要求提出的问题而判定 MISSING。
- 原创主题、今日热点、行业分析、总结/改写等可由后续获准能力自主完成的任务，不得因为用户没给链接而判定缺失。
- 判定 MISSING 会终止本次 AI员工执行，不保留可由下一条普通消息续接的运行状态。必须明确告诉用户：将原需求与缺失信息合并成一条完整新请求，并重新 %s；新请求从第一个步骤重新执行。不得说“收到后继续”“自动继续”或暗示不重新 @ 也能续接。`,
		strings.TrimSpace(spec.EmployeeName), strings.TrimSpace(spec.EmployeeID), strings.TrimSpace(spec.Role), strings.TrimSpace(spec.PipelineName),
		contextRule, targetRule, domainRule, strings.Join(workflow, "\n"), delivery, aiEmployeeMention(spec.EmployeeAtName, spec.EmployeeName, spec.EmployeeID))
}

func buildAIEmployeeDomainIntakeRule(pipeline Pipeline) string {
	if strings.TrimSpace(pipeline.AIEmployeeID) == "fundraising-advisor" {
		return `融资专家专用受理规则：
- 本步骤只识别任务对象和事实边界，不替用户设计 BP 架构，不根据“还有、以及、参考、能力、场景、结果”等连接词推断产品线、业务线、主副线、产品矩阵或第一/第二曲线。
- 将用户明确称为自有产品的对象记为“用户称为自有产品”；明确称为参考的对象记为“参考对象”；其余连续描述原样保留为一条“用户补充描述”，关系与实现状态均记为未知。不得拆成编号、字母或“方向一/二/三”，也不得改称候选产品、候选能力、规划方向或独立业务。
- 只有用户或可核验证据明确说明关系与状态时，才能记录其属于某个产品、模块、业务或已上线/规划中；“未提供证据”不等于“未实现”“未落地”或“仅在规划”。
- 未提供融资轮次、公司阶段、目标金额、估值、里程碑或商业数字时一律记为未知，不得从“没有披露”推断天使、种子、Pre-A、早期或任何轮次。
- 执行顺序必须是：公司、产品与对比研究 → 融资专业分析与交付。研究步骤先获取主体与产品基本情况、指定材料及一致维度的比较证据；融资方法只能在研究之后使用。`
	}
	switch strings.ToLower(strings.TrimSpace(pipeline.Name)) {
	case "lead-research":
		return `潜客研究专用受理规则：
- 先识别用户要研究的目标产品。用户明确给出产品名称、描述、目录或文件中的任一种，且后续步骤具备只读分析能力时，可判定 READY，由后续步骤主动读取和分析细节，不要求用户预先整理完整产品资料或提供本地路径。
- 用户只给出产品名称，或者明确说产品“网上能找到”时，只要后续产品画像步骤具备 web_search/web_fetch，也应判定 READY；由后续步骤查找并核验产品自身的一方网页。不得因为默认工作区没有资料就要求用户补文件。
- 用户明确指定的产品名称是任务边界，目录中的其他项目、EXE、工具名称和旧报告不得替换目标产品。
- 如果当前请求既没有可识别的产品名称/描述，也没有可检查的目录/文件或相关同会话上下文，必须返回 INPUT_STATUS: MISSING，并询问目标产品或资料位置。
- 受理步骤没有工具权限，不得在这里猜测产品定位或认定产品画像、市场结论、候选公司已经验证完成；读取后发现产品身份冲突或核心定位无法唯一确定时，由“产品画像与 SWOT”步骤停止并请求用户补充。
- 同会话旧助手回答、旧潜客报告、旧 S/A/B 评级、定价建议和候选名单只可列为“待验证历史假设”；不得写成“必须复用”的事实或强制下游沿用。
- 大模型厂商、Agent/RPA/NLP 软件厂商、集成商等供给方不得因为“与产品相关”就被预分类为购买方；必须由后续实体分类和购买信号证据判定。
- 执行顺序必须是：产品画像与 SWOT → 搜索策略 → 多源搜索与核验 → 报告整理。禁止跳过前两步直接搜索。`
	case "investment-advisory":
		return `投资研究专用受理规则：
- 先识别 INVESTMENT_INTENT：market_brief、instrument_analysis、sector_analysis、fund_etf_analysis、event_explanation、general_strategy、personalized_position、portfolio_review、education。
- 再识别 DECISION_LEVEL：information、analysis、general_strategy、personalized_action、execution；以及 ANSWER_DEPTH：quick、standard、deep。
- 只要能识别出金融研究动作，以及整体市场、某个市场/指数、个股/公司/代码、行业/板块/主题、基金/ETF、债券等任一研究对象，就属于可执行请求。
- “整体市场/大盘/当前或某日行情”本身就是完整的 market 类研究对象。未点名具体市场时，应结合用户语言、时区、当前交易时段和可取得的数据选择主要本地市场作为明确假设，并补充关键跨市场联动；不得仅为缩小范围而反问。
- information、analysis 和 general_strategy 必须判定 READY。投资周期、风险偏好、持仓、成本、账户规模和可交易数量未提供时，分别标为 unknown 或 not_provided，先做中性的信息分析、条件场景和风险提示；这些可选个性化信息不得阻断行情、标的、行业、事件或一般策略研究。
- 出现“买、卖、操作”等字样不等于需要个性化信息。没有明确结合本人持仓、资金或风险时，按 general_strategy 给触发条件、观察位置和失效条件，不询问个人参数。
- 只有 personalized_action 明确要求结合本人持仓形成加减仓、止盈止损或精确仓位方案时，才检查持仓成本、仓位比例、持有周期和风险承受能力；账户规模只在计算具体金额或数量时需要。
- 请求范围较宽但仍可执行时，采用保守、中性的默认口径并公开说明假设，不得把“希望更个性化”误判成“缺少决定性输入”。
- 只有 personalized_action 缺少会实质改变操作方案的个人参数，或者连金融研究对象和动作都无法识别且后续获准只读能力也无法解析时，才可返回 INPUT_STATUS: MISSING。
- 判定 READY 时，在输出末尾逐行给出：INVESTMENT_INTENT、DECISION_LEVEL、ANSWER_DEPTH、PERSONALIZATION_REQUIRED、MISSING_DECISIVE_FIELDS。以上是内部编排字段，不得在最终用户报告中展示。`
	case "ppt-polish":
		return `PPT 设计与润色专用受理规则：
- 先识别 PPT_INPUT_MODE：existing_deck（已有 PPT/PPTX）、source_material（只有介绍材料/Word/PDF/Markdown/图片/URL）、detailed_brief（用户详细给出内容和要求）、concept_only（只有明确主题或简短描述）。
- existing_deck：读取现有结构和内容，保留事实与必要数据，再做内容和视觉重构。
- source_material：把材料视为内容证据，从零规划演示结构；不得因为没有原始 PPTX 而要求用户补 PPT 文件。
- detailed_brief：以用户明确的受众、用途、观点、页数、风格和输出要求为硬输入，缺失的普通设计参数采用合理默认值。
- concept_only：只要有明确主题、产品、项目或要表达的问题，就使用中性的默认假设形成精简演示稿；不得机械询问受众、用途、页数或配色。没有材料支持的产品数字、客户案例和商业结论不得编造。
- 只有既没有可读取材料，也没有可识别主题/产品/项目/问题时，才可返回 INPUT_STATUS: MISSING。
- 判定 READY 时，在输出末尾给出 PPT_INPUT_MODE；这是内部编排字段，不得在最终交付说明中展示。`
	default:
		return ""
	}
}

func aiEmployeeMention(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(value), "@"))
		if value != "" {
			return "@" + value
		}
	}
	return "@该AI员工"
}

func aiEmployeePipelineMention(pipeline Pipeline) string {
	return aiEmployeeMention(pipeline.AIEmployeeAtName, pipeline.AIEmployeeName, pipeline.AIEmployeeID)
}

func aiEmployeeStepAllowsMissingInput(step *PipelineStep) bool {
	if step == nil {
		return false
	}
	return strings.TrimSpace(step.Name) == aiEmployeeIntakeStepName || step.AllowMissingInput
}

func appendAIEmployeeClarificationProtocol(prompt string, pipeline Pipeline, step *PipelineStep) string {
	if strings.TrimSpace(pipeline.AIEmployeeID) == "" && strings.TrimSpace(pipeline.AIEmployeeAtName) == "" {
		return prompt
	}
	if !aiEmployeeStepAllowsMissingInput(step) {
		return fmt.Sprintf(`%s

---

## AI员工自主执行与证据降级协议
- 本步骤已经处于受理完成后的业务执行阶段，无权把工具失败、网页不可达、公开数据缺失、证据覆盖不足、外部服务超时或迭代预算不足升级为“用户输入缺失”。
- 遇到上述情况时，保留已取得的有效结果，明确标注 unknown、fetch_failed、source_conflict 或降级范围，并把结果交给后续步骤继续收敛。
- 当前消息、用户明确提供的 URL/附件/正文、受理结果和上游步骤产出是本步骤的工作边界。除非用户明确要求扫描工作区，或步骤契约明确要求发现本地目标，否则不得枚举无关目录、读取无关项目或用工作区旧文件替代用户给定材料。
- 只能调用本步骤当前实际提供的工具。若某个工具不可用，只能改用另一个已提供且语义合适的工具，或基于现有证据形成降级结果；不得通过宽泛 shell/目录扫描模拟缺失工具。
- 调用工具时必须严格使用该工具发布的顶层参数名和层级；必填字段不得被 intent、context、mode 等自造元数据替代。若返回 TOOL SCHEMA REPAIR，按错误中列出的精确字段修正一次，不要重复原调用。
- 输出媒介由用户当前请求明确决定。报告、方案、计划书、BP 等领域名词本身不等于 PPTX、PDF、Word、表格或落盘文件授权；用户未明确指定文件或格式时，必须在聊天正文完整交付，不得为了“更完整”自行新增文件制作、格式转换或无关工具探索。
- 禁止输出 INPUT_STATUS: MISSING、%s 或要求用户重新 @ 发起。只有系统明确授权可晚期澄清的步骤才能因真正的决定性用户输入缺口停止。`, prompt, pipelineStopMarker)
	}
	return fmt.Sprintf(`%s

---

## AI员工自主执行与必要缺失协议
- 默认自主执行。只要当前请求能形成有用交付，就应使用获准的只读能力、合理的中性假设和明确的不确定性标注继续工作；不得因缺少可选偏好、个性化信息或用户未主动整理的公开资料而反问。
- 只有缺口无法通过当前上下文、获准只读能力或合理假设解决，并且会实质改变任务对象或交付结果时，才允许用最少问题说明决定性缺口，并输出 INPUT_STATUS: MISSING 与最终的 <!--PIPELINE_STOP-->。
- 一旦输出 MISSING，本次执行立即终止，不会由用户下一条普通消息自动续接。必须明确要求用户发送一条以 %s 开头的完整新请求，把原需求和缺失信息一次写全；新请求从第一个步骤重新执行。
- 不得使用“收到后继续”“自动继续”“直接补充即可”等暗示隐式续接的措辞。`, prompt, aiEmployeePipelineMention(pipeline))
}

func stripAIEmployeeRestartSection(output string) string {
	base := strings.TrimSpace(stripPipelineControlMarkers(output))
	for _, heading := range []string{"## 重新发起方式", "### 重新发起方式"} {
		if index := strings.Index(base, heading); index >= 0 {
			base = strings.TrimSpace(base[:index])
		}
	}
	return base
}

func normalizeAIEmployeeStepWaitingInputOutput(output string, pipeline Pipeline, step *PipelineStep) (string, bool) {
	if !pipelineOutputWaitsForInput(output) {
		return output, false
	}
	if aiEmployeeStepAllowsMissingInput(step) {
		return normalizeAIEmployeeWaitingInputOutput(output, pipeline), false
	}
	base := stripAIEmployeeRestartSection(output)
	if base == "" {
		base = "本步骤未取得足够证据，后续步骤必须在现有信息范围内形成降级结果。"
	}
	base += "\n\nDATA_STATUS: degraded\n当前缺口来自工具、外部数据或证据覆盖，不属于需要用户补充的决定性输入；Pipeline 将继续完成可用答复。"
	return strings.TrimSpace(base), true
}

func normalizeAIEmployeeWaitingInputOutput(output string, pipeline Pipeline) string {
	if !pipelineOutputWaitsForInput(output) {
		return output
	}
	mention := aiEmployeePipelineMention(pipeline)
	if mention == "@该AI员工" && strings.TrimSpace(pipeline.AIEmployeeID) == "" {
		return output
	}
	base := strings.TrimSpace(stripPipelineControlMarkers(output))
	for _, phrase := range []string{
		"收到后我会继续", "收到后继续", "补充后我会继续", "补充后继续",
		"回复后我会继续", "回复后继续", "可自动继续", "自动继续", "直接补充即可",
	} {
		base = strings.ReplaceAll(base, phrase, "请在重新 @ 发起的完整新请求中提供")
	}
	if strings.Contains(base, "## 重新发起方式") && strings.Contains(base, mention) {
		return strings.TrimSpace(base + "\n\nINPUT_STATUS: MISSING\n" + pipelineStopMarker)
	}
	restart := fmt.Sprintf(`## 重新发起方式
本次 AI员工执行已停止，不保留可由下一条普通消息续接的状态。请发送一条新的完整请求，以 %s 开头，并把原需求与上述缺失信息合并后一次写全。新请求将从第一个步骤重新执行。

以上若出现“继续、续接、收到后处理”等表述，均以本节为准。`, mention)
	return strings.TrimSpace(base + "\n\n" + restart + "\n\nINPUT_STATUS: MISSING\n" + pipelineStopMarker)
}

func aiEmployeeOutputRequiresFreshInvocation(output string, pipeline Pipeline) bool {
	return pipelineOutputWaitsForInput(output) &&
		(strings.TrimSpace(pipeline.AIEmployeeID) != "" || strings.TrimSpace(pipeline.AIEmployeeAtName) != "")
}

func aiEmployeePipelineCanInspectExplicitTarget(pipeline Pipeline) bool {
	for _, step := range pipeline.Steps {
		if strings.TrimSpace(step.Name) == aiEmployeeIntakeStepName {
			continue
		}
		if step.NoTools || step.ProfessionalAgent != nil {
			return false
		}
		for _, tool := range []string{"read_file", "list_dir", "glob", "grep", "extract", "persistent_shell"} {
			if aiEmployeeToolListed(step.Tools, tool) {
				return true
			}
		}
		return false
	}
	return false
}

// normalizeAIEmployeeIntakeDecision keeps intent understanding model-driven,
// but prevents an internal intake gate from blocking either an explicitly
// requested choice/confirmation interaction or a current-turn path that the
// first business step is authorized to inspect.
func normalizeAIEmployeeIntakeDecision(pipeline Pipeline, input, output string) (string, bool) {
	waitingForInput := pipelineOutputWaitsForInput(output)
	hasFinalStop := hasStandaloneFinalPipelineStopMarker(output)
	if !waitingForInput && !hasFinalStop {
		return output, false
	}

	current := strings.TrimSpace(pipeline.AIEmployeeOriginalTask)
	if current == "" {
		current = strings.TrimSpace(stripAIEmployeeContextBlock(input))
	}
	if hasFinalStop && aiEmployeeTaskRequestsInteractiveHandoff(pipeline.AIEmployeeID, current) {
		base := stripAIEmployeeRestartSection(output)
		if index := strings.Index(base, "## 输入判定"); index >= 0 {
			base = strings.TrimSpace(base[:index])
		}
		return strings.TrimSpace(base + `

## 输入判定
- 当前用户明确要求先呈现选项、确认范围或收集必要信息；该交互本身就是本轮可交付结果。
- 后续面向用户的业务步骤应直接给出清晰选项与最少决策问题，不得启动尚未由用户选择的分支，也不得要求用户复制整条请求重新发起。

INTAKE_STATUS: READY`), true
	}
	if !waitingForInput {
		// The intake contract reserves a terminal stop for a decisive missing
		// input. A model can occasionally emit the stop marker after declaring
		// the request READY; treating that contradictory marker as authoritative
		// exposes the internal intake brief and skips every business step.
		// Without the missing-input status, continue the pipeline.
		return strings.TrimSpace(strings.ReplaceAll(output, pipelineStopMarker, "")), true
	}
	if !aiEmployeePipelineCanInspectExplicitTarget(pipeline) {
		return output, false
	}
	target := detectPipelineTargetScope(current)
	if target == "" {
		return output, false
	}

	steps := make([]string, 0, len(pipeline.Steps))
	for _, step := range pipeline.Steps {
		if name := strings.TrimSpace(step.Name); name != "" && name != aiEmployeeIntakeStepName {
			steps = append(steps, name)
		}
	}
	flow := "读取并验证目标后完成业务交付"
	if len(steps) > 0 {
		flow = strings.Join(steps, " → ")
	}
	contextDecision := "旧对话不得覆盖当前路径；只复用与当前目标一致且能在本轮验证的信息。"
	if strings.TrimSpace(pipeline.AIEmployeeContextPath) == "" {
		contextDecision = "没有需要优先复用的同会话材料。"
	}
	return fmt.Sprintf(`## 意图理解
- 目标结果：%s
- 已有输入：当前用户已明确提供目标路径 %s；该输入足以启动后续只读检查。
- 历史上下文取舍：%s
- 合理推断：在用户已明确的地域、对象和交付范围内自主完成可由后续获准能力取得的信息，不重复追问路径。

## 约束账本
- 任务身份坐标：目标路径 %s，不得替换成历史路径或其他项目。
- 硬约束：工具权限、安全边界，以及用户明确使用“仅限/必须/至少/至多/不要/禁止”或明确数值上下限表达的条件，不得静默放宽；用户明确授权不足时放宽的业务条件除外。
- 优先条件：地区、行业、企业规模、时间、数量和风格等普通描述先严格满足；结果不足时可逐项放宽并标注。
- 自动放宽顺序：由后续业务步骤按对目标影响从小到大执行；完全匹配与近似候选分层，目标路径永不放宽。

## 执行边界
- 能做什么：首个业务步骤读取或扫描 %s，验证项目内容后继续后续流程。
- 不能做什么：受理步骤不虚构路径可访问性，不沿用其他路径的旧扫描结论，不执行未授权的外部写操作。

## 执行流程与交付标准
- 后续流程：%s
- 验收标准：每一步填入实际结果；最终交付必须基于本轮目标读取和搜索证据，不得用计划、旧项目资料或占位内容冒充。

## 输入判定
INTAKE_STATUS: READY`, truncateRunes(current, 1200), target, contextDecision, target, target, flow), true
}

// appendAIEmployeeInteractiveHandoffInstruction keeps an explicitly requested
// choice or confirmation as a real user-facing turn boundary. The intake step
// still supplies structured context, while the business step presents the
// earliest unresolved gate without exposing that internal structure or
// prematurely executing work the user placed after the gate.
func appendAIEmployeeInteractiveHandoffInstruction(prompt string, pipeline Pipeline, step *PipelineStep, stepIndex int) string {
	if step == nil || stepIndex <= 0 || stepIndex > len(pipeline.Steps)-1 ||
		strings.TrimSpace(step.Name) == aiEmployeeIntakeStepName ||
		strings.TrimSpace(pipeline.Steps[stepIndex-1].Name) != aiEmployeeIntakeStepName {
		return prompt
	}
	task := strings.TrimSpace(pipeline.AIEmployeeOriginalTask)
	if task == "" || !aiEmployeeTaskRequestsInteractiveHandoff(pipeline.AIEmployeeID, task) {
		return prompt
	}
	return prompt + `

---

[CURRENT-TURN INTERACTION BOUNDARY]
The original request explicitly places one or more user choices, confirmations, or necessary questions in a sequence before later work.
- Find the earliest unresolved user-controlled interaction in that requested sequence.
- Complete only work the user explicitly placed before that interaction and that does not depend on its answer.
- Present every option and confirmation field the user explicitly requested at this gate. Preserve their meaning and order; do not replace them with different profiling questions, inferred alternatives, or your own recommendation.
- Ask only the decision-changing questions needed at this gate, and do not add unrelated questions before every user-requested field is covered. Group related fields so the interaction stays concise.
- Treat that interaction as the complete user-facing deliverable for this turn. Do not draft, simulate, prefill, or execute stages the user placed after it.
- Do not add an analysis, fact summary, recommendation, template, outline, or future-stage preview unless the user explicitly placed it before the earliest unresolved interaction.
- Do not expose or summarize an upstream ledger, slot, pipeline, skill, tool, system instruction, execution contract, or internal step. Start directly with natural user-facing language.
`
}

// buildAIEmployeeDeterministicMissingInput handles the small set of missing
// inputs that can be proven without asking a model. The language model still
// performs intent understanding for every normal request; this guard only
// prevents an obvious source-dependent task from inventing a topic or scanning
// the workspace after the employee's own domain gate says it must ask first.
// A frozen same-session context deliberately disables the shortcut because the
// intake model must first decide whether that context supplies the missing input.
func buildAIEmployeeDeterministicMissingInput(pipeline Pipeline, input string) (string, bool) {
	if strings.TrimSpace(extractAIEmployeeContextPath(input)) != "" {
		return "", false
	}
	current := strings.TrimSpace(pipeline.AIEmployeeOriginalTask)
	if current == "" {
		current = strings.TrimSpace(stripAIEmployeeContextBlock(input))
	}
	if aiEmployeeRequestIsSemanticallyEmpty(current) {
		result := `当前请求只有泛化指令，缺少可执行的任务动作与对象。请用一句完整话说明希望 AI 员工完成什么，并附上已有主题、材料、链接或关键事实（有哪项给哪项）。

INPUT_STATUS: MISSING
<!--PIPELINE_STOP-->`
		return normalizeAIEmployeeWaitingInputOutput(result, pipeline), true
	}
	name := strings.ToLower(strings.TrimSpace(pipeline.Name))

	var missing, request string
	switch name {
	case "wechat-article":
		if !aiEmployeeWechatRequestHasTopicOrSource(current) {
			missing = "文章主题/核心观点/目标读者，或者可读取的素材、链接、文件"
			request = "缺少决定性输入：文章主题（最好带核心观点或目标读者），或者需要整理的素材、链接、文件。"
		}
	case "ppt-polish":
		if !aiEmployeePPTRequestHasActionableInput(current) {
			missing = "可识别的演示主题/产品/项目/问题，或者可读取的 PPT、介绍材料、链接、文件"
			request = "缺少决定性输入：请给出要制作或润色的演示主题，或者提供可读取的 PPT、介绍材料、链接、文件。"
		}
	case "bid-proposal":
		if !aiEmployeeRequestHasReadableMaterial(current, 240) {
			missing = "可读取的招标/采购文件路径、URL、附件路径或足够完整的正文"
			request = "缺少决定性输入：招标/采购文件、可访问链接、附件路径或足够完整的正文。"
		}
	case "investment-advisory":
		profile := investmentadvisory.ClassifyInvestmentRequest(current)
		if profile.PersonalizationRequired && len(profile.MissingDecisiveFields) > 0 {
			missing = strings.Join(profile.MissingDecisiveFields, "、")
			request = "这是结合本人持仓的个性化操作请求。缺少会实质改变方案的参数：" + missing + "。"
		}
	}

	if missing == "" {
		return "", false
	}

	steps := make([]string, 0, len(pipeline.Steps))
	for _, step := range pipeline.Steps {
		if stepName := strings.TrimSpace(step.Name); stepName != "" && stepName != aiEmployeeIntakeStepName {
			steps = append(steps, stepName)
		}
	}
	flow := "重新 @该AI员工并提交完整新请求后，从第一个业务步骤开始"
	if len(steps) > 0 {
		flow = strings.Join(steps, " → ")
	}
	result := fmt.Sprintf(`## 意图理解
- 目标结果：完成用户要求的 %s 交付。
- 已有输入：%s
- 当前缺口：%s。

## 执行边界
- 当前不会搜索工作目录、猜测素材、虚构内容或提前生成交付物。
- 本次执行到此终止，不保留可由下一条普通消息自动续接的状态。

## 执行流程与交付标准
- 新请求获受理后的流程：%s
- 验收标准：交付物必须真实可用、路径可验证，不得用计划或占位内容冒充结果。

## 输入判定
%s

INPUT_STATUS: MISSING
<!--PIPELINE_STOP-->`, name, aiEmployeeIntakeInputSummary(current), missing, flow, request)
	return normalizeAIEmployeeWaitingInputOutput(result, pipeline), true
}

// ensureAIEmployeeExplicitConfirmationCoverage preserves an explicit
// user-authored confirmation list at an interaction boundary. Models may
// paraphrase or omit one field while compacting; the host appends the user's
// own clause verbatim instead of inventing domain-specific required fields.
func ensureAIEmployeeExplicitConfirmationCoverage(task, output string) string {
	clause := extractAIEmployeeExplicitConfirmationClause(task)
	cleaned := strings.TrimSpace(output)
	if clause != "" && !strings.Contains(cleaned, clause) {
		cleaned = strings.TrimSpace(cleaned + "\n\n另请一并确认：" + clause + "。")
	}

	var missingOptions []string
	for _, option := range extractAIEmployeeExplicitCircledOptions(task) {
		if aiEmployeeOutputCoversExplicitOption(cleaned, option) {
			continue
		}
		missingOptions = append(missingOptions, option)
	}
	if len(missingOptions) > 0 {
		cleaned = strings.TrimSpace(cleaned + "\n\n请同时保留以下用户指定选项：\n" +
			strings.Join(missingOptions, "\n"))
	}
	for _, label := range extractAIEmployeeExplicitLabelRequirements(task) {
		if !strings.Contains(cleaned, label) {
			cleaned = strings.TrimSpace(cleaned + "\n\n用户指定标签：**" + label + "**")
		}
	}
	if aiEmployeeTaskRequiresExplicitUserProvenance(task) &&
		!aiEmployeeOutputHasExplicitUserProvenance(cleaned) {
		cleaned = strings.TrimSpace(cleaned +
			"\n\n来源归属：上述由用户口头补充的内容，**暂记为你的业务方向陈述**，不是所给原文内容，且未提供独立来源。")
	}
	return cleaned
}

func aiEmployeeTaskRequiresExplicitUserProvenance(task string) bool {
	normalized := strings.ToLower(strings.TrimSpace(task))
	userOrigin := containsAnyText(normalized, []string{
		"用户口述", "用户陈述", "项目方陈述", "我的口头补充", "我口头补充",
		"我在正文中", "仅来自用户", "只来自用户",
	})
	sourceContrast := containsAnyText(normalized, []string{
		"不是原文", "非原文", "不来自原文", "不得归因", "不要归因",
		"仅来自用户", "只来自用户", "未提供来源", "没有来源",
	})
	return userOrigin && sourceContrast
}

func aiEmployeeOutputHasExplicitUserProvenance(output string) bool {
	normalized := strings.ToLower(strings.TrimSpace(output))
	return containsAnyText(normalized, []string{
		"用户口述", "用户陈述", "项目方陈述", "业务方向陈述", "用户本轮输入", "用户本轮口头输入",
	}) && containsAnyText(normalized, []string{
		"不是原文", "非原文", "不来自原文", "未在原文", "原文未",
		"不得归因", "不归因", "未提供独立来源", "没有独立来源",
	})
}

func extractAIEmployeeExplicitLabelRequirements(task string) []string {
	var labels []string
	for _, match := range aiEmployeeExplicitLabelRequirement.FindAllStringSubmatch(task, -1) {
		if len(match) < 2 {
			continue
		}
		label := strings.TrimSpace(match[1])
		duplicate := false
		for _, existing := range labels {
			if label == existing {
				duplicate = true
				break
			}
		}
		if label != "" && !duplicate {
			labels = append(labels, label)
		}
	}
	return labels
}

func extractAIEmployeeExplicitConfirmationClause(task string) string {
	task = strings.TrimSpace(task)
	if task == "" {
		return ""
	}
	start := -1
	startLength := 0
	for _, marker := range []string{"请先确认", "请首先确认", "先确认", "首先确认"} {
		if index := strings.Index(task, marker); index >= 0 &&
			(start < 0 || index < start) {
			start = index
			startLength = len(marker)
		}
	}
	if start < 0 {
		return ""
	}
	remainder := task[start+startLength:]
	end := -1
	for _, marker := range []string{"，并让我", "并让我", "，然后让我", "然后让我", "，再让我", "再让我"} {
		if index := strings.Index(remainder, marker); index >= 0 &&
			(end < 0 || index < end) {
			end = index
		}
	}
	if end < 0 {
		return ""
	}
	clause := strings.Trim(strings.TrimSpace(remainder[:end]), "，,。；;：:")
	if runeCount := len([]rune(clause)); runeCount < 2 || runeCount > 160 {
		return ""
	}
	return clause
}

func aiEmployeeIntakeInputSummary(input string) string {
	if strings.TrimSpace(input) == "" {
		return "当前请求为空。"
	}
	return truncateRunes(strings.TrimSpace(input), 600)
}

func aiEmployeeRequestHasReadableMaterial(input string, pastedTextMinRunes int) bool {
	text := strings.TrimSpace(input)
	if text == "" {
		return false
	}
	if pipelinecore.ContainsSourceLocator(text) {
		return true
	}
	// A sufficiently long pasted body is usable even when the user did not add
	// a filename. Short phrases such as “这份文件” are intentionally rejected.
	if utf8RuneCount(text) >= pastedTextMinRunes && strings.ContainsAny(text, "。；;\n") {
		return true
	}
	return false
}

var aiEmployeeSemanticallyEmptyWordsRe = regexp.MustCompile(`(?i)(帮我|帮忙|麻烦|请|给我|替我|想要|需要|做|处理|看|看看|看下|弄|搞|分析|检查|审查|写|生成|制作|整理|完成|执行|开始|继续|一下|这个|那个|这份|那份|它|吧|啦|呢|谢谢|please|help|do|it|this|that|start|continue|handle|process|make|create|check|review|analy[sz]e)`)

// aiEmployeeRequestIsSemanticallyEmpty detects requests that contain only a
// generic command and no task object, topic, material or business coordinate.
// The rule is intentionally domain-neutral so every employee gets the same
// boundary: do not invent a task merely because a role was explicitly selected.
func aiEmployeeRequestIsSemanticallyEmpty(input string) bool {
	text := strings.TrimSpace(input)
	if text == "" {
		return true
	}
	if pipelinecore.ContainsSourceLocator(text) {
		return false
	}
	remaining := aiEmployeeSemanticallyEmptyWordsRe.ReplaceAllString(text, "")
	remaining = strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) || unicode.IsPunct(r) || unicode.IsSymbol(r) {
			return -1
		}
		return r
	}, remaining)
	return utf8RuneCount(remaining) < 2
}

var aiEmployeeWechatGenericWordsRe = regexp.MustCompile(`(?i)(帮我|麻烦|请|给我|想要|需要|整理|撰写|写作|写|生成|制作|创作|输出|润色|排版|一份|一篇|一个|这份|这篇|关于|微信公众号|公众号|推文|文章|稿件|成稿|内容|一下|吧|谢谢|please|write|create|generate|wechat|article)`)

var aiEmployeePPTGenericWordsRe = regexp.MustCompile(`(?i)(帮我|麻烦|请|给我|想要|需要|根据|基于|用|把|生成|制作|创建|做|输出|润色|美化|优化|设计|一份|一个|这份|这个|关于|简单|介绍材料|材料|正文|内容|一下|吧|谢谢|pptx?|powerpoint|幻灯片|演示文稿|路演稿|deck|please|create|generate|make|polish|presentation)`)

func aiEmployeePPTRequestHasActionableInput(input string) bool {
	text := strings.TrimSpace(input)
	if text == "" {
		return false
	}
	if pipelinecore.ContainsSourceLocator(text) {
		return true
	}
	remaining := aiEmployeePPTGenericWordsRe.ReplaceAllString(text, "")
	remaining = strings.TrimFunc(remaining, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsPunct(r) || unicode.IsSymbol(r)
	})
	return utf8RuneCount(remaining) >= 2
}

func aiEmployeeWechatRequestHasTopicOrSource(input string) bool {
	text := strings.TrimSpace(input)
	if text == "" {
		return false
	}
	if pipelinecore.ContainsSourceLocator(text) {
		return true
	}
	remaining := aiEmployeeWechatGenericWordsRe.ReplaceAllString(text, "")
	remaining = strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) || unicode.IsPunct(r) || unicode.IsSymbol(r) {
			return -1
		}
		return r
	}, remaining)
	return utf8RuneCount(remaining) >= 2
}

func utf8RuneCount(value string) int {
	return len([]rune(value))
}

// buildAIEmployeeIntakeFailureFallback keeps a domain pipeline usable when the
// intake model emits only an unusable tool intent or otherwise fails to produce
// text. It deliberately does not guess whether domain-specific material is
// sufficient; the first business step retains its own stricter input gate.
func buildAIEmployeeIntakeFailureFallback(pipeline Pipeline, input string) string {
	current := strings.TrimSpace(stripAIEmployeeContextBlock(input))
	if current == "" {
		current = "（当前请求为空，由后续业务输入门禁判定）"
	}
	stepNames := make([]string, 0, len(pipeline.Steps))
	for _, step := range pipeline.Steps {
		if name := strings.TrimSpace(step.Name); name != "" && name != aiEmployeeIntakeStepName {
			stepNames = append(stepNames, name)
		}
	}
	flow := "由后续业务输入门禁继续判定"
	if len(stepNames) > 0 {
		flow = strings.Join(stepNames, " → ")
	}
	contextDecision := "未发现宿主候选上下文。"
	if path := extractAIEmployeeContextPath(input); path != "" {
		contextDecision = "存在宿主候选上下文 " + path + "；首个业务步骤必须先判断相关性，相关则读取复用，不相关则忽略。"
	}
	return fmt.Sprintf(`## 意图理解
- 目标结果：完成用户当前明确要求的业务交付。
- 已有输入：%s
- 上下文判定：%s
- 受理降级说明：意图模型未形成可用文本，宿主已安全接续；不得因此把任务标记为失败或擅自补造材料。

## 执行边界
- 能做什么：只按 Runtime Pipeline 已声明的业务步骤和工具权限继续。
- 不能做什么：不得编造缺失文件、链接、正文、数据或外部发布权限。

## 执行流程与交付标准
- 执行流程：%s
- 输入是否足够、是否需要用户补充，由首个业务步骤的领域门禁严格判定。

## 输入判定
INTAKE_STATUS: READY`, truncateRunes(current, 1200), contextDecision, flow)
}

func buildAEPipelineDeliveryStandards(steps []PipelineStep) string {
	standards := make([]string, 0, len(steps))
	for _, step := range steps {
		name := strings.TrimSpace(step.Name)
		if name == "" {
			name = "未命名步骤"
		}
		if step.SourceContract != nil {
			standards = append(standards, fmt.Sprintf("- %s：正式素材必须可追溯并冻结；缺失时不得编造。", name))
		}
		if contract := step.ArtifactContract; contract != nil && len(contract.Paths) > 0 {
			line := fmt.Sprintf("- %s：必须生成 %s", name, strings.Join(contract.Paths, "、"))
			if contract.MinBytes > 0 {
				line += fmt.Sprintf("，每项至少 %d 字节", contract.MinBytes)
			}
			if contract.RejectPlaceholders {
				line += "，不得保留占位符"
			}
			standards = append(standards, line+"。")
		}
		if contract := step.OutputContract; contract != nil {
			parts := make([]string, 0, 2)
			if contract.MinChars > 0 {
				parts = append(parts, fmt.Sprintf("正文至少 %d 字符", contract.MinChars))
			}
			if len(contract.RequiredSections) > 0 {
				parts = append(parts, "必须包含 "+strings.Join(contract.RequiredSections, "、"))
			}
			if len(parts) > 0 {
				standards = append(standards, fmt.Sprintf("- %s：%s。", name, strings.Join(parts, "；")))
			}
		}
	}
	if len(standards) == 0 {
		return "- 完成用户要求的实际结果；不得只给计划、不得伪造路径或把过程说明冒充交付。"
	}
	return strings.Join(standards, "\n")
}
