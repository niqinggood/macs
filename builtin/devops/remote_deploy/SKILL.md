---
name: remote_deploy
description: Safe deployment workflow for remote servers via SSH — check connection, dry-run, backup, deploy. Use when deploying to remote servers or running commands on remote hosts. / SSH远程部署、连接服务器、远程执行命令
---

# Remote Deployment

Safe deployment workflow for remote servers via SSH. Uses the `remote` tool (not in prompt — call `tool_detail("remote")` first to get parameters).

## Rules

1. **Check connection first**: Use remote(action="check_connection") to verify SSH access before any operation.
2. **Backup before deploy**: Use remote(action="deploy", backup_dirs="/opt/app") for safe full deploy. Never deploy without backup.
3. **Dry-run first**: Set dry_run=true to preview changes before executing.
4. **Prefer deploy over manual steps**: remote(action="deploy") does backup → upload → restart → verify in one safe flow.
5. **Credentials in params only**: Pass host/username/password per call, never hardcode or log them.
6. **No destructive commands**: rm -rf, fdisk, shutdown, and other dangerous commands are blocked.

## Typical Flow

```
1. remote(action="check_connection", host="...", username="...", password="...")
2. remote(action="deploy", host="...", backup_dirs="/opt/app", dry_run=true)  // preview
3. remote(action="deploy", host="...", backup_dirs="/opt/app")                // actual deploy
```
