from __future__ import annotations

import json
import sys
import unittest
from datetime import datetime, timezone
from pathlib import Path


SKILL_ROOT = Path(__file__).resolve().parents[1]
SCRIPTS = SKILL_ROOT / "scripts"
sys.path.insert(0, str(SCRIPTS))

from elder_monitor_core import load_policy, validate_event  # noqa: E402
from simulate_agentos import (  # noqa: E402
    SCENARIOS,
    find_assessment,
    make_agentos_payload,
    make_event,
    run_local_scenario,
)
from test_elder_monitor_scenarios import format_scenario  # noqa: E402


class SimulatorTests(unittest.TestCase):
    def test_all_synthetic_scenarios_match_expected_severity(self) -> None:
        policy = load_policy()
        for spec in SCENARIOS:
            with self.subTest(scenario=spec["name"]):
                result = run_local_scenario(spec["name"], "unit", policy)
                self.assertTrue(result["passed"], result)

    def test_generated_events_are_valid_and_non_identifying(self) -> None:
        end = datetime(2026, 1, 2, 3, 4, 5, tzinfo=timezone.utc)
        for spec in SCENARIOS:
            with self.subTest(scenario=spec["name"]):
                event = make_event(spec["name"], "unit", end)
                self.assertEqual(validate_event(event), [])
                serialized = json.dumps(event, ensure_ascii=False)
                self.assertNotIn("phone", serialized.lower())
                self.assertNotIn("address", serialized.lower())

    def test_agentos_payload_binds_skill_and_forbids_external_actions(self) -> None:
        event = make_event("fall_no_response", "unit")
        role = {"role_id": "Assistant", "role_revision": "r1-test"}
        payload = make_agentos_payload(event, "fall_no_response", "unit", role, "sim-user")
        self.assertEqual(payload["skill_run"]["skill_name"], "elder-monitor")
        self.assertEqual(payload["skill_run"]["version"], "1.2.0")
        self.assertEqual(payload["role_revision"], "r1-test")
        self.assertIn("严禁发送通知", payload["message"])
        self.assertNotIn("token", json.dumps(payload).lower())

        alias_payload = make_agentos_payload(
            event,
            "fall_no_response",
            "unit",
            role,
            "sim-user",
            skill_name="elder",
            skill_version="1.2.0",
        )
        self.assertEqual(alias_payload["skill_run"]["skill_name"], "elder")

    def test_finds_assessment_in_common_agentos_result_shapes(self) -> None:
        assessment = {"severity": "high", "event_id": "sim-unit"}
        variants = [
            assessment,
            {"result": assessment},
            json.dumps(assessment),
            "```json\n" + json.dumps(assessment) + "\n```",
            "说明文字\n" + json.dumps(assessment, ensure_ascii=False) + "\n结束",
        ]
        for value in variants:
            with self.subTest(value=value):
                self.assertEqual(find_assessment(value), assessment)

    def test_invalid_result_is_not_mistaken_for_assessment(self) -> None:
        self.assertIsNone(find_assessment("no structured result"))
        self.assertIsNone(find_assessment({"status": "completed"}))

    def test_human_readable_scenario_includes_meaning_reason_and_exact_event(self) -> None:
        spec = SCENARIOS[0]
        event = make_event(spec["name"], "unit")
        rendered = format_scenario(1, len(SCENARIOS), spec, event)
        self.assertIn(spec["description"], rendered)
        self.assertIn(spec["expected_reason"], rendered)
        self.assertIn(spec["expected_severity"], rendered)
        self.assertIn(json.dumps(event, ensure_ascii=False, indent=2), rendered)


if __name__ == "__main__":
    unittest.main()
