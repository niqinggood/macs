# Deployment patterns

Repository source lives at `skills/elder-monitor`. A packaged server should install the runtime copy at `<openkitty-workdir>/.openkitty/skills/elder-monitor`, preserving the whole directory so scripts, schemas, rules, tests, and references remain available to a frozen AgentOS Skill Run.

Read this reference when choosing triggers, background execution, or production topology.

## Event-driven path

Use device gateway -> message broker or webhook -> assessment worker for:

- suspected falls;
- explicit help requests;
- distress clusters;
- no-response results;
- sensor or gateway outages.

Use stable event IDs, bounded retries, a dead-letter queue, and per-person ordering where later events depend on earlier voice-check state.

## Scheduled path

Use a scheduler for:

- unacknowledged-alert review;
- hourly activity trends;
- daily caregiver summaries;
- stale-data and device-health checks;
- periodic policy replay.

Scheduled execution does not replace real-time triggers. For a development-only local Codex scheduled task, the machine and desktop app must remain running. Production monitoring should use a service supervisor, container platform, or managed scheduler appropriate to the deployment environment.

## Recommended processes

```text
edge-gateway
event-normalizer
window-aggregator
risk-assessment-worker
voice-check-worker
alert-dispatcher
audit-store
operator-console
```

The risk worker must start and run with no cloud model configured. Voice, multimodal, and notification credentials belong in a secret manager, never in this skill.

## Rollout

1. Offline replay.
2. Shadow mode with human labels.
3. Internal caregiver notifications.
4. Limited pilot with approved escalation.
5. Broader rollout only after reviewing misses, false alerts, device failures, privacy requests, and incident response.

