# macs core code walkthrough

[简体中文](code-walkthrough.md) | [English](code-walkthrough.en.md) · [Project introduction](../../README.en.md) · [Full request lifecycle, Chinese](../../internal/README.md)

Start with a real task, then follow recovery, retained experience, and document delivery. Paths and symbols refer to the current source. `internal/app` still contains host implementations being migrated into smaller packages.

## 1. Understand the three layers

```text
cmd/macs: executable entry point and version handoff
    ↓
internal/app: configuration, lifecycle, Gateway, Agent, and database assembly
    ↓
internal/* and pkg/*: domain rules, tool contracts, implementations, reusable components
```

The root [`assets.go`](../../assets.go) is a separate resource package. It uses `go:embed` for the Web UI, built-in Skills, and configuration template. Business logic belongs in application or domain packages. Stable domain packages must not depend on `internal/app`.

## 2. Startup: from main to running services

Begin at [`cmd/macs/main.go`](../../cmd/macs/main.go). It assigns the release version to `app.CurrentVersion` and calls `app.Main()`.

The main host code is in [`internal/app/mac.go`](../../internal/app/mac.go):

```text
Main → run → configuration and workspace resolution → NewBot → ClawBot.Run
```

`FindConfig` searches explicit, project, and user configuration locations and can generate a configuration from the embedded template on first run. `NewBot` assembles the application; `ClawBot.Run` runs configured services. Search for these functions before reading the large file sequentially.

## 3. Follow one Web Agent request

`GatewayService.handleChat` in [`internal/app/gateway.go`](../../internal/app/gateway.go) receives `POST /api/chat`, parses the request, and selects its task path. Agent work enters `processTaskWithSession`, which binds the workspace, session, execution identity, cancellation, and applicable resume state to the execution context.

```text
Web request
  → Gateway: parse and route
  → processTaskWithSession: establish execution context
  → LLMAgent.Generate: drive model and tools
  → validate results and persist messages and task state
  → JSON or SSE
```

This describes Agent mode. Simple chat uses a separate model-call path in [`simple_chat.go`](../../internal/app/simple_chat.go). Direct media generation also has its own task snapshots. Every send action in the UI does not enter the Agent tool loop.

## 4. The Agent loop: from reasoning to action

The main entry is [`LLMAgent.Generate`](../../internal/app/mac.go). It acquires an execution lease and restores a valid turn checkpoint when resuming. It then establishes turn-scoped tool activation, memory, Skills, and model context.

Trace these state transitions:

1. How the task, messages, and active plan enter model context.
2. Whether the model produces text or tool calls.
3. How calls pass through the Registry and Middleware.
4. How tool receipts return to the message sequence for another model step.
5. When execution continues, completes, is cancelled, or fails.
6. Which states are checkpointed around tools, and which outcomes enter the session or applicable long-term memory.

[`internal/agentloop/`](../../internal/agentloop/) contains neutral turn-state, lease, and recovery rules. [`internal/modelruntime/`](../../internal/modelruntime/) handles model-call policies. These have different responsibilities from host assembly.

One acceptance tool is [`goal_verify`](../../internal/tools/testing/goal_verify.go), which returns `PASS / FAIL / PARTIAL` with check evidence. Its conclusions apply to the selected checks; the existence of a file does not establish an entire task's completion. See [goal verification, Chinese](goal-verification.md).

## 5. Tools, Skills, and Pipelines

A concrete action implements [`toolcontract.Tool`](../../internal/toolcontract/tool.go):

```go
type Tool interface {
    Name() string
    Description() string
    Parameters() map[string]interface{}
    Execute(ctx context.Context, params map[string]interface{}) (string, error)
}
```

[`internal/toolregistry/registry.go`](../../internal/toolregistry/registry.go) handles registration and execution. `tool_detail` provides on-demand discovery, and [`activation.go`](../../internal/toolregistry/activation.go) manages turn-scoped activation. Implementations live in `internal/tools/<category>/`; execution policies live in [`internal/middleware/`](../../internal/middleware/).

Skills load and activate procedural instructions through [`internal/skills/`](../../internal/skills/). Explicit Skill execution is coordinated by [`skill_run.go`](../../internal/app/skill_run.go). Activating a Skill supplies a method and constraints; it does not itself create another Agent.

Pipelines organize multi-step work. [`internal/app/pipeline.go`](../../internal/app/pipeline.go) coordinates the host, while [`internal/pipeline/`](../../internal/pipeline/) owns step checkpoints, output and source contracts, and rework rules. AI Employee supplies business identities, routing, and workflow bindings; start at [`ai_employee.go`](../../internal/app/ai_employee.go).

## 6. AgentOS: connect execution to business applications

Start at [`internal/app/agent_os_api.go`](../../internal/app/agent_os_api.go) for HTTP integration and host coordination, then follow protocol and persistence models in [`internal/agentos/`](../../internal/agentos/).

```text
Business application submits a new task
  → 202 + task_id + links
  → durable queue, lease, and execution host
  → core Agent / Pipeline
  → final task snapshot and deliverables
```

This reuses the core execution capability. Durable snapshots are the final source of task state. SSE supplies recent progress and bounded replay; clients must query a snapshot after the replay window is lost.

For recovery, read [`internal/session/checkpoint.go`](../../internal/session/checkpoint.go), [`recovery.go`](../../internal/session/recovery.go), [`internal/app/recovery_host.go`](../../internal/app/recovery_host.go), and tool receipts together. **Resume at established model or tool boundaries. Do not automatically replay a write with an uncertain outcome.** Changes here must preserve idempotency, cancellation, lease expiry, and consistent terminal state.

See the [AgentOS API](../agent-os-api.md) and [runtime reliability](runtime-reliability.md), currently in Chinese.

## 7. Why memory, sessions, and knowledge remain separate

| Component | Main question | Entry point |
| --- | --- | --- |
| memory | What to retain, recall, correct, or forget | [`internal/memory/`](../../internal/memory/), [memory service](memory-service.md) |
| session | How to retain execution history and resume a turn | [`internal/session/`](../../internal/session/) |
| conversation | How to maintain and compact active conversation context | [`internal/conversation/`](../../internal/conversation/) |
| RAG | Which passages to retrieve from imported material | [`internal/ragmodel/`](../../internal/ragmodel/), [`internal/rag/`](../../internal/rag/), [`internal/tools/knowledge/`](../../internal/tools/knowledge/) |

A lasting preference for cited reports belongs in memory. A tool receipt belongs to execution history. A reference specification belongs to the knowledge source. All three may help a task, but they have different lifecycles. Forgetting a memory item does not automatically delete every original message or derived record.

## 8. Two implementations that make experience reusable

**Document template profiles.** [`internal/tools/content/extract.go`](../../internal/tools/content/extract.go) exposes inspection and filling. The model proposes declarative `field_mapping`, `table_cells`, and `repeat_rows`; a shared OOXML engine executes them. [`docx_template_profile.go`](../../internal/tools/content/docx_template_profile.go) stores reusable rules against a structural fingerprint. The Web host in [`document_automation_api.go`](../../internal/app/document_automation_api.go) uses the same engine. See [document automation, Chinese](document-automation.md).

**UI workflow learning.** [`internal/tools/computer/`](../../internal/tools/computer/) coordinates observation, actions, and verification through existing browser and desktop backends. [`internal/workflowlearn/compiler.go`](../../internal/workflowlearn/compiler.go) compiles a successful Session into parameters, actions, and assertions. [`workflow_learner.go`](../../internal/tools/skills/workflow_learner.go) adapts it to an Agent tool. Replay still calls `computer`. See [computer use, Chinese](computer-use.md).

Both mechanisms require checking actual outcomes. Changes to layouts, locators, or template structure can require profile or recipe revalidation.

## 9. Choose an entry point for your first change

| Change | Start here |
| --- | --- |
| Add a tool | A neighboring `internal/tools/<category>/` implementation, its contract, discovery and registration, policies, and adjacent tests |
| Adjust recall or forgetting | `internal/memory/` and host call sites; distinguish long-term items from history |
| Change cancellation or recovery | AgentOS, session checkpoints, the recovery host, receipts, and terminal-state tests |
| Extend DOCX support | Existing structural inspection, stable addresses, declarative mapping, and template fixtures; avoid company-specific branches |
| Modify Web presentation | Existing `web/` HTML/CSS/JS; preserve the design, check changed scripts, and rebuild embedded resources |

Find symbols from the repository root:

```bash
rg -n 'func .*handleChat|func .*processTaskWithSession' internal/app/gateway.go
rg -n 'func .*Generate|WithNewTurnActivation' internal/app/mac.go
rg -n 'type Tool interface|func .*Execute' internal/toolcontract internal/toolregistry
```

Inspect `git status` and relevant diffs first. After editing, run nearby tests, `git diff --check`, and the main build when applicable. Known migration failures are recorded in [`AGENTS.md`](../../AGENTS.md); distinguish them from new regressions. Dependency rules are documented in [Go modularization, Chinese](go-modularization.md).
