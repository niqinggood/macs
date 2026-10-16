---
name: lit-search
description: >
  Multi-source academic literature search. PubMed, CrossRef, arXiv, Google Scholar,
  Semantic Scholar, Scopus, ScienceDirect. Dedup, citation verification, MeSH strategy.
  Trigger: 找论文/查文献/文献检索/literature search/paper search/scholarly articles.
version: 1.0.0
---

# Literature Search

Multi-source academic literature search with dedup and citation verification.

## Quick Start

Use openkitty's built-in `social_trending` tool with `source=rss` for academic feeds,
or use the `rss_feed` tool directly for arXiv, PubMed RSS.

### PubMed / MEDLINE

```
social_trending source=rss url="https://eutils.ncbi.nlm.nih.gov/entrez/eutils/erss.cgi?rss_guid=SEARCH_TERM"
```

Replace SEARCH_TERM with URL-encoded query (e.g., `cancer+immunotherapy`).

### arXiv

```
social_trending source=rss url="https://rss.arxiv.org/rss/CS.AI"    # AI
social_trending source=rss url="https://rss.arxiv.org/rss/q-bio"    # Biology
```

Category codes: cs.AI, cs.CL, stat.ML, q-bio, physics, math, econ.

### CrossRef (DOI search)

Use `web_fetch` to query CrossRef API:
```
web_fetch url="https://api.crossref.org/works?query=TITLE&rows=10"
```

### Semantic Scholar

```
web_fetch url="https://api.semanticscholar.org/graph/v1/paper/search?query=QUERY&limit=10"
```

## Multi-Source Search Workflow

1. **T1 (fast)**: arXiv RSS + Semantic Scholar API → quick coverage
2. **T2 (comprehensive)**: PubMed E-utilities + CrossRef → medical + general
3. **T3 (premium)**: Scopus / ScienceDirect → require institutional access

Fallback chain: if one source returns empty, try the next.

## Dedup Strategy

- Group by DOI first (most reliable)
- Then by title similarity (edit distance > 0.85 → merge)
- Keep highest-quality source (PubMed > CrossRef > arXiv)

## MeSH Strategy

For medical/life science topics:
1. Use `web_fetch` to query `https://eutils.ncbi.nlm.nih.gov/entrez/eutils/esearch.fcgi?db=mesh&term=TERM`
2. Extract MeSH terms
3. Build PubMed query: `(MeSH_term1[MeSH Terms]) AND (MeSH_term2[MeSH Terms])`

## Output Format

For each paper found:
```
Title: ...
Authors: ... (first 3 et al.)
Year: ...
DOI: ...
Source: [PubMed|arXiv|CrossRef|...]
Abstract: ... (first 200 chars)
```
