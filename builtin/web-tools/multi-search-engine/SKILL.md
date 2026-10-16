---
name: "multi-search-engine"
description: "Adaptive web search and evidence collection across 17 engines (8 CN + 9 Global). Use for current facts, open-ended web research, multi-source comparisons, finding canonical pages, or recovering from irrelevant, blocked, stale, or incomplete search results. Supports advanced operators, time filters, site search, privacy engines, and WolframAlpha without API keys."
---

# Multi Search Engine v2.0.1

Integration of 17 search engines for web crawling without API keys.

## Adaptive search workflow (advisory)

Treat this workflow as decision guidance, not a fixed state machine. Use the latest observation and the user's actual evidence needs to choose the next action; a different route is valid when it is more promising.

1. Translate the request into an evidence checklist: requested fields, geography, time window, comparison dimensions, source quality, and required deliverable.
2. Start with the most specific promising source, engine, or read-only tool. Batch independent discovery queries when that saves round trips.
3. Inspect the returned results before deciding what comes next. Continue only while a material evidence gap remains.
4. When a result is irrelevant, empty, blocked, duplicated, or stale, change the retrieval strategy materially. Options include changing keywords or language, adding an exact date or time window, using `site:` or a source-type term, switching engines, opening a discovered canonical page, using a platform/professional Skill, or using the browser for JavaScript/login/interaction.
5. Fetch direct pages when their URLs are known from the user, search results, or documented canonical endpoints. Do not guess `/price`, `/news`, or similar paths.
6. Stop when the evidence supports the requested fields and important claims. If a field remains unavailable, label it unknown or explain the coverage gap; never fill it from plausibility or model memory.

There is no target number of searches and no special meaning attached to a third or sixth call. Avoid only identical no-progress calls and unnecessary expansion after the evidence is sufficient. For live facts, prefer dated primary or authoritative sources, preserve conflicts, and distinguish event time from publication time.

Possible starting points, not requirements: Sogou or Bing CN for broad Chinese public-web discovery; Brave, DuckDuckGo, Google, or Bing INT for global/English discovery; WeChat search for public-account articles; and WolframAlpha for calculations or conversions. If the user names a social platform, professional dataset, or dedicated live-data tool, prefer its Skill/tool over a generic engine.

## Search Engines

### Domestic (8)
- **Bing CN**: `https://cn.bing.com/search?q={keyword}&ensearch=0`
- **Bing INT**: `https://cn.bing.com/search?q={keyword}&ensearch=1`
- **360**: `https://www.so.com/s?q={keyword}`
- **Sogou**: `https://sogou.com/web?query={keyword}`
- **WeChat**: `https://wx.sogou.com/weixin?type=2&query={keyword}`
- **Toutiao**: `https://so.toutiao.com/search?keyword={keyword}`
- **Jisilu**: `https://www.jisilu.cn/explore/?keyword={keyword}`
- **Baidu**: `https://www.baidu.com/s?wd={keyword}`

### International (9)
- **Google**: `https://www.google.com/search?q={keyword}`
- **Google HK**: `https://www.google.com.hk/search?q={keyword}`
- **DuckDuckGo**: `https://duckduckgo.com/html/?q={keyword}`
- **Yahoo**: `https://search.yahoo.com/search?p={keyword}`
- **Startpage**: `https://www.startpage.com/sp/search?query={keyword}`
- **Brave**: `https://search.brave.com/search?q={keyword}`
- **Ecosia**: `https://www.ecosia.org/search?q={keyword}`
- **Qwant**: `https://www.qwant.com/?q={keyword}`
- **WolframAlpha**: `https://www.wolframalpha.com/input?i={keyword}`

## Quick Examples

```javascript
// Basic search
web_fetch({"url": "https://www.google.com/search?q=python+tutorial"})

// Site-specific
web_fetch({"url": "https://www.google.com/search?q=site:github.com+react"})

// File type
web_fetch({"url": "https://www.google.com/search?q=machine+learning+filetype:pdf"})

// Time filter (past week)
web_fetch({"url": "https://www.google.com/search?q=ai+news&tbs=qdr:w"})

// Privacy search
web_fetch({"url": "https://duckduckgo.com/html/?q=privacy+tools"})

// DuckDuckGo Bangs
web_fetch({"url": "https://duckduckgo.com/html/?q=!gh+tensorflow"})

// Knowledge calculation
web_fetch({"url": "https://www.wolframalpha.com/input?i=100+USD+to+CNY"})
```

## Advanced Operators

| Operator | Example | Description |
|----------|---------|-------------|
| `site:` | `site:github.com python` | Search within site |
| `filetype:` | `filetype:pdf report` | Specific file type |
| `""` | `"machine learning"` | Exact match |
| `-` | `python -snake` | Exclude term |
| `OR` | `cat OR dog` | Either term |

## Time Filters

| Parameter | Description |
|-----------|-------------|
| `tbs=qdr:h` | Past hour |
| `tbs=qdr:d` | Past day |
| `tbs=qdr:w` | Past week |
| `tbs=qdr:m` | Past month |
| `tbs=qdr:y` | Past year |

## Privacy Engines

- **DuckDuckGo**: No tracking
- **Startpage**: Google results + privacy
- **Brave**: Independent index
- **Qwant**: EU GDPR compliant

## Bangs Shortcuts (DuckDuckGo)

| Bang | Destination |
|------|-------------|
| `!g` | Google |
| `!gh` | GitHub |
| `!so` | Stack Overflow |
| `!w` | Wikipedia |
| `!yt` | YouTube |

## WolframAlpha Queries

- Math: `integrate x^2 dx`
- Conversion: `100 USD to CNY`
- Stocks: `AAPL stock`
- Weather: `weather in Beijing`

## Documentation

- `references/advanced-search.md` - Domestic search guide
- `references/international-search.md` - International search guide
- `CHANGELOG.md` - Version history

## License

MIT
