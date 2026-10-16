---
name: paper-to-patent
description: >
  Convert academic papers to patent applications. Claims drafting, prior art
  search, patent formatting. Trigger: 论文转专利/patent from paper/专利申请/
  技术交底书/发明专利申请.
version: 1.0.0
---

# Paper to Patent

Convert research papers into patent application drafts.

## Patent vs Paper

| Aspect | Paper | Patent |
|--------|-------|--------|
| Purpose | Share knowledge | Protect invention |
| Audience | Researchers | Patent examiners |
| Language | Technical precise | Legal precise + broad |
| Structure | IMRaD | Claims → Description → Drawings |
| Novelty | New to field | New to world |

## Workflow

### Phase 1: Extraction
- Identify the core invention from the paper
- What problem does it solve? (technical problem)
- How does it solve it? (technical solution)
- What are the key claims?

### Phase 2: Prior Art Search
- Patent databases: Google Patents, USPTO, EPO, WIPO
- Check if the invention is truly novel
- Identify distinguishing features

### Phase 3: Claims Drafting
Independent claims (broad protection):
```
1. A method for [WHAT IT DOES], comprising:
   a) [STEP 1];
   b) [STEP 2];
   c) [STEP 3].
```

Dependent claims (specific implementations):
```
2. The method of claim 1, wherein [SPECIFIC DETAIL].
```

### Phase 4: Description
- Technical field
- Background (prior art problems)
- Summary of invention
- Detailed description
- Examples/embodiments
- Industrial applicability

### Phase 5: Abstract & Drawings
- Patent abstract (≤150 words)
- Reference numerals for drawings
- Flow charts for methods

## Output Format

Standard patent disclosure document (技术交底书):
```
发明名称:
申请人:
技术领域:
背景技术:
发明内容:
附图说明:
具体实施方式:
权利要求:
```

## Integration

Uses `lit-search` for prior art, `figure-chart` for patent drawings.
