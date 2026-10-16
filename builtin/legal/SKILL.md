---
name: legal
description: Route legal research, regulatory updates, contract review, compliance, dispute analysis, and legal drafting to the appropriate built-in legal skill.
metadata:
  version: "2.0.0"
  author: "OpenKitty"
  tags: [legal, contract, NDA, compliance, law, agreement, clause, 合同, 法律, 法务, 合规, 诉讼]
  read_when: [需要在法律子技能之间路由]
---

# Legal Skills

## 路由

- 使用 `legal-assistant` 处理法律检索、法规动态、常见法律问题、合同审查/起草、合规映射、争议与证据分析、法律文书辅助。
- 使用 `understand-contracts` 处理长合同、多合同对比、条款知识图谱和七阶段结构化提取；其法律结论仍应按 `legal-assistant` 的权威法源规则核验。

默认直接在聊天中回答；只有用户明确要求文件时才写入 `.openkitty/storage/`。不要编造法条、案号、期限、金额、监管状态或来源链接。不得自动改写或脱敏用户提供的原始材料；需要公开副本时另行提示并按用户要求制作。

法律检索要从官方原文确认适用法域、效力状态、施行时间和事实发生时点。高风险事项仍应完成可做的初步分析、证据清单和律师交接问题，不以空泛免责声明结束。
