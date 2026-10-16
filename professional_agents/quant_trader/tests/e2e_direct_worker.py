#!/usr/bin/env python3
"""Black-box the isolated quant-trader worker without an OpenKitty server.

The harness loads one explicitly selected model profile into agent-scoped
environment variables, launches ``worker.py`` over NDJSON, validates the one
backend authorized for the selected employee, and writes a credential-free evidence file. Secret values
are never printed or persisted.
"""

from __future__ import annotations

import argparse
from datetime import datetime, timezone
import json
import os
from pathlib import Path
import subprocess
import sys
import time
from typing import Any, Dict, Mapping

import yaml


PROTOCOL = "openkitty.professional-agent/v1"
EMPLOYEE_PROFILES = {
    "quant-trader": ("trading", "tradingagents"),
    "investment-research-manager": ("research-management", "ai-hedge-fund"),
    "financial-intelligence-analyst": ("financial-intelligence", "fingpt"),
}


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser()
    parser.add_argument("--config", required=True)
    parser.add_argument("--model-profile", default="minimax_user_test")
    parser.add_argument("--run-root", required=True)
    parser.add_argument("--output", required=True)
    parser.add_argument("--timeout-seconds", type=int, default=1200)
    parser.add_argument(
        "--employee",
        choices=sorted(EMPLOYEE_PROFILES),
        default="quant-trader",
    )
    return parser.parse_args()


def load_profile(path: Path, name: str) -> Dict[str, str]:
    raw = yaml.safe_load(path.read_text(encoding="utf-8"))
    models = raw.get("model_cfg") if isinstance(raw, Mapping) else None
    profile = models.get(name) if isinstance(models, Mapping) else None
    if not isinstance(profile, Mapping):
        raise RuntimeError("configured model profile was not found")
    key = str(profile.get("api_key") or "").strip()
    base_url = str(profile.get("base_url") or "").strip().rstrip("/")
    model = str(profile.get("model_name") or "MiniMax-M3").strip()
    if not key or not base_url:
        raise RuntimeError("configured model profile has no credential/base URL")
    return {"key": key, "base_url": base_url, "model": model or "MiniMax-M3"}


def scoped_environment(workspace: Path, run_root: Path, profile: Mapping[str, str]) -> Dict[str, str]:
    env = dict(os.environ)
    # Do not let unrelated shell overrides change the transport under test.
    for name in list(env):
        if name.startswith("QUANT_TRADER_"):
            env.pop(name, None)
    key = profile["key"]
    base_url = profile["base_url"]
    model = profile["model"]
    env.update(
        {
            "OPENKITTY_WORKSPACE": str(workspace),
            "OPENKITTY_AGENT_RUN_DIR": str(run_root),
            "QUANT_TRADER_AUTO_DISCOVERY": "true",
            "QUANT_TRADER_CHINA_MARKET_DATA": "enabled",
            "QUANT_TRADER_CHINA_MARKET_TIMEOUT_SECONDS": "12",
            "QUANT_TRADER_CHINA_MARKET_MAX_BYTES": str(2 * 1024 * 1024),
            "QUANT_TRADER_TRADINGAGENTS_TIMEOUT_SECONDS": "900",
            "QUANT_TRADER_AI_HEDGE_FUND_TIMEOUT_SECONDS": "300",
            "QUANT_TRADER_FINGPT_TIMEOUT_SECONDS": "300",
            "QUANT_TRADER_MINIMAX_CN_API_KEY": key,
            "QUANT_TRADER_MINIMAX_API_KEY": key,
            "QUANT_TRADER_MINIMAX_BASE_URL": base_url,
            "QUANT_TRADER_MINIMAX_MODEL": model,
            "QUANT_TRADER_OPENAI_API_KEY": key,
            "QUANT_TRADER_OPENAI_API_BASE": base_url,
            "PYTHONIOENCODING": "utf-8",
            "PYTHONUNBUFFERED": "1",
        }
    )
    return env


def validate(response: Mapping[str, Any], employee_id: str) -> Dict[str, bool]:
    _, assigned_backend = EMPLOYEE_PROFILES[employee_id]
    data = response.get("data") if isinstance(response.get("data"), Mapping) else {}
    resolution = (
        data.get("security_resolution")
        if isinstance(data.get("security_resolution"), Mapping)
        else {}
    )
    market = data.get("market_data") if isinstance(data.get("market_data"), Mapping) else {}
    results = data.get("backend_results") if isinstance(data.get("backend_results"), list) else []
    by_backend = {
        str(item.get("backend")): item for item in results if isinstance(item, Mapping)
    }
    assigned_result = by_backend.get(assigned_backend, {})
    assigned_status = str(assigned_result.get("status") or "")
    upstream_by_backend = {
        "tradingagents": "TradingAgentsGraph.propagate",
        "ai-hedge-fund": "src.main.run_hedge_fund",
    }
    checks = {
        "worker_terminal": response.get("status") in {"completed", "degraded"},
        "symbol_resolved": resolution.get("symbols") == ["600596.SS"],
        "external_market_data": employee_id == "financial-intelligence-analyst" or (
            market.get("status") == "available"
            and market.get("kind") == "external"
            and market.get("provider")
            in {
                "eastmoney-push2his",
                "tencent-newfqkline",
                "china-market-multi-source",
            }
            and int(market.get("rows") or 0) >= 20
        ),
        "assigned_backend_only": set(by_backend) == {assigned_backend},
        "assigned_backend_usable": assigned_status not in {
            "",
            "unavailable",
            "error",
            "failed",
        },
        "assigned_upstream": (
            bool(assigned_result.get("data", {}).get("upstream_api"))
            if assigned_backend == "fingpt"
            else assigned_result.get("data", {}).get("upstream_api")
            == upstream_by_backend[assigned_backend]
        ),
        "live_trading_disabled": data.get("live_trading") is False,
    }
    return checks


def redact(value: str, secret: str) -> str:
    result = value.replace(secret, "[REDACTED]") if secret else value
    # Defense in depth for common bearer-key shapes in exception text.
    for prefix in ("sk-cp-", "sk-", "Bearer "):
        start = 0
        while True:
            index = result.find(prefix, start)
            if index < 0:
                break
            end = index + len(prefix)
            while end < len(result) and result[end] not in " \t\r\n\"'<>[]{}(),":
                end += 1
            result = result[:index] + "[REDACTED]" + result[end:]
            start = index + len("[REDACTED]")
    return result


def main() -> int:
    args = parse_args()
    workspace = Path(__file__).resolve().parents[3]
    agent_root = Path(__file__).resolve().parents[1]
    config = Path(args.config).expanduser().resolve(strict=True)
    run_root = Path(args.run_root).expanduser().resolve()
    output = Path(args.output).expanduser().resolve()
    run_root.mkdir(parents=True, exist_ok=True)
    output.parent.mkdir(parents=True, exist_ok=True)
    profile = load_profile(config, args.model_profile)
    env = scoped_environment(workspace, run_root, profile)
    capability, _ = EMPLOYEE_PROFILES[args.employee]
    input_data: Dict[str, Any] = {"mode": "research", "symbols": ["600596.SS"]}
    if args.employee == "financial-intelligence-analyst":
        input_data["text"] = (
            "新安股份公告显示本年度主营业务收入和归母净利润同比增长，同时提示原材料"
            "价格波动、海外需求变化和汇率风险可能影响后续经营表现。"
        )
    request = {
        "protocol": PROTOCOL,
        "request_id": "direct-worker-xinan-" + datetime.now(timezone.utc).strftime("%Y%m%dT%H%M%SZ"),
        "agent_id": "quant-trader",
        "employee_id": args.employee,
        "tenant_id": "default",
        "session_id": "direct-worker-xinan",
        "execution_id": "direct-worker-xinan",
        "step_index": 0,
        "capability": capability,
        "input": json.dumps(input_data, ensure_ascii=False),
    }
    started = time.monotonic()
    completed = subprocess.run(
        [sys.executable, "-u", "worker.py"],
        input=json.dumps(request, ensure_ascii=False) + "\n",
        text=True,
        encoding="utf-8",
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        cwd=agent_root,
        env=env,
        timeout=max(60, min(args.timeout_seconds, 1800)),
        check=False,
    )
    duration_ms = int((time.monotonic() - started) * 1000)
    safe_stdout = redact(completed.stdout or "", profile["key"])
    safe_stderr = redact(completed.stderr or "", profile["key"])
    if profile["key"] in safe_stdout or profile["key"] in safe_stderr:
        raise RuntimeError("credential redaction failed")
    lines = [line for line in safe_stdout.splitlines() if line.strip()]
    if len(lines) != 1:
        raise RuntimeError("worker emitted %d non-empty stdout lines" % len(lines))
    response = json.loads(lines[0])
    if not isinstance(response, Mapping):
        raise RuntimeError("worker response is not a JSON object")
    checks = validate(response, args.employee)
    evidence = {
        "schema": "openkitty.quant-trader.direct-worker-e2e/v1",
        "generated_at": datetime.now(timezone.utc).isoformat().replace("+00:00", "Z"),
        "command": "python -u worker.py  # NDJSON input; scoped secrets loaded in memory",
        "cwd": str(agent_root),
        "request": request,
        "exit_code": completed.returncode,
        "duration_ms": duration_ms,
        "stderr_characters": len(safe_stderr),
        "stderr_tail": safe_stderr[-2000:],
        "checks": checks,
        "response": response,
    }
    serialized = json.dumps(evidence, ensure_ascii=False, indent=2)
    if profile["key"] in serialized:
        raise RuntimeError("credential appeared in evidence")
    output.write_text(serialized + "\n", encoding="utf-8")
    summary = {
        "output": str(output),
        "exit_code": completed.returncode,
        "duration_ms": duration_ms,
        "checks": checks,
        "status": response.get("status"),
        "backend_statuses": {
            item.get("backend"): item.get("status")
            for item in response.get("data", {}).get("backend_results", [])
            if isinstance(item, Mapping)
        },
    }
    print(json.dumps(summary, ensure_ascii=False, indent=2))
    return 0 if completed.returncode == 0 and all(checks.values()) else 1


if __name__ == "__main__":
    raise SystemExit(main())
