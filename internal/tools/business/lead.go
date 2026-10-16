package business

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"mac/internal/configvalue"
	"mac/internal/toolcontract"
)

// ====================== Lead Backend Interface ======================

// LeadBackend is the pluggable backend for lead management.
// Implement to connect to real CRM systems (Salesforce, HubSpot, etc.).
type LeadBackend interface {
	SaveLead(lead *LeadRecord) (string, error)
	GetLead(leadID string) (*LeadRecord, error)
	UpdateLeadStatus(leadID, status, notes string) error
	ListLeads(status string) ([]*LeadRecord, error)
	SaveOutreach(leadID, channel, content string) (string, error)
}

// ====================== Lead Data Types ======================

type LeadRecord struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	Company       string   `json:"company"`
	Industry      string   `json:"industry"`
	Position      string   `json:"position"`
	ContactInfo   string   `json:"contact_info,omitempty"`
	Source        string   `json:"source"`
	PainPoints    []string `json:"pain_points"`
	Budget        string   `json:"budget,omitempty"`
	Timeline      string   `json:"timeline,omitempty"`
	DecisionRole  string   `json:"decision_role,omitempty"`
	Score         int      `json:"score"` // 0-100
	ScoreReasons  []string `json:"score_reasons"`
	Status        string   `json:"status"` // new/contacted/qualified/proposal/negotiation/won/lost
	NextStep      string   `json:"next_step"`
	NextFollowUp  string   `json:"next_follow_up,omitempty"`
	Tags          []string `json:"tags"`
	ResearchNotes string   `json:"research_notes,omitempty"`
	CreatedAt     string   `json:"created_at"`
	UpdatedAt     string   `json:"updated_at"`
}

type OutreachRecord struct {
	ID        string `json:"id"`
	LeadID    string `json:"lead_id"`
	Channel   string `json:"channel"` // email/wechat/phone/linkedin
	Subject   string `json:"subject"`
	Content   string `json:"content"`
	CreatedAt string `json:"created_at"`
}

type LeadReport struct {
	TotalLeads     int            `json:"total_leads"`
	ByStatus       map[string]int `json:"by_status"`
	BySource       map[string]int `json:"by_source"`
	AvgScore       float64        `json:"avg_score"`
	ConversionRate float64        `json:"conversion_rate"`
	TopLeads       []*LeadRecord  `json:"top_leads"`
	UpdatedAt      string         `json:"updated_at"`
}

// ====================== Mock Lead Backend ======================

type MockLeadBackend struct {
	leads      map[string]*LeadRecord
	outreaches map[string][]*OutreachRecord
	mu         sync.RWMutex
}

func NewMockLeadBackend() *MockLeadBackend {
	m := &MockLeadBackend{
		leads:      make(map[string]*LeadRecord),
		outreaches: make(map[string][]*OutreachRecord),
	}
	m.seedData()
	return m
}

func (m *MockLeadBackend) seedData() {
	seeds := []*LeadRecord{
		{
			ID: "LEAD001", Name: "张总", Company: "星辰科技有限公司", Industry: "企业服务SaaS",
			Position: "技术VP", Source: "行业会议", PainPoints: []string{"开发效率低", "代码质量不稳定"},
			Budget: "50-100万", Timeline: "Q3", DecisionRole: "技术决策者",
			Score: 85, ScoreReasons: []string{"明确痛点", "预算充足", "决策权高"},
			Status: "qualified", NextStep: "产品演示", NextFollowUp: "2026-07-01",
			Tags: []string{"高意向", "SaaS"}, ResearchNotes: "公司刚完成B轮融资，技术团队150人，正在做研发效能提升",
			CreatedAt: "2026-06-20", UpdatedAt: "2026-06-25",
		},
		{
			ID: "LEAD002", Name: "李经理", Company: "远航物流集团", Industry: "物流供应链",
			Position: "运营总监", Source: "官网留资", PainPoints: []string{"人工成本高", "客服效率低"},
			Budget: "30-50万", Timeline: "Q4", DecisionRole: "业务推动者",
			Score: 65, ScoreReasons: []string{"痛点匹配", "预算中等", "需要推动审批"},
			Status: "contacted", NextStep: "需求确认会议", NextFollowUp: "2026-07-03",
			Tags: []string{"物流", "客服场景"}, ResearchNotes: "传统物流企业，2000+员工，已有简单客服系统",
			CreatedAt: "2026-06-22", UpdatedAt: "2026-06-26",
		},
		{
			ID: "LEAD003", Name: "王总", Company: "金瑞金融服务", Industry: "金融科技",
			Position: "CTO", Source: "转介绍", PainPoints: []string{"合规审查效率低", "合同量大"},
			Budget: "100万+", Timeline: "Q3", DecisionRole: "最终决策者",
			Score: 95, ScoreReasons: []string{"强痛点", "预算充足", "一把手推动", "合规刚需"},
			Status: "proposal", NextStep: "报价方案提交", NextFollowUp: "2026-06-30",
			Tags: []string{"高意向", "金融", "合同审查"}, ResearchNotes: "A股上市公司，法务+合规团队30人，每年处理合同5000+份",
			CreatedAt: "2026-06-15", UpdatedAt: "2026-06-27",
		},
		{
			ID: "LEAD004", Name: "赵经理", Company: "新锐电商平台", Industry: "电子商务",
			Position: "增长负责人", Source: "线上广告", PainPoints: []string{"客户流失率高", "转化率低"},
			Budget: "20-30万", Timeline: "未确定", DecisionRole: "建议者",
			Score: 45, ScoreReasons: []string{"痛点模糊", "预算偏低", "决策权有限", "时间线不明确"},
			Status: "new", NextStep: "初步沟通了解需求",
			Tags: []string{"电商", "客服场景"}, ResearchNotes: "创业公司，50人，正在快速扩张",
			CreatedAt: "2026-06-26", UpdatedAt: "2026-06-26",
		},
		{
			ID: "LEAD005", Name: "陈总", Company: "恒达制造集团", Industry: "制造业",
			Position: "CEO", Source: "行业峰会", PainPoints: []string{"供应链效率低", "质检流程需数字化"},
			Budget: "200万+", Timeline: "Q3-Q4", DecisionRole: "最终决策者",
			Score: 90, ScoreReasons: []string{"一把手推动", "预算充足", "数字化转型刚需", "多场景应用"},
			Status: "qualified", NextStep: "高层对接会议", NextFollowUp: "2026-07-05",
			Tags:          []string{"高意向", "制造业", "供应链", "质检"},
			ResearchNotes: "年营收50亿+，5000+员工，正在进行数字化转型，去年调研过多家供应商",
			CreatedAt:     "2026-06-18", UpdatedAt: "2026-06-25",
		},
	}
	for _, l := range seeds {
		m.leads[l.ID] = l
	}
}

func (m *MockLeadBackend) SaveLead(lead *LeadRecord) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if lead.ID == "" {
		lead.ID = fmt.Sprintf("LEAD%03d", len(m.leads)+1)
	}
	lead.CreatedAt = time.Now().Format("2006-01-02")
	lead.UpdatedAt = lead.CreatedAt
	m.leads[lead.ID] = lead
	return lead.ID, nil
}

func (m *MockLeadBackend) GetLead(leadID string) (*LeadRecord, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	leadID = strings.ToUpper(strings.TrimSpace(leadID))
	if l, ok := m.leads[leadID]; ok {
		return l, nil
	}
	return nil, fmt.Errorf("线索 %s 未找到", leadID)
}

func (m *MockLeadBackend) UpdateLeadStatus(leadID, status, notes string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	leadID = strings.ToUpper(strings.TrimSpace(leadID))
	l, ok := m.leads[leadID]
	if !ok {
		return fmt.Errorf("线索 %s 未找到", leadID)
	}
	l.Status = status
	if notes != "" {
		l.ResearchNotes += "\n" + notes
	}
	l.UpdatedAt = time.Now().Format("2006-01-02")
	return nil
}

func (m *MockLeadBackend) ListLeads(status string) ([]*LeadRecord, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var result []*LeadRecord
	for _, l := range m.leads {
		if status == "" || status == "all" || l.Status == status {
			result = append(result, l)
		}
	}
	return result, nil
}

func (m *MockLeadBackend) SaveOutreach(leadID, channel, content string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	outreachID := fmt.Sprintf("OUT%d", len(m.outreaches)+1)
	rec := &OutreachRecord{
		ID:        outreachID,
		LeadID:    leadID,
		Channel:   channel,
		Content:   content,
		CreatedAt: time.Now().Format("2006-01-02 15:04:05"),
	}
	m.outreaches[leadID] = append(m.outreaches[leadID], rec)
	return outreachID, nil
}

// ====================== Lead Tool: Research ======================

type LeadResearchTool struct {
	backend LeadBackend
}

func NewLeadResearchTool(backend LeadBackend) *LeadResearchTool {
	return &LeadResearchTool{backend: backend}
}

func (t *LeadResearchTool) Name() string { return "lead_research" }
func (t *LeadResearchTool) Description() string {
	return "研究潜在客户信息。输入公司名称或客户名称，返回公司背景、行业、规模、痛点、决策链等分析。结合 web_search 和 web_fetch 获取最新信息。"
}
func (t *LeadResearchTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"company_name": map[string]interface{}{"type": "string", "description": "目标公司名称"},
			"contact_name": map[string]interface{}{"type": "string", "description": "联系人名称（可选）"},
			"industry":     map[string]interface{}{"type": "string", "description": "行业（可选，用于缩小搜索范围）"},
		},
		"required": []string{"company_name"},
	}
}
func (t *LeadResearchTool) IsReadOnly() bool { return true }
func (t *LeadResearchTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	company, _ := params["company_name"].(string)
	contact, _ := params["contact_name"].(string)

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("# 潜客研究: %s\n\n", company))

	// Research template - the AI should use web_search + web_fetch for real data,
	// this tool provides the structured template and prompts for what to research.
	sb.WriteString("## 研究清单\n\n")
	sb.WriteString("请使用 web_search 和 web_fetch 工具搜索以下信息:\n\n")
	sb.WriteString(fmt.Sprintf("1. **公司概况**: %s 的主营业务、规模、发展阶段\n", company))
	sb.WriteString("2. **行业地位**: 市场份额、竞争对手、行业趋势\n")
	sb.WriteString("3. **决策链**: 组织架构、关键决策人")
	if contact != "" {
		sb.WriteString(fmt.Sprintf("（重点关注: %s）\n", contact))
	}
	sb.WriteString("4. **痛点推测**: 基于行业和规模推测可能的业务痛点\n")
	sb.WriteString("5. **预算能力**: 融资情况、IT投入、采购周期\n")
	sb.WriteString("6. **切入建议**: 最佳接触方式、切入点、时机\n\n")
	sb.WriteString("## 搜索关键词建议\n")
	sb.WriteString(fmt.Sprintf("- `%s 融资 新闻`\n", company))
	sb.WriteString(fmt.Sprintf("- `%s %s 招聘`\n", company, extractIndustry(company)))
	sb.WriteString(fmt.Sprintf("- `%s 数字化 转型`\n", company))
	sb.WriteString("\n研究完成后请用 lead_save 保存线索档案。")

	return sb.String(), nil
}

// ====================== Lead Tool: Save ======================

type LeadSaveTool struct {
	backend LeadBackend
}

func NewLeadSaveTool(backend LeadBackend) *LeadSaveTool {
	return &LeadSaveTool{backend: backend}
}

func (t *LeadSaveTool) Name() string { return "lead_save" }
func (t *LeadSaveTool) Description() string {
	return "保存新线索到CRM系统。需要填写公司名、行业、痛点、评分、下一步计划。保存后返回线索ID用于后续跟进。"
}
func (t *LeadSaveTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"name":        map[string]interface{}{"type": "string", "description": "联系人姓名"},
			"company":     map[string]interface{}{"type": "string", "description": "公司名称"},
			"industry":    map[string]interface{}{"type": "string", "description": "行业"},
			"position":    map[string]interface{}{"type": "string", "description": "职位"},
			"source":      map[string]interface{}{"type": "string", "description": "线索来源（行业会议/官网留资/转介绍/线上广告/自主开发）"},
			"pain_points": map[string]interface{}{"type": "string", "description": "痛点（逗号分隔）"},
			"score":       map[string]interface{}{"type": "integer", "description": "意向评分 0-100"},
			"next_step":   map[string]interface{}{"type": "string", "description": "下一步动作"},
			"notes":       map[string]interface{}{"type": "string", "description": "研究备注"},
		},
		"required": []string{"name", "company", "source"},
	}
}
func (t *LeadSaveTool) IsReadOnly() bool { return false }
func (t *LeadSaveTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	lead := &LeadRecord{
		Name:          configvalue.String(params, "name", ""),
		Company:       configvalue.String(params, "company", ""),
		Industry:      configvalue.String(params, "industry", ""),
		Position:      configvalue.String(params, "position", ""),
		Source:        configvalue.String(params, "source", ""),
		PainPoints:    splitCSV(configvalue.String(params, "pain_points", "")),
		Score:         configvalue.Int(params, "score", 0),
		NextStep:      configvalue.String(params, "next_step", ""),
		ResearchNotes: configvalue.String(params, "notes", ""),
		Status:        "new",
		CreatedAt:     time.Now().Format("2006-01-02"),
		UpdatedAt:     time.Now().Format("2006-01-02"),
	}
	id, err := t.backend.SaveLead(lead)
	if err != nil {
		return fmt.Sprintf("保存失败: %v", err), nil
	}
	data, _ := json.MarshalIndent(lead, "", "  ")
	return fmt.Sprintf("线索已保存: %s\n```json\n%s\n```\n\n下一步: %s", id, string(data), lead.NextStep), nil
}

// ====================== Lead Tool: Score ======================

type LeadScoreTool struct {
	backend LeadBackend
}

func NewLeadScoreTool(backend LeadBackend) *LeadScoreTool {
	return &LeadScoreTool{backend: backend}
}

func (t *LeadScoreTool) Name() string { return "lead_score" }
func (t *LeadScoreTool) Description() string {
	return "评估线索质量并打分(0-100)。评估维度：需求匹配度、预算能力、决策权、时间紧迫度、竞争态势。分数>=80为高意向，50-79为中等，<50为低意向。"
}
func (t *LeadScoreTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"lead_id":     map[string]interface{}{"type": "string", "description": "线索ID"},
			"need_fit":    map[string]interface{}{"type": "integer", "description": "需求匹配度 0-25"},
			"budget":      map[string]interface{}{"type": "integer", "description": "预算能力 0-25"},
			"authority":   map[string]interface{}{"type": "integer", "description": "决策权 0-25"},
			"timeline":    map[string]interface{}{"type": "integer", "description": "时间紧迫度 0-15"},
			"competition": map[string]interface{}{"type": "integer", "description": "竞争态势 0-10"},
		},
		"required": []string{"lead_id"},
	}
}
func (t *LeadScoreTool) IsReadOnly() bool { return false }
func (t *LeadScoreTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	leadID, _ := params["lead_id"].(string)
	lead, err := t.backend.GetLead(leadID)
	if err != nil {
		return fmt.Sprintf("查询失败: %v", err), nil
	}

	need := configvalue.Int(params, "need_fit", 0)
	budget := configvalue.Int(params, "budget", 0)
	auth := configvalue.Int(params, "authority", 0)
	timeline := configvalue.Int(params, "timeline", 0)
	comp := configvalue.Int(params, "competition", 0)

	total := need + budget + auth + timeline + comp

	grade := "低意向"
	switch {
	case total >= 80:
		grade = "高意向 - 重点跟进"
	case total >= 50:
		grade = "中等意向 - 持续培育"
	}

	lead.Score = total
	lead.Status = "qualified"
	lead.ScoreReasons = []string{
		fmt.Sprintf("需求匹配: %d/25", need),
		fmt.Sprintf("预算能力: %d/25", budget),
		fmt.Sprintf("决策权: %d/25", auth),
		fmt.Sprintf("时间紧迫: %d/15", timeline),
		fmt.Sprintf("竞争态势: %d/10", comp),
	}
	t.backend.UpdateLeadStatus(leadID, "qualified", "")

	return fmt.Sprintf("## 线索评分: %s (%s)\n\n总分: **%d/100** — %s\n\n| 维度 | 得分 |\n|------|------|\n| 需求匹配度 | %d/25 |\n| 预算能力 | %d/25 |\n| 决策权 | %d/25 |\n| 时间紧迫度 | %d/15 |\n| 竞争态势 | %d/10 |\n\n下一步: %s",
		lead.Name, lead.Company, total, grade, need, budget, auth, timeline, comp, lead.NextStep), nil
}

// ====================== Lead Tool: Outreach ======================

type LeadOutreachTool struct {
	backend LeadBackend
}

func NewLeadOutreachTool(backend LeadBackend) *LeadOutreachTool {
	return &LeadOutreachTool{backend: backend}
}

func (t *LeadOutreachTool) Name() string { return "lead_outreach" }
func (t *LeadOutreachTool) Description() string {
	return "生成个性化触达话术。根据线索画像生成匹配的邮件/微信/电话话术。输入线索ID和触达渠道，输出适合场景的专业话术。"
}
func (t *LeadOutreachTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"lead_id": map[string]interface{}{"type": "string", "description": "线索ID"},
			"channel": map[string]interface{}{"type": "string", "description": "触达渠道: email/wechat/phone/linkedin"},
			"purpose": map[string]interface{}{"type": "string", "description": "触达目的: 初次接触/产品演示邀请/方案跟进/节日问候/续约提醒"},
		},
		"required": []string{"lead_id", "channel", "purpose"},
	}
}
func (t *LeadOutreachTool) IsReadOnly() bool { return false }
func (t *LeadOutreachTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	leadID, _ := params["lead_id"].(string)
	channel, _ := params["channel"].(string)
	purpose, _ := params["purpose"].(string)

	lead, err := t.backend.GetLead(leadID)
	if err != nil {
		return fmt.Sprintf("查询失败: %v", err), nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("# 触达话术生成\n\n"))
	sb.WriteString(fmt.Sprintf("**线索**: %s - %s (%s)\n", lead.Name, lead.Company, lead.Position))
	sb.WriteString(fmt.Sprintf("**渠道**: %s | **目的**: %s\n", channel, purpose))
	sb.WriteString(fmt.Sprintf("**评分**: %d/100 | **痛点**: %s\n\n", lead.Score, strings.Join(lead.PainPoints, "、")))

	// Generate channel-appropriate template
	switch channel {
	case "email":
		sb.WriteString("## 邮件模板\n\n")
		sb.WriteString(fmt.Sprintf("**主题**: 关于%s的%s方案 — %s\n\n", lead.Company, extractMainPainPoint(lead.PainPoints), getEmailHook(purpose)))
		sb.WriteString(fmt.Sprintf("%s总您好，\n\n", lead.Name))
		sb.WriteString(fmt.Sprintf("了解到贵司在%s方面的情况，我们在这方面有成熟的解决方案，已经帮助[同行业客户]实现了[具体效果]。\n\n", strings.Join(lead.PainPoints, "和")))
		sb.WriteString("如果有兴趣，我们可以安排一次15分钟的简短交流，看看是否能帮到您。\n\n")
		sb.WriteString("期待您的回复。\n\n此致")
	case "wechat":
		sb.WriteString("## 微信话术\n\n")
		sb.WriteString(fmt.Sprintf("%s总好，我是[公司名]的[姓名]。关注到贵司在%s方面的情况，我们做过类似案例。方便的话可以交流一下？不打扰您。\n\n", lead.Name, lead.PainPoints[0]))
		sb.WriteString("> 话术要点: 简洁直接，15秒内说明来意和能提供的价值。附带1-2个同行业案例名称增加可信度。")
	case "phone":
		sb.WriteString("## 电话话术\n\n")
		sb.WriteString("**开场(15秒)**: 您好，是[公司名]的[姓名]总吗？我是[公司名]的[姓名]，了解到贵司在[痛点]方面的情况，想看看能否帮到您。\n\n")
		sb.WriteString("**价值陈述(30秒)**: 我们帮助[同行业客户]在[具体指标]上提升了[X]%。\n\n")
		sb.WriteString("**行动邀约(15秒)**: 您这周有空吗？15分钟电话或视频简单聊聊，不耽误您时间。\n\n")
		sb.WriteString("> 话术要点: 点名+来意+价值+邀约，全程控制在60秒内。准备好2-3个FAQ备用。")
	}

	// Save the outreach record
	content := sb.String()
	t.backend.SaveOutreach(leadID, channel, content)
	t.backend.UpdateLeadStatus(leadID, "contacted", fmt.Sprintf("已生成%s触达话术", channel))

	return content, nil
}

// ====================== Lead Tool: Pipeline Report ======================

type LeadPipelineTool struct {
	backend LeadBackend
}

func NewLeadPipelineTool(backend LeadBackend) *LeadPipelineTool {
	return &LeadPipelineTool{backend: backend}
}

func (t *LeadPipelineTool) Name() string { return "lead_pipeline" }
func (t *LeadPipelineTool) Description() string {
	return "查看获客管道全景。按状态(new/contacted/qualified/proposal/negotiation/won/lost)、来源、评分查看所有线索。生成转化漏斗报告。"
}
func (t *LeadPipelineTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"status": map[string]interface{}{"type": "string", "description": "按状态筛选(留空则全部): new/contacted/qualified/proposal/negotiation/won/lost"},
		},
		"required": []string{},
	}
}
func (t *LeadPipelineTool) IsReadOnly() bool { return true }
func (t *LeadPipelineTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	status, _ := params["status"].(string)

	leads, err := t.backend.ListLeads(status)
	if err != nil {
		return fmt.Sprintf("查询失败: %v", err), nil
	}
	if len(leads) == 0 {
		return "当前没有符合条件的线索。", nil
	}

	// Build report
	byStatus := map[string]int{}
	bySource := map[string]int{}
	totalScore := 0
	var topLeads []*LeadRecord

	for _, l := range leads {
		byStatus[l.Status]++
		bySource[l.Source]++
		totalScore += l.Score
		if l.Score >= 70 {
			topLeads = append(topLeads, l)
		}
	}

	// Conversion rate: won / (new+contacted+qualified+proposal+negotiation+won)
	totalPipeline := 0
	won := 0
	for _, l := range leads {
		if l.Status != "lost" {
			totalPipeline++
		}
		if l.Status == "won" {
			won++
		}
	}
	convRate := 0.0
	if totalPipeline > 0 {
		convRate = float64(won) / float64(totalPipeline) * 100
	}

	var sb strings.Builder
	sb.WriteString("# 获客管道报告\n\n")

	sb.WriteString("## 概览\n")
	sb.WriteString(fmt.Sprintf("- **总线索数**: %d\n", len(leads)))
	sb.WriteString(fmt.Sprintf("- **平均评分**: %.1f/100\n", float64(totalScore)/float64(len(leads))))
	sb.WriteString(fmt.Sprintf("- **转化率**: %.1f%%\n", convRate))

	sb.WriteString("\n## 漏斗分布\n\n")
	stages := []string{"new", "contacted", "qualified", "proposal", "negotiation", "won", "lost"}
	stageNames := map[string]string{
		"new": "新线索", "contacted": "已触达", "qualified": "已评级",
		"proposal": "方案中", "negotiation": "谈判中", "won": "已成交", "lost": "已流失",
	}
	sb.WriteString("| 阶段 | 数量 |\n|------|------|\n")
	for _, s := range stages {
		if c, ok := byStatus[s]; ok {
			sb.WriteString(fmt.Sprintf("| %s | %d |\n", stageNames[s], c))
		} else {
			sb.WriteString(fmt.Sprintf("| %s | 0 |\n", stageNames[s]))
		}
	}

	sb.WriteString("\n## 来源分布\n\n")
	sb.WriteString("| 来源 | 数量 |\n|------|------|\n")
	for src, count := range bySource {
		sb.WriteString(fmt.Sprintf("| %s | %d |\n", src, count))
	}

	if len(topLeads) > 0 {
		sb.WriteString(fmt.Sprintf("\n## 高意向线索 (>=70分, 共%d个)\n\n", len(topLeads)))
		sb.WriteString("| 公司 | 联系人 | 评分 | 状态 | 下一步 |\n")
		sb.WriteString("|------|--------|------|------|--------|\n")
		for _, l := range topLeads {
			sb.WriteString(fmt.Sprintf("| %s | %s | %d | %s | %s |\n",
				l.Company, l.Name, l.Score, stageNames[l.Status], l.NextStep))
		}
	}

	sb.WriteString(fmt.Sprintf("\n报告生成时间: %s\n", time.Now().Format("2006-01-02 15:04:05")))
	return sb.String(), nil
}

// ====================== Lead Tool: Update Status ======================

type LeadUpdateTool struct {
	backend LeadBackend
}

func NewLeadUpdateTool(backend LeadBackend) *LeadUpdateTool {
	return &LeadUpdateTool{backend: backend}
}

func (t *LeadUpdateTool) Name() string { return "lead_update" }
func (t *LeadUpdateTool) Description() string {
	return "更新线索状态和跟进记录。状态: new(新线索)/contacted(已触达)/qualified(已评级)/proposal(方案中)/negotiation(谈判中)/won(已成交)/lost(已流失)。每次更新可附带备注说明。"
}
func (t *LeadUpdateTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"lead_id": map[string]interface{}{"type": "string", "description": "线索ID"},
			"status":  map[string]interface{}{"type": "string", "description": "新状态: new/contacted/qualified/proposal/negotiation/won/lost"},
			"notes":   map[string]interface{}{"type": "string", "description": "跟进备注"},
		},
		"required": []string{"lead_id", "status"},
	}
}
func (t *LeadUpdateTool) IsReadOnly() bool { return false }
func (t *LeadUpdateTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	leadID, _ := params["lead_id"].(string)
	status, _ := params["status"].(string)
	notes, _ := params["notes"].(string)

	err := t.backend.UpdateLeadStatus(leadID, status, notes)
	if err != nil {
		return fmt.Sprintf("更新失败: %v", err), nil
	}

	lead, _ := t.backend.GetLead(leadID)
	return fmt.Sprintf("线索已更新: %s — %s → %s\n备注: %s", lead.Name, lead.Company, status, notes), nil
}

// ====================== Helpers ======================

var globalLeadBackend LeadBackend

func GetLeadBackend() LeadBackend {
	if globalLeadBackend == nil {
		globalLeadBackend = NewMockLeadBackend()
	}
	return globalLeadBackend
}

func SetLeadBackend(backend LeadBackend) {
	globalLeadBackend = backend
}

// RegisterLeadTools returns all lead acquisition tools.
func RegisterLeadTools(backend LeadBackend) []toolcontract.Tool {
	return []toolcontract.Tool{
		NewLeadResearchTool(backend),
		NewLeadSaveTool(backend),
		NewLeadScoreTool(backend),
		NewLeadOutreachTool(backend),
		NewLeadPipelineTool(backend),
		NewLeadUpdateTool(backend),
	}
}

func splitCSV(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	var result []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}

func extractIndustry(company string) string {
	// Simple heuristic for search keyword generation
	for _, kw := range []string{"科技", "金融", "物流", "制造", "电商", "医疗", "教育", "地产"} {
		if strings.Contains(company, kw) {
			return kw
		}
	}
	return ""
}

func extractMainPainPoint(pains []string) string {
	if len(pains) > 0 {
		return pains[0]
	}
	return "业务"
}

func getEmailHook(purpose string) string {
	switch purpose {
	case "初次接触":
		return "提升效率的可能性"
	case "产品演示邀请":
		return "10分钟了解方案详情"
	case "方案跟进":
		return "上次沟通的后续"
	case "节日问候":
		return "祝您节日快乐"
	default:
		return "一个可能的解决方案"
	}
}
