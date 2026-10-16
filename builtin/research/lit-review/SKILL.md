---
name: lit-review
description: >
  Systematic literature review with PRISMA workflow. Socratic guided planning,
  multi-source search, dedup, synthesis, gap analysis. Trigger: 文献综述/literature review/systematic review/写综述.
version: 1.0.0
---

# Systematic Literature Review

PRISMA-guided systematic review with Socratic planning.

## Workflow

### Phase 1: Plan (Socratic Dialogue)

Ask user:
1. What is the research question? (PICO framework for clinical)
2. What databases to search? (default: PubMed + arXiv + CrossRef)
3. Inclusion/exclusion criteria?
4. Time range? (default: last 5 years)
5. Language restrictions?

### Phase 2: Search

Execute multi-source search via `lit-search`:
- PubMed E-utilities
- arXiv RSS
- CrossRef API
- Semantic Scholar API
- Google Scholar (via web search)

### Phase 3: Screen

1. Remove duplicates (use `citation-mgmt` dedup)
2. Title/abstract screening
3. Full-text eligibility check

### Phase 4: Extract

For each included paper, extract:
- Study design
- Sample size
- Key findings
- Effect sizes
- Limitations

### Phase 5: Synthesize

- Thematic analysis
- Gap identification
- PRISMA flow diagram
- Summary tables

## PRISMA Flow Diagram

```
Records identified: N
  ↓ Duplicates removed: N
Records screened: N
  ↓ Excluded: N
Full-text assessed: N
  ↓ Excluded (with reasons): N
Studies included: N
```

## Output

- PRISMA checklist
- Summary table (Author/Year/Design/N/Findings)
- Narrative synthesis
- Research gap analysis
