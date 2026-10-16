#!/usr/bin/env python3
"""Repeat the macs business baseline in fresh, process-owned workspaces."""

from __future__ import annotations

import argparse
import dataclasses
import hashlib
import json
import math
import pathlib
import platform
import subprocess
import sys
import time
from typing import Any

import run_agent_harness100 as harness

common = harness.common
drivers = harness.drivers
REPO_ROOT = pathlib.Path(__file__).resolve().parents[2]


def select_tasks(suite: dict[str, Any], ids: str, all_tasks: bool = False) -> list[dict[str, Any]]:
    selected = {value.strip().upper() for value in ids.split(",") if value.strip()}
    if selected and all_tasks:
        raise ValueError("--ids and --all cannot be combined")
    known = {task["id"] for task in suite["tasks"]}
    if selected - known:
        raise ValueError(f"unknown task IDs: {sorted(selected - known)}")
    if selected:
        return [task for task in suite["tasks"] if task["id"] in selected]
    return suite["tasks"] if all_tasks else suite["tasks"][:suite["base_count"]]


def digest_json(value: Any) -> str:
    return hashlib.sha256(json.dumps(value, sort_keys=True, ensure_ascii=False).encode("utf-8")).hexdigest()


def digest_file(path: pathlib.Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as source:
        for chunk in iter(lambda: source.read(65536), b""):
            digest.update(chunk)
    return digest.hexdigest()


def frozen_inputs(suite: dict[str, Any], config: pathlib.Path, command: tuple[str, ...]) -> dict[str, Any]:
    # Store fingerprints, never config contents or environment variable values.
    files = {str(index): digest_file(pathlib.Path(part)) for index, part in enumerate(command) if pathlib.Path(part).is_file()}
    fixtures = harness.HERE / "common30" / "fixtures"
    fixture_hashes = {path.relative_to(fixtures).as_posix(): digest_file(path) for path in sorted(fixtures.rglob("*")) if path.is_file()}
    for task in suite["tasks"]:
        for relative in task.get("repo_fixtures", []):
            path = (REPO_ROOT / relative).resolve()
            path.relative_to(REPO_ROOT.resolve())
            fixture_hashes["repo/" + relative] = digest_file(path)
    return {
        "manifest_sha256": digest_json(suite),
        "fixtures_sha256": digest_json(fixture_hashes),
        "config_sha256": digest_file(config),
        "command_sha256": digest_json(command),
        "command_files_sha256": files,
    }


def revision_metadata() -> dict[str, Any]:
    def git(*args: str) -> str:
        result = subprocess.run(["git", *args], cwd=REPO_ROOT, capture_output=True, timeout=10, check=True)
        return result.stdout.decode("utf-8", errors="replace").strip()
    try:
        return {"commit": git("rev-parse", "HEAD"), "dirty": bool(git("status", "--porcelain")), "diff_sha256": hashlib.sha256(git("diff", "HEAD", "--").encode()).hexdigest()}
    except (OSError, subprocess.SubprocessError):
        return {"commit": None, "dirty": None, "diff_sha256": None}


def percentile(values: list[float], fraction: float) -> float | None:
    if not values:
        return None
    ordered = sorted(values)
    return ordered[max(0, math.ceil(len(ordered) * fraction) - 1)]


def aggregate(tasks: list[dict[str, Any]], repetitions: int, trials: list[dict[str, Any]]) -> dict[str, Any]:
    """Use the planned denominator; missing runs and invalid evidence cannot pass."""
    if not tasks or repetitions < 1:
        raise ValueError("a baseline needs tasks and at least one repetition")
    task_ids = {task["id"] for task in tasks}
    if len(task_ids) != len(tasks):
        raise ValueError("duplicate task IDs")
    by_trial: dict[int, dict[str, dict[str, Any]]] = {}
    for trial in trials:
        index = trial["repetition"]
        if index in by_trial or not isinstance(index, int) or not 1 <= index <= repetitions:
            raise ValueError("invalid or duplicate repetition")
        outcomes: dict[str, dict[str, Any]] = {}
        for item in trial.get("tasks", []):
            task_id = item["id"]
            if task_id not in task_ids or task_id in outcomes:
                raise ValueError("unknown or duplicate task outcome")
            outcomes[task_id] = item
        by_trial[index] = outcomes

    rows = []
    for task in tasks:
        attempts = []
        for index in range(1, repetitions + 1):
            item = by_trial.get(index, {}).get(task["id"], {})
            outcome = item.get("outcome", "missing")
            checks = item.get("checks", [])
            if outcome == "passed" and (not checks or any(check.get("passed") is not True for check in checks)):
                outcome = "invalid_evidence"
            duration = item.get("duration_seconds")
            if isinstance(duration, bool) or not isinstance(duration, (int, float)) or not math.isfinite(duration) or duration < 0:
                duration = None
            attempts.append({"repetition": index, "outcome": outcome, "duration_seconds": duration})
        passed = sum(item["outcome"] == "passed" for item in attempts)
        rows.append({"id": task["id"], "category": task["category"], "passed": passed, "planned": repetitions, "all_passed": passed == repetitions, "attempts": attempts})

    def metrics(selected: list[dict[str, Any]]) -> dict[str, Any]:
        attempts = [attempt for row in selected for attempt in row["attempts"]]
        durations = [item["duration_seconds"] for item in attempts if item["duration_seconds"] is not None]
        counts: dict[str, int] = {}
        for item in attempts:
            counts[item["outcome"]] = counts.get(item["outcome"], 0) + 1
        return {
            "tasks": len(selected),
            "planned_attempts": len(attempts),
            "passed_attempts": counts.get("passed", 0),
            "attempt_pass_rate": counts.get("passed", 0) / len(attempts),
            "first_attempt_pass_rate": sum(row["attempts"][0]["outcome"] == "passed" for row in selected) / len(selected),
            "all_repetitions_pass_rate": sum(row["all_passed"] for row in selected) / len(selected),
            "unstable_tasks": sum(0 < row["passed"] < repetitions for row in selected),
            "outcome_counts": counts,
            "duration_p50_seconds": percentile(durations, 0.50),
            "duration_p95_seconds": percentile(durations, 0.95),
            "duration_observations": len(durations),
            # Existing drivers do not expose a uniform authoritative usage
            # contract. Missing usage is unknown, never a zero-cost claim.
            "tokens": None,
            "cost_usd": None,
        }

    return {
        "overall": metrics(rows),
        "categories": {category: metrics([row for row in rows if row["category"] == category]) for category in sorted({row["category"] for row in rows})},
        "tasks": rows,
    }


def run_trial(suite: dict[str, Any], tasks: list[dict[str, Any]], options: drivers.DriverOptions, max_parallel: int) -> list[dict[str, Any]]:
    """Own the gateway even if startup fails; reuse the existing task drivers."""
    common.stage_workspace(options.workspace)
    external = any(task.get("runner", "chat") in harness.STANDARD_RUNNERS or drivers.requires_external_gateway(task) for task in tasks)
    if not external:
        return harness.execute_suite(suite, tasks, options, max_parallel)
    context = drivers.DriverContext(
        task={"id": "baseline", "max_minutes": 10}, defaults=suite["defaults"], options=options,
        task_result_dir=options.results / "gateway", run_dir=options.workspace, repo_fixtures=[],
    )
    gateway = drivers.ManagedGateway(context, drivers._runtime_config(context))
    try:
        gateway.start()
        # ManagedGateway verifies the actual workspace before reporting ready.
        return harness.execute_suite(suite, tasks, dataclasses.replace(options, base_url=gateway.base_url), max_parallel)
    finally:
        gateway.stop()


def write_report(root: pathlib.Path, report: dict[str, Any]) -> None:
    common.write_json(root / "baseline.json", report)
    overall = report["metrics"]["overall"]
    lines = [
        "# macs Agent 重复评测基线", "",
        f"- 模型标记（操作者填写）：{report['model_label']}",
        f"- 计划：{overall['tasks']} 题 × {report['repetitions']} 次；输入指纹一致：{report['inputs_unchanged']}",
        f"- 首次通过率：{overall['first_attempt_pass_rate']:.1%}",
        f"- 全部尝试通过率：{overall['attempt_pass_rate']:.1%}",
        f"- 重复全部通过率：{overall['all_repetitions_pass_rate']:.1%}",
        f"- 耗时 P50 / P95（秒）：{overall['duration_p50_seconds']} / {overall['duration_p95_seconds']}",
        "- Token 与费用：未知（当前 Driver 未提供统一可信用量）。", "",
        "重复全部通过率是本次观测值，不是独立同分布假设下的可靠性估计。缺失、跳过、基础设施失败均保留在计划分母中。", "",
        "| 类别 | 题数 | 尝试通过率 | 重复全部通过率 |", "|---|---:|---:|---:|",
    ]
    for category, value in report["metrics"]["categories"].items():
        lines.append(f"| {category} | {value['tasks']} | {value['attempt_pass_rate']:.1%} | {value['all_repetitions_pass_rate']:.1%} |")
    lines += ["", "| 任务 | 通过 / 计划 | 各次结果 |", "|---|---:|---|"]
    for row in report["metrics"]["tasks"]:
        lines.append(f"| {row['id']} | {row['passed']} / {row['planned']} | {', '.join(item['outcome'] for item in row['attempts'])} |")
    (root / "baseline.md").write_text("\n".join(lines) + "\n", encoding="utf-8")


def run_baseline(suite: dict[str, Any], tasks: list[dict[str, Any]], options: drivers.DriverOptions, repetitions: int, max_parallel: int, model_label: str) -> dict[str, Any]:
    root = options.results.resolve()
    if not 1 <= repetitions <= 20:
        raise ValueError("repetitions must be between 1 and 20")
    if options.gateway_config is None or not options.gateway_config.is_file():
        raise ValueError("--gateway-config must name a test gateway configuration")
    if not options.gateway_command or any(token not in "\0".join(options.gateway_command) for token in ("{config}", "{workspace}", "{port}")):
        raise ValueError("gateway command must include {config}, {workspace}, and {port}")
    initial = frozen_inputs(suite, options.gateway_config, options.gateway_command)
    root.mkdir(parents=True, exist_ok=False)  # Never mix new and historical evidence.
    report: dict[str, Any] = {
        "schema_version": 1, "started_at": drivers.utc_now(), "finished_at": None,
        "model_label": model_label, "repetitions": repetitions,
        "selected_ids": [task["id"] for task in tasks], "frozen_inputs": initial,
        "repository": revision_metadata(), "python": platform.python_version(), "platform": platform.platform(),
        "inputs_unchanged": True, "trials": [], "metrics": aggregate(tasks, repetitions, []),
    }
    write_report(root, report)
    try:
        for index in range(1, repetitions + 1):
            if frozen_inputs(harness.load_suite(), options.gateway_config, options.gateway_command) != initial:
                report["inputs_unchanged"] = False
                break
            trial_root = root / f"trial-{index:02d}"
            trial_options = dataclasses.replace(options, workspace=trial_root / "workspace", results=trial_root / "results")
            started = time.monotonic()
            trial: dict[str, Any] = {"repetition": index, "state": "completed", "tasks": []}
            try:
                trial["tasks"] = run_trial(suite, tasks, trial_options, max_parallel)
            except Exception as error:
                trial["state"] = "harness_error"
                trial["error"] = f"{type(error).__name__}: {error}"
                # Record each planned task explicitly, even when gateway startup fails.
                trial["tasks"] = [{"id": task["id"], "outcome": "harness_error"} for task in tasks]
            trial["duration_seconds"] = round(time.monotonic() - started, 3)
            report["trials"].append(trial)
            report["metrics"] = aggregate(tasks, repetitions, report["trials"])
            write_report(root, report)
    finally:
        report["finished_at"] = drivers.utc_now()
        try:
            report["inputs_unchanged"] = report["inputs_unchanged"] and frozen_inputs(harness.load_suite(), options.gateway_config, options.gateway_command) == initial
        except (OSError, ValueError):
            report["inputs_unchanged"] = False
        report["metrics"] = aggregate(tasks, repetitions, report["trials"])
        write_report(root, report)
    return report


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--ids", default="", help="Comma-separated IDs; default is the unchanged common-30")
    parser.add_argument("--all", action="store_true", help="Select all 100 tasks")
    parser.add_argument("--repetitions", type=int, default=3)
    parser.add_argument("--max-parallel", type=int, default=2)
    parser.add_argument("--results", type=pathlib.Path, help="New private output directory, e.g. .local/agent-baseline/run-01")
    gateway = parser.add_mutually_exclusive_group()
    gateway.add_argument("--gateway-command-json", default="")
    gateway.add_argument("--gateway-command-file", type=pathlib.Path, help="UTF-8 JSON string array; avoids Windows shell quoting loss")
    parser.add_argument("--gateway-config", type=pathlib.Path)
    parser.add_argument("--browser-command-json", default="")
    parser.add_argument("--model-label", default="config-defined (unverified)")
    parser.add_argument("--startup-timeout-seconds", type=int, default=90)
    parser.add_argument("--validate-only", action="store_true")
    args = parser.parse_args()
    suite = harness.load_suite()
    try:
        tasks = select_tasks(suite, args.ids, args.all)
        if not 1 <= args.repetitions <= 20:
            raise ValueError("--repetitions must be between 1 and 20")
        if args.validate_only:
            print(f"validated {len(tasks)} tasks x {args.repetitions} repetitions = {len(tasks) * args.repetitions} planned attempts")
            return 0
        if args.results is None:
            raise ValueError("--results is required")
        command_text = args.gateway_command_file.read_text(encoding="utf-8-sig") if args.gateway_command_file else args.gateway_command_json
        command_label = "--gateway-command-file" if args.gateway_command_file else "--gateway-command-json"
        options = drivers.DriverOptions(
            base_url="", workspace=args.results.resolve(), results=args.results.resolve(), repo_root=REPO_ROOT,
            gateway_command=drivers.parse_command_json(command_text, command_label),
            gateway_config=args.gateway_config.resolve() if args.gateway_config else None,
            browser_command=drivers.parse_command_json(args.browser_command_json, "--browser-command-json"),
            startup_timeout_seconds=max(10, args.startup_timeout_seconds),
        )
        report = run_baseline(suite, tasks, options, args.repetitions, max(1, args.max_parallel), args.model_label)
    except (ValueError, OSError) as error:
        parser.error(str(error))
    print(args.results.resolve() / "baseline.md")
    return 0 if report["inputs_unchanged"] and report["metrics"]["overall"]["all_repetitions_pass_rate"] == 1 else 2


if __name__ == "__main__":
    sys.exit(main())
