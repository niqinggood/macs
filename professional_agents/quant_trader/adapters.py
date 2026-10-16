"""Isolated adapters for external quantitative-agent runtimes.

The adapters deliberately do not import TradingAgents, AI Hedge Fund, or
FinGPT. Each backend must be exposed through either a pre-existing Python
adapter process or an HTTP endpoint. This keeps dependency and process state
outside OpenKitty's main process.
"""

from __future__ import annotations

import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
from typing import Any, Dict, Iterable, List, Mapping, Optional, Tuple
from urllib import error as urlerror
from urllib import request as urlrequest
from urllib.parse import urlparse


BACKEND_PROTOCOL = "openkitty.professional-agent/backend/v1"
BACKEND_NAMES = ("tradingagents", "ai-hedge-fund", "fingpt")
BUNDLED_BACKEND_ENTRYPOINTS = {
    "tradingagents": "tradingagents_backend.py",
    "ai-hedge-fund": "ai_hedge_fund_backend.py",
    "fingpt": "fingpt_backend.py",
}

# A checked-out backend and its interpreter intentionally live outside the
# professional-agent code directory.  Keeping this layout in one table makes
# discovery portable across workspaces instead of baking E:/openkitty into the
# deployment environment.
BACKEND_RUNTIME_LAYOUT = {
    "tradingagents": {
        "repository": "TradingAgents",
        "venv": "tradingagents",
        "markers": ("tradingagents",),
    },
    "ai-hedge-fund": {
        "repository": "ai-hedge-fund",
        "venv": "ai-hedge-fund",
        "markers": ("src",),
    },
    "fingpt": {
        "repository": "FinGPT",
        "venv": "fingpt",
        # The reviewed bundled callable uses FinGPT's FinoGrid integration.
        "markers": ("fingpt", "finogrid"),
    },
}

# These names mirror env.example and are policy, not secret values.  A bundled
# backend receives only this reviewed set when the deployer did not provide an
# explicit *_ENV_ALLOWLIST.  Explicit (including intentionally empty)
# allowlists always win.
BUNDLED_BACKEND_ENV_ALLOWLISTS = {
    "tradingagents": (
        "MINIMAX_CN_API_KEY",
        "TRADINGAGENTS_LLM_PROVIDER",
        "TRADINGAGENTS_DEEP_THINK_LLM",
        "TRADINGAGENTS_QUICK_THINK_LLM",
        "TRADINGAGENTS_OUTPUT_LANGUAGE",
        "TRADINGAGENTS_MAX_DEBATE_ROUNDS",
        "TRADINGAGENTS_MAX_RISK_ROUNDS",
    ),
    "ai-hedge-fund": (
        "OPENAI_API_KEY",
        "OPENAI_API_BASE",
        "FINANCIAL_DATASETS_API_KEY",
    ),
    "fingpt": (
        "QUANT_TRADER_FINGPT_CALLABLE",
        "MINIMAX_API_KEY",
        "MINIMAX_BASE_URL",
        "MINIMAX_MODEL",
    ),
}

# Reviewed, non-secret values for unattended bundled runtimes.  They are
# applied only when the matching name is allowlisted and no explicit value was
# supplied.  Credentials deliberately do not appear in this map.
BUNDLED_BACKEND_ENV_DEFAULTS = {
    "tradingagents": {
        "TRADINGAGENTS_LLM_PROVIDER": "minimax-cn",
        "TRADINGAGENTS_DEEP_THINK_LLM": "MiniMax-M3",
        "TRADINGAGENTS_QUICK_THINK_LLM": "MiniMax-M3",
        "TRADINGAGENTS_OUTPUT_LANGUAGE": "Chinese",
        "TRADINGAGENTS_MAX_DEBATE_ROUNDS": "1",
        "TRADINGAGENTS_MAX_RISK_ROUNDS": "1",
    },
    "ai-hedge-fund": {},
    "fingpt": {
        "MINIMAX_MODEL": "MiniMax-M3",
    },
}

AUTO_DISCOVERED_BACKEND_ENV_DEFAULTS = {
    "fingpt": {
        "QUANT_TRADER_FINGPT_CALLABLE": "fingpt_minimax_callable:analyze",
    },
}

# TradingAgents performs a multi-role debate and risk-review graph, so its
# normal wall time is materially longer than the single-call AI Hedge Fund and
# FinGPT adapters. Keep every timeout bounded and operator-overridable, while
# giving the graph enough time to complete under normal provider latency.
BACKEND_TIMEOUT_DEFAULTS = {
    "tradingagents": 900,
    "ai-hedge-fund": 300,
    "fingpt": 300,
}


def _env_prefix(name: str) -> str:
    return "QUANT_TRADER_" + name.upper().replace("-", "_")


def _positive_int(raw: Optional[str], default: int, minimum: int, maximum: int) -> int:
    try:
        value = int(raw or "")
    except (TypeError, ValueError):
        return default
    return max(minimum, min(value, maximum))


def _string_list(raw: Optional[str]) -> List[str]:
    if not raw:
        return []
    return sorted({part.strip() for part in raw.split(",") if part.strip()})


def _enabled(raw: Optional[str], default: bool = True) -> bool:
    if raw is None or not raw.strip():
        return default
    return raw.strip().lower() not in {"0", "false", "no", "off", "disabled"}


def _runtime_root_candidates(
    environ: Mapping[str, str], agent_dir: Path
) -> List[Tuple[Path, str]]:
    """Return bounded, deterministic runtime roots; never scan the filesystem."""

    explicit = (environ.get("QUANT_TRADER_RUNTIME_ROOT") or "").strip()
    if explicit:
        return [(Path(explicit).expanduser(), "explicit-runtime-root")]

    candidates: List[Tuple[Path, str]] = []

    workspace = (environ.get("OPENKITTY_WORKSPACE") or "").strip()
    if workspace:
        candidates.append(
            (
                Path(workspace).expanduser() / ".openkitty" / "runtime" / "quant-trader",
                "openkitty-workspace",
            )
        )

    run_dir = (environ.get("OPENKITTY_AGENT_RUN_DIR") or "").strip()
    if run_dir:
        run_path = Path(run_dir).expanduser()
        for parent in (run_path, *run_path.parents):
            if parent.name.lower() == ".openkitty":
                candidates.append(
                    (parent / "runtime" / "quant-trader", "agent-run-dir")
                )
                break

    # A reviewed agent registry commonly sits at
    # <workspace>/professional_agents/<agent>.  Walking ancestors is bounded
    # and also supports a symlinked registry without relying on the process CWD.
    for anchor, source in ((agent_dir, "agent-directory"), (Path.cwd(), "working-directory")):
        for parent in (anchor, *anchor.parents):
            candidates.append(
                (parent / ".openkitty" / "runtime" / "quant-trader", source)
            )

    result: List[Tuple[Path, str]] = []
    seen = set()
    for candidate, source in candidates:
        normalized = os.path.normcase(str(candidate.resolve()))
        if normalized in seen:
            continue
        seen.add(normalized)
        result.append((candidate, source))
    return result


def _venv_python(venv_dir: Path) -> Optional[Path]:
    for relative in (
        Path("Scripts") / "python.exe",
        Path("bin") / "python",
        Path("bin") / "python3",
    ):
        candidate = venv_dir / relative
        if candidate.is_file():
            return candidate.resolve()
    return None


def _discover_backend_runtime(
    name: str, environ: Mapping[str, str], agent_dir: Path
) -> Optional[Tuple[Path, Path, Path, str]]:
    layout = BACKEND_RUNTIME_LAYOUT[name]
    for candidate, source in _runtime_root_candidates(environ, agent_dir):
        runtime_root = candidate.resolve()
        repository = runtime_root / str(layout["repository"])
        if not repository.is_dir():
            continue
        markers = tuple(str(item) for item in layout["markers"])
        if any(not (repository / marker).exists() for marker in markers):
            continue
        python = _venv_python(runtime_root / "venvs" / str(layout["venv"]))
        if python is None:
            continue
        return runtime_root, repository.resolve(), python, source
    return None


def _safe_process_env(allowlist: Iterable[str]) -> Dict[str, str]:
    """Build a narrow child environment instead of inheriting all host secrets."""

    baseline = {
        "PATH",
        "PATHEXT",
        "SYSTEMROOT",
        "WINDIR",
        "COMSPEC",
        "TEMP",
        "TMP",
        "TMPDIR",
        "HOME",
        "USERPROFILE",
        "LANG",
        "LC_ALL",
        "SSL_CERT_FILE",
        "SSL_CERT_DIR",
        "REQUESTS_CA_BUNDLE",
    }
    selected = baseline | {name for name in allowlist if name}
    result = {name: os.environ[name] for name in selected if name in os.environ}
    result["PYTHONIOENCODING"] = "utf-8"
    result["PYTHONUNBUFFERED"] = "1"
    # Defense in depth: downstream wrappers receive an explicit safety signal.
    result["OPENKITTY_LIVE_TRADING"] = "0"
    result["OPENKITTY_EXECUTION_MODE"] = "research-paper-only"
    return result


def _unavailable(name: str, message: str, configuration: Mapping[str, Any]) -> Dict[str, Any]:
    return {
        "backend": name,
        "status": "unavailable",
        "output": "",
        "data": {"configuration": dict(configuration)},
        "warnings": [message],
        "artifacts": [],
    }


def _error(name: str, message: str) -> Dict[str, Any]:
    return {
        "backend": name,
        "status": "error",
        "output": "",
        "data": {},
        "warnings": [message],
        "artifacts": [],
    }


class ExternalBackendAdapter:
    """One normalized adapter backed by HTTP or a dedicated Python process."""

    def __init__(self, name: str, environ: Optional[Mapping[str, str]] = None) -> None:
        if name not in BACKEND_NAMES:
            raise ValueError("unsupported backend: %s" % name)
        self.name = name
        self.environ = dict(os.environ if environ is None else environ)
        prefix = _env_prefix(name)
        get = self.environ.get
        self.http_endpoint = (get(prefix + "_HTTP_ENDPOINT") or "").strip()
        self.repo = (get(prefix + "_REPO") or "").strip()
        self.python = (get(prefix + "_PYTHON") or "").strip()
        self.entrypoint = (get(prefix + "_ENTRYPOINT") or "").strip()
        self.module = (get(prefix + "_MODULE") or "").strip()
        self.uses_bundled_shim = False
        self.auto_discovered = False
        self.discovery_source = ""
        self.runtime_root = ""
        has_explicit_backend_configuration = any(
            (self.http_endpoint, self.repo, self.python, self.entrypoint, self.module)
        )
        if (
            not has_explicit_backend_configuration
            and _enabled(get("QUANT_TRADER_AUTO_DISCOVERY"))
        ):
            discovered = _discover_backend_runtime(
                name, self.environ, Path(__file__).resolve().parent
            )
            if discovered is not None:
                runtime_root, repository, python, source = discovered
                self.repo = str(repository)
                self.python = str(python)
                self.auto_discovered = True
                self.discovery_source = source
                self.runtime_root = str(runtime_root)
        if self.python and not self.entrypoint and not self.module:
            self.entrypoint = str(
                Path(__file__).resolve().parent
                / "backend_shims"
                / BUNDLED_BACKEND_ENTRYPOINTS[name]
            )
            self.uses_bundled_shim = True
        self.timeout_seconds = _positive_int(
            get(prefix + "_TIMEOUT_SECONDS"), BACKEND_TIMEOUT_DEFAULTS[name], 1, 3600
        )
        raw_allowlist = get(prefix + "_ENV_ALLOWLIST")
        if raw_allowlist is None and self.uses_bundled_shim:
            self.env_allowlist = sorted(BUNDLED_BACKEND_ENV_ALLOWLISTS[name])
            self.uses_default_env_allowlist = True
        else:
            self.env_allowlist = _string_list(raw_allowlist)
            self.uses_default_env_allowlist = False
        self.process_env_defaults = dict(BUNDLED_BACKEND_ENV_DEFAULTS[name])
        if self.auto_discovered:
            self.process_env_defaults.update(
                AUTO_DISCOVERED_BACKEND_ENV_DEFAULTS.get(name, {})
            )
        self.max_response_bytes = _positive_int(
            self.environ.get("QUANT_TRADER_MAX_BACKEND_BYTES"),
            4 * 1024 * 1024,
            1024,
            64 * 1024 * 1024,
        )

    def configuration(self) -> Dict[str, Any]:
        transports = int(bool(self.http_endpoint)) + int(
            bool(self.python and (self.entrypoint or self.module))
        )
        issues: List[str] = []
        if transports > 1:
            state = "misconfigured"
            transport = "ambiguous"
        elif self.http_endpoint:
            transport = "http"
            parsed = urlparse(self.http_endpoint)
            if parsed.scheme not in ("http", "https") or not parsed.netloc:
                issues.append("HTTP endpoint must use http or https")
            state = "misconfigured" if issues else "configured"
        elif self.python and (self.entrypoint or self.module):
            transport = "process"
            if not self._resolve_python():
                issues.append("Python executable is unavailable")
            repo_path: Optional[Path] = None
            if self.repo:
                repo_path = Path(self.repo).expanduser().resolve()
                if not repo_path.is_dir():
                    issues.append("repository directory does not exist")
            if self.entrypoint:
                entrypoint = Path(self.entrypoint).expanduser()
                if not entrypoint.is_absolute() and repo_path is not None:
                    entrypoint = repo_path / entrypoint
                if not entrypoint.resolve().is_file():
                    issues.append("adapter entrypoint does not exist")
            state = "misconfigured" if issues else "configured"
        else:
            state = "unavailable"
            transport = "none"
        return {
            "backend": self.name,
            "state": state,
            "transport": transport,
            "repo_configured": bool(self.repo),
            "python_configured": bool(self.python),
            "endpoint_configured": bool(self.http_endpoint),
            "bundled_shim": self.uses_bundled_shim,
            "auto_discovered": self.auto_discovered,
            "discovery_source": self.discovery_source,
            "runtime_root_configured": bool(self.runtime_root),
            "default_env_allowlist": self.uses_default_env_allowlist,
            "reviewed_env_defaults": sorted(
                key
                for key in self.process_env_defaults
                if key in self.env_allowlist
            ),
            "issues": issues,
        }

    def invoke(self, backend_request: Mapping[str, Any]) -> Dict[str, Any]:
        config = self.configuration()
        if config["state"] == "misconfigured":
            return _unavailable(
                self.name,
                "Configure exactly one transport for %s (HTTP or process)." % self.name,
                config,
            )
        if config["state"] != "configured":
            return _unavailable(
                self.name,
                "%s backend is not configured; no result was fabricated." % self.name,
                config,
            )

        if config["transport"] == "http":
            raw, transport_error = self._invoke_http(backend_request)
        else:
            raw, transport_error = self._invoke_process(backend_request)
        if transport_error:
            return _error(self.name, transport_error)
        return self._normalize_response(raw)

    def _invoke_http(self, backend_request: Mapping[str, Any]) -> Tuple[Any, Optional[str]]:
        parsed = urlparse(self.http_endpoint)
        if parsed.scheme not in ("http", "https") or not parsed.netloc:
            return None, "%s HTTP endpoint must use http or https." % self.name
        body = json.dumps(backend_request, ensure_ascii=False).encode("utf-8")
        req = urlrequest.Request(
            self.http_endpoint,
            data=body,
            headers={"Content-Type": "application/json"},
            method="POST",
        )
        try:
            with urlrequest.urlopen(req, timeout=self.timeout_seconds) as response:
                limited = response.read(self.max_response_bytes + 1)
                if len(limited) > self.max_response_bytes:
                    return None, "%s HTTP response exceeded the configured size limit." % self.name
        except (urlerror.URLError, TimeoutError, OSError) as exc:
            return None, "%s HTTP backend failed: %s" % (self.name, exc)
        try:
            return json.loads(limited.decode("utf-8")), None
        except (UnicodeDecodeError, json.JSONDecodeError) as exc:
            return None, "%s HTTP backend returned invalid JSON: %s" % (self.name, exc)

    def _invoke_process(self, backend_request: Mapping[str, Any]) -> Tuple[Any, Optional[str]]:
        python_executable = self._resolve_python()
        if not python_executable:
            return None, "%s Python executable does not exist or is not executable." % self.name

        cwd: Optional[str] = None
        if self.repo:
            repo_path = Path(self.repo).expanduser().resolve()
            if not repo_path.is_dir():
                return None, "%s repository directory does not exist." % self.name
            cwd = str(repo_path)

        if self.entrypoint:
            entrypoint = Path(self.entrypoint).expanduser()
            if not entrypoint.is_absolute() and cwd:
                entrypoint = Path(cwd) / entrypoint
            entrypoint = entrypoint.resolve()
            if not entrypoint.is_file():
                return None, "%s adapter entrypoint does not exist." % self.name
            command = [python_executable, str(entrypoint)]
        elif self.module:
            command = [python_executable, "-m", self.module]
        else:
            return None, "%s process adapter needs ENTRYPOINT or MODULE." % self.name

        child_env = self._process_environment()

        payload = json.dumps(backend_request, ensure_ascii=False)
        try:
            completed = subprocess.run(
                command,
                input=payload,
                text=True,
                encoding="utf-8",
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE,
                cwd=cwd,
                env=child_env,
                timeout=self.timeout_seconds,
                check=False,
            )
        except (OSError, subprocess.SubprocessError) as exc:
            return None, "%s process backend failed: %s" % (self.name, exc)

        stdout = completed.stdout or ""
        stderr = completed.stderr or ""
        if len(stdout.encode("utf-8", errors="replace")) > self.max_response_bytes:
            return None, "%s process response exceeded the configured size limit." % self.name
        if completed.returncode != 0:
            detail = stderr.strip()[-1000:] or "no stderr"
            return None, "%s process exited with code %d: %s" % (
                self.name,
                completed.returncode,
                detail,
            )
        try:
            return json.loads(stdout), None
        except json.JSONDecodeError as exc:
            return None, "%s process backend returned invalid JSON: %s" % (self.name, exc)

    def _process_environment(self) -> Dict[str, str]:
        child_env = _safe_process_env(self.env_allowlist)
        # Values come from the adapter's environment snapshot so tests and
        # embedded callers do not accidentally inherit unrelated secrets.
        # Prefer agent-scoped credentials and translate them only inside the
        # backend process (for example QUANT_TRADER_OPENAI_API_KEY becomes
        # OPENAI_API_KEY). The OpenKitty host's generic credentials are never
        # passed to this professional agent by its manifest.
        for key in self.env_allowlist:
            scoped_key = "QUANT_TRADER_" + key
            value = self.environ.get(scoped_key)
            if value in (None, ""):
                value = self.environ.get(key)
            if value not in (None, ""):
                child_env[key] = value
        for key, value in self.process_env_defaults.items():
            if key in self.env_allowlist:
                child_env.setdefault(key, value)
        return child_env

    def _resolve_python(self) -> Optional[str]:
        candidate = Path(self.python).expanduser()
        if candidate.is_absolute() or candidate.parent != Path("."):
            resolved = candidate.resolve()
            return str(resolved) if resolved.is_file() else None
        found = shutil.which(self.python, path=self.environ.get("PATH"))
        return found

    def _normalize_response(self, raw: Any) -> Dict[str, Any]:
        if not isinstance(raw, dict):
            return _error(self.name, "%s backend response must be a JSON object." % self.name)
        status = str(raw.get("status") or "").strip().lower()
        status = {"completed": "success", "failed": "error"}.get(status, status)
        if status not in {"success", "degraded", "unavailable", "error"}:
            return _error(self.name, "%s backend response has an invalid status." % self.name)
        output = raw.get("output", "")
        if not isinstance(output, str):
            return _error(self.name, "%s backend output must be a string." % self.name)
        data = raw.get("data", {})
        if not isinstance(data, dict):
            return _error(self.name, "%s backend data must be an object." % self.name)
        warnings = raw.get("warnings", [])
        if not isinstance(warnings, list) or any(not isinstance(item, str) for item in warnings):
            return _error(self.name, "%s backend warnings must be a string array." % self.name)
        artifacts = raw.get("artifacts", [])
        if not isinstance(artifacts, list):
            return _error(self.name, "%s backend artifacts must be an array." % self.name)
        return {
            "backend": self.name,
            "status": status,
            "output": output,
            "data": data,
            "warnings": warnings,
            "artifacts": artifacts,
        }


def build_backend_request(
    backend: str,
    request_id: str,
    mode: str,
    input_data: Mapping[str, Any],
    run_dir: Path,
    market_data_provenance: Optional[Mapping[str, Any]] = None,
) -> Dict[str, Any]:
    """Create the immutable, explicitly non-live request sent to a backend."""

    request = {
        "protocol": BACKEND_PROTOCOL,
        "backend": backend,
        "request_id": request_id,
        "capability": "analyze",
        "mode": mode,
        "input": dict(input_data),
        "run_dir": str(run_dir),
        "safety": {
            "research_only": mode == "research",
            "paper_trading_only": mode == "paper",
            "live_trading": False,
            "order_submission": False,
        },
    }
    # This field lives outside caller-controlled ``input``.  The professional
    # agent adds it only after it fetched and validated external observations,
    # letting backend shims distinguish real market data from synthetic test
    # fixtures without trusting a user-supplied provenance label.
    if market_data_provenance:
        request["market_data_provenance"] = dict(market_data_provenance)
    return request
