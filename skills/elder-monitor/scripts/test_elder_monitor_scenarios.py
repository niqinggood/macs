#!/usr/bin/env python3
"""逐场景展示合成信号，并测试老人监护本地规则或 AgentOS 完整链路。

本脚本覆盖九类情况：
1. 正常活动：验证正常坐姿和活动不会误告警。
2. 长时间坐姿：验证超过个人基线后进入低风险复查。
3. 痛苦样发声 + 低活动：验证多模态组合信号进入高风险。
4. 疑似跌倒：验证高置信度跌倒进入高风险确认流程。
5. 跌倒后无响应：验证跌倒与有效语音确认无响应组合为严重风险。
6. 明确求助：验证老人主动求助直接进入严重风险。
7. 设备离线：验证监护覆盖缺失独立进入运维处置。
8. 噪声误报：验证低置信度音频不会被次数放大成告警。
9. 语音服务故障：验证服务错误不会被伪装成老人无响应。

运行前会打印每个场景的业务含义、期望结果、判定原因，以及本次实际
送入规则引擎/AgentOS 的完整 event JSON。脚本只请求风险研判，不会执行
recommended_actions，不会发送通知、拨打电话或调用急救服务。
"""

from __future__ import annotations

import argparse
import json
import os
import sys
import time
import uuid
from datetime import datetime, timezone
from pathlib import Path
from typing import Any

from elder_monitor_core import evaluate_event, load_policy, validate_event
from simulate_agentos import (
    AgentOSClient,
    SCENARIOS,
    SKILL_VERSION,
    make_event,
    run_agentos_scenario,
    scenario_names,
)


def _select_scenarios(values: list[str]) -> list[dict[str, Any]]:
    requested = scenario_names() if not values or "all" in values else values
    catalog = {item["name"]: item for item in SCENARIOS}
    unknown = [name for name in requested if name not in catalog]
    if unknown:
        raise ValueError(
            f"未知场景: {', '.join(unknown)}；可选值: {', '.join(scenario_names())}"
        )
    selected: list[dict[str, Any]] = []
    for name in requested:
        if all(item["name"] != name for item in selected):
            selected.append(catalog[name])
    return selected


def format_scenario(
    index: int,
    total: int,
    spec: dict[str, Any],
    event: dict[str, Any],
) -> str:
    expected_rules = spec.get("expected_rules") or []
    rules_text = ", ".join(expected_rules) if expected_rules else "无规则触发"
    return "\n".join(
        [
            "=" * 88,
            f"场景 {index}/{total}: {spec['name']}",
            f"含义: {spec['description']}",
            f"期望风险等级: {spec['expected_severity']}",
            f"期望触发规则: {rules_text}",
            f"判定原因: {spec['expected_reason']}",
            "发送的 event JSON:",
            json.dumps(event, ensure_ascii=False, indent=2),
            "=" * 88,
        ]
    )


def _local_result(
    spec: dict[str, Any],
    event: dict[str, Any],
    policy: dict[str, Any],
) -> dict[str, Any]:
    errors = validate_event(event)
    if errors:
        return {
            "scenario": spec["name"],
            "expected_severity": spec["expected_severity"],
            "passed": False,
            "errors": errors,
        }
    assessment = evaluate_event(event, policy)
    expected_rules = set(spec.get("expected_rules") or [])
    actual_rules = set(assessment.get("triggered_rules") or [])
    severity_passed = assessment.get("severity") == spec["expected_severity"]
    rules_passed = expected_rules.issubset(actual_rules)
    return {
        "scenario": spec["name"],
        "expected_severity": spec["expected_severity"],
        "actual_severity": assessment.get("severity"),
        "expected_rules": sorted(expected_rules),
        "actual_rules": sorted(actual_rules),
        "passed": severity_passed and rules_passed,
        "assessment": assessment,
    }


def _print_result(label: str, result: dict[str, Any]) -> None:
    state = "通过" if result.get("passed") else "失败"
    actual = result.get("actual_severity", "未返回")
    expected = result.get("expected_severity", "")
    print(f"[{label}] {state}: 期望={expected}, 实际={actual}", flush=True)
    if result.get("task_id"):
        print(
            "[AgentOS] "
            f"task_id={result['task_id']}, status={result.get('task_status')}, "
            f"skill_run={result.get('skill_run_phase')}",
            flush=True,
        )
    if result.get("error"):
        print(f"[{label}] 错误: {result['error']}", flush=True)


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--mode",
        choices=("local", "agentos", "both"),
        default="local",
        help="local=只测规则；agentos=只测接口；both=两层都测",
    )
    parser.add_argument(
        "--scenario",
        action="append",
        default=[],
        help="指定一个场景，可重复；不指定或填写 all 时测试全部场景",
    )
    parser.add_argument(
        "--base-url",
        default=os.environ.get("OPENKITTY_AGENTOS_URL", "http://127.0.0.1:18080"),
    )
    parser.add_argument("--token", default=os.environ.get("OPENKITTY_AGENTOS_TOKEN"))
    parser.add_argument("--role-id", default=os.environ.get("OPENKITTY_AGENTOS_ROLE_ID"))
    parser.add_argument(
        "--skill-name",
        default="elder",
        help="AgentOS Skill 名称或别名；默认 elder，用于验证 @elder 对应别名",
    )
    parser.add_argument("--skill-version", default=SKILL_VERSION)
    parser.add_argument("--user-id", default="elder-monitor-local-tester")
    parser.add_argument("--poll-interval", type=float, default=1.0)
    parser.add_argument("--task-timeout", type=float, default=300.0)
    parser.add_argument("--http-timeout", type=float, default=30.0)
    parser.add_argument("--output", type=Path, help="保存完整 JSON 测试报告")
    return parser


def main(argv: list[str] | None = None) -> int:
    args = build_parser().parse_args(argv)
    try:
        selected = _select_scenarios(args.scenario)
    except ValueError as error:
        print(error, file=sys.stderr)
        return 2
    if args.mode in {"agentos", "both"} and not args.token:
        print(
            "AgentOS 模式需要 --token 或环境变量 OPENKITTY_AGENTOS_TOKEN",
            file=sys.stderr,
        )
        return 2
    if args.poll_interval <= 0 or args.task_timeout <= 0 or args.http_timeout <= 0:
        print("轮询间隔和超时时间必须大于 0", file=sys.stderr)
        return 2

    run_id = datetime.now(timezone.utc).strftime("%Y%m%dT%H%M%SZ") + "-" + uuid.uuid4().hex[:8]
    common_end = datetime.now(timezone.utc)
    policy = load_policy() if args.mode in {"local", "both"} else None
    client = None
    role = None
    if args.mode in {"agentos", "both"}:
        client = AgentOSClient(args.base_url, args.token, args.user_id, args.http_timeout)
        try:
            role = client.discover_role(args.role_id)
        except Exception as error:
            print(f"AgentOS 初始化失败: {error}", file=sys.stderr)
            return 1
        print(
            f"AgentOS 岗位: {role.get('role_id')}，版本: {role.get('role_revision')}",
            flush=True,
        )

    report: dict[str, Any] = {
        "run_id": run_id,
        "generated_at": common_end.isoformat(timespec="seconds"),
        "mode": args.mode,
        "synthetic_data_only": True,
        "external_actions_executed": False,
        "scenarios": [],
    }

    for index, spec in enumerate(selected, start=1):
        event = make_event(spec["name"], run_id, common_end)
        print(format_scenario(index, len(selected), spec, event), flush=True)
        item: dict[str, Any] = {
            "scenario": spec["name"],
            "meaning": spec["description"],
            "expected_severity": spec["expected_severity"],
            "expected_rules": spec.get("expected_rules", []),
            "expected_reason": spec["expected_reason"],
            "sent_event": event,
        }
        if policy is not None:
            local = _local_result(spec, event, policy)
            item["local"] = local
            _print_result("本地规则", local)
        if client is not None and role is not None:
            try:
                agentos = run_agentos_scenario(
                    spec["name"],
                    run_id,
                    client,
                    role,
                    args.poll_interval,
                    args.task_timeout,
                    event=event,
                    skill_name=args.skill_name,
                    skill_version=args.skill_version,
                )
            except Exception as error:  # keep subsequent scenarios runnable
                agentos = {
                    "scenario": spec["name"],
                    "expected_severity": spec["expected_severity"],
                    "passed": False,
                    "error": str(error),
                }
            item["agentos"] = agentos
            _print_result("AgentOS", agentos)
        report["scenarios"].append(item)

    checks = []
    for item in report["scenarios"]:
        for key in ("local", "agentos"):
            if key in item:
                checks.append(item[key].get("passed") is True)
    report["passed"] = bool(checks) and all(checks)
    report["summary"] = {
        "total_scenarios": len(report["scenarios"]),
        "total_checks": len(checks),
        "passed_checks": sum(checks),
        "failed_checks": len(checks) - sum(checks),
    }

    print("\n" + "#" * 88)
    print(
        f"测试完成: 场景={len(report['scenarios'])}, 检查={len(checks)}, "
        f"通过={sum(checks)}, 失败={len(checks) - sum(checks)}"
    )
    print(f"总体结果: {'通过' if report['passed'] else '失败'}")
    print("外部通知/电话/急救动作: 未执行")
    if args.output:
        args.output.parent.mkdir(parents=True, exist_ok=True)
        args.output.write_text(
            json.dumps(report, ensure_ascii=False, indent=2) + "\n",
            encoding="utf-8",
        )
        print(f"完整报告: {args.output}")
    print("#" * 88)
    return 0 if report["passed"] else 1


if __name__ == "__main__":
    raise SystemExit(main())
