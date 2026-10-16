---
name: paper-review
description: >
  Multi-agent academic peer review. Domain expert + methodology + statistics +
  editorial synthesis. Produces structured review reports with calibrated quality
  scores. Trigger: 审稿/peer review/review manuscript/evaluate paper.
version: 1.0.0
---

# Multi-Agent Peer Review

Structured academic peer review using multiple specialized reviewer agents.

## Review Agents

| Agent | Role | Checks |
|-------|------|--------|
| Domain Reviewer | Subject expertise | Literature coverage, novelty, significance |
| Methodology Reviewer | Methods rigor | Design validity, reproducibility, bias |
| Statistics Reviewer | Data analysis | Sample size, test choice, reporting standards |
| Perspective Reviewer | Broader impact | Interdisciplinary relevance, ethics |
| Devil's Advocate | Critical scrutiny | Weakest points, alternative interpretations |
| Editorial Synthesizer | Integration | Merge reviews, consistency, recommendation |

## Review Modes

### Standard Mode (default)
All 6 agents review independently, editorial synthesizer merges.

### Sprint Mode (fast)
Domain + Methodology only. For time-constrained reviews.

### Calibration Mode (quality check)
Agent self-evaluates against user-provided gold-standard reviews.
Reports FNR (false negative rate) and FPR (false positive rate).

### Re-Review Mode
Check if author revisions addressed previous concerns.

## Review Report Structure

```markdown
# Peer Review Report

**Manuscript:** [TITLE]
**Reviewer:** Scholar Skills v1.0
**Date:** [DATE]

## Overall Assessment
[1-5 score with justification]

## Major Concerns
1. [Concern 1 with specific evidence]
2. [Concern 2 with specific evidence]

## Minor Issues
1. [Issue 1]
2. [Issue 2]

## Statistical Review
- Sample size adequacy: [OK/CONCERN]
- Test appropriateness: [OK/CONCERN]
- Reporting standards: [OK/CONCERN]

## Recommendation
[Accept / Minor Revision / Major Revision / Reject]
```

## Quality Gates

Before submitting review:
- [ ] Each claim references specific manuscript text
- [ ] Statistical concerns are specific (not "poor stats")
- [ ] Recommendation matches evidence strength
- [ ] No hallucinated citations in review

## Integration with openkitty

Uses openkitty's `assemble_team` for multi-agent mode:
```
assemble_team agents=[domain_expert,methodologist,statistician,devils_advocate]
  task="Review manuscript: [TITLE]"
```
