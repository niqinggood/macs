#!/usr/bin/env python3
"""Parameterised OpenKitty deployment over the system SSH client.

Authentication is intentionally delegated to SSH config, ssh-agent, or an
identity file supplied at invocation time. This script never accepts or stores
passwords.
"""

from __future__ import annotations

import argparse
import shlex
import subprocess
import sys
import time
from pathlib import Path


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--host", required=True, help="SSH host or config alias")
    parser.add_argument("--user", help="SSH user; omit when defined in SSH config")
    parser.add_argument("--port", type=int, default=22)
    parser.add_argument("--identity-file", type=Path)
    parser.add_argument("--local-binary", required=True, type=Path)
    parser.add_argument("--remote-path", required=True)
    parser.add_argument("--service", help="systemd service to restart")
    parser.add_argument("--health-url", default="http://127.0.0.1:8080/readyz")
    parser.add_argument("--sudo", action="store_true", help="Use non-interactive sudo")
    parser.add_argument("--accept-new-host-key", action="store_true")
    parser.add_argument("--dry-run", action="store_true")
    return parser.parse_args()


class SSHRunner:
    def __init__(self, args: argparse.Namespace) -> None:
        self.args = args
        self.target = f"{args.user}@{args.host}" if args.user else args.host
        strict = "accept-new" if args.accept_new_host_key else "yes"
        common = [
            "-p",
            str(args.port),
            "-o",
            f"StrictHostKeyChecking={strict}",
            "-o",
            "BatchMode=yes",
            "-o",
            "ConnectTimeout=10",
        ]
        if args.identity_file:
            common.extend(["-i", str(args.identity_file.expanduser())])
        self.ssh_base = ["ssh", *common, self.target]
        self.scp_base = ["scp", *self._scp_args(common)]

    @staticmethod
    def _scp_args(ssh_args: list[str]) -> list[str]:
        result: list[str] = []
        iterator = iter(ssh_args)
        for item in iterator:
            value = next(iterator)
            result.extend(["-P" if item == "-p" else item, value])
        return result

    def run(self, command: str, *, check: bool = True) -> subprocess.CompletedProcess[str]:
        argv = [*self.ssh_base, command]
        if self.args.dry_run:
            print("DRY-RUN:", shlex.join(argv))
            return subprocess.CompletedProcess(argv, 0, "", "")
        return subprocess.run(argv, text=True, capture_output=True, check=check)

    def upload(self, local: Path, remote: str) -> None:
        argv = [*self.scp_base, str(local), f"{self.target}:{remote}"]
        if self.args.dry_run:
            print("DRY-RUN:", shlex.join(argv))
            return
        subprocess.run(argv, check=True)


def validate(args: argparse.Namespace) -> None:
    binary = args.local_binary.expanduser().resolve()
    if not binary.is_file():
        raise ValueError(f"local binary does not exist: {binary}")
    args.local_binary = binary
    if not args.remote_path.startswith("/") or args.remote_path == "/":
        raise ValueError("--remote-path must be a specific absolute path")
    if not 1 <= args.port <= 65535:
        raise ValueError("--port must be between 1 and 65535")


def main() -> int:
    args = parse_args()
    ssh = None
    temporary = ""
    backup = ""
    remote = ""
    privileged = ""
    deployed = False
    try:
        validate(args)
        ssh = SSHRunner(args)
        timestamp = time.strftime("%Y%m%d-%H%M%S")
        remote = args.remote_path
        temporary = f"{remote}.new.{timestamp}"
        backup = f"{remote}.bak.{timestamp}"
        q_remote = shlex.quote(remote)
        q_temporary = shlex.quote(temporary)
        q_backup = shlex.quote(backup)
        privileged = "sudo -n " if args.sudo else ""

        print("[1/6] Checking SSH connection")
        ssh.run("true")

        print("[2/6] Checking target directory")
        ssh.run(f"test -d {shlex.quote(str(Path(remote).parent))} && df -Pk {q_remote} 2>/dev/null || true")

        print("[3/6] Backing up current binary")
        ssh.run(f"if test -e {q_remote}; then {privileged}cp -a {q_remote} {q_backup}; fi")

        print("[4/6] Uploading candidate")
        ssh.upload(args.local_binary, temporary)
        ssh.run(f"{privileged}chmod 0755 {q_temporary} && {privileged}mv {q_temporary} {q_remote}")
        deployed = not args.dry_run

        print("[5/6] Restarting and verifying")
        if args.service:
            ssh.run(f"{privileged}systemctl restart {shlex.quote(args.service)}")
        if args.health_url:
            ssh.run(
                "curl --fail --silent --show-error --max-time 15 "
                + shlex.quote(args.health_url)
                + " >/dev/null"
            )

        print("[6/6] Deployment complete")
        if args.dry_run:
            print("Dry run only; no remote state was changed.")
        else:
            print(f"Rollback binary retained at: {backup}")
        return 0
    except (ValueError, OSError, subprocess.CalledProcessError) as exc:
        print(f"deployment failed: {exc}", file=sys.stderr)
        if ssh is not None and deployed:
            print("attempting automatic rollback", file=sys.stderr)
            failed = f"{remote}.failed.{time.strftime('%Y%m%d-%H%M%S')}"
            rollback_command = (
                f"if test -e {shlex.quote(backup)}; then "
                f"{privileged}mv {shlex.quote(remote)} {shlex.quote(failed)} && "
                f"{privileged}mv {shlex.quote(backup)} {shlex.quote(remote)}; "
                f"else {privileged}mv {shlex.quote(remote)} {shlex.quote(failed)}; fi"
            )
            try:
                rollback = ssh.run(rollback_command, check=False)
                if rollback.returncode == 0 and args.service:
                    ssh.run(f"{privileged}systemctl restart {shlex.quote(args.service)}", check=False)
            except OSError:
                rollback = None
            if rollback is None or rollback.returncode != 0:
                print("automatic rollback failed; manual recovery is required", file=sys.stderr)
        return 1
    finally:
        if ssh is not None and temporary and not args.dry_run:
            try:
                ssh.run(f"rm -f -- {shlex.quote(temporary)}", check=False)
            except OSError:
                pass


if __name__ == "__main__":
    raise SystemExit(main())
