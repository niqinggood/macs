# OpenKitty MCP 与 VS Code 扩展帮助

本文说明 OpenKitty 的 MCP Client 能力、VS Code 私有扩展安装与使用方式，以及
常见问题和验证方法。

## 一、MCP Client

OpenKitty 已通过官方 Go SDK 接入 MCP。连接后的第三方 MCP 工具会进入普通
`ToolRegistry`，所有 OpenKitty Agent 均可像调用内置工具一样调用它们。

### 支持的传输

| 配置值 | 用途 |
|---|---|
| `stdio` | 启动本地 MCP Server，通过 stdin/stdout 通信 |
| `streamable_http` | 使用现代 MCP Streamable HTTP |
| `sse` | 使用 MCP 2024-11-05 传统 HTTP+SSE |
| `auto` | 先尝试 Streamable HTTP，遇到兼容的 4xx 响应后降级到传统 SSE |

当前客户端支持协商至 MCP `2025-11-25`，实现依据：

- [MCP Transport 规范](https://modelcontextprotocol.io/specification/2025-11-25/basic/transports)
- [MCP 官方 Go SDK](https://github.com/modelcontextprotocol/go-sdk)

### 已支持的协议能力

- Initialize 生命周期与协议版本协商。
- stdio、JSON HTTP 响应及 SSE 响应。
- `MCP-Session-Id` 与 `MCP-Protocol-Version` 会话头。
- Bearer Token 和自定义 HTTP Header。
- Tool、Resource 分页读取。
- Tool 调用、Resource 读取和 Ping。
- stdio 子进程环境继承、工作目录与优雅关闭。
- `notifications/tools/list_changed` 动态工具更新。
- 动态同步已经绑定的 Agent `ToolRegistry`。
- 多 MCP Server 工具重名保护。
- 仅提供 Resource、不提供 Tool 的 MCP Server 也会保持连接。

### stdio 配置示例

```yaml
mcp_servers:
  - name: filesystem
    enabled: true
    transport: stdio
    command: npx
    args:
      - -y
      - "@modelcontextprotocol/server-filesystem"
      - .
    env:
      - LOG_LEVEL=info
    cwd: E:\projects\example
    timeout_seconds: 30
```

连接成功后，MCP Server 中名为 `read_document` 的工具默认会注册为：

```text
mcp_read_document
```

### 远程 MCP 配置示例

```yaml
mcp_servers:
  - name: company-tools
    enabled: true
    transport: auto
    url: https://mcp.example.com/mcp
    token: ${MCP_TOKEN}
    headers:
      X-Tenant-ID: tenant-42
    tool_prefix: company
    timeout_seconds: 30
```

配置 `tool_prefix: company` 后，远程的 `search` 工具会注册为：

```text
mcp_company_search
```

`token` 会以以下 Header 发送：

```http
Authorization: Bearer <token>
```

不要把真实 Token 提交到 Git。应使用 OpenKitty 已有的配置变量或密钥注入方式。

### 工具重名规则

如果不同 MCP Server 都提供 `search`：

```text
mcp_search
mcp_second-server_search
```

OpenKitty 会保留第一个工具，并为后续冲突工具增加 Server 名称，不会静默覆盖。

以下名称由 OpenKitty 保留：

```text
mcp_list_resources
mcp_read_resource
```

第三方 MCP Server 出现同名工具时，会自动使用 Server 前缀。

### Resource 使用

Agent 可以调用：

```text
mcp_list_resources
mcp_read_resource
```

即使 MCP Server 没有 Tool、只有 Resource，也会保持连接并出现在 Resource
查询中。

## 二、VS Code 私有扩展

OpenKitty 不需要开源，也不需要修改、fork 或重新编译 VS Code。标准 VS Code
Extension 可以采用私有许可证，并通过 VSIX 或企业私有市场发布。

当前扩展是一个薄客户端：核心 Agent、授权、模型、工具与业务逻辑继续运行在
OpenKitty 服务端。

相关官方资料：

- [VS Code Chat Participant API](https://code.visualstudio.com/api/extension-guides/ai/chat)
- [VS Code VSIX 打包与发布](https://code.visualstudio.com/api/working-with-extensions/publishing-extension)
- [VS Code 企业扩展与 Private Marketplace](https://code.visualstudio.com/docs/enterprise/extensions)

### 扩展能力

- Activity Bar 中的 OpenKitty 侧边栏。
- 实时加载 AI 员工、状态、职责和 Pipeline。
- 选择 AI 员工并提交任务。
- 查看最终结果并停止正在执行的任务。
- 自动携带当前 Workspace、活动文件、编辑器选区和诊断。
- VS Code Chat 中使用 `@openkitty`。
- 命令面板和编辑器右键调用 AI 员工。
- API Key 使用 VS Code `SecretStorage` 保存。
- 支持任务轮询、超时和取消。

### 安装

扩展安装包：

```text
E:\openkitty\dist\openkitty-vscode-0.1.0.vsix
```

命令行安装：

```powershell
code --install-extension E:\openkitty\dist\openkitty-vscode-0.1.0.vsix
```

也可以在 VS Code 中执行：

```text
Extensions: Install from VSIX...
```

然后选择上述 VSIX 文件。

当前安装包校验信息：

```text
版本：0.1.0
扩展 ID：openkitty.openkitty
SHA-256：4F197153D03600D4FF93055BF1E577368F690A9E5760DA6B9292E78AB121740D
```

### 基础设置

打开 VS Code Settings，搜索 `OpenKitty`。

主要配置：

| 设置 | 默认值 | 说明 |
|---|---:|---|
| `openkitty.baseUrl` | `http://127.0.0.1:8090` | OpenKitty Gateway 地址 |
| `openkitty.defaultEmployee` | 空 | 未指定员工时使用的员工 ID 或 @名称 |
| `openkitty.includeEditorContext` | `true` | 是否携带编辑器上下文 |
| `openkitty.maxSelectionCharacters` | `12000` | 最多携带的选区字符数 |
| `openkitty.pollIntervalMs` | `1000` | 任务状态轮询间隔 |
| `openkitty.requestTimeoutMinutes` | `30` | 单个任务最长等待时间 |

如果 Gateway 使用 API Key，运行：

```text
OpenKitty: Set API Key
```

Key 会进入 VS Code `SecretStorage`，不会写入 `settings.json`。

清除 Key：

```text
OpenKitty: Clear API Key
```

### 侧边栏使用

1. 启动 OpenKitty Gateway。
2. 点击 VS Code Activity Bar 中的 OpenKitty 图标。
3. 等待连接状态变为 `Connected`。
4. 选择一个 AI 员工。
5. 输入任务。
6. 根据需要保留或关闭 `Include editor context`。
7. 点击 `Send`，或按 `Ctrl+Enter`/`Cmd+Enter`。
8. 任务执行中可以点击 `Stop`。

### VS Code Chat 中调用

VS Code 的 Chat Participant 名称必须静态写在扩展清单中，而 OpenKitty 的
AI 员工来自服务端动态配置。因此扩展使用一个稳定入口：

```text
@openkitty
```

然后在任务中指定员工：

```text
@openkitty @开发工程师 修复当前选区，运行相关测试并解释改动
```

也支持不带第二个 `@`：

```text
@openkitty 开发工程师 修复当前选区
```

查看员工列表：

```text
@openkitty /employees
```

检查连接：

```text
@openkitty /status
```

### 编辑器右键与命令面板

选择代码后，可以使用编辑器右键菜单：

```text
OpenKitty: Ask AI Employee
```

命令面板还提供：

```text
OpenKitty: Open AI Employees
OpenKitty: Ask AI Employee
OpenKitty: Refresh AI Employees
OpenKitty: Set API Key
OpenKitty: Clear API Key
OpenKitty: Cancel Current Task
```

### 自动携带的 IDE 上下文

开启 `openkitty.includeEditorContext` 后，请求会携带：

- 当前 Workspace 根目录。
- 活动文件相对路径。
- 文件语言。
- 当前选区及行号。
- 活动文件最多 20 条错误或警告诊断。

不会默认发送整个文件；只有用户选中的代码会作为代码内容发送。

### 使用的 OpenKitty API

```text
GET  /api/health
GET  /api/ae/list
POST /api/chat
GET  /api/task/{task_id}
POST /api/task/stop
```

扩展会向 `/api/chat` 发送真正的 `@员工名`，因此复用 Web、飞书等渠道现有的
AI 员工路由和授权逻辑。

## 三、私有分发建议

### 内测或小团队

直接分发带版本号的 VSIX：

```text
openkitty-vscode-0.1.0.vsix
```

可以放在：

- 企业文件服务器。
- 内部制品库。
- 需要登录的下载页面。
- OpenKitty 桌面客户端安装包中。

手工安装的 VSIX 默认不会自动更新，因此新版本需要重新安装。

### 企业规模

部署 VS Code Private Marketplace，可以获得：

- 内网托管私有扩展。
- VS Code 内直接搜索和安装。
- 自动更新。
- 扩展允许列表。
- 版本控制与集中发布。
- 隔离公网 Marketplace。

### 闭源边界

扩展可以不发布 TypeScript 源码，也可以使用私有许可证，但 VSIX 中的编译后
JavaScript 仍可能被分析。因此：

- 不要在扩展中写死 API Key。
- 不要把模型密钥打入 VSIX。
- 不要把核心 Agent 编排逻辑放入扩展。
- 授权、计费、商业员工、提示词和工具实现应保留在 OpenKitty 服务端。

当前实现已经采用这种薄客户端架构。

## 四、本地构建

```powershell
cd E:\openkitty\vscode-extension
npm install
npm test
npm run package
```

打包结果：

```text
E:\openkitty\dist\openkitty-vscode-0.1.0.vsix
```

## 五、验证结果

本次实现已完成以下验证：

- MCP stdio 完整生命周期。
- Streamable HTTP JSON 响应。
- 传统 SSE。
- Streamable HTTP 到传统 SSE 自动降级。
- Bearer Token 和自定义 Header。
- Resource 列出与读取。
- 多 Server 工具重名。
- Tool Registry 动态同步。
- VS Code TypeScript 编译。
- 模拟 OpenKitty Gateway 请求与任务轮询。
- VSIX 打包。
- VS Code 隔离扩展目录安装。
- `go vet ./...`。
- 除仓库已知归档模块边界用例外的 `go test ./...`。

## 六、常见问题

### 侧边栏显示 Disconnected

检查：

1. OpenKitty 是否已经启动。
2. `openkitty.baseUrl` 是否正确。
3. 浏览器能否访问 `http://127.0.0.1:8090/api/health`。
4. Gateway 配置了 API Key 时，是否执行了 `OpenKitty: Set API Key`。
5. 远程地址是否被防火墙、代理或证书拦截。

### 员工列表为空

检查：

- OpenKitty 中是否已加载可见 AI 员工。
- `/api/ae/list` 是否返回员工。
- 员工是否被标记为 `hidden` 或 `internal_only`。
- 商业员工是否具有有效授权。

### Chat 中无法识别员工

推荐使用：

```text
@openkitty @员工名 任务内容
```

或在设置中填写：

```text
openkitty.defaultEmployee
```

该设置既可以填写员工 ID，也可以填写员工的 @名称。

### 任务长时间没有结果

检查 OpenKitty Web 中对应任务状态，并根据实际任务耗时调整：

```text
openkitty.requestTimeoutMinutes
```

也可以在侧边栏点击 `Stop`，或运行：

```text
OpenKitty: Cancel Current Task
```

### VSIX 更新后仍是旧版本

重新执行：

```powershell
code --install-extension E:\openkitty\dist\openkitty-vscode-0.1.0.vsix --force
```

如果采用 Private Marketplace，则应提升扩展版本号后重新发布。
