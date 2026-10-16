import contextlib
import hashlib
import io
import json
import os
import tempfile
import threading
import unittest
import urllib.error
from unittest import mock

import chatroom_agent


def action_results(output):
    return [
        json.loads(line.removeprefix(chatroom_agent.ACTION_RESULT_PREFIX))
        for line in output.splitlines()
        if line.startswith(chatroom_agent.ACTION_RESULT_PREFIX)
    ]


class ChatroomActionResultTests(unittest.TestCase):
    def test_post_emits_human_and_machine_readable_success(self):
        content = "今日微博热点\n\n这里是正文。"
        response = {
            "status": "success",
            "shareUrl": "https://www.novaexel.com/nova/chatroom/posts/2527",
            "post": {
                "id": 2527,
                "topicId": 14,
                "author": "openkitty-researcher",
            },
        }
        stdout = io.StringIO()

        with mock.patch.object(chatroom_agent, "request_json", return_value=response):
            with contextlib.redirect_stdout(stdout):
                result = chatroom_agent.create_post(14, content, api_key="test-key")

        self.assertEqual(result["id"], 2527)
        self.assertIn("Posted! ID: 2527", stdout.getvalue())
        envelopes = action_results(stdout.getvalue())
        self.assertEqual(len(envelopes), 1)
        self.assertEqual(
            envelopes[0],
            {
                "ok": True,
                "action": "post",
                "result_id": 2527,
                "topic_id": 14,
                "share_url": "https://www.novaexel.com/nova/chatroom/posts/2527",
                "content_sha256": hashlib.sha256(content.encode("utf-8")).hexdigest(),
                "robot_id": "openkitty-researcher",
            },
        )

    def test_reply_emits_reply_action(self):
        content = "同意，并补充一点。"
        response = {
            "ok": True,
            "post": {
                "id": 2528,
                "topicId": 3,
                "robotId": "openkitty-agent",
            },
        }
        stdout = io.StringIO()

        with mock.patch.object(chatroom_agent, "request_json", return_value=response):
            with contextlib.redirect_stdout(stdout):
                chatroom_agent.create_post(3, content, api_key="test-key", reply_to=2527)

        self.assertIn("Replied! ID: 2528", stdout.getvalue())
        envelope = action_results(stdout.getvalue())[0]
        self.assertTrue(envelope["ok"])
        self.assertEqual(envelope["action"], "reply")
        self.assertEqual(envelope["result_id"], 2528)
        self.assertEqual(envelope["topic_id"], 3)
        self.assertEqual(envelope["robot_id"], "openkitty-agent")

    def test_failed_business_response_never_emits_success(self):
        responses = (
            {"status": "error", "message": "invalid topic"},
            {"ok": False, "post": {"id": 1}},
            {"success": 0, "post": {"id": 1}},
            {"error": "rejected", "post": {"id": 1}},
            {"status": "pending", "post": {"id": 1}},
        )
        for response in responses:
            with self.subTest(response=response):
                stdout = io.StringIO()
                stderr = io.StringIO()
                with mock.patch.object(chatroom_agent, "request_json", return_value=response):
                    with contextlib.redirect_stdout(stdout), contextlib.redirect_stderr(stderr):
                        with self.assertRaises(SystemExit):
                            chatroom_agent.create_post(999, "content", api_key="test-key")

                self.assertEqual(action_results(stdout.getvalue()), [])
                self.assertNotIn('"ok":true', stdout.getvalue().replace(" ", "").lower())
                self.assertIn("did not return a confirmed post", stderr.getvalue())


class ChatroomPreparedPublishTests(unittest.TestCase):
    def _prepare(self, temp_dir, name="post", body="A complete body with verified facts."):
        source_path = os.path.join(temp_dir, name + ".md")
        prepared_path = os.path.join(temp_dir, name + ".prepared.json")
        with open(source_path, "w", encoding="utf-8") as handle:
            handle.write("Daily market brief\n\n" + body + "\n")
        stdout = io.StringIO()
        with contextlib.redirect_stdout(stdout):
            chatroom_agent.prepare_post(2, "@" + source_path, prepared_path)
        self.assertIn(chatroom_agent.PREPARED_RESULT_PREFIX, stdout.getvalue())
        return prepared_path

    def test_prepare_is_local_and_freezes_content_hash(self):
        with tempfile.TemporaryDirectory() as temp_dir:
            with mock.patch.object(chatroom_agent, "request_json") as request:
                prepared_path = self._prepare(temp_dir)

            request.assert_not_called()
            with open(prepared_path, "r", encoding="utf-8") as handle:
                payload = json.load(handle)
            self.assertEqual(payload["action"], "post")
            self.assertEqual(payload["topic_id"], 2)
            self.assertEqual(
                payload["content_sha256"],
                hashlib.sha256(payload["content"].encode("utf-8")).hexdigest(),
            )

    def test_publish_replays_receipt_without_a_second_network_request(self):
        response = {
            "status": "success",
            "shareUrl": "https://www.novaexel.com/nova/chatroom/posts/3001",
            "post": {"id": 3001, "topicId": 2, "author": "openkitty-test"},
        }
        with tempfile.TemporaryDirectory() as temp_dir:
            prepared_path = self._prepare(temp_dir)
            stdout = io.StringIO()
            with mock.patch.object(chatroom_agent, "request_json", return_value=response) as request:
                with contextlib.redirect_stdout(stdout):
                    first = chatroom_agent.publish_prepared(
                        prepared_path,
                        "web_session123-t1",
                        api_key="test-key",
                    )
                    second = chatroom_agent.publish_prepared(
                        prepared_path,
                        "web_session123-t1",
                        api_key="test-key",
                    )

            self.assertEqual(request.call_count, 1)
            self.assertEqual(first, second)
            self.assertEqual(len(action_results(stdout.getvalue())), 2)
            self.assertIn("no second network request", stdout.getvalue())

    def test_intent_cannot_be_reused_for_a_different_payload(self):
        response = {
            "status": "success",
            "post": {"id": 3002, "topicId": 2, "author": "openkitty-test"},
        }
        with tempfile.TemporaryDirectory() as temp_dir:
            first_path = self._prepare(temp_dir, "first", "First verified final body.")
            second_path = self._prepare(temp_dir, "second", "Different replacement body.")
            state_dir = os.path.join(temp_dir, "shared-intents")
            stderr = io.StringIO()
            with mock.patch.object(chatroom_agent, "request_json", return_value=response) as request:
                with contextlib.redirect_stdout(io.StringIO()):
                    chatroom_agent.publish_prepared(
                        first_path,
                        "same-task",
                        api_key="test-key",
                        state_dir=state_dir,
                    )
                with contextlib.redirect_stderr(stderr):
                    with self.assertRaises(SystemExit):
                        chatroom_agent.publish_prepared(
                            second_path,
                            "same-task",
                            api_key="test-key",
                            state_dir=state_dir,
                        )

            self.assertEqual(request.call_count, 1)
            self.assertIn("already bound", stderr.getvalue())

    def test_uncertain_live_attempt_is_not_retried_automatically(self):
        with tempfile.TemporaryDirectory() as temp_dir:
            prepared_path = self._prepare(temp_dir)
            with mock.patch.object(chatroom_agent, "request_json", side_effect=SystemExit(1)) as request:
                with self.assertRaises(SystemExit):
                    chatroom_agent.publish_prepared(
                        prepared_path,
                        "failed-task",
                        api_key="test-key",
                    )
                with self.assertRaises(SystemExit):
                    chatroom_agent.publish_prepared(
                        prepared_path,
                        "failed-task",
                        api_key="test-key",
                    )

            self.assertEqual(request.call_count, 1)

    def test_confirmed_non_delivery_can_retry_same_frozen_intent(self):
        response = {
            "status": "success",
            "post": {"id": 3011, "topicId": 2, "author": "openkitty-test"},
        }
        with tempfile.TemporaryDirectory() as temp_dir:
            prepared_path = self._prepare(temp_dir)
            failure = chatroom_agent.PublishRequestError(
                "ERROR: connection refused before delivery",
                retry_safe=True,
            )
            with mock.patch.object(
                chatroom_agent,
                "request_json",
                side_effect=[failure, response],
            ) as request:
                with self.assertRaises(SystemExit):
                    chatroom_agent.publish_prepared(
                        prepared_path,
                        "retryable-task",
                        api_key="test-key",
                    )
                with contextlib.redirect_stdout(io.StringIO()):
                    receipt = chatroom_agent.publish_prepared(
                        prepared_path,
                        "retryable-task",
                        api_key="test-key",
                    )

            self.assertEqual(request.call_count, 2)
            self.assertEqual(receipt["result_id"], 3011)

    def test_network_delivery_classifier_keeps_timeouts_uncertain(self):
        self.assertTrue(
            chatroom_agent._network_failure_is_confirmed_before_delivery(
                ConnectionRefusedError("refused")
            )
        )
        self.assertFalse(
            chatroom_agent._network_failure_is_confirmed_before_delivery(
                TimeoutError("timed out")
            )
        )

    def test_http_rejection_classification_distinguishes_4xx_and_5xx(self):
        for status, retry_safe in ((401, True), (429, True), (500, False)):
            error = urllib.error.HTTPError(
                "https://example.invalid/posts",
                status,
                "failure",
                {},
                io.BytesIO(b'{"error":"rejected"}'),
            )
            with self.subTest(status=status):
                with mock.patch.object(
                    chatroom_agent.urllib.request,
                    "urlopen",
                    side_effect=error,
                ):
                    with self.assertRaises(chatroom_agent.PublishRequestError) as raised:
                        chatroom_agent.request_json(
                            "https://example.invalid/posts",
                            method="POST",
                            payload={"content": "frozen"},
                            classify_write_failure=True,
                        )
                self.assertEqual(raised.exception.retry_safe, retry_safe)

    def test_missing_key_does_not_consume_publish_intent(self):
        response = {
            "status": "success",
            "post": {"id": 3010, "topicId": 2, "author": "openkitty-test"},
        }
        with tempfile.TemporaryDirectory() as temp_dir:
            prepared_path = self._prepare(temp_dir)
            state_dir = os.path.join(temp_dir, "publish-intents")
            with mock.patch.object(chatroom_agent, "load_key", return_value=None):
                with self.assertRaises(SystemExit):
                    chatroom_agent.publish_prepared(
                        prepared_path,
                        "key-preflight-task",
                        state_dir=state_dir,
                    )
            self.assertFalse(os.path.exists(os.path.join(state_dir, "key-preflight-task.json")))

            with mock.patch.object(chatroom_agent, "load_key", return_value="saved-key"):
                with mock.patch.object(chatroom_agent, "request_json", return_value=response) as request:
                    with contextlib.redirect_stdout(io.StringIO()):
                        receipt = chatroom_agent.publish_prepared(
                            prepared_path,
                            "key-preflight-task",
                            state_dir=state_dir,
                        )

            self.assertEqual(receipt["result_id"], 3010)
            self.assertEqual(request.call_count, 1)

    def test_concurrent_publishers_share_one_live_attempt(self):
        response = {
            "status": "success",
            "post": {"id": 3003, "topicId": 2, "author": "openkitty-test"},
        }
        started = threading.Event()
        release = threading.Event()

        def delayed_response(*_args, **_kwargs):
            started.set()
            release.wait(timeout=2)
            return response

        with tempfile.TemporaryDirectory() as temp_dir:
            prepared_path = self._prepare(temp_dir)
            with mock.patch.object(chatroom_agent, "request_json", side_effect=delayed_response) as request:
                result = []
                error = []

                def first_publisher():
                    try:
                        result.append(
                            chatroom_agent.publish_prepared(
                                prepared_path,
                                "concurrent-task",
                                api_key="test-key",
                            )
                        )
                    except BaseException as exc:  # keep thread failures observable
                        error.append(exc)

                worker = threading.Thread(target=first_publisher)
                worker.start()
                self.assertTrue(started.wait(timeout=2))
                with self.assertRaises(SystemExit):
                    chatroom_agent.publish_prepared(
                        prepared_path,
                        "concurrent-task",
                        api_key="test-key",
                    )
                release.set()
                worker.join(timeout=2)

            self.assertFalse(worker.is_alive())
            self.assertEqual(error, [])
            self.assertEqual(result[0]["result_id"], 3003)
            self.assertEqual(request.call_count, 1)


class ChatroomSourceSearchTests(unittest.TestCase):
    def test_full_instruction_is_reduced_and_paginated_source_is_frozen(self):
        calls = []
        post = {
            "id": 9001,
            "topicId": 3,
            "author": "research-agent",
            "createdAt": "2026-07-09T10:00:00+08:00",
            "contentType": "markdown",
            "content": "# 百度的『掉队』复盘\n\n这是从真实聊天室返回的完整原帖内容。" * 20,
        }

        def fake_request(url, **_kwargs):
            calls.append(url)
            if url.endswith("/topics"):
                return {"topics": [{"id": 3, "postCount": 200}]}
            if "page=1" in url:
                return {"posts": [{"id": 8999, "topicId": 3, "content": "unrelated"}]}
            if "page=2" in url:
                return {"posts": [post]}
            if url.endswith("/posts/9001"):
                return {"post": post}
            raise AssertionError(f"unexpected URL: {url}")

        with tempfile.TemporaryDirectory() as temp_dir:
            output_path = os.path.join(temp_dir, "source.json")
            stdout = io.StringIO()
            with mock.patch.object(chatroom_agent, "request_json", side_effect=fake_request):
                with contextlib.redirect_stdout(stdout):
                    payload = chatroom_agent.search_posts(
                        "chatroom 有一个关于百度掉队的帖子，希望你给我提取内容整理成公众号文章",
                        max_results=3,
                        max_pages=6,
                        output=output_path,
                    )

            self.assertEqual(payload["matches"][0]["post_id"], 9001)
            self.assertTrue(os.path.isfile(output_path))
            with open(output_path, "r", encoding="utf-8") as handle:
                frozen = json.load(handle)
            self.assertEqual(frozen["matches"][0]["content"], post["content"])
            receipts = [
                json.loads(line.removeprefix(chatroom_agent.SOURCE_RESULT_PREFIX))
                for line in stdout.getvalue().splitlines()
                if line.startswith(chatroom_agent.SOURCE_RESULT_PREFIX)
            ]
            self.assertEqual(receipts[0]["match_count"], len(frozen["matches"]))
            with open(output_path, "rb") as handle:
                self.assertEqual(receipts[0]["bundle_sha256"], hashlib.sha256(handle.read()).hexdigest())
            page_calls = [url for url in calls if "/posts?" in url]
            self.assertEqual(len(page_calls), 2, "high-confidence match should stop pagination immediately")
            self.assertTrue(all("limit=50" in url for url in page_calls), page_calls)
            self.assertIn(f"{chatroom_agent.BASE}/posts/9001", calls, "selected list item must be refetched as full detail")


if __name__ == "__main__":
    unittest.main()
