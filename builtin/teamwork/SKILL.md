---
name: teamwork
description: Multi-agent collaboration — orchestration, sub-agents, team research, and role-based teams
version: 1.0.0
author: OpenKitty
status: stable
triggers:
  - agent orchestration
  - sub-agent
  - team
  - swarm
  - parallel
  - collaborate
  - assemble team
  - 多智能体
  - 协作
---

# Teamwork Skills

Multi-agent coordination patterns. When one agent isn't enough, use these to split work across multiple agents.

## Sub-skills

### agent-orchestration
**Use when**: User needs to coordinate multiple agents in a structured workflow — either a sequential pipeline or parallel fan-out with merge. Best for complex multi-step tasks where steps have dependencies.

Examples: "用多个 agent 协作完成这个项目" / "Orchestrate a code review + deploy pipeline"

### sub-agent
**Use when**: User needs to delegate a single well-defined sub-task to a child agent. The sub-agent works independently and returns results. Best for isolated tasks that don't need coordination with other agents.

Examples: "让一个 agent 去查这个 API 文档" / "Spawn a sub-agent to research X"

### team-research
**Use when**: User needs multiple agents to research different aspects of a topic in parallel, then merge findings. Each agent takes a different angle (technical, market, competitive).

Examples: "全面调研这个行业" / "Research topic X from multiple angles"

### team-roles
**Use when**: User wants to assemble a team where each agent plays a specific role (architect, developer, reviewer, tester). Best for software projects requiring different expertise.

Examples: "组建一个开发团队来做这个功能" / "Assemble a dev team with architect + developer + QA"

## Decision Guide

| Need | Use |
|------|-----|
| One sub-task, fire-and-forget | `sub-agent` |
| Multi-step pipeline with dependencies | `agent-orchestration` |
| Parallel research from different angles | `team-research` |
| Role-based dev team | `team-roles` |

## IRON RULES

1. **Tool safety**: NEVER use curl or raw CLI commands for external API calls. Use Python (`write_file` + `code_check run`) instead.
2. **Read before act**: Always research/read existing context before creating output. Don't work blind.
3. **Quality over speed**: Every output must be complete, accurate, and well-structured. If you can't do it right, explain what's missing rather than fabricating.
4. **Output to `.openkitty/storage/`**: All generated files go to `.openkitty/storage/`. Never leave artifacts in the project root.
5. **No fabricated data**: Don't invent numbers, citations, names, or facts. If uncertain, state your uncertainty clearly.

## Quality Standards

| ✅ Good | ❌ Bad |
|---------|--------|
| Well-structured with clear headings and logical flow | Flat text with no organization |
| Specific, actionable, data-backed | Vague generalities, no specifics |
| Original analysis and insight | Copy-paste from sources without value-add |
| Proper Markdown formatting | Plain text dump |

Output: `.openkitty/storage/`
