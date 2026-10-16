# Quality Gates — ARS v3.2 7-Mode Blocking Checklist

Source: Lu et al. (2026) *Nature* 651:914-919. The AI Scientist failure modes.

## The 7 Failure Modes (ARS)

| Mode | Name | Detection |
|------|------|-----------|
| 1 | **Implementation Bug** | Suspiciously round numbers? Error bars identical? Logs saved with exit code 0? |
| 2 | **Hallucinated Citation** | DOI/PMID/title resolves to wrong paper? Claim contradicts cited paper? |
| 3 | **Methodology Fabrication** | Describes experiments that were never run? |
| 4 | **Shortcut Reliance** | Generalization claimed from single dataset? No ablation study? |
| 5 | **Bug-as-Insight** | "Interesting" result traced to a bug? Bug documented as feature? |
| 6 | **Frame Lock** | Alternative interpretations unexplored? Contradictory evidence ignored? |
| 7 | **Data Leakage** | Test data used in training? Temporal leakage? |

## Gate Positions

- **Stage 2.5** (after writing, before review): Block on suspected failures
- **Stage 4.5** (after revision, before finalize): Final integrity check

## Usage

```
Before submitting: run all 7 checks.
Flag any suspected mode → block until user acknowledges.
Document findings in review report.
```
