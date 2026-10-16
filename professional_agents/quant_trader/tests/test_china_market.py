from datetime import date, datetime, timedelta, timezone
import http.client
import json
from pathlib import Path
import sys
import tempfile
import unittest
from unittest import mock


ROOT = Path(__file__).resolve().parents[1]
if str(ROOT) not in sys.path:
    sys.path.insert(0, str(ROOT))

from china_market import (
    ChinaMarketError,
    ChinaSecurityResolver,
    EastmoneyDailyHistory,
    KLINE_ENDPOINT,
    MarketDataError,
    SUGGEST_ENDPOINT,
    TENCENT_KLINE_ENDPOINT,
    _NoRedirectHandler,
    SecurityResolutionError,
    bounded_json_get,
    canonicalize_security_identifier,
    extract_chinese_security_queries,
)
from professional_agent import QuantProfessionalAgent


def suggest_payload(name, code, market, quote_id):
    return {
        "QuotationCodeTable": {
            "Data": [
                {
                    "Code": code,
                    "Name": name,
                    "Classify": "AStock",
                    "SecurityTypeName": market,
                    "QuoteID": quote_id,
                }
            ],
            "Status": 0,
        }
    }


def kline_payload(start="2026-05-25", rows=35):
    first = date.fromisoformat(start)
    values = []
    for index in range(rows):
        day = first + timedelta(days=index)
        base = 10.0 + index / 10.0
        values.append(
            "%s,%.2f,%.2f,%.2f,%.2f,%d,1000000,2.0,0.5,0.1,1.0"
            % (
                day.isoformat(),
                base,
                base + 0.05,
                base + 0.2,
                base - 0.2,
                10000 + index,
            )
        )
    return {
        "rc": 0,
        "data": {"code": "600596", "market": 1, "name": "新安股份", "klines": values},
    }


def tencent_kline_payload(start="2026-05-25", rows=35):
    source = kline_payload(start, rows)["data"]["klines"]
    values = []
    for raw in source:
        parts = raw.split(",")
        values.append(
            [
                parts[0],
                parts[1],
                parts[2],
                parts[3],
                parts[4],
                parts[5],
                "",
                "",
                "",
                "",
            ]
        )
    return {
        "code": 0,
        "msg": "",
        "data": {"sh600596": {"qfqday": values, "qt": {}}},
    }


class FakeAdapter:
    def __init__(self, name):
        self.name = name
        self.requests = []

    def configuration(self):
        return {"backend": self.name, "state": "configured", "transport": "test"}

    def invoke(self, request):
        self.requests.append(request)
        data = {}
        if self.name == "ai-hedge-fund":
            data = {
                "decisions": {
                    "600596.SS": {
                        "action": "hold",
                        "quantity": 0,
                        "confidence": 0.75,
                        "reasoning": "multi-discipline evidence remains balanced",
                    }
                },
                "analyst_signals": {
                    "technical_analyst_agent": {"600596.SS": {"signal": "neutral"}},
                    "fundamentals_analyst_agent": {"600596.SS": {"signal": "neutral"}},
                    "valuation_analyst_agent": {"600596.SS": {"signal": "neutral"}},
                    "news_sentiment_analyst_agent": {"600596.SS": {"signal": "neutral"}},
                },
            }
        return {
            "backend": self.name,
            "status": "success",
            "output": self.name + " result",
            "data": data,
            "warnings": [],
            "artifacts": [],
        }


class ChinaMarketTests(unittest.TestCase):
    def test_explicit_mainland_codes_are_normalized_without_stock_hardcoding(self):
        cases = {
            "600596": ("600596.SS", "1.600596", "SSE"),
            "SH600519": ("600519.SS", "1.600519", "SSE"),
            "000001.SZ": ("000001.SZ", "0.000001", "SZSE"),
            "SZ300750": ("300750.SZ", "0.300750", "SZSE"),
            "920185.BJ": ("920185.BJ", "0.920185", "BSE"),
        }
        for raw, expected in cases.items():
            with self.subTest(raw=raw):
                resolved = canonicalize_security_identifier(raw)
                self.assertIsNotNone(resolved)
                self.assertEqual(
                    (resolved.symbol, resolved.secid, resolved.exchange), expected
                )
        with self.assertRaises(SecurityResolutionError):
            canonicalize_security_identifier("600596.SZ")

    def test_natural_language_extracts_one_or_multiple_chinese_names(self):
        self.assertEqual(
            extract_chinese_security_queries("@量化交易员 分析下新安股份"),
            ["新安股份"],
        )
        self.assertEqual(
            extract_chinese_security_queries("比较宁德时代和比亚迪未来走势"),
            ["宁德时代", "比亚迪"],
        )
        self.assertEqual(
            extract_chinese_security_queries("请结合 TradingAgents 分析 AAPL"),
            [],
        )

    def test_resolver_uses_security_master_and_rejects_ambiguity(self):
        payloads = {
            "新安股份": suggest_payload("新安股份", "600596", "沪A", "1.600596"),
            "贵州茅台": suggest_payload("贵州茅台", "600519", "沪A", "1.600519"),
            "宁德时代": suggest_payload("宁德时代", "300750", "深A", "0.300750"),
        }

        def get_json(endpoint, params):
            self.assertEqual(endpoint, SUGGEST_ENDPOINT)
            return payloads[str(params["input"])]

        resolver = ChinaSecurityResolver(get_json=get_json)
        resolved = resolver.resolve_many(["新安股份", "贵州茅台", "宁德时代"])
        self.assertEqual(
            [item.symbol for item in resolved],
            ["600596.SS", "600519.SS", "300750.SZ"],
        )

        ambiguous = ChinaSecurityResolver(
            get_json=lambda endpoint, params: {
                "QuotationCodeTable": {
                    "Data": [
                        {
                            "Code": "600001",
                            "Name": "同名甲",
                            "SecurityTypeName": "沪A",
                            "QuoteID": "1.600001",
                        },
                        {
                            "Code": "000001",
                            "Name": "同名乙",
                            "SecurityTypeName": "深A",
                            "QuoteID": "0.000001",
                        },
                    ]
                }
            }
        )
        with self.assertRaisesRegex(SecurityResolutionError, "ambiguous"):
            ambiguous.resolve("同名")

    def test_daily_history_validates_and_records_external_provenance(self):
        calls = []

        def get_json(endpoint, params):
            calls.append((endpoint, dict(params)))
            return kline_payload()

        security = canonicalize_security_identifier("600596")
        history = EastmoneyDailyHistory(get_json=get_json)
        rows, provenance = history.history(security, as_of="2026-07-14", limit=30)
        self.assertEqual(calls[0][0], KLINE_ENDPOINT)
        self.assertEqual(calls[0][1]["secid"], "1.600596")
        self.assertEqual(len(calls), 1, "successful primary must not call Tencent")
        self.assertEqual(len(rows), 30)
        self.assertEqual(rows[-1]["date"], "2026-06-28")
        self.assertEqual(provenance["kind"], "external")
        self.assertEqual(provenance["provider"], "eastmoney-push2his")
        self.assertEqual(provenance["adjustment"], "forward-adjusted")
        self.assertEqual(provenance["volume_unit"], "shares")
        self.assertEqual(provenance["source_volume_unit"], "lot")
        self.assertEqual(rows[0]["volume"] % 100, 0)

    def test_eastmoney_disconnect_falls_back_to_tencent_with_actual_provenance(self):
        calls = []

        def get_json(endpoint, params):
            calls.append((endpoint, dict(params)))
            if endpoint == KLINE_ENDPOINT:
                raise ChinaMarketError("RemoteDisconnected")
            self.assertEqual(endpoint, TENCENT_KLINE_ENDPOINT)
            return tencent_kline_payload(rows=40)

        security = canonicalize_security_identifier("600596")
        history = EastmoneyDailyHistory(get_json=get_json)
        rows, provenance = history.history(security, as_of="2026-07-14", limit=30)
        self.assertEqual([item[0] for item in calls], [KLINE_ENDPOINT, TENCENT_KLINE_ENDPOINT])
        self.assertIn("sh600596,day,", calls[1][1]["param"])
        self.assertIn("2026-07-14", calls[1][1]["param"])
        self.assertEqual(len(rows), 30)
        self.assertEqual(provenance["provider"], "tencent-newfqkline")
        self.assertEqual(provenance["fallback_from"], "eastmoney-push2his")
        self.assertEqual(provenance["volume_unit"], "shares")
        self.assertEqual(provenance["source_volume_unit"], "lot")

    def test_eastmoney_and_tencent_lots_normalize_to_same_share_volume(self):
        history = EastmoneyDailyHistory(get_json=lambda endpoint, params: {})
        eastmoney_rows = history._validated_rows(
            history._eastmoney_candidates(kline_payload(rows=30)),
            begin_date=date(2026, 5, 1),
            end_date=date(2026, 7, 14),
            row_limit=30,
            provider="eastmoney-push2his",
        )
        tencent_rows = history._validated_rows(
            history._tencent_candidates(tencent_kline_payload(rows=30), "sh600596"),
            begin_date=date(2026, 5, 1),
            end_date=date(2026, 7, 14),
            row_limit=30,
            provider="tencent-newfqkline",
        )
        self.assertEqual(
            [(row["date"], row["volume"]) for row in eastmoney_rows],
            [(row["date"], row["volume"]) for row in tencent_rows],
        )

    def test_both_daily_history_sources_fail_transparently(self):
        def get_json(endpoint, params):
            if endpoint == KLINE_ENDPOINT:
                raise ChinaMarketError("primary disconnected")
            raise ChinaMarketError("fallback disconnected")

        history = EastmoneyDailyHistory(get_json=get_json)
        with self.assertRaises(MarketDataError) as caught:
            history.history(
                canonicalize_security_identifier("600596"),
                as_of="2026-07-14",
                limit=30,
            )
        message = str(caught.exception)
        self.assertIn("Eastmoney primary", message)
        self.assertIn("Tencent fallback", message)

    def test_tencent_response_contract_rejects_message_missing_node_and_empty_rows(self):
        invalid = [
            {"code": 0, "msg": "upstream error", "data": {}},
            {"code": 0, "msg": "", "data": {}},
            {"code": 0, "msg": "", "data": {"sh600596": {"qfqday": []}}},
        ]
        for payload in invalid:
            with self.subTest(payload=payload), self.assertRaises(MarketDataError):
                EastmoneyDailyHistory._tencent_candidates(payload, "sh600596")

    def test_history_validation_enforces_closed_dates_ohlc_and_duplicate_consistency(self):
        history = EastmoneyDailyHistory(get_json=lambda endpoint, params: {})
        candidates = EastmoneyDailyHistory._eastmoney_candidates(
            kline_payload("2026-05-25", 35)
        )
        rows = history._validated_rows(
            candidates,
            begin_date=date(2026, 6, 1),
            end_date=date(2026, 6, 30),
            row_limit=30,
            provider="test-provider",
        )
        self.assertTrue(all("2026-06-01" <= row["date"] <= "2026-06-30" for row in rows))

        invalid = list(candidates)
        invalid_row = dict(invalid[10])
        invalid_row["date"] = "2026-06-29"
        invalid_row["low"] = "99.00"
        invalid.append(invalid_row)
        with self.assertRaisesRegex(MarketDataError, "OHLCV invariant"):
            history._validated_rows(
                invalid,
                begin_date=date(2026, 6, 1),
                end_date=date(2026, 6, 30),
                row_limit=30,
                provider="test-provider",
            )

        conflicting = list(candidates[:20])
        duplicate = dict(conflicting[0])
        duplicate["close"] = str(float(duplicate["close"]) + 0.01)
        conflicting.append(duplicate)
        with self.assertRaisesRegex(MarketDataError, "conflicting observations"):
            history._validated_rows(
                conflicting,
                begin_date=date(2026, 5, 1),
                end_date=date(2026, 7, 1),
                row_limit=30,
                provider="test-provider",
            )

    def test_tencent_fallback_rejects_legacy_bse_code(self):
        legacy = canonicalize_security_identifier("830000")
        with self.assertRaisesRegex(MarketDataError, "legacy"):
            EastmoneyDailyHistory._tencent_symbol(legacy)

    def test_network_helper_rejects_cross_host_redirect_result(self):
        class RedirectedResponse:
            headers = {"Content-Type": "application/json"}

            def __enter__(self):
                return self

            def __exit__(self, *args):
                return False

            def geturl(self):
                return "https://example.com/redirected"

            def read(self, limit):
                return b'{"ok":true}'

        with self.assertRaisesRegex(ChinaMarketError, "redirect"):
            bounded_json_get(
                SUGGEST_ENDPOINT,
                {"input": "test", "type": 14},
                opener=lambda request, timeout: RedirectedResponse(),
            )

    def test_daily_history_excludes_current_unfinished_china_session(self):
        security = canonicalize_security_identifier("600596")
        history = EastmoneyDailyHistory(
            get_json=lambda endpoint, params: kline_payload("2026-06-10", 35),
            now=lambda: datetime(
                2026, 7, 14, 9, 30, tzinfo=timezone(timedelta(hours=8))
            ),
        )
        rows, provenance = history.history(security, as_of="2026-07-14", limit=35)
        self.assertEqual(rows[-1]["date"], "2026-07-13")
        self.assertEqual(provenance["last_observation"], "2026-07-13")
        self.assertTrue(provenance["completed_daily_bars_only"])

    def test_network_helper_rejects_unapproved_hosts_before_opening(self):
        with self.assertRaisesRegex(ChinaMarketError, "allowlist"):
            bounded_json_get(
                "https://example.com/steal",
                {},
                opener=lambda *args, **kwargs: self.fail("opener must not run"),
            )

    def test_network_helper_retries_one_remote_disconnect_under_shared_deadline(self):
        calls = []

        class Response:
            headers = {"Content-Type": "application/json; charset=utf-8"}

            def __enter__(self):
                return self

            def __exit__(self, *args):
                return False

            def read(self, limit):
                return json.dumps({"ok": True}).encode("utf-8")

        def opener(request, timeout):
            calls.append(timeout)
            if len(calls) == 1:
                raise http.client.RemoteDisconnected("peer closed connection")
            return Response()

        payload = bounded_json_get(
            SUGGEST_ENDPOINT,
            {"input": "test", "type": 14},
            timeout_seconds=2,
            opener=opener,
        )
        self.assertEqual(payload, {"ok": True})
        self.assertEqual(len(calls), 2)
        self.assertLessEqual(calls[1], calls[0])

    def test_network_helper_bypasses_ambient_system_proxy_for_fixed_hosts(self):
        class Response:
            headers = {"Content-Type": "application/json"}

            def __enter__(self):
                return self

            def __exit__(self, *args):
                return False

            def read(self, limit):
                return b'{"ok":true}'

        direct = mock.Mock()
        direct.open.return_value = Response()
        with mock.patch("china_market.urlrequest.build_opener", return_value=direct) as build:
            payload = bounded_json_get(SUGGEST_ENDPOINT, {"input": "test", "type": 14})

        self.assertEqual(payload, {"ok": True})
        handler = build.call_args.args[0]
        self.assertEqual(handler.proxies, {})
        self.assertIsInstance(build.call_args.args[1], _NoRedirectHandler)
        direct.open.assert_called_once()

    def test_professional_agent_routes_real_a_share_history_to_graph_backends_only(self):
        def get_json(endpoint, params):
            if endpoint == SUGGEST_ENDPOINT:
                self.assertEqual(params["input"], "新安股份")
                return suggest_payload("新安股份", "600596", "沪A", "1.600596")
            if endpoint == KLINE_ENDPOINT:
                return kline_payload(rows=45)
            self.fail("unexpected endpoint")

        adapters = {
            name: FakeAdapter(name)
            for name in ("tradingagents", "ai-hedge-fund", "fingpt")
        }
        with tempfile.TemporaryDirectory() as temp:
            run_root = Path(temp) / "runs"
            run_root.mkdir()
            agent = QuantProfessionalAgent(
                {"OPENKITTY_AGENT_RUN_DIR": str(run_root)},
                adapters,
                security_resolver=ChinaSecurityResolver(get_json=get_json),
                market_history=EastmoneyDailyHistory(get_json=get_json),
            )
            request = {
                "protocol": "openkitty.professional-agent/v1",
                "request_id": "china-routing",
                "agent_id": "quant-trader",
                "employee_id": "quant-trader",
                "tenant_id": "default",
                "session_id": "s1",
                "execution_id": "e1",
                "step_index": 0,
                "capability": "analyze",
                "input": "@量化交易员 分析下新安股份",
            }
            response = agent.handle(request)
            manager_response = agent.handle(
                {
                    **request,
                    "request_id": "china-manager-routing",
                    "employee_id": "investment-research-manager",
                    "capability": "research-management",
                    "input": "@投研经理 分析下新安股份",
                }
            )
            intelligence_response = agent.handle(
                {
                    **request,
                    "request_id": "china-intelligence-routing",
                    "employee_id": "financial-intelligence-analyst",
                    "capability": "financial-intelligence",
                    "input": json.dumps(
                        {
                            "mode": "research",
                            "symbols": ["600596.SS"],
                            "text": (
                                "新安股份公告显示本年度主营业务收入和归母净利润同比增长，同时提示"
                                "原材料价格波动、海外需求变化及汇率风险可能影响后续经营表现。"
                            ),
                        },
                        ensure_ascii=False,
                    ),
                }
            )
            explicit_response = agent.handle(
                {
                    **request,
                    "request_id": "china-explicit-model",
                    "employee_id": "investment-research-manager",
                    "capability": "research-management",
                    "input": json.dumps(
                        {
                            "mode": "research",
                            "symbols": ["600596"],
                            "frameworks": ["ai-hedge-fund"],
                            "price_history": [{"source": "caller-test"}],
                            "price_source": "request-fixture",
                            "market_data_provenance": {
                                "kind": "external",
                                "provider": "tencent-newfqkline",
                                "instruments": [{"symbol": "600596.SS"}],
                            },
                            "model_name": "caller-model",
                            "model_provider": "caller-provider",
                        }
                    ),
                }
            )

        self.assertEqual(response["status"], "completed")
        self.assertEqual(manager_response["status"], "completed")
        self.assertEqual(intelligence_response["status"], "completed")
        self.assertEqual(response["data"]["security_resolution"]["symbols"], ["600596.SS"])
        self.assertEqual(response["data"]["market_data"]["status"], "available")
        self.assertEqual(response["data"]["market_data"]["provider"], "eastmoney-push2his")
        effective_date = response["data"]["market_data"]["effective_as_of"]

        ta = adapters["tradingagents"].requests[0]
        self.assertEqual(ta["input"]["symbols"], ["600596.SS"])
        self.assertEqual(ta["input"]["market_data_source"], "request-fixture")
        self.assertEqual(ta["input"]["tradingagents_analysts"], ["market"])
        self.assertEqual(ta["input"]["as_of"], effective_date)
        self.assertGreaterEqual(len(ta["input"]["price_history"]["600596.SS"]), 20)
        self.assertEqual(ta["market_data_provenance"]["kind"], "external")

        hedge = adapters["ai-hedge-fund"].requests[0]
        self.assertEqual(hedge["input"]["price_source"], "request-fixture")
        self.assertEqual(
            hedge["input"]["ai_hedge_fund_analysts"],
            [
                "technical_analyst",
                "fundamentals_analyst",
                "valuation_analyst",
                "news_sentiment_analyst",
            ],
        )
        self.assertEqual(hedge["input"]["model_name"], "MiniMax-M3")
        self.assertEqual(hedge["input"]["model_provider"], "OpenAI")
        self.assertEqual(hedge["input"]["end_date"], effective_date)
        self.assertEqual(hedge["market_data_provenance"]["provider"], "eastmoney-push2his")

        fingpt = adapters["fingpt"].requests[0]
        self.assertNotIn("price_history", fingpt["input"])
        self.assertNotIn("market_data_provenance", fingpt)
        self.assertIn("新安股份", fingpt["input"]["text"])
        self.assertEqual(explicit_response["status"], "completed")
        explicit_hedge = adapters["ai-hedge-fund"].requests[1]
        self.assertEqual(explicit_hedge["input"]["model_name"], "caller-model")
        self.assertEqual(explicit_hedge["input"]["model_provider"], "caller-provider")
        self.assertNotIn("market_data_provenance", explicit_hedge)
        self.assertEqual(
            explicit_response["data"]["market_data"]["kind"], "unverified-fixture"
        )


if __name__ == "__main__":
    unittest.main()
