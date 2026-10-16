package todo

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/robfig/cron/v3"
	"gorm.io/gorm"
)

// parseTodoTime 兼容多种时间格式
func ParseTime(s string) (time.Time, error) {
	formats := []string{
		"2006-01-02 15:04:05",
		"2006-01-02T15:04",
		"2006-01-02T15:04:05",
		time.RFC3339,
	}
	for _, f := range formats {
		if t, err := time.ParseInLocation(f, s, time.Local); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("无法解析时间: %s", s)
}

// TodoManager TODO管理器
type PipelineExecutor interface {
	ExecutePipeline(pipelineName, input, todoID string) (string, error)
}

type Manager struct {
	db             *gorm.DB
	defaultAgent   func() string
	executeAgent   func(context.Context, string, string) (string, error)
	pipelineExec   PipelineExecutor
	cron           *cron.Cron
	logger         *log.Logger
	mu             sync.RWMutex
	stopCh         chan struct{}
	stopped        bool
	checkInterval  time.Duration
	LastStatsLogAt time.Time
}

func (tm *Manager) SetPipelineExecutor(executor PipelineExecutor) {
	if tm == nil {
		return
	}
	tm.mu.Lock()
	tm.pipelineExec = executor
	tm.mu.Unlock()
}

// NewTodoManager 创建TODO管理器
func NewManager(
	db *gorm.DB,
	defaultAgent func() string,
	executeAgent func(context.Context, string, string) (string, error),
) *Manager {
	// 创建带秒级精度的cron
	c := cron.New(cron.WithSeconds())

	return &Manager{
		db:            db,
		defaultAgent:  defaultAgent,
		executeAgent:  executeAgent,
		cron:          c,
		logger:        log.New(os.Stdout, "[TodoManager] ", log.LstdFlags),
		stopCh:        make(chan struct{}),
		checkInterval: 30 * time.Second, // 每10秒检查一次待执行任务
	}
}

// InitTodoTables 初始化TODO表
func InitTables(db *gorm.DB) error {
	err := db.AutoMigrate(
		&Todo{},
		&TodoExecutionLog{},
	)
	if err != nil {
		return fmt.Errorf("TODO表结构迁移失败: %w", err)
	}
	log.Println("TODO表初始化成功")

	if err := initPresetTodos(db); err != nil {
		log.Printf("初始化预设任务失败: %v", err)
		// 不返回错误，避免影响启动
	}
	return nil
}
func initPresetTodos(db *gorm.DB) error {
	log.Println("开始初始化预设TODO任务...")

	// 定义预设任务列表
	presetTodos := []struct {
		PresetKey    string       // 唯一标识，用于去重
		Title        string       // 任务标题
		Description  string       // 任务描述
		ScheduleType string       // 调度类型
		RunHour      int          // 执行小时
		RunMinute    int          // 执行分钟
		Priority     TodoPriority // 优先级
		MaxRetries   int          // 最大重试次数
		Metadata     string       // 元数据
		CreatedBy    string       // 创建者
	}{
		{
			PresetKey: "daily_report_17:00",
			Title:     "日报总结",
			Description: `请总结今天的工作内容，包括：
1. 今天完成的主要任务
2. 遇到的问题和解决方案
3. 明天的工作计划
4. 需要的支持和资源

请将日报保存到合适的位置。`,
			ScheduleType: "cron",
			RunHour:      17,
			RunMinute:    0,
			Priority:     PriorityNormal,
			MaxRetries:   2,
			Metadata:     `{"type":"daily_report","auto_save":true,"preset_key":"daily_report_17:00"}`,
			CreatedBy:    "system",
		},
		{
			PresetKey: "daily_learning_18:00",
			Title:     "Agent自动化知识学习与总结",
			Description: `请完成以下任务：
1. 学习Agent自动化相关的知识，包括但不限于：
   - Agent工作流最佳实践
   - 自动化任务编排技巧
   - 错误处理和重试策略
   - 任务优先级管理
   - 并发控制和资源管理

2. 总结今天学到的内容，形成知识文档

3. 使用 knowledge_save 工具将总结的知识保存到知识库
   - 分类：agent_automation
   - 标签：daily_learning, automation, best_practice
   - 标题格式：YYYY-MM-DD Agent自动化学习总结

请确保输出结构化的知识内容，便于后续检索和使用。`,
			ScheduleType: "cron",
			RunHour:      18,
			RunMinute:    0,
			Priority:     PriorityLow,
			MaxRetries:   1,
			Metadata:     `{"type":"daily_learning","auto_save_to_knowledge":true,"category":"agent_automation","preset_key":"daily_learning_18:00"}`,
			CreatedBy:    "system",
		},
		{
			PresetKey: "self_evolve_weekly_03:00",
			Title:     "Agent自进化: 技能策展与知识沉淀",
			Description: `## Agent Self-Evolution Task (借鉴Hermes Curator)

1. 调用 curate_skills action="auto_prune" 对技能库评分修剪
2. 调用 search_history 回顾本周复杂任务(关键词: create, build, implement, fix, deploy, refactor)
3. 对有重复模式的任务，使用 create_skill 沉淀为可复用技能
4. 总结本周学会的新能力，使用 knowledge_save 保存
5. 输出进化报告: 新增技能、裁剪技能、知识沉淀

目标：每周让 agent 自动成长，减少重复劳动。`,
			ScheduleType: "cron",
			RunHour:      3,
			RunMinute:    7,
			Priority:     PriorityLow,
			MaxRetries:   1,
			Metadata:     `{"type":"self_evolution","preset_key":"self_evolve_weekly_03:00"}`,
			CreatedBy:    "system",
		},
		{
			PresetKey: "storage_cleanup_daily_04:07",
			Title:     "目录整理: 清理storage杂乱文件",
			Description: `## 目录整理任务 — 分类规则

【用户交付物 → storage/ 根目录，不移动】
  - HTML网页、PDF报告、DOCX文档、图片、视频
  - 用户明确要求生成的文件（报告、代码、导出数据等）

【系统中间文件 → 移入 storage/.system/analysis/】
  - *-analysis.md、*-design.md、*-mapping.md 等分析文档
  - 非用户要求生成的 .json/.md（如抓取的原始CSS、中间数据）

【工具结果溢出 → 移入 storage/.system/archive/】
  - tool_results/ 下超过1天的 .txt 文件

【临时文件 → 删除】
  - tmp/ 下超过24小时的文件
  - 废弃的 draft、temp、backup 文件

【执行步骤】
1. list_dir(storage/, depth=1) 查看当前状态
2. 按上述规则分类处理
3. 输出清理报告: 用户文件X个 | 归档Y个 | 删除Z个

目标: storage/根目录只留用户交付物，整洁清晰。`,
			ScheduleType: "cron",
			RunHour:      4,
			RunMinute:    7,
			Priority:     PriorityLow,
			MaxRetries:   1,
			Metadata:     `{"type":"storage_cleanup","preset_key":"storage_cleanup_daily_04:07"}`,
			CreatedBy:    "system",
		},
	}

	createdCount := 0
	skippedCount := 0

	for _, preset := range presetTodos {
		// 计算 PresetKey 的哈希值作为唯一标识
		presetHash := calculateHash(preset.PresetKey)

		// 检查是否已存在相同哈希值的任务（通过 metadata 中的 preset_key 或 hash）
		var existingTodo Todo
		lookup := db.Where("metadata LIKE ?", fmt.Sprintf(`%%"preset_key":"%s"%%`, preset.PresetKey)).
			Or("metadata LIKE ?", fmt.Sprintf(`%%"hash":"%s"%%`, presetHash)).
			Where("status != ?", TodoStatusCancelled). // 排除已取消的任务
			Limit(1).Find(&existingTodo)

		if lookup.Error != nil {
			log.Printf("检查预设任务失败 %s: %v", preset.PresetKey, lookup.Error)
			continue
		}
		if lookup.RowsAffected > 0 {
			// 任务已存在，跳过创建
			log.Printf("预设任务已存在，跳过: %s (ID: %s, 执行时间: %v)",
				preset.Title, existingTodo.TodoID, existingTodo.NextRunAt)
			skippedCount++
			continue
		}

		// 计算执行时间（如果是今天已过时间，则设置到明天）
		now := time.Now()
		runAt := time.Date(now.Year(), now.Month(), now.Day(), preset.RunHour, preset.RunMinute, 0, 0, time.Local)
		if now.After(runAt) {
			runAt = runAt.Add(24 * time.Hour)
			log.Printf("当前时间已过今天 %02d:%02d，任务将安排在明天执行", preset.RunHour, preset.RunMinute)
		}

		// 创建新任务
		todo := &Todo{
			TodoID:       fmt.Sprintf("%s_%d", preset.PresetKey, time.Now().UnixNano()),
			Title:        preset.Title,
			Description:  preset.Description,
			ScheduleType: preset.ScheduleType,
			Priority:     preset.Priority,
			RunAt:        &runAt,
			NextRunAt:    &runAt,
			MaxRetries:   preset.MaxRetries,
			Status:       TodoStatusPending,
			Metadata: fmt.Sprintf(`{"hash":"%s","preset_key":"%s",%s`,
				presetHash, preset.PresetKey, strings.TrimPrefix(preset.Metadata, "{")),
			CreatedBy: preset.CreatedBy,
			CreatedAt: now,
			UpdatedAt: now,
		}

		// 为 cron 类型生成 cron 表达式
		if preset.ScheduleType == "cron" {
			todo.CronExpr = fmt.Sprintf("0 %d %d * * *", preset.RunMinute, preset.RunHour)
		}

		// 修复 Metadata 格式，确保是有效的 JSON
		if !strings.HasPrefix(preset.Metadata, "{") {
			todo.Metadata = fmt.Sprintf(`{"hash":"%s","preset_key":"%s","type":"%s"}`,
				presetHash, preset.PresetKey, preset.PresetKey)
		}

		if err := db.Create(todo).Error; err != nil {
			log.Printf("创建预设任务失败 %s: %v", preset.Title, err)
			continue
		}

		createdCount++
		log.Printf("✅ 创建预设任务成功: %s", preset.Title)
		log.Printf("   - ID: %s", todo.TodoID)
		log.Printf("   - 执行时间: %s", runAt.Format("2006-01-02 15:04:05"))
		log.Printf("   - 优先级: %s", preset.Priority)
	}

	log.Printf("预设任务初始化完成 - 新建: %d, 已存在: %d", createdCount, skippedCount)
	return nil
}

func calculateHash(s string) string {
	hash := 0
	for i := 0; i < len(s); i++ {
		hash = (hash << 5) + int(s[i])
	}
	return fmt.Sprintf("%x", hash)
}

func (tm *Manager) Start() {
	tm.logger.Println("TODO管理器启动")
	tm.logger.Printf("检查间隔: %v", tm.checkInterval)

	// 启动时恢复僵尸任务（crash 后 stuck 在 running 状态的）
	tm.recoverZombieTasks()

	// 加载已有的cron任务并计算 next_run_at
	tm.loadCronJobs()

	// 启动定期检查器（处理所有类型：once/interval/cron）
	tm.logger.Println("启动定期检查器 goroutine")
	go tm.runScheduler()

	// 启动僵尸检测（每5分钟扫描一次running超时任务）
	go tm.runZombieDetector()

	// 启动后立即执行一次检查
	tm.logger.Println("立即执行首次任务检查")
	go func() {
		time.Sleep(45 * time.Second)
		tm.checkAndExecuteTasks()
	}()
}

// recoverZombieTasks 重启时恢复僵尸任务（借鉴hermes durable worker lifecycle）
func (tm *Manager) recoverZombieTasks() {
	var zombies []Todo
	tm.db.Where("status = ? AND updated_at < ?", TodoStatusRunning, time.Now().Add(-10*time.Minute)).Find(&zombies)
	if len(zombies) > 0 {
		tm.logger.Printf("🔄 发现 %d 个僵尸任务，重置为pending", len(zombies))
		for _, z := range zombies {
			tm.db.Model(&z).Updates(map[string]interface{}{
				"status":     TodoStatusPending,
				"updated_at": time.Now(),
			})
			tm.logger.Printf("   已恢复: %s - %s (stuck since %v)", z.TodoID, z.Title, z.UpdatedAt)
		}
	}
}

// runZombieDetector 定期检测僵尸任务（每5分钟）
func (tm *Manager) runZombieDetector() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-tm.stopCh:
			return
		case <-ticker.C:
			var zombies []Todo
			// 超过30分钟仍在running → 僵尸
			tm.db.Where("status = ? AND updated_at < ?", TodoStatusRunning, time.Now().Add(-30*time.Minute)).Find(&zombies)
			for _, z := range zombies {
				tm.logger.Printf("🧟 检测到僵尸任务: %s (running since %v)", z.TodoID, z.UpdatedAt)
				z.RetryCount++
				if z.RetryCount > z.MaxRetries {
					tm.db.Model(&z).Updates(map[string]interface{}{
						"status":      TodoStatusFailed,
						"last_error":  "僵尸任务：超时30分钟未完成",
						"retry_count": z.RetryCount,
						"updated_at":  time.Now(),
					})
				} else {
					tm.db.Model(&z).Updates(map[string]interface{}{
						"status":      TodoStatusPending,
						"retry_count": z.RetryCount,
						"updated_at":  time.Now(),
					})
				}
			}
		}
	}
}

// Stop 停止TODO管理器
func (tm *Manager) Stop() {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	if tm.stopped {
		return
	}
	tm.stopped = true
	tm.logger.Println("TODO管理器停止 - 准备发送停止信号")
	close(tm.stopCh)
	tm.logger.Println("TODO管理器停止 - 停止信号已发送")
	tm.cron.Stop()
	tm.logger.Println("TODO管理器停止 - cron调度器已停止")
}

func (tm *Manager) runScheduler() {
	tm.logger.Println("🚀 调度器已启动")
	ticker := time.NewTicker(tm.checkInterval)
	defer ticker.Stop()

	// 立即执行一次检查
	tm.checkAndExecuteTasks()

	loopCount := 1
	for {
		select {
		case <-tm.stopCh:
			tm.logger.Println("⛔ 调度器已停止")
			return
		case <-ticker.C:
			loopCount++
			// tm.logger.Printf("[第%d次检查] %s", loopCount, time.Now().Format("15:04:05"))
			tm.checkAndExecuteTasks()
		}
	}
}

func (tm *Manager) DeleteTodo(todoID string) error {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	var todo Todo
	result := tm.db.Where("todo_id = ?", todoID).Delete(&todo)
	if result.Error != nil {
		return fmt.Errorf("删除TODO失败: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("TODO不存在: %s", todoID)
	}

	tm.logger.Printf("TODO已物理删除: %s", todoID)
	return nil
}

// UpdateTodo updates a todo with the given fields
func (tm *Manager) UpdateTodo(todoID string, updates map[string]interface{}) (*Todo, error) {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	var todo Todo
	if err := tm.db.Where("todo_id = ?", todoID).First(&todo).Error; err != nil {
		return nil, fmt.Errorf("TODO不存在: %s", todoID)
	}

	// Apply allowed fields
	if v, ok := updates["title"]; ok {
		if s, ok := v.(string); ok {
			todo.Title = s
		}
	}
	if v, ok := updates["description"]; ok {
		if s, ok := v.(string); ok {
			todo.Description = s
		}
	}
	if v, ok := updates["schedule_type"]; ok {
		if s, ok := v.(string); ok {
			todo.ScheduleType = s
			if todo.Status == TodoStatusCompleted || todo.Status == TodoStatusFailed {
				todo.Status = TodoStatusPending
			}
		}
	}
	if v, ok := updates["cron_expr"]; ok {
		if s, ok := v.(string); ok {
			todo.CronExpr = s
			if s != "" {
				next := calculateNextCronTimeStatic(s, time.Now())
				todo.NextRunAt = &next
			}
		}
	}
	if v, ok := updates["interval_sec"]; ok {
		switch n := v.(type) {
		case float64:
			todo.IntervalSec = int(n)
		case int:
			todo.IntervalSec = n
		}
	}
	if v, ok := updates["agent_name"]; ok {
		if s, ok := v.(string); ok {
			todo.AgentName = s
		}
	}
	if v, ok := updates["status"]; ok {
		if s, ok := v.(string); ok {
			todo.Status = TodoStatus(s)
		}
	}
	if v, ok := updates["run_at"]; ok {
		if s, ok := v.(string); ok && s != "" {
			if t, err := ParseTime(s); err == nil {
				todo.RunAt = &t
				if todo.ScheduleType == "once" {
					todo.NextRunAt = &t
				}
			}
		}
	}
	if v, ok := updates["input_data"]; ok {
		if s, ok := v.(string); ok {
			todo.InputData = s
		}
	}

	todo.UpdatedAt = time.Now()
	if err := tm.db.Save(&todo).Error; err != nil {
		return nil, fmt.Errorf("更新TODO失败: %w", err)
	}

	tm.logger.Printf("TODO已更新: %s - %s", todo.TodoID, todo.Title)
	return &todo, nil
}

func (tm *Manager) checkAndExecuteTasks() {
	now := time.Now()

	// 2. 查询需要执行的任务
	var todos []Todo
	err := tm.db.Where("status = ? AND next_run_at IS NOT NULL AND next_run_at <= ?",
		TodoStatusPending, now).
		Order("priority DESC, created_at ASC").
		Find(&todos).Error

	if err != nil {
		tm.logger.Printf("❌ 查询待执行任务失败: %v", err)
		return
	}

	// 3. 只在【真正执行任务】时打印
	if len(todos) > 0 {
		tm.logger.Printf("🎯 本次执行 %d 个任务:", len(todos))
		for _, todo := range todos {
			tm.logger.Printf("🚀 执行任务: %s - %s", todo.TodoID, todo.Title)
			tm.executeTodo(&todo)
		}
	}

	// ==============================================
	// 统计日志 → 【每小时打印一次】，不再每45秒打印
	// ==============================================
	const statsInterval = time.Hour
	if now.Sub(tm.LastStatsLogAt) >= statsInterval {
		var totalPending int64
		tm.db.Model(&Todo{}).Where("status = ?", TodoStatusPending).Count(&totalPending)
		tm.logger.Printf("📊 [每小时统计] 当前 pending 任务总数: %d", totalPending)

		// 打印 pending 任务详情（每小时一次）
		var allPending []Todo
		tm.db.Where("status = ?", TodoStatusPending).Find(&allPending)
		if len(allPending) > 0 {
			tm.logger.Printf("🔍 [每小时统计] pending 任务执行时间预览:")
			for _, todo := range allPending {
				timeUntil := ""
				if todo.NextRunAt != nil {
					if todo.NextRunAt.After(now) {
						timeUntil = fmt.Sprintf("还需等待 %v", todo.NextRunAt.Sub(now).Round(time.Second))
					} else {
						timeUntil = "已过期待执行"
					}
				} else {
					timeUntil = "无执行时间"
				}
				tm.logger.Printf("   - %s: %s | next_run_at=%v [%s]",
					todo.TodoID, todo.Title, todo.NextRunAt, timeUntil)
			}
		}

		// 更新最后统计时间
		tm.LastStatsLogAt = now
	}
}

// calculateNextCronTimeStatic 计算cron表达式的下次执行时间
// 确保返回值严格晚于 after，防止轮询死循环
func calculateNextCronTimeStatic(cronExpr string, after time.Time) time.Time {
	parser := cron.NewParser(cron.Second | cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.DowOptional)
	schedule, err := parser.Parse(cronExpr)
	if err != nil {
		log.Printf("解析cron表达式失败 %s: %v，回退到明天此时", cronExpr, err)
		return after.Add(24 * time.Hour)
	}
	next := schedule.Next(after)
	if !next.After(after) {
		log.Printf("cron Next() 返回非未来时间 %v (after=%v)，强制 +24h", next, after)
		next = after.Add(24 * time.Hour)
	}
	return next
}

// loadCronJobs 加载cron类型的任务到调度器

func (tm *Manager) loadCronJobs() {
	var todos []Todo
	err := tm.db.Where("schedule_type = ? AND status = ?", "cron", TodoStatusPending).
		Find(&todos).Error

	if err != nil {
		tm.logger.Printf("加载cron任务失败: %v", err)
		return
	}

	for _, todo := range todos {
		tm.scheduleCronJob(&todo)
	}
}

// scheduleCronJob 调度cron任务
func (tm *Manager) scheduleCronJob(todo *Todo) {
	if todo.CronExpr == "" {
		return
	}

	now := time.Now()
	next := calculateNextCronTimeStatic(todo.CronExpr, now)
	todo.NextRunAt = &next
	tm.db.Save(todo)
	tm.logger.Printf("已调度cron任务: %s, 表达式: %s, 下次: %v", todo.Title, todo.CronExpr, next)
}

// executeTodo 执行TODO任务
func (tm *Manager) executeTodo(todo *Todo) {
	tm.logger.Printf("⚡ 执行任务: %s - %s", todo.TodoID, todo.Title)
	tm.logger.Printf("   调度类型: %s, 优先级: %s", todo.ScheduleType, todo.Priority)
	// 原子性认领任务：WHERE status='pending' 保证只有第一个goroutine能抢到，防止并发重复执行
	claimResult := tm.db.Model(&Todo{}).Where("todo_id = ? AND status = ?", todo.TodoID, TodoStatusPending).
		Updates(map[string]interface{}{
			"status":     TodoStatusRunning,
			"updated_at": time.Now(),
		})
	if claimResult.RowsAffected == 0 {
		return // 已被其他goroutine认领或状态已变更
	}
	todo.Status = TodoStatusRunning

	// 防抖：距离上次执行不到60秒则跳过
	if todo.LastRunAt != nil && time.Since(*todo.LastRunAt) < 60*time.Second {
		tm.logger.Printf("任务 %s 距上次执行不足60秒，跳过", todo.TodoID)
		tm.db.Model(&Todo{}).Where("todo_id = ?", todo.TodoID).Update("status", TodoStatusPending)
		return
	}

	startTime := time.Now()
	tm.logger.Printf("开始执行任务: %s - %s", todo.TodoID, todo.Title)

	// 记录执行日志
	execLog := &TodoExecutionLog{
		TodoID: todo.TodoID,
		RunAt:  startTime,
		Status: "running",
	}
	tm.db.Create(execLog)

	// 执行任务
	var result string
	var execErr error

	// 确定使用哪个Agent
	agentName := todo.AgentName
	if agentName == "" && tm.defaultAgent != nil {
		agentName = tm.defaultAgent()
	}

	// 如果指定了Pipeline，优先走Pipeline执行
	if todo.PipelineName != "" && tm.pipelineExec != nil {
		tm.logger.Printf("通过Pipeline执行: %s", todo.PipelineName)
		prompt := todo.Description
		if todo.InputData != "" {
			prompt = todo.InputData
		}
		result, execErr = tm.pipelineExec.ExecutePipeline(todo.PipelineName, prompt, todo.TodoID)
	} else {
		if tm.executeAgent == nil {
			execErr = fmt.Errorf("Agent 执行器不可用")
		} else {
			// 构建上下文（带超时：借鉴hermes 5分钟限制）
			taskCtx, taskCancel := context.WithTimeout(context.Background(), 5*time.Minute)
			defer taskCancel()
			taskCtx = context.WithValue(taskCtx, "task_id", todo.TodoID)
			prompt := todo.Description
			if todo.InputData != "" {
				prompt = todo.InputData
			}

			// 执行
			result, execErr = tm.executeAgent(taskCtx, agentName, prompt)
		}
	}

	duration := time.Since(startTime)

	// 更新执行日志
	execLog.DurationMs = duration.Milliseconds()

	// 更新TODO状态
	tm.mu.Lock()
	defer tm.mu.Unlock()

	now := time.Now()
	todo.LastRunAt = &now
	todo.RunCount++
	todo.LastResult = result
	todo.LastError = ""

	// 状态流转：失败时判断重试（借鉴hermes retry budget）
	if execErr != nil {
		todo.LastError = execErr.Error()
		todo.RetryCount++

		if todo.RetryCount < todo.MaxRetries {
			// 指数退避: 1m, 2m, 4m, 8m... 上限10分钟
			backoffSec := 60 * (1 << (todo.RetryCount - 1))
			if backoffSec > 600 {
				backoffSec = 600
			}
			nextRun := now.Add(time.Duration(backoffSec) * time.Second)
			todo.NextRunAt = &nextRun
			todo.Status = TodoStatusPending
			tm.logger.Printf("任务 %s 失败(第%d次)，%ds后重试: %v", todo.TodoID, todo.RetryCount, backoffSec, execErr)
		} else {
			execLog.Status = "failed"
			execLog.Error = execErr.Error()
			todo.Status = TodoStatusFailed
			tm.logger.Printf("任务 %s 已达最大重试次数(%d)，标记为 failed: %v", todo.TodoID, todo.MaxRetries, execErr)
		}
	} else {
		todo.OutputData = result
		execLog.Status = "completed"
		execLog.Result = result
		todo.RetryCount = 0

		switch todo.ScheduleType {
		case "once":
			todo.Status = TodoStatusCompleted
			todo.NextRunAt = nil // 一次性任务无需下次执行
			tm.logger.Printf("一次性任务 %s 执行完成", todo.TodoID)

		case "interval":
			if todo.IntervalSec <= 0 {
				todo.IntervalSec = 60
			}
			nextRun := now.Add(time.Duration(todo.IntervalSec) * time.Second)
			todo.NextRunAt = &nextRun
			todo.Status = TodoStatusPending
			tm.logger.Printf("间隔任务 %s 完成，下次: %s", todo.TodoID, nextRun.Format("2006-01-02 15:04:05"))

		case "cron":
			if todo.CronExpr != "" {
				next := calculateNextCronTimeStatic(todo.CronExpr, now)
				todo.NextRunAt = &next
			}
			todo.Status = TodoStatusPending
			tm.logger.Printf("cron任务 %s 完成，下次: %v", todo.TodoID, todo.NextRunAt)

		default:
			todo.Status = TodoStatusCompleted
			tm.logger.Printf("未知调度类型 %s，标记为 completed", todo.ScheduleType)
		}
	}

	todo.UpdatedAt = now
	tm.db.Save(todo)
	execLog.DurationMs = duration.Milliseconds()
	tm.db.Save(execLog)

	tm.logger.Printf("任务 %s 执行完成，耗时: %dms", todo.TodoID, duration.Milliseconds())
}

// CreateTodo 创建TODO任务
func (tm *Manager) CreateTodo(todo *Todo) (*Todo, error) {
	if todo.TodoID == "" {
		todo.TodoID = fmt.Sprintf("todo_%d", time.Now().UnixNano())
	}

	if todo.Priority == "" {
		todo.Priority = PriorityNormal
	}

	if todo.ScheduleType == "" {
		todo.ScheduleType = "once"
	}

	// 计算首次执行时间
	now := time.Now()
	switch todo.ScheduleType {
	case "once":
		if todo.RunAt != nil {
			todo.NextRunAt = todo.RunAt
		} else {
			todo.NextRunAt = &now
		}
	case "interval":
		if todo.IntervalSec <= 0 {
			todo.IntervalSec = 60 // 默认1分钟
		}
		nextRun := now.Add(time.Duration(todo.IntervalSec) * time.Second)
		todo.NextRunAt = &nextRun
	case "cron":
		if todo.CronExpr == "" {
			return nil, fmt.Errorf("cron任务需要cron表达式")
		}
		nextRun := calculateNextCronTimeStatic(todo.CronExpr, now)
		todo.NextRunAt = &nextRun
	}

	tm.logger.Printf("========================================")
	tm.logger.Printf("创建TODO任务:")
	tm.logger.Printf("  ID: %s", todo.TodoID)
	tm.logger.Printf("  标题: %s", todo.Title)
	tm.logger.Printf("  调度类型: %s", todo.ScheduleType)
	tm.logger.Printf("  下次执行时间: %v", todo.NextRunAt)
	tm.logger.Printf("  当前时间: %s", now.Format("2006-01-02 15:04:05"))
	tm.logger.Printf("========================================")

	todo.Status = TodoStatusPending
	todo.CreatedAt = now
	todo.UpdatedAt = now

	result := tm.db.Create(todo)
	if result.Error != nil {
		return nil, fmt.Errorf("创建TODO失败: %w", result.Error)
	}

	// 如果是cron任务，立即调度
	if todo.ScheduleType == "cron" {
		tm.scheduleCronJob(todo)
	}

	tm.logger.Printf("创建TODO: %s - %s, 调度类型: %s, 下次执行: %v",
		todo.TodoID, todo.Title, todo.ScheduleType, todo.NextRunAt)

	return todo, nil
}

// GetTodo 获取TODO
func (tm *Manager) GetTodo(todoID string) (*Todo, error) {
	var todo Todo
	err := tm.db.Where("todo_id = ?", todoID).First(&todo).Error
	if err != nil {
		return nil, err
	}
	return &todo, nil
}

// ListTodos 列出TODO
func (tm *Manager) ListTodos(agentName, teamName string, status TodoStatus, limit int) ([]Todo, error) {
	var todos []Todo
	query := tm.db.Model(&Todo{})

	if agentName != "" {
		query = query.Where("agent_name = ?", agentName)
	}
	if teamName != "" {
		query = query.Where("team_name = ?", teamName)
	}
	if status != "" {
		query = query.Where("status = ?", status)
	}

	err := query.Order("priority DESC, created_at DESC").Limit(limit).Find(&todos).Error
	return todos, err
}

// CancelTodo 取消TODO
func (tm *Manager) CancelTodo(todoID string) error {
	return tm.db.Model(&Todo{}).
		Where("todo_id = ?", todoID).
		Updates(map[string]interface{}{
			"status":     TodoStatusCancelled,
			"updated_at": time.Now(),
		}).Error
}

// GetExecutionLogs 获取执行日志
func (tm *Manager) GetExecutionLogs(todoID string, limit int) ([]TodoExecutionLog, error) {
	var logs []TodoExecutionLog
	err := tm.db.Where("todo_id = ?", todoID).
		Order("run_at DESC").
		Limit(limit).
		Find(&logs).Error
	return logs, err
}
