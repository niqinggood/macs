# Citation Verification — 3-Index Triangulation

Source: ARS v3.9.0 + Zhao et al. (2026) arXiv:2605.07723.

## Three-Index Verification

| Index | API | Coverage |
|-------|-----|----------|
| Semantic Scholar | `api.semanticscholar.org/graph/v1/paper/` | 200M+ papers |
| OpenAlex | `api.openalex.org/works?filter=doi:` | 250M+ works |
| Crossref | `api.crossref.org/works/` | 150M+ records |

## Triangulation Matrix

| k (matched) | Suffix | Action |
|-------------|--------|--------|
| 3 | `TRIANGULATION-MATCHED` | ✅ High confidence |
| 2 | `PARTIAL-UNMATCH` | ⚠️ Moderate, flag for review |
| 1 | `COVERAGE-NOISE` | ⚠️ Low, likely unindexed |
| 0 | `UNMATCHED` | ❌ Citation likely hallucinated |

## Verification Flow

```
For each citation in manuscript:
  1. Extract DOI → if found, query all 3 indexes
  2. No DOI → extract title + first author → query
  3. Manual entry → skip verification (trust user)
  4. Output: verified ✅ / partial ⚠️ / unmatched ❌
```

## Integration

Uses openkitty's `web_fetch` to query APIs. No external MCP needed.
