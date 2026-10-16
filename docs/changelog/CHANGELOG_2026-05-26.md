# OpenKitty 改动记录 — 2026-05-26

## 修复的 Bug

| 问题 | 文件 | 修复 |
|------|------|------|
| 同一秒任务ID碰撞 | gateway.go | `taskID` 改为 `UnixNano()` |
| 进度日志每条重复两次 | mac.go | 去掉 `callLLM` 中的重复 `OnAgentThinking` 广播 |
| 中文截断乱码 `─�` | mac.go | `[]rune` 替代字节切片 |
| CLI每次等持久Shell超时 | cli.go | 移除持久Shell尝试，直用临时执行 |
| `update_plan_step` 无标题 | mac.go | step_id + title + status 完整展示 |
| DuckDuckGo IPv6连接失败 | web_fetch.go | HTTP Transport 强制 `tcp4` |
| 用户气泡路径被转蓝色链接 | web/app.js | 用户消息跳过 storage/ 链接转换 |
| 任务结果偶发不显示 | web/app.js | 2s/5s 两级重试+自动刷新 |
| 加载气泡变水平布局 | web/app.js | 整体重建 loadingHtml，上下结构保留 |
| `removeShell`/`CloseAll` 锁泄漏 | terminal_manager.go | `Close()` 移到锁外执行 |
| Shell `Close()` 永久阻塞 | persistent_shell_common.go | `cmd.Wait()` 加 10 秒超时 |
| Git Bash `--login` 卡住 | persistent_shell_windows.go | 加 `--norc` 跳过用户脚本 |
| config热重载跳过协作变更 | gateway.go | `needUpdateEngine` 加协作配置比较 |
| write_file `path` 参数解析失败 | write.go, listdir_http_listskills.go | 加 `file`→`path` 兜底 |

## 性能优化

| 项目 | 改动 | 效果 |
|------|------|------|
| HTTP超时 | 30s → 15s | 搜索失败快一倍 |
| Dialer超时 | 30s → 5s | Google被墙5s放弃 |
| JS渲染超时 | 30s → 20s | |
| UPX压缩 | Windows/Linux | 47MB → 10MB |
| 系统提示词 | Skills 50→31 | 省 ~120 tokens |

## 功能增强

| 功能 | 文件 |
|------|------|
| 进度日志下载按钮 | web/app.js |
| 进度日志API | gateway.go |
| 进度日志持久化 | gateway.go |
| 搜索工具描述优化(优先bing/sogou) | web_fetch.go |
| CLI权限放开 | config.yaml |
| 加载超时自动刷新(15s按钮/20s自动) | web/app.js |
| 执行日志下载(⏱旁📥) | web/app.js |
| 气泡截断放宽80→200字符 | web/app.js |
| 工具路径显示完整路径 | mac.go |
| 多Agent协作配置支持 | gateway.go |

## Skills 精简

合并: 5PPT→1, 4资讯→2, 3股票→1, 3渠道→1
删除: arxiv, clawhub, health, melodylab, pre-sales, weather, wecom, tmux, desktop-control
新增: server-deploy, health-check, backup-restore, cross-compile, machine-monitor
结果: 50 → 31

## Web UI 优化

- 用户消息不转storage链接
- 任务完成后自动检测结果并重试
- 加载气泡保持上下结构
- 时间/耗时/下载合并一行
- 对话框允许所有非渠道会话输入
- scrollToBottom 加 requestAnimationFrame
- 进度气泡截断 80→200 字符
