# MACS SDK（本地预览）

这个目录是 MACS 对外集成层的本地开发骨架。它让 LIMS、ERP、CRM、桌面程序和其他 Agent 在不嵌入 MACS 核心源码的情况下调用 MACS。

当前状态：**预览版，尚未拆成独立模块，也未发布。** 目录中的代码暂时继承仓库根目录许可证。确定正式代码托管组织后，再将稳定部分抽成独立 `macs-sdk-go` / `macs-sdk-python` 模块并单独确定许可证。

## 为什么不用动态库

MACS 主程序继续以独立二进制运行，SDK 通过稳定协议调用：

```text
LIMS / ERP / CRM / 客户程序
          |
          |  MACS SDK（HTTP + SSE）
          v
      AgentOS API
          |
          v
   MACS 私有 Agent 运行时
```

这样客户不需要 Go 编译环境，也不会受到 Go `plugin`、Windows DLL、Linux `.so` 和编译器版本 ABI 的耦合。私有 Tool 或外部 Agent 优先作为独立进程，通过 MCP、HTTP、gRPC 或未来的 A2A 适配器连接。

## Go 客户端

当前包位于 `sdk/go/macs`，只使用 Go 标准库，已提供：

- 岗位发现；
- 带幂等键的异步任务提交；
- 任务查询、列表与取消；
- 运行中任务追加指令，以及追加回执的单条查询和列表；
- SSE 任务事件订阅；
- 类型化通用进度，包括阶段、步骤位置、工具、Agent、重试次数和关联 ID；
- `WatchTaskWithOptions` 支持携带 `Last-Event-ID` 续接服务端的有界事件窗口；
- `WaitTask`：事件流断开后自动回查持久任务，直到进入终态。

外部系统不应同步等待一次很长的 HTTP 响应。推荐固定采用三步协议：

1. 提交任务，立即保存服务端返回的 `task_id`；
2. 订阅响应中的 `links.events`，把 `progress` / `task_snapshot` 显示成“正在解析模板、正在映射字段、正在生成文档”等业务进度；
3. SSE 断线或调用方不支持长连接时，按 `links.self` 轮询持久任务快照，直到进入终态。

因此 SSE 是低延迟通知，任务查询接口才是最终事实来源。调用方重试提交时必须复用同一个幂等键，避免重复创建实验报告。

本仓库内部临时导入路径为：

```go
import macs "mac/sdk/go/macs"
```

这个路径不是最终公开 API。正式模块地址确定后会改为类似 `github.com/<owner>/macs-sdk-go/macs` 的可验证路径。

最小调用示例：

```go
client, err := macs.NewClient(
    "http://127.0.0.1:8090",
    macs.WithBearerToken(os.Getenv("MACS_SERVICE_TOKEN")),
)
if err != nil {
    log.Fatal(err)
}

task, err := client.SubmitTask(ctx, macs.SubmitTaskRequest{
    RoleID:  "laboratory-report",
    Message: "根据样品数据生成并填写报告",
    Parameters: map[string]any{
        "sample_id": "S-2026-001",
    },
}, "lims-order-2026-001")
if err != nil {
    log.Fatal(err)
}

// 业务条件变化时，可对仍在 pending/running 的单 Agent 或 Skill Run 任务
// 追加一条持久指令。网络重试必须复用同一个幂等键。
followup, err := client.AddTaskFollowup(
    ctx,
    task.TaskID,
    "改用样品 B，并在结果中保留审计编号",
    "lims-order-2026-001-followup-1",
)
if err != nil {
    log.Fatal(err)
}
log.Printf("followup=%s status=%s", followup.FollowupID, followup.Status)

final, err := client.WaitTask(ctx, task.TaskID, macs.WaitOptions{
    OnProgress: func(progress macs.Progress) error {
        log.Printf("step=%s message=%s", progress.StepName, progress.Message)
        return nil
    },
    OnSnapshot: func(task macs.Task) error {
        log.Printf("status=%s phase=%s", task.Status, task.PhaseLabel)
        return nil
    },
})
```

## Python 客户端

`sdk/python` 提供 Python 3.9+、零第三方运行依赖的 AgentOS 预览客户端，覆盖岗位发现、幂等任务提交、任务查询/列表/取消、SSE 订阅，以及断线后回查持久快照的 `wait_task`。它与 Go 客户端使用同一份 AgentOS HTTP 契约，不嵌入 MACS 运行时。本轮新增的任务追加能力先由服务端和 Go SDK 固化；Python SDK 尚未暴露对应便捷方法，可直接按 OpenAPI 调用 HTTP 端点。

安装和示例见 [`sdk/python/README.md`](./python/README.md)。

## 后续边界

- SDK 只包含协议类型、客户端、校验器和示例；
- Agent 推理循环、工作流学习、复杂模板理解和企业连接器实现不进入 SDK；
- A2A 兼容层应作为 AgentOS 的协议适配器实现，不替换已有的可靠任务存储；
- OpenAPI 仍以 [`docs/openapi-agentos.yaml`](../docs/openapi-agentos.yaml) 为当前事实来源。
