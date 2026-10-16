---
name: research
description: >-
  Academic and technical research toolkit — literature search, statistical analysis, exploratory data analysis, citation management, hypothesis generation, clinical research. Use when: analyzing data, searching papers, designing experiments, testing hypotheses, conducting systematic reviews. 科研, 数据分析, 文献检索, 论文, 统计, 实验设计, 临床研究.
version: 2.0.0
author: OpenKitty
status: stable
---

# Research Skills

Merged from scientific-agent-skills, bioSkills, nature-skills, and academic-research-skills. Covers the full research cycle: find → analyze → write → cite.

---

## ⚠️ IRON RULES

1. **Source citation mandatory.** Every claim must cite a verifiable source. No assertions without evidence.
2. **No fabricated data.** Never invent statistics, quotes, or findings. State gaps explicitly.
3. **Assumptions before conclusions.** Every statistical test must document its assumptions and whether they hold.
4. **Effect sizes, not just p-values.** Practical significance ≠ statistical significance.
5. **Multiple sources minimum.** Literature searches must use 3+ databases. Single-source = bias.
6. **Validate citations.** Every DOI must resolve. Every BibTeX entry must pass syntax check.
7. **Legal compliance.** Public sources only. No scraping behind auth walls.

---

## Sub-Skills

### Data & Analysis

| Sub-Skill | Use For |
|-----------|---------|
| **data-analysis** | Statistical tests, assumption checks, effect sizes, APA reports |
| **statistical-analysis** | Guided test selection, power analysis, APA-formatted results |
| **senior-data-scientist** | A/B testing, causal inference (DiD), SHAP, MLflow |
| **figure-chart** | Publication-grade scientific figures (matplotlib/ggplot2) |
| **data-visualization/** | 7 visualization skills: ggplot2, matplotlib, color-palettes, heatmaps, multipanel, statistical-annotation, interactive |
| **deep-research** | Multi-phase research: search → analyze → synthesize → report |

### Statistics & Methods

| Sub-Skill | Use For |
|-----------|---------|
| **effect-measures** | OR, RR, RD, HR, NNT with CI methods — pure applied statistics |
| **power-and-sample-size** | Cohen's d, equivalence testing, non-inferiority, adaptive designs |
| **multiple-testing** | FDR (BH/BY/Storey q), FWER (Bonferroni/Holm), IHW |

### Literature & Citations

| Sub-Skill | Use For |
|-----------|---------|
| **lit-search** | Multi-source search (PubMed/CrossRef/arXiv/Google Scholar) |
| **lit-review** | PRISMA systematic review with verified citations, thematic synthesis |
| **citation-mgmt** | DOI resolution, BibTeX generation, citation validation |

### Writing & Publishing

| Sub-Skill | Use For |
|-----------|---------|
| **paper-write** | Draft IMRAD manuscript sections from research notes |
| **paper-polish** | Polish academic prose to Nature/Science-level English |
| **paper-review** | Simulate peer review before submission |
| **paper-reader** | Generate Chinese-English bilingual paper readers |
| **reviewer-response** | Draft point-by-point reviewer response letters |
| **paper2ppt** | Generate presentation decks from papers |
| **paper-to-patent** | Convert papers into invention patent drafts |
| **research-grants** | NSF/NIH/DOE/DARPA grant proposal writing |
| **venue-templates** | LaTeX templates for Nature, Science, NeurIPS, ICML, CVPR, etc. |
| **academic-pipeline** | Full research pipeline orchestrator (research→write→review→revise) with integrity gates |
| **nature-data** | Data availability statements, FAIR metadata checklists, repository selection |

### Thinking & Evaluation

| Sub-Skill | Use For |
|-----------|---------|
| **scientific-critical-thinking** | Evidence quality evaluation (GRADE, Cochrane RoB, bias detection) |
| **scientific-brainstorming** | Creative research ideation, interdisciplinary gap identification |

### Other

| Sub-Skill | Use For |
|-----------|---------|
| **hypothesis-generation** | Systematic hypothesis development from observations |
| **medical** | Clinical research, ICD coding, HIPAA-compliant PII redaction |
| **research-lookup** | Real-time research info retrieval with smart routing |
Systematic hypothesis development from observations. Workflow: understand phenomenon → comprehensive search → synthesize evidence → generate competing hypotheses → design tests → predict outcomes. Always present competing explanations — single-hypothesis analysis is confirmation bias.

### medical-research
Clinical research and medical document analysis. Extracts diagnoses, medications, ICD codes from clinical records. HIPAA-compliant PII redaction. Systematic review methodology for clinical evidence. See `skills/research/medical/SKILL.md`.

### research-lookup
Real-time research information retrieval. Routes queries by type: academic keywords → Google Scholar/PubMed/arXiv; deep multi-source → multi-round search + synthesis; general → fast web search. Always cite sources with access dates.

### Paper Writing & Publishing

| Sub-Skill | Use For |
|-----------|---------|
| **paper-write** | Draft IMRAD manuscript sections from research notes |
| **paper-polish** | Polish academic prose to journal-level English |
| **paper-review** | Pre-submission peer review simulation |
| **paper-reader** | Generate bilingual paper readers from PDF/DOI/arXiv |
| **reviewer-response** | Draft point-by-point reviewer response letters |
| **paper2ppt** | Generate presentation decks from papers |
| **paper-to-patent** | Convert papers into invention patent drafts |
| **lit-search** | Multi-source literature search (PubMed/CrossRef/arXiv) |
| **lit-review** | PRISMA systematic review with verified citations |
| **citation-mgmt** | BibTeX generation, DOI validation, citation formatting |
| **data-analysis** | Statistical analysis and visualization for papers |
| **figure-chart** | Publication-grade scientific figures (Python/R) |
| **deep-research** | Multi-phase deep research with synthesis reports |

---

## Quality Standards

| ✅ Good | ❌ Bad |
|---------|--------|
| Every claim has a DOI-backed citation | "Research shows that..." |
| Assumptions checked + violations documented | t-test on non-normal data, unreported |
| Thematic synthesis across studies | Study-by-study list |
| 3+ databases searched with documentation | Single-database search |
| BibTeX validated + DOIs resolved | Hand-typed references |
| Peer-reviewed sources preferred | Blog posts as primary evidence |

## Anti-Patterns

| # | Anti-Pattern | Correct |
|---|-------------|---------|
| 1 | HARKing (Hypothesizing After Results Known) | Pre-register or label as exploratory |
| 2 | Cherry-picking significant results | Report ALL planned analyses |
| 3 | Single-source literature | Minimum 3 databases |
| 4 | Study-by-study listing | Thematic synthesis |
| 5 | Blindly trusting APIs for metadata | Spot-check author/year/title |
| 6 | No PRISMA diagram in systematic reviews | MANDATORY: include PRISMA flowchart |

## Pre-Output Checklist
- [ ] All claims have verifiable source citations
- [ ] Statistical assumptions documented
- [ ] Thematic synthesis (not study-by-study)
- [ ] PRISMA flowchart included (for reviews)
- [ ] Citations validated (DOIs resolve)
- [ ] No fabricated data
- [ ] Output saved to `.openkitty/storage/`

Output: `.openkitty/storage/`
