"""NDJSON adapter for a configured FinGPT inference callable.

FinGPT is a collection of model demos rather than one stable runtime API.
The isolated environment must therefore select a reviewed callable through
``QUANT_TRADER_FINGPT_CALLABLE=module:function``.  The callable receives one
validated research/paper request mapping and must not perform order execution.
"""

from __future__ import annotations

import os
import inspect
from typing import Any, Dict, Mapping

try:
    from ._common import (
        ValidatedRequest,
        dispatch,
        ensure_cwd_importable,
        format_output,
        json_safe,
        load_configured_callable,
        make_response,
        run_ndjson,
    )
except ImportError:  # Direct script execution.
    from _common import (  # type: ignore
        ValidatedRequest,
        dispatch,
        ensure_cwd_importable,
        format_output,
        json_safe,
        load_configured_callable,
        make_response,
        run_ndjson,
    )


BACKEND = "fingpt"


def _normalize_callable_result(result: Any, spec: str) -> Dict[str, Any]:
    # A reviewed integration may already return the backend response contract.
    # A plain model result is wrapped without interpreting or inventing it.
    if isinstance(result, Mapping) and any(
        key in result for key in ("output", "data", "warnings", "artifacts")
    ):
        response = dict(result)
        response.setdefault("status", "success")
        data = response.get("data", {})
        if isinstance(data, Mapping):
            data = dict(data)
            data.setdefault("provider", BACKEND)
            data.setdefault("configured_callable", spec)
            response["data"] = data
        warnings = response.get("warnings", [])
        if not isinstance(warnings, list):
            warnings = [str(warnings)]
        response["warnings"] = list(warnings) + [
            "FinGPT was used for research/paper inference only; no order was submitted."
        ]
        return response

    safe_result = json_safe(result)
    return make_response(
        "success",
        output=format_output(safe_result),
        data={
            "provider": BACKEND,
            "configured_callable": spec,
            "result": safe_result,
        },
        warnings=[
            "FinGPT was used for research/paper inference only; no order was submitted."
        ],
    )


async def _await_callable_result(awaitable: Any, spec: str) -> Dict[str, Any]:
    return _normalize_callable_result(await awaitable, spec)


def _invoke(request: ValidatedRequest) -> Any:
    ensure_cwd_importable()
    spec = os.environ.get("QUANT_TRADER_FINGPT_CALLABLE", "")
    callable_impl = load_configured_callable(spec)
    result = callable_impl(request.callable_payload())
    if inspect.isawaitable(result):
        return _await_callable_result(result, spec)
    return _normalize_callable_result(result, spec)


def handle_request(raw_request: Any) -> Dict[str, Any]:
    return dispatch(BACKEND, raw_request, _invoke)


if __name__ == "__main__":
    run_ndjson(BACKEND, handle_request)
