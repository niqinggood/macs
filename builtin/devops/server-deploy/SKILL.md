---
name: server-deploy
description: Build and deploy OpenKitty to a remote Linux service using parameterized targets, verified backups, dry runs, and health checks.
---

# Server Deploy

Deploy OpenKitty without storing a server address or credential in the skill or generated artifacts.

The reusable implementation is `scripts/deploy_kitty.py`. It uses the system
SSH client and accepts the host, identity, binary, installation path, service,
and health URL as arguments. Authentication remains in SSH config, ssh-agent,
or an identity file supplied at invocation time.

```bash
python3 scripts/deploy_kitty.py \
  --host <ssh-config-alias> \
  --local-binary <local-openkitty-binary> \
  --remote-path <remote-binary-path> \
  --service <service-name> \
  --dry-run
```

Remove `--dry-run` only after reviewing the generated plan.

## Inputs

- `<host>` or SSH config alias.
- `<ssh-user>` supplied by the caller or SSH config.
- Local binary path.
- Remote installation path.
- Service name and health endpoint.
- Authentication supplied per invocation through SSH agent, key, or secret store.

## Workflow

1. Build the Linux binary and run the release credential guard.
2. Load the `remote` tool schema when needed.
3. Call `check_connection` for the caller-provided target.
4. Inspect current service status and free space.
5. Run `deploy` with the exact `local_path`, `remote_path`, `backup_dirs`, and `service`, first with `dry_run=true`.
6. Present the plan and obtain authorization for the mutation.
7. Run the same deployment without dry-run.
8. Verify `/livez`, `/readyz`, version, logs, and queue health.
9. Restore the verified backup if health checks fail.

Do not use hard-coded process killing, host-key bypass, privileged usernames, or credentials in scripts. Prefer the service manager and a least-privileged deployment identity.

## Example shape

```text
remote(
  action="deploy",
  host="<host>",
  username="<ssh-user>",
  local_path="<local-binary>",
  remote_path="<remote-path>",
  backup_dirs="<data-path>,<config-path>",
  service="<service-name>",
  dry_run=true
)
```

The runtime may add protected authentication to the tool call. Never copy it into this example, a worklog, or deployment output.
