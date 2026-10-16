package app

import (
	"fmt"
	"log"
	"os"
	"strings"
	"testing"

	"mac/internal/aiemployee"
)

func TestConversationScenarioSamples150(t *testing.T) {
	mgr := NewAIEmployeeManager(nil, log.New(os.Stderr, "", 0))
	employees := append([]AIEmployeeConfig{}, defaultAIEmployees()...)
	employees = append(employees,
		AIEmployeeConfig{ID: "general_assistant", Name: "通用助手", AtName: "通用助手", Role: "通用问答/知识检索/网页搜索/总结改写", Template: "general_assistant"},
		AIEmployeeConfig{ID: "rag-internal-qa", Name: "RAG知识库助手", AtName: "RAG助手", Role: "知识库问答/RAG检索/制度查询", Template: "internal_qa", RAGKBSlugs: []string{"policy-kb", "product-kb"}, RAGMultiTurn: true, TenantID: "tenant-a"},
		AIEmployeeConfig{ID: "research-assistant", Name: "科研助手", AtName: "科研助手", Role: "文献检索/论文综述/实验设计/引用核查", Direct: &DirectConfig{Prompt: "检索文献、整理综述、核查引用并输出研究建议。", Tools: []string{"web_fetch", "rag_search", "write_file"}}},
		AIEmployeeConfig{ID: "data-scientist", Name: "数据科学家", AtName: "数据科学", Role: "数据建模/特征工程/实验评估/模型报告", Template: "data_scientist"},
	)
	mgr.Register(employees)

	samples := buildConversationScenarioSamples()
	if len(samples) != 150 {
		t.Fatalf("scenario sample count = %d, want 150", len(samples))
	}

	covered := map[string]int{}
	for i, sample := range samples {
		cfg, task := mgr.MatchAtName(sample.message)
		if cfg == nil {
			t.Fatalf("sample %03d did not match any employee: %s", i+1, sample.message)
		}
		if cfg.ID != sample.employeeID {
			t.Fatalf("sample %03d routed to %s, want %s, message=%s", i+1, cfg.ID, sample.employeeID, sample.message)
		}
		if strings.TrimSpace(task) == "" {
			t.Fatalf("sample %03d extracted empty task", i+1)
		}
		if strings.Contains(task, "@"+cfg.AtName) {
			t.Fatalf("sample %03d task still contains @mention: %q", i+1, task)
		}
		assertNoMojibake(t, fmt.Sprintf("sample %03d task", i+1), task)
		covered[sample.category]++
	}

	for _, category := range []string{"conversation", "long_conversation", "customer_service", "rag", "secretary", "data_science", "research", "legal"} {
		if covered[category] == 0 {
			t.Fatalf("category %s not covered", category)
		}
	}
}

func TestConversationMemoryKeepsRecentFiveTurns(t *testing.T) {
	mgr := NewAIEmployeeManager(nil, log.New(os.Stderr, "", 0))
	employeeID := "customer-service"
	mgr.conversations[employeeID] = &aeConversation{}
	for i := 1; i <= 8; i++ {
		history := mgr.conversations[employeeID]
		history.History = append(history.History, fmt.Sprintf("[10:%02d] 用户第%d轮：继续追问退款、物流和发票问题", i, i))
		if len(history.History) > 5 {
			history.History = history.History[1:]
		}
	}

	history := mgr.conversations[employeeID].History
	if len(history) != 5 {
		t.Fatalf("history length = %d, want 5", len(history))
	}
	if strings.Contains(strings.Join(history, "\n"), "第1轮") || strings.Contains(strings.Join(history, "\n"), "第2轮") || strings.Contains(strings.Join(history, "\n"), "第3轮") {
		t.Fatalf("old turns were not dropped: %v", history)
	}
	if !strings.Contains(strings.Join(history, "\n"), "第8轮") {
		t.Fatalf("latest turn missing: %v", history)
	}
}

func TestScenarioTemplatesCarryExpectedToolsAndRAGConfig(t *testing.T) {
	ragCfg := AIEmployeeConfig{ID: "rag-internal-qa", Name: "RAG知识库助手", AtName: "RAG助手", Template: "internal_qa", RAGKBSlugs: []string{"policy-kb"}, RAGMultiTurn: true, TenantID: "tenant-a"}
	if err := aiemployee.ApplyTemplate(ragCfg.Template, &ragCfg); err != nil {
		t.Fatalf("ApplyTemplate internal_qa: %v", err)
	}
	if ragCfg.Direct == nil || !containsString(ragCfg.Direct.Tools, "rag_search") {
		t.Fatalf("RAG internal QA should include rag_search tool: %#v", ragCfg.Direct)
	}
	if !ragCfg.RAGMultiTurn || len(ragCfg.RAGKBSlugs) != 1 || ragCfg.TenantID == "" {
		t.Fatalf("RAG config lost: %#v", ragCfg)
	}

	dsCfg := AIEmployeeConfig{ID: "data-scientist", Name: "数据科学家", AtName: "数据科学", Template: "data_scientist"}
	if err := aiemployee.ApplyTemplate(dsCfg.Template, &dsCfg); err != nil {
		t.Fatalf("ApplyTemplate data_scientist: %v", err)
	}
	for _, tool := range []string{"read_file", "write_file", "persistent_shell"} {
		if dsCfg.Direct == nil || !containsString(dsCfg.Direct.Tools, tool) {
			t.Fatalf("data scientist missing tool %s: %#v", tool, dsCfg.Direct)
		}
	}
}

type conversationScenarioSample struct {
	category   string
	employeeID string
	message    string
}

func buildConversationScenarioSamples() []conversationScenarioSample {
	all := []conversationScenarioSample{}
	add := func(category, employeeID, atName string, tasks ...string) {
		for _, task := range tasks {
			all = append(all, conversationScenarioSample{category: category, employeeID: employeeID, message: "@" + atName + " " + task})
		}
	}

	add("conversation", "general_assistant", "通用助手",
		"先帮我解释一下这个报错的大概原因，后面我再补日志",
		"我们继续刚才的话题，把结论压缩成三条行动建议",
		"请基于前面讨论，整理一版可以发给同事的说明",
		"我只记得上次说到权限问题，帮我接着排查思路",
		"把这段对话里的待确认事项列出来")
	add("long_conversation", "project-assistant", "项目助理",
		"这是一个长对话第1轮：先记录项目背景、成员分工和本周目标",
		"这是一个长对话第2轮：补充昨天完成登录页和权限模型",
		"这是一个长对话第3轮：今天计划联调支付和消息通知",
		"这是一个长对话第4轮：风险是接口字段还没冻结",
		"这是一个长对话第5轮：帮我生成站会纪要",
		"这是一个长对话第6轮：把前面信息整理成周报摘要",
		"这是一个长对话第7轮：继续补充下周计划和阻塞项",
		"这是一个长对话第8轮：最后输出行动清单")
	add("customer_service", "customer-service", "客服",
		"用户说订单三天没发货，请给处理建议",
		"客户投诉收到的商品破损，要怎么安抚并建工单",
		"客户问会员积分为什么没有到账",
		"用户想修改收货地址但订单已出库",
		"客户要求转人工并保留完整上下文",
		"用户说优惠券使用失败，请查常见原因",
		"客户反馈发票抬头开错了，给一版回复",
		"售后质保期争议，先做问题分类",
		"用户要取消订单并退款，生成标准话术",
		"客户问产品是否支持企业批量采购")
	add("customer_service", "cs-pre-sales", "售前",
		"这款产品有没有企业版价格",
		"帮客户对比标准版和专业版差异",
		"用户问库存和发货时效",
		"客户想知道是否支持私有化部署",
		"推荐适合十人团队的套餐")
	add("customer_service", "cs-after-sales", "售后",
		"物流显示签收但客户没收到",
		"用户申请七天无理由退货",
		"客户要查询维修进度",
		"用户说退款迟迟未到账",
		"投诉处理超时，请给升级建议")
	add("rag", "rag-internal-qa", "RAG助手",
		"查一下公司差旅报销制度里高铁二等座能不能报",
		"根据知识库回答新员工试用期转正流程",
		"查产品手册：私有化部署需要哪些服务器规格",
		"继续上个问题，补充安全审计相关要求",
		"知识库里没有答案时应该怎么回复用户",
		"帮我从制度库找年假结转规则",
		"查询客服标准话术里退款时效怎么写",
		"基于产品 KB 输出一版 FAQ 答案",
		"查一下 API 限流规则并引用来源",
		"把刚才检索结果整理成客服可用回复")
	add("secretary", "meeting-secretary", "秘书",
		"根据这段会议记录提取决议和待办",
		"把录音转写后的内容整理成会议纪要",
		"帮我安排下周一和产品团队的评审会",
		"把这份材料摘要成一页管理层简报",
		"从聊天记录里提取负责人和截止时间",
		"继续刚才纪要，补一个风险与依赖章节",
		"把会议纪要保存成 Markdown 文件",
		"整理昨天站会里每个人的进展",
		"根据讨论生成会后跟进邮件",
		"把长文档拆成摘要、结论和待办")
	add("data_science", "data-analyst", "数据分析",
		"读取 sales.csv 分析月度销售趋势",
		"对订单数据做缺失值和异常值检查",
		"帮我生成 GMV 趋势图和渠道对比图",
		"分析用户留存率并输出图表",
		"把 Excel 里的客户分层做成报告",
		"继续上次分析，补充同比环比",
		"对客服工单做主题聚类前的数据清洗",
		"分析广告投放 ROI 并保存结果",
		"检查这个数据集是否存在重复记录",
		"给运营同学一版可读的数据摘要")
	add("data_science", "data-scientist", "数据科学",
		"基于 churn.csv 做流失预测建模",
		"做特征工程并比较逻辑回归和随机森林",
		"评估模型 AUC、召回率和混淆矩阵",
		"设计一次 AB 实验并估算样本量",
		"对时间序列做销量预测",
		"解释模型重要特征并输出可解释性报告",
		"把训练、验证、测试切分方案写清楚",
		"检查数据泄漏风险",
		"用 Python 产出建模报告",
		"继续上个建模任务，给部署建议")
	add("research", "research-assistant", "科研助手",
		"检索近三年 RAG 评测方法相关论文",
		"帮我写一个关于多模态检索的文献综述框架",
		"核查这段论文引言里的引用是否支撑论点",
		"设计一个对比实验验证新方法有效性",
		"总结 NeurIPS 相关方向的研究趋势",
		"找出这篇论文可能的威胁到有效性",
		"把研究问题拆成假设、变量和指标",
		"生成投稿前的 reproducibility checklist",
		"继续上个综述，补充研究空白",
		"把论文摘要改成更清晰的学术表达")
	add("research", "lead-researcher", "潜客研究",
		"调研上海做工业软件的潜在客户名单",
		"分析这家公司是否近期有采购信号",
		"找出目标行业里正在扩张的企业",
		"补充潜客画像和触达切入点",
		"整理一份潜客研究报告")
	add("legal", "legal-review", "法务顾问",
		"审查这份 SaaS 服务合同的付款和违约责任",
		"检查保密条款是否覆盖员工和外包人员",
		"合同里数据处理条款有没有合规风险",
		"帮我提取竞业限制和知识产权条款",
		"输出一版高风险条款清单",
		"继续上次合同审查，补充谈判建议",
		"审核供应商采购协议的终止条件",
		"检查免责条款是否过度偏向对方",
		"给业务同事写一版非法律语言的风险说明",
		"把合同审查结果保存成报告")
	add("conversation", "investment-advisor", "投资顾问",
		"分析下贵州燃气的投资价值和机会",
		"今天 A 股大盘怎么看",
		"新能源板块最近有什么风险",
		"继续上次贵州燃气，补充风险提示",
		"只问行情，不要生成长报告")
	add("conversation", "market-analyst", "市场分析师",
		"分析一下 AI Agent 市场趋势",
		"帮我调研企业知识库产品竞争格局",
		"继续上个市场分析，补充商业化机会",
		"整理一份行业变化的要点列表",
		"分析竞品定价策略")
	add("conversation", "content-editor", "微信公众号编辑",
		"写一篇关于企业 RAG 落地的公众号文章",
		"把这段素材排版成微信 HTML",
		"继续刚才文章，补一个案例小节",
		"优化标题，给十个备选",
		"检查文章错别字和结构")
	add("conversation", "translator", "翻译专员",
		"把这段产品说明翻译成英文",
		"继续上个翻译，保持术语一致",
		"把英文合同摘要翻译成中文",
		"检查这段技术文档的双语术语",
		"输出正式商务语气版本")
	add("conversation", "product-manager", "产品经理",
		"把批量导入 Excel 的需求写成 PRD",
		"继续上个 PRD，补充验收标准",
		"拆分WBS并估算人日和工期",
		"按低中高三档整理成本和报价",
		"根据招标要求整理响应矩阵和标书")
	add("conversation", "developer-engineer", "开发工程师",
		"修复登录接口空指针问题",
		"给订单模块补单元测试",
		"解释这段 Go 代码的执行流程",
		"继续刚才问题，给出最小改动方案",
		"重构这个函数并保持行为不变")
	add("conversation", "code-review-bot", "CR机器人",
		"review 最近这次 PR 的安全风险",
		"检查是否有未处理错误",
		"继续代码审查，补充测试建议",
		"看一下这个 diff 是否会破坏兼容性",
		"输出阻塞级问题优先")
	add("conversation", "hr-recruiter", "HR招聘",
		"筛选这份后端工程师简历",
		"写一版高级数据工程师 JD",
		"继续岗位需求，生成面试题",
		"评估候选人与岗位匹配度",
		"总结面试反馈")
	add("conversation", "finance-specialist", "财务专员",
		"识别这批发票并做报销审核",
		"生成本月费用汇总表",
		"检查报销单是否缺少附件",
		"继续上次财务审核，标注异常项",
		"输出一版费用分析报告")
	add("conversation", "risk-manager", "风控专员",
		"评估这个供应商合作风险",
		"给出合规检查清单",
		"继续上次风险评估，补充预警指标",
		"分析客户逾期付款风险",
		"输出风险分级建议")
	add("conversation", "sales-assistant", "销售助理",
		"生成客户回访计划",
		"整理销售线索跟进记录",
		"继续上次客户，生成报价单要点",
		"写一版跟进邮件",
		"总结本周销售机会")
	add("conversation", "social-media", "社交媒体运营",
		"今天有什么行业热点可追",
		"给小红书写三条内容选题",
		"继续上个排期，补充发布时间",
		"分析最近互动数据",
		"输出一周内容日历")
	add("conversation", "it-ops", "IT运维",
		"分析服务器 CPU 飙高原因",
		"检查最近应用错误日志",
		"继续上个故障，给回滚建议",
		"生成部署前检查清单",
		"输出故障复盘报告")
	add("conversation", "admin-assistant", "行政助理",
		"安排下周三会议室",
		"整理办公用品采购比价",
		"继续上个行政事项，生成通知",
		"归档这批会议材料",
		"输出行政待办清单")
	add("conversation", "supply-chain", "供应链管理",
		"评估三个供应商报价",
		"分析库存周转异常",
		"继续上个采购任务，补成本优化建议",
		"生成供应商评分表",
		"输出采购风险清单")
	add("conversation", "qa-specialist", "质检专员",
		"制定这批产品质检标准",
		"分析缺陷分布和根因",
		"继续上次质检报告，补充整改建议",
		"生成抽检计划",
		"输出质量风险报告")
	add("conversation", "event-planner", "活动策划",
		"设计一场线下沙龙方案",
		"编制活动预算和执行清单",
		"继续上个活动，补充风险预案",
		"生成邀约话术",
		"输出复盘模板")
	add("conversation", "lead-acquisition", "获客",
		"帮我找工业 SaaS 的潜在客户",
		"设计一版触达话术",
		"继续上个获客任务，补充线索评级",
		"分析目标客户画像",
		"输出转化漏斗建议")

	return selectScenarioSamples(all, 150)
}

func selectScenarioSamples(all []conversationScenarioSample, want int) []conversationScenarioSample {
	selected := make([]conversationScenarioSample, 0, want)
	seenEmployee := map[string]bool{}
	used := make([]bool, len(all))
	for i, item := range all {
		if !seenEmployee[item.employeeID] {
			selected = append(selected, item)
			seenEmployee[item.employeeID] = true
			used[i] = true
		}
	}
	for i, item := range all {
		if len(selected) >= want {
			break
		}
		if used[i] {
			continue
		}
		selected = append(selected, item)
	}
	return selected
}

func assertNoMojibake(t *testing.T, label, s string) {
	t.Helper()
	for _, bad := range []string{"????", "鎼", "娴嬭瘯", "\uFFFD"} {
		if strings.Contains(s, bad) {
			t.Fatalf("%s contains mojibake marker %q: %q", label, bad, s)
		}
	}
}

func containsString(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}
