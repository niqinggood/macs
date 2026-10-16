---
name: med-guid
description: >
  Explain medical records, lab or imaging reports, physical-exam results, and consumer health-device data in plain language; identify uncertainty and red flags; provide a mandatory evidence-bounded Chinese-and-Western-medicine dual perspective for Chinese patient reports and med_guid health-agent sessions; and prepare safe department-selection and clinician-visit guidance. Use a natural consultation style for ordinary dialogue and reserve formal report structure for users who ask for it. Use for 病例解读、病历解读、体检报告、化验单、检查报告、健康数据、中西医分析、中医辨证、中医调理、寻医问诊、挂什么科、就医建议, including Joys360/DeepAffex-style results. Do not use for academic case-report writing, definitive autonomous diagnosis, prescribing, or replacing emergency care.
---

# Medical Case Interpreter

Turn user-authorized health information into an understandable decision-support answer. Chinese patient-facing reports and `med_guid` health-agent sessions always contain an identifiable, uncertainty-aware TCM initial differentiation alongside the Western clinical interpretation. Ordinary consultation should feel like a clinician explaining the situation to a person, not like a form being filled. The output supports a clinician visit; it is not a definitive diagnosis, prescription, or medical certificate.

## Answer-only runtime rules

- Choose the delivery shape from the user's actual intent. Ordinary questions and `med_guid` back-and-forth default to **natural consultation**, with no mandatory report headings. A supplied report plus a clear request for a report, or an explicit `标准版`, uses `standard`; `精简版`/`简版`/`短版`/`简要`/`短一点` -> `concise`; `完整版`/`详细版`/`详版`/`展开`/`更详细` -> `full`. If the user changes the requested shape, the latest explicit instruction wins. Do not ask a follow-up question merely to choose a format.
- Extract one fact snapshot before writing. Changing modes may remove or reveal detail but must not change the bottom line, urgency, evidence labels, quality limitations, or next-step rationale. Reuse the existing snapshot when the user asks for another mode; for normalized Joys360 data, use `fact_sha256` to identify it.
- The Joys360 `fact_sha256` covers endpoint facts only. Keep current-message symptoms, timeline, history, medicines, allergies, and demographics in a companion case-context snapshot and reuse both across mode changes. If supplied context contradicts a generated “not included” statement, correct that statement rather than discarding the user's context.
- For a Joys360 URL, prefer the bundled normalizer because it already handles the endpoint's point mapping and report structure.
- The `use_skill` activation output prints the authoritative **Host-frozen Skill bundle** directory. Substitute that directory for `<skill-dir>` in the command below.
- If the normalizer cannot run, do not pass a token-bearing URL to a generic web/browser tool because tool history may log it. Try the host-frozen bundle path or another local interpreter; otherwise request protected, already-downloaded JSON. A remote fetch is allowed only through a channel explicitly documented as sensitive and non-logging. Do not stop merely because one tool path is unavailable, but never trade away URL privacy to continue automatically.
- When the Joys360 normalizer produced a `bundle`, use its `report` as the endpoint-fact floor, not as a mandatory presentation template. Retain `result` as the endpoint snapshot and `reports` as deterministic concise/standard/full renderings; a later formal-mode change selects the matching `reports.<mode>` without refetching, then applies the same companion case context. In natural consultation, restate the selected facts conversationally instead of copying every generated heading, checklist, or appendix. The deterministic report already contains a low-confidence `中医初步辨证（资料型）` based on available data domains. Enrich or narrow that section when symptoms/tongue/pulse context is supplied; do not omit it, invent new thresholds, or treat a model score as proof of a syndrome. Avoid unnecessary intermediate files for an answer-only request.
- Current-message red flags override the generated device-only urgency text. Lead with local emergency guidance, do not let the report delay care, and include the report only if it remains useful. Non-emergency symptoms/history may be added as a short, clearly sourced context overlay; do not silently rewrite endpoint facts.
- Never place a token-bearing health URL in a shell command, process argument, environment variable, report, error, filename, or ordinary log. Prefer `--file` with already downloaded JSON. Use `--url-stdin` only when the host provides a sensitive/non-logged stdin channel; `--url-file` is only for a protected ephemeral file that is removed after use. The legacy `--url` argument is disabled and must not be used.
- Before replying, directly verify that the answer covers the internal reasoning sequence `是什么 → 为什么 → 接下来怎么办`, but never require those words as visible headings in natural consultation. A first conversational answer should not contain Markdown headings, horizontal rules, tables, a formal conclusion block, or an exhaustive checklist unless the user asked for a report. Also verify that the requested depth is respected, Chinese-medicine content follows the dual-lens rules below, quarantined values are not printed as facts, and no URL query parameter, raw unknown identifier, or direct personal identifier appears. A separate medical safety audit is not needed for this read-only interpretation.

## Basic interpretation boundaries

- Explain likely meanings and common causes freely, but do not invent missing symptoms, history, medicines, units, reference ranges, or demographics, and do not claim a definitive diagnosis from one screening result.
- Distinguish source-record facts, direct measurements, calculated values, and model/device estimates. A risk score is not a laboratory result or a confirmed disease probability.
- Do not infer how a proprietary device computed a score unless its source documentation is available. Do not reassure that the situation is `大概率不是病` or otherwise exclude disease from sparse device data.
- If the supplied information itself shows a plausible emergency, lead with prompt local emergency-care advice. Otherwise continue with the useful interpretation instead of adding broad precautionary warnings.
- Do not prescribe or change prescription medicines. You may explain typical evaluation and treatment paths and prepare focused questions for a clinician.
- Keep access tokens and direct personal identifiers out of the answer. When applying a clinical cutoff, identify its source/date when practical; otherwise label it as general context rather than an absolute conclusion.

## 中西医双视角 (Chinese-and-Western-medicine dual lens)

Read [references/tcm-integration.md](references/tcm-integration.md) for every Chinese patient-facing standard/full report, every `med_guid` health-agent session, and whenever the user asks for a TCM, 中西医, 体质, 证候, 食疗, 中药, 针灸, or integrative interpretation. Concise reports keep at least one TCM summary line. Only an emergency-first answer or a single-metric lookup may defer the full TCM section; once the urgent instruction or metric answer is complete, include it if the user supplied a broader case.

- Start from one shared fact snapshot, then keep **西医临床解读** and **中医辨证线索** distinguishable in meaning. In natural conversation this may be a transition such as `从现代医学看……` and `从中医辨证线索看……`; separate report headings are optional. The two views may complement each other, but one is not a translation or proof of the other.
- Western clinical triage, validation, and referral determine urgency. A TCM perspective must never downgrade a red flag, replace confirmatory testing, or delay conventional care.
- TCM syndrome differentiation depends on `望、闻、问、切`、四诊合参 and the symptom course. Use the evidence tiers in the reference: symptoms plus credible tongue/pulse data may support a leading `证候倾向`; symptoms without tongue/pulse support a lower-confidence preliminary tendency; lab/device-only input supports `资料型待辨方向`, not a confirmed syndrome. Do not stop at `中医辨证信息不足` when usable health domains exist—name the most relevant candidate axes and the few observations that distinguish them.
- Never map a biomarker or diagnosis directly to a syndrome: high glucose is not automatically `消渴`, fatty liver is not automatically `痰湿`, and hypertension is not automatically `肝阳上亢`.
- With symptom-level evidence, give at most one leading `证候倾向` and one meaningful alternative, each with supporting facts, conflicting/missing facts, and calibrated language such as `可能与……相符，需结合……辨别`. With device/lab-only data, cover every usable health domain (normally 3–6 domain groups) and expand each group into a small set of genuinely relevant sub-directions. Do not rank them as primary/secondary or turn them into a syndrome catalogue. A C-tier formal report must show the reasoning route `八纲 → 病位 → 气血津液 → 脏腑/兼夹 → 鉴别问题`, while allowing every unsupported step to remain `待辨`.
- When giving next steps, put emergency/confirmation/referral actions first. Then offer low-risk shared lifestyle measures or topics to discuss with a qualified clinician. General treatment principles such as `健脾化湿` may be explained as consultation language, but do not provide individualized formulas, herb combinations, doses, treatment courses, or self-needling instructions.
- Flag medication/herb interactions and additional caution for pregnancy or breastfeeding, children, older adults, allergies, anticoagulant use, and liver or kidney disease. Recommend qualified-practitioner and pharmacist/doctor review before herbs or acupuncture.

## Workflow

1. Identify the input type: clinical note/discharge summary, lab report, imaging/pathology report, physical exam, symptom description, or consumer/device data. Chinese patient reports and `med_guid` sessions use the dual perspective by default; otherwise detect whether the user requested a Western, TCM, or dual perspective.
2. Extract the timeline, symptoms, known diagnoses, medicines/allergies, key findings, units, reference ranges, evidence class, and source quality into one fact snapshot. Preserve “not provided” rather than guessing. Read the normalized-case contract in [references/report-guide.md](references/report-guide.md) when the source is not handled by the bundled normalizer.
3. Run the urgent-symptom screen. Read [references/report-guide.md](references/report-guide.md) for triage wording, output structure, and department routing.
4. Interpret abnormalities in clinical context. Explain what each item measures, why it may matter, common non-diagnostic explanations, and what would confirm or refute concern.
5. Derive the TCM section separately. Assign evidence tier A/B/C, then produce either a leading preliminary pattern plus alternative (A/B) or domain-based `资料型待辨方向` (C). For C-tier formal reports, show how each data domain changes the next diagnostic questions: first cold/heat and deficiency/excess, then organ location and qi-blood-fluid pattern, then possible combinations. Record discriminating missing facts instead of inventing tongue, pulse, symptoms, or certainty.
6. Prioritize findings by urgency and actionability instead of listing every value or possible syndrome equally.
7. Recommend the least burdensome reasonable next step: repeat a valid measurement, collect missing history, arrange primary care, choose a relevant specialty, or seek urgent care. Add low-risk integrative options only after those steps.
8. End with the one or two most useful next actions. Add a clinician-visit checklist or formal limitations block only when the case is complex, a report was requested, or that detail materially helps the user.

Ask only for missing details that could materially change urgency or interpretation. Typical high-value details are current symptoms, age range, sex at birth when clinically relevant, pregnancy/postpartum status, known conditions, current medicines, allergies, and whether an abnormal value was repeated under standard conditions.

## Joys360 and DeepAffex-style data

When the input is a Joys360 URL/JSON or contains point IDs such as `MFBG_RISK_PROB`, `BP_SYSTOLIC`, or `NOTE_DEGRADED_ACCURACY`:

1. Read [references/joys360.md](references/joys360.md).
2. Normalize the data with the bundled script instead of interpreting raw `ovalue` values:

   ```bash
   # Resolve python3 first, then python (common on Windows). The URL is not present in argv.
   <python> "<skill-dir>/scripts/normalize_joys360.py" --file "<downloaded-json>" --format bundle --mode standard

   # Only with a host-provided sensitive/non-logged stdin channel:
   <python> "<skill-dir>/scripts/normalize_joys360.py" --url-stdin --format bundle --mode standard
   ```

   Replace `standard` with `concise` or `full` when requested. `bundle.report` is the selected formal rendering and factual floor, `bundle.reports` contains all three deterministic report modes for later mode changes, and `bundle.result` is the reusable normalized snapshot. In a conversational session, express that floor naturally rather than returning it verbatim. Use `--format report` only when no later rerender is possible, and `--format json` only when structured data alone is genuinely needed. The normalized schema includes `result.metrics`, `result.quality`, `result.coverage`, and `result.fact_sha256`; there is no `result.points`. Consume stdout directly and do not create intermediate JSON files for an answer-only request.

3. Use the deterministic report as the factual floor. Add context only when supported by the supplied record or a cited current guideline. Do not turn estimated anthropometrics or model scores into a diagnosis, treat correlated outputs as independent confirmation, or convert model-risk estimates into disease probabilities.
4. Treat every returned health point as screening information. Prominently surface the quality notes before discussing values.
5. Use confirmatory clinical measurements or tests for care decisions. Never use proprietary scores or color bands as clinical diagnostic cutoffs.

## Output contract

Use `是什么 → 为什么 → 接下来怎么办` as an **internal reasoning sequence, not mandatory headings**. The answer still needs a bottom line, a concise explanation of the evidence and uncertainty, and practical next steps, but its visible shape should match the conversation.

For ordinary `med_guid` dialogue:

- start with the judgment itself in one sentence; do not prefix it with `核心判断：`、`结论：`, warm up, summarize the user's message, or announce the answer structure;
- use an information budget rather than a target length: normally 220–340 Chinese characters and at most three short paragraphs, with less when few facts matter. Keep only facts that change urgency, interpretation, or action;
- after the judgment, explain no more than two most likely decisive Western-clinical reasons and one most relevant TCM `待辨方向`. Do not list alternative syndromes in an ordinary first answer; compress both lenses into one short paragraph;
- do not infer age group, sex, or the algorithm behind a device score when the user did not supply it; call an undocumented score only a device/vendor indication rather than guessing that it came from HRV or another signal;
- always include visible Western-clinical and TCM initial-differentiation content for a broader Chinese health case, but use short clauses such as `现代医学上……；中医线索上……` rather than two titled sections;
- in ordinary conversation, name at most one most relevant TCM `待辨方向`; expand alternatives only after the user asks for TCM detail;
- end with `先做：` followed by no more than three prioritized actions, preferably in one compact line, and give only one first-contact department when routing is needed. Ask one `还需确认：` question only when its answer would change urgency or the leading interpretation; it must contain one question about one fact, not multiple questions joined by `以及`、`和` or `或`. Otherwise do not append questions;
- omit self-introduction, greetings, empathy filler, restated data, normal-item walkthroughs, broad cause/syndrome/department catalogues, generic lifestyle advice, repeated conclusions, generic emergency lists, and boilerplate disclaimers such as `仅供参考`. Do not use headings, horizontal rules, tables, or consecutive bullet piles in ordinary dialogue.

Use `精简版`、`标准版`、`完整版` and the formal `是什么 / 为什么 / 怎么办` structure only when the user asks for a report or explicitly selects that format. If TCM evidence is sparse but usable domains exist, do not omit the TCM view or return only `中医辨证信息不足`.

Adjust depth without changing the factual conclusion:

- **精简版**: a quick-read answer, usually 200–450 Chinese characters; one bottom line, no more than two priority findings, one TCM line, and no more than three next actions. Omit normal items, background exposition, and generic cautions.
- **标准版**: a practical report, usually 500–900 Chinese characters; use at most five top-level sections and no nested heading tree. Include only prioritized findings, a compressed dual-lens explanation, confirmation/routing, and case-specific cautions.
- **完整版**: a detailed report with source/data-quality notes, all material findings grouped by evidence class, fuller reasoning and uncertainty, care routing, confirmation checklist, clinician questions, red flags, and an appendix of all supplied metrics when useful.

If the user requests a different length after seeing a report, rerender the same normalized facts in the requested mode rather than fetching or interpreting the source again unless freshness matters. Do not rank unrelated model-risk percentages against one another; fixed display order is not clinical priority.

Do not output unsupported “normal/abnormal”, “high/medium/low”, sex-specific, or proprietary-score interpretations. Cite the guideline name, jurisdiction, date, and source for every clinical threshold that is actually used.

For 完整版 only, follow the full template in [references/report-guide.md](references/report-guide.md). A report request without a depth choice uses the compact standard shape above, not the full template. If the evidence is insufficient, provide a limitation-aware preparation note rather than fabricating a complete report.
