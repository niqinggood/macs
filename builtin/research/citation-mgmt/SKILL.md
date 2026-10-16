---
name: citation-mgmt
description: >
  Citation format conversion (.nbib/.ris/.bib), dedup, reference management,
  BibTeX generation, DOI lookup. Trigger: 引文/参考文献/citation/convert refs/格式化引用.
version: 1.0.0
---

# Citation Management

Convert, deduplicate, and manage academic citations.

## Format Conversion

### Input → Output Matrix

| From | To | Method |
|------|------|--------|
| .nbib (PubMed) | .bib (BibTeX) | Parse XML → BibTeX template |
| .ris (EndNote) | .bib | Tag mapping (TY→@type, etc.) |
| .bib | .ris | Reverse mapping |
| DOI | .bib | CrossRef API → BibTeX |
| PMID | .bib | PubMed API → BibTeX |

### RIS → BibTeX Mapping

```
TY  - JOUR  → @article
TY  - CONF  → @inproceedings
T1  -       → title
A1  -       → author
PY  -       → year
JO  -       → journal
VL  -       → volume
IS  -       → number
SP  -       → pages
DO  -       → doi
UR  -       → url
```

### DOI Lookup

Use `web_fetch` with CrossRef API:
```
web_fetch url="https://api.crossref.org/works/DOI" Accept=application/json
```

Parse response:
```json
{
  "title": "...",
  "author": [{"given": "...", "family": "..."}],
  "published": {"date-parts": [[2024]]},
  "container-title": "...",
  "volume": "...",
  "page": "...",
  "DOI": "..."
}
```

## Dedup Strategy

1. **DOI match** — exact match → merge (highest priority)
2. **Title similarity** — Levenshtein > 0.85 → merge candidates
3. **Author + Year + Journal** — fuzzy match → merge candidates
4. **Manual resolution** — present candidates to user for confirmation

## BibTeX Output

```bibtex
@article{key2024,
  author = {Last, First and Last2, First2},
  title = {Full Title of the Paper},
  journal = {Journal Name},
  year = {2024},
  volume = {1},
  number = {2},
  pages = {1--10},
  doi = {10.xxxx/xxxxx},
  url = {https://doi.org/10.xxxx/xxxxx}
}
```

## Integration

Uses openkitty's `web_fetch` for API calls. File operations via `write` tool.
Python scripts for complex conversion: `skills/scholar-skills/shared/format-converter.py`
