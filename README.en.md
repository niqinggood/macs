<h1 align="center">macs</h1>

<p align="center"><a href="README.md">简体中文</a> | <strong>English</strong></p>

<p align="center">
  <strong>Multi-Agent Collaboration System</strong><br>
  Help Agents deliver verifiable work and turn experience into reusable capabilities
</p>

<p align="center">
  <a href="go.mod"><img alt="Go 1.26" src="https://img.shields.io/badge/Go-1.26-00ADD8?logo=go&amp;logoColor=white"></a>
  <a href="#architecture"><img alt="Embedded Web UI" src="https://img.shields.io/badge/Web_UI-Embedded-8B3545"></a>
  <a href="#data-and-deployment"><img alt="SQLite and MySQL" src="https://img.shields.io/badge/Storage-SQLite%20%2F%20MySQL-58677C"></a>
  <a href="LICENSE"><img alt="Source Available License" src="https://img.shields.io/badge/License-Source_Available-7B4554"></a>
</p>

<p align="center">
  <a href="#core-strengths">Core strengths</a> ·
  <a href="#positioning-and-comparison">Positioning</a> ·
  <a href="#capabilities">Capabilities</a> ·
  <a href="#quick-start">Quick start</a> ·
  <a href="#memory-and-forgetting">Memory</a> ·
  <a href="#business-system-integration">Integration</a> ·
  <a href="#core-code-walkthrough">Code walkthrough</a> ·
  <a href="#documentation">Documentation</a>
</p>

---

## What is macs?

**macs is an AI Agent work system you can deploy yourself.** Describe a goal in natural language and let Agents inspect information, understand a project, use tools, coordinate execution, and deliver files. Applications can also invoke these capabilities through an API.

The product provides two main modes:

- **Agent mode:** interpret goals, plan tasks, use tools, and coordinate multiple agents to perform complex work and verify deliverables.
- **Conversational mode:** everyday questions, writing, image generation, and music creation. Image generation supports platform models such as Nano Banana and `gpt-image2`; available models depend on the account's model catalog.

The application is primarily written in Go and embeds its native Web UI and built-in Skills. Chat, multi-agent collaboration, Pipelines, AI Employee, and AgentOS share the core Agent runtime, so methods validated in personal work can also support team and business workflows.

The formal product name is **macs**, its Chinese name is **麦卡斯**, and its nickname is **openkitty**. Existing technical names such as `openkitty.exe`, `openkitty-config.yaml`, and `OPENKITTY_*` remain for compatibility.

| Use | What it supports |
| --- | --- |
| Personal assistant | Questions, research, coding, file processing, knowledge retrieval, and everyday work |
| Project collaboration | Agents with different responsibilities for research, writing, analysis, and review |
| Automation workbench | Document templates, browser and desktop actions, and reusable successful procedures |
| Business task service | Asynchronous task submission, status, progress, results, and deliverables for existing applications |

## Core strengths

**macs brings natural-language tasks, real tool execution, recoverable workflows, result verification, and reusable experience into one deployable Agent system.** Coding, networking, and document processing are ways that system performs work.

### 1. One runtime for personal use and business applications

Web conversations, multi-agent collaboration, Pipelines, AI Employee, and AgentOS reuse core execution capabilities. Tools, Skills, and business methods validated for one entry point can be used in team workflows or exposed through an API, reducing repeated assembly.

### 2. Stateful long tasks with recovery and inspectable results

Durable tasks, tool receipts, leases, and checkpoints describe execution. Pipelines also constrain step inputs, outputs, and rework. Applications can query final snapshots, add instructions, or cancel work. Recovery follows established boundaries; writes with uncertain outcomes stop automatic replay.

### 3. Connect flexible reasoning with repeatable execution

Agents handle interpretation and exploration. Pipelines, declarative template mappings, and deterministic executors make reviewed procedures repeatable. For example, a model can propose Word field mappings while a shared DOCX engine fills the template and checks structure and completeness. Successful mappings can be reused for later batches.

### 4. Retain preferences, knowledge, and successful methods separately

Memory retains applicable preferences and experience; RAG retrieves source material; Skills organize methods; template profiles retain mapping rules. `workflow_learner` can compile successful `computer` sessions into parameterized recipes with success assertions. Memory can be corrected and forgotten. Reuse still requires verification, and not every task automatically becomes a skill.

### 5. Deliver an inspectable implementation with a usable workbench

The Go application embeds the Web UI and built-in Skills and can use SQLite locally. Models, tools, and business logic can be extended through the provided interfaces and under the applicable license. Start on a personal computer and deploy as a service when needed. Browsers, Office/PDF conversion, and external platforms retain their own dependencies.

These characteristics are supported by existing implementation. Actual quality, time, and cost must be evaluated against the selected models and tasks.

## Positioning and comparison

macs is designed for people and teams that want to **operate their own deployment, adapt it to their business, and make Agent execution part of ongoing workflows**.

| Product | Direction emphasized in official information | macs characteristics to consider |
| --- | --- | --- |
| [OpenClaw](https://github.com/openclaw/openclaw) | A deployable assistant, messaging channels, Gateway, and extensions | Business Pipelines, durable task APIs, and document template delivery assembled in one product |
| [WorkBuddy](https://cloud.tencent.com/product/workbuddy) | An office workbench, expert collaboration, and Tencent ecosystem connections | An inspectable and adaptable Go implementation under its license, embedded Web UI, and configurable business capabilities |
| [Hermes Agent](https://github.com/NousResearch/hermes-agent) | Memory, learning from experience, and skill accumulation | Memory alongside business workflow contracts, template profiles, and UI recipes with assertions |
| [Codex](https://developers.openai.com/blog/codex-as-a-platform) | An Agent harness with CLI, SDK, and app-server integration | An assembled workbench with document processing, memory, messaging, and business orchestration |

Other products also offer memory, tools, collaboration, and business integration. This is a comparison of direction, not evidence that macs leads in coding success, speed, cost, or memory quality. Reviewed on 2026-10-16. See [positioning and evidence](docs/product-positioning.en.md) ([简体中文](docs/product-positioning.md)).

## Typical workflows

These examples show how the mechanisms support practical work. Results depend on the model, inputs, and runtime environment.

### 1. Coding: understand, modify, verify, and deliver

Agents can inspect repository structure and conventions, locate problems with search, LSP, AST, and dependency information, edit files, and run tests, builds, static checks, and behavioral acceptance checks.

- **Understand existing projects:** read project guidance, find symbols, inspect definitions and references, and determine change scope.
- **Perform engineering work:** edit files, coordinate cross-file changes, run commands, and use Git/worktree tools when applicable.
- **Use the real project environment:** work with the compilers, dependencies, and material already present on the deployment host.
- **Retain verification evidence:** `goal_verify` returns `PASS / FAIL / PARTIAL` for selected checks, including executable behavior assertions where configured.
- **Expose coding tasks to applications:** use AgentOS to submit work, inspect progress, add requirements, cancel execution, and retrieve results.

Examples include fixing an existing project, checking interface changes, batch edits, generating tests, and serving Agent tasks inside a development platform.

Source: [development tools](internal/tools/development/), [goal verification](docs/architecture/goal-verification.md), [runtime reliability](docs/architecture/runtime-reliability.md).

### 2. Research: connect discovery, reading, and subsequent work

Agents can search, read original material, inspect pages, and extract structured information before continuing with analysis, reports, knowledge retrieval, or business actions.

- Combine search, fetching, platform readers, and browser tools according to available backends.
- Use `agent_reach` for read-only search, reading, trends, and transcription routes through configured CLIs, APIs, or browsers, with native fallbacks.
- Use the browser and `computer` for interactive pages and available signed-in sessions.
- Connect internal APIs and services within configured access boundaries.

Examples include sourced research, industry monitoring, and preparing documents from business information. Platform access depends on the actual account, backend, and environment.

Source: [network tools](internal/tools/network/), [Agent Reach routing](internal/tools/network/agent_reach.go), [computer use](docs/architecture/computer-use.md).

### 3. Documents: work with existing templates and delivery requirements

The document engine supports structured filling of existing Word templates, alongside extraction and report generation.

- Inspect fields, content controls, bookmarks, and table structure.
- Fill using declarative mappings, stable cell addresses, and repeating rows.
- Review mappings before generation and check structure and unresolved content afterward, with previews and visual review when required.
- Save reusable profiles by template fingerprint for later batches with new input data.

Examples include fixed-format reports, contract information, records, batch forms, and presentation material. Mapping is based on document structure and data contracts.

Documentation: [user manual, Chinese](docs/macs-user-manual.md), [document automation, Chinese](docs/architecture/document-automation.md).

### 4. Automation: reuse successful interface work

The unified `computer` tool follows observation, action, and verification through existing browser and desktop backends. `workflow_learner` can derive input parameters and success assertions from a successful Session and replay and evaluate the resulting recipe with new inputs.

Changes to layouts, locators, or desktop environments require renewed observation and verification.

### 5. Memory: retain useful context and maintain it

Agents can selectively use long-term preferences, project conventions, and relevant historical references. Users can inspect sources, edit items, adjust recall, and forget information. Explicit lasting requirements and candidates requiring review receive different treatment. See [memory and forgetting](#memory-and-forgetting).

### 6. Integration: use the same capabilities in business workflows

Use Web and messaging channels directly, or integrate through AgentOS, SDKs, Pipelines, AI Employee, MCP, and tools. Long tasks have persistent state, idempotent submission, events, cancellation, and recovery mechanisms for integration with application lifecycles.

## Capabilities

| Area | Included capabilities |
| --- | --- |
| Chat and models | Agent and simple-chat modes, message history, attachments, and configured cloud or local models |
| Agent execution | Planning, model/tool loops, result checks, file delivery, timeouts, and cancellation |
| Coding | Search, LSP, AST, cross-file changes, refactoring, tests, builds, and goal verification |
| Research | Web search, page and platform reading, source checking, and research workflows |
| Memory | Preferences, conventions, historical references, recall controls, corrections, and forgetting |
| RAG | Import, chunking, keyword and vector retrieval, and configured enhanced retrieval and reranking |
| Documents | Extraction, Word mappings, repeating rows, reports, and office artifacts |
| Browser and desktop | Observation, interface actions, screenshots, verification, and parameterized reuse |
| Multi-agent teams | Role-based division of work, sequential and parallel collaboration, team templates, and delegation |
| Pipeline | Multi-step workflows, input/output contracts, conditions, approvals, and checkpoint recovery |
| AI Employee | Business identity, routing, Skills and workflow bindings, and scheduled work |
| Messaging channels | Configured Feishu, WeChat, DingTalk, WeCom, QQ, Telegram, email, and other integrations |
| Multimodal and media | Image understanding, speech processing, image generation (Nano Banana, gpt-image2, and other configured models), music creation, and video through configured services |
| MCP, Skills, and Hooks | Extend tools and information sources, organize methods, and connect lifecycle handling |
| AgentOS and SDKs | Async submission, durable snapshots, SSE, idempotency, follow-ups, cancellation, and artifacts |

Specific tools, channels, and media features require their respective services, accounts, or runtimes. Installing the main application does not configure every extension backend.

## Quick start

### 1. Get the source

Source builds require Git and **Go 1.26**. The Web UI is native HTML/CSS/JavaScript and has no frontend bundling step.

```bash
git clone https://github.com/niqinggood/macs.git
cd macs
```

### 2. Copy configuration and start

Copy the template on first use. Keep an existing personal configuration and start with that file if already configured.

Windows / PowerShell:

```powershell
Copy-Item .\openkitty-config.template.yaml .\openkitty-config.yaml
go run ./cmd/openkitty --config .\openkitty-config.yaml --path . --port 8090
```

Linux / macOS:

```bash
cp openkitty-config.template.yaml openkitty-config.yaml
go run ./cmd/openkitty --config ./openkitty-config.yaml --path . --port 8090
```

Open `http://127.0.0.1:8090/web/`, using the actual address reported by the process. The template listens on `127.0.0.1:8080`; the example overrides its port to 8090. Existing deployments retain their own protocol, listener, and authentication settings.

The Web UI includes a **中文 / EN** switch. Some extended pages and longer help content are not fully translated yet.

### 3. Configure an available model

In Web settings, configure a provider endpoint, model name, and credentials. Select a model for the assistant or inherit the current default. Existing Ollama, vLLM, or compatible services can be configured by their actual endpoints. Install and start local model services and prepare models separately.

Model keys can reference environment variables. For example, configure a compatible service in your existing `model_cfg`:

```yaml
default_model: my-model
model_cfg:
  my-model:
    type: openai
    base_url: https://your-model-provider.example/v1
    model_name: REPLACE_WITH_MODEL_NAME
    api_key_env: MACS_MODEL_API_KEY
    max_tokens: 4096
```

Set the variable in the startup terminal before starting or restarting macs:

```powershell
# Windows / PowerShell
$env:MACS_MODEL_API_KEY = "REPLACE_WITH_YOUR_MODEL_API_KEY"
```

```bash
# Linux / macOS
export MACS_MODEL_API_KEY='REPLACE_WITH_YOUR_MODEL_API_KEY'
```

Replace placeholders with your configuration. Edit an existing YAML section instead of appending duplicate top-level keys. An empty assistant `model` can inherit the current default text model. Already submitted tasks retain their model settings.

Details: [model configuration and user manual, Chinese](docs/macs-user-manual.md), [configuration template](openkitty-config.template.yaml).

### 4. Submit a first task

Start with clear inputs and delivery requirements:

```text
Read the material in the specified directory and create a Markdown summary.
Include the main conclusions, referenced files, and questions still requiring confirmation.
```

Use Agent mode for tools, Agent memory, and multi-step execution, with the required capabilities enabled. Simple chat and direct media generation have separate execution paths.

### Build the executable

```powershell
go run ./cmd/releaseguard
go build -buildvcs=false -trimpath -o openkitty.exe ./cmd/openkitty
```

On Linux/macOS, use `-o openkitty`. Existing binary distributions do not require Go. Release packaging uses the [existing build scripts](scripts/README.md). Rebuild the executable after changing `web/`, `builtin/`, or other embedded resources.

Windows executables support double-click startup. Current macOS and Linux builds are command-line executables; grant execution permission and start from a terminal. Uniform desktop application packaging is still incomplete.

## Memory and forgetting

Memory is enabled by default. Change it in Web **Settings → Work & Memory → Agent Memory**, or through configuration:

```yaml
memory_config:
  enabled: true
```

Each assistant also has a `has_memory` setting. Changes apply to new tasks. Simple chat bypasses the Agent long-term recall path.

| Stage | Current mechanism |
| --- | --- |
| Retain | Explicit lasting requirements, user-maintained preferences and conventions, and applicable experience and summaries |
| Recall | Select by current question, scope, active state, and context budget; semantic recall requires an embedding backend |
| Maintain | Inspect sources, add and edit items, make corrections, and manage pinning and recall |
| Forget | Forgotten unified memory items leave automatic recall, with retraction protection against revival through older compatibility sources |

For example: “Remember that future project reports should be in English and include references.” Existing items can also be edited or forgotten in management. Explicit current instructions override long-term defaults.

Long-term items, messages, summaries, learning notes, and knowledge sources remain distinct. Source records help explain retrieved references, but do not prove the model used a particular item. Forgetting one item does not automatically erase all derived material.

Details, currently in Chinese: [memory service](docs/architecture/memory-service.md), [memory system summary](docs/architecture/memory-system-summary.md).

## Business system integration

External applications use the **AgentOS asynchronous task contract**. A new task returns `202 + task_id + links`. Use the returned links to query state, subscribe to events, and retrieve results.

```text
Business application submits a task
    → receives task_id and query links
    → macs queues and executes model/tool calls
    → application queries snapshots or subscribes to task SSE
    → application receives terminal state, results, and artifacts
```

This supports:

- Publishing Agents or business roles with capabilities determined by the caller identity.
- Retrying submission with an idempotency key to avoid duplicate tasks.
- Inspecting progress, adding requirements during execution, and cancelling work.
- Persisting tasks, leases, checkpoints, and tool receipts for recovery at established boundaries.
- Integration through Go/Python SDKs or HTTP and SSE.

The following example follows the [AgentOS quick start](docs/agentos-quickstart.md), using its separate port `8081`, configured `document-assistant` role, and business service credential. Configure them in your own deployment first.

```bash
export MACS_SERVICE_TOKEN='REPLACE_WITH_YOUR_SERVICE_TOKEN'

cat > task.json <<'JSON'
{
  "role_id": "document-assistant",
  "message": "Create a work summary: requirements discussion and interface design were completed today."
}
JSON

curl -i --max-time 30 -X POST http://127.0.0.1:8081/api/v1/agent/tasks \
  -H "Authorization: Bearer $MACS_SERVICE_TOKEN" \
  -H 'Content-Type: application/json; charset=utf-8' \
  -H 'Idempotency-Key: summary-example-001' \
  --data-binary @task.json
```

Successful submission means acceptance. Read `links.self` for a durable snapshot and `links.events` for progress. Retry identical content with the same idempotency key; use a new key for a new task.

The final task snapshot is authoritative. SSE only retains a bounded recent replay window; query the snapshot after `replay_reset`. Recovery occurs at established model or tool boundaries, and writes with uncertain outcomes stop automatic replay.

Documentation: [deployment and calls](docs/agentos-quickstart.md), [API contract](docs/agent-os-api.md), [OpenAPI](docs/openapi-agentos.yaml), [SDKs](sdk/README.md), [Python SDK](sdk/python/README.md).

## Architecture

```mermaid
flowchart LR
    Web["Web / messaging channels"] --> Gateway["Gateway"]
    Business["Applications / SDKs"] --> AgentOS["AgentOS"]
    Gateway --> Runtime["Agent runtime"]
    AgentOS --> Runtime
    Workflow["Pipeline / AI Employee"] --> Runtime
    Runtime <--> Models["Configured model services"]
    Runtime <--> Tools["Tool Registry / Middleware"]
    Tools --> Actions["Files / network / code / browser / desktop"]
    Context["Memory / RAG / session context"] --> Runtime
    Runtime --> State["Tasks / checkpoints / tool receipts"]
```

| Concept | Responsibility |
| --- | --- |
| Agent | The executor driving the model/tool loop |
| Tool | One concrete action, such as reading a file, searching, or running tests |
| Skill | Activatable procedural instructions and capability constraints |
| Pipeline | Multi-step Agent orchestration with contracts and checkpoints |
| AI Employee | Business identity, routing, and workflow binding |
| AgentOS | Identity, asynchronous tasks, and recovery contracts around the same runtime |

| Path | Responsibility |
| --- | --- |
| [`cmd/openkitty/`](cmd/openkitty/) | Executable entry point and version injection |
| [`internal/app/`](internal/app/) | Assembly, Gateway, and coordination across domains |
| [`internal/agentloop/`](internal/agentloop/) | Turn state, leases, and recovery |
| [`internal/agentos/`](internal/agentos/) | Protocol, durable tasks, and domain models |
| [`internal/pipeline/`](internal/pipeline/) | Checkpoints and input/output contracts |
| [`internal/memory/`](internal/memory/) | Long-term items, recall, corrections, and forgetting |
| [`internal/tools/`](internal/tools/) | Workspace, network, content, development, and interface tools |
| [`internal/toolregistry/`](internal/toolregistry/) | Discovery, registration, filtering, and execution |
| [`pkg/`](pkg/) | Reusable public Go capabilities |
| [`web/`](web/) | Native Web UI without a bundling step |
| [`builtin/`](builtin/), [`default-skills/`](default-skills/) | Embedded Skills and runtime resources |
| [`scripts/`](scripts/) | Build, quality, and publishing entry points |

## Core code walkthrough

Follow one Agent conversation before exploring recovery and reusable methods:

```text
cmd/openkitty/main.go: main
  → internal/app/mac.go: Main → run → NewBot → ClawBot.Run
  → POST /api/chat
  → internal/app/gateway.go: handleChat → processTaskWithSession
  → internal/app/mac.go: LLMAgent.Generate
  → Tool Registry / Middleware → Tool.Execute → tool receipt
  → further reasoning or result checks → task/session/applicable memory persistence
  → response
```

| Reading goal | Source | Focus |
| --- | --- | --- |
| Model/tool execution | [`LLMAgent.Generate`](internal/app/mac.go), [`agentloop`](internal/agentloop/) | Turn context, loop, cancellation, and recovery boundaries |
| Tool discovery and execution | [`Tool contract`](internal/toolcontract/tool.go), [`Registry`](internal/toolregistry/registry.go), [`Middleware`](internal/middleware/) | Schemas, activation, execution policy, and receipts |
| Durable business tasks | [`AgentOS host`](internal/app/agent_os_api.go), [`domain models`](internal/agentos/), [`checkpoints`](internal/session/checkpoint.go) | Async submission, snapshots, leases, idempotency, and recovery |
| Repeatable work | [`Pipeline`](internal/pipeline/), [`DOCX profiles`](internal/tools/content/docx_template_profile.go), [`recipe compiler`](internal/workflowlearn/compiler.go) | Contracts, fingerprints, parameters, and assertions |
| Retained experience | [`memory`](internal/memory/), [`session`](internal/session/), [`conversation`](internal/conversation/) | Different responsibilities of long-term facts, execution history, and active context |

This chain describes Agent mode; simple chat and direct media generation use separate paths. [`assets.go`](assets.go) embeds resources. `internal/app/` assembles the host, and independent domain rules belong in their `internal/*` packages. Modularization is still in progress.

Read the [full English walkthrough](docs/architecture/code-walkthrough.en.md) ([简体中文](docs/architecture/code-walkthrough.md)), then consult the [detailed request lifecycle, Chinese](internal/README.md).

## Data and deployment

- **Local persistence:** SQLite is the default without MySQL; the workspace database is stored under `.openkitty/`.
- **Service deployment:** MySQL is configurable. Docker Compose, persistent service, and Kubernetes examples are available.
- **Deliverables:** Documents, attachments, knowledge files, and some compatibility records use the filesystem. Back up the workspace and configuration together.
- **Platforms and dependencies:** Corresponding Windows, Linux, and macOS builds are supported. Browsers, desktop interaction, OCR, Office/PDF conversion, and some tools require additional runtimes.
- **Extensions:** Configure MCP, model services, channels, and external CLIs as needed; account and provider availability affect these features.

The main application is a Go executable; full task environments can require external tools. Docker builds run the quality gate and are affected by the current test baseline.

Deployment: [AgentOS](docs/agentos-quickstart.md), [Kubernetes](docs/kubernetes.md), [Dockerfile](Dockerfile), [Docker Compose](docker-compose.yml).

## Development and validation

Run from the repository root:

```bash
# Release input checks and main build
go run ./cmd/releaseguard
go build -buildvcs=false -trimpath ./cmd/openkitty

# Focused tests for the changed area
go test ./internal/agentos -count=1

# Full test suite
go test ./... -count=1
```

Full quality gates aligned with CI:

```powershell
# Windows
.\scripts\quality_gate.ps1
```

```bash
# Linux / macOS
bash scripts/quality_gate.sh
```

After editing native frontend scripts, at least check every changed script:

```bash
node --check web/app.js
node --check web/chat.js
```

**The repository is still undergoing modularization.** Some older tests have known package-boundary, import-cycle, and obsolete-symbol failures; the full suite is not a green baseline. See [`AGENTS.md`](AGENTS.md). Record main builds, focused tests, and real-task acceptance separately.

Preserve workspace and user data, respect package boundaries, and report checks actually performed. State unresolved failures, missing dependencies, and checks not run.

Read [contribution guidance](CONTRIBUTING.md). Evaluation entry points: [capability evaluation plan](docs/engineering/agent-capability-evaluation-plan.md), [repeatability baseline](testdata/blackbox/AGENT_BASELINE.md).

## Documentation

The English introduction, positioning comparison, and core code walkthrough are maintained alongside Chinese versions. Most deeper existing manuals and architecture documents are currently in Chinese.

| Topic | Documentation |
| --- | --- |
| Chinese project introduction | [README](README.md) |
| Positioning and comparisons | [English](docs/product-positioning.en.md) / [简体中文](docs/product-positioning.md) |
| Core code walkthrough | [English](docs/architecture/code-walkthrough.en.md) / [简体中文](docs/architecture/code-walkthrough.md) |
| Getting started and daily use | [User manual](docs/macs-user-manual.md) |
| Documentation index | [Documentation](docs/README.md) |
| Business integration | [AgentOS quick start](docs/agentos-quickstart.md), [API](docs/agent-os-api.md), [OpenAPI](docs/openapi-agentos.yaml) |
| Memory and forgetting | [Memory service](docs/architecture/memory-service.md) |
| Document templates | [Document automation](docs/architecture/document-automation.md) |
| Browser, desktop, and reusable actions | [Computer use](docs/architecture/computer-use.md) |
| Pipelines and teams | [Pipeline guide](docs/architecture/PIPELINE_GUIDE.md), [team templates](docs/architecture/team-templates.md) |
| Runtime recovery | [Runtime reliability](docs/architecture/runtime-reliability.md) |
| Package boundaries | [Go modularization](docs/architecture/go-modularization.md), [detailed lifecycle](internal/README.md) |
| Build and validation | [Scripts](scripts/README.md) |

## License and contributions

macs uses **MACS Source Available License 1.0**. The license grants specified rights for personal non-commercial use, teaching, non-commercial research, evaluation, and internal use by qualifying small businesses, subject to its terms.

Other production commercial use, hosting for third parties, redistribution, OEM, and white-label use require the applicable commercial authorization. Third-party components retain their own licenses.

- [Full license](LICENSE)
- [Commercial licensing](COMMERCIAL-LICENSE.md)
- [Copyright and notices](NOTICE)
- [Third-party sources, license texts, and outstanding review items](THIRD-PARTY-NOTICES.md)
- [Contributing](CONTRIBUTING.md)
- [Trademarks](TRADEMARKS.md) and [branding](BRANDING.md)

Report problems and feature requests through [Issues](https://github.com/niqinggood/macs/issues), with the version, environment, reproduction steps, and redacted logs.
