---
name: reviewer-response
description: >
  Draft professional responses to peer review comments. Track revisions,
  generate rebuttal letters. Trigger: 审稿回复/回应审稿人/reviewer response/rebuttal letter.
version: 1.0.0
---

# Reviewer Response

Professional response to peer review comments with revision tracking.

## Response Structure

```markdown
# Response to Reviewers

Dear Editor,

We thank the reviewers for their careful reading and constructive feedback.
Below we address each comment individually. Changes in the manuscript are
highlighted in blue.

## Reviewer 1

### Comment 1: [QUOTE]
**Response:** [POLITE ACKNOWLEDGMENT + WHAT WE CHANGED]
**Action:** [SPECIFIC CHANGE WITH LINE NUMBERS]

### Comment 2: ...
```

## Response Rules

1. **Always acknowledge** — "We thank the reviewer for this observation"
2. **Be specific** — cite exact line numbers where changes were made
3. **Show evidence** — include before/after text snippets
4. **Disagree politely** — "We respectfully note that..." + evidence
5. **Track everything** — maintain a revision log

## Multi-Reviewer Synthesis

When multiple reviewers make similar comments:
- Group by theme
- Respond once, note "Also raised by Reviewer 2"
- Prioritize: Major concerns before minor issues

## Integration

Uses `paper-review` to first understand reviewer comments, then generates
point-by-point responses.
