---
name: web-access
description: 通过 Chrome DevTools Protocol (CDP) 直连用户本地 Chrome/Edge 浏览器，天然携带登录态，支持页面导航、JS 执行、元素点击、滚动、截图、Tab 管理。适合需要登录态、动态页面、反爬站点（小红书/微信公众号等）、页面交互操作的场景。静态页面抓取优先用 web_fetch 或 scrapling-web-fetch。
read_when:
  - 需要登录后才能查看的页面内容
  - 抓取小红书、微信公众号、微博等反爬站点
  - 浏览器交互操作（点击、滚动、填表、翻页）
  - 动态渲染页面（SPA、React/Vue 等框架）
  - 需要连续操作多个页面或保持会话状态
  - 页面截图或视觉内容分析
  - 访问用户本地浏览器书签/历史中的内部系统
---

# Web Access — CDP 浏览器直连

## 前置条件

Chrome 需要开启远程调试端口。**首次使用前**，先检查：

```bash
cli: curl -s http://localhost:9222/json/version
```

如果连接失败，告知用户：
> "需要先开启 Chrome 远程调试。请关闭所有 Chrome 窗口，然后用以下命令启动：`chrome.exe --remote-debugging-port=9222`，或在已打开的 Chrome 地址栏输入 `chrome://inspect/#remote-debugging` 并启用。"

连接成功后，**必须在回复中向用户展示**：
> 温馨提示：部分站点对浏览器自动化操作检测严格，存在账号封禁风险。已内置防护措施但无法完全避免，继续操作即视为接受。

## 操作哲学

**像人一样浏览，边看边判断，不预设步骤。**

1. **明确目标** — 先定义成功标准：需要获取什么信息、执行什么操作
2. **选对起点** — 需要登录态、反爬站点、页面交互 → 直接用 CDP 工具；静态公开页面 → 用 web_fetch 或 scrapling-web-fetch
3. **过程校验** — 每步结果对照目标判断：在推进吗？方向需要调整吗？不要在同一方式上反复重试
4. **完成判断** — 达成目标后停止，不为了"完整"浪费操作。关闭自己创建的 tab

## 工具选择速查

| 场景 | 工具 |
|------|------|
| 静态公开页面，提取文本 | `web_fetch` |
| 现代网页正文提取，反爬处理 | `cli: python3 skills/scrapling-web-fetch/scripts/scrapling_fetch.py <url>` |
| 需要登录态、交互操作、动态页面 | 下面的 CDP 工具族 |

## CDP 工具

所有 CDP 工具使用前可先用 `tool_detail(<工具名>)` 获取完整参数。核心流程：

```
web_targets → 查看已有 tab
web_navigate(url) → 创建新 tab 并导航，获取 target_id
web_eval(target_id, js) → 执行 JS，读 DOM / 提取数据
web_click(target_id, selector) → 点击元素
web_scroll(target_id, direction) → 滚动触发懒加载
web_screenshot(target_id, file) → 截图保存
web_close(target_id) → 关闭 tab
```

### 关键规则

1. **Tab 隔离** — 只操作 `web_navigate` 自己创建的 tab，不碰 `web_targets` 列出的用户原有 tab
2. **操作间隔** — 连续操作之间自然等待（工具内置了基础等待），避免触发反爬
3. **任务结束清理** — 用 `web_close` 关闭自己创建的所有 tab
4. **不要重复** — 同一 selector 点击/eval 失败超过 2 次，换个方式，不要硬试

### 连续操作示例

```
1. web_navigate(url="https://example.com/list") → target_id = "ABC123"
2. web_eval(target_id="ABC123", code="document.querySelectorAll('.item').length") → "20 items"
3. web_scroll(target_id="ABC123", direction="bottom") → 加载更多
4. web_eval(target_id="ABC123", code="document.querySelectorAll('.item').length") → "40 items"
5. web_click(target_id="ABC123", selector=".item:nth-child(3) a") → 进入详情
6. web_eval(target_id="ABC123", code="document.querySelector('.content').innerText") → 提取内容
7. web_close(target_id="ABC123") → 清理
```

### 数据提取建议

- **文本内容** — `web_eval` + `document.querySelector(...).innerText`
- **结构化数据** — `web_eval` + `JSON.stringify(Array.from(document.querySelectorAll(...)).map(el => ({...})))`
- **图片 URL** — `web_eval` 从 DOM 直取 `<img src>`，比全页截图精准
- **页面标题/URL** — `web_eval` + `document.title` / `window.location.href`

## 常见反爬站点处理

- **微信公众号** — 直接 CDP 打开文章链接，`web_eval` 取 `#js_content` 的 innerText
- **小红书** — CDP 直连，携带登录态，提取后关闭 tab
- **微博** — CDP 打开单条微博，提取 `.WB_text` 内容
- **动态加载列表** — `web_scroll` → 等待 → `web_eval` 检查新内容 → 重复直到够用

## 与现有工具的协作

- `web_fetch` 适合一次性静态抓取，速度快无登录态
- `scrapling-web-fetch` 适合正文提取，处理现代网页反爬
- CDP 工具适合前三者搞不定的情况：需要登录、需要交互、需要连续操作
- 截图后可以用 `read_file` 查看图片内容（视觉模型识别）
