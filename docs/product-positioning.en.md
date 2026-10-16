# macs positioning and comparison

[简体中文](product-positioning.md) | [English](product-positioning.en.md) · [Project introduction](../README.en.md)

Reviewed on 2026-10-16 against the current macs source and official public product information. This document distinguishes product direction from demonstrated outcomes. No controlled comparison benchmark has been run.

## Product position

**macs is a self-hostable Agent work system that brings natural-language goals, tool execution, recoverable workflows, result verification, and reusable experience into one runtime. Its implementation can be adapted to a business under the applicable license.**

Consider an ongoing business task: read operational data, identify missing information, fill an existing report template, check fields and files, and return task status and deliverables to the calling application. Later batches can reuse reviewed template rules and workflows. User preferences are maintained separately through memory.

Networking, coding, RAG, MCP, and multiple agents supply capabilities. The product value is how these capabilities complete a task together, which outcomes can be checked, and how successful methods can be used again.

**openkitty is the nickname and historical compatibility identity of macs in this repository. OpenClaw is a separate project.** Comparing macs with an older openkitty release requires specific version evidence instead.

## Comparison with other products

| Product | Direction described by official sources | Existing macs characteristics to consider | Boundaries of the comparison |
| --- | --- | --- | --- |
| OpenClaw | A personal or team assistant with a Gateway, messaging channels, and extensible models and tools | A Go executable with an embedded workbench, business Pipelines, durable AgentOS tasks, DOCX template profiles, and workflow reuse | OpenClaw also supports self-hosting, memory, and extensions. These are shared capabilities. [Official repository](https://github.com/openclaw/openclaw) |
| WorkBuddy | An office workbench with expert collaboration and ecosystem integrations; Enterprise also offers OpenAPI and private deployment | Inspect and adapt the implementation within its license, and assemble models, tools, document processing, and business task contracts | WorkBuddy has execution and enterprise integration capabilities. [Product](https://cloud.tencent.com/product/workbuddy), [Enterprise](https://cloud.tencent.com/product/workbuddy-enterprise) |
| Hermes Agent | Persistent memory, past-session search, and creating and improving skills from experience | Combine memory with workflow contracts, reusable document mappings, and UI recipes with success assertions | Learning from experience is a stated Hermes focus. Better macs memory or learning results have not been demonstrated. [Official repository](https://github.com/NousResearch/hermes-agent) |
| Codex | A reusable Agent harness with CLI, SDK, and app-server integration surfaces | An assembled application with a workbench, document engine, memory, messaging channels, and business orchestration | Codex can also be embedded in business products. [Official OpenAI explanation](https://developers.openai.com/blog/codex-as-a-platform) |

These are positioning judgments based on the cited material. macs is a candidate for teams that want to operate their own deployment, adapt business implementations, and connect Agent tasks to existing systems. Suitability depends on actual tasks and integration requirements.

## Implementation evidence

| Characteristic | Existing mechanism | Source and documentation |
| --- | --- | --- |
| Personal and business entry points share execution | The host connects Web, AgentOS, Pipeline, and AI Employee to the core Agent | [`internal/app/`](../internal/app/), [request lifecycle](../internal/README.md) |
| Durable execution state | Task records, tool receipts, leases, and session and step checkpoints | [`internal/agentos/`](../internal/agentos/), [`internal/session/`](../internal/session/), [runtime reliability](architecture/runtime-reliability.md) |
| Contracts for repeatable steps | Pipeline input/output contracts, rework, and recovery | [`internal/pipeline/`](../internal/pipeline/), [Pipeline guide](architecture/PIPELINE_GUIDE.md) |
| Model interpretation plus deterministic filling | The Agent proposes mappings; the DOCX engine checks structure, fills, verifies, and reuses profiles by template fingerprint | [`internal/tools/content/`](../internal/tools/content/), [document automation](architecture/document-automation.md) |
| Successful actions become reusable recipes | Compile parameters, actions, and assertions from a successful `computer` Session; replay uses the same execution entry point | [`internal/workflowlearn/compiler.go`](../internal/workflowlearn/compiler.go), [computer use](architecture/computer-use.md) |
| Maintainable memory | Selective recall, corrections, forgetting, and source management | [`internal/memory/`](../internal/memory/), [memory service](architecture/memory-service.md) |
| Consolidated main application | A Go executable embeds the native Web UI, built-in Skills, and default configuration; SQLite is available locally | [`assets.go`](../assets.go), [getting started](../README.en.md#quick-start) |

The presence of these mechanisms does not establish superior task success, reliability, or cost.

## Accuracy boundaries

- Other products also have memory, multiple agents, MCP, Skills, networking, and self-hosting. Feature counts do not establish task quality.
- `workflow_learner` currently handles successful `computer` sessions. It does not automatically turn every conversation, coding task, or business workflow into a useful skill.
- Verification only supports the checks actually executed and their evidence. A file existing or a command succeeding does not establish every business requirement.
- Recovery follows checkpoint and receipt boundaries. Writes with uncertain outcomes must not be replayed automatically, and SSE does not replace durable task snapshots.
- The main executable embeds resources, but models, browsers, Office/PDF conversion, and external services still have dependencies. Uniform double-click desktop packaging is not complete across all three platforms.
- Known test failures remain from the modularization migration. A successful main build does not mean the full test suite passes.
- macs uses a source-available license. Commercial use and distribution are governed by [`LICENSE`](../LICENSE); it is not an OSI open-source license or an unconditional commercial grant.

## How to demonstrate the advantages

Use the existing [business evaluation baseline](../testdata/blackbox/AGENT_BASELINE.md) to prioritize three scenarios: modifying and validating an existing code project, delivering consecutive batches with a fixed template, and submitting and tracking long-running business tasks. Control inputs, model conditions, acceptance criteria, and retry budgets. Record success, human interventions, repeated explanations, elapsed time, and actual charges.

For memory and workflow reuse, compare the first and later batches. Check whether repeated explanations decrease, current instructions override old preferences, forgotten items stop being recalled, and changed inputs still produce correct outputs. Comparative performance claims should follow these measurements.

Suggested introduction: **macs helps Agents deliver verifiable work and turn experience into reusable capabilities.**
