"""Framework-neutral adapter protocols and non-networked mocks."""

from __future__ import annotations

from dataclasses import dataclass
from typing import Any, Protocol


@dataclass(frozen=True)
class VoiceCheckResult:
    responded: bool | None
    intent: str
    confidence: float
    service_status: str


class VoiceAdapter(Protocol):
    def request_check(self, person_id: str, prompt: str, timeout_sec: int) -> str: ...

    def get_result(self, check_id: str) -> VoiceCheckResult: ...


class MultimodalAdapter(Protocol):
    def analyze(self, sanitized_payload: dict[str, Any]) -> list[dict[str, Any]]: ...


class AlertAdapter(Protocol):
    def create_alert(self, assessment: dict[str, Any], idempotency_key: str) -> str: ...

    def acknowledge(self, alert_id: str, actor: str, outcome: str) -> str: ...


class MockVoiceAdapter:
    """In-memory voice adapter for demos. It never records or sends audio."""

    def __init__(self, result: VoiceCheckResult | None = None) -> None:
        self.result = result or VoiceCheckResult(None, "unknown", 0.0, "timeout")
        self.requests: dict[str, dict[str, Any]] = {}

    def request_check(self, person_id: str, prompt: str, timeout_sec: int) -> str:
        check_id = f"voice-check-{len(self.requests) + 1}"
        self.requests[check_id] = {
            "person_id": person_id,
            "prompt": prompt,
            "timeout_sec": timeout_sec,
        }
        return check_id

    def get_result(self, check_id: str) -> VoiceCheckResult:
        if check_id not in self.requests:
            raise KeyError(f"unknown voice check: {check_id}")
        return self.result


class MockMultimodalAdapter:
    """Returns scripted normalized observations without making a cloud call."""

    def __init__(self, observations: list[dict[str, Any]] | None = None) -> None:
        self.observations = observations or []
        self.calls: list[dict[str, Any]] = []

    def analyze(self, sanitized_payload: dict[str, Any]) -> list[dict[str, Any]]:
        self.calls.append(sanitized_payload)
        return list(self.observations)


class RecordingAlertAdapter:
    """Records intended alerts for tests; it never contacts a person or service."""

    def __init__(self) -> None:
        self.alerts: dict[str, dict[str, Any]] = {}

    def create_alert(self, assessment: dict[str, Any], idempotency_key: str) -> str:
        for alert_id, record in self.alerts.items():
            if record["idempotency_key"] == idempotency_key:
                return alert_id
        alert_id = f"mock-alert-{len(self.alerts) + 1}"
        self.alerts[alert_id] = {
            "assessment": assessment,
            "idempotency_key": idempotency_key,
            "status": "created",
        }
        return alert_id

    def acknowledge(self, alert_id: str, actor: str, outcome: str) -> str:
        record = self.alerts[alert_id]
        record.update({"status": "acknowledged", "actor": actor, "outcome": outcome})
        return "acknowledged"


