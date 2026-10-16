---
name: paper-polish
description: >
  Academic language polishing and style improvement. Grammar fix, clarity enhance,
  journal-specific style calibration. Trigger: 润色/polish/improve writing/style check.
version: 1.0.0
---

# Academic Language Polishing

Improve academic writing clarity, grammar, and style.

## Polishing Levels

| Level | What It Does | When To Use |
|-------|-------------|-------------|
| Light | Fix grammar, typos, punctuation | Final check before submit |
| Medium | + Improve sentence flow, remove redundancy | Draft revision |
| Deep | + Restructure paragraphs, enhance argumentation | Early draft improvement |
| Journal-Specific | + Apply journal style guide | Pre-submission polish |

## Style Calibration

If user provides previous published papers:
1. Analyze writing patterns (sentence length, voice, vocabulary)
2. Extract style profile (formal/informal ratio, passive/active ratio)
3. Apply to current manuscript

## Quality Checks

- [ ] Grammar and spelling correct
- [ ] Consistent terminology
- [ ] Active voice where appropriate
- [ ] No redundant phrases
- [ ] Paragraph transitions smooth
- [ ] Journal style guide applied

## Prompt Template

```
Polish the following academic text:
- Level: [light/medium/deep]
- Target journal: [JOURNAL_NAME]
- Style: [formal/conservative/modern]

[TEXT]
```
