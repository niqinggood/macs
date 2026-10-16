#!/usr/bin/env python3
"""Deterministic calculator for software-project effort and quotations.

The calculator does not estimate scope. It validates an already prepared WBS,
aggregates PERT effort, and converts internal delivery costs into a quotation.
It uses only the Python standard library and performs no network access.
"""

from __future__ import annotations

import argparse
import json
import sys
from decimal import (
    Decimal,
    InvalidOperation,
    ROUND_CEILING,
    ROUND_HALF_UP,
    localcontext,
)
from pathlib import Path
from typing import Any, Mapping, Sequence


ZERO = Decimal("0")
ONE = Decimal("1")
SIX = Decimal("6")
MONEY = Decimal("0.01")
DAY = Decimal("0.0001")
RATE = Decimal("0.000001")
Z_SCORES = {
    "expected": ZERO,
    "p80": Decimal("0.8416212336"),
    "p95": Decimal("1.6448536270"),
}


class QuoteInputError(ValueError):
    """Raised when quote input is incomplete or internally inconsistent."""


def _mapping(value: Any, path: str) -> Mapping[str, Any]:
    if not isinstance(value, Mapping):
        raise QuoteInputError(f"{path} must be an object")
    return value


def _sequence(value: Any, path: str) -> Sequence[Any]:
    if isinstance(value, (str, bytes, bytearray)) or not isinstance(value, Sequence):
        raise QuoteInputError(f"{path} must be an array")
    return value


def _required(container: Mapping[str, Any], key: str, path: str) -> Any:
    if key not in container:
        raise QuoteInputError(f"{path}.{key} is required")
    return container[key]


def _text(value: Any, path: str) -> str:
    if not isinstance(value, str) or not value.strip():
        raise QuoteInputError(f"{path} must be a non-empty string")
    return value.strip()


def _decimal(value: Any, path: str) -> Decimal:
    if isinstance(value, bool) or value is None:
        raise QuoteInputError(f"{path} must be a number")
    try:
        result = value if isinstance(value, Decimal) else Decimal(str(value))
    except (InvalidOperation, ValueError):
        raise QuoteInputError(f"{path} must be a valid number") from None
    if not result.is_finite():
        raise QuoteInputError(f"{path} must be finite")
    return result


def _non_negative(value: Any, path: str) -> Decimal:
    result = _decimal(value, path)
    if result < ZERO:
        raise QuoteInputError(f"{path} must be >= 0")
    return result


def _positive(value: Any, path: str) -> Decimal:
    result = _decimal(value, path)
    if result <= ZERO:
        raise QuoteInputError(f"{path} must be > 0")
    return result


def _fraction(value: Any, path: str) -> Decimal:
    result = _decimal(value, path)
    if result < ZERO or result >= ONE:
        raise QuoteInputError(f"{path} must be >= 0 and < 1")
    return result


def _sqrt(value: Decimal) -> Decimal:
    if value <= ZERO:
        return ZERO
    with localcontext() as context:
        context.prec = 40
        return value.sqrt()


def _quantize(value: Decimal, quantum: Decimal) -> Decimal:
    return value.quantize(quantum, rounding=ROUND_HALF_UP)


def _json_number(value: Decimal, quantum: Decimal = MONEY) -> int | float:
    quantized = _quantize(value, quantum)
    if quantized == quantized.to_integral_value():
        return int(quantized)
    return float(quantized)


def _round_quote(value: Decimal, quantum: Decimal, mode: str) -> Decimal:
    if quantum == ZERO:
        return _quantize(value, MONEY)
    rounding = ROUND_CEILING if mode == "up" else ROUND_HALF_UP
    units = (value / quantum).to_integral_value(rounding=rounding)
    return _quantize(units * quantum, MONEY)


def _cost_rows(value: Any, path: str, recurring: bool = False) -> tuple[list[dict[str, Any]], Decimal]:
    if value is None:
        return [], ZERO
    rows: list[dict[str, Any]] = []
    total = ZERO
    for index, raw in enumerate(_sequence(value, path)):
        row_path = f"{path}[{index}]"
        row = _mapping(raw, row_path)
        name = _text(_required(row, "name", row_path), f"{row_path}.name")
        amount = _non_negative(_required(row, "amount", row_path), f"{row_path}.amount")
        output: dict[str, Any] = {"name": name, "amount": _json_number(amount)}
        if recurring:
            output["period"] = _text(_required(row, "period", row_path), f"{row_path}.period")
            if "pricing_note" in row:
                output["pricing_note"] = _text(row["pricing_note"], f"{row_path}.pricing_note")
        else:
            total += amount
            if "category" in row:
                output["category"] = _text(row["category"], f"{row_path}.category")
        rows.append(output)
    return rows, total


def calculate_quote(payload: Mapping[str, Any]) -> dict[str, Any]:
    """Validate and calculate a quote from a parsed JSON object."""

    root = _mapping(payload, "input")
    project = _mapping(_required(root, "project", "input"), "project")
    project_name = _text(_required(project, "name", "project"), "project.name")
    currency = _text(_required(project, "currency", "project"), "project.currency").upper()
    confidence = _text(
        _required(project, "pricing_confidence", "project"),
        "project.pricing_confidence",
    ).lower()
    if confidence not in Z_SCORES:
        raise QuoteInputError("project.pricing_confidence must be expected, p80, or p95")

    raw_rate_card = _mapping(_required(root, "rate_card", "input"), "rate_card")
    if not raw_rate_card:
        raise QuoteInputError("rate_card must contain at least one role")
    rate_card: dict[str, Decimal] = {}
    for raw_role, raw_rate in raw_rate_card.items():
        role = _text(raw_role, "rate_card role")
        if role in rate_card:
            raise QuoteInputError(f"duplicate rate_card role: {role}")
        rate_card[role] = _positive(raw_rate, f"rate_card.{role}")

    raw_items = _sequence(_required(root, "items", "input"), "items")
    if not raw_items:
        raise QuoteInputError("items must contain at least one WBS row")

    item_results: list[dict[str, Any]] = []
    seen_ids: set[str] = set()
    expected_days = ZERO
    days_variance = ZERO
    expected_cost = ZERO
    cost_variance = ZERO

    for index, raw in enumerate(raw_items):
        item_path = f"items[{index}]"
        item = _mapping(raw, item_path)
        item_id = _text(_required(item, "id", item_path), f"{item_path}.id")
        if item_id in seen_ids:
            raise QuoteInputError(f"duplicate WBS item id: {item_id}")
        seen_ids.add(item_id)

        workstream = _text(_required(item, "workstream", item_path), f"{item_path}.workstream")
        deliverable = _text(_required(item, "deliverable", item_path), f"{item_path}.deliverable")
        role = _text(_required(item, "role", item_path), f"{item_path}.role")
        optimistic = _non_negative(_required(item, "optimistic", item_path), f"{item_path}.optimistic")
        most_likely = _non_negative(
            _required(item, "most_likely", item_path), f"{item_path}.most_likely"
        )
        pessimistic = _non_negative(
            _required(item, "pessimistic", item_path), f"{item_path}.pessimistic"
        )
        if not optimistic <= most_likely <= pessimistic:
            raise QuoteInputError(
                f"{item_path} must satisfy optimistic <= most_likely <= pessimistic"
            )
        if pessimistic == ZERO:
            raise QuoteInputError(f"{item_path}.pessimistic must be > 0")

        if "rate_override" in item:
            day_rate = _positive(item["rate_override"], f"{item_path}.rate_override")
            rate_source = "override"
        else:
            if role not in rate_card:
                raise QuoteInputError(
                    f"{item_path}.role {role!r} is missing from rate_card and has no rate_override"
                )
            day_rate = rate_card[role]
            rate_source = "rate_card"

        pert_days = (optimistic + Decimal("4") * most_likely + pessimistic) / SIX
        sigma_days = (pessimistic - optimistic) / SIX
        pert_cost = pert_days * day_rate
        sigma_cost = sigma_days * day_rate

        expected_days += pert_days
        days_variance += sigma_days * sigma_days
        expected_cost += pert_cost
        cost_variance += sigma_cost * sigma_cost

        output_item: dict[str, Any] = {
            "id": item_id,
            "workstream": workstream,
            "deliverable": deliverable,
            "role": role,
            "optimistic_days": _json_number(optimistic, DAY),
            "most_likely_days": _json_number(most_likely, DAY),
            "pessimistic_days": _json_number(pessimistic, DAY),
            "expected_days": _json_number(pert_days, DAY),
            "sigma_days": _json_number(sigma_days, DAY),
            "day_cost": _json_number(day_rate),
            "rate_source": rate_source,
            "expected_cost": _json_number(pert_cost),
        }
        for optional_key in ("status", "assumption", "acceptance"):
            if optional_key in item:
                output_item[optional_key] = _text(item[optional_key], f"{item_path}.{optional_key}")
        item_results.append(output_item)

    sigma_days_total = _sqrt(days_variance)
    sigma_cost_total = _sqrt(cost_variance)
    effort_by_confidence = {
        label: expected_days + z_score * sigma_days_total
        for label, z_score in Z_SCORES.items()
    }
    labor_by_confidence = {
        label: expected_cost + z_score * sigma_cost_total
        for label, z_score in Z_SCORES.items()
    }
    selected_days = effort_by_confidence[confidence]
    selected_labor_cost = labor_by_confidence[confidence]

    one_time_rows, one_time_total = _cost_rows(root.get("one_time_costs"), "one_time_costs")
    recurring_rows, _ = _cost_rows(root.get("recurring_costs"), "recurring_costs", recurring=True)

    commercial = _mapping(_required(root, "commercial", "input"), "commercial")
    overhead_rate = _fraction(
        _required(commercial, "overhead_rate", "commercial"), "commercial.overhead_rate"
    )
    contingency_rate = _fraction(
        _required(commercial, "contingency_rate", "commercial"),
        "commercial.contingency_rate",
    )
    target_margin = _fraction(
        _required(commercial, "target_margin", "commercial"), "commercial.target_margin"
    )
    discount_rate = _fraction(
        _required(commercial, "discount_rate", "commercial"), "commercial.discount_rate"
    )
    tax_rate = _fraction(_required(commercial, "tax_rate", "commercial"), "commercial.tax_rate")
    round_to = _non_negative(
        _required(commercial, "round_to", "commercial"), "commercial.round_to"
    )
    rounding = str(commercial.get("rounding", "up")).strip().lower()
    if rounding not in {"up", "nearest"}:
        raise QuoteInputError("commercial.rounding must be up or nearest")

    overhead = selected_labor_cost * overhead_rate
    cost_base = selected_labor_cost + overhead + one_time_total
    contingency = cost_base * contingency_rate
    delivery_cost = cost_base + contingency
    pre_tax_before_discount = delivery_cost / (ONE - target_margin)
    discount_amount = pre_tax_before_discount * discount_rate
    pre_tax_unrounded = pre_tax_before_discount - discount_amount
    pre_tax_rounded = _round_quote(pre_tax_unrounded, round_to, rounding)
    tax = _quantize(pre_tax_rounded * tax_rate, MONEY)
    total_with_tax = _quantize(pre_tax_rounded + tax, MONEY)
    actual_margin = (
        (pre_tax_rounded - delivery_cost) / pre_tax_rounded
        if pre_tax_rounded > ZERO
        else ZERO
    )

    warnings = [
        "P80/P95 use a normal approximation and assume independent WBS-row uncertainty; correlated project risks can make the range too narrow.",
        "The calculator prices an already scoped WBS. It does not validate scope completeness, acceptance criteria, schedule dependencies, or market rates.",
        "Do not use contingency for a risk already included in pessimistic task estimates.",
        "Recurring costs are listed separately and are not included in the one-time project total.",
    ]
    if discount_rate > ZERO and actual_margin < target_margin:
        warnings.append("The discount reduces actual margin below the target margin.")
    if actual_margin < ZERO:
        warnings.append("The rounded discounted pre-tax quote is below delivery cost.")

    return {
        "project": {
            "name": project_name,
            "currency": currency,
            "pricing_confidence": confidence,
        },
        "method": {
            "effort": "PERT three-point per WBS row",
            "aggregation": "independent-row variance with normal-approximation upper percentiles",
            "pricing": "internal delivery cost plus overhead, contingency, and target gross margin",
        },
        "items": item_results,
        "effort": {
            "expected_days": _json_number(expected_days, DAY),
            "sigma_days": _json_number(sigma_days_total, DAY),
            "p80_days": _json_number(effort_by_confidence["p80"], DAY),
            "p95_days": _json_number(effort_by_confidence["p95"], DAY),
            "selected_days": _json_number(selected_days, DAY),
        },
        "labor": {
            "expected_cost": _json_number(expected_cost),
            "sigma_cost": _json_number(sigma_cost_total),
            "p80_cost": _json_number(labor_by_confidence["p80"]),
            "p95_cost": _json_number(labor_by_confidence["p95"]),
            "selected_cost": _json_number(selected_labor_cost),
        },
        "one_time_costs": one_time_rows,
        "recurring_costs": recurring_rows,
        "commercial": {
            "overhead_rate": _json_number(overhead_rate, RATE),
            "contingency_rate": _json_number(contingency_rate, RATE),
            "target_margin": _json_number(target_margin, RATE),
            "discount_rate": _json_number(discount_rate, RATE),
            "tax_rate": _json_number(tax_rate, RATE),
            "round_to": _json_number(round_to),
            "rounding": rounding,
        },
        "quote": {
            "selected_labor_cost": _json_number(selected_labor_cost),
            "overhead": _json_number(overhead),
            "one_time_external_cost": _json_number(one_time_total),
            "cost_base": _json_number(cost_base),
            "contingency": _json_number(contingency),
            "delivery_cost": _json_number(delivery_cost),
            "pre_tax_before_discount": _json_number(pre_tax_before_discount),
            "discount_amount": _json_number(discount_amount),
            "pre_tax_unrounded": _json_number(pre_tax_unrounded),
            "rounding_adjustment": _json_number(pre_tax_rounded - pre_tax_unrounded),
            "pre_tax_after_discount": _json_number(pre_tax_rounded),
            "tax": _json_number(tax),
            "total_with_tax": _json_number(total_with_tax),
            "actual_margin_after_discount": _json_number(actual_margin, RATE),
        },
        "warnings": warnings,
    }


def _escape_cell(value: Any) -> str:
    return str(value).replace("|", "\\|").replace("\r", " ").replace("\n", " ")


def _money(value: Any, currency: str) -> str:
    return f"{currency} {Decimal(str(value)):,.2f}"


def _percent(value: Any) -> str:
    return f"{Decimal(str(value)) * Decimal('100'):.2f}%"


def render_markdown(result: Mapping[str, Any]) -> str:
    project = _mapping(result["project"], "result.project")
    currency = str(project["currency"])
    effort = _mapping(result["effort"], "result.effort")
    labor = _mapping(result["labor"], "result.labor")
    quote = _mapping(result["quote"], "result.quote")
    commercial = _mapping(result["commercial"], "result.commercial")

    lines = [
        f"# {_escape_cell(project['name'])} 报价计算复核",
        "",
        f"- 定价置信情景：`{project['pricing_confidence']}`",
        f"- 币种：`{currency}`",
        "- 口径：内部交付成本价卡；周期性费用不计入一次性总价",
        "",
        "## WBS 三点估算",
        "",
        "| ID | 工作流 | 交付物 | 角色 | O | M | P | E | 日成本 | E 成本 |",
        "|---|---|---|---|---:|---:|---:|---:|---:|---:|",
    ]
    for item in _sequence(result["items"], "result.items"):
        row = _mapping(item, "result item")
        lines.append(
            "| {id} | {workstream} | {deliverable} | {role} | {o} | {m} | {p} | {e} | {rate} | {cost} |".format(
                id=_escape_cell(row["id"]),
                workstream=_escape_cell(row["workstream"]),
                deliverable=_escape_cell(row["deliverable"]),
                role=_escape_cell(row["role"]),
                o=row["optimistic_days"],
                m=row["most_likely_days"],
                p=row["pessimistic_days"],
                e=row["expected_days"],
                rate=_money(row["day_cost"], currency),
                cost=_money(row["expected_cost"], currency),
            )
        )

    lines.extend(
        [
            "",
            "## 工作量与直接成本情景",
            "",
            "| 指标 | Expected | P80 | P95 | 选定 |",
            "|---|---:|---:|---:|---:|",
            f"| 人日 | {effort['expected_days']} | {effort['p80_days']} | {effort['p95_days']} | {effort['selected_days']} |",
            f"| 角色直接成本 | {_money(labor['expected_cost'], currency)} | {_money(labor['p80_cost'], currency)} | {_money(labor['p95_cost'], currency)} | {_money(labor['selected_cost'], currency)} |",
            "",
            "## 商务计算",
            "",
            "| 步骤 | 金额/比例 |",
            "|---|---:|",
            f"| 选定角色直接成本 | {_money(quote['selected_labor_cost'], currency)} |",
            f"| 间接成本（{_percent(commercial['overhead_rate'])}） | {_money(quote['overhead'], currency)} |",
            f"| 一次性外部成本 | {_money(quote['one_time_external_cost'], currency)} |",
            f"| 成本基数 | {_money(quote['cost_base'], currency)} |",
            f"| 风险准备金（{_percent(commercial['contingency_rate'])}） | {_money(quote['contingency'], currency)} |",
            f"| 交付总成本 | {_money(quote['delivery_cost'], currency)} |",
            f"| 目标毛利率 | {_percent(commercial['target_margin'])} |",
            f"| 折扣前未税价 | {_money(quote['pre_tax_before_discount'], currency)} |",
            f"| 折扣（{_percent(commercial['discount_rate'])}） | -{_money(quote['discount_amount'], currency)} |",
            f"| 取整后未税报价 | **{_money(quote['pre_tax_after_discount'], currency)}** |",
            f"| 税（{_percent(commercial['tax_rate'])}） | {_money(quote['tax'], currency)} |",
            f"| 含税总价 | **{_money(quote['total_with_tax'], currency)}** |",
            f"| 折扣后实际毛利率 | {_percent(quote['actual_margin_after_discount'])} |",
        ]
    )

    one_time_rows = _sequence(result["one_time_costs"], "result.one_time_costs")
    if one_time_rows:
        lines.extend(["", "### 一次性外部成本", "", "| 名称 | 金额 |", "|---|---:|"])
        for raw in one_time_rows:
            row = _mapping(raw, "one-time row")
            lines.append(f"| {_escape_cell(row['name'])} | {_money(row['amount'], currency)} |")

    recurring_rows = _sequence(result["recurring_costs"], "result.recurring_costs")
    if recurring_rows:
        lines.extend(["", "### 周期性/按量费用（未计入一次性总价）", "", "| 名称 | 金额 | 周期 |", "|---|---:|---|"])
        for raw in recurring_rows:
            row = _mapping(raw, "recurring row")
            lines.append(
                f"| {_escape_cell(row['name'])} | {_money(row['amount'], currency)} | {_escape_cell(row['period'])} |"
            )

    lines.extend(["", "## 计算限制", ""])
    for warning in _sequence(result["warnings"], "result.warnings"):
        lines.append(f"- {_escape_cell(warning)}")
    return "\n".join(lines) + "\n"


def example_payload() -> dict[str, Any]:
    return {
        "_notice": "FICTITIOUS FORMAT EXAMPLE ONLY. Replace every rate, effort, tax, and commercial assumption.",
        "project": {
            "name": "示例：小程序 + H5 + 管理后台",
            "currency": "CNY",
            "pricing_confidence": "p80",
        },
        "rate_card": {
            "product": 1000,
            "frontend": 1200,
            "backend": 1400,
            "qa": 900,
            "devops": 1500,
            "algorithm": 1800,
        },
        "items": [
            {
                "id": "MP-01",
                "workstream": "小程序",
                "deliverable": "平台登录、账号绑定与异常流",
                "role": "frontend",
                "optimistic": 2,
                "most_likely": 3,
                "pessimistic": 5,
                "status": "示例假设",
            },
            {
                "id": "BE-01",
                "workstream": "公共后端",
                "deliverable": "账号服务、鉴权、审计与 API 契约",
                "role": "backend",
                "optimistic": 3,
                "most_likely": 5,
                "pessimistic": 9,
                "status": "示例假设",
            },
            {
                "id": "QA-01",
                "workstream": "质量",
                "deliverable": "跨端回归、接口测试与发布验收",
                "role": "qa",
                "optimistic": 2,
                "most_likely": 4,
                "pessimistic": 7,
                "status": "示例假设",
            },
        ],
        "one_time_costs": [
            {"name": "示例压测环境", "amount": 2000, "category": "cloud"}
        ],
        "recurring_costs": [
            {
                "name": "示例云资源",
                "amount": 1000,
                "period": "month",
                "pricing_note": "按实际规格和用量重新核价",
            }
        ],
        "commercial": {
            "overhead_rate": 0.1,
            "contingency_rate": 0.08,
            "target_margin": 0.25,
            "discount_rate": 0,
            "tax_rate": 0.06,
            "round_to": 100,
            "rounding": "up",
        },
    }


def _load_payload(path: str) -> Mapping[str, Any]:
    try:
        if path == "-":
            raw = sys.stdin.read()
        else:
            raw = Path(path).read_text(encoding="utf-8-sig")
    except UnicodeError as error:
        raise QuoteInputError(f"input must be UTF-8 JSON: {error}") from error
    try:
        parsed = json.loads(raw, parse_float=Decimal, parse_int=Decimal)
    except json.JSONDecodeError as error:
        raise QuoteInputError(f"invalid JSON: {error}") from error
    return _mapping(parsed, "input")


def main(argv: Sequence[str] | None = None) -> int:
    parser = argparse.ArgumentParser(
        description="Validate a technical-project WBS and calculate a cost-plus-margin quotation."
    )
    parser.add_argument("input", nargs="?", help="UTF-8 JSON input file, or - for stdin")
    parser.add_argument("--format", choices=("json", "markdown"), default="json")
    parser.add_argument("--example", action="store_true", help="print a fictitious input example")
    args = parser.parse_args(argv)

    if args.example:
        print(json.dumps(example_payload(), ensure_ascii=False, indent=2))
        return 0
    if not args.input:
        parser.error("input is required unless --example is used")

    try:
        result = calculate_quote(_load_payload(args.input))
    except (OSError, QuoteInputError) as error:
        print(f"quotation calculator error: {error}", file=sys.stderr)
        return 2

    if args.format == "markdown":
        sys.stdout.write(render_markdown(result))
    else:
        print(json.dumps(result, ensure_ascii=False, indent=2))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
