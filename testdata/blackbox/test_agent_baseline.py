import dataclasses
import json
import pathlib
import subprocess
import sys
import tempfile
import unittest
from unittest import mock

import run_agent_baseline as baseline


def outcome(task_id, status="passed", duration=1.0):
    return {"id": task_id, "outcome": status, "checks": [{"passed": status == "passed"}], "duration_seconds": duration}


class BaselineMetricsTest(unittest.TestCase):
    tasks = [{"id": "A", "category": "code"}, {"id": "B", "category": "documents"}]

    def test_missing_skipped_and_failed_attempts_keep_planned_denominator(self):
        trials = [
            {"repetition": 1, "tasks": [outcome("A"), outcome("B", "failed", 4)]},
            {"repetition": 2, "tasks": [outcome("A", duration=2), outcome("B", "skipped_special", None)]},
        ]
        result = baseline.aggregate(self.tasks, 3, trials)
        overall = result["overall"]
        self.assertEqual(6, overall["planned_attempts"])
        self.assertEqual(2, overall["passed_attempts"])
        self.assertAlmostEqual(1 / 3, overall["attempt_pass_rate"])
        self.assertEqual(0.5, overall["first_attempt_pass_rate"])
        self.assertEqual(0, overall["all_repetitions_pass_rate"])
        self.assertEqual(2, overall["outcome_counts"]["missing"])
        self.assertEqual(1, overall["unstable_tasks"])
        self.assertEqual(2, overall["duration_p50_seconds"])
        self.assertEqual(4, overall["duration_p95_seconds"])
        self.assertIsNone(overall["tokens"])
        self.assertIsNone(overall["cost_usd"])
        self.assertEqual(3, result["categories"]["documents"]["planned_attempts"])

    def test_success_claim_without_checks_or_with_failed_check_is_not_a_pass(self):
        trials = [{"repetition": 1, "tasks": [{"id": "A", "outcome": "passed"}, {"id": "B", "outcome": "passed", "checks": [{"passed": False}]}]}]
        result = baseline.aggregate(self.tasks, 1, trials)["overall"]
        self.assertEqual(0, result["passed_attempts"])
        self.assertEqual(2, result["outcome_counts"]["invalid_evidence"])
        self.assertIsNone(result["duration_p95_seconds"])

    def test_duplicate_and_unknown_results_are_rejected(self):
        for trials in [
            [{"repetition": 1}, {"repetition": 1}],
            [{"repetition": 2}],
            [{"repetition": 1, "tasks": [outcome("unknown")]}],
            [{"repetition": 1, "tasks": [outcome("A"), outcome("A")]}],
        ]:
            with self.subTest(trials=trials), self.assertRaises(ValueError):
                baseline.aggregate(self.tasks, 1, trials)

    def test_default_corpus_is_unchanged_common30(self):
        suite = baseline.harness.load_suite()
        self.assertEqual(suite["tasks"][:30], baseline.select_tasks(suite, ""))
        self.assertEqual(100, len(baseline.select_tasks(suite, "", True)))
        self.assertEqual(["H033"], [task["id"] for task in baseline.select_tasks(suite, "h033")])
        with self.assertRaises(ValueError):
            baseline.select_tasks(suite, "H033", True)
        with self.assertRaises(ValueError):
            baseline.select_tasks(suite, "unknown")


class BaselineExecutionTest(unittest.TestCase):
    def options(self, root):
        config = root / "test-config.yaml"
        config.write_text("test_config: true\n", encoding="utf-8")
        return baseline.drivers.DriverOptions(
            base_url="", workspace=root / "unused", results=root / "baseline",
            repo_root=baseline.REPO_ROOT, gateway_config=config,
            gateway_command=(sys.executable, "-c", "raise SystemExit(7)", "{config}", "{workspace}", "{port}"),
            startup_timeout_seconds=2, poll_interval_seconds=0.02,
        )

    def selected(self):
        suite = baseline.harness.load_suite()
        return suite, baseline.select_tasks(suite, "H033,H051")

    def test_repetitions_use_fresh_workspaces_and_shared_runner(self):
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            options = self.options(root)
            suite, tasks = self.selected()
            starts, stops, task_workspaces = [], [], []

            class Gateway:
                base_url = "http://test.invalid"

                def __init__(self, ctx, config):
                    self.ctx = ctx

                def start(self):
                    starts.append(self.ctx.options.workspace)

                def stop(self):
                    stops.append(self.ctx.options.workspace)

            def run_task(task, defaults, base_url, workspace, results):
                marker = workspace / (task["id"] + ".executed")
                self.assertFalse(marker.exists(), "a repeated task inherited earlier output")
                marker.write_text("proof", encoding="utf-8")
                task_workspaces.append(workspace)
                return outcome(task["id"])

            with mock.patch.object(baseline.drivers, "ManagedGateway", Gateway), mock.patch.object(baseline.common, "run_task", side_effect=run_task):
                report = baseline.run_baseline(suite, tasks, options, 2, 1, "deterministic test double")
            self.assertEqual(2, len(set(starts)))
            self.assertEqual(starts, stops)
            self.assertEqual(2, len(set(task_workspaces)))
            self.assertEqual(1, report["metrics"]["overall"]["all_repetitions_pass_rate"])
            self.assertEqual(4, report["metrics"]["overall"]["duration_observations"])
            self.assertTrue(report["inputs_unchanged"])
            self.assertTrue((options.results / "trial-01" / "results" / "summary.json").is_file())
            persisted = json.loads((options.results / "baseline.json").read_text(encoding="utf-8"))
            self.assertEqual(report, persisted)
            with self.assertRaises(FileExistsError):
                baseline.run_baseline(suite, tasks, options, 2, 1, "must not overwrite")

    def test_real_process_startup_failure_is_recorded_for_every_task_and_trial(self):
        with tempfile.TemporaryDirectory() as directory:
            options = self.options(pathlib.Path(directory))
            suite, tasks = self.selected()
            report = baseline.run_baseline(suite, tasks, options, 2, 1, "startup failure fixture")
            self.assertEqual({"harness_error": 4}, report["metrics"]["overall"]["outcome_counts"])
            self.assertEqual(0, report["metrics"]["overall"]["all_repetitions_pass_rate"])
            self.assertTrue(report["inputs_unchanged"])
            for trial in report["trials"]:
                self.assertEqual("harness_error", trial["state"])
                self.assertIn("code 7", trial["error"])

    def test_gateway_cleanup_also_runs_when_start_raises(self):
        with tempfile.TemporaryDirectory() as directory:
            options = self.options(pathlib.Path(directory))
            suite, tasks = self.selected()
            gateway = mock.Mock()
            gateway.start.side_effect = RuntimeError("startup failed")
            with mock.patch.object(baseline.drivers, "ManagedGateway", return_value=gateway):
                with self.assertRaisesRegex(RuntimeError, "startup failed"):
                    baseline.run_trial(suite, tasks, options, 1)
            gateway.stop.assert_called_once_with()

    def test_input_change_stops_new_trials_without_shrinking_denominator(self):
        with tempfile.TemporaryDirectory() as directory:
            options = self.options(pathlib.Path(directory))
            suite, tasks = self.selected()

            def run_trial(*args):
                options.gateway_config.write_text("test_config: changed\n", encoding="utf-8")
                return [outcome(task["id"]) for task in tasks]

            with mock.patch.object(baseline, "run_trial", side_effect=run_trial) as run:
                report = baseline.run_baseline(suite, tasks, options, 3, 1, "drift fixture")
            run.assert_called_once()
            self.assertFalse(report["inputs_unchanged"])
            self.assertEqual(6, report["metrics"]["overall"]["planned_attempts"])
            self.assertEqual(4, report["metrics"]["overall"]["outcome_counts"]["missing"])

    def test_invalid_configuration_does_not_create_output(self):
        with tempfile.TemporaryDirectory() as directory:
            options = self.options(pathlib.Path(directory))
            suite, tasks = self.selected()
            with self.assertRaises(ValueError):
                baseline.run_baseline(suite, tasks, dataclasses.replace(options, gateway_command=()), 2, 1, "fixture")
            self.assertFalse(options.results.exists())

    def test_command_file_cli_accepts_powershell_utf8_bom_and_writes_failure_report(self):
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            options = self.options(root)
            command_file = root / "gateway-command.json"
            command_file.write_text(json.dumps(options.gateway_command), encoding="utf-8-sig")
            completed = subprocess.run(
                [sys.executable, str(pathlib.Path(baseline.__file__).resolve()),
                 "--gateway-command-file", str(command_file),
                 "--gateway-config", str(options.gateway_config),
                 "--results", str(options.results), "--ids", "H033",
                 "--repetitions", "1", "--startup-timeout-seconds", "10"],
                capture_output=True, timeout=30,
            )
            self.assertEqual(2, completed.returncode, completed.stderr.decode(errors="replace"))
            report = json.loads((options.results / "baseline.json").read_text(encoding="utf-8"))
            self.assertEqual({"harness_error": 1}, report["metrics"]["overall"]["outcome_counts"])
            self.assertIn("code 7", report["trials"][0]["error"])


if __name__ == "__main__":
    unittest.main()
