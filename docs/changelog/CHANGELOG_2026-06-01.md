# Changelog 2026-06-01

## TODO 定时任务系统

### Bug 修复
- **定时任务无限循环**：`checkAndExecuteTasks` 不再拾取 cron 类型任务（由 cron 调度器专管），且要求 `next_run_at IS NOT NULL`
- **cron 任务完成后 NextRunAt 未更新**：完成后根据 cron 表达式计算并更新下次执行时间
- **失败任务无限重试**：失败直接标记 `failed`，不再反复重试；新增 60 秒防抖，上次执行不足 60 秒则跳过
- **前端 `[object HTMLDivElement]`**：`modal.innerHTML` 拼接时缺少分号，`overlay.appendChild` 返回值被当字符串拼接
- **一次性任务 `run_at` 零值**：`parseTimeInLocation` 格式与前端 `datetime-local` 不匹配，新增 `parseTodoTime` 兼容 4 种时间格式
- **已完成 once 任务 `next_run_at` 残留**：完成后置 nil，避免显示"下次: 0001-01-01"
- **预设任务类型错误**：`日报总结`和`学习总结`从 `once` 改为 `cron`，自动生成 cron 表达式

### 新功能
- **TODO 编辑模态框**：友好调度类型选择（一次性/每天/每周/每月/间隔），自动解析 cron 回填
- **执行历史**：每个任务增加"历史"按钮，弹窗显示每次执行的状态、耗时、错误原因
- **列表展示优化**：显示调度类型描述和 cron 表达式
- **编辑弹窗回填**：已完成任务优先显示 `last_run_at`，标签自动切换为"已执行时间"

### 后端 API
- **新增 PUT /api/todo/{id}**：更新 todo 字段
- **新增 GET /api/todo/{id}/logs**：获取执行历史
- **新增 `UpdateTodo` 方法**：支持更新标题、描述、调度类型、cron、间隔、agent 等

## web_fetch 搜索工具优化

### 改动
- **新增 `engine` + `query` 参数**：agent 不再需要手动拼搜索引擎 URL，直接 `web_fetch(engine="sogou", query="今日油价")`
- **`buildSearchURL`**：工具内部自动构建正确的搜索引擎 URL
- **描述更新**：明确提示使用 `engine+query` 模式

### 效果
- 避免 agent 拼出不存在的域名（如 `bing_cn.com`）
- 减少 URL 构造错误，提高搜索成功率

## 前端架构拆分

- **app.js 6670 行拆分为 4 个模块**：
  - `app.js`（3747 行）：全局状态、工具函数、文件浏览、配置、任务面板
  - `todo.js`（1075 行）：TODO 全部功能
  - `sse.js`（558 行）：SSE 连接、进度事件、看门狗
  - `chat.js`（1375 行）：消息发送/渲染、会话、协同
- 加载顺序：app.js → todo.js → sse.js → chat.js

## 加载状态改进

- **看门狗阈值放宽**：15s 显示"处理中"、60s 显示"可能连接异常"、120s 自动复位
- **移除强制刷新**：任务完成后不再 5 秒 `location.reload()`
- **SSE 重连同步**：重连后自动 `loadSession`，3 秒超时强制复位 `isLoading`

## 启动行为

- **不再每次启动打开新浏览器窗口**：改用 `cmd /c start`（Win）/ `open`（Mac）/ `xdg-open`（Linux），复用已有窗口
- **CORS 全局支持**：所有 API 端点添加 CORS 头

## 代理/AI 行为

- **CreateTodoTool**：agent 创建 todo 时自动绑定当前对话的 agent_name
- **executeTodo**：默认 agent 改为 `config.Agents[0]`（配置列表第一个），不再随机
- **Generate context**：注入 `agent_name` 到 context，工具可获取当前运行的 agent
