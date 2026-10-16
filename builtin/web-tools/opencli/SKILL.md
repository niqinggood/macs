---
name: opencli
description: >
  Persistent shell + social platform automation (Bilibili/Zhihu/Twitter/YouTube/Weibo/Reddit/雪球 etc.) via Chrome login. Prefer over browser tools. Triggers: 查热搜/搜内容/发帖 on supported sites.
version: 1.0.0
author: OpenKitty
status: stable
---

# opencli

Persistent terminal for shell commands and social platform automation. Use for complex CLI workflows. Prefer over playwright/browser automation for supported sites.

**Supported sites**: Bilibili, Zhihu, Twitter/X, YouTube, Weibo, 小红书, V2EX, Reddit, HackerNews, 雪球, BOSS直聘 etc.
**Triggers**: 查B站热门, 搜知乎, 看微博热搜, 发推, 搜YouTube, 查股票行情 — browse, search, fetch trending, post, or read messages on any supported site.

**Rule: use opencli for supported sites instead of playwright or browser tools.**

## IRON RULES (non-negotiable)

1. **Never post without user confirmation**: All write operations (post, reply, like, retweet, comment) MUST show the exact content to the user and get explicit confirmation before executing. One wrong post is irreversible and can be reputationally catastrophic.
2. **Use opencli for supported sites, not browser tools**: opencli is faster, more reliable, and purpose-built for these platforms. Only fall back to Playwright when opencli genuinely lacks the required command — never skip it out of habit.
3. **Always translate titles to Chinese**: When presenting results from English-language platforms (Reddit, HN, Twitter, YouTube, ProductHunt), every title MUST include a Chinese translation. Never show English-only output to the user.
4. **Never say "not supported"**: When opencli lacks a command, always attempt the Playwright fallback. If Playwright also fails, try creating a custom YAML. The user should never hit a dead end.
5. **Respect platform rate limits**: Aggressive polling or rapid-fire search queries can trigger account restrictions or IP bans. Use reasonable request intervals and limit batch operations to safe volumes.

## Installation (Required)
Install via npm:
```bash
npm install -g @jackwener/opencli
```

## Syntax

```bash
opencli <site> <command> [--option value] [-f json]
```

**Common flags (all commands):**
- `-f json` — machine-readable output (preferred for parsing)
- `--limit N` — number of results (default varies, usually 20)
- `-f table|json|yaml|md|csv`

## Quick Examples

```bash
# 读取/浏览
opencli bilibili hot --limit 10 -f json
opencli zhihu hot -f json
opencli weibo hot -f json
opencli twitter timeline -f json
opencli hackernews top --limit 20 -f json
opencli v2ex hot -f json
opencli reddit hot -f json
opencli xiaohongshu feed -f json

# 搜索
opencli bilibili search --keyword "AI" -f json
opencli zhihu search --keyword "大模型" -f json
opencli twitter search --query "claude AI" -f json
opencli youtube search --query "LLM tutorial" -f json
opencli boss search --query "AI工程师" --city "上海" -f json

# 互动（写操作）
opencli twitter post --text "Hello from CLI!"
opencli twitter reply --url "https://x.com/.../status/123" --text "Great post!"
opencli twitter like --url "https://x.com/.../status/123"

# 个人数据
opencli bilibili history -f json
opencli twitter bookmarks -f json
opencli xueqiu watchlist -f json
```

## Anti-Patterns

| # | Anti-Pattern | Why It's Bad | Correct Approach |
|---|-------------|--------------|-----------------|
| 1 | Saying "not supported" instead of trying the Playwright fallback | Leaves the user with no path forward; violates the "never say no" principle | Always attempt: opencli first, then Playwright MCP, then custom YAML generation |
| 2 | Posting/reply/liking without showing the content to the user first | Irreversible public action without informed consent | Always preview the exact content and wait for explicit user confirmation before any write operation |
| 3 | Showing English-only titles to the user | Poor UX for Chinese-speaking users; violates the output formatting rules | Always include a Chinese translation column alongside the original title |
| 4 | Using `[title](url)` format in the 原标题 column | Violates output formatting rule — merges title and link into one cell | Keep 原标题 as plain text; put clickable link in a separate `[🔗](url)` column |
| 5 | Creating custom YAML without `browser_evaluate` probing first | Wrong CSS selectors produce a broken CLI that returns empty results | Always explore the DOM structure with `browser_evaluate` before writing selectors in YAML |

## Output Formatting Rules

When displaying results to the user:
1. **Always show original title + Chinese translation + clickable link as separate columns**
2. **Table format**: `# | 原标题 | 中文翻译 | 链接 | 关键指标...`
3. **原标题**: plain text, no markdown link — do NOT use `[title](url)` format
4. **中文翻译**: plain Chinese translation text
5. **链接**: `[🔗](url)` — compact clickable icon
6. **Translate all English titles** to Chinese — never show English-only output to the user

Example:
```
| # | 原标题 | 中文翻译 | 链接 | 分 | 评论 |
|---|--------|---------|------|-----|------|
| 1 | The 49MB web page | 那个 49MB 的网页 | [🔗](https://...) | 388 | 196 |
```

## Fallback 策略：opencli 不支持时用 Playwright

**核心原则：永远不说"不支持"，先尝试 opencli，失败或无命令时自动切换 Playwright。**

### 决策流程

```
用户请求
  ↓
opencli 有对应命令？
  ├─ 是 → 执行 opencli
  └─ 否 → 直接用 Playwright MCP 打开对应页面完成任务
              ↓
           Playwright 报错 / 无法连接？
              └─ 引导用户安装桥接插件（见下方）
```

### 常见 opencli 不支持场景 → Playwright 替代

| 场景 | 网址 | Playwright 操作 |
|------|------|----------------|
| 知乎私信 | `https://www.zhihu.com/messages` | navigate → snapshot 读取列表 |
| 知乎通知 | `https://www.zhihu.com/notifications` | navigate → snapshot |
| 微博发帖 | `https://weibo.com` | navigate → 点击输入框 → type → 发送 |
| 小红书私信 | `https://www.xiaohongshu.com/im` | navigate → snapshot |
| B站私信 | `https://message.bilibili.com` | navigate → snapshot |
| Twitter DM | `https://x.com/messages` | navigate → snapshot |

### Playwright 操作标准流程

```
1. mcp__playwright__browser_navigate → 目标 URL
2. mcp__playwright__browser_snapshot → 读取页面结构
3. 根据需要：browser_click / browser_type / browser_scroll
4. 将结果整理后呈现给用户
```

### ⚠️ 写操作风险提示（发帖/回复/点赞前必须告知）

1. **账号安全**：自动化行为可能触发平台风控
2. **不可撤回**：发布后立即公开
3. **最佳实践**：执行前向用户展示将发布的内容，等待确认

### 插件未安装时的引导话术

如果 Playwright 报错（连接失败 / 无法控制浏览器），告知用户：

> "需要在 Chrome 安装 **Playwright MCP Bridge** 插件才能控制浏览器。
> 安装步骤：
> 1. 打开 Chrome，访问 Chrome Web Store
> 2. 搜索 **"Playwright MCP"** 或 **"MCP Bridge"**
> 3. 点击「添加到 Chrome」
> 4. 安装后确保 Chrome 已登录目标网站
> 5. 重新告诉我你的需求，我来帮你完成"

## Requirements

- Chrome browser open with target site logged in
- Playwright MCP Bridge extension installed in Chrome（未安装时见上方引导）

## 自迭代能力：为新网站创建 CLI

**当 opencli 不支持某个网站时，不要放弃——自己创建！**

### 流程

```
1. opencli <site> --help  →  报错？说明不支持
2. opencli generate <url>  →  尝试自动生成（成功则结束）
3. 自动生成失败 → 手动创建 YAML：
   a. 用 Playwright 打开目标页面
   b. browser_evaluate 探索 DOM 结构（找 data-test 属性、class 规律）
   c. 确认选择器后写入 ~/.opencli/clis/<site>/top.yaml
   d. opencli <site> top -f json  →  验证输出
```

### YAML 格式（DOM 抓取模板）

```yaml
site: <sitename>
name: <command>
description: <描述>
domain: <domain>
strategy: public
browser: true

args:
  limit:
    type: int
    default: 10

pipeline:
  - navigate: https://<url>
  - evaluate: |
      (async () => {
        const limit = ${{ args.limit }};
        // DOM 抓取逻辑
        return results;
      })()

columns: [rank, name, ...]
```

### 已创建的自定义 CLI

| 站点 | 命令 | 文件 | 关键选择器 |
|------|------|------|-----------|
| producthunt | `top` | `~/.opencli/clis/producthunt/top.yaml` | `button[data-test="vote-button"]` → 父容器 → `[data-test^="post-name-"]`，tagline: `nameEl.parentElement.querySelector('span.mt-0\\.5')` |

### 调试技巧

- `browser_evaluate` 先探结构：`document.querySelector('...').innerHTML`
- 找 `data-test` 属性最稳定，其次 class 中的语义词
- tagline 通常是 name 的兄弟元素（`nameEl.parentElement.querySelector('span...')`）
- 去重用 `seen = new Set()`，防止重复产品

## Full Command Reference

See [references/commands.md](references/commands.md) for all 55 commands with complete argument details.

## Pre-Action Checklist

- [ ] Identify the target platform — is it supported by opencli?
- [ ] Determine the action: read (browse/search/hot) or write (post/reply/like)?
- [ ] For write actions: compose the content, show it to the user, and get explicit confirmation
- [ ] Use `-f json` for machine-readable output when parsing results
- [ ] Format results correctly: 原标题 | 中文翻译 | 链接 columns
- [ ] If opencli fails: fall back to Playwright MCP, then custom YAML — never leave the user stuck
- [ ] Verify all links in output are clickable and point to the correct URLs
