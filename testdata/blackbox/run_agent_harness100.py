#!/usr/bin/env python3
"""Run the OpenKitty 100-task Agent Harness against a live gateway.

The suite intentionally extends, rather than copies, the existing common-30
corpus.  This keeps the original regression suite stable while giving the
larger harness one entry point and one combined set of verdicts.
"""

from __future__ import annotations

import argparse
import concurrent.futures
import dataclasses
import datetime as dt
import json
import pathlib
import sys
import time
from typing import Any

import harness100_drivers as drivers
import run_common30 as common


HERE = pathlib.Path(__file__).resolve().parent
MANIFEST_PATH = HERE / "agent_harness_100.json"
STANDARD_RUNNERS = {"chat", "multi_turn"}
SUPPORTED_STANDARD_CHECKS = {
    "artifact_glob",
    "citation_url_count",
    "command_exit",
    "delivery_receipt_sha256",
    "file_contains_all",
    "file_contains_groups",
    "file_first_line",
    "file_not_contains",
    "file_not_regex",
    "file_regex_count",
    "file_regex_min_count",
    "file_size_min",
    "first_turn_artifact_glob",
    "first_turn_response_contains_all",
    "first_turn_response_regex",
    "html_renderable_image",
    "json_fields",
    "progress_contains_any",
    "progress_not_contains_any",
    "response_contains_all",
    "response_contains_any",
    "response_not_contains",
    "response_regex",
    "terminal_status",
}


def _normalized_prompt(task: dict[str, Any]) -> str:
    prompts = [str(task.get("prompt") or "")]
    prompts.extend(str(value) for value in task.get("follow_ups") or [])
    return "\n".join(" ".join(value.split()).casefold() for value in prompts)


def validate_suite(suite: dict[str, Any]) -> None:
    tasks = suite.get("tasks")
    if not isinstance(tasks, list):
        raise ValueError("suite.tasks must be an array")
    expected = int(suite.get("expected_total", 100))
    if len(tasks) != expected:
        raise ValueError(f"suite has {len(tasks)} tasks; expected {expected}")

    ids: set[str] = set()
    titles: set[str] = set()
    prompts: set[str] = set()
    required = {"id", "category", "title", "agent", "prompt", "acceptance"}
    for index, task in enumerate(tasks, start=1):
        missing = sorted(required - set(task))
        if missing:
            raise ValueError(f"task #{index} missing fields: {missing}")
        task_id = str(task["id"]).strip()
        title = str(task["title"]).strip()
        prompt = _normalized_prompt(task)
        if not task_id or task_id in ids:
            raise ValueError(f"duplicate or empty task id: {task_id!r}")
        if not title or title in titles:
            raise ValueError(f"duplicate or empty title at {task_id}: {title!r}")
        if not prompt or prompt in prompts:
            raise ValueError(f"duplicate or empty prompt at {task_id}")
        ids.add(task_id)
        titles.add(title)
        prompts.add(prompt)

        checks = task.get("acceptance", {}).get("checks")
        if not isinstance(checks, list) or not checks:
            raise ValueError(f"task {task_id} has no acceptance checks")
        for check in checks:
            for field in ("path", "cwd", "delivered_dir"):
                value = str(check.get(field) or "").replace("\\", "/")
                if not value.startswith("runs/"):
                    continue
                parts = value.split("/")
                if len(parts) > 1 and parts[1].upper() != task_id.upper():
                    raise ValueError(
                        f"task {task_id} has cross-task {field}: {check.get(field)!r}"
                    )
        runner = str(task.get("runner") or "chat")
        if runner in STANDARD_RUNNERS:
            unsupported = sorted(
                {str(check.get("type") or "") for check in checks}
                - SUPPORTED_STANDARD_CHECKS
            )
            if unsupported:
                raise ValueError(
                    f"task {task_id} uses unsupported standard-runner checks: {unsupported}"
                )
        else:
            drivers.validate_special_task(task)


def load_suite(path: pathlib.Path = MANIFEST_PATH) -> dict[str, Any]:
    manifest = common.read_json(path)
    base_name = manifest.get("base_corpus")
    if not isinstance(base_name, str) or not base_name.strip():
        raise ValueError("base_corpus is required")
    base_path = (path.parent / base_name).resolve()
    base = common.read_json(base_path)
    extension = manifest.get("tasks")
    if not isinstance(extension, list):
        raise ValueError("manifest.tasks must be an array")

    defaults = dict(base.get("defaults") or {})
    defaults.update(manifest.get("defaults") or {})
    suite = {
        "schema_version": manifest.get("schema_version", 1),
        "name": manifest.get("name", "OpenKitty Agent Harness 100"),
        "description": manifest.get("description", ""),
        "expected_total": manifest.get("expected_total", 100),
        "defaults": defaults,
        "coverage": manifest.get("coverage", {}),
        "tasks": list(base.get("tasks") or []) + extension,
        "base_count": len(base.get("tasks") or []),
        "extension_count": len(extension),
    }
    validate_suite(suite)
    return suite


def write_summary(
    results_root: pathlib.Path,
    suite: dict[str, Any],
    outcomes: list[dict[str, Any]],
    started_at: str,
) -> None:
    counts: dict[str, int] = {}
    for item in outcomes:
        outcome = str(item.get("outcome") or "unknown")
        counts[outcome] = counts.get(outcome, 0) + 1
    summary = {
        "suite": suite["name"],
        "started_at": started_at,
        "finished_at": dt.datetime.now(dt.timezone.utc).isoformat(),
        "counts": counts,
        "tasks": outcomes,
    }
    common.write_json(results_root / "summary.json", summary)
    lines = [
        f"# {suite['name']} result",
        "",
        f"- Started: {started_at}",
        f"- Counts: `{json.dumps(counts, ensure_ascii=False)}`",
        "",
        "| ID | Outcome | Status | Failed checks |",
        "|---|---|---|---:|",
    ]
    for item in outcomes:
        failed = sum(1 for check in item.get("checks", []) if not check.get("passed"))
        lines.append(
            f"| {item['id']} | {item['outcome']} | {item.get('terminal_status', '')} | {failed} |"
        )
    (results_root / "summary.md").write_text("\n".join(lines) + "\n", encoding="utf-8")


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser()
    parser.add_argument("--base-url", default="http://127.0.0.1:8080")
    parser.add_argument("--workspace", type=pathlib.Path)
    parser.add_argument("--results", type=pathlib.Path)
    parser.add_argument("--ids", default="", help="Comma-separated task IDs; empty selects all")
    parser.add_argument("--max-parallel", type=int, default=2)
    parser.add_argument("--stage-only", action="store_true")
    parser.add_argument("--validate-only", action="store_true")
    parser.add_argument("--list", action="store_true", dest="list_tasks")
    parser.add_argument(
        "--gateway-command-json",
        default="",
        help=(
            "JSON string array used by process-owned drivers. It must contain "
            "{config}, {workspace}, and {port} placeholders."
        ),
    )
    parser.add_argument(
        "--gateway-config",
        type=pathlib.Path,
        help="Known-good runtime config copied into each isolated process-driver workspace",
    )
    parser.add_argument(
        "--browser-command-json",
        default="",
        help=(
            "Optional JSON string array for W29. Defaults to the repository's "
            "Go/chromedp browser driver."
        ),
    )
    parser.add_argument("--startup-timeout-seconds", type=int, default=90)
    parser.add_argument(
        "--allow-skipped-special",
        action="store_true",
        help="Return success when selected dedicated-driver tasks were skipped",
    )
    return parser.parse_args()



def run_timed_task(
    task: dict[str, Any], defaults: dict[str, Any], options: drivers.DriverOptions
) -> dict[str, Any]:
    started = time.monotonic()
    try:
        if task.get("runner", "chat") in STANDARD_RUNNERS:
            outcome = common.run_task(task, defaults, options.base_url, options.workspace, options.results)
        else:
            outcome = drivers.run_task(task, defaults, options)
    except Exception as error:
        common.log(f"[{task['id']}] HARNESS ERROR {type(error).__name__}: {error}")
        outcome = {
            "id": task["id"],
            "title": task["title"],
            "outcome": "harness_error",
            "error": f"{type(error).__name__}: {error}",
        }
    outcome["category"] = task["category"]
    outcome["duration_seconds"] = round(time.monotonic() - started, 3)
    common.write_json(options.results / task["id"] / "verdict.json", outcome)
    return outcome


def execute_suite(
    suite: dict[str, Any],
    tasks: list[dict[str, Any]],
    options: drivers.DriverOptions,
    max_parallel: int = 2,
) -> list[dict[str, Any]]:
    """Shared execution path for a single run and isolated repeated baselines."""
    options.results.mkdir(parents=True, exist_ok=True)
    started_at = dt.datetime.now(dt.timezone.utc).isoformat()
    outcomes: list[dict[str, Any]] = []
    standard = [task for task in tasks if task.get("runner", "chat") in STANDARD_RUNNERS]
    with concurrent.futures.ThreadPoolExecutor(max_workers=max(1, max_parallel)) as executor:
        futures = [executor.submit(run_timed_task, task, suite["defaults"], options) for task in standard]
        for future in concurrent.futures.as_completed(futures):
            outcomes.append(future.result())

    # Process-owning drivers never open the live regression gateway's database.
    for task in tasks:
        if task.get("runner", "chat") in STANDARD_RUNNERS:
            continue
        task_options = options
        if str(task.get("runner") or "") in drivers.PROCESS_RUNNERS:
            workspace = (options.workspace / ".harness100-process" / task["id"].lower()).resolve()
            common.stage_workspace(workspace)
            task_options = dataclasses.replace(options, workspace=workspace)
        outcomes.append(run_timed_task(task, suite["defaults"], task_options))
    order = {task["id"]: index for index, task in enumerate(tasks)}
    outcomes.sort(key=lambda item: order[item["id"]])
    write_summary(options.results, suite, outcomes, started_at)
    return outcomes


def main() -> int:
    args = parse_args()
    suite = load_suite()
    if args.validate_only:
        print(
            f"validated {len(suite['tasks'])} tasks "
            f"({suite['base_count']} base + {suite['extension_count']} extension)"
        )
        return 0
    if args.list_tasks:
        for task in suite["tasks"]:
            runner = task.get("runner", "chat")
            print(f"{task['id']}\t{task['category']}\t{runner}\t{task['title']}")
        return 0
    if args.workspace is None or args.results is None:
        raise SystemExit("--workspace and --results are required unless --validate-only or --list is used")

    selected = {value.strip().upper() for value in args.ids.split(",") if value.strip()}
    tasks = [task for task in suite["tasks"] if not selected or task["id"].upper() in selected]
    unknown = selected - {task["id"].upper() for task in suite["tasks"]}
    if unknown:
        raise SystemExit(f"unknown task IDs: {sorted(unknown)}")

    args.workspace = args.workspace.resolve()
    args.results = args.results.resolve()
    common.stage_workspace(args.workspace)
    if args.stage_only:
        print(args.workspace)
        return 0

    external_gateway_tasks = [
        task
        for task in tasks
        if task.get("runner", "chat") in STANDARD_RUNNERS
        or drivers.requires_external_gateway(task)
    ]
    if external_gateway_tasks:
        common.gateway_health(args.base_url, args.workspace)

    try:
        gateway_command = drivers.parse_command_json(
            args.gateway_command_json, "--gateway-command-json"
        )
        browser_command = drivers.parse_command_json(
            args.browser_command_json, "--browser-command-json"
        )
    except ValueError as error:
        raise SystemExit(str(error)) from error
    driver_options = drivers.DriverOptions(
        base_url=args.base_url,
        workspace=args.workspace,
        results=args.results,
        repo_root=HERE.parents[1],
        gateway_command=gateway_command,
        gateway_config=args.gateway_config.resolve() if args.gateway_config else None,
        browser_command=browser_command,
        startup_timeout_seconds=max(10, args.startup_timeout_seconds),
    )

    outcomes = execute_suite(suite, tasks, driver_options, max(1, args.max_parallel))
    return common.outcomes_exit_code(outcomes, args.allow_skipped_special)


if __name__ == "__main__":
    sys.exit(main())
