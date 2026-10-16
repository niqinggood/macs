# GitHub 发布前检查

## 1. 本地内容边界

`.local/`、运行数据库、机器配置、QA 预览、生成式 DOCX/PDF、软件著作权申请材料、签名许可证、
发布二进制和远程部署历史不应提交。
这些文件通过 `.gitignore` 隔离；不要使用 `git add -f` 强制加入。根目录的 `备案/`、
`_extract_softcopyright_*/` 和 `memory/` 同样属于本机材料或运行数据。SQLite 的 WAL、SHM
及 journal 文件也不应提交。

整理工作区时，可以把历史申报材料、外部参考项目、临时预览和已生成发行包移到仓库外的
`../openkitty-local-archive/<整理批次>/`，在归档目录保存原路径、目标路径及移动结果清单。
运行中的配置、数据库、用户记忆及正在使用的预览程序保留原位；源码、测试夹具、内嵌资源和
第三方许可证属于项目组成部分，不能按扩展名或是否未跟踪直接移除。

## 2. 执行预检

在仓库根目录运行：

```powershell
powershell -ExecutionPolicy Bypass -File scripts/github/preflight.ps1
```

脚本会检查补丁格式、`.local/` 跟踪状态、GitHub 100 MB 文件上限、Python 语法，
并运行全部 Go 测试。仅在已经单独跑过测试时才使用 `-SkipTests`。

## 3. 检查提交范围

源码仓库应保留实际依赖的 `pkg/codegraph/`、`_lib/`、PDF/OCR 辅助脚本、测试与 `testdata/`、第三方许可 `third_party/`，以及现行使用、API、架构和部署文档。`professional_agents/quant_trader/` 是当前 AI 交易员、投研经理和金信分析师的隔离执行器，属于已有功能。

无引用词库、一次性调研与社区操作脚本、旧申报/手册生成器、历史研发记录和空的 `23-old/`、`reference/` 占位模块不进入上传范围。这些内容移至仓库外保留；若本机临时恢复旧源码目录，应恢复它自己的 `go.mod`，避免进入主模块的包发现范围。

```powershell
git status --short
git diff --stat
git diff
```

当前工作区可能同时存在多个任务的修改。应按功能拆分提交，不要使用不经检查的
`git add .`。

## 4. 凭据与历史

工作区干净不代表 Git 历史安全。公开推送前应使用专门的 secret scanner 检查全部
历史；曾经提交过的密码或 Token 必须先轮换，再使用 `git filter-repo` 等工具清除历史。

以往整理发现一份旧生产部署工作日志包含非占位密码记录，相关本机部署历史现已移至仓库外的
私有归档。如果远端将包含旧提交历史，仍必须执行密码轮换和历史清理。

本仓库已有较长的本地开发历史，并且旧提交中曾包含部署凭据记录。首次建立公开仓库时，默认使用当前已审查
工作树生成无旧历史的快照，不要直接推送现有 `.git` 历史：

```powershell
powershell -ExecutionPolicy Bypass -File scripts/github/new-public-snapshot.ps1 `
  -Destination ..\macs-public -PlanOnly
```

脚本默认只选择仍存在、且未被忽略的已跟踪文件，跳过已删除的旧文件、外部参考快照、
本机运行数据与生成式 QA 产物。未跟踪的新源码、测试和许可证必须逐项审查后，显式通过
`-IncludeUntracked` 加入；不能忽略有效新文件，也不能把全部新增文件视为已审查内容。
`-PlanOnly` 只输出文件及体积计划，不创建目标目录或修改原仓库暂存区。例如：

```powershell
$reviewedNewFiles = @(
  "CONTRIBUTING.md"
  "THIRD-PARTY-NOTICES.md"
  # 按当前工作区补充已经审查的新源码、测试和 third_party 许可文件。
)
& .\scripts\github\new-public-snapshot.ps1 `
  -Destination ..\macs-public -IncludeUntracked $reviewedNewFiles -PlanOnly

# 确认计划完整后移除 -PlanOnly，生成可核对的副本。
& .\scripts\github\new-public-snapshot.ps1 `
  -Destination ..\macs-public -IncludeUntracked $reviewedNewFiles
```

生成步骤在目标目录初始化全新的 `main` 分支和暂存区，不提交、不设置远端、不推送。
原仓库的 index 和历史保持原状；本地挪走旧文件后仍出现 Git 删除记录属于正常现象，
不代表归档文件丢失。随后应在目标目录审查：

```powershell
git status --short
git diff --cached --stat
powershell -ExecutionPolicy Bypass -File scripts/github/preflight.ps1
```

当前发布副本须携带已有 `LICENSE`、`NOTICE`、`COMMERCIAL-LICENSE.md`、`TRADEMARKS.md`、
`BRANDING.md`、`THIRD-PARTY-NOTICES.md` 及 `third_party/`。当前许可证属于源码可见许可；
公开上传不会自动改变授权范围，第三方清单中尚未解决的来源或许可问题仍需处理。
