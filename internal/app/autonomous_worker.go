package app

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"
)

// ====================== Autonomous Work Mode ======================

// WorkCycleType defines the type of autonomous work a DE performs.
type WorkCycleType string

const (
	CycleDailyReport     WorkCycleType = "daily_report"     // Daily summary report
	CycleLeadDiscovery   WorkCycleType = "lead_discovery"   // Find new leads proactively
	CycleFollowUp        WorkCycleType = "follow_up"        // Check pending follow-ups
	CycleCustomerCheck   WorkCycleType = "customer_checkin" // Proactive customer check
	CyclePipelineReview  WorkCycleType = "pipeline_review"  // Review sales pipeline
	CycleCompetitorIntel WorkCycleType = "competitor_intel" // Competitor monitoring
	CycleNurture         WorkCycleType = "nurture"          // Lead nurturing campaign
	CycleSLAAlert        WorkCycleType = "sla_alert"        // Check SLA compliance
	CycleKnowledgeGap    WorkCycleType = "knowledge_gap"    // Detect unanswered questions
	CycleSatisfaction    WorkCycleType = "satisfaction"     // Post-resolution survey
	CycleCustom          WorkCycleType = "custom"           // Custom task
)

// WorkCycle defines an autonomous work item for a DE.
type WorkCycle struct {
	Type       WorkCycleType `json:"type"`
	Name       string        `json:"name"`
	CronExpr   string        `json:"cron_expr"`   // e.g. "0 9 * * 1-5" for 9am weekdays
	TaskPrompt string        `json:"task_prompt"` // What the DE should do
	Tools      []string      `json:"tools"`       // Tools available for this task
	Enabled    bool          `json:"enabled"`
	LastRun    time.Time     `json:"last_run"`
	LastResult string        `json:"last_result"`
	RunCount   int           `json:"run_count"`
	ErrorCount int           `json:"error_count"`
}

// AutonomousConfig defines a DE's autonomous work configuration.
type AutonomousConfig struct {
	Enabled          bool        `json:"enabled"`
	WorkCycles       []WorkCycle `json:"work_cycles"`
	MaxConcurrent    int         `json:"max_concurrent"` // Max concurrent autonomous tasks
	NotifyOnError    bool        `json:"notify_on_error"`
	NotifyOnComplete bool        `json:"notify_on_complete"`
}

// ====================== Autonomous Worker Engine ======================

// AutonomousWorker manages proactive, scheduled work for digital employees.
type AutonomousWorker struct {
	aeManager *AIEmployeeManager
	gateway   *GatewayService
	logger    *log.Logger
	cycles    map[string]*AutonomousConfig // employeeID -> config
	mu        sync.RWMutex
	stopCh    chan struct{}
	running   map[string]bool // employeeID -> is running an autonomous task
	runMu     sync.Mutex
}

func NewAutonomousWorker(gw *GatewayService, logger *log.Logger) *AutonomousWorker {
	return &AutonomousWorker{
		aeManager: gw.aeManager,
		gateway:   gw,
		logger:    logger,
		cycles:    make(map[string]*AutonomousConfig),
		running:   make(map[string]bool),
	}
}

// RegisterEmployee sets up autonomous work cycles for a DE.
func (aw *AutonomousWorker) RegisterEmployee(employeeID string, config AutonomousConfig) {
	aw.mu.Lock()
	defer aw.mu.Unlock()

	// Apply defaults to each cycle
	for i := range config.WorkCycles {
		if config.WorkCycles[i].CronExpr == "" {
			config.WorkCycles[i].CronExpr = aw.defaultCron(config.WorkCycles[i].Type)
		}
		if config.WorkCycles[i].TaskPrompt == "" {
			config.WorkCycles[i].TaskPrompt = aw.defaultPrompt(employeeID, config.WorkCycles[i].Type)
		}
	}
	aw.cycles[employeeID] = &config
	aw.logger.Printf("[AutoWorker] Registered %s with %d work cycles (enabled=%v)",
		employeeID, len(config.WorkCycles), config.Enabled)
}

// Start begins the autonomous work loop.
func (aw *AutonomousWorker) Start() {
	aw.mu.RLock()
	defer aw.mu.RUnlock()

	// Initialize LastRun to now for all cycles, so they don't fire on first startup
	now := time.Now()
	for _, cfg := range aw.cycles {
		for i := range cfg.WorkCycles {
			cfg.WorkCycles[i].LastRun = now
		}
	}

	aw.logger.Printf("[AutoWorker] Starting autonomous worker engine")
	ticker := time.NewTicker(30 * time.Second) // Check every 30s
	aw.stopCh = make(chan struct{})

	go func() {
		for {
			select {
			case <-ticker.C:
				aw.checkAndRun()
			case <-aw.stopCh:
				ticker.Stop()
				aw.logger.Printf("[AutoWorker] Stopped")
				return
			}
		}
	}()
}

// Stop halts the autonomous work loop.
func (aw *AutonomousWorker) Stop() {
	if aw.stopCh != nil {
		close(aw.stopCh)
	}
}

// checkAndRun evaluates all work cycles and runs those that are due.
func (aw *AutonomousWorker) checkAndRun() {
	aw.mu.RLock()
	snapshot := make(map[string]*AutonomousConfig, len(aw.cycles))
	for k, v := range aw.cycles {
		snapshot[k] = v
	}
	aw.mu.RUnlock()

	now := time.Now()
	for empID, cfg := range snapshot {
		if !cfg.Enabled {
			continue
		}

		// Check concurrency limit
		aw.runMu.Lock()
		if aw.running[empID] {
			aw.runMu.Unlock()
			continue
		}
		aw.runMu.Unlock()

		for i := range cfg.WorkCycles {
			cycle := &cfg.WorkCycles[i]
			if !cycle.Enabled {
				continue
			}
			if !aw.isDue(cycle, now) {
				continue
			}

			// Run the work cycle
			aw.runMu.Lock()
			aw.running[empID] = true
			aw.runMu.Unlock()

			go aw.executeCycle(empID, cycle)
			break // One task per check cycle
		}
	}
}

// isDue checks if a work cycle should run now based on its cron expression.
func (aw *AutonomousWorker) isDue(cycle *WorkCycle, now time.Time) bool {
	if cycle.CronExpr == "" {
		return false
	}

	// Parse cron-like schedule
	// Support: hourly, daily at HH:MM, weekly on weekday at HH:MM, and standard cron
	schedule := cycle.CronExpr

	switch {
	case schedule == "hourly":
		return now.Sub(cycle.LastRun) >= 1*time.Hour
	case schedule == "every_30m":
		return now.Sub(cycle.LastRun) >= 30*time.Minute
	case schedule == "every_4h":
		return now.Sub(cycle.LastRun) >= 4*time.Hour
	case len(schedule) > 5 && schedule[:5] == "daily":
		// "daily_09:00" format
		targetTime, err := time.Parse("daily_15:04", schedule)
		if err != nil {
			return false
		}
		targetToday := time.Date(now.Year(), now.Month(), now.Day(),
			targetTime.Hour(), targetTime.Minute(), 0, 0, now.Location())
		return now.After(targetToday) && cycle.LastRun.Before(targetToday)
	case len(schedule) > 7 && schedule[:7] == "weekly_":
		// "weekly_Mon_09:00" format
		parts := splitSchedule(schedule)
		if len(parts) < 3 {
			return false
		}
		weekday := parseWeekday(parts[1])
		targetTime, err := time.Parse("15:04", parts[2])
		if err != nil {
			return false
		}
		if now.Weekday() != weekday {
			return false
		}
		targetToday := time.Date(now.Year(), now.Month(), now.Day(),
			targetTime.Hour(), targetTime.Minute(), 0, 0, now.Location())
		return now.After(targetToday) && cycle.LastRun.Before(targetToday)
	default:
		// Standard cron: minute hour dayOfMonth month dayOfWeek
		return matchCron(schedule, now) && cycle.LastRun.Before(now.Add(-1*time.Minute))
	}
}

// executeCycle runs a single work cycle task with runtime context injection.
func (aw *AutonomousWorker) executeCycle(employeeID string, cycle *WorkCycle) {
	aw.logger.Printf("[AutoWorker] %s executing: %s (%s)", employeeID, cycle.Name, cycle.Type)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	// Inject autonomous context
	ctx = context.WithValue(ctx, "autonomous_mode", true)
	ctx = context.WithValue(ctx, "work_cycle", string(cycle.Type))

	// Build task prompt with runtime data context
	task := cycle.TaskPrompt
	if task == "" {
		task = aw.defaultPrompt(employeeID, cycle.Type)
	}
	visibleTask := autonomousVisibleTask(cycle, task)

	// Inject live context so the AI has real data to work with
	dataCtx := aw.buildRuntimeContext(employeeID, cycle.Type)
	if dataCtx != "" {
		task = dataCtx + "\n\n" + task
	}

	sessionID := fmt.Sprintf("auto_%s_%d", employeeID, time.Now().Unix())
	if aw.aeManager != nil && aw.aeManager.gateway != nil && aw.aeManager.gateway.db != nil {
		agentName := employeeID
		if cfg, ok := aw.aeManager.GetEmployee(employeeID); ok && strings.TrimSpace(cfg.Name) != "" {
			agentName = cfg.Name
		}
		aw.aeManager.gateway.saveChatMessage(sessionID, "user", agentName, visibleTask, "pending", sessionID, "auto", sessionID)
	}

	_, err := aw.aeManager.Dispatch(ctx, employeeID, task, sessionID)
	if err != nil {
		aw.logger.Printf("[AutoWorker] %s %s error: %v", employeeID, cycle.Name, err)
		aw.mu.Lock()
		cycle.ErrorCount++
		aw.mu.Unlock()
	} else {
		aw.logger.Printf("[AutoWorker] %s %s dispatched OK", employeeID, cycle.Name)
	}

	aw.mu.Lock()
	cycle.LastRun = time.Now()
	cycle.RunCount++
	aw.mu.Unlock()

	aw.runMu.Lock()
	delete(aw.running, employeeID)
	aw.runMu.Unlock()
}

func autonomousVisibleTask(cycle *WorkCycle, task string) string {
	if cycle == nil {
		return task
	}
	name := strings.TrimSpace(cycle.Name)
	if name == "" {
		name = string(cycle.Type)
	}
	task = strings.TrimSpace(task)
	if task == "" {
		return "自主任务：" + name
	}
	return fmt.Sprintf("自主任务：%s\n\n%s", name, task)
}

// buildRuntimeContext gathers live data to inject into autonomous task prompts.
func (aw *AutonomousWorker) buildRuntimeContext(employeeID string, cycleType WorkCycleType) string {
	now := time.Now()

	switch cycleType {
	case CycleSLAAlert, CycleCustomerCheck, CycleSatisfaction:
		// For CS cycles, inject recent work records
		records := aw.aeManager.GetWorkLog(employeeID)
		if len(records) > 0 {
			var sb strings.Builder
			sb.WriteString(fmt.Sprintf("## 当前数据 (生成时间: %s)\n", now.Format("2006-01-02 15:04")))
			sb.WriteString(fmt.Sprintf("工作记录总数: %d\n", len(records)))
			recent := 0
			for i := len(records) - 1; i >= 0 && recent < 3; i-- {
				r := records[i]
				sb.WriteString(fmt.Sprintf("- [%s] %s (%s)\n", r.CreatedAt.Format("01-02 15:04"), truncate(r.Task, 100), r.Status))
				recent++
			}
			return sb.String()
		}

	case CycleFollowUp, CyclePipelineReview, CycleLeadDiscovery, CycleNurture:
		// For lead cycles, inject current pipeline state
		if aw.gateway != nil && aw.gateway.pipelineExec != nil {
			records := aw.aeManager.GetWorkLog(employeeID)
			if len(records) > 0 {
				var sb strings.Builder
				sb.WriteString(fmt.Sprintf("## 当前线索数据 (生成时间: %s)\n", now.Format("2006-01-02 15:04")))
				sb.WriteString(fmt.Sprintf("工作记录总数: %d\n", len(records)))
				sb.WriteString("最近活动:\n")
				count := 0
				for i := len(records) - 1; i >= 0 && count < 5; i-- {
					r := records[i]
					sb.WriteString(fmt.Sprintf("- [%s] %s → %s\n", r.CreatedAt.Format("01-02 15:04"), truncate(r.Task, 80), r.Status))
					count++
				}
				return sb.String()
			}
		}

	case CycleDailyReport:
		records := aw.aeManager.GetWorkLog(employeeID)
		if len(records) > 0 {
			var sb strings.Builder
			sb.WriteString(fmt.Sprintf("## 今日工作摘要 (%s)\n", now.Format("2006-01-02")))
			todayCount := 0
			for i := len(records) - 1; i >= 0; i-- {
				r := records[i]
				if r.CreatedAt.After(now.Truncate(24 * time.Hour)) {
					todayCount++
				}
			}
			sb.WriteString(fmt.Sprintf("今日任务数: %d\n总任务数: %d\n", todayCount, len(records)))
			return sb.String()
		}
	}

	return ""
}

// defaultCron returns default schedule for each work cycle type.
func (aw *AutonomousWorker) defaultCron(cycleType WorkCycleType) string {
	switch cycleType {
	case CycleDailyReport:
		return "daily_18:00" // End of work day
	case CycleLeadDiscovery:
		return "daily_09:00" // Start of work day
	case CycleFollowUp:
		return "daily_10:00" // Mid-morning
	case CycleCustomerCheck:
		return "daily_11:00"
	case CyclePipelineReview:
		return "daily_17:00"
	case CycleCompetitorIntel:
		return "weekly_Mon_09:00" // Monday morning
	case CycleNurture:
		return "weekly_Wed_14:00" // Mid-week
	case CycleSLAAlert:
		return "hourly"
	case CycleKnowledgeGap:
		return "daily_16:00"
	case CycleSatisfaction:
		return "daily_10:00"
	default:
		return "daily_09:00"
	}
}

// defaultPrompt returns the default task prompt for each work cycle type.
func (aw *AutonomousWorker) defaultPrompt(employeeID string, cycleType WorkCycleType) string {
	cfg, _ := aw.aeManager.GetEmployee(employeeID)
	empName := employeeID
	if cfg != nil {
		empName = cfg.Name
	}

	switch cycleType {
	case CycleDailyReport:
		return fmt.Sprintf("自主任务：生成今日工作报告。\n\n作为%s，回顾今天的工作记录(使用 lead_pipeline 查看线索进展)，输出必须围绕：\n1. 接了什么任务\n2. 交付什么\n3. 结果如何\n4. 怎么产出的\n\n同时补充：今日新增线索、已触达/跟进线索、待处理事项、明日计划。将完整 Markdown 报告保存到 .openkitty/storage/daily-report-%s.md，并在结尾给出报告链接，不要只输出过程日志。", empName, time.Now().Format("2006-01-02"))

	case CycleLeadDiscovery:
		return fmt.Sprintf("自主任务：发现新潜在客户。\n\n作为%s，主动搜索行业动态和潜在客户信号：\n1. 使用 web_search 搜索行业新闻(融资/扩张/招聘/转型)\n2. 使用 web_fetch 深入了解有信号的公司\n3. 评估是否为潜在客户(痛点匹配+预算能力)\n4. 使用 lead_save 保存高质量线索\n5. 使用 lead_score 评分\n\n重点关注：最近1周的行业新闻和融资公告。", empName)

	case CycleFollowUp:
		return fmt.Sprintf("自主任务：跟进检查。\n\n作为%s，扫描所有线索中需要跟进的：\n1. 使用 lead_pipeline 查看所有线索\n2. 找出超过3天未跟进的线索\n3. 对需要跟进的线索使用 lead_outreach 生成触达话术\n4. 更新跟进状态\n\n优先级：高评分线索 > 中等评分 > 低评分", empName)

	case CycleCustomerCheck:
		return fmt.Sprintf("自主任务：主动客户关怀。\n\n作为%s，对近期有交互的客户进行主动关怀：\n1. 检查近期服务记录\n2. 对24小时前解决的问题进行回访\n3. 询问是否还有其他需要帮助的\n4. 收集客户满意度反馈\n\n语气温暖自然，不打扰客户。", empName)

	case CyclePipelineReview:
		return fmt.Sprintf("自主任务：获客管道审查。\n\n作为%s，审查整体获客管道：\n1. 使用 lead_pipeline 查看完整管道\n2. 分析转化漏斗，找出瓶颈\n3. 标注停滞的线索(超过7天无进展)\n4. 对高意向线索制定推进策略\n5. 输出管道健康度报告保存到 .openkitty/storage/pipeline-review-%s.md\n\n报告结构统一为：接了什么任务 → 交付什么 → 结果如何 → 怎么产出的，并重点关注转化率、平均周期、卡点分析。", empName, time.Now().Format("2006-01-02"))

	case CycleCompetitorIntel:
		return fmt.Sprintf("自主任务：竞品情报收集。\n\n作为%s，定期监控竞品动态：\n1. 使用 web_search 搜索主要竞品最新动态\n2. 关注竞品融资/新产品/客户案例/价格变化\n3. 分析竞品动向对获客策略的影响\n4. 保存到 .openkitty/storage/competitor-intel-%s.md\n\n报告结构统一为：接了什么任务 → 交付什么 → 结果如何 → 怎么产出的，并补充竞品差异化话术。", empName, time.Now().Format("2006-01-02"))

	case CycleNurture:
		return fmt.Sprintf("自主任务：线索培育。\n\n作为%s，对低意向线索进行长期培育：\n1. 使用 lead_pipeline 筛选评分<50的线索\n2. 为每条线索准备有价值的行业洞察内容\n3. 不要推销，提供价值\n4. 记录培育动作\n\n如需输出培育总结，统一使用“接了什么任务 → 交付什么 → 结果如何 → 怎么产出的”的结构。目标是保持联系、展示专业度、等待采购需求信号。", empName)

	case CycleSLAAlert:
		return fmt.Sprintf("自主任务：SLA合规检查。\n\n作为%s，检查是否有超时的客服工单或待处理事项：\n1. 检查是否有超过响应时间的问题\n2. 检查是否有pending超过24小时的工单\n3. 对超时项生成告警\n4. 优先处理紧急事项\n\n如果输出报告，统一用“接了什么任务 → 交付什么 → 结果如何 → 怎么产出的”的结构，并附上报告链接。", empName)

	case CycleKnowledgeGap:
		return fmt.Sprintf("自主任务：知识缺口检测。\n\n作为%s，分析近期无法回答的问题：\n1. 检查被转人工或无法处理的问题\n2. 识别高频但无标准答案的问题\n3. 建议补充FAQ或知识库内容\n4. 使用 rag_search 验证知识库覆盖度\n\n输出知识缺口报告保存到 .openkitty/storage/knowledge-gap-%s.md，报告结构统一为：接了什么任务 → 交付什么 → 结果如何 → 怎么产出的。", empName, time.Now().Format("2006-01-02"))

	case CycleSatisfaction:
		return fmt.Sprintf("自主任务：满意度回访。\n\n作为%s，对近期已完成服务的客户进行满意度回访：\n1. 检查24小时内完成的工单\n2. 发送简短的满意度调研\n3. 对不满意客户立即升级处理\n4. 汇总满意度数据\n\n如需生成回访报告，统一使用“接了什么任务 → 交付什么 → 结果如何 → 怎么产出的”的结构，并附上报告链接。", empName)

	default:
		return fmt.Sprintf("自主任务。作为%s，请根据你的职责主动完成以下工作：\n\n1. 检查待处理事项\n2. 推进未完成的任务\n3. 记录工作进展\n4. 必要时通知主管", empName)
	}
}

// ====================== Pre-built Configs ======================

// DefaultLeadAcquisitionConfig returns autonomous work config for lead acquisition DE.
func DefaultLeadAcquisitionConfig() AutonomousConfig {
	return AutonomousConfig{
		Enabled:       true,
		MaxConcurrent: 1,
		WorkCycles: []WorkCycle{
			{Type: CycleLeadDiscovery, Name: "每日潜客发现", Enabled: true},
			{Type: CycleFollowUp, Name: "跟进提醒检查", Enabled: true},
			{Type: CyclePipelineReview, Name: "管道健康审查", Enabled: true},
			{Type: CycleCompetitorIntel, Name: "竞品情报收集", Enabled: true},
			{Type: CycleNurture, Name: "线索长期培育", Enabled: true},
			{Type: CycleDailyReport, Name: "每日获客报告", Enabled: true},
		},
		NotifyOnComplete: false,
		NotifyOnError:    true,
	}
}

// DefaultCustomerServiceConfig returns autonomous work config for CS DE.
func DefaultCustomerServiceConfig() AutonomousConfig {
	return AutonomousConfig{
		Enabled:       true,
		MaxConcurrent: 1,
		WorkCycles: []WorkCycle{
			{Type: CycleSLAAlert, Name: "SLA合规检查", Enabled: true},
			{Type: CycleCustomerCheck, Name: "主动客户关怀", Enabled: true},
			{Type: CycleSatisfaction, Name: "满意度回访", Enabled: true},
			{Type: CycleKnowledgeGap, Name: "知识缺口检测", Enabled: true},
			{Type: CycleDailyReport, Name: "每日客服报告", Enabled: true},
		},
		NotifyOnComplete: false,
		NotifyOnError:    true,
	}
}

// ====================== Helpers ======================

func splitSchedule(s string) []string {
	var parts []string
	for _, p := range splitByChar(s, '_') {
		parts = append(parts, p)
	}
	return parts
}

func splitByChar(s string, sep byte) []string {
	var result []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == sep {
			if i > start {
				result = append(result, s[start:i])
			}
			start = i + 1
		}
	}
	if start < len(s) {
		result = append(result, s[start:])
	}
	return result
}

func parseWeekday(s string) time.Weekday {
	switch s {
	case "Mon", "mon", "1":
		return time.Monday
	case "Tue", "tue", "2":
		return time.Tuesday
	case "Wed", "wed", "3":
		return time.Wednesday
	case "Thu", "thu", "4":
		return time.Thursday
	case "Fri", "fri", "5":
		return time.Friday
	case "Sat", "sat", "6":
		return time.Saturday
	case "Sun", "sun", "0", "7":
		return time.Sunday
	default:
		return time.Monday
	}
}

func matchCron(cronExpr string, now time.Time) bool {
	// Simple cron: minute hour dayOfMonth month dayOfWeek
	var min, hour, dom, month, dow int
	_, err := fmt.Sscanf(cronExpr, "%d %d %d %d %d", &min, &hour, &dom, &month, &dow)
	if err != nil {
		return false
	}

	match := func(field, current int) bool {
		return field == -1 || field == current
	}

	return match(min, now.Minute()) &&
		match(hour, now.Hour()) &&
		match(dom, now.Day()) &&
		match(month, int(now.Month())) &&
		match(dow, int(now.Weekday()))
}

// ====================== Global Instance ======================

var globalAutoWorker *AutonomousWorker

func GetAutonomousWorker() *AutonomousWorker {
	return globalAutoWorker
}
