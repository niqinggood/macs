"""Exercise Windows batch entry points without compiling the product or installing npm.

Run on Windows: python -m unittest discover -s scripts/build -p 'test_*.py'
The small fake tool executable is built locally with Go; no network is needed.
"""

import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest


ROOT = Path(__file__).resolve().parents[2]
WINDOWS = "abuild_windows.bat"
LINUX = "scripts/build/build_linux.bat"
MAC = "scripts/build/build_mac.bat"
DESKTOP = "electron-app/build_desktop.bat"
HELPER = r'''
package main
import ("encoding/json"; "fmt"; "os"; "path/filepath"; "strings")
func main() {
    name := strings.ToLower(filepath.Base(os.Args[0]))
    name = strings.TrimSuffix(name, ".exe") + ".exe"
    args := os.Args[1:]
    cwd, _ := os.Getwd()
    log, _ := os.OpenFile(os.Getenv("FAKE_LOG"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
    json.NewEncoder(log).Encode(map[string]any{"tool":name,"args":args,"cwd":cwd,"os":os.Getenv("GOOS"),"arch":os.Getenv("GOARCH")})
    log.Close()
    fail := func(key string) { if os.Getenv(key) == "1" { os.Exit(23) } }
    switch name {
    case "go.exe":
        switch args[0] {
        case "env":
            if args[1] == "GOBIN" && os.Getenv("FAKE_EMPTY_GOBIN") != "1" { fmt.Println(os.Getenv("FAKE_BIN")) }
        case "run": fail("FAKE_GUARD_FAIL")
        case "build":
            if os.Getenv("FAKE_FAIL_TARGET") == os.Getenv("GOOS")+"/"+os.Getenv("GOARCH") { os.Exit(23) }
            fail("FAKE_BUILD_FAIL")
            for i, arg := range args { if arg == "-o" { os.WriteFile(args[i+1], []byte("new binary"), 0600) } }
        }
    case "go-winres.exe": fail("FAKE_WINRES_FAIL")
    case "upx.exe":
        if args[0] == "-t" { fail("FAKE_UPX_TEST_FAIL") } else { fail("FAKE_UPX_COMPRESS_FAIL") }
    case "rar.exe":
        fail("FAKE_RAR_FAIL")
        if args[0] == "a" { for _, arg := range args { if strings.HasSuffix(arg,".build.rar") { os.WriteFile(arg, []byte("new archive"), 0600) } } }
    case "node.exe":
        if args[0] == "install" || args[0] == "ci" { fail("FAKE_NPM_INSTALL_FAIL") } else { fail("FAKE_NPM_BUILD_FAIL") }
    }
}
'''


@unittest.skipUnless(os.name == "nt", "Windows cmd.exe is required")
class BuildScriptsTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.sandbox = tempfile.TemporaryDirectory(prefix="macs-build-tests ")
        cls.base = Path(cls.sandbox.name)
        source = cls.base / "tool.go"
        source.write_text(HELPER, encoding="utf-8")
        cls.helper = cls.base / "tool.exe"
        env = dict(os.environ, GOOS="windows", GOARCH="amd64", CGO_ENABLED="0")
        subprocess.run(["go", "build", "-o", str(cls.helper), str(source)],
                       env=env, check=True, capture_output=True, timeout=120)

    @classmethod
    def tearDownClass(cls):
        cls.sandbox.cleanup()

    def setUp(self):
        self.case = Path(tempfile.mkdtemp(dir=self.base, prefix="case "))
        self.project = self.case / "project with spaces \u6d4b\u8bd5"
        self.project.mkdir()
        self.bin = self.case / "tools with spaces"
        self.bin.mkdir()
        for name in (WINDOWS, LINUX, MAC, DESKTOP, "scripts/build/build_windows.bat"):
            dest = self.project / name
            dest.parent.mkdir(parents=True, exist_ok=True)
            shutil.copyfile(ROOT / name, dest)
        (self.project / "winres").mkdir()
        (self.project / "winres/kitty.png").write_bytes(b"icon")
        (self.project / "scripts/quality_gate.ps1").write_text(
            'if ($env:FAKE_GATE_FAIL -eq "1") { exit 23 }; exit 0', encoding="utf-8")
        for name in ("go.exe", "go-winres.exe", "upx.exe", "Rar.exe", "node.exe"):
            shutil.copyfile(self.helper, self.bin / name)
        (self.bin / "npm.cmd").write_text('@echo off\n"%~dp0node.exe" %*\n', encoding="ascii")
        self.log = self.case / "calls.jsonl"
        self.env = {k: v for k, v in os.environ.items() if not k.startswith(("MACS_", "FAKE_"))}
        system = Path(os.environ["SystemRoot"]) / "System32"
        self.env.update(PATH=os.pathsep.join([str(self.bin), str(system), str(system / "WindowsPowerShell/v1.0")]),
                        FAKE_LOG=str(self.log), FAKE_BIN=str(self.bin), MACS_SKIP_RAR="1")

    def run_script(self, script, *args, **env):
        # Quote every fixture argument: CMD otherwise treats ';' and '=' as
        # argument separators before the script can validate the value.
        values = [str(self.project / script), *args]
        assert all('"' not in value and "\n" not in value for value in values)
        command = "call " + " ".join('"' + value + '"' for value in values)
        # cmd.exe parses quotes itself; list2cmdline on the entire /c argument
        # would introduce literal backslash-quotes into the batch filename.
        result = subprocess.run("cmd.exe /d /s /c " + command, cwd=self.case,
                                env=dict(self.env, **env), capture_output=True, timeout=45)
        self.output = result.stdout.decode(errors="replace") + result.stderr.decode(errors="replace")
        return result.returncode

    def calls(self, tool=None):
        calls = [json.loads(line) for line in self.log.read_text(encoding="utf-8").splitlines()] if self.log.exists() else []
        return [call for call in calls if tool is None or call["tool"] == tool]

    def test_help_needs_no_build_tools(self):
        for script in (WINDOWS, LINUX, MAC, DESKTOP):
            with self.subTest(script=script):
                self.assertEqual(self.run_script(script, "--help"), 0, self.output)
                self.assertIn("Usage:", self.output)
        self.assertEqual(self.calls(), [])

    def test_rejects_whole_invalid_version(self):
        for script in (WINDOWS, LINUX, MAC, DESKTOP):
            for version in ("2.0.0 extra", "../2.0.0", ";bad"):
                with self.subTest(script=script, version=version):
                    self.assertNotEqual(self.run_script(script, version), 0, self.output)
                    self.assertIn("ERROR:", self.output)
        self.assertEqual(self.calls(), [])

    def test_windows_upx_from_path_and_exact_version(self):
        self.assertEqual(self.run_script(WINDOWS, "2.0.0", "require", FAKE_EMPTY_GOBIN="1"), 0, self.output)
        self.assertEqual([c["args"][0] for c in self.calls("upx.exe")], ["--best", "-t"])
        self.assertTrue((self.project / "openKitty-2.0.0-win.exe").is_file())
        build = next(c for c in self.calls("go.exe") if c["args"][0] == "build")
        self.assertIn("main.CurrentVersion=2.0.0", " ".join(build["args"]))
        self.assertEqual((build["os"], build["arch"]), ("windows", "amd64"))

    def test_windows_missing_required_upx_keeps_old_exe(self):
        (self.bin / "upx.exe").unlink()
        old = self.project / "openKitty.exe"
        old.write_bytes(b"last good build")
        self.assertNotEqual(self.run_script(WINDOWS, "2.0.0", "require"), 0, self.output)
        self.assertEqual(old.read_bytes(), b"last good build")
        self.assertFalse(any(c["args"][0] == "build" for c in self.calls("go.exe")))

    def test_windows_compression_failures_preserve_old_exe(self):
        old = self.project / "openKitty.exe"
        old.write_bytes(b"last good build")
        for failure in ("FAKE_UPX_COMPRESS_FAIL", "FAKE_UPX_TEST_FAIL", "FAKE_BUILD_FAIL", "FAKE_WINRES_FAIL"):
            with self.subTest(failure=failure):
                self.assertEqual(self.run_script(WINDOWS, "2.0.0", "require", **{failure: "1"}), 23, self.output)
                self.assertEqual(old.read_bytes(), b"last good build")
                self.assertFalse((self.project / "openKitty.build.exe").exists())

    def test_windows_auto_without_upx_and_off(self):
        (self.bin / "upx.exe").unlink()
        self.assertEqual(self.run_script(WINDOWS, "2.0.0", "auto"), 0, self.output)
        self.assertIn("uncompressed", self.output)
        self.assertEqual(self.run_script(WINDOWS, "2.0.0", "off", MACS_UPX="missing.exe"), 0, self.output)

    def test_guard_failure_stops_all_builds(self):
        for script, args in ((WINDOWS, ("2.0.0",)), (LINUX, ("2.0.0",)), (MAC, ("2.0.0", "fast"))):
            with self.subTest(script=script):
                self.assertEqual(self.run_script(script, *args, FAKE_GUARD_FAIL="1"), 23, self.output)
        self.assertFalse(any(c["tool"] == "go.exe" and c["args"][0] == "build" for c in self.calls()))

    def test_windows_copy_failure_is_not_success(self):
        # A directory at the target filename makes MOVE fail on Windows.
        (self.project / "openKitty.exe").mkdir()
        self.assertNotEqual(self.run_script(WINDOWS, "2.0.0", "off"), 0, self.output)
        self.assertNotIn("Build complete:", self.output)

    def test_rar_failure_is_not_success(self):
        self.assertEqual(self.run_script(WINDOWS, "2.0.0", "off", MACS_SKIP_RAR="0", FAKE_RAR_FAIL="1"), 23, self.output)
        self.assertNotIn("Build complete:", self.output)

    def test_linux_accepts_both_orders_and_normalizes_architecture(self):
        for args in (("2.0.0", "ARM64"), ("AMD64", "2.0.0")):
            with self.subTest(args=args):
                self.assertEqual(self.run_script(LINUX, *args), 0, self.output)
        builds = [c for c in self.calls("go.exe") if c["args"][0] == "build"]
        self.assertEqual([(c["os"], c["arch"]) for c in builds], [("linux", "arm64"), ("linux", "amd64")])

    def test_linux_failure_preserves_old_output(self):
        old = self.project / "dist/openkitty_linux_amd64"
        old.parent.mkdir()
        old.write_bytes(b"old")
        self.assertEqual(self.run_script(LINUX, "2.0.0", "amd64", FAKE_BUILD_FAIL="1"), 23, self.output)
        self.assertEqual(old.read_bytes(), b"old")

    def test_mac_second_failure_preserves_both_old_outputs(self):
        for name in ("intel", "apple"):
            (self.project / ("openkitty-2.0.0-mac-" + name)).write_bytes(b"old")
        self.assertEqual(self.run_script(MAC, "2.0.0", "fast", FAKE_FAIL_TARGET="darwin/arm64"), 23, self.output)
        for name in ("intel", "apple"):
            self.assertEqual((self.project / ("openkitty-2.0.0-mac-" + name)).read_bytes(), b"old")
        self.assertEqual(list(self.project.glob("*.build")), [])

    def test_release_gate_failure_stops_mac_and_desktop(self):
        for script in (MAC, DESKTOP):
            with self.subTest(script=script):
                self.assertNotEqual(self.run_script(script, "2.0.0", "release", FAKE_GATE_FAIL="1"), 0, self.output)
        self.assertFalse(any(c["tool"] == "go.exe" and c["args"][0] == "build" for c in self.calls()))

    def test_desktop_npm_failures_and_version_override(self):
        self.assertEqual(self.run_script(DESKTOP, "2.0.0", "fast", FAKE_NPM_INSTALL_FAIL="1"), 23, self.output)
        self.assertEqual(self.run_script(DESKTOP, "2.0.0", "fast", FAKE_NPM_BUILD_FAIL="1"), 23, self.output)
        self.assertEqual(self.run_script(DESKTOP, "2.0.0", "fast"), 0, self.output)
        packaging = [c for c in self.calls("node.exe") if c["args"][0] == "run"][-1]
        self.assertEqual(Path(packaging["cwd"]), self.project / "electron-app")
        self.assertIn("--config.extraMetadata.version=2.0.0", packaging["args"])
        self.assertIn("portable", self.output)


if __name__ == "__main__":
    unittest.main()
