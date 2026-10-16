---
name: backup-restore
description: Back up and restore OpenKitty configuration and data using caller-supplied targets and external authentication, without persisting credentials.
---

# Backup and Restore

Create restorable OpenKitty backups without embedding infrastructure details in generated scripts.

Reusable implementations are included in `scripts/backup_kitty.py` and
`scripts/restore_kitty.py`. They use the system SSH client, accept every target
and path as an argument, and rely on SSH config, ssh-agent, or a caller-supplied
identity file. They intentionally do not accept passwords.

```bash
python3 scripts/backup_kitty.py \
  --host <ssh-config-alias> \
  --source-root <remote-openkitty-root> \
  --include config --include data --include storage

python3 scripts/restore_kitty.py <backup-archive> \
  --host <ssh-config-alias> \
  --target-root <remote-openkitty-root>
```

The restore command prints a plan by default and requires `--execute` before it
changes remote state.

## Required input

- Target: `<host>` or an existing SSH config alias.
- Identity: `<ssh-user>` or the identity defined by SSH config.
- OpenKitty data paths and destination path.
- Database type: SQLite or MySQL.
- Authentication supplied at invocation time through SSH agent, key, or secret store.

Never write authentication material into `storage/`, generated Python, Markdown, shell history, task output, or logs.

## Backup workflow

1. Resolve and display the exact source and destination paths.
2. Use `remote(action="check_connection", host="<host>", username="<ssh-user>")`.
3. Preview the backup with `remote(action="backup", ..., dry_run=true)`.
4. Create the backup with `backup_dirs` limited to the requested OpenKitty paths.
5. Back up the database consistently:
   - SQLite: stop writes or use the SQLite online backup mechanism.
   - MySQL: use a database-native consistent dump supplied by the deployment environment.
6. Record size and checksum, then test archive listing or restoration into an isolated directory.
7. Remove temporary remote archives only after download and verification succeed.

Configuration backups can contain provider keys and channel credentials. Restrict access to the backup and use encryption supplied by the deployment environment.

## Restore workflow

1. Require explicit restore authorization and identify the rollback point.
2. Validate checksum and archive paths; reject absolute paths and parent traversal.
3. Stop or drain OpenKitty cleanly.
4. Restore into a staging directory first.
5. Atomically switch to the restored data where possible.
6. Start OpenKitty and verify `/readyz`, task state, and logs.
7. Roll back if verification fails.

Do not generate one-line commands containing credentials. If password authentication is unavoidable, pass it only in the protected tool invocation and never echo or persist it.
