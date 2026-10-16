from contextlib import redirect_stdout
import asyncio
import io
import json
import os
from pathlib import Path
import sys
import tempfile
import types
import unittest
from unittest import mock


SHIM_ROOT = Path(__file__).resolve().parents[1]
if str(SHIM_ROOT) not in sys.path:
    sys.path.insert(0, str(SHIM_ROOT))

import _common
import ai_hedge_fund_backend
import fingpt_backend
import fingpt_minimax_callable
import tradingagents_backend


def backend_request(backend, run_dir, mode="research", input_data=None, **overrides):
    request = {
        "protocol": _common.BACKEND_PROTOCOL,
        "backend": backend,
        "request_id": "test-request",
        "capability": "analyze",
        "mode": mode,
        "input": dict(input_data or {"symbols": ["AAPL"], "mode": mode}),
        "run_dir": str(run_dir),
        "safety": {
            "research_only": mode == "research",
            "paper_trading_only": mode == "paper",
            "live_trading": False,
            "order_submission": False,
        },
    }
    request.update(overrides)
    return request


def package_module(name):
    module = types.ModuleType(name)
    module.__path__ = []
    return module


class BackendShimTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.run_dir = Path(self.temp.name).resolve()

    def tearDown(self):
        self.temp.cleanup()

    def test_protocol_and_live_order_flags_are_rejected_before_invocation(self):
        calls = []

        def implementation(request):
            calls.append(request)
            return _common.make_response("success", output="should not run")

        bad_protocol = backend_request("fingpt", self.run_dir)
        bad_protocol["protocol"] = "wrong"
        bad_safety = backend_request("fingpt", self.run_dir)
        bad_safety["safety"]["order_submission"] = True
        nested_live = backend_request(
            "fingpt",
            self.run_dir,
            input_data={"symbols": ["AAPL"], "mode": "research", "execute_orders": True},
        )

        for request in (bad_protocol, bad_safety, nested_live):
            response = _common.dispatch("fingpt", request, implementation)
            self.assertEqual(response["status"], "error")
            self.assertEqual(response["artifacts"], [])
        self.assertEqual(calls, [])

    def test_external_market_data_provenance_uses_fixed_provider_allowlist(self):
        calls = []

        def implementation(request):
            calls.append(request.market_data_provenance)
            return _common.make_response("success", output="validated")

        valid = backend_request(
            "fingpt",
            self.run_dir,
            market_data_provenance={
                "kind": "external",
                "provider": "tencent-newfqkline",
                "instruments": [{"symbol": "600596.SS"}],
            },
        )
        invalid = backend_request(
            "fingpt",
            self.run_dir,
            market_data_provenance={
                "kind": "external",
                "provider": "caller-claimed-external",
                "instruments": [{"symbol": "600596.SS"}],
            },
        )
        self.assertEqual(_common.dispatch("fingpt", valid, implementation)["status"], "success")
        self.assertEqual(_common.dispatch("fingpt", invalid, implementation)["status"], "error")
        self.assertEqual(len(calls), 1)
        self.assertEqual(calls[0]["provider"], "tencent-newfqkline")

    def test_tradingagents_calls_propagate_and_suppresses_upstream_stdout(self):
        calls = []

        default_config = types.ModuleType("tradingagents.default_config")
        default_config.DEFAULT_CONFIG = {"provider": "test"}
        graph_module = types.ModuleType("tradingagents.graph.trading_graph")

        class FakeGraph:
            def __init__(self, selected_analysts, debug, config):
                self.selected_analysts = selected_analysts
                self.debug = debug
                self.config = config

            def propagate(self, symbol, as_of):
                print("noisy upstream progress")
                calls.append((symbol, as_of))
                return {
                    "market_report": "verified market evidence for %s" % symbol,
                    "trader_investment_plan": "wait for confirmation and define invalidation",
                    "final_trade_decision": "paper decision only",
                }, {"action": "hold", "symbol": symbol}

        graph_module.TradingAgentsGraph = FakeGraph
        modules = {
            "tradingagents": package_module("tradingagents"),
            "tradingagents.default_config": default_config,
            "tradingagents.graph": package_module("tradingagents.graph"),
            "tradingagents.graph.trading_graph": graph_module,
        }
        request = backend_request(
            "tradingagents",
            self.run_dir,
            input_data={
                "symbols": ["AAPL", "MSFT"],
                "as_of": "2026-07-14",
                "mode": "research",
            },
        )
        with mock.patch.dict(sys.modules, modules):
            response = tradingagents_backend.handle_request(request)

        self.assertEqual(response["status"], "success")
        self.assertEqual(calls, [("AAPL", "2026-07-14"), ("MSFT", "2026-07-14")])
        self.assertEqual(response["data"]["decisions"]["AAPL"]["action"], "hold")
        self.assertIn("# AI交易员研判", response["output"])
        self.assertIn("交易执行计划", response["output"])
        self.assertIn("market_report", response["data"]["reports"]["AAPL"])
        self.assertTrue(any("stdout was suppressed" in item for item in response["warnings"]))
        self.assertFalse(response["data"]["safety"]["live_trading"])

    def test_ai_hedge_fund_calls_run_function_with_blank_paper_portfolio(self):
        captured = {}
        main_module = types.ModuleType("src.main")

        def run_hedge_fund(**kwargs):
            print("progress")
            captured.update(kwargs)
            return {
                "decisions": {"AAPL": {"action": "hold", "quantity": 0}},
                "analyst_signals": {
                    "technical_analyst_agent": {"AAPL": {"risk": "medium"}}
                },
            }

        main_module.run_hedge_fund = run_hedge_fund
        modules = {
            "src": package_module("src"),
            "src.main": main_module,
        }
        request = backend_request(
            "ai-hedge-fund",
            self.run_dir,
            mode="paper",
            input_data={
                "symbols": ["AAPL"],
                "mode": "paper",
                "start_date": "2026-06-01",
                "end_date": "2026-07-14",
                "initial_cash": 250000,
                "margin_requirement": 0.25,
                "selected_analysts": ["technical_analyst"],
            },
        )
        with mock.patch.dict(sys.modules, modules):
            response = ai_hedge_fund_backend.handle_request(request)

        self.assertEqual(response["status"], "success")
        self.assertEqual(captured["tickers"], ["AAPL"])
        self.assertEqual(captured["portfolio"]["cash"], 250000.0)
        self.assertEqual(captured["portfolio"]["positions"]["AAPL"]["long"], 0)
        self.assertNotIn("orders", captured["portfolio"])
        self.assertEqual(response["data"]["decisions"]["AAPL"]["action"], "hold")
        self.assertIn("# AI投研经理结论", response["output"])
        self.assertIn("组合裁决", response["output"])
        self.assertFalse(response["data"]["safety"]["order_submission"])

    def test_ai_hedge_fund_default_zero_confidence_decision_is_degraded(self):
        main_module = types.ModuleType("src.main")

        def run_hedge_fund(**kwargs):
            del kwargs
            return {
                "decisions": {
                    "AAPL": {
                        "action": "hold",
                        "quantity": 0,
                        "confidence": 0,
                        "reasoning": "Default decision: hold",
                    }
                },
                "analyst_signals": {},
            }

        main_module.run_hedge_fund = run_hedge_fund
        modules = {
            "src": package_module("src"),
            "src.main": main_module,
        }
        request = backend_request(
            "ai-hedge-fund",
            self.run_dir,
            input_data={
                "symbols": ["AAPL"],
                "start_date": "2026-06-01",
                "end_date": "2026-07-14",
            },
        )
        with mock.patch.dict(sys.modules, modules):
            response = ai_hedge_fund_backend.handle_request(request)

        self.assertEqual(response["status"], "degraded")
        self.assertFalse(response["data"]["quality_gate"]["passed"])
        self.assertGreaterEqual(len(response["data"]["quality_gate"]["issues"]), 2)
        self.assertTrue(
            all(
                "not counted as success" in warning
                for warning in response["warnings"]
                if "Default decision" in warning or "non-positive-confidence" in warning
            )
        )

        main_module.run_hedge_fund = lambda **kwargs: {
            "decisions": "Default decision: hold",
            "analyst_signals": {},
        }
        with mock.patch.dict(sys.modules, modules):
            output_only = ai_hedge_fund_backend.handle_request(request)
        self.assertEqual(output_only["status"], "degraded")
        self.assertFalse(output_only["data"]["quality_gate"]["passed"])

        main_module.run_hedge_fund = lambda **kwargs: {
            "decisions": {
                "AAPL": {
                    "action": "hold",
                    "confidence": 0,
                    "reasoning": "The available evidence is inconclusive.",
                }
            },
            "analyst_signals": {},
        }
        with mock.patch.dict(sys.modules, modules):
            missing_quantity = ai_hedge_fund_backend.handle_request(request)
        self.assertEqual(missing_quantity["status"], "degraded")
        self.assertTrue(
            any(
                "non-positive-confidence" in issue
                for issue in missing_quantity["data"]["quality_gate"]["issues"]
            )
        )

    def test_ai_hedge_fund_high_confidence_fallback_and_error_signal_are_degraded(self):
        for fallback_text in (
            "No valid trade available",
            "Defaulting to a neutral signal",
            "Insufficient data for analysis",
            "Missing price data for AAPL",
            "Error in analysis",
            "Error generating the analyst signal",
            "Parsing error in model response",
        ):
            with self.subTest(fallback_text=fallback_text):
                self.assertTrue(
                    ai_hedge_fund_backend._fallback_text_markers(fallback_text)
                )

        main_module = types.ModuleType("src.main")

        def run_hedge_fund(**kwargs):
            del kwargs
            return {
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
                                "Error generating analysis: missing price data; "
                                "defaulting to neutral."
                            ),
                        }
                    },
                    "fundamentals_analyst_agent": {
                        "AAPL": {
                            "signal": "neutral",
                            "confidence": 0,
                            "reasoning": "Evidence was returned without a usable score.",
                        }
                    }
                },
                "risk_management": {
                    "AAPL": {"reasoning": "Missing price data for risk calculation"}
                },
            }

        main_module.run_hedge_fund = run_hedge_fund
        modules = {
            "src": package_module("src"),
            "src.main": main_module,
        }
        request = backend_request(
            "ai-hedge-fund",
            self.run_dir,
            input_data={
                "symbols": ["AAPL"],
                "start_date": "2026-06-01",
                "end_date": "2026-07-14",
                "selected_analysts": [
                    "technical_analyst",
                    "fundamentals_analyst",
                ],
            },
        )
        with mock.patch.dict(sys.modules, modules):
            response = ai_hedge_fund_backend.handle_request(request)

        self.assertEqual(response["status"], "degraded")
        quality = response["data"]["quality_gate"]
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
        self.assertTrue(
            any(
                "fundamentals_analyst_agent/AAPL" in issue
                and "non-positive-confidence" in issue
                for issue in quality["issues"]
            )
        )
        self.assertTrue(
            any("missing price data" in issue for issue in quality["issues"])
        )

        main_module.run_hedge_fund = lambda **kwargs: {
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
                    "AAPL": {
                        "signal": "neutral",
                        "reasoning": "Parsing error in upstream analysis",
                    }
                }
            },
        }
        no_selection_request = backend_request(
            "ai-hedge-fund",
            self.run_dir,
            input_data={
                "symbols": ["AAPL"],
                "start_date": "2026-06-01",
                "end_date": "2026-07-14",
            },
        )
        with mock.patch.dict(sys.modules, modules):
            no_selection = ai_hedge_fund_backend.handle_request(no_selection_request)
        self.assertEqual(no_selection["status"], "degraded")
        self.assertFalse(no_selection["data"]["quality_gate"]["passed"])
        self.assertTrue(
            any(
                "parsing error" in issue
                for issue in no_selection["data"]["quality_gate"]["issues"]
            )
        )

    def test_fingpt_calls_configured_callable_and_confines_artifacts(self):
        module = types.ModuleType("reviewed_fingpt")
        seen = {}

        def analyze(payload):
            seen.update(payload)
            artifact = Path(payload["run_dir"]) / "sentiment.json"
            artifact.write_text('{"sentiment":"neutral"}', encoding="utf-8")
            return {
                "status": "success",
                "output": "neutral",
                "data": {"score": 0.0},
                "warnings": [],
                "artifacts": [str(artifact)],
            }

        module.analyze = analyze
        request = backend_request(
            "fingpt",
            self.run_dir,
            input_data={"symbols": ["AAPL"], "mode": "research", "question": "sentiment"},
        )
        with mock.patch.dict(sys.modules, {"reviewed_fingpt": module}), mock.patch.dict(
            os.environ,
            {"QUANT_TRADER_FINGPT_CALLABLE": "reviewed_fingpt:analyze"},
            clear=False,
        ):
            response = fingpt_backend.handle_request(request)

        self.assertEqual(response["status"], "success")
        self.assertFalse(seen["safety"]["live_trading"])
        self.assertFalse(seen["safety"]["order_submission"])
        self.assertEqual(response["artifacts"][0]["path"], "sentiment.json")
        self.assertEqual(response["data"]["configured_callable"], "reviewed_fingpt:analyze")

    def test_fingpt_without_callable_is_unavailable(self):
        request = backend_request("fingpt", self.run_dir)
        with mock.patch.dict(os.environ, {}, clear=False):
            os.environ.pop("QUANT_TRADER_FINGPT_CALLABLE", None)
            response = fingpt_backend.handle_request(request)
        self.assertEqual(response["status"], "unavailable")
        self.assertTrue(response["warnings"])

    def test_fingpt_financial_intelligence_output_is_evidence_bounded(self):
        text = (
            "新安股份有限公司于2026年7月14日公告，公司签署一份长期供货合同。"
            "公告同时提示原材料价格波动和合同履约存在不确定性。"
        )
        evidence = fingpt_minimax_callable._extract_financial_evidence(
            text, ["600596.SS"]
        )
        output = fingpt_minimax_callable._render_financial_intelligence(
            text, "neutral", 0, evidence
        )

        self.assertIn("AI金信分析师", output)
        self.assertIn("2026年7月14日", evidence["dates"])
        self.assertIn("600596.SS", evidence["entities"])
        self.assertTrue(any("供货合同" in item for item in evidence["event_evidence"]))
        self.assertIn("不代表已由外部来源核实", output)

    def test_fingpt_async_callable_cannot_override_wrapper_safety(self):
        module = types.ModuleType("async_fingpt")

        async def analyze(payload):
            await asyncio.sleep(0)
            return {
                "output": "async result",
                "data": {"safety": {"live_trading": True}},
                "warnings": [],
                "artifacts": [],
            }

        module.analyze = analyze
        request = backend_request("fingpt", self.run_dir)
        with mock.patch.dict(sys.modules, {"async_fingpt": module}), mock.patch.dict(
            os.environ,
            {"QUANT_TRADER_FINGPT_CALLABLE": "async_fingpt:analyze"},
            clear=False,
        ):
            response = fingpt_backend.handle_request(request)
        self.assertEqual(response["status"], "success")
        self.assertEqual(response["output"], "async result")
        self.assertFalse(response["data"]["safety"]["live_trading"])

    def test_ndjson_runner_emits_one_compact_response_per_request(self):
        request = backend_request("fingpt", self.run_dir)
        source = io.StringIO(json.dumps(request) + "\n" + "not-json\n")
        target = io.StringIO()

        def handler(raw):
            return _common.make_response("success", output=raw["request_id"])

        with mock.patch.object(sys, "stdin", source), redirect_stdout(target):
            _common.run_ndjson("fingpt", handler)
        lines = [json.loads(line) for line in target.getvalue().splitlines()]
        self.assertEqual(len(lines), 2)
        self.assertEqual(lines[0]["status"], "success")
        self.assertEqual(lines[1]["status"], "error")
        self.assertEqual(set(lines[0]), {"status", "output", "data", "warnings", "artifacts"})


if __name__ == "__main__":
    unittest.main()
