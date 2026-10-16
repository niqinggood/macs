#!/usr/bin/env python3
"""Generate elder-monitor signals and verify the local policy and AgentOS flow."""

from __future__ import annotations

import argparse
import json
import os
import re
import sys
import time
import urllib.error
import urllib.request
import uuid
from datetime import datetime, timedelta, timezone
from pathlib import Path
from typing import Any

from elder_monitor_core import evaluate_event, load_policy, validate_event


SKILL_NAME = "elder-monitor"
SKILL_VERSION = "1.2.0"
TERMINAL_STATUSES = {"completed", "degraded", "failed", "stopped"}


SCENARIOS: tuple[dict[str, Any], ...] = (
    {
        "name": "normal_activity",
        "description": "正常坐姿与正常活动，不应告警",
        "expected_severity": "none",
        "expected_rules": [],
        "expected_reason": "坐姿只有 10 分钟，活动与设备状态正常，没有任何信号达到处置阈值。",
        "duration_minutes": 10,
        "observations": [
            {"type": "posture", "value": "sitting", "duration_sec": 600, "confidence": 0.98, "source": "camera_pose"},
            {"type": "motion_level", "value": "normal", "confidence": 0.95, "source": "mmwave"},
            {"type": "device_health", "value": "online", "confidence": 0.99, "source": "gateway"},
        ],
        "profile": {"mobility": "independent", "normal_sitting_minutes": 30},
    },
    {
        "name": "prolonged_sitting",
        "description": "坐姿明显超过个人基线，应进入低风险复查",
        "expected_severity": "low",
        "expected_rules": ["prolonged_sitting_above_baseline"],
        "expected_reason": "连续坐姿 45 分钟，超过个人基线 20 分钟的 1.5 倍及策略最低 30 分钟阈值。",
        "duration_minutes": 45,
        "observations": [
            {"type": "posture", "value": "sitting", "duration_sec": 2700, "confidence": 0.96, "source": "camera_pose"},
            {"type": "motion_level", "value": "low", "confidence": 0.82, "source": "mmwave"},
        ],
        "profile": {"mobility": "assisted", "normal_sitting_minutes": 20},
    },
    {
        "name": "distress_low_motion",
        "description": "重复痛苦样发声并伴随低活动，应为高风险",
        "expected_severity": "high",
        "expected_rules": ["distress_cluster_with_low_motion"],
        "expected_reason": "窗口内 8 次高置信度痛苦样发声，同时毫米波检测到活动低于个人基线。",
        "duration_minutes": 5,
        "observations": [
            {"type": "distress_vocalization", "value": True, "count": 8, "confidence": 0.91, "source": "audio_classifier"},
            {"type": "motion_level", "value": "below_personal_baseline", "confidence": 0.86, "source": "mmwave"},
        ],
        "context": {"responded_to_voice_check": None, "voice_check_intent": "unknown", "voice_check_confidence": 0.0, "voice_check_service_status": None},
    },
    {
        "name": "fall_detected",
        "description": "高置信度疑似跌倒，但尚未确认无响应，应为高风险",
        "expected_severity": "high",
        "expected_rules": ["high_confidence_fall_or_floor"],
        "expected_reason": "视觉跌倒置信度 0.94 且检测到倒地姿态；未取得语音确认，因此不能直接判定无响应。",
        "duration_minutes": 2,
        "observations": [
            {"type": "fall_suspected", "value": True, "confidence": 0.94, "source": "vision_fall_detector"},
            {"type": "posture", "value": "lying_on_floor", "duration_sec": 35, "confidence": 0.89, "source": "camera_pose"},
        ],
        "context": {"responded_to_voice_check": None, "voice_check_intent": "unknown", "voice_check_confidence": 0.0, "voice_check_service_status": None},
    },
    {
        "name": "fall_no_response",
        "description": "跌倒后语音确认无响应，应为严重风险",
        "expected_severity": "critical",
        "expected_rules": ["fall_or_floor_plus_no_response"],
        "expected_reason": "高置信度跌倒信号与一次成功执行的语音确认无响应同时成立。",
        "duration_minutes": 2,
        "observations": [
            {"type": "fall_suspected", "value": True, "confidence": 0.95, "source": "vision_fall_detector"},
            {"type": "no_response", "value": True, "confidence": 0.93, "source": "voice_check"},
        ],
        "context": {"responded_to_voice_check": False, "voice_check_intent": "unknown", "voice_check_confidence": 0.93, "voice_check_service_status": "ok"},
    },
    {
        "name": "explicit_help",
        "description": "老人明确说需要帮助，应为严重风险",
        "expected_severity": "critical",
        "expected_rules": ["explicit_help_request"],
        "expected_reason": "语音意图以 0.97 置信度识别到明确求助，明确求助优先于其他普通规则。",
        "duration_minutes": 1,
        "observations": [
            {"type": "help_request", "value": True, "confidence": 0.97, "source": "speech_intent"},
        ],
        "context": {"responded_to_voice_check": True, "voice_check_intent": "need_help", "voice_check_confidence": 0.97, "voice_check_service_status": "ok"},
    },
    {
        "name": "device_offline",
        "description": "设备离线代表监护覆盖缺失，应走中风险运维处置",
        "expected_severity": "medium",
        "expected_rules": ["monitoring_coverage_unavailable"],
        "expected_reason": "网关心跳确认设备离线；这表示监护覆盖不可用，不表示老人正常或无响应。",
        "duration_minutes": 5,
        "observations": [
            {"type": "device_health", "value": "offline", "duration_sec": 300, "confidence": 0.99, "source": "gateway_heartbeat"},
        ],
    },
    {
        "name": "noise_false_positive",
        "description": "大量低置信度噪声不能被误判为痛苦告警",
        "expected_severity": "none",
        "expected_rules": [],
        "expected_reason": "虽然候选发声次数多，但分类置信度只有 0.35，低于 0.6 阈值，且活动水平正常。",
        "duration_minutes": 5,
        "observations": [
            {"type": "distress_vocalization", "value": True, "count": 12, "confidence": 0.35, "source": "audio_classifier"},
            {"type": "motion_level", "value": "normal", "confidence": 0.92, "source": "mmwave"},
        ],
    },
    {
        "name": "voice_service_failure",
        "description": "跌倒信号存在但语音服务失败，不能伪造无响应，应保持高风险",
        "expected_severity": "high",
        "expected_rules": ["high_confidence_fall_or_floor"],
        "expected_reason": "跌倒信号达到高风险阈值；语音服务状态是 error，只能记为未知，不能升级成‘确认无响应’。",
        "duration_minutes": 2,
        "observations": [
            {"type": "fall_suspected", "value": True, "confidence": 0.92, "source": "vision_fall_detector"},
        ],
        "context": {"responded_to_voice_check": None, "voice_check_intent": "unknown", "voice_check_confidence": 0.0, "voice_check_service_status": "error"},
    },
)


def scenario_names() -> list[str]:
    return [item["name"] for item in SCENARIOS]


def _scenario(name: str) -> dict[str, Any]:
    for item in SCENARIOS:
        if item["name"] == name:
            return item
    raise KeyError(name)


def make_event(
    scenario_name: str,
    run_id: str,
    end_time: datetime | None = None,
) -> dict[str, Any]:
    """Build a normalized event with synthetic, non-identifying sensor values."""
    spec = _scenario(scenario_name)
    end = end_time or datetime.now(timezone.utc)
    if end.tzinfo is None:
        end = end.replace(tzinfo=timezone.utc)
    start = end - timedelta(minutes=int(spec["duration_minutes"]))
    event: dict[str, Any] = {
        "event_id": f"sim_{run_id}_{scenario_name}",
        "person_id": "elder_sim_001",
        "window": {
            "start": start.isoformat(timespec="seconds"),
            "end": end.isoformat(timespec="seconds"),
        },
        "observations": json.loads(json.dumps(spec["observations"])),
        "context": {
            "room": "simulation_room",
            "time_period": "unknown",
            **json.loads(json.dumps(spec.get("context", {}))),
        },
        "profile": json.loads(json.dumps(spec.get("profile", {"mobility": "unknown"}))),
    }
    for index, observation in enumerate(event["observations"], start=1):
        observation.setdefault("evidence_ref", f"sim://{run_id}/{scenario_name}/{index}")
    return event


def run_local_scenario(name: str, run_id: str, policy: dict[str, Any]) -> dict[str, Any]:
    spec = _scenario(name)
    event = make_event(name, run_id)
    errors = validate_event(event)
    if errors:
        return {
            "scenario": name,
            "description": spec["description"],
            "expected_severity": spec["expected_severity"],
            "passed": False,
            "errors": errors,
        }
    assessment = evaluate_event(event, policy)
    actual = assessment.get("severity")
    return {
        "scenario": name,
        "description": spec["description"],
        "expected_severity": spec["expected_severity"],
        "actual_severity": actual,
        "passed": actual == spec["expected_severity"],
        "event": event,
        "assessment": assessment,
    }


def _json_candidates(text: str) -> list[Any]:
    candidates: list[Any] = []
    stripped = text.strip()
    if not stripped:
        return candidates
    try:
        candidates.append(json.loads(stripped))
    except json.JSONDecodeError:
        pass
    for match in re.finditer(r"```(?:json)?\s*(.*?)\s*```", stripped, re.IGNORECASE | re.DOTALL):
        try:
            candidates.append(json.loads(match.group(1)))
        except json.JSONDecodeError:
            pass
    decoder = json.JSONDecoder()
    for index, character in enumerate(stripped):
        if character not in "[{":
            continue
        try:
            value, _ = decoder.raw_decode(stripped[index:])
        except json.JSONDecodeError:
            continue
        candidates.append(value)
    return candidates


def find_assessment(value: Any) -> dict[str, Any] | None:
    """Find an assessment object in AgentOS result wrappers or model text."""
    if isinstance(value, str):
        for candidate in _json_candidates(value):
            found = find_assessment(candidate)
            if found is not None:
                return found
        return None
    if isinstance(value, dict):
        if isinstance(value.get("severity"), str):
            return value
        preferred = ("assessment", "result", "output", "data", "content")
        for key in preferred:
            if key in value:
                found = find_assessment(value[key])
                if found is not None:
                    return found
        for nested in value.values():
            found = find_assessment(nested)
            if found is not None:
                return found
    elif isinstance(value, list):
        for nested in value:
            found = find_assessment(nested)
            if found is not None:
                return found
    return None


class AgentOSClient:
    def __init__(self, base_url: str, token: str, user_id: str, timeout: float = 30.0):
        self.base_url = base_url.rstrip("/")
        self.token = token
        self.user_id = user_id
        self.timeout = timeout

    def _request(
        self,
        method: str,
        path: str,
        payload: dict[str, Any] | None = None,
        extra_headers: dict[str, str] | None = None,
    ) -> dict[str, Any]:
        headers = {
            "Accept": "application/json",
            "Authorization": f"Bearer {self.token}",
            "X-OpenKitty-User-ID": self.user_id,
        }
        body = None
        if payload is not None:
            headers["Content-Type"] = "application/json"
            body = json.dumps(payload, ensure_ascii=False).encode("utf-8")
        if extra_headers:
            headers.update(extra_headers)
        request = urllib.request.Request(
            f"{self.base_url}{path}",
            data=body,
            headers=headers,
            method=method,
        )
        try:
            with urllib.request.urlopen(request, timeout=self.timeout) as response:
                data = json.load(response)
        except urllib.error.HTTPError as error:
            response_body = error.read().decode("utf-8", errors="replace")[:2000]
            raise RuntimeError(f"AgentOS HTTP {error.code}: {response_body}") from error
        except urllib.error.URLError as error:
            raise RuntimeError(f"AgentOS connection failed: {error.reason}") from error
        if not isinstance(data, dict):
            raise RuntimeError("AgentOS returned a non-object JSON response")
        return data

    def discover_role(self, preferred_role_id: str | None = None) -> dict[str, Any]:
        response = self._request("GET", "/api/v1/agent/roles")
        roles = [role for role in response.get("roles", []) if role.get("enabled")]
        if preferred_role_id:
            for role in roles:
                if role.get("role_id") == preferred_role_id:
                    return role
            available = ", ".join(str(role.get("role_id")) for role in roles) or "none"
            raise RuntimeError(f"role {preferred_role_id!r} is unavailable; available: {available}")
        if not roles:
            raise RuntimeError("AgentOS returned no enabled authorized roles")
        return roles[0]

    def submit(self, payload: dict[str, Any], idempotency_key: str) -> dict[str, Any]:
        return self._request(
            "POST",
            "/api/v1/agent/tasks",
            payload,
            {
                "Idempotency-Key": idempotency_key,
                "X-Request-ID": f"req-{idempotency_key}",
            },
        )

    def get_task(self, task_id: str) -> dict[str, Any]:
        return self._request("GET", f"/api/v1/agent/tasks/{task_id}")


def make_agentos_payload(
    event: dict[str, Any],
    scenario_name: str,
    run_id: str,
    role: dict[str, Any],
    user_id: str,
    skill_name: str = SKILL_NAME,
    skill_version: str = SKILL_VERSION,
) -> dict[str, Any]:
    message = (
        "这是养老监护链路的合成信号测试。使用 $elder-monitor 对下面的归一化事件执行确定性评估。"
        "只返回符合 schemas/assessment.schema.json 的单个 JSON 对象，不要使用 Markdown。"
        "严禁发送通知、拨打电话、调用急救、写入外部系统或执行 recommended_actions；"
        "recommended_actions 只作为建议字段返回。事件如下：\n"
        + json.dumps(event, ensure_ascii=False, separators=(",", ":"))
    )
    payload: dict[str, Any] = {
        "role_id": role["role_id"],
        "user_id": user_id,
        "session_id": f"elder-monitor-sim-{run_id}-{scenario_name}",
        "message": message,
        "skill_run": {
            "skill_name": skill_name,
            "version": skill_version,
            "client_reference": f"sim-{run_id}-{scenario_name}",
        },
    }
    if role.get("role_revision"):
        payload["role_revision"] = role["role_revision"]
    return payload


def run_agentos_scenario(
    name: str,
    run_id: str,
    client: AgentOSClient,
    role: dict[str, Any],
    poll_interval: float,
    task_timeout: float,
    event: dict[str, Any] | None = None,
    skill_name: str = SKILL_NAME,
    skill_version: str = SKILL_VERSION,
) -> dict[str, Any]:
    spec = _scenario(name)
    event = event or make_event(name, run_id)
    validation_errors = validate_event(event)
    if validation_errors:
        return {
            "scenario": name,
            "expected_severity": spec["expected_severity"],
            "passed": False,
            "errors": validation_errors,
        }
    payload = make_agentos_payload(
        event,
        name,
        run_id,
        role,
        client.user_id,
        skill_name=skill_name,
        skill_version=skill_version,
    )
    idempotency_key = f"elder-monitor-{run_id}-{name}"
    submitted = client.submit(payload, idempotency_key)
    task_id = submitted.get("task_id")
    if not isinstance(task_id, str) or not task_id:
        raise RuntimeError("AgentOS submit response has no task_id")

    deadline = time.monotonic() + task_timeout
    task = submitted
    while task.get("status") not in TERMINAL_STATUSES:
        if time.monotonic() >= deadline:
            return {
                "scenario": name,
                "description": spec["description"],
                "expected_severity": spec["expected_severity"],
                "task_id": task_id,
                "task_status": task.get("status"),
                "passed": False,
                "error": f"task did not reach a terminal state within {task_timeout:g} seconds",
            }
        time.sleep(poll_interval)
        task = client.get_task(task_id)

    assessment = find_assessment(task.get("result"))
    actual = assessment.get("severity") if assessment else None
    skill_run = task.get("skill_run") if isinstance(task.get("skill_run"), dict) else {}
    receipt_phase = skill_run.get("phase")
    passed = (
        task.get("status") in {"completed", "degraded"}
        and actual == spec["expected_severity"]
        and receipt_phase == "completed"
    )
    record: dict[str, Any] = {
        "scenario": name,
        "description": spec["description"],
        "expected_severity": spec["expected_severity"],
        "actual_severity": actual,
        "task_id": task_id,
        "task_status": task.get("status"),
        "skill_run_phase": receipt_phase,
        "passed": passed,
    }
    if assessment:
        record["assessment"] = assessment
    else:
        record["error"] = "no assessment JSON found in AgentOS task result"
        record["result_excerpt"] = str(task.get("result", ""))[:1000]
    if task.get("error"):
        record["task_error"] = task["error"]
    return record


def _selected_scenarios(values: list[str]) -> list[str]:
    if not values or "all" in values:
        return scenario_names()
    selected: list[str] = []
    valid = set(scenario_names())
    for value in values:
        if value not in valid:
            raise ValueError(f"unknown scenario {value!r}; choose from: {', '.join(scenario_names())}")
        if value not in selected:
            selected.append(value)
    return selected


def _new_run_id() -> str:
    timestamp = datetime.now(timezone.utc).strftime("%Y%m%dT%H%M%SZ")
    return f"{timestamp}-{uuid.uuid4().hex[:8]}"


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--mode", choices=("local", "agentos", "both"), default="local")
    parser.add_argument("--scenario", action="append", default=[], help="repeat for selected scenarios; default: all")
    parser.add_argument("--base-url", default=os.environ.get("OPENKITTY_AGENTOS_URL", "http://127.0.0.1:18080"))
    parser.add_argument("--token", default=os.environ.get("OPENKITTY_AGENTOS_TOKEN"))
    parser.add_argument("--role-id", default=os.environ.get("OPENKITTY_AGENTOS_ROLE_ID"))
    parser.add_argument("--skill-name", default=SKILL_NAME, help="canonical name or installed alias")
    parser.add_argument("--skill-version", default=SKILL_VERSION)
    parser.add_argument("--user-id", default="elder-monitor-simulator")
    parser.add_argument("--run-id", help="stable run identifier; generated when omitted")
    parser.add_argument("--poll-interval", type=float, default=2.0)
    parser.add_argument("--task-timeout", type=float, default=300.0)
    parser.add_argument("--http-timeout", type=float, default=30.0)
    parser.add_argument("--output", type=Path, help="write the full JSON report to this path")
    parser.add_argument("--pretty", action="store_true")
    return parser


def main(argv: list[str] | None = None) -> int:
    args = build_parser().parse_args(argv)
    try:
        selected = _selected_scenarios(args.scenario)
    except ValueError as error:
        print(str(error), file=sys.stderr)
        return 2
    if args.poll_interval <= 0 or args.task_timeout <= 0 or args.http_timeout <= 0:
        print("timeouts and poll interval must be positive", file=sys.stderr)
        return 2
    if args.mode in {"agentos", "both"} and not args.token:
        print("AgentOS mode requires --token or OPENKITTY_AGENTOS_TOKEN", file=sys.stderr)
        return 2

    run_id = args.run_id or _new_run_id()
    report: dict[str, Any] = {
        "run_id": run_id,
        "generated_at": datetime.now(timezone.utc).isoformat(timespec="seconds"),
        "synthetic_data_only": True,
        "external_actions_executed": False,
        "selected_scenarios": selected,
    }

    if args.mode in {"local", "both"}:
        policy = load_policy()
        report["local"] = [run_local_scenario(name, run_id, policy) for name in selected]

    if args.mode in {"agentos", "both"}:
        client = AgentOSClient(args.base_url, args.token, args.user_id, args.http_timeout)
        try:
            role = client.discover_role(args.role_id)
            report["agentos_role"] = {
                "role_id": role.get("role_id"),
                "role_revision": role.get("role_revision"),
                "agent_name": role.get("agent_name"),
            }
            agentos_results = []
            for name in selected:
                print(f"[AgentOS] running {name}...", file=sys.stderr, flush=True)
                try:
                    result = run_agentos_scenario(
                        name,
                        run_id,
                        client,
                        role,
                        args.poll_interval,
                        args.task_timeout,
                        skill_name=args.skill_name,
                        skill_version=args.skill_version,
                    )
                except Exception as error:  # isolate scenarios so the report remains useful
                    result = {
                        "scenario": name,
                        "expected_severity": _scenario(name)["expected_severity"],
                        "passed": False,
                        "error": str(error),
                    }
                agentos_results.append(result)
            report["agentos"] = agentos_results
        except Exception as error:
            report["agentos"] = []
            report["agentos_setup_error"] = str(error)

    result_groups = [report[key] for key in ("local", "agentos") if key in report]
    report["passed"] = bool(result_groups) and all(
        item.get("passed") is True for group in result_groups for item in group
    ) and "agentos_setup_error" not in report
    indent = 2 if args.pretty else None
    rendered = json.dumps(report, ensure_ascii=False, indent=indent)
    print(rendered)
    if args.output:
        args.output.parent.mkdir(parents=True, exist_ok=True)
        args.output.write_text(rendered + "\n", encoding="utf-8")
    return 0 if report["passed"] else 1


if __name__ == "__main__":
    raise SystemExit(main())
