#!/usr/bin/env python3
"""Keep a private, verifiable copy of the actual working-tree release sources."""

from __future__ import annotations

import argparse
import hashlib
import json
from pathlib import Path, PurePosixPath
import shutil
import stat
import subprocess
import sys
from datetime import datetime, timezone
import zipfile


PRIVATE_ROOTS = {".git", ".local", ".openkitty", "author", "storage", "tenants", "runs", "tmp", "output"}
PRIVATE_NAMES = {"openkitty-config.yaml", "config.json", ".env"}
RUNTIME_SUFFIXES = {".db", ".sqlite", ".sqlite3", ".lic", ".log", ".exe", ".syso", ".pyc"}
ROOT_ARTIFACT_SUFFIXES = {".zip", ".rar", ".7z", ".pdf", ".docx", ".pptx", ".xlsx"}


def git(root: Path, *args: str) -> bytes:
    return subprocess.check_output(["git", "-C", str(root), *args], timeout=60)


def digest(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


def checked_file(root: Path, relative: str) -> Path:
    parts = PurePosixPath(relative).parts
    if not parts or ".." in parts or PurePosixPath(relative).is_absolute() or "\\" in relative or ":" in relative:
        raise ValueError("archive paths must be relative repository paths")
    path = root.joinpath(*parts)
    for candidate in [path, *path.parents]:
        if candidate == root:
            break
        attributes = getattr(candidate.lstat(), "st_file_attributes", 0)
        if candidate.is_symlink() or attributes & getattr(stat, "FILE_ATTRIBUTE_REPARSE_POINT", 1024):
            raise ValueError(f"linked archive input requires separate review: {relative}")
    if not path.resolve().is_relative_to(root.resolve()):
        raise ValueError("archive input escaped the repository")
    return path


def source_paths(root: Path) -> list[str]:
    names = git(root, "ls-files", "-z", "--cached", "--others", "--exclude-standard").decode("utf-8").split("\0")
    ignored = set(git(root, "ls-files", "-z", "-ci", "--exclude-standard").decode("utf-8").split("\0"))
    selected = []
    for name in sorted(set(names) - ignored):
        parts = PurePosixPath(name).parts
        if not parts or parts[0] in PRIVATE_ROOTS or parts[-1] in PRIVATE_NAMES:
            continue
        if any(p in {"node_modules", "__pycache__"} for p in parts):
            continue
        suffix = PurePosixPath(name).suffix.lower()
        if suffix in RUNTIME_SUFFIXES or parts[-1].startswith(".env.") and "example" not in parts[-1]:
            continue
        if len(parts) == 1 and suffix in ROOT_ARTIFACT_SUFFIXES:
            continue
        # Tracked deletions are not part of the working-tree snapshot.
        if not (root / name).exists() and not (root / name).is_symlink():
            continue
        path = checked_file(root, name)
        if path.is_file():
            selected.append(name)
    return selected


def create_archive(root: Path, output: Path, version: str, binary: Path | None = None) -> dict:
    root = root.resolve()
    output = output.absolute()
    if not output.resolve().is_relative_to(root / ".local"):
        raise ValueError("private copyright archives must be written below repository .local/")
    for parent in [output, *output.parents]:
        if parent.exists() and (parent.is_symlink() or getattr(parent.stat(), "st_file_attributes", 0) & 1024):
            raise ValueError("archive destination must not pass through a linked directory")
        if parent == root:
            break
    if output.exists():
        raise ValueError("archive output must be a new directory; preserve earlier versions")
    paths = source_paths(root)
    if not paths:
        raise ValueError("no source files found")
    output.mkdir(parents=True)
    rows = []
    with zipfile.ZipFile(output / "sources.zip", "w", compression=zipfile.ZIP_DEFLATED, compresslevel=6) as archive:
        for name in paths:
            data = checked_file(root, name).read_bytes()
            archive.writestr(name, data)
            rows.append({"path": name, "bytes": len(data), "sha256": digest(data)})
    # Detect edits during collection, including changes in the selected file set.
    if paths != source_paths(root) or any(digest(checked_file(root, r["path"]).read_bytes()) != r["sha256"] for r in rows):
        raise ValueError("sources changed during collection; keep this incomplete directory and retry in a new one")
    manifest = {
        "format_version": 1,
        "software_name": "macs",
        "version": version,
        "created_at": datetime.now(timezone.utc).isoformat(),
        "base_commit": git(root, "rev-parse", "HEAD").decode().strip(),
        "working_tree_has_changes": bool(git(root, "status", "--porcelain")),
        "archive": "sources.zip",
        "archive_sha256": digest((output / "sources.zip").read_bytes()),
        "file_count": len(rows),
        "files": rows,
        "exclusions": "Git-ignored files, private/runtime paths and root generated artifacts; embedded assets are retained",
        "rights_statement": "Technical snapshot only; authorship and third-party rights require supporting records",
    }
    if binary is not None:
        binary = binary.resolve(strict=True)
        shutil.copyfile(binary, output / "macs-binary.exe")
        manifest["binary"] = {"file": "macs-binary.exe", "sha256": digest((output / "macs-binary.exe").read_bytes())}
        build_info = subprocess.check_output(["go", "version", "-m", str(output / "macs-binary.exe")], timeout=60)
        (output / "binary-build.txt").write_bytes(build_info)
        manifest["binary"]["build_info_file"] = "binary-build.txt"
        manifest["binary"]["build_info_sha256"] = digest(build_info)
    (output / "manifest.json").write_text(json.dumps(manifest, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    return {"software_name": "macs", "file_count": len(rows), "archive_sha256": manifest["archive_sha256"]}


def verify_archive(directory: Path, root: Path | None = None) -> dict:
    manifest = json.loads((directory / "manifest.json").read_text(encoding="utf-8"))
    if manifest.get("archive") != "sources.zip":
        raise ValueError("unexpected archive name")
    archive_path = directory / "sources.zip"
    if digest(archive_path.read_bytes()) != manifest["archive_sha256"]:
        raise ValueError("source archive digest does not match")
    rows = manifest["files"]
    names = [r["path"] for r in rows]
    if len(names) != len(set(names)) or len(names) != manifest["file_count"]:
        raise ValueError("invalid source manifest file count or duplicate paths")
    for name in names:
        if ".." in PurePosixPath(name).parts or PurePosixPath(name).is_absolute() or "\\" in name or ":" in name:
            raise ValueError("unsafe path in source manifest")
    with zipfile.ZipFile(archive_path) as archive:
        if sorted(archive.namelist()) != sorted(names):
            raise ValueError("source archive entries do not match the manifest")
        for row in rows:
            data = archive.read(row["path"])
            if len(data) != row["bytes"] or digest(data) != row["sha256"]:
                raise ValueError(f"source content differs: {row['path']}")
    if "binary" in manifest:
        if manifest["binary"]["file"] != "macs-binary.exe" or digest((directory / "macs-binary.exe").read_bytes()) != manifest["binary"]["sha256"]:
            raise ValueError("binary digest does not match")
        if manifest["binary"].get("build_info_file") != "binary-build.txt" or digest((directory / "binary-build.txt").read_bytes()) != manifest["binary"]["build_info_sha256"]:
            raise ValueError("binary build information does not match")
    result = {"archive_verified": True, "file_count": len(rows)}
    if root is not None:
        current = set(source_paths(root))
        result["added"] = sorted(current - set(names))
        result["removed"] = sorted(set(names) - current)
        result["changed"] = [r["path"] for r in rows if r["path"] in current and digest(checked_file(root, r["path"]).read_bytes()) != r["sha256"]]
        result["worktree_matches"] = not any(result[k] for k in ("added", "removed", "changed"))
    return result


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    sub = parser.add_subparsers(dest="command", required=True)
    create = sub.add_parser("create")
    create.add_argument("--output", type=Path, required=True)
    create.add_argument("--version", required=True)
    create.add_argument("--binary", type=Path)
    verify = sub.add_parser("verify")
    verify.add_argument("--directory", type=Path, required=True)
    verify.add_argument("--worktree", action="store_true")
    args = parser.parse_args()
    root = Path(__file__).resolve().parents[2]
    try:
        if args.command == "create":
            result = create_archive(root, args.output, args.version, args.binary)
        else:
            result = verify_archive(args.directory, root if args.worktree else None)
        print(json.dumps(result, ensure_ascii=True, indent=2))
        return 1 if result.get("worktree_matches") is False else 0
    except (ValueError, OSError, subprocess.SubprocessError, zipfile.BadZipFile, KeyError) as exc:
        print(f"copyright archive: {exc}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
