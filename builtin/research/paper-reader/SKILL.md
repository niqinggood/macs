---
name: paper-reader
description: >
  Read and summarize academic papers. Extract key findings, methods, limitations.
  Generate structured summaries and critical assessments. Trigger: 读论文/论文总结/paper summary/read paper.
version: 1.0.0
---

# Paper Reader

Intelligent paper reading with structured extraction and critical assessment.

## Reading Modes

| Mode | Output | Time |
|------|--------|------|
| Quick Scan | 3-sentence TL;DR | 30s |
| Standard | Structured summary | 3min |
| Deep Read | Full analysis + criticism | 10min |

## Quick Scan Output

```
TL;DR: [One sentence what they did]
Key finding: [One sentence main result]
Why it matters: [One sentence significance]
```

## Standard Summary

```markdown
## Paper Summary

**Title:** ...
**Authors:** ... (Year)
**DOI:** ...

### What They Did
[2-3 sentences on methods/approach]

### Key Findings
- Finding 1 (with evidence)
- Finding 2
- Finding 3

### Limitations
- Limitation 1
- Limitation 2

### Relevance
How this relates to [USER'S RESEARCH AREA]
```

## Deep Read Analysis

Adds to Standard:
- Methodology critique (sample size, design, bias)
- Statistical validity assessment
- Comparison to related work
- Potential follow-up studies
- Citation network analysis

## Batch Reading

For multiple papers:
1. Read all abstracts → rank by relevance
2. Standard summary for top 5
3. Synthesis table comparing methods/findings across papers

## Integration

Uses openkitty's document extraction + `lit-search` for finding papers.
