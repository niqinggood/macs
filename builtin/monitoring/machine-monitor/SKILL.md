---
name: machine-monitor
description: Collect a read-only health overview from multiple caller-supplied hosts without storing credentials in inventory or reports.
---

# Machine Monitor

Collect comparable OpenKitty and host metrics from a fleet.

Use `scripts/monitor.py` with a copy of `servers.example.json`:

```bash
python3 scripts/monitor.py <inventory.json> \
  --output storage/fleet_report.md
```

The inventory contains routing metadata and optional environment-variable names
that resolve identity-file paths. It never contains credential values.

## Inventory format

Store only non-secret routing metadata:

```json
{
  "servers": [
    {
      "name": "production-a",
      "host": "<ssh-config-alias>",
      "user": "<ssh-user>",
      "identity_env": "<identity-file-environment-variable>",
      "openkitty_path": "<remote-path>"
    }
  ]
}
```

`identity_env` contains only the name of an environment variable whose runtime
value is an identity-file path. Never replace it with a password, token, key
material, or the identity-file contents. Prefer SSH config aliases so the
inventory needs no identity fields.

## Collection workflow

1. Validate every inventory entry and deduplicate targets.
2. Resolve authentication in memory at invocation time.
3. Check connections with bounded timeouts.
4. Run read-only checks for CPU, load, memory, disk, uptime, service status, `/livez`, `/readyz`, and queue metrics.
5. Limit concurrency so monitoring does not overload the fleet.
6. Redact addresses and command output that resembles credentials.
7. Generate `storage/fleet_report.md` containing display names, status, metrics, observation timestamps, and errors only.

## Output

| Server | Connectivity | CPU/load | Memory | Disk | OpenKitty | Queue |
|---|---|---|---|---|---|---|
| display name | online/offline | measured value | measured value | measured value | ready/not ready | running/queued |

Monitoring is read-only. Any restart, deployment, cleanup, or configuration change must be handled as a separate explicitly authorized task.
