# MACS V2 工作台

V2 工作台是对现有 Web UI 的渐进式改版，入口为 `/web-v2`，实际静态资源位于：

```text
web/workbench-v2.html
web/workbench-v2.css
web/workbench-v2.js
```

旧入口 `/web/` 继续保留。V2 不复制 Agent、Task、Pipeline 或产物引擎，也不改变它们的持久化语义；它只把已有能力组织成“项目—任务—执行进度—产物”的统一界面。

界面采用“会话优先、过程按需、产物集中”的工作台结构：输入框和对话是主界面，Agent 的执行步骤默认折叠，产物与技术详情在右侧面板中按需打开。视觉系统使用白色主画布、浅灰项目栏和深色主操作，避免用大面积品牌色或仪表盘卡片抢占任务内容。

## 设计边界

- 左侧栏只承载新任务、搜索、当前项目、运行中提示和最近任务。
- 中央区域承载新任务入口、会话消息和关键执行进度。
- 右侧产物/详情栏仅在用户需要时出现。
- 手机端使用单层顶栏和抽屉式项目栏，不保留常驻底部导航。
- 新任务页保持一个主输入框和少量任务建议，不使用营销式 Hero、指标卡或功能宫格。
- 用户消息使用轻量气泡，Agent 回复使用开放排版；运行过程使用可展开的步骤列表。
- 输入框支持选择或拖入附件，能力选择仍复用现有 Agent、AI Employee 与 Skill 接口。
- 原始模型错误和调试日志默认折叠，普通界面显示可操作的错误摘要。
- 普通任务不新增权限选择器；不可逆动作仍由既有 Middleware 和审批流程处理。

## 复用的接口

| 用途 | 接口 |
| --- | --- |
| 健康与项目上下文 | `GET /api/health`、`GET /api/status` |
| 会话创建与历史 | `POST /api/sessions`、`GET /api/sessions`、`GET /api/session/{id}` |
| 普通 Agent 任务 | `POST /api/chat`、`GET /api/task/{id}` |
| 运行状态与停止 | `GET /api/tasks/running`、`POST /api/task/stop/{id}` |
| 运行中追加要求 | `POST /api/interrupt` |
| 实时进度 | `GET /api/events?task_id=...` |
| 附件与产物 | `POST /api/upload`、`GET /api/artifacts` |
| 能力发现 | `GET /api/agents`、`GET /api/skills`、`GET /api/ae/list` |

SSE 断开时，页面回退到任务快照轮询。最终消息仍以持久会话和任务状态为准；页面不会把瞬时 SSE 当作持久数据库。

## 本地验证

重新构建并启动主程序后访问：

```text
http://localhost:8090/web-v2
```

前端修改至少执行：

```powershell
node --check web/workbench-v2.js
```

再检查桌面端、390px 手机端、抽屉导航、会话切换、任务提交、运行中追加要求、折叠执行过程、产物侧栏和浏览器控制台。
