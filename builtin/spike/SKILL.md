---
name: spike
description: "Throwaway experiments to validate an idea before committing to a real build. Use when user wants to test feasibility, compare approaches, or prototype."
read_when:
  - Testing if an approach is feasible
  - Comparing tools or libraries
  - Quick prototype before full implementation
  - User says "spike", "try it out", "prototype", "proof of concept"
version: 1.0.0
author: OpenKitty
status: stable
---

# Spike

Throwaway experiments to validate ideas. Spikes are disposable by design — throw them away once they've paid their debt.

## IRON RULES (non-negotiable)

1. **Timebox strictly**: A spike must not exceed 2 hours of active work. If it takes longer, it is no longer a spike — it is a build in disguise. Kill it and start a proper project.
2. **Delete after verdict**: Spike code is throwaway by design. Once the verdict is written, the code should be discarded. Never copy-paste spike code into the real build — the spike skipped error handling, testing, and architecture on purpose.
3. **One question per spike**: Each spike validates exactly one feasibility question. Compound questions produce ambiguous verdicts and waste time. If the idea has 3 unknowns, run 3 separate spikes.
4. **No production polish**: Hardcode everything. Skip error handling, skip tests, skip config files. The goal is a verdict, not a deployable artifact. Polish is a sign the spike went on too long.
5. **Verdict in writing, or it didn't happen**: Every spike MUST end with a README.md containing a VERDICT section (VALIDATED / PARTIAL / INVALIDATED). No verdict means the spike delivered zero value.

## Core Method

```
decompose → research → build → verdict
```

### 1. Decompose

Break the idea into 2-5 independent feasibility questions. Order by risk — the spike most likely to kill the idea runs first.

### 2. Research (per spike)

Before building:
1. Brief: 2-3 sentences on what and why
2. Compare approaches if multiple tools/libraries exist
3. Pick one approach

Use: `web_fetch` to find candidates, `read_file` on library docs, `cli` to check installed versions.

### 3. Build

One directory per spike, kept standalone:

```
spikes/
  001-websocket-streaming/
    README.md
    main.py
  002a-pdf-parse-pdfjs/
    README.md
    parse.js
```

Build something the user can interact with — a CLI, a test, a minimal web page. Not just a log line. Hardcode everything, avoid complex setup.

### 4. Verdict

Each spike's README.md closes with:

```markdown
## Verdict: VALIDATED | PARTIAL | INVALIDATED

### What worked
- ...

### What didn't
- ...

### Recommendation for the real build
- ...
```

**VALIDATED** = core question answered yes, with evidence.
**PARTIAL** = works under constraints, document them.
**INVALIDATED** = doesn't work — this is a SUCCESSFUL spike (prevented a bad path).

## Comparison Spikes

When comparing approaches (002a/002b), build back-to-back, then head-to-head:

```markdown
| Dimension | Approach A | Approach B |
|-----------|-----------|-----------|
| Quality | 9/10 | 7/10 |
| Setup | easy | complex |
| Performance | fast | slow |
```

## Anti-Patterns

| # | Anti-Pattern | Why It's Bad | Correct Approach |
|---|-------------|--------------|-----------------|
| 1 | Turning a spike into the real build | Carries prototype debt (no tests, no error handling, hardcoded values) into production | Throw away the spike, start fresh for the real build |
| 2 | Running a spike without a clear feasibility question | Produces code but no verdict — the spike failed its only purpose | Define exactly one question, write it in the README before writing code |
| 3 | Polishing spike code (adding tests, config, error handling) | Wastes time on code that will be thrown away; delays the verdict | Hardcode everything, the only output that matters is the verdict |
| 4 | Skipping the comparison spike when choosing between tools/libraries | Biased tool selection based on familiarity, not evidence | Build back-to-back comparison spikes (002a/002b) with a head-to-head table |
| 5 | Running spikes sequentially when they are independent | Wastes calendar time; each spike blocks the next | Run independent feasibility questions as parallel spikes |

## Output

- Create `storage/spikes/` directory
- One sub-directory per spike: `NNN-descriptive-name/`
- README.md per spike with question, approach, results, verdict
- Code is throwaway — don't clean up for production

## Pre-Action Checklist

- [ ] Understand the user's idea and identify the core feasibility question(s)
- [ ] Decompose into 2-5 independent spikes, ordered by risk (riskiest first)
- [ ] Write a one-sentence brief for each spike before touching any code
- [ ] Create the spike directory under `storage/spikes/`
- [ ] Build the minimal thing that answers the question — nothing more
- [ ] Write the verdict in README.md and present it to the user
