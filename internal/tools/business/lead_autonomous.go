package business

import (
	"context"
	"fmt"
	"strings"
	"time"

	"mac/internal/configvalue"
	"mac/internal/toolcontract"
)

// ====================== Lead Auto-Discovery Tool ======================

// LeadDiscoverTool proactively searches for new leads based on industry signals.
type LeadDiscoverTool struct {
	backend LeadBackend
}

func NewLeadDiscoverTool(backend LeadBackend) *LeadDiscoverTool {
	return &LeadDiscoverTool{backend: backend}
}

func (t *LeadDiscoverTool) Name() string { return "lead_discover" }
func (t *LeadDiscoverTool) Description() string {
	return "主动发现新潜在客户。按行业/关键词搜索近期的融资、扩张、招聘等买入信号，识别有价值的销售线索。用于自主工作模式的每日潜客发现。"
}
func (t *LeadDiscoverTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"industry":    map[string]interface{}{"type": "string", "description": "目标行业(如: SaaS/金融/制造/电商/物流)"},
			"signal_type": map[string]interface{}{"type": "string", "description": "信号类型: funding(融资)/hiring(扩张招聘)/tech_upgrade(技术升级)/expansion(业务扩张)"},
			"max_results": map[string]interface{}{"type": "integer", "description": "最多返回数量，默认5"},
		},
		"required": []string{"industry"},
	}
}
func (t *LeadDiscoverTool) IsReadOnly() bool { return true }
func (t *LeadDiscoverTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	industry, _ := params["industry"].(string)
	signalType, _ := params["signal_type"].(string)
	maxResults := configvalue.Int(params, "max_results", 5)
	if maxResults <= 0 {
		maxResults = 5
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("# 潜客发现任务: %s\n\n", industry))

	prompts := map[string]string{
		"funding":      fmt.Sprintf("`%s 融资 2026`", industry),
		"hiring":       fmt.Sprintf("`%s 招聘 技术团队 扩张 2026`", industry),
		"tech_upgrade": fmt.Sprintf("`%s 数字化转型 技术升级 AI 2026`", industry),
		"expansion":    fmt.Sprintf("`%s 业务扩张 新市场 新业务 2026`", industry),
	}

	sb.WriteString("## 搜索指令\n\n")
	sb.WriteString("使用 web_search 工具搜索以下关键词(每个搜索返回前5条结果):\n\n")
	if signalType != "" {
		if prompt, ok := prompts[signalType]; ok {
			sb.WriteString(fmt.Sprintf("- %s\n", prompt))
		}
	} else {
		for _, prompt := range prompts {
			sb.WriteString(fmt.Sprintf("- %s\n", prompt))
		}
	}

	sb.WriteString(fmt.Sprintf("\n## 筛选标准\n\n"))
	sb.WriteString(fmt.Sprintf("从搜索结果中筛选: 1)%s行业 2)有明确买入信号 3)规模匹配\n\n", industry))
	sb.WriteString("## 执行步骤\n\n")
	sb.WriteString("1. web_search 执行上述搜索\n")
	sb.WriteString("2. web_fetch 深入了解有信号的公司(至少3家)\n")
	sb.WriteString("3. 评估每家的意向(budget+need+authority+timeline)\n")
	sb.WriteString(fmt.Sprintf("4. 对评分>=50的使用 lead_save 保存(最多%d条)\n", maxResults))
	sb.WriteString("5. 使用 lead_score 对每条进行评分\n")
	sb.WriteString(fmt.Sprintf("\n目标: 发现至少%d条有价值的潜在线索", maxResults))

	return sb.String(), nil
}

// ====================== Lead Enrichment Tool ======================

// LeadEnrichTool enriches existing leads with deeper research.
type LeadEnrichTool struct {
	backend LeadBackend
}

func NewLeadEnrichTool(backend LeadBackend) *LeadEnrichTool {
	return &LeadEnrichTool{backend: backend}
}

func (t *LeadEnrichTool) Name() string { return "lead_enrich" }
func (t *LeadEnrichTool) Description() string {
	return "充实线索档案。对已有线索进行深度研究：公司最新动态、组织架构、技术栈、采购历史等。用于提升画像质量和触达精准度。"
}
func (t *LeadEnrichTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"lead_id": map[string]interface{}{"type": "string", "description": "线索ID(留空则充实所有低信息量线索)"},
		},
		"required": []string{},
	}
}
func (t *LeadEnrichTool) IsReadOnly() bool { return false }
func (t *LeadEnrichTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	leadID, _ := params["lead_id"].(string)

	if leadID != "" {
		lead, err := t.backend.GetLead(leadID)
		if err != nil {
			return fmt.Sprintf("查询失败: %v", err), nil
		}
		return t.enrichSingle(lead), nil
	}

	// Enrich all leads with low info
	leads, _ := t.backend.ListLeads("all")
	var enriched []string
	for _, l := range leads {
		if l.ResearchNotes == "" || len(l.PainPoints) == 0 {
			enriched = append(enriched, l.Company)
		}
	}
	if len(enriched) == 0 {
		return "所有线索档案已充实，无需补充。", nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("# 批量充实任务 (%d条)\n\n", len(enriched)))
	sb.WriteString("以下线索需要补充信息:\n\n")
	for _, c := range enriched {
		sb.WriteString(fmt.Sprintf("- **%s**: 使用 web_search + web_fetch 搜索公司背景、痛点、决策链\n", c))
	}
	sb.WriteString("\n每完成一家，使用 lead_update 更新备注。")
	return sb.String(), nil
}

func (t *LeadEnrichTool) enrichSingle(lead *LeadRecord) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("# 充实档案: %s - %s\n\n", lead.Name, lead.Company))
	sb.WriteString("## 当前信息\n")
	sb.WriteString(fmt.Sprintf("- 行业: %s\n", lead.Industry))
	sb.WriteString(fmt.Sprintf("- 已知痛点: %s\n", strings.Join(lead.PainPoints, "、")))
	sb.WriteString(fmt.Sprintf("- 已有备注: %s\n\n", lead.ResearchNotes))
	sb.WriteString("## 需要补充\n\n")
	sb.WriteString(fmt.Sprintf("1. web_search `%s 融资 新闻 2026` — 最新融资和财务信息\n", lead.Company))
	sb.WriteString(fmt.Sprintf("2. web_search `%s %s 招聘` — 团队规模和扩张方向\n", lead.Company, lead.Industry))
	sb.WriteString(fmt.Sprintf("3. web_search `%s 数字化 技术` — 技术栈和采购偏好\n", lead.Company))
	sb.WriteString(fmt.Sprintf("4. web_fetch 公司官网 — 产品/服务/客户案例\n"))
	sb.WriteString("\n完成后使用 lead_update 更新该线索的备注和研究记录。")
	return sb.String()
}

// ====================== Lead Nurture Tool ======================

// LeadNurtureTool manages long-term lead nurturing.
type LeadNurtureTool struct {
	backend LeadBackend
}

func NewLeadNurtureTool(backend LeadBackend) *LeadNurtureTool {
	return &LeadNurtureTool{backend: backend}
}

func (t *LeadNurtureTool) Name() string { return "lead_nurture" }
func (t *LeadNurtureTool) Description() string {
	return "管理线索培育。筛选低意向/长期未跟进的线索，生成有价值的行业洞察内容进行长期培育。培育目标：保持联系、展示专业度、等待买入信号。"
}
func (t *LeadNurtureTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"min_score":     map[string]interface{}{"type": "integer", "description": "筛选评分低于此值的线索，默认50"},
			"days_inactive": map[string]interface{}{"type": "integer", "description": "筛选超过N天未更新的线索，默认7"},
		},
		"required": []string{},
	}
}
func (t *LeadNurtureTool) IsReadOnly() bool { return false }
func (t *LeadNurtureTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	minScore := configvalue.Int(params, "min_score", 50)
	if minScore <= 0 {
		minScore = 50
	}
	daysInactive := configvalue.Int(params, "days_inactive", 7)
	if daysInactive <= 0 {
		daysInactive = 7
	}

	leads, err := t.backend.ListLeads("all")
	if err != nil {
		return fmt.Sprintf("查询失败: %v", err), nil
	}

	cutoff := time.Now().AddDate(0, 0, -daysInactive)
	var nurtureList []*LeadRecord
	for _, l := range leads {
		if l.Score < minScore || l.Status == "new" {
			updated, _ := time.Parse("2006-01-02", l.UpdatedAt)
			if updated.Before(cutoff) {
				nurtureList = append(nurtureList, l)
			}
		}
	}

	if len(nurtureList) == 0 {
		return fmt.Sprintf("无符合条件的培育线索(评分<%d且%d天未更新)。", minScore, daysInactive), nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("# 线索培育任务 (%d条)\n\n", len(nurtureList)))
	sb.WriteString(fmt.Sprintf("筛选条件: 评分<%d, %d天未更新\n\n", minScore, daysInactive))
	sb.WriteString("| 公司 | 联系人 | 评分 | 最后更新 | 培育建议 |\n")
	sb.WriteString("|------|--------|------|----------|----------|\n")
	for _, l := range nurtureList {
		action := "发送行业洞察"
		if l.Status == "new" {
			action = "初次触达介绍"
		} else if l.Score >= 30 {
			action = "案例分享+价值展示"
		}
		sb.WriteString(fmt.Sprintf("| %s | %s | %d | %s | %s |\n",
			l.Company, l.Name, l.Score, l.UpdatedAt, action))
	}
	sb.WriteString("\n## 执行\n")
	sb.WriteString("对每条线索使用 lead_outreach 生成培育内容(选择email渠道, purpose=初次接触)。")
	sb.WriteString("\n培育后使用 lead_update 更新状态。")

	return sb.String(), nil
}

// ====================== Extended Registration ======================

// RegisterLeadAutonomousTools returns tools for autonomous lead work.
func RegisterLeadAutonomousTools(backend LeadBackend) []toolcontract.Tool {
	return []toolcontract.Tool{
		NewLeadDiscoverTool(backend),
		NewLeadEnrichTool(backend),
		NewLeadNurtureTool(backend),
	}
}

// RegisterAllLeadTools returns all lead tools including autonomous ones.
func RegisterAllLeadTools(backend LeadBackend) []toolcontract.Tool {
	return append(RegisterLeadTools(backend), RegisterLeadAutonomousTools(backend)...)
}
