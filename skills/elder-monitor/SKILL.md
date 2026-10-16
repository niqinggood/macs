---
name: elder-monitor
description: >-
  Analyze multimodal elder-care observations from camera/video, audio, millimeter-wave radar, motion sensors, voice checks, and device-health signals; normalize them into auditable events, apply deterministic safety rules, and return risk levels and caregiver actions. Use for 养老老人监护、居家养老、独居老人看护、跌倒/倒地、痛苦发声、明确求助、长时间低活动、无人响应、设备离线、批量复盘或这些流程的测试。Do not use as a medical diagnosis, as continuous emergency-service coverage, or as the sole authority for dispatch.
metadata:
  version: "1.2.0"
  author: "OpenKitty"
  aliases: [elder, elder-care-monitor, senior-monitor, 老人监护, 养老监护, 居家养老监控, 独居老人监控, 跌倒监测]
  tags: [elder care, multimodal, monitoring, fall detection, audio, video, mmwave, 老人, 养老, 监护, 多模态, 跌倒, 求助, 无响应, 风险分级]
  read_when: [用户要求分析老人视频音频或传感器事件、判断跌倒求助无响应风险、设计养老监护流程或复盘监控告警]
---

# 老人多模态监护

把摄像头、音频、毫米波/动作传感器、语音确认和设备状态转换成可审计风险决策。安全关键结论由确定性规则先行；多模态模型只提取或复核观察，不诊断疾病，也不能压低或延迟高风险规则。

## 安全不变量

- 只报告观察事实和风险假设，不根据姿态、声音或行为诊断疾病、认知状态、醉酒、情绪或疼痛原因。
- 明确求助，或疑似跌倒/倒地后无响应时，立即返回预授权紧急升级动作；不得等待云模型。
- 告警投递与风险评估分离。除非调用方明确授权，不自行联系家属、照护机构或急救服务。
- 阈值仅为软件演示默认值。真实部署优先使用个人基线和经照护团队审批的策略。
- 每个结论必须保留时间窗、证据来源、置信度、未知项、策略版本和触发规则。
- 设备离线是“监护覆盖缺失”，不是“老人正常”或“老人无响应”。
- 数据最小化：使用化名 ID，不把姓名、电话、证件、精确住址、原始音视频或密钥写入模型提示、日志和测试样本。
- 涉及真实家庭、机构或老人数据时先读 [隐私、同意与人工复核](references/privacy-and-human-review.md)。

## 支持的多模态输入

| 模态 | 允许归一化的观察 | 不允许直接推断 |
|---|---|---|
| 视频/图像 | 姿态、是否疑似倒地、场景遮挡、观察置信度 | 疾病、身份、意识状态、受伤程度 |
| 音频 | 痛苦样发声次数、明确求助意图、噪声/服务失败 | 疼痛病因、精神状态、说话人身份 |
| 毫米波/动作 | 活动水平、姿态、持续时间、相对个人基线 | “生命体征正常”、具体疾病 |
| 语音确认 | 是否响应、`okay/need_help/unknown`、服务状态 | 服务报错等于无人响应 |
| 设备状态 | 离线、陈旧、遮挡、错误 | 老人状态正常或异常 |

不熟悉的传感器或生命体征设备先读 [事件契约](references/event-contract.md)。没有定义物理量、单位、置信度、失败状态和处置策略前，不把它塞进现有类型或直接触发告警。

## 评估一个事件窗口

1. 原始描述、新传感器或陌生载荷先按 [事件契约](references/event-contract.md) 归一化；每人、每个观察窗口生成一个事件。
2. 保存为符合 `schemas/event.schema.json` 的 JSON，并验证：

   `python scripts/validate_event.py <event.json>`

3. 运行确定性策略：

   `python scripts/evaluate_event.py <event.json> --pretty`

4. 仅当结果要求语音确认或可选模型复核时，调用已授权适配器；接入前读 [集成契约](references/integrations.md)。
5. 返回符合 `schemas/assessment.schema.json` 的对象；添加自然语言说明时不得删除规则输出字段。

若用户只给自然语言且当前环境不能运行脚本，仍按同一事件契约和规则顺序手工归一化，并明确标注“未执行本地验证脚本”。不要虚构脚本结果。

## 固定交付格式

默认面向照护人员按以下顺序输出；调用方要求机器可读时只返回 assessment JSON：

1. **一、风险结论**：`none/low/medium/high/critical`、一句摘要、是否需要告警。
2. **二、证据时间线**：按时间列观察、来源、置信度和证据引用；只写传感器事实。
3. **三、不确定性与冲突**：噪声、遮挡、数据陈旧、模态矛盾、缺少个人基线或语音未确认。
4. **四、处置动作**：严格按优先级列动作、超时和是否需要人工确认；未获授权的外部通知写成“待授权”，不能假称已发送。
5. **五、审计信息**：事件 ID、策略版本、触发规则、模型名称/版本（若使用）和“非医疗诊断”。

## 风险等级

- `none`：保留审计记录；不告警。
- `low`：安排复查或进入趋势观察。
- `medium`：语音确认或通知监控台；设备离线走独立运维告警。
- `high`：立即确认并通知已授权照护人，要求回执。
- `critical`：立即执行预授权紧急升级；云分析只能补充，不能阻塞。

严格按 [风险策略](references/risk-policy.md) 的优先级解释。普通坐姿十分钟不能单独触发异常；重复痛苦样发声与低活动共同出现时才可能升级。

## 批量复盘与部署

JSONL 批量复盘：

`python scripts/review_batch.py <events.jsonl> --pretty`

批量任务用于趋势、未闭环告警和设备覆盖复查，不替代实时跌倒、明确求助和无响应事件。触发器、队列、离线能力、灰度上线和人工闭环见 [部署模式](references/deployment.md)。

上线前使用合成信号一次跑完正常、久坐、痛苦发声、跌倒、跌倒后无响应、明确求助、设备离线、噪声误报和语音服务失败：

`python scripts/simulate_agentos.py --mode local --pretty`

人工查看每个场景的业务含义、判定原因和实际发送 JSON，并逐项执行测试：

`python scripts/test_elder_monitor_scenarios.py --mode local --output elder-monitor-test-report.json`

已部署 AgentOS 的端到端验证命令、认证方式和安全边界见 [集成契约](references/integrations.md)。模拟器永远只要求返回研判结果，不执行结果里的通知或紧急处置建议。

## 多模态模型使用

- 模型只提取/复核姿态、痛苦样发声、场景上下文或遮挡等歧义观察。
- 送云端前运行 `python scripts/redact_cloud_payload.py <event.json> --pretty`；不得上传原始身份或精确位置。
- 记录模型名、版本、置信度和脱敏证据引用。
- 模型失败不得降低或延迟 `high/critical`；模型不得把设备故障改写成“无人响应”。

## 修改与验证

改变风险等级、阈值或优先级前先读 [风险策略](references/risk-policy.md)，更新策略版本、添加能说明改动原因的 fixture，并运行：

`python -m unittest discover -s tests -v`

未通过全部黄金用例，或照护团队尚未审批阈值、确认时限、联系人、同意、留存与离线方案时，不得宣称可用于真实试点。

## 参考资料

- [事件契约](references/event-contract.md)：归一化与字段语义。
- [风险策略](references/risk-policy.md)：规则优先级、等级和调参约束。
- [集成契约](references/integrations.md)：语音、多模态、告警和 OpenKitty 接口。
- [部署模式](references/deployment.md)：实时路径、定时复盘和上线阶段。
- [隐私、同意与人工复核](references/privacy-and-human-review.md)：家庭/机构监控的最低治理要求。
