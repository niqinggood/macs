---
name: monitoring
description: System health checking, machine resource monitoring, and monitoring stack setup
version: 1.0.0
author: OpenKitty
status: stable
triggers:
  - health
  - monitor
  - monitoring
  - cpu
  - memory
  - disk
  - uptime
  - alert
  - 监控
  - 健康检查
  - 资源
---

# Monitoring Skills

System and machine health monitoring — from quick one-off checks to full stack deployment.

## Sub-skills

### health-check
**Use when**: User wants a quick one-time health check of a server or service. Checks: uptime, CPU load, memory usage, disk space, key service status.

Examples: "检查一下服务器状态" / "Is the server healthy?" / "Check if nginx is running"

### machine-monitor
**Use when**: User wants to set up ongoing resource monitoring (CPU, memory, disk, network). Watches a machine over time and reports anomalies. Not a one-off check — continuous observation.

Examples: "监控这台机器的内存使用" / "Watch CPU temperature on this server"

### monitoring-stack
**Use when**: User wants to deploy a full monitoring infrastructure (Prometheus + Grafana + AlertManager). For dashboards, alert rules, and data pipelines — not ad-hoc checks.

Examples: "部署一套监控系统" / "Set up Prometheus and Grafana"

## Decision Guide

| Need | Use |
|------|-----|
| One-time status check | `health-check` |
| Continuous resource watching | `machine-monitor` |
| Full infra deployment | `monitoring-stack` |

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
