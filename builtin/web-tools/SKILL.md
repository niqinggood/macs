---
name: web-tools
description: Web browsing, multi-engine search (17+ engines), neural semantic search (Exa), web scraping, social platform CLI (opencli).
triggers:
  - web
  - browser
  - search
  - scrape
  - fetch
  - screenshot
  - html
  - css
  - design
  - navigate
  - click
  - 网页
  - 搜索
  - 抓取
  - 截图
version: 1.0.0
author: OpenKitty
status: stable
---

# Web Tools

Browser automation, search aggregation, web scraping, and frontend design.

## IRON RULES (non-negotiable)

1. **No AI slop aesthetics**: Never generate purple gradients, heavy box-shadows, Inter/Roboto/Google Fonts imports, or emoji-heavy hero sections. Load `taste-design` FIRST before writing any HTML/CSS/UI code. Warm-monochrome minimalist design system is the only acceptable aesthetic.
2. **Content extraction before design**: When asked to redesign or style a page, always extract and understand the content first. Form follows content — designing without understanding what you're designing for produces generic, unusable results.
3. **Respect robots.txt and rate limits**: When scraping or fetching, obey robots.txt directives and throttle requests. Do not hammer servers with rapid-fire requests — use reasonable delays between fetches.
4. **Use the right tool for the job**: `agent-browser` (CDP) for interactive browsing; `scrapling-web-fetch` for JavaScript-rendered content extraction; `web-access` for simple GET requests. Never use CDP when a simple fetch would suffice — it's wasteful and slow.
5. **Screenshot only after interaction settles**: When the user asks to see a page state, ensure all interactions (clicks, form fills, scrolls, animations) are complete before taking the screenshot. A screenshot of a loading spinner is useless.

## Sub-skills

### agent-browser
**Use when**: User needs to interact with a web page — navigate, click, scroll, fill forms, extract dynamic content, or take screenshots. Uses Chrome DevTools Protocol (CDP) for full browser control.

Examples: "打开这个网页截图" / "Navigate to X and extract the table" / "Click the login button"

### multi-search-engine
**Use when**: User wants to search across multiple search engines simultaneously for comprehensive results. Combines results from Google, Bing, DuckDuckGo, etc.

Examples: "全面搜索这个关键词" / "Search X across multiple engines"

### scrapling-web-fetch
**Use when**: User needs to fetch and extract clean content from a web page. Handles JavaScript rendering and returns clean markdown/text. Use for reading articles, not interactive browsing.

Examples: "抓取这篇文章的内容" / "Fetch the text from this URL"

### web-access
**Use when**: User needs to access a specific URL and retrieve its content. Simpler than scrapling — just fetches the raw page or API response.

Examples: "访问这个链接" / "Get the content from this endpoint"

### opencli
**Use when**: User wants to interact with social platforms (Bilibili, Zhihu, Twitter/X, YouTube, Weibo, 小红书, Reddit, HackerNews, 雪球, etc.) — browse trending, search content, post, or read. CLI-based, faster and more reliable than browser automation for supported sites.

Examples: "查B站热门" / "搜知乎" / "看微博热搜" / "发推" / "搜YouTube"

### design skills → now at `skills/design/`
For any visual work (HTML, CSS, UI, restyle): read `skills/design/SKILL.md` first. Contains `taste-design` (anti-slop system) and `web-design` (restyle workflow).

## Anti-Patterns

| # | Anti-Pattern | Why It's Bad | Correct Approach |
|---|-------------|--------------|-----------------|
| 1 | Using purple gradients, Inter font, heavy shadows, and emoji spam in UI designs | The "AI-generated look" signals low quality and erodes user trust | Follow `taste-design`: warm monochrome palette, system fonts, minimal shadows, no decorative emojis |
| 2 | Using `agent-browser` (CDP) for a simple static page fetch | Unnecessary overhead, 3-5x slower, more failure points than a direct HTTP request | Use `web-access` for simple GETs, `scrapling-web-fetch` for content extraction |
| 3 | Scraping page content without verifying a sample of the output | Broken, partial, or empty content returned silently — the user gets garbage | Always spot-check the extracted content before presenting results to the user |
| 4 | Designing a page layout without first extracting and reading the content | Generic templates that don't serve the actual content; mismatched structure | Extract content first, understand the information hierarchy, then design around it |
| 5 | Forgetting to load `taste-design` before generating any UI code | The default AI aesthetic (purple gradients, heavy shadows) takes over immediately | Always read `taste-design` before writing a single line of HTML or CSS |

## Decision Guide

| User says | Use |
|-----------|-----|
| "打开/截图/点击网页" | `agent-browser` |
| "多引擎搜索 X" | `multi-search-engine` |
| "抓取/提取文章内容" | `scrapling-web-fetch` |
| "访问这个 URL" | `web-access` |
| "设计/写一个网页" | `web-design` + `taste-design` |
| "改样式/配色/美化" | `taste-design` |
| "Extract all links from..." | `scrapling-web-fetch` |

Rule: interactive → `agent-browser` / content extraction → `scrapling-web-fetch` / simple GET → `web-access` / multi-source search → `multi-search-engine` / UI creation → `web-design` + `taste-design` / style changes → `taste-design`.

Output: `.openkitty/storage/`

## Pre-Action Checklist

- [ ] Understand the user's web task — browsing, searching, scraping, or designing?
- [ ] Select the correct tool: agent-browser / scrapling-web-fetch / web-access / multi-search-engine / web-design
- [ ] For design tasks: read `taste-design` FIRST before generating any HTML/CSS
- [ ] For scraping: identify the target content and verify extraction with a spot-check
- [ ] For interactive browsing: plan the click/scroll/fill sequence before navigating
- [ ] Verify the output is complete, correct, and (for designs) follows the warm-monochrome aesthetic
