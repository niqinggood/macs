"""Integration checks for publication boundaries; all repositories are local."""

from __future__ import annotations

import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest

REPO = Path(__file__).resolve().parents[2]
POWERSHELL = shutil.which("pwsh") or shutil.which("powershell")


@unittest.skipUnless(POWERSHELL and shutil.which("git"), "PowerShell and git required")
class PublicSnapshotTests(unittest.TestCase):
    def setUp(self) -> None:
        checks = REPO / ".local" / "checks"
        checks.mkdir(parents=True, exist_ok=True)
        self.temp = tempfile.TemporaryDirectory(prefix="public-snapshot-", dir=checks)
        self.addCleanup(self.temp.cleanup)
        self.base = Path(self.temp.name).resolve()
        if self.base.parent != checks.resolve():
            raise RuntimeError("Test cleanup must remain inside .local/checks")
        self.source = self.base / "source"
        scripts = self.source / "scripts" / "github"
        scripts.mkdir(parents=True)
        self.script = scripts / "new-public-snapshot.ps1"
        shutil.copyfile(REPO / "scripts" / "github" / self.script.name, self.script)
        self.git("init", "-b", "main")
        self.write("README.md", "original\n")
        self.write("config.json", '{"fixture":"local-only"}\n')
        self.git("add", "README.md", "config.json")
        self.git("-c", "user.name=Snapshot Test", "-c", "user.email=test@example.invalid",
                 "commit", "-m", "fixture")
        self.write(".gitignore", "/config.json\n/.local/\n/.deploy/\n")
        self.git("add", ".gitignore", "scripts/github/new-public-snapshot.ps1")
        self.git("-c", "user.name=Snapshot Test", "-c", "user.email=test@example.invalid",
                 "commit", "-m", "publication rules")
        self.write("README.md", "reviewed working tree\n")
        self.write("new-source.txt", "explicitly reviewed\n")
        self.write("unreviewed.txt", "must not be copied by default\n")
        self.write(".deploy/private.txt", "local fixture\n")

    def write(self, relative: str, value: str) -> None:
        path = self.source / relative
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(value, encoding="utf-8")

    def git(self, *args: str, cwd: Path | None = None) -> str:
        result = subprocess.run(["git", "-C", str(cwd or self.source), *args],
                                capture_output=True, text=True, encoding="utf-8",
                                errors="replace", timeout=30, check=True)
        return result.stdout

    def run_snapshot(self, destination: Path, *args: str) -> subprocess.CompletedProcess:
        return subprocess.run([POWERSHELL, "-NoProfile", "-ExecutionPolicy", "Bypass",
                               "-File", str(self.script), "-Destination", str(destination),
                               *args], cwd=self.source, capture_output=True, text=True,
                              encoding="utf-8", errors="replace", timeout=60)

    def test_plan_is_readonly_and_omits_unreviewed_files(self) -> None:
        destination = self.base / "preview"
        before = self.git("status", "--porcelain")
        result = self.run_snapshot(destination, "-PlanOnly")
        self.assertEqual(result.returncode, 0, result.stderr)
        plan = json.loads(result.stdout)
        paths = {item["path"] for item in plan["files"]}
        self.assertIn("README.md", paths)
        self.assertNotIn("config.json", paths)
        self.assertNotIn("unreviewed.txt", paths)
        self.assertEqual(plan["excluded_ignored_tracked"], 1)
        self.assertFalse(destination.exists())
        self.assertEqual(before, self.git("status", "--porcelain"))

    def test_snapshot_preserves_changes_and_has_no_history_or_remote(self) -> None:
        destination = self.base / "preview"
        before = self.git("status", "--porcelain")
        result = self.run_snapshot(destination, "-IncludeUntracked", "new-source.txt")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual((destination / "README.md").read_text(), "reviewed working tree\n")
        self.assertTrue((destination / "new-source.txt").exists())
        self.assertFalse((destination / "config.json").exists())
        self.assertFalse((destination / "unreviewed.txt").exists())
        self.assertFalse((destination / ".deploy").exists())
        self.assertEqual(self.git("remote", cwd=destination), "")
        self.assertEqual(self.git("rev-list", "--all", cwd=destination), "")
        self.assertEqual(before, self.git("status", "--porcelain"))

    def test_ignored_or_escaping_explicit_file_is_rejected(self) -> None:
        for path in (".deploy/private.txt", "../outside.txt"):
            with self.subTest(path=path):
                destination = self.base / "rejected"
                result = self.run_snapshot(destination, "-IncludeUntracked", path)
                self.assertNotEqual(result.returncode, 0)
                self.assertFalse(destination.exists())

    def test_source_and_nonempty_destinations_are_rejected(self) -> None:
        destination = self.base / "existing"
        destination.mkdir()
        marker = destination / "keep.txt"
        marker.write_text("keep")
        for target in (self.source, destination, self.source / "unignored-preview"):
            with self.subTest(target=target):
                result = self.run_snapshot(target, "-PlanOnly")
                self.assertNotEqual(result.returncode, 0)
        self.assertEqual(marker.read_text(), "keep")

    def test_linked_sources_are_rejected_when_supported(self) -> None:
        target = self.base / "outside.txt"
        target.write_text("outside")
        linked = self.source / "linked.txt"
        try:
            os.symlink(target, linked)
        except (OSError, NotImplementedError):
            self.skipTest("Creating symlinks is unavailable")
        result = self.run_snapshot(self.base / "preview", "-IncludeUntracked", "linked.txt")
        self.assertNotEqual(result.returncode, 0)


if __name__ == "__main__":
    unittest.main()
