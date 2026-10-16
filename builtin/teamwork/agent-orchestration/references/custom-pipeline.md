# 自定义流水线配置

## 配置文件位置

在 `storage/pipelines/` 下创建 YAML 配置文件。

## 配置格式

```yaml
# storage/pipelines/my-pipeline.yaml
name: my-pipeline
description: "我的自定义流水线"

phases:
  - id: scan
    role: Scanner
    agent_count: 1
    prompt_file: ./prompts/scan.md
    timeout: 300
    
  - id: analyze
    role: Analyzer
    agent_count: 5
    batch_size: 25
    prompt_file: ./prompts/analyze.md
    timeout: 300
    retry: 2
    
  - id: synthesize
    role: Synthesizer
    agent_count: 1
    depends_on: [analyze]
    prompt_file: ./prompts/synthesize.md
    
  - id: review
    role: Reviewer
    agent_count: 1
    depends_on: [synthesize]
    prompt_file: ./prompts/review.md

quality_gates:
  analyze:
    coverage: 0.90
  synthesize:
    min_layers: 2
  review:
    pass_rate: 0.80
    critical_issues: 0

output_dir: storage/pipeline/{pipeline_name}/
```

## 字段说明

| 字段 | 说明 | 必需 |
|------|------|------|
| `name` | 流水线名称，用于 `/orchestrate {name}` 启动 | 是 |
| `description` | 流水线用途描述 | 否 |
| `phases[].id` | Phase 唯一标识 | 是 |
| `phases[].role` | Agent 角色类型 | 是 |
| `phases[].agent_count` | 并发 agent 数量（默认 1） | 否 |
| `phases[].batch_size` | 单 agent 处理单元数（默认 30） | 否 |
| `phases[].prompt_file` | 相对于 skill 根目录的 prompt 文件 | 是 |
| `phases[].depends_on` | 依赖的 Phase ID 列表 | 否 |
| `phases[].timeout` | 超时秒数（默认 300） | 否 |
| `phases[].retry` | 失败重试次数（默认 2） | 否 |
| `quality_gates` | Phase 级质量门禁阈值 | 否 |
| `output_dir` | 输出目录 | 是 |

## 与业务 Skill 的关系

业务 skill（如 understand-codebase）定义每个 Phase 的具体 prompt 内容（在 `references/prompts/` 中），agent-orchestration 负责按 YAML 配置执行这些 prompt 的编排。

两者通过流水线名关联：业务 skill 注册模板 → agent-orchestration 执行模板。
