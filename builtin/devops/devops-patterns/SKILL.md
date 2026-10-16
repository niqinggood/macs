---
name: devops-patterns
description: CI/CD pipelines, deployment strategies, and infrastructure automation patterns.
---

# DevOps Patterns

## CI/CD Pipelines
- Fail fast: lint + unit tests before expensive integration tests
- Cache dependencies — `npm install` on every build wastes minutes
- Pin action versions with SHA — tags can change, SHA is immutable
- Secrets in environment variables, never in code or logs
- Parallel jobs for independent steps — test/lint/build can run together

## Deployment Strategies

| Strategy | How | Rollback | Risk |
|----------|-----|----------|------|
| Blue-green | New + old run together, switch traffic at once | Instant | Low |
| Canary | Route % traffic to new version gradually | Per-route | Lowest |
| Rolling | Update instances one by one | Reverse order | Medium |
| Recreate | Stop old, start new | Restore old | High downtime |

- Always have rollback plan before deploying
- Database migrations must be backward-compatible
- Feature flags decouple deploy from release

## Infrastructure as Code
- Terraform/Pulumi for cloud resources, Ansible for server config
- State files in remote backend (S3/GCS) with locking
- Plan before apply — always review changes
- Tag everything for cost tracking

## When Asked About Deployments
1. What's the current state? (kubectl/terraform show)
2. What's changing? (diff the config)
3. What's the rollback plan?
4. How to verify? (health checks, smoke tests)
5. Who to notify? (Slack/Feishu channel)
