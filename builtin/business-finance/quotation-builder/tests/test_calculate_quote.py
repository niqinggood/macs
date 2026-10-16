from __future__ import annotations

import importlib.util
import io
import json
import unittest
from contextlib import redirect_stdout
from pathlib import Path


SCRIPT = Path(__file__).resolve().parents[1] / "scripts" / "calculate_quote.py"
SPEC = importlib.util.spec_from_file_location("quotation_calculator", SCRIPT)
if SPEC is None or SPEC.loader is None:  # pragma: no cover - import guard
    raise RuntimeError(f"cannot load {SCRIPT}")
CALCULATOR = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(CALCULATOR)


def base_payload() -> dict:
    return {
        "project": {
            "name": "Calculator test",
            "currency": "CNY",
            "pricing_confidence": "expected",
        },
        "rate_card": {"backend": 1000},
        "items": [
            {
                "id": "BE-01",
                "workstream": "Backend",
                "deliverable": "Auditable API",
                "role": "backend",
                "optimistic": 1,
                "most_likely": 2,
                "pessimistic": 4,
            }
        ],
        "one_time_costs": [{"name": "Test environment", "amount": 100}],
        "recurring_costs": [{"name": "Hosting", "amount": 500, "period": "month"}],
        "commercial": {
            "overhead_rate": 0.1,
            "contingency_rate": 0.1,
            "target_margin": 0.25,
            "discount_rate": 0,
            "tax_rate": 0.06,
            "round_to": 0,
            "rounding": "up",
        },
    }


class QuoteCalculatorTests(unittest.TestCase):
    def test_pert_and_cost_plus_margin_are_reproducible(self) -> None:
        result = CALCULATOR.calculate_quote(base_payload())

        self.assertAlmostEqual(result["effort"]["expected_days"], 13 / 6, places=4)
        self.assertAlmostEqual(result["effort"]["sigma_days"], 0.5, places=4)
        self.assertGreater(result["effort"]["p80_days"], result["effort"]["expected_days"])
        self.assertGreater(result["labor"]["p95_cost"], result["labor"]["p80_cost"])
        self.assertEqual(result["quote"]["one_time_external_cost"], 100)
        self.assertAlmostEqual(
            result["quote"]["total_with_tax"],
            result["quote"]["pre_tax_after_discount"] + result["quote"]["tax"],
            places=2,
        )
        self.assertAlmostEqual(result["quote"]["actual_margin_after_discount"], 0.25, places=5)

    def test_recurring_costs_are_not_in_one_time_total(self) -> None:
        with_recurring = CALCULATOR.calculate_quote(base_payload())
        without_payload = base_payload()
        without_payload["recurring_costs"] = []
        without_recurring = CALCULATOR.calculate_quote(without_payload)

        self.assertEqual(
            with_recurring["quote"]["total_with_tax"],
            without_recurring["quote"]["total_with_tax"],
        )
        self.assertEqual(with_recurring["recurring_costs"][0]["amount"], 500)

    def test_p80_is_used_for_pricing_when_selected(self) -> None:
        payload = base_payload()
        payload["project"]["pricing_confidence"] = "p80"
        result = CALCULATOR.calculate_quote(payload)

        self.assertEqual(result["labor"]["selected_cost"], result["labor"]["p80_cost"])
        self.assertGreater(result["quote"]["selected_labor_cost"], result["labor"]["expected_cost"])

    def test_discount_reports_reduced_actual_margin(self) -> None:
        payload = base_payload()
        payload["commercial"]["discount_rate"] = 0.1
        result = CALCULATOR.calculate_quote(payload)

        self.assertLess(result["quote"]["actual_margin_after_discount"], 0.25)
        self.assertTrue(any("discount" in warning.lower() for warning in result["warnings"]))

    def test_invalid_three_point_order_is_rejected(self) -> None:
        payload = base_payload()
        payload["items"][0]["optimistic"] = 3
        payload["items"][0]["most_likely"] = 2
        with self.assertRaisesRegex(CALCULATOR.QuoteInputError, "optimistic <="):
            CALCULATOR.calculate_quote(payload)

    def test_unknown_role_is_rejected(self) -> None:
        payload = base_payload()
        payload["items"][0]["role"] = "algorithm"
        with self.assertRaisesRegex(CALCULATOR.QuoteInputError, "missing from rate_card"):
            CALCULATOR.calculate_quote(payload)

    def test_duplicate_wbs_id_is_rejected(self) -> None:
        payload = base_payload()
        payload["items"].append(dict(payload["items"][0]))
        with self.assertRaisesRegex(CALCULATOR.QuoteInputError, "duplicate WBS item id"):
            CALCULATOR.calculate_quote(payload)

    def test_markdown_contains_auditable_sections(self) -> None:
        markdown = CALCULATOR.render_markdown(CALCULATOR.calculate_quote(base_payload()))

        self.assertIn("WBS 三点估算", markdown)
        self.assertIn("商务计算", markdown)
        self.assertIn("周期性/按量费用", markdown)
        self.assertIn("BE-01", markdown)

    def test_example_is_valid_calculator_input(self) -> None:
        example = CALCULATOR.example_payload()
        result = CALCULATOR.calculate_quote(example)
        self.assertGreater(result["quote"]["total_with_tax"], 0)

        output = io.StringIO()
        with redirect_stdout(output):
            exit_code = CALCULATOR.main(["--example"])
        self.assertEqual(exit_code, 0)
        self.assertIn("_notice", json.loads(output.getvalue()))


if __name__ == "__main__":
    unittest.main()
