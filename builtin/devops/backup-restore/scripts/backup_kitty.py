#!/usr/bin/env python3
"""Create and download a parameterised OpenKitty filesystem backup."""

from __future__ import annotations

import argparse
import hashlib
import json
import shlex
import subprocess
import sys
import time
from pathlib import Path, PurePosixPath


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--host", required=True, help="SSH host or config alias")
    parser.add_argument("--user")
    parser.add_argument("--port", type=int, default=22)
    parser.add_argument("--identity-file", type=Path)
    parser.add_argument("--source-root", required=True, help="Remote OpenKitty root")
    parser.add_argument("--include", action="append", required=True, help="Relative path; repeat as needed")
    parser.add_argument("--output-dir", type=Path, default=Path("storage/backups"))
    parser.add_argument("--accept-new-host-key", action="store_true")
    parser.add_argument("--dry-run", action="store_true")
    return parser.parse_args()


def validate_relative(value: str) -> str:
    path = PurePosixPath(value)
    if path.is_absolute() or ".." in path.parts or value in {"", "."}:
        raise ValueError(f"unsafe relative include path: {value!r}")
    return value


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


def run(argv: list[str], dry_run: bool) -> subprocess.CompletedProcess[str]:
    if dry_run:
        print("DRY-RUN:", shlex.join(argv))
        return subprocess.CompletedProcess(argv, 0, "", "")
    return subprocess.run(argv, text=True, capture_output=True, check=True)


def sha256(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as stream:
        for chunk in iter(lambda: stream.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def main() -> int:
    args = parse_args()
    try:
        if not args.source_root.startswith("/") or args.source_root == "/":
            raise ValueError("--source-root must be a specific absolute path")
        includes = [validate_relative(item) for item in args.include]
        ssh, scp, target = connection_args(args)
        timestamp = time.strftime("%Y%m%d-%H%M%S")
        archive_name = f"openkitty-backup-{timestamp}.tar.gz"
        remote_archive = f"/tmp/{archive_name}"
        output_dir = args.output_dir.expanduser().resolve()
        local_archive = output_dir / archive_name

        create = "cd {root} && tar -czf {archive} -- {items}".format(
            root=shlex.quote(args.source_root),
            archive=shlex.quote(remote_archive),
            items=" ".join(shlex.quote(item) for item in includes),
        )
        run([*ssh, target, create], args.dry_run)
        if args.dry_run:
            run([*scp, f"{target}:{remote_archive}", str(local_archive)], True)
            return 0

        output_dir.mkdir(parents=True, exist_ok=True)
        try:
            run([*scp, f"{target}:{remote_archive}", str(local_archive)], False)
        finally:
            subprocess.run([*ssh, target, f"rm -f -- {shlex.quote(remote_archive)}"], check=False)

        manifest = {
            "archive": local_archive.name,
            "created_at": timestamp,
            "source_root": args.source_root,
            "includes": includes,
            "size": local_archive.stat().st_size,
            "sha256": sha256(local_archive),
        }
        manifest_path = local_archive.with_suffix(local_archive.suffix + ".json")
        manifest_path.write_text(json.dumps(manifest, indent=2), encoding="utf-8")
        print(f"backup: {local_archive}")
        print(f"manifest: {manifest_path}")
        return 0
    except (ValueError, OSError, subprocess.CalledProcessError) as exc:
        print(f"backup failed: {exc}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
