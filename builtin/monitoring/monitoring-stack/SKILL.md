---
name: monitoring-stack
description: Set up observability — metrics, logs, traces, and alerts — for applications and infrastructure.
---

# Monitoring & Observability

## Complexity Levels

| Level | Tools | Setup Time | Best For |
|-------|-------|------------|----------|
| Minimal | UptimeRobot, Healthchecks.io | 15 min | Side projects, MVPs |
| Standard | Uptime Kuma, Sentry, basic Grafana | 1-2 hours | Small teams |
| Professional | Prometheus, Grafana, Loki, Alertmanager | 1-2 days | Production |
| Enterprise | Datadog, New Relic, full OSS stack | Ongoing | Large-scale |

## Three Pillars

| Pillar | Answers | Tools |
|--------|---------|-------|
| Metrics | "How is the system performing?" | Prometheus, Grafana, Datadog |
| Logs | "What happened?" | Loki, ELK, CloudWatch |
| Traces | "Where is the bottleneck?" | Jaeger, Tempo, Datadog APM |

## Alerting Rules
- Alert on symptoms, not causes — "service down" not "CPU high"
- Every alert must have a runbook link
- P1 (wake up) vs P2 (next business day) vs P3 (ticket)
- Silence during maintenance windows
- Alert fatigue kills response — keep signal/noise high

## Dashboard Design
- Row 1: golden signals (latency, traffic, errors, saturation)
- Row 2: resource health (CPU, memory, disk per instance)
- Row 3: business metrics (orders/min, active users, revenue)
- Use red/green thresholds sparingly — colorblind-friendly palette

## Common Stack
```yaml
prometheus: metrics collection + alerting rules
grafana: dashboards + alerting UI
loki: log aggregation
promtail: log shipping
alertmanager: alert routing (PagerDuty/Slack/Feishu)
```
