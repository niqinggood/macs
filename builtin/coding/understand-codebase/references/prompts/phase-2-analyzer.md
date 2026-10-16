# Phase 2: 并行文件分析 (file-analyzer)

## 目标

对每个核心文件进行符号提取和依赖分析。

## 执行方式

将文件分成每批 20-30 个，每批启动一个 agent，最多 5 并发。

## 并发控制

- 单批不超过 30 个文件
- 同时运行不超过 5 个 agent
- 每个 agent 超时 5 分钟
- 失败重试 2 次，指数退避

## 输出格式（每批）

```
【模块分析：{module_name}】
文件总数：N
核心符号：
  - 公开函数/类/接口及签名
  - 关键数据结构
内部依赖关系：
  - 谁调用了谁（模块内）
外部依赖：
  - import 了哪些外部包
  - 引用了哪些项目内其他模块
```

## Prompt

```
Analyze the following files in {module_path}:

{file_list}

For each file, report:
1. Public/exported symbols (functions, classes, interfaces, structs) with signatures
2. Internal dependencies — which symbols in this module reference which other symbols
3. External imports — packages/modules from outside this module
4. Key patterns or notable design choices

Focus on the interface surface — what does this module expose to other modules?
```
