package app

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"mac/internal/toolregistry"
)

// 借鉴WorkBuddy Expert Teams: 按模板动态组队，多个role-specific agent并行协同

// Default team templates (used when config doesn't specify)
var defaultTeamTemplates = map[string][]TeamRole{
	"research": {
		{Role: "研究员", SystemPrompt: "你是信息搜集专家。用web_fetch搜索相关资料，提取关键事实、数据和引用。回答要客观准确，标注信息来源。"},
		{Role: "分析师", SystemPrompt: "你是数据分析师。对搜集到的信息进行深度分析，找出模式、趋势、矛盾点，给出洞察。"},
		{Role: "撰稿人", SystemPrompt: "你是专业撰稿人。将分析结果整理成结构清晰、语言流畅的报告。使用markdown格式。"},
	},
	"coding": {
		{Role: "架构师", SystemPrompt: "你是系统架构师。分析需求，设计整体方案，确定技术选型、模块划分、接口定义。输出架构设计文档。"},
		{Role: "程序员", SystemPrompt: "你是全栈开发工程师。根据架构设计编写代码，处理边界情况，编写测试。代码要清晰。"},
		{Role: "审查员", SystemPrompt: "你是代码审查专家。检查代码质量、安全性、性能问题，运行测试验证，给出改进建议。"},
	},
	"fullstack": {
		{Role: "PM", SystemPrompt: "你是产品经理。理解用户需求，拆解为具体任务，定义验收标准。协调团队工作。"},
		{Role: "后端", SystemPrompt: "你是后端开发专家。设计API、数据库schema、业务逻辑。关注性能、安全、可扩展性。"},
		{Role: "前端", SystemPrompt: "你是前端开发专家。负责UI实现、交互体验、响应式设计。关注可访问性和性能。"},
		{Role: "测试", SystemPrompt: "你是测试工程师。编写测试用例，执行自动化测试，发现bug和边界问题。"},
	},
	"content": {
		{Role: "策划", SystemPrompt: "你是内容策划。根据主题和目标受众，规划内容结构、角度、亮点。"},
		{Role: "写手", SystemPrompt: "你是专业写手。根据策划方案撰写内容，语言生动、逻辑清晰、有感染力。"},
		{Role: "编辑", SystemPrompt: "你是资深编辑。审核内容质量，修正语法错误，优化表达，确保风格统一。"},
	},
	"ppt": {
		{Role: "艺术总监", SystemPrompt: "你是 PPT 艺术总监。先判断 audience、communication job、narrative arc、editability、visual ambition、generator route 与 QA 边界。用 2-4 行给出路线后立即推进，不把任务固定到通用模板。", Skill: "ppt-art-director"},
		{Role: "内容策划", SystemPrompt: "你是演示内容策划。从冻结材料中提炼核心信息，让每页承担一个叙事任务。目录、章节页、KPI、总结页只在内容需要时使用，不按固定配额凑页，不编造数字、案例或占位内容。", Skill: "ppt-art-director"},
		{Role: "设计排版", SystemPrompt: "你是幻灯片设计师。严格按三级生产：Level 3 高端定制走完整 PPTMaster；Level 2 常规正式演示默认从内置脚手架创建任务专用 python-pptx/PptxGenJS 逐页脚本；Level 1 base-ppt 只用于明确快速、内部、周期性、固定结构或公开失败降级。禁止空 generator 直接 convert 成 PPTX，禁止用一次性内联大脚本冒充常规路线。不得因可编辑要求而强塞固定 JSON schema。", Skill: "ppt-art-director"},
		{Role: "视觉审核", SystemPrompt: "你是视觉质量审查员。实际渲染全套页面，检查空白/错误/占位页、缺失图解、事实越界、重叠、溢出、对齐、对比度与重复巨卡片；只要求一次有针对性的修正和复查，避免无意义循环。", Skill: "ppt-art-director"},
	},
	"codebase": {
		{Role: "项目扫描", SystemPrompt: "你是项目结构分析师。扫描代码库目录结构，识别技术栈、构建系统、入口点、模块划分。输出结构化的项目概览。", Skill: "understand-codebase"},
		{Role: "文件分析", SystemPrompt: "你是代码分析专家。对核心文件进行符号提取、依赖分析、模块接口梳理。5并发并行分析，每批≤30文件。聚焦模块对外暴露的接口。", Skill: "understand-codebase"},
		{Role: "架构审核", SystemPrompt: "你是系统架构审核员。综合项目扫描和文件分析结果，识别架构分层、设计模式、数据流、循环依赖。输出分层架构图和架构评估报告。", Skill: "understand-codebase"},
	},
	"contract": {
		{Role: "结构扫描", SystemPrompt: "你是合同结构分析师。识别合同类型、主体、管辖法律、条款结构。输出合同的结构化概览，标注关键定义条款位置。", Skill: "understand-contracts"},
		{Role: "条款分析", SystemPrompt: "你是合同条款分析专家。逐条分析风险点、谈判空间，以红黄绿三级标注风险等级。识别无限责任、单方解约权、模糊表述等风险信号。", Skill: "understand-contracts"},
		{Role: "合规审核", SystemPrompt: "你是合规审核员。综合条款分析构建整体风险矩阵，生成权利义务清单和合规检查日历。检查条款内部一致性和逻辑矛盾。", Skill: "understand-contracts"},
	},
	"competitor": {
		{Role: "格局扫描", SystemPrompt: "你是市场竞争格局分析师。识别市场中的直接竞对、间接竞对、潜在进入者、替代品。输出竞对分类清单和市场份额估算。", Skill: "understand-competitors"},
		{Role: "竞对分析", SystemPrompt: "你是竞对深度分析专家。对每个主要竞对进行五维分析(产品/技术/市场/组织/财务)，输出优势劣势对比和我们的相对定位。", Skill: "understand-competitors"},
		{Role: "策略输出", SystemPrompt: "你是竞争策略顾问。综合所有竞对分析，构建战略定位图、识别攻防策略、生成竞对比对卡(Battle Card)和战略建议报告。", Skill: "understand-competitors"},
	},
}

// hybridTemplates define templates that mix sequential and parallel stages.
// Each stage is a group of roles; a stage with >1 role runs in parallel within that stage.
var hybridTemplateStages = map[string][][]int{
	"fullstack":  {{0}, {1, 2}, {3}},    // PM → [后端+前端]并行 → 测试
	"competitor": {{0}, {1, 1, 1}, {2}}, // 格局扫描 → 3个竞对分析并行 → 策略输出
}

const teamRoleDeliveryBoundary = `## 交付边界（必须遵守）
- 将本角色的实质产出直接返回给团队编排器，供其他角色和合成员继续使用。
- 只有原始任务明确要求创建或修改文件、代码或其他工作区交付物时，才调用写入/编辑工具。
- 原始任务没有要求落盘时，不要自行发明文件名或目录；不要因为任务包含“研究、分析、报告、撰稿”等词就创建中间文件或最终文件。
- 原始任务明确要求落盘时，严格使用用户指定或任务约定的目标路径，并在工具调用中一次性提供完整路径参数。
- 完成职责后直接返回结果，不要为了留痕、角色交接或填充输出格式而创建文件。`

// AssembleTeamTool 动态组队工具
type AssembleTeamTool struct {
	workspace    string
	fileLogger   *log.Logger
	db           interface{} // *gorm.DB, used by sub-agents
	toolRegistry *toolregistry.Registry
	configMu     sync.RWMutex
	config       *Config // for accessing team_templates
}

func NewAssembleTeamTool(workspace string, fileLogger *log.Logger, db interface{}, registry *toolregistry.Registry, cfg *Config) *AssembleTeamTool {
	return &AssembleTeamTool{
		workspace:    workspace,
		fileLogger:   fileLogger,
		db:           db,
		toolRegistry: registry,
		config:       cfg,
	}
}

func (t *AssembleTeamTool) Name() string       { return "assemble_team" }
func (t *AssembleTeamTool) MaxResultSize() int { return 30000 }

// Keep cached tools bound to their own engine configuration after a hot reload.
func (t *AssembleTeamTool) updateConfig(cfg *Config) {
	t.configMu.Lock()
	defer t.configMu.Unlock()
	t.config = cfg
}

func (t *AssembleTeamTool) configuredRoles(template string) []TeamRole {
	t.configMu.RLock()
	defer t.configMu.RUnlock()
	if t.config == nil {
		return nil
	}
	return append([]TeamRole(nil), t.config.TeamTemplates[template]...)
}

func (t *AssembleTeamTool) templateNames() []string {
	names := []string{"auto", "research", "coding", "fullstack", "content", "ppt", "codebase", "contract", "competitor", "academic"}
	known := make(map[string]bool, len(names))
	for _, name := range names {
		known[name] = true
	}
	t.configMu.RLock()
	var custom []string
	if t.config != nil {
		for name, roles := range t.config.TeamTemplates {
			if len(roles) > 0 && !known[name] {
				custom = append(custom, name)
			}
		}
	}
	t.configMu.RUnlock()
	sort.Strings(custom)
	return append(names, custom...)
}

func teamTemplateMode(template string) string {
	if _, hybrid := hybridTemplateStages[template]; hybrid {
		return "hybrid"
	}
	switch template {
	case "coding", "ppt", "codebase", "contract", "academic":
		return "sequential"
	default:
		return "parallel"
	}
}

func (t *AssembleTeamTool) Description() string {
	return `Assemble a dynamic team of role-specific agents. Use "auto" to auto-match the best template based on your task description. Templates: research, coding, fullstack, content, ppt, codebase, contract, competitor. Each agent gets a role-specific prompt and works independently.`
}

func (t *AssembleTeamTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"template": map[string]interface{}{
				"type":        "string",
				"enum":        t.templateNames(),
				"description": "Team template: research(研究员+分析师+撰稿人), coding(架构师+程序员+审查员), fullstack(PM→[后端+前端]→测试), content(策划+写手+编辑), ppt(PPT工坊), codebase(代码审计), contract(合同审查), competitor(竞对情报)",
			},
			"task": map[string]interface{}{
				"type":        "string",
				"description": "The task description. Be clear about what you want the team to produce.",
			},
			"custom_roles": map[string]interface{}{
				"type":        "array",
				"description": "Optional: custom role definitions. Each item: {role: string, system_prompt: string, skill: string (optional skill name like 'ppt-art-director' to auto-inject skill instructions)}",
				"items": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"role":          map[string]interface{}{"type": "string"},
						"system_prompt": map[string]interface{}{"type": "string"},
						"skill":         map[string]interface{}{"type": "string"},
					},
				},
			},
		},
		"required": []string{"task"},
	}
}

// autoMatch 根据任务关键词自动匹配最佳团队模板
func (t *AssembleTeamTool) autoMatch(task string) (template string, reason string) {
	lower := strings.ToLower(task)

	// 关键词 → 模板映射（优先级从高到低）
	type matcher struct {
		keywords []string
		template string
		reason   string
	}
	matchers := []matcher{
		{[]string{"ppt", "幻灯片", "演示", "presentation", "keynote"}, "ppt", "PPT/演示任务"},
		{[]string{"合同", "contract", "条款", "协议", "法律"}, "contract", "合同/法律审查"},
		{[]string{"竞对", "竞品", "competitor", "对手", "竞争分析"}, "competitor", "竞对/竞争分析"},
		{[]string{"代码", "code", "编程", "开发", "重构", "bug", "fix", "程序员", "架构", "api", "接口"}, "coding", "编程/开发任务"},
		{[]string{"代码库", "codebase", "项目结构", "架构审核", "依赖分析"}, "codebase", "代码库分析"},
		{[]string{"前端", "后端", "全栈", "fullstack", "前后端", "产品经理", "测试"}, "fullstack", "全栈开发"},
		{[]string{"写", "文章", "blog", "博客", "报告", "report", "内容", "文案", "策划", "编辑"}, "content", "内容创作"},
		{[]string{"研究", "调研", "research", "分析", "数据", "市场", "趋势", "行业"}, "research", "研究/分析任务"},
	}

	for _, m := range matchers {
		for _, kw := range m.keywords {
			if strings.Contains(lower, kw) {
				return m.template, m.reason
			}
		}
	}
	return "", "无明确特征，回退默认"
}

func (t *AssembleTeamTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	template, _ := params["template"].(string)
	task, _ := params["task"].(string)

	if task == "" {
		return "", fmt.Errorf("task is required")
	}
	// Team orchestration depends on runtime-bound sub-agent tools. Agents added
	// by a hot config reload used to receive assemble_team during construction
	// but miss those later-bound dependencies, leaving a visible outer tool that
	// could never execute. Repair that lifecycle gap once at the capability
	// boundary so every caller benefits, including older cached agents.
	if err := t.ensureRuntimeTeamTools(); err != nil {
		return "", err
	}

	// auto: 根据任务描述自动匹配最佳模板和 Agent 组合
	if template == "" || template == "auto" {
		matched, reason := t.autoMatch(task)
		if matched != "" {
			template = matched
			t.logf("[TEAM] 自动匹配模板: %s (原因: %s)", template, reason)
		} else {
			template = "research" // 兜底
		}
	}

	// Get roles: custom > config > default
	var roles []TeamRole
	if customRoles, ok := params["custom_roles"].([]interface{}); ok && len(customRoles) > 0 {
		for _, cr := range customRoles {
			if m, ok := cr.(map[string]interface{}); ok {
				role, _ := m["role"].(string)
				prompt, _ := m["system_prompt"].(string)
				skill, _ := m["skill"].(string)
				if role != "" && prompt != "" {
					roles = append(roles, TeamRole{Role: role, SystemPrompt: prompt, Skill: skill})
				}
			}
		}
	}
	if len(roles) == 0 {
		roles = t.configuredRoles(template)
	}
	if len(roles) == 0 {
		roles = defaultTeamTemplates[template]
	}
	if len(roles) == 0 {
		return "", fmt.Errorf("unknown template: %s. Use: research, coding, fullstack, content, ppt, codebase, contract, competitor", template)
	}

	// Check for hybrid template stages
	if stages, ok := hybridTemplateStages[template]; ok {
		return t.executeHybrid(ctx, template, task, roles, stages)
	}

	// Sequential templates: each role builds on the previous one's output
	isSequential := teamTemplateMode(template) == "sequential"

	mode := "parallel"
	if isSequential {
		mode = "sequential"
	}
	t.logf("[TEAM] 组装%s团队(%d人) mode=%s: %s", template, len(roles), mode, task)

	if isSequential {
		return t.executeSequential(ctx, template, task, roles)
	}
	return t.executeParallel(ctx, template, task, roles)
}

// ========== Hybrid Execution (sequential stages with parallel subgroups) ==========

func (t *AssembleTeamTool) executeHybrid(ctx context.Context, template, task string, roles []TeamRole, stages [][]int) (string, error) {
	if err := validateTeamStages(roles, stages); err != nil {
		return "", err
	}
	subTool, err := t.requireTeamTool("sub_agent")
	if err != nil {
		return "", err
	}
	parallelTool := t.optionalTeamTool("parallel_sub_agent")

	var output strings.Builder
	output.WriteString(fmt.Sprintf("🤝 **%s 团队** (%d人 · 混合协作) — %s\n\n", template, len(roles), task))

	// Collect all role outputs for synthesis
	allRoleOutputs := make([]string, len(roles))
	allRoleNames := make([]string, len(roles))
	for i, r := range roles {
		allRoleNames[i] = r.Role
	}

	for stageIdx, indices := range stages {
		if len(indices) == 1 {
			// Single role stage — sequential within the pipeline
			i := indices[0]
			r := roles[i]
			roleTask := t.buildRoleTask(r, task)
			roleTask.WriteString(fmt.Sprintf("\n\n## 阶段 %d/%d\n这是混合协作的第 %d 阶段。", stageIdx+1, len(stages), stageIdx+1))

			// Feed all previous stage outputs
			var prevOutputs []string
			for _, prevStage := range stages[:stageIdx] {
				for _, pi := range prevStage {
					if allRoleOutputs[pi] != "" && !strings.HasPrefix(allRoleOutputs[pi], "(执行失败") {
						prevOutputs = append(prevOutputs, fmt.Sprintf("### %s 的产出\n%s", roles[pi].Role, allRoleOutputs[pi]))
					}
				}
			}
			if len(prevOutputs) > 0 {
				roleTask.WriteString("\n\n## 上游阶段的完整产出\n")
				for _, po := range prevOutputs {
					roleTask.WriteString(fmt.Sprintf("\n<details>\n<summary>展开查看</summary>\n\n%s\n\n</details>\n", po))
				}
				roleTask.WriteString("\n**重要指引**: 基于上游产出继续推进。不要重复已完成的工作，而是填补遗漏、深化或执行你的专属职责。")
			}

			t.logf("[TEAM] 混合阶段[%d/%d] %s 开始", stageIdx+1, len(stages), r.Role)
			result, err := subTool.Execute(ctx, map[string]interface{}{
				"agent_name":     r.Role,
				"task":           roleTask.String(),
				"max_iterations": float64(20),
			})
			if err != nil {
				t.logf("[TEAM] 混合阶段[%d/%d] %s 失败: %v", stageIdx+1, len(stages), r.Role, err)
				allRoleOutputs[i] = fmt.Sprintf("(执行失败: %v)", err)
				output.WriteString(fmt.Sprintf("### %s ❌\n%v\n\n---\n\n", r.Role, err))
			} else {
				allRoleOutputs[i] = result
				output.WriteString(fmt.Sprintf("### %s ✅\n%s\n\n---\n\n", r.Role, result))
				t.logf("[TEAM] 混合阶段[%d/%d] %s 完成 (%d chars)", stageIdx+1, len(stages), r.Role, len(result))
			}
		} else {
			// Parallel group stage
			if parallelTool == nil {
				// Fallback: run sequentially within the group
				t.logf("[TEAM] 混合阶段[%d/%d] parallel_sub_agent不可用，降级串行", stageIdx+1, len(stages))
				for _, i := range indices {
					r := roles[i]
					roleTask := t.buildRoleTask(r, task)

					var prevOutputs []string
					for _, prevStage := range stages[:stageIdx] {
						for _, pi := range prevStage {
							if allRoleOutputs[pi] != "" && !strings.HasPrefix(allRoleOutputs[pi], "(执行失败") {
								prevOutputs = append(prevOutputs, fmt.Sprintf("### %s 的产出\n%s", roles[pi].Role, allRoleOutputs[pi]))
							}
						}
					}
					if len(prevOutputs) > 0 {
						roleTask.WriteString("\n\n## 上游阶段的完整产出\n")
						for _, po := range prevOutputs {
							roleTask.WriteString(fmt.Sprintf("\n<details>\n<summary>展开查看</summary>\n\n%s\n\n</details>\n", po))
						}
					}

					result, err := subTool.Execute(ctx, map[string]interface{}{
						"agent_name":     r.Role,
						"task":           roleTask.String(),
						"max_iterations": float64(20),
					})
					if err != nil {
						allRoleOutputs[i] = fmt.Sprintf("(执行失败: %v)", err)
						output.WriteString(fmt.Sprintf("### %s ❌\n%v\n\n---\n\n", r.Role, err))
					} else {
						allRoleOutputs[i] = result
						output.WriteString(fmt.Sprintf("### %s ✅\n%s\n\n---\n\n", r.Role, result))
					}
				}
			} else {
				// Build parallel tasks
				var ptasks []map[string]interface{}
				for _, i := range indices {
					r := roles[i]
					roleTask := t.buildRoleTask(r, task)

					var prevOutputs []string
					for _, prevStage := range stages[:stageIdx] {
						for _, pi := range prevStage {
							if allRoleOutputs[pi] != "" && !strings.HasPrefix(allRoleOutputs[pi], "(执行失败") {
								prevOutputs = append(prevOutputs, fmt.Sprintf("### %s 的产出\n%s", roles[pi].Role, allRoleOutputs[pi]))
							}
						}
					}
					if len(prevOutputs) > 0 {
						roleTask.WriteString("\n\n## 上游阶段的完整产出\n")
						for _, po := range prevOutputs {
							roleTask.WriteString(fmt.Sprintf("\n<details>\n<summary>展开查看</summary>\n\n%s\n\n</details>\n", po))
						}
						roleTask.WriteString("\n**指引**: 基于上游产出，从你角色的角度独立完成工作。")
					}

					ptasks = append(ptasks, map[string]interface{}{
						"agent_name": r.Role,
						"task":       roleTask.String(),
					})
				}

				t.logf("[TEAM] 混合阶段[%d/%d] 并行执行 %d 个角色", stageIdx+1, len(stages), len(indices))

				var ptasksIface []interface{}
				for _, pt := range ptasks {
					ptasksIface = append(ptasksIface, pt)
				}
				parallelResult, err := parallelTool.Execute(ctx, map[string]interface{}{
					"tasks":           ptasksIface,
					"timeout_minutes": 10,
				})
				if err != nil {
					t.logf("[TEAM] 混合并行阶段[%d/%d] 失败: %v", stageIdx+1, len(stages), err)
					for _, i := range indices {
						allRoleOutputs[i] = fmt.Sprintf("(并行执行失败: %v)", err)
					}
					output.WriteString(fmt.Sprintf("### 并行阶段 ❌\n%v\n\n---\n\n", err))
				} else {
					for _, i := range indices {
						allRoleOutputs[i] = parallelResult // parallel_sub_agent returns aggregated results
					}
					output.WriteString(fmt.Sprintf("### 并行阶段 ✅\n%s\n\n---\n\n", parallelResult))
					t.logf("[TEAM] 混合并行阶段[%d/%d] 完成", stageIdx+1, len(stages))
				}
			}
		}
	}

	// Synthesize
	return t.synthesize(ctx, task, allRoleNames, allRoleOutputs, output.String())
}

// ========== Sequential Execution ==========

func (t *AssembleTeamTool) executeSequential(ctx context.Context, template, task string, roles []TeamRole) (string, error) {
	subTool, err := t.requireTeamTool("sub_agent")
	if err != nil {
		return "", err
	}

	var output strings.Builder
	output.WriteString(fmt.Sprintf("🤝 **%s 团队** (%d人 · 串行协作) — %s\n\n", template, len(roles), task))

	allOutputs := make([]string, len(roles))
	failCount := 0

	for i, r := range roles {
		roleTask := t.buildRoleTask(r, task)

		if i > 0 {
			roleTask.WriteString("\n\n## 上游角色的完整产出\n")
			for j := 0; j < i; j++ {
				if allOutputs[j] != "" && !strings.HasPrefix(allOutputs[j], "(执行失败") {
					roleTask.WriteString(fmt.Sprintf("\n### %s 的输出\n<details>\n<summary>点击展开 %s 的完整产出</summary>\n\n%s\n\n</details>\n", roles[j].Role, roles[j].Role, allOutputs[j]))
				}
			}
			roleTask.WriteString("\n**重要指引**: 你必须在以上所有上游产出的基础上继续推进。不要重复已经完成的工作，而是填补遗漏、深化分析、或执行你角色的专属职责。请仔细阅读所有上游产出，确认哪些已覆盖、哪些待补充。如果发现上游遗漏了原始任务中的关键信息，请补全。如果某个上游角色失败了，请用你的能力弥补其职责。")
		} else {
			roleTask.WriteString("\n\n## 你的职责\n你是团队的第一个角色。请为后续角色打好基础：完整覆盖原始任务的所有要点，输出结构化、便于下游消费的成果。不要遗漏任何原始任务中提到的信息。")
		}

		t.logf("[TEAM] 串行[%d/%d] %s 开始工作", i+1, len(roles), r.Role)

		result, err := subTool.Execute(ctx, map[string]interface{}{
			"agent_name":     r.Role,
			"task":           roleTask.String(),
			"max_iterations": float64(20),
		})
		if err != nil {
			failCount++
			output.WriteString(fmt.Sprintf("### %s ❌\n错误: %v\n\n---\n\n", r.Role, err))
			t.logf("[TEAM] 串行[%d/%d] %s 失败: %v (继续执行后续角色)", i+1, len(roles), r.Role, err)
			allOutputs[i] = fmt.Sprintf("(执行失败: %v)", err)
		} else {
			allOutputs[i] = result
			output.WriteString(fmt.Sprintf("### %s ✅\n%s\n\n---\n\n", r.Role, result))
			t.logf("[TEAM] 串行[%d/%d] %s 完成 (%d chars)", i+1, len(roles), r.Role, len(result))
		}
	}

	if failCount > 0 {
		t.logf("[TEAM] 串行完成: %d/%d 成功, %d 失败", len(roles)-failCount, len(roles), failCount)
	}

	// Synthesize
	roleNames := make([]string, len(roles))
	for i, r := range roles {
		roleNames[i] = r.Role
	}
	return t.synthesize(ctx, task, roleNames, allOutputs, output.String())
}

// ========== Parallel Execution ==========

func (t *AssembleTeamTool) executeParallel(ctx context.Context, template, task string, roles []TeamRole) (string, error) {
	type subTask struct {
		AgentName string `json:"agent_name"`
		Task      string `json:"task"`
	}
	var tasks []subTask
	for _, r := range roles {
		roleTask := t.buildRoleTask(r, task)
		roleTask.WriteString(fmt.Sprintf("\n\n## 任务\n%s\n\n请基于你的角色和工作规范完成任务。输出结构化的结果。你和其他角色并行工作，专注你角色的视角。", task))
		tasks = append(tasks, subTask{AgentName: r.Role, Task: roleTask.String()})
	}

	parallelTool := t.optionalTeamTool("parallel_sub_agent")
	if parallelTool == nil {
		// Fallback to sequential
		return t.executeSequential(ctx, template, task, roles)
	}

	var ptasksInterface []interface{}
	for _, st := range tasks {
		ptasksInterface = append(ptasksInterface, map[string]interface{}{
			"agent_name": st.AgentName,
			"task":       st.Task,
		})
	}
	result, err := parallelTool.Execute(ctx, map[string]interface{}{
		"tasks":           ptasksInterface,
		"timeout_minutes": 8,
	})
	if err != nil {
		return "", fmt.Errorf("team execution failed: %w", err)
	}

	rawOutput := fmt.Sprintf("🤝 **%s 团队** (%d人 · 并行协作)\n\n%s", template, len(roles), result)

	// Synthesize: pass each role's output separately
	// parallel_sub_agent aggregates results, so we pass the raw result as the single source
	roleNames := make([]string, len(roles))
	for i, r := range roles {
		roleNames[i] = r.Role
	}
	allOutputs := make([]string, 1)
	allOutputs[0] = result
	return t.synthesize(ctx, task, roleNames, allOutputs, rawOutput)
}

// ========== Shared Helpers ==========

// buildRoleTask constructs the base task prompt for a single role.
func (t *AssembleTeamTool) buildRoleTask(r TeamRole, task string) *strings.Builder {
	var rt strings.Builder
	rt.WriteString(fmt.Sprintf("## 你的角色: %s\n\n### 角色说明\n%s", r.Role, r.SystemPrompt))
	rt.WriteString("\n\n" + teamRoleDeliveryBoundary)

	// Load role-specific reference doc (from team-roles/)
	refDoc := t.loadRoleRef(r.Role)
	if refDoc != "" {
		rt.WriteString(fmt.Sprintf("\n\n### 工作规范 (必须遵守)\n%s", refDoc))
	}

	// Load associated skill instructions (from skills/{skill}/SKILL.md)
	if r.Skill != "" {
		skillDoc := t.loadSkillContent(r.Skill)
		if skillDoc != "" {
			rt.WriteString(fmt.Sprintf("\n\n### 关联技能指令 (来自 %s skill，必须遵守)\n%s", r.Skill, skillDoc))
		}
	}

	rt.WriteString(fmt.Sprintf("\n\n## 原始任务 (始终保留，不要丢失上下文)\n%s", task))
	return &rt
}

func (t *AssembleTeamTool) logf(format string, args ...interface{}) {
	if t != nil && t.fileLogger != nil {
		t.fileLogger.Printf(format, args...)
	}
}

func (t *AssembleTeamTool) ensureRuntimeTeamTools() error {
	if t == nil || t.toolRegistry == nil {
		return fmt.Errorf("sub_agent tool not available: tool registry is nil")
	}
	if tool, ok := t.toolRegistry.Get("sub_agent"); ok && tool != nil {
		return nil
	}
	engine := globalEngine
	if engine == nil || engine.agentFactory == nil {
		return fmt.Errorf("sub_agent tool not available: multi-agent runtime is not initialized")
	}
	workspace := t.workspace
	if strings.TrimSpace(workspace) == "" {
		workspace = engine.agentFactory.workspace
	}
	engine.registerRuntimeToolsForRegistry(t.toolRegistry, workspace, t.fileLogger)
	if tool, ok := t.toolRegistry.Get("sub_agent"); !ok || tool == nil {
		return fmt.Errorf("sub_agent tool not available after runtime dependency rebinding")
	}
	t.logf("[TEAM] repaired missing runtime sub-agent dependencies before execution")
	return nil
}

func (t *AssembleTeamTool) requireTeamTool(name string) (Tool, error) {
	if t == nil || t.toolRegistry == nil {
		return nil, fmt.Errorf("%s tool not available: tool registry is nil", name)
	}
	tool, ok := t.toolRegistry.Get(name)
	if !ok || tool == nil {
		return nil, fmt.Errorf("%s tool not available", name)
	}
	return tool, nil
}

func (t *AssembleTeamTool) optionalTeamTool(name string) Tool {
	if t == nil || t.toolRegistry == nil {
		return nil
	}
	tool, ok := t.toolRegistry.Get(name)
	if !ok {
		return nil
	}
	return tool
}

func validateTeamStages(roles []TeamRole, stages [][]int) error {
	for stageIdx, stage := range stages {
		for _, roleIdx := range stage {
			if roleIdx < 0 || roleIdx >= len(roles) {
				return fmt.Errorf("hybrid stage %d references role index %d, but only %d role(s) are available", stageIdx+1, roleIdx, len(roles))
			}
		}
	}
	return nil
}

// loadSkillContent loads a skill's SKILL.md file and returns its body (without frontmatter).
func (t *AssembleTeamTool) loadSkillContent(skillName string) string {
	skillPath, data, err := t.readTeamSkillFile(skillName)
	if err != nil {
		t.logf("[TEAM] Skill %s not found: %v", skillName, err)
		return ""
	}

	content := string(data)
	// Strip YAML frontmatter (--- ... ---)
	if strings.HasPrefix(content, "---") {
		idx := strings.Index(content[3:], "---")
		if idx > 0 {
			content = content[idx+6:]
		}
	}
	// Trim leading whitespace but keep the structure
	content = strings.TrimSpace(content)

	// Limit to prevent context blowout — keep the design rules, skip verbose examples
	lines := strings.Split(content, "\n")
	if len(lines) > 120 {
		// Keep first 100 lines (core instructions) + last 20 lines (QA/final guidance)
		content = strings.Join(lines[:100], "\n") + "\n\n... (skill body truncated for context)\n\n" + strings.Join(lines[len(lines)-20:], "\n")
	}

	t.logf("[TEAM] Loaded skill %s from %s for role (%d lines)", skillName, skillPath, len(lines))
	return content
}

func (t *AssembleTeamTool) readTeamSkillFile(skillName string) (string, []byte, error) {
	skillName = strings.TrimSpace(skillName)
	if skillName == "" || strings.ContainsAny(skillName, `/\`) {
		return "", nil, fmt.Errorf("invalid skill name %q", skillName)
	}

	for _, path := range t.teamSkillCandidatePaths(skillName) {
		data, err := os.ReadFile(path)
		if err == nil {
			return path, data, nil
		}
	}
	return "", nil, fmt.Errorf("SKILL.md not found for %s", skillName)
}

func (t *AssembleTeamTool) teamSkillCandidatePaths(skillName string) []string {
	roots := []string{}
	if t != nil && strings.TrimSpace(t.workspace) != "" {
		roots = append(roots, t.workspace)
	}
	roots = append(roots, ".")

	skillBases := []string{"builtin", "skills", "openkitty-skills"}
	seen := map[string]bool{}
	var paths []string
	add := func(path string) {
		path = filepath.Clean(path)
		if seen[path] {
			return
		}
		seen[path] = true
		paths = append(paths, path)
	}

	for _, root := range roots {
		root = strings.TrimSpace(root)
		if root == "" {
			root = "."
		}
		for _, base := range skillBases {
			baseDir := filepath.Join(root, filepath.FromSlash(base))
			add(filepath.Join(baseDir, skillName, "SKILL.md"))

			entries, err := os.ReadDir(baseDir)
			if err != nil {
				continue
			}
			for _, entry := range entries {
				if entry.IsDir() {
					add(filepath.Join(baseDir, entry.Name(), skillName, "SKILL.md"))
				}
			}
		}
	}
	return paths
}

// synthesize runs a final "synthesizer" sub-agent that merges, deduplicates,
// unifies style, checks against the original task, and produces the final deliverable.
func (t *AssembleTeamTool) synthesize(ctx context.Context, task string, roleNames []string, allOutputs []string, fallbackOutput string) (string, error) {
	subTool := t.optionalTeamTool("sub_agent")
	if subTool == nil {
		t.logf("[TEAM] sub_agent不可用，跳过合成，返回原始拼接")
		return fallbackOutput, nil
	}

	var outputsStr strings.Builder
	hasRealOutput := false
	for i, out := range allOutputs {
		if out != "" && !strings.HasPrefix(out, "(执行失败") {
			hasRealOutput = true
			outputsStr.WriteString(fmt.Sprintf("\n### %s\n<details>\n<summary>展开</summary>\n\n%s\n\n</details>\n", roleNames[i], out))
		}
	}

	if !hasRealOutput {
		t.logf("[TEAM] 无有效产出，跳过合成")
		return fallbackOutput, nil
	}

	synthTask := fmt.Sprintf(`## 你的角色: 合成员 (Synthesizer)

### 角色说明
你是团队的质量把关者。所有上游角色已完成各自的工作，你需要将所有产出合并为一份统一、高质量的最终交付物。

## 原始任务
%s

## 所有角色的产出
%s

## 你的职责

1. **合并去重**: 合并各角色重复的内容，保留最完整的版本
2. **统一风格**: 统一格式、术语、语气，确保读起来像一个人写的
3. **对照检查**: 逐条对照原始任务，确认所有要点已覆盖，如有遗漏请补全
4. **移除无效内容**: 去掉纯装饰性、无实际功能的元素（如不工作的按钮、假分页等）
5. **保留有效内容**: 保留所有真实数据、有效交互、有价值的信息

**关键原则**:
- 直接输出最终的、完整的交付物。不要输出"以下是合并后的结果"之类的引导语
- 不要自己编造原始任务中没有的数据
- 如果上游有角色失败，用你的知识弥补其职责产出的缺失
- 优先保证内容的准确性和完整性，其次才是格式美观

%s`, task, outputsStr.String(), teamRoleDeliveryBoundary)

	t.logf("[TEAM] 合成员开始工作")
	result, err := subTool.Execute(ctx, map[string]interface{}{
		"agent_name":     "合成员",
		"task":           synthTask,
		"max_iterations": float64(10),
	})
	if err != nil {
		t.logf("[TEAM] 合成员失败: %v，回退原始拼接", err)
		return fallbackOutput, nil
	}
	t.logf("[TEAM] 合成员完成 (%d chars)", len(result))
	return result, nil
}

// loadRoleRef 加载角色参考文档
func (t *AssembleTeamTool) loadRoleRef(role string) string {
	roleFileMap := map[string]string{
		"前端": "frontend.md", "frontend": "frontend.md",
		"后端": "backend.md", "backend": "backend.md",
		"测试": "qa.md", "QA": "qa.md", "qa": "qa.md", "测试工程师": "qa.md",
		"PM": "pm.md", "pm": "pm.md", "产品经理": "pm.md",
		"研究员": "researcher.md", "分析师": "analyst.md", "撰稿人": "writer.md",
		"架构师": "architect.md", "程序员": "developer.md", "审查员": "reviewer.md",
		"策划": "strategist.md", "写手": "writer.md", "编辑": "editor.md",
	}
	filename := roleFileMap[role]
	if filename == "" {
		filename = strings.ToLower(role) + ".md"
	}
	path := filepath.Join("skills", "team-roles", filename)
	data, err := os.ReadFile(path)
	if err != nil {
		return "" // 文档不存在，不报错
	}
	return string(data)
}
