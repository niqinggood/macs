# OpenKitty 向独立机器人、VLA 与自主运动演进的思考

> 文档状态：架构思考稿  
> 更新时间：2026-08-23  
> 目标：在不削弱 OpenKitty 现有 Agent 能力的前提下，让它逐步具备真实机器人接入、连续感知、VLA 推理和自主运动能力。

## 1. 核心结论

OpenKitty 最适合成为机器人的“任务大脑”和“执行主管”，而不是直接承担电机伺服、关节插补或毫秒级碰撞响应。

现有 OpenKitty 已经具备机器人上层系统最难复用的一批能力：宿主控制的 Turn 执行、Skill 编排、工具注册与热更新、任务恢复、幂等回执、即时停止、多步骤规划、模型调用和人机交互。下一阶段不应重新开发一套 Agent，也不应把所有控制能力塞进大模型，而应补齐一层独立、连续、确定性的机器人运行时。

推荐的总体结构是：

```text
用户 / 语音 / 远程任务
          ↓
OpenKitty Mission Executive
目标理解、任务规划、Skill 编排、记忆、恢复、解释
          ↓ 结构化 Goal
Embodiment Runtime
连续观测、状态估计、控制权仲裁、VLA Policy Adapter、Action Chunk
          ↓ 带时效和版本的动作
实时控制与安全层
ROS2 Control / Nav2 / MoveIt / MCU / PLC / 厂商控制器
          ↓
底盘、机械臂、夹爪和传感器
```

OpenKitty 可以恢复“去仓库找到箱子”的任务，但不应在进程恢复后盲目重放“左轮转 300 ms”或“关节 2 移动 15°”。任务可以恢复，物理动作必须先停止、重新观测和对账。

## 2. OpenKitty 当前已经具备的基础

### 2.1 应直接复用的能力

- `robot_status`、`robot_move`、`robot_stop`、`robot_command` 已经形成受约束的高层动作入口。
- 现有机器人运行时已经具备动作限幅、`command_id` 幂等、非阻塞回执、抢占停止和追加式审计。
- Skill 热更新和单次运行快照适合固定一次任务所使用的行为版本。
- TurnCheckpoint、工具执行回执和任务恢复能力适合长周期机器人任务。
- UART、Modbus、ROS2 等底层工具已经能承担设备调试、Bring-up、故障诊断和新硬件快速验证。
- Dofbot Skill 证明了“型号 Skill + 有界 SDK Bridge”的硬件扩展路线是可行的。
- 当前架构已经明确：实时闭环应留在 MCU、PLC、ROS2 控制器或厂商驱动中，OpenKitty 负责理解目标、调用动作、观察结果和编排任务。

相关实现：

- [机器人运行时](robot_tool.go)
- [ROS2 工具](ros2_tool.go)
- [UART 工具](uart_tool.go)
- [Modbus 工具](modbus_tool.go)
- [机器人控制架构](docs/robot-control-architecture.md)
- [底层硬件工具](docs/hardware-transport-tools.md)
- [Dofbot Skill](skills/dofbot-control/SKILL.md)

### 2.2 当前能力的真实边界

当前代码可以验证机器人控制语义，但还不能等同于真实机器人闭环：

1. `RobotDriver` 当前只实现了 `sim`。
2. `StartMotion` 只接受一次动作，没有连续反馈流、轨迹进度、按命令取消和故障事件。
3. 位姿是根据时间、距离和速度在宿主内估算的，不是编码器、里程计或关节状态的真实反馈。
4. 当前所谓看门狗主要表现为动作持续时间上限和 Go 定时器；如果 OpenKitty 进程崩溃，它无法替代 MCU 或控制器上的硬件心跳看门狗。
5. `robot_status` 中的 `e_stop`、`obstacle_blocked`、`tilt_fault` 当前固定为 `false`。接入实体机器人前，未知状态必须表达为 `unknown`，不能表达成“确定安全”。
6. ROS2 工具通过一次性 CLI 子进程调用，适合读取、诊断和低频动作，不适合高频订阅、Action 生命周期管理和连续控制。
7. 当前视觉链路主要是“拍摄单张图片—调用远程视觉模型—返回文字描述”，尚未形成同步视频、深度、目标跟踪、坐标系和本体状态联合输入。
8. `WatchTool.monitor` 是秒级抓拍，不是 VLA 需要的连续观测循环；其取消和休眠逻辑也需要改成上下文可中断。
9. 机器人 Hub 当前按 workspace 创建。真实硬件如果沿用这一方式，不同 workspace 可能竞争同一设备，因此实体机器人控制必须收敛为系统级唯一运行时。

## 3. 演进时必须坚持的原则

### 3.1 不降低现有 Agent 能力

- 不删除 shell、UART、Modbus、ROS2 或厂商 SDK 等现有能力。
- 底层工具继续用于设备发现、调试、维修、诊断和新型号快速接入。
- 自主执行主链路使用强类型 Robot Runtime，但不因此废掉原始工具。
- 不给普通软件任务增加机器人专用限制。
- 不引入全局强制脱敏；原始传感器数据是否保留由部署配置和具体业务决定。

### 3.2 物理安全不等于反复询问用户

不应把“每走一步都确认一次”当成安全。这样会显著降低自主能力，也不能处理毫秒级危险。

更合理的方式是：

- 在“进入自主模式”“人工接管”“离开限定工作区”等模式切换点处理授权或确认。
- 常规动作由确定性安全层自动做速度、加速度、力矩、碰撞和工作区限制。
- 急停、避障、失联停车不等待 LLM，也不等待用户确认。
- 高层 Agent 仍然可以自由规划，只是输出的物理动作必须经过下层控制器的连续校验。

### 3.3 不重复开发成熟机器人基础设施

- 移动导航优先使用 Nav2 的定位、规划、行为树、恢复、避障和回充能力。
- 机械臂轨迹和伺服优先使用 ros2_control、MoveIt 和厂商控制器。
- 坐标变换使用 TF，机器人描述使用 URDF/Xacro。
- OpenKitty 负责任务语义、技能选择、跨系统编排和异常恢复，不重新实现完整 SLAM、轨迹优化器或关节控制器。

### 3.4 VLA 是策略组件，不是完整机器人系统

VLA 可以把图像、语言和本体状态转换为动作，但不能独立解决：

- 急停和硬件失效；
- 控制周期抖动；
- 过期观测和网络延迟；
- 多控制源冲突；
- 电池、温度和算力降级；
- 标定漂移；
- 任务恢复和动作对账。

因此 VLA 应以独立 Policy Server 形式接入，而不是直接嵌入 OpenKitty 的 Turn 工具循环。

## 4. 建议新增的 Embodiment Runtime

### 4.1 系统级唯一运行时

真实机器人应有一个独立进程或服务，独占物理控制面。OpenKitty、手柄、Web 控制台、Nav2 和 VLA 都通过它申请控制权，不能各自直接竞争电机或 ROS Topic。

可采用：

- ROS2 节点与 Action Server；
- 本机 gRPC；
- Unix Domain Socket；
- Windows Named Pipe；
- 与厂商 SDK 隔离的 Python/C++ Sidecar。

Go 主进程继续承担 Agent 编排；Python/CUDA/厂商 SDK 依赖留在独立运行时或 Policy Server，避免污染 OpenKitty 核心依赖。

### 4.2 Driver 接口

下一代接口不应只有 `StartMotion/Stop/Status`，建议至少包含：

```text
Connect / Disconnect
Configure / Calibrate
Activate / Deactivate
Capabilities
Observe
ExecuteGoal
ExecuteActionChunk
Feedback
Cancel
Stop
EmergencyStop
Health
```

其中：

- `Goal` 是“导航到充电站”“抓取红色盒子”这类任务级请求。
- `ActionChunk` 是 VLA 输出的短时连续动作。
- `Feedback` 是执行进度、真实状态、安全干预和失败原因。
- `Stop` 是受控软件停车。
- `EmergencyStop` 必须有独立高优先级路径，不能继承一个可能已经取消的普通任务 Context。

### 4.3 生命周期状态机

建议统一机器人生命周期：

```text
disconnected
    ↓
connecting
    ↓
uncalibrated → calibrating
    ↓
inactive
    ↓
ready
    ↓
active ↔ paused
    ↓
degraded / fault / estopped
```

只有 `ready` 或 `active` 才能接收运动。`fault` 和 `estopped` 的恢复流程必须显式，不允许模型仅凭文字把状态改回正常。

## 5. 强类型观测与动作协议

连续机器人控制不能依赖含义不明确的 `map[string]interface{}`。建议定义稳定的 Protobuf、ROS Message 或等价强类型协议。

### 5.1 ObservationEnvelope

至少包含：

```text
robot_id
session_id
observation_sequence
monotonic_timestamp_ns
wall_clock_timestamp
frame_id
calibration_hash
sensor_freshness
camera/depth/lidar references
joint positions/velocities/efforts
base pose/velocity
imu/force/battery/temperature
safety state
```

关键要求：

- 使用单调时钟判断数据年龄，不能只依赖墙上时间。
- 每个传感器有独立时间戳和 freshness。
- 摄像头、深度、关节和动作必须能对齐到同一观测序列。
- 标定变化后生成新的 `calibration_hash`，旧策略或旧缓存不得悄悄继续使用。

### 5.2 ActionChunk

至少包含：

```text
robot_id
session_id
control_epoch
command_id
policy_id + policy_hash
source_observation_sequence
action_space
frame_id
units
control_period
actions[]
valid_from / valid_until
```

`control_epoch` 是增强鲁棒性的核心字段：

- 每次急停、人工接管、驱动重连和进程重启都递增。
- 旧 epoch 的动作全部无效。
- 即使远程 VLA 在急停后才返回结果，运行时也不会执行这段过期动作。

### 5.3 ActionFeedback

至少返回：

```text
accepted / rejected / executing / completed / canceled / faulted
executed_step_count
latest_observation_sequence
measured_state
safety_interventions[]
latency and jitter
failure_code
retriable
```

失败码应是稳定枚举，文字说明只是补充。这样 Agent 才能根据“定位丢失”“动作过期”“碰撞区触发”“驱动失联”选择不同恢复策略。

## 6. 控制权、停止和恢复

### 6.1 控制权仲裁

机器人可能同时收到多个来源的命令：

- OpenKitty 任务；
- VLA Policy；
- Nav2；
- Web 遥控；
- 手柄；
- 本地急停。

需要一个明确的优先级和控制租约：

```text
物理急停
> 本地人工接管
> 软件停止
> 故障恢复控制器
> Nav2 / MoveIt / VLA 当前控制租约
> OpenKitty 新任务
```

控制租约是为了解决多控制源竞争，不是增加用户权限审批。租约失去心跳后，下层自动保持或停车。

### 6.2 真正的看门狗

必须有至少两层：

1. Robot Runtime 心跳看门狗：检测 Policy、Nav2 或 OpenKitty 失联。
2. MCU/驱动器硬件看门狗：检测 Robot Runtime 进程崩溃、系统卡死或通信中断。

硬件看门狗应在没有刷新时自动输出零速度、制动或进入厂商定义的安全状态。Go 定时器只能作为上层补充。

### 6.3 恢复语义

进程恢复时：

1. 立即建立独立停止/保持状态。
2. 新建 `session_id` 和 `control_epoch`。
3. 读取真实关节、里程计、急停、控制器和传感器状态。
4. 将未完成动作标记为 `unknown_after_restart`，禁止自动重放。
5. 恢复高层任务目标，并基于新观测重新规划。

现有 TurnCheckpoint 和工具回执可以继续用于任务恢复，但物理动作回执需要单独的重启对账语义。

## 7. VLA 接入方式

### 7.1 Policy Server Adapter

建议定义统一 Policy 接口，适配 OpenPI、OpenVLA、LeRobot、GR00T 或后续模型：

```text
LoadPolicy
GetMetadata
ResetEpisode
Infer(observation, instruction) -> ActionChunk
Health
UnloadPolicy
```

Policy metadata 必须声明：

- 支持的 embodiment；
- 输入图像和本体状态格式；
- 动作空间、维度和单位；
- 控制频率和 action horizon；
- 归一化统计版本；
- 所需摄像头和标定；
- 模型、权重和代码版本。

### 7.2 Action Chunk 与异步推理

VLA 推理通常慢于底层控制周期，因此推荐：

1. Policy 根据最新观测预测一段短动作。
2. Robot Runtime 逐步执行其中一部分。
3. 执行当前 Chunk 时异步请求下一段。
4. 下一段到达时检查观测序列、epoch 和 `valid_until`。
5. 状态变化过大、传感器过期或安全层介入后丢弃旧 Chunk，重新推理。

Action horizon 不能固定写死，应按模型、硬件、任务和延迟配置。Chunk 越长吞吐越高，但开环误差越大；Chunk 越短闭环性越强，但更容易受推理延迟影响。

### 7.3 混合自主路线

不建议“所有运动都使用 VLA”。更强的组合是：

- OpenKitty：长任务理解、分解和跨系统编排；
- Nav2：地图导航、局部避障、路径跟随、恢复和回充；
- MoveIt/ros2_control：机械臂轨迹、碰撞和关节约束；
- VLA：开放场景理解、物体操作、语义子任务和传统规划难以表达的动作；
- Safety Supervisor：对所有来源的动作做最终约束。

这种结构既保留确定性控制能力，又能逐步增加 VLA 的覆盖范围。

## 8. 自主移动优先路线

如果目标是尽快得到可工作的独立机器人，建议先做移动底盘，再做复杂机械臂 VLA。

第一台设备至少应具备：

- 轮式底盘及编码器；
- IMU；
- 2D LiDAR 或可靠的深度感知；
- 前向 RGB/深度摄像头；
- 物理急停或电源切断；
- MCU 级通信失联停车；
- 电池电量和充电状态；
- ROS2 驱动或可封装的稳定厂商 SDK。

第一阶段能力目标：

1. 真实状态读取和软件停止。
2. 遥控与控制权接管。
3. 建图、定位和短距离导航。
4. 动态避障和定位丢失恢复。
5. 自动回充。
6. OpenKitty 执行多航点、多步骤任务。
7. VLA/VLM 只参与语义识别和目标选择，不直接控制轮速。

移动链路稳定后再增加机械臂、夹爪、腕部相机和 VLA ActionChunk，可以显著降低同时调试定位、导航、视觉、操作和模型的复杂度。

## 9. 数据闭环

现有 `.openkitty/robot/audit.jsonl` 适合动作审计，但不足以训练和评估 VLA。需要独立 Episode Store。

每个 episode 应记录：

- 语言任务和拆分后的子任务；
- 多摄像头视频、深度和传感器数据；
- 本体状态和真实执行动作；
- Policy 输出、被执行的 Chunk 和被丢弃的 Chunk；
- Nav2、MoveIt、安全层和人工控制的动作来源；
- 人工介入、恢复过程和失败原因；
- Skill、Policy、Driver、标定和配置版本；
- 推理延迟、控制抖动、传感器年龄；
- 成功条件和最终标签。

存储建议：

- 元数据和低频信号使用 Parquet 或等价列式格式；
- 视频使用 MP4 等标准媒体格式；
- 审计记录只保存引用和摘要，不把每一帧塞进 JSONL；
- 数据格式尽量兼容 LeRobotDataset，便于使用现有训练和可视化生态；
- 保留失败、人工纠正和恢复数据，它们通常比只有成功演示更有价值；
- 不做全局强制脱敏，是否保留原始视频、音频和环境信息由具体部署策略决定。

当前“审计文件无法写入则拒绝运动”的策略适合保守模拟阶段。独立机器人需要重新权衡可用性：建议预留固定大小的本地环形日志或独立分区，避免磁盘满导致已经安全可执行的任务完全失能，同时持续上报存储故障。停止命令永远不能被日志系统阻塞。

## 10. 仿真、测试与评估

### 10.1 同一接口覆盖仿真和实机

`sim` 不应只是宿主内的时间积分器，后续应支持：

- Gazebo / Ignition；
- Isaac Sim / Isaac Lab；
- MuJoCo；
- 厂商数字孪生；
- Hardware-in-the-loop。

仿真和实机必须共用同一 Observation、Goal、ActionChunk、Feedback 和 Driver 契约，避免出现“仿真是一套代码、实机又重写一套”。

### 10.2 分级验证

推荐按以下阶段扩大自主权：

1. 单元测试：协议、限幅、状态机、epoch 和幂等。
2. 仿真闭环：任务成功、碰撞、超时和恢复。
3. Shadow Mode：Policy 产生动作但不执行，与传统控制器结果比较。
4. 台架测试：机器人架空或限制在安全区域。
5. 人工监护的有界自主。
6. 扩大场景和持续时间。
7. 无人值守，但保留远程接管和物理急停。

### 10.3 故障注入

至少覆盖：

- 摄像头断流、旧帧和时间戳乱序；
- LiDAR、IMU、编码器异常；
- Policy 超时、GPU OOM 和模型进程崩溃；
- 网络延迟、断网和重复响应；
- ROS2 节点重启和 Action 丢失；
- 定位漂移和地图不一致；
- 电池低、温度高和算力降频；
- 磁盘满和日志写入失败；
- 急停、人工接管和控制租约冲突；
- OpenKitty 在动作执行中崩溃并重启。

### 10.4 关键指标

- 任务成功率；
- 人工介入率；
- 碰撞和近碰撞次数；
- 急停到实际零速度的延迟；
- 观测年龄与动作年龄；
- Policy 推理 P50/P95/P99；
- 控制循环抖动和超周期次数；
- 安全层裁剪或拒绝动作比例；
- 故障恢复成功率；
- 单任务能耗和完成时间。

## 11. 热插拔策略

OpenKitty 已有 Skill 热更新，不需要再造一套 Skill 管理器。机器人方向需要补的是三类不同热插拔语义：

### 11.1 Skill 热更新

- 继续使用现有事务式加载和运行快照。
- 已开始任务固定原 Skill 版本；新任务使用新版本。

### 11.2 Policy 热更新

- Policy 以版本和 hash 标识。
- 一个 episode 内固定 Policy。
- 新模型先健康检查、预热和 Shadow 验证。
- 只在机器人保持、暂停或 episode 边界切换。

### 11.3 Driver 热更新

- Driver 采用独立进程或 ROS2 插件，避免原生 SDK 崩溃拖垮 OpenKitty。
- Driver 更新前必须停止动作并释放控制接口。
- 更新后重新连接、校准、读取状态和递增 `control_epoch`。
- 不能在活动轨迹中无缝替换底层 Driver。

## 12. 建议实施顺序

### P0：真实硬件闭环

1. 明确第一台机器人和一个有边界的目标任务。
2. 定义 Observation、Goal、ActionChunk、Feedback 和生命周期协议。
3. 建立系统级唯一 Robot Runtime。
4. 实现第一个真实 ROS2 或厂商 SDK Driver。
5. 接入真实急停、编码器/关节反馈、传感器 freshness 和 MCU 看门狗。
6. 完成控制租约、`session_id`、`control_epoch` 和重启对账。
7. 让仿真与实机使用相同接口。

### P1：可靠自主移动或机械臂控制

移动机器人路线：

1. Nav2 建图、定位、导航和恢复。
2. Collision Monitor、速度平滑和动态障碍处理。
3. 多航点任务、自动回充和人工接管。

机械臂路线：

1. ros2_control 和 MoveIt。
2. URDF、TF、关节/工作空间限制和碰撞模型。
3. 夹爪、腕部相机、力矩或力传感器。
4. 预定义姿态、轨迹和视觉定位。

### P2：VLA 与数据闭环

1. Policy Server Adapter。
2. 连续同步观测和 Action Chunk Broker。
3. Episode Store 和遥操作采集。
4. Shadow Mode、仿真批量评估和故障注入。
5. 人工介入数据采集与再训练。
6. 有边界的实机 VLA 任务。

### P3：独立运行能力

1. 本地推理和断网降级。
2. 电池、温度、算力和存储健康管理。
3. 自动回充、暂停恢复和长时间任务。
4. OTA 更新、版本回滚和设备健康诊断。
5. 多机器人任务分配与交通管理，只有单机器人稳定后再做。

## 13. 明确不建议做的事情

- 不让 LLM Tool Call 直接输出 PWM、关节力矩或高频轮速。
- 不用 Go 的普通定时器冒充硬件看门狗。
- 不把单张图片文字描述当成连续机器人视觉。
- 不让不同 workspace 各自持有同一真实硬件连接。
- 不在机器人运动中热切换 Driver、Policy 或动作空间。
- 不在进程恢复后自动重放未确认完成的物理动作。
- 不为了统一接口删除 UART、Modbus、ROS2、shell 和厂商 SDK 调试能力。
- 不为每一个正常动作增加人工审批；安全应由持续运行的确定性控制层完成。
- 不重复开发 Nav2、MoveIt、ros2_control 已经成熟提供的能力。
- 不一开始同时攻克自主导航、双臂操作、全身控制和通用 VLA。

## 14. 推荐的第一个里程碑

最务实的首个里程碑是：

> OpenKitty 在一台真实轮式机器人上，通过系统级 Robot Runtime 和 ROS2/Nav2，完成“接收自然语言目标—自主导航—动态避障—到达反馈—中途人工停止—断网保持本地安全—恢复后重新观测并继续高层任务”的完整闭环。

这个里程碑完成后，OpenKitty 就不再只是“能调用硬件工具的 Agent”，而是具备了独立机器人操作系统上层的基本形态。随后增加机械臂和 VLA，只是在已稳定的观测、动作、安全、数据和恢复契约上扩展新的 embodiment 与 policy，不需要推翻现有系统。

## 15. 当前验证基线

2026-08-22 已执行：

```text
go test ./internal/app -run 'TestRobot|TestROS2|TestResolveVisionPath' -count=1
```

结果：

```text
ok      mac     3.436s
```

该结果证明当前模拟机器人动作语义、ROS2 工具约束和视觉路径处理相关测试通过；它不代表任何实体机器人、实时控制、真实急停或 VLA 闭环已经验证。

## 16. 外部参考方向

- ROS2 Control：https://control.ros.org/rolling/index.html
- Nav2 Collision Monitor：https://docs.nav2.org/configuration/packages/collision_monitor/configuring-collision-monitor-node.html
- Nav2 Behavior Trees：https://docs.nav2.org/behavior_trees/index.html
- MoveIt Servo：https://moveit.picknik.ai/main/doc/examples/realtime_servo/realtime_servo_tutorial.html
- Physical Intelligence OpenPI：https://github.com/Physical-Intelligence/openpi
- OpenVLA：https://github.com/openvla/openvla
- Hugging Face LeRobot：https://github.com/huggingface/lerobot
- LeRobotDataset v3：https://huggingface.co/docs/lerobot/lerobot-dataset-v3
- Isaac Lab-Arena：https://developer.nvidia.com/isaac/lab-arena
