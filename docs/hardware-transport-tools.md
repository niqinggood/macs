# OpenKitty 底层硬件工具

OpenKitty 核心提供通用底层硬件工具，不在核心中解释前进、后退、左转、右转、
停止、机械臂姿态或任何设备型号语义。具体型号的 Skill 负责把用户意图转换为
底层工具调用。

```text
用户意图
    ↓
设备型号 Skill
    ↓
plc_modbus / uart / ros2
    ↓
实体硬件或硬件中间件
```

## 配置门

三个工具均由 `openkitty-config.yaml` 独立控制：

```yaml
tools_config:
    enabled: true

    modbus:
        enabled: false
        allow_write: false

    uart:
        enabled: false
        allow_write: false

    ros2:
        enabled: false
        allow_publish: false
        allow_service_call: false
        allow_action: false
```

工具只有在 `tools_config.enabled=true`、自身 `enabled=true`、未列入全局
`disabled_tools`，且未被 Agent 工具白名单或黑名单排除时才会注册。配置热更新
会立即注册或移除工具。

配置文件决定工具是否可用、连接参数和硬限制。Skill 不能启用被配置关闭的工具，
也不能绕过写入权限。

## `uart`

`uart` 提供以下通用操作：

- `list_targets`
- `probe`
- `read`
- `write`
- `transact`

输入与输出支持 `hex`、`base64` 和 `utf8`。每次调用打开目标、完成一次有界操作
后关闭，不维持隐式跨任务串口会话。同一串口目标的操作会串行执行。

命名 target 可以隐藏实际端口路径，并为写入增加第二层 `allow_write`。也可以由
受信任的硬件 Skill 在调用时提供直接端口及串口参数。

示例：

```json
{
  "action": "transact",
  "target": "device_uart",
  "encoding": "hex",
  "data": "AA5501",
  "read_bytes": 8,
  "response_encoding": "hex"
}
```

核心只负责发送 `AA5501` 并返回收到的字节，不解释这组数据的设备含义。

## `ros2`

`ros2` 通过固定的 ROS 2 CLI 子命令集合提供：

- 节点、Topic、Service、Action 列表；
- Topic 信息和单次读取；
- Topic 单次发布；
- Service 类型查询和调用；
- Action 信息查询和目标发送。

发布、Service 调用和 Action 目标分别由独立权限控制。可选 allowlist 限制资源
名称；空 allowlist 表示不额外限制名称，但写操作仍必须经过对应权限开关。

示例：

```json
{
  "action": "service_call",
  "name": "/device/set_mode",
  "message_type": "example_interfaces/srv/SetBool",
  "payload": {
    "data": true
  }
}
```

核心不解释 `/device/set_mode` 或 payload 的设备语义。名称、类型、payload 和调用
顺序由设备型号 Skill 提供。

## Skill 的职责

设备型号 Skill 可以定义：

- 用户说法与操作名称；
- 每个操作调用哪个底层工具；
- UART 数据、Modbus 地址和值、ROS 2 名称、类型和 payload；
- 多步调用顺序、等待时间和结果解释；
- 型号特有的检查、停止和恢复流程。

Skill 负责“如何操作这个型号”；底层工具负责“按参数执行并返回结果”。

## 兼容性

现有 `robot_status`、`robot_move`、`robot_stop` 和 `robot_command` 作为当前 `sim`
机器人运行时的兼容能力继续保留。新的硬件 Skill 不依赖这些高层工具，可以直接
选择 `plc_modbus`、`uart` 或 `ros2`。
