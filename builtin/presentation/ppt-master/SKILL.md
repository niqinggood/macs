---
name: ppt-master
description: "Run the complete bundled upstream PPT Master workflow for higher-end commercial, executive, client, investor, launch, or showcase decks when premium SVG-first visual storytelling is the main value, or when the user explicitly requests PPTmaster/PPTmaster-main/full Strategist-Executor execution. The upstream references, scripts, templates, LICENSE, and NOTICE are bundled under skills/ppt-master/."
read_when:
  - generator: pptmaster
  - generator=pptmaster
  - PPTmaster-main
  - explicit full upstream PPT Master route
---

# PPT Master Router

This is **Level 3 — premium custom**, the highest OpenKitty PPT production level. Selecting it commits the task to the complete upstream workflow and its premium QA; it must never be used as a decorative name for a local script or silently collapse to `base-ppt`.

This is the OpenKitty entry point for the upstream **PPT Master v2.2.0** project by LƯƠNG HẢI ANH (`luonghaianh1208/PPTmaster`, MIT).

The complete upstream skill is bundled at:

`<runtime-dir>/skills/ppt-master/SKILL.md`

Its role prompts are under `<runtime-dir>/skills/ppt-master/references/`, its scripts under `<runtime-dir>/skills/ppt-master/scripts/`, and its templates under `<runtime-dir>/skills/ppt-master/templates/`.

## Full upstream route

Use this Skill when the user explicitly requests PPTmaster/full upstream execution or when `ppt-art-director` selects it as the higher-end commercial route after separating template intent from renderer choice. Premium executive, board, client, investor, sales, brand, launch, or showcase work should prefer PPTMaster when full SVG-first production is appropriate, content is sufficiently frozen, and strict native-template preservation is unnecessary.

PPTMaster is a rendering workflow, not a native PPTX template. It is a strong autonomous candidate when visual storytelling dominates, content is frozen, strict preservation of an existing PPTX master is unnecessary, grouped/native SVG editing is acceptable, and the task is not an explicit fast draft. A `strict-existing` template intent must stay on a native-template route. A `style-reference` deck may inform PPTMaster's design specification, but the result is a restyle rather than native-template preservation.

After this route is selected, read the bundled upstream `SKILL.md`, `references/shared-standards.md`, `references/strategist.md`, and the one relevant Executor reference before generating slides. Do not replace any upstream role with a local renderer while continuing to call the result PPTmaster.

## Explicit override

If the user explicitly specifies any of the following:

- `generator: pptmaster`
- `generator=pptmaster`
- `pptmaster`
- `PPTmaster-main`
- “走 PPTmaster”

then:

1. Call `use_skill ppt-master` and use its returned Runtime Directory.
2. Read and follow the complete upstream skill from that runtime, including Strategist and the relevant Executor role.
3. Use the bundled runtime; never depend on a user workspace `reference/` directory.
4. Keep source material, project files, SVG, notes, and final PPTX inside `.openkitty/storage/`.
5. Run source/project/template/strategy/executor/quality/finalize/export stages in order. Do not substitute generic architecture slides for a business, investment, medical, or other domain deck.
6. State that the `upstream PPT Master workflow` actually ran. Never apply that label to a local OpenKitty renderer.

## Route boundary

- Generic PPT requests belong first to `ppt-art-director`, which records template intent separately and chooses `base-ppt`, a task-specific native renderer, PPTMaster, or a visual-first route according to content, beauty, editability, template fidelity, and speed.
- Registered PPTMaster templates are SVG layout families plus `design_spec.md`; they are not interchangeable with native `.pptx` slide masters. Keep that distinction explicit in the handoff.
- This skill always means the complete upstream Source → Project → Template → Strategist → Executor → quality → finalize → export sequence.
- If the upstream workflow cannot run, do not silently substitute another renderer under the PPTmaster name. Report the concrete blocker and offer the task-specific native renderer first, then `base-ppt` as the fast neutral fallback, under separately named routes.

## Required QA

- Complete the upstream quality, finalize, and export stages; speed is not a reason to skip them.
- Render or preview the exported deck and inspect every slide for overflow, clipping, weak hierarchy, inconsistent margins, low contrast, and unsupported factual graphics.
- Fix discovered defects and re-render the affected slides before delivery.
- Verify the final PPTX exists, opens, has the expected slide count, and remains under the task's `.openkitty/storage/` path.

## OpenKitty automation profile

The upstream skill normally pauses for template selection and eight confirmations. In this product, the user has asked for an agentic, fast workflow. After PPTMaster has been explicitly requested or autonomously selected, when the source, audience, deliverable, and style are already inferable, record recommended defaults in `design_spec.md` and continue autonomously. Ask only when a missing choice would materially change the deliverable.

This OpenKitty wrapper policy overrides the upstream confirmation pauses only. Treat inferable template/style/layout choices as approved, while still completing every source, project, strategy, Executor, quality, finalize, and export stage. Do not omit quality stages in the name of speed.

This wrapper overrides the upstream `--move` source rule for product safety. Import user-provided files with `project_manager.py import-sources` **without** `--move`, so the project receives a copy and the original remains untouched. If an internal workflow already created a task-private staging copy, moving that copy is harmless, but never move, rename, overwrite, or delete the user's original file.

Before the first Python stage, perform one bounded dependency check for the modules actually needed by that task. If a core import is missing, install once from `<runtime-dir>/requirements.txt`; do not enter an install/retry loop. Optional image or web dependencies need not block a Markdown-to-PPTX task that does not use them.

For an existing deck polish request, preserve the old PPTX, create a new project/output name, reuse the frozen source, and run the visual workflow once. Do not restart research, call live-data tools, or manufacture new factual charts merely to decorate the deck.

## Execution note

The runnable workflow and scripts are bundled under the OpenKitty builtin skill:

`builtin/presentation/ppt-master/skills/ppt-master/`

The upstream PPTmaster-main reference tree is a development reference source only. Runtime tasks must not depend on any `reference/` directory existing in the user workspace.

Before executing manually, use the bundled builtin copy and keep all generated project/output files under `.openkitty/storage/`.

## Use this route for

- explicit `generator: pptmaster` or `generator=pptmaster`
- explicit `pptmaster`, `PPTmaster-main`, or “走 PPTmaster” requests
- explicit requests to run the complete upstream Strategist/Executor workflow
- an autonomous `ppt-art-director` handoff that names PPTMaster and records why SVG-first premium production fits better than a native task-specific renderer

## Fallback rule

- Do not silently downgrade an explicit PPTmaster request.
- Report the concrete upstream failure and preserve all source/project evidence.
- Offer `base-ppt` or a task-specific renderer only as a clearly separate OpenKitty route; never present its output as PPTmaster.
