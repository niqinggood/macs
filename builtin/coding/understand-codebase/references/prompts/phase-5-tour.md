# Phase 5: 学习路径生成 (tour-builder)

## 目标

为新人或接手者生成结构化的代码学习路径。

## 输入

Phase 2+3+4 的全部输出。

## Prompt

```
Based on the complete codebase analysis below, create an onboarding learning path:

[All previous phase outputs]

Design a structured learning path for a new developer:
1. Day 1-2: Environment setup and project overview — which files to read first
2. Day 3-5: Core modules deep dive — ordered reading list with rationale
3. Week 2: Key business flows — trace through the most important workflows
4. Week 3+: Advanced topics — extension points, performance critical paths, testing strategy

For each phase, list specific files to read in order, with a brief explanation of why each file matters.

Also generate a "Quick Reference Card" — the top 10 files/functions a developer needs to know to be productive.
```

## 输出格式

```
【onboarding 学习路径】

Day 1: 环境搭建 + 项目全景
  - 阅读 README，搭建开发环境
  - 运行项目，体验核心功能
  - 阅读架构分析，理解分层结构
  - 推荐阅读文件：[file list]

Day 2-3: 核心模块深入
  - 跟随一个请求的完整生命周期
  - 推荐阅读顺序：[ordered file list]
  ...
```
