#!/usr/bin/env python3
"""Deterministic OpenAI-compatible server for OpenKitty process black-box tests."""

from __future__ import annotations

import argparse
import json
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path


class Scenario:
    def __init__(self, log_path: Path) -> None:
        self._lock = threading.Lock()
        self._agent_step = 0
        self._log_path = log_path

    def record(self, payload: dict) -> None:
        entry = {
            "time": time.time(),
            "message_count": len(payload.get("messages") or []),
            "tool_definition_count": len(payload.get("tools") or []),
            "model": payload.get("model", ""),
        }
        with self._log_path.open("a", encoding="utf-8") as handle:
            handle.write(json.dumps(entry, ensure_ascii=False) + "\n")

    def next_message(self, payload: dict) -> tuple[dict, str]:
        # Auxiliary judge/compression calls do not expose tools. Keep them
        # deterministic without consuming the scripted agent turn sequence.
        if not payload.get("tools"):
            return {"role": "assistant", "content": "PASS"}, "stop"

        with self._lock:
            self._agent_step += 1
            step = self._agent_step

        calls = {
            1: (
                "write_file",
                {
                    "path": "blackbox/long_task_notes.md",
                    "content": "# Long Task Notes\n\nphase: collected\nitems: 3\n",
                },
            ),
            # This intentionally fails. A capable agent should consume the
            # structured edit recovery guidance and inspect the current file.
            2: (
                "edit_file",
                {
                    "path": "blackbox/long_task_notes.md",
                    "old_string": "phase: missing-old-value",
                    "new_string": "phase: analyzed",
                },
            ),
            3: ("read_file", {"path": "blackbox/long_task_notes.md"}),
            4: (
                "edit_file",
                {
                    "path": "blackbox/long_task_notes.md",
                    "old_string": "phase: collected\nitems: 3",
                    "new_string": "phase: analyzed\nitems: 3\nrecovered_from_edit_mismatch: true",
                },
            ),
            5: (
                "write_file",
                {
                    "path": "blackbox/final_report.md",
                    "content": (
                        "# Black-box Long Task Report\n\n"
                        "- collected_items: 3\n"
                        "- edit_recovery: verified\n"
                        "- status: complete\n"
                    ),
                },
            ),
            6: ("read_file", {"path": "blackbox/final_report.md"}),
        }
        if step in calls:
            name, arguments = calls[step]
            return {
                "role": "assistant",
                "content": "",
                "tool_calls": [
                    {
                        "id": f"call_blackbox_{step}",
                        "type": "function",
                        "function": {
                            "name": name,
                            "arguments": json.dumps(arguments, ensure_ascii=False),
                        },
                    }
                ],
            }, "tool_calls"

        return {
            "role": "assistant",
            "content": (
                "长任务已完成。已生成并回读验证 blackbox/final_report.md；"
                "首次 edit_file 因 old_string 不匹配失败后，已读取当前文件并使用精确内容重试成功。"
            ),
        }, "stop"


class Handler(BaseHTTPRequestHandler):
    scenario: Scenario

    def log_message(self, _format: str, *_args: object) -> None:
        return

    def do_GET(self) -> None:  # noqa: N802
        self._send_json(200, {"ok": True})

    def do_POST(self) -> None:  # noqa: N802
        try:
            length = int(self.headers.get("Content-Length", "0"))
            payload = json.loads(self.rfile.read(length) or b"{}")
        except (ValueError, json.JSONDecodeError) as error:
            self._send_json(400, {"error": {"message": str(error)}})
            return

        self.scenario.record(payload)
        message, finish_reason = self.scenario.next_message(payload)
        self._send_json(
            200,
            {
                "id": "chatcmpl-openkitty-blackbox",
                "object": "chat.completion",
                "created": int(time.time()),
                "model": payload.get("model", "blackbox-model"),
                "choices": [
                    {
                        "index": 0,
                        "message": message,
                        "finish_reason": finish_reason,
                    }
                ],
                "usage": {
                    "prompt_tokens": 128,
                    "completion_tokens": 32,
                    "total_tokens": 160,
                },
            },
        )

    def _send_json(self, status: int, payload: dict) -> None:
        body = json.dumps(payload, ensure_ascii=False).encode("utf-8")
        self.send_response(status)
        self.send_header("Content-Type", "application/json; charset=utf-8")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--port", type=int, required=True)
    parser.add_argument("--log", type=Path, required=True)
    args = parser.parse_args()
    args.log.parent.mkdir(parents=True, exist_ok=True)
    Handler.scenario = Scenario(args.log)
    ThreadingHTTPServer(("127.0.0.1", args.port), Handler).serve_forever()


if __name__ == "__main__":
    main()
