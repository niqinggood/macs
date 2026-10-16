import sys
from pathlib import Path
import unittest


SHIMS = Path(__file__).resolve().parents[1] / "backend_shims"
if str(SHIMS) not in sys.path:
    sys.path.insert(0, str(SHIMS))

from _common import RequestRejected
from ai_hedge_fund_backend import _analysts


class AIHedgeFundBackendTests(unittest.TestCase):
    def test_human_analyst_aliases_map_to_upstream_keys(self):
        self.assertEqual(
            _analysts(
                {
                    "ai_hedge_fund_analysts": [
                        "technical",
                        "Fundamentals",
                        "news-sentiment",
                        "technical_analyst",
                    ]
                }
            ),
            [
                "technical_analyst",
                "fundamentals_analyst",
                "news_sentiment_analyst",
            ],
        )

    def test_unknown_analyst_is_rejected_before_upstream_graph(self):
        with self.assertRaisesRegex(RequestRejected, "unknown AI Hedge Fund analyst"):
            _analysts({"selected_analysts": ["technical-ish"]})


if __name__ == "__main__":
    unittest.main()
