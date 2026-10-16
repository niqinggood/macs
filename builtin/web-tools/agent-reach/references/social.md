# Social and community routing

The router keeps upstream backends optional and preserves OpenKitty fallbacks.

| Platform | Primary route | Fallback |
|---|---|---|
| Xiaohongshu | OpenCLI, then xhs-cli | `site:xiaohongshu.com` web discovery |
| Twitter/X | twitter-cli, then OpenCLI | `site:x.com` web discovery |
| Reddit | OpenCLI, then rdt-cli | `site:reddit.com` web discovery or native trending |
| Bilibili | bili-cli, then public Bilibili search API | OpenCLI, then web discovery |
| V2EX | OpenKitty native public route | `site:v2ex.com` web discovery |
| LinkedIn | Jina/direct URL reading | `site:linkedin.com` web discovery |
| Xiaoyuzhou | OpenKitty native public route | `site:xiaoyuzhoufm.com` web discovery |
| Xueqiu | OpenKitty native public route | `site:xueqiu.com` web discovery |

For login-dependent channels, `action=doctor` exposes status and active backend. Do not retry a login failure repeatedly. Ask for the specific missing browser login/cookie only after public fallback is insufficient for the requested evidence.

Xiaohongshu note detail requires the complete URL/token returned by search; a bare note ID may not be sufficient. Keep requests read-only and rate-limited.
