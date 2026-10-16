#!/usr/bin/env python3
"""Compatibility entrypoint; OpenKitty invokes worker.py from the manifest."""

from __future__ import annotations

import json
import os
import sys
from typing import Any, Dict

from professional_agent import AGENT_ID, PROTOCOL, QuantProfessionalAgent, parse_request_line


def _max_request_bytes() -> int:
    try:
        value = int(os.environ.get("QUANT_TRADER_MAX_REQUEST_BYTES", "1048576"))
    except ValueError:
        value = 1048576
    return max(1024, min(value, 16 * 1024 * 1024))


def _write(response: Dict[str, Any]) -> None:
    # Keep the NDJSON wire encoding deterministic even when Windows launches
    # Python with a legacy console code page. JSON escapes decode back to the
    # original Unicode employee/profile names at the protocol peer.
    sys.stdout.write(json.dumps(response, ensure_ascii=True, separators=(",", ":")) + "\n")
    sys.stdout.flush()


def main() -> int:
    try:
        agent = QuantProfessionalAgent()
    except Exception as exc:  # startup must still emit protocol-safe output
        _write(
            {
                "protocol": PROTOCOL,
                "request_id": "",
                "agent_id": AGENT_ID,
                "status": "failed",
                "output": "",
                "data": {},
                "warnings": ["quant-trader startup failed: %s" % exc],
                "artifacts": [],
                "error": "quant-trader startup failed: %s" % exc,
            }
        )
        return 1

    max_bytes = _max_request_bytes()
    for raw_line in sys.stdin:
        line = raw_line.strip()
        if not line:
            continue
        request, parse_error = parse_request_line(line, max_bytes)
        if parse_error:
            _write(
                {
                    "protocol": PROTOCOL,
                    "request_id": "",
                    "agent_id": AGENT_ID,
                    "status": "failed",
                    "output": "",
                    "data": {},
                    "warnings": [parse_error],
                    "artifacts": [],
                    "error": parse_error,
                }
            )
            continue
        try:
            response = agent.handle(request)
        except Exception as exc:
            request_id = request.get("request_id", "") if isinstance(request, dict) else ""
            response = {
                "protocol": PROTOCOL,
                "request_id": str(request_id),
                "agent_id": AGENT_ID,
                "status": "failed",
                "output": "",
                "data": {},
                "warnings": ["Unhandled request error: %s" % exc],
                "artifacts": [],
                "error": "Unhandled request error: %s" % exc,
            }
        _write(response)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
