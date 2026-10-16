import hashlib
import json
import pathlib
import tempfile
import unittest
from unittest import mock

import run_common30 as harness


class Common30HarnessSafetyTest(unittest.TestCase):
    def test_gateway_health_requires_matching_workspace(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            workspace = pathlib.Path(directory).resolve()
            response = (200, {"project": {"workspace": str(workspace)}}, "{}")
            with mock.patch.object(harness, "http_request", return_value=response):
                harness.gateway_health("http://gateway", workspace)

            mismatch = (200, {"project": {"workspace": str(workspace / "other")}}, "{}")
            with mock.patch.object(harness, "http_request", return_value=mismatch):
                with self.assertRaisesRegex(RuntimeError, "workspace mismatch"):
                    harness.gateway_health("http://gateway", workspace)

    def test_skipped_special_is_nonzero_unless_explicitly_allowed(self) -> None:
        outcomes = [{"outcome": "passed"}, {"outcome": "skipped_special"}]
        self.assertEqual(harness.outcomes_exit_code(outcomes), 2)
        self.assertEqual(harness.outcomes_exit_code(outcomes, allow_skipped_special=True), 0)

    def test_timeout_cleanup_requests_task_cancellation_without_masking_failure(self) -> None:
        with mock.patch.object(
            harness,
            "http_request",
            return_value=(200, {"status": "stopped", "task_id": "task / 1"}, "{}"),
        ) as request:
            receipt = harness.cancel_task_after_timeout("http://gateway", "task / 1")
        self.assertTrue(receipt["succeeded"], receipt)
        request.assert_called_once_with(
            "http://gateway", "POST", "/api/task/stop/task%20%2F%201", timeout=20
        )

        with mock.patch.object(harness, "http_request", side_effect=TimeoutError("offline")):
            receipt = harness.cancel_task_after_timeout("http://gateway", "task-2")
        self.assertFalse(receipt["succeeded"], receipt)
        self.assertIn("offline", receipt["error"])

    def test_poll_timeout_always_runs_cleanup_and_keeps_timeout_outcome(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            cleanup = {"requested": True, "succeeded": True, "http_status": 200}
            with mock.patch.object(harness.time, "monotonic", side_effect=[0.0, 11.0]), mock.patch.object(
                harness, "cancel_task_after_timeout", return_value=cleanup
            ) as cancel:
                result = harness.poll_task(
                    "http://gateway", "runner-task", 0, pathlib.Path(directory)
                )
            cancel.assert_called_once_with("http://gateway", "runner-task")
            self.assertEqual(result["status"], "timeout")
            self.assertEqual(result["cancellation"], cleanup)
            history = json.loads(
                (pathlib.Path(directory) / "status_history.json").read_text(encoding="utf-8")
            )
            self.assertEqual(history[-1]["runner_timeout_cleanup"], cleanup)

    def test_delivery_receipt_oracle_recomputes_hashes(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            workspace = pathlib.Path(directory)
            delivered = workspace / "runs" / "A30" / "delivered"
            delivered.mkdir(parents=True)
            (delivered / "README.md").write_text("ready\n", encoding="utf-8")
            (delivered / "summary.json").write_text('{"ok":true}\n', encoding="utf-8")
            hashes = {
                name: hashlib.sha256((delivered / name).read_bytes()).hexdigest()
                for name in ("README.md", "summary.json")
            }
            (delivered / "checksums.txt").write_text(
                "\n".join(f"{digest}  {name}" for name, digest in hashes.items()) + "\n",
                encoding="utf-8",
            )
            hashes["checksums.txt"] = hashlib.sha256((delivered / "checksums.txt").read_bytes()).hexdigest()
            receipt_path = workspace / "runs" / "A30" / "delivery_receipt.json"
            receipt_path.write_text(
                json.dumps(
                    {
                        "status": "delivered",
                        "delivered_at": "2026-08-05T00:00:00Z",
                        "files": list(hashes),
                        "sha256": hashes,
                        "verification": {"status": "verified"},
                    }
                ),
                encoding="utf-8",
            )
            check = {
                "type": "delivery_receipt_sha256",
                "path": "runs/A30/delivery_receipt.json",
                "delivered_dir": "runs/A30/delivered",
                "files": list(hashes),
                "checksum_manifest": "checksums.txt",
                "manifest_files": ["README.md", "summary.json"],
            }
            result = harness.evaluate_check(check, workspace, {}, "", "", "", {})
            self.assertTrue(result["passed"], result["detail"])

            receipt = json.loads(receipt_path.read_text(encoding="utf-8"))
            receipt["sha256"]["README.md"] = "0" * 64
            receipt_path.write_text(json.dumps(receipt), encoding="utf-8")
            result = harness.evaluate_check(check, workspace, {}, "", "", "", {})
            self.assertFalse(result["passed"])
            self.assertIn("hash mismatch", result["detail"])

    def test_file_regex_min_count_accepts_extra_evidence(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            workspace = pathlib.Path(directory)
            evidence = workspace / "evidence.md"
            evidence.write_text("source\nsource\nsource\n", encoding="utf-8")
            check = {
                "type": "file_regex_min_count",
                "path": "evidence.md",
                "pattern": "^source$",
                "min_count": 2,
            }
            result = harness.evaluate_check(check, workspace, {}, "", "", "", {})
            self.assertTrue(result["passed"], result["detail"])

            check["min_count"] = 4
            result = harness.evaluate_check(check, workspace, {}, "", "", "", {})
            self.assertFalse(result["passed"])

    def test_delivery_receipt_accepts_manifest_without_checksum_manifest(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            workspace = pathlib.Path(directory)
            package = workspace / "package"
            package.mkdir()
            (package / "overview.md").write_text("ready\n", encoding="utf-8")
            (package / "metrics.json").write_text('{"records":9}\n', encoding="utf-8")
            hashes = {
                name: hashlib.sha256((package / name).read_bytes()).hexdigest()
                for name in ("overview.md", "metrics.json")
            }
            (package / "manifest.json").write_text(
                json.dumps(
                    {
                        "files": list(hashes),
                        "sha256": hashes,
                        "verified_at": "2026-08-13T00:00:00Z",
                        "status": "verified",
                        "verification": {"status": "verified"},
                    }
                ),
                encoding="utf-8",
            )
            check = {
                "type": "delivery_receipt_sha256",
                "path": "package/manifest.json",
                "delivered_dir": "package",
                "files": list(hashes),
            }
            result = harness.evaluate_check(check, workspace, {}, "", "", "", {})
            self.assertTrue(result["passed"], result["detail"])


if __name__ == "__main__":
    unittest.main()
