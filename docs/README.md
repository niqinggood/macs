# macs 文档导航

面向使用者的入口是 [macs 使用手册](macs-user-manual.md)。macs 是正式名称，openkitty 是产品小名。

项目介绍提供[简体中文](../README.md)与[English](../README.en.md)版本。核心优势、与其他 Agent 产品的比较及证据边界见[产品定位与比较](product-positioning.md)（[English](product-positioning.en.md)）。

查看实际操作界面与 Word 生成过程：[界面演示图集](showcase.md)（[English](showcase.en.md)）。

初次阅读源码建议先看[核心代码导读](architecture/code-walkthrough.md)（[English](architecture/code-walkthrough.en.md)），按启动、请求、模型与工具循环、任务恢复、记忆和流程复用的顺序跟踪；完整细节再查 [`internal/README.md`](../internal/README.md)。

手册覆盖首次启动、模型与智能体配置、任务执行、记忆管理、知识库、文档自动化、AI 员工、任务流、外部接入、备份与常见问题。

版权申报字段、源码留档、第三方材料和贡献授权见 [macs 版权材料与发布说明](legal/README.md)。

把 macs 部署为业务系统可调用的服务，先看 [AgentOS 部署与调用入门](agentos-quickstart.md)，包含启动命令、健康检查、错误排查与完整调用示例。

仓库根目录只保留项目入口文件。其余文档按用途归档：

- `architecture/`：AgentOS、Agent、Pipeline、能力边界与技术架构。
- `engineering/`：能力评测方案与仍被架构说明引用的工程审阅。
- `changelog/`：按日期保存的历史变更记录。
- `troubleshooting/`：问题排查和操作经验。

旧研发日志、阶段记录、过期原型、旧说明书及申报材料生成脚本保存在仓库外的本机归档中，不随源码上传。现行用户手册、API、架构、部署、评测依据和许可证说明保留在仓库中。

准备公开仓库时，请先阅读 [`github-publishing.md`](github-publishing.md)。

当前产品入口仍为仓库根目录的 `README.md`，规划入口为 `ROADMAP.md`。

Go 源码模块化的当前边界与迁移顺序见 [`architecture/go-modularization.md`](architecture/go-modularization.md)。

Agent 赛题选择、30 题业务黄金集、公开榜单接入及评分方法见 [macs Agent 能力评估方案](engineering/agent-capability-evaluation-plan.md)。

Agent 模式的偏好、习惯、历史经验、后台归纳与 Obsidian 接入设计见 [macs 记忆系统完整升级方案](architecture/memory-system-upgrade-plan.md)；简单问答不接入长期记忆。

当前记忆写入、工具统计、相似历史检索、prompt 召回与实际缺口见 [记忆逻辑审阅材料](architecture/memory-logic-review.md)。

原生 Web 的界面、对话标题、帮助分区与导出规则见 [macs Web 视觉与帮助文档](architecture/web-ui.md)。

轻量 AI 应用的 graph、预算、幂等、租约与崩溃恢复合同见 [`llm-app-runtime.md`](llm-app-runtime.md)。

面向企业/LIMS 的 DOCX 模板检查、字段自动映射、重复表格行和严格填写流程见 [`architecture/document-automation.md`](architecture/document-automation.md)。

## GitHub 发布边界

`.local/` 保存本机 QA、部署历史、预览、二进制、私有记录和参考素材，已被
`.gitignore` 排除。不要把 `.local/` 中的内容手工强制提交；需要公开的内容应先
去除凭据、机器地址和运行数据，再迁入对应的正式目录。

执行权限、计划模式与减少重复确认的规则见 [`architecture/execution-permissions.md`](architecture/execution-permissions.md)。
