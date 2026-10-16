"""Bounded Chinese-security resolution and daily OHLCV retrieval.

This module is intentionally independent from the three quantitative engines.
It translates user-facing Chinese security names and common mainland code
forms into the Yahoo-compatible symbols consumed by TradingAgents and the AI
Hedge Fund adapter.  For mainland equities it retrieves adjusted daily OHLCV
from Eastmoney with Tencent as an independent fallback, so both upstream
graphs receive the same real external observations when Yahoo is unavailable.

No brokerage or order API is present here.  Network access is restricted to
three fixed HTTPS hosts, with response-size and timeout limits.
"""

from __future__ import annotations

from dataclasses import dataclass
from datetime import date, datetime, timedelta, timezone
import http.client
import json
import math
import re
import time
from typing import Any, Callable, Dict, Iterable, List, Mapping, Optional, Sequence, Tuple
import unicodedata
from urllib import error as urlerror
from urllib import parse as urlparse
from urllib import request as urlrequest


SUGGEST_ENDPOINT = "https://searchapi.eastmoney.com/api/suggest/get"
KLINE_ENDPOINT = "https://push2his.eastmoney.com/api/qt/stock/kline/get"
TENCENT_KLINE_ENDPOINT = (
    "https://proxy.finance.qq.com/ifzqgtimg/appstock/app/newfqkline/get"
)
ALLOWED_ENDPOINTS = {
    ("searchapi.eastmoney.com", "/api/suggest/get"),
    ("push2his.eastmoney.com", "/api/qt/stock/kline/get"),
    ("proxy.finance.qq.com", "/ifzqgtimg/appstock/app/newfqkline/get"),
}
DEFAULT_TIMEOUT_SECONDS = 5
DEFAULT_MAX_RESPONSE_BYTES = 2 * 1024 * 1024
MIN_HISTORY_ROWS = 20
MAX_HISTORY_ROWS = 300

CHINESE_CHARACTER = re.compile(r"[\u3400-\u9fff]")
MAINLAND_CANONICAL = re.compile(r"^(\d{6})\.(SS|SZ|BJ)$", re.IGNORECASE)
PREFIXED_MAINLAND = re.compile(
    r"^(SH|SSE|XSHG|SZ|SZSE|XSHE|BJ|BSE)(\d{6})$", re.IGNORECASE
)
SUFFIXED_MAINLAND = re.compile(
    r"^(\d{6})\.(SH|SSE|XSHG|SZ|SZSE|XSHE|BJ|BSE)$", re.IGNORECASE
)
BARE_MAINLAND = re.compile(r"^\d{6}$")

MENTION_PATTERN = re.compile(r"@[A-Za-z0-9_\-\u3400-\u9fff]+")
ACTION_NAME_PATTERN = re.compile(
    r"(?:分析|研究|看看|看一下|看下|评估|解读|诊断|查询|查一下|查下|关注|"
    r"比较|对比)(?:一下|下)?\s*([\u3400-\u9fff]{2,24})"
)
MARKED_NAME_PATTERN = re.compile(
    r"(?:股票|标的|证券)(?:名称|名字)?(?:是|为|:|：)?\s*([\u3400-\u9fff]{2,24})"
)
QUESTION_NAME_PATTERN = re.compile(
    r"([\u3400-\u9fff]{2,16})(?:怎么样|如何|能买吗|值得关注吗|值得研究吗)"
)
NAME_SPLIT_PATTERN = re.compile(r"[、,，;；/]|以及|(?:[\u3400-\u9fff]{2,})与(?=[\u3400-\u9fff]{2,})")

LEADING_FILLERS = (
    "请结合",
    "请使用",
    "麻烦帮我",
    "请帮我",
    "帮我",
    "麻烦",
    "请",
    "给我",
    "关于",
    "对于",
    "对",
    "一下",
    "下",
)
TRAILING_FILLERS = (
    "值得关注吗",
    "值得研究吗",
    "怎么样",
    "能买吗",
    "如何",
    "未来走势",
    "后续走势",
    "近期走势",
    "投资价值",
    "基本面",
    "技术面",
    "风险情况",
    "风险",
    "前景",
    "走势",
    "表现",
    "情况",
)
NON_SECURITY_TERMS = frozenset(
    {
        "量化交易员",
        "只做研究",
        "不执行交易",
        "不要交易",
        "模拟交易",
        "纸面交易",
        "今天",
        "近期",
        "现在",
    }
)


class ChinaMarketError(RuntimeError):
    """Base class for transparent market-data and resolution failures."""


class SecurityResolutionError(ChinaMarketError):
    """A user security identifier could not be resolved unambiguously."""


class MarketDataError(ChinaMarketError):
    """External market history could not be retrieved or validated."""


class _NoRedirectHandler(urlrequest.HTTPRedirectHandler):
    """Reject redirects so an allowlisted origin cannot bounce to another host."""

    def redirect_request(self, req: Any, fp: Any, code: int, msg: str, headers: Any, newurl: str) -> Any:
        del req, fp, code, msg, headers, newurl
        raise ChinaMarketError("market-data endpoint redirects are forbidden")


@dataclass(frozen=True)
class ResolvedSecurity:
    query: str
    symbol: str
    code: str
    name: str
    exchange: str
    market: str
    secid: str
    source: str

    @property
    def is_mainland_equity(self) -> bool:
        return self.exchange in {"SSE", "SZSE", "BSE"} and bool(self.secid)

    def as_dict(self) -> Dict[str, Any]:
        return {
            "query": self.query,
            "symbol": self.symbol,
            "code": self.code,
            "name": self.name,
            "exchange": self.exchange,
            "market": self.market,
            "secid": self.secid,
            "source": self.source,
        }


def _bounded_int(raw: Any, default: int, minimum: int, maximum: int) -> int:
    try:
        value = int(raw)
    except (TypeError, ValueError):
        return default
    return max(minimum, min(value, maximum))


def _endpoint_url(endpoint: str, params: Mapping[str, Any]) -> str:
    parsed = urlparse.urlparse(endpoint)
    if parsed.scheme != "https" or (parsed.hostname or "", parsed.path) not in ALLOWED_ENDPOINTS:
        raise ChinaMarketError("market-data endpoint is not on the fixed HTTPS allowlist")
    query = urlparse.urlencode({key: str(value) for key, value in params.items()})
    return urlparse.urlunparse((parsed.scheme, parsed.netloc, parsed.path, "", query, ""))


def bounded_json_get(
    endpoint: str,
    params: Mapping[str, Any],
    *,
    timeout_seconds: int = DEFAULT_TIMEOUT_SECONDS,
    max_response_bytes: int = DEFAULT_MAX_RESPONSE_BYTES,
    opener: Optional[Callable[..., Any]] = None,
) -> Mapping[str, Any]:
    """GET JSON from one allowlisted endpoint under strict resource bounds."""

    timeout = _bounded_int(timeout_seconds, DEFAULT_TIMEOUT_SECONDS, 1, 20)
    max_bytes = _bounded_int(
        max_response_bytes, DEFAULT_MAX_RESPONSE_BYTES, 1024, 8 * 1024 * 1024
    )
    url = _endpoint_url(endpoint, params)
    req = urlrequest.Request(
        url,
        headers={
            "Accept": "application/json",
            "User-Agent": "OpenKitty-QuantTrader/0.1",
            "Referer": "https://quote.eastmoney.com/",
        },
        method="GET",
    )
    if opener is None:
        # urllib inherits the Windows Internet Settings proxy even when the
        # OpenKitty process has no proxy environment variables.  A stale local
        # desktop proxy then appears as a RemoteDisconnected error while a
        # direct HTTPS request succeeds.  These three fixed, allowlisted China-
        # market endpoints are reached directly and never carry secrets.
        direct_opener = urlrequest.build_opener(
            urlrequest.ProxyHandler({}), _NoRedirectHandler()
        )
        open_impl = direct_opener.open
    else:
        open_impl = opener
    deadline = time.monotonic() + timeout
    raw = b""
    last_error: Optional[BaseException] = None
    # Both calls are idempotent GETs.  One immediate retry absorbs the
    # occasional upstream TCP close while a shared deadline bounds total time.
    for attempt in range(2):
        remaining = deadline - time.monotonic()
        if remaining <= 0:
            break
        try:
            with open_impl(req, timeout=max(0.1, remaining)) as response:
                final_url = response.geturl() if hasattr(response, "geturl") else url
                final_parsed = urlparse.urlparse(final_url)
                requested_parsed = urlparse.urlparse(url)
                if (
                    final_parsed.scheme != "https"
                    or final_parsed.hostname != requested_parsed.hostname
                    or final_parsed.path != requested_parsed.path
                ):
                    raise ChinaMarketError("market-data endpoint redirect was rejected")
                content_type = str(response.headers.get("Content-Type", ""))
                if (
                    content_type
                    and "json" not in content_type.lower()
                    and "text" not in content_type.lower()
                ):
                    raise ChinaMarketError(
                        "market-data endpoint returned a non-JSON content type"
                    )
                raw = response.read(max_bytes + 1)
            last_error = None
            break
        except ChinaMarketError:
            raise
        except (
            urlerror.URLError,
            http.client.RemoteDisconnected,
            ConnectionResetError,
            TimeoutError,
            OSError,
        ) as exc:
            last_error = exc
            if attempt == 1:
                break
    if last_error is not None:
        raise ChinaMarketError("market-data request failed after 2 attempts: %s" % last_error) from last_error
    if not raw:
        raise ChinaMarketError("market-data request timed out within the configured deadline")
    if len(raw) > max_bytes:
        raise ChinaMarketError("market-data response exceeded the configured size limit")
    try:
        decoded = json.loads(raw.decode("utf-8"))
    except (UnicodeDecodeError, json.JSONDecodeError) as exc:
        raise ChinaMarketError("market-data endpoint returned invalid JSON") from exc
    if not isinstance(decoded, Mapping):
        raise ChinaMarketError("market-data endpoint returned a non-object JSON value")
    return decoded


def _exchange_for_code(code: str) -> Tuple[str, str, str]:
    if code.startswith(("600", "601", "603", "605", "688", "689")):
        return "SSE", "沪A", "1." + code
    if code.startswith(("000", "001", "002", "003", "300", "301")):
        return "SZSE", "深A", "0." + code
    # Beijing Exchange listings historically used 4/8 prefixes and are being
    # migrated to the 920 prefix.  Yahoo coverage is less reliable, but the
    # canonical .BJ identity remains useful and is not guessed as Shanghai.
    if code.startswith(("4", "8", "920")):
        return "BSE", "京A", "0." + code
    raise SecurityResolutionError(
        "cannot infer a mainland A-share exchange for code %s; include .SH/.SZ/.BJ" % code
    )


def _suffix_for_exchange(exchange: str) -> str:
    return {"SSE": "SS", "SZSE": "SZ", "BSE": "BJ"}[exchange]


def canonicalize_security_identifier(raw: str) -> Optional[ResolvedSecurity]:
    """Normalize explicit ticker/code forms without making a network request."""

    if not isinstance(raw, str):
        raise SecurityResolutionError("every security identifier must be a string")
    query = unicodedata.normalize("NFKC", raw).strip().lstrip("$")
    if not query:
        raise SecurityResolutionError("security identifier must not be empty")
    upper = query.upper()
    if CHINESE_CHARACTER.search(upper):
        return None

    prefixed = PREFIXED_MAINLAND.fullmatch(upper)
    suffixed = SUFFIXED_MAINLAND.fullmatch(upper)
    canonical = MAINLAND_CANONICAL.fullmatch(upper)
    if prefixed:
        raw_exchange, code = prefixed.groups()
    elif suffixed:
        code, raw_exchange = suffixed.groups()
    elif canonical:
        code, raw_exchange = canonical.groups()
    elif BARE_MAINLAND.fullmatch(upper):
        code = upper
        exchange, market, secid = _exchange_for_code(code)
        return ResolvedSecurity(
            query=query,
            symbol=code + "." + _suffix_for_exchange(exchange),
            code=code,
            name="",
            exchange=exchange,
            market=market,
            secid=secid,
            source="local-code-rules",
        )
    else:
        return ResolvedSecurity(
            query=query,
            symbol=upper,
            code=upper,
            name="",
            exchange="",
            market="",
            secid="",
            source="explicit-symbol",
        )

    exchange_aliases = {
        "SH": "SSE",
        "SS": "SSE",
        "SSE": "SSE",
        "XSHG": "SSE",
        "SZ": "SZSE",
        "SZSE": "SZSE",
        "XSHE": "SZSE",
        "BJ": "BSE",
        "BSE": "BSE",
    }
    exchange = exchange_aliases[raw_exchange.upper()]
    expected_exchange, market, secid = _exchange_for_code(code)
    if exchange != expected_exchange:
        raise SecurityResolutionError(
            "code %s conflicts with the supplied exchange %s" % (code, raw_exchange)
        )
    return ResolvedSecurity(
        query=query,
        symbol=code + "." + _suffix_for_exchange(exchange),
        code=code,
        name="",
        exchange=exchange,
        market=market,
        secid=secid,
        source="local-code-rules",
    )


def _trim_name_candidate(value: str) -> str:
    candidate = value.strip()
    changed = True
    while changed:
        changed = False
        for filler in LEADING_FILLERS:
            if candidate.startswith(filler) and len(candidate) - len(filler) >= 2:
                candidate = candidate[len(filler) :].strip()
                changed = True
                break
    changed = True
    while changed:
        changed = False
        for filler in TRAILING_FILLERS:
            if candidate.endswith(filler) and len(candidate) - len(filler) >= 2:
                candidate = candidate[: -len(filler)].strip()
                changed = True
                break
    return candidate


def extract_chinese_security_queries(text: str) -> List[str]:
    """Extract likely Chinese security names from natural-language requests.

    This is deliberately a candidate extractor, not a name dictionary.  Every
    candidate must still be confirmed by the external security master and
    ambiguous results are rejected instead of guessed.
    """

    if not isinstance(text, str) or not text.strip():
        return []
    normalized = unicodedata.normalize("NFKC", text)
    normalized = MENTION_PATTERN.sub(" ", normalized)
    raw_candidates: List[str] = []
    for pattern in (MARKED_NAME_PATTERN, ACTION_NAME_PATTERN, QUESTION_NAME_PATTERN):
        raw_candidates.extend(match.group(1) for match in pattern.finditer(normalized))

    candidates: List[str] = []
    for raw in raw_candidates:
        # Handle the common "比较甲和乙" form without treating a leading 和
        # in a company name as a separator.
        pieces = re.split(r"和(?=[\u3400-\u9fff]{2,}$)", raw)
        for piece in pieces:
            candidate = _trim_name_candidate(piece)
            if (
                2 <= len(candidate) <= 16
                and candidate not in NON_SECURITY_TERMS
                and CHINESE_CHARACTER.fullmatch(candidate[0])
                and candidate not in candidates
            ):
                candidates.append(candidate)
    return candidates[:10]


class ChinaSecurityResolver:
    def __init__(
        self,
        *,
        timeout_seconds: int = DEFAULT_TIMEOUT_SECONDS,
        max_response_bytes: int = DEFAULT_MAX_RESPONSE_BYTES,
        get_json: Optional[Callable[[str, Mapping[str, Any]], Mapping[str, Any]]] = None,
    ) -> None:
        self.timeout_seconds = _bounded_int(timeout_seconds, DEFAULT_TIMEOUT_SECONDS, 1, 20)
        self.max_response_bytes = _bounded_int(
            max_response_bytes, DEFAULT_MAX_RESPONSE_BYTES, 1024, 8 * 1024 * 1024
        )
        self._get_json = get_json
        self._cache: Dict[str, ResolvedSecurity] = {}

    def _fetch(self, endpoint: str, params: Mapping[str, Any]) -> Mapping[str, Any]:
        if self._get_json is not None:
            return self._get_json(endpoint, params)
        return bounded_json_get(
            endpoint,
            params,
            timeout_seconds=self.timeout_seconds,
            max_response_bytes=self.max_response_bytes,
        )

    def resolve(self, raw: str) -> ResolvedSecurity:
        explicit = canonicalize_security_identifier(raw)
        if explicit is not None:
            return explicit
        query = unicodedata.normalize("NFKC", raw).strip()
        if query in self._cache:
            return self._cache[query]
        if not (2 <= len(query) <= 32) or not CHINESE_CHARACTER.search(query):
            raise SecurityResolutionError("invalid Chinese security name")
        try:
            payload = self._fetch(SUGGEST_ENDPOINT, {"input": query, "type": 14})
        except ChinaMarketError as exc:
            raise SecurityResolutionError(
                "Chinese security lookup failed; provide an explicit code such as 600596.SH: %s"
                % exc
            ) from exc

        table = payload.get("QuotationCodeTable")
        rows = table.get("Data") if isinstance(table, Mapping) else None
        if not isinstance(rows, list):
            rows = []
        eligible: List[Mapping[str, Any]] = []
        for row in rows:
            if not isinstance(row, Mapping):
                continue
            market = str(row.get("SecurityTypeName") or "")
            code = str(row.get("Code") or "")
            if market in {"沪A", "深A", "京A"} and BARE_MAINLAND.fullmatch(code):
                eligible.append(row)
        exact = [row for row in eligible if str(row.get("Name") or "").strip() == query]
        selected: Optional[Mapping[str, Any]] = None
        if len(exact) == 1:
            selected = exact[0]
        elif len(eligible) == 1:
            selected = eligible[0]
        elif len(eligible) > 1:
            matches = ", ".join(
                "%s(%s)" % (str(row.get("Name") or ""), str(row.get("Code") or ""))
                for row in eligible[:5]
            )
            raise SecurityResolutionError(
                "Chinese security name %s is ambiguous: %s; provide an explicit code"
                % (query, matches)
            )
        else:
            raise SecurityResolutionError(
                "Chinese security name %s was not found as a mainland A-share; provide an explicit code"
                % query
            )

        code = str(selected.get("Code"))
        exchange, inferred_market, inferred_secid = _exchange_for_code(code)
        response_market = str(selected.get("SecurityTypeName") or inferred_market)
        market_exchange = {"沪A": "SSE", "深A": "SZSE", "京A": "BSE"}[response_market]
        if market_exchange != exchange:
            raise SecurityResolutionError("security lookup returned a conflicting exchange/code pair")
        secid = str(selected.get("QuoteID") or inferred_secid)
        if not re.fullmatch(r"[01]\.\d{6}", secid):
            secid = inferred_secid
        resolved = ResolvedSecurity(
            query=query,
            symbol=code + "." + _suffix_for_exchange(exchange),
            code=code,
            name=str(selected.get("Name") or query).strip(),
            exchange=exchange,
            market=response_market,
            secid=secid,
            source="eastmoney-security-suggest",
        )
        self._cache[query] = resolved
        return resolved

    def resolve_many(self, values: Sequence[str]) -> List[ResolvedSecurity]:
        resolved: List[ResolvedSecurity] = []
        seen = set()
        for value in values:
            item = self.resolve(value)
            if item.symbol not in seen:
                resolved.append(item)
                seen.add(item.symbol)
        return resolved


class EastmoneyDailyHistory:
    """Chinese daily history with Eastmoney primary and Tencent HTTPS fallback.

    The historical class name is retained for API compatibility.  Provenance
    always records the source that actually supplied each instrument.
    """
    def __init__(
        self,
        *,
        timeout_seconds: int = DEFAULT_TIMEOUT_SECONDS,
        max_response_bytes: int = DEFAULT_MAX_RESPONSE_BYTES,
        get_json: Optional[Callable[[str, Mapping[str, Any]], Mapping[str, Any]]] = None,
        now: Optional[Callable[[], datetime]] = None,
    ) -> None:
        self.timeout_seconds = _bounded_int(timeout_seconds, DEFAULT_TIMEOUT_SECONDS, 1, 20)
        self.max_response_bytes = _bounded_int(
            max_response_bytes, DEFAULT_MAX_RESPONSE_BYTES, 1024, 8 * 1024 * 1024
        )
        self._get_json = get_json
        self._now = now or (lambda: datetime.now(timezone(timedelta(hours=8))))

    def _fetch(self, endpoint: str, params: Mapping[str, Any]) -> Mapping[str, Any]:
        if self._get_json is not None:
            return self._get_json(endpoint, params)
        return bounded_json_get(
            endpoint,
            params,
            timeout_seconds=self.timeout_seconds,
            max_response_bytes=self.max_response_bytes,
        )

    @staticmethod
    def _eastmoney_candidates(payload: Mapping[str, Any]) -> List[Dict[str, Any]]:
        data = payload.get("data")
        klines = data.get("klines") if isinstance(data, Mapping) else None
        if not isinstance(klines, list):
            raise MarketDataError("Eastmoney returned no daily history")
        candidates: List[Dict[str, Any]] = []
        for raw in klines:
            if not isinstance(raw, str):
                continue
            parts = raw.split(",")
            if len(parts) < 6:
                continue
            candidates.append(
                {
                    "date": parts[0],
                    "open": parts[1],
                    "close": parts[2],
                    "high": parts[3],
                    "low": parts[4],
                    "volume": parts[5],
                    # Eastmoney A-share f56 is reported in hands/lots.
                    "volume_multiplier": 100,
                }
            )
        return candidates

    @staticmethod
    def _tencent_symbol(security: ResolvedSecurity) -> str:
        if security.exchange == "BSE" and not security.code.startswith("920"):
            raise MarketDataError(
                "Tencent fallback refuses legacy 4/8-prefix BSE codes; use a current 920 code"
            )
        prefix = {"SSE": "sh", "SZSE": "sz", "BSE": "bj"}.get(security.exchange)
        if not prefix:
            raise MarketDataError("Tencent daily history needs a mainland exchange")
        return prefix + security.code

    @staticmethod
    def _tencent_candidates(
        payload: Mapping[str, Any], tencent_symbol: str
    ) -> List[Dict[str, Any]]:
        if payload.get("code") not in (0, "0"):
            raise MarketDataError("Tencent daily history returned a non-zero status")
        if payload.get("msg") not in (None, ""):
            raise MarketDataError("Tencent daily history returned a non-empty error message")
        data = payload.get("data")
        if not isinstance(data, Mapping):
            raise MarketDataError("Tencent daily history returned no data object")
        instrument = data.get(tencent_symbol)
        if not isinstance(instrument, Mapping):
            raise MarketDataError("Tencent daily history omitted the requested symbol node")
        klines = instrument.get("qfqday") if isinstance(instrument, Mapping) else None
        if not isinstance(klines, list) or not klines:
            raise MarketDataError("Tencent returned no forward-adjusted daily history")
        candidates: List[Dict[str, Any]] = []
        for raw in klines:
            if not isinstance(raw, list) or len(raw) < 6:
                continue
            candidates.append(
                {
                    "date": raw[0],
                    "open": raw[1],
                    "close": raw[2],
                    "high": raw[3],
                    "low": raw[4],
                    "volume": raw[5],
                    # Tencent qfqday volume is reported in hands/lots.
                    "volume_multiplier": 100,
                }
            )
        return candidates

    def _validated_rows(
        self,
        candidates: Iterable[Mapping[str, Any]],
        *,
        begin_date: date,
        end_date: date,
        row_limit: int,
        provider: str,
    ) -> List[Dict[str, Any]]:
        rows: List[Dict[str, Any]] = []
        china_now = self._now()
        if china_now.tzinfo is None:
            china_now = china_now.replace(tzinfo=timezone(timedelta(hours=8)))
        else:
            china_now = china_now.astimezone(timezone(timedelta(hours=8)))
        current_session_incomplete = (china_now.hour, china_now.minute) < (15, 10)
        for raw in candidates:
            try:
                row_date = datetime.strptime(str(raw.get("date") or ""), "%Y-%m-%d").date()
                open_price = float(raw.get("open"))
                close_price = float(raw.get("close"))
                high_price = float(raw.get("high"))
                low_price = float(raw.get("low"))
                volume = int(
                    float(raw.get("volume")) * float(raw.get("volume_multiplier", 1))
                )
            except (TypeError, ValueError, OverflowError):
                continue
            prices = (open_price, high_price, low_price, close_price)
            if row_date < begin_date or row_date > end_date or (
                row_date == china_now.date()
                and current_session_incomplete
                and end_date >= china_now.date()
            ):
                continue
            if (
                not all(math.isfinite(value) and value > 0 for value in prices)
                or low_price > min(open_price, close_price)
                or high_price < max(open_price, close_price)
                or high_price < low_price
                or volume < 0
            ):
                raise MarketDataError(
                    "%s returned an invalid OHLCV invariant for %s"
                    % (provider, row_date.isoformat())
                )
            rows.append(
                {
                    "date": row_date.isoformat(),
                    "open": open_price,
                    "high": high_price,
                    "low": low_price,
                    "close": close_price,
                    "volume": volume,
                }
            )
        unique_rows: Dict[str, Dict[str, Any]] = {}
        for row in rows:
            existing = unique_rows.get(row["date"])
            if existing is not None and existing != row:
                raise MarketDataError(
                    "%s returned conflicting observations for %s"
                    % (provider, row["date"])
                )
            unique_rows[row["date"]] = row
        rows = sorted(unique_rows.values(), key=lambda row: row["date"])
        if len(rows) < MIN_HISTORY_ROWS:
            raise MarketDataError(
                "%s returned %d valid rows; at least %d are required"
                % (provider, len(rows), MIN_HISTORY_ROWS)
            )
        return rows[-row_limit:]

    def history(
        self, security: ResolvedSecurity, *, as_of: str, limit: int = 180
    ) -> Tuple[List[Dict[str, Any]], Dict[str, Any]]:
        if not security.is_mainland_equity:
            raise MarketDataError("Chinese daily history only supports resolved mainland equities")
        try:
            end_date = datetime.strptime(as_of, "%Y-%m-%d").date()
        except ValueError as exc:
            raise MarketDataError("as_of must use ISO YYYY-MM-DD") from exc
        row_limit = _bounded_int(limit, 180, MIN_HISTORY_ROWS, MAX_HISTORY_ROWS)
        begin_date = end_date - timedelta(days=max(400, row_limit * 3))
        primary_error: Optional[BaseException] = None
        provider = "eastmoney-push2his"
        try:
            payload = self._fetch(
                KLINE_ENDPOINT,
                {
                    "secid": security.secid,
                    "fields1": "f1,f2,f3,f4,f5,f6",
                    "fields2": "f51,f52,f53,f54,f55,f56,f57,f58,f59,f60,f61",
                    "klt": 101,
                    "fqt": 1,
                    "beg": begin_date.strftime("%Y%m%d"),
                    "end": end_date.strftime("%Y%m%d"),
                    "lmt": row_limit,
                }
            )
            rows = self._validated_rows(
                self._eastmoney_candidates(payload),
                begin_date=begin_date,
                end_date=end_date,
                row_limit=row_limit,
                provider=provider,
            )
        except (ChinaMarketError, MarketDataError) as exc:
            primary_error = exc
            tencent_symbol = self._tencent_symbol(security)
            provider = "tencent-newfqkline"
            try:
                payload = self._fetch(
                    TENCENT_KLINE_ENDPOINT,
                    {
                        "param": "%s,day,%s,%s,%d,qfq"
                        % (
                            tencent_symbol,
                            begin_date.isoformat(),
                            end_date.isoformat(),
                            row_limit,
                        ),
                        "r": int(self._now().timestamp() * 1000),
                    },
                )
                rows = self._validated_rows(
                    self._tencent_candidates(payload, tencent_symbol),
                    begin_date=begin_date,
                    end_date=end_date,
                    row_limit=row_limit,
                    provider=provider,
                )
            except (ChinaMarketError, MarketDataError) as fallback_exc:
                raise MarketDataError(
                    "Chinese daily history failed; Eastmoney primary: %s; "
                    "Tencent fallback: %s" % (exc, fallback_exc)
                ) from fallback_exc
        retrieved_at = datetime.now(timezone.utc).isoformat().replace("+00:00", "Z")
        provenance = {
            "kind": "external",
            "provider": provider,
            "transport": "https",
            "frequency": "1d",
            "adjustment": "forward-adjusted",
            "completed_daily_bars_only": True,
            "volume_unit": "shares",
            "source_volume_unit": "lot",
            "secid": security.secid,
            "symbol": security.symbol,
            "name": security.name,
            "requested_as_of": end_date.isoformat(),
            "first_observation": rows[0]["date"],
            "last_observation": rows[-1]["date"],
            "rows": len(rows),
            "retrieved_at": retrieved_at,
        }
        if primary_error is not None:
            provenance["fallback_from"] = "eastmoney-push2his"
            provenance["fallback_reason"] = str(primary_error)[:500]
        return rows, provenance

    def histories(
        self, securities: Iterable[ResolvedSecurity], *, as_of: str, limit: int = 180
    ) -> Tuple[Dict[str, List[Dict[str, Any]]], Dict[str, Any]]:
        histories: Dict[str, List[Dict[str, Any]]] = {}
        instruments: List[Dict[str, Any]] = []
        for security in securities:
            rows, provenance = self.history(security, as_of=as_of, limit=limit)
            histories[security.symbol] = rows
            instruments.append(provenance)
        providers = sorted(
            {
                str(item.get("provider") or "")
                for item in instruments
                if item.get("provider")
            }
        )
        source_volume_units = sorted(
            {
                str(item.get("source_volume_unit") or "")
                for item in instruments
                if item.get("source_volume_unit")
            }
        )
        summary = {
            "kind": "external",
            "provider": providers[0] if len(providers) == 1 else "china-market-multi-source",
            "providers": providers,
            "transport": "https",
            "frequency": "1d",
            "adjustment": "forward-adjusted",
            "completed_daily_bars_only": True,
            "volume_unit": "shares",
            "source_volume_units": source_volume_units,
            "requested_as_of": as_of,
            "instruments": instruments,
            "rows": sum(len(rows) for rows in histories.values()),
            "retrieved_at": datetime.now(timezone.utc).isoformat().replace("+00:00", "Z"),
        }
        if len(source_volume_units) == 1:
            summary["source_volume_unit"] = source_volume_units[0]
        return histories, summary


def resolve_input_securities(
    input_data: Mapping[str, Any], resolver: ChinaSecurityResolver
) -> Tuple[List[ResolvedSecurity], List[str]]:
    """Resolve explicit symbols, or extract Chinese names from the question."""

    raw = input_data.get("symbols")
    if raw is None:
        raw = input_data.get("tickers")
    if raw is None:
        raw = input_data.get("symbol", input_data.get("ticker"))
    if raw is None:
        question = input_data.get("question", "")
        candidates = extract_chinese_security_queries(question if isinstance(question, str) else "")
    elif isinstance(raw, str):
        candidates = [raw]
    elif isinstance(raw, list) and all(isinstance(item, str) for item in raw):
        candidates = list(raw)
    else:
        raise SecurityResolutionError("symbols/tickers must be a string or string array")
    if not candidates:
        return [], []
    if len(candidates) > 50:
        raise SecurityResolutionError("between 1 and 50 symbols are allowed")
    return resolver.resolve_many(candidates), candidates
