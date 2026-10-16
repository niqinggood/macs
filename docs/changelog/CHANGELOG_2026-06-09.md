# Changelog 2026-06-09 — 团队协作系统深度强化

## 总览

在昨天修复信息衰减的基础上，系统性地补齐团队协作的 4 个短板：质量把关、角色规范、执行效率、容错能力。

---

## 一、合成员 Synthesizer — 团队产出的质量把关

### 问题

所有角色完成后，输出是各角色结果的简单拼接，导致：
- 设计风格不一致（多角色各写各的）
- 重复内容未合并
- 装饰性无效元素混入（假分页按钮等）
- 遗漏原始任务中的要点无人检查

### 方案

新增 `synthesize()` 方法，所有团队模式（并行/串行/混合）完成后自动调一个"合成员"子 Agent：

- 合并去重 + 统一格式风格
- 逐条对照原始任务检查遗漏
- 移除无实际功能的装饰性内容
- 合成失败自动回退到原始拼接，不影响交付

### 关键代码

```go
func (t *AssembleTeamTool) synthesize(ctx context.Context, task string,
    roleNames []string, allOutputs []string, fallbackOutput string) (string, error) {
    // 用 sub_agent 创建合成员，max_iterations=10
    // 失败时返回 fallbackOutput（原始拼接），不丢数据
}
```

---

## 二、角色参考文档补全

### 问题

`skills/team-roles/` 下只有 3 个文件（frontend/backend/qa），但 4 个模板用了 14 种角色。缺失规范导致角色产出质量参差不齐。

### 新增 9 个规范文档

| 文档 | 角色 | 核心约束 |
|------|------|----------|
| `pm.md` | PM/产品经理 | 用户故事格式、三级优先级、验收标准 Checklist |
| `architect.md` | 架构师 | 简单优先、模块职责单一、技术选型说明理由 |
| `developer.md` | 程序员 | 函数<50行、边界条件必处理、不硬编码 |
| `reviewer.md` | 审查员 | 四维检查(正确性/安全/性能/可维护)、三级问题分类 |
| `researcher.md` | 研究员 | 交叉验证、标注来源、区分事实与观点 |
| `analyst.md` | 分析师 | 先结论后分析、矛盾识别、信息盲区标注 |
| `writer.md` | 撰稿人/写手 | 开门见山、金字塔结构、具体>抽象 |
| `strategist.md` | 策划 | 目标先行、受众分析、差异化定位 |
| `editor.md` | 编辑 | 结构性→语言性→准确性→风格 四层检查 |

每个文档 ~20 行，聚焦可执行的约束而非泛泛的原则。

---

## 三、fullstack 混合执行模式

### 问题

全栈团队的 PM → 后端 → 前端 → 测试 纯串行低效——后端和前端完全可以并行工作。

### 方案

新增 `executeHybrid()` + `hybridTemplateStages` 配置：

```
fullstack: PM → [后端 + 前端] 并行 → 测试
```

- 每个 stage 拿到之前所有 stage 的完整产出
- 并行 stage 内部用 `parallel_sub_agent` 同时执行
- `parallel_sub_agent` 不可用时自动降级为串行
- 所有 stage 完成后走合成员统一交付

### 架构

```go
var hybridTemplateStages = map[string][][]int{
    "fullstack": {{0}, {1, 2}, {3}}, // PM → [后端+前端]并行 → 测试
}
```

---

## 四、串行模式失败不中断

### 问题

4 人团队第 2 个角色挂了，后续全部跳过，整个任务白跑。

### 方案

- 角色失败后记录错误，`allOutputs[i] = "(执行失败: err)"`，继续执行下一个
- 后续角色收到指引："如果某个上游角色失败了，请用你的能力弥补其职责"
- 最终报告：`[TEAM] 串行完成: 3/4 成功, 1 失败`

---

## 五、代码重构

### 提取公共方法

- `buildRoleTask()` — 统一构建角色任务 prompt，消除 executeSequential/executeParallel/executeHybrid 中的重复代码
- `synthesize()` — 所有执行路径共享同一个合成员逻辑
- `loadRoleRef()` — 补充 PM/策划/写手/编辑 的角色映射

---

## 六、Web UI 团队面板更新

| 改动 | 说明 |
|------|------|
| 模式标签 | 每张卡片显示 并行/串行/混合 彩色标签 |
| fullstack 结构可视化 | 角色列表加 `→` 箭头表示阶段分隔 |
| 描述文案更新 | 每张卡片描述含执行流程 + 合成员说明 |
| 模式颜色 | 并行=绿、串行=橙、混合=蓝 |

### CSS 新增

```css
.team-mode-badge { ... }
.mode-parallel { background: #d3f9d8; color: #2b8a3e; }
.mode-sequential { background: #fff3bf; color: #e67700; }
.mode-hybrid { background: #d0ebff; color: #1c7ed6; }
.team-flow-arrow { ... }
```

---

## 涉及文件

```
assemble_team.go                      — 合成员 + 混合模式 + 失败不中断 + 代码重构
skills/team-roles/pm.md               — 新增 PM 规范
skills/team-roles/architect.md        — 新增 架构师规范
skills/team-roles/developer.md        — 新增 程序员规范
skills/team-roles/reviewer.md         — 新增 审查员规范
skills/team-roles/researcher.md       — 新增 研究员规范
skills/team-roles/analyst.md          — 新增 分析师规范
skills/team-roles/writer.md           — 新增 撰稿人/写手规范
skills/team-roles/strategist.md       — 新增 策划规范
skills/team-roles/editor.md           — 新增 编辑规范
web/app.js                            — 团队面板模式标签 + 文案更新
web/style.css                         — 模式标签 + 流程箭头样式
```
