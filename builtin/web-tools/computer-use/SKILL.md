---
name: computer-use
description: 使用统一 computer 工具持续观察和操作浏览器或桌面应用，支持多步动作、截图、结果验证和会话复用。适合 LIMS、ERP、OA、Word、Excel、内部网站以及没有稳定 API 的图形界面流程。
read_when:
  - 需要操作 Windows 或 Linux 桌面应用
  - 需要跨页面或跨步骤填写复杂表单
  - 需要从 LIMS、ERP、OA 等系统采集或录入数据
  - 需要通过截图和界面状态确认操作结果
  - 命令行、文件工具和结构化 API 无法完成任务
---

# Computer Use

`computer` 是浏览器与本地桌面的统一操作入口。它保留 `session_id`、浏览器 tab、最新观察和步骤历史，使 Agent 能执行“观察 → 操作 → 验证 → 继续”的闭环。

## 基本流程

1. 首次操作前调用 `computer(action="status")`，确认当前平台和桌面后端。
2. 用 `computer(action="observe", surface="browser", url=...)` 打开并观察网页；操作已打开的本地应用时使用 `surface="desktop"`。
3. 从观察结果中确认页面文字、编号控件或活动窗口。
4. 简单操作使用 `action="act"`；连续输入、选择和点击使用 `action="run"`，每组不要超过一个明确的界面状态转换。
5. 后续调用始终传回同一个 `session_id`。工具默认在动作后重新观察并返回截图。
6. 浏览器可以用 `expect_text` 验证结果；桌面可以用 `expect_window` 验证活动窗口。需要理解桌面正文或定位坐标时，把返回的 `last_screenshot_path` 交给 `vision`，问题中明确要求识别控件、文字和坐标。
7. 完成后调用 `computer(action="close", session_id=...)`。

## 状态与快速判断

- 后续 `act/run` 传回最新 `observation_id`。`stale_observation` 表示该观察已失效，先重新观察。此标识校验会话版本，不能检测工具以外的人为页面改动；用户接管、弹窗或窗口移动后仍须重新观察。
- `run.steps` 中可以给每一步附上 `expect_text` 或 `expect_window`。状态转换未达到预期时立即停止，即使 `stop_on_error=false` 也不能继续下一步提交。
- `next_action=observe_and_replan` 表示先检查当前状态再重规划。动作失败可能发生在实际写入之后，禁止自动重放提交、发送、支付或录入。
- 对重复的文本/JSON 分类，可先通过 `tool_detail` 发现 `decision` 并检查 `action=status`。可用时将相同状态下的独立问题合并为一次 Choice/Score/Noul 请求。`route=deliberate` 时交回主模型推理。决策工具不能读取图片，截图定位继续使用视觉模型。
- 概率判断不替代成功断言、用户授权、Middleware 或最终视觉检查。未配置决策供应商时按正常观察—操作—验证流程继续。

## 浏览器示例

```json
{
  "action": "run",
  "surface": "browser",
  "url": "https://lims.example.local",
  "objective": "查询样品 S-001 的实验结果",
  "steps": [
    {"operation": "input", "index": 2, "text": "S-001"},
    {"operation": "click", "index": 3}
  ],
  "expect_text": "检测结果"
}
```

页面导航或大幅更新后，重新 `observe`，不要继续使用旧的控件编号。登录状态由同一个浏览器会话保留。

## 桌面示例

```json
{
  "action": "run",
  "surface": "desktop",
  "objective": "在实验室客户端中查询样品",
  "steps": [
    {"operation": "click", "x": 420, "y": 185},
    {"operation": "type", "text": "S-001"},
    {"operation": "key", "key": "enter"}
  ],
  "expect_window": "LIMS"
}
```

桌面坐标必须来自最近一次截图及其视觉分析。窗口移动、缩放、弹窗或页面切换后先重新观察；不要根据旧截图猜坐标。

## Windows 与 Linux

- Windows 使用 Win32 活动窗口、鼠标和键盘后端，截图使用 Pillow ImageGrab 或 ffmpeg。
- Linux 浏览器操作统一使用 Chrome CDP，不依赖桌面协议，优先用于 Web LIMS。
- Linux X11 桌面优先使用 `xdotool`，截图可使用 `gnome-screenshot`、`scrot`、`maim` 或 ImageMagick。
- Linux Wayland 按环境选择 `grim`、`wtype`、`ydotool` 等组件。不同桌面合成器限制不同，先读取 `computer status` 的能力报告；桌面输入不可用时仍可使用完整的 CDP 浏览器能力。

## 与文档自动化衔接

从企业系统采集到的数据应先整理成结构化 JSON，再调用 DOCX 模板自动化能力。不要把大量实验数据逐字复制进聊天上下文：优先保存为工作区文件，然后把文件路径和字段结构交给文档工具。
