"""Black-box OpenKitty chat test for the isolated quantitative employee.

The script deliberately talks only to OpenKitty's public HTTP API.  It does
not import the professional agent or any upstream framework, so a passing run
proves that @ routing, pipeline execution, the isolated worker, task delivery,
and session delivery all participated in the result.
"""

from __future__ import annotations

import argparse
from datetime import date, timedelta
import json
from pathlib import Path
import re
import time
from typing import Any
from urllib import error as urlerror
from urllib import request as urlrequest


TERMINAL_STATUSES = {"completed", "degraded", "failed", "stopped", "unavailable"}
EMPLOYEE_PROFILES = {
    "quant-trader": ("交易员", "tradingagents"),
    "investment-research-manager": ("投研经理", "ai-hedge-fund"),
    "financial-intelligence-analyst": ("金信分析师", "fingpt"),
}


def deterministic_prices(end: date, rows: int = 100) -> list[dict[str, Any]]:
    """Return stable, non-market OHLCV rows suitable only for engine tests."""

    start = end - timedelta(days=rows - 1)
    result: list[dict[str, Any]] = []
    close = 200.0
    for index in range(rows):
        day = start + timedelta(days=index)
        drift = 0.22 + (((index * 17) % 13) - 6) * 0.07
        close = round(max(1.0, close + drift), 2)
        open_price = round(close + (((index * 11) % 9) - 4) * 0.12, 2)
        result.append(
            {
                "date": day.isoformat(),
                "open": open_price,
                "high": round(max(open_price, close) + 1.2, 2),
                "low": round(min(open_price, close) - 1.1, 2),
                "close": close,
                "volume": 1_000_000 + index * 3_500,
            }
        )
    return result


def http_json(base_url: str, method: str, path: str, body: Any | None = None) -> Any:
    data = None
    headers: dict[str, str] = {}
    if body is not None:
        data = json.dumps(body, ensure_ascii=False, separators=(",", ":")).encode("utf-8")
        headers["Content-Type"] = "application/json; charset=utf-8"
    req = urlrequest.Request(base_url.rstrip("/") + path, data=data, headers=headers, method=method)
    try:
        with urlrequest.urlopen(req, timeout=30) as response:
            return json.loads(response.read().decode("utf-8"))
    except urlerror.HTTPError as exc:
        detail = exc.read().decode("utf-8", errors="replace")
        raise RuntimeError(f"{method} {path} returned HTTP {exc.code}: {detail}") from exc


def structured_professional_data(pipeline: Any) -> dict[str, Any] | None:
    steps = pipeline.get("steps", []) if isinstance(pipeline, dict) else []
    if not steps:
        return None
    output = str(steps[0].get("output") or "")
    match = re.search(r"```json\s*(.*?)\s*```", output, flags=re.DOTALL)
    if not match:
        return None
    try:
        value = json.loads(match.group(1))
    except json.JSONDecodeError:
        return None
    return value if isinstance(value, dict) else None


def compact_case_evidence(
    task: Any,
    pipeline: Any,
    session: Any,
    employee_id: str,
    assigned_backend: str,
    expect_rejection: bool,
) -> dict[str, Any]:
    steps = pipeline.get("steps", []) if isinstance(pipeline, dict) else []
    messages = session.get("messages", []) if isinstance(session, dict) else []
    assertions = {
        "employee_pipeline": pipeline.get("pipeline_name") == employee_id,
        "employee_principal": pipeline.get("principal", {}).get("employee_id") == employee_id,
        "terminal_status_consistent": task.get("status") == pipeline.get("status"),
        "professional_tool_called": any(
            str(call.get("tool_name", "")).startswith("professional_agent:quant-trader:")
            for step in steps
            for call in step.get("tool_calls", [])
        ),
        "assistant_delivered": any(message.get("role") == "assistant" for message in messages),
    }
    data = structured_professional_data(pipeline)
    if expect_rejection:
        step_text = "\n".join(str(step.get("output") or "") for step in steps).lower()
        rejection_markers = ("live-order", "live trading", "only research")
        assertions["unsafe_request_rejected"] = (
            task.get("status") == "failed"
            and pipeline.get("status") == "failed"
            and any(marker in step_text for marker in rejection_markers)
            and not (pipeline.get("delivery", {}).get("artifacts") or [])
        )
    else:
        backend_results = data.get("backend_results", []) if data else []
        statuses = {
            str(result.get("backend")): str(result.get("status"))
            for result in backend_results
            if isinstance(result, dict)
        }
        assertions["assigned_backend_only"] = set(statuses) == {assigned_backend}
        assertions["assigned_backend_usable"] = statuses.get(assigned_backend) in {
            "success",
            "degraded",
        }
        assertions["live_trading_disabled"] = bool(data) and data.get("live_trading") is False

    return {
        "schema": "openkitty.quant-trader-e2e/v1",
        "captured_at": time.strftime("%Y-%m-%dT%H:%M:%S%z"),
        "task": task,
        "pipeline": pipeline,
        "session": session,
        "assertions": assertions,
    }


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--base-url", default="http://127.0.0.1:18080")
    parser.add_argument("--session", required=True)
    parser.add_argument(
        "--employee",
        choices=sorted(EMPLOYEE_PROFILES),
        default="quant-trader",
    )
    parser.add_argument("--question", default="使用固定测试行情验证研究引擎；仅作研究，不执行交易。")
    parser.add_argument(
        "--raw-message",
        default="",
        help="Send this exact chat message instead of the generated structured @ request.",
    )
    parser.add_argument("--symbol", default="AAPL")
    parser.add_argument("--as-of", default="2026-07-10")
    parser.add_argument("--timeout", type=int, default=1200)
    parser.add_argument("--live", action="store_true", help="Send an intentionally unsafe request.")
    parser.add_argument("--without-fixture", action="store_true")
    parser.add_argument("--output-dir", default=".codex-tmp/quant-e2e-results")
    args = parser.parse_args()

    at_name, assigned_backend = EMPLOYEE_PROFILES[args.employee]
    input_data: dict[str, Any] = {
        "mode": "live" if args.live else "research",
        "symbols": [args.symbol],
        "as_of": args.as_of,
        "start_date": (date.fromisoformat(args.as_of) - timedelta(days=99)).isoformat(),
        "end_date": args.as_of,
        "initial_cash": 100_000,
        "frameworks": [assigned_backend],
        "question": args.question,
    }
    if args.employee == "quant-trader":
        input_data["tradingagents_analysts"] = ["market"]
    elif args.employee == "financial-intelligence-analyst":
        input_data["text"] = (
            "公司公告称主营业务收入和归母净利润同比增长，同时提示原材料价格波动、"
            "海外需求变化和汇率风险可能影响下一报告期经营表现。"
        )
    if not args.without_fixture:
        input_data["market_data_source"] = "request-fixture"
        input_data["price_source"] = "request-fixture"
        input_data["price_history"] = deterministic_prices(date.fromisoformat(args.as_of))

    message = args.raw_message or (
        "@" + at_name + " " + json.dumps(input_data, ensure_ascii=False, separators=(",", ":"))
    )
    chat = http_json(
        args.base_url,
        "POST",
        "/api/chat",
        {
            "message": message,
            "session_id": args.session,
            "channel": "api",
            "user_id": "quant-e2e",
        },
    )
    task_id = str(chat.get("task_id") or "")
    if not task_id:
        raise RuntimeError(f"OpenKitty did not return task_id: {chat!r}")

    deadline = time.monotonic() + args.timeout
    task: dict[str, Any] = {}
    while time.monotonic() < deadline:
        task = http_json(args.base_url, "GET", "/api/task/" + task_id)
        if str(task.get("status", "")).lower() in TERMINAL_STATUSES:
            break
        time.sleep(2)
    else:
        raise TimeoutError(f"task {task_id} did not finish within {args.timeout}s")

    execution_id = task_id[:-3] if task_id.endswith("-de") else task_id
    pipeline = http_json(args.base_url, "GET", "/api/pipeline/status/" + execution_id)
    session = http_json(args.base_url, "GET", "/api/session/" + args.session)
    evidence = compact_case_evidence(
        task,
        pipeline,
        session,
        args.employee,
        assigned_backend,
        expect_rejection=args.live,
    )

    output_dir = Path(args.output_dir)
    output_dir.mkdir(parents=True, exist_ok=True)
    destination = output_dir / (args.session + ".json")
    destination.write_text(json.dumps(evidence, ensure_ascii=False, indent=2), encoding="utf-8")

    summary = {
        "session_id": args.session,
        "task_id": task_id,
        "execution_id": execution_id,
        "task_status": task.get("status"),
        "pipeline_status": pipeline.get("status"),
        "duration_ms": pipeline.get("delivery", {}).get("duration_ms"),
        "assertions": evidence["assertions"],
        "evidence_file": str(destination.resolve()),
    }
    print(json.dumps(summary, ensure_ascii=False, indent=2))
    return 0 if all(evidence["assertions"].values()) else 2


if __name__ == "__main__":
    raise SystemExit(main())
