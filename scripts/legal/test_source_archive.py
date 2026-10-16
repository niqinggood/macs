import importlib.util
import json
from pathlib import Path
import subprocess
import tempfile
import unittest


spec = importlib.util.spec_from_file_location("source_archive", Path(__file__).with_name("source_archive.py"))
archive = importlib.util.module_from_spec(spec)
spec.loader.exec_module(archive)


class SourceArchiveTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        subprocess.run(["git", "init", "-q", str(self.root)], check=True)
        (self.root / ".gitignore").write_text(".local/\nprivate.txt\n", encoding="utf-8")
        (self.root / "main.go").write_text("package main\n", encoding="utf-8")
        (self.root / "private.txt").write_text("private", encoding="utf-8")
        subprocess.run(["git", "-C", str(self.root), "add", ".gitignore", "main.go"], check=True)
        subprocess.run(["git", "-C", str(self.root), "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-qm", "fixture"], check=True)
        self.out = self.root / ".local" / "archive"

    def test_includes_actual_uncommitted_sources_and_embedded_assets(self):
        (self.root / "new.go").write_text("package added\n", encoding="utf-8")
        (self.root / "main.go").write_text("package changed\n", encoding="utf-8")
        (self.root / "builtin").mkdir()
        (self.root / "builtin" / "icon.png").write_bytes(b"image")
        archive.create_archive(self.root, self.out, "V1")
        self.assertTrue(archive.verify_archive(self.out, self.root)["worktree_matches"])
        manifest = json.loads((self.out / "manifest.json").read_text())
        names = {r["path"] for r in manifest["files"]}
        self.assertTrue({"new.go", "main.go", "builtin/icon.png"} <= names)
        self.assertNotIn("private.txt", names)

    def test_detects_worktree_change_without_invalidating_saved_archive(self):
        archive.create_archive(self.root, self.out, "V1")
        (self.root / "main.go").write_text("changed", encoding="utf-8")
        result = archive.verify_archive(self.out, self.root)
        self.assertTrue(result["archive_verified"])
        self.assertEqual(result["changed"], ["main.go"])
        self.assertFalse(result["worktree_matches"])

    def test_rejects_overwrite_and_nonprivate_destination(self):
        with self.assertRaises(ValueError):
            archive.create_archive(self.root, self.root / "public", "V1")
        archive.create_archive(self.root, self.out, "V1")
        with self.assertRaises(ValueError):
            archive.create_archive(self.root, self.out, "V1")

    def test_detects_archive_tampering(self):
        archive.create_archive(self.root, self.out, "V1")
        with (self.out / "sources.zip").open("ab") as stream:
            stream.write(b"tampered")
        with self.assertRaisesRegex(ValueError, "digest"):
            archive.verify_archive(self.out)

    def test_rejects_manifest_path_escape(self):
        archive.create_archive(self.root, self.out, "V1")
        path = self.out / "manifest.json"
        manifest = json.loads(path.read_text())
        manifest["files"][0]["path"] = "../escape.go"
        path.write_text(json.dumps(manifest))
        with self.assertRaisesRegex(ValueError, "unsafe path"):
            archive.verify_archive(self.out)


if __name__ == "__main__":
    unittest.main()
