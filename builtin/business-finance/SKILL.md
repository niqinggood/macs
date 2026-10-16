---
name: business-finance
description: Business operations, financial analysis, market research. 公司估值, 竞品分析, 市场报告.
version: 1.0.0
author: OpenKitty
status: stable
---

# Business & Finance Skills

Three domains: analysis, finance markets, and business operations.

---

## ⚠️ IRON RULES

1. **Python only** via `write_file` + `code_check run`. NEVER curl/cli for API calls.
2. **Data accuracy**: Financial numbers must be verified against real sources. Never fabricate prices, earnings, or market data.
3. **Legal disclaimer**: All reports are informational, not investment advice. Always include a disclaimer.
4. **Source citation**: Every financial claim must cite its source (URL + date).
5. **Output to `.openkitty/storage/`**: All reports, charts, and documents go here.

---

## Part A: Analysis & Research

| Sub-Skill | Use For |
|-----------|---------|
| **company-valuation** | DCF, comparable analysis, business valuation |
| **data-analysis** | Statistical analysis, visualization, insights |
| **competitors** | Competitive matrix, SWOT, positioning |
| **decision-chain** | Map decision pathways and implications |
| **industry-trends** | Sector analysis, emerging trends, forecasts |

Decision guide:
- "这家公司值多少钱" → company-valuation
- "帮我做竞品分析" → competitors  
- "行业趋势是什么" → industry-trends
- "分析这个数据" → data-analysis

---

## Part B: Financial Markets

| Sub-Skill | Use For |
|-----------|---------|
| **earnings-preview** | Pre-earnings estimate analysis |
| **earnings-recap** | Post-earnings results comparison |
| **etf-premium** | ETF premium/discount tracking |
| **options-payoff** | Options payoff curve visualization |
| **pl-report-generator** | P&L report generation |
| **stock-daily-report** | A股 daily market report |
| **stock-price-query** | Real-time quotes for A-shares, HK, US stocks — no API key needed |
| **mx-stocks-screener** | Natural-language stock screener via Eastmoney Miaoxiang API |

Decision guide:
- "财报前瞻" → earnings-preview
- "财报回顾" → earnings-recap
- "ETF溢价" → etf-premium
- "期权收益曲线" → options-payoff
- "损益报表" → pl-report-generator
- "今天A股日报" → stock-daily-report

---

## Part C: Business Operations

| Sub-Skill | Use For |
|-----------|---------|
| **quotation-builder** | Price quotations, proposals, invoices |
| **sepa-strategy** | SEPA transfer optimization, cross-border payments |

Decision guide:
- "做个报价单" → quotation-builder
- "SEPA付款优化" → sepa-strategy

---

---

## Part D: Market Research

| Sub-Skill | Use For |
|-----------|---------|
| **market-research-reports** | Professional reports (50+ pages): executive summary, industry overview, Porter's Five/PESTLE/SWOT, TAM/SAM/SOM, trend analysis, strategic recommendations |
| **industry-trends** | Sector analysis, emerging trends, forecasts |

Workflow: define scope → multi-source data collection → framework analysis → report generation. Every report must include executive summary and data sources.

---

## Part E: Sales Intelligence

| Sub-Skill | Use For |
|-----------|---------|
| **lead-researcher** | B2B sales lead discovery from public sources |

Workflow: define ideal customer profile → search public channels (company websites, job postings, industry reports) → identify decision-makers → assess quality (BANT: Budget/Authority/Need/Timeline) → prioritize. 10 precise leads > 100 generic names.

---

## Quality Standards

| ✅ Good | ❌ Bad |
|---------|--------|
| Data-backed with specific numbers and sources | "The market is doing well" |
| Proper financial terminology and units | Confusing percentages with basis points |
| Clear methodology explanation | Black-box conclusions |
| Disclaimer included | No risk disclosure |
| Qualified leads with decision-maker contact | Generic company names without contacts |

---

## Anti-Patterns

| # | Anti-Pattern | Correct Behavior |
|---|-------------|-----------------|
| 1 | Making investment recommendations | Provide analysis, let user decide |
| 2 | Using stale/outdated market data | Always web_fetch current data |
| 3 | Mixing up currencies (¥ vs $) | Clearly label all currency units |
| 4 | Missing risk disclosure | Always include disclaimer |

---

## Part F: Investing Frameworks (投资大师框架)

Pure LLM knowledge — no code dependencies. Investment philosophy and decision frameworks.

| Sub-Skill | Use For |
|-----------|---------|
| **duan-yongping-investor** | Duan Yongping: benfen culture, circle of competence, long-termism |
| **li-lu-investor** | Li Lu: civilization-evolution value investing, moat analysis |
| **wu-jun-investor** | Wu Jun: intelligent-era tech investing, data-driven trend selection |
| **hillhouse-investment-framework** | Zhang Lei/Hillhouse: founder-first, time-compounding framework |
| **factor-investment** | Fama-French 5-factor quantitative stock screening system |
| **investment-five-questions** | Buffett-Munger 5-question value investing screen (ROIC>15% etc.) |
| **investor-reading-list** | Curated 10-book cognitive foundation for investors |

## Part G: Quantitative Data & Platforms (量化数据与平台)

Data APIs, backtesting frameworks, and quant trading platforms. Most require Python + API keys.

| Sub-Skill | Use For |
|-----------|---------|
| **akshare** | Free Chinese financial data API (stocks, funds, bonds, macro) |
| **baostock** | Free A-share historical data (K-line, financials, indices) |
| **tushare** | Professional Chinese financial data (stocks, funds, futures, macro) |
| **jqdatasdk** | JoinQuant data SDK for strategy research and backtesting |
| **miniqmt** | Mini QMT quantitative trading platform integration |
| **akquant** | AKShare-based quant workflow with data fetching + strategy |
| **backtrader** | Python backtesting framework for trading strategies |
| **rqalpha** | Open-source quantitative trading engine |
| **pywencai** | iWencai natural-language financial data query |
| **tdxquant** | TDX (Tongdaxin) quantitative trading toolkit |
| **joinquant-docs** | JoinQuant API reference, factor docs, FAQs |
| **qmt-docs** | QMT platform documentation and API reference |

## Part H: AlphaEar Real-time Finance (实时财经)

Curated real-time market intelligence suite. `npx skills` format.

| Sub-Skill | Use For |
|-----------|---------|
| **alphaear-news** | CN real-time news aggregation (Weibo/Zhihu/WallstreetCN/CLS/Polymarket) |
| **alphaear-sentiment** | FinBERT + LLM financial text sentiment scoring |
| **alphaear-predictor** | Kronos time-series forecasting with news-aware adjustments |
| **alphaear-logic-visualizer** | Draw.io XML investment logic/reasoning chain visualization |
| **alphaear-deepear-lite** | Lightweight deep research scraping financial signals via DeepEar Lite API |

## Part I: Stock Analysis Tools (分析工具)

Active analysis with Python scripts and automated workflows.

| Sub-Skill | Use For |
|-----------|---------|
| **stock-analysis-best** | Systematic A/HK/US stock analysis + auto HTML/PDF reports |
| **biga** | Smart A-stock screening, dynamic pool ≤30, timing signals |
| **eyes** | Global hotspot event monitoring, P0-P3 grading, impact chain |
| **ssehome-invest** | A-stock K-line + Tavily news, multi-timeframe technical analysis |
| **skill-creator** | Create new skills from templates (meta-skill for skill development) |

## Part J: Portfolio Management (组合管理)

Portfolio tracking, monitoring, and decision support.

| Sub-Skill | Use For |
|-----------|---------|
| **portfolio-monitor** | Real-time portfolio tracking (stocks/crypto), PnL, alerts |
| **j-portfolio-monitor** | Weekly portfolio review: fundamentals, valuation, news, risk |
| **j-investment-checklist** | Buy/sell decision checklist: circle, financials, valuation, sizing |

## Part K: Investment Research & Briefing (投研简报)

Deep research, briefs, and professional analysis frameworks.

| Sub-Skill | Use For |
|-----------|---------|
| **investment-researcher** | Professional A/HK stock deep research (financials, moat, management) |
| **investment-brief-generator** | AI stock brief: real-time quotes + technical + sentiment, Markdown |
| **vc-analyst** | VC/PE 5-layer analysis (Founder/Industry/Gov/Financial/Industrial) + IC debate |
| **quant-research-agent** | AI quant CLI agent: auto-search financial news, sentiment/risk analysis |

## Part L: Other (其他)

| Sub-Skill | Use For |
|-----------|---------|
| **real-estate-investment-advisor** | Full-chain real estate investment: valuation, mortgage, rental yield, city scoring |

## Part M: SEO & Digital Marketing (原有)

| Sub-Skill | Use For |
|-----------|---------|
| **seo/keyword-research** | Find high-value keywords with search volume, difficulty, and intent analysis |
| **seo/keyword-clustering** | Group keywords into topic clusters for content planning |
| **seo/competitor-analysis** | Analyze competitor SEO strategies: traffic, keywords, backlinks |
| **seo/competitive-landscape** | Map the full competitive landscape in a market niche |
| **seo/link-prospecting** | Find and qualify backlink opportunities |
| **seo/seo-coach** | Entry point — orients users and routes to the right SEO workflow |
| **seo/seo-project-setup** | Initialize SEO project structure with config and tracking |

All SEO skills are MCP-driven with live API data. See `skills/business-finance/seo/` for individual SKILL.md files.

Output: `.openkitty/storage/`
