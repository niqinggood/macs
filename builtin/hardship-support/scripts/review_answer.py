#!/usr/bin/env python3
"""Deterministic lint for hardship-support drafts.

The script reports evidence and safety defects; it never rewrites the answer.
It uses only the Python standard library and prints a JSON result while keeping
exit status zero so a model can inspect failures, repair, and rerun.
"""

from __future__ import annotations

import argparse
import base64
from decimal import Decimal, InvalidOperation
import hashlib
import json
from pathlib import Path
import re
import sys
from typing import Iterable


NUMBER = r"\d[\d,，]*(?:\.\d+)?"
NUMBER_RE = re.compile(rf"(?<![A-Za-z0-9_])({NUMBER})(\s*[%％])?")
UNIT_RE = re.compile(r"^\s*(个百分点|个工作日|工作日|个月|小时|分钟|万元|亿元|元/月|元／月|元|天|日|周|月|年|期|次|笔)")


def normalized_number(raw: str) -> str:
    try:
        value = Decimal(raw.replace(",", "").replace("，", ""))
    except InvalidOperation:
        return raw
    rendered = format(value, "f")
    if "." in rendered:
        rendered = rendered.rstrip("0").rstrip(".")
    return rendered or "0"


def numbers_in(text: str) -> set[str]:
    return {normalized_number(match.group(1)) for match in NUMBER_RE.finditer(text)}


def normalized_unit(text: str, match: re.Match[str]) -> str:
    if match.group(2):
        return "%"
    unit_match = UNIT_RE.match(text[match.end():])
    if not unit_match:
        return ""
    unit = unit_match.group(1)
    if unit in ("元/月", "元／月"):
        return "元"
    return unit


def number_units_in(text: str) -> dict[str, set[str]]:
    result: dict[str, set[str]] = {}
    for match in NUMBER_RE.finditer(text):
        value = normalized_number(match.group(1))
        unit = normalized_unit(text, match)
        if unit:
            result.setdefault(value, set()).add(unit)
    return result


def first_number_after(text: str, patterns: Iterable[str]) -> Decimal | None:
    for pattern in patterns:
        match = re.search(pattern, text, flags=re.I | re.S)
        if not match:
            continue
        try:
            return Decimal(match.group(1).replace(",", "").replace("，", ""))
        except InvalidOperation:
            continue
    return None


def decimal_token(value: Decimal) -> str:
    return normalized_number(format(value, "f"))


def task_cashflow(task: str) -> tuple[set[str], tuple[str, str, str, str, str] | None]:
    income = first_number_after(task, [
        rf"(?:每月|月)?(?:到手|税后|可支配)?(?:收入|工资)[^\d]{{0,12}}({NUMBER})",
        rf"(?:每月|月)到手[^\d]{{0,8}}({NUMBER})",
    ])
    necessary = first_number_after(task, [
        rf"(?:必要|基本)(?:生活)?支出[^\d]{{0,12}}({NUMBER})",
        rf"生活(?:必要|基本)?支出[^\d]{{0,12}}({NUMBER})",
    ])
    fixed = first_number_after(task, [
        rf"(?:每月|月)?最低(?:还款|还)[^\d]{{0,12}}({NUMBER})",
        rf"(?:固定|刚性)还款[^\d]{{0,12}}({NUMBER})",
    ])
    derived: set[str] = set()
    if income is None or necessary is None or fixed is None:
        return derived, None
    post_expense = income - necessary
    post_fixed = post_expense - fixed
    values = tuple(decimal_token(item) for item in (income, necessary, fixed, post_expense, post_fixed))
    derived.update((values[3], values[4]))
    return derived, values


def issue(code: str, message: str, matches: list[str] | None = None) -> dict[str, object]:
    result: dict[str, object] = {"code": code, "message": message}
    if matches:
        result["matches"] = matches[:8]
    return result


def excerpt(text: str, start: int, end: int, radius: int = 45) -> str:
    left = max(0, start - radius)
    right = min(len(text), end + radius)
    return re.sub(r"\s+", " ", text[left:right]).strip()


def number_is_list_marker(line: str, start: int, end: int) -> bool:
    prefix = line[:start]
    suffix = line[end:]
    if re.fullmatch(r"\s*(?:#{1,6}\s*)?(?:[-+*>]\s*)?", prefix) and re.match(r"\s*[.、)）:]\s+", suffix):
        return True
    if re.fullmatch(r"\s*\|\s*", prefix) and re.match(r"\s*\|", suffix):
        return True
    return False


def unsupported_numbers(answer: str, allowed: set[str], allowed_units: dict[str, set[str]]) -> list[str]:
    found: list[str] = []
    for line in answer.splitlines():
        for match in NUMBER_RE.finditer(line):
            value = normalized_number(match.group(1))
            unit = normalized_unit(line, match)
            if number_is_list_marker(line, match.start(), match.end()):
                continue
            if value in allowed and (not unit or unit in allowed_units.get(value, set())):
                continue
            found.append(re.sub(r"\s+", " ", line).strip())
    return list(dict.fromkeys(item for item in found if item))[:12]


def matching_excerpts(answer: str, pattern: str) -> list[str]:
    return [excerpt(answer, match.start(), match.end()) for match in re.finditer(pattern, answer, flags=re.I | re.S)][:8]


def review(task: str, answer: str) -> dict[str, object]:
    task = task.strip()
    answer = answer.strip()
    issues: list[dict[str, object]] = []
    offline = bool(re.search(r"(?:不要|禁止|无需|不许|不需要)(?:再)?(?:联网|上网|搜索|检索)|(?:不联网|离线回答)", task))
    allowed = numbers_in(task)
    allowed_units = number_units_in(task)
    derived, cashflow = task_cashflow(task)
    allowed.update(derived)
    for value in derived:
        allowed_units.setdefault(value, set()).add("元")
    current_cash_known = bool(re.search(rf"(?:当前|现在|眼下|手头|账户)[^。；;\n]{{0,18}}(?:现金|余额)[^。；;\n]{{0,12}}{NUMBER}", task))

    if not answer:
        issues.append(issue("empty_answer", "候选答案为空。"))

    bad_numbers = unsupported_numbers(answer, allowed, allowed_units)
    if bad_numbers:
        issues.append(issue(
            "unsupported_number",
            "答案含用户、材料、实际来源或必要算术之外的数字；待核实标签不能使这些数字合格。",
            bad_numbers,
        ))

    if cashflow is not None:
        income, necessary, fixed, post_expense, post_fixed = cashflow
        compact = re.sub(r"[\s,，元/月（）()\[\]`*_：:]", "", answer)
        first_formula = re.search(rf"{re.escape(income)}[-−—]{re.escape(necessary)}(?:=|＝){re.escape(post_expense)}", compact)
        second_formula = re.search(rf"{re.escape(post_expense)}[-−—]{re.escape(fixed)}(?:=|＝){re.escape(post_fixed)}", compact)
        if not first_formula or not second_formula:
            issues.append(issue(
                "cashflow_formula_missing",
                "必须分别展示收入减必要支出、再减固定还款的两条纯算式。",
            ))

        post_fixed_pattern = re.escape(post_fixed)
        if len(post_fixed) > 3:
            post_fixed_pattern = rf"(?:{re.escape(post_fixed)}|{re.escape(post_fixed[:-3])}[,，]{re.escape(post_fixed[-3:])})"
        allocation_matches = matching_excerpts(
            answer,
            rf"(?:把|将|用){post_fixed_pattern}[^。；;\n]{{0,30}}(?:用在|用于|分给|支付给|还给)|{post_fixed_pattern}[^。；;\n]{{0,38}}(?:给信用卡|给网贷|给亲友|信用卡[^。；;\n]{{0,12}}(?:部分还款|额外还款))",
        )
        if not current_cash_known and allocation_matches:
            issues.append(issue(
                "remainder_allocated_without_cash",
                "扣除固定还款后的余额只是月度资金池；当前现金未知时不能把它指定给某笔债务。",
                allocation_matches,
            ))

    if not current_cash_known and re.search(r"(?:现金|余额)", task + answer):
        has_unknown = any(
            re.search(pattern, answer)
            for pattern in (
                r"(?:当前|现在|眼下|手头|账户)[^。；;\n]{0,18}(?:现金|余额)[^。；;\n]{0,18}(?:未知|未提供|不清楚|无法确定|待核实)",
                r"(?:现金|余额)[^。；;\n]{0,18}(?:未知|未提供|不清楚|无法确定|待核实)",
            )
        )
        if not has_unknown:
            issues.append(issue("current_cash_unknown", "用户未提供当前现金时，答案必须明确保持未知。"))

    if offline:
        offline_names = (
            "国家金融监督管理总局", "中国人民银行征信中心", "央行征信中心", "银保监",
            "云闪付", "国家企业信用信息公示系统", "12345", "12363", "12378",
        )
        matches = [name for name in offline_names if name in answer and name not in task]
        phone_like = matching_excerpts(answer, r"(?<!\d)(?:95\d{2,5}|400[-\s]?\d{3,}|1[012]\d{3,5})(?!\d)")
        urls = [value for value in re.findall(r"https?://[^\s)>]+", answer) if value not in task]
        if matches or phone_like or urls:
            issues.append(issue(
                "offline_current_locator",
                "用户禁止联网时，不得输出记忆中的热线、客服号段、具体当前机构入口或 URL，即使标成待核实。",
                matches + phone_like + urls,
            ))
        product_terms = [term for term in ("停息挂账", "冷静期", "撤销权") if term in answer and term not in task]
        if product_terms:
            issues.append(issue(
                "offline_product_term",
                "用户未提及且本轮未核验的现行产品/权利术语应删除，改为索取书面方案与适用条件。",
                product_terms,
            ))

    debt_labels = [label for label in ("信用卡", "网贷", "亲友") if label in task]
    if debt_labels:
        chinese_counts = {"一": 1, "二": 2, "两": 2, "三": 3, "四": 4, "五": 5, "六": 6, "七": 7, "八": 8, "九": 9, "十": 10}
        wrong_counts = []
        for match in re.finditer(r"([一二两三四五六七八九十]|\d+)\s*笔(?:债务|欠款)", answer):
            raw = match.group(1)
            stated = int(raw) if raw.isdigit() else chinese_counts.get(raw)
            if stated is not None and stated != len(debt_labels):
                wrong_counts.append(excerpt(answer, match.start(), match.end()))
        if wrong_counts:
            issues.append(issue(
                "debt_count_mismatch",
                "答案把非债务事项计入债务笔数，或遗漏了用户明确列出的债务。",
                wrong_counts,
            ))

    if "账单" not in task:
        status_matches = matching_excerpts(answer, r"(?:当前|本期)?已出账单|本期账单已经(?:出|生成)")
        if status_matches:
            issues.append(issue(
                "invented_bill_status",
                "用户没有说明账单已出，不能把最低还款信息升级成已出账单状态。",
                status_matches,
            ))
    rule_patterns = (
        (
            "unsupported_legal_status",
            r"(?:按|视为|说明|表明|可判断)[^。；;\n]{0,28}(?:未进入|不存在|没有进入|尚未进入)[^。；;\n]{0,16}(?:诉讼|仲裁|执行|司法程序|法律程序)",
            "没有收到纸质材料不能推出未进入诉讼、仲裁、执行或电子送达程序。",
        ),
        (
            "payment_promise",
            r"(?:仍会|将会|会|先|继续|保持|维持|保证|确保|按现有)[^。；;\n]{0,32}(?:最低还款|最低额)[^。；;\n]{0,20}(?:履约|支付|还款|付入|转入|留出|锁定|覆盖)|(?:最低还款|最低额)[^。；;\n]{0,24}(?:仍会|将会|会|先|继续|保持|维持|保证|确保)[^。；;\n]{0,16}(?:履约|支付|还款|付入|转入|留出|锁定|覆盖)",
            "月度最低还款基线不能在当前现金和账单状态未知时改写成付款承诺或动作。",
        ),
        (
            "unsupported_credit_outcome",
            r"(?:最低还款|最低额)[^。；;\n]{0,36}(?:维持|保住|稳住|避免|确保)[^。；;\n]{0,24}(?:征信|账户状态|正常状态|逾期|催收|诉讼)",
            "不得声称最低还款必然维持征信、账户状态或避免其他后果。",
        ),
        (
            "due_date_reclassified",
            r"\d+\s*天后[^。；;\n]{0,16}(?:为|就是|属于)[^。；;\n]{0,12}(?:当期还款日|整体到期日|合同到期日)",
            "用户只说几天后到期时，不得擅自重新定性为当期或整体合同到期。",
        ),
        (
            "friend_fixed_schedule",
            r"(?:亲友|朋友)[^。；;\n]{0,80}(?:下月|下个月|每月|每个月)[^。；;\n]{0,30}(?:开始|起|还|支付|转)|(?:下月|下个月|每月|每个月)[^。；;\n]{0,30}(?:开始|起|还|支付|转)[^。；;\n]{0,50}(?:亲友|朋友)",
            "主要债务方案未明确前，不替亲友债制定固定开始月份或月付日程。",
        ),
        (
            "account_identity_absolute",
            r"(?:合同主体|放款主体|还款账户|收款账户|放款与还款|放款和还款)[^。；;\n]{0,48}(?:必须|只能|只可|都要|都走|应当)[^。；;\n]{0,30}(?:同一|一致|对公账户|本人账户|同名账户|持牌机构)",
            "账户与主体核验应以合同和正式通知为准，不能写成统一绝对规则。",
        ),
        (
            "account_history_absolute",
            r"(?:必须|只能|务必)[^。；;\n]{0,20}(?:与|同)[^。；;\n]{0,20}(?:此前|原来|历史)[^。；;\n]{0,12}(?:还款|收款|放款)?账户[^。；;\n]{0,12}(?:一致|相同)",
            "历史账户一致性只能作为核验线索，不能作为统一真实性规则。",
        ),
        (
            "unverified_channel_classification",
            r"(?:非合同|陌生|非官方)[^。；;\n]{0,12}(?:电话|号段|号码)[^。；;\n]{0,24}(?:不接|不回拨|就是|视为|认定|不可信)",
            "不能只按来电号段或是否为合同电话判断真伪；应通过自行确认的渠道回查。",
        ),
        (
            "paper_only_process",
            r"(?:短信|电子通知)[^。；;\n]{0,24}(?:不是|不算|不构成)[^。；;\n]{0,20}(?:正式|送达|文书|程序)",
            "电子通知和送达不能仅按载体一概排除，应核对主体、案号、入口和内容。",
        ),
        (
            "internal_detail",
            r"(?:\.openkitty/|runtime/builtin|reference(?:s)?/|tool_evidence|cashflow_check\.py|内部证据审计|ALLOWED_NUMBERS|DO_NOT_OUTPUT)",
            "最终答案不得暴露内部路径、脚本、审计字段或工具契约。",
        ),
    )
    for code, pattern, message in rule_patterns:
        matches = matching_excerpts(answer, pattern)
        if matches:
            issues.append(issue(code, message, matches))

    return {
        "review_status": "pass" if not issues else "fail",
        "offline": offline,
        "allowed_numbers": sorted(allowed, key=lambda value: (Decimal(value), value)),
        "issues": issues,
    }


def self_test() -> None:
    task = (
        "我每月到手8000元，必要生活支出5000元，信用卡欠20000元，年化约18%，"
        "每月最低还2000元；网贷欠10000元，3天后到期；亲友欠5000元。"
        "请给今天、7天、30天行动。不要联网。"
    )
    bad = """
8000 - 5000 = 3000；3000 - 2000 = 1000。当前账户现金未知。
按未进入诉讼处理。我仍会按2000元最低还款履约，并在24小时内拨打12345。
下个月开始每月给亲友500元。可申请停息挂账。合同主体与收款账户必须是同一持牌机构。
"""
    bad_result = review(task, bad)
    assert bad_result["review_status"] == "fail", bad_result
    bad_codes = {item["code"] for item in bad_result["issues"]}
    assert {"unsupported_number", "offline_current_locator", "unsupported_legal_status", "payment_promise", "friend_fixed_schedule", "offline_product_term", "account_identity_absolute"}.issubset(bad_codes), bad_result

    good = """
## 月度计算
8000 - 5000 = 3000。
3000 - 2000 = 1000。
这是月度测算；当前账户现金余额未知，具体付款待核对账单与实际余额后决定。

## 处理顺序
一、先核对3天后到期的网贷账单与费用并联系贷款机构，不把顺序当成付款指令。
二、核对信用卡账单、到期日和2000元最低还款信息，索取书面方案。
三、向亲友说明目前需要先核账，再共同商量安排，不承诺金额和日期。

## 今天 / 7天 / 30天
今天保存账单和催收短信；7天内整理书面回复；30天内按新账单重新评估。
"""
    good_result = review(task, good)
    assert good_result["review_status"] == "pass", good_result
    print(json.dumps({"self_test": "pass"}, ensure_ascii=False))


def main() -> int:
    parser = argparse.ArgumentParser(description="Lint a hardship-support answer without rewriting it")
    parser.add_argument("--task-base64", help="base64-encoded original user task")
    answer_source = parser.add_mutually_exclusive_group()
    answer_source.add_argument("--answer-file", help="UTF-8 candidate answer file")
    answer_source.add_argument("--answer-stdin", action="store_true", help="read the UTF-8 candidate answer from stdin")
    parser.add_argument("--self-test", action="store_true")
    args = parser.parse_args()
    if args.self_test:
        self_test()
        return 0
    if not args.task_base64 or not (args.answer_file or args.answer_stdin):
        parser.error("--task-base64 and one answer source are required unless --self-test is used")
    try:
        task = base64.b64decode(args.task_base64, validate=True).decode("utf-8")
        answer = sys.stdin.read() if args.answer_stdin else Path(args.answer_file).read_text(encoding="utf-8")
        answer = answer.replace("\r\n", "\n").replace("\r", "\n").strip()
        result = review(task, answer)
        result["output_sha256"] = hashlib.sha256(answer.encode("utf-8")).hexdigest()
    except Exception as exc:  # Keep a machine-readable result for bounded repair.
        result = {
            "review_status": "error",
            "offline": False,
            "allowed_numbers": [],
            "issues": [{"code": "validator_error", "message": str(exc)}],
        }
    print(json.dumps(result, ensure_ascii=False, indent=2))
    return 0


if __name__ == "__main__":
    sys.exit(main())
