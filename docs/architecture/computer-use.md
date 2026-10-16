# Computer Use 统一界面自动化

macs 的 `computer` 工具把已有 Chrome CDP 与本地桌面操作整合为一个有状态协议。目标不是为某个 LIMS 编写固定脚本，而是让 Agent 通过同一种“观察—操作—验证”循环处理不同企业的 Web 或桌面流程。

## 调用链

```text
LLMAgent
  -> computer(action=status|observe|act|run|history|close)
  -> 内存 Session（surface / target_id / latest observation / history）
  -> browser: web_navigate + web_page + web_close
     desktop: desktop_task
  -> 最新 DOM/活动窗口 + PNG 截图 + 验证结果
```

领域实现位于：

- `internal/tools/computer/tool.go`：统一协议、会话、批量动作、观察和验证。
- `internal/tools/desktop/task.go`：Windows/Linux/macOS 的桌面后端选择、能力探测及交互记录。
- `internal/tools/browser/cdp.go`：浏览器 tab、稳定编号控件和 CDP 操作。
- `internal/app/mac.go`：只负责把已有后端回调注入 `computer`。

## 会话语义

- 第一次 `observe`、`act` 或 `run` 会创建 `session_id`。
- 浏览器会话保留 `target_id` 和登录状态；桌面会话绑定当前图形桌面。
- `run` 最多接受 16 个有序动作，默认首个失败即停止。
- `act` 和 `run` 默认在动作后重新观察；每次观察保存独立 PNG，并更新会话的最新截图。桌面正文和坐标定位由 `vision(last_screenshot_path)` 完成。
- 浏览器用 `expect_text` 验证 DOM 文字；桌面用 `expect_window` 验证活动窗口。桌面正文继续由视觉模型检查最新截图。
- `computer` 标记为不可并行执行，避免多个动作竞争同一个鼠标、键盘或 tab。

同一工具实例内部也串行执行直接调用与工作流回放，等待者遵循 `context` 取消。
这不是跨进程桌面锁；不同 Agent 实例共用物理桌面时仍需宿主调度或隔离。
每次成功观察生成 `observation_id`，并置 `observation_valid=true`。`act/run`
可传回该 ID，旧版本或已经失效的观察会在执行后端动作前被拒绝。
可能改变界面的动作会先使旧观察失效，即使后端随后返回错误也不恢复有效性。
它是会话版本检查，不是实时 DOM 指纹；外部用户操作后仍须重新观察。

`run.steps` 支持逐步 `expect_text` / `expect_window`。断言在该步动作后立即检查，
结果留在步骤的 `verification` 中；断言失败始终停止后续动作。其参数随步骤保留，
`workflow_learner` 回放时仍在原位置执行，不把中间页面条件挪到流程末尾。
顶层成功断言保持原有最终验收语义。

回执的 `next_action` 为 `continue`、`observe` 或 `observe_and_replan`。
失败回执不授权重放写入操作；`automatic_replay_allowed=false` 明确要求重新观察
与判断。桌面后端报告失败时，不能把同一回执中的窗口文字当作成功验收。

需要快速分类时可使用共享的 [`decision` 工具](fast-decisions.md)，但概率判断不能
代替 UI 成功证据，也不改变权限检查或视觉模型的职责。

历史在进程内 Session 中保留动作名称、结构化回放参数、耗时、截断后的输出、错误和成功断言，供 `workflow_learner` 在本次运行中泛化；Session 不落盘，进程重启后需要重新观察目标界面。编译后的复用方案只保存变量槽，不保存被槽化的样品输入值。

## 平台策略

### 浏览器

浏览器在 Windows、Linux 和 macOS 上统一使用 Chrome DevTools Protocol。对于 Web LIMS，这是首选路径：可读取 DOM、使用稳定编号控件、上传文件，并在页面更新后重新提取控件。

### Windows 桌面

使用 Win32 获取活动窗口，使用系统鼠标/键盘调用完成点击、输入、按键、滚动和拖拽；截图使用 Pillow ImageGrab 或 ffmpeg。

### Linux 桌面

- X11：优先 `xdotool`；剪贴板输入可配合 `xclip`；截图可使用 `gnome-screenshot`、`scrot`、`maim` 或 ImageMagick。
- Wayland：截图优先 `grim`，键盘可使用 `wtype`，指针可使用 `ydotool`；实际能力受合成器和桌面会话限制。
- 无图形会话或桌面后端不完整时，`computer(action="status")` 会返回可用组件；CDP 浏览器自动化仍可独立使用。

## LIMS 到 Word

推荐的业务链路是：

```text
computer 进入企业 LIMS 并查询样品
  -> 结构化提取实验结果
  -> 保存为 JSON/CSV
  -> 文档自动化检查企业 DOCX 模板
  -> 自动映射、重复行填充和完整性验证
  -> computer 打开生成文档做最终视觉检查（需要时）
```

界面布局变化时优先重新观察和重定位。稳定、高频流程可以直接从成功的 Session 历史生成可回放配方：

```text
computer(action=run, ..., expect_text="保存成功")
  -> workflow_learner(action=preview, session_id="computer_...", name="enterprise-lims-entry")
  -> 核对输入槽、定位器警告和成功断言
  -> workflow_learner(action=save, ..., variables=[...])
  -> 生成 <skills-dir>/enterprise-lims-entry/SKILL.md + recipe.json
  -> workflow_learner(action=replay, name="enterprise-lims-entry", inputs={...}, evaluate=true)
```

`computer` 在内存 Session 中保留每步结构化参数和独立成功断言；`workflow_learner` 会自动把输入、选择、文件路径等演示值替换为 `${inputs.<name>}`。演示值本身不写入 `recipe.json` 或生成的 Skill。DOM 序号和桌面坐标仍可回放，但会降低 readiness 分数，提示后续补录稳定 selector 或重新观察定位。

评测支持一个或多个 `session_id`。每次运行按操作顺序、执行状态和成功断言计算分数；多次评测返回通过率、平均分和最低分，并可在 Skill 的 `evals/` 目录保存结构化报告。它用于判断流程是否已经能无人值守复用，不替代 DOCX 引擎自己的字段完整性和版式验证。
