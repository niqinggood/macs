# macs 目标验收

`internal/tools/testing/goal_verify.go` 的 `goal_verify` 复用现有工具注册与权限链，运行选定检查并返回证据。它不单凭命令退出成功推断自然语言任务已完成。

## 参数与结果

- `goal`：待验收目标，必填。
- `check`：tests、build、lint、behavior、all，默认 all；未知值报错。
- `test_cmd`：可选测试命令，未提供时按 Go/Python/Node/Rust 项目选择默认命令。
- `behavior_cmd`：可执行验收断言，失败必须返回非零；behavior/all 未提供时为 PARTIAL。
- `files`：逗号分隔的工作区文件，单独检查存在性。越界、符号链接或 junction 逃逸、目录冒充文件均失败；文件存在不证明内容或行为正确。

报告包含 `VERDICT: PASS|FAIL|PARTIAL` 和 `EVIDENCE`。任一必测检查失败则 FAIL；没有可运行检查、没有已执行测试证据或缺少行为断言则 PARTIAL。PASS 仅覆盖选定检查，仍需核对其覆盖范围与用户目标。Git 状态只作为 INFO，未提交改动不影响验收结果。

默认测试、构建和 lint 命令保留标准错误和失败退出码，不再使用 `|| true` 或吞掉 stderr。缺少工具、缺少 npm 脚本和 lint 非零退出均失败。Node lint 使用 `npx --no-install eslint .`，不为验收临时安装依赖。显式吞错或截断证据的命令复用 `internal/coding` 的检测规则，返回 PARTIAL。

测试检查还要求可识别的已执行测试摘要；Go 的无测试包、`[no tests to run]`、零测试和静默命令不会变成 PASS。混合套件中只要有实际执行的测试就可提供证据。自定义框架如无识别得到的计数，需保留真实测试摘要或提供独立行为验收，不能打印伪造的成功回执。

## 进程生命周期

整个 goal_verify 调用最多 120 秒，Git 子检查最多 5 秒。命令通过 `RunCommandWithContext` 复用 `processutil.ShellCommand` 和 `RunTree`：Windows 使用非交互 PowerShell 并传递原生命令退出码，其他平台使用 sh；取消/超时会终止进程树。命令输出持续排空并最多保留末尾 1 MiB，报告进一步压缩证据。

`run_tests` 同样传递调用方 context；无 context 的旧调用者继续使用兼容包装 `RunCommandWithEnvironment`。独立子 Agent 的 `verify_work` 继续承担目标与交付物的综合核验，不另建执行器。

```powershell
go test ./internal/tools/testing ./internal/processutil -count=1
```

测试覆盖失败退出码、缺失 linter、零测试和混合套件、文件越界/符号链接、脏工作区、取消/超时和输出上限。原误放在 development 包的 Goal Verify 测试已移到被测实现所在的 testing 包。
