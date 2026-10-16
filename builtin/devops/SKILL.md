---
name: devops
version: 1.1.0
author: OpenKitty
status: stable
description: DevOps workflows for backup, deployment, Kubernetes, patrol, and system administration without embedding environment-specific credentials.
---

# DevOps

Use the smallest specialized skill that matches the request:

- `backup-restore`: create, verify, and restore OpenKitty backups.
- `remote_deploy`: generic remote operations through the `remote` tool.
- `server-deploy`: build and deploy OpenKitty to Linux.
- `server-patrol`: read-only host inspection.
- `storage-cleanup`: inspect and clean storage with explicit target validation.
- `k8s-best-practices`: Kubernetes manifests, probes, rollout, and capacity.
- `cross-compile`: cross-platform builds.

## Non-negotiable rules

1. Never put a real host, username, password, token, private key, or local operator path in a skill, script, example, log, or artifact.
2. Receive the target and identity at invocation time. Prefer an SSH agent, an existing SSH config host alias, or a secret-store reference.
3. Run `check_connection` and a dry run before a remote mutation.
4. Back up the exact affected paths and verify the backup before deployment.
5. Monitoring and patrol remain read-only. Deployment, restart, restore, and cleanup require explicit user authorization.
6. Keep a rollback target and verify service health after every mutation.
7. Never weaken host-key verification merely to make an example shorter.

## Standard remote flow

1. Collect `<host>`, `<ssh-user>`, `<remote-path>`, and an authentication reference from the caller.
2. Load the `remote` tool schema with `tool_detail("remote")` if it is not already available.
3. Check connectivity.
4. Preview with `dry_run=true`.
5. Execute only the authorized operation.
6. Verify status, readiness, logs, and rollback viability.

Examples must always use placeholders such as `<host>` and `<ssh-user>`. Credentials are supplied per call by the runtime and must never be persisted in repository files.
