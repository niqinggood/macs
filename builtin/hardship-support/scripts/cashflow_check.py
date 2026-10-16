#!/usr/bin/env python3
"""Check one-period cashflow arithmetic without deciding a repayment plan."""

from __future__ import annotations

import argparse
import json
from decimal import Decimal, InvalidOperation


def decimal_value(raw: str) -> Decimal:
    try:
        value = Decimal(raw.replace(",", "").replace("，", "").strip())
    except (InvalidOperation, AttributeError) as exc:
        raise argparse.ArgumentTypeError(f"invalid amount: {raw!r}") from exc
    if not value.is_finite():
        raise argparse.ArgumentTypeError(f"amount must be finite: {raw!r}")
    return value


def display(value: Decimal) -> str:
    normalized = value.normalize()
    if normalized == normalized.to_integral():
        return format(normalized, "f")
    return format(normalized, "f").rstrip("0").rstrip(".")


def main() -> int:
    parser = argparse.ArgumentParser(
        description="Check one-period income, necessary expenses, fixed payments, and proposed allocations."
    )
    parser.add_argument("--income", required=True, type=decimal_value)
    parser.add_argument("--necessary", required=True, type=decimal_value)
    parser.add_argument("--fixed", action="append", default=[], type=decimal_value)
    parser.add_argument("--planned", action="append", default=[], type=decimal_value)
    args = parser.parse_args()

    fixed_total = sum(args.fixed, Decimal("0"))
    planned_total = sum(args.planned, Decimal("0"))
    pre_debt = args.income - args.necessary
    after_fixed = pre_debt - fixed_total
    unallocated = after_fixed - planned_total

    warnings: list[str] = []
    if any(value < 0 for value in [args.income, args.necessary, *args.fixed, *args.planned]):
        warnings.append("negative input detected; confirm whether it represents a refund or reversal")
    if pre_debt < 0:
        warnings.append("necessary expenses exceed income")
    if after_fixed < 0:
        warnings.append("fixed payments exceed the post-expense amount")
    if unallocated < 0:
        warnings.append("proposed allocations exceed the amount remaining after fixed payments")

    result = {
        "input": {
            "income": display(args.income),
            "necessary": display(args.necessary),
            "fixed": [display(value) for value in args.fixed],
            "planned": [display(value) for value in args.planned],
        },
        "totals": {
            "fixed": display(fixed_total),
            "planned": display(planned_total),
        },
        "formulas": [
            f"{display(args.income)} - {display(args.necessary)} = {display(pre_debt)}",
            f"{display(pre_debt)} - {display(fixed_total)} = {display(after_fixed)}",
            f"{display(after_fixed)} - {display(planned_total)} = {display(unallocated)}",
        ],
        "post_expense": display(pre_debt),
        "post_fixed": display(after_fixed),
        "unallocated": display(unallocated),
        "within_known_cashflow": unallocated >= 0,
        "warnings": warnings,
        "scope": "arithmetic only; current account cash, debt priority, and creditor acceptance are not inferred",
    }
    print(json.dumps(result, ensure_ascii=False, indent=2))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
