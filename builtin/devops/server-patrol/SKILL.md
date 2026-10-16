---
name: server-patrol
description: Linux服务器+网络设备只读健康巡检。批量检查CPU/内存/磁盘/服务/端口/网络连通性，输出OK/WARN/CRIT报告。
read_when:
  - 服务器巡检、网络设备巡检、健康检查
  - 批量检查机器状态、定时巡检
---

# 基础设施巡检

面向 Linux 服务器和网络设备的只读巡检。不修改配置、不重启服务。

## 检查项

| 对象 | 检查项 |
|------|--------|
| Linux | uptime、CPU、内存、磁盘、systemd 服务、Docker 容器 |
| 网络 | Ping 连通、TCP 端口、Nginx/MySQL/Redis 状态 |

## 使用

```bash
# 进入skill目录
cd skills/server-patrol

# 批量巡检（从 openkitty ops_hosts 配置读取目标）
python3 scripts/patrol.py --all

# 指定目标
python3 scripts/patrol.py --host 10.0.1.5 --user root
python3 scripts/patrol.py --hosts 10.0.1.5,10.0.1.6

# JSON输出
python3 scripts/patrol.py --all --format json
```

## 输出

每个目标输出 OK / WARN / CRIT 三级状态，含具体数值和建议。

## 安全

- 只读操作，禁止 reboot、shutdown、写配置
- 密码/密钥不输出到日志
- 使用 SSH 密钥连接（默认 ~/.ssh/id_rsa）
