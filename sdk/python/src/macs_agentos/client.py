"""Transport-only client for the MACS AgentOS API."""

from __future__ import annotations

import json
import socket
import time
from collections.abc import Iterator, Mapping
from email.message import Message
from http.client import HTTPResponse
from typing import Any, Callable
from urllib.error import HTTPError, URLError
from urllib.parse import quote, urlencode, urljoin, urlsplit, urlunsplit
from urllib.request import OpenerDirector, Request, build_opener

from .errors import (
    APIError,
    MACSError,
    ProtocolError,
    TransportError,
    WaitTimeoutError,
)
from .events import Event, iter_sse

MAX_JSON_RESPONSE_BYTES = 16 << 20
TERMINAL_TASK_STATUSES = frozenset({"completed", "degraded", "failed", "stopped"})

EventCallback = Callable[[Event], None]
PayloadCallback = Callable[[dict[str, Any]], None]


class Client:
    """Call a MACS AgentOS service over HTTP and SSE.

    The client deliberately contains no Agent runtime. ``bearer_token`` is for
    a service-client credential and ``api_key`` is for a control-plane key;
    configure at most one of them.
    """

    def __init__(
        self,
        base_url: str,
        *,
        bearer_token: str = "",
        api_key: str = "",
        act_as_user_id: str = "",
        timeout: float | None = 30.0,
        sse_timeout: float | None = None,
        user_agent: str = "macs-sdk-python/preview",
        opener: OpenerDirector | None = None,
    ) -> None:
        self._base_url = _normalize_base_url(base_url)
        self._bearer_token = bearer_token.strip()
        self._api_key = api_key.strip()
        self._act_as_user_id = act_as_user_id.strip()
        self._timeout = _normalize_timeout(timeout, "timeout")
        self._sse_timeout = _normalize_timeout(sse_timeout, "sse_timeout")
        self._user_agent = user_agent.strip()
        self._opener = opener or build_opener()
        if self._bearer_token and self._api_key:
            raise ValueError(
                "MACS SDK: configure either bearer_token or api_key, not both"
            )

    def list_roles(self) -> dict[str, Any]:
        """Return Agent roles visible to the configured identity."""

        return self._json_request("GET", "api/v1/agent/roles")

    def submit_task(
        self,
        request: Mapping[str, Any],
        *,
        idempotency_key: str = "",
    ) -> dict[str, Any]:
        """Create a durable task and return its accepted snapshot."""

        headers = {}
        if idempotency_key.strip():
            headers["Idempotency-Key"] = idempotency_key.strip()
        return self._json_request(
            "POST",
            "api/v1/agent/tasks",
            body=dict(request),
            headers=headers,
        )

    def get_task(self, task_id: str) -> dict[str, Any]:
        """Return the current durable task snapshot."""

        return self._json_request("GET", _task_path(task_id))

    def list_tasks(
        self,
        *,
        caller_id: str = "",
        role_id: str = "",
        status: str = "",
        cursor: str = "",
        limit: int | None = None,
    ) -> dict[str, Any]:
        """Return one cursor page of tasks."""

        query: dict[str, Any] = {}
        if caller_id.strip():
            query["caller_id"] = caller_id.strip()
        if role_id.strip():
            query["role_id"] = role_id.strip()
        if status.strip():
            query["status"] = status.strip()
        if cursor.strip():
            query["cursor"] = cursor.strip()
        if limit is not None:
            if limit < 1 or limit > 100:
                raise ValueError("MACS SDK: limit must be between 1 and 100")
            query["limit"] = str(limit)
        path = "api/v1/agent/tasks"
        if query:
            path += "?" + urlencode(query)
        return self._json_request("GET", path)

    def cancel_task(self, task_id: str) -> dict[str, Any]:
        """Request cancellation of a non-terminal task."""

        return self._json_request("POST", _task_path(task_id, "cancel"), body={})

    def list_approvals(self, task_id: str) -> dict[str, Any]:
        """Return approval records associated with a task."""

        return self._json_request("GET", _task_path(task_id, "approvals"))

    def approve_task(
        self, task_id: str, approval_id: str, *, reason: str = ""
    ) -> dict[str, Any]:
        """Approve one suspended tool action."""

        return self._decide_approval(task_id, approval_id, "approve", reason)

    def deny_task(
        self, task_id: str, approval_id: str, *, reason: str = ""
    ) -> dict[str, Any]:
        """Deny one suspended tool action."""

        return self._decide_approval(task_id, approval_id, "deny", reason)

    def watch_task(
        self,
        task_id: str,
        *,
        client_id: str = "",
        last_event_id: str = "",
        timeout: float | None = None,
    ) -> Iterator[Event]:
        """Yield low-latency events from one task's SSE stream.

        The stream is a bounded notification window, not the durable source of
        truth. Call :meth:`get_task` after disconnection or use
        :meth:`wait_task` for automatic polling fallback.
        """

        path = _task_path(task_id, "events")
        if client_id.strip():
            path += "?" + urlencode({"client_id": client_id.strip()})
        headers = {
            "Accept": "text/event-stream",
            "Cache-Control": "no-cache",
        }
        if last_event_id.strip():
            headers["Last-Event-ID"] = last_event_id.strip()
        stream_timeout = _normalize_timeout(timeout, "timeout")
        deadline = None if stream_timeout is None else time.monotonic() + stream_timeout
        io_timeout = _minimum_timeout(self._sse_timeout, stream_timeout)
        response = self._open("GET", path, headers=headers, timeout=io_timeout)
        try:
            content_type = response.headers.get("Content-Type", "")
            media_type = content_type.split(";", 1)[0].strip().lower()
            if media_type != "text/event-stream":
                raise ProtocolError(
                    "MACS SDK: task event endpoint returned content type "
                    f"{content_type!r}"
                )
            try:
                yield from iter_sse(response, deadline=deadline)
            except (socket.timeout, OSError, TimeoutError) as exc:
                raise TransportError(
                    f"MACS SDK: read task event stream: {exc}"
                ) from exc
        finally:
            response.close()

    def wait_task(
        self,
        task_id: str,
        *,
        poll_interval: float = 1.0,
        timeout: float | None = None,
        client_id: str = "",
        on_event: EventCallback | None = None,
        on_progress: PayloadCallback | None = None,
        on_snapshot: PayloadCallback | None = None,
        disable_events: bool = False,
    ) -> dict[str, Any]:
        """Wait until the durable task snapshot reaches a terminal state.

        SSE is used for latency. If it is unavailable or disconnects, this
        method polls the task endpoint; the final value always comes from the
        durable snapshot endpoint.
        """

        if poll_interval <= 0:
            raise ValueError("MACS SDK: poll_interval must be positive")
        wait_timeout = _normalize_timeout(timeout, "timeout")
        deadline = None if wait_timeout is None else time.monotonic() + wait_timeout

        current = self.get_task(task_id)
        _call_payload_callback(on_snapshot, current)
        if task_is_terminal(current):
            return current

        if not disable_events:
            remaining = (
                None if deadline is None else max(0.001, deadline - time.monotonic())
            )
            events = self.watch_task(
                task_id,
                client_id=client_id,
                timeout=remaining,
            )
            try:
                while True:
                    try:
                        event = next(events)
                        progress = event.progress()
                        snapshot = event.task_snapshot()
                    except StopIteration:
                        break
                    except (TransportError, ProtocolError):
                        # The durable endpoint remains available even when the
                        # notification stream is malformed or disconnected.
                        break
                    if on_event is not None:
                        on_event(event)
                    if progress is not None:
                        _call_payload_callback(on_progress, progress)
                    if snapshot is not None:
                        _call_payload_callback(on_snapshot, snapshot)
                        if task_is_terminal(snapshot):
                            break
                    if deadline is not None and time.monotonic() >= deadline:
                        raise WaitTimeoutError(
                            f"MACS SDK: timed out waiting for task {task_id!r}"
                        )
            finally:
                events.close()

        while True:
            if deadline is not None and time.monotonic() >= deadline:
                raise WaitTimeoutError(
                    f"MACS SDK: timed out waiting for task {task_id!r}"
                )
            current = self.get_task(task_id)
            _call_payload_callback(on_snapshot, current)
            if task_is_terminal(current):
                return current
            sleep_for = poll_interval
            if deadline is not None:
                sleep_for = min(sleep_for, max(0.0, deadline - time.monotonic()))
            time.sleep(sleep_for)

    def _decide_approval(
        self,
        task_id: str,
        approval_id: str,
        action: str,
        reason: str,
    ) -> dict[str, Any]:
        approval_id = approval_id.strip()
        if not approval_id:
            raise ValueError("MACS SDK: approval_id is required")
        path = (
            _task_path(task_id, "approvals")
            + "/"
            + quote(approval_id, safe="")
            + "/"
            + action
        )
        body = {"reason": reason.strip()} if reason.strip() else {}
        return self._json_request("POST", path, body=body)

    def _json_request(
        self,
        method: str,
        path: str,
        *,
        body: Any | None = None,
        headers: Mapping[str, str] | None = None,
    ) -> dict[str, Any]:
        request_headers = {"Accept": "application/json"}
        encoded: bytes | None = None
        if body is not None:
            try:
                encoded = json.dumps(
                    body, ensure_ascii=False, separators=(",", ":")
                ).encode("utf-8")
            except (TypeError, ValueError) as exc:
                raise ValueError(f"MACS SDK: encode request: {exc}") from exc
            request_headers["Content-Type"] = "application/json"
        if headers:
            request_headers.update(headers)

        response = self._open(
            method,
            path,
            data=encoded,
            headers=request_headers,
            timeout=self._timeout,
        )
        try:
            raw = _read_limited(response, MAX_JSON_RESPONSE_BYTES)
            if not raw:
                return {}
            try:
                value = json.loads(raw.decode("utf-8"))
            except (UnicodeDecodeError, ValueError) as exc:
                raise ProtocolError("MACS SDK: AgentOS returned invalid JSON") from exc
            if not isinstance(value, dict):
                raise ProtocolError("MACS SDK: AgentOS JSON response must be an object")
            return value
        finally:
            response.close()

    def _open(
        self,
        method: str,
        path: str,
        *,
        data: bytes | None = None,
        headers: Mapping[str, str] | None = None,
        timeout: float | None,
    ) -> HTTPResponse:
        request_headers = self._default_headers()
        if headers:
            request_headers.update(headers)
        request = Request(
            self._resolve(path),
            data=data,
            headers=request_headers,
            method=method,
        )
        try:
            response = self._opener.open(request, timeout=timeout)
        except HTTPError as exc:
            try:
                api_error = _decode_api_error(exc)
            finally:
                exc.close()
            raise api_error from None
        except (URLError, OSError, TimeoutError) as exc:
            reason = getattr(exc, "reason", exc)
            raise TransportError(
                f"MACS SDK: {method} {request.full_url}: {reason}"
            ) from exc
        status = getattr(response, "status", response.getcode())
        if status < 200 or status >= 300:
            try:
                raise _decode_api_error(response)
            finally:
                response.close()
        return response

    def _default_headers(self) -> dict[str, str]:
        headers: dict[str, str] = {}
        if self._bearer_token:
            headers["Authorization"] = "Bearer " + self._bearer_token
        if self._api_key:
            headers["X-API-Key"] = self._api_key
        if self._act_as_user_id:
            headers["X-OpenKitty-User-ID"] = self._act_as_user_id
        if self._user_agent:
            headers["User-Agent"] = self._user_agent
        return headers

    def _resolve(self, path: str) -> str:
        parsed = urlsplit(path)
        if parsed.scheme or parsed.netloc or path.startswith("/"):
            raise ValueError("MACS SDK: request path must be relative")
        return urljoin(self._base_url, path)


def task_is_terminal(task: Mapping[str, Any]) -> bool:
    """Return whether a durable task snapshot is terminal."""

    return str(task.get("status", "")).strip().lower() in TERMINAL_TASK_STATUSES


def _task_path(task_id: str, action: str = "") -> str:
    task_id = task_id.strip()
    if not task_id:
        raise ValueError("MACS SDK: task_id is required")
    path = "api/v1/agent/tasks/" + quote(task_id, safe="")
    if action:
        path += "/" + quote(action, safe="")
    return path


def _normalize_base_url(value: str) -> str:
    value = value.strip()
    parsed = urlsplit(value)
    if parsed.scheme not in {"http", "https"}:
        raise ValueError("MACS SDK: base_url must use http or https")
    if not parsed.netloc or parsed.username is not None or parsed.password is not None:
        raise ValueError(
            "MACS SDK: base_url must contain a host and no embedded credentials"
        )
    if parsed.query or parsed.fragment:
        raise ValueError(
            "MACS SDK: base_url must not contain a query string or fragment"
        )
    path = parsed.path.rstrip("/") + "/"
    return urlunsplit((parsed.scheme, parsed.netloc, path, "", ""))


def _normalize_timeout(value: float | None, name: str) -> float | None:
    if value is None:
        return None
    normalized = float(value)
    if normalized <= 0:
        raise ValueError(f"MACS SDK: {name} must be positive or None")
    return normalized


def _minimum_timeout(*values: float | None) -> float | None:
    present = [value for value in values if value is not None]
    return min(present) if present else None


def _read_limited(response: Any, limit: int) -> bytes:
    try:
        raw = response.read(limit + 1)
    except (OSError, TimeoutError) as exc:
        raise TransportError(f"MACS SDK: read AgentOS response: {exc}") from exc
    if len(raw) > limit:
        raise ProtocolError(f"MACS SDK: AgentOS response exceeds {limit} bytes")
    return raw


def _decode_api_error(response: Any) -> APIError:
    status = int(getattr(response, "status", getattr(response, "code", 0)) or 0)
    headers: Message = getattr(response, "headers", Message())
    request_id = headers.get("X-Request-ID", "")
    retry_after: float | None = None
    try:
        retry_value = float(headers.get("Retry-After", ""))
        if retry_value > 0:
            retry_after = retry_value
    except (TypeError, ValueError):
        pass

    code = ""
    message = ""
    retryable = False
    try:
        raw = _read_limited(response, MAX_JSON_RESPONSE_BYTES)
        payload = json.loads(raw.decode("utf-8")) if raw else {}
        if isinstance(payload, dict):
            code = str(payload.get("code", "") or "")
            message = str(payload.get("message", "") or payload.get("error", "") or "")
            request_id = str(payload.get("request_id", "") or request_id)
            retryable = bool(payload.get("retryable", False))
    except (MACSError, UnicodeDecodeError, ValueError):
        pass
    return APIError(
        status_code=status,
        code=code,
        message=message,
        request_id=request_id,
        retryable=retryable,
        retry_after=retry_after,
    )


def _call_payload_callback(
    callback: PayloadCallback | None, payload: dict[str, Any]
) -> None:
    if callback is not None:
        callback(payload)
