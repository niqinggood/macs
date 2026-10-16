---
name: medical-research
description: >
  Medical and clinical research support. Systematic reviews (PRISMA/Cochrane),
  clinical trial analysis, evidence-based medicine appraisal, case report writing.
  Trigger: 医学研究/临床研究/systematic review/clinical trial/循证医学/病例报告.
version: 1.0.0
---

# Medical Research

Clinical and medical research support tools.

## Evidence Hierarchy

| Level | Type | Appraisal Tool |
|-------|------|----------------|
| I | Systematic Review / Meta-analysis | AMSTAR 2 |
| II | RCT | Cochrane RoB 2 |
| III | Cohort Study | NOS |
| IV | Case-Control | NOS |
| V | Case Series / Report | CARE Checklist |

## Systematic Review (Cochrane-style)

### PICO Framework
- **P**opulation: patient group
- **I**ntervention: treatment/exposure
- **C**omparison: alternative/placebo
- **O**utcome: measured effect

### Search Strategy
1. PubMed/MEDLINE (MeSH terms)
2. Cochrane Library
3. EMBASE
4. ClinicalTrials.gov
5. WHO ICTRP

### Risk of Bias Assessment
- Random sequence generation
- Allocation concealment
- Blinding (participants, personnel, outcome)
- Incomplete outcome data
- Selective reporting

## Statistical Methods

- Fixed-effects vs Random-effects meta-analysis
- Heterogeneity: I² statistic, Q-test
- Publication bias: funnel plot, Egger's test
- Forest plot generation
- GRADE evidence quality assessment

## Clinical Trial Analysis

- Sample size calculation (power analysis)
- Interim analysis
- Subgroup analysis
- Adverse event reporting

## Case Report (CARE Guidelines)

```
Title: [Condition] — A Case Report
Abstract: Introduction → Case Presentation → Discussion → Conclusion
Keywords: 2-5 MeSH terms
Introduction: Why this case is novel
Case Presentation: Timeline, findings, diagnostic assessment, interventions, outcomes
Discussion: Strengths, limitations, literature review, take-away lessons
Patient Perspective: (if obtained)
Informed Consent: (required)
```

## Integration

Uses `lit-search` with MeSH strategy, `data-analysis` for statistics,
`citation-mgmt` for references. HIPAA-compliant when used with local models.
