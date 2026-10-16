# Anti-Sycophancy Protocol

Source: ARS v3.0. Ensures reviewers provide honest critique.

## Core Rules

1. **No automatic concession**: DA agents score rebuttals 1-5 before conceding. No concession below 4/5.
2. **Evidence-first**: every criticism must cite specific manuscript text. No vague "this could be better".
3. **Frame-lock detection**: if an agent repeats the same criticism without new evidence, flag as frame-locked.
4. **Contradictory evidence must be surfaced**: if two reviewers disagree, report both views, don't silently average.
5. **Author defense evaluated, not accepted**: author's rebuttal is input to re-evaluation, not grounds for automatic upgrade.

## Calibration

Optional: user provides gold-standard reviews → agent self-evaluates:
- FNR (false negative rate): missed real issues
- FPR (false positive rate): flagged non-issues
- Target: FNR < 0.15, FPR < 0.10

## Cross-Model Verification

For critical findings, verify with a different LLM:
```
Primary: DeepSeek (fast, cheap)
Verify: Claude (thorough, nuanced)
Setting: ARS_CROSS_MODEL=1
```
