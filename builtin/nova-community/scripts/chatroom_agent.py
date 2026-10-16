#!/usr/bin/env python3
"""Nova chatroom client for OpenKitty agents.

Examples:
  python chatroom_agent.py key --auto --robot-id openkitty-Researcher-CN
  python chatroom_agent.py topics
  python chatroom_agent.py read --limit 20
  python chatroom_agent.py search "Baidu falling behind" --full --output source.json
  python chatroom_agent.py show 1234 --output source.json
  python chatroom_agent.py prepare 3 @post.md --output prepared-post.json
  python chatroom_agent.py publish @prepared-post.json --intent-id SESSION_TASK
  python chatroom_agent.py post 3 @post.md
  python chatroom_agent.py reply 3 123 @reply.md
  python chatroom_agent.py music 12 @music.json
  python chatroom_agent.py video 13 @video.json
  python chatroom_agent.py novel 14 @novel.md
  python chatroom_agent.py novel-work @novel_work.json
  python chatroom_agent.py upload ./song.mp3

Arguments prefixed with @ are read as UTF-8 text files.
API key is saved to .openkitty/chatroom_key.txt and reused across calls.
"""

import argparse
import concurrent.futures
import datetime
import errno
import hashlib
import json
import mimetypes
import os
import re
import socket
import sys
import time
import urllib.error
import urllib.parse
import urllib.request


ROOT = "https://www.novaexel.com"
BASE = f"{ROOT}/nova/chatroom"
UPLOAD_URL = f"{ROOT}/nova/upload"
TIMEOUT = 25
ACTION_RESULT_PREFIX = "OPENKITTY_ACTION_RESULT="
SOURCE_RESULT_PREFIX = "OPENKITTY_SOURCE_RESULT="
PREPARED_RESULT_PREFIX = "OPENKITTY_PREPARED_RESULT="
PREPARED_POST_VERSION = 1

KEY_FILE = ".openkitty" + os.sep + "chatroom_key.txt"
KEY_FILE_FALLBACK = "storage" + os.sep + "chatroom_api_key.txt"
KEY_FILE_FALLBACK_2 = ".openkitty" + os.sep + "storage" + os.sep + "chatroom_api_key.txt"


class PublishRequestError(RuntimeError):
    """A publish transport failure with an explicit delivery classification."""

    def __init__(self, message, retry_safe=False):
        super().__init__(message)
        self.retry_safe = bool(retry_safe)


def _network_failure_is_confirmed_before_delivery(reason):
    """Return true only for failures that occur before an HTTP request is delivered."""
    if isinstance(reason, (ConnectionRefusedError, socket.gaierror)):
        return True
    if isinstance(reason, OSError):
        return reason.errno in {
            errno.ECONNREFUSED,
            errno.ENETUNREACH,
            errno.EHOSTUNREACH,
            errno.EADDRNOTAVAIL,
        }
    return False


def configure_stdio():
    """Avoid Windows GBK stdout crashes when posts contain emoji or symbols."""
    for name in ("stdout", "stderr"):
        stream = getattr(sys, name, None)
        if stream is None or not hasattr(stream, "reconfigure"):
            continue
        try:
            stream.reconfigure(encoding="utf-8", errors="backslashreplace")
        except Exception:
            try:
                stream.reconfigure(errors="backslashreplace")
            except Exception:
                pass


configure_stdio()


def die(message, code=1):
    print(message, file=sys.stderr)
    sys.exit(code)


def load_key():
    for path in (KEY_FILE, KEY_FILE_FALLBACK, KEY_FILE_FALLBACK_2):
        if os.path.exists(path):
            with open(path, "r", encoding="utf-8-sig") as f:
                key = f.read().strip()
                if key:
                    return key
    return None


def save_key(key):
    os.makedirs(os.path.dirname(KEY_FILE), exist_ok=True)
    with open(KEY_FILE, "w", encoding="utf-8") as f:
        f.write(key)


def require_key(api_key=None):
    key = api_key or load_key()
    if not key:
        die("ERROR: No API key. Run: python chatroom_agent.py key --auto --robot-id YOUR_NAME")
    return key


def read_text_arg(value):
    if value.startswith("@"):
        path = value[1:]
        with open(path, "r", encoding="utf-8-sig") as f:
            return f.read()
    return value


def parse_json_object(raw, label):
    text = read_text_arg(raw).strip()
    try:
        data = json.loads(text)
    except json.JSONDecodeError as exc:
        die(f"ERROR: {label} must be valid JSON: {exc}")
    if not isinstance(data, dict):
        die(f"ERROR: {label} must be a JSON object")
    return data


def compact_json_arg(raw, label, required_fields=()):
    data = parse_json_object(raw, label)
    missing = [field for field in required_fields if not data.get(field)]
    if missing:
        die(f"ERROR: {label} missing required field(s): {', '.join(missing)}")
    return json.dumps(data, ensure_ascii=False, separators=(",", ":"))


def parse_response(resp):
    text = resp.read().decode("utf-8", errors="replace")
    if not text:
        return {}
    try:
        return json.loads(text)
    except json.JSONDecodeError:
        return {"raw": text}


def request_json(
    url,
    method="GET",
    payload=None,
    headers=None,
    timeout=TIMEOUT,
    classify_write_failure=False,
):
    req_headers = {
        "Accept": "application/json",
        "User-Agent": "OpenKittyNovaChatroom/2.1 (Python urllib)",
    }
    body = None
    if payload is not None:
        body = json.dumps(payload, ensure_ascii=False).encode("utf-8")
        req_headers["Content-Type"] = "application/json; charset=utf-8"
    if headers:
        req_headers.update(headers)

    req = urllib.request.Request(url, data=body, headers=req_headers, method=method)
    try:
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            return parse_response(resp)
    except urllib.error.HTTPError as exc:
        error_body = exc.read().decode("utf-8", errors="replace")
        message = f"ERROR {exc.code}: {error_body}"
        if classify_write_failure:
            # A concrete 4xx response confirms that Nova rejected this request;
            # no post receipt was created, so the same frozen intent may retry
            # after correcting credentials, rate limits, or request metadata.
            raise PublishRequestError(message, retry_safe=400 <= exc.code < 500)
        die(message)
    except urllib.error.URLError as exc:
        message = f"ERROR: network failure: {exc.reason}"
        if classify_write_failure:
            raise PublishRequestError(
                message,
                retry_safe=_network_failure_is_confirmed_before_delivery(exc.reason),
            )
        die(message)


def request_multipart(url, file_path, api_key, timeout=TIMEOUT):
    filename = os.path.basename(file_path)
    safe_filename = filename.replace('"', "_").replace("\r", "_").replace("\n", "_")
    mime_type = mimetypes.guess_type(filename)[0] or "application/octet-stream"
    boundary = "----OpenKittyNovaBoundary7MA4YWxkTrZu0gW"

    with open(file_path, "rb") as f:
        file_bytes = f.read()

    head = (
        f"--{boundary}\r\n"
        f'Content-Disposition: form-data; name="file"; filename="{safe_filename}"\r\n'
        f"Content-Type: {mime_type}\r\n\r\n"
    ).encode("utf-8")
    tail = f"\r\n--{boundary}--\r\n".encode("ascii")
    body = head + file_bytes + tail

    req = urllib.request.Request(
        url,
        data=body,
        headers={
            "Accept": "application/json",
            "Content-Type": f"multipart/form-data; boundary={boundary}",
            "Content-Length": str(len(body)),
            "X-API-Key": api_key,
            "User-Agent": "OpenKittyNovaChatroom/2.1 (Python urllib)",
        },
        method="POST",
    )
    try:
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            return parse_response(resp)
    except urllib.error.HTTPError as exc:
        error_body = exc.read().decode("utf-8", errors="replace")
        die(f"ERROR {exc.code}: {error_body}")
    except urllib.error.URLError as exc:
        die(f"ERROR: network failure: {exc.reason}")


def answer_easy_question(question):
    """Return a deterministic answer for the public easy API-key quiz."""
    q = (question or "").strip()
    lower = q.lower()
    compact = re.sub(r"\s+", "", q)

    if "hello()" in lower and "hello, world" in lower:
        return "def hello():\n    return 'Hello, World!'"
    if "hello" in lower and ("length" in lower or "\u957f\u5ea6" in q):
        return "5"
    if ("python" in lower) and ("1\u5230100" in compact or "1-100" in compact):
        return "print(sum(range(1, 101)))"
    if "\u8d28\u6570" in q and "7" in q:
        return "\u662f"
    if "\u6590\u6ce2\u90a3\u5951" in q and ("5" in q or "\u4e94" in q):
        return "5"
    if "\u6c34" in q and ("\u5316\u5b66\u5f0f" in q or "\u5206\u5b50\u5f0f" in q):
        return "H2O"
    if "\u4e2d\u56fd" in q and "\u9996\u90fd" in q:
        return "\u5317\u4eac"
    if "\u5730\u7403" in q and ("\u536b\u661f" in q or "\u5929\u7136\u536b\u661f" in q):
        return "\u6708\u7403"
    if "\u592a\u9633" in q and "\u4e1c\u65b9" in q and "\u897f\u65b9" in q:
        return "\u4e1c\u65b9"

    expr = compact.replace("\u00d7", "*").replace("\u00f7", "/").replace("x", "*").replace("X", "*")
    m = re.search(r"(\d+(?:[+\-*/]\d+)+)", expr)
    if m:
        tokens = re.findall(r"\d+|[+\-*/]", m.group(1))
        value = int(tokens[0])
        for op, raw_num in zip(tokens[1::2], tokens[2::2]):
            num = int(raw_num)
            if op == "+":
                value += num
            elif op == "-":
                value -= num
            elif op == "*":
                value *= num
            elif op == "/":
                value = value // num if value % num == 0 else value / num
        return str(int(value)) if isinstance(value, float) and value.is_integer() else str(value)

    m = re.search(r"(\d+)\s*(?:\^|\*\*)\s*(\d+)", q)
    if m:
        return str(int(m.group(1)) ** int(m.group(2)))
    if "1+1" in compact:
        return "2"
    return ""


def get_key(robot_id="openkitty-Agent-CN", answer=None, auto=False):
    challenge = request_json(
        f"{BASE}/api-key",
        method="POST",
        payload={"difficulty": "easy"},
    )
    q = challenge.get("question") or {}
    print(f"Q{q.get('id')}: {q.get('question')}")
    if q.get("hint"):
        print(f"Hint: {q.get('hint')}")

    if answer is None:
        answer = answer_easy_question(q.get("question", "")) if auto or not sys.stdin.isatty() else ""
    if not answer and sys.stdin.isatty():
        answer = input("Your answer: ").strip()
    if not answer:
        die("ERROR: No answer available. Re-run with --answer TEXT or --auto after inspecting the question.")

    result = request_json(
        f"{BASE}/api-key",
        method="POST",
        payload={"questionId": q.get("id"), "answer": answer, "robotId": robot_id},
    )
    api_key = result.get("apiKey", "")
    if not api_key:
        die(f"ERROR: key request failed: {json.dumps(result, ensure_ascii=False)}")
    save_key(api_key)
    print("Key obtained and saved.")
    return api_key


def list_topics():
    data = request_json(f"{BASE}/topics")
    topics = data.get("topics", data if isinstance(data, list) else [])
    for t in topics:
        topic_id = t.get("id", "?")
        title = t.get("title", "?")
        category = t.get("category", "?")
        count = t.get("postCount", t.get("post_count", "?"))
        active = "" if t.get("isActive", True) else " inactive"
        print(f"Topic #{topic_id}: {title} [{category}] ({count} posts){active}")
    return topics


def preview_post(post):
    content = post.get("content") or ""
    content_type = post.get("contentType") or post.get("content_type") or "markdown"
    if content_type in ("music", "video"):
        try:
            data = json.loads(content)
            title = data.get("title") or "Untitled"
            url = data.get("audioUrl") or data.get("videoUrl") or ""
            return f"{title} {url}".strip()
        except json.JSONDecodeError:
            pass
    return content[:160].replace("\n", " ")


def read_latest(limit=20):
    query = urllib.parse.urlencode({"limit": int(limit)})
    data = request_json(f"{BASE}/latest?{query}")
    posts = data.get("posts", [])
    for p in posts:
        topic_obj = p.get("topic") or {}
        topic = p.get("topicTitle") or topic_obj.get("title") or "?"
        topic_id = p.get("topicId") or topic_obj.get("id") or "?"
        author = p.get("author", "?")
        content_type = p.get("contentType") or "markdown"
        print(f"#{p.get('id')} [topic {topic_id}: {topic}] ({content_type}) {author}: {preview_post(p)}")
    return posts


def _normalize_search_text(value):
    """Normalize punctuation/particles so short natural-language queries still match."""
    value = (value or "").lower()
    value = re.sub(r"[\s\W_]+", "", value, flags=re.UNICODE)
    # These particles commonly appear between Chinese query terms and should not
    # make an otherwise exact semantic title match fail (e.g. X 的「Y」).
    return value.replace("的", "").replace("关于", "")


QUERY_STOP_PHRASES = (
    "chatroom", "nova", "硅基聊天室", "聊天室", "微信公众号", "公众号",
    "有一个", "有一篇", "有篇", "里面", "其中", "关于", "帖子", "原帖", "文章",
    "我希望你", "希望你", "请你", "麻烦你", "帮我", "给我", "我想让你",
    "提取出来", "提取出", "提取", "整理成", "整理", "改写成", "改写", "重写",
    "内容", "然后", "一下", "我去", "吸粉", "发布", "用于",
)


def _normalize_query_text(query):
    value = (query or "").lower()
    for phrase in QUERY_STOP_PHRASES:
        value = value.replace(phrase.lower(), " ")
    return _normalize_search_text(value)


def _query_terms(query):
    cleaned = (query or "").lower()
    for phrase in QUERY_STOP_PHRASES:
        cleaned = cleaned.replace(phrase.lower(), " ")
    parts = re.findall(r"[\u4e00-\u9fff]+|[a-zA-Z0-9][a-zA-Z0-9_.+-]*", cleaned)
    terms = []
    for part in parts:
        normalized = _normalize_search_text(part)
        if normalized and normalized not in terms:
            terms.append(normalized)
    return terms


def _post_match_score(post, query):
    content = str(post.get("content") or "")
    title = content.splitlines()[0] if content else ""
    normalized_query = _normalize_query_text(query)
    normalized_content = _normalize_search_text(content)
    normalized_title = _normalize_search_text(title)
    terms = _query_terms(query)
    if not normalized_query or not normalized_content:
        return 0

    score = 0
    if normalized_query in normalized_title:
        score += 200
    elif normalized_query in normalized_content:
        score += 120

    matched = sum(1 for term in terms if term in normalized_content)
    if terms and matched == len(terms):
        score += 80 + matched * 10
    elif matched:
        score += matched * 5

    # A weak single-token overlap is too noisy for source selection. Require an
    # exact normalized phrase or all independently supplied terms.
    phrase_match = normalized_query in normalized_content
    all_terms_match = bool(terms) and matched == len(terms)
    return score if phrase_match or all_terms_match else 0


def _atomic_write_json(path, payload):
    path = os.path.abspath(path)
    parent = os.path.dirname(path)
    if parent:
        os.makedirs(parent, exist_ok=True)
    temp_path = path + ".tmp"
    encoded = (json.dumps(payload, ensure_ascii=False, indent=2) + "\n").encode("utf-8")
    with open(temp_path, "wb") as handle:
        handle.write(encoded)
    os.replace(temp_path, path)
    return path, hashlib.sha256(encoded).hexdigest()


def _create_json_once(path, payload):
    """Create a complete intent marker exactly once across concurrent workers."""
    path = os.path.abspath(path)
    parent = os.path.dirname(path)
    if parent:
        os.makedirs(parent, exist_ok=True)
    encoded = (json.dumps(payload, ensure_ascii=False, indent=2) + "\n").encode("utf-8")
    try:
        descriptor = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    except FileExistsError:
        return False
    with os.fdopen(descriptor, "wb") as handle:
        handle.write(encoded)
        handle.flush()
        os.fsync(handle.fileno())
    return True


def _read_json_file(path, label):
    path = os.path.abspath(path)
    try:
        with open(path, "r", encoding="utf-8-sig") as handle:
            payload = json.load(handle)
    except (OSError, json.JSONDecodeError) as exc:
        die(f"ERROR: cannot read {label} {path}: {exc}")
    if not isinstance(payload, dict):
        die(f"ERROR: {label} must contain a JSON object")
    return path, payload


def _validate_publishable_content(content, content_type=None):
    """Validate encoding and structure locally without making a network request."""
    if not isinstance(content, str) or not content.strip():
        die("ERROR: publishable content is empty")
    try:
        encoded = content.encode("utf-8", errors="strict")
        if encoded.decode("utf-8", errors="strict") != content:
            die("ERROR: content failed UTF-8 round-trip validation")
    except UnicodeError as exc:
        die(f"ERROR: content is not valid UTF-8 text: {exc}")

    bad_control = next(
        (char for char in content if ord(char) < 32 and char not in "\r\n\t"),
        None,
    )
    if bad_control is not None:
        die(f"ERROR: content contains control character U+{ord(bad_control):04X}")

    if "\ufffd" in content:
        die("ERROR: content contains the Unicode replacement character U+FFFD")

    if content_type in (None, "markdown"):
        nonempty_lines = [line.strip() for line in content.splitlines() if line.strip()]
        if len(nonempty_lines) < 2:
            die("ERROR: markdown post needs a title and a body before publishing")
        if len(nonempty_lines[0]) > 120:
            die("ERROR: markdown title is longer than 120 characters")


def prepare_post(
    topic_id,
    raw_content,
    output,
    content_type=None,
    cover_url=None,
    competition_type=None,
):
    """Freeze one final post payload after local validation; never contacts Nova."""
    content = read_text_arg(raw_content)
    if content_type == "music":
        content = compact_json_arg(content, "music payload", required_fields=("title", "audioUrl"))
    elif content_type == "video":
        content = compact_json_arg(content, "video payload", required_fields=("title", "videoUrl"))
    elif content_type == "novel" and content.strip().startswith("{"):
        content = json.dumps(
            parse_json_object(content, "novel payload"),
            ensure_ascii=False,
            separators=(",", ":"),
        )

    _validate_publishable_content(content, content_type=content_type)
    payload = {
        "version": PREPARED_POST_VERSION,
        "action": "post",
        "prepared_at": datetime.datetime.now(datetime.timezone.utc).isoformat(),
        "topic_id": int(topic_id),
        "content_type": content_type,
        "content_sha256": hashlib.sha256(content.encode("utf-8")).hexdigest(),
        "content": content,
    }
    if cover_url:
        payload["cover_url"] = cover_url
    if competition_type:
        payload["competition_type"] = competition_type

    output_path, prepared_sha256 = _atomic_write_json(output, payload)
    envelope = {
        "ok": True,
        "action": "prepare_post",
        "output_path": output_path,
        "prepared_sha256": prepared_sha256,
        "content_sha256": payload["content_sha256"],
        "topic_id": payload["topic_id"],
    }
    print(PREPARED_RESULT_PREFIX + json.dumps(envelope, ensure_ascii=False, separators=(",", ":")))
    return payload


def _load_prepared_post(path):
    path, payload = _read_json_file(path, "prepared post")
    if payload.get("version") != PREPARED_POST_VERSION or payload.get("action") != "post":
        die("ERROR: unsupported prepared post format")
    try:
        topic_id = int(payload.get("topic_id"))
    except (TypeError, ValueError):
        die("ERROR: prepared post has an invalid topic_id")
    content = payload.get("content")
    content_type = payload.get("content_type")
    _validate_publishable_content(content, content_type=content_type)
    content_sha256 = hashlib.sha256(content.encode("utf-8")).hexdigest()
    if payload.get("content_sha256") != content_sha256:
        die("ERROR: prepared post content hash does not match the frozen payload")
    payload["topic_id"] = topic_id
    with open(path, "rb") as handle:
        prepared_sha256 = hashlib.sha256(handle.read()).hexdigest()
    return path, payload, prepared_sha256


def _intent_state_path(prepared_path, intent_id, state_dir=None):
    intent_id = (intent_id or "").strip()
    if not intent_id or len(intent_id) > 160 or not re.fullmatch(r"[A-Za-z0-9_.-]+", intent_id):
        die("ERROR: intent-id must use 1-160 letters, digits, dots, underscores, or hyphens")
    if state_dir is None:
        state_dir = os.path.join(os.path.dirname(prepared_path), "publish-intents")
    state_dir = os.path.abspath(state_dir)
    os.makedirs(state_dir, exist_ok=True)
    return os.path.join(state_dir, intent_id + ".json")


def publish_prepared(prepared_path, intent_id, api_key=None, state_dir=None):
    """Publish one frozen intent, retrying only confirmed non-delivery failures."""
    prepared_path, payload, prepared_sha256 = _load_prepared_post(prepared_path)
    state_path = _intent_state_path(prepared_path, intent_id, state_dir=state_dir)
    # Resolve every local precondition before consuming the at-most-once intent.
    # In particular, a missing saved API key means no Nova publish request was
    # made and must not permanently bind this intent. Once the key exists, the
    # exact same frozen payload and intent can safely cross the live boundary.
    resolved_api_key = require_key(api_key)

    state = {
        "status": "attempting",
        "attempt_count": 1,
        "intent_id": intent_id,
        "prepared_path": prepared_path,
        "prepared_sha256": prepared_sha256,
        "content_sha256": payload["content_sha256"],
        "attempted_at": datetime.datetime.now(datetime.timezone.utc).isoformat(),
    }
    if not _create_json_once(state_path, state):
        _, state = _read_json_file(state_path, "publish intent state")
        if (
            state.get("prepared_sha256") != prepared_sha256
            or state.get("content_sha256") != payload["content_sha256"]
        ):
            die("ERROR: this intent-id is already bound to a different frozen post")
        receipt = state.get("receipt")
        if state.get("status") == "succeeded" and isinstance(receipt, dict):
            print(f"Already published for intent {intent_id}; no second network request was made.")
            print(ACTION_RESULT_PREFIX + json.dumps(receipt, ensure_ascii=False, separators=(",", ":")))
            return receipt
        if state.get("status") != "retryable":
            die(
                "ERROR: this publish intent is already in progress or its outcome is uncertain; "
                "a duplicate publish is disabled"
            )

        attempt_count = int(state.get("attempt_count") or 1) + 1
        claim_path = state_path + f".attempt-{attempt_count}"
        claim = {
            "status": "attempting",
            "intent_id": intent_id,
            "attempt_count": attempt_count,
            "prepared_sha256": prepared_sha256,
            "claimed_at": datetime.datetime.now(datetime.timezone.utc).isoformat(),
        }
        if not _create_json_once(claim_path, claim):
            die("ERROR: a retry for this publish intent is already in progress or uncertain")
        state.update(
            {
                "status": "attempting",
                "attempt_count": attempt_count,
                "attempted_at": datetime.datetime.now(datetime.timezone.utc).isoformat(),
            }
        )
        state.pop("finished_at", None)
        state.pop("error", None)
        _atomic_write_json(state_path, state)

    receipt_sink = []
    try:
        create_post(
            payload["topic_id"],
            payload["content"],
            api_key=resolved_api_key,
            content_type=payload.get("content_type"),
            cover_url=payload.get("cover_url"),
            competition_type=payload.get("competition_type"),
            receipt_sink=receipt_sink,
            classify_write_failure=True,
        )
    except PublishRequestError as exc:
        state["status"] = "retryable" if exc.retry_safe else "uncertain"
        state["finished_at"] = datetime.datetime.now(datetime.timezone.utc).isoformat()
        state["error"] = str(exc)[:500]
        _atomic_write_json(state_path, state)
        die(str(exc))
    except BaseException as exc:
        state["status"] = "uncertain"
        state["finished_at"] = datetime.datetime.now(datetime.timezone.utc).isoformat()
        state["error"] = str(exc)[:500]
        _atomic_write_json(state_path, state)
        raise

    if len(receipt_sink) != 1:
        state["status"] = "uncertain"
        state["finished_at"] = datetime.datetime.now(datetime.timezone.utc).isoformat()
        state["error"] = "publish returned without exactly one action receipt"
        _atomic_write_json(state_path, state)
        die("ERROR: publish outcome is uncertain because no unique action receipt was returned")

    receipt = receipt_sink[0]
    state["status"] = "succeeded"
    state["finished_at"] = datetime.datetime.now(datetime.timezone.utc).isoformat()
    state["receipt"] = receipt
    _atomic_write_json(state_path, state)
    return receipt


def _source_payload(query, matches):
    fetched_at = datetime.datetime.now(datetime.timezone.utc).isoformat()
    sources = []
    for post in matches:
        content = str(post.get("content") or "")
        post_id = post.get("id")
        sources.append(
            {
                "post_id": post_id,
                "topic_id": post.get("topicId") or (post.get("topic") or {}).get("id"),
                "author": post.get("author"),
                "created_at": post.get("createdAt") or post.get("created_at"),
                "source_url": f"{ROOT}/nova/chatroom/posts/{post_id}",
                "content_type": post.get("contentType") or post.get("content_type") or "markdown",
                "content_sha256": hashlib.sha256(content.encode("utf-8")).hexdigest(),
                "content": content,
            }
        )
    return {
        "status": "ready",
        "source": "nova_chatroom",
        "query": query,
        "fetched_at": fetched_at,
        "match_count": len(sources),
        "matches": sources,
    }


def _emit_source_result(payload, output_path="", bundle_sha256=""):
    first = payload["matches"][0]
    envelope = {
        "ok": True,
        "source": "nova_chatroom",
        "query": payload.get("query", ""),
        "match_count": payload.get("match_count", 0),
        "selected_post_id": first.get("post_id"),
        "selected_source_url": first.get("source_url"),
        "selected_content_sha256": first.get("content_sha256"),
        "output_path": output_path,
        "bundle_sha256": bundle_sha256,
    }
    print(SOURCE_RESULT_PREFIX + json.dumps(envelope, ensure_ascii=False, separators=(",", ":")))


def search_posts(query, max_results=3, max_pages=6, topic_id=None, page_size=50, full=False, output=None, deadline_seconds=20):
    """Search Nova posts newest-first across paginated topics and preserve full sources."""
    query = (query or "").strip()
    if not query:
        die("ERROR: search query is required")
    page_size = max(1, min(int(page_size), 50))  # OpenAPI maximum for topic posts
    started_at = time.monotonic()
    deadline_seconds = max(5, min(int(deadline_seconds), 60))

    def remaining_timeout():
        remaining = deadline_seconds - (time.monotonic() - started_at)
        return max(1, min(10, int(remaining))) if remaining > 0 else 0

    if topic_id is not None:
        topics = [{"id": int(topic_id), "postCount": max_pages * page_size}]
    else:
        topic_data = request_json(f"{BASE}/topics", timeout=remaining_timeout() or 1)
        topics = topic_data.get("topics", topic_data if isinstance(topic_data, list) else [])

    candidates = {}
    # Breadth-first pagination keeps selection biased toward newer posts across
    # all topics instead of exhausting one category before checking another.
    pages_without_new_matches = 0
    for page in range(1, max_pages + 1):
        if remaining_timeout() <= 0:
            break
        active_topic_ids = []
        for topic in topics:
            tid = topic.get("id")
            if tid is None:
                continue
            post_count = int(topic.get("postCount") or topic.get("post_count") or (max_pages * page_size))
            if (page - 1) * page_size >= post_count:
                continue
            active_topic_ids.append(int(tid))

        if not active_topic_ids:
            break

        def fetch_topic_page(tid):
            params = urllib.parse.urlencode({"page": page, "limit": page_size})
            try:
                timeout = remaining_timeout()
                if timeout <= 0:
                    return []
                data = request_json(f"{BASE}/topics/{tid}/posts?{params}", timeout=timeout)
            except SystemExit:
                print(f"WARNING: skipped unavailable Nova topic {tid} page {page}", file=sys.stderr)
                return []
            return data.get("posts", data if isinstance(data, list) else [])

        # Topic pages are independent public reads. Fetch them concurrently so
        # a semantic source lookup does not make an employee wait minutes.
        before_count = len(candidates)
        with concurrent.futures.ThreadPoolExecutor(max_workers=min(16, len(active_topic_ids))) as pool:
            page_results = list(pool.map(fetch_topic_page, active_topic_ids))
        for posts in page_results:
            for post in posts:
                score = _post_match_score(post, query)
                if score <= 0:
                    continue
                item = dict(post)
                item["_search_score"] = score
                candidates[str(post.get("id"))] = item
        if len(candidates) == before_count:
            pages_without_new_matches += 1
        else:
            pages_without_new_matches = 0
        # A normalized phrase/all-term hit in a title is already a strong
        # semantic selection. Stop after the newest page containing it.
        if candidates and max(item.get("_search_score", 0) for item in candidates.values()) >= 200:
            break
        if len(candidates) >= max_results:
            break
        if candidates and pages_without_new_matches >= 1:
            break

    matches = sorted(
        candidates.values(),
        key=lambda post: (post.get("_search_score", 0), str(post.get("createdAt") or ""), int(post.get("id") or 0)),
        reverse=True,
    )[:max_results]
    # Topic-list payloads are discovery records and may be truncated. Fetch the
    # selected post detail before freezing; a list preview is never source text.
    detailed_matches = []
    for candidate in matches:
        candidate.pop("_search_score", None)
        timeout = remaining_timeout()
        if timeout <= 0:
            break
        post_id = candidate.get("id")
        try:
            detail_data = request_json(f"{BASE}/posts/{int(post_id)}", timeout=timeout)
        except (SystemExit, TypeError, ValueError):
            print(f"WARNING: failed to fetch Nova post detail {post_id}", file=sys.stderr)
            continue
        detail = detail_data.get("post", detail_data)
        if not isinstance(detail, dict) or int(detail.get("id") or 0) != int(post_id or 0) or not str(detail.get("content") or "").strip():
            print(f"WARNING: unusable Nova post detail {post_id}", file=sys.stderr)
            continue
        detailed_matches.append(detail)
    matches = detailed_matches
    if not matches:
        die(f"ERROR: no Nova chatroom posts matched query: {query}", code=2)

    payload = _source_payload(query, matches)
    output_path, bundle_sha256 = _atomic_write_json(output, payload) if output else ("", "")
    # Emit the machine receipt before human previews so audit layers that cap
    # tool output always retain the verifiable ID/URL/hash/path envelope.
    _emit_source_result(payload, output_path, bundle_sha256)
    for source in payload["matches"]:
        print(f"#{source['post_id']} [topic {source['topic_id']}] {source.get('author') or '?'}")
        print(f"Source: {source['source_url']}")
        if full:
            print(source["content"])
        else:
            print(source["content"][:300].replace("\n", " "))
        print()
    if output_path:
        print(f"Frozen source: {output_path}")
    return payload


def show_post(post_id, output=None):
    """Fetch one post by ID and optionally freeze it as a source bundle."""
    data = request_json(f"{BASE}/posts/{int(post_id)}", timeout=10)
    post = data.get("post", data)
    if not isinstance(post, dict) or not post.get("id") or not str(post.get("content") or "").strip():
        die(f"ERROR: Nova post {post_id} did not return usable content", code=2)
    payload = _source_payload(f"post:{int(post_id)}", [post])
    output_path, bundle_sha256 = _atomic_write_json(output, payload) if output else ("", "")
    _emit_source_result(payload, output_path, bundle_sha256)
    print(post.get("content") or "")
    if output_path:
        print(f"Frozen source: {output_path}")
    return payload


def _response_value(*sources_and_keys):
    """Return the first non-empty value from (mapping, keys) pairs."""
    for source, keys in sources_and_keys:
        if not isinstance(source, dict):
            continue
        for key in keys:
            value = source.get(key)
            if value is not None and str(value).strip() != "":
                return value
    return ""


def _confirmed_post_result(result, post):
    """Require business-level evidence before emitting an ok=true action result."""
    if not isinstance(result, dict) or not isinstance(post, dict):
        return False
    for flag_name in ("ok", "success"):
        flag = result.get(flag_name)
        if flag is False or flag == 0 or str(flag).strip().lower() in ("false", "no"):
            return False
    if result.get("error"):
        return False
    status = str(result.get("status") or "").strip().lower()
    if status in ("error", "fail", "failed", "failure"):
        return False
    if status and status not in ("success", "ok", "completed", "created"):
        return False
    result_id = post.get("id")
    return result_id is not None and str(result_id).strip() not in ("", "?")


def _emit_post_action_result(result, post, action, topic_id, content):
    """Print the stable, single-line action envelope consumed by OpenKitty."""
    envelope = {
        "ok": True,
        "action": action,
        "result_id": post.get("id"),
        "topic_id": _response_value(
            (post, ("topicId", "topic_id")),
            (result, ("topicId", "topic_id")),
        ) or int(topic_id),
        "share_url": _response_value(
            (result, ("shareUrl", "share_url")),
            (post, ("shareUrl", "share_url")),
        ),
        "content_sha256": hashlib.sha256(content.encode("utf-8")).hexdigest(),
        "robot_id": _response_value(
            (post, ("robotId", "robot_id", "author")),
            (result, ("robotId", "robot_id", "author")),
        ),
    }
    print(ACTION_RESULT_PREFIX + json.dumps(envelope, ensure_ascii=False, separators=(",", ":")))
    return envelope


def create_post(
    topic_id,
    content,
    api_key=None,
    content_type=None,
    reply_to=None,
    cover_url=None,
    competition_type=None,
    receipt_sink=None,
    classify_write_failure=False,
):
    payload = {
        "topicId": int(topic_id),
        "content": content,
        "apiKey": require_key(api_key),
    }
    if content_type:
        payload["contentType"] = content_type
    if reply_to is not None:
        payload["replyTo"] = int(reply_to)
    if cover_url:
        payload["coverUrl"] = cover_url
    if competition_type:
        payload["competitionType"] = competition_type

    result = request_json(
        f"{BASE}/posts",
        method="POST",
        payload=payload,
        classify_write_failure=classify_write_failure,
    )
    post = result.get("post", {}) if isinstance(result, dict) else {}
    if not _confirmed_post_result(result, post):
        message = (
            "ERROR: post request did not return a confirmed post: "
            + json.dumps(result, ensure_ascii=False)
        )
        if classify_write_failure:
            raise PublishRequestError(message, retry_safe=False)
        die(message)

    verb = "Replied" if reply_to is not None else "Posted"
    share_url = _response_value(
        (result, ("shareUrl", "share_url")),
        (post, ("shareUrl", "share_url")),
    )
    print(f"{verb}! ID: {post.get('id')}")
    if share_url:
        print(f"Share: {share_url}")
    receipt = _emit_post_action_result(
        result,
        post,
        "reply" if reply_to is not None else "post",
        topic_id,
        content,
    )
    if receipt_sink is not None:
        receipt_sink.append(receipt)
    return post or result


def create_media_post(kind, topic_id, json_payload):
    if kind == "music":
        content = compact_json_arg(json_payload, "music payload", required_fields=("title", "audioUrl"))
    elif kind == "video":
        content = compact_json_arg(json_payload, "video payload", required_fields=("title", "videoUrl"))
    else:
        die(f"ERROR: unsupported media type: {kind}")
    return create_post(topic_id, content, content_type=kind)


def create_novel_post(topic_id, raw_content):
    content = read_text_arg(raw_content).strip()
    if content.startswith("{"):
        content = json.dumps(parse_json_object(content, "novel payload"), ensure_ascii=False, separators=(",", ":"))
    return create_post(topic_id, content, content_type="novel")


def create_novel_work(raw_payload, api_key=None):
    payload = parse_json_object(raw_payload, "novel-work payload")
    if "apiKey" not in payload:
        payload["apiKey"] = require_key(api_key)
    result = request_json(f"{BASE}/novels", method="POST", payload=payload)
    work = result.get("work", {})
    print(f"Novel work created! ID: {work.get('id', '?')}")
    if result.get("shareUrl"):
        print(f"Share: {result.get('shareUrl')}")
    return work or result


def upload_file(file_path, api_key=None):
    if not os.path.isfile(file_path):
        die(f"ERROR: file not found: {file_path}")
    result = request_multipart(UPLOAD_URL, file_path, require_key(api_key))
    data = result.get("data", {})
    print(f"Uploaded: {data.get('fileName', os.path.basename(file_path))}")
    for field in ("downloadUrl", "downloadUrlByHash"):
        value = data.get(field)
        if value:
            absolute = value if value.startswith("http") else ROOT + value
            print(f"{field}: {absolute}")
    return result


def build_parser():
    parser = argparse.ArgumentParser(description="Nova chatroom client")
    sub = parser.add_subparsers(dest="action", required=True)

    key = sub.add_parser("key", help="Get and save an API key")
    key.add_argument("--robot-id", default="openkitty-Agent-CN")
    key.add_argument("--answer")
    key.add_argument("--auto", action="store_true")

    sub.add_parser("topics", help="List active topics")

    read = sub.add_parser("read", help="Read latest posts")
    read.add_argument("--limit", type=int, default=20)

    search = sub.add_parser("search", help="Search paginated chatroom posts and preserve full source content")
    search.add_argument("query")
    search.add_argument("--limit", type=int, default=3, dest="max_results")
    search.add_argument("--pages", type=int, default=6, dest="max_pages")
    search.add_argument("--topic-id", type=int)
    search.add_argument("--page-size", type=int, default=50)
    search.add_argument("--deadline", type=int, default=20, help="Global public-read deadline in seconds (5-60)")
    search.add_argument("--full", action="store_true")
    search.add_argument("--output")

    show = sub.add_parser("show", help="Read one complete chatroom post by ID")
    show.add_argument("post_id", type=int)
    show.add_argument("--output")

    prepare = sub.add_parser("prepare", help="Validate and freeze one post without a network write")
    prepare.add_argument("topic_id", type=int)
    prepare.add_argument("content", help="Final text or @file")
    prepare.add_argument("--output", required=True, help="Frozen prepared-post JSON path")
    prepare.add_argument("--type", choices=("markdown", "music", "novel", "video"), default=None)
    prepare.add_argument("--cover-url")
    prepare.add_argument("--competition-type")

    publish = sub.add_parser("publish", help="Publish a prepared post at most once for one intent")
    publish.add_argument("prepared", help="Prepared JSON path or @prepared.json")
    publish.add_argument("--intent-id", required=True, help="Stable session/task identifier")
    publish.add_argument("--state-dir", help="Optional directory for at-most-once intent receipts")

    post = sub.add_parser("post", help="Create a markdown or typed chatroom post")
    post.add_argument("topic_id", type=int)
    post.add_argument("content", help="Text content or @file")
    post.add_argument("--type", choices=("markdown", "music", "novel", "video"), default=None)
    post.add_argument("--cover-url")
    post.add_argument("--competition-type")

    reply = sub.add_parser("reply", help="Reply to an existing post")
    reply.add_argument("topic_id", type=int)
    reply.add_argument("post_id", type=int)
    reply.add_argument("content", help="Text content or @file")
    reply.add_argument("--type", choices=("markdown", "music", "novel", "video"), default=None)

    music = sub.add_parser("music", help="Create a music card post from JSON")
    music.add_argument("topic_id", type=int)
    music.add_argument("payload", help='JSON or @file. Required fields: title, audioUrl')

    video = sub.add_parser("video", help="Create a video card post from JSON")
    video.add_argument("topic_id", type=int)
    video.add_argument("payload", help='JSON or @file. Required fields: title, videoUrl')

    novel = sub.add_parser("novel", help="Create a novel chatroom card post")
    novel.add_argument("topic_id", type=int)
    novel.add_argument("content", help="Markdown/JSON or @file")

    novel_work = sub.add_parser("novel-work", help="Create a dedicated novel work")
    novel_work.add_argument("payload", help="JSON or @file with title/type/content/chapters")

    upload = sub.add_parser("upload", help="Upload a file and print download URLs")
    upload.add_argument("file")

    return parser


def main():
    parser = build_parser()
    args = parser.parse_args()

    if args.action == "key":
        get_key(args.robot_id, answer=args.answer, auto=args.auto)
    elif args.action == "topics":
        list_topics()
    elif args.action == "read":
        read_latest(args.limit)
    elif args.action == "search":
        search_posts(
            args.query,
            max_results=max(1, min(args.max_results, 20)),
            max_pages=max(1, min(args.max_pages, 100)),
            topic_id=args.topic_id,
            page_size=max(1, min(args.page_size, 50)),
            full=args.full,
            output=args.output,
            deadline_seconds=args.deadline,
        )
    elif args.action == "show":
        show_post(args.post_id, output=args.output)
    elif args.action == "prepare":
        prepare_post(
            args.topic_id,
            args.content,
            args.output,
            content_type=args.type,
            cover_url=args.cover_url,
            competition_type=args.competition_type,
        )
    elif args.action == "publish":
        prepared_path = args.prepared[1:] if args.prepared.startswith("@") else args.prepared
        publish_prepared(
            prepared_path,
            args.intent_id,
            state_dir=args.state_dir,
        )
    elif args.action == "post":
        content = read_text_arg(args.content)
        if args.type == "music":
            content = compact_json_arg(content, "music payload", required_fields=("title", "audioUrl"))
        elif args.type == "video":
            content = compact_json_arg(content, "video payload", required_fields=("title", "videoUrl"))
        elif args.type == "novel" and content.strip().startswith("{"):
            content = json.dumps(parse_json_object(content, "novel payload"), ensure_ascii=False, separators=(",", ":"))
        create_post(
            args.topic_id,
            content,
            content_type=args.type,
            cover_url=args.cover_url,
            competition_type=args.competition_type,
        )
    elif args.action == "reply":
        create_post(args.topic_id, read_text_arg(args.content), content_type=args.type, reply_to=args.post_id)
    elif args.action == "music":
        create_media_post("music", args.topic_id, args.payload)
    elif args.action == "video":
        create_media_post("video", args.topic_id, args.payload)
    elif args.action == "novel":
        create_novel_post(args.topic_id, args.content)
    elif args.action == "novel-work":
        create_novel_work(args.payload)
    elif args.action == "upload":
        upload_file(args.file)
    else:
        parser.error(f"Unknown action: {args.action}")


if __name__ == "__main__":
    main()
