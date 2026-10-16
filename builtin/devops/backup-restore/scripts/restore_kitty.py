#!/usr/bin/env python3
"""Restore a verified OpenKitty archive through SSH with rollback."""

from __future__ import annotations

import argparse
import hashlib
import json
import shlex
import subprocess
import sys
import tarfile
import time
from pathlib import Path, PurePosixPath


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("archive", type=Path)
    parser.add_argument("--host", required=True)
    parser.add_argument("--user")
    parser.add_argument("--port", type=int, default=22)
    parser.add_argument("--identity-file", type=Path)
    parser.add_argument("--target-root", required=True)
    parser.add_argument("--service")
    parser.add_argument("--health-url", default="http://127.0.0.1:8080/readyz")
    parser.add_argument("--sudo", action="store_true")
    parser.add_argument("--accept-new-host-key", action="store_true")
    parser.add_argument("--execute", action="store_true", help="Required to change remote state")
    return parser.parse_args()


def sha256(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as stream:
        for chunk in iter(lambda: stream.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def validate_archive(path: Path) -> None:
    manifest_path = path.with_suffix(path.suffix + ".json")
    if manifest_path.is_file():
        manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
        if manifest.get("sha256") != sha256(path):
            raise ValueError("archive checksum does not match manifest")
    with tarfile.open(path, "r:gz") as archive:
        for member in archive.getmembers():
            name = PurePosixPath(member.name)
            if name.is_absolute() or ".." in name.parts:
                raise ValueError(f"unsafe archive member: {member.name!r}")
            if member.issym() or member.islnk():
                raise ValueError(f"links are not accepted in restore archives: {member.name!r}")


def connection_args(args: argparse.Namespace) -> tuple[list[str], list[str], str]:
    target = f"{args.user}@{args.host}" if args.user else args.host
    strict = "accept-new" if args.accept_new_host_key else "yes"
    ssh = ["ssh", "-p", str(args.port), "-o", f"StrictHostKeyChecking={strict}", "-o", "BatchMode=yes"]
    scp = ["scp", "-P", str(args.port), "-o", f"StrictHostKeyChecking={strict}", "-o", "BatchMode=yes"]
    if args.identity_file:
        identity = str(args.identity_file.expanduser())
        ssh.extend(["-i", identity])
        scp.extend(["-i", identity])
    return ssh, scp, target


def main() -> int:
    args = parse_args()
    ssh: list[str] = []
    target = ""
    remote_archive = ""
    staging = ""
    rollback = ""
    target_root = ""
    privileged = ""
    switched = False
    try:
        archive = args.archive.expanduser().resolve()
        if not archive.is_file():
            raise ValueError(f"archive not found: {archive}")
        if not args.target_root.startswith("/") or args.target_root == "/":
            raise ValueError("--target-root must be a specific absolute path")
        validate_archive(archive)
        ssh, scp, target = connection_args(args)
        timestamp = time.strftime("%Y%m%d-%H%M%S")
        remote_archive = f"/tmp/openkitty-restore-{timestamp}.tar.gz"
        target_root = args.target_root.rstrip("/")
        staging = f"{target_root}.restore.{timestamp}"
        rollback = f"{target_root}.rollback.{timestamp}"
        privileged = "sudo -n " if args.sudo else ""

        plan = {
            "target": target,
            "archive": str(archive),
            "target_root": target_root,
            "staging": staging,
            "rollback": rollback,
            "service": args.service,
        }
        print(json.dumps(plan, indent=2))
        if not args.execute:
            print("Plan only. Re-run with --execute to restore.")
            return 0

        subprocess.run([*ssh, target, "true"], check=True)
        subprocess.run([*scp, str(archive), f"{target}:{remote_archive}"], check=True)
        extract = (
            f"{privileged}mkdir -p {shlex.quote(staging)} && "
            f"{privileged}tar -xzf {shlex.quote(remote_archive)} -C {shlex.quote(staging)}"
        )
        subprocess.run([*ssh, target, extract], check=True)
        switch = (
            f"if test -e {shlex.quote(target_root)}; then {privileged}mv {shlex.quote(target_root)} {shlex.quote(rollback)}; fi && "
            f"{privileged}mv {shlex.quote(staging)} {shlex.quote(target_root)}"
        )
        subprocess.run([*ssh, target, switch], check=True)
        switched = True
        if args.service:
            subprocess.run([*ssh, target, f"{privileged}systemctl restart {shlex.quote(args.service)}"], check=True)
        if args.health_url:
            subprocess.run(
                [*ssh, target, "curl --fail --silent --show-error --max-time 15 " + shlex.quote(args.health_url) + " >/dev/null"],
                check=True,
            )
        subprocess.run([*ssh, target, f"rm -f -- {shlex.quote(remote_archive)}"], check=False)
        print(f"restore complete; rollback retained at {rollback}")
        return 0
    except (ValueError, OSError, json.JSONDecodeError, tarfile.TarError, subprocess.CalledProcessError) as exc:
        print(f"restore failed: {exc}", file=sys.stderr)
        if ssh and target and rollback and target_root:
            failed = f"{target_root}.failed.{time.strftime('%Y%m%d-%H%M%S')}"
            rollback_command = (
                f"if test -e {shlex.quote(rollback)}; then "
                f"if test -e {shlex.quote(target_root)}; then {privileged}mv {shlex.quote(target_root)} {shlex.quote(failed)}; fi; "
                f"{privileged}mv {shlex.quote(rollback)} {shlex.quote(target_root)}; fi"
            )
            try:
                rollback_result = subprocess.run([*ssh, target, rollback_command], check=False)
                if rollback_result.returncode == 0 and switched and args.service:
                    subprocess.run(
                        [*ssh, target, f"{privileged}systemctl restart {shlex.quote(args.service)}"],
                        check=False,
                    )
            except OSError:
                pass
        return 1
    finally:
        if ssh and target and remote_archive:
            cleanup = f"rm -f -- {shlex.quote(remote_archive)}"
            if staging:
                cleanup += f"; {privileged}rm -rf -- {shlex.quote(staging)}"
            try:
                subprocess.run([*ssh, target, cleanup], check=False)
            except OSError:
                pass


if __name__ == "__main__":
    raise SystemExit(main())
