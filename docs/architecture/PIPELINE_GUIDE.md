# OpenKitty Pipeline 系统文档

## 概述

Pipeline 是 OpenKitty 的声明式多步骤工作流引擎：

```
触发 → Step1(工具白名单) → 🔒审批 → Step2 → 🔒审批 → ... → 完成
 └─ SSE实时推送 ─┘  └─ 每步审计轨迹 ─┘  └─ DB持久化 ─┘
```

---

## 企业为什么需要 Pipeline（不是 Team，不是 LangGraph）

### 核心问题：AI 辅助企业流程，最怕什么？

**怕不可审计、怕越权、怕失控。** 

一个采购申请，AI 帮你写了采购单 → 很好。但：
- 谁让 AI 写的？什么时候？调了什么工具？
- AI 比价时搜了哪些网站？有没有被供应商 SEO 误导？
- 采购单金额超过预算线，谁批的？
- 如果出了合规问题，能拿出完整的操作记录吗？

这就是 Pipeline 要解决的问题。下面说清楚什么时候用 Pipeline，什么时候用别的。

---

### 选型决策表

| 你的场景 | 用 Pipeline | 用 Team | 用 LangGraph |
|----------|:-----------:|:-------:|:------------:|
| 采购/预算/合同审批，需要每步有人签字 | ✅ | ❌ | ❌ |
| 安全/合规/审计，需要全链路操作记录 | ✅ | ❌ | ❌ |
| 数据删除/跨境/隐私，需满足 GDPR/个保法 | ✅ | ❌ | ❌ |
| 发布上线检查清单，多环节有权限管控 | ✅ | ❌ | ❌ |
| 员工入离职，HR+IT+行政多部门串签 | ✅ | ❌ | ❌ |
| AI 帮我查资料、写分析报告 | ❌ | ✅ | 可选 |
| AI 帮我对比竞品、做 SWOT | ❌ | ✅ | 可选 |
| AI 自循环优化代码，不断重试直到通过 | ❌ | ❌ | ✅ |
| AI Agent 自主决策走哪条路 | ❌ | ❌ | ✅ |
| 多 Agent 并行讨论，各抒己见 | ❌ | ✅ | ❌ |

**一句话：**
- Pipeline = 受控工作流（流程固定、权限分明、操作留痕）
- Team = 开放式协作（多视角、自由探索、快速产出）
- LangGraph = 自主决策型 Agent（AI 自己判断、循环、分支）

---

### Pipeline 解决的企业刚需

#### 1. 权限隔离
财务审批步骤只能 `read_file`，不能 `write_file`。付款步骤才能 `cli`。**一个 Agent 走完全程，但不同阶段能力不同。**

#### 2. 操作留痕
每一笔工具调用都记录在 DB：谁在什么时间调了 grep 搜了什么文件、调了 cli 执行了什么命令、耗时多少毫秒。**做合规审计时不用翻日志，直接查 API。**

#### 3. 人机问责
审批事件记录：谁的审批、点了 OK 还是给了反馈。**出问题能追溯到是谁批的。**

#### 4. 断点恢复
审批等太久？关了浏览器也不丢。下次打开继续——因为每一步都持久化到 DB。

#### 5. 外部对接
工单系统通过 webhook 触发 pipeline、webhook 收到结果关闭工单。HR 系统 API 触发入职流。**不用人手动操作。**

#### 6. 容错降级
步骤失败不会让整个流程崩溃。API 模式下失败会回调 webhook 通知外部系统人工介入。

---

### Pipeline vs RPA vs BPM

| | Pipeline | RPA (UiPath等) | BPM (Camunda等) |
|------|:---:|:---:|:---:|
| AI 理解能力 | ✅ LLM 驱动 | ❌ 规则驱动 | ❌ 规则驱动 |
| 自然语言触发 | ✅ "帮我采购3台Mac" | ❌ 需结构化输入 | ❌ 需结构化输入 |
| 非结构化处理 | ✅ 合同/发票/报告 | ❌ 需OCR+模板 | ❌ |
| 审计轨迹 | ✅ 每步工具级 | 部分 | ✅ |
| 部署复杂度 | 单二进制+配置 | 重 | 重 |

Pipeline 是 **AI-native 的轻量 BPM**。不用画流程图，不用写规则引擎，用自然语言配置。

---

## 与 Team / LangGraph 的详细对比

| 维度 | Pipeline | Team | LangGraph |
|------|----------|------|-----------|
| **工具控制** | 每步独立白名单 | 全员共享全部工具 | 无内置白名单 |
| **权限审批** | HITL 阻点 + 审计 | 无 | interrupt() |
| **触发方式** | 关键词+API+Webhook | 显式调用 | 代码触发 |
| **持久化** | DB存储，断点可恢复 | 无 | Checkpointer |
| **进度可见** | SSE 实时每步+工具明细 | 一次性输出 | streaming |
| **审计轨迹** | ✅ 每步全记录 | ❌ | 需自建 |
| **条件分支** | ✅ 规则匹配 | ❌ | ✅ LLM或规则 |
| **循环重试** | ✅ max+condition | ❌ | ✅ cycles |
| **并行步骤** | ✅ fan-out/merge | 串行/并行 | ✅ |
| **LLM动态路由** | ❌ (可用标记+规则替代) | ❌ | ✅ |
| **适用场景** | 合规审批/DevOps/HR | 研究分析/创意 | 自主Agent |

---

## 审计系统

### 每步工具调用全记录

Pipeline 的所有 step 工具都通过 `auditTool` 透明包装，自动记录每次工具调用：

```json
{
  "steps": [{
    "name": "数据定位",
    "status": "completed",
    "output": "...",
    "tool_calls": [
      {
        "timestamp": "2026-06-11T10:00:01Z",
        "tool_name": "grep",
        "params": "{\"pattern\":\"user_id=138xxxx\",\"path\":\"./\"}",
        "result": "found in orders_db.sql:423, logs/app.log:1521",
        "duration_ms": 230
      },
      {
        "timestamp": "2026-06-11T10:00:04Z",
        "tool_name": "cli",
        "params": "{\"command\":\"SELECT * FROM users WHERE phone='138xxxx'\"}",
        "result": "1 row returned (masked)",
        "duration_ms": 150
      }
    ]
  }]
}
```

### 审批留痕

每次 HITL 审批都有完整记录：

```json
{
  "audit_log": [
    {
      "timestamp": "2026-06-11T10:00:05Z",
      "role": "数据定位",
      "action": "approved",
      "feedback": "ok"
    },
    {
      "timestamp": "2026-06-11T10:05:33Z",
      "role": "执行删除",
      "action": "feedback",
      "feedback": "先保留180天日志数据，其余可删"
    }
  ]
}
```

### 合规价值

以 `privacy-compliance` (GDPR删除)为例，审计轨迹可回答：
- 谁在何时批准了删除范围？
- Agent 具体读了哪些文件？
- 删除了什么数据（哪个表/哪些行/什么时间）？
- 备份存在哪里？
- 每步操作耗时多少？

满足 GDPR/个保法的数据处理活动记录要求。

---

## 全部 26 个 Pipeline

### 开发安全类（3个）

#### security-audit — 安全审计
```
项目扫描 → 代码审计 🔒 → 安全报告
```

#### code-review — 代码审查
```
架构评估 → 代码质量 🔒 → 改进报告
```

#### release-checklist — 版本发布检查
```
代码冻结 → 测试验证 🔒QA → 安全扫描 🔒安全 → 变更通告 → 部署 → 线上验证
```

---

### 财务类（4个）

#### procurement — 采购审批流
```
需求分析 → 供应商比价 🔒组长 → 生成采购单
```

#### budget-approval — 预算审批流
```
智能初审 → 风险分析 🔒总监 → 财务复核 🔒财务 → 生成批文
```

#### invoice-reimbursement — 发票识别+报销
```
OCR识别 → 真伪校验 → 合规检查 🔒财务 → 生成报销单
```

#### travel-approval — 差旅审批流
```
行程规划 → 预算预估 🔒部门 → 合规检查 🔒行政 → 预订+预填报销
```

#### payment-approval — 付款审批流
```
合同关联 → 验收确认 🔒PM → 财务复核+付款 🔒财务 → 回单归档
```

---

### IT运维类（4个）

#### incident-triage — 故障工单分流
```
信息收集 → 根因分析 🔒技术负责人 → 修复建议 → 复盘报告
```

#### db-change-review — DB变更审批
```
SQL审核 🔒DBA → 执行计划分析 → 备份确认+执行 🔒二次确认 → 验证
```

#### permission-request — 权限申请/回收
```
需求分析 → 最小权限设计 🔒安全 → 执行开通/回收 → 验证+通知
```

#### cert-renewal — TLS证书续期
```
到期检测 → 生成CSR+申请 → 部署 🔒确认 → 验证+通知
```

---

### 安全类（3个）

#### security-incident — 安全事件响应
```
范围评估 → 取证+遏制 🔒CSO → 根因调查 → 通知合规 🔒法务 → 复盘
```

#### pentest-remediation — 渗透测试修复跟踪
```
漏洞提取 → 分类分配 🔒负责人 → 修复验证 → 复测确认 🔒安全
```

---

### HR类（4个）

#### onboarding — 员工入职流转
```
信息核验 → 账号开通 🔒HR → 设备与权限 → 培训任务
```

#### offboarding — 员工离职流转
```
权限回收清单 → 数据交接 🔒上级 → 资产确认
```

#### recruitment — 招聘流程
```
JD优化 → 简历筛选 🔒HR → 面试题库 → Offer审批 🔒薪资 → 背调
```

#### performance-review — 绩效评估
```
数据收集 → 自评模板 🔒主管 → 360评估 → 面谈+归档 🔒HRBP
```

---

### 供应链/运营类（3个）

#### complaint-resolution — 客户投诉闭环
```
情绪识别 → 责任定责 🔒客服主管 → 回复方案 → 结案归档
```

#### inventory-check — 库存盘点
```
盘点任务 → 差异分析 🔒仓库主管 → 调账审批 🔒财务 → 报告归档
```

#### logistics-exception — 物流异常处理
```
取证收集 → 责任判定 🔒物流主管 → 赔付+补发 → 归档
```

---

### 法务合规类（4个）

#### contract-review — 合同审查
```
结构扫描 → 条款分析 🔒法务 → 合规报告
```

#### privacy-compliance — 数据删除合规（审计重点）
```
数据定位 🔒DPO → 隔离备份 → 执行删除 🔒DPO → 合规报告
```

#### ip-application — 知识产权/软著申请
```
代码提取 → 申请表填写 🔒法务 → 提交 → 进度跟踪
```

#### data-export-assessment — 数据出境评估
```
数据分类分级 → 必要性评估 🔒DPO → 安全评估 → 保护方案+报送 🔒法务
```

---

### 销售/BD类（3个）

#### vendor-qualification — 供应商准入评估
```
资质收集 → 背景调查 🔒法务 → 技术评估 🔒CTO → 评分推荐
```

#### quote-approval — 报价折扣审批
```
客户背景分析 → 利润率计算 → 竞对对比 🔒销售总监 → 审批单 🔒VP(低价时)
```

#### credit-assessment — 客户信用评估
```
企业查询 → 经营风险评估 → 信用评分 🔒风控 → 额度建议+风控提示
```

---

### 分析创作类（3个）

#### research-report — 研究报告
```
信息搜集 → 深度分析 🔒 → 生成报告
```

#### ppt-generation — PPT生成
```
内容策划 → 设计排版 → 视觉审核 🔒
```

#### competitor-analysis — 竞对分析
```
格局扫描 → 深度分析 🔒 → 策略输出
```

#### weekly-report — 周报自动生成
```
数据收集 → 内容生成 🔒主管 → 发送
```

---

## 使用方式

### REST API

```bash
# 触发 pipeline
curl -X POST http://localhost:8090/api/pipeline/run \
  -H "Content-Type: application/json" \
  -d '{"pipeline":"procurement","input":"采购3台MacBook Pro","mode":"interactive"}'

# 查询状态（含审计轨迹）
curl http://localhost:8090/api/pipeline/status/{exec_id}

# 审批
curl -X POST http://localhost:8090/api/pipeline/approve \
  -H "Content-Type: application/json" \
  -d '{"exec_id":"pipe_procurement_xxx","feedback":"ok"}'

# 带反馈的审批（step会重新执行）
curl -X POST http://localhost:8090/api/pipeline/approve \
  -H "Content-Type: application/json" \
  -d '{"exec_id":"pipe_procurement_xxx","feedback":"重新比价，只看京东自营和官方旗舰店"}'

# 列出所有 pipeline
curl http://localhost:8090/api/pipeline/list
```

### 飞书/钉钉/企微消息

```
/采购 MacBook Pro 3台      → procurement
/安全审查                   → security-audit
/发票报销 上传3张发票        → invoice-reimbursement
/DB变更 ALTER TABLE...      → db-change-review
/权限申请 实习生张三          → permission-request
/安全事件 疑似数据泄露       → security-incident
/招聘 Go后端高级工程师        → recruitment
/盘点 华东仓月盘             → inventory-check
/软著申请 OpenKitty v2.3    → ip-application
/数据出境 新加坡分析平台      → data-export-assessment
/预算申请 Q3推广费50万       → budget-approval
```

### API模式 + Webhook（对接外部系统）

```json
{
  "pipeline": "complaint-resolution",
  "input": "用户投诉：订单未收到但已扣款",
  "mode": "api",
  "notify_url": "https://your-system.com/webhook/pipeline",
  "session_id": "ticket_12345"
}
```
API 模式下，遇到 HITL 审批点时 pipeline 自动持久化到 DB 并退出 goroutine，通过 webhook 通知外部系统。审批后调用 `/api/pipeline/approve` 继续。

---

## 三种高级模式

### 条件分支（替代 LangGraph conditional edges）

```yaml
steps:
  - name: "智能初审"
    prompt: |
      ...最后注明：[路由: 大额] 或 [路由: 小额]
    routing:
      - when: "contains '大额'"
        goto: "VP审批"
      - when: ""
        goto: "部门审批"      # 空条件 = default

  - name: "部门审批"   # 小额走这里
    ...
  - name: "VP审批"      # 大额走这里
    ...
```

**适用**：金额分级审批、失败/成功分支、多级升链。

### 循环重试（替代 LangGraph cycles）

```yaml
steps:
  - name: "代码质量检查"
    prompt: |
      ...如果发现问题注明 [检测结果: 存在问题]
      如果合格注明 [检测结果: 通过]
    repeat:
      max: 3             # 最多执行3次（含首次）
      condition: "contains '存在问题'"
      on_max: "人工审核"  # 3次都不过，跳到人工
```

**适用**：质量门禁、自动修复+验证、提交→检查→修复→再检查。

### 并行步骤（替代 LangGraph fan-out）

```yaml
steps:
  - name: "质量安全验证"
    prompt: "并行执行以下检查"
    parallel:
      - name: "测试验证"
        prompt: "运行测试套件..."
        tools: ["cli", "grep"]
      - name: "安全扫描"
        prompt: "扫描漏洞..."
        tools: ["cli", "code_check"]
    # ← 自动等待全部完成，merge结果到 parent step
```

**适用**：测试+安全并行、多供应商并行比价、多系统并行检查。

---

## 配置参考

```yaml
pipeline:
  triggers:
    - when: "采购"           # 匹配消息中的关键词
      run: "procurement"     # 要执行的pipeline名
      channel: "feishu"      # 可选：限定渠道

  pipelines:
    procurement:
      name: "采购审批流"
      desc: "需求分析→供应商比价→采购单生成"
      steps:
        - name: "需求分析"
          prompt: |
            step提示词，支持 ${input}（用户输入）和 ${stepN.output}（前序步骤输出）
          tools: ["read_file", "cli"]       # 工具白名单
          max_iter: 8                        # 最大工具调用轮次
          model: "deepseek_model"            # 可选：指定模型
          require_approval: false            # 是否需要HITL审批

        - name: "供应商比价"
          prompt: |
            基于需求：${step0.output}
          tools: ["web_fetch"]
          max_iter: 8
          require_approval: true             # 在此暂停等人审批

        - name: "生成采购单"
          prompt: |
            需求：${step0.output}
            比价：${step1.output}
          tools: ["write_file"]
          max_iter: 5
```

## MCP 服务器集成

Pipeline 步骤可使用 MCP 工具（`mcp_` 前缀），实现跨服务扩展：

```yaml
mcp_servers:
  - name: "jira-mcp"
    url: "http://jira-mcp.internal:3000/mcp"
    enabled: true

  - name: "python-tools"
    command: "python"
    args: ["mcp_server.py"]
    enabled: true
```

MCP 工具的每次调用同样被审计包装器记录，形成完整轨迹。
