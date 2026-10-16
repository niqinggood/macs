#!/usr/bin/env python3
"""
Server Patrol — read-only health inspection for Linux servers.
Usage: python3 patrol.py --all | --host <ip> [--user <name>] [--format json]
"""
import subprocess, json, sys, os, argparse, time

def ssh_run(host, user, cmd, timeout=10):
    """Run command via SSH and return (stdout, stderr, exit_code)."""
    key_path = os.path.expanduser("~/.ssh/id_rsa")
    ssh_cmd = ["ssh", "-o", "StrictHostKeyChecking=no", "-o", "ConnectTimeout=5",
               "-i", key_path, f"{user}@{host}", cmd]
    try:
        r = subprocess.run(ssh_cmd, capture_output=True, text=True, timeout=timeout)
        return r.stdout.strip(), r.stderr.strip(), r.returncode
    except subprocess.TimeoutExpired:
        return "", "timeout", -1
    except Exception as e:
        return "", str(e), -1

def check_linux(host, user):
    """Run standard Linux health checks."""
    results = {"host": host, "user": user, "checks": [], "timestamp": time.strftime("%Y-%m-%d %H:%M:%S")}

    checks = [
        ("uptime", "uptime -p"),
        ("load", "cat /proc/loadavg | awk '{print $1,$2,$3}'"),
        ("cpu", "top -bn1 | grep 'Cpu(s)' | awk '{print 100-$8\"%\"}'"),
        ("memory", "free -m | awk 'NR==2{printf \"%.1f%% (%.0fM/%.0fM)\",$3/$2*100,$3,$2}'"),
        ("disk_root", "df -h / | awk 'NR==2{print $5\" (\"$4\" free)\"}'"),
        ("processes", "ps aux --no-headers | wc -l"),
        ("zombie", "ps aux | awk '{if($8==\"Z\") print}' | wc -l"),
        ("docker", "docker ps --format 'table {{.Names}}\t{{.Status}}' 2>/dev/null || echo 'docker not running'"),
        ("sshd", "systemctl is-active sshd 2>/dev/null || systemctl is-active ssh 2>/dev/null || echo 'unknown'"),
        ("last_login", "last -n 3 2>/dev/null | awk '{print $1,$3,$4,$5,$6,$7}'"),
    ]

    for name, cmd in checks:
        out, err, code = ssh_run(host, user, cmd)
        status = "OK"
        if code != 0:
            status = "WARN"
        if name == "cpu":
            try:
                v = float(out.replace("%",""))
                if v > 80: status = "CRIT"
                elif v > 60: status = "WARN"
            except: pass
        elif name == "memory":
            try:
                v = float(out.split("%")[0])
                if v > 90: status = "CRIT"
                elif v > 75: status = "WARN"
            except: pass
        elif name == "disk_root":
            try:
                v = int(out.split("%")[0])
                if v > 90: status = "CRIT"
                elif v > 80: status = "WARN"
            except: pass
        elif name == "zombie":
            try:
                if int(out) > 5: status = "WARN"
            except: pass

        results["checks"].append({"name": name, "status": status, "output": out or err[:100]})

    return results

def format_report(results, fmt="text"):
    if fmt == "json":
        return json.dumps(results, ensure_ascii=False, indent=2)

    lines = []
    lines.append(f"# 巡检报告: {results['host']} ({results['timestamp']})")
    lines.append("")
    lines.append("| 检查项 | 状态 | 结果 |")
    lines.append("|--------|------|------|")
    for c in results["checks"]:
        icon = {"OK": "✅", "WARN": "🟡", "CRIT": "🔴"}.get(c["status"], "❓")
        lines.append(f"| {c['name']} | {icon} {c['status']} | {c['output'][:100]} |")

    crits = [c for c in results["checks"] if c["status"] == "CRIT"]
    warns = [c for c in results["checks"] if c["status"] == "WARN"]
    if crits or warns:
        lines.append("")
        lines.append("## 需要关注")
    for c in crits:
        lines.append(f"- 🔴 {c['name']}: {c['output'][:100]}")
    for c in warns:
        lines.append(f"- 🟡 {c['name']}: {c['output'][:100]}")

    return "\n".join(lines)

if __name__ == "__main__":
    p = argparse.ArgumentParser()
    p.add_argument("--host", help="Single host IP")
    p.add_argument("--hosts", help="Comma-separated host IPs")
    p.add_argument("--user", default="root", help="SSH user")
    p.add_argument("--all", action="store_true", help="Use openkitty ops_hosts config")
    p.add_argument("--format", default="text", choices=["text","json"])
    args = p.parse_args()

    hosts = []
    if args.host:
        hosts = [args.host]
    elif args.hosts:
        hosts = args.hosts.split(",")
    elif args.all:
        # Try reading openkitty config for ops_hosts
        try:
            import yaml
            with open("config.yaml") as f:
                cfg = yaml.safe_load(f)
            for h in cfg.get("ops_hosts", []):
                hosts.append(h["host"])
                if not args.user or args.user == "root":
                    args.user = h.get("user", "root")
        except:
            print("ERROR: --all requires config.yaml with ops_hosts section", file=sys.stderr)
            sys.exit(1)
    else:
        print("Usage: patrol.py --host <ip> | --all", file=sys.stderr)
        sys.exit(1)

    all_reports = []
    for host in hosts:
        host = host.strip()
        if not host: continue
        print(f"  Checking {host}...", file=sys.stderr)
        r = check_linux(host, args.user)
        print(format_report(r, args.format))
        all_reports.append(r)
        if len(hosts) > 1:
            print()

    if args.format == "json" and len(hosts) > 1:
        print(json.dumps(all_reports, ensure_ascii=False, indent=2))
