import json
import os
import pathlib
import re
import subprocess
import tempfile
import unittest

import run_agent_harness100 as harness100


class AgentHarness100DefinitionTest(unittest.TestCase):
    def test_suite_has_exactly_100_unique_executable_definitions(self) -> None:
        suite = harness100.load_suite()
        self.assertEqual(100, len(suite["tasks"]))
        self.assertEqual(30, suite["base_count"])
        self.assertEqual(70, suite["extension_count"])
        self.assertEqual(100, len({task["id"] for task in suite["tasks"]}))
        self.assertTrue(all(task["acceptance"]["checks"] for task in suite["tasks"]))

    def test_original_common30_is_the_unchanged_base_layer(self) -> None:
        suite = harness100.load_suite()
        base = harness100.common.read_json(
            pathlib.Path(harness100.__file__).with_name("common_30_complex_tasks.json")
        )
        self.assertEqual(base["tasks"], suite["tasks"][:30])

    def test_standard_runner_checks_are_supported_by_the_generic_driver(self) -> None:
        suite = harness100.load_suite()
        for task in suite["tasks"]:
            runner = task.get("runner", "chat")
            if runner not in harness100.STANDARD_RUNNERS:
                continue
            check_types = {check["type"] for check in task["acceptance"]["checks"]}
            self.assertFalse(
                check_types - harness100.SUPPORTED_STANDARD_CHECKS,
                f"{task['id']} has unsupported checks",
            )

    def test_dedicated_driver_cases_cannot_silently_count_as_passed(self) -> None:
        suite = harness100.load_suite()
        special = [
            task for task in suite["tasks"] if task.get("runner", "chat") not in harness100.STANDARD_RUNNERS
        ]
        self.assertGreaterEqual(len(special), 10)
        outcomes = [{"outcome": "passed"}, {"outcome": "skipped_special"}]
        self.assertEqual(2, harness100.common.outcomes_exit_code(outcomes))

    def test_every_dedicated_runner_has_an_executable_driver(self) -> None:
        suite = harness100.load_suite()
        special = [
            task
            for task in suite["tasks"]
            if task.get("runner", "chat") not in harness100.STANDARD_RUNNERS
        ]
        self.assertEqual(16, len(special))
        self.assertEqual(
            {task["runner"] for task in special},
            harness100.drivers.all_runner_names(),
        )
        for task in special:
            harness100.drivers.validate_special_task(task)

    def test_process_drivers_are_isolated_from_the_external_gateway(self) -> None:
        suite = harness100.load_suite()
        for task in suite["tasks"]:
            if task.get("runner") in harness100.drivers.PROCESS_RUNNERS:
                self.assertFalse(harness100.drivers.requires_external_gateway(task))

    def test_missing_process_command_is_not_reported_as_a_skip_or_pass(self) -> None:
        suite = harness100.load_suite()
        task = next(task for task in suite["tasks"] if task["id"] == "C06")
        with self.subTest(task=task["id"]):
            with self.assertRaises(harness100.drivers.DriverUnavailable):
                # ManagedGateway performs this check before touching a process.
                options = harness100.drivers.DriverOptions(
                    base_url="http://127.0.0.1:1",
                    workspace=pathlib.Path.cwd(),
                    results=pathlib.Path.cwd(),
                    repo_root=pathlib.Path.cwd(),
                )
                context = harness100.drivers.DriverContext(
                    task=task,
                    defaults=suite["defaults"],
                    options=options,
                    task_result_dir=pathlib.Path.cwd(),
                    run_dir=pathlib.Path.cwd() / "runs" / "C06",
                    repo_fixtures=[],
                )
                harness100.drivers.ManagedGateway(
                    context, pathlib.Path.cwd() / "missing-config.yaml"
                )

    def test_special_evaluator_reads_host_evidence(self) -> None:
        task = {
            "id": "X",
            "title": "test",
            "category": "test",
            "runner": "turn_checkpoint_pre_tool_crash",
            "acceptance": {"checks": []},
        }
        options = harness100.drivers.DriverOptions(
            base_url="http://127.0.0.1:1",
            workspace=pathlib.Path.cwd(),
            results=pathlib.Path.cwd(),
            repo_root=pathlib.Path.cwd(),
        )
        context = harness100.drivers.DriverContext(
            task=task,
            defaults={},
            options=options,
            task_result_dir=pathlib.Path.cwd(),
            run_dir=pathlib.Path.cwd(),
            repo_fixtures=[],
        )
        run = harness100.drivers.DriverResult(
            evidence={
                "tool_calls": {"call-1": {"execution_count": 1}},
                "checkpoint_tool_states": {"call-1": ["pending", "completed"]},
            }
        )
        count = harness100.drivers.evaluate_special_check(
            {"type": "tool_call_execution_count", "call_id": "call-1", "count": 1},
            context,
            run,
        )
        state = harness100.drivers.evaluate_special_check(
            {
                "type": "checkpoint_tool_state",
                "call_id": "call-1",
                "observed": ["pending", "completed"],
            },
            context,
            run,
        )
        self.assertTrue(count["passed"])
        self.assertTrue(state["passed"])

    def test_pipeline_cursor_maps_provider_call_id_by_tool_and_path(self) -> None:
        with tempfile.TemporaryDirectory() as temp_dir:
            root = pathlib.Path(temp_dir)
            workspace = root / "workspace"
            results = root / "results"
            checkpoint_dir = workspace / ".openkitty" / "pipeline_checkpoints"
            checkpoint_dir.mkdir(parents=True)
            results.mkdir()
            turn_path = root / "turn.checkpoint.json"
            turn_path.write_text(
                json.dumps(
                    {
                        "task_id": "pipeline-turn-provider-id",
                        "pending_tool_calls": [
                            {
                                "id": "call_provider_123",
                                "name": "read_file",
                                "arguments": {"path": r"runs\H099\intermediate.md"},
                            }
                        ],
                    }
                ),
                encoding="utf-8",
            )
            (checkpoint_dir / "pipeline.json").write_text(
                json.dumps(
                    {
                        "pipeline_exec_id": "pipe-h099",
                        "turn_checkpoint": str(turn_path),
                    }
                ),
                encoding="utf-8",
            )
            options = harness100.drivers.DriverOptions(
                base_url="http://127.0.0.1:1",
                workspace=workspace,
                results=results,
                repo_root=root,
                poll_interval_seconds=0.01,
            )
            context = harness100.drivers.DriverContext(
                task={"id": "H099", "acceptance": {"checks": []}},
                defaults={},
                options=options,
                task_result_dir=results,
                run_dir=workspace / "runs" / "H099",
                repo_fixtures=[],
            )
            observed = harness100.drivers._wait_pipeline_checkpoint_cursor(
                context,
                "pipe-h099",
                path_fragment="runs/H099/intermediate.md",
                tool_name="read_file",
                timeout=1,
            )
            self.assertEqual("call_provider_123", observed["actual_call_id"])

    def test_h099_counts_checkpointed_completion_without_storage_contract_mismatch(self) -> None:
        checkpoint = {
            "messages": [
                {
                    "role": "tool",
                    "tool_call_id": "call-write",
                    "content": "write receipt",
                },
                {
                    "role": "tool",
                    "tool_call_id": "call-read",
                    "content": "read receipt",
                },
            ]
        }
        self.assertEqual(
            1,
            harness100.drivers._checkpoint_tool_completion_count(
                checkpoint, "call-write"
            ),
        )
        stages = harness100.drivers._pipeline_steps("H099")
        self.assertNotIn("artifact_contract", stages[-1])

    @unittest.skipUnless(os.name == "nt", "Windows hook path normalization")
    def test_windows_tool_hook_matches_native_path_separators(self) -> None:
        with tempfile.TemporaryDirectory() as temp_dir:
            root = pathlib.Path(temp_dir)
            workspace = root / "workspace"
            results = root / "results"
            workspace.mkdir()
            results.mkdir()
            task = {
                "id": "H095",
                "title": "test",
                "category": "test",
                "runner": "turn_checkpoint_pre_tool_crash",
                "acceptance": {"checks": []},
            }
            options = harness100.drivers.DriverOptions(
                base_url="http://127.0.0.1:1",
                workspace=workspace,
                results=results,
                repo_root=root,
            )
            context = harness100.drivers.DriverContext(
                task=task,
                defaults={},
                options=options,
                task_result_dir=results,
                run_dir=workspace / "runs" / "H095",
                repo_fixtures=[],
            )
            gate, entered = harness100.drivers._write_hook_gate(
                context, "runs/H095/result.txt"
            )
            gate.write_text("release", encoding="utf-8")
            script = workspace / ".openkitty" / "hooks" / "pre_tool_use.ps1"
            payload = json.dumps(
                {"params": {"path": str(workspace / "runs" / "H095" / "result.txt")}}
            )
            completed = subprocess.run(
                [
                    "powershell.exe",
                    "-NoProfile",
                    "-NonInteractive",
                    "-ExecutionPolicy",
                    "Bypass",
                    "-File",
                    str(script),
                ],
                input=payload,
                text=True,
                capture_output=True,
                timeout=20,
                check=False,
            )
            self.assertEqual(0, completed.returncode, completed.stderr)
            self.assertIn('"allowed":true', completed.stdout)
            self.assertTrue(entered.is_file())

    def test_extension_ids_and_documented_index_match_the_suite(self) -> None:
        suite = harness100.load_suite()
        expected_extension_ids = [f"H{number:03d}" for number in range(31, 101)]
        self.assertEqual(expected_extension_ids, [task["id"] for task in suite["tasks"][30:]])

        documentation = pathlib.Path(harness100.__file__).with_name(
            "AGENT_HARNESS_100.md"
        ).read_text(encoding="utf-8")
        documented_ids = re.findall(r"^\|\s*\d+\s*\|\s*([^| ]+)\s*\|", documentation, re.MULTILINE)
        self.assertEqual([task["id"] for task in suite["tasks"]], documented_ids)

    def test_acceptance_paths_do_not_point_into_another_task(self) -> None:
        suite = harness100.load_suite()
        for task in suite["tasks"]:
            for check in task["acceptance"]["checks"]:
                for field in ("path", "cwd", "delivered_dir"):
                    value = str(check.get(field) or "").replace("\\", "/")
                    if value.startswith("runs/"):
                        self.assertEqual(task["id"].upper(), value.split("/")[1].upper())


if __name__ == "__main__":
    unittest.main()
