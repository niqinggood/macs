"""Deterministic elder-monitoring assessment core with no external dependencies."""

from __future__ import annotations

import hashlib
import json
from datetime import datetime
from pathlib import Path
from typing import Any


SKILL_ROOT = Path(__file__).resolve().parents[1]
DEFAULT_POLICY_PATH = SKILL_ROOT / "rules" / "risk-policy.json"
ALLOWED_OBSERVATION_TYPES = {
    "posture",
    "motion_level",
    "distress_vocalization",
    "fall_suspected",
    "help_request",
    "no_response",
    "device_health",
}


def load_policy(path: str | Path | None = None) -> dict[str, Any]:
    policy_path = Path(path) if path else DEFAULT_POLICY_PATH
    with policy_path.open("r", encoding="utf-8") as handle:
        return json.load(handle)


def _parse_timestamp(value: Any, field: str, errors: list[str]) -> datetime | None:
    if not isinstance(value, str) or not value:
        errors.append(f"{field} must be a non-empty ISO 8601 string")
        return None
    try:
        parsed = datetime.fromisoformat(value.replace("Z", "+00:00"))
    except ValueError:
        errors.append(f"{field} is not a valid ISO 8601 timestamp")
        return None
    if parsed.tzinfo is None:
        errors.append(f"{field} must include a timezone offset")
    return parsed


def validate_event(event: Any) -> list[str]:
    """Return validation errors. An empty list means the event is usable."""
    errors: list[str] = []
    if not isinstance(event, dict):
        return ["event must be a JSON object"]

    for field in ("event_id", "person_id"):
        value = event.get(field)
        if not isinstance(value, str) or not value.strip():
            errors.append(f"{field} must be a non-empty string")

    window = event.get("window")
    if not isinstance(window, dict):
        errors.append("window must be an object")
    else:
        start = _parse_timestamp(window.get("start"), "window.start", errors)
        end = _parse_timestamp(window.get("end"), "window.end", errors)
        if start and end and start.tzinfo and end.tzinfo and end <= start:
            errors.append("window.end must be later than window.start")

    observations = event.get("observations")
    if not isinstance(observations, list) or not observations:
        errors.append("observations must be a non-empty array")
    else:
        for index, observation in enumerate(observations):
            prefix = f"observations[{index}]"
            if not isinstance(observation, dict):
                errors.append(f"{prefix} must be an object")
                continue
            for field in ("type", "source"):
                value = observation.get(field)
                if not isinstance(value, str) or not value.strip():
                    errors.append(f"{prefix}.{field} must be a non-empty string")
            observation_type = observation.get("type")
            if (
                isinstance(observation_type, str)
                and observation_type.strip()
                and observation_type not in ALLOWED_OBSERVATION_TYPES
            ):
                allowed = ", ".join(sorted(ALLOWED_OBSERVATION_TYPES))
                errors.append(f"{prefix}.type must be one of: {allowed}")
            if "value" not in observation:
                errors.append(f"{prefix}.value is required")
            confidence = observation.get("confidence")
            if not isinstance(confidence, (int, float)) or isinstance(confidence, bool):
                errors.append(f"{prefix}.confidence must be a number")
            elif not 0 <= float(confidence) <= 1:
                errors.append(f"{prefix}.confidence must be between 0 and 1")
            count = observation.get("count")
            if count is not None and (not isinstance(count, int) or isinstance(count, bool) or count < 0):
                errors.append(f"{prefix}.count must be a non-negative integer")
            duration = observation.get("duration_sec")
            if duration is not None and (
                not isinstance(duration, (int, float)) or isinstance(duration, bool) or duration < 0
            ):
                errors.append(f"{prefix}.duration_sec must be a non-negative number")

    context = event.get("context", {})
    if not isinstance(context, dict):
        errors.append("context must be an object when provided")
    else:
        if context.get("responded_to_voice_check") not in (True, False, None):
            errors.append("context.responded_to_voice_check must be true, false, or null")
        voice_confidence = context.get("voice_check_confidence")
        if voice_confidence is not None and (
            not isinstance(voice_confidence, (int, float))
            or isinstance(voice_confidence, bool)
            or not 0 <= float(voice_confidence) <= 1
        ):
            errors.append("context.voice_check_confidence must be between 0 and 1")
        service_status = context.get("voice_check_service_status")
        if service_status not in (None, "ok", "timeout", "error"):
            errors.append("context.voice_check_service_status must be ok, timeout, error, or null")
        if service_status == "error" and context.get("responded_to_voice_check") is False:
            errors.append(
                "context.responded_to_voice_check must be null when voice_check_service_status is error"
            )

    profile = event.get("profile", {})
    if not isinstance(profile, dict):
        errors.append("profile must be an object when provided")
    else:
        for field in ("normal_sitting_minutes", "normal_inactive_minutes"):
            value = profile.get(field)
            if value is not None and (
                not isinstance(value, (int, float)) or isinstance(value, bool) or value <= 0
            ):
                errors.append(f"profile.{field} must be a positive number")

    return errors


def _truthy(value: Any) -> bool:
    if value is True:
        return True
    if isinstance(value, str):
        return value.strip().lower() in {
            "true",
            "yes",
            "detected",
            "suspected",
            "help",
            "need_help",
            "no_response",
        }
    return False


def _number(value: Any, default: float = 0.0) -> float:
    if isinstance(value, (int, float)) and not isinstance(value, bool):
        return float(value)
    return default


def _confidence(observation: dict[str, Any]) -> float:
    return max(0.0, min(1.0, _number(observation.get("confidence"))))


def _round_confidence(value: float) -> float:
    return round(max(0.0, min(1.0, value)), 2)


def _stable_assessment_id(event_id: str, policy_version: str) -> str:
    digest = hashlib.sha256(f"{event_id}|{policy_version}".encode("utf-8")).hexdigest()[:16]
    return f"assess_{digest}"


def evaluate_event(event: dict[str, Any], policy: dict[str, Any]) -> dict[str, Any]:
    errors = validate_event(event)
    if errors:
        raise ValueError("; ".join(errors))

    thresholds = policy["thresholds"]
    operations = policy["operational_defaults"]
    observations = event["observations"]
    context = event.get("context", {})
    profile = event.get("profile", {})

    distress = [item for item in observations if item.get("type") == "distress_vocalization"]
    distress_count = sum(int(item.get("count", 1)) for item in distress)
    distress_confidence = max((_confidence(item) for item in distress), default=0.0)

    motion = [item for item in observations if item.get("type") == "motion_level"]
    low_motion_values = {"low", "very_low", "below_personal_baseline", "none"}
    low_motion_items = [
        item for item in motion if str(item.get("value", "")).strip().lower() in low_motion_values
    ]
    low_motion_confidence = max((_confidence(item) for item in low_motion_items), default=0.0)
    low_motion = low_motion_confidence >= thresholds["low_motion_min_confidence"]

    posture = [item for item in observations if item.get("type") == "posture"]
    floor_items = [
        item for item in posture if str(item.get("value", "")).strip().lower() == "lying_on_floor"
    ]
    floor_confidence = max((_confidence(item) for item in floor_items), default=0.0)

    sitting_minutes = max(
        (
            _number(item.get("duration_sec")) / 60
            for item in posture
            if str(item.get("value", "")).strip().lower() == "sitting"
        ),
        default=0.0,
    )
    baseline_sitting = _number(profile.get("normal_sitting_minutes"))
    if baseline_sitting > 0:
        prolonged_limit = max(
            float(thresholds["prolonged_inactivity_min_minutes"]),
            baseline_sitting * float(thresholds["baseline_excess_ratio"]),
        )
    else:
        prolonged_limit = float(thresholds["prolonged_inactivity_min_minutes"])
    prolonged_sitting = sitting_minutes >= prolonged_limit

    fall = [item for item in observations if item.get("type") == "fall_suspected" and _truthy(item.get("value"))]
    fall_confidence = max((_confidence(item) for item in fall), default=0.0)
    fall_high = fall_confidence >= thresholds["fall_high_confidence"]

    help_items = [item for item in observations if item.get("type") == "help_request" and _truthy(item.get("value"))]
    help_confidence = max((_confidence(item) for item in help_items), default=0.0)
    voice_check_confidence = _number(context.get("voice_check_confidence"))
    voice_check_status = context.get("voice_check_service_status")
    voice_intent_help = (
        context.get("voice_check_intent") == "need_help"
        and voice_check_status in (None, "ok")
        and voice_check_confidence >= thresholds["help_request_min_confidence"]
    )
    explicit_help = help_confidence >= thresholds["help_request_min_confidence"] or voice_intent_help

    no_response_items = [
        item for item in observations if item.get("type") == "no_response" and _truthy(item.get("value"))
    ]
    no_response_confidence = max((_confidence(item) for item in no_response_items), default=0.0)
    no_response = (
        context.get("responded_to_voice_check") is False and voice_check_status != "error"
    ) or no_response_confidence >= 0.75
    voice_check_unknown = (
        context.get("responded_to_voice_check") is None or voice_check_status == "error"
    )

    device_issues = [
        item
        for item in observations
        if item.get("type") == "device_health"
        and str(item.get("value", "")).strip().lower() in {"offline", "stale", "blocked", "error"}
        and _confidence(item) >= 0.8
    ]

    severity = "none"
    summary = "未发现达到演示策略处置阈值的异常。"
    triggered_rules: list[str] = []
    evidence: list[str] = []
    uncertainty: list[str] = []
    actions: list[dict[str, Any]] = []

    if explicit_help:
        severity = "critical"
        summary = "检测到明确求助，需要立即执行预授权的紧急升级流程。"
        triggered_rules.append("explicit_help_request")
        evidence.append(f"明确求助信号置信度 {_round_confidence(max(help_confidence, 1.0 if voice_intent_help else 0.0))}")
    elif (fall_high or floor_confidence >= thresholds["fall_high_confidence"]) and no_response:
        severity = "critical"
        summary = "疑似跌倒或倒地姿态后未获得响应，需要立即执行预授权的紧急升级流程。"
        triggered_rules.append("fall_or_floor_plus_no_response")
        evidence.append(
            f"疑似跌倒/倒地证据置信度 {_round_confidence(max(fall_confidence, floor_confidence))}"
        )
        evidence.append("语音确认后未获得响应")
    elif fall_high or floor_confidence >= thresholds["fall_high_confidence"]:
        severity = "high"
        summary = "检测到高置信度疑似跌倒或倒地姿态，需要立即确认并通知照护人。"
        triggered_rules.append("high_confidence_fall_or_floor")
        evidence.append(
            f"疑似跌倒/倒地证据置信度 {_round_confidence(max(fall_confidence, floor_confidence))}"
        )
    elif (
        distress_count >= thresholds["distress_alert_count"]
        and distress_confidence >= thresholds["distress_min_confidence"]
        and low_motion
    ):
        severity = "high"
        summary = "重复痛苦样发声与活动低于个人基线同时出现，需要立即确认并通知照护人。"
        triggered_rules.append("distress_cluster_with_low_motion")
        evidence.append(
            f"窗口内检测到 {distress_count} 次痛苦样发声，置信度 {_round_confidence(distress_confidence)}"
        )
        evidence.append(f"活动水平偏低，置信度 {_round_confidence(low_motion_confidence)}")
    elif (
        distress_count >= thresholds["distress_alert_count"]
        and distress_confidence >= thresholds["distress_min_confidence"]
    ) or (
        distress_count >= thresholds["distress_attention_count"]
        and distress_confidence >= thresholds["distress_min_confidence"]
        and low_motion
    ):
        severity = "medium"
        summary = "检测到需要补充确认的痛苦样发声模式。"
        triggered_rules.append("distress_requires_confirmation")
        evidence.append(
            f"窗口内检测到 {distress_count} 次痛苦样发声，置信度 {_round_confidence(distress_confidence)}"
        )
        if low_motion:
            evidence.append(f"活动水平偏低，置信度 {_round_confidence(low_motion_confidence)}")
    elif device_issues:
        severity = "medium"
        summary = "监护设备数据不可用，需要运维人员确认监护覆盖。"
        triggered_rules.append("monitoring_coverage_unavailable")
        for item in device_issues:
            evidence.append(f"设备状态 {item.get('value')}，来源 {item.get('source')}")
    elif prolonged_sitting:
        severity = "low"
        summary = "坐姿持续时间明显超过当前个人基线或演示策略下限，建议稍后复查。"
        triggered_rules.append("prolonged_sitting_above_baseline")
        evidence.append(
            f"坐姿持续 {round(sitting_minutes, 1)} 分钟，当前比较阈值 {round(prolonged_limit, 1)} 分钟"
        )

    if sitting_minutes > 0 and not prolonged_sitting:
        if baseline_sitting > 0:
            evidence.append(
                f"坐姿持续 {round(sitting_minutes, 1)} 分钟，低于个人比较阈值 {round(prolonged_limit, 1)} 分钟"
            )
        else:
            evidence.append(f"坐姿持续 {round(sitting_minutes, 1)} 分钟，未达到演示策略下限")

    if distress_count > 0:
        uncertainty.append("痛苦样发声可能包含电视、访客或环境噪声")
    if severity in {"medium", "high"} and voice_check_unknown:
        uncertainty.append("尚未取得语音确认结果")
    if baseline_sitting <= 0 and sitting_minutes > 0:
        uncertainty.append("缺少个人坐姿基线，未使用个体化比较")
    if device_issues:
        uncertainty.append("设备不可用期间不能据此判断老人状态正常")
    if voice_check_status == "error":
        uncertainty.append("语音确认服务失败，不能解释为老人未响应")

    if severity == "critical":
        actions = [
            {"action": "notify_caregiver", "priority": 1},
            {
                "action": "activate_pre_authorized_urgent_escalation",
                "priority": 2,
                "note": "按已审批的联系人和紧急处置策略执行",
            },
        ]
    elif severity == "high":
        if voice_check_unknown:
            actions.append(
                {
                    "action": "voice_check",
                    "priority": 1,
                    "timeout_sec": int(operations["voice_check_timeout_sec"]),
                }
            )
        actions.append({"action": "notify_caregiver", "priority": len(actions) + 1})
    elif severity == "medium" and device_issues:
        actions = [{"action": "notify_device_operator", "priority": 1}]
    elif severity == "medium":
        actions = [
            {
                "action": "voice_check",
                "priority": 1,
                "timeout_sec": int(operations["voice_check_timeout_sec"]),
            },
            {"action": "notify_monitoring_console", "priority": 2},
        ]
    elif severity == "low":
        actions = [
            {
                "action": "schedule_recheck",
                "priority": 1,
                "timeout_sec": int(operations["alert_cooldown_sec"]),
            }
        ]
    else:
        actions = [{"action": "retain_audit_record", "priority": 1}]

    hypotheses: list[dict[str, Any]] = []
    if distress_count > 0:
        count_factor = min(1.0, distress_count / max(1, thresholds["distress_alert_count"]))
        hypotheses.append(
            {
                "type": "possible_discomfort_or_pain",
                "confidence": _round_confidence(distress_confidence * (0.6 + 0.4 * count_factor)),
            }
        )
    if low_motion:
        hypotheses.append(
            {
                "type": "possible_mobility_difficulty",
                "confidence": _round_confidence(low_motion_confidence * 0.8),
            }
        )
    if fall_high or floor_confidence >= thresholds["fall_high_confidence"]:
        hypotheses.append(
            {
                "type": "possible_fall",
                "confidence": _round_confidence(max(fall_confidence, floor_confidence)),
            }
        )
    if device_issues:
        hypotheses.append({"type": "monitoring_coverage_loss", "confidence": 0.9})

    model_review_required = (
        severity == "medium"
        and not device_issues
        and distress_confidence < thresholds["model_review_confidence_below"]
    )
    if severity in {"high", "critical"}:
        model_reason = "确定性规则已达到处置等级，模型不得延迟或降低该结果"
    elif model_review_required:
        model_reason = "非高危信号仍有歧义，可在不阻塞处置的前提下复核"
    else:
        model_reason = "当前策略不需要模型复核"

    policy_version = str(policy["policy_version"])
    return {
        "assessment_id": _stable_assessment_id(event["event_id"], policy_version),
        "event_id": event["event_id"],
        "severity": severity,
        "alert_required": severity in {"medium", "high", "critical"},
        "summary": summary,
        "risk_hypotheses": hypotheses,
        "evidence": evidence,
        "uncertainty": uncertainty,
        "recommended_actions": actions,
        "triggered_rules": triggered_rules,
        "model_review": {"required": model_review_required, "reason": model_reason},
        "policy_version": policy_version,
        "not_a_diagnosis": True,
    }

