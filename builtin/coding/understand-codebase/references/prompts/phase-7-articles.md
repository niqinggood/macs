# Phase 7: 文档知识库图谱化 (article-analyzer)

## 目标

如果项目有 Wiki、docs、README 等文档，将其内容结构化为知识图谱节点。

## 输入

项目文档目录或 Wiki 链接。

## Prompt

```
Analyze the following project documentation and link it to the code analysis:

Documentation files:
{doc_file_list}

[Phase 1-5 outputs]

For each document:
1. Extract key topics and concepts
2. Link to the relevant code modules from earlier phases
3. Identify gaps — documented features not found in code, or code features not documented
4. Build cross-references between documents and code

The goal is a unified knowledge graph connecting docs ↔ code.
```

## 输出格式

```
【文档知识图谱】
文档节点：
  - README.md → [项目介绍, 快速开始, 架构概览]
  - docs/api.md → [REST API 端点列表, 认证方式, 请求/响应格式]

跨文档关联：
  - README 中提到的 "X" → docs/X.md 第3节

与代码图谱的关联：
  - docs/api.md 描述的端点 → 代码中的具体位置
```
