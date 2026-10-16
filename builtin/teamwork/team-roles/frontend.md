# 前端开发规范

## 技术栈偏好
- 优先使用 HTML5 + CSS3 + 原生 JS，避免引入过重框架
- 如需框架: React/Vue (CDN) 或 Svelte
- UI 组件: 优先手写 CSS，其次 Tailwind CDN

## 代码规范
- 语义化 HTML 标签 (`<header>`, `<nav>`, `<main>`, `<section>`, `<article>`)
- CSS 变量统一管理颜色/字体/间距
- class 命名用 BEM 或功能语义化
- JS 函数单一职责，命名见名知意
- 所有交互要有过渡动画 (transition 0.2s-0.3s)

## 响应式设计
- 移动优先 (min-width: 320px)
- 断点: 480px / 768px / 1024px / 1440px
- 图片用 max-width: 100% + 懒加载
- 触摸目标最小 44x44px

## 性能
- CSS 放 `<head>`, JS 放 `</body>` 前或 async/defer
- 关键 CSS 内联，非关键异步加载
- 字体用系统栈: `-apple-system, BlinkMacSystemFont, 'Segoe UI', 'PingFang SC', 'Microsoft YaHei'`
- 避免超过 3 层 DOM 嵌套

## 可访问性 (a11y)
- 所有交互元素有 `aria-label` 或可读文本
- 表单有 `<label>` 关联
- 颜色对比度 ≥ 4.5:1
- 支持键盘导航 (Tab/Enter/Escape)

## 输出要求
- 完整可运行的 HTML 文件或组件代码
- 包含必要的 CSS/JS 内联
- 代码有简要注释说明关键逻辑
- 交付时附带用法说明
