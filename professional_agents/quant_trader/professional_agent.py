"""OpenKitty isolated quantitative professional agent."""

from __future__ import annotations

import copy
from datetime import datetime, timezone
import json
import os
from pathlib import Path
import re
from typing import Any, Dict, List, Mapping, Optional, Sequence, Tuple
import uuid

from adapters import BACKEND_NAMES, ExternalBackendAdapter, build_backend_request
from china_market import (
    ChinaSecurityResolver,
    EastmoneyDailyHistory,
    MarketDataError,
    SecurityResolutionError,
    resolve_input_securities,
)


PROTOCOL = "openkitty.professional-agent/v1"
AGENT_ID = "quant-trader"
EMPLOYEE_PROFILES: Dict[str, Dict[str, Any]] = {
    # ``quant-trader`` is intentionally retained as the compatibility identity
    # for the former combined employee.  Its contract is now the narrower
    # AI交易员 role and it can invoke TradingAgents only.
    "quant-trader": {
        "name": "AI交易员",
        "at_name": "交易员",
        "role_capability": "trading",
        "backend": "tradingagents",
        "scope": "market structure, trading thesis, timing and paper-trade risk",
    },
    "investment-research-manager": {
        "name": "AI投研经理",
        "at_name": "投研经理",
        "role_capability": "research-management",
        "backend": "ai-hedge-fund",
        "scope": "multi-discipline investment research, conflict resolution and paper-position advice",
    },
    "financial-intelligence-analyst": {
        "name": "AI金信分析师",
        "at_name": "金信分析师",
        "role_capability": "financial-intelligence",
        "backend": "fingpt",
        "scope": "financial text, announcements, news, research reports and sentiment",
    },
}
ALLOWED_EMPLOYEES = frozenset(EMPLOYEE_PROFILES)
CAPABILITIES = frozenset(
    {"analyze", "health", "trading", "research-management", "financial-intelligence"}
)
ALLOWED_MODES = frozenset({"research", "paper"})
UNSAFE_MODE_VALUES = frozenset(
    {
        "live",
        "live-trading",
        "live_trading",
        "execute",
        "execution",
        "real",
        "real-money",
        "real_money",
        "broker",
        "submit-order",
        "submit_order",
    }
)
UNSAFE_BOOLEAN_KEYS = frozenset(
    {
        "allow_live",
        "allow_live_trading",
        "live",
        "live_trading",
        "execute",
        "execute_orders",
        "order_submission",
        "place_order",
        "submit_order",
    }
)
SAFE_RUN_COMPONENT = re.compile(r"[^A-Za-z0-9_.-]+")
PLAIN_DATE_PATTERN = re.compile(r"(?<!\d)(20\d{2}-\d{2}-\d{2})(?!\d)")
MARKED_SYMBOL_PATTERN = re.compile(
    r"(?:股票|标的|证券代码|代码|ticker|symbol|分析|研究|看看)"
    r"(?:\s*(?:为|是|[:：]))?\s*"
    r"(\$?[A-Za-z]{1,10}(?:[.-][A-Za-z0-9]{1,8})?|\d{5,6}(?:\.(?:SS|SH|SSE|XSHG|SZ|SZSE|XSHE|BJ|BSE|HK))?)",
    re.IGNORECASE,
)
GENERIC_SYMBOL_PATTERN = re.compile(
    r"(?<![A-Za-z0-9])"
    r"(\$?[A-Za-z]{1,10}(?:[.-][A-Za-z0-9]{1,8})?|\d{5,6}(?:\.(?:SS|SH|SSE|XSHG|SZ|SZSE|XSHE|BJ|BSE|HK))?)"
    r"(?![A-Za-z0-9])"
)
SYMBOL_STOPWORDS = frozenset(
    {
        "AI",
        "API",
        "FUND",
        "GPT",
        "HEDGE",
        "HTTP",
        "HTTPS",
        "JSON",
        "LLM",
        "OPENKITTY",
        "TRADINGAGENTS",
        "FINGPT",
    }
)
UNSAFE_NATURAL_PATTERNS = (
    re.compile(
        r"实盘(?:交易)?|真实资金|真实(?:买入|卖出|建仓|平仓|下单|交易)|"
        r"提交订单|执行交易|代我下单|替我下单|帮我下单"
    ),
    re.compile(r"(?:立即|马上|现在|替我|帮我|给我).{0,16}(?:买入|卖出|建仓|平仓|下单)"),
    re.compile(
        r"\b(?:live[ -]?trad(?:e|ing)|place\s+(?:an\s+)?order|submit\s+(?:an\s+)?order|"
        r"execute\s+(?:the\s+)?(?:trades?|orders?)|buy\s+.+\s+now|sell\s+.+\s+now)\b",
        re.IGNORECASE,
    ),
)
CHINESE_NEGATION_PREFIX = re.compile(
    r"(?:不要|不用|无需|无须|禁止|严禁|切勿|不能|不可|不准|不应|不允许|"
    r"拒绝|避免|别|勿|绝不|永不|不是|非|不)\s*"
    r"(?:(?:再|去|做|进行|执行|使用|用|提交|开启|启动|直接|实际|真的|"
    r"替我|帮我|给我|任何(?:形式)?的?|一切|所有)\s*){0,3}$"
)
CHINESE_NEGATION_WITHIN_MATCH = re.compile(
    r"(?:不要|不用|禁止|严禁|切勿|不能|不可|不准|不应|不允许|别|勿|绝不|永不|不)\s*"
    r"(?:(?:再|去|进行|执行|直接|实际|真的)\s*){0,2}"
    r"(?:买入|卖出|建仓|平仓|下单)"
)
CHINESE_NEGATION_SUFFIX = re.compile(
    r"^\s*(?:是|属于)?\s*(?:不允许(?:的)?|被禁止|已禁用|禁止(?:的)?|严禁|"
    r"不可(?:执行)?|不能(?:执行)?|不会执行|不执行|不提交|不要做|不用做)"
)
ENGLISH_NEGATION_PREFIX = re.compile(
    r"(?:\b(?:do|does|did|must|should|can|could|will|would)\s+not|"
    r"\b(?:don't|doesn't|didn't|mustn't|shouldn't|can't|cannot|couldn't|won't|wouldn't)|"
    r"\b(?:never|no|without)|\b(?:avoid(?:ing)?|refrain\s+from|refuse\s+to|decline\s+to)|"
    r"\bnot\s+(?:allowed|authorized|permitted)\s+to)"
    r"(?:\s+(?:ever|actually|directly|really|to|using|doing|engaging\s+in|attempting\s+to|"
    r"want(?:ing)?\s+(?:you\s+)?to|ask(?:ing)?\s+(?:you\s+)?to))*\s*$",
    re.IGNORECASE,
)
ENGLISH_NEGATION_SUFFIX = re.compile(
    r"^\s*(?:(?:is|are|must\s+be|should\s+be|has\s+been|have\s+been)\s+)?"
    r"(?:disabled|forbidden|prohibited|blocked|off|not\s+(?:allowed|authorized|permitted))\b",
    re.IGNORECASE,
)
PAPER_NATURAL_PATTERN = re.compile(r"模拟盘|模拟交易|纸面交易|paper(?:[ -]?trad(?:e|ing))?", re.IGNORECASE)

AI_HEDGE_FUND_MAINLAND_DEFAULT_ANALYSTS = (
    "technical_analyst",
    "fundamentals_analyst",
    "valuation_analyst",
    "news_sentiment_analyst",
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
FINANCIAL_TEXT_MIN_CHARACTERS = 40
FINANCIAL_TEXT_TASK_PREFIX = re.compile(
    r"^\s*(?:@(?:AI)?金信分析师\s*)?(?:(?:请|帮我|麻烦|请你)\s*)?"
    r"(?:分析|解读|研判|总结|看看|评估)(?:一下|下)?(?:以下|这段|关于)?[^：:\n]{0,40}[：:]\s*"
)


def _public_profile(employee_id: str) -> Dict[str, Any]:
    profile = EMPLOYEE_PROFILES[employee_id]
    return {
        "employee_id": employee_id,
        "name": profile["name"],
        "at_name": profile["at_name"],
        "capability": profile["role_capability"],
        "backend": profile["backend"],
        "scope": profile["scope"],
    }


def _numeric(value: Any) -> Optional[float]:
    if isinstance(value, bool):
        return None
    try:
        number = float(value)
    except (TypeError, ValueError, OverflowError):
        return None
    return number if number == number and number not in {float("inf"), float("-inf")} else None


def _hedge_fallback_text_markers(value: Any) -> List[str]:
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


def _hedge_decision_quality_issues(decisions: Any) -> List[str]:
    """Identify upstream fallback decisions that are not investment research."""
    issues = [
        "upstream returned fallback/error decision text: %s" % marker
        for marker in _hedge_fallback_text_markers(decisions)
    ]
    candidates: List[Mapping[str, Any]] = []
    if isinstance(decisions, Mapping):
        if any(key in decisions for key in ("reasoning", "confidence", "quantity", "action")):
            candidates.append(decisions)
        else:
            candidates.extend(item for item in decisions.values() if isinstance(item, Mapping))
    elif isinstance(decisions, list):
        candidates.extend(item for item in decisions if isinstance(item, Mapping))
    for decision in candidates:
        reasoning = str(decision.get("reasoning") or "")
        if "default decision" in reasoning.lower():
            issues.append("upstream returned a Default decision fallback")
        confidence = _numeric(decision.get("confidence"))
        if confidence is not None and confidence <= 0:
            issues.append("upstream returned a non-positive-confidence decision")
    return list(dict.fromkeys(issues))


def _hedge_selected_signal_quality_issues(
    selected_analysts: Sequence[Any],
    symbols: Sequence[str],
    analyst_signals: Any,
) -> List[str]:
    """Require a non-fallback signal for every selected analyst and symbol."""
    signals = analyst_signals if isinstance(analyst_signals, Mapping) else {}
    expected_keys = [
        "%s_agent" % _hedge_analyst_key(item).removesuffix("_agent")
        for item in selected_analysts
    ]
    missing_keys = sorted(key for key in expected_keys if key not in signals)
    issues: List[str] = []
    if missing_keys:
        issues.append(
            "upstream omitted selected-analyst signals: %s" % ", ".join(missing_keys)
        )
    for analyst_key in expected_keys:
        raw_by_symbol = signals.get(analyst_key)
        if not isinstance(raw_by_symbol, Mapping):
            if analyst_key in signals:
                issues.append(
                    "upstream returned an invalid selected-analyst signal map for %s"
                    % analyst_key
                )
            continue
        for symbol in symbols:
            raw_signal = raw_by_symbol.get(symbol)
            if raw_signal in (None, "", {}, []):
                issues.append(
                    "upstream omitted selected-analyst signal for %s/%s"
                    % (analyst_key, symbol)
                )
                continue
            markers = _hedge_fallback_text_markers(raw_signal)
            if markers:
                issues.append(
                    "upstream returned fallback/error selected-analyst signal for "
                    "%s/%s: %s"
                    % (analyst_key, symbol, ", ".join(markers))
                )
            confidence = (
                _numeric(raw_signal.get("confidence"))
                if isinstance(raw_signal, Mapping)
                else None
            )
            if confidence is not None and confidence <= 0:
                issues.append(
                    "upstream returned a non-positive-confidence selected-analyst "
                    "signal for %s/%s" % (analyst_key, symbol)
                )
    return list(dict.fromkeys(issues))


def _financial_text(input_data: Mapping[str, Any]) -> str:
    for key in ("text", "news"):
        value = input_data.get(key)
        if isinstance(value, str) and value.strip():
            return value.strip()
    question = input_data.get("question")
    if not isinstance(question, str):
        return ""
    return FINANCIAL_TEXT_TASK_PREFIX.sub("", question.strip(), count=1).strip()


def _hedge_analyst_key(value: Any) -> str:
    normalized = str(value).strip().lower().replace("-", "_").replace(" ", "_")
    return AI_HEDGE_FUND_ANALYST_ALIASES.get(normalized, normalized)


def _response(
    request_id: str,
    status: str,
    output: str = "",
    data: Optional[Mapping[str, Any]] = None,
    warnings: Optional[Sequence[str]] = None,
    artifacts: Optional[Sequence[Mapping[str, Any]]] = None,
    error: str = "",
) -> Dict[str, Any]:
    return {
        "protocol": PROTOCOL,
        "request_id": request_id,
        "agent_id": AGENT_ID,
        "status": status,
        "output": output,
        "data": dict(data or {}),
        "warnings": list(warnings or []),
        "artifacts": list(artifacts or []),
        "error": error,
    }


def _contains_unsafe_execution_flag(value: Any, key: str = "") -> bool:
    normalized_key = key.strip().lower().replace("-", "_")
    if normalized_key in UNSAFE_BOOLEAN_KEYS and bool(value):
        return True
    if normalized_key in {"mode", "execution_mode", "operation", "action"}:
        normalized_value = str(value).strip().lower().replace(" ", "-")
        if normalized_value in UNSAFE_MODE_VALUES:
            return True
    if isinstance(value, dict):
        return any(_contains_unsafe_execution_flag(item, str(item_key)) for item_key, item in value.items())
    if isinstance(value, list):
        return any(_contains_unsafe_execution_flag(item) for item in value)
    return False


def _unsafe_match_is_clearly_negated(text: str, match: re.Match[str]) -> bool:
    """Return true only when a local, explicit negation governs this unsafe match."""
    before = text[max(0, match.start() - 80) : match.start()]
    after = text[match.end() : match.end() + 48]
    matched = match.group(0)
    return bool(
        CHINESE_NEGATION_PREFIX.search(before)
        or CHINESE_NEGATION_WITHIN_MATCH.search(matched)
        or CHINESE_NEGATION_SUFFIX.search(after)
        or ENGLISH_NEGATION_PREFIX.search(before)
        or ENGLISH_NEGATION_SUFFIX.search(after)
    )


def _contains_unsafe_natural_language(value: Any) -> bool:
    if isinstance(value, str):
        return any(
            not _unsafe_match_is_clearly_negated(value, match)
            for pattern in UNSAFE_NATURAL_PATTERNS
            for match in pattern.finditer(value)
        )
    if isinstance(value, dict):
        return any(
            _contains_unsafe_natural_language(item)
            for item_key, item in value.items()
            if str(item_key).strip().lower() in {"question", "prompt", "task", "instruction"}
        )
    return False


def _plain_language_frameworks(text: str) -> List[str]:
    lowered = text.lower().replace("_", "-")
    result: List[str] = []
    if "tradingagents" in lowered or "trading agents" in lowered:
        result.append("tradingagents")
    if any(alias in lowered for alias in ("ai hedge fund", "ai-hedge-fund", "aihedgefund")):
        result.append("ai-hedge-fund")
    if "fingpt" in lowered:
        result.append("fingpt")
    return result


def _normalize_symbol(raw: str) -> str:
    return raw.strip().lstrip("$").upper()


def _plain_language_symbols(text: str) -> List[str]:
    symbols: List[str] = []

    def add(raw: str, explicitly_marked: bool = False) -> None:
        symbol = _normalize_symbol(raw)
        if not symbol or symbol in symbols:
            return
        has_digit = any(ch.isdigit() for ch in symbol)
        has_alpha = any(ch.isalpha() for ch in symbol)
        was_dollar_prefixed = raw.strip().startswith("$")
        was_uppercase = raw.lstrip("$") == raw.lstrip("$").upper()
        if not explicitly_marked and not (was_dollar_prefixed or was_uppercase or has_digit):
            return
        if not explicitly_marked and symbol in SYMBOL_STOPWORDS:
            return
        if not (has_alpha or has_digit):
            return
        symbols.append(symbol)

    for match in MARKED_SYMBOL_PATTERN.finditer(text):
        add(match.group(1), explicitly_marked=True)
    for match in GENERIC_SYMBOL_PATTERN.finditer(text):
        add(match.group(1))
    return symbols


def _decode_plain_language_input(text: str) -> Dict[str, Any]:
    decoded: Dict[str, Any] = {
        "mode": "paper" if PAPER_NATURAL_PATTERN.search(text) else "research",
        "question": text,
    }
    symbols = _plain_language_symbols(text)
    if symbols:
        decoded["symbols"] = symbols
    date_match = PLAIN_DATE_PATTERN.search(text)
    if date_match:
        decoded["as_of"] = date_match.group(1)
    frameworks = _plain_language_frameworks(text)
    if frameworks:
        decoded["frameworks"] = frameworks
    return decoded


def _is_within(path: Path, parent: Path) -> bool:
    try:
        path.relative_to(parent)
        return True
    except ValueError:
        return False


class QuantProfessionalAgent:
    def __init__(
        self,
        environ: Optional[Mapping[str, str]] = None,
        adapters: Optional[Mapping[str, ExternalBackendAdapter]] = None,
        security_resolver: Optional[ChinaSecurityResolver] = None,
        market_history: Optional[EastmoneyDailyHistory] = None,
    ) -> None:
        self.environ = dict(os.environ if environ is None else environ)
        configured_root = self.environ.get("OPENKITTY_AGENT_RUN_DIR", "").strip()
        if not configured_root:
            raise ValueError("OPENKITTY_AGENT_RUN_DIR is required")
        try:
            self.run_root = Path(configured_root).expanduser().resolve(strict=True)
        except OSError as exc:
            raise ValueError("OPENKITTY_AGENT_RUN_DIR does not exist or is not readable") from exc
        if not self.run_root.is_dir():
            raise ValueError("OPENKITTY_AGENT_RUN_DIR must be an existing directory")
        self.adapters = dict(
            adapters
            or {
                name: ExternalBackendAdapter(name, self.environ)
                for name in BACKEND_NAMES
            }
        )
        try:
            china_timeout = int(
                self.environ.get("QUANT_TRADER_CHINA_MARKET_TIMEOUT_SECONDS", "5")
            )
        except (TypeError, ValueError):
            china_timeout = 5
        try:
            china_max_bytes = int(
                self.environ.get("QUANT_TRADER_CHINA_MARKET_MAX_BYTES", str(2 * 1024 * 1024))
            )
        except (TypeError, ValueError):
            china_max_bytes = 2 * 1024 * 1024
        self.security_resolver = security_resolver or ChinaSecurityResolver(
            timeout_seconds=china_timeout,
            max_response_bytes=china_max_bytes,
        )
        self.market_history = market_history or EastmoneyDailyHistory(
            timeout_seconds=china_timeout,
            max_response_bytes=china_max_bytes,
        )
        china_market_mode = str(
            self.environ.get("QUANT_TRADER_CHINA_MARKET_DATA", "enabled")
        ).strip().lower()
        self.china_market_enabled = china_market_mode not in {
            "0",
            "false",
            "off",
            "disabled",
        }

    def handle(self, request: Any) -> Dict[str, Any]:
        if not isinstance(request, dict):
            return _response("", "failed", error="Request must be a JSON object.")
        request_id = str(request.get("request_id") or "").strip()
        if not request_id:
            return _response("", "failed", error="request_id is required.")
        if request.get("protocol") != PROTOCOL:
            return _response(request_id, "failed", error="Unsupported or missing protocol.")
        if str(request.get("agent_id") or "").strip() != AGENT_ID:
            return _response(request_id, "failed", error="agent_id must be 'quant-trader'.")
        employee_id = str(request.get("employee_id") or "").strip()
        if employee_id not in ALLOWED_EMPLOYEES:
            return _response(
                request_id,
                "failed",
                error=(
                    "This professional agent is available only to: %s."
                    % ", ".join(sorted(ALLOWED_EMPLOYEES))
                ),
            )
        capability = str(request.get("capability") or "").strip().lower()
        if capability not in CAPABILITIES:
            return _response(request_id, "failed", error="Unsupported capability: %s" % capability)
        expected_capability = str(EMPLOYEE_PROFILES[employee_id]["role_capability"])
        compatible_capabilities = {expected_capability}
        if employee_id == "quant-trader":
            # Legacy quant-trader pipelines used the generic analyze capability.
            compatible_capabilities.add("analyze")
        if capability != "health" and capability not in compatible_capabilities:
            return _response(
                request_id,
                "failed",
                data={"employee_profile": _public_profile(employee_id)},
                error=(
                    "Employee '%s' requires capability '%s'."
                    % (employee_id, expected_capability)
                ),
            )

        run_dir, run_error = self._create_request_run_dir(request_id)
        if run_error:
            return _response(request_id, "failed", error=run_error)
        assert run_dir is not None

        if capability == "health":
            return self._health(request_id, employee_id, run_dir)
        return self._analyze(request_id, employee_id, request, run_dir)

    def _create_request_run_dir(
        self, request_id: str
    ) -> Tuple[Optional[Path], Optional[str]]:
        # The host run directory already carries the execution/step identity.
        # Repeating a long request ID plus a full UUID here can push legitimate
        # backend report trees beyond legacy Windows MAX_PATH. Keep a short,
        # collision-resistant physical key; the full request_id stays in the
        # protocol response and audit records.
        safe_id = SAFE_RUN_COMPONENT.sub("-", request_id).strip(".-")[:8] or "request"
        candidate = (self.run_root / (safe_id + "-" + uuid.uuid4().hex[:16])).resolve()
        if not _is_within(candidate, self.run_root) or candidate == self.run_root:
            return None, "request run directory escaped OPENKITTY_AGENT_RUN_DIR."
        try:
            candidate.mkdir(parents=True, exist_ok=False)
        except OSError as exc:
            return None, "Could not create isolated run_dir: %s" % exc
        return candidate, None

    def _health(self, request_id: str, employee_id: str, run_dir: Path) -> Dict[str, Any]:
        profile = _public_profile(employee_id)
        assigned_backend = str(profile["backend"])
        configurations = {
            assigned_backend: self.adapters[assigned_backend].configuration()
            for _ in (0,)
            if assigned_backend in self.adapters
        }
        available = sorted(
            name for name, config in configurations.items() if config.get("state") == "configured"
        )
        warnings = []
        if not available:
            warnings.append(
                "%s backend is not configured. Analyze requests will return unavailable."
                % assigned_backend
            )
        return _response(
            request_id,
            "completed",
            output="%s worker is healthy; live trading is disabled." % profile["name"],
            data={
                "agent_id": AGENT_ID,
                "employee_profile": profile,
                "capabilities": sorted(CAPABILITIES),
                "allowed_modes": sorted(ALLOWED_MODES),
                "live_trading": False,
                "run_dir": run_dir.relative_to(self.run_root).as_posix(),
                "backends": configurations,
            },
            warnings=warnings,
        )

    def _analyze(
        self,
        request_id: str,
        employee_id: str,
        request: Mapping[str, Any],
        run_dir: Path,
    ) -> Dict[str, Any]:
        input_data, input_error = self._decode_input(request.get("input", ""))
        if input_error:
            return _response(request_id, "failed", error=input_error)
        assert input_data is not None
        mode = str(input_data.get("mode") or "research").strip().lower()
        if mode not in ALLOWED_MODES:
            return _response(
                request_id,
                "failed",
                error="Only research and paper modes are allowed; live trading is disabled.",
            )
        if _contains_unsafe_execution_flag(input_data) or _contains_unsafe_natural_language(input_data):
            return _response(
                request_id,
                "failed",
                error="The request contains a live-order or execution flag; only research/paper work is allowed.",
            )

        profile = _public_profile(employee_id)
        assigned_backend = str(profile["backend"])
        if assigned_backend == "fingpt":
            source_text = _financial_text(input_data)
            if len(source_text) < FINANCIAL_TEXT_MIN_CHARACTERS:
                return _response(
                    request_id,
                    "failed",
                    data={
                        "employee_profile": profile,
                        "text_quality_gate": {
                            "passed": False,
                            "input_characters": len(source_text),
                            "minimum_characters": FINANCIAL_TEXT_MIN_CHARACTERS,
                        },
                    },
                    error=(
                        "AI金信分析师 requires at least %d characters of caller-supplied "
                        "announcement, news, research-report, or sentiment text. Paste or "
                        "upload the source text; it does not retrieve quotes or source material."
                        % FINANCIAL_TEXT_MIN_CHARACTERS
                    ),
                )

        requested_backends = input_data.get("frameworks", [assigned_backend])
        if not isinstance(requested_backends, list) or any(
            not isinstance(name, str) for name in requested_backends
        ):
            return _response(request_id, "failed", error="frameworks must be a string array.")
        requested_backends = list(dict.fromkeys(name.strip().lower() for name in requested_backends))
        unknown = sorted(set(requested_backends) - set(BACKEND_NAMES))
        if unknown:
            return _response(
                request_id,
                "failed",
                error="Unknown quantitative backend(s): %s" % ", ".join(unknown),
            )
        if not requested_backends:
            return _response(request_id, "failed", error="At least one backend is required.")
        if requested_backends != [assigned_backend]:
            return _response(
                request_id,
                "failed",
                data={"employee_profile": profile},
                error=(
                    "%s is isolated to backend '%s'; frameworks must be exactly ['%s']."
                    % (profile["name"], assigned_backend, assigned_backend)
                ),
            )

        forwarded_input = copy.deepcopy(input_data)
        # Provenance is agent-owned.  A caller may provide fixture OHLCV for
        # testing, but it cannot self-label that data as externally verified.
        forwarded_input.pop("market_data_provenance", None)
        forwarded_input.pop("_openkitty_market_data", None)
        forwarded_input["mode"] = mode
        forwarded_input["frameworks"] = requested_backends

        if assigned_backend == "fingpt":
            # Financial-intelligence input is source prose, not a ticker/name
            # query. Running arbitrary announcement text through the security
            # resolver can misclassify phrases such as “以下演示材料” as an
            # A-share name and fail before FinGPT sees the document.
            resolved_securities = []
            requested_identifiers = []
            # This employee consumes source prose only.  Strip caller-supplied
            # market/ticker metadata so the backend and the execution summary
            # cannot imply that prices or a verified security master were used.
            for key in (
                "price_history",
                "market_data_source",
                "price_source",
                "symbols",
                "tickers",
                "symbol",
                "ticker",
            ):
                forwarded_input.pop(key, None)
        else:
            try:
                resolved_securities, requested_identifiers = resolve_input_securities(
                    forwarded_input, self.security_resolver
                )
            except SecurityResolutionError as exc:
                return _response(
                    request_id,
                    "failed",
                    error="Security resolution failed: %s" % exc,
                )

        security_resolution: Dict[str, Any] = {
            "requested": requested_identifiers,
            "symbols": [item.symbol for item in resolved_securities],
            "instruments": [item.as_dict() for item in resolved_securities],
        }
        if assigned_backend == "fingpt":
            security_resolution["status"] = "not-applicable-for-financial-text"
        if resolved_securities:
            forwarded_input["symbols"] = [item.symbol for item in resolved_securities]
            for alias in ("tickers", "symbol", "ticker"):
                forwarded_input.pop(alias, None)

        market_data_provenance: Dict[str, Any] = {}
        market_data_summary: Dict[str, Any] = {"status": "not-requested"}
        preparation_warnings: List[str] = []
        market_backends_requested = bool(
            {"tradingagents", "ai-hedge-fund"} & set(requested_backends)
        )
        all_mainland = bool(resolved_securities) and all(
            item.is_mainland_equity for item in resolved_securities
        )
        has_caller_history = forwarded_input.get("price_history") not in (None, "", [], {})
        if assigned_backend == "fingpt":
            market_data_summary = {
                "status": "not-applicable-for-financial-text",
                "reason": "AI金信分析师 analyzes supplied financial prose and does not consume price data.",
            }
        elif has_caller_history:
            market_data_summary = {
                "status": "caller-supplied",
                "kind": "unverified-fixture",
                "provider": "request-fixture",
            }
        elif market_backends_requested and all_mainland:
            requested_as_of = next(
                (
                    str(forwarded_input[key]).strip()
                    for key in ("as_of", "end_date", "date")
                    if forwarded_input.get(key) not in (None, "")
                ),
                datetime.now(timezone.utc).date().isoformat(),
            )
            if self.china_market_enabled:
                try:
                    histories, market_data_provenance = self.market_history.histories(
                        resolved_securities, as_of=requested_as_of, limit=180
                    )
                    latest_dates = [
                        str(item.get("last_observation") or "")
                        for item in market_data_provenance.get("instruments", [])
                        if isinstance(item, Mapping)
                    ]
                    effective_as_of = min(value for value in latest_dates if value)
                    market_data_provenance["effective_as_of"] = effective_as_of
                    forwarded_input["price_history"] = histories
                    # The graph's analysis date must describe the observations
                    # it actually received, not a later weekend/system date.
                    forwarded_input["as_of"] = effective_as_of
                    forwarded_input["end_date"] = effective_as_of
                    security_resolution["effective_as_of"] = effective_as_of
                    market_data_summary = dict(market_data_provenance)
                    market_data_summary["status"] = "available"
                except (MarketDataError, ValueError) as exc:
                    market_data_summary = {
                        "status": "unavailable",
                        "kind": "external",
                        "provider": "china-market-primary-fallback",
                        "requested_as_of": requested_as_of,
                        "error": str(exc),
                    }
                    preparation_warnings.append(
                        "Chinese A-share daily OHLCV was unavailable from both configured "
                        "external sources; market backends will use their configured fallback "
                        "and no price data was fabricated: %s" % exc
                    )
            else:
                market_data_summary = {
                    "status": "disabled",
                    "kind": "external",
                    "provider": "china-market-primary-fallback",
                    "requested_as_of": requested_as_of,
                }
                preparation_warnings.append(
                    "China market-data enrichment is disabled; no OHLCV was fabricated."
                )
        elif market_backends_requested and resolved_securities and not all_mainland:
            market_data_summary = {
                "status": "not-applicable",
                "reason": (
                    "China market-data primary/fallback enrichment is limited to "
                    "all-mainland-equity requests."
                ),
            }

        results: List[Dict[str, Any]] = []
        accepted_artifacts: List[Dict[str, Any]] = []
        warnings: List[str] = list(preparation_warnings)

        for backend in requested_backends:
            backend_dir = run_dir / backend
            backend_dir.mkdir(parents=False, exist_ok=False)
            backend_input = copy.deepcopy(forwarded_input)
            adapter = self.adapters.get(backend)
            if adapter is None:
                result = {
                    "backend": backend,
                    "status": "unavailable",
                    "output": "",
                    "data": {},
                    "warnings": ["%s adapter is not loaded." % backend],
                    "artifacts": [],
                }
            else:
                backend_provenance: Dict[str, Any] = {}
                if backend == "fingpt":
                    # FinGPT is a financial-text sentiment capability, not a
                    # price forecaster.  Do not send a large OHLCV fixture to
                    # its language-model callable.
                    backend_input.pop("price_history", None)
                    backend_input.pop("market_data_source", None)
                    backend_input.pop("price_source", None)
                elif market_data_provenance:
                    backend_provenance = market_data_provenance
                    if backend == "tradingagents":
                        backend_input["market_data_source"] = "request-fixture"
                    elif backend == "ai-hedge-fund":
                        backend_input["price_source"] = "request-fixture"

                if all_mainland and backend == "tradingagents" and not any(
                    key in backend_input
                    for key in ("tradingagents_analysts", "selected_analysts", "analysts")
                ):
                    backend_input["tradingagents_analysts"] = ["market"]
                if all_mainland and backend == "ai-hedge-fund" and not any(
                    key in backend_input
                    for key in ("ai_hedge_fund_analysts", "selected_analysts", "analysts")
                ):
                    backend_input["ai_hedge_fund_analysts"] = list(
                        AI_HEDGE_FUND_MAINLAND_DEFAULT_ANALYSTS
                    )
                if all_mainland and backend == "ai-hedge-fund":
                    analyst_keys = next(
                        (
                            backend_input[key]
                            for key in (
                                "ai_hedge_fund_analysts",
                                "selected_analysts",
                                "analysts",
                            )
                            if key in backend_input
                        ),
                        [],
                    )
                    normalized_analysts = {
                        _hedge_analyst_key(item) for item in analyst_keys
                    } if isinstance(analyst_keys, list) else set()
                    if normalized_analysts and normalized_analysts <= {
                        "technical",
                        "technicals",
                        "technical_analyst",
                    }:
                        return _response(
                            request_id,
                            "failed",
                            data={"employee_profile": profile},
                            error=(
                                "AI投研经理 cannot complete mainland-equity research with "
                                "technical analysis alone; include fundamentals, valuation, "
                                "or sentiment coverage."
                            ),
                        )
                if all_mainland and backend == "ai-hedge-fund":
                    # The reviewed employee credential is exposed to this
                    # child as an OpenAI-compatible MiniMax endpoint.  Avoid
                    # sending the upstream gpt-4.1 default to that endpoint.
                    # Explicit caller choices remain authoritative.
                    backend_input.setdefault("model_name", "MiniMax-M3")
                    backend_input.setdefault("model_provider", "OpenAI")
                if (
                    all_mainland
                    and backend == "ai-hedge-fund"
                    and not market_data_provenance
                    and "price_source" not in backend_input
                ):
                    backend_input["price_source"] = "yfinance"
                backend_request = build_backend_request(
                    backend,
                    request_id,
                    mode,
                    backend_input,
                    backend_dir,
                    market_data_provenance=backend_provenance,
                )
                result = adapter.invoke(backend_request)

            if backend == "ai-hedge-fund":
                result = self._apply_investment_manager_quality_gate(result, backend_input)
            elif backend == "fingpt":
                result = self._apply_financial_intelligence_boundary(result, backend_input)

            safe_artifacts, artifact_warnings = self._validate_artifacts(
                result.get("artifacts", []), run_dir, backend
            )
            result["artifacts"] = safe_artifacts
            accepted_artifacts.extend(safe_artifacts)
            warnings.extend(str(item) for item in result.get("warnings", []))
            warnings.extend(artifact_warnings)
            results.append(result)

        statuses = [result["status"] for result in results]
        successful = [result for result in results if result["status"] in {"success", "degraded"}]
        if successful and all(status == "success" for status in statuses):
            overall_status = "completed"
        elif successful:
            overall_status = "degraded"
        elif all(status == "unavailable" for status in statuses):
            overall_status = "unavailable"
        else:
            overall_status = "failed"

        output_parts: List[str] = []
        for result in successful:
            body = str(result.get("output") or "").strip()
            if not body:
                continue
            if not body.startswith("#"):
                body = "# %s\n\n%s" % (profile["name"], body)
            output_parts.append(body)
        if output_parts:
            output = "\n\n".join(output_parts)
        elif overall_status == "unavailable":
            output = "No configured quantitative backend is available; no analysis was fabricated."
        else:
            output = "The configured quantitative backends did not return a usable analysis."

        response_data = {
            "mode": mode,
            "live_trading": False,
            "employee_profile": profile,
            "run_dir": run_dir.relative_to(self.run_root).as_posix(),
            "security_resolution": security_resolution,
            "market_data": market_data_summary,
            "backend_results": results,
        }
        try:
            structured_path = run_dir / "structured_result.json"
            structured_path.write_text(
                json.dumps(response_data, ensure_ascii=False, indent=2),
                encoding="utf-8",
            )
            accepted_artifacts.append(
                {
                    "name": "%s完整结构化结果" % profile["name"],
                    "path": structured_path.relative_to(self.run_root).as_posix(),
                    "media_type": "application/json",
                    "size": structured_path.stat().st_size,
                }
            )
        except (OSError, TypeError, ValueError) as exc:
            warnings.append("Could not persist the bounded structured result artifact: %s" % exc)

        return _response(
            request_id,
            overall_status,
            output=output,
            data=response_data,
            warnings=list(dict.fromkeys(warnings)),
            artifacts=accepted_artifacts,
            error=("The configured quantitative backends failed." if overall_status == "failed" else ""),
        )

    @staticmethod
    def _apply_investment_manager_quality_gate(
        result: Mapping[str, Any], input_data: Mapping[str, Any]
    ) -> Dict[str, Any]:
        checked = dict(result)
        data = checked.get("data", {})
        data = dict(data) if isinstance(data, Mapping) else {}
        quality = data.get("quality_gate", {})
        quality_issues: List[str] = []
        if isinstance(quality, Mapping) and quality.get("passed") is False:
            raw_issues = quality.get("issues", [])
            if isinstance(raw_issues, list):
                quality_issues.extend(str(item) for item in raw_issues if str(item).strip())
        decisions = data.get("decisions")
        if not isinstance(decisions, (Mapping, list)) or not decisions:
            quality_issues.append("upstream returned no structured investment decision")
        quality_issues.extend(_hedge_decision_quality_issues(decisions))
        if "default decision" in str(checked.get("output") or "").lower():
            quality_issues.append("upstream output contains a Default decision fallback")
        quality_issues.extend(
            "upstream output contains fallback/error text: %s" % marker
            for marker in _hedge_fallback_text_markers(checked.get("output"))
        )
        quality_issues.extend(
            "upstream analyst signals contain fallback/error text: %s" % marker
            for marker in _hedge_fallback_text_markers(data.get("analyst_signals"))
        )
        for risk_key in (
            "risk_management",
            "risk_management_signals",
            "risk_management_analysis",
        ):
            quality_issues.extend(
                "upstream %s contains fallback/error text: %s" % (risk_key, marker)
                for marker in _hedge_fallback_text_markers(data.get(risk_key))
            )
        selected_analysts = next(
            (
                input_data[key]
                for key in (
                    "ai_hedge_fund_analysts",
                    "selected_analysts",
                    "analysts",
                )
                if isinstance(input_data.get(key), list)
            ),
            [],
        )
        if selected_analysts:
            analyst_signals = data.get("analyst_signals")
            raw_symbols = input_data.get("symbols", input_data.get("tickers", []))
            if isinstance(raw_symbols, str):
                symbols = [raw_symbols.strip()] if raw_symbols.strip() else []
            elif isinstance(raw_symbols, list):
                symbols = [str(item).strip() for item in raw_symbols if str(item).strip()]
            else:
                symbols = []
            if not symbols and isinstance(input_data.get("symbol"), str):
                symbol = str(input_data["symbol"]).strip()
                symbols = [symbol] if symbol else []
            if (
                not symbols
                and isinstance(decisions, Mapping)
                and not any(
                    key in decisions
                    for key in ("reasoning", "confidence", "quantity", "action")
                )
            ):
                symbols = [str(item) for item in decisions]
            quality_issues.extend(
                _hedge_selected_signal_quality_issues(
                    selected_analysts, symbols, analyst_signals
                )
            )
        quality_issues = list(dict.fromkeys(quality_issues))
        data["manager_quality_gate"] = {
            "passed": not quality_issues,
            "issues": quality_issues,
            "success_requires_non_fallback_decision": True,
        }
        checked["data"] = data
        if quality_issues and checked.get("status") == "success":
            checked["status"] = "degraded"
        warnings = checked.get("warnings", [])
        warnings = list(warnings) if isinstance(warnings, list) else [str(warnings)]
        if quality_issues:
            warnings.append(
                "AI投研经理 output is incomplete and is not counted as success: %s."
                % "; ".join(quality_issues)
            )
        checked["warnings"] = list(dict.fromkeys(warnings))
        return checked

    @staticmethod
    def _apply_financial_intelligence_boundary(
        result: Mapping[str, Any], input_data: Mapping[str, Any]
    ) -> Dict[str, Any]:
        checked = dict(result)
        data = checked.get("data", {})
        data = dict(data) if isinstance(data, Mapping) else {}
        source_characters = len(_financial_text(input_data))
        reported_characters = _numeric(data.get("input_characters"))
        measured_characters = (
            int(reported_characters)
            if reported_characters is not None
            else source_characters
        )
        text_quality_passed = measured_characters >= FINANCIAL_TEXT_MIN_CHARACTERS
        data["text_quality_gate"] = {
            "passed": text_quality_passed,
            "input_characters": measured_characters,
            "minimum_characters": FINANCIAL_TEXT_MIN_CHARACTERS,
        }
        data["analysis_boundary"] = {
            "input": "caller-supplied financial text",
            "supports": ["announcement", "news", "research-report", "sentiment"],
            "does_not_support": [
                "market-data retrieval",
                "price forecasting",
                "trade recommendation",
                "order execution",
            ],
        }
        checked["data"] = data
        warnings = checked.get("warnings", [])
        warnings = list(warnings) if isinstance(warnings, list) else [str(warnings)]
        warnings.append(
            "AI金信分析师 analyzes supplied financial text only; it does not retrieve quotes, "
            "forecast prices, recommend trades, or submit orders."
        )
        if not text_quality_passed:
            if checked.get("status") == "success":
                checked["status"] = "degraded"
            warnings.append(
                "AI金信分析师 backend reported insufficient source text; this output is not "
                "counted as success. Paste or upload the announcement, news, or report text."
            )
        checked["warnings"] = list(dict.fromkeys(warnings))
        return checked

    def _decode_input(self, raw: Any) -> Tuple[Optional[Dict[str, Any]], Optional[str]]:
        if not isinstance(raw, str):
            return None, "input must be a string."
        text = raw.strip()
        if not text:
            return {"mode": "research", "question": ""}, None
        try:
            decoded = json.loads(text)
        except json.JSONDecodeError:
            return _decode_plain_language_input(text), None
        if not isinstance(decoded, dict):
            return None, "JSON input must decode to an object."
        return decoded, None

    def _validate_artifacts(
        self, raw_artifacts: Any, run_dir: Path, backend: str
    ) -> Tuple[List[Dict[str, Any]], List[str]]:
        if not isinstance(raw_artifacts, list):
            return [], ["%s returned a non-array artifact list; it was discarded." % backend]
        accepted: List[Dict[str, Any]] = []
        warnings: List[str] = []
        for item in raw_artifacts:
            if isinstance(item, str):
                artifact = {"path": item}
            elif isinstance(item, dict):
                artifact = dict(item)
            else:
                warnings.append("%s returned an invalid artifact entry; it was discarded." % backend)
                continue
            raw_path = artifact.get("path")
            if not isinstance(raw_path, str) or not raw_path.strip():
                warnings.append("%s returned an artifact without a valid path; it was discarded." % backend)
                continue
            path = Path(raw_path).expanduser()
            if not path.is_absolute():
                # Backend-relative paths are rooted in that backend's private
                # subdirectory. Absolute paths are accepted only after the
                # same request-root confinement check below.
                path = run_dir / backend / path
            try:
                resolved = path.resolve(strict=True)
            except OSError:
                warnings.append("%s artifact does not exist and was discarded." % backend)
                continue
            if not _is_within(resolved, run_dir) or resolved == run_dir:
                warnings.append("%s artifact escaped its request run_dir and was discarded." % backend)
                continue
            if not (resolved.is_file() or resolved.is_dir()):
                warnings.append("%s artifact is not a regular file/directory and was discarded." % backend)
                continue
            # The protocol peer (OpenKitty) owns ``self.run_root``.  ``run_dir``
            # is an additional per-request isolation directory created by this
            # worker, so returning a path relative to it drops one component
            # and makes the host look for the artifact in the wrong place.
            # Keep request confinement above, but express accepted artifacts in
            # the host's coordinate system for deterministic validation.
            artifact["path"] = resolved.relative_to(self.run_root).as_posix()
            artifact.setdefault("backend", backend)
            accepted.append(artifact)
        return accepted, warnings


def parse_request_line(line: str, max_request_bytes: int) -> Tuple[Optional[Any], Optional[str]]:
    if len(line.encode("utf-8", errors="replace")) > max_request_bytes:
        return None, "Request exceeded the configured size limit."
    try:
        return json.loads(line), None
    except json.JSONDecodeError as exc:
        return None, "Invalid JSON: %s" % exc
