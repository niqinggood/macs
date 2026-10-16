"""Python preview SDK for the MACS AgentOS API."""

from .client import TERMINAL_TASK_STATUSES, Client, task_is_terminal
from .errors import (
    APIError,
    MACSError,
    ProtocolError,
    TransportError,
    WaitTimeoutError,
)
from .events import Event, iter_sse

__all__ = [
    "TERMINAL_TASK_STATUSES",
    "APIError",
    "Client",
    "Event",
    "MACSError",
    "ProtocolError",
    "TransportError",
    "WaitTimeoutError",
    "iter_sse",
    "task_is_terminal",
]

__version__ = "0.1.0.dev0"
