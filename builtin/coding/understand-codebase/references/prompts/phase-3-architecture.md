# Phase 3: 架构分层识别 (architecture-analyzer)

## 目标

综合 Phase 1+2 的结果，识别项目的架构分层和模式。

## 输入

Phase 1 的项目概览 + Phase 2 的所有模块分析。

## Prompt

```
Based on the project overview and module analyses below, identify the architecture:

[Phase 1 output]
[Phase 2 outputs]

Analyze:
1. Architectural layers — what layers exist and how are they organized?
2. Dependency direction — which layers depend on which? Any violations?
3. Design patterns in use (MVC, layered, hexagonal, event-driven, CQRS, etc.)
4. Key data flows — trace a typical request through the system
5. Circular dependency warnings — any cycles that should be broken?
6. Architectural boundaries — where are the natural seams for refactoring?

Draw an ASCII layer diagram showing the architecture.
```

## 输出格式

```
【架构分析】
分层结构：
  ┌──────────────────┐
  │  表示层           │
  ├──────────────────┤
  │  业务逻辑层       │
  ├──────────────────┤
  │  数据访问层       │
  ├──────────────────┤
  │  基础设施层       │
  └──────────────────┘

层间依赖规则：...
识别到的设计模式：...
关键数据流：...
循环依赖警告：...
```
