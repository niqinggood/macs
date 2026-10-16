import json
import os
from pathlib import Path
import subprocess
import shutil
import sys
import tempfile
import textwrap
import unittest
from unittest import mock


ROOT = Path(__file__).resolve().parents[1]
if str(ROOT) not in sys.path:
    sys.path.insert(0, str(ROOT))

from adapters import BACKEND_PROTOCOL, ExternalBackendAdapter
from professional_agent import PROTOCOL, QuantProfessionalAgent

LONG_FINANCIAL_TEXT = (
    "公司公告显示本年度主营业务收入和归母净利润同比增长，同时提示原材料价格波动、"
    "海外需求变化及汇率风险可能影响后续经营表现。"
)


def request(request_id="r1", capability=None, input_data=None, **extra):
    employee_id = str(extra.get("employee_id") or "quant-trader")
    if capability is None:
        capability = {
            "quant-trader": "trading",
            "investment-research-manager": "research-management",
            "financial-intelligence-analyst": "financial-intelligence",
        }.get(employee_id, "analyze")
    value = {
        "protocol": PROTOCOL,
        "request_id": request_id,
        "agent_id": "quant-trader",
        "employee_id": employee_id,
        "tenant_id": "default",
        "session_id": "test-session",
        "execution_id": "test-execution",
        "step_index": 0,
        "capability": capability,
        "input": json.dumps(input_data or {}),
    }
    value.update(extra)
    return value


class FakeAdapter:
    def __init__(self, name, result=None):
        self.name = name
        self.result = result or {
            "backend": name,
            "status": "unavailable",
            "output": "",
            "data": {},
            "warnings": [name + " unavailable"],
            "artifacts": [],
        }
        self.requests = []

    def configuration(self):
        return {"backend": self.name, "state": "unavailable", "transport": "none"}

    def invoke(self, backend_request):
        self.requests.append(backend_request)
        return dict(self.result)


class QuantProfessionalAgentTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.run_root = Path(self.temp.name) / "runs"
        self.run_root.mkdir()
        self.environ = {"OPENKITTY_AGENT_RUN_DIR": str(self.run_root), "PATH": os.environ.get("PATH", "")}

    def tearDown(self):
        self.temp.cleanup()

    def test_manifest_declares_required_contract(self):
        manifest = json.loads((ROOT / "manifest.json").read_text(encoding="utf-8"))
        self.assertEqual(manifest["protocol"], PROTOCOL)
        self.assertEqual(manifest["id"], "quant-trader")
        self.assertEqual(
            set(manifest["allowed_employees"]),
            {
                "quant-trader",
                "investment-research-manager",
                "financial-intelligence-analyst",
            },
        )
        self.assertEqual(
            set(manifest["capabilities"]),
            {
                "analyze",
                "health",
                "trading",
                "research-management",
                "financial-intelligence",
            },
        )
        self.assertEqual(manifest["command"], "python")
        self.assertEqual(manifest["args"], ["-u", "worker.py"])
        self.assertGreater(manifest["max_output_bytes"], 0)
        self.assertGreaterEqual(manifest["max_timeout_seconds"], 1200)

    def test_backend_timeouts_match_workload_and_remain_overrideable(self):
        trading = ExternalBackendAdapter("tradingagents", self.environ)
        hedge = ExternalBackendAdapter("ai-hedge-fund", self.environ)
        fingpt = ExternalBackendAdapter("fingpt", self.environ)
        self.assertEqual(trading.timeout_seconds, 900)
        self.assertEqual(hedge.timeout_seconds, 300)
        self.assertEqual(fingpt.timeout_seconds, 300)

        configured = dict(self.environ)
        configured["QUANT_TRADER_TRADINGAGENTS_TIMEOUT_SECONDS"] = "123"
        self.assertEqual(
            ExternalBackendAdapter("tradingagents", configured).timeout_seconds,
            123,
        )

    def test_wrong_employee_is_rejected(self):
        agent = QuantProfessionalAgent(self.environ)
        response = agent.handle(request(employee_id="main-agent"))
        self.assertEqual(response["status"], "failed")
        self.assertTrue(response["error"])
        self.assertEqual(response["artifacts"], [])

    def test_live_mode_and_execution_flags_are_rejected(self):
        agent = QuantProfessionalAgent(self.environ)
        live = agent.handle(request("live", input_data={"mode": "live"}))
        flagged = agent.handle(
            request("flagged", input_data={"mode": "paper", "execute_orders": True})
        )
        self.assertEqual(live["status"], "failed")
        self.assertEqual(flagged["status"], "failed")

    def test_unconfigured_backends_return_unavailable_without_fabrication(self):
        adapters = {
            name: FakeAdapter(name)
            for name in ("tradingagents", "ai-hedge-fund", "fingpt")
        }
        agent = QuantProfessionalAgent(self.environ, adapters)
        response = agent.handle(request(input_data={"mode": "research"}))
        self.assertEqual(response["status"], "unavailable")
        self.assertIn("no analysis was fabricated", response["output"].lower())
        self.assertEqual(len(response["data"]["backend_results"]), 1)
        self.assertEqual(response["data"]["backend_results"][0]["backend"], "tradingagents")

    def test_backend_request_forces_non_live_safety(self):
        adapter = FakeAdapter(
            "fingpt",
            {
                "backend": "fingpt",
                "status": "success",
                "output": "real backend output",
                "data": {},
                "warnings": [],
                "artifacts": [],
            },
        )
        agent = QuantProfessionalAgent(self.environ, {"fingpt": adapter})
        response = agent.handle(
            request(
                input_data={
                    "mode": "paper",
                    "frameworks": ["fingpt"],
                    "text": LONG_FINANCIAL_TEXT,
                },
                employee_id="financial-intelligence-analyst",
            )
        )
        self.assertEqual(response["status"], "completed")
        sent = adapter.requests[0]
        self.assertEqual(sent["protocol"], BACKEND_PROTOCOL)
        self.assertFalse(sent["safety"]["live_trading"])
        self.assertFalse(sent["safety"]["order_submission"])
        self.assertTrue(sent["safety"]["paper_trading_only"])

    def test_combined_framework_request_is_rejected_by_role_isolation(self):
        adapters = {
            name: FakeAdapter(
                name,
                {
                    "backend": name,
                    "status": "success",
                    "output": name + " real-shaped result",
                    "data": {},
                    "warnings": [],
                    "artifacts": [],
                },
            )
            for name in ("tradingagents", "ai-hedge-fund", "fingpt")
        }
        agent = QuantProfessionalAgent(self.environ, adapters)
        raw = (
            "请结合 TradingAgents、AI Hedge Fund 和 FinGPT 分析 AAPL、NVDA，"
            "截至 2026-07-11，只做研究。"
        )
        req = request("plain-language")
        req["input"] = raw
        response = agent.handle(req)

        self.assertEqual(response["status"], "failed")
        self.assertIn("isolated", response["error"])
        self.assertTrue(all(not adapter.requests for adapter in adapters.values()))

    def test_plain_language_can_select_one_backend_and_paper_mode(self):
        adapter = FakeAdapter(
            "tradingagents",
            {
                "backend": "tradingagents",
                "status": "success",
                "output": "paper result",
                "data": {},
                "warnings": [],
                "artifacts": [],
            },
        )
        agent = QuantProfessionalAgent(self.environ, {"tradingagents": adapter})
        req = request("plain-paper")
        req["input"] = "请只用 TradingAgents 做 AAPL 模拟盘研究"
        response = agent.handle(req)

        self.assertEqual(response["status"], "completed")
        self.assertEqual(response["data"]["mode"], "paper")
        self.assertEqual(len(adapter.requests), 1)
        self.assertEqual(adapter.requests[0]["input"]["symbols"], ["AAPL"])
        self.assertTrue(adapter.requests[0]["safety"]["paper_trading_only"])

    def test_plain_language_normalizes_common_shanghai_suffix_without_ghost_symbol(self):
        adapter = FakeAdapter(
            "tradingagents",
            {
                "backend": "tradingagents",
                "status": "success",
                "output": "research result",
                "data": {},
                "warnings": [],
                "artifacts": [],
            },
        )
        agent = QuantProfessionalAgent(self.environ, {"tradingagents": adapter})
        req = request("plain-shanghai-suffix")
        req["input"] = "只做研究，请分析新安股份（600596.SH）的技术结构和风险"
        response = agent.handle(req)

        self.assertEqual(response["status"], "completed")
        self.assertEqual(response["data"]["security_resolution"]["symbols"], ["600596.SS"])
        self.assertEqual(adapter.requests[0]["input"]["symbols"], ["600596.SS"])
        self.assertNotIn("SH", adapter.requests[0]["input"]["symbols"])

    def test_plain_language_live_order_is_rejected_before_backends(self):
        adapters = {
            name: FakeAdapter(name)
            for name in ("tradingagents", "ai-hedge-fund", "fingpt")
        }
        agent = QuantProfessionalAgent(self.environ, adapters)
        req = request("plain-live")
        req["input"] = "帮我用真实资金立即买入 AAPL 并提交订单"
        response = agent.handle(req)

        self.assertEqual(response["status"], "failed")
        self.assertIn("live-order", response["error"])
        self.assertTrue(all(not adapter.requests for adapter in adapters.values()))

    def test_chinese_real_order_wording_is_rejected_before_backends(self):
        adapters = {
            name: FakeAdapter(name)
            for name in ("tradingagents", "ai-hedge-fund", "fingpt")
        }
        agent = QuantProfessionalAgent(self.environ, adapters)
        req = request("real-order-zh")
        req["input"] = "请真实下单买入新安股份"

        response = agent.handle(req)

        self.assertEqual(response["status"], "failed")
        self.assertIn("live-order", response["error"])
        self.assertTrue(all(not adapter.requests for adapter in adapters.values()))

    def test_json_question_with_live_order_language_is_also_rejected(self):
        adapter = FakeAdapter("fingpt")
        agent = QuantProfessionalAgent(self.environ, {"fingpt": adapter})
        response = agent.handle(
            request(
                "json-live-language",
                input_data={
                    "mode": "research",
                    "symbols": ["AAPL"],
                    "frameworks": ["fingpt"],
                    "question": "Please place an order to buy AAPL now.",
                },
            )
        )

        self.assertEqual(response["status"], "failed")
        self.assertEqual(adapter.requests, [])

    def test_clear_local_natural_language_negations_are_allowed(self):
        safe_questions = [
            "请用 FinGPT 研究 AAPL，不执行交易、不提交订单，也不要用真实资金。",
            "禁止实盘交易，请仅用 FinGPT 研究 AAPL。",
            "请用 FinGPT 研究 AAPL，现在不要买入。",
            "不要真实下单，只用 FinGPT 研究 AAPL。",
            "Use FinGPT to research AAPL. Do not place an order. Do not execute the trade.",
            "Live trading is disabled; use FinGPT to research AAPL only.",
        ]
        for index, question in enumerate(safe_questions):
            with self.subTest(question=question):
                adapter = FakeAdapter(
                    "fingpt",
                    {
                        "backend": "fingpt",
                        "status": "success",
                        "output": "research result",
                        "data": {},
                        "warnings": [],
                        "artifacts": [],
                    },
                )
                agent = QuantProfessionalAgent(self.environ, {"fingpt": adapter})
                req = request(
                    "negated-%d" % index,
                    employee_id="financial-intelligence-analyst",
                )
                req["input"] = question + " 待分析正文：" + LONG_FINANCIAL_TEXT

                response = agent.handle(req)

                self.assertEqual(response["status"], "completed")
                self.assertEqual(len(adapter.requests), 1)

    def test_unrelated_or_partial_negation_does_not_bypass_live_order_guard(self):
        unsafe_questions = [
            "不要分析风险，马上用 FinGPT 帮我下单 AAPL。",
            "不要提交订单，但现在用 FinGPT 帮我下单 AAPL。",
            "Do not summarize. Use FinGPT to place an order to buy AAPL now.",
            "Do not place an order, but execute the trade with FinGPT.",
        ]
        for index, question in enumerate(unsafe_questions):
            with self.subTest(question=question):
                adapter = FakeAdapter("fingpt")
                agent = QuantProfessionalAgent(self.environ, {"fingpt": adapter})
                req = request("not-a-bypass-%d" % index)
                req["input"] = question

                response = agent.handle(req)

                self.assertEqual(response["status"], "failed")
                self.assertIn("live-order", response["error"])
                self.assertEqual(adapter.requests, [])

    def test_structured_live_flags_override_negated_question_text(self):
        cases = [
            {"mode": "live", "question": "不要实盘，只做研究。"},
            {
                "mode": "research",
                "frameworks": ["fingpt"],
                "submit_order": True,
                "question": "Do not place an order; research only.",
            },
        ]
        for index, input_data in enumerate(cases):
            with self.subTest(input_data=input_data):
                adapter = FakeAdapter("fingpt")
                agent = QuantProfessionalAgent(self.environ, {"fingpt": adapter})

                response = agent.handle(request("structured-%d" % index, input_data=input_data))

                self.assertEqual(response["status"], "failed")
                self.assertEqual(adapter.requests, [])

    def test_run_dir_is_unique_and_requires_host_directory(self):
        agent = QuantProfessionalAgent(self.environ)
        first = agent.handle(request("same", capability="health"))
        second = agent.handle(request("same", capability="health"))
        self.assertNotEqual(first["data"]["run_dir"], second["data"]["run_dir"])
        self.assertLessEqual(len(Path(first["data"]["run_dir"]).name), 25)
        with self.assertRaises(ValueError):
            QuantProfessionalAgent({"OPENKITTY_AGENT_RUN_DIR": str(Path(self.temp.name) / "missing")})

    def test_artifacts_are_confined_to_request_run_dir(self):
        outside = Path(self.temp.name) / "outside.txt"
        outside.write_text("outside", encoding="utf-8")

        class ArtifactAdapter(FakeAdapter):
            def invoke(inner_self, backend_request):
                backend_dir = Path(backend_request["run_dir"])
                inside = backend_dir / "inside.json"
                inside.write_text("{}", encoding="utf-8")
                return {
                    "backend": "fingpt",
                    "status": "success",
                    "output": "backend output",
                    "data": {},
                    "warnings": [],
                    "artifacts": [str(inside), str(outside), "missing.json"],
                }

        agent = QuantProfessionalAgent(self.environ, {"fingpt": ArtifactAdapter("fingpt")})
        response = agent.handle(
            request(
                input_data={
                    "mode": "research",
                    "frameworks": ["fingpt"],
                    "text": LONG_FINANCIAL_TEXT,
                },
                employee_id="financial-intelligence-analyst",
            )
        )
        artifact_names = {Path(item["path"]).name for item in response["artifacts"]}
        self.assertEqual(artifact_names, {"inside.json", "structured_result.json"})
        for artifact in response["artifacts"]:
            self.assertFalse(Path(artifact["path"]).is_absolute())
            self.assertTrue((self.run_root / artifact["path"]).is_file())
        self.assertTrue(any("escaped" in warning for warning in response["warnings"]))
        self.assertTrue(any("does not exist" in warning for warning in response["warnings"]))

    def test_process_adapter_uses_external_python_and_normalizes_response(self):
        adapter_script = Path(self.temp.name) / "fake_backend.py"
        adapter_script.write_text(
            textwrap.dedent(
                """
                import json, pathlib, sys
                request = json.load(sys.stdin)
                path = pathlib.Path(request["run_dir"]) / "result.json"
                path.write_text(json.dumps({"safe": request["safety"]}), encoding="utf-8")
                json.dump({
                    "status": "success",
                    "output": "external process result",
                    "data": {"backend": request["backend"]},
                    "warnings": [],
                    "artifacts": [str(path)]
                }, sys.stdout)
                """
            ),
            encoding="utf-8",
        )
        env = dict(self.environ)
        env.update(
            {
                "QUANT_TRADER_FINGPT_PYTHON": sys.executable,
                "QUANT_TRADER_FINGPT_ENTRYPOINT": str(adapter_script),
            }
        )
        agent = QuantProfessionalAgent(env)
        response = agent.handle(
            request(
                input_data={
                    "mode": "research",
                    "frameworks": ["fingpt"],
                    "text": LONG_FINANCIAL_TEXT,
                },
                employee_id="financial-intelligence-analyst",
            )
        )
        self.assertEqual(response["status"], "completed")
        self.assertIn("external process result", response["output"])
        self.assertEqual(
            {Path(item["path"]).name for item in response["artifacts"]},
            {"result.json", "structured_result.json"},
        )

    def test_process_adapter_prefers_agent_scoped_credentials(self):
        adapter_script = Path(self.temp.name) / "credential_backend.py"
        adapter_script.write_text(
            textwrap.dedent(
                """
                import json, os, sys
                request = json.load(sys.stdin)
                json.dump({
                    "status": "success",
                    "output": "credential mapping checked",
                    "data": {"credential": os.environ.get("OPENAI_API_KEY", "")},
                    "warnings": [],
                    "artifacts": []
                }, sys.stdout)
                """
            ),
            encoding="utf-8",
        )
        env = dict(self.environ)
        env.update(
            {
                "QUANT_TRADER_FINGPT_PYTHON": sys.executable,
                "QUANT_TRADER_FINGPT_ENTRYPOINT": str(adapter_script),
                "QUANT_TRADER_FINGPT_ENV_ALLOWLIST": "OPENAI_API_KEY",
                "QUANT_TRADER_OPENAI_API_KEY": "agent-scoped",
                "OPENAI_API_KEY": "host-generic-must-not-win",
            }
        )
        agent = QuantProfessionalAgent(env)
        response = agent.handle(
            request(
                input_data={
                    "mode": "research",
                    "frameworks": ["fingpt"],
                    "text": LONG_FINANCIAL_TEXT,
                },
                employee_id="financial-intelligence-analyst",
            )
        )
        backend = response["data"]["backend_results"][0]
        self.assertEqual(backend["data"]["credential"], "agent-scoped")

    def test_dedicated_python_defaults_to_bundled_upstream_shim(self):
        adapter = ExternalBackendAdapter(
            "tradingagents",
            {
                "PATH": os.environ.get("PATH", ""),
                "QUANT_TRADER_TRADINGAGENTS_PYTHON": sys.executable,
            },
        )
        configuration = adapter.configuration()
        self.assertTrue(configuration["bundled_shim"])
        self.assertEqual(configuration["state"], "configured")
        self.assertTrue(adapter.entrypoint.endswith("tradingagents_backend.py"))

    def _create_discoverable_runtime(self):
        workspace = Path(self.temp.name) / "portable-workspace"
        runtime = workspace / ".openkitty" / "runtime" / "quant-trader"
        layouts = {
            "tradingagents": ("TradingAgents", ("tradingagents",)),
            "ai-hedge-fund": ("ai-hedge-fund", ("src",)),
            "fingpt": ("FinGPT", ("fingpt", "finogrid")),
        }
        for backend, (repo_name, markers) in layouts.items():
            repository = runtime / repo_name
            for marker in markers:
                (repository / marker).mkdir(parents=True, exist_ok=True)
            python = runtime / "venvs" / backend / "Scripts" / "python.exe"
            python.parent.mkdir(parents=True, exist_ok=True)
            shutil.copy2(sys.executable, python)
        run_dir = workspace / ".openkitty" / "professional-agents" / "quant-trader" / "run"
        run_dir.mkdir(parents=True)
        return runtime, run_dir

    def test_backends_auto_discover_from_openkitty_run_directory(self):
        runtime, run_dir = self._create_discoverable_runtime()
        environment = {
            "OPENKITTY_AGENT_RUN_DIR": str(run_dir),
            "PATH": "",
        }
        for backend in ("tradingagents", "ai-hedge-fund", "fingpt"):
            with self.subTest(backend=backend):
                adapter = ExternalBackendAdapter(backend, environment)
                configuration = adapter.configuration()
                self.assertEqual(configuration["state"], "configured")
                self.assertEqual(configuration["transport"], "process")
                self.assertTrue(configuration["bundled_shim"])
                self.assertTrue(configuration["auto_discovered"])
                self.assertEqual(configuration["discovery_source"], "agent-run-dir")
                self.assertTrue(configuration["runtime_root_configured"])
                self.assertTrue(Path(adapter.repo).is_relative_to(runtime))

    def test_auto_discovered_backends_receive_only_reviewed_non_secret_defaults(self):
        _, run_dir = self._create_discoverable_runtime()
        environment = {
            "OPENKITTY_AGENT_RUN_DIR": str(run_dir),
            "PATH": "",
        }
        with mock.patch.dict(os.environ, {}, clear=True):
            tradingagents = ExternalBackendAdapter("tradingagents", environment)
            ai_hedge_fund = ExternalBackendAdapter("ai-hedge-fund", environment)
            fingpt = ExternalBackendAdapter("fingpt", environment)
            tradingagents_env = tradingagents._process_environment()
            ai_hedge_fund_env = ai_hedge_fund._process_environment()
            fingpt_env = fingpt._process_environment()

        self.assertEqual(tradingagents_env["TRADINGAGENTS_LLM_PROVIDER"], "minimax-cn")
        self.assertEqual(tradingagents_env["TRADINGAGENTS_DEEP_THINK_LLM"], "MiniMax-M3")
        self.assertEqual(tradingagents_env["TRADINGAGENTS_QUICK_THINK_LLM"], "MiniMax-M3")
        self.assertEqual(tradingagents_env["TRADINGAGENTS_OUTPUT_LANGUAGE"], "Chinese")
        self.assertEqual(tradingagents_env["TRADINGAGENTS_MAX_DEBATE_ROUNDS"], "1")
        self.assertEqual(tradingagents_env["TRADINGAGENTS_MAX_RISK_ROUNDS"], "1")
        self.assertEqual(
            fingpt_env["QUANT_TRADER_FINGPT_CALLABLE"],
            "fingpt_minimax_callable:analyze",
        )
        self.assertEqual(fingpt_env["MINIMAX_MODEL"], "MiniMax-M3")
        for child_env in (tradingagents_env, ai_hedge_fund_env, fingpt_env):
            for credential_name in (
                "MINIMAX_CN_API_KEY",
                "OPENAI_API_KEY",
                "FINANCIAL_DATASETS_API_KEY",
                "MINIMAX_API_KEY",
            ):
                self.assertNotIn(credential_name, child_env)

        self.assertTrue(tradingagents.uses_default_env_allowlist)
        self.assertEqual(
            set(ai_hedge_fund.env_allowlist),
            {"OPENAI_API_KEY", "OPENAI_API_BASE", "FINANCIAL_DATASETS_API_KEY"},
        )
        self.assertEqual(
            set(fingpt.env_allowlist),
            {
                "QUANT_TRADER_FINGPT_CALLABLE",
                "MINIMAX_API_KEY",
                "MINIMAX_BASE_URL",
                "MINIMAX_MODEL",
            },
        )

    def test_explicit_allowlist_and_non_secret_values_override_reviewed_defaults(self):
        _, run_dir = self._create_discoverable_runtime()
        adapter = ExternalBackendAdapter(
            "tradingagents",
            {
                "OPENKITTY_AGENT_RUN_DIR": str(run_dir),
                "QUANT_TRADER_TRADINGAGENTS_ENV_ALLOWLIST": (
                    "TRADINGAGENTS_LLM_PROVIDER,TRADINGAGENTS_OUTPUT_LANGUAGE"
                ),
                "QUANT_TRADER_TRADINGAGENTS_LLM_PROVIDER": "reviewed-custom-provider",
                "QUANT_TRADER_TRADINGAGENTS_OUTPUT_LANGUAGE": "English",
                "PATH": "",
            },
        )
        with mock.patch.dict(os.environ, {}, clear=True):
            child_env = adapter._process_environment()
        self.assertFalse(adapter.uses_default_env_allowlist)
        self.assertEqual(
            set(adapter.env_allowlist),
            {"TRADINGAGENTS_LLM_PROVIDER", "TRADINGAGENTS_OUTPUT_LANGUAGE"},
        )
        self.assertEqual(
            child_env["TRADINGAGENTS_LLM_PROVIDER"], "reviewed-custom-provider"
        )
        self.assertEqual(child_env["TRADINGAGENTS_OUTPUT_LANGUAGE"], "English")
        self.assertNotIn("TRADINGAGENTS_DEEP_THINK_LLM", child_env)

    def test_explicit_fingpt_callable_overrides_auto_discovered_default(self):
        _, run_dir = self._create_discoverable_runtime()
        adapter = ExternalBackendAdapter(
            "fingpt",
            {
                "OPENKITTY_AGENT_RUN_DIR": str(run_dir),
                "QUANT_TRADER_FINGPT_CALLABLE": "reviewed_custom:analyze",
                "PATH": "",
            },
        )
        with mock.patch.dict(os.environ, {}, clear=True):
            child_env = adapter._process_environment()
        self.assertEqual(
            child_env["QUANT_TRADER_FINGPT_CALLABLE"],
            "reviewed_custom:analyze",
        )

    def test_explicit_empty_allowlist_disables_bundled_allowlist_defaults(self):
        _, run_dir = self._create_discoverable_runtime()
        adapter = ExternalBackendAdapter(
            "ai-hedge-fund",
            {
                "OPENKITTY_AGENT_RUN_DIR": str(run_dir),
                "QUANT_TRADER_AI_HEDGE_FUND_ENV_ALLOWLIST": "",
                "PATH": "",
            },
        )
        self.assertFalse(adapter.uses_default_env_allowlist)
        self.assertEqual(adapter.env_allowlist, [])

    def test_explicit_backend_transport_wins_over_auto_discovery(self):
        _, run_dir = self._create_discoverable_runtime()
        adapter = ExternalBackendAdapter(
            "fingpt",
            {
                "OPENKITTY_AGENT_RUN_DIR": str(run_dir),
                "QUANT_TRADER_FINGPT_HTTP_ENDPOINT": "https://backend.invalid/analyze",
            },
        )
        configuration = adapter.configuration()
        self.assertEqual(configuration["transport"], "http")
        self.assertFalse(configuration["auto_discovered"])
        self.assertFalse(configuration["bundled_shim"])

    def test_explicit_runtime_root_is_portable_discovery_override(self):
        runtime, _ = self._create_discoverable_runtime()
        adapter = ExternalBackendAdapter(
            "ai-hedge-fund",
            {
                "OPENKITTY_AGENT_RUN_DIR": str(self.run_root),
                "QUANT_TRADER_RUNTIME_ROOT": str(runtime),
                "PATH": "",
            },
        )
        configuration = adapter.configuration()
        self.assertEqual(configuration["state"], "configured")
        self.assertTrue(configuration["auto_discovered"])
        self.assertEqual(configuration["discovery_source"], "explicit-runtime-root")
        self.assertEqual(Path(adapter.repo).parent, runtime.resolve())

    def test_backend_auto_discovery_can_be_disabled(self):
        _, run_dir = self._create_discoverable_runtime()
        adapter = ExternalBackendAdapter(
            "tradingagents",
            {
                "OPENKITTY_AGENT_RUN_DIR": str(run_dir),
                "QUANT_TRADER_AUTO_DISCOVERY": "false",
                "PATH": "",
            },
        )
        configuration = adapter.configuration()
        self.assertEqual(configuration["state"], "unavailable")
        self.assertEqual(configuration["transport"], "none")
        self.assertFalse(configuration["auto_discovered"])

    def test_ndjson_entrypoint_emits_one_protocol_response(self):
        env = os.environ.copy()
        env["OPENKITTY_AGENT_RUN_DIR"] = str(self.run_root)
        completed = subprocess.run(
            [sys.executable, "-u", str(ROOT / "worker.py")],
            input=json.dumps(request("health", capability="health")) + "\n",
            text=True,
            encoding="utf-8",
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            env=env,
            timeout=10,
            check=True,
        )
        lines = [line for line in completed.stdout.splitlines() if line.strip()]
        self.assertEqual(len(lines), 1)
        response = json.loads(lines[0])
        self.assertEqual(response["protocol"], PROTOCOL)
        self.assertEqual(response["request_id"], "health")
        self.assertEqual(response["agent_id"], "quant-trader")
        self.assertEqual(response["status"], "completed")


if __name__ == "__main__":
    unittest.main()
