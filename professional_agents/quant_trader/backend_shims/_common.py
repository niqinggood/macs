"""Shared protocol and safety helpers for quantitative backend shims.

The shims are deliberately one-way research adapters.  They accept analysis
requests, call an upstream research runtime, and normalize the result.  This
module contains no brokerage or order-execution operation.
"""

from __future__ import annotations

import asyncio
from contextlib import redirect_stdout
from dataclasses import asdict, dataclass, is_dataclass
from datetime import datetime, timezone
import importlib
import inspect
import json
import math
import os
from pathlib import Path
import re
import sys
from typing import Any, Callable, Dict, Iterable, List, Mapping, Optional, Sequence, Tuple


BACKEND_PROTOCOL = "openkitty.professional-agent/backend/v1"
ALLOWED_MODES = frozenset({"research", "paper"})
MAX_REQUEST_BYTES = 4 * 1024 * 1024
MAX_SYMBOLS = 50
SYMBOL_PATTERN = re.compile(r"^[A-Za-z0-9][A-Za-z0-9._^:=/\-]{0,31}$")
TRUSTED_MARKET_DATA_PROVIDERS = frozenset(
    {"eastmoney-push2his", "tencent-newfqkline", "china-market-multi-source"}
)
CALLABLE_PATTERN = re.compile(
    r"^[A-Za-z_]\w*(?:\.[A-Za-z_]\w*)*:[A-Za-z_]\w*(?:\.[A-Za-z_]\w*)*$"
)

UNSAFE_TRUE_KEYS = frozenset(
    {
        "allow_live",
        "allow_live_trading",
        "live",
        "live_trading",
        "execute",
        "execute_order",
        "execute_orders",
        "order_submission",
        "place_order",
        "place_orders",
        "submit_order",
        "submit_orders",
    }
)
UNSAFE_PRESENCE_KEYS = frozenset(
    {
        "broker_credentials",
        "broker_order",
        "broker_orders",
        "order_request",
        "orders_to_submit",
    }
)
UNSAFE_ACTION_VALUES = frozenset(
    {
        "broker",
        "execute",
        "execution",
        "live",
        "live-trading",
        "live_trading",
        "place-order",
        "place_order",
        "real",
        "real-money",
        "real_money",
        "submit-order",
        "submit_order",
    }
)

# Defense in depth for upstream code that consults environment flags.
os.environ["OPENKITTY_LIVE_TRADING"] = "0"
os.environ["OPENKITTY_EXECUTION_MODE"] = "research-paper-only"


class RequestRejected(ValueError):
    """The caller did not satisfy the backend protocol or safety contract."""


class BackendUnavailable(RuntimeError):
    """The configured upstream runtime or callable cannot be loaded."""


@dataclass(frozen=True)
class ValidatedRequest:
    backend: str
    request_id: str
    mode: str
    input_data: Dict[str, Any]
    run_dir: Path
    safety: Dict[str, bool]
    market_data_provenance: Dict[str, Any]

    def callable_payload(self) -> Dict[str, Any]:
        return {
            "protocol": BACKEND_PROTOCOL,
            "backend": self.backend,
            "request_id": self.request_id,
            "capability": "analyze",
            "mode": self.mode,
            "input": dict(self.input_data),
            "run_dir": str(self.run_dir),
            "safety": dict(self.safety),
            "market_data_provenance": dict(self.market_data_provenance),
        }


class _CountingWriter:
    """Discard upstream stdout while preserving file-like behavior."""

    encoding = "utf-8"
    errors = "replace"

    def __init__(self) -> None:
        self.count = 0

    @property
    def buffer(self) -> "_CountingWriter":
        return self

    def write(self, value: Any) -> int:
        if isinstance(value, bytes):
            length = len(value)
        else:
            length = len(str(value))
        self.count += length
        return length

    def flush(self) -> None:
        return None

    def isatty(self) -> bool:
        return False

    def writable(self) -> bool:
        return True

    def fileno(self) -> int:
        raise OSError("isolated backend stdout has no file descriptor")


def _normalized_key(value: Any) -> str:
    return str(value).strip().lower().replace("-", "_").replace(" ", "_")


def _contains_unsafe_execution(value: Any, key: str = "", depth: int = 0) -> bool:
    if depth > 24:
        raise RequestRejected("input nesting exceeds the safety validation limit")
    normalized = _normalized_key(key)
    if normalized in UNSAFE_TRUE_KEYS and bool(value):
        return True
    if normalized in UNSAFE_PRESENCE_KEYS and value not in (None, False, "", [], {}):
        return True
    if normalized in {"action", "execution_mode", "mode", "operation"}:
        action = str(value).strip().lower().replace(" ", "-")
        if action in UNSAFE_ACTION_VALUES:
            return True
    if isinstance(value, Mapping):
        return any(
            _contains_unsafe_execution(item, str(item_key), depth + 1)
            for item_key, item in value.items()
        )
    if isinstance(value, (list, tuple)):
        return any(_contains_unsafe_execution(item, "", depth + 1) for item in value)
    return False


def validate_request(raw: Any, expected_backend: str) -> ValidatedRequest:
    if not isinstance(raw, Mapping):
        raise RequestRejected("backend request must be a JSON object")
    if raw.get("protocol") != BACKEND_PROTOCOL:
        raise RequestRejected("unsupported or missing backend protocol")
    if str(raw.get("backend") or "").strip() != expected_backend:
        raise RequestRejected("backend identity mismatch")
    request_id = str(raw.get("request_id") or "").strip()
    if not request_id:
        raise RequestRejected("request_id is required")
    if str(raw.get("capability") or "").strip().lower() != "analyze":
        raise RequestRejected("only the analyze capability is supported")
    mode = str(raw.get("mode") or "").strip().lower()
    if mode not in ALLOWED_MODES:
        raise RequestRejected("only research and paper modes are allowed")
    input_data = raw.get("input")
    if not isinstance(input_data, Mapping):
        raise RequestRejected("input must be a JSON object")
    input_data = dict(input_data)
    nested_mode = str(input_data.get("mode") or mode).strip().lower()
    if nested_mode != mode:
        raise RequestRejected("top-level and input modes must match")
    if _contains_unsafe_execution(input_data):
        raise RequestRejected("live trading and order submission are forbidden")

    safety = raw.get("safety")
    if not isinstance(safety, Mapping):
        raise RequestRejected("safety contract is required")
    expected_safety = {
        "research_only": mode == "research",
        "paper_trading_only": mode == "paper",
        "live_trading": False,
        "order_submission": False,
    }
    for key, expected in expected_safety.items():
        if safety.get(key) is not expected:
            raise RequestRejected("invalid safety contract field: %s" % key)

    run_dir_raw = raw.get("run_dir")
    if not isinstance(run_dir_raw, str) or not run_dir_raw.strip():
        raise RequestRejected("run_dir is required")
    run_dir_candidate = Path(run_dir_raw).expanduser()
    if not run_dir_candidate.is_absolute():
        raise RequestRejected("run_dir must be absolute")
    try:
        run_dir = run_dir_candidate.resolve(strict=True)
    except OSError as exc:
        raise RequestRejected("run_dir must already exist") from exc
    if not run_dir.is_dir():
        raise RequestRejected("run_dir must be a directory")

    raw_provenance = raw.get("market_data_provenance")
    if raw_provenance is None:
        market_data_provenance: Dict[str, Any] = {}
    elif isinstance(raw_provenance, Mapping):
        market_data_provenance = dict(raw_provenance)
        if market_data_provenance.get("kind") != "external":
            raise RequestRejected("market_data_provenance.kind must be external")
        if market_data_provenance.get("provider") not in TRUSTED_MARKET_DATA_PROVIDERS:
            raise RequestRejected("unsupported external market-data provenance provider")
        instruments = market_data_provenance.get("instruments")
        if not isinstance(instruments, list) or not instruments:
            raise RequestRejected("external market-data provenance needs instruments")
    else:
        raise RequestRejected("market_data_provenance must be an object")

    return ValidatedRequest(
        backend=expected_backend,
        request_id=request_id,
        mode=mode,
        input_data=input_data,
        run_dir=run_dir,
        safety=expected_safety,
        market_data_provenance=market_data_provenance,
    )


def ensure_cwd_importable() -> None:
    """Permit a shim launched with an external repository as cwd to import it."""

    cwd = str(Path.cwd().resolve())
    if cwd not in sys.path:
        sys.path.insert(0, cwd)


def extract_symbols(input_data: Mapping[str, Any]) -> List[str]:
    raw = input_data.get("symbols")
    if raw is None:
        raw = input_data.get("tickers")
    if raw is None:
        raw = input_data.get("symbol", input_data.get("ticker"))
    if isinstance(raw, str):
        candidates: Sequence[Any] = [raw]
    elif isinstance(raw, list):
        candidates = raw
    else:
        raise RequestRejected("symbols/tickers must be a string or string array")
    if not candidates or len(candidates) > MAX_SYMBOLS:
        raise RequestRejected("between 1 and %d symbols are required" % MAX_SYMBOLS)
    symbols: List[str] = []
    seen = set()
    for item in candidates:
        if not isinstance(item, str):
            raise RequestRejected("every symbol must be a string")
        symbol = item.strip()
        if not SYMBOL_PATTERN.fullmatch(symbol):
            raise RequestRejected("invalid symbol: %s" % symbol[:40])
        if symbol not in seen:
            symbols.append(symbol)
            seen.add(symbol)
    if not symbols:
        raise RequestRejected("at least one symbol is required")
    return symbols


def extract_date(
    input_data: Mapping[str, Any], keys: Iterable[str], default_today: bool = True
) -> Tuple[str, bool]:
    raw: Any = None
    for key in keys:
        if input_data.get(key) not in (None, ""):
            raw = input_data.get(key)
            break
    defaulted = raw is None
    if defaulted:
        if not default_today:
            raise RequestRejected("an ISO date is required")
        raw = datetime.now(timezone.utc).date().isoformat()
    if not isinstance(raw, str):
        raise RequestRejected("date values must be ISO YYYY-MM-DD strings")
    try:
        parsed = datetime.strptime(raw.strip(), "%Y-%m-%d").date()
    except ValueError as exc:
        raise RequestRejected("date values must use ISO YYYY-MM-DD") from exc
    return parsed.isoformat(), defaulted


def finite_number(
    value: Any, name: str, default: float, minimum: float, maximum: float
) -> float:
    if value in (None, ""):
        result = float(default)
    elif isinstance(value, bool):
        raise RequestRejected("%s must be numeric" % name)
    else:
        try:
            result = float(value)
        except (TypeError, ValueError) as exc:
            raise RequestRejected("%s must be numeric" % name) from exc
    if not math.isfinite(result) or result < minimum or result > maximum:
        raise RequestRejected("%s is outside its allowed range" % name)
    return result


def json_safe(value: Any, depth: int = 0) -> Any:
    if depth > 24:
        return "<maximum serialization depth reached>"
    if value is None or isinstance(value, (str, bool, int)):
        return value
    if isinstance(value, float):
        return value if math.isfinite(value) else str(value)
    if isinstance(value, Path):
        return str(value)
    if is_dataclass(value) and not isinstance(value, type):
        return json_safe(asdict(value), depth + 1)
    model_dump = getattr(value, "model_dump", None)
    if callable(model_dump):
        try:
            return json_safe(model_dump(mode="json"), depth + 1)
        except Exception:
            pass
    if isinstance(value, Mapping):
        return {
            str(key): json_safe(item, depth + 1)
            for key, item in value.items()
        }
    if isinstance(value, (list, tuple, set, frozenset)):
        return [json_safe(item, depth + 1) for item in value]
    return repr(value)


def format_output(value: Any) -> str:
    if isinstance(value, str):
        return value.strip()
    return json.dumps(json_safe(value), ensure_ascii=False, indent=2, sort_keys=True)


def _path_within(path: Path, parent: Path) -> bool:
    try:
        path.relative_to(parent)
        return True
    except ValueError:
        return False


def _normalize_artifacts(
    raw: Any, run_dir: Path
) -> Tuple[List[Dict[str, Any]], List[str]]:
    if raw is None:
        return [], []
    if not isinstance(raw, list):
        return [], ["backend artifacts were discarded because they were not an array"]
    accepted: List[Dict[str, Any]] = []
    warnings: List[str] = []
    for item in raw:
        if isinstance(item, str):
            artifact: Dict[str, Any] = {"path": item}
        elif isinstance(item, Mapping):
            artifact = dict(item)
        else:
            warnings.append("an invalid backend artifact entry was discarded")
            continue
        raw_path = artifact.get("path")
        if not isinstance(raw_path, str) or not raw_path.strip():
            warnings.append("a backend artifact without a path was discarded")
            continue
        candidate = Path(raw_path).expanduser()
        if not candidate.is_absolute():
            candidate = run_dir / candidate
        try:
            resolved = candidate.resolve(strict=True)
        except OSError:
            warnings.append("a missing backend artifact was discarded")
            continue
        if not _path_within(resolved, run_dir) or resolved == run_dir:
            warnings.append("a backend artifact outside run_dir was discarded")
            continue
        if not resolved.is_file() or resolved.stat().st_size <= 0:
            warnings.append("an empty or non-file backend artifact was discarded")
            continue
        normalized = json_safe(artifact)
        assert isinstance(normalized, dict)
        normalized["path"] = resolved.relative_to(run_dir).as_posix()
        accepted.append(normalized)
    return accepted, warnings


def make_response(
    status: str,
    output: str = "",
    data: Optional[Mapping[str, Any]] = None,
    warnings: Optional[Sequence[str]] = None,
    artifacts: Optional[Sequence[Any]] = None,
) -> Dict[str, Any]:
    return {
        "status": status,
        "output": output,
        "data": dict(data or {}),
        "warnings": list(warnings or []),
        "artifacts": list(artifacts or []),
    }


def _normalize_response(raw: Any, request: ValidatedRequest) -> Dict[str, Any]:
    if not isinstance(raw, Mapping):
        raw = make_response(
            "success",
            output=format_output(raw),
            data={"result": json_safe(raw)},
        )
    status = str(raw.get("status") or "").strip().lower()
    status = {"completed": "success", "failed": "error"}.get(status, status)
    if status not in {"success", "degraded", "unavailable", "error"}:
        raise ValueError("backend implementation returned an invalid status")
    output = raw.get("output", "")
    if not isinstance(output, str):
        output = format_output(output)
    data = raw.get("data", {})
    if not isinstance(data, Mapping):
        data = {"result": json_safe(data)}
    else:
        data = json_safe(data)
    assert isinstance(data, dict)
    # The wrapper, not an upstream model/callable, owns the safety statement.
    data["safety"] = {
        "mode": request.mode,
        "live_trading": False,
        "order_submission": False,
    }
    warnings_raw = raw.get("warnings", [])
    if not isinstance(warnings_raw, list):
        warnings_raw = ["backend returned malformed warnings"]
    warnings = [str(item) for item in warnings_raw]
    artifacts, artifact_warnings = _normalize_artifacts(
        raw.get("artifacts", []), request.run_dir
    )
    warnings.extend(artifact_warnings)
    return make_response(
        status,
        output=output,
        data=data,
        warnings=list(dict.fromkeys(warnings)),
        artifacts=artifacts,
    )


def dispatch(
    expected_backend: str,
    raw_request: Any,
    implementation: Callable[[ValidatedRequest], Any],
) -> Dict[str, Any]:
    try:
        request = validate_request(raw_request, expected_backend)
    except RequestRejected as exc:
        return make_response("error", warnings=["request rejected: %s" % exc])

    suppressed = _CountingWriter()
    try:
        with redirect_stdout(suppressed):
            raw_response = implementation(request)
            if inspect.isawaitable(raw_response):
                raw_response = asyncio.run(raw_response)
        response = _normalize_response(raw_response, request)
    except BackendUnavailable as exc:
        response = make_response("unavailable", warnings=[str(exc)])
        response = _normalize_response(response, request)
    except RequestRejected as exc:
        response = make_response("error", warnings=["request rejected: %s" % exc])
        response = _normalize_response(response, request)
    except Exception as exc:
        detail = str(exc).replace("\r", " ").replace("\n", " ").strip()[:500]
        message = "%s backend invocation failed (%s)" % (
            expected_backend,
            type(exc).__name__,
        )
        if detail:
            message += ": " + detail
        response = make_response("error", warnings=[message])
        response = _normalize_response(response, request)
    if suppressed.count:
        response["warnings"] = list(
            dict.fromkeys(
                list(response.get("warnings", []))
                + ["upstream stdout was suppressed to preserve the NDJSON protocol"]
            )
        )
    return response


def load_configured_callable(spec: str) -> Callable[[Mapping[str, Any]], Any]:
    spec = spec.strip()
    if not spec:
        raise BackendUnavailable("QUANT_TRADER_FINGPT_CALLABLE is not configured")
    if not CALLABLE_PATTERN.fullmatch(spec):
        raise BackendUnavailable(
            "QUANT_TRADER_FINGPT_CALLABLE must use module:function syntax"
        )
    module_name, attribute_path = spec.split(":", 1)
    ensure_cwd_importable()
    try:
        value: Any = importlib.import_module(module_name)
        for component in attribute_path.split("."):
            value = getattr(value, component)
    except (ImportError, AttributeError) as exc:
        raise BackendUnavailable("configured FinGPT callable could not be loaded") from exc
    if not callable(value):
        raise BackendUnavailable("configured FinGPT target is not callable")
    return value


def run_ndjson(
    backend: str, handler: Callable[[Any], Mapping[str, Any]]
) -> None:
    """Read one request per line and emit exactly one JSON response per line."""

    for line in sys.stdin:
        if not line.strip():
            continue
        if len(line.encode("utf-8", errors="replace")) > MAX_REQUEST_BYTES:
            response: Mapping[str, Any] = make_response(
                "error", warnings=["request exceeded the backend size limit"]
            )
        else:
            try:
                raw = json.loads(line)
            except json.JSONDecodeError as exc:
                response = make_response(
                    "error", warnings=["invalid backend JSON: %s" % exc]
                )
            else:
                response = handler(raw)
        safe_response = json_safe(response)
        sys.stdout.write(
            json.dumps(
                safe_response,
                ensure_ascii=False,
                allow_nan=False,
                separators=(",", ":"),
            )
            + "\n"
        )
        sys.stdout.flush()
