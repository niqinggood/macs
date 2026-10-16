from __future__ import annotations

import io
import json
import unittest
from email.message import Message
from typing import Any, Callable
from urllib.error import HTTPError

from macs_agentos import APIError, Client


class FakeResponse(io.BytesIO):
    def __init__(
        self,
        body: bytes,
        *,
        status: int = 200,
        content_type: str = "application/json",
        headers: Message | None = None,
    ) -> None:
        super().__init__(body)
        self.status = status
        self.code = status
        self.headers = headers or Message()
        if content_type:
            self.headers["Content-Type"] = content_type

    def getcode(self) -> int:
        return self.status


class FakeOpener:
    def __init__(self, responder: Callable[[Any], Any]) -> None:
        self.responder = responder
        self.requests: list[Any] = []

    def open(self, request: Any, timeout: float | None = None) -> Any:
        request.test_timeout = timeout
        self.requests.append(request)
        response = self.responder(request)
        if isinstance(response, Exception):
            raise response
        return response


class ClientTests(unittest.TestCase):
    def test_submit_uses_auth_user_and_idempotency_headers(self) -> None:
        opener = FakeOpener(
            lambda _: FakeResponse(b'{"task_id":"agt_1","status":"pending"}')
        )
        client = Client(
            "https://macs.example.test/base",
            bearer_token="secret",
            act_as_user_id="person-42",
            opener=opener,
        )

        task = client.submit_task(
            {"role_id": "research", "message": "analyze"},
            idempotency_key="order-42",
        )

        self.assertEqual(task["task_id"], "agt_1")
        request = opener.requests[0]
        self.assertEqual(
            request.full_url,
            "https://macs.example.test/base/api/v1/agent/tasks",
        )
        self.assertEqual(request.get_header("Authorization"), "Bearer secret")
        self.assertEqual(request.get_header("X-openkitty-user-id"), "person-42")
        self.assertEqual(request.get_header("Idempotency-key"), "order-42")
        self.assertEqual(json.loads(request.data)["role_id"], "research")

    def test_structured_api_error_is_preserved(self) -> None:
        headers = Message()
        headers["Content-Type"] = "application/json"
        headers["Retry-After"] = "2"
        error = HTTPError(
            "https://macs.example.test/api/v1/agent/tasks/x",
            429,
            "Too Many Requests",
            headers,
            io.BytesIO(
                b'{"code":"rate_limited","message":"slow down",'
                b'"request_id":"req_1","retryable":true}'
            ),
        )
        client = Client("https://macs.example.test", opener=FakeOpener(lambda _: error))

        with self.assertRaises(APIError) as raised:
            client.get_task("agt_1")

        self.assertEqual(raised.exception.status_code, 429)
        self.assertEqual(raised.exception.code, "rate_limited")
        self.assertEqual(raised.exception.request_id, "req_1")
        self.assertTrue(raised.exception.retryable)
        self.assertEqual(raised.exception.retry_after, 2.0)

    def test_wait_uses_sse_then_durable_polling(self) -> None:
        snapshots = [
            b'{"task_id":"agt_1","status":"running"}',
            b'{"task_id":"agt_1","status":"completed","result":"ok"}',
        ]

        def respond(request: Any) -> FakeResponse:
            if request.full_url.endswith("/events"):
                return FakeResponse(
                    b"event: progress\n"
                    b'data: {"task_id":"agt_1","message":"working"}\n\n',
                    content_type="text/event-stream; charset=utf-8",
                )
            return FakeResponse(snapshots.pop(0))

        progress = []
        client = Client("https://macs.example.test", opener=FakeOpener(respond))

        final = client.wait_task(
            "agt_1",
            poll_interval=0.001,
            timeout=1,
            on_progress=progress.append,
        )

        self.assertEqual(final["status"], "completed")
        self.assertEqual(progress[0]["message"], "working")

    def test_approval_path_escapes_untrusted_ids(self) -> None:
        opener = FakeOpener(lambda _: FakeResponse(b'{"task":{"status":"pending"}}'))
        client = Client("https://macs.example.test", opener=opener)

        client.approve_task("agt/1", "approval/1", reason="change CR-42 approved")

        request = opener.requests[0]
        self.assertEqual(
            request.full_url,
            "https://macs.example.test/api/v1/agent/tasks/agt%2F1/approvals/approval%2F1/approve",
        )
        self.assertEqual(json.loads(request.data), {"reason": "change CR-42 approved"})

    def test_callback_errors_are_not_hidden_as_sse_fallback(self) -> None:
        snapshots = [b'{"task_id":"agt_1","status":"running"}']

        def respond(request: Any) -> FakeResponse:
            if request.full_url.endswith("/events"):
                return FakeResponse(
                    b'event: progress\ndata: {"task_id":"agt_1"}\n\n',
                    content_type="text/event-stream",
                )
            return FakeResponse(snapshots.pop(0))

        client = Client("https://macs.example.test", opener=FakeOpener(respond))

        with self.assertRaisesRegex(RuntimeError, "stop callback"):
            client.wait_task(
                "agt_1",
                timeout=1,
                on_progress=lambda _: (_ for _ in ()).throw(
                    RuntimeError("stop callback")
                ),
            )

    def test_rejects_ambiguous_credentials(self) -> None:
        with self.assertRaises(ValueError):
            Client(
                "https://macs.example.test",
                bearer_token="service-token",
                api_key="control-key",
            )


if __name__ == "__main__":
    unittest.main()
