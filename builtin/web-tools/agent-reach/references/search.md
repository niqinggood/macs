# Search routing

Use the native `agent_reach` tool rather than composing shell commands.

| Need | Call | Expected backend order |
|---|---|---|
| General semantic web search | `action=search, platform=exa` | Agent Reach Exa, then OpenKitty `web_search` |
| Public GitHub repositories | `action=search, platform=github, kind=repositories` | `gh search repos`, then `site:github.com` |
| GitHub code | `action=search, platform=github, kind=code` | `gh search code`, then `site:github.com` |
| GitHub issues/PR discovery | `action=search, platform=github, kind=issues` | `gh search issues`, then `site:github.com` |

Search results are discovery evidence. Read canonical URLs before making detailed claims. When a low-yield result leaves a material gap, change query language, terms, domain, platform, or source type instead of repeating the same call.
