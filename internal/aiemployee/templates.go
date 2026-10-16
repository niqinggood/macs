package aiemployee

import (
	"fmt"
	"strings"
	"time"
)

type Template struct {
	Name          string        `json:"name"`
	Description   string        `json:"description"`
	SystemMessage string        `json:"system_message"`
	Direct        *DirectConfig `json:"direct,omitempty"`
}

func IsWithinWorkingHours(hours string) bool {
	return IsWithinWorkingHoursAt(hours, time.Now())
}

func IsWithinWorkingHoursAt(hours string, now time.Time) bool {
	currentMinutes := now.Hour()*60 + now.Minute()
	for _, segment := range strings.Split(hours, ",") {
		parts := strings.SplitN(strings.TrimSpace(segment), "-", 2)
		if len(parts) != 2 {
			continue
		}
		start, startErr := time.Parse("15:04", strings.TrimSpace(parts[0]))
		end, endErr := time.Parse("15:04", strings.TrimSpace(parts[1]))
		if startErr != nil || endErr != nil {
			continue
		}
		startMinutes := start.Hour()*60 + start.Minute()
		endMinutes := end.Hour()*60 + end.Minute()
		if startMinutes <= endMinutes && currentMinutes >= startMinutes && currentMinutes <= endMinutes {
			return true
		}
		if startMinutes > endMinutes && (currentMinutes >= startMinutes || currentMinutes <= endMinutes) {
			return true
		}
	}
	return false
}

func Templates() []Template {
	return []Template{
		{Name: "customer_service", Description: "通用客服专员，处理客户咨询、售后问题、FAQ应答", SystemMessage: "你是专业的客服专员。用 rag_search 检索知识库获取标准答案，用 cs_product_query 查询商品信息。先共情再解决问题，使用工具查询后再回复，不确定时诚实引导转人工。", Direct: &DirectConfig{Prompt: "回答客户问题，使用工具查询后再给出准确回复。", Tools: []string{"rag_search", "cs_product_query"}}},
		{Name: "tech_support", Description: "技术支持专员，排查产品使用问题和FAQ解答", SystemMessage: "你是技术支持专员。工作流程：1) 先理解问题 2) 查FAQ和知识库 3) 给排查步骤 4) 无法解决则升级。使用 rag_search 查知识库，必要时建议用户联系售后。", Direct: &DirectConfig{Prompt: "排查客户的技术问题，先查rag_search获取解决方案，逐步引导用户排查。", Tools: []string{"rag_search", "web_search"}}},
		{Name: "internal_qa", Description: "企业内部问答助手，回答公司制度、流程、知识库问题", SystemMessage: "你是企业内部问答助手。所有答案从知识库中检索，不可编造。如果知识库没有相关信息，诚实告知并建议联系相关部门。", Direct: &DirectConfig{Prompt: "回答员工的企业内部问题，严格从知识库检索。", Tools: []string{"rag_search"}}},
		{Name: "general_assistant", Description: "通用AI助手，知识检索 + 网页搜索", SystemMessage: "你是通用AI助手。可用 rag_search 检索内部知识库，用 web_fetch 获取外部信息。简洁专业，不确定时不编造。", Direct: &DirectConfig{Prompt: "回答用户问题，优先查知识库，必要时搜索网络。", Tools: []string{"rag_search", "web_fetch", "web_search"}}},
		{Name: "data_analyst", Description: "数据分析师，数据加载、统计分析、可视化图表和完整分析报告", SystemMessage: "你是数据分析师。使用 read_file 读取数据文件(.csv/.xlsx/.json)，使用 persistent_shell 运行 Python(pandas/numpy/matplotlib/seaborn) 做分析和可视化。用 write_file 保存分析图表PNG和完整 Markdown 分析报告到 storage/ 目录。报告必须包含摘要、数据概览、数据质量、核心发现、图表解读、建议和产出文件清单；不要只返回零散结论或图表路径。如果用户明确要求 PPT/PPTX/PowerPoint/幻灯片/演示文稿或汇报材料，必须在完整 Markdown 报告和图表之后生成真实 .pptx 文件并在最终回复给出路径。", Direct: &DirectConfig{Prompt: "分析数据文件，用Python做统计分析和可视化，并整理成完整 Markdown 分析报告保存到 storage/。如果用户明确要求 PPT/PPTX/PowerPoint/幻灯片/演示文稿或汇报材料，必须继续生成真实 .pptx 文件。最终回复必须给出报告路径、图表路径、PPT路径（如适用）和3-6条摘要。", Tools: []string{"read_file", "write_file", "persistent_shell", "web_fetch"}}},
		{Name: "data_scientist", Description: "数据科学家，深入数据分析和ML建模", SystemMessage: "你是数据科学家。使用 read_file 读取数据，persistent_shell 运行 Python(pandas/sklearn/xgboost/numpy/scipy)，write_file 保存结果。遵循CRISP-DM方法论：业务理解→数据理解→数据准备→建模→评估→部署。", Direct: &DirectConfig{Prompt: "深入分析数据，用Python做ML建模分析，结果保存到storage/。", Tools: []string{"read_file", "write_file", "persistent_shell", "web_fetch"}}},
		{Name: "daily_summary", Description: "日报/周报生成助手，收集信息并生成工作报告", SystemMessage: "你是报告生成助手。使用 persistent_shell 收集 git 提交记录和项目变更，使用 read_file 读取现有文档，使用 write_file 生成报告，使用 web_fetch 获取外部数据。输出必须围绕“接了什么任务 → 交付什么 → 结果如何 → 怎么产出的”。", Direct: &DirectConfig{Prompt: "收集今日工作信息，生成结果导向工作报告，并保存为 Markdown 文件。报告结构固定为：接了什么任务、交付什么、结果如何、怎么产出的。", Tools: []string{"read_file", "write_file", "persistent_shell", "web_fetch", "rag_search"}}},
	}
}

func ApplyTemplate(templateName string, config *Config) error {
	for _, template := range Templates() {
		if template.Name != templateName {
			continue
		}
		if config.SystemMessage == "" {
			config.SystemMessage = template.SystemMessage
		}
		if config.Direct == nil && template.Direct != nil {
			config.Direct = template.Direct
		}
		return nil
	}
	return fmt.Errorf("template '%s' not found (available: customer_service, tech_support, internal_qa, general_assistant)", templateName)
}
