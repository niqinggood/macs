# MACS AgentOS Python SDK（预览）

这是 MACS AgentOS Data API 的纯 Python 客户端。它只封装 HTTP、SSE、鉴权、幂等提交和断线轮询，不包含或暴露 MACS 的 Agent 运行时。

当前为仓库内预览版，继承仓库根目录许可证，尚未发布到 PyPI。支持 Python 3.9 及以上，不依赖第三方运行库。

## 安装

从 MACS 仓库根目录执行：

```bash
python -m pip install ./sdk/python
```

开发时也可使用：

```bash
python -m pip install -e ./sdk/python
```

## 最小调用

```python
import os

from macs_agentos import Client

client = Client(
    "http://127.0.0.1:8090",
    bearer_token=os.environ["MACS_SERVICE_TOKEN"],
)

task = client.submit_task(
    {
        "role_id": "customer-research",
        "message": "分析客户资料并给出三条跟进建议",
        "parameters": {"customer_id": "42"},
    },
    idempotency_key="crm-customer-42-v1",
)

final = client.wait_task(
    task["task_id"],
    timeout=30 * 60,
    on_progress=lambda event: print(event.get("message", "")),
)
print(final["status"], final.get("artifacts", []))
```

如果服务客户端配置了 `require_user_id: true`，调用方还要设置稳定的业务用户：

```python
client = Client(
    "https://macs.example.com",
    bearer_token=os.environ["MACS_SERVICE_TOKEN"],
    act_as_user_id="person-42",
)
```

不要把服务 Token 发给浏览器或终端用户，也不要在代码、日志或部署包里写死真实凭据。

## 行为约定

- `submit_task` 立即返回 `202` 对应的任务快照；网络不确定时必须复用同一个 `idempotency_key`。
- `watch_task` 只消费低延迟 SSE 通知，支持 `client_id`、`last_event_id` 和流总超时。
- `wait_task` 在 SSE 断开或格式异常后自动轮询任务快照；最终结果始终以 `GET /api/v1/agent/tasks/{task_id}` 为准。
- `list_approvals`、`approve_task` 和 `deny_task` 覆盖需要人工确认的工具动作；审批权限仍由服务端 Scope 和岗位边界决定。
- `APIError` 保留服务端的 HTTP 状态、稳定错误码、请求 ID、可重试标志和 `Retry-After`。
- SDK 返回字典而不是封闭模型，服务端增加兼容字段时旧客户端仍可读取原始结果。

完整可运行示例见 [`examples/submit_and_wait.py`](./examples/submit_and_wait.py)。

## 本地验证

无需连接真实 MACS 服务：

```bash
PYTHONPATH=./sdk/python/src python -m unittest discover -s ./sdk/python/tests -v
```

Windows PowerShell：

```powershell
$env:PYTHONPATH = ".\sdk\python\src"
python -m unittest discover -s .\sdk\python\tests -v
```
