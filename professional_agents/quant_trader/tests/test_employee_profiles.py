import json
import os
from pathlib import Path
import sys
import tempfile
import unittest
from unittest import mock


ROOT = Path(__file__).resolve().parents[1]
if str(ROOT) not in sys.path:
    sys.path.insert(0, str(ROOT))

from professional_agent import (
    AI_HEDGE_FUND_MAINLAND_DEFAULT_ANALYSTS,
    PROTOCOL,
    QuantProfessionalAgent,
)


class FakeAdapter:
    def __init__(self, name, result=None):
        self.name = name
        self.requests = []
        self.result = result or {
            "backend": name,
            "status": "success",
            "output": name + " evidence",
            "data": {},
            "warnings": [],
            "artifacts": [],
        }

    def configuration(self):
        return {"backend": self.name, "state": "configured", "transport": "test"}

    def invoke(self, request):
        self.requests.append(request)
        return dict(self.result)


def make_request(employee_id, input_data=None, capability=None):
    if capability is None:
        capability = {
            "quant-trader": "trading",
            "investment-research-manager": "research-management",
            "financial-intelligence-analyst": "financial-intelligence",
        }[employee_id]
    if input_data is None:
        input_data = {"mode": "research"}
        if employee_id == "financial-intelligence-analyst":
            input_data["text"] = (
                "公司公告显示本年度主营业务收入和归母净利润同比增长，同时提示原材料"
                "价格波动、海外需求变化及汇率风险可能影响后续经营表现。"
            )
    return {
        "protocol": PROTOCOL,
        "request_id": "profile-" + employee_id,
        "agent_id": "quant-trader",
        "employee_id": employee_id,
        "tenant_id": "default",
        "session_id": "s1",
        "execution_id": "e1",
        "step_index": 0,
        "capability": capability,
        "input": json.dumps(input_data, ensure_ascii=False),
    }


class EmployeeProfileContractTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.run_root = Path(self.temp.name) / "runs"
        self.run_root.mkdir()
        self.environment = {
            "OPENKITTY_AGENT_RUN_DIR": str(self.run_root),
            "PATH": os.environ.get("PATH", ""),
            "QUANT_TRADER_CHINA_MARKET_DATA": "disabled",
        }

    def tearDown(self):
        self.temp.cleanup()

    def _agent(self, result_by_backend=None):
        result_by_backend = result_by_backend or {}
        if "ai-hedge-fund" not in result_by_backend:
            result_by_backend = dict(result_by_backend)
            result_by_backend["ai-hedge-fund"] = {
                "backend": "ai-hedge-fund",
                "status": "success",
                "output": "evidence-backed hold",
                "data": {
                    "decisions": {
                        "AAPL": {
                            "action": "hold",
                            "quantity": 0,
                            "confidence": 0.75,
                            "reasoning": "multi-discipline evidence remains balanced",
                        },
                        "600596.SS": {
                            "action": "hold",
                            "quantity": 0,
                            "confidence": 0.75,
                            "reasoning": "multi-discipline evidence remains balanced",
                        },
                    },
                    "analyst_signals": {
                        "technical_analyst_agent": {
                            "AAPL": {"signal": "neutral"},
                            "600596.SS": {"signal": "neutral"},
                        },
                        "fundamentals_analyst_agent": {
                            "AAPL": {"signal": "neutral"},
                            "600596.SS": {"signal": "neutral"},
                        },
                        "valuation_analyst_agent": {
                            "AAPL": {"signal": "neutral"},
                            "600596.SS": {"signal": "neutral"},
                        },
                        "news_sentiment_analyst_agent": {
                            "AAPL": {"signal": "neutral"},
                            "600596.SS": {"signal": "neutral"},
                        },
                    },
                },
                "warnings": [],
                "artifacts": [],
            }
        adapters = {
            backend: FakeAdapter(backend, result_by_backend.get(backend))
            for backend in ("tradingagents", "ai-hedge-fund", "fingpt")
        }
        return QuantProfessionalAgent(self.environment, adapters), adapters

    def test_each_employee_invokes_exactly_one_assigned_backend(self):
        assignments = {
            "quant-trader": ("AI交易员", "tradingagents", "trading"),
            "investment-research-manager": (
                "AI投研经理",
                "ai-hedge-fund",
                "research-management",
            ),
            "financial-intelligence-analyst": (
                "AI金信分析师",
                "fingpt",
                "financial-intelligence",
            ),
        }
        for employee_id, (name, assigned, capability) in assignments.items():
            with self.subTest(employee_id=employee_id):
                agent, adapters = self._agent()
                response = agent.handle(make_request(employee_id))

                self.assertEqual(response["status"], "completed")
                self.assertEqual(
                    [item["backend"] for item in response["data"]["backend_results"]],
                    [assigned],
                )
                self.assertEqual(response["data"]["employee_profile"]["name"], name)
                self.assertEqual(
                    response["data"]["employee_profile"]["capability"], capability
                )
                self.assertEqual(len(adapters[assigned].requests), 1)
                self.assertTrue(
                    all(
                        not adapter.requests
                        for backend, adapter in adapters.items()
                        if backend != assigned
                    )
                )

    def test_employee_cannot_select_another_role_backend(self):
        agent, adapters = self._agent()
        response = agent.handle(
            make_request(
                "quant-trader",
                {"mode": "research", "frameworks": ["fingpt"]},
            )
        )

        self.assertEqual(response["status"], "failed")
        self.assertIn("isolated", response["error"])
        self.assertTrue(all(not adapter.requests for adapter in adapters.values()))

    def test_employee_and_capability_must_match(self):
        agent, adapters = self._agent()
        request = make_request("investment-research-manager")
        request["capability"] = "financial-intelligence"
        response = agent.handle(request)

        self.assertEqual(response["status"], "failed")
        self.assertIn("requires capability 'research-management'", response["error"])
        self.assertTrue(all(not adapter.requests for adapter in adapters.values()))

    def test_legacy_quant_trader_analyze_capability_maps_to_tradingagents_only(self):
        agent, adapters = self._agent()
        response = agent.handle(make_request("quant-trader", capability="analyze"))

        self.assertEqual(response["status"], "completed")
        self.assertEqual(len(adapters["tradingagents"].requests), 1)
        self.assertEqual(adapters["ai-hedge-fund"].requests, [])
        self.assertEqual(adapters["fingpt"].requests, [])

    def test_mainland_investment_manager_defaults_to_multidiscipline_research(self):
        agent, adapters = self._agent()
        fixture = adapters["ai-hedge-fund"].result["data"]
        fixture["decisions"]["600596.SS"] = fixture["decisions"].pop("AAPL")
        for per_analyst in fixture["analyst_signals"].values():
            per_analyst["600596.SS"] = per_analyst.pop("AAPL")
        response = agent.handle(
            make_request(
                "investment-research-manager",
                {"mode": "research", "symbols": ["600596.SS"]},
            )
        )

        self.assertEqual(response["status"], "completed")
        sent = adapters["ai-hedge-fund"].requests[0]["input"]
        self.assertEqual(
            sent["ai_hedge_fund_analysts"],
            list(AI_HEDGE_FUND_MAINLAND_DEFAULT_ANALYSTS),
        )
        self.assertGreater(len(sent["ai_hedge_fund_analysts"]), 1)

    def test_mainland_investment_manager_rejects_explicit_technical_only_scope(self):
        agent, adapters = self._agent()
        response = agent.handle(
            make_request(
                "investment-research-manager",
                {
                    "mode": "research",
                    "symbols": ["600596.SS"],
                    "ai_hedge_fund_analysts": ["technical"],
                },
            )
        )

        self.assertEqual(response["status"], "failed")
        self.assertIn("technical analysis alone", response["error"])
        self.assertEqual(adapters["ai-hedge-fund"].requests, [])

    def test_investment_manager_fallback_decision_is_not_success(self):
        fallback = {
            "backend": "ai-hedge-fund",
            "status": "success",
            "output": "hold",
            "data": {
                "decisions": {
                    "AAPL": {
                        "action": "hold",
                        "quantity": 0,
                        "confidence": 0,
                        "reasoning": "Default decision: hold",
                    }
                }
            },
            "warnings": [],
            "artifacts": [],
        }
        agent, _ = self._agent({"ai-hedge-fund": fallback})
        response = agent.handle(make_request("investment-research-manager"))

        self.assertEqual(response["status"], "degraded")
        backend = response["data"]["backend_results"][0]
        self.assertFalse(backend["data"]["manager_quality_gate"]["passed"])
        self.assertTrue(any("not counted as success" in item for item in response["warnings"]))

    def test_investment_manager_high_confidence_no_trade_and_error_signal_are_degraded(self):
        fallback = {
            "backend": "ai-hedge-fund",
            "status": "success",
            "output": "hold",
            "data": {
                "decisions": {
                    "AAPL": {
                        "action": "hold",
                        "quantity": 0,
                        "confidence": 100,
                        "reasoning": "No valid trade available",
                    }
                },
                "analyst_signals": {
                    "technical_analyst_agent": {
                        "AAPL": {
                            "signal": "neutral",
                            "confidence": 100,
                            "reasoning": (
                                "Error in analysis: missing price data; "
                                "defaulting to neutral."
                            ),
                        }
                    }
                },
            },
            "warnings": [],
            "artifacts": [],
        }
        agent, _ = self._agent({"ai-hedge-fund": fallback})
        response = agent.handle(
            make_request(
                "investment-research-manager",
                {
                    "mode": "research",
                    "symbols": ["AAPL"],
                    "selected_analysts": ["technical_analyst"],
                },
            )
        )

        self.assertEqual(response["status"], "degraded")
        quality = response["data"]["backend_results"][0]["data"][
            "manager_quality_gate"
        ]
        self.assertFalse(quality["passed"])
        self.assertTrue(
            any("no valid trade" in issue.lower() for issue in quality["issues"])
        )
        self.assertTrue(
            any(
                "technical_analyst_agent/AAPL" in issue
                and "fallback/error" in issue
                for issue in quality["issues"]
            )
        )

    def test_investment_manager_scans_unselected_analyst_and_risk_fallbacks(self):
        fallback = {
            "backend": "ai-hedge-fund",
            "status": "success",
            "output": "evidence-backed hold",
            "data": {
                "decisions": {
                    "AAPL": {
                        "action": "hold",
                        "quantity": 0,
                        "confidence": 80,
                        "reasoning": "Evidence is balanced.",
                    }
                },
                "analyst_signals": {
                    "technical_analyst_agent": {
                        "AAPL": {"reasoning": "Parsing error in model response"}
                    }
                },
                "risk_management": {
                    "AAPL": {"reasoning": "Missing price data for risk calculation"}
                },
            },
            "warnings": [],
            "artifacts": [],
        }
        agent, _ = self._agent({"ai-hedge-fund": fallback})
        response = agent.handle(make_request("investment-research-manager"))

        self.assertEqual(response["status"], "degraded")
        quality = response["data"]["backend_results"][0]["data"][
            "manager_quality_gate"
        ]
        self.assertFalse(quality["passed"])
        self.assertTrue(any("parsing error" in issue for issue in quality["issues"]))
        self.assertTrue(any("missing price data" in issue for issue in quality["issues"]))

    def test_investment_manager_success_without_structured_decision_is_degraded(self):
        unproven = {
            "backend": "ai-hedge-fund",
            "status": "success",
            "output": "hold",
            "data": {},
            "warnings": [],
            "artifacts": [],
        }
        agent, _ = self._agent({"ai-hedge-fund": unproven})
        response = agent.handle(make_request("investment-research-manager"))

        self.assertEqual(response["status"], "degraded")
        issues = response["data"]["backend_results"][0]["data"][
            "manager_quality_gate"
        ]["issues"]
        self.assertTrue(any("no structured investment decision" in issue for issue in issues))

    def test_mainland_manager_requires_every_selected_analyst_signal(self):
        partial = {
            "backend": "ai-hedge-fund",
            "status": "success",
            "output": "multi-discipline decision",
            "data": {
                "decisions": {
                    "600596.SS": {
                        "action": "hold",
                        "quantity": 0,
                        "confidence": 0.7,
                        "reasoning": "evidence is balanced",
                    }
                },
                "analyst_signals": {
                    "technical_analyst_agent": {
                        "600596.SS": {"signal": "neutral"}
                    }
                },
            },
            "warnings": [],
            "artifacts": [],
        }
        agent, _ = self._agent({"ai-hedge-fund": partial})
        response = agent.handle(
            make_request(
                "investment-research-manager",
                {"mode": "research", "symbols": ["600596.SS"]},
            )
        )

        self.assertEqual(response["status"], "degraded")
        issues = response["data"]["backend_results"][0]["data"][
            "manager_quality_gate"
        ]["issues"]
        self.assertTrue(any("omitted selected-analyst signals" in issue for issue in issues))

    def test_financial_intelligence_response_declares_text_only_boundary(self):
        agent, adapters = self._agent()
        response = agent.handle(
            make_request(
                "financial-intelligence-analyst",
                {
                    "mode": "research",
                    "text": (
                        "公司发布年度业绩预增公告，称主营业务销量增长、产品结构改善，"
                        "但同时提示原材料涨价和海外需求波动可能影响下一报告期利润。"
                    ),
                    "price_history": [{"close": 1}],
                    "market_data_source": "caller-claim",
                    "price_source": "caller-claim",
                    "symbols": ["600596.SH"],
                },
            )
        )

        self.assertEqual(response["status"], "completed")
        sent = adapters["fingpt"].requests[0]["input"]
        for forbidden in (
            "price_history",
            "market_data_source",
            "price_source",
            "symbols",
        ):
            self.assertNotIn(forbidden, sent)
        self.assertEqual(
            response["data"]["market_data"]["status"],
            "not-applicable-for-financial-text",
        )
        self.assertEqual(
            response["data"]["security_resolution"]["status"],
            "not-applicable-for-financial-text",
        )
        boundary = response["data"]["backend_results"][0]["data"]["analysis_boundary"]
        self.assertIn("price forecasting", boundary["does_not_support"])
        self.assertIn("caller-supplied financial text", boundary["input"])

    def test_financial_text_never_enters_security_name_resolution(self):
        agent, adapters = self._agent()
        source = (
            "请分析以下虚构演示材料：2026年7月14日，新安股份有限公司称营业收入增长，"
            "但净利润下降，并提示原材料价格、项目交付和客户回款存在不确定性。"
        )
        with mock.patch(
            "professional_agent.resolve_input_securities",
            side_effect=AssertionError("financial text reached security resolution"),
        ):
            response = agent.handle(
                make_request(
                    "financial-intelligence-analyst",
                    {"mode": "research", "text": source},
                )
            )

        self.assertEqual(response["status"], "completed")
        self.assertEqual(len(adapters["fingpt"].requests), 1)
        self.assertEqual(
            response["data"]["security_resolution"]["status"],
            "not-applicable-for-financial-text",
        )

    def test_financial_intelligence_rejects_bare_stock_analysis_instruction(self):
        agent, adapters = self._agent()
        request = make_request("financial-intelligence-analyst")
        request["input"] = "@金信分析师 分析新安股份"
        response = agent.handle(request)

        self.assertEqual(response["status"], "failed")
        self.assertIn("Paste or upload", response["error"])
        self.assertFalse(response["data"]["text_quality_gate"]["passed"])
        self.assertTrue(all(not adapter.requests for adapter in adapters.values()))

    def test_financial_intelligence_short_backend_input_count_is_not_success(self):
        short_result = {
            "backend": "fingpt",
            "status": "success",
            "output": "neutral",
            "data": {"input_characters": 7},
            "warnings": [],
            "artifacts": [],
        }
        agent, _ = self._agent({"fingpt": short_result})
        response = agent.handle(make_request("financial-intelligence-analyst"))

        self.assertEqual(response["status"], "degraded")
        gate = response["data"]["backend_results"][0]["data"]["text_quality_gate"]
        self.assertFalse(gate["passed"])
        self.assertTrue(any("not counted as success" in item for item in response["warnings"]))

    def test_health_is_scoped_to_calling_employee_backend(self):
        agent, adapters = self._agent()
        response = agent.handle(
            make_request("financial-intelligence-analyst", capability="health")
        )

        self.assertEqual(set(response["data"]["backends"]), {"fingpt"})
        self.assertEqual(
            response["data"]["employee_profile"]["at_name"], "金信分析师"
        )
        self.assertTrue(all(not adapter.requests for adapter in adapters.values()))


if __name__ == "__main__":
    unittest.main()
