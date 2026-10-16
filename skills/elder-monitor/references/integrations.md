# Integration contracts

Read this reference when connecting voice, multimodal, notification, OpenKitty, or another agent runtime.

The classes in `scripts/adapters.py` are dependency-free protocols and mocks. They perform no network calls and send no real notifications.

## Voice check

```python
check_id = voice.request_check(
    person_id="elder_demo_001",
    prompt="您还好吗？如果需要帮助，请说需要帮助。",
    timeout_sec=30,
)
result = voice.get_result(check_id)
```

Required normalized result fields:

- `responded`: `true`, `false`, or `null` when the service failed.
- `intent`: `okay`, `need_help`, or `unknown`.
- `confidence`: 0 to 1.
- `service_status`: `ok`, `timeout`, or `error`.

Never translate `service_status=error` into `responded=false`.

## Multimodal analysis

```python
observations = multimodal.analyze(sanitized_payload)
```

Run `redact_cloud_payload.py` first. The adapter should return only normalized observations with source, model version, confidence, and opaque evidence references. Do not return a disease diagnosis.

Cloud failure must not lower or delay a deterministic `high` or `critical` result.

## Alert dispatch

```python
alert_id = alerts.create_alert(
    assessment=assessment,
    idempotency_key=assessment["assessment_id"],
)
```

The production adapter must implement authentication, least privilege, idempotency, retries with a stopping condition, acknowledgement timeout, escalation policy, and an audit log. Emergency-service integration requires separate authorization and governance.

## OpenKitty adapter

Register a tool equivalent to:

```text
assess_elder_event(event: object) -> assessment: object
```

The tool may call `scripts/evaluate_event.py` as a subprocess or expose the same core function through a local service. Keep schema and policy files unchanged so the same golden tests can run in other agent frameworks.

Recommended agent sequence:

1. Fetch or receive the recent event window.
2. Normalize and validate.
3. Run deterministic assessment.
4. Execute only actions that the caller has authorized.
5. Persist the assessment and acknowledgement outcome.

## AgentOS synthetic-signal test

Run all deterministic scenarios locally first:

```text
python scripts/simulate_agentos.py --mode local --pretty
```

For a human-readable walkthrough that prints each scenario's meaning, expected rule, decision reason, and exact event JSON before execution, run:

```text
python scripts/test_elder_monitor_scenarios.py --mode local \
  --output elder-monitor-test-report.json
```

Then exercise the installed Skill through AgentOS. Keep the service credential in an environment variable rather than a command line or report:

```text
OPENKITTY_AGENTOS_TOKEN=<service-token> python scripts/simulate_agentos.py \
  --mode both \
  --base-url http://127.0.0.1:18080 \
  --role-id Assistant \
  --output elder-monitor-simulation.json \
  --pretty
```

On PowerShell, set `$env:OPENKITTY_AGENTOS_TOKEN` for the current process and run the same Python command without the inline assignment. `--role-id` is optional; the simulator otherwise chooses the first enabled role returned to the authenticated service client and pins its revision on task submission.

Keep the repository source at `skills/elder-monitor`. For a packaged OpenKitty server, deploy the runtime copy to `<openkitty-workdir>/.openkitty/skills/elder-monitor`; authenticated AgentOS tasks reload that managed runtime directory and the subject's private Skill directory. A source checkout's top-level `skills` directory is not a substitute for the managed runtime directory in a user-scoped AgentOS task.

Each scenario uses an independent session and idempotency key. The AgentOS prompt explicitly forbids notifications, calls, emergency dispatch, external writes, or execution of `recommended_actions`. The report records only task identity, Skill-run phase, parsed assessment, and pass/fail status; it never includes the token. A task passes only when it reaches `completed` or `degraded`, the Skill-run receipt is `completed`, and the returned severity matches the deterministic expectation.
