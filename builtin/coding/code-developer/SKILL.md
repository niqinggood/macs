---
name: code-developer
description: "Orchestrator for software development: routes tasks through the right workflow (spec→plan→TDD→review→verify). Use for writing, debugging, refactoring, or testing code in any language."
read_when:
  - Writing new code or features
  - Fixing bugs
  - Refactoring existing code
  - User asks to "build", "implement", "code", "develop"
  - Multi-step development tasks
  - Before committing or pushing code
---

# Code Developer

## Overview

Code Developer is the central orchestrator for all programming tasks. It routes work through a structured pipeline: understand requirements → plan approach → implement with TDD → review quality → verify completion. It delegates to specialized skills at each phase.

**Core principle:** Every code change follows a disciplined workflow. No ad-hoc edits, no untested code, no unreviewed changes.

## Workflow Decision Tree

```
User asks to build/fix/refactor something
              │
              ▼
     ┌─ Is this a bug fix? ──────┐
     │ YES                        │ NO
     ▼                            ▼
  Use systematic-debugging    Requirements clear?
  (root cause FIRST)          │ YES           │ NO
     │                        ▼               ▼
     │                   Use plan-mode    Ask clarifying
     │                   to write spec    questions first
     │                        │
     └────────────┬───────────┘
                  ▼
         ┌─ Are tests needed? ──┐
         │ YES (always for      │ RARELY
         │ new code)            │
         ▼                      ▼
    Use test-driven-       Implement directly
    development            (small, obvious fixes)
         │                      │
         └──────────┬───────────┘
                    ▼
           Use requesting-code-review
           (pre-commit quality gates)
                    │
                    ▼
           Verify: tests pass, build succeeds,
           behavior matches requirements
                    │
                    ▼
                  Done
```

## Phase 1: Understand

Before writing code, understand what's needed.

**For bug fixes:** Use `systematic-debugging` skill. Root cause investigation is MANDATORY before any fix. Never skip Phase 1 of that skill.

**For new features:** Read the requirements carefully. If anything is ambiguous, ask the user. Don't guess.

**For refactoring:** Identify what's changing and why. Run existing tests first to establish baseline.

**Action:** Use `read_file`, `grep`, `glob`, `lsp_hover`, `lsp_references` to understand the codebase before touching it.

## Phase 2: Plan

Write a concrete, actionable plan before writing code.

**When to plan:**
- Multi-file changes (3+ files)
- New features
- Architectural changes
- Refactoring

**When to skip planning:**
- Single-line fixes
- Typo corrections
- Trivial configuration changes

**Action:** Use `plan-mode` skill. Save plan to `storage/plans/`. Include exact file paths, complete code, TDD steps per task, verification commands.

**If a plan already exists:** Review it critically before executing. Flag gaps, unclear steps, missing edge cases.

## Phase 3: Implement

Write code following strict discipline.

### TDD Path (default for all new code)

Use `test-driven-development` skill. The RED-GREEN-REFACTOR cycle is non-negotiable:

```
RED:    Write test → verify it FAILS
GREEN:  Write minimal code → verify it PASSES
REFACTOR: Clean up → verify still PASSES
COMMIT
```

**Rules:**
- Never write implementation before test
- Never write more code than the test demands
- Never skip "verify it fails" step
- One assertion per test (generally)
- Commit after each tiny increment

### Direct Implementation Path (rare exceptions only)

Only for: typo fixes, config changes, single-line obvious corrections. Still verify after.

### Parallel Work

For independent tasks, use `sub_agent` or `parallel_sub_agent` tools:
- Each sub-agent gets one task with exact instructions
- Sub-agents must follow the same TDD discipline
- Review sub-agent output before merging

## Phase 4: Review

No agent should verify its own work. Use `requesting-code-review` skill:

1. **Get the diff:** `git diff --cached` (or `git diff` if unstaged)
2. **Security scan:** Check for secrets, injection risks, dangerous patterns
3. **Self-review checklist:** Hardcoded secrets, input validation, error handling, debug prints, commented-out code
4. **Tests and linting:** Run language-appropriate test suite and linter
5. **Independent review:** Spawn sub_agent to review diff with fresh context

**Fix any issues found before proceeding.** Re-run tests after fixes.

## Phase 5: Verify

Prove the change works before declaring done.

### Verification Checklist

- [ ] All tests pass: `go test ./...` / `pytest` / `npm test`
- [ ] Build succeeds: `go build` / `npm run build` / `cargo build`
- [ ] No new lint warnings
- [ ] Behavior matches requirements (manual test if needed)
- [ ] No regressions in existing functionality

### Verification Commands

```bash
# Go
go test ./... -count=1
go build ./...
go vet ./...

# Python
pytest -v
python -m mypy src/
ruff check .

# Node
npm test
npm run build
npx eslint .

# Rust
cargo test
cargo build --release
cargo clippy
```

**Never claim completion without a preceding verification tool call.** "Should work" without evidence is not completion.

## Language-Specific Patterns

### Go

- Always run `gofmt` before committing
- Use table-driven tests: `func TestXxx(t *testing.T) { tests := []struct{...}{} }`
- Error values, not exceptions: `if err != nil { return fmt.Errorf("context: %w", err) }`
- One package per directory. No circular imports.
- Accept interfaces, return structs.
- Use `context.Context` for cancellation and deadlines.

### Python

- Type hints on all function signatures (`def foo(x: int) -> str:`)
- Pytest with fixtures for test setup
- Virtual environment (`venv`) isolation
- `pathlib` for paths, not `os.path`
- Data classes over raw dicts for structured data

### TypeScript/JavaScript

- Prefer `const` over `let`, never `var`
- Async/await over raw promises
- Strict TypeScript: no `any` without justification
- Test with Jest/Vitest

### Rust

- `cargo fmt` and `cargo clippy` before commit
- Use `Result<T, E>` and `Option<T>` — no unwrap without justification
- Tests in same file with `#[cfg(test)]` module
- Derive macros over manual impls where possible

## Red Flags — STOP and Reconsider

If you catch yourself:
- Writing code without a plan → STOP. Plan first.
- Writing implementation before test → STOP. Write test first.
- "Just one quick fix" → STOP. Root cause first (use systematic-debugging).
- Adding features not requested → STOP. YAGNI. Minimum code only.
- "I'll add tests later" → STOP. Tests now or never.
- "This is too simple to test" → STOP. Simple code has edge cases.
- Skipping verification → STOP. Run the tests.
- Claiming done without running tests → STOP. Run verification first.
- Adding abstraction "for the future" → STOP. YAGNI.

## Tools Reference

| Phase | Tools |
|-------|-------|
| **Understand** | `read_file`, `grep`, `glob`, `lsp_hover`, `lsp_references`, `lsp_symbols`, `lsp_call_hierarchy` |
| **Plan** | `plan_task`, `update_plan_step`, `write_file` (to storage/plans/) |
| **Implement** | `edit_file`, `write_file`, `read_file`, `grep`, `glob` |
| **Check** | `code_check` (syntax/lint/format/build), `run_tests`, `build` |
| **Debug** | `debug` (error analysis), `cli` (run commands), `systematic-debugging` skill |
| **Review** | `code_review`, `git` (diff/log), `sub_agent` (independent review) |
| **Refactor** | `refactor` (analyze), `edit_file`, `lsp_definition`, `lsp_references` |

## Remember

```
UNDERSTAND → PLAN → TDD → REVIEW → VERIFY

No untested code.
No unreviewed changes.
No unverified completion.
No skipped phases.
```
