"""Server-Sent Event parsing for MACS AgentOS task streams."""

from __future__ import annotations

import json
import time
from collections.abc import Iterator
from dataclasses import dataclass
from typing import Any, BinaryIO

from .errors import ProtocolError, TransportError

MAX_SSE_LINE_BYTES = 1 << 20


@dataclass(frozen=True)
class Event:
    """One AgentOS Server-Sent Event."""

    type: str
    data: str
    id: str = ""
    retry: float | None = None

    def json(self) -> Any:
        """Decode the event data as JSON."""

        try:
            return json.loads(self.data)
        except (TypeError, ValueError) as exc:
            raise ProtocolError(
                f"event {self.type!r} does not contain valid JSON"
            ) from exc

    def progress(self) -> dict[str, Any] | None:
        """Return a progress payload, or ``None`` for another event type."""

        if self.type != "progress":
            return None
        value = self.json()
        if not isinstance(value, dict):
            raise ProtocolError("progress event data must be a JSON object")
        return value

    def task_snapshot(self) -> dict[str, Any] | None:
        """Return a durable task snapshot, or ``None`` for another event."""

        if self.type != "task_snapshot":
            return None
        value = self.json()
        if not isinstance(value, dict):
            raise ProtocolError("task_snapshot event data must be a JSON object")
        return value


def _readline(stream: BinaryIO, max_line_bytes: int) -> bytes:
    try:
        raw = stream.readline(max_line_bytes + 1)
    except (OSError, TimeoutError) as exc:
        raise TransportError(f"read AgentOS event stream: {exc}") from exc
    if isinstance(raw, str):
        raw = raw.encode("utf-8")
    if len(raw) > max_line_bytes:
        raise ProtocolError(f"AgentOS event stream line exceeds {max_line_bytes} bytes")
    return raw


def iter_sse(
    stream: BinaryIO,
    *,
    max_line_bytes: int = MAX_SSE_LINE_BYTES,
    deadline: float | None = None,
) -> Iterator[Event]:
    """Parse a binary ``text/event-stream`` response incrementally.

    ``deadline`` is an optional absolute ``time.monotonic()`` value. It lets a
    higher-level wait operation enforce an overall deadline even when the
    server continuously sends heartbeat comments.
    """

    if max_line_bytes < 1:
        raise ValueError("max_line_bytes must be positive")

    event_type = "message"
    event_id = ""
    retry: float | None = None
    data_lines = []

    def dispatch() -> Event | None:
        nonlocal event_type, event_id, retry, data_lines
        if not data_lines:
            event_type = "message"
            retry = None
            return None
        event = Event(
            type=event_type or "message",
            data="\n".join(data_lines),
            id=event_id,
            retry=retry,
        )
        event_type = "message"
        retry = None
        data_lines = []
        return event

    while True:
        if deadline is not None and time.monotonic() >= deadline:
            raise TransportError("AgentOS event stream deadline exceeded")
        raw = _readline(stream, max_line_bytes)
        if raw == b"":
            event = dispatch()
            if event is not None:
                yield event
            return
        try:
            line = raw.rstrip(b"\r\n").decode("utf-8")
        except UnicodeDecodeError as exc:
            raise ProtocolError("AgentOS event stream is not valid UTF-8") from exc

        if line == "":
            event = dispatch()
            if event is not None:
                yield event
            continue
        if line.startswith(":"):
            continue

        field, separator, value = line.partition(":")
        if separator and value.startswith(" "):
            value = value[1:]
        if field == "event":
            if value:
                event_type = value
        elif field == "data":
            data_lines.append(value)
        elif field == "id":
            if "\x00" not in value:
                event_id = value
        elif field == "retry":
            try:
                milliseconds = int(value)
            except ValueError:
                continue
            if milliseconds >= 0:
                retry = milliseconds / 1000.0
