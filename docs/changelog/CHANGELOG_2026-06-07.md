# Changelog 2026-06-07 — 借鉴 Claude Code / Hermes，全面强化 Agent 能力

## 总览

通过深入研究 Claude Code 泄露源码和 Hermes Agent 架构，本轮对 openkitty 进行了系统性能力升级。

---

## 一、Web 抓取增强（借鉴 Hermes）

| 改动 | 文件 | 效果 |
|------|------|------|
| readability.js 自动安装依赖 | `web_fetch.go` | jsdom/@mozilla/readability 缺失时自动 `npm install` |
| LLM 压缩中间层 | `web_fetch.go` | 内容 >5000 字自动调 LLM 压缩到 ~5000 字 |
| chromedp 反检测加固 | `web_fetch.go` | 注入 `navigator.webdriver=undefined`，伪造 plugins/languages，真实 UA |
| SSRF 防护 | `web_fetch.go` | `isPrivateIP()` 拦截内网/回环地址 |
| base64 图片清理 | `web_fetch.go` | `cleanBase64Images()` 移除 `data:image;base64` |
| readability.js 图片保留 | `skills/readability.js` | `<img src>` 转 `![image](url)` 防止照片丢失 |

## 二、PTC 程序化工具调用（借鉴 Hermes）

| 改动 | 文件 |
|------|------|
| PTC HTTP 服务器 + 沙箱执行 | `ptc_server.go` |
| 9 个工具白名单（edit_file/code_check/cli/git 等） | `ptc_server.go` |
| 环境变量清理 `scrubEnv()` | `ptc_server.go` |
| `ptc_execute` 核心工具 | `tools.go` |

**效果：** 多步工具调用折叠成一次 PTC 执行，中间结果不进上下文。

## 三、Prompt 注入防御（借鉴 Hermes，参考 arxiv 2601.09625）

| 卡点 | 位置 | 策略 |
|------|------|------|
| 工具输出 | `offloadToolResult` | 高危脱敏/低危标记 |
| 记忆召回 | `GetMemoryContext` 调用点 | 高危丢弃/低危保留 |
| 技能加载 | `loadSkillsFromDir` | 高危拒绝加载/低危继续 |

**检测规则：** 10 条高危模式 + 7 条可疑模式 + 零宽字符检测

## 四、Cron/Todo 稳定化（借鉴 Hermes）

| 改动 | 效果 |
|------|------|
| 僵尸任务恢复 | 启动时重置 stuck >10min 的 running 任务 |
| 僵尸检测 goroutine | 每 5 分钟扫描 >30min 的 running 任务 |
| 任务超时 | `context.WithTimeout(5min)` |
| 指数退避重试 | 失败后 1m→2m→4m→8m 退避，用尽重试才标记 failed |

## 五、技能系统进化（借鉴 Hermes Curator）

| 改动 | 文件 |
|------|------|
| 技能指标追踪（评分/使用/去重） | `skill_curator.go` |
| `curate_skills` 工具 | `skill_curator.go` |
| 自动评分算法（频率40+成功率30+新鲜度20+文档10） | `skill_curator.go` |
| 技能加载/使用自动记录 | `skill.go` |
| 每周自进化预设任务 | `todo.go` |

**自进化闭环：** SKILL_HINT → create_skill → curate_skills → 每周自动进化

## 六、借鉴 Claude Code 优化

| 改动 | 效果 |
|------|------|
| 9 段式摘要模板 | compressSummary 升级：会话目标/已完成/未完成/关键决策/代码变更/问题/待验证/用户偏好/关键信息 |
| 工具结果语义去重 | 相同输出自动合并，减少重复 token |
| 上下文滑动窗口 | token 超限时先丢弃旧消息(免费)，够用则跳过 LLM 压缩 |
| 工具字母排序 | `sort.Strings(names)` 保证 Prompt Cache 命中 |
| Git worktree 隔离 | `worktree_add/remove/list`，agent 可在独立分支工作 |

## 七、其他改进

| 改动 | 说明 |
|------|------|
| `max_result_size` 按工具可配 | web_fetch=100K，grep=5K，read_file=无限，默认 8K |
| `runtime_dir` 配置 | opt-in 运行时目录统一（默认保持现有行为） |
| 危险命令检测增强 | shutdown/reboot/eval/nc/curl\|bash/base64 -d |

---

## 提交记录

```
272c51a feat: Agent自进化预设任务 - 每周自动技能策展与知识沉淀
9c8e13d feat: 滑动窗口上下文裁剪(借鉴Claude Code Layer4)
c92a386 feat: 工具定义按字母排序保证Prompt Cache命中(借鉴Claude Code)
7daba51 feat: 借鉴Claude Code - 9段摘要模板 + 语义去重 + worktree隔离
d2eca61 feat: PTC程序化工具调用 + Curator技能进化 + Prompt注入防御 + Cron稳定化
e493a4c feat: 借鉴hermes, 加强web_fetch抓取与内容处理能力
```
