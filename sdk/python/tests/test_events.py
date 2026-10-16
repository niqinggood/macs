import io
import json
import time
import unittest

from macs_agentos import ProtocolError, TransportError, iter_sse


class EventParserTests(unittest.TestCase):
    def test_parses_multiline_data_id_and_retry(self) -> None:
        stream = io.BytesIO(
            b": heartbeat\n"
            b"id: 42\n"
            b"event: progress\n"
            b"retry: 1500\n"
            b'data: {"message":\n'
            b'data: "working"}\n\n'
        )

        events = list(iter_sse(stream))

        self.assertEqual(len(events), 1)
        self.assertEqual(events[0].id, "42")
        self.assertEqual(events[0].type, "progress")
        self.assertEqual(events[0].retry, 1.5)
        self.assertEqual(json.loads(events[0].data)["message"], "working")

    def test_rejects_oversized_line(self) -> None:
        with self.assertRaises(ProtocolError):
            list(iter_sse(io.BytesIO(b"data: 123456\n\n"), max_line_bytes=5))

    def test_honors_overall_deadline(self) -> None:
        with self.assertRaises(TransportError):
            list(
                iter_sse(
                    io.BytesIO(b": heartbeat\n"),
                    deadline=time.monotonic() - 1,
                )
            )


if __name__ == "__main__":
    unittest.main()
