#!/usr/bin/env python3
"""Normalize the inspected Joys360 health-data response without leaking its token."""

from __future__ import annotations

import argparse
import hashlib
import html
import json
import math
import re
import shutil
import subprocess
import sys
import unicodedata
import urllib.error
import urllib.parse
import urllib.request
from pathlib import Path
from typing import Any


MAX_BYTES = 2 * 1024 * 1024
MAX_ITEMS = 256
MAX_SOURCE_LABEL_CHARS = 160
MAX_URL_CHARS = 8192
MAX_FULL_REPORT_METRICS = 120
MAX_NOTE_ENTRIES_PER_METRIC = 64
ALLOWED_HOST = "www.joys360.com"
ALLOWED_PATH = "/interface-server/api/getOneMachineHealthDataTCM"


POINTS: dict[str, tuple[str, str | None, str]] = {
    "PHYSIO_SCORE": ("生理指标评分", None, "proprietary_score"),
    "ABSI": ("体型指数", None, "model_point"),
    "HPT_RISK_PROB": ("高血压风险估算", "%", "model_risk_estimate"),
    "WEIGHT": ("估算体重", "kg", "estimated_anthropometric"),
    "RISKS_SCORE": ("疾病风险评分", None, "proprietary_score"),
    "HEALTH_SCORE": ("综合健康评分", None, "proprietary_score"),
    "GENDER": ("预测性别（未映射编码）", None, "estimated_demographic"),
    "FLD_RISK_PROB": ("脂肪肝风险估算", "%", "model_risk_estimate"),
    "BMI_CALC": ("估算BMI", "kg/m²", "estimated_anthropometric"),
    "WAIST_CIRCUM": ("估算腰围", "cm", "estimated_anthropometric"),
    "BP_HEART_ATTACK": ("心脏病发作风险估算", "%", "model_risk_estimate"),
    "BP_CVD": ("心血管疾病风险估算", "%", "model_risk_estimate"),
    "HR_BPM": ("心率估算", "beats/min", "device_estimate"),
    "CVD_MULTI_YEAR_RISK_YEARS": ("心血管风险时间范围", "years", "model_metadata"),
    "TEMPERATURE_SENSOR": ("体温估算", "°C", "device_estimate"),
    "BP_STROKE": ("中风风险估算", "%", "model_risk_estimate"),
    "AGE_CVM": ("心血管代谢年龄估算", "years", "model_point"),
    "SNR": ("信噪比", "dB", "quality_metadata"),
    "IHB_COUNT": ("不规则心跳计数估算", "count", "device_estimate"),
    "HDLTC_RISK_PROB": ("高胆固醇血症风险估算", "%", "model_risk_estimate"),
    "HRV_SDNN": ("心率变异性估算（SDNN）", "ms", "device_estimate"),
    "BP_RPP": ("心脏负荷模型点", None, "model_point"),
    "WAIST_TO_HEIGHT": ("估算腰高比", "%", "estimated_anthropometric"),
    "BP_TAU": ("血管功能模型点", None, "model_point"),
    "BP_SYSTOLIC": ("收缩压估算", "mmHg", "device_estimate"),
    "MENTAL_SCORE": ("心理指标评分", None, "proprietary_score"),
    "CVD_MULTI_YEAR_RISK_PROBS": ("多年心血管疾病风险估算", "%", "model_risk_estimate"),
    "HBA1C_RISK_PROB": ("糖化血红蛋白升高风险估算", "%", "model_risk_estimate"),
    "OVERALL_METABOLIC_RISK_PROB": ("整体代谢健康风险估算", "%", "model_risk_estimate"),
    "MFBG_RISK_PROB": ("空腹血糖升高风险估算", "%", "model_risk_estimate"),
    "TG_RISK_PROB": ("高甘油三酯血症风险估算", "%", "model_risk_estimate"),
    "SLEEP_QUALITY": ("睡眠质量指数", None, "proprietary_score"),
    "PHYSICAL_SCORE": ("身体指标评分", None, "proprietary_score"),
    "ANXIETY_INDEX": ("焦虑指数", None, "proprietary_score"),
    "VITALITY": ("活力指数", None, "proprietary_score"),
    "HEIGHT": ("估算身高", "cm", "estimated_anthropometric"),
    "MSI": ("精神压力指数", None, "proprietary_score"),
    "BP_DIASTOLIC": ("舒张压估算", "mmHg", "device_estimate"),
    "AGE": ("皮肤年龄估算", "years", "model_point"),
    "VITAL_SCORE": ("生命体征评分", None, "proprietary_score"),
    "DBT_RISK_PROB": ("2型糖尿病风险估算", "%", "model_risk_estimate"),
    "BR_BPM": ("呼吸频率估算", "breaths/min", "device_estimate"),
}

DEVICE_IDS = (
    "BP_SYSTOLIC",
    "BP_DIASTOLIC",
    "HR_BPM",
    "BR_BPM",
    "TEMPERATURE_SENSOR",
    "HRV_SDNN",
    "IHB_COUNT",
)
ANTHROPOMETRIC_IDS = ("HEIGHT", "WEIGHT", "BMI_CALC", "WAIST_CIRCUM", "WAIST_TO_HEIGHT")
# This is a stable display order, not a severity ranking. These model outputs
# describe different outcomes and must never be compared by numeric magnitude.
RISK_DISPLAY_IDS = (
    "HPT_RISK_PROB",
    "BP_CVD",
    "BP_HEART_ATTACK",
    "BP_STROKE",
    "MFBG_RISK_PROB",
    "HBA1C_RISK_PROB",
    "DBT_RISK_PROB",
    "HDLTC_RISK_PROB",
    "TG_RISK_PROB",
    "FLD_RISK_PROB",
    "OVERALL_METABOLIC_RISK_PROB",
    "CVD_MULTI_YEAR_RISK_PROBS",
)
ACTIONABLE_EVIDENCE_CLASSES = {
    "device_estimate",
    "estimated_anthropometric",
    "model_risk_estimate",
    "proprietary_score",
    "model_point",
}

# Device/lab-only data cannot establish a TCM syndrome. These maintained
# domains make the missing TCM portion visible by routing the report to
# low-confidence questions and care principles without treating a model score
# as evidence that a particular syndrome is present.
TCM_SCREENING_DOMAINS: tuple[dict[str, Any], ...] = (
    {
        "direction": "脾胃运化、痰湿与湿热方向待辨",
        "axes": "脾气虚、脾阳虚、痰湿内蕴、湿热内蕴、食积或胃热",
        "ids": frozenset(
            {
                "WEIGHT",
                "BMI_CALC",
                "WAIST_CIRCUM",
                "WAIST_TO_HEIGHT",
                "FLD_RISK_PROB",
                "HBA1C_RISK_PROB",
                "MFBG_RISK_PROB",
                "DBT_RISK_PROB",
                "HDLTC_RISK_PROB",
                "TG_RISK_PROB",
                "OVERALL_METABOLIC_RISK_PROB",
            }
        ),
        "reasoning": "先用食欲、餐后腹胀和便溏辨脾虚；再用畏寒喜暖辨阳虚，用身重痰多和苔厚腻辨痰湿，用口苦尿黄和苔黄腻辨湿热，并以嗳腐吞酸或消谷善饥鉴别食积与胃热。",
        "questions": "重点补充食欲与餐后腹胀、身重困倦、口黏口苦、痰、大便黏滞或便溏、怕冷怕热、嗳气反酸及舌质舌苔。",
        "care": (
            "规律三餐和睡眠，减少酒精、含糖饮料、夜宵及过量甜腻油炸食物，并按体力逐步增加日常活动。",
            "不依赖单一“祛湿食物”、极端断食或泻下排湿；若后续见明显虚寒或湿热线索，再由合格中医师个体化调整。",
        ),
    },
    {
        "direction": "肝的疏泄、心神与气血阴阳方向待辨",
        "axes": "肝郁气滞、肝郁化火、心神不宁、痰热扰心、心血虚、心脾两虚或心阴虚",
        "ids": frozenset(
            {
                "MENTAL_SCORE",
                "SLEEP_QUALITY",
                "ANXIETY_INDEX",
                "VITALITY",
                "MSI",
                "HRV_SDNN",
            }
        ),
        "reasoning": "先看症状是否随情绪变化以辨肝郁，再以急躁口苦辨郁火，以胸闷痰多和烦扰辨痰热，以心悸健忘、纳差乏力辨心血虚/心脾两虚，以盗汗口干辨心阴虚。",
        "questions": "重点补充情绪诱因、胸胁或胃脘胀闷、叹气、急躁口苦、心悸、胸闷痰多、入睡与多梦早醒、盗汗口干、食欲和白天疲乏。",
        "care": (
            "固定作息和起床时间，午后减少咖啡因，安排温和有氧活动、户外活动、呼吸放松和睡眠记录。",
            "持续明显焦虑、抑郁、失眠或功能受损应同步寻求专业评估，不用酒精、补品或自行服药代替评估。",
        ),
    },
    {
        "direction": "肝阳/肝肾、痰瘀与心气血方向待辨",
        "axes": "肝阳偏亢、肝肾阴虚、痰浊、血瘀、心气虚、心阳虚或气血两虚",
        "ids": frozenset(
            {
                "HPT_RISK_PROB",
                "BP_CVD",
                "BP_HEART_ATTACK",
                "BP_STROKE",
                "CVD_MULTI_YEAR_RISK_PROBS",
                "BP_SYSTOLIC",
                "BP_DIASTOLIC",
                "HR_BPM",
                "IHB_COUNT",
                "BP_RPP",
                "BP_TAU",
            }
        ),
        "reasoning": "先用头胀眩晕、耳鸣急躁辨肝阳/肝肾方向，再以胸闷痰多辨痰浊、固定刺痛和舌暗辨血瘀，以活动后心悸气短、自汗或畏寒辨心气/心阳不足。",
        "questions": "先规范复测，再补充头痛眩晕耳鸣与急躁、胸闷痰多或固定刺痛、心悸气短、自汗畏寒、面色唇舌颜色及活动诱因。",
        "care": (
            "先完成标准血压和心血管评估；在医学评估允许范围内避免久坐、逐步活动，并记录症状与活动关系。",
            "避免自行使用活血、温补或降压类中药；服用抗凝/抗血小板药者尤其不要自行使用丹参、三七、红花等产品。",
        ),
    },
)
# Narrow machine-edge guards for raw vital-sign estimates. These are not
# clinical cutoffs and are never displayed as normal/abnormal thresholds; they
# only request a prompt validated remeasurement when a value sits very close
# to the script's broad corruption boundary. Count/variability metrics are
# deliberately excluded because a percentage-of-range rule is not meaningful.
PROMPT_REMEASUREMENT_GUARDS: dict[str, tuple[float, float]] = {
    "BP_SYSTOLIC": (30, 340),
    "BP_DIASTOLIC": (15, 240),
    "HR_BPM": (15, 290),
    "BR_BPM": (2, 95),
    "TEMPERATURE_SENSOR": (21, 49),
}

# Broad machine-data sanity bounds only. They quarantine impossible or corrupt
# payloads; they are deliberately not diagnostic or treatment thresholds.
PLAUSIBLE_RANGES: dict[str, tuple[float, float]] = {
    "BP_SYSTOLIC": (20, 350),
    "BP_DIASTOLIC": (10, 250),
    "HR_BPM": (10, 300),
    "BR_BPM": (1, 100),
    "TEMPERATURE_SENSOR": (20, 50),
    "HRV_SDNN": (0, 1000),
    "IHB_COUNT": (0, 10000),
    "HEIGHT": (50, 250),
    "WEIGHT": (1, 500),
    "BMI_CALC": (5, 100),
    "WAIST_CIRCUM": (20, 300),
    "WAIST_TO_HEIGHT": (0, 200),
    "AGE": (0, 130),
    "AGE_CVM": (0, 130),
    "CVD_MULTI_YEAR_RISK_YEARS": (1, 100),
}

UNRELIABLE_NOTE_CODES = {
    "NOTE_SNR_BELOW_THRESHOLD",
    "NOTE_FT_LIVENSSS_FAILED",
    "NOTE_MODEL_LIVENSSS_FAILED",
}
LIMITED_NOTE_CODES = {
    "NOTE_USED_PRED_DEMOG",
    "NOTE_MISSING_MEDICAL_INFO",
    "NOTE_DEGRADED_ACCURACY",
}


NOTE_MEANINGS = {
    "NOTE_USED_PRED_DEMOG": "缺少用户填写资料，计算使用了预测的人口学信息",
    "NOTE_MISSING_MEDICAL_INFO": "未提供医疗史问卷信息",
    "NOTE_DEGRADED_ACCURACY": "信号计算准确度下降",
    "NOTE_SNR_BELOW_THRESHOLD": "信噪比低于供应商阈值",
    "NOTE_FT_LIVENSSS_FAILED": "第一次活体检测失败",
    "NOTE_MODEL_LIVENSSS_FAILED": "第二次活体检测失败",
    "NOTE_UNRECOGNIZED": "返回了无法安全识别的质量标记",
}
KNOWN_NOTE_CODES = frozenset(NOTE_MEANINGS)


# The public endpoint currently has two response shapes. Older responses expose
# abbrName/tabList, while newer responses may contain only a localized name and
# score. Keep both so useful data is not discarded when the presentation field
# changes. The two "未获取" rows are resolved by position only after the full
# 42-row canonical shape has been confirmed with multiple anchors.
NAME_TO_POINT_ID = {
    "生理指标": "PHYSIO_SCORE",
    "体型指数": "ABSI",
    "高血压风险": "HPT_RISK_PROB",
    "预测体重": "WEIGHT",
    "疾病风险": "RISKS_SCORE",
    "综合健康评分": "HEALTH_SCORE",
    "预测性别": "GENDER",
    "脂肪肝风险": "FLD_RISK_PROB",
    "体重指数BMI": "BMI_CALC",
    "腰围": "WAIST_CIRCUM",
    "心脏病风险": "BP_HEART_ATTACK",
    "心血管疾病风险": "BP_CVD",
    "心率(bpm)": "HR_BPM",
    "体温": "TEMPERATURE_SENSOR",
    "中风风险": "BP_STROKE",
    "心血管代谢年龄": "AGE_CVM",
    "信噪比": "SNR",
    "不规则心跳": "IHB_COUNT",
    "高胆固醇血症风险": "HDLTC_RISK_PROB",
    "心率变异性": "HRV_SDNN",
    "心脏负荷": "BP_RPP",
    "腰围身高比": "WAIST_TO_HEIGHT",
    "血管功能": "BP_TAU",
    "收缩压": "BP_SYSTOLIC",
    "心理指标": "MENTAL_SCORE",
    "多年心血管疾病风险": "CVD_MULTI_YEAR_RISK_PROBS",
    "糖化血红蛋白水平高于5.7%的风险": "HBA1C_RISK_PROB",
    "整体代谢健康风险": "OVERALL_METABOLIC_RISK_PROB",
    "空腹血糖水平高于5.5mmol/L的风险": "MFBG_RISK_PROB",
    "高甘油三酯血症风险": "TG_RISK_PROB",
    "睡眠质量指数": "SLEEP_QUALITY",
    "身体指标": "PHYSICAL_SCORE",
    "焦虑指数": "ANXIETY_INDEX",
    "活力指数": "VITALITY",
    "精神压力指数": "MSI",
    "舒张压": "BP_DIASTOLIC",
    "皮肤年龄": "AGE",
    "生命体征": "VITAL_SCORE",
    "2 型糖尿病风险": "DBT_RISK_PROB",
    "2型糖尿病风险": "DBT_RISK_PROB",
    "呼吸(brpm)": "BR_BPM",
}

CANONICAL_NAME_ONLY_IDS = tuple(POINTS)
CANONICAL_NAME_ONLY_ANCHORS = {
    0: "生理指标",
    2: "高血压风险",
    12: "心率(bpm)",
    24: "收缩压",
    37: "舒张压",
    41: "呼吸(brpm)",
}


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req: Any, fp: Any, code: int, msg: str, headers: Any, newurl: str) -> None:
        raise urllib.error.HTTPError(req.full_url, code, "redirect rejected", headers, fp)


def validate_url(raw_url: str) -> None:
    if not isinstance(raw_url, str) or not raw_url.strip():
        raise ValueError("URL is empty")
    if len(raw_url) > MAX_URL_CHARS:
        raise ValueError("URL exceeds the supported length")
    # urllib/http.client can include the rejected request target in an
    # InvalidURL traceback. Accept only visible ASCII here so whitespace,
    # controls, and unencoded Unicode never reach that lower-level parser.
    if any(ord(character) < 0x21 or ord(character) > 0x7E for character in raw_url):
        raise ValueError("URL contains unsupported characters")
    if re.search(r"%(?![0-9A-Fa-f]{2})", raw_url):
        raise ValueError("URL contains invalid percent encoding")
    try:
        parsed = urllib.parse.urlsplit(raw_url)
        port = parsed.port
    except ValueError:
        raise ValueError("URL is not valid") from None
    if parsed.scheme.lower() != "https":
        raise ValueError("only HTTPS is allowed")
    if parsed.username or parsed.password:
        raise ValueError("URL user information is not allowed")
    if (parsed.hostname or "").lower() != ALLOWED_HOST:
        raise ValueError("unsupported host")
    if port not in (None, 443):
        raise ValueError("unsupported port")
    if parsed.path != ALLOWED_PATH:
        raise ValueError("unsupported endpoint path")
    if parsed.fragment:
        raise ValueError("URL fragments are not allowed")


def fetch_json(raw_url: str, timeout: float) -> Any:
    validate_url(raw_url)
    if not math.isfinite(timeout) or timeout < 1 or timeout > 60:
        raise ValueError("timeout must be between 1 and 60 seconds")
    request = urllib.request.Request(
        raw_url,
        headers={"Accept": "application/json", "User-Agent": "med-guid/1.0"},
    )
    opener = urllib.request.build_opener(NoRedirect())
    try:
        with opener.open(request, timeout=timeout) as response:
            payload = response.read(MAX_BYTES + 1)
    except urllib.error.HTTPError as exc:
        raise RuntimeError(f"fetch failed with HTTP status {exc.code}") from None
    except (urllib.error.URLError, TimeoutError):
        payload = fetch_with_curl(raw_url, timeout)
    except Exception:
        # Do not let urllib/http.client implementation errors expose a token
        # bearing request target through their exception text or traceback.
        raise RuntimeError("fetch failed because the endpoint request was rejected") from None
    if len(payload) > MAX_BYTES:
        raise ValueError("response exceeds the 2 MiB limit")
    try:
        return json.loads(payload.decode("utf-8-sig"))
    except UnicodeDecodeError:
        raise ValueError("response is not valid UTF-8 JSON") from None
    except json.JSONDecodeError as exc:
        raise ValueError(f"response is not valid JSON at line {exc.lineno}") from None


def fetch_with_curl(raw_url: str, timeout: float) -> bytes:
    curl = shutil.which("curl")
    if not curl:
        raise RuntimeError("fetch failed because the endpoint was unreachable")
    seconds = max(1, int(math.ceil(timeout)))
    escaped_url = raw_url.replace("\\", "\\\\").replace('"', '\\"')
    curl_config = f'url = "{escaped_url}"\n'.encode("utf-8")
    try:
        completed = subprocess.run(
            [
                curl,
                "--silent",
                "--show-error",
                "--fail",
                "--proto",
                "=https",
                "--max-redirs",
                "0",
                "--connect-timeout",
                str(seconds),
                "--max-time",
                str(seconds),
                "--max-filesize",
                str(MAX_BYTES),
                "--header",
                "Accept: application/json",
                "--header",
                "User-Agent: med-guid/1.0",
                "--config",
                "-",
            ],
            input=curl_config,
            capture_output=True,
            check=False,
            timeout=seconds + 5,
        )
    except (OSError, subprocess.TimeoutExpired):
        raise RuntimeError("fetch failed because the endpoint was unreachable") from None
    if completed.returncode != 0:
        raise RuntimeError("fetch failed because the endpoint was unreachable")
    return completed.stdout


def load_json(path: Path) -> Any:
    if not path.is_file():
        raise ValueError("input path must be a regular file")
    with path.open("rb") as handle:
        payload = handle.read(MAX_BYTES + 1)
    if len(payload) > MAX_BYTES:
        raise ValueError("input file exceeds the 2 MiB limit")
    try:
        return json.loads(payload.decode("utf-8-sig"))
    except UnicodeDecodeError:
        raise ValueError("input file is not valid UTF-8 JSON") from None
    except json.JSONDecodeError as exc:
        raise ValueError(f"input file is not valid JSON at line {exc.lineno}") from None


def to_number(value: Any) -> int | float | None:
    if value is None or isinstance(value, bool):
        return None
    try:
        number = float(str(value).strip())
    except (TypeError, ValueError):
        return None
    if not math.isfinite(number):
        return None
    return int(number) if number.is_integer() else number


def sanitize_source_label(value: Any) -> str:
    text = unicodedata.normalize("NFKC", str(value or ""))
    text = "".join(character for character in text if character in "\t " or unicodedata.category(character)[0] != "C")
    text = re.sub(r"\s+", " ", text).strip()
    return text[:MAX_SOURCE_LABEL_CHARS]


def normalized_name_key(value: Any) -> str:
    return re.sub(r"\s+", "", sanitize_source_label(value)).casefold()


def point_id_from_name(source_label: str) -> str:
    key = normalized_name_key(source_label)
    for name, point_id in NAME_TO_POINT_ID.items():
        if normalized_name_key(name) == key:
            return point_id
    return ""


def opaque_unknown_point_id(raw_point_id: str) -> str:
    digest = hashlib.sha256(raw_point_id.encode("utf-8")).hexdigest()[:16].upper()
    return f"UNKNOWN_{digest}"


def safe_source_label(source_label: str, point_id: str) -> str:
    # Only retain an allow-listed metric name that maps back to the resolved
    # point. Arbitrary labels may contain names, identifiers, URLs, or tokens.
    return source_label if source_label and point_id_from_name(source_label) == point_id else ""


def parse_metric_value(point_id: str, evidence_class: str, raw_value: Any) -> tuple[int | float | None, str]:
    if raw_value is None or (isinstance(raw_value, str) and not raw_value.strip()):
        return None, "missing"
    value = to_number(raw_value)
    if value is None:
        return None, "not_numeric"
    supported_range = PLAUSIBLE_RANGES.get(point_id)
    if supported_range is None and evidence_class == "model_risk_estimate":
        supported_range = (0, 100)
    if supported_range is not None and not supported_range[0] <= float(value) <= supported_range[1]:
        return None, "outside_supported_range"
    if abs(float(value)) > 1_000_000_000:
        return None, "outside_supported_range"
    return value, "valid"


def remeasurement_priority(metric: dict[str, Any]) -> str | None:
    if metric.get("evidence_class") != "device_estimate":
        return None
    point_id = str(metric.get("id") or "")
    status = str(metric.get("value_status") or "missing")
    if status == "outside_supported_range":
        return "prompt" if point_id in PROMPT_REMEASUREMENT_GUARDS else "confirm_source"
    if status != "valid":
        return "confirm_source"
    value = metric.get("value")
    guard = PROMPT_REMEASUREMENT_GUARDS.get(point_id)
    if guard is not None and isinstance(value, (int, float)) and not isinstance(value, bool):
        lower_guard, upper_guard = guard
        if float(value) <= lower_guard or float(value) >= upper_guard:
            return "prompt"
    return "routine"


def metric_quality_level(notes: list[str], value_status: str, evidence_class: str) -> str:
    note_set = set(notes)
    if UNRELIABLE_NOTE_CODES.intersection(note_set):
        return "unreliable"
    if value_status != "valid" or LIMITED_NOTE_CODES.intersection(note_set) or "NOTE_UNRECOGNIZED" in note_set:
        return "limited"
    if evidence_class in {"unknown_model_point", "quality_metadata", "model_metadata", "estimated_demographic"}:
        return "context_only"
    return "screening_only"


def fact_sha256(
    metrics: list[dict[str, Any]],
    quality_notes: list[str],
    issues: list[dict[str, Any]],
    coverage_summary: dict[str, int],
) -> str:
    facts = {
        "metrics": [
            {
                "id": metric.get("id"),
                "label": metric.get("label"),
                "source_label": metric.get("source_label"),
                "value": metric.get("value"),
                "unit": metric.get("unit"),
                "evidence_class": metric.get("evidence_class"),
                "value_status": metric.get("value_status"),
                "quality_level": metric.get("quality_level"),
                "quality_notes": metric.get("quality_notes") or [],
                "remeasurement_priority": metric.get("remeasurement_priority"),
            }
            for metric in metrics
        ],
        "quality_notes": quality_notes,
        "normalization_issues": issues,
        "coverage": coverage_summary,
    }
    encoded = json.dumps(facts, ensure_ascii=False, sort_keys=True, separators=(",", ":")).encode("utf-8")
    return hashlib.sha256(encoded).hexdigest()


def parse_notes(value: Any) -> tuple[list[str], int, int]:
    if value is None:
        return [], 0, 0
    raw = value if isinstance(value, list) else str(value).split(",")
    notes: list[str] = []
    unrecognized_entries = 0
    for item in raw:
        item_text = str(item)
        if len(item_text) > 160:
            code = "NOTE_UNRECOGNIZED"
            unrecognized_entries += 1
        else:
            code = unicodedata.normalize("NFKC", item_text).strip().upper()
        if not code:
            continue
        if not re.fullmatch(r"[A-Z0-9_]{1,80}", code) or code not in KNOWN_NOTE_CODES:
            code = "NOTE_UNRECOGNIZED"
            unrecognized_entries += 1
        if code not in notes:
            notes.append(code)
    overflow = max(0, len(raw) - MAX_NOTE_ENTRIES_PER_METRIC)
    return notes, overflow, unrecognized_entries


def is_canonical_name_only_response(items: list[Any]) -> bool:
    if len(items) != len(CANONICAL_NAME_ONLY_IDS):
        return False
    for index, expected_name in CANONICAL_NAME_ONLY_ANCHORS.items():
        item = items[index]
        if not isinstance(item, dict) or normalized_name_key(item.get("name")) != normalized_name_key(expected_name):
            return False
    return True


def normalize(document: Any) -> dict[str, Any]:
    if not isinstance(document, dict):
        raise ValueError("top-level JSON must be an object")
    normalization_issues: list[dict[str, Any]] = []
    header = document.get("header")
    if header is None:
        normalization_issues.append(
            {"code": "missing_header", "count": 1, "meaning_zh": "响应缺少状态头，来源完整性需要复核"}
        )
    elif not isinstance(header, dict):
        raise ValueError("header must be an object when present")
    elif "code" not in header:
        normalization_issues.append(
            {"code": "missing_status_code", "count": 1, "meaning_zh": "响应状态头缺少应用状态码"}
        )
    else:
        application_code = to_number(header.get("code"))
        if application_code is None:
            raise ValueError("endpoint returned an invalid application status")
        if application_code != 0:
            raise ValueError("endpoint returned a non-zero application status")
    body = document.get("body")
    items = body.get("list") if isinstance(body, dict) else None
    if not isinstance(items, list):
        raise ValueError("expected body.list to be an array")
    if len(items) > MAX_ITEMS:
        raise ValueError(f"body.list exceeds the supported {MAX_ITEMS}-item limit")

    metrics_by_id: dict[str, dict[str, Any]] = {}
    metric_order: list[str] = []
    all_notes: list[str] = []
    ignored = 0
    duplicates = 0
    duplicate_ids: list[str] = []
    quality_note_overflow = 0
    unrecognized_quality_notes = 0
    canonical_name_only = is_canonical_name_only_response(items)
    for index, item in enumerate(items):
        if not isinstance(item, dict):
            ignored += 1
            continue
        source_label = sanitize_source_label(item.get("name"))
        raw_point_id = sanitize_source_label(item.get("abbrName")).upper()
        if raw_point_id and not re.fullmatch(r"[A-Z0-9_]{1,100}", raw_point_id):
            raw_point_id = ""
        name_point_id = point_id_from_name(source_label)
        if raw_point_id in POINTS:
            point_id = raw_point_id
        elif name_point_id:
            point_id = name_point_id
        elif raw_point_id:
            point_id = opaque_unknown_point_id(raw_point_id)
        else:
            point_id = ""
        if not point_id and canonical_name_only:
            point_id = CANONICAL_NAME_ONLY_IDS[index]
        if not point_id:
            ignored += 1
            continue
        notes, note_overflow, unknown_note_count = parse_notes(item.get("tabList"))
        quality_note_overflow += note_overflow
        unrecognized_quality_notes += unknown_note_count
        all_notes.extend(notes)
        label, unit, kind = POINTS.get(
            point_id,
            ("未分类模型指标", None, "unknown_model_point"),
        )
        value, value_status = parse_metric_value(point_id, kind, item.get("score"))
        candidate = {
            "id": point_id,
            "label": label,
            "source_label": safe_source_label(source_label, point_id),
            "value": value,
            "unit": unit,
            "evidence_class": kind,
            "value_status": value_status,
            "quality_notes": notes,
            "quality_level": metric_quality_level(notes, value_status, kind),
        }
        existing = metrics_by_id.get(point_id)
        if existing is None:
            metrics_by_id[point_id] = candidate
            metric_order.append(point_id)
            continue

        duplicates += 1
        if point_id not in duplicate_ids:
            duplicate_ids.append(point_id)
        merged_notes = list(dict.fromkeys((existing.get("quality_notes") or []) + notes))
        existing["quality_notes"] = merged_notes
        if existing.get("value") != value or existing.get("value_status") != value_status:
            existing["value"] = None
            existing["value_status"] = "conflicting_duplicate"
        existing["quality_level"] = metric_quality_level(
            merged_notes,
            str(existing.get("value_status") or "missing"),
            str(existing.get("evidence_class") or "unknown_model_point"),
        )

    metrics = [metrics_by_id[point_id] for point_id in metric_order]
    for metric in metrics:
        metric["remeasurement_priority"] = remeasurement_priority(metric)
    unique_notes = list(dict.fromkeys(all_notes))
    valid_values = sum(metric.get("value_status") == "valid" for metric in metrics)
    actionable_values = sum(
        metric.get("value_status") == "valid" and metric.get("evidence_class") in ACTIONABLE_EVIDENCE_CLASSES
        for metric in metrics
    )
    invalid_values = len(metrics) - valid_values
    unknown_metrics = sum(metric.get("evidence_class") == "unknown_model_point" for metric in metrics)
    if ignored:
        normalization_issues.append(
            {"code": "ignored_items", "count": ignored, "meaning_zh": "部分条目因结构或标识无法识别而未纳入解释"}
        )
    if duplicates:
        normalization_issues.append(
            {
                "code": "duplicate_metric_ids",
                "count": duplicates,
                "metric_ids": duplicate_ids,
                "meaning_zh": "发现重复指标；数值冲突的指标已隔离，不用于报告结论",
            }
        )
    if invalid_values:
        normalization_issues.append(
            {
                "code": "invalid_or_missing_values",
                "count": invalid_values,
                "meaning_zh": "部分数值缺失、非数字、冲突或超出机器数据支持范围，已标记为待确认",
            }
        )
    if unknown_metrics:
        normalization_issues.append(
            {
                "code": "unknown_metric_ids",
                "count": unknown_metrics,
                "meaning_zh": "部分指标标识没有受维护的语义映射，仅作为未分类模型指标保留",
            }
        )
    if unrecognized_quality_notes:
        normalization_issues.append(
            {
                "code": "unrecognized_quality_notes",
                "count": unrecognized_quality_notes,
                "meaning_zh": "未知质量标记已折叠为 NOTE_UNRECOGNIZED，并按信息有限处理",
            }
        )
    if quality_note_overflow:
        normalization_issues.append(
            {
                "code": "quality_note_overflow",
                "count": quality_note_overflow,
                "meaning_zh": "过量质量标记已折叠为受控摘要，没有逐条进入报告",
            }
        )

    if not metrics or actionable_values == 0:
        quality_level = "insufficient"
    elif UNRELIABLE_NOTE_CODES.intersection(unique_notes):
        quality_level = "unreliable"
    elif (
        LIMITED_NOTE_CODES.intersection(unique_notes)
        or normalization_issues
        or "NOTE_UNRECOGNIZED" in unique_notes
    ):
        quality_level = "limited"
    else:
        quality_level = "screening_only"

    coverage_summary = {
        "input_items": len(items),
        "parsed_metrics": len(metrics),
        "valid_values": valid_values,
        "actionable_values": actionable_values,
        "invalid_values": invalid_values,
        "unknown_metrics": unknown_metrics,
        "ignored_items": ignored,
        "duplicate_items": duplicates,
    }
    result = {
        "schema_version": "med-guid.joys360.v2",
        "source": "Joys360 / DeepAffex-style point response",
        "clinical_use": "screening_only_not_diagnostic",
        "quality": {
            "level": quality_level,
            "notes": [
                {"code": code, "meaning_zh": NOTE_MEANINGS.get(code, "未知供应商质量标记")}
                for code in unique_notes
            ],
            "normalization_issues": normalization_issues,
        },
        "metrics": metrics,
        "coverage": coverage_summary,
        "ignored_items": ignored,
        "duplicate_items": duplicates,
        "normalization_notes": [
            "Values come from score; scaled internal ovalue fields are intentionally ignored.",
            "Unknown source fields are ignored to avoid propagating identifiers or access data.",
            "Risk percentages are model estimates, not laboratory values or diagnoses.",
            "Numeric sanity bounds quarantine corrupt machine data and are not clinical cutoffs.",
        ],
    }
    result["fact_sha256"] = fact_sha256(metrics, unique_notes, normalization_issues, coverage_summary)
    return result


EVIDENCE_CLASS_ZH = {
    "device_estimate": "设备估算",
    "estimated_anthropometric": "体型估算",
    "estimated_demographic": "人口学估算",
    "model_risk_estimate": "模型风险估算",
    "proprietary_score": "专有评分",
    "model_point": "模型指标",
    "model_metadata": "模型元数据",
    "quality_metadata": "质量信息",
    "unknown_model_point": "未分类模型指标",
}

QUALITY_LEVEL_ZH = {
    "screening_only": "仅适合筛查参考",
    "context_only": "仅作背景信息",
    "limited": "信息有限，需标准复核",
    "unreliable": "结果可靠性不足，不应用于判断",
    "insufficient": "有效信息不足",
}

VALUE_STATUS_ZH = {
    "valid": "可显示的设备返回值",
    "missing": "数值缺失",
    "not_numeric": "数值格式无效",
    "outside_supported_range": "超出机器数据支持范围",
    "conflicting_duplicate": "重复条目数值冲突",
}


def display_value(metric: dict[str, Any]) -> str:
    value = metric.get("value")
    if value is None or metric.get("value_status") != "valid":
        return "待确认"
    unit = metric.get("unit")
    return f"{value} {unit}" if unit else str(value)


def result_metrics(result: dict[str, Any]) -> list[dict[str, Any]]:
    metrics = result.get("metrics") if isinstance(result, dict) else None
    return [metric for metric in metrics or [] if isinstance(metric, dict)]


def valid_metrics(result: dict[str, Any]) -> list[dict[str, Any]]:
    return [metric for metric in result_metrics(result) if metric.get("value_status") == "valid"]


def actionable_metrics(result: dict[str, Any]) -> list[dict[str, Any]]:
    return [
        metric
        for metric in valid_metrics(result)
        if metric.get("evidence_class") in ACTIONABLE_EVIDENCE_CLASSES
    ]


def metrics_by_id(result: dict[str, Any]) -> dict[str, dict[str, Any]]:
    return {str(metric.get("id")): metric for metric in result_metrics(result) if metric.get("id")}


def ordered_metrics(result: dict[str, Any], point_ids: tuple[str, ...]) -> list[dict[str, Any]]:
    by_id = {str(metric.get("id")): metric for metric in valid_metrics(result) if metric.get("id")}
    return [by_id[point_id] for point_id in point_ids if point_id in by_id]


def ordered_risk_metrics(result: dict[str, Any]) -> list[dict[str, Any]]:
    metrics = valid_metrics(result)
    by_id = {str(metric.get("id")): metric for metric in metrics if metric.get("id")}
    ordered = [by_id[point_id] for point_id in RISK_DISPLAY_IDS if point_id in by_id]
    seen = {str(metric.get("id")) for metric in ordered}
    ordered.extend(
        metric
        for metric in metrics
        if metric.get("evidence_class") == "model_risk_estimate" and str(metric.get("id")) not in seen
    )
    return ordered


def quality_level(result: dict[str, Any]) -> str:
    quality = result.get("quality") if isinstance(result, dict) else None
    return str(quality.get("level") or "insufficient") if isinstance(quality, dict) else "insufficient"


def quality_notes(result: dict[str, Any]) -> list[dict[str, Any]]:
    quality = result.get("quality") if isinstance(result, dict) else None
    notes = quality.get("notes") if isinstance(quality, dict) else None
    return [note for note in notes or [] if isinstance(note, dict)]


def normalization_issues(result: dict[str, Any]) -> list[dict[str, Any]]:
    quality = result.get("quality") if isinstance(result, dict) else None
    issues = quality.get("normalization_issues") if isinstance(quality, dict) else None
    return [issue for issue in issues or [] if isinstance(issue, dict)]


def coverage(result: dict[str, Any]) -> dict[str, int]:
    raw = result.get("coverage") if isinstance(result, dict) else None
    if not isinstance(raw, dict):
        return {}
    output: dict[str, int] = {}
    for key, value in raw.items():
        if isinstance(value, (int, float)) and not isinstance(value, bool):
            output[str(key)] = max(0, int(value))
    return output


def metric_summary(metrics: list[dict[str, Any]], limit: int) -> str:
    return "、".join(f"{metric.get('label', '未命名指标')} {display_value(metric)}" for metric in metrics[:limit])


def confirmation_actions(result: dict[str, Any]) -> list[str]:
    all_metrics = result_metrics(result)
    metrics = actionable_metrics(result)
    device_metrics = [metric for metric in all_metrics if metric.get("evidence_class") == "device_estimate"]
    prompt_remeasurement = any(metric.get("remeasurement_priority") == "prompt" for metric in device_metrics)
    present = {str(metric.get("id")) for metric in metrics}
    actions: list[str] = []
    if prompt_remeasurement:
        actions.append(
            "本次设备值接近或超出脚本的机器数据边界；这不是临床分级。请立即用经过验证的标准设备规范复测，并根据当前症状、复测结果和当地医疗服务建议决定是否需要及时就医。"
        )
    elif present.intersection(DEVICE_IDS):
        actions.append("用经过验证的标准设备按规范复测本次涉及的生命体征，并保存连续记录。")
    elif device_metrics:
        actions.append("设备数值缺失、冲突或格式不可用；请重新测量或重新获取原始结果，不要猜测数值。")
    if not metrics:
        actions.extend(
            [
                "重新获取原始结果或提供可读的报告页面；当前没有可用于结论的有效指标。",
                "补充当前症状、开始时间和变化；如正在明显不适，不要等待设备报告。",
                "如无法获得可靠复测或症状正在加重，请联系当地医疗服务，由其根据症状决定就诊地点。",
            ]
        )
        return list(dict.fromkeys(actions))
    if present.intersection(ANTHROPOMETRIC_IDS):
        actions.append("直接测量身高、体重和腰围，不用本次体型估算替代真实测量。")
    if present.intersection({"MFBG_RISK_PROB", "HBA1C_RISK_PROB", "DBT_RISK_PROB", "OVERALL_METABOLIC_RISK_PROB"}):
        actions.append("结合症状和病史，与医生讨论是否需要标准空腹血糖和 HbA1c 等确认检查。")
    if present.intersection({"HDLTC_RISK_PROB", "TG_RISK_PROB"}):
        actions.append("与医生讨论是否需要标准血脂检查，不把模型百分比当作血脂结果。")
    if "FLD_RISK_PROB" in present:
        actions.append("如临床情况支持，与医生讨论肝功能及是否需要影像确认。")
    if present.intersection({"HPT_RISK_PROB", "BP_CVD", "BP_HEART_ATTACK", "BP_STROKE", "CVD_MULTI_YEAR_RISK_PROBS"}):
        actions.append("结合真实年龄、病史、用药和规范血压测量，由医生评估心血管相关提示。")
    actions.append("没有警示症状时，可先看全科医学科、家庭医生或普通内科，统一安排必要复核。")
    return list(dict.fromkeys(actions))


def clinician_questions(result: dict[str, Any]) -> list[str]:
    usable = actionable_metrics(result)
    if not usable:
        return [
            "这份资料为什么不能用于判断，需要怎样重新测量或补充？",
            "哪些正在发生的症状会改变就诊地点或紧急程度？",
        ]
    present = {str(metric.get("id")) for metric in usable}
    questions = ["哪些设备或模型提示值得优先用标准检查确认？"]
    if present.intersection({"BP_SYSTOLIC", "BP_DIASTOLIC", "HPT_RISK_PROB"}):
        questions.append("我是否需要家庭血压监测或动态血压监测？")
    if present.intersection({"MFBG_RISK_PROB", "HBA1C_RISK_PROB", "DBT_RISK_PROB"}):
        questions.append("结合我的实际情况，血糖和 HbA1c 应如何安排？")
    if present.intersection({"HDLTC_RISK_PROB", "TG_RISK_PROB", "FLD_RISK_PROB"}):
        questions.append("是否需要血脂、肝功能或进一步影像检查？")
    questions.append("哪些症状或复测结果会改变当前随访计划？")
    return list(dict.fromkeys(questions))


def tcm_screening_candidates(result: dict[str, Any]) -> list[dict[str, Any]]:
    usable = actionable_metrics(result)
    present = {str(metric.get("id")) for metric in usable if metric.get("id")}
    candidates: list[dict[str, Any]] = []
    for domain in TCM_SCREENING_DOMAINS:
        domain_ids = domain["ids"]
        matched = [metric for metric in usable if str(metric.get("id")) in domain_ids]
        if not matched:
            continue
        labels = "、".join(str(metric.get("label") or "未命名指标") for metric in matched[:3])
        if len(matched) > 3:
            labels += f"等 {len(matched)} 项"
        candidates.append(
            {
                "direction": str(domain["direction"]),
                "axes": str(domain["axes"]),
                "basis": f"当前资料覆盖“{labels}”所在的数据域；这里只决定辨证追问方向，不表示这些值已确认异常。",
                "reasoning": str(domain["reasoning"]),
                "questions": str(domain["questions"]),
                "care": list(domain["care"]),
            }
        )
    return candidates[:3]


def tcm_screening_lines(result: dict[str, Any], *, compact: bool = False) -> list[str]:
    usable = actionable_metrics(result)
    candidates = tcm_screening_candidates(result)
    if not usable:
        if compact:
            return ["中医初步辨证：有效资料不足，暂不能列候选证候方向；需先重测并补充寒热、饮食二便、睡眠、舌象和临床脉象。"]
        return [
            "- 辨证层级：信息不足。当前没有可用健康事实，不能形成资料型待辨方向。",
            "- 需要补充：寒热、汗与口渴、食欲腹胀、二便、睡眠精力、疼痛特点、舌象和由合格中医师判断的脉象。",
        ]
    if compact:
        if not candidates:
            return ["中医初步辨证：现有指标未落入受维护的辨证数据域，需结合症状、病程、舌象和临床脉象判断。"]
        directions = "；".join(candidate["direction"] for candidate in candidates)
        return [f"中医初步辨证：C 级资料型，可优先辨别“{directions}”；不按严重度排序，也不是已确立证型。"]

    lines = [
        "- 辨证层级：C 级（仅设备/模型资料）。以下为资料型待辨方向，不是已确立证型或中医面诊确诊，也不按严重程度排序。",
        "- 总体诊断思路：现有资料只能决定下一步从八纲（寒热、虚实）、病位、气血津液和脏腑兼夹逐层追问；缺少症状、病程、舌象和临床脉象的环节均暂不定证。",
    ]
    if not candidates:
        lines.append("- 当前指标未落入受维护的辨证数据域；请结合症状、病程、舌象和临床脉象再判断。")
        return lines
    for index, candidate in enumerate(candidates, start=1):
        lines.extend(
            [
                f"- 待辨方向 {index}：{candidate['direction']}。",
                f"  - 细分候选：{candidate['axes']}。这些是需要相互鉴别的方向，不是一个复合证型。",
                f"  - 资料依据：{candidate['basis']}",
                f"  - 辨证路径：{candidate['reasoning']}",
                f"  - 关键鉴别：{candidate['questions']}",
            ]
        )
    return lines


def tcm_care_lines(result: dict[str, Any], limit: int = 2) -> list[str]:
    candidates = tcm_screening_candidates(result)
    if not candidates:
        return ["- 先补充四诊和症状资料；在未辨证前不自行使用中药方、温补/清热产品或针刺。"]
    lines = [
        "- 通用基础：保持三餐和睡眠规律、食物多样、减少烟酒与过量精制糖、避免久坐，在可耐受范围内逐步活动并记录睡眠、精力、食欲、二便和症状变化。"
    ]
    for candidate in candidates[:limit]:
        lines.append(f"- 针对“{candidate['direction']}”：")
        lines.extend(f"  - {care}" for care in candidate["care"])
    lines.append("- 穴位保健只作舒缓选择：可轻柔按揉足三里、内关、神门、印堂或太冲，每处约 1–2 分钟，以不痛、不留瘀青为度；皮肤破损、孕期、出血风险或严重基础疾病者先咨询专业人员，不自行针刺。")
    lines.append("- 若希望进一步辨证，请把上述鉴别问题、舌照、病程、既往病史和全部药物/保健品清单带给合格中医师；不要按本报告自行配方用药或针刺。")
    return lines


RED_FLAG_SENTENCE = (
    "如同时出现新发严重胸部压迫感、明显呼吸困难、持续晕厥、突发一侧无力或言语不清、"
    "突然严重意识改变、无法控制的大出血、严重过敏反应，或当前有伤害自己的想法等警示情况，"
    "应立即联系当地急救或前往急诊；有自伤风险时不要独处。"
)


def render_standard_report(result: dict[str, Any]) -> str:
    metrics = result_metrics(result)
    usable_metrics = actionable_metrics(result)
    device_metrics = ordered_metrics(result, DEVICE_IDS)
    anthropometric_metrics = ordered_metrics(result, ANTHROPOMETRIC_IDS)
    risk_metrics = ordered_risk_metrics(result)
    invalid_metrics = [metric for metric in metrics if metric.get("value_status") != "valid"]
    level = quality_level(result)
    summary = coverage(result)

    lines = [
        "# 健康数据解读与就医准备报告（标准版）",
        "",
        "## 结论先行",
        (
            "这是一组筛查用途的设备/模型估算，不是医院检验或诊断。"
            if usable_metrics
            else "当前响应没有提供可用的健康指标，不能据此解释疾病风险或安排特定检查。"
        ),
        "",
        "## 一、是什么",
        "",
        "- 紧急程度：仅凭设备结果无法分级；是否紧急取决于当前症状和标准复测。",
        f"- 数据用途：{result.get('clinical_use', 'screening_only_not_diagnostic')}",
        f"- 数据质量：{QUALITY_LEVEL_ZH.get(level, level)}",
        f"- 指标数量：{len(metrics)}",
    ]
    if summary:
        lines.append(
            "- 数据覆盖："
            f"输入 {summary.get('input_items', 0)} 项，解析 {summary.get('parsed_metrics', len(metrics))} 项，"
            f"有效值 {summary.get('valid_values', 0)} 项，待确认 {summary.get('invalid_values', 0)} 项。"
        )
    for note in quality_notes(result):
        lines.append(f"- 质量提示：{note.get('code', 'UNKNOWN')} — {note.get('meaning_zh', '未知含义')}")
    for issue in normalization_issues(result):
        lines.append(f"- 解析提示：{issue.get('meaning_zh', '存在需要复核的数据问题')}（{issue.get('count', 0)} 项）")

    if not usable_metrics:
        lines.extend(["", "### 当前限制", "", "- 没有取得可用于结论的有效指标；请重新获取原始结果或提供清晰的报告内容。"])

    if device_metrics:
        lines.extend(["", "### 设备估算值（不作临床正常/异常分级）", "", "| 指标 | 数值 | 证据类型 |", "|---|---:|---|"])
        for metric in device_metrics:
            lines.append(f"| {markdown_cell(metric['label'])} | {markdown_cell(display_value(metric))} | 设备估算 |")

    if anthropometric_metrics:
        lines.extend(["", "### 估算体型数据（需直接测量确认）", "", "| 指标 | 数值 | 证据类型 |", "|---|---:|---|"])
        for metric in anthropometric_metrics:
            lines.append(f"| {markdown_cell(metric['label'])} | {markdown_cell(display_value(metric))} | 体型估算 |")

    if risk_metrics:
        lines.extend(
            [
                "",
                "### 模型风险估算（按固定指标顺序展示，不作跨指标高低排序）",
                "",
                "| 指标 | 数值 | 证据类型 |",
                "|---|---:|---|",
            ]
        )
        for metric in risk_metrics[:6]:
            lines.append(f"| {markdown_cell(metric['label'])} | {markdown_cell(display_value(metric))} | 模型风险估算 |")
        if len(risk_metrics) > 6:
            lines.append(f"\n其余 {len(risk_metrics) - 6} 项模型输出见完整版附录；省略不代表不重要。")

    if invalid_metrics:
        lines.extend(["", "### 待确认数据", "", "| 指标 | 原因 |", "|---|---|"])
        for metric in invalid_metrics[:8]:
            status = str(metric.get("value_status") or "missing")
            lines.append(
                f"| {markdown_cell(metric.get('label', '未命名指标'))} | "
                f"{markdown_cell(VALUE_STATUS_ZH.get(status, '数值不可用'))} |"
            )
        if len(invalid_metrics) > 8:
            lines.append(f"\n另有 {len(invalid_metrics) - 8} 项待确认数据，详见完整版附录。")

    lines.extend(["", "### 中医初步辨证（资料型 · C 级）", ""])
    lines.extend(tcm_screening_lines(result))

    lines.extend(
        [
            "",
            "## 二、为什么",
            "",
            "- 这些结果来自同一次设备信号和相关模型，彼此可能相关，不能当作多项独立证据相互印证。",
            "- 风险百分比是模型输出，不是血糖、糖化血红蛋白、血脂、肝功能等化验值，也不能直接证明已经患病。",
            "- 接口本身未包含医疗史或使用了预测人口学信息时，模型输入可能受影响；如用户另有提供，应与本接口事实合并判断。出现准确度下降提示时，更需要标准设备或临床检验复核。",
            "- 专有健康、心理、活力和风险评分没有通用临床阈值，因此本报告不对它们作好坏或高低判断。",
            "- 中医部分只按现有数据域安排辨证方向；主证仍需症状、病程、舌象和临床脉象支持，不能把模型百分比直接翻译成证型。",
            "",
            "## 三、怎么办",
            "",
        ]
    )
    for index, action in enumerate(confirmation_actions(result), start=1):
        lines.append(f"{index}. {action}")
    lines.extend(["", "### 中医调护与面诊准备", ""])
    lines.extend(tcm_care_lines(result))
    lines.extend(["", "### 可以问医生", ""])
    lines.extend(f"- {question}" for question in clinician_questions(result)[:4])
    lines.extend(
        [
            "",
            "### 会立即改变计划的警示症状",
            "",
            f"- {RED_FLAG_SENTENCE}",
            "",
            "本报告没有使用临床阈值进行疾病分级，仅用于就医准备，不构成诊断、处方或医疗证明。",
        ]
    )
    return "\n".join(lines)


def render_concise_report(result: dict[str, Any]) -> str:
    metrics = result_metrics(result)
    usable_metrics = actionable_metrics(result)
    device = ordered_metrics(result, ("BP_SYSTOLIC", "BP_DIASTOLIC", "HR_BPM"))
    body = ordered_metrics(result, ("BMI_CALC", "WAIST_CIRCUM", "WEIGHT"))
    risk_metrics = ordered_risk_metrics(result)
    issues = normalization_issues(result)
    findings: list[str] = []
    if quality_notes(result) or issues:
        findings.append("存在设备质量或数据解析限制，相关结果应先复测确认。")
    if not usable_metrics:
        findings.append("本次没有取得可用于结论的有效指标。")
    if device:
        findings.append("设备估算：" + metric_summary(device, 3))
    if body:
        findings.append("体型估算：" + metric_summary(body, 3))
    if risk_metrics:
        findings.append("部分模型输出（固定顺序、不作高低比较）：" + metric_summary(risk_metrics, 3))
    if not findings:
        findings.append("本次没有取得可解读的健康指标。")

    level = quality_level(result)
    lines = [
        "# 健康数据解读（精简版）",
        "",
        "## 是什么",
        "",
        f"这是筛查用途的设备/模型结果，共 {len(metrics)} 项、有效值 {len(usable_metrics)} 项；数据质量为“{QUALITY_LEVEL_ZH.get(level, level)}”，不能直接当作医院检验或诊断。",
        "紧急程度：仅凭设备结果无法分级，取决于当前症状和标准复测。",
    ]
    lines.extend(f"- {finding}" for finding in findings[:3])
    lines.extend(tcm_screening_lines(result, compact=True))
    lines.extend(
        [
            "",
            "## 为什么",
            "",
            "- 设备数值和体型数据是估算值，需要标准器械直接复测。",
            "- 各风险百分比对应不同模型终点，不能横向比较，也不等于化验结果或已经患病。",
            "",
            "## 怎么办",
            "",
        ]
    )
    actions = confirmation_actions(result)
    selected_actions = ["出现严重胸部不适、明显呼吸困难、突发神经功能异常等警示症状时，立即联系当地急救或前往急诊。"]
    if actions:
        selected_actions.append(actions[0])
    if len(actions) > 1:
        selected_actions.append(actions[-1])
    for index, action in enumerate(list(dict.fromkeys(selected_actions))[:3], start=1):
        lines.append(f"{index}. {action}")
    return "\n".join(lines)


def markdown_cell(value: Any) -> str:
    rendered = "" if value is None else str(value)
    rendered = html.escape(rendered.replace("\r", " ").replace("\n", " "), quote=False)
    # Encode Markdown structure as entities. This keeps table content readable
    # without allowing an untrusted label to become an image, link, or code.
    structural = frozenset("\\`*_{}[]()#+-.!|")
    return "".join(f"&#{ord(character)};" if character in structural else character for character in rendered)


def render_full_report(result: dict[str, Any]) -> str:
    metrics = result_metrics(result)
    usable_metrics = actionable_metrics(result)
    device_metrics = ordered_metrics(result, DEVICE_IDS)
    anthropometric_metrics = ordered_metrics(result, ANTHROPOMETRIC_IDS)
    risk_metrics = ordered_risk_metrics(result)
    level = quality_level(result)
    summary = coverage(result)
    fact_hash = str(result.get("fact_sha256") or "未提供")
    lines = [
        "# 健康资料解读与就医准备报告（完整版）",
        "",
        "## 结论先行",
        (
            "现有内容属于筛查设备和模型估算；最可行动的步骤是先确认数据质量，再用标准测量或临床检查复核，不能据此直接诊断。"
            if usable_metrics
            else "当前没有取得可用于结论的有效指标；应先重新获取原始资料，并根据正在发生的症状决定是否需要及时就医。"
        ),
        "",
        "## 一、是什么",
        "",
        "### 紧急程度",
        "",
        "- 级别：当前资料不足以仅凭设备结果分级。",
        "- 依据：没有当前症状、标准复测和临床查体；设备或模型极端值本身不能确认或排除急症。",
        "",
        "### 资料与可信度",
        "",
        f"- 资料来源：{result.get('source', 'Joys360 / DeepAffex-style point response')}",
        f"- 数据质量：{QUALITY_LEVEL_ZH.get(level, level)}",
        f"- 事实快照 SHA-256：`{fact_hash}`",
        "- 本接口结果未包含、但会改变判断的信息：当前症状及开始时间、真实年龄和相关人口学资料、既往病史、用药/过敏史、标准复测结果；如用户已另行提供，应合并使用而不是视为缺失。",
    ]
    if summary:
        lines.append(
            "- 覆盖情况："
            f"输入 {summary.get('input_items', 0)} 项；解析 {summary.get('parsed_metrics', len(metrics))} 项；"
            f"有效值 {summary.get('valid_values', 0)} 项；待确认 {summary.get('invalid_values', 0)} 项；"
            f"忽略 {summary.get('ignored_items', 0)} 项；重复 {summary.get('duplicate_items', 0)} 项。"
        )
    for note in quality_notes(result):
        lines.append(f"- 设备质量提示：{note.get('code', 'UNKNOWN')} — {note.get('meaning_zh', '未知含义')}")
    for issue in normalization_issues(result):
        lines.append(f"- 数据解析提示：{issue.get('meaning_zh', '存在需要复核的数据问题')}（{issue.get('count', 0)} 项）")

    lines.extend(
        [
            "",
            "### 重点发现",
            "",
            "| 处理优先级 | 发现 | 证据类型 | 通俗解释 | 不确定性 |",
            "|---|---|---|---|---|",
        ]
    )
    if not usable_metrics:
        lines.append("| 先补资料 | 没有可用指标 | 待确认 | 当前无法形成数据结论 | 需要重新获取原始结果 |")
    for metric in device_metrics[:4]:
        status = str(metric.get("value_status") or "missing")
        lines.append(
            f"| 先标准复测 | {markdown_cell(metric.get('label'))} {markdown_cell(display_value(metric))} | 设备估算 | "
            f"设备返回的筛查值 | {markdown_cell(VALUE_STATUS_ZH.get(status, '仍需标准设备确认'))} |"
        )
    for metric in anthropometric_metrics[:3]:
        status = str(metric.get("value_status") or "missing")
        lines.append(
            f"| 先直接测量 | {markdown_cell(metric.get('label'))} {markdown_cell(display_value(metric))} | 体型估算 | "
            f"不能替代实际身高、体重或腰围 | {markdown_cell(VALUE_STATUS_ZH.get(status, '仍需直接测量'))} |"
        )
    for metric in risk_metrics[:6]:
        status = str(metric.get("value_status") or "missing")
        lines.append(
            f"| 结合病史确认 | {markdown_cell(metric.get('label'))} {markdown_cell(display_value(metric))} | 模型风险估算 | "
            f"模型输出，不是化验值或患病概率 | {markdown_cell(VALUE_STATUS_ZH.get(status, '模型适用性和输入信息未知'))} |"
        )

    lines.extend(["", "### 中医初步辨证（资料型 · C 级）", ""])
    lines.extend(tcm_screening_lines(result))

    department_guidance = (
        "- 没有警示症状且涉及多个系统时，优先全科医学科、家庭医生或普通内科；仅在标准复核或症状支持时再转相应专科。"
        if usable_metrics
        else "- 仅凭当前无效资料不能指定科室；有症状时按症状联系当地医疗服务，无症状时先补齐原始资料。"
    )
    lines.extend(
        [
            "",
            "## 二、为什么",
            "",
            "- 支持的事实只有接口明确返回的筛查值及质量标记；本报告没有把缺失的人口学资料、症状或病史补写成事实。",
            "- 同一次信号可派生多个相关模型结果，不能把这些输出视为多份独立证据，也不能按百分比大小跨指标比较严重程度。",
            "- 设备生命体征、体型估算和专有评分均需要各自适当的标准测量或临床检查确认。",
            "- 当前没有标准检验、重复测量或临床评估可与设备结果相互印证，因此不能确认疾病、排除疾病或确定紧急程度。",
            "- 中医部分属于 C 级资料型待辨方向；它只确定下一步应问哪些四诊问题，不把设备域映射成主证。",
            "",
            "## 三、怎么办",
            "",
            "### 下一步",
            "",
        ]
    )
    for index, action in enumerate(confirmation_actions(result), start=1):
        lines.append(f"{index}. {action}")
    lines.extend(["", "### 中医调护与面诊准备", ""])
    lines.extend(tcm_care_lines(result, limit=3))
    lines.extend(
        [
            "",
            "### 建议首诊科室",
            "",
            department_guidance,
            "",
            "### 就诊时携带",
            "",
            "- 原始报告、测量时间和条件、连续标准复测记录、既往病史和家族史、当前用药与过敏清单。",
            "",
            "### 问医生的问题",
            "",
        ]
    )
    lines.extend(f"- {question}" for question in clinician_questions(result))
    lines.extend(
        [
            "",
            "### 需要立即改变计划的警示症状",
            "",
            f"- {RED_FLAG_SENTENCE}",
            "",
            "## 适用范围与依据",
            "",
            "- 本报告用于解释设备返回内容和准备就医，不构成诊断、处方、医疗证明或急诊排除意见。",
            "- 本报告没有应用临床诊断阈值；数值边界只用于隔离明显损坏的机器数据，不用于正常/异常分级。",
            "- 证据语义来自报告中的 Joys360 / DeepAffex-style 点标识；具体照护决策应以适用地区的当前临床指南和医生评估为准。",
            "",
            "## 附录：全部指标",
            "",
            "以下按接口返回顺序列出；证据类型不因数值大小而改变。",
            "",
            "| 序号 | 指标 | 数值 | 数值状态 | 证据类型 | 单项质量 |",
            "|---:|---|---:|---|---|---|",
        ]
    )
    visible_metrics = metrics[:MAX_FULL_REPORT_METRICS]
    for index, metric in enumerate(visible_metrics, start=1):
        evidence = EVIDENCE_CLASS_ZH.get(str(metric.get("evidence_class") or ""), "其他")
        status = VALUE_STATUS_ZH.get(str(metric.get("value_status") or "missing"), "数值不可用")
        item_quality = QUALITY_LEVEL_ZH.get(
            str(metric.get("quality_level") or ""),
            str(metric.get("quality_level") or "未分类"),
        )
        lines.append(
            f"| {index} | {markdown_cell(metric.get('label'))} | {markdown_cell(display_value(metric))} | "
            f"{markdown_cell(status)} | {markdown_cell(evidence)} | {markdown_cell(item_quality)} |"
        )
    if len(metrics) > len(visible_metrics):
        lines.append(
            f"\n- 附录为控制输出长度仅显示前 {len(visible_metrics)} 项；其余 {len(metrics) - len(visible_metrics)} 项保留在 JSON 事实快照中。"
        )
    lines.extend(
        [
            "",
            f"- 已解析指标：{len(metrics)} 项；未识别条目：{int(result.get('ignored_items') or 0)} 项；重复条目：{int(result.get('duplicate_items') or 0)} 项。",
        ]
    )
    return "\n".join(lines)


def render_report(result: dict[str, Any], mode: str = "standard") -> str:
    if mode not in {"concise", "standard", "full"}:
        raise ValueError("unsupported report mode")
    if mode == "concise":
        return render_concise_report(result)
    if mode == "full":
        return render_full_report(result)
    return render_standard_report(result)


def read_url_stdin() -> str:
    raw = sys.stdin.read(MAX_URL_CHARS + 1)
    if len(raw) > MAX_URL_CHARS:
        raise ValueError("URL exceeds the supported length")
    raw = raw.strip()
    if not raw:
        raise ValueError("URL stdin is empty")
    return raw


def read_url_file(path: Path) -> str:
    if not path.is_file():
        raise ValueError("URL input path must be a regular file")
    with path.open("r", encoding="utf-8-sig") as handle:
        raw = handle.read(MAX_URL_CHARS + 1)
    if len(raw) > MAX_URL_CHARS:
        raise ValueError("URL exceeds the supported length")
    raw = raw.strip()
    if not raw:
        raise ValueError("URL input file is empty")
    return raw


def public_error_message(exc: BaseException) -> str:
    if isinstance(exc, OSError):
        return "input or network data could not be read"
    return str(exc)


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description=__doc__)
    source = parser.add_mutually_exclusive_group(required=True)
    source.add_argument("--url-stdin", action="store_true", help="Read the sensitive endpoint URL from standard input")
    source.add_argument("--url-file", type=Path, help="Read the sensitive endpoint URL from a protected local file")
    # Parse the legacy flag only so it can fail with a constant, non-echoing
    # error instead of argparse printing the sensitive argument back to logs.
    source.add_argument("--url", dest="legacy_url", help=argparse.SUPPRESS)
    source.add_argument("--file", type=Path, help="Downloaded Joys360 JSON file")
    parser.add_argument("--timeout", type=float, default=20.0, help="Fetch timeout in seconds (default: 20)")
    parser.add_argument(
        "--format",
        choices=("json", "report", "bundle"),
        default="json",
        help="Output normalized JSON, a report, or a report plus its reusable fact snapshot",
    )
    parser.add_argument(
        "--mode",
        choices=("concise", "standard", "full"),
        default="standard",
        help="Report depth: concise, standard (default), or full",
    )
    return parser


def main() -> int:
    if hasattr(sys.stdout, "reconfigure"):
        sys.stdout.reconfigure(encoding="utf-8", errors="replace")
    if hasattr(sys.stderr, "reconfigure"):
        sys.stderr.reconfigure(encoding="utf-8", errors="replace")
    args = build_parser().parse_args()
    try:
        if args.url_stdin:
            document = fetch_json(read_url_stdin(), args.timeout)
        elif args.url_file:
            document = fetch_json(read_url_file(args.url_file), args.timeout)
        elif args.legacy_url:
            raise ValueError("direct URL arguments are disabled; use protected URL input")
        else:
            document = load_json(args.file)
        output = normalize(document)
    except (OSError, RuntimeError, ValueError) as exc:
        print(json.dumps({"ok": False, "error": public_error_message(exc)}, ensure_ascii=False), file=sys.stderr)
        return 2
    if args.format == "report":
        print(render_report(output, args.mode))
    elif args.format == "bundle":
        reports = {mode: render_report(output, mode) for mode in ("concise", "standard", "full")}
        print(
            json.dumps(
                {
                    "ok": True,
                    "mode": args.mode,
                    "fact_sha256": output.get("fact_sha256"),
                    "result": output,
                    "report": reports[args.mode],
                    "reports": reports,
                },
                ensure_ascii=False,
                indent=2,
            )
        )
    else:
        print(json.dumps({"ok": True, "result": output}, ensure_ascii=False, indent=2))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
