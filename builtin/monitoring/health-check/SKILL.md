---
name: health-check
description: Run read-only health checks against an OpenKitty host using a caller-supplied target and external SSH authentication.
---

# Health Check

Inspect one OpenKitty host without embedding or persisting infrastructure credentials.

Use the included `scripts/health_check.py` for a directly executable check:

```bash
python3 scripts/health_check.py \
  --host <ssh-config-alias> \
  --service <service-name>
```

The script uses SSH config, ssh-agent, or an identity file passed at runtime. It
does not accept or persist passwords.

## Inputs

- `<host>` or SSH config alias.
- `<ssh-user>` or SSH config identity.
- Optional service name and OpenKitty installation path.
- Authentication supplied through SSH agent, key, or secret store at invocation time.

Never save a password, token, private key, or real host to a generated script or report.

## Procedure

1. Use `remote(action="check_connection", host="<host>", username="<ssh-user>")`.
2. Run read-only commands through `remote(action="exec", ...)` to collect:
   - hostname and uptime;
   - CPU load and core count;
   - memory and disk utilization;
   - service-manager status;
   - listening port state;
   - `/livez`, `/readyz`, and queue metrics;
   - recent error logs, with secrets redacted.
3. Do not restart, kill, write files, or change configuration during a health check.
4. Report the target by caller-provided display name or masked address.

## Suggested thresholds

| Metric | Warning | Critical |
|---|---:|---:|
| Memory | over 80% | over 95% |
| Disk | over 80% | over 90% |
| Load | over core count | over twice core count |
| `/readyz` | intermittent failure | sustained failure |

The report should include observation time, evidence, and recommended next action. It must not include connection parameters or authentication material.
