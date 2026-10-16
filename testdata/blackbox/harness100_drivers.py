#!/usr/bin/env python3
"""Dedicated lifecycle drivers for the OpenKitty Agent Harness 100.

The common runner deliberately stays unchanged for ordinary chat tasks.  This
module owns the cases that need more than one HTTP request: process crashes,
browser interaction, parallel sessions, collaboration and pipelines.  Every
driver emits host-observed evidence and evaluates the manifest checks against
that evidence; an unavailable dependency is never reported as a pass.
"""

from __future__ import annotations

import concurrent.futures
import dataclasses
import datetime as dt
import hashlib
import json
import mimetypes
import os
import pathlib
import re
import shlex
import shutil
import socket
import sqlite3
import subprocess
import sys
import threading
import time
import urllib.parse
import uuid
from typing import Any, Callable

import run_common30 as common


STANDARD_CHECKS = frozenset({
    "artifact_glob",
    "delivery_receipt_sha256",
    "file_contains_all",
    "file_regex_count",
    "json_fields",
    "response_not_contains",
    "terminal_status",
})

SPECIAL_CHECKS = frozenset({
    "attachment_available_after_restart",
    "backup_created",
    "branch_status",
    "checkpoint_terminal_persisted",
    "checkpoint_tool_state",
    "console_error_count",
    "duplicate_side_effect_count",
    "duplicate_tool_call_count",
    "http_json_contains",
    "http_status",
    "idempotent_run_count",
    "mobile_overflow_x",
    "parallel_overlap",
    "persisted_attachment_count",
    "pipeline_stage_count",
    "pipeline_status",
    "post_cancel_tool_call_count",
    "process_exit",
    "recovery_available",
    "refresh_preserves_attachments",
    "request_attachment_count",
    "restart_count",
    "resume_cursor",
    "resume_status",
    "same_bubble_attachment_count",
    "session_cross_talk_count",
    "session_status",
    "session_turn_count",
    "stage_receipt_hashes",
    "stage_run_count",
    "stdout_contains_all",
    "subagent_count",
    "tool_call_execution_count",
    "workspace_partition_intact",
})

SUPPORTED_CHECKS = STANDARD_CHECKS | SPECIAL_CHECKS

PROCESS_RUNNERS = frozenset({
    "process_config_recovery",
    "process_restart_resume",
    "turn_checkpoint_post_tool_crash",
    "turn_checkpoint_pre_tool_crash",
    "turn_checkpoint_multi_tool_resume",
    "session_attachment_restart",
    "cancel_restart_race",
    "pipeline_turn_checkpoint_resume",
    "durable_end_to_end",
})


class DriverUnavailable(RuntimeError):
    """The selected scenario cannot run because its explicit dependency is absent."""


@dataclasses.dataclass(frozen=True)
class DriverOptions:
    base_url: str
    workspace: pathlib.Path
    results: pathlib.Path
    repo_root: pathlib.Path
    gateway_command: tuple[str, ...] = ()
    gateway_config: pathlib.Path | None = None
    browser_command: tuple[str, ...] = ()
    startup_timeout_seconds: int = 90
    poll_interval_seconds: float = 0.2


@dataclasses.dataclass
class DriverResult:
    evidence: dict[str, Any] = dataclasses.field(default_factory=dict)
    final_payload: dict[str, Any] = dataclasses.field(default_factory=dict)
    final_text: str = ""
    progress_text: str = ""
    first_text: str = ""
    first_glob_counts: dict[str, int] = dataclasses.field(default_factory=dict)
    session_id: str = ""
    turn_count: int = 0


@dataclasses.dataclass
class DriverContext:
    task: dict[str, Any]
    defaults: dict[str, Any]
    options: DriverOptions
    task_result_dir: pathlib.Path
    run_dir: pathlib.Path
    repo_fixtures: list[str]

    @property
    def task_id(self) -> str:
        return str(self.task["id"])

    @property
    def timeout_seconds(self) -> int:
        minutes = int(self.task.get("max_minutes", self.defaults.get("max_minutes", 10)))
        return max(60, minutes * 60)

    def prompt(self, extra: str = "") -> str:
        value = common.expand_prompt(self.task["prompt"], self.task_id, self.repo_fixtures)
        marker = f"\n\nHARNESS_SCENARIO={self.task_id}"
        if extra:
            marker += "\n" + extra.strip()
        return value + marker


def utc_now() -> str:
    return dt.datetime.now(dt.timezone.utc).isoformat()


def _free_local_port() -> int:
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as sock:
        sock.bind(("127.0.0.1", 0))
        return int(sock.getsockname()[1])


def _command_display(command: list[str]) -> str:
    return subprocess.list2cmdline(command) if os.name == "nt" else shlex.join(command)


class ManagedGateway:
    """Own exactly one test gateway process and its append-only process log."""

    def __init__(self, ctx: DriverContext, config_path: pathlib.Path) -> None:
        if not ctx.options.gateway_command:
            raise DriverUnavailable(
                "process driver requires --gateway-command-json; no process was started"
            )
        template = list(ctx.options.gateway_command)
        joined = "\x00".join(template)
        missing = [token for token in ("{config}", "{workspace}", "{port}") if token not in joined]
        if missing:
            raise DriverUnavailable(
                "--gateway-command-json must contain isolated placeholders " + ", ".join(missing)
            )
        self.ctx = ctx
        self.config_path = config_path.resolve()
        self.port = _free_local_port()
        self.base_url = f"http://127.0.0.1:{self.port}"
        self.log_path = ctx.task_result_dir / "gateway.log"
        self.process: subprocess.Popen[bytes] | None = None
        self.restart_count = 0
        self._log_handle: Any = None

    def _expanded_command(self) -> list[str]:
        values = {
            "config": str(self.config_path),
            "workspace": str(self.ctx.options.workspace),
            "port": str(self.port),
            "base_url": self.base_url,
            "repo": str(self.ctx.options.repo_root),
            "task_id": self.ctx.task_id,
            "results": str(self.ctx.task_result_dir),
        }
        return [part.format(**values) for part in self.ctx.options.gateway_command]

    def start(self) -> None:
        if self.process is not None and self.process.poll() is None:
            raise RuntimeError("managed gateway is already running")
        command = self._expanded_command()
        self.log_path.parent.mkdir(parents=True, exist_ok=True)
        self._log_handle = self.log_path.open("ab", buffering=0)
        env = os.environ.copy()
        env["OPENKITTY_SQLITE_PATH"] = str(
            self.ctx.options.workspace / ".openkitty" / "openkitty.db"
        )
        creationflags = 0
        if os.name == "nt" and hasattr(subprocess, "CREATE_NO_WINDOW"):
            creationflags = int(subprocess.CREATE_NO_WINDOW)
        self.process = subprocess.Popen(
            command,
            cwd=self.ctx.options.workspace,
            env=env,
            stdin=subprocess.DEVNULL,
            stdout=self._log_handle,
            stderr=subprocess.STDOUT,
            creationflags=creationflags,
        )
        common.write_json(
            self.ctx.task_result_dir / f"gateway_start_{self.restart_count:02d}.json",
            {
                "time": utc_now(),
                "pid": self.process.pid,
                "command": command,
                "command_display": _command_display(command),
                "base_url": self.base_url,
                "config": str(self.config_path),
            },
        )
        self._wait_until_ready()

    def _wait_until_ready(self) -> None:
        deadline = time.monotonic() + self.ctx.options.startup_timeout_seconds
        last_error = ""
        while time.monotonic() < deadline:
            if self.process is not None and self.process.poll() is not None:
                raise RuntimeError(
                    f"gateway exited during startup with code {self.process.returncode}; "
                    f"see {self.log_path}"
                )
            try:
                status, payload, raw = common.http_request(
                    self.base_url, "GET", "/api/status", timeout=2
                )
                if status == 200 and isinstance(payload, dict):
                    project = payload.get("project")
                    actual = project.get("workspace") if isinstance(project, dict) else None
                    if isinstance(actual, str) and (
                        common.canonical_workspace(actual)
                        == common.canonical_workspace(self.ctx.options.workspace)
                    ):
                        return
                    last_error = f"workspace={actual!r} raw={raw[:200]!r}"
                else:
                    last_error = f"HTTP {status}: {raw[:200]}"
            except Exception as error:  # connection refusal is expected during boot
                last_error = f"{type(error).__name__}: {error}"
            time.sleep(self.ctx.options.poll_interval_seconds)
        # A final HTTP probe may consume the remaining startup budget while
        # the child exits. Preserve its exit status instead of masking it as
        # a connection timeout.
        if self.process is not None and self.process.poll() is not None:
            raise RuntimeError(
                f"gateway exited during startup with code {self.process.returncode}; "
                f"see {self.log_path}"
            )
        raise RuntimeError(f"gateway did not become ready: {last_error}")

    def is_running(self) -> bool:
        return self.process is not None and self.process.poll() is None

    def stop(self, *, crash: bool = False) -> None:
        process = self.process
        if process is not None and process.poll() is None:
            if crash:
                process.kill()
            else:
                process.terminate()
            try:
                process.wait(timeout=15)
            except subprocess.TimeoutExpired:
                process.kill()
                process.wait(timeout=10)
        if self._log_handle is not None:
            self._log_handle.close()
            self._log_handle = None
        self.process = None

    def restart(self) -> None:
        self.stop(crash=True)
        self.restart_count += 1
        self.start()

    def stay_alive_for(self, seconds: float) -> float:
        started = time.monotonic()
        deadline = started + seconds
        while time.monotonic() < deadline:
            if not self.is_running():
                break
            time.sleep(min(0.2, deadline - time.monotonic()))
        return max(0.0, time.monotonic() - started)

    def stdout(self) -> str:
        if self._log_handle is not None:
            self._log_handle.flush()
        if not self.log_path.exists():
            return ""
        return self.log_path.read_text(encoding="utf-8-sig", errors="replace")

    def __enter__(self) -> "ManagedGateway":
        self.start()
        return self

    def __exit__(self, *_args: object) -> None:
        self.stop()


def _runtime_config(ctx: DriverContext) -> pathlib.Path:
    source = ctx.options.gateway_config
    if source is None:
        raise DriverUnavailable("process driver requires --gateway-config")
    source = source.resolve()
    if not source.is_file():
        raise DriverUnavailable(f"gateway config does not exist: {source}")
    target = ctx.task_result_dir / "runtime" / "openkitty-config.yaml"
    target.parent.mkdir(parents=True, exist_ok=True)
    shutil.copy2(source, target)
    return target


def _chat_payload(ctx: DriverContext, session_id: str, message: str, **extra: Any) -> dict[str, Any]:
    payload: dict[str, Any] = {
        "task_type": "chat",
        "session_id": session_id,
        "message": message,
        "agent_name": ctx.task.get("agent", "Assistant"),
        "channel": ctx.defaults.get("channel", "harness100"),
        "user_id": f"{ctx.defaults.get('user_id', 'agent-harness100')}-{ctx.task_id}",
        "stream": False,
    }
    payload.update(extra)
    return payload


def _start_chat(
    ctx: DriverContext,
    base_url: str,
    session_id: str,
    message: str,
    *,
    attachments: list[dict[str, Any]] | None = None,
    label: str = "chat",
) -> tuple[str, dict[str, Any]]:
    payload = _chat_payload(ctx, session_id, message)
    if attachments:
        payload["attachments"] = attachments
    common.write_json(ctx.task_result_dir / f"{label}_request.json", payload)
    status, initial, raw = common.http_request(base_url, "POST", "/api/chat", payload, timeout=45)
    common.write_json(
        ctx.task_result_dir / f"{label}_initial.json",
        {"http_status": status, "body": initial, "raw": raw},
    )
    if status >= 400 or not isinstance(initial, dict) or not initial.get("task_id"):
        raise RuntimeError(f"chat start HTTP {status}: {raw[:500]}")
    return str(initial["task_id"]), initial


def _poll_chat(ctx: DriverContext, base_url: str, task_id: str, label: str) -> dict[str, Any]:
    final = common.poll_task(
        base_url, task_id, ctx.timeout_seconds, ctx.task_result_dir / label
    )
    common.write_json(ctx.task_result_dir / f"{label}_final.json", final)
    return final


def _result_text(payload: dict[str, Any]) -> str:
    text = common.result_text(payload.get("result"))
    return text if text.strip() else common.result_text(payload)


def _session(ctx: DriverContext, base_url: str, session_id: str, label: str) -> dict[str, Any]:
    status, payload, raw = common.http_request(
        base_url,
        "GET",
        "/api/session/" + urllib.parse.quote(session_id, safe=""),
        timeout=30,
    )
    common.write_json(
        ctx.task_result_dir / f"{label}_session.json",
        {"http_status": status, "body": payload, "raw": raw},
    )
    if status != 200 or not isinstance(payload, dict):
        raise RuntimeError(f"session read HTTP {status}: {raw[:500]}")
    return payload


def _upload(base_url: str, path: pathlib.Path) -> dict[str, Any]:
    boundary = "----openkitty-harness-" + uuid.uuid4().hex
    mime = mimetypes.guess_type(path.name)[0] or "application/octet-stream"
    head = (
        f"--{boundary}\r\n"
        f'Content-Disposition: form-data; name="file"; filename="{path.name}"\r\n'
        f"Content-Type: {mime}\r\n\r\n"
    ).encode("utf-8")
    body = head + path.read_bytes() + f"\r\n--{boundary}--\r\n".encode("ascii")
    import urllib.error
    import urllib.request

    request = urllib.request.Request(
        base_url.rstrip("/") + "/api/upload",
        data=body,
        method="POST",
        headers={"Content-Type": f"multipart/form-data; boundary={boundary}"},
    )
    try:
        with urllib.request.urlopen(request, timeout=60) as response:
            raw = response.read().decode("utf-8", errors="replace")
            payload = json.loads(raw)
            if response.status != 200 or not payload.get("success"):
                raise RuntimeError(f"upload HTTP {response.status}: {raw[:500]}")
            return payload
    except urllib.error.HTTPError as error:
        raw = error.read().decode("utf-8", errors="replace")
        raise RuntimeError(f"upload HTTP {error.code}: {raw[:500]}") from error


def _checkpoint_files(workspace: pathlib.Path) -> list[pathlib.Path]:
    root = workspace / ".openkitty" / "logs" / "transcripts"
    return sorted(root.glob("*.checkpoint.json")) if root.exists() else []


def _read_checkpoint(path: pathlib.Path) -> dict[str, Any] | None:
    try:
        value = common.read_json(path)
        return value if isinstance(value, dict) else None
    except (OSError, ValueError, json.JSONDecodeError):
        return None


def _tool_arguments(raw: Any) -> dict[str, Any]:
    if isinstance(raw, dict):
        return raw
    if isinstance(raw, str):
        try:
            decoded = json.loads(raw)
        except json.JSONDecodeError:
            return {}
        return decoded if isinstance(decoded, dict) else {}
    return {}


def _checkpoint_tool_calls(checkpoint: dict[str, Any]) -> list[dict[str, Any]]:
    """Return provider-independent tool calls in conversation order.

    OpenKitty deliberately replaces provider tool-call IDs with deterministic
    host IDs.  Harness manifest IDs (for example ``h096-write``) are therefore
    logical labels, not values a real provider can force into the response.
    Drivers identify the concrete call by tool name and target path, then keep
    both IDs in evidence.
    """

    result: list[dict[str, Any]] = []
    for message in checkpoint.get("messages") or []:
        if not isinstance(message, dict):
            continue
        for raw_call in message.get("tool_calls") or []:
            if not isinstance(raw_call, dict):
                continue
            function = raw_call.get("function")
            if not isinstance(function, dict):
                function = {}
            call_id = str(raw_call.get("id") or raw_call.get("call_id") or "")
            name = str(function.get("name") or raw_call.get("name") or "")
            arguments = _tool_arguments(
                function.get("arguments", raw_call.get("arguments"))
            )
            if call_id:
                result.append({"id": call_id, "name": name, "arguments": arguments})
    return result


def _call_path(call: dict[str, Any]) -> str:
    arguments = call.get("arguments")
    if not isinstance(arguments, dict):
        return ""
    return str(arguments.get("path") or arguments.get("file") or "").replace("\\", "/")


def _checkpoint_call_id(
    checkpoint: dict[str, Any],
    *,
    path_fragment: str,
    tool_name: str = "",
) -> str:
    fragment = path_fragment.replace("\\", "/").casefold()
    matches = [
        call
        for call in _checkpoint_tool_calls(checkpoint)
        if fragment in _call_path(call).casefold()
        and (not tool_name or str(call.get("name") or "") == tool_name)
    ]
    return str(matches[-1].get("id") or "") if matches else ""


def _pending_checkpoint_call_id(
    checkpoint: dict[str, Any],
    *,
    path_fragment: str,
    tool_name: str = "",
) -> str:
    fragment = path_fragment.replace("\\", "/").casefold()
    for raw_call in checkpoint.get("pending_tool_calls") or []:
        if not isinstance(raw_call, dict):
            continue
        call = {
            "id": str(raw_call.get("id") or ""),
            "name": str(raw_call.get("name") or ""),
            "arguments": _tool_arguments(raw_call.get("arguments")),
        }
        if (
            fragment in _call_path(call).casefold()
            and (not tool_name or call["name"] == tool_name)
        ):
            return call["id"]
    return ""


def _wait_checkpoint(
    ctx: DriverContext,
    task_id: str,
    predicate: Callable[[dict[str, Any]], bool],
    *,
    timeout: float = 90,
) -> tuple[pathlib.Path, dict[str, Any], list[dict[str, Any]]]:
    deadline = time.monotonic() + timeout
    observations: dict[int, dict[str, Any]] = {}
    while time.monotonic() < deadline:
        for path in reversed(_checkpoint_files(ctx.options.workspace)):
            value = _read_checkpoint(path)
            if not value or str(value.get("task_id") or "") != task_id:
                continue
            revision = int(value.get("revision") or 0)
            observations[revision] = {
                "revision": revision,
                "phase": value.get("phase"),
                "pending": [call.get("id") for call in value.get("pending_tool_calls") or []],
                "last_completed": value.get("last_completed_tool_call_id"),
                "path": str(path),
                "observed_at": utc_now(),
            }
            if predicate(value):
                return path, value, [observations[key] for key in sorted(observations)]
        time.sleep(ctx.options.poll_interval_seconds)
    raise TimeoutError(f"checkpoint condition not observed for task {task_id}")


def _wait_file(path: pathlib.Path, timeout: float = 60) -> None:
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        if path.exists():
            return
        time.sleep(0.1)
    raise TimeoutError(f"file was not observed: {path}")


def _recoveries(ctx: DriverContext, base_url: str, session_id: str = "") -> dict[str, Any]:
    suffix = ""
    if session_id:
        suffix = "?session_id=" + urllib.parse.quote(session_id, safe="")
    status, payload, raw = common.http_request(base_url, "GET", "/api/recoveries" + suffix, timeout=30)
    common.write_json(
        ctx.task_result_dir / f"recoveries_{int(time.time() * 1000)}.json",
        {"http_status": status, "body": payload, "raw": raw},
    )
    if status != 200 or not isinstance(payload, dict):
        raise RuntimeError(f"recoveries HTTP {status}: {raw[:500]}")
    return payload


def _wait_recovery(
    ctx: DriverContext, base_url: str, task_id: str, session_id: str
) -> dict[str, Any]:
    deadline = time.monotonic() + ctx.options.startup_timeout_seconds
    last: dict[str, Any] = {}
    while time.monotonic() < deadline:
        last = _recoveries(ctx, base_url, session_id)
        for item in last.get("recoveries") or []:
            if str(item.get("task_id") or "") == task_id:
                return item
        time.sleep(ctx.options.poll_interval_seconds)
    raise TimeoutError(f"recovery was not published for task {task_id}: {last}")


def _resume(ctx: DriverContext, base_url: str, old_task_id: str, label: str) -> dict[str, Any]:
    status, payload, raw = common.http_request(
        base_url, "POST", "/api/task/resume", {"task_id": old_task_id}, timeout=45
    )
    common.write_json(
        ctx.task_result_dir / f"{label}_resume.json",
        {"http_status": status, "body": payload, "raw": raw},
    )
    if status != 202 or not isinstance(payload, dict) or not payload.get("task_id"):
        raise RuntimeError(f"resume HTTP {status}: {raw[:500]}")
    return _poll_chat(ctx, base_url, str(payload["task_id"]), label + "_resumed")


def _sqlite_rows(workspace: pathlib.Path, sql: str, args: tuple[Any, ...] = ()) -> list[dict[str, Any]]:
    db_path = workspace / ".openkitty" / "openkitty.db"
    if not db_path.is_file():
        return []
    connection = sqlite3.connect(f"file:{db_path.as_posix()}?mode=ro", uri=True, timeout=5)
    connection.row_factory = sqlite3.Row
    try:
        return [dict(row) for row in connection.execute(sql, args).fetchall()]
    except sqlite3.Error:
        return []
    finally:
        connection.close()


def _tool_receipt_rows_for_task(
    workspace: pathlib.Path, task_id: str
) -> list[dict[str, Any]]:
    return _sqlite_rows(
        workspace,
        "SELECT id, task_id, tool_call_id, tool_name, status, read_only, started_at, completed_at "
        "FROM tool_execution_receipts WHERE task_id = ? ORDER BY id",
        (task_id,),
    )


def _tool_receipt_evidence(
    workspace: pathlib.Path,
    call_ids: list[str],
    *,
    aliases: dict[str, str] | None = None,
) -> dict[str, Any]:
    result: dict[str, Any] = {}
    aliases = aliases or {}
    for logical_call_id in call_ids:
        actual_call_id = str(aliases.get(logical_call_id) or logical_call_id)
        rows = _sqlite_rows(
            workspace,
            "SELECT task_id, tool_call_id, tool_name, status, read_only, started_at, completed_at "
            "FROM tool_execution_receipts WHERE tool_call_id = ? ORDER BY id",
            (actual_call_id,),
        )
        succeeded = [row for row in rows if str(row.get("status") or "").lower() == "succeeded"]
        result[logical_call_id] = {
            "logical_call_id": logical_call_id,
            "actual_call_id": actual_call_id,
            "execution_count": len(succeeded),
            "receipt_count": len(rows),
            "receipts": rows,
        }
    return result


def _checkpoint_tool_completion_count(
    checkpoint: dict[str, Any], actual_call_id: str
) -> int:
    """Count completed executions recorded in a persisted Turn checkpoint.

    A process can be terminated after the Turn checkpoint commits a tool result
    but before the asynchronous SQLite receipt is durable. Recovery tests must
    therefore use the checkpoint as the pre-restart source of truth, then count
    only new receipts after restart as duplicate executions.
    """

    count = 0
    for message in checkpoint.get("messages") or []:
        if not isinstance(message, dict) or str(message.get("role") or "") != "tool":
            continue
        call_id = str(message.get("tool_call_id") or message.get("call_id") or "")
        if call_id == actual_call_id:
            count += 1
    return count


def _terminal_task_persisted(workspace: pathlib.Path, task_id: str) -> bool:
    rows = _sqlite_rows(
        workspace,
        "SELECT status FROM gateway_task_records WHERE task_id = ? LIMIT 1",
        (task_id,),
    )
    if not rows:
        return False
    return str(rows[0].get("status") or "").lower() in common.TERMINAL


def _write_hook_gate(
    ctx: DriverContext,
    path_fragment: str,
    *,
    occurrence: int = 1,
) -> tuple[pathlib.Path, pathlib.Path]:
    if occurrence < 1:
        raise ValueError("hook occurrence must be at least 1")
    hooks = ctx.options.workspace / ".openkitty" / "hooks"
    hooks.mkdir(parents=True, exist_ok=True)
    gate = ctx.task_result_dir / "release-tool-hook"
    entered = ctx.task_result_dir / "tool-hook-entered"
    counter = ctx.task_result_dir / "tool-hook-count"
    # A task may be rerun into the same results directory.  A stale release
    # marker would make the crash window disappear and turn the lifecycle
    # assertion into a false pass, so every run starts with fresh gate state.
    for marker in (gate, entered, counter):
        try:
            marker.unlink()
        except FileNotFoundError:
            pass
    if os.name == "nt":
        script = hooks / "pre_tool_use.ps1"
        gate_text = str(gate).replace("'", "''")
        entered_text = str(entered).replace("'", "''")
        counter_text = str(counter).replace("'", "''")
        fragment_text = path_fragment.replace("'", "''")
        script.write_text(
            "$raw = [Console]::In.ReadToEnd()\n"
            "$ctx = $raw | ConvertFrom-Json\n"
            # Tool paths are native Windows paths while suite fragments use
            # portable forward slashes.  Normalize before matching so the
            # crash gate is actually exercised on Windows.
            "$toolPath = ([string]$ctx.params.path).Replace('\\', '/')\n"
            f"if ($toolPath -like '*{fragment_text}*') {{\n"
            "  $count = 0\n"
            f"  if (Test-Path -LiteralPath '{counter_text}') {{ $count = [int](Get-Content -LiteralPath '{counter_text}' -Raw) }}\n"
            "  $count += 1\n"
            f"  Set-Content -LiteralPath '{counter_text}' -Value $count\n"
            f"  if ($count -ge {occurrence}) {{\n"
            f"    Set-Content -LiteralPath '{entered_text}' -Value 'entered'\n"
            f"    while (-not (Test-Path -LiteralPath '{gate_text}')) {{ Start-Sleep -Milliseconds 100 }}\n"
            "  }\n"
            "}\n"
            "Write-Output '{\"allowed\":true}'\n",
            encoding="utf-8",
        )
    else:
        script = hooks / "pre_tool_use.sh"
        script.write_text(
            "#!/bin/sh\n"
            "payload=$(cat)\n"
            f"if printf '%s' \"$payload\" | grep -F -- {shlex.quote(path_fragment)} >/dev/null 2>&1; then\n"
            "  count=0\n"
            f"  if [ -f {shlex.quote(str(counter))} ]; then count=$(cat {shlex.quote(str(counter))}); fi\n"
            "  count=$((count + 1))\n"
            f"  printf '%s' \"$count\" > {shlex.quote(str(counter))}\n"
            f"  if [ \"$count\" -ge {occurrence} ]; then\n"
            f"    printf entered > {shlex.quote(str(entered))}\n"
            f"    while [ ! -f {shlex.quote(str(gate))} ]; do sleep 0.1; done\n"
            "  fi\n"
            "fi\n"
            "printf '{\"allowed\":true}'\n",
            encoding="utf-8",
        )
        script.chmod(0o755)
    return gate, entered


def _remove_hook(ctx: DriverContext) -> None:
    hooks = ctx.options.workspace / ".openkitty" / "hooks"
    for name in ("pre_tool_use.ps1", "pre_tool_use.sh", "pre_tool_use.bat"):
        try:
            (hooks / name).unlink()
        except FileNotFoundError:
            pass


def _run_config_recovery(ctx: DriverContext) -> DriverResult:
    source = ctx.options.workspace / "fixtures" / "config" / "semantic-invalid-config.yaml"
    if not source.is_file():
        raise RuntimeError(f"staged invalid config is missing: {source}")
    config = ctx.task_result_dir / "runtime" / "openkitty-config.yaml"
    config.parent.mkdir(parents=True, exist_ok=True)
    shutil.copy2(source, config)
    gateway = ManagedGateway(ctx, config)
    try:
        gateway.start()
        alive = gateway.stay_alive_for(15)
        http_evidence: dict[str, Any] = {}
        for path in ("/web", "/api/config/health"):
            status, payload, raw = common.http_request(gateway.base_url, "GET", path, timeout=20)
            http_evidence[path] = {"status": status, "body": payload, "raw": raw}
        backups = [str(path) for path in config.parent.glob(config.name + ".broken-*.bak*")]
        evidence = {
            "process": {
                "stayed_running_seconds": alive,
                "running_after_probe": gateway.is_running(),
                "stdout": gateway.stdout(),
                "backups": backups,
            },
            "http": http_evidence,
            "restart_count": 0,
        }
        return DriverResult(
            evidence=evidence,
            final_payload={"status": "completed" if gateway.is_running() else "failed"},
        )
    finally:
        gateway.stop()


def _run_restart_resume(ctx: DriverContext) -> DriverResult:
    config = _runtime_config(ctx)
    gateway = ManagedGateway(ctx, config)
    gate, entered = _write_hook_gate(ctx, "runs/A26/journal.md")
    try:
        gateway.start()
        session_id = f"harness_{ctx.task_id.lower()}_{uuid.uuid4().hex[:10]}"
        task_id, _ = _start_chat(ctx, gateway.base_url, session_id, ctx.prompt(), label="initial")
        _wait_file(entered, timeout=120)
        checkpoint_path, checkpoint, observations = _wait_checkpoint(
            ctx,
            task_id,
            lambda value: str(value.get("phase") or "") == "tools_pending"
            and bool(value.get("pending_tool_calls")),
            timeout=120,
        )
        gateway.restart()
        recovery = _wait_recovery(ctx, gateway.base_url, task_id, session_id)
        gate.write_text("release", encoding="utf-8")
        final = _resume(ctx, gateway.base_url, task_id, "resume")
        evidence = {
            "recovery_available": bool(recovery.get("can_resume", True)),
            "resume_status": str(final.get("status") or "").lower(),
            "restart_count": gateway.restart_count,
            "checkpoint": {
                "path": str(checkpoint_path),
                "last": checkpoint,
                "observations": observations,
            },
        }
        return DriverResult(
            evidence=evidence,
            final_payload=final,
            final_text=_result_text(final),
            session_id=session_id,
            turn_count=2,
        )
    finally:
        try:
            gate.write_text("release", encoding="utf-8")
        except OSError:
            pass
        gateway.stop()
        _remove_hook(ctx)


def _configured_agents(base_url: str) -> list[str]:
    status, payload, _raw = common.http_request(base_url, "GET", "/api/config/sections", timeout=30)
    if status != 200 or not isinstance(payload, dict):
        return []
    result: list[str] = []
    for value in payload.get("agents") or []:
        if isinstance(value, dict) and str(value.get("name") or "").strip():
            result.append(str(value["name"]).strip())
    return result


def _collaboration_result(payload: dict[str, Any]) -> dict[str, Any]:
    result = payload.get("result")
    return result if isinstance(result, dict) else {}


def _run_collaboration(ctx: DriverContext, *, failure_case: bool) -> DriverResult:
    base_url = ctx.options.base_url
    agents = _configured_agents(base_url)
    if not agents:
        raise DriverUnavailable("gateway exposes no configured agents for collaboration")
    chosen = [agents[index % len(agents)] for index in range(3)]
    if failure_case:
        task_specs = [
            {
                "task_id": "A",
                "agent": chosen[0],
                "description": "Read fixtures/data/sales_targets.csv and return a self-contained result for branch A.",
            },
            {
                "task_id": "B",
                "agent": "__harness_missing_agent__",
                "description": "Read fixtures/data/missing.csv; this branch must fail independently.",
            },
            {
                "task_id": "C",
                "agent": chosen[2],
                "description": "Read fixtures/data/cleaning_policy.md and return a self-contained result for branch C.",
            },
        ]
    else:
        responsibilities = [
            ("A", "商务测算", "runs/H088/branches/business.json"),
            ("B", "技术硬门槛", "runs/H088/branches/technical.json"),
            ("C", "交付风险", "runs/H088/branches/delivery.json"),
        ]
        task_specs = [
            {
                "task_id": branch,
                "agent": chosen[index],
                "description": (
                    f"Analyze only {topic}. Read fixtures/procurement and write only {path}; "
                    "do not write shared_facts.json or decision.md."
                ),
            }
            for index, (branch, topic, path) in enumerate(responsibilities)
        ]
    session_id = f"harness_{ctx.task_id.lower()}_{uuid.uuid4().hex[:10]}"
    request = {
        "task_type": "collaboration",
        "message": ctx.prompt(),
        "session_id": session_id,
        "channel": ctx.defaults.get("channel", "harness100"),
        "user_id": f"harness-{ctx.task_id}",
        "collaboration": {
            "type": "parallel",
            "tasks": task_specs,
            "max_round": 1,
            "failure_strategy": "continue",
        },
        "parameters": {"team_name": f"Harness {ctx.task_id}"},
    }
    common.write_json(ctx.task_result_dir / "collaboration_request.json", request)
    status, initial, raw = common.http_request(
        base_url, "POST", "/api/collaboration", request, timeout=45
    )
    common.write_json(
        ctx.task_result_dir / "collaboration_initial.json",
        {"http_status": status, "body": initial, "raw": raw},
    )
    if status >= 400 or not isinstance(initial, dict) or not initial.get("task_id"):
        raise RuntimeError(f"collaboration HTTP {status}: {raw[:500]}")
    collab_final = common.poll_task(
        base_url,
        str(initial["task_id"]),
        ctx.timeout_seconds,
        ctx.task_result_dir / "collaboration",
    )
    common.write_json(ctx.task_result_dir / "collaboration_final.json", collab_final)
    collab = _collaboration_result(collab_final)
    task_results = collab.get("task_results") or []

    if failure_case:
        merge_instruction = (
            "Using the immediately preceding collaboration result, write runs/H089/summary.json. "
            "It must name failed_branch B and preserve A/C as completed. HARNESS_SCENARIO=H089"
        )
    else:
        merge_instruction = (
            "Read the three branch files under runs/H088/branches. Merge them into "
            "runs/H088/shared_facts.json with top-level facts, sources, conflicts, then write "
            "runs/H088/decision.md covering 商务、技术、交付、甲云、硬门槛. HARNESS_SCENARIO=H088"
        )
    merge_task, _ = _start_chat(
        ctx, base_url, session_id, merge_instruction, label="merge"
    )
    merge_final = _poll_chat(ctx, base_url, merge_task, "merge")

    branch_status: dict[str, str] = {}
    durations: list[float] = []
    for item in task_results:
        if not isinstance(item, dict):
            continue
        branch = str(item.get("task_id") or "")
        status_value = str(item.get("status") or "").lower()
        branch_status[branch] = "completed" if status_value in {"completed", "success"} else status_value
        metrics = item.get("execution_metrics")
        if isinstance(metrics, dict):
            try:
                durations.append(float(metrics.get("duration") or 0))
            except (TypeError, ValueError):
                pass
    wall = float(collab.get("execution_duration") or 0)
    overlap = max(0.0, sum(durations) - wall)
    evidence = {
        "subagent_count": len(task_results),
        "parallel_overlap_seconds": overlap,
        "parallel_branch_duration_seconds": durations,
        "parallel_wall_seconds": wall,
        "branch_status": branch_status,
        "collaboration": collab,
    }
    return DriverResult(
        evidence=evidence,
        final_payload=merge_final,
        final_text=_result_text(merge_final),
        first_text=_result_text(collab_final),
        session_id=session_id,
        turn_count=2,
    )


def _pipeline_steps(task_id: str) -> list[dict[str, Any]]:
    if task_id == "H090":
        return [
            {
                "name": "stage1",
                "prompt": (
                    "[HARNESS_PIPELINE_STEP=H090:stage1] Read fixtures/data/sales_2026_q1.csv, "
                    "clean it and write runs/H090/stage1_clean.json. Return a concise receipt."
                ),
                "tools": ["read_file", "write_file"],
                "source_contract": {"required": True, "frozen_path": "fixtures/data/sales_2026_q1.csv"},
                "artifact_contract": {"paths": ["runs/H090/stage1_clean.json"], "min_bytes": 2},
            },
            {
                "name": "stage2",
                "prompt": (
                    "[HARNESS_PIPELINE_STEP=H090:stage2] Read only runs/H090/stage1_clean.json, "
                    "summarize regions and write runs/H090/stage2_regions.json."
                ),
                "tools": ["read_file", "write_file"],
                "source_contract": {"required": True, "frozen_path": "runs/H090/stage1_clean.json", "require_downstream_read": True},
                "artifact_contract": {"paths": ["runs/H090/stage2_regions.json"], "min_bytes": 2, "require_read": True},
            },
            {
                "name": "stage3",
                "prompt": (
                    "[HARNESS_PIPELINE_STEP=H090:stage3] Read only runs/H090/stage2_regions.json "
                    "and write runs/H090/final.md covering 华东、华南、华北、数据口径."
                ),
                "tools": ["read_file", "write_file"],
                "source_contract": {"required": True, "frozen_path": "runs/H090/stage2_regions.json", "require_downstream_read": True},
                "artifact_contract": {"paths": ["runs/H090/final.md"], "min_bytes": 20, "require_read": True},
            },
        ]
    if task_id == "H091":
        return [
            {
                "name": "stage1",
                "prompt": "[HARNESS_PIPELINE_STEP=H091:stage1] Read fixtures/data/sales_targets.csv and return a verified immutable baseline.",
                "tools": ["read_file"],
                "source_contract": {"required": True, "frozen_path": "fixtures/data/sales_targets.csv"},
            },
            {
                "name": "stage2",
                "prompt": (
                    "[HARNESS_PIPELINE_STEP=H091:stage2] On the first attempt, demonstrate the "
                    "missing.json failure, repair the task-local input, and end with RETRY_STAGE2. "
                    "On the retry, reuse stage1 and return a verified stage2 result."
                ),
                "tools": ["read_file", "write_file"],
                "repeat": {"max": 2, "condition": "contains 'RETRY_STAGE2'"},
            },
            {
                "name": "stage3",
                "prompt": (
                    "[HARNESS_PIPELINE_STEP=H091:stage3] Write runs/H091/recovery.md describing "
                    "stage2 失败、重跑 and stage1 复用."
                ),
                "tools": ["write_file"],
                "artifact_contract": {"paths": ["runs/H091/recovery.md"], "min_bytes": 20},
            },
        ]
    if task_id == "H092":
        return [
            {
                "name": "stage1",
                "prompt": (
                    "[HARNESS_PIPELINE_STEP=H092:stage1] Write runs/H092/result.json and "
                    "runs/H092/receipt.json exactly once for idempotency_key h092-fixed."
                ),
                "tools": ["write_file"],
                "artifact_contract": {
                    "paths": ["runs/H092/result.json", "runs/H092/receipt.json"],
                    "min_bytes": 2,
                },
            }
        ]
    if task_id == "H099":
        return [
            {
                "name": "stage1",
                "prompt": "[HARNESS_PIPELINE_STEP=H099:stage1] Read fixtures/article/source_notes.md and return stage1 evidence.",
                "tools": ["read_file"],
            },
            {
                "name": "stage2",
                "prompt": (
                    "[HARNESS_PIPELINE_STEP=H099:stage2] In one model turn call h099-stage2-call1 "
                    "to read fixtures/article/source_notes.md, h099-stage2-call2 to write "
                    "runs/H099/intermediate.md, and h099-stage2-call3 to read it back."
                ),
                "tools": ["read_file", "write_file"],
            },
            {
                "name": "stage3",
                "prompt": "[HARNESS_PIPELINE_STEP=H099:stage3] Write runs/H099/final.md from the verified stage2 result.",
                "tools": ["write_file"],
            },
        ]
    if task_id == "H100":
        return [
            {
                "name": "stage1",
                "prompt": (
                    "[HARNESS_PIPELINE_STEP=H100:stage1] Merge the completed business and technical "
                    "branches and write runs/H100/merged.json."
                ),
                "tools": ["read_file", "write_file"],
                "artifact_contract": {"paths": ["runs/H100/merged.json"], "min_bytes": 2},
            },
            {
                "name": "stage2",
                "prompt": (
                    "[HARNESS_PIPELINE_STEP=H100:stage2] In one turn read runs/H100/merged.json, "
                    "write runs/H100/delivered/decision.md and runs/H100/delivered/evidence.json, "
                    "then read evidence.json back. Finish by writing runs/H100/receipt.json with "
                    "host-verifiable SHA-256 values for both delivered files."
                ),
                "tools": ["read_file", "write_file"],
                "artifact_contract": {
                    "paths": [
                        "runs/H100/delivered/decision.md",
                        "runs/H100/delivered/evidence.json",
                        "runs/H100/receipt.json",
                    ],
                    "min_bytes": 2,
                },
            },
        ]
    raise RuntimeError(f"no pipeline definition for {task_id}")


def _save_pipeline(ctx: DriverContext, base_url: str, task_id: str) -> str:
    name = f"harness-{task_id.lower()}-{uuid.uuid4().hex[:10]}"
    payload = {
        "name": name,
        "description": f"Dedicated lifecycle harness pipeline for {task_id}",
        "steps": _pipeline_steps(task_id),
    }
    status, response, raw = common.http_request(base_url, "POST", "/api/pipeline/save", payload, timeout=45)
    common.write_json(
        ctx.task_result_dir / "pipeline_save.json",
        {"http_status": status, "request": payload, "body": response, "raw": raw},
    )
    if status >= 400:
        raise RuntimeError(f"pipeline save HTTP {status}: {raw[:500]}")
    return name


def _start_pipeline(
    ctx: DriverContext,
    base_url: str,
    name: str,
    *,
    label: str,
    idempotency_key: str = "",
) -> str:
    payload: dict[str, Any] = {
        "pipeline": name,
        "input": ctx.prompt(),
        "session_id": f"pipeline_{ctx.task_id.lower()}_{uuid.uuid4().hex[:8]}",
        "mode": "api",
    }
    if idempotency_key:
        payload["idempotency_key"] = idempotency_key
    status, response, raw = common.http_request(base_url, "POST", "/api/pipeline/run", payload, timeout=45)
    common.write_json(
        ctx.task_result_dir / f"{label}_pipeline_start.json",
        {"http_status": status, "request": payload, "body": response, "raw": raw},
    )
    if status >= 400 or not isinstance(response, dict) or not response.get("exec_id"):
        raise RuntimeError(f"pipeline run HTTP {status}: {raw[:500]}")
    return str(response["exec_id"])


def _pipeline_status(base_url: str, exec_id: str) -> dict[str, Any]:
    status, payload, raw = common.http_request(
        base_url,
        "GET",
        "/api/pipeline/status/" + urllib.parse.quote(exec_id, safe=""),
        timeout=30,
    )
    if status != 200 or not isinstance(payload, dict):
        raise RuntimeError(f"pipeline status HTTP {status}: {raw[:500]}")
    return payload


def _poll_pipeline(
    ctx: DriverContext, base_url: str, exec_id: str, label: str
) -> dict[str, Any]:
    deadline = time.monotonic() + ctx.timeout_seconds
    history: list[dict[str, Any]] = []
    last = ""
    while time.monotonic() < deadline:
        payload = _pipeline_status(base_url, exec_id)
        status = str(payload.get("status") or "").lower()
        if status != last or status in common.TERMINAL:
            history.append({"time": utc_now(), "status": status, "payload": payload})
            last = status
        if status in common.TERMINAL:
            common.write_json(ctx.task_result_dir / f"{label}_pipeline_history.json", history)
            common.write_json(ctx.task_result_dir / f"{label}_pipeline_final.json", payload)
            return payload
        time.sleep(max(0.2, ctx.options.poll_interval_seconds))
    raise TimeoutError(f"pipeline {exec_id} exceeded {ctx.timeout_seconds}s")


def _step_attempt_count(step: dict[str, Any], checkpoint: dict[str, Any] | None) -> int:
    if str(step.get("status") or "") == "pending":
        return 0
    count = 1 + len(step.get("revisions") or [])
    if isinstance(checkpoint, dict):
        repeat = checkpoint.get("repeat_counts")
        if isinstance(repeat, dict):
            # Callers replace this value with the index-aware count below.
            count = max(count, 1)
    return count


def _pipeline_evidence(payload: dict[str, Any], run_count: int = 1) -> dict[str, Any]:
    steps = payload.get("steps") or []
    checkpoint = payload.get("checkpoint") if isinstance(payload.get("checkpoint"), dict) else {}
    repeats = checkpoint.get("repeat_counts") if isinstance(checkpoint, dict) else {}
    stage_counts: dict[str, int] = {}
    receipts: dict[str, dict[str, str]] = {}
    for index, step in enumerate(steps):
        if not isinstance(step, dict):
            continue
        name = str(step.get("name") or f"stage{index + 1}")
        count = _step_attempt_count(step, checkpoint)
        if isinstance(repeats, dict):
            try:
                count = max(count, 1 + int(repeats.get(str(index), 0)))
            except (TypeError, ValueError):
                pass
        stage_counts[name] = count
        source = step.get("source_snapshot")
        input_hash = str(source.get("sha256") or "") if isinstance(source, dict) else ""
        output_hash = hashlib.sha256(str(step.get("output") or "").encode("utf-8")).hexdigest()
        receipts[name] = {"input_sha256": input_hash, "output_sha256": output_hash}
    return {
        "status": str(payload.get("status") or "").lower(),
        "stage_count": len(steps),
        "stage_run_counts": stage_counts,
        "stage_receipts": receipts,
        "logical_run_count": run_count,
        "payload": payload,
    }


def _run_pipeline(ctx: DriverContext, mode: str) -> DriverResult:
    base_url = ctx.options.base_url
    pipeline_name = _save_pipeline(ctx, base_url, ctx.task_id)
    if mode == "idempotency":
        with concurrent.futures.ThreadPoolExecutor(max_workers=2) as executor:
            futures = [
                executor.submit(
                    _start_pipeline,
                    ctx,
                    base_url,
                    pipeline_name,
                    label=f"run_{index}",
                    idempotency_key="h092-fixed",
                )
                for index in (1, 2)
            ]
            exec_ids = [future.result() for future in futures]
        finals = [
            _poll_pipeline(ctx, base_url, exec_id, f"run_{index}")
            for index, exec_id in enumerate(exec_ids, start=1)
        ]
        logical = len(set(exec_ids))
        final = finals[-1]
        evidence = {
            "pipeline": _pipeline_evidence(final, logical),
            "idempotency": {"h092-fixed": logical},
            "duplicate_side_effect_count": max(0, logical - 1),
            "exec_ids": exec_ids,
        }
    else:
        exec_id = _start_pipeline(ctx, base_url, pipeline_name, label="run")
        final = _poll_pipeline(ctx, base_url, exec_id, "run")
        evidence = {"pipeline": _pipeline_evidence(final), "exec_ids": [exec_id]}
    return DriverResult(
        evidence=evidence,
        final_payload={"status": final.get("status"), "result": final.get("final_output")},
        final_text=str(final.get("final_output") or ""),
        session_id=str(final.get("session_id") or ""),
        turn_count=1,
    )


def _run_concurrent_sessions(ctx: DriverContext) -> DriverResult:
    base_url = ctx.options.base_url
    suffix = uuid.uuid4().hex[:8]
    sessions = {"red": f"h093_red_{suffix}", "blue": f"h093_blue_{suffix}"}
    prompts = {
        "red": (
            "HARNESS_SCENARIO=H093 RED. Read only fixtures/data/sales_targets.csv; write only "
            "runs/H093/red/result.md. Never mention BLUE, meeting, or runs/H093/blue."
        ),
        "blue": (
            "HARNESS_SCENARIO=H093 BLUE. Read only fixtures/meeting; write only "
            "runs/H093/blue/result.md. Never mention RED, sales_targets, or runs/H093/red."
        ),
    }

    def run_one(label: str) -> tuple[str, dict[str, Any]]:
        task_id, _ = _start_chat(
            ctx, base_url, sessions[label], prompts[label], label=f"{label}_chat"
        )
        return label, _poll_chat(ctx, base_url, task_id, f"{label}_chat")

    with concurrent.futures.ThreadPoolExecutor(max_workers=2) as executor:
        completed = dict(executor.map(run_one, ("red", "blue")))
    texts = {name: _result_text(payload) for name, payload in completed.items()}
    session_payloads = {
        name: _session(ctx, base_url, session_id, name)
        for name, session_id in sessions.items()
    }
    def observable_assistant_text(label: str) -> str:
        # The user prompt deliberately names the other partition in a
        # prohibition ("Never mention BLUE").  Counting the whole session
        # would therefore manufacture cross-talk before the agent even ran.
        # Only assistant-visible output and files written to that partition
        # are valid isolation evidence.
        messages = session_payloads[label].get("messages") or []
        assistant = "\n".join(
            str(item.get("content") or "")
            for item in messages
            if isinstance(item, dict) and str(item.get("role") or "").lower() == "assistant"
        )
        root = common.safe_workspace_path(ctx.options.workspace, f"runs/H093/{label}")
        artifacts: list[str] = []
        if root.is_dir():
            for path in sorted(root.rglob("*")):
                if path.is_file():
                    try:
                        artifacts.append(path.read_text(encoding="utf-8", errors="replace"))
                    except OSError:
                        continue
        return "\n".join([texts[label], assistant, *artifacts])

    red_blob = observable_assistant_text("red")
    blue_blob = observable_assistant_text("blue")
    cross_talk = sum(
        value.casefold() in red_blob.casefold()
        for value in ("blue", "runs/h093/blue", "fixtures/meeting")
    ) + sum(
        value.casefold() in blue_blob.casefold()
        for value in ("red", "runs/h093/red", "sales_targets")
    )
    evidence = {
        "sessions": {
            name: {"status": str(payload.get("status") or "").lower(), "id": sessions[name]}
            for name, payload in completed.items()
        },
        "session_cross_talk_count": cross_talk,
        "workspace_partition": {
            path: (
                common.safe_workspace_path(ctx.options.workspace, path).is_dir()
                and any(
                    item.is_file()
                    for item in common.safe_workspace_path(ctx.options.workspace, path).rglob("*")
                )
            )
            for path in ("runs/H093/red", "runs/H093/blue")
        },
    }
    final_status = "completed" if all(
        str(payload.get("status") or "").lower() == "completed"
        for payload in completed.values()
    ) else "failed"
    return DriverResult(
        evidence=evidence,
        final_payload={"status": final_status, "result": texts},
        final_text="\n".join(texts.values()),
        turn_count=2,
    )


def _run_browser(ctx: DriverContext) -> DriverResult:
    command_template = ctx.options.browser_command
    if not command_template:
        command_template = (
            "go",
            "run",
            "./testdata/blackbox/browser_driver",
            "--base-url",
            "{base_url}",
            "--csv",
            "{csv}",
            "--png",
            "{png}",
        )
    csv_path = ctx.options.workspace / "fixtures" / "data" / "sales_targets.csv"
    png_candidates = [
        ctx.options.workspace / value for value in ctx.repo_fixtures if value.lower().endswith(".png")
    ]
    png_path = png_candidates[0] if png_candidates else ctx.options.repo_root / "winres" / "kitty.png"
    values = {
        "base_url": ctx.options.base_url,
        "workspace": str(ctx.options.workspace),
        "results": str(ctx.task_result_dir),
        "repo": str(ctx.options.repo_root),
        "csv": str(csv_path),
        "png": str(png_path),
        "task_id": ctx.task_id,
    }
    command = [part.format(**values) for part in command_template]
    completed = subprocess.run(
        command,
        cwd=ctx.options.repo_root,
        capture_output=True,
        text=True,
        encoding="utf-8",
        errors="replace",
        timeout=ctx.timeout_seconds,
    )
    (ctx.task_result_dir / "browser_stdout.log").write_text(completed.stdout, encoding="utf-8")
    (ctx.task_result_dir / "browser_stderr.log").write_text(completed.stderr, encoding="utf-8")
    if completed.returncode != 0:
        if "executable file not found" in completed.stderr.lower() or "cannot find" in completed.stderr.lower():
            raise DriverUnavailable(f"browser driver dependency unavailable: {completed.stderr[-500:]}")
        raise RuntimeError(
            f"browser driver exited {completed.returncode}: {completed.stderr[-1000:]}"
        )
    try:
        evidence = json.loads(completed.stdout)
    except json.JSONDecodeError as error:
        raise RuntimeError(f"browser driver returned invalid JSON: {completed.stdout[-1000:]}") from error
    session_id = str(evidence.get("session_id") or "")
    if session_id:
        persisted = _session(ctx, ctx.options.base_url, session_id, "browser")
        messages = persisted.get("messages") or []
        attachment_counts = [
            len(item.get("attachments") or [])
            for item in messages
            if isinstance(item, dict) and item.get("role") == "user"
        ]
        evidence["persisted_attachment_count"] = max(attachment_counts, default=0)
    return DriverResult(
        evidence=evidence,
        final_payload={"status": "completed"},
        session_id=session_id,
        turn_count=1,
    )


def _checkpoint_driver(ctx: DriverContext, scenario: str) -> DriverResult:
    config = _runtime_config(ctx)
    gateway = ManagedGateway(ctx, config)
    gate: pathlib.Path | None = None
    entered: pathlib.Path | None = None
    hook_fragment = ""
    call_ids: list[str]
    if scenario == "pre":
        hook_fragment = "runs/H095/result.txt"
        call_ids = ["h095-write"]
    elif scenario == "multi":
        # The task contract names evidence.md without imposing a runs/H096
        # directory.  Match that contract instead of a hidden driver-only
        # path so either relative or absolute tool parameters are observable.
        hook_fragment = "evidence.md"
        call_ids = ["h096-read", "h096-write", "h096-verify"]
    else:
        call_ids = ["h094-write"]
    if hook_fragment:
        # H096 needs call 2 to finish and call 3 to be pending.  Both calls
        # reference the same path, so block the second matching hook event.
        gate, entered = _write_hook_gate(
            ctx,
            hook_fragment,
            occurrence=2 if scenario == "multi" else 1,
        )
    try:
        gateway.start()
        session_id = f"harness_{ctx.task_id.lower()}_{uuid.uuid4().hex[:10]}"
        task_id, _ = _start_chat(ctx, gateway.base_url, session_id, ctx.prompt(), label="initial")
        if scenario == "pre":
            assert entered is not None
            _wait_file(entered, timeout=120)
            predicate = lambda value: bool(
                _pending_checkpoint_call_id(
                    value,
                    path_fragment="runs/H095/result.txt",
                    tool_name="write_file",
                )
            )
        elif scenario == "multi":
            assert entered is not None
            _wait_file(entered, timeout=120)
            predicate = lambda value: (
                value.get("last_completed_tool_call_id")
                == _checkpoint_call_id(
                    value,
                    path_fragment="evidence.md",
                    tool_name="write_file",
                )
                and bool(
                    _pending_checkpoint_call_id(
                        value,
                        path_fragment="evidence.md",
                        tool_name="read_file",
                    )
                )
            )
        else:
            predicate = lambda value: (
                bool(value.get("last_completed_tool_call_id"))
                and value.get("last_completed_tool_call_id")
                == _checkpoint_call_id(
                    value,
                    path_fragment="runs/H094/journal.md",
                    tool_name="write_file",
                )
            )
        checkpoint_path, checkpoint, observations = _wait_checkpoint(
            ctx, task_id, predicate, timeout=150
        )
        actual_call_ids: dict[str, str]
        if scenario == "pre":
            actual_call_ids = {
                "h095-write": _pending_checkpoint_call_id(
                    checkpoint,
                    path_fragment="runs/H095/result.txt",
                    tool_name="write_file",
                )
            }
        elif scenario == "multi":
            actual_call_ids = {
                "h096-read": _checkpoint_call_id(
                    checkpoint,
                    path_fragment="fixtures/article/source_notes.md",
                    tool_name="read_file",
                ),
                "h096-write": _checkpoint_call_id(
                    checkpoint,
                    path_fragment="evidence.md",
                    tool_name="write_file",
                ),
                "h096-verify": _pending_checkpoint_call_id(
                    checkpoint,
                    path_fragment="evidence.md",
                    tool_name="read_file",
                ),
            }
        else:
            actual_call_ids = {
                "h094-write": _checkpoint_call_id(
                    checkpoint,
                    path_fragment="runs/H094/journal.md",
                    tool_name="write_file",
                )
            }
        missing_aliases = [key for key, value in actual_call_ids.items() if not value]
        if missing_aliases:
            raise RuntimeError(
                "could not map logical tool calls to host IDs: " + ", ".join(missing_aliases)
            )
        gateway.restart()
        recovery = _wait_recovery(ctx, gateway.base_url, task_id, session_id)
        if gate is not None:
            gate.write_text("release", encoding="utf-8")
        final = _resume(ctx, gateway.base_url, task_id, "resume")
        receipts = _tool_receipt_evidence(
            ctx.options.workspace, call_ids, aliases=actual_call_ids
        )
        states: dict[str, list[str]] = {call_id: [] for call_id in call_ids}
        for observation in observations:
            for logical_call_id, actual_call_id in actual_call_ids.items():
                if (
                    actual_call_id in (observation.get("pending") or [])
                    and "pending" not in states[logical_call_id]
                ):
                    states[logical_call_id].append("pending")
                if (
                    actual_call_id == observation.get("last_completed")
                    and "completed" not in states[logical_call_id]
                ):
                    states[logical_call_id].append("completed")
        for call_id, receipt in receipts.items():
            if receipt.get("execution_count") == 1 and "completed" not in states[call_id]:
                states[call_id].append("completed")
        resume_cursor = ""
        pending_actual = str(
            (checkpoint.get("pending_tool_calls") or [{}])[0].get("id") or ""
        )
        resume_cursor = next(
            (
                logical
                for logical, actual in actual_call_ids.items()
                if actual == pending_actual
            ),
            "",
        )
        evidence = {
            "recovery_available": bool(recovery.get("can_resume", True)),
            "resume_status": str(final.get("status") or "").lower(),
            "restart_count": gateway.restart_count,
            "tool_calls": receipts,
            "checkpoint_tool_states": states,
            "resume_cursor": resume_cursor,
            "logical_call_map": actual_call_ids,
            "checkpoint": {
                "path": str(checkpoint_path),
                "crash_snapshot": checkpoint,
                "observations": observations,
            },
        }
        return DriverResult(
            evidence=evidence,
            final_payload=final,
            final_text=_result_text(final),
            session_id=session_id,
            turn_count=2,
        )
    finally:
        if gate is not None:
            try:
                gate.write_text("release", encoding="utf-8")
            except OSError:
                pass
        gateway.stop()
        _remove_hook(ctx)


def _run_attachment_restart(ctx: DriverContext) -> DriverResult:
    config = _runtime_config(ctx)
    gateway = ManagedGateway(ctx, config)
    try:
        gateway.start()
        source = ctx.options.workspace / "fixtures" / "data" / "sales_targets.csv"
        uploaded = _upload(gateway.base_url, source)
        attachment = {
            "name": uploaded.get("filename"),
            "path": uploaded.get("path"),
            "mime_type": uploaded.get("type"),
            "size": uploaded.get("size"),
            "kind": uploaded.get("kind"),
        }
        session_id = f"harness_h097_{uuid.uuid4().hex[:10]}"
        first_id, _ = _start_chat(
            ctx,
            gateway.base_url,
            session_id,
            ctx.prompt(),
            attachments=[attachment],
            label="turn_1",
        )
        first = _poll_chat(ctx, gateway.base_url, first_id, "turn_1")
        gateway.restart()
        persisted_before = _session(ctx, gateway.base_url, session_id, "after_restart")
        followup = (
            "继续，按地区总结到 runs/H097/summary.md；必须使用同一 session 已保存的附件，"
            "不要要求重新上传。 HARNESS_SCENARIO=H097"
        )
        second_id, _ = _start_chat(
            ctx, gateway.base_url, session_id, followup, label="turn_2"
        )
        second = _poll_chat(ctx, gateway.base_url, second_id, "turn_2")
        persisted_after = _session(ctx, gateway.base_url, session_id, "final")
        user_messages = [
            item
            for item in persisted_after.get("messages") or []
            if isinstance(item, dict) and item.get("role") == "user"
        ]
        attachment_count = max(
            (len(item.get("attachments") or []) for item in user_messages), default=0
        )
        evidence = {
            "attachment_available_after_restart": attachment_count,
            "session_turn_count": len(user_messages),
            "restart_count": gateway.restart_count,
            "session_after_restart": persisted_before,
        }
        return DriverResult(
            evidence=evidence,
            final_payload=second,
            final_text=_result_text(second),
            first_text=_result_text(first),
            session_id=session_id,
            turn_count=2,
        )
    finally:
        gateway.stop()


def _run_cancel_restart(ctx: DriverContext) -> DriverResult:
    config = _runtime_config(ctx)
    gateway = ManagedGateway(ctx, config)
    try:
        gateway.start()
        session_id = f"harness_h098_{uuid.uuid4().hex[:10]}"
        task_id, _ = _start_chat(ctx, gateway.base_url, session_id, ctx.prompt(), label="initial")
        _wait_checkpoint(ctx, task_id, lambda _value: True, timeout=120)
        before_rows = _tool_receipt_rows_for_task(ctx.options.workspace, task_id)
        before_ids = {str(row.get("tool_call_id") or "") for row in before_rows}
        status, stop_payload, raw = common.http_request(
            gateway.base_url,
            "POST",
            "/api/task/stop/" + urllib.parse.quote(task_id, safe=""),
            timeout=30,
        )
        common.write_json(
            ctx.task_result_dir / "stop.json",
            {"http_status": status, "body": stop_payload, "raw": raw},
        )
        gateway.restart()
        status_code, final, final_raw = common.http_request(
            gateway.base_url,
            "GET",
            "/api/task/" + urllib.parse.quote(task_id, safe=""),
            timeout=30,
        )
        if status_code != 200 or not isinstance(final, dict):
            raise RuntimeError(f"cancelled task query HTTP {status_code}: {final_raw[:500]}")
        recovery_payload = _recoveries(ctx, gateway.base_url, session_id)
        recoverable = any(
            str(item.get("task_id") or "") == task_id
            for item in recovery_payload.get("recoveries") or []
        )
        after_rows = _tool_receipt_rows_for_task(ctx.options.workspace, task_id)
        post_cancel_rows = [
            row
            for row in after_rows
            if str(row.get("tool_call_id") or "") not in before_ids
            and str(row.get("status") or "").lower() == "succeeded"
        ]
        post_cancel = len(post_cancel_rows)
        evidence = {
            "recovery_available": recoverable,
            "post_cancel_tool_call_count": post_cancel,
            "post_cancel_tool_calls": post_cancel_rows,
            "checkpoint_terminal_persisted": _terminal_task_persisted(
                ctx.options.workspace, task_id
            ),
            "restart_count": gateway.restart_count,
        }
        return DriverResult(
            evidence=evidence,
            final_payload=final,
            final_text=_result_text(final),
            session_id=session_id,
            turn_count=1,
        )
    finally:
        gateway.stop()


def _wait_pipeline_checkpoint_cursor(
    ctx: DriverContext,
    exec_id: str,
    *,
    path_fragment: str,
    tool_name: str,
    timeout: float = 180,
) -> dict[str, Any]:
    root = ctx.options.workspace / ".openkitty" / "pipeline_checkpoints"
    deadline = time.monotonic() + timeout
    last: dict[str, Any] = {}
    while time.monotonic() < deadline:
        for path in sorted(root.glob("*.json"), reverse=True) if root.exists() else []:
            value = _read_checkpoint(path)
            if not value or str(value.get("pipeline_exec_id") or "") != exec_id:
                continue
            last = value
            turn_path = str(value.get("turn_checkpoint") or "")
            if turn_path:
                turn = _read_checkpoint(pathlib.Path(turn_path))
                actual_call_id = (
                    _pending_checkpoint_call_id(
                        turn or {}, path_fragment=path_fragment, tool_name=tool_name
                    )
                    if turn
                    else ""
                )
                if actual_call_id:
                    return {
                        "pipeline": value,
                        "turn": turn,
                        "path": str(path),
                        "actual_call_id": actual_call_id,
                    }
        time.sleep(ctx.options.poll_interval_seconds)
    raise TimeoutError(
        f"pipeline checkpoint cursor {tool_name}:{path_fragment} not observed: {last}"
    )


def _run_pipeline_checkpoint(ctx: DriverContext) -> DriverResult:
    config = _runtime_config(ctx)
    gateway = ManagedGateway(ctx, config)
    # Stage2 call2 writes this path and call3 reads it.  Let the write commit,
    # then hold the read so the persisted cursor is unambiguously call3.
    gate, entered = _write_hook_gate(
        ctx, "runs/H099/intermediate.md", occurrence=2
    )
    try:
        gateway.start()
        name = _save_pipeline(ctx, gateway.base_url, "H099")
        exec_id = _start_pipeline(ctx, gateway.base_url, name, label="run")
        _wait_file(entered, timeout=180)
        checkpoint = _wait_pipeline_checkpoint_cursor(
            ctx,
            exec_id,
            path_fragment="runs/H099/intermediate.md",
            tool_name="read_file",
            timeout=180,
        )
        actual_call_ids = {
            "h099-stage2-call2": _checkpoint_call_id(
                checkpoint["turn"],
                path_fragment="runs/H099/intermediate.md",
                tool_name="write_file",
            ),
            "h099-stage2-call3": str(checkpoint.get("actual_call_id") or ""),
        }
        if any(not value for value in actual_call_ids.values()):
            raise RuntimeError(
                "could not map H099 logical tool calls to host IDs: "
                + repr(actual_call_ids)
            )
        receipt_before_restart = _tool_receipt_evidence(
            ctx.options.workspace,
            ["h099-stage2-call2"],
            aliases=actual_call_ids,
        )
        gateway.restart()
        gate.write_text("release", encoding="utf-8")
        final = _poll_pipeline(ctx, gateway.base_url, exec_id, "resumed")
        receipt = _tool_receipt_evidence(
            ctx.options.workspace,
            ["h099-stage2-call2"],
            aliases=actual_call_ids,
        )
        logical_call_id = "h099-stage2-call2"
        before_count = int(
            receipt_before_restart[logical_call_id].get("execution_count") or 0
        )
        after_count = int(receipt[logical_call_id].get("execution_count") or 0)
        checkpoint_count = _checkpoint_tool_completion_count(
            checkpoint["turn"], actual_call_ids[logical_call_id]
        )
        receipt[logical_call_id].update(
            {
                "execution_count": checkpoint_count + max(0, after_count - before_count),
                "checkpoint_completion_count": checkpoint_count,
                "receipt_count_before_restart": before_count,
                "receipt_count_after_restart": after_count,
            }
        )
        pipeline = _pipeline_evidence(final)
        evidence = {
            "pipeline": pipeline,
            "tool_calls": receipt,
            "resume_cursor": "h099-stage2-call3",
            "restart_count": gateway.restart_count,
            "checkpoint": checkpoint,
        }
        return DriverResult(
            evidence=evidence,
            final_payload={"status": final.get("status"), "result": final.get("final_output")},
            final_text=str(final.get("final_output") or ""),
            session_id=str(final.get("session_id") or ""),
            turn_count=1,
        )
    finally:
        try:
            gate.write_text("release", encoding="utf-8")
        except OSError:
            pass
        gateway.stop()
        _remove_hook(ctx)


def _run_durable_end_to_end(ctx: DriverContext) -> DriverResult:
    config = _runtime_config(ctx)
    gateway = ManagedGateway(ctx, config)
    gate, entered = _write_hook_gate(ctx, "runs/H100/delivered/evidence.json")
    try:
        gateway.start()
        agents = _configured_agents(gateway.base_url)
        if not agents:
            raise DriverUnavailable("gateway exposes no agents for H100 collaboration")
        collab_request = {
            "task_type": "collaboration",
            "message": ctx.prompt(),
            "session_id": f"h100_collab_{uuid.uuid4().hex[:8]}",
            "channel": "harness100",
            "user_id": "harness-H100",
            "collaboration": {
                "type": "parallel",
                "tasks": [
                    {"task_id": "business", "agent": agents[0], "description": "Analyze procurement commercial facts and write runs/H100/business.json only."},
                    {"task_id": "technical", "agent": agents[-1], "description": "Analyze procurement technical gates and write runs/H100/technical.json only."},
                ],
                "max_round": 1,
                "failure_strategy": "continue",
            },
            "parameters": {"team_name": "Harness H100"},
        }
        status, initial, raw = common.http_request(
            gateway.base_url, "POST", "/api/collaboration", collab_request, timeout=45
        )
        if status >= 400 or not isinstance(initial, dict) or not initial.get("task_id"):
            raise RuntimeError(f"H100 collaboration HTTP {status}: {raw[:500]}")
        collab_final = common.poll_task(
            gateway.base_url,
            str(initial["task_id"]),
            ctx.timeout_seconds,
            ctx.task_result_dir / "collaboration",
        )
        collab = _collaboration_result(collab_final)
        gateway.restart()  # first lifecycle boundary: completed branch state remains durable

        name = _save_pipeline(ctx, gateway.base_url, "H100")
        exec_id = _start_pipeline(ctx, gateway.base_url, name, label="pipeline")
        _wait_file(entered, timeout=180)
        gateway.restart()  # second boundary: inside stage2's tool sequence
        gate.write_text("release", encoding="utf-8")
        final = _poll_pipeline(ctx, gateway.base_url, exec_id, "pipeline_resumed")
        call_ids: list[str] = []
        for step in final.get("steps") or []:
            for call in step.get("tool_calls") or []:
                call_id = str(call.get("call_id") or "")
                if call_id:
                    call_ids.append(call_id)
        receipts = _tool_receipt_evidence(ctx.options.workspace, sorted(set(call_ids)))
        duplicate_count = sum(
            max(0, int(item.get("receipt_count") or 0) - 1) for item in receipts.values()
        )
        evidence = {
            "subagent_count": len(collab.get("task_results") or []),
            "pipeline": _pipeline_evidence(final),
            "tool_calls": receipts,
            "duplicate_tool_call_count": duplicate_count,
            "restart_count": gateway.restart_count,
        }
        return DriverResult(
            evidence=evidence,
            final_payload={"status": final.get("status"), "result": final.get("final_output")},
            final_text=str(final.get("final_output") or ""),
            turn_count=2,
        )
    finally:
        try:
            gate.write_text("release", encoding="utf-8")
        except OSError:
            pass
        gateway.stop()
        _remove_hook(ctx)


Runner = Callable[[DriverContext], DriverResult]


RUNNERS: dict[str, Runner] = {
    "process_config_recovery": _run_config_recovery,
    "process_restart_resume": _run_restart_resume,
    "browser_attachments": _run_browser,
    "multi_agent_collaboration": lambda ctx: _run_collaboration(ctx, failure_case=False),
    "multi_agent_failure_isolation": lambda ctx: _run_collaboration(ctx, failure_case=True),
    "pipeline_e2e": lambda ctx: _run_pipeline(ctx, "e2e"),
    "pipeline_retry_failed_stage": lambda ctx: _run_pipeline(ctx, "retry"),
    "pipeline_idempotency": lambda ctx: _run_pipeline(ctx, "idempotency"),
    "concurrent_session_isolation": _run_concurrent_sessions,
    "turn_checkpoint_post_tool_crash": lambda ctx: _checkpoint_driver(ctx, "post"),
    "turn_checkpoint_pre_tool_crash": lambda ctx: _checkpoint_driver(ctx, "pre"),
    "turn_checkpoint_multi_tool_resume": lambda ctx: _checkpoint_driver(ctx, "multi"),
    "session_attachment_restart": _run_attachment_restart,
    "cancel_restart_race": _run_cancel_restart,
    "pipeline_turn_checkpoint_resume": _run_pipeline_checkpoint,
    "durable_end_to_end": _run_durable_end_to_end,
}


def validate_special_task(task: dict[str, Any]) -> None:
    runner = str(task.get("runner") or "chat")
    if runner not in RUNNERS:
        raise ValueError(f"task {task.get('id')} has no dedicated driver for runner {runner!r}")
    unsupported = sorted(
        {str(check.get("type") or "") for check in task.get("acceptance", {}).get("checks", [])}
        - SUPPORTED_CHECKS
    )
    if unsupported:
        raise ValueError(f"task {task.get('id')} uses unsupported dedicated checks: {unsupported}")


def requires_external_gateway(task: dict[str, Any]) -> bool:
    return str(task.get("runner") or "") not in PROCESS_RUNNERS


def _evidence_value(evidence: dict[str, Any], *path: str, default: Any = None) -> Any:
    value: Any = evidence
    for key in path:
        if not isinstance(value, dict) or key not in value:
            return default
        value = value[key]
    return value


def evaluate_special_check(
    check: dict[str, Any],
    ctx: DriverContext,
    run: DriverResult,
) -> dict[str, Any]:
    kind = str(check["type"])
    if kind in STANDARD_CHECKS:
        return common.evaluate_check(
            check,
            ctx.options.workspace,
            run.final_payload,
            run.final_text,
            run.progress_text,
            run.first_text,
            run.first_glob_counts,
        )
    passed = False
    detail = ""
    evidence = run.evidence
    try:
        if kind == "process_exit":
            actual = float(_evidence_value(evidence, "process", "stayed_running_seconds", default=0))
            minimum = float(check["must_stay_running_seconds"])
            running = bool(_evidence_value(evidence, "process", "running_after_probe", default=False))
            passed = running and actual >= minimum - 0.25
            detail = f"running={running} actual={actual:.2f}s minimum={minimum:.2f}s"
        elif kind == "stdout_contains_all":
            stdout = str(_evidence_value(evidence, "process", "stdout", default=""))
            passed, detail = common.contains_all(stdout, list(check["values"]))
        elif kind == "backup_created":
            backups = _evidence_value(evidence, "process", "backups", default=[])
            passed = isinstance(backups, list) and len(backups) > 0
            detail = f"backups={backups!r}"
        elif kind in {"http_status", "http_json_contains"}:
            observed = _evidence_value(evidence, "http", str(check["path"]), default={})
            if kind == "http_status":
                actual = int(observed.get("status", 0)) if isinstance(observed, dict) else 0
                passed = actual == int(check["status"])
                detail = f"actual={actual} expected={check['status']}"
            else:
                blob = common.result_text(observed.get("body")) if isinstance(observed, dict) else ""
                passed, detail = common.contains_all(blob, list(check["values"]))
        elif kind == "recovery_available":
            actual = bool(evidence.get("recovery_available", False))
            passed = actual is bool(check["value"])
            detail = f"actual={actual} expected={check['value']}"
        elif kind == "resume_status":
            actual = str(evidence.get("resume_status") or "").lower()
            expected = [str(value).lower() for value in check["one_of"]]
            passed = actual in expected
            detail = f"actual={actual!r} expected={expected!r}"
        elif kind in {
            "same_bubble_attachment_count",
            "request_attachment_count",
            "persisted_attachment_count",
            "console_error_count",
            "attachment_available_after_restart",
            "session_turn_count",
            "post_cancel_tool_call_count",
            "duplicate_side_effect_count",
            "duplicate_tool_call_count",
        }:
            actual = int(evidence.get(kind, 0))
            expected = int(check["count"])
            passed = actual == expected
            detail = f"actual={actual} expected={expected}"
        elif kind == "refresh_preserves_attachments":
            actual = bool(evidence.get(kind, False))
            passed = actual is bool(check["value"])
            detail = f"actual={actual} expected={check['value']}"
        elif kind == "mobile_overflow_x":
            actual = float(evidence.get(kind, float("inf")))
            maximum = float(check["max"])
            passed = actual <= maximum
            detail = f"actual={actual} maximum={maximum}"
        elif kind == "subagent_count":
            actual = int(evidence.get("subagent_count", 0))
            minimum = int(check["min"])
            passed = actual >= minimum
            detail = f"actual={actual} minimum={minimum}"
        elif kind == "parallel_overlap":
            actual = float(evidence.get("parallel_overlap_seconds", 0))
            minimum = float(check["min_seconds"])
            passed = actual >= minimum
            detail = f"actual={actual:.3f}s minimum={minimum:.3f}s"
        elif kind == "branch_status":
            actual = str(_evidence_value(evidence, "branch_status", str(check["branch"]), default="")).lower()
            expected = [str(value).lower() for value in check["one_of"]]
            passed = actual in expected
            detail = f"branch={check['branch']} actual={actual!r} expected={expected!r}"
        elif kind == "pipeline_status":
            actual = str(_evidence_value(evidence, "pipeline", "status", default="")).lower()
            expected = [str(value).lower() for value in check["one_of"]]
            passed = actual in expected
            detail = f"actual={actual!r} expected={expected!r}"
        elif kind == "pipeline_stage_count":
            actual = int(_evidence_value(evidence, "pipeline", "stage_count", default=0))
            expected = int(check["count"])
            passed = actual == expected
            detail = f"actual={actual} expected={expected}"
        elif kind == "stage_receipt_hashes":
            receipts = _evidence_value(evidence, "pipeline", "stage_receipts", default={})
            problems = []
            for stage in check["stages"]:
                item = receipts.get(stage) if isinstance(receipts, dict) else None
                if not isinstance(item, dict) or not re.fullmatch(r"[0-9a-f]{64}", str(item.get("input_sha256") or "")):
                    problems.append(f"{stage}.input_sha256")
                if not isinstance(item, dict) or not re.fullmatch(r"[0-9a-f]{64}", str(item.get("output_sha256") or "")):
                    problems.append(f"{stage}.output_sha256")
            passed = not problems
            detail = "missing_or_invalid=" + repr(problems)
        elif kind == "stage_run_count":
            actual = int(_evidence_value(evidence, "pipeline", "stage_run_counts", str(check["stage"]), default=0))
            expected = int(check["count"])
            passed = actual == expected
            detail = f"stage={check['stage']} actual={actual} expected={expected}"
        elif kind == "idempotent_run_count":
            actual = int(_evidence_value(evidence, "idempotency", str(check["key"]), default=0))
            expected = int(check["count"])
            passed = actual == expected
            detail = f"key={check['key']} actual={actual} expected={expected}"
        elif kind == "session_status":
            actual = str(_evidence_value(evidence, "sessions", str(check["session"]), "status", default="")).lower()
            expected = [str(value).lower() for value in check["one_of"]]
            passed = actual in expected
            detail = f"session={check['session']} actual={actual!r} expected={expected!r}"
        elif kind == "session_cross_talk_count":
            actual = int(evidence.get("session_cross_talk_count", 0))
            expected = int(check["count"])
            passed = actual == expected
            detail = f"actual={actual} expected={expected}"
        elif kind == "workspace_partition_intact":
            partition = evidence.get("workspace_partition")
            missing = [path for path in check["paths"] if not isinstance(partition, dict) or not partition.get(path)]
            passed = not missing
            detail = f"missing={missing!r}"
        elif kind == "tool_call_execution_count":
            actual = int(_evidence_value(evidence, "tool_calls", str(check["call_id"]), "execution_count", default=0))
            expected = int(check["count"])
            passed = actual == expected
            detail = f"call_id={check['call_id']} actual={actual} expected={expected}"
        elif kind == "checkpoint_tool_state":
            actual = _evidence_value(evidence, "checkpoint_tool_states", str(check["call_id"]), default=[])
            expected = list(check["observed"])
            missing = [value for value in expected if value not in actual]
            passed = not missing
            detail = f"actual={actual!r} missing={missing!r}"
        elif kind == "resume_cursor":
            actual = str(evidence.get("resume_cursor") or "")
            passed = actual == str(check["call_id"])
            detail = f"actual={actual!r} expected={check['call_id']!r}"
        elif kind == "checkpoint_terminal_persisted":
            actual = bool(evidence.get(kind, False))
            passed = actual is bool(check["value"])
            detail = f"actual={actual} expected={check['value']}"
        elif kind == "restart_count":
            actual = int(evidence.get("restart_count", 0))
            expected = int(check["count"])
            passed = actual == expected
            detail = f"actual={actual} expected={expected}"
        else:
            detail = "unsupported dedicated check"
    except Exception as error:
        passed = False
        detail = f"{type(error).__name__}: {error}"
    return {"type": kind, "passed": passed, "detail": detail, "check": check}


def run_task(
    task: dict[str, Any],
    defaults: dict[str, Any],
    options: DriverOptions,
) -> dict[str, Any]:
    validate_special_task(task)
    task_id = str(task["id"])
    task_result_dir = options.results / task_id
    task_result_dir.mkdir(parents=True, exist_ok=True)
    run_dir = options.workspace / "runs" / task_id
    if run_dir.exists() and any(run_dir.iterdir()):
        raise RuntimeError(f"refusing to reuse non-empty task directory: {run_dir}")
    repo_fixtures = common.stage_repo_fixtures(task, options.workspace)
    ctx = DriverContext(
        task=task,
        defaults=defaults,
        options=options,
        task_result_dir=task_result_dir,
        run_dir=run_dir,
        repo_fixtures=repo_fixtures,
    )
    runner_name = str(task["runner"])
    common.log(f"[{task_id}] START dedicated runner={runner_name} {task['title']}")
    try:
        run = RUNNERS[runner_name](ctx)
    except DriverUnavailable as error:
        outcome = {
            "id": task_id,
            "title": task["title"],
            "category": task["category"],
            "runner": runner_name,
            "outcome": "driver_unavailable",
            "reason": str(error),
        }
        common.write_json(task_result_dir / "verdict.json", outcome)
        common.log(f"[{task_id}] DRIVER UNAVAILABLE {error}")
        return outcome

    common.write_json(task_result_dir / "evidence.json", run.evidence)
    checks = [evaluate_special_check(check, ctx, run) for check in task["acceptance"]["checks"]]
    passed = bool(checks) and all(check["passed"] for check in checks)
    outcome = {
        "id": task_id,
        "title": task["title"],
        "category": task["category"],
        "runner": runner_name,
        "outcome": "passed" if passed else "failed",
        "terminal_status": run.final_payload.get("status"),
        "checks": checks,
        "turn_count": run.turn_count,
        "session_id": run.session_id,
        "evidence_file": str(task_result_dir / "evidence.json"),
    }
    common.write_json(task_result_dir / "verdict.json", outcome)
    failures = sum(1 for check in checks if not check["passed"])
    common.log(
        f"[{task_id}] {'PASS' if passed else 'FAIL'} dedicated "
        f"status={run.final_payload.get('status')} failed_checks={failures}/{len(checks)}"
    )
    return outcome


def parse_command_json(raw: str, option_name: str) -> tuple[str, ...]:
    if not raw.strip():
        return ()
    try:
        value = json.loads(raw)
    except json.JSONDecodeError as error:
        raise ValueError(f"{option_name} must be a JSON string array: {error}") from error
    if not isinstance(value, list) or not value or not all(isinstance(item, str) and item for item in value):
        raise ValueError(f"{option_name} must be a non-empty JSON string array")
    return tuple(value)


def all_runner_names() -> set[str]:
    return set(RUNNERS)
