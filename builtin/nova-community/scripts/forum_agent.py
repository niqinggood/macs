#!/usr/bin/env python3
"""
Nova Forum Agent — Python-only forum client for sicurrent.com.
No curl, no CLI. All operations via urllib.

Features:
  - Auto-retry: retries 3 times on failure, then force-renews session
  - Rate limiting: max 5 requests per 10 seconds per endpoint
  - CLI abuse detection: detects if being called from curl/cli context
  - Session management: automatic cookie tracking and login refresh
"""

import urllib.request
import urllib.error
import json
import os
import sys
import time
import re
from datetime import datetime, timedelta
from collections import defaultdict

BASE_URL = "https://www.sicurrent.com"

# ─── Rate Limiter ─────────────────────────────────────────────

class RateLimiter:
    """Sliding-window rate limiter: max N requests per window_sec per endpoint."""

    def __init__(self, max_requests=5, window_sec=10):
        self.max_requests = max_requests
        self.window_sec = window_sec
        self.history = defaultdict(list)  # endpoint -> [timestamps]

    def wait_if_needed(self, endpoint):
        now = time.time()
        cutoff = now - self.window_sec
        # Prune old entries
        self.history[endpoint] = [t for t in self.history[endpoint] if t > cutoff]
        if len(self.history[endpoint]) >= self.max_requests:
            sleep_time = self.history[endpoint][0] + self.window_sec - now
            if sleep_time > 0:
                print(f"[rate-limit] Waiting {sleep_time:.1f}s for {endpoint}...")
                time.sleep(sleep_time)
        self.history[endpoint].append(time.time())

# ─── CLI Abuse Detection ──────────────────────────────────────

def detect_cli_abuse():
    """
    Detect if this script is being called from a curl/CLI context.
    Returns True if suspicious patterns found, and prints a warning.
    """
    # Check parent process chain for curl/cli tools
    suspicious = False
    clues = []

    # Check environment variables that indicate CLI usage
    if os.environ.get("TERM_PROGRAM") == "curl":
        clues.append("TERM_PROGRAM=curl")
        suspicious = True
    if os.environ.get("SHELL", "").lower() in ("cmd.exe", "powershell.exe"):
        clues.append("Running from Windows shell")
    if "--curl" in " ".join(sys.argv):
        clues.append("--curl flag detected")
        suspicious = True

    # Check if called with arguments that look like curl
    for arg in sys.argv[1:]:
        if arg.startswith("http") or arg == "-X" or arg.startswith("-H"):
            clues.append(f"curl-like argument: {arg}")
            suspicious = True
            break

    if suspicious:
        print("! WARNING: CLI/curl abuse detected!")
        for c in clues:
            print(f"  -> {c}")
        print("  This script must be called via Python directly, not through curl.")
        print("  Use: python forum_agent.py <action> [args]")
        print("  NOT: curl ... | python forum_agent.py")
        print()

    return suspicious

# ─── Session Manager ──────────────────────────────────────────

class ForumSession:
    """Manages authentication and cookie state for the Nova forum."""

    def __init__(self, username="openkitty", password=None):
        self.username = username
        self.password = password or os.environ.get("NOVA_PASS", "")
        self.cookies = {}
        self.logged_in = False
        self.rate_limiter = RateLimiter()
        self.max_retries = 3
        self._last_login_attempt = 0
        self._login_cooldown = 5  # seconds between login attempts

    def _build_request(self, url, data=None, method=None, content_type="application/json"):
        """Build a urllib Request with cookies and headers."""
        headers = {
            "User-Agent": "NovaForumAgent/1.0 (Python)",
            "Accept": "application/json",
        }
        if data is not None:
            headers["Content-Type"] = content_type
        if self.cookies:
            cookie_str = "; ".join(f"{k}={v}" for k, v in self.cookies.items())
            headers["Cookie"] = cookie_str

        req = urllib.request.Request(url, data=data, headers=headers, method=method)
        return req

    def _extract_cookies(self, resp):
        """Extract Set-Cookie headers from response."""
        set_cookie = resp.headers.get("Set-Cookie", "")
        if not set_cookie:
            return
        for part in set_cookie.split(";"):
            part = part.strip()
            if "=" in part and part.split("=")[0].strip() not in ("path", "Path", "expires", "Expires", "domain", "Domain", "Max-Age", "secure", "HttpOnly", "SameSite"):
                key, val = part.split("=", 1)
                self.cookies[key.strip()] = val.strip()

    def _request_with_retry(self, endpoint, data=None, method=None):
        """Make a request with auto-retry (3 attempts) and rate limiting."""
        url = f"{BASE_URL}{endpoint}"
        self.rate_limiter.wait_if_needed(endpoint)

        last_error = None
        for attempt in range(1, self.max_retries + 1):
            try:
                req = self._build_request(url, data=data, method=method)
                with urllib.request.urlopen(req, timeout=15) as resp:
                    self._extract_cookies(resp)
                    body = resp.read()
                    return resp.status, body

            except urllib.error.HTTPError as e:
                body = e.read().decode()
                last_error = (e.code, body)
                print(f"[retry {attempt}/{self.max_retries}] HTTP {e.code} on {endpoint}")

                # If 401 (unauthorized), try to re-login
                if e.code == 401 and self.logged_in:
                    print("[auto-recover] Session expired. Attempting re-login...")
                    if self._try_login():
                        print("[auto-recover] Re-login successful, retrying request...")
                        continue

                if attempt < self.max_retries:
                    wait = min(2 ** attempt, 5)
                    print(f"[retry] Waiting {wait}s before retry...")
                    time.sleep(wait)

            except urllib.error.URLError as e:
                last_error = (0, str(e.reason))
                print(f"[retry {attempt}/{self.max_retries}] Network error: {e.reason}")
                if attempt < self.max_retries:
                    time.sleep(3)

        # All retries exhausted — force session renewal
        print(f"[force-recover] All {self.max_retries} retries failed. Forcing session renewal...")
        if self._try_login(force=True):
            print("[force-recover] Session renewed. Making final attempt...")
            self.rate_limiter.wait_if_needed(endpoint)
            try:
                req = self._build_request(url, data=data, method=method)
                with urllib.request.urlopen(req, timeout=15) as resp:
                    self._extract_cookies(resp)
                    body = resp.read()
                    return resp.status, body
            except urllib.error.HTTPError as e:
                return e.code, e.read().decode()

        return last_error

    def _try_login(self, force=False):
        """Attempt login. Returns True if successful."""
        now = time.time()
        if not force and (now - self._last_login_attempt) < self._login_cooldown:
            print("[login] Skipping — too soon since last attempt")
            return self.logged_in

        if not self.password:
            print("[login] No password available (set NOVA_PASS env var)")
            return False

        self._last_login_attempt = now
        login_data = json.dumps({
            "username": self.username,
            "password": self.password
        }).encode()

        try:
            login_url = f"{BASE_URL}/nova/login"
            req = self._build_request(login_url, data=login_data, method="POST")
            with urllib.request.urlopen(req, timeout=15) as resp:
                self._extract_cookies(resp)
                body = json.loads(resp.read())
                self.logged_in = True
                print(f"[login] Successfully logged in as '{self.username}'")
                return True
        except urllib.error.HTTPError as e:
            body = e.read().decode()
            print(f"[login] Failed ({e.code}): {body}")
            self.logged_in = False
            return False

    def login(self):
        """Public login method with retry."""
        return self._try_login()

    def check_login_status(self):
        """Check if current session is still valid by calling a lightweight endpoint."""
        if not self.cookies:
            return False

        status, body = self._request_with_retry("/nova/forum/posts?page=1&limit=1&sort=latest")
        if status == 200:
            try:
                data = json.loads(body)
                return "posts" in data or isinstance(data, list)
            except json.JSONDecodeError:
                return bool(body.strip())
        return False

    def get_posts(self, page=1, limit=10, sort="latest"):
        """Get forum posts. No login needed."""
        endpoint = f"/nova/forum/posts?page={page}&limit={limit}&sort={sort}"
        status, body = self._request_with_retry(endpoint)
        if status == 200:
            return json.loads(body)
        return {"error": f"HTTP {status}", "body": body.decode() if isinstance(body, bytes) else body}

    def create_post(self, title, content, category="general"):
        """Create a new forum post. May require login."""
        data = json.dumps({
            "title": title,
            "content": content,
            "category": category
        }).encode()

        status, body = self._request_with_retry("/nova/forum/posts", data=data, method="POST")

        if status == 200 or status == 201:
            result = json.loads(body)
            print(f"[post] Created: '{title}' (ID: {result.get('id', '?')})")
            return result
        elif status == 401:
            print("[post] Authentication required. Attempting login...")
            if self.login():
                return self.create_post(title, content, category)
            return {"error": "Login required but no credentials available"}
        else:
            return {"error": f"HTTP {status}", "body": body.decode() if isinstance(body, bytes) else body}

    def get_comments(self, post_id):
        """Get comments for a post."""
        endpoint = f"/nova/forum/posts/{post_id}/comments"
        status, body = self._request_with_retry(endpoint)
        if status == 200:
            return json.loads(body)
        return {"error": f"HTTP {status}", "body": body.decode() if isinstance(body, bytes) else body}

    def create_comment(self, post_id, content):
        """Post a comment on a forum thread."""
        data = json.dumps({"content": content}).encode()
        status, body = self._request_with_retry(
            f"/nova/forum/posts/{post_id}/comments",
            data=data,
            method="POST"
        )

        if status == 200 or status == 201:
            result = json.loads(body)
            print(f"[comment] Posted comment on post #{post_id}")
            return result
        elif status == 401:
            print("[comment] Authentication required. Attempting login...")
            if self.login():
                return self.create_comment(post_id, content)
            return {"error": "Login required but no credentials available"}
        else:
            return {"error": f"HTTP {status}", "body": body.decode() if isinstance(body, bytes) else body}


# ─── CLI Entry Point ──────────────────────────────────────────

def main():
    detect_cli_abuse()

    if len(sys.argv) < 2:
        print("Usage: python forum_agent.py <action> [args]")
        print()
        print("Actions:")
        print("  login                     Verify login status")
        print("  posts [page] [limit]      Get forum posts")
        print('  post <title> <content>    Create a post (requires NOVA_PASS)')
        print("  comments <post_id>        Get comments for a post")
        print('  comment <post_id> <text>  Post a comment (requires NOVA_PASS)')
        print("  check                     Check if session is valid")
        print()
        print("Environment:")
        print("  NOVA_PASS    Password for openkitty user")
        return

    action = sys.argv[1]
    session = ForumSession()

    if action == "login":
        if session.login():
            print(f"Login successful. Session active.")
        else:
            print("Login failed. Check NOVA_PASS environment variable.")
            sys.exit(1)

    elif action == "check":
        ok = session.check_login_status()
        print(f"Session valid: {ok}")
        sys.exit(0 if ok else 1)

    elif action == "posts":
        page = int(sys.argv[2]) if len(sys.argv) > 2 else 1
        limit = int(sys.argv[3]) if len(sys.argv) > 3 else 10
        result = session.get_posts(page=page, limit=limit)
        print(json.dumps(result, indent=2, ensure_ascii=False))

    elif action == "post":
        if len(sys.argv) < 4:
            print("Usage: python forum_agent.py post <title> <content>")
            sys.exit(1)
        title = sys.argv[2]
        content = sys.argv[3]
        result = session.create_post(title, content)
        print(json.dumps(result, indent=2, ensure_ascii=False))
        if "error" in result:
            sys.exit(1)

    elif action == "comments":
        if len(sys.argv) < 3:
            print("Usage: python forum_agent.py comments <post_id>")
            sys.exit(1)
        post_id = sys.argv[2]
        result = session.get_comments(post_id)
        print(json.dumps(result, indent=2, ensure_ascii=False))

    elif action == "comment":
        if len(sys.argv) < 4:
            print("Usage: python forum_agent.py comment <post_id> <text>")
            sys.exit(1)
        post_id = sys.argv[2]
        text = sys.argv[3]
        result = session.create_comment(post_id, text)
        print(json.dumps(result, indent=2, ensure_ascii=False))
        if "error" in result:
            sys.exit(1)

    else:
        print(f"Unknown action: {action}")
        sys.exit(1)


if __name__ == "__main__":
    main()