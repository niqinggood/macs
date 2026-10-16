---
name: deep-research
description: >
  Multi-agent deep research for complex academic questions. 3-agent team
  (architect + synthesizer + compiler) with cross-model verification.
  Trigger: 深度研究/deep research/comprehensive investigation/复杂课题.
version: 1.0.0
---

# Deep Research

Multi-agent systematic investigation for complex research questions.

## Agent Team

| Agent | Role | Tools |
|-------|------|-------|
| Research Architect | Plan strategy, select sources | lit-search, web_fetch |
| Synthesis Agent | Analyze findings, identify patterns | LLM reasoning |
| Report Compiler | Structure output, format citations | paper-write, citation-mgmt |

## Workflow

### Phase 1: Architecture (Architect)
1. Decompose research question into sub-questions
2. Assign search strategies for each
3. Set quality thresholds

### Phase 2: Exploration (All agents)
1. Architect dispatches searches
2. Synthesizer processes findings
3. Compiler tracks progress

### Phase 3: Synthesis (Synthesizer)
1. Cross-reference findings across sources
2. Identify consensus and contradictions
3. Generate gap analysis

### Phase 4: Compilation (Compiler)
1. Structured report with sections
2. Full citation list
3. Executive summary

## Cross-Model Verification

For critical findings, verify with a different LLM:
```
Primary: DeepSeek (fast, cheap)
Verify: Claude (thorough, nuanced)
```

## Integration

Uses openkitty's `assemble_team`:
```
assemble_team agents=[researcher,analyst,writer]
  task="Deep research: [RESEARCH_QUESTION]"
  depth=comprehensive
  sources=[pubmed,arxiv,crossref,semantic_scholar]
```
