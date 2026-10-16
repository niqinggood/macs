# OpenKitty isolated finance-employee worker

`quant-trader` is one isolated professional-agent process shared by three
strictly separated AI employee contracts. Each employee has one capability and
exactly one backend; a request can never fan out across the three projects.

| Employee ID | User-facing employee | Activation | Capability | Only backend | Responsibility |
|---|---|---|---|---|---|
| `quant-trader` | AI交易员 | `@交易员` | `trading` (`analyze` is legacy-compatible) | TradingAgents | Market structure, timing, trading thesis, and paper-trade risk |
| `investment-research-manager` | AI投研经理 | `@投研经理` | `research-management` | AI Hedge Fund | Multi-discipline research, evidence conflicts, and paper-position advice |
| `financial-intelligence-analyst` | AI金信分析师 | `@金信分析师` | `financial-intelligence` | FinGPT | Supplied announcements, news, research reports, and sentiment text |

It is not registered as a general-purpose tool or skill. OpenKitty reaches it
only through an explicitly activated AI employee:

```text
user `@交易员 ...`       -> AI交易员 pipeline       -> TradingAgents only
user `@投研经理 ...`     -> AI投研经理 pipeline     -> AI Hedge Fund only
user `@金信分析师 ...`   -> AI金信分析师 pipeline   -> FinGPT only
```

Messages without an OpenKitty-recognized `@` activation do not activate these
employees. Alias registration belongs to the host; this worker authorizes the
three exact employee IDs above.

The OpenKitty worker itself does **not** import those projects, clone
repositories, install packages, hold broker credentials, or submit live
orders. The dedicated backend child process imports only its configured
upstream repository. Missing or failed backends are reported as
`unavailable`/`error`; the wrapper never invents an analysis to fill a gap.

## OpenKitty protocol

The process reads one JSON object per stdin line and writes exactly one JSON
object per stdout line. Logs from external adapters stay on their stderr and
are not copied to stdout.

Protocol: `openkitty.professional-agent/v1`

Health request:

```json
{"protocol":"openkitty.professional-agent/v1","request_id":"health-1","agent_id":"quant-trader","employee_id":"quant-trader","tenant_id":"default","session_id":"s1","execution_id":"exec-1","step_index":0,"capability":"health","input":"{}"}
```

AI交易员 request (`analyze` remains accepted only for the legacy
`quant-trader` identity; new callers should send `trading`):

```json
{"protocol":"openkitty.professional-agent/v1","request_id":"analysis-1","agent_id":"quant-trader","employee_id":"quant-trader","tenant_id":"default","session_id":"s1","execution_id":"exec-1","step_index":0,"capability":"trading","input":"{\"mode\":\"research\",\"symbols\":[\"AAPL\"],\"question\":\"Assess the trading setup and downside risk\"}"}
```

Every response contains:

```json
{
  "protocol": "openkitty.professional-agent/v1",
  "request_id": "analysis-1",
  "agent_id": "quant-trader",
  "status": "completed|degraded|unavailable|failed",
  "output": "backend-derived text only",
  "data": {},
  "warnings": [],
  "artifacts": [],
  "error": ""
}
```

The worker enforces `employee_id` to capability to backend as a one-to-one
authorization contract. Explicit `frameworks` must contain only that employee's
assigned backend. Only `research` and `paper` modes are accepted. Structured
live/order flags are rejected, and every downstream
request carries `live_trading=false` and `order_submission=false`. Natural
language requests to place or execute a real order are rejected before any
backend is invoked. This is a research/simulation boundary, not investment
advice and not a broker integration.

## Chinese A-share inputs and market data

Natural-language requests such as `@交易员 分析下新安股份` are resolved
through a bounded Chinese security-master lookup; no per-stock name table is
hardcoded. Common explicit forms (`600596`, `SH600596`, `600596.SH`) are
normalized locally to the Yahoo-compatible `600596.SS`. Ambiguous or unknown
names fail with a request for an explicit code rather than selecting a guess.

For an all-mainland-equity request, the worker retrieves up to 180 completed,
forward-adjusted daily OHLCV observations from an Eastmoney primary source and
a Tencent HTTPS fallback over fixed allowlisted endpoints. Each request uses a
bounded shared deadline, a 2 MiB response cap, and at most one retry for an
idempotent GET. An unfinished current China-market daily bar is excluded before
15:10 Asia/Shanghai. The last completed observation becomes TradingAgents'
`as_of` and AI Hedge Fund's `end_date`; volume from both providers is normalized
from hands/lots to shares.

The same validated rows are then passed through each upstream graph's existing
request-fixture transport, but are labeled `kind=external`; `provider` records
the source actually used (`eastmoney-push2his`, `tencent-newfqkline`, or a
multi-source marker). Caller-provided fixtures remain explicitly
unverified/synthetic and therefore degraded. For A-shares, AI交易员 defaults to
TradingAgents' market analyst. AI投研经理 defaults to technical, fundamentals,
valuation, and news-sentiment analysts; an explicit technical-only A-share
scope is rejected as incomplete. AI Hedge Fund `Default decision` fallbacks and
non-positive-confidence results are degraded and never counted as success.

AI金信分析师 receives no OHLCV. It requires at least 40 characters of
caller-supplied announcement, news, research-report, or sentiment text. A bare
instruction such as “分析新安股份” fails with a request to paste or upload the
source. It does not fetch quotes or source material, forecast prices, issue a
trade recommendation, or submit orders.

## Backend isolation

The worker is isolated from OpenKitty's main agent and invokes only the backend
authorized for the calling employee. Because the three roles share one manifest
and supervisor process, this is **not** credential isolation between the three
employee identities: the manifest may provision all reviewed backend bindings
to the worker. Each child backend still receives a narrow allowlisted
environment. Deploy three manifests/processes if role-to-role credential
isolation is required.

The default deployment uses three separate process transports. The checked-out
repositories and their Python interpreters use this layout (shown here for the
current `E:/openkitty` workspace):

| Backend | Repository | Isolated interpreter |
| --- | --- | --- |
| TradingAgents | `E:/openkitty/.openkitty/runtime/quant-trader/TradingAgents` | `E:/openkitty/.openkitty/runtime/quant-trader/venvs/tradingagents/Scripts/python.exe` |
| AI Hedge Fund | `E:/openkitty/.openkitty/runtime/quant-trader/ai-hedge-fund` | `E:/openkitty/.openkitty/runtime/quant-trader/venvs/ai-hedge-fund/Scripts/python.exe` |
| FinGPT | `E:/openkitty/.openkitty/runtime/quant-trader/FinGPT` | `E:/openkitty/.openkitty/runtime/quant-trader/venvs/fingpt/Scripts/python.exe` |

Do not point multiple backends at one shared venv. By default the worker
discovers this layout relative to `OPENKITTY_AGENT_RUN_DIR` or the reviewed
agent directory, so moving the whole workspace does not require rewriting
absolute paths. Discovery is bounded to these OpenKitty roots; it never scans
the filesystem. `QUANT_TRADER_RUNTIME_ROOT` can select another reviewed root,
and `QUANT_TRADER_AUTO_DISCOVERY=false` disables discovery. Explicit backend
HTTP/process variables always take precedence and are never mixed with an
automatically discovered transport.

Bundled transports also receive the reviewed non-secret defaults documented
in `env.example` when no explicit backend allowlist/value is supplied. In
particular, auto-discovered FinGPT selects
`fingpt_minimax_callable:analyze`, while TradingAgents defaults to the
MiniMax-China provider profile, Chinese output, and one debate/risk round.
These defaults never contain credentials; API keys still have to arrive via
the agent-scoped `QUANT_TRADER_*` secret variables.

Each backend must use exactly one of these transports:

1. A dedicated Python interpreter (`*_PYTHON`) running the bundled
   upstream-specific shim. `*_ENTRYPOINT` or `*_MODULE` may override it.
2. An explicitly deployed and reviewed HTTP endpoint (`*_HTTP_ENDPOINT`).

See `env.example` for all variables. Paths and endpoints are configuration,
not installation instructions. The wrapper never clones or modifies an
external repository. All HTTP examples are commented out by default; never
configure HTTP and process transport for the same backend at once.

Bundled shims call the verified upstream boundaries directly:

- TradingAgents: `TradingAgentsGraph.propagate`.
- AI Hedge Fund: `src.main.run_hedge_fund` with an in-memory paper portfolio.
- FinGPT: a reviewed `module:function` selected by
  `QUANT_TRADER_FINGPT_CALLABLE`, because the upstream repository has no
  single stable runtime API. The bundled
  `fingpt_minimax_callable:analyze` adapter calls FinGPT's upstream FinoGrid
  MiniMax sentiment provider. Its output is financial sentiment research, not
  a price forecast.

Process adapter stdin receives one JSON object:

```json
{
  "protocol": "openkitty.professional-agent/backend/v1",
  "backend": "fingpt",
  "request_id": "analysis-1",
  "capability": "analyze",
  "mode": "research",
  "input": {},
  "run_dir": ".../analysis-1-uuid/fingpt",
  "safety": {
    "research_only": true,
    "paper_trading_only": false,
    "live_trading": false,
    "order_submission": false
  }
}
```

It must emit one JSON object on stdout with `status`, `output`, `data`,
`warnings`, and `artifacts`. HTTP adapters accept and return the same objects.
Example backend response:

```json
{
  "status": "success",
  "output": "Evidence-backed backend result",
  "data": {"score": 0.42},
  "warnings": [],
  "artifacts": [{"path": "sentiment.json", "media_type": "application/json"}]
}
```

OpenKitty creates a host-owned run directory and injects its absolute path via
`OPENKITTY_AGENT_RUN_DIR`. The worker creates a unique request directory below
it. Artifacts must already exist beneath the backend's per-request `run_dir`.
Missing files, path traversal, absolute paths outside the request directory,
and symlink escapes are discarded with warnings. Paths returned to OpenKitty
are always relative to the host-owned run directory.

Child processes receive a narrow environment. Add only the variables a backend
actually needs to its `*_ENV_ALLOWLIST`. Do not add brokerage credentials: this
agent has no live-trading capability.

Secrets are agent-scoped. Configure only the required `QUANT_TRADER_*` values,
such as `QUANT_TRADER_OPENAI_API_KEY`,
`QUANT_TRADER_FINANCIAL_DATASETS_API_KEY`, or
`QUANT_TRADER_MINIMAX_API_KEY`; the worker translates an explicitly
allowlisted value to the conventional backend name only inside that backend
process. For the bundled FinGPT integration,
`QUANT_TRADER_MINIMAX_BASE_URL` selects the account region and
`QUANT_TRADER_MINIMAX_MODEL` selects the model. The host's generic credentials
are not inherited.

TradingAgents' reviewed China-region profile is separately scoped through
`QUANT_TRADER_MINIMAX_CN_API_KEY` and the
`QUANT_TRADER_TRADINGAGENTS_*` provider/model settings shown in `env.example`.
The child receives only their conventional, explicitly allowlisted names;
these values are not added to the OpenKitty host's generic model environment.

Backend wall times are bounded and may be overridden with the corresponding
`QUANT_TRADER_*_TIMEOUT_SECONDS` setting. TradingAgents defaults to 900 seconds
because its multi-role debate/risk graph is substantially longer than the
single-call AI Hedge Fund and FinGPT adapters, which default to 300 seconds.
The enclosing professional-agent pipeline allows 1200 seconds so cleanup and
the remaining backends are not pre-empted by the inner TradingAgents budget.

Never put a real key in `env.example`, this README, a committed config, a test
fixture, or a test report. Inject it at process start from the deployment's
secret store, and rotate any key that has been pasted into chat or logs.

## Run

Direct worker execution is for development/health diagnostics only; normal
user activation must go through `@交易员`, `@投研经理`, or `@金信分析师`. From
this directory:

```powershell
python -u worker.py
```

For a one-shot health probe:

```powershell
$env:OPENKITTY_AGENT_RUN_DIR = (New-Item -ItemType Directory -Force .\test-run).FullName
'{"protocol":"openkitty.professional-agent/v1","request_id":"h1","agent_id":"quant-trader","employee_id":"quant-trader","tenant_id":"default","session_id":"s1","execution_id":"e1","step_index":0,"capability":"health","input":"{}"}' | python -u worker.py
```

## Test

The implementation and tests use only the Python standard library:

```powershell
python -m unittest discover -s tests -v
```
