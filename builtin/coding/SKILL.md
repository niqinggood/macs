---
name: coding
version: 1.0.0
author: OpenKitty
status: stable
description: Coding skill — code-developer, requesting-code-review, systematic-debugging, test-driven-development, understand-codebase.
---

# Coding Skill

## IRON RULES (non-negotiable)

1. **No ad-hoc edits**: Every code change follows the structured workflow — understand requirements, plan approach, TDD implementation, independent review, verify completion. No cowboy coding.
2. **Root cause before fix**: Never patch symptoms. Complete systematic debugging Phase 1 (diagnosis) before proposing any solution. Symptom fixes create new bugs.
3. **Test-driven development**: No implementation code without a failing test first. RED → GREEN → REFACTOR cycle is mandatory for all new features and bug fixes.
4. **Independent review required**: No agent verifies its own work. Fresh context catches what familiarity misses. All changes must pass a separate review pass.
5. **Security scan mandatory**: Hardcoded secrets, shell injection vectors, and unsafe patterns must be caught and resolved before any commit.
6. **Understand before changing**: Read the surrounding code, map dependencies, and assess downstream impact before modifying any function or module.

---

## Sub-Skills

### code-developer
# Code Developer

## Overview

Code Developer is the central orchestrator for all programming tasks. It routes work through a structured pipeline: understand requirements → plan approach → implement with TDD → review quality → verify completion. It delegates to specialized skills at each phase.

**Core principle:** Every code change follows a disciplined workflow. No ad-hoc edits, no untested code, no unreviewed changes.

## Workflow Decision Tree

```
User asks to build/fix/refactor something
 
... (full details in sub-directory)

### requesting-code-review
# Pre-Commit Code Review

No agent should verify its own work. Fresh context finds what you miss.

## Step 1: Get the diff

```bash
git diff --cached
```

If empty, try `git diff` then `git diff HEAD~1 HEAD`. If still empty, nothing to verify.

## Step 2: Static security scan

```bash
# Hardcoded secrets
git diff --cached | grep "^+" | grep -iE "(api_key|secret|password|token)\s*=\s*['\"][^'\"]{6,}['\"]"

# Shell injection
git diff --cached | grep "^+" | grep -E "os\.system\(|subprocess.*shell=T
... (full details in sub-directory)

### systematic-debugging
# Systematic Debugging

## Overview

Random fixes waste time and create new bugs. Quick patches mask underlying issues.

**Core principle:** ALWAYS find root cause before attempting fixes. Symptom fixes are failure.

## The Iron Law

```
NO FIXES WITHOUT ROOT CAUSE INVESTIGATION FIRST
```

If you haven't completed Phase 1, you cannot propose fixes.

## When to Use

Use for ANY technical issue: test failures, bugs in production, unexpected behavior, performance problems, build failures, integrati
... (full details in sub-directory)

### test-driven-development
# Test-Driven Development

## Overview

TDD creates reliable code through a disciplined RED → GREEN → REFACTOR cycle. Every line of production code is backed by a test that was written first.

**Core principle:** You cannot write implementation code until you have a failing test that demands it.

## The TDD Cycle

```
RED    → Write a failing test
GREEN  → Write minimal code to pass
REFACTOR → Clean up while keeping green
REPEAT
```

## When to Use

**Always use when:**
- Writing new features
- 
... (full details in sub-directory)

### understand-codebase
# Understand Codebase

## 适用场景

- 接手新项目，快速理解代码结构
- 修改核心模块前，分析影响范围
- 生成新人入职指南
- 提取业务域概念和术语
- 为项目自动生成 CLAUDE.md

## 7 Agent 流水线

```
Phase 1: project-scanner     → 项目结构扫描（1 agent）
Phase 2: file-analyzer       → 并行文件分析（5并发，每批≤30文件）
Phase 3: architecture-analyzer → 架构分层识别（1 agent）
Phase 4: domain-analyzer     → 业务域概念提取（1 agent）
Phase 5: tour-builder        → 学习路径生成（1 agent）
Phase 6: graph-reviewer      → 质量审核（1 agent）
Phase 7: article-analyzer    → 文档/知识库图谱化（1 agent）
```

Phase 2 并发控制：最多 5 agent 并行，
... (full details in sub-directory)

---

## Anti-Patterns (DO NOT DO)

| # | Anti-Pattern | Why It Fails | Correct Behavior |
|---|-------------|--------------|-----------------|
| 1 | Jumping to a fix without completing root cause analysis | Symptom patches mask the real bug and create unpredictable side effects | Complete systematic debugging Phase 1; understand the full causal chain before touching code |
| 2 | Writing implementation before writing tests | Untested code is unverified code; regressions become invisible until production | Follow RED → GREEN → REFACTOR strictly; no production code without a failing test first |
| 3 | Self-reviewing your own code changes | Cognitive bias blinds you to your own mistakes and blind spots | Always request a separate review pass with fresh context before merging |
| 4 | Making isolated changes without reading callers and dependencies | Side effects silently break unrelated functionality that depends on the modified code | Map the full dependency graph and all call sites before editing any function signature or behavior |
| 5 | Skipping the pre-commit security scan | Secrets in code, injection vectors, and unsafe patterns ship directly to production | Run the static security scan as a mandatory gate; never skip it for "trivial" changes |

---

## Pre-Output Checklist
- [ ] All code changes follow the structured workflow (understand → plan → TDD → review → verify)
- [ ] Root cause identified before any fix applied
- [ ] Tests written and passing before implementation considered complete
- [ ] Independent code review completed (not self-reviewed)
- [ ] Security scan passed — no hardcoded secrets, no injection vectors
- [ ] Dependencies and downstream impacts assessed
- [ ] Output saved to `.openkitty/storage/`

## Output
All output: `.openkitty/storage/`
