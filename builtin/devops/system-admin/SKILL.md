---
name: system-admin
description: "Windows system administration — check disk space, memory, list/kill processes, check services, view system info, monitor resources. Use when user asks about system health, disk usage, running programs, or Windows services. Tool: sys. Always call tool_detail first."
read_when:
  - Check disk space or memory usage / 查磁盘、内存使用、还剩多少空间
  - List or kill running processes / 查看进程、杀掉进程
  - Monitor system resources / 监控系统资源
  - Run system diagnostics or health checks / 系统诊断、健康检查
  - Manage Windows services / 管理Windows服务
---

# System Administration

## Step 1: Get tool parameters

```
tool_detail("sys")
```

## Step 2: Use the tool

- `sys(action, ...)` — perform admin tasks. Common actions: `check_disk`, `check_memory`, `list_processes`, `kill_process`, `check_service`, `system_info`.
