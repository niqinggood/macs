---
name: agent-reach
description: "Route read-only internet research through Agent Reach for explicit web URLs, RSS, GitHub, YouTube, Bilibili/B站, Twitter/X/推特, Reddit, Xiaohongshu/小红书/XHS, V2EX, LinkedIn/领英/jobs, Xiaoyuzhou/小宇宙 podcasts, and Xueqiu/雪球. Use when a request explicitly asks for 全网调研、网上搜索、网络搜索、网页链接读取、平台讨论、GitHub 搜索、视频字幕或播客转录."
---

# Agent Reach

Use `agent_reach` as the unified read-only entrypoint. It chooses an installed upstream backend and, if that backend is missing or unhealthy, falls back to OpenKitty's existing `web_search`, `web_fetch`, or `social_trending` path. Do not replace those native tools or tell the user a platform is unavailable before the returned fallback has also failed.

Do not use this Skill for local workspace search, offline analysis/translation, or posting/commenting/liking.

## Workflow

1. Map the request to one of: `search`, `read`, `trending`, or `transcribe`.
2. Call `agent_reach` directly. A manual doctor call is optional: the router already tries bounded backends and reports the route. Use `action="doctor"` when diagnosing availability or choosing among login-dependent backends.
3. Inspect `Backend` and `Fallback reason` in the result. They are operational provenance, not source citations.
4. Continue retrieval until the user's evidence fields are supported. Search listings are discovery; open direct pages for claims that need article-level evidence.
5. Cite only URLs returned in the content. If login, cookies, a transcription key, or a browser extension is still required after fallback, report that exact remaining gap.

## Calls

```json
{"action":"doctor","platform":"xiaohongshu"}
{"action":"search","platform":"github","kind":"code","query":"Go checkpoint resume tool call","limit":10}
{"action":"search","platform":"bilibili","query":"Agent harness 测试","limit":5}
{"action":"read","platform":"auto","target":"https://example.com/article"}
{"action":"trending","platform":"reddit","limit":10}
{"action":"transcribe","platform":"youtube","target":"https://www.youtube.com/watch?v=...","provider":"auto"}
```

For a complex platform workflow, read only the relevant file from `references/` in the returned runtime directory. The bundled `scripts/doctor.py` is an optional human/debugging helper; the normal Agent path is the native `agent_reach` tool.

## Routing constraints

- Generic current weather, fuel prices, and A-share facts keep using their dedicated native tools first.
- Generic public-web search may still use `web_search`/`multi-search-engine`; Agent Reach adds Exa and platform-specific routes.
- Bilibili search uses bili-cli, then the public Bilibili API, then OpenCLI and web fallback; Bilibili read/subtitles can still use bili-cli or OpenCLI. Do not use yt-dlp for Bilibili.
- YouTube uses yt-dlp; `transcribe` uses Agent Reach's configured Groq/OpenAI Whisper chain.
- GitHub public repository/code search uses `gh`; if authentication or CLI execution fails, the router uses a `site:github.com` web fallback.
- Twitter, Reddit, and Xiaohongshu prefer their CLI/browser-session backends; the router degrades to public web discovery when login infrastructure is unavailable.
- This integration is read-only. Do not use it for posting, replying, liking, creating issues/PRs, forking, or any other mutation.

## References

- `references/search.md` — web/Exa and GitHub search
- `references/social.md` — social/community platforms and fallback behavior
- `references/web-video.md` — URL reading, RSS, YouTube/Bilibili, and transcription
