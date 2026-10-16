# macs AgentOS 部署与调用入门

个人使用可以直接启动 macs，在页面选择模型并让助手做事。AgentOS 是把同一套助手提供给业务系统调用的企业智能后台：提交任务立即返回任务 ID，随后查询进度，完成后读取结果和交付物。它保存任务并提供用户隔离、审批和恢复。只有要接业务后端或部署服务器时，才需要下面的高级配置。Web「帮助」提供「配置模型、开始对话、个人助手、接入AgentOS（企业智能后台）」四个导航。

企业可以把 macs 部署到自己的服务器，由自己的业务系统直接调用原生 AgentOS 接口，模型服务也由企业自行配置。部署、启动、调试与调用步骤均适用于独立部署。

### AgentOS 与页面模式、部署模式的关系

| 使用方式 | 谁提交请求 | 执行方式 |
| --- | --- | --- |
| 简单问答 | 用户在对话框发送 | 直接调用所选模型回答 |
| Agent 模式 | 用户在对话框发送任务 | Agent 规划、使用工具并交付结果 |
| AgentOS 服务 | 受信业务后端通过 API 提交 | 使用同一 Agent 运行时，任务持久化后排队执行 |

AgentOS 不是另一个模型。原生任务请求没有 `mode: chat` 或 `interaction_mode` 参数。`gateway.agent_os_mode` 是部署分工：默认 `all` 同时接收和执行；`gateway` 仅接收，需要另有 `worker` 执行。拆分部署必须共用可访问的任务数据库并保持岗位配置一致，单机先使用 `all`。`headless` 只控制自动打开浏览器。

先把 macs 和业务后端放在同一台机器上跑通。后台的 `127.0.0.1` 指后台所在机器，不是访问者电脑；云端后端不能凭这个地址连接用户本机的 macs。分开部署需要后台可访问的 HTTPS 入口。

## 个人使用：双击启动，在页面选择模型

### Windows 启动后怎样操作

1. 下载与电脑匹配的发布包，解压后双击 `openkitty.exe`。个人使用发布包无需安装 Go，也无需先手写 YAML。
2. 首次无参数运行可能出现自动安装提示；约 3 秒后默认复制程序到 `%LOCALAPPDATA%\openkitty\bin`，尝试加入用户 PATH 并创建桌面快捷方式。输入 `n` 可跳过安装，仍继续启动；安装失败也会继续尝试运行。当前兼容快捷方式名为 `MACS`。
3. 程序通常自动打开浏览器。若没有打开，查看启动窗口里的「Web 页面」地址，复制到浏览器。默认端口常为 `8080`，被占用时可能换到其他端口；以实际日志为准。
4. 在左下角登录平台账号，然后点输入框附近的「选择模型」，选择带「平台」标签的文字模型。也可打开「系统设置 → AI 服务 → 自定义模型」，填写自己的服务地址、模型 ID 和 API Key，点击「保存并应用」。本地无认证模型按服务要求留空密钥。
5. 在「系统设置 → 我的助手」选择默认助手。助手的模型默认为「跟随当前选择的模型」；需要固定模型时编辑该助手，单独选定一个已配置的模型并保存。
6. 回到对话框：选 Agent 模式让助手使用工具处理任务；只想直接提问时选简单问答。发送请求后才会按实际使用的模型计费。

启动窗口应保持运行；关闭运行中的程序会停止本机服务。设置里的「启动时不打开网页」只影响启动时是否自动打开浏览器，网页与 API 仍可访问，不是开启 AgentOS 的开关。

配置读取顺序是：命令行 `--config` 指定的文件、当前工作目录里的 `openkitty-config.yaml`、当前目录 `.openkitty/openkitty-config.yaml`、用户目录 `.openkitty/openkitty-config.yaml`。都没有时，会从内嵌模板生成用户目录中的配置，再启动可修复的 Web 设置页。首次双击与之后从桌面快捷方式运行的工作目录可能不同；查看启动日志确认实际使用的配置与工作目录，避免误以为文件会随程序移动。

### 平台网页登录与独立服务的区别

**在页面聊天**时，平台模型可以使用本次提交者的登录账号，无需用户手填模型 API Key，费用按该账号扣除。登录凭证不会写入模型配置，也不会借给其他渠道或重启后恢复的任务。

**通过 AgentOS API、聊天渠道或无人值守服务调用**时，不能只依靠左下角已登录。模型要有可独立使用的凭据：自己的模型 API Key，或平台账号的访问密钥；已运行的本地无认证模型可按其规则免密钥使用。平台模型可在「系统设置 → AI 服务 → 编辑模型 → 更多模型选项」勾选「使用账号访问密钥（供脚本或第三方接入）」，填写平台用户中心积分钱包提供的本人密钥，再保存。它对应的调用会从密钥所属账号扣费；它与下文业务服务的接入凭证各有用途。

输入框中的文字模型选择会保存为 `default_model`。未单独指定模型的助手会继承它；AgentOS 接收新任务时会把当时的实际模型冻结到岗位版本中。之后切换当前模型不改变已经接收的任务，单独指定模型的助手始终优先使用自己的模型。图片、音乐或视频生成的临时选择不等于 AgentOS 助手的文字模型配置。

在某个 PowerShell 窗口里执行 `$env:MACS_MODEL_API_KEY = "…"` 只设置那个窗口及其启动子进程的临时环境。从资源管理器双击程序不会获得这次临时设置。个人使用可直接在 Web 设置中配置模型；采用环境变量部署时，应从设置了变量的同一终端启动，或由服务器的服务管理器提供环境变量。

### 可选：检查本机 API 是否就绪

普通聊天不需要先运行调试脚本。要确认本机服务和可用岗位，可以从 Web「帮助 → 接入 AgentOS → 调试」下载 `agentos_check.py`，安装 Python 3.9 或更新版本后运行：

```text
python agentos_check.py --local --url http://127.0.0.1:8080
```

把 `8080` 换成启动日志里的实际 Web 端口；如果已配置独立 AgentOS 端口，就使用那个端口。默认没有独立监听地址时，任务 API 与 Web 共用本机端口，也没有单独的「开启 AgentOS」按钮。

`--local` 仅接受本机地址；只查询健康状态和岗位，不调用模型或扣费。本机兼容无认证模式不需业务凭证；已配置认证时仍需有效凭证，不能用该参数跳过认证。追加 `--submit` 才会提交真实任务测试，并可能产生模型费用；测试所用模型需要前一节说明的独立凭据。

当前设置页面可以配置模型、助手、主 Web 监听地址和启动行为；尚未提供 AgentOS 独立端口、发布岗位和业务服务凭证的完整图形化管理。需要其他软件或其他电脑接入时，继续下面的高级部署，并由维护人员配置这些字段。

## 1. 高级部署：准备程序与配置

以下面向业务后端和服务器部署。示例使用两个入口：`127.0.0.1:8080/web/` 是管理界面，`127.0.0.1:8081` 是 AgentOS 数据面。以自己的配置和实际监听日志为准。

使用与操作系统、CPU 架构匹配的发布包，解压到固定目录。Windows 程序名为 `openkitty.exe`，Linux/macOS 为 `openkitty`。使用发布包无需安装 Go。

从源码运行时需要 Go 1.26。在仓库根目录执行：

```text
go run ./cmd/releaseguard
go build -buildvcs=false -trimpath -o openkitty.exe ./cmd/openkitty
```

Linux/macOS 将输出名改为 `-o openkitty`。Web 资源嵌入程序，修改 `web/` 后需重新构建。正式发布包使用 `scripts/build/` 中的构建流程；以上命令仅用于本机源码构建。

首次部署创建 UTF-8 的 `openkitty-config.yaml`，或复制包内模板后编辑。下面是单模型、单助手、单调用方的最小结构。已有配置按字段合并，避免重复的 `gateway`、`agents` 或 `model_cfg`。

```yaml
headless: true
default_agent: ServiceAssistant
default_model: service-model
model_cfg:
  service-model:
    type: openai
    base_url: "https://your-model-provider.example/v1"
    model_name: REPLACE_WITH_MODEL_NAME
    api_key_env: MACS_MODEL_API_KEY
    max_tokens: 4096
    temperature: 0.2
agents:
  - name: ServiceAssistant
    type: custom
    model: service-model
    system_message: "按用户要求整理工作摘要，清楚说明结果。"
gateway:
  enabled: true
  enable_webui: true
  listen_addr: "127.0.0.1:8080"
  agent_os_listen_addr: "127.0.0.1:8081"
  agent_os_mode: all
  max_tasks: 2
  max_queued_tasks: 100
  task_timeout: 300
service_roles:
  - id: document-assistant
    agent_name: ServiceAssistant
    description: 整理文档并返回结果
    enabled: true
    allowed_tenants: [team-a]
    allowed_callers: [business-app]
tenants:
  - id: team-a
    name: 示例团队
    token: REPLACE_WITH_LONG_RANDOM_TENANT_TOKEN
service_clients:
  - id: business-app
    tenant_id: team-a
    token: REPLACE_WITH_LONG_RANDOM_SERVICE_TOKEN
    enabled: true
    allowed_roles: [document-assistant]
    scopes: ["tasks:submit", "tasks:read:own", "tasks:cancel:own"]
```

替换模型服务商的地址和模型名；以上 `type: openai` 用于兼容 OpenAI 格式的服务，其他模型类型沿用对应的原配置。启动时从 `MACS_MODEL_API_KEY` 环境变量读取模型密钥。平台服务可填平台提供的 `/api/macs/v1` 地址、实际 `platform:…` 文字模型 ID，并把平台账号访问密钥作为模型密钥；服务器不会沿用浏览器登录。

已有配置可复用现有模型与助手。示例明确给 `ServiceAssistant` 绑定 `service-model`，便于服务使用固定模型；希望它跟随当前文字模型时可将 `agents[].model` 留空，保留正确的 `default_model`。无需为单机接入另建团队时，`tenant_id` 和 `allowed_tenants` 可使用内置 `default`，不添加示例 `team-a` 的租户管理凭证；对外服务仍需要自己的 `service_clients[].token` 和岗位授权。

三个凭证分别承担不同职责：

| 凭证 | 用途 | 放置位置 |
| --- | --- | --- |
| 模型 API Key | macs 调用模型 | macs 启动环境，由 `api_key_env` 引用 |
| `tenants[].token` | 租户管理凭证 | 受保护的 macs 配置 |
| `service_clients[].token` | 业务后端调用岗位 | macs 配置，以及受信业务后端的密钥配置 |

配置中的两个 `token` 应使用不同的随机长值；业务凭证至少 24 个字符，建议随机生成 32 字节。PowerShell 每运行一次生成一个凭证，运行两次分别填入配置：

```powershell
$bytes = New-Object byte[] 32
$rng = [Security.Cryptography.RandomNumberGenerator]::Create()
$rng.GetBytes($bytes)
[Convert]::ToBase64String($bytes)
$rng.Dispose()
```

Linux/macOS 可运行两次 `openssl rand -hex 32`。这些服务凭证直接填写到受保护的配置文件，不使用模型密钥代替，也不下发给浏览器。

`agents` 是执行助手；`service_roles` 是对外岗位，`agent_name` 必须精确匹配助手名称；`service_clients` 绑定租户、允许调用的岗位和权限。已有助手可以保留，只需把岗位绑定改为其实际名称。

无 MySQL 配置时默认使用 SQLite，无需额外数据库服务。指定 `--path ./workspace` 后，默认数据库位于 `workspace/.openkitty/openkitty.db`，除非另设 `OPENKITTY_SQLITE_PATH`。持久保存整个工作目录及配置；已有 MySQL 配置时继续使用 MySQL。

## 2. 高级启动：先在本机运行

以下命令均在程序和配置所在目录执行。

Windows / PowerShell：

```powershell
$env:MACS_MODEL_API_KEY = "REPLACE_WITH_YOUR_MODEL_API_KEY"
$env:OPENKITTY_FIXED_PORT = "true"
New-Item -ItemType Directory -Force .\workspace | Out-Null
.\openkitty.exe --config .\openkitty-config.yaml --path .\workspace
```

Linux / macOS：

```bash
chmod +x ./openkitty
mkdir -p ./workspace
export MACS_MODEL_API_KEY='REPLACE_WITH_YOUR_MODEL_API_KEY'
export OPENKITTY_FIXED_PORT=true
./openkitty --config ./openkitty-config.yaml --path ./workspace
```

保留终端查看日志，按 Ctrl+C 停止服务。`headless: true` 只关闭自动打开浏览器，Web 和 API 仍可使用。`OPENKITTY_FIXED_PORT=true` 避免主 Web 端口占用时自动换端口；AgentOS 独立监听失败会报错。

启动后打开 `http://127.0.0.1:8080/web/` 管理助手。日志中应出现：

```text
[AgentOS] dedicated data plane listening on 127.0.0.1:8081
```

### Linux 服务器常驻运行

先在服务器按前面的命令跑通。程序和配置放在 `/opt/macs`，然后准备专用用户、工作目录和密钥文件。已有 `macs` 用户时跳过第一行：

```bash
sudo useradd --system --user-group --home-dir /opt/macs/workspace --shell /usr/sbin/nologin macs
sudo install -d -o macs -g macs -m 750 /opt/macs/workspace
sudo touch /etc/macs-agentos.env
sudo chmod 600 /etc/macs-agentos.env
sudo nano /etc/macs-agentos.env
```

程序须可执行，配置须可被 `macs` 用户读取；工作目录由该用户写入。模型密钥文件内容：

```text
MACS_MODEL_API_KEY=REPLACE_WITH_YOUR_MODEL_API_KEY
```

创建 `/etc/systemd/system/macs-agentos.service`：

```ini
[Unit]
Description=macs AgentOS service
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=macs
WorkingDirectory=/opt/macs/workspace
Environment=HOME=/opt/macs/workspace
EnvironmentFile=/etc/macs-agentos.env
Environment=OPENKITTY_FIXED_PORT=true
ExecStart=/opt/macs/openkitty --config /opt/macs/openkitty-config.yaml --path /opt/macs/workspace
Restart=on-failure
RestartSec=5
TimeoutStopSec=60

[Install]
WantedBy=multi-user.target
```

```bash
sudo systemctl daemon-reload
sudo systemctl enable --now macs-agentos
sudo systemctl status macs-agentos
sudo journalctl -u macs-agentos -f
```

同机反向代理可以继续连接 `127.0.0.1:8081`，由代理提供 HTTPS。内网直接访问时，将 `gateway.agent_os_listen_addr` 改成 `0.0.0.0:8081` 并限制访问来源。Web 管理端口继续保留在本机，必要时通过受控通道访问。非回环数据面必须配置认证，示例中的服务客户端已提供认证。

代理保留 `Authorization`、`Idempotency-Key` 和用户身份请求头；SSE 关闭缓冲，并设置足够的读取超时。业务后端的基础地址改为服务器地址。监听地址变更后重启服务；升级和重启继续使用相同的工作目录、数据库与配置。

## 3. 高级调试：健康检查与岗位发现

### Python 调试：下载一个脚本，按提示填写

先按前面的启动步骤运行 macs。打开 Web「帮助 → 接入 AgentOS → 调试」，点击「下载 Python 调试脚本」，得到 `agentos_check.py`。源码也在 [`web/agentos_check.py`](../web/agentos_check.py)。需要 Python 3.9 或更新版本，无需安装第三方库。

在下载文件所在文件夹打开终端，运行：

```text
python --version
python agentos_check.py
```

Windows 也可使用 `py`，Linux/macOS 常用 `python3`。如果直接使用仓库中的源码，在仓库根目录运行 `python web/agentos_check.py`。

按提示填入两个值：

- **服务地址**：查看配置的 `gateway.agent_os_listen_addr`。本指南示例是 `127.0.0.1:8081`，输入时写成 `http://127.0.0.1:8081`。另一台电脑运行脚本时使用 macs 服务器地址。
- **业务服务凭证**：在 `openkitty-config.yaml` 的 `service_clients` 找到 `id: business-app`，复制该项的实际 `token` 值。不要使用 `REPLACE_WITH_…` 占位符；模型 API Key 和网站登录密码不能代替此凭证。凭证输入时不会显示字符，粘贴后按回车。

可选环境变量 `MACS_AGENTOS_URL` 和 `MACS_SERVICE_TOKEN` 会代替交互输入；脚本不打印凭证。首次健康检查及岗位发现读取服务状态和配置，不调用模型。

成功输出类似：

```text
[1/3] 服务已启动
[2/3] 服务已就绪
[3/3] 可用岗位：
  - document-assistant 整理文档并返回结果
检查通过：服务地址和凭证可用。
```

要验证模型能回答，执行完整任务测试：

```text
python agentos_check.py --submit
```

这条命令会调用所配置的模型，按平台或服务商规则计费。脚本提交一条短摘要任务，输出幂等编号和任务 ID，每隔 2 秒查询一次，最多等待 300 秒。`completed` 时输出结果和「模型测试成功」；`degraded`、`failed`、`stopped` 返回失败，`waiting_approval` 提示交给审批人员并停止本地等待。

常用参数：

| 参数 | 用途 |
| --- | --- |
| `--url http://127.0.0.1:8081` | 指定服务地址 |
| `--role document-assistant` | 指定测试岗位 ID；更换为岗位列表中的实际值 |
| `--user-id person-42` | 凭证启用 `require_user_id` 时指定同一自然人身份 |
| `--task 返回的任务ID` | 继续查询已有任务，不创建新任务 |
| `--idempotency-key 原提交编号` | 网络中断后重试同一 POST；同时保持原 `--role` 和身份 |
| `--wait-timeout 60` | 设置本地等待时间 |

等待超时或手动中断不会取消服务端任务，保存任务 ID 再查询。脚本不会自动重发结果不确定的提交。HTTP 错误给出中文检查项及服务端 `request_id`；不会跟随重定向向其他地址转发凭证。

### PowerShell 直接检查接口

已有接口调试经验时，可另开 PowerShell 终端，填入同一个业务服务凭证：

```powershell
$agentBase = "http://127.0.0.1:8081"
$env:MACS_SERVICE_TOKEN = "REPLACE_WITH_LONG_RANDOM_SERVICE_TOKEN"
$headers = @{ Authorization = "Bearer $env:MACS_SERVICE_TOKEN" }
Invoke-RestMethod "$agentBase/livez"
Invoke-RestMethod "$agentBase/readyz"
Invoke-RestMethod "$agentBase/api/v1/agent/roles" -Headers $headers
```

Linux / macOS：

```bash
export MACS_SERVICE_TOKEN='REPLACE_WITH_LONG_RANDOM_SERVICE_TOKEN'
curl -fsS http://127.0.0.1:8081/livez
curl -fsS http://127.0.0.1:8081/readyz
curl -fsS -H "Authorization: Bearer $MACS_SERVICE_TOKEN" \
  http://127.0.0.1:8081/api/v1/agent/roles
```

预期：`/livez` 返回 `status: alive`，`/readyz` 返回 `status: ready`，岗位列表出现 `document-assistant`。健康检查验证服务、数据库和停机状态；实际任务才验证模型和助手能力。

| 症状 | 检查项 |
| --- | --- |
| 无法连接 / 端口被占用 | 启动日志、监听地址、防火墙与代理。`--port` 只修改主 Web 端口，AgentOS 使用 `agent_os_listen_addr` |
| 401 | 业务服务凭证、`Authorization: Bearer …`、客户端是否启用、代理是否保留认证头 |
| 403 / 岗位列表为空 | `allowed_roles`、`allowed_tenants`、`allowed_callers`、岗位启用状态和 Scopes |
| 404 | 独立数据面端口、任务 ID、提交和查询是否使用同一身份 |
| 400 / 409 | 响应 `code`、`message`；岗位、输入、版本，是否用同一幂等键提交了不同内容 |
| 长期 pending | 单机模式是否为 `agent_os_mode: all`、执行进程和队列容量；`gateway` 模式需要另外运行 Worker |
| waiting_approval | 由具备 `approvals:read`、`approvals:decide` 的人员核对并处理 |
| `/readyz` 返回 503 | 数据库可用性、服务是否正在停止 |
| failed / degraded | 任务的 `error`、`result` 与服务日志；模型地址、密钥、额度、工具权限及超时 |

添加 `X-Request-ID` 便于关联日志；反馈时保留 `request_id` 和任务 ID，隐藏凭证。Linux 常驻服务使用 `journalctl -u macs-agentos -f` 看日志，队列指标可查看 `/metrics`。

## 4. 业务调用：提交、等待、读取结果

真实任务会调用配置的模型，按平台或模型服务商规则计费。先用一条简短摘要检查完整流程。

Python 用户可直接使用上一节的 `python agentos_check.py --submit`。业务项目的岗位发现、任务提交、查询、等待和 SSE 接入可以复用 [`sdk/python/`](../sdk/python/README.md) 的客户端；下载的调试脚本用于验证服务配置。

### PowerShell 完整示例

沿用上一节的 `$agentBase` 和 `$headers`：

```powershell
$requestHeaders = $headers.Clone()
$requestHeaders["Idempotency-Key"] = "summary-" + [guid]::NewGuid().ToString("N")
$requestHeaders["X-Request-ID"] = "check-" + [guid]::NewGuid().ToString("N")
$body = @{
  role_id = "document-assistant"
  message = "将以下内容整理为工作摘要：今天完成了需求讨论与接口设计。"
} | ConvertTo-Json
$task = Invoke-RestMethod -Method Post -Uri "$agentBase/api/v1/agent/tasks" `
  -Headers $requestHeaders -ContentType "application/json; charset=utf-8" `
  -Body ([Text.Encoding]::UTF8.GetBytes($body)) -TimeoutSec 30
$task.task_id
$taskUrl = $agentBase.TrimEnd('/') + $task.links.self
$deadline = [DateTime]::UtcNow.AddSeconds(300)
do {
  $snapshot = Invoke-RestMethod $taskUrl -Headers $headers -TimeoutSec 30
  $snapshot.status
  if ($snapshot.status -eq "waiting_approval") {
    Write-Host "任务需要审批，请交给有权限的人员处理。"
    break
  }
  if ($snapshot.status -in @("completed", "degraded", "failed", "stopped")) { break }
  if ([DateTime]::UtcNow -ge $deadline) {
    throw "等待超时，任务仍可能运行。请继续查询 $taskUrl"
  }
  Start-Sleep -Seconds 2
} while ($true)
$snapshot | Select-Object task_id, status, result, error, artifacts | ConvertTo-Json -Depth 20
```

本地等待超时或出现审批状态后，任务仍存在；稍后继续查询同一个 `$taskUrl`，或明确发送取消请求。网络中断后重试 POST 时保留原 `$requestHeaders` 和 `$body`，不要重新生成幂等键。

### Linux / macOS 使用 curl

沿用调试步骤的 `MACS_SERVICE_TOKEN`：

```bash
cat > task.json <<'JSON'
{
  "role_id": "document-assistant",
  "message": "将以下内容整理为工作摘要：今天完成了需求讨论与接口设计。"
}
JSON
curl -i --max-time 30 -X POST http://127.0.0.1:8081/api/v1/agent/tasks \
  -H "Authorization: Bearer $MACS_SERVICE_TOKEN" \
  -H 'Content-Type: application/json; charset=utf-8' \
  -H 'Idempotency-Key: summary-check-001' --data-binary @task.json
curl -fsS --max-time 30 -H "Authorization: Bearer $MACS_SERVICE_TOKEN" \
  http://127.0.0.1:8081/api/v1/agent/tasks/REPLACE_WITH_RETURNED_TASK_ID
```

把返回的真实任务 ID 填入最后一条命令。每隔 2 秒查询一次，直到终态；为等待设置业务超时。示例幂等键同内容重试可以复用，另一项工作要换新键。

新任务返回 `202 + task_id + links`，只表示已接收；相同身份、相同幂等键和内容重放返回原任务及 `200`。状态含义：

| 状态 | 含义 | 后续动作 |
| --- | --- | --- |
| pending | 排队 | 继续查询 |
| running | 执行 | 继续查询或订阅进度 |
| waiting_approval | 等待审批 | 交给有权限的操作员处理 |
| completed | 完成 | 读取结果和交付物 |
| degraded | 结果有降级，终态 | 核对结果、错误和任务说明 |
| failed | 失败，终态 | 读取错误并排查 |
| stopped | 停止，终态 | 按停止处理 |

读取最终快照的 `result` 和 `artifacts`；有交付物时使用响应中的下载地址，下载继续携带业务凭证和必要的用户身份头。不要把所有终态都当成成功。

## 5. 实时进度、多用户、Skill 与审批

- 用返回的 `links.events` 订阅 SSE。断线或收到 `replay_reset` 后回查 `links.self`。`Last-Event-ID` 只覆盖当前实例的近期事件，最终状态以持久快照为准。
- 多用户业务客户端配置 `users:act_as` 和 `require_user_id: true`。提交、查询、SSE、取消、审批及文件下载均携带稳定的 `X-OpenKitty-User-ID`；提交体若有 `user_id`，必须与 Header 一致。身份来自业务后端的登录态，服务凭证留在后端。
- 显式 Skill 使用 `skill_run: {"skill_name": "已安装的技能名", "parameters": {}}`，参数按 Skill 合同填写。同一请求不能同时使用 `skill_run` 和 `collaboration`。
- 审批单独配置 `approvals:read`、`approvals:decide` 的操作员凭证。查询任务 `/approvals` 得到 `approval_id`，再 POST 到 `/approvals/{approval_id}/approve` 或 `/deny`。上述普通任务客户端没有这些审批权限。

完整接口、错误、幂等和恢复语义见 [AgentOS 异步任务 API](./agent-os-api.md)；岗位与企业授权见 [企业 Agent 服务调用](./enterprise-agent-service.md)；机器可读契约为 [OpenAPI](./openapi-agentos.yaml)，客户端在 `sdk/python/`、`sdk/go/macs/`。
