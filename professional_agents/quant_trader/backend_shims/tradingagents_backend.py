"""NDJSON adapter for TauricResearch/TradingAgents.

This shim invokes the upstream research graph only.  It does not expose or
call a broker, exchange, or order-submission API.
"""

from __future__ import annotations

from datetime import datetime
import json
import math
from typing import Any, Dict, List, Mapping

try:
    from ._common import (
        BackendUnavailable,
        RequestRejected,
        ValidatedRequest,
        dispatch,
        ensure_cwd_importable,
        extract_date,
        extract_symbols,
        format_output,
        json_safe,
        make_response,
        run_ndjson,
    )
except ImportError:  # Direct script execution.
    from _common import (  # type: ignore
        BackendUnavailable,
        RequestRejected,
        ValidatedRequest,
        dispatch,
        ensure_cwd_importable,
        extract_date,
        extract_symbols,
        format_output,
        json_safe,
        make_response,
        run_ndjson,
    )


BACKEND = "tradingagents"
ALLOWED_ANALYSTS = frozenset({"market", "social", "news", "fundamentals"})
USER_REPORT_FIELDS = (
    ("final_trade_decision", "最终风控裁决"),
    ("trader_investment_plan", "交易执行计划"),
    ("investment_plan", "研究团队结论"),
    ("market_report", "市场与技术依据"),
    ("fundamentals_report", "基本面依据"),
    ("news_report", "新闻依据"),
    ("sentiment_report", "情绪依据"),
)


def _bounded_report(value: Any, limit: int = 8000) -> str:
    text = str(value or "").strip()
    if len(text) <= limit:
        return text
    return text[:limit].rstrip() + "\n\n[内容过长，完整版本见结构化结果产物]"


def _selected_state_reports(state: Any) -> Dict[str, str]:
    if not isinstance(state, Mapping):
        return {}
    reports: Dict[str, str] = {}
    for key, _ in USER_REPORT_FIELDS:
        value = _bounded_report(state.get(key))
        if value:
            reports[key] = value
    return reports


def _trader_markdown(
    symbol: str, as_of: str, decision: Any, reports: Mapping[str, str]
) -> str:
    parts = [
        "# AI交易员研判",
        "",
        "- 标的：%s" % symbol,
        "- 数据截至：%s" % as_of,
        "- 决策标签：%s" % _bounded_report(format_output(decision), 500),
        "- 边界：仅研究/模拟交易，不执行真实下单。",
    ]
    for key, title in USER_REPORT_FIELDS:
        value = reports.get(key, "")
        if value:
            parts.extend(["", "## %s" % title, "", value])
    if not reports:
        parts.extend(
            [
                "",
                "## 说明",
                "",
                "上游本次只返回了决策标签，没有返回可解释的交易计划；请勿把该标签单独当作交易指令。",
            ]
        )
    return "\n".join(parts)


def _selected_analysts(input_data: Mapping[str, Any]) -> List[str]:
    raw = input_data.get(
        "tradingagents_analysts",
        input_data.get("selected_analysts", input_data.get("analysts", ["market"])),
    )
    if raw is None:
        return ["market"]
    if not isinstance(raw, list) or not raw:
        raise RequestRejected("selected_analysts must be a non-empty array")
    result: List[str] = []
    for item in raw:
        if not isinstance(item, str):
            raise RequestRejected("selected_analysts entries must be strings")
        name = item.strip().lower()
        if name not in ALLOWED_ANALYSTS:
            raise RequestRejected("unsupported TradingAgents analyst: %s" % name)
        if name not in result:
            result.append(name)
    return result


def _fixture_frames(input_data: Mapping[str, Any], symbols: List[str]) -> Dict[str, Any]:
    try:
        import pandas as pd
    except ImportError as exc:
        raise BackendUnavailable("TradingAgents fixture support requires pandas") from exc

    raw = input_data.get("price_history")
    if isinstance(raw, list) and len(symbols) == 1:
        raw_by_symbol: Mapping[str, Any] = {symbols[0]: raw}
    elif isinstance(raw, Mapping):
        raw_by_symbol = raw
    else:
        raise RequestRejected(
            "request-fixture market_data_source requires price_history by symbol "
            "(or an array for one symbol)"
        )

    frames: Dict[str, Any] = {}
    for symbol in symbols:
        rows = raw_by_symbol.get(symbol)
        if not isinstance(rows, list) or not (20 <= len(rows) <= 500):
            raise RequestRejected(
                "price_history for %s must contain between 20 and 500 rows" % symbol
            )
        normalized: List[Dict[str, Any]] = []
        for row in rows:
            if not isinstance(row, Mapping):
                raise RequestRejected("every price_history row must be an object")
            date_value = row.get("date", row.get("time"))
            if not isinstance(date_value, str):
                raise RequestRejected("every price_history row needs an ISO date/time")
            try:
                date_value = datetime.fromisoformat(
                    date_value.strip().replace("Z", "+00:00")
                ).date().isoformat()
                values = {
                    name: float(row.get(name))
                    for name in ("open", "high", "low", "close")
                }
                volume = int(row.get("volume", 0))
            except (TypeError, ValueError, OverflowError) as exc:
                raise RequestRejected("price_history contains an invalid OHLCV row") from exc
            if not all(math.isfinite(value) and value > 0 for value in values.values()):
                raise RequestRejected("price_history OHLC values must be finite and positive")
            normalized.append(
                {
                    "date": date_value,
                    "open": values["open"],
                    "high": values["high"],
                    "low": values["low"],
                    "close": values["close"],
                    "volume": max(0, volume),
                }
            )
        frame = pd.DataFrame(normalized).drop_duplicates(subset=["date"], keep="last")
        frame["date"] = pd.to_datetime(frame["date"])
        frame = frame.sort_values("date").set_index("date")
        frames[symbol] = frame
    return frames


def _indicator_values(frame: Any) -> Dict[str, Any]:
    close = frame["close"].astype(float)
    high = frame["high"].astype(float)
    low = frame["low"].astype(float)
    volume = frame["volume"].astype(float)
    ema12 = close.ewm(span=12, adjust=False).mean()
    ema26 = close.ewm(span=26, adjust=False).mean()
    macd = ema12 - ema26
    macds = macd.ewm(span=9, adjust=False).mean()
    delta = close.diff()
    gain = delta.clip(lower=0).rolling(14).mean()
    loss = (-delta.clip(upper=0)).rolling(14).mean()
    rs = gain / loss.replace(0, float("nan"))
    rsi = 100 - (100 / (1 + rs))
    middle = close.rolling(20).mean()
    deviation = close.rolling(20).std()
    previous = close.shift(1)
    true_range = (high - low).to_frame("hl")
    true_range["hc"] = (high - previous).abs()
    true_range["lc"] = (low - previous).abs()
    atr = true_range.max(axis=1).rolling(14).mean()
    volume_sum = volume.rolling(20).sum()
    vwma = (close * volume).rolling(20).sum() / volume_sum.replace(0, float("nan"))

    series = {
        "close_50_sma": close.rolling(50).mean(),
        "close_200_sma": close.rolling(200).mean(),
        "close_10_ema": close.ewm(span=10, adjust=False).mean(),
        "rsi": rsi,
        "macd": macd,
        "macds": macds,
        "macdh": macd - macds,
        "boll": middle,
        "boll_ub": middle + 2 * deviation,
        "boll_lb": middle - 2 * deviation,
        "atr": atr,
        "vwma": vwma,
    }
    result: Dict[str, Any] = {}
    for name, values in series.items():
        value = values.iloc[-1]
        result[name] = round(float(value), 6) if not math.isnan(float(value)) else None
    return result


def _install_fixture_market_tools(frames: Mapping[str, Any]) -> None:
    try:
        from langchain_core.tools import tool
        import tradingagents.agents.analysts.market_analyst as market_analyst
        import tradingagents.agents.utils.agent_utils as agent_utils
        import tradingagents.graph.trading_graph as trading_graph
    except ImportError as exc:
        raise BackendUnavailable("TradingAgents market tool modules are unavailable") from exc

    def subset(symbol: str, start_date: str = "", end_date: str = "") -> Any:
        frame = frames.get(symbol)
        if frame is None:
            raise ValueError("No request fixture exists for symbol %s" % symbol)
        selected = frame
        if start_date:
            selected = selected[selected.index >= start_date]
        if end_date:
            selected = selected[selected.index <= end_date]
        return selected

    @tool("get_stock_data")
    def fixture_stock_data(symbol: str, start_date: str, end_date: str) -> str:
        """Return caller-supplied OHLCV fixture rows for one symbol and date range."""
        frame = subset(symbol, start_date, end_date)
        return frame.reset_index().to_csv(index=False)

    @tool("get_indicators")
    def fixture_indicators(
        symbol: str, indicator: str, curr_date: str, look_back_days: int = 30
    ) -> str:
        """Compute requested indicators deterministically from the OHLCV fixture."""
        del look_back_days
        frame = subset(symbol, "", curr_date)
        values = _indicator_values(frame)
        requested = [part.strip().lower() for part in indicator.split(",") if part.strip()]
        return json.dumps(
            {
                "symbol": symbol,
                "as_of": frame.index[-1].date().isoformat(),
                "source": "request-fixture",
                "indicators": {name: values.get(name) for name in requested},
            },
            ensure_ascii=False,
        )

    @tool("get_verified_market_snapshot")
    def fixture_snapshot(
        symbol: str, curr_date: str, look_back_days: int = 30
    ) -> str:
        """Return an exact OHLCV and indicator snapshot from the request fixture."""
        frame = subset(symbol, "", curr_date).tail(max(1, min(int(look_back_days), 90)))
        latest = frame.iloc[-1]
        return json.dumps(
            {
                "symbol": symbol,
                "as_of": frame.index[-1].date().isoformat(),
                "source": "request-fixture",
                "ohlcv": {name: float(latest[name]) for name in ("open", "high", "low", "close", "volume")},
                "indicators": _indicator_values(frame),
                "recent_closes": [float(value) for value in frame["close"].tail(10)],
            },
            ensure_ascii=False,
        )

    for module in (market_analyst, agent_utils, trading_graph):
        module.get_stock_data = fixture_stock_data
        module.get_indicators = fixture_indicators
        module.get_verified_market_snapshot = fixture_snapshot


def _failure_message(symbol: str, exc: Exception) -> str:
    detail = str(exc).replace("\r", " ").replace("\n", " ").strip()[:300]
    result = "TradingAgents failed for %s (%s)" % (symbol, type(exc).__name__)
    return result + (": " + detail if detail else "")


def _invoke(request: ValidatedRequest) -> Dict[str, Any]:
    ensure_cwd_importable()
    try:
        from tradingagents.default_config import DEFAULT_CONFIG
        from tradingagents.graph.trading_graph import TradingAgentsGraph
    except ImportError as exc:
        raise BackendUnavailable(
            "TradingAgents is not installed in this isolated Python runtime"
        ) from exc

    symbols = extract_symbols(request.input_data)
    as_of, date_defaulted = extract_date(
        request.input_data, ("as_of", "date", "end_date")
    )
    try:
        config = DEFAULT_CONFIG.copy()
    except AttributeError as exc:
        raise BackendUnavailable("TradingAgents DEFAULT_CONFIG is unavailable") from exc

    # Keep every upstream cache, report, and memory write inside this backend's
    # already-confined request directory.  Provider/model values still come
    # from the isolated child environment or upstream defaults.
    config["results_dir"] = str(request.run_dir / "results")
    config["data_cache_dir"] = str(request.run_dir / "cache")
    config["memory_log_path"] = str(request.run_dir / "memory" / "trading_memory.md")
    selected_analysts = _selected_analysts(request.input_data)
    market_data_source = str(
        request.input_data.get("market_data_source") or "upstream"
    ).strip().lower()
    fixture_rows = 0
    if market_data_source == "request-fixture":
        frames = _fixture_frames(request.input_data, symbols)
        fixture_rows = sum(len(frame) for frame in frames.values())
        _install_fixture_market_tools(frames)
    elif market_data_source != "upstream":
        raise RequestRejected("market_data_source must be upstream or request-fixture")

    graph = TradingAgentsGraph(
        selected_analysts=tuple(selected_analysts), debug=False, config=config
    )
    decisions: Dict[str, Any] = {}
    state_reports: Dict[str, Dict[str, str]] = {}
    warnings: List[str] = [
        "TradingAgents was used for research/paper analysis only; no order was submitted."
    ]
    external_provenance = (
        dict(request.market_data_provenance)
        if market_data_source == "request-fixture" and request.market_data_provenance
        else {}
    )
    if external_provenance:
        provenance_symbols = {
            str(item.get("symbol") or "")
            for item in external_provenance.get("instruments", [])
            if isinstance(item, Mapping)
        }
        if provenance_symbols != set(symbols):
            raise RequestRejected("external market-data provenance does not match symbols")
    if market_data_source == "request-fixture":
        if external_provenance:
            warnings.append(
                "TradingAgents used externally retrieved, forward-adjusted %s daily OHLCV "
                "through its request-fixture transport; this is historical research data, "
                "not a real-time quote."
                % external_provenance.get("provider", "market-data")
            )
        else:
            warnings.append(
                "TradingAgents ran against explicit synthetic/test OHLCV supplied by the caller; "
                "this is an engine integration result, not live-market evidence."
            )
    failures: List[str] = []
    for symbol in symbols:
        try:
            propagated = graph.propagate(symbol, as_of)
            if isinstance(propagated, tuple) and len(propagated) >= 2:
                state_reports[symbol] = _selected_state_reports(propagated[0])
                decision = propagated[1]
            else:
                decision = propagated
                state_reports[symbol] = {}
            decisions[symbol] = json_safe(decision)
        except Exception as exc:
            failures.append(_failure_message(symbol, exc))

    if date_defaulted:
        warnings.append("No as_of date was supplied; the current UTC date was used.")
    warnings.extend(failures)
    if not decisions:
        return make_response(
            "error",
            data={
                "provider": BACKEND,
                "upstream_api": "TradingAgentsGraph.propagate",
                "symbols": symbols,
                "as_of": as_of,
                "decisions": {},
                "selected_analysts": selected_analysts,
                "market_data_source": (
                    external_provenance.get("provider")
                    if external_provenance
                    else market_data_source
                ),
                "market_data_transport": market_data_source,
                "market_data_provenance": external_provenance,
                "fixture_price_rows": fixture_rows,
            },
            warnings=warnings,
        )

    output = "\n\n---\n\n".join(
        _trader_markdown(symbol, as_of, decision, state_reports.get(symbol, {}))
        for symbol, decision in decisions.items()
    )
    artifacts = [
        str(path)
        for path in sorted((request.run_dir / "results").rglob("*"))
        if path.is_file() and path.stat().st_size > 0
    ] if (request.run_dir / "results").is_dir() else []
    return make_response(
        "degraded"
        if failures or (market_data_source == "request-fixture" and not external_provenance)
        else "success",
        output=output,
        data={
            "provider": BACKEND,
            "upstream_api": "TradingAgentsGraph.propagate",
            "symbols": list(decisions),
            "as_of": as_of,
            "decisions": decisions,
            "reports": state_reports,
            "selected_analysts": selected_analysts,
            "market_data_source": (
                external_provenance.get("provider")
                if external_provenance
                else market_data_source
            ),
            "market_data_transport": market_data_source,
            "market_data_provenance": external_provenance,
            "fixture_price_rows": fixture_rows,
        },
        warnings=warnings,
        artifacts=artifacts,
    )


def handle_request(raw_request: Any) -> Dict[str, Any]:
    return dispatch(BACKEND, raw_request, _invoke)


if __name__ == "__main__":
    run_ndjson(BACKEND, handle_request)
