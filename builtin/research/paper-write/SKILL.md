---
name: paper-write
description: >
  Full academic paper writing pipeline: outline→draft→format→cite→submit.
  Covers IMRaD structure, abstract, bilingual abstract, citation formatting,
  journal-specific requirements. Trigger: 写论文/draft paper/write manuscript.
version: 1.0.0
---

# Paper Writing Pipeline

Complete academic writing workflow from outline to submission-ready manuscript.

## Pipeline Stages

### Stage 1: Intake & Planning

Collect from user:
- Research question / thesis statement
- Target journal (for formatting requirements)
- Key findings / data / figures
- Draft outline or existing notes

### Stage 2: Literature Foundation

Run `lit-search` to gather background literature. Load `citation-mgmt` for reference formatting.

### Stage 3: Outline Generation

Generate IMRaD outline:
```
1. Introduction — background, gap, hypothesis
2. Methods — design, participants, procedures, analysis
3. Results — findings, tables, figures
4. Discussion — interpretation, limitations, implications
5. Conclusion — summary, future work
```

### Stage 4: Section Writing

For each section:
1. Write draft (500-1000 words per section)
2. Insert citations from literature base
3. Cross-reference figures and tables
4. Check logical flow between sections

### Stage 5: Abstract

Write structured abstract (250-300 words):
- Background: 1-2 sentences
- Methods: 2-3 sentences
- Results: 3-4 sentences
- Conclusions: 1-2 sentences

Optionally generate bilingual abstract (Chinese + English).

### Stage 6: Format & Polish

- Apply journal-specific template
- Format references (APA/MLA/Chicago/Vancouver)
- Check word count limits
- Generate cover letter

## Quality Checklist

- [ ] IMRaD structure complete
- [ ] All citations verified (no hallucinations)
- [ ] Figures referenced in order
- [ ] Abstract within word limit
- [ ] Keywords included
- [ ] Author information complete
- [ ] Conflict of interest statement
- [ ] Data availability statement

## Tools Used

- `web_fetch` — journal guidelines, citation verification
- `citation-mgmt` — reference formatting
- `lit-search` — literature background
- LLM — drafting, editing, formatting
