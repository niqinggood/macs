"""NDJSON adapter for virattt/ai-hedge-fund.

The upstream ``run_hedge_fund`` function calculates simulated decisions and
signals.  This shim constructs an in-memory paper portfolio and never submits
those decisions to a broker.
"""

from __future__ import annotations

from datetime import datetime, timedelta
import math
import re
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
        finite_number,
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
        finite_number,
        format_output,
        json_safe,
        make_response,
        run_ndjson,
    )


BACKEND = "ai-hedge-fund"
AI_HEDGE_FUND_ANALYST_KEYS = frozenset(
    {
        "aswath_damodaran",
        "ben_graham",
        "bill_ackman",
        "cathie_wood",
        "charlie_munger",
        "michael_burry",
        "mohnish_pabrai",
        "nassim_taleb",
        "peter_lynch",
        "phil_fisher",
        "rakesh_jhunjhunwala",
        "stanley_druckenmiller",
        "warren_buffett",
        "technical_analyst",
        "fundamentals_analyst",
        "growth_analyst",
        "news_sentiment_analyst",
        "sentiment_analyst",
        "valuation_analyst",
    }
)
AI_HEDGE_FUND_ANALYST_ALIASES = {
    "technical": "technical_analyst",
    "technicals": "technical_analyst",
    "fundamental": "fundamentals_analyst",
    "fundamentals": "fundamentals_analyst",
    "growth": "growth_analyst",
    "news_sentiment": "news_sentiment_analyst",
    "sentiment": "sentiment_analyst",
    "valuation": "valuation_analyst",
}

AI_HEDGE_FUND_FALLBACK_TEXT_RULES = (
    (re.compile(r"\bno\s+valid\s+trade\b", re.IGNORECASE), "no valid trade"),
    (re.compile(r"\bdefaulting\b", re.IGNORECASE), "defaulting"),
    (
        re.compile(r"\binsufficient\b[^.!?\n]{0,40}\bdata\b", re.IGNORECASE),
        "insufficient data",
    ),
    (
        re.compile(r"\bmissing\b[^.!?\n]{0,30}\bprice\b[^.!?\n]{0,30}\bdata\b", re.IGNORECASE),
        "missing price data",
    ),
    (
        re.compile(r"\berror\s+(?:in|during)\s+(?:the\s+)?analysis\b", re.IGNORECASE),
        "error in analysis",
    ),
    (
        re.compile(
            r"\berror\s+(?:(?:in|while|when)\s+)?generating\b", re.IGNORECASE
        ),
        "error generating",
    ),
    (
        re.compile(
            r"\b(?:parsing|parse)\s+error\b|\b(?:failed|unable)\s+to\s+parse\b",
            re.IGNORECASE,
        ),
        "parsing error",
    ),
    (re.compile(r"^\s*error\s*$", re.IGNORECASE), "error"),
)


def _number(value: Any) -> Any:
    if isinstance(value, bool):
        return None
    try:
        number = float(value)
    except (TypeError, ValueError, OverflowError):
        return None
    return number if math.isfinite(number) else None


def _fallback_text_markers(value: Any) -> List[str]:
    """Return bounded labels for upstream fallback/error text in nested output."""
    markers: List[str] = []

    def visit(item: Any) -> None:
        if isinstance(item, str):
            for pattern, label in AI_HEDGE_FUND_FALLBACK_TEXT_RULES:
                if pattern.search(item):
                    markers.append(label)
            return
        if isinstance(item, Mapping):
            for nested in item.values():
                visit(nested)
            return
        if isinstance(item, (list, tuple)):
            for nested in item:
                visit(nested)

    visit(value)
    return list(dict.fromkeys(markers))


def _decision_quality_issues(decisions: Any) -> List[str]:
    """Detect upstream no-op fallbacks that must never be reported as success."""
    candidates: List[Mapping[str, Any]] = []
    if isinstance(decisions, Mapping):
        if any(key in decisions for key in ("reasoning", "confidence", "quantity", "action")):
            candidates.append(decisions)
        else:
            candidates.extend(item for item in decisions.values() if isinstance(item, Mapping))
    elif isinstance(decisions, list):
        candidates.extend(item for item in decisions if isinstance(item, Mapping))

    issues = [
        "AI Hedge Fund returned fallback/error decision text: %s" % marker
        for marker in _fallback_text_markers(decisions)
    ]
    for decision in candidates:
        if "default decision" in str(decision.get("reasoning") or "").lower():
            issues.append("AI Hedge Fund returned a Default decision fallback")
        confidence = _number(decision.get("confidence"))
        if confidence is not None and confidence <= 0:
            issues.append("AI Hedge Fund returned a non-positive-confidence decision")
    return list(dict.fromkeys(issues))


def _selected_analyst_signal_quality_issues(
    selected_analysts: List[str],
    symbols: List[str],
    analyst_signals: Any,
) -> List[str]:
    """Require a non-fallback signal for every selected analyst and symbol."""
    signals = analyst_signals if isinstance(analyst_signals, Mapping) else {}
    expected_keys = ["%s_agent" % analyst.removesuffix("_agent") for analyst in selected_analysts]
    missing_keys = sorted(key for key in expected_keys if key not in signals)
    issues: List[str] = [
        "AI Hedge Fund analyst/risk signals contain fallback/error text: %s" % marker
        for marker in _fallback_text_markers(analyst_signals)
    ]
    if selected_analysts and not isinstance(analyst_signals, Mapping):
        issues.append("AI Hedge Fund returned invalid analyst-signals structure")
    if missing_keys:
        issues.append(
            "AI Hedge Fund omitted selected-analyst signals: %s"
            % ", ".join(missing_keys)
        )
    for analyst_key in expected_keys:
        raw_by_symbol = signals.get(analyst_key)
        if not isinstance(raw_by_symbol, Mapping):
            if analyst_key in signals:
                issues.append(
                    "AI Hedge Fund returned an invalid selected-analyst signal map for %s"
                    % analyst_key
                )
            continue
        for symbol in symbols:
            raw_signal = raw_by_symbol.get(symbol)
            if raw_signal in (None, "", {}, []):
                issues.append(
                    "AI Hedge Fund omitted selected-analyst signal for %s/%s"
                    % (analyst_key, symbol)
                )
                continue
            markers = _fallback_text_markers(raw_signal)
            if markers:
                issues.append(
                    "AI Hedge Fund returned fallback/error selected-analyst signal for "
                    "%s/%s: %s"
                    % (analyst_key, symbol, ", ".join(markers))
                )
            confidence = (
                _number(raw_signal.get("confidence"))
                if isinstance(raw_signal, Mapping)
                else None
            )
            if confidence is not None and confidence <= 0:
                issues.append(
                    "AI Hedge Fund returned a non-positive-confidence selected-analyst "
                    "signal for %s/%s" % (analyst_key, symbol)
                )
    return list(dict.fromkeys(issues))


def _compact_value(value: Any, limit: int = 600) -> str:
    if isinstance(value, str):
        text = value.strip()
    else:
        try:
            import json

            text = json.dumps(value, ensure_ascii=False, separators=(",", ":"))
        except (TypeError, ValueError):
            text = str(value)
    text = " ".join(text.split())
    return text if len(text) <= limit else text[:limit].rstrip() + "…"


def _markdown_cell(value: Any, limit: int = 180) -> str:
    return _compact_value(value, limit).replace("|", "\\|") or "-"


def _manager_markdown(
    symbols: List[str],
    start_date: str,
    end_date: str,
    decisions: Any,
    analyst_signals: Any,
    quality_issues: List[str],
) -> str:
    parts = [
        "# AI投研经理结论",
        "",
        "- 标的：%s" % ("、".join(symbols) or "未识别"),
        "- 研究区间：%s 至 %s" % (start_date, end_date),
        "- 决策性质：模拟组合研究，不执行真实下单。",
    ]
    if quality_issues:
        parts.extend(
            [
                "- 验收状态：**降级，不计作有效投研结论**",
                "",
                "## 降级原因",
                "",
                *["- %s" % issue for issue in quality_issues],
            ]
        )

    if isinstance(decisions, Mapping) and decisions:
        parts.extend(
            [
                "",
                "## 组合裁决",
                "",
                "| 标的 | 动作 | 置信度 | 模拟数量 |",
                "|---|---|---:|---:|",
            ]
        )
        for symbol, raw in decisions.items():
            decision = raw if isinstance(raw, Mapping) else {"action": raw}
            parts.append(
                "| %s | %s | %s | %s |"
                % (
                    _markdown_cell(symbol),
                    _markdown_cell(decision.get("action", decision.get("signal", "-"))),
                    _markdown_cell(decision.get("confidence", "-")),
                    _markdown_cell(decision.get("quantity", "-")),
                )
            )
            reasoning = decision.get("reasoning")
            if reasoning not in (None, "", {}, []):
                parts.append("\n- **%s 裁决依据**：%s" % (symbol, _compact_value(reasoning)))

    if isinstance(analyst_signals, Mapping) and analyst_signals:
        parts.extend(
            [
                "",
                "## 研究团队信号",
                "",
                "| 分析模块 | 标的 | 信号/状态 | 置信度 |",
                "|---|---|---|---:|",
            ]
        )
        for analyst, raw_by_symbol in analyst_signals.items():
            if not isinstance(raw_by_symbol, Mapping):
                continue
            for symbol, raw_signal in raw_by_symbol.items():
                signal = raw_signal if isinstance(raw_signal, Mapping) else {"signal": raw_signal}
                parts.append(
                    "| %s | %s | %s | %s |"
                    % (
                        _markdown_cell(str(analyst).removesuffix("_agent")),
                        _markdown_cell(symbol),
                        _markdown_cell(signal.get("signal", signal.get("risk", "已返回"))),
                        _markdown_cell(signal.get("confidence", "-")),
                    )
                )
                reasoning = signal.get("reasoning")
                if reasoning not in (None, "", {}, []):
                    parts.append(
                        "\n- **%s / %s 依据**：%s"
                        % (str(analyst).removesuffix("_agent"), symbol, _compact_value(reasoning))
                    )

    parts.extend(
        [
            "",
            "## 使用边界",
            "",
            "该结果用于证据汇总与模拟仓位讨论；默认兜底、零/非正置信度或缺失研究模块均不会被包装成成功。",
        ]
    )
    return "\n".join(parts)


def _patch_price_getter(get_prices: Any) -> None:
    import src.tools.api as api_tools
    import src.agents.risk_manager as risk_manager
    import src.agents.technicals as technicals

    api_tools.get_prices = get_prices
    risk_manager.get_prices = get_prices
    technicals.get_prices = get_prices


def _install_yfinance_price_source() -> None:
    """Use keyless yfinance prices while keeping AI Hedge Fund's graph intact.

    The upstream project currently hard-codes Financial Datasets for prices.
    This narrowly scoped adapter is opt-in and replaces only that read path;
    analyst, risk, and portfolio decisions still run through upstream
    ``src.main.run_hedge_fund``.  No brokerage or write API is introduced.
    """

    try:
        import yfinance as yf
        from src.data.models import Price
    except ImportError as exc:
        raise BackendUnavailable(
            "AI Hedge Fund yfinance price adapter is not installed in this isolated runtime"
        ) from exc

    cache: Dict[tuple[str, str, str], List[Any]] = {}

    def get_prices(ticker: str, start_date: str, end_date: str, api_key: str = None) -> List[Any]:
        del api_key
        key = (ticker, start_date, end_date)
        if key in cache:
            return list(cache[key])
        exclusive_end = (
            datetime.strptime(end_date, "%Y-%m-%d").date() + timedelta(days=1)
        ).isoformat()
        frame = yf.Ticker(ticker).history(
            start=start_date,
            end=exclusive_end,
            interval="1d",
            auto_adjust=False,
            actions=False,
        )
        prices: List[Any] = []
        for timestamp, row in frame.iterrows():
            values = [row.get("Open"), row.get("Close"), row.get("High"), row.get("Low")]
            try:
                numeric = [float(value) for value in values]
                volume = int(row.get("Volume", 0))
            except (TypeError, ValueError, OverflowError):
                continue
            if not all(math.isfinite(value) for value in numeric):
                continue
            prices.append(
                Price(
                    open=numeric[0],
                    close=numeric[1],
                    high=numeric[2],
                    low=numeric[3],
                    volume=max(0, volume),
                    time=timestamp.date().isoformat(),
                )
            )
        cache[key] = prices
        return list(prices)

    _patch_price_getter(get_prices)


def _install_request_fixture_price_source(
    input_data: Mapping[str, Any], symbols: List[str]
) -> int:
    """Install an explicit caller-supplied OHLCV fixture for engine testing."""

    try:
        from src.data.models import Price
    except ImportError as exc:
        raise BackendUnavailable("AI Hedge Fund Price model is unavailable") from exc

    raw = input_data.get("price_history")
    if isinstance(raw, list) and len(symbols) == 1:
        raw_by_symbol: Mapping[str, Any] = {symbols[0]: raw}
    elif isinstance(raw, Mapping):
        raw_by_symbol = raw
    else:
        raise RequestRejected(
            "request-fixture price_source requires price_history as an object by symbol "
            "(or an array for one symbol)"
        )

    prices_by_symbol: Dict[str, List[Any]] = {}
    total_rows = 0
    for symbol in symbols:
        rows = raw_by_symbol.get(symbol)
        if not isinstance(rows, list) or not (20 <= len(rows) <= 500):
            raise RequestRejected(
                "price_history for %s must contain between 20 and 500 rows" % symbol
            )
        prices: List[Any] = []
        for row in rows:
            if not isinstance(row, Mapping):
                raise RequestRejected("every price_history row must be an object")
            date_value = row.get("time", row.get("date"))
            if not isinstance(date_value, str):
                raise RequestRejected("every price_history row needs an ISO date/time")
            try:
                date_value = datetime.fromisoformat(
                    date_value.strip().replace("Z", "+00:00")
                ).date().isoformat()
                values = [
                    float(row.get("open")),
                    float(row.get("close")),
                    float(row.get("high")),
                    float(row.get("low")),
                ]
                volume = int(row.get("volume", 0))
            except (TypeError, ValueError, OverflowError) as exc:
                raise RequestRejected("price_history contains an invalid OHLCV row") from exc
            if not all(math.isfinite(value) and value > 0 for value in values):
                raise RequestRejected("price_history OHLC values must be finite and positive")
            prices.append(
                Price(
                    open=values[0],
                    close=values[1],
                    high=values[2],
                    low=values[3],
                    volume=max(0, volume),
                    time=date_value,
                )
            )
        prices.sort(key=lambda item: item.time)
        prices_by_symbol[symbol] = prices
        total_rows += len(prices)

    def get_prices(ticker: str, start_date: str, end_date: str, api_key: str = None) -> List[Any]:
        del api_key
        return [
            item
            for item in prices_by_symbol.get(ticker, [])
            if start_date <= item.time <= end_date
        ]

    _patch_price_getter(get_prices)
    return total_rows


def _string_option(input_data: Mapping[str, Any], key: str, default: str) -> str:
    value = input_data.get(key, default)
    if not isinstance(value, str) or not value.strip() or len(value.strip()) > 128:
        raise RequestRejected("%s must be a non-empty string of at most 128 characters" % key)
    return value.strip()


def _analysts(input_data: Mapping[str, Any]) -> List[str]:
    raw = input_data.get(
        "ai_hedge_fund_analysts",
        input_data.get("selected_analysts", input_data.get("analysts", [])),
    )
    if raw is None:
        return []
    if not isinstance(raw, list) or len(raw) > 64:
        raise RequestRejected("selected_analysts must be an array with at most 64 entries")
    result: List[str] = []
    for item in raw:
        if not isinstance(item, str) or not item.strip() or len(item.strip()) > 128:
            raise RequestRejected("selected_analysts entries must be short strings")
        normalized = item.strip().lower().replace("-", "_").replace(" ", "_")
        normalized = AI_HEDGE_FUND_ANALYST_ALIASES.get(normalized, normalized)
        if normalized not in AI_HEDGE_FUND_ANALYST_KEYS:
            raise RequestRejected(
                "unknown AI Hedge Fund analyst %r; use an upstream analyst key or a "
                "supported shorthand such as technical, fundamentals, sentiment, or valuation"
                % item.strip()
            )
        if normalized not in result:
            result.append(normalized)
    return result


def _blank_portfolio(
    symbols: List[str], initial_cash: float, margin_requirement: float
) -> Dict[str, Any]:
    return {
        "cash": initial_cash,
        "margin_requirement": margin_requirement,
        "margin_used": 0.0,
        "positions": {
            symbol: {
                "long": 0,
                "short": 0,
                "long_cost_basis": 0.0,
                "short_cost_basis": 0.0,
                "short_margin_used": 0.0,
            }
            for symbol in symbols
        },
        "realized_gains": {
            symbol: {"long": 0.0, "short": 0.0} for symbol in symbols
        },
    }


def _invoke(request: ValidatedRequest) -> Dict[str, Any]:
    ensure_cwd_importable()
    try:
        from src.main import run_hedge_fund
    except ImportError as exc:
        raise BackendUnavailable(
            "AI Hedge Fund is not installed in this isolated Python runtime"
        ) from exc

    symbols = extract_symbols(request.input_data)
    price_source = _string_option(
        request.input_data, "price_source", "financial-datasets"
    ).strip().lower()
    if price_source not in {"financial-datasets", "yfinance", "request-fixture"}:
        raise RequestRejected(
            "price_source must be financial-datasets, yfinance, or request-fixture"
        )
    fixture_rows = 0
    if price_source == "yfinance":
        _install_yfinance_price_source()
    elif price_source == "request-fixture":
        fixture_rows = _install_request_fixture_price_source(request.input_data, symbols)
    external_provenance = (
        dict(request.market_data_provenance)
        if price_source == "request-fixture" and request.market_data_provenance
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
    end_date, end_defaulted = extract_date(
        request.input_data, ("end_date", "as_of", "date")
    )
    if request.input_data.get("start_date") not in (None, ""):
        start_date, _ = extract_date(
            request.input_data, ("start_date",), default_today=False
        )
    else:
        start_date = (
            datetime.strptime(end_date, "%Y-%m-%d").date() - timedelta(days=90)
        ).isoformat()
    if start_date > end_date:
        raise RequestRejected("start_date must not be after end_date")

    initial_cash = finite_number(
        request.input_data.get("initial_cash", request.input_data.get("initial_capital")),
        "initial_cash",
        100000.0,
        0.01,
        1e15,
    )
    margin_requirement = finite_number(
        request.input_data.get("margin_requirement"),
        "margin_requirement",
        0.0,
        0.0,
        1.0,
    )
    show_reasoning = request.input_data.get("show_reasoning", False)
    if not isinstance(show_reasoning, bool):
        raise RequestRejected("show_reasoning must be boolean")
    selected_analysts = _analysts(request.input_data)
    model_name = _string_option(request.input_data, "model_name", "gpt-4.1")
    model_provider = _string_option(request.input_data, "model_provider", "OpenAI")
    portfolio = _blank_portfolio(symbols, initial_cash, margin_requirement)

    raw_result = run_hedge_fund(
        tickers=symbols,
        start_date=start_date,
        end_date=end_date,
        portfolio=portfolio,
        show_reasoning=show_reasoning,
        selected_analysts=selected_analysts,
        model_name=model_name,
        model_provider=model_provider,
    )
    safe_result = json_safe(raw_result)
    if isinstance(safe_result, dict):
        decisions = safe_result.get("decisions")
        analyst_signals = safe_result.get("analyst_signals")
    else:
        decisions = safe_result
        analyst_signals = None
    warnings = [
        "AI Hedge Fund ran against an in-memory paper portfolio; no order was submitted."
    ]
    if price_source == "yfinance":
        warnings.append(
            "The OpenKitty adapter supplied keyless yfinance prices because the upstream "
            "Financial Datasets credential was not selected."
        )
    elif price_source == "request-fixture":
        if external_provenance:
            warnings.append(
                "AI Hedge Fund used externally retrieved, forward-adjusted %s daily OHLCV "
                "through its request-fixture transport; this is historical research data, "
                "not a real-time quote."
                % external_provenance.get("provider", "market-data")
            )
        else:
            warnings.append(
                "AI Hedge Fund ran against explicit synthetic/test OHLCV supplied by the caller; "
                "this is an engine integration result, not live-market evidence."
            )
    if end_defaulted:
        warnings.append("No end/as_of date was supplied; the current UTC date was used.")
    status = "success"
    if decisions in (None, {}, []):
        status = "degraded"
        warnings.append("AI Hedge Fund returned no parsed decisions.")
    if price_source == "request-fixture" and not external_provenance:
        status = "degraded"
    signal_quality_issues = _selected_analyst_signal_quality_issues(
        selected_analysts, symbols, analyst_signals
    )
    decision_output = format_output(decisions if decisions is not None else safe_result)
    quality_issues = _decision_quality_issues(decisions)
    quality_issues.extend(signal_quality_issues)
    quality_issues.extend(
        "AI Hedge Fund upstream result contains fallback/error text: %s" % marker
        for marker in _fallback_text_markers(safe_result)
    )
    if not isinstance(decisions, (Mapping, list)) or not decisions:
        quality_issues.append("AI Hedge Fund returned no structured investment decision")
    if "default decision" in decision_output.lower():
        quality_issues.append("AI Hedge Fund output contains a Default decision fallback")
    quality_issues = list(dict.fromkeys(quality_issues))
    if quality_issues:
        status = "degraded"
        warnings.extend("%s; output is incomplete and is not counted as success." % issue for issue in quality_issues)
    manager_output = _manager_markdown(
        symbols,
        start_date,
        end_date,
        decisions,
        analyst_signals,
        quality_issues,
    )
    return make_response(
        status,
        output=manager_output,
        data={
            "provider": BACKEND,
            "upstream_api": "src.main.run_hedge_fund",
            "market_data_source": (
                external_provenance.get("provider")
                if external_provenance
                else price_source
            ),
            "market_data_transport": price_source,
            "market_data_provenance": external_provenance,
            "fixture_price_rows": fixture_rows,
            "symbols": symbols,
            "start_date": start_date,
            "end_date": end_date,
            "paper_portfolio": {
                "initial_cash": initial_cash,
                "margin_requirement": margin_requirement,
            },
            "decisions": decisions,
            "analyst_signals": analyst_signals,
            "quality_gate": {
                "passed": not quality_issues,
                "issues": quality_issues,
                "success_requires_non_fallback_decision": True,
            },
        },
        warnings=warnings,
    )


def handle_request(raw_request: Any) -> Dict[str, Any]:
    return dispatch(BACKEND, raw_request, _invoke)


if __name__ == "__main__":
    run_ndjson(BACKEND, handle_request)
