"""Exceptions raised by the MACS AgentOS client."""

from __future__ import annotations

from dataclasses import dataclass


class MACSError(Exception):
    """Base class for all SDK errors."""


class TransportError(MACSError):
    """The request could not reach the server or the connection was lost."""


class ProtocolError(MACSError):
    """The server returned a response that violates the AgentOS contract."""


class WaitTimeoutError(MACSError):
    """A task did not reach a durable terminal state before the deadline."""


@dataclass(eq=False)
class APIError(MACSError):
    """A structured non-2xx response returned by AgentOS."""

    status_code: int
    code: str = ""
    message: str = ""
    request_id: str = ""
    retryable: bool = False
    retry_after: float | None = None

    def __str__(self) -> str:
        message = self.message or "AgentOS request failed"
        if self.code:
            return f"MACS AgentOS: {message} ({self.code}, HTTP {self.status_code})"
        return f"MACS AgentOS: {message} (HTTP {self.status_code})"
