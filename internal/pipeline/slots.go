package pipeline

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"mac/internal/textutil"
)

const (
	SlotPending = "pending"
	SlotFilling = "filling"
	SlotFilled  = "filled"
	SlotMissing = "missing"
	SlotInvalid = "invalid"

	SlotFilledMarker  = "SLOT_STATUS: FILLED"
	SlotMissingMarker = "SLOT_STATUS: MISSING"
)

// PipelineSlotContract turns an open-ended prompt into a fillable delivery
// unit. The host can derive one for legacy steps while built-in pipelines can
// add stricter domain fields and validation rules.
type PipelineSlotContract struct {
	ID              string   `json:"id" yaml:"id"`
	TaskDescription string   `json:"task_description" yaml:"task_description"`
	DependsOn       []string `json:"depends_on,omitempty" yaml:"depends_on,omitempty"`
	InputSources    []string `json:"input_sources,omitempty" yaml:"input_sources,omitempty"`
	RequiredFields  []string `json:"required_fields" yaml:"required_fields"`
	FillRules       []string `json:"fill_rules,omitempty" yaml:"fill_rules,omitempty"`
	ValidationRules []string `json:"validation_rules,omitempty" yaml:"validation_rules,omitempty"`
	OnMissing       string   `json:"on_missing,omitempty" yaml:"on_missing,omitempty"`
}

type PipelineSlotValidationCheck struct {
	Name   string `json:"name"`
	Passed bool   `json:"passed"`
	Detail string `json:"detail,omitempty"`
}

// PipelineSlotFillRecord is the auditable receipt for one filled slot. The
// full value remains owned by the host's step result.
type PipelineSlotFillRecord struct {
	SlotID         string                        `json:"slot_id"`
	Status         string                        `json:"status"`
	OutputSHA256   string                        `json:"output_sha256,omitempty"`
	OutputChars    int                           `json:"output_chars,omitempty"`
	ArtifactPaths  []string                      `json:"artifact_paths,omitempty"`
	SourceSnapshot string                        `json:"source_snapshot,omitempty"`
	Checks         []PipelineSlotValidationCheck `json:"checks,omitempty"`
}

// SlotStepContractInput is the application-independent portion of a pipeline
// step needed to derive its slot contract.
type SlotStepContractInput struct {
	Name                       string
	Prompt                     string
	HasSourceContract          bool
	OutputMinChars             int
	OutputRequiredSections     []string
	ArtifactPaths              []string
	RejectArtifactPlaceholders bool
}

// CloneSlotContract prevents runtime derivation from mutating a configured
// contract shared by other executions.
func CloneSlotContract(source *PipelineSlotContract) *PipelineSlotContract {
	if source == nil {
		return nil
	}
	clone := *source
	clone.DependsOn = append([]string(nil), source.DependsOn...)
	clone.InputSources = append([]string(nil), source.InputSources...)
	clone.RequiredFields = append([]string(nil), source.RequiredFields...)
	clone.FillRules = append([]string(nil), source.FillRules...)
	clone.ValidationRules = append([]string(nil), source.ValidationRules...)
	return &clone
}

// DeriveSlotContract builds a deterministic contract without depending on
// application execution types.
func DeriveSlotContract(pipelineName string, step SlotStepContractInput, index int, previousSlot, intakeStepName string) *PipelineSlotContract {
	id := fmt.Sprintf("step_%02d", index)
	if strings.TrimSpace(step.Name) == strings.TrimSpace(intakeStepName) {
		id = "task_brief"
	}
	contract := &PipelineSlotContract{
		ID:              id,
		TaskDescription: slotTaskDescription(step.Name, step.Prompt),
		InputSources:    []string{"current_user_request", "approved_same_session_context"},
		RequiredFields:  []string{"本步骤的实际结果"},
		FillRules: []string{
			"只填充当前槽位，不提前执行后续槽位",
			"只能使用本步骤声明的工具和已经通过校验的上游槽位",
			"事实、文件和来源不得编造",
		},
		ValidationRules: []string{"输出不得为空", "不得用计划、过程话术或占位符冒充结果"},
		OnMissing:       "只列出会实质改变交付且无法自主取得的决定性缺口，然后等待用户补充。",
	}
	if previousSlot != "" {
		contract.DependsOn = []string{previousSlot}
		contract.InputSources = append(contract.InputSources, "validated_upstream_slots")
	}
	if strings.TrimSpace(step.Name) == strings.TrimSpace(intakeStepName) {
		contract.RequiredFields = []string{"目标结果", "最终交付物", "已有输入", "历史上下文取舍", "任务身份坐标", "硬约束", "优先条件", "自动放宽顺序", "执行边界", "后续槽位顺序", "验收标准", "输入是否足够"}
		contract.ValidationRules = []string{"必须理解完整 Pipeline 后再判定 READY/MISSING", "必须区分不可替换目标、硬约束和可放宽偏好", "不得把地区、行业、企业规模、时间或数量同时列为不可替换身份和可放宽条件", "不得承诺 Pipeline 权限或产物契约不支持的文件交付", "仅缺决定性输入时追问", "不得在受理槽位执行业务或调用工具"}
		contract.OnMissing = "输出需要用户补充的最少信息，并停止在 task_brief；补充后重新受理。"
	}
	appendSlotContractRequirements(contract, step)
	applyDomainSlotContract(strings.ToLower(strings.TrimSpace(pipelineName)), strings.TrimSpace(step.Name), contract)
	return contract
}

func slotTaskDescription(name, prompt string) string {
	name = strings.TrimSpace(name)
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return "完成“" + name + "”并形成可供下游使用的实际结果。"
	}
	if split := strings.Index(prompt, "\n\n"); split > 0 {
		prompt = prompt[:split]
	}
	return textutil.TruncateRunes(strings.TrimSpace(prompt), 320)
}

func appendSlotContractRequirements(contract *PipelineSlotContract, step SlotStepContractInput) {
	if contract == nil {
		return
	}
	if step.HasSourceContract {
		contract.RequiredFields = append(contract.RequiredFields, "可追溯的正式素材或明确的原创主题模式")
		contract.ValidationRules = append(contract.ValidationRules, "引用素材时必须通过 Source Contract 冻结并保留来源定位")
	}
	if step.OutputMinChars > 0 {
		contract.ValidationRules = append(contract.ValidationRules, fmt.Sprintf("有效正文不少于 %d 字符", step.OutputMinChars))
	}
	if len(step.OutputRequiredSections) > 0 {
		contract.RequiredFields = append(contract.RequiredFields, step.OutputRequiredSections...)
	}
	if len(step.ArtifactPaths) > 0 {
		contract.RequiredFields = append(contract.RequiredFields, "真实交付文件："+strings.Join(step.ArtifactPaths, "、"))
		contract.ValidationRules = append(contract.ValidationRules, "所有声明文件必须真实存在并通过 Artifact Contract")
		if step.RejectArtifactPlaceholders {
			contract.ValidationRules = append(contract.ValidationRules, "交付物不得包含占位符")
		}
	}
}

func applyDomainSlotContract(pipelineName, stepName string, contract *PipelineSlotContract) {
	if contract == nil || contract.ID == "task_brief" {
		return
	}
	switch pipelineName {
	case "lead-research":
		switch stepName {
		case "产品画像与SWOT":
			contract.RequiredFields = []string{"产品维度表", "证据账本", "SWOT（优势/劣势/机会/威胁）", "竞品与替代方案", "正向ICP", "负面ICP", "实体分类规则", "待验证问题", "PRODUCT_PROFILE_STATUS: READY"}
			contract.ValidationRules = append(contract.ValidationRules, "旧助手回答和旧潜客报告只能作为待验证假设", "产品身份和核心定位足以推导ICP与关键词时可进入搜索；非决定性未知必须标注置信度和验证方式")
		case "搜索策略":
			contract.RequiredFields = []string{"搜索假设", "关键词矩阵", "不少于12条精确检索式", "负面关键词", "多源渠道计划", "接纳与淘汰规则", "证据有效性标准", "SEARCH_PLAN_STATUS: READY"}
			contract.ValidationRules = append(contract.ValidationRules, "不得执行搜索", "关键词必须由已确认事实、明确标注的分析假设和ICP推导")
		case "多源搜索与核验":
			contract.RequiredFields = []string{"检索执行记录", "实体分类表", "已核验潜客表", "排除对象表", "证据有效性审计", "RESEARCH_STATUS"}
			contract.ValidationRules = append(contract.ValidationRules, "只有购买方进入潜客表", "高置信潜客需要两个独立来源类别或一个第一方强购买信号", "不得用未核验候选凑数量")
		case "报告整理":
			contract.RequiredFields = []string{"产品结论与SWOT", "检索关键词与渠道", "已核验潜客优先级表", "排除对象表", "证据有效性审计", "下一轮检索计划"}
			contract.ValidationRules = append(contract.ValidationRules, "不得继承未被本轮证据重新验证的旧评级、定价或候选", "竞品、供应商和合作伙伴不得进入潜客优先级表")
		}
	case "wechat-article":
		switch stepName {
		case "素材准备":
			contract.RequiredFields = []string{"素材模式（现有素材/原创主题）", "素材或主题定位", "目标读者", "发布目的", "事实与观点边界", "版权风险", "标题角度", "摘要方向", "正文大纲"}
			contract.ValidationRules = append(contract.ValidationRules, "无主题且无素材时必须停下追问，禁止自行随机选题")
		case "公众号交付物":
			contract.RequiredFields = []string{"推荐标题", "40-120字摘要", "完整可发布Markdown正文", "微信兼容HTML", "封面图提示词", "配图建议", "两份文件路径与SHA-256回执", "发布前自查"}
		}
	case "ppt-polish":
		switch stepName {
		case "素材读取":
			contract.RequiredFields = []string{"源素材真实路径", "主题", "目标受众", "页数或内容结构", "结构问题", "版式问题", "信息密度问题", "视觉一致性问题", "润色方向"}
		case "内容重构":
			contract.RequiredFields = []string{"逐页标题", "每页唯一核心观点", "每页内容层级", "图表或配图建议", "演讲备注", "保留的原始数据与引用", "真实Markdown大纲路径"}
		case "视觉润色":
			contract.RequiredFields = []string{"真实PPTX路径", "生成路线", "可编辑性", "页面数", "渲染检查结果", "针对性修正记录"}
			contract.ValidationRules = append(contract.ValidationRules, "正文和标签应避免堆积大量≤12pt小字；命中时必须记录为可读性改进项，但不得因此丢弃已生成的有效PPTX", "PPTX必须大于10KB且至少4页")
		case "交付自查":
			contract.RequiredFields = []string{"PPTX真实路径", "页数", "源素材路径", "润色变化摘要", "最小字号与密度审计结果", "仍需人工确认内容"}
		}
	case "bid-proposal":
		switch stepName {
		case "招标解读":
			contract.RequiredFields = []string{"项目名称", "采购方", "截止时间", "资格条件", "评分办法", "技术要求", "商务要求", "提交材料", "废标风险", "缺失信息", "材料来源"}
			contract.ValidationRules = append(contract.ValidationRules, "没有可读取招标材料时必须追问，不得生成通用模板")
		case "响应矩阵":
			contract.RequiredFields = []string{"逐条招标要求", "响应策略", "我方证据", "负责人或待补充", "优先级", "风险等级", "真实矩阵文件路径"}
		case "标书初稿":
			contract.RequiredFields = []string{"项目理解", "总体方案", "技术路线", "实施计划", "团队与资质", "服务保障", "商务响应", "差异化优势", "交付物清单", "真实初稿路径"}
		case "风险自查":
			contract.RequiredFields = []string{"强制条款遗漏", "资质缺口", "时间节点", "报价商务风险", "技术偏离", "证明材料缺口", "人工确认项", "全部交付路径"}
		}
	}
}

// AppendSlotInstruction adds the stable fill/missing protocol to a host prompt.
func AppendSlotInstruction(prompt string, slot *PipelineSlotContract, waitingInputMarker, stopMarker string) string {
	if slot == nil {
		return prompt
	}
	return prompt + fmt.Sprintf(`

---

## 当前 Pipeline 填充槽位
- slot_id：%s
- 槽位任务：%s
- 依赖槽位：%s
- 允许输入来源：%s
- 必填内容：%s
- 填充规范：%s
- 校验规则：%s
- 缺参动作：%s

你现在只填这个槽位。上游槽位已经由宿主按顺序校验；不要重做上游，也不要提前生成下游交付。
完成时另起一行输出“%s”。如果缺少决定性输入，简洁说明最少需要补什么，另起一行输出“%s”，再输出“%s”，最后一行输出“%s”。`,
		slot.ID,
		slot.TaskDescription,
		slotList(slot.DependsOn),
		slotList(slot.InputSources),
		slotList(slot.RequiredFields),
		slotList(slot.FillRules),
		slotList(slot.ValidationRules),
		slot.OnMissing,
		SlotFilledMarker,
		SlotMissingMarker,
		waitingInputMarker,
		stopMarker,
	)
}

func slotList(values []string) string {
	seen := make(map[string]bool)
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		result = append(result, value)
	}
	sort.Strings(result)
	if len(result) == 0 {
		return "无"
	}
	return strings.Join(result, "；")
}

type SlotArtifact struct {
	Path      string
	Validated bool
}

type SlotToolCall struct {
	Failed    bool
	Params    string
	Result    string
	Artifacts []SlotArtifact
}

// SlotArtifactPaths extracts only paths backed by a successful tool call or a
// host-validated artifact record. Plain model prose is intentionally ignored.
func SlotArtifactPaths(calls []SlotToolCall) []string {
	seen := make(map[string]bool)
	var paths []string
	add := func(candidate string) {
		candidate = strings.TrimSpace(strings.ReplaceAll(candidate, "\\", "/"))
		if index := strings.Index(strings.ToLower(candidate), ".openkitty/storage/"); index >= 0 {
			candidate = candidate[index:]
		} else if strings.HasPrefix(strings.ToLower(candidate), "storage/") {
			candidate = ".openkitty/" + candidate
		} else {
			return
		}
		candidate = strings.TrimRight(candidate, ".,;:!?，。；：！？`\"'")
		if candidate == "" || seen[candidate] {
			return
		}
		seen[candidate] = true
		paths = append(paths, candidate)
	}
	for _, call := range calls {
		if call.Failed {
			continue
		}
		for _, artifact := range call.Artifacts {
			if artifact.Validated {
				add(artifact.Path)
			}
		}
		collectSlotJSONPaths(call.Params, add)
		collectSlotJSONPaths(call.Result, add)
	}
	return paths
}

func collectSlotJSONPaths(raw string, add func(string)) {
	if strings.TrimSpace(raw) == "" || add == nil {
		return
	}
	var value interface{}
	if json.Unmarshal([]byte(raw), &value) != nil {
		return
	}
	var walk func(interface{})
	walk = func(current interface{}) {
		switch typed := current.(type) {
		case map[string]interface{}:
			for key, child := range typed {
				if text, ok := child.(string); ok && strings.Contains(strings.ToLower(key), "path") {
					add(text)
				}
				walk(child)
			}
		case []interface{}:
			for _, child := range typed {
				walk(child)
			}
		}
	}
	walk(value)
}

type SlotFillInput struct {
	Contract               *PipelineSlotContract
	Status                 string
	CleanOutput            string
	ProgressOnly           bool
	ArtifactPaths          []string
	SourceSnapshot         string
	HasSourceContract      bool
	SourceContractRequired bool
	SourceWhenReferenced   bool
	HasArtifactContract    bool
	ArtifactContractPaths  []string
	HasOutputContract      bool
	ResultAccepted         bool
}

// BuildSlotFillRecord creates the stable receipt after the host has cleaned
// control markers and evaluated its application-specific result state.
func BuildSlotFillRecord(input SlotFillInput) *PipelineSlotFillRecord {
	if input.Contract == nil {
		return nil
	}
	clean := strings.TrimSpace(input.CleanOutput)
	hash := ""
	if clean != "" {
		hash = fmt.Sprintf("%x", sha256.Sum256([]byte(clean)))
	}
	record := &PipelineSlotFillRecord{
		SlotID:         input.Contract.ID,
		Status:         input.Status,
		OutputSHA256:   hash,
		OutputChars:    utf8.RuneCountInString(clean),
		ArtifactPaths:  append([]string(nil), input.ArtifactPaths...),
		SourceSnapshot: input.SourceSnapshot,
		Checks: []PipelineSlotValidationCheck{
			{Name: "non_empty_output", Passed: clean != "", Detail: "槽位输出必须非空"},
			{Name: "substantive_result", Passed: clean != "" && !input.ProgressOnly, Detail: "不得仅输出计划或过程话术"},
		},
	}
	if input.HasSourceContract {
		passed := input.SourceSnapshot != "" || !input.SourceContractRequired || input.SourceWhenReferenced
		record.Checks = append(record.Checks, PipelineSlotValidationCheck{Name: "source_contract", Passed: passed, Detail: input.SourceSnapshot})
	}
	if input.HasArtifactContract {
		record.Checks = append(record.Checks, PipelineSlotValidationCheck{Name: "artifact_contract", Passed: input.ResultAccepted, Detail: strings.Join(input.ArtifactContractPaths, "、")})
	}
	if input.HasOutputContract {
		record.Checks = append(record.Checks, PipelineSlotValidationCheck{Name: "output_contract", Passed: input.ResultAccepted})
	}
	return record
}
