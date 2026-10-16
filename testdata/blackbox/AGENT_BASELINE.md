# macs Agent 重复评测基线

`run_agent_baseline.py` 复用 `run_agent_harness100.py` 的统一执行器和专用 Driver，不改写题目、断言或任务状态协议。默认选择原有 common-30 业务黄金集，重复 3 次；`--all` 选择完整 100 题，`--ids` 可选择指定题目。两者不能同时使用。

## 运行

只校验计划，不调用模型、不创建工作区：

```powershell
python testdata/blackbox/run_agent_baseline.py --validate-only
python testdata/blackbox/run_agent_baseline.py --all --repetitions 3 --validate-only
```

实际运行需准备独立测试配置并沿用现有构建入口生成二进制。启动命令是 JSON 字符串数组，必须包含 `{config}`、`{workspace}`、`{port}`；命令路径使用绝对路径，因为每轮工作目录不同。Windows PowerShell 推荐用 `--gateway-command-file` 读取 UTF-8 文件（支持 BOM），避免命令行引号丢失；也支持 `--gateway-command-json` 直接传值，两者互斥。例如从仓库根目录执行：

```powershell
$baselineBinary = (Resolve-Path .local/bin/macs.exe).Path
$baselineCommand = @($baselineBinary, '--config', '{config}', '--path', '{workspace}', '--port', '{port}') | ConvertTo-Json -Compress
$baselineCommand | Set-Content -Encoding UTF8 .local/config/gateway-command.json
python testdata/blackbox/run_agent_baseline.py `
  --gateway-command-file .local/config/gateway-command.json `
  --gateway-config .local/config/agent-baseline.yaml `
  --results .local/agent-baseline/run-01 `
  --model-label '部署的模型名称和版本' `
  --repetitions 3 --max-parallel 2
```

先运行少量案例时可加 `--ids H033,H051,H058`。W29 的浏览器配置沿用 `--browser-command-json`，专用 Driver 的依赖仍需满足。模型标记是操作者提供的元数据，不会修改配置中的模型或证明服务端实际使用了该模型。

每轮启动独立 Gateway、工作区和 SQLite 路径，复用宿主 `/api/status` 检查实际工作区。进程型专用任务仍使用更深一层隔离目录。测试配置应使用本地隔离数据库和测试账号；配置、日志和产物留在 `.local/` 等私有运行目录，不提交到源码仓库。输出目录必须全新，已有目录会被拒绝，以免混入旧证据。

## 指标与证据

根目录输出 `baseline.json` 和 `baseline.md`；每轮的 `trial-NN/results/` 保留原 Driver 的请求、任务快照、产物检查和 `verdict.json`。每完成一轮刷新汇总，中断时也保存已经完成的轮次。

| 指标 | 定义 |
| --- | --- |
| 首次通过率 | 第 1 轮通过题数 / 计划题数 |
| 全部尝试通过率 | 通过次数 /（计划题数 × 计划重复数） |
| 重复全部通过率 | 每次都通过的题数 / 计划题数 |
| 不稳定任务数 | 至少一次通过、但没有全部通过的题数 |
| 耗时 P50 / P95 | 有耗时记录的任务尝试，按 nearest-rank 计算，并记录观测数 |
| Token / 费用 | 现有 Driver 没有统一可信用量合同，保持 null，不估算为零 |

另按能力类别汇总，并列出每题各轮结果。缺失、跳过、超时、启动错误和验收失败都保留在计划分母中。声称 passed 但没有检查证据或包含失败检查的记录标记 invalid_evidence；重复、未知的任务回执拒绝汇总。重复全部通过率是样本观测值，不是推导出的独立概率，也不等于“任意一次成功”的指标。

保存题目、夹具、配置、命令和可直接定位的命令文件 SHA-256，以及 Git commit/dirty 状态、工作区 diff 指纹和 Python/平台信息。每轮前和结束时核对输入指纹；发生变化停止后续轮次并将剩余尝试记为缺失。应使用构建好的二进制以冻结运行时代码，环境变量引用的模型版本、远端服务版本和系统工具环境仍需操作者固定，指纹不能证明这些外部依赖未变。

只有输入指纹一致且每题每次均通过时退出码为 0，其余评测结果为 2；参数错误同样返回非零。`--validate-only` 和单元测试通过只说明定义与执行器可用，不能写成真实 Agent 任务通过率。

## 执行器回归

```powershell
python -m unittest discover -s testdata/blackbox -p 'test_*.py'
```

测试覆盖指标分母、无证据成功、缺失/重复回执、工作区隔离、真实子进程启动失败、启动异常清理、输入变化与拒绝覆盖历史结果。成功任务使用测试替身，不消费模型，也不作为真实模型能力成绩。
