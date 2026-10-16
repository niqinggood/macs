# Patient-facing interpretation and care-navigation report

Use this guide for a formal report or whenever detailed triage/department routing is needed. It does not impose report headings on ordinary consultation dialogue.

## Delivery shapes

- **自然诊聊**（普通 `med_guid` 对话默认）：第一句直接写判断，不加 `核心判断：`、`结论：` 标签；第二段最多解释 2 个最可能的西医原因，并压缩补充 1 个中医待辨方向；最后以 `先做：` 给至多 3 项行动。通常 220–340 个中文字符、最多 3 段，信息少就更短。科室只给一个首选路径。只有一个答案会改变判断的问题时才加 `还需确认：`，且一个问句只问一件事，不把多个问题用 `以及/和/或` 拼接。`是什么、为什么、怎么办`只是内部覆盖检查。不要寒暄、自我介绍、复述、逐项解读正常数据、罗列所有原因/证型/科室、泛化急症清单、总结全文或重复免责声明；不用标题、分隔线、表格和连续项目符号。
- **报告模式**：用户明确要求报告、标准版/精简版/完整版，或需要交付可保存的正式解读时，再使用下面的结构化模式。

- **精简版**: keep one bottom line, up to two priority findings, one short TCM line, and up to three actions. Do not include a background section, normal-item list, or generic disclaimer.
- **标准版**: use at most five top-level sections—`结论`、`关键依据`、`中西医判断`、`下一步`、optional `需关注`—with no nested heading tree. Include only content that changes interpretation or action; omit empty sections.
- **完整版**: use the formal template below. Include all material findings and uncertainties, and place exhaustive supplied metrics in an appendix so the main conclusion remains easy to find.

Changing modes changes detail, not conclusions or evidence labels. Reuse the same normalized facts when rerendering.

## Normalized case snapshot

For records not handled by a deterministic normalizer, extract one internal snapshot before drafting. Do not expose this structure unless useful, but keep it stable across mode changes:

- `source_type` and source date/time when supplied;
- `facts[]`: source label, value/text, unit, supplied reference range, evidence label, source location, and uncertainty;
- `timeline[]`: only dated events actually present in the record;
- `current_symptoms[]` and `red_flags[]`: preserve “not provided” separately from an explicit denial;
- `known_conditions[]`, `medicines[]`, and `allergies[]`;
- `tcm_observations[]`: only supplied cold/heat, sweating/thirst, appetite/digestion, stool/urine, sleep/energy, pain, menstrual, tongue, pulse, and symptom-course observations; keep unobserved domains as “not provided”;
- `quality_issues[]`: OCR uncertainty, missing units/ranges, conflicting duplicates, device warnings, or unreadable sections;
- `missing_critical_context[]`;
- one `urgency` assessment with its supplied evidence and uncertainty;
- `actions[]`, department rationale, and clinician questions derived from that same snapshot.

Never silently choose between conflicting duplicate values. Mark both as conflicting and keep them out of conclusions until confirmed. Treat nonnumeric, unitless, physically impossible, or unreadable values as `待确认`; do not repair them by guessing. If no usable fact remains, produce a limitation-aware preparation note rather than the normal test/department template.

Before delivering a formal rendered mode, check these invariants:

- the bottom line, reasoning, and next actions are present; literal `是什么`、`为什么`、`怎么办` headings are required only when that formal structure was requested;
- urgency, bottom line, evidence labels, and quality warnings match the snapshot;
- concise has no more than three priority findings and three actions;
- standard contains confirmation steps and routing rationale but no exhaustive appendix;
- full follows the formal template and reports appendix omissions explicitly;
- no unrelated model percentages are ranked by magnitude;
- no quarantined value, unsupported threshold, identifier, or access URL is emitted.
- any TCM `证候倾向` is supported by four-examination/symptom-course facts, explicitly uncertain, and does not alter clinical urgency.
- every Chinese standard/full report and `med_guid` session contains a visible TCM initial-differentiation section; device/lab-only input uses evidence tier C and `待辨方向` rather than silently omitting TCM content.

## Urgency screen

Lead with emergency guidance when the current presentation suggests a time-sensitive threat, such as new severe chest pressure, severe difficulty breathing, fainting with ongoing symptoms, new one-sided weakness or speech difficulty, sudden severe confusion, uncontrolled major bleeding, a severe allergic reaction, or current intent to self-harm. Tell the user to contact local emergency services or go to the nearest emergency department; do not let a long report delay care.

For an extreme value from an unvalidated consumer device without symptoms, advise immediate repeat measurement with an appropriate validated device and escalation according to the repeat value and symptoms. Do not declare or dismiss an emergency from the algorithmic estimate alone.

Use one of these routing labels and explain why:

- **立即急诊**: active red flags or a credible time-sensitive threat.
- **尽快线下评估**: new/worsening symptoms, a seriously abnormal confirmed value, or a finding that should not wait for routine follow-up.
- **计划门诊复核**: stable, non-emergency abnormalities that need confirmation or longitudinal assessment.
- **日常随访/健康管理**: no red flags and no clinically meaningful confirmed abnormality.

Do not assign a precise deadline unless it follows from a cited guideline or the clinical context supplied by the user.

## Evidence labels

Label every important finding as one of:

- **病历原文** — explicitly documented diagnosis, symptom, medication, or clinician assessment;
- **直接测量/检验** — a standard measurement or laboratory result with unit and context;
- **计算值** — derived from provided inputs using a stated method;
- **设备估算** — consumer-device or camera-derived estimate;
- **模型风险估算** — algorithmic risk or proprietary score;
- **待确认** — missing, ambiguous, OCR-uncertain, unitless, or lacking a usable reference range.

Never upgrade a lower-confidence class through wording alone.

## Full report template（仅完整版）

Do not use this nested template for an ordinary `报告` or `标准版` request; those use the compact five-section standard shape above.

```markdown
# 健康资料解读与就医准备报告

## 结论先行
[One-sentence summary: urgency + most actionable issue + primary limitation]

## 一、是什么

### 紧急程度
- 级别：立即急诊 / 尽快线下评估 / 计划门诊复核 / 日常随访
- 依据：...

### 资料与可信度
- 资料来源：...
- 已知时间线：...
- 数据质量问题：...
- 缺失但会改变判断的信息：...

### 重点发现
| 优先级 | 发现 | 证据类型 | 通俗解释 | 不确定性 |
|---|---|---|---|---|

### 西医视角
[Clinical meaning of the prioritized findings and the main questions still requiring confirmation.]

### 中医补充视角
[State A/B/C evidence tier. For A/B, give one leading preliminary pattern and one alternative. For C, cover all usable data domains (normally 3–6 groups), expand each group into a few relevant sub-directions, and show the diagnostic route from 八纲 to 病位、气血津液 and 脏腑/兼夹. Each group needs decisive symptom/tongue/pulse questions and must remain unranked. Do not omit this section merely because four-examination data is absent.]

## 二、为什么

### 西医机制链
[Explain what each prioritized finding represents, why it may matter, and why the available evidence cannot confirm a diagnosis. Include supporting and contradicting facts. Do not call correlated outputs independent evidence.]

### 中医病机线索
[Explain at most one leading pattern tendency and one alternative from supplied observations. State what supports and weakens each. Do not present this as a translation of the Western mechanism.]

## 三、怎么办

### 下一步
1. 需要先复测/确认的项目：...
2. 建议首诊科室：...
3. 就诊时携带：原始报告、用药清单、过敏史、连续测量记录等

### 中西医结合的安全做法
- 可共同采用的低风险生活措施：...
- 中医调养参考：按饮食、起居、运动、情志和低风险穴位保健组织；先写通用建议，再写带条件的方向性建议，不把食疗写成药方。
- 若希望中医进一步评估，建议记录/携带的四诊与症状变化：...
- 可向合格中医师、医生或药师讨论的问题：...

### 问医生的问题
- ...

### 需要立即改变计划的警示症状
- ...

## 适用范围与依据
[Not a diagnosis; list guideline/source names, jurisdiction, dates, and links]
```

Keep the first page useful. Move exhaustive normal items to an appendix or omit them.

For concise mode, preserve the TCM distinction in at least one line. Standard/full Chinese reports and `med_guid` sessions must show the TCM section. An emergency-first answer or single-metric lookup may defer it, but a broader case answer must not silently omit it.

## Department routing

Prefer primary care/general internal medicine when several systems are involved or confirmation is still needed. Route more specifically only when the record supports it:

- cardiology: persistent confirmed blood-pressure abnormality, rhythm concern, exertional chest symptoms, syncope, or established cardiovascular disease;
- endocrinology: confirmed glucose/HbA1c abnormality, difficult metabolic disease, or complex obesity/endocrine questions;
- gastroenterology/hepatology: confirmed liver-test abnormalities, imaging evidence of liver disease, or persistent relevant symptoms;
- neurology: focal neurologic symptoms or established neurologic findings; emergency routing overrides routine clinic selection;
- psychiatry/clinical psychology: significant anxiety/depression symptoms or functional impairment; current self-harm intent requires emergency/crisis support;
- the department named by the treating clinician: when an existing referral or follow-up plan is already documented.

Avoid routing solely from a consumer-device risk score. Recommend confirmation first unless symptoms or verified records independently support the specialty.

## Evidence and privacy references

Use current primary/authoritative sources. Useful starting points:

- WHO, *Ethics and governance of artificial intelligence for health* (2021): https://www.who.int/publications/i/item/9789240029200
- WHO, AI-for-health regulatory considerations (2023): https://www.who.int/news/item/19-10-2023-who-outlines-considerations-for-regulation-of-artificial-intelligence-for-health
- China, *Chinese Guidelines for the Prevention and Treatment of Hypertension (2024 revision)*: https://pmc.ncbi.nlm.nih.gov/articles/PMC11937835/
- National Health Commission, `WS/T 428-2013 成人体重判定`: https://www.nhc.gov.cn/wjw/yingyang/201308/a233d450fdbc47c5ad4f08b7e394d1e8.shtml
- National Health Commission, *医疗卫生机构网络安全管理办法* (2022): https://www.nhc.gov.cn/guihuaxxs/c100133/202208/8a23d01133214a779879094dd20cd383.shtml
- PRC Personal Information Protection Law, Article 28 (medical/health information is sensitive personal information): https://www.cac.gov.cn/2021-08/20/c_1631050028355286.htm
- WHO, *Global traditional medicine strategy 2025–2034* (2025): https://www.who.int/publications/i/item/9789240113176
- NCCIH, *Traditional Chinese Medicine: What You Need To Know*: https://www.nccih.nih.gov/health/traditional-chinese-medicine-what-you-need-to-know
- China, *中华人民共和国中医药法释义（十二）* (四诊与辨证论治): https://www.yunfu.gov.cn/zyyj/zwgk/bmwjyjd/content/post_1493418.html

Check for newer revisions and population-specific guidance before applying thresholds. State whether a source is a clinical guideline, vendor definition, law/policy, or general patient-education reference.
