# 项目专家团队配置

项目专家团队复用 `assemble_team`，不创建另一套任务执行器。工具选取岗位的顺序是本次调用的 `custom_roles`、配置文件的 `team_templates`、内置模板。Web 团队目录使用后台的实际岗位、职责与 Skill，编辑和新增写入当前配置文件，浏览器不另存团队副本。

## 本机配置接口

`GET /api/team-templates` 返回：

```json
{
  "config_revision": "文件内容的 SHA-256",
  "teams": [{
    "id": "research",
    "name": "调研分析团队",
    "description": "搜集资料、分析事实并整理报告",
    "mode": "parallel",
    "builtin": true,
    "role_count_locked": false,
    "roles": [{"role": "研究员", "system_prompt": "岗位职责", "skill": "可选 Skill 名称"}]
  }]
}
```

稳定 `id` 就是 `Config.TeamTemplates` 的映射 key。内置 key 保持不变，允许覆盖岗位配置。新自定义团队以其名称作为 key，不另建名称或模式元数据。`roles[].model` 是既有兼容字段，接口保留它；目前 `assemble_team` 不据此切换岗位模型，界面不应承诺独立模型选择。

`PUT /api/team-templates` 只保存一个团队：

```json
{
  "id": "我的专家团队",
  "config_revision": "读取时的版本",
  "roles": [{"role": "核对员", "system_prompt": "核对输入资料，不编造参数", "skill": ""}]
}
```

需要本机地址、本机 Host、同源请求；不依赖记忆数据库，也不是认证的多租户 API。未知 JSON 字段、查询参数和非 GET/PUT 方法拒绝。请求最多 512 KiB；团队名和岗位名最多 80 个字符、岗位 1–16 个，每项职责最多 8000 个字符、职责合计最多 64000 个字符。Skill 验证名称格式及路径边界，不代表它已经安装；运行时仍按现有 Skill 加载规则读取。

保存复用 `LoadConfig`、系统设置配置验证、`SaveConfig` 原子写入和 `ReloadConfig`。只改变目标团队，保留其他配置及模型密钥环境引用。系统设置与团队写入在本 Gateway 内共用写锁；配置文件版本不匹配时返回 `409`，界面应保留草稿，重新读取后由用户核对再保存。文件版本检查可发现外部修改，但不是跨进程文件锁。

成功返回 `200 {status:"success",team,config_revision}`。如果文件已经保存但运行时更新失败，返回 `500 {status:"saved_reload_failed",message,config_revision}`；界面必须显示部分失败，不能提示已完整生效。响应不会带模型 API Key、供应商地址或内部重载错误详情。GET 目录以配置文件为准，因此重载失败时仍能看到已写入文件的修改。

## 实际执行模式

| key | 模式 | 编辑约束 |
| --- | --- | --- |
| `research`、`content`、普通自定义 key | parallel | 岗位并行处理 |
| `coding`、`ppt`、`codebase`、`contract`、`academic` | sequential | 后续岗位接收前序产出 |
| `fullstack` | hybrid | 固定 4 岗位，产品规划 → 后端/前端并行 → 测试 |
| `competitor` | hybrid | 固定 3 岗位，格局扫描 → 同一竞对岗位 3 路并行 → 策略输出 |

`academic` 是既有运行时串行 key，即使由用户新增，目录仍如实显示 sequential。`auto` 只用于工具自动匹配，不能保存为团队。两个混合流程必须保留固定岗位数量和顺序，避免阶段索引越界或新增岗位未被执行。

团队变化触发两个宿主的引擎热更新；已有助手的 `assemble_team` 更新自己的配置引用，工具 Schema 也列出自定义 key。更新不会为 task-scoped AgentOS 运行时重新注册已被隔离的协作工具。选用团队只填入对话草稿，是否提交任务仍由用户决定。

实现：`internal/app/team_templates_api.go`、`assemble_team.go`，宿主注册与热加载在 `gateway.go`、`mac.go`。聚焦回归：`go test ./internal/app -run '^TestTeamTemplates' -count=1`。
