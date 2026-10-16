from __future__ import annotations

import json
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path


SKILL_ROOT = Path(__file__).resolve().parents[1]
SCRIPTS = SKILL_ROOT / "scripts"
FIXTURES = Path(__file__).resolve().parent / "fixtures"
sys.path.insert(0, str(SCRIPTS))

from adapters import RecordingAlertAdapter  # noqa: E402
from elder_monitor_core import evaluate_event, load_policy, validate_event  # noqa: E402
from redact_cloud_payload import sanitize_event  # noqa: E402


def load_fixture(name: str) -> dict:
    with (FIXTURES / name).open("r", encoding="utf-8") as handle:
        return json.load(handle)


class ElderMonitorTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls) -> None:
        cls.policy = load_policy()

    def test_normal_ten_minute_sitting_does_not_alert(self) -> None:
        result = evaluate_event(load_fixture("normal-sitting.json"), self.policy)
        self.assertEqual(result["severity"], "none")
        self.assertFalse(result["alert_required"])

    def test_distress_plus_low_motion_is_high(self) -> None:
        result = evaluate_event(load_fixture("distress-low-motion.json"), self.policy)
        self.assertEqual(result["severity"], "high")
        self.assertIn("distress_cluster_with_low_motion", result["triggered_rules"])
        self.assertEqual(result["recommended_actions"][0]["action"], "voice_check")

    def test_fall_plus_no_response_is_critical_without_model_wait(self) -> None:
        result = evaluate_event(load_fixture("fall-no-response.json"), self.policy)
        self.assertEqual(result["severity"], "critical")
        self.assertFalse(result["model_review"]["required"])
        self.assertIn("fall_or_floor_plus_no_response", result["triggered_rules"])

    def test_assessment_id_is_stable(self) -> None:
        event = load_fixture("distress-low-motion.json")
        first = evaluate_event(event, self.policy)
        second = evaluate_event(event, self.policy)
        self.assertEqual(first["assessment_id"], second["assessment_id"])

    def test_invalid_event_reports_errors(self) -> None:
        errors = validate_event({"event_id": "x", "person_id": "p"})
        self.assertTrue(errors)

    def test_unknown_sensor_type_requires_contract_extension(self) -> None:
        event = load_fixture("normal-sitting.json")
        event["observations"][0]["type"] = "vital_alert"
        errors = validate_event(event)
        self.assertTrue(any("must be one of" in error for error in errors))

    def test_voice_service_error_cannot_be_encoded_as_no_response(self) -> None:
        event = load_fixture("normal-sitting.json")
        event["context"].update(
            {
                "responded_to_voice_check": False,
                "voice_check_service_status": "error",
            }
        )
        errors = validate_event(event)
        self.assertTrue(any("must be null" in error for error in errors))

    def test_explicit_help_request_is_critical(self) -> None:
        event = load_fixture("normal-sitting.json")
        event["observations"] = [
            {
                "type": "help_request",
                "value": True,
                "confidence": 0.96,
                "source": "speech_intent",
            }
        ]
        result = evaluate_event(event, self.policy)
        self.assertEqual(result["severity"], "critical")
        self.assertIn("explicit_help_request", result["triggered_rules"])

    def test_redaction_does_not_turn_false_string_into_help(self) -> None:
        event = load_fixture("normal-sitting.json")
        event["observations"] = [
            {
                "type": "help_request",
                "value": "false",
                "confidence": 0.9,
                "source": "speech_intent",
            }
        ]
        payload = sanitize_event(event)
        self.assertIs(payload["observations"][0]["value"], False)

    def test_cloud_payload_removes_identity_and_precise_location(self) -> None:
        event = load_fixture("distress-low-motion.json")
        payload = sanitize_event(event)
        serialized = json.dumps(payload, ensure_ascii=False)
        for forbidden in ("person_id", "elder_demo_001", "living_room", "known_risks", "previous_fall"):
            self.assertNotIn(forbidden, serialized)
        self.assertIn("event_ref", payload)

    def test_alert_mock_is_idempotent(self) -> None:
        assessment = evaluate_event(load_fixture("distress-low-motion.json"), self.policy)
        adapter = RecordingAlertAdapter()
        first = adapter.create_alert(assessment, assessment["assessment_id"])
        second = adapter.create_alert(assessment, assessment["assessment_id"])
        self.assertEqual(first, second)
        self.assertEqual(len(adapter.alerts), 1)

    def test_batch_isolates_bad_lines(self) -> None:
        good = json.dumps(load_fixture("normal-sitting.json"), ensure_ascii=False)
        with tempfile.TemporaryDirectory() as temp_dir:
            batch_path = Path(temp_dir) / "events.jsonl"
            batch_path.write_text(good + "\n{bad json}\n", encoding="utf-8")
            completed = subprocess.run(
                [sys.executable, "-B", str(SCRIPTS / "review_batch.py"), str(batch_path)],
                check=False,
                capture_output=True,
                text=True,
                encoding="utf-8",
            )
        result = json.loads(completed.stdout)
        self.assertEqual(completed.returncode, 1)
        self.assertEqual(result["processed"], 1)
        self.assertEqual(result["failures"], 1)

    def test_schema_documents_are_valid_json(self) -> None:
        for name in ("event.schema.json", "assessment.schema.json"):
            with (SKILL_ROOT / "schemas" / name).open("r", encoding="utf-8") as handle:
                self.assertIsInstance(json.load(handle), dict)


if __name__ == "__main__":
    unittest.main()

