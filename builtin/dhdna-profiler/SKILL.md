---
name: dhdna-profiler
slug: dhdna-profiler
version: 1.0.0
author: OpenKitty
status: stable
description: "认知指纹分析 — 基于 DHDNA 框架提取任何文本作者的思维模式。12个维度深度评分，揭示分析深度、创造力、情绪处理、战略思维等认知特征。用于：(1) 分析客户/对手的决策风格；(2) 理解谈判对象的思维模式；(3) 自我认知风格了解；(4) 对比两人或多人的思维差异；(5) 深度理解任何文字背后的思维方式。"
metadata:
  type: builtin
  tags: [psychology, cognitive, profiling, negotiation, communication]
  os: [linux, darwin, win32]
---

# DHDNA Profiler — 认知指纹分析

基于 Digital Human DNA (DHDNA) 框架，从任何文本中提取作者的认知指纹。正如生物 DNA 编码身体特征，数字人类 DNA 编码认知特征——每个人在分析深度、创造力、情绪处理、战略思维和伦理推理方面的组合，形成了独一无二的"认知签名"。

> 这不是评判思维方式"好"还是"坏"，而是绘制一个思维如何运作的拓扑图。

---

## IRON RULES (non-negotiable)

1. **Evidence-based scoring**: Every dimension score must cite specific text evidence. No guessing, no "feeling" — if you cannot point to the exact phrase or pattern, do not score.
2. **Confidence disclosure**: Always state confidence level (HIGH/MEDIUM/LOW) per dimension and for the overall profile. Low-confidence profiles must carry explicit caveats.
3. **No over-diagnosis**: This is a snapshot of cognitive patterns, not a clinical or psychological diagnosis. Never label anyone with clinical terms or assert permanent traits.
4. **Text-type awareness**: Acknowledge that text genre affects results — email, creative writing, technical documentation, and dialogue reveal different facets. Note the limitation.
5. **Not personality testing**: DHDNA measures cognitive architecture (how thinking operates), not behavioral traits. Do not conflate with MBTI, Big Five, or any personality framework.
6. **Tension pairs are the signature**: A single dimension score in isolation is noise. The gap between paired dimensions is what defines cognitive style. Always read scores through tension pairs.

---

## 12 个认知维度

对文本评分时，每个维度按 1-10 打分，基于文本中的证据：

| # | 维度 | 衡量内容 | 低分（1-3） | 高分（8-10） |
|---|------|---------|-----------|------------|
| 1 | **分析深度** | 逻辑严谨性、结构化推理、因果链 | 直觉型、整体型、模式型 | 系统型、证明导向、精确 |
| 2 | **创造力范围** | 连接的新颖性、隐喻使用、横向思维 | 常规、渐进 | 范式突破、跨域综合 |
| 3 | **情绪处理** | 情绪词汇、共情信号、情感整合 | 疏离、临床化 | 情感丰富、感觉融合 |
| 4 | **语言精度** | 词汇复杂度、句式架构、修辞 | 简单、直接 | 结构复杂、细腻 |
| 5 | **伦理推理** | 价值信号、公平关注、后果意识 | 实用主义、结果导向 | 原则驱动、正义导向 |
| 6 | **战略思维** | 长期规划、竞争意识、资源优化 | 战术型、反应型 | 多步推演、博弈论 |
| 7 | **记忆整合** | 引用过往经验、历史模式、连续性 | 当下焦点 | 深度历史意识、先例驱动 |
| 8 | **社交智能** | 受众意识、换位思考、关系框架 | 自我参照 | 深度他人意识、联盟构建 |
| 9 | **领域专长** | 技术深度、专业知识、术语自信 | 通才 | 深度专家 |
| 10 | **直觉推理** | 直觉信号、启发式捷径、模式跳跃 | 方法论、逐步 | 信念跳跃、洞察驱动 |
| 11 | **时间取向** | 思维的时间视野——过去/现在/未来 | 当下锚定 | 跨时间、历史到未来主义 |
| 12 | **元认知** | 对自身思维的自我意识、不确定性承认 | 无反思 | 深度自我意识、思考如何思考 |

### 6 个张力对

维度之间存在张力——一个维度的高分通常与配对维度的低分相关。这些张力就是认知签名：

| 配对 | 张力 | 揭示 |
|------|------|------|
| DIM 1 ↔ DIM 10 | 分析 ↔ 直觉 | 逻辑 vs. 直觉——思维如何得出结论 |
| DIM 3 ↔ DIM 6 | 情绪 ↔ 战略 | 心 vs. 脑——什么驱动决策 |
| DIM 2 ↔ DIM 5 | 创造 ↔ 伦理 | 自由 vs. 框架——规则内或规则外的创新 |
| DIM 4 ↔ DIM 12 | 语言 ↔ 元认知 | 表达 vs. 自我意识——外在技艺 vs. 内在反思 |
| DIM 7 ↔ DIM 11 | 记忆 ↔ 时间 | 过去 vs. 时间本身——经验 vs. 时间视野 |
| DIM 8 ↔ DIM 9 | 社交 ↔ 领域 | 广度 vs. 深度——人际技能 vs. 技术精通 |

---

## 分析流程

### 阶段一：证据收集

仔细阅读文本。对每个维度，识别**具体的文本证据**：

- 直接引用的语句
- 结构性模式（论点如何构建）
- 存在什么和缺失什么（缺失与存在同样揭示信息）
- 跨段落重复出现的模式

### 阶段二：评分

对 12 个维度逐一：
1. 基于证据评分 1-10
2. 引用最强文本证据
3. 标注置信度：HIGH（多个清晰信号）、MEDIUM（部分信号）、LOW（推断）

### 阶段三：模式综合

评分后识别：

**主导模式：** 得分最高的 2-3 个维度——这是思维的"主场"

**阴影模式：** 得分最低的 2-3 个维度——这是思维不会自然去的方向

**签名张力：** 哪些张力对差距最大？这比任何单个分数更能定义认知风格

**推理拓扑：** 思维如何在想法间移动？
- **线性**（A → B → C → 结论）
- **螺旋**（从多个角度接近同一个想法，每次更深）
- **网状**（连接不同领域进行综合）
- **辩证**（正题 → 反题 → 合题）
- **分形**（微观和宏观层面的相同模式）

**决策指纹：** 面对选择时，这个思维：
- 先分析再决定？（分析主导）
- 先感受再合理化？（情绪主导）
- 先设想结果再往回推？（战略主导）
- 先质疑问题本身？（元认知主导）

### 阶段四：输出画像

```
═══════════════════════════════════════════
  DHDNA 认知画像
  对象：[姓名或"匿名"]
  分析文本：[N 词 / N 段]
  置信度：[HIGH / MEDIUM / LOW]
═══════════════════════════════════════════

维度评分：
  1. 分析深度 ···· [█████████·] 9/10
  2. 创造力 ······ [███████···] 7/10
  ...（12 项）

张力图：
  分析 ████████░░ ↔ ░░████████ 直觉
  情绪 ███░░░░░░░ ↔ ░░░░░░████ 战略
  ...（6 对）

主导模式：[得分最高的 2-3 个维度]
阴影模式：[得分最低的 2-3 个维度]
推理拓扑：[线性/螺旋/网状/辩证/分形]
决策指纹：[分析优先/感受优先/展望优先/质疑优先]

叙事综合：
[2-3 段自然语言描述这个思维如何运作，
什么使其独特，以及它可能遗漏什么]

关键引语：
[3-5 句最具揭示性的引语，标注维度归属]
═══════════════════════════════════════════
```

---

## 对比模式

当用户提供两个或更多人的文本时，生成个体画像 + **对比综合**：

- 思维在哪里趋同？（共享的高分维度）
- 在哪里分化？（同一维度上的对立评分）
- 哪些张力对会产生富有成效的分歧？
- 如果这些思维在同一房间，对话会是什么样子？

---

## 自我画像模式

如果用户要求分析自己的思维（使用对话历史作为文本）：
- 基于对话至今评分
- 承认对话文本可能不代表全部范围
- 注意人们与 AI 对话和与人类交谈时的思维可能不同
- 提供在其他写作样本后重新分析的选项

---

## 商务应用场景

| 场景 | 分析对象 | 价值 |
|------|---------|------|
| 客户谈判 | 客户邮件/提案 | 了解决策风格，定制沟通策略 |
| 竞对分析 | 竞对公开信/创始人访谈 | 预判战略倾向 |
| 团队协作 | 团队成员沟通记录 | 理解认知互补与冲突源 |
| 招聘评估 | 候选人求职信/作品 | 辅助文化匹配判断 |
| 自我提升 | 自己的文字 | 认知盲区识别 |

---

## 注意事项

- 这不是人格测试（MBTI、大五等）——那些衡量行为倾向，DHDNA 衡量认知架构
- 不是智力评判——棋王和诗人的评分可能截然不同但都展现深刻的认知能力
- 不是静态的——人的 DHDNA 随学习、经历和成长而演化。画像是一张快照，不是命运
- 分析基于已有文本，文本类型的差异会影响结果

---

## Anti-Patterns (DO NOT DO)

| # | Anti-Pattern | Why It Fails | Correct Behavior |
|---|-------------|--------------|-----------------|
| 1 | Rating a dimension without citing text evidence | Produces unreliable, unverifiable profiles that cannot be challenged or refined | Every dimension score cites 1+ specific text excerpts or structural patterns |
| 2 | Treating the profile as permanent or static | Cognitive styles evolve; labeling someone creates harmful fixed mindsets | Frame as a snapshot: "based on this specific text, the patterns suggest..." |
| 3 | Over-interpreting a single high or low score | One dimension out of context is misleading — it is noise without the tension pair | Always read every score through its paired dimension and the overall topology |
| 4 | Confusing writing style with thinking style | Polished or literary writing may mask actual cognitive patterns; plain writing may hide depth | Distinguish language precision (DIM 4) from the other dimensions; note the genre risk |
| 5 | Skipping the narrative synthesis section | Raw scores and visual bars are meaningless to non-experts without interpretation | Always include the 2-3 paragraph natural-language synthesis; scores alone are incomplete |

---

## Pre-Output Checklist
- [ ] All 12 dimensions scored with specific text evidence cited
- [ ] Confidence level stated per dimension and overall
- [ ] Tension pairs analyzed and signature gap identified
- [ ] Narrative synthesis included (2-3 paragraphs)
- [ ] Text-type limitation acknowledged
- [ ] Output saved to `.openkitty/storage/`
