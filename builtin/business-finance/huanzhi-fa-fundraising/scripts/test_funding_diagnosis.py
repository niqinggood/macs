import unittest

from funding_diagnosis import DIMENSIONS, diagnose


class FundingDiagnosisTests(unittest.TestCase):
    def test_complete_input_is_deterministic(self):
        payload = {name: {"score": 4, "evidence": name} for name in DIMENSIONS}
        first = diagnose(payload)
        second = diagnose(payload)
        self.assertEqual(first, second)
        self.assertEqual(first["status"], "ok")
        self.assertEqual(first["score"], 80.0)
        self.assertEqual(first["grade"], "ready")

    def test_missing_dimension_is_not_inferred(self):
        payload = {name: 3 for name in DIMENSIONS if name != "timing"}
        result = diagnose(payload)
        self.assertEqual(result["status"], "insufficient_data")
        self.assertIsNone(result["score"])
        self.assertEqual(result["missing_dimensions"], ["timing"])

    def test_out_of_range_score_is_rejected(self):
        payload = {name: 3 for name in DIMENSIONS}
        payload["traction"] = 6
        with self.assertRaises(ValueError):
            diagnose(payload)


if __name__ == "__main__":
    unittest.main()
