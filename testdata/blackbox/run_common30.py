#!/usr/bin/env python3
"""Run the common-30 OpenKitty black-box corpus against a live gateway.

The harness intentionally uses only the Python standard library. It stages a
fresh workspace, drives the public HTTP API, records every task receipt, and
evaluates deterministic assertions. Special process/browser scenarios are
reported as skipped until their dedicated driver runs them; they are never
counted as passes.
"""

from __future__ import annotations

import argparse
import concurrent.futures
import datetime as dt
import glob
import hashlib
import json
import os
import pathlib
import re
import shutil
import subprocess
import sys
import threading
import time
import urllib.error
import urllib.parse
import urllib.request
from typing import Any


ROOT = pathlib.Path(__file__).resolve().parents[2]
CORPUS_PATH = pathlib.Path(__file__).with_name("common_30_complex_tasks.json")
FIXTURE_SOURCE = pathlib.Path(__file__).with_name("common30") / "fixtures"
TERMINAL = {"completed", "failed", "stopped", "cancelled", "canceled", "degraded"}
PRINT_LOCK = threading.Lock()


def log(message: str) -> None:
    with PRINT_LOCK:
        print(message, flush=True)


def read_json(path: pathlib.Path) -> dict[str, Any]:
    return json.loads(path.read_text(encoding="utf-8-sig"))


def write_json(path: pathlib.Path, value: Any) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(value, ensure_ascii=False, indent=2), encoding="utf-8")


def http_request(
    base_url: str,
    method: str,
    path: str,
    payload: dict[str, Any] | None = None,
    timeout: float = 30,
) -> tuple[int, Any, str]:
    body = None
    headers = {"Accept": "application/json"}
    if payload is not None:
        body = json.dumps(payload, ensure_ascii=False).encode("utf-8")
        headers["Content-Type"] = "application/json; charset=utf-8"
    request = urllib.request.Request(base_url.rstrip("/") + path, data=body, method=method, headers=headers)
    try:
        with urllib.request.urlopen(request, timeout=timeout) as response:
            raw = response.read().decode("utf-8", errors="replace")
            try:
                parsed: Any = json.loads(raw)
            except json.JSONDecodeError:
                parsed = raw
            return response.status, parsed, raw
    except urllib.error.HTTPError as error:
        raw = error.read().decode("utf-8", errors="replace")
        try:
            parsed = json.loads(raw)
        except json.JSONDecodeError:
            parsed = raw
        return error.code, parsed, raw


def canonical_workspace(value: os.PathLike[str] | str) -> str:
    return os.path.normcase(os.path.realpath(os.fspath(value)))


def gateway_health(base_url: str, expected_workspace: pathlib.Path) -> None:
    status, payload, raw = http_request(base_url, "GET", "/api/status", timeout=10)
    if status != 200:
        raise RuntimeError(f"gateway health failed: HTTP {status}: {raw[:300]}")
    if not isinstance(payload, dict):
        raise RuntimeError("gateway health returned no structured project workspace")
    project = payload.get("project")
    actual_workspace = project.get("workspace") if isinstance(project, dict) else None
    if not isinstance(actual_workspace, str) or not actual_workspace.strip():
        raise RuntimeError("gateway health did not report project.workspace")
    actual = canonical_workspace(actual_workspace)
    expected = canonical_workspace(expected_workspace)
    if actual != expected:
        raise RuntimeError(
            "gateway workspace mismatch: "
            f"server={actual_workspace!r} expected={str(expected_workspace)!r}; "
            "start an isolated gateway with the requested workspace"
        )


def stage_workspace(workspace: pathlib.Path) -> None:
    workspace.mkdir(parents=True, exist_ok=True)
    fixture_target = workspace / "fixtures"
    shutil.copytree(FIXTURE_SOURCE, fixture_target, dirs_exist_ok=True)
    user_skill = FIXTURE_SOURCE / "user_skill" / "quarterly-risk-memo"
    shutil.copytree(user_skill, workspace / "skills" / "quarterly-risk-memo", dirs_exist_ok=True)


def stage_repo_fixtures(task: dict[str, Any], workspace: pathlib.Path) -> list[str]:
    staged: list[str] = []
    for relative in task.get("repo_fixtures") or []:
        source = (ROOT / relative).resolve()
        try:
            source.relative_to(ROOT.resolve())
        except ValueError as error:
            raise RuntimeError(f"repo fixture escapes repository: {relative}") from error
        if not source.is_file():
            raise FileNotFoundError(f"repo fixture missing: {relative}")
        target = workspace / "fixtures" / "repo" / pathlib.Path(relative)
        target.parent.mkdir(parents=True, exist_ok=True)
        shutil.copy2(source, target)
        staged.append(target.relative_to(workspace).as_posix())
    return staged


def expand_prompt(text: str, task_id: str, repo_fixtures: list[str]) -> str:
    expanded = text.replace("{{fixture_root}}", "fixtures").replace("{{run_dir}}", f"runs/{task_id}")
    if repo_fixtures:
        expanded += "\n\n测试夹具已放在工作区内，路径如下（把它们当作用户随任务提供的文件）：\n"
        expanded += "\n".join(f"- `{path}`" for path in repo_fixtures)
    expanded += (
        "\n\n黑盒运行约束：只在当前隔离工作区内读写；不得注册、购买、发消息、上传外部数据或执行其他外部写操作；"
        "没有可核验证据时不得宣称产物已完成。"
    )
    return expanded


def result_text(value: Any) -> str:
    if value is None:
        return ""
    if isinstance(value, str):
        return value
    if isinstance(value, dict):
        preferred = []
        for key in ("final_answer", "response", "content", "message", "result", "output", "summary"):
            if key in value:
                preferred.append(result_text(value[key]))
        if any(item.strip() for item in preferred):
            return "\n".join(item for item in preferred if item.strip())
    return json.dumps(value, ensure_ascii=False, indent=2)


def cancel_task_after_timeout(base_url: str, task_id: str) -> dict[str, Any]:
    """Best-effort cleanup for a runner-owned task whose polling budget expired."""
    path = "/api/task/stop/" + urllib.parse.quote(task_id, safe="")
    try:
        status, payload, raw = http_request(base_url, "POST", path, timeout=20)
    except Exception as error:  # timeout reporting must survive cleanup failure
        return {"requested": True, "succeeded": False, "error": str(error)}
    return {
        "requested": True,
        "succeeded": status == 200,
        "http_status": status,
        "response": payload if isinstance(payload, dict) else raw[:500],
    }


def poll_task(base_url: str, task_id: str, timeout_seconds: int, result_dir: pathlib.Path) -> dict[str, Any]:
    # The task can publish its terminal row at the same instant the nominal
    # budget expires. Give the gateway a short settlement window so a result
    # completed one second late is not misreported as an abandoned timeout.
    settlement_grace_seconds = min(30, max(10, timeout_seconds // 60))
    deadline = time.monotonic() + timeout_seconds + settlement_grace_seconds
    history: list[dict[str, Any]] = []
    last_status = ""
    while time.monotonic() < deadline:
        status_code, payload, raw = http_request(
            base_url, "GET", "/api/task/" + urllib.parse.quote(task_id, safe=""), timeout=20
        )
        if status_code == 404:
            time.sleep(1)
            continue
        if status_code != 200 or not isinstance(payload, dict):
            raise RuntimeError(f"task poll failed: HTTP {status_code}: {raw[:500]}")
        status = str(payload.get("status") or "").lower()
        if status != last_status or status in TERMINAL:
            history.append({"time": dt.datetime.now(dt.timezone.utc).isoformat(), "payload": payload})
            last_status = status
        if status in TERMINAL:
            write_json(result_dir / "status_history.json", history)
            return payload
        time.sleep(2)
    cancellation = cancel_task_after_timeout(base_url, task_id)
    history.append({
        "time": dt.datetime.now(dt.timezone.utc).isoformat(),
        "runner_timeout_cleanup": cancellation,
    })
    write_json(result_dir / "status_history.json", history)
    return {
        "task_id": task_id,
        "status": "timeout",
        "error": f"exceeded {timeout_seconds}s",
        "cancellation": cancellation,
    }


def download_progress(base_url: str, task_id: str, output: pathlib.Path) -> str:
    status, payload, raw = http_request(
        base_url, "GET", "/api/task/progress-log/" + urllib.parse.quote(task_id, safe=""), timeout=30
    )
    text = raw if isinstance(payload, str) else json.dumps(payload, ensure_ascii=False, indent=2)
    output.write_text(f"HTTP {status}\n{text}", encoding="utf-8")
    return text


def safe_workspace_path(workspace: pathlib.Path, relative: str) -> pathlib.Path:
    candidate = (workspace / relative).resolve()
    try:
        candidate.relative_to(workspace.resolve())
    except ValueError as error:
        raise RuntimeError(f"assertion path escapes workspace: {relative}") from error
    return candidate


def brace_patterns(pattern: str) -> list[str]:
    match = re.search(r"\{([^{}]+)\}", pattern)
    if not match:
        return [pattern]
    values = match.group(1).split(",")
    return sum(
        (brace_patterns(pattern[: match.start()] + value + pattern[match.end() :]) for value in values), []
    )


def glob_files(workspace: pathlib.Path, pattern: str) -> list[pathlib.Path]:
    found: set[pathlib.Path] = set()
    for expanded in brace_patterns(pattern):
        absolute = safe_workspace_path(workspace, expanded)
        for value in glob.glob(str(absolute), recursive=True):
            path = pathlib.Path(value)
            if path.is_file():
                found.add(path.resolve())
    return sorted(found)


def contains_all(haystack: str, values: list[str]) -> tuple[bool, str]:
    missing = [value for value in values if value not in haystack]
    return not missing, "missing=" + repr(missing) if missing else "all present"


def contains_groups(haystack: str, groups: list[list[str]]) -> tuple[bool, str]:
    folded = haystack.casefold()
    missing = [group for group in groups if not any(value.casefold() in folded for value in group)]
    return not missing, "missing_groups=" + repr(missing) if missing else "all concept groups present"


def evaluate_check(
    check: dict[str, Any],
    workspace: pathlib.Path,
    final_payload: dict[str, Any],
    final_text: str,
    progress_text: str,
    first_text: str,
    first_glob_counts: dict[str, int],
) -> dict[str, Any]:
    kind = check["type"]
    passed = False
    detail = ""
    try:
        if kind == "terminal_status":
            actual = str(final_payload.get("status") or "").lower()
            expected = [str(value).lower() for value in check["one_of"]]
            passed, detail = actual in expected, f"actual={actual!r} expected={expected!r}"
        elif kind in {"response_contains_all", "first_turn_response_contains_all"}:
            source = first_text if kind.startswith("first_turn") else final_text
            passed, detail = contains_all(source, check["values"])
        elif kind == "response_contains_any":
            hits = [value for value in check["values"] if value in final_text]
            passed, detail = bool(hits), f"hits={hits!r}"
        elif kind == "response_not_contains":
            passed = check["value"] not in final_text
            detail = f"forbidden_present={not passed}"
        elif kind in {"first_turn_response_regex", "response_regex"}:
            source = first_text if kind.startswith("first_turn") else final_text
            passed = re.search(check["pattern"], source, re.MULTILINE) is not None
            detail = f"pattern={check['pattern']!r}"
        elif kind in {"progress_contains_any", "progress_not_contains_any"}:
            hits = [value for value in check["values"] if value in progress_text]
            passed = bool(hits) if kind == "progress_contains_any" else not hits
            detail = f"hits={hits!r}"
        elif kind in {"artifact_glob", "first_turn_artifact_glob"}:
            files = glob_files(workspace, check["pattern"]) if kind == "artifact_glob" else []
            count = len(files) if kind == "artifact_glob" else first_glob_counts.get(check["pattern"], 0)
            minimum = int(check.get("min_count", 0))
            maximum = int(check.get("max_count", 2**31 - 1))
            passed = minimum <= count <= maximum
            detail = f"count={count} min={minimum} max={maximum} files={[str(p.relative_to(workspace.resolve())) for p in files[:10]]}"
        elif kind in {
            "file_contains_all",
            "file_contains_groups",
            "file_not_contains",
            "file_not_regex",
            "file_first_line",
            "file_regex_count",
            "file_regex_min_count",
        }:
            path = safe_workspace_path(workspace, check["path"])
            content = path.read_text(encoding="utf-8-sig", errors="replace")
            if kind == "file_contains_all":
                passed, detail = contains_all(content, check["values"])
            elif kind == "file_contains_groups":
                passed, detail = contains_groups(content, check["groups"])
            elif kind == "file_not_contains":
                passed, detail = check["value"] not in content, f"forbidden_present={check['value'] in content}"
            elif kind == "file_not_regex":
                hit = re.search(check["pattern"], content, re.MULTILINE)
                passed, detail = hit is None, f"match={hit.group(0)!r}" if hit else "no match"
            elif kind == "file_first_line":
                first = content.splitlines()[0] if content.splitlines() else ""
                passed, detail = first == check["value"], f"actual={first!r}"
            elif kind == "file_regex_count":
                count = len(re.findall(check["pattern"], content, re.MULTILINE))
                passed, detail = count == int(check["count"]), f"actual={count} expected={check['count']}"
            else:
                count = len(re.findall(check["pattern"], content, re.MULTILINE))
                minimum = int(check["min_count"])
                passed, detail = count >= minimum, f"actual={count} minimum={minimum}"
        elif kind == "file_size_min":
            path = safe_workspace_path(workspace, check["path"])
            size = path.stat().st_size
            passed, detail = size >= int(check["bytes"]), f"size={size} minimum={check['bytes']}"
        elif kind == "command_exit":
            cwd = safe_workspace_path(workspace, check["cwd"])
            completed = subprocess.run(
                check["command"], cwd=cwd, shell=True, capture_output=True, text=True, timeout=180
            )
            expected = int(check.get("exit_code", 0))
            passed = completed.returncode == expected
            combined = completed.stdout + "\n" + completed.stderr
            looks_like_test_command = re.search(
                r"(?i)(?:unittest|pytest|go\s+test|cargo\s+test|dotnet\s+test|(?:npm|pnpm|yarn)\s+(?:run\s+)?test)",
                check["command"],
            ) is not None
            zero_tests = re.search(
                r"(?i)(?:ran\s+0\s+tests?|collected\s+0\s+items?|no\s+tests?\s+(?:ran|found|collected)|no\s+test\s+files?\s+found)",
                combined,
            ) is not None
            if passed and looks_like_test_command and zero_tests and not check.get("allow_zero_tests", False):
                passed = False
            detail = (
                f"exit={completed.returncode} expected={expected}\n"
                f"zero_tests={zero_tests}\nstdout={completed.stdout[-1500:]}\nstderr={completed.stderr[-1500:]}"
            )
        elif kind == "citation_url_count":
            path = safe_workspace_path(workspace, check["path"])
            urls = set(re.findall(r"https?://[^\s)>\]]+", path.read_text(encoding="utf-8-sig", errors="replace")))
            passed, detail = len(urls) >= int(check["min_count"]), f"count={len(urls)}"
        elif kind == "json_fields":
            path = safe_workspace_path(workspace, check["path"])
            value = read_json(path)
            missing = [field for field in check["fields"] if field not in value]
            passed, detail = not missing, f"missing={missing!r}"
        elif kind == "delivery_receipt_sha256":
            receipt_path = safe_workspace_path(workspace, check["path"])
            delivered_dir = safe_workspace_path(workspace, check["delivered_dir"])
            receipt = read_json(receipt_path)
            expected_files = [str(value) for value in check["files"]]
            receipt_hashes = receipt.get("sha256")
            receipt_files = receipt.get("files")
            verification = receipt.get("verification")
            verification_status = verification.get("status") if isinstance(verification, dict) else verification
            problems: list[str] = []
            if not isinstance(receipt_hashes, dict):
                problems.append("sha256 must be an object keyed by delivered filename")
                receipt_hashes = {}
            if not isinstance(receipt_files, list):
                problems.append("files must be an array")
                receipt_files = []
            for relative in expected_files:
                file_path = safe_workspace_path(delivered_dir, relative)
                if not file_path.is_file():
                    problems.append(f"missing delivered file: {relative}")
                    continue
                actual_hash = hashlib.sha256(file_path.read_bytes()).hexdigest()
                recorded_hash = receipt_hashes.get(relative)
                if not isinstance(recorded_hash, str) or recorded_hash.casefold() != actual_hash:
                    problems.append(f"hash mismatch: {relative}")
                if relative not in receipt_files:
                    problems.append(f"files[] missing: {relative}")
            manifest_name = check.get("checksum_manifest")
            for relative in check.get("manifest_files", []):
                if not manifest_name:
                    break
                manifest_path = safe_workspace_path(delivered_dir, str(manifest_name))
                manifest = manifest_path.read_text(encoding="utf-8-sig", errors="replace")
                file_path = safe_workspace_path(delivered_dir, str(relative))
                actual_hash = hashlib.sha256(file_path.read_bytes()).hexdigest()
                if actual_hash not in manifest or str(relative) not in manifest:
                    problems.append(f"checksum manifest mismatch: {relative}")
            if str(verification_status).casefold() != "verified":
                problems.append(f"verification status is {verification_status!r}, want 'verified'")
            passed, detail = not problems, "verified hashes and manifest" if not problems else "; ".join(problems)
        elif kind == "html_renderable_image":
            path = safe_workspace_path(workspace, check["path"])
            html = path.read_text(encoding="utf-8-sig", errors="replace")
            sources = re.findall(r"<img\b[^>]*\bsrc=[\"']([^\"']+)", html, re.IGNORECASE)
            valid = 0
            for source in sources:
                if source.startswith(("data:image/", "http://", "https://")):
                    valid += 1
                else:
                    image_path = (path.parent / urllib.parse.unquote(source.split("?", 1)[0])).resolve()
                    if image_path.is_file() and image_path.stat().st_size > 0:
                        valid += 1
            passed, detail = valid >= int(check["min_count"]), f"renderable={valid} sources={sources!r}"
        else:
            detail = "unsupported deterministic check; requires dedicated special runner"
    except Exception as error:  # Assertion errors are data, not harness crashes.
        passed = False
        detail = f"{type(error).__name__}: {error}"
    return {"type": kind, "passed": passed, "detail": detail, "check": check}


def run_task(
    task: dict[str, Any],
    defaults: dict[str, Any],
    base_url: str,
    workspace: pathlib.Path,
    results_root: pathlib.Path,
) -> dict[str, Any]:
    task_id = task["id"]
    runner = task.get("runner", "chat")
    task_result_dir = results_root / task_id
    task_result_dir.mkdir(parents=True, exist_ok=True)
    if runner not in {"chat", "multi_turn"}:
        outcome = {
            "id": task_id,
            "title": task["title"],
            "outcome": "skipped_special",
            "runner": runner,
            "reason": "requires dedicated process/browser driver",
        }
        write_json(task_result_dir / "verdict.json", outcome)
        log(f"[{task_id}] SKIP special runner={runner}")
        return outcome

    run_dir = workspace / "runs" / task_id
    if run_dir.exists() and any(run_dir.iterdir()):
        raise RuntimeError(f"refusing to reuse non-empty task directory: {run_dir}")
    repo_fixtures = stage_repo_fixtures(task, workspace)
    session_id = f"bb30_{task_id.lower()}_{int(time.time() * 1000)}"
    prompts = [task["prompt"]] + list(task.get("follow_ups") or [])
    turns: list[dict[str, Any]] = []
    progress_parts: list[str] = []
    first_glob_counts: dict[str, int] = {}
    max_minutes = int(task.get("max_minutes", defaults.get("max_minutes", 10)))
    final_payload: dict[str, Any] = {}
    log(f"[{task_id}] START {task['title']}")
    for index, raw_prompt in enumerate(prompts, start=1):
        prompt = expand_prompt(raw_prompt, task_id, repo_fixtures if index == 1 else [])
        request_payload = {
            "task_type": "chat",
            "session_id": session_id,
            "message": prompt,
            "agent_name": task.get("agent", "Assistant"),
            "channel": defaults.get("channel", "blackbox"),
            "user_id": f"{defaults.get('user_id', 'common30')}-{task_id}",
            "stream": False,
        }
        write_json(task_result_dir / f"turn_{index:02d}_request.json", request_payload)
        status_code, initial, raw = http_request(base_url, "POST", "/api/chat", request_payload, timeout=45)
        write_json(task_result_dir / f"turn_{index:02d}_initial.json", {"http_status": status_code, "body": initial, "raw": raw})
        if status_code >= 400 or not isinstance(initial, dict) or not initial.get("task_id"):
            final_payload = {"status": "harness_error", "error": f"chat start HTTP {status_code}: {raw[:500]}"}
            turns.append({"request": request_payload, "initial": initial, "final": final_payload, "text": result_text(final_payload)})
            break
        gateway_task_id = str(initial["task_id"])
        final_payload = poll_task(base_url, gateway_task_id, max_minutes * 60, task_result_dir / f"turn_{index:02d}")
        text = result_text(final_payload.get("result"))
        if not text:
            text = result_text(final_payload)
        progress = download_progress(base_url, gateway_task_id, task_result_dir / f"turn_{index:02d}_progress.log")
        progress_parts.append(progress)
        turn = {"request": request_payload, "initial": initial, "final": final_payload, "text": text}
        turns.append(turn)
        write_json(task_result_dir / f"turn_{index:02d}_final.json", turn)
        if index == 1:
            for check in task.get("acceptance", {}).get("checks", []):
                if check.get("type") == "first_turn_artifact_glob":
                    first_glob_counts[check["pattern"]] = len(glob_files(workspace, check["pattern"]))
        if str(final_payload.get("status") or "").lower() not in {"completed", "degraded"}:
            break

    final_text = turns[-1]["text"] if turns else ""
    first_text = turns[0]["text"] if turns else ""
    progress_text = "\n".join(progress_parts)
    checks = [
        evaluate_check(check, workspace, final_payload, final_text, progress_text, first_text, first_glob_counts)
        for check in task.get("acceptance", {}).get("checks", [])
    ]
    passed = bool(checks) and all(check["passed"] for check in checks)
    outcome = {
        "id": task_id,
        "title": task["title"],
        "category": task["category"],
        "runner": runner,
        "outcome": "passed" if passed else "failed",
        "terminal_status": final_payload.get("status"),
        "checks": checks,
        "turn_count": len(turns),
        "session_id": session_id,
    }
    write_json(task_result_dir / "verdict.json", outcome)
    failures = sum(1 for check in checks if not check["passed"])
    log(f"[{task_id}] {'PASS' if passed else 'FAIL'} status={final_payload.get('status')} failed_checks={failures}/{len(checks)}")
    return outcome


def write_summary(results_root: pathlib.Path, outcomes: list[dict[str, Any]], started_at: str) -> None:
    counts: dict[str, int] = {}
    for item in outcomes:
        counts[item["outcome"]] = counts.get(item["outcome"], 0) + 1
    summary = {"started_at": started_at, "finished_at": dt.datetime.now(dt.timezone.utc).isoformat(), "counts": counts, "tasks": outcomes}
    write_json(results_root / "summary.json", summary)
    lines = ["# OpenKitty common-30 black-box result", "", f"- Started: {started_at}", f"- Counts: `{json.dumps(counts, ensure_ascii=False)}`", "", "| ID | Outcome | Status | Failed checks |", "|---|---|---|---:|"]
    for item in outcomes:
        failed = sum(1 for check in item.get("checks", []) if not check.get("passed"))
        lines.append(f"| {item['id']} | {item['outcome']} | {item.get('terminal_status', '')} | {failed} |")
    (results_root / "summary.md").write_text("\n".join(lines) + "\n", encoding="utf-8")


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser()
    parser.add_argument("--base-url", default="http://127.0.0.1:8080")
    parser.add_argument("--workspace", type=pathlib.Path, required=True)
    parser.add_argument("--results", type=pathlib.Path, required=True)
    parser.add_argument("--ids", default="", help="Comma-separated task IDs; empty selects all")
    parser.add_argument("--max-parallel", type=int, default=2)
    parser.add_argument("--stage-only", action="store_true")
    parser.add_argument(
        "--allow-skipped-special",
        action="store_true",
        help="Return success when selected dedicated process/browser runners were skipped",
    )
    return parser.parse_args()


def outcomes_exit_code(outcomes: list[dict[str, Any]], allow_skipped_special: bool = False) -> int:
    successful = {"passed"}
    if allow_skipped_special:
        successful.add("skipped_special")
    return 0 if all(item.get("outcome") in successful for item in outcomes) else 2


def main() -> int:
    args = parse_args()
    args.workspace = args.workspace.resolve()
    args.results = args.results.resolve()
    corpus = read_json(CORPUS_PATH)
    stage_workspace(args.workspace)
    if args.stage_only:
        print(args.workspace.resolve())
        return 0
    gateway_health(args.base_url, args.workspace)
    selected = {value.strip().upper() for value in args.ids.split(",") if value.strip()}
    tasks = [task for task in corpus["tasks"] if not selected or task["id"].upper() in selected]
    unknown = selected - {task["id"].upper() for task in corpus["tasks"]}
    if unknown:
        raise SystemExit(f"unknown task IDs: {sorted(unknown)}")
    args.results.mkdir(parents=True, exist_ok=True)
    started_at = dt.datetime.now(dt.timezone.utc).isoformat()
    outcomes: list[dict[str, Any]] = []
    with concurrent.futures.ThreadPoolExecutor(max_workers=max(1, args.max_parallel)) as executor:
        futures = {
            executor.submit(run_task, task, corpus["defaults"], args.base_url, args.workspace, args.results): task
            for task in tasks
        }
        for future in concurrent.futures.as_completed(futures):
            task = futures[future]
            try:
                outcomes.append(future.result())
            except Exception as error:
                log(f"[{task['id']}] HARNESS ERROR {type(error).__name__}: {error}")
                outcome = {"id": task["id"], "title": task["title"], "outcome": "harness_error", "error": f"{type(error).__name__}: {error}"}
                write_json(args.results / task["id"] / "verdict.json", outcome)
                outcomes.append(outcome)
    order = {task["id"]: index for index, task in enumerate(corpus["tasks"])}
    outcomes.sort(key=lambda item: order.get(item["id"], 10**9))
    write_summary(args.results, outcomes, started_at)
    return outcomes_exit_code(outcomes, args.allow_skipped_special)


if __name__ == "__main__":
    sys.exit(main())
